// The binding set in force: loading it for the active game and field profile, saving a new one, and keeping it
// compiled against the devices as they come and go.

package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
)

// BindingsState is the binding set in force and what is wrong with it.
type BindingsState struct {
	Game     string      `json:"game"`
	Profile  string      `json:"profile"`
	Set      *BindingSet `json:"set"`
	Problems Problems    `json:"problems"`
}

// loadBindings reads the binding set of the active game and profile from the database and puts it in force. A
// stored set that no longer reads is reported as a problem, and no bindings are in force.
func (m *Manager) loadBindings() {
	// storeMu keeps the database from being replaced between reading the set and putting it in force.
	m.storeMu.Lock()
	m.mu.Lock()
	game, profile, db := m.game, m.profile, m.db
	m.mu.Unlock()

	set, loadError := readBindings(db, game, profile)

	m.mu.Lock()
	defer m.unlockStore()
	if m.game != game || m.profile != profile {
		// The game or profile changed again while the set was read; that call loads the right one.
		return
	}
	m.source, m.loadError = set, loadError
	m.recompile()
}

// readBindings reads the binding set a database holds for a game and a profile: an empty one if it holds none, or
// with no game or no database. If the stored set cannot be read, it returns an empty set and why.
func readBindings(db *model.Database, game *gamedef.Game, profile string) (*BindingSet, string) {
	if game == nil || db == nil {
		return &BindingSet{}, ""
	}
	stored, err := db.GetBindingSet(game.ID, profile)
	switch {
	case err != nil:
		return &BindingSet{}, fmt.Sprintf(
			"the bindings of %q in field profile %q could not be read: %v", game.ID, profile, err,
		)
	case stored == nil:
		return &BindingSet{}, ""
	}
	set, err := ParseBindings([]byte(stored.Yaml))
	if err != nil {
		return &BindingSet{}, fmt.Sprintf("the stored bindings of %q in field profile %q: %v", game.ID, profile, err)
	}
	return set, ""
}

// recompile compiles the binding set in force against the game and the devices as they are now. The caller holds
// mu.
func (m *Manager) recompile() {
	previous := m.set
	m.set = compile(m.source, m.environment(), previous)
	m.seedInputs()
	m.releaseUndriven(previous)
	m.dropStaleFaults()
}

// Bindings returns the binding set in force, with every problem it has right now: errors that keep bindings from
// working (because a device's manifest changed, say) and warnings.
func (m *Manager) Bindings() BindingsState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := BindingsState{Profile: m.profile, Set: m.source.Clone(), Problems: Problems{}}
	if m.game != nil {
		state.Game = m.game.ID
	}
	if m.loadError != "" {
		state.Problems = append(state.Problems, Problem{Message: m.loadError})
	}
	state.Problems = append(state.Problems, m.set.problems...)
	return state
}

// ValidateBindings checks a binding set against the active game and the devices, without saving it.
func (m *Manager) ValidateBindings(set *BindingSet) Problems {
	m.mu.Lock()
	defer m.mu.Unlock()
	return compile(set, m.environment(), nil).problems
}

// SaveBindings stores a binding set for the active game and profile and puts it in force. A set with errors is
// refused and nothing changes; the problems, errors and warnings alike, are returned either way.
func (m *Manager) SaveBindings(set *BindingSet) (Problems, error) {
	// storeMu is held from the check until the set is in force, so that the database it was written to is still
	// the one in use by then. What putting it in force decided is carried out once storeMu is released, because
	// storing a device takes it.
	m.storeMu.Lock()
	problems, work, err := m.saveBindings(set)
	m.storeMu.Unlock()
	m.run(work)
	return problems, err
}

// saveBindings does what SaveBindings says. The caller holds storeMu, and carries out the work returned once it
// has released it.
func (m *Manager) saveBindings(set *BindingSet) (Problems, deferredWork, error) {
	m.mu.Lock()
	game, profile, db := m.game, m.profile, m.db
	problems := compile(set, m.environment(), nil).problems
	m.mu.Unlock()
	if game == nil {
		return problems, deferredWork{}, fmt.Errorf("there is no active game to bind to")
	}
	if err := problems.Err(); err != nil {
		return problems, deferredWork{}, err
	}
	data, err := set.YAML()
	if err != nil {
		return problems, deferredWork{}, err
	}
	if db != nil {
		if err = db.SaveBindingSet(game.ID, profile, string(data)); err != nil {
			return problems, deferredWork{}, err
		}
	}

	m.mu.Lock()
	if m.game == game && m.profile == profile {
		m.source, m.loadError = set.Clone(), ""
		m.recompile()
	}
	work := m.work
	m.work = deferredWork{}
	m.mu.Unlock()
	return problems, work, nil
}

// ExportBindings returns the binding set in force as YAML.
func (m *Manager) ExportBindings() ([]byte, error) {
	m.mu.Lock()
	source := m.source.Clone()
	m.mu.Unlock()
	return source.YAML()
}

// ImportBindings reads a binding set from YAML and saves it as SaveBindings does.
func (m *Manager) ImportBindings(data []byte) (Problems, error) {
	set, err := ParseBindings(data)
	if err != nil {
		return Problems{{Message: err.Error()}}, err
	}
	return m.SaveBindings(set)
}
