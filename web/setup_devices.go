// The Devices page: approving the devices that turn up, adding devices by hand with a token and a ready-to-paste
// snippet, watching every channel live, testing outputs by hand, and how the transports are doing. The Bindings and
// virtual devices pages follow the same live feed.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/network"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// The largest request the device pages send: a binding set or a manifest.
	maxDevicesPageRequestBytes = 1 << 20
	// Device events a page's feed holds before dropping; the full state it sends every few seconds repairs a gap.
	devicesPageEventBuffer = 2000
	// How often a page's feed sends the full state when something about a device changed, and at least how often
	// otherwise: status reports, which carry the uptime and signal strength, are not events.
	devicesPageChangedPeriod = 250 * time.Millisecond
	devicesPageRefreshPeriod = 2 * time.Second
)

// devicesPageTransport is one transport as the Devices page shows it.
type devicesPageTransport struct {
	device.TransportStatus
	Clients int `json:"clients"` // Devices connected over it now.
}

// devicesPageMqtt is where devices reach the broker, for the snippets shown with a new token.
type devicesPageMqtt struct {
	Hosts  []string `json:"hosts"`  // Addresses of this computer, the likeliest for devices first.
	Port   int      `json:"port"`   // MQTT over TCP; 0 when it is turned off.
	WsPort int      `json:"wsPort"` // MQTT over WebSocket; 0 when it is turned off.
}

// devicesPageState is everything the device pages show, sent by the feed and by GET /setup/devices/state.
type devicesPageState struct {
	Time       int64                  `json:"time"` // The server's clock in UTC milliseconds: strips animate on it.
	Game       string                 `json:"game"`
	Profile    string                 `json:"profile"`
	Devices    []device.Info          `json:"devices"`
	Transports []devicesPageTransport `json:"transports"`
	Faults     []device.Fault         `json:"faults"`
	Drivers    []device.OutputDriver  `json:"drivers"`
	Virtual    []virtualDeviceView    `json:"virtual"`
	Mqtt       devicesPageMqtt        `json:"mqtt"`
}

// Shows the Devices page.
func (web *Web) devicesPageGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.renderDevicesPage(w, "templates/setup_devices.html")
}

// renderDevicesPage renders one of the device pages, which take nothing from the template but the event settings:
// everything else arrives over the feed.
func (web *Web) renderDevicesPage(w http.ResponseWriter, page string) {
	template, err := web.parseFiles(page, "templates/base.html")
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

// Returns everything the device pages show.
func (web *Web) devicesPageStateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	writeJson(w, http.StatusOK, web.devicesPageState(r.Host))
}

func (web *Web) devicesPageState(requestHost string) devicesPageState {
	manager := web.arena.Devices
	state := devicesPageState{
		Time: time.Now().UnixMilli(), Game: manager.Bindings().Game, Profile: manager.Profile(),
		Devices: manager.Devices(), Faults: manager.Faults(), Drivers: manager.OutputDrivers(),
		Mqtt: web.devicesPageMqtt(requestHost),
	}
	clients := make(map[string]int)
	for _, info := range state.Devices {
		for transport := range info.Presence {
			clients[transport]++
		}
	}
	for _, status := range manager.TransportStatuses() {
		state.Transports = append(state.Transports, devicesPageTransport{status, clients[status.Name]})
	}
	state.Virtual = web.virtualDeviceViews(state.Devices)
	return state
}

func (web *Web) devicesPageMqtt(requestHost string) devicesPageMqtt {
	mqtt := devicesPageMqtt{
		Hosts: deviceHostCandidates(requestHost, localIpv4Addresses()),
		Port:  web.arena.EventSettings.DeviceMqttPort, WsPort: web.arena.EventSettings.DeviceMqttWsPort,
	}
	// A running broker says which ports it really has.
	for _, address := range web.arena.DeviceMqtt.Addresses() {
		scheme, hostPort, _ := strings.Cut(address, "://")
		_, portText, err := net.SplitHostPort(hostPort)
		if err != nil {
			continue
		}
		if port, err := strconv.Atoi(portText); err == nil {
			switch scheme {
			case "tcp":
				mqtt.Port = port
			case "ws":
				mqtt.WsPort = port
			}
		}
	}
	return mqtt
}

// deviceHostCandidates lists the addresses a device could reach this computer at, the likeliest first: the field
// address driver stations use, if this computer has it; the address the page was opened with, if it is not this
// computer's loopback; then every other address it has. Devices rarely resolve host names, so names come last.
func deviceHostCandidates(requestHost string, local []string) []string {
	var hosts []string
	add := func(host string) {
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = host
	}
	requestHost = strings.Trim(requestHost, "[]")
	requestIp := net.ParseIP(requestHost)
	if slices.Contains(local, network.ServerIpAddress) {
		add(network.ServerIpAddress)
	}
	if requestIp != nil && !requestIp.IsLoopback() && !requestIp.IsUnspecified() {
		add(requestHost)
	}
	for _, address := range local {
		add(address)
	}
	if requestIp == nil && requestHost != "" && requestHost != "localhost" {
		add(requestHost)
	}
	add(network.ServerIpAddress)
	return hosts
}

// localIpv4Addresses returns this computer's IPv4 addresses, without loopback and link-local ones.
func localIpv4Addresses() []string {
	var addresses []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range ifaceAddresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				addresses = append(addresses, ip.String())
			}
		}
	}
	return addresses
}

// The feed of the device pages: the full state on connecting, every device event as it happens, and the full state
// again soon after anything but an input or output changed, and every few seconds regardless.
func (web *Web) devicesPageWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)
	events := web.arena.Bus.Subscribe([]string{"device"}, devicesPageEventBuffer)
	defer events.Close()

	// The game names the elements, phases and alliances the Bindings page offers.
	go ws.HandleNotifiers(web.arena.GameNotifier)
	go web.feedDevicesPage(ws, events, r.Host)

	// The pages send nothing; reading only notices that the page has gone.
	for {
		if _, _, err = ws.Read(); err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			return
		}
	}
}

func (web *Web) feedDevicesPage(ws *websocket.Websocket, events *bus.Subscription, requestHost string) {
	sendState := func() bool {
		return ws.Write("devices", web.devicesPageState(requestHost)) == nil
	}
	if !sendState() {
		return
	}
	ticker := time.NewTicker(devicesPageChangedPeriod)
	defer ticker.Stop()
	changed, lastSent := false, time.Now()
	for {
		select {
		case event, ok := <-events.C:
			if !ok {
				return
			}
			if ws.Write("deviceEvent", event) != nil {
				return
			}
			// The page applies inputs and outputs itself; anything else is easier to send whole.
			if event.Type != "device.input" && event.Type != "device.output" {
				changed = true
			}
		case now := <-ticker.C:
			if changed || now.Sub(lastSent) >= devicesPageRefreshPeriod {
				if !sendState() {
					return
				}
				changed, lastSent = false, now
			}
		}
	}
}

// decodeDevicesPageRequest reads a JSON request body, refusing fields the request does not have. It writes the error
// and returns false if the body cannot be read.
func decodeDevicesPageRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDevicesPageRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJsonError(w, http.StatusBadRequest, "the request is not valid: "+err.Error())
		return false
	}
	return true
}

// writeDevicesPageResult answers a request that changed a device: the device as it is now, or why nothing changed.
func (web *Web) writeDevicesPageResult(w http.ResponseWriter, id string, err error) {
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	response := map[string]any{"ok": true}
	if info, ok := web.arena.Devices.Device(id); ok {
		response["device"] = info
	}
	writeJson(w, http.StatusOK, response)
}

// Creates a device on an operator's behalf. The token is in this response and nowhere else, ever: only its hash is
// stored.
func (web *Web) deviceCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var request struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !decodeDevicesPageRequest(w, r, &request) {
		return
	}
	id := strings.TrimSpace(request.ID)
	token, err := web.arena.Devices.CreateDevice(id, strings.TrimSpace(request.Name))
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, _ := web.arena.Devices.Device(id)
	writeJson(w, http.StatusOK, map[string]any{"token": token, "device": info, "mqtt": web.devicesPageMqtt(r.Host)})
}

// Gives an approved device a new token, shown once like a new device's.
func (web *Web) deviceTokenHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	token, err := web.arena.Devices.RegenerateToken(id)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, _ := web.arena.Devices.Device(id)
	writeJson(w, http.StatusOK, map[string]any{"token": token, "device": info, "mqtt": web.devicesPageMqtt(r.Host)})
}

// deviceActionHandler returns the handler of one action on one device that needs nothing but the device's id.
func (web *Web) deviceActionHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !web.userIsAdmin(w, r) {
			return
		}
		id := r.PathValue("id")
		manager := web.arena.Devices
		var err error
		switch action {
		case "approve":
			err = manager.Approve(id)
		case "block":
			err = manager.Block(id)
		case "unblock":
			err = manager.Unblock(id)
		case "delete":
			// A virtual device that is deleted must not stay connected inside the FMS.
			if virtual := web.arena.DeviceVirtual.Device(id); virtual != nil {
				virtual.Disconnect()
			}
			err = manager.Delete(id)
		case "identify":
			err = manager.Identify(id)
		case "reboot":
			err = manager.Reboot(id)
		case "clear_faults":
			err = manager.ClearFaults(id)
		default:
			err = fmt.Errorf("unknown device action %q", action)
		}
		web.writeDevicesPageResult(w, id, err)
	}
}

// Changes what the operator says about a device: its name, notes, and whether a match needs it.
func (web *Web) deviceSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var request struct {
		Name     *string `json:"name"`
		Notes    *string `json:"notes"`
		Required *bool   `json:"required"`
	}
	if !decodeDevicesPageRequest(w, r, &request) {
		return
	}
	id := r.PathValue("id")
	manager := web.arena.Devices
	var errs []error
	if request.Name != nil {
		errs = append(errs, manager.SetName(id, strings.TrimSpace(*request.Name)))
	}
	if request.Notes != nil {
		errs = append(errs, manager.SetNotes(id, *request.Notes))
	}
	if request.Required != nil {
		errs = append(errs, manager.SetRequired(id, *request.Required))
	}
	web.writeDevicesPageResult(w, id, errors.Join(errs...))
}

// Clears every fault that can be cleared, and says why the others cannot.
func (web *Web) devicesClearFaultsHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	if err := web.arena.Devices.ClearAllFaults(); err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJson(w, http.StatusOK, map[string]any{"ok": true, "faults": web.arena.Devices.Faults()})
}

// Sends a command to an output by hand. The body is the command, as docs/devices.md writes it.
func (web *Web) deviceOutputTestHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var command device.Command
	if !decodeDevicesPageRequest(w, r, &command) {
		return
	}
	ref := device.ChannelRef{Device: r.PathValue("id"), Channel: r.PathValue("channel")}
	web.writeDevicesPageResult(w, ref.Device, web.arena.Devices.TestOutput(ref, &command))
}

// Ends a test of an output and clears it.
func (web *Web) deviceOutputEndTestHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	ref := device.ChannelRef{Device: r.PathValue("id"), Channel: r.PathValue("channel")}
	web.writeDevicesPageResult(w, ref.Device, web.arena.Devices.EndOutputTest(ref))
}
