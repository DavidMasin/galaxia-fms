// Checking a lighting configuration against the active game and the known devices, and compiling it into the form
// the engine runs: fixtures with their LED ranges, and for every target a look per moment, with its colours worked
// out and its expressions compiled once.

package lighting

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The longest period a look may have, so that a typo of a few zeros is caught.
const maxPeriodMs = 60000

var patterns = []string{
	device.PatternOff, device.PatternSolid, device.PatternBlink, device.PatternPulse, device.PatternChase,
	device.PatternRainbow, device.PatternProgress,
}

// The patterns each kind of fixture can show. A digital or relay output is on, off or blinking; a pwm output can
// also show a progress value as its level.
var kindPatterns = map[string][]string{
	KindLEDStrip: patterns,
	KindDigital:  {device.PatternOff, device.PatternSolid, device.PatternBlink},
	KindRelay:    {device.PatternOff, device.PatternSolid, device.PatternBlink},
	KindPWM:      {device.PatternOff, device.PatternSolid, device.PatternBlink, device.PatternProgress},
}

// Environment is what a lighting configuration is checked and compiled against.
type Environment struct {
	Game     *gamedef.Game
	Stations []string // Ids of the field's driver stations, for expressions.
	// The devices the FMS knows, by id, with their manifests; a nil manifest is a device that has not sent one.
	Manifests map[string]*device.Manifest
}

// Problem is something wrong with a lighting configuration. An error keeps a fixture or a cell from working; a
// warning does not.
type Problem struct {
	Fixture string `json:"fixture,omitempty"`
	Moment  string `json:"moment,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"`
	// What a stored cell names that does not exist (unknownMoment or unknownTarget): such a cell is kept, since
	// another game may make sense of it, but a cell being set now must make sense.
	unknown string
}

const (
	unknownMoment = "moment"
	unknownTarget = "target"
)

func (problem Problem) String() string {
	switch {
	case problem.Moment != "":
		return fmt.Sprintf("%s at %s: %s", problem.Target, problem.Moment, problem.Message)
	case problem.Fixture != "":
		return fmt.Sprintf("%s: %s", problem.Fixture, problem.Message)
	}
	return problem.Message
}

// Problems is a list of problems.
type Problems []Problem

// Errors returns the problems that are not warnings.
func (problems Problems) Errors() Problems {
	var errors Problems
	for _, problem := range problems {
		if !problem.Warning {
			errors = append(errors, problem)
		}
	}
	return errors
}

// Err returns nil if there are no errors, and otherwise one error listing them.
func (problems Problems) Err() error {
	errors := problems.Errors()
	if len(errors) == 0 {
		return nil
	}
	messages := make([]string, len(errors))
	for i, problem := range errors {
		messages[i] = problem.String()
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}

// program is a configuration ready to run. It never changes once compiled, so the arena goroutine and the pages
// can read it at the same time; what changes from tick to tick is kept by whoever renders it.
type program struct {
	game        *gamedef.Game
	config      *Config
	moments     []Moment
	momentIndex map[string]int
	phaseIndex  map[string]int // Phase id -> moment index.
	fixtures    []*fixtureProgram
	byID        map[string]*fixtureProgram
	cells       []*cellProgram
	colors      map[string]string // Alliance id -> #rrggbb.
	expressions bool              // Some cell has an expression, so the field's extra roots are needed.
	problems    Problems
}

type fixtureProgram struct {
	fixture  *Fixture
	ref      device.ChannelRef
	kind     string
	pixels   int
	limit    float64
	alliance string // The fixture's alliance, if the game has it.
	runnable bool   // The fixture has no errors of its own.
	whole    *targetProgram
	segments []*targetProgram
	ranges   []ledRange // Sorted, covering the strip from LED 0.
}

type targetProgram struct {
	id       string
	alliance string           // Sets own, and the colour "alliance" of a segment.
	cells    []*cellProgram   // By moment index; nil where the cell is empty or broken.
	segment  *Segment         // Nil for the whole fixture.
	fixture  *fixtureProgram  // The fixture it belongs to.
	looks    map[string]*Look // By moment id, as configured.
}

// ledRange is a run of a strip's LEDs that is either one segment or a gap between segments.
type ledRange struct {
	start, count int
	segment      *targetProgram // Nil for a gap.
	alliance     string         // What the colour "alliance" means on these LEDs.
}

type cellProgram struct {
	index   int
	moment  int
	target  *targetProgram
	look    *Look
	pattern string
	colors  []string // #rrggbb, ColorAlliance or ColorWinner.
	ms      int
	br      float64
	fill    *float64 // A constant v.
	v       *rules.Program
	when    *rules.Program
	own     string
}

// Validate checks a configuration and returns everything wrong with it.
func Validate(config *Config, env Environment) Problems {
	return compile(config, env).problems
}

type compiler struct {
	env   Environment
	prog  *program
	roots map[bool]map[string]*rules.Shape // By whether own is defined.
}

func compile(config *Config, env Environment) *program {
	if config == nil {
		config = &Config{}
	}
	prog := &program{
		game: env.Game, config: config, moments: Moments(env.Game), momentIndex: make(map[string]int),
		phaseIndex: make(map[string]int), byID: make(map[string]*fixtureProgram), colors: make(map[string]string),
	}
	c := &compiler{env: env, prog: prog, roots: make(map[bool]map[string]*rules.Shape)}
	for i, moment := range prog.moments {
		prog.momentIndex[moment.ID] = i
		if moment.Kind == MomentKindPhase {
			prog.phaseIndex[strings.TrimPrefix(moment.ID, phasePrefix)] = i
		}
	}
	if env.Game != nil {
		for _, alliance := range env.Game.Alliances.List {
			prog.colors[alliance.ID] = strings.ToLower(alliance.Color)
		}
	}

	channels := make(map[device.ChannelRef]string)
	for _, fixture := range config.Fixtures {
		fp := c.fixture(fixture, channels)
		prog.fixtures = append(prog.fixtures, fp)
		if _, taken := prog.byID[fixture.ID]; !taken {
			prog.byID[fixture.ID] = fp
		}
	}

	// Cells, by moment priority and then target, so that problems are listed in grid order.
	targets := make(map[string]*targetProgram)
	for _, fp := range prog.fixtures {
		if prog.byID[fp.fixture.ID] != fp {
			continue
		}
		targets[fp.whole.id] = fp.whole
		for _, segment := range fp.segments {
			targets[segment.id] = segment
		}
	}
	momentIds := sortedKeys(config.Scenes)
	sort.SliceStable(momentIds, func(i, j int) bool {
		a, aKnown := prog.momentIndex[momentIds[i]]
		b, bKnown := prog.momentIndex[momentIds[j]]
		if aKnown != bKnown {
			return aKnown
		}
		return a < b
	})
	for _, momentId := range momentIds {
		row := config.Scenes[momentId]
		momentIndex, knownMoment := prog.momentIndex[momentId]
		for _, targetId := range sortedKeys(row) {
			look := row[targetId]
			target := targets[targetId]
			fixtureId, _, _ := strings.Cut(targetId, "/")
			switch {
			case target == nil:
				c.prog.problems = append(c.prog.problems, Problem{
					Fixture: fixtureId, Moment: momentId, Target: targetId, Warning: true, unknown: unknownTarget,
					Message: fmt.Sprintf("there is no fixture or segment %q; the cell is ignored", targetId),
				})
				continue
			case !knownMoment:
				c.prog.problems = append(c.prog.problems, Problem{
					Fixture: fixtureId, Moment: momentId, Target: targetId, Warning: true, unknown: unknownMoment,
					Message: fmt.Sprintf("%q is not a moment of %s; the cell is ignored", momentId, c.gameName()),
				})
				continue
			}
			target.looks[momentId] = look
			if cell := c.cell(target, momentIndex, momentId, look); cell != nil {
				target.cells[momentIndex] = cell
			}
		}
	}
	return prog
}

func (c *compiler) gameName() string {
	if c.env.Game == nil {
		return "the active game"
	}
	return fmt.Sprintf("game %q", c.env.Game.ID)
}

// fixture checks one fixture and works out its LED ranges.
func (c *compiler) fixture(fixture *Fixture, channels map[device.ChannelRef]string) *fixtureProgram {
	id := fixture.ID
	errorsBefore := len(c.prog.problems.Errors())
	fail := func(format string, args ...any) {
		c.prog.problems = append(c.prog.problems, Problem{Fixture: id, Message: fmt.Sprintf(format, args...)})
	}
	warn := func(format string, args ...any) {
		c.prog.problems = append(c.prog.problems, Problem{
			Fixture: id, Message: fmt.Sprintf(format, args...), Warning: true,
		})
	}
	fp := &fixtureProgram{
		fixture: fixture, ref: device.ChannelRef{Device: fixture.Device, Channel: fixture.Channel},
		kind: fixture.Kind, pixels: fixture.Pixels, limit: fixture.limit(),
	}

	switch {
	case !idPattern.MatchString(id):
		fail("fixture id %q must be 1 to 40 lowercase letters, digits, dashes and underscores", id)
	case c.prog.byID[id] != nil:
		fail("the id is used by another fixture")
	}
	if !device.ValidDeviceID(fixture.Device) {
		fail("%q is not a valid device id", fixture.Device)
	} else if fixture.Device == device.PlcDeviceID {
		fail("the PLC's coils are driven by the arena")
	}
	if !device.ValidChannelID(fixture.Channel) {
		fail("%q is not a valid channel id", fixture.Channel)
	}
	if other := channels[fp.ref]; other != "" {
		fail("channel %s is already shown by fixture %q", fp.ref, other)
	} else {
		channels[fp.ref] = id
	}
	if !kinds[fixture.Kind] {
		fail("kind must be led_strip, digital, relay or pwm, not %q", fixture.Kind)
	}
	if fixture.Brightness != nil && (*fixture.Brightness < 0 || *fixture.Brightness > 1) {
		fail("brightness is a limit from 0 to 1, not %g", *fixture.Brightness)
	}
	if fixture.Alliance != "" {
		if c.env.Game != nil && c.env.Game.Alliance(fixture.Alliance) == nil {
			fail("%q is not an alliance of %s (%s)", fixture.Alliance, c.gameName(), c.allianceList())
		} else {
			fp.alliance = fixture.Alliance
		}
	}

	// The device's manifest has the last word on what the channel is.
	manifest, known := c.env.Manifests[fixture.Device]
	switch {
	case !device.ValidDeviceID(fixture.Device) || fixture.Device == device.PlcDeviceID:
	case !known:
		warn("device %q has not connected yet; the fixture is driven once it does", fixture.Device)
	case manifest == nil:
		warn("device %q has not sent its manifest yet; the fixture is checked when it does", fixture.Device)
	default:
		channel := manifest.Channel(fixture.Channel)
		switch {
		case channel == nil:
			fail("device %q has no channel %q", fixture.Device, fixture.Channel)
		case channel.Dir != device.DirOut:
			fail("channel %q of device %q is an input", fixture.Channel, fixture.Device)
		case channel.Type != fixture.Kind:
			fail("channel %q of device %q is a %s, not a %s", fixture.Channel, fixture.Device, channel.Type,
				fixture.Kind)
		case channel.Type == KindLEDStrip:
			if fixture.Pixels != 0 && fixture.Pixels != channel.Pixels {
				warn("the strip has %d LEDs now (it had %d when the fixture was saved)", channel.Pixels,
					fixture.Pixels)
			}
			fp.pixels = channel.Pixels
		}
	}

	fp.whole = &targetProgram{
		id: id, alliance: fp.alliance, cells: make([]*cellProgram, len(c.prog.moments)), fixture: fp,
		looks: make(map[string]*Look),
	}
	if fixture.Kind == KindLEDStrip {
		if fp.pixels < 1 {
			fail("a led_strip fixture needs its number of LEDs (pixels)")
		}
		c.segments(fp, fail)
	} else if len(fixture.Segments) > 0 {
		fail("only a led_strip fixture has segments")
	}
	fp.runnable = len(c.prog.problems.Errors()) == errorsBefore
	return fp
}

// segments checks the segments of a strip and divides the strip into ranges: the segments, and the gaps around
// them.
func (c *compiler) segments(fp *fixtureProgram, fail func(format string, args ...any)) {
	seen := make(map[string]bool)
	var sorted []*targetProgram
	for i := range fp.fixture.Segments {
		segment := &fp.fixture.Segments[i]
		ok := true
		switch {
		case !idPattern.MatchString(segment.ID):
			fail("segment id %q must be 1 to 40 lowercase letters, digits, dashes and underscores", segment.ID)
			ok = false
		case seen[segment.ID]:
			fail("two segments are called %q", segment.ID)
			ok = false
		}
		seen[segment.ID] = true
		if segment.Start < 0 || segment.Count < 1 {
			fail("segment %q needs a start of 0 or more and a count of 1 or more", segment.ID)
			ok = false
		} else if fp.pixels > 0 && segment.Start+segment.Count > fp.pixels {
			fail("segment %q (LEDs %d to %d) runs past the end of the strip, which has %d LEDs", segment.ID,
				segment.Start, segment.Start+segment.Count-1, fp.pixels)
			ok = false
		}
		alliance := fp.alliance
		if segment.Alliance != "" {
			if c.env.Game != nil && c.env.Game.Alliance(segment.Alliance) == nil {
				fail("segment %q: %q is not an alliance of %s (%s)", segment.ID, segment.Alliance, c.gameName(),
					c.allianceList())
			} else {
				alliance = segment.Alliance
			}
		}
		target := &targetProgram{
			id: SegmentTarget(fp.fixture.ID, segment.ID), alliance: alliance, segment: segment, fixture: fp,
			cells: make([]*cellProgram, len(c.prog.moments)), looks: make(map[string]*Look),
		}
		fp.segments = append(fp.segments, target)
		if ok {
			sorted = append(sorted, target)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].segment.Start < sorted[j].segment.Start })
	next := 0
	for i, target := range sorted {
		segment := target.segment
		if i > 0 && segment.Start < next {
			fail("segments %q and %q overlap", sorted[i-1].segment.ID, segment.ID)
			continue
		}
		if segment.Start > next {
			fp.ranges = append(fp.ranges, ledRange{start: next, count: segment.Start - next, alliance: fp.alliance})
		}
		fp.ranges = append(fp.ranges, ledRange{
			start: segment.Start, count: segment.Count, segment: target, alliance: target.alliance,
		})
		next = segment.Start + segment.Count
	}
	if next < fp.pixels {
		fp.ranges = append(fp.ranges, ledRange{start: next, count: fp.pixels - next, alliance: fp.alliance})
	}
}

func (c *compiler) allianceList() string {
	if c.env.Game == nil {
		return ""
	}
	return strings.Join(c.env.Game.AllianceIDs(), ", ")
}

// cell checks and compiles one look. It returns nil if the look has errors.
func (c *compiler) cell(target *targetProgram, momentIndex int, momentId string, look *Look) *cellProgram {
	fp := target.fixture
	errorsBefore := len(c.prog.problems.Errors())
	problem := func(warning bool, format string, args ...any) {
		c.prog.problems = append(c.prog.problems, Problem{
			Fixture: fp.fixture.ID, Moment: momentId, Target: target.id, Message: fmt.Sprintf(format, args...),
			Warning: warning,
		})
	}
	fail := func(format string, args ...any) { problem(false, format, args...) }
	cell := &cellProgram{
		moment: momentIndex, target: target, look: look, pattern: look.P, ms: look.Ms, br: 1, own: target.alliance,
	}
	if cell.pattern == "" {
		cell.pattern = device.PatternSolid
	}

	allowed := kindPatterns[fp.kind]
	if allowed == nil {
		allowed = patterns
	}
	if !contains(allowed, cell.pattern) {
		if contains(patterns, cell.pattern) {
			fail("a %s fixture can show %s, not %s", fp.kind, strings.Join(allowed, ", "), cell.pattern)
		} else {
			fail("%q is not a pattern (%s)", cell.pattern, strings.Join(patterns, ", "))
		}
	}
	if look.Ms < 0 || look.Ms > maxPeriodMs {
		fail("the period is in milliseconds, from 0 (the pattern's default) to %d, not %d", maxPeriodMs, look.Ms)
	}
	if look.Br != nil {
		if *look.Br < 0 || *look.Br > 1 {
			fail("brightness is from 0 to 1, not %g", *look.Br)
		} else {
			cell.br = *look.Br
		}
	}

	// Colours. Only a strip shows them; the others are on or off.
	if len(look.C) > 2 {
		fail("a look has at most two colours, not %d", len(look.C))
	}
	usesAlliance := false
	for _, color := range look.C {
		switch {
		case color == ColorAlliance:
			usesAlliance = true
			cell.colors = append(cell.colors, ColorAlliance)
		case color == ColorWinner:
			cell.colors = append(cell.colors, ColorWinner)
		case color == ColorOff:
			cell.colors = append(cell.colors, "#000000")
		case colorPattern.MatchString(color):
			cell.colors = append(cell.colors, strings.ToLower(color))
		default:
			fail("%q is not a colour (#rrggbb, %s, %s or %s)", color, ColorAlliance, ColorWinner, ColorOff)
		}
	}
	if usesAlliance && fp.kind == KindLEDStrip && !c.anyAlliance(target) {
		problem(true, "the colour %q needs an alliance on the fixture or segment; it shows white", ColorAlliance)
	}

	// Expressions.
	compileExpr := func(what, src string) *rules.Program {
		roots := c.roots[cell.own != ""]
		if roots == nil {
			if c.env.Game == nil {
				fail("%s %q: there is no active game to evaluate it against", what, src)
				return nil
			}
			roots = device.OutputRoots(c.env.Game, c.env.Stations, cell.own != "")
			c.roots[cell.own != ""] = roots
		}
		program, err := rules.Compile(src, roots, rules.Options{})
		if err != nil {
			// own and opp exist only for LEDs that belong to an alliance, which is easy to miss.
			hint := ""
			message := err.Error()
			if cell.own == "" &&
				(strings.Contains(message, `unknown name "own"`) || strings.Contains(message, `unknown name "opp"`)) {
				hint = "; own and opp name the alliance of the fixture or segment, so give it one first"
			}
			fail("%s %q: %v%s", what, src, err, hint)
			return nil
		}
		c.prog.expressions = true
		return program
	}
	if look.When != "" {
		cell.when = compileExpr("when", look.When)
	}
	switch {
	case cell.pattern == device.PatternProgress && strings.TrimSpace(look.V) == "":
		fail("progress needs v, the fill from 0 to 1 (a number or an expression)")
	case cell.pattern != device.PatternProgress && look.V != "":
		fail("v is the fill of the progress pattern; %s does not use it", cell.pattern)
	case look.V != "":
		if number, err := strconv.ParseFloat(strings.TrimSpace(look.V), 64); err == nil {
			if number < 0 || number > 1 || math.IsNaN(number) {
				fail("v is a fill from 0 to 1, not %g", number)
			}
			cell.fill = &number
		} else {
			cell.v = compileExpr("v", look.V)
		}
	}
	if len(c.prog.problems.Errors()) > errorsBefore {
		return nil
	}
	cell.index = len(c.prog.cells)
	c.prog.cells = append(c.prog.cells, cell)
	return cell
}

// anyAlliance reports whether the colour "alliance" means something somewhere on a target.
func (c *compiler) anyAlliance(target *targetProgram) bool {
	if target.alliance != "" {
		return true
	}
	if target.segment != nil {
		return false
	}
	for _, segment := range target.fixture.segments {
		if segment.alliance != "" {
			return true
		}
	}
	return false
}

func contains(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}
	return false
}
