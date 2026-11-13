package field

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/lighting"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// waitForLighting waits until the Lighting engine has no reload under way in the background.
func waitForLighting(t *testing.T, arena *Arena) {
	t.Helper()
	waitUntil(t, func() bool { return !arena.Lighting.Status().Reloading }, "the lighting to load")
}

// A virtual LED strip lit from the Lighting page follows a whole match of the ball-count game: the moments come in
// order, and the strip shows each one's look.
func TestLightingFollowsAMatch(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	startDevices(t, arena, false)
	t.Cleanup(func() { waitForLighting(t, arena) })
	goal, err := arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "goal", Channels: []*device.Channel{
		{ID: "strip", Dir: device.DirOut, Type: device.TypeLEDStrip, Pixels: 30},
	}})
	require.Nil(t, err)
	require.Nil(t, arena.Devices.Approve("goal"))

	engine := arena.Lighting
	_, err = engine.SaveFixture(arena.Game, "default", &lighting.Fixture{
		ID: "goal", Name: "Red goal", Device: "goal", Channel: "strip", Kind: lighting.KindLEDStrip, Alliance: "red",
	}, true)
	require.Nil(t, err)
	looks := map[string]*lighting.Look{
		"teams_ready":  {P: "solid", C: []string{"#00ff00"}},
		"cue.endgame":  {P: "blink", C: []string{"#ffff00"}, Ms: 200},
		"match_end":    {P: "pulse", C: []string{"#8000ff"}},
		"score_posted": {P: "chase", C: []string{"winner"}},
		"phase.pause":  {P: "progress", C: []string{"alliance", "#101010"}, V: "phase_remaining / phase_duration"},
	}
	for moment, look := range looks {
		_, err = engine.SetCell(arena.Game, "default", moment, "goal", look)
		require.Nil(t, err)
	}
	assert.Equal(t, map[device.ChannelRef]string{{Device: "goal", Channel: "strip"}: lighting.Owner},
		arena.Devices.Claims())

	var moments []string
	var commands []*device.Command
	tick := func() {
		for i := 0; i < deviceOutputTicks; i++ {
			arena.Update()
		}
		moment := engine.Live().Moment
		if len(moments) == 0 || moments[len(moments)-1] != moment {
			moments = append(moments, moment)
			commands = append(commands, goal.Output("strip"))
		}
	}

	tick()
	bypassAll(arena)
	tick()
	require.Nil(t, arena.StartMatch())
	tick()
	for _, seconds := range []float64{5, 16, 30, 116.9} {
		setMatchTime(arena, seconds)
		tick()
	}
	_, err = arena.ApplyScoreChange(scoring.Change{
		Alliance: "red", Element: "balls", Op: scoring.OpIncrement,
		Source: scoring.Source{Kind: scoring.SourceScorer, ID: "test"},
	})
	require.Nil(t, err)
	setMatchTime(arena, 117.1) // The endgame cue: 20 seconds left.
	tick()
	time.Sleep(lighting.CueDuration)
	setMatchTime(arena, 120)
	tick()
	setMatchTime(arena, 137.5)
	tick()
	require.Equal(t, PostMatch, arena.MatchState)

	// Commit and post, as the Commit & Post button does, and load the next match.
	result, _, _ := arena.ScoreSnapshot()
	arena.PublishMatchCommitted(arena.CurrentMatch, result, false)
	require.Nil(t, arena.ResetMatch())
	require.Nil(t, arena.LoadTestMatch())
	tick()

	assert.Equal(t, []string{
		"idle", "teams_ready", "phase.auto", "phase.pause", "phase.teleop", "cue.endgame", "phase.teleop",
		"match_end", "score_posted",
	}, moments)
	red := "#d32f2f"
	pauseFill := 0.5 // A second into the two-second pause: half of it left, 15 of 30 LEDs.
	assert.Equal(t, []*device.Command{
		{P: "solid", C: []string{"#ffffff"}},
		{P: "solid", C: []string{"#00ff00"}},
		{P: "solid", C: []string{red}},
		{P: "progress", C: []string{red, "#101010"}, V: &pauseFill},
		{P: "solid", C: []string{red}},
		{P: "blink", C: []string{"#ffff00"}, Ms: 200},
		{P: "solid", C: []string{red}},
		{P: "pulse", C: []string{"#8000ff"}},
		{P: "chase", C: []string{red}},
	}, commands)
	assert.Empty(t, engine.Live().Errors)
}
