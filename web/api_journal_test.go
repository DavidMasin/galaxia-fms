package web

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestApiJournal(t *testing.T) {
	web := setupTestWeb(t)
	today := time.Now().Format("2006-01-02")

	// Before the journal is started there is nothing, which is an empty list rather than an error.
	listing := decodeJson(t, web.getHttpResponse("/api/v1/journal"))
	assert.Empty(t, listing["days"])
	assert.Equal(t, float64(30), listing["keepDays"])
	assert.Equal(t, 404, web.getHttpResponse("/api/v1/journal/"+today).Code)

	require.NoError(t, web.arena.Journal.Start())
	defer web.arena.Journal.Stop()
	web.arena.Bus.Publish("match.started", map[string]any{"match": "Q1"})

	// A day's file has every event so far, one JSON object per line, without waiting for the journal to stop. Other
	// events of the arena's own may be around it.
	var body string
	require.Eventually(t, func() bool {
		recorder := web.getHttpResponse("/api/v1/journal/" + today)
		body = recorder.Body.String()
		return recorder.Code == 200 && strings.Contains(body, `"match.started"`)
	}, 5*time.Second, 10*time.Millisecond)
	recorder := web.getHttpResponse("/api/v1/journal/" + today)
	assert.Equal(t, "application/x-ndjson", recorder.Header().Get("Content-Type"))
	assert.Equal(
		t, `attachment; filename="galaxia-events-`+today+`.jsonl"`, recorder.Header().Get("Content-Disposition"),
	)
	found := false
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), line)
		assert.NotEmpty(t, event["type"])
		if event["type"] == "match.started" {
			found = true
			assert.Equal(t, map[string]any{"match": "Q1"}, event["data"])
			assert.NotZero(t, event["seq"])
		}
	}
	assert.True(t, found)

	listing = decodeJson(t, web.getHttpResponse("/api/v1/journal"))
	days := listing["days"].([]any)
	if assert.Len(t, days, 1) {
		assert.Equal(t, today, days[0].(map[string]any)["day"])
		assert.Greater(t, days[0].(map[string]any)["bytes"], float64(0))
	}

	// Only a day can be asked for.
	for _, day := range []string{"today", "2026-11-21.jsonl", "..%2Fevent.db", "%2e%2e"} {
		assert.Equal(t, 400, web.getHttpResponse("/api/v1/journal/"+day).Code, day)
	}
	assert.Equal(t, 404, web.getHttpResponse("/api/v1/journal/2001-01-01").Code)
}
