// Joins the two sides of a match record with what the stored result of the match holds for each of them.

package tournament

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
)

// matchSide is one side of a played match. A match record always has a red and a blue side of three stations,
// while a stored result names its alliances with the ids of the game it was played with; this is the two joined.
type matchSide struct {
	allianceId      string                 // Id of the alliance in the game the result was played with.
	playoffAlliance int                    // Number of the playoff alliance, or zero outside the playoffs.
	teamIds         [3]int                 // Teams at robot positions 1 to 3; zero where there is none.
	isSurrogate     [3]bool                // Whether the team at each position plays as a surrogate.
	state           *scoring.AllianceState // What the result holds for the alliance: raw values, fouls and cards.
}

// latestMatchResult returns the result of the latest play of a completed match. A completed match always has one,
// so finding none is an error rather than an empty score.
func latestMatchResult(database *model.Database, matchId int) (*model.MatchResult, error) {
	matchResult, err := database.GetMatchResultForMatch(matchId)
	if err != nil {
		return nil, err
	}
	if matchResult == nil {
		return nil, fmt.Errorf("found no match result for match %d", matchId)
	}
	return matchResult, nil
}

// resultSides returns the red and the blue side of a match, in that order. g is the game the result was played
// with: its first alliance played the red side of the match record and its second the blue side.
func resultSides(match *model.Match, matchResult *model.MatchResult, g *gamedef.Game) ([2]matchSide, error) {
	sides := [2]matchSide{
		{
			playoffAlliance: match.PlayoffRedAlliance,
			teamIds:         [3]int{match.Red1, match.Red2, match.Red3},
			isSurrogate:     [3]bool{match.Red1IsSurrogate, match.Red2IsSurrogate, match.Red3IsSurrogate},
		},
		{
			playoffAlliance: match.PlayoffBlueAlliance,
			teamIds:         [3]int{match.Blue1, match.Blue2, match.Blue3},
			isSurrogate:     [3]bool{match.Blue1IsSurrogate, match.Blue2IsSurrogate, match.Blue3IsSurrogate},
		},
	}
	if len(g.Alliances.List) != len(sides) {
		return sides, fmt.Errorf(
			"match %d was played with game %q, which has %d alliances; a match record holds exactly two",
			match.Id, g.ID, len(g.Alliances.List),
		)
	}

	state := matchResult.State
	if state == nil {
		// The raw state is only a cache of the change list, so a result stored without it is folded here.
		state, _ = scoring.Fold(g, matchResult.Changes)
	}
	for i := range sides {
		allianceId := g.Alliances.List[i].ID
		sides[i].allianceId = allianceId
		sides[i].state = state.Alliances[allianceId]
		if sides[i].state == nil {
			return sides, fmt.Errorf("the result of match %d holds nothing for alliance %q", match.Id, allianceId)
		}
	}
	return sides, nil
}

// teamAt returns the team at a robot position (1..n) of this side, or zero if the match record has none there.
func (side *matchSide) teamAt(position int) int {
	if position < 1 || position > len(side.teamIds) {
		return 0
	}
	return side.teamIds[position-1]
}

// cardedPositions returns the robot positions (1..n) of this side that hold any card the game defines.
func (side *matchSide) cardedPositions(g *gamedef.Game) []int {
	var positions []int
	for position := 1; position <= len(side.state.Robots); position++ {
		if g.Card(side.state.Cards[position]) != nil {
			positions = append(positions, position)
		}
	}
	return positions
}
