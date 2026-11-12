package gamedef

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Registry is the set of games found in a directory, one per subdirectory. It notices files that change on disk,
// so a game can be edited while the system is running.
type Registry struct {
	dir      string
	mu       sync.RWMutex
	games    map[string]*Game
	problems map[string]Diagnostics
	stamps   map[string]fileStamp
}

type fileStamp struct {
	modified time.Time
	size     int64
}

// Summary describes one game in the registry, whether or not it loaded.
type Summary struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Valid       bool        `json:"valid"`
	Hash        string      `json:"hash,omitempty"`
	Diagnostics Diagnostics `json:"diagnostics"`
}

var gameIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// NewRegistry creates a registry over dir. Call Load before using it.
func NewRegistry(dir string) *Registry {
	return &Registry{
		dir: dir, games: map[string]*Game{}, problems: map[string]Diagnostics{}, stamps: map[string]fileStamp{},
	}
}

// Seed copies games from fsys (the examples built into the binary) into the registry's directory, if that
// directory does not exist yet. It never overwrites anything.
func (r *Registry) Seed(fsys fs.FS) error {
	if _, err := os.Stat(r.dir); err == nil {
		return nil
	}
	return fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(r.dir, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if filepath.Ext(path) == ".go" {
			return nil
		}
		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0644)
	})
}

// Load reads every game in the directory, replacing what the registry held.
func (r *Registry) Load() error {
	_, err := r.Poll()
	return err
}

// Poll re-reads any game whose file has changed, appeared or disappeared since the last call, and returns the ids
// of those games. It is cheap when nothing has changed.
func (r *Registry) Poll() ([]string, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			entries = nil
		} else {
			return nil, err
		}
	}
	seen := map[string]fileStamp{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(r.dir, entry.Name(), GameFile))
		if err != nil {
			continue
		}
		seen[entry.Name()] = fileStamp{modified: info.ModTime(), size: info.Size()}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var changed []string
	for id := range r.stamps {
		if _, still := seen[id]; !still {
			delete(r.stamps, id)
			delete(r.games, id)
			delete(r.problems, id)
			changed = append(changed, id)
		}
	}
	for id, stamp := range seen {
		if r.stamps[id] == stamp {
			continue
		}
		r.stamps[id] = stamp
		game, diagnostics := LoadFile(filepath.Join(r.dir, id, GameFile))
		if game != nil && game.ID != id {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "error", Path: "/id", Line: 1,
				Message: fmt.Sprintf("id %q must match the directory name %q", game.ID, id),
			})
			game = nil
		}
		delete(r.games, id)
		delete(r.problems, id)
		if game != nil {
			r.games[id] = game
		}
		if len(diagnostics) > 0 {
			r.problems[id] = diagnostics
		}
		changed = append(changed, id)
	}
	sort.Strings(changed)
	return changed, nil
}

// Get returns the game with the given id, or nil if there is none or it has errors.
func (r *Registry) Get(id string) *Game {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.games[id]
}

// List describes every game in the directory, valid or not, sorted by id.
func (r *Registry) List() []Summary {
	r.mu.RLock()
	defer r.mu.RUnlock()
	summaries := make([]Summary, 0, len(r.stamps))
	for id := range r.stamps {
		summary := Summary{ID: id, Name: id, Diagnostics: r.problems[id]}
		if game := r.games[id]; game != nil {
			summary.Name, summary.Description, summary.Valid, summary.Hash = game.Name, game.Description, true, game.Hash
		}
		if summary.Diagnostics == nil {
			summary.Diagnostics = Diagnostics{}
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(a, b int) bool { return summaries[a].ID < summaries[b].ID })
	return summaries
}

// Source returns the YAML of a game as it is on disk, even if it does not load.
func (r *Registry) Source(id string) ([]byte, error) {
	if !gameIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%q is not a valid game id", id)
	}
	return os.ReadFile(filepath.Join(r.dir, id, GameFile))
}

// Save validates src and, if it has no errors, writes it as the game with the given id and reloads it. The
// diagnostics are returned either way; nothing is written when they contain errors.
func (r *Registry) Save(id string, src []byte) (Diagnostics, error) {
	if !gameIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%q is not a valid game id: use lowercase letters, digits and dashes", id)
	}
	game, diagnostics := Parse(src)
	if game != nil && game.ID != id {
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "error", Path: "/id", Line: 1,
			Message: fmt.Sprintf("the file says id %q, but it is being saved as %q", game.ID, id),
		})
	}
	if diagnostics.HasErrors() {
		return diagnostics, nil
	}
	dir := filepath.Join(r.dir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return diagnostics, err
	}
	// Write to a temporary file and rename, so a crash cannot leave half a game on disk.
	temporary := filepath.Join(dir, GameFile+".tmp")
	if err := os.WriteFile(temporary, src, 0644); err != nil {
		return diagnostics, err
	}
	if err := os.Rename(temporary, filepath.Join(dir, GameFile)); err != nil {
		return diagnostics, err
	}
	_, err := r.Poll()
	return diagnostics, err
}
