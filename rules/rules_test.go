package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func testRoots() map[string]*Shape {
	robot := Obj(map[string]*Shape{"climb": StringShape, "position": NumberShape})
	alliance := Obj(map[string]*Shape{
		"id":     StringShape,
		"balls":  Obj(map[string]*Shape{"total": NumberShape, "auto": NumberShape}),
		"robots": ListOf(robot),
		"points": Obj(map[string]*Shape{"balls": NumberShape}),
		"bonus":  AnyShape,
	})
	return map[string]*Shape{
		"own": alliance, "alliances": ListOf(alliance), "params": Obj(map[string]*Shape{"threshold": NumberShape}),
		"flag": BoolShape,
	}
}

func testEnv() map[string]any {
	own := map[string]any{
		"id":    "red",
		"balls": map[string]any{"total": 7, "auto": 3},
		"robots": []any{
			map[string]any{"climb": "high", "position": 1},
			map[string]any{"climb": "low", "position": 2},
			map[string]any{"climb": "high", "position": 3},
		},
		"points": map[string]any{"balls": 10.0},
		"bonus":  nil,
	}
	return map[string]any{
		"own": own, "alliances": []any{own}, "params": map[string]any{"threshold": 5.0}, "flag": true,
	}
}

func TestCompileAndRun(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"own.balls.total", 7},
		{"own.balls.total >= params.threshold ? 10 : 0", 10},
		{"count(own.robots, #.climb == 'high')", 2},
		{"sum(alliances, #.balls.auto)", 3},
		{"own.balls.total / 2", 3.5},
		{"floor(own.balls.total / 2) * 2", 6},
		{"flag", 1},
		{"not flag", 0},
		{"own.balls.total >= 7 and own.points.balls > 9", 1},
		{"clamp(own.balls.total, 0, 5)", 5},
		{"let n = count(own.robots, #.position > 1); n * 3", 6},
		{"own.bonus ?? 4", 4},
		{"max(map(own.robots, #.position))", 3},
		{"own.id == 'red' ? 1 : 2", 1},
	}
	for _, tc := range cases {
		prog, err := Compile(tc.src, testRoots(), Options{})
		if assert.NoError(t, err, tc.src) {
			got, err := prog.RunNumber(testEnv())
			assert.NoError(t, err, tc.src)
			assert.InDelta(t, tc.want, got, 1e-9, tc.src)
		}
	}
}

func TestCompileRejectsUnknownNames(t *testing.T) {
	cases := []struct {
		src     string
		message string
	}{
		{"owm.balls.total", `unknown name "owm" (did you mean "own"?)`},
		{"own.bals.total", `"own" has no property "bals" (did you mean "balls"?)`},
		{"own.balls.teleop", `"own.balls" has no property "teleop"`},
		{"count(own.robots, #.clmb == 'high')", `"#" has no property "clmb" (did you mean "climb"?)`},
		{"sum(alliances, #.balls.totl)", `"#.balls" has no property "totl" (did you mean "total"?)`},
		{"own.id.length", `"own.id" is not an object, so it has no property "length"`},
		{"filter(own.robots, #.position > 1)[0].nope", `has no property "nope"`},
		{"phase == 'auto'", `unknown name "phase"`},
		{"", "expression is empty"},
		{"own.balls.total +", "unexpected token"},
	}
	for _, tc := range cases {
		_, err := Compile(tc.src, testRoots(), Options{})
		if assert.Error(t, err, tc.src) {
			assert.Contains(t, err.Error(), tc.message, tc.src)
		}
	}
}

func TestCompileReportsPosition(t *testing.T) {
	_, err := Compile("own.balls.total +\n  own.nope", testRoots(), Options{})
	ruleError, ok := err.(*Error)
	if assert.True(t, ok) {
		assert.Equal(t, 2, ruleError.Line)
		assert.Equal(t, 6, ruleError.Column)
	}
}

func TestUntypedValuesAreNotChecked(t *testing.T) {
	// "bonus" has no declared shape, so anything may be read from it.
	_, err := Compile("own.bonus.anything.at.all", testRoots(), Options{})
	assert.NoError(t, err)
}

func TestPickOnlyWhereAllowed(t *testing.T) {
	_, err := Compile("pick('a', 'b')", testRoots(), Options{})
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "pick() may only be used in a trigger")
	}

	prog, err := Compile("pick('a', 'b')", testRoots(), Options{AllowPick: true})
	assert.NoError(t, err)
	original := PickFunc
	defer func() { PickFunc = original }()
	PickFunc = func(options []any) any { return options[1] }
	got, err := prog.Run(testEnv())
	assert.NoError(t, err)
	assert.Equal(t, "b", got)

	// The default picks one of the options.
	PickFunc = original
	for i := 0; i < 20; i++ {
		got, err = prog.Run(testEnv())
		assert.NoError(t, err)
		assert.Contains(t, []any{"a", "b"}, got)
	}
}

func TestProgramRecordsWhatItReads(t *testing.T) {
	prog, err := Compile("own.points.balls + count(own.robots, #.climb == 'high') + params.threshold", testRoots(), Options{})
	if assert.NoError(t, err) {
		assert.Equal(t, []string{"own", "params"}, prog.Roots)
		assert.Equal(t, []string{"#.climb", "own.points", "own.points.balls", "own.robots", "params.threshold"}, prog.Paths)
	}
}

func TestRunNumberRejectsNonNumbers(t *testing.T) {
	prog, err := Compile("own.id", testRoots(), Options{})
	assert.NoError(t, err)
	_, err = prog.RunNumber(testEnv())
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "expected a number")
	}

	prog, err = Compile("own.balls.total / (own.balls.total - 7)", testRoots(), Options{})
	assert.NoError(t, err)
	_, err = prog.RunNumber(testEnv())
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "not a finite number")
	}
}

func TestToNumberAndTruthy(t *testing.T) {
	for value, want := range map[any]float64{true: 1, false: 0, 3: 3, 2.5: 2.5, int64(4): 4} {
		got, ok := ToNumber(value)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	got, ok := ToNumber(nil)
	assert.True(t, ok)
	assert.Equal(t, 0.0, got)
	_, ok = ToNumber("7")
	assert.False(t, ok)

	assert.True(t, Truthy(true))
	assert.True(t, Truthy(1))
	assert.True(t, Truthy("x"))
	assert.False(t, Truthy(false))
	assert.False(t, Truthy(nil))
	assert.False(t, Truthy(0.0))
	assert.False(t, Truthy(""))
}
