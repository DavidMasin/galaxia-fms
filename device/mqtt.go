// The MQTT transport: the broker inside the FMS that ESP32s, Pico Ws, Raspberry Pis and browser pages connect to.
// This file holds the protocol (topics, who may do what, presence); mqtt_mochi.go adapts it to the broker library,
// which can be replaced without touching anything here.

package device

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MqttTransportName is the name of the MQTT transport.
const MqttTransportName = "mqtt"

// TopicPrefix starts every topic of the protocol.
const TopicPrefix = "galaxia/v1/"

const (
	mqttHousekeepingPeriod = time.Second
	outboxLimit            = 10000
)

// MqttConfig says where the broker listens. An empty address turns that listener off.
type MqttConfig struct {
	TCPAddress string // MQTT over TCP, such as ":1883".
	WSAddress  string // MQTT over WebSocket, such as ":8083".
}

// MqttAddress turns a port from the settings into a listening address; 0 (or less) turns the listener off.
func MqttAddress(port int) string {
	if port <= 0 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}

// broker is the MQTT server under the transport.
type broker interface {
	Addresses() []string
	Publish(topic string, payload []byte, retain bool) error
	// Disconnect drops a client, so that it connects again (with its new rights) or not at all.
	Disconnect(conn brokerConn)
	// Repair makes sure the broker still delivers to the given connections, which a library bug may have lost.
	Repair(conns []brokerConn)
	Close()
}

// brokerConn is one client connection. Two values are equal only if they are the same connection.
type brokerConn interface {
	ClientID() string
	Remote() string
}

// brokerHandler is what the broker asks of the transport. Every method may be called from any goroutine.
type brokerHandler interface {
	authenticate(clientID, username string, password []byte) error
	allowed(clientID, topic string, write bool) bool
	connected(conn brokerConn)
	disconnected(conn brokerConn)
	message(clientID, topic string, payload []byte)
}

// startBroker starts the broker library. It is a variable so that tests can see how the transport copes with a
// broker that fails.
var startBroker = startMochiBroker

// MqttTransport is the MQTT transport.
type MqttTransport struct {
	presenceMu sync.Mutex // Held while a connection is recorded and reported, so that reports are in order.

	mu        sync.Mutex
	config    MqttConfig
	sink      Sink
	broker    broker
	running   bool
	lastError string
	sessions  map[string]brokerConn // The current connection of each device.
	outbox    *outbox
	stop      chan struct{}
	loops     sync.WaitGroup
	lastField []byte
}

func NewMqttTransport(config MqttConfig) *MqttTransport {
	return &MqttTransport{config: config, sessions: make(map[string]brokerConn)}
}

func (t *MqttTransport) Name() string {
	return MqttTransportName
}

// Start starts the broker on the configured addresses. With both turned off it does nothing.
func (t *MqttTransport) Start(sink Sink) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return nil
	}
	t.sink = sink
	config := t.config
	if config.TCPAddress == "" && config.WSAddress == "" {
		t.lastError = ""
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()

	b, err := startBroker(config, t)
	t.mu.Lock()
	if err != nil {
		t.lastError = err.Error()
		t.mu.Unlock()
		return err
	}
	t.broker, t.running, t.lastError = b, true, ""
	t.sessions = make(map[string]brokerConn)
	t.outbox = newOutbox()
	t.stop = make(chan struct{})
	t.loops.Add(2)
	go t.sendLoop(b, t.outbox, t.stop)
	go t.housekeeping(b, t.stop)
	box, field := t.outbox, t.lastField
	t.mu.Unlock()

	// A broker that has just started retains nothing: give it every output in force and the field state.
	for _, output := range sink.Outputs() {
		box.put(outputMessage(output.Device, output.Channel, output.Command))
	}
	if field != nil {
		box.put(outMessage{topic: TopicPrefix + "field/state", payload: field, retain: true, coalesce: true})
	}
	return nil
}

// Stop stops the broker, disconnecting every device.
func (t *MqttTransport) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	b := t.broker
	t.running, t.broker, t.outbox = false, nil, nil
	close(t.stop)
	t.mu.Unlock()
	b.Close()
	t.loops.Wait()

	// The broker reports the disconnections it makes as it closes; any it did not are reported here.
	t.presenceMu.Lock()
	t.mu.Lock()
	sessions, sink := t.sessions, t.sink
	t.sessions = make(map[string]brokerConn)
	t.mu.Unlock()
	for id := range sessions {
		sink.Offline(MqttTransportName, id)
	}
	t.presenceMu.Unlock()
}

// Configure changes where the broker listens. A running broker is restarted on the new addresses; devices
// reconnect by themselves.
func (t *MqttTransport) Configure(config MqttConfig) error {
	t.mu.Lock()
	changed := config != t.config
	t.config = config
	running, sink := t.running, t.sink
	t.mu.Unlock()
	if !changed || sink == nil {
		return nil
	}
	if running {
		t.Stop()
	}
	return t.Start(sink)
}

// Config returns where the broker is configured to listen.
func (t *MqttTransport) Config() MqttConfig {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.config
}

// Status says whether the broker runs and where it listens.
func (t *MqttTransport) Status() TransportStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := TransportStatus{Name: MqttTransportName, Running: t.running, Error: t.lastError}
	if t.broker != nil {
		status.Addresses = t.broker.Addresses()
	}
	if !t.running && t.lastError == "" && t.config.TCPAddress == "" && t.config.WSAddress == "" {
		status.Error = "turned off in the settings"
	}
	return status
}

// Addresses returns where the broker listens, with the ports it actually got.
func (t *MqttTransport) Addresses() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broker == nil {
		return nil
	}
	return t.broker.Addresses()
}

func (t *MqttTransport) SendOutput(deviceID, channel string, command *Command) {
	t.queue(outputMessage(deviceID, channel, command))
}

func (t *MqttTransport) SendCommand(deviceID string, command DeviceCommand) {
	payload, _ := json.Marshal(command)
	t.queue(outMessage{topic: TopicPrefix + "dev/" + deviceID + "/cmd", payload: payload})
}

func (t *MqttTransport) SendField(state FieldState) {
	payload, _ := json.Marshal(state)
	t.mu.Lock()
	t.lastField = payload
	t.mu.Unlock()
	t.queue(outMessage{topic: TopicPrefix + "field/state", payload: payload, retain: true, coalesce: true})
}

// AccessChanged disconnects the device, so that it connects again with its new rights (or is refused).
func (t *MqttTransport) AccessChanged(deviceID string) {
	t.mu.Lock()
	conn, b := t.sessions[deviceID], t.broker
	t.mu.Unlock()
	if conn != nil && b != nil {
		// Writing to a client whose network has gone can take until its keep-alive runs out.
		go b.Disconnect(conn)
	}
}

func (t *MqttTransport) queue(message outMessage) {
	t.mu.Lock()
	box := t.outbox
	t.mu.Unlock()
	if box != nil {
		box.put(message)
	}
}

// outputMessage is the message for an output command: retained, so that a device that restarts gets it back, and
// empty to clear it. A transient command (a sound) is not retained.
func outputMessage(deviceID, channel string, command *Command) outMessage {
	message := outMessage{topic: TopicPrefix + "dev/" + deviceID + "/out/" + channel, retain: true, coalesce: true}
	if command != nil {
		message.payload, _ = json.Marshal(command)
		if command.Transient {
			message.retain, message.coalesce = false, false
		}
	}
	return message
}

func (t *MqttTransport) sendLoop(b broker, box *outbox, stop chan struct{}) {
	defer t.loops.Done()
	for {
		select {
		case <-stop:
			return
		case <-box.wake:
		}
		for _, message := range box.take() {
			_ = b.Publish(message.topic, message.payload, message.retain)
		}
	}
}

// housekeeping publishes the clock every second and repairs lost sessions.
func (t *MqttTransport) housekeeping(b broker, stop chan struct{}) {
	defer t.loops.Done()
	ticker := time.NewTicker(mqttHousekeepingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			t.queue(outMessage{topic: TopicPrefix + "field/clock", payload: clockMessage(now), coalesce: true})
			t.mu.Lock()
			conns := make([]brokerConn, 0, len(t.sessions))
			for _, conn := range t.sessions {
				conns = append(conns, conn)
			}
			t.mu.Unlock()
			b.Repair(conns)
		}
	}
}

// authenticate applies the connection rules of docs/devices.md: the client id is the username is the device id,
// and the password is its token.
func (t *MqttTransport) authenticate(clientID, username string, password []byte) error {
	if !ValidDeviceID(clientID) {
		return ErrInvalidID
	}
	if username != clientID {
		return ErrBadToken
	}
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	_, err := sink.Authenticate(MqttTransportName, clientID, string(password))
	return err
}

// allowed decides what a device may publish (write) and subscribe to or receive (read), as docs/devices.md lays
// out. For a read, topic may be a subscription filter: it is allowed if everything it can match is allowed.
func (t *MqttTransport) allowed(clientID, topic string, write bool) bool {
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	if sink == nil {
		return false
	}
	access := sink.Access(clientID)
	if access == AccessNone {
		return false
	}
	own := TopicPrefix + "dev/" + clientID + "/"
	if write {
		switch topic {
		case own + "manifest", own + "status":
			return true
		case own + "hb":
			return access == AccessApproved
		}
		channel, isInput := strings.CutPrefix(topic, own+"in/")
		return isInput && access == AccessApproved && ValidChannelID(channel)
	}
	readable := []string{TopicPrefix + "field/#"}
	if access == AccessApproved {
		readable = append(readable, own+"out/+", own+"cmd")
	}
	for _, filter := range readable {
		if filterCovers(filter, topic) {
			return true
		}
	}
	return false
}

// filterCovers reports whether every topic the filter (or topic) inner can match is matched by outer.
func filterCovers(outer, inner string) bool {
	outerLevels, innerLevels := strings.Split(outer, "/"), strings.Split(inner, "/")
	for i, level := range outerLevels {
		if level == "#" {
			return true
		}
		if i >= len(innerLevels) {
			return false
		}
		switch innerLevel := innerLevels[i]; {
		case innerLevel == "#":
			return false
		case level == "+":
		case innerLevel != level:
			return false
		}
	}
	return len(innerLevels) == len(outerLevels)
}

func (t *MqttTransport) connected(conn brokerConn) {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	t.sessions[conn.ClientID()] = conn
	sink := t.sink
	t.mu.Unlock()
	sink.Online(MqttTransportName, conn.ClientID(), conn.Remote())
}

// disconnected reports a connection that went. When a device reconnects before its old connection is noticed to be
// gone, the broker reports the old one after the new: only the current connection counts.
func (t *MqttTransport) disconnected(conn brokerConn) {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	id := conn.ClientID()
	current := t.sessions[id] == conn
	if current {
		delete(t.sessions, id)
	}
	sink := t.sink
	t.mu.Unlock()
	if current {
		sink.Offline(MqttTransportName, id)
	}
}

// message handles a message a device published.
func (t *MqttTransport) message(clientID, topic string, payload []byte) {
	rest, ok := strings.CutPrefix(topic, TopicPrefix+"dev/"+clientID+"/")
	if !ok {
		return
	}
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	switch {
	case rest == "manifest":
		if len(payload) == 0 {
			return // A device clearing its retained manifest.
		}
		manifest, err := ParseManifest(payload)
		if err != nil {
			sink.Malformed(MqttTransportName, clientID, "manifest", err)
			return
		}
		_ = sink.Manifest(MqttTransportName, clientID, manifest)
	case rest == "status":
		if len(payload) == 0 {
			return
		}
		status, err := ParseStatus(payload)
		if err != nil {
			sink.Malformed(MqttTransportName, clientID, "status", err)
			return
		}
		sink.Status(MqttTransportName, clientID, status)
	case rest == "hb":
		heartbeat, err := ParseHeartbeat(payload)
		if err != nil {
			sink.Malformed(MqttTransportName, clientID, "hb", err)
			return
		}
		_ = sink.Heartbeat(MqttTransportName, clientID, heartbeat)
	case strings.HasPrefix(rest, "in/"):
		channel := strings.TrimPrefix(rest, "in/")
		reading, err := ParseReading(payload)
		if err != nil {
			sink.Malformed(MqttTransportName, clientID, "in/"+channel, err)
			return
		}
		_ = sink.Input(MqttTransportName, clientID, channel, reading)
	}
}

// outMessage is one message waiting to be published.
type outMessage struct {
	topic    string
	payload  []byte
	retain   bool
	coalesce bool // Only the latest message for the topic matters.
}

// outbox queues messages for the broker so that the manager never waits for it. Messages that coalesce replace the
// one waiting for the same topic, so an output that changes faster than it is sent loses nothing but the steps in
// between; the others (commands, sounds) are kept in order, up to a limit.
type outbox struct {
	mu      sync.Mutex
	entries []outMessage
	index   map[string]int // Position in entries of the waiting message of each coalescing topic.
	wake    chan struct{}
}

func newOutbox() *outbox {
	return &outbox{index: make(map[string]int), wake: make(chan struct{}, 1)}
}

func (box *outbox) put(message outMessage) {
	box.mu.Lock()
	if i, ok := box.index[message.topic]; ok && message.coalesce {
		box.entries[i] = message
	} else if message.coalesce || len(box.entries) < outboxLimit {
		if message.coalesce {
			box.index[message.topic] = len(box.entries)
		}
		box.entries = append(box.entries, message)
	}
	box.mu.Unlock()
	select {
	case box.wake <- struct{}{}:
	default:
	}
}

func (box *outbox) take() []outMessage {
	box.mu.Lock()
	defer box.mu.Unlock()
	entries := box.entries
	box.entries = nil
	box.index = make(map[string]int)
	return entries
}
