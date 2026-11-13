// Model and datastore methods for field devices: the electronics that send inputs to the FMS and show its outputs.
// The device package keeps the live registry; this is what survives a restart.

package model

import (
	"fmt"
	"sort"
	"time"
)

// Approval states of a device.
const (
	DevicePending  = "pending"
	DeviceApproved = "approved"
	DeviceBlocked  = "blocked"
)

// Device is what the FMS remembers about one field device.
type Device struct {
	Id        int `db:"id"`
	DeviceId  string
	Name      string // Given on the Devices page; empty means the name from the manifest.
	State     string // DevicePending, DeviceApproved or DeviceBlocked.
	TokenHash string // Hex SHA-256 of the token: pinned once approved, the last one presented while pending.
	Manifest  string // The last manifest the device sent, as JSON.
	Required  bool   // A match cannot start while a required device is offline.
	Notes     string
	// The last running total of each counter channel. Keeping it across a restart of the FMS means a device that
	// kept running is not counted again from zero.
	CounterTotals map[string]float64
	CreatedAt     time.Time
	LastSeen      time.Time
}

func (database *Database) CreateDevice(device *Device) error {
	existing, err := database.GetDeviceByDeviceId(device.DeviceId)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("a device with id %q already exists", device.DeviceId)
	}
	return database.deviceTable.create(device)
}

func (database *Database) GetDeviceById(id int) (*Device, error) {
	return database.deviceTable.getById(id)
}

// GetDeviceByDeviceId returns the device with the given device id, or nil if there is none. An event has a few
// dozen devices at most, so a scan is cheaper than keeping a second index.
func (database *Database) GetDeviceByDeviceId(deviceId string) (*Device, error) {
	devices, err := database.deviceTable.getAll()
	if err != nil {
		return nil, err
	}
	for i := range devices {
		if devices[i].DeviceId == deviceId {
			return &devices[i], nil
		}
	}
	return nil, nil
}

func (database *Database) UpdateDevice(device *Device) error {
	return database.deviceTable.update(device)
}

func (database *Database) DeleteDevice(id int) error {
	return database.deviceTable.delete(id)
}

func (database *Database) TruncateDevices() error {
	return database.deviceTable.truncate()
}

// GetAllDevices returns every device, ordered by device id.
func (database *Database) GetAllDevices() ([]Device, error) {
	devices, err := database.deviceTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceId < devices[j].DeviceId })
	return devices, nil
}
