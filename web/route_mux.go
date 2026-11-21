// The server's request router. It runs every handler with the arena's lock held unless told otherwise, and remembers
// what was registered so that tests can check the API's routes against their description.

package web

import (
	"net/http"
)

// routeMux is an http.ServeMux that wraps the handlers registered with it and keeps their patterns.
type routeMux struct {
	*http.ServeMux
	patterns []string
	// locked wraps a handler so that it runs with the arena's lock held; see arena_lock.go.
	locked func(http.HandlerFunc) http.HandlerFunc
}

func newRouteMux(locked func(http.HandlerFunc) http.HandlerFunc) *routeMux {
	return &routeMux{ServeMux: http.NewServeMux(), locked: locked}
}

// HandleFunc registers a handler that runs with the arena's lock held, and keeps the pattern.
func (mux *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.patterns = append(mux.patterns, pattern)
	mux.ServeMux.HandleFunc(pattern, mux.locked(handler))
}

// HandleUnlocked registers a handler that runs without the arena's lock and takes it itself around whatever it does
// with the arena. It is for the few that cannot have their request read into memory first.
func (mux *routeMux) HandleUnlocked(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.patterns = append(mux.patterns, pattern)
	mux.ServeMux.HandleFunc(pattern, handler)
}
