package bus

import (
	"bufio"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// readJournal returns the events in one journal file.
func readJournal(t *testing.T, path string) []Event {
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event), scanner.Text())
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	return events
}

func eventTypes(events []Event) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

func seqs(events []Event) []uint64 {
	numbers := make([]uint64, len(events))
	for i, event := range events {
		numbers[i] = event.Seq
	}
	return numbers
}

// waitForLines flushes the journal until a day's file has the wanted number of events.
func waitForLines(t *testing.T, journal *Journal, path string, count int) []Event {
	deadline := time.Now().Add(5 * time.Second)
	for {
		journal.Flush()
		if _, err := os.Stat(path); err == nil {
			if events := readJournal(t, path); len(events) >= count {
				return events
			}
		}
		if time.Now().After(deadline) {
			require.Failf(t, "the journal was not written", "want %d events in %s", count, path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestJournalWritesEveryEvent(t *testing.T) {
	b := New(100)
	b.SetContext(func() (string, string) { return "Q1", "" })
	dir := filepath.Join(t.TempDir(), "journal")
	journal := NewJournal(b, dir, 0)

	// Nothing exists until the journal starts; what happened before is written when it does.
	b.Publish("system.started", nil)
	b.Publish("match.loaded", map[string]any{"id": 7})
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
	days, err := journal.Days()
	assert.NoError(t, err)
	assert.Empty(t, days)

	require.NoError(t, journal.Start())
	require.NoError(t, journal.Start(), "starting twice is harmless")
	b.Publish("match.started", map[string]any{"time": "now"})
	today := time.Now().Format("2006-01-02")
	path, err := journal.Path(today)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, today+".jsonl"), path)

	// A reader sees events without waiting for the journal to stop.
	events := waitForLines(t, journal, path, 3)
	assert.Equal(t, []string{"system.started", "match.loaded", "match.started"}, eventTypes(events))
	assert.Equal(t, []uint64{1, 2, 3}, seqs(events))
	assert.Equal(t, "Q1", events[2].Match)
	assert.Equal(t, map[string]any{"time": "now"}, events[2].Data)

	// Stopping writes what is waiting, and starting again carries on in the same file without repeating anything.
	b.Publish("match.ended", nil)
	journal.Stop()
	journal.Stop()
	assert.Equal(t, []uint64{1, 2, 3, 4}, seqs(readJournal(t, path)))
	b.Publish("match.committed", nil)
	require.NoError(t, journal.Start())
	journal.Stop()
	assert.Equal(t, []uint64{1, 2, 3, 4, 5}, seqs(readJournal(t, path)))

	days, err = journal.Days()
	assert.NoError(t, err)
	if assert.Len(t, days, 1) {
		assert.Equal(t, today, days[0].Day)
		assert.Greater(t, days[0].Bytes, int64(100))
	}
}

// testClock is a clock a test moves by hand while the journal's goroutine reads it.
type testClock struct {
	mu   sync.Mutex
	time time.Time
}

func (clock *testClock) now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.time
}

func (clock *testClock) advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.time = clock.time.Add(duration)
}

func TestJournalHasAFilePerDay(t *testing.T) {
	b := New(100)
	clock := &testClock{time: time.Date(2026, 11, 20, 23, 59, 58, 0, time.Local)}
	b.now = clock.now
	dir := t.TempDir()
	journal := NewJournal(b, dir, 0)
	journal.now = clock.now
	require.NoError(t, journal.Start())

	b.Publish("match.started", nil)
	clock.advance(5 * time.Second)
	b.Publish("match.phase_changed", nil)
	b.Publish("match.ended", nil)
	journal.Stop()

	assert.Equal(t, []string{"match.started"}, eventTypes(readJournal(t, filepath.Join(dir, "2026-11-20.jsonl"))))
	assert.Equal(
		t, []string{"match.phase_changed", "match.ended"},
		eventTypes(readJournal(t, filepath.Join(dir, "2026-11-21.jsonl"))),
	)
	days, _ := journal.Days()
	assert.Equal(t, []string{"2026-11-21", "2026-11-20"}, []string{days[0].Day, days[1].Day})
}

func TestJournalKeepsOnlyRecentDays(t *testing.T) {
	b := New(100)
	dir := t.TempDir()
	for _, name := range []string{
		"2026-11-18.jsonl", "2026-11-19.jsonl", "2026-11-20.jsonl", "2026-11-21.jsonl", "notes.txt",
		"2026-11-01.jsonl.bak",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0644))
	}
	journal := NewJournal(b, dir, 3)
	journal.now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local) }
	require.NoError(t, journal.Start())
	journal.Stop()

	// Three days are kept, counting today, and nothing that is not a journal file is touched.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.Equal(
		t, []string{"2026-11-01.jsonl.bak", "2026-11-19.jsonl", "2026-11-20.jsonl", "2026-11-21.jsonl", "notes.txt"},
		names,
	)
	days, _ := journal.Days()
	assert.Len(t, days, 3)
}

func TestJournalSaysWhatWasLost(t *testing.T) {
	// A bus that remembers only four events, and a journal driven by hand, as if it had fallen behind.
	b := New(4)
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	b.now = func() time.Time { return clock }
	dir := t.TempDir()
	journal := NewJournal(b, dir, 0)
	journal.now = func() time.Time { return clock }
	path := filepath.Join(dir, "2026-11-21.jsonl")
	// The journal was never started, so nothing else closes its file.
	defer func() {
		journal.mu.Lock()
		journal.closeLocked()
		journal.mu.Unlock()
	}()

	for i := 0; i < 10; i++ {
		b.Publish("score.changed", i)
	}
	// Starting late is not a loss: the journal begins with what the bus still remembers.
	journal.catchUp(nil)
	journal.Flush()
	assert.Equal(t, []uint64{7, 8, 9, 10}, seqs(readJournal(t, path)))

	// Ten more happen while the journal is stuck with event 12 in hand. The bus has forgotten 11 to 16, so 12 is
	// written from the hand, and a line says that 13 to 16 are missing.
	var inHand Event
	for i := 0; i < 10; i++ {
		if event := b.Publish("score.changed", i); event.Seq == 12 {
			inHand = event
		}
	}
	journal.catchUp(&inHand)
	journal.Flush()
	events := readJournal(t, path)
	assert.Equal(t, []uint64{7, 8, 9, 10, 0, 12, 0, 17, 18, 19, 20}, seqs(events))
	assert.Equal(t, JournalGapType, events[4].Type)
	assert.Equal(t, map[string]any{"after": float64(10), "before": float64(12)}, events[4].Data)
	assert.Equal(t, JournalGapType, events[6].Type)
	assert.Equal(t, map[string]any{"after": float64(12), "before": float64(17)}, events[6].Data)

	// An event already written is not written again.
	journal.writeNext(Event{Seq: 20, Type: "score.changed", Time: clock})
	journal.Flush()
	assert.Len(t, readJournal(t, path), 11)
}

func TestJournalPaths(t *testing.T) {
	journal := NewJournal(New(4), filepath.Join("some", "dir"), 0)
	assert.Equal(t, filepath.Join("some", "dir"), journal.Dir())
	path, err := journal.Path("2026-11-21")
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join("some", "dir", "2026-11-21.jsonl"), path)
	// Only a date reaches the file system.
	for _, day := range []string{"", "today", "2026-11-21.jsonl", "../event", "2026-11-21/../../x", `..\..\event.db`} {
		_, err = journal.Path(day)
		assert.Error(t, err, day)
	}
}

func TestJournalThatCannotBeKept(t *testing.T) {
	// A file where the folder should be.
	blocked := filepath.Join(t.TempDir(), "journal")
	require.NoError(t, os.WriteFile(blocked, nil, 0644))
	journal := NewJournal(New(4), blocked, 0)
	assert.ErrorContains(t, journal.Start(), "the event journal cannot be kept in")
	journal.Stop()
}
