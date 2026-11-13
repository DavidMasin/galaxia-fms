// Outputs: what the output bindings make each device show, and the channels other components drive.

package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"math"
	"time"
)

// Snapshot is the state of the field the arena hands to the manager about twenty times a second, built on the
// arena's goroutine.
type Snapshot struct {
	Time  time.Time
	Game  *gamedef.Game
	Clock scoring.Clock
	// Eval evaluates an expression compiled against the game's live roots for an alliance ("" for none), with
	// extra roots. The arena runs it against the live score under its own lock.
	Eval       func(program *rules.Program, own string, extra map[string]any) (any, error)
	Scores     map[string]float64 // Each alliance's score.
	Stations   map[string]StationState
	FieldReset bool
	Volunteers bool
	// Events are the match events since the last snapshot: match.start, match.end, match.abort, phase.<id>.start,
	// phase.<id>.end, cue.<id>, score.posted.
	Events []string
}

// StationState is what output expressions see of a driver station, under stations.<id>.
type StationState struct {
	Team      int  `json:"team"`
	Connected bool `json:"connected"` // The driver station is connected and its robot linked.
	Ready     bool `json:"ready"`     // Nothing at this station keeps the match from starting.
	EStop     bool `json:"estop"`
	AStop     bool `json:"astop"`
	Bypass    bool `json:"bypass"`
}

func (s *Snapshot) fired(event string) bool {
	for _, candidate := range s.Events {
		if candidate == event {
			return true
		}
	}
	return false
}

// fieldState is what field/state says.
func (s *Snapshot) fieldState() *FieldState {
	state := &FieldState{
		State: s.Clock.State, Phase: s.Clock.Phase,
		PhaseRemaining: round1(math.Max(0, s.Clock.PhaseDuration-s.Clock.PhaseElapsed)),
		MatchRemaining: round1(s.Clock.MatchRemaining), Scores: make(map[string]float64, len(s.Scores)),
	}
	for id, score := range s.Scores {
		state.Scores[id] = score
	}
	return state
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

// UpdateOutputs evaluates every output binding against the snapshot and sends the commands that changed, then
// publishes field/state if it changed or a second has passed, then lets other components (the Lighting engine)
// drive their channels. The arena calls it on its own goroutine; it never waits for a device.
func (m *Manager) UpdateOutputs(snapshot *Snapshot) {
	m.mu.Lock()
	now := snapshot.Time
	roots := m.outputExtraRoots(snapshot)
	for _, binding := range m.set.outputs {
		dev := m.devices[binding.ref.Device]
		if dev == nil || binding.channelType == "" {
			continue
		}
		channel := dev.manifest.Channel(binding.ref.Channel)
		if channel == nil || channel.Type != binding.channelType {
			continue
		}
		command, send, err := binding.evaluate(snapshot, roots, now)
		if err != nil {
			if !binding.errorLogged {
				binding.errorLogged = true
				m.logOnce("output:"+binding.def.ID, "Failed to evaluate output binding %q: %v", binding.def.ID, err)
			}
			continue
		}
		if send && (command.Transient || !command.Equal(dev.outputs[channel.ID])) {
			m.sendOutput(binding.ref.Device, binding.ref.Channel, command)
		}
	}

	field := snapshot.fieldState()
	if !field.sameAs(m.lastField) || now.Sub(m.lastFieldTime) >= time.Second {
		m.lastField, m.lastFieldTime = field, now
		m.broadcast(func(transport Transport) { transport.SendField(*field) })
	}
	hooks := append([]func(*Snapshot){}, m.tickHooks...)
	m.unlock()

	for _, hook := range hooks {
		hook(snapshot)
	}
}

// outputExtraRoots are the names output expressions see besides the game's: stations, field_reset, volunteers and
// fault. The caller holds mu.
func (m *Manager) outputExtraRoots(snapshot *Snapshot) map[string]any {
	stations := make(map[string]any, len(m.stations))
	for _, id := range m.stations {
		station := snapshot.Stations[id]
		stations[id] = map[string]any{
			"team": float64(station.Team), "connected": station.Connected, "ready": station.Ready,
			"estop": station.EStop, "astop": station.AStop, "bypass": station.Bypass,
		}
	}
	return map[string]any{
		"stations": stations, "field_reset": snapshot.FieldReset, "volunteers": snapshot.Volunteers,
		"fault": len(m.faults) > 0,
	}
}

// evaluate works out what an output binding shows now. send is false when there is nothing to send: a sound that
// did not fire.
func (binding *outputBinding) evaluate(
	snapshot *Snapshot, roots map[string]any, now time.Time,
) (command *Command, send bool, err error) {
	eval := func(program *rules.Program) (any, error) {
		return snapshot.Eval(program, binding.def.Alliance, roots)
	}
	sound := binding.channelType == TypeSound

	var on bool
	if binding.def.On != "" {
		fired := snapshot.fired(binding.def.On)
		if sound {
			if fired {
				return &Command{Play: binding.look.look.Play, Transient: true}, true, nil
			}
			return nil, false, nil
		}
		if fired {
			binding.pulseUntil = now.Add(binding.pulse)
		}
		on = now.Before(binding.pulseUntil)
	} else {
		on = true
		if binding.when != nil {
			value, err := eval(binding.when)
			if err != nil {
				return nil, false, err
			}
			on = rules.Truthy(value)
		}
	}
	rising := on && !binding.on
	if on != binding.on || binding.since.IsZero() {
		binding.on, binding.since = on, now
	}
	if sound {
		if rising {
			return &Command{Play: binding.look.look.Play, Transient: true}, true, nil
		}
		return nil, false, nil
	}

	switch binding.channelType {
	case TypeDigital, TypeRelay, TypePWM:
		command, err = binding.level(on, eval, now)
	case TypeLEDStrip:
		look := binding.look
		if !on {
			look = binding.elseLook
		}
		command, err = look.command(eval)
	case TypeDisplay:
		command, err = binding.display(on, eval)
	default:
		return nil, false, fmt.Errorf("unknown output type %q", binding.channelType)
	}
	return command, err == nil, err
}

// level works out a digital, relay or pwm output: on (1, or for pwm the value), off (0), or blinking between the
// two when the look says blink, timed by the FMS from the moment the condition last changed.
func (binding *outputBinding) level(on bool, eval func(*rules.Program) (any, error), now time.Time) (*Command, error) {
	look := binding.elseLook
	high := 0.0
	if on {
		look = binding.look
		high = 1
		if binding.value != nil {
			value, err := eval(binding.value)
			if err != nil {
				return nil, err
			}
			if binding.channelType == TypePWM {
				number, ok := rules.ToNumber(value)
				if !ok {
					return nil, fmt.Errorf("value gave %v, which is not a number", value)
				}
				high = math.Max(0, math.Min(1, number))
			} else if !rules.Truthy(value) {
				high = 0
			}
		}
	} else if look != nil {
		// The else look of a digital output turns it on (solid or blink) while the condition is false.
		high = 1
	}
	level := high
	if look != nil {
		switch look.look.P {
		case PatternOff:
			level = 0
		case PatternBlink:
			period := time.Duration(look.look.Ms) * time.Millisecond
			if period <= 0 {
				period = defaultBlinkPeriod
			}
			if now.Sub(binding.since)%period >= period/2 {
				level = 0
			}
		}
	}
	return &Command{V: number(level)}, nil
}

// display works out a display output: the value as text, or as a number, while on; blank while off.
func (binding *outputBinding) display(on bool, eval func(*rules.Program) (any, error)) (*Command, error) {
	if !on || binding.value == nil {
		empty := ""
		return &Command{Text: &empty}, nil
	}
	value, err := eval(binding.value)
	if err != nil {
		return nil, err
	}
	switch value := value.(type) {
	case string:
		return &Command{Text: &value}, nil
	case nil:
		empty := ""
		return &Command{Text: &empty}, nil
	case bool:
		text := fmt.Sprint(value)
		return &Command{Text: &text}, nil
	}
	if n, ok := rules.ToNumber(value); ok {
		return &Command{V: number(n)}, nil
	}
	text := fmt.Sprint(value)
	return &Command{Text: &text}, nil
}

// command builds an LED strip command from a look. No look means off.
func (look *compiledLook) command(eval func(*rules.Program) (any, error)) (*Command, error) {
	if look == nil {
		return &Command{P: PatternOff}, nil
	}
	pattern := look.look.P
	if pattern == "" {
		pattern = PatternSolid
	}
	command := &Command{
		P: pattern, C: append([]string(nil), look.colors...), Ms: look.look.Ms, Br: clonePointer(look.look.Br),
	}
	if look.v != nil {
		fill, err := evalFraction(eval, look.v)
		if err != nil {
			return nil, err
		}
		command.V = number(fill)
	}
	for _, segment := range look.segments {
		out := Segment{
			S: segment.segment.S, N: segment.segment.N, P: segment.segment.P,
			C: append([]string(nil), segment.colors...), Ms: segment.segment.Ms,
		}
		if out.P == "" {
			out.P = PatternSolid
		}
		if segment.v != nil {
			fill, err := evalFraction(eval, segment.v)
			if err != nil {
				return nil, err
			}
			out.V = number(fill)
		}
		command.Seg = append(command.Seg, out)
	}
	return command, nil
}

// evalFraction evaluates an expression that gives a fill from 0 to 1. Values outside are clamped, and the result is
// rounded to a thousandth, finer than any strip shows, so that a fill creeping with the clock is not resent on
// every evaluation.
func evalFraction(eval func(*rules.Program) (any, error), program *rules.Program) (float64, error) {
	value, err := eval(program)
	if err != nil {
		return 0, err
	}
	fill, ok := rules.ToNumber(value)
	if !ok || math.IsNaN(fill) {
		return 0, fmt.Errorf("v gave %v, which is not a number", value)
	}
	return math.Round(math.Max(0, math.Min(1, fill))*1000) / 1000, nil
}

// sendOutput records the command in force on a channel and sends it to every transport. A nil command clears the
// channel. A transient command (a sound) is sent but not kept. The caller holds mu.
func (m *Manager) sendOutput(deviceID, channel string, command *Command) {
	if dev := m.devices[deviceID]; dev != nil && !command.isTransient() {
		if command == nil {
			delete(dev.outputs, channel)
		} else {
			dev.outputs[channel] = command.Clone()
		}
	}
	sent := command.Clone()
	m.emit("device.output", map[string]any{"id": deviceID, "channel": channel, "command": sent})
	m.broadcast(func(transport Transport) { transport.SendOutput(deviceID, channel, sent.Clone()) })
}

func (c *Command) isTransient() bool {
	return c != nil && c.Transient
}

// releaseUndriven clears the outputs that were driven by bindings of the previous set and no longer are, unless
// another component drives them now. The caller holds mu.
func (m *Manager) releaseUndriven(previous *compiledSet) {
	if previous == nil {
		return
	}
	for ref := range previous.driven {
		if m.set.driven[ref] != nil || m.claims[ref] != "" {
			continue
		}
		if dev := m.devices[ref.Device]; dev != nil && dev.outputs[ref.Channel] != nil {
			m.sendOutput(ref.Device, ref.Channel, nil)
		}
	}
}

// ClaimOutputs makes the given output channels the owner's to drive with DriveOutput, in place of the channels it
// claimed before; those it no longer claims are cleared. It fails, claiming nothing, if a channel is bound by the
// binding set or claimed by another owner. The Lighting engine claims its fixtures this way.
func (m *Manager) ClaimOutputs(owner string, refs []ChannelRef) error {
	if owner == "" || owner == BindingsOwner {
		return fmt.Errorf("%q cannot claim outputs", owner)
	}
	m.mu.Lock()
	defer m.unlock()
	wanted := make(map[ChannelRef]bool, len(refs))
	for _, ref := range refs {
		if ref.Device == PlcDeviceID {
			return fmt.Errorf("the PLC's coils are driven by the arena")
		}
		if other := m.claims[ref]; other != "" && other != owner {
			return fmt.Errorf("output %s is already driven by %s", ref, other)
		}
		for _, output := range m.source.Outputs {
			if output.Device == ref.Device && output.Channel == ref.Channel {
				return fmt.Errorf("output %s is driven by binding %q", ref, output.ID)
			}
		}
		wanted[ref] = true
	}
	for ref, claimant := range m.claims {
		if claimant == owner && !wanted[ref] {
			delete(m.claims, ref)
			if dev := m.devices[ref.Device]; dev != nil && dev.outputs[ref.Channel] != nil {
				m.sendOutput(ref.Device, ref.Channel, nil)
			}
		}
	}
	for ref := range wanted {
		m.claims[ref] = owner
	}
	return nil
}

// ReleaseOutputs gives up every channel the owner claimed, and clears them.
func (m *Manager) ReleaseOutputs(owner string) {
	if owner == "" || owner == BindingsOwner {
		return
	}
	_ = m.ClaimOutputs(owner, nil)
}

// Claims returns which component drives each claimed channel.
func (m *Manager) Claims() map[ChannelRef]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	claims := make(map[ChannelRef]string, len(m.claims))
	for ref, owner := range m.claims {
		claims[ref] = owner
	}
	return claims
}

// DriveOutput sends a command to a channel the owner claimed. Like the bindings' own outputs, it is only sent if it
// differs from the command in force.
func (m *Manager) DriveOutput(owner string, ref ChannelRef, command *Command) error {
	m.mu.Lock()
	defer m.unlock()
	if m.claims[ref] != owner {
		return fmt.Errorf("output %s is not claimed by %s", ref, owner)
	}
	dev := m.devices[ref.Device]
	if dev == nil {
		return fmt.Errorf("there is no device %q", ref.Device)
	}
	if command.isTransient() || !command.Equal(dev.outputs[ref.Channel]) {
		m.sendOutput(ref.Device, ref.Channel, command)
	}
	return nil
}

// OnOutputTick registers a function called after every UpdateOutputs, on the arena's goroutine and without the
// manager's lock, with the same snapshot. It must not block. The Lighting engine drives its fixtures from here.
func (m *Manager) OnOutputTick(hook func(*Snapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tickHooks = append(m.tickHooks, hook)
}
