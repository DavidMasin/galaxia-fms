// What the Lighting engine needs to evaluate its own expressions exactly as output bindings are evaluated: the names
// they may use and the values of the names the manager adds to the game's.

package device

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
)

// OutputRoots returns the names an output expression may use: the game's live names (with own when perAlliance is
// set), plus stations, field_reset, volunteers and fault.
func OutputRoots(game *gamedef.Game, stations []string, perAlliance bool) map[string]*rules.Shape {
	return outputRoots(game, stations, perAlliance)
}

// OutputExtraRoots returns the values of the names OutputRoots adds to the game's, for the given snapshot, to be
// passed to Snapshot.Eval as its extra roots.
func (m *Manager) OutputExtraRoots(snapshot *Snapshot) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outputExtraRoots(snapshot)
}
