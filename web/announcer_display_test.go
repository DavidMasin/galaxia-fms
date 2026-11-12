// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

func TestAnnouncerDisplay(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/displays/announcer?displayId=1")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Announcer Display - Untitled Event - Galaxia FMS")
}

func TestAnnouncerDisplayMatchLoad(t *testing.T) {
	web := setupTestWeb(t)
	match := model.Match{Type: model.Playoff, Red1: 254, Red2: 1114, Blue3: 2056}
	web.arena.LoadMatch(&match)

	recorder := web.getHttpResponse("/displays/announcer/match_load")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "254")
	assert.Contains(t, recorder.Body.String(), "1114")
	assert.Contains(t, recorder.Body.String(), "2056")
	// Each side is headed by the name the game gives its alliance, and laid out in that side's slot.
	assert.Contains(t, recorder.Body.String(), "bg-red")
	assert.Contains(t, recorder.Body.String(), "bg-blue")
	assert.Contains(t, recorder.Body.String(), "<b>Red &ndash; Alliance 0</b>")
	assert.Contains(t, recorder.Body.String(), "<b>Blue &ndash; Alliance 0</b>")
	// The three stations without a team are said to be empty.
	assert.Equal(t, 3, strings.Count(recorder.Body.String(), "No team present"))

	// A team's rank is shown next to it.
	assert.Nil(t, web.arena.Database.CreateRanking(testRanking1()))
	recorder = web.getHttpResponse("/displays/announcer/match_load")
	assert.Equal(t, 200, recorder.Code)
	assert.Regexp(t, `<div class="col-sm-6">1</div>`, recorder.Body.String())
}

func TestAnnouncerDisplayScorePosted(t *testing.T) {
	web := setupTestWeb(t)

	// Before anything is committed the announcer sees an empty result of the active game: a tie, with the game's
	// breakdown rows and nothing in them.
	web.arena.SavedMatch = &model.Match{Type: model.Qualification, LongName: "Qual 17"}
	recorder := web.getHttpResponse("/displays/announcer/score_posted")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Qual 17")
	assert.Contains(t, recorder.Body.String(), "Winner: Tie")
	assert.Contains(t, recorder.Body.String(), "Auto fuel")

	// A played result of the rebuilt-style example game: red loses 133 to 289, committed seven fouls, and its first
	// robot holds a yellow card.
	match := model.Match{
		Id: 1, Type: model.Qualification, LongName: "Qual 18", Status: model.BlueWonMatch, Red1: 101, Red2: 102,
		Red3: 103, Blue1: 104, Blue2: 105, Blue3: 106,
	}
	matchResult := model.BuildTestMatchResult(web.arena.Database, match.Id, 1)
	_, err := matchResult.Edit(
		model.TestGame("rebuilt-style"),
		scoring.Change{
			Alliance: "blue", Robot: 2, Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "minor", Rule: "G418",
			Bucket: "endgame", Source: scoring.Source{Kind: scoring.SourceReferee},
		},
	)
	assert.Nil(t, err)
	web.arena.SavedMatch = &match
	web.arena.SavedMatchResult = matchResult
	web.arena.SavedRankings = []model.Ranking{
		model.NewRanking(scoring.TeamRanking{TeamID: 101, Rank: 3, PreviousRank: 1}),
		model.NewRanking(scoring.TeamRanking{TeamID: 104, Rank: 1, PreviousRank: 2}),
	}

	recorder = web.getHttpResponse("/displays/announcer/score_posted")
	assert.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "Final Results &ndash; Qual 18")
	// The winner is named as the game names it and shown in the colour the game gives it.
	assert.Contains(t, body, "Winner: Blue")
	assert.Contains(t, body, "background-color: #1565c0")
	// Breakdown rows by name, with their values.
	assert.Regexp(t, `Auto fuel</div>\s*<div class="col-sm-4">18</div>`, body)
	assert.Regexp(t, `Fouls</div>\s*<div class="col-sm-4">85</div>`, body)
	assert.Regexp(t, `Fouls</div>\s*<div class="col-sm-4">5</div>`, body)
	assert.Regexp(t, `<b>138</b>`, body)
	assert.Regexp(t, `<b>289</b>`, body)
	// Ranking points by name.
	assert.Regexp(t, `Win RP</div>\s*<div class="col-sm-4">3</div>`, body)
	assert.Regexp(t, `Energized RP</div>\s*<div class="col-sm-4">1</div>`, body)
	assert.Contains(t, body, "Supercharged RP")
	assert.Contains(t, body, "Traversal RP")
	// Fouls with the name of their type, the team and the rule with its text.
	assert.Contains(t, body, "Major foul (15)")
	assert.Contains(t, body, "Foul (5)")
	assert.Contains(t, body, "Team 105")
	assert.Contains(t, body, "G418: Pinning")
	assert.Contains(t, body, "A robot may not pin an opponent for more than three seconds.")
	// Cards by team, under the name the game gives the card.
	assert.Regexp(t, `Team 101</div>\s*<div class="col-sm-4">Yellow card</div>`, body)
	// Rankings with the direction of the change.
	assert.Contains(t, body, "(was 1)")
	assert.Contains(t, body, "(was 2)")

	// A playoff result shows no ranking points, and says which tiebreaker decided it.
	tied := model.NewMatchResult(model.TestGame("rebuilt-style"))
	tied.MatchType = model.Playoff
	_, err = tied.Edit(
		model.TestGame("rebuilt-style"),
		scoring.Change{
			Alliance: "red", Element: "fuel", Op: scoring.OpSet, Value: 4.0, Bucket: "auto",
			Source: scoring.Source{Kind: scoring.SourceAdmin},
		},
	)
	assert.Nil(t, err)
	_, err = tied.Edit(
		model.TestGame("rebuilt-style"),
		scoring.Change{
			Alliance: "blue", Element: "fuel", Op: scoring.OpSet, Value: 4.0, Bucket: "endgame",
			Source: scoring.Source{Kind: scoring.SourceAdmin},
		},
	)
	assert.Nil(t, err)
	web.arena.SavedMatch = &model.Match{Type: model.Playoff, LongName: "Final 1", UseTiebreakCriteria: true}
	web.arena.SavedMatchResult = tied
	recorder = web.getHttpResponse("/displays/announcer/score_posted")
	assert.Equal(t, 200, recorder.Code)
	body = recorder.Body.String()
	assert.Contains(t, body, "Winner: Red")
	assert.Contains(t, body, "Decided by the tiebreaker: Auto fuel")
	assert.NotContains(t, body, "Energized RP")
	assert.NotContains(t, body, "Ranking Points")
}

func TestAnnouncerDisplayScorePostedOtherGame(t *testing.T) {
	web := setupTestWeb(t)

	// A result is announced with the game it was played with, whatever game is active now.
	g := model.TestGame("ball-count")
	assert.Nil(t, web.arena.Database.SaveGameSnapshot(g))
	matchResult := model.NewMatchResult(g)
	matchResult.MatchType = model.Qualification
	for _, change := range []scoring.Change{
		{Alliance: "red", Element: "balls", Op: scoring.OpSet, Value: 3.0, Bucket: "auto"},
		{Alliance: "red", Element: "balls", Op: scoring.OpSet, Value: 10.0, Bucket: "teleop"},
		{Alliance: "blue", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "foul"},
	} {
		change.Source = scoring.Source{Kind: scoring.SourceAdmin}
		_, err := matchResult.Edit(g, change)
		assert.Nil(t, err)
	}
	web.arena.SavedMatch = &model.Match{Type: model.Qualification, LongName: "Qual 3"}
	web.arena.SavedMatchResult = matchResult

	recorder := web.getHttpResponse("/displays/announcer/score_posted")
	assert.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "Winner: Red")
	assert.Regexp(t, `Auto</div>\s*<div class="col-sm-4">6</div>`, body)
	assert.Regexp(t, `Teleop</div>\s*<div class="col-sm-4">10</div>`, body)
	assert.Regexp(t, `<b>19</b>`, body)
	assert.Regexp(t, `Win RP</div>\s*<div class="col-sm-4">2</div>`, body)
	assert.NotContains(t, body, "Energized")
}

func TestAnnouncerDisplayWebsocket(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/displays/announcer/websocket?displayId=1", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketType(t, ws, "displayConfiguration")
	readWebsocketType(t, ws, "game")
	readWebsocketType(t, ws, "audienceDisplayMode")
	readWebsocketType(t, ws, "eventStatus")
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
	readWebsocketType(t, ws, "realtimeScore")
	readWebsocketType(t, ws, "scorePosted")

	web.arena.MatchLoadNotifier.Notify()
	readWebsocketType(t, ws, "matchLoad")
	web.arena.AllianceStations["R1"].Bypass = true
	web.arena.AllianceStations["R2"].Bypass = true
	web.arena.AllianceStations["R3"].Bypass = true
	web.arena.AllianceStations["B1"].Bypass = true
	web.arena.AllianceStations["B2"].Bypass = true
	web.arena.AllianceStations["B3"].Bypass = true
	web.arena.StartMatch()
	web.arena.Update()
	matchTime := readWebsocketUntil(t, ws, "matchTime").(map[string]any)
	assert.Equal(t, float64(field.InMatch), matchTime["MatchState"])
	assert.Equal(t, web.arena.Game.Match.Phases[0].ID, matchTime["PhaseId"])
	web.arena.RealtimeScoreNotifier.Notify()
	readWebsocketUntil(t, ws, "realtimeScore")
	web.arena.ScorePostedNotifier.Notify()
	readWebsocketUntil(t, ws, "scorePosted")
}
