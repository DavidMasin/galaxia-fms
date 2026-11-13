// Rendering: which look each fixture, segment and gap shows for a list of moments, and the device command that
// shows it.

package lighting

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/rules"
	"math"
	"strconv"
	"time"
)

const (
	white             = "#ffffff"
	black             = "#000000"
	defaultLevelBlink = time.Second // As for digital output bindings.
	// A fill that cannot be worked out in Test or Preview (outside the match a clock-based fill often cannot) is
	// shown half full, so that the look can still be seen.
	sampleFill = 0.5
)

// evaluator works out which cells apply and what their fills are, once per pass over the fixtures: a cell drawn
// on several ranges of a strip is evaluated once.
type evaluator struct {
	snapshot *device.Snapshot
	devices  *device.Manager
	extras   map[string]any
	memo     []cellState // By cell index.
	stamp    uint64
	// In Test and Preview the chosen moment is shown as designed: conditions are taken as true, and a fill that
	// cannot be worked out shows half.
	designed bool
}

type cellState struct {
	stamp   uint64
	applies bool
	fill    float64
	err     string
}

// begin starts a pass over the fixtures of a program.
func (ev *evaluator) begin(prog *program, snapshot *device.Snapshot, devices *device.Manager, designed bool) {
	if len(ev.memo) != len(prog.cells) {
		ev.memo = make([]cellState, len(prog.cells))
		ev.stamp = 0
	}
	ev.stamp++
	ev.snapshot, ev.devices, ev.extras, ev.designed = snapshot, devices, nil, designed
	if snapshot != nil && snapshot.Game != prog.game {
		// Expressions compiled for one game cannot be run against another's score.
		ev.snapshot = nil
	}
}

// state evaluates a cell, once per pass.
func (ev *evaluator) state(cell *cellProgram) *cellState {
	state := &ev.memo[cell.index]
	if state.stamp == ev.stamp {
		return state
	}
	*state = cellState{stamp: ev.stamp, applies: true}
	if cell.when != nil && !ev.designed {
		value, err := ev.run(cell.when, cell.own)
		if err != nil {
			state.applies, state.err = false, fmt.Sprintf("when %q: %v", cell.look.When, err)
			return state
		}
		state.applies = rules.Truthy(value)
	}
	switch {
	case cell.fill != nil:
		state.fill = *cell.fill
	case cell.v != nil:
		fill, err := ev.fraction(cell)
		switch {
		case err == nil:
			state.fill = fill
		case ev.designed:
			state.fill = sampleFill
		default:
			state.applies, state.err = false, fmt.Sprintf("v %q: %v", cell.look.V, err)
		}
	}
	return state
}

func (ev *evaluator) applies(cell *cellProgram) bool {
	return ev.state(cell).applies
}

// fraction evaluates a fill, clamped to 0..1.
func (ev *evaluator) fraction(cell *cellProgram) (float64, error) {
	value, err := ev.run(cell.v, cell.own)
	if err != nil {
		return 0, err
	}
	fill, ok := rules.ToNumber(value)
	if !ok || math.IsNaN(fill) {
		return 0, fmt.Errorf("gave %v, which is not a number", value)
	}
	return math.Max(0, math.Min(1, fill)), nil
}

// run evaluates an expression against the live score, as output bindings are.
func (ev *evaluator) run(program *rules.Program, own string) (value any, err error) {
	if ev.snapshot == nil || ev.snapshot.Eval == nil {
		return nil, fmt.Errorf("the field state is not available yet")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			value, err = nil, fmt.Errorf("evaluation failed: %v", recovered)
		}
	}()
	if ev.extras == nil {
		ev.extras = ev.devices.OutputExtraRoots(ev.snapshot)
	}
	return ev.snapshot.Eval(program, own, ev.extras)
}

// renderer draws fixtures for a list of moments, highest priority first.
type renderer struct {
	prog   *program
	eval   *evaluator
	active []int // Moment indices.
	winner string
	picks  []*cellProgram // Scratch, one per range of the strip being drawn.
}

// pick returns the look a range of a strip shows: walking down the moments, the first that has an applicable look
// for its segment, or failing that for the whole fixture. An empty or inapplicable cell falls through.
func (r *renderer) pick(fp *fixtureProgram, segment *targetProgram) *cellProgram {
	for _, moment := range r.active {
		if segment != nil {
			if cell := segment.cells[moment]; cell != nil && r.eval.applies(cell) {
				return cell
			}
		}
		if cell := fp.whole.cells[moment]; cell != nil && r.eval.applies(cell) {
			return cell
		}
	}
	return nil
}

// strip works out the command of a led_strip fixture.
func (r *renderer) strip(fp *fixtureProgram) *device.Command {
	if cap(r.picks) < len(fp.ranges) {
		r.picks = make([]*cellProgram, len(fp.ranges))
	}
	picks := r.picks[:len(fp.ranges)]
	lit, uniform, maxBr := false, true, 0.0
	for i := range fp.ranges {
		picks[i] = r.pick(fp, fp.ranges[i].segment)
		if picks[i] != nil {
			lit = true
			maxBr = math.Max(maxBr, picks[i].br)
		}
		if picks[i] != picks[0] || (usesAlliance(picks[i]) && fp.ranges[i].alliance != fp.ranges[0].alliance) {
			uniform = false
		}
	}
	if !lit {
		return &device.Command{P: device.PatternOff}
	}
	if uniform {
		// One look over the whole strip, so that a chase or a rainbow runs its full length.
		cell := picks[0]
		command := &device.Command{
			P: cell.pattern, C: r.colors(cell, fp.ranges[0].alliance, 1), Ms: cell.ms,
			Br: brightness(fp.limit * cell.br),
		}
		if cell.pattern == device.PatternProgress {
			command.C, command.V = progress(command.C, r.eval.state(cell).fill, 0, fp.pixels, fp.pixels,
				fp.fixture.Reverse)
		}
		return command
	}

	// Pieces: neighbouring ranges that show the same look in the same colours are drawn as one. The command's
	// brightness is the brightest piece's; the others are dimmed through their colours, which is the same thing
	// for every pattern but the rainbow, whose colours are its own.
	command := &device.Command{Br: brightness(fp.limit * maxBr)}
	for i := 0; i < len(picks); {
		cell, start, count, alliance := picks[i], fp.ranges[i].start, fp.ranges[i].count, fp.ranges[i].alliance
		j := i + 1
		for j < len(picks) && picks[j] == cell && (!usesAlliance(cell) || fp.ranges[j].alliance == alliance) {
			count += fp.ranges[j].count
			j++
		}
		i = j
		if cell == nil {
			continue
		}
		scale := 1.0
		if maxBr > 0 {
			scale = cell.br / maxBr
		}
		segment := device.Segment{S: start, N: count, P: cell.pattern, C: r.colors(cell, alliance, scale), Ms: cell.ms}
		if cell.pattern == device.PatternProgress {
			// A look of the whole fixture split around a segment still fills the strip as one bar.
			length, offset := count, 0
			if cell.target.segment == nil {
				length, offset = fp.pixels, start
			}
			segment.C, segment.V = progress(segment.C, r.eval.state(cell).fill, offset, count, length,
				fp.fixture.Reverse)
		}
		if fp.fixture.Reverse {
			segment.S = fp.pixels - start - count
		}
		command.Seg = append(command.Seg, segment)
	}
	return command
}

// progress works out the colours and fill of a progress piece of count LEDs that starts offset LEDs into a bar of
// length LEDs. The fill is a whole number of LEDs, so that a fill creeping with the clock is only resent when a LED
// changes. A reversed fixture fills from its own LED 0, which is the device's last: the colours are swapped and
// the fill turned round.
func progress(colors []string, fill float64, offset, count, length int, reverse bool) ([]string, *float64) {
	lit := int(math.Round(fill*float64(length))) - offset
	lit = max(0, min(count, lit))
	if reverse {
		on, off := black, black
		if len(colors) > 0 {
			on = colors[0]
		}
		if len(colors) > 1 {
			off = colors[1]
		}
		colors = []string{off, on}
		lit = count - lit
	}
	value := math.Round(float64(lit)/float64(count)*10000) / 10000
	return colors, &value
}

func usesAlliance(cell *cellProgram) bool {
	if cell == nil {
		return false
	}
	for _, color := range cell.colors {
		if color == ColorAlliance {
			return true
		}
	}
	return false
}

// colors resolves the colours of a look drawn on LEDs of the given alliance, dimmed by scale.
func (r *renderer) colors(cell *cellProgram, alliance string, scale float64) []string {
	if len(cell.colors) == 0 {
		return nil
	}
	colors := make([]string, len(cell.colors))
	for i, color := range cell.colors {
		switch color {
		case ColorAlliance:
			color = r.prog.colors[alliance]
			if color == "" {
				color = white
			}
		case ColorWinner:
			color = r.winner
		}
		if scale < 1 {
			color = scaleColor(color, scale)
		}
		colors[i] = color
	}
	return colors
}

// winnerColor is the colour "winner" stands for: the winning alliance's, or white after a tie or before any match
// has been posted.
func (prog *program) winnerColor(winner string) string {
	if color := prog.colors[winner]; color != "" {
		return color
	}
	return white
}

func scaleColor(color string, scale float64) string {
	value, err := strconv.ParseUint(color[1:], 16, 32)
	if err != nil || len(color) != 7 {
		return color
	}
	channel := func(shift uint) uint64 {
		return uint64(math.Round(float64((value>>shift)&0xff) * scale))
	}
	return fmt.Sprintf("#%02x%02x%02x", channel(16), channel(8), channel(0))
}

// brightness is the br of a command: left out at full brightness, rounded to a thousandth otherwise.
func brightness(value float64) *float64 {
	if value >= 1 {
		return nil
	}
	value = math.Round(math.Max(0, value)*1000) / 1000
	return &value
}

// single returns the look a digital, relay or pwm fixture shows.
func (r *renderer) single(fp *fixtureProgram) *cellProgram {
	for _, moment := range r.active {
		if cell := fp.whole.cells[moment]; cell != nil && r.eval.applies(cell) {
			return cell
		}
	}
	return nil
}

// levelState is what a digital, relay or pwm fixture remembers between ticks: the look it showed, and since when,
// which is where its blinking counts from (as for an output binding, it starts on).
type levelState struct {
	cell  *cellProgram
	since time.Time
}

// level works out the command of a digital, relay or pwm fixture showing a look: {"v":1} or {"v":0}, or for pwm
// the level, blinking from the moment the look took effect.
func (r *renderer) level(fp *fixtureProgram, cell *cellProgram, state *levelState, now time.Time) *device.Command {
	if cell != state.cell || state.since.IsZero() {
		state.cell, state.since = cell, now
	}
	if cell == nil || cell.pattern == device.PatternOff {
		return levelCommand(0)
	}
	high := r.high(fp, cell)
	if cell.pattern == device.PatternBlink {
		period := time.Duration(cell.ms) * time.Millisecond
		if period <= 0 {
			period = defaultLevelBlink
		}
		if now.Sub(state.since)%period >= period/2 {
			high = 0
		}
	}
	return levelCommand(high)
}

// high is the level a fixture shows while a look is on: 1 for digital and relay, and for pwm the brightness (and
// the fill of a progress look).
func (r *renderer) high(fp *fixtureProgram, cell *cellProgram) float64 {
	if fp.kind != KindPWM {
		return 1
	}
	high := fp.limit * cell.br
	if cell.pattern == device.PatternProgress {
		high *= r.eval.state(cell).fill
	}
	return high
}

// designedLevel is how the page draws a digital, relay or pwm fixture showing a look in Preview: blinking is drawn
// by the browser from the pattern.
func (r *renderer) designedLevel(fp *fixtureProgram, cell *cellProgram) *device.Command {
	if cell == nil || cell.pattern == device.PatternOff {
		return levelCommand(0)
	}
	high := r.high(fp, cell)
	if cell.pattern != device.PatternBlink {
		return levelCommand(high)
	}
	ms := cell.ms
	if ms <= 0 {
		ms = int(defaultLevelBlink / time.Millisecond)
	}
	return &device.Command{P: device.PatternBlink, C: []string{scaleColor(white, high)}, Ms: ms}
}

func levelCommand(level float64) *device.Command {
	level = math.Round(math.Max(0, math.Min(1, level))*1000) / 1000
	return &device.Command{V: &level}
}

// sameCommand reports whether two commands would send the same message, without encoding them.
func sameCommand(a, b *device.Command) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if !sameNumber(a.V, b.V) || a.P != b.P || !sameStrings(a.C, b.C) || a.Ms != b.Ms || !sameNumber(a.Br, b.Br) ||
		len(a.Seg) != len(b.Seg) || a.Play != b.Play || a.At != b.At || a.Transient != b.Transient ||
		(a.Text == nil) != (b.Text == nil) || (a.Text != nil && *a.Text != *b.Text) {
		return false
	}
	for i := range a.Seg {
		x, y := &a.Seg[i], &b.Seg[i]
		if x.S != y.S || x.N != y.N || x.P != y.P || !sameStrings(x.C, y.C) || x.Ms != y.Ms || !sameNumber(x.V, y.V) {
			return false
		}
	}
	return true
}

func sameNumber(a, b *float64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
