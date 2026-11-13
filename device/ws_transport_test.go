package device

import (
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startWs starts a WebSocket transport behind a test server and returns it with the server's address. configure,
// if given, changes the transport before it starts.
func startWs(t *testing.T, h *harness, configure func(*WsTransport)) (*WsTransport, string) {
	t.Helper()
	transport := NewWsTransport()
	if configure != nil {
		configure(transport)
	}
	require.Nil(t, h.m.AddTransport(transport))
	t.Cleanup(transport.Stop)
	server := httptest.NewServer(transport)
	t.Cleanup(server.Close)
	return transport, strings.TrimPrefix(server.URL, "http://")
}

// wsDevice is a test device on a WebSocket connection.
type wsDevice struct {
	t    *testing.T
	conn *websocket.Conn
}

func dialWsRaw(t *testing.T, address string) *wsDevice {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+address+"/api/v1/devices/ws", nil)
	require.Nil(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &wsDevice{t: t, conn: conn}
}

// dialWs connects and says hello, and returns the device and the FMS's answer.
func dialWs(t *testing.T, address, id, token string, manifest []byte) (*wsDevice, map[string]any) {
	t.Helper()
	dev := dialWsRaw(t, address)
	hello := map[string]any{"type": "hello", "id": id, "token": token}
	if manifest != nil {
		hello["manifest"] = json.RawMessage(manifest)
	}
	data, _ := json.Marshal(hello)
	dev.send(string(data))
	return dev, dev.next()
}

func (d *wsDevice) send(text string) {
	d.t.Helper()
	require.Nil(d.t, d.conn.WriteMessage(websocket.TextMessage, []byte(text)))
}

// next returns the next message from the FMS.
func (d *wsDevice) next() map[string]any {
	d.t.Helper()
	_ = d.conn.SetReadDeadline(time.Now().Add(wait))
	_, data, err := d.conn.ReadMessage()
	require.Nil(d.t, err)
	var message map[string]any
	require.Nil(d.t, json.Unmarshal(data, &message), string(data))
	return message
}

// until returns the next message of the given type. The clock and the field state come on their own and are
// skipped; anything else is a failure.
func (d *wsDevice) until(kind string) map[string]any {
	d.t.Helper()
	for {
		message := d.next()
		switch message["type"] {
		case kind:
			return message
		case "clock", "field":
		default:
			d.t.Fatalf("expected a %s message, got %v", kind, message)
		}
	}
}

// closeError reads until the FMS closes the connection, and returns how it did.
func (d *wsDevice) closeError() *websocket.CloseError {
	d.t.Helper()
	for {
		_ = d.conn.SetReadDeadline(time.Now().Add(wait))
		_, _, err := d.conn.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			require.True(d.t, errors.As(err, &closeErr), "%v", err)
			return closeErr
		}
	}
}

func deviceOnline(h *harness, id string) bool {
	info, _ := h.m.Device(id)
	return info.Online
}

func TestWsDevice(t *testing.T) {
	h := newHarness(t)
	_, address := startWs(t, h, nil)

	// A new device is let in as pending: it is told so, its manifest is taken, and it may report its status and
	// follow the field, nothing more.
	dev, welcome := dialWs(t, address, "beam-1", "secret", manifestJson("beam-1"))
	assert.Equal(t, map[string]any{"type": "welcome", "approved": false}, welcome)
	dev.send(`{"type":"in","ch":"count","v":4}`)
	dev.send(`{"type":"status","online":true,"up":8123,"ip":"10.0.100.61"}`)
	eventually(t, func() bool { info, _ := h.m.Device("beam-1"); return info.Status.IP != "" }, "the status")
	info, _ := h.m.Device("beam-1")
	assert.Equal(t, "pending", info.State)
	assert.True(t, info.Online)
	assert.Equal(t, "Beam", info.Name)
	assert.Equal(t, "10.0.100.61", info.Status.IP)
	assert.Empty(t, info.Inputs, "an input from a pending device is dropped")
	assert.True(t, strings.HasPrefix(info.Presence[WsTransportName].Remote, "127.0.0.1:"))
	h.m.UpdateOutputs(h.snapshot("teleop", 1))
	field := dev.until("field")
	assert.Equal(t, "teleop", field["phase"])
	assert.Equal(t, "running", field["state"])

	// Approving it closes the connection, so that it comes back with an approved device's rights.
	require.Nil(t, h.m.Approve("beam-1"))
	closeErr := dev.closeError()
	assert.Equal(t, WsCloseReconnect, closeErr.Code)
	assert.Contains(t, closeErr.Text, "connect again")
	eventually(t, func() bool { return !deviceOnline(h, "beam-1") }, "the device to go offline")
	h.bind(`
inputs:
  - { id: goal, device: beam-1, channel: count, action: { score: balls, op: increment, alliance: red } }
outputs:
  - { id: lamp, device: beam-1, channel: lamp, when: "phase == 'teleop'" }
`)
	h.m.UpdateOutputs(h.snapshot("teleop", 1))

	// On connecting again it is welcomed as approved and handed its outputs.
	dev, welcome = dialWs(t, address, "beam-1", "secret", manifestJson("beam-1"))
	assert.Equal(t, map[string]any{"type": "welcome", "approved": true}, welcome)
	assert.Equal(t, map[string]any{"type": "out", "ch": "lamp", "v": 1.0}, dev.until("out"))
	assert.True(t, deviceOnline(h, "beam-1"))
	assert.Empty(t, h.m.Faults())

	// Its inputs count.
	dev.send(`{"type":"in","ch":"count","v":1,"seq":1}`)
	dev.send(`{"type":"in","ch":"count","v":3,"seq":2,"conf":0.5}`)
	eventually(t, func() bool {
		info, _ := h.m.Device("beam-1")
		return info.Inputs["count"].Value == 3.0
	}, "the second input")
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", "increment", 2.0)
		assert.Equal(t, 0.5, *changes[0].Source.Confidence)
	}

	// What cannot be read shows on the device.
	dev.send(`{"type":"in","ch":"count","v":`)
	eventually(t, func() bool {
		info, _ := h.m.Device("beam-1")
		return info.Refusal != nil && strings.Contains(info.Refusal.Reason, "message: the message is not a JSON object")
	}, "the refusal")
	dev.send(`{"type":"sing"}`)
	eventually(t, func() bool {
		info, _ := h.m.Device("beam-1")
		return strings.Contains(info.Refusal.Reason, `unknown message type "sing"`)
	}, "the refusal")
	dev.send(`{"type":"in","ch":"count","v":"many"}`)
	eventually(t, func() bool {
		info, _ := h.m.Device("beam-1")
		return strings.Contains(info.Refusal.Reason, "count is a counter channel; v must be a number")
	}, "the refusal")

	// Identify reaches it, and so does the clock.
	require.Nil(t, h.m.Identify("beam-1"))
	assert.Equal(t, map[string]any{"type": "cmd", "do": "identify"}, dev.until("cmd"))
	clock := dev.until("clock")
	assert.InDelta(t, float64(time.Now().UnixMilli()), clock["t"], 5000)

	// Removing the binding clears the output: an out message with no command.
	h.bind(`
inputs:
  - { id: goal, device: beam-1, channel: count, action: { score: balls, op: increment, alliance: red } }
`)
	assert.Equal(t, map[string]any{"type": "out", "ch": "lamp"}, dev.until("out"))

	// Closing the socket marks it offline.
	h.eventsOf("device.offline")
	_ = dev.conn.Close()
	eventually(t, func() bool { return !deviceOnline(h, "beam-1") }, "the device to go offline")
	offline := h.eventsOf("device.offline")
	if assert.Equal(t, 1, len(offline)) {
		assert.Equal(t, WsTransportName, offline[0]["transport"])
	}
}

func TestWsRefusals(t *testing.T) {
	h := newHarness(t)
	transport, address := startWs(t, h, nil)
	token, err := h.m.CreateDevice("arcade-1", "")
	require.Nil(t, err)
	_, _ = h.m.Authenticate(WsTransportName, "blocked-1", "x")
	require.Nil(t, h.m.Block("blocked-1"))

	cases := []struct {
		first  string
		code   int
		reason string
	}{
		{`{"type":"hello","id":"arcade-1","token":"wrong"}`, WsCloseRefused, "bad token"},
		{`{"type":"hello","id":"blocked-1","token":"x"}`, WsCloseRefused, "the device is blocked"},
		{`{"type":"hello","id":"Not_Valid","token":"x"}`, WsCloseRefused, "the id is not a valid device id"},
		{`{"type":"hello","id":"plc","token":"x"}`, WsCloseRefused, "the device id is reserved"},
		{`{"type":"status","online":true}`, WsCloseProtocol, `the first message must be {"type":"hello"`},
		{`hello`, WsCloseProtocol, `the first message must be {"type":"hello"`},
	}
	for _, c := range cases {
		dev := dialWsRaw(t, address)
		dev.send(c.first)
		closeErr := dev.closeError()
		assert.Equal(t, c.code, closeErr.Code, c.first)
		assert.Contains(t, closeErr.Text, c.reason, c.first)
	}
	dev := dialWsRaw(t, address)
	require.Nil(t, dev.conn.WriteMessage(websocket.BinaryMessage, []byte(`{"type":"hello"}`)))
	assert.Equal(t, WsCloseUnsupported, dev.closeError().Code)

	// A manifest that is refused is said so in the welcome, and shows on the device.
	_, welcome := dialWs(t, address, "arcade-1", token, manifestJson("other-1"))
	assert.Equal(t, true, welcome["approved"])
	assert.Equal(t, `the manifest is for "other-1", but it came from "arcade-1"`, welcome["error"])
	_, welcome = dialWs(t, address, "arcade-1", token, []byte(`{"proto":2,"id":"arcade-1","channels":[]}`))
	assert.Equal(t, "invalid manifest: proto must be 1, not 2", welcome["error"])
	info, _ := h.m.Device("arcade-1")
	assert.Equal(t, "manifest: invalid manifest: proto must be 1, not 2", info.Refusal.Reason)

	// A stopped transport turns connections away.
	transport.Stop()
	response, err := http.Get("http://" + address + "/api/v1/devices/ws")
	require.Nil(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
}

func TestWsTakeoverAndStop(t *testing.T) {
	h := newHarness(t)
	transport, address := startWs(t, h, nil)
	token, _ := h.m.CreateDevice("flaky-1", "")

	// A newer connection of the same device replaces the older, which is closed and does not take the device
	// offline when it goes.
	first, _ := dialWs(t, address, "flaky-1", token, manifestJson("flaky-1"))
	second, welcome := dialWs(t, address, "flaky-1", token, manifestJson("flaky-1"))
	assert.Equal(t, true, welcome["approved"])
	closeErr := first.closeError()
	assert.Equal(t, WsCloseRefused, closeErr.Code)
	assert.Contains(t, closeErr.Text, "replaced by a newer connection")
	eventually(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return len(transport.conns) == 1
	}, "the first connection to end")
	assert.True(t, deviceOnline(h, "flaky-1"))
	second.send(`{"type":"status","ip":"10.0.100.62"}`)
	eventually(t, func() bool { info, _ := h.m.Device("flaky-1"); return info.Status.IP != "" }, "the status")

	// Stopping the transport closes every connection and takes the devices offline.
	transport.Stop()
	assert.Equal(t, WsCloseShuttingDown, second.closeError().Code)
	assert.False(t, deviceOnline(h, "flaky-1"))
	assert.False(t, transport.Status().Running)
}

func TestWsHeartbeat(t *testing.T) {
	h := newHarness(t)
	_, address := startWs(t, h, nil)
	token, err := h.m.CreateDevice("stopbox", "")
	require.Nil(t, err)
	h.bind(`
inputs:
  - { id: stop, device: stopbox, channel: stop, action: { field: estop, station: R1 } }
`)
	dev, _ := dialWs(t, address, "stopbox", token, manifestJson("stopbox"))
	dev.send(`{"type":"hb","seq":1,"ch":{"stop":1}}`)
	eventually(t, func() bool {
		info, _ := h.m.Device("stopbox")
		return info.Inputs["stop"].Value == 1.0
	}, "the heartbeat")
	assert.Empty(t, h.m.Faults())
	assert.False(t, h.m.Stops().Stations["R1"].EStop)
	dev.send(`{"type":"hb","seq":2,"ch":{"stop":0}}`)
	eventually(t, func() bool { return h.m.Stops().Stations["R1"].EStop }, "the stop")
	assert.Equal(t, 1, len(h.m.Faults()))
}

func TestWsIdleConnection(t *testing.T) {
	h := newHarness(t)
	_, address := startWs(t, h, func(transport *WsTransport) {
		transport.pingPeriod, transport.idleTimeout = 20*time.Millisecond, 200*time.Millisecond
	})

	// A device that reads answers the FMS's pings, and stays.
	dev, _ := dialWs(t, address, "quiet-1", "x", nil)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			_ = dev.conn.SetReadDeadline(deadline)
			if _, _, err := dev.conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	<-reading
	assert.True(t, deviceOnline(h, "quiet-1"))

	// One that stops answering is taken to be gone.
	eventually(t, func() bool { return !deviceOnline(h, "quiet-1") }, "the device to go offline")
}
