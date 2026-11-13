// Routes for the Devices, Bindings and virtual device pages.

package web

import (
	"net/http"
)

// addDeviceRoutes registers the device management pages and their API. Their API lives under /setup rather than
// /api/v1/devices, which belongs to the devices themselves (the WebSocket and HTTP transports).
func (web *Web) addDeviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/devices", web.devicesPageGetHandler)
	mux.HandleFunc("GET /setup/devices/state", web.devicesPageStateHandler)
	mux.HandleFunc("GET /setup/devices/websocket", web.devicesPageWebsocketHandler)
	mux.HandleFunc("POST /setup/devices/create", web.deviceCreateHandler)
	mux.HandleFunc("POST /setup/devices/clear_faults", web.devicesClearFaultsHandler)
	for _, action := range []string{"approve", "block", "unblock", "delete", "identify", "reboot", "clear_faults"} {
		mux.HandleFunc("POST /setup/devices/{id}/"+action, web.deviceActionHandler(action))
	}
	mux.HandleFunc("POST /setup/devices/{id}/settings", web.deviceSettingsHandler)
	mux.HandleFunc("POST /setup/devices/{id}/token", web.deviceTokenHandler)
	mux.HandleFunc("POST /setup/devices/{id}/outputs/{channel}/test", web.deviceOutputTestHandler)
	mux.HandleFunc("POST /setup/devices/{id}/outputs/{channel}/end_test", web.deviceOutputEndTestHandler)

	mux.HandleFunc("GET /setup/devices/virtual", web.virtualDevicesGetHandler)
	mux.HandleFunc("POST /setup/devices/virtual/create", web.virtualDeviceCreateHandler)
	for _, action := range []string{"input", "heartbeat", "connect", "disconnect", "remove"} {
		mux.HandleFunc("POST /setup/devices/virtual/{id}/"+action, web.virtualDeviceHandler(action))
	}

	mux.HandleFunc("GET /setup/bindings", web.bindingsPageGetHandler)
	mux.HandleFunc("GET /setup/bindings/state", web.bindingsPageStateHandler)
	mux.HandleFunc("GET /setup/bindings/export", web.bindingsExportHandler)
	mux.HandleFunc("POST /setup/bindings/import", web.bindingsImportHandler)
	mux.HandleFunc("POST /setup/bindings/save", web.bindingsSaveHandler)
	mux.HandleFunc("POST /setup/bindings/validate", web.bindingsValidateHandler)
}
