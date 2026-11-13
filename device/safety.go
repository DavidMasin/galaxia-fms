// Safety: stops from devices, the heartbeat watchdog, latched faults, and the conditions a match cannot start
// without. A wireless device is never the only stop path: the driver station and wired field stops are.

package device

import (
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"sort"
	"strings"
	"time"
)

// The heartbeat a device with safety channels sends, and how long the FMS waits for one before it stops.
const (
	HeartbeatPeriod  = 200 * time.Millisecond
	HeartbeatTimeout = time.Second
)

// Fault is a latched problem with a device bound to a stop. Its stop is held until an operator clears it.
type Fault struct {
	Device  string    `json:"device"`
	Binding string    `json:"binding"`
	Reason  string    `json:"reason"`
	Time    time.Time `json:"time"`
}

// Stops are the stops field devices ask for right now.
type Stops struct {
	// Stations is keyed by the id of every station a device can stop, whether or not it is stopping it now, so that
	// the arena knows which stations it must ask about even without a PLC.
	Stations  map[string]StationStop
	FieldStop bool
}

// StationStop is what devices ask of one station.
type StationStop struct {
	EStop bool
	AStop bool
}

// FieldAction is something a device asked the field to do. The arena carries it out on its own goroutine.
type FieldAction struct {
	Action  string    `json:"action"`
	Device  string    `json:"device"`
	Binding string    `json:"binding"`
	Time    time.Time `json:"time"`
}

// stopBound reports whether any stop is bound to a channel of the device. The caller holds mu.
func (m *Manager) stopBound(deviceID string) bool {
	for _, binding := range m.set.stops {
		if binding.ref.Device == deviceID {
			return true
		}
	}
	return false
}

// latchFault latches a fault on a stop binding, unless one is already latched. The caller holds mu.
func (m *Manager) latchFault(binding *inputBinding, reason string) {
	if m.faults[binding.def.ID] != nil {
		return
	}
	fault := &Fault{Device: binding.ref.Device, Binding: binding.def.ID, Reason: reason, Time: m.now()}
	m.faults[binding.def.ID] = fault
	m.emit("device.fault", map[string]any{"id": fault.Device, "binding": fault.Binding, "reason": reason})
}

// latchDeviceFaults latches a fault on every stop bound to the device. The caller holds mu.
func (m *Manager) latchDeviceFaults(deviceID, reason string) {
	for _, binding := range m.set.stops {
		if binding.ref.Device == deviceID {
			m.latchFault(binding, reason)
		}
	}
}

// watchdog latches a fault on the stops of a device with safety channels that has stopped sending heartbeats.
// The caller holds mu.
func (m *Manager) watchdog(now time.Time) {
	for _, binding := range m.set.stops {
		if !binding.safety || m.faults[binding.def.ID] != nil {
			continue
		}
		dev := m.devices[binding.ref.Device]
		if dev == nil || !dev.approved() || !dev.online() {
			continue
		}
		if silence := now.Sub(dev.hbTime); silence > HeartbeatTimeout {
			m.latchFault(binding, fmt.Sprintf("no heartbeat for %.1f seconds", silence.Seconds()))
		}
	}
}

// Faults returns the latched faults, oldest first.
func (m *Manager) Faults() []Fault {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sortedFaults()
}

// Faulted reports whether any fault is latched.
func (m *Manager) Faulted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.faults) > 0
}

// ClearFaults clears the faults of a device, at an operator's request. It only does so if the device is back:
// connected, sending heartbeats if it has safety channels, and with every stop bound to it released. Otherwise it
// returns why not and the faults stay.
func (m *Manager) ClearFaults(deviceID string) error {
	m.mu.Lock()
	defer m.unlock()
	return m.clearFaults(deviceID, m.now())
}

// ClearAllFaults clears every fault that can be cleared, and says why the others cannot.
func (m *Manager) ClearAllFaults() error {
	m.mu.Lock()
	defer m.unlock()
	now := m.now()
	devices := make(map[string]bool)
	for _, fault := range m.faults {
		devices[fault.Device] = true
	}
	var errs []error
	for _, deviceID := range sortedKeys(devices) {
		if err := m.clearFaults(deviceID, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) clearFaults(deviceID string, now time.Time) error {
	var faulted []string
	for bindingID, fault := range m.faults {
		if fault.Device == deviceID {
			faulted = append(faulted, bindingID)
		}
	}
	if len(faulted) == 0 {
		return fmt.Errorf("device %q has no fault", deviceID)
	}
	dev := m.devices[deviceID]
	switch {
	case dev == nil:
		return fmt.Errorf("device %q is not known any more", deviceID)
	case !dev.approved():
		return fmt.Errorf("device %q is not approved", deviceID)
	case !dev.online():
		return fmt.Errorf("device %q is offline; its fault can be cleared once it is back", deviceID)
	}
	for _, binding := range m.set.stops {
		if binding.ref.Device != deviceID {
			continue
		}
		if binding.safety && now.Sub(dev.hbTime) > HeartbeatTimeout {
			return fmt.Errorf("device %q is not sending heartbeats", deviceID)
		}
		if binding.state.level || binding.state.pending {
			return fmt.Errorf("device %q still reports its stop (%s) as active", deviceID, binding.def.Channel)
		}
	}
	sort.Strings(faulted)
	for _, bindingID := range faulted {
		fault := m.faults[bindingID]
		delete(m.faults, bindingID)
		m.emit("device.fault_cleared", map[string]any{"id": deviceID, "binding": bindingID, "reason": fault.Reason})
	}
	return nil
}

// dropStaleFaults removes faults whose binding is gone or no longer a stop. The caller holds mu.
func (m *Manager) dropStaleFaults() {
	stops := make(map[string]bool, len(m.set.stops))
	for _, binding := range m.set.stops {
		stops[binding.def.ID] = true
	}
	for _, bindingID := range sortedKeys(m.faults) {
		if !stops[bindingID] {
			fault := m.faults[bindingID]
			delete(m.faults, bindingID)
			m.emit(
				"device.fault_cleared",
				map[string]any{"id": fault.Device, "binding": bindingID, "reason": "the binding was removed"},
			)
		}
	}
}

// Stops returns the stops field devices ask for. A stop binding is active while its input is active or while a
// fault is latched on it. The arena combines these with the PLC's stops.
func (m *Manager) Stops() Stops {
	m.mu.Lock()
	defer m.mu.Unlock()
	stops := Stops{Stations: make(map[string]StationStop)}
	for _, binding := range m.set.stops {
		active := binding.state.level || m.faults[binding.def.ID] != nil
		station := binding.def.Action.Station
		switch binding.def.Action.Field {
		case ActionEStop:
			stop := stops.Stations[station]
			stop.EStop = stop.EStop || active
			stops.Stations[station] = stop
		case ActionAStop:
			stop := stops.Stations[station]
			stop.AStop = stop.AStop || active
			stops.Stations[station] = stop
		case ActionFieldStop:
			stops.FieldStop = stops.FieldStop || active
		}
	}
	return stops
}

// StartConditions returns, in words, everything about the devices that keeps a match from starting: a device
// bound to a stop that is not connected and approved, or is faulted, a required device that is offline, and a
// field stop from a device.
func (m *Manager) StartConditions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var conditions []string

	stopsByDevice := make(map[string][]string)
	fieldStop := make(map[string]bool)
	for _, binding := range m.set.stops {
		action := binding.def.Action
		what := "field stop"
		switch action.Field {
		case ActionEStop:
			what = "E-stop " + action.Station
		case ActionAStop:
			what = "A-stop " + action.Station
		}
		stopsByDevice[binding.ref.Device] = append(stopsByDevice[binding.ref.Device], what)
		if action.Field == ActionFieldStop && (binding.state.level || m.faults[binding.def.ID] != nil) {
			fieldStop[binding.ref.Device] = true
		}
	}
	for _, deviceID := range sortedKeys(stopsByDevice) {
		dev := m.devices[deviceID]
		label := fmt.Sprintf("stop device %s (%s)", m.label(deviceID), strings.Join(stopsByDevice[deviceID], ", "))
		switch {
		case dev == nil:
			conditions = append(conditions, label+" has never connected")
		case dev.record.State == model.DeviceBlocked:
			conditions = append(conditions, label+" is blocked")
		case !dev.approved():
			conditions = append(conditions, label+" is waiting for approval")
		case !dev.online():
			conditions = append(conditions, label+" is offline")
		}
		var reasons []string
		for _, fault := range m.sortedFaults() {
			if fault.Device == deviceID {
				reasons = append(reasons, fault.Reason)
			}
		}
		if len(reasons) > 0 {
			conditions = append(conditions, fmt.Sprintf(
				"%s has a fault (%s); clear it on the Devices page once it is safe", label, strings.Join(reasons, "; "),
			))
		}
	}
	for _, deviceID := range sortedKeys(m.devices) {
		dev := m.devices[deviceID]
		if dev.record.Required && !dev.online() {
			conditions = append(conditions, fmt.Sprintf("required device %s is offline", m.label(deviceID)))
		}
	}
	for _, deviceID := range sortedKeys(fieldStop) {
		conditions = append(conditions, fmt.Sprintf("a field stop from device %s is active", m.label(deviceID)))
	}
	return conditions
}

// label names a device in a message: its id, and its name if it has one. The caller holds mu.
func (m *Manager) label(deviceID string) string {
	if dev := m.devices[deviceID]; dev != nil && dev.name() != deviceID {
		return fmt.Sprintf("%s %q", deviceID, dev.name())
	}
	return deviceID
}

// TakeFieldActions returns the field actions devices asked for since the last call, oldest first. The arena calls
// it on every pass of its loop and carries them out.
func (m *Manager) TakeFieldActions() []FieldAction {
	m.mu.Lock()
	defer m.mu.Unlock()
	actions := m.fieldActions
	m.fieldActions = nil
	return actions
}

// ReportFieldAction tells the manager how a field action went. One that failed is a refusal on the device.
func (m *Manager) ReportFieldAction(action FieldAction, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.unlock()
	m.refuse(action.Device, action.Binding, fmt.Sprintf("%s: %v", action.Action, err))
}
