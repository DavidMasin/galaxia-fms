package web

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v3"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Every route in the API's table, and every version 1 route registered elsewhere, must be described in
// docs/openapi.yaml, and the description must not list anything that does not exist.
func TestApiRoutesMatchTheDescription(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", openApiSpecPath))
	assert.NoError(t, err)
	var spec struct {
		OpenAPI string                            `yaml:"openapi"`
		Paths   map[string]map[string]interface{} `yaml:"paths"`
	}
	assert.NoError(t, yaml.Unmarshal(src, &spec))
	assert.Equal(t, "3.1.0", spec.OpenAPI)

	described := map[string]bool{}
	for path, operations := range spec.Paths {
		for method := range operations {
			if method == "parameters" {
				continue
			}
			described[strings.ToUpper(method)+" "+path] = true
		}
	}
	registered := map[string]bool{}
	for _, route := range apiRoutes {
		registered[route.method+" "+route.path] = true
	}
	for _, route := range alreadyRegisteredApiRoutes {
		registered[route[0]+" "+route[1]] = true
	}
	var missing, extra []string
	for route := range registered {
		if !described[route] {
			missing = append(missing, route)
		}
	}
	for route := range described {
		if !registered[route] {
			extra = append(extra, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	assert.Empty(t, missing, "routes not described in docs/openapi.yaml")
	assert.Empty(t, extra, "operations described in docs/openapi.yaml that do not exist")

	// And the registered routes really answer.
	web := setupTestWeb(t)
	for _, route := range alreadyRegisteredApiRoutes {
		if route[0] == "GET" && !strings.Contains(route[1], "{") {
			assert.Equal(t, 200, web.getHttpResponse(route[1]).Code, route[1])
		}
	}
}

func TestApiTimeAndHealth(t *testing.T) {
	web := setupTestWeb(t)
	before := time.Now().UnixMilli()
	response := decodeJson(t, web.getHttpResponse("/api/v1/time"))
	after := time.Now().UnixMilli()
	assert.GreaterOrEqual(t, int64(response["t"].(float64)), before)
	assert.LessOrEqual(t, int64(response["t"].(float64)), after)
	_, err := time.Parse(time.RFC3339Nano, response["iso"].(string))
	assert.NoError(t, err)

	response = decodeJson(t, web.getHttpResponse("/api/v1/health"))
	assert.Equal(t, "ok", response["status"])
	assert.Equal(t, "rebuilt-style", response["game"])
	assert.Equal(t, "pre_match", response["matchState"])

	recorder := web.getHttpResponse("/api/v1/openapi.yaml")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "openapi: 3.1.0")

	response = decodeJson(t, web.getHttpResponse("/api/v1/game"))
	assert.Equal(t, "rebuilt-style", response["id"])
}

func TestApiMatchesAndResults(t *testing.T) {
	web := setupTestWeb(t)
	database := web.arena.Database
	match := model.Match{
		Type: model.Qualification, TypeOrder: 1, ShortName: "Q1", LongName: "Qualification 1", Red1: 254, Red2: 1114,
		Red3: 971, Blue1: 1678, Blue2: 2056, Blue3: 118, Time: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC),
	}
	assert.NoError(t, database.CreateMatch(&match))
	unplayed := model.Match{Type: model.Qualification, TypeOrder: 2, ShortName: "Q2", LongName: "Qualification 2"}
	assert.NoError(t, database.CreateMatch(&unplayed))

	// Store a result in which red scores 20 auto fuel and blue commits a major foul.
	matchResult := model.NewMatchResult(web.arena.Game)
	matchResult.MatchId = match.Id
	matchResult.MatchType = match.Type
	assert.NoError(t, database.SaveGameSnapshot(web.arena.Game))
	for _, change := range []scoring.Change{
		{Alliance: "red", Element: "fuel", Op: scoring.OpSet, Value: 20, Bucket: "auto"},
		{Alliance: "blue", Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: "major"},
	} {
		change.Source = scoring.Source{Kind: scoring.SourceAdmin}
		_, err := matchResult.Edit(web.arena.Game, change)
		assert.NoError(t, err)
	}
	assert.NoError(t, database.CreateMatchResult(matchResult))

	recorder := web.getHttpResponse("/api/v1/matches?type=qualification")
	assert.Equal(t, 200, recorder.Code)
	matches := decodeJson(t, recorder)["matches"].([]any)
	if assert.Len(t, matches, 2) {
		first := matches[0].(map[string]any)
		assert.Equal(t, "Q1", first["shortName"])
		assert.Equal(t, "Qualification", first["type"])
		assert.Equal(t, map[string]any{"red": []any{254.0, 1114.0, 971.0}, "blue": []any{1678.0, 2056.0, 118.0}},
			first["teams"])
		result := first["result"].(map[string]any)
		assert.Equal(t, "red", result["winner"])
		assert.Equal(t, map[string]any{"red": 35.0, "blue": 0.0}, result["scores"])
		assert.Nil(t, matches[1].(map[string]any)["result"])
	}

	recorder = web.getHttpResponse("/api/v1/matches/" + itoa(match.Id))
	assert.Equal(t, 200, recorder.Code)
	detail := decodeJson(t, recorder)
	assert.Len(t, detail["changes"], 2)
	assert.NotNil(t, detail["computed"])
	assert.NotNil(t, detail["state"])

	assert.Equal(t, 404, web.getHttpResponse("/api/v1/matches/9999").Code)
	assert.Equal(t, 400, web.getHttpResponse("/api/v1/matches/abc").Code)
	assert.Equal(t, 400, web.getHttpResponse("/api/v1/matches?type=friendly").Code)
}

func itoa(n int) string {
	data, _ := json.Marshal(n)
	return string(data)
}

func TestApiCurrentMatch(t *testing.T) {
	web := setupTestWeb(t)
	response := decodeJson(t, web.getHttpResponse("/api/v1/match"))
	assert.Equal(t, "pre_match", response["state"])
	assert.Equal(t, -1.0, response["phaseIndex"])
	match := response["match"].(map[string]any)
	assert.Equal(t, "Test", match["type"])
	assert.Equal(t, map[string]any{"red": []any{0.0, 0.0, 0.0}, "blue": []any{0.0, 0.0, 0.0}}, match["teams"])
	score := response["score"].(map[string]any)
	assert.Contains(t, score["alliances"], "red")
}

func TestApiRankingsTeamsAlliances(t *testing.T) {
	web := setupTestWeb(t)
	database := web.arena.Database
	assert.NoError(t, database.CreateTeam(&model.Team{Id: 254, Nickname: "The Cheesy Poofs", WpaKey: "secret-key",
		FtaNotes: "private"}))
	ranking := model.NewRanking(scoring.TeamRanking{TeamID: 254, Rank: 1, RP: 4, Played: 2, Wins: 1, Ties: 1,
		Sort: []float64{2, 65, 30, 10, 0.5}})
	assert.NoError(t, database.CreateRanking(&ranking))
	assert.NoError(t, database.CreateAlliance(&model.Alliance{Id: 1, TeamIds: []int{254, 1114}, Lineup: [3]int{254, 1114, 0}}))

	response := decodeJson(t, web.getHttpResponse("/api/v1/rankings"))
	columns := response["columns"].([]any)
	assert.Len(t, columns, 4, "the random tiebreaker is not a column")
	assert.Equal(t, "Ranking score", columns[0].(map[string]any)["name"])
	first := response["rankings"].([]any)[0].(map[string]any)
	assert.Equal(t, 254.0, first["team"])
	assert.Equal(t, []any{2.0, 65.0, 30.0, 10.0}, first["values"])

	recorder := web.getHttpResponse("/api/v1/teams")
	assert.NotContains(t, recorder.Body.String(), "secret-key")
	assert.NotContains(t, recorder.Body.String(), "private")
	teams := decodeJson(t, recorder)["teams"].([]any)
	assert.Equal(t, "The Cheesy Poofs", teams[0].(map[string]any)["nickname"])

	alliances := decodeJson(t, web.getHttpResponse("/api/v1/alliances"))["alliances"].([]any)
	assert.Equal(t, map[string]any{"number": 1.0, "teams": []any{254.0, 1114.0}, "lineup": []any{254.0, 1114.0, 0.0}},
		alliances[0])
}

func TestApiEventsPoll(t *testing.T) {
	web := setupTestWeb(t)
	start := web.arena.Bus.LastSeq()
	web.arena.Bus.Publish("test.one", map[string]any{"n": 1})
	web.arena.Bus.Publish("other.two", nil)
	web.arena.Bus.Publish("test.three", nil)

	response := decodeJson(t, web.getHttpResponse("/api/v1/events?since="+itoa(int(start))+"&types=test"))
	events := response["events"].([]any)
	if assert.Len(t, events, 2) {
		assert.Equal(t, "test.one", events[0].(map[string]any)["type"])
		assert.Equal(t, map[string]any{"n": 1.0}, events[0].(map[string]any)["data"])
		assert.Equal(t, "test.three", events[1].(map[string]any)["type"])
	}
	assert.Equal(t, true, response["complete"])
	assert.Equal(t, false, response["more"])

	response = decodeJson(t, web.getHttpResponse("/api/v1/events?since="+itoa(int(start))+"&limit=1"))
	assert.Len(t, response["events"], 1)
	assert.Equal(t, true, response["more"])

	assert.Equal(t, 400, web.getHttpResponse("/api/v1/events?since=x").Code)
	assert.Equal(t, 400, web.getHttpResponse("/api/v1/events?limit=0").Code)
}

// readSseEvent reads the next event from a Server-Sent Events stream.
func readSseEvent(t *testing.T, reader *bufio.Reader) (id, eventType string, event bus.Event) {
	for {
		line, err := reader.ReadString('\n')
		if !assert.NoError(t, err) {
			return
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			assert.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		case line == "" && eventType != "":
			return
		}
	}
}

func TestApiEventStream(t *testing.T) {
	web := setupTestWeb(t)
	server, _ := web.startTestServer()
	defer server.Close()
	start := web.arena.Bus.LastSeq()
	web.arena.Bus.Publish("test.backlog", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events/stream?types=test", nil)
	request.Header.Set("Last-Event-ID", itoa(int(start)))
	response, err := http.DefaultClient.Do(request)
	if !assert.NoError(t, err) {
		return
	}
	defer response.Body.Close()
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	reader := bufio.NewReader(response.Body)

	// The backlog first, then a live event; events outside the filter are not sent.
	id, eventType, event := readSseEvent(t, reader)
	assert.Equal(t, "test.backlog", eventType)
	assert.Equal(t, itoa(int(event.Seq)), id)
	web.arena.Bus.Publish("other.skipped", nil)
	web.arena.Bus.Publish("test.live", map[string]any{"ok": true})
	_, eventType, event = readSseEvent(t, reader)
	assert.Equal(t, "test.live", eventType)
	assert.Equal(t, map[string]any{"ok": true}, event.Data)
}

func TestApiEventStreamReportsGaps(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.Bus = bus.New(3)
	for i := 0; i < 10; i++ {
		web.arena.Bus.Publish("test.old", i)
	}
	server, _ := web.startTestServer()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events/stream?since=2", nil)
	response, err := http.DefaultClient.Do(request)
	if !assert.NoError(t, err) {
		return
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	id, eventType, event := readSseEvent(t, reader)
	assert.Equal(t, "stream.gap", eventType)
	assert.Equal(t, "", id, "a gap notice is not resumable")
	assert.Equal(t, map[string]any{"after": 2.0, "before": 8.0}, event.Data)
	_, eventType, event = readSseEvent(t, reader)
	assert.Equal(t, "test.old", eventType)
	assert.Equal(t, uint64(8), event.Seq)
}

func TestApiEventSocket(t *testing.T) {
	web := setupTestWeb(t)
	server, wsUrl := web.startTestServer()
	defer server.Close()
	start := web.arena.Bus.LastSeq()
	web.arena.Bus.Publish("match.backlog", nil)

	conn, _, err := websocket.DefaultDialer.Dial(wsUrl+"/api/v1/events/ws?types=match&since="+itoa(int(start)), nil)
	if !assert.NoError(t, err) {
		return
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var event bus.Event
	assert.NoError(t, conn.ReadJSON(&event))
	assert.Equal(t, "match.backlog", event.Type)

	web.arena.Bus.Publish("score.changed", nil)
	web.arena.Bus.Publish("match.live", 7)
	assert.NoError(t, conn.ReadJSON(&event))
	assert.Equal(t, "match.live", event.Type)
	assert.Equal(t, 7.0, event.Data)

	// A match starting on the field reaches subscribers through the same stream.
	for _, station := range []string{"R1", "R2", "R3", "B1", "B2", "B3"} {
		web.arena.AllianceStations[station].Bypass = true
	}
	assert.NoError(t, web.arena.StartMatch())
	web.arena.Update()
	assert.NoError(t, conn.ReadJSON(&event))
	assert.Equal(t, "match.started", event.Type)
	assert.Equal(t, "T", event.Match)
}

func TestApiExports(t *testing.T) {
	web := setupTestWeb(t)
	database := web.arena.Database
	match := model.Match{Type: model.Qualification, TypeOrder: 1, ShortName: "Q1", Red1: 254, Blue1: 1114}
	assert.NoError(t, database.CreateMatch(&match))
	assert.NoError(t, database.SaveGameSnapshot(web.arena.Game))
	matchResult := model.NewMatchResult(web.arena.Game)
	matchResult.MatchId, matchResult.MatchType = match.Id, match.Type
	confidence := 0.8
	for _, change := range []scoring.Change{
		{Alliance: "red", Element: "fuel", Op: scoring.OpSet, Value: 12, Bucket: "auto",
			Source: scoring.Source{Kind: scoring.SourceDevice, ID: "beam-red", Name: "Red beam", Confidence: &confidence}},
		{Alliance: "blue", Robot: 2, Element: "endgame_tower", Op: scoring.OpSet, Value: "level2", Bucket: "endgame",
			Source: scoring.Source{Kind: scoring.SourceScorer, Name: "Blue near side"}},
	} {
		_, err := matchResult.Edit(web.arena.Game, change)
		assert.NoError(t, err)
	}
	assert.NoError(t, database.CreateMatchResult(matchResult))

	readCsv := func(path string) [][]string {
		recorder := web.getHttpResponse(path)
		assert.Equal(t, 200, recorder.Code, path)
		assert.Equal(t, "text/csv; charset=utf-8", recorder.Header().Get("Content-Type"))
		assert.Contains(t, recorder.Header().Get("Content-Disposition"), "attachment")
		rows, err := csv.NewReader(strings.NewReader(recorder.Body.String())).ReadAll()
		assert.NoError(t, err)
		return rows
	}
	column := func(rows [][]string, name string) int {
		for i, header := range rows[0] {
			if header == name {
				return i
			}
		}
		t.Fatalf("no column %q in %v", name, rows[0])
		return -1
	}

	matches := readCsv("/api/v1/exports/matches.csv")
	assert.Len(t, matches, 3) // Header, then one row per alliance.
	assert.Equal(t, "254", matches[1][column(matches, "teams")])
	assert.Equal(t, "12", matches[1][column(matches, "score")])
	assert.Equal(t, "blue", matches[1][column(matches, "winner")], "20 tower points beat 12 fuel")
	assert.Equal(t, "loss", matches[1][column(matches, "result")])

	scores := readCsv("/api/v1/exports/scores.csv?type=qualification")
	assert.Len(t, scores, 3)
	assert.Equal(t, "12", scores[1][column(scores, "points.fuel")])
	assert.Equal(t, "20", scores[2][column(scores, "points.endgame_tower")])
	assert.Equal(t, "12", scores[1][column(scores, "breakdown.auto_fuel_points")])
	assert.Equal(t, "0", scores[1][column(scores, "rp.win")])
	assert.Equal(t, "3", scores[2][column(scores, "rp.win")])

	audit := readCsv("/api/v1/exports/audit.csv")
	assert.Len(t, audit, 3)
	assert.Equal(t, "beam-red", audit[1][column(audit, "source_id")])
	assert.Equal(t, "0.8", audit[1][column(audit, "confidence")])
	assert.Equal(t, "level2", audit[2][column(audit, "value")])
	assert.Equal(t, "2", audit[2][column(audit, "robot")])

	recorder := web.getHttpResponse("/api/v1/exports/audit.json")
	assert.Equal(t, 200, recorder.Code)
	assert.Len(t, decodeJson(t, recorder)["matches"], 1)
	assert.Equal(t, 200, web.getHttpResponse("/api/v1/exports/rankings.csv").Code)
	assert.Equal(t, 200, web.getHttpResponse("/api/v1/exports/scores.json").Code)
	assert.Equal(t, 404, web.getHttpResponse("/api/v1/exports/nope.csv").Code)
	assert.Equal(t, 404, web.getHttpResponse("/api/v1/exports/matches.xlsx").Code)
	assert.Equal(t, 400, web.getHttpResponse("/api/v1/exports/matches.csv?type=friendly").Code)
}

func TestApiConnectionReport(t *testing.T) {
	web := setupTestWeb(t)
	match := &model.Match{Type: model.Qualification, ShortName: "Q9997", Blue2: 9997}
	assert.NoError(t, web.arena.Database.CreateMatch(match))

	logsDir := filepath.Join(".", "static", "logs")
	assert.NoError(t, os.MkdirAll(logsDir, 0755))
	filename := filepath.Join(logsDir, "20261113120000_Qualification_Match_Q9997_9997.csv")
	t.Cleanup(func() { os.Remove(filename) })
	lines := []string{"matchTimeSec,packetType,teamId,allianceStation,dsLinked,radioLinked,rioLinked,robotLinked," +
		"auto,enabled,emergencyStop,autonomousStop,batteryVoltage,missedPacketCount,dsRobotTripTimeMs,rxRate,txRate," +
		"signalNoiseRatio"}
	// The robot drops out between 3 s and 5 s.
	for i := 0; i <= 20; i++ {
		seconds := float64(i) / 2
		linked := seconds < 3 || seconds >= 5
		lines = append(lines, strings.Join([]string{
			strconv.FormatFloat(seconds, 'f', 6, 64), "22", "9997", "B2", "true", "true", strconv.FormatBool(linked),
			strconv.FormatBool(linked), "true", "true", "false", "false", "12.100000", strconv.Itoa(i), "5", "400.0",
			"390.0", "35",
		}, ","))
	}
	assert.NoError(t, os.WriteFile(filename, []byte(strings.Join(lines, "\n")+"\n"), 0644))

	recorder := web.getHttpResponse("/api/v1/diagnostics/connection/" + strconv.Itoa(match.Id) + "/B2")
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	response := decodeJson(t, recorder)
	assert.Equal(t, 9997.0, response["team"])
	plays := response["plays"].([]any)
	if assert.Len(t, plays, 1) {
		report := plays[0].(map[string]any)["report"].(map[string]any)
		assert.Equal(t, "poor", report["verdict"])
		assert.Equal(t, 2.0, report["robotDownSec"])
		assert.Contains(t, report["findings"].([]any)[0], "lost its connection 1 time(s), for 2.0 s")
	}

	// The match log page shows the same report.
	page := web.getHttpResponse("/match_logs/" + strconv.Itoa(match.Id) + "/B2/log")
	assert.Equal(t, 200, page.Code)
	assert.Contains(t, page.Body.String(), "Connection report")
	assert.Contains(t, page.Body.String(), "lost its connection 1 time(s)")
	assert.Contains(t, page.Body.String(), "/static/js/lib/chart-4.6.1.umd.min.js")

	assert.Equal(t, 404, web.getHttpResponse("/api/v1/diagnostics/connection/"+strconv.Itoa(match.Id)+"/R1").Code)
}

func TestTeamConnectionReportRedirect(t *testing.T) {
	web := setupTestWeb(t)
	match := &model.Match{Type: model.Practice, ShortName: "P7", Red3: 9996}
	assert.NoError(t, web.arena.Database.CreateMatch(match))
	logsDir := filepath.Join(".", "static", "logs")
	assert.NoError(t, os.MkdirAll(logsDir, 0755))
	older := filepath.Join(logsDir, "20261113100000_Practice_Match_P6_9996.csv")
	newer := filepath.Join(logsDir, "20261113110000_Practice_Match_P7_9996.csv")
	t.Cleanup(func() {
		os.Remove(older)
		os.Remove(newer)
	})
	assert.NoError(t, os.WriteFile(older, []byte("matchTimeSec\n"), 0644))
	assert.NoError(t, os.WriteFile(newer, []byte("matchTimeSec\n"), 0644))

	recorder := web.getHttpResponse("/reports/connection/team/9996")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "/match_logs/"+strconv.Itoa(match.Id)+"/R3/log", recorder.Header().Get("Location"))
	assert.Equal(t, 404, web.getHttpResponse("/reports/connection/team/9995").Code)
	assert.Equal(t, 400, web.getHttpResponse("/reports/connection/team/abc").Code)
}
