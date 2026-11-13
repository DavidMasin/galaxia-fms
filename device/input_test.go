package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCounterInput(t *testing.T) {
	h := newHarness(t)
	beam := h.connect("beam", &Channel{ID: "count", Dir: DirIn, Type: TypeCounter, Name: "Balls through goal"})
	h.bind(`
inputs:
  - { id: goal, device: beam, channel: count, action: { score: balls, op: increment, alliance: red } }
  - { id: comets, device: beam, channel: count, action: { score: comets, op: decrement } }
`)

	// The first total ever heard is where counting starts.
	require.Nil(t, beam.SetInput("count", 5))
	assert.Empty(t, h.scorer.take())

	// Then differences count, so a lost message loses nothing.
	require.Nil(t, beam.SetInput("count", 7))
	changes := h.scorer.take()
	if assert.Equal(t, 2, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", scoring.OpIncrement, 2.0)
		assertChange(t, changes[1], "", 0, "comets", scoring.OpDecrement, 2.0)
		source := scoring.Source{Kind: "device", ID: "beam", Name: "Test beam · Balls through goal"}
		assert.Equal(t, source, changes[0].Source)
	}
	require.Nil(t, beam.SetInput("count", 7))
	assert.Empty(t, h.scorer.take(), "an unchanged total counts nothing")

	// A lower total means the device restarted, and the new total counts in full.
	conf := 0.9
	require.Nil(t, beam.SendInput("count", Reading{Value: 3, Conf: &conf}))
	changes = h.scorer.take()
	if assert.Equal(t, 2, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", scoring.OpIncrement, 3.0)
		assert.Equal(t, 0.9, *changes[0].Source.Confidence)
	}

	// The running total is stored, so that a restart of the FMS does not count it again.
	record, _ := h.db.GetDeviceByDeviceId("beam")
	assert.Equal(t, map[string]float64{"count": 3}, record.CounterTotals)
	info, _ := h.m.Device("beam")
	assert.Equal(t, 3.0, info.Inputs["count"].Value)
	assert.Equal(t, 0.9, *info.Inputs["count"].Conf)
	inputs := h.eventsOf("device.input")
	if assert.Equal(t, 4, len(inputs)) {
		assert.Equal(t, map[string]any{"id": "beam", "channel": "count", "value": 3.0, "conf": 0.9}, inputs[3])
	}

	// A negative total is not a total.
	assert.ErrorContains(t, beam.SetInput("count", -1), "its running total cannot be negative")
}

func TestDigitalEdges(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("a"), digital("b"), digital("c"), digital("d"))
	h.bind(`
inputs:
  - { id: rising, device: box, channel: a, action: { score: balls, op: increment, alliance: red } }
  - { id: falling, device: box, channel: b, edge: falling, action: { score: balls, op: increment, alliance: blue } }
  - { id: both, device: box, channel: c, edge: both, action: { score: lit, op: toggle, alliance: red } }
  - { id: inverted, device: box, channel: d, invert: true, action: { score: lit, op: toggle, alliance: blue } }
`)
	count := func() int { return len(h.scorer.take()) }

	// A digital input rests at 0: reading 0 first is no edge, reading 1 first is a rising one.
	require.Nil(t, box.SetInput("a", 0))
	assert.Equal(t, 0, count())
	require.Nil(t, box.SetInput("a", 1))
	assert.Equal(t, 1, count())
	require.Nil(t, box.SetInput("a", 1))
	assert.Equal(t, 0, count(), "a repeated level is no edge")
	require.Nil(t, box.SetInput("a", 0))
	assert.Equal(t, 0, count())
	require.Nil(t, box.SetInput("a", true))
	assert.Equal(t, 1, count())

	require.Nil(t, box.SetInput("b", 1))
	assert.Equal(t, 0, count())
	require.Nil(t, box.SetInput("b", 0))
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "blue", 0, "balls", scoring.OpIncrement, 1.0)
	}

	require.Nil(t, box.SetInput("c", 1))
	require.Nil(t, box.SetInput("c", 0))
	assert.Equal(t, 2, count(), "both edges")

	// Inverted, the input is active while it reads 0.
	require.Nil(t, box.SetInput("d", 1))
	assert.Equal(t, 0, count())
	require.Nil(t, box.SetInput("d", 0))
	assert.Equal(t, 1, count())

	assert.ErrorContains(t, box.SetInput("a", 2), "a is a digital channel; v must be 0 or 1, not 2")
	info, _ := h.m.Device("box")
	if assert.NotNil(t, info.Refusal) {
		assert.Equal(t, "a is a digital channel; v must be 0 or 1, not 2", info.Refusal.Reason)
	}
}

func TestDebounce(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("plate"))
	h.bind(`
inputs:
  - { id: plate, device: box, channel: plate, debounce_ms: 30, action: { score: balls, op: increment, alliance: red } }
`)
	start := h.clock.Now()

	// A new level must hold for the debounce time; the arena's tick commits it.
	require.Nil(t, box.SetInput("plate", 1))
	h.m.Tick(start.Add(20 * time.Millisecond))
	assert.Empty(t, h.scorer.take())
	h.m.Tick(start.Add(30 * time.Millisecond))
	assert.Equal(t, 1, len(h.scorer.take()))

	// A bounce back within the time is no edge.
	h.clock.Advance(100 * time.Millisecond)
	require.Nil(t, box.SetInput("plate", 0))
	h.clock.Advance(10 * time.Millisecond)
	require.Nil(t, box.SetInput("plate", 1))
	h.m.Tick(h.clock.Advance(100 * time.Millisecond))
	assert.Empty(t, h.scorer.take(), "0 did not hold, so 1 never fell")

	// A message repeating a level that has held long enough commits it too.
	require.Nil(t, box.SetInput("plate", 0))
	h.clock.Advance(40 * time.Millisecond)
	require.Nil(t, box.SetInput("plate", 0))
	require.Nil(t, box.SetInput("plate", 1))
	h.clock.Advance(35 * time.Millisecond)
	require.Nil(t, box.SetInput("plate", 1))
	assert.Equal(t, 1, len(h.scorer.take()))
}

func TestMinInterval(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("button"))
	h.bind(`
inputs:
  - { id: button, device: box, channel: button, min_interval_ms: 500,
      action: { score: balls, op: increment, alliance: red } }
`)
	press := func() int {
		require.Nil(t, box.SetInput("button", 1))
		require.Nil(t, box.SetInput("button", 0))
		return len(h.scorer.take())
	}
	assert.Equal(t, 1, press())
	h.clock.Advance(100 * time.Millisecond)
	assert.Equal(t, 0, press(), "within min_interval_ms of the last trigger")
	h.clock.Advance(450 * time.Millisecond)
	assert.Equal(t, 1, press())
}

func TestScoreOps(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("plate"), digital("toggle"), digital("hold"), digital("go"), digital("stop"),
		digital("down"))
	h.bind(`
inputs:
  - { id: climb, device: box, channel: plate, action: { score: climb, op: set, value: high, alliance: red, robot: 2 } }
  - { id: lit, device: box, channel: toggle, action: { score: lit, op: toggle, alliance: blue } }
  - { id: hold, device: box, channel: hold, action: { score: balance, op: hold, alliance: red } }
  - { id: go, device: box, channel: go, action: { score: balance, op: timer_start, alliance: blue } }
  - { id: stop, device: box, channel: stop, action: { score: balance, op: timer_stop, alliance: blue } }
  - { id: down, device: box, channel: down, action: { score: payload, op: decrement, value: 2.5, alliance: red } }
`)
	one := func(channel string, value int) scoring.Change {
		t.Helper()
		require.Nil(t, box.SetInput(channel, value))
		changes := h.scorer.take()
		require.Equal(t, 1, len(changes), channel)
		return changes[0]
	}
	assertChange(t, one("plate", 1), "red", 2, "climb", scoring.OpSet, "high")
	assertChange(t, one("toggle", 1), "blue", 0, "lit", scoring.OpToggle, nil)
	assertChange(t, one("hold", 1), "red", 0, "balance", scoring.OpTimerStart, nil)
	assertChange(t, one("hold", 0), "red", 0, "balance", scoring.OpTimerStop, nil)
	assertChange(t, one("go", 1), "blue", 0, "balance", scoring.OpTimerStart, nil)
	assertChange(t, one("stop", 1), "blue", 0, "balance", scoring.OpTimerStop, nil)
	assertChange(t, one("down", 1), "red", 0, "payload", scoring.OpDecrement, 2.5)

	// A hold the score refused is not held: the timer is started again on the next rise.
	require.Nil(t, box.SetInput("hold", 0))
	h.scorer.refuse = fmt.Errorf("not now")
	require.Nil(t, box.SetInput("hold", 1))
	require.Nil(t, box.SetInput("hold", 0))
	h.scorer.refuse = nil
	assert.Empty(t, h.scorer.take(), "no stop for a timer that never started")
	assertChange(t, one("hold", 1), "red", 0, "balance", scoring.OpTimerStart, nil)

	// A device that disconnects stops the timer it was holding.
	box.Disconnect()
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balance", scoring.OpTimerStop, nil)
	}
}

func TestAnalogInputs(t *testing.T) {
	h := newHarness(t)
	box := h.connect(
		"box", &Channel{ID: "kg", Dir: DirIn, Type: TypeAnalog}, &Channel{ID: "pos", Dir: DirIn, Type: TypeEncoder},
		&Channel{ID: "level", Dir: DirIn, Type: TypeAnalog},
	)
	h.bind(`
inputs:
  - { id: weigh, device: box, channel: kg, scale: 0.001, offset: -0.2,
      action: { score: payload, op: set, alliance: blue } }
  - { id: turn, device: box, channel: pos, action: { score: payload, op: set, alliance: red } }
  - { id: full, device: box, channel: level, threshold: 10, hysteresis: 2,
      action: { score: balls, op: increment, alliance: red } }
`)
	require.Nil(t, box.SetInput("kg", 1200))
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "blue", 0, "payload", scoring.OpSet, 1.0)
	}
	require.Nil(t, box.SetInput("kg", 1200))
	assert.Empty(t, h.scorer.take(), "an unchanged reading is not set again")
	require.Nil(t, box.SetInput("pos", -42))
	changes = h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "payload", scoring.OpSet, -42.0)
	}

	// With a threshold the reading acts as a digital input; with hysteresis it must fall that far below to reset.
	rises := 0
	for _, reading := range []float64{5, 10, 9, 8.5, 12, 7.9, 11, 10.5} {
		require.Nil(t, box.SetInput("level", reading))
		rises += len(h.scorer.take())
	}
	assert.Equal(t, 2, rises, "rises at 10 and at 11 after falling below 8 at 7.9")

	// A refused set is tried again with the next reading, even an equal one.
	h.scorer.refuse = fmt.Errorf("not now")
	require.Nil(t, box.SetInput("kg", 2200))
	h.scorer.refuse = nil
	require.Nil(t, box.SetInput("kg", 2200))
	assert.Equal(t, 1, len(h.scorer.take()))
}

func TestTextInput(t *testing.T) {
	h := newHarness(t)
	reader := h.connect("reader", &Channel{ID: "tag", Dir: DirIn, Type: TypeText})
	h.bind(`
inputs:
  - { id: tag, device: reader, channel: tag, action: { score: climb, op: set, alliance: blue, robot: 3 } }
`)
	require.Nil(t, reader.SetInput("tag", "low"))
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "blue", 3, "climb", scoring.OpSet, "low")
	}
	require.Nil(t, reader.SetInput("tag", "low"))
	assert.Empty(t, h.scorer.take(), "the same tag read again sets nothing")

	require.Nil(t, reader.SetInput("tag", "medium"))
	assert.Empty(t, h.scorer.take())
	info, _ := h.m.Device("reader")
	if assert.NotNil(t, info.Refusal) {
		want := Refusal{Binding: "tag", Reason: `"medium" is not an option of climb`, Time: h.clock.Now(), Count: 1}
		assert.Equal(t, want, *info.Refusal)
	}
	refused := map[string]any{"id": "reader", "binding": "tag", "reason": `"medium" is not an option of climb`}
	assert.Equal(t, []map[string]any{refused}, h.eventsOf("device.refused"))
}

func TestPhasesAndScoring(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("button"))
	h.bind(`
inputs:
  - { id: button, device: box, channel: button, phases: [auto], action: { score: balls, op: increment, alliance: red } }
`)
	press := func() int {
		require.Nil(t, box.SetInput("button", 1))
		require.Nil(t, box.SetInput("button", 0))
		return len(h.scorer.take())
	}
	h.running("auto")
	assert.Equal(t, 1, press())
	h.running("teleop")
	assert.Equal(t, 0, press(), "outside its phases")
	h.m.SetMatch(MatchInfo{Running: true, Phase: "teleop", GracePhase: "auto", Scoring: true})
	assert.Equal(t, 1, press(), "in the score grace of one of its phases")
	h.m.SetMatch(MatchInfo{Scoring: true, GracePhase: "auto"})
	assert.Equal(t, 1, press(), "in the score grace after the match")

	// Before a match, a score action does nothing, and says nothing.
	h.m.SetMatch(MatchInfo{})
	assert.Equal(t, 0, press())
	assert.Empty(t, h.eventsOf("device.refused"))
}

func TestRefusals(t *testing.T) {
	h := newHarness(t)
	box := h.connect("box", digital("button"))
	h.bind(`
inputs:
  - { id: button, device: box, channel: button, action: { score: balls, op: increment, alliance: red } }
`)
	h.scorer.refuse = fmt.Errorf("balls does not accept changes during the pause phase")
	for i := 0; i < 3; i++ {
		require.Nil(t, box.SetInput("button", 1))
		require.Nil(t, box.SetInput("button", 0))
	}
	info, _ := h.m.Device("box")
	if assert.NotNil(t, info.Refusal) {
		assert.Equal(t, "button", info.Refusal.Binding)
		assert.Equal(t, "balls does not accept changes during the pause phase", info.Refusal.Reason)
		assert.Equal(t, 3, info.Refusal.Count)
	}
	// Published once a second at most, so a busy sensor does not flood the bus.
	assert.Equal(t, 1, len(h.eventsOf("device.refused")))
	h.clock.Advance(time.Second)
	require.Nil(t, box.SetInput("button", 1))
	assert.Equal(t, []map[string]any{{
		"id": "box", "binding": "button", "reason": "balls does not accept changes during the pause phase",
	}}, h.eventsOf("device.refused"))
}

func TestInputsNeedAnApprovedDeviceAndManifest(t *testing.T) {
	h := newHarness(t)
	pending, err := h.virtual.Connect(&Manifest{Proto: 1, ID: "newbie", Channels: []*Channel{digital("button")}})
	require.Nil(t, err)
	assert.Equal(t, ErrNotApproved, pending.SetInput("button", 1))
	assert.Equal(t, ErrNotApproved, pending.Heartbeat())

	// An approved device that has not told the FMS its channels.
	_, err = h.m.AuthenticateLocal(VirtualTransportName, "quiet")
	require.Nil(t, err)
	require.Nil(t, h.m.Approve("quiet"))
	h.m.Online(VirtualTransportName, "quiet", "")
	err = h.m.Input(VirtualTransportName, "quiet", "x", Reading{Value: 1.0})
	assert.EqualError(t, err, "an input came before the manifest")
	assert.Equal(t, ErrUnknown, h.m.Input(VirtualTransportName, "nobody", "x", Reading{Value: 1.0}))

	box := h.connect("box", digital("button"), output("lamp", TypeDigital))
	assert.EqualError(t, box.SetInput("lamp", 1), `"lamp" is not an input channel of the manifest`)
	assert.EqualError(t, box.SetInput("tag", "x"), `"tag" is not an input channel of the manifest`)

	// A manifest that is not the device's own is refused.
	err = h.m.Manifest(VirtualTransportName, "box", &Manifest{Proto: 1, ID: "other"})
	assert.EqualError(t, err, `the manifest is for "other", but it came from "box"`)
}

func TestFieldActions(t *testing.T) {
	h := newHarness(t)
	arcade := h.connect("arcade", digital("start"), digital("reset"))
	h.bind(`
inputs:
  - { id: start, device: arcade, channel: start, action: { field: start_match } }
  - { id: reset, device: arcade, channel: reset, edge: falling, action: { field: field_reset } }
`)
	require.Nil(t, arcade.SetInput("start", 1))
	require.Nil(t, arcade.SetInput("start", 0))
	require.Nil(t, arcade.SetInput("reset", 1))
	assert.Empty(t, h.m.fieldActions[1:])
	require.Nil(t, arcade.SetInput("reset", 0))
	actions := h.m.TakeFieldActions()
	assert.Equal(t, []FieldAction{
		{Action: ActionStartMatch, Device: "arcade", Binding: "start", Time: h.clock.Now()},
		{Action: ActionFieldReset, Device: "arcade", Binding: "reset", Time: h.clock.Now()},
	}, actions)
	assert.Empty(t, h.m.TakeFieldActions(), "each action is taken once")

	// A field action that fails is a refusal on the device that asked.
	h.m.ReportFieldAction(actions[0], fmt.Errorf("cannot start match: an emergency stop is active (R1)"))
	h.m.ReportFieldAction(actions[1], nil)
	info, _ := h.m.Device("arcade")
	if assert.NotNil(t, info.Refusal) {
		assert.Equal(t, "start", info.Refusal.Binding)
		assert.Equal(t, "start_match: cannot start match: an emergency stop is active (R1)", info.Refusal.Reason)
	}
}
