// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web handlers for announcer display.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"log"
	"net/http"
	"sort"
)

// announcerLoadSide is one side of the loaded match as the announcer reads it out.
type announcerLoadSide struct {
	Slot            string
	AllianceName    string
	PlayoffAlliance int           // The number of the playoff alliance, or zero outside the playoffs.
	Teams           []*model.Team // By robot position; nil where the station is empty.
	OffFieldTeams   []*model.Team // Members of the playoff alliance that are not playing this match.
}

// announcerFoul is one foul of a posted result, in words.
type announcerFoul struct {
	TypeName string
	Points   string
	Team     int // Zero if the foul was not attributed to a team.
	Rule     string
	RuleName string
	RuleText string
}

// announcerCard is a card a team was given in a posted result.
type announcerCard struct {
	Team     int
	Position int
	Name     string
}

// announcerRanking is where a team stands after a posted qualification result.
type announcerRanking struct {
	Team         int
	Rank         int
	PreviousRank int
}

// announcerPostedAlliance is everything about one alliance of a posted result.
type announcerPostedAlliance struct {
	Id            string
	Name          string
	Slot          string
	Score         string
	Disqualified  bool
	Breakdown     []namedValue
	RankingPoints []namedValue
	RpTotal       string
	Fouls         []announcerFoul
	Cards         []announcerCard
	Rankings      []announcerRanking
}

// announcerScorePosted is a posted result laid out for the announcer.
type announcerScorePosted struct {
	Match             *model.Match
	ShowRankingPoints bool
	WinnerName        string // Empty for a tie.
	WinnerColor       string
	Tiebreak          string
	Alliances         []announcerPostedAlliance
}

// Renders the announcer display which shows team info and scores for the current match.
func (web *Web) announcerDisplayHandler(w http.ResponseWriter, r *http.Request) {
	if !web.enforceDisplayConfiguration(w, r, nil) {
		return
	}

	template, err := web.parseFiles("templates/announcer_display.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	data := struct {
		*model.EventSettings
	}{web.arena.EventSettings}
	err = template.ExecuteTemplate(w, "base_no_navbar", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Renders a partial template for when a new match is loaded.
func (web *Web) announcerDisplayMatchLoadHandler(w http.ResponseWriter, r *http.Request) {
	template, err := web.parseFiles("templates/announcer_display_match_load.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	match := web.arena.CurrentMatch
	playoffAllianceIds := []int{match.PlayoffRedAlliance, match.PlayoffBlueAlliance}
	offFieldTeamIds := [][]int{nil, nil}
	if match.Type == model.Playoff {
		offFieldTeamIds[0], offFieldTeamIds[1], err = web.arena.Database.GetOffFieldTeamIds(match)
		if err != nil {
			handleWebErr(w, err)
			return
		}
	}

	rankings := make(map[int]int)
	addRanking := func(teamId int) {
		ranking, err := web.arena.Database.GetRankingForTeam(teamId)
		if err != nil {
			log.Printf("Failed to get ranking for team %d for the announcer display: %v", teamId, err)
		}
		if ranking != nil {
			rankings[teamId] = ranking.Rank
		}
	}
	var sides []announcerLoadSide
	for index, alliance := range web.arena.Game.Alliances.List {
		if index >= len(playoffAllianceIds) {
			break
		}
		side := announcerLoadSide{
			Slot: slotForIndex(index), AllianceName: alliance.Name, PlayoffAlliance: playoffAllianceIds[index],
		}
		for _, stationId := range web.arena.AllianceStationIds(index) {
			team := web.arena.AllianceStations[stationId].Team
			side.Teams = append(side.Teams, team)
			if team != nil {
				addRanking(team.Id)
			}
		}
		for _, teamId := range offFieldTeamIds[index] {
			team, err := web.arena.Database.GetTeamById(teamId)
			if err != nil {
				handleWebErr(w, err)
				return
			}
			if team != nil {
				side.OffFieldTeams = append(side.OffFieldTeams, team)
				addRanking(team.Id)
			}
		}
		sides = append(sides, side)
	}

	data := struct {
		Match     *model.Match
		IsPlayoff bool
		Sides     []announcerLoadSide
		Rankings  map[int]int
	}{match, match.Type == model.Playoff, sides, rankings}
	err = template.ExecuteTemplate(w, "announcer_display_match_load", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Renders a partial template for when a final score is posted.
func (web *Web) announcerDisplayScorePostedHandler(w http.ResponseWriter, r *http.Request) {
	template, err := web.parseFiles("templates/announcer_display_score_posted.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	message, ok := web.arena.GenerateScorePostedMessage().(*field.ScorePostedMessage)
	if !ok {
		handleWebErr(w, fmt.Errorf("the posted result could not be read"))
		return
	}
	// The names of everything in the result come from the game it was played with.
	resultGame, err := web.arena.Database.GetSnapshotGame(message.GameHash)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	err = template.ExecuteTemplate(w, "announcer_display_score_posted", buildAnnouncerScorePosted(resultGame, message))
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// buildAnnouncerScorePosted lays out a posted result with its alliances in the order of the game it was played with.
func buildAnnouncerScorePosted(g *gamedef.Game, message *field.ScorePostedMessage) *announcerScorePosted {
	posted := &announcerScorePosted{
		Match: message.Match, ShowRankingPoints: message.Match.Type == model.Qualification, Tiebreak: message.Tiebreak,
	}
	for index, alliance := range g.Alliances.List {
		postedAlliance := message.Alliances[alliance.ID]
		if postedAlliance == nil {
			continue
		}
		if alliance.ID == message.Winner {
			posted.WinnerName, posted.WinnerColor = alliance.Name, alliance.Color
		}
		listed := announcerPostedAlliance{
			Id: alliance.ID, Name: alliance.Name, Slot: slotForIndex(index), Score: formatNumber(postedAlliance.Score),
			Disqualified: postedAlliance.Disqualified, RpTotal: formatNumber(postedAlliance.RPTotal),
		}
		for _, row := range postedAlliance.Breakdown {
			listed.Breakdown = append(
				listed.Breakdown, namedValue{Id: row.ID, Name: row.Name, Value: row.Value, Text: formatNumber(row.Value)},
			)
		}
		for _, rankingPoint := range postedAlliance.RankingPoints {
			listed.RankingPoints = append(
				listed.RankingPoints,
				namedValue{
					Id: rankingPoint.Id, Name: rankingPoint.Name, Value: rankingPoint.Value,
					Text: formatNumber(rankingPoint.Value),
				},
			)
		}
		for _, foul := range postedAlliance.Fouls {
			listed.Fouls = append(
				listed.Fouls,
				announcerFoul{
					TypeName: foul.TypeName, Points: formatNumber(foul.Points), Team: foul.Team, Rule: foul.Rule,
					RuleName: foul.RuleName, RuleText: foul.RuleText,
				},
			)
		}
		for position, cardId := range postedAlliance.Cards {
			card := announcerCard{Position: position, Name: cardId}
			if definition := g.Card(cardId); definition != nil {
				card.Name = definition.Name
			}
			if position >= 1 && position <= len(postedAlliance.Teams) {
				card.Team = postedAlliance.Teams[position-1]
			}
			listed.Cards = append(listed.Cards, card)
		}
		sort.Slice(listed.Cards, func(i, j int) bool { return listed.Cards[i].Position < listed.Cards[j].Position })
		for _, teamId := range postedAlliance.Teams {
			if ranking := postedAlliance.Rankings[teamId]; ranking != nil {
				listed.Rankings = append(
					listed.Rankings,
					announcerRanking{Team: teamId, Rank: ranking.Rank, PreviousRank: ranking.PreviousRank},
				)
			}
		}
		posted.Alliances = append(posted.Alliances, listed)
	}
	return posted
}

// The websocket endpoint for the announcer display client to send control commands and receive status updates.
func (web *Web) announcerDisplayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	display, err := web.registerDisplay(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer web.arena.MarkDisplayDisconnected(display.DisplayConfiguration.Id)

	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)

	// Subscribe the websocket to the notifiers whose messages will be passed on to the client.
	ws.HandleNotifiers(
		display.Notifier,
		web.arena.GameNotifier,
		web.arena.AudienceDisplayModeNotifier,
		web.arena.EventStatusNotifier,
		web.arena.MatchLoadNotifier,
		web.arena.MatchTimeNotifier,
		web.arena.RealtimeScoreNotifier,
		web.arena.ScorePostedNotifier,
		web.arena.ReloadDisplaysNotifier,
	)
}
