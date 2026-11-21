// A brake on guessing passwords: an address that fails to log in too often has to wait.

package web

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// How many failed logins one address may make within loginFailureWindow before it has to wait.
	loginFailureLimit = 10
	// How far back failed logins count, and so how long a blocked address waits.
	loginFailureWindow = time.Minute
)

// loginThrottle remembers the recent failed logins of each address. Its zero value is ready to use.
type loginThrottle struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

// recent drops an address's failures that are older than the window, and returns the rest.
func (throttle *loginThrottle) recent(address string, now time.Time) []time.Time {
	failures := throttle.failures[address]
	keep := 0
	for _, at := range failures {
		if now.Sub(at) < loginFailureWindow {
			failures[keep] = at
			keep++
		}
	}
	failures = failures[:keep]
	if keep == 0 {
		// Addresses that stopped trying are forgotten, so the map does not grow with every visitor.
		delete(throttle.failures, address)
	} else {
		throttle.failures[address] = failures
	}
	return failures
}

// blocked reports whether the address has failed too often lately to try again now.
func (throttle *loginThrottle) blocked(address string, now time.Time) bool {
	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	return len(throttle.recent(address, now)) >= loginFailureLimit
}

// failed records a failed login from the address.
func (throttle *loginThrottle) failed(address string, now time.Time) {
	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	if throttle.failures == nil {
		throttle.failures = make(map[string][]time.Time)
	}
	throttle.failures[address] = append(throttle.recent(address, now), now)
}

// succeeded forgets the address's failures: whoever is there knows a password.
func (throttle *loginThrottle) succeeded(address string) {
	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	delete(throttle.failures, address)
}

// clientAddress is the address a request came from, without its port. Headers that claim another address are not
// believed: anyone can send them.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
