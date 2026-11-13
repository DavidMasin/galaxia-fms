// The Bindings page: what each device input does to the match and what each output shows, for the active game and
// field profile. The page edits a binding set as cards and checks it with the server on every change; a set is
// saved only when it has no errors.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"io"
	"net/http"
	"regexp"
)

// bindingsPageSet is a binding set as the page edits it, with every problem it has and its YAML.
type bindingsPageSet struct {
	Game     string             `json:"game"`
	Profile  string             `json:"profile"`
	Stations []string           `json:"stations"` // The field's stations, which stops name.
	Set      *device.BindingSet `json:"set"`
	Yaml     string             `json:"yaml"`
	Problems device.Problems    `json:"problems"`
	Saved    bool               `json:"saved,omitempty"`
	Error    string             `json:"error,omitempty"`
}

var unsafeFileNameCharacters = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Shows the Bindings page.
func (web *Web) bindingsPageGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.renderDevicesPage(w, "templates/setup_bindings.html")
}

// Returns the binding set in force, with every problem it has now.
func (web *Web) bindingsPageStateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	state := web.arena.Devices.Bindings()
	writeJson(w, http.StatusOK, web.bindingsPageSet(state.Set, state.Problems))
}

func (web *Web) bindingsPageSet(set *device.BindingSet, problems device.Problems) bindingsPageSet {
	state := web.arena.Devices.Bindings()
	response := bindingsPageSet{
		Game: state.Game, Profile: state.Profile, Stations: web.arena.Devices.Environment().Stations, Set: set,
		Problems: problems,
	}
	if response.Problems == nil {
		response.Problems = device.Problems{}
	}
	if set.Inputs == nil {
		set.Inputs = []*device.InputBinding{}
	}
	if set.Outputs == nil {
		set.Outputs = []*device.OutputBinding{}
	}
	if yaml, err := set.YAML(); err == nil {
		response.Yaml = string(yaml)
	}
	return response
}

// readBindingsRequest reads a binding set from a request body, written as YAML or as JSON. It writes the error and
// returns nil if the set cannot be read.
func readBindingsRequest(w http.ResponseWriter, r *http.Request) *device.BindingSet {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDevicesPageRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusRequestEntityTooLarge, "the bindings are too large")
		return nil
	}
	set, err := device.ParseBindings(body)
	if err != nil {
		writeJson(w, http.StatusBadRequest, bindingsPageSet{
			Error: err.Error(), Problems: device.Problems{{Message: err.Error()}},
		})
		return nil
	}
	return set
}

// Checks a binding set against the active game and the devices, without saving it.
func (web *Web) bindingsValidateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	set := readBindingsRequest(w, r)
	if set == nil {
		return
	}
	writeJson(w, http.StatusOK, web.bindingsPageSet(set, web.arena.Devices.ValidateBindings(set)))
}

// Saves a binding set for the active game and profile and puts it in force. A set with errors is refused.
func (web *Web) bindingsSaveHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	set := readBindingsRequest(w, r)
	if set == nil {
		return
	}
	web.writeSavedBindings(w, set, func() (device.Problems, error) { return web.arena.Devices.SaveBindings(set) })
}

// Reads a binding set from YAML, as exported, and saves it.
func (web *Web) bindingsImportHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDevicesPageRequestBytes))
	if err != nil {
		writeJsonError(w, http.StatusRequestEntityTooLarge, "the bindings are too large")
		return
	}
	set, err := device.ParseBindings(body)
	if err != nil {
		// ImportBindings would refuse it for the same reason; the set it read is shown with the problems.
		set = &device.BindingSet{}
	}
	web.writeSavedBindings(w, set, func() (device.Problems, error) { return web.arena.Devices.ImportBindings(body) })
}

func (web *Web) writeSavedBindings(
	w http.ResponseWriter, set *device.BindingSet, save func() (device.Problems, error),
) {
	problems, err := save()
	if err != nil {
		response := web.bindingsPageSet(set, problems)
		response.Error = err.Error()
		writeJson(w, http.StatusUnprocessableEntity, response)
		return
	}
	state := web.arena.Devices.Bindings()
	response := web.bindingsPageSet(state.Set, state.Problems)
	response.Saved = true
	writeJson(w, http.StatusOK, response)
}

// Downloads the binding set in force as YAML.
func (web *Web) bindingsExportHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	data, err := web.arena.Devices.ExportBindings()
	if err != nil {
		handleWebErr(w, err)
		return
	}
	state := web.arena.Devices.Bindings()
	name := fmt.Sprintf("bindings-%s-%s.yaml", state.Game, state.Profile)
	name = unsafeFileNameCharacters.ReplaceAllString(name, "_")
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = w.Write(data)
}
