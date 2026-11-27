// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package web

import (
	"bytes"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupSettings(t *testing.T) {
	web := setupTestWeb(t)

	// Check the default setting values.
	recorder := web.getHttpResponse("/setup/settings")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Untitled Event")
	assert.Contains(t, recorder.Body.String(), "8")
	assert.NotContains(t, recorder.Body.String(), "tbaPublishingEnabled\"  checked")
	// The game-specific settings are a selector of the games that are installed and the parameters of each.
	assert.Contains(t, recorder.Body.String(), `<select class="form-select" id="gameId" name="gameId"`)
	assert.Regexp(t, `<option value="rebuilt-style" selected\s*>\s*Rebuilt-style\s*</option>`, recorder.Body.String())
	assert.Regexp(t, `<option value="ball-count"\s*>\s*Ball Count\s*</option>`, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `name="gameParam.rebuilt-style.energized_threshold"`)
	assert.Contains(t, recorder.Body.String(), "Energized fuel threshold")
	assert.Contains(t, recorder.Body.String(), `placeholder="100"`)
	assert.Contains(t, recorder.Body.String(), "At least 0.")
	assert.Contains(t, recorder.Body.String(), "takes effect when the arena returns to pre-match")
	assert.NotContains(t, recorder.Body.String(), "LED Lighting")
	assert.NotContains(t, recorder.Body.String(), "ledControllerAddress")

	// Change the settings and check the response.
	recorder = web.postHttpResponse(
		"/setup/settings",
		"name=Chezy Champs&code=CC&playoffType=single&numPlayoffAlliances=16&tbaPublishingEnabled=on&"+
			"tbaEventCode=2014cc&tbaSecretId=secretId&tbaSecret=tbasec",
	)
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "/setup/settings#event", recorder.Header().Get("Location"))
	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), "Chezy Champs")
	assert.Contains(t, recorder.Body.String(), "16")
	assert.Contains(t, recorder.Body.String(), "tbaPublishingEnabled\"  checked")
	assert.Contains(t, recorder.Body.String(), "2014cc")
	assert.Contains(t, recorder.Body.String(), "secretId")
	assert.Contains(t, recorder.Body.String(), "tbasec")
	// A form without a game selection leaves the game alone.
	assert.Equal(t, "rebuilt-style", web.arena.EventSettings.GameId)
	assert.Equal(t, "rebuilt-style", web.arena.Game.ID)

	recorder = web.postHttpResponse("/setup/settings", "name=Field Tab Event&activeSettingsTab=field")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "/setup/settings#field", recorder.Header().Get("Location"))
}

func TestSetupSettingsGame(t *testing.T) {
	web := setupTestWeb(t)

	// Override two parameters of the selected game. An empty input means the game's own value.
	recorder := web.postHttpResponse(
		"/setup/settings",
		"activeSettingsTab=game&gameId=rebuilt-style&gameParam.rebuilt-style.energized_threshold=80&"+
			"gameParam.rebuilt-style.supercharged_threshold=&gameParam.rebuilt-style.traversal_threshold=0",
	)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, "/setup/settings#game", recorder.Header().Get("Location"))
	assert.Equal(
		t, map[string]float64{"energized_threshold": 80, "traversal_threshold": 0}, web.arena.EventSettings.GameParams,
	)
	stored, _ := web.arena.Database.GetEventSettings()
	assert.Equal(t, web.arena.EventSettings.GameParams, stored.GameParams)
	recorder = web.getHttpResponse("/setup/settings")
	assert.Regexp(
		t, `name="gameParam.rebuilt-style.energized_threshold"\s+value="80" placeholder="100"`, recorder.Body.String(),
	)
	assert.Regexp(
		t, `name="gameParam.rebuilt-style.supercharged_threshold"\s+value="" placeholder="360"`, recorder.Body.String(),
	)

	// The overrides are what the match on the field is scored with: 80 fuel now earns the bonus, and a win is
	// worth two ranking points with the traversal bonus turned off.
	startTestMatch(t, web)
	_, err := web.arena.ApplyScoreChange(
		scoring.Change{
			Alliance: "red", Element: "fuel", Op: scoring.OpIncrement, Value: 80,
			Source: scoring.Source{Kind: scoring.SourceScorer},
		},
	)
	assert.Nil(t, err)
	result, _, _ := web.arena.ScoreSnapshot()
	assert.Equal(t, 1.0, result.Alliances["red"].RP["energized"])
	assert.Equal(t, 2.0, result.Alliances["red"].RP["win"])
	assert.Nil(t, web.arena.AbortMatch())
	assert.Nil(t, web.arena.ResetMatch())
	assert.Nil(t, web.arena.LoadTestMatch())

	// Values a parameter does not accept are refused, and nothing is saved.
	for body, message := range map[string]string{
		"gameId=rebuilt-style&gameParam.rebuilt-style.energized_threshold=lots": "Energized fuel threshold must be a number",
		"gameId=rebuilt-style&gameParam.rebuilt-style.energized_threshold=-1":   "Energized fuel threshold cannot be less than 0",
		"gameId=rebuilt-style&gameParam.rebuilt-style.energized_threshold=1.5":  "Energized fuel threshold must be a whole number",
		"gameId=nonexistent": "Game &#34;nonexistent&#34; is missing or has errors",
		// The orbital-rush example has three alliances, which the field cannot seat yet.
		"gameId=orbital-rush": "cannot be selected",
	} {
		recorder = web.postHttpResponse("/setup/settings", "name=Should Not Save&activeSettingsTab=game&"+body)
		assert.Equal(t, 200, recorder.Code)
		assert.Contains(t, recorder.Body.String(), message, body)
		assert.Equal(t, "Untitled Event", web.arena.EventSettings.Name)
		assert.Equal(t, "rebuilt-style", web.arena.EventSettings.GameId)
		assert.Equal(t, 80.0, web.arena.EventSettings.GameParams["energized_threshold"])
	}

	// A game that cannot be played on this field is listed, cannot be picked, and says why.
	recorder = web.getHttpResponse("/setup/settings")
	assert.Regexp(
		t, `<option value="orbital-rush"\s+disabled>\s*Orbital Rush \(cannot be selected\)`, recorder.Body.String(),
	)
	assert.Contains(t, recorder.Body.String(), "This game cannot be selected:")
	assert.Contains(t, recorder.Body.String(), "only games with exactly two alliances")

	// Select another game. Its own parameters are read, and those of the previous game are dropped.
	recorder = web.postHttpResponse(
		"/setup/settings", "gameId=ball-count&gameParam.rebuilt-style.energized_threshold=70&activeSettingsTab=game",
	)
	assert.Equal(t, 303, recorder.Code, recorder.Body.String())
	assert.Equal(t, "ball-count", web.arena.EventSettings.GameId)
	assert.Empty(t, web.arena.EventSettings.GameParams)
	assert.Equal(t, "ball-count", web.arena.Game.ID)
	recorder = web.getHttpResponse("/setup/settings")
	assert.Regexp(t, `<option value="ball-count" selected\s*>`, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "This game has no parameters to set.")
}

func TestParseGameParam(t *testing.T) {
	minimum, maximum := 1.0, 5.0
	number := &gamedef.Param{ID: "ratio", Type: "number", Min: &minimum, Max: &maximum}
	value, err := parseGameParam(number, "2.5")
	assert.Nil(t, err)
	assert.Equal(t, 2.5, value)
	_, err = parseGameParam(number, "5.5")
	assert.EqualError(t, err, "ratio cannot be more than 5.")
	_, err = parseGameParam(number, "NaN")
	assert.NotNil(t, err)

	flag := &gamedef.Param{ID: "overdrive", Name: "Overdrive", Type: "bool"}
	for raw, expected := range map[string]float64{"true": 1, "on": 1, "1": 1, "false": 0, "Off": 0, "0": 0} {
		value, err = parseGameParam(flag, raw)
		assert.Nil(t, err)
		assert.Equal(t, expected, value, raw)
	}
	_, err = parseGameParam(flag, "maybe")
	assert.EqualError(t, err, "Overdrive must be on or off.")
	assert.Equal(t, "true", formatGameParam(flag, 1))
	assert.Equal(t, "false", formatGameParam(flag, 0))
	assert.Equal(t, "2.5", formatGameParam(number, 2.5))
}

func TestSetupSettingsBlockedDuringMatch(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.Name = "Original Event"
	web.arena.MatchState = field.InMatch

	recorder := web.postHttpResponse("/setup/settings", "name=Changed Event&activeSettingsTab=field")

	assert.Equal(t, 200, recorder.Code)
	assert.Contains(
		t, recorder.Body.String(), "Settings cannot be changed while a match is in progress or is uncommitted.",
	)
	assert.Contains(t, recorder.Body.String(), "hash = \"#field\"")
	assert.Equal(t, "Original Event", web.arena.EventSettings.Name)
}

func TestSetupSettingsGameDuringMatch(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.Name = "Original Event"

	// With a match on the field, the game selection is the one thing that is saved; it does not take effect yet.
	startTestMatch(t, web)
	recorder := web.postHttpResponse("/setup/settings", "name=Changed Event&activeSettingsTab=game&gameId=ball-count")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(
		t, recorder.Body.String(), "The game selection was saved and will be used once the match on the field is over.",
	)
	assert.Contains(t, recorder.Body.String(), "settings cannot be changed while a match is in progress")
	assert.Contains(t, recorder.Body.String(), "The field is currently playing <b>Rebuilt-style</b>")
	assert.Equal(t, "Original Event", web.arena.EventSettings.Name)
	assert.Equal(t, "ball-count", web.arena.EventSettings.GameId)
	stored, _ := web.arena.Database.GetEventSettings()
	assert.Equal(t, "ball-count", stored.GameId)
	assert.Equal(t, "rebuilt-style", web.arena.Game.ID)

	// A selection that cannot be played is refused during a match as it is before one.
	recorder = web.postHttpResponse("/setup/settings", "activeSettingsTab=game&gameId=orbital-rush")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "cannot be selected")
	assert.Equal(t, "ball-count", web.arena.EventSettings.GameId)

	// The match is finished and committed with the game it started with, and the next one is loaded with the new.
	endTestMatch(web)
	assert.Equal(t, field.PostMatch, web.arena.MatchState)
	assert.Equal(t, "rebuilt-style", web.arena.Game.ID)
	assert.Nil(t, web.commitPostAndLoadNextMatch())
	assert.Equal(t, "rebuilt-style", web.arena.SavedMatchResult.GameId)
	assert.Equal(t, "ball-count", web.arena.Game.ID)
	assert.Equal(t, field.PreMatch, web.arena.MatchState)
	_, hasBalls := liveState(t, web).Alliances["red"].Elements["balls"]
	assert.True(t, hasBalls)
	recorder = web.getHttpResponse("/setup/settings")
	assert.NotContains(t, recorder.Body.String(), "The field is currently playing")
}

func TestSetupSettingsAllowedDuringTimeoutStates(t *testing.T) {
	for _, matchState := range []field.MatchState{field.TimeoutActive, field.PostTimeout} {
		web := setupTestWeb(t)
		web.arena.MatchState = matchState

		recorder := web.postHttpResponse("/setup/settings", "name=Changed Event")

		assert.Equal(t, 303, recorder.Code)
		assert.Equal(t, "Changed Event", web.arena.EventSettings.Name)
	}
}

func TestSettingsSaveAllowed(t *testing.T) {
	testCases := []struct {
		matchState field.MatchState
		allowed    bool
	}{
		{field.PreMatch, true},
		{field.Countdown, false},
		{field.InMatch, false},
		{field.PostMatch, false},
		{field.TimeoutActive, true},
		{field.PostTimeout, true},
	}

	for _, testCase := range testCases {
		assert.Equal(t, testCase.allowed, settingsSaveAllowed(testCase.matchState))
	}
}

func TestSetupSettingsDoubleElimination(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.postHttpResponse("/setup/settings", "playoffType=DoubleEliminationPlayoff&numPlayoffAlliances=4")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.DoubleEliminationPlayoff, web.arena.EventSettings.PlayoffType)
	assert.Equal(t, 4, web.arena.EventSettings.NumPlayoffAlliances)

	recorder = web.postHttpResponse("/setup/settings", "playoffType=DoubleEliminationPlayoff&numPlayoffAlliances=3")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Number of alliances for double elimination must be 4 or 8.")
	assert.Equal(t, 4, web.arena.EventSettings.NumPlayoffAlliances)
}

func TestSetupSettingsInvalidValues(t *testing.T) {
	web := setupTestWeb(t)
	recorder := web.postHttpResponse("/setup/settings", "playoffType=SingleEliminationPlayoff&numPlayoffAlliances=8")
	assert.Equal(t, 303, recorder.Code)

	// Invalid number of alliances.
	recorder = web.postHttpResponse("/setup/settings", "playoffType=SingleEliminationPlayoff&numAlliances=1")
	assert.Contains(t, recorder.Body.String(), "must be between 2 and 16")

	recorder = web.postHttpResponse("/setup/settings", "playoffType=DoubleEliminationPlayoff&numPlayoffAlliances=3")
	assert.Contains(t, recorder.Body.String(), "must be 4 or 8")

	// Changing the playoff type after alliance selection is finalized.
	assert.Nil(t, web.arena.Database.CreateAlliance(&model.Alliance{Id: 1}))
	recorder = web.postHttpResponse("/setup/settings", "playoffType=DoubleEliminationPlayoff")
	assert.Contains(t, recorder.Body.String(), "Cannot change playoff type or size after alliance selection")

	// Changing the playoff size after alliance selection is finalized.
	recorder = web.postHttpResponse("/setup/settings", "numPlayoffAlliances=2")
	assert.Contains(t, recorder.Body.String(), "Cannot change playoff type or size after alliance selection")
}

func TestSetupSettingsDeviceMqttPorts(t *testing.T) {
	web := setupTestWeb(t)

	// A form without the fields leaves the ports as they are, rather than turning the broker off.
	recorder := web.postHttpResponse("/setup/settings", "name=Ports Event")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.DefaultDeviceMqttPort, web.arena.EventSettings.DeviceMqttPort)
	assert.Equal(t, model.DefaultDeviceMqttWsPort, web.arena.EventSettings.DeviceMqttWsPort)

	recorder = web.postHttpResponse("/setup/settings", "deviceMqttPort=11883&deviceMqttWsPort=0")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, 11883, web.arena.EventSettings.DeviceMqttPort)
	assert.Equal(t, 0, web.arena.EventSettings.DeviceMqttWsPort)

	for _, body := range []string{
		"deviceMqttPort=70000", "deviceMqttPort=-1", "deviceMqttPort=", "deviceMqttWsPort=abc",
	} {
		recorder = web.postHttpResponse("/setup/settings", body)
		assert.Equal(t, 200, recorder.Code, body)
		assert.Contains(t, recorder.Body.String(), "must be numbers from 0 (off) to 65535", body)
	}
	recorder = web.postHttpResponse("/setup/settings", "deviceMqttPort=9000&deviceMqttWsPort=9000")
	assert.Contains(t, recorder.Body.String(), "must differ")
	assert.Equal(t, 11883, web.arena.EventSettings.DeviceMqttPort)
	assert.Equal(t, 0, web.arena.EventSettings.DeviceMqttWsPort)

	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), `name="deviceMqttPort"`)
	assert.Contains(t, recorder.Body.String(), `value="11883"`)
}

func TestSetupSettingsCountdown(t *testing.T) {
	web := setupTestWeb(t)
	assert.Equal(t, model.CountdownOff, web.arena.EventSettings.CountdownMode, "the test arena does without")

	// A form without the field leaves the countdown as it is.
	recorder := web.postHttpResponse("/setup/settings", "name=Countdown Event")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.CountdownOff, web.arena.EventSettings.CountdownMode)

	recorder = web.postHttpResponse("/setup/settings", "countdownMode=quick")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.CountdownQuick, web.arena.EventSettings.CountdownMode)
	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), `<option value="quick" selected>`)
	assert.Contains(
		t, recorder.Body.String(),
		"This game counts 3 beats after a spoken line before a match starts. Its quick countdown is one beat.",
	)
	assert.Contains(t, recorder.Body.String(), "/static/countdown_preview.html")

	recorder = web.postHttpResponse("/setup/settings", "countdownMode=")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.CountdownGame, web.arena.EventSettings.CountdownMode)
	settings, err := web.arena.Database.GetEventSettings()
	assert.Nil(t, err)
	assert.Equal(t, model.CountdownGame, settings.CountdownMode)

	recorder = web.postHttpResponse("/setup/settings", "countdownMode=slow")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "The countdown is played as the game says, quick, or not at all.")
	assert.Equal(t, model.CountdownGame, web.arena.EventSettings.CountdownMode)

	g := web.arena.Games.Get("ball-count")
	g.Match.Countdown.Voice = ""
	g.Match.Countdown.Beats = 1
	assert.Equal(
		t, "This game counts one beat before a match starts. Its quick countdown is one beat.", describeCountdown(g),
	)
	g.Match.Countdown.Enabled = false
	assert.Equal(t, "This game starts its matches without a countdown.", describeCountdown(g))
}

func TestSetupSettingsFieldProfile(t *testing.T) {
	web := setupTestWeb(t)
	assert.Equal(t, model.DefaultFieldProfile, web.arena.EventSettings.FieldProfile)

	// A new name makes a new profile and takes it into use.
	recorder := web.postHttpResponse("/setup/settings", "fieldProfile= tent ")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "tent", web.arena.EventSettings.FieldProfile)
	assert.Equal(t, "tent", web.arena.Devices.Profile())
	profiles, _ := web.arena.Database.GetAllFieldProfiles()
	assert.Equal(t, []string{"default", "tent"}, []string{profiles[0].Name, profiles[1].Name})

	// An existing name switches back without making another; a form without the field keeps the choice.
	recorder = web.postHttpResponse("/setup/settings", "fieldProfile=default")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.DefaultFieldProfile, web.arena.EventSettings.FieldProfile)
	recorder = web.postHttpResponse("/setup/settings", "name=No Profile Field")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, model.DefaultFieldProfile, web.arena.EventSettings.FieldProfile)
	profiles, _ = web.arena.Database.GetAllFieldProfiles()
	assert.Len(t, profiles, 2)

	recorder = web.postHttpResponse("/setup/settings", "fieldProfile=%20%20")
	assert.Contains(t, recorder.Body.String(), "The field profile needs a name.")
	assert.Equal(t, model.DefaultFieldProfile, web.arena.EventSettings.FieldProfile)

	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), `<option value="tent">`)
}

func TestSetupSettingsClearDb(t *testing.T) {
	createData := func(web *Web) {
		assert.Nil(t, web.arena.Database.CreateTeam(&model.Team{Id: 254}))
		assert.Nil(t, web.arena.Database.CreateMatch(&model.Match{Type: model.Practice}))
		assert.Nil(t, web.arena.Database.CreateMatch(&model.Match{Type: model.Qualification}))
		assert.Nil(t, web.arena.Database.CreateMatch(&model.Match{Type: model.Playoff}))
		assert.Nil(t, web.arena.Database.CreateMatchResult(&model.MatchResult{MatchId: 1, PlayNumber: 1}))
		assert.Nil(t, web.arena.Database.CreateMatchResult(&model.MatchResult{MatchId: 1, PlayNumber: 2}))
		assert.Nil(t, web.arena.Database.CreateMatchResult(&model.MatchResult{MatchId: 2, PlayNumber: 1}))
		assert.Nil(t, web.arena.Database.CreateMatchResult(&model.MatchResult{MatchId: 3, PlayNumber: 1}))
		assert.Nil(t, web.arena.Database.CreateRanking(&model.Ranking{TeamId: 254}))
		assert.Nil(t, web.arena.Database.CreateAlliance(&model.Alliance{Id: 1}))
		web.arena.AllianceSelectionAlliances = append(web.arena.AllianceSelectionAlliances, model.Alliance{Id: 1})
	}

	// Test clearing practice data.
	web := setupTestWeb(t)
	createData(web)
	recorder := web.postHttpResponse("/setup/db/clear/practice", "")
	assert.Equal(t, 303, recorder.Code)
	teams, _ := web.arena.Database.GetAllTeams()
	assert.NotEmpty(t, teams)
	matches, _ := web.arena.Database.GetMatchesByType(model.Practice, true)
	assert.Empty(t, matches)
	matchResult, _ := web.arena.Database.GetMatchResultForMatch(1)
	assert.Nil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Qualification, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(2)
	assert.NotNil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Playoff, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(3)
	assert.NotNil(t, matchResult)
	rankings, _ := web.arena.Database.GetAllRankings()
	assert.NotEmpty(t, rankings)
	tournament.CalculateRankings(web.arena.Database, web.arena.Game, false)
	assert.NotEmpty(t, rankings)
	alliances, _ := web.arena.Database.GetAllAlliances()
	assert.NotEmpty(t, alliances)
	assert.NotEmpty(t, web.arena.AllianceSelectionAlliances)

	// Test clearing qualification data.
	web = setupTestWeb(t)
	createData(web)
	recorder = web.postHttpResponse("/setup/db/clear/qualification", "")
	assert.Equal(t, 303, recorder.Code)
	teams, _ = web.arena.Database.GetAllTeams()
	assert.NotEmpty(t, teams)
	matches, _ = web.arena.Database.GetMatchesByType(model.Practice, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(1)
	assert.NotNil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Qualification, true)
	assert.Empty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(2)
	assert.Nil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Playoff, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(3)
	assert.NotNil(t, matchResult)
	rankings, _ = web.arena.Database.GetAllRankings()
	assert.Empty(t, rankings)
	tournament.CalculateRankings(web.arena.Database, web.arena.Game, false)
	assert.Empty(t, rankings)
	alliances, _ = web.arena.Database.GetAllAlliances()
	assert.NotEmpty(t, alliances)
	assert.NotEmpty(t, web.arena.AllianceSelectionAlliances)

	// Test clearing playoff data.
	web = setupTestWeb(t)
	createData(web)
	recorder = web.postHttpResponse("/setup/db/clear/playoff", "")
	assert.Equal(t, 303, recorder.Code)
	teams, _ = web.arena.Database.GetAllTeams()
	assert.NotEmpty(t, teams)
	matches, _ = web.arena.Database.GetMatchesByType(model.Practice, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(1)
	assert.NotNil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Qualification, true)
	assert.NotEmpty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(2)
	assert.NotNil(t, matchResult)
	matches, _ = web.arena.Database.GetMatchesByType(model.Playoff, true)
	assert.Empty(t, matches)
	matchResult, _ = web.arena.Database.GetMatchResultForMatch(3)
	assert.Nil(t, matchResult)
	rankings, _ = web.arena.Database.GetAllRankings()
	assert.NotEmpty(t, rankings)
	tournament.CalculateRankings(web.arena.Database, web.arena.Game, false)
	assert.NotEmpty(t, rankings)
	alliances, _ = web.arena.Database.GetAllAlliances()
	assert.Empty(t, alliances)
	assert.Empty(t, web.arena.AllianceSelectionAlliances)

	// Test with invalid match types.
	recorder = web.postHttpResponse("/setup/db/clear/all", "")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Invalid tournament stage to clear")
	recorder = web.postHttpResponse("/setup/db/clear/test", "")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Invalid tournament stage to clear")
}

func TestSetupSettingsBackupRestoreDb(t *testing.T) {
	web := setupTestWeb(t)

	// Modify a parameter so that we know when the database has been restored.
	web.arena.EventSettings.Name = "Chezy Champs"
	assert.Nil(t, web.arena.Database.UpdateEventSettings(web.arena.EventSettings))

	// Back up the database.
	recorder := web.getHttpResponse("/setup/db/save")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/octet-stream", recorder.HeaderMap["Content-Type"][0])
	backupBody := recorder.Body

	// Wipe the database to reset the defaults.
	web = setupTestWeb(t)
	assert.NotEqual(t, "Chezy Champs", web.arena.EventSettings.Name)

	// Check restoring with a missing file.
	recorder = web.postHttpResponse("/setup/db/restore", "")
	assert.Contains(t, recorder.Body.String(), "No database backup file was specified")
	assert.NotEqual(t, "Chezy Champs", web.arena.EventSettings.Name)

	// Check restoring with a corrupt file.
	recorder = web.postFileHttpResponse("/setup/db/restore", "databaseFile", bytes.NewBufferString("invalid"))
	assert.Contains(t, recorder.Body.String(), "Could not read uploaded database backup file")
	assert.NotEqual(t, "Chezy Champs", web.arena.EventSettings.Name)

	// Check restoring with the backup retrieved before.
	recorder = web.postFileHttpResponse("/setup/db/restore", "databaseFile", backupBody)
	assert.Equal(t, "Chezy Champs", web.arena.EventSettings.Name)
}

// After a backup is restored, the Devices, Bindings and Lighting pages show what the restored database holds and
// save to it, with no restart in between.
func TestSetupSettingsRestoreDbDevicesBindingsAndLighting(t *testing.T) {
	saveBindings := func(web *Web, id, action string) {
		recorder := web.requestWithBody("POST", "/setup/bindings/save",
			`{"inputs":[{"id":"`+id+`","device":"goal","channel":"beam","action":{"field":"`+action+`"}}]}`)
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
	}
	saveFixture := func(web *Web, channel, kind string) {
		code, response := postLighting(t, web, "/setup/lighting/fixtures", map[string]any{
			"create": true, "fixture": map[string]any{"id": channel, "device": "goal", "channel": channel, "kind": kind},
		})
		require.Equal(t, http.StatusOK, code, "%v", response)
	}

	// The event that is backed up: a device made on the Devices page, a binding of the goal's beam, and the goal's
	// horn as a lighting fixture.
	web, _ := setupLightingWeb(t)
	code, response := postDevicesPage(t, web, "/setup/devices/create", map[string]string{
		"id": "arcade-1", "name": "Arcade button",
	})
	require.Equal(t, 200, code, response)
	saveBindings(web, "backed-up", "start_match")
	saveFixture(web, "horn", "relay")
	recorder := web.getHttpResponse("/setup/db/save")
	require.Equal(t, 200, recorder.Code)
	backup := recorder.Body

	// The event it is restored over has other things: another device, another binding, the goal's strip as a fixture,
	// which is lit, and a connected device the backup has never seen.
	web, goal := setupLightingWeb(t)
	code, response = postDevicesPage(t, web, "/setup/devices/create", map[string]string{"id": "old-1"})
	require.Equal(t, 200, code, response)
	saveBindings(web, "not-backed-up", "field_reset")
	saveFixture(web, "strip", "led_strip")
	_, err := web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "extra", Channels: []*device.Channel{
		{ID: "button", Dir: device.DirIn, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	require.Nil(t, web.arena.Devices.Approve("extra"))
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}
	require.NotNil(t, goal.Output("strip"))
	replaced := web.arena.Database

	recorder = web.postFileHttpResponse("/setup/db/restore", "databaseFile", backup)
	require.Equal(t, 303, recorder.Code, recorder.Body.String())
	require.NotSame(t, replaced, web.arena.Database)

	// The Devices page: the backup's devices. The goal is in both and stays connected and approved; the device the
	// backup does not have is still connected, and waits for approval again.
	type shown struct {
		state  string
		online bool
	}
	devices := make(map[string]shown)
	for _, info := range getDevicesPageState(t, web).Devices {
		devices[info.ID] = shown{info.State, info.Online}
	}
	assert.Equal(t, map[string]shown{
		"arcade-1": {"approved", false}, "goal": {"approved", true}, "extra": {"pending", true},
	}, devices)

	// The Bindings page: the backup's set.
	recorder = web.getHttpResponse("/setup/bindings/state")
	require.Equal(t, 200, recorder.Code)
	bindings := decodeBindingsPageSet(t, recorder.Body.Bytes())
	if assert.Equal(t, 1, len(bindings.Set.Inputs)) {
		assert.Equal(t, "backed-up", bindings.Set.Inputs[0].ID)
	}
	assert.Empty(t, bindings.Problems)

	// The Lighting page: the backup's fixture, in force, and the strip let go of.
	data := getLightingData(t, web, "")
	fixtures := data["config"].(map[string]any)["fixtures"].([]any)
	if assert.Equal(t, 1, len(fixtures)) {
		assert.Equal(t, "horn", fixtures[0].(map[string]any)["id"])
	}
	assert.Equal(t, map[string]any{"horn": true}, data["status"].(map[string]any)["claimed"])
	assert.Nil(t, goal.Output("strip"))
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}
	assert.NotNil(t, goal.Output("horn"))

	// All three save, and to the restored database.
	code, response = postDevicesPage(t, web, "/setup/devices/create", map[string]string{"id": "new-1"})
	require.Equal(t, 200, code, response)
	code, response = postDevicesPage(t, web, "/setup/devices/extra/approve", nil)
	require.Equal(t, 200, code, response)
	record, err := web.arena.Database.GetDeviceByDeviceId("new-1")
	require.Nil(t, err)
	assert.NotNil(t, record)
	record, err = web.arena.Database.GetDeviceByDeviceId("extra")
	require.Nil(t, err)
	if assert.NotNil(t, record) {
		assert.Equal(t, model.DeviceApproved, record.State)
	}
	saveBindings(web, "saved-after", "volunteers")
	stored, err := web.arena.Database.GetBindingSet("ball-count", "default")
	require.Nil(t, err)
	if assert.NotNil(t, stored) {
		assert.Contains(t, stored.Yaml, "saved-after")
	}
	code, response = postLighting(t, web, "/setup/lighting/cell", map[string]any{
		"moment": "idle", "target": "horn", "look": map[string]any{"p": "blink"},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	profileId, err := web.arena.Database.GetLightingProfileId("default")
	require.Nil(t, err)
	scenes, err := web.arena.Database.GetLightingScenes("ball-count", profileId)
	require.Nil(t, err)
	if assert.NotNil(t, scenes) {
		assert.Equal(t, "blink", scenes.Cells["idle"]["horn"].P)
	}
}

func TestSetupSettingsPublishToTba(t *testing.T) {
	web := setupTestWeb(t)

	tbaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tbaServer.Close()

	web.arena.TbaClient.BaseUrl = tbaServer.URL
	web.arena.EventSettings.TbaPublishingEnabled = true

	recorder := web.getHttpResponse("/setup/settings/publish_teams")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "/setup/settings#publishing", recorder.Header().Get("Location"))

	web.arena.TbaClient.BaseUrl = "fakeurl"

	recorder = web.getHttpResponse("/setup/settings/publish_alliances")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Failed to publish alliances")
	assert.Contains(t, recorder.Body.String(), "hash = \"#publishing\"")

	recorder = web.getHttpResponse("/setup/settings/publish_awards")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Failed to publish awards")
	assert.Contains(t, recorder.Body.String(), "hash = \"#publishing\"")

	recorder = web.getHttpResponse("/setup/settings/publish_matches")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Failed to delete published matches")
	assert.Contains(t, recorder.Body.String(), "hash = \"#publishing\"")

	recorder = web.getHttpResponse("/setup/settings/publish_rankings")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Failed to publish rankings")
	assert.Contains(t, recorder.Body.String(), "hash = \"#publishing\"")

	recorder = web.getHttpResponse("/setup/settings/publish_teams")
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Failed to publish teams")
	assert.Contains(t, recorder.Body.String(), "hash = \"#publishing\"")
}

func (web *Web) postFileHttpResponse(path string, paramName string, file *bytes.Buffer) *httptest.ResponseRecorder {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile(paramName, "file.ext")
	io.Copy(part, file)
	writer.Close()
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

func TestSetupSettingsRadioOnlyNetwork(t *testing.T) {
	web := setupTestWeb(t)
	assert.False(t, web.arena.EventSettings.RadioOnlyNetwork)

	recorder := web.postHttpResponse(
		"/setup/settings", "name=Tent&playoffType=single&numPlayoffAlliances=8&networkSecurityEnabled=on&"+
			"radioOnlyNetwork=on",
	)
	assert.Equal(t, 303, recorder.Code)
	assert.True(t, web.arena.EventSettings.NetworkSecurityEnabled)
	assert.True(t, web.arena.EventSettings.RadioOnlyNetwork)
	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), "name=\"radioOnlyNetwork\"  checked")

	recorder = web.postHttpResponse(
		"/setup/settings", "name=Tent&playoffType=single&numPlayoffAlliances=8&networkSecurityEnabled=on",
	)
	assert.Equal(t, 303, recorder.Code)
	assert.False(t, web.arena.EventSettings.RadioOnlyNetwork)
}

func TestSetupSettingsApKeepTeams(t *testing.T) {
	web := setupTestWeb(t)
	assert.False(t, web.arena.EventSettings.ApKeepTeams)

	recorder := web.postHttpResponse(
		"/setup/settings", "name=Tent&playoffType=single&numPlayoffAlliances=8&networkSecurityEnabled=on&"+
			"apKeepTeams=on",
	)
	assert.Equal(t, 303, recorder.Code)
	assert.True(t, web.arena.EventSettings.ApKeepTeams)
	recorder = web.getHttpResponse("/setup/settings")
	assert.Contains(t, recorder.Body.String(), "name=\"apKeepTeams\"  checked")
	recorder = web.getHttpResponse("/match_play")
	assert.Contains(t, recorder.Body.String(), "const accessPointKeepsTeams = true;")

	recorder = web.postHttpResponse(
		"/setup/settings", "name=Tent&playoffType=single&numPlayoffAlliances=8&networkSecurityEnabled=on",
	)
	assert.Equal(t, 303, recorder.Code)
	assert.False(t, web.arena.EventSettings.ApKeepTeams)
	recorder = web.getHttpResponse("/match_play")
	assert.Contains(t, recorder.Body.String(), "const accessPointKeepsTeams = false;")
}
