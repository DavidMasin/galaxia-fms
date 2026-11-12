// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web routes for editing match results.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// The largest edited state the result editor accepts.
const maxMatchReviewRequestBytes = 1 << 20

// MatchReviewSide is one side of a match in the match review list: the red side first, then the blue side.
type MatchReviewSide struct {
	Slot          string
	Teams         []int
	Score         string
	RankingPoints []namedValue
	RpTotal       string
}

type MatchReviewListItem struct {
	Id         int
	ShortName  string
	Time       string
	Sides      []MatchReviewSide
	ColorClass string
	IsComplete bool
}

// matchReviewEditData is everything the result editor's script needs to build the form: the game the result was
// played with, the raw state as stored, who played, and the result as it currently computes.
type matchReviewEditData struct {
	MatchId   string           `json:"matchId"` // The match's id, or "current" for the match on the field.
	IsCurrent bool             `json:"isCurrent"`
	IsPlayoff bool             `json:"isPlayoff"`
	Game      *gamedef.View    `json:"game"`
	State     *scoring.State   `json:"state"`
	Teams     map[string][]int `json:"teams"`
	Summary   *resultView      `json:"summary"`
}

// Shows the match review interface.
func (web *Web) matchReviewHandler(w http.ResponseWriter, r *http.Request) {
	practiceMatches, err := web.buildMatchReviewList(model.Practice)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	qualificationMatches, err := web.buildMatchReviewList(model.Qualification)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	playoffMatches, err := web.buildMatchReviewList(model.Playoff)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	template, err := web.parseFiles("templates/match_review.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	matchesByType := map[model.MatchType][]MatchReviewListItem{
		model.Practice:      practiceMatches,
		model.Qualification: qualificationMatches,
		model.Playoff:       playoffMatches,
	}
	currentMatchType := web.arena.CurrentMatch.Type
	if currentMatchType == model.Test {
		currentMatchType = model.Practice
	}
	data := struct {
		*model.EventSettings
		MatchesByType    map[model.MatchType][]MatchReviewListItem
		CurrentMatchType model.MatchType
	}{web.arena.EventSettings, matchesByType, currentMatchType}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Shows the page to edit the results for a match.
func (web *Web) matchReviewEditGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, resultGame, isCurrent, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	web.renderMatchReviewEdit(w, r, match, matchResult, resultGame, isCurrent, "")
}

// renderMatchReviewEdit shows the result editor for the given result as it is stored, with an error message if the
// changes the user asked for could not be made.
func (web *Web) renderMatchReviewEdit(
	w http.ResponseWriter, r *http.Request, match *model.Match, matchResult *model.MatchResult, g *gamedef.Game,
	isCurrent bool, errorMessage string,
) {
	template, err := web.parseFiles("templates/edit_match_result.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	summary, err := computeResultView(web.arena.Database, match, matchResult)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	editData := matchReviewEditData{
		MatchId: r.PathValue("matchId"), IsCurrent: isCurrent, IsPlayoff: match.Type == model.Playoff, Game: g.View(),
		State: matchResult.State, Teams: resultTeams(g, match, matchResult), Summary: summary,
	}
	// The data goes into a script element as it is. Marshal escapes the characters that could end one early.
	editDataJson, err := json.Marshal(editData)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		Match          *model.Match
		MatchIdPath    string
		GameName       string
		EditDataJson   string
		IsCurrentMatch bool
		Changes        []auditLogEntry
		ErrorMessage   string
	}{
		web.arena.EventSettings, match, r.PathValue("matchId"), g.Name, string(editDataJson), isCurrent,
		buildAuditLog(g, matchResult), errorMessage,
	}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// computeResultView computes a result with the game it was played with and lays it out for a page.
func computeResultView(
	database *model.Database, match *model.Match, matchResult *model.MatchResult,
) (*resultView, error) {
	matchResult.MatchType = match.Type
	result, g, err := matchResult.Compute(database, match.UseTiebreakCriteria)
	if err != nil {
		return nil, err
	}
	return buildResultView(g, result, resultTeams(g, match, matchResult)), nil
}

// readEditedState reads the raw state the result editor sends back, as JSON in the given reader.
func readEditedState(reader io.Reader) (*scoring.State, error) {
	var state scoring.State
	if err := json.NewDecoder(reader).Decode(&state); err != nil {
		return nil, fmt.Errorf("the edited result could not be read: %w", err)
	}
	return &state, nil
}

// applyEditedState makes the stored result match the edited raw state by appending the changes that lead from one
// to the other. It returns the changes it made and a description of each one that was refused; a refused change
// leaves the result as it was.
func applyEditedState(
	g *gamedef.Game, matchResult *model.MatchResult, edited *scoring.State,
) (applied []scoring.Change, problems []string) {
	if matchResult.State == nil {
		matchResult.State, _ = scoring.Fold(g, matchResult.Changes)
	}
	for _, change := range stateChanges(g, matchResult.State, edited, matchReviewSource) {
		if _, err := matchResult.Edit(g, change); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		applied = append(applied, change)
	}
	return
}

// Calculates the summary of an edited result without saving it.
func (web *Web) matchReviewSummaryPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, resultGame, _, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	edited, err := readEditedState(http.MaxBytesReader(w, r.Body, maxMatchReviewRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The result was read for this request alone, so it can be edited freely; nothing here is stored.
	_, problems := applyEditedState(resultGame, matchResult, edited)
	summary, err := computeResultView(web.arena.Database, match, matchResult)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	summary.Errors = append(summary.Errors, problems...)
	writeJson(w, http.StatusOK, summary)
}

// Updates the results for a match.
func (web *Web) matchReviewEditPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, resultGame, isCurrent, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	edited, err := readEditedState(strings.NewReader(r.PostFormValue("stateJson")))
	if err != nil {
		handleWebErr(w, err)
		return
	}

	// Nothing is saved unless every change can be made, so the editor is shown again with what was refused. The
	// result it shows is read afresh, because the one at hand has the accepted changes in it.
	changes, problems := applyEditedState(resultGame, matchResult, edited)
	if len(problems) > 0 {
		match, matchResult, resultGame, isCurrent, err = web.getMatchResultFromRequest(r)
		if err != nil {
			handleWebErr(w, err)
			return
		}
		web.renderMatchReviewEdit(
			w, r, match, matchResult, resultGame, isCurrent, "Nothing was saved: "+strings.Join(problems, "; "),
		)
		return
	}

	if isCurrent {
		// If editing the current match, make the same changes to the score on the field.
		for _, change := range changes {
			if _, err = web.arena.ApplyScoreChange(change); err != nil {
				handleWebErr(w, err)
				return
			}
		}

		http.Redirect(w, r, "/match_play", 303)
	} else {
		err = web.commitMatchScore(match, matchResult, true)
		if err != nil {
			handleWebErr(w, err)
			return
		}

		http.Redirect(w, r, "/match_review", 303)
	}
}

// Undoes one change of a match's result, or brings an undone change back by undoing its undo record.
func (web *Web) matchReviewUndoPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, resultGame, isCurrent, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	seq, err := strconv.Atoi(r.PostFormValue("seq"))
	if err != nil {
		handleWebErr(w, fmt.Errorf("Error: invalid score change '%s'", r.PostFormValue("seq")))
		return
	}

	if isCurrent {
		_, err = web.arena.UndoScoreChange(seq, matchReviewSource)
	} else if _, err = matchResult.Undo(resultGame, seq, matchReviewSource); err == nil {
		err = web.commitMatchScore(match, matchResult, true)
	}
	if err != nil {
		handleWebErr(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/match_review/%s/edit", r.PathValue("matchId")), 303)
}

// Load the match result for the match referenced in the HTTP query string, together with the game it was played
// with, which is the game every page about the result is built from.
func (web *Web) getMatchResultFromRequest(
	r *http.Request,
) (*model.Match, *model.MatchResult, *gamedef.Game, bool, error) {
	// If editing the current match, get it from memory instead of the DB.
	if r.PathValue("matchId") == "current" {
		matchResult, err := web.arena.BuildMatchResult()
		if err != nil {
			return nil, nil, nil, false, err
		}
		return web.arena.CurrentMatch, matchResult, web.arena.Game, true, nil
	}

	matchId, err := strconv.Atoi(r.PathValue("matchId"))
	if err != nil {
		return nil, nil, nil, false, err
	}
	match, err := web.arena.Database.GetMatchById(matchId)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if match == nil {
		return nil, nil, nil, false, fmt.Errorf("Error: No such match: %d", matchId)
	}
	matchResult, err := web.arena.Database.GetMatchResultForMatch(matchId)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if matchResult == nil {
		// We're scoring a match that hasn't been played yet, but that's okay. It is scored with the active game,
		// which like the game of any result has to be stored for the result to be computed.
		if err = web.arena.Database.SaveGameSnapshot(web.arena.Game); err != nil {
			return nil, nil, nil, false, err
		}
		matchResult = model.NewMatchResult(web.arena.Game)
		matchResult.MatchId = matchId
		matchResult.MatchType = match.Type
		matchResult.Teams = teamsForGame(web.arena.Game, match)
		// Only the parameters the game declares are kept, as for a result that comes off the field.
		for _, param := range web.arena.Game.Params {
			if value, ok := web.arena.EventSettings.GameParams[param.ID]; ok {
				matchResult.Params[param.ID] = value
			}
		}
		return match, matchResult, web.arena.Game, false, nil
	}

	resultGame, err := web.arena.Database.GetSnapshotGame(matchResult.GameHash)
	if err != nil {
		return nil, nil, nil, false, err
	}
	// The stored state is a cache of the change list in the form it was written to the datastore, where an empty
	// map and no map are the same thing. Folding the changes again gives a state that can be edited.
	matchResult.State, _ = scoring.Fold(resultGame, matchResult.Changes)
	return match, matchResult, resultGame, false, nil
}

// Constructs the list of matches to display in the match review interface.
func (web *Web) buildMatchReviewList(matchType model.MatchType) ([]MatchReviewListItem, error) {
	matches, err := web.arena.Database.GetMatchesByType(matchType, false)
	if err != nil {
		return []MatchReviewListItem{}, err
	}

	matchReviewList := make([]MatchReviewListItem, len(matches))
	for i, match := range matches {
		matchReviewList[i].Id = match.Id
		matchReviewList[i].ShortName = match.ShortName
		matchReviewList[i].Time = match.Time.Local().Format("Mon 1/02 03:04 PM")
		for index, teams := range matchSideTeams(&match) {
			matchReviewList[i].Sides = append(
				matchReviewList[i].Sides, MatchReviewSide{Slot: slotForIndex(index), Teams: teams},
			)
		}
		matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
		if err != nil {
			return []MatchReviewListItem{}, err
		}
		if matchResult != nil {
			// A result that can no longer be computed is listed without a score rather than taking the page down.
			summary, err := computeResultView(web.arena.Database, &match, matchResult)
			if err != nil {
				log.Printf("Failed to compute the result of match %d for match review: %v", match.Id, err)
			} else {
				for index, alliance := range summary.Alliances {
					if index >= len(matchReviewList[i].Sides) {
						break
					}
					side := &matchReviewList[i].Sides[index]
					side.Score, side.RpTotal, side.RankingPoints = alliance.Score, alliance.RpTotal, alliance.RankingPoints
				}
			}
		}
		switch match.Status {
		case model.RedWonMatch:
			matchReviewList[i].ColorClass = "red"
			matchReviewList[i].IsComplete = true
		case model.BlueWonMatch:
			matchReviewList[i].ColorClass = "blue"
			matchReviewList[i].IsComplete = true
		case model.TieMatch:
			matchReviewList[i].ColorClass = "yellow"
			matchReviewList[i].IsComplete = true
		default:
			matchReviewList[i].ColorClass = ""
			matchReviewList[i].IsComplete = false
		}
	}

	return matchReviewList, nil
}
