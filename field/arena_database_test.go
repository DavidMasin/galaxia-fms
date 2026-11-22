package field

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/lighting"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

// A restored database may select another game and another field profile. The arena gives the device manager all
// three together, so the devices, the bindings and the lighting that are in use afterwards are the ones the
// restored event has for its own game and profile.
func TestLoadSettingsWithAnotherDatabase(t *testing.T) {
	goal := &device.Manifest{Proto: 1, ID: "goal", Name: "Goal", Channels: []*device.Channel{
		{ID: "beam", Dir: device.DirIn, Type: device.TypeDigital},
		{ID: "horn", Dir: device.DirOut, Type: device.TypeRelay},
	}}

	// The event that is backed up plays ball-count in the profile "tent".
	backedUp := setupTestArena(t)
	startDevices(t, backedUp, false)
	backedUp.EventSettings.GameId = "ball-count"
	backedUp.EventSettings.FieldProfile = "tent"
	require.Nil(t, backedUp.Database.CreateFieldProfile(&model.FieldProfile{Name: "tent"}))
	require.Nil(t, backedUp.Database.UpdateEventSettings(backedUp.EventSettings))
	require.Nil(t, backedUp.LoadSettings())
	_, err := backedUp.DeviceVirtual.Connect(goal)
	require.Nil(t, err)
	require.Nil(t, backedUp.Devices.Approve("goal"))
	bindDevices(t, backedUp, `
inputs:
  - { id: backed-up, device: goal, channel: beam, action: { field: start_match } }
`)
	_, err = backedUp.Lighting.SaveFixture(backedUp.Game, "tent", &lighting.Fixture{
		ID: "horn", Device: "goal", Channel: "horn", Kind: lighting.KindRelay,
	}, true)
	require.Nil(t, err)
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	backup, err := os.Create(backupPath)
	require.Nil(t, err)
	require.Nil(t, backedUp.Database.WriteBackup(backup))
	require.Nil(t, backup.Close())

	// The event it is restored over plays the default game in the default profile, with a binding of its own.
	arena := setupTestArena(t)
	startDevices(t, arena, false)
	require.NotEqual(t, "ball-count", arena.Game.ID)
	_, err = arena.Devices.CreateDevice("old-1", "")
	require.Nil(t, err)
	bindDevices(t, arena, `
inputs:
  - { id: not-backed-up, device: old-1, channel: button, action: { field: field_reset } }
`)

	// What restoring does: the database is closed, the backup opened in its place, and the settings loaded.
	require.Nil(t, arena.Database.Close())
	arena.Database, err = model.OpenDatabase(backupPath)
	require.Nil(t, err)
	require.Nil(t, arena.LoadSettings())

	assert.Equal(t, "ball-count", arena.Game.ID)
	assert.Equal(t, "tent", arena.Devices.Profile())
	devices := arena.Devices.Devices()
	if assert.Equal(t, 1, len(devices)) {
		assert.Equal(t, "goal", devices[0].ID)
	}
	bindings := arena.Devices.Bindings()
	assert.Equal(t, "ball-count", bindings.Game)
	assert.Equal(t, "tent", bindings.Profile)
	assert.Empty(t, bindings.Problems)
	if assert.Equal(t, 1, len(bindings.Set.Inputs)) {
		assert.Equal(t, "backed-up", bindings.Set.Inputs[0].ID)
	}
	status := arena.Lighting.Status()
	assert.Equal(t, "ball-count", status.Game)
	assert.Equal(t, "tent", status.Profile)
	assert.Equal(t, map[string]bool{"horn": true}, status.Claimed)

	// And what is saved now goes to the restored database.
	bindDevices(t, arena, `
inputs:
  - { id: saved-after, device: goal, channel: beam, action: { field: volunteers } }
`)
	stored, err := arena.Database.GetBindingSet("ball-count", "tent")
	require.Nil(t, err)
	if assert.NotNil(t, stored) {
		assert.Contains(t, stored.Yaml, "saved-after")
	}
}
