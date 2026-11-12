// Copyright 2026 Team 254. All Rights Reserved.

package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func setupPlayoffCardTest(t *testing.T) (*Web, *model.Match) {
	web := setupTestWeb(t)
	tournament.CreateTestAlliances(web.arena.Database, 2)
	for _, id := range []int{101, 102, 103, 104, 201, 202, 203, 204, 999} {
		require.NoError(t, web.arena.Database.CreateTeam(&model.Team{Id: id, YellowCard: id == 201}))
	}
	web.arena.EventSettings.PlayoffType = model.SingleEliminationPlayoff
	web.arena.EventSettings.NumPlayoffAlliances = 2
	require.NoError(t, web.arena.CreatePlayoffTournament())
	require.NoError(t, web.arena.CreatePlayoffMatches(time.Now()))
	matches, err := web.arena.Database.GetMatchesByType(model.Playoff, false)
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	match := &matches[0]
	require.NoError(t, web.arena.LoadMatch(match))
	return web, match
}

func cardMessage(t *testing.T, message any) map[string]any {
	data, err := json.Marshal(message)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded
}

func TestPlayoffAllianceCardReviewAndCommit(t *testing.T) {
	web, match := setupPlayoffCardTest(t)
	db := web.arena.Database

	page := web.getHttpResponse("/match_review/current/edit")
	require.Equal(t, 200, page.Code)
	editData := readEditData(t, page.Body.String())
	assert.True(t, editData.IsPlayoff)

	// Before the match is committed, give the red alliance a card that does not disqualify and the blue alliance
	// one that does, and something for red to win with.
	edited := copyState(t, editData.State)
	edited.Alliances["red"].Robots[0]["auto_tower"].Option = "level1"
	edited.Alliances["red"].Cards = map[int]string{2: "yellow"}
	edited.Alliances["blue"].Cards = map[int]string{1: "red"}
	summary := postSummary(t, web, "current", edited)
	assert.False(t, summary.Alliances[0].Disqualified)
	assert.True(t, summary.Alliances[1].Disqualified)
	assert.Equal(t, "red", summary.Winner)
	response := postEditedState(t, web, "current", edited)
	require.Equal(t, 303, response.Code, response.Body.String())

	// In the playoffs a card that disqualifies takes the whole alliance out of the match. The stored state only
	// holds the card: the disqualification follows from it.
	state := liveState(t, web)
	assert.Equal(t, map[int]string{2: "yellow"}, state.Alliances["red"].Cards)
	assert.Equal(t, map[int]string{1: "red"}, state.Alliances["blue"].Cards)
	assert.False(t, state.Alliances["blue"].Disqualified)
	result, _, _ := web.arena.ScoreSnapshot()
	assert.False(t, result.Alliances["red"].Disqualified)
	assert.True(t, result.Alliances["blue"].Disqualified)
	alliance, _ := db.GetAllianceById(1)
	assert.False(t, alliance.YellowCard) // No carryover before commit.

	require.NoError(t, web.commitCurrentMatchScore())
	saved, err := db.GetMatchResultForMatch(match.Id)
	require.NoError(t, err)
	assert.Equal(t, model.Playoff, saved.MatchType)
	assert.Equal(t, map[int]string{2: "yellow"}, saved.State.Alliances["red"].Cards)
	assert.Equal(t, map[int]string{1: "red"}, saved.State.Alliances["blue"].Cards)
	committed, _ := db.GetMatchById(match.Id)
	assert.Equal(t, model.RedWonMatch, committed.Status)

	// A card of any kind is carried by the playoff alliance, not by the team that was shown it. Team cards from
	// the qualification rounds are left alone.
	alliance, _ = db.GetAllianceById(1)
	assert.True(t, alliance.YellowCard)
	alliance, _ = db.GetAllianceById(2)
	assert.True(t, alliance.YellowCard)
	team, _ := db.GetTeamById(104)
	assert.False(t, team.YellowCard)
	team, _ = db.GetTeamById(201)
	assert.True(t, team.YellowCard)

	// The posted result names the cards by robot position and lists the alliance member that did not play.
	posted := cardMessage(t, web.arena.GenerateScorePostedMessage())
	postedAlliances := posted["alliances"].(map[string]any)
	assert.Equal(t, map[string]any{"2": "yellow"}, postedAlliances["red"].(map[string]any)["cards"])
	assert.Equal(t, []any{float64(104)}, postedAlliances["red"].(map[string]any)["offFieldTeams"])
	assert.Equal(t, true, postedAlliances["blue"].(map[string]any)["disqualified"])
	assert.Equal(t, "red", posted["winner"])
	announcer := web.getHttpResponse("/displays/announcer/score_posted")
	require.Equal(t, 200, announcer.Code, announcer.Body.String())
	assert.Contains(t, announcer.Body.String(), "Yellow card</div>")
	assert.Contains(t, announcer.Body.String(), "Red card</div>")
	assert.Contains(t, announcer.Body.String(), "(disqualified)")

	// Substitute the fourth team, then a new team not yet in the roster: the alliance still carries its card.
	for _, substitute := range []int{104, 999} {
		require.NoError(t, web.arena.SubstituteTeams(substitute, 101, 103, 202, 201, 203))
		loaded := cardMessage(t, web.arena.GenerateMatchLoadMessage())
		assert.Equal(t, true, loaded["PlayoffRedAllianceYellowCard"])
		assert.Equal(t, true, loaded["PlayoffBlueAllianceYellowCard"])
	}

	// Historical corrections must refresh carryover without altering current uncommitted state.
	require.NoError(t, web.arena.LoadMatch(web.arena.CurrentMatch))
	startTestMatch(t, web)
	endTestMatch(web)
	_, err = web.arena.ApplyScoreChange(
		scoring.Change{
			Alliance: "red", Robot: 1, Element: "endgame_tower", Op: scoring.OpSet, Value: "level3",
			Source: scoring.Source{Kind: scoring.SourceScorer},
		},
	)
	require.NoError(t, err)
	corrected := copyState(t, saved.State)
	corrected.Alliances["red"].Cards = map[int]string{}
	response = postEditedState(t, web, match.Id, corrected)
	require.Equal(t, 303, response.Code, response.Body.String())
	loaded := cardMessage(t, web.arena.GenerateMatchLoadMessage())
	assert.Equal(t, false, loaded["PlayoffRedAllianceYellowCard"])
	assert.Equal(t, true, loaded["PlayoffBlueAllianceYellowCard"])
	assert.Equal(t, "level3", liveState(t, web).Alliances["red"].Robots[0]["endgame_tower"].Option)
}

func TestPlayoffCardPreviewAndCorrection(t *testing.T) {
	web, match := setupPlayoffCardTest(t)
	g := web.arena.Game
	require.NoError(t, web.arena.Database.SaveGameSnapshot(g))

	// A result in which both alliances score the same, so that only a disqualification decides it.
	matchResult := model.NewMatchResult(g)
	matchResult.MatchId = match.Id
	matchResult.MatchType = model.Playoff
	matchResult.Teams = teamsForGame(g, match)
	for _, alliance := range []string{"red", "blue"} {
		_, err := matchResult.Edit(
			g,
			scoring.Change{
				Alliance: alliance, Robot: 1, Element: "auto_tower", Op: scoring.OpSet, Value: "level1",
				Source: scoring.Source{Kind: scoring.SourceAdmin},
			},
		)
		require.NoError(t, err)
	}
	require.NoError(t, web.commitMatchScore(match, matchResult, true))

	for _, tc := range []struct {
		name             string
		card             string
		disqualifiedFlag bool
		disqualified     bool
		carried          bool
	}{
		{"disqualifying card", "red", false, true, true},
		{"card that does not disqualify", "yellow", false, false, true},
		{"no card", "", false, false, false},
		{"disqualified without a card", "", true, true, false},
		{"nothing again", "", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := web.arena.Database.GetMatchResultForMatch(match.Id)
			require.NoError(t, err)
			edited := copyState(t, stored.State)
			edited.Alliances["red"].Cards = map[int]string{}
			if tc.card != "" {
				edited.Alliances["red"].Cards[2] = tc.card
			}
			edited.Alliances["red"].Disqualified = tc.disqualifiedFlag

			// What the editor previews is what gets committed.
			preview := postSummary(t, web, match.Id, edited)
			assert.Equal(t, tc.disqualified, preview.Alliances[0].Disqualified)
			response := postEditedState(t, web, match.Id, edited)
			require.Equal(t, 303, response.Code, response.Body.String())

			stored, err = web.arena.Database.GetMatchResultForMatch(match.Id)
			require.NoError(t, err)
			committed, err := computeResultView(web.arena.Database, match, stored)
			require.NoError(t, err)
			assert.Equal(t, preview.Alliances, committed.Alliances)
			assert.Equal(t, preview.Winner, committed.Winner)

			updated, _ := web.arena.Database.GetMatchById(match.Id)
			if tc.disqualified {
				assert.Equal(t, "blue", committed.Winner)
				assert.Equal(t, "0", committed.Alliances[0].Score)
				assert.Equal(t, model.BlueWonMatch, updated.Status)
			} else {
				assert.Equal(t, "", committed.Winner)
				assert.Equal(t, model.TieMatch, updated.Status)
			}
			alliance, _ := web.arena.Database.GetAllianceById(1)
			assert.Equal(t, tc.carried, alliance.YellowCard)
		})
	}
}
