package bus

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestPublishAndSubscribe(t *testing.T) {
	b := New(10)
	fixed := time.Date(2026, 11, 12, 17, 0, 0, 0, time.FixedZone("local", 2*3600))
	b.now = func() time.Time { return fixed }
	b.SetContext(func() (string, string) { return "Q12", "" })

	all := b.Subscribe(nil, 10)
	matchOnly := b.Subscribe([]string{"match"}, 10)
	exact := b.Subscribe([]string{"score.changed", "device."}, 10)

	first := b.Publish("match.started", map[string]any{"n": 1})
	b.Publish("score.changed", nil)
	b.Publish("score.totals", nil)
	b.Publish("device.input", nil)
	b.Publish("matchmaker.ready", nil) // Shares a prefix with "match" as text, but is not beneath it.

	assert.Equal(t, uint64(1), first.Seq)
	assert.Equal(t, "Q12", first.Match)
	assert.Equal(t, time.UTC, first.Time.Location())
	assert.True(t, first.Time.Equal(fixed))
	assert.Equal(t, uint64(5), b.LastSeq())

	types := func(sub *Subscription) []string {
		var got []string
		for {
			select {
			case event := <-sub.C:
				got = append(got, event.Type)
			default:
				return got
			}
		}
	}
	assert.Equal(
		t, []string{"match.started", "score.changed", "score.totals", "device.input", "matchmaker.ready"}, types(all),
	)
	assert.Equal(t, []string{"match.started"}, types(matchOnly))
	assert.Equal(t, []string{"score.changed", "device.input"}, types(exact))

	// A closed subscription receives nothing more and its channel is closed.
	matchOnly.Close()
	matchOnly.Close() // Closing twice is harmless.
	b.Publish("match.ended", nil)
	_, open := <-matchOnly.C
	assert.False(t, open)
	assert.Equal(t, []string{"match.ended"}, types(all))
}

func TestSlowSubscriberDropsWithoutBlocking(t *testing.T) {
	b := New(100)
	slow := b.Subscribe(nil, 2)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			b.Publish("tick", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber")
	}
	assert.Equal(t, uint64(48), slow.Dropped())
	assert.Equal(t, uint64(1), (<-slow.C).Seq)
	assert.Equal(t, uint64(2), (<-slow.C).Seq)

	// The subscriber sees the gap from the sequence numbers and catches up from the replay ring.
	missed, complete := b.Since(2, nil)
	assert.True(t, complete)
	assert.Len(t, missed, 48)
	assert.Equal(t, uint64(3), missed[0].Seq)
}

func TestSinceReportsGaps(t *testing.T) {
	b := New(3)
	events, complete := b.Since(0, nil)
	assert.Empty(t, events)
	assert.True(t, complete)

	for i := 0; i < 5; i++ {
		b.Publish("a.b", i)
	}
	b.Publish("c.d", nil)

	// The ring holds events 4, 5 and 6.
	events, complete = b.Since(3, nil)
	assert.True(t, complete)
	assert.Len(t, events, 3)
	events, complete = b.Since(1, nil)
	assert.False(t, complete, "events 2 and 3 have been forgotten")
	assert.Len(t, events, 3)
	events, complete = b.Since(4, []string{"c"})
	assert.True(t, complete)
	if assert.Len(t, events, 1) {
		assert.Equal(t, "c.d", events[0].Type)
	}
	events, complete = b.Since(6, nil)
	assert.True(t, complete)
	assert.Empty(t, events)
}
