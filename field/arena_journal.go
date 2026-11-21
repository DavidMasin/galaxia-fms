// The arena's side of the event journal: every event on the bus written to a file per day, beside the database.

package field

import (
	"github.com/DavidMasin/galaxia-fms/bus"
	"log"
	"path/filepath"
)

// journalFolder is where the journal's files go, beside the database file.
const journalFolder = "journal"

// arenaJournal is embedded in Arena, so that the journal is reached as arena.Journal.
type arenaJournal struct {
	Journal *bus.Journal // Writes every event to a file per day.
}

// newJournal makes the journal. Nothing is written until startJournal, so that an arena made for a test leaves no
// files behind; what the bus remembers from before is written then.
func (arena *Arena) newJournal() {
	dir := filepath.Join(filepath.Dir(arena.Database.Path), journalFolder)
	arena.Journal = bus.NewJournal(arena.Bus, dir, bus.DefaultJournalDays)
}

// startJournal starts writing, and announces that the FMS has started: sequence numbers begin again with each run,
// and this event is how a reader of the journal, the event stream or a webhook can tell.
func (arena *Arena) startJournal() {
	if err := arena.Journal.Start(); err != nil {
		// The field runs without a journal; the reason is in the log.
		log.Printf("%v", err)
	}
	arena.Bus.Publish("system.started", map[string]any{"game": arena.Game.ID})
}
