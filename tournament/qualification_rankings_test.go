// Copyright 2017 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package tournament

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"testing"
)

// teamRecord is what the rankings hold for one team. The tests list the records they expect as tables with one
// row per team and the columns in the order of these fields; the last three are the ranking stats of the
// rebuilt-style game.
type teamRecord struct {
	teamId            int
	previousRank      int
	played            int
	wins              int
	losses            int
	ties              int
	disqualifications int
	rp                float64
	matchPoints       float64
	autoFuelPoints    float64
	towerPoints       float64
}

func TestCalculateRankings(t *testing.T) {
	draws := seedRankingRandom(t, 45)
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	// Every match added to a team's record draws its random tiebreaker anew, so a team ends up with the value drawn
	// for its last match. One calculation of the schedule below draws fifteen values, in the order the matches were
	// played and the stations are listed, skipping surrogates: teams 1 to 6 for the first match, teams 1, 5, 2 and
	// 4 for the second, and teams 6, 5, 3, 2 and 1 for the third.
	lastDraw := map[int]int{1: 15, 2: 14, 3: 13, 4: 10, 5: 12, 6: 11}
	assertRandomValues := func(calculation int, rankings []model.Ranking) {
		t.Helper()
		for _, ranking := range rankings {
			expected := draws[(calculation-1)*15+lastDraw[ranking.TeamId]-1]
			assert.Equal(t, expected, ranking.Random, "team %d", ranking.TeamId)
		}
	}

	replayedMatchId := setupMatchResultsForRankings(t, database)
	updatedRankings, err := CalculateRankings(database, g, false)
	assert.Nil(t, err)
	rankings, err := database.GetAllRankings()
	assert.Nil(t, err)
	assert.Equal(t, updatedRankings, rankings)

	// Teams 4 and 6 both average 3 ranking points a match and team 4 has the higher average match points. Teams 1
	// and 2 are separated the same way: team 2 earned nothing from the match it was disqualified from.
	assertTeamRecords(
		t,
		[]teamRecord{
			{4, 0, 2, 1, 0, 1, 0, 6, 337, 53, 135},
			{6, 0, 2, 1, 0, 1, 0, 6, 204, 35, 90},
			{5, 0, 3, 1, 0, 2, 0, 7, 337, 53, 135},
			{1, 0, 3, 0, 1, 2, 0, 2, 266, 36, 90},
			{2, 0, 3, 0, 0, 2, 1, 2, 133, 18, 45},
			{3, 0, 2, 0, 1, 1, 0, 1, 133, 18, 45},
		},
		rankings,
	)
	assertRandomValues(1, rankings)

	// Test after changing a match result: the third match is replayed once more and this time red wins it, 289 to
	// 133. Each team's previous rank is its rank from the calculation before.
	matchResult3 := buildMirroredMatchResult(t, database, replayedMatchId, 3)
	assert.Nil(t, database.CreateMatchResult(matchResult3))
	updatedRankings, err = CalculateRankings(database, g, false)
	assert.Nil(t, err)
	rankings, err = database.GetAllRankings()
	assert.Nil(t, err)
	assert.Equal(t, updatedRankings, rankings)
	assertTeamRecords(
		t,
		[]teamRecord{
			{6, 2, 2, 2, 0, 0, 0, 10, 408, 70, 180},
			{5, 3, 3, 2, 0, 1, 0, 11, 541, 88, 225},
			{4, 1, 2, 1, 0, 1, 0, 6, 337, 53, 135},
			{1, 4, 3, 0, 2, 1, 0, 1, 399, 54, 135},
			{2, 5, 3, 0, 1, 1, 1, 1, 266, 36, 90},
			{3, 6, 2, 0, 2, 0, 0, 0, 266, 36, 90},
		},
		rankings,
	)
	assertRandomValues(2, rankings)

	// Test after another replay, back to the standard result in which blue wins 289 to 133, recalculated the way
	// an edit from match review is: every team keeps the previous rank it had instead of getting its last rank.
	matchResult3 = model.BuildTestMatchResult(database, replayedMatchId, 4)
	assert.Nil(t, database.CreateMatchResult(matchResult3))
	updatedRankings, err = CalculateRankings(database, g, true)
	assert.Nil(t, err)
	rankings, err = database.GetAllRankings()
	assert.Nil(t, err)
	assert.Equal(t, updatedRankings, rankings)
	assertTeamRecords(
		t,
		[]teamRecord{
			{4, 1, 2, 1, 0, 1, 0, 6, 337, 53, 135},
			{3, 6, 2, 1, 1, 0, 0, 5, 337, 53, 135},
			{6, 2, 2, 1, 1, 0, 0, 5, 337, 53, 135},
			{5, 3, 3, 1, 1, 1, 0, 6, 470, 71, 180},
			{1, 4, 3, 1, 1, 1, 0, 6, 470, 71, 180},
			{2, 5, 3, 1, 0, 1, 1, 6, 337, 53, 135},
		},
		rankings,
	)
	assertRandomValues(3, rankings)

	// Teams 3 and 6 are level on every average, and so are teams 5 and 1. Only the random value separates them.
	if assert.Equal(t, 6, len(rankings)) {
		assert.Equal(t, rankings[1].Sort[:4], rankings[2].Sort[:4])
		assert.Greater(t, rankings[1].Random, rankings[2].Random)
		assert.Equal(t, rankings[3].Sort[:4], rankings[4].Sort[:4])
		assert.Greater(t, rankings[3].Random, rankings[4].Random)
	}
}

func TestCalculateRankingsHandleCards(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	match := model.Match{
		Type:      model.Qualification,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match))

	// The standard result already has a yellow card at red position 1. Add a red card on the losing side, and a
	// red and a yellow card on the winning side, which earns 5 ranking points.
	matchResult := model.BuildTestMatchResult(database, match.Id, 1)
	giveCard(t, g, matchResult, "red", 2, "red")
	giveCard(t, g, matchResult, "blue", 1, "red")
	giveCard(t, g, matchResult, "blue", 3, "yellow")
	assert.Nil(t, database.CreateMatchResult(matchResult))

	_, err := CalculateRankings(database, g, false)
	assert.Nil(t, err)
	records := make(map[int]teamRecord)
	rankings, err := database.GetAllRankings()
	assert.Nil(t, err)
	for _, ranking := range rankings {
		records[ranking.TeamId] = teamRecordOf(ranking)
	}
	assert.Equal(t, 6, len(records))

	// A yellow card does not disqualify: teams 1 and 6 are ranked like their partners, teams 3 and 5, without a
	// card.
	lost := teamRecord{played: 1, losses: 1, matchPoints: 133, autoFuelPoints: 18, towerPoints: 45}
	won := teamRecord{played: 1, wins: 1, rp: 5, matchPoints: 204, autoFuelPoints: 35, towerPoints: 90}
	for teamId, expected := range map[int]teamRecord{1: lost, 3: lost, 5: won, 6: won} {
		expected.teamId = teamId
		assert.Equal(t, expected, records[teamId])
	}

	// A red card does: the match is a disqualification for that team alone, which takes no result, ranking points
	// or stats from it, whether its alliance lost (team 2) or won (team 4).
	for _, teamId := range []int{2, 4} {
		assert.Equal(t, teamRecord{teamId: teamId, played: 1, disqualifications: 1}, records[teamId])
		ranking, err := database.GetRankingForTeam(teamId)
		assert.Nil(t, err)
		assert.Empty(t, ranking.Stats)
	}
}

func TestCalculateRankingsEmptyStation(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	// A station without a team has nobody to rank: no ranking is made for team number zero.
	match := model.Match{
		Type:      model.Qualification,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match))
	assert.Nil(t, database.CreateMatchResult(model.BuildTestMatchResult(database, match.Id, 1)))

	rankings, err := CalculateRankings(database, g, false)
	assert.Nil(t, err)
	var teamIds []int
	for _, ranking := range rankings {
		teamIds = append(teamIds, ranking.TeamId)
	}
	assert.ElementsMatch(t, []int{1, 2, 4, 5, 6}, teamIds)
}

func TestCalculateRankingsSortsWithGivenGame(t *testing.T) {
	database := setupTestDb(t)
	setupMatchResultsForRankings(t, database)

	// Ranked for an event that plays the game the results were played with: one sort value for each of the five
	// keys of its ranking order, which are the averages per match of ranking points, match points, auto fuel
	// points and tower points, and the random value.
	rebuiltStyle := model.TestGame("rebuilt-style")
	rankings, err := CalculateRankings(database, rebuiltStyle, false)
	assert.Nil(t, err)
	if assert.Equal(t, 6, len(rankings)) {
		for i, ranking := range rankings {
			assert.Equal(t, i+1, ranking.Rank)
			assert.Equal(t, ranking.TeamId, ranking.TeamID)
			assert.Equal(t, len(rebuiltStyle.Ranking.Order), len(ranking.Sort))
		}
		assert.Equal(t, 4, rankings[0].TeamId)
		assert.Equal(t, []float64{3, 168.5, 26.5, 67.5, rankings[0].Random}, rankings[0].Sort)
	}

	// The same results ranked for an event whose active game is another one. Each result is still scored by the
	// game it was played with, but the teams are sorted by the ranking order of the game passed in, which has
	// three keys: the averages of ranking points and match points, and the random value.
	ballCount := model.TestGame("ball-count")
	rankings, err = CalculateRankings(database, ballCount, false)
	assert.Nil(t, err)
	if assert.Equal(t, 6, len(rankings)) {
		for i, ranking := range rankings {
			assert.Equal(t, i+1, ranking.Rank)
			assert.Equal(t, ranking.TeamId, ranking.TeamID)
			assert.Equal(t, len(ballCount.Ranking.Order), len(ranking.Sort))
		}
		assert.Equal(t, 4, rankings[0].TeamId)
		assert.Equal(t, []float64{3, 168.5, rankings[0].Random}, rankings[0].Sort)
		assert.Equal(t, 6.0, rankings[0].RP)
		assert.Equal(t, 135.0, rankings[0].Stats["tower_points"])
	}
}

func TestCalculateRankingsMissingMatchResult(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	// A match that has been played always has a result. If it does not, leaving it out would rank the teams on
	// fewer matches than they played without anybody noticing.
	match := model.Match{
		Type:      model.Qualification,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.RedWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match))
	message := fmt.Sprintf("found no match result for match %d", match.Id)

	rankings, err := CalculateRankings(database, g, false)
	assert.Nil(t, rankings)
	assert.EqualError(t, err, message)
	assert.EqualError(t, CalculateTeamCards(database), message)

	// The same goes for a result whose game definition was never stored: it cannot be scored.
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = match.Id
	matchResult.PlayNumber = 1
	matchResult.MatchType = model.Qualification
	assert.Nil(t, database.CreateMatchResult(matchResult))
	_, err = CalculateRankings(database, g, false)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), fmt.Sprintf("match %d: there is no stored game definition", match.Id))
	}
	err = CalculateTeamCards(database)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), fmt.Sprintf("match %d: there is no stored game definition", match.Id))
	}

	// Once the definition is there, the match is ranked.
	assert.Nil(t, database.SaveGameSnapshot(g))
	rankings, err = CalculateRankings(database, g, false)
	assert.Nil(t, err)
	assert.Equal(t, 6, len(rankings))
	assert.Nil(t, CalculateTeamCards(database))
}

func TestCalculateTeamCards(t *testing.T) {
	database := setupTestDb(t)
	g := model.TestGame("rebuilt-style")

	for teamId := 1; teamId <= 6; teamId++ {
		assert.Nil(t, database.CreateTeam(&model.Team{Id: teamId}))
	}
	// Team 7 plays no match, so whatever it carried before is cleared.
	assert.Nil(t, database.CreateTeam(&model.Team{Id: 7, YellowCard: true}))
	assertCarriedCards := func(expectedTeamIds ...int) {
		t.Helper()
		assert.Nil(t, CalculateTeamCards(database))
		teams, err := database.GetAllTeams()
		assert.Nil(t, err)
		var teamIds []int
		for _, team := range teams {
			if team.YellowCard {
				teamIds = append(teamIds, team.Id)
			}
		}
		assert.Equal(t, expectedTeamIds, teamIds)
	}
	assertCarriedCards()

	// A card is held by a robot position, and the team at that position is read from the match record. Any card
	// is carried into the next match: the yellow one the standard result has at red position 1 as much as a red
	// one. Team 8 is not at the event, so there is nothing to carry its card.
	match1 := model.Match{
		Type:      model.Qualification,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     8,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match1))
	matchResult1 := model.BuildTestMatchResult(database, match1.Id, 1)
	giveCard(t, g, matchResult1, "blue", 2, "yellow")
	giveCard(t, g, matchResult1, "blue", 3, "red")
	assert.Nil(t, database.CreateMatchResult(matchResult1))
	assertCarriedCards(1, 6)

	// Taking a card away again clears what the team carries.
	giveCard(t, g, matchResult1, "red", 1, "")
	assert.Nil(t, database.UpdateMatchResult(matchResult1))
	assertCarriedCards(6)

	// A replay supersedes the earlier result, cards included.
	replay := model.NewMatchResult(g)
	replay.MatchId = match1.Id
	replay.PlayNumber = 2
	replay.MatchType = model.Qualification
	giveCard(t, g, replay, "red", 3, "yellow")
	assert.Nil(t, database.CreateMatchResult(replay))
	assertCarriedCards(3)

	// Cards from different matches add up, and a surrogate carries its card like any other team.
	match2 := model.Match{
		Type:            model.Qualification,
		TypeOrder:       2,
		Red1:            6,
		Red2:            5,
		Red3:            4,
		Blue1:           3,
		Blue2:           2,
		Blue3:           1,
		Status:          model.BlueWonMatch,
		Red1IsSurrogate: true,
	}
	assert.Nil(t, database.CreateMatch(&match2))
	assert.Nil(t, database.CreateMatchResult(model.BuildTestMatchResult(database, match2.Id, 1)))
	assertCarriedCards(3, 6)

	// A match that has not been completed establishes nothing, even with a card saved in its result.
	match2.Status = model.MatchScheduled
	assert.Nil(t, database.UpdateMatch(&match2))
	assertCarriedCards(3)

	// Practice and playoff matches do not count either.
	for _, matchType := range []model.MatchType{model.Practice, model.Playoff} {
		match := model.Match{
			Type:      matchType,
			TypeOrder: 1,
			Red1:      1,
			Red2:      2,
			Red3:      3,
			Blue1:     4,
			Blue2:     5,
			Blue3:     6,
			Status:    model.BlueWonMatch,
		}
		assert.Nil(t, database.CreateMatch(&match))
		assert.Nil(t, database.CreateMatchResult(model.BuildTestMatchResult(database, match.Id, 1)))
	}
	assertCarriedCards(3)
}

// Sets up a schedule and results that touches on all possible variables. Returns the id of the third qualification
// match, which is the one the tests replay.
func setupMatchResultsForRankings(t *testing.T, database *model.Database) int {
	g := model.TestGame("rebuilt-style")

	// Red loses 133 to 289, and the team at red position 2 is shown a red card.
	match1 := model.Match{
		Type:      model.Qualification,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match1))
	matchResult1 := model.BuildTestMatchResult(database, match1.Id, 1)
	giveCard(t, g, matchResult1, "red", 2, "red")
	assert.Nil(t, database.CreateMatchResult(matchResult1))

	// A tie at 218, with a surrogate on each side.
	match2 := model.Match{
		Type:             model.Qualification,
		TypeOrder:        2,
		Red1:             1,
		Red2:             3,
		Red3:             5,
		Blue1:            2,
		Blue2:            4,
		Blue3:            6,
		Status:           model.TieMatch,
		Red2IsSurrogate:  true,
		Blue3IsSurrogate: true,
	}
	assert.Nil(t, database.CreateMatch(&match2))
	matchResult2 := buildTiedMatchResult(t, database, match2.Id, 1)
	assert.Nil(t, database.CreateMatchResult(matchResult2))

	// Played twice: the replay, in which nobody scores anything, is the result that counts.
	match3 := model.Match{
		Type:            model.Qualification,
		TypeOrder:       3,
		Red1:            6,
		Red2:            5,
		Red3:            4,
		Blue1:           3,
		Blue2:           2,
		Blue3:           1,
		Status:          model.TieMatch,
		Red3IsSurrogate: true,
	}
	assert.Nil(t, database.CreateMatch(&match3))
	matchResult3 := model.BuildTestMatchResult(database, match3.Id, 1)
	assert.Nil(t, database.CreateMatchResult(matchResult3))
	matchResult3 = model.NewMatchResult(g)
	matchResult3.MatchId = match3.Id
	matchResult3.PlayNumber = 2
	matchResult3.MatchType = model.Qualification
	assert.Nil(t, database.CreateMatchResult(matchResult3))

	// Practice and playoff matches are not ranked.
	match4 := model.Match{
		Type:      model.Practice,
		TypeOrder: 1,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match4))
	matchResult4 := model.BuildTestMatchResult(database, match4.Id, 1)
	assert.Nil(t, database.CreateMatchResult(matchResult4))

	match5 := model.Match{
		Type:      model.Playoff,
		TypeOrder: 8,
		Red1:      1,
		Red2:      2,
		Red3:      3,
		Blue1:     4,
		Blue2:     5,
		Blue3:     6,
		Status:    model.BlueWonMatch,
	}
	assert.Nil(t, database.CreateMatch(&match5))
	matchResult5 := model.BuildTestMatchResult(database, match5.Id, 1)
	assert.Nil(t, database.CreateMatchResult(matchResult5))

	// Neither is a match that has not been played, even if a result is stored for it.
	match6 := model.Match{
		Type:      model.Qualification,
		TypeOrder: 4,
		Red1:      7,
		Red2:      8,
		Red3:      9,
		Blue1:     10,
		Blue2:     11,
		Blue3:     12,
		Status:    model.MatchScheduled,
	}
	assert.Nil(t, database.CreateMatch(&match6))
	matchResult6 := model.BuildTestMatchResult(database, match6.Id, 1)
	assert.Nil(t, database.CreateMatchResult(matchResult6))

	return match3.Id
}

// seedRankingRandom makes the random ranking tiebreaker reproducible for the rest of the test, and returns the
// first count values that will be drawn.
func seedRankingRandom(t *testing.T, count int) []float64 {
	original := scoring.RandomFloat
	t.Cleanup(
		func() {
			scoring.RandomFloat = original
		},
	)
	randomizer := rand.New(rand.NewSource(1))
	scoring.RandomFloat = randomizer.Float64

	preview := rand.New(rand.NewSource(1))
	draws := make([]float64, count)
	for i := range draws {
		draws[i] = preview.Float64()
	}
	return draws
}

func teamRecordOf(ranking model.Ranking) teamRecord {
	return teamRecord{
		teamId:            ranking.TeamId,
		previousRank:      ranking.PreviousRank,
		played:            ranking.Played,
		wins:              ranking.Wins,
		losses:            ranking.Losses,
		ties:              ranking.Ties,
		disqualifications: ranking.Disqualifications,
		rp:                ranking.RP,
		matchPoints:       ranking.Stats["match_points"],
		autoFuelPoints:    ranking.Stats["auto_fuel_points"],
		towerPoints:       ranking.Stats["tower_points"],
	}
}

// assertTeamRecords checks the rankings against the expected records, best team first, and that each ranking is
// consistent in itself: ranks count up from one, and the datastore key is the team of the record it wraps.
func assertTeamRecords(t *testing.T, expected []teamRecord, rankings []model.Ranking) {
	t.Helper()
	if !assert.Equal(t, len(expected), len(rankings)) {
		return
	}
	for i, ranking := range rankings {
		assert.Equal(t, expected[i], teamRecordOf(ranking), "rank %d", i+1)
		assert.Equal(t, i+1, ranking.Rank)
		assert.Equal(t, ranking.TeamId, ranking.TeamID)
	}
}
