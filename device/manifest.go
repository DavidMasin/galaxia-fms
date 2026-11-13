// Package device connects field electronics to the FMS: ESP32s, Pico Ws, Raspberry Pis, browser pages pretending to
// be a device, and the field PLC. Devices announce themselves with a manifest, are approved by an operator, and are
// bound to the match by data (bindings) rather than by code. docs/devices.md is the specification; this package
// implements it and never names a particular game, device or channel.
package device

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ProtocolVersion is the only value of a manifest's proto field this FMS understands.
const ProtocolVersion = 1

// Channel directions.
const (
	DirIn  = "in"
	DirOut = "out"
)

// Channel types. TypeDigital is both an input and an output type.
const (
	TypeDigital  = "digital"
	TypeCounter  = "counter"
	TypeAnalog   = "analog"
	TypeEncoder  = "encoder"
	TypeText     = "text"
	TypeRelay    = "relay"
	TypePWM      = "pwm"
	TypeLEDStrip = "led_strip"
	TypeDisplay  = "display"
	TypeSound    = "sound"
)

var (
	inputTypes = map[string]bool{
		TypeDigital: true, TypeCounter: true, TypeAnalog: true, TypeEncoder: true, TypeText: true,
	}
	outputTypes = map[string]bool{
		TypeDigital: true, TypeRelay: true, TypePWM: true, TypeLEDStrip: true, TypeDisplay: true, TypeSound: true,
	}
	deviceIDPattern  = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	channelIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// Manifest is what a device says about itself when it connects.
type Manifest struct {
	Proto    int        `json:"proto"`
	ID       string     `json:"id"`
	Name     string     `json:"name,omitempty"`
	Firmware string     `json:"fw,omitempty"`
	SDK      string     `json:"sdk,omitempty"`
	Board    string     `json:"board,omitempty"`
	Channels []*Channel `json:"channels"`
}

// Channel is one input or output of a device.
type Channel struct {
	ID     string   `json:"id"`
	Dir    string   `json:"dir"`
	Type   string   `json:"type"`
	Name   string   `json:"name,omitempty"`
	Safety bool     `json:"safety,omitempty"` // A normally-closed stop circuit, reported in the heartbeat.
	Pixels int      `json:"pixels,omitempty"` // Number of LEDs of a led_strip.
	Unit   string   `json:"unit,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
}

// ValidDeviceID reports whether id can name a device: lowercase letters, digits and dashes, at most 40 characters.
func ValidDeviceID(id string) bool {
	return deviceIDPattern.MatchString(id)
}

// ValidChannelID reports whether id can name a channel: lowercase letters, digits and underscores.
func ValidChannelID(id string) bool {
	return channelIDPattern.MatchString(id)
}

// ParseManifest decodes a manifest and checks it. It does not check the id against the device that sent it; the
// caller does.
func ParseManifest(data []byte) (*Manifest, error) {
	manifest := new(Manifest)
	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("the manifest is not valid JSON: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Validate checks the manifest against docs/devices.md and returns every problem it finds in one error.
func (m *Manifest) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if m.Proto != ProtocolVersion {
		add("proto must be %d, not %d", ProtocolVersion, m.Proto)
	}
	if !ValidDeviceID(m.ID) {
		add("id %q must be 1 to 40 lowercase letters, digits and dashes", m.ID)
	}
	seen := make(map[string]bool, len(m.Channels))
	for i, channel := range m.Channels {
		if channel == nil {
			add("channel %d is empty", i+1)
			continue
		}
		where := fmt.Sprintf("channel %d (%s)", i+1, channel.ID)
		switch {
		case !ValidChannelID(channel.ID):
			add("channel %d: id %q must be lowercase letters, digits and underscores", i+1, channel.ID)
		case seen[channel.ID]:
			add("%s: the id is used twice", where)
		}
		seen[channel.ID] = true
		switch channel.Dir {
		case DirIn:
			if !inputTypes[channel.Type] {
				add("%s: %q is not an input type (digital, counter, analog, encoder, text)", where, channel.Type)
			}
		case DirOut:
			if !outputTypes[channel.Type] {
				add(
					"%s: %q is not an output type (digital, relay, pwm, led_strip, display, sound)", where,
					channel.Type,
				)
			}
			if channel.Safety {
				add("%s: only inputs can be safety channels", where)
			}
		default:
			add("%s: dir must be in or out, not %q", where, channel.Dir)
		}
		if channel.Safety && channel.Dir == DirIn && channel.Type != TypeDigital {
			add("%s: a safety channel is a stop circuit, so it must be digital", where)
		}
		if channel.Type == TypeLEDStrip && channel.Dir == DirOut {
			if channel.Pixels < 1 {
				add("%s: a led_strip needs pixels, its number of LEDs", where)
			}
		} else if channel.Pixels != 0 {
			add("%s: pixels is only for led_strip outputs", where)
		}
		ranged := (channel.Dir == DirIn && channel.Type == TypeAnalog) ||
			(channel.Dir == DirOut && channel.Type == TypePWM)
		if !ranged && (channel.Unit != "" || channel.Min != nil || channel.Max != nil) {
			add("%s: unit, min and max are only for analog inputs and pwm outputs", where)
		}
		if channel.Min != nil && channel.Max != nil && *channel.Min > *channel.Max {
			add("%s: min is greater than max", where)
		}
	}
	if len(problems) > 0 {
		return errors.New("invalid manifest: " + strings.Join(problems, "; "))
	}
	return nil
}

// Channel returns the channel with the given id, or nil.
func (m *Manifest) Channel(id string) *Channel {
	if m == nil {
		return nil
	}
	for _, channel := range m.Channels {
		if channel != nil && channel.ID == id {
			return channel
		}
	}
	return nil
}

// HasSafety reports whether any input of the device is a safety channel.
func (m *Manifest) HasSafety() bool {
	if m == nil {
		return false
	}
	for _, channel := range m.Channels {
		if channel != nil && channel.Safety {
			return true
		}
	}
	return false
}

// DisplayName is how the channel is called in the audit log and on the pages: its name, or its id if it has none.
func (c *Channel) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}
