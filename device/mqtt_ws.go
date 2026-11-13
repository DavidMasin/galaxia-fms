// MQTT over WebSocket, for devices that run in a browser. The library has a listener of its own, but it only binds
// its port once it is serving, so a port already in use would go unreported and port 0 (used by tests) could not be
// read back. This one binds when it is added.

package device

import (
	"errors"
	"github.com/gorilla/websocket"
	"github.com/mochi-mqtt/server/v2/listeners"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

type websocketListener struct {
	id        string
	address   string
	listener  net.Listener
	server    *http.Server
	upgrader  websocket.Upgrader
	establish listeners.EstablishFn
	mu        sync.Mutex
	closed    bool
}

func newWebsocketListener(id, address string) *websocketListener {
	return &websocketListener{
		id: id, address: address,
		upgrader: websocket.Upgrader{
			Subprotocols: []string{"mqtt"},
			// Browser devices are pages served from anywhere on the field network.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (l *websocketListener) ID() string { return l.id }

func (l *websocketListener) Address() string {
	if l.listener != nil {
		return l.listener.Addr().String()
	}
	return l.address
}

func (l *websocketListener) Protocol() string { return "ws" }

func (l *websocketListener) Init(*slog.Logger) error {
	listener, err := net.Listen("tcp", l.address)
	if err != nil {
		return err
	}
	l.listener = listener
	l.server = &http.Server{Handler: http.HandlerFunc(l.handle), ReadHeaderTimeout: 10 * time.Second}
	return nil
}

func (l *websocketListener) Serve(establish listeners.EstablishFn) {
	l.mu.Lock()
	l.establish = establish
	l.mu.Unlock()
	_ = l.server.Serve(l.listener)
}

// handle upgrades a request to a WebSocket and runs an MQTT connection over it. Any path is accepted; the
// documented one is /mqtt, which is what browser MQTT clients use by default.
func (l *websocketListener) handle(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	establish, closed := l.establish, l.closed
	l.mu.Unlock()
	if closed || establish == nil {
		http.Error(w, "the broker is not running", http.StatusServiceUnavailable)
		return
	}
	conn, err := l.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = establish(l.id, &websocketConn{Conn: conn.UnderlyingConn(), ws: conn})
}

func (l *websocketListener) Close(closeClients listeners.CloseFn) {
	l.mu.Lock()
	alreadyClosed := l.closed
	l.closed = true
	l.mu.Unlock()
	if !alreadyClosed && l.server != nil {
		// Close stops accepting; the upgraded connections are the broker's, which closeClients disconnects.
		_ = l.server.Close()
	}
	closeClients(l.id)
}

// websocketConn carries an MQTT byte stream in binary WebSocket messages. Deadlines and addresses are those of the
// underlying connection.
type websocketConn struct {
	net.Conn
	ws     *websocket.Conn
	reader io.Reader
}

func (c *websocketConn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			messageType, reader, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if messageType != websocket.BinaryMessage {
				return 0, errors.New("MQTT over WebSocket must use binary messages")
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if errors.Is(err, io.EOF) {
			// The end of one message; MQTT packets may span messages, so go on with the next.
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *websocketConn) Write(p []byte) (int, error) {
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *websocketConn) Close() error {
	return c.ws.Close()
}
