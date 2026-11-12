package scoring

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"math"
	"math/rand/v2"
	"sort"
)

// TeamRanking is one team's accumulated record across the matches it has played.
type TeamRanking struct {
	TeamID            int                `json:"team_id"`
	Rank              int                `json:"rank"`
	PreviousRank      int                `json:"previous_rank"`
	RP                float64            `json:"rp"`
	Played            int                `json:"played"`
	Wins              int                `json:"wins"`
	Losses            int                `json:"losses"`
	Ties              int                `json:"ties"`
	Disqualifications int                `json:"disqualifications"`
	Stats             map[string]float64 `json:"stats"`
	Sort              []float64          `json:"sort"` // One value per ranking order key, computed by SortRankings.
	Random            float64            `json:"random"`
}

// RandomFloat supplies the random tiebreaker value. Tests replace it.
var RandomFloat = rand.Float64

// AddMatch adds one played match to a team's record. disqualified is true when the team itself was disqualified
// from the match (for example by a red card), in which case it earns nothing from it.
func (r *TeamRanking) AddMatch(alliance *AllianceResult, disqualified bool) {
	r.Played++
	r.Random = RandomFloat()
	if disqualified || alliance.Disqualified {
		r.Disqualifications++
		return
	}
	switch alliance.Result {
	case ResultWin:
		r.Wins++
	case ResultLoss:
		r.Losses++
	case ResultTie:
		r.Ties++
	}
	r.RP += alliance.RPTotal
	if r.Stats == nil {
		r.Stats = make(map[string]float64)
	}
	for id, value := range alliance.Stats {
		r.Stats[id] += value
	}
}

// SortRankings computes each team's values for the game's ranking order, sorts the teams, and assigns ranks. The
// slice is sorted in place.
func SortRankings(g *gamedef.Game, rankings []TeamRanking) error {
	for i := range rankings {
		ranking := &rankings[i]
		ranking.Sort = make([]float64, len(g.Ranking.Order))
		env := map[string]any{
			"rp": ranking.RP, "played": ranking.Played, "wins": ranking.Wins, "losses": ranking.Losses,
			"ties": ranking.Ties, "disqualifications": ranking.Disqualifications,
		}
		for _, stat := range g.Ranking.Stats {
			env[stat.ID] = ranking.Stats[stat.ID]
		}
		for j, key := range g.Ranking.Order {
			if key.Random {
				ranking.Sort[j] = ranking.Random
				continue
			}
			if ranking.Played == 0 {
				continue // Averages are undefined before the first match; rank such teams last and equal.
			}
			value, err := key.Expr.Prog.RunNumber(env)
			if err != nil {
				return exprError("ranking order "+key.Name, key.Expr, err)
			}
			if key.Decimals > 0 || key.Expr != nil {
				scale := math.Pow(10, float64(max(key.Decimals, 6)))
				value = math.Round(value*scale) / scale
			}
			ranking.Sort[j] = value
		}
	}

	sort.SliceStable(rankings, func(a, b int) bool {
		for j, key := range g.Ranking.Order {
			left, right := rankings[a].Sort[j], rankings[b].Sort[j]
			if left == right {
				continue
			}
			if key.Dir == "asc" {
				return left < right
			}
			return left > right
		}
		return rankings[a].TeamID < rankings[b].TeamID
	})
	for i := range rankings {
		rankings[i].Rank = i + 1
	}
	return nil
}
