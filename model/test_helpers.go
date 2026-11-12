// Copyright 2017 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Helper methods for use in tests in this package and others.

package model

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func SetupTestDb(t *testing.T) *Database {
	BaseDir = ".."
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	database, err := OpenDatabase(dbPath)
	assert.Nil(t, err)
	t.Cleanup(
		func() {
			database.Close()
		},
	)
	return database
}

// TestGame loads one of the example games that ship in the repository's games directory. BaseDir must already
// point at the repository root, which SetupTestDb takes care of.
func TestGame(id string) *gamedef.Game {
	g, diagnostics := gamedef.LoadFile(filepath.Join(BaseDir, "games", id, gamedef.GameFile))
	if g == nil {
		panic(fmt.Sprintf("example game %q does not load: %v", id, diagnostics))
	}
	return g
}

// BuildTestMatchResult returns a qualification result of the rebuilt-style example game and stores the game
// snapshot it needs. The red alliance loses 133 to 289; red committed two minor and five major fouls and its
// first robot holds a yellow card. It is the worked example "Upstream score summary" in that game's tests.yaml.
func BuildTestMatchResult(database *Database, matchId int, playNumber int) *MatchResult {
	g := TestGame("rebuilt-style")
	if err := database.SaveGameSnapshot(g); err != nil {
		panic(err)
	}
	matchResult := NewMatchResult(g)
	matchResult.MatchId = matchId
	matchResult.PlayNumber = playNumber
	matchResult.MatchType = Qualification

	scorer := scoring.Source{Kind: scoring.SourceScorer, ID: "test"}
	referee := scoring.Source{Kind: scoring.SourceReferee, ID: "test"}
	edit := func(change scoring.Change) {
		if _, err := matchResult.Edit(g, change); err != nil {
			panic(err)
		}
	}
	edit(scoring.Change{Element: "auto_winner", Op: scoring.OpSet, Value: "blue", Source: scorer})
	// Values are float64 so that a result read back from the datastore, where JSON turns every number into one,
	// compares equal to the one that was written.
	fuel := map[string][]float64{"red": {18, 10, 20, 30, 25, 40, 15}, "blue": {35, 12, 40, 30, 50, 28, 9}}
	autoTowers := map[string][]string{"red": {"none", "level2", "none"}, "blue": {"level1", "none", "level3"}}
	endgameTowers := map[string][]string{"red": {"level1", "level2", "none"}, "blue": {"level3", "level2", "level1"}}
	phases := []string{"auto", "transition", "shift1", "shift2", "shift3", "shift4", "endgame"}
	for _, alliance := range []string{"red", "blue"} {
		for i, phase := range phases {
			edit(scoring.Change{
				Alliance: alliance, Element: "fuel", Op: scoring.OpSet, Value: fuel[alliance][i], Bucket: phase,
				Source: scorer,
			})
		}
		for position := 1; position <= 3; position++ {
			edit(scoring.Change{
				Alliance: alliance, Robot: position, Element: "auto_tower", Op: scoring.OpSet,
				Value: autoTowers[alliance][position-1], Bucket: "auto", Source: scorer,
			})
			edit(scoring.Change{
				Alliance: alliance, Robot: position, Element: "endgame_tower", Op: scoring.OpSet,
				Value: endgameTowers[alliance][position-1], Bucket: "endgame", Source: scorer,
			})
		}
	}
	for _, foulType := range []string{"major", "minor", "minor", "major", "major", "major", "major"} {
		edit(scoring.Change{
			Alliance: "red", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: foulType, Bucket: "shift1",
			Source: referee,
		})
	}
	edit(scoring.Change{
		Alliance: "red", Robot: 1, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "yellow", Source: referee,
	})
	return matchResult
}

func BuildTestAlliances(database *Database) {
	database.CreateAlliance(&Alliance{Id: 2, TeamIds: []int{1718, 2451, 1619}, Lineup: [3]int{2451, 1718, 1619}})
	database.CreateAlliance(&Alliance{Id: 1, TeamIds: []int{254, 469, 2848, 74, 3175}, Lineup: [3]int{469, 254, 2848}})
}
