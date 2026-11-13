// The version 1 API's routes. They are listed in a table so that a test can check every one of them against the
// OpenAPI description in docs/openapi.yaml, and the reverse.

package web

import (
	"net/http"
)

// openApiSpecPath is where the OpenAPI description lives, relative to the base directory.
const openApiSpecPath = "docs/openapi.yaml"

// apiRoute is one operation of the version 1 API.
type apiRoute struct {
	method  string
	path    string // As in the OpenAPI description, with {parameters}.
	handler func(web *Web) http.HandlerFunc
}

// apiRoutes lists the operations of the version 1 API that this file registers. The game routes are registered in
// newHandler, before this table existed; they are listed here too so the description test covers them.
var apiRoutes = []apiRoute{
	{"GET", "/api/v1/time", func(web *Web) http.HandlerFunc { return web.apiTimeHandler }},
	{"GET", "/api/v1/health", func(web *Web) http.HandlerFunc { return web.apiHealthHandler }},
	{"GET", "/api/v1/openapi.yaml", func(web *Web) http.HandlerFunc { return web.apiSpecHandler }},
	{"GET", "/api/v1/game", func(web *Web) http.HandlerFunc { return web.apiGameHandler }},
	{"GET", "/api/v1/match", func(web *Web) http.HandlerFunc { return web.apiCurrentMatchHandler }},
	{"GET", "/api/v1/matches", func(web *Web) http.HandlerFunc { return web.apiMatchesHandler }},
	{"GET", "/api/v1/matches/{id}", func(web *Web) http.HandlerFunc { return web.apiMatchHandler }},
	{"GET", "/api/v1/rankings", func(web *Web) http.HandlerFunc { return web.apiRankingsHandler }},
	{"GET", "/api/v1/teams", func(web *Web) http.HandlerFunc { return web.apiTeamsHandler }},
	{"GET", "/api/v1/alliances", func(web *Web) http.HandlerFunc { return web.apiAlliancesHandler }},
	{"GET", "/api/v1/events", func(web *Web) http.HandlerFunc { return web.apiEventsHandler }},
	{"GET", "/api/v1/events/stream", func(web *Web) http.HandlerFunc { return web.apiEventStreamHandler }},
	{"GET", "/api/v1/events/ws", func(web *Web) http.HandlerFunc { return web.apiEventSocketHandler }},
	{"GET", "/api/v1/exports/{file}", func(web *Web) http.HandlerFunc { return web.apiExportHandler }},
	{
		"GET", "/api/v1/diagnostics/connection/{matchId}/{stationId}",
		func(web *Web) http.HandlerFunc { return web.apiConnectionReportHandler },
	},
}

// alreadyRegisteredApiRoutes are version 1 operations registered elsewhere in newHandler. They are part of the API
// and must be described like the rest.
var alreadyRegisteredApiRoutes = [][2]string{
	{"GET", "/api/v1/games"},
	{"POST", "/api/v1/games/score"},
	{"POST", "/api/v1/games/validate"},
	{"GET", "/api/v1/games/{id}"},
	{"PUT", "/api/v1/games/{id}"},
}

// addApiRoutes registers the version 1 API.
func (web *Web) addApiRoutes(mux *http.ServeMux) {
	for _, route := range apiRoutes {
		mux.HandleFunc(route.method+" "+route.path, route.handler(web))
	}
}
