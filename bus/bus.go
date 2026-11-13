// Package bus is the internal event bus. Every state change in the field management system is published here, and
// displays, device outputs, webhooks, logs and recorders learn about the world only by subscribing.
//
// Publishing never blocks: the arena loop must not wait for a slow consumer. Each subscriber has a bounded queue;
// when it is full the event is dropped for that subscriber and counted. Events carry a sequence number, so a
// subscriber can see the gap and fetch what it missed from the replay ring with Since.
package bus

import (
	"strings"
	"sync"
	"time"
)

// Event is one thing that happened.
type Event struct {
	Seq   uint64    `json:"seq"`
	Time  time.Time `json:"time"`
	Type  string    `json:"type"`            // Dotted, most general first: "match.phase_changed".
	Match string    `json:"match,omitempty"` // Short name of the match in progress, if any.
	Run   string    `json:"run,omitempty"`   // Id of the practice run in progress, if any.
	Data  any       `json:"data,omitempty"`
}

// Bus distributes events to subscribers and keeps the most recent ones for replay.
type Bus struct {
	mu      sync.Mutex
	seq     uint64
	ring    []Event
	next    int // Index in ring where the next event is written.
	full    bool
	subs    map[*Subscription]struct{}
	context func() (match, run string)
	now     func() time.Time
}

// New creates a bus that remembers the last ringSize events.
func New(ringSize int) *Bus {
	if ringSize < 1 {
		ringSize = 1
	}
	return &Bus{ring: make([]Event, ringSize), subs: make(map[*Subscription]struct{}), now: time.Now}
}

// SetContext sets the function that supplies the current match and practice run for each published event.
func (b *Bus) SetContext(context func() (match, run string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.context = context
}

// Publish records an event and hands it to every interested subscriber without blocking.
func (b *Bus) Publish(eventType string, data any) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	event := Event{Seq: b.seq, Time: b.now().UTC(), Type: eventType, Data: data}
	if b.context != nil {
		event.Match, event.Run = b.context()
	}
	b.ring[b.next] = event
	b.next = (b.next + 1) % len(b.ring)
	if b.next == 0 {
		b.full = true
	}
	for sub := range b.subs {
		if !matches(sub.prefixes, eventType) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			sub.dropped++
		}
	}
	return event
}

// Subscribe returns a subscription to events whose type equals or starts with one of the prefixes. No prefixes
// means every event. buffer is how many undelivered events the subscription holds before dropping.
func (b *Bus) Subscribe(prefixes []string, buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	sub := &Subscription{bus: b, prefixes: prefixes, ch: make(chan Event, buffer)}
	sub.C = sub.ch
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[sub] = struct{}{}
	return sub
}

// Since returns the remembered events with a sequence number greater than seq that match the prefixes, oldest
// first. complete is false if events after seq have already been forgotten, meaning the caller has a gap it
// cannot fill from the bus.
func (b *Bus) Since(seq uint64, prefixes []string) (events []Event, complete bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := b.next
	start := 0
	if b.full {
		count = len(b.ring)
		start = b.next
	}
	complete = true
	for i := 0; i < count; i++ {
		event := b.ring[(start+i)%len(b.ring)]
		if i == 0 && event.Seq > seq+1 {
			complete = false
		}
		if event.Seq > seq && matches(prefixes, event.Type) {
			events = append(events, event)
		}
	}
	if count == 0 && seq < b.seq {
		complete = false
	}
	return events, complete
}

// LastSeq returns the sequence number of the most recent event.
func (b *Bus) LastSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Subscription is one subscriber's queue of events.
type Subscription struct {
	C        <-chan Event
	bus      *Bus
	prefixes []string
	ch       chan Event
	dropped  uint64
	closed   bool
}

// Dropped returns how many events were discarded because this subscription's queue was full.
func (s *Subscription) Dropped() uint64 {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.dropped
}

// ResetDropped sets the dropped count back to zero, once the subscriber has dealt with the gap.
func (s *Subscription) ResetDropped() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.dropped = 0
}

// Close stops delivery and closes the channel.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if !s.closed {
		s.closed = true
		delete(s.bus.subs, s)
		close(s.ch)
	}
}

// matches reports whether an event type is selected by a list of prefixes. A prefix selects the type it names and
// everything beneath it: "match" selects "match.started", and so does "match.".
func matches(prefixes []string, eventType string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(prefix, ".")
		if prefix == "" || eventType == prefix || strings.HasPrefix(eventType, prefix+".") {
			return true
		}
	}
	return false
}
