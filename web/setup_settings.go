// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web routes for configuring the event settings.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"io"
	"log"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const settingsLockedMessage = "Settings cannot be changed while a match is in progress or is uncommitted."

// settingsGame is one game definition as the settings page offers it for selection.
type settingsGame struct {
	Id          string
	Name        string
	Description string
	Selected    bool
	Selectable  bool
	Problem     string // Why the game cannot be selected, if it cannot.
	Params      []settingsGameParam
}

// settingsGameParam is one parameter of a game that the event can override.
type settingsGameParam struct {
	Id      string
	Name    string
	Type    string
	Default string
	Value   string // The event's override, or empty to use the default.
	Min     string
	Max     string
}

// Shows the event settings editing page.
func (web *Web) settingsGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	web.renderSettings(w, r, "")
}

// Saves the event settings.
func (web *Web) settingsPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	activeSettingsTab := settingsTabFromRequest(r)
	eventSettings := web.arena.EventSettings

	// Validate the game selection before anything is changed, so that a selection that cannot be played is refused
	// as a whole.
	gameId, gameParams, err := web.readGameSelection(r)
	if err != nil {
		web.renderSettingsWithStatus(w, r, err.Error(), activeSettingsTab, http.StatusOK)
		return
	}
	gameSelectionChanged := gameId != eventSettings.GameId || !maps.Equal(gameParams, eventSettings.GameParams)

	if !settingsSaveAllowed(web.arena.MatchState) {
		// The game selection is the one thing that can be saved with a match on the field, because the arena does
		// not take it into use until that match is over. Everything else would reconfigure the field right now.
		message := settingsLockedMessage
		if gameSelectionChanged {
			eventSettings.GameId, eventSettings.GameParams = gameId, gameParams
			if err = web.arena.Database.UpdateEventSettings(eventSettings); err != nil {
				handleWebErr(w, err)
				return
			}
			web.gameChangePending.Store(true)
			message = "The game selection was saved and will be used once the match on the field is over. No other " +
				"setting was changed: " + strings.ToLower(settingsLockedMessage[:1]) + settingsLockedMessage[1:]
		}
		web.renderSettingsWithStatus(w, r, message, activeSettingsTab, http.StatusOK)
		return
	}
	eventSettings.GameId, eventSettings.GameParams = gameId, gameParams

	previousEventName := eventSettings.Name
	eventSettings.Name = r.PostFormValue("name")
	if len(eventSettings.Name) < 1 && eventSettings.Name != previousEventName {
		eventSettings.Name = previousEventName
	}
	previousAdminPassword := eventSettings.AdminPassword

	// The device broker's ports. A port of 0 turns that listener off, so a form without the fields keeps them as they
	// are rather than turning the broker off. They are checked here, before the settings start to change.
	mqttPorts := [2]int{eventSettings.DeviceMqttPort, eventSettings.DeviceMqttWsPort}
	for i, field := range [2]string{"deviceMqttPort", "deviceMqttWsPort"} {
		if !r.PostForm.Has(field) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue(field)))
		if err != nil || value < 0 || value > 65535 {
			web.renderSettingsWithStatus(
				w, r, "Device MQTT ports must be numbers from 0 (off) to 65535.", activeSettingsTab, http.StatusOK,
			)
			return
		}
		mqttPorts[i] = value
	}
	if mqttPorts[0] != 0 && mqttPorts[0] == mqttPorts[1] {
		web.renderSettingsWithStatus(
			w, r, "The device MQTT port and the MQTT over WebSocket port must differ.", activeSettingsTab,
			http.StatusOK,
		)
		return
	}

	var playoffType model.PlayoffType
	numAlliances := 0
	playoffTypeValue := r.PostFormValue("playoffType")
	playoffTypeProvided := playoffTypeValue != ""
	if playoffTypeValue == "" && eventSettings.PlayoffType == model.SingleEliminationPlayoff {
		playoffTypeValue = "SingleEliminationPlayoff"
	}
	if playoffTypeValue == "SingleEliminationPlayoff" || playoffTypeValue == "single" {
		playoffType = model.SingleEliminationPlayoff
		if r.PostFormValue("numPlayoffAlliances") == "" {
			if playoffTypeProvided {
				numAlliances = 0
			} else {
				numAlliances = eventSettings.NumPlayoffAlliances
			}
		} else {
			numAlliances, _ = strconv.Atoi(r.PostFormValue("numPlayoffAlliances"))
		}
		if numAlliances < 2 || numAlliances > 16 {
			web.renderSettingsWithStatus(w, r, "Number of alliances must be between 2 and 16.", activeSettingsTab, http.StatusOK)
			return
		}
	} else {
		playoffType = model.DoubleEliminationPlayoff
		if r.PostFormValue("numPlayoffAlliances") == "" {
			if eventSettings.PlayoffType == model.DoubleEliminationPlayoff {
				numAlliances = eventSettings.NumPlayoffAlliances
			} else {
				numAlliances = 8
			}
		} else {
			numAlliances, _ = strconv.Atoi(r.PostFormValue("numPlayoffAlliances"))
		}
		if numAlliances != 4 && numAlliances != 8 {
			web.renderSettingsWithStatus(
				w, r, "Number of alliances for double elimination must be 4 or 8.", activeSettingsTab, http.StatusOK,
			)
			return
		}
	}
	if eventSettings.PlayoffType != playoffType || eventSettings.NumPlayoffAlliances != numAlliances {
		alliances, err := web.arena.Database.GetAllAlliances()
		if err != nil {
			handleWebErr(w, err)
			return
		}
		if len(alliances) > 0 {
			web.renderSettingsWithStatus(
				w, r, "Cannot change playoff type or size after alliance selection has been finalized.", activeSettingsTab,
				http.StatusOK,
			)
			return
		}
	}
	eventSettings.PlayoffType = playoffType

	eventSettings.NumPlayoffAlliances = numAlliances
	eventSettings.SelectionRound2Order = r.PostFormValue("selectionRound2Order")
	eventSettings.SelectionRound3Order = r.PostFormValue("selectionRound3Order")
	eventSettings.SelectionShowUnpickedTeams = r.PostFormValue("selectionShowUnpickedTeams") == "on"
	eventSettings.TbaDownloadEnabled = r.PostFormValue("tbaDownloadEnabled") == "on"
	eventSettings.TbaPublishingEnabled = r.PostFormValue("tbaPublishingEnabled") == "on"
	eventSettings.TbaEventCode = r.PostFormValue("tbaEventCode")
	eventSettings.TbaSecretId = r.PostFormValue("tbaSecretId")
	eventSettings.TbaSecret = r.PostFormValue("tbaSecret")
	eventSettings.AutoAudienceDisplayEnabled = r.PostFormValue("autoAudienceDisplayEnabled") == "on"
	eventSettings.NexusEnabled = r.PostFormValue("nexusEnabled") == "on"
	eventSettings.NexusAutoQueueEnabled = r.PostFormValue("nexusAutoQueueEnabled") == "on"
	eventSettings.NexusAutoQueueKey = r.PostFormValue("nexusAutoQueueKey")
	eventSettings.NetworkSecurityEnabled = r.PostFormValue("networkSecurityEnabled") == "on"
	eventSettings.RadioOnlyNetwork = r.PostFormValue("radioOnlyNetwork") == "on"
	eventSettings.ApAddress = r.PostFormValue("apAddress")
	eventSettings.ApPassword = r.PostFormValue("apPassword")
	eventSettings.ApChannel, _ = strconv.Atoi(r.PostFormValue("apChannel"))
	eventSettings.SwitchAddress = r.PostFormValue("switchAddress")
	eventSettings.SwitchPassword = r.PostFormValue("switchPassword")
	eventSettings.SCCManagementEnabled = r.PostFormValue("sccManagementEnabled") == "on"
	eventSettings.RedSCCAddress = r.PostFormValue("redSCCAddress")
	eventSettings.BlueSCCAddress = r.PostFormValue("blueSCCAddress")
	eventSettings.SCCUsername = r.PostFormValue("sccUsername")
	eventSettings.SCCPassword = r.PostFormValue("sccPassword")
	eventSettings.SCCUpCommands = r.PostFormValue("sccUpCommands")
	eventSettings.SCCDownCommands = r.PostFormValue("sccDownCommands")
	eventSettings.PlcAddress = r.PostFormValue("plcAddress")
	eventSettings.DeviceMqttPort, eventSettings.DeviceMqttWsPort = mqttPorts[0], mqttPorts[1]
	eventSettings.AdminPassword = r.PostFormValue("adminPassword")
	eventSettings.TeamSignRed1Id, _ = strconv.Atoi(r.PostFormValue("teamSignRed1Id"))
	eventSettings.TeamSignRed2Id, _ = strconv.Atoi(r.PostFormValue("teamSignRed2Id"))
	eventSettings.TeamSignRed3Id, _ = strconv.Atoi(r.PostFormValue("teamSignRed3Id"))
	eventSettings.TeamSignRedTimerId, _ = strconv.Atoi(r.PostFormValue("teamSignRedTimerId"))
	eventSettings.TeamSignBlue1Id, _ = strconv.Atoi(r.PostFormValue("teamSignBlue1Id"))
	eventSettings.TeamSignBlue2Id, _ = strconv.Atoi(r.PostFormValue("teamSignBlue2Id"))
	eventSettings.TeamSignBlue3Id, _ = strconv.Atoi(r.PostFormValue("teamSignBlue3Id"))
	eventSettings.TeamSignBlueTimerId, _ = strconv.Atoi(r.PostFormValue("teamSignBlueTimerId"))
	eventSettings.UseLiteUdpPort = r.PostFormValue("useLiteUdpPort") == "on"
	eventSettings.BlackmagicAddresses = r.PostFormValue("blackmagicAddresses")
	eventSettings.CompanionAddress = r.PostFormValue("companionAddress")
	eventSettings.CompanionPort, _ = strconv.Atoi(r.PostFormValue("companionPort"))
	eventSettings.CompanionMatchPreviewPage, _ = strconv.Atoi(r.PostFormValue("companionMatchPreviewPage"))
	eventSettings.CompanionMatchPreviewRow, _ = strconv.Atoi(r.PostFormValue("companionMatchPreviewRow"))
	eventSettings.CompanionMatchPreviewColumn, _ = strconv.Atoi(r.PostFormValue("companionMatchPreviewColumn"))
	eventSettings.CompanionSetAudiencePage, _ = strconv.Atoi(r.PostFormValue("companionSetAudiencePage"))
	eventSettings.CompanionSetAudienceRow, _ = strconv.Atoi(r.PostFormValue("companionSetAudienceRow"))
	eventSettings.CompanionSetAudienceColumn, _ = strconv.Atoi(r.PostFormValue("companionSetAudienceColumn"))
	eventSettings.CompanionMatchStartPage, _ = strconv.Atoi(r.PostFormValue("companionMatchStartPage"))
	eventSettings.CompanionMatchStartRow, _ = strconv.Atoi(r.PostFormValue("companionMatchStartRow"))
	eventSettings.CompanionMatchStartColumn, _ = strconv.Atoi(r.PostFormValue("companionMatchStartColumn"))
	eventSettings.CompanionTeleopStartPage, _ = strconv.Atoi(r.PostFormValue("companionTeleopStartPage"))
	eventSettings.CompanionTeleopStartRow, _ = strconv.Atoi(r.PostFormValue("companionTeleopStartRow"))
	eventSettings.CompanionTeleopStartColumn, _ = strconv.Atoi(r.PostFormValue("companionTeleopStartColumn"))
	eventSettings.CompanionEndgameStartPage, _ = strconv.Atoi(r.PostFormValue("companionEndgameStartPage"))
	eventSettings.CompanionEndgameStartRow, _ = strconv.Atoi(r.PostFormValue("companionEndgameStartRow"))
	eventSettings.CompanionEndgameStartColumn, _ = strconv.Atoi(r.PostFormValue("companionEndgameStartColumn"))
	eventSettings.CompanionMatchEndPage, _ = strconv.Atoi(r.PostFormValue("companionMatchEndPage"))
	eventSettings.CompanionMatchEndRow, _ = strconv.Atoi(r.PostFormValue("companionMatchEndRow"))
	eventSettings.CompanionMatchEndColumn, _ = strconv.Atoi(r.PostFormValue("companionMatchEndColumn"))
	eventSettings.CompanionPostResultPage, _ = strconv.Atoi(r.PostFormValue("companionPostResultPage"))
	eventSettings.CompanionPostResultRow, _ = strconv.Atoi(r.PostFormValue("companionPostResultRow"))
	eventSettings.CompanionPostResultColumn, _ = strconv.Atoi(r.PostFormValue("companionPostResultColumn"))
	eventSettings.CompanionAllianceSelectionPage, _ = strconv.Atoi(r.PostFormValue("companionAllianceSelectionPage"))
	eventSettings.CompanionAllianceSelectionRow, _ = strconv.Atoi(r.PostFormValue("companionAllianceSelectionRow"))
	eventSettings.CompanionAllianceSelectionColumn, _ = strconv.Atoi(r.PostFormValue("companionAllianceSelectionColumn"))
	eventSettings.CompanionMatchAbortPage, _ = strconv.Atoi(r.PostFormValue("companionMatchAbortPage"))
	eventSettings.CompanionMatchAbortRow, _ = strconv.Atoi(r.PostFormValue("companionMatchAbortRow"))
	eventSettings.CompanionMatchAbortColumn, _ = strconv.Atoi(r.PostFormValue("companionMatchAbortColumn"))

	err = web.arena.Database.UpdateEventSettings(eventSettings)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	// Refresh the arena in case any of the settings changed. If the selected game turns out not to be usable, the
	// arena has applied every other setting and kept the game it had; that is reported, not treated as a failure.
	web.gameChangePending.Store(false)
	loadErr := web.arena.LoadSettings()

	if eventSettings.AdminPassword != previousAdminPassword {
		// Delete any existing user sessions to force a logout.
		if err := web.arena.Database.TruncateUserSessions(); err != nil {
			handleWebErr(w, err)
			return
		}
	}

	if loadErr != nil {
		web.renderSettingsWithStatus(w, r, "Settings saved, but: "+loadErr.Error(), activeSettingsTab, http.StatusOK)
		return
	}
	http.Redirect(w, r, "/setup/settings#"+activeSettingsTab, 303)
}

// readGameSelection reads the game the settings form selects and the form's overrides of that game's parameters,
// and checks that the game can be played on this field and that every override is a value its parameter accepts.
// A form that does not carry a game selection leaves the event's selection as it is.
func (web *Web) readGameSelection(r *http.Request) (string, map[string]float64, error) {
	eventSettings := web.arena.EventSettings
	gameId := r.PostFormValue("gameId")
	if gameId == "" {
		return eventSettings.GameId, eventSettings.GameParams, nil
	}

	g := web.arena.Games.Get(gameId)
	if g == nil {
		return "", nil, fmt.Errorf(
			"Game %q is missing or has errors; it has to load without errors before it can be selected.", gameId,
		)
	}
	if err := field.CheckGameSelectable(g); err != nil {
		return "", nil, fmt.Errorf("Game %q cannot be selected: %s.", gameId, err.Error())
	}

	// An empty input means "use the game's default", which is stored as the absence of an override.
	params := make(map[string]float64)
	for _, param := range g.Params {
		raw := strings.TrimSpace(r.PostFormValue(gameParamInputName(gameId, param.ID)))
		if raw == "" {
			continue
		}
		value, err := parseGameParam(param, raw)
		if err != nil {
			return "", nil, err
		}
		params[param.ID] = value
	}
	return gameId, params, nil
}

// gameParamInputName returns the name of the form input that holds the event's override of a game's parameter. The
// page carries the inputs of every game, so that switching the selection shows the right ones without a round trip.
func gameParamInputName(gameId, paramId string) string {
	return "gameParam." + gameId + "." + paramId
}

// parseGameParam turns what was typed for a game parameter into its value. Parameter values are stored as numbers;
// a boolean is 1 or 0.
func parseGameParam(param *gamedef.Param, raw string) (float64, error) {
	name := param.Name
	if name == "" {
		name = param.ID
	}
	if param.Type == "bool" {
		switch strings.ToLower(raw) {
		case "true", "1", "on", "yes":
			return 1, nil
		case "false", "0", "off", "no":
			return 0, nil
		}
		return 0, fmt.Errorf("%s must be on or off.", name)
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s must be a number; %q is not one.", name, raw)
	}
	if param.Type == "int" && value != math.Trunc(value) {
		return 0, fmt.Errorf("%s must be a whole number.", name)
	}
	if param.Min != nil && value < *param.Min {
		return 0, fmt.Errorf("%s cannot be less than %s.", name, formatNumber(*param.Min))
	}
	if param.Max != nil && value > *param.Max {
		return 0, fmt.Errorf("%s cannot be more than %s.", name, formatNumber(*param.Max))
	}
	return value, nil
}

// buildSettingsGames lists every game definition for the settings page: which one is selected, which ones cannot be
// and why, and each game's parameters with the event's overrides of the selected one.
func (web *Web) buildSettingsGames() []settingsGame {
	eventSettings := web.arena.EventSettings
	var games []settingsGame
	for _, summary := range web.arena.Games.List() {
		listed := settingsGame{
			Id: summary.ID, Name: summary.Name, Description: summary.Description,
			Selected: summary.ID == eventSettings.GameId,
		}
		g := web.arena.Games.Get(summary.ID)
		switch {
		case g == nil:
			listed.Problem = "it has errors"
			for _, diagnostic := range summary.Diagnostics {
				if diagnostic.Severity == "error" {
					listed.Problem = "it has errors, the first being " + diagnostic.String()
					break
				}
			}
		case field.CheckGameSelectable(g) != nil:
			listed.Problem = field.CheckGameSelectable(g).Error()
		default:
			listed.Selectable = true
		}
		if g != nil {
			for _, param := range g.Params {
				listedParam := settingsGameParam{Id: param.ID, Name: param.Name, Type: param.Type}
				if listedParam.Name == "" {
					listedParam.Name = param.ID
				}
				defaultValue := g.ParamDefaults()[param.ID]
				listedParam.Default = formatGameParam(param, defaultValue)
				if override, ok := eventSettings.GameParams[param.ID]; ok && listed.Selected {
					listedParam.Value = formatGameParam(param, override)
				}
				if param.Min != nil {
					listedParam.Min = formatNumber(*param.Min)
				}
				if param.Max != nil {
					listedParam.Max = formatNumber(*param.Max)
				}
				listed.Params = append(listed.Params, listedParam)
			}
		}
		games = append(games, listed)
	}
	return games
}

// formatGameParam formats the value of a game parameter the way its input on the settings page takes it.
func formatGameParam(param *gamedef.Param, value float64) string {
	if param.Type == "bool" {
		if value != 0 {
			return "true"
		}
		return "false"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func settingsSaveAllowed(matchState field.MatchState) bool {
	return matchState == field.PreMatch || matchState == field.TimeoutActive || matchState == field.PostTimeout
}

func settingsTabFromRequest(r *http.Request) string {
	switch r.PostFormValue("activeSettingsTab") {
	case "event", "game", "field", "publishing", "automation":
		return r.PostFormValue("activeSettingsTab")
	default:
		return "event"
	}
}

// Sends a copy of the event database file to the client as a download.
func (web *Web) saveDbHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	filename := fmt.Sprintf(
		"%s-%s.db", strings.Replace(web.arena.EventSettings.Name, " ", "_", -1), time.Now().Format("20060102150405"),
	)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	if err := web.arena.Database.WriteBackup(w); err != nil {
		handleWebErr(w, err)
		return
	}
}

// Accepts an event database file as an upload and loads it.
func (web *Web) restoreDbHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	file, _, err := r.FormFile("databaseFile")
	if err != nil {
		web.renderSettings(w, r, "No database backup file was specified.")
		return
	}
	defer file.Close()

	// Write the file to a temporary location on disk and verify that it can be opened as a database.
	tempFile, err := os.CreateTemp(filepath.Dir(web.arena.Database.Path), "uploaded-db-")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	tempFilePath := tempFile.Name()
	defer func() {
		if tempFilePath == "" {
			return
		}
		if err := os.Remove(tempFilePath); err != nil {
			log.Printf("Failed to remove temporary uploaded database file %s: %v", tempFilePath, err)
		}
	}()
	_, err = io.Copy(tempFile, file)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	if err = tempFile.Close(); err != nil {
		handleWebErr(w, err)
		return
	}
	tempDb, err := model.OpenDatabase(tempFilePath)
	if err != nil {
		web.renderSettings(
			w, r, "Could not read uploaded database backup file. Please verify that it a valid database file.",
		)
		return
	}
	if err = tempDb.Close(); err != nil {
		handleWebErr(w, err)
		return
	}

	// Back up the current database.
	err = web.arena.Database.Backup(web.arena.EventSettings.Name, "pre_restore")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	// Replace the current database with the new one.
	if err = web.arena.Database.Close(); err != nil {
		handleWebErr(w, err)
		return
	}
	err = os.Remove(web.arena.Database.Path)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	err = os.Rename(tempFilePath, web.arena.Database.Path)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	tempFilePath = ""
	web.arena.Database, err = model.OpenDatabase(web.arena.Database.Path)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	// A restored database may select a game that cannot be used here. The arena then keeps the game it has and has
	// applied every other setting, so that is reported on the page rather than treated as a failed restore.
	web.gameChangePending.Store(false)
	err = web.arena.LoadSettings()
	if err != nil {
		web.renderSettings(w, r, "The database was restored, but: "+err.Error())
		return
	}

	http.Redirect(w, r, "/setup/settings", 303)
}

// Deletes all match data including and beyond the given tournament stage.
func (web *Web) clearDbHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	matchType, err := model.MatchTypeFromString(r.PathValue("type"))
	if err != nil || matchType == model.Test {
		web.renderSettings(w, r, "Invalid tournament stage to clear.")
		return

	}

	// Back up the database.
	err = web.arena.Database.Backup(web.arena.EventSettings.Name, "pre_clear")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	switch matchType {
	case model.Practice:
		if err = web.deleteMatchDataForType(model.Practice); err != nil {
			handleWebErr(w, err)
			return
		}
	case model.Qualification:
		if err = web.deleteMatchDataForType(model.Qualification); err != nil {
			handleWebErr(w, err)
			return
		}
		if err = web.arena.Database.TruncateRankings(); err != nil {
			handleWebErr(w, err)
			return
		}
	case model.Playoff:
		if err = web.deleteMatchDataForType(model.Playoff); err != nil {
			handleWebErr(w, err)
			return
		}
		if err = web.arena.Database.TruncateAlliances(); err != nil {
			handleWebErr(w, err)
			return
		}
		web.arena.AllianceSelectionAlliances = []model.Alliance{}
		web.arena.AllianceSelectionRankedTeams = []model.AllianceSelectionRankedTeam{}
	}

	http.Redirect(w, r, "/setup/settings", 303)
}

// Publishes the playoff alliances to the web.
func (web *Web) settingsPublishAlliancesHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	if web.arena.EventSettings.TbaPublishingEnabled {
		err := web.arena.TbaClient.PublishAlliances(web.arena.Database)
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to publish alliances: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
	} else {
		web.renderSettingsWithStatus(w, r, "TBA publishing is not enabled", "publishing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/setup/settings#publishing", 303)
}

// Publishes the awards to the web.
func (web *Web) settingsPublishAwardsHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	if web.arena.EventSettings.TbaPublishingEnabled {
		err := web.arena.TbaClient.PublishAwards(web.arena.Database)
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to publish awards: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
	} else {
		web.renderSettingsWithStatus(w, r, "TBA publishing is not enabled", "publishing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/setup/settings#publishing", 303)
}

// Publishes the match schedule and results to the web.
func (web *Web) settingsPublishMatchesHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	if web.arena.EventSettings.TbaPublishingEnabled {
		err := web.arena.TbaClient.DeletePublishedMatches()
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to delete published matches: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
		err = web.arena.TbaClient.PublishMatches(web.arena.Database)
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to publish matches: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
	} else {
		web.renderSettingsWithStatus(w, r, "TBA publishing is not enabled", "publishing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/setup/settings#publishing", 303)
}

// Publishes the standings to the web.
func (web *Web) settingsPublishRankingsHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	if web.arena.EventSettings.TbaPublishingEnabled {
		err := web.arena.TbaClient.PublishRankings(web.arena.Database, web.arena.Game)
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to publish rankings: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
	} else {
		web.renderSettingsWithStatus(w, r, "TBA publishing is not enabled", "publishing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/setup/settings#publishing", 303)
}

// Publishes the team list to the web.
func (web *Web) settingsPublishTeamsHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	if web.arena.EventSettings.TbaPublishingEnabled {
		err := web.arena.TbaClient.PublishTeams(web.arena.Database)
		if err != nil {
			web.renderSettingsWithStatus(
				w, r, "Failed to publish teams: "+err.Error(), "publishing", http.StatusInternalServerError,
			)
			return
		}
	} else {
		web.renderSettingsWithStatus(w, r, "TBA publishing is not enabled", "publishing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/setup/settings#publishing", 303)
}

func (web *Web) renderSettings(w http.ResponseWriter, r *http.Request, errorMessage string) {
	web.renderSettingsWithStatus(w, r, errorMessage, "event", http.StatusOK)
}

func (web *Web) renderSettingsWithStatus(
	w http.ResponseWriter, r *http.Request, errorMessage string, activeSettingsTab string, statusCode int,
) {
	template, err := web.parseFiles("templates/setup_settings.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		ErrorMessage      string
		ActiveSettingsTab string
		NexusBaseUrl      string
		Games             []settingsGame
		ActiveGameId      string // The game the arena is playing, which lags the selection while a match is on.
		ActiveGameName    string
		MatchInProgress   bool
	}{
		web.arena.EventSettings, errorMessage, activeSettingsTab, web.arena.NexusClient.BaseUrl,
		web.buildSettingsGames(), web.arena.Game.ID, web.arena.Game.Name, !settingsSaveAllowed(web.arena.MatchState),
	}
	if statusCode != http.StatusOK {
		w.WriteHeader(statusCode)
	}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Deletes all match data (matches, results, and scheduled breaks) for the given match type.
func (web *Web) deleteMatchDataForType(matchType model.MatchType) error {
	matches, err := web.arena.Database.GetMatchesByType(matchType, true)
	if err != nil {
		return err
	}
	for _, match := range matches {
		// Loop to delete all match results for the match before deleting the match itself.
		matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
		if err != nil {
			return err
		}
		for matchResult != nil {
			if err = web.arena.Database.DeleteMatchResult(matchResult.Id); err != nil {
				return err
			}
			matchResult, err = web.arena.Database.GetMatchResultForMatch(match.Id)
			if err != nil {
				return err
			}
		}

		if err = web.arena.Database.DeleteMatch(match.Id); err != nil {
			return err
		}
	}
	if err = web.arena.Database.DeleteScheduledBreaksByMatchType(matchType); err != nil {
		return err
	}
	return nil
}
