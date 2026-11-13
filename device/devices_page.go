// What the virtual devices page reads of the virtual devices it drives: which are connected, what each says it is,
// what its inputs read on the device's side, and whether it sends heartbeats.

package device

import (
	"sort"
)

// Devices returns the connected virtual devices, ordered by id.
func (t *VirtualTransport) Devices() []*VirtualDevice {
	t.mu.Lock()
	defer t.mu.Unlock()
	devices := make([]*VirtualDevice, 0, len(t.devices))
	for _, dev := range t.devices {
		devices = append(devices, dev)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].id < devices[j].id })
	return devices
}

// Manifest returns a copy of the manifest the device connected with.
func (d *VirtualDevice) Manifest() *Manifest {
	manifest := *d.manifest
	manifest.Channels = make([]*Channel, len(d.manifest.Channels))
	for i, channel := range d.manifest.Channels {
		copied := *channel
		manifest.Channels[i] = &copied
	}
	return &manifest
}

// Values returns the value last set on each input, as the device itself holds them: a counter's running total since
// it connected, a safety channel's state. They are kept even when the FMS refuses the input (while the device is
// pending, say), as a real device keeps counting.
func (d *VirtualDevice) Values() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	values := make(map[string]any, len(d.values))
	for channel, value := range d.values {
		values[channel] = value
	}
	return values
}

// Beating reports whether the device is sending heartbeats (StartHeartbeat without StopHeartbeat since).
func (d *VirtualDevice) Beating() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopBeating != nil
}
