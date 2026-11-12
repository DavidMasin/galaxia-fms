// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// stateJson returns a raw state as JSON, which is how two states are compared: a state that has been stored or sent
// to a browser has lost the difference between an empty map and none.
func stateJson(t *testing.T, state *scoring.State) string {
	data, err := json.Marshal(state)
	require.NoError(t, err)
	return string(data)
}

// copyState returns a deep copy of a raw state, made the way a browser would: through JSON.
func copyState(t *testing.T, state *scoring.State) *scoring.State {
	data, err := json.Marshal(state)
	require.NoError(t, err)
	var copied scoring.State
	require.NoError(t, json.Unmarshal(data, &copied))
	return &copied
}

// postEditedState saves an edited raw state the way the result editor's form does.
func postEditedState(t *testing.T, web *Web, matchId any, state *scoring.State) *httptest.ResponseRecorder {
	data, err := json.Marshal(state)
	require.NoError(t, err)
	return web.postHttpResponse(
		fmt.Sprintf("/match_review/%v/edit", matchId), "stateJson="+url.QueryEscape(string(data)),
	)
}

// postSummary asks for the summary of an edited raw state the way the result editor's script does.
func postSummary(t *testing.T, web *Web, matchId any, state *scoring.State) *resultView {
	data, err := json.Marshal(state)
	require.NoError(t, err)
	recorder := web.postHttpResponse(fmt.Sprintf("/match_review/%v/summary", matchId), string(data))
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	assert.Equal(t, "application/json", recorder.Header()["Content-Type"][0])
	var summary resultView
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &summary))
	return &summary
}

// readEditData extracts the data the result editor page embeds for its script.
func readEditData(t *testing.T, body string) *matchReviewEditData {
	matches := regexp.MustCompile(`initResultEditor\((.*)\);`).FindStringSubmatch(body)
	require.Equal(t, 2, len(matches), "the page does not embed the editor's data")
	var editData matchReviewEditData
	require.NoError(t, json.Unmarshal([]byte(matches[1]), &editData))
	return &editData
}

func TestMatchReview(t *testing.T) {
	web := setupTestWeb(t)

	match1 := model.Match{Type: model.Practice, ShortName: "P1", Status: model.RedWonMatch}
	match2 := model.Match{Type: model.Practice, ShortName: "P2"}
	match3 := model.Match{
		Type: model.Qualification, ShortName: "Q1", Status: model.BlueWonMatch, Red1: 101, Red2: 102, Red3: 103,
		Blue1: 104, Blue2: 105, Blue3: 106,
	}
	match4 := model.Match{Type: model.Playoff, ShortName: "SF1-1", Status: model.TieMatch}
	match5 := model.Match{Type: model.Playoff, ShortName: "SF1-2"}
	web.arena.Database.CreateMatch(&match1)
	web.arena.Database.CreateMatch(&match2)
	web.arena.Database.CreateMatch(&match3)
	web.arena.Database.CreateMatch(&match4)
	web.arena.Database.CreateMatch(&match5)
	assert.Nil(t, web.arena.Database.CreateMatchResult(model.BuildTestMatchResult(web.arena.Database, match3.Id, 1)))

	// Check that all matches are listed on the page.
	recorder := web.getHttpResponse("/match_review")
	assert.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, ">P1<")
	assert.Contains(t, body, ">P2<")
	assert.Contains(t, body, ">Q1<")
	assert.Contains(t, body, ">SF1-1<")
	assert.Contains(t, body, ">SF1-2<")
	// The two sides are headed by the names the game gives its alliances.
	assert.Contains(t, body, "Red Alliance")
	assert.Contains(t, body, "Blue Score")
	assert.Contains(t, body, "101, 102, 103")
	// A played qualification match shows each side's score and its ranking points, named by the game.
	assert.Regexp(t, `(?s)>\s*133\s*<span class="match-review-rps`, body)
	assert.Regexp(t, `(?s)>\s*289\s*<span class="match-review-rps`, body)
	assert.Contains(t, body, `title="Win: 0, Energized: 0, Supercharged: 0, Traversal: 0"`)
	assert.Contains(t, body, `title="Win: 3, Energized: 1, Supercharged: 0, Traversal: 1"`)
	assert.Contains(t, body, "(5 RP)")
}

func TestMatchReviewEditExistingResult(t *testing.T) {
	web := setupTestWeb(t)

	tournament.CreateTestAlliances(web.arena.Database, 8)
	web.arena.EventSettings.PlayoffType = model.SingleEliminationPlayoff
	web.arena.EventSettings.NumPlayoffAlliances = 8
	web.arena.CreatePlayoffTournament()
	web.arena.CreatePlayoffMatches(time.Now())

	match, _ := web.arena.Database.GetMatchByTypeOrder(model.Playoff, 36)
	match.Status = model.RedWonMatch
	web.arena.Database.UpdateMatch(match)
	matchResult := model.BuildTestMatchResult(web.arena.Database, match.Id, 1)
	matchResult.MatchType = match.Type
	assert.Nil(t, web.arena.Database.CreateMatchResult(matchResult))

	recorder := web.getHttpResponse("/match_review")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), ">QF4-3<")
	assert.Regexp(t, `(?s)>\s*133\s*</td>`, recorder.Body.String()) // The red score
	assert.Regexp(t, `(?s)>\s*289\s*</td>`, recorder.Body.String()) // The blue score
	assert.NotContains(t, recorder.Body.String(), "match-review-rps")

	// Check response for non-existent match.
	recorder = web.getHttpResponse(fmt.Sprintf("/match_review/%d/edit", 12345))
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "No such match")

	recorder = web.getHttpResponse(fmt.Sprintf("/match_review/%d/edit", match.Id))
	assert.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, " Quarterfinal 4-3 ")
	assert.Contains(t, body, "Played with the game <b>Rebuilt-style</b>")
	assert.Contains(t, body, "/static/js/match_review.js")
	// The page itself knows nothing of the game: the form is built from the data it embeds.
	templateOnly := body[:strings.Index(body, "initResultEditor(")]
	assert.NotContains(t, strings.SplitN(templateOnly, `id="auditLog"`, 2)[0], "Tower")
	editData := readEditData(t, body)
	assert.Equal(t, fmt.Sprint(match.Id), editData.MatchId)
	assert.False(t, editData.IsCurrent)
	assert.True(t, editData.IsPlayoff)
	assert.Equal(t, "rebuilt-style", editData.Game.ID)
	assert.Equal(t, matchResult.GameHash, editData.Game.Hash)
	assert.JSONEq(t, stateJson(t, matchResult.State), stateJson(t, editData.State))
	assert.Equal(t, "blue", editData.Summary.Winner)
	assert.Equal(t, "Blue", editData.Summary.WinnerName)
	if assert.Equal(t, 2, len(editData.Summary.Alliances)) {
		red, blue := editData.Summary.Alliances[0], editData.Summary.Alliances[1]
		assert.Equal(t, "red", red.Slot)
		assert.Equal(t, "133", red.Score)
		assert.Equal(t, "289", blue.Score)
		assert.Equal(t, "Auto fuel", red.Breakdown[0].Name)
		assert.Equal(t, 18.0, red.Breakdown[0].Value)
		assert.Equal(t, "85", blue.Breakdown[4].Text)
	}

	// The audit log lists every change with who made it, where in the match and what it did.
	assert.Contains(t, body, `id="auditLog"`)
	assert.Equal(t, len(matchResult.Changes), strings.Count(body, `action="/match_review/`+fmt.Sprint(match.Id)+`/undo"`))
	assert.Contains(t, body, "scorer: test")
	assert.Contains(t, body, "referee: test")
	assert.Contains(t, body, "Autonomous winner set to Blue")
	assert.Contains(t, body, "Red · Fuel set to 18")
	assert.Contains(t, body, "Blue · robot 3 · Endgame tower set to Level 1")
	assert.Contains(t, body, "Red · Major foul called")
	assert.Contains(t, body, "Red · robot 1 · Yellow card given")
	assert.Contains(t, body, "<td>post</td>")
	assert.Contains(t, body, "<td>Shift 1</td>")
	assert.Contains(t, body, "In effect")

	// Update the score to something else: red keeps only two endgame climbs, blue one auto climb and one major
	// foul, and nobody has a card.
	edited := copyState(t, editData.State)
	g := model.TestGame("rebuilt-style")
	blank := scoring.NewState(g)
	for _, alliance := range []string{"red", "blue"} {
		edited.Alliances[alliance].Elements = blank.Alliances[alliance].Elements
		edited.Alliances[alliance].Robots = blank.Alliances[alliance].Robots
		edited.Alliances[alliance].Fouls = []scoring.Foul{}
		edited.Alliances[alliance].Cards = map[int]string{}
	}
	edited.Field = blank.Field
	edited.Alliances["red"].Robots[1]["endgame_tower"].Option = "level2"
	edited.Alliances["red"].Robots[2]["endgame_tower"].Option = "level1"
	edited.Alliances["blue"].Robots[0]["auto_tower"].Option = "level1"
	edited.Alliances["blue"].Fouls = []scoring.Foul{{Type: "minor", Rule: "G418", Robot: 2}}

	// The summary of the edited state comes from the server and saves nothing.
	summary := postSummary(t, web, match.Id, edited)
	assert.Equal(t, "35", summary.Alliances[0].Score)
	assert.Equal(t, "15", summary.Alliances[1].Score)
	assert.Equal(t, "red", summary.Winner)
	assert.Equal(t, 0, len(summary.Errors))
	stored, _ := web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, len(matchResult.Changes), len(stored.Changes))

	recorder = postEditedState(t, web, match.Id, edited)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, "/match_review", recorder.Header().Get("Location"))

	// Check for the updated scores back on the match list page.
	recorder = web.getHttpResponse("/match_review")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), ">QF4-3<")
	assert.Regexp(t, `(?s)>\s*35\s*</td>`, recorder.Body.String()) // The red score
	assert.Regexp(t, `(?s)>\s*15\s*</td>`, recorder.Body.String()) // The blue score
	assert.NotContains(t, recorder.Body.String(), "match-review-rps")

	// Every difference became a change by the match review page, made after the match, and nothing was rewritten.
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, 1, stored.PlayNumber)
	assert.Greater(t, len(stored.Changes), len(matchResult.Changes))
	assert.Equal(t, matchResult.Changes, stored.Changes[:len(matchResult.Changes)])
	for _, change := range stored.Changes[len(matchResult.Changes):] {
		assert.Equal(t, scoring.SourceAdmin, change.Source.Kind)
		assert.Equal(t, "match_review", change.Source.ID)
		assert.Equal(t, scoring.PhasePost, change.Phase)
		if change.Element == "fuel" {
			// A counter is corrected phase by phase.
			assert.NotEqual(t, "", change.Bucket)
		}
	}
	assert.Equal(t, 0, len(stored.State.Alliances["red"].Fouls))
	if assert.Equal(t, 1, len(stored.State.Alliances["blue"].Fouls)) {
		assert.Equal(t, scoring.Foul{
			ID: stored.State.Alliances["blue"].Fouls[0].ID, Type: "minor", Rule: "G418", Robot: 2, Phase: "post",
		}, stored.State.Alliances["blue"].Fouls[0])
	}
	assert.Equal(t, 0, stored.State.Alliances["red"].Elements["fuel"].Total())
	match, _ = web.arena.Database.GetMatchById(match.Id)
	assert.Equal(t, model.RedWonMatch, match.Status)

	// Saving the state as it is stored changes nothing.
	changesBefore := len(stored.Changes)
	recorder = postEditedState(t, web, match.Id, copyState(t, stored.State))
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, changesBefore, len(stored.Changes))

	// A change can be undone from the audit log; the winner is worked out again.
	var foulSeq int
	for _, change := range stored.Changes {
		if change.Element == scoring.TargetFoul && change.Op == scoring.OpAdd && change.Alliance == "blue" {
			foulSeq = change.Seq
		}
	}
	recorder = web.postHttpResponse(fmt.Sprintf("/match_review/%d/undo", match.Id), fmt.Sprintf("seq=%d", foulSeq))
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, fmt.Sprintf("/match_review/%d/edit", match.Id), recorder.Header().Get("Location"))
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, 0, len(stored.State.Alliances["blue"].Fouls))
	assert.Equal(t, foulSeq, stored.Changes[len(stored.Changes)-1].Reverts)
	recorder = web.getHttpResponse(fmt.Sprintf("/match_review/%d/edit", match.Id))
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf("Undone by #%d", stored.Changes[len(stored.Changes)-1].Seq))
	assert.Contains(t, recorder.Body.String(), fmt.Sprintf("Undo of #%d (Foul called (G418))", foulSeq))
	assert.Contains(t, recorder.Body.String(), "audit-log-undone")
	assert.Contains(t, recorder.Body.String(), ">Redo</button>")
	assert.Equal(t, "30", readEditData(t, recorder.Body.String()).Summary.Alliances[0].Score)

	// Undoing the undo record brings the foul back; a change cannot be undone twice.
	undoSeq := stored.Changes[len(stored.Changes)-1].Seq
	recorder = web.postHttpResponse(fmt.Sprintf("/match_review/%d/undo", match.Id), fmt.Sprintf("seq=%d", undoSeq))
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, 1, len(stored.State.Alliances["blue"].Fouls))
	recorder = web.postHttpResponse(fmt.Sprintf("/match_review/%d/undo", match.Id), "seq=99999")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "no score change with sequence number 99999")
	recorder = web.postHttpResponse(fmt.Sprintf("/match_review/%d/undo", match.Id), "seq=abc")
	assert.Equal(t, 500, recorder.Code)

	// A state that cannot be reached is refused as a whole, and the editor says why.
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	changesBefore = len(stored.Changes)
	invalid := copyState(t, stored.State)
	invalid.Alliances["red"].Robots[0]["endgame_tower"].Option = "level9"
	invalid.Alliances["blue"].Robots[2]["endgame_tower"].Option = "level3"
	summary = postSummary(t, web, match.Id, invalid)
	if assert.Equal(t, 1, len(summary.Errors)) {
		assert.Contains(t, summary.Errors[0], `endgame_tower has no option "level9"`)
	}
	recorder = postEditedState(t, web, match.Id, invalid)
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Nothing was saved")
	assert.Contains(t, recorder.Body.String(), "endgame_tower has no option")
	stored, _ = web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, changesBefore, len(stored.Changes))
	recorder = web.postHttpResponse(fmt.Sprintf("/match_review/%d/edit", match.Id), "stateJson=notjson")
	assert.Equal(t, 500, recorder.Code)
}

func TestMatchReviewEditUsesTheGameOfTheResult(t *testing.T) {
	web := setupTestWeb(t)

	// A result played with one game is edited with that game after the event has moved on to another.
	match := model.Match{
		Type: model.Qualification, ShortName: "Q1", LongName: "Qualification 1", Status: model.BlueWonMatch, Red1: 101,
		Red2: 102, Red3: 103, Blue1: 104, Blue2: 105, Blue3: 106,
	}
	web.arena.Database.CreateMatch(&match)
	assert.Nil(t, web.arena.Database.CreateMatchResult(model.BuildTestMatchResult(web.arena.Database, match.Id, 1)))
	selectTestGame(t, web, "ball-count")

	recorder := web.getHttpResponse(fmt.Sprintf("/match_review/%d/edit", match.Id))
	assert.Equal(t, 200, recorder.Code)
	editData := readEditData(t, recorder.Body.String())
	assert.Equal(t, "rebuilt-style", editData.Game.ID)
	assert.False(t, editData.IsPlayoff)
	assert.Equal(t, "289", editData.Summary.Alliances[1].Score)
	assert.Equal(t, "Energized", editData.Summary.Alliances[1].RankingPoints[1].Name)

	edited := copyState(t, editData.State)
	edited.Alliances["red"].Elements["fuel"].Counts["endgame"] = 200
	recorder = postEditedState(t, web, match.Id, edited)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	stored, _ := web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, "rebuilt-style", stored.GameId)
	lastChange := stored.Changes[len(stored.Changes)-1]
	assert.Equal(t, "fuel", lastChange.Element)
	assert.Equal(t, "endgame", lastChange.Bucket)
	assert.Equal(t, 200.0, lastChange.Value)
	updated, _ := web.arena.Database.GetMatchById(match.Id)
	assert.Equal(t, model.RedWonMatch, updated.Status)

	// The rankings were recalculated: with the result's own game for the points, sorted by the active game's order.
	rankings, _ := web.arena.Database.GetAllRankings()
	if assert.Equal(t, 6, len(rankings)) {
		assert.Equal(t, 1, rankings[0].Wins)
		assert.Contains(t, []int{101, 102, 103}, rankings[0].TeamId)
		assert.Equal(t, len(web.arena.Game.Ranking.Order), len(rankings[0].Sort))
	}
}

func TestMatchReviewCreateNewResult(t *testing.T) {
	web := setupTestWeb(t)

	tournament.CreateTestAlliances(web.arena.Database, 8)
	web.arena.EventSettings.PlayoffType = model.SingleEliminationPlayoff
	web.arena.EventSettings.NumPlayoffAlliances = 8
	web.arena.CreatePlayoffTournament()
	web.arena.CreatePlayoffMatches(time.Now())

	match, _ := web.arena.Database.GetMatchByTypeOrder(model.Playoff, 36)
	match.Status = model.RedWonMatch
	web.arena.Database.UpdateMatch(match)

	recorder := web.getHttpResponse("/match_review")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), ">QF4-3<")
	assert.NotContains(t, recorder.Body.String(), ">13<") // The red score
	assert.NotContains(t, recorder.Body.String(), ">10<") // The blue score

	// A match that was never played is scored from scratch with the active game.
	recorder = web.getHttpResponse(fmt.Sprintf("/match_review/%d/edit", match.Id))
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), " Quarterfinal 4-3 ")
	assert.Contains(t, recorder.Body.String(), "Nothing has been scored in this match yet.")
	editData := readEditData(t, recorder.Body.String())
	assert.Equal(t, web.arena.Game.ID, editData.Game.ID)
	assert.Equal(t, []int{match.Red1, match.Red2, match.Red3}, editData.Teams["red"])
	assert.Equal(t, []int{match.Blue1, match.Blue2, match.Blue3}, editData.Teams["blue"])
	assert.Equal(t, "", editData.Summary.Winner)

	// Update the score to something else.
	edited := copyState(t, editData.State)
	edited.Alliances["red"].Robots[1]["endgame_tower"].Option = "level2"
	edited.Alliances["red"].Robots[2]["endgame_tower"].Option = "level1"
	edited.Alliances["blue"].Robots[0]["auto_tower"].Option = "level1"
	edited.Alliances["blue"].Fouls = []scoring.Foul{{Type: "minor", Robot: 1}}
	edited.Alliances["red"].Cards = map[int]string{2: "yellow"}
	recorder = postEditedState(t, web, match.Id, edited)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())

	// Check for the updated scores back on the match list page.
	recorder = web.getHttpResponse("/match_review")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), ">QF4-3<")
	assert.Regexp(t, `(?s)>\s*35\s*</td>`, recorder.Body.String()) // The red score
	assert.Regexp(t, `(?s)>\s*15\s*</td>`, recorder.Body.String()) // The blue score

	stored, _ := web.arena.Database.GetMatchResultForMatch(match.Id)
	if assert.NotNil(t, stored) {
		assert.Equal(t, 1, stored.PlayNumber)
		assert.Equal(t, model.Playoff, stored.MatchType)
		assert.Equal(t, web.arena.Game.Hash, stored.GameHash)
		assert.Equal(t, []int{match.Red1, match.Red2, match.Red3}, stored.Teams["red"])
		assert.Equal(t, map[int]string{match.Red2: "yellow"}, stored.TeamCards("red"))
	}
	// The playoff alliance now carries a card into its next match.
	alliance, _ := web.arena.Database.GetAllianceById(match.PlayoffRedAlliance)
	assert.True(t, alliance.YellowCard)
}

func TestMatchReviewEditCurrentMatch(t *testing.T) {
	web := setupTestWeb(t)

	match := model.Match{
		Type:      model.Qualification,
		LongName:  "Qualification 352",
		ShortName: "Q352",
		Red1:      1001,
		Red2:      1002,
		Red3:      1003,
		Blue1:     1004,
		Blue2:     1005,
		Blue3:     1006,
	}
	web.arena.Database.CreateMatch(&match)
	web.arena.LoadMatch(&match)
	assert.Equal(t, match, *web.arena.CurrentMatch)

	recorder := web.getHttpResponse("/match_review/current/edit")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), " Qualification 352 ")
	editData := readEditData(t, recorder.Body.String())
	assert.Equal(t, "current", editData.MatchId)
	assert.True(t, editData.IsCurrent)
	assert.Equal(t, []int{1001, 1002, 1003}, editData.Teams["red"])

	// Play the match, then correct its score before it is committed.
	startTestMatch(t, web)
	endTestMatch(web)
	assert.Equal(t, field.PostMatch, web.arena.MatchState)
	edited := copyState(t, editData.State)
	edited.Alliances["red"].Robots[1]["endgame_tower"].Option = "level2"
	edited.Alliances["red"].Robots[2]["endgame_tower"].Option = "level1"
	edited.Alliances["red"].Elements["fuel"].Counts = map[string]int{"auto": 4, "shift2": 6}
	edited.Alliances["blue"].Robots[0]["auto_tower"].Option = "level1"
	edited.Alliances["blue"].Fouls = []scoring.Foul{{Type: "major", Rule: "G403", Robot: 3}}
	edited.Alliances["red"].Cards = map[int]string{1: "yellow"}
	edited.Field["auto_winner"].Option = "red"

	// The only changes so far are the game's own: its rule that decides the winner of autonomous.
	changesBefore := len(mustBuildMatchResult(t, web).Changes)
	summary := postSummary(t, web, "current", edited)
	assert.Equal(t, "55", summary.Alliances[0].Score)
	assert.Equal(t, "15", summary.Alliances[1].Score)
	assert.Equal(t, changesBefore, len(mustBuildMatchResult(t, web).Changes))

	recorder = postEditedState(t, web, "current", edited)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, "/match_play", recorder.Header().Get("Location"))

	// Check that the persisted match is still unedited and that the realtime scores have been updated instead.
	stored, _ := web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Nil(t, stored)
	state := liveState(t, web)
	assert.Equal(t, "level2", state.Alliances["red"].Robots[1]["endgame_tower"].Option)
	assert.Equal(t, "level1", state.Alliances["red"].Robots[2]["endgame_tower"].Option)
	assert.Equal(t, map[string]int{"auto": 4, "shift2": 6}, state.Alliances["red"].Elements["fuel"].Counts)
	assert.Equal(t, "level1", state.Alliances["blue"].Robots[0]["auto_tower"].Option)
	assert.Equal(t, 1, len(state.Alliances["blue"].Fouls))
	assert.Equal(t, map[int]string{1: "yellow"}, state.Alliances["red"].Cards)
	assert.Equal(t, "red", state.Field["auto_winner"].Option)
	result, _, _ := web.arena.ScoreSnapshot()
	assert.Equal(t, 55.0, result.Alliances["red"].Score)
	assert.Equal(t, 15.0, result.Alliances["blue"].Score)
	editChanges := mustBuildMatchResult(t, web).Changes[changesBefore:]
	assert.NotEmpty(t, editChanges)
	for _, change := range editChanges {
		assert.Equal(t, scoring.SourceAdmin, change.Source.Kind)
		assert.Equal(t, scoring.PhasePost, change.Phase)
	}

	// The audit log of the match on the field can be undone from as well.
	changes := mustBuildMatchResult(t, web).Changes
	recorder = web.getHttpResponse("/match_review/current/edit")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, len(changes), strings.Count(recorder.Body.String(), `action="/match_review/current/undo"`))
	var cardSeq int
	for _, change := range changes {
		if change.Element == scoring.TargetCard {
			cardSeq = change.Seq
		}
	}
	recorder = web.postHttpResponse("/match_review/current/undo", fmt.Sprintf("seq=%d", cardSeq))
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, "/match_review/current/edit", recorder.Header().Get("Location"))
	assert.Equal(t, 0, len(liveState(t, web).Alliances["red"].Cards))
}

func TestMatchReviewSummary(t *testing.T) {
	web := setupTestWeb(t)

	match := model.Match{
		Type: model.Qualification, ShortName: "Q1", Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105, Blue3: 106,
	}
	web.arena.Database.CreateMatch(&match)
	matchResult := model.BuildTestMatchResult(web.arena.Database, match.Id, 1)
	assert.Nil(t, web.arena.Database.CreateMatchResult(matchResult))

	// The summary of the stored state is the stored result.
	summary := postSummary(t, web, match.Id, copyState(t, matchResult.State))
	assert.Equal(t, "blue", summary.Winner)
	assert.Equal(t, "Blue", summary.WinnerName)
	assert.Equal(t, "blue", summary.WinnerSlot)
	if assert.Equal(t, 2, len(summary.Alliances)) {
		red, blue := summary.Alliances[0], summary.Alliances[1]
		assert.Equal(t, "red", red.Id)
		assert.Equal(t, "Red", red.Name)
		assert.Equal(t, []int{101, 102, 103}, red.Teams)
		assert.Equal(t, "133", red.Score)
		assert.Equal(t, "loss", red.Result)
		assert.Equal(t, "289", blue.Score)
		assert.Equal(t, "5", blue.RpTotal)
		assert.Equal(
			t,
			[]namedValue{
				{Id: "win", Name: "Win", Value: 3, Text: "3"}, {Id: "energized", Name: "Energized", Value: 1, Text: "1"},
				{Id: "supercharged", Name: "Supercharged", Value: 0, Text: "0"},
				{Id: "traversal", Name: "Traversal", Value: 1, Text: "1"},
			},
			blue.RankingPoints,
		)
		assert.Equal(t, 5, len(blue.Breakdown))
	}

	// Each kind of edit shows up in the summary: a count in a phase, a selection, a foul, a card, a disqualification.
	edited := copyState(t, matchResult.State)
	edited.Alliances["red"].Elements["fuel"].Counts["endgame"] += 300
	summary = postSummary(t, web, match.Id, edited)
	assert.Equal(t, "433", summary.Alliances[0].Score)
	assert.Equal(t, "red", summary.Winner)
	assert.Equal(t, 1.0, summary.Alliances[0].RankingPoints[1].Value)

	edited = copyState(t, matchResult.State)
	edited.Field["auto_winner"].Option = "red"
	summary = postSummary(t, web, match.Id, edited)
	assert.NotEqual(t, "133", summary.Alliances[0].Score)

	edited = copyState(t, matchResult.State)
	edited.Alliances["red"].Fouls = edited.Alliances["red"].Fouls[:2]
	summary = postSummary(t, web, match.Id, edited)
	assert.Equal(t, "224", summary.Alliances[1].Score)

	edited = copyState(t, matchResult.State)
	edited.Alliances["blue"].Disqualified = true
	summary = postSummary(t, web, match.Id, edited)
	assert.Equal(t, "0", summary.Alliances[1].Score)
	assert.True(t, summary.Alliances[1].Disqualified)
	assert.Equal(t, "red", summary.Winner)

	// A part of the state that is left out is taken as unchanged.
	summary = postSummary(t, web, match.Id, &scoring.State{})
	assert.Equal(t, "133", summary.Alliances[0].Score)
	assert.Equal(t, "289", summary.Alliances[1].Score)

	// None of this was saved.
	stored, _ := web.arena.Database.GetMatchResultForMatch(match.Id)
	assert.Equal(t, len(matchResult.Changes), len(stored.Changes))

	recorder := web.postHttpResponse(fmt.Sprintf("/match_review/%d/summary", match.Id), "notjson")
	assert.Equal(t, 400, recorder.Code)
	recorder = web.postHttpResponse("/match_review/12345/summary", "{}")
	assert.Equal(t, 500, recorder.Code)
}

func TestStateChanges(t *testing.T) {
	model.BaseDir = ".."
	g := model.TestGame("orbital-rush")
	from := scoring.NewState(g)
	apply := func(state *scoring.State, changes []scoring.Change) {
		for i := range changes {
			changes[i].Seq = i + 1
			changes[i].Phase = scoring.PhasePost
			assert.Nil(t, state.Apply(g, &changes[i]), "%+v", changes[i])
		}
	}

	// Nothing changed, nothing to do.
	assert.Empty(t, stateChanges(g, from, copyState(t, from), matchReviewSource))

	// Change one value of every type of element the example game has, for each scope it has it in, and check that
	// applying the changes to the old state gives the new one.
	to := copyState(t, from)
	changed := 0
	phases := g.Match.Phases
	edit := func(element string, value *scoring.Value) {
		definition := g.Element(element)
		switch definition.Type {
		case "counter":
			value.Counts = map[string]int{phases[0].ID: 2, phases[len(phases)-1].ID: 3}
		case "boolean":
			value.Bool, value.Phase = true, phases[0].ID
		case "enum":
			value.Option, value.Phase = definition.Options[len(definition.Options)-1].ID, phases[len(phases)-1].ID
		case "timer":
			value.Seconds = 4.5
		case "number":
			value.Number = 1
			if definition.Min != nil {
				value.Number = *definition.Min + 1
			}
		}
		changed++
	}
	types := make(map[string]bool)
	for _, element := range g.Elements {
		types[element.Type] = true
		switch element.Scope {
		case "field":
			edit(element.ID, to.Field[element.ID])
		case "alliance":
			edit(element.ID, to.Alliances[g.Alliances.List[0].ID].Elements[element.ID])
		case "robot":
			edit(element.ID, to.Alliances[g.Alliances.List[1].ID].Robots[1][element.ID])
		}
	}
	assert.Equal(t, 5, len(types), "the example game should have an element of every type")
	first, second := g.Alliances.List[0].ID, g.Alliances.List[1].ID
	to.Alliances[first].Fouls = []scoring.Foul{{Type: g.Fouls.Types[0].ID, Robot: 2}}
	to.Alliances[second].Cards = map[int]string{1: g.Cards[0].ID}
	to.Alliances[second].Disqualified = true

	changes := stateChanges(g, from, to, matchReviewSource)
	assert.GreaterOrEqual(t, len(changes), changed+3)
	for _, change := range changes {
		assert.Equal(t, matchReviewSource, change.Source)
	}
	result := scoring.NewState(g)
	apply(result, changes)
	// The foul's id is the sequence number of the change that added it, which the edited state cannot know.
	if assert.Equal(t, 1, len(result.Alliances[first].Fouls)) {
		result.Alliances[first].Fouls[0].ID = 0
		result.Alliances[first].Fouls[0].Phase = ""
	}
	assert.JSONEq(t, stateJson(t, to), stateJson(t, result))

	// Going back again undoes all of it: the fouls and cards are removed and the values reset. A list of fouls
	// that is left out means "unchanged", so the state to go back to says explicitly that there are none.
	from2 := scoring.NewState(g)
	apply(from2, stateChanges(g, from, to, matchReviewSource))
	target := copyState(t, from)
	target.Alliances[first].Fouls = []scoring.Foul{}
	assert.Empty(t, stateChanges(g, from2, copyState(t, from2), matchReviewSource))
	back := stateChanges(g, from2, target, matchReviewSource)
	apply(from2, back)
	assert.Equal(t, 0, len(from2.Alliances[first].Fouls))
	assert.Equal(t, 0, len(from2.Alliances[second].Cards))
	assert.False(t, from2.Alliances[second].Disqualified)
	for _, element := range g.Elements {
		if element.Type == "counter" && element.Scope == "alliance" {
			assert.Equal(t, 0, from2.Alliances[first].Elements[element.ID].Total())
		}
	}
}
