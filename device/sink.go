// What transports tell the manager: devices connecting and going, and the messages they send. These are the Sink
// methods of the Manager.

package device

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"strconv"
	"time"
)

// Authenticate decides whether a device presenting the given token may connect, as docs/devices.md lays out: an
// unknown device is accepted as pending and its token remembered, a pending one is accepted and its token replaced,
// an approved one needs its pinned token, and a blocked one is refused.
func (m *Manager) Authenticate(transport, deviceID, token string) (Access, error) {
	return m.authenticate(deviceID, token, true)
}

// AuthenticateLocal is Authenticate for transports without tokens.
func (m *Manager) AuthenticateLocal(transport, deviceID string) (Access, error) {
	return m.authenticate(deviceID, "", false)
}

func (m *Manager) authenticate(deviceID, token string, hasToken bool) (Access, error) {
	if !ValidDeviceID(deviceID) {
		return AccessNone, ErrInvalidID
	}
	if deviceID == PlcDeviceID {
		return AccessNone, ErrReservedID
	}
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	if dev == nil {
		record := model.Device{DeviceId: deviceID, State: model.DevicePending, CreatedAt: m.now().UTC()}
		if hasToken {
			record.TokenHash = HashToken(token)
		}
		m.devices[deviceID] = newDevice(record)
		m.persist(deviceID)
		m.emit("device.pending", map[string]any{"id": deviceID, "name": deviceID})
		m.recompile()
		return AccessPending, nil
	}
	switch dev.record.State {
	case model.DeviceApproved:
		if !hasToken || tokenMatches(dev.record.TokenHash, token) {
			return AccessApproved, nil
		}
		return AccessNone, ErrBadToken
	case model.DevicePending:
		if hasToken {
			if hash := HashToken(token); hash != dev.record.TokenHash {
				dev.record.TokenHash = hash
				m.persist(deviceID)
			}
		}
		return AccessPending, nil
	}
	return AccessNone, ErrBlocked
}

// Access returns what a device may do right now.
func (m *Manager) Access(deviceID string) Access {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.access(deviceID)
}

func (m *Manager) access(deviceID string) Access {
	dev := m.devices[deviceID]
	switch {
	case dev == nil:
		return AccessNone
	case dev.approved():
		return AccessApproved
	case dev.record.State == model.DevicePending:
		return AccessPending
	}
	return AccessNone
}

// Online records that a device connected over a transport.
func (m *Manager) Online(transport, deviceID, remote string) {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	if dev == nil {
		return
	}
	now := m.now()
	if dev.presence[transport] != nil && dev.approved() {
		// The transport saw a new connection before it saw the old one go: the device lost its connection.
		m.latchDeviceFaults(deviceID, "it reconnected, so its connection had dropped")
	}
	dev.presence[transport] = &Presence{Remote: remote, Since: now}
	dev.hbTime = now
	dev.record.LastSeen = now.UTC()
	m.emit("device.online", map[string]any{"id": deviceID, "transport": transport})
}

// Offline records that a device's connection over a transport went away.
func (m *Manager) Offline(transport, deviceID string) {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	if dev == nil || dev.presence[transport] == nil {
		return
	}
	delete(dev.presence, transport)
	m.emit("device.offline", map[string]any{"id": deviceID, "transport": transport})
	if dev.online() {
		return
	}
	now := m.now()
	dev.record.LastSeen = now.UTC()
	m.persist(deviceID)
	m.releaseInputs(deviceID, now)
	if dev.approved() {
		m.latchDeviceFaults(deviceID, "it disconnected")
	}
}

// Manifest records what a device says it is.
func (m *Manager) Manifest(transport, deviceID string, manifest *Manifest) error {
	err := manifest.Validate()
	if err == nil && manifest.ID != deviceID {
		err = fmt.Errorf("the manifest is for %q, but it came from %q", manifest.ID, deviceID)
	}
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	switch {
	case dev == nil:
		return ErrUnknown
	case dev.record.State == model.DeviceBlocked:
		return ErrBlocked
	case err != nil:
		m.refuse(deviceID, "", err.Error())
		return err
	}
	data, _ := json.Marshal(manifest)
	dev.manifest = manifest
	dev.record.LastSeen = m.now().UTC()
	if string(data) != dev.record.Manifest {
		dev.record.Manifest = string(data)
		m.persist(deviceID)
		m.recompile()
	}
	m.emit("device.manifest", map[string]any{"id": deviceID, "manifest": manifest})
	return nil
}

// Status records a device's report on its own health. Whether a device is online is decided by its transport,
// not by what the status says.
func (m *Manager) Status(transport, deviceID string, status Status) {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	if dev == nil || dev.record.State == model.DeviceBlocked {
		return
	}
	dev.status = status
	dev.statusTime = m.now()
	dev.record.LastSeen = dev.statusTime.UTC()
}

// Input processes one input message.
func (m *Manager) Input(transport, deviceID, channelID string, reading Reading) error {
	m.mu.Lock()
	defer m.unlock()
	dev, channel, err := m.inputChannel(deviceID, channelID)
	if err != nil {
		return err
	}
	value, err := channelValue(channel, reading.Value)
	if err != nil {
		m.refuse(deviceID, "", err.Error())
		return err
	}
	now := m.now()
	m.recordInput(dev, channel, value, reading.Conf, reading.Seq, now, true)
	m.process(dev, channel, value, reading.Conf, now)
	return nil
}

// Heartbeat processes a heartbeat: it feeds the watchdog and reports the state of the safety channels.
func (m *Manager) Heartbeat(transport, deviceID string, heartbeat Heartbeat) error {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[deviceID]
	switch {
	case dev == nil:
		return ErrUnknown
	case !dev.approved():
		return ErrNotApproved
	}
	now := m.now()
	dev.hbTime, dev.hbSeq = now, heartbeat.Seq
	dev.record.LastSeen = now.UTC()
	for _, channelID := range sortedKeys(heartbeat.Channels) {
		channel := dev.manifest.Channel(channelID)
		if channel == nil || channel.Dir != DirIn || channel.Type != TypeDigital {
			m.refuse(deviceID, "", fmt.Sprintf("the heartbeat names %q, which is not a digital input", channelID))
			continue
		}
		value, err := channelValue(channel, heartbeat.Channels[channelID])
		if err != nil {
			m.refuse(deviceID, "", err.Error())
			continue
		}
		// A heartbeat repeats the state five times a second; only a change is news.
		previous := dev.inputs[channelID]
		changed := previous == nil || previous.Value != value
		m.recordInput(dev, channel, value, nil, nil, now, changed)
		m.process(dev, channel, value, nil, now)
	}
	return nil
}

// Malformed records a message from a device that could not be read.
func (m *Manager) Malformed(transport, deviceID, what string, err error) {
	m.mu.Lock()
	defer m.unlock()
	if m.devices[deviceID] != nil {
		m.refuse(deviceID, "", fmt.Sprintf("%s: %v", what, err))
	}
}

// Outputs returns every output command in force.
func (m *Manager) Outputs() []OutputCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	var outputs []OutputCommand
	for _, id := range sortedKeys(m.devices) {
		dev := m.devices[id]
		if dev.builtIn {
			continue
		}
		for _, channel := range sortedKeys(dev.outputs) {
			output := OutputCommand{Device: id, Channel: channel, Command: dev.outputs[channel].Clone()}
			outputs = append(outputs, output)
		}
	}
	return outputs
}

// OutputsOf returns the output commands in force for one device.
func (m *Manager) OutputsOf(deviceID string) map[string]*Command {
	m.mu.Lock()
	defer m.mu.Unlock()
	outputs := make(map[string]*Command)
	if dev := m.devices[deviceID]; dev != nil && !dev.builtIn {
		for channel, command := range dev.outputs {
			outputs[channel] = command.Clone()
		}
	}
	return outputs
}

// inputChannel returns the device and channel an input is for, if the device may send it. The caller holds mu.
func (m *Manager) inputChannel(deviceID, channelID string) (*device, *Channel, error) {
	dev := m.devices[deviceID]
	switch {
	case dev == nil:
		return nil, nil, ErrUnknown
	case !dev.approved():
		return dev, nil, ErrNotApproved
	case dev.manifest == nil:
		err := fmt.Errorf("an input came before the manifest")
		m.refuse(deviceID, "", err.Error())
		return dev, nil, err
	}
	channel := dev.manifest.Channel(channelID)
	if channel == nil || channel.Dir != DirIn {
		err := fmt.Errorf("%q is not an input channel of the manifest", channelID)
		m.refuse(deviceID, "", err.Error())
		return dev, nil, err
	}
	return dev, channel, nil
}

// channelValue checks a reported value against the type of its channel and returns it as the manager keeps it:
// a float64, or a string for text channels.
func channelValue(channel *Channel, value any) (any, error) {
	if channel.Type == TypeText {
		switch value := value.(type) {
		case string:
			return value, nil
		case float64:
			// A numeric barcode read as a number is still the text of the code.
			return strconv.FormatFloat(value, 'f', -1, 64), nil
		}
		return nil, fmt.Errorf("%s is a text channel; v must be a string, not %v", channel.ID, value)
	}
	value, err := normaliseValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", channel.ID, err)
	}
	number, ok := value.(float64)
	if !ok {
		return nil, fmt.Errorf("%s is a %s channel; v must be a number, not %q", channel.ID, channel.Type, value)
	}
	switch channel.Type {
	case TypeDigital:
		if number != 0 && number != 1 {
			return nil, fmt.Errorf("%s is a digital channel; v must be 0 or 1, not %g", channel.ID, number)
		}
	case TypeCounter:
		if number < 0 {
			return nil, fmt.Errorf("%s is a counter; its running total cannot be negative (%g)", channel.ID, number)
		}
	}
	return number, nil
}

// recordInput remembers the last value of a channel and, if publish is set, publishes it.
func (m *Manager) recordInput(
	dev *device, channel *Channel, value any, conf *float64, seq *int64, now time.Time, publish bool,
) {
	dev.inputs[channel.ID] = &InputValue{Value: value, Conf: conf, Seq: seq, Time: now}
	dev.record.LastSeen = now.UTC()
	if publish {
		data := map[string]any{"id": dev.record.DeviceId, "channel": channel.ID, "value": value}
		if conf != nil {
			data["conf"] = *conf
		}
		m.emit("device.input", data)
	}
}
