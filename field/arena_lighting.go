// The arena's side of the Lighting page: it builds the engine, which drives the fixtures from the device manager's
// output ticks, and tells it what the device snapshot does not say about the field.

package field

import (
	"github.com/DavidMasin/galaxia-fms/lighting"
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

// lightingField is what the Lighting engine needs to know about the field beyond the device snapshot. It runs on
// the arena goroutine, which owns everything it reads.
func (arena *Arena) lightingField() lighting.FieldInfo {
	return lighting.FieldInfo{
		FieldStop:  arena.Devices.Stops().FieldStop || (arena.Plc.IsEnabled() && arena.Plc.GetFieldEStop()),
		TeamsReady: len(arena.getAllianceStationStartConditions(arena.activeStationIds()...)) == 0,
		Awards:     arena.AllianceStationDisplayMode == "logo",
	}
}
