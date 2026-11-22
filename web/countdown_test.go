package web

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// A display is told the moment the match will start, and that the start was called off. One that connects in the
// middle of a countdown is told the same moment as the rest.
func TestCountdownReachesDisplaysAndTheApi(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.CountdownMode = model.CountdownQuick
	description := loadApiDescription(t)
	server, wsUrl := web.startTestServer()
	defer server.Close()
	dial := func() *websocket.Websocket {
		conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/displays/audience/websocket?displayId=1", nil)
		require.Nil(t, err)
		t.Cleanup(func() { conn.Close() })
		return websocket.NewTestWebsocket(conn)
	}
	ws := dial()
	assert.Equal(t, "idle", readWebsocketUntil(t, ws, "countdown").(map[string]any)["state"])

	setAllStationsBypassed(web)
	before := time.Now()
	web.arena.Do(func() {
		require.Nil(t, web.arena.StartMatch())
	})
	armed := readWebsocketUntil(t, ws, "countdown").(map[string]any)
	assert.Equal(t, "armed", armed["state"])
	assert.Equal(t, 1.0, armed["beats"])
	assert.Equal(t, false, armed["intro"])
	assert.Equal(t, true, armed["sound"])
	goAt := time.UnixMilli(int64(armed["goAt"].(float64)))
	assert.WithinDuration(t, before.Add(1800*time.Millisecond), goAt, 500*time.Millisecond)

	// A second display, connecting now, counts down to the same moment.
	late := dial()
	assert.Equal(t, armed, readWebsocketUntil(t, late, "countdown"))

	// The API says so too.
	recorder := web.getHttpResponse("/api/v1/match")
	description.check(t, "GET", "/api/v1/match", recorder)
	current := decodeJson(t, recorder)
	assert.Equal(t, "countdown", current["state"])
	assert.Equal(t, map[string]any{"t0": armed["goAt"], "beats": 1.0}, current["countdown"])
	assert.Equal(t, "countdown", decodeJson(t, web.getHttpResponse("/api/v1/health"))["matchState"])

	// Calling the start off reaches both.
	web.arena.Do(func() {
		require.Nil(t, web.arena.AbortMatch())
	})
	assert.Equal(t, "cancelled", readWebsocketUntil(t, ws, "countdown").(map[string]any)["state"])
	assert.Equal(t, "cancelled", readWebsocketUntil(t, late, "countdown").(map[string]any)["state"])
	current = decodeJson(t, web.getHttpResponse("/api/v1/match"))
	assert.Equal(t, "pre_match", current["state"])
	assert.NotContains(t, current, "countdown")
	assert.Equal(t, "idle", readWebsocketUntil(t, dial(), "countdown").(map[string]any)["state"])
}
