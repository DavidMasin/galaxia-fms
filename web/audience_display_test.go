// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestAudienceDisplay(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/displays/audience")
	assert.Equal(t, 302, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "displayId=100")
	assert.Contains(t, recorder.Header().Get("Location"), "background=%230f0")
	assert.Contains(t, recorder.Header().Get("Location"), "reversed=false")
	assert.Contains(t, recorder.Header().Get("Location"), "overlayLocation=bottom")
	assert.Contains(t, recorder.Header().Get("Location"), "zoomFactor=1")

	recorder = web.getHttpResponse(
		"/displays/audience?displayId=1&background=%23000&reversed=false&overlayLocation=top",
	)
	assert.Equal(t, 302, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "overlayLocation=top")
	assert.Contains(t, recorder.Header().Get("Location"), "zoomFactor=1")

	recorder = web.getHttpResponse(
		"/displays/audience?displayId=1&background=%23000&reversed=false&overlayLocation=top&zoomFactor=1.5",
	)
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Audience Display - Untitled Event - Galaxia FMS")
	assert.Contains(t, recorder.Body.String(), "finalTiebreakReason")
}

func TestAudienceDisplayWebsocket(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/displays/audience/websocket?displayId=1", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketType(t, ws, "displayConfiguration")
	readWebsocketType(t, ws, "game")
	readWebsocketType(t, ws, "audienceDisplayMode")
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
	readWebsocketType(t, ws, "realtimeScore")
	readWebsocketType(t, ws, "scorePosted")
	readWebsocketType(t, ws, "allianceSelection")
	readWebsocketType(t, ws, "lowerThird")

	// Run through a match cycle.
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
	web.arena.Update()
	// Starting the match switches the display to the match screen, plays the first cue of the game, starts the
	// clock and, because the game has a value that depends on the clock, sends the score again.
	messages := readWebsocketMultiple(t, ws, 4)
	screen, ok := messages["audienceDisplayMode"]
	if assert.True(t, ok) {
		assert.Equal(t, "match", screen)
	}
	sound, ok := messages["playSound"]
	if assert.True(t, ok) {
		assert.Equal(t, "start", sound)
	}
	_, ok = messages["matchTime"]
	assert.True(t, ok)
	_, ok = messages["realtimeScore"]
	assert.True(t, ok)
	web.arena.RealtimeScoreNotifier.Notify()
	readWebsocketType(t, ws, "realtimeScore")

	// A tied playoff result of the rebuilt-style game is decided by the game's tiebreakers: blue committed a major
	// foul, which gives each alliance fifteen points and the red alliance the match.
	g := model.TestGame("rebuilt-style")
	matchResult := model.NewMatchResult(g)
	matchResult.MatchType = model.Playoff
	for _, change := range []scoring.Change{
		{Alliance: "red", Element: "fuel", Op: scoring.OpSet, Value: 0.0, Bucket: "auto"},
		{Alliance: "blue", Robot: 1, Element: "auto_tower", Op: scoring.OpSet, Value: "level1", Bucket: "auto"},
		{Alliance: "blue", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "major"},
	} {
		change.Source = scoring.Source{Kind: scoring.SourceAdmin}
		_, err = matchResult.Edit(g, change)
		assert.Nil(t, err)
	}
	web.arena.SavedMatch = &model.Match{Type: model.Playoff, Status: model.RedWonMatch, UseTiebreakCriteria: true}
	web.arena.SavedMatchResult = matchResult
	web.arena.ScorePostedNotifier.Notify()
	scorePosted := readWebsocketType(t, ws, "scorePosted").(map[string]any)
	assert.Equal(t, "rebuilt-style", scorePosted["gameId"])
	assert.Equal(t, "red", scorePosted["winner"])
	assert.Equal(t, "Major fouls by the opponent", scorePosted["tiebreak"])
	alliances := scorePosted["alliances"].(map[string]any)
	assert.Equal(t, 15.0, alliances["red"].(map[string]any)["score"])
	assert.Equal(t, 15.0, alliances["blue"].(map[string]any)["score"])

	// The parameters the result was played with go with it: a win is worth three ranking points while the
	// traversal bonus exists and two when the event turned it off.
	winRankingPoints := func(message map[string]any) float64 {
		for _, rankingPoint := range message["alliances"].(map[string]any)["red"].(map[string]any)["rankingPoints"].([]any) {
			if rankingPoint.(map[string]any)["id"] == "win" {
				return rankingPoint.(map[string]any)["value"].(float64)
			}
		}
		return -1
	}
	for _, threshold := range []float64{0, 50, 0, 42} {
		matchResult.Params["traversal_threshold"] = threshold
		web.arena.ScorePostedNotifier.Notify()
		scorePosted = readWebsocketType(t, ws, "scorePosted").(map[string]any)
		if threshold == 0 {
			assert.Equal(t, 2.0, winRankingPoints(scorePosted))
		} else {
			assert.Equal(t, 3.0, winRankingPoints(scorePosted))
		}
	}

	// Test other overlays.
	web.arena.AllianceSelectionNotifier.Notify()
	readWebsocketType(t, ws, "allianceSelection")
	web.arena.LowerThirdNotifier.Notify()
	readWebsocketType(t, ws, "lowerThird")
}
