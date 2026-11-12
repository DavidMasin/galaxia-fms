package scoring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/stretchr/testify/assert"
)

const gamesDir = "../games"

func loadGame(t *testing.T, id string) *gamedef.Game {
	game, diagnostics := gamedef.LoadFile(filepath.Join(gamesDir, id, gamedef.GameFile))
	if !assert.NotNil(t, game, diagnostics.Error()) {
		t.FailNow()
	}
	return game
}

func TestExampleGameCases(t *testing.T) {
	for _, id := range []string{"ball-count", "orbital-rush", "rebuilt-style"} {
		game := loadGame(t, id)
		src, err := os.ReadFile(filepath.Join(gamesDir, id, "tests.yaml"))
		assert.NoError(t, err)
		outcomes, err := RunCases(game, src)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(outcomes), 4, id)
		for _, outcome := range outcomes {
			assert.Empty(t, outcome.Failures, "%s: %s", id, outcome.Name)
		}
	}
}

func TestCasesReportFailures(t *testing.T) {
	game := loadGame(t, "ball-count")
	outcomes, err := RunCases(game, []byte(`
cases:
  - name: wrong on purpose
    state:
      red: { balls: { auto: 3 } }
    expect:
      winner: blue
      red: { score: 7, result: loss, points: { balls: 5, nope: 1 }, rp: { win: 0 }, colour: red }
      purple: { score: 1 }
  - state:
      red: { ballz: 3 }
  - state:
      red: { balls: 3 }
`))
	assert.NoError(t, err)
	if assert.Len(t, outcomes, 3) {
		assert.Equal(t, "wrong on purpose", outcomes[0].Name)
		assert.Equal(t, []string{
			`expect.purple is not an alliance, "field", "winner" or "tiebreak"`,
			"red.colour is not something a case can check",
			"red.points.balls: got 6, want 5",
			"red.points.nope does not exist",
			`red.result: got "win", want "loss"`,
			"red.rp.win: got 2, want 0",
			"red.score: got 6, want 7",
			`winner: got "red", want "blue"`,
		}, outcomes[0].Failures)
		assert.Equal(t, "case 2", outcomes[1].Name)
		assert.Equal(t, []string{`state of red: there is no element "ballz"`}, outcomes[1].Failures)
		assert.Contains(t, outcomes[2].Failures[0], "write its counts per phase")
	}

	_, err = RunCases(game, []byte("cases: [nope"))
	assert.Error(t, err)
}

func change(seq int, alliance, element, op string, value any) Change {
	return Change{Seq: seq, Alliance: alliance, Element: element, Op: op, Value: value, Phase: "orbit"}
}

func TestApplyEveryElementType(t *testing.T) {
	game := loadGame(t, "orbital-rush")
	state := NewState(game)
	red := state.Alliances["red"]

	// Starting values, including declared initial values.
	assert.Equal(t, 0, red.Elements["cargo"].Total())
	assert.Equal(t, "down", red.Elements["shield"].Option)
	assert.Equal(t, "none", red.Robots[0]["dock"].Option)
	assert.Equal(t, 100.0, red.Robots[1]["fuel_left"].Number)
	assert.Equal(t, 1.0, state.Field["gravity"].Number)
	assert.Equal(t, "calm", state.Field["storm"].Option)
	assert.False(t, state.Field["gate_open"].Bool)

	// Counter: per-phase buckets, a default amount of one, and limits.
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "cargo", Op: OpIncrement, Phase: "launch"}))
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "cargo", Op: OpIncrement, Value: 4, Phase: "orbit"}))
	// A change made in the grace window is credited to the phase that just ended.
	assert.NoError(t, state.Apply(game, &Change{
		Alliance: "red", Element: "cargo", Op: OpIncrement, Phase: "drift", Bucket: "launch",
	}))
	assert.Equal(t, map[string]int{"launch": 2, "orbit": 4}, red.Elements["cargo"].Counts)
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "cargo", Op: OpDecrement, Value: 4, Phase: "orbit"}))
	assert.Equal(t, map[string]int{"launch": 2}, red.Elements["cargo"].Counts)
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "cargo", Op: OpSet, Value: 7.0, Bucket: "reentry"}))
	assert.Equal(t, 9, red.Elements["cargo"].Total())
	for _, bad := range []Change{
		{Alliance: "red", Element: "cargo", Op: OpDecrement, Phase: "orbit"},               // Nothing to remove.
		{Alliance: "red", Element: "cargo", Op: OpIncrement, Value: 95, Phase: "orbit"},    // Above max.
		{Alliance: "red", Element: "cargo", Op: OpIncrement, Value: 1.5, Phase: "orbit"},   // Not whole.
		{Alliance: "red", Element: "cargo", Op: OpIncrement, Value: -1, Phase: "orbit"},    // Negative.
		{Alliance: "red", Element: "cargo", Op: OpIncrement, Phase: PhasePost},             // No phase to credit.
		{Alliance: "red", Element: "cargo", Op: OpToggle, Phase: "orbit"},                  // Wrong operation.
		{Alliance: "purple", Element: "cargo", Op: OpIncrement, Phase: "orbit"},            // No such alliance.
		{Alliance: "red", Element: "nope", Op: OpIncrement, Phase: "orbit"},                // No such element.
		{Alliance: "red", Element: "relays", Op: OpIncrement, Phase: "orbit"},              // Robot element, no robot.
		{Alliance: "red", Robot: 3, Element: "relays", Op: OpIncrement, Phase: "orbit"},    // Robot out of range.
		{Alliance: "red", Element: "shield", Op: OpSet, Value: "sideways", Phase: "orbit"}, // No such option.
		{Alliance: "red", Element: "shield", Op: OpIncrement, Phase: "orbit"},
		{Alliance: "red", Element: "beacon", Op: OpSet, Value: "yes", Phase: "launch"},
		{Alliance: "red", Element: "beacon", Op: OpIncrement, Phase: "launch"},
		{Alliance: "red", Element: "payload_mass", Op: OpSet, Value: 41, Phase: "orbit"},
		{Alliance: "red", Element: "payload_mass", Op: OpDecrement, Value: 1, Phase: "orbit"},
		{Alliance: "red", Element: "payload_mass", Op: OpSet, Value: "heavy", Phase: "orbit"},
		{Alliance: "red", Element: "payload_mass", Op: OpToggle, Value: 1, Phase: "orbit"},
		{Alliance: "red", Element: "station_hold", Op: OpSet, Value: -3, Phase: "orbit"},
		{Alliance: "red", Element: "station_hold", Op: OpIncrement, Phase: "orbit"},
	} {
		bad := bad
		assert.Error(t, state.Apply(game, &bad), "%+v", bad)
	}
	assert.Equal(t, 9, red.Elements["cargo"].Total(), "a rejected change must leave the state untouched")

	// Boolean: set and toggle, remembering the phase it became true in.
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "beacon", Op: OpToggle, Phase: "launch"}))
	assert.Equal(t, Value{Bool: true, Phase: "launch"}, *red.Elements["beacon"])
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "beacon", Op: OpSet, Value: false, Phase: "orbit"}))
	assert.Equal(t, Value{}, *red.Elements["beacon"])

	// Enum.
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Robot: 2, Element: "dock", Op: OpSet, Value: "high", Phase: "reentry"}))
	assert.Equal(t, Value{Option: "high", Phase: "reentry"}, *red.Robots[1]["dock"])
	assert.Equal(t, "none", red.Robots[0]["dock"].Option)

	// Timer: start, stop, restart, cap at max, and direct set.
	start := Change{Alliance: "red", Element: "station_hold", Op: OpTimerStart, MatchTime: 20}
	stop := Change{Alliance: "red", Element: "station_hold", Op: OpTimerStop, MatchTime: 32.5}
	assert.NoError(t, state.Apply(game, &start))
	assert.True(t, red.Elements["station_hold"].Running)
	start.MatchTime = 25 // Starting a running timer changes nothing.
	assert.NoError(t, state.Apply(game, &start))
	assert.NoError(t, state.Apply(game, &stop))
	assert.Equal(t, Value{Seconds: 12.5}, *red.Elements["station_hold"])
	assert.NoError(t, state.Apply(game, &stop)) // Stopping a stopped timer changes nothing.
	assert.Equal(t, 12.5, red.Elements["station_hold"].Seconds)
	start.MatchTime, stop.MatchTime = 40, 500
	assert.NoError(t, state.Apply(game, &start))
	assert.NoError(t, state.Apply(game, &stop))
	assert.Equal(t, 105.0, red.Elements["station_hold"].Seconds)
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "station_hold", Op: OpSet, Value: 3}))
	assert.Equal(t, 3.0, red.Elements["station_hold"].Seconds)

	// Number.
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "payload_mass", Op: OpSet, Value: 12.5}))
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "payload_mass", Op: OpIncrement, Value: 2}))
	assert.NoError(t, state.Apply(game, &Change{Alliance: "red", Element: "payload_mass", Op: OpDecrement, Value: 0.5}))
	assert.Equal(t, 14.0, red.Elements["payload_mass"].Number)

	// Field elements ignore the alliance.
	assert.NoError(t, state.Apply(game, &Change{Element: "comets", Op: OpIncrement, Phase: "orbit"}))
	assert.NoError(t, state.Apply(game, &Change{Element: "gravity", Op: OpSet, Value: 1.5}))
	assert.Equal(t, 1, state.Field["comets"].Total())
	assert.Equal(t, 1.5, state.Field["gravity"].Number)
}

func TestApplyFoulsCardsAndDisqualification(t *testing.T) {
	game := loadGame(t, "orbital-rush")
	state := NewState(game)
	blue := state.Alliances["blue"]

	assert.NoError(t, state.Apply(game, &Change{Seq: 5, Alliance: "blue", Element: TargetFoul, Op: OpAdd, Value: "minor", Phase: "orbit"}))
	assert.NoError(t, state.Apply(game, &Change{
		Seq: 6, Alliance: "blue", Robot: 2, Element: TargetFoul, Op: OpAdd, Value: "major", Rule: "O2", Phase: "reentry",
	}))
	assert.Equal(t, []Foul{
		{ID: 5, Type: "minor", Phase: "orbit"}, {ID: 6, Type: "major", Rule: "O2", Robot: 2, Phase: "reentry"},
	}, blue.Fouls)
	assert.NoError(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetFoul, Op: OpRemove, Value: 5}))
	assert.Equal(t, []Foul{{ID: 6, Type: "major", Rule: "O2", Robot: 2, Phase: "reentry"}}, blue.Fouls)
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetFoul, Op: OpRemove, Value: 5}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetFoul, Op: OpAdd, Value: "gigantic"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Robot: 9, Element: TargetFoul, Op: OpAdd, Value: "minor"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetFoul, Op: OpSet, Value: "minor"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "teal", Element: TargetFoul, Op: OpAdd, Value: "minor"}))

	assert.NoError(t, state.Apply(game, &Change{Alliance: "blue", Robot: 1, Element: TargetCard, Op: OpSet, Value: "yellow"}))
	assert.Equal(t, map[int]string{1: "yellow"}, blue.Cards)
	assert.NoError(t, state.Apply(game, &Change{Alliance: "blue", Robot: 1, Element: TargetCard, Op: OpSet, Value: ""}))
	assert.Empty(t, blue.Cards)
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Robot: 1, Element: TargetCard, Op: OpSet, Value: "purple"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetCard, Op: OpSet, Value: "yellow"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "teal", Robot: 1, Element: TargetCard, Op: OpSet, Value: "yellow"}))

	assert.NoError(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetDisqualified, Op: OpSet, Value: true}))
	assert.True(t, blue.Disqualified)
	assert.Error(t, state.Apply(game, &Change{Alliance: "blue", Element: TargetDisqualified, Op: OpSet, Value: "yes"}))
	assert.Error(t, state.Apply(game, &Change{Alliance: "teal", Element: TargetDisqualified, Op: OpSet, Value: true}))
}

func TestUndoRedoAndFold(t *testing.T) {
	game := loadGame(t, "ball-count")
	changes := []Change{
		{Seq: 1, Alliance: "red", Element: "balls", Op: OpIncrement, Phase: "auto"},
		{Seq: 2, Alliance: "red", Element: "balls", Op: OpIncrement, Phase: "teleop"},
		{Seq: 3, Alliance: "blue", Element: "balls", Op: OpIncrement, Value: 5, Phase: "teleop"},
		{Seq: 4, Alliance: "blue", Element: TargetFoul, Op: OpAdd, Value: "foul", Phase: "teleop"},
	}
	score := func() (float64, float64) {
		state, skipped := Fold(game, changes)
		assert.Empty(t, skipped)
		result, err := Compute(game, state, Options{})
		assert.NoError(t, err)
		return result.Alliances["red"].Score, result.Alliances["blue"].Score
	}
	red, blue := score()
	assert.Equal(t, 6.0, red) // 2 + 1 + 3 foul points
	assert.Equal(t, 5.0, blue)

	// Undo the foul.
	changes = append(changes, Change{Seq: 5, Reverts: 4, Source: Source{Kind: SourceReferee}})
	red, blue = score()
	assert.Equal(t, 3.0, red)
	assert.Len(t, Live(changes), 3)

	// Undo the undo: the foul is back.
	changes = append(changes, Change{Seq: 6, Reverts: 5})
	red, _ = score()
	assert.Equal(t, 6.0, red)
	assert.Len(t, Live(changes), 4)

	// Undo blue's five balls, then try to remove one of them: that removal no longer makes sense and is skipped
	// without breaking the rest.
	changes = append(changes,
		Change{Seq: 7, Reverts: 3},
		Change{Seq: 8, Alliance: "blue", Element: "balls", Op: OpDecrement, Phase: "teleop"},
	)
	state, skipped := Fold(game, changes)
	if assert.Len(t, skipped, 1) {
		assert.Equal(t, 8, skipped[0].Seq)
	}
	assert.Equal(t, 0, state.Alliances["blue"].Elements["balls"].Total())
	assert.Equal(t, 2, state.Alliances["red"].Elements["balls"].Total())
}

func TestTriggers(t *testing.T) {
	original := rules.PickFunc
	defer func() { rules.PickFunc = original }()
	rules.PickFunc = func(options []any) any { return options[len(options)-1] }

	// The 2026-style game decides the auto winner when teleop begins.
	rebuilt := loadGame(t, "rebuilt-style")
	state, err := BuildState(rebuilt, map[string]map[string]any{
		"red": {"fuel": map[string]any{"auto": 8}}, "blue": {"fuel": map[string]any{"auto": 5}},
	})
	assert.NoError(t, err)
	changes, err := TriggerChanges(rebuilt, state, Options{}, TriggerEvent{PhaseStart: "transition"})
	assert.NoError(t, err)
	if assert.Len(t, changes, 1) {
		assert.Equal(t, "auto_winner", changes[0].Element)
		assert.Equal(t, "", changes[0].Alliance)
		assert.Equal(t, OpSet, changes[0].Op)
		assert.Equal(t, "red", changes[0].Value)
		assert.Equal(t, Source{Kind: SourceRule, ID: "trigger-1", Name: "Game rule"}, changes[0].Source)
		assert.NoError(t, state.Apply(rebuilt, &changes[0]))
		assert.Equal(t, "red", state.Field["auto_winner"].Option)
	}

	// A tie is settled by pick(), here stubbed to take the last option.
	state = NewState(rebuilt)
	changes, err = TriggerChanges(rebuilt, state, Options{}, TriggerEvent{PhaseStart: "transition"})
	assert.NoError(t, err)
	assert.Equal(t, "blue", changes[0].Value)

	// Nothing fires on other events.
	changes, err = TriggerChanges(rebuilt, state, Options{}, TriggerEvent{PhaseEnd: "transition"})
	assert.NoError(t, err)
	assert.Empty(t, changes)

	// Orbital Rush has a trigger of each kind, including one evaluated per alliance.
	orbital := loadGame(t, "orbital-rush")
	state, err = BuildState(orbital, map[string]map[string]any{
		"red":   {"cargo": map[string]any{"orbit": 9}, "beacon": true},
		"blue":  {"cargo": map[string]any{"orbit": 4}},
		"green": {"cargo": map[string]any{"orbit": 9}},
	})
	assert.NoError(t, err)
	changes, err = TriggerChanges(orbital, state, Options{}, TriggerEvent{PhaseEnd: "launch"})
	assert.NoError(t, err)
	assert.Equal(t, "meteor", changes[0].Value)
	changes, err = TriggerChanges(orbital, state, Options{}, TriggerEvent{PhaseStart: "orbit"})
	assert.NoError(t, err)
	assert.Equal(t, true, changes[0].Value)
	changes, err = TriggerChanges(orbital, state, Options{}, TriggerEvent{Cue: "reentry_start"})
	assert.NoError(t, err)
	if assert.Len(t, changes, 3) {
		values := map[string]any{}
		for _, c := range changes {
			assert.Equal(t, "momentum", c.Element)
			values[c.Alliance] = c.Value
		}
		// Red and green are level on nine, so neither leads; blue trails.
		assert.Equal(t, map[string]any{"red": false, "blue": false, "green": false}, values)
	}
}

func TestLiveValues(t *testing.T) {
	game := loadGame(t, "rebuilt-style")
	state, err := BuildState(game, map[string]map[string]any{"field": {"auto_winner": "red"}})
	assert.NoError(t, err)
	result, err := Compute(game, state, Options{})
	assert.NoError(t, err)

	active := func(phase string, matchState string) (bool, bool) {
		live, err := result.Live(Clock{State: matchState, Phase: phase, PhaseDuration: 25, PhaseElapsed: 5})
		assert.NoError(t, err)
		return live.Alliances["red"]["hub_active"].(bool), live.Alliances["blue"]["hub_active"].(bool)
	}
	for phase, want := range map[string][2]bool{
		"auto": {true, true}, "pause": {true, true}, "transition": {true, true}, "endgame": {true, true},
		"shift1": {false, true}, "shift2": {true, false}, "shift3": {false, true}, "shift4": {true, false},
	} {
		red, blue := active(phase, "running")
		assert.Equal(t, want, [2]bool{red, blue}, phase)
	}
	red, blue := active("", "idle")
	assert.Equal(t, [2]bool{false, false}, [2]bool{red, blue})

	// Live values can read each other, and the clock.
	orbital := loadGame(t, "orbital-rush")
	state, err = BuildState(orbital, map[string]map[string]any{"field": {"gate_open": true}, "blue": {"momentum": true}})
	assert.NoError(t, err)
	result, err = Compute(orbital, state, Options{})
	assert.NoError(t, err)
	live, err := result.Live(Clock{State: "running", Phase: "reentry", PhaseDuration: 30, PhaseElapsed: 22})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"gate_lit": true, "urgency": "high"}, live.Field)
	assert.Equal(t, false, live.Alliances["red"]["lane_open"])
	assert.Equal(t, true, live.Alliances["blue"]["lane_open"])
	live, err = result.Live(Clock{State: "running", Phase: "launch", PhaseDuration: 15, PhaseElapsed: 1})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"gate_lit": false, "urgency": "normal"}, live.Field)
	assert.Equal(t, false, live.Alliances["blue"]["lane_open"])

	// The same environment evaluates game data for a station, the team sign, and any output binding.
	clock := Clock{State: "running", Phase: "reentry", PhaseDuration: 30, PhaseElapsed: 22, MatchRemaining: 8}
	station := map[string]any{"station": map[string]any{"id": "G2", "alliance": "green", "position": 2, "team": 254}}
	gameData, err := result.Eval(orbital.Match.DSGameData.Prog, "green", clock, station)
	assert.NoError(t, err)
	assert.Equal(t, "G2", gameData)
	_, err = result.Live(clock)
	assert.NoError(t, err)
	sign, err := result.Eval(orbital.Layout.TeamSign.Prog, "blue", clock, nil)
	assert.NoError(t, err)
	assert.Equal(t, "0 GO", sign)
	binding, err := rules.Compile("match_remaining <= 10 and own.cargo.total == 0", orbital.LiveRoots(true), rules.Options{})
	assert.NoError(t, err)
	lit, err := result.Eval(binding, "red", clock, nil)
	assert.NoError(t, err)
	assert.Equal(t, true, lit)
}

func TestComputeUsesTeamsAndParams(t *testing.T) {
	game, diagnostics := gamedef.Parse([]byte(`schema: 1
id: solo
name: Solo
alliances:
  size: 2
  list: [{ id: home, name: Home, short: H, color: "#123456" }]
params:
  bonus: { default: 4 }
match:
  phases: [{ id: run, duration: 60, robots: teleop }]
elements:
  - { id: laps, type: counter, scope: robot, points: { expr: "value.total * (robot.team == 254 ? 2 : 1)" } }
derived:
  - { id: extra, scope: alliance, points: "own.points.laps >= 4 ? params.bonus : 0" }
ranking:
  points: [{ id: win, expr: "result == 'win'" }]
`))
	assert.Empty(t, diagnostics)
	state, err := BuildState(game, map[string]map[string]any{
		"home": {"robots": []any{map[string]any{"laps": 2}, map[string]any{"laps": 1}}},
	})
	assert.NoError(t, err)

	result, err := Compute(game, state, Options{})
	assert.NoError(t, err)
	assert.Equal(t, 3.0, result.Alliances["home"].Score)

	result, err = Compute(game, state, Options{Teams: map[string][]int{"home": {254, 1114}}, Params: map[string]float64{"bonus": 10}})
	assert.NoError(t, err)
	home := result.Alliances["home"]
	assert.Equal(t, 15.0, home.Score) // 2*2 + 1, plus the bonus of 10.
	// A game with one alliance has nobody to lose to.
	assert.Equal(t, "home", result.Winner)
	assert.Equal(t, ResultWin, home.Result)
	assert.Equal(t, 1.0, home.RPTotal)

	state.Alliances["home"].Disqualified = true
	result, err = Compute(game, state, Options{})
	assert.NoError(t, err)
	assert.Equal(t, "", result.Winner)
	assert.Equal(t, ResultLoss, result.Alliances["home"].Result)
	assert.Equal(t, 0.0, result.Alliances["home"].Score)
}

func TestRankings(t *testing.T) {
	game := loadGame(t, "rebuilt-style")
	values := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	next := 0
	original := RandomFloat
	defer func() { RandomFloat = original }()
	RandomFloat = func() float64 { next++; return values[(next-1)%len(values)] }

	play := func(redFuel, blueFuel int) *Result {
		state, err := BuildState(game, map[string]map[string]any{
			"red": {"fuel": map[string]any{"auto": redFuel}}, "blue": {"fuel": map[string]any{"auto": blueFuel}},
		})
		assert.NoError(t, err)
		result, err := Compute(game, state, Options{})
		assert.NoError(t, err)
		return result
	}

	rankings := []TeamRanking{{TeamID: 254}, {TeamID: 1114}, {TeamID: 971}, {TeamID: 1678}, {TeamID: 2056}}
	// Match 1: 254 beats 1114, 20 to 10. Match 2: 971 ties 254 at 30. Match 3: 1114 beats 971 with 120 fuel, which
	// also earns the energized point. 1678 was disqualified from match 3. 2056 has not played.
	first, second, third := play(20, 10), play(30, 30), play(120, 5)
	rankings[0].AddMatch(first.Alliances["red"], false)
	rankings[1].AddMatch(first.Alliances["blue"], false)
	rankings[2].AddMatch(second.Alliances["red"], false)
	rankings[0].AddMatch(second.Alliances["blue"], false)
	rankings[1].AddMatch(third.Alliances["red"], false)
	rankings[2].AddMatch(third.Alliances["blue"], false)
	rankings[3].AddMatch(third.Alliances["red"], true)

	assert.NoError(t, SortRankings(game, rankings))
	order := make([]int, len(rankings))
	byTeam := map[int]TeamRanking{}
	for i, ranking := range rankings {
		order[i] = ranking.TeamID
		byTeam[ranking.TeamID] = ranking
		assert.Equal(t, i+1, ranking.Rank)
	}
	// 254: 3 + 1 = 4 RP in 2 matches (2.0). 1114: 0 + 3 + 1 = 4 RP in 2 (2.0). 971: 1 + 0 = 1 RP in 2 (0.5).
	// 254 and 1114 are level on ranking score; 1114 has the higher average match points (65 against 25).
	assert.Equal(t, []int{1114, 254, 971, 1678, 2056}, order)
	assert.Equal(t, 4.0, byTeam[254].RP)
	assert.Equal(t, [3]int{1, 0, 1}, [3]int{byTeam[254].Wins, byTeam[254].Losses, byTeam[254].Ties})
	assert.Equal(t, 4.0, byTeam[1114].RP)
	assert.Equal(t, 130.0, byTeam[1114].Stats["match_points"])
	assert.Equal(t, []float64{2, 65, 65, 0, 0.5}, byTeam[1114].Sort)
	assert.Equal(t, 1, byTeam[1678].Disqualifications)
	assert.Equal(t, 0.0, byTeam[1678].RP)
	assert.Equal(t, 1, byTeam[1678].Played)
	assert.Equal(t, 0, byTeam[2056].Played)
	assert.Equal(t, []float64{0, 0, 0, 0, 0}, byTeam[2056].Sort)

	// An ascending key ranks the lower value first.
	orbital := loadGame(t, "orbital-rush")
	few := TeamRanking{TeamID: 1, Played: 1, Stats: map[string]float64{"fouls_given": 1}}
	many := TeamRanking{TeamID: 2, Played: 1, Stats: map[string]float64{"fouls_given": 3}}
	orbitalRankings := []TeamRanking{many, few}
	assert.NoError(t, SortRankings(orbital, orbitalRankings))
	assert.Equal(t, 1, orbitalRankings[0].TeamID)
}
