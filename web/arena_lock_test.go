package web

import (
	"bytes"
	"fmt"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// arenaIsFree reports whether the arena's lock can be had within a moment.
func arenaIsFree(web *Web) bool {
	free := make(chan struct{})
	go web.arena.Do(func() {
		close(free)
	})
	select {
	case <-free:
		return true
	case <-time.After(100 * time.Millisecond):
		// The goroutine gets the lock once whoever holds it lets go, and gives it straight back.
		return false
	}
}

func TestLockedHandlerHoldsTheArenaAndAnswersAfterwards(t *testing.T) {
	web := setupTestWeb(t)
	handler := web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {
		assert.False(t, arenaIsFree(web), "the handler runs with the arena's lock held")
		body, err := io.ReadAll(r.Body)
		assert.Nil(t, err)
		w.Header().Set("X-Test", "kept")
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprintf(w, "read %s", body)
	})

	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("POST", "/anything", strings.NewReader("the body")))
	assert.True(t, arenaIsFree(web))
	assert.Equal(t, http.StatusTeapot, recorder.Code)
	assert.Equal(t, "kept", recorder.Header().Get("X-Test"))
	assert.Equal(t, "read the body", recorder.Body.String())
}

// An answer without a status is sent without one, so that its content type is still worked out from what it says.
func TestLockedHandlerLeavesTheStatusToTheServer(t *testing.T) {
	web := setupTestWeb(t)
	handler := web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>a page</body></html>")
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/anything", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "text/html; charset=utf-8", recorder.Header().Get("Content-Type"))

	// A handler that answers nothing gets the server's empty 200.
	handler = web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {})
	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/anything", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 0, recorder.Body.Len())
}

// The server survives a handler that panics; the arena must come out of it unlocked.
func TestLockedHandlerReleasesTheArenaWhenTheHandlerPanics(t *testing.T) {
	web := setupTestWeb(t)
	handler := web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {
		panic("a handler gone wrong")
	})
	assert.PanicsWithValue(t, "a handler gone wrong", func() {
		handler(httptest.NewRecorder(), httptest.NewRequest("GET", "/anything", nil))
	})
	assert.True(t, arenaIsFree(web))
}

func TestLockedHandlerRefusesABodyTooLargeToKeep(t *testing.T) {
	web := setupTestWeb(t)
	called := false
	handler := web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	recorder := httptest.NewRecorder()
	body := bytes.NewReader(make([]byte, maxBufferedRequestBytes+1))
	handler(recorder, httptest.NewRequest("POST", "/anything", body))
	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Equal(t, "The request is too large.", strings.TrimSpace(recorder.Body.String()))
	assert.False(t, called)
	assert.True(t, arenaIsFree(web))

	// The API says so in JSON, like its other errors.
	recorder = httptest.NewRecorder()
	body = bytes.NewReader(make([]byte, maxBufferedRequestBytes+1))
	handler(recorder, httptest.NewRequest("POST", "/api/v1/anything", body))
	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.JSONEq(t, `{"error":"the request is too large"}`, recorder.Body.String())
	assert.False(t, called)

	// A body of exactly the limit is the handler's to read.
	recorder = httptest.NewRecorder()
	body = bytes.NewReader(make([]byte, maxBufferedRequestBytes))
	handler(recorder, httptest.NewRequest("POST", "/anything", body))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, called)
}

// A handler that streams lets go of the arena for the rest of the request, and what it had answered goes out then.
func TestStreamResponseReleasesTheArena(t *testing.T) {
	web := setupTestWeb(t)
	handler := web.lockedHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Before", "kept")
		assert.False(t, arenaIsFree(web))
		w = streamResponse(w)
		assert.True(t, arenaIsFree(web), "the arena is free while the handler streams")
		_, isRecorder := w.(*httptest.ResponseRecorder)
		assert.True(t, isRecorder, "the handler writes straight to the client from here")
		// Asking twice is harmless.
		assert.Equal(t, w, streamResponse(w))
		fmt.Fprint(w, "streamed")
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("GET", "/anything", nil))
	assert.Equal(t, "kept", recorder.Header().Get("X-Before"))
	assert.Equal(t, "streamed", recorder.Body.String(), "nothing is sent a second time when the handler returns")
	assert.True(t, arenaIsFree(web))
}

// A websocket handler holds the arena only until it has taken the connection over; while its client stays, the arena
// loop and every other handler must get their turn.
func TestWebsocketHandlerReleasesTheArenaOnUpgrade(t *testing.T) {
	web := setupTestWeb(t)
	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, response, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/api/arena/websocket", nil)
	require.Nil(t, err)
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	assert.True(t, arenaIsFree(web))
	web.arena.Update()

	// A request that is not an upgrade is answered like any other, with the arena given back.
	plain, err := http.Get(server.URL + "/api/arena/websocket")
	require.Nil(t, err)
	plain.Body.Close()
	assert.Equal(t, http.StatusBadRequest, plain.StatusCode)
	assert.True(t, arenaIsFree(web))
}

func TestLockedPart(t *testing.T) {
	web := setupTestWeb(t)
	recorder := httptest.NewRecorder()
	proceed := web.lockedPart(recorder, func(w http.ResponseWriter) bool {
		assert.False(t, arenaIsFree(web))
		return true
	})
	assert.True(t, proceed)
	assert.True(t, arenaIsFree(web))
	assert.Equal(t, 0, recorder.Body.Len())

	proceed = web.lockedPart(recorder, func(w http.ResponseWriter) bool {
		http.Error(w, "not you", http.StatusForbidden)
		return false
	})
	assert.False(t, proceed)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "not you\n", recorder.Body.String())
}

// Committing a match and loading the next one let go of the arena while they wait, so a second one asked for in
// that time is refused rather than run in the middle of the first.
func TestMatchFlowIsNotEnteredTwice(t *testing.T) {
	web := setupTestWeb(t)
	require.Nil(t, web.beginMatchFlow())
	err := web.commitPostAndLoadNextMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "a match is being committed or loaded right now")
	}
	err = web.resetAndLoadNextMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "a match is being committed or loaded right now")
	}
	web.endMatchFlow()
	assert.Nil(t, web.resetAndLoadNextMatch())
	assert.False(t, web.matchFlowBusy, "the flow is left when it is done")
}
