package web

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func findVirtualDevice(t *testing.T, web *Web, id string) *virtualDeviceView {
	t.Helper()
	for _, view := range getDevicesPageState(t, web).Virtual {
		if view.ID == id {
			return &view
		}
	}
	return nil
}

// A virtual counter, approved and bound to the ball-count game's balls, scores in a running match.
func TestVirtualCounterScoresInAMatch(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")
	startTestDevices(t, web)

	manifest := map[string]any{
		"proto": 1, "id": "virtual-counter-1", "name": "Virtual beam break",
		"channels": []map[string]any{{"id": "count", "dir": "in", "type": "counter", "name": "Count"}},
	}
	code, response := postDevicesPage(t, web, "/setup/devices/virtual/create", map[string]any{"manifest": manifest})
	require.Equal(t, 200, code, response)
	created := response["device"].(map[string]any)
	assert.Equal(t, "pending", created["state"], "a virtual device needs approval like any other")
	view := findVirtualDevice(t, web, "virtual-counter-1")
	require.NotNil(t, view)
	assert.True(t, view.Connected)
	assert.Equal(t, "galaxia-virtual", view.Manifest.SDK)

	// While it is pending, its counting is refused but kept, as a real sensor keeps counting.
	code, response = postDevicesPage(t, web, "/setup/devices/virtual/virtual-counter-1/input",
		map[string]any{"channel": "count", "add": 1})
	assert.Equal(t, 400, code)
	assert.Equal(t, "the device is waiting for approval", response["error"])
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual-counter-1/approve", nil)
	require.Equal(t, 200, code)

	set, err := device.ParseBindings([]byte(`
inputs:
  - id: red-goal
    device: virtual-counter-1
    channel: count
    action: { score: balls, op: increment, alliance: red }
`))
	require.Nil(t, err)
	problems, err := web.arena.Devices.SaveBindings(set)
	require.Nil(t, err, "%v", problems)

	startTestMatch(t, web)
	assert.Equal(t, "auto", web.arena.CurrentPhase().ID)
	score := func(alliance string) float64 {
		result, _, _ := web.arena.ScoreSnapshot()
		return result.Alliances[alliance].Score
	}
	// The FMS has not heard a total from it yet: the one it holds goes first, so that this ball counts.
	code, response = postDevicesPage(t, web, "/setup/devices/virtual/virtual-counter-1/input",
		map[string]any{"channel": "count", "add": 1})
	require.Equal(t, 200, code, response)
	assert.Equal(t, 2.0, score("red"), "a ball in autonomous is worth two")
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-counter-1/input",
		map[string]any{"channel": "count", "add": 2})
	require.Equal(t, 200, code)
	assert.Equal(t, 6.0, score("red"))
	assert.Equal(t, 0.0, score("blue"))
	assert.Equal(t, 3.0, findVirtualDevice(t, web, "virtual-counter-1").Values["count"])
	info, _ := web.arena.Devices.Device("virtual-counter-1")
	assert.Equal(t, 3.0, info.Inputs["count"].Value)

	code, response = postDevicesPage(t, web, "/setup/devices/virtual/virtual-counter-1/input",
		map[string]any{"channel": "nothing", "add": 1})
	assert.Equal(t, 400, code)
	assert.Equal(t, `"nothing" is not a counter input of virtual device "virtual-counter-1"`, response["error"])
	assert.Equal(t, field.InMatch, web.arena.MatchState)
}

func TestVirtualDeviceLifecycle(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	manifest := map[string]any{"proto": 1, "id": "virtual-estop-1", "name": "Box", "channels": []map[string]any{
		{"id": "stop", "dir": "in", "type": "digital", "safety": true},
		{"id": "light", "dir": "out", "type": "digital"},
	}}
	code, response := postDevicesPage(t, web, "/setup/devices/virtual/create", map[string]any{"manifest": manifest})
	require.Equal(t, 200, code, response)
	view := findVirtualDevice(t, web, "virtual-estop-1")
	require.NotNil(t, view)
	assert.True(t, view.Beating, "a device with a safety channel sends heartbeats from the start")
	assert.Equal(t, map[string]any{"stop": 1.0}, view.Values)

	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/heartbeat", map[string]any{"on": false})
	require.Equal(t, 200, code)
	assert.False(t, findVirtualDevice(t, web, "virtual-estop-1").Beating)
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/heartbeat", map[string]any{"on": true})
	require.Equal(t, 200, code)
	assert.True(t, findVirtualDevice(t, web, "virtual-estop-1").Beating)

	// Switched off, it stays on the page to be switched on again with the same manifest.
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/disconnect", nil)
	require.Equal(t, 200, code)
	view = findVirtualDevice(t, web, "virtual-estop-1")
	require.NotNil(t, view)
	assert.False(t, view.Connected)
	code, response = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/heartbeat",
		map[string]any{"on": true})
	assert.Equal(t, 400, code)
	assert.Equal(t, `virtual device "virtual-estop-1" is not connected`, response["error"])
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/connect", nil)
	require.Equal(t, 200, code)
	view = findVirtualDevice(t, web, "virtual-estop-1")
	assert.True(t, view.Connected)
	assert.Equal(t, "light", view.Manifest.Channels[1].ID)
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/connect", nil)
	assert.Equal(t, 400, code, "it is connected already")

	// Removing it forgets it.
	code, _ = postDevicesPage(t, web, "/setup/devices/virtual/virtual-estop-1/remove", nil)
	require.Equal(t, 200, code)
	assert.Nil(t, findVirtualDevice(t, web, "virtual-estop-1"))
	_, ok := web.arena.Devices.Device("virtual-estop-1")
	assert.False(t, ok)
	assert.Nil(t, web.arena.DeviceVirtual.Device("virtual-estop-1"))
}

func TestVirtualDeviceRefusals(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("real-board", "A real board")
	require.Nil(t, err)

	for _, manifest := range []map[string]any{
		{"id": "real-board", "channels": []any{}},
		{"id": "Not Valid", "channels": []any{}},
		{"id": "x", "channels": []map[string]any{{"id": "a", "dir": "in", "type": "sparkle"}}},
	} {
		code, response := postDevicesPage(t, web, "/setup/devices/virtual/create", map[string]any{"manifest": manifest})
		assert.Equal(t, 400, code, manifest)
		assert.NotEmpty(t, response["error"])
	}
	code, response := postDevicesPage(t, web, "/setup/devices/virtual/create", map[string]any{})
	assert.Equal(t, 400, code)
	assert.Equal(t, "a virtual device needs a manifest", response["error"])
	code, response = postDevicesPage(t, web, "/setup/devices/virtual/real-board/remove", nil)
	assert.Equal(t, 400, code)
	assert.Equal(t, `there is no virtual device "real-board"`, response["error"])
	_, ok := web.arena.Devices.Device("real-board")
	assert.True(t, ok, "a device that is not virtual is not removed from here")
	code, _ = postDevicesPage(
		t, web, "/setup/devices/virtual/nothing/input", map[string]any{"channel": "a", "value": 1},
	)
	assert.Equal(t, 400, code)
}
