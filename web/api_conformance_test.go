package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// apiDescription is docs/openapi.yaml, ready to check answers against.
type apiDescription struct {
	document map[string]any
	compiler *jsonschema.Compiler
	schemas  map[string]*jsonschema.Schema
}

func loadApiDescription(t *testing.T) *apiDescription {
	src, err := os.ReadFile(filepath.Join("..", openApiSpecPath))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(src, &document))
	// Through JSON, so that the schema library sees the value types it expects.
	data, err := json.Marshal(document)
	require.NoError(t, err)
	resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	require.NoError(t, compiler.AddResource("openapi.json", resource))
	return &apiDescription{document: document, compiler: compiler, schemas: map[string]*jsonschema.Schema{}}
}

// pointerEscape writes one step of a JSON pointer.
func pointerEscape(step string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(step)
}

// check holds an answer to what the description says the operation answers with that status: the status must be
// described, and a JSON answer must fit its schema.
func (description *apiDescription) check(
	t *testing.T, method, path string, recorder *httptest.ResponseRecorder,
) {
	label := fmt.Sprintf("%s %s -> %d", method, path, recorder.Code)
	operation, _ := description.document["paths"].(map[string]any)[path].(map[string]any)
	responses, _ := operation[strings.ToLower(method)].(map[string]any)
	answers, _ := responses["responses"].(map[string]any)
	answer, described := answers[strconv.Itoa(recorder.Code)].(map[string]any)
	if !assert.True(t, described, "%s: docs/openapi.yaml does not describe this answer", label) {
		return
	}
	pointer := "#/paths/" + pointerEscape(path) + "/" + strings.ToLower(method) + "/responses/" +
		strconv.Itoa(recorder.Code)
	if reference, ok := answer["$ref"].(string); ok {
		// A shared answer, such as the error.
		pointer = reference
		shared, _ := description.document["components"].(map[string]any)["responses"].(map[string]any)
		answer, _ = shared[reference[strings.LastIndex(reference, "/")+1:]].(map[string]any)
	}
	content, _ := answer["content"].(map[string]any)
	if len(content) == 0 {
		assert.Zero(t, recorder.Body.Len(), "%s: an answer described without a body has one", label)
		return
	}
	// The answer must be of one of the kinds described; only JSON has a shape to check.
	kind, _, _ := strings.Cut(recorder.Header().Get("Content-Type"), ";")
	media, described := content[kind]
	if !assert.True(t, described, "%s: the description does not list answers of type %q", label, kind) {
		return
	}
	if kind != "application/json" {
		return
	}
	if described, _ := media.(map[string]any); described["schema"] == nil {
		// JSON whose shape the description leaves open, such as an export whose columns depend on the game.
		assert.True(t, json.Valid(recorder.Body.Bytes()), "%s: the answer is not JSON", label)
		return
	}
	pointer += "/content/application~1json/schema"
	schema := description.schemas[pointer]
	if schema == nil {
		var err error
		schema, err = description.compiler.Compile("openapi.json" + pointer)
		if !assert.NoError(t, err, label) {
			return
		}
		description.schemas[pointer] = schema
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(recorder.Body.Bytes()))
	if assert.NoError(t, err, label) {
		assert.NoError(t, schema.Validate(instance), "%s: %s", label, recorder.Body.String())
	}
}

// What the API really answers must fit what docs/openapi.yaml says it answers: the statuses and the shapes.
func TestApiAnswersMatchTheDescription(t *testing.T) {
	description := loadApiDescription(t)
	web := setupTestWeb(t)
	database := web.arena.Database

	// A team, a played match with a result, a ranking, and a webhook with a delivery in its log.
	assert.NoError(t, database.CreateTeam(&model.Team{Id: 254, Name: "Team", Nickname: "Nick", City: "City"}))
	match := model.Match{Type: model.Qualification, TypeOrder: 1, ShortName: "Q1", LongName: "Qualification 1",
		Red1: 254, Blue1: 1114, Status: model.TieMatch}
	assert.NoError(t, database.CreateMatch(&match))
	assert.NoError(t, database.SaveGameSnapshot(web.arena.Game))
	matchResult := model.NewMatchResult(web.arena.Game)
	matchResult.MatchId, matchResult.MatchType = match.Id, match.Type
	_, err := matchResult.Edit(web.arena.Game, scoring.Change{
		Alliance: "red", Element: "fuel", Op: scoring.OpSet, Value: 12, Bucket: "auto",
		Source: scoring.Source{Kind: scoring.SourceScorer, Name: "Red near side"},
	})
	assert.NoError(t, err)
	assert.NoError(t, database.CreateMatchResult(matchResult))
	ranking := model.Ranking{TeamId: 254}
	ranking.Rank = 1
	assert.NoError(t, database.CreateRanking(&ranking))
	web.arena.Bus.Publish("match.started", map[string]any{"match": "Q1"})
	// The journal writes into a folder beside the test's database, which goes with the test.
	require.NoError(t, web.arena.Journal.Start())
	defer web.arena.Journal.Stop()
	today := time.Now().Format("2006-01-02")

	asJson := map[string]string{"Content-Type": "application/json"}
	recorder := web.request(
		"POST", "/api/v1/webhooks", `{"url":"http://127.0.0.1:9/hook","name":"Overlay"}`, asJson,
	)
	description.check(t, "POST", "/api/v1/webhooks", recorder)
	require.Equal(t, 201, recorder.Code, recorder.Body.String())
	recorder = web.request("POST", "/api/v1/webhooks/1/test", "", nil)
	description.check(t, "POST", "/api/v1/webhooks/{id}/test", recorder)
	require.Equal(t, 202, recorder.Code)
	deliveryId := decodeJson(t, recorder)["id"].(string)

	matchPath := fmt.Sprintf("/api/v1/matches/%d", match.Id)
	for _, test := range []struct {
		method, path, request, body string
		status                      int
	}{
		{"GET", "/api/v1/time", "/api/v1/time", "", 200},
		{"GET", "/api/v1/health", "/api/v1/health", "", 200},
		{"GET", "/api/v1/openapi.json", "/api/v1/openapi.json", "", 200},
		{"GET", "/api/v1/auth", "/api/v1/auth", "", 200},
		{"GET", "/api/v1/game", "/api/v1/game", "", 200},
		{"GET", "/api/v1/match", "/api/v1/match", "", 200},
		{"GET", "/api/v1/matches", "/api/v1/matches", "", 200},
		{"GET", "/api/v1/matches", "/api/v1/matches?type=nonsense", "", 400},
		{"GET", "/api/v1/matches/{id}", matchPath, "", 200},
		{"GET", "/api/v1/matches/{id}", "/api/v1/matches/999", "", 404},
		{"GET", "/api/v1/rankings", "/api/v1/rankings", "", 200},
		{"GET", "/api/v1/teams", "/api/v1/teams", "", 200},
		{"GET", "/api/v1/alliances", "/api/v1/alliances", "", 200},
		{"GET", "/api/v1/events", "/api/v1/events", "", 200},
		{"GET", "/api/v1/exports/{file}", "/api/v1/exports/matches.json", "", 200},
		{"GET", "/api/v1/exports/{file}", "/api/v1/exports/matches.csv", "", 200},
		{"GET", "/api/v1/journal", "/api/v1/journal", "", 200},
		{"GET", "/api/v1/journal/{day}", "/api/v1/journal/" + today, "", 200},
		{"GET", "/api/v1/journal/{day}", "/api/v1/journal/2001-01-01", "", 404},
		{"GET", "/api/v1/journal/{day}", "/api/v1/journal/yesterday", "", 400},
		{"GET", "/api/v1/openapi.yaml", "/api/v1/openapi.yaml", "", 200},
		{"GET", "/api/v1/exports/{file}", "/api/v1/exports/nothing.json", "", 404},
		{"GET", "/api/v1/games", "/api/v1/games", "", 200},
		{"GET", "/api/v1/games/{id}", "/api/v1/games/ball-count", "", 200},
		{"GET", "/api/v1/games/{id}", "/api/v1/games/no-such-game", "", 404},
		{"POST", "/api/v1/games/validate", "/api/v1/games/validate", "id: x", 200},
		{"POST", "/api/v1/games/score", "/api/v1/games/score", `{"id":"ball-count","state":{}}`, 200},
		{"POST", "/api/v1/games/score", "/api/v1/games/score", `{"id":"no-such-game"}`, 404},
		{"GET", "/api/v1/webhooks", "/api/v1/webhooks", "", 200},
		{"GET", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1", "", 200},
		{"GET", "/api/v1/webhooks/{id}", "/api/v1/webhooks/9", "", 404},
		{"GET", "/api/v1/webhooks/{id}", "/api/v1/webhooks/x", "", 400},
		{"PUT", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1", `{"enabled":false}`, 200},
		{"PUT", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1", `{"url":"ftp://x"}`, 400},
		{"POST", "/api/v1/webhooks", "/api/v1/webhooks", `{"name":"No address"}`, 400},
		{"POST", "/api/v1/webhooks/{id}/secret", "/api/v1/webhooks/1/secret", "", 200},
		{"GET", "/api/v1/webhooks/{id}/deliveries", "/api/v1/webhooks/1/deliveries", "", 200},
		{"GET", "/api/v1/webhooks/{id}/deliveries", "/api/v1/webhooks/1/deliveries?limit=0", "", 400},
		{
			"POST", "/api/v1/webhooks/{id}/deliveries/{deliveryId}/resend",
			"/api/v1/webhooks/1/deliveries/" + deliveryId + "/resend", "", 202,
		},
		{
			"POST", "/api/v1/webhooks/{id}/deliveries/{deliveryId}/resend",
			"/api/v1/webhooks/1/deliveries/no-such-delivery/resend", "", 404,
		},
		{"DELETE", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1", "", 204},
		{"DELETE", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1", "", 404},
	} {
		recorder = web.request(test.method, test.request, test.body, asJson)
		assert.Equal(t, test.status, recorder.Code, "%s %s: %s", test.method, test.request, recorder.Body.String())
		description.check(t, test.method, test.path, recorder)
	}

	// With logins on: nobody gets 401 and a role that may not gets 403, both in the shape of the shared error.
	web.arena.EventSettings.AdminPassword = "the admin password"
	_, readToken, err := database.CreateApiToken("Reader", model.RoleReadOnly)
	require.NoError(t, err)
	for _, test := range []struct{ method, path, request string }{
		{"GET", "/api/v1/webhooks", "/api/v1/webhooks"},
		{"POST", "/api/v1/webhooks", "/api/v1/webhooks"},
		{"GET", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1"},
		{"PUT", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1"},
		{"DELETE", "/api/v1/webhooks/{id}", "/api/v1/webhooks/1"},
		{"POST", "/api/v1/webhooks/{id}/secret", "/api/v1/webhooks/1/secret"},
		{"POST", "/api/v1/webhooks/{id}/test", "/api/v1/webhooks/1/test"},
		{"GET", "/api/v1/webhooks/{id}/deliveries", "/api/v1/webhooks/1/deliveries"},
		{"POST", "/api/v1/webhooks/{id}/deliveries/{deliveryId}/resend", "/api/v1/webhooks/1/deliveries/x/resend"},
	} {
		recorder = web.request(test.method, test.request, "{}", nil)
		assert.Equal(t, 401, recorder.Code, test.request)
		description.check(t, test.method, test.path, recorder)
		recorder = web.request(test.method, test.request, "{}", bearer(readToken))
		assert.Equal(t, 403, recorder.Code, test.request)
		description.check(t, test.method, test.path, recorder)
	}
	recorder = web.request("GET", "/api/v1/auth", "", bearer(readToken))
	description.check(t, "GET", "/api/v1/auth", recorder)

	// Every route that reads says 401 to nobody, and the description says so for each.
	for _, test := range []struct{ path, request string }{
		{"/api/v1/game", "/api/v1/game"},
		{"/api/v1/match", "/api/v1/match"},
		{"/api/v1/matches", "/api/v1/matches"},
		{"/api/v1/matches/{id}", matchPath},
		{"/api/v1/rankings", "/api/v1/rankings"},
		{"/api/v1/teams", "/api/v1/teams"},
		{"/api/v1/alliances", "/api/v1/alliances"},
		{"/api/v1/events", "/api/v1/events"},
		{"/api/v1/events/stream", "/api/v1/events/stream"},
		{"/api/v1/events/ws", "/api/v1/events/ws"},
		{"/api/v1/exports/{file}", "/api/v1/exports/matches.json"},
		{"/api/v1/journal", "/api/v1/journal"},
		{"/api/v1/journal/{day}", "/api/v1/journal/" + today},
		{"/api/v1/diagnostics/connection/{matchId}/{stationId}", "/api/v1/diagnostics/connection/1/R1"},
		{"/api/v1/games", "/api/v1/games"},
		{"/api/v1/games/{id}", "/api/v1/games/ball-count"},
	} {
		recorder = web.request("GET", test.request, "", nil)
		assert.Equal(t, 401, recorder.Code, test.request)
		description.check(t, "GET", test.path, recorder)
		// With a token the same request gets past the door. The two streams are left out: they would never end.
		if !strings.HasPrefix(test.path, "/api/v1/events/") {
			assert.NotEqual(t, 401, web.request("GET", test.request, "", bearer(readToken)).Code, test.request)
		}
	}
	for _, test := range []struct{ method, path, request string }{
		{"POST", "/api/v1/games/validate", "/api/v1/games/validate"},
		{"POST", "/api/v1/games/score", "/api/v1/games/score"},
		{"PUT", "/api/v1/games/{id}", "/api/v1/games/some-game"},
	} {
		recorder = web.request(test.method, test.request, "{}", nil)
		assert.Equal(t, 401, recorder.Code, test.request)
		description.check(t, test.method, test.path, recorder)
	}
	recorder = web.request("PUT", "/api/v1/games/some-game", "id: some-game", bearer(readToken))
	assert.Equal(t, 403, recorder.Code)
	description.check(t, "PUT", "/api/v1/games/{id}", recorder)
}
