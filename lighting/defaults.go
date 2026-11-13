// The starting scenes a new fixture gets in each game.

package lighting

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/gamedef"
)

// Colours of the starting scenes.
const (
	startingEStopColor      = "#ff0000"
	startingFaultColor      = "#ff8000"
	startingFieldResetColor = "#00ff00"
	startingVolunteersColor = "#8000ff"
	startingIdleColor       = white
)

// StartingScenes returns the looks a new fixture starts with in a game, by moment, as docs/devices.md lists them:
// the alliance colour while each phase runs, green on field reset, purple for volunteers, red blinking on E-stop,
// orange blinking on a fault, and white at idle. A digital, relay or pwm fixture has no colours: it is on, or
// blinks.
func StartingScenes(game *gamedef.Game, fixture *Fixture) map[string]*Look {
	look := func(pattern, color string) *Look {
		if fixture.Kind != KindLEDStrip {
			return &Look{P: pattern}
		}
		return &Look{P: pattern, C: []string{color}}
	}
	looks := map[string]*Look{
		MomentEStop:      look(device.PatternBlink, startingEStopColor),
		MomentFault:      look(device.PatternBlink, startingFaultColor),
		MomentFieldReset: look(device.PatternSolid, startingFieldResetColor),
		MomentVolunteers: look(device.PatternSolid, startingVolunteersColor),
		MomentIdle:       look(device.PatternSolid, startingIdleColor),
	}
	if game != nil {
		for _, phase := range game.Match.Phases {
			looks[phasePrefix+phase.ID] = look(device.PatternSolid, ColorAlliance)
		}
	}
	return looks
}

// seedFixture gives a fixture its starting scenes in the cells it has none in.
func seedFixture(config *Config, game *gamedef.Game, fixture *Fixture) {
	for moment, look := range StartingScenes(game, fixture) {
		if config.Cell(moment, fixture.ID) == nil {
			config.SetCell(moment, fixture.ID, look)
		}
	}
}
