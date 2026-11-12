package gamedef

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRegistrySeedsOnlyWhenMissing(t *testing.T) {
	builtIn := fstest.MapFS{
		"test-game/game.yaml":  {Data: []byte(base)},
		"test-game/tests.yaml": {Data: []byte("cases: []\n")},
		"embed.go":             {Data: []byte("package games\n")},
	}
	dir := filepath.Join(t.TempDir(), "games")
	registry := NewRegistry(dir)
	assert.NoError(t, registry.Seed(builtIn))
	assert.NoError(t, registry.Load())
	assert.NotNil(t, registry.Get("test-game"))
	assert.FileExists(t, filepath.Join(dir, "test-game", "tests.yaml"))
	assert.NoFileExists(t, filepath.Join(dir, "embed.go"))

	// Once the directory exists it belongs to the user: seeding again restores nothing.
	assert.NoError(t, os.RemoveAll(filepath.Join(dir, "test-game")))
	assert.NoError(t, registry.Seed(builtIn))
	assert.NoDirExists(t, filepath.Join(dir, "test-game"))
}

func TestRegistryPollNoticesChanges(t *testing.T) {
	dir := t.TempDir()
	registry := NewRegistry(dir)
	assert.NoError(t, registry.Load())
	assert.Empty(t, registry.List())

	// A missing directory is an empty registry, not an error.
	missing := NewRegistry(filepath.Join(dir, "nowhere"))
	assert.NoError(t, missing.Load())
	assert.Empty(t, missing.List())

	write := func(id, content string, age time.Duration) {
		path := filepath.Join(dir, id, GameFile)
		assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		assert.NoError(t, os.WriteFile(path, []byte(content), 0644))
		// Set the modification time explicitly so the test does not depend on the clock's resolution.
		stamp := time.Now().Add(-age)
		assert.NoError(t, os.Chtimes(path, stamp, stamp))
	}

	write("test-game", base, time.Hour)
	write("broken", "schema: 1\nid: broken\n", time.Hour)
	assert.NoError(t, os.MkdirAll(filepath.Join(dir, "no-game-file"), 0755))
	changed, err := registry.Poll()
	assert.NoError(t, err)
	assert.Equal(t, []string{"broken", "test-game"}, changed)
	assert.NotNil(t, registry.Get("test-game"))
	assert.Nil(t, registry.Get("broken"))

	list := registry.List()
	if assert.Len(t, list, 2) {
		assert.Equal(t, "broken", list[0].ID)
		assert.False(t, list[0].Valid)
		assert.NotEmpty(t, list[0].Diagnostics)
		assert.Equal(t, "Test Game", list[1].Name)
		assert.True(t, list[1].Valid)
		assert.Empty(t, list[1].Diagnostics)
		assert.Len(t, list[1].Hash, 64)
	}

	// Nothing changed.
	changed, err = registry.Poll()
	assert.NoError(t, err)
	assert.Empty(t, changed)

	// Edit one, delete the other.
	write("test-game", strings.Replace(base, "name: Test Game", "name: Renamed", 1), time.Minute)
	assert.NoError(t, os.RemoveAll(filepath.Join(dir, "broken")))
	changed, err = registry.Poll()
	assert.NoError(t, err)
	assert.Equal(t, []string{"broken", "test-game"}, changed)
	assert.Equal(t, "Renamed", registry.Get("test-game").Name)
	assert.Len(t, registry.List(), 1)

	// A file that becomes invalid stops being offered, and its problems are listed.
	write("test-game", strings.Replace(base, "type: counter", "type: tally", 1), time.Second)
	changed, err = registry.Poll()
	assert.NoError(t, err)
	assert.Equal(t, []string{"test-game"}, changed)
	assert.Nil(t, registry.Get("test-game"))
	assert.False(t, registry.List()[0].Valid)
}

func TestRegistrySave(t *testing.T) {
	dir := t.TempDir()
	registry := NewRegistry(dir)
	assert.NoError(t, registry.Load())

	diagnostics, err := registry.Save("test-game", []byte(base))
	assert.NoError(t, err)
	assert.Empty(t, diagnostics)
	assert.NotNil(t, registry.Get("test-game"))
	saved, err := registry.Source("test-game")
	assert.NoError(t, err)
	assert.Equal(t, base, string(saved))
	assert.NoFileExists(t, filepath.Join(dir, "test-game", GameFile+".tmp"))

	// Errors are returned and nothing is written.
	broken := strings.Replace(base, "type: counter", "type: tally", 1)
	diagnostics, err = registry.Save("test-game", []byte(broken))
	assert.NoError(t, err)
	assert.True(t, diagnostics.HasErrors())
	saved, _ = registry.Source("test-game")
	assert.Equal(t, base, string(saved))

	// The id in the file must be the id it is saved under.
	diagnostics, err = registry.Save("other-game", []byte(base))
	assert.NoError(t, err)
	if assert.True(t, diagnostics.HasErrors()) {
		assert.Contains(t, diagnostics.Error(), `the file says id "test-game", but it is being saved as "other-game"`)
	}
	assert.NoDirExists(t, filepath.Join(dir, "other-game"))

	// Ids that could escape the games directory are refused.
	for _, id := range []string{"../evil", "a/b", "", "UPPER", "..", "has space"} {
		_, err = registry.Save(id, []byte(base))
		assert.Error(t, err, id)
		_, err = registry.Source(id)
		assert.Error(t, err, id)
	}
	_, err = registry.Source("not-there")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestView(t *testing.T) {
	games, _, err := LoadDir(gamesDir)
	assert.NoError(t, err)
	view := games["orbital-rush"].View()

	assert.Equal(t, "orbital-rush", view.ID)
	assert.Equal(t, 2, view.AllianceSize)
	assert.Equal(t, []string{"R1", "R2", "B1", "B2", "G1", "G2"}, view.Stations)
	assert.Equal(t, 125.0, view.Duration)
	assert.Equal(t, false, view.Features["brackets"])
	assert.Equal(t, true, view.Features["gameData"])
	assert.Len(t, view.Elements, 16)
	assert.Len(t, view.Params, 4)
	assert.Len(t, view.RankingPoints, 5)
	assert.Equal(t, []string{"Fewest major fouls", "Launch points", "Dock points"}, view.Tiebreakers)

	// Phases carry their start time and the offset of each cue.
	assert.Equal(t, "orbit", view.Phases[2].ID)
	assert.Equal(t, 18.0, view.Phases[2].Start)
	assert.Equal(t, CueView{ID: "storm_warning", Offset: 30, Sound: "shift_change"}, view.Phases[2].Cues[1])
	assert.Equal(t, CueView{ID: "final_ten", Offset: 20, Sound: "warning"}, view.Phases[4].Cues[1])

	byID := map[string]ElementView{}
	for _, element := range view.Elements {
		byID[element.ID] = element
	}
	assert.Equal(t, []string{"launch", "orbit", "reentry"}, byID["cargo"].Phases)
	assert.True(t, byID["cargo"].PhasePoints)
	assert.True(t, byID["shield"].PhasePoints, "an option with by-phase points makes the phase matter")
	assert.False(t, byID["dock"].PhasePoints)
	// Defaults are filled in where the game says nothing.
	assert.Equal(t, []string{"launch", "drift", "orbit", "blackout", "reentry"}, byID["comets"].Phases)
	assert.Equal(t, []string{"scorer"}, byID["storm"].Edit)

	ballCount := games["ball-count"].View()
	assert.Equal(t, true, ballCount.Features["brackets"])
	assert.Equal(t, false, ballCount.Features["gameData"])
	assert.Equal(t, "stepper", ballCount.Elements[0].Widget)
	assert.Equal(t, "Scoring", ballCount.Elements[0].Group)
	assert.Equal(t, []string{"device", "scorer"}, ballCount.Elements[0].Edit)
	assert.Equal(t, "red", ballCount.Alliances[0].DS)

	// A game that leaves sections out still sends empty lists, never null, because browser code loops over them.
	encoded, err := json.Marshal(ballCount)
	assert.NoError(t, err)
	assert.NotContains(t, string(encoded), "null")
	for _, key := range []string{"cards", "rules", "panels", "indicators", "derived", "live", "params"} {
		assert.Contains(t, string(encoded), `"`+key+`":[]`)
	}
}
