// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web API for providing JSON-formatted event data.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/partner"
	"github.com/DavidMasin/galaxia-fms/playoff"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
)

// MatchResultWithScore is a stored match result together with what the game it was played with makes of it.
type MatchResultWithScore struct {
	model.MatchResult
	// Computed holds the points, breakdown, ranking points and outcome of every alliance, keyed by alliance id. It
	// is null if the result can no longer be computed, for example because its game definition is gone.
	Computed *scoring.Result
}

type MatchWithResult struct {
	model.Match
	Result *MatchResultWithScore
}

// RankingsApiColumn describes one sort value of the rankings: a key of the active game's ranking order.
type RankingsApiColumn struct {
	Name     string
	Decimals int
	Random   bool
}

// RankingsApiRanking is one team's place in the rankings. Sort holds one value per column.
type RankingsApiRanking struct {
	TeamId            int
	Nickname          string
	Rank              int
	PreviousRank      int
	RP                float64
	Played            int
	Wins              int
	Losses            int
	Ties              int
	Disqualifications int
	Sort              []float64
}

type allianceMatchup struct {
	Id                 string
	RedAllianceSource  string
	BlueAllianceSource string
	RedAlliance        *model.Alliance
	BlueAlliance       *model.Alliance
	IsActive           bool
	SeriesLeader       string
	SeriesStatus       string
	IsComplete         bool
}

// Generates a JSON dump of the matches and results.
func (web *Web) matchesApiHandler(w http.ResponseWriter, r *http.Request) {
	matchType, err := model.MatchTypeFromString(r.PathValue("type"))
	if err != nil {
		handleWebErr(w, err)
		return
	}

	matches, err := web.arena.Database.GetMatchesByType(matchType, false)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	matchesWithResults := make([]MatchWithResult, len(matches))
	for i, match := range matches {
		matchesWithResults[i].Match = match
		matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
		if err != nil {
			handleWebErr(w, err)
			return
		}
		if matchResult != nil {
			matchResultWithScore := &MatchResultWithScore{MatchResult: *matchResult}
			computed, _, err := matchResult.Compute(web.arena.Database, match.UseTiebreakCriteria)
			if err != nil {
				log.Printf("Failed to compute the result of match %d for the matches API: %v", match.Id, err)
			} else {
				matchResultWithScore.Computed = computed
			}
			matchesWithResults[i].Result = matchResultWithScore
		}
	}

	jsonData, err := json.MarshalIndent(matchesWithResults, "", "  ")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Access-Control-Allow-Origin", "*")
	_, err = w.Write(jsonData)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Generates a JSON dump of the sponsor slides for use by the audience display.
func (web *Web) sponsorSlidesApiHandler(w http.ResponseWriter, r *http.Request) {
	sponsors, err := web.arena.Database.GetAllSponsorSlides()
	if err != nil {
		handleWebErr(w, err)
		return
	}

	if sponsors == nil {
		// Go marshals an empty slice to null, so explicitly create it so that it appears as an empty JSON array.
		sponsors = make([]model.SponsorSlide, 0)
	}
	jsonData, err := json.MarshalIndent(sponsors, "", "  ")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Access-Control-Allow-Origin", "*")
	_, err = w.Write(jsonData)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Generates a JSON dump of the qualification rankings, primarily for use by the rankings display.
func (web *Web) rankingsApiHandler(w http.ResponseWriter, r *http.Request) {
	rankings, err := web.arena.Database.GetAllRankings()
	if err != nil {
		handleWebErr(w, err)
		return
	}

	// The columns are the keys the active game sorts teams by; each ranking holds one value per column.
	columns := make([]RankingsApiColumn, len(web.arena.Game.Ranking.Order))
	for i, key := range web.arena.Game.Ranking.Order {
		columns[i] = RankingsApiColumn{Name: key.Name, Decimals: key.Decimals, Random: key.Random}
	}

	// Get team info so that nicknames can be displayed.
	teams, err := web.arena.Database.GetAllTeams()
	if err != nil {
		handleWebErr(w, err)
		return
	}
	teamNicknames := make(map[int]string)
	for _, team := range teams {
		teamNicknames[team.Id] = team.Nickname
	}

	// Go marshals a nil slice to null, so the slices are created explicitly to appear as empty JSON arrays.
	apiRankings := make([]RankingsApiRanking, len(rankings))
	for i, ranking := range rankings {
		// Rankings calculated under another game may hold a different number of sort values; a browser is always
		// given exactly one per column.
		sortValues := make([]float64, len(columns))
		copy(sortValues, ranking.Sort)
		apiRankings[i] = RankingsApiRanking{
			TeamId: ranking.TeamId, Nickname: teamNicknames[ranking.TeamId], Rank: ranking.Rank,
			PreviousRank: ranking.PreviousRank, RP: ranking.RP, Played: ranking.Played, Wins: ranking.Wins,
			Losses: ranking.Losses, Ties: ranking.Ties, Disqualifications: ranking.Disqualifications,
			Sort: sortValues,
		}
	}

	// Get the last match scored so we can report that on the display.
	matches, err := web.arena.Database.GetMatchesByType(model.Qualification, false)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	var highestPlayedMatch model.Match
	for _, match := range matches {
		if match.IsComplete() {
			highestPlayedMatch = match
		}
	}

	data := struct {
		Columns            []RankingsApiColumn
		Rankings           []RankingsApiRanking
		HighestPlayedMatch string
	}{columns, apiRankings, highestPlayedMatch.ShortName}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Access-Control-Allow-Origin", "*")
	_, err = w.Write(jsonData)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Generates a JSON dump of the alliances.
func (web *Web) alliancesApiHandler(w http.ResponseWriter, r *http.Request) {
	alliances, err := web.arena.Database.GetAllAlliances()
	if err != nil {
		handleWebErr(w, err)
		return
	}

	jsonData, err := json.MarshalIndent(alliances, "", "  ")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Access-Control-Allow-Origin", "*")
	_, err = w.Write(jsonData)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Websocket API for receiving arena status updates.
func (web *Web) arenaWebsocketApiHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)

	// Subscribe the websocket to the notifiers whose messages will be passed on to the client.
	ws.HandleNotifiers(web.arena.GameNotifier, web.arena.MatchLoadNotifier, web.arena.MatchTimeNotifier)
}

// Serves the avatar for a given team, or a default if none exists.
func (web *Web) teamAvatarsApiHandler(w http.ResponseWriter, r *http.Request) {
	teamId, err := strconv.Atoi(r.PathValue("teamId"))
	if err != nil {
		handleWebErr(w, err)
		return
	}

	avatarPath := fmt.Sprintf("%s/%d.png", partner.AvatarsDir, teamId)
	if _, err := os.Stat(avatarPath); os.IsNotExist(err) {
		avatarPath = fmt.Sprintf("%s/0.png", partner.AvatarsDir)
	}

	http.ServeFile(w, r, avatarPath)
}

func (web *Web) bracketSvgApiHandler(w http.ResponseWriter, r *http.Request) {
	var activeMatch *model.Match
	if activeMatchValue, ok := r.URL.Query()["activeMatch"]; ok {
		if activeMatchValue[0] == "current" {
			activeMatch = web.arena.CurrentMatch
		} else if activeMatchValue[0] == "saved" {
			activeMatch = web.arena.SavedMatch
		}
	}

	w.Header().Add("Content-Type", "image/svg+xml")
	w.Header().Add("Access-Control-Allow-Origin", "*")
	if err := web.generateBracketSvg(w, activeMatch); err != nil {
		handleWebErr(w, err)
		return
	}
}

func (web *Web) generateBracketSvg(w io.Writer, activeMatch *model.Match) error {
	alliances, err := web.arena.Database.GetAllAlliances()
	if err != nil {
		return err
	}

	matchups := make(map[string]*allianceMatchup)
	if web.arena.PlayoffTournament != nil {
		for _, matchGroup := range web.arena.PlayoffTournament.MatchGroups() {
			matchup, ok := matchGroup.(*playoff.Matchup)
			if !ok {
				continue
			}
			allianceMatchup := allianceMatchup{
				Id:                 matchup.Id(),
				RedAllianceSource:  matchup.RedAllianceSourceDisplayName(),
				BlueAllianceSource: matchup.BlueAllianceSourceDisplayName(),
				IsComplete:         matchup.IsComplete(),
			}
			if matchup.RedAllianceId > 0 {
				if len(alliances) > 0 {
					allianceMatchup.RedAlliance = &alliances[matchup.RedAllianceId-1]
				} else {
					allianceMatchup.RedAlliance = &model.Alliance{Id: matchup.RedAllianceId}
				}
			}
			if matchup.BlueAllianceId > 0 {
				if len(alliances) > 0 {
					allianceMatchup.BlueAlliance = &alliances[matchup.BlueAllianceId-1]
				} else {
					allianceMatchup.BlueAlliance = &model.Alliance{Id: matchup.BlueAllianceId}
				}
			}
			if activeMatch != nil {
				allianceMatchup.IsActive = activeMatch.PlayoffMatchGroupId == matchup.Id()
			}
			allianceMatchup.SeriesLeader, allianceMatchup.SeriesStatus = matchup.StatusText()
			matchups[matchup.Id()] = &allianceMatchup
		}
	}

	bracketType := "double"
	numAlliances := web.arena.EventSettings.NumPlayoffAlliances
	if web.arena.EventSettings.PlayoffType == model.DoubleEliminationPlayoff && numAlliances == 4 {
		bracketType = "double4"
	} else if web.arena.EventSettings.PlayoffType == model.SingleEliminationPlayoff {
		if numAlliances > 8 {
			bracketType = "16"
		} else if numAlliances > 4 {
			bracketType = "8"
		} else if numAlliances > 2 {
			bracketType = "4"
		} else {
			bracketType = "2"
		}
	}

	template, err := web.parseFiles("templates/bracket.svg")
	if err != nil {
		return err
	}
	data := struct {
		BracketType string
		Matchups    map[string]*allianceMatchup
	}{bracketType, matchups}
	return template.ExecuteTemplate(w, "bracket", data)
}
