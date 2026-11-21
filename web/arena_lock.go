// How the web layer shares the arena with the arena loop.
//
// Every handler runs with the arena's lock held (see field/arena_lock.go): what a page reads belongs to one moment,
// and what a button changes is not interleaved with a pass of the loop or with another button. The loop needs the
// lock a hundred times a second, so a handler never waits on a browser while it holds it:
//
//   - The request's body is read before the lock is taken.
//   - What the handler answers is kept in memory and sent after the lock is released.
//   - A handler that takes the connection over, as a websocket does, lets go of the lock at that moment. From then
//     on it takes the lock around each thing it does with the arena, with withArena.
//   - A handler that streams its answer calls streamResponse once it has read what it needs from the arena.
//   - A handler that has to wait on something else (The Blue Alliance, a password hash, a schedule being generated)
//     lets go of the lock around the wait with the arena's Unlocked, and checks afterwards whatever it relied on.
//
// So the functions of this package are called with the lock held, and say so where they are not: the part of a
// websocket handler after the upgrade, goroutines, and the handlers registered with HandleUnlocked.

package web

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/partner"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"io"
	"net"
	"net/http"
	"strings"
)

// maxBufferedRequestBytes is the largest request body a handler that runs under the arena's lock is given; it is
// what net/http itself allows a form to be. The one larger upload, a database to restore, is read without the lock.
const maxBufferedRequestBytes = 10 << 20

// lockedResponse is the http.ResponseWriter of a handler that holds the arena's lock. It keeps the answer until the
// lock has been released.
type lockedResponse struct {
	web    *Web
	writer http.ResponseWriter
	header http.Header
	status int // What the handler gave WriteHeader, or zero if it never called it.
	body   bytes.Buffer
	// Set once the lock has been released: when the handler took the connection over or started to stream, and
	// otherwise when it returned.
	released bool
	// Set once there is nothing left for the wrapper to send when the handler returns: the connection was taken
	// over, or the handler writes to the client itself.
	handedOver bool
}

func (response *lockedResponse) Header() http.Header {
	return response.header
}

func (response *lockedResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}

func (response *lockedResponse) Write(data []byte) (int, error) {
	return response.body.Write(data)
}

// Hijack hands the connection to the handler, as a websocket upgrade asks, and releases the arena's lock: from here
// on the handler talks to its client for as long as the client stays.
func (response *lockedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := response.writer.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("the connection cannot be taken over")
	}
	conn, buffer, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	response.handedOver = true
	response.release()
	return conn, buffer, nil
}

func (response *lockedResponse) release() {
	if !response.released {
		response.released = true
		response.web.arena.Unlock()
	}
}

// send gives the client what the handler answered, in the way the handler did: the status is written only if the
// handler wrote one, so that an answer without one still gets its content type worked out from its first bytes.
func (response *lockedResponse) send() {
	header := response.writer.Header()
	for name, values := range response.header {
		header[name] = values
	}
	if response.status != 0 {
		response.writer.WriteHeader(response.status)
	}
	if response.body.Len() > 0 {
		response.writer.Write(response.body.Bytes())
	}
}

// answered reports whether the handler has answered anything yet.
func (response *lockedResponse) answered() bool {
	return response.status != 0 || response.body.Len() > 0 || len(response.header) > 0
}

// lockedHandler wraps a handler so that it runs with the arena's lock held, as described at the top of this file.
func (web *Web) lockedHandler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !bufferRequestBody(w, r) {
			return
		}
		response := &lockedResponse{web: web, writer: w, header: make(http.Header)}
		func() {
			web.arena.LockAs(r.Method + " " + r.URL.Path)
			// Deferred, so that a handler that panics does not leave the arena locked: the server survives a
			// panicking handler, and the field must too.
			defer response.release()
			handler(response, r)
		}()
		if !response.handedOver {
			response.send()
		}
	}
}

// bufferRequestBody reads the whole body of a request into memory, so that the handler does not wait for a slow
// client to send it. It answers the request itself, and returns false, if the body cannot be read or is too large.
func bufferRequestBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBufferedRequestBytes))
	if err != nil {
		status, message := http.StatusBadRequest, "the request could not be read"
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status, message = http.StatusRequestEntityTooLarge, "the request is too large"
		}
		// The API answers in JSON, errors included; a page answers in words.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJsonError(w, status, message)
		} else {
			http.Error(w, strings.ToUpper(message[:1])+message[1:]+".", status)
		}
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return true
}

// streamResponse is called by a handler whose answer is long or never ends, once it has read what it needs from the
// arena. The arena's lock is released for the rest of the request, what the handler has answered so far is sent,
// and the writer returned writes straight to the client. A handler that is not holding the lock gets its own
// writer back.
func streamResponse(w http.ResponseWriter) http.ResponseWriter {
	response, ok := w.(*lockedResponse)
	if !ok || response.released {
		return w
	}
	response.handedOver = true
	response.release()
	response.send()
	return response.writer
}

// withArena runs fn with the arena's lock held, for the part of a websocket handler that follows the upgrade: there
// the handler waits for its client, without the lock, and takes it for each message. What fn writes to the websocket
// is sent once the lock has been released, so a client that is slow to read cannot keep it held.
func (web *Web) withArena(ws *websocket.Websocket, fn func()) {
	ws.Hold()
	defer ws.Release()
	web.arena.LockAs("a websocket command")
	defer web.arena.Unlock()
	fn()
}

// lockedPart runs fn with the arena's lock held and sends what it answered afterwards, for a handler registered
// with HandleUnlocked. It returns what fn returned: whether the handler should carry on.
func (web *Web) lockedPart(w http.ResponseWriter, fn func(w http.ResponseWriter) bool) bool {
	response := &lockedResponse{web: web, writer: w, header: make(http.Header)}
	var proceed bool
	func() {
		web.arena.Lock()
		defer response.release()
		proceed = fn(response)
	}()
	if !response.handedOver && response.answered() {
		response.send()
	}
	return proceed
}

// backupDatabase writes a backup of the database beside it, without the arena's lock: a large database takes a
// while to copy.
func (web *Web) backupDatabase(reason string) (err error) {
	database, eventName := web.arena.Database, web.arena.EventSettings.Name
	web.arena.Unlocked(func() {
		err = database.Backup(eventName, reason)
	})
	return
}

// withTba runs fn, which talks to The Blue Alliance, without the arena's lock. fn is given the client and the
// database as they were when it was called, and must not touch the arena.
func (web *Web) withTba(fn func(client *partner.TbaClient, database *model.Database) error) (err error) {
	client, database := web.arena.TbaClient, web.arena.Database
	web.arena.Unlocked(func() {
		err = fn(client, database)
	})
	return
}
