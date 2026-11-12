// Package gamedef loads and validates game definitions: the YAML files that describe a game's alliances, match
// phases, scoring elements, fouls, rules and rankings. Nothing in this package knows about any particular game.
package gamedef

import (
	"fmt"
	"strconv"

	"github.com/DavidMasin/galaxia-fms/rules"
	"go.yaml.in/yaml/v3"
)

// Element types.
const (
	TypeCounter = "counter"
	TypeBoolean = "boolean"
	TypeEnum    = "enum"
	TypeTimer   = "timer"
	TypeNumber  = "number"
)

// Scopes.
const (
	ScopeAlliance = "alliance"
	ScopeRobot    = "robot"
	ScopeField    = "field"
)

// Robot states during a phase.
const (
	RobotsAuto     = "auto"
	RobotsTeleop   = "teleop"
	RobotsDisabled = "disabled"
)

// Game is one parsed and validated game definition.
type Game struct {
	Schema      int            `yaml:"schema"`
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	Version     string         `yaml:"version"`
	Description string         `yaml:"description"`
	Alliances   Alliances      `yaml:"alliances"`
	Params      Params         `yaml:"params"`
	Match       Match          `yaml:"match"`
	Elements    []*Element     `yaml:"elements"`
	Fouls       Fouls          `yaml:"fouls"`
	Cards       []*Card        `yaml:"cards"`
	Derived     []*Derived     `yaml:"derived"`
	Triggers    []*Trigger     `yaml:"triggers"`
	Live        []*Live        `yaml:"live"`
	Breakdown   []*NamedExpr   `yaml:"breakdown"`
	Ranking     Ranking        `yaml:"ranking"`
	Playoff     Playoff        `yaml:"playoff"`
	Layout      Layout         `yaml:"layout"`
	TBA         TBA            `yaml:"tba"`
	Hash        string         `yaml:"-"` // SHA-256 of the source, hex.
	Source      []byte         `yaml:"-"` // The YAML this game was loaded from.
	Order       []ComputedNode `yaml:"-"` // Evaluation order of element points and derived values.
	elementByID map[string]*Element
	phaseByID   map[string]*Phase
}

type Alliances struct {
	Size int         `yaml:"size"`
	List []*Alliance `yaml:"list"`
}

type Alliance struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Short string `yaml:"short" json:"short"`
	Color string `yaml:"color" json:"color"`
	DS    string `yaml:"ds" json:"ds"`
}

// Param is a constant that an event can tune from the settings page without editing the game file.
type Param struct {
	ID      string
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Default any      `yaml:"default"`
	Min     *float64 `yaml:"min"`
	Max     *float64 `yaml:"max"`
}

// Params keeps parameters in the order they appear in the file.
type Params []*Param

func (p *Params) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: params must be a mapping", node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		param := &Param{ID: node.Content[i].Value}
		if err := node.Content[i+1].Decode(param); err != nil {
			return err
		}
		*p = append(*p, param)
	}
	return nil
}

type Match struct {
	Countdown  Countdown `yaml:"countdown"`
	Phases     []*Phase  `yaml:"phases"`
	ScoreGrace float64   `yaml:"score_grace"`
	DSGameData *Expr     `yaml:"ds_game_data"`
}

type Countdown struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	Voice      string `yaml:"voice" json:"voice"`
	Beats      int    `yaml:"beats" json:"beats"`
	QuickBeats int    `yaml:"quick_beats" json:"quickBeats"`
}

type Phase struct {
	ID       string  `yaml:"id"`
	Name     string  `yaml:"name"`
	Duration float64 `yaml:"duration"`
	Robots   string  `yaml:"robots"`
	Cues     []*Cue  `yaml:"cues"`
}

// Cue is a moment within a phase that plays a sound and can be bound to by device outputs.
type Cue struct {
	ID        string   `yaml:"id"`
	At        string   `yaml:"at"`
	Remaining *float64 `yaml:"remaining"`
	Elapsed   *float64 `yaml:"elapsed"`
	Sound     string   `yaml:"sound"`
}

// Offset returns how many seconds into a phase of the given duration the cue fires.
func (c *Cue) Offset(duration float64) float64 {
	switch {
	case c.Remaining != nil:
		return duration - *c.Remaining
	case c.Elapsed != nil:
		return *c.Elapsed
	case c.At == "end":
		return duration
	}
	return 0
}

type Element struct {
	ID      string    `yaml:"id"`
	Name    string    `yaml:"name"`
	Type    string    `yaml:"type"`
	Scope   string    `yaml:"scope"`
	Min     *float64  `yaml:"min"`
	Max     *float64  `yaml:"max"`
	Step    *float64  `yaml:"step"`
	Unit    string    `yaml:"unit"`
	Initial any       `yaml:"initial"`
	Options []*Option `yaml:"options"`
	Points  *Points   `yaml:"points"`
	Edit    []string  `yaml:"edit"`
	Phases  []string  `yaml:"phases"`
	Cycle   bool      `yaml:"cycle"`
	UI      UIHint    `yaml:"ui"`
}

// Option returns the enum option with the given id, or nil.
func (e *Element) Option(id string) *Option {
	for _, option := range e.Options {
		if option.ID == id {
			return option
		}
	}
	return nil
}

// EditableBy reports whether the given kind of source (device, scorer, referee) may change the element live.
func (e *Element) EditableBy(kind string) bool {
	if len(e.Edit) == 0 {
		return true
	}
	for _, allowed := range e.Edit {
		if allowed == kind {
			return true
		}
	}
	return false
}

// AcceptsPhase reports whether live changes are accepted during the given phase.
func (e *Element) AcceptsPhase(phase string) bool {
	if len(e.Phases) == 0 {
		return true
	}
	for _, allowed := range e.Phases {
		if allowed == phase {
			return true
		}
	}
	return false
}

type Option struct {
	ID     string  `yaml:"id"`
	Name   string  `yaml:"name"`
	Points *Points `yaml:"points"`
}

type UIHint struct {
	Group  string  `yaml:"group"`
	Widget string  `yaml:"widget"`
	Order  float64 `yaml:"order"`
	Hidden bool    `yaml:"hidden"`
}

// Points is how an element earns points: a flat value, a value per phase, or an expression.
type Points struct {
	Flat    float64
	ByPhase map[string]float64
	Expr    *Expr
	Line    int
}

func (p *Points) UnmarshalYAML(node *yaml.Node) error {
	p.Line = node.Line
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&p.Flat)
	case yaml.MappingNode:
		if len(node.Content) == 2 && node.Content[0].Value == "expr" {
			p.Expr = new(Expr)
			return node.Content[1].Decode(p.Expr)
		}
		return node.Decode(&p.ByPhase)
	}
	return fmt.Errorf("line %d: points must be a number, a map of phase to number, or {expr: ...}", node.Line)
}

// For returns the points value for something scored in the given phase, when the points are flat or per phase.
func (p *Points) For(phase string) float64 {
	if p == nil {
		return 0
	}
	if p.ByPhase != nil {
		return p.ByPhase[phase]
	}
	return p.Flat
}

type Fouls struct {
	Types []*FoulType `yaml:"types"`
	Rules []*Rule     `yaml:"rules"`
}

type FoulType struct {
	ID     string  `yaml:"id" json:"id"`
	Name   string  `yaml:"name" json:"name"`
	Points float64 `yaml:"points" json:"points"`
	To     string  `yaml:"to" json:"to"`
}

type Rule struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	Text string `yaml:"text" json:"text"`
	Foul string `yaml:"foul" json:"foul"`
}

type Card struct {
	ID            string `yaml:"id" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Color         string `yaml:"color" json:"color"`
	SecondBecomes string `yaml:"second_becomes" json:"secondBecomes"`
	Disqualifies  bool   `yaml:"disqualifies" json:"disqualifies"`
}

// Derived is a value computed from raw state. With Points set it contributes to the alliance score.
type Derived struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Scope  string `yaml:"scope"`
	Expr   *Expr  `yaml:"expr"`
	Points *Expr  `yaml:"points"`
}

// Trigger writes a value into raw state once, at a moment in the match.
type Trigger struct {
	On struct {
		PhaseStart string `yaml:"phase_start"`
		PhaseEnd   string `yaml:"phase_end"`
		Cue        string `yaml:"cue"`
	} `yaml:"on"`
	Set struct {
		Element string `yaml:"element"`
		Value   *Expr  `yaml:"value"`
	} `yaml:"set"`
}

// Live is a value that may depend on the clock. It is for displays and device outputs, never for scoring.
type Live struct {
	ID    string `yaml:"id"`
	Name  string `yaml:"name"`
	Scope string `yaml:"scope"`
	Expr  *Expr  `yaml:"expr"`
}

type NamedExpr struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Expr *Expr  `yaml:"expr"`
}

type Ranking struct {
	Points []*NamedExpr `yaml:"points"`
	Stats  []*NamedExpr `yaml:"stats"`
	Order  []*OrderKey  `yaml:"order"`
}

type OrderKey struct {
	Name     string `yaml:"name"`
	Expr     *Expr  `yaml:"expr"`
	Dir      string `yaml:"dir"`
	Decimals int    `yaml:"decimals"`
	Random   bool   `yaml:"random"`
}

type Playoff struct {
	Tiebreakers []*NamedExpr `yaml:"tiebreakers"`
}

type Layout struct {
	ScoringPanels []*Panel `yaml:"scoring_panels"`
	Audience      struct {
		Live       []string `yaml:"live"`
		Indicators []string `yaml:"indicators"`
	} `yaml:"audience"`
	TeamSign *Expr `yaml:"team_sign"`
}

type Panel struct {
	ID        string   `yaml:"id" json:"id"`
	Name      string   `yaml:"name" json:"name"`
	Alliances []string `yaml:"alliances" json:"alliances"`
	Groups    []string `yaml:"groups" json:"groups"`
}

type TBA struct {
	Year      int              `yaml:"year"`
	Breakdown map[string]*Expr `yaml:"breakdown"`
}

// Expr is an expression in the definition, with where it was written and its compiled form.
type Expr struct {
	Src    string
	Line   int
	Column int
	Prog   *rules.Program
}

func (e *Expr) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: an expression must be a single value", node.Line)
	}
	e.Src, e.Line, e.Column = node.Value, node.Line, node.Column
	return nil
}

// ComputedNode is one step in the evaluation order: an element's points or a derived value.
type ComputedNode struct {
	Kind string // "points" or "derived"
	ID   string
}

// Element returns the element with the given id, or nil.
func (g *Game) Element(id string) *Element { return g.elementByID[id] }

// Phase returns the phase with the given id, or nil.
func (g *Game) Phase(id string) *Phase { return g.phaseByID[id] }

// Alliance returns the alliance with the given id, or nil.
func (g *Game) Alliance(id string) *Alliance {
	for _, alliance := range g.Alliances.List {
		if alliance.ID == id {
			return alliance
		}
	}
	return nil
}

// AllianceIDs returns the alliance ids in definition order.
func (g *Game) AllianceIDs() []string {
	ids := make([]string, len(g.Alliances.List))
	for i, alliance := range g.Alliances.List {
		ids[i] = alliance.ID
	}
	return ids
}

// FoulType returns the foul type with the given id, or nil.
func (g *Game) FoulType(id string) *FoulType {
	for _, foulType := range g.Fouls.Types {
		if foulType.ID == id {
			return foulType
		}
	}
	return nil
}

// Card returns the card with the given id, or nil.
func (g *Game) Card(id string) *Card {
	for _, card := range g.Cards {
		if card.ID == id {
			return card
		}
	}
	return nil
}

// Duration returns the total length of the match in seconds, not counting the countdown.
func (g *Game) Duration() float64 {
	total := 0.0
	for _, phase := range g.Match.Phases {
		total += phase.Duration
	}
	return total
}

// ParamDefaults returns every parameter's default value as a number (booleans become 1 or 0).
func (g *Game) ParamDefaults() map[string]float64 {
	values := make(map[string]float64, len(g.Params))
	for _, param := range g.Params {
		values[param.ID], _ = rules.ToNumber(param.Default)
	}
	return values
}

// StationIDs returns the station ids of the game, such as R1, R2, R3, B1, B2, B3.
func (g *Game) StationIDs() []string {
	var ids []string
	for _, alliance := range g.Alliances.List {
		for position := 1; position <= g.Alliances.Size; position++ {
			ids = append(ids, alliance.Short+strconv.Itoa(position))
		}
	}
	return ids
}
