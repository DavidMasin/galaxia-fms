// Input processing: turning what devices report into score changes, field actions and stops, as the bindings say.

package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"time"
)

// process feeds one reading to every binding of its channel. The caller holds mu.
func (m *Manager) process(dev *device, channel *Channel, value any, conf *float64, now time.Time) {
	id := dev.record.DeviceId
	delta := 0.0
	if channel.Type == TypeCounter {
		total := value.(float64)
		last, known := dev.record.CounterTotals[channel.ID]
		switch {
		case !known:
			// The first total the FMS ever hears from a channel is where it starts counting: it cannot know how much
			// of that total happened while it was not listening (before the device was approved, say).
		case total < last:
			// The device restarted, and everything it has counted since is new.
			delta = total
		default:
			delta = total - last
		}
		if !known || total != last {
			dev.record.CounterTotals[channel.ID] = total
			if !dev.builtIn {
				m.persist(id)
			}
		}
	}
	for _, binding := range m.set.byChannel[ChannelRef{Device: id, Channel: channel.ID}] {
		if binding.channelType == channel.Type {
			m.feed(binding, dev, channel, value, delta, conf, now)
		}
	}
}

// feed gives one reading to one binding.
func (m *Manager) feed(
	binding *inputBinding, dev *device, channel *Channel, value any, delta float64, conf *float64, now time.Time,
) {
	switch {
	case binding.digital:
		m.sample(binding, dev, channel, binding.levelOf(value), conf, now)
	case channel.Type == TypeCounter:
		if delta > 0 && m.passes(binding, now) {
			m.queueChange(binding, dev, channel, conf, binding.def.Action.Op, delta)
		}
	case analogKind(channel.Type):
		scaled := value.(float64)*binding.scale + binding.offset
		if binding.state.lastSet != any(scaled) && m.passes(binding, now) {
			binding.state.lastSet = scaled
			m.queueChange(binding, dev, channel, conf, scoring.OpSet, scaled)
		}
	case channel.Type == TypeText:
		text := value.(string)
		if binding.state.lastSet == any(text) || !m.passes(binding, now) {
			return
		}
		if binding.element.Option(text) == nil {
			reason := fmt.Sprintf("%q is not an option of %s", text, binding.element.ID)
			m.refuse(dev.record.DeviceId, binding.def.ID, reason)
			return
		}
		binding.state.lastSet = text
		m.queueChange(binding, dev, channel, conf, scoring.OpSet, text)
	}
}

// levelOf works out whether a reading makes a digital binding active, before debouncing. A safety channel is
// active, meaning stopped, when it reads 0; an analog channel with a threshold is active above the threshold, and
// with hysteresis stays active until it falls that far below it.
func (binding *inputBinding) levelOf(value any) bool {
	raw, _ := value.(float64)
	active := raw != 0
	if threshold := binding.def.Threshold; threshold != nil {
		scaled := raw*binding.scale + binding.offset
		hysteresis := 0.0
		if binding.def.Hysteresis != nil {
			hysteresis = *binding.def.Hysteresis
		}
		if binding.state.above {
			binding.state.above = scaled >= *threshold-hysteresis
		} else {
			binding.state.above = scaled >= *threshold
		}
		active = binding.state.above
	} else if binding.safety {
		active = !active
	}
	return active != binding.def.Invert
}

// sample takes a new level of a digital binding. With a debounce time, the level must hold that long before it
// counts; Tick commits a level that held without another message arriving.
func (m *Manager) sample(
	binding *inputBinding, dev *device, channel *Channel, level bool, conf *float64, now time.Time,
) {
	state := &binding.state
	if level == state.level {
		state.pending = false
		return
	}
	if binding.debounce > 0 {
		if !state.pending || state.pendingLevel != level {
			state.pending, state.pendingLevel, state.pendingSince = true, level, now
			return
		}
		if now.Sub(state.pendingSince) < binding.debounce {
			return
		}
	}
	state.pending = false
	m.commit(binding, dev, channel, level, conf, now)
}

// commit makes a new level of a digital binding count, and does what the binding says on that edge.
func (m *Manager) commit(
	binding *inputBinding, dev *device, channel *Channel, level bool, conf *float64, now time.Time,
) {
	binding.state.level = level
	id := dev.record.DeviceId
	action := binding.def.Action
	switch binding.kind {
	case actionStop:
		// The stop itself is read from the level (see Stops). A safety circuit that opens also latches a fault,
		// which holds the stop until an operator has looked.
		if level && binding.safety && dev.approved() {
			m.latchFault(binding, fmt.Sprintf(
				"its safety channel %s read 0: the stop was pressed or its circuit opened", channel.ID,
			))
		}
	case actionTrigger:
		if edgeFires(binding.def.Edge, level) && m.passes(binding, now) {
			m.fieldActions = append(
				m.fieldActions, FieldAction{Action: action.Field, Device: id, Binding: binding.def.ID, Time: now},
			)
		}
	case actionScore:
		if action.Op == OpHold {
			switch {
			case level && !binding.state.holding && m.match.Scoring && m.phaseAllows(binding):
				binding.state.holding = true
				m.queueChange(binding, dev, channel, conf, scoring.OpTimerStart, nil)
			case !level && binding.state.holding:
				binding.state.holding = false
				if m.match.Scoring {
					m.queueChange(binding, dev, channel, conf, scoring.OpTimerStop, nil)
				}
			}
			return
		}
		if !edgeFires(binding.def.Edge, level) || !m.passes(binding, now) {
			return
		}
		switch action.Op {
		case scoring.OpIncrement, scoring.OpDecrement:
			m.queueChange(binding, dev, channel, conf, action.Op, binding.amount)
		case scoring.OpSet:
			m.queueChange(binding, dev, channel, conf, action.Op, binding.value)
		default:
			m.queueChange(binding, dev, channel, conf, action.Op, nil)
		}
	}
}

// edgeFires reports whether a change to the given level is an edge the binding acts on.
func edgeFires(edge string, level bool) bool {
	switch edge {
	case EdgeFalling:
		return !level
	case EdgeBoth:
		return true
	}
	return level
}

// passes decides whether a binding acts now: the match must be in one of its phases, a score action needs a match
// that accepts input, and a trigger within min_interval_ms of the last one is ignored. A binding that acts is
// remembered as having triggered.
func (m *Manager) passes(binding *inputBinding, now time.Time) bool {
	if !m.phaseAllows(binding) || (binding.kind == actionScore && !m.match.Scoring) {
		return false
	}
	state := &binding.state
	if binding.minInterval > 0 && !state.lastTrigger.IsZero() && now.Sub(state.lastTrigger) < binding.minInterval {
		return false
	}
	state.lastTrigger = now
	return true
}

// phaseAllows reports whether the binding's phases let it act now. Input that arrives in the score grace after one
// of its phases ended still counts, as the arena credits it to that phase.
func (m *Manager) phaseAllows(binding *inputBinding) bool {
	if len(binding.phases) == 0 {
		return true
	}
	return (m.match.Running && binding.phases[m.match.Phase]) ||
		(m.match.GracePhase != "" && binding.phases[m.match.GracePhase])
}

// queueChange prepares a score change from a binding; it is applied once mu is released.
func (m *Manager) queueChange(
	binding *inputBinding, dev *device, channel *Channel, conf *float64, op string, value any,
) {
	action := binding.def.Action
	source := scoring.Source{
		Kind: scoring.SourceDevice, ID: dev.record.DeviceId, Name: dev.name() + " · " + channel.DisplayName(),
		Confidence: clonePointer(conf),
	}
	change := scoring.Change{
		Alliance: action.Alliance, Robot: action.Robot, Element: binding.element.ID, Op: op, Value: value,
		Source: source,
	}
	pending := pendingChange{change: change, device: dev.record.DeviceId, binding: binding}
	m.work.changes = append(m.work.changes, pending)
}

// Tick runs what depends on time passing rather than on messages: levels that have waited out their debounce time
// and the safety watchdog. The arena calls it on every pass of its loop.
func (m *Manager) Tick(now time.Time) {
	m.mu.Lock()
	defer m.unlock()
	for _, binding := range m.set.inputs {
		state := &binding.state
		if !state.pending || now.Sub(state.pendingSince) < binding.debounce {
			continue
		}
		state.pending = false
		dev := m.devices[binding.ref.Device]
		if dev == nil || dev.manifest.Channel(binding.ref.Channel) == nil {
			continue
		}
		m.commit(binding, dev, dev.manifest.Channel(binding.ref.Channel), state.pendingLevel, nil, now)
	}
	m.watchdog(now)
}

// releaseInputs forgets the levels of a device's bindings when it disconnects, without acting on them: nothing it
// reported can be trusted to still hold. A timer it was holding is stopped. The caller holds mu.
func (m *Manager) releaseInputs(deviceID string, now time.Time) {
	dev := m.devices[deviceID]
	for _, binding := range m.set.inputs {
		if binding.ref.Device != deviceID {
			continue
		}
		state := &binding.state
		if state.holding && m.match.Scoring && dev != nil {
			if channel := dev.manifest.Channel(binding.ref.Channel); channel != nil {
				m.queueChange(binding, dev, channel, nil, scoring.OpTimerStop, nil)
			}
		}
		state.holding, state.level, state.pending, state.above = false, false, false, false
	}
}

// seedInputs sets the level of digital bindings that are new (or changed) from the last value their channel
// reported, without acting on it: a binding saved while a stop is pressed must hold the stop at once, and one saved
// while a button is held must not fire. The caller holds mu.
func (m *Manager) seedInputs() {
	for _, binding := range m.set.inputs {
		if binding.state.seeded {
			continue
		}
		binding.state.seeded = true
		dev := m.devices[binding.ref.Device]
		if dev == nil || !dev.online() || !binding.digital {
			continue
		}
		if last := dev.inputs[binding.ref.Channel]; last != nil {
			binding.state.level = binding.levelOf(last.Value)
		}
	}
}
