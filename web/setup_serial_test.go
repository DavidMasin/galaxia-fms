package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serialPost asks the serial page to open or close a port and returns the status and the page's view.
func serialPost(t *testing.T, web *Web, body string) (int, serialPortsView) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request, _ := http.NewRequest("POST", "/setup/devices/serial/ports", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	web.newHandler().ServeHTTP(recorder, request)
	var view serialPortsView
	_ = json.Unmarshal(recorder.Body.Bytes(), &view)
	return recorder.Code, view
}

// chosenPort returns the view of a port, if the page lists it.
func chosenPort(view serialPortsView, name string) (serialPortView, bool) {
	for _, port := range view.Ports {
		if port.Name == name {
			return port, true
		}
	}
	return serialPortView{}, false
}

func TestSetupSerial(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/setup/devices/serial")
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Serial Devices - Untitled Event - Galaxia FMS")
	assert.Contains(t, recorder.Body.String(), "/static/js/setup_serial.js")

	// No port is open until one is chosen. The ports of the computer the tests run on are whatever they are.
	recorder = web.getHttpResponse("/setup/devices/serial/ports")
	assert.Equal(t, http.StatusOK, recorder.Code)
	var view serialPortsView
	require.Nil(t, json.Unmarshal(recorder.Body.Bytes(), &view))
	assert.False(t, view.Running)
	for _, port := range view.Ports {
		assert.False(t, port.Chosen, port.Name)
		assert.True(t, port.Present, port.Name)
	}

	// Choosing a port stores it in the settings and hands it to the transport at once; one that is not plugged in
	// is listed all the same, so that it can be closed again.
	status, view := serialPost(t, web, `{"port":" test-port-1 ","open":true}`)
	assert.Equal(t, http.StatusOK, status)
	port, ok := chosenPort(view, "test-port-1")
	require.True(t, ok)
	assert.True(t, port.Chosen)
	assert.False(t, port.Present)
	if assert.NotNil(t, port.State) {
		assert.False(t, port.State.Open)
		assert.Equal(t, "the serial transport is not running", port.State.Problem)
	}
	status, _ = serialPost(t, web, `{"port":"test-port-2","open":true}`)
	assert.Equal(t, http.StatusOK, status)
	settings, err := web.arena.Database.GetEventSettings()
	require.Nil(t, err)
	assert.Equal(t, "test-port-1,test-port-2", settings.DeviceSerialPorts)
	assert.Equal(t, []string{"test-port-1", "test-port-2"}, web.arena.DeviceSerial().Configured())

	// Closing it takes it out of both.
	status, view = serialPost(t, web, `{"port":"test-port-1","open":false}`)
	assert.Equal(t, http.StatusOK, status)
	_, ok = chosenPort(view, "test-port-1")
	assert.False(t, ok)
	settings, _ = web.arena.Database.GetEventSettings()
	assert.Equal(t, []string{"test-port-2"}, settings.SerialPorts())
	assert.Equal(t, []string{"test-port-2"}, web.arena.DeviceSerial().Configured())

	// Requests that make no sense are refused.
	for _, body := range []string{`{"port":`, `{"port":"  ","open":true}`, `{"port":"a,b","open":true}`} {
		status, _ = serialPost(t, web, body)
		assert.Equal(t, http.StatusBadRequest, status, body)
	}

	// Like the other settings, the ports cannot be changed with a match on the field.
	web.arena.MatchState = field.PostMatch
	status, view = serialPost(t, web, `{"port":"test-port-3","open":true}`)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, settingsLockedMessage, view.Error)
	assert.Equal(t, []string{"test-port-2"}, web.arena.DeviceSerial().Configured())
	web.arena.MatchState = field.PreMatch

	// Settings saved elsewhere, or restored, are taken into use when they are loaded.
	settings.SetSerialPorts([]string{"test-port-4", "test-port-4", ""})
	assert.Equal(t, "test-port-4", settings.DeviceSerialPorts)
	require.Nil(t, web.arena.Database.UpdateEventSettings(settings))
	require.Nil(t, web.arena.LoadSettings())
	assert.Equal(t, []string{"test-port-4"}, web.arena.DeviceSerial().Configured())

	// The page is for administrators.
	web.arena.EventSettings.AdminPassword = "admin"
	recorder = web.getHttpResponse("/setup/devices/serial")
	assert.Equal(t, http.StatusTemporaryRedirect, recorder.Code)
	status, _ = serialPost(t, web, `{"port":"test-port-5","open":true}`)
	assert.Equal(t, http.StatusTemporaryRedirect, status)
}
