// Routes for the Lighting page.

package web

import (
	"net/http"
)

// addLightingRoutes registers the Lighting page and its API.
func (web *Web) addLightingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/lighting", web.lightingGetHandler)
	mux.HandleFunc("POST /setup/lighting/cell", web.lightingCellPostHandler)
	mux.HandleFunc("GET /setup/lighting/data", web.lightingDataHandler)
	mux.HandleFunc("GET /setup/lighting/export", web.lightingExportHandler)
	mux.HandleFunc("POST /setup/lighting/fixtures", web.lightingFixturePostHandler)
	mux.HandleFunc("POST /setup/lighting/fixtures/{id}/delete", web.lightingFixtureDeleteHandler)
	mux.HandleFunc("POST /setup/lighting/fixtures/{id}/identify", web.lightingIdentifyPostHandler)
	mux.HandleFunc("POST /setup/lighting/import", web.lightingImportHandler)
	mux.HandleFunc("POST /setup/lighting/test", web.lightingTestPostHandler)
	mux.HandleFunc("POST /setup/lighting/test/cancel", web.lightingTestCancelHandler)
	mux.HandleFunc("GET /setup/lighting/websocket", web.lightingWebsocketHandler)
}
