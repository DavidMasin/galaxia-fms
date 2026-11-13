package device

import (
	"encoding/base64"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTokens(t *testing.T) {
	token, err := GenerateToken()
	require.Nil(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	assert.Nil(t, err, "URL-safe base64")
	assert.Equal(t, 32, len(raw))
	other, _ := GenerateToken()
	assert.NotEqual(t, token, other)

	// SHA-256 of "abc".
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", HashToken("abc"))
	assert.True(t, tokenMatches(HashToken(token), token))
	assert.False(t, tokenMatches(HashToken(token), other))
	assert.False(t, tokenMatches("", ""), "nothing matches an empty hash")
}

func TestAuthentication(t *testing.T) {
	h := newHarness(t)
	m := h.m

	// Unknown: accepted as pending, and the token remembered.
	access, err := m.Authenticate(MqttTransportName, "beam-red-1", "first")
	assert.Nil(t, err)
	assert.Equal(t, AccessPending, access)
	pending := h.eventsOf("device.pending")
	if assert.Equal(t, 1, len(pending)) {
		assert.Equal(t, "beam-red-1", pending[0]["id"])
	}
	record, _ := h.db.GetDeviceByDeviceId("beam-red-1")
	require.NotNil(t, record, "a pending device is stored")
	assert.Equal(t, model.DevicePending, record.State)
	assert.Equal(t, HashToken("first"), record.TokenHash)

	// Pending: accepted, and the remembered token replaced.
	access, err = m.Authenticate(MqttTransportName, "beam-red-1", "second")
	assert.Nil(t, err)
	assert.Equal(t, AccessPending, access)
	record, _ = h.db.GetDeviceByDeviceId("beam-red-1")
	assert.Equal(t, HashToken("second"), record.TokenHash)

	// Approving pins the token it last presented.
	require.Nil(t, m.Approve("beam-red-1"))
	assert.EqualError(t, m.Approve("beam-red-1"), `device "beam-red-1" is already approved`)
	access, err = m.Authenticate(MqttTransportName, "beam-red-1", "second")
	assert.Nil(t, err)
	assert.Equal(t, AccessApproved, access)
	access, err = m.Authenticate(MqttTransportName, "beam-red-1", "first")
	assert.Equal(t, ErrBadToken, err)
	assert.Equal(t, AccessNone, access)

	// Blocked: refused whatever the token.
	require.Nil(t, m.Block("beam-red-1"))
	_, err = m.Authenticate(MqttTransportName, "beam-red-1", "second")
	assert.Equal(t, ErrBlocked, err)
	_, err = m.AuthenticateLocal(VirtualTransportName, "beam-red-1")
	assert.Equal(t, ErrBlocked, err)
	assert.Equal(t, AccessNone, m.Access("beam-red-1"))

	// Unblocking makes it pending again.
	require.Nil(t, m.Unblock("beam-red-1"))
	assert.Equal(t, AccessPending, m.Access("beam-red-1"))
	assert.EqualError(t, m.Unblock("beam-red-1"), `device "beam-red-1" is not blocked`)

	// Ids that cannot be devices.
	_, err = m.Authenticate(MqttTransportName, "Beam_1", "x")
	assert.Equal(t, ErrInvalidID, err)
	_, err = m.Authenticate(MqttTransportName, PlcDeviceID, "x")
	assert.Equal(t, ErrReservedID, err)

	// A device approved without a token (a virtual one) is accepted locally but cannot be impersonated over MQTT.
	access, err = m.AuthenticateLocal(VirtualTransportName, "local-1")
	assert.Nil(t, err)
	assert.Equal(t, AccessPending, access)
	require.Nil(t, m.Approve("local-1"))
	access, _ = m.AuthenticateLocal(VirtualTransportName, "local-1")
	assert.Equal(t, AccessApproved, access)
	_, err = m.Authenticate(MqttTransportName, "local-1", "")
	assert.Equal(t, ErrBadToken, err)
	_, err = m.Authenticate(MqttTransportName, "local-1", "anything")
	assert.Equal(t, ErrBadToken, err)
}

func TestCreatedDevicesAndTokens(t *testing.T) {
	h := newHarness(t)
	m := h.m

	token, err := m.CreateDevice("arcade-1", "Arcade button")
	require.Nil(t, err)
	access, err := m.Authenticate(MqttTransportName, "arcade-1", token)
	assert.Nil(t, err)
	assert.Equal(t, AccessApproved, access)
	info, ok := m.Device("arcade-1")
	if assert.True(t, ok) {
		assert.Equal(t, "Arcade button", info.Name)
		assert.Equal(t, model.DeviceApproved, info.State)
		assert.True(t, info.HasToken)
	}
	_, err = m.CreateDevice("arcade-1", "")
	assert.EqualError(t, err, `there is already a device "arcade-1"`)
	_, err = m.CreateDevice("Arcade", "")
	assert.ErrorContains(t, err, "is not a valid device id")
	_, err = m.CreateDevice(PlcDeviceID, "")
	assert.ErrorContains(t, err, "reserved for the field PLC")

	// A new token replaces the old one.
	newToken, err := m.RegenerateToken("arcade-1")
	require.Nil(t, err)
	_, err = m.Authenticate(MqttTransportName, "arcade-1", token)
	assert.Equal(t, ErrBadToken, err)
	_, err = m.Authenticate(MqttTransportName, "arcade-1", newToken)
	assert.Nil(t, err)

	// Everything is stored, and a new manager on the same database knows it.
	require.Nil(t, m.SetRequired("arcade-1", true))
	require.Nil(t, m.SetNotes("arcade-1", "By the scoring table"))
	require.Nil(t, m.SetName("arcade-1", "Start button"))
	reloaded, err := NewManager(Options{Database: h.db})
	require.Nil(t, err)
	info, ok = reloaded.Device("arcade-1")
	if assert.True(t, ok) {
		assert.Equal(t, "Start button", info.Name)
		assert.True(t, info.Required)
		assert.Equal(t, "By the scoring table", info.Notes)
	}
	access, err = reloaded.Authenticate(MqttTransportName, "arcade-1", newToken)
	assert.Nil(t, err)
	assert.Equal(t, AccessApproved, access)

	// Deleting forgets the device; connecting again makes a new, pending one.
	require.Nil(t, m.Delete("arcade-1"))
	_, ok = m.Device("arcade-1")
	assert.False(t, ok)
	record, _ := h.db.GetDeviceByDeviceId("arcade-1")
	assert.Nil(t, record)
	access, _ = m.Authenticate(MqttTransportName, "arcade-1", newToken)
	assert.Equal(t, AccessPending, access)
	assert.EqualError(t, m.Delete("nobody"), `there is no device "nobody"`)
}

func TestMqttAccessControl(t *testing.T) {
	h := newHarness(t)
	transport := NewMqttTransport(MqttConfig{})
	transport.sink = h.m
	_, _ = h.m.Authenticate(MqttTransportName, "approved-1", "t")
	require.Nil(t, h.m.Approve("approved-1"))
	_, _ = h.m.Authenticate(MqttTransportName, "pending-1", "t")

	dev := func(id, rest string) string { return TopicPrefix + "dev/" + id + "/" + rest }
	cases := []struct {
		client string
		topic  string
		write  bool
		want   bool
	}{
		// What an approved device may publish.
		{"approved-1", dev("approved-1", "manifest"), true, true},
		{"approved-1", dev("approved-1", "status"), true, true},
		{"approved-1", dev("approved-1", "in/count"), true, true},
		{"approved-1", dev("approved-1", "hb"), true, true},
		{"approved-1", dev("approved-1", "in/Bad"), true, false},
		{"approved-1", dev("approved-1", "in/a/b"), true, false},
		{"approved-1", dev("approved-1", "out/strip"), true, false},
		{"approved-1", dev("approved-1", "cmd"), true, false},
		{"approved-1", dev("pending-1", "in/count"), true, false},
		{"approved-1", TopicPrefix + "field/state", true, false},
		{"approved-1", "elsewhere/topic", true, false},
		// What it may subscribe to and receive.
		{"approved-1", dev("approved-1", "out/+"), false, true},
		{"approved-1", dev("approved-1", "out/strip"), false, true},
		{"approved-1", dev("approved-1", "cmd"), false, true},
		{"approved-1", TopicPrefix + "field/#", false, true},
		{"approved-1", TopicPrefix + "field/+", false, true},
		{"approved-1", TopicPrefix + "field/clock", false, true},
		{"approved-1", TopicPrefix + "field", false, true},
		{"approved-1", dev("approved-1", "out/#"), false, false},
		{"approved-1", dev("approved-1", "#"), false, false},
		{"approved-1", dev("approved-1", "in/count"), false, false},
		{"approved-1", dev("pending-1", "out/+"), false, false},
		{"approved-1", TopicPrefix + "#", false, false},
		{"approved-1", "#", false, false},
		{"approved-1", "$SYS/#", false, false},
		// A pending device may only announce itself and follow the field.
		{"pending-1", dev("pending-1", "manifest"), true, true},
		{"pending-1", dev("pending-1", "status"), true, true},
		{"pending-1", dev("pending-1", "in/count"), true, false},
		{"pending-1", dev("pending-1", "hb"), true, false},
		{"pending-1", TopicPrefix + "field/#", false, true},
		{"pending-1", dev("pending-1", "out/+"), false, false},
		{"pending-1", dev("pending-1", "cmd"), false, false},
		// Anyone else may do nothing.
		{"stranger", dev("stranger", "manifest"), true, false},
		{"stranger", TopicPrefix + "field/#", false, false},
	}
	for _, c := range cases {
		allowed := transport.allowed(c.client, c.topic, c.write)
		assert.Equal(t, c.want, allowed, "%s write=%t %s", c.client, c.write, c.topic)
	}

	// The connection rules: the client id must be the username.
	assert.Equal(t, ErrInvalidID, transport.authenticate("Bad Id", "Bad Id", nil))
	assert.Equal(t, ErrBadToken, transport.authenticate("approved-1", "someone-else", []byte("t")))
	assert.Nil(t, transport.authenticate("approved-1", "approved-1", []byte("t")))
	assert.Equal(t, ErrBadToken, transport.authenticate("approved-1", "approved-1", []byte("wrong")))
}

func TestFilterCovers(t *testing.T) {
	cases := []struct {
		outer, inner string
		want         bool
	}{
		{"a/b/#", "a/b", true},
		{"a/b/#", "a/b/c/d", true},
		{"a/b/#", "a/b/#", true},
		{"a/b/#", "a/b/+", true},
		{"a/b/#", "a/#", false},
		{"a/b/#", "a/+/c", false},
		{"a/+/c", "a/x/c", true},
		{"a/+/c", "a/+/c", true},
		{"a/+/c", "a/#", false},
		{"a/+/c", "a/x/c/d", false},
		{"a/+/c", "a/x", false},
		{"a/b", "a/b", true},
		{"a/b", "a/c", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, filterCovers(c.outer, c.inner), "%s covers %s", c.outer, c.inner)
	}
}
