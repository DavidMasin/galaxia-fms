package field

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRealtimeScoreMessage(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)

	// Before anything is scored the message already has its full shape, with nothing in it.
	message := arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage)
	encoded, err := json.Marshal(message)
	assert.Nil(t, err)
	emptyAlliance := `{
		"score": 0, "foulPoints": 0, "points": {"balls": 0}, "phasePoints": {}, "values": {},
		"breakdown": [
			{"id": "auto_points", "name": "Auto", "value": 0}, {"id": "teleop_points", "name": "Teleop", "value": 0},
			{"id": "foul_points", "name": "Fouls", "value": 0}
		],
		"result": "tie", "rp": {"win": 1}, "rpTotal": 1, "live": {},
		"state": {
			"elements": {"balls": {}}, "robots": [{}, {}, {}], "fouls": [], "cards": {}, "disqualified": false
		}
	}`
	assert.JSONEq(
		t,
		`{
			"gameHash": "`+arena.Game.Hash+`", "seq": 0, "winner": "", "tiebreak": "",
			"field": {"state": {}, "values": {}, "live": {}},
			"alliances": {"red": `+emptyAlliance+`, "blue": `+emptyAlliance+`}
		}`,
		string(encoded),
	)

	// Three balls in auto and ten in teleop for red, and a foul by red in teleop.
	startTestMatch(t, arena)
	for i := 0; i < 3; i++ {
		apply(t, arena, count("red", "balls", testDevice))
	}
	runMatchTo(arena, 37)
	for i := 0; i < 10; i++ {
		apply(t, arena, count("red", "balls", testDevice))
	}
	apply(t, arena, count("blue", "balls", testDevice))
	foul := apply(
		t, arena,
		scoring.Change{
			Alliance: "red", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "foul", Source: testReferee,
		},
	)
	assert.Equal(t, 15, foul.Seq)
	message = arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage)
	encoded, err = json.Marshal(message)
	assert.Nil(t, err)
	assert.JSONEq(
		t,
		`{
			"gameHash": "`+arena.Game.Hash+`", "seq": 15, "winner": "red", "tiebreak": "",
			"field": {"state": {}, "values": {}, "live": {}},
			"alliances": {
				"red": {
					"score": 16, "foulPoints": 0, "points": {"balls": 16},
					"phasePoints": {"balls": {"auto": 6, "teleop": 10}}, "values": {},
					"breakdown": [
						{"id": "auto_points", "name": "Auto", "value": 6},
						{"id": "teleop_points", "name": "Teleop", "value": 10},
						{"id": "foul_points", "name": "Fouls", "value": 0}
					],
					"result": "win", "rp": {"win": 2}, "rpTotal": 2, "live": {},
					"state": {
						"elements": {"balls": {"counts": {"auto": 3, "teleop": 10}}}, "robots": [{}, {}, {}],
						"fouls": [{"id": 15, "type": "foul", "phase": "teleop"}], "cards": {}, "disqualified": false
					}
				},
				"blue": {
					"score": 4, "foulPoints": 3, "points": {"balls": 1}, "phasePoints": {"balls": {"teleop": 1}},
					"values": {},
					"breakdown": [
						{"id": "auto_points", "name": "Auto", "value": 0},
						{"id": "teleop_points", "name": "Teleop", "value": 1},
						{"id": "foul_points", "name": "Fouls", "value": 3}
					],
					"result": "loss", "rp": {"win": 0}, "rpTotal": 0, "live": {},
					"state": {
						"elements": {"balls": {"counts": {"teleop": 1}}}, "robots": [{}, {}, {}], "fouls": [],
						"cards": {}, "disqualified": false
					}
				}
			}
		}`,
		string(encoded),
	)

	// A message that has been handed out does not change when the score does: it is turned into JSON later, on
	// another goroutine.
	apply(t, arena, count("red", "balls", testDevice))
	apply(
		t, arena,
		scoring.Change{
			Alliance: "red", Robot: 1, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "", Source: testReferee,
		},
	)
	encodedAgain, err := json.Marshal(message)
	assert.Nil(t, err)
	assert.Equal(t, string(encoded), string(encodedAgain))
	assert.Equal(t, 17.0, arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage).Alliances["red"].Score)
}

func TestRealtimeScoreMessageFieldAndRobots(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	apply(
		t, arena,
		scoring.Change{
			Alliance: "blue", Robot: 2, Element: "auto_tower", Op: scoring.OpSet, Value: "level2", Source: testScorer,
		},
	)
	apply(
		t, arena,
		scoring.Change{
			Alliance: "blue", Robot: 2, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "yellow",
			Source: testReferee,
		},
	)
	apply(t, arena, scoring.Change{Element: "auto_winner", Op: scoring.OpSet, Value: "blue", Source: testScorer})

	message := arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage)
	assert.Equal(t, 3, message.Seq)
	assert.Equal(t, "blue", message.Field.State["auto_winner"].Option)
	blue := message.Alliances["blue"]
	assert.Equal(t, "level2", blue.State.Robots[1]["auto_tower"].Option)
	assert.Equal(t, "none", blue.State.Robots[0]["auto_tower"].Option)
	assert.Equal(t, map[int]string{2: "yellow"}, blue.State.Cards)
	assert.Equal(t, 15.0, blue.Points["auto_tower_points"])
	assert.Equal(t, 15.0, blue.Values["tower_points"])
	assert.Equal(t, true, blue.Values["bonus_eligible"])
	assert.Equal(t, map[string]any{"hub_active": true}, blue.Live)
	assert.Equal(t, map[string]float64{"win": 3, "energized": 0, "supercharged": 0, "traversal": 0}, blue.RP)
}

func TestScoringStatusMessage(t *testing.T) {
	arena := setupTestArena(t)
	type positionStatus struct {
		Ready          bool
		NumPanels      int
		NumPanelsReady int
	}
	type scoringStatus struct {
		RefereeScoreReady bool
		PositionStatuses  map[string]positionStatus
	}
	getStatus := func() scoringStatus {
		encoded, err := json.Marshal(arena.generateScoringStatusMessage())
		assert.Nil(t, err)
		var status scoringStatus
		assert.Nil(t, json.Unmarshal(encoded, &status))
		return status
	}

	// There is a position for every scoring panel of the game.
	status := getStatus()
	assert.False(t, status.RefereeScoreReady)
	assert.Equal(
		t,
		map[string]positionStatus{"red_near": {}, "red_far": {}, "blue_near": {}, "blue_far": {}},
		status.PositionStatuses,
	)
	assert.False(t, arena.scoringPanelsReady())

	// A position is ready when it is staffed and every panel at it has committed.
	panels := map[string]*websocket.Websocket{}
	for _, position := range []string{"red_near", "red_far", "blue_near", "blue_far"} {
		panels[position] = &websocket.Websocket{}
		arena.ScoringPanelRegistry.RegisterPanel(position, panels[position])
	}
	second := &websocket.Websocket{}
	arena.ScoringPanelRegistry.RegisterPanel("red_far", second)
	arena.ScoringPanelRegistry.SetScoreCommitted("red_near", panels["red_near"])
	arena.ScoringPanelRegistry.SetScoreCommitted("red_far", panels["red_far"])
	status = getStatus()
	assert.Equal(t, positionStatus{Ready: true, NumPanels: 1, NumPanelsReady: 1}, status.PositionStatuses["red_near"])
	assert.Equal(t, positionStatus{Ready: false, NumPanels: 2, NumPanelsReady: 1}, status.PositionStatuses["red_far"])
	assert.Equal(t, positionStatus{Ready: false, NumPanels: 1}, status.PositionStatuses["blue_far"])
	assert.False(t, arena.scoringPanelsReady())

	arena.ScoringPanelRegistry.SetScoreCommitted("red_far", second)
	arena.ScoringPanelRegistry.SetScoreCommitted("blue_near", panels["blue_near"])
	arena.ScoringPanelRegistry.SetScoreCommitted("blue_far", panels["blue_far"])
	assert.True(t, arena.scoringPanelsReady())
	assert.Nil(t, arena.SetFoulsCommitted("red", true))
	assert.Nil(t, arena.SetFoulsCommitted("blue", true))
	assert.True(t, getStatus().RefereeScoreReady)

	// A game without a panel layout has one position per alliance.
	selectGame(t, arena, "ball-count", nil)
	status = getStatus()
	assert.False(t, status.RefereeScoreReady)
	assert.Equal(t, map[string]positionStatus{"red": {}, "blue": {}}, status.PositionStatuses)
}

func TestPlcStackLightsFollowTheMatch(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	var plc FakePlc
	plc.isEnabled = true
	plc.ftaReady = true
	arena.Plc = &plc

	arena.Update()
	assert.Equal(t, [4]bool{true, true, false, false}, plc.stackLights)

	arena.AllianceStations["R1"].Bypass = true
	arena.AllianceStations["R2"].Bypass = true
	arena.AllianceStations["B1"].Bypass = true
	arena.AllianceStations["B2"].Bypass = true
	arena.Update()
	assert.Equal(t, [4]bool{true, true, false, false}, plc.stackLights)

	arena.AllianceStations["R3"].Bypass = true
	arena.Update()
	assert.Equal(t, [4]bool{false, true, false, false}, plc.stackLights)
	assert.Equal(t, false, plc.stackLightBuzzer)

	// All teams are ready.
	arena.AllianceStations["B3"].Bypass = true
	plc.cycleState = true
	arena.Update()
	assert.Equal(t, [4]bool{false, false, false, true}, plc.stackLights)
	assert.Equal(t, true, plc.stackLightBuzzer)

	// Green light when blink cycle is off.
	plc.cycleState = false
	arena.Update()
	assert.Equal(t, [4]bool{false, false, false, false}, plc.stackLights)

	// Start the match.
	assert.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, [4]bool{false, false, false, true}, plc.stackLights)
	assert.Equal(t, false, plc.stackLightBuzzer)

	// The lights stay green through every phase of the match, including the pause.
	runMatchTo(arena, 16)
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	assert.Equal(t, [4]bool{false, false, false, true}, plc.stackLights)
	runMatchTo(arena, 60)
	assert.Equal(t, [4]bool{false, false, false, true}, plc.stackLights)

	// End the match.
	runMatchTo(arena, 137.05)
	arena.Update()
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.Equal(t, [4]bool{false, false, true, false}, plc.stackLights)
	assert.Equal(t, false, plc.fieldResetLight)

	// Ready the score: the referees confirm the fouls of both alliances and every scoring panel commits.
	assert.Nil(t, arena.SetFoulsCommitted("red", true))
	assert.Nil(t, arena.SetFoulsCommitted("blue", true))
	redWs := &websocket.Websocket{}
	arena.ScoringPanelRegistry.RegisterPanel("red", redWs)
	arena.ScoringPanelRegistry.SetScoreCommitted("red", redWs)
	arena.Update()
	assert.Equal(t, [4]bool{false, false, true, false}, plc.stackLights)
	blueWs := &websocket.Websocket{}
	arena.ScoringPanelRegistry.RegisterPanel("blue", blueWs)
	arena.ScoringPanelRegistry.SetScoreCommitted("blue", blueWs)
	arena.Update()
	assert.Equal(t, [4]bool{false, false, false, false}, plc.stackLights)

	arena.FieldReset = true
	arena.Update()
	assert.Equal(t, true, plc.fieldResetLight)

	assert.Equal(t, false, plc.awardsModeLight)

	arena.SetAllianceStationDisplayMode("logo")
	arena.Update()
	assert.Equal(t, true, plc.awardsModeLight)

	arena.SetAllianceStationDisplayMode("match")
	arena.Update()
	assert.Equal(t, false, plc.awardsModeLight)
}

func TestScorePostedMessage(t *testing.T) {
	arena := setupTestArena(t)

	// Before any match is committed there is an empty result to show.
	message := arena.GenerateScorePostedMessage().(*ScorePostedMessage)
	assert.Equal(t, "rebuilt-style", message.GameId)
	assert.Equal(t, "", message.Winner)
	if assert.Contains(t, message.Alliances, "red") {
		assert.Equal(t, 0.0, message.Alliances["red"].Score)
		assert.Equal(t, []int{0, 0, 0}, message.Alliances["red"].Teams)
		assert.Empty(t, message.Alliances["red"].Fouls)
	}
	_, err := json.Marshal(message)
	assert.Nil(t, err)

	// A committed qualification match.
	match := &model.Match{
		Id: 12, Type: model.Qualification, ShortName: "Q12", Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105,
		Blue3: 106, Status: model.BlueWonMatch,
	}
	matchResult := model.BuildTestMatchResult(arena.Database, match.Id, 1)
	matchResult.Teams = map[string][]int{"red": {101, 102, 103}, "blue": {104, 105, 106}}
	g := model.TestGame("rebuilt-style")
	_, err = matchResult.Edit(
		g,
		scoring.Change{
			Alliance: "red", Robot: 3, Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "major", Rule: "G418",
			Bucket: "endgame", Source: testReferee,
		},
	)
	assert.Nil(t, err)
	arena.SavedMatch = match
	arena.SavedMatchResult = matchResult
	arena.SavedRankings = []model.Ranking{
		model.NewRanking(scoring.TeamRanking{TeamID: 104, Rank: 1, PreviousRank: 3}),
		model.NewRanking(scoring.TeamRanking{TeamID: 101, Rank: 2, PreviousRank: 1}),
		model.NewRanking(scoring.TeamRanking{TeamID: 999, Rank: 3, PreviousRank: 2}),
	}

	message = arena.GenerateScorePostedMessage().(*ScorePostedMessage)
	assert.Same(t, match, message.Match)
	assert.Equal(t, g.Hash, message.GameHash)
	assert.Equal(t, "blue", message.Winner)
	assert.Equal(t, "", message.Tiebreak)
	red, blue := message.Alliances["red"], message.Alliances["blue"]
	assert.Equal(t, []int{101, 102, 103}, red.Teams)
	assert.Equal(t, []int{104, 105, 106}, blue.Teams)
	assert.Equal(t, 133.0, red.Score)
	assert.Equal(t, 304.0, blue.Score)
	assert.Equal(t, scoring.ResultLoss, red.Result)
	assert.Equal(t, scoring.ResultWin, blue.Result)
	assert.Equal(
		t,
		[]scoring.Row{
			{ID: "auto_fuel_points", Name: "Auto fuel", Value: 35},
			{ID: "auto_tower_points", Name: "Auto tower", Value: 30},
			{ID: "teleop_fuel_points", Name: "Teleop fuel", Value: 79},
			{ID: "teleop_tower_points", Name: "Endgame tower", Value: 60},
			{ID: "foul_points", Name: "Fouls", Value: 100},
		},
		blue.Breakdown,
	)
	assert.Equal(
		t,
		[]ScorePostedRankingPoint{
			{Id: "win", Name: "Win", Value: 3}, {Id: "energized", Name: "Energized", Value: 1},
			{Id: "supercharged", Name: "Supercharged", Value: 0}, {Id: "traversal", Name: "Traversal", Value: 1},
		},
		blue.RankingPoints,
	)
	assert.Equal(t, 5.0, blue.RPTotal)
	assert.Equal(t, 0.0, red.RPTotal)

	// Cards are listed by robot position, and fouls come with the names a person needs to read them.
	assert.Equal(t, map[int]string{1: "yellow"}, red.Cards)
	assert.Empty(t, blue.Cards)
	assert.Empty(t, blue.Fouls)
	if assert.Equal(t, 8, len(red.Fouls)) {
		assert.Equal(t, "major", red.Fouls[0].Type)
		assert.Equal(t, "Major foul", red.Fouls[0].TypeName)
		assert.Equal(t, 15.0, red.Fouls[0].Points)
		assert.Equal(t, 0, red.Fouls[0].Team)
		assert.Equal(t, "", red.Fouls[0].RuleName)
		assert.Equal(t, "Foul", red.Fouls[1].TypeName)
		assert.Equal(t, 5.0, red.Fouls[1].Points)

		// The same rule number is listed for a minor and a major foul; the entry for the foul called is used.
		last := red.Fouls[7]
		assert.Equal(t, "G418", last.Rule)
		assert.Equal(t, "Extended pinning", last.RuleName)
		assert.Equal(t, "Continued or repeated pinning after the first call.", last.RuleText)
		assert.Equal(t, 3, last.Robot)
		assert.Equal(t, 103, last.Team)
		assert.Equal(t, "endgame", last.Phase)
	}

	// For a qualification match each team on the field comes with its rank before and after.
	assert.Equal(t, map[int]*ScorePostedRanking{101: {Rank: 2, PreviousRank: 1}}, red.Rankings)
	assert.Equal(t, map[int]*ScorePostedRanking{104: {Rank: 1, PreviousRank: 3}}, blue.Rankings)
	assert.Equal(t, 0, red.Wins)
	assert.Equal(t, "", red.Destination)
	assert.Empty(t, red.OffFieldTeams)

	// The JSON carries the foul fields at the top level of each foul.
	encoded, err := json.Marshal(message)
	assert.Nil(t, err)
	var decoded struct {
		Alliances map[string]struct {
			Fouls []map[string]any `json:"fouls"`
			Cards map[string]string
		} `json:"alliances"`
	}
	assert.Nil(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(
		t,
		map[string]any{
			"id": 36.0, "type": "major", "rule": "G418", "robot": 3.0, "phase": "endgame", "typeName": "Major foul",
			"points": 15.0, "team": 103.0, "ruleName": "Extended pinning",
			"ruleText": "Continued or repeated pinning after the first call.",
		},
		decoded.Alliances["red"].Fouls[7],
	)
	assert.Equal(t, map[string]string{"1": "yellow"}, decoded.Alliances["red"].Cards)
}

func TestScorePostedMessagePlayoff(t *testing.T) {
	arena := setupTestArena(t)

	// A playoff match that finished level on points and was settled by the game's tiebreakers.
	match := &model.Match{
		Id: 40, Type: model.Playoff, ShortName: "F1", Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105,
		Blue3: 106, UseTiebreakCriteria: true, PlayoffMatchGroupId: "no-such-group",
	}
	g := model.TestGame("rebuilt-style")
	assert.Nil(t, arena.Database.SaveGameSnapshot(g))
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = match.Id
	matchResult.MatchType = model.Playoff
	for alliance, phase := range map[string]string{"red": "auto", "blue": "endgame"} {
		_, err := matchResult.Edit(
			g,
			scoring.Change{
				Alliance: alliance, Element: "fuel", Op: scoring.OpSet, Value: 10, Bucket: phase, Source: testScorer,
			},
		)
		assert.Nil(t, err)
	}
	arena.SavedMatch = match
	arena.SavedMatchResult = matchResult
	arena.SavedRankings = []model.Ranking{model.NewRanking(scoring.TeamRanking{TeamID: 101, Rank: 1})}

	message := arena.GenerateScorePostedMessage().(*ScorePostedMessage)
	assert.Equal(t, 10.0, message.Alliances["red"].Score)
	assert.Equal(t, 10.0, message.Alliances["blue"].Score)
	assert.Equal(t, "red", message.Winner)
	assert.Equal(t, "Auto fuel", message.Tiebreak)
	assert.Empty(t, message.Alliances["red"].Rankings, "ranks only move in qualification matches")

	// Without the tiebreak criteria the same result is a tie.
	match.UseTiebreakCriteria = false
	message = arena.GenerateScorePostedMessage().(*ScorePostedMessage)
	assert.Equal(t, "", message.Winner)
	assert.Equal(t, "", message.Tiebreak)

	// A red card on any robot disqualifies the whole alliance in the playoffs.
	_, err := matchResult.Edit(
		g,
		scoring.Change{
			Alliance: "red", Robot: 2, Element: scoring.TargetCard, Op: scoring.OpSet, Value: "red", Source: testReferee,
		},
	)
	assert.Nil(t, err)
	message = arena.GenerateScorePostedMessage().(*ScorePostedMessage)
	assert.Equal(t, "blue", message.Winner)
	assert.True(t, message.Alliances["red"].Disqualified)
	assert.Equal(t, 0.0, message.Alliances["red"].Score)
	assert.Equal(t, map[int]string{2: "red"}, message.Alliances["red"].Cards)
}
