// The manager: the registry of devices, the binding set in force, and everything that ties devices to the match.

package device

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"log"
	"maps"
	"sort"
	"sync"
	"time"
)

// Scorer applies a change to the live score. The arena is the scorer; ApplyScoreChange must be safe to call from
// any goroutine.
type Scorer interface {
	ApplyScoreChange(change scoring.Change) (scoring.Change, error)
}

// Options are what a manager is built with.
type Options struct {
	Database *model.Database  // Where devices and binding sets are stored. Nil keeps everything in memory (tests).
	Bus      *bus.Bus         // Where device events are published. May be nil.
	Scorer   Scorer           // Where score actions go. May be nil, in which case they are refused.
	Now      func() time.Time // The clock; time.Now if nil. Tests replace it.
}

// MatchInfo is what input processing needs to know about the match. The arena sets it on every pass of its loop.
type MatchInfo struct {
	Running    bool   // A phase is running.
	Phase      string // The phase that is running.
	GracePhase string // An earlier phase that late input is still credited to (score_grace), if any.
	Scoring    bool   // The score accepts device input: the match is running or ended within the score grace.
}

// Manager keeps every field device, applies the binding set of the active game and field profile to what they
// send, and works out what their outputs show.
//
// Threading: mu guards everything below it. Transports call the Sink methods from their own goroutines, the arena
// calls Tick, Stops, StartConditions, TakeFieldActions and UpdateOutputs from its loop, and the web layer calls the
// rest. Nothing slow happens under mu: work that talks to the score, the bus, the transports or the database is
// collected while it is held and carried out after it is released (see unlock). Database writes are serialised by
// storeMu, which is always taken before mu and never while mu is held.
type Manager struct {
	db      *model.Database
	bus     *bus.Bus
	scorer  Scorer
	now     func() time.Time
	storeMu sync.Mutex

	mu            sync.Mutex
	devices       map[string]*device
	transports    []Transport
	started       bool
	startErrors   map[string]string
	game          *gamedef.Game
	stations      []string
	profile       string
	source        *BindingSet // The binding set in force, as stored.
	loadError     string      // Why the stored binding set could not be read, if it could not.
	set           *compiledSet
	match         MatchInfo
	faults        map[string]*Fault // By binding id.
	fieldActions  []FieldAction
	claims        map[ChannelRef]string
	tickHooks     []func(*Snapshot)
	lastField     *FieldState
	lastFieldTime time.Time
	refusalTimes  map[string]time.Time
	errorsLogged  map[string]bool
	plc           plcState
	work          deferredWork
}

// device is one device in the registry.
type device struct {
	record     model.Device
	manifest   *Manifest
	builtIn    bool                 // The PLC: not stored, always approved.
	presence   map[string]*Presence // By transport.
	status     Status
	statusTime time.Time
	inputs     map[string]*InputValue
	outputs    map[string]*Command // The command in force on each output channel.
	hbTime     time.Time           // The last heartbeat, or the time the device connected.
	hbSeq      int64
	refusal    *Refusal
	approvedAt time.Time // When it was last approved: its transport may drop it then, to reconnect it.
}

// A transport drops a device it has just approved so that it reconnects with its new rights (see
// Transport.AccessChanged). Losing the connection this long after the approval is that, not a fault.
const approvalReconnectGrace = 5 * time.Second

// reconnectingAfterApproval reports whether a lost connection is the one dropped because the device was approved.
func (dev *device) reconnectingAfterApproval(now time.Time) bool {
	return !dev.approvedAt.IsZero() && now.Sub(dev.approvedAt) < approvalReconnectGrace
}

// Presence is a device's connection over one transport.
type Presence struct {
	Remote string    `json:"remote"`
	Since  time.Time `json:"since"`
}

// InputValue is the last value an input channel reported.
type InputValue struct {
	Value any       `json:"value"`
	Conf  *float64  `json:"conf,omitempty"`
	Seq   *int64    `json:"seq,omitempty"`
	Time  time.Time `json:"time"`
}

// Refusal is the last thing the FMS would not do for a device: an input it would not apply, a message it could not
// read, a field action that failed.
type Refusal struct {
	Binding string    `json:"binding,omitempty"`
	Reason  string    `json:"reason"`
	Time    time.Time `json:"time"`
	Count   int       `json:"count"` // How many times in a row.
}

// Info is a copy of everything the FMS knows about a device, for the pages.
type Info struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	State      string                `json:"state"`
	BuiltIn    bool                  `json:"builtIn"`
	Required   bool                  `json:"required"`
	Notes      string                `json:"notes"`
	HasToken   bool                  `json:"hasToken"`
	Manifest   *Manifest             `json:"manifest"`
	Online     bool                  `json:"online"`
	Presence   map[string]Presence   `json:"presence"`
	LastSeen   time.Time             `json:"lastSeen"`
	Status     Status                `json:"status"`
	StatusTime time.Time             `json:"statusTime"`
	Inputs     map[string]InputValue `json:"inputs"`
	Outputs    map[string]*Command   `json:"outputs"`
	Heartbeat  time.Time             `json:"heartbeat"`
	Faults     []Fault               `json:"faults"`
	Refusal    *Refusal              `json:"refusal"`
	StopBound  bool                  `json:"stopBound"` // A stop is bound to one of its channels.
	CreatedAt  time.Time             `json:"createdAt"`
}

// Event payloads go on the bus once mu is released.
type busEvent struct {
	kind string
	data any
}

type pendingChange struct {
	change  scoring.Change
	device  string
	binding *inputBinding
}

// deferredWork is what was decided under mu and is carried out once it is released.
type deferredWork struct {
	sends   []func()
	events  []busEvent
	changes []pendingChange
	saves   []string
}

// NewManager builds a manager and loads the stored devices. Transports are added with AddTransport and started
// with Start.
func NewManager(options Options) (*Manager, error) {
	m := &Manager{
		db: options.Database, bus: options.Bus, scorer: options.Scorer, now: options.Now,
		devices: make(map[string]*device), faults: make(map[string]*Fault), claims: make(map[ChannelRef]string),
		refusalTimes: make(map[string]time.Time), errorsLogged: make(map[string]bool),
		startErrors: make(map[string]string), profile: model.DefaultFieldProfile, source: &BindingSet{},
	}
	if m.now == nil {
		m.now = time.Now
	}
	m.set = compile(m.source, Environment{}, nil)
	if m.db != nil {
		records, err := m.db.GetAllDevices()
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
			m.devices[record.DeviceId] = dev
		}
	}
	return m, nil
}

func newDevice(record model.Device) *device {
	if record.CounterTotals == nil {
		record.CounterTotals = make(map[string]float64)
	}
	return &device{
		record: record, presence: make(map[string]*Presence), inputs: make(map[string]*InputValue),
		outputs: make(map[string]*Command),
	}
}

func (dev *device) online() bool {
	return len(dev.presence) > 0
}

func (dev *device) approved() bool {
	return dev.record.State == model.DeviceApproved
}

// name is what the device is called on the pages and in the audit log.
func (dev *device) name() string {
	switch {
	case dev.record.Name != "":
		return dev.record.Name
	case dev.manifest != nil && dev.manifest.Name != "":
		return dev.manifest.Name
	}
	return dev.record.DeviceId
}

// unlock releases mu and then carries out the work decided while it was held: sends to transports (which never
// block), bus events, score changes and database writes, in that order. A refused score change takes mu again to
// record the refusal, and unlocks through here once more.
func (m *Manager) unlock() {
	work := m.work
	m.work = deferredWork{}
	m.mu.Unlock()
	m.run(work)
}

// run carries out deferred work. It is called without mu held.
func (m *Manager) run(work deferredWork) {
	for _, send := range work.sends {
		send()
	}
	if m.bus != nil {
		for _, event := range work.events {
			m.bus.Publish(event.kind, event.data)
		}
	}
	for _, change := range work.changes {
		m.applyChange(change)
	}
	saved := make(map[string]bool)
	for _, id := range work.saves {
		if saved[id] {
			continue
		}
		saved[id] = true
		if err := m.save(id); err != nil {
			log.Printf("Failed to store device %q: %v", id, err)
		}
	}
}

func (m *Manager) emit(kind string, data any) {
	m.work.events = append(m.work.events, busEvent{kind: kind, data: data})
}

func (m *Manager) persist(id string) {
	m.work.saves = append(m.work.saves, id)
}

// broadcast arranges for f to be called with every transport once mu is released.
func (m *Manager) broadcast(f func(Transport)) {
	transports := append([]Transport(nil), m.transports...)
	m.work.sends = append(m.work.sends, func() {
		for _, transport := range transports {
			f(transport)
		}
	})
}

// applyChange hands a score change to the arena and records a refusal on the device.
func (m *Manager) applyChange(pending pendingChange) {
	var err error
	if m.scorer == nil {
		err = fmt.Errorf("there is no arena to score with")
	} else {
		_, err = m.scorer.ApplyScoreChange(pending.change)
	}
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.unlock()
	bindingID := ""
	if binding := pending.binding; binding != nil {
		bindingID = binding.def.ID
		// What the binding assumed would happen did not.
		if pending.change.Op == scoring.OpTimerStart && binding.def.Action.Op == OpHold {
			binding.state.holding = false
		}
		binding.state.lastSet = nil
	}
	m.refuse(pending.device, bindingID, err.Error())
}

// refuse records something the FMS would not do for a device, and publishes it. The same refusal repeated within
// a second is counted but not published again, so that a sensor that reports many times a second does not flood
// the bus.
func (m *Manager) refuse(deviceID, bindingID, reason string) {
	now := m.now()
	if dev := m.devices[deviceID]; dev != nil {
		if dev.refusal != nil && dev.refusal.Binding == bindingID && dev.refusal.Reason == reason {
			dev.refusal.Count++
			dev.refusal.Time = now
		} else {
			dev.refusal = &Refusal{Binding: bindingID, Reason: reason, Time: now, Count: 1}
		}
	}
	key := deviceID + "\x00" + bindingID + "\x00" + reason
	if last, ok := m.refusalTimes[key]; ok && now.Sub(last) < time.Second {
		return
	}
	if len(m.refusalTimes) > 1000 {
		m.refusalTimes = make(map[string]time.Time)
	}
	m.refusalTimes[key] = now
	m.emit("device.refused", map[string]any{"id": deviceID, "binding": bindingID, "reason": reason})
}

// save writes one device to the database as it is in memory now. It is called without mu held.
func (m *Manager) save(id string) error {
	if m.db == nil {
		return nil
	}
	m.storeMu.Lock()
	defer m.storeMu.Unlock()

	m.mu.Lock()
	dev := m.devices[id]
	if dev == nil || dev.builtIn {
		m.mu.Unlock()
		return nil
	}
	record := dev.record
	record.CounterTotals = maps.Clone(dev.record.CounterTotals)
	m.mu.Unlock()

	if record.Id != 0 {
		return m.db.UpdateDevice(&record)
	}
	if err := m.db.CreateDevice(&record); err != nil {
		return err
	}
	m.mu.Lock()
	if m.devices[id] == dev {
		dev.record.Id = record.Id
	}
	m.mu.Unlock()
	return nil
}

// AddTransport adds a transport. If the manager has been started, the transport is started at once.
func (m *Manager) AddTransport(transport Transport) error {
	m.mu.Lock()
	m.transports = append(m.transports, transport)
	started := m.started
	m.mu.Unlock()
	if started {
		return m.startTransport(transport)
	}
	return nil
}

// Start starts every transport. A transport that fails to start is reported in TransportStatuses and the error
// returned; the others still start.
func (m *Manager) Start() error {
	m.mu.Lock()
	m.started = true
	transports := append([]Transport(nil), m.transports...)
	m.mu.Unlock()
	var firstErr error
	for _, transport := range transports {
		if err := m.startTransport(transport); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) startTransport(transport Transport) error {
	err := transport.Start(m)
	m.mu.Lock()
	if err != nil {
		m.startErrors[transport.Name()] = err.Error()
	} else {
		delete(m.startErrors, transport.Name())
	}
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to start the %s device transport: %w", transport.Name(), err)
	}
	return nil
}

// Stop stops every transport.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.started = false
	transports := append([]Transport(nil), m.transports...)
	m.mu.Unlock()
	for _, transport := range transports {
		transport.Stop()
	}
}

// TransportStatuses says how each transport is doing.
func (m *Manager) TransportStatuses() []TransportStatus {
	m.mu.Lock()
	transports := append([]Transport(nil), m.transports...)
	startErrors := maps.Clone(m.startErrors)
	started := m.started
	m.mu.Unlock()
	statuses := make([]TransportStatus, 0, len(transports))
	for _, transport := range transports {
		status := TransportStatus{Name: transport.Name(), Running: started}
		if reporter, ok := transport.(StatusReporter); ok {
			status = reporter.Status()
		}
		if startErr := startErrors[transport.Name()]; startErr != "" {
			status.Running, status.Error = false, startErr
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// SetGame makes the given game the one bindings apply to, with the field's station ids, and loads the binding set
// of that game and the current profile.
func (m *Manager) SetGame(game *gamedef.Game, stations []string) {
	m.mu.Lock()
	m.game = game
	m.stations = append([]string(nil), stations...)
	m.mu.Unlock()
	m.loadBindings()
}

// SetProfile makes the given field profile the one whose bindings apply.
func (m *Manager) SetProfile(profile string) {
	if profile == "" {
		profile = model.DefaultFieldProfile
	}
	m.mu.Lock()
	changed := profile != m.profile
	m.profile = profile
	m.mu.Unlock()
	if changed {
		m.loadBindings()
	}
}

// Profile returns the field profile whose bindings apply.
func (m *Manager) Profile() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.profile
}

// SetMatch tells the manager where the match is. The arena calls it on every pass of its loop.
func (m *Manager) SetMatch(match MatchInfo) {
	m.mu.Lock()
	m.match = match
	m.mu.Unlock()
}

// Devices returns every device, ordered by id.
func (m *Manager) Devices() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := sortedKeys(m.devices)
	infos := make([]Info, len(ids))
	for i, id := range ids {
		infos[i] = m.info(m.devices[id])
	}
	return infos
}

// Device returns one device.
func (m *Manager) Device(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev := m.devices[id]
	if dev == nil {
		return Info{}, false
	}
	return m.info(dev), true
}

func (m *Manager) info(dev *device) Info {
	id := dev.record.DeviceId
	info := Info{
		ID: id, Name: dev.name(), State: dev.record.State, BuiltIn: dev.builtIn, Required: dev.record.Required,
		Notes: dev.record.Notes, HasToken: dev.record.TokenHash != "", Online: dev.online(),
		Presence: make(map[string]Presence, len(dev.presence)), LastSeen: dev.record.LastSeen, Status: dev.status,
		StatusTime: dev.statusTime, Inputs: make(map[string]InputValue, len(dev.inputs)),
		Outputs: make(map[string]*Command, len(dev.outputs)), Heartbeat: dev.hbTime, Faults: []Fault{},
		StopBound: m.stopBound(id), CreatedAt: dev.record.CreatedAt,
	}
	if dev.manifest != nil {
		manifest := *dev.manifest
		info.Manifest = &manifest
	}
	for transport, presence := range dev.presence {
		info.Presence[transport] = *presence
	}
	for channel, value := range dev.inputs {
		info.Inputs[channel] = *value
	}
	for channel, command := range dev.outputs {
		info.Outputs[channel] = command.Clone()
	}
	for _, fault := range m.sortedFaults() {
		if fault.Device == id {
			info.Faults = append(info.Faults, fault)
		}
	}
	if dev.refusal != nil {
		refusal := *dev.refusal
		info.Refusal = &refusal
	}
	return info
}

// CreateDevice adds an approved device on an operator's behalf and returns the token generated for it, which is
// shown once and then only stored as a hash.
func (m *Manager) CreateDevice(id, name string) (string, error) {
	if !ValidDeviceID(id) {
		return "", fmt.Errorf("%q is not a valid device id: use 1 to 40 lowercase letters, digits and dashes", id)
	}
	if id == PlcDeviceID {
		return "", fmt.Errorf("%q is reserved for the field PLC", id)
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if m.devices[id] != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("there is already a device %q", id)
	}
	m.devices[id] = newDevice(model.Device{
		DeviceId: id, Name: name, State: model.DeviceApproved, TokenHash: HashToken(token), CreatedAt: m.now().UTC(),
	})
	m.recompile()
	m.unlock()
	return token, m.save(id)
}

// Approve lets a pending (or blocked) device in. It pins the token the device last presented.
func (m *Manager) Approve(id string) error {
	return m.changeState(id, model.DeviceApproved)
}

// Block refuses a device from now on and disconnects it. A blocked device bound to a stop faults that stop.
func (m *Manager) Block(id string) error {
	return m.changeState(id, model.DeviceBlocked)
}

// Unblock makes a blocked device pending again, to be approved like a new one.
func (m *Manager) Unblock(id string) error {
	m.mu.Lock()
	dev := m.devices[id]
	if dev != nil && dev.record.State != model.DeviceBlocked {
		m.mu.Unlock()
		return fmt.Errorf("device %q is not blocked", id)
	}
	m.mu.Unlock()
	return m.changeState(id, model.DevicePending)
}

func (m *Manager) changeState(id, state string) error {
	m.mu.Lock()
	dev, err := m.editable(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if dev.record.State == state {
		m.mu.Unlock()
		return fmt.Errorf("device %q is already %s", id, state)
	}
	if state == model.DeviceApproved && dev.record.State == model.DevicePending && dev.online() {
		dev.approvedAt = m.now()
	}
	dev.record.State = state
	if state == model.DeviceBlocked {
		m.latchDeviceFaults(id, "it was blocked")
	}
	m.broadcast(func(transport Transport) { transport.AccessChanged(id) })
	m.unlock()
	return m.save(id)
}

// Delete forgets a device. Its outputs are cleared, and if it connects again it is a new, pending device.
func (m *Manager) Delete(id string) error {
	// storeMu is held throughout, so that no write of the device is in flight while its record goes.
	m.storeMu.Lock()
	m.mu.Lock()
	dev, err := m.editable(id)
	if err != nil {
		m.mu.Unlock()
		m.storeMu.Unlock()
		return err
	}
	for channel := range dev.outputs {
		m.sendOutput(id, channel, nil)
	}
	for bindingID, fault := range m.faults {
		if fault.Device == id {
			delete(m.faults, bindingID)
		}
	}
	delete(m.devices, id)
	m.broadcast(func(transport Transport) { transport.AccessChanged(id) })
	m.recompile()
	recordId := dev.record.Id
	work := m.work
	m.work = deferredWork{}
	m.mu.Unlock()
	if recordId != 0 && m.db != nil {
		err = m.db.DeleteDevice(recordId)
	}
	// The deferred work may store other devices, which takes storeMu.
	m.storeMu.Unlock()
	m.run(work)
	return err
}

// RegenerateToken gives an approved device a new token, which is returned once. Its current connections are
// dropped.
func (m *Manager) RegenerateToken(id string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	dev, err := m.editable(id)
	if err == nil && !dev.approved() {
		err = fmt.Errorf("device %q is not approved; approve it first", id)
	}
	if err != nil {
		m.mu.Unlock()
		return "", err
	}
	dev.record.TokenHash = HashToken(token)
	m.broadcast(func(transport Transport) { transport.AccessChanged(id) })
	m.unlock()
	return token, m.save(id)
}

// SetRequired marks a device as one the match cannot start without.
func (m *Manager) SetRequired(id string, required bool) error {
	return m.edit(id, func(dev *device) { dev.record.Required = required })
}

// SetNotes stores an operator's notes about a device.
func (m *Manager) SetNotes(id, notes string) error {
	return m.edit(id, func(dev *device) { dev.record.Notes = notes })
}

// SetName gives a device the name it is shown with; empty means the name from its manifest.
func (m *Manager) SetName(id, name string) error {
	return m.edit(id, func(dev *device) { dev.record.Name = name })
}

func (m *Manager) edit(id string, change func(dev *device)) error {
	m.mu.Lock()
	dev, err := m.editable(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	change(dev)
	m.unlock()
	return m.save(id)
}

// editable returns a device an operator may change. The caller holds mu.
func (m *Manager) editable(id string) (*device, error) {
	dev := m.devices[id]
	switch {
	case dev == nil:
		return nil, fmt.Errorf("there is no device %q", id)
	case dev.builtIn:
		return nil, fmt.Errorf("device %q is built in and cannot be changed", id)
	}
	return dev, nil
}

// Identify asks a device to show itself (flash a light, beep).
func (m *Manager) Identify(id string) error {
	return m.command(id, CommandIdentify)
}

// Reboot asks a device to restart.
func (m *Manager) Reboot(id string) error {
	return m.command(id, CommandReboot)
}

func (m *Manager) command(id, do string) error {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[id]
	switch {
	case dev == nil:
		return fmt.Errorf("there is no device %q", id)
	case !dev.approved() || dev.builtIn:
		return fmt.Errorf("device %q cannot be sent commands", id)
	case !dev.online():
		return fmt.Errorf("device %q is offline", id)
	}
	m.broadcast(func(transport Transport) { transport.SendCommand(id, DeviceCommand{Do: do}) })
	return nil
}

// environment is what bindings are compiled against right now. The caller holds mu.
func (m *Manager) environment() Environment {
	manifests := make(map[string]*Manifest, len(m.devices))
	for id, dev := range m.devices {
		manifests[id] = dev.manifest
	}
	return Environment{Game: m.game, Stations: m.stations, Manifests: manifests, Claims: maps.Clone(m.claims)}
}

// Environment returns what a binding set is checked against right now, for the Bindings page.
func (m *Manager) Environment() Environment {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.environment()
}

// sortedFaults returns the faults, oldest first. The caller holds mu.
func (m *Manager) sortedFaults() []Fault {
	faults := make([]Fault, 0, len(m.faults))
	for _, fault := range m.faults {
		faults = append(faults, *fault)
	}
	sort.Slice(faults, func(i, j int) bool {
		if !faults[i].Time.Equal(faults[j].Time) {
			return faults[i].Time.Before(faults[j].Time)
		}
		return faults[i].Binding < faults[j].Binding
	})
	return faults
}

// logOnce logs a problem the first time it happens: outputs are evaluated twenty times a second.
func (m *Manager) logOnce(key, format string, args ...any) {
	if !m.errorsLogged[key] {
		m.errorsLogged[key] = true
		log.Printf(format, args...)
	}
}
