// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRefereePanel(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/panels/referee")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Referee Panel - Untitled Event - Galaxia FMS")
	// The panel is built in the browser from the game, so the page names nothing of any game.
	assert.Contains(t, recorder.Body.String(), "/static/js/game_view.js")
	assert.Contains(t, recorder.Body.String(), "/static/js/score_controls.js")
	assert.NotContains(t, recorder.Body.String(), "Tower")
	assert.NotContains(t, recorder.Body.String(), "Major")
}

func TestResolveCard(t *testing.T) {
	model.BaseDir = ".."
	g := model.TestGame("rebuilt-style")

	for _, test := range []struct {
		name      string
		requested string
		current   string
		carried   bool
		expected  string
		refused   bool
	}{
		{"clearing always works", "", "red", true, "", false},
		{"first card", "yellow", "", false, "yellow", false},
		{"same card again in the match becomes the second card", "yellow", "yellow", false, "red", false},
		{"card on top of a carried one becomes the second card", "yellow", "", true, "red", false},
		{"disqualifying card cannot be the first card", "red", "", false, "", true},
		{"disqualifying card for a team that has a card in this match", "red", "yellow", false, "red", false},
		{"disqualifying card for a team that carries a card", "red", "", true, "red", false},
		{"disqualifying card held again", "red", "red", false, "red", false},
		{"a lesser card replaces the disqualifying one", "yellow", "red", false, "yellow", false},
		{"unknown card", "purple", "", false, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			card, err := resolveCard(g, test.requested, test.current, test.carried)
			if test.refused {
				assert.NotNil(t, err)
			} else if assert.Nil(t, err) {
				assert.Equal(t, test.expected, card)
			}
		})
	}

	// A game without cards has none to give.
	_, err := resolveCard(model.TestGame("ball-count"), "yellow", "", false)
	assert.NotNil(t, err)
}

func TestRefereePanelWebsocket(t *testing.T) {
	web := setupTestWeb(t)
	// Team 1680 comes into the match with a card from an earlier one.
	assert.Nil(t, web.arena.Database.CreateTeam(&model.Team{Id: 256}))
	assert.Nil(t, web.arena.Database.CreateTeam(&model.Team{Id: 1680, YellowCard: true}))
	assert.Nil(
		t,
		web.arena.LoadMatch(
			&model.Match{Type: model.Test, ShortName: "T", Red1: 256, Red2: 257, Red3: 258, Blue1: 1680, Blue2: 1681},
		),
	)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/referee/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection, the game first.
	gameView := readWebsocketType(t, ws, "game").(map[string]any)
	assert.Equal(t, "rebuilt-style", gameView["id"])
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
	readWebsocketType(t, ws, "realtimeScore")
	readWebsocketType(t, ws, "scoringStatus")
	readWebsocketType(t, ws, "arenaStatus")

	// Nothing can be called before the match has started.
	ws.Write("foul", map[string]any{"alliance": "red", "type": "major"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "the match has not started")

	// Test foul addition. A foul has the type the referee picked and nothing else until it is filled in.
	startTestMatch(t, web)
	ws.Write("foul", map[string]any{"alliance": "red", "type": "major"})
	ws.Write("foul", map[string]any{"alliance": "blue", "type": "minor"})
	ws.Write("foul", map[string]any{"alliance": "red", "type": "minor"})
	ws.Write("foul", map[string]any{"alliance": "red", "type": "nonexistent"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no foul type")
	state := liveState(t, web)
	if assert.Equal(t, 2, len(state.Alliances["red"].Fouls)) {
		assert.Equal(t, "major", state.Alliances["red"].Fouls[0].Type)
		assert.Equal(t, 0, state.Alliances["red"].Fouls[0].Robot)
		assert.Equal(t, "", state.Alliances["red"].Fouls[0].Rule)
		assert.Equal(t, "auto", state.Alliances["red"].Fouls[0].Phase)
		assert.Equal(t, "minor", state.Alliances["red"].Fouls[1].Type)
	}
	if assert.Equal(t, 1, len(state.Alliances["blue"].Fouls)) {
		assert.Equal(t, "minor", state.Alliances["blue"].Fouls[0].Type)
	}
	result, _, _ := web.arena.ScoreSnapshot()
	assert.Equal(t, 5.0, result.Alliances["red"].Score)
	assert.Equal(t, 20.0, result.Alliances["blue"].Score)
	assert.False(t, web.arena.FoulsCommitted("red"))
	assert.False(t, web.arena.FoulsCommitted("blue"))
	assert.False(t, web.arena.RefereeScoreReady())
	redMajorId := state.Alliances["red"].Fouls[0].ID
	redMinorId := state.Alliances["red"].Fouls[1].ID
	blueMinorId := state.Alliances["blue"].Fouls[0].ID

	// The list of fouls offers the game's foul types, the teams of the alliance and the rules for the foul's type.
	recorder := web.getHttpResponse("/panels/referee/foul_list")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `<option value="major" selected>Major foul</option>`)
	assert.Contains(t, recorder.Body.String(), `<option value="collusion">Collusion</option>`)
	assert.Contains(t, recorder.Body.String(), "G403 [Major foul]: Auto contact across the centre line")
	assert.Contains(t, recorder.Body.String(), "G418 [Major foul]: Extended pinning")
	assert.Contains(t, recorder.Body.String(), "G418 [Foul]: Pinning")
	assert.Contains(t, recorder.Body.String(), "256")
	assert.Contains(t, recorder.Body.String(), "1681")
	// The third station of the second alliance has no team, so it is offered by its position.
	assert.Contains(t, recorder.Body.String(), "#3")

	// Test foul mutation. A foul is corrected by replacing it, so it gets a new id each time.
	ws.Write("foul", map[string]any{"alliance": "red", "update": redMinorId, "type": "major"})
	ws.Write("foul", map[string]any{"alliance": "red", "update": redMajorId, "type": "major", "robot": 1})
	ws.Write("foul", map[string]any{"alliance": "blue", "update": blueMinorId, "type": "minor", "rule": "G421"})
	ws.Write("foul", map[string]any{"alliance": "blue", "update": 999, "type": "minor"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no foul 999")
	state = liveState(t, web)
	if assert.Equal(t, 2, len(state.Alliances["red"].Fouls)) {
		assert.Equal(t, "major", state.Alliances["red"].Fouls[0].Type)
		assert.Equal(t, 0, state.Alliances["red"].Fouls[0].Robot)
		assert.NotEqual(t, redMinorId, state.Alliances["red"].Fouls[0].ID)
		assert.Equal(t, "major", state.Alliances["red"].Fouls[1].Type)
		assert.Equal(t, 1, state.Alliances["red"].Fouls[1].Robot)
	}
	if assert.Equal(t, 1, len(state.Alliances["blue"].Fouls)) {
		assert.Equal(t, "G421", state.Alliances["blue"].Fouls[0].Rule)
	}
	blueMinorId = state.Alliances["blue"].Fouls[0].ID

	// A rule that the game does not list for the foul's type is not kept: G403 is a major foul only.
	ws.Write("foul", map[string]any{"alliance": "blue", "update": blueMinorId, "type": "minor", "rule": "G403"})
	// Changing the robot back to none and the rule to one that fits.
	ws.Write("foul", map[string]any{"alliance": "blue", "type": "major", "rule": "G403", "robot": 2})
	ws.Write("foul", map[string]any{"alliance": "blue", "type": "minor", "robot": 9})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "out of range")
	state = liveState(t, web)
	if assert.Equal(t, 2, len(state.Alliances["blue"].Fouls)) {
		assert.Equal(t, "", state.Alliances["blue"].Fouls[0].Rule)
		assert.Equal(t, "G403", state.Alliances["blue"].Fouls[1].Rule)
		assert.Equal(t, 2, state.Alliances["blue"].Fouls[1].Robot)
	}

	// Test foul deletion.
	ws.Write("foul", map[string]any{"alliance": "blue", "remove": state.Alliances["blue"].Fouls[0].ID})
	ws.Write("foul", map[string]any{"alliance": "blue", "remove": state.Alliances["blue"].Fouls[1].ID})
	ws.Write("foul", map[string]any{"alliance": "red", "remove": 999})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no foul 999")
	// A foul belongs to the alliance it was called against.
	ws.Write("foul", map[string]any{"alliance": "blue", "remove": state.Alliances["red"].Fouls[0].ID})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no foul")
	ws.Write("foul", map[string]any{"alliance": "red", "remove": state.Alliances["red"].Fouls[0].ID})
	ws.Write("signalReset", nil)
	assert.Eventually(
		t, func() bool { return len(liveState(t, web).Alliances["red"].Fouls) == 1 }, time.Second, 10*time.Millisecond,
	)
	state = liveState(t, web)
	assert.Equal(t, 0, len(state.Alliances["blue"].Fouls))
	assert.Equal(t, 1, state.Alliances["red"].Fouls[0].Robot)
	// Field reset is not signalled while a match is running.
	assert.Equal(t, "match", web.arena.AllianceStationDisplayMode)

	// A referee may change the elements that list referees, and no others while the match runs.
	ws.Write("score", scoreData("red", 2, "endgame_tower", "set", "level2", ""))
	change := readWebsocketUntil(t, ws, "scoreChange").(map[string]any)["change"].(map[string]any)
	for change["element"] != "endgame_tower" {
		change = readWebsocketUntil(t, ws, "scoreChange").(map[string]any)["change"].(map[string]any)
	}
	assert.Equal(t, "referee", change["source"].(map[string]any)["kind"])
	assert.Equal(t, "level2", liveState(t, web).Alliances["red"].Robots[1]["endgame_tower"].Option)
	ws.Write("score", scoreData("red", 0, "fuel", "increment", nil, ""))
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "fuel cannot be changed by a referee during the match")
	ws.Write("undo", map[string]any{"seq": int(change["seq"].(float64))})
	assert.Eventually(
		t,
		func() bool { return liveState(t, web).Alliances["red"].Robots[1]["endgame_tower"].Option == "none" },
		time.Second,
		10*time.Millisecond,
	)
	// The dedicated commands are the only way to a foul or a card.
	ws.Write("score", scoreData("red", 1, "card", "set", "red", ""))
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "card is not one")

	// Committing is ignored until the match is over.
	ws.Write("commitAndPost", nil)
	ws.Write("toggleBypass", "R1")
	assert.Eventually(t, func() bool { return !web.arena.AllianceStations["R1"].Bypass }, time.Second, 10*time.Millisecond)
	assert.False(t, web.arena.RefereeScoreReady())
	assert.Equal(t, field.InMatch, web.arena.MatchState)
	ws.Write("toggleBypass", "X9")
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "Invalid alliance station")

	// Test card logic. Team 256 has no card: the first card it is given stands, and the disqualifying card cannot
	// be its first.
	endTestMatch(web)
	card := func(alliance string, robot int) string {
		return liveState(t, web).Alliances[alliance].Cards[robot]
	}
	ws.Write("card", map[string]any{"alliance": "red", "robot": 1, "card": "red"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "Red card can only be given to a team that already has a card")
	assert.Equal(t, "", card("red", 1))
	ws.Write("card", map[string]any{"alliance": "red", "robot": 1, "card": "yellow"})
	assert.Eventually(t, func() bool { return card("red", 1) == "yellow" }, time.Second, 10*time.Millisecond)
	// The same card a second time in the match becomes the card the game names for that.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 1, "card": "yellow"})
	assert.Eventually(t, func() bool { return card("red", 1) == "red" }, time.Second, 10*time.Millisecond)
	ws.Write("card", map[string]any{"alliance": "red", "robot": 1, "card": ""})
	assert.Eventually(t, func() bool { return card("red", 1) == "" }, time.Second, 10*time.Millisecond)
	// With a card in hand, the disqualifying card can be given directly.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 2, "card": "yellow"})
	ws.Write("card", map[string]any{"alliance": "red", "robot": 2, "card": "red"})
	assert.Eventually(t, func() bool { return card("red", 2) == "red" }, time.Second, 10*time.Millisecond)

	// Team 1680 carries a card from an earlier match: another one becomes the second card straight away.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 1, "card": "yellow"})
	assert.Eventually(t, func() bool { return card("blue", 1) == "red" }, time.Second, 10*time.Millisecond)
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 1, "card": ""})
	assert.Eventually(t, func() bool { return card("blue", 1) == "" }, time.Second, 10*time.Millisecond)
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 1, "card": "red"})
	assert.Eventually(t, func() bool { return card("blue", 1) == "red" }, time.Second, 10*time.Millisecond)
	// Its partner does not carry one.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 2, "card": "yellow"})
	assert.Eventually(t, func() bool { return card("blue", 2) == "yellow" }, time.Second, 10*time.Millisecond)

	// Requests that make no sense are refused.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 2, "card": "purple"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no card")
	ws.Write("card", map[string]any{"alliance": "green", "robot": 2, "card": "yellow"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "there is no alliance")
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 7, "card": "yellow"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "out of range")
	// Only a playoff alliance can be given a card as a whole.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 0, "card": "yellow"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "only in a playoff match")

	// In a match that is not a playoff match a disqualifying card costs the team, not the alliance.
	result, _, _ = web.arena.ScoreSnapshot()
	assert.False(t, result.Alliances["red"].Disqualified)
	assert.False(t, result.Alliances["blue"].Disqualified)

	// Test field reset and match committing.
	web.arena.AllianceStationDisplayMode = "logo"
	ws.Write("signalReset", nil)
	assert.Eventually(
		t, func() bool { return web.arena.AllianceStationDisplayMode == "fieldReset" }, time.Second, 10*time.Millisecond,
	)
	assert.False(t, web.arena.FoulsCommitted("red"))
	assert.False(t, web.arena.FoulsCommitted("blue"))
	web.arena.AllianceStationDisplayMode = "logo"
	ws.Write("signalVolunteers", nil)
	assert.Eventually(
		t, func() bool { return web.arena.AllianceStationDisplayMode == "signalCount" }, time.Second, 10*time.Millisecond,
	)
	ws.Write("commitAndPost", nil)
	assert.Eventually(t, func() bool { return web.arena.AudienceDisplayMode == "score" }, time.Second, 10*time.Millisecond)
	// The committed match went to the final score buffer with its fouls and cards, and a new match is loaded.
	assert.Equal(t, "T", web.arena.SavedMatch.ShortName)
	assert.Equal(t, 1, len(web.arena.SavedMatchResult.State.Alliances["red"].Fouls))
	assert.Equal(t, map[int]string{2: "red"}, web.arena.SavedMatchResult.State.Alliances["red"].Cards)
	assert.Equal(t, map[int]string{257: "red"}, web.arena.SavedMatchResult.TeamCards("red"))
	assert.Equal(t, field.PreMatch, web.arena.MatchState)
	assert.Equal(t, 0, len(liveState(t, web).Alliances["red"].Fouls))
}

func TestRefereePanelWebsocketPlayoffCards(t *testing.T) {
	web, match := setupPlayoffCardTest(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/panels/referee/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)
	readWebsocketUntil(t, ws, "arenaStatus")

	startTestMatch(t, web)
	endTestMatch(web)
	cards := func(alliance string) map[int]string {
		return liveState(t, web).Alliances[alliance].Cards
	}
	disqualified := func(alliance string) bool {
		result, _, _ := web.arena.ScoreSnapshot()
		return result.Alliances[alliance].Disqualified
	}

	// A playoff alliance is given a card as a whole: every one of its robot positions holds it.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 0, "card": "yellow"})
	assert.Eventually(t, func() bool { return len(cards("red")) == 3 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, map[int]string{1: "yellow", 2: "yellow", 3: "yellow"}, cards("red"))
	assert.False(t, disqualified("red"))

	// The same card again becomes the game's second card, which disqualifies, and in the playoffs that takes the
	// whole alliance out of the match.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 0, "card": "yellow"})
	assert.Eventually(t, func() bool { return cards("red")[3] == "red" }, time.Second, 10*time.Millisecond)
	assert.Equal(t, map[int]string{1: "red", 2: "red", 3: "red"}, cards("red"))
	assert.True(t, disqualified("red"))
	assert.False(t, disqualified("blue"))

	// Clearing the card lifts the disqualification again.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 0, "card": ""})
	assert.Eventually(t, func() bool { return len(cards("red")) == 0 }, time.Second, 10*time.Millisecond)
	assert.False(t, disqualified("red"))

	// The disqualifying card cannot be an alliance's first card either.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 0, "card": "red"})
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "can only be given to a team that already has a card")
	assert.Equal(t, 0, len(cards("blue")))

	// It is the alliance that carries a card from an earlier playoff match, not the team: team 201 comes with one
	// from the qualification rounds, which does not count here.
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 0, "card": "yellow"})
	assert.Eventually(t, func() bool { return len(cards("blue")) == 3 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, map[int]string{1: "yellow", 2: "yellow", 3: "yellow"}, cards("blue"))
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 0, "card": ""})
	assert.Eventually(t, func() bool { return len(cards("blue")) == 0 }, time.Second, 10*time.Millisecond)
	alliance, err := web.arena.Database.GetAllianceById(match.PlayoffBlueAlliance)
	assert.Nil(t, err)
	alliance.YellowCard = true
	assert.Nil(t, web.arena.Database.UpdateAlliance(alliance))
	ws.Write("card", map[string]any{"alliance": "blue", "robot": 0, "card": "yellow"})
	assert.Eventually(t, func() bool { return len(cards("blue")) == 3 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, map[int]string{1: "red", 2: "red", 3: "red"}, cards("blue"))
	assert.True(t, disqualified("blue"))

	// A single robot position can still be given a card.
	ws.Write("card", map[string]any{"alliance": "red", "robot": 2, "card": "yellow"})
	assert.Eventually(t, func() bool { return len(cards("red")) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, map[int]string{2: "yellow"}, cards("red"))
}
