// Tests of what a game with scoring places, a latch at the end of a phase and rules about "every robot that played"
// needs from a running match. The last of them plays the reefscape example game.

package field

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRemovalReachesAnEarlierPhase(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	redBalls := func() map[string]int {
		return arena.Score.State.Alliances["red"].Elements["balls"].Counts
	}
	remove := func(amount any, source scoring.Source) scoring.Change {
		return scoring.Change{Alliance: "red", Element: "balls", Op: scoring.OpDecrement, Value: amount, Source: source}
	}

	startTestMatch(t, arena)
	runMatchTo(arena, 5)
	apply(t, arena, count("red", "balls", testScorer))
	apply(t, arena, count("red", "balls", testScorer))
	runMatchTo(arena, 40)
	apply(t, arena, count("red", "balls", testScorer))
	assert.Equal(t, map[string]int{"auto": 2, "teleop": 1}, redBalls())

	// What is taken away comes off the running phase while it holds something.
	change := apply(t, arena, remove(nil, testScorer))
	assert.Equal(t, "teleop", change.Phase)
	assert.Equal(t, "", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 2}, redBalls())

	// After that it was scored earlier, and comes off the phase that holds it instead of being refused.
	change = apply(t, arena, remove(nil, testDevice))
	assert.Equal(t, "teleop", change.Phase)
	assert.Equal(t, "auto", change.Bucket)
	assert.Equal(t, map[string]int{"auto": 1}, redBalls())
	assert.Equal(t, 2.0, allianceScore(arena, "red"))

	// Undoing the removal puts it back where it was taken from.
	_, err := arena.UndoScoreChange(change.Seq, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, map[string]int{"auto": 2}, redBalls())

	// A removal no single phase can cover is still refused, and so is one from an empty counter.
	apply(t, arena, count("red", "balls", testScorer))
	_, err = arena.ApplyScoreChange(remove(3, testScorer))
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "has only 1 in phase teleop")
	}
	change = apply(t, arena, remove(2, testScorer))
	assert.Equal(t, "auto", change.Bucket)
	apply(t, arena, remove(1, testScorer))
	assert.Empty(t, redBalls())
	_, err = arena.ApplyScoreChange(remove(nil, testScorer))
	assert.NotNil(t, err)

	// A source that names the phase is taken at its word.
	apply(t, arena, count("red", "balls", testScorer))
	_, err = arena.ApplyScoreChange(scoring.Change{
		Alliance: "red", Element: "balls", Op: scoring.OpDecrement, Bucket: "auto", Source: testAdmin,
	})
	assert.NotNil(t, err)
	assert.Equal(t, map[string]int{"teleop": 1}, redBalls())
}

// A rule that speaks of "every robot that played" has to know which robots did. The reefscape example game has
// one: its autonomous ranking point needs every robot that was not bypassed to leave, and one coral scored in auto.
func TestBypassedTeamsAreRecorded(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "reefscape", nil)
	for _, teamId := range []int{254, 1114} {
		assert.Nil(t, arena.Database.CreateTeam(&model.Team{Id: teamId}))
	}
	assert.Nil(t, arena.assignTeam(254, "R2"))
	assert.Nil(t, arena.assignTeam(1114, "B3"))
	events := arena.Bus.Subscribe([]string{"score"}, 100)
	defer events.Close()
	autoRp := func(allianceId string) any {
		result, _, _ := arena.ScoreSnapshot()
		return result.Alliances[allianceId].Values["auto_rp"]
	}
	bypass := func(allianceId string, robot int, value bool, source scoring.Source) scoring.Change {
		return scoring.Change{
			Alliance: allianceId, Robot: robot, Element: scoring.TargetBypassed, Op: scoring.OpSet, Value: value,
			Source: source,
		}
	}

	// Team 254 is bypassed and team 1114 plays. The four stations without a team are bypassed too, as a match
	// cannot otherwise start.
	bypassAllStations(arena)
	arena.AllianceStations["B3"].Bypass = false
	arena.AllianceStations["B3"].DsConn = &DriverStationConnection{TeamId: 1114, RobotLinked: true}
	assert.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)

	// The bypassed team is written into the score, by the arena and on the record. The stations with no team
	// are not: scoring already knows that nothing played there.
	assert.Equal(t, map[int]bool{2: true}, arena.Score.State.Alliances["red"].Bypassed)
	assert.Empty(t, arena.Score.State.Alliances["blue"].Bypassed)
	if assert.Equal(t, 1, len(arena.Score.Changes)) {
		change := arena.Score.Changes[0]
		assert.Equal(t, scoring.TargetBypassed, change.Element)
		assert.Equal(t, "red", change.Alliance)
		assert.Equal(t, 2, change.Robot)
		assert.Equal(t, true, change.Value)
		assert.Equal(t, scoring.SourceRule, change.Source.Kind)
		received := drainEvents(events)
		if assert.Equal(t, 1, len(received)) {
			assert.Equal(t, "score.changed", received[0].Type)
			assert.Equal(t, change, received[0].Data)
		}
	}
	// Browsers were sent the score in the same pass, once.
	assert.False(t, arena.scoreNotifyPending)

	// Each alliance scores a coral in auto, and no robot leaves.
	runMatchTo(arena, 5)
	for _, allianceId := range []string{"red", "blue"} {
		apply(t, arena, scoring.Change{
			Alliance: allianceId, Element: "l2_a", Op: scoring.OpSet, Value: true, Source: testScorer,
		})
	}
	runMatchTo(arena, 20)
	// Red had no robot that played, so none had to leave. Blue's one robot did not.
	assert.Equal(t, true, autoRp("red"))
	assert.Equal(t, false, autoRp("blue"))
	apply(t, arena, scoring.Change{
		Alliance: "blue", Robot: 3, Element: "leave", Op: scoring.OpSet, Value: true, Source: testScorer,
	})
	assert.Equal(t, true, autoRp("blue"))

	// Who played is not for a panel or a device to say, but match review and the API can put it right.
	for _, source := range []scoring.Source{testScorer, testReferee, testDevice} {
		_, err := arena.ApplyScoreChange(bypass("red", 2, false, source))
		assert.NotNil(t, err, source.Kind)
	}
	apply(t, arena, bypass("red", 2, false, testAdmin))
	assert.Empty(t, arena.Score.State.Alliances["red"].Bypassed)
	assert.Equal(t, false, autoRp("red"))
	apply(t, arena, bypass("blue", 3, true, scoring.Source{Kind: scoring.SourceAPI, ID: "integration"}))
	apply(t, arena, scoring.Change{
		Alliance: "blue", Robot: 3, Element: "leave", Op: scoring.OpSet, Value: false, Source: testScorer,
	})
	assert.Equal(t, true, autoRp("blue"))

	// The record is part of the stored result.
	runMatchTo(arena, 200)
	assert.Equal(t, PostMatch, arena.MatchState)
	matchResult, err := arena.BuildMatchResult()
	assert.Nil(t, err)
	assert.Equal(t, map[int]bool{3: true}, matchResult.State.Alliances["blue"].Bypassed)
	assert.Empty(t, matchResult.State.Alliances["red"].Bypassed)
}

// Plays the reefscape example game through the moments its latch depends on.
func TestReefscapeLatchInAMatch(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "reefscape", nil)
	red := func() *scoring.AllianceState {
		return arena.Score.State.Alliances["red"]
	}
	points := func(id string) float64 {
		result, _, _ := arena.ScoreSnapshot()
		return result.Alliances["red"].Points[id]
	}
	set := func(element string, value bool) scoring.Change {
		return scoring.Change{Alliance: "red", Element: element, Op: scoring.OpSet, Value: value, Source: testScorer}
	}
	trough := func(op string) scoring.Change {
		return scoring.Change{Alliance: "red", Element: "trough", Op: op, Source: testScorer}
	}

	// Autonomous: a coral on L4 branch A, one on L2 branch C that falls off again, and two in the trough.
	startTestMatch(t, arena)
	runMatchTo(arena, 6)
	apply(t, arena, set("l4_a", true))
	apply(t, arena, set("l2_c", true))
	apply(t, arena, set("l2_c", false))
	apply(t, arena, trough(scoring.OpIncrement))
	apply(t, arena, trough(scoring.OpIncrement))
	// Nothing is latched yet, so for now everything on the reef is worth its teleop value: 5 + 2 x 2.
	assert.Equal(t, 0.0, points("auto_coral_points"))
	assert.Equal(t, 9.0, points("teleop_coral_points"))
	// The latch itself cannot be touched by a scorer before teleop.
	_, err := arena.ApplyScoreChange(set("auto_l4_a", true))
	assert.NotNil(t, err)

	// One more coral lands on L3 branch B during the three seconds after autonomous. It is still assessed.
	runMatchTo(arena, 16.5)
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	apply(t, arena, set("l3_b", true))
	assert.False(t, red().Elements["auto_l4_a"].Bool)

	// Teleop begins: the latch is taken, by the game's rule and on the record. Of the 74 values the game copies
	// at that moment only the three that hold something are changes; a place that was empty and stays empty in
	// the latch is not one.
	recorded := len(arena.Score.Changes)
	runMatchTo(arena, 18.1)
	assert.Equal(t, "teleop", arena.CurrentPhase().ID)
	assert.Equal(t, recorded+3, len(arena.Score.Changes))
	assert.True(t, red().Elements["auto_l4_a"].Bool)
	assert.True(t, red().Elements["auto_l3_b"].Bool)
	assert.False(t, red().Elements["auto_l2_c"].Bool)
	assert.Equal(t, 2.0, red().Elements["auto_trough"].Number)
	latch := arena.Score.Changes[len(arena.Score.Changes)-1]
	assert.Equal(t, "auto_trough", latch.Element)
	assert.Equal(t, scoring.SourceRule, latch.Source.Kind)
	// 7 for L4, 6 for L3 and 2 x 3 for the trough.
	assert.Equal(t, 19.0, points("auto_coral_points"))
	assert.Equal(t, 0.0, points("teleop_coral_points"))
	result, _, _ := arena.ScoreSnapshot()
	assert.Equal(t, 4.0, result.Alliances["red"].Values["auto_coral"])

	// Teleop coral earns the teleop value: L4 branch B is 5 and a third coral in the trough is 2.
	runMatchTo(arena, 40)
	apply(t, arena, set("l4_b", true))
	apply(t, arena, trough(scoring.OpIncrement))
	assert.Equal(t, 19.0, points("auto_coral_points"))
	assert.Equal(t, 7.0, points("teleop_coral_points"))

	// The coral on L4 branch A is knocked off: its 7 points go, and it still counts as scored in auto.
	apply(t, arena, set("l4_a", false))
	assert.Equal(t, 12.0, points("auto_coral_points"))
	result, _, _ = arena.ScoreSnapshot()
	assert.Equal(t, 4.0, result.Alliances["red"].Values["auto_coral"])
	// Scored on that branch again, it is worth 7 again, not 5.
	apply(t, arena, set("l4_a", true))
	assert.Equal(t, 19.0, points("auto_coral_points"))
	assert.Equal(t, 7.0, points("teleop_coral_points"))

	// Two coral leave the trough. The first is the teleop coral. The second was scored in auto, which the count
	// has to be taken from, and it costs 3.
	apply(t, arena, trough(scoring.OpDecrement))
	assert.Equal(t, 19.0, points("auto_coral_points"))
	assert.Equal(t, 5.0, points("teleop_coral_points"))
	change := apply(t, arena, trough(scoring.OpDecrement))
	assert.Equal(t, "auto", change.Bucket)
	assert.Equal(t, 16.0, points("auto_coral_points"))
	// One comes back in teleop and is counted as the auto coral that left.
	apply(t, arena, trough(scoring.OpIncrement))
	assert.Equal(t, 19.0, points("auto_coral_points"))
	assert.Equal(t, 5.0, points("teleop_coral_points"))

	// A scorer who was late can put the latch right during teleop.
	apply(t, arena, set("auto_l4_b", true))
	assert.Equal(t, 26.0, points("auto_coral_points"))
	assert.Equal(t, 0.0, points("teleop_coral_points"))

	// The lights on the barge follow the processors, and the last 20 seconds are flagged.
	live := func() (any, any, any) {
		_, values, _ := arena.ScoreSnapshot()
		return values.Alliances["red"]["barge_lights"], values.Alliances["blue"]["barge_lights"],
			values.Field["last_seconds"]
	}
	processor := func(allianceId string) scoring.Change {
		return count(allianceId, "processor", testDevice)
	}
	apply(t, arena, processor("red"))
	arena.Update()
	redLights, blueLights, lastSeconds := live()
	assert.EqualValues(t, 1, redLights)
	assert.EqualValues(t, 0, blueLights)
	assert.Equal(t, false, lastSeconds)
	apply(t, arena, processor("red"))
	apply(t, arena, processor("red"))
	apply(t, arena, processor("blue"))
	apply(t, arena, processor("blue"))
	runMatchTo(arena, 134)
	redLights, blueLights, lastSeconds = live()
	assert.EqualValues(t, 3, redLights)
	assert.EqualValues(t, 3, blueLights)
	assert.Equal(t, true, lastSeconds)

	// An algae that passes the processor's sensor within three seconds of the end still counts; the panels then
	// finish the barge.
	runMatchTo(arena, 153.5)
	assert.Equal(t, PostMatch, arena.MatchState)
	apply(t, arena, processor("blue"))
	apply(t, arena, scoring.Change{
		Alliance: "red", Robot: 1, Element: "barge", Op: scoring.OpSet, Value: "deep", Source: testScorer,
	})
	assert.Equal(t, 26.0+3*6+12, allianceScore(arena, "red"))
	assert.Equal(t, 3.0*6, allianceScore(arena, "blue"))
}
