// Copyright 2026 Team 254. All Rights Reserved.

package tournament

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCalculateAllianceCards(t *testing.T) {
	db := setupTestDb(t)
	g := model.TestGame("rebuilt-style")
	require.NoError(t, db.SaveGameSnapshot(g))
	CreateTestAlliances(db, 2)
	require.NoError(t, db.CreateTeam(&model.Team{Id: 101, YellowCard: true}))
	require.NoError(t, db.CreateTeam(&model.Team{Id: 104}))
	match := &model.Match{
		Type: model.Playoff, PlayoffRedAlliance: 1, PlayoffBlueAlliance: 2, Red1: 102, Red2: 101, Red3: 103, Blue1: 202,
		Blue2: 201, Blue3: 203, Status: model.RedWonMatch,
	}
	require.NoError(t, db.CreateMatch(match))

	// A completed match always has a result.
	assert.EqualError(t, CalculateAllianceCards(db), fmt.Sprintf("found no match result for match %d", match.Id))

	result := model.NewMatchResult(g)
	result.MatchId, result.PlayNumber, result.MatchType = match.Id, 1, model.Playoff
	require.NoError(t, db.CreateMatchResult(result))
	// A card at any robot position is carried by the whole alliance, whichever card of the game it is. The
	// cards that teams carry from the qualification matches are left alone.
	for _, tc := range []struct {
		name   string
		card   string
		yellow bool
	}{{"yellow", "yellow", true}, {"red", "red", true}, {"none", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			giveCard(t, g, result, "red", 2, tc.card)
			require.NoError(t, db.UpdateMatchResult(result))
			require.NoError(t, CalculateAllianceCards(db))
			red, _ := db.GetAllianceById(1)
			blue, _ := db.GetAllianceById(2)
			assert.Equal(t, tc.yellow, red.YellowCard)
			assert.False(t, blue.YellowCard)
			team, _ := db.GetTeamById(101)
			assert.True(t, team.YellowCard)
			team, _ = db.GetTeamById(104)
			assert.False(t, team.YellowCard)
		})
	}
	// It does not matter which robot of the alliance holds the card.
	giveCard(t, g, result, "red", 3, "yellow")
	require.NoError(t, db.UpdateMatchResult(result))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ := db.GetAllianceById(1)
	assert.True(t, alliance.YellowCard)
	giveCard(t, g, result, "red", 3, "")
	require.NoError(t, db.UpdateMatchResult(result))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.False(t, alliance.YellowCard)

	// The same alliance plays blue in another match; corrections must respect its other cards.
	match2 := &model.Match{
		Type: model.Playoff, PlayoffRedAlliance: 2, PlayoffBlueAlliance: 1, Red1: 202, Red2: 201, Red3: 203, Blue1: 102,
		Blue2: 101, Blue3: 103, Status: model.BlueWonMatch,
	}
	require.NoError(t, db.CreateMatch(match2))
	result2 := model.NewMatchResult(g)
	result2.MatchId, result2.PlayNumber, result2.MatchType = match2.Id, 1, model.Playoff
	giveCard(t, g, result2, "blue", 1, "yellow")
	require.NoError(t, db.CreateMatchResult(result2))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.True(t, alliance.YellowCard)
	alliance, _ = db.GetAllianceById(2)
	assert.False(t, alliance.YellowCard)
	// Removing a card from one match must preserve a yellow established in another.
	giveCard(t, g, result, "red", 2, "red")
	require.NoError(t, db.UpdateMatchResult(result))
	require.NoError(t, CalculateAllianceCards(db))
	giveCard(t, g, result, "red", 2, "")
	require.NoError(t, db.UpdateMatchResult(result))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.True(t, alliance.YellowCard)
	// A replay replaces the effective result, rather than accumulating superseded cards.
	replay := model.NewMatchResult(g)
	replay.MatchId, replay.PlayNumber, replay.MatchType = match2.Id, 2, model.Playoff
	require.NoError(t, db.CreateMatchResult(replay))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.False(t, alliance.YellowCard)
	// An uncompleted match with a saved card must not establish carryover.
	match2.Status = model.MatchScheduled
	require.NoError(t, db.UpdateMatch(match2))
	giveCard(t, g, replay, "blue", 1, "red")
	require.NoError(t, db.UpdateMatchResult(replay))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.False(t, alliance.YellowCard)
	// Cards from qualification matches belong to teams and never reach an alliance.
	qualification := &model.Match{
		Type: model.Qualification, PlayoffRedAlliance: 1, PlayoffBlueAlliance: 2, Status: model.BlueWonMatch,
	}
	require.NoError(t, db.CreateMatch(qualification))
	require.NoError(t, db.CreateMatchResult(model.BuildTestMatchResult(db, qualification.Id, 1)))
	require.NoError(t, CalculateAllianceCards(db))
	alliance, _ = db.GetAllianceById(1)
	assert.False(t, alliance.YellowCard)
}
