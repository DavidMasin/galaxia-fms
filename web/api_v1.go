// Version 1 of the read API: the current match, matches and results, rankings, teams, alliances and the clock.
// Shapes are described in docs/openapi.yaml, which a test keeps in step with the routes.

package web

import (
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// When the server started, for the health endpoint.
var serverStartTime = time.Now()

// apiTimeHandler returns the server's clock, so devices and displays can measure how far theirs is off.
func (web *Web) apiTimeHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	writeJson(w, http.StatusOK, map[string]any{"t": now.UnixMilli(), "iso": now.Format(time.RFC3339Nano)})
}

// apiHealthHandler reports whether the server is up and what it is doing.
func (web *Web) apiHealthHandler(w http.ResponseWriter, r *http.Request) {
	arena := web.arena
	game := ""
	if arena.Game != nil {
		game = arena.Game.ID
	}
	writeJson(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"uptimeSec":  int(time.Since(serverStartTime).Seconds()),
		"game":       game,
		"matchState": matchStateName(arena.MatchState),
		"lastEvent":  arena.Bus.LastSeq(),
	})
}

// matchStateName gives the API name of a match state.
func matchStateName(state field.MatchState) string {
	switch state {
	case field.PreMatch:
		return "pre_match"
	case field.StartMatch:
		return "starting"
	case field.InMatch:
		return "in_match"
	case field.PostMatch:
		return "post_match"
	case field.TimeoutActive:
		return "timeout"
	case field.PostTimeout:
		return "post_timeout"
	}
	return "unknown"
}

// apiGameHandler returns the active game's description.
func (web *Web) apiGameHandler(w http.ResponseWriter, r *http.Request) {
	if web.arena.Game == nil {
		writeJsonError(w, http.StatusServiceUnavailable, "no game is active")
		return
	}
	writeJson(w, http.StatusOK, web.arena.Game.View())
}

// apiMatchSummary is a match as the API lists it.
type apiMatchSummary struct {
	Id        int              `json:"id"`
	Type      string           `json:"type"`
	TypeOrder int              `json:"typeOrder"`
	ShortName string           `json:"shortName"`
	LongName  string           `json:"longName"`
	Time      time.Time        `json:"time"`
	StartedAt *time.Time       `json:"startedAt"`
	Status    string           `json:"status"`
	Teams     map[string][]int `json:"teams"` // Alliance id -> team number by robot position, zero where empty.
	Result    *apiResult       `json:"result"`
}

// apiResult is the outcome of a played match.
type apiResult struct {
	GameId     string             `json:"gameId"`
	PlayNumber int                `json:"playNumber"`
	Winner     string             `json:"winner"`
	Tiebreak   string             `json:"tiebreak"`
	Scores     map[string]float64 `json:"scores"`
}

func matchStatusName(status model.MatchStatus) string {
	switch status {
	case model.MatchScheduled:
		return "scheduled"
	case model.MatchHidden:
		return "hidden"
	case model.RedWonMatch, model.BlueWonMatch, model.TieMatch:
		return "complete"
	}
	return "unknown"
}

// summarizeMatch builds the API view of a match. The alliance ids come from the game the match was played with when
// it has a result, and from the active game otherwise.
func (web *Web) summarizeMatch(match *model.Match) (*apiMatchSummary, *scoring.Result, *model.MatchResult, error) {
	summary := &apiMatchSummary{
		Id: match.Id, Type: match.Type.String(), TypeOrder: match.TypeOrder, ShortName: match.ShortName,
		LongName: match.LongName, Time: match.Time.UTC(), Status: matchStatusName(match.Status),
		Teams: map[string][]int{},
	}
	if !match.StartedAt.IsZero() {
		started := match.StartedAt.UTC()
		summary.StartedAt = &started
	}
	allianceIds := []string{web.arena.AllianceId(0), web.arena.AllianceId(1)}
	matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
	if err != nil {
		return nil, nil, nil, err
	}
	var computed *scoring.Result
	if matchResult != nil {
		result, game, err := matchResult.Compute(web.arena.Database, match.UseTiebreakCriteria)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("match %d: %w", match.Id, err)
		}
		computed = result
		if len(game.Alliances.List) >= 2 {
			allianceIds = []string{game.Alliances.List[0].ID, game.Alliances.List[1].ID}
		}
		summary.Result = &apiResult{
			GameId: matchResult.GameId, PlayNumber: matchResult.PlayNumber, Winner: result.Winner,
			Tiebreak: result.Tiebreak, Scores: map[string]float64{},
		}
		for id, alliance := range result.Alliances {
			summary.Result.Scores[id] = alliance.Score
		}
	}
	sides := [][]int{{match.Red1, match.Red2, match.Red3}, {match.Blue1, match.Blue2, match.Blue3}}
	for i, id := range allianceIds {
		if id != "" {
			summary.Teams[id] = sides[i]
		}
	}
	return summary, computed, matchResult, nil
}

// apiMatchesHandler lists the matches of one type with their results.
func (web *Web) apiMatchesHandler(w http.ResponseWriter, r *http.Request) {
	typeName := r.URL.Query().Get("type")
	if typeName == "" {
		typeName = "qualification"
	}
	matchType, err := model.MatchTypeFromString(typeName)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	matches, err := web.arena.Database.GetMatchesByType(matchType, false)
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summaries := make([]*apiMatchSummary, 0, len(matches))
	for i := range matches {
		summary, _, _, err := web.summarizeMatch(&matches[i])
		if err != nil {
			writeJsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries = append(summaries, summary)
	}
	writeJson(w, http.StatusOK, map[string]any{"matches": summaries})
}

// apiMatchHandler returns one match with its full result: the computed score and the audit log of every change.
func (web *Web) apiMatchHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, "the match id must be a number")
		return
	}
	match, err := web.arena.Database.GetMatchById(id)
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if match == nil {
		writeJsonError(w, http.StatusNotFound, fmt.Sprintf("there is no match %d", id))
		return
	}
	summary, computed, matchResult, err := web.summarizeMatch(match)
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := map[string]any{"match": summary, "computed": computed, "changes": []scoring.Change{}}
	if matchResult != nil {
		response["changes"] = matchResult.Changes
		response["state"] = matchResult.State
	}
	writeJson(w, http.StatusOK, response)
}

// apiCurrentMatchHandler returns the match on the field now: what it is, the clock, and the live score.
func (web *Web) apiCurrentMatchHandler(w http.ResponseWriter, r *http.Request) {
	arena := web.arena
	matchTime := arena.MatchTimeSnapshot()
	response := map[string]any{
		"state":              matchStateName(matchTime.MatchState),
		"matchTimeSec":       matchTime.MatchTimeSec,
		"phaseIndex":         matchTime.PhaseIndex,
		"phaseId":            matchTime.PhaseId,
		"phaseTimeSec":       matchTime.PhaseTimeSec,
		"phaseRemainingSec":  matchTime.PhaseRemainingSec,
		"timeoutDurationSec": matchTime.TimeoutDurationSec,
		"score":              arena.RealtimeScoreSnapshot(),
	}
	if arena.CurrentMatch != nil {
		match := arena.CurrentMatch
		current := &apiMatchSummary{
			Id: match.Id, Type: match.Type.String(), TypeOrder: match.TypeOrder, ShortName: match.ShortName,
			LongName: match.LongName, Time: match.Time.UTC(), Status: matchStatusName(match.Status),
			Teams: map[string][]int{},
		}
		for index := 0; index < 2; index++ {
			allianceId := arena.AllianceId(index)
			if allianceId == "" {
				continue
			}
			teams := []int{}
			for _, stationId := range arena.AllianceStationIds(index) {
				teamId := 0
				if station := arena.AllianceStations[stationId]; station != nil && station.Team != nil {
					teamId = station.Team.Id
				}
				teams = append(teams, teamId)
			}
			current.Teams[allianceId] = teams
		}
		response["match"] = current
	}
	writeJson(w, http.StatusOK, response)
}

// apiRankingsHandler returns the qualification rankings with the active game's ranking columns.
func (web *Web) apiRankingsHandler(w http.ResponseWriter, r *http.Request) {
	rankings, err := web.arena.Database.GetAllRankings()
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type column struct {
		Name     string `json:"name"`
		Decimals int    `json:"decimals"`
	}
	columns := []column{}
	var keep []int // Indexes into each ranking's sort values; the random tiebreaker is not shown.
	if web.arena.Game != nil {
		for i, key := range web.arena.Game.Ranking.Order {
			if !key.Random {
				columns = append(columns, column{Name: key.Name, Decimals: key.Decimals})
				keep = append(keep, i)
			}
		}
	}
	type ranking struct {
		Rank              int                `json:"rank"`
		PreviousRank      int                `json:"previousRank"`
		Team              int                `json:"team"`
		RP                float64            `json:"rp"`
		Played            int                `json:"played"`
		Wins              int                `json:"wins"`
		Losses            int                `json:"losses"`
		Ties              int                `json:"ties"`
		Disqualifications int                `json:"disqualifications"`
		Values            []float64          `json:"values"` // One per column.
		Stats             map[string]float64 `json:"stats"`
	}
	out := make([]ranking, 0, len(rankings))
	for _, r := range rankings {
		values := make([]float64, 0, len(keep))
		for _, index := range keep {
			if index < len(r.Sort) {
				values = append(values, r.Sort[index])
			}
		}
		stats := r.Stats
		if stats == nil {
			stats = map[string]float64{}
		}
		out = append(out, ranking{
			Rank: r.Rank, PreviousRank: r.PreviousRank, Team: r.TeamId, RP: r.RP, Played: r.Played, Wins: r.Wins,
			Losses: r.Losses, Ties: r.Ties, Disqualifications: r.Disqualifications, Values: values, Stats: stats,
		})
	}
	writeJson(w, http.StatusOK, map[string]any{"columns": columns, "rankings": out})
}

// apiTeamsHandler lists the teams at the event. Wi-Fi keys and FTA notes are left out: this endpoint is public.
func (web *Web) apiTeamsHandler(w http.ResponseWriter, r *http.Request) {
	teams, err := web.arena.Database.GetAllTeams()
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type team struct {
		Id         int    `json:"id"`
		Name       string `json:"name"`
		Nickname   string `json:"nickname"`
		City       string `json:"city"`
		StateProv  string `json:"stateProv"`
		Country    string `json:"country"`
		SchoolName string `json:"schoolName"`
		RookieYear int    `json:"rookieYear"`
		RobotName  string `json:"robotName"`
	}
	out := make([]team, 0, len(teams))
	for _, t := range teams {
		out = append(out, team{
			Id: t.Id, Name: t.Name, Nickname: t.Nickname, City: t.City, StateProv: t.StateProv, Country: t.Country,
			SchoolName: t.SchoolName, RookieYear: t.RookieYear, RobotName: t.RobotName,
		})
	}
	writeJson(w, http.StatusOK, map[string]any{"teams": out})
}

// apiAlliancesHandler lists the playoff alliances.
func (web *Web) apiAlliancesHandler(w http.ResponseWriter, r *http.Request) {
	alliances, err := web.arena.Database.GetAllAlliances()
	if err != nil {
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type alliance struct {
		Number int   `json:"number"`
		Teams  []int `json:"teams"`
		Lineup []int `json:"lineup"`
	}
	out := make([]alliance, 0, len(alliances))
	for _, a := range alliances {
		teams := a.TeamIds
		if teams == nil {
			teams = []int{}
		}
		out = append(out, alliance{Number: a.Id, Teams: teams, Lineup: a.Lineup[:]})
	}
	writeJson(w, http.StatusOK, map[string]any{"alliances": out})
}

// apiSpecHandler serves the OpenAPI description of this API.
func (web *Web) apiSpecHandler(w http.ResponseWriter, r *http.Request) {
	spec, err := os.ReadFile(filepath.Join(model.BaseDir, openApiSpecPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJsonError(w, http.StatusNotFound, "the API description is not installed")
			return
		}
		writeJsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(spec)
}
