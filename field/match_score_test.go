package field

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
	"time"
)

func TestApplyScoreChange(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	events := arena.Bus.Subscribe([]string{"score"}, 10)
	defer events.Close()
	redBalls := func() *scoring.Value {
		return arena.Score.State.Alliances["red"].Elements["balls"]
	}

	// Before the match nothing can be scored, except by an administrator.
	for _, source := range []scoring.Source{testDevice, testScorer, testReferee, {Kind: scoring.SourceAPI}} {
		_, err := arena.ApplyScoreChange(count("red", "balls", source))
		if assert.NotNil(t, err, source.Kind) {
			assert.Contains(t, err.Error(), "the match has not started")
		}
	}
	assert.Empty(t, arena.Score.Changes)
	assert.Empty(t, drainEvents(events))
	change, err := arena.ApplyScoreChange(
		scoring.Change{
			Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Value: 2, Bucket: "auto", Source: testAdmin,
		},
	)
	assert.Nil(t, err)
	assert.Equal(t, 1, change.Seq)
	assert.Equal(t, "", change.Phase)
	assert.Equal(t, "auto", change.Bucket)
	assert.Equal(t, 2, redBalls().Counts["auto"])
	assert.Equal(t, 4.0, allianceScore(arena, "red"))
	assert.Equal(t, 1, len(drainEvents(events)))

	// Loading a match starts the score over.
	assert.Nil(t, arena.LoadTestMatch())
	assert.Empty(t, arena.Score.Changes)
	assert.Equal(t, 0, redBalls().Total())
	drainEvents(events)

	// During the match a change is stamped with where in the match it happened.
	startTestMatch(t, arena)
	runMatchTo(arena, 6)
	before := time.Now().UTC()
	change, err = arena.ApplyScoreChange(
		scoring.Change{
			Seq: 99, Phase: "teleop", MatchTime: 1000, Reverts: 5, Alliance: "red", Element: "balls",
			Op: scoring.OpIncrement, Source: testDevice,
		},
	)
	assert.Nil(t, err)
	assert.Equal(t, 1, change.Seq)
	assert.Equal(t, "auto", change.Phase)
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, 0, change.Reverts)
	assert.InDelta(t, 6, change.MatchTime, 0.2)
	assert.False(t, change.Time.Before(before))
	assert.Equal(t, time.UTC, change.Time.Location())
	assert.Equal(t, testDevice, change.Source)
	assert.Equal(t, []scoring.Change{change}, arena.Score.Changes)
	assert.Equal(t, 1, redBalls().Counts["auto"])

	// The result is recomputed, and the change is published.
	assert.Equal(t, 2.0, allianceScore(arena, "red"))
	assert.Equal(t, "red", arena.Score.Result.Winner)
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, "score.changed", received[0].Type)
		assert.Equal(t, change, received[0].Data)
	}

	// Later changes get later sequence numbers and the phase of their own moment.
	runMatchTo(arena, 30)
	change = apply(t, arena, count("blue", "balls", testScorer))
	assert.Equal(t, 2, change.Seq)
	assert.Equal(t, "teleop", change.Phase)
	assert.Equal(t, 1.0, allianceScore(arena, "blue"))
	drainEvents(events)

	// A change that makes no sense for the game is refused and leaves no trace.
	for _, bad := range []scoring.Change{
		count("red", "no_such_element", testScorer),
		count("green", "balls", testScorer),
		{Alliance: "red", Element: "balls", Op: scoring.OpToggle, Source: testScorer},
		{Alliance: "red", Element: "balls", Op: scoring.OpDecrement, Value: 5, Source: testScorer},
		{Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Source: scoring.Source{Kind: "bystander"}},
		{Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Source: scoring.Source{}},
	} {
		_, err = arena.ApplyScoreChange(bad)
		assert.NotNil(t, err, "%+v", bad)
	}
	assert.Equal(t, 2, len(arena.Score.Changes))
	assert.Equal(t, 1, redBalls().Total())
	assert.Empty(t, drainEvents(events))
}

func TestScoreChangePermissionsBySourceKind(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	runMatchTo(arena, 5)
	api := scoring.Source{Kind: scoring.SourceAPI, ID: "integration"}
	rule := scoring.Source{Kind: scoring.SourceRule, ID: "test"}
	set := func(element string, robot int, value any, source scoring.Source) scoring.Change {
		change := scoring.Change{Element: element, Robot: robot, Op: scoring.OpSet, Value: value, Source: source}
		if element != "auto_winner" {
			change.Alliance = "red"
		}
		return change
	}
	foul := scoring.Change{Alliance: "red", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "minor"}
	card := scoring.Change{Alliance: "red", Robot: 2, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "yellow"}
	disqualify := scoring.Change{Alliance: "blue", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true}
	from := func(change scoring.Change, source scoring.Source) scoring.Change {
		change.Source = source
		return change
	}

	for _, testCase := range []struct {
		name          string
		change        scoring.Change
		expectedError string
	}{
		// An element lists who may change it while the match runs.
		{"device counts fuel", count("red", "fuel", testDevice), ""},
		{"scorer counts fuel", count("red", "fuel", testScorer), ""},
		{"referee cannot count fuel", count("red", "fuel", testReferee), "fuel cannot be changed by a referee"},
		{"scorer sets the auto tower", set("auto_tower", 1, "level1", testScorer), ""},
		{"referee sets the auto tower", set("auto_tower", 2, "level2", testReferee), ""},
		{"device cannot set the auto tower", set("auto_tower", 3, "level1", testDevice), "cannot be changed by a device"},
		{"scorer sets a field element", set("auto_winner", 0, "blue", testScorer), ""},
		{"referee cannot", set("auto_winner", 0, "red", testReferee), "auto_winner cannot be changed by a referee"},
		{"device cannot", set("auto_winner", 0, "red", testDevice), "auto_winner cannot be changed by a device"},

		// Administrators, the game's own rules and API clients are not held to that list.
		{"admin sets a field element", set("auto_winner", 0, "red", testAdmin), ""},
		{"api sets the auto tower", set("auto_tower", 3, "level3", api), ""},
		{"rule sets a field element", set("auto_winner", 0, "none", rule), ""},

		// Fouls and cards are the referees' call.
		{"referee calls a foul", from(foul, testReferee), ""},
		{"admin calls a foul", from(foul, testAdmin), ""},
		{"api calls a foul", from(foul, api), ""},
		{"scorer cannot call a foul", from(foul, testScorer), "cannot change fouls or cards"},
		{"device cannot call a foul", from(foul, testDevice), "cannot change fouls or cards"},
		{"rule cannot call a foul", from(foul, rule), "cannot change fouls or cards"},
		{"referee gives a card", from(card, testReferee), ""},
		{"scorer cannot give a card", from(card, testScorer), "cannot change fouls or cards"},
		{"referee disqualifies an alliance", from(disqualify, testReferee), ""},
		{"scorer cannot", from(disqualify, testScorer), "cannot change fouls or cards"},
	} {
		changesBefore := len(arena.Score.Changes)
		_, err := arena.ApplyScoreChange(testCase.change)
		if testCase.expectedError == "" {
			assert.Nil(t, err, testCase.name)
			assert.Equal(t, changesBefore+1, len(arena.Score.Changes), testCase.name)
		} else {
			if assert.NotNil(t, err, testCase.name) {
				assert.Contains(t, err.Error(), testCase.expectedError, testCase.name)
			}
			assert.Equal(t, changesBefore, len(arena.Score.Changes), testCase.name)
		}
	}
	assert.Equal(t, 3, len(arena.Score.State.Alliances["red"].Fouls))
	assert.Equal(t, map[int]string{2: "yellow"}, arena.Score.State.Alliances["red"].Cards)
	assert.True(t, arena.Score.State.Alliances["blue"].Disqualified)

	// An element also lists the phases it accepts changes in. Once its phase and the grace window after it are
	// over, panels can no longer change it until the match has ended; an administrator still can.
	runMatchTo(arena, 40)
	assert.Equal(t, "shift1", arena.CurrentPhase().ID)
	_, err := arena.ApplyScoreChange(set("auto_tower", 1, "level3", testScorer))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "auto_tower does not accept changes during the shift1 phase")
	}
	_, err = arena.ApplyScoreChange(set("auto_tower", 1, "level3", testReferee))
	assert.NotNil(t, err)
	assert.Equal(t, "level1", arena.Score.State.Alliances["red"].Robots[0]["auto_tower"].Option)
	change := apply(t, arena, set("auto_tower", 1, "level3", testAdmin))
	assert.Equal(t, "shift1", change.Phase)
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, "level3", arena.Score.State.Alliances["red"].Robots[0]["auto_tower"].Option)

	// A panel cannot get around that by naming the phase it would like; an unrestricted source may name one.
	sneaky := set("auto_tower", 1, "none", testScorer)
	sneaky.Bucket = "auto"
	_, err = arena.ApplyScoreChange(sneaky)
	assert.NotNil(t, err)
	correction := scoring.Change{
		Alliance: "red", Element: "fuel", Op: scoring.OpIncrement, Value: 4, Bucket: "auto", Source: testAdmin,
	}
	change = apply(t, arena, correction)
	assert.Equal(t, "shift1", change.Phase)
	assert.Equal(t, "auto", change.Bucket)
	assert.Equal(t, 6, arena.Score.State.Alliances["red"].Elements["fuel"].Counts["auto"])
	correction.Bucket = "halftime"
	_, err = arena.ApplyScoreChange(correction)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "\"halftime\" is not a phase of this game")
	}
}

func TestScoreChangeGraceWindow(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	fuel := func(alliance string) map[string]int {
		return arena.Score.State.Alliances[alliance].Elements["fuel"].Counts
	}
	setTower := func(element string, source scoring.Source) scoring.Change {
		return scoring.Change{
			Alliance: "red", Robot: 1, Element: element, Op: scoring.OpSet, Value: "level1", Source: source,
		}
	}

	// In the first phase there is nothing earlier to credit anything to.
	change := apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, "auto", change.Phase)
	assert.Equal(t, "", change.Bucket)

	// One second after auto ended: fuel that was in the air at the buzzer still counts for auto, and so does what a
	// scorer enters for an element that only auto accepts.
	runMatchTo(arena, 21)
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	change = apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, "pause", change.Phase)
	assert.Equal(t, "auto", change.Bucket)
	change = apply(t, arena, setTower("auto_tower", testScorer))
	assert.Equal(t, "pause", change.Phase)
	assert.Equal(t, "auto", change.Bucket)
	assert.Equal(t, "auto", arena.Score.State.Alliances["red"].Robots[0]["auto_tower"].Phase)

	// What a person enters for an element that the current phase accepts too belongs to the current phase.
	change = apply(t, arena, count("blue", "fuel", testScorer))
	assert.Equal(t, "pause", change.Phase)
	assert.Equal(t, "", change.Bucket)
	change = apply(t, arena, setTower("endgame_tower", testScorer))
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 2}, fuel("red"))
	assert.Equal(t, map[string]int{"pause": 1}, fuel("blue"))

	// Half a second into teleop the window of auto has closed, three and a half seconds after its end. The pause
	// has no window of its own: nothing was moving in it that could still arrive.
	runMatchTo(arena, 23.5)
	assert.Equal(t, "transition", arena.CurrentPhase().ID)
	change = apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, "transition", change.Phase)
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 2, "transition": 1}, fuel("red"))
	_, err := arena.ApplyScoreChange(setTower("auto_tower", testScorer))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "auto_tower does not accept changes during the transition phase")
	}

	// A second into the next phase, the window of the phase before it is open.
	runMatchTo(arena, 34)
	assert.Equal(t, "shift1", arena.CurrentPhase().ID)
	change = apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, "shift1", change.Phase)
	assert.Equal(t, "transition", change.Bucket)
	change = apply(t, arena, count("red", "fuel", testScorer))
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 2, "transition": 2, "shift1": 1}, fuel("red"))

	// Four seconds in, it has closed.
	runMatchTo(arena, 37)
	change = apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, "shift1", change.Phase)
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 2, "transition": 2, "shift1": 2}, fuel("red"))
}

func TestScoreChangeGraceWindowOutlastsAShortPause(t *testing.T) {
	arena := setupTestArena(t)
	dir := useScratchGames(t, arena)
	source := string(model.TestGame("ball-count").Source)
	writeGame(t, arena, dir, "ball-count", strings.Replace(source, "score_grace: 2", "score_grace: 3", 1))
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(t, 3.0, arena.Game.Match.ScoreGrace)
	startTestMatch(t, arena)

	// The pause lasts two seconds and the window three, so it is still open in the first second of teleop.
	runMatchTo(arena, 17.5)
	assert.Equal(t, "teleop", arena.CurrentPhase().ID)
	change := apply(t, arena, count("red", "balls", testDevice))
	assert.Equal(t, "auto", change.Bucket)
	runMatchTo(arena, 18.5)
	change = apply(t, arena, count("red", "balls", testDevice))
	assert.Equal(t, "", change.Bucket)
	assert.Equal(
		t, map[string]int{"auto": 1, "teleop": 1}, arena.Score.State.Alliances["red"].Elements["balls"].Counts,
	)
}

func TestScoreChangeAfterMatch(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	fuel := func() map[string]int {
		return arena.Score.State.Alliances["red"].Elements["fuel"].Counts
	}

	// A sensor that reports just after the final buzzer is credited to the last phase.
	change := apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, scoring.PhasePost, change.Phase)
	assert.Equal(t, "endgame", change.Bucket)
	assert.Equal(t, 163.0, change.MatchTime)
	assert.Equal(t, map[string]int{"endgame": 1}, fuel())

	// Once the grace window is over, devices are no longer listened to.
	arena.matchStopTime = arena.matchStopTime.Add(-4 * time.Second)
	_, err := arena.ApplyScoreChange(count("red", "fuel", testDevice))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "the match is over; input from Test sensor is no longer counted")
	}

	// People put the score right. For a counter they have to say which phase they mean.
	_, err = arena.ApplyScoreChange(count("red", "fuel", testScorer))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "say which phase this change is credited to")
	}
	correction := count("red", "fuel", testScorer)
	correction.Bucket = "shift2"
	change = apply(t, arena, correction)
	assert.Equal(t, scoring.PhasePost, change.Phase)
	assert.Equal(t, "shift2", change.Bucket)
	assert.Equal(t, map[string]int{"endgame": 1, "shift2": 1}, fuel())
	correction.Bucket = "overtime"
	_, err = arena.ApplyScoreChange(correction)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "\"overtime\" is not a phase of this game")
	}

	// Scorers and referees may now change any element, whoever may change it during the match and whenever.
	correction = count("red", "fuel", testReferee)
	correction.Bucket = "auto"
	apply(t, arena, correction)
	tower := scoring.Change{
		Alliance: "red", Robot: 3, Element: "auto_tower", Op: scoring.OpSet, Value: "level2", Bucket: "auto",
		Source: testScorer,
	}
	change = apply(t, arena, tower)
	assert.Equal(t, "auto", change.Bucket)
	tower.Element, tower.Bucket = "endgame_tower", ""
	change = apply(t, arena, tower)
	assert.Equal(t, scoring.PhasePost, change.Phase)
	assert.Equal(t, "", change.Bucket)
	apply(t, arena, scoring.Change{Element: "auto_winner", Op: scoring.OpSet, Value: "red", Source: testReferee})

	// Fouls stay with the referees.
	foul := scoring.Change{
		Alliance: "blue", Robot: 1, Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "major", Rule: "G420",
	}
	foul.Source = testScorer
	_, err = arena.ApplyScoreChange(foul)
	assert.NotNil(t, err)
	foul.Source = testReferee
	change = apply(t, arena, foul)
	assert.Equal(
		t,
		[]scoring.Foul{{ID: change.Seq, Type: "major", Rule: "G420", Robot: 1, Phase: scoring.PhasePost}},
		arena.Score.State.Alliances["blue"].Fouls,
	)

	// Red: one auto fuel, one in the endgame, one in shift 2 (red won auto), a level 2 endgame climb, an auto
	// climb, and a major foul by blue.
	assert.Equal(t, 1.0+1+1+20+15+15, allianceScore(arena, "red"))
}

func TestUndoScoreChange(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	startTestMatch(t, arena)
	events := arena.Bus.Subscribe([]string{"score.undone"}, 10)
	defer events.Close()
	redBalls := func() int {
		return arena.Score.State.Alliances["red"].Elements["balls"].Total()
	}
	for i := 0; i < 3; i++ {
		apply(t, arena, count("red", "balls", testScorer))
	}
	assert.Equal(t, 6.0, allianceScore(arena, "red"))

	// Undoing appends a change that reverts the one named, and rebuilds the state without it.
	runMatchTo(arena, 5)
	revert, err := arena.UndoScoreChange(2, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, 4, revert.Seq)
	assert.Equal(t, 2, revert.Reverts)
	assert.Equal(t, "auto", revert.Phase)
	assert.InDelta(t, 5, revert.MatchTime, 0.2)
	assert.Equal(t, "balls", revert.Element)
	assert.Equal(t, "red", revert.Alliance)
	assert.Equal(t, testScorer, revert.Source)
	assert.Equal(t, 4, len(arena.Score.Changes))
	assert.Equal(t, 2, redBalls())
	assert.Equal(t, 4.0, allianceScore(arena, "red"))
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, revert, received[0].Data)
	}

	// The same change cannot be undone twice, and a change that does not exist cannot be undone at all.
	_, err = arena.UndoScoreChange(2, testScorer)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "score change 2 has already been undone")
	}
	_, err = arena.UndoScoreChange(42, testScorer)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "there is no score change with sequence number 42")
	}
	assert.Equal(t, 4, len(arena.Score.Changes))
	assert.Empty(t, drainEvents(events))

	// Undoing the undo brings the change back.
	revert, err = arena.UndoScoreChange(4, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, 5, revert.Seq)
	assert.Equal(t, 4, revert.Reverts)
	assert.Equal(t, 3, redBalls())
	assert.Equal(t, 6.0, allianceScore(arena, "red"))

	// Undoing takes the same permission as making the change: a scorer cannot take back a referee's foul.
	foul := apply(
		t, arena,
		scoring.Change{Alliance: "blue", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "foul", Source: testReferee},
	)
	assert.Equal(t, 3.0+6, allianceScore(arena, "red"))
	_, err = arena.UndoScoreChange(foul.Seq, testScorer)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "cannot change fouls or cards")
	}
	_, err = arena.UndoScoreChange(foul.Seq, testDevice)
	assert.NotNil(t, err)
	assert.Equal(t, 1, len(arena.Score.State.Alliances["blue"].Fouls))
	_, err = arena.UndoScoreChange(foul.Seq, testReferee)
	assert.Nil(t, err)
	assert.Empty(t, arena.Score.State.Alliances["blue"].Fouls)
	assert.Equal(t, 6.0, allianceScore(arena, "red"))

	// It works the same after the match, where the undo itself is recorded as a post-match change.
	runMatchTo(arena, 137.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	revert, err = arena.UndoScoreChange(1, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, scoring.PhasePost, revert.Phase)
	assert.Equal(t, 137.0, revert.MatchTime)
	assert.Equal(t, 2, redBalls())

	// Before a match only an administrator can.
	assert.Nil(t, arena.ResetMatch())
	_, err = arena.ApplyScoreChange(
		scoring.Change{Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Bucket: "auto", Source: testAdmin},
	)
	assert.Nil(t, err)
	last := arena.Score.Changes[len(arena.Score.Changes)-1].Seq
	_, err = arena.UndoScoreChange(last, testScorer)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "the match has not started")
	}
	_, err = arena.UndoScoreChange(last, testAdmin)
	assert.Nil(t, err)
}

func TestTimersStopWhenTheMatchDoes(t *testing.T) {
	for _, aborted := range []bool{false, true} {
		arena := setupTestArena(t)
		dir := useScratchGames(t, arena)
		writeGame(t, arena, dir, "ball-count", ballCountWithTimer())
		selectGame(t, arena, "ball-count", nil)
		startTestMatch(t, arena)
		hold := func(alliance string) *scoring.Value {
			return arena.Score.State.Alliances[alliance].Elements["hold"]
		}

		// Red starts its timer twenty seconds in and never stops it; blue stops its own after ten seconds.
		runMatchTo(arena, 20)
		start := scoring.Change{Alliance: "red", Element: "hold", Op: scoring.OpTimerStart, Source: testScorer}
		apply(t, arena, start)
		start.Alliance = "blue"
		apply(t, arena, start)
		runMatchTo(arena, 30)
		apply(t, arena, scoring.Change{Alliance: "blue", Element: "hold", Op: scoring.OpTimerStop, Source: testScorer})
		assert.True(t, hold("red").Running)
		assert.False(t, hold("blue").Running)
		assert.InDelta(t, 10, hold("blue").Seconds, 0.3)
		changesBefore := len(arena.Score.Changes)

		expectedSeconds := 117.0
		if aborted {
			runMatchTo(arena, 50)
			assert.Nil(t, arena.AbortMatch())
			expectedSeconds = 30
		} else {
			runMatchTo(arena, 137.05)
		}
		assert.Equal(t, PostMatch, arena.MatchState)

		// The arena stopped the running timer at the moment the match stopped, as a change of its own.
		assert.False(t, hold("red").Running)
		assert.InDelta(t, expectedSeconds, hold("red").Seconds, 0.3)
		assert.InDelta(t, 10, hold("blue").Seconds, 0.3)
		if assert.Equal(t, changesBefore+1, len(arena.Score.Changes)) {
			stop := arena.Score.Changes[changesBefore]
			assert.Equal(t, scoring.OpTimerStop, stop.Op)
			assert.Equal(t, "red", stop.Alliance)
			assert.Equal(t, "hold", stop.Element)
			assert.Equal(t, scoring.SourceRule, stop.Source.Kind)
			assert.Equal(t, scoring.PhasePost, stop.Phase)
			assert.InDelta(t, expectedSeconds+20, stop.MatchTime, 0.3)
		}
		assert.InDelta(t, expectedSeconds, allianceScore(arena, "red"), 0.3)
	}
}

func TestLiveValuesFollowTheClock(t *testing.T) {
	arena := setupTestArena(t)
	hubActive := func() [2]any {
		return [2]any{arena.Score.Live.Alliances["red"]["hub_active"], arena.Score.Live.Alliances["blue"]["hub_active"]}
	}

	// Nothing is active before the match.
	arena.Update()
	assert.Equal(t, [2]any{false, false}, hubActive())

	// Both alliances are active through auto, the pause and the transition.
	startTestMatch(t, arena)
	assert.Equal(t, [2]any{true, true}, hubActive())
	apply(t, arena, count("red", "fuel", testDevice))
	runMatchTo(arena, 21)
	assert.Equal(t, [2]any{true, true}, hubActive())
	runMatchTo(arena, 30)
	assert.Equal(t, "red", arena.Score.State.Field["auto_winner"].Option)
	assert.Equal(t, [2]any{true, true}, hubActive())

	// Then they take turns, the alliance that lost auto first. Only the clock moves here: no score changes.
	changes := len(arena.Score.Changes)
	runMatchTo(arena, 33.05)
	assert.Equal(t, [2]any{false, true}, hubActive())
	runMatchTo(arena, 58.05)
	assert.Equal(t, [2]any{true, false}, hubActive())
	runMatchTo(arena, 83.05)
	assert.Equal(t, [2]any{false, true}, hubActive())
	runMatchTo(arena, 108.05)
	assert.Equal(t, [2]any{true, false}, hubActive())
	runMatchTo(arena, 133.05)
	assert.Equal(t, [2]any{true, true}, hubActive())
	assert.Equal(t, changes, len(arena.Score.Changes))

	// The live values reach browsers with the score.
	message := arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage)
	assert.Equal(t, map[string]any{"hub_active": true}, message.Alliances["red"].Live)

	// And they are what makes scoring depend on the moment: fuel only counts for the alliance whose turn it was.
	runMatchTo(arena, 163.05)
	assert.Equal(t, [2]any{false, false}, hubActive())
	for _, phase := range []string{"shift1", "shift2"} {
		for _, alliance := range []string{"red", "blue"} {
			apply(
				t, arena,
				scoring.Change{
					Alliance: alliance, Element: "fuel", Op: scoring.OpSet, Value: 10, Bucket: phase, Source: testScorer,
				},
			)
		}
	}
	assert.Equal(t, 11.0, allianceScore(arena, "red"))
	assert.Equal(t, 10.0, allianceScore(arena, "blue"))
}

func TestBuildMatchResult(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "rebuilt-style", map[string]float64{"energized_threshold": 2, "not_a_parameter": 7})
	for teamId := 101; teamId <= 106; teamId++ {
		arena.Database.CreateTeam(&model.Team{Id: teamId})
	}
	match := model.Match{
		Type: model.Qualification, ShortName: "Q7", Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105,
		Blue3: 106,
	}
	assert.Nil(t, arena.Database.CreateMatch(&match))
	assert.Nil(t, arena.LoadMatch(&match))
	assert.Nil(t, arena.Database.TruncateGameSnapshots())
	startTestMatch(t, arena)
	apply(t, arena, count("red", "fuel", testDevice))
	apply(t, arena, count("red", "fuel", testDevice))
	apply(t, arena, count("blue", "fuel", testDevice))
	runMatchTo(arena, 163.05)

	matchResult, err := arena.BuildMatchResult()
	assert.Nil(t, err)
	assert.Equal(t, match.Id, matchResult.MatchId)
	assert.Equal(t, 0, matchResult.PlayNumber)
	assert.Equal(t, model.Qualification, matchResult.MatchType)
	assert.Equal(t, "rebuilt-style", matchResult.GameId)
	assert.Equal(t, arena.Game.Hash, matchResult.GameHash)
	assert.Equal(t, map[string]float64{"energized_threshold": 2}, matchResult.Params)
	assert.Equal(t, map[string][]int{"red": {101, 102, 103}, "blue": {104, 105, 106}}, matchResult.Teams)
	assert.Equal(t, arena.Score.Changes, matchResult.Changes)
	assert.Equal(t, arena.Score.State, matchResult.State)

	// The game the match was played with is stored, so the result can be recomputed from what was saved.
	snapshot, err := arena.Database.GetGameSnapshot(arena.Game.Hash)
	assert.Nil(t, err)
	assert.NotNil(t, snapshot)
	matchResult.PlayNumber = 1
	assert.Nil(t, arena.Database.CreateMatchResult(matchResult))
	stored, err := arena.Database.GetMatchResultForMatch(match.Id)
	assert.Nil(t, err)
	result, _, err := stored.Compute(arena.Database, false)
	assert.Nil(t, err)
	assert.Equal(t, 2.0, result.Alliances["red"].Score)
	assert.Equal(t, 1.0, result.Alliances["blue"].Score)
	assert.Equal(t, "red", result.Winner)
	assert.Equal(t, 1.0, result.Alliances["red"].RP["energized"], "the event's parameter override applies")
	assert.Equal(t, 0.0, result.Alliances["blue"].RP["energized"])
	assert.Equal(t, arena.Score.Result.Alliances["red"].RPTotal, result.Alliances["red"].RPTotal)

	// The stored result is a copy: what happens on the field afterwards does not reach it.
	correction := count("blue", "fuel", testScorer)
	correction.Bucket = "auto"
	apply(t, arena, correction)
	assert.Equal(t, 1, matchResult.State.Alliances["blue"].Elements["fuel"].Total())
	assert.Equal(t, len(arena.Score.Changes)-1, len(matchResult.Changes))
}

func TestFoulsCommitted(t *testing.T) {
	arena := setupTestArena(t)
	assert.False(t, arena.RefereeScoreReady())
	assert.False(t, arena.FoulsCommitted("red"))

	assert.Nil(t, arena.SetFoulsCommitted("red", true))
	assert.True(t, arena.FoulsCommitted("red"))
	assert.False(t, arena.RefereeScoreReady())
	assert.Nil(t, arena.SetFoulsCommitted("blue", true))
	assert.True(t, arena.RefereeScoreReady())
	assert.NotNil(t, arena.SetFoulsCommitted("green", true))

	// It is part of the workflow of one match, not of the score: the next match starts unconfirmed.
	assert.Nil(t, arena.LoadTestMatch())
	assert.False(t, arena.RefereeScoreReady())
}

func TestPublishMatchCommitted(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"match.committed", "match.result_edited"}, 10)
	defer events.Close()
	matchResult := model.BuildTestMatchResult(arena.Database, 8, 1)
	result, _, err := matchResult.Compute(arena.Database, false)
	assert.Nil(t, err)

	arena.PublishMatchCommitted(&model.Match{Id: 8}, result, false)
	arena.PublishMatchCommitted(&model.Match{Id: 8}, result, true)
	received := drainEvents(events)
	if assert.Equal(t, []string{"match.committed", "match.result_edited"}, eventTypes(received)) {
		assert.Equal(
			t,
			map[string]any{"id": 8, "winner": "blue", "scores": map[string]float64{"red": 133, "blue": 289}},
			received[0].Data,
		)
		assert.Equal(t, received[0].Data, received[1].Data)
	}
}
