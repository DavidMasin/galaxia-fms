package web

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func decodeBindingsPageSet(t *testing.T, body []byte) bindingsPageSet {
	t.Helper()
	var response bindingsPageSet
	require.Nil(t, json.Unmarshal(body, &response), string(body))
	return response
}

func TestBindingsState(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")
	recorder := web.getHttpResponse("/setup/bindings/state")
	require.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"inputs":[]`, "an empty set has empty lists, not null")
	response := decodeBindingsPageSet(t, recorder.Body.Bytes())
	assert.Equal(t, "ball-count", response.Game)
	assert.Equal(t, "default", response.Profile)
	assert.Equal(t, []string{"R1", "R2", "R3", "B1", "B2", "B3"}, response.Stations)
	assert.Empty(t, response.Problems)
}

// The page sends the set it edits as JSON, and the YAML tab as YAML; either way the FMS answers with the set, its
// YAML and every problem, each problem naming the binding it belongs to.
func TestBindingsValidateRoundTrip(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")
	startTestDevices(t, web)
	_, err := web.arena.Devices.CreateDevice("beam", "Beam")
	require.Nil(t, err)
	_, err = web.arena.DeviceVirtual.Connect(&device.Manifest{Proto: 1, ID: "beam", Channels: []*device.Channel{
		{ID: "count", Dir: device.DirIn, Type: device.TypeCounter},
	}})
	require.Nil(t, err)

	body := `{"inputs":[
		{"id":"red-goal","device":"beam","channel":"count",
		 "action":{"score":"balls","op":"increment","alliance":"red"}},
		{"id":"blue-goal","device":"beam","channel":"count","action":{"score":"balls","op":"toggle","alliance":"blue"}},
		{"id":"later","device":"not-yet","channel":"button","action":{"field":"start_match"},"phases":["auto"]}
	],"outputs":[]}`
	recorder := web.requestWithBody("POST", "/setup/bindings/validate", body)
	require.Equal(t, 200, recorder.Code)
	response := decodeBindingsPageSet(t, recorder.Body.Bytes())
	byBinding := make(map[string][]device.Problem)
	for _, problem := range response.Problems {
		byBinding[problem.Binding] = append(byBinding[problem.Binding], problem)
	}
	assert.Empty(t, byBinding["red-goal"])
	if assert.Equal(t, 1, len(byBinding["blue-goal"])) {
		assert.False(t, byBinding["blue-goal"][0].Warning)
		assert.Contains(t, byBinding["blue-goal"][0].Message, "op toggle does not apply")
	}
	if assert.Equal(t, 1, len(byBinding["later"])) {
		assert.True(t, byBinding["later"][0].Warning)
	}
	assert.Equal(t, 3, len(response.Set.Inputs))
	assert.Contains(t, response.Yaml, "phases: [auto]")

	// The YAML it gave reads back as the same set.
	recorder = web.requestWithBody("POST", "/setup/bindings/validate", response.Yaml)
	require.Equal(t, 200, recorder.Code)
	again := decodeBindingsPageSet(t, recorder.Body.Bytes())
	assert.Equal(t, response.Set, again.Set)
	assert.Equal(t, response.Problems, again.Problems)
	assert.Equal(t, response.Yaml, again.Yaml)

	// A set that does not read, or has unknown keys, says why.
	for _, bad := range []string{"inputs: [", `{"inputs":[{"id":"x","debounce":30}]}`} {
		recorder = web.requestWithBody("POST", "/setup/bindings/validate", bad)
		assert.Equal(t, 400, recorder.Code, bad)
		response = decodeBindingsPageSet(t, recorder.Body.Bytes())
		assert.Contains(t, response.Error, "the bindings are not valid", bad)
		assert.Equal(t, 1, len(response.Problems))
	}
	recorder = web.requestWithBody("POST", "/setup/bindings/validate", `{"inputs":[{"id":"x","debounce":30}]}`)
	assert.Contains(t, recorder.Body.String(), "debounce")
}

func TestBindingsSaveExportImport(t *testing.T) {
	web := setupTestWeb(t)
	selectTestGame(t, web, "ball-count")

	// A set with errors is refused, and nothing changes.
	recorder := web.requestWithBody("POST", "/setup/bindings/save",
		`{"inputs":[{"id":"x","device":"beam","channel":"count","action":{"score":"nothing","op":"increment"}}]}`)
	assert.Equal(t, 422, recorder.Code)
	response := decodeBindingsPageSet(t, recorder.Body.Bytes())
	assert.Contains(t, response.Error, "the bindings have 1 problem(s)")
	assert.False(t, response.Saved)
	assert.Empty(t, web.arena.Devices.Bindings().Set.Inputs)

	// A good one is saved and in force, with its warnings.
	recorder = web.requestWithBody("POST", "/setup/bindings/save",
		`{"inputs":[{"id":"red-goal","device":"beam","channel":"count","action":{"score":"balls","op":"increment",`+
			`"alliance":"red"}}]}`)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	response = decodeBindingsPageSet(t, recorder.Body.Bytes())
	assert.True(t, response.Saved)
	assert.Equal(t, 1, len(response.Problems), "the device has not connected yet")
	assert.True(t, response.Problems[0].Warning)
	inForce := web.arena.Devices.Bindings().Set
	if assert.Equal(t, 1, len(inForce.Inputs)) {
		assert.Equal(t, "red-goal", inForce.Inputs[0].ID)
	}

	recorder = web.getHttpResponse("/setup/bindings/export")
	require.Equal(t, 200, recorder.Code)
	assert.Equal(t, "application/yaml", recorder.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="bindings-ball-count-default.yaml"`,
		recorder.Header().Get("Content-Disposition"))
	exported := recorder.Body.String()
	assert.Contains(t, exported, "id: red-goal")

	// Importing replaces the set; what was exported comes back exactly.
	recorder = web.requestWithBody("POST", "/setup/bindings/import", `
outputs:
  - { id: green, device: stack, channel: green, when: "match_state == 'running'" }
`)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	inForce = web.arena.Devices.Bindings().Set
	assert.Empty(t, inForce.Inputs)
	assert.Equal(t, 1, len(inForce.Outputs))
	recorder = web.requestWithBody("POST", "/setup/bindings/import", exported)
	require.Equal(t, 200, recorder.Code)
	assert.Equal(t, exported, web.getHttpResponse("/setup/bindings/export").Body.String())

	recorder = web.requestWithBody("POST", "/setup/bindings/import", "inputs: {")
	assert.Equal(t, 422, recorder.Code)
	assert.True(t, strings.Contains(recorder.Body.String(), "the bindings are not valid"))
	assert.Equal(t, exported, web.getHttpResponse("/setup/bindings/export").Body.String())
}
