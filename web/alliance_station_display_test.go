// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestAllianceStationDisplay(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/displays/alliance_station")
	assert.Equal(t, 302, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "displayId=100")
	assert.Contains(t, recorder.Header().Get("Location"), "station=R1")

	recorder = web.getHttpResponse("/displays/alliance_station?displayId=1&station=B1")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Alliance Station Display - Untitled Event - Galaxia FMS")
}

func TestAllianceStationDisplayWebsocket(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/displays/alliance_station/websocket?displayId=1", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketType(t, ws, "displayConfiguration")
	readWebsocketType(t, ws, "game")
	readWebsocketType(t, ws, "allianceStationDisplayMode")
	readWebsocketType(t, ws, "arenaStatus")
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
	readWebsocketType(t, ws, "realtimeScore")

	// Change to a different screen.
	web.arena.AllianceStationDisplayMode = "logo"
	web.arena.AllianceStationDisplayModeNotifier.Notify()
	readWebsocketType(t, ws, "allianceStationDisplayMode")

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
	messages := readWebsocketMultiple(t, ws, 3)
	_, ok := messages["matchTime"]
	assert.True(t, ok)
	// Move the clock to the end of the first phase of the game; the display is told about the next one.
	firstPhaseDuration := time.Duration(web.arena.Game.Match.Phases[0].Duration * float64(time.Second))
	web.arena.MatchStartTime = time.Now().Add(-firstPhaseDuration)
	web.arena.Update()
	matchTime := readWebsocketUntil(t, ws, "matchTime").(map[string]any)
	assert.Equal(t, float64(1), matchTime["PhaseIndex"])
	assert.Equal(t, web.arena.Game.Match.Phases[1].ID, matchTime["PhaseId"])
	web.arena.RealtimeScoreNotifier.Notify()
	readWebsocketUntil(t, ws, "realtimeScore")
}
