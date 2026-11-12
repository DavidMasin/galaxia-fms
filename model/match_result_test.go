// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package model

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

var testScorer = scoring.Source{Kind: scoring.SourceScorer, ID: "test"}

func TestGetNonexistentMatchResult(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	match, err := db.GetMatchResultForMatch(1114)
	assert.Nil(t, err)
	assert.Nil(t, match)
}

func TestMatchResultCrud(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	matchResult := BuildTestMatchResult(db, 254, 5)
	matchResult.Params["energized_threshold"] = 80
	matchResult.Teams["red"] = []int{1868, 25, 254}
	assert.Nil(t, db.CreateMatchResult(matchResult))
	matchResult2, err := db.GetMatchResultForMatch(254)
	assert.Nil(t, err)
	assert.Equal(t, matchResult, matchResult2)

	_, err = matchResult.Edit(
		TestGame("rebuilt-style"),
		scoring.Change{
			Alliance: "blue", Robot: 2, Element: "endgame_tower", Op: scoring.OpSet, Value: "level1", Source: testScorer,
		},
	)
	assert.Nil(t, err)
	assert.Nil(t, db.UpdateMatchResult(matchResult))
	matchResult2, err = db.GetMatchResultForMatch(254)
	assert.Nil(t, err)
	assert.Equal(t, matchResult, matchResult2)
	assert.Equal(t, "level1", matchResult2.State.Alliances["blue"].Robots[1]["endgame_tower"].Option)

	assert.Nil(t, db.DeleteMatchResult(matchResult.Id))
	matchResult2, err = db.GetMatchResultForMatch(254)
	assert.Nil(t, err)
	assert.Nil(t, matchResult2)
}

func TestTruncateMatchResults(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	matchResult := BuildTestMatchResult(db, 254, 1)
	assert.Nil(t, db.CreateMatchResult(matchResult))
	assert.Nil(t, db.TruncateMatchResults())
	matchResult2, err := db.GetMatchResultForMatch(254)
	assert.Nil(t, err)
	assert.Nil(t, matchResult2)
}

func TestGetMatchResultForMatch(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	matchResult := BuildTestMatchResult(db, 254, 2)
	assert.Nil(t, db.CreateMatchResult(matchResult))
	matchResult2 := BuildTestMatchResult(db, 254, 5)
	assert.Nil(t, db.CreateMatchResult(matchResult2))
	matchResult3 := BuildTestMatchResult(db, 254, 4)
	assert.Nil(t, db.CreateMatchResult(matchResult3))

	// Should return the match result with the highest play number (i.e. the most recent).
	matchResult4, err := db.GetMatchResultForMatch(254)
	assert.Nil(t, err)
	assert.Equal(t, matchResult2, matchResult4)
}

func TestNewMatchResult(t *testing.T) {
	setupTestDb(t)
	g := TestGame("ball-count")
	matchResult := NewMatchResult(g)
	assert.Equal(t, "ball-count", matchResult.GameId)
	assert.Equal(t, g.Hash, matchResult.GameHash)
	assert.Equal(t, map[string][]int{"red": {0, 0, 0}, "blue": {0, 0, 0}}, matchResult.Teams)
	assert.Empty(t, matchResult.Changes)
	assert.Equal(t, scoring.NewState(g), matchResult.State)
}

func TestMatchResultCompute(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	// The stored result is recomputed from raw state with the game it was played with.
	matchResult := BuildTestMatchResult(db, 1, 1)
	assert.Nil(t, db.CreateMatchResult(matchResult))
	matchResult, err := db.GetMatchResultForMatch(1)
	assert.Nil(t, err)
	result, g, err := matchResult.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, "rebuilt-style", g.ID)
	assert.Equal(t, 133.0, result.Alliances["red"].Score)
	assert.Equal(t, 289.0, result.Alliances["blue"].Score)
	assert.Equal(t, 85.0, result.Alliances["blue"].FoulPoints)
	assert.Equal(t, "blue", result.Winner)
	assert.Equal(t, 5.0, result.Alliances["blue"].RPTotal)
	assert.Equal(t, BlueWonMatch, MatchStatusFromResult(g, result))

	// The event's parameter overrides in force at the time are part of the result.
	matchResult.Params["energized_threshold"] = 80
	result, _, err = matchResult.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, 1.0, result.Alliances["red"].RP["energized"])

	// A result whose game was never stored cannot be computed, and says so.
	matchResult.GameHash = "0123"
	_, _, err = matchResult.Compute(db, false)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "no stored game definition")
	}
}

func TestMatchResultComputeUsesItsOwnSnapshot(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	g := TestGame("ball-count")
	assert.Nil(t, db.SaveGameSnapshot(g))
	matchResult := NewMatchResult(g)
	matchResult.MatchId = 1
	matchResult.PlayNumber = 1
	_, err := matchResult.Edit(
		g,
		scoring.Change{
			Alliance: "red", Element: "balls", Op: scoring.OpSet, Value: 5, Bucket: "teleop", Source: testScorer,
		},
	)
	assert.Nil(t, err)
	assert.Nil(t, db.CreateMatchResult(matchResult))

	// The same game edited later, so that a teleop ball is worth ten points instead of one.
	edited, diagnostics := gamedef.Parse(
		[]byte(strings.Replace(string(g.Source), "points: { auto: 2, teleop: 1 }", "points: { auto: 2, teleop: 10 }", 1)),
	)
	if !assert.NotNil(t, edited, "%v", diagnostics) {
		return
	}
	assert.NotEqual(t, g.Hash, edited.Hash)
	assert.Nil(t, db.SaveGameSnapshot(edited))
	matchResult2 := NewMatchResult(edited)
	matchResult2.MatchId = 2
	matchResult2.PlayNumber = 1
	_, err = matchResult2.Edit(
		edited,
		scoring.Change{
			Alliance: "red", Element: "balls", Op: scoring.OpSet, Value: 5, Bucket: "teleop", Source: testScorer,
		},
	)
	assert.Nil(t, err)
	assert.Nil(t, db.CreateMatchResult(matchResult2))

	// Each result is scored by the rules it was played under.
	stored, err := db.GetMatchResultForMatch(1)
	assert.Nil(t, err)
	result, storedGame, err := stored.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, g.Hash, storedGame.Hash)
	assert.Equal(t, 5.0, result.Alliances["red"].Score)
	stored, err = db.GetMatchResultForMatch(2)
	assert.Nil(t, err)
	result, storedGame, err = stored.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, edited.Hash, storedGame.Hash)
	assert.Equal(t, 50.0, result.Alliances["red"].Score)
}

func TestMatchResultComputeWithoutState(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	// The raw state is only a cache of the change list; a result without it is folded on the fly.
	matchResult := BuildTestMatchResult(db, 1, 1)
	matchResult.State = nil
	result, _, err := matchResult.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, 133.0, result.Alliances["red"].Score)
	assert.Equal(t, 289.0, result.Alliances["blue"].Score)
}

func TestMatchResultEditAndUndo(t *testing.T) {
	setupTestDb(t)
	g := TestGame("ball-count")
	matchResult := NewMatchResult(g)

	change, err := matchResult.Edit(
		g,
		scoring.Change{
			Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Value: 4, Bucket: "auto", Source: testScorer,
		},
	)
	assert.Nil(t, err)
	assert.Equal(t, 1, change.Seq)
	assert.Equal(t, scoring.PhasePost, change.Phase)
	assert.False(t, change.Time.IsZero())
	change, err = matchResult.Edit(
		g, scoring.Change{Alliance: "blue", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "foul"},
	)
	assert.Nil(t, err)
	assert.Equal(t, 2, change.Seq)
	assert.Equal(t, 4, matchResult.State.Alliances["red"].Elements["balls"].Counts["auto"])
	assert.Equal(t, []scoring.Foul{{ID: 2, Type: "foul", Phase: "post"}}, matchResult.State.Alliances["blue"].Fouls)

	// A counter is counted per phase, so an edit after the match has to say which phase it means.
	_, err = matchResult.Edit(
		g, scoring.Change{Alliance: "red", Element: "balls", Op: scoring.OpIncrement, Source: testScorer},
	)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "is not a phase of this game")
	}
	assert.Equal(t, 2, len(matchResult.Changes))

	// Undoing a change appends a record and rebuilds the state without the change.
	revert, err := matchResult.Undo(g, 1, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, 3, revert.Seq)
	assert.Equal(t, 1, revert.Reverts)
	assert.Equal(t, "balls", revert.Element)
	assert.Equal(t, 3, len(matchResult.Changes))
	assert.Equal(t, 0, matchResult.State.Alliances["red"].Elements["balls"].Total())
	assert.Equal(t, 1, len(matchResult.State.Alliances["blue"].Fouls))

	// Undoing the undo brings it back.
	_, err = matchResult.Undo(g, 3, testScorer)
	assert.Nil(t, err)
	assert.Equal(t, 4, matchResult.State.Alliances["red"].Elements["balls"].Total())

	_, err = matchResult.Undo(g, 99, testScorer)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "no score change with sequence number 99")
	}
}

func TestComputeResultPlayoffDisqualification(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	g := TestGame("rebuilt-style")
	matchResult := BuildTestMatchResult(db, 1, 1)
	matchResult.Teams["red"] = []int{101, 102, 103}
	matchResult.Teams["blue"] = []int{104, 105, 106}
	referee := scoring.Source{Kind: scoring.SourceReferee}
	_, err := matchResult.Edit(
		g,
		scoring.Change{Alliance: "blue", Robot: 3, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "red", Source: referee},
	)
	assert.Nil(t, err)
	assert.Equal(t, map[int]string{101: "yellow"}, matchResult.TeamCards("red"))
	assert.Equal(t, map[int]string{106: "red"}, matchResult.TeamCards("blue"))
	assert.Empty(t, DisqualifiedPositions(g, matchResult.State.Alliances["red"]))
	assert.Equal(t, []int{3}, DisqualifiedPositions(g, matchResult.State.Alliances["blue"]))

	// In a qualification match the card costs the team its match in the rankings, not the alliance its score.
	result, _, err := matchResult.Compute(db, false)
	assert.Nil(t, err)
	assert.Equal(t, "blue", result.Winner)
	assert.False(t, result.Alliances["blue"].Disqualified)

	// In a playoff match it disqualifies the whole alliance.
	matchResult.MatchType = Playoff
	result, _, err = matchResult.Compute(db, true)
	assert.Nil(t, err)
	assert.Equal(t, "red", result.Winner)
	assert.True(t, result.Alliances["blue"].Disqualified)
	assert.Equal(t, 0.0, result.Alliances["blue"].Score)
	assert.Equal(t, RedWonMatch, MatchStatusFromResult(g, result))
	assert.False(t, matchResult.State.Alliances["blue"].Disqualified, "the stored state must not be changed")

	// Taking the card away again undoes it.
	_, err = matchResult.Edit(
		g,
		scoring.Change{Alliance: "blue", Robot: 3, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "", Source: referee},
	)
	assert.Nil(t, err)
	result, _, err = matchResult.Compute(db, true)
	assert.Nil(t, err)
	assert.Equal(t, "blue", result.Winner)
}

func TestMatchStatusFromResult(t *testing.T) {
	setupTestDb(t)
	g := TestGame("ball-count")
	compute := func(red, blue int, playoff bool) *scoring.Result {
		matchResult := NewMatchResult(g)
		for alliance, balls := range map[string]int{"red": red, "blue": blue} {
			_, err := matchResult.Edit(
				g,
				scoring.Change{
					Alliance: alliance, Element: "balls", Op: scoring.OpSet, Value: balls, Bucket: "teleop",
					Source: testScorer,
				},
			)
			assert.Nil(t, err)
		}
		result, err := ComputeResult(g, matchResult.State, Qualification, scoring.Options{Playoff: playoff})
		assert.Nil(t, err)
		return result
	}
	assert.Equal(t, RedWonMatch, MatchStatusFromResult(g, compute(3, 2, false)))
	assert.Equal(t, BlueWonMatch, MatchStatusFromResult(g, compute(2, 3, false)))
	assert.Equal(t, TieMatch, MatchStatusFromResult(g, compute(3, 3, false)))
	assert.Equal(t, TieMatch, MatchStatusFromResult(g, compute(3, 3, true)))
}

func TestGameSnapshots(t *testing.T) {
	db := setupTestDb(t)
	defer db.Close()

	snapshot, err := db.GetGameSnapshot("abc")
	assert.Nil(t, err)
	assert.Nil(t, snapshot)

	// Saving the same game twice stores it once.
	ballCount := TestGame("ball-count")
	assert.Nil(t, db.SaveGameSnapshot(ballCount))
	assert.Nil(t, db.SaveGameSnapshot(ballCount))
	assert.Nil(t, db.SaveGameSnapshot(TestGame("rebuilt-style")))
	snapshots, err := db.GetAllGameSnapshots()
	assert.Nil(t, err)
	assert.Equal(t, 2, len(snapshots))

	snapshot, err = db.GetGameSnapshot(ballCount.Hash)
	assert.Nil(t, err)
	if assert.NotNil(t, snapshot) {
		assert.Equal(t, "ball-count", snapshot.GameId)
		assert.Equal(t, ballCount.Hash, snapshot.Hash)
		assert.Equal(t, string(ballCount.Source), snapshot.Yaml)
	}

	// The parsed game is the same definition, and is parsed only once.
	g, err := db.GetSnapshotGame(ballCount.Hash)
	assert.Nil(t, err)
	assert.Equal(t, ballCount.Hash, g.Hash)
	g2, err := db.GetSnapshotGame(ballCount.Hash)
	assert.Nil(t, err)
	assert.Same(t, g, g2)

	assert.Nil(t, db.TruncateGameSnapshots())
	_, err = db.GetSnapshotGame(ballCount.Hash)
	assert.NotNil(t, err)
}
