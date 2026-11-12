package gamedef

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const gamesDir = "../games"

var exampleGames = []string{"ball-count", "orbital-rush", "rebuilt-style"}

func TestExampleGamesLoadCleanly(t *testing.T) {
	games, problems, err := LoadDir(gamesDir)
	assert.NoError(t, err)
	assert.Empty(t, problems)
	for _, id := range exampleGames {
		if assert.Contains(t, games, id) {
			assert.Equal(t, id, games[id].ID)
			assert.Len(t, games[id].Hash, 64)
			assert.NotEmpty(t, games[id].Source)
		}
	}
}

func TestExampleGameShapes(t *testing.T) {
	games, _, err := LoadDir(gamesDir)
	assert.NoError(t, err)

	ballCount := games["ball-count"]
	assert.Equal(t, []string{"R1", "R2", "R3", "B1", "B2", "B3"}, ballCount.StationIDs())
	assert.Equal(t, 137.0, ballCount.Duration())
	assert.Equal(t, "red", ballCount.Alliance("red").DS)
	assert.Equal(t, "blue", ballCount.Alliance("blue").DS)
	assert.Equal(t, "Autonomous", ballCount.Phase("auto").Name)
	assert.Equal(t, 100.0, ballCount.Phase("teleop").Cues[1].Offset(120))

	orbital := games["orbital-rush"]
	assert.Equal(t, []string{"R1", "R2", "B1", "B2", "G1", "G2"}, orbital.StationIDs())
	assert.Equal(t, []string{"red", "blue", "green"}, orbital.AllianceIDs())
	assert.Equal(
		t, map[string]float64{"cargo_threshold": 12, "coop_threshold": 6, "storm_multiplier": 1.5, "overdrive_enabled": 1},
		orbital.ParamDefaults(),
	)
	assert.Equal(t, "bool", orbital.Params[3].Type)
	assert.True(t, orbital.Element("cargo").EditableBy("device"))
	assert.False(t, orbital.Element("cargo").EditableBy("referee"))
	assert.True(t, orbital.Element("cargo").AcceptsPhase("orbit"))
	assert.False(t, orbital.Element("cargo").AcceptsPhase("drift"))
	assert.True(t, orbital.Element("comets").AcceptsPhase("drift"))
	assert.Equal(t, 6.0, orbital.Element("shield").Option("full").Points.For("reentry"))
	assert.Equal(t, 3, orbital.Match.Countdown.Beats)
	assert.Equal(t, 1, orbital.Match.Countdown.QuickBeats)

	// Values are ordered after what they read: the dock bonus needs dock points, the co-op bonus needs co-op.
	position := map[string]int{}
	for i, node := range orbital.Order {
		position[node.Kind+":"+node.ID] = i
	}
	assert.Less(t, position["points:dock"], position["derived:storm_dock_bonus"])
	assert.Less(t, position["derived:high_docks"], position["derived:double_dock"])
	assert.Less(t, position["derived:coop"], position["derived:coop_bonus"])
	assert.Less(t, position["points:cargo"], position["derived:gravity_bonus"])
	assert.Len(t, orbital.Order, 11+9)
}

// schemaPaths lists every property the schema allows, as paths such as "elements[].ui.widget".
func schemaPaths(t *testing.T) []string {
	var schema map[string]any
	assert.NoError(t, json.Unmarshal(SchemaJSON(), &schema))
	defs := schema["$defs"].(map[string]any)
	paths := map[string]bool{}
	var walk func(node map[string]any, prefix string)
	walk = func(node map[string]any, prefix string) {
		if ref, ok := node["$ref"].(string); ok {
			node = defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		}
		for _, keyword := range []string{"oneOf", "anyOf"} {
			if options, ok := node[keyword].([]any); ok {
				for _, option := range options {
					walk(option.(map[string]any), prefix)
				}
			}
		}
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, child := range properties {
				path := name
				if prefix != "" {
					path = prefix + "." + name
				}
				paths[path] = true
				walk(child.(map[string]any), path)
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, prefix+"[]")
		}
		if additional, ok := node["additionalProperties"].(map[string]any); ok {
			walk(additional, prefix+".*")
		}
	}
	walk(schema, "")
	list := make([]string, 0, len(paths))
	for path := range paths {
		list = append(list, path)
	}
	sort.Strings(list)
	return list
}

// usedPaths lists the property paths a game file actually uses, in the same form as schemaPaths.
func usedPaths(t *testing.T, id string) map[string]bool {
	src, err := os.ReadFile(filepath.Join(gamesDir, id, GameFile))
	assert.NoError(t, err)
	var doc any
	assert.NoError(t, yaml.Unmarshal(src, &doc))
	used := map[string]bool{}
	wildcards := map[string]bool{"params": true, "tba.breakdown": true}
	var walk func(node any, prefix string)
	walk = func(node any, prefix string) {
		switch value := node.(type) {
		case map[string]any:
			for name, child := range value {
				path := name
				switch {
				case wildcards[prefix]:
					path = prefix + ".*"
				case prefix != "":
					path = prefix + "." + name
				}
				used[path] = true
				walk(child, path)
			}
		case []any:
			for _, child := range value {
				walk(child, prefix+"[]")
			}
		}
	}
	walk(doc, "")
	return used
}

func TestExampleGamesUseEverySchemaFeature(t *testing.T) {
	all := map[string]bool{}
	for _, id := range exampleGames {
		for path := range usedPaths(t, id) {
			all[path] = true
		}
	}
	orbital := usedPaths(t, "orbital-rush")
	for _, path := range schemaPaths(t) {
		if strings.HasSuffix(path, ".*") || strings.Contains(path, ".*.") && !strings.HasPrefix(path, "params") {
			continue // Free-form keys.
		}
		assert.True(t, all[path], "no example game uses %s", path)
		// Orbital Rush is the reference for the whole schema, apart from publishing to The Blue Alliance, which
		// a three-alliance game cannot do.
		if !strings.HasPrefix(path, "tba") {
			assert.True(t, orbital[path], "orbital-rush does not use %s", path)
		}
	}
}

// base is a small valid game that the diagnostics tests break in one place at a time.
const base = `schema: 1
id: test-game
name: Test Game
alliances:
  size: 2
  list:
    - { id: red, name: Red, short: R, color: "#ff0000" }
    - { id: blue, name: Blue, short: B, color: "#0000ff" }
match:
  phases:
    - { id: auto, duration: 15, robots: auto }
    - { id: teleop, duration: 60, robots: teleop }
elements:
  - { id: balls, type: counter, scope: alliance, points: { auto: 2, teleop: 1 } }
  - { id: parked, type: boolean, scope: robot, points: 3 }
  - { id: mode, type: enum, scope: field, options: [{ id: day }, { id: night }] }
derived:
  - { id: parked_count, scope: alliance, expr: "count(own.robots, #.parked)" }
  - { id: park_bonus, scope: alliance, points: "own.parked_count == 2 ? 5 : 0" }
`

func TestBaseGameIsValid(t *testing.T) {
	game, diagnostics := Parse([]byte(base))
	assert.Empty(t, diagnostics)
	if assert.NotNil(t, game) {
		assert.Equal(t, "Balls", game.Element("balls").Name)
		assert.Equal(t, "Parked Count", game.Derived[0].Name)
		assert.Len(t, game.Ranking.Order, 2) // The default order: ranking score, then random.
	}
}

func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		replace [2]string // Text in base to replace, and its replacement. Empty old text appends.
		message string
		line    int
	}{
		{"yaml syntax", [2]string{"name: Test Game", "name: [Test Game"}, "did not find expected", 0},
		{"unknown top-level key", [2]string{"", "scoring: {}\n"}, "additional properties 'scoring' not allowed", 1},
		{"missing required", [2]string{"name: Test Game\n", ""}, "missing property 'name'", 1},
		{"bad element type", [2]string{"type: counter", "type: tally"}, "value must be one of", 14},
		{"bad id", [2]string{"id: balls", "id: Balls"}, "does not match pattern", 14},
		{"too many alliances robots", [2]string{"size: 2", "size: 5"}, "maximum", 5},
		{"enum without options", [2]string{", options: [{ id: day }, { id: night }]", ""}, "missing property 'options'", 16},
		{"duplicate element", [2]string{"id: parked,", "id: balls,"}, `id "balls" is already used by an element`, 15},
		{"reserved element id", [2]string{"id: balls", "id: score"}, `"score" cannot be used as an id`, 14},
		{"reserved alliance id", [2]string{"id: red,", "id: own,"}, `"own" cannot be used as an alliance id`, 7},
		{"duplicate short code", [2]string{"short: B", "short: R"}, `short code "R" is used by more than one alliance`, 8},
		{"duplicate phase", [2]string{"id: teleop, duration", "id: auto, duration"}, `phase id "auto" is used more than once`, 12},
		{"reserved phase", [2]string{"id: teleop, duration", "id: total, duration"}, `"total" cannot be used as a phase id`, 12},
		{"points for unknown phase", [2]string{"teleop: 1 }", "endgame: 1 }"}, `there is no phase "endgame"`, 14},
		{"field element with points", [2]string{"scope: field,", "scope: field, points: 2,"}, "a field element cannot earn points", 16},
		{"unknown name in expression", [2]string{"own.robots, #.parked)", "own.robots, #.parkd)"}, `"#" has no property "parkd" (did you mean "parked"?)`, 18},
		{"clock in scoring expression", [2]string{"own.parked_count == 2", "phase == 'auto'"}, `unknown name "phase"`, 19},
		{"score read before it exists", [2]string{"own.parked_count == 2", "own.score > 2"}, `"own.score" cannot be read here`, 19},
		{"circular values", [2]string{`expr: "count(own.robots, #.parked)"`, `expr: "own.points.park_bonus"`}, "depend on each other in a circle: park_bonus, parked_count", 18},
		{"derived points on field", [2]string{"id: park_bonus, scope: alliance", "id: park_bonus, scope: field"}, "only an alliance-scope derived value can earn points", 19},
		{
			"trigger on missing element",
			[2]string{"", "triggers:\n  - on: { phase_end: auto }\n    set: { element: nope, value: \"1\" }\n"},
			`there is no element "nope"`, 22,
		},
		{
			"trigger on counter",
			[2]string{"", "triggers:\n  - on: { phase_end: auto }\n    set: { element: balls, value: \"1\" }\n"},
			"a trigger can set a boolean, enum or number element", 22,
		},
		{
			"pick outside a trigger",
			[2]string{`points: "own.parked_count == 2 ? 5 : 0"`, `points: "pick(1, 2)"`},
			"pick() may only be used in a trigger", 19,
		},
		{
			"breakdown expression error",
			[2]string{"", "breakdown:\n  - { id: total, expr: \"own.points.ball\" }\n"},
			`"own.points" has no property "ball" (did you mean "balls"?)`, 21,
		},
		{
			"order uses unknown stat",
			[2]string{"", "ranking:\n  order:\n    - { name: X, expr: \"wins + bogus\" }\n"},
			`unknown name "bogus"`, 22,
		},
		{
			"cue outside phase",
			[2]string{"id: auto, duration: 15, robots: auto", "id: auto, duration: 15, robots: auto, cues: [{ remaining: 20 }]"},
			"this cue falls outside the phase", 11,
		},
		{
			"layout references missing alliance",
			[2]string{"", "layout:\n  scoring_panels:\n    - { id: p, alliances: [green] }\n"},
			`there is no alliance "green"`, 22,
		},
		{
			"rule references missing foul type",
			[2]string{"", "fouls:\n  types: [{ id: minor, points: 2 }]\n  rules: [{ id: G1, foul: major }]\n"},
			`there is no foul type "major"`, 22,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := base + tc.replace[1]
			if tc.replace[0] != "" {
				assert.Contains(t, base, tc.replace[0], "the test's replacement target is missing from the base game")
				src = strings.Replace(base, tc.replace[0], tc.replace[1], 1)
			}
			game, diagnostics := Parse([]byte(src))
			assert.Nil(t, game)
			if assert.True(t, diagnostics.HasErrors()) {
				found := false
				for _, diagnostic := range diagnostics {
					if strings.Contains(diagnostic.Message, tc.message) {
						found = true
						if tc.line > 0 {
							assert.Equal(t, tc.line, diagnostic.Line, diagnostic.String())
						}
					}
				}
				assert.True(t, found, "no diagnostic contains %q; got:\n%s", tc.message, diagnostics.Error())
			}
		})
	}
}

func TestLoadDirRejectsMismatchedID(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(dir, "other-name"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "other-name", GameFile), []byte(base), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("ignored"), 0644))
	games, problems, err := LoadDir(dir)
	assert.NoError(t, err)
	assert.Empty(t, games)
	if assert.Contains(t, problems, "other-name") {
		assert.Contains(t, problems["other-name"].Error(), `id "test-game" must match the directory name "other-name"`)
	}
}

func TestEmptyAndMissingFiles(t *testing.T) {
	game, diagnostics := Parse(nil)
	assert.Nil(t, game)
	assert.True(t, diagnostics.HasErrors())

	game, diagnostics = LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.Nil(t, game)
	assert.True(t, diagnostics.HasErrors())
}

func TestDiagnosticString(t *testing.T) {
	d := Diagnostic{Severity: "error", Path: "/elements/0/id", Line: 14, Message: "bad"}
	assert.Equal(t, "error: line 14 (/elements/0/id): bad", d.String())
	assert.Equal(t, "error: /x: bad", Diagnostic{Severity: "error", Path: "/x", Message: "bad"}.String())
	assert.Equal(t, fmt.Sprint(Diagnostics{d}.Error()), d.String())
}
