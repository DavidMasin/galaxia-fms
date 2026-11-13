// Finding a transport by name, for the layers that serve its requests: the web server hands WebSocket and HTTP
// device requests to their transports, and the serial page configures the serial one.

package device

// Transport returns the transport added with the given name, or nil if there is none.
func (m *Manager) Transport(name string) Transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, transport := range m.transports {
		if transport.Name() == name {
			return transport
		}
	}
	return nil
}
