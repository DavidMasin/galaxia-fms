// The virtual devices page: devices that live inside the FMS, made from a template or a pasted manifest, whose
// inputs are clicked and whose outputs are drawn. The FMS owns them (Arena.DeviceVirtual), so they keep running when
// the page is closed; like any device, a new one waits for approval.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"net/http"
	"strings"
)

// virtualDeviceSdk is the sdk a virtual device made on the page announces. It is how the page recognises its devices
// once they are disconnected, to offer connecting them again.
const virtualDeviceSdk = "galaxia-virtual"

// virtualDeviceView is what the page shows of one virtual device besides what the registry knows of it.
type virtualDeviceView struct {
	ID        string                     `json:"id"`
	Connected bool                       `json:"connected"`
	Beating   bool                       `json:"beating"` // Sending heartbeats.
	Manifest  *device.Manifest           `json:"manifest"`
	Values    map[string]any             `json:"values"`  // Its inputs, as the device holds them.
	Outputs   map[string]*device.Command `json:"outputs"` // What the device was last told to show.
	Commands  []device.DeviceCommand     `json:"commands"`
	Field     *device.FieldState         `json:"field"`
}

// Shows the virtual devices page.
func (web *Web) virtualDevicesGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.renderDevicesPage(w, "templates/virtual_devices.html")
}

// virtualDeviceViews lists the connected virtual devices, and the disconnected ones the page made, which can be
// connected again.
func (web *Web) virtualDeviceViews(infos []device.Info) []virtualDeviceView {
	views := []virtualDeviceView{}
	connected := make(map[string]bool)
	for _, virtual := range web.arena.DeviceVirtual.Devices() {
		connected[virtual.ID()] = true
		views = append(views, virtualDeviceView{
			ID: virtual.ID(), Connected: virtual.Connected(), Beating: virtual.Beating(), Manifest: virtual.Manifest(),
			Values: virtual.Values(), Outputs: virtual.Outputs(), Commands: virtual.Commands(), Field: virtual.Field(),
		})
	}
	for _, info := range infos {
		if !connected[info.ID] && info.Manifest != nil && info.Manifest.SDK == virtualDeviceSdk {
			views = append(views, virtualDeviceView{
				ID: info.ID, Manifest: info.Manifest, Values: map[string]any{}, Outputs: map[string]*device.Command{},
				Commands: []device.DeviceCommand{},
			})
		}
	}
	return views
}

// Makes a virtual device from a manifest and connects it. It is pending until approved, unless the registry already
// knows and approved it.
func (web *Web) virtualDeviceCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var request struct {
		Manifest *device.Manifest `json:"manifest"`
	}
	if !decodeDevicesPageRequest(w, r, &request) {
		return
	}
	manifest := request.Manifest
	if manifest == nil {
		writeJsonError(w, http.StatusBadRequest, "a virtual device needs a manifest")
		return
	}
	manifest.Proto = device.ProtocolVersion
	manifest.ID = strings.TrimSpace(manifest.ID)
	if info, ok := web.arena.Devices.Device(manifest.ID); ok && !web.isVirtualDevice(info) {
		writeJsonError(w, http.StatusBadRequest, fmt.Sprintf(
			"there is already a device %q that is not virtual; pick another id, or delete that device first",
			manifest.ID,
		))
		return
	}
	manifest.SDK = virtualDeviceSdk
	if manifest.Board == "" {
		manifest.Board = "virtual"
	}
	if err := web.connectVirtualDevice(manifest); err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	web.writeDevicesPageResult(w, manifest.ID, nil)
}

func (web *Web) isVirtualDevice(info device.Info) bool {
	_, connected := info.Presence[device.VirtualTransportName]
	return connected || (info.Manifest != nil && info.Manifest.SDK == virtualDeviceSdk)
}

// connectVirtualDevice connects a virtual device and starts it as firmware starts: heartbeats if it has a safety
// channel, and every counter reported at 0 so that the FMS counts from there.
func (web *Web) connectVirtualDevice(manifest *device.Manifest) error {
	virtual, err := web.arena.DeviceVirtual.Connect(manifest)
	if err != nil {
		return err
	}
	if manifest.HasSafety() {
		virtual.StartHeartbeat(0)
	}
	if web.arena.Devices.Access(manifest.ID) == device.AccessApproved {
		for _, channel := range manifest.Channels {
			if channel.Dir == device.DirIn && channel.Type == device.TypeCounter {
				_ = virtual.SetInput(channel.ID, 0)
			}
		}
	}
	return nil
}

// virtualDeviceHandler returns the handler of one action on a virtual device.
func (web *Web) virtualDeviceHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !web.userIsAdmin(w, r) {
			return
		}
		id := r.PathValue("id")
		virtual := web.arena.DeviceVirtual.Device(id)
		var err error
		switch action {
		case "input":
			err = web.virtualDeviceInput(w, r, virtual, id)
		case "heartbeat":
			var request struct {
				On bool `json:"on"`
			}
			if !decodeDevicesPageRequest(w, r, &request) {
				return
			}
			if err = virtualDeviceConnected(virtual, id); err == nil {
				if request.On {
					virtual.StartHeartbeat(0)
				} else {
					virtual.StopHeartbeat()
				}
			}
		case "disconnect":
			if err = virtualDeviceConnected(virtual, id); err == nil {
				virtual.Disconnect()
			}
		case "connect":
			err = web.reconnectVirtualDevice(virtual, id)
		case "remove":
			if virtual != nil {
				virtual.Disconnect()
			}
			if info, ok := web.arena.Devices.Device(id); !ok || !web.isVirtualDevice(info) {
				err = fmt.Errorf("there is no virtual device %q", id)
			} else {
				err = web.arena.Devices.Delete(id)
			}
		default:
			err = fmt.Errorf("unknown virtual device action %q", action)
		}
		if err == errVirtualRequestAnswered {
			return
		}
		web.writeDevicesPageResult(w, id, err)
	}
}

// errVirtualRequestAnswered says that a request was answered already, with an error about the request itself.
var errVirtualRequestAnswered = fmt.Errorf("the request was answered")

func virtualDeviceConnected(virtual *device.VirtualDevice, id string) error {
	if virtual == nil {
		return fmt.Errorf("virtual device %q is not connected", id)
	}
	return nil
}

// virtualDeviceInput sets one input of a virtual device: {"channel": "...", "value": ...} reports a value, and
// {"channel": "...", "add": n} adds to a counter's running total, as a sensor counting would.
func (web *Web) virtualDeviceInput(
	w http.ResponseWriter, r *http.Request, virtual *device.VirtualDevice, id string,
) error {
	var request struct {
		Channel string   `json:"channel"`
		Value   any      `json:"value"`
		Add     *float64 `json:"add"`
	}
	if !decodeDevicesPageRequest(w, r, &request) {
		return errVirtualRequestAnswered
	}
	if err := virtualDeviceConnected(virtual, id); err != nil {
		return err
	}
	if request.Add == nil {
		return virtual.SetInput(request.Channel, request.Value)
	}
	channel := virtual.Manifest().Channel(request.Channel)
	if channel == nil || channel.Dir != device.DirIn || channel.Type != device.TypeCounter {
		return fmt.Errorf("%q is not a counter input of virtual device %q", request.Channel, id)
	}
	total, _ := virtual.Values()[request.Channel].(float64)
	// A device sends its totals again once it is let in; until the FMS has heard one, the first only sets where
	// counting starts, so the total so far goes first and the addition counts.
	if info, ok := web.arena.Devices.Device(id); ok {
		if _, heard := info.Inputs[request.Channel]; !heard {
			if err := virtual.SetInput(request.Channel, total); err != nil {
				return err
			}
		}
	}
	return virtual.SetInput(request.Channel, max(0, total+*request.Add))
}

// reconnectVirtualDevice connects a virtual device the page made again, with the manifest it had: a device that was
// switched off and on.
func (web *Web) reconnectVirtualDevice(virtual *device.VirtualDevice, id string) error {
	if virtual != nil {
		return fmt.Errorf("virtual device %q is connected already", id)
	}
	info, ok := web.arena.Devices.Device(id)
	if !ok || info.Manifest == nil || info.Manifest.SDK != virtualDeviceSdk {
		return fmt.Errorf("there is no virtual device %q to connect", id)
	}
	return web.connectVirtualDevice(info.Manifest)
}
