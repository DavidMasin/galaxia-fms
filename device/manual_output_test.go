package device

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTestOutput(t *testing.T) {
	h := newHarness(t)
	lamp := h.connect(
		"lamp", output("light", TypeDigital), output("strip", TypeLEDStrip), output("dimmer", TypePWM),
		output("score", TypeDisplay), output("horn", TypeSound), output("bound", TypeRelay), digital("button"),
	)
	h.bind(`
outputs:
  - { id: bound, device: lamp, channel: bound, when: "true" }
`)
	ref := func(channel string) ChannelRef { return ChannelRef{Device: "lamp", Channel: channel} }
	on, half := 1.0, 0.5

	// A free channel takes the command and is kept by the test.
	require.Nil(t, h.m.TestOutput(ref("light"), &Command{V: &on}))
	assert.Equal(t, &Command{V: &on}, lamp.Output("light"))
	assert.Equal(t, ManualOutputOwner, h.m.Claims()[ref("light")])
	require.Nil(t, h.m.TestOutput(ref("light"), &Command{V: &on}))
	assert.Equal(t, 1, len(lamp.History()), "only a change is sent")
	strip := &Command{P: PatternBlink, C: []string{"#00ff00"}, Ms: 400, Br: &half}
	require.Nil(t, h.m.TestOutput(ref("strip"), strip))
	assert.Equal(t, strip, lamp.Output("strip"))
	require.Nil(t, h.m.TestOutput(ref("dimmer"), &Command{V: &half}))
	text := "1:32"
	require.Nil(t, h.m.TestOutput(ref("score"), &Command{Text: &text}))

	// A sound plays and holds nothing.
	require.Nil(t, h.m.TestOutput(ref("horn"), &Command{Play: "buzzer"}))
	history := lamp.History()
	assert.Equal(t, &Command{Play: "buzzer", Transient: true}, history[len(history)-1].Command)
	assert.Nil(t, lamp.Output("horn"))
	assert.NotContains(t, h.m.Claims(), ref("horn"))

	// Commands that do not suit the channel are refused.
	two := 2.0
	for channel, command := range map[string]*Command{
		"light": {V: &half}, "dimmer": {V: &two}, "strip": {P: "sparkle"}, "score": {}, "horn": {V: &on},
	} {
		assert.Error(t, h.m.TestOutput(ref(channel), command), channel)
	}
	assert.EqualError(t, h.m.TestOutput(ref("strip"), &Command{P: PatternSolid, C: []string{"alliance"}}),
		`output lamp/strip: "alliance" is not a colour (#rrggbb)`)
	assert.EqualError(t, h.m.TestOutput(ref("button"), &Command{V: &on}),
		`device "lamp" has no output channel "button"`)
	assert.EqualError(t, h.m.TestOutput(ChannelRef{Device: "none", Channel: "x"}, &Command{V: &on}),
		`there is no device "none"`)
	assert.Error(t, h.m.TestOutput(ref("light"), nil))

	// A channel a binding or another component drives says so, and is left alone.
	assert.EqualError(t, h.m.TestOutput(ref("bound"), &Command{V: &on}),
		`output lamp/bound is driven by binding "bound"; remove the binding on the Bindings page to test it by hand`)
	spare := h.connect("spare", output("light", TypeDigital))
	require.Nil(t, h.m.ClaimOutputs("lighting", []ChannelRef{{Device: "spare", Channel: "light"}}))
	assert.EqualError(t, h.m.TestOutput(ChannelRef{Device: "spare", Channel: "light"}, &Command{V: &on}),
		"output spare/light is driven by lighting; release it there to test it by hand")
	assert.Nil(t, spare.Output("light"))

	// While a test lasts, the others are refused the channel in turn.
	assert.EqualError(t, h.m.ClaimOutputs("lighting", []ChannelRef{ref("light")}),
		"output lamp/light is already driven by manual test")
	problems := h.m.ValidateBindings(&BindingSet{Outputs: []*OutputBinding{
		{ID: "light", Device: "lamp", Channel: "light", When: "true"},
	}})
	assert.Contains(t, problems.Errors()[0].Message, "is driven by manual test")

	assert.Equal(t, []OutputDriver{
		{Device: "lamp", Channel: "bound", Owner: BindingsOwner, Binding: "bound", Active: true},
		{Device: "lamp", Channel: "dimmer", Owner: ManualOutputOwner, Active: true},
		{Device: "lamp", Channel: "light", Owner: ManualOutputOwner, Active: true},
		{Device: "lamp", Channel: "score", Owner: ManualOutputOwner, Active: true},
		{Device: "lamp", Channel: "strip", Owner: ManualOutputOwner, Active: true},
		{Device: "spare", Channel: "light", Owner: "lighting", Active: true},
	}, h.m.OutputDrivers())

	// Ending the test clears the output and frees the channel.
	require.Nil(t, h.m.EndOutputTest(ref("light")))
	assert.Nil(t, lamp.Output("light"))
	assert.NotContains(t, h.m.Claims(), ref("light"))
	assert.EqualError(t, h.m.EndOutputTest(ref("light")), "output lamp/light is not being tested")
	assert.Empty(t, h.m.ValidateBindings(&BindingSet{Outputs: []*OutputBinding{
		{ID: "light", Device: "lamp", Channel: "light", When: "true"},
	}}))
}

func TestTestOutputNeedsAnApprovedDevice(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.AuthenticateLocal(VirtualTransportName, "newcomer")
	require.Nil(t, err)
	_, err = h.virtual.Connect(&Manifest{Proto: 1, ID: "newcomer", Channels: []*Channel{output("light", TypeDigital)}})
	require.Nil(t, err)
	on := 1.0
	assert.EqualError(t, h.m.TestOutput(ChannelRef{Device: "newcomer", Channel: "light"}, &Command{V: &on}),
		`device "newcomer" is not approved; approve it before testing its outputs`)
	assert.Contains(t, h.m.TestOutput(ChannelRef{Device: PlcDeviceID, Channel: "coil"}, &Command{V: &on}).Error(),
		"the PLC's coils are driven by the arena")
}

// A binding with errors still keeps its channel, but is shown as not driving it.
func TestOutputDriversOfABrokenBinding(t *testing.T) {
	h := newHarness(t)
	h.connect("lamp", output("light", TypeDigital))
	h.bind(`
outputs:
  - { id: light, device: lamp, channel: light, when: "true" }
`)
	// The device comes back with a manifest that no longer has the channel.
	h.connect("lamp", output("other", TypeDigital))
	assert.Equal(t, []OutputDriver{{Device: "lamp", Channel: "light", Owner: BindingsOwner, Binding: "light"}},
		h.m.OutputDrivers())
}
