package gamedef

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed game.schema.json
var schemaJSON []byte

// SchemaJSON returns the JSON Schema that game definitions are validated against.
func SchemaJSON() []byte { return schemaJSON }

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("game.schema.json", doc); err != nil {
		return nil, err
	}
	return compiler.Compile("game.schema.json")
})

// Diagnostic is one problem found in a game definition.
type Diagnostic struct {
	Severity string `json:"severity"` // "error" or "warning"
	Path     string `json:"path"`     // JSON pointer to the offending value, for example /elements/2/points
	Line     int    `json:"line"`     // 1-based; 0 when unknown
	Column   int    `json:"column"`   // 1-based; 0 when unknown
	Message  string `json:"message"`
}

func (d Diagnostic) String() string {
	where := d.Path
	if d.Line > 0 {
		where = fmt.Sprintf("line %d", d.Line)
		if d.Path != "" {
			where += " (" + d.Path + ")"
		}
	}
	return fmt.Sprintf("%s: %s: %s", d.Severity, where, d.Message)
}

// Diagnostics is a list of problems. It implements error.
type Diagnostics []Diagnostic

func (d Diagnostics) HasErrors() bool {
	for _, diagnostic := range d {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func (d Diagnostics) Error() string {
	lines := make([]string, len(d))
	for i, diagnostic := range d {
		lines[i] = diagnostic.String()
	}
	return strings.Join(lines, "\n")
}

// Names that have a fixed meaning on an alliance object and so cannot be used as element, derived or live ids.
var reservedMembers = map[string]bool{
	"id": true, "name": true, "robots": true, "points": true, "phase_points": true, "fouls": true,
	"foul_points": true, "cards": true, "disqualified": true, "breakdown": true, "score": true, "rp": true,
	"position": true, "team": true, "total": true, "seconds": true, "running": true, "count": true,
}

// Names that have a fixed meaning at the top level of an expression and so cannot be used as alliance ids.
var reservedRoots = map[string]bool{
	"own": true, "opp": true, "opponents": true, "alliances": true, "field": true, "params": true, "value": true,
	"robot": true, "result": true, "phase": true, "phase_remaining": true, "phase_elapsed": true,
	"phase_duration": true, "match_time": true, "match_remaining": true, "match_state": true, "station": true,
	"true": true, "false": true, "nil": true, "let": true, "in": true, "not": true, "and": true, "or": true,
}

// Names that cannot be used as phase ids because counters expose them alongside the per-phase counts.
var reservedPhases = map[string]bool{"total": true, "expr": true, "post": true, "idle": true, "countdown": true}

// Names available in ranking order expressions besides the game's own stats.
var rankingTotals = []string{"rp", "played", "wins", "losses", "ties", "disqualifications"}

// Parse reads, validates and compiles a game definition. The returned game is nil if there are any errors.
func Parse(src []byte) (*Game, Diagnostics) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, Diagnostics{yamlDiagnostic(err)}
	}
	if len(root.Content) == 0 {
		return nil, Diagnostics{{Severity: "error", Message: "the file is empty"}}
	}
	doc := root.Content[0]

	// Structural validation against the JSON Schema.
	var instance any
	if err := doc.Decode(&instance); err != nil {
		return nil, Diagnostics{yamlDiagnostic(err)}
	}
	schema, err := compiledSchema()
	if err != nil {
		return nil, Diagnostics{{Severity: "error", Message: "internal error: schema does not compile: " + err.Error()}}
	}
	v := &validator{doc: doc}
	if err := schema.Validate(instance); err != nil {
		var validationError *jsonschema.ValidationError
		if errors.As(err, &validationError) {
			v.addSchemaErrors(validationError)
		} else {
			v.errorf("", "%s", err.Error())
		}
		return nil, v.diagnostics
	}

	game := new(Game)
	if err := doc.Decode(game); err != nil {
		return nil, Diagnostics{yamlDiagnostic(err)}
	}
	v.game = game
	v.validate()
	if v.diagnostics.HasErrors() {
		return nil, v.diagnostics
	}

	hash := sha256.Sum256(src)
	game.Hash = hex.EncodeToString(hash[:])
	game.Source = append([]byte(nil), src...)
	return game, v.diagnostics
}

// LoadFile parses the game definition at path.
func LoadFile(path string) (*Game, Diagnostics) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, Diagnostics{{Severity: "error", Message: err.Error()}}
	}
	return Parse(src)
}

// GameFile is the name of the definition inside a game's directory.
const GameFile = "game.yaml"

// LoadDir loads every game under dir, where each game lives in dir/<id>/game.yaml. It returns the games that
// loaded cleanly and the diagnostics for every game, keyed by directory name.
func LoadDir(dir string) (map[string]*Game, map[string]Diagnostics, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	games := make(map[string]*Game)
	problems := make(map[string]Diagnostics)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), GameFile)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		game, diagnostics := LoadFile(path)
		if game != nil && game.ID != entry.Name() {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "error", Path: "/id", Line: 1,
				Message: fmt.Sprintf("id %q must match the directory name %q", game.ID, entry.Name()),
			})
			game = nil
		}
		if len(diagnostics) > 0 {
			problems[entry.Name()] = diagnostics
		}
		if game != nil {
			games[entry.Name()] = game
		}
	}
	return games, problems, nil
}

var yamlLinePattern = regexp.MustCompile(`line (\d+)`)

func yamlDiagnostic(err error) Diagnostic {
	d := Diagnostic{Severity: "error", Message: strings.TrimPrefix(err.Error(), "yaml: ")}
	if match := yamlLinePattern.FindStringSubmatch(err.Error()); match != nil {
		d.Line, _ = strconv.Atoi(match[1])
	}
	return d
}

type validator struct {
	doc         *yaml.Node
	game        *Game
	diagnostics Diagnostics
}

func (v *validator) errorf(path, format string, args ...any) {
	v.add("error", path, 0, 0, fmt.Sprintf(format, args...))
}

func (v *validator) add(severity, path string, line, column int, msg string) {
	if line == 0 {
		if node := nodeAt(v.doc, path); node != nil {
			line, column = node.Line, node.Column
		}
	}
	v.diagnostics = append(
		v.diagnostics, Diagnostic{Severity: severity, Path: path, Line: line, Column: column, Message: msg},
	)
}

// nodeAt follows a JSON pointer through the YAML document. It returns the deepest node it can reach.
func nodeAt(node *yaml.Node, pointer string) *yaml.Node {
	if pointer == "" {
		return node
	}
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		var next *yaml.Node
		switch node.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == part {
					next = node.Content[i+1]
					break
				}
			}
		case yaml.SequenceNode:
			if index, err := strconv.Atoi(part); err == nil && index >= 0 && index < len(node.Content) {
				next = node.Content[index]
			}
		}
		if next == nil {
			return node
		}
		node = next
	}
	return node
}

func (v *validator) addSchemaErrors(validationError *jsonschema.ValidationError) {
	printer := message.NewPrinter(language.English)
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, cause := range e.Causes {
				walk(cause)
			}
			return
		}
		path := ""
		if len(e.InstanceLocation) > 0 {
			path = "/" + strings.Join(e.InstanceLocation, "/")
		}
		msg := e.ErrorKind.LocalizedString(printer)
		if key := path + "\x00" + msg; !seen[key] {
			seen[key] = true
			v.errorf(path, "%s", msg)
		}
	}
	walk(validationError)
}

func titleFromID(id string) string {
	words := strings.Split(strings.ReplaceAll(id, "-", "_"), "_")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// validate runs every check that the JSON Schema cannot express, fills in defaults, and compiles expressions.
func (v *validator) validate() {
	g := v.game
	g.elementByID = make(map[string]*Element)
	g.phaseByID = make(map[string]*Phase)

	// Alliances.
	shorts := map[string]bool{}
	allianceIDs := map[string]bool{}
	for i, alliance := range g.Alliances.List {
		path := fmt.Sprintf("/alliances/list/%d", i)
		if allianceIDs[alliance.ID] {
			v.errorf(path+"/id", "alliance id %q is used more than once", alliance.ID)
		}
		if reservedRoots[alliance.ID] {
			v.errorf(path+"/id", "%q cannot be used as an alliance id because expressions use that name", alliance.ID)
		}
		if shorts[alliance.Short] {
			v.errorf(path+"/short", "short code %q is used by more than one alliance", alliance.Short)
		}
		allianceIDs[alliance.ID] = true
		shorts[alliance.Short] = true
		if alliance.DS == "" {
			alliance.DS = []string{"red", "blue"}[i%2]
		}
	}

	// Phases and cues.
	cueIDs := map[string]bool{}
	for i, phase := range g.Match.Phases {
		path := fmt.Sprintf("/match/phases/%d", i)
		if g.phaseByID[phase.ID] != nil {
			v.errorf(path+"/id", "phase id %q is used more than once", phase.ID)
		}
		if reservedPhases[phase.ID] {
			v.errorf(path+"/id", "%q cannot be used as a phase id", phase.ID)
		}
		g.phaseByID[phase.ID] = phase
		if phase.Name == "" {
			phase.Name = titleFromID(phase.ID)
		}
		for j, cue := range phase.Cues {
			cuePath := fmt.Sprintf("%s/cues/%d", path, j)
			if offset := cue.Offset(phase.Duration); offset < 0 || offset > phase.Duration {
				v.errorf(cuePath, "this cue falls outside the phase, which lasts %g seconds", phase.Duration)
			}
			if cue.ID != "" {
				if cueIDs[cue.ID] {
					v.errorf(cuePath+"/id", "cue id %q is used more than once", cue.ID)
				}
				cueIDs[cue.ID] = true
			}
		}
	}
	if g.Match.Countdown.Enabled && g.Match.Countdown.Beats == 0 {
		g.Match.Countdown.Beats = 3
	}
	if g.Match.Countdown.QuickBeats == 0 {
		g.Match.Countdown.QuickBeats = 1
	}

	// Parameters.
	for _, param := range g.Params {
		path := "/params/" + param.ID
		_, isBool := param.Default.(bool)
		if param.Type == "" {
			param.Type = "number"
			if isBool {
				param.Type = "bool"
			} else if _, isInt := param.Default.(int); isInt {
				param.Type = "int"
			}
		}
		if (param.Type == "bool") != isBool {
			v.errorf(path+"/default", "the default does not match type %q", param.Type)
		}
		if param.Name == "" {
			param.Name = titleFromID(param.ID)
		}
	}

	// Elements.
	memberIDs := map[string]string{} // id -> what it is, across elements, derived and live values
	claim := func(path, id, what string) {
		if reservedMembers[id] {
			v.errorf(path, "%q cannot be used as an id because expressions use that name", id)
		} else if existing, taken := memberIDs[id]; taken {
			v.errorf(path, "id %q is already used by %s", id, existing)
		}
		memberIDs[id] = what
	}
	for i, element := range g.Elements {
		path := fmt.Sprintf("/elements/%d", i)
		claim(path+"/id", element.ID, "an element")
		g.elementByID[element.ID] = element
		if element.Name == "" {
			element.Name = titleFromID(element.ID)
		}
		if element.Type != TypeEnum && len(element.Options) > 0 {
			v.errorf(path+"/options", "only an enum element has options")
		}
		if (element.Type == TypeBoolean || element.Type == TypeEnum) && (element.Min != nil || element.Max != nil) {
			v.errorf(path, "min and max do not apply to a %s element", element.Type)
		}
		if element.Min != nil && element.Max != nil && *element.Min > *element.Max {
			v.errorf(path+"/min", "min is greater than max")
		}
		optionIDs := map[string]bool{}
		for j, option := range element.Options {
			optionPath := fmt.Sprintf("%s/options/%d", path, j)
			if optionIDs[option.ID] {
				v.errorf(optionPath+"/id", "option id %q is used more than once", option.ID)
			}
			optionIDs[option.ID] = true
			if option.Name == "" {
				option.Name = titleFromID(option.ID)
			}
			v.checkPhaseKeys(optionPath+"/points", option.Points)
			if option.Points != nil && option.Points.Expr != nil {
				v.errorf(optionPath+"/points", "an option's points must be a number or a map of phase to number")
			}
		}
		if element.Initial != nil {
			ok := false
			switch initial := element.Initial.(type) {
			case bool:
				ok = element.Type == TypeBoolean
			case string:
				ok = element.Type == TypeEnum && optionIDs[initial]
			case int, float64:
				ok = element.Type == TypeNumber
			}
			if !ok {
				v.errorf(path+"/initial", "the initial value does not fit a %s element", element.Type)
			}
		}
		for j, phase := range element.Phases {
			if g.phaseByID[phase] == nil {
				v.errorf(fmt.Sprintf("%s/phases/%d", path, j), "there is no phase %q", phase)
			}
		}
		v.checkPhaseKeys(path+"/points", element.Points)
		if element.Points != nil {
			if element.Scope == ScopeField {
				v.errorf(path+"/points", "a field element cannot earn points because it belongs to no alliance; "+
					"use a derived value with points instead")
			}
			if element.Type == TypeEnum && element.Points.Expr == nil {
				v.errorf(path+"/points", "put points on each option of an enum, or use {expr: ...} here")
			}
		}
	}

	// Fouls and cards.
	foulTypes := map[string]bool{}
	for i, foulType := range g.Fouls.Types {
		path := fmt.Sprintf("/fouls/types/%d", i)
		if foulTypes[foulType.ID] || foulType.ID == "count" {
			v.errorf(path+"/id", "foul type id %q is used more than once or is reserved", foulType.ID)
		}
		foulTypes[foulType.ID] = true
		if foulType.Name == "" {
			foulType.Name = titleFromID(foulType.ID)
		}
		if foulType.To == "" {
			foulType.To = "opponents"
		}
	}
	ruleIDs := map[string]bool{}
	for i, rule := range g.Fouls.Rules {
		path := fmt.Sprintf("/fouls/rules/%d", i)
		if ruleIDs[rule.ID+"\x00"+rule.Foul] {
			v.errorf(path+"/id", "rule %q is listed more than once for foul type %q", rule.ID, rule.Foul)
		}
		ruleIDs[rule.ID+"\x00"+rule.Foul] = true
		if !foulTypes[rule.Foul] {
			v.errorf(path+"/foul", "there is no foul type %q", rule.Foul)
		}
	}
	cardIDs := map[string]bool{}
	for i, card := range g.Cards {
		if cardIDs[card.ID] {
			v.errorf(fmt.Sprintf("/cards/%d/id", i), "card id %q is used more than once", card.ID)
		}
		cardIDs[card.ID] = true
		if card.Name == "" {
			card.Name = titleFromID(card.ID)
		}
	}
	for i, card := range g.Cards {
		if card.SecondBecomes != "" && !cardIDs[card.SecondBecomes] {
			v.errorf(fmt.Sprintf("/cards/%d/second_becomes", i), "there is no card %q", card.SecondBecomes)
		}
	}

	// Derived and live values.
	for i, derived := range g.Derived {
		path := fmt.Sprintf("/derived/%d", i)
		claim(path+"/id", derived.ID, "a derived value")
		if derived.Name == "" {
			derived.Name = titleFromID(derived.ID)
		}
		if derived.Points != nil && derived.Scope != ScopeAlliance {
			v.errorf(path+"/points", "only an alliance-scope derived value can earn points")
		}
	}
	for i, live := range g.Live {
		path := fmt.Sprintf("/live/%d", i)
		claim(path+"/id", live.ID, "a live value")
		if live.Name == "" {
			live.Name = titleFromID(live.ID)
		}
	}

	// Triggers.
	for i, trigger := range g.Triggers {
		path := fmt.Sprintf("/triggers/%d", i)
		for _, phase := range []string{trigger.On.PhaseStart, trigger.On.PhaseEnd} {
			if phase != "" && g.phaseByID[phase] == nil {
				v.errorf(path+"/on", "there is no phase %q", phase)
			}
		}
		if trigger.On.Cue != "" && !cueIDs[trigger.On.Cue] {
			v.errorf(path+"/on/cue", "there is no cue with id %q", trigger.On.Cue)
		}
		element := g.elementByID[trigger.Set.Element]
		switch {
		case element == nil:
			v.errorf(path+"/set/element", "there is no element %q", trigger.Set.Element)
		case element.Scope == ScopeRobot:
			v.errorf(path+"/set/element", "a trigger can set a field or alliance element, not a robot element")
		case element.Type == TypeCounter || element.Type == TypeTimer:
			v.errorf(path+"/set/element", "a trigger can set a boolean, enum or number element")
		}
	}

	// Breakdown, ranking, playoff.
	rowIDs := map[string]bool{}
	for i, row := range g.Breakdown {
		if rowIDs[row.ID] {
			v.errorf(fmt.Sprintf("/breakdown/%d/id", i), "breakdown id %q is used more than once", row.ID)
		}
		rowIDs[row.ID] = true
		if row.Name == "" {
			row.Name = titleFromID(row.ID)
		}
	}
	rpIDs := map[string]bool{}
	for i, rp := range g.Ranking.Points {
		if rpIDs[rp.ID] {
			v.errorf(fmt.Sprintf("/ranking/points/%d/id", i), "ranking point id %q is used more than once", rp.ID)
		}
		rpIDs[rp.ID] = true
		if rp.Name == "" {
			rp.Name = titleFromID(rp.ID)
		}
	}
	statIDs := map[string]bool{}
	for i, stat := range g.Ranking.Stats {
		path := fmt.Sprintf("/ranking/stats/%d/id", i)
		if statIDs[stat.ID] {
			v.errorf(path, "stat id %q is used more than once", stat.ID)
		}
		for _, reserved := range rankingTotals {
			if stat.ID == reserved {
				v.errorf(path, "%q cannot be used as a stat id", stat.ID)
			}
		}
		statIDs[stat.ID] = true
		if stat.Name == "" {
			stat.Name = titleFromID(stat.ID)
		}
	}
	if len(g.Ranking.Order) == 0 {
		// Sensible default: ranking points per match, then a coin flip.
		g.Ranking.Order = []*OrderKey{
			{Name: "Ranking score", Expr: &Expr{Src: "played > 0 ? rp / played : 0"}, Decimals: 2},
			{Random: true},
		}
	}
	for i, key := range g.Ranking.Order {
		if key.Dir == "" {
			key.Dir = "desc"
		}
		if key.Name == "" && !key.Random {
			key.Name = fmt.Sprintf("Tiebreaker %d", i+1)
		}
	}

	// Layout references.
	for i, panel := range g.Layout.ScoringPanels {
		if panel.Name == "" {
			panel.Name = titleFromID(panel.ID)
		}
		for j, alliance := range panel.Alliances {
			if !allianceIDs[alliance] {
				v.errorf(fmt.Sprintf("/layout/scoring_panels/%d/alliances/%d", i, j), "there is no alliance %q", alliance)
			}
		}
	}
	for i, ref := range g.Layout.Audience.Live {
		id, _, _ := strings.Cut(ref, ".")
		if element := g.elementByID[id]; element == nil && memberIDs[id] == "" {
			v.errorf(fmt.Sprintf("/layout/audience/live/%d", i), "there is no element or derived value %q", id)
		}
	}
	for i, id := range g.Layout.Audience.Indicators {
		if memberIDs[id] != "a live value" && memberIDs[id] != "a derived value" {
			v.errorf(fmt.Sprintf("/layout/audience/indicators/%d", i), "there is no live or derived value %q", id)
		}
	}

	if v.diagnostics.HasErrors() {
		return // Expressions cannot be checked reliably against a broken structure.
	}
	v.compileAll()
}

func (v *validator) checkPhaseKeys(path string, points *Points) {
	if points == nil {
		return
	}
	for phase := range points.ByPhase {
		if v.game.phaseByID[phase] == nil {
			v.errorf(path, "there is no phase %q", phase)
		}
	}
}

// compile compiles one expression and records a diagnostic if it fails.
func (v *validator) compile(e *Expr, path string, roots map[string]*rules.Shape, opts rules.Options) {
	if e == nil {
		return
	}
	prog, err := rules.Compile(e.Src, roots, opts)
	if err != nil {
		line, column := e.Line, e.Column
		var ruleError *rules.Error
		if errors.As(err, &ruleError) && line > 0 {
			if ruleError.Line > 1 {
				line += ruleError.Line - 1
				column = ruleError.Column + 1
			} else {
				column += ruleError.Column
			}
		}
		v.add("error", path, line, column, fmt.Sprintf("in expression %q: %s", e.Src, err.Error()))
		return
	}
	e.Prog = prog
}

// valueShape describes what an element's value looks like inside an expression.
func (g *Game) valueShape(element *Element) *rules.Shape {
	switch element.Type {
	case TypeCounter:
		fields := map[string]*rules.Shape{"total": rules.NumberShape}
		for _, phase := range g.Match.Phases {
			fields[phase.ID] = rules.NumberShape
		}
		return rules.Obj(fields)
	case TypeBoolean:
		return rules.BoolShape
	case TypeEnum:
		return rules.StringShape
	case TypeTimer:
		return rules.Obj(map[string]*rules.Shape{"seconds": rules.NumberShape, "running": rules.BoolShape})
	}
	return rules.NumberShape
}

// shapes builds the expression environment shapes for this game.
type shapes struct {
	alliance *rules.Shape
	field    *rules.Shape
	robot    *rules.Shape
	params   *rules.Shape
}

func (g *Game) buildShapes(withLive bool) shapes {
	number := rules.NumberShape
	robot := map[string]*rules.Shape{"position": number, "team": number}
	alliance := map[string]*rules.Shape{
		"id": rules.StringShape, "name": rules.StringShape, "foul_points": number, "score": number,
		"cards": rules.ListOf(rules.StringShape), "disqualified": rules.BoolShape,
	}
	field := map[string]*rules.Shape{}
	points := map[string]*rules.Shape{}
	phasePoints := map[string]*rules.Shape{}
	perPhase := map[string]*rules.Shape{}
	for _, phase := range g.Match.Phases {
		perPhase[phase.ID] = number
	}
	for _, element := range g.Elements {
		shape := g.valueShape(element)
		switch element.Scope {
		case ScopeAlliance:
			alliance[element.ID] = shape
		case ScopeRobot:
			robot[element.ID] = shape
		case ScopeField:
			field[element.ID] = shape
		}
		if element.Scope != ScopeField {
			points[element.ID] = number
			phasePoints[element.ID] = rules.Obj(perPhase)
		}
	}
	for _, derived := range g.Derived {
		switch {
		case derived.Points != nil:
			points[derived.ID] = number
		case derived.Scope == ScopeAlliance:
			alliance[derived.ID] = rules.AnyShape
		default:
			field[derived.ID] = rules.AnyShape
		}
	}
	if withLive {
		for _, live := range g.Live {
			if live.Scope == ScopeAlliance {
				alliance[live.ID] = rules.AnyShape
			} else {
				field[live.ID] = rules.AnyShape
			}
		}
	}
	fouls := map[string]*rules.Shape{"count": number}
	for _, foulType := range g.Fouls.Types {
		fouls[foulType.ID] = rules.Obj(map[string]*rules.Shape{"count": number, "points": number})
	}
	breakdown := map[string]*rules.Shape{}
	for _, row := range g.Breakdown {
		breakdown[row.ID] = number
	}
	rp := map[string]*rules.Shape{"total": number}
	for _, rankingPoint := range g.Ranking.Points {
		rp[rankingPoint.ID] = number
	}
	alliance["robots"] = rules.ListOf(rules.Obj(robot))
	alliance["points"] = rules.Obj(points)
	alliance["phase_points"] = rules.Obj(phasePoints)
	alliance["fouls"] = rules.Obj(fouls)
	alliance["breakdown"] = rules.Obj(breakdown)
	alliance["rp"] = rules.Obj(rp)

	params := map[string]*rules.Shape{}
	for _, param := range g.Params {
		if param.Type == "bool" {
			params[param.ID] = rules.BoolShape
		} else {
			params[param.ID] = number
		}
	}
	return shapes{
		alliance: rules.Obj(alliance), field: rules.Obj(field), robot: rules.Obj(robot), params: rules.Obj(params),
	}
}

// roots returns the top-level names for an expression evaluated for one alliance (perAlliance) or for the field.
func (g *Game) roots(s shapes, perAlliance bool) map[string]*rules.Shape {
	roots := map[string]*rules.Shape{
		"alliances": rules.ListOf(s.alliance), "field": s.field, "params": s.params,
	}
	for _, alliance := range g.Alliances.List {
		roots[alliance.ID] = s.alliance
	}
	if perAlliance {
		roots["own"] = s.alliance
		roots["opponents"] = rules.ListOf(s.alliance)
		if len(g.Alliances.List) == 2 {
			roots["opp"] = s.alliance
		}
	}
	return roots
}

func withRoots(base map[string]*rules.Shape, extra map[string]*rules.Shape) map[string]*rules.Shape {
	merged := make(map[string]*rules.Shape, len(base)+len(extra))
	for name, shape := range base {
		merged[name] = shape
	}
	for name, shape := range extra {
		merged[name] = shape
	}
	return merged
}

// LiveRoots are the clock-dependent names available to live values, device outputs and driver station game data.
var liveRootShapes = map[string]*rules.Shape{
	"phase": rules.StringShape, "phase_remaining": rules.NumberShape, "phase_elapsed": rules.NumberShape,
	"phase_duration": rules.NumberShape, "match_time": rules.NumberShape, "match_remaining": rules.NumberShape,
	"match_state": rules.StringShape,
}

// LiveRoots returns the names an expression may use when it is evaluated against the live match: everything a
// scoring expression sees, plus the clock. Bindings use this to compile output expressions.
func (g *Game) LiveRoots(perAlliance bool) map[string]*rules.Shape {
	return withRoots(g.roots(g.buildShapes(true), perAlliance), liveRootShapes)
}

func (v *validator) compileAll() {
	g := v.game
	s := g.buildShapes(false)
	allianceRoots := g.roots(s, true)
	fieldRoots := g.roots(s, false)
	none := rules.Options{}

	// Element points and derived values: the inputs to the score.
	for i, element := range g.Elements {
		if element.Points == nil || element.Points.Expr == nil {
			continue
		}
		extra := map[string]*rules.Shape{"value": g.valueShape(element)}
		if element.Scope == ScopeRobot {
			extra["robot"] = s.robot
		}
		path := fmt.Sprintf("/elements/%d/points/expr", i)
		v.compile(element.Points.Expr, path, withRoots(allianceRoots, extra), none)
		v.checkBeforeScore(element.Points.Expr, path)
	}
	for i, derived := range g.Derived {
		roots := allianceRoots
		if derived.Scope == ScopeField {
			roots = fieldRoots
		}
		path := fmt.Sprintf("/derived/%d", i)
		v.compile(derived.Expr, path+"/expr", roots, none)
		v.compile(derived.Points, path+"/points", roots, none)
		v.checkBeforeScore(derived.Expr, path+"/expr")
		v.checkBeforeScore(derived.Points, path+"/points")
	}
	for i, trigger := range g.Triggers {
		roots := fieldRoots
		if element := g.elementByID[trigger.Set.Element]; element != nil && element.Scope == ScopeAlliance {
			roots = allianceRoots
		}
		v.compile(trigger.Set.Value, fmt.Sprintf("/triggers/%d/set/value", i), roots, rules.Options{AllowPick: true})
	}

	// Everything that reads the finished score.
	for i, row := range g.Breakdown {
		v.compile(row.Expr, fmt.Sprintf("/breakdown/%d/expr", i), allianceRoots, none)
	}
	resultRoots := withRoots(allianceRoots, map[string]*rules.Shape{"result": rules.StringShape})
	for i, rp := range g.Ranking.Points {
		v.compile(rp.Expr, fmt.Sprintf("/ranking/points/%d/expr", i), resultRoots, none)
	}
	for i, stat := range g.Ranking.Stats {
		v.compile(stat.Expr, fmt.Sprintf("/ranking/stats/%d/expr", i), resultRoots, none)
	}
	for i, tiebreaker := range g.Playoff.Tiebreakers {
		v.compile(tiebreaker.Expr, fmt.Sprintf("/playoff/tiebreakers/%d/expr", i), allianceRoots, none)
	}
	for key, e := range g.TBA.Breakdown {
		v.compile(e, "/tba/breakdown/"+key, resultRoots, none)
	}
	orderRoots := map[string]*rules.Shape{}
	for _, name := range rankingTotals {
		orderRoots[name] = rules.NumberShape
	}
	for _, stat := range g.Ranking.Stats {
		orderRoots[stat.ID] = rules.NumberShape
	}
	for i, key := range g.Ranking.Order {
		v.compile(key.Expr, fmt.Sprintf("/ranking/order/%d/expr", i), orderRoots, none)
	}

	// Clock-dependent expressions.
	for i, live := range g.Live {
		v.compile(live.Expr, fmt.Sprintf("/live/%d/expr", i), g.LiveRoots(live.Scope == ScopeAlliance), none)
	}
	v.compile(g.Layout.TeamSign, "/layout/team_sign", g.LiveRoots(true), none)
	station := rules.Obj(map[string]*rules.Shape{
		"id": rules.StringShape, "alliance": rules.StringShape, "position": rules.NumberShape,
		"team": rules.NumberShape,
	})
	v.compile(
		g.Match.DSGameData, "/match/ds_game_data",
		withRoots(g.LiveRoots(true), map[string]*rules.Shape{"station": station}), none,
	)

	if !v.diagnostics.HasErrors() {
		v.orderComputed()
	}
}

// checkBeforeScore rejects reads of values that only exist once the score has been computed.
func (v *validator) checkBeforeScore(e *Expr, path string) {
	if e == nil || e.Prog == nil {
		return
	}
	for _, read := range e.Prog.Paths {
		for _, segment := range strings.Split(read, ".")[1:] {
			if segment == "score" || segment == "breakdown" || segment == "rp" {
				v.add("error", path, e.Line, e.Column, fmt.Sprintf(
					"%q cannot be read here, because the score is worked out from this value", read,
				))
				return
			}
		}
	}
}

// orderComputed sorts element points and derived values so that each comes after everything it reads.
func (v *validator) orderComputed() {
	g := v.game
	type node struct {
		ComputedNode
		progs []*rules.Program
		path  string
	}
	var nodes []*node
	byPointsID := map[string]int{} // id readable at points.<id> -> node index
	byValueID := map[string]int{}  // id readable at <alliance>.<id> or field.<id> -> node index
	for i, element := range g.Elements {
		if element.Scope == ScopeField {
			continue
		}
		n := &node{ComputedNode: ComputedNode{Kind: "points", ID: element.ID}, path: fmt.Sprintf("/elements/%d", i)}
		if element.Points != nil && element.Points.Expr != nil {
			n.progs = append(n.progs, element.Points.Expr.Prog)
		}
		byPointsID[element.ID] = len(nodes)
		nodes = append(nodes, n)
	}
	for i, derived := range g.Derived {
		n := &node{ComputedNode: ComputedNode{Kind: "derived", ID: derived.ID}, path: fmt.Sprintf("/derived/%d", i)}
		if derived.Points != nil {
			n.progs = append(n.progs, derived.Points.Prog)
			byPointsID[derived.ID] = len(nodes)
		} else {
			n.progs = append(n.progs, derived.Expr.Prog)
			byValueID[derived.ID] = len(nodes)
		}
		nodes = append(nodes, n)
	}

	dependsOn := make([]map[int]bool, len(nodes))
	for i, n := range nodes {
		dependsOn[i] = map[int]bool{}
		for _, prog := range n.progs {
			for _, read := range prog.Paths {
				segments := strings.Split(read, ".")
				for j := 1; j < len(segments); j++ {
					previous, segment := segments[j-1], segments[j]
					if previous == "points" || previous == "phase_points" {
						if target, ok := byPointsID[segment]; ok && target != i {
							dependsOn[i][target] = true
						}
					} else if previous != "fouls" {
						if target, ok := byValueID[segment]; ok && target != i {
							dependsOn[i][target] = true
						}
					}
				}
			}
		}
	}

	done := make([]bool, len(nodes))
	for len(g.Order) < len(nodes) {
		progressed := false
		for i, n := range nodes {
			if done[i] {
				continue
			}
			ready := true
			for dependency := range dependsOn[i] {
				if !done[dependency] {
					ready = false
					break
				}
			}
			if ready {
				done[i] = true
				g.Order = append(g.Order, n.ComputedNode)
				progressed = true
			}
		}
		if !progressed {
			var stuck []string
			first := ""
			for i, n := range nodes {
				if !done[i] {
					stuck = append(stuck, n.ID)
					if first == "" {
						first = n.path
					}
				}
			}
			sort.Strings(stuck)
			v.errorf(first, "these values depend on each other in a circle: %s", strings.Join(stuck, ", "))
			return
		}
	}
}
