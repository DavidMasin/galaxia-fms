// Virtual devices: devices that live inside the FMS, for development and tests, driven from Go (and, through it,
// from a web page or the command line). Like a device on a serial port they need no token, but a new one still has
// to be approved.

package device

import (
	"fmt"
	"sync"
	"time"
)

// VirtualTransportName is the name of the virtual transport.
const VirtualTransportName = "virtual"

const virtualHistorySize = 100

// VirtualTransport hosts virtual devices.
type VirtualTransport struct {
	mu      sync.Mutex
	sink    Sink
	devices map[string]*VirtualDevice
	field   *FieldState
}

// VirtualDevice is one virtual device. Its methods may be called from any goroutine.
type VirtualDevice struct {
	transport *VirtualTransport
	id        string
	manifest  *Manifest

	mu          sync.Mutex
	connected   bool
	seq         int64
	hbSeq       int64
	values      map[string]any // The value last set on each input.
	outputs     map[string]*Command
	history     []OutputCommand
	commands    []DeviceCommand
	field       *FieldState
	stopBeating chan struct{}
}

func NewVirtualTransport() *VirtualTransport {
	return &VirtualTransport{devices: make(map[string]*VirtualDevice)}
}

func (t *VirtualTransport) Name() string {
	return VirtualTransportName
}

func (t *VirtualTransport) Start(sink Sink) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sink = sink
	return nil
}

// Stop disconnects every virtual device.
func (t *VirtualTransport) Stop() {
	t.mu.Lock()
	devices := make([]*VirtualDevice, 0, len(t.devices))
	for _, dev := range t.devices {
		devices = append(devices, dev)
	}
	t.mu.Unlock()
	for _, dev := range devices {
		dev.Disconnect()
	}
}

func (t *VirtualTransport) Status() TransportStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TransportStatus{Name: VirtualTransportName, Running: t.sink != nil}
}

// Connect connects a virtual device with the given manifest: it is authenticated (and is pending if it is new),
// comes online, and sends its manifest. A device with the same id that is already connected is replaced.
func (t *VirtualTransport) Connect(manifest *Manifest) (*VirtualDevice, error) {
	if manifest == nil {
		return nil, fmt.Errorf("a virtual device needs a manifest")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	sink, previous := t.sink, t.devices[manifest.ID]
	t.mu.Unlock()
	if sink == nil {
		return nil, fmt.Errorf("the virtual transport has not been started")
	}
	if previous != nil {
		previous.Disconnect()
	}
	if _, err := sink.AuthenticateLocal(VirtualTransportName, manifest.ID); err != nil {
		return nil, err
	}
	dev := &VirtualDevice{
		transport: t, id: manifest.ID, manifest: manifest, connected: true, values: make(map[string]any),
		outputs: make(map[string]*Command),
	}
	for _, channel := range manifest.Channels {
		if channel.Safety {
			// A safety circuit is closed, and reads 1, until something opens it.
			dev.values[channel.ID] = 1.0
		}
	}
	t.mu.Lock()
	t.devices[manifest.ID] = dev
	dev.field = t.field
	t.mu.Unlock()
	sink.Online(VirtualTransportName, manifest.ID, "virtual")
	if err := sink.Manifest(VirtualTransportName, manifest.ID, manifest); err != nil {
		dev.Disconnect()
		return nil, err
	}
	for channel, command := range sink.OutputsOf(manifest.ID) {
		dev.receive(channel, command)
	}
	return dev, nil
}

// Device returns the connected virtual device with the given id, or nil.
func (t *VirtualTransport) Device(id string) *VirtualDevice {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.devices[id]
}

func (t *VirtualTransport) SendOutput(deviceID, channel string, command *Command) {
	if dev := t.Device(deviceID); dev != nil {
		dev.receive(channel, command)
	}
}

func (t *VirtualTransport) SendCommand(deviceID string, command DeviceCommand) {
	if dev := t.Device(deviceID); dev != nil {
		dev.mu.Lock()
		dev.commands = append(dev.commands, command)
		dev.mu.Unlock()
	}
}

func (t *VirtualTransport) SendField(state FieldState) {
	t.mu.Lock()
	t.field = &state
	devices := make([]*VirtualDevice, 0, len(t.devices))
	for _, dev := range t.devices {
		devices = append(devices, dev)
	}
	t.mu.Unlock()
	for _, dev := range devices {
		dev.mu.Lock()
		dev.field = &state
		dev.mu.Unlock()
	}
}

// AccessChanged disconnects a virtual device that may no longer connect (blocked or deleted). An approved or
// pending one keeps its connection: it is asked about its rights on every message.
func (t *VirtualTransport) AccessChanged(deviceID string) {
	t.mu.Lock()
	sink, dev := t.sink, t.devices[deviceID]
	t.mu.Unlock()
	if dev != nil && sink != nil && sink.Access(deviceID) == AccessNone {
		dev.Disconnect()
	}
}

// ID returns the device id.
func (d *VirtualDevice) ID() string {
	return d.id
}

// Connected reports whether the device is still connected.
func (d *VirtualDevice) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}

// SetInput reports a new value of an input: 0 or 1 (or false or true) for digital inputs, the running total for a
// counter, a number for analog and encoder inputs, a string for text.
func (d *VirtualDevice) SetInput(channel string, value any) error {
	return d.SendInput(channel, Reading{Value: value})
}

// SendInput reports an input with every field of the message. A missing sequence number is filled in.
func (d *VirtualDevice) SendInput(channel string, reading Reading) error {
	value, err := normaliseValue(reading.Value)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if !d.connected {
		d.mu.Unlock()
		return fmt.Errorf("virtual device %q is not connected", d.id)
	}
	d.values[channel] = value
	if reading.Seq == nil {
		d.seq++
		seq := d.seq
		reading.Seq = &seq
	}
	sink := d.transport.sinkFor()
	d.mu.Unlock()
	reading.Value = value
	return sink.Input(VirtualTransportName, d.id, channel, reading)
}

// Heartbeat sends one heartbeat with the state of every safety channel.
func (d *VirtualDevice) Heartbeat() error {
	d.mu.Lock()
	if !d.connected {
		d.mu.Unlock()
		return fmt.Errorf("virtual device %q is not connected", d.id)
	}
	d.hbSeq++
	heartbeat := Heartbeat{Seq: d.hbSeq, Channels: make(map[string]any)}
	for _, channel := range d.manifest.Channels {
		if channel.Safety {
			heartbeat.Channels[channel.ID] = d.values[channel.ID]
		}
	}
	sink := d.transport.sinkFor()
	d.mu.Unlock()
	return sink.Heartbeat(VirtualTransportName, d.id, heartbeat)
}

// StartHeartbeat sends a heartbeat every period (HeartbeatPeriod if zero) until StopHeartbeat or Disconnect.
func (d *VirtualDevice) StartHeartbeat(period time.Duration) {
	if period <= 0 {
		period = HeartbeatPeriod
	}
	d.mu.Lock()
	if d.stopBeating != nil {
		d.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	d.stopBeating = stop
	d.mu.Unlock()
	_ = d.Heartbeat()
	go func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = d.Heartbeat()
			}
		}
	}()
}

// StopHeartbeat stops the heartbeats StartHeartbeat sends, as a device that hangs would.
func (d *VirtualDevice) StopHeartbeat() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopBeating != nil {
		close(d.stopBeating)
		d.stopBeating = nil
	}
}

// SendStatus reports the device's health.
func (d *VirtualDevice) SendStatus(status Status) {
	if sink := d.transport.sinkFor(); sink != nil && d.Connected() {
		sink.Status(VirtualTransportName, d.id, status)
	}
}

// Disconnect takes the device off line.
func (d *VirtualDevice) Disconnect() {
	d.mu.Lock()
	if !d.connected {
		d.mu.Unlock()
		return
	}
	d.connected = false
	if d.stopBeating != nil {
		close(d.stopBeating)
		d.stopBeating = nil
	}
	d.mu.Unlock()

	t := d.transport
	t.mu.Lock()
	if t.devices[d.id] == d {
		delete(t.devices, d.id)
	}
	sink := t.sink
	t.mu.Unlock()
	if sink != nil {
		sink.Offline(VirtualTransportName, d.id)
	}
}

// Output returns the command in force on an output channel, or nil.
func (d *VirtualDevice) Output(channel string) *Command {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outputs[channel].Clone()
}

// Outputs returns the commands in force on every output channel.
func (d *VirtualDevice) Outputs() map[string]*Command {
	d.mu.Lock()
	defer d.mu.Unlock()
	outputs := make(map[string]*Command, len(d.outputs))
	for channel, command := range d.outputs {
		outputs[channel] = command.Clone()
	}
	return outputs
}

// History returns the last output commands received, oldest first, including clears (nil) and transient ones.
func (d *VirtualDevice) History() []OutputCommand {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]OutputCommand(nil), d.history...)
}

// Commands returns the device commands (identify, reboot) received.
func (d *VirtualDevice) Commands() []DeviceCommand {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DeviceCommand(nil), d.commands...)
}

// Field returns the last field state received, or nil.
func (d *VirtualDevice) Field() *FieldState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.field
}

func (d *VirtualDevice) receive(channel string, command *Command) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if command == nil {
		delete(d.outputs, channel)
	} else if !command.Transient {
		d.outputs[channel] = command.Clone()
	}
	d.history = append(d.history, OutputCommand{Device: d.id, Channel: channel, Command: command.Clone()})
	if len(d.history) > virtualHistorySize {
		d.history = d.history[len(d.history)-virtualHistorySize:]
	}
}

func (t *VirtualTransport) sinkFor() Sink {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sink
}
