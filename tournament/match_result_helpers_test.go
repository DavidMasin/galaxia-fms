// Builders of match results for the tests in this package. They all use the rebuilt-style example game.

package tournament

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"testing"
)

// The standard test result, model.BuildTestMatchResult, has a weak alliance on the red side and a strong one on
// the blue side. The weak alliance scores 133 points of its own, gives away 85 in fouls and holds a yellow card
// at robot position 1; the strong one scores 204 points and commits no fouls. So red loses 133 to 289 and blue
// earns 5 ranking points. The builders below seat the same two alliances differently.

// buildMirroredMatchResult returns the standard test result with the sides swapped: red wins 289 to 133.
func buildMirroredMatchResult(t *testing.T, database *model.Database, matchId, playNumber int) *model.MatchResult {
	return composeMatchResult(t, database, matchId, playNumber, "blue", "red", "red")
}

// buildTiedMatchResult returns a result in which both sides play like the weak alliance of the standard test
// result and nobody won autonomous: a tie at 218, of which 85 are foul points, worth 1 ranking point each.
func buildTiedMatchResult(t *testing.T, database *model.Database, matchId, playNumber int) *model.MatchResult {
	return composeMatchResult(t, database, matchId, playNumber, "red", "red", "none")
}

// composeMatchResult builds a result from the alliances of the standard test result. redPlaysLike and
// bluePlaysLike name the standard alliance whose scoring, fouls and card that side repeats. autoWinner says who
// won autonomous, which decides the shifts in which an alliance's fuel counts: the points quoted above are those
// of the weak alliance when it did not win autonomous and of the strong alliance when it did.
func composeMatchResult(
	t *testing.T, database *model.Database, matchId, playNumber int, redPlaysLike, bluePlaysLike, autoWinner string,
) *model.MatchResult {
	g := model.TestGame("rebuilt-style")
	standard := model.BuildTestMatchResult(database, matchId, playNumber)
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = matchId
	matchResult.PlayNumber = playNumber
	matchResult.MatchType = standard.MatchType

	playsLike := map[string]string{"red": redPlaysLike, "blue": bluePlaysLike}
	for _, change := range standard.Changes {
		if change.Element == "auto_winner" {
			change.Value = autoWinner
			_, err := matchResult.Edit(g, change)
			assert.Nil(t, err)
			continue
		}
		for _, allianceId := range []string{"red", "blue"} {
			if playsLike[allianceId] == change.Alliance {
				repeated := change
				repeated.Alliance = allianceId
				_, err := matchResult.Edit(g, repeated)
				assert.Nil(t, err)
			}
		}
	}
	return matchResult
}

// giveCard gives the robot position of an alliance a card, or takes its card away if card is empty.
func giveCard(
	t *testing.T, g *gamedef.Game, matchResult *model.MatchResult, allianceId string, position int, card string,
) {
	_, err := matchResult.Edit(
		g,
		scoring.Change{
			Alliance: allianceId, Robot: position, Element: scoring.TargetCard, Op: scoring.OpSet, Value: card,
			Source: scoring.Source{Kind: scoring.SourceReferee},
		},
	)
	assert.Nil(t, err)
}

func TestMatchResultBuilders(t *testing.T) {
	database := setupTestDb(t)

	result, _, err := model.BuildTestMatchResult(database, 1, 1).Compute(database, false)
	assert.Nil(t, err)
	assert.Equal(t, 133.0, result.Alliances["red"].Score)
	assert.Equal(t, 289.0, result.Alliances["blue"].Score)
	assert.Equal(t, 0.0, result.Alliances["red"].RPTotal)
	assert.Equal(t, 5.0, result.Alliances["blue"].RPTotal)
	weakStats := map[string]float64{"match_points": 133, "auto_fuel_points": 18, "tower_points": 45}
	strongStats := map[string]float64{"match_points": 204, "auto_fuel_points": 35, "tower_points": 90}
	assert.Equal(t, weakStats, result.Alliances["red"].Stats)
	assert.Equal(t, strongStats, result.Alliances["blue"].Stats)

	mirrored := buildMirroredMatchResult(t, database, 1, 1)
	result, _, err = mirrored.Compute(database, false)
	assert.Nil(t, err)
	assert.Equal(t, "red", result.Winner)
	assert.Equal(t, 289.0, result.Alliances["red"].Score)
	assert.Equal(t, 133.0, result.Alliances["blue"].Score)
	assert.Equal(t, 5.0, result.Alliances["red"].RPTotal)
	assert.Equal(t, 0.0, result.Alliances["blue"].RPTotal)
	assert.Equal(t, strongStats, result.Alliances["red"].Stats)
	assert.Equal(t, weakStats, result.Alliances["blue"].Stats)
	assert.Empty(t, mirrored.State.Alliances["red"].Cards)
	assert.Equal(t, map[int]string{1: "yellow"}, mirrored.State.Alliances["blue"].Cards)

	result, _, err = buildTiedMatchResult(t, database, 1, 1).Compute(database, false)
	assert.Nil(t, err)
	assert.Equal(t, "", result.Winner)
	for _, allianceId := range []string{"red", "blue"} {
		assert.Equal(t, 218.0, result.Alliances[allianceId].Score)
		assert.Equal(t, 85.0, result.Alliances[allianceId].FoulPoints)
		assert.Equal(t, 1.0, result.Alliances[allianceId].RPTotal)
		assert.Equal(t, weakStats, result.Alliances[allianceId].Stats)
	}
}
