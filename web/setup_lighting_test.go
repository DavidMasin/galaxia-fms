package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/lighting"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"testing"
	"time"
)

// setupLightingWeb is a web server on the ball-count game with the device transports running (the MQTT broker off)
// and a virtual device with a ten-LED strip and a relay.
func setupLightingWeb(t *testing.T) (*Web, *device.VirtualDevice) {
	t.Helper()
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")
	require.Nil(t, web.arena.DeviceMqtt.Configure(device.MqttConfig{}))
	require.Nil(t, web.arena.Devices.Start())
	t.Cleanup(web.arena.Devices.Stop)
	t.Cleanup(func() {
		// A reload the engine started by itself must be done before the database closes.
		deadline := time.Now().Add(5 * time.Second)
		for web.arena.Lighting.Status().Reloading && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	})
	channels := []*device.Channel{
		{ID: "strip", Dir: device.DirOut, Type: device.TypeLEDStrip, Name: "Goal strip", Pixels: 10},
		{ID: "horn", Dir: device.DirOut, Type: device.TypeRelay},
		{ID: "beam", Dir: device.DirIn, Type: device.TypeDigital},
	}
	goal, err := web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "goal", Name: "Goal", Channels: channels})
	require.Nil(t, err)
	require.Nil(t, web.arena.Devices.Approve("goal"))
	return web, goal
}

// postLighting posts a JSON request to the Lighting page and decodes the answer.
func postLighting(t *testing.T, web *Web, path string, body any) (int, map[string]any) {
	t.Helper()
	text := ""
	if body != nil {
		data, err := json.Marshal(body)
		require.Nil(t, err)
		text = string(data)
	}
	recorder := web.requestWithBody("POST", path, text)
	var decoded map[string]any
	require.Nil(t, json.Unmarshal(recorder.Body.Bytes(), &decoded), recorder.Body.String())
	return recorder.Code, decoded
}

func getLightingData(t *testing.T, web *Web, profile string) map[string]any {
	t.Helper()
	path := "/setup/lighting/data"
	if profile != "" {
		path += "?profile=" + profile
	}
	recorder := web.getHttpResponse(path)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	return decodeJson(t, recorder)
}

func TestLightingPage(t *testing.T) {
	web := setupTestWeb(t)
	recorder := web.getHttpResponse("/setup/lighting")
	assert.Equal(t, 200, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "Lighting - Untitled Event - Galaxia FMS")
	assert.Contains(t, body, "/static/js/led_patterns.js")
	assert.Contains(t, body, "/static/js/setup_lighting.js")
	assert.Contains(t, body, "/static/css/setup_lighting.css")

	// Admin only.
	web.arena.EventSettings.AdminPassword = "secret"
	recorder = web.getHttpResponse("/setup/lighting")
	assert.Equal(t, 307, recorder.Code)
	recorder = web.getHttpResponse("/setup/lighting/data")
	assert.Equal(t, 307, recorder.Code)
}

func TestLightingFixturesAndCells(t *testing.T) {
	web, goal := setupLightingWeb(t)

	data := getLightingData(t, web, "")
	assert.Equal(t, "default", data["profile"])
	assert.Equal(t, true, data["active"])
	assert.Equal(t, "ball-count", data["game"].(map[string]any)["id"])
	assert.Equal(t, []any{map[string]any{"name": "default", "active": true}}, data["profiles"])
	moments := data["moments"].([]any)
	assert.Equal(t, "estop", moments[0].(map[string]any)["id"])
	assert.Equal(t, "idle", moments[len(moments)-1].(map[string]any)["id"])
	devices := data["devices"].([]any)
	if assert.Equal(t, 1, len(devices)) {
		channels := devices[0].(map[string]any)["channels"].([]any)
		assert.Equal(t, 2, len(channels), "only the outputs lighting can drive")
		assert.Equal(t, map[string]any{"id": "strip", "name": "Goal strip", "type": "led_strip", "pixels": 10.0},
			channels[0])
	}

	// A fixture with errors is refused, and says why.
	code, response := postLighting(t, web, "/setup/lighting/fixtures", map[string]any{
		"create": true, "fixture": map[string]any{"id": "goal", "device": "goal", "channel": "strip", "kind": "relay"},
	})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, response["error"], `channel "strip" of device "goal" is a led_strip, not a relay`)
	code, response = postLighting(t, web, "/setup/lighting/fixtures", map[string]any{"create": true, "colour": 1})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, response["error"], "unknown field")

	code, response = postLighting(t, web, "/setup/lighting/fixtures", map[string]any{
		"create": true, "fixture": map[string]any{
			"id": "goal", "name": "Red goal", "device": "goal", "channel": "strip", "kind": "led_strip",
			"alliance": "red", "brightness": 0.5,
			"segments": []any{map[string]any{"id": "left", "start": 0, "count": 5, "alliance": "blue"}},
		},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	data = getLightingData(t, web, "")
	fixtures := data["config"].(map[string]any)["fixtures"].([]any)
	if assert.Equal(t, 1, len(fixtures)) {
		assert.Equal(t, "Red goal", fixtures[0].(map[string]any)["name"])
		assert.Equal(t, 0.5, fixtures[0].(map[string]any)["brightness"])
	}
	scenes := data["config"].(map[string]any)["scenes"].(map[string]any)
	assert.Equal(t, map[string]any{"p": "solid", "c": []any{"#ffffff"}}, scenes["idle"].(map[string]any)["goal"])
	assert.Equal(t, map[string]any{"goal": true}, data["status"].(map[string]any)["claimed"])

	// A cell with errors is refused with its problems; a good one is saved and shown on the hardware.
	code, response = postLighting(t, web, "/setup/lighting/cell", map[string]any{
		"moment": "idle", "target": "goal/left", "look": map[string]any{"p": "progress"},
	})
	assert.Equal(t, http.StatusBadRequest, code)
	problems := response["problems"].([]any)
	if assert.Equal(t, 1, len(problems)) {
		assert.Equal(t, "goal/left", problems[0].(map[string]any)["target"])
		assert.Contains(t, problems[0].(map[string]any)["message"], "progress needs v")
	}
	code, response = postLighting(t, web, "/setup/lighting/cell", map[string]any{
		"moment": "idle", "target": "goal/left", "look": map[string]any{"p": "solid", "c": []string{"alliance"}},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}
	assert.Equal(t, &device.Command{Br: floatPointer(0.5), Seg: []device.Segment{
		{S: 0, N: 5, P: "solid", C: []string{"#1565c0"}}, {S: 5, N: 5, P: "solid", C: []string{"#ffffff"}},
	}}, goal.Output("strip"))
	code, _ = postLighting(t, web, "/setup/lighting/cell", map[string]any{"moment": "idle", "target": "goal/left"})
	assert.Equal(t, http.StatusOK, code, "a cell without a look is cleared")
	scenes = getLightingData(t, web, "")["config"].(map[string]any)["scenes"].(map[string]any)
	_, ok := scenes["idle"].(map[string]any)["goal/left"]
	assert.False(t, ok)

	// Test and Identify.
	code, _ = postLighting(t, web, "/setup/lighting/test", map[string]any{"moment": "estop"})
	assert.Equal(t, http.StatusOK, code)
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}
	assert.Equal(t, "estop", web.arena.Lighting.Live().Moment)
	assert.Equal(t, "blink", goal.Output("strip").P)
	code, _ = postLighting(t, web, "/setup/lighting/test/cancel", nil)
	assert.Equal(t, http.StatusOK, code)
	code, response = postLighting(t, web, "/setup/lighting/test", map[string]any{"moment": "nothing"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, response["error"], "is not a moment")
	code, _ = postLighting(t, web, "/setup/lighting/fixtures/goal/identify", nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []device.DeviceCommand{{Do: "identify"}}, goal.Commands())

	// Another profile is edited without touching the field.
	_, err := web.arena.Database.GetAllFieldProfiles()
	require.Nil(t, err)
	code, response = postLighting(t, web, "/setup/lighting/fixtures?profile=default", map[string]any{
		"create": true, "fixture": map[string]any{"id": "horn", "device": "goal", "channel": "horn", "kind": "relay"},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	recorder := web.getHttpResponse("/setup/lighting/data?profile=nothing")
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `there is no field profile \"nothing\"`)

	code, _ = postLighting(t, web, "/setup/lighting/fixtures/horn/delete", nil)
	assert.Equal(t, http.StatusOK, code)
	code, response = postLighting(t, web, "/setup/lighting/fixtures/horn/delete", nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, `there is no fixture "horn"`, response["error"])
}

func TestLightingExportAndImport(t *testing.T) {
	web, _ := setupLightingWeb(t)
	code, response := postLighting(t, web, "/setup/lighting/fixtures", map[string]any{
		"create": true, "fixture": map[string]any{"id": "goal", "device": "goal", "channel": "strip", "kind": "led_strip"},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)

	recorder := web.getHttpResponse("/setup/lighting/export")
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/yaml", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "attachment; filename*=UTF-8''lighting-default-ball-count.yaml",
		recorder.Header().Get("Content-Disposition"))
	exported := recorder.Body.String()
	config, err := lighting.ParseConfig([]byte(exported))
	require.Nil(t, err)
	assert.Equal(t, "goal", config.Fixtures[0].ID)
	assert.Equal(t, "#ffffff", config.Scenes["idle"]["goal"].C[0])

	edited := strings.Replace(exported, "'#ffffff'", "'#123456'", 1)
	assert.NotEqual(t, exported, edited)
	recorder = web.requestWithBody("POST", "/setup/lighting/import", edited)
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	config, _, err = web.arena.Lighting.Load(web.arena.Game, "default")
	require.Nil(t, err)
	assert.Equal(t, "#123456", config.Scenes["idle"]["goal"].C[0])

	recorder = web.requestWithBody("POST", "/setup/lighting/import", "fixtures: [{ id: Bad }]")
	assert.Equal(t, 400, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "must be 1 to 40 lowercase")
}

func TestLightingWebsocket(t *testing.T) {
	web, _ := setupLightingWeb(t)
	code, response := postLighting(t, web, "/setup/lighting/fixtures", map[string]any{
		"create": true, "fixture": map[string]any{"id": "goal", "device": "goal", "channel": "strip", "kind": "led_strip"},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	code, response = postLighting(t, web, "/setup/lighting/cell", map[string]any{
		"moment": "phase.teleop", "target": "goal",
		"look": map[string]any{"p": "progress", "c": []string{"#00ff00"}, "v": "phase_remaining / phase_duration"},
	})
	require.Equal(t, http.StatusOK, code, "%v", response)
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/setup/lighting/websocket", nil)
	require.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)

	// What the engine sends and what the page's own feed sends come from different goroutines, in any order.
	first := readLightingMessages(t, ws, "lightingLive", "lightingConfig", "lightingClock", "lightingCells")
	live := first["lightingLive"].(map[string]any)
	assert.Equal(t, "idle", live["moment"])
	assert.Equal(t, map[string]any{"p": "solid", "c": []any{"#ffffff"}}, live["fixtures"].(map[string]any)["goal"])
	assert.Equal(t, "default", first["lightingConfig"].(map[string]any)["profile"])
	assert.InDelta(t, float64(time.Now().UnixMilli()), first["lightingClock"].(map[string]any)["time"], 5000)
	cells := first["lightingCells"].(map[string]any)
	assert.Equal(t, false, cells["phase.teleop"].(map[string]any)["goal"].(map[string]any)["when"],
		"outside the match the fill cannot be worked out")

	require.Nil(t, ws.Write("preview", "estop"))
	preview := readWebsocketUntil(t, ws, "lightingPreview").(map[string]any)
	assert.Equal(t, "estop", preview["moment"])
	assert.Equal(t, map[string]any{"p": "blink", "c": []any{"#ff0000"}},
		preview["fixtures"].(map[string]any)["goal"])

	// A change to the hardware is pushed as it happens.
	setAllStationsBypassed(web)
	require.Nil(t, web.arena.StartMatch())
	for i := 0; i < 5; i++ {
		web.arena.Update()
	}
	for {
		live = readWebsocketUntil(t, ws, "lightingLive").(map[string]any)
		if live["moment"] == "phase.auto" {
			break
		}
	}
	assert.Equal(t, map[string]any{"p": "solid", "c": []any{"#ffffff"}}, live["fixtures"].(map[string]any)["goal"],
		"a fixture without an alliance shows the alliance colour as white")

	require.Nil(t, ws.Write("nothing", nil))
	assert.Contains(t, readWebsocketUntil(t, ws, "error"), "Invalid message type")
}

// readLightingMessages reads websocket messages until one of each of the given types has arrived, and returns the
// first of each.
func readLightingMessages(t *testing.T, ws *websocket.Websocket, types ...string) map[string]any {
	t.Helper()
	messages := make(map[string]any)
	for i := 0; i < 50 && len(messages) < len(types); i++ {
		messageType, message, err := ws.ReadWithTimeout(2 * time.Second)
		require.Nil(t, err)
		for _, wanted := range types {
			if _, seen := messages[wanted]; wanted == messageType && !seen {
				messages[messageType] = message
			}
		}
	}
	require.Equal(t, len(types), len(messages), "received %v", messages)
	return messages
}

func floatPointer(value float64) *float64 {
	return &value
}
