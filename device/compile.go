// Checking bindings against the active game and the known devices, and compiling them into the form the manager
// runs.

package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PlcDeviceID is the id under which the field PLC appears as a device. No other device may use it.
const PlcDeviceID = "plc"

// BindingsOwner is the owner of the output channels the binding set drives, in claims.
const BindingsOwner = "bindings"

// Match events an output can pulse on, besides phase.<id>.start, phase.<id>.end and cue.<id>.
const (
	EventMatchStart  = "match.start"
	EventMatchEnd    = "match.end"
	EventMatchAbort  = "match.abort"
	EventScorePosted = "score.posted"
)

const defaultBlinkPeriod = time.Second

var (
	bindingIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	colorPattern     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	patterns         = map[string]bool{
		PatternOff: true, PatternSolid: true, PatternBlink: true, PatternPulse: true, PatternChase: true,
		PatternRainbow: true, PatternProgress: true,
	}
	fieldActions = map[string]bool{
		ActionStartMatch: true, ActionAbortMatch: true, ActionEStop: true, ActionAStop: true, ActionFieldStop: true,
		ActionFieldReset: true, ActionVolunteers: true, ActionCommit: true, ActionNextMatch: true,
	}
	scoreOps = map[string]bool{
		scoring.OpIncrement: true, scoring.OpDecrement: true, scoring.OpSet: true, scoring.OpToggle: true,
		OpHold: true, scoring.OpTimerStart: true, scoring.OpTimerStop: true,
	}
)

// Environment is what a binding set is checked and compiled against.
type Environment struct {
	Game     *gamedef.Game
	Stations []string // Ids of the field's driver stations, such as R1..B3.
	// The devices the FMS knows, by id, with their manifests; a nil manifest is a device that has not sent one.
	Manifests map[string]*Manifest
	// Output channels driven by another component (the Lighting page), and which.
	Claims map[ChannelRef]string
}

// Problem is something wrong with a binding. An error keeps the binding from working; a warning does not.
type Problem struct {
	Binding string `json:"binding,omitempty"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"`
}

func (problem Problem) String() string {
	if problem.Binding == "" {
		return problem.Message
	}
	return fmt.Sprintf("%s: %s", problem.Binding, problem.Message)
}

// Problems is a list of problems with a binding set.
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
	return fmt.Errorf("the bindings have %d problem(s): %s", len(errors), strings.Join(messages, "; "))
}

// Validate checks a binding set and returns everything wrong with it.
func Validate(set *BindingSet, env Environment) Problems {
	return compile(set, env, nil).problems
}

// What an input binding does.
type actionKind int

const (
	actionScore   actionKind = iota // Changes the score.
	actionTrigger                   // A field action fired on an edge.
	actionStop                      // A stop held while the input is active.
)

// inputBinding is an input binding ready to run, with its state.
type inputBinding struct {
	def         *InputBinding
	ref         ChannelRef
	kind        actionKind
	element     *gamedef.Element
	value       any // The value a set applies, in the type the element takes.
	amount      float64
	phases      map[string]bool
	channelType string // Empty until the device's manifest is known; the binding does nothing until then.
	safety      bool
	digital     bool // A digital channel, or an analog or encoder one with a threshold.
	debounce    time.Duration
	minInterval time.Duration
	scale       float64
	offset      float64
	state       inputState
}

// inputState is what an input binding remembers between readings.
type inputState struct {
	seeded       bool
	level        bool // The committed logical level: true is active.
	pending      bool // A new level is waiting out the debounce time.
	pendingLevel bool
	pendingSince time.Time
	above        bool // An analog reading is over the threshold, before debouncing.
	lastTrigger  time.Time
	lastSet      any // The last value an analog or text set applied, so that repeats are not applied again.
	holding      bool
}

// outputBinding is an output binding ready to run, with its state.
type outputBinding struct {
	def         *OutputBinding
	ref         ChannelRef
	channelType string
	when        *rules.Program
	value       *rules.Program
	look        *compiledLook
	elseLook    *compiledLook
	pulse       time.Duration
	// State.
	on          bool      // Whether the condition held (or the pulse ran) at the last evaluation.
	since       time.Time // When on last changed: blinking counts from here.
	pulseUntil  time.Time
	errorLogged bool
}

type compiledLook struct {
	look     *Look
	colors   []string
	v        *rules.Program
	segments []compiledSegment
}

type compiledSegment struct {
	segment *LookSegment
	colors  []string
	v       *rules.Program
}

// compiledSet is a binding set ready to run: only the bindings without errors, and what was wrong with the rest.
type compiledSet struct {
	source    *BindingSet
	inputs    []*inputBinding
	byChannel map[ChannelRef][]*inputBinding
	stops     []*inputBinding
	outputs   []*outputBinding
	driven    map[ChannelRef]*outputBinding
	problems  Problems
}

type compiler struct {
	env      Environment
	set      *compiledSet
	ids      map[string]bool
	outputs  map[ChannelRef]string // The binding that first claimed each output channel, valid or not.
	game     *gamedef.Game
	stations map[string]bool
}

// compile checks a binding set and compiles the bindings that have no errors. State is carried over from previous
// for bindings that did not change, so that recompiling (because a device sent a new manifest, say) does not lose
// a held level or a running pulse.
func compile(set *BindingSet, env Environment, previous *compiledSet) *compiledSet {
	if set == nil {
		set = &BindingSet{}
	}
	c := &compiler{
		env: env, game: env.Game, ids: make(map[string]bool), stations: make(map[string]bool),
		outputs: make(map[ChannelRef]string),
		set: &compiledSet{
			source: set, byChannel: make(map[ChannelRef][]*inputBinding),
			driven: make(map[ChannelRef]*outputBinding),
		},
	}
	for _, station := range env.Stations {
		c.stations[station] = true
	}
	if c.game == nil {
		if len(set.Inputs)+len(set.Outputs) > 0 {
			c.add("", false, "there is no active game to bind to")
		}
		return c.set
	}

	previousInputs := make(map[string]*inputBinding)
	previousOutputs := make(map[string]*outputBinding)
	if previous != nil {
		for _, binding := range previous.inputs {
			previousInputs[binding.def.ID] = binding
		}
		for _, binding := range previous.outputs {
			previousOutputs[binding.def.ID] = binding
		}
	}
	for _, input := range set.Inputs {
		binding := c.input(input)
		if binding == nil {
			continue
		}
		if old := previousInputs[input.ID]; old != nil && reflect.DeepEqual(old.def, input) &&
			old.channelType == binding.channelType {
			binding.state = old.state
		}
		c.set.inputs = append(c.set.inputs, binding)
		c.set.byChannel[binding.ref] = append(c.set.byChannel[binding.ref], binding)
		if binding.kind == actionStop {
			c.set.stops = append(c.set.stops, binding)
		}
	}
	for _, output := range set.Outputs {
		binding := c.output(output)
		if binding == nil {
			continue
		}
		if old := previousOutputs[output.ID]; old != nil && reflect.DeepEqual(old.def, output) &&
			old.channelType == binding.channelType {
			binding.on, binding.since, binding.pulseUntil = old.on, old.since, old.pulseUntil
		}
		c.set.outputs = append(c.set.outputs, binding)
		c.set.driven[binding.ref] = binding
	}
	return c.set
}

func (c *compiler) add(binding string, warning bool, format string, args ...any) {
	c.set.problems = append(
		c.set.problems, Problem{Binding: binding, Message: fmt.Sprintf(format, args...), Warning: warning},
	)
}

// checkCommon checks what every binding has: a unique id and a device channel. It reports whether they are usable.
func (c *compiler) checkCommon(kind, id, deviceID, channel string) bool {
	ok := true
	switch {
	case !bindingIDPattern.MatchString(id):
		c.add(id, false, "%s binding id %q must be 1 to 64 letters, digits, dots, dashes and underscores", kind, id)
		ok = false
	case c.ids[id]:
		c.add(id, false, "the id is used by another binding")
		ok = false
	}
	c.ids[id] = true
	if !ValidDeviceID(deviceID) {
		c.add(id, false, "device %q is not a valid device id (lowercase letters, digits and dashes)", deviceID)
		ok = false
	}
	if !ValidChannelID(channel) {
		c.add(id, false, "channel %q is not a valid channel id (lowercase letters, digits and underscores)", channel)
		ok = false
	}
	return ok
}

// channel looks the bound channel up in the device's manifest. It returns nil, with a warning, if the device has
// not told the FMS about its channels yet, and nil, with an error, if it has and the channel is not right.
func (c *compiler) channel(id, deviceID, channelID, dir string) (*Channel, bool) {
	manifest, known := c.env.Manifests[deviceID]
	if !known {
		c.add(id, true, "device %q has not connected yet; the binding is checked against it when it does", deviceID)
		return nil, true
	}
	if manifest == nil {
		c.add(id, true, "device %q has not sent its manifest yet; the binding is checked when it does", deviceID)
		return nil, true
	}
	channel := manifest.Channel(channelID)
	if channel == nil {
		c.add(id, false, "device %q has no channel %q", deviceID, channelID)
		return nil, false
	}
	if channel.Dir != dir {
		what := map[string]string{DirIn: "an input", DirOut: "an output"}
		c.add(id, false, "channel %q of device %q is %s, not %s", channelID, deviceID, what[channel.Dir], what[dir])
		return nil, false
	}
	return channel, true
}

func (c *compiler) input(def *InputBinding) *inputBinding {
	errorsBefore := len(c.set.problems.Errors())
	if !c.checkCommon("input", def.ID, def.Device, def.Channel) {
		return nil
	}
	id := def.ID
	fail := func(format string, args ...any) { c.add(id, false, format, args...) }
	binding := &inputBinding{
		def: def, ref: ChannelRef{Device: def.Device, Channel: def.Channel}, phases: make(map[string]bool),
		debounce: time.Duration(def.DebounceMs) * time.Millisecond, scale: 1,
		minInterval: time.Duration(def.MinIntervalMs) * time.Millisecond,
	}
	channel, _ := c.channel(id, def.Device, def.Channel, DirIn)
	known := channel != nil
	if known {
		binding.channelType, binding.safety = channel.Type, channel.Safety
	}
	analogType := binding.channelType == TypeAnalog || binding.channelType == TypeEncoder
	binding.digital = binding.channelType == TypeDigital || (analogType && def.Threshold != nil)

	switch def.Edge {
	case "", EdgeRising, EdgeFalling, EdgeBoth:
	default:
		fail("edge must be rising, falling or both, not %q", def.Edge)
	}
	if def.DebounceMs < 0 || def.MinIntervalMs < 0 {
		fail("debounce_ms and min_interval_ms cannot be negative")
	}
	if known && !binding.digital {
		if def.Edge != "" {
			fail("edge is for digital inputs; %q is %s", def.Channel, channel.Type)
		}
		if def.Invert {
			fail("invert is for digital inputs; %q is %s", def.Channel, channel.Type)
		}
		if def.DebounceMs > 0 {
			fail("debounce_ms is for digital inputs; %q is %s", def.Channel, channel.Type)
		}
	}
	if binding.safety && def.Invert {
		fail("%q is a safety channel, which always reads 1 when safe; it cannot be inverted", def.Channel)
	}
	if def.MinIntervalMs > 0 && binding.channelType == TypeCounter {
		fail("a counter is counted from its running total, so min_interval_ms does not apply to it")
	}
	for _, phase := range def.Phases {
		if c.game.Phase(phase) == nil {
			fail("%q is not a phase of game %q", phase, c.game.ID)
		}
		binding.phases[phase] = true
	}
	if known && !analogType && (def.Scale != nil || def.Offset != nil || def.Threshold != nil) {
		fail("scale, offset and threshold are for analog and encoder inputs; %q is %s", def.Channel, channel.Type)
	}
	if def.Scale != nil {
		binding.scale = *def.Scale
	}
	if def.Offset != nil {
		binding.offset = *def.Offset
	}
	if def.Hysteresis != nil {
		if def.Threshold == nil {
			fail("hysteresis needs a threshold")
		}
		if *def.Hysteresis < 0 {
			fail("hysteresis cannot be negative")
		}
	}

	action := def.Action
	switch {
	case action.Score != "" && action.Field != "":
		fail("an action either changes the score (score) or acts on the field (field), not both")
	case action.Score != "":
		c.scoreAction(binding, known)
	case action.Field != "":
		c.fieldAction(binding, known)
	default:
		fail("the action needs score (an element to change) or field (a field action)")
	}
	if len(c.set.problems.Errors()) > errorsBefore {
		return nil
	}
	return binding
}

func (c *compiler) scoreAction(binding *inputBinding, known bool) {
	def, action := binding.def, binding.def.Action
	id := def.ID
	fail := func(format string, args ...any) { c.add(id, false, format, args...) }
	binding.kind = actionScore
	if action.Field != "" || action.Station != "" {
		fail("station is for field actions")
	}
	element := c.game.Element(action.Score)
	if element == nil {
		fail("there is no scoring element %q in game %q", action.Score, c.game.ID)
		return
	}
	binding.element = element
	if !element.EditableBy(scoring.SourceDevice) {
		fail("game %q does not let devices change %s (its edit list is %v)", c.game.ID, element.ID, element.Edit)
	}

	// Who the change is for.
	switch element.Scope {
	case gamedef.ScopeField:
		if action.Alliance != "" || action.Robot != 0 {
			fail("%s is scored for the field as a whole; it takes no alliance or robot", element.ID)
		}
	default:
		if action.Alliance == "" {
			fail(
				"%s is scored per alliance; say which (one of %s)", element.ID,
				strings.Join(c.game.AllianceIDs(), ", "),
			)
		} else if c.game.Alliance(action.Alliance) == nil {
			fail(
				"%q is not an alliance of game %q (%s)", action.Alliance, c.game.ID,
				strings.Join(c.game.AllianceIDs(), ", "),
			)
		}
		if element.Scope == gamedef.ScopeRobot {
			if action.Robot < 1 || action.Robot > c.game.Alliances.Size {
				fail("%s is scored per robot; robot must be 1 to %d", element.ID, c.game.Alliances.Size)
			}
		} else if action.Robot != 0 {
			fail("%s is scored per alliance; it takes no robot", element.ID)
		}
	}

	// What the operation does to that kind of element.
	op := action.Op
	if !scoreOps[op] {
		fail("op must be increment, decrement, set, toggle, hold, timer_start or timer_stop, not %q", op)
		return
	}
	elementOps := map[string][]string{
		gamedef.TypeCounter: {scoring.OpIncrement, scoring.OpDecrement, scoring.OpSet},
		gamedef.TypeNumber:  {scoring.OpIncrement, scoring.OpDecrement, scoring.OpSet},
		gamedef.TypeBoolean: {scoring.OpSet, scoring.OpToggle},
		gamedef.TypeEnum:    {scoring.OpSet},
		gamedef.TypeTimer:   {scoring.OpSet, OpHold, scoring.OpTimerStart, scoring.OpTimerStop},
	}
	if !contains(elementOps[element.Type], op) {
		fail("%s is a %s; op %s does not apply to it (use %s)", element.ID, element.Type, op,
			strings.Join(elementOps[element.Type], ", "))
		return
	}

	// What the operation does with this kind of input.
	inputOps := map[string][]string{
		scoring.OpIncrement: {"digital", TypeCounter}, scoring.OpDecrement: {"digital", TypeCounter},
		scoring.OpSet: {"digital", TypeAnalog, TypeEncoder, TypeText}, scoring.OpToggle: {"digital"},
		OpHold: {"digital"}, scoring.OpTimerStart: {"digital"}, scoring.OpTimerStop: {"digital"},
	}
	inputKind := binding.channelType
	if binding.digital {
		inputKind = "digital"
	}
	if known && !contains(inputOps[op], inputKind) {
		what := binding.channelType
		if binding.channelType != TypeDigital && binding.digital {
			what += " with a threshold, which acts as digital"
		}
		fail("op %s takes a %s input; %q is %s", op, strings.Join(inputOps[op], " or "), def.Channel, what)
		return
	}
	if op == OpHold && def.Edge != "" {
		fail("hold runs the timer while the input is 1, so it takes no edge")
	}

	// The value.
	switch op {
	case scoring.OpIncrement, scoring.OpDecrement:
		binding.amount = 1
		if action.Value != nil {
			if binding.channelType == TypeCounter {
				fail("a counter input changes %s by the difference in its total; it takes no value", element.ID)
				return
			}
			amount, ok := toNumber(action.Value)
			if !ok || amount <= 0 || (element.Type == gamedef.TypeCounter && amount != math.Trunc(amount)) {
				fail("value must be a positive number (a whole one for a counter), not %v", action.Value)
				return
			}
			binding.amount = amount
		}
	case scoring.OpSet:
		needsValue := binding.digital || (!known && action.Value != nil)
		switch {
		case needsValue && action.Value == nil:
			fail("set with a digital input needs a value to set %s to", element.ID)
		case !needsValue && known && action.Value != nil:
			fail(
				"set with %s input %q sets %s to the reading; it takes no value", binding.channelType, def.Channel,
				element.ID,
			)
		case needsValue:
			value, err := elementValue(element, action.Value)
			if err != nil {
				fail("%v", err)
			}
			binding.value = value
		case binding.channelType == TypeText && element.Type != gamedef.TypeEnum:
			fail(
				"a text input can only set an enum, to the option whose id it reads; %s is a %s", element.ID,
				element.Type,
			)
		case analogKind(binding.channelType) && element.Type != gamedef.TypeNumber:
			fail(
				"an %s input sets a number to its scaled reading; %s is a %s", binding.channelType, element.ID,
				element.Type,
			)
		}
	default:
		if action.Value != nil {
			fail("op %s takes no value", op)
		}
	}
}

func (c *compiler) fieldAction(binding *inputBinding, known bool) {
	def, action := binding.def, binding.def.Action
	id := def.ID
	fail := func(format string, args ...any) { c.add(id, false, format, args...) }
	if action.Op != "" || action.Value != nil || action.Alliance != "" || action.Robot != 0 {
		fail("op, value, alliance and robot are for score actions")
	}
	if !fieldActions[action.Field] {
		fail(
			"field action must be start_match, abort_match, estop, astop, field_stop, field_reset, volunteers, "+
				"commit or next_match, not %q", action.Field,
		)
		return
	}
	if known && !binding.digital {
		fail("field actions take a digital input; %q is %s", def.Channel, binding.channelType)
	}
	binding.kind = actionTrigger
	switch action.Field {
	case ActionEStop, ActionAStop, ActionFieldStop:
		binding.kind = actionStop
		if action.Field == ActionFieldStop {
			if action.Station != "" {
				fail("field_stop stops the whole field; it takes no station")
			}
		} else if !c.stations[action.Station] {
			fail("%s needs the station it stops, one of %s; %q is not", action.Field,
				strings.Join(sortedKeys(c.stations), ", "), action.Station)
		}
		if def.Edge != "" || def.MinIntervalMs != 0 || len(def.Phases) != 0 {
			fail(
				"a stop is held for as long as the input is active, at any time; edge, min_interval_ms and " +
					"phases do not apply",
			)
		}
	default:
		if action.Station != "" {
			fail("station is only for estop and astop")
		}
	}
}

func (c *compiler) output(def *OutputBinding) *outputBinding {
	errorsBefore := len(c.set.problems.Errors())
	if !c.checkCommon("output", def.ID, def.Device, def.Channel) {
		return nil
	}
	id := def.ID
	fail := func(format string, args ...any) { c.add(id, false, format, args...) }
	ref := ChannelRef{Device: def.Device, Channel: def.Channel}
	binding := &outputBinding{def: def, ref: ref, pulse: time.Duration(def.PulseMs) * time.Millisecond}

	if def.Device == PlcDeviceID {
		fail("the PLC's coils are driven by the arena and cannot be bound")
	}
	if other := c.outputs[ref]; other != "" {
		fail("channel %s is already driven by binding %q; an output can have only one", ref, other)
	} else if owner := c.env.Claims[ref]; owner != "" && owner != BindingsOwner {
		fail("channel %s is driven by %s; an output is driven by one binding or by %s, not both", ref, owner, owner)
	}
	if def.Alliance != "" && c.game.Alliance(def.Alliance) == nil {
		fail("%q is not an alliance of game %q (%s)", def.Alliance, c.game.ID, strings.Join(c.game.AllianceIDs(), ", "))
	}
	if _, taken := c.outputs[ref]; !taken {
		c.outputs[ref] = id
	}
	channel, _ := c.channel(id, def.Device, def.Channel, DirOut)
	if channel != nil {
		binding.channelType = channel.Type
	}
	// A sound plays once; it has no length. Until the device is known, a look that plays something says it is one.
	sound := binding.channelType == TypeSound ||
		(binding.channelType == "" && def.Look != nil && def.Look.Play != "")

	// Event pulses.
	if def.On != "" {
		if def.When != "" {
			fail("an output takes when (a condition) or on (an event), not both")
		}
		if err := c.checkEvent(def.On); err != nil {
			fail("%v", err)
		}
		if def.PulseMs <= 0 && !sound {
			fail("on needs pulse_ms, how long the output stays on")
		}
	} else if def.PulseMs != 0 {
		fail("pulse_ms is for outputs that pulse on an event (on)")
	}

	// Expressions.
	roots := outputRoots(c.game, c.env.Stations, def.Alliance != "")
	compileExpr := func(what, src string) *rules.Program {
		if src == "" {
			return nil
		}
		program, err := rules.Compile(src, roots, rules.Options{})
		if err != nil {
			fail("%s %q: %v", what, src, err)
			return nil
		}
		return program
	}
	binding.when = compileExpr("when", def.When)
	binding.value = compileExpr("value", def.Value)
	binding.look = c.look(id, "look", def.Look, def.Alliance, compileExpr)
	binding.elseLook = c.look(id, "else", def.Else, def.Alliance, compileExpr)

	// What each kind of output takes.
	switch binding.channelType {
	case TypeDigital, TypeRelay, TypePWM:
		for _, look := range []*Look{def.Look, def.Else} {
			if look == nil {
				continue
			}
			if look.P != PatternOff && look.P != PatternSolid && look.P != PatternBlink {
				fail("a %s output can be off, solid or blink, not %q", binding.channelType, look.P)
			}
			if len(look.C) > 0 || look.V != "" || look.Br != nil || len(look.Seg) > 0 || look.Play != "" {
				fail("only p and ms apply to a %s output", binding.channelType)
			}
		}
		if def.When == "" && def.Value == "" && def.On == "" && def.Look == nil {
			fail("a %s output needs when, value or on", binding.channelType)
		}
	case TypeLEDStrip:
		if def.Look == nil {
			fail("a led_strip output needs a look")
		}
		if def.Value != "" {
			fail("a led_strip output shows its look; value does not apply")
		}
		for _, look := range []*Look{def.Look, def.Else} {
			if look != nil && look.Play != "" {
				fail("play is for sound outputs")
			}
		}
	case TypeDisplay:
		if def.Value == "" {
			fail("a display output needs a value: the text or number to show")
		}
		if def.Look != nil || def.Else != nil {
			fail("a display output shows its value; look and else do not apply")
		}
	case TypeSound:
		if def.Look == nil || def.Look.Play == "" || def.Look.P != "" || len(def.Look.C) > 0 || def.Look.V != "" {
			fail("a sound output needs a look with only play, the sound to play")
		}
		if def.Else != nil || def.Value != "" {
			fail("a sound output plays its look; value and else do not apply")
		}
		if def.When == "" && def.On == "" {
			fail("a sound output needs when (it plays as the condition turns true) or on")
		}
	}
	if len(c.set.problems.Errors()) > errorsBefore {
		return nil
	}
	return binding
}

func (c *compiler) look(
	id, what string, look *Look, alliance string, compileExpr func(what, src string) *rules.Program,
) *compiledLook {
	if look == nil {
		return nil
	}
	fail := func(format string, args ...any) { c.add(id, false, what+": "+format, args...) }
	compiled := &compiledLook{look: look}
	if look.P != "" && !patterns[look.P] {
		fail("p must be off, solid, blink, pulse, chase, rainbow or progress, not %q", look.P)
	}
	if look.Ms < 0 {
		fail("ms cannot be negative")
	}
	if look.Br != nil && (*look.Br < 0 || *look.Br > 1) {
		fail("br is a brightness from 0 to 1, not %g", *look.Br)
	}
	compiled.colors = c.colors(look.C, alliance, fail)
	compiled.v = compileExpr(what+" v", look.V)
	for i := range look.Seg {
		segment := &look.Seg[i]
		if segment.S < 0 || segment.N < 1 {
			fail("segment %d needs a start s of 0 or more and a count n of 1 or more", i+1)
		}
		if segment.P != "" && !patterns[segment.P] {
			fail("segment %d: %q is not a pattern", i+1, segment.P)
		}
		if segment.Ms < 0 {
			fail("segment %d: ms cannot be negative", i+1)
		}
		compiled.segments = append(compiled.segments, compiledSegment{
			segment: segment, colors: c.colors(segment.C, alliance, fail),
			v: compileExpr(fmt.Sprintf("%s segment %d v", what, i+1), segment.V),
		})
	}
	return compiled
}

// colors resolves the colours of a look: #rrggbb as they are, and "alliance" to the colour of the binding's
// alliance in the game.
func (c *compiler) colors(colors []string, alliance string, fail func(format string, args ...any)) []string {
	var resolved []string
	for _, color := range colors {
		switch {
		case color == ColorAlliance:
			if allianceDef := c.game.Alliance(alliance); allianceDef != nil {
				resolved = append(resolved, allianceDef.Color)
			} else {
				fail("the colour %q needs the binding to name its alliance", ColorAlliance)
			}
		case colorPattern.MatchString(color):
			resolved = append(resolved, strings.ToLower(color))
		default:
			fail("%q is not a colour (#rrggbb or %q)", color, ColorAlliance)
		}
	}
	return resolved
}

// checkEvent checks the event an output pulses on.
func (c *compiler) checkEvent(event string) error {
	switch event {
	case EventMatchStart, EventMatchEnd, EventMatchAbort, EventScorePosted:
		return nil
	}
	if rest, ok := strings.CutPrefix(event, "phase."); ok {
		phase, edge, found := strings.Cut(rest, ".")
		if found && (edge == "start" || edge == "end") && c.game.Phase(phase) != nil {
			return nil
		}
		return fmt.Errorf("%q is not phase.<id>.start or phase.<id>.end of a phase of game %q", event, c.game.ID)
	}
	if cue, ok := strings.CutPrefix(event, "cue."); ok {
		for _, phase := range c.game.Match.Phases {
			for _, candidate := range phase.Cues {
				if candidate.ID != "" && candidate.ID == cue {
					return nil
				}
			}
		}
		return fmt.Errorf("game %q has no cue %q", c.game.ID, cue)
	}
	return fmt.Errorf(
		"%q is not an event (match.start, match.end, match.abort, phase.<id>.start, phase.<id>.end, cue.<id>, "+
			"score.posted)", event,
	)
}

// outputRoots returns the names an output expression may use: the game's live names, plus the stations, the field
// signals and the device fault flag.
func outputRoots(game *gamedef.Game, stations []string, perAlliance bool) map[string]*rules.Shape {
	roots := game.LiveRoots(perAlliance)
	station := rules.Obj(map[string]*rules.Shape{
		"team": rules.NumberShape, "connected": rules.BoolShape, "ready": rules.BoolShape, "estop": rules.BoolShape,
		"astop": rules.BoolShape, "bypass": rules.BoolShape,
	})
	byStation := make(map[string]*rules.Shape, len(stations))
	for _, id := range stations {
		byStation[id] = station
	}
	roots["stations"] = rules.Obj(byStation)
	roots["field_reset"] = rules.BoolShape
	roots["volunteers"] = rules.BoolShape
	roots["fault"] = rules.BoolShape
	return roots
}

// elementValue converts the value of a set action to the type the element takes.
func elementValue(element *gamedef.Element, value any) (any, error) {
	switch element.Type {
	case gamedef.TypeBoolean:
		if b, ok := value.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("%s is a boolean; value must be true or false, not %v", element.ID, value)
	case gamedef.TypeEnum:
		option, ok := value.(string)
		if !ok || element.Option(option) == nil {
			var ids []string
			for _, option := range element.Options {
				ids = append(ids, option.ID)
			}
			return nil, fmt.Errorf(
				"%s is an enum; value must be one of %s, not %v", element.ID, strings.Join(ids, ", "), value,
			)
		}
		return option, nil
	case gamedef.TypeNumber:
		if n, ok := toNumber(value); ok {
			return n, nil
		}
		return nil, fmt.Errorf("%s is a number; value must be a number, not %v", element.ID, value)
	case gamedef.TypeCounter:
		if n, ok := toNumber(value); ok && n >= 0 && n == math.Trunc(n) {
			return n, nil
		}
		return nil, fmt.Errorf("%s is a counter; value must be a whole number of 0 or more, not %v", element.ID, value)
	case gamedef.TypeTimer:
		if n, ok := toNumber(value); ok && n >= 0 {
			return n, nil
		}
		return nil, fmt.Errorf("%s is a timer; value must be a number of seconds, not %v", element.ID, value)
	}
	return nil, fmt.Errorf("%s cannot be set", element.ID)
}

// toNumber converts a number from YAML or JSON to a float64. Unlike rules.ToNumber it does not take booleans.
func toNumber(value any) (float64, bool) {
	switch value := value.(type) {
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float64:
		return value, true
	}
	return 0, false
}

func analogKind(channelType string) bool {
	return channelType == TypeAnalog || channelType == TypeEncoder
}

func contains(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
