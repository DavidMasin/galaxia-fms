// The binding set in force: loading it for the active game and field profile, saving a new one, and keeping it
// compiled against the devices as they come and go.

package device

import (
	"fmt"
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
	m.mu.Lock()
	game, profile := m.game, m.profile
	m.mu.Unlock()

	set, loadError := &BindingSet{}, ""
	if game != nil && m.db != nil {
		stored, err := m.db.GetBindingSet(game.ID, profile)
		switch {
		case err != nil:
			loadError = fmt.Sprintf(
				"the bindings of %q in field profile %q could not be read: %v", game.ID, profile, err,
			)
		case stored != nil:
			if parsed, err := ParseBindings([]byte(stored.Yaml)); err != nil {
				loadError = fmt.Sprintf("the stored bindings of %q in field profile %q: %v", game.ID, profile, err)
			} else {
				set = parsed
			}
		}
	}

	m.mu.Lock()
	defer m.unlock()
	if m.game != game || m.profile != profile {
		// The game or profile changed again while the set was read; that call loads the right one.
		return
	}
	m.source, m.loadError = set, loadError
	m.recompile()
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
	m.mu.Lock()
	game, profile := m.game, m.profile
	problems := compile(set, m.environment(), nil).problems
	m.mu.Unlock()
	if game == nil {
		return problems, fmt.Errorf("there is no active game to bind to")
	}
	if err := problems.Err(); err != nil {
		return problems, err
	}
	data, err := set.YAML()
	if err != nil {
		return problems, err
	}
	if m.db != nil {
		if err = m.db.SaveBindingSet(game.ID, profile, string(data)); err != nil {
			return problems, err
		}
	}

	m.mu.Lock()
	defer m.unlock()
	if m.game == game && m.profile == profile {
		m.source, m.loadError = set.Clone(), ""
		m.recompile()
	}
	return problems, nil
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
