package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/network"
	"github.com/DavidMasin/galaxia-fms/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strconv"
	"strings"
	"testing"
)

// startTestDevices starts the arena's device transports as Run would, with the MQTT broker off: the ports in the
// settings may be in use on the computer running the tests.
func startTestDevices(t *testing.T, web *Web) {
	t.Helper()
	require.Nil(t, web.arena.DeviceMqtt.Configure(device.MqttConfig{}))
	require.Nil(t, web.arena.Devices.Start())
	t.Cleanup(web.arena.Devices.Stop)
}

// postDevicesPage sends a JSON request to one of the device pages and decodes the answer.
func postDevicesPage(t *testing.T, web *Web, path string, body any) (int, map[string]any) {
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

func getDevicesPageState(t *testing.T, web *Web) devicesPageState {
	t.Helper()
	recorder := web.getHttpResponse("/setup/devices/state")
	require.Equal(t, 200, recorder.Code)
	var state devicesPageState
	require.Nil(t, json.Unmarshal(recorder.Body.Bytes(), &state))
	return state
}

func TestDevicesPages(t *testing.T) {
	web := setupTestWeb(t)
	for path, script := range map[string]string{
		"/setup/devices": "setup_devices.js", "/setup/bindings": "setup_bindings.js",
		"/setup/devices/virtual": "virtual_devices.js",
	} {
		recorder := web.getHttpResponse(path)
		assert.Equal(t, 200, recorder.Code, path)
		assert.Contains(t, recorder.Body.String(), "/static/js/"+script, path)
		assert.Contains(t, recorder.Body.String(), "/static/css/devices.css", path)
	}

	// The pages and their API are for the administrator.
	web.arena.EventSettings.AdminPassword = "secret"
	for _, path := range []string{"/setup/devices", "/setup/devices/state", "/setup/bindings/state"} {
		assert.Equal(t, 307, web.getHttpResponse(path).Code, path)
	}
	recorder := web.requestWithBody("POST", "/setup/devices/create", `{"id":"x"}`)
	assert.Equal(t, 307, recorder.Code)
	_, ok := web.arena.Devices.Device("x")
	assert.False(t, ok)
}

// A device made on the page is approved with a token that is shown once; the FMS keeps only its hash.
func TestDevicesCreateShowsTheTokenOnce(t *testing.T) {
	web := setupTestWeb(t)
	code, response := postDevicesPage(t, web, "/setup/devices/create", map[string]string{
		"id": "red-goal", "name": " Red goal ",
	})
	require.Equal(t, 200, code, response)
	token := response["token"].(string)
	assert.Equal(t, 43, len(token))
	created := response["device"].(map[string]any)
	assert.Equal(t, "Red goal", created["name"])
	assert.Equal(t, "approved", created["state"])
	assert.Equal(t, true, created["hasToken"])
	assert.NotNil(t, response["mqtt"].(map[string]any)["hosts"])

	state := web.getHttpResponse("/setup/devices/state").Body.String()
	assert.NotContains(t, state, token)
	record, err := web.arena.Database.GetDeviceByDeviceId("red-goal")
	require.Nil(t, err)
	assert.Equal(t, device.HashToken(token), record.TokenHash)

	// A new token replaces the old one, and is shown once too.
	code, response = postDevicesPage(t, web, "/setup/devices/red-goal/token", nil)
	require.Equal(t, 200, code)
	newToken := response["token"].(string)
	assert.NotEqual(t, token, newToken)
	record, _ = web.arena.Database.GetDeviceByDeviceId("red-goal")
	assert.Equal(t, device.HashToken(newToken), record.TokenHash)

	for _, request := range []map[string]string{{"id": "Red Goal"}, {"id": "plc"}, {"id": "red-goal"}} {
		code, response = postDevicesPage(t, web, "/setup/devices/create", request)
		assert.Equal(t, 400, code, request)
		assert.NotEmpty(t, response["error"])
		assert.Nil(t, response["token"])
	}
	recorder := web.requestWithBody("POST", "/setup/devices/create", `{"id":"a","colour":"red"}`)
	assert.Equal(t, 400, recorder.Code, "unknown fields are refused")
}

func TestDevicesApproveAndBlock(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	box, err := web.arena.DeviceVirtual.Connect(&device.Manifest{
		Proto: 1, ID: "box", Name: "Box", Channels: []*device.Channel{
			{ID: "button", Dir: device.DirIn, Type: device.TypeDigital},
			{ID: "lamp", Dir: device.DirOut, Type: device.TypeDigital},
		},
	})
	require.Nil(t, err)

	// It is pending: its inputs are refused until it is approved.
	state := getDevicesPageState(t, web)
	if assert.Equal(t, 1, len(state.Devices)) {
		assert.Equal(t, "pending", state.Devices[0].State)
		assert.True(t, state.Devices[0].Online)
	}
	assert.Error(t, box.SetInput("button", 1))
	code, response := postDevicesPage(t, web, "/setup/devices/box/approve", nil)
	require.Equal(t, 200, code, response)
	assert.Equal(t, "approved", response["device"].(map[string]any)["state"])
	require.Nil(t, box.SetInput("button", 1))
	info, _ := web.arena.Devices.Device("box")
	assert.Equal(t, 1.0, info.Inputs["button"].Value)

	code, response = postDevicesPage(t, web, "/setup/devices/box/approve", nil)
	assert.Equal(t, 400, code)
	assert.Equal(t, `device "box" is already approved`, response["error"])

	// Settings.
	code, _ = postDevicesPage(t, web, "/setup/devices/box/settings", map[string]any{
		"name": "Scoring table", "notes": "Under the red driver station", "required": true,
	})
	require.Equal(t, 200, code)
	info, _ = web.arena.Devices.Device("box")
	assert.Equal(t, "Scoring table", info.Name)
	assert.Equal(t, "Under the red driver station", info.Notes)
	assert.True(t, info.Required)
	code, _ = postDevicesPage(t, web, "/setup/devices/box/settings", map[string]any{"required": false})
	require.Equal(t, 200, code)
	info, _ = web.arena.Devices.Device("box")
	assert.False(t, info.Required)
	assert.Equal(t, "Scoring table", info.Name, "fields left out are not changed")

	// Identify reaches the device.
	code, _ = postDevicesPage(t, web, "/setup/devices/box/identify", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, []device.DeviceCommand{{Do: "identify"}}, box.Commands())

	// Blocking disconnects it; unblocking makes it pending again.
	code, _ = postDevicesPage(t, web, "/setup/devices/box/block", nil)
	require.Equal(t, 200, code)
	assert.False(t, box.Connected())
	info, _ = web.arena.Devices.Device("box")
	assert.Equal(t, "blocked", info.State)
	_, err = web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "box"})
	assert.Error(t, err, "a blocked device cannot connect")
	code, _ = postDevicesPage(t, web, "/setup/devices/box/unblock", nil)
	require.Equal(t, 200, code)
	info, _ = web.arena.Devices.Device("box")
	assert.Equal(t, "pending", info.State)
	code, _ = postDevicesPage(t, web, "/setup/devices/box/reboot", nil)
	assert.Equal(t, 400, code, "a device that is not approved cannot be sent commands")

	code, _ = postDevicesPage(t, web, "/setup/devices/box/delete", nil)
	require.Equal(t, 200, code)
	_, ok := web.arena.Devices.Device("box")
	assert.False(t, ok)
	code, response = postDevicesPage(t, web, "/setup/devices/box/delete", nil)
	assert.Equal(t, 400, code)
	assert.Equal(t, `there is no device "box"`, response["error"])
}

func TestDevicesManualOutput(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("stack", "Stack light")
	require.Nil(t, err)
	stack, err := web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "stack", Channels: []*device.Channel{
		{ID: "green", Dir: device.DirOut, Type: device.TypeRelay},
		{ID: "strip", Dir: device.DirOut, Type: device.TypeLEDStrip, Pixels: 10},
		{ID: "bound", Dir: device.DirOut, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	set, err := device.ParseBindings([]byte(`
outputs:
  - { id: running, device: stack, channel: bound, when: "match_state == 'running'" }
`))
	require.Nil(t, err)
	_, err = web.arena.Devices.SaveBindings(set)
	require.Nil(t, err)

	code, response := postDevicesPage(t, web, "/setup/devices/stack/outputs/green/test", map[string]any{"v": 1})
	require.Equal(t, 200, code, response)
	on := 1.0
	assert.Equal(t, &device.Command{V: &on}, stack.Output("green"))
	code, _ = postDevicesPage(t, web, "/setup/devices/stack/outputs/strip/test", map[string]any{
		"p": "blink", "c": []string{"#ff0000", "#000000"}, "ms": 400,
	})
	require.Equal(t, 200, code)
	assert.Equal(t, "blink", stack.Output("strip").P)

	// The page says who drives each output.
	drivers := getDevicesPageState(t, web).Drivers
	assert.Contains(t, drivers, device.OutputDriver{
		Device: "stack", Channel: "bound", Owner: device.BindingsOwner, Binding: "running", Active: true,
	})
	assert.Contains(t, drivers, device.OutputDriver{
		Device: "stack", Channel: "green", Owner: device.ManualOutputOwner, Active: true,
	})

	// A bound output is not fought over, and a bad command is refused.
	code, response = postDevicesPage(t, web, "/setup/devices/stack/outputs/bound/test", map[string]any{"v": 1})
	assert.Equal(t, 400, code)
	assert.Contains(t, response["error"], `driven by binding "running"`)
	code, response = postDevicesPage(t, web, "/setup/devices/stack/outputs/green/test", map[string]any{"v": 0.5})
	assert.Equal(t, 400, code)
	assert.Contains(t, response["error"], "takes {\"v\":1} or {\"v\":0}")
	recorder := web.requestWithBody("POST", "/setup/devices/stack/outputs/green/test", `{"value":1}`)
	assert.Equal(t, 400, recorder.Code)

	code, _ = postDevicesPage(t, web, "/setup/devices/stack/outputs/green/end_test", nil)
	require.Equal(t, 200, code)
	assert.Nil(t, stack.Output("green"))
	code, response = postDevicesPage(t, web, "/setup/devices/stack/outputs/green/end_test", nil)
	assert.Equal(t, 400, code)
	assert.Equal(t, "output stack/green is not being tested", response["error"])
}

func TestDevicesClearFaults(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("estop", "E-stop box")
	require.Nil(t, err)
	manifest := &device.Manifest{Proto: 1, ID: "estop", Channels: []*device.Channel{
		{ID: "stop", Dir: device.DirIn, Type: device.TypeDigital, Safety: true},
	}}
	box, err := web.arena.DeviceVirtual.Connect(manifest)
	require.Nil(t, err)
	box.StartHeartbeat(0)
	set, err := device.ParseBindings([]byte(`
inputs:
  - { id: estop-r1, device: estop, channel: stop, action: { field: estop, station: R1 } }
`))
	require.Nil(t, err)
	_, err = web.arena.Devices.SaveBindings(set)
	require.Nil(t, err)

	// The box loses power: its stop faults, and the fault cannot be cleared until it is back.
	box.Disconnect()
	state := getDevicesPageState(t, web)
	if assert.Equal(t, 1, len(state.Faults)) {
		assert.Equal(t, "estop-r1", state.Faults[0].Binding)
	}
	code, response := postDevicesPage(t, web, "/setup/devices/estop/clear_faults", nil)
	assert.Equal(t, 400, code)
	assert.Contains(t, response["error"], "is offline")
	code, _ = postDevicesPage(t, web, "/setup/devices/clear_faults", nil)
	assert.Equal(t, 400, code)

	box, err = web.arena.DeviceVirtual.Connect(manifest)
	require.Nil(t, err)
	box.StartHeartbeat(0)
	code, response = postDevicesPage(t, web, "/setup/devices/clear_faults", nil)
	require.Equal(t, 200, code, response)
	assert.Empty(t, response["faults"])
	assert.False(t, web.arena.Devices.Faulted())
}

// The feed sends the whole state, then every device event as it happens.
func TestDevicesWebsocket(t *testing.T) {
	web := setupTestWeb(t)
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("arcade", "Arcade")
	require.Nil(t, err)

	server, wsUrl := web.startTestServer()
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/setup/devices/websocket", nil)
	require.Nil(t, err)
	defer conn.Close()
	ws := websocket.NewTestWebsocket(conn)
	messages := readWebsocketMultiple(t, ws, 2)
	assert.Contains(t, messages, "game")
	state := messages["devices"].(map[string]any)
	assert.Equal(t, "arcade", state["devices"].([]any)[0].(map[string]any)["id"])
	assert.NotEmpty(t, state["transports"])

	arcade, err := web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "arcade", Channels: []*device.Channel{
		{ID: "button", Dir: device.DirIn, Type: device.TypeDigital},
	}})
	require.Nil(t, err)
	require.Nil(t, arcade.SetInput("button", 1))
	for i := 0; ; i++ {
		event, _ := readWebsocketUntil(t, ws, "deviceEvent").(map[string]any)
		require.True(t, event != nil && i < 10, "no input event")
		if event["type"] == "device.input" {
			assert.Equal(t, map[string]any{"id": "arcade", "channel": "button", "value": 1.0}, event["data"])
			break
		}
	}
	// Anything but an input or output is followed by the whole state again.
	state = readWebsocketUntil(t, ws, "devices").(map[string]any)
	info := state["devices"].([]any)[0].(map[string]any)
	assert.Equal(t, true, info["online"])
}

func TestDevicesStateMqtt(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.DeviceMqttPort = 2883
	state := getDevicesPageState(t, web)
	assert.Equal(t, 2883, state.Mqtt.Port, "the port from the settings while the broker is not running")
	assert.NotEmpty(t, state.Mqtt.Hosts)

	// A running broker says which port it really has.
	require.Nil(t, web.arena.DeviceMqtt.Configure(device.MqttConfig{TCPAddress: "127.0.0.1:0"}))
	require.Nil(t, web.arena.Devices.Start())
	t.Cleanup(web.arena.Devices.Stop)
	address := web.arena.DeviceMqtt.Addresses()[0]
	state = getDevicesPageState(t, web)
	assert.True(t, strings.HasSuffix(address, ":"+strconv.Itoa(state.Mqtt.Port)), address)
	var mqtt devicesPageTransport
	for _, transport := range state.Transports {
		if transport.Name == device.MqttTransportName {
			mqtt = transport
		}
	}
	assert.True(t, mqtt.Running)
	assert.Equal(t, 0, mqtt.Clients)
}

func TestDeviceHostCandidates(t *testing.T) {
	saved := network.ServerIpAddress
	defer func() { network.ServerIpAddress = saved }()
	network.ServerIpAddress = "10.0.100.5"
	local := []string{"192.168.1.20", "10.0.100.5"}

	assert.Equal(t, []string{"10.0.100.5", "192.168.1.20"}, deviceHostCandidates("localhost:8080", local))
	assert.Equal(t, []string{"10.0.100.5", "192.168.1.20"}, deviceHostCandidates("192.168.1.20:8080", local))
	assert.Equal(t, []string{"10.0.100.5", "192.168.1.20", "fms.example"},
		deviceHostCandidates("fms.example:8080", local))
	assert.Equal(t, []string{"172.16.0.9", "192.168.1.20", "10.0.100.5"},
		deviceHostCandidates("172.16.0.9", []string{"192.168.1.20"}))
	assert.Equal(t, []string{"10.0.100.5"}, deviceHostCandidates("[::1]:8080", nil))
	assert.Equal(t, []string{"10.0.100.5"}, deviceHostCandidates("127.0.0.1", nil))
}

func TestDevicesRequestLimits(t *testing.T) {
	web := setupTestWeb(t)
	recorder := web.requestWithBody(
		"POST", "/setup/devices/create", `{"id":"`+strings.Repeat("a", maxDevicesPageRequestBytes)+`"}`,
	)
	assert.Equal(t, 400, recorder.Code)
	recorder = web.requestWithBody(
		"POST", "/setup/bindings/validate", "inputs: []\n#"+strings.Repeat("a", maxDevicesPageRequestBytes),
	)
	assert.Equal(t, 413, recorder.Code)
}
