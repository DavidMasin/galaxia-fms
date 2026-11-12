// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// liveState returns the raw score state of the match on the field.
func liveState(t *testing.T, web *Web) *scoring.State {
	matchResult, err := web.arena.BuildMatchResult()
	assert.Nil(t, err)
	return matchResult.State
}

// scoreData builds the data of a "score" command.
func scoreData(alliance string, robot int, element, op string, value any, bucket string) map[string]any {
	data := map[string]any{"alliance": alliance, "robot": robot, "element": element, "op": op}
	if value != nil {
		data["value"] = value
	}
	if bucket != "" {
		data["bucket"] = bucket
	}
	return data
}

func TestScoringPanel(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/panels/scoring/invalidposition")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Invalid position")

	// The positions are the scoring panels the active game lays out; the rebuilt-style example has four.
	for position, name := range map[string]string{
		"red_near": "Red near side", "red_far": "Red far side", "blue_near": "Blue near side",
		"blue_far": "Blue far side",
	} {
		recorder = web.getHttpResponse("/panels/scoring/" + position)
		assert.Equal(t, 200, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "Scoring Panel - Untitled Event - Galaxia FMS")
		assert.Contains(t, recorder.Body.String(), name)
		// The page is built in the browser from the game, so it loads the shared helpers and names no element.
		assert.Contains(t, recorder.Body.String(), "/static/js/game_view.js")
		assert.Contains(t, recorder.Body.String(), "/static/js/score_controls.js")
		assert.Contains(t, recorder.Body.String(), "/panels/scoring/"+position+"/manifest")
		assert.NotContains(t, recorder.Body.String(), "Tower")
	}
	recorder = web.getHttpResponse("/panels/scoring/red")
	assert.Equal(t, 500, recorder.Code)

	// A tablet can pin a position to its home screen.
	recorder = web.getHttpResponse("/panels/scoring/blue_far/manifest")
	assert.Equal(t, 200, recorder.Code)
	var manifest map[string]any
	assert.Nil(t, json.Unmarshal(recorder.Body.Bytes(), &manifest))
	assert.Equal(t, "Blue far side Scoring", manifest["name"])
	assert.Equal(t, "/panels/scoring/blue_far", manifest["start_url"])
	assert.Equal(t, 404, web.getHttpResponse("/panels/scoring/nowhere/manifest").Code)

	// A game that lays out no panels gets one per alliance, and one for the field only if it scores anything there.
	selectTestGame(t, web, "ball-count")
	assert.Equal(t, 200, web.getHttpResponse("/panels/scoring/red").Code)
	recorder = web.getHttpResponse("/panels/scoring/blue")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Blue")
	assert.Equal(t, 500, web.getHttpResponse("/panels/scoring/red_near").Code)
	assert.Equal(t, 500, web.getHttpResponse("/panels/scoring/field").Code)

	// The navigation bar lists the panels of the active game.
	recorder = web.getHttpResponse("/")
	assert.Contains(t, recorder.Body.String(), `href="/panels/scoring/red">Red</a>`)
	assert.Contains(t, recorder.Body.String(), `href="/panels/scoring/blue">Blue</a>`)
	assert.NotContains(t, recorder.Body.String(), "/panels/scoring/red_near")
}

func TestScoringPanelWebsocket(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	_, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/scoring/blorpy/websocket", nil)
	assert.NotNil(t, err)
	redConn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/scoring/red_near/websocket", nil)
	assert.Nil(t, err)
	defer redConn.Close()
	redWs := websocket.NewTestWebsocket(redConn)
	assert.Equal(t, 1, web.arena.ScoringPanelRegistry.GetNumPanels("red_near"))
	assert.Equal(t, 0, web.arena.ScoringPanelRegistry.GetNumPanels("blue_near"))
	blueConn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/scoring/blue_near/websocket", nil)
	assert.Nil(t, err)
	defer blueConn.Close()
	blueWs := websocket.NewTestWebsocket(blueConn)
	assert.Equal(t, 1, web.arena.ScoringPanelRegistry.GetNumPanels("red_near"))
	assert.Equal(t, 1, web.arena.ScoringPanelRegistry.GetNumPanels("blue_near"))

	// Should get a few status updates right after connection, the game before anything that is made of it.
	for _, ws := range []*websocket.Websocket{redWs, blueWs} {
		readWebsocketType(t, ws, "resetLocalState")
		gameView := readWebsocketType(t, ws, "game").(map[string]any)
		assert.Equal(t, "rebuilt-style", gameView["id"])
		readWebsocketType(t, ws, "matchLoad")
		readWebsocketType(t, ws, "matchTime")
		realtimeScore := readWebsocketType(t, ws, "realtimeScore").(map[string]any)
		assert.Equal(t, web.arena.Game.Hash, realtimeScore["gameHash"])
		assert.Equal(t, 0.0, realtimeScore["seq"])
	}

	// Nothing can be scored before the match has started, and the panel is told why.
	redWs.Write("score", scoreData("red", 1, "auto_tower", "set", "level1", ""))
	assert.Contains(t, readWebsocketError(t, redWs), "the match has not started")
	redWs.Write("commitMatch", nil)
	assert.Contains(t, readWebsocketError(t, redWs), "Cannot commit score: Match is not over.")

	// During the first phase the scorers set what the robots did in it.
	startTestMatch(t, web)
	redWs.Write("score", scoreData("red", 1, "auto_tower", "set", "level1", ""))
	change := readWebsocketUntil(t, redWs, "scoreChange").(map[string]any)["change"].(map[string]any)
	assert.Equal(t, "auto_tower", change["element"])
	assert.Equal(t, "auto", change["phase"])
	assert.Equal(t, "scorer", change["source"].(map[string]any)["kind"])
	assert.Equal(t, "red_near", change["source"].(map[string]any)["id"])
	firstSeq := int(change["seq"].(float64))
	blueWs.Write("score", scoreData("blue", 2, "auto_tower", "set", "level3", ""))
	readWebsocketUntil(t, redWs, "scoreChange")
	redWs.Write("score", scoreData("red", 3, "auto_tower", "set", "level2", ""))
	readWebsocketUntil(t, redWs, "scoreChange")
	state := liveState(t, web)
	assert.Equal(t, "level1", state.Alliances["red"].Robots[0]["auto_tower"].Option)
	assert.Equal(t, "none", state.Alliances["red"].Robots[1]["auto_tower"].Option)
	assert.Equal(t, "level2", state.Alliances["red"].Robots[2]["auto_tower"].Option)
	assert.Equal(t, "level3", state.Alliances["blue"].Robots[1]["auto_tower"].Option)

	// Both panels are sent the new score after each change; the raw state is in it for them to draw from.
	for {
		realtimeScore := readWebsocketUntil(t, blueWs, "realtimeScore").(map[string]any)
		if realtimeScore["seq"].(float64) < 3 {
			// An earlier score, sent when the match started or after one of the changes before the last.
			continue
		}
		redState := realtimeScore["alliances"].(map[string]any)["red"].(map[string]any)["state"].(map[string]any)
		robots := redState["robots"].([]any)
		assert.Equal(t, "level1", robots[0].(map[string]any)["auto_tower"].(map[string]any)["option"])
		assert.Equal(t, "level2", robots[2].(map[string]any)["auto_tower"].(map[string]any)["option"])
		assert.Equal(t, 30.0, realtimeScore["alliances"].(map[string]any)["red"].(map[string]any)["score"])
		break
	}

	// A counter is stepped, and counted in the phase that is running.
	redWs.Write("score", scoreData("red", 0, "fuel", "increment", nil, ""))
	readWebsocketUntil(t, redWs, "scoreChange")
	redWs.Write("score", scoreData("red", 0, "fuel", "increment", 4, ""))
	readWebsocketUntil(t, redWs, "scoreChange")
	redWs.Write("score", scoreData("red", 0, "fuel", "decrement", nil, ""))
	readWebsocketUntil(t, redWs, "scoreChange")
	assert.Equal(t, map[string]int{"auto": 4}, liveState(t, web).Alliances["red"].Elements["fuel"].Counts)
	result, _, _ := web.arena.ScoreSnapshot()
	assert.Equal(t, 4.0+30.0, result.Alliances["red"].Score)

	// A change can be undone, once, and every panel is told that it is no longer in effect.
	redWs.Write("undo", map[string]any{"seq": firstSeq})
	undone := readWebsocketUntil(t, blueWs, "scoreChange").(map[string]any)
	for undone["undone"] != true {
		// The changes made so far come first, each as it was when it was made.
		undone = readWebsocketUntil(t, blueWs, "scoreChange").(map[string]any)
	}
	assert.Equal(t, float64(firstSeq), undone["change"].(map[string]any)["seq"])
	assert.Equal(t, "none", liveState(t, web).Alliances["red"].Robots[0]["auto_tower"].Option)
	readWebsocketUntil(t, redWs, "scoreChange")
	redWs.Write("undo", map[string]any{"seq": firstSeq})
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "already been undone")
	redWs.Write("undo", map[string]any{"seq": 999})
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "no score change with sequence number 999")

	// Invalid commands are refused with a reason and change nothing.
	seqBefore := len(mustBuildMatchResult(t, web).Changes)
	redWs.Write("invalid", nil)
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "Invalid message type 'invalid'.")
	redWs.Write("score", scoreData("red", 1, "auto_tower", "set", "level9", ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "has no option")
	redWs.Write("score", scoreData("red", 4, "auto_tower", "set", "level1", ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "out of range")
	redWs.Write("score", scoreData("red", 0, "nonexistent", "increment", nil, ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "there is no element")
	redWs.Write("score", scoreData("green", 0, "fuel", "increment", nil, ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "there is no alliance")
	// Fouls and cards are the referees' to give.
	redWs.Write("score", scoreData("red", 0, "foul", "add", "major", ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "foul is not one")
	redWs.Write("score", scoreData("red", 1, "card", "set", "yellow", ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "card is not one")
	assert.Equal(t, seqBefore, len(mustBuildMatchResult(t, web).Changes))

	// Once its phase is well over, an element that only accepts that phase is closed to the scorers, while the
	// others stay open. A scorer cannot name another phase to get around that.
	web.arena.MatchStartTime = time.Now().Add(-40 * time.Second)
	web.arena.Update()
	assert.Equal(t, "shift1", web.arena.CurrentPhase().ID)
	redWs.Write("score", scoreData("red", 2, "auto_tower", "set", "level1", "auto"))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "does not accept changes during the shift1 phase")
	redWs.Write("score", scoreData("red", 1, "endgame_tower", "set", "level2", ""))
	redWs.Write("score", scoreData("red", 3, "endgame_tower", "set", "level3", ""))
	blueWs.Write("score", scoreData("blue", 2, "endgame_tower", "set", "level3", ""))
	blueWs.Write("score", scoreData("blue", 3, "endgame_tower", "set", "level1", ""))
	blueWs.Write("commitMatch", nil)
	assert.Contains(t, readWebsocketUntil(t, blueWs, "error"), "Cannot commit score: Match is not over.")
	assert.Eventually(
		t,
		func() bool {
			state := liveState(t, web)
			return state.Alliances["red"].Robots[2]["endgame_tower"].Option == "level3" &&
				state.Alliances["blue"].Robots[2]["endgame_tower"].Option == "level1"
		},
		time.Second,
		10*time.Millisecond,
	)
	state = liveState(t, web)
	assert.Equal(t, "level2", state.Alliances["red"].Robots[0]["endgame_tower"].Option)
	assert.Equal(t, "none", state.Alliances["red"].Robots[1]["endgame_tower"].Option)
	assert.Equal(t, "level3", state.Alliances["blue"].Robots[1]["endgame_tower"].Option)

	// After the match everything can be put right, including what was closed. A counter is kept per phase, so the
	// scorer has to say which phase a correction belongs to.
	endTestMatch(web)
	assert.Equal(t, field.PostMatch, web.arena.MatchState)
	redWs.Write("score", scoreData("red", 2, "auto_tower", "set", "level1", ""))
	redWs.Write("score", scoreData("red", 0, "fuel", "increment", 2, ""))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "say which phase this change is credited to")
	redWs.Write("score", scoreData("red", 0, "fuel", "increment", 2, "nonexistent"))
	assert.Contains(t, readWebsocketUntil(t, redWs, "error"), "is not a phase of this game")
	redWs.Write("score", scoreData("red", 0, "fuel", "set", 7, "endgame"))
	assert.Eventually(
		t,
		func() bool {
			return liveState(t, web).Alliances["red"].Elements["fuel"].Counts["endgame"] == 7
		},
		time.Second,
		10*time.Millisecond,
	)
	state = liveState(t, web)
	assert.Equal(t, "level1", state.Alliances["red"].Robots[1]["auto_tower"].Option)
	assert.Equal(t, map[string]int{"auto": 4, "endgame": 7}, state.Alliances["red"].Elements["fuel"].Counts)

	// Test committing logic.
	redWs.Write("commitMatch", nil)
	blueWs.Write("commitMatch", nil)
	assert.Eventually(
		t,
		func() bool {
			return web.arena.ScoringPanelRegistry.GetNumScoreCommitted("red_near") == 1 &&
				web.arena.ScoringPanelRegistry.GetNumScoreCommitted("blue_near") == 1
		},
		time.Second,
		10*time.Millisecond,
	)

	// A panel that connects late, or again, is told what has been scored so that it can offer to undo it.
	lateConn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/scoring/red_far/websocket", nil)
	assert.Nil(t, err)
	defer lateConn.Close()
	lateWs := websocket.NewTestWebsocket(lateConn)
	readWebsocketType(t, lateWs, "resetLocalState")
	var history []map[string]any
	for {
		messageType, message, err := lateWs.ReadWithTimeout(time.Second)
		assert.Nil(t, err)
		if messageType != "scoreChange" {
			assert.Equal(t, "game", messageType)
			break
		}
		history = append(history, message.(map[string]any))
	}
	changes := mustBuildMatchResult(t, web).Changes
	scoringChanges := 0
	for _, change := range changes {
		if change.Reverts == 0 {
			scoringChanges++
		}
	}
	assert.Equal(t, min(scoringChanges, scoreChangeHistoryLength), len(history))
	// The first change of the match was undone; the last one is in effect.
	assert.Equal(t, float64(firstSeq), history[0]["change"].(map[string]any)["seq"])
	assert.Equal(t, true, history[0]["undone"])
	assert.Equal(t, false, history[len(history)-1]["undone"])
	assert.Equal(t, 1, web.arena.ScoringPanelRegistry.GetNumPanels("red_far"))

	// Load another match to reset the results.
	web.arena.ResetMatch()
	web.arena.LoadTestMatch()
	readWebsocketUntil(t, redWs, "matchLoad")
	assert.Equal(t, 0, web.arena.ScoringPanelRegistry.GetNumScoreCommitted("red_near"))
	assert.Equal(t, 0, web.arena.ScoringPanelRegistry.GetNumScoreCommitted("blue_near"))
	assert.Equal(t, 0, len(mustBuildMatchResult(t, web).Changes))
}

func TestScoringPanelWebsocketBallCount(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/scoring/blue/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)
	readWebsocketType(t, ws, "resetLocalState")
	gameView := readWebsocketType(t, ws, "game").(map[string]any)
	assert.Equal(t, "ball-count", gameView["id"])
	// The game lays out no panels; the browser works out the same default ones the server does.
	assert.Equal(t, 0, len(gameView["panels"].([]any)))

	// A ball is worth two points in autonomous and one in teleop.
	startTestMatch(t, web)
	for i := 0; i < 3; i++ {
		ws.Write("score", scoreData("blue", 0, "balls", "increment", nil, ""))
		readWebsocketUntil(t, ws, "scoreChange")
	}
	web.arena.MatchStartTime = time.Now().Add(-30 * time.Second)
	web.arena.Update()
	assert.Equal(t, "teleop", web.arena.CurrentPhase().ID)
	ws.Write("score", scoreData("blue", 0, "balls", "increment", 5, ""))
	readWebsocketUntil(t, ws, "scoreChange")
	assert.Equal(t, map[string]int{"auto": 3, "teleop": 5}, liveState(t, web).Alliances["blue"].Elements["balls"].Counts)
	result, _, _ := web.arena.ScoreSnapshot()
	assert.Equal(t, 11.0, result.Alliances["blue"].Score)
	assert.Equal(t, "blue", result.Winner)
}

// mustBuildMatchResult returns the result of the match on the field as it stands.
func mustBuildMatchResult(t *testing.T, web *Web) *model.MatchResult {
	matchResult, err := web.arena.BuildMatchResult()
	assert.Nil(t, err)
	return matchResult
}
