// Testing outputs by hand from the Devices page: an operator sends a command to an output channel to check its
// wiring. A channel under test is claimed the way the Lighting engine claims its fixtures, so that nothing else can
// drive it at the same time without saying so, and a channel that a binding or another component drives cannot be
// tested until it lets go.

package device

import (
	"fmt"
	"math"
	"sort"
)

// ManualOutputOwner owns the output channels an operator is testing by hand.
const ManualOutputOwner = "manual test"

// OutputDriver says what drives one output channel.
type OutputDriver struct {
	Device  string `json:"device"`
	Channel string `json:"channel"`
	// Owner is BindingsOwner for a binding, ManualOutputOwner for a test, or the component that claimed the channel.
	Owner   string `json:"owner"`
	Binding string `json:"binding,omitempty"` // The output binding, when a binding drives it.
	// Active is false for a binding with errors, which drives nothing until it is fixed but still keeps the channel.
	Active bool `json:"active"`
}

// OutputDrivers returns what drives every driven output channel, ordered by device and channel.
func (m *Manager) OutputDrivers() []OutputDriver {
	m.mu.Lock()
	defer m.mu.Unlock()
	drivers := []OutputDriver{}
	seen := make(map[ChannelRef]bool)
	for _, output := range m.source.Outputs {
		ref := ChannelRef{Device: output.Device, Channel: output.Channel}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		active := m.set.driven[ref] != nil && m.set.driven[ref].def.ID == output.ID
		drivers = append(drivers, OutputDriver{
			Device: ref.Device, Channel: ref.Channel, Owner: BindingsOwner, Binding: output.ID, Active: active,
		})
	}
	for ref, owner := range m.claims {
		if !seen[ref] {
			drivers = append(drivers, OutputDriver{
				Device: ref.Device, Channel: ref.Channel, Owner: owner, Active: true,
			})
		}
	}
	sort.Slice(drivers, func(i, j int) bool {
		if drivers[i].Device != drivers[j].Device {
			return drivers[i].Device < drivers[j].Device
		}
		return drivers[i].Channel < drivers[j].Channel
	})
	return drivers
}

// TestOutput sends a command to an output channel on an operator's behalf and keeps the channel until EndOutputTest.
// It is refused, saying what drives the channel, if a binding or another component drives it; while the test lasts,
// they are refused the channel in turn. A sound is played but not kept: it holds nothing.
func (m *Manager) TestOutput(ref ChannelRef, command *Command) error {
	if command == nil {
		return fmt.Errorf("a test needs a command; end the test to clear the output")
	}
	command = command.Clone()
	m.mu.Lock()
	defer m.unlock()
	channel, err := m.testableOutput(ref)
	if err != nil {
		return err
	}
	if err = checkTestCommand(channel, command); err != nil {
		return fmt.Errorf("output %s: %v", ref, err)
	}
	if channel.Type == TypeSound {
		command.Transient = true
	} else {
		m.claims[ref] = ManualOutputOwner
	}
	if command.Transient || !command.Equal(m.devices[ref.Device].outputs[ref.Channel]) {
		m.sendOutput(ref.Device, ref.Channel, command)
	}
	return nil
}

// EndOutputTest stops testing an output channel and clears it, so that a binding or the Lighting page may drive it.
func (m *Manager) EndOutputTest(ref ChannelRef) error {
	m.mu.Lock()
	defer m.unlock()
	if m.claims[ref] != ManualOutputOwner {
		return fmt.Errorf("output %s is not being tested", ref)
	}
	delete(m.claims, ref)
	if dev := m.devices[ref.Device]; dev != nil && dev.outputs[ref.Channel] != nil {
		m.sendOutput(ref.Device, ref.Channel, nil)
	}
	// A binding set loaded while the channel was under test could not drive it; now it can.
	m.recompile()
	return nil
}

// testableOutput returns the output channel an operator may test, or why not. The caller holds mu.
func (m *Manager) testableOutput(ref ChannelRef) (*Channel, error) {
	if ref.Device == PlcDeviceID {
		return nil, fmt.Errorf("the PLC's coils are driven by the arena; test them on the Field Testing page")
	}
	dev := m.devices[ref.Device]
	switch {
	case dev == nil:
		return nil, fmt.Errorf("there is no device %q", ref.Device)
	case !dev.approved():
		return nil, fmt.Errorf("device %q is not approved; approve it before testing its outputs", ref.Device)
	}
	channel := dev.manifest.Channel(ref.Channel)
	if channel == nil || channel.Dir != DirOut {
		return nil, fmt.Errorf("device %q has no output channel %q", ref.Device, ref.Channel)
	}
	for _, output := range m.source.Outputs {
		if output.Device == ref.Device && output.Channel == ref.Channel {
			return nil, fmt.Errorf(
				"output %s is driven by binding %q; remove the binding on the Bindings page to test it by hand", ref,
				output.ID,
			)
		}
	}
	if owner := m.claims[ref]; owner != "" && owner != ManualOutputOwner {
		return nil, fmt.Errorf("output %s is driven by %s; release it there to test it by hand", ref, owner)
	}
	return channel, nil
}

// checkTestCommand checks that a command is one the channel's type takes, as docs/devices.md lays out.
func checkTestCommand(channel *Channel, command *Command) error {
	fraction := func(what string, value *float64) error {
		if value != nil && (math.IsNaN(*value) || *value < 0 || *value > 1) {
			return fmt.Errorf("%s must be from 0 to 1, not %g", what, *value)
		}
		return nil
	}
	notStrip := command.P != "" || len(command.C) > 0 || command.Ms != 0 || command.Br != nil || len(command.Seg) > 0
	switch channel.Type {
	case TypeDigital, TypeRelay:
		if command.V == nil || (*command.V != 0 && *command.V != 1) || notStrip || command.Text != nil ||
			command.Play != "" {
			return fmt.Errorf("a %s output takes {\"v\":1} or {\"v\":0}", channel.Type)
		}
	case TypePWM:
		if command.V == nil || notStrip || command.Text != nil || command.Play != "" {
			return fmt.Errorf("a pwm output takes {\"v\": a level from 0 to 1}")
		}
		return fraction("v", command.V)
	case TypeLEDStrip:
		if command.Text != nil || command.Play != "" {
			return fmt.Errorf("a led_strip output takes a pattern, not text or a sound")
		}
		if command.P == "" && len(command.Seg) == 0 {
			return fmt.Errorf("a led_strip command needs a pattern p")
		}
		if err := checkTestPattern("", command.P, command.C, command.Ms); err != nil {
			return err
		}
		if err := fraction("br", command.Br); err != nil {
			return err
		}
		if err := fraction("v", command.V); err != nil {
			return err
		}
		for i, segment := range command.Seg {
			what := fmt.Sprintf("segment %d: ", i+1)
			if segment.S < 0 || segment.N < 1 {
				return fmt.Errorf("%sneeds a start s of 0 or more and a count n of 1 or more", what)
			}
			if err := checkTestPattern(what, segment.P, segment.C, segment.Ms); err != nil {
				return err
			}
			if err := fraction(what+"v", segment.V); err != nil {
				return err
			}
		}
	case TypeDisplay:
		if (command.Text == nil) == (command.V == nil) || notStrip || command.Play != "" {
			return fmt.Errorf("a display output takes {\"text\":...} or {\"v\":...}")
		}
	case TypeSound:
		if command.Play == "" || command.V != nil || notStrip || command.Text != nil {
			return fmt.Errorf("a sound output takes {\"play\": the sound}")
		}
	default:
		return fmt.Errorf("unknown output type %q", channel.Type)
	}
	return nil
}

func checkTestPattern(what, pattern string, colors []string, ms int) error {
	if pattern != "" && !patterns[pattern] {
		return fmt.Errorf("%sp must be off, solid, blink, pulse, chase, rainbow or progress, not %q", what, pattern)
	}
	for _, color := range colors {
		if !colorPattern.MatchString(color) {
			return fmt.Errorf("%s%q is not a colour (#rrggbb)", what, color)
		}
	}
	if ms < 0 {
		return fmt.Errorf("%sms cannot be negative", what)
	}
	return nil
}
