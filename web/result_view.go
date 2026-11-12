// Lays out a computed match result for the pages that are rendered on the server: match review, the result editor
// and the announcer's partials. Everything a page shows about a game (names, breakdown rows, ranking points)
// comes from the game definition the result was played with.

package web

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"math"
	"strconv"
)

// The two layout slots of a page and the two sides of a match record. A game's first alliance takes the first and
// its second the second, whatever the game calls and colours them.
var layoutSlots = []string{"red", "blue"}

// namedValue is one labelled number of a result, formatted for display.
type namedValue struct {
	Id    string  `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Text  string  `json:"text"`
}

// resultAllianceView is what a computed result holds for one alliance.
type resultAllianceView struct {
	Id            string       `json:"id"`
	Name          string       `json:"name"`
	Slot          string       `json:"slot"`
	Color         string       `json:"color"`
	Teams         []int        `json:"teams"`
	Score         string       `json:"score"`
	Breakdown     []namedValue `json:"breakdown"`
	RankingPoints []namedValue `json:"rankingPoints"`
	RpTotal       string       `json:"rpTotal"`
	Result        string       `json:"result"`
	Disqualified  bool         `json:"disqualified"`
}

// resultView is a computed result with its alliances in the order of the game.
type resultView struct {
	Winner     string                `json:"winner"` // Alliance id, or empty for a tie.
	WinnerName string                `json:"winnerName"`
	WinnerSlot string                `json:"winnerSlot"`
	Tiebreak   string                `json:"tiebreak"`
	Alliances  []*resultAllianceView `json:"alliances"`
	Errors     []string              `json:"errors"`
}

// formatNumber formats a computed value for display: whole numbers without decimals, others to at most two places.
func formatNumber(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
}

// slotForIndex returns the layout slot of the alliance at the given index of its game, or the empty string for an
// alliance beyond the two a page has room for.
func slotForIndex(index int) string {
	if index < 0 || index >= len(layoutSlots) {
		return ""
	}
	return layoutSlots[index]
}

// matchSideTeams returns the teams of a match record by side: the first side first.
func matchSideTeams(match *model.Match) [][]int {
	return [][]int{{match.Red1, match.Red2, match.Red3}, {match.Blue1, match.Blue2, match.Blue3}}
}

// teamsForGame returns the teams of a match record keyed by the alliance ids of the given game, by robot position.
func teamsForGame(g *gamedef.Game, match *model.Match) map[string][]int {
	sides := matchSideTeams(match)
	teams := make(map[string][]int, len(g.Alliances.List))
	for index, alliance := range g.Alliances.List {
		teams[alliance.ID] = make([]int, g.Alliances.Size)
		if index < len(sides) {
			copy(teams[alliance.ID], sides[index])
		}
	}
	return teams
}

// resultTeams returns the teams of a result by alliance id and robot position: the ones recorded with the result,
// which are the ones that played, or for a result that has none the ones in the match record.
func resultTeams(g *gamedef.Game, match *model.Match, matchResult *model.MatchResult) map[string][]int {
	for _, teams := range matchResult.Teams {
		for _, teamId := range teams {
			if teamId != 0 {
				return matchResult.Teams
			}
		}
	}
	return teamsForGame(g, match)
}

// buildResultView lays out a computed result. teams gives the team numbers by alliance id and robot position.
func buildResultView(g *gamedef.Game, result *scoring.Result, teams map[string][]int) *resultView {
	view := &resultView{
		Winner: result.Winner, Tiebreak: result.Tiebreak, Alliances: []*resultAllianceView{}, Errors: []string{},
	}
	for index, alliance := range g.Alliances.List {
		allianceResult := result.Alliances[alliance.ID]
		allianceView := &resultAllianceView{
			Id: alliance.ID, Name: alliance.Name, Slot: slotForIndex(index), Color: alliance.Color,
			Teams: append([]int{}, teams[alliance.ID]...), Score: formatNumber(allianceResult.Score),
			Breakdown: []namedValue{}, RankingPoints: []namedValue{}, RpTotal: formatNumber(allianceResult.RPTotal),
			Result: allianceResult.Result, Disqualified: allianceResult.Disqualified,
		}
		for _, row := range allianceResult.Breakdown {
			allianceView.Breakdown = append(
				allianceView.Breakdown,
				namedValue{Id: row.ID, Name: row.Name, Value: row.Value, Text: formatNumber(row.Value)},
			)
		}
		for _, rankingPoint := range g.Ranking.Points {
			value := allianceResult.RP[rankingPoint.ID]
			allianceView.RankingPoints = append(
				allianceView.RankingPoints,
				namedValue{Id: rankingPoint.ID, Name: rankingPoint.Name, Value: value, Text: formatNumber(value)},
			)
		}
		if alliance.ID == result.Winner {
			view.WinnerName, view.WinnerSlot = alliance.Name, allianceView.Slot
		}
		view.Alliances = append(view.Alliances, allianceView)
	}
	return view
}

// rankingColumns returns the columns of the active game's ranking order that reports print: every sort key except
// the random tiebreaker, which means nothing to a reader. indexes gives each column's place in Ranking.Sort.
func rankingColumns(g *gamedef.Game) (columns []*gamedef.OrderKey, indexes []int) {
	for index, key := range g.Ranking.Order {
		if key.Random {
			continue
		}
		columns = append(columns, key)
		indexes = append(indexes, index)
	}
	return
}

// rankingSortText formats one of a team's sort values for the column it belongs to. Rankings stored under a
// different game may hold fewer values than the active game has columns; those cells are left empty.
func rankingSortText(ranking *model.Ranking, key *gamedef.OrderKey, index int) string {
	if index >= len(ranking.Sort) {
		return ""
	}
	return strconv.FormatFloat(ranking.Sort[index], 'f', key.Decimals, 64)
}
