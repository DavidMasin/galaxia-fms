// The WebSocket transport, for scripts that would rather not speak MQTT. A device connects to /api/v1/devices/ws
// on the FMS's web port, says hello with its id, token and manifest, and then exchanges the MQTT payloads as JSON
// text frames, each tagged with its type. The web server hands the requests to ServeHTTP.

package device

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"sync"
	"time"
)

// WsTransportName is the name of the WebSocket transport.
const WsTransportName = "ws"

const (
	wsHelloTimeout   = 10 * time.Second // How long a new connection has to say hello.
	wsPingPeriod     = 5 * time.Second
	wsIdleTimeout    = 15 * time.Second // As MQTT's keep-alive: a connection that sends nothing, pongs included, is gone.
	wsWriteTimeout   = 10 * time.Second
	wsControlTimeout = time.Second // For close frames: a peer whose network has gone must not hold up the caller.
	wsMessageLimit   = 64 * 1024
)

// The close codes the FMS ends a device's connection with. The reason is given in words with each.
const (
	WsCloseRefused      = websocket.ClosePolicyViolation // Refused (bad token, blocked, invalid id), or replaced.
	WsCloseProtocol     = websocket.CloseProtocolError   // The first message was not a hello.
	WsCloseUnsupported  = websocket.CloseUnsupportedData // A binary frame.
	WsCloseReconnect    = websocket.CloseServiceRestart  // The device's access changed: it should connect again.
	WsCloseShuttingDown = websocket.CloseGoingAway       // The transport stopped.
)

// WsTransport is the WebSocket transport.
type WsTransport struct {
	presenceMu sync.Mutex // Held while a connection is recorded and reported, so that reports are in order.

	mu        sync.Mutex
	sink      Sink
	running   bool
	sessions  map[string]*wsConn // The current connection of each device.
	conns     map[*wsConn]bool   // Every open connection, whether or not it has said hello.
	handlers  sync.WaitGroup     // The requests being served, so that Stop can wait for them to report.
	lastField []byte
	stop      chan struct{}
	loops     sync.WaitGroup

	upgrader     websocket.Upgrader
	helloTimeout time.Duration
	pingPeriod   time.Duration
	idleTimeout  time.Duration
}

// wsConn is one device connection.
type wsConn struct {
	ws        *websocket.Conn
	remote    string
	id        string // Set once the device has been let in, then never changed.
	approved  bool   // Likewise: a change of access ends the connection.
	box       *outbox
	closed    chan struct{}
	closeOnce sync.Once
}

// wsHello is the first message of a connection.
type wsHello struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Token    string          `json:"token"`
	Manifest json.RawMessage `json:"manifest"`
}

// wsWelcome answers a hello. Error says why the manifest of the hello was refused, if it was.
type wsWelcome struct {
	Type     string `json:"type"`
	Approved bool   `json:"approved"`
	Error    string `json:"error,omitempty"`
}

func NewWsTransport() *WsTransport {
	return &WsTransport{
		sessions: make(map[string]*wsConn), conns: make(map[*wsConn]bool),
		upgrader: websocket.Upgrader{
			// Devices are scripts and pages anywhere on the field network; the token is what lets them in.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		helloTimeout: wsHelloTimeout, pingPeriod: wsPingPeriod, idleTimeout: wsIdleTimeout,
	}
}

func (t *WsTransport) Name() string {
	return WsTransportName
}

// Start begins accepting connections and sending the clock.
func (t *WsTransport) Start(sink Sink) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return nil
	}
	t.sink, t.running, t.stop = sink, true, make(chan struct{})
	t.loops.Add(1)
	go t.sendClock(t.stop)
	return nil
}

// Stop closes every connection, waits until each has been reported offline, and refuses new ones.
func (t *WsTransport) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.stop)
	conns := make([]*wsConn, 0, len(t.conns))
	for conn := range t.conns {
		conns = append(conns, conn)
	}
	t.mu.Unlock()
	for _, conn := range conns {
		conn.close(WsCloseShuttingDown, "the FMS is shutting down")
	}
	t.loops.Wait()
	t.handlers.Wait()
}

// Status says whether the transport accepts connections.
func (t *WsTransport) Status() TransportStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TransportStatus{Name: WsTransportName, Running: t.running}
}

func (t *WsTransport) SendOutput(deviceID, channel string, command *Command) {
	if conn := t.session(deviceID); conn != nil && conn.approved {
		key, coalesce := outputKey(channel, command)
		conn.box.put(outMessage{topic: key, payload: wsOutput(channel, command), coalesce: coalesce})
	}
}

func (t *WsTransport) SendCommand(deviceID string, command DeviceCommand) {
	if conn := t.session(deviceID); conn != nil && conn.approved {
		conn.box.put(outMessage{topic: "cmd", payload: taggedMessage(command, "type", "cmd")})
	}
}

func (t *WsTransport) SendField(state FieldState) {
	payload := taggedMessage(state, "type", "field")
	t.mu.Lock()
	t.lastField = payload
	conns := t.currentConns()
	t.mu.Unlock()
	for _, conn := range conns {
		conn.box.put(outMessage{topic: "field", payload: payload, coalesce: true})
	}
}

// AccessChanged ends the device's connection, so that it connects again with its new rights (or is refused).
func (t *WsTransport) AccessChanged(deviceID string) {
	if conn := t.session(deviceID); conn != nil {
		go conn.close(WsCloseReconnect, "the device's access changed: connect again")
	}
}

// ServeHTTP serves one device connection, from the upgrade until it closes.
func (t *WsTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	sink, running := t.sink, t.running
	if running {
		t.handlers.Add(1)
	}
	t.mu.Unlock()
	if !running {
		http.Error(w, "the WebSocket device transport is not running", http.StatusServiceUnavailable)
		return
	}
	defer t.handlers.Done()
	ws, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // The upgrader has answered the request.
	}
	conn := &wsConn{ws: ws, remote: r.RemoteAddr, closed: make(chan struct{})}
	t.mu.Lock()
	running = t.running
	if running {
		t.conns[conn] = true
	}
	t.mu.Unlock()
	if !running {
		conn.close(WsCloseShuttingDown, "the FMS is shutting down")
		return
	}
	defer func() {
		t.mu.Lock()
		delete(t.conns, conn)
		t.mu.Unlock()
	}()
	t.serve(conn, sink)
}

// serve lets a device in and then handles its messages until the connection ends.
func (t *WsTransport) serve(conn *wsConn, sink Sink) {
	defer conn.close(websocket.CloseNormalClosure, "")
	ws := conn.ws
	ws.SetReadLimit(wsMessageLimit)
	_ = ws.SetReadDeadline(time.Now().Add(t.helloTimeout))
	hello, code, err := readHello(ws)
	if err != nil {
		conn.close(code, err.Error())
		return
	}
	access, err := sink.Authenticate(WsTransportName, hello.ID, hello.Token)
	if err != nil {
		conn.close(WsCloseRefused, refusalReason(err))
		return
	}
	conn.id, conn.approved, conn.box = hello.ID, access == AccessApproved, newOutbox()
	t.connected(conn, sink)
	defer t.disconnected(conn, sink)

	welcome := wsWelcome{Type: "welcome", Approved: conn.approved}
	if len(hello.Manifest) > 0 && string(hello.Manifest) != "null" {
		manifest, err := ParseManifest(hello.Manifest)
		if err != nil {
			sink.Malformed(WsTransportName, conn.id, "manifest", err)
		} else {
			err = sink.Manifest(WsTransportName, conn.id, manifest)
		}
		if err != nil {
			welcome.Error = refusalReason(err)
		}
	}
	// The welcome is written before the writer starts, so that it comes first; what was queued for the device
	// since its connection was recorded follows it.
	data, _ := json.Marshal(welcome)
	_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		return
	}
	if conn.approved {
		for channel, command := range sink.OutputsOf(conn.id) {
			key, coalesce := outputKey(channel, command)
			conn.box.put(outMessage{topic: key, payload: wsOutput(channel, command), coalesce: coalesce})
		}
	}
	t.mu.Lock()
	field := t.lastField
	t.mu.Unlock()
	if field != nil {
		conn.box.put(outMessage{topic: "field", payload: field, coalesce: true})
	}
	go t.write(conn)
	t.read(conn, sink)
}

// readHello reads the first message of a connection, and if it is not a hello says how to close the connection.
func readHello(ws *websocket.Conn) (wsHello, int, error) {
	var hello wsHello
	messageType, data, err := ws.ReadMessage()
	switch {
	case err != nil:
		return hello, WsCloseProtocol, errors.New("no hello came")
	case messageType != websocket.TextMessage:
		return hello, WsCloseUnsupported, errors.New("messages must be JSON text frames")
	}
	if err := json.Unmarshal(data, &hello); err != nil || hello.Type != "hello" {
		return hello, WsCloseProtocol, errors.New(`the first message must be {"type":"hello","id":...,"token":...}`)
	}
	return hello, 0, nil
}

// connected records a device's connection and reports it. A connection the device already had is closed: as
// over MQTT, the newer one takes over, and the manager treats the overlap as a dropped connection.
func (t *WsTransport) connected(conn *wsConn, sink Sink) {
	t.presenceMu.Lock()
	t.mu.Lock()
	previous := t.sessions[conn.id]
	t.sessions[conn.id] = conn
	t.mu.Unlock()
	sink.Online(WsTransportName, conn.id, conn.remote)
	t.presenceMu.Unlock()
	if previous != nil {
		go previous.close(WsCloseRefused, "replaced by a newer connection of the same device")
	}
}

// disconnected reports a connection that ended, if it was still the device's current one.
func (t *WsTransport) disconnected(conn *wsConn, sink Sink) {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	current := t.sessions[conn.id] == conn
	if current {
		delete(t.sessions, conn.id)
	}
	t.mu.Unlock()
	if current {
		sink.Offline(WsTransportName, conn.id)
	}
}

// read handles what the device sends until the connection ends. Anything at all, a pong included, shows that it is
// still there.
func (t *WsTransport) read(conn *wsConn, sink Sink) {
	ws := conn.ws
	alive := func() { _ = ws.SetReadDeadline(time.Now().Add(t.idleTimeout)) }
	alive()
	ws.SetPongHandler(func(string) error { alive(); return nil })
	for {
		messageType, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		alive()
		if messageType != websocket.TextMessage {
			sink.Malformed(WsTransportName, conn.id, "message", errors.New("messages must be JSON text frames"))
			continue
		}
		t.handle(conn, sink, data)
	}
}

// handle hands one message to the sink. A pending device may only report its status: its inputs and heartbeats are
// dropped, as the broker drops them.
func (t *WsTransport) handle(conn *wsConn, sink Sink, data []byte) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		sink.Malformed(WsTransportName, conn.id, "message", fmt.Errorf("the message is not a JSON object: %v", err))
		return
	}
	switch envelope.Type {
	case "status":
		status, err := ParseStatus(data)
		if err != nil {
			sink.Malformed(WsTransportName, conn.id, "status", err)
			return
		}
		sink.Status(WsTransportName, conn.id, status)
	case "in":
		if !conn.approved {
			return
		}
		var input struct {
			Channel string `json:"ch"`
		}
		_ = json.Unmarshal(data, &input)
		reading, err := ParseReading(data)
		if err != nil {
			sink.Malformed(WsTransportName, conn.id, "in/"+input.Channel, err)
			return
		}
		_ = sink.Input(WsTransportName, conn.id, input.Channel, reading)
	case "hb":
		if !conn.approved {
			return
		}
		heartbeat, err := ParseHeartbeat(data)
		if err != nil {
			sink.Malformed(WsTransportName, conn.id, "hb", err)
			return
		}
		_ = sink.Heartbeat(WsTransportName, conn.id, heartbeat)
	case "hello":
		err := errors.New("hello is only the first message; connect again to change the manifest")
		sink.Malformed(WsTransportName, conn.id, "hello", err)
	default:
		sink.Malformed(WsTransportName, conn.id, "message", fmt.Errorf("unknown message type %q", envelope.Type))
	}
}

// write sends what is queued for the device, and pings it, until the connection ends.
func (t *WsTransport) write(conn *wsConn) {
	ping := time.NewTicker(t.pingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-conn.closed:
			return
		case <-ping.C:
			if err := conn.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				conn.close(websocket.CloseAbnormalClosure, "")
				return
			}
		case <-conn.box.wake:
			for _, message := range conn.box.take() {
				_ = conn.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				if err := conn.ws.WriteMessage(websocket.TextMessage, message.payload); err != nil {
					conn.close(websocket.CloseAbnormalClosure, "")
					return
				}
			}
		}
	}
}

// sendClock sends the field clock to every device every second.
func (t *WsTransport) sendClock(stop chan struct{}) {
	defer t.loops.Done()
	ticker := time.NewTicker(clockPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			payload := taggedMessage(json.RawMessage(clockMessage(now)), "type", "clock")
			t.mu.Lock()
			conns := t.currentConns()
			t.mu.Unlock()
			for _, conn := range conns {
				conn.box.put(outMessage{topic: "clock", payload: payload, coalesce: true})
			}
		}
	}
}

func (t *WsTransport) session(deviceID string) *wsConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[deviceID]
}

// currentConns returns the current connection of every device. The caller holds mu.
func (t *WsTransport) currentConns() []*wsConn {
	conns := make([]*wsConn, 0, len(t.sessions))
	for _, conn := range t.sessions {
		conns = append(conns, conn)
	}
	return conns
}

// wsOutput is the message for an output command. One with no command fields clears the output: the device turns it
// off.
func wsOutput(channel string, command *Command) []byte {
	return taggedMessage(command, "type", "out", "ch", channel)
}

// close ends the connection, telling the device why. A close frame that cannot be written at once is given up on.
func (c *wsConn) close(code int, reason string) {
	c.closeOnce.Do(func() {
		if code != websocket.CloseAbnormalClosure {
			if len(reason) > 123 {
				reason = reason[:123] // The most a close frame carries.
			}
			message := websocket.FormatCloseMessage(code, reason)
			_ = c.ws.WriteControl(websocket.CloseMessage, message, time.Now().Add(wsControlTimeout))
		}
		_ = c.ws.Close()
		close(c.closed)
	})
}
