// The event stream: everything published on the internal event bus, offered to outside programs as a poll, as
// Server-Sent Events, and over a WebSocket.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/gorilla/websocket"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	eventStreamBuffer    = 256
	eventStreamKeepAlive = 15 * time.Second
	eventPollLimit       = 1000
)

// eventFilter reads the "types" query parameter: a comma-separated list of event type prefixes.
func eventFilter(r *http.Request) []string {
	var prefixes []string
	for _, prefix := range strings.Split(r.URL.Query().Get("types"), ",") {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// eventStart reads where a client wants to resume from: the Last-Event-ID header that browsers send when an
// EventSource reconnects, or the "since" query parameter. Without either, the stream starts with new events only.
func eventStart(r *http.Request, current uint64) (uint64, error) {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("since")
	}
	if value == "" {
		return current, nil
	}
	seq, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an event sequence number", value)
	}
	return seq, nil
}

// apiEventsHandler returns the remembered events after a sequence number, for clients that poll.
func (web *Web) apiEventsHandler(w http.ResponseWriter, r *http.Request) {
	since, err := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	if err != nil && r.URL.Query().Get("since") != "" {
		writeJsonError(w, http.StatusBadRequest, "since must be an event sequence number")
		return
	}
	limit := eventPollLimit
	if value := r.URL.Query().Get("limit"); value != "" {
		if limit, err = strconv.Atoi(value); err != nil || limit < 1 || limit > eventPollLimit {
			writeJsonError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", eventPollLimit))
			return
		}
	}
	events, complete := web.arena.Bus.Since(since, eventFilter(r))
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	if events == nil {
		events = []bus.Event{}
	}
	writeJson(w, http.StatusOK, map[string]any{
		"events": events, "complete": complete, "more": more, "lastSeq": web.arena.Bus.LastSeq(),
	})
}

// eventFeed delivers events in order from a starting point: first those already remembered, then live ones, with a
// "gap" notice whenever some could not be delivered (because they were forgotten, or the client fell behind).
type eventFeed struct {
	bus      *bus.Bus
	prefixes []string
	sub      *bus.Subscription
	lastSeq  uint64
}

// gapEvent tells a client that events between two sequence numbers were lost, so it should reload its state.
func gapEvent(after, before uint64) bus.Event {
	return bus.Event{
		Type: "stream.gap", Time: time.Now().UTC(),
		Data: map[string]any{"after": after, "before": before},
	}
}

func newEventFeed(b *bus.Bus, prefixes []string) *eventFeed {
	// Subscribe before reading the backlog, so nothing published in between is missed; duplicates are dropped by
	// sequence number.
	return &eventFeed{bus: b, prefixes: prefixes, sub: b.Subscribe(prefixes, eventStreamBuffer)}
}

// backlog returns the remembered events after since.
func (feed *eventFeed) backlog(since uint64) []bus.Event {
	feed.lastSeq = since
	events, complete := feed.bus.Since(since, feed.prefixes)
	var out []bus.Event
	if !complete {
		first := feed.bus.LastSeq() + 1
		if len(events) > 0 {
			first = events[0].Seq
		}
		out = append(out, gapEvent(since, first))
	}
	for _, event := range events {
		out = append(out, event)
		feed.lastSeq = event.Seq
	}
	return out
}

// next turns a live event into what should be sent: nothing if it was already sent from the backlog, a gap notice
// first if the subscription dropped some.
func (feed *eventFeed) next(event bus.Event) []bus.Event {
	if event.Seq <= feed.lastSeq {
		return nil
	}
	var out []bus.Event
	if dropped := feed.sub.Dropped(); dropped > 0 {
		// Some events never reached this subscription because the client was slow. Fill in from the bus's memory if
		// it still has them, otherwise report the gap.
		missed, complete := feed.bus.Since(feed.lastSeq, feed.prefixes)
		if !complete {
			out = append(out, gapEvent(feed.lastSeq, event.Seq))
		}
		for _, m := range missed {
			if m.Seq < event.Seq && m.Seq > feed.lastSeq {
				out = append(out, m)
			}
		}
		feed.sub.ResetDropped()
	}
	out = append(out, event)
	feed.lastSeq = event.Seq
	return out
}

func (feed *eventFeed) close() {
	feed.sub.Close()
}

// apiEventStreamHandler streams events as Server-Sent Events. Browsers can use it with EventSource, which resumes
// from the last event it saw after a reconnect.
func (web *Web) apiEventStreamHandler(w http.ResponseWriter, r *http.Request) {
	since, err := eventStart(r, web.arena.Bus.LastSeq())
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	feed := newEventFeed(web.arena.Bus, eventFilter(r))
	defer feed.close()
	write := func(event bus.Event) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if event.Seq > 0 {
			if _, err = fmt.Fprintf(w, "id: %d\n", event.Seq); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
		return err
	}
	// Tell the browser how long to wait before reconnecting, then send the backlog.
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	for _, event := range feed.backlog(since) {
		if write(event) != nil {
			return
		}
	}
	if controller.Flush() != nil {
		return
	}

	keepAlive := time.NewTicker(eventStreamKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case event, ok := <-feed.sub.C:
			if !ok {
				return
			}
			for _, out := range feed.next(event) {
				if write(out) != nil {
					return
				}
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}

var eventSocketUpgrader = websocket.Upgrader{
	ReadBufferSize: 1024, WriteBufferSize: 4096,
	// The event stream is read-only and public, like the read API, so any page may open it.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// apiEventSocketHandler streams events over a WebSocket: one JSON event per text message.
func (web *Web) apiEventSocketHandler(w http.ResponseWriter, r *http.Request) {
	since, err := eventStart(r, web.arena.Bus.LastSeq())
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	conn, err := eventSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	feed := newEventFeed(web.arena.Bus, eventFilter(r))
	defer feed.close()

	// The client sends nothing; reading is only how a close from its side is noticed.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	write := func(event bus.Event) error {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(event)
	}
	for _, event := range feed.backlog(since) {
		if write(event) != nil {
			return
		}
	}
	keepAlive := time.NewTicker(eventStreamKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-closed:
			return
		case <-keepAlive.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		case event, ok := <-feed.sub.C:
			if !ok {
				return
			}
			for _, out := range feed.next(event) {
				if write(out) != nil {
					return
				}
			}
		}
	}
}
