// The messages devices and the FMS exchange, as every transport carries them.

package device

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Reading is one input message: a channel's value, with the optional sequence number, confidence and device time.
type Reading struct {
	Value any      `json:"v"`              // float64 for numeric channels, string for text channels.
	Seq   *int64   `json:"seq,omitempty"`  // The device's own message counter.
	Conf  *float64 `json:"conf,omitempty"` // 0 to 1, from devices that estimate (vision).
	Ts    *int64   `json:"ts,omitempty"`   // The device's clock when it read the value.
}

// Status is a device's periodic report on its own health.
type Status struct {
	Online *bool    `json:"online,omitempty"`
	Up     *float64 `json:"up,omitempty"`
	RSSI   *float64 `json:"rssi,omitempty"`
	IP     string   `json:"ip,omitempty"`
	Heap   *float64 `json:"heap,omitempty"`
}

// Heartbeat is sent every 200 ms by a device with safety channels, with the state of each.
type Heartbeat struct {
	Seq      int64          `json:"seq"`
	Channels map[string]any `json:"ch"`
}

// Command is what an output channel is told to show. Which fields apply depends on the channel type; see
// docs/devices.md. A nil *Command clears the output: the channel is no longer driven by anything.
type Command struct {
	V    *float64  `json:"v,omitempty"`    // digital, relay, pwm, display; the fill of a progress pattern.
	P    string    `json:"p,omitempty"`    // led_strip pattern.
	C    []string  `json:"c,omitempty"`    // led_strip colours, #rrggbb.
	Ms   int       `json:"ms,omitempty"`   // led_strip period.
	Br   *float64  `json:"br,omitempty"`   // led_strip brightness.
	Seg  []Segment `json:"seg,omitempty"`  // led_strip segments.
	Text *string   `json:"text,omitempty"` // display text.
	Play string    `json:"play,omitempty"` // sound to play.
	At   int64     `json:"at,omitempty"`   // UTC milliseconds at which to show it.
	// Transient marks a command that must not be replayed to a device that connects later, such as a sound. It is
	// not part of the message.
	Transient bool `json:"-"`
}

// Segment is one part of a led_strip with a pattern of its own.
type Segment struct {
	S  int      `json:"s"`
	N  int      `json:"n"`
	P  string   `json:"p,omitempty"`
	C  []string `json:"c,omitempty"`
	Ms int      `json:"ms,omitempty"`
	V  *float64 `json:"v,omitempty"`
}

// Equal reports whether two commands would send the same message. Nil equals only nil.
func (c *Command) Equal(other *Command) bool {
	if c == nil || other == nil {
		return c == nil && other == nil
	}
	if c.Transient != other.Transient {
		return false
	}
	a, _ := json.Marshal(c)
	b, _ := json.Marshal(other)
	return bytes.Equal(a, b)
}

// Clone returns a copy that shares nothing with the original.
func (c *Command) Clone() *Command {
	if c == nil {
		return nil
	}
	clone := *c
	clone.V = clonePointer(c.V)
	clone.Br = clonePointer(c.Br)
	clone.Text = clonePointer(c.Text)
	clone.C = append([]string(nil), c.C...)
	if c.Seg != nil {
		clone.Seg = make([]Segment, len(c.Seg))
		for i, segment := range c.Seg {
			clone.Seg[i] = segment
			clone.Seg[i].C = append([]string(nil), segment.C...)
			clone.Seg[i].V = clonePointer(segment.V)
		}
	}
	return &clone
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

// number returns a pointer to a copy of value, for the optional fields of a command.
func number(value float64) *float64 {
	return &value
}

// Device commands.
const (
	CommandIdentify = "identify"
	CommandReboot   = "reboot"
)

// DeviceCommand is sent to a device as a whole rather than to one of its outputs.
type DeviceCommand struct {
	Do string `json:"do"`
}

// FieldState is the state of the match every device may follow, published as field/state.
type FieldState struct {
	State          string             `json:"state"`
	Phase          string             `json:"phase"`
	PhaseRemaining float64            `json:"phase_remaining"`
	MatchRemaining float64            `json:"match_remaining"`
	Scores         map[string]float64 `json:"scores"`
}

// sameAs reports whether two field states differ in anything but the clock, which changes on every look.
func (s *FieldState) sameAs(other *FieldState) bool {
	if s == nil || other == nil || s.State != other.State || s.Phase != other.Phase ||
		len(s.Scores) != len(other.Scores) {
		return false
	}
	for id, score := range s.Scores {
		if otherScore, ok := other.Scores[id]; !ok || otherScore != score {
			return false
		}
	}
	return true
}

// ParseReading decodes an input message. A boolean value is taken as 1 or 0.
func ParseReading(data []byte) (Reading, error) {
	var reading Reading
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&reading); err != nil {
		return reading, fmt.Errorf("the input is not valid JSON: %v", err)
	}
	value, err := normaliseValue(reading.Value)
	if err != nil {
		return reading, err
	}
	reading.Value = value
	if reading.Conf != nil && (*reading.Conf < 0 || *reading.Conf > 1) {
		return reading, fmt.Errorf("conf must be between 0 and 1, not %g", *reading.Conf)
	}
	return reading, nil
}

// normaliseValue turns the value of an input into a float64 or a string.
func normaliseValue(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		number, err := value.Float64()
		if err != nil {
			return nil, fmt.Errorf("v is not a number: %v", err)
		}
		return number, nil
	case float64, string:
		return value, nil
	case int:
		return float64(value), nil
	case bool:
		if value {
			return 1.0, nil
		}
		return 0.0, nil
	case nil:
		return nil, fmt.Errorf("the input has no value (v)")
	}
	return nil, fmt.Errorf("v must be a number, true or false, or a string, not %T", value)
}

// ParseStatus decodes a status message.
func ParseStatus(data []byte) (Status, error) {
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return status, fmt.Errorf("the status is not valid JSON: %v", err)
	}
	return status, nil
}

// ParseHeartbeat decodes a heartbeat.
func ParseHeartbeat(data []byte) (Heartbeat, error) {
	var heartbeat Heartbeat
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&heartbeat); err != nil {
		return heartbeat, fmt.Errorf("the heartbeat is not valid JSON: %v", err)
	}
	for channel, value := range heartbeat.Channels {
		normalised, err := normaliseValue(value)
		if err != nil {
			return heartbeat, fmt.Errorf("channel %s: %v", channel, err)
		}
		heartbeat.Channels[channel] = normalised
	}
	return heartbeat, nil
}

// clockMessage is the payload of field/clock.
func clockMessage(now time.Time) []byte {
	data, _ := json.Marshal(map[string]int64{"t": now.UnixMilli()})
	return data
}
