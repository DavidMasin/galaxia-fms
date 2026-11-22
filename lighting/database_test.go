package lighting

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

const lampConfig = `
fixtures:
  - { id: lamp, device: goal, channel: lamp, kind: digital }
scenes:
  idle: { lamp: { p: solid } }
`

// restoredHarness is a harness whose database stands in for a backup: another event's lighting, a fixture on the
// goal's lamp. It is made before the harness that takes its database over, so that it is cleaned up after it.
func restoredHarness(t *testing.T) *harness {
	backup := newHarness(t)
	backup.connectGoal()
	backup.load(lampConfig)
	return backup
}

func TestSetDatabase(t *testing.T) {
	backup := restoredHarness(t)
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig)
	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")
	assert.Equal(t, map[string]bool{"goal": true}, h.engine.Status().Claimed)

	// As restoring a backup does it: the old database is closed, the device manager is given the new one, and then
	// the engine.
	require.Nil(t, h.db.Close())
	require.Nil(t, h.devices.SetDatabase(backup.db))
	require.Nil(t, h.engine.SetDatabase(backup.db, h.game))

	// The restored lighting is in force at once, without waiting for a tick: its fixture is claimed, and the one
	// the old database had is let go of and cleared.
	status := h.engine.Status()
	assert.True(t, status.Loaded)
	assert.Empty(t, status.LoadError)
	assert.Equal(t, "ball-count", status.Game)
	assert.Equal(t, map[string]bool{"lamp": true}, status.Claimed)
	assert.Equal(t, map[device.ChannelRef]string{{Device: "goal", Channel: "lamp"}: Owner}, h.devices.Claims())
	assertOutput(t, nil, goal, "strip")
	h.idle()
	assertOutput(t, &device.Command{V: number(1)}, goal, "lamp")

	// The page is shown what the restored database holds.
	config, problems, err := h.engine.Load(h.game, "default")
	require.Nil(t, err)
	assert.Empty(t, problems)
	if assert.Equal(t, 1, len(config.Fixtures)) {
		assert.Equal(t, "lamp", config.Fixtures[0].ID)
	}
	assert.Equal(t, &Look{P: "solid"}, config.Cell("idle", "lamp"))

	// And what it saves goes there. The old database is closed, so nothing can have gone to it.
	_, err = h.engine.SetCell(h.game, "default", "idle", "lamp", &Look{P: "blink"})
	require.Nil(t, err)
	profileId, err := backup.db.GetLightingProfileId("default")
	require.Nil(t, err)
	scenes, err := backup.db.GetLightingScenes(h.game.ID, profileId)
	require.Nil(t, err)
	assert.Equal(t, "blink", scenes.Cells["idle"]["lamp"].P)
	_, err = h.engine.SaveFixture(h.game, "default", &Fixture{
		ID: "dimmer", Device: "goal", Channel: "dimmer", Kind: KindPWM,
	}, true)
	require.Nil(t, err)
	fixtures, err := backup.db.GetLightingFixtures(profileId)
	require.Nil(t, err)
	assert.Equal(t, 2, len(fixtures))
	assert.Equal(t, map[string]bool{"lamp": true, "dimmer": true}, h.engine.Status().Claimed)

	// Given the database it already has, the engine reloads nothing.
	revision := h.engine.Status().Revision
	require.Nil(t, h.engine.SetDatabase(backup.db, h.game))
	assert.Equal(t, revision, h.engine.Status().Revision)
}

// A database that has no lighting for the active profile leaves no fixture claimed.
func TestSetDatabaseWithoutLighting(t *testing.T) {
	empty := newHarness(t)
	h := newHarness(t)
	goal := h.connectGoal()
	h.load(priorityConfig)
	h.idle()
	assertOutput(t, solid("#ffffff"), goal, "strip")

	require.Nil(t, h.db.Close())
	require.Nil(t, h.devices.SetDatabase(empty.db))
	require.Nil(t, h.engine.SetDatabase(empty.db, h.game))
	assert.Empty(t, h.engine.Status().Claimed)
	assert.Empty(t, h.devices.Claims())
	assertOutput(t, nil, goal, "strip")
	config, _, err := h.engine.Load(h.game, "default")
	require.Nil(t, err)
	assert.Empty(t, config.Fixtures)
}

// The database is replaced while the page reads, saves and reloads, and the field ticks in between: nothing
// deadlocks, and the race detector finds nothing.
func TestSetDatabaseWhileInUse(t *testing.T) {
	backup := restoredHarness(t)
	h := newHarness(t)
	h.connectGoal()
	h.load(priorityConfig)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _, _ = h.engine.Load(h.game, "default")
			h.engine.Status()
			h.engine.CellValues()
			_ = h.engine.Reload(h.game)
		}
	}()
	for i := 0; i < 10; i++ {
		require.Nil(t, h.devices.SetDatabase(backup.db))
		require.Nil(t, h.engine.SetDatabase(backup.db, h.game))
		h.idle()
		require.Nil(t, h.devices.SetDatabase(h.db))
		require.Nil(t, h.engine.SetDatabase(h.db, h.game))
		h.idle()
	}
	close(stop)
	wg.Wait()
	assert.Equal(t, map[string]bool{"goal": true}, h.engine.Status().Claimed)
}
