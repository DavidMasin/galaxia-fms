// Bindings: what each device input does to the match and what each device output shows. They are data, stored per
// game and field profile and exchanged as YAML; compile.go checks them against the game and the devices.

package device

import (
	"bytes"
	"fmt"
	"go.yaml.in/yaml/v3"
	"strings"
)

// Field actions an input can trigger.
const (
	ActionStartMatch = "start_match"
	ActionAbortMatch = "abort_match"
	ActionEStop      = "estop"
	ActionAStop      = "astop"
	ActionFieldStop  = "field_stop"
	ActionFieldReset = "field_reset"
	ActionVolunteers = "volunteers"
	ActionCommit     = "commit"
	ActionNextMatch  = "next_match"
)

// OpHold runs a timer element while a digital input is 1. The other score operations are scoring's own.
const OpHold = "hold"

// Edges a digital input can trigger on.
const (
	EdgeRising  = "rising"
	EdgeFalling = "falling"
	EdgeBoth    = "both"
)

// LED strip patterns.
const (
	PatternOff      = "off"
	PatternSolid    = "solid"
	PatternBlink    = "blink"
	PatternPulse    = "pulse"
	PatternChase    = "chase"
	PatternRainbow  = "rainbow"
	PatternProgress = "progress"
)

// ColorAlliance stands for the colour of the binding's alliance in a look.
const ColorAlliance = "alliance"

// BindingSet is every binding of one game and field profile.
type BindingSet struct {
	Inputs  []*InputBinding  `yaml:"inputs,omitempty" json:"inputs"`
	Outputs []*OutputBinding `yaml:"outputs,omitempty" json:"outputs"`
}

// InputBinding says what one input channel of a device does.
type InputBinding struct {
	ID      string `yaml:"id" json:"id"`
	Device  string `yaml:"device" json:"device"`
	Channel string `yaml:"channel" json:"channel"`
	// Edge is for digital inputs: rising (the default), falling or both.
	Edge          string   `yaml:"edge,omitempty" json:"edge,omitempty"`
	Invert        bool     `yaml:"invert,omitempty" json:"invert,omitempty"`
	DebounceMs    int      `yaml:"debounce_ms,omitempty" json:"debounce_ms,omitempty"`
	MinIntervalMs int      `yaml:"min_interval_ms,omitempty" json:"min_interval_ms,omitempty"`
	Phases        []string `yaml:"phases,omitempty,flow" json:"phases,omitempty"` // Empty means any time.
	// Scale and Offset are for analog and encoder inputs: the value is raw * scale + offset.
	Scale  *float64 `yaml:"scale,omitempty" json:"scale,omitempty"`
	Offset *float64 `yaml:"offset,omitempty" json:"offset,omitempty"`
	// Threshold turns an analog input into a digital one, with Hysteresis below it before it turns off.
	Threshold  *float64 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Hysteresis *float64 `yaml:"hysteresis,omitempty" json:"hysteresis,omitempty"`
	Action     Action   `yaml:"action" json:"action"`
}

// Action is what an input binding does: a change to the score (Score set) or something the field does (Field set).
type Action struct {
	Score    string `yaml:"score,omitempty" json:"score,omitempty"`
	Op       string `yaml:"op,omitempty" json:"op,omitempty"`
	Value    any    `yaml:"value,omitempty" json:"value,omitempty"`
	Alliance string `yaml:"alliance,omitempty" json:"alliance,omitempty"`
	Robot    int    `yaml:"robot,omitempty" json:"robot,omitempty"`
	Field    string `yaml:"field,omitempty" json:"field,omitempty"`
	Station  string `yaml:"station,omitempty" json:"station,omitempty"`
}

// OutputBinding says what one output channel of a device shows.
type OutputBinding struct {
	ID       string `yaml:"id" json:"id"`
	Device   string `yaml:"device" json:"device"`
	Channel  string `yaml:"channel" json:"channel"`
	Alliance string `yaml:"alliance,omitempty" json:"alliance,omitempty"` // Sets "own" and the colour "alliance".
	When     string `yaml:"when,omitempty" json:"when,omitempty"`         // On while true.
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`       // pwm level, display text or number.
	Look     *Look  `yaml:"look,omitempty" json:"look,omitempty"`
	Else     *Look  `yaml:"else,omitempty" json:"else,omitempty"`
	On       string `yaml:"on,omitempty" json:"on,omitempty"` // An event that fires a pulse, instead of a condition.
	PulseMs  int    `yaml:"pulse_ms,omitempty" json:"pulse_ms,omitempty"`
}

// Look is how an output appears: an LED strip command, with expressions where numbers vary, or for a digital
// output the pattern the FMS blinks it with.
type Look struct {
	P    string        `yaml:"p,omitempty" json:"p,omitempty"`
	C    []string      `yaml:"c,omitempty,flow" json:"c,omitempty"`
	Ms   int           `yaml:"ms,omitempty" json:"ms,omitempty"`
	V    string        `yaml:"v,omitempty" json:"v,omitempty"` // Progress fill, 0 to 1: a number or an expression.
	Br   *float64      `yaml:"br,omitempty" json:"br,omitempty"`
	Seg  []LookSegment `yaml:"seg,omitempty" json:"seg,omitempty"`
	Play string        `yaml:"play,omitempty" json:"play,omitempty"` // Sound outputs: the sound to play.
}

// LookSegment is one segment of a strip's look.
type LookSegment struct {
	S  int      `yaml:"s" json:"s"`
	N  int      `yaml:"n" json:"n"`
	P  string   `yaml:"p,omitempty" json:"p,omitempty"`
	C  []string `yaml:"c,omitempty,flow" json:"c,omitempty"`
	Ms int      `yaml:"ms,omitempty" json:"ms,omitempty"`
	V  string   `yaml:"v,omitempty" json:"v,omitempty"`
}

// ParseBindings reads a binding set from YAML (or JSON, which is YAML too). Unknown keys are errors, so that a typo
// such as "debounce: 30" is reported rather than ignored.
func ParseBindings(data []byte) (*BindingSet, error) {
	set := new(BindingSet)
	if len(bytes.TrimSpace(data)) == 0 {
		return set, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(set); err != nil {
		return nil, fmt.Errorf("the bindings are not valid: %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	for i, input := range set.Inputs {
		if input == nil {
			return nil, fmt.Errorf("the bindings are not valid: input %d is empty", i+1)
		}
	}
	for i, output := range set.Outputs {
		if output == nil {
			return nil, fmt.Errorf("the bindings are not valid: output %d is empty", i+1)
		}
	}
	return set, nil
}

// YAML returns the binding set as the YAML the Bindings page exports.
func (set *BindingSet) YAML() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(set); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Clone returns a deep copy of the binding set, so that a caller can keep it while the set in use changes.
func (set *BindingSet) Clone() *BindingSet {
	if set == nil {
		return &BindingSet{}
	}
	data, err := set.YAML()
	if err != nil {
		return &BindingSet{}
	}
	clone, err := ParseBindings(data)
	if err != nil {
		return &BindingSet{}
	}
	return clone
}

// ChannelRef names one channel of one device.
type ChannelRef struct {
	Device  string `json:"device"`
	Channel string `json:"channel"`
}

func (ref ChannelRef) String() string {
	return ref.Device + "/" + ref.Channel
}
