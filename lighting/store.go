// Reading and changing the stored lighting: what the Lighting page edits, for any field profile. A change to the
// active profile is put in force at once.

package lighting

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"slices"
	"strings"
)

// load reads the lighting of a profile for a game and gives every fixture that has not had them in this game its
// starting scenes (if seed is set). The caller holds storeMu.
func (e *Engine) load(game *gamedef.Game, profileId int, profile string, seed bool) (*Config, error) {
	config := &Config{Profile: profile, Fixtures: []*Fixture{}, Scenes: Scenes{}}
	records, err := e.db.GetLightingFixtures(profileId)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		config.Fixtures = append(config.Fixtures, fixtureFromRecord(record))
	}
	if game == nil {
		return config, nil
	}
	config.Game = game.ID
	record, err := e.db.GetLightingScenes(game.ID, profileId)
	if err != nil {
		return nil, err
	}
	config.Scenes = scenesFromRecord(record)
	if !seed {
		return config, nil
	}
	if record == nil {
		record = &model.LightingScenes{GameId: game.ID, ProfileId: profileId}
	}
	seeded := false
	for _, fixture := range config.Fixtures {
		if !slices.Contains(record.Seeded, fixture.ID) {
			seedFixture(config, game, fixture)
			record.Seeded = append(record.Seeded, fixture.ID)
			seeded = true
		}
	}
	if seeded {
		scenesRecord(config.Scenes, record)
		if err = e.db.SaveLightingScenes(record); err != nil {
			return nil, err
		}
	}
	return config, nil
}

// profileId returns the id of a field profile, creating the default profile if the event has none yet. The caller
// holds storeMu.
func (e *Engine) profileId(profile string) (int, error) {
	record, err := e.db.GetFieldProfileByName(profile)
	if err != nil {
		return 0, err
	}
	if record == nil {
		return 0, fmt.Errorf("there is no field profile %q", profile)
	}
	return record.Id, nil
}

// isActive reports whether a profile is the one in force.
func (e *Engine) isActive(profile string) bool {
	return e.devices.Profile() == profile
}

// Load returns the lighting of a field profile for a game, and everything wrong with it. Fixtures that have not had
// them in this game are given their starting scenes.
func (e *Engine) Load(game *gamedef.Game, profile string) (*Config, Problems, error) {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	profileId, err := e.profileId(profile)
	if err != nil {
		return nil, nil, err
	}
	config, err := e.load(game, profileId, profile, true)
	if err != nil {
		return nil, nil, err
	}
	problems := compile(config, e.environment(game)).problems
	if problems == nil {
		problems = Problems{}
	}
	return config, problems, nil
}

// SaveFixture adds a fixture to a profile (create set) or changes one. A new fixture gets its starting scenes;
// cells of segments a change removes are deleted in every game. Nothing is saved if the fixture has errors, and on
// the active profile, if a binding drives its channel. The problems of the whole configuration are returned.
func (e *Engine) SaveFixture(game *gamedef.Game, profile string, fixture *Fixture, create bool) (Problems, error) {
	fixture = fixture.clone()
	fixture.ID = strings.TrimSpace(fixture.ID)
	fixture.Name = strings.TrimSpace(fixture.Name)
	if fixture.Name == "" {
		fixture.Name = fixture.ID
	}
	if fixture.Kind != KindLEDStrip {
		fixture.Pixels, fixture.Reverse, fixture.Segments = 0, false, nil
	}
	if fixture.Brightness != nil && *fixture.Brightness == 1 {
		fixture.Brightness = nil
	}
	for i := range fixture.Segments {
		segment := &fixture.Segments[i]
		segment.ID, segment.Name = strings.TrimSpace(segment.ID), strings.TrimSpace(segment.Name)
		if segment.Name == "" {
			segment.Name = segment.ID
		}
	}

	// The number of LEDs is the strip's own, once it has said.
	if manifest := e.devices.Environment().Manifests[fixture.Device]; fixture.Kind == KindLEDStrip {
		if channel := manifest.Channel(fixture.Channel); channel != nil && channel.Type == KindLEDStrip {
			fixture.Pixels = channel.Pixels
		}
	}

	problems, err := e.change(game, profile, func(profileId int, config *Config) (Problems, error) {
		existing := config.Fixture(fixture.ID)
		switch {
		case create && existing != nil:
			return nil, fmt.Errorf("there is already a fixture %q", fixture.ID)
		case !create && existing == nil:
			return nil, fmt.Errorf("there is no fixture %q", fixture.ID)
		}
		var removed []string
		if existing != nil {
			kept := make(map[string]bool)
			for _, segment := range fixture.Segments {
				kept[segment.ID] = true
			}
			for _, segment := range existing.Segments {
				if !kept[segment.ID] {
					removed = append(removed, SegmentTarget(fixture.ID, segment.ID))
				}
			}
			*existing = *fixture
		} else {
			config.Fixtures = append(config.Fixtures, fixture)
		}
		problems := compile(config, e.environment(game)).problems
		var errors Problems
		for _, problem := range problems.Errors() {
			if problem.Fixture == fixture.ID && problem.Moment == "" {
				errors = append(errors, problem)
			}
		}
		if len(errors) > 0 {
			return problems, fmt.Errorf("the fixture cannot be saved: %v", errors.Err())
		}
		if e.isActive(profile) {
			for _, output := range e.devices.Bindings().Set.Outputs {
				if output.Device == fixture.Device && output.Channel == fixture.Channel {
					return problems, fmt.Errorf(
						"channel %s/%s is driven by output binding %q; an output is lit by a binding or by the "+
							"Lighting page, not both", fixture.Device, fixture.Channel, output.ID,
					)
				}
			}
		}

		// Store it.
		records, err := e.db.GetLightingFixtures(profileId)
		if err != nil {
			return problems, err
		}
		var record *model.LightingFixture
		position := 0
		for i := range records {
			position = max(position, records[i].Position+1)
			if records[i].FixtureId == fixture.ID {
				record = &records[i]
			}
		}
		if record == nil {
			record = &model.LightingFixture{ProfileId: profileId, Position: position}
			fixtureRecord(fixture, record)
			err = e.db.CreateLightingFixture(record)
		} else {
			fixtureRecord(fixture, record)
			err = e.db.UpdateLightingFixture(record)
		}
		if err != nil {
			return problems, err
		}
		if len(removed) > 0 {
			err = e.dropTargets(profileId, removed, nil)
		}
		return problems, err
	})
	if err != nil {
		return problems, err
	}
	// A new fixture's starting scenes, and the problems of the configuration as it now is.
	_, problems, err = e.Load(game, profile)
	return problems, err
}

// DeleteFixture removes a fixture from a profile, with its cells in every game.
func (e *Engine) DeleteFixture(game *gamedef.Game, profile, fixtureId string) error {
	_, err := e.change(game, profile, func(profileId int, config *Config) (Problems, error) {
		fixture := config.Fixture(fixtureId)
		if fixture == nil {
			return nil, fmt.Errorf("there is no fixture %q", fixtureId)
		}
		records, err := e.db.GetLightingFixtures(profileId)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.FixtureId == fixtureId {
				if err = e.db.DeleteLightingFixture(record.Id); err != nil {
					return nil, err
				}
			}
		}
		return nil, e.dropTargets(profileId, fixture.Targets(), []string{fixtureId})
	})
	return err
}

// dropTargets deletes the cells of the given targets in every game of a profile, and forgets that the given
// fixtures were given their starting scenes. The caller holds storeMu.
func (e *Engine) dropTargets(profileId int, targets, fixtures []string) error {
	records, err := e.db.GetAllLightingScenes(profileId)
	if err != nil {
		return err
	}
	for i := range records {
		record := &records[i]
		changed := false
		for moment, row := range record.Cells {
			for _, target := range targets {
				if _, ok := row[target]; ok {
					delete(row, target)
					changed = true
				}
			}
			if len(row) == 0 {
				delete(record.Cells, moment)
			}
		}
		seeded := slices.DeleteFunc(slices.Clone(record.Seeded), func(id string) bool {
			return slices.Contains(fixtures, id)
		})
		if len(seeded) != len(record.Seeded) {
			record.Seeded, changed = seeded, true
		}
		if changed {
			if err = e.db.SaveLightingScenes(record); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetCell sets the look of one cell of a profile's scenes for a game; a nil look clears the cell. A look with
// errors is not saved; the problems of that cell are returned.
func (e *Engine) SetCell(game *gamedef.Game, profile, moment, target string, look *Look) (Problems, error) {
	if game == nil {
		return nil, fmt.Errorf("there is no active game")
	}
	return e.change(game, profile, func(profileId int, config *Config) (Problems, error) {
		var problems Problems
		if look != nil {
			look = look.clone()
			look.V, look.When = strings.TrimSpace(look.V), strings.TrimSpace(look.When)
			config.SetCell(moment, target, look)
			for _, problem := range compile(config, e.environment(game)).problems {
				if problem.Moment == moment && problem.Target == target {
					// An unknown moment or target is only a warning for a stored configuration, which another game
					// may make sense of; a new cell must make sense now.
					problem.Warning = problem.Warning && problem.unknown == ""
					problems = append(problems, problem)
				}
			}
			if err := problems.Err(); err != nil {
				return problems, err
			}
		}
		record, err := e.db.GetLightingScenes(game.ID, profileId)
		if err != nil {
			return problems, err
		}
		if record == nil {
			record = &model.LightingScenes{GameId: game.ID, ProfileId: profileId}
		}
		if record.Cells == nil {
			record.Cells = map[string]map[string]model.LightingLook{}
		}
		if look == nil {
			delete(record.Cells[moment], target)
			if len(record.Cells[moment]) == 0 {
				delete(record.Cells, moment)
			}
		} else {
			if record.Cells[moment] == nil {
				record.Cells[moment] = map[string]model.LightingLook{}
			}
			record.Cells[moment][target] = lookRecord(look)
		}
		return problems, e.db.SaveLightingScenes(record)
	})
}

// change runs a change to a profile's stored lighting under storeMu, given the profile's configuration for the game
// as stored, and puts the profile in force again if it is the active one.
func (e *Engine) change(
	game *gamedef.Game, profile string, apply func(profileId int, config *Config) (Problems, error),
) (Problems, error) {
	e.storeMu.Lock()
	profileId, err := e.profileId(profile)
	var problems Problems
	if err == nil {
		var config *Config
		if config, err = e.load(game, profileId, profile, false); err == nil {
			problems, err = apply(profileId, config)
		}
	}
	e.storeMu.Unlock()
	if err != nil {
		return problems, err
	}
	if e.isActive(profile) {
		if err = e.Reload(game); err != nil {
			return problems, err
		}
	}
	if problems == nil {
		problems = Problems{}
	}
	return problems, nil
}

// Export returns a profile's fixtures and its scenes for a game as YAML.
func (e *Engine) Export(game *gamedef.Game, profile string) ([]byte, error) {
	config, _, err := e.Load(game, profile)
	if err != nil {
		return nil, err
	}
	return config.YAML()
}

// Import replaces a profile's fixtures, and its scenes for the game, with those of an exported file. Cells of
// moments the game does not have are left out. Nothing changes if the file has errors.
func (e *Engine) Import(game *gamedef.Game, profile string, data []byte) (Problems, error) {
	if game == nil {
		return nil, fmt.Errorf("there is no active game")
	}
	imported, err := ParseConfig(data)
	if err != nil {
		return Problems{{Message: err.Error()}}, err
	}
	for _, fixture := range imported.Fixtures {
		if fixture.Name == "" {
			fixture.Name = fixture.ID
		}
	}
	return e.change(game, profile, func(profileId int, _ *Config) (Problems, error) {
		problems := compile(imported, e.environment(game)).problems
		var errors, kept Problems
		for _, problem := range problems {
			switch {
			case problem.unknown == unknownMoment:
				// The file was made for another game, or the game lost a phase or a cue.
				imported.SetCell(problem.Moment, problem.Target, nil)
				kept = append(kept, problem)
			case problem.unknown == unknownTarget:
				problem.Warning = false
				errors = append(errors, problem)
			case !problem.Warning:
				errors = append(errors, problem)
			default:
				kept = append(kept, problem)
			}
		}
		if imported.Game != "" && imported.Game != game.ID {
			kept = append(kept, Problem{
				Warning: true, Message: fmt.Sprintf("the file was exported for game %q", imported.Game),
			})
		}
		if len(errors) > 0 {
			return append(errors, kept...), fmt.Errorf("the file cannot be imported: %v", errors.Err())
		}

		// Replace the fixtures.
		records, err := e.db.GetLightingFixtures(profileId)
		if err != nil {
			return problems, err
		}
		for _, record := range records {
			if err = e.db.DeleteLightingFixture(record.Id); err != nil {
				return problems, err
			}
		}
		var targets []string
		ids := make([]string, 0, len(imported.Fixtures))
		for i, fixture := range imported.Fixtures {
			record := &model.LightingFixture{ProfileId: profileId, Position: i}
			fixtureRecord(fixture, record)
			if err = e.db.CreateLightingFixture(record); err != nil {
				return problems, err
			}
			targets = append(targets, fixture.Targets()...)
			ids = append(ids, fixture.ID)
		}

		// The cells of fixtures and segments that are gone go from every game.
		var gone []string
		for _, record := range records {
			fixture := fixtureFromRecord(record)
			for _, target := range fixture.Targets() {
				if !slices.Contains(targets, target) {
					gone = append(gone, target)
				}
			}
		}
		if err = e.dropTargets(profileId, gone, nil); err != nil {
			return problems, err
		}

		// The game's scenes are the file's, and the file's fixtures count as given their starting scenes.
		record, err := e.db.GetLightingScenes(game.ID, profileId)
		if err != nil {
			return problems, err
		}
		if record == nil {
			record = &model.LightingScenes{GameId: game.ID, ProfileId: profileId}
		}
		scenesRecord(imported.Scenes, record)
		record.Seeded = ids
		return kept, e.db.SaveLightingScenes(record)
	})
}
