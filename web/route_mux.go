// The server's request router, which remembers what was registered so that tests can check the API's routes against
// their description.

package web

import (
	"net/http"
)

// routeMux is an http.ServeMux that keeps the patterns registered with it.
type routeMux struct {
	*http.ServeMux
	patterns []string
}

func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux()}
}

// HandleFunc registers a handler, as http.ServeMux.HandleFunc does, and keeps the pattern.
func (mux *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.patterns = append(mux.patterns, pattern)
	mux.ServeMux.HandleFunc(pattern, handler)
}

// Handle registers a handler, as http.ServeMux.Handle does, and keeps the pattern.
func (mux *routeMux) Handle(pattern string, handler http.Handler) {
	mux.patterns = append(mux.patterns, pattern)
	mux.ServeMux.Handle(pattern, handler)
}
