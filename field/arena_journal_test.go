package field

import (
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArenaJournal(t *testing.T) {
	arena := setupTestArena(t)
	dir := filepath.Join(filepath.Dir(arena.Database.Path), "journal")
	assert.Equal(t, dir, arena.Journal.Dir())

	// An arena that is not run keeps no journal, so tests leave nothing behind.
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err))

	// Running it starts the journal and announces the start, which is how a reader tells one run from the next.
	arena.startJournal()
	arena.Journal.Stop()
	data, err := os.ReadFile(filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl"))
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Contains(t, lines[len(lines)-1], `"type":"system.started"`)
	assert.Contains(t, lines[len(lines)-1], `"game":"`+arena.Game.ID+`"`)
}
