// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIndex(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Home - Untitled Event - Galaxia FMS")
}

func (web *Web) getHttpResponse(path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", path, nil)
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

func (web *Web) getHttpResponseWithHeaders(path string, headers map[string]string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

func (web *Web) postHttpResponse(path string, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

// Starts a real local HTTP server that can be used by more sophisticated tests.
func (web *Web) startTestServer() (*httptest.Server, string) {
	server := httptest.NewServer(web.newHandler())
	return server, "ws" + server.URL[len("http"):]
}

// Receives the next websocket message and asserts that it is an error.
func readWebsocketError(t *testing.T, ws *websocket.Websocket) string {
	messageType, data, err := ws.Read()
	if assert.Nil(t, err) && assert.Equal(t, "error", messageType) {
		return data.(string)
	}
	return "error"
}

// Receives the next websocket message and asserts that it is of the given type.
func readWebsocketType(t *testing.T, ws *websocket.Websocket, expectedMessageType string) any {
	messageType, message, err := ws.ReadWithTimeout(time.Second)
	if assert.Nil(t, err) {
		assert.Equal(t, expectedMessageType, messageType)
	}
	return message
}

func readWebsocketMultiple(t *testing.T, ws *websocket.Websocket, count int) map[string]any {
	messages := make(map[string]any)
	for i := 0; i < count; i++ {
		messageType, message, err := ws.ReadWithTimeout(time.Second)
		if assert.Nil(t, err) {
			messages[messageType] = message
		}
	}
	return messages
}

// Receives websocket messages until one of the given type arrives, and returns it. Messages of other types are
// skipped: the arena sends the clock, the score and the status of the field whenever they change, so what arrives
// between two things a test does is not always the same.
func readWebsocketUntil(t *testing.T, ws *websocket.Websocket, expectedMessageType string) any {
	for i := 0; i < 50; i++ {
		messageType, message, err := ws.ReadWithTimeout(2 * time.Second)
		if !assert.Nil(t, err) {
			return nil
		}
		if messageType == expectedMessageType {
			return message
		}
	}
	assert.Fail(t, "did not receive a websocket message of type "+expectedMessageType)
	return nil
}

func setupTestWeb(t *testing.T) *Web {
	arena := field.SetupTestArena(t)
	return NewWeb(arena)
}

// selectTestGame makes the given example game the active one, as selecting it on the settings page would.
func selectTestGame(t *testing.T, web *Web, gameId string) {
	web.arena.EventSettings.GameId = gameId
	web.arena.EventSettings.GameParams = map[string]float64{}
	assert.Nil(t, web.arena.Database.UpdateEventSettings(web.arena.EventSettings))
	assert.Nil(t, web.arena.LoadSettings())
	assert.Equal(t, gameId, web.arena.Game.ID)
}

// testRanking1 and testRanking2 are the two stored rankings used by the tests of everything that lists rankings.
// Their sort values belong to the ranking order of the rebuilt-style example game: ranking score, average match
// points, average auto fuel, average tower points and the random tiebreaker.
func testRanking1() *model.Ranking {
	ranking := model.NewRanking(
		scoring.TeamRanking{
			TeamID: 254, Rank: 1, RP: 20, Played: 10, Wins: 3, Losses: 2, Ties: 1,
			Stats: map[string]float64{"match_points": 625, "auto_fuel_points": 90, "tower_points": 554},
			Sort:  []float64{2, 62.5, 9, 55.4, 0.254}, Random: 0.254,
		},
	)
	return &ranking
}

func testRanking2() *model.Ranking {
	ranking := model.NewRanking(
		scoring.TeamRanking{
			TeamID: 1114, Rank: 2, RP: 18, Played: 10, Wins: 1, Losses: 3, Ties: 2,
			Stats: map[string]float64{"match_points": 700, "auto_fuel_points": 625, "tower_points": 90},
			Sort:  []float64{1.8, 70, 62.5, 9, 0.1114}, Random: 0.1114,
		},
	)
	return &ranking
}

// setAllStationsBypassed bypasses every station so that a match can be started without robots.
func setAllStationsBypassed(web *Web) {
	for _, allianceStation := range web.arena.AllianceStations {
		allianceStation.Bypass = true
	}
}

// endTestMatch moves the clock of a running match to its end and lets the arena notice.
func endTestMatch(web *Web) {
	duration := time.Duration(web.arena.Game.Duration() * float64(time.Second))
	web.arena.MatchStartTime = time.Now().Add(-duration - 10*time.Millisecond)
	web.arena.Update()
}

// playTestMatch starts a match with every station bypassed, enters its first phase and returns.
func startTestMatch(t *testing.T, web *Web) {
	setAllStationsBypassed(web)
	assert.Nil(t, web.arena.StartMatch())
	web.arena.Update()
	assert.Equal(t, field.InMatch, web.arena.MatchState)
}
