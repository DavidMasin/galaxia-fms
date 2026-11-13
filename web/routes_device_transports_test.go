package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/device"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// deviceCall makes a request as an HTTP device.
func deviceCall(t *testing.T, web *Web, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request, _ := http.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	web.newHandler().ServeHTTP(recorder, request)
	var answer map[string]any
	if recorder.Body.Len() > 0 {
		require.Nil(t, json.Unmarshal(recorder.Body.Bytes(), &answer), recorder.Body.String())
	}
	return recorder.Code, answer
}

func TestDeviceTransportsAreSetUp(t *testing.T) {
	web := setupTestWeb(t)
	var names []string
	for _, status := range web.arena.Devices.TransportStatuses() {
		names = append(names, status.Name)
	}
	assert.Subset(t, names, []string{device.WsTransportName, device.HttpTransportName, device.SerialTransportName})

	// A test arena does not run, so its transports turn devices away.
	status, answer := deviceCall(t, web, "GET", "/api/v1/devices/beam-1/out", "secret", "")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "the HTTP device transport is not running", answer["error"])
	recorder := web.getHttpResponse("/api/v1/devices/ws")
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestDeviceHttpRoutes(t *testing.T) {
	web := setupTestWeb(t)
	transport := web.arena.DeviceHttp()
	require.Nil(t, transport.Start(web.arena.Devices))
	t.Cleanup(transport.Stop)
	manifest := `{"proto":1,"id":"panel-1","name":"Panel","channels":[{"id":"count","dir":"in","type":"counter"},
		{"id":"lamp","dir":"out","type":"digital"}]}`

	// The device endpoints need no admin login: devices present tokens of their own.
	web.arena.EventSettings.AdminPassword = "admin"
	status, answer := deviceCall(t, web, "PUT", "/api/v1/devices/panel-1/manifest", "", manifest)
	assert.Equal(t, http.StatusUnauthorized, status)
	status, answer = deviceCall(t, web, "PUT", "/api/v1/devices/panel-1/manifest", "secret", manifest)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{"approved": false}, answer)
	info, ok := web.arena.Devices.Device("panel-1")
	require.True(t, ok)
	assert.Equal(t, "pending", info.State)
	assert.True(t, info.Online)
	status, _ = deviceCall(t, web, "POST", "/api/v1/devices/panel-1/in/count", "secret", `{"v":1}`)
	assert.Equal(t, http.StatusForbidden, status)

	require.Nil(t, web.arena.Devices.Approve("panel-1"))
	status, _ = deviceCall(t, web, "POST", "/api/v1/devices/panel-1/in/count", "secret", `{"v":1}`)
	assert.Equal(t, http.StatusNoContent, status)
	info, _ = web.arena.Devices.Device("panel-1")
	assert.Equal(t, 1.0, info.Inputs["count"].Value)
	status, answer = deviceCall(t, web, "GET", "/api/v1/devices/panel-1/out?wait=0", "secret", "")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{}, answer["outputs"])
	assert.NotNil(t, answer["seq"])
}

func TestDeviceWebsocketRoute(t *testing.T) {
	web := setupTestWeb(t)
	transport := web.arena.DeviceWebsocket()
	require.Nil(t, transport.Start(web.arena.Devices))
	t.Cleanup(transport.Stop)
	server, wsUrl := web.startTestServer()
	defer server.Close()

	// The device endpoints need no admin login: devices present tokens of their own.
	web.arena.EventSettings.AdminPassword = "admin"
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(wsUrl+"/api/v1/devices/ws", nil)
	require.Nil(t, err)
	defer conn.Close()
	hello := `{"type":"hello","id":"script-1","token":"secret","manifest":{"proto":1,"id":"script-1","channels":[]}}`
	require.Nil(t, conn.WriteMessage(gorillawebsocket.TextMessage, []byte(hello)))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	require.Nil(t, err)
	assert.JSONEq(t, `{"type":"welcome","approved":false}`, string(data))
	info, ok := web.arena.Devices.Device("script-1")
	require.True(t, ok)
	assert.True(t, info.Online)
	assert.Contains(t, info.Presence, device.WsTransportName)
}
