// Package lighting runs the Lighting page: fixtures (the output channels that show lighting) and scenes (how each
// fixture looks at each moment of the field), compiled against the active game and driven through the device
// manager, which keeps the bindings off the channels it claims. docs/devices.md ("The Lighting page") is the
// specification. Nothing in it names a game, a device or a channel.
package lighting

import (
	"bytes"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"go.yaml.in/yaml/v3"
	"regexp"
	"sort"
	"strings"
)

// Kinds of fixture: the output channel types lighting can drive.
const (
	KindLEDStrip = device.TypeLEDStrip
	KindDigital  = device.TypeDigital
	KindRelay    = device.TypeRelay
	KindPWM      = device.TypePWM
)

// Colours a look may name besides #rrggbb.
const (
	ColorAlliance = "alliance" // The alliance of the fixture or segment the look is drawn on.
	ColorWinner   = "winner"   // The alliance that won the last match posted.
	ColorOff      = "off"      // Black.
)

var (
	idPattern    = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	kinds        = map[string]bool{KindLEDStrip: true, KindDigital: true, KindRelay: true, KindPWM: true}
)

// IsKind reports whether lighting can drive an output channel of the given type.
func IsKind(channelType string) bool {
	return kinds[channelType]
}

// Fixture is one output channel that shows lighting.
type Fixture struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name,omitempty" json:"name"`
	Device  string `yaml:"device" json:"device"`
	Channel string `yaml:"channel" json:"channel"`
	Kind    string `yaml:"kind" json:"kind"`
	// Pixels is the number of LEDs of a strip. The device's manifest, once known, has the last word.
	Pixels int `yaml:"pixels,omitempty" json:"pixels"`
	// Brightness limits every look of the fixture, from 0 to 1; nil is 1.
	Brightness *float64  `yaml:"brightness,omitempty" json:"brightness,omitempty"`
	Reverse    bool      `yaml:"reverse,omitempty" json:"reverse"`
	Alliance   string    `yaml:"alliance,omitempty" json:"alliance"`
	Segments   []Segment `yaml:"segments,omitempty" json:"segments"`
}

// Segment is a named run of LEDs of a strip, counted from the fixture's LED 0.
type Segment struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name,omitempty" json:"name"`
	Start    int    `yaml:"start" json:"start"`
	Count    int    `yaml:"count" json:"count"`
	Alliance string `yaml:"alliance,omitempty" json:"alliance"`
}

// Look is one cell of the scene grid: how a fixture or segment looks at one moment.
type Look struct {
	P    string   `yaml:"p,omitempty" json:"p,omitempty"`
	C    []string `yaml:"c,omitempty,flow" json:"c,omitempty"`
	Ms   int      `yaml:"ms,omitempty" json:"ms,omitempty"`
	V    string   `yaml:"v,omitempty" json:"v,omitempty"`       // Progress fill, 0 to 1: a number or an expression.
	When string   `yaml:"when,omitempty" json:"when,omitempty"` // The cell applies only while this is true.
	Br   *float64 `yaml:"br,omitempty" json:"br,omitempty"`
}

// Scenes is the scene grid: moment id -> target -> look. A target is a fixture id, or fixture/segment for one
// segment.
type Scenes map[string]map[string]*Look

// Config is the lighting of one field profile for one game: what the page edits and exports.
type Config struct {
	Profile  string     `yaml:"profile,omitempty" json:"profile"`
	Game     string     `yaml:"game,omitempty" json:"game"`
	Fixtures []*Fixture `yaml:"fixtures" json:"fixtures"`
	Scenes   Scenes     `yaml:"scenes" json:"scenes"`
}

// SegmentTarget is the target id of a segment of a fixture.
func SegmentTarget(fixture, segment string) string {
	return fixture + "/" + segment
}

// limit is the fixture's brightness limit.
func (fixture *Fixture) limit() float64 {
	if fixture.Brightness == nil {
		return 1
	}
	return *fixture.Brightness
}

// Fixture returns the fixture with the given id, or nil.
func (config *Config) Fixture(id string) *Fixture {
	for _, fixture := range config.Fixtures {
		if fixture.ID == id {
			return fixture
		}
	}
	return nil
}

// Cell returns the look of one cell, or nil.
func (config *Config) Cell(moment, target string) *Look {
	return config.Scenes[moment][target]
}

// SetCell sets one cell; a nil look clears it.
func (config *Config) SetCell(moment, target string, look *Look) {
	if look == nil {
		if row := config.Scenes[moment]; row != nil {
			delete(row, target)
			if len(row) == 0 {
				delete(config.Scenes, moment)
			}
		}
		return
	}
	if config.Scenes == nil {
		config.Scenes = make(Scenes)
	}
	if config.Scenes[moment] == nil {
		config.Scenes[moment] = make(map[string]*Look)
	}
	config.Scenes[moment][target] = look
}

// Targets returns the ids a scene may target for a fixture: the fixture and each of its segments.
func (fixture *Fixture) Targets() []string {
	targets := []string{fixture.ID}
	for _, segment := range fixture.Segments {
		targets = append(targets, SegmentTarget(fixture.ID, segment.ID))
	}
	return targets
}

// Clone returns a deep copy of the configuration.
func (config *Config) Clone() *Config {
	clone := &Config{Profile: config.Profile, Game: config.Game, Scenes: make(Scenes, len(config.Scenes))}
	for _, fixture := range config.Fixtures {
		clone.Fixtures = append(clone.Fixtures, fixture.clone())
	}
	for moment, row := range config.Scenes {
		cloneRow := make(map[string]*Look, len(row))
		for target, look := range row {
			cloneRow[target] = look.clone()
		}
		clone.Scenes[moment] = cloneRow
	}
	return clone
}

func (fixture *Fixture) clone() *Fixture {
	clone := *fixture
	clone.Brightness = clonePointer(fixture.Brightness)
	clone.Segments = append([]Segment(nil), fixture.Segments...)
	return &clone
}

func (look *Look) clone() *Look {
	if look == nil {
		return nil
	}
	clone := *look
	clone.C = append([]string(nil), look.C...)
	clone.Br = clonePointer(look.Br)
	return &clone
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

// ParseConfig reads an exported configuration. Unknown keys are errors, so that a typo is reported rather than
// ignored.
func ParseConfig(data []byte) (*Config, error) {
	config := &Config{Scenes: make(Scenes)}
	if len(bytes.TrimSpace(data)) == 0 {
		return config, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("the lighting file is not valid: %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	for i, fixture := range config.Fixtures {
		if fixture == nil {
			return nil, fmt.Errorf("the lighting file is not valid: fixture %d is empty", i+1)
		}
	}
	if config.Scenes == nil {
		config.Scenes = make(Scenes)
	}
	for moment, row := range config.Scenes {
		for target, look := range row {
			if look == nil {
				return nil, fmt.Errorf("the lighting file is not valid: the look of %s at %s is empty", target, moment)
			}
		}
	}
	return config, nil
}

// YAML returns the configuration as the Lighting page exports it.
func (config *Config) YAML() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// fixtureFromRecord and fixtureRecord convert between a fixture and its stored record.
func fixtureFromRecord(record model.LightingFixture) *Fixture {
	fixture := &Fixture{
		ID: record.FixtureId, Name: record.Name, Device: record.DeviceId, Channel: record.ChannelId,
		Kind: record.Kind, Pixels: record.Pixels, Reverse: record.Reverse, Alliance: record.Alliance,
		Segments: []Segment{},
	}
	if record.Brightness != 1 {
		brightness := record.Brightness
		fixture.Brightness = &brightness
	}
	for _, segment := range record.Segments {
		fixture.Segments = append(fixture.Segments, Segment{
			ID: segment.Id, Name: segment.Name, Start: segment.Start, Count: segment.Count,
			Alliance: segment.Alliance,
		})
	}
	return fixture
}

func fixtureRecord(fixture *Fixture, record *model.LightingFixture) {
	record.FixtureId, record.Name, record.DeviceId = fixture.ID, fixture.Name, fixture.Device
	record.ChannelId, record.Kind, record.Pixels = fixture.Channel, fixture.Kind, fixture.Pixels
	record.Brightness, record.Reverse, record.Alliance = fixture.limit(), fixture.Reverse, fixture.Alliance
	record.Segments = nil
	for _, segment := range fixture.Segments {
		record.Segments = append(record.Segments, model.LightingSegment{
			Id: segment.ID, Name: segment.Name, Start: segment.Start, Count: segment.Count,
			Alliance: segment.Alliance,
		})
	}
}

func lookFromRecord(record model.LightingLook) *Look {
	return &Look{
		P: record.P, C: append([]string(nil), record.C...), Ms: record.Ms, V: record.V, When: record.When,
		Br: clonePointer(record.Br),
	}
}

func lookRecord(look *Look) model.LightingLook {
	return model.LightingLook{
		P: look.P, C: append([]string(nil), look.C...), Ms: look.Ms, V: look.V, When: look.When,
		Br: clonePointer(look.Br),
	}
}

// scenesFromRecord returns the scenes of a stored grid.
func scenesFromRecord(record *model.LightingScenes) Scenes {
	scenes := make(Scenes)
	if record == nil {
		return scenes
	}
	for moment, row := range record.Cells {
		for target, look := range row {
			if scenes[moment] == nil {
				scenes[moment] = make(map[string]*Look)
			}
			scenes[moment][target] = lookFromRecord(look)
		}
	}
	return scenes
}

func scenesRecord(scenes Scenes, record *model.LightingScenes) {
	record.Cells = make(map[string]map[string]model.LightingLook, len(scenes))
	for moment, row := range scenes {
		if len(row) == 0 {
			continue
		}
		cells := make(map[string]model.LightingLook, len(row))
		for target, look := range row {
			cells[target] = lookRecord(look)
		}
		record.Cells[moment] = cells
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
