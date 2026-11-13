// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"bytes"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log"
	"os"
	"sync"
	"testing"
	"time"
)

type synchronizedLogWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
}

func newSynchronizedLogWriter() *synchronizedLogWriter {
	return &synchronizedLogWriter{writes: make(chan struct{}, 16)}
}

func (w *synchronizedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	w.writes <- struct{}{}
	return n, err
}

func (w *synchronizedLogWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *synchronizedLogWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Reset()
	for len(w.writes) > 0 {
		<-w.writes
	}
}

func (w *synchronizedLogWriter) waitForWrites(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-w.writes:
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for log write %d of %d", i+1, count)
		}
	}
}

func TestMatchPlay(t *testing.T) {
	web := setupTestWeb(t)

	// Check that some text near the bottom of the page is present.
	recorder := web.getHttpResponse("/match_play")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Are you sure you want to discard the results for this match?")
}

func TestMatchPlayMatchList(t *testing.T) {
	web := setupTestWeb(t)

	match1 := model.Match{Type: model.Practice, ShortName: "P1", Status: model.RedWonMatch}
	match2 := model.Match{Type: model.Practice, ShortName: "P2"}
	match3 := model.Match{Type: model.Qualification, ShortName: "Q1", Status: model.BlueWonMatch}
	match4 := model.Match{Type: model.Playoff, ShortName: "SF1-1", Status: model.TieMatch}
	match5 := model.Match{Type: model.Playoff, ShortName: "SF1-2"}
	web.arena.Database.CreateMatch(&match1)
	web.arena.Database.CreateMatch(&match2)
	web.arena.Database.CreateMatch(&match3)
	web.arena.Database.CreateMatch(&match4)
	web.arena.Database.CreateMatch(&match5)

	// Check that all matches are listed on the page.
	recorder := web.getHttpResponse("/match_play/match_load")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "P1")
	assert.Contains(t, recorder.Body.String(), "P2")
	assert.Contains(t, recorder.Body.String(), "Q1")
	assert.Contains(t, recorder.Body.String(), "SF1-1")
	assert.Contains(t, recorder.Body.String(), "SF1-2")
}

// testResult builds a result of the active game for the given match from a list of changes, the way a played match
// would have produced it.
func testResult(t *testing.T, web *Web, match *model.Match, changes ...scoring.Change) *model.MatchResult {
	g := web.arena.Game
	require.NoError(t, web.arena.Database.SaveGameSnapshot(g))
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = match.Id
	matchResult.MatchType = match.Type
	matchResult.Teams = teamsForGame(g, match)
	for _, change := range changes {
		change.Source = scoring.Source{Kind: scoring.SourceAdmin, ID: "test"}
		_, err := matchResult.Edit(g, change)
		require.NoError(t, err)
	}
	return matchResult
}

// The changes the commit tests build results of the rebuilt-style example game from.
func towerChange(alliance string, robot int, element, option string) scoring.Change {
	return scoring.Change{Alliance: alliance, Robot: robot, Element: element, Op: scoring.OpSet, Value: option}
}

func fuelChange(alliance, phase string, count float64) scoring.Change {
	return scoring.Change{Alliance: alliance, Element: "fuel", Op: scoring.OpSet, Value: count, Bucket: phase}
}

func foulChange(alliance, foulType string) scoring.Change {
	return scoring.Change{Alliance: alliance, Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: foulType}
}

func cardChange(alliance string, robot int, card string) scoring.Change {
	return scoring.Change{Alliance: alliance, Robot: robot, Element: scoring.TargetCard, Op: scoring.OpSet, Value: card}
}

func TestCommitMatch(t *testing.T) {
	web := setupTestWeb(t)
	committed := web.arena.Bus.Subscribe([]string{"match.committed", "match.result_edited"}, 16)
	defer committed.Close()

	// Committing test match should update the stored saved match but not persist anything.
	match := &model.Match{Id: 0, Type: model.Test, Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105, Blue3: 106}
	matchResult := testResult(t, web, match, towerChange("blue", 3, "endgame_tower", "level1"))
	err := web.commitMatchScore(match, matchResult, false)
	assert.Nil(t, err)
	matchResult, err = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Nil(t, err)
	assert.Nil(t, matchResult)
	assert.Equal(t, match, web.arena.SavedMatch)
	assert.Equal(t, model.BlueWonMatch, web.arena.SavedMatch.Status)
	assert.Equal(t, "level1", web.arena.SavedMatchResult.State.Alliances["blue"].Robots[2]["endgame_tower"].Option)

	// Committing the same match more than once should create a second match result record.
	match.Type = model.Qualification
	assert.Nil(t, web.arena.Database.CreateMatch(match))
	matchResult = testResult(t, web, match, towerChange("blue", 1, "auto_tower", "level1"))
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	assert.Equal(t, 1, matchResult.PlayNumber)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.BlueWonMatch, match.Status)

	matchResult = testResult(t, web, match, towerChange("red", 1, "endgame_tower", "level2"))
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	assert.Equal(t, 2, matchResult.PlayNumber)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.RedWonMatch, match.Status)

	matchResult = testResult(t, web, match)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	assert.Equal(t, 3, matchResult.PlayNumber)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.TieMatch, match.Status)

	// The rankings were recalculated from the latest result with the game's own ranking order.
	rankings, _ := web.arena.Database.GetAllRankings()
	if assert.Equal(t, 6, len(rankings)) {
		assert.Equal(t, 1, rankings[0].Ties)
		assert.Equal(t, 1.0, rankings[0].RP)
		assert.Equal(t, len(web.arena.Game.Ranking.Order), len(rankings[0].Sort))
	}

	// Verify TBA publishing by checking the log for the expected failure messages.
	web.arena.TbaClient.BaseUrl = "fakeUrl"
	web.arena.EventSettings.TbaPublishingEnabled = true
	writer := newSynchronizedLogWriter()
	log.SetOutput(writer)
	defer log.SetOutput(os.Stderr)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	writer.waitForWrites(t, 2)
	assert.Contains(t, writer.String(), "Failed to publish matches")
	assert.Contains(t, writer.String(), "Failed to publish rankings")

	// Committing a practice match should publish matches to TBA, but not rankings.
	writer.Reset()
	practiceMatch := &model.Match{Type: model.Practice, ShortName: "P1"}
	assert.Nil(t, web.arena.Database.CreateMatch(practiceMatch))
	practiceResult := testResult(t, web, practiceMatch)
	err = web.commitMatchScore(practiceMatch, practiceResult, true)
	assert.Nil(t, err)
	writer.waitForWrites(t, 1)
	assert.Contains(t, writer.String(), "Failed to publish matches")
	assert.NotContains(t, writer.String(), "Failed to publish rankings")

	// Every commit was announced on the event bus: the first as a commit, the others as edits of a result.
	var eventTypes []string
	for len(eventTypes) < 6 {
		select {
		case event := <-committed.C:
			eventTypes = append(eventTypes, event.Type)
		case <-time.After(time.Second):
			t.Fatalf("only %d commits were announced on the event bus", len(eventTypes))
		}
	}
	assert.Equal(t, "match.committed", eventTypes[0])
	assert.Equal(t, "match.result_edited", eventTypes[1])
}

func TestCommitTiebreak(t *testing.T) {
	web := setupTestWeb(t)

	match := &model.Match{
		Type:                model.Qualification,
		TypeOrder:           1,
		Red1:                1,
		Red2:                2,
		Red3:                3,
		Blue1:               4,
		Blue2:               5,
		Blue3:               6,
		UseTiebreakCriteria: false,
	}
	web.arena.Database.CreateMatch(match)

	// The playoff tiebreakers of the rebuilt-style game are, in order: major fouls by the opponent, fuel scored in
	// autonomous, and tower points. These two sides are level on points and on all three.
	type side struct {
		transitionFuel float64
		fouls          []string
	}
	build := func(red, blue side) *model.MatchResult {
		var changes []scoring.Change
		for _, alliance := range []struct {
			id   string
			side side
		}{{"red", red}, {"blue", blue}} {
			if alliance.side.transitionFuel > 0 {
				changes = append(changes, fuelChange(alliance.id, "transition", alliance.side.transitionFuel))
			}
			for _, foulType := range alliance.side.fouls {
				changes = append(changes, foulChange(alliance.id, foulType))
			}
		}
		return testResult(t, web, match, changes...)
	}
	scores := func(matchResult *model.MatchResult) (float64, float64) {
		result, _, err := matchResult.Compute(web.arena.Database, match.UseTiebreakCriteria)
		require.NoError(t, err)
		return result.Alliances["red"].Score, result.Alliances["blue"].Score
	}
	level := side{transitionFuel: 5, fouls: []string{"minor", "minor"}}
	other := side{fouls: []string{"minor"}}
	matchResult := build(level, other)

	// Sanity check that the test scores are equal; they will need to be updated accordingly for each new game.
	redScore, blueScore := scores(matchResult)
	assert.Equal(t, redScore, blueScore)
	assert.Equal(t, 10.0, redScore)

	err := web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.TieMatch, match.Status)

	// The match should still be tied since the tiebreaker criteria for a perfect tie are fulfilled.
	match.UseTiebreakCriteria = true
	web.arena.Database.UpdateMatch(match)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.TieMatch, match.Status)

	// Change the score to still be equal nominally but trigger the tiebreaker criteria: blue commits a major foul
	// instead of a minor one and scores ten fuel to make up for it.
	breaker := side{transitionFuel: 10, fouls: []string{"major"}}
	matchResult = build(level, breaker)

	// Sanity check that the test scores are equal; they will need to be updated accordingly for each new game.
	redScore, blueScore = scores(matchResult)
	assert.Equal(t, redScore, blueScore)
	assert.Equal(t, 20.0, redScore)

	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.RedWonMatch, match.Status)
	result, _, err := matchResult.Compute(web.arena.Database, true)
	assert.Nil(t, err)
	assert.Equal(t, "Major fouls by the opponent", result.Tiebreak)

	// Without the tiebreak criteria the same result is a tie.
	result, _, err = matchResult.Compute(web.arena.Database, false)
	assert.Nil(t, err)
	assert.Equal(t, "", result.Winner)

	// Swap red and blue and verify that the tie is broken in the other direction.
	matchResult = build(breaker, level)

	// Sanity check that the test scores are equal; they will need to be updated accordingly for each new game.
	redScore, blueScore = scores(matchResult)
	assert.Equal(t, redScore, blueScore)

	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	match, _ = web.arena.Database.GetMatchById(1)
	assert.Equal(t, model.BlueWonMatch, match.Status)
}

func TestCommitCards(t *testing.T) {
	web := setupTestWeb(t)

	// Check that a yellow card sticks with a team.
	team1 := &model.Team{Id: 3}
	team2 := &model.Team{Id: 5}
	web.arena.Database.CreateTeam(team1)
	web.arena.Database.CreateTeam(team2)
	match := &model.Match{Id: 0, Type: model.Qualification, Red1: 1, Red2: 2, Red3: 3, Blue1: 4, Blue2: 5, Blue3: 6}
	assert.Nil(t, web.arena.Database.CreateMatch(match))
	matchResult := testResult(t, web, match, cardChange("red", 3, "yellow"), cardChange("blue", 2, "yellow"))
	assert.Equal(t, map[int]string{3: "yellow"}, matchResult.TeamCards("red"))
	err := web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	team1, _ = web.arena.Database.GetTeamById(3)
	assert.True(t, team1.YellowCard)
	team2, _ = web.arena.Database.GetTeamById(5)
	assert.True(t, team2.YellowCard)

	// Check that editing a match result removes a yellow card from a team.
	matchResult = testResult(t, web, match)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	team1, _ = web.arena.Database.GetTeamById(3)
	assert.False(t, team1.YellowCard)
	team2, _ = web.arena.Database.GetTeamById(5)
	assert.False(t, team2.YellowCard)

	// Check that a red card causes a yellow card to stick with a team, and counts the match as a disqualification
	// for that team only: its partners keep what the alliance earned.
	matchResult = testResult(
		t, web, match, cardChange("red", 3, "red"), cardChange("blue", 2, "red"),
		towerChange("red", 1, "endgame_tower", "level1"),
	)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	team1, _ = web.arena.Database.GetTeamById(3)
	assert.True(t, team1.YellowCard)
	team2, _ = web.arena.Database.GetTeamById(5)
	assert.True(t, team2.YellowCard)
	match, _ = web.arena.Database.GetMatchById(match.Id)
	assert.Equal(t, model.RedWonMatch, match.Status)
	for teamId, disqualifications := range map[int]int{1: 0, 2: 0, 3: 1, 4: 0, 5: 1, 6: 0} {
		ranking, _ := web.arena.Database.GetRankingForTeam(teamId)
		if assert.NotNil(t, ranking) {
			assert.Equal(t, disqualifications, ranking.Disqualifications, "team %d", teamId)
		}
	}
	ranking, _ := web.arena.Database.GetRankingForTeam(1)
	assert.Equal(t, 1, ranking.Wins)
	ranking, _ = web.arena.Database.GetRankingForTeam(3)
	assert.Equal(t, 0, ranking.Wins)

	// Check that disqualifying an alliance without a card does not cause a card to stick with a team.
	matchResult = testResult(
		t, web, match,
		scoring.Change{Alliance: "red", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true},
		scoring.Change{Alliance: "blue", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true},
	)
	err = web.commitMatchScore(match, matchResult, true)
	assert.Nil(t, err)
	team1, _ = web.arena.Database.GetTeamById(3)
	assert.False(t, team1.YellowCard)
	team2, _ = web.arena.Database.GetTeamById(5)
	assert.False(t, team2.YellowCard)

	// Check that a red card in playoffs zeroes out the score.
	tournament.CreateTestAlliances(web.arena.Database, 2)
	web.arena.EventSettings.PlayoffType = model.SingleEliminationPlayoff
	web.arena.EventSettings.NumPlayoffAlliances = 2
	web.arena.CreatePlayoffTournament()
	web.arena.CreatePlayoffMatches(time.Now())
	match.Type = model.Playoff
	match.PlayoffRedAlliance = 1
	match.PlayoffBlueAlliance = 2
	web.arena.Database.UpdateMatch(match)
	g := web.arena.Game
	scores := func(matchResult *model.MatchResult) (float64, float64) {
		result, _, err := matchResult.Compute(web.arena.Database, match.UseTiebreakCriteria)
		require.NoError(t, err)
		return result.Alliances["red"].Score, result.Alliances["blue"].Score
	}
	matchResult = model.BuildTestMatchResult(web.arena.Database, match.Id, 0)
	matchResult.MatchType = match.Type
	_, err = matchResult.Edit(g, cardChange("red", 1, "red"))
	assert.Nil(t, err)
	assert.Nil(t, web.commitMatchScore(match, matchResult, true))
	redScore, blueScore := scores(matchResult)
	assert.Equal(t, 0.0, redScore)
	assert.NotEqual(t, 0.0, blueScore)
	// The stored state holds the card, not the disqualification that follows from it.
	assert.False(t, matchResult.State.Alliances["red"].Disqualified)

	// Check that disqualifying an alliance in playoffs zeroes out the score.
	_, err = matchResult.Edit(g, cardChange("red", 1, ""))
	assert.Nil(t, err)
	_, err = matchResult.Edit(
		g, scoring.Change{Alliance: "blue", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true},
	)
	assert.Nil(t, err)
	assert.Nil(t, web.commitMatchScore(match, matchResult, true))
	redScore, blueScore = scores(matchResult)
	assert.NotEqual(t, 0.0, redScore)
	assert.Equal(t, 0.0, blueScore)

	// Check that a red card in playoffs forces a loss even if both alliances score 0.
	matchResult = testResult(t, web, match, cardChange("red", 2, "red"))
	assert.Nil(t, web.commitMatchScore(match, matchResult, true))
	match, _ = web.arena.Database.GetMatchById(match.Id)
	assert.Equal(t, model.BlueWonMatch, match.Status)

	// Check that a disqualification in playoffs forces a loss even if both alliances score 0.
	matchResult = testResult(
		t, web, match,
		scoring.Change{Alliance: "blue", Element: scoring.TargetDisqualified, Op: scoring.OpSet, Value: true},
	)
	assert.Nil(t, web.commitMatchScore(match, matchResult, true))
	match, _ = web.arena.Database.GetMatchById(match.Id)
	assert.Equal(t, model.RedWonMatch, match.Status)
}

func TestMatchPlayWebsocketCommands(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.Database.CreateTeam(&model.Team{Id: 254})

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/match_play/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketType(t, ws, "game")
	readWebsocketType(t, ws, "allianceStationDisplayMode")
	readWebsocketType(t, ws, "arenaStatus")
	readWebsocketType(t, ws, "audienceDisplayMode")
	readWebsocketType(t, ws, "eventStatus")
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketType(t, ws, "matchTime")
	readWebsocketType(t, ws, "realtimeScore")
	readWebsocketType(t, ws, "scorePosted")
	readWebsocketType(t, ws, "scoringStatus")

	// Test that a server-side error is communicated to the client.
	ws.Write("nonexistenttype", nil)
	assert.Contains(t, readWebsocketError(t, ws), "Invalid message type")

	// Test match setup commands.
	ws.Write("substituteTeams", map[string]int{"Red1": 0, "Red2": 0, "Red3": 0, "Blue1": 1, "Blue2": 0, "Blue3": 0})
	assert.Equal(t, readWebsocketError(t, ws), "Team 1 is not present at the event.")
	ws.Write("substituteTeams", map[string]int{"Red1": 0, "Red2": 0, "Red3": 0, "Blue1": 254, "Blue2": 0, "Blue3": 0})
	readWebsocketType(t, ws, "matchLoad")
	assert.Equal(t, 254, web.arena.CurrentMatch.Blue1)
	ws.Write("substituteTeams", map[string]int{"Red1": 0, "Red2": 0, "Red3": 0, "Blue1": 0, "Blue2": 0, "Blue3": 0})
	readWebsocketType(t, ws, "matchLoad")
	assert.Equal(t, 0, web.arena.CurrentMatch.Blue1)
	ws.Write("toggleBypass", nil)
	assert.Contains(t, readWebsocketError(t, ws), "Failed to parse")
	ws.Write("toggleBypass", "R4")
	assert.Contains(t, readWebsocketError(t, ws), "Invalid alliance station")
	ws.Write("toggleBypass", "R3")
	readWebsocketType(t, ws, "arenaStatus")
	assert.Equal(t, true, web.arena.AllianceStations["R3"].Bypass)
	ws.Write("toggleBypass", "R3")
	readWebsocketType(t, ws, "arenaStatus")
	assert.Equal(t, false, web.arena.AllianceStations["R3"].Bypass)

	// Go through match flow.
	web.arena.AudienceDisplayMode = "intro"
	ws.Write("abortMatch", nil)
	assert.Contains(t, readWebsocketError(t, ws), "cannot abort match")
	ws.Write("startMatch", nil)
	assert.Contains(t, readWebsocketError(t, ws), "cannot start match")
	web.arena.AllianceStations["R1"].Bypass = true
	web.arena.AllianceStations["R2"].Bypass = true
	web.arena.AllianceStations["R3"].Bypass = true
	web.arena.AllianceStations["B1"].Bypass = true
	web.arena.AllianceStations["B2"].Bypass = true
	web.arena.AllianceStations["B3"].Bypass = true
	ws.Write("startMatch", nil)
	readWebsocketType(t, ws, "eventStatus")
	assert.Equal(t, field.StartMatch, web.arena.MatchState)
	ws.Write("commitAndPost", nil)
	assert.Contains(t, readWebsocketError(t, ws), "cannot commit match while it is in progress")
	ws.Write("discardResults", nil)
	assert.Contains(t, readWebsocketError(t, ws), "cannot reset match while it is in progress")
	ws.Write("abortMatch", nil)
	readWebsocketType(t, ws, "audienceDisplayMode")
	assert.Equal(t, field.PostMatch, web.arena.MatchState)

	// Score something after the match, the way the panels would; each change sends the score once.
	scorer := scoring.Source{Kind: scoring.SourceScorer, ID: "test"}
	for _, change := range []scoring.Change{
		towerChange("red", 1, "endgame_tower", "level1"), towerChange("red", 2, "endgame_tower", "level2"),
		towerChange("blue", 1, "auto_tower", "level1"),
	} {
		change.Source = scorer
		_, err = web.arena.ApplyScoreChange(change)
		assert.Nil(t, err)
		realtimeScore := readWebsocketType(t, ws, "realtimeScore").(map[string]any)
		assert.Equal(t, web.arena.Game.Hash, realtimeScore["gameHash"])
	}
	ws.Write("commitAndPost", nil)
	readWebsocketTypes(
		t, ws, 8, "scorePosted", "matchLoad", "realtimeScore", "allianceStationDisplayMode", "scoringStatus",
		"audienceDisplayMode",
	)
	savedState := web.arena.SavedMatchResult.State
	assert.Equal(t, "level1", savedState.Alliances["red"].Robots[0]["endgame_tower"].Option)
	assert.Equal(t, "level2", savedState.Alliances["red"].Robots[1]["endgame_tower"].Option)
	assert.Equal(t, "none", savedState.Alliances["red"].Robots[2]["endgame_tower"].Option)
	assert.Equal(t, "level1", savedState.Alliances["blue"].Robots[0]["auto_tower"].Option)
	assert.Equal(t, 3, len(web.arena.SavedMatchResult.Changes))
	assert.Equal(t, model.RedWonMatch, web.arena.SavedMatch.Status)
	assert.Equal(t, field.PreMatch, web.arena.MatchState)
	ws.Write("discardResults", nil)
	readWebsocketTypes(t, ws, 6, "matchLoad", "realtimeScore", "allianceStationDisplayMode", "scoringStatus")
	assert.Equal(t, field.PreMatch, web.arena.MatchState)

	// Test changing the displays.
	ws.Write("setAudienceDisplay", "logo")
	readWebsocketType(t, ws, "audienceDisplayMode")
	assert.Equal(t, "logo", web.arena.AudienceDisplayMode)
	ws.Write("setAllianceStationDisplay", "logo")
	readWebsocketType(t, ws, "allianceStationDisplayMode")
	assert.Equal(t, "logo", web.arena.AllianceStationDisplayMode)

	// Test changing timeout display text.
	ws.Write("setTimeoutDisplay", map[string]string{"Description": "Lunch Break", "NextMatchName": "Practice 2"})
	message := readWebsocketType(t, ws, "matchLoad").(map[string]any)
	assert.Equal(t, "Lunch Break", message["BreakDescription"])
	assert.Equal(t, "Practice 2", message["BreakNextMatchName"])

	ws.Write(
		"startTimeout",
		map[string]any{"Description": "Repair Break", "NextMatchName": "", "DurationSec": float64(90)},
	)
	messages := readWebsocketTypes(t, ws, 3, "matchLoad", "allianceStationDisplayMode")
	message = messages["matchLoad"].(map[string]any)
	assert.Equal(t, field.TimeoutActive, web.arena.MatchState)
	assert.Equal(t, "Repair Break", message["BreakDescription"])
	assert.Equal(t, "", message["BreakNextMatchName"])
}

func TestMatchPlayWebsocketLoadMatch(t *testing.T) {
	web := setupTestWeb(t)
	tournament.CreateTestAlliances(web.arena.Database, 8)
	web.arena.CreatePlayoffTournament()

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/match_play/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketMultiple(t, ws, 10)

	web.arena.Database.CreateTeam(&model.Team{Id: 101})
	web.arena.Database.CreateTeam(&model.Team{Id: 102})
	web.arena.Database.CreateTeam(&model.Team{Id: 103})
	web.arena.Database.CreateTeam(&model.Team{Id: 104})
	web.arena.Database.CreateTeam(&model.Team{Id: 105})
	web.arena.Database.CreateTeam(&model.Team{Id: 106})
	match := model.Match{
		Type:      model.Playoff,
		ShortName: "QF4-3",
		Status:    model.RedWonMatch,
		Red1:      101,
		Red2:      102,
		Red3:      103,
		Blue1:     104,
		Blue2:     105,
		Blue3:     106,
	}
	web.arena.Database.CreateMatch(&match)

	matchIdMessage := struct{ MatchId int }{match.Id}
	ws.Write("loadMatch", matchIdMessage)
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketMultiple(t, ws, 3)
	assert.Equal(t, 101, web.arena.CurrentMatch.Red1)
	assert.Equal(t, 102, web.arena.CurrentMatch.Red2)
	assert.Equal(t, 103, web.arena.CurrentMatch.Red3)
	assert.Equal(t, 104, web.arena.CurrentMatch.Blue1)
	assert.Equal(t, 105, web.arena.CurrentMatch.Blue2)
	assert.Equal(t, 106, web.arena.CurrentMatch.Blue3)

	// Load a test match.
	matchIdMessage.MatchId = 0
	ws.Write("loadMatch", matchIdMessage)
	readWebsocketType(t, ws, "matchLoad")
	readWebsocketMultiple(t, ws, 3)
	assert.Equal(t, 0, web.arena.CurrentMatch.Red1)
	assert.Equal(t, 0, web.arena.CurrentMatch.Red2)
	assert.Equal(t, 0, web.arena.CurrentMatch.Red3)
	assert.Equal(t, 0, web.arena.CurrentMatch.Blue1)
	assert.Equal(t, 0, web.arena.CurrentMatch.Blue2)
	assert.Equal(t, 0, web.arena.CurrentMatch.Blue3)

	// Load a nonexistent match.
	matchIdMessage.MatchId = 254
	ws.Write("loadMatch", matchIdMessage)
	assert.Contains(t, readWebsocketError(t, ws), "invalid match ID 254")
}

func TestMatchPlayWebsocketShowAndClearResult(t *testing.T) {
	web := setupTestWeb(t)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/match_play/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketMultiple(t, ws, 10)

	matchIdMessage := struct{ MatchId int }{1}
	ws.Write("showResult", matchIdMessage)
	assert.Contains(t, readWebsocketError(t, ws), "invalid match ID 1")

	match := model.Match{Type: model.Qualification, ShortName: "Q1", Status: model.TieMatch}
	web.arena.Database.CreateMatch(&match)
	ws.Write("showResult", matchIdMessage)
	assert.Contains(t, readWebsocketError(t, ws), "No result found")

	web.arena.Database.CreateMatchResult(model.BuildTestMatchResult(web.arena.Database, match.Id, 1))
	assert.Nil(t, web.arena.Database.CreateRanking(testRanking1()))
	ws.Write("showResult", matchIdMessage)
	readWebsocketType(t, ws, "scorePosted")
	assert.Equal(t, match.Id, web.arena.SavedMatch.Id)
	assert.Equal(t, match.Id, web.arena.SavedMatchResult.MatchId)
	assert.Equal(t, 1, len(web.arena.SavedRankings))

	matchIdMessage.MatchId = 0
	ws.Write("showResult", matchIdMessage)
	readWebsocketType(t, ws, "scorePosted")
	assert.Equal(t, model.Match{}, *web.arena.SavedMatch)
	assert.Equal(t, *model.NewMatchResult(web.arena.Game), *web.arena.SavedMatchResult)
	assert.Empty(t, web.arena.SavedRankings)
}

func TestMatchPlayWebsocketNotifications(t *testing.T) {
	web := setupTestWeb(t)

	web.arena.Database.CreateTeam(&model.Team{Id: 254})

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/match_play/websocket", nil)
	assert.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// Should get a few status updates right after connection.
	readWebsocketMultiple(t, ws, 10)

	web.arena.AudienceDisplayMode = "intro"
	web.arena.AllianceStationDisplayMode = "blank"
	web.arena.AllianceStations["R1"].Bypass = true
	web.arena.AllianceStations["R2"].Bypass = true
	web.arena.AllianceStations["R3"].Bypass = true
	web.arena.AllianceStations["B1"].Bypass = true
	web.arena.AllianceStations["B2"].Bypass = true
	web.arena.AllianceStations["B3"].Bypass = true
	assert.Nil(t, web.arena.StartMatch())
	web.arena.Update()
	messages := readWebsocketTypes(
		t, ws, 8, "matchTime", "audienceDisplayMode", "allianceStationDisplayMode", "eventStatus",
	)
	_, ok := messages["matchTime"]
	assert.True(t, ok)
	_, ok = messages["audienceDisplayMode"]
	assert.True(t, ok)
	_, ok = messages["allianceStationDisplayMode"]
	assert.True(t, ok)
	_, ok = messages["eventStatus"]
	assert.True(t, ok)
	web.arena.MatchStartTime = time.Now().Add(-3 * time.Second)
	web.arena.Update()
	var matchTime field.MatchTimeMessage
	err = mapstructure.Decode(readWebsocketTypeEventually(t, ws, "matchTime", 4), &matchTime)
	assert.Nil(t, err)
	assert.Equal(t, field.InMatch, matchTime.MatchState)
	assert.Equal(t, "auto", matchTime.PhaseId)
	assert.Equal(t, 3, matchTime.MatchTimeSec)
	web.arena.ScoringStatusNotifier.Notify()
	readWebsocketTypeEventually(t, ws, "scoringStatus", 4)

	// Should get a tick notification when an integer second threshold is crossed.
	web.arena.MatchStartTime = time.Now().Add(-time.Second - 10*time.Millisecond) // Crossed
	web.arena.Update()
	err = mapstructure.Decode(readWebsocketTypeEventually(t, ws, "matchTime", 4), &matchTime)
	assert.Nil(t, err)
	assert.Equal(t, field.InMatch, matchTime.MatchState)
	assert.Equal(t, "auto", matchTime.PhaseId)
	assert.Equal(t, 1, matchTime.MatchTimeSec)
	web.arena.MatchStartTime = time.Now().Add(-2*time.Second + 10*time.Millisecond) // Not crossed yet
	web.arena.Update()
	web.arena.MatchStartTime = time.Now().Add(-2*time.Second - 10*time.Millisecond) // Crossed
	web.arena.Update()
	err = mapstructure.Decode(readWebsocketTypeEventually(t, ws, "matchTime", 4), &matchTime)
	assert.Nil(t, err)
	assert.Equal(t, field.InMatch, matchTime.MatchState)
	assert.Equal(t, "auto", matchTime.PhaseId)
	assert.Equal(t, 2, matchTime.MatchTimeSec)

	// Check across a phase boundary: the state stays the same and the phase moves on.
	firstPhase := web.arena.Game.Match.Phases[0]
	web.arena.MatchStartTime = time.Now().Add(-time.Duration(firstPhase.Duration * float64(time.Second)))
	web.arena.Update()
	statusReceived, matchTime := readWebsocketStatusMatchTime(t, ws)
	assert.Equal(t, true, statusReceived)
	assert.Equal(t, field.InMatch, matchTime.MatchState)
	assert.Equal(t, 1, matchTime.PhaseIndex)
	assert.Equal(t, web.arena.Game.Match.Phases[1].ID, matchTime.PhaseId)
	assert.Equal(t, int(firstPhase.Duration), matchTime.MatchTimeSec)

	// At the end of the last phase the match is over and no phase is running.
	endTestMatch(web)
	_, matchTime = readWebsocketStatusMatchTime(t, ws)
	for matchTime.MatchState != field.PostMatch {
		err = mapstructure.Decode(readWebsocketTypeEventually(t, ws, "matchTime", 20), &matchTime)
		assert.Nil(t, err)
	}
	assert.Equal(t, -1, matchTime.PhaseIndex)
	assert.Equal(t, "", matchTime.PhaseId)
}

// Handles the status and matchTime messages arriving in either order.
func readWebsocketStatusMatchTime(t *testing.T, ws *websocket.Websocket) (bool, field.MatchTimeMessage) {
	return getStatusMatchTime(t, readWebsocketTypes(t, ws, 5, "arenaStatus", "matchTime"))
}

func getStatusMatchTime(t *testing.T, messages map[string]any) (bool, field.MatchTimeMessage) {
	_, statusReceived := messages["arenaStatus"]
	message, ok := messages["matchTime"]
	var matchTime field.MatchTimeMessage
	if assert.True(t, ok) {
		err := mapstructure.Decode(message, &matchTime)
		assert.Nil(t, err)
	}
	return statusReceived, matchTime
}

func readWebsocketTypes(
	t *testing.T,
	ws *websocket.Websocket,
	maxMessages int,
	expectedMessageTypes ...string,
) map[string]any {
	messages := make(map[string]any)
	expected := make(map[string]struct{})
	for _, messageType := range expectedMessageTypes {
		expected[messageType] = struct{}{}
	}

	for i := 0; i < maxMessages && len(expected) > 0; i++ {
		messageType, message, err := ws.ReadWithTimeout(time.Second)
		if !assert.Nil(t, err) {
			break
		}
		messages[messageType] = message
		delete(expected, messageType)
	}

	for messageType := range expected {
		assert.Failf(t, "Expected websocket message not received", "message type %q", messageType)
	}
	return messages
}

func readWebsocketTypeEventually(
	t *testing.T,
	ws *websocket.Websocket,
	expectedMessageType string,
	maxMessages int,
) any {
	return readWebsocketTypes(t, ws, maxMessages, expectedMessageType)[expectedMessageType]
}

// waitForMatchPlay polls a condition that a field action, carried out on the arena's worker, makes true.
func waitForMatchPlay(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A device bound to commit and next_match does what the Commit & Post button and loading the next match do.
func TestMatchPlayDeviceFieldActions(t *testing.T) {
	web := setupTestWeb(t)
	require.Nil(t, web.RegisterFieldActionHandlers())
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("table", "Scoring table")
	require.Nil(t, err)
	table, err := web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "table", Channels: []*device.Channel{
		{ID: "commit", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "next", Dir: device.DirIn, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	set, err := device.ParseBindings([]byte(`
inputs:
  - { id: commit, device: table, channel: commit, action: { field: commit } }
  - { id: next, device: table, channel: next, action: { field: next_match } }
`))
	require.Nil(t, err)
	_, err = web.arena.Devices.SaveBindings(set)
	require.Nil(t, err)
	press := func(channel string) {
		require.Nil(t, table.SetInput(channel, 1))
		require.Nil(t, table.SetInput(channel, 0))
		web.arena.Update()
	}
	refusal := func() string {
		info, _ := web.arena.Devices.Device("table")
		if info.Refusal == nil {
			return ""
		}
		return info.Refusal.Binding + ": " + info.Refusal.Reason
	}

	q1 := model.Match{Type: model.Qualification, TypeOrder: 1, ShortName: "Q1", LongName: "Qualification 1"}
	q2 := model.Match{Type: model.Qualification, TypeOrder: 2, ShortName: "Q2", LongName: "Qualification 2"}
	require.Nil(t, web.arena.Database.CreateMatch(&q1))
	require.Nil(t, web.arena.Database.CreateMatch(&q2))
	require.Nil(t, web.arena.LoadMatch(&q1))
	startTestMatch(t, web)
	endTestMatch(web)
	require.Equal(t, field.PostMatch, web.arena.MatchState)

	// Loading the next match never throws away a match waiting to be committed.
	press("next")
	waitForMatchPlay(t, func() bool {
		return refusal() == "next: next_match: the match on the field has not been committed; commit it, or "+
			"discard it on Match Play"
	}, "the next match to be refused")
	assert.Equal(t, q1.Id, web.arena.CurrentMatch.Id)

	press("commit")
	waitForMatchPlay(t, func() bool {
		match, _ := web.arena.Database.GetMatchById(q1.Id)
		return match != nil && match.IsComplete() && web.arena.CurrentMatch.Id == q2.Id
	}, "the commit and the next match")
	result, err := web.arena.Database.GetMatchResultForMatch(q1.Id)
	require.Nil(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, field.PreMatch, web.arena.MatchState)
	assert.Equal(t, q1.Id, web.arena.SavedMatch.Id, "the score is posted")

	// With nothing waiting, the next match is the first unplayed one.
	press("next")
	waitForMatchPlay(t, func() bool { return web.arena.CurrentMatch.Id == q2.Id }, "the next match")
	press("commit")
	waitForMatchPlay(t, func() bool {
		return refusal() == "commit: commit: cannot commit match while it is in progress"
	}, "a commit with nothing to commit to be refused")
}

// Match Play lists the device faults when there are any, and clears them.
func TestMatchPlayDeviceFaults(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("estop", "Red E-stop")
	require.Nil(t, err)
	manifest := &device.Manifest{Proto: 1, ID: "estop", Channels: []*device.Channel{
		{ID: "stop", Dir: device.DirIn, Type: device.TypeDigital, Safety: true},
	}}
	box, err := web.arena.DeviceVirtual.Connect(manifest)
	require.Nil(t, err)
	box.StartHeartbeat(0)
	set, err := device.ParseBindings([]byte(`
inputs:
  - { id: estop-r1, device: estop, channel: stop, action: { field: estop, station: R1 } }
`))
	require.Nil(t, err)
	_, err = web.arena.Devices.SaveBindings(set)
	require.Nil(t, err)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/match_play/websocket", nil)
	require.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)
	// Without faults, nothing more than before is sent.
	readWebsocketMultiple(t, ws, 10)

	box.Disconnect()
	faults := readWebsocketUntil(t, ws, "deviceFaults").([]any)
	if assert.Equal(t, 1, len(faults)) {
		fault := faults[0].(map[string]any)
		assert.Equal(t, "estop", fault["device"])
		assert.Equal(t, "Red E-stop", fault["deviceName"])
		assert.Equal(t, "estop-r1", fault["binding"])
		assert.Equal(t, "it disconnected", fault["reason"])
	}

	ws.Write("clearDeviceFaults", nil)
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "is offline; its fault can be cleared once it is back")
	box, err = web.arena.DeviceVirtual.Connect(manifest)
	require.Nil(t, err)
	box.StartHeartbeat(0)
	ws.Write("clearDeviceFaults", nil)
	assert.Empty(t, readWebsocketUntil(t, ws, "deviceFaults"))
	assert.False(t, web.arena.Devices.Faulted())
}
