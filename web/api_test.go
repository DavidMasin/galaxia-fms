// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestMatchesApi(t *testing.T) {
	web := setupTestWeb(t)

	match1 := model.Match{
		Type:             model.Qualification,
		ShortName:        "Q1",
		Time:             time.Unix(0, 0),
		Red1:             1,
		Red2:             2,
		Red3:             3,
		Blue1:            4,
		Blue2:            5,
		Blue3:            6,
		Blue1IsSurrogate: true,
		Blue2IsSurrogate: true,
		Blue3IsSurrogate: true,
	}
	match2 := model.Match{
		Type:            model.Qualification,
		ShortName:       "Q2",
		Time:            time.Unix(600, 0),
		Red1:            7,
		Red2:            8,
		Red3:            9,
		Blue1:           10,
		Blue2:           11,
		Blue3:           12,
		Red1IsSurrogate: true,
		Red2IsSurrogate: true,
		Red3IsSurrogate: true,
	}
	match3 := model.Match{
		Type:      model.Practice,
		ShortName: "P1",
		Time:      time.Now(),
		Red1:      6,
		Red2:      5,
		Red3:      4,
		Blue1:     3,
		Blue2:     2,
		Blue3:     1,
	}
	web.arena.Database.CreateMatch(&match1)
	web.arena.Database.CreateMatch(&match2)
	web.arena.Database.CreateMatch(&match3)
	matchResult1 := model.BuildTestMatchResult(web.arena.Database, match1.Id, 1)
	web.arena.Database.CreateMatchResult(matchResult1)

	recorder := web.getHttpResponse("/api/matches/qualification")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	var matchesData []MatchWithResult
	err := json.Unmarshal([]byte(recorder.Body.String()), &matchesData)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(matchesData)) {
		assert.Equal(t, match1.Id, matchesData[0].Match.Id)
		// The stored result comes back whole: the game it was played with, its raw state and its audit log.
		result := matchesData[0].Result
		assert.Equal(t, matchResult1.Id, result.Id)
		assert.Equal(t, "rebuilt-style", result.GameId)
		assert.Equal(t, matchResult1.GameHash, result.GameHash)
		assert.Equal(t, len(matchResult1.Changes), len(result.Changes))
		assert.JSONEq(t, stateJson(t, matchResult1.State), stateJson(t, result.State))
		// With it comes what the game makes of it, per alliance.
		if assert.NotNil(t, result.Computed) {
			assert.Equal(t, "blue", result.Computed.Winner)
			assert.Equal(t, 133.0, result.Computed.Alliances["red"].Score)
			assert.Equal(t, 289.0, result.Computed.Alliances["blue"].Score)
			assert.Equal(t, 85.0, result.Computed.Alliances["blue"].FoulPoints)
			assert.Equal(t, 1.0, result.Computed.Alliances["blue"].RP["energized"])
		}
		assert.Equal(t, match2.Id, matchesData[1].Match.Id)
		assert.Nil(t, matchesData[1].Result)
	}

	// The JSON carries the computed result under the alliance ids of the game.
	assert.Contains(t, recorder.Body.String(), `"Computed": {`)
	assert.Contains(t, recorder.Body.String(), `"alliances": {`)
}

func TestRankingsApi(t *testing.T) {
	web := setupTestWeb(t)

	// Test that empty rankings produces an empty array.
	recorder := web.getHttpResponse("/api/rankings")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	rankingsData := struct {
		Columns            []RankingsApiColumn
		Rankings           []RankingsApiRanking
		HighestPlayedMatch string
	}{}
	err := json.Unmarshal([]byte(recorder.Body.String()), &rankingsData)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(rankingsData.Rankings))
	assert.Contains(t, recorder.Body.String(), `"Rankings": []`)
	assert.Equal(t, "", rankingsData.HighestPlayedMatch)

	// The columns are the ranking order of the active game, in order, whether or not there are rankings yet.
	assert.Equal(
		t,
		[]RankingsApiColumn{
			{Name: "Ranking score", Decimals: 2}, {Name: "Avg match", Decimals: 2}, {Name: "Avg auto fuel", Decimals: 2},
			{Name: "Avg tower", Decimals: 2}, {Random: true},
		},
		rankingsData.Columns,
	)

	web.arena.Database.CreateRanking(testRanking2())
	web.arena.Database.CreateRanking(testRanking1())
	ranking1 := RankingsApiRanking{
		TeamId: 1114, Nickname: "Simbots", Rank: 2, RP: 18, Played: 10, Wins: 1, Losses: 3, Ties: 2,
		Sort: []float64{1.8, 70, 62.5, 9, 0.1114},
	}
	ranking2 := RankingsApiRanking{
		TeamId: 254, Nickname: "ChezyPof", Rank: 1, RP: 20, Played: 10, Wins: 3, Losses: 2, Ties: 1,
		Sort: []float64{2, 62.5, 9, 55.4, 0.254},
	}
	web.arena.Database.CreateMatch(&model.Match{Type: model.Qualification, ShortName: "Q29", Status: model.RedWonMatch})
	web.arena.Database.CreateMatch(&model.Match{Type: model.Qualification, ShortName: "Q30"})
	web.arena.Database.CreateTeam(&model.Team{Id: 254, Nickname: "ChezyPof"})
	web.arena.Database.CreateTeam(&model.Team{Id: 1114, Nickname: "Simbots"})

	recorder = web.getHttpResponse("/api/rankings")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	err = json.Unmarshal([]byte(recorder.Body.String()), &rankingsData)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(rankingsData.Rankings)) {
		assert.Equal(t, ranking1, rankingsData.Rankings[1])
		assert.Equal(t, ranking2, rankingsData.Rankings[0])
	}
	assert.Equal(t, "Q29", rankingsData.HighestPlayedMatch)

	// The exact shape is what the rankings display reads.
	for _, key := range []string{
		`"TeamId": 254`, `"Nickname": "ChezyPof"`, `"Rank": 1`, `"PreviousRank": 0`, `"RP": 20`, `"Played": 10`,
		`"Wins": 3`, `"Losses": 2`, `"Ties": 1`, `"Disqualifications": 0`, `"Sort": [`, `"Columns": [`,
		`"Decimals": 2`, `"Random": false`,
	} {
		assert.Contains(t, recorder.Body.String(), key)
	}

	// Under a game with a different ranking order, every ranking still has exactly one sort value per column.
	selectTestGame(t, web, "ball-count")
	recorder = web.getHttpResponse("/api/rankings")
	assert.Equal(t, 200, recorder.Code)
	assert.Nil(t, json.Unmarshal([]byte(recorder.Body.String()), &rankingsData))
	if assert.Equal(t, 3, len(rankingsData.Columns)) {
		assert.Equal(t, "Avg match points", rankingsData.Columns[1].Name)
		assert.Equal(t, 1, rankingsData.Columns[1].Decimals)
	}
	for _, ranking := range rankingsData.Rankings {
		assert.Equal(t, 3, len(ranking.Sort))
	}
}

func TestSponsorSlidesApi(t *testing.T) {
	web := setupTestWeb(t)

	slide1 := model.SponsorSlide{0, "subtitle", "line1", "line2", "image", 2, 1}
	slide2 := model.SponsorSlide{0, "Chezy Sponsaur", "Teh", "Chezy Pofs", "ejface.jpg", 54, 2}
	assert.Nil(t, web.arena.Database.CreateSponsorSlide(&slide1))
	assert.Nil(t, web.arena.Database.CreateSponsorSlide(&slide2))

	recorder := web.getHttpResponse("/api/sponsor_slides")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	var sponsorSlides []model.SponsorSlide
	err := json.Unmarshal([]byte(recorder.Body.String()), &sponsorSlides)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(sponsorSlides)) {
		assert.Equal(t, slide1, sponsorSlides[0])
		assert.Equal(t, slide2, sponsorSlides[1])
	}
}

func TestAlliancesApi(t *testing.T) {
	web := setupTestWeb(t)

	model.BuildTestAlliances(web.arena.Database)

	recorder := web.getHttpResponse("/api/alliances")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	var alliances []model.Alliance
	err := json.Unmarshal([]byte(recorder.Body.String()), &alliances)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(alliances)) {
		if assert.Equal(t, 5, len(alliances[0].TeamIds)) {
			assert.Equal(t, 254, alliances[0].TeamIds[0])
			assert.Equal(t, 469, alliances[0].TeamIds[1])
			assert.Equal(t, 2848, alliances[0].TeamIds[2])
			assert.Equal(t, 74, alliances[0].TeamIds[3])
			assert.Equal(t, 3175, alliances[0].TeamIds[4])
		}
		if assert.Equal(t, 3, len(alliances[1].TeamIds)) {
			assert.Equal(t, 1718, alliances[1].TeamIds[0])
			assert.Equal(t, 2451, alliances[1].TeamIds[1])
			assert.Equal(t, 1619, alliances[1].TeamIds[2])
		}
	}
}

func TestArenaWebsocketApi(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/api/arena/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketType(t, ws, "game")
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
}

func TestBracketSvgApiDoubleElimination(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.PlayoffType = model.DoubleEliminationPlayoff
	tournament.CreateTestAlliances(web.arena.Database, 8)
	web.arena.CreatePlayoffTournament()

	recorder := web.getHttpResponse("/api/bracket/svg")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "image/svg+xml", recorder.Header()["Content-Type"][0])
	assert.Contains(t, recorder.Body.String(), "Best-of-3")
}

func TestBracketSvgApiFourAllianceDoubleElimination(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.PlayoffType = model.DoubleEliminationPlayoff
	web.arena.EventSettings.NumPlayoffAlliances = 4
	tournament.CreateTestAlliances(web.arena.Database, 4)
	web.arena.CreatePlayoffTournament()

	recorder := web.getHttpResponse("/api/bracket/svg")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "image/svg+xml", recorder.Header()["Content-Type"][0])
	assert.Contains(t, recorder.Body.String(), "bracket_double4")
	assert.Contains(t, recorder.Body.String(), "match_M5")
	assert.Contains(t, recorder.Body.String(), "Finals")
}
