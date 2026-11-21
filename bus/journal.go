// The journal: every event appended to a file as it happens, one JSON object per line and one file per day. The bus
// only remembers recent events, and only while the process lives; the journal is what is left of a session
// afterwards, to tail, to export, and to line up with robot logs.

package bus

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultJournalDays is how many days of journal files are kept.
	DefaultJournalDays = 30
	// JournalGapType marks, in the journal only, events that were lost before they could be written.
	JournalGapType = "journal.gap"

	journalExtension = ".jsonl"
	// How many events may wait to be written. Writing is much faster than the field makes events, so this is only
	// reached if the disk stalls; what overflows is read back from the bus's memory.
	journalBuffer = 4096
	// How long a written event may sit in memory before it reaches the file, so that tailing the file works.
	journalFlushEvery = time.Second
)

var journalDayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// JournalDay is one day's file.
type JournalDay struct {
	Day   string `json:"day"` // As in the file name: 2026-11-21, by the clock of the computer the FMS runs on.
	Bytes int64  `json:"bytes"`
}

// Journal writes the events of a bus to files.
type Journal struct {
	bus      *Bus
	dir      string
	keepDays int
	now      func() time.Time

	// mu guards the file and its writer, which the writing goroutine and Flush both use.
	mu      sync.Mutex
	file    *os.File
	writer  *bufio.Writer
	day     string
	failing bool
	// The sequence number of the last event written. Only the writing goroutine uses it.
	lastSeq uint64

	started bool
	stop    chan struct{}
	done    chan struct{}
}

// NewJournal makes a journal that will write the bus's events into dir. Nothing is written until Start, so that an
// arena made for a test leaves no files behind.
func NewJournal(b *Bus, dir string, keepDays int) *Journal {
	if keepDays < 1 {
		keepDays = DefaultJournalDays
	}
	return &Journal{bus: b, dir: dir, keepDays: keepDays, now: time.Now}
}

// Start begins writing. The events the bus still remembers from before are written first, so that a journal started
// a moment after the process has the whole run.
func (j *Journal) Start() error {
	if j.started {
		return nil
	}
	if err := os.MkdirAll(j.dir, 0755); err != nil {
		return fmt.Errorf("the event journal cannot be kept in %s: %w", j.dir, err)
	}
	j.started = true
	j.stop, j.done = make(chan struct{}), make(chan struct{})
	// Subscribe before reading the bus's memory, so that nothing falls between the two; what is in both is written
	// once, by its sequence number.
	subscription := j.bus.Subscribe(nil, journalBuffer)
	j.prune()
	go j.run(subscription)
	return nil
}

// Stop writes what is waiting and closes the file.
func (j *Journal) Stop() {
	if !j.started {
		return
	}
	close(j.stop)
	<-j.done
	j.started = false
}

// Dir returns where the journal's files are.
func (j *Journal) Dir() string {
	return j.dir
}

// Days lists the days there is a file for, newest first.
func (j *Journal) Days() ([]JournalDay, error) {
	entries, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return []JournalDay{}, nil
	}
	if err != nil {
		return nil, err
	}
	days := []JournalDay{}
	for _, entry := range entries {
		day, isJournal := strings.CutSuffix(entry.Name(), journalExtension)
		if entry.IsDir() || !isJournal || !journalDayPattern.MatchString(day) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		days = append(days, JournalDay{Day: day, Bytes: info.Size()})
	}
	sort.Slice(days, func(a, b int) bool { return days[a].Day > days[b].Day })
	return days, nil
}

// Path returns the file of a day written as 2026-11-21, or an error if that is not a day. The file may not exist.
func (j *Journal) Path(day string) (string, error) {
	// Only a date reaches the file system, whatever was asked for.
	if !journalDayPattern.MatchString(day) {
		return "", fmt.Errorf("%q is not a day; write it as 2026-11-21", day)
	}
	return filepath.Join(j.dir, day+journalExtension), nil
}

// Flush puts what has been written so far into the file, for a reader who wants it now.
func (j *Journal) Flush() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.flushLocked()
}

func (j *Journal) run(subscription *Subscription) {
	defer close(j.done)
	defer subscription.Close()
	ticker := time.NewTicker(journalFlushEvery)
	defer ticker.Stop()
	j.catchUp(nil)
	for {
		select {
		case <-j.stop:
			// Take what is already waiting, so that a clean stop loses nothing.
			for {
				select {
				case event, ok := <-subscription.C:
					if ok {
						j.receive(subscription, event)
						continue
					}
				default:
				}
				break
			}
			j.mu.Lock()
			j.closeLocked()
			j.mu.Unlock()
			return
		case event, ok := <-subscription.C:
			if !ok {
				return
			}
			j.receive(subscription, event)
		case <-ticker.C:
			j.Flush()
		}
	}
}

// receive writes one event from the subscription, after any the bus dropped because the journal fell behind.
func (j *Journal) receive(subscription *Subscription, event Event) {
	if subscription.Dropped() > 0 {
		// Reset before reading the bus's memory: anything dropped after this is counted again and caught next time.
		subscription.ResetDropped()
		j.catchUp(&event)
	} else {
		j.writeNext(event)
	}
}

// catchUp writes, in order, the events the bus remembers after the last one written, together with the event in
// hand if there is one: it waited in the queue while later ones were dropped, so the bus may have forgotten it.
func (j *Journal) catchUp(current *Event) {
	events, _ := j.bus.Since(j.lastSeq, nil)
	if current != nil && current.Seq > j.lastSeq {
		i, found := slices.BinarySearchFunc(events, current.Seq, func(event Event, seq uint64) int {
			return cmp.Compare(event.Seq, seq)
		})
		if !found {
			events = slices.Insert(events, i, *current)
		}
	}
	for _, event := range events {
		j.writeNext(event)
	}
}

// writeNext writes the next event, after a line saying which events between it and the last one written were lost.
// An event that was already written, which the queue and the bus's memory can both hold, is not written again.
func (j *Journal) writeNext(event Event) {
	if event.Seq <= j.lastSeq {
		return
	}
	if event.Seq > j.lastSeq+1 && j.lastSeq > 0 {
		j.write(Event{
			Time: j.now().UTC(), Type: JournalGapType,
			Data: map[string]uint64{"after": j.lastSeq, "before": event.Seq},
		})
	}
	j.lastSeq = max(j.lastSeq, event.Seq)
	j.write(event)
}

// write appends one event to the file of its day.
func (j *Journal) write(event Event) {
	line, err := json.Marshal(event)
	if err != nil {
		log.Printf("Journal: event %d (%s) cannot be written as JSON: %v", event.Seq, event.Type, err)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	day := event.Time.Local().Format("2006-01-02")
	if day != j.day || j.file == nil {
		j.closeLocked()
		path := filepath.Join(j.dir, day+journalExtension)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			j.fail(err)
			return
		}
		newDay := j.day != "" && day != j.day
		j.file, j.writer, j.day = file, bufio.NewWriter(file), day
		if newDay {
			// A new day's file is the moment to drop the oldest.
			go j.prune()
		}
	}
	if _, err = j.writer.Write(append(line, '\n')); err != nil {
		j.fail(err)
		return
	}
	if j.failing {
		log.Printf("Journal: writing to %s works again", j.dir)
		j.failing = false
	}
}

// fail reports that the journal cannot be written, once for as long as it lasts rather than once per event.
func (j *Journal) fail(err error) {
	if !j.failing {
		log.Printf("Journal: events are not being written: %v", err)
		j.failing = true
	}
	j.closeLocked()
}

func (j *Journal) flushLocked() {
	if j.writer == nil {
		return
	}
	if err := j.writer.Flush(); err != nil {
		j.fail(err)
	}
}

func (j *Journal) closeLocked() {
	if j.file == nil {
		return
	}
	if err := j.writer.Flush(); err != nil && !j.failing {
		log.Printf("Journal: events are not being written: %v", err)
		j.failing = true
	}
	j.file.Close()
	j.file, j.writer = nil, nil
}

// prune deletes the files of days more than keepDays ago.
func (j *Journal) prune() {
	days, err := j.Days()
	if err != nil {
		return
	}
	oldest := j.now().Local().AddDate(0, 0, -j.keepDays+1).Format("2006-01-02")
	for _, day := range days {
		if day.Day < oldest {
			if err = os.Remove(filepath.Join(j.dir, day.Day+journalExtension)); err != nil {
				log.Printf("Journal: an old file could not be deleted: %v", err)
			}
		}
	}
}
