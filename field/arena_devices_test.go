package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/device/mqtttest"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const deviceWait = 5 * time.Second

// startDevices starts the arena's device transports as Run would, but with the MQTT broker on a free local port, or
// off, rather than on the ports from the settings.
func startDevices(t *testing.T, arena *Arena, mqtt bool) string {
	t.Helper()
	config := device.MqttConfig{}
	if mqtt {
		config.TCPAddress = "127.0.0.1:0"
	}
	require.Nil(t, arena.DeviceMqtt.Configure(config))
	require.Nil(t, arena.Devices.Start())
	t.Cleanup(arena.Devices.Stop)
	if !mqtt {
		return ""
	}
	return strings.TrimPrefix(arena.DeviceMqtt.Addresses()[0], "tcp://")
}

// bindDevices saves a binding set for the active game, which must have no errors.
func bindDevices(t *testing.T, arena *Arena, source string) {
	t.Helper()
	set, err := device.ParseBindings([]byte(source))
	require.Nil(t, err)
	problems, err := arena.Devices.SaveBindings(set)
	require.Nil(t, err, "%v", problems)
}

// bypassAll lets a match start without driver stations.
func bypassAll(arena *Arena) {
	for _, station := range arena.AllianceStations {
		station.Bypass = true
	}
}

func waitUntil(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(deviceWait)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDeviceSettings(t *testing.T) {
	arena := setupTestArena(t)
	assert.Equal(t, "default", arena.Devices.Profile())
	assert.Equal(t, device.MqttConfig{TCPAddress: ":1883", WSAddress: ":8083"}, arena.DeviceMqtt.Config())

	arena.EventSettings.FieldProfile = "tent"
	arena.EventSettings.DeviceMqttWsPort = 0
	require.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	require.Nil(t, arena.LoadSettings())
	assert.Equal(t, "tent", arena.Devices.Profile())
	assert.Equal(t, device.MqttConfig{TCPAddress: ":1883"}, arena.DeviceMqtt.Config())
	assert.False(t, arena.DeviceMqtt.Status().Running, "a test arena never opens a port")

	// Bindings are kept per game and per profile.
	bindDevices(t, arena, `
inputs:
  - { id: start, device: arcade, channel: button, action: { field: start_match } }
`)
	assert.Equal(t, 1, len(arena.Devices.Bindings().Set.Inputs))
	arena.EventSettings.FieldProfile = "default"
	require.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	require.Nil(t, arena.LoadSettings())
	assert.Empty(t, arena.Devices.Bindings().Set.Inputs)
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(t, "ball-count", arena.Devices.Bindings().Game)
}

// A real MQTT client connects to the embedded broker, is held pending, is approved, and its counter changes the live
// score of a running match.
func TestDeviceMqttScoresInMatch(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	address := startDevices(t, arena, true)
	topic := func(rest string) string { return device.TopicPrefix + "dev/beam-red-1/" + rest }
	manifest := []byte(`{"proto":1,"id":"beam-red-1","name":"Red goal","channels":[
		{"id":"count","dir":"in","type":"counter","name":"Balls"}]}`)
	connect := func(token string) *mqtttest.Client {
		client, code, err := mqtttest.Dial(address, mqtttest.Options{
			ClientID: "beam-red-1", Username: "beam-red-1", Password: token, WillTopic: topic("status"),
			WillPayload: []byte(`{"online":false}`), WillRetain: true,
		})
		require.Nil(t, err)
		require.Equal(t, byte(mqtttest.ConnAccepted), code)
		t.Cleanup(client.Drop)
		require.Nil(t, client.Publish(topic("manifest"), manifest, true))
		require.Nil(t, client.Ping(deviceWait))
		return client
	}

	client := connect("its-token")
	info, _ := arena.Devices.Device("beam-red-1")
	assert.Equal(t, "pending", info.State)
	require.Nil(t, arena.Devices.Approve("beam-red-1"))
	<-client.Done()
	client = connect("its-token")
	info, _ = arena.Devices.Device("beam-red-1")
	assert.Equal(t, "approved", info.State)

	// A wrong token is refused now that the device is approved.
	options := mqtttest.Options{ClientID: "beam-red-1", Username: "beam-red-1", Password: "x"}
	_, code, err := mqtttest.Dial(address, options)
	require.Nil(t, err)
	assert.Equal(t, byte(mqtttest.ConnBadCredentials), code)

	bindDevices(t, arena, `
inputs:
  - { id: red-goal, device: beam-red-1, channel: count, action: { score: balls, op: increment, alliance: red } }
`)
	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":0}`), false))
	bypassAll(arena)
	require.Nil(t, arena.StartMatch())
	arena.Update()
	runMatchTo(arena, 20)
	assert.Equal(t, "teleop", arena.CurrentPhase().ID)

	require.Nil(t, client.Publish(topic("in/count"), []byte(`{"v":3,"seq":7}`), false))
	require.Nil(t, client.Ping(deviceWait))
	result, _, _ := arena.ScoreSnapshot()
	assert.Equal(t, 3.0, result.Alliances["red"].Score)
	assert.Equal(t, 0.0, result.Alliances["blue"].Score)
	arena.scoreMutex.Lock()
	changes := append([]scoring.Change(nil), arena.Score.Changes...)
	arena.scoreMutex.Unlock()
	if assert.Equal(t, 1, len(changes)) {
		assert.Equal(t, scoring.Source{Kind: "device", ID: "beam-red-1", Name: "Red goal · Balls"}, changes[0].Source)
		assert.Equal(t, "teleop", changes[0].Phase)
	}
}

// A virtual E-stop box whose safety channel is bound to R1 stops sending heartbeats in a match: R1 is E-stopped, a
// fault is latched, and the next match cannot start until the box is back and the fault cleared.
func TestDeviceEStopHeartbeat(t *testing.T) {
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	box, err := arena.DeviceVirtual.Connect(&device.Manifest{
		Proto: 1, ID: "estop-red", Name: "Red E-stop",
		Channels: []*device.Channel{{ID: "stop", Dir: device.DirIn, Type: device.TypeDigital, Safety: true}},
	})
	require.Nil(t, err)
	require.Nil(t, arena.Devices.Approve("estop-red"))
	bindDevices(t, arena, `
inputs:
  - { id: estop-r1, device: estop-red, channel: stop, action: { field: estop, station: R1 } }
`)
	box.StartHeartbeat(device.HeartbeatPeriod)
	defer box.StopHeartbeat()

	bypassAll(arena)
	require.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.False(t, arena.AllianceStations["R1"].EStop)

	// The box hangs.
	box.StopHeartbeat()
	time.Sleep(device.HeartbeatTimeout + 200*time.Millisecond)
	arena.Update()
	assert.True(t, arena.AllianceStations["R1"].EStop)
	assert.False(t, arena.AllianceStations["R2"].EStop)
	faults := arena.Devices.Faults()
	if assert.Equal(t, 1, len(faults)) {
		assert.Equal(t, "estop-r1", faults[0].Binding)
		assert.Contains(t, faults[0].Reason, "no heartbeat")
	}

	// The E-stop stays latched for the match, and the next one cannot start.
	box.StartHeartbeat(device.HeartbeatPeriod)
	arena.Update()
	assert.True(t, arena.AllianceStations["R1"].EStop)
	require.Nil(t, arena.AbortMatch())
	arena.Update()
	require.Nil(t, arena.ResetMatch())
	arena.Update()
	bypassAll(arena) // Resetting the match clears the bypasses.
	err = arena.StartMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), `stop device estop-red "Red E-stop" (E-stop R1) has a fault`)
		assert.Contains(t, err.Error(), "an emergency stop is active (R1)")
	}

	// Once the operator clears the fault of a box that is back and safe, the match can start.
	require.Nil(t, arena.Devices.ClearFaults("estop-red"))
	arena.Update()
	assert.False(t, arena.AllianceStations["R1"].EStop)
	assert.Nil(t, arena.StartMatch())
}

func TestDeviceStopsWithPlc(t *testing.T) {
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	plc := &FakePlc{isEnabled: true, ftaReady: true}
	arena.Plc = plc
	box, err := arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "stops", Channels: []*device.Channel{
		{ID: "b1", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "field", Dir: device.DirIn, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	require.Nil(t, arena.Devices.Approve("stops"))
	bindDevices(t, arena, `
inputs:
  - { id: b1, device: stops, channel: b1, action: { field: estop, station: B1 } }
  - { id: field, device: stops, channel: field, action: { field: field_stop } }
`)

	// The PLC's stops and the device's are combined: either stops a station.
	require.Nil(t, box.SetInput("b1", 1))
	arena.Update()
	assert.True(t, arena.AllianceStations["B1"].EStop)
	require.Nil(t, box.SetInput("b1", 0))
	plc.blueEStops[0] = true
	arena.Update()
	assert.True(t, arena.AllianceStations["B1"].EStop)
	plc.blueEStops[0] = false
	arena.Update()
	assert.False(t, arena.AllianceStations["B1"].EStop)

	// A device's field stop aborts the match, like the PLC's.
	bypassAll(arena)
	require.Nil(t, arena.StartMatch())
	arena.Update()
	runMatchTo(arena, 5)
	require.Nil(t, box.SetInput("field", 1))
	arena.Update()
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.True(t, arena.matchAborted)
	assert.Contains(t, arena.getStartMatchConditions(), "a field stop from device stops is active")
}

func TestDeviceFieldActions(t *testing.T) {
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	box, err := arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "table", Channels: []*device.Channel{
		{ID: "start", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "abort", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "commit", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "next", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "reset", Dir: device.DirIn, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	require.Nil(t, arena.Devices.Approve("table"))
	bindDevices(t, arena, `
inputs:
  - { id: start, device: table, channel: start, action: { field: start_match } }
  - { id: abort, device: table, channel: abort, action: { field: abort_match } }
  - { id: commit, device: table, channel: commit, action: { field: commit } }
  - { id: next, device: table, channel: next, action: { field: next_match } }
  - { id: reset, device: table, channel: reset, action: { field: field_reset } }
`)
	press := func(channel string) {
		require.Nil(t, box.SetInput(channel, 1))
		require.Nil(t, box.SetInput(channel, 0))
	}
	refusal := func() string {
		info, _ := arena.Devices.Device("table")
		if info.Refusal == nil {
			return ""
		}
		return info.Refusal.Binding + ": " + info.Refusal.Reason
	}

	// A start that the arena refuses is a refusal on the device.
	press("start")
	arena.Update()
	assert.Equal(t, PreMatch, arena.MatchState)
	assert.Contains(t, refusal(), "start: start_match: cannot start match: not all robots are connected or bypassed")

	bypassAll(arena)
	press("start")
	arena.Update()
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	press("reset")
	arena.Update()
	assert.Equal(t, "reset: field_reset: not while a match is in progress", refusal())
	press("abort")
	arena.Update()
	assert.Equal(t, PostMatch, arena.MatchState)
	press("reset")
	arena.Update()
	assert.True(t, arena.FieldReset)

	// Commit and next match are carried out by handlers the web layer registers, off the arena loop.
	press("commit")
	arena.Update()
	waitUntil(t, func() bool { return strings.Contains(refusal(), "nothing has been set up to carry out commit") },
		"the missing handler to be reported")
	var commits atomic.Int32
	require.Nil(t, arena.SetFieldActionHandler(device.ActionCommit, func() error {
		commits.Add(1)
		return nil
	}))
	require.Nil(t, arena.SetFieldActionHandler(device.ActionNextMatch, func() error {
		return fmt.Errorf("the current match is not committed")
	}))
	assert.NotNil(t, arena.SetFieldActionHandler(device.ActionStartMatch, func() error { return nil }))
	press("commit")
	arena.Update()
	waitUntil(t, func() bool { return commits.Load() == 1 }, "the commit handler")
	press("next")
	arena.Update()
	waitUntil(t, func() bool { return refusal() == "next: next_match: the current match is not committed" },
		"the next match handler's refusal")
}

func TestDeviceOutputsFollowTheMatch(t *testing.T) {
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	stack, err := arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "stack", Channels: []*device.Channel{
		{ID: "green", Dir: device.DirOut, Type: device.TypeDigital},
		{ID: "horn", Dir: device.DirOut, Type: device.TypeRelay},
		{ID: "ready", Dir: device.DirOut, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	require.Nil(t, arena.Devices.Approve("stack"))
	bindDevices(t, arena, `
outputs:
  - { id: green, device: stack, channel: green, when: "match_state == 'running'" }
  - { id: horn, device: stack, channel: horn, on: match.start, pulse_ms: 60000 }
  - { id: ready, device: stack, channel: ready, when: "stations.R1.bypass and field_reset == false" }
`)
	update := func() {
		for i := 0; i < deviceOutputTicks; i++ {
			arena.Update()
		}
	}
	on, off := 1.0, 0.0
	update()
	assert.Equal(t, &device.Command{V: &off}, stack.Output("green"))
	assert.Equal(t, &device.Command{V: &off}, stack.Output("horn"))

	bypassAll(arena)
	require.Nil(t, arena.StartMatch())
	update()
	assert.Equal(t, &device.Command{V: &on}, stack.Output("green"))
	assert.Equal(t, &device.Command{V: &on}, stack.Output("horn"), "pulsed by match.start")
	assert.Equal(t, &device.Command{V: &on}, stack.Output("ready"))
	field := stack.Field()
	if assert.NotNil(t, field) {
		assert.Equal(t, "running", field.State)
		assert.Equal(t, "auto", field.Phase)
	}
}

func TestDeviceStartConditions(t *testing.T) {
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	_, err := arena.Devices.CreateDevice("scoreboard", "Scoreboard")
	require.Nil(t, err)
	require.Nil(t, arena.Devices.SetRequired("scoreboard", true))
	bypassAll(arena)
	err = arena.StartMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), `required device scoreboard "Scoreboard" is offline`)
	}
	_, err = arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "scoreboard"})
	require.Nil(t, err)
	assert.Nil(t, arena.StartMatch())
}
