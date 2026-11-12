package tournament

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestResultSides(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")
	match := model.Match{
		Id:                  7,
		Type:                model.Playoff,
		PlayoffRedAlliance:  3,
		PlayoffBlueAlliance: 6,
		Red1:                1,
		Red2:                2,
		Red3:                3,
		Blue1:               4,
		Blue2:               5,
		Blue3:               6,
		Blue2IsSurrogate:    true,
	}

	// The standard result has a yellow card at red position 1.
	matchResult := model.BuildTestMatchResult(database, match.Id, 1)
	giveCard(t, g, matchResult, "blue", 3, "red")
	sides, err := resultSides(&match, matchResult, g)
	assert.Nil(t, err)
	red, blue := sides[0], sides[1]

	assert.Equal(t, "red", red.allianceId)
	assert.Equal(t, 3, red.playoffAlliance)
	assert.Equal(t, [3]int{1, 2, 3}, red.teamIds)
	assert.Equal(t, [3]bool{false, false, false}, red.isSurrogate)
	assert.Same(t, matchResult.State.Alliances["red"], red.state)
	assert.Equal(t, []int{1}, red.cardedPositions(g))

	assert.Equal(t, "blue", blue.allianceId)
	assert.Equal(t, 6, blue.playoffAlliance)
	assert.Equal(t, [3]int{4, 5, 6}, blue.teamIds)
	assert.Equal(t, [3]bool{false, true, false}, blue.isSurrogate)
	assert.Same(t, matchResult.State.Alliances["blue"], blue.state)
	assert.Equal(t, []int{3}, blue.cardedPositions(g))

	assert.Equal(t, 4, blue.teamAt(1))
	assert.Equal(t, 6, blue.teamAt(3))
	assert.Equal(t, 0, blue.teamAt(0))
	assert.Equal(t, 0, blue.teamAt(4))

	// The raw state is only a cache of the change list. A result stored without it says the same, and is not
	// changed by being read.
	matchResult.State = nil
	sides, err = resultSides(&match, matchResult, g)
	assert.Nil(t, err)
	assert.Equal(t, []int{1}, sides[0].cardedPositions(g))
	assert.Equal(t, []int{3}, sides[1].cardedPositions(g))
	assert.Nil(t, matchResult.State)
}

func TestResultSidesNeedTwoAlliances(t *testing.T) {
	database := setupTestDb(t)

	// A match record has a red and a blue side. A result of a game with three alliances does not fit one, and
	// guessing which two of them to rank would be worse than saying so.
	threeAlliances := model.TestGame("orbital-rush")
	assert.Equal(t, 3, len(threeAlliances.Alliances.List))
	assert.Nil(t, database.SaveGameSnapshot(threeAlliances))
	for _, matchType := range []model.MatchType{model.Qualification, model.Playoff} {
		match := model.Match{Type: matchType, TypeOrder: 1, Red1: 1, Red2: 2, Blue1: 3, Blue2: 4}
		match.Status = model.TieMatch
		assert.Nil(t, database.CreateMatch(&match))
		matchResult := model.NewMatchResult(threeAlliances)
		matchResult.MatchId = match.Id
		matchResult.PlayNumber = 1
		matchResult.MatchType = matchType
		assert.Nil(t, database.CreateMatchResult(matchResult))
	}

	message := `was played with game "orbital-rush", which has 3 alliances; a match record holds exactly two`
	rankings, err := CalculateRankings(database, model.TestGame("rebuilt-style"), false)
	assert.Nil(t, rankings)
	assert.ErrorContains(t, err, message)
	assert.ErrorContains(t, CalculateTeamCards(database), message)
	assert.ErrorContains(t, CalculateAllianceCards(database), message)
}
