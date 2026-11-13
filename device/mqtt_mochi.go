// The MQTT broker library (mochi-mqtt) behind the broker interface. Nothing else in the FMS uses the library, so it
// can be replaced by writing another file like this one.

package device

import (
	"errors"
	"fmt"
	"github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"log"
	"log/slog"
	"strings"
)

const (
	// Sessions a device left without a clean start are forgotten after this long; devices are meant to connect clean.
	mochiSessionExpirySec = 60
	// The library publishes statistics under $SYS, which no device may read; rarely is often enough.
	mochiSysTopicPeriodSec = 3600
)

type mochiBroker struct {
	server    *mqtt.Server
	listeners []listeners.Listener
}

// mochiConn is a client of the library. It is a comparable value, so that two of them are equal exactly when they
// hold the same client.
type mochiConn struct {
	client *mqtt.Client
}

func (conn mochiConn) ClientID() string { return conn.client.ID }
func (conn mochiConn) Remote() string   { return conn.client.Net.Remote }

func startMochiBroker(config MqttConfig, handler brokerHandler) (broker, error) {
	capabilities := mqtt.NewDefaultServerCapabilities()
	capabilities.MaximumSessionExpiryInterval = mochiSessionExpirySec
	server := mqtt.New(&mqtt.Options{
		InlineClient: true, Capabilities: capabilities, SysTopicResendInterval: mochiSysTopicPeriodSec,
		Logger: slog.New(slog.NewTextHandler(logWriter{}, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	b := &mochiBroker{server: server}
	if err := server.AddHook(&mochiHook{handler: handler}, nil); err != nil {
		return nil, err
	}
	if config.TCPAddress != "" {
		b.listeners = append(b.listeners, listeners.NewTCP(listeners.Config{ID: "tcp", Address: config.TCPAddress}))
	}
	if config.WSAddress != "" {
		b.listeners = append(b.listeners, newWebsocketListener("ws", config.WSAddress))
	}
	for _, listener := range b.listeners {
		if err := server.AddListener(listener); err != nil {
			_ = server.Close()
			return nil, fmt.Errorf("cannot listen on %s for MQTT: %w", listener.Address(), err)
		}
	}

	// Inline subscriptions hand what devices publish straight to the handler, on the publishing client's own
	// goroutine, so that the messages of one device are handled in the order it sent them.
	deliver := func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
		handler.message(pk.Origin, pk.TopicName, pk.Payload)
	}
	for i, filter := range []string{"dev/+/manifest", "dev/+/status", "dev/+/in/+", "dev/+/hb"} {
		if err := server.Subscribe(TopicPrefix+filter, i+1, deliver); err != nil {
			_ = server.Close()
			return nil, err
		}
	}
	if err := server.Serve(); err != nil {
		_ = server.Close()
		return nil, err
	}
	return b, nil
}

func (b *mochiBroker) Addresses() []string {
	addresses := make([]string, len(b.listeners))
	for i, listener := range b.listeners {
		addresses[i] = listener.Protocol() + "://" + listener.Address()
	}
	return addresses
}

func (b *mochiBroker) Publish(topic string, payload []byte, retain bool) error {
	return b.server.Publish(topic, payload, retain, 0)
}

func (b *mochiBroker) Disconnect(conn brokerConn) {
	client := conn.(mochiConn).client
	if client.Properties.ProtocolVersion == 5 {
		_ = b.server.DisconnectClient(client, packets.ErrAdministrativeAction)
		return
	}
	// MQTT 3.1.1 has no disconnect from the server: the connection is just closed.
	client.Stop(packets.ErrAdministrativeAction)
}

// Repair works around a race in the library's session takeover: when a device reconnects at the moment its old
// connection is being torn down, the old connection's clean-up can remove the new one from the client list, or its
// subscriptions from the topic index (both are keyed by client id), after which nothing published reaches it. A
// connection that is still open gets back what it lost.
func (b *mochiBroker) Repair(conns []brokerConn) {
	for _, conn := range conns {
		client := conn.(mochiConn).client
		if client.Closed() {
			continue
		}
		if _, ok := b.server.Clients.Get(client.ID); !ok {
			b.server.Clients.Add(client)
		}
		for _, subscription := range client.State.Subscriptions.GetAll() {
			b.server.Topics.Subscribe(client.ID, subscription)
		}
	}
}

func (b *mochiBroker) Close() {
	_ = b.server.Close()
}

// mochiHook is how the library asks the transport about clients.
type mochiHook struct {
	mqtt.HookBase
	handler brokerHandler
}

func (h *mochiHook) ID() string {
	return "galaxia-devices"
}

func (h *mochiHook) Provides(b byte) bool {
	switch b {
	case mqtt.OnConnect, mqtt.OnConnectAuthenticate, mqtt.OnACLCheck, mqtt.OnSessionEstablished, mqtt.OnDisconnect:
		return true
	}
	return false
}

// OnConnect decides whether a client may connect. It is used rather than OnConnectAuthenticate because it can refuse
// with the right reason: the library answers every refused authentication with the same code.
func (h *mochiHook) OnConnect(client *mqtt.Client, pk packets.Packet) error {
	err := ErrReservedID
	if client.ID != mqtt.InlineClientId {
		err = h.handler.authenticate(client.ID, string(client.Properties.Username), pk.Connect.Password)
	}
	if err == nil {
		return nil
	}
	code := connackCode(err, client.Properties.ProtocolVersion)
	_ = client.WritePacket(packets.Packet{FixedHeader: packets.FixedHeader{Type: packets.Connack}, ReasonCode: code})
	return fmt.Errorf("refused device %q: %w", client.ID, err)
}

// connackCode is the CONNACK return code (MQTT 3.1.1) or reason code (MQTT 5) for a refusal.
func connackCode(err error, protocolVersion byte) byte {
	v5 := protocolVersion == 5
	switch {
	case errors.Is(err, ErrBadToken):
		if v5 {
			return packets.ErrBadUsernameOrPassword.Code
		}
		return packets.ErrMalformedUsernameOrPassword.Code // 0x04, "bad user name or password" in 3.1.1.
	case errors.Is(err, ErrInvalidID), errors.Is(err, ErrReservedID):
		if v5 {
			return packets.ErrClientIdentifierNotValid.Code
		}
		return packets.Err3ClientIdentifierNotValid.Code
	}
	if v5 {
		return packets.ErrNotAuthorized.Code
	}
	return packets.Err3NotAuthorized.Code
}

func (h *mochiHook) OnConnectAuthenticate(client *mqtt.Client, pk packets.Packet) bool {
	// OnConnect has already refused any client that may not connect.
	return true
}

func (h *mochiHook) OnACLCheck(client *mqtt.Client, topic string, write bool) bool {
	return h.handler.allowed(client.ID, topic, write)
}

func (h *mochiHook) OnSessionEstablished(client *mqtt.Client, pk packets.Packet) {
	h.handler.connected(mochiConn{client: client})
}

// OnDisconnect follows every disconnection, including one that sends the client's Last Will, so presence is taken
// from here rather than from the will.
func (h *mochiHook) OnDisconnect(client *mqtt.Client, err error, expire bool) {
	h.handler.disconnected(mochiConn{client: client})
}

// logWriter sends the library's log lines to the FMS log.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	log.Printf("MQTT broker: %s", strings.TrimSpace(string(p)))
	return len(p), nil
}
