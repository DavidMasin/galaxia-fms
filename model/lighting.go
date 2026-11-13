// Model and datastore methods for the Lighting page: the fixtures of each field profile (the outputs that show
// lighting) and the scene grid of each game in each profile. The lighting package checks and runs them.
//
// Both are kept by the id of their field profile rather than its name, so that renaming a profile carries its
// lighting along without anything having to be rewritten.

package model

import (
	"sort"
	"time"
)

// LightingFixture is one output channel that shows lighting in a field profile.
type LightingFixture struct {
	Id         int `db:"id"`
	ProfileId  int
	FixtureId  string // Unique within the profile; scenes name it.
	Position   int    // Order on the page.
	Name       string
	DeviceId   string
	ChannelId  string
	Kind       string // The channel type: led_strip, digital, relay or pwm.
	Pixels     int    // Number of LEDs of a led_strip, as its manifest said when the fixture was last saved.
	Brightness float64
	Reverse    bool
	Alliance   string
	Segments   []LightingSegment
}

// LightingSegment is a named run of LEDs of a fixture.
type LightingSegment struct {
	Id       string
	Name     string
	Start    int
	Count    int
	Alliance string
}

// LightingLook is how a fixture or segment looks at one moment: one cell of the scene grid.
type LightingLook struct {
	P    string   `json:"p,omitempty"`
	C    []string `json:"c,omitempty"`
	Ms   int      `json:"ms,omitempty"`
	V    string   `json:"v,omitempty"`
	When string   `json:"when,omitempty"`
	Br   *float64 `json:"br,omitempty"`
}

// LightingScenes is the scene grid of one game in one field profile.
type LightingScenes struct {
	Id        int `db:"id"`
	GameId    string
	ProfileId int
	Cells     map[string]map[string]LightingLook // Moment id -> target (fixture or fixture/segment) -> look.
	// The fixtures that have been given their starting scenes in this game, so that a fixture whose scenes an
	// operator cleared is not given them again.
	Seeded    []string
	UpdatedAt time.Time
}

// GetLightingProfileId returns the id of the field profile with the given name, or 0 if there is none. Unlike
// GetFieldProfileByName it never writes: the Lighting engine calls it while reloading in the background, which
// must not create profiles behind the Settings page's back.
func (database *Database) GetLightingProfileId(name string) (int, error) {
	profiles, err := database.fieldProfileTable.getAll()
	if err != nil {
		return 0, err
	}
	for _, profile := range profiles {
		if profile.Name == name {
			return profile.Id, nil
		}
	}
	return 0, nil
}

func (database *Database) CreateLightingFixture(fixture *LightingFixture) error {
	return database.lightingFixtureTable.create(fixture)
}

func (database *Database) GetLightingFixtureById(id int) (*LightingFixture, error) {
	return database.lightingFixtureTable.getById(id)
}

func (database *Database) UpdateLightingFixture(fixture *LightingFixture) error {
	return database.lightingFixtureTable.update(fixture)
}

func (database *Database) DeleteLightingFixture(id int) error {
	return database.lightingFixtureTable.delete(id)
}

// GetLightingFixtures returns the fixtures of a field profile, in page order.
func (database *Database) GetLightingFixtures(profileId int) ([]LightingFixture, error) {
	all, err := database.lightingFixtureTable.getAll()
	if err != nil {
		return nil, err
	}
	var fixtures []LightingFixture
	for _, fixture := range all {
		if fixture.ProfileId == profileId {
			fixtures = append(fixtures, fixture)
		}
	}
	sort.SliceStable(fixtures, func(i, j int) bool {
		if fixtures[i].Position != fixtures[j].Position {
			return fixtures[i].Position < fixtures[j].Position
		}
		return fixtures[i].Id < fixtures[j].Id
	})
	return fixtures, nil
}

func (database *Database) TruncateLightingFixtures() error {
	return database.lightingFixtureTable.truncate()
}

// GetLightingScenes returns the scene grid of a game in a field profile, or nil if none has been saved.
func (database *Database) GetLightingScenes(gameId string, profileId int) (*LightingScenes, error) {
	all, err := database.lightingScenesTable.getAll()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].GameId == gameId && all[i].ProfileId == profileId {
			return &all[i], nil
		}
	}
	return nil, nil
}

// GetAllLightingScenes returns the scene grids of every game of a field profile, ordered by game.
func (database *Database) GetAllLightingScenes(profileId int) ([]LightingScenes, error) {
	all, err := database.lightingScenesTable.getAll()
	if err != nil {
		return nil, err
	}
	var scenes []LightingScenes
	for _, record := range all {
		if record.ProfileId == profileId {
			scenes = append(scenes, record)
		}
	}
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].GameId < scenes[j].GameId })
	return scenes, nil
}

// SaveLightingScenes stores a scene grid, creating the record if it has no id yet.
func (database *Database) SaveLightingScenes(scenes *LightingScenes) error {
	scenes.UpdatedAt = time.Now().UTC()
	if scenes.Id == 0 {
		return database.lightingScenesTable.create(scenes)
	}
	return database.lightingScenesTable.update(scenes)
}

func (database *Database) DeleteLightingScenes(id int) error {
	return database.lightingScenesTable.delete(id)
}

func (database *Database) TruncateLightingScenes() error {
	return database.lightingScenesTable.truncate()
}

// DeleteLightingOfProfile deletes the fixtures and scene grids of a field profile, for when the profile is deleted.
func (database *Database) DeleteLightingOfProfile(profileId int) error {
	fixtures, err := database.GetLightingFixtures(profileId)
	if err != nil {
		return err
	}
	for _, fixture := range fixtures {
		if err = database.lightingFixtureTable.delete(fixture.Id); err != nil {
			return err
		}
	}
	scenes, err := database.GetAllLightingScenes(profileId)
	if err != nil {
		return err
	}
	for _, record := range scenes {
		if err = database.lightingScenesTable.delete(record.Id); err != nil {
			return err
		}
	}
	return nil
}
