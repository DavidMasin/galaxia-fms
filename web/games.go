// Web handlers and API for game definitions: listing, editing, validating, and scoring in a sandbox.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
)

// The largest game definition or sandbox request the API accepts.
const maxGameRequestBytes = 1 << 20

// Shows the game editor and sandbox.
func (web *Web) gamesGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	template, err := web.parseFiles("templates/setup_games.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
	}{web.arena.EventSettings}
	if err = template.ExecuteTemplate(w, "base", data); err != nil {
		handleWebErr(w, err)
	}
}

func writeJson(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func writeJsonError(w http.ResponseWriter, status int, message string) {
	writeJson(w, status, map[string]string{"error": message})
}

// Lists every game in the games directory, including those that fail to load.
func (web *Web) gamesApiListHandler(w http.ResponseWriter, r *http.Request) {
	web.arena.ReloadGames()
	writeJson(w, http.StatusOK, map[string]any{"games": web.arena.Games.List()})
}

// gameResponse is what the API returns about one game definition.
type gameResponse struct {
	ID          string              `json:"id"`
	Yaml        string              `json:"yaml"`
	Valid       bool                `json:"valid"`
	Diagnostics gamedef.Diagnostics `json:"diagnostics"`
	View        *gamedef.View       `json:"view"` // Null when the game has errors.
	Saved       bool                `json:"saved"`
}

func describeGame(id string, src []byte) gameResponse {
	game, diagnostics := gamedef.Parse(src)
	response := gameResponse{ID: id, Yaml: string(src), Valid: game != nil, Diagnostics: diagnostics}
	if response.Diagnostics == nil {
		response.Diagnostics = gamedef.Diagnostics{}
	}
	if game != nil {
		response.View = game.View()
	}
	return response
}

// Returns one game's YAML, its diagnostics, and its browser view if it is valid.
func (web *Web) gameApiGetHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := web.arena.Games.Source(id)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, fs.ErrNotExist) {
			status = http.StatusNotFound
			err = fmt.Errorf("there is no game %q", id)
		}
		writeJsonError(w, status, err.Error())
		return
	}
	writeJson(w, http.StatusOK, describeGame(id, src))
}

// Validates a game definition sent as the request body, without saving it.
func (web *Web) gameApiValidateHandler(w http.ResponseWriter, r *http.Request) {
	src, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGameRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusRequestEntityTooLarge, "the game definition is too large")
		return
	}
	writeJson(w, http.StatusOK, describeGame("", src))
}

// Saves a game definition sent as the request body. Nothing is written if it has errors.
func (web *Web) gameApiPutHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	src, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGameRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusRequestEntityTooLarge, "the game definition is too large")
		return
	}
	diagnostics, err := web.arena.Games.Save(id, src)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	response := describeGame(id, src)
	response.Diagnostics = diagnostics
	if response.Diagnostics == nil {
		response.Diagnostics = gamedef.Diagnostics{}
	}
	if diagnostics.HasErrors() {
		response.Valid, response.View = false, nil
		writeJson(w, http.StatusUnprocessableEntity, response)
		return
	}
	response.Saved = true
	web.arena.Bus.Publish("system.game_reloaded", map[string]any{"id": id, "valid": true})
	writeJson(w, http.StatusOK, response)
}

// scoreRequest asks for the score of a raw state. The game is given either by id or as YAML, so the editor can
// try out a definition before it is saved.
type scoreRequest struct {
	ID      string                    `json:"id"`
	Yaml    string                    `json:"yaml"`
	State   map[string]map[string]any `json:"state"`
	Params  map[string]float64        `json:"params"`
	Playoff bool                      `json:"playoff"`
	Clock   struct {
		Phase   string  `json:"phase"`
		Elapsed float64 `json:"elapsed"`
	} `json:"clock"`
}

// Computes the score of a raw state: the sandbox behind the game editor's test match.
func (web *Web) gameApiScoreHandler(w http.ResponseWriter, r *http.Request) {
	var request scoreRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGameRequestBytes))
	if err := decoder.Decode(&request); err != nil {
		writeJsonError(w, http.StatusBadRequest, "the request is not valid JSON: "+err.Error())
		return
	}

	var game *gamedef.Game
	if request.Yaml != "" {
		var diagnostics gamedef.Diagnostics
		if game, diagnostics = gamedef.Parse([]byte(request.Yaml)); game == nil {
			writeJson(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "the game definition has errors", "diagnostics": diagnostics,
			})
			return
		}
	} else if game = web.arena.Games.Get(request.ID); game == nil {
		writeJsonError(w, http.StatusNotFound, fmt.Sprintf("there is no valid game %q", request.ID))
		return
	}

	state, err := scoring.BuildState(game, request.State)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := scoring.Compute(game, state, scoring.Options{Params: request.Params, Playoff: request.Playoff})
	if err != nil {
		writeJsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	live, err := result.Live(scoring.ClockAt(game, request.Clock.Phase, request.Clock.Elapsed))
	if err != nil {
		writeJsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJson(w, http.StatusOK, map[string]any{"result": result, "live": live, "state": state})
}
