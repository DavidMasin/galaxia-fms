package model

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLightingFixtureCrud(t *testing.T) {
	db := setupTestDb(t)

	fixtures, err := db.GetLightingFixtures(1)
	assert.Nil(t, err)
	assert.Empty(t, fixtures)

	half := 0.5
	strip := &LightingFixture{
		ProfileId: 1, FixtureId: "goal-red", Position: 2, Name: "Red goal", DeviceId: "goal-red", ChannelId: "strip",
		Kind: "led_strip", Pixels: 30, Brightness: 0.8, Reverse: true, Alliance: "red",
		Segments: []LightingSegment{{Id: "left", Name: "Left", Start: 0, Count: 15, Alliance: "red"}},
	}
	require.Nil(t, db.CreateLightingFixture(strip))
	assert.Equal(t, 1, strip.Id)
	stack := &LightingFixture{
		ProfileId: 1, FixtureId: "stack", Position: 1, Name: "Stack", DeviceId: "stack", ChannelId: "green",
		Kind: "digital", Brightness: half,
	}
	require.Nil(t, db.CreateLightingFixture(stack))
	require.Nil(t, db.CreateLightingFixture(&LightingFixture{ProfileId: 2, FixtureId: "other", Kind: "relay"}))

	fixtures, err = db.GetLightingFixtures(1)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(fixtures)) {
		assert.Equal(t, *stack, fixtures[0], "ordered by position")
		assert.Equal(t, *strip, fixtures[1])
	}

	strip.Segments = append(strip.Segments, LightingSegment{Id: "right", Start: 15, Count: 15, Alliance: "blue"})
	strip.Position = 0
	require.Nil(t, db.UpdateLightingFixture(strip))
	read, err := db.GetLightingFixtureById(strip.Id)
	assert.Nil(t, err)
	assert.Equal(t, strip, read)
	fixtures, _ = db.GetLightingFixtures(1)
	assert.Equal(t, "goal-red", fixtures[0].FixtureId)

	require.Nil(t, db.DeleteLightingFixture(strip.Id))
	fixtures, _ = db.GetLightingFixtures(1)
	assert.Equal(t, 1, len(fixtures))

	require.Nil(t, db.TruncateLightingFixtures())
	fixtures, _ = db.GetLightingFixtures(2)
	assert.Empty(t, fixtures)
}

func TestLightingScenesCrud(t *testing.T) {
	db := setupTestDb(t)

	scenes, err := db.GetLightingScenes("ball-count", 1)
	assert.Nil(t, err)
	assert.Nil(t, scenes)

	brightness := 0.4
	scenes = &LightingScenes{
		GameId: "ball-count", ProfileId: 1, Seeded: []string{"goal-red"},
		Cells: map[string]map[string]LightingLook{
			"estop":        {"goal-red": {P: "blink", C: []string{"#ff0000"}}},
			"phase.teleop": {"goal-red/left": {P: "progress", C: []string{"alliance", "#202020"}, V: "0.5", Br: &brightness}},
		},
	}
	require.Nil(t, db.SaveLightingScenes(scenes))
	assert.Equal(t, 1, scenes.Id)
	require.Nil(t, db.SaveLightingScenes(&LightingScenes{GameId: "rebuilt-style", ProfileId: 1}))
	require.Nil(t, db.SaveLightingScenes(&LightingScenes{GameId: "ball-count", ProfileId: 2}))

	read, err := db.GetLightingScenes("ball-count", 1)
	assert.Nil(t, err)
	assert.Equal(t, scenes.Cells, read.Cells)
	assert.Equal(t, scenes.Seeded, read.Seeded)
	assert.Equal(t, scenes.UpdatedAt.Unix(), read.UpdatedAt.Unix())

	read.Cells["idle"] = map[string]LightingLook{"goal-red": {P: "solid", C: []string{"#ffffff"}, When: "true"}}
	require.Nil(t, db.SaveLightingScenes(read))
	read, _ = db.GetLightingScenes("ball-count", 1)
	assert.Equal(t, "true", read.Cells["idle"]["goal-red"].When)
	assert.Equal(t, 1, read.Id, "saving again updates the record")

	all, err := db.GetAllLightingScenes(1)
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(all)) {
		assert.Equal(t, "ball-count", all[0].GameId)
		assert.Equal(t, "rebuilt-style", all[1].GameId)
	}

	require.Nil(t, db.DeleteLightingScenes(read.Id))
	read, _ = db.GetLightingScenes("ball-count", 1)
	assert.Nil(t, read)
	require.Nil(t, db.TruncateLightingScenes())
	all, _ = db.GetAllLightingScenes(2)
	assert.Empty(t, all)
}

func TestLightingOfProfile(t *testing.T) {
	db := setupTestDb(t)

	// The profile id is looked up without creating the default profile.
	id, err := db.GetLightingProfileId(DefaultFieldProfile)
	assert.Nil(t, err)
	assert.Equal(t, 0, id)
	profiles, _ := db.GetAllFieldProfiles()
	id, err = db.GetLightingProfileId(DefaultFieldProfile)
	assert.Nil(t, err)
	assert.Equal(t, profiles[0].Id, id)

	// Lighting is kept by profile id, so it survives renaming the profile.
	tent := &FieldProfile{Name: "tent"}
	require.Nil(t, db.CreateFieldProfile(tent))
	require.Nil(t, db.CreateLightingFixture(&LightingFixture{ProfileId: tent.Id, FixtureId: "a", Kind: "relay"}))
	require.Nil(t, db.SaveLightingScenes(&LightingScenes{GameId: "ball-count", ProfileId: tent.Id}))
	require.Nil(t, db.CreateLightingFixture(&LightingFixture{ProfileId: profiles[0].Id, FixtureId: "b"}))
	require.Nil(t, db.RenameFieldProfile(tent.Id, "big tent"))
	id, _ = db.GetLightingProfileId("big tent")
	assert.Equal(t, tent.Id, id)
	fixtures, _ := db.GetLightingFixtures(id)
	assert.Equal(t, 1, len(fixtures))

	require.Nil(t, db.DeleteLightingOfProfile(tent.Id))
	fixtures, _ = db.GetLightingFixtures(tent.Id)
	assert.Empty(t, fixtures)
	scenes, _ := db.GetAllLightingScenes(tent.Id)
	assert.Empty(t, scenes)
	fixtures, _ = db.GetLightingFixtures(profiles[0].Id)
	assert.Equal(t, 1, len(fixtures), "other profiles keep theirs")
}
