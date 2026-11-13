package device

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

type fakePlc struct {
	enabled, healthy bool
	inputs, coils    []bool
}

func (p *fakePlc) IsEnabled() bool         { return p.enabled }
func (p *fakePlc) IsHealthy() bool         { return p.healthy }
func (p *fakePlc) GetInputNames() []string { return []string{"fieldEStop", "red1EStop", "ftaReady"} }
func (p *fakePlc) GetCoilNames() []string  { return []string{"heartbeat", "stackLightGreen"} }
func (p *fakePlc) GetInputs() []bool       { return p.inputs }
func (p *fakePlc) GetCoils() []bool        { return p.coils }

func TestPlcDevice(t *testing.T) {
	h := newHarness(t)
	source := &fakePlc{inputs: []bool{true, false, true}, coils: []bool{true, false}}

	// No PLC configured, no device.
	h.m.UpdatePlc(source)
	_, ok := h.m.Device(PlcDeviceID)
	assert.False(t, ok)

	// Configured but not answering: there, and offline.
	source.enabled = true
	h.m.UpdatePlc(source)
	info, ok := h.m.Device(PlcDeviceID)
	require.True(t, ok)
	assert.True(t, info.BuiltIn)
	assert.Equal(t, "approved", info.State)
	assert.False(t, info.Online)
	if assert.NotNil(t, info.Manifest) {
		assert.Equal(t, []*Channel{
			{ID: "field_e_stop", Dir: DirIn, Type: TypeDigital, Name: "fieldEStop"},
			{ID: "red1_e_stop", Dir: DirIn, Type: TypeDigital, Name: "red1EStop"},
			{ID: "fta_ready", Dir: DirIn, Type: TypeDigital, Name: "ftaReady"},
			{ID: "heartbeat", Dir: DirOut, Type: TypeDigital, Name: "heartbeat"},
			{ID: "stack_light_green", Dir: DirOut, Type: TypeDigital, Name: "stackLightGreen"},
		}, info.Manifest.Channels)
	}

	// Healthy: online, its inputs mirrored and its coils shown.
	source.healthy = true
	h.m.UpdatePlc(source)
	info, _ = h.m.Device(PlcDeviceID)
	assert.True(t, info.Online)
	assert.Equal(t, 1.0, info.Inputs["field_e_stop"].Value)
	assert.Equal(t, 0.0, info.Inputs["red1_e_stop"].Value)
	assert.Equal(t, map[string]*Command{"heartbeat": v(1), "stack_light_green": v(0)}, info.Outputs)
	assert.Equal(t, []map[string]any{{"id": "plc", "transport": "plc"}}, h.eventsOf("device.online"))
	assert.Equal(t, 3, len(h.eventsOf("device.input")))

	// Its inputs can be bound like any device's; its coils cannot, and it cannot be edited.
	h.bind(`
inputs:
  - { id: ready, device: plc, channel: fta_ready, edge: falling, action: { field: volunteers } }
`)
	source.inputs[2] = false
	h.m.UpdatePlc(source)
	assert.Equal(t, 1, len(h.m.TakeFieldActions()))
	assert.Equal(t, 1, len(h.eventsOf("device.input")), "only changes are reported")
	assert.ErrorContains(t, h.m.Block(PlcDeviceID), "is built in")
	assert.ErrorContains(t, h.m.Delete(PlcDeviceID), "is built in")
	assert.Empty(t, h.m.Outputs(), "the coils are never sent to anyone")

	// Unhealthy again, then unconfigured.
	source.healthy = false
	h.m.UpdatePlc(source)
	info, _ = h.m.Device(PlcDeviceID)
	assert.False(t, info.Online)
	source.enabled = false
	h.m.UpdatePlc(source)
	_, ok = h.m.Device(PlcDeviceID)
	assert.False(t, ok)
	record, _ := h.db.GetDeviceByDeviceId(PlcDeviceID)
	assert.Nil(t, record, "the PLC is never stored")
}

func TestPlcChannelID(t *testing.T) {
	assert.Equal(t, "red1_e_stop", plcChannelID("red1EStop"))
	assert.Equal(t, "blue_connected3", plcChannelID("blueConnected3"))
	assert.Equal(t, "stack_light_green", plcChannelID("stackLightGreen"))
	assert.True(t, ValidChannelID(plcChannelID("fieldIoConnection")))
}
