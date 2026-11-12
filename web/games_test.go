package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/stretchr/testify/assert"
)

func (web *Web) requestWithBody(method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, strings.NewReader(body))
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

func decodeJson(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded), recorder.Body.String())
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	return decoded
}

func readExampleGame(t *testing.T, id string) string {
	src, err := os.ReadFile(filepath.Join("..", "games", id, gamedef.GameFile))
	assert.NoError(t, err)
	return string(src)
}

func TestGamesPage(t *testing.T) {
	web := setupTestWeb(t)
	recorder := web.getHttpResponse("/setup/games")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Test match")
	assert.Contains(t, recorder.Body.String(), "/static/js/setup_games.js")
}

func TestGamesApiListAndGet(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/api/v1/games")
	assert.Equal(t, 200, recorder.Code)
	games := decodeJson(t, recorder)["games"].([]any)
	var ids []string
	for _, game := range games {
		summary := game.(map[string]any)
		ids = append(ids, summary["id"].(string))
		assert.Equal(t, true, summary["valid"], summary["id"])
		assert.Empty(t, summary["diagnostics"])
	}
	assert.Equal(t, []string{"ball-count", "orbital-rush", "rebuilt-style"}, ids)

	recorder = web.getHttpResponse("/api/v1/games/ball-count")
	assert.Equal(t, 200, recorder.Code)
	game := decodeJson(t, recorder)
	assert.Equal(t, "ball-count", game["id"])
	assert.Equal(t, true, game["valid"])
	assert.Equal(t, readExampleGame(t, "ball-count"), game["yaml"])
	view := game["view"].(map[string]any)
	assert.Equal(t, "Ball Count", view["name"])
	assert.Equal(t, []any{"R1", "R2", "R3", "B1", "B2", "B3"}, view["stations"])
	// The view uses the lower-case names the browser code reads.
	alliance := view["alliances"].([]any)[0].(map[string]any)
	assert.Equal(t, "red", alliance["id"])
	assert.Equal(t, "#d32f2f", alliance["color"])

	recorder = web.getHttpResponse("/api/v1/games/no-such-game")
	assert.Equal(t, 404, recorder.Code)
	assert.Equal(t, `there is no game "no-such-game"`, decodeJson(t, recorder)["error"])
	recorder = web.getHttpResponse("/api/v1/games/Not%20Valid")
	assert.Equal(t, 400, recorder.Code)
}

func TestGamesApiValidate(t *testing.T) {
	web := setupTestWeb(t)
	valid := readExampleGame(t, "ball-count")

	recorder := web.requestWithBody("POST", "/api/v1/games/validate", valid)
	assert.Equal(t, 200, recorder.Code)
	response := decodeJson(t, recorder)
	assert.Equal(t, true, response["valid"])
	assert.Empty(t, response["diagnostics"])
	assert.NotNil(t, response["view"])

	broken := strings.Replace(valid, "own.phase_points.balls.auto", "own.phase_points.bals.auto", 1)
	recorder = web.requestWithBody("POST", "/api/v1/games/validate", broken)
	assert.Equal(t, 200, recorder.Code)
	response = decodeJson(t, recorder)
	assert.Equal(t, false, response["valid"])
	assert.Nil(t, response["view"])
	diagnostic := response["diagnostics"].([]any)[0].(map[string]any)
	assert.Equal(t, "error", diagnostic["severity"])
	assert.Contains(t, diagnostic["message"], `has no property "bals" (did you mean "balls"?)`)
	assert.Equal(t, 53.0, diagnostic["line"])

	recorder = web.requestWithBody("POST", "/api/v1/games/validate", strings.Repeat("x", maxGameRequestBytes+1))
	assert.Equal(t, 413, recorder.Code)
}

func TestGamesApiSave(t *testing.T) {
	web := setupTestWeb(t)
	// Point the registry at a scratch directory so the test never writes into the repository's games.
	dir := t.TempDir()
	web.arena.Games = gamedef.NewRegistry(dir)
	assert.NoError(t, web.arena.Games.Load())
	events := web.arena.Bus.Subscribe([]string{"system.game_reloaded"}, 5)
	defer events.Close()

	game := strings.Replace(readExampleGame(t, "ball-count"), "id: ball-count", "id: my-game", 1)
	recorder := web.requestWithBody("PUT", "/api/v1/games/my-game", game)
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	response := decodeJson(t, recorder)
	assert.Equal(t, true, response["saved"])
	assert.Equal(t, true, response["valid"])
	assert.FileExists(t, filepath.Join(dir, "my-game", gamedef.GameFile))
	assert.NotNil(t, web.arena.Games.Get("my-game"))
	select {
	case event := <-events.C:
		assert.Equal(t, map[string]any{"id": "my-game", "valid": true}, event.Data)
	default:
		t.Error("saving a game did not publish an event")
	}

	// A definition with errors is rejected with its diagnostics, and the saved file is left alone.
	recorder = web.requestWithBody("PUT", "/api/v1/games/my-game", strings.Replace(game, "type: counter", "type: tally", 1))
	assert.Equal(t, 422, recorder.Code)
	response = decodeJson(t, recorder)
	assert.Equal(t, false, response["saved"])
	assert.Equal(t, false, response["valid"])
	assert.NotEmpty(t, response["diagnostics"])
	saved, err := web.arena.Games.Source("my-game")
	assert.NoError(t, err)
	assert.Equal(t, game, string(saved))

	// The id in the URL must match the file, and must be a plain name.
	recorder = web.requestWithBody("PUT", "/api/v1/games/other-name", game)
	assert.Equal(t, 422, recorder.Code)
	recorder = web.requestWithBody("PUT", "/api/v1/games/Bad%20Id", game)
	assert.Equal(t, 400, recorder.Code)

	// Saving requires the admin password when one is set.
	web.arena.EventSettings.AdminPassword = "secret"
	recorder = web.requestWithBody("PUT", "/api/v1/games/my-game", game)
	assert.Equal(t, 307, recorder.Code)
}

func TestGamesApiScore(t *testing.T) {
	web := setupTestWeb(t)

	// By id.
	recorder := web.requestWithBody("POST", "/api/v1/games/score", `{
		"id": "ball-count",
		"state": {"red": {"balls": {"auto": 3, "teleop": 10}, "fouls": ["foul"]}, "blue": {"balls": {"teleop": 12}}}
	}`)
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	response := decodeJson(t, recorder)
	result := response["result"].(map[string]any)
	assert.Equal(t, "red", result["winner"])
	alliances := result["alliances"].(map[string]any)
	assert.Equal(t, 16.0, alliances["red"].(map[string]any)["score"])
	assert.Equal(t, 15.0, alliances["blue"].(map[string]any)["score"])
	assert.Equal(t, 2.0, alliances["red"].(map[string]any)["rp_total"])
	assert.NotNil(t, response["live"])
	assert.NotNil(t, response["state"])

	// By YAML, so an unsaved definition can be tried out. This is the form the sandbox page sends, including
	// values wrapped with their phase, parameters, the playoff flag and the clock.
	request := map[string]any{
		"yaml": readExampleGame(t, "orbital-rush"),
		"state": map[string]any{
			"field": map[string]any{"gate_open": map[string]any{"value": true, "phase": "launch"}},
			"red": map[string]any{
				"cargo": map[string]any{"reentry": 5}, "momentum": map[string]any{"value": true, "phase": "launch"},
				"robots":       []any{map[string]any{"dock": map[string]any{"value": "high", "phase": "reentry"}}, map[string]any{}},
				"fouls":        []any{},
				"cards":        map[string]any{"1": "yellow"},
				"disqualified": false,
			},
		},
		"params":  map[string]any{"overdrive_enabled": 0},
		"playoff": true,
		"clock":   map[string]any{"phase": "reentry", "elapsed": 25},
	}
	body, _ := json.Marshal(request)
	recorder = web.requestWithBody("POST", "/api/v1/games/score", string(body))
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	response = decodeJson(t, recorder)
	red := response["result"].(map[string]any)["alliances"].(map[string]any)["red"].(map[string]any)
	assert.Equal(t, 26.0, red["score"]) // 10 cargo + 10 dock + 6 fuel; overdrive is switched off.
	assert.Equal(t, 0.0, red["points"].(map[string]any)["overdrive"])
	assert.Equal(t, 0.0, red["rp"].(map[string]any)["clean"])
	live := response["live"].(map[string]any)
	assert.Equal(t, map[string]any{"gate_lit": true, "urgency": "high"}, live["field"])
	assert.Equal(t, true, live["alliances"].(map[string]any)["red"].(map[string]any)["lane_open"])

	// Errors.
	recorder = web.requestWithBody("POST", "/api/v1/games/score", `{"id": "no-such-game"}`)
	assert.Equal(t, 404, recorder.Code)
	recorder = web.requestWithBody("POST", "/api/v1/games/score", `not json`)
	assert.Equal(t, 400, recorder.Code)
	recorder = web.requestWithBody("POST", "/api/v1/games/score", `{"yaml": "schema: 1\n"}`)
	assert.Equal(t, 422, recorder.Code)
	assert.NotEmpty(t, decodeJson(t, recorder)["diagnostics"])
	recorder = web.requestWithBody("POST", "/api/v1/games/score", `{"id": "ball-count", "state": {"red": {"nope": 1}}}`)
	assert.Equal(t, 400, recorder.Code)
	assert.Equal(t, `state of red: there is no element "nope"`, decodeJson(t, recorder)["error"])
}
