// The field PLC as a read-only device. Its stops, field stop and "FTA ready" switch keep working in the arena, as in
// Cheesy Arena; here it is only shown, and its inputs may be bound like any device's.

package device

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"slices"
	"strings"
	"unicode"
)

// PlcTransport is the name of the PLC's transport in device presence and events.
const PlcTransport = "plc"

// PlcSource is the part of the field PLC the device layer reads.
type PlcSource interface {
	IsEnabled() bool
	IsHealthy() bool
	GetInputNames() []string
	GetCoilNames() []string
	GetInputs() []bool
	GetCoils() []bool
}

type plcState struct {
	inputNames []string
	coilNames  []string
}

// UpdatePlc mirrors the PLC into the device registry: the device "plc" is there while a PLC is configured, online
// while it is healthy, with one digital input per PLC input and one read-only output per coil. The arena calls it
// from its loop, where it reads the PLC anyway.
func (m *Manager) UpdatePlc(source PlcSource) {
	m.mu.Lock()
	defer m.unlock()
	dev := m.devices[PlcDeviceID]
	if source == nil || !source.IsEnabled() {
		if dev != nil {
			if dev.online() {
				delete(dev.presence, PlcTransport)
				m.emit("device.offline", map[string]any{"id": PlcDeviceID, "transport": PlcTransport})
			}
			delete(m.devices, PlcDeviceID)
			m.plc = plcState{}
			m.recompile()
		}
		return
	}

	now := m.now()
	inputNames, coilNames := source.GetInputNames(), source.GetCoilNames()
	if dev == nil || !slices.Equal(inputNames, m.plc.inputNames) || !slices.Equal(coilNames, m.plc.coilNames) {
		if dev == nil {
			dev = newDevice(model.Device{DeviceId: PlcDeviceID, Name: "Field PLC", State: model.DeviceApproved})
			dev.builtIn = true
			m.devices[PlcDeviceID] = dev
		}
		manifest := &Manifest{Proto: ProtocolVersion, ID: PlcDeviceID, Name: "Field PLC"}
		for _, name := range inputNames {
			manifest.Channels = append(
				manifest.Channels, &Channel{ID: plcChannelID(name), Dir: DirIn, Type: TypeDigital, Name: name},
			)
		}
		for _, name := range coilNames {
			manifest.Channels = append(
				manifest.Channels, &Channel{ID: plcChannelID(name), Dir: DirOut, Type: TypeDigital, Name: name},
			)
		}
		dev.manifest = manifest
		m.plc = plcState{inputNames: inputNames, coilNames: coilNames}
		m.recompile()
	}

	healthy := source.IsHealthy()
	switch {
	case healthy && !dev.online():
		dev.presence[PlcTransport] = &Presence{Since: now}
		dev.hbTime = now
		m.emit("device.online", map[string]any{"id": PlcDeviceID, "transport": PlcTransport})
	case !healthy && dev.online():
		delete(dev.presence, PlcTransport)
		m.emit("device.offline", map[string]any{"id": PlcDeviceID, "transport": PlcTransport})
		m.releaseInputs(PlcDeviceID, now)
		m.latchDeviceFaults(PlcDeviceID, "the PLC stopped responding")
	}
	if !healthy {
		return
	}
	dev.record.LastSeen = now.UTC()

	inputs := source.GetInputs()
	for i, name := range inputNames {
		if i >= len(inputs) {
			break
		}
		channel := dev.manifest.Channel(plcChannelID(name))
		value := 0.0
		if inputs[i] {
			value = 1
		}
		if previous := dev.inputs[channel.ID]; previous != nil && previous.Value == any(value) {
			continue
		}
		m.recordInput(dev, channel, value, nil, nil, now, true)
		m.process(dev, channel, value, nil, now)
	}
	coils := source.GetCoils()
	for i, name := range coilNames {
		if i >= len(coils) {
			break
		}
		value := 0.0
		if coils[i] {
			value = 1
		}
		// The coils are the arena's to drive; they are recorded to be shown, never sent anywhere.
		dev.outputs[plcChannelID(name)] = &Command{V: number(value)}
	}
}

// plcChannelID turns a PLC input or coil name such as "red1EStop" into a channel id such as "red1_e_stop".
func plcChannelID(name string) string {
	var id strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				id.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			id.WriteRune(r)
		} else {
			id.WriteByte('_')
		}
	}
	return id.String()
}
