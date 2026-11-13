// The Lighting page: fixtures, the scene grid, a live view of every fixture, Test and Identify, and export and
// import. The page edits any field profile; the active one is what the field shows.

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/lighting"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

const (
	// The largest request the Lighting page sends: an imported file.
	maxLightingRequestBytes = 1 << 20
	// How often the page's feed looks for new values of the cells' expressions, and for a new preview.
	lightingCellsPeriod   = 500 * time.Millisecond
	lightingPreviewPeriod = 200 * time.Millisecond
	// How often the feed sends the server's clock, which the page animates its strips on.
	lightingClockPeriod = 5 * time.Second
	// How long the feed waits after a device event before sending the devices, so that a burst is sent once.
	lightingDevicesDelay = 250 * time.Millisecond
)

// lightingProfile is one field profile in the page's selector.
type lightingProfile struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// lightingGame is what the page needs of the active game.
type lightingGame struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Alliances []lightingColour `json:"alliances"`
}

type lightingColour struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// lightingDevice is a device with the output channels lighting can drive.
type lightingDevice struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Online   bool              `json:"online"`
	Channels []lightingChannel `json:"channels"`
}

type lightingChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Pixels int    `json:"pixels,omitempty"`
	// What else drives the channel now in the active profile, if anything: a binding or another component.
	DrivenBy string `json:"drivenBy,omitempty"`
}

// lightingData is everything the page shows about one profile.
type lightingData struct {
	Profiles []lightingProfile `json:"profiles"`
	Profile  string            `json:"profile"`
	Active   bool              `json:"active"` // The profile shown is the one the field uses.
	Game     lightingGame      `json:"game"`
	Moments  []lighting.Moment `json:"moments"`
	Config   *lighting.Config  `json:"config"`
	Problems lighting.Problems `json:"problems"`
	Status   lighting.Status   `json:"status"`
	Devices  []lightingDevice  `json:"devices"`
	Time     int64             `json:"time"` // The server's clock in UTC milliseconds.
}

// Shows the Lighting page.
func (web *Web) lightingGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	template, err := web.parseFiles("templates/setup_lighting.html", "templates/base.html")
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

// lightingProfileName is the profile a request is about: the one it names, or the active one.
func (web *Web) lightingProfileName(r *http.Request) string {
	if profile := r.URL.Query().Get("profile"); profile != "" {
		return profile
	}
	return web.arena.Devices.Profile()
}

// Returns everything the page shows about a profile.
func (web *Web) lightingDataHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	data, err := web.lightingData(web.lightingProfileName(r))
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJson(w, http.StatusOK, data)
}

func (web *Web) lightingData(profile string) (*lightingData, error) {
	game := web.arena.Game
	config, problems, err := web.arena.Lighting.Load(game, profile)
	if err != nil {
		return nil, err
	}
	data := &lightingData{
		Profile: profile, Active: profile == web.arena.Devices.Profile(), Moments: lighting.Moments(game),
		Config: config, Problems: problems, Status: web.arena.Lighting.Status(), Devices: web.lightingDevices(),
		Game: lightingGame{ID: game.ID, Name: game.Name, Alliances: []lightingColour{}}, Time: time.Now().UnixMilli(),
	}
	for _, alliance := range game.Alliances.List {
		data.Game.Alliances = append(data.Game.Alliances, lightingColour{
			ID: alliance.ID, Name: alliance.Name, Color: alliance.Color,
		})
	}
	profiles, err := web.arena.Database.GetAllFieldProfiles()
	if err != nil {
		return nil, err
	}
	for _, record := range profiles {
		data.Profiles = append(data.Profiles, lightingProfile{
			Name: record.Name, Active: record.Name == web.arena.Devices.Profile(),
		})
	}
	return data, nil
}

// lightingDevices lists the devices with output channels lighting can drive, with what else drives each channel.
func (web *Web) lightingDevices() []lightingDevice {
	drivenBy := make(map[device.ChannelRef]string)
	for _, output := range web.arena.Devices.Bindings().Set.Outputs {
		drivenBy[device.ChannelRef{Device: output.Device, Channel: output.Channel}] =
			fmt.Sprintf("output binding %q", output.ID)
	}
	for ref, owner := range web.arena.Devices.Claims() {
		if owner != lighting.Owner && drivenBy[ref] == "" {
			drivenBy[ref] = owner
		}
	}
	devices := []lightingDevice{}
	for _, info := range web.arena.Devices.Devices() {
		if info.BuiltIn || info.Manifest == nil {
			continue
		}
		dev := lightingDevice{
			ID: info.ID, Name: info.Name, State: info.State, Online: info.Online, Channels: []lightingChannel{},
		}
		for _, channel := range info.Manifest.Channels {
			if channel.Dir != device.DirOut || !lighting.IsKind(channel.Type) {
				continue
			}
			dev.Channels = append(dev.Channels, lightingChannel{
				ID: channel.ID, Name: channel.DisplayName(), Type: channel.Type, Pixels: channel.Pixels,
				DrivenBy: drivenBy[device.ChannelRef{Device: info.ID, Channel: channel.ID}],
			})
		}
		if len(dev.Channels) > 0 {
			devices = append(devices, dev)
		}
	}
	return devices
}

// lightingRequest is what the page posts to change the lighting.
type lightingRequest struct {
	Fixture *lighting.Fixture `json:"fixture"`
	Create  bool              `json:"create"`
	Moment  string            `json:"moment"`
	Target  string            `json:"target"`
	Look    *lighting.Look    `json:"look"`
}

func decodeLightingRequest(w http.ResponseWriter, r *http.Request) (*lightingRequest, bool) {
	var request lightingRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLightingRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJsonError(w, http.StatusBadRequest, "the request is not valid: "+err.Error())
		return nil, false
	}
	return &request, true
}

// writeLightingResult answers a change: the problems it has, and why it was refused if it was.
func writeLightingResult(w http.ResponseWriter, problems lighting.Problems, err error) {
	if problems == nil {
		problems = lighting.Problems{}
	}
	if err != nil {
		writeJson(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "problems": problems})
		return
	}
	writeJson(w, http.StatusOK, map[string]any{"problems": problems})
}

// Adds or changes a fixture.
func (web *Web) lightingFixturePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	request, ok := decodeLightingRequest(w, r)
	if !ok {
		return
	}
	if request.Fixture == nil {
		writeJsonError(w, http.StatusBadRequest, "the request has no fixture")
		return
	}
	problems, err := web.arena.Lighting.SaveFixture(
		web.arena.Game, web.lightingProfileName(r), request.Fixture, request.Create,
	)
	writeLightingResult(w, problems, err)
}

// Deletes a fixture.
func (web *Web) lightingFixtureDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	err := web.arena.Lighting.DeleteFixture(web.arena.Game, web.lightingProfileName(r), r.PathValue("id"))
	writeLightingResult(w, nil, err)
}

// Sets or clears one cell of the scene grid.
func (web *Web) lightingCellPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	request, ok := decodeLightingRequest(w, r)
	if !ok {
		return
	}
	problems, err := web.arena.Lighting.SetCell(
		web.arena.Game, web.lightingProfileName(r), request.Moment, request.Target, request.Look,
	)
	writeLightingResult(w, problems, err)
}

// Shows a moment on the hardware for ten seconds.
func (web *Web) lightingTestPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	request, ok := decodeLightingRequest(w, r)
	if !ok {
		return
	}
	writeLightingResult(w, nil, web.arena.Lighting.Test(request.Moment))
}

// Ends the Test in progress.
func (web *Web) lightingTestCancelHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.arena.Lighting.CancelTest()
	writeLightingResult(w, nil, nil)
}

// Asks a fixture's device to identify itself and blinks the fixture.
func (web *Web) lightingIdentifyPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	err := web.arena.Lighting.Identify(r.PathValue("id"))
	writeLightingResult(w, nil, err)
}

// Downloads a profile's fixtures and its scenes for the active game.
func (web *Web) lightingExportHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	profile := web.lightingProfileName(r)
	data, err := web.arena.Lighting.Export(web.arena.Game, profile)
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	filename := fmt.Sprintf("lighting-%s-%s.yaml", profile, web.arena.Game.ID)
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	w.Write(data)
}

// Replaces a profile's fixtures and its scenes for the active game with those of an exported file, sent as the
// request body.
func (web *Web) lightingImportHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLightingRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusRequestEntityTooLarge, "the file is too large")
		return
	}
	problems, err := web.arena.Lighting.Import(web.arena.Game, web.lightingProfileName(r), data)
	writeLightingResult(w, problems, err)
}

// The page's feed. What every fixture shows comes from the engine as it changes, at most twenty times a second,
// and the configuration's reloads with it; the browser animates the strips itself on the server's clock, so
// nothing is sent while a look stays the same. A WebSocket rather than polling, because the live view has to follow
// the hardware within a tick and the page is open for long stretches. Besides, each connection sends the values of
// the cells' expressions, its preview, the devices when they change, and the clock.
func (web *Web) lightingWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)
	engine := web.arena.Lighting
	go ws.HandleNotifiers(engine.LiveNotifier, engine.ConfigNotifier)

	feed := &lightingFeed{web: web, ws: ws, preview: make(chan string, 1), done: make(chan struct{})}
	defer close(feed.done)
	go feed.run(web.arena.Bus.Subscribe(
		[]string{"device.online", "device.offline", "device.pending", "device.manifest"}, 100,
	))

	for {
		messageType, data, err := ws.Read()
		if err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			return
		}
		switch messageType {
		case "preview":
			moment, _ := data.(string)
			select {
			case <-feed.preview:
			default:
			}
			feed.preview <- moment
		default:
			writeWebsocketError(ws, fmt.Sprintf("Invalid message type '%s'.", messageType))
		}
	}
}

// lightingFeed sends one page what is particular to it, from a goroutine of its own.
type lightingFeed struct {
	web     *Web
	ws      *websocket.Websocket
	preview chan string // The moment the page previews; empty for the live view.
	done    chan struct{}
}

func (feed *lightingFeed) run(events *bus.Subscription) {
	defer events.Close()
	engine := feed.web.arena.Lighting
	cells := time.NewTicker(lightingCellsPeriod)
	defer cells.Stop()
	previews := time.NewTicker(lightingPreviewPeriod)
	defer previews.Stop()
	clock := time.NewTicker(lightingClockPeriod)
	defer clock.Stop()
	var devicesDue <-chan time.Time
	var lastCells, lastPreview []byte
	moment := ""

	send := func(messageType string, data any, last *[]byte) bool {
		encoded, err := json.Marshal(data)
		if err != nil {
			log.Printf("Lighting page: %v", err)
			return true
		}
		if last != nil {
			if bytes.Equal(encoded, *last) {
				return true
			}
			*last = encoded
		}
		return feed.ws.Write(messageType, json.RawMessage(encoded)) == nil
	}
	sendPreview := func() bool {
		if moment == "" {
			return true
		}
		commands, err := engine.Preview(moment)
		if err != nil {
			return send("lightingPreview", map[string]any{"moment": moment, "error": err.Error()}, &lastPreview)
		}
		return send("lightingPreview", map[string]any{"moment": moment, "fixtures": commands}, &lastPreview)
	}

	ok := send("lightingClock", map[string]int64{"time": time.Now().UnixMilli()}, nil) &&
		send("lightingCells", engine.CellValues(), &lastCells)
	for ok {
		select {
		case <-feed.done:
			return
		case moment = <-feed.preview:
			lastPreview = nil
			ok = sendPreview()
		case <-previews.C:
			ok = sendPreview()
		case <-cells.C:
			ok = send("lightingCells", engine.CellValues(), &lastCells)
		case <-clock.C:
			ok = send("lightingClock", map[string]int64{"time": time.Now().UnixMilli()}, nil)
		case _, open := <-events.C:
			if !open {
				return
			}
			if devicesDue == nil {
				devicesDue = time.After(lightingDevicesDelay)
			}
		case <-devicesDue:
			devicesDue = nil
			ok = send("lightingDevices", feed.web.lightingDevices(), nil)
		}
	}
}
