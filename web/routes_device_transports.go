// Routes for devices that connect over WebSocket or plain HTTP instead of MQTT, and for the page that chooses the
// serial ports devices are plugged into.

package web

import (
	"net/http"
)

// addDeviceTransportRoutes registers the WebSocket and HTTP device transports, and the serial port page.
func (web *Web) addDeviceTransportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices/ws", web.deviceWebsocketHandler)
	mux.HandleFunc("PUT /api/v1/devices/{id}/manifest", web.deviceHttpManifestHandler)
	mux.HandleFunc("POST /api/v1/devices/{id}/in/{ch}", web.deviceHttpInputHandler)
	mux.HandleFunc("GET /api/v1/devices/{id}/out", web.deviceHttpOutputsHandler)
	mux.HandleFunc("GET /setup/devices/serial", web.serialSetupGetHandler)
	mux.HandleFunc("GET /setup/devices/serial/ports", web.serialPortsGetHandler)
	mux.HandleFunc("POST /setup/devices/serial/ports", web.serialPortsPostHandler)
}

// The device endpoints are not behind the admin login: devices present their own tokens, which the transports
// check.

func (web *Web) deviceWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if transport := web.arena.DeviceWebsocket(); transport != nil {
		transport.ServeHTTP(w, r)
		return
	}
	writeJsonError(w, http.StatusServiceUnavailable, "the WebSocket device transport is not set up")
}

func (web *Web) deviceHttpManifestHandler(w http.ResponseWriter, r *http.Request) {
	if transport := web.arena.DeviceHttp(); transport != nil {
		transport.ServeManifest(w, r, r.PathValue("id"))
		return
	}
	writeJsonError(w, http.StatusServiceUnavailable, "the HTTP device transport is not set up")
}

func (web *Web) deviceHttpInputHandler(w http.ResponseWriter, r *http.Request) {
	if transport := web.arena.DeviceHttp(); transport != nil {
		transport.ServeInput(w, r, r.PathValue("id"), r.PathValue("ch"))
		return
	}
	writeJsonError(w, http.StatusServiceUnavailable, "the HTTP device transport is not set up")
}

func (web *Web) deviceHttpOutputsHandler(w http.ResponseWriter, r *http.Request) {
	if transport := web.arena.DeviceHttp(); transport != nil {
		transport.ServeOutputs(w, r, r.PathValue("id"))
		return
	}
	writeJsonError(w, http.StatusServiceUnavailable, "the HTTP device transport is not set up")
}
