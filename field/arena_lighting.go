// The arena's side of the Lighting page: it builds the engine, which drives the fixtures from the device manager's
// output ticks, and tells it what the device snapshot does not say about the field.

package field

import (
	"github.com/DavidMasin/galaxia-fms/lighting"
	"log"
)

// arenaLighting is embedded in Arena, so that the engine is reached as arena.Lighting.
type arenaLighting struct {
	Lighting *lighting.Engine // Drives the Lighting page's fixtures from its scenes.
}

// newLighting builds the Lighting engine. It runs from the device manager's output ticks, on the arena goroutine,
// and loads the lighting of the active profile and game by itself once the first tick says which game that is.
func (arena *Arena) newLighting() {
	arena.Lighting = lighting.NewEngine(lighting.Options{
		Devices: arena.Devices, Database: arena.Database, Bus: arena.Bus, Field: arena.lightingField,
	})
}

// applyLightingDatabase gives the Lighting engine the database. It is the one it has, unless a backup was restored:
// then the engine reads the lighting of the active profile and game from the new one before this returns. It comes
// after the device manager has the database and the game is settled, because lighting is compiled against the
// manager's devices and claims its channels around the manager's bindings.
func (arena *Arena) applyLightingDatabase() error {
	err := arena.Lighting.SetDatabase(arena.Database, arena.Game)
	if err != nil {
		log.Printf("Lighting: %v", err)
	}
	return err
}

// lightingField is what the Lighting engine needs to know about the field beyond the device snapshot. It runs on
// the arena goroutine, which owns everything it reads.
func (arena *Arena) lightingField() lighting.FieldInfo {
	return lighting.FieldInfo{
		FieldStop:  arena.Devices.Stops().FieldStop || (arena.Plc.IsEnabled() && arena.Plc.GetFieldEStop()),
		TeamsReady: len(arena.getAllianceStationStartConditions(arena.activeStationIds()...)) == 0,
		Awards:     arena.AllianceStationDisplayMode == "logo",
	}
}
