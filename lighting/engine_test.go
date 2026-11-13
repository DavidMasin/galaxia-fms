package lighting

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

const (
	red  = "#d32f2f"
	blue = "#1565c0"
)

func TestMoments(t *testing.T) {
	h := newHarness(t)
	var ids []string
	for _, moment := range Moments(h.game) {
		ids = append(ids, moment.ID)
	}
	assert.Equal(t, []string{
		"estop", "fault", "cue.endgame", "phase.auto", "phase.pause", "phase.teleop", "score_posted", "match_end",
		"field_reset", "volunteers", "teams_ready", "awards", "idle",
	}, ids)
	moments := Moments(h.game)
	assert.Equal(t, "Cue: Endgame", moments[2].Name)
	assert.Equal(t, "For 2 seconds after the cue fires (Teleop, 20 s left)", moments[2].When)
	assert.Equal(t, "Teleop", moments[5].Name)
}

func TestStartingScenes(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	problems, err := h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "goal", Device: "goal", Channel: "strip", Kind: KindLEDStrip, Alliance: "red",
	}, true)
	require.Nil(t, err, "%v", problems)
	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "lamp", Name: "Lamp", Device: "goal", Channel: "lamp", Kind: KindDigital,
	}, true)
	require.Nil(t, err)

	config, problems, err := h.engine.Load(h.game, "default")
	require.Nil(t, err)
	assert.Empty(t, problems)
	assert.Equal(t, 10, config.Fixtures[0].Pixels, "taken from the manifest")
	assert.Equal(t, "goal", config.Fixtures[0].Name, "named after its id")
	assert.Equal(t, Scenes{
		"estop":        {"goal": {P: "blink", C: []string{"#ff0000"}}, "lamp": {P: "blink"}},
		"fault":        {"goal": {P: "blink", C: []string{"#ff8000"}}, "lamp": {P: "blink"}},
		"phase.auto":   {"goal": {P: "solid", C: []string{"alliance"}}, "lamp": {P: "solid"}},
		"phase.pause":  {"goal": {P: "solid", C: []string{"alliance"}}, "lamp": {P: "solid"}},
		"phase.teleop": {"goal": {P: "solid", C: []string{"alliance"}}, "lamp": {P: "solid"}},
		"field_reset":  {"goal": {P: "solid", C: []string{"#00ff00"}}, "lamp": {P: "solid"}},
		"volunteers":   {"goal": {P: "solid", C: []string{"#8000ff"}}, "lamp": {P: "solid"}},
		"idle":         {"goal": {P: "solid", C: []string{"#ffffff"}}, "lamp": {P: "solid"}},
	}, config.Scenes)

	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")
	h.running("auto", 3)
	assertOutput(t, solid(red), goal, "strip")
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")

	// A fixture whose cells were cleared is not given them again.
	_, err = h.engine.SetCell(h.game, "default", "idle", "goal", nil)
	require.Nil(t, err)
	config, _, _ = h.engine.Load(h.game, "default")
	assert.Nil(t, config.Cell("idle", "goal"))

	// Another game starts the fixture over.
	other := model.TestGame("rebuilt-style")
	config, _, err = h.engine.Load(other, "default")
	require.Nil(t, err)
	assert.Equal(t, &Look{P: "solid", C: []string{"#ffffff"}}, config.Cell("idle", "goal"))
	assert.Equal(t, &Look{P: "solid", C: []string{"alliance"}}, config.Cell("phase.endgame", "goal"))
}

const priorityConfig = `
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip, pixels: 10, alliance: red }
scenes:
  idle: { goal: { p: solid, c: ["#ffffff"] } }
  teams_ready: { goal: { p: solid, c: ["#00ff00"] } }
  phase.teleop: { goal: { p: solid, c: [alliance] } }
  cue.endgame: { goal: { p: blink, c: ["#ffff00"] } }
  estop: { goal: { p: blink, c: ["#ff0000"], ms: 250 } }
  match_end: { goal: { p: pulse, c: ["#0000ff"] } }
  score_posted: { goal: { p: solid, c: [winner] } }
  field_reset: { goal: { p: solid, c: [off] } }
  awards: { goal: { p: rainbow } }
`

func TestMomentPriorityAndFallThrough(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig)
	live := func() Live { return h.engine.Live() }

	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")
	assert.Equal(t, "idle", live().Moment)
	h.info.TeamsReady = true
	h.idle()
	assertOutput(t, solid("#00ff00"), goal, "strip")
	assert.Equal(t, []string{"teams_ready", "idle"}, live().Active)
	h.info.Awards = true
	h.idle()
	assertOutput(t, solid("#00ff00"), goal, "strip")
	assert.Equal(t, []string{"teams_ready", "awards", "idle"}, live().Active)
	h.info = FieldInfo{}

	// Autonomous has no look of its own: it falls through to idle.
	h.running("auto", 1, "match.start", "phase.auto.start")
	assertOutput(t, solid("#ffffff"), goal, "strip")
	assert.Equal(t, []string{"phase.auto", "idle"}, live().Active)
	h.running("teleop", 1)
	assertOutput(t, solid(red), goal, "strip")

	// A cue holds its moment for two seconds, over the phase.
	h.running("teleop", 100, "cue.endgame")
	assertOutput(t, &device.Command{P: "blink", C: []string{"#ffff00"}}, goal, "strip")
	assert.Equal(t, "cue.endgame", live().Moment)
	h.clock.Advance(1999 * time.Millisecond)
	h.running("teleop", 102)
	assertOutput(t, &device.Command{P: "blink", C: []string{"#ffff00"}}, goal, "strip")
	h.clock.Advance(time.Millisecond)
	h.running("teleop", 102)
	assertOutput(t, solid(red), goal, "strip")

	// An E-stop is over everything, the field stop as much as a station's.
	h.info.FieldStop = true
	h.running("teleop", 103)
	assertOutput(t, &device.Command{P: "blink", C: []string{"#ff0000"}, Ms: 250}, goal, "strip")
	h.info.FieldStop = false
	snapshot := h.snapshot("running", "teleop", 104)
	snapshot.Stations["B2"] = device.StationState{EStop: true}
	h.tick(snapshot)
	assertOutput(t, &device.Command{P: "blink", C: []string{"#ff0000"}, Ms: 250}, goal, "strip")
	assert.Equal(t, []string{"estop", "phase.teleop", "idle"}, live().Active)

	// After the match until the score is posted, then the winner for ten seconds.
	h.tick(h.snapshot("post", "", 0, "match.end"))
	assertOutput(t, &device.Command{P: "pulse", C: []string{"#0000ff"}}, goal, "strip")
	h.engine.mu.Lock()
	h.engine.winner = "blue"
	h.engine.mu.Unlock()
	h.tick(h.snapshot("post", "", 0, "score.posted"))
	assertOutput(t, solid(blue), goal, "strip")
	assert.Equal(t, []string{"score_posted", "idle"}, live().Active, "the score is posted, so the match is over")
	h.clock.Advance(5 * time.Second)
	h.idle()
	assertOutput(t, solid(blue), goal, "strip")
	h.clock.Advance(5 * time.Second)
	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")

	// Field reset, set to off, still has a look: off.
	h.tick(func() *device.Snapshot { s := h.snapshot("idle", "", 0); s.FieldReset = true; return s }())
	assertOutput(t, solid("#000000"), goal, "strip")
}

func TestColours(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(`
fixtures:
  - id: goal
    device: goal
    channel: strip
    kind: led_strip
    segments:
      - { id: left, start: 0, count: 3, alliance: red }
      - { id: right, start: 6, count: 2, alliance: blue }
scenes:
  idle: { goal: { p: solid, c: [alliance] } }
  phase.auto: { goal: { p: blink, c: [winner, alliance] } }
  phase.teleop: { "goal/left": { p: solid, c: ["#AbCdEf"] }, "goal/right": { p: solid, c: [alliance] } }
`)
	// The colour "alliance" is each segment's alliance; LEDs outside a segment have none, and show white.
	h.idle()
	assertOutput(t, &device.Command{Seg: []device.Segment{
		{S: 0, N: 3, P: "solid", C: []string{red}}, {S: 3, N: 3, P: "solid", C: []string{"#ffffff"}},
		{S: 6, N: 2, P: "solid", C: []string{blue}}, {S: 8, N: 2, P: "solid", C: []string{"#ffffff"}},
	}}, goal, "strip")

	// The winner is white until a match is posted, and after a tie.
	h.running("auto", 1)
	assert.Equal(t, []string{"#ffffff", red}, goal.Output("strip").Seg[0].C)
	h.engine.events = h.bus.Subscribe([]string{"match.committed"}, 10)
	h.bus.Publish("match.committed", map[string]any{"id": 1, "winner": "blue", "scores": map[string]float64{}})
	h.running("auto", 2)
	assert.Equal(t, []string{blue, red}, goal.Output("strip").Seg[0].C)
	h.bus.Publish("match.committed", map[string]any{"id": 2, "winner": "", "scores": map[string]float64{}})
	h.running("auto", 3)
	assert.Equal(t, []string{"#ffffff", red}, goal.Output("strip").Seg[0].C)
	assert.Equal(t, "", h.engine.Live().Winner)

	// Segments with looks of their own; the rest of the strip falls through to idle.
	h.running("teleop", 1)
	assertOutput(t, &device.Command{Seg: []device.Segment{
		{S: 0, N: 3, P: "solid", C: []string{"#abcdef"}}, {S: 3, N: 3, P: "solid", C: []string{"#ffffff"}},
		{S: 6, N: 2, P: "solid", C: []string{blue}}, {S: 8, N: 2, P: "solid", C: []string{"#ffffff"}},
	}}, goal, "strip")
	_, err := h.engine.SetCell(h.game, "default", "phase.teleop", "goal", &Look{P: "off"})
	require.Nil(t, err)
	h.running("teleop", 2)
	assertOutput(t, &device.Command{Seg: []device.Segment{
		{S: 0, N: 3, P: "solid", C: []string{"#abcdef"}}, {S: 3, N: 3, P: "off"},
		{S: 6, N: 2, P: "solid", C: []string{blue}}, {S: 8, N: 2, P: "off"},
	}}, goal, "strip")

	// The whole strip in one colour is one command.
	_, err = h.engine.SetCell(h.game, "default", "idle", "goal", &Look{P: "chase", C: []string{"#123456"}})
	require.Nil(t, err)
	h.idle()
	assertOutput(t, &device.Command{P: "chase", C: []string{"#123456"}}, goal, "strip")
}

func TestSegmentsReverseAndBrightness(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(`
fixtures:
  - id: goal
    device: goal
    channel: strip
    kind: led_strip
    brightness: 0.5
    reverse: true
    alliance: blue
    segments:
      - { id: left, start: 0, count: 3, alliance: red }
      - { id: right, start: 6, count: 2 }
scenes:
  idle: { goal: { p: solid, c: [alliance], br: 0.8 } }
  phase.auto: { "goal/left": { p: solid, c: ["#ff0000"], br: 0.5 }, goal: { p: solid, c: ["#00ff00"] } }
  phase.teleop:
    goal: { p: progress, c: ["#00ff00", "#202020"], v: "0.5" }
    "goal/right": { p: chase, c: [alliance] }
  phase.pause: { goal: { p: progress, c: ["#00ff00"], v: "0.3" } }
`)
	// Reversed: the device counts from the other end, so each piece is mirrored. The brightness is the look's
	// times the fixture's limit.
	h.idle()
	assertOutput(t, &device.Command{Br: number(0.4), Seg: []device.Segment{
		{S: 7, N: 3, P: "solid", C: []string{red}}, {S: 0, N: 7, P: "solid", C: []string{blue}},
	}}, goal, "strip")

	// A dimmer piece is dimmed through its colours under the brightest piece's brightness.
	h.running("auto", 1)
	assertOutput(t, &device.Command{Br: number(0.5), Seg: []device.Segment{
		{S: 7, N: 3, P: "solid", C: []string{"#800000"}}, {S: 0, N: 7, P: "solid", C: []string{"#00ff00"}},
	}}, goal, "strip")

	// A progress bar of the whole fixture split around a segment fills the strip as one bar, from the fixture's
	// LED 0: five of ten LEDs, all of them in the first piece (LEDs 0-5) and none in the last (8-9). Reversed, each
	// piece fills from its far end.
	h.running("teleop", 1)
	assertOutput(t, &device.Command{Br: number(0.5), Seg: []device.Segment{
		{S: 4, N: 6, P: "progress", C: []string{"#202020", "#00ff00"}, V: number(0.1667)},
		{S: 2, N: 2, P: "chase", C: []string{blue}},
		{S: 0, N: 2, P: "progress", C: []string{"#202020", "#00ff00"}, V: number(1)},
	}}, goal, "strip")

	// One bar over the whole strip, reversed: three LEDs lit at the far end of the device.
	h.running("pause", 1)
	assertOutput(t, &device.Command{
		P: "progress", C: []string{"#000000", "#00ff00"}, V: number(0.7), Br: number(0.5),
	}, goal, "strip")
}

func TestExpressions(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(`
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip, alliance: red }
  - { id: dimmer, device: goal, channel: dimmer, kind: pwm, brightness: 0.5 }
scenes:
  idle: { goal: { p: solid, c: ["#ffffff"] } }
  phase.teleop:
    goal: { p: progress, c: [alliance, "#202020"], v: "phase_remaining / phase_duration", when: "own.score >= 2" }
    dimmer: { p: progress, v: "match_time / 137" }
`)
	// The condition is false: the cell falls through to idle.
	h.running("teleop", 31)
	assertOutput(t, solid("#ffffff"), goal, "strip")
	for i := 0; i < 2; i++ {
		require.Nil(t, h.score.Apply(h.game, &scoring.Change{
			Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Phase: "teleop",
		}))
	}
	h.running("teleop", 31)
	assertOutput(t, &device.Command{P: "progress", C: []string{red, "#202020"}, V: number(0.7)}, goal, "strip")
	assertOutput(t, &device.Command{V: number(0.175)}, goal, "dimmer", "48 s of 137, at half brightness")

	// The fill follows the clock a LED at a time: from 89 s left of 120 to 80, it is 7 of 10 LEDs.
	count := outputCount(goal, "strip")
	h.running("teleop", 35)
	h.running("teleop", 40)
	assert.Equal(t, count, outputCount(goal, "strip"))
	h.running("teleop", 43)
	assertOutput(t, &device.Command{P: "progress", C: []string{red, "#202020"}, V: number(0.6)}, goal, "strip")

	// An expression that cannot work is refused when the cell is set.
	_, err := h.engine.SetCell(h.game, "default", "idle", "goal", &Look{P: "progress", V: "1 / phase"})
	assert.NotNil(t, err)

	// The swatches of the page show the live values.
	values := h.engine.CellValues()
	assert.InDelta(t, 77.0/120, *values["phase.teleop"]["goal"].Fill, 1e-9)
	assert.True(t, values["phase.teleop"]["goal"].Applies)
	assert.InDelta(t, 60.0/137, *values["phase.teleop"]["dimmer"].Fill, 1e-9)
}

func TestExpressionFailureAtRuntime(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(`
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip }
scenes:
  idle: { goal: { p: solid, c: ["#ffffff"] } }
  phase.teleop: { goal: { p: progress, c: ["#00ff00"], v: "(phase_elapsed - 5) / (phase_elapsed - 5)" } }
`)
	h.running("teleop", 5)
	assertOutput(t, solid("#ffffff"), goal, "strip")
	errors := h.engine.Live().Errors
	if assert.Equal(t, 1, len(errors)) {
		assert.Equal(t, "phase.teleop", errors[0].Moment)
		assert.Equal(t, "goal", errors[0].Target)
		assert.Contains(t, errors[0].Message, "not a number")
	}
	h.running("teleop", 6)
	assertOutput(t, &device.Command{P: "progress", C: []string{"#00ff00"}, V: number(1)}, goal, "strip")
	assert.Empty(t, h.engine.Live().Errors)
}

func TestDigitalAndPwmFixtures(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(`
fixtures:
  - { id: lamp, device: goal, channel: lamp, kind: digital }
  - { id: horn, device: goal, channel: horn, kind: relay }
  - { id: dimmer, device: goal, channel: dimmer, kind: pwm, brightness: 0.8 }
scenes:
  idle: { lamp: { p: off }, dimmer: { p: solid, br: 0.5 } }
  estop: { lamp: { p: blink }, horn: { p: solid }, dimmer: { p: blink, ms: 400 } }
`)
	h.idle()
	assertOutput(t, &device.Command{V: number(0)}, goal, "lamp")
	assertOutput(t, &device.Command{V: number(0)}, goal, "horn")
	assertOutput(t, &device.Command{V: number(0.4)}, goal, "dimmer")

	// Blinking is done by the FMS, starting on, from the moment the look took effect.
	h.info.FieldStop = true
	h.clock.Advance(123 * time.Millisecond)
	h.idle()
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")
	assertOutput(t, &device.Command{V: number(1)}, goal, "horn")
	assertOutput(t, &device.Command{V: number(0.8)}, goal, "dimmer")
	h.clock.Advance(200 * time.Millisecond)
	h.idle()
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")
	assertOutput(t, &device.Command{V: number(0)}, goal, "dimmer")
	h.clock.Advance(300 * time.Millisecond)
	h.idle()
	assertOutput(t, &device.Command{V: number(0)}, goal, "lamp")
	assertOutput(t, &device.Command{V: number(0.8)}, goal, "dimmer")
	h.clock.Advance(500 * time.Millisecond)
	h.idle()
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")
	assertOutput(t, &device.Command{V: number(0)}, goal, "dimmer")

	preview, err := h.engine.Preview("estop")
	require.Nil(t, err)
	assert.Equal(t, &device.Command{P: "blink", C: []string{"#ffffff"}, Ms: 1000}, preview["lamp"])
	assert.Equal(t, &device.Command{V: number(1)}, preview["horn"])
	assert.Equal(t, &device.Command{P: "blink", C: []string{"#cccccc"}, Ms: 400}, preview["dimmer"])
}

func TestChangesOnlySentOnChange(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig)
	for i := 0; i < 5; i++ {
		h.clock.Advance(50 * time.Millisecond)
		h.idle()
	}
	assert.Equal(t, 1, outputCount(goal, "strip"))
	h.running("teleop", 1)
	h.running("teleop", 2)
	assert.Equal(t, 2, outputCount(goal, "strip"))

	// A device that comes back gets what is in force when it connects, and nothing more is sent while the look
	// stays the same.
	goal.Disconnect()
	goal = h.connectGoal()
	assertOutput(t, solid(red), goal, "strip")
	h.running("teleop", 3)
	assert.Equal(t, 1, outputCount(goal, "strip"))
}

func TestClaimsAndBindings(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	set, err := device.ParseBindings([]byte(`
outputs:
  - { id: lamp-binding, device: goal, channel: lamp, when: "match_state == 'running'" }
`))
	require.Nil(t, err)
	_, err = h.devices.SaveBindings(set)
	require.Nil(t, err)

	// A fixture on a bound channel is refused.
	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "lamp", Device: "goal", Channel: "lamp", Kind: KindDigital,
	}, true)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), `driven by output binding "lamp-binding"`)
	}

	// A binding to a fixture's channel is refused in turn.
	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "goal", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
	}, true)
	require.Nil(t, err)
	assert.Equal(t, map[device.ChannelRef]string{{Device: "goal", Channel: "strip"}: Owner}, h.devices.Claims())
	set.Outputs = append(set.Outputs, &device.OutputBinding{
		ID: "strip-binding", Device: "goal", Channel: "strip", Look: &device.Look{P: "solid", C: []string{"#ffffff"}},
	})
	problems, err := h.devices.SaveBindings(set)
	assert.NotNil(t, err)
	assert.Contains(t, problems.Err().Error(), "driven by the Lighting page")

	// A fixture made in another profile, whose channel the active profile's bindings drive, is not driven when that
	// profile becomes the active one, and says why. Once the binding goes, the fixture is claimed by itself.
	tent := &model.FieldProfile{Name: "tent"}
	require.Nil(t, h.db.CreateFieldProfile(tent))
	_, err = h.engine.SaveFixture(h.game, "tent", &Fixture{
		ID: "lamp", Device: "goal", Channel: "lamp", Kind: KindDigital,
	}, true)
	require.Nil(t, err, "the bindings of the active profile do not apply to another")
	h.devices.SetProfile("tent")
	set.Outputs = set.Outputs[:1]
	_, err = h.devices.SaveBindings(set)
	require.Nil(t, err)
	require.Nil(t, h.engine.Reload(h.game))
	status := h.engine.Status()
	assert.Equal(t, "tent", status.Profile)
	assert.Contains(t, status.Conflicts["lamp"], `driven by output binding "lamp-binding"`)
	assert.False(t, status.Claimed["lamp"])
	assert.Empty(t, h.devices.Claims(), "the default profile's strip was let go")
	assertOutput(t, nil, goal, "strip")

	_, err = h.devices.SaveBindings(&device.BindingSet{})
	require.Nil(t, err)
	h.running("teleop", 1)
	h.waitForReloads()
	assert.True(t, h.engine.Status().Claimed["lamp"])
	h.clock.Advance(time.Second)
	h.running("teleop", 2)
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")
}

func TestTestAndIdentify(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig + `
  phase.auto: { goal: { p: solid, c: ["#0000ff"], when: "false" } }
`)
	h.idle()
	assert.NotNil(t, h.engine.Test("no-such-moment"))

	// A test shows its moment as designed, conditions taken as true, for ten seconds.
	require.Nil(t, h.engine.Test("phase.auto"))
	h.idle()
	assertOutput(t, solid("#0000ff"), goal, "strip")
	live := h.engine.Live()
	assert.Equal(t, "phase.auto", live.Moment)
	assert.Equal(t, &TestStatus{Moment: "phase.auto", Until: h.clock.Now().Add(TestDuration).UnixMilli()}, live.Test)
	h.clock.Advance(TestDuration - time.Millisecond)
	h.running("teleop", 1)
	assertOutput(t, solid("#0000ff"), goal, "strip")
	h.clock.Advance(time.Millisecond)
	h.running("teleop", 2)
	assertOutput(t, solid(red), goal, "strip")
	assert.Nil(t, h.engine.Live().Test)

	// A test can be cancelled. Its empty cells fall through to idle.
	require.Nil(t, h.engine.Test("volunteers"))
	h.running("teleop", 3)
	assertOutput(t, solid("#ffffff"), goal, "strip")
	h.engine.CancelTest()
	h.running("teleop", 4)
	assertOutput(t, solid(red), goal, "strip")

	// Identify sends the device the command and blinks the fixture white for three seconds.
	assert.NotNil(t, h.engine.Identify("nothing"))
	require.Nil(t, h.engine.Identify("goal"))
	assert.Equal(t, []device.DeviceCommand{{Do: "identify"}}, goal.Commands())
	h.running("teleop", 5)
	assertOutput(t, &device.Command{P: "blink", C: []string{"#ffffff"}, Ms: 400}, goal, "strip")
	assert.Contains(t, h.engine.Live().Identify, "goal")
	h.clock.Advance(IdentifyDuration)
	h.running("teleop", 8)
	assertOutput(t, solid(red), goal, "strip")
	assert.Empty(t, h.engine.Live().Identify)

	// Preview shows a moment as Test would, without touching the hardware.
	preview, err := h.engine.Preview("cue.endgame")
	require.Nil(t, err)
	assert.Equal(t, &device.Command{P: "blink", C: []string{"#ffff00"}}, preview["goal"])
	assertOutput(t, solid(red), goal, "strip")
}

func TestValidation(t *testing.T) {
	h := newHarness(t)
	h.connectGoal()
	save := func(fixture *Fixture) string {
		_, err := h.engine.SaveFixture(h.game, "default", fixture, true)
		if err == nil {
			return ""
		}
		return err.Error()
	}
	assert.Contains(t, save(&Fixture{ID: "Bad Id", Device: "goal", Channel: "strip", Kind: KindLEDStrip}),
		"must be 1 to 40 lowercase")
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "strip", Kind: KindDigital}),
		`channel "strip" of device "goal" is a led_strip, not a digital`)
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "nothing", Kind: KindDigital}),
		`device "goal" has no channel "nothing"`)
	assert.Contains(t, save(&Fixture{ID: "a", Device: "plc", Channel: "coil", Kind: KindDigital}),
		"the PLC's coils are driven by the arena")
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Alliance: "green"}), `"green" is not an alliance of game "ball-count" (red, blue)`)
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Brightness: number(1.5)}), "brightness is a limit from 0 to 1")
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Segments: []Segment{{ID: "x", Start: 0, Count: 6}, {ID: "y", Start: 5, Count: 2}}}),
		`segments "x" and "y" overlap`)
	assert.Contains(t, save(&Fixture{ID: "a", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Segments: []Segment{{ID: "x", Start: 8, Count: 3}}}), "runs past the end of the strip, which has 10 LEDs")
	assert.Equal(t, "", save(&Fixture{ID: "goal", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Segments: []Segment{{ID: "x", Start: 0, Count: 5}}}))
	assert.Contains(t, save(&Fixture{ID: "goal", Device: "goal", Channel: "lamp", Kind: KindDigital}),
		`there is already a fixture "goal"`)
	assert.Contains(t, save(&Fixture{ID: "b", Device: "goal", Channel: "strip", Kind: KindLEDStrip}),
		`channel goal/strip is already shown by fixture "goal"`)
	assert.Equal(t, "", save(&Fixture{ID: "lamp", Device: "goal", Channel: "lamp", Kind: KindDigital}))
	assert.Contains(t, save(&Fixture{ID: "later", Device: "not-yet", Channel: "strip", Kind: KindLEDStrip,
		Pixels: 0}), "needs its number of LEDs")
	assert.Equal(t, "", save(&Fixture{ID: "later", Device: "not-yet", Channel: "strip", Kind: KindLEDStrip,
		Pixels: 20}), "a device that has not connected yet is a warning")

	set := func(moment, target string, look *Look) string {
		problems, err := h.engine.SetCell(h.game, "default", moment, target, look)
		if err == nil {
			assert.Empty(t, problems.Errors())
			return ""
		}
		assert.NotEmpty(t, problems)
		return err.Error()
	}
	assert.Contains(t, set("idle", "lamp", &Look{P: "rainbow"}), "a digital fixture can show off, solid, blink")
	assert.Contains(t, set("idle", "goal", &Look{P: "sparkle"}), `"sparkle" is not a pattern`)
	assert.Contains(t, set("idle", "goal", &Look{C: []string{"red"}}), `"red" is not a colour`)
	assert.Contains(t, set("idle", "goal", &Look{C: []string{"#ff0000", "#00ff00", "#0000ff"}}), "at most two")
	assert.Contains(t, set("idle", "goal", &Look{P: "progress"}), "progress needs v")
	assert.Contains(t, set("idle", "goal", &Look{V: "0.5"}), "v is the fill of the progress pattern")
	assert.Contains(t, set("idle", "goal", &Look{P: "progress", V: "2"}), "v is a fill from 0 to 1, not 2")
	assert.Contains(t, set("idle", "goal", &Look{When: "own.score > 1"}), `when "own.score > 1"`)
	assert.Contains(t, set("idle", "goal", &Look{When: "own.score > 1"}), "so give it one first")
	assert.NotContains(t, set("idle", "goal", &Look{When: "rde.score > 1"}), "so give it one first")
	assert.Contains(t, set("idle", "goal", &Look{Ms: -1}), "the period is in milliseconds")
	assert.Contains(t, set("idle", "goal", &Look{Br: number(2)}), "brightness is from 0 to 1")
	assert.Contains(t, set("idle", "goal/nothing", &Look{}), `there is no fixture or segment "goal/nothing"`)
	assert.Contains(t, set("phase.nothing", "goal", &Look{}), `"phase.nothing" is not a moment`)
	assert.Equal(t, "", set("idle", "goal/x", &Look{P: "pulse", C: []string{"winner", "off"}, Ms: 900}))
	assert.Equal(t, "", set("idle", "goal/x", nil))
	assert.Equal(t, "", set("phase.teleop", "goal", &Look{When: "red.score > blue.score"}))

	// A warning does not keep a look from being saved.
	problems, err := h.engine.SetCell(h.game, "default", "idle", "goal", &Look{C: []string{"alliance"}})
	assert.Nil(t, err)
	if assert.Equal(t, 1, len(problems)) {
		assert.True(t, problems[0].Warning)
		assert.Contains(t, problems[0].Message, "it shows white")
	}
}

func TestEditingFixtures(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	_, err := h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "goal", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Segments: []Segment{{ID: "a", Start: 0, Count: 5}, {ID: "b", Start: 5, Count: 5}},
	}, true)
	require.Nil(t, err)
	_, err = h.engine.SetCell(h.game, "default", "idle", "goal/a", &Look{C: []string{"#ff0000"}})
	require.Nil(t, err)
	_, err = h.engine.SetCell(h.game, "default", "idle", "goal/b", &Look{C: []string{"#0000ff"}})
	require.Nil(t, err)
	other := model.TestGame("rebuilt-style")
	_, err = h.engine.SetCell(other, "default", "idle", "goal/b", &Look{C: []string{"#0000ff"}})
	require.Nil(t, err)

	// Removing a segment removes its cells in every game.
	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "goal", Name: "Goal", Device: "goal", Channel: "strip", Kind: KindLEDStrip,
		Segments: []Segment{{ID: "a", Start: 0, Count: 5}},
	}, false)
	require.Nil(t, err)
	config, _, _ := h.engine.Load(h.game, "default")
	assert.Equal(t, "Goal", config.Fixtures[0].Name)
	assert.NotNil(t, config.Cell("idle", "goal/a"))
	assert.Nil(t, config.Cell("idle", "goal/b"))
	config, _, _ = h.engine.Load(other, "default")
	assert.Nil(t, config.Cell("idle", "goal/b"))

	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{ID: "nothing", Kind: KindLEDStrip}, false)
	assert.NotNil(t, err)

	// Deleting a fixture lets its channel go.
	h.idle()
	assert.NotNil(t, goal.Output("strip"))
	require.Nil(t, h.engine.DeleteFixture(h.game, "default", "goal"))
	config, _, _ = h.engine.Load(h.game, "default")
	assert.Empty(t, config.Fixtures)
	assert.Empty(t, config.Scenes)
	assert.Nil(t, goal.Output("strip"))
	assert.Empty(t, h.devices.Claims())
	assert.NotNil(t, h.engine.DeleteFixture(h.game, "default", "goal"))
}

func TestExportAndImport(t *testing.T) {
	h := newHarness(t)
	h.connectGoal()
	h.load(priorityConfig)
	data, err := h.engine.Export(h.game, "default")
	require.Nil(t, err)
	exported, err := ParseConfig(data)
	require.Nil(t, err)
	assert.Equal(t, "default", exported.Profile)
	assert.Equal(t, "ball-count", exported.Game)
	imported, err := ParseConfig([]byte(priorityConfig))
	require.Nil(t, err)
	imported.Fixtures[0].Name = "goal"
	assert.Equal(t, imported.Fixtures, exported.Fixtures)
	assert.Equal(t, imported.Scenes, exported.Scenes)

	// A file with errors changes nothing.
	problems, err := h.engine.Import(h.game, "default", []byte(`
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip }
scenes:
  idle: { other: { p: solid } }
`))
	assert.NotNil(t, err)
	assert.Equal(t, `there is no fixture or segment "other"; the cell is ignored`, problems[0].Message)
	_, err = h.engine.Import(h.game, "default", []byte("fixtures: [{ id: goal, colour: red }]"))
	assert.Contains(t, err.Error(), "field colour not found")
	config, _, _ := h.engine.Load(h.game, "default")
	assert.Equal(t, exported.Scenes, config.Scenes)

	// A file made for another game keeps the cells of the moments this game has.
	problems, err = h.engine.Import(h.game, "default", []byte(`
game: rebuilt-style
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip }
scenes:
  idle: { goal: { p: solid } }
  phase.shift1: { goal: { p: blink } }
`))
	require.Nil(t, err)
	assert.Equal(t, 2, len(problems))
	config, _, _ = h.engine.Load(h.game, "default")
	assert.Equal(t, Scenes{"idle": {"goal": {P: "solid"}}}, config.Scenes, "no starting scenes for an import")
}

func TestStatusAndProblems(t *testing.T) {
	h := newHarness(t)
	h.connectGoal()
	h.load(`
fixtures:
  - { id: goal, device: goal, channel: strip, kind: led_strip }
  - { id: away, device: away, channel: strip, kind: led_strip, pixels: 8 }
scenes:
  idle: { goal: { p: solid, c: [alliance] } }
`)
	status := h.engine.Status()
	assert.True(t, status.Loaded)
	assert.Equal(t, "default", status.Profile)
	assert.Equal(t, "ball-count", status.Game)
	assert.Equal(t, map[string]bool{"goal": true, "away": true}, status.Claimed)
	if assert.Equal(t, 2, len(status.Problems)) {
		assert.Contains(t, status.Problems[0].Message, `device "away" has not connected yet`)
		assert.Contains(t, status.Problems[1].Message, "it shows white")
	}

	// A fixture whose device the manager does not know yet cannot be driven; the page is told why.
	h.idle()
	errors := h.engine.Live().Errors
	if assert.Equal(t, 1, len(errors)) {
		assert.Equal(t, "away", errors[0].Fixture)
		assert.Contains(t, errors[0].Message, `there is no device "away"`)
	}
	away := h.connect("away", strip("strip", 8))
	h.idle()
	assertOutput(t, &device.Command{P: "off"}, away, "strip")
	assert.Empty(t, h.engine.Live().Errors)
}

func TestReloadInTheBackground(t *testing.T) {
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig)
	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")

	// A new game is loaded in the background; until it is, what is shown is held. Then the fixture, new to that
	// game, shows its starting scenes there.
	other := model.TestGame("rebuilt-style")
	h.devices.SetGame(other, testStations)
	h.score = scoring.NewState(other)
	h.game = other
	h.running("auto", 1)
	assertOutput(t, solid("#ffffff"), goal, "strip")
	h.waitForReloads()
	assert.Equal(t, "rebuilt-style", h.engine.Status().Game)
	h.running("auto", 2)
	assertOutput(t, solid(red), goal, "strip")

	// So does a new profile.
	h.devices.SetProfile("tent")
	h.running("auto", 3)
	h.waitForReloads()
	assert.Equal(t, "tent", h.engine.Status().Profile)
	assert.Empty(t, h.devices.Claims())
	assertOutput(t, nil, goal, "strip")
}
