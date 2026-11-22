package device

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// The bindings docs/milestones/kickoff-2025.md gives for the electronics the 2025 game manual describes: a sensor
// behind each processor, a switch on a branch of a practice reef, the three lights on each side of the barge and
// the coral counts on the back of the timer. They are checked here against the reefscape example game, so that
// the report does not promise something a field crew cannot type in.
const reefscapeBindings = `
inputs:
  - { id: red-processor, device: processor-red, channel: count,
      action: { score: processor, op: increment, alliance: red } }
  - { id: blue-processor, device: processor-blue, channel: count,
      action: { score: processor, op: increment, alliance: blue } }
  - { id: red-l4-a-on, device: reef-red, channel: l4a, edge: rising, debounce_ms: 100,
      action: { score: l4_a, op: set, value: true, alliance: red } }
  - { id: red-l4-a-off, device: reef-red, channel: l4a, edge: falling, debounce_ms: 100,
      action: { score: l4_a, op: set, value: false, alliance: red } }
outputs:
  - id: barge-red
    device: barge-lights
    channel: red
    alliance: red
    when: "not field.last_seconds"
    look: { p: progress, c: [alliance, "#101010"], v: "own.barge_lights / 3" }
    else: { p: blink, c: [alliance], ms: 500 }
  - id: barge-blue
    device: barge-lights
    channel: blue
    alliance: blue
    when: "not field.last_seconds"
    look: { p: progress, c: [alliance, "#101010"], v: "own.barge_lights / 3" }
    else: { p: blink, c: [alliance], ms: 500 }
  - id: timer-red
    device: timer-red
    channel: back
    alliance: red
    value: "string(own.l1) + '-' + string(own.l2) + '-' + string(own.l3) + '-' + string(own.l4)"
`

func TestReefscapeBindings(t *testing.T) {
	game, diagnostics := gamedef.LoadFile("../games/reefscape/game.yaml")
	require.NotNil(t, game, "%v", diagnostics)
	counter := func(id string) *Channel {
		return &Channel{ID: id, Dir: DirIn, Type: TypeCounter}
	}
	env := Environment{
		Game: game, Stations: testStations,
		Manifests: map[string]*Manifest{
			"processor-red":  {Proto: 1, ID: "processor-red", Channels: []*Channel{counter("count")}},
			"processor-blue": {Proto: 1, ID: "processor-blue", Channels: []*Channel{counter("count")}},
			"reef-red":       {Proto: 1, ID: "reef-red", Channels: []*Channel{digital("l4a")}},
			"barge-lights": {Proto: 1, ID: "barge-lights", Channels: []*Channel{
				output("red", TypeLEDStrip), output("blue", TypeLEDStrip),
			}},
			"timer-red": {Proto: 1, ID: "timer-red", Channels: []*Channel{output("back", TypeDisplay)}},
		},
	}
	assert.Empty(t, problemsOf(t, env, reefscapeBindings))

	// The game lets a sensor score these, and nothing else of the reef's latch.
	for _, id := range []string{"processor", "net", "trough", "l2_a", "l4_l"} {
		assert.True(t, game.Element(id).EditableBy("device"), id)
	}
	for _, id := range []string{"auto_l4_a", "auto_trough", "leave", "barge"} {
		assert.False(t, game.Element(id).EditableBy("device"), id)
	}
	problems := problemsOf(t, env, `
inputs:
  - { id: latch, device: reef-red, channel: l4a, action: { score: auto_l4_a, op: toggle, alliance: red } }
`)
	if assert.Len(t, problems, 1) {
		assert.Contains(t, problems[0].Message, "auto_l4_a")
	}
}
