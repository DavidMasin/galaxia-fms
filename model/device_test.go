package model

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestDeviceCrud(t *testing.T) {
	db := setupTestDb(t)

	device, err := db.GetDeviceByDeviceId("beam-red-1")
	assert.Nil(t, err)
	assert.Nil(t, device)

	device = &Device{
		DeviceId: "beam-red-1", State: DevicePending, TokenHash: "abc", Manifest: `{"proto":1}`,
		CounterTotals: map[string]float64{"count": 4}, CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	assert.Nil(t, db.CreateDevice(device))
	assert.Equal(t, 1, device.Id)
	assert.NotNil(t, db.CreateDevice(&Device{DeviceId: "beam-red-1"}), "device ids are unique")

	device2, err := db.GetDeviceByDeviceId("beam-red-1")
	assert.Nil(t, err)
	assert.Equal(t, device, device2)
	device2, err = db.GetDeviceById(1)
	assert.Nil(t, err)
	assert.Equal(t, device, device2)

	device.State = DeviceApproved
	device.Required = true
	device.Notes = "Under the red goal"
	assert.Nil(t, db.UpdateDevice(device))
	device2, _ = db.GetDeviceByDeviceId("beam-red-1")
	assert.Equal(t, device, device2)

	assert.Nil(t, db.CreateDevice(&Device{DeviceId: "arcade-1", State: DeviceBlocked}))
	devices, err := db.GetAllDevices()
	assert.Nil(t, err)
	if assert.Equal(t, 2, len(devices)) {
		assert.Equal(t, "arcade-1", devices[0].DeviceId)
		assert.Equal(t, "beam-red-1", devices[1].DeviceId)
	}

	assert.Nil(t, db.DeleteDevice(device.Id))
	device2, err = db.GetDeviceByDeviceId("beam-red-1")
	assert.Nil(t, err)
	assert.Nil(t, device2)

	assert.Nil(t, db.TruncateDevices())
	devices, _ = db.GetAllDevices()
	assert.Empty(t, devices)
}

func TestFieldProfiles(t *testing.T) {
	db := setupTestDb(t)

	// The default profile appears the first time the profiles are read.
	profiles, err := db.GetAllFieldProfiles()
	assert.Nil(t, err)
	assert.Equal(t, []FieldProfile{{Id: 1, Name: DefaultFieldProfile}}, profiles)
	profiles, _ = db.GetAllFieldProfiles()
	assert.Equal(t, 1, len(profiles))

	tent := &FieldProfile{Name: "  tent "}
	assert.Nil(t, db.CreateFieldProfile(tent))
	assert.Equal(t, "tent", tent.Name)
	err = db.CreateFieldProfile(&FieldProfile{Name: "tent"})
	assert.EqualError(t, err, `there is already a field profile named "tent"`)
	assert.EqualError(t, db.CreateFieldProfile(&FieldProfile{Name: " "}), "a field profile needs a name")
	profile, err := db.GetFieldProfileByName("tent")
	assert.Nil(t, err)
	assert.Equal(t, tent, profile)

	// Renaming carries the binding sets and the event's choice along.
	assert.Nil(t, db.SaveBindingSet("ball-count", "tent", "inputs: []\n"))
	settings, _ := db.GetEventSettings()
	settings.FieldProfile = "tent"
	assert.Nil(t, db.UpdateEventSettings(settings))
	assert.Nil(t, db.RenameFieldProfile(tent.Id, "school gym"))
	settings, _ = db.GetEventSettings()
	assert.Equal(t, "school gym", settings.FieldProfile)
	bindingSet, _ := db.GetBindingSet("ball-count", "school gym")
	if assert.NotNil(t, bindingSet) {
		assert.Equal(t, "inputs: []\n", bindingSet.Yaml)
	}
	assert.EqualError(t, db.RenameFieldProfile(tent.Id, "default"), `there is already a field profile named "default"`)

	// The active profile cannot be deleted; another can, and its bindings go with it.
	assert.EqualError(
		t, db.DeleteFieldProfile(tent.Id),
		`field profile "school gym" is the active one; choose another in the settings first`,
	)
	settings.FieldProfile = DefaultFieldProfile
	assert.Nil(t, db.UpdateEventSettings(settings))
	assert.Nil(t, db.CreateLightingFixture(&LightingFixture{ProfileId: tent.Id, Name: "Strip"}))
	assert.Nil(t, db.SaveLightingScenes(&LightingScenes{GameId: "ball-count", ProfileId: tent.Id}))
	assert.Nil(t, db.DeleteFieldProfile(tent.Id))
	bindingSet, _ = db.GetBindingSet("ball-count", "school gym")
	assert.Nil(t, bindingSet)
	fixtures, _ := db.GetLightingFixtures(tent.Id)
	assert.Empty(t, fixtures, "its lighting goes with it too")
	scenes, _ := db.GetAllLightingScenes(tent.Id)
	assert.Empty(t, scenes)
	profiles, _ = db.GetAllFieldProfiles()
	assert.Equal(t, []FieldProfile{{Id: 1, Name: DefaultFieldProfile}}, profiles)
}

func TestBindingSets(t *testing.T) {
	db := setupTestDb(t)

	bindingSet, err := db.GetBindingSet("ball-count", "default")
	assert.Nil(t, err)
	assert.Nil(t, bindingSet)

	assert.Nil(t, db.SaveBindingSet("ball-count", "default", "inputs: []\n"))
	assert.Nil(t, db.SaveBindingSet("ball-count", "tent", "outputs: []\n"))
	assert.Nil(t, db.SaveBindingSet("ball-count", "default", "inputs: [x]\n"))
	assert.Nil(t, db.SaveBindingSet("rebuilt-style", "default", "{}\n"))
	bindingSet, err = db.GetBindingSet("ball-count", "default")
	assert.Nil(t, err)
	if assert.NotNil(t, bindingSet) {
		assert.Equal(t, "inputs: [x]\n", bindingSet.Yaml)
		assert.False(t, bindingSet.UpdatedAt.IsZero())
	}

	bindingSets, err := db.GetAllBindingSets()
	assert.Nil(t, err)
	if assert.Equal(t, 3, len(bindingSets)) {
		assert.Equal(t, "default", bindingSets[0].Profile)
		assert.Equal(t, "tent", bindingSets[1].Profile)
		assert.Equal(t, "rebuilt-style", bindingSets[2].GameId)
	}

	assert.Nil(t, db.DeleteBindingSet("ball-count", "default"))
	assert.Nil(t, db.DeleteBindingSet("ball-count", "default"), "deleting what is not there is not an error")
	bindingSet, _ = db.GetBindingSet("ball-count", "default")
	assert.Nil(t, bindingSet)
	assert.Nil(t, db.TruncateBindingSets())
	bindingSets, _ = db.GetAllBindingSets()
	assert.Empty(t, bindingSets)
}

func TestEventSettingsBeforeDevices(t *testing.T) {
	db := setupTestDb(t)

	// A record written before field devices existed gets the default profile and ports; a port turned off later
	// stays off.
	settings, _ := db.GetEventSettings()
	settings.FieldProfile = ""
	settings.DeviceMqttPort = 0
	settings.DeviceMqttWsPort = 0
	assert.Nil(t, db.UpdateEventSettings(settings))
	settings, _ = db.GetEventSettings()
	assert.Equal(t, DefaultFieldProfile, settings.FieldProfile)
	assert.Equal(t, 1883, settings.DeviceMqttPort)
	assert.Equal(t, 8083, settings.DeviceMqttWsPort)

	settings.DeviceMqttWsPort = 0
	assert.Nil(t, db.UpdateEventSettings(settings))
	settings, _ = db.GetEventSettings()
	assert.Equal(t, 0, settings.DeviceMqttWsPort)
}
