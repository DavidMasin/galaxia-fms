package device

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// testEnvironment knows a device with one channel of every kind, and a device that has not sent its manifest.
func testEnvironment(t *testing.T) Environment {
	return Environment{
		Game: testGame(t), Stations: testStations,
		Manifests: map[string]*Manifest{
			"box": {Proto: 1, ID: "box", Channels: []*Channel{
				digital("button"), safety("stop"), {ID: "count", Dir: DirIn, Type: TypeCounter},
				{ID: "level", Dir: DirIn, Type: TypeAnalog}, {ID: "turns", Dir: DirIn, Type: TypeEncoder},
				{ID: "tag", Dir: DirIn, Type: TypeText}, output("lamp", TypeDigital), output("relay", TypeRelay),
				output("fan", TypePWM), output("strip", TypeLEDStrip), output("screen", TypeDisplay),
				output("horn", TypeSound),
			}},
			"silent": nil,
		},
		Claims: map[ChannelRef]string{{Device: "box", Channel: "relay"}: "lighting"},
	}
}

func problemsOf(t *testing.T, env Environment, source string) Problems {
	t.Helper()
	set, err := ParseBindings([]byte(source))
	require.Nil(t, err)
	return Validate(set, env)
}

func TestValidBindings(t *testing.T) {
	env := testEnvironment(t)
	problems := problemsOf(t, env, `
inputs:
  - { id: goal, device: box, channel: count, phases: [auto, teleop],
      action: { score: balls, op: increment, alliance: red } }
  - { id: press, device: box, channel: button, edge: falling, debounce_ms: 30, min_interval_ms: 500,
      action: { score: climb, op: set, value: high, alliance: red, robot: 1 } }
  - { id: lit, device: box, channel: button, invert: true, action: { score: lit, op: toggle, alliance: blue } }
  - { id: hold, device: box, channel: button, action: { score: balance, op: hold, alliance: red } }
  - { id: weigh, device: box, channel: level, scale: 0.001, offset: -0.2,
      action: { score: payload, op: set, alliance: blue } }
  - { id: heavy, device: box, channel: level, threshold: 5, hysteresis: 0.5, action: { score: comets, op: increment } }
  - { id: spin, device: box, channel: turns, action: { score: payload, op: set, alliance: red } }
  - { id: read, device: box, channel: tag, action: { score: climb, op: set, alliance: blue, robot: 2 } }
  - { id: start, device: box, channel: button, action: { field: start_match } }
  - { id: stop-r1, device: box, channel: stop, action: { field: estop, station: R1 } }
  - { id: later, device: newcomer, channel: button, action: { field: volunteers } }
outputs:
  - { id: green, device: box, channel: lamp, when: "match_state == 'running'", look: { p: blink, ms: 400 } }
  - { id: fan, device: box, channel: fan, value: "phase_remaining / phase_duration" }
  - { id: goal-strip, device: box, channel: strip, alliance: red, when: "own.hub_active",
      look: { p: blink, c: [alliance], ms: 400 }, else: { p: solid, c: ["#101010"] } }
  - { id: screen, device: box, channel: screen, value: "stations.R1.team" }
  - { id: horn, device: box, channel: horn, on: phase.teleop.start, look: { play: buzzer } }
  - { id: flash, device: silent, channel: light, on: cue.go, pulse_ms: 300 }
`)
	assert.Empty(t, problems.Errors(), "%v", problems)
	// Bindings to devices the FMS has not heard from are kept, and checked again when it does.
	if assert.Equal(t, 2, len(problems)) {
		assert.Equal(t, Problem{
			Binding: "later", Warning: true,
			Message: `device "newcomer" has not connected yet; the binding is checked against it when it does`,
		}, problems[0])
		assert.Equal(t, "flash", problems[1].Binding)
		assert.Contains(t, problems[1].Message, "has not sent its manifest yet")
	}
}

func TestBindingErrors(t *testing.T) {
	env := testEnvironment(t)
	cases := []struct {
		binding string
		want    string
	}{
		// Inputs.
		{`{ id: x, device: box, channel: count, action: { score: bals, op: increment, alliance: red } }`,
			`there is no scoring element "bals" in game "device-test"`},
		{`{ id: x, device: box, channel: count, action: { score: manual, op: increment, alliance: red } }`,
			`game "device-test" does not let devices change manual (its edit list is [scorer])`},
		{`{ id: x, device: box, channel: button, action: { score: lit, op: increment, alliance: red } }`,
			"lit is a boolean; op increment does not apply to it (use set, toggle)"},
		{`{ id: x, device: box, channel: button, action: { score: balls, op: add, alliance: red } }`,
			`op must be increment, decrement, set, toggle, hold, timer_start or timer_stop, not "add"`},
		{`{ id: x, device: box, channel: count, action: { score: lit, op: toggle, alliance: red } }`,
			`op toggle takes a digital input; "count" is counter`},
		{`{ id: x, device: box, channel: level, action: { score: balance, op: hold, alliance: red } }`,
			`op hold takes a digital input; "level" is analog`},
		{`{ id: x, device: box, channel: count, action: { score: payload, op: set, alliance: red } }`,
			`op set takes a digital or analog or encoder or text input; "count" is counter`},
		{`{ id: x, device: box, channel: count, action: { score: balls, op: increment, alliance: green } }`,
			`"green" is not an alliance of game "device-test" (red, blue)`},
		{`{ id: x, device: box, channel: count, action: { score: balls, op: increment } }`,
			"balls is scored per alliance; say which (one of red, blue)"},
		{`{ id: x, device: box, channel: count, action: { score: comets, op: increment, alliance: red } }`,
			"comets is scored for the field as a whole; it takes no alliance or robot"},
		{`{ id: x, device: box, channel: button,
			action: { score: climb, op: set, value: high, alliance: red, robot: 4 } }`,
			"climb is scored per robot; robot must be 1 to 3"},
		{`{ id: x, device: box, channel: button,
			action: { score: climb, op: set, value: top, alliance: red, robot: 1 } }`,
			"climb is an enum; value must be one of none, low, high, not top"},
		{`{ id: x, device: box, channel: button, action: { score: lit, op: set, alliance: red } }`,
			"set with a digital input needs a value to set lit to"},
		{`{ id: x, device: box, channel: button, action: { score: lit, op: set, value: 1, alliance: red } }`,
			"lit is a boolean; value must be true or false, not 1"},
		{`{ id: x, device: box, channel: level, action: { score: payload, op: set, value: 3, alliance: red } }`,
			`set with analog input "level" sets payload to the reading; it takes no value`},
		{`{ id: x, device: box, channel: level, action: { score: lit, op: set, alliance: red } }`,
			"an analog input sets a number to its scaled reading; lit is a boolean"},
		{`{ id: x, device: box, channel: tag, action: { score: payload, op: set, alliance: red } }`,
			"a text input can only set an enum"},
		{`{ id: x, device: box, channel: count, action: { score: balls, op: increment, value: 2, alliance: red } }`,
			"a counter input changes balls by the difference in its total; it takes no value"},
		{`{ id: x, device: box, channel: button, action: { score: balls, op: increment, value: 1.5, alliance: red } }`,
			"value must be a positive number (a whole one for a counter), not 1.5"},
		{`{ id: x, device: box, channel: button, action: { score: lit, op: toggle, value: true, alliance: red } }`,
			"op toggle takes no value"},
		{`{ id: x, device: box, channel: button, edge: both, action: { score: balance, op: hold, alliance: red } }`,
			"hold runs the timer while the input is 1, so it takes no edge"},
		{`{ id: x, device: box, channel: count, edge: rising, action: { score: balls, op: increment, alliance: red } }`,
			`edge is for digital inputs; "count" is counter`},
		{`{ id: x, device: box, channel: button, edge: up, action: { field: start_match } }`,
			`edge must be rising, falling or both, not "up"`},
		{`{ id: x, device: box, channel: count, debounce_ms: 20,
			action: { score: balls, op: increment, alliance: red } }`,
			`debounce_ms is for digital inputs; "count" is counter`},
		{`{ id: x, device: box, channel: count, min_interval_ms: 20,
			action: { score: balls, op: increment, alliance: red } }`,
			"min_interval_ms does not apply to it"},
		{`{ id: x, device: box, channel: stop, invert: true, action: { field: estop, station: R1 } }`,
			`"stop" is a safety channel, which always reads 1 when safe; it cannot be inverted`},
		{`{ id: x, device: box, channel: button, scale: 2, action: { field: start_match } }`,
			"scale, offset and threshold are for analog and encoder inputs"},
		{`{ id: x, device: box, channel: level, hysteresis: 1, action: { score: payload, op: set, alliance: red } }`,
			"hysteresis needs a threshold"},
		{`{ id: x, device: box, channel: button, phases: [overtime], action: { field: start_match } }`,
			`"overtime" is not a phase of game "device-test"`},
		{`{ id: x, device: box, channel: missing, action: { field: start_match } }`,
			`device "box" has no channel "missing"`},
		{`{ id: x, device: box, channel: lamp, action: { field: start_match } }`,
			`channel "lamp" of device "box" is an output, not an input`},
		{`{ id: x, device: Box, channel: button, action: { field: start_match } }`,
			`device "Box" is not a valid device id`},
		{`{ id: x, device: box, channel: button, action: { field: launch } }`,
			`field action must be start_match, abort_match, estop, astop, field_stop, field_reset, volunteers, ` +
				`commit or next_match, not "launch"`},
		{`{ id: x, device: box, channel: button, action: { field: estop, station: R4 } }`,
			`estop needs the station it stops, one of B1, B2, B3, R1, R2, R3; "R4" is not`},
		{`{ id: x, device: box, channel: button, action: { field: field_stop, station: R1 } }`,
			"field_stop stops the whole field; it takes no station"},
		{`{ id: x, device: box, channel: button, action: { field: start_match, station: R1 } }`,
			"station is only for estop and astop"},
		{`{ id: x, device: box, channel: button, edge: falling, action: { field: estop, station: R1 } }`,
			"a stop is held for as long as the input is active, at any time"},
		{`{ id: x, device: box, channel: count, action: { field: start_match } }`,
			`field actions take a digital input; "count" is counter`},
		{`{ id: x, device: box, channel: button, action: { field: start_match, op: set } }`,
			"op, value, alliance and robot are for score actions"},
		{`{ id: x, device: box, channel: button, action: {} }`,
			"the action needs score (an element to change) or field (a field action)"},
		{`{ id: x, device: box, channel: button, action: { score: lit, field: start_match } }`,
			"an action either changes the score (score) or acts on the field (field), not both"},
		{`{ id: "bad id", device: box, channel: button, action: { field: start_match } }`,
			`input binding id "bad id" must be 1 to 64 letters`},
	}
	for _, c := range cases {
		problems := problemsOf(t, env, "inputs:\n  - "+c.binding+"\n").Errors()
		if assert.NotEmpty(t, problems, c.binding) {
			assert.Contains(t, problems.Err().Error(), c.want, c.binding)
		}
	}

	outputCases := []struct {
		binding string
		want    string
	}{
		{`{ id: x, device: box, channel: lamp, when: "own.hub_active" }`, `unknown name "own"`},
		{`{ id: x, device: box, channel: lamp, alliance: red, when: "own.hub_activ" }`, `when "own.hub_activ"`},
		{`{ id: x, device: box, channel: lamp, when: "stations.R9.estop" }`, "R9"},
		{`{ id: x, device: box, channel: lamp, alliance: green, when: "true" }`, `"green" is not an alliance`},
		{`{ id: x, device: box, channel: lamp, when: "true", on: match.start, pulse_ms: 100 }`,
			"an output takes when (a condition) or on (an event), not both"},
		{`{ id: x, device: box, channel: lamp, on: phase.overtime.start, pulse_ms: 100 }`,
			`"phase.overtime.start" is not phase.<id>.start or phase.<id>.end of a phase`},
		{`{ id: x, device: box, channel: lamp, on: cue.missing, pulse_ms: 100 }`,
			`game "device-test" has no cue "missing"`},
		{`{ id: x, device: box, channel: lamp, on: match.begin, pulse_ms: 100 }`, `"match.begin" is not an event`},
		{`{ id: x, device: box, channel: lamp, on: match.start }`, "on needs pulse_ms, how long the output stays on"},
		{`{ id: x, device: box, channel: lamp, when: "true", pulse_ms: 100 }`, "pulse_ms is for outputs that pulse"},
		{`{ id: x, device: box, channel: lamp }`, "a digital output needs when, value or on"},
		{`{ id: x, device: box, channel: lamp, when: "true", look: { p: chase } }`,
			`can be off, solid or blink, not "chase"`},
		{`{ id: x, device: box, channel: lamp, when: "true", look: { p: blink, c: ["#ffffff"] } }`,
			"only p and ms apply to a digital output"},
		{`{ id: x, device: box, channel: strip, when: "true" }`, "a led_strip output needs a look"},
		{`{ id: x, device: box, channel: strip, value: "1", look: { p: solid } }`, "value does not apply"},
		{`{ id: x, device: box, channel: strip, look: { p: sparkle } }`, `look: p must be off, solid, blink`},
		{`{ id: x, device: box, channel: strip, look: { p: solid, c: [red] } }`, `look: "red" is not a colour`},
		{`{ id: x, device: box, channel: strip, look: { p: solid, c: [alliance] } }`,
			`look: the colour "alliance" needs the binding to name its alliance`},
		{`{ id: x, device: box, channel: strip, look: { p: solid, br: 2 } }`, "look: br is a brightness from 0 to 1"},
		{`{ id: x, device: box, channel: strip, look: { p: progress, v: "phase_remaning" } }`,
			`look v "phase_remaning"`},
		{`{ id: x, device: box, channel: strip, look: { seg: [{ s: 0, n: 0 }] } }`, "segment 1 needs a start s"},
		{`{ id: x, device: box, channel: screen, when: "true" }`, "a display output needs a value"},
		{`{ id: x, device: box, channel: horn, look: { play: buzzer } }`, "a sound output needs when"},
		{`{ id: x, device: box, channel: horn, on: match.start, look: { p: solid } }`,
			"a sound output needs a look with only play"},
		{`{ id: x, device: box, channel: relay, when: "true" }`,
			"channel box/relay is driven by lighting; an output is driven by one binding or by lighting, not both"},
		{`{ id: x, device: plc, channel: stack_light_green, when: "true" }`,
			"the PLC's coils are driven by the arena and cannot be bound"},
		{`{ id: x, device: box, channel: button, when: "true" }`, `channel "button" of device "box" is an input`},
	}
	for _, c := range outputCases {
		problems := problemsOf(t, env, "outputs:\n  - "+c.binding+"\n").Errors()
		if assert.NotEmpty(t, problems, c.binding) {
			assert.Contains(t, problems.Err().Error(), c.want, c.binding)
		}
	}

	// Ids are unique across inputs and outputs, and an output channel has one binding.
	problems := problemsOf(t, env, `
inputs:
  - { id: same, device: box, channel: button, action: { field: start_match } }
outputs:
  - { id: same, device: box, channel: lamp, when: "true" }
  - { id: first, device: box, channel: fan, value: "0.5" }
  - { id: second, device: box, channel: fan, value: "0.7" }
`).Errors()
	if assert.Equal(t, 2, len(problems)) {
		assert.Equal(t, Problem{Binding: "same", Message: "the id is used by another binding"}, problems[0])
		assert.Equal(t, Problem{
			Binding: "second",
			Message: `channel box/fan is already driven by binding "first"; an output can have only one`,
		}, problems[1])
	}

	// Without a game there is nothing to bind to.
	problems = Validate(&BindingSet{Inputs: []*InputBinding{{ID: "x"}}}, Environment{})
	assert.Equal(t, Problems{{Message: "there is no active game to bind to"}}, problems)
}

func TestBindingsYaml(t *testing.T) {
	source := `
inputs:
  - id: red-goal
    device: beam-red-1
    channel: count
    action: { score: balls, op: increment, alliance: red }
    phases: [auto, teleop]
  - id: climb
    device: climb-red
    channel: plate
    edge: rising
    invert: false
    debounce_ms: 30
    min_interval_ms: 500
    action: { score: climb, op: set, value: high, alliance: red, robot: 1 }
  - id: scale
    device: load-cell
    channel: kg
    scale: 0.001
    offset: -0.2
    action: { score: payload, op: set, alliance: blue }
  - id: estop-red-1
    device: estop-box-red
    channel: stop
    action: { field: estop, station: R1 }
outputs:
  - id: goal-strip
    device: goal-red
    channel: strip
    alliance: red
    when: "own.hub_active"
    look: { p: blink, c: ["alliance"], ms: 400 }
    else: { p: solid, c: ["#101010"] }
  - id: fill
    device: timer-strip
    channel: strip
    look: { p: progress, c: ["#00c853", "#202020"], v: "phase_remaining / phase_duration" }
  - id: buzzer
    device: stack-light
    channel: buzzer
    on: phase.teleop.start
    pulse_ms: 300
`
	set, err := ParseBindings([]byte(source))
	require.Nil(t, err)
	assert.Equal(t, 4, len(set.Inputs))
	assert.Equal(t, 3, len(set.Outputs))
	assert.Equal(t, 0.001, *set.Inputs[2].Scale)
	assert.Equal(t, "high", set.Inputs[1].Action.Value)
	assert.Equal(t, []string{"#00c853", "#202020"}, set.Outputs[1].Look.C)

	exported, err := set.YAML()
	require.Nil(t, err)
	again, err := ParseBindings(exported)
	require.Nil(t, err)
	assert.Equal(t, set, again, "a set survives export and import unchanged")
	reexported, _ := again.YAML()
	assert.Equal(t, string(exported), string(reexported))
	assert.True(t, strings.HasPrefix(string(exported), "inputs:\n  - id: red-goal\n"), string(exported))

	// JSON is YAML too.
	fromJson, err := ParseBindings(
		[]byte(`{"inputs":[{"id":"a","device":"b","channel":"c","action":{"field":"start_match"}}]}`),
	)
	require.Nil(t, err)
	assert.Equal(t, "start_match", fromJson.Inputs[0].Action.Field)

	// Typos are errors, not silently ignored keys.
	_, err = ParseBindings(
		[]byte("inputs:\n  - { id: a, device: b, channel: c, debounce: 30, action: { field: start_match } }\n"),
	)
	assert.ErrorContains(t, err, "field debounce not found")
	_, err = ParseBindings([]byte("inputs: [ { id: a\n"))
	assert.ErrorContains(t, err, "the bindings are not valid")
	empty, err := ParseBindings(nil)
	assert.Nil(t, err)
	assert.Equal(t, &BindingSet{}, empty)
}
