// Transports carry device messages between the devices and the manager. MQTT, the virtual devices and the PLC are
// transports today; WebSocket, HTTP and serial plug in the same way.

package device

import (
	"errors"
)

// Access is what a device may do.
type Access int

const (
	AccessNone     Access = iota // Unknown, blocked, or refused.
	AccessPending                // May announce itself (manifest, status) and follow the field state.
	AccessApproved               // May also send inputs and heartbeats and receive outputs and commands.
)

func (access Access) String() string {
	switch access {
	case AccessPending:
		return "pending"
	case AccessApproved:
		return "approved"
	}
	return "none"
}

// Reasons a device is refused when it connects. Transports turn them into their own refusal codes.
var (
	ErrInvalidID   = errors.New("the client id is not a valid device id")
	ErrReservedID  = errors.New("the device id is reserved")
	ErrBadToken    = errors.New("bad username or password")
	ErrBlocked     = errors.New("not authorised")
	ErrNotApproved = errors.New("the device is waiting for approval")
	ErrUnknown     = errors.New("there is no such device")
)

// Transport carries messages for devices that reach the FMS one way. Every method that sends must return at once:
// the manager calls them from the arena loop, which must never wait for the network. A transport queues what it
// sends and, for outputs, may keep only the latest command of each channel.
type Transport interface {
	// Name identifies the transport in device presence and events: "mqtt", "virtual", "plc", later "ws", "http",
	// "serial".
	Name() string
	// Start begins accepting devices, which it reports to sink. It may be called again after Stop.
	Start(sink Sink) error
	// Stop disconnects every device and stops accepting new ones.
	Stop()
	// SendOutput tells a device's output channel what to show. A nil command clears it: the channel is no longer
	// driven. Outputs are sent for every bound device whether or not it is connected over this transport; a
	// transport that cannot reach the device drops them, one that can keep them (MQTT retains) does so.
	SendOutput(deviceID, channel string, command *Command)
	// SendCommand sends identify or reboot to a device.
	SendCommand(deviceID string, command DeviceCommand)
	// SendField publishes the state of the match to every device.
	SendField(state FieldState)
	// AccessChanged says that a device was approved, blocked, deleted or given a new token. A transport whose
	// devices learn their rights when they connect disconnects the device, so that it connects again with its new
	// rights.
	AccessChanged(deviceID string)
}

// Sink receives what transports hear from devices. The Manager is the sink; tests may use another. Every method may
// be called from any goroutine, and the calls for one device must be made in the order its messages arrived.
type Sink interface {
	// Authenticate decides whether a device presenting the given token may connect. An unknown device is accepted
	// as pending and remembered. It is for transports that carry a token.
	Authenticate(transport, deviceID, token string) (Access, error)
	// AuthenticateLocal is Authenticate for transports where reaching the FMS at all is the credential (serial,
	// virtual): there is no token, but a new device still has to be approved.
	AuthenticateLocal(transport, deviceID string) (Access, error)
	// Access returns what a device may do right now.
	Access(deviceID string) Access
	// Online and Offline report a device's connection over a transport. Online for a device that is already online
	// over the same transport means it reconnected without the transport noticing it go.
	Online(transport, deviceID, remote string)
	Offline(transport, deviceID string)
	Manifest(transport, deviceID string, manifest *Manifest) error
	Status(transport, deviceID string, status Status)
	Input(transport, deviceID, channel string, reading Reading) error
	Heartbeat(transport, deviceID string, heartbeat Heartbeat) error
	// Malformed reports a message that could not be read, so that it shows on the device's status.
	Malformed(transport, deviceID, what string, err error)
	// Outputs returns every output command in force, for a transport that has just started and must catch up.
	Outputs() []OutputCommand
	// OutputsOf returns the commands in force for one device, for a transport that sends them on connect.
	OutputsOf(deviceID string) map[string]*Command
}

// OutputCommand is the command in force on one output channel.
type OutputCommand struct {
	Device  string
	Channel string
	Command *Command
}

// TransportStatus says how a transport is doing, for the Devices page.
type TransportStatus struct {
	Name      string   `json:"name"`
	Running   bool     `json:"running"`
	Addresses []string `json:"addresses,omitempty"` // Where it listens.
	Error     string   `json:"error,omitempty"`     // Why it is not running, or what last went wrong.
}

// StatusReporter is implemented by transports that have more to say than whether they run.
type StatusReporter interface {
	Status() TransportStatus
}
