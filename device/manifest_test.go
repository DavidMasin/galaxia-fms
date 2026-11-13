package device

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestParseManifest(t *testing.T) {
	// The example from docs/devices.md.
	manifest, err := ParseManifest([]byte(`{
		"proto": 1, "id": "beam-red-1", "name": "Red goal beam break", "fw": "1.0.0", "sdk": "galaxia-arduino/0.1.0",
		"board": "esp32dev",
		"channels": [
			{ "id": "count", "dir": "in", "type": "counter", "name": "Balls through goal" },
			{ "id": "beam", "dir": "in", "type": "digital", "name": "Beam state" },
			{ "id": "stop", "dir": "in", "type": "digital", "name": "E-stop", "safety": true },
			{ "id": "strip", "dir": "out", "type": "led_strip", "name": "Goal lights", "pixels": 30 }
		]
	}`))
	if assert.Nil(t, err) {
		assert.Equal(t, "beam-red-1", manifest.ID)
		assert.Equal(t, "1.0.0", manifest.Firmware)
		assert.Equal(t, 4, len(manifest.Channels))
		assert.True(t, manifest.Channel("stop").Safety)
		assert.Equal(t, 30, manifest.Channel("strip").Pixels)
		assert.Nil(t, manifest.Channel("missing"))
		assert.True(t, manifest.HasSafety())
		assert.Equal(t, "Beam state", manifest.Channel("beam").DisplayName())
	}

	_, err = ParseManifest([]byte(`{"proto": 1,`))
	assert.ErrorContains(t, err, "the manifest is not valid JSON")
}

func TestManifestValidation(t *testing.T) {
	one, two := 1.0, 2.0
	valid := func() *Manifest {
		return &Manifest{Proto: 1, ID: "box-1", Channels: []*Channel{
			{ID: "button", Dir: DirIn, Type: TypeDigital},
			{ID: "level", Dir: DirIn, Type: TypeAnalog, Unit: "kg", Min: &one, Max: &two},
			{ID: "fan", Dir: DirOut, Type: TypePWM, Min: &one},
			{ID: "strip", Dir: DirOut, Type: TypeLEDStrip, Pixels: 8},
		}}
	}
	assert.Nil(t, valid().Validate())

	cases := []struct {
		name   string
		change func(m *Manifest)
		want   string
	}{
		{"proto", func(m *Manifest) { m.Proto = 2 }, "proto must be 1, not 2"},
		{"empty id", func(m *Manifest) { m.ID = "" }, `id "" must be 1 to 40 lowercase letters, digits and dashes`},
		{"upper case id", func(m *Manifest) { m.ID = "Box-1" }, `id "Box-1" must be`},
		{"long id", func(m *Manifest) { m.ID = "a123456789a123456789a123456789a1234567890" }, "must be 1 to 40"},
		{"channel id", func(m *Manifest) { m.Channels[0].ID = "Button-1" }, `channel 1: id "Button-1" must be`},
		{"duplicate", func(m *Manifest) { m.Channels[1].ID = "button" }, "channel 2 (button): the id is used twice"},
		{"dir", func(m *Manifest) { m.Channels[0].Dir = "both" }, `dir must be in or out, not "both"`},
		{"input type", func(m *Manifest) { m.Channels[0].Type = TypeRelay }, `"relay" is not an input type`},
		{"output type", func(m *Manifest) { m.Channels[2].Type = TypeCounter }, `"counter" is not an output type`},
		{"safety output", func(m *Manifest) { m.Channels[2].Safety = true }, "only inputs can be safety channels"},
		{"safety analog", func(m *Manifest) { m.Channels[1].Safety = true }, "a safety channel is a stop circuit"},
		{"no pixels", func(m *Manifest) { m.Channels[3].Pixels = 0 }, "a led_strip needs pixels"},
		{"pixels elsewhere", func(m *Manifest) { m.Channels[0].Pixels = 3 }, "pixels is only for led_strip outputs"},
		{"unit on digital", func(m *Manifest) { m.Channels[0].Unit = "V" }, "unit, min and max are only for analog"},
		{"min over max", func(m *Manifest) { m.Channels[1].Min, m.Channels[1].Max = &two, &one }, "min is greater"},
		{"nil channel", func(m *Manifest) { m.Channels = append(m.Channels, nil) }, "channel 5 is empty"},
	}
	for _, c := range cases {
		manifest := valid()
		c.change(manifest)
		err := manifest.Validate()
		if assert.NotNil(t, err, c.name) {
			assert.Contains(t, err.Error(), c.want, c.name)
		}
	}

	// Every problem is reported at once.
	manifest := valid()
	manifest.Proto = 0
	manifest.Channels[3].Pixels = 0
	err := manifest.Validate()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "proto must be 1")
		assert.Contains(t, err.Error(), "needs pixels")
	}
}

func TestParseMessages(t *testing.T) {
	reading, err := ParseReading([]byte(`{"v":1,"seq":42,"conf":0.93,"ts":123456}`))
	if assert.Nil(t, err) {
		assert.Equal(t, 1.0, reading.Value)
		assert.Equal(t, int64(42), *reading.Seq)
		assert.Equal(t, 0.93, *reading.Conf)
		assert.Equal(t, int64(123456), *reading.Ts)
	}
	reading, err = ParseReading([]byte(`{"v":true}`))
	assert.Nil(t, err)
	assert.Equal(t, 1.0, reading.Value)
	reading, err = ParseReading([]byte(`{"v":"tag-17"}`))
	assert.Nil(t, err)
	assert.Equal(t, "tag-17", reading.Value)
	_, err = ParseReading([]byte(`{"seq":1}`))
	assert.EqualError(t, err, "the input has no value (v)")
	_, err = ParseReading([]byte(`{"v":[1]}`))
	assert.ErrorContains(t, err, "v must be a number")
	_, err = ParseReading([]byte(`{"v":1,"conf":1.5}`))
	assert.EqualError(t, err, "conf must be between 0 and 1, not 1.5")
	_, err = ParseReading([]byte(`not json`))
	assert.ErrorContains(t, err, "not valid JSON")

	heartbeat, err := ParseHeartbeat([]byte(`{"seq":9,"ch":{"stop":1}}`))
	if assert.Nil(t, err) {
		assert.Equal(t, int64(9), heartbeat.Seq)
		assert.Equal(t, map[string]any{"stop": 1.0}, heartbeat.Channels)
	}
	_, err = ParseHeartbeat([]byte(`{"seq":9,"ch":{"stop":null}}`))
	assert.ErrorContains(t, err, "channel stop")

	status, err := ParseStatus([]byte(`{"online":true,"up":8123,"rssi":-58,"ip":"10.0.100.61","heap":182000}`))
	if assert.Nil(t, err) {
		assert.True(t, *status.Online)
		assert.Equal(t, -58.0, *status.RSSI)
		assert.Equal(t, "10.0.100.61", status.IP)
	}
}

func TestCommandEqualAndClone(t *testing.T) {
	a := &Command{P: PatternBlink, C: []string{"#ff0000"}, Ms: 500, Br: number(0.6)}
	b := a.Clone()
	assert.True(t, a.Equal(b))
	b.C[0] = "#00ff00"
	assert.Equal(t, "#ff0000", a.C[0], "a clone shares nothing")
	assert.False(t, a.Equal(b))
	assert.False(t, a.Equal(nil))
	assert.True(t, (*Command)(nil).Equal(nil))
	assert.False(t, (&Command{V: number(0)}).Equal(&Command{}), "v=0 is a value, not an absent one")
}
