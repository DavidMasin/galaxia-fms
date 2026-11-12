package scoring

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"go.yaml.in/yaml/v3"
	"math"
	"sort"
	"strconv"
)

// A case file holds worked examples for a game: raw states written in a compact form, and the scores they must
// produce. Every game ships one as tests.yaml beside game.yaml. The same compact state form is accepted by the
// "game score" command.
//
//	cases:
//	  - name: Red wins on balls
//	    params: { bonus_threshold: 10 }
//	    playoff: false
//	    state:
//	      red:
//	        balls: { auto: 3, teleop: 10 }      # counter: count per phase
//	        balance: 12.5                       # timer: seconds
//	        robots:
//	          - { leave: true, climb: high }    # boolean and enum values
//	          - { climb: { value: low, phase: teleop } }
//	        fouls: [minor, { type: major, robot: 2 }]
//	        cards: { 1: yellow }
//	      field: { auto_winner: red }
//	    expect:
//	      winner: red
//	      red: { score: 44, result: win, points: { balls: 32 }, rp: { win: 2 } }

// CaseFile is the content of a tests.yaml file.
type CaseFile struct {
	Cases []*Case `yaml:"cases"`
}

// Case is one worked example.
type Case struct {
	Name    string                    `yaml:"name"`
	Params  map[string]float64        `yaml:"params"`
	Playoff bool                      `yaml:"playoff"`
	State   map[string]map[string]any `yaml:"state"`
	Expect  map[string]any            `yaml:"expect"`
}

// CaseOutcome is what happened when a case was run.
type CaseOutcome struct {
	Name     string   `json:"name"`
	Failures []string `json:"failures"` // Empty when the case passed.
}

// BuildState turns the compact state form into a raw State.
func BuildState(g *gamedef.Game, compact map[string]map[string]any) (*State, error) {
	state := NewState(g)
	seq := 0
	apply := func(change Change) error {
		seq++
		change.Seq = seq
		return state.Apply(g, &change)
	}

	setElement := func(owner string, robot int, id string, raw any) error {
		element := g.Element(id)
		if element == nil {
			return fmt.Errorf("there is no element %q", id)
		}
		wantScope := gamedef.ScopeAlliance
		switch {
		case owner == "field":
			wantScope = gamedef.ScopeField
		case robot > 0:
			wantScope = gamedef.ScopeRobot
		}
		if element.Scope != wantScope {
			return fmt.Errorf("%s is a %s element and cannot be set here", id, element.Scope)
		}
		base := Change{Alliance: owner, Robot: robot, Element: id, Op: OpSet, Source: Source{Kind: SourceAdmin}}
		if owner == "field" {
			base.Alliance = ""
		}

		// Booleans and enums may be written as {value: ..., phase: ...} to say when they were set.
		phase := ""
		if len(element.Phases) > 0 {
			phase = element.Phases[0]
		}
		if wrapped, ok := raw.(map[string]any); ok && element.Type != gamedef.TypeCounter {
			if p, ok := wrapped["phase"].(string); ok {
				phase = p
			}
			raw = wrapped["value"]
		}

		switch element.Type {
		case gamedef.TypeCounter:
			counts, ok := raw.(map[string]any)
			if !ok {
				if len(g.Match.Phases) != 1 {
					return fmt.Errorf("%s is a counter; write its counts per phase, such as {auto: 2, teleop: 5}", id)
				}
				counts = map[string]any{g.Match.Phases[0].ID: raw}
			}
			for _, bucket := range sortedKeys(counts) {
				change := base
				change.Bucket, change.Value = bucket, counts[bucket]
				if err := apply(change); err != nil {
					return err
				}
			}
			return nil
		case gamedef.TypeTimer:
			base.Value = raw
		default:
			base.Value, base.Bucket = raw, phase
		}
		return apply(base)
	}

	for _, owner := range sortedKeys(compact) {
		values := compact[owner]
		if owner != "field" && g.Alliance(owner) == nil {
			return nil, fmt.Errorf("state: there is no alliance %q", owner)
		}
		for _, key := range sortedKeys(values) {
			raw := values[key]
			var err error
			switch {
			case owner != "field" && key == "robots":
				robots, _ := raw.([]any)
				for i, entry := range robots {
					robot, _ := entry.(map[string]any)
					for _, id := range sortedKeys(robot) {
						if err = setElement(owner, i+1, id, robot[id]); err != nil {
							break
						}
					}
					if err != nil {
						break
					}
				}
			case owner != "field" && key == "fouls":
				fouls, _ := raw.([]any)
				for _, entry := range fouls {
					change := Change{Alliance: owner, Element: TargetFoul, Op: OpAdd}
					switch foul := entry.(type) {
					case string:
						change.Value = foul
					case map[string]any:
						change.Value = foul["type"]
						change.Rule, _ = foul["rule"].(string)
						if robot, ok := rules.ToNumber(foul["robot"]); ok {
							change.Robot = int(robot)
						}
					}
					if err = apply(change); err != nil {
						break
					}
				}
			case owner != "field" && key == "cards":
				for position, card := range anyMap(raw) {
					robot, convErr := strconv.Atoi(position)
					if convErr != nil {
						err = fmt.Errorf("cards are keyed by robot position, got %q", position)
						break
					}
					change := Change{Alliance: owner, Robot: robot, Element: TargetCard, Op: OpSet, Value: card}
					if err = apply(change); err != nil {
						break
					}
				}
			case owner != "field" && key == "disqualified":
				err = apply(Change{Alliance: owner, Element: TargetDisqualified, Op: OpSet, Value: raw})
			default:
				err = setElement(owner, 0, key, raw)
			}
			if err != nil {
				return nil, fmt.Errorf("state of %s: %w", owner, err)
			}
		}
	}
	return state, nil
}

// anyMap reads a YAML mapping whose keys may be numbers, returning string keys.
func anyMap(raw any) map[string]any {
	out := make(map[string]any)
	switch m := raw.(type) {
	case map[string]any:
		for key, value := range m {
			out[key] = value
		}
	case map[any]any:
		for key, value := range m {
			out[fmt.Sprint(key)] = value
		}
	}
	return out
}

// RunCases runs every case in a case file against the game.
func RunCases(g *gamedef.Game, src []byte) ([]CaseOutcome, error) {
	var file CaseFile
	if err := yaml.Unmarshal(src, &file); err != nil {
		return nil, err
	}
	outcomes := make([]CaseOutcome, 0, len(file.Cases))
	for i, c := range file.Cases {
		outcome := CaseOutcome{Name: c.Name}
		if outcome.Name == "" {
			outcome.Name = fmt.Sprintf("case %d", i+1)
		}
		state, err := BuildState(g, c.State)
		if err != nil {
			outcome.Failures = []string{err.Error()}
			outcomes = append(outcomes, outcome)
			continue
		}
		result, err := Compute(g, state, Options{Params: c.Params, Playoff: c.Playoff})
		if err != nil {
			outcome.Failures = []string{err.Error()}
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.Failures = compareExpectations(g, result, c.Expect)
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

func compareExpectations(g *gamedef.Game, result *Result, expect map[string]any) []string {
	var failures []string
	fail := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }

	for _, key := range sortedKeys(expect) {
		want := expect[key]
		switch {
		case key == "winner":
			wanted, _ := want.(string)
			if wanted == "none" {
				wanted = ""
			}
			if result.Winner != wanted {
				fail("winner: got %q, want %q", result.Winner, wanted)
			}
		case key == "tiebreak":
			if result.Tiebreak != want {
				fail("tiebreak: got %q, want %q", result.Tiebreak, want)
			}
		case key == "field":
			for id, wanted := range anyMap(want) {
				if !sameValue(result.Field[id], wanted) {
					fail("field.%s: got %v, want %v", id, result.Field[id], wanted)
				}
			}
		case g.Alliance(key) != nil:
			alliance := result.Alliances[key]
			wanted, _ := want.(map[string]any)
			for _, field := range sortedKeys(wanted) {
				value := wanted[field]
				switch field {
				case "score":
					checkNumber(fail, key+".score", alliance.Score, value)
				case "foul_points":
					checkNumber(fail, key+".foul_points", alliance.FoulPoints, value)
				case "rp_total":
					checkNumber(fail, key+".rp_total", alliance.RPTotal, value)
				case "result":
					if alliance.Result != value {
						fail("%s.result: got %q, want %q", key, alliance.Result, value)
					}
				case "points":
					checkNumbers(fail, key+".points", alliance.Points, value)
				case "rp":
					checkNumbers(fail, key+".rp", alliance.RP, value)
				case "stats":
					checkNumbers(fail, key+".stats", alliance.Stats, value)
				case "breakdown":
					rows := make(map[string]float64, len(alliance.Breakdown))
					for _, row := range alliance.Breakdown {
						rows[row.ID] = row.Value
					}
					checkNumbers(fail, key+".breakdown", rows, value)
				case "values":
					for id, wantedValue := range anyMap(value) {
						if !sameValue(alliance.Values[id], wantedValue) {
							fail("%s.values.%s: got %v, want %v", key, id, alliance.Values[id], wantedValue)
						}
					}
				default:
					fail("%s.%s is not something a case can check", key, field)
				}
			}
		default:
			fail("expect.%s is not an alliance, \"field\", \"winner\" or \"tiebreak\"", key)
		}
	}
	sort.Strings(failures)
	return failures
}

func checkNumber(fail func(string, ...any), label string, got float64, want any) {
	wanted, ok := rules.ToNumber(want)
	if !ok || math.Abs(got-wanted) > 1e-6 {
		fail("%s: got %v, want %v", label, got, want)
	}
}

func checkNumbers(fail func(string, ...any), label string, got map[string]float64, want any) {
	wanted := anyMap(want)
	for _, id := range sortedKeys(wanted) {
		if _, exists := got[id]; !exists {
			fail("%s.%s does not exist", label, id)
			continue
		}
		checkNumber(fail, label+"."+id, got[id], wanted[id])
	}
}

// sameValue compares a computed value with an expected one, treating all numeric types alike.
func sameValue(got, want any) bool {
	if _, isBool := want.(bool); !isBool {
		if _, gotIsBool := got.(bool); !gotIsBool {
			gotNumber, gotOK := rules.ToNumber(got)
			wantNumber, wantOK := rules.ToNumber(want)
			if gotOK && wantOK && got != nil && want != nil {
				return math.Abs(gotNumber-wantNumber) <= 1e-6
			}
		}
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}
