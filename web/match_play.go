// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web routes for controlling match play.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/tournament"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"github.com/mitchellh/mapstructure"
	"io"
	"log"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"
)

// How often Match Play compares the device faults with what it last showed, besides on every fault event: deleting
// a faulted device drops its faults without one.
const matchPlayDeviceFaultsPeriod = 2 * time.Second

// matchFlowMutex serialises committing a match and loading the next one: the Commit & Post button, the Discard
// Results button and the field devices bound to commit and next_match may all ask at the same moment.
var matchFlowMutex sync.Mutex

type MatchPlayListItem struct {
	Id         int
	ShortName  string
	Time       string
	Status     model.MatchStatus
	ColorClass string
}

const defaultTimeoutDescription = "Field Break"

type MatchPlayList []MatchPlayListItem

// Shows the match play control interface.
func (web *Web) matchPlayHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	template, err := web.parseFiles(
		"templates/match_play.html", "templates/audience_display_radio_buttons.html", "templates/base.html",
	)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		PlcIsEnabled          bool
		PlcArmorBlockStatuses map[string]bool
	}{
		web.arena.EventSettings,
		web.arena.Plc.IsEnabled(),
		web.arena.Plc.GetArmorBlockStatuses(),
	}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Renders a partial template containing the list of matches.
func (web *Web) matchPlayMatchLoadHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	practiceMatches, err := web.buildMatchPlayList(model.Practice)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	qualificationMatches, err := web.buildMatchPlayList(model.Qualification)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	playoffMatches, err := web.buildMatchPlayList(model.Playoff)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	matchesByType := map[model.MatchType]MatchPlayList{
		model.Practice:      practiceMatches,
		model.Qualification: qualificationMatches,
		model.Playoff:       playoffMatches,
	}
	currentMatchType := web.arena.CurrentMatch.Type
	if currentMatchType == model.Test {
		currentMatchType = model.Practice
	}

	template, err := web.parseFiles("templates/match_play_match_load.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		MatchesByType    map[model.MatchType]MatchPlayList
		CurrentMatchType model.MatchType
	}{
		matchesByType,
		currentMatchType,
	}
	err = template.ExecuteTemplate(w, "match_play_match_load.html", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// The websocket endpoint for the match play client to send control commands and receive status updates.
func (web *Web) matchPlayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)

	// Subscribe the websocket to the notifiers whose messages will be passed on to the client, in a separate goroutine.
	go ws.HandleNotifiers(
		web.arena.GameNotifier,
		web.arena.AllianceStationDisplayModeNotifier,
		web.arena.ArenaStatusNotifier,
		web.arena.AudienceDisplayModeNotifier,
		web.arena.EventStatusNotifier,
		web.arena.MatchLoadNotifier,
		web.arena.MatchTimeNotifier,
		web.arena.RealtimeScoreNotifier,
		web.arena.ScorePostedNotifier,
		web.arena.ScoringStatusNotifier,
	)
	deviceEvents := web.arena.Bus.Subscribe([]string{"device.fault", "device.fault_cleared"}, 64)
	defer deviceEvents.Close()
	go web.sendDeviceFaults(ws, deviceEvents)

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, data, err := ws.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Println(err)
			return
		}

		switch messageType {
		case "loadMatch":
			args := struct {
				MatchId int
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			err = web.arena.ResetMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			web.applyPendingGameChange()
			if args.MatchId == 0 {
				err = web.arena.LoadTestMatch()
			} else {
				match, err := web.arena.Database.GetMatchById(args.MatchId)
				if err != nil {
					writeWebsocketError(ws, err.Error())
					continue
				}
				if match == nil {
					writeWebsocketError(ws, fmt.Sprintf("invalid match ID %d", args.MatchId))
					continue
				}
				err = web.arena.LoadMatch(match)
			}
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "showResult":
			args := struct {
				MatchId int
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			if args.MatchId == 0 {
				// Load an empty match to effectively clear the buffer. Like any result, an empty one needs the game it
				// belongs to stored in order to be computed.
				if err = web.arena.Database.SaveGameSnapshot(web.arena.Game); err != nil {
					writeWebsocketError(ws, err.Error())
					continue
				}
				web.arena.SavedMatch = &model.Match{}
				web.arena.SavedMatchResult = model.NewMatchResult(web.arena.Game)
				web.arena.SavedRankings = []model.Ranking{}
				web.arena.ScorePostedNotifier.Notify()
				continue
			}
			match, err := web.arena.Database.GetMatchById(args.MatchId)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			if match == nil {
				writeWebsocketError(ws, fmt.Sprintf("invalid match ID %d", args.MatchId))
				continue
			}
			matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			if matchResult == nil {
				writeWebsocketError(ws, fmt.Sprintf("No result found for match ID %d.", args.MatchId))
				continue
			}
			if match.ShouldUpdateRankings() {
				web.arena.SavedRankings, err = web.arena.Database.GetAllRankings()
				if err != nil {
					writeWebsocketError(ws, err.Error())
					continue
				}
			} else {
				web.arena.SavedRankings = []model.Ranking{}
			}
			web.arena.SavedMatch = match
			web.arena.SavedMatchResult = matchResult
			web.arena.ScorePostedNotifier.Notify()
		case "substituteTeams":
			args := struct {
				Red1  int
				Red2  int
				Red3  int
				Blue1 int
				Blue2 int
				Blue3 int
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			err = web.arena.SubstituteTeams(args.Red1, args.Red2, args.Red3, args.Blue1, args.Blue2, args.Blue3)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "toggleBypass":
			station, ok := data.(string)
			if !ok {
				writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			err = web.arena.ToggleBypass(station)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "startMatch":
			args := struct {
				MuteMatchSounds bool
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			web.arena.MuteMatchSounds = args.MuteMatchSounds
			err = web.arena.StartMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "abortMatch":
			err = web.arena.AbortMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "signalVolunteers":
			web.arena.SignalVolunteers()
		case "signalReset":
			web.arena.SignalReset()
		case "commitAndPost":
			err = web.commitPostAndLoadNextMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "discardResults":
			err = web.resetAndLoadNextMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "clearDeviceFaults":
			err = web.arena.Devices.ClearAllFaults()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "setAudienceDisplay":
			mode, ok := data.(string)
			if !ok {
				writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			web.arena.SetAudienceDisplayMode(mode)
		case "setAllianceStationDisplay":
			mode, ok := data.(string)
			if !ok {
				writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			web.arena.SetAllianceStationDisplayMode(mode)
		case "startTimeout":
			var timeoutSettings struct {
				Description   string
				NextMatchName string
				DurationSec   float64
			}
			durationSec, ok := data.(float64)
			if ok {
				timeoutSettings.Description = defaultTimeoutDescription
				timeoutSettings.DurationSec = durationSec
			} else {
				err = mapstructure.Decode(data, &timeoutSettings)
				if err != nil || timeoutSettings.DurationSec == 0 {
					writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
					continue
				}
				if timeoutSettings.Description == "" {
					timeoutSettings.Description = defaultTimeoutDescription
				}
			}
			err = web.arena.StartAdHocTimeout(
				timeoutSettings.Description, timeoutSettings.NextMatchName, int(timeoutSettings.DurationSec),
			)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "setTimeoutDisplay":
			var timeoutSettings struct {
				Description   string
				NextMatchName string
			}
			err = mapstructure.Decode(data, &timeoutSettings)
			if err != nil {
				ws.WriteError(fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			if timeoutSettings.Description == "" {
				timeoutSettings.Description = defaultTimeoutDescription
			}
			web.arena.SetTimeoutDisplay(timeoutSettings.Description, timeoutSettings.NextMatchName)
		case "setTestMatchName":
			if web.arena.CurrentMatch.Type != model.Test {
				// Don't allow changing the name of a non-test match.
				continue
			}
			name, ok := data.(string)
			if !ok {
				writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			web.arena.CurrentMatch.LongName = name
			web.arena.MatchLoadNotifier.Notify()
		default:
			writeWebsocketError(ws, fmt.Sprintf("Invalid message type '%s'.", messageType))
		}
	}
}

func (web *Web) commitPostAndLoadNextMatch() error {
	matchFlowMutex.Lock()
	defer matchFlowMutex.Unlock()
	if web.arena.MatchState != field.PostMatch {
		return fmt.Errorf("cannot commit match while it is in progress")
	}

	err := web.commitCurrentMatchScore()
	if err != nil {
		return err
	}

	web.arena.SetAudienceDisplayMode("score")
	if web.arena.EventSettings.AutoAudienceDisplayEnabled {
		go web.arena.AutomateAudienceDisplay(web.arena.CurrentMatch)
	}

	err = web.arena.ResetMatch()
	if err != nil {
		return err
	}
	web.applyPendingGameChange()

	return web.arena.LoadNextMatch(true)
}

// resetAndLoadNextMatch clears the field and loads the first unplayed match, dropping the results of the match on the
// field if there are any: what the Discard Results button does.
func (web *Web) resetAndLoadNextMatch() error {
	matchFlowMutex.Lock()
	defer matchFlowMutex.Unlock()
	if err := web.arena.ResetMatch(); err != nil {
		return err
	}
	web.applyPendingGameChange()
	return web.arena.LoadNextMatch(false)
}

// loadNextMatchForDevice carries out the next_match field action: it loads the next match as Discard Results does,
// but never discards anything, so it is refused while the match on the field waits to be committed.
func (web *Web) loadNextMatchForDevice() error {
	if web.arena.MatchState == field.PostMatch {
		return fmt.Errorf("the match on the field has not been committed; commit it, or discard it on Match Play")
	}
	return web.resetAndLoadNextMatch()
}

// RegisterFieldActionHandlers lets field devices commit the match and load the next one: the commit field action
// does what the Commit & Post button does, and next_match what loading the next match does. The arena runs them on a
// worker of its own, never on its loop.
func (web *Web) RegisterFieldActionHandlers() error {
	if err := web.arena.SetFieldActionHandler(device.ActionCommit, web.commitPostAndLoadNextMatch); err != nil {
		return err
	}
	return web.arena.SetFieldActionHandler(device.ActionNextMatch, web.loadNextMatchForDevice)
}

// sendDeviceFaults keeps Match Play's list of device faults up to date: it is sent whenever it changes, and on
// connecting only if there are faults, since the page starts with none.
func (web *Web) sendDeviceFaults(ws *websocket.Websocket, events *bus.Subscription) {
	ticker := time.NewTicker(matchPlayDeviceFaultsPeriod)
	defer ticker.Stop()
	shown := []device.Fault{}
	send := func() bool {
		faults := web.arena.Devices.Faults()
		if reflect.DeepEqual(faults, shown) {
			return true
		}
		shown = faults
		return ws.Write("deviceFaults", web.deviceFaultsMessage(faults)) == nil
	}
	if !send() {
		return
	}
	for {
		select {
		case _, ok := <-events.C:
			if !ok || !send() {
				return
			}
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

// matchPlayDeviceFault is one device fault as Match Play shows it.
type matchPlayDeviceFault struct {
	device.Fault
	DeviceName string `json:"deviceName"`
}

func (web *Web) deviceFaultsMessage(faults []device.Fault) []matchPlayDeviceFault {
	message := make([]matchPlayDeviceFault, len(faults))
	for i, fault := range faults {
		message[i].Fault = fault
		message[i].DeviceName = fault.Device
		if info, ok := web.arena.Devices.Device(fault.Device); ok {
			message[i].DeviceName = info.Name
		}
	}
	return message
}

// applyPendingGameChange takes into use a game selection that was saved while a match was on the field. It is
// called once that match has been reset: reloading the settings also reconnects the field hardware, which must not
// happen in the middle of a match.
func (web *Web) applyPendingGameChange() {
	if !web.gameChangePending.CompareAndSwap(true, false) {
		return
	}
	if err := web.arena.LoadSettings(); err != nil {
		log.Printf("Failed to apply the game selection that was waiting for the match to end: %v", err)
	}
}

// Saves the given match and result to the database, supplanting any previous result for the match. The winner is
// worked out from the result with the game it was played with, and the rankings, the carried cards and the playoff
// bracket are brought up to date; an edit made from match review goes through exactly the same steps.
func (web *Web) commitMatchScore(match *model.Match, matchResult *model.MatchResult, isMatchReviewEdit bool) error {
	var updatedRankings []model.Ranking

	// Whether a card disqualifies the whole alliance depends on the kind of match the result belongs to.
	matchResult.MatchType = match.Type
	result, resultGame, err := matchResult.Compute(web.arena.Database, match.UseTiebreakCriteria)
	if err != nil {
		return err
	}

	// Update the match record.
	match.ScoreCommittedAt = time.Now()
	match.Status = model.MatchStatusFromResult(resultGame, result)

	if match.Type != model.Test {
		if matchResult.PlayNumber == 0 {
			// Determine the play number for this new match result.
			prevMatchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
			if err != nil {
				return err
			}
			if prevMatchResult != nil {
				matchResult.PlayNumber = prevMatchResult.PlayNumber + 1
			} else {
				matchResult.PlayNumber = 1
			}

			// Save the match result record to the database.
			err = web.arena.Database.CreateMatchResult(matchResult)
			if err != nil {
				return err
			}
		} else {
			// We are updating a match result record that already exists.
			err := web.arena.Database.UpdateMatchResult(matchResult)
			if err != nil {
				return err
			}
		}

		err := web.arena.Database.UpdateMatch(match)
		if err != nil {
			return err
		}

		if match.ShouldUpdateCards() {
			if match.Type == model.Playoff {
				err = tournament.CalculateAllianceCards(web.arena.Database)
			} else {
				err = tournament.CalculateTeamCards(web.arena.Database)
			}
			if err != nil {
				return err
			}
		}

		if match.ShouldUpdateRankings() {
			// Recalculate all the rankings.
			rankings, err := tournament.CalculateRankings(web.arena.Database, web.arena.Game, isMatchReviewEdit)
			if err != nil {
				return err
			}
			updatedRankings = rankings
		}

		if match.ShouldUpdatePlayoffMatches() {
			if err = web.arena.Database.UpdateAllianceFromMatch(
				match.PlayoffRedAlliance, [3]int{match.Red1, match.Red2, match.Red3},
			); err != nil {
				return err
			}
			if err = web.arena.Database.UpdateAllianceFromMatch(
				match.PlayoffBlueAlliance, [3]int{match.Blue1, match.Blue2, match.Blue3},
			); err != nil {
				return err
			}

			// Populate any subsequent playoff matches.
			if err = web.arena.UpdatePlayoffTournament(); err != nil {
				return err
			}

			// Generate awards if the tournament is over.
			if web.arena.PlayoffTournament.IsComplete() {
				winnerAllianceId := web.arena.PlayoffTournament.WinningAllianceId()
				finalistAllianceId := web.arena.PlayoffTournament.FinalistAllianceId()
				if err = tournament.CreateOrUpdateWinnerAndFinalistAwards(
					web.arena.Database, winnerAllianceId, finalistAllianceId,
				); err != nil {
					return err
				}
			}
		}

		if web.arena.EventSettings.TbaPublishingEnabled {
			// Publish asynchronously to The Blue Alliance.
			go func() {
				if err := web.arena.TbaClient.PublishMatches(web.arena.Database); err != nil {
					log.Printf("Failed to publish matches: %s", err.Error())
				}
				if match.ShouldUpdateRankings() {
					if err := web.arena.TbaClient.PublishRankings(web.arena.Database, web.arena.Game); err != nil {
						log.Printf("Failed to publish rankings: %s", err.Error())
					}
				}
			}()
		}

		if web.arena.EventSettings.NexusAutoQueueEnabled && !isMatchReviewEdit {
			// Trigger Nexus AutoQueue asynchronously, ignoring errors.
			go func() {
				web.arena.NexusClient.AutoQueue(match.LongName, match.TypeOrder, match.Status)
			}()
		}

		// Back up the database, but don't error out if it fails.
		err = web.arena.Database.Backup(
			web.arena.EventSettings.Name, fmt.Sprintf("post_%s_match_%s", match.Type, match.ShortName),
		)
		if err != nil {
			log.Println(err)
		}
	}

	if isMatchReviewEdit && match.Type == model.Playoff && web.arena.CurrentMatch.Type == model.Playoff {
		web.arena.MatchLoadNotifier.Notify()
		web.arena.RealtimeScoreNotifier.Notify()
	}

	if !isMatchReviewEdit {
		// Store the result in the buffer to be shown in the audience display.
		web.arena.SavedMatch = match
		web.arena.SavedMatchResult = matchResult
		web.arena.SavedRankings = updatedRankings
		web.arena.ScorePostedNotifier.Notify()
	}

	web.arena.PublishMatchCommitted(match, result, isMatchReviewEdit)
	return nil
}

// Saves the realtime result as the final score for the match currently loaded into the arena.
func (web *Web) commitCurrentMatchScore() error {
	matchResult, err := web.arena.BuildMatchResult()
	if err != nil {
		return err
	}
	return web.commitMatchScore(web.arena.CurrentMatch, matchResult, false)
}

// Helper function to implement the required interface for Sort.
func (list MatchPlayList) Len() int {
	return len(list)
}

// Helper function to implement the required interface for Sort.
func (list MatchPlayList) Less(i, j int) bool {
	return list[i].Status == model.MatchScheduled && list[j].Status != model.MatchScheduled
}

// Helper function to implement the required interface for Sort.
func (list MatchPlayList) Swap(i, j int) {
	list[i], list[j] = list[j], list[i]
}

// Constructs the list of matches to display on the side of the match play interface.
func (web *Web) buildMatchPlayList(matchType model.MatchType) (MatchPlayList, error) {
	matches, err := web.arena.Database.GetMatchesByType(matchType, false)
	if err != nil {
		return MatchPlayList{}, err
	}

	matchPlayList := make(MatchPlayList, len(matches))
	for i, match := range matches {
		matchPlayList[i].Id = match.Id
		matchPlayList[i].ShortName = match.ShortName
		matchPlayList[i].Time = match.Time.Local().Format("3:04 PM")
		matchPlayList[i].Status = match.Status
		switch match.Status {
		case model.RedWonMatch:
			matchPlayList[i].ColorClass = "red"
		case model.BlueWonMatch:
			matchPlayList[i].ColorClass = "blue"
		case model.TieMatch:
			matchPlayList[i].ColorClass = "yellow"
		default:
			matchPlayList[i].ColorClass = ""
		}
		if web.arena.CurrentMatch != nil && matchPlayList[i].Id == web.arena.CurrentMatch.Id {
			matchPlayList[i].ColorClass = "green"
		}
	}

	// Sort the list to put all completed matches at the bottom.
	sort.Stable(matchPlayList)

	return matchPlayList, nil
}
