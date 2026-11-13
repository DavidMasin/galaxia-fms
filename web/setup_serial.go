// The page that chooses the serial ports field devices are plugged into, and shows how each open one is doing.

package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// serialPortView is one port as the serial page shows it: a port of the computer, a port the settings open, or
// both. A port that is chosen but unplugged is still listed, so that it can be closed.
type serialPortView struct {
	device.SerialPortInfo
	Present bool                    `json:"present"` // The computer has the port now.
	Chosen  bool                    `json:"chosen"`  // The settings open it for devices.
	State   *device.SerialPortState `json:"state,omitempty"`
}

// serialPortsView is what the serial page shows.
type serialPortsView struct {
	Running   bool             `json:"running"`
	Ports     []serialPortView `json:"ports"`
	ListError string           `json:"listError,omitempty"` // Why the computer's ports could not be listed.
	Error     string           `json:"error,omitempty"`     // Why a change was refused.
}

// Shows the serial port page.
func (web *Web) serialSetupGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	template, err := web.parseFiles("templates/setup_serial.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
	}{web.arena.EventSettings}
	if err = template.ExecuteTemplate(w, "base", data); err != nil {
		handleWebErr(w, err)
	}
}

// Returns the ports of the computer and the state of the chosen ones, as JSON.
func (web *Web) serialPortsGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	writeJson(w, http.StatusOK, web.serialPortsView())
}

// Opens or closes a port for devices: the body is {"port": name, "open": true or false}. The choice is stored in
// the settings and taken into use at once, and the page's view is returned.
func (web *Web) serialPortsPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var request struct {
		Port string `json:"port"`
		Open bool   `json:"open"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJsonError(w, http.StatusBadRequest, "the request is not valid JSON: "+err.Error())
		return
	}
	request.Port = strings.TrimSpace(request.Port)
	if request.Port == "" || strings.Contains(request.Port, ",") {
		writeJsonError(w, http.StatusBadRequest, "name a serial port, such as COM3 or /dev/ttyUSB0")
		return
	}
	if !settingsSaveAllowed(web.arena.MatchState) {
		// Closing a port takes its devices offline, which would fault a stop in the middle of a match.
		view := web.serialPortsView()
		view.Error = settingsLockedMessage
		writeJson(w, http.StatusConflict, view)
		return
	}

	ports := slices.DeleteFunc(web.arena.EventSettings.SerialPorts(), func(port string) bool {
		return port == request.Port
	})
	if request.Open {
		ports = append(ports, request.Port)
	}
	updated := *web.arena.EventSettings
	updated.SetSerialPorts(ports)
	if err := web.arena.Database.UpdateEventSettings(&updated); err != nil {
		handleWebErr(w, err)
		return
	}
	web.arena.EventSettings.DeviceSerialPorts = updated.DeviceSerialPorts
	web.arena.ConfigureDeviceSerialPorts()
	writeJson(w, http.StatusOK, web.serialPortsView())
}

// serialPortsView lists the computer's ports and the chosen ones together, by name.
func (web *Web) serialPortsView() serialPortsView {
	view := serialPortsView{Ports: []serialPortView{}}
	transport := web.arena.DeviceSerial()
	if transport == nil {
		view.ListError = "the serial device transport is not set up"
		return view
	}
	view.Running = transport.Status().Running
	listed, err := transport.Ports()
	if err != nil {
		view.ListError = err.Error()
	}
	index := make(map[string]int)
	for _, info := range listed {
		index[info.Name] = len(view.Ports)
		view.Ports = append(view.Ports, serialPortView{SerialPortInfo: info, Present: true})
	}
	states := make(map[string]device.SerialPortState)
	for _, state := range transport.PortStates() {
		states[state.Name] = state
	}
	for _, name := range web.arena.EventSettings.SerialPorts() {
		i, ok := index[name]
		if !ok {
			i = len(view.Ports)
			view.Ports = append(view.Ports, serialPortView{SerialPortInfo: device.SerialPortInfo{Name: name}})
		}
		view.Ports[i].Chosen = true
		if state, ok := states[name]; ok {
			view.Ports[i].State = &state
		}
	}
	sort.Slice(view.Ports, func(i, j int) bool { return view.Ports[i].Name < view.Ports[j].Name })
	return view
}
