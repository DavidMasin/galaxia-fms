package device

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

const stopBindings = `
inputs:
  - { id: estop-r1, device: stopbox, channel: stop, action: { field: estop, station: R1 } }
  - { id: astop-b2, device: stopbox, channel: auto, action: { field: astop, station: B2 } }
  - { id: field, device: stopbox, channel: field, action: { field: field_stop } }
`

func stopBox(h *harness) *VirtualDevice {
	return h.connect("stopbox", safety("stop"), digital("auto"), digital("field"))
}

func TestStops(t *testing.T) {
	h := newHarness(t)
	box := stopBox(h)
	h.bind(stopBindings)
	require.Nil(t, box.Heartbeat())

	// Every station a device can stop is listed, stopped or not.
	assert.Equal(t, Stops{Stations: map[string]StationStop{"R1": {}, "B2": {}}}, h.m.Stops())

	// A plain input stops while it is 1, and lets go when it is 0 again.
	require.Nil(t, box.SetInput("auto", 1))
	assert.Equal(t, StationStop{AStop: true}, h.m.Stops().Stations["B2"])
	require.Nil(t, box.SetInput("auto", 0))
	assert.Equal(t, StationStop{}, h.m.Stops().Stations["B2"])
	require.Nil(t, box.SetInput("field", 1))
	assert.True(t, h.m.Stops().FieldStop)
	assert.Contains(t, h.m.StartConditions(), `a field stop from device stopbox "Test stopbox" is active`)
	require.Nil(t, box.SetInput("field", 0))
	assert.False(t, h.m.Stops().FieldStop)
	assert.Empty(t, h.m.Faults(), "a plain input is a stop, not a fault")

	// A safety channel stops when it reads 0, and that latches a fault which holds the stop after it reads 1 again.
	require.Nil(t, box.SetInput("stop", 0))
	assert.Equal(t, StationStop{EStop: true}, h.m.Stops().Stations["R1"])
	faults := h.m.Faults()
	if assert.Equal(t, 1, len(faults)) {
		assert.Equal(t, Fault{
			Device: "stopbox", Binding: "estop-r1", Time: h.clock.Now(),
			Reason: "its safety channel stop read 0: the stop was pressed or its circuit opened",
		}, faults[0])
	}
	assert.Equal(t, 1, len(h.eventsOf("device.fault")))
	require.Nil(t, box.SetInput("stop", 1))
	assert.Equal(t, StationStop{EStop: true}, h.m.Stops().Stations["R1"], "held by the fault")
	assert.True(t, h.m.Faulted())

	// The operator clears it once the device is back and safe.
	require.Nil(t, h.m.ClearFaults("stopbox"))
	assert.Equal(t, StationStop{}, h.m.Stops().Stations["R1"])
	assert.Equal(t, []map[string]any{{
		"id": "stopbox", "binding": "estop-r1",
		"reason": "its safety channel stop read 0: the stop was pressed or its circuit opened",
	}}, h.eventsOf("device.fault_cleared"))
	assert.EqualError(t, h.m.ClearFaults("stopbox"), `device "stopbox" has no fault`)
}

func TestHeartbeatWatchdog(t *testing.T) {
	h := newHarness(t)
	box := stopBox(h)
	h.bind(stopBindings)
	start := h.clock.Now()

	// Heartbeats keep the watchdog quiet.
	for i := 1; i <= 10; i++ {
		h.clock.Advance(HeartbeatPeriod)
		require.Nil(t, box.Heartbeat())
		h.m.Tick(h.clock.Now())
	}
	assert.Empty(t, h.m.Faults())
	info, _ := h.m.Device("stopbox")
	assert.Equal(t, start.Add(10*HeartbeatPeriod), info.Heartbeat)

	// One second of silence stops the robot and latches a fault on every stop bound to the device.
	h.m.Tick(h.clock.Now().Add(HeartbeatTimeout))
	assert.Empty(t, h.m.Faults(), "exactly one second is not yet silence")
	h.m.Tick(h.clock.Advance(HeartbeatTimeout + time.Millisecond))
	faults := h.m.Faults()
	if assert.Equal(t, 3, len(faults)) {
		assert.Equal(t, "no heartbeat for 1.0 seconds", faults[0].Reason)
	}
	stops := h.m.Stops()
	want := Stops{Stations: map[string]StationStop{"R1": {EStop: true}, "B2": {AStop: true}}, FieldStop: true}
	assert.Equal(t, want, stops)
	conditions := h.m.StartConditions()
	assert.Contains(t, conditions, `stop device stopbox "Test stopbox" (E-stop R1, A-stop B2, field stop) has a fault `+
		`(no heartbeat for 1.0 seconds); clear it on the Devices page once it is `+
		`safe`)

	// It cannot be cleared while the device is still silent.
	assert.EqualError(t, h.m.ClearFaults("stopbox"), `device "stopbox" is not sending heartbeats`)
	require.Nil(t, box.Heartbeat())
	require.Nil(t, h.m.ClearFaults("stopbox"))
	assert.Empty(t, h.m.StartConditions())
}

func TestDisconnectFaults(t *testing.T) {
	h := newHarness(t)
	box := stopBox(h)
	h.bind(stopBindings)
	require.Nil(t, box.Heartbeat())
	assert.Empty(t, h.m.StartConditions())

	box.Disconnect()
	faults := h.m.Faults()
	if assert.Equal(t, 3, len(faults)) {
		assert.Equal(t, "it disconnected", faults[0].Reason)
	}
	assert.True(t, h.m.Stops().Stations["R1"].EStop)
	conditions := h.m.StartConditions()
	assert.Equal(t, `stop device stopbox "Test stopbox" (E-stop R1, A-stop B2, field stop) is offline`, conditions[0])
	err := h.m.ClearFaults("stopbox")
	assert.EqualError(t, err, `device "stopbox" is offline; its fault can be cleared once it is back`)
	assert.Equal(t, []map[string]any{{"id": "stopbox", "transport": "virtual"}}, h.eventsOf("device.offline"))

	// Back, but still reporting its stop as pressed.
	box = stopBox(h)
	require.Nil(t, box.SetInput("auto", 1))
	assert.EqualError(t, h.m.ClearFaults("stopbox"), `device "stopbox" still reports its stop (auto) as active`)
	require.Nil(t, box.SetInput("auto", 0))
	assert.Nil(t, h.m.ClearAllFaults())
	assert.Empty(t, h.m.Faults())
	assert.False(t, h.m.Stops().Stations["R1"].EStop)

	// A connection replaced before the old one was seen to go is a dropped connection too.
	h.m.Online(VirtualTransportName, "stopbox", "virtual")
	assert.Equal(t, "it reconnected, so its connection had dropped", h.m.Faults()[0].Reason)
}

func TestStartConditions(t *testing.T) {
	h := newHarness(t)
	h.bind(stopBindings + `
  - { id: other, device: newbox, channel: stop, action: { field: estop, station: B1 } }
`)
	assert.Equal(t, []string{
		`stop device newbox (E-stop B1) has never connected`,
		`stop device stopbox (E-stop R1, A-stop B2, field stop) has never connected`,
	}, h.m.StartConditions())

	// Pending and blocked devices are not stops anyone can rely on.
	_, err := h.virtual.Connect(&Manifest{Proto: 1, ID: "newbox", Channels: []*Channel{safety("stop")}})
	require.Nil(t, err)
	assert.Contains(t, h.m.StartConditions(), `stop device newbox (E-stop B1) is waiting for approval`)
	require.Nil(t, h.m.Approve("newbox"))
	require.Nil(t, h.m.Block("newbox"))
	conditions := h.m.StartConditions()
	assert.Contains(t, conditions, `stop device newbox (E-stop B1) is blocked`)
	assert.Contains(
		t, conditions,
		`stop device newbox (E-stop B1) has a fault (it was blocked); clear it on the Devices page once it is safe`,
	)
	assert.True(t, h.m.Stops().Stations["B1"].EStop, "blocking a stop device stops its station")

	// A required device must be online, stop or not.
	arcade := h.connect("arcade", digital("button"))
	require.Nil(t, h.m.SetRequired("arcade", true))
	assert.NotContains(t, h.m.StartConditions(), `required device arcade "Test arcade" is offline`)
	arcade.Disconnect()
	assert.Contains(t, h.m.StartConditions(), `required device arcade "Test arcade" is offline`)
	info, _ := h.m.Device("arcade")
	assert.Empty(t, info.Faults, "a device bound to no stop does not fault")

	// Removing a stop binding drops its faults.
	h.bind("inputs: []\n")
	assert.Empty(t, h.m.Faults())
	assert.Equal(t, []string{`required device arcade "Test arcade" is offline`}, h.m.StartConditions())
}

func TestStopSeededFromLastReading(t *testing.T) {
	h := newHarness(t)
	box := stopBox(h)
	require.Nil(t, box.SetInput("auto", 1))

	// A binding saved while its input is already active holds its stop at once, without waiting for a change.
	h.bind(stopBindings)
	assert.True(t, h.m.Stops().Stations["B2"].AStop)

	// And a binding that is saved again unchanged keeps its state.
	h.bind(stopBindings + "  - { id: extra, device: stopbox, channel: auto, action: { field: volunteers } }\n")
	assert.True(t, h.m.Stops().Stations["B2"].AStop)
	assert.Empty(t, h.m.TakeFieldActions(), "seeding a level fires nothing")
}
