// What the WebSocket, HTTP and serial transports share: they carry the MQTT payloads as JSON objects tagged with
// their kind, and they tell a refused device why in words of their own.

package device

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// clockPeriod is how often the WebSocket and serial transports send the field clock, as MQTT publishes field/clock.
const clockPeriod = time.Second

// taggedMessage returns body, which must marshal to a JSON object, with the given key and value pairs put in front:
// taggedMessage(command, "type", "out", "ch", "strip") gives {"type":"out","ch":"strip",...the command}. A nil body
// gives the pairs alone.
func taggedMessage(body any, pairs ...string) []byte {
	var message bytes.Buffer
	message.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			message.WriteByte(',')
		}
		key, _ := json.Marshal(pairs[i])
		value, _ := json.Marshal(pairs[i+1])
		message.Write(key)
		message.WriteByte(':')
		message.Write(value)
	}
	data, err := json.Marshal(body)
	if err == nil && len(data) > 2 && data[0] == '{' {
		// Everything after the opening brace of the body, closing brace included.
		message.WriteByte(',')
		message.Write(data[1:])
	} else {
		message.WriteByte('}')
	}
	return message.Bytes()
}

// outputKey is what an output message coalesces on in an outbox: only the latest command of a channel matters. A
// transient command (a sound) does not coalesce, so that two sounds in a row both play.
func outputKey(channel string, command *Command) (string, bool) {
	return "out/" + channel, !command.isTransient()
}

// refusalReason says why a device was refused, for a transport that tells the device in words.
func refusalReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidID):
		return "the id is not a valid device id: use 1 to 40 lowercase letters, digits and dashes"
	case errors.Is(err, ErrReservedID):
		return "the device id is reserved"
	case errors.Is(err, ErrBadToken):
		return "bad token"
	case errors.Is(err, ErrBlocked):
		return "the device is blocked"
	case errors.Is(err, ErrNotApproved):
		return "the device is waiting for approval"
	}
	return err.Error()
}
