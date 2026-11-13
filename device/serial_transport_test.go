package device

import (
	"bufio"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSerialPorts stands in for the computer's serial ports: a port that is plugged in opens as one end of an
// in-memory pipe, whose other end is handed to the test as the board.
type fakeSerialPorts struct {
	mu      sync.Mutex
	plugged map[string]bool
	opened  chan *fakeBoard
}

func newFakeSerialPorts(plugged ...string) *fakeSerialPorts {
	ports := &fakeSerialPorts{plugged: make(map[string]bool), opened: make(chan *fakeBoard, 10)}
	for _, name := range plugged {
		ports.plugged[name] = true
	}
	return ports
}

func (p *fakeSerialPorts) open(name string) (io.ReadWriteCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.plugged[name] {
		return nil, errors.New("serial port not found")
	}
	host, board := net.Pipe()
	p.opened <- newFakeBoard(name, board)
	return host, nil
}

func (p *fakeSerialPorts) list() ([]SerialPortInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ports []SerialPortInfo
	for name, plugged := range p.plugged {
		if plugged {
			ports = append(ports, SerialPortInfo{Name: name, USB: true, VID: "303A", PID: "1001", Product: "Board"})
		}
	}
	return ports, nil
}

func (p *fakeSerialPorts) plug(name string, plugged bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plugged[name] = plugged
}

// next returns the board on the next port the transport opens.
func (p *fakeSerialPorts) next(t *testing.T, name string) *fakeBoard {
	t.Helper()
	select {
	case board := <-p.opened:
		require.Equal(t, name, board.port)
		t.Cleanup(func() { _ = board.conn.Close() })
		return board
	case <-time.After(wait):
		t.Fatalf("port %s was not opened", name)
		return nil
	}
}

// fakeBoard is the device end of a port.
type fakeBoard struct {
	port  string
	conn  net.Conn
	lines chan map[string]any
}

func newFakeBoard(port string, conn net.Conn) *fakeBoard {
	board := &fakeBoard{port: port, conn: conn, lines: make(chan map[string]any, 100)}
	go func() {
		defer close(board.lines)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var message map[string]any
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				board.lines <- message
			}
		}
	}()
	return board
}

func (b *fakeBoard) send(t *testing.T, line string) {
	t.Helper()
	_ = b.conn.SetWriteDeadline(time.Now().Add(wait))
	_, err := b.conn.Write([]byte(line))
	require.Nil(t, err)
}

// until returns the next line of the given kind from the FMS, skipping the clock unless that is what is asked for.
func (b *fakeBoard) until(t *testing.T, kind string) map[string]any {
	t.Helper()
	for {
		select {
		case message, ok := <-b.lines:
			if !ok {
				t.Fatalf("the port closed while waiting for a %s line", kind)
			}
			if message["t"] == kind {
				return message
			}
			if message["t"] != "clock" {
				t.Fatalf("expected a %s line, got %v", kind, message)
			}
		case <-time.After(wait):
			t.Fatalf("no %s line came", kind)
		}
	}
}

// closed waits for the FMS to close the port.
func (b *fakeBoard) closed(t *testing.T) {
	t.Helper()
	for {
		select {
		case _, ok := <-b.lines:
			if !ok {
				return
			}
		case <-time.After(wait):
			t.Fatal("the port was not closed")
		}
	}
}

func startSerial(t *testing.T, h *harness, ports *fakeSerialPorts, names ...string) *SerialTransport {
	t.Helper()
	transport := NewSerialTransport()
	transport.openPort, transport.listPorts, transport.retryPeriod = ports.open, ports.list, 20*time.Millisecond
	transport.Configure(names)
	require.Nil(t, h.m.AddTransport(transport))
	t.Cleanup(transport.Stop)
	return transport
}

const serialManifest = `{"t":"manifest","proto":1,"id":"panel-1","name":"Panel","channels":[` +
	`{"id":"count","dir":"in","type":"counter"},{"id":"stop","dir":"in","type":"digital","safety":true},` +
	`{"id":"lamp","dir":"out","type":"digital"}]}` + "\r\n"

func portState(transport *SerialTransport, name string) SerialPortState {
	for _, state := range transport.PortStates() {
		if state.Name == name {
			return state
		}
	}
	return SerialPortState{}
}

func TestSerialDevice(t *testing.T) {
	h := newHarness(t)
	ports := newFakeSerialPorts("COM3")
	transport := startSerial(t, h, ports, "COM3", " COM3 ", "")
	assert.Equal(t, []string{"COM3"}, transport.Configured())
	board := ports.next(t, "COM3")

	// The firmware's own logging is ignored, and so is anything before the manifest, which names the device.
	board.send(t, "ets Jun  8 2016 00:22:57\r\nrst:0x1 (POWERON_RESET)\n")
	board.send(t, `{"t":"in","ch":"count","v":1}`+"\n")
	eventually(t, func() bool {
		return strings.Contains(portState(transport, "COM3").Problem, "a message came before the manifest")
	}, "the problem")
	assert.Empty(t, h.m.Devices())

	// The clock comes every second.
	clock := board.until(t, "clock")
	assert.InDelta(t, float64(time.Now().UnixMilli()), clock["t_ms"], 5000)

	// The manifest lets the device in, as pending: it needs no token, but it has to be approved.
	board.send(t, serialManifest)
	eventually(t, func() bool { return deviceOnline(h, "panel-1") }, "the device to come online")
	info, _ := h.m.Device("panel-1")
	assert.Equal(t, "pending", info.State)
	assert.Equal(t, "Panel", info.Name)
	assert.Equal(t, "COM3", info.Presence[SerialTransportName].Remote)
	assert.False(t, info.HasToken)
	state := portState(transport, "COM3")
	assert.True(t, state.Open)
	assert.Equal(t, "panel-1", state.Device)
	assert.Equal(t, "pending", state.Access)
	assert.Empty(t, state.Problem)

	// Approving it takes effect on the line it is on: its outputs are sent, and its inputs count.
	h.bind(`
inputs:
  - { id: goal, device: panel-1, channel: count, action: { score: balls, op: increment, alliance: red } }
  - { id: stop, device: panel-1, channel: stop, action: { field: estop, station: R1 } }
outputs:
  - { id: lamp, device: panel-1, channel: lamp, when: "phase == 'teleop'" }
`)
	h.m.UpdateOutputs(h.snapshot("teleop", 1))
	require.Nil(t, h.m.Approve("panel-1"))
	assert.Equal(t, map[string]any{"t": "out", "ch": "lamp", "v": 1.0}, board.until(t, "out"))
	assert.True(t, deviceOnline(h, "panel-1"))
	board.send(t, `{"t":"hb","seq":1,"ch":{"stop":1}}`+"\n")
	board.send(t, `{"t":"in","ch":"count","v":0}`+"\n")
	board.send(t, `{"t":"in","ch":"count","v":2,"seq":5}`+"\n")
	board.send(t, `{"t":"status","up":12,"ip":"usb"}`+"\n")
	eventually(t, func() bool { info, _ := h.m.Device("panel-1"); return info.Status.IP == "usb" }, "the status")
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", "increment", 2.0)
	}
	assert.Empty(t, h.m.Faults())

	// Commands and cleared outputs reach it.
	require.Nil(t, h.m.Identify("panel-1"))
	assert.Equal(t, map[string]any{"t": "cmd", "do": "identify"}, board.until(t, "cmd"))
	h.m.UpdateOutputs(h.snapshot("auto", 1))
	assert.Equal(t, map[string]any{"t": "out", "ch": "lamp", "v": 0.0}, board.until(t, "out"))

	// What cannot be read shows on the device and on the port.
	board.send(t, `{"t":"in","ch":"count","v":}`+"\n")
	eventually(t, func() bool {
		info, _ := h.m.Device("panel-1")
		return info.Refusal != nil && strings.Contains(info.Refusal.Reason, "message: the line is not a JSON object")
	}, "the refusal")
	assert.Contains(t, portState(transport, "COM3").Problem, "the line is not a JSON object")

	// Unplugging the board takes it offline and faults its stop (once losing the connection can no longer be the
	// reconnection an approval causes); plugged in again, it is opened by itself.
	h.clock.Advance(approvalReconnectGrace)
	ports.plug("COM3", false)
	_ = board.conn.Close()
	eventually(t, func() bool { return !deviceOnline(h, "panel-1") }, "the device to go offline")
	assert.Equal(t, 1, len(h.m.Faults()))
	eventually(t, func() bool {
		return strings.Contains(portState(transport, "COM3").Problem, "serial port not found")
	}, "the port to be missed")
	assert.False(t, portState(transport, "COM3").Open)
	assert.Contains(t, transport.Status().Error, "COM3: cannot open it (serial port not found)")
	ports.plug("COM3", true)
	board = ports.next(t, "COM3")
	board.send(t, serialManifest)
	assert.Equal(t, map[string]any{"t": "out", "ch": "lamp", "v": 0.0}, board.until(t, "out"))
	assert.True(t, deviceOnline(h, "panel-1"))
	assert.Equal(t, []string{"COM3"}, transport.Status().Addresses)

	// Taking the port out of the settings closes it.
	transport.Configure(nil)
	board.closed(t)
	eventually(t, func() bool { return !deviceOnline(h, "panel-1") }, "the device to go offline")
	assert.Empty(t, transport.PortStates())
}

func TestSerialAccessChanges(t *testing.T) {
	h := newHarness(t)
	ports := newFakeSerialPorts("ttyACM0")
	transport := startSerial(t, h, ports, "ttyACM0")
	board := ports.next(t, "ttyACM0")
	board.send(t, serialManifest)
	eventually(t, func() bool { return deviceOnline(h, "panel-1") }, "the device to come online")
	require.Nil(t, h.m.Approve("panel-1"))
	eventually(t, func() bool { return portState(transport, "ttyACM0").Access == "approved" }, "the approval")

	// Blocking it takes it offline; its messages are ignored.
	require.Nil(t, h.m.Block("panel-1"))
	eventually(t, func() bool { return !deviceOnline(h, "panel-1") }, "the device to go offline")
	eventually(t, func() bool { return portState(transport, "ttyACM0").Problem == "the device is blocked" }, "problem")
	board.send(t, `{"t":"status","ip":"blocked"}`+"\n")
	board.send(t, serialManifest)
	time.Sleep(50 * time.Millisecond)
	info, _ := h.m.Device("panel-1")
	assert.False(t, info.Online)
	assert.Empty(t, info.Status.IP)

	// Unblocked, it is pending again, on the same line.
	require.Nil(t, h.m.Unblock("panel-1"))
	eventually(t, func() bool { return deviceOnline(h, "panel-1") }, "the device to come back")
	assert.Equal(t, "pending", portState(transport, "ttyACM0").Access)

	// Deleted, it is a new pending device, as one that reconnected would be.
	require.Nil(t, h.m.Delete("panel-1"))
	eventually(t, func() bool { info, ok := h.m.Device("panel-1"); return ok && info.Online }, "the device to return")
	info, _ = h.m.Device("panel-1")
	assert.Equal(t, "pending", info.State)
	assert.NotNil(t, info.Manifest)

	// A manifest with another id is another device: the board was flashed again without being unplugged.
	board.send(t, strings.Replace(serialManifest, "panel-1", "panel-2", 1))
	eventually(t, func() bool { return deviceOnline(h, "panel-2") }, "the new device to come online")
	assert.False(t, deviceOnline(h, "panel-1"))
	assert.Equal(t, "panel-2", portState(transport, "ttyACM0").Device)

	// The transport lists the computer's ports, and stopping it takes its devices offline.
	listed, err := transport.Ports()
	require.Nil(t, err)
	assert.Equal(t, []SerialPortInfo{{Name: "ttyACM0", USB: true, VID: "303A", PID: "1001", Product: "Board"}}, listed)
	transport.Stop()
	board.closed(t)
	assert.False(t, deviceOnline(h, "panel-2"))
	assert.Equal(t, "the serial transport is not running", portState(transport, "ttyACM0").Problem)
}

func TestSerialLongLine(t *testing.T) {
	h := newHarness(t)
	ports := newFakeSerialPorts("COM4")
	transport := startSerial(t, h, ports, "COM4")
	board := ports.next(t, "COM4")

	// A line too long to be a message is dropped whole; the next one is read as usual.
	board.send(t, "{"+strings.Repeat("x", serialLineLimit+10)+"}\n")
	eventually(t, func() bool {
		return portState(transport, "COM4").Problem == "message: a line is longer than 64 KiB"
	}, "the problem")
	board.send(t, serialManifest)
	eventually(t, func() bool { return deviceOnline(h, "panel-1") }, "the device to come online")
}
