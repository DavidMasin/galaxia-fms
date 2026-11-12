// Model and datastore methods for the game definitions that stored match results were played with.

package model

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
)

// GameSnapshot is one distinct game definition file that at least one match result was played with. A result is
// always recomputed with its own snapshot, so editing a game later does not rewrite history.
type GameSnapshot struct {
	Id     int `db:"id"`
	Hash   string
	GameId string
	Yaml   string
}

// SaveGameSnapshot stores the game's source, unless a snapshot with the same hash is already there.
func (database *Database) SaveGameSnapshot(g *gamedef.Game) error {
	database.snapshotMutex.Lock()
	defer database.snapshotMutex.Unlock()

	existing, err := database.findGameSnapshot(g.Hash)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	return database.gameSnapshotTable.create(&GameSnapshot{Hash: g.Hash, GameId: g.ID, Yaml: string(g.Source)})
}

// GetGameSnapshot returns the snapshot with the given hash, or nil if there is none.
func (database *Database) GetGameSnapshot(hash string) (*GameSnapshot, error) {
	database.snapshotMutex.Lock()
	defer database.snapshotMutex.Unlock()

	return database.findGameSnapshot(hash)
}

// GetSnapshotGame returns the parsed game for the snapshot with the given hash. Parsing compiles every expression
// in the file, so each snapshot is parsed once and then reused; a parsed game is never modified.
func (database *Database) GetSnapshotGame(hash string) (*gamedef.Game, error) {
	database.snapshotMutex.Lock()
	defer database.snapshotMutex.Unlock()

	if g, ok := database.snapshotGames[hash]; ok {
		return g, nil
	}
	snapshot, err := database.findGameSnapshot(hash)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("there is no stored game definition with hash %q", hash)
	}
	g, diagnostics := gamedef.Parse([]byte(snapshot.Yaml))
	if g == nil {
		return nil, fmt.Errorf("the stored definition of game %q no longer loads: %w", snapshot.GameId, diagnostics)
	}
	database.snapshotGames[hash] = g
	return g, nil
}

func (database *Database) GetAllGameSnapshots() ([]GameSnapshot, error) {
	return database.gameSnapshotTable.getAll()
}

func (database *Database) TruncateGameSnapshots() error {
	database.snapshotMutex.Lock()
	defer database.snapshotMutex.Unlock()

	database.snapshotGames = make(map[string]*gamedef.Game)
	return database.gameSnapshotTable.truncate()
}

// findGameSnapshot scans for a snapshot by hash. The table is keyed by an integer like every other table, and an
// event only ever has a handful of snapshots, so a scan is cheaper than a second index would be to maintain.
func (database *Database) findGameSnapshot(hash string) (*GameSnapshot, error) {
	snapshots, err := database.gameSnapshotTable.getAll()
	if err != nil {
		return nil, err
	}
	for i := range snapshots {
		if snapshots[i].Hash == hash {
			return &snapshots[i], nil
		}
	}
	return nil, nil
}
