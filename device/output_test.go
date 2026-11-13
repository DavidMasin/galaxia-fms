package device

import (
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func v(value float64) *Command {
	return &Command{V: number(value)}
}

func TestDigitalOutputs(t *testing.T) {
	h := newHarness(t)
	light := h.connect("light", output("green", TypeDigital), output("amber", TypeRelay))
	h.bind(`
outputs:
  - { id: green, device: light, channel: green, when: "match_state == 'running'" }
  - { id: amber, device: light, channel: amber, when: "phase == 'auto'", look: { p: blink, ms: 400 }, else: { p: off } }
`)
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Equal(t, map[string]*Command{"green": v(0), "amber": v(0)}, light.Outputs())

	// A command is only sent when it changes.
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Equal(t, 2, len(light.History()))
	assert.Equal(t, 2, len(h.eventsOf("device.output")))

	h.m.UpdateOutputs(h.snapshot("teleop", 10))
	assert.Equal(t, v(1), light.Output("green"))
	outputs := h.eventsOf("device.output")
	if assert.Equal(t, 1, len(outputs)) {
		assert.Equal(t, map[string]any{"id": "light", "channel": "green", "command": v(1)}, outputs[0])
	}

	// The FMS blinks a digital output itself, from the moment its condition turned true.
	h.m.UpdateOutputs(h.snapshot("auto", 1))
	assert.Equal(t, v(1), light.Output("amber"))
	h.clock.Advance(150 * time.Millisecond)
	h.m.UpdateOutputs(h.snapshot("auto", 1.15))
	assert.Equal(t, v(1), light.Output("amber"))
	h.clock.Advance(100 * time.Millisecond)
	h.m.UpdateOutputs(h.snapshot("auto", 1.25))
	assert.Equal(t, v(0), light.Output("amber"))
	h.clock.Advance(200 * time.Millisecond)
	h.m.UpdateOutputs(h.snapshot("auto", 1.45))
	assert.Equal(t, v(1), light.Output("amber"))

	// The bindings and the device both show what is in force.
	info, _ := h.m.Device("light")
	assert.Equal(t, map[string]*Command{"green": v(1), "amber": v(1)}, info.Outputs)
}

func TestEventPulses(t *testing.T) {
	h := newHarness(t)
	stack := h.connect("stack", output("buzzer", TypeRelay), output("horn", TypeSound))
	h.bind(`
outputs:
  - { id: buzzer, device: stack, channel: buzzer, on: phase.teleop.start, pulse_ms: 300 }
  - { id: horn, device: stack, channel: horn, on: cue.go, look: { play: horn } }
`)
	h.m.UpdateOutputs(h.snapshot("auto", 14.9))
	assert.Equal(t, v(0), stack.Output("buzzer"))
	assert.Nil(t, stack.Output("horn"), "a sound has no state")

	h.m.UpdateOutputs(h.snapshot("teleop", 0, "phase.auto.end", "phase.teleop.start"))
	assert.Equal(t, v(1), stack.Output("buzzer"))
	h.m.UpdateOutputs(h.snapshot("teleop", 0.2))
	assert.Equal(t, v(1), stack.Output("buzzer"))
	h.clock.Advance(300 * time.Millisecond)
	h.m.UpdateOutputs(h.snapshot("teleop", 0.3))
	assert.Equal(t, v(0), stack.Output("buzzer"))

	// A sound is sent each time its event fires, and never kept to be replayed.
	h.m.UpdateOutputs(h.snapshot("auto", 0, "match.start", "phase.auto.start", "cue.go"))
	h.m.UpdateOutputs(h.snapshot("auto", 0.05))
	h.m.UpdateOutputs(h.snapshot("auto", 0, "cue.go"))
	var plays int
	for _, sent := range stack.History() {
		if sent.Channel == "horn" {
			plays++
			assert.Equal(t, &Command{Play: "horn", Transient: true}, sent.Command)
		}
	}
	assert.Equal(t, 2, plays)
	assert.Nil(t, stack.Output("horn"))
	_, kept := h.m.OutputsOf("stack")["horn"]
	assert.False(t, kept)
}

func TestStripAndDisplayOutputs(t *testing.T) {
	h := newHarness(t)
	goal := h.connect("goal", output("strip", TypeLEDStrip), output("fill", TypeLEDStrip),
		output("score", TypeDisplay), output("team", TypeDisplay), output("fan", TypePWM))
	h.bind(`
outputs:
  - { id: strip, device: goal, channel: strip, alliance: red, when: "own.hub_active",
      look: { p: blink, c: [alliance, "#FFFFFF"], ms: 400, br: 0.6 }, else: { p: solid, c: ["#101010"] } }
  - { id: fill, device: goal, channel: fill, look: { p: progress, c: ["#00c853", "#202020"],
      v: "phase_remaining / phase_duration", seg: [{ s: 0, n: 5, c: [ "#ff0000" ] }] } }
  - { id: score, device: goal, channel: score, alliance: blue, value: "string(own.score) + ' pts'" }
  - { id: team, device: goal, channel: team, value: "stations.B2.team" }
  - { id: fan, device: goal, channel: fan, value: "match_time / 10" }
`)
	h.m.UpdateOutputs(h.snapshot("auto", 6))
	assert.Equal(t, &Command{P: PatternSolid, C: []string{"#101010"}}, goal.Output("strip"))
	assert.Equal(t, &Command{
		P: PatternProgress, C: []string{"#00c853", "#202020"}, V: number(0.6),
		Seg: []Segment{{S: 0, N: 5, P: PatternSolid, C: []string{"#ff0000"}}},
	}, goal.Output("fill"))
	text := "0 pts"
	assert.Equal(t, &Command{Text: &text}, goal.Output("score"))
	assert.Equal(t, v(104), goal.Output("team"))
	assert.Equal(t, v(0.6), goal.Output("fan"))

	// The colour "alliance" is that alliance's colour in the game; a pwm level is clamped to 1.
	h.m.UpdateOutputs(h.snapshot("teleop", 30))
	assert.Equal(t, &Command{P: PatternBlink, C: []string{"#d32f2f", "#ffffff"}, Ms: 400, Br: number(0.6)},
		goal.Output("strip"))
	assert.Equal(t, v(1), goal.Output("fan"))
	assert.Equal(t, number(0.75), goal.Output("fill").V)

	// The score as it stands.
	state := scoring.NewState(h.game)
	change := scoring.Change{Alliance: "blue", Element: "balls", Op: scoring.OpIncrement, Value: 4, Phase: "teleop"}
	require.Nil(t, state.Apply(h.game, &change))
	h.m.UpdateOutputs(h.snapshotOf(state, "teleop", 31))
	text = "4 pts"
	assert.Equal(t, &Command{Text: &text}, goal.Output("score"))
}

func TestOutputsFollowTheBindings(t *testing.T) {
	h := newHarness(t)
	h.bind(`
outputs:
  - { id: green, device: late, channel: green, when: "true" }
  - { id: fault, device: late, channel: fault, when: "fault" }
`)
	// A device the FMS has not heard from gets nothing until it says what it has.
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Empty(t, h.m.OutputsOf("late"))
	late := h.connect("late", output("green", TypeDigital), output("fault", TypeDigital), safety("stop"))
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Equal(t, map[string]*Command{"green": v(1), "fault": v(0)}, late.Outputs())

	// The fault flag lights while any device fault is latched.
	h.bind(`
inputs:
  - { id: stop, device: late, channel: stop, action: { field: estop, station: R1 } }
outputs:
  - { id: green, device: late, channel: green, when: "true" }
  - { id: fault, device: late, channel: fault, when: "fault" }
`)
	require.Nil(t, late.SetInput("stop", 0))
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Equal(t, v(1), late.Output("fault"))

	// Removing a binding clears its output, so that the retained command goes too.
	h.bind(`
outputs:
  - { id: fault, device: late, channel: fault, when: "fault" }
`)
	assert.Nil(t, late.Output("green"))
	history := late.History()
	assert.Equal(t, OutputCommand{Device: "late", Channel: "green"}, history[len(history)-1])
	assert.NotContains(t, h.m.OutputsOf("late"), "green")
}

func TestOutputClaims(t *testing.T) {
	h := newHarness(t)
	lamp := h.connect("lamp", output("bound", TypeDigital), output("strip", TypeLEDStrip), output("spare", TypeRelay))
	h.bind(`
outputs:
  - { id: bound, device: lamp, channel: bound, when: "true" }
`)
	strip := ChannelRef{Device: "lamp", Channel: "strip"}
	spare := ChannelRef{Device: "lamp", Channel: "spare"}

	assert.EqualError(t, h.m.ClaimOutputs("lighting", []ChannelRef{{Device: "lamp", Channel: "bound"}}),
		`output lamp/bound is driven by binding "bound"`)
	require.Nil(t, h.m.ClaimOutputs("lighting", []ChannelRef{strip, spare}))
	err := h.m.ClaimOutputs("other", []ChannelRef{strip})
	assert.EqualError(t, err, "output lamp/strip is already driven by lighting")
	assert.Equal(t, map[ChannelRef]string{strip: "lighting", spare: "lighting"}, h.m.Claims())
	assert.Error(t, h.m.ClaimOutputs(BindingsOwner, nil))

	// A claimed channel cannot be bound.
	problems := h.m.ValidateBindings(&BindingSet{Outputs: []*OutputBinding{{ID: "x", Device: "lamp", Channel: "strip",
		Look: &Look{P: PatternSolid}}}})
	assert.Equal(
		t, "channel lamp/strip is driven by lighting; an output is driven by one binding or by lighting, not both",
		problems.Errors()[0].Message,
	)

	// The owner drives it, and only changes are sent.
	command := &Command{P: PatternRainbow, Ms: 2000}
	require.Nil(t, h.m.DriveOutput("lighting", strip, command))
	require.Nil(t, h.m.DriveOutput("lighting", strip, command.Clone()))
	assert.Equal(t, command, lamp.Output("strip"))
	assert.Equal(t, 1, len(lamp.History()))
	assert.EqualError(t, h.m.DriveOutput("other", strip, command), "output lamp/strip is not claimed by other")

	// The owner is called after every evaluation with the same snapshot.
	var seen *Snapshot
	h.m.OnOutputTick(func(snapshot *Snapshot) { seen = snapshot })
	snapshot := h.snapshot("teleop", 5)
	h.m.UpdateOutputs(snapshot)
	assert.Same(t, snapshot, seen)

	// Releasing a claim clears what it drove.
	require.Nil(t, h.m.ClaimOutputs("lighting", []ChannelRef{spare}))
	assert.Nil(t, lamp.Output("strip"))
	h.m.ReleaseOutputs("lighting")
	assert.Empty(t, h.m.Claims())
}

func TestFieldState(t *testing.T) {
	h := newHarness(t)
	follower := h.connect("follower")
	h.m.UpdateOutputs(h.snapshot("teleop", 26.6))
	assert.Equal(t, &FieldState{
		State: "running", Phase: "teleop", PhaseRemaining: 93.4, MatchRemaining: 93.4,
		Scores: map[string]float64{"red": 0, "blue": 0},
	}, follower.Field())

	// Sent again when it changes, or once a second.
	h.virtual.field, follower.field = nil, nil
	h.m.UpdateOutputs(h.snapshot("teleop", 27))
	assert.Nil(t, follower.Field(), "only the clock moved")
	h.clock.Advance(time.Second)
	h.m.UpdateOutputs(h.snapshot("teleop", 28))
	assert.Equal(t, 92.0, follower.Field().PhaseRemaining)
	follower.field = nil
	h.m.UpdateOutputs(h.snapshot("", 0))
	assert.Equal(t, "idle", follower.Field().State)
}
