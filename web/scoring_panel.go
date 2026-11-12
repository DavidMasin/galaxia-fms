// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web handlers for scoring interface.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"github.com/mitchellh/mapstructure"
	"io"
	"log"
	"net/http"
)

// How many of the latest score changes a panel is told about when it connects, so that its undo list survives a
// reload of the page.
const scoreChangeHistoryLength = 20

// scoreCommand is the data of the "score" command that scoring and referee panels send.
type scoreCommand struct {
	Alliance string
	Robot    int
	Element  string
	Op       string
	Value    any
	Bucket   string
}

// change turns the command into a change made by the given source.
func (command *scoreCommand) change(source scoring.Source) scoring.Change {
	return scoring.Change{
		Alliance: command.Alliance, Robot: command.Robot, Element: command.Element, Op: command.Op,
		Value: command.Value, Bucket: command.Bucket, Source: source,
	}
}

// undoCommand is the data of the "undo" command that scoring and referee panels send.
type undoCommand struct {
	Seq int
}

// scoringPanel returns the scoring panel position of the active game with the given id, or nil if it has none.
func (web *Web) scoringPanel(id string) *gamedef.Panel {
	for _, panel := range web.arena.ScoringPanels() {
		if panel.ID == id {
			return panel
		}
	}
	return nil
}

// Renders the scoring interface which enables input of scores in real-time.
func (web *Web) scoringPanelHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	position := r.PathValue("position")
	panel := web.scoringPanel(position)
	if panel == nil {
		handleWebErr(w, fmt.Errorf("Invalid position '%s'.", position))
		return
	}

	template, err := web.parseFiles("templates/scoring_panel.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		PositionName string
		PanelName    string
	}{web.arena.EventSettings, position, panel.Name}
	err = template.ExecuteTemplate(w, "base_no_navbar", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Serves the web app manifest of a scoring panel, so that a tablet can pin each position to its home screen.
func (web *Web) scoringPanelManifestHandler(w http.ResponseWriter, r *http.Request) {
	position := r.PathValue("position")
	panel := web.scoringPanel(position)
	if panel == nil {
		http.NotFound(w, r)
		return
	}

	path := "/panels/scoring/" + position
	manifest := map[string]any{
		"name": panel.Name + " Scoring",
		"icons": []map[string]string{
			{"src": "/static/img/panel-icon.png", "type": "image/png", "sizes": "512x512"},
		},
		"scope":     path,
		"start_url": path,
		"display":   "standalone",
	}
	writeJson(w, http.StatusOK, manifest)
}

// The websocket endpoint for the scoring interface client to send control commands and receive status updates.
func (web *Web) scoringPanelWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	position := r.PathValue("position")
	panel := web.scoringPanel(position)
	if panel == nil {
		handleWebErr(w, fmt.Errorf("Invalid position '%s'.", position))
		return
	}
	source := scoring.Source{Kind: scoring.SourceScorer, ID: position, Name: panel.Name}

	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)
	web.arena.ScoringPanelRegistry.RegisterPanel(position, ws)
	web.arena.ScoringStatusNotifier.Notify()
	defer web.arena.ScoringStatusNotifier.Notify()
	defer web.arena.ScoringPanelRegistry.UnregisterPanel(position, ws)

	// Instruct panel to clear any local state in case this is a reconnect, and then tell it what has been scored so
	// far so that it can offer to undo it.
	writeWebsocketMessage(ws, "resetLocalState", nil)
	web.writeScoreChangeHistory(ws)

	// Subscribe the websocket to the notifiers whose messages will be passed on to the client, in a separate goroutine.
	go ws.HandleNotifiers(
		web.arena.GameNotifier,
		web.arena.MatchLoadNotifier,
		web.arena.MatchTimeNotifier,
		web.arena.RealtimeScoreNotifier,
		web.arena.ScoreChangeNotifier,
		web.arena.ReloadDisplaysNotifier,
	)

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		command, data, err := ws.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Println(err)
			return
		}

		switch command {
		case "commitMatch":
			if web.arena.MatchState != field.PostMatch {
				// Don't allow committing the score until the match is over.
				writeWebsocketError(ws, "Cannot commit score: Match is not over.")
				continue
			}
			web.arena.ScoringPanelRegistry.SetScoreCommitted(position, ws)
			web.arena.ScoringStatusNotifier.Notify()
		case "score", "undo":
			if err = web.handleScoreCommand(command, data, source); err != nil {
				writeWebsocketError(ws, err.Error())
			}
		default:
			writeWebsocketError(ws, fmt.Sprintf("Invalid message type '%s'.", command))
		}
	}
}

// handleScoreCommand carries out a "score" or "undo" command from a scoring or referee panel. Whether the source may
// make the change right now is the arena's decision; its refusal is returned for the panel to show.
func (web *Web) handleScoreCommand(command string, data any, source scoring.Source) error {
	if command == "undo" {
		var args undoCommand
		if err := mapstructure.Decode(data, &args); err != nil {
			return err
		}
		_, err := web.arena.UndoScoreChange(args.Seq, source)
		return err
	}

	var args scoreCommand
	if err := mapstructure.Decode(data, &args); err != nil {
		return err
	}
	switch args.Element {
	case scoring.TargetFoul, scoring.TargetCard, scoring.TargetDisqualified:
		// These have commands of their own, which apply the rules that go with them.
		return fmt.Errorf("the score command changes scoring elements; %s is not one", args.Element)
	}
	_, err := web.arena.ApplyScoreChange(args.change(source))
	return err
}

// writeScoreChangeHistory tells a panel that has just connected about the latest changes to the score of the loaded
// match, in the form of the "scoreChange" messages it would have received had it been connected all along.
func (web *Web) writeScoreChangeHistory(ws *websocket.Websocket) {
	matchResult, err := web.arena.BuildMatchResult()
	if err != nil {
		log.Printf("Failed to read the score changes of the loaded match: %v", err)
		return
	}
	inEffect := make(map[int]bool)
	for _, change := range scoring.Live(matchResult.Changes) {
		inEffect[change.Seq] = true
	}
	var history []field.ScoreChangeMessage
	for _, change := range matchResult.Changes {
		if change.Reverts == 0 {
			history = append(history, field.ScoreChangeMessage{Change: change, Undone: !inEffect[change.Seq]})
		}
	}
	if len(history) > scoreChangeHistoryLength {
		history = history[len(history)-scoreChangeHistoryLength:]
	}
	for _, message := range history {
		writeWebsocketMessage(ws, "scoreChange", message)
	}
}
