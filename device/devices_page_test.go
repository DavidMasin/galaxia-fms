package device

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVirtualDevicesForThePage(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", safety("stop"), &Channel{ID: "count", Dir: DirIn, Type: TypeCounter, Name: "Count"})
	h.connect("arcade", digital("button"))

	devices := h.virtual.Devices()
	if assert.Equal(t, 2, len(devices)) {
		assert.Equal(t, "arcade", devices[0].ID())
		assert.Equal(t, "box", devices[1].ID())
	}

	// The manifest is a copy.
	manifest := box.Manifest()
	assert.Equal(t, "Count", manifest.Channel("count").Name)
	manifest.Channels[1].Name = "Changed"
	assert.Equal(t, "Count", box.Manifest().Channel("count").Name)

	// A safety channel starts closed; values are kept on the device's side even when the FMS refuses them.
	assert.Equal(t, map[string]any{"stop": 1.0}, box.Values())
	require.Nil(t, box.SetInput("count", 3))
	assert.Equal(t, map[string]any{"stop": 1.0, "count": 3.0}, box.Values())
	_, err := h.m.AuthenticateLocal(VirtualTransportName, "newcomer")
	require.Nil(t, err)
	newcomer, err := h.virtual.Connect(&Manifest{Proto: 1, ID: "newcomer", Channels: []*Channel{digital("button")}})
	require.Nil(t, err)
	assert.Error(t, newcomer.SetInput("button", 1))
	assert.Equal(t, map[string]any{"button": 1.0}, newcomer.Values())

	assert.False(t, box.Beating())
	box.StartHeartbeat(0)
	assert.True(t, box.Beating())
	box.StopHeartbeat()
	assert.False(t, box.Beating())

	box.Disconnect()
	assert.Equal(t, 2, len(h.virtual.Devices()))
}
