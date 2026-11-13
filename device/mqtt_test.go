package device

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device/mqtttest"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const wait = 5 * time.Second

// startMqtt starts an MQTT transport on free local ports and returns it with its TCP and WebSocket addresses.
func startMqtt(t *testing.T, h *harness) (*MqttTransport, string, string) {
	t.Helper()
	transport := NewMqttTransport(MqttConfig{TCPAddress: "127.0.0.1:0", WSAddress: "127.0.0.1:0"})
	require.Nil(t, h.m.AddTransport(transport))
	t.Cleanup(transport.Stop)
	addresses := transport.Addresses()
	require.Equal(t, 2, len(addresses))
	return transport, strings.TrimPrefix(addresses[0], "tcp://"), strings.TrimPrefix(addresses[1], "ws://")
}

func dial(t *testing.T, address, id, token string) *mqtttest.Client {
	t.Helper()
	client, code, err := mqtttest.Dial(address, mqtttest.Options{
		ClientID: id, Username: id, Password: token, WillTopic: TopicPrefix + "dev/" + id + "/status",
		WillPayload: []byte(`{"online":false}`), WillRetain: true,
	})
	require.Nil(t, err)
	require.Equal(t, byte(mqtttest.ConnAccepted), code)
	t.Cleanup(client.Drop)
	return client
}

func manifestJson(id string) []byte {
	return []byte(fmt.Sprintf(`{"proto":1,"id":%q,"name":"Beam","channels":[
		{"id":"count","dir":"in","type":"counter"},{"id":"stop","dir":"in","type":"digital","safety":true},
		{"id":"lamp","dir":"out","type":"digital"}]}`, id))
}

// eventually waits for a condition that another goroutine makes true.
func eventually(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMqttDevice(t *testing.T) {
	h := newHarness(t)
	_, address, _ := startMqtt(t, h)
	topic := func(rest string) string { return TopicPrefix + "dev/beam-1/" + rest }

	// A new device is accepted and held pending; it may announce itself and follow the field, nothing more.
	client := dial(t, address, "beam-1", "secret")
	require.Nil(t, client.Publish(topic("manifest"), manifestJson("beam-1"), true))
	status := []byte(`{"online":true,"up":8123,"rssi":-58,"ip":"10.0.100.61"}`)
	require.Nil(t, client.Publish(topic("status"), status, true))
	code, err := client.Subscribe(topic("out/+"), wait)
	require.Nil(t, err)
	assert.Equal(t, byte(mqtttest.SubackFailure), code, "a pending device gets no outputs")
	code, err = client.Subscribe(TopicPrefix+"field/#", wait)
	require.Nil(t, err)
	assert.Equal(t, byte(0), code)
	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":4}`), false))
	require.Nil(t, client.Ping(wait))
	info, _ := h.m.Device("beam-1")
	assert.Equal(t, "pending", info.State)
	assert.True(t, info.Online)
	assert.Equal(t, "Beam", info.Name)
	assert.Equal(t, "10.0.100.61", info.Status.IP)
	assert.Empty(t, info.Inputs, "the broker drops an input from a pending device")
	assert.True(t, strings.HasPrefix(info.Presence[MqttTransportName].Remote, "127.0.0.1:"))

	// Approving it drops the connection, so that it comes back with an approved device's rights.
	require.Nil(t, h.m.Approve("beam-1"))
	select {
	case <-client.Done():
	case <-time.After(wait):
		t.Fatal("the device was not disconnected when it was approved")
	}
	eventually(t, func() bool { info, _ := h.m.Device("beam-1"); return !info.Online }, "the device to go offline")
	client = dial(t, address, "beam-1", "secret")
	require.Nil(t, client.Publish(topic("manifest"), manifestJson("beam-1"), true))
	code, _ = client.Subscribe(topic("out/+"), wait)
	assert.Equal(t, byte(0), code)
	code, _ = client.Subscribe(topic("cmd"), wait)
	assert.Equal(t, byte(0), code)
	code, _ = client.Subscribe(TopicPrefix+"field/#", wait)
	assert.Equal(t, byte(0), code)

	// Its inputs now count, in a running match.
	h.bind(`
inputs:
  - { id: goal, device: beam-1, channel: count, action: { score: balls, op: increment, alliance: red } }
outputs:
  - { id: lamp, device: beam-1, channel: lamp, when: "phase == 'teleop'" }
`)
	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":1,"seq":1}`), false))
	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":3,"seq":2,"conf":0.5}`), false))
	require.Nil(t, client.Ping(wait))
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", "increment", 2.0)
		assert.Equal(t, "beam-1", changes[0].Source.ID)
		assert.Equal(t, 0.5, *changes[0].Source.Confidence)
	}

	// A malformed message shows on the device.
	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":`), false))
	require.Nil(t, client.Ping(wait))
	info, _ = h.m.Device("beam-1")
	assert.Contains(t, info.Refusal.Reason, "in/count: the input is not valid JSON")

	// Outputs arrive, and identify reaches the device.
	h.m.UpdateOutputs(h.snapshot("teleop", 1))
	message, err := client.WaitFor(topic("out/lamp"), wait)
	require.Nil(t, err)
	assert.JSONEq(t, `{"v":1}`, string(message.Payload))
	require.Nil(t, h.m.Identify("beam-1"))
	message, err = client.WaitFor(topic("cmd"), wait)
	require.Nil(t, err)
	assert.JSONEq(t, `{"do":"identify"}`, string(message.Payload))
	state, err := client.WaitFor(TopicPrefix+"field/state", wait)
	require.Nil(t, err)
	var fieldState FieldState
	require.Nil(t, json.Unmarshal(state.Payload, &fieldState))
	assert.Equal(t, "teleop", fieldState.Phase)
	clock, err := client.WaitFor(TopicPrefix+"field/clock", 3*time.Second)
	require.Nil(t, err)
	assert.Contains(t, string(clock.Payload), `"t":`)

	// Outputs are retained: a device that restarts gets its outputs as soon as it subscribes.
	client.Close()
	eventually(t, func() bool { info, _ := h.m.Device("beam-1"); return !info.Online }, "the device to go offline")
	client = dial(t, address, "beam-1", "secret")
	_, _ = client.Subscribe(topic("out/+"), wait)
	message, err = client.WaitFor(topic("out/lamp"), wait)
	require.Nil(t, err)
	assert.True(t, message.Retain)
	assert.JSONEq(t, `{"v":1}`, string(message.Payload))

	// Removing the binding clears the retained output.
	h.bind(`
inputs:
  - { id: goal, device: beam-1, channel: count, action: { score: balls, op: increment, alliance: red } }
`)
	message, err = client.WaitFor(topic("out/lamp"), wait)
	require.Nil(t, err)
	assert.Empty(t, message.Payload)
	client.Close()
	client = dial(t, address, "beam-1", "secret")
	_, _ = client.Subscribe(topic("out/+"), wait)
	_, err = client.WaitFor(topic("out/lamp"), 300*time.Millisecond)
	assert.NotNil(t, err, "nothing is retained any more")

	// Losing the connection without a goodbye marks it offline.
	client.Drop()
	eventually(t, func() bool { info, _ := h.m.Device("beam-1"); return !info.Online }, "the device to go offline")
	assert.NotEmpty(t, h.eventsOf("device.offline"))
}

func TestMqttRefusals(t *testing.T) {
	h := newHarness(t)
	_, address, _ := startMqtt(t, h)
	token, err := h.m.CreateDevice("arcade-1", "")
	require.Nil(t, err)
	_, _ = h.m.Authenticate(MqttTransportName, "blocked-1", "x")
	require.Nil(t, h.m.Block("blocked-1"))

	cases := []struct {
		id, username, password string
		want                   byte
	}{
		{"arcade-1", "arcade-1", "wrong", mqtttest.ConnBadCredentials},
		{"arcade-1", "someone", token, mqtttest.ConnBadCredentials},
		{"blocked-1", "blocked-1", "x", mqtttest.ConnNotAuthorized},
		{"Not_Valid", "Not_Valid", "x", mqtttest.ConnIdentifierRefused},
		{"plc", "plc", "x", mqtttest.ConnIdentifierRefused},
		{"inline", "inline", "x", mqtttest.ConnIdentifierRefused},
		{"arcade-1", "arcade-1", token, mqtttest.ConnAccepted},
	}
	for _, c := range cases {
		options := mqtttest.Options{ClientID: c.id, Username: c.username, Password: c.password}
		client, code, err := mqtttest.Dial(address, options)
		require.Nil(t, err, c.id)
		assert.Equal(t, c.want, code, "%s/%s", c.id, c.username)
		if client != nil {
			client.Close()
		}
	}

	// An approved device may not publish for another, nor read another's outputs.
	client := dial(t, address, "arcade-1", token)
	code, _ := client.Subscribe(TopicPrefix+"dev/beam-1/out/+", wait)
	assert.Equal(t, byte(mqtttest.SubackFailure), code)
	code, _ = client.Subscribe(TopicPrefix+"#", wait)
	assert.Equal(t, byte(mqtttest.SubackFailure), code)
	require.Nil(t, client.Publish(TopicPrefix+"dev/blocked-1/manifest", manifestJson("blocked-1"), false))
	require.Nil(t, client.Ping(wait))
	info, _ := h.m.Device("blocked-1")
	assert.Nil(t, info.Manifest)
}

func TestMqttHeartbeat(t *testing.T) {
	h := newHarness(t)
	_, address, _ := startMqtt(t, h)
	token, err := h.m.CreateDevice("stopbox", "")
	require.Nil(t, err)
	h.bind(`
inputs:
  - { id: stop, device: stopbox, channel: stop, action: { field: estop, station: R1 } }
`)
	client := dial(t, address, "stopbox", token)
	require.Nil(t, client.Publish(TopicPrefix+"dev/stopbox/manifest", manifestJson("stopbox"), true))
	require.Nil(t, client.Publish(TopicPrefix+"dev/stopbox/hb", []byte(`{"seq":1,"ch":{"stop":1}}`), false))
	require.Nil(t, client.Ping(wait))
	assert.Empty(t, h.m.Faults())
	require.Nil(t, client.Publish(TopicPrefix+"dev/stopbox/hb", []byte(`{"seq":2,"ch":{"stop":0}}`), false))
	require.Nil(t, client.Ping(wait))
	assert.True(t, h.m.Stops().Stations["R1"].EStop)
	assert.Equal(t, 1, len(h.m.Faults()))
}

func TestMqttOverWebsocket(t *testing.T) {
	h := newHarness(t)
	_, _, wsAddress := startMqtt(t, h)
	dialer := websocket.Dialer{Subprotocols: []string{"mqtt"}, HandshakeTimeout: wait}
	conn, _, err := dialer.Dial("ws://"+wsAddress+"/mqtt", nil)
	require.Nil(t, err)
	client, code, err := mqtttest.Connect(
		&websocketConn{Conn: conn.UnderlyingConn(), ws: conn},
		mqtttest.Options{ClientID: "browser-1", Username: "browser-1", Password: "x"},
	)
	require.Nil(t, err)
	require.Equal(t, byte(mqtttest.ConnAccepted), code)
	defer client.Drop()
	require.Nil(t, client.Publish(TopicPrefix+"dev/browser-1/manifest", manifestJson("browser-1"), true))
	require.Nil(t, client.Ping(wait))
	info, ok := h.m.Device("browser-1")
	require.True(t, ok)
	assert.True(t, info.Online)
	assert.NotNil(t, info.Manifest)
}

func TestMqttPortsAndRestart(t *testing.T) {
	h := newHarness(t)
	transport, address, _ := startMqtt(t, h)
	token, _ := h.m.CreateDevice("lamp-1", "")
	h.bind(`
outputs:
  - { id: lamp, device: lamp-1, channel: lamp, when: "true" }
`)
	client := dial(t, address, "lamp-1", token)
	require.Nil(t, client.Publish(TopicPrefix+"dev/lamp-1/manifest", manifestJson("lamp-1"), true))
	require.Nil(t, client.Ping(wait))
	h.m.UpdateOutputs(h.snapshot("", 0))

	// A port that is taken is reported, and the broker does not run.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.Nil(t, err)
	defer busy.Close()
	err = transport.Configure(MqttConfig{TCPAddress: busy.Addr().String()})
	assert.ErrorContains(t, err, "cannot listen on")
	status := transport.Status()
	assert.False(t, status.Running)
	assert.Contains(t, status.Error, "cannot listen on")
	eventually(t, func() bool { info, _ := h.m.Device("lamp-1"); return !info.Online }, "the device to go offline")
	statuses := h.m.TransportStatuses()
	assert.Equal(t, 2, len(statuses))

	// Turned off, it says so.
	require.Nil(t, transport.Configure(MqttConfig{}))
	assert.Equal(t, "turned off in the settings", transport.Status().Error)

	// On a new port, it starts again with every output in force retained.
	require.Nil(t, transport.Configure(MqttConfig{TCPAddress: "127.0.0.1:0"}))
	assert.True(t, transport.Status().Running)
	newAddress := strings.TrimPrefix(transport.Addresses()[0], "tcp://")
	client = dial(t, newAddress, "lamp-1", token)
	_, _ = client.Subscribe(TopicPrefix+"dev/lamp-1/out/+", wait)
	message, err := client.WaitFor(TopicPrefix+"dev/lamp-1/out/lamp", wait)
	require.Nil(t, err)
	assert.JSONEq(t, `{"v":1}`, string(message.Payload))
	assert.Equal(t, MqttConfig{TCPAddress: "127.0.0.1:0"}, transport.Config())
	assert.Equal(t, ":1883", MqttAddress(1883))
	assert.Equal(t, "", MqttAddress(0))
}

// The broker library has known bugs in session takeover, when a client connects with the id of a connection that
// is still open. A device that reconnects over and over, overlapping its connections, must neither deadlock the
// broker nor end up offline or deaf.
func TestMqttReconnectLoop(t *testing.T) {
	h := newHarness(t)
	_, address, _ := startMqtt(t, h)
	token, _ := h.m.CreateDevice("flaky-1", "")
	h.bind(`
outputs:
  - { id: lamp, device: flaky-1, channel: lamp, when: "true" }
`)

	done := make(chan *mqtttest.Client)
	go func() {
		var previous []*mqtttest.Client
		var mu sync.Mutex
		var last *mqtttest.Client
		for i := 0; i < 40; i++ {
			client, code, err := mqtttest.Dial(address, mqtttest.Options{
				ClientID: "flaky-1", Username: "flaky-1", Password: token,
				WillTopic: TopicPrefix + "dev/flaky-1/status", WillPayload: []byte(`{"online":false}`),
			})
			if err != nil || code != mqtttest.ConnAccepted {
				t.Errorf("connection %d failed: %v (code %d)", i, err, code)
				break
			}
			_ = client.Publish(TopicPrefix+"dev/flaky-1/manifest", manifestJson("flaky-1"), true)
			_, _ = client.Subscribe(TopicPrefix+"dev/flaky-1/out/+", time.Second)
			// Leave some connections to be taken over, drop others and close the rest cleanly.
			mu.Lock()
			switch i % 3 {
			case 0:
				previous = append(previous, client)
			case 1:
				go client.Drop()
			}
			mu.Unlock()
			last = client
		}
		for _, client := range previous {
			if client != last {
				client.Drop()
			}
		}
		done <- last
	}()

	var last *mqtttest.Client
	select {
	case last = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the reconnect loop deadlocked")
	}
	require.NotNil(t, last)
	defer last.Drop()

	// The last connection is the device's: it is online, and it hears its outputs.
	eventually(t, func() bool {
		select {
		case <-last.Done():
			return true
		default:
		}
		info, _ := h.m.Device("flaky-1")
		return info.Online
	}, "the device to be online")
	select {
	case <-last.Done():
		// The last connection was one of those dropped on purpose; connect once more.
		last = dial(t, address, "flaky-1", token)
		_ = last.Publish(TopicPrefix+"dev/flaky-1/manifest", manifestJson("flaky-1"), true)
		_, _ = last.Subscribe(TopicPrefix+"dev/flaky-1/out/+", wait)
	default:
	}
	require.Nil(t, last.Ping(wait))
	h.m.UpdateOutputs(h.snapshot("", 0))
	message, err := last.WaitFor(TopicPrefix+"dev/flaky-1/out/lamp", wait)
	require.Nil(t, err)
	assert.JSONEq(t, `{"v":1}`, string(message.Payload))
	eventually(t, func() bool { info, _ := h.m.Device("flaky-1"); return info.Online }, "the device to be online")
}

func TestOutbox(t *testing.T) {
	box := newOutbox()
	box.put(outMessage{topic: "a", payload: []byte("1"), coalesce: true})
	box.put(outMessage{topic: "b", payload: []byte("x")})
	box.put(outMessage{topic: "a", payload: []byte("2"), coalesce: true})
	box.put(outMessage{topic: "b", payload: []byte("y")})
	messages := box.take()
	assert.Equal(t, []outMessage{
		{topic: "a", payload: []byte("2"), coalesce: true}, {topic: "b", payload: []byte("x")},
		{topic: "b", payload: []byte("y")},
	}, messages)
	assert.Empty(t, box.take())

	// Messages that do not coalesce are dropped beyond the limit; outputs never are.
	for i := 0; i < outboxLimit+10; i++ {
		box.put(outMessage{topic: "cmd"})
	}
	box.put(outMessage{topic: "out", coalesce: true})
	messages = box.take()
	assert.Equal(t, outboxLimit+1, len(messages))
	assert.Equal(t, "out", messages[len(messages)-1].topic)
}
