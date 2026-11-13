// The serial transport, for boards with no network: one JSON object per line, 115200 baud, over a USB serial port.
// Being plugged into the FMS computer is the device's credential, so it has no token, but a new device still has to
// be approved. The ports to open are a setting; a port that goes away (unplugged) is opened again when it is back.

package device

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go.bug.st/serial"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// SerialTransportName is the name of the serial transport.
const SerialTransportName = "serial"

// SerialBaudRate is the speed of every serial device.
const SerialBaudRate = 115200

const (
	serialRetryPeriod = time.Second // How often a configured port that is not open is tried again.
	serialLineLimit   = 64 * 1024
	serialReadSize    = 4096
	// A real port read with a timeout returns empty once a second at most; one that returns empty this often in a
	// row has gone, whatever its driver says.
	serialEmptyReadLimit = 100
	serialReadTimeout    = time.Second
)

// SerialPortInfo describes one serial port of the computer.
type SerialPortInfo struct {
	Name         string `json:"name"`
	USB          bool   `json:"usb"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serialNumber,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
}

// SerialPortState is how one of the ports the transport is configured to open is doing.
type SerialPortState struct {
	Name    string    `json:"name"`
	Open    bool      `json:"open"`
	Since   time.Time `json:"since,omitzero"`    // When it was opened.
	Device  string    `json:"device,omitempty"`  // The device on it, once its manifest has come and it was let in.
	Access  string    `json:"access,omitempty"`  // What that device may do: pending or approved.
	Problem string    `json:"problem,omitempty"` // Why the port is not open, or the last thing wrong with what it sent.
}

// SerialTransport is the serial transport.
type SerialTransport struct {
	presenceMu sync.Mutex // Held while a device's presence is changed and reported, so that reports are in order.

	mu        sync.Mutex
	sink      Sink
	running   bool
	names     []string               // The ports to open.
	ports     map[string]*serialPort // The open ports, by name.
	devices   map[string]*serialPort // The port each device is on.
	failures  map[string]string      // Why each configured port that is not open is not.
	wake      chan struct{}
	stop      chan struct{}
	loops     sync.WaitGroup // The supervisor.
	portLoops sync.WaitGroup // The reader and writer of every open port.

	openPort    func(name string) (io.ReadWriteCloser, error)
	listPorts   func() ([]SerialPortInfo, error)
	retryPeriod time.Duration
}

// serialPort is one open port.
type serialPort struct {
	name      string
	rw        io.ReadWriteCloser
	box       *outbox
	done      chan struct{}
	closeOnce sync.Once
	since     time.Time
	// Serialises what is reported about the device on the port, from its messages and from changes to its access,
	// so that the sink hears about it in order.
	sinkMu sync.Mutex

	// Guarded by the transport's mu.
	device   string
	access   Access
	manifest *Manifest // The last manifest read, to let the device in again when its access changes.
	problem  string
}

func NewSerialTransport() *SerialTransport {
	return &SerialTransport{
		ports: make(map[string]*serialPort), devices: make(map[string]*serialPort), failures: make(map[string]string),
		openPort: openSerialPort, listPorts: listSerialPorts, retryPeriod: serialRetryPeriod,
	}
}

// openSerialPort opens a port of the computer for a device.
func openSerialPort(name string) (io.ReadWriteCloser, error) {
	mode := &serial.Mode{BaudRate: SerialBaudRate, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	port, err := serial.Open(name, mode)
	if err != nil {
		return nil, err
	}
	// Reads that come back now and then even with nothing to read let the reader notice a port whose driver keeps
	// quiet about being unplugged.
	if err := port.SetReadTimeout(serialReadTimeout); err != nil {
		_ = port.Close()
		return nil, err
	}
	return port, nil
}

func (t *SerialTransport) Name() string {
	return SerialTransportName
}

// Start opens the configured ports and keeps them open.
func (t *SerialTransport) Start(sink Sink) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return nil
	}
	t.sink, t.running = sink, true
	t.stop, t.wake = make(chan struct{}), make(chan struct{}, 1)
	t.loops.Add(1)
	go t.supervise(t.stop, t.wake)
	return nil
}

// Stop closes every port, reporting its device offline.
func (t *SerialTransport) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.stop)
	t.mu.Unlock()
	t.loops.Wait()
	t.mu.Lock()
	ports := make([]*serialPort, 0, len(t.ports))
	for _, port := range t.ports {
		ports = append(ports, port)
	}
	t.failures = make(map[string]string)
	t.mu.Unlock()
	for _, port := range ports {
		t.closePort(port, "")
	}
	t.portLoops.Wait()
}

// Configure sets the ports to open. Ports no longer listed are closed at once; new ones are opened straight away
// if the transport runs.
func (t *SerialTransport) Configure(names []string) {
	var clean []string
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(clean, name) {
			clean = append(clean, name)
		}
	}
	t.mu.Lock()
	t.names = clean
	var removed []*serialPort
	for name, port := range t.ports {
		if !slices.Contains(clean, name) {
			removed = append(removed, port)
		}
	}
	for name := range t.failures {
		if !slices.Contains(clean, name) {
			delete(t.failures, name)
		}
	}
	running, wake := t.running, t.wake
	t.mu.Unlock()
	for _, port := range removed {
		t.closePort(port, "")
	}
	if running {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// Configured returns the ports the transport is configured to open.
func (t *SerialTransport) Configured() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.names...)
}

// Ports lists the serial ports of the computer.
func (t *SerialTransport) Ports() ([]SerialPortInfo, error) {
	ports, err := t.listPorts()
	sort.Slice(ports, func(i, j int) bool { return ports[i].Name < ports[j].Name })
	return ports, err
}

// PortStates says how each configured port is doing, in the order they were configured.
func (t *SerialTransport) PortStates() []SerialPortState {
	t.mu.Lock()
	defer t.mu.Unlock()
	states := make([]SerialPortState, 0, len(t.names))
	for _, name := range t.names {
		state := SerialPortState{Name: name, Problem: t.failures[name]}
		if port := t.ports[name]; port != nil {
			state.Open, state.Since, state.Device, state.Problem = true, port.since, port.device, port.problem
			if port.device != "" {
				state.Access = port.access.String()
			}
		} else if !t.running {
			state.Problem = "the serial transport is not running"
		}
		states = append(states, state)
	}
	return states
}

// Status says which ports are open and what is wrong with the others.
func (t *SerialTransport) Status() TransportStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := TransportStatus{Name: SerialTransportName, Running: t.running}
	var problems []string
	for _, name := range t.names {
		if t.ports[name] != nil {
			status.Addresses = append(status.Addresses, name)
		} else if failure := t.failures[name]; failure != "" {
			problems = append(problems, name+": "+failure)
		}
	}
	status.Error = strings.Join(problems, "; ")
	return status
}

func (t *SerialTransport) SendOutput(deviceID, channel string, command *Command) {
	if port := t.approvedPort(deviceID); port != nil {
		key, coalesce := outputKey(channel, command)
		port.box.put(outMessage{topic: key, payload: serialOutput(channel, command), coalesce: coalesce})
	}
}

func (t *SerialTransport) SendCommand(deviceID string, command DeviceCommand) {
	if port := t.approvedPort(deviceID); port != nil {
		port.box.put(outMessage{topic: "cmd", payload: serialLine(taggedMessage(command, "t", "cmd"))})
	}
}

// SendField does nothing: a serial line is told the clock, not the state of the field.
func (t *SerialTransport) SendField(state FieldState) {}

// AccessChanged takes the device's new rights into use. There is no connection to drop for it to reconnect with
// them, so the transport does what reconnecting would: a device that may stay has its rights changed in place (and
// its outputs sent if it was just approved), one that may not is taken offline, and one that was deleted is let in
// again as a new, pending device.
func (t *SerialTransport) AccessChanged(deviceID string) {
	t.mu.Lock()
	var ports []*serialPort
	for _, port := range t.ports {
		if port.device == deviceID || (port.manifest != nil && port.manifest.ID == deviceID) {
			ports = append(ports, port)
		}
	}
	t.mu.Unlock()
	for _, port := range ports {
		go t.recheck(port, deviceID)
	}
}

func (t *SerialTransport) recheck(port *serialPort, deviceID string) {
	port.sinkMu.Lock()
	defer port.sinkMu.Unlock()
	if port.closed() {
		return
	}
	t.mu.Lock()
	attached, manifest, sink := port.device == deviceID, port.manifest, t.sink
	t.mu.Unlock()
	access := sink.Access(deviceID)
	if attached && access != AccessNone {
		t.mu.Lock()
		wasApproved := port.access == AccessApproved
		port.access = access
		t.mu.Unlock()
		if !wasApproved && access == AccessApproved {
			t.sendOutputs(port, sink, deviceID)
		}
		return
	}
	if attached {
		t.detach(port)
	}
	t.mu.Lock()
	elsewhere := t.devices[deviceID] != nil
	t.mu.Unlock()
	// A port the device was taken over from (the same id on two boards) does not take it back.
	if manifest != nil && manifest.ID == deviceID && !elsewhere {
		t.admit(port, sink, manifest)
	}
}

// supervise opens the configured ports, again whenever one is missing, and sends the clock every second.
func (t *SerialTransport) supervise(stop, wake chan struct{}) {
	defer t.loops.Done()
	retry := time.NewTicker(t.retryPeriod)
	defer retry.Stop()
	clock := time.NewTicker(clockPeriod)
	defer clock.Stop()
	t.openConfigured()
	for {
		select {
		case <-stop:
			return
		case <-wake:
			t.openConfigured()
		case <-retry.C:
			t.openConfigured()
		case now := <-clock.C:
			t.sendClock(now)
		}
	}
}

func (t *SerialTransport) openConfigured() {
	t.mu.Lock()
	var names []string
	for _, name := range t.names {
		if t.ports[name] == nil {
			names = append(names, name)
		}
	}
	t.mu.Unlock()
	for _, name := range names {
		rw, err := t.openPort(name)
		t.mu.Lock()
		wanted := t.running && slices.Contains(t.names, name) && t.ports[name] == nil
		if err != nil || !wanted {
			if err != nil && wanted {
				t.failures[name] = fmt.Sprintf("cannot open it (%v); it is tried again every second", err)
			}
			t.mu.Unlock()
			if rw != nil {
				_ = rw.Close()
			}
			continue
		}
		port := &serialPort{name: name, rw: rw, box: newOutbox(), done: make(chan struct{}), since: time.Now()}
		t.ports[name] = port
		delete(t.failures, name)
		t.portLoops.Add(2)
		t.mu.Unlock()
		go t.read(port)
		go t.write(port)
	}
}

func (t *SerialTransport) sendClock(now time.Time) {
	payload := serialLine(taggedMessage(map[string]int64{"t_ms": now.UnixMilli()}, "t", "clock"))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, port := range t.ports {
		port.box.put(outMessage{topic: "clock", payload: payload, coalesce: true})
	}
}

// read reads lines from the port until it closes or goes away.
func (t *SerialTransport) read(port *serialPort) {
	defer t.portLoops.Done()
	buffer := make([]byte, serialReadSize)
	var line []byte
	discarding := false // The rest of a line that was too long.
	tooLong := func() {
		port.sinkMu.Lock()
		defer port.sinkMu.Unlock()
		t.malformed(port, "message", errors.New("a line is longer than 64 KiB"))
	}
	empty, emptySince := 0, time.Time{}
	for {
		n, err := port.rw.Read(buffer)
		if n == 0 && err == nil {
			if empty == 0 {
				emptySince = time.Now()
			}
			if empty++; empty < serialEmptyReadLimit {
				continue
			}
			if time.Since(emptySince) >= serialReadTimeout {
				empty = 0
				continue
			}
			err = errors.New("it keeps reading nothing, as an unplugged port can")
		}
		empty = 0
		for data := buffer[:n]; len(data) > 0; {
			end := bytes.IndexByte(data, '\n')
			if end < 0 {
				if !discarding {
					line = append(line, data...)
				}
				if len(line) > serialLineLimit {
					tooLong()
					line, discarding = line[:0], true
				}
				break
			}
			if !discarding && len(line)+end > serialLineLimit {
				tooLong()
			} else if !discarding {
				t.handleLine(port, append(line, data[:end]...))
			}
			line, discarding = line[:0], false
			data = data[end+1:]
		}
		if err != nil {
			t.closePort(port, fmt.Sprintf("the port was lost (%v); it is opened again when it is back", err))
			return
		}
	}
}

// write sends what is queued for the port.
func (t *SerialTransport) write(port *serialPort) {
	defer t.portLoops.Done()
	for {
		select {
		case <-port.done:
			return
		case <-port.box.wake:
			for _, message := range port.box.take() {
				if _, err := port.rw.Write(message.payload); err != nil {
					t.closePort(port, fmt.Sprintf("writing to the port failed (%v); it is opened again", err))
					return
				}
			}
		}
	}
}

// handleLine handles one line from the port. A line that is not a JSON object is the firmware's own logging, and
// is not for the FMS.
func (t *SerialTransport) handleLine(port *serialPort, line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	port.sinkMu.Lock()
	defer port.sinkMu.Unlock()
	if port.closed() {
		return
	}
	var envelope struct {
		T string `json:"t"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.malformed(port, "message", fmt.Errorf("the line is not a JSON object: %v", err))
		return
	}
	t.mu.Lock()
	deviceID, access, sink := port.device, port.access, t.sink
	t.mu.Unlock()
	if envelope.T == "manifest" {
		t.handleManifest(port, sink, line)
		return
	}
	if deviceID == "" {
		t.malformed(port, envelope.T, errors.New("a message came before the manifest"))
		return
	}
	switch envelope.T {
	case "status":
		status, err := ParseStatus(line)
		if err != nil {
			t.malformed(port, "status", err)
			return
		}
		sink.Status(SerialTransportName, deviceID, status)
	case "in":
		if access != AccessApproved {
			return
		}
		var input struct {
			Channel string `json:"ch"`
		}
		_ = json.Unmarshal(line, &input)
		reading, err := ParseReading(line)
		if err != nil {
			t.malformed(port, "in/"+input.Channel, err)
			return
		}
		_ = sink.Input(SerialTransportName, deviceID, input.Channel, reading)
	case "hb":
		if access != AccessApproved {
			return
		}
		heartbeat, err := ParseHeartbeat(line)
		if err != nil {
			t.malformed(port, "hb", err)
			return
		}
		_ = sink.Heartbeat(SerialTransportName, deviceID, heartbeat)
	default:
		t.malformed(port, "message", fmt.Errorf("unknown message type %q", envelope.T))
	}
}

// handleManifest lets in the device a manifest names, or updates the manifest of the device on the port. A manifest
// with another id means another device: a board that was swapped or flashed again without being unplugged.
func (t *SerialTransport) handleManifest(port *serialPort, sink Sink, line []byte) {
	manifest, err := ParseManifest(line)
	if err != nil {
		t.malformed(port, "manifest", err)
		return
	}
	t.mu.Lock()
	port.manifest = manifest
	deviceID := port.device
	t.mu.Unlock()
	if deviceID != "" && deviceID != manifest.ID {
		t.detach(port)
		deviceID = ""
	}
	if deviceID == "" {
		t.admit(port, sink, manifest)
		return
	}
	if err := sink.Manifest(SerialTransportName, deviceID, manifest); err != nil {
		t.setProblem(port, refusalReason(err))
	}
}

// admit lets the device a manifest names in on the port, reports it online, hands over its manifest and sends it
// its outputs. The caller holds port.sinkMu.
func (t *SerialTransport) admit(port *serialPort, sink Sink, manifest *Manifest) {
	access, err := sink.AuthenticateLocal(SerialTransportName, manifest.ID)
	if err != nil {
		t.setProblem(port, refusalReason(err))
		return
	}
	t.presenceMu.Lock()
	t.mu.Lock()
	if port.closed() || t.ports[port.name] != port {
		t.mu.Unlock()
		t.presenceMu.Unlock()
		return
	}
	// The same device on another port loses it there, as a newer MQTT connection takes over an older one; the
	// manager treats the overlap as a dropped connection.
	if previous := t.devices[manifest.ID]; previous != nil && previous != port {
		previous.device, previous.access = "", AccessNone
	}
	t.devices[manifest.ID] = port
	port.device, port.access, port.problem = manifest.ID, access, ""
	t.mu.Unlock()
	sink.Online(SerialTransportName, manifest.ID, port.name)
	t.presenceMu.Unlock()

	if err := sink.Manifest(SerialTransportName, manifest.ID, manifest); err != nil {
		t.setProblem(port, refusalReason(err))
	}
	if access == AccessApproved {
		t.sendOutputs(port, sink, manifest.ID)
	}
}

// detach reports the device on the port offline, if the port is still where it is. The caller holds port.sinkMu.
func (t *SerialTransport) detach(port *serialPort) {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	deviceID, sink := port.device, t.sink
	current := deviceID != "" && t.devices[deviceID] == port
	if current {
		delete(t.devices, deviceID)
	}
	port.device, port.access = "", AccessNone
	t.mu.Unlock()
	if current {
		sink.Offline(SerialTransportName, deviceID)
	}
}

// closePort closes a port and reports its device offline. A problem is kept to show while the port is not open.
func (t *SerialTransport) closePort(port *serialPort, problem string) {
	first := false
	port.closeOnce.Do(func() {
		first = true
		close(port.done)
		_ = port.rw.Close()
	})
	if !first {
		return
	}
	t.mu.Lock()
	if t.ports[port.name] == port {
		delete(t.ports, port.name)
		if problem != "" && t.running && slices.Contains(t.names, port.name) {
			t.failures[port.name] = problem
		}
	}
	t.mu.Unlock()
	port.sinkMu.Lock()
	defer port.sinkMu.Unlock()
	t.detach(port)
}

func (t *SerialTransport) sendOutputs(port *serialPort, sink Sink, deviceID string) {
	for channel, command := range sink.OutputsOf(deviceID) {
		key, coalesce := outputKey(channel, command)
		port.box.put(outMessage{topic: key, payload: serialOutput(channel, command), coalesce: coalesce})
	}
}

// malformed reports a line that could not be read: on the device, if the port has one, and on the port.
func (t *SerialTransport) malformed(port *serialPort, what string, err error) {
	t.mu.Lock()
	deviceID, sink := port.device, t.sink
	port.problem = fmt.Sprintf("%s: %v", what, err)
	t.mu.Unlock()
	if deviceID != "" {
		sink.Malformed(SerialTransportName, deviceID, what, err)
	}
}

func (t *SerialTransport) setProblem(port *serialPort, problem string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	port.problem = problem
}

// approvedPort returns the port of a device that may receive outputs and commands, or nil.
func (t *SerialTransport) approvedPort(deviceID string) *serialPort {
	t.mu.Lock()
	defer t.mu.Unlock()
	if port := t.devices[deviceID]; port != nil && port.access == AccessApproved {
		return port
	}
	return nil
}

func (port *serialPort) closed() bool {
	select {
	case <-port.done:
		return true
	default:
		return false
	}
}

// serialOutput is the line for an output command. One with no command fields clears the output: the device turns
// it off.
func serialOutput(channel string, command *Command) []byte {
	return serialLine(taggedMessage(command, "t", "out", "ch", channel))
}

// serialLine ends a message with its line end, in a slice of its own: payloads are shared between ports.
func serialLine(message []byte) []byte {
	return append(message[:len(message):len(message)], '\n')
}
