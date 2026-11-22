// The manager's database: reading the stored devices from it, and following it when it is replaced, which is what
// restoring a backup does.

package device

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"log"
	"maps"
	"strings"
	"time"
)

// storedDevices reads the devices of a database as registry entries: offline, each with the manifest it last sent.
func storedDevices(db *model.Database) (map[string]*device, error) {
	devices := make(map[string]*device)
	if db == nil {
		return devices, nil
	}
	records, err := db.GetAllDevices()
	if err != nil {
		return nil, fmt.Errorf("failed to load the devices: %w", err)
	}
	for _, record := range records {
		dev := newDevice(record)
		if record.Manifest != "" {
			manifest := new(Manifest)
			if err := json.Unmarshal([]byte(record.Manifest), manifest); err == nil && manifest.Validate() == nil {
				dev.manifest = manifest
			}
		}
		devices[record.DeviceId] = dev
	}
	return devices, nil
}

// Setup is where the manager stores and what its bindings apply to.
type Setup struct {
	Database *model.Database // Where devices and binding sets are stored.
	Profile  string          // The field profile whose bindings apply; empty for the default one.
	Game     *gamedef.Game   // The game the bindings apply to.
	Stations []string        // The ids of the field's stations.
}

// Configure makes the given database, field profile and game the ones in use, in one step: the binding set is
// read once, for all three. The arena calls it whenever its settings are loaded and whenever its game changes.
// With the database the manager already has, it is SetGame and SetProfile. With another one, because a backup was
// restored, the devices and the binding set of that database are in use when it returns:
//
//   - A device the new database has is what that database says: its state, its token, its name, its notes, whether
//     it is required. What no database holds is kept: its connections, its last inputs and outputs, its heartbeat.
//     While it is connected its manifest is kept too, and the running totals of its counters are those of whoever
//     heard from it last, which is the manager if it is connected: an older total would count again what was
//     already counted.
//   - A connected device the new database does not have becomes pending again, as a new device would, with the
//     token it connected with.
//   - A device that is neither connected nor in the new database is forgotten, and its outputs are cleared.
//   - A device whose state or token changed is reported to the transports, which make it connect again with the
//     rights it now has.
//   - Nothing about safety is reset. Faults stay latched and stops stay held: see restoreFaults. A device that was
//     approved and no longer is faults every stop bound to it, as blocking it does, because it can no longer stop
//     anything.
//
// The profile and the game come with the database because the restored event may use other ones, and a binding
// set put in force on the way to the right one would drop the faults of the stops it does not have.
//
// If the new database cannot be read, an error is returned and nothing changes, the game and the profile included:
// the bindings in force, and with them the stops and the faults, stay whole until a database can be read. The next
// call tries again. The caller may hold the arena's lock; the database is read with storeMu held and mu not.
func (m *Manager) Configure(setup Setup) error {
	profile := setup.Profile
	if profile == "" {
		profile = model.DefaultFieldProfile
	}
	stations := append([]string(nil), setup.Stations...)
	return m.follow(setup.Database, func() {
		m.game, m.stations, m.profile = setup.Game, stations, profile
	})
}

// SetDatabase makes db the database in use, as Configure does, for the game and the field profile the manager
// already has.
func (m *Manager) SetDatabase(db *model.Database) error {
	return m.follow(db, func() {})
}

// follow does what Configure says. retarget sets the game and the profile the caller wants; it is run with mu held.
func (m *Manager) follow(db *model.Database, retarget func()) error {
	// storeMu is held until the new database is in use: no device is being written, and no binding set read or
	// saved, while one database gives way to the other.
	m.storeMu.Lock()
	if db == m.db {
		m.mu.Lock()
		game, profile := m.game, m.profile
		retarget()
		changed := m.game != game || m.profile != profile
		m.mu.Unlock()
		m.storeMu.Unlock()
		if changed {
			m.loadBindings()
		}
		return nil
	}
	stored, err := storedDevices(db)
	if err != nil {
		m.storeMu.Unlock()
		return err
	}

	m.mu.Lock()
	retarget()
	for {
		game, profile := m.game, m.profile
		m.mu.Unlock()
		set, loadError := readBindings(db, game, profile)
		m.mu.Lock()
		if m.game == game && m.profile == profile {
			m.db, m.source, m.loadError = db, set, loadError
			break
		}
		// The game or the profile changed while the set was read: read the right one.
	}
	held := m.heldFaults()
	changes := m.adoptDevices(stored)
	m.recompile()
	m.restoreFaults(held)
	for _, id := range sortedKeys(changes.unapproved) {
		m.latchDeviceFaults(id, changes.unapproved[id])
	}
	devices, faults := len(m.devices), len(m.faults)
	m.unlockStore()

	log.Printf(
		"Devices: the database was replaced. Devices known now: %d. Faults latched: %d.%s", devices, faults,
		changes.describe(),
	)
	return nil
}

// unlockStore releases mu and storeMu, which the caller holds, and then carries out the work decided under mu. The
// work may store devices, which takes storeMu, so it cannot run before that is released.
func (m *Manager) unlockStore() {
	work := m.work
	m.work = deferredWork{}
	m.mu.Unlock()
	m.storeMu.Unlock()
	m.run(work)
}

// adoption is what adoptDevices did to the devices the manager had.
type adoption struct {
	pending    []string          // Connected, not in the new database: waiting for approval again.
	forgotten  []string          // Neither connected nor in the new database.
	unapproved map[string]string // Approved before and not now: why, in the words of a fault.
}

func (a *adoption) describe() string {
	var text string
	if len(a.pending) > 0 {
		text += fmt.Sprintf(
			" Connected but not in it, and waiting for approval again: %s.", strings.Join(a.pending, ", "),
		)
	}
	if len(a.forgotten) > 0 {
		text += fmt.Sprintf(" Not in it and not connected, so forgotten: %s.", strings.Join(a.forgotten, ", "))
	}
	return text
}

// adoptDevices makes the devices of the database that was just put in place the registry, keeping of the devices
// the manager had what no database holds, as Configure describes. The caller holds storeMu and mu, and compiles
// the bindings afterwards.
func (m *Manager) adoptDevices(stored map[string]*device) *adoption {
	now := m.now()
	changes := &adoption{unapproved: make(map[string]string)}
	for _, id := range sortedKeys(m.devices) {
		dev := m.devices[id]
		if dev.builtIn {
			// The PLC is not stored. It stays as it is.
			stored[id] = dev
			continue
		}
		wasApproved, state, tokenHash := dev.approved(), dev.record.State, dev.record.TokenHash
		switch known := stored[id]; {
		case known != nil:
			if m.adoptRecord(dev, known) {
				m.persist(id)
			}
			stored[id] = dev
			switch {
			case wasApproved && !dev.approved():
				changes.unapproved[id] = fmt.Sprintf("the restored database has it as %s", dev.record.State)
			case !wasApproved && dev.approved() && dev.online():
				// Its transport may drop it so that it connects again as an approved device; that is no fault.
				dev.approvedAt = now
			}
		case dev.online():
			// The token it connected with is kept, as it is for a new device: approving it pins that token.
			dev.record = model.Device{
				DeviceId: id, State: model.DevicePending, TokenHash: tokenHash, Manifest: dev.record.Manifest,
				CounterTotals: dev.record.CounterTotals, CreatedAt: now.UTC(), LastSeen: dev.record.LastSeen,
			}
			dev.approvedAt = time.Time{}
			stored[id] = dev
			m.persist(id)
			m.emit("device.pending", map[string]any{"id": id, "name": dev.name()})
			changes.pending = append(changes.pending, id)
			if wasApproved {
				changes.unapproved[id] = "the restored database does not have it, so it has to be approved again"
			}
		default:
			for _, channel := range sortedKeys(dev.outputs) {
				m.sendOutput(id, channel, nil)
			}
			changes.forgotten = append(changes.forgotten, id)
			if wasApproved {
				changes.unapproved[id] = "the restored database does not have it"
			}
		}
		if kept := stored[id]; kept == nil || kept.record.State != state || kept.record.TokenHash != tokenHash {
			m.broadcast(func(transport Transport) { transport.AccessChanged(id) })
		}
	}
	m.devices = stored
	return changes
}

// adoptRecord gives a device the manager already had the record a new database holds for it, and reports whether
// the record now differs from the stored one and so has to be written. The caller holds mu.
func (m *Manager) adoptRecord(dev *device, stored *device) bool {
	record, changed := stored.record, false
	if dev.online() && dev.manifest != nil {
		// What the device says it is now is newer than what a backup remembers of it.
		if record.Manifest != dev.record.Manifest {
			record.Manifest, changed = dev.record.Manifest, true
		}
	} else {
		dev.manifest = stored.manifest
	}

	// The totals of whoever heard from the device last, completed with the channels only the other knows.
	newer, older := record.CounterTotals, dev.record.CounterTotals
	if dev.online() || !dev.record.LastSeen.Before(record.LastSeen) {
		newer, older = older, newer
	}
	totals := make(map[string]float64, len(older)+len(newer))
	maps.Copy(totals, older)
	maps.Copy(totals, newer)
	if !maps.Equal(totals, record.CounterTotals) {
		changed = true
	}
	record.CounterTotals = totals
	if dev.record.LastSeen.After(record.LastSeen) {
		record.LastSeen = dev.record.LastSeen
	}
	dev.record = record
	return changed
}

// heldFault is a latched fault with the channel its stop binding reads.
type heldFault struct {
	ref   ChannelRef
	fault Fault
}

// heldFaults returns the latched faults before the binding set of another database is compiled. The caller holds
// mu.
func (m *Manager) heldFaults() []heldFault {
	var held []heldFault
	for _, binding := range m.set.stops {
		if fault := m.faults[binding.def.ID]; fault != nil {
			held = append(held, heldFault{ref: binding.ref, fault: *fault})
		}
	}
	return held
}

// restoreFaults keeps a restore from letting go of a stop that a fault holds. Compiling a binding set keeps the
// faults of the stop bindings it still has, by their ids, and drops the others. If the restored set has the stop
// under another id, on the same channel of the same device, the fault is latched on that binding, with the reason
// and the time it had. Only a fault whose channel the restored set binds to no stop at all is gone, and compiling
// has published device.fault_cleared for it. The caller holds mu.
func (m *Manager) restoreFaults(held []heldFault) {
	for _, was := range held {
		if m.faults[was.fault.Binding] != nil {
			continue
		}
		for _, binding := range m.set.stops {
			if binding.ref != was.ref || m.faults[binding.def.ID] != nil {
				continue
			}
			fault := was.fault
			fault.Binding = binding.def.ID
			m.faults[fault.Binding] = &fault
			m.emit(
				"device.fault", map[string]any{"id": fault.Device, "binding": fault.Binding, "reason": fault.Reason},
			)
		}
	}
}
