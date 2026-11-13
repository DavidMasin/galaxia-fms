// The engine: it keeps the lighting of the active field profile and game compiled, claims the fixtures' channels,
// and on every output tick works out the moment and drives every fixture.

package lighting

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"log"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"
)

// Owner is the name the engine claims its fixtures' channels under; the Bindings page shows it.
const Owner = "the Lighting page"

const (
	TestDuration     = 10 * time.Second
	IdentifyDuration = 3 * time.Second
	identifyPeriodMs = 400
	// How often a fixture whose channel another component drives is tried again, in case it was let go.
	conflictRetry = 5 * time.Second
	eventBuffer   = 64
)

// Options are what an engine is built with.
type Options struct {
	Devices  *device.Manager
	Database *model.Database
	Bus      *bus.Bus         // For the winner of each posted match and new manifests. May be nil.
	Field    func() FieldInfo // Called on the arena goroutine on every output tick. May be nil.
	Now      func() time.Time // The clock for Test and Identify; time.Now if nil. Tests replace it.
}

// Engine runs the lighting of the active field profile.
//
// Threading: the output tick runs on the arena goroutine and owns tick. mu guards everything else the tick shares
// with the pages: the compiled program (which never changes once compiled, so it is read without the lock once its
// pointer is taken), the claims, Test and Identify, the winner and what the page is shown. storeMu serialises
// reloads and every change to the stored configuration; it is taken before mu, and the tick never takes it, so the
// arena loop never waits for the database. The manager is only called without mu held.
type Engine struct {
	devices *device.Manager
	db      *model.Database
	field   func() FieldInfo
	now     func() time.Time
	events  *bus.Subscription

	// LiveNotifier sends what every fixture shows whenever it changes; ConfigNotifier says that the configuration
	// of the active profile was reloaded.
	LiveNotifier   *websocket.Notifier
	ConfigNotifier *websocket.Notifier

	storeMu sync.Mutex

	mu           sync.Mutex
	program      *program
	loaded       configKey // What program was compiled for.
	requested    configKey // What the tick last asked to be loaded.
	pendingGame  *gamedef.Game
	reloading    bool
	reloadAgain  bool
	loadError    string
	revision     int
	claimed      map[string]bool   // By fixture id: its channel is the engine's to drive.
	conflicts    map[string]string // By fixture id: why its channel is not.
	test         *testState
	identify     map[string]time.Time // By fixture id: until when it blinks white.
	winner       string               // The alliance that won the last posted match; empty for a tie.
	lastSnapshot *device.Snapshot
	live         Live

	tick tickState // The arena goroutine's own.
}

type configKey struct {
	game    *gamedef.Game
	profile string
}

type testState struct {
	moment string
	until  time.Time
}

// tickState is what the output tick keeps from one tick to the next. Only the arena goroutine touches it.
type tickState struct {
	prog          *program
	moments       fieldMoments
	eval          evaluator
	active        []int
	activeIds     []string
	picks         []*cellProgram
	driven        map[string]drivenOutput // By fixture id: the command last sent.
	levels        map[string]*levelState
	identifyStart map[string]time.Time
	cellErrors    map[int]string    // By cell index: the last evaluation failure.
	driveErrors   map[string]string // By fixture id.
	winner        string
	lastRetry     time.Time
}

type drivenOutput struct {
	ref     device.ChannelRef
	command *device.Command
}

// Live is what the hardware shows right now, for the page.
type Live struct {
	Time     int64                      `json:"time"` // Server UTC milliseconds when it was worked out.
	Profile  string                     `json:"profile"`
	Game     string                     `json:"game"`
	Moment   string                     `json:"moment"` // The highest-priority moment that applies, or the test's.
	Active   []string                   `json:"active"` // Every moment that applies, highest priority first.
	Test     *TestStatus                `json:"test"`
	Identify map[string]int64           `json:"identify"` // Fixture id -> UTC milliseconds it blinks until.
	Fixtures map[string]*device.Command `json:"fixtures"` // Fixture id -> the command in force.
	Winner   string                     `json:"winner"`
	Errors   Problems                   `json:"errors"` // Expressions that failed and outputs that could not be driven.
}

// TestStatus is the Test in progress.
type TestStatus struct {
	Moment string `json:"moment"`
	Until  int64  `json:"until"` // UTC milliseconds.
}

// Status is the state of the configuration in force, for the page.
type Status struct {
	Profile   string            `json:"profile"`
	Game      string            `json:"game"`
	Loaded    bool              `json:"loaded"`
	Reloading bool              `json:"reloading"` // A reload is under way in the background.
	LoadError string            `json:"loadError,omitempty"`
	Revision  int               `json:"revision"`
	Problems  Problems          `json:"problems"`
	Claimed   map[string]bool   `json:"claimed"`
	Conflicts map[string]string `json:"conflicts"`
}

// NewEngine builds an engine and hooks it to the manager's output ticks. Nothing is loaded until the first tick
// says which game is active, or Reload is called.
func NewEngine(options Options) *Engine {
	e := &Engine{
		devices: options.Devices, db: options.Database, field: options.Field, now: options.Now,
		claimed: map[string]bool{}, conflicts: map[string]string{}, identify: map[string]time.Time{},
	}
	if e.now == nil {
		e.now = time.Now
	}
	e.tick = tickState{
		moments: fieldMoments{cueTimes: map[string]time.Time{}}, driven: map[string]drivenOutput{},
		levels: map[string]*levelState{}, identifyStart: map[string]time.Time{}, cellErrors: map[int]string{},
		driveErrors: map[string]string{},
	}
	e.live = Live{Identify: map[string]int64{}, Fixtures: map[string]*device.Command{}, Errors: Problems{}}
	e.LiveNotifier = websocket.NewNotifier("lightingLive", func() any { return e.Live() })
	e.ConfigNotifier = websocket.NewNotifier("lightingConfig", func() any { return e.Status() })
	if options.Bus != nil {
		e.events = options.Bus.Subscribe([]string{"device.manifest", "match.committed"}, eventBuffer)
	}
	e.devices.OnOutputTick(e.onOutputTick)
	return e
}

// Reload reads the lighting of the active profile for the given game, compiles it, and claims its channels. The
// pages call it after every change; the engine calls it by itself, in the background, when the game, the profile
// or a device's manifest changes.
func (e *Engine) Reload(game *gamedef.Game) error {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	profile := e.devices.Profile()
	config, loadErr := &Config{Profile: profile, Scenes: Scenes{}}, error(nil)
	if game != nil && e.db != nil {
		var profileId int
		if profileId, loadErr = e.db.GetLightingProfileId(profile); loadErr == nil && profileId != 0 {
			var loaded *Config
			if loaded, loadErr = e.load(game, profileId, profile, true); loadErr == nil {
				config = loaded
			}
		}
	}
	prog := compile(config, e.environment(game))
	claimed, conflicts := e.claim(prog)

	e.mu.Lock()
	e.program, e.loaded = prog, configKey{game: game, profile: profile}
	if game != nil {
		// What the tick would ask for, unless the game or profile has moved on since, which it then notices; and
		// what a reload the engine starts by itself (for a manifest, say) loads.
		e.requested, e.pendingGame = e.loaded, game
	}
	e.claimed, e.conflicts = claimed, conflicts
	e.loadError = ""
	if loadErr != nil {
		e.loadError = fmt.Sprintf("the lighting of field profile %q could not be read: %v", profile, loadErr)
	}
	e.revision++
	status := e.statusLocked()
	e.mu.Unlock()
	e.ConfigNotifier.NotifyWithMessage(status)
	if loadErr != nil {
		return fmt.Errorf("%s", status.LoadError)
	}
	return nil
}

// environment is what lighting is compiled against right now.
func (e *Engine) environment(game *gamedef.Game) Environment {
	env := e.devices.Environment()
	return Environment{Game: game, Stations: env.Stations, Manifests: env.Manifests}
}

// claim claims the channels of every fixture that can run, except those the bindings or another component drive,
// and returns which were claimed and why the others were not. The caller holds storeMu, so claims are only ever
// made by one goroutine at a time.
func (e *Engine) claim(prog *program) (map[string]bool, map[string]string) {
	for attempt := 0; ; attempt++ {
		claimed, conflicts := map[string]bool{}, map[string]string{}
		bound := make(map[device.ChannelRef]string)
		for _, output := range e.devices.Bindings().Set.Outputs {
			bound[device.ChannelRef{Device: output.Device, Channel: output.Channel}] = output.ID
		}
		claims := e.devices.Claims()
		var refs []device.ChannelRef
		for _, fp := range prog.fixtures {
			id := fp.fixture.ID
			switch {
			case !fp.runnable || prog.byID[id] != fp:
			case bound[fp.ref] != "":
				conflicts[id] = fmt.Sprintf(
					"channel %s is driven by output binding %q; remove the binding to light it from here", fp.ref,
					bound[fp.ref],
				)
			case claims[fp.ref] != "" && claims[fp.ref] != Owner:
				conflicts[id] = fmt.Sprintf("channel %s is driven by %s", fp.ref, claims[fp.ref])
			default:
				refs = append(refs, fp.ref)
				claimed[id] = true
			}
		}
		err := e.devices.ClaimOutputs(Owner, refs)
		if err == nil {
			return claimed, conflicts
		}
		if attempt > 0 {
			// The bindings changed between the check and the claim, twice: claim nothing for now and try again
			// later.
			e.devices.ReleaseOutputs(Owner)
			for id := range claimed {
				conflicts[id] = err.Error()
			}
			return map[string]bool{}, conflicts
		}
	}
}

// requestReloadLocked asks for a reload in the background, for the game last seen. The caller holds mu.
func (e *Engine) requestReloadLocked() {
	if e.pendingGame == nil {
		return
	}
	if e.reloading {
		e.reloadAgain = true
		return
	}
	e.reloading = true
	go func() {
		for {
			e.mu.Lock()
			game := e.pendingGame
			e.mu.Unlock()
			if err := e.Reload(game); err != nil {
				log.Printf("Lighting: %v", err)
			}
			e.mu.Lock()
			if !e.reloadAgain {
				e.reloading = false
				e.mu.Unlock()
				return
			}
			e.reloadAgain = false
			e.mu.Unlock()
		}
	}()
}

func (e *Engine) onOutputTick(snapshot *device.Snapshot) {
	var info FieldInfo
	if e.field != nil {
		info = e.field()
	}
	e.update(snapshot, info)
}

// update works out the moment and drives every fixture. It runs on the arena goroutine on every output tick, after
// the bindings, and does no I/O: a reload it needs runs in the background.
func (e *Engine) update(snapshot *device.Snapshot, info FieldInfo) {
	now := snapshot.Time
	t := &e.tick
	profile := e.devices.Profile()
	faulted := e.devices.Faulted()

	e.mu.Lock()
	if key := (configKey{game: snapshot.Game, profile: profile}); snapshot.Game != nil && key != e.requested {
		e.requested, e.pendingGame = key, snapshot.Game
		e.requestReloadLocked()
	}
	e.takeEventsLocked()
	if len(e.conflicts) > 0 && now.Sub(t.lastRetry) >= conflictRetry {
		t.lastRetry = now
		e.requestReloadLocked()
	}
	e.lastSnapshot = snapshot
	prog := e.program
	claimed := e.claimed
	changed := false
	if e.test != nil && !now.Before(e.test.until) {
		e.test, changed = nil, true
	}
	if e.test != nil && prog != nil {
		if _, ok := prog.momentIndex[e.test.moment]; !ok {
			// The game changed under the test.
			e.test, changed = nil, true
		}
	}
	var test *testState
	if e.test != nil {
		test = &testState{moment: e.test.moment, until: e.test.until}
	}
	var identify map[string]time.Time
	for id, until := range e.identify {
		if now.Before(until) {
			if identify == nil {
				identify = make(map[string]time.Time, len(e.identify))
			}
			identify[id] = until
		} else {
			delete(e.identify, id)
			changed = true
		}
	}
	winner := e.winner
	e.mu.Unlock()

	for id := range t.identifyStart {
		if _, ok := identify[id]; !ok {
			delete(t.identifyStart, id)
			changed = true
		}
	}
	for id := range identify {
		if _, ok := t.identifyStart[id]; !ok {
			t.identifyStart[id] = now
			changed = true
		}
	}

	t.moments.observe(snapshot)
	if prog != t.prog {
		t.prog, changed = prog, true
		// Everything is sent again: the manager only passes on what differs from what the device has.
		clear(t.driven)
		clear(t.levels)
		clear(t.cellErrors)
		clear(t.driveErrors)
	}
	if winner != t.winner {
		t.winner, changed = winner, true
	}
	if prog == nil || prog.game != snapshot.Game {
		// Not loaded yet, or the game just changed and its lighting is being loaded: hold what is shown.
		if changed {
			e.publishLive(now, nil, test, identify)
		}
		return
	}

	// The moments that apply, highest priority first. A test shows its moment over idle.
	if test != nil {
		t.active = append(t.active[:0], prog.momentIndex[test.moment])
		if idle := prog.momentIndex[MomentIdle]; idle != t.active[0] {
			t.active = append(t.active, idle)
		}
	} else {
		t.active = t.moments.active(prog, snapshot, info, faulted, t.active[:0])
	}
	same := len(t.active) == len(t.activeIds)
	for i := 0; same && i < len(t.active); i++ {
		same = prog.moments[t.active[i]].ID == t.activeIds[i]
	}
	if !same {
		t.activeIds = make([]string, len(t.active))
		for i, index := range t.active {
			t.activeIds[i] = prog.moments[index].ID
		}
		changed = true
	}

	t.eval.begin(prog, snapshot, e.devices, test != nil)
	r := renderer{prog: prog, eval: &t.eval, active: t.active, winner: prog.winnerColor(winner), picks: t.picks}
	type drive struct {
		fp      *fixtureProgram
		command *device.Command
	}
	var drives []drive
	for _, fp := range prog.fixtures {
		id := fp.fixture.ID
		if !claimed[id] {
			continue
		}
		var command *device.Command
		if start, ok := t.identifyStart[id]; ok {
			command = identifyCommand(fp, now.Sub(start))
		} else {
			if fp.kind == KindLEDStrip {
				command = r.strip(fp)
			} else {
				state := t.levels[id]
				if state == nil {
					state = &levelState{}
					t.levels[id] = state
				}
				command = r.level(fp, r.single(fp), state, now)
			}
		}
		if last, ok := t.driven[id]; ok && last.ref == fp.ref && sameCommand(last.command, command) {
			continue
		}
		drives = append(drives, drive{fp: fp, command: command})
	}
	t.picks = r.picks

	// Evaluation failures, kept until the cell evaluates again without one.
	for _, cell := range prog.cells {
		state := &t.eval.memo[cell.index]
		if state.stamp != t.eval.stamp {
			continue
		}
		if state.err != t.cellErrors[cell.index] {
			changed = true
			if state.err == "" {
				delete(t.cellErrors, cell.index)
			} else {
				t.cellErrors[cell.index] = state.err
				log.Printf("Lighting: %s at %s: %s", cell.target.id, prog.moments[cell.moment].ID, state.err)
			}
		}
	}

	for _, d := range drives {
		id := d.fp.fixture.ID
		if err := e.devices.DriveOutput(Owner, d.fp.ref, d.command); err != nil {
			delete(t.driven, id)
			if t.driveErrors[id] != err.Error() {
				t.driveErrors[id], changed = err.Error(), true
			}
			continue
		}
		t.driven[id] = drivenOutput{ref: d.fp.ref, command: d.command}
		changed = true
		if _, failed := t.driveErrors[id]; failed {
			delete(t.driveErrors, id)
		}
	}
	for id := range t.driven {
		if !claimed[id] {
			delete(t.driven, id)
			changed = true
		}
	}
	if changed {
		e.publishLive(now, prog, test, identify)
	}
}

// publishLive updates what the page is shown and sends it.
func (e *Engine) publishLive(now time.Time, prog *program, test *testState, identify map[string]time.Time) {
	t := &e.tick
	live := Live{
		Time: now.UnixMilli(), Active: append([]string{}, t.activeIds...),
		Identify: make(map[string]int64, len(identify)), Fixtures: make(map[string]*device.Command, len(t.driven)),
		Winner: t.winner, Errors: Problems{},
	}
	if prog != nil && prog.game != nil {
		live.Game = prog.game.ID
	}
	if len(live.Active) > 0 {
		live.Moment = live.Active[0]
	}
	if test != nil {
		live.Test = &TestStatus{Moment: test.moment, Until: test.until.UnixMilli()}
	}
	for id, until := range identify {
		live.Identify[id] = until.UnixMilli()
	}
	for id, output := range t.driven {
		live.Fixtures[id] = output.command.Clone()
	}
	if prog != nil {
		for _, cell := range prog.cells {
			if message, ok := t.cellErrors[cell.index]; ok {
				live.Errors = append(live.Errors, Problem{
					Fixture: cell.target.fixture.fixture.ID, Moment: prog.moments[cell.moment].ID,
					Target: cell.target.id, Message: message,
				})
			}
		}
	}
	for _, id := range sortedKeys(t.driveErrors) {
		live.Errors = append(live.Errors, Problem{Fixture: id, Message: t.driveErrors[id]})
	}
	e.mu.Lock()
	live.Profile = e.loaded.profile
	e.live = live
	e.mu.Unlock()
	e.LiveNotifier.NotifyWithMessage(live)
}

// takeEventsLocked reads what the bus has said since the last tick: the winner of each posted match, and manifests
// that may change what a fixture's channel is. The caller holds mu.
func (e *Engine) takeEventsLocked() {
	if e.events == nil {
		return
	}
	for {
		select {
		case event, ok := <-e.events.C:
			if !ok {
				e.events = nil
				return
			}
			switch event.Type {
			case "match.committed":
				if data, ok := event.Data.(map[string]any); ok {
					if winner, ok := data["winner"].(string); ok {
						e.winner = winner
					}
				}
			case "device.manifest":
				e.requestReloadLocked()
			}
		default:
			return
		}
	}
}

// observe notes the match events of a snapshot: when each cue fired and when a score was posted.
func (m *fieldMoments) observe(snapshot *device.Snapshot) {
	for _, event := range snapshot.Events {
		switch {
		case event == device.EventScorePosted:
			m.postedAt, m.postedSinceEnd = snapshot.Time, true
		case strings.HasPrefix(event, cuePrefix):
			m.cueTimes[event] = snapshot.Time
		}
	}
	if snapshot.Clock.State != "post" {
		// A new match ends without a score posted.
		m.postedSinceEnd = false
	}
	for id, at := range m.cueTimes {
		if snapshot.Time.Sub(at) >= CueDuration {
			delete(m.cueTimes, id)
		}
	}
}

// active appends to out the index of every moment that applies now, highest priority first, ending with idle.
func (m *fieldMoments) active(
	prog *program, snapshot *device.Snapshot, info FieldInfo, faulted bool, out []int,
) []int {
	add := func(id string) {
		if index, ok := prog.momentIndex[id]; ok {
			out = append(out, index)
		}
	}
	estop := info.FieldStop
	for _, station := range snapshot.Stations {
		estop = estop || station.EStop
	}
	if estop {
		add(MomentEStop)
	}
	if faulted {
		add(MomentFault)
	}
	if len(m.cueTimes) > 0 {
		// The most recent cue first.
		first := len(out)
		for id := range m.cueTimes {
			add(id)
		}
		cues := out[first:]
		sort.Slice(cues, func(i, j int) bool {
			a, b := m.cueTimes[prog.moments[cues[i]].ID], m.cueTimes[prog.moments[cues[j]].ID]
			if !a.Equal(b) {
				return a.After(b)
			}
			return cues[i] < cues[j]
		})
	}
	state := snapshot.Clock.State
	if index, ok := prog.phaseIndex[snapshot.Clock.Phase]; ok && state == "running" {
		out = append(out, index)
	}
	if !m.postedAt.IsZero() && snapshot.Time.Sub(m.postedAt) < ScorePostedDuration {
		add(MomentScorePosted)
	}
	if state == "post" && !m.postedSinceEnd {
		add(MomentMatchEnd)
	}
	if snapshot.FieldReset {
		add(MomentFieldReset)
	}
	if snapshot.Volunteers {
		add(MomentVolunteers)
	}
	if state == "idle" && info.TeamsReady {
		add(MomentTeamsReady)
	}
	if info.Awards {
		add(MomentAwards)
	}
	add(MomentIdle)
	return out
}

// identifyCommand blinks a fixture white, at its brightness limit.
func identifyCommand(fp *fixtureProgram, elapsed time.Duration) *device.Command {
	if fp.kind == KindLEDStrip {
		return &device.Command{
			P: device.PatternBlink, C: []string{white}, Ms: identifyPeriodMs, Br: brightness(fp.limit),
		}
	}
	high := 1.0
	if fp.kind == KindPWM {
		high = fp.limit
	}
	if elapsed%(identifyPeriodMs*time.Millisecond) >= identifyPeriodMs*time.Millisecond/2 {
		high = 0
	}
	return levelCommand(high)
}

// Test shows a moment on the hardware for TestDuration, in place of the live moments, or until cancelled. The
// moment is shown as designed: its conditions are taken as true.
func (e *Engine) Test(moment string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.program == nil {
		return fmt.Errorf("the lighting has not been loaded yet")
	}
	if _, ok := e.program.momentIndex[moment]; !ok {
		return fmt.Errorf("%q is not a moment of the active game", moment)
	}
	e.test = &testState{moment: moment, until: e.now().Add(TestDuration)}
	return nil
}

// CancelTest ends the Test in progress.
func (e *Engine) CancelTest() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.test != nil {
		e.test.until = time.Time{}
	}
}

// Identify asks a fixture's device to identify itself and blinks the fixture white for IdentifyDuration. The
// blinking happens even if the device cannot be sent the command, whose error is returned.
func (e *Engine) Identify(fixtureId string) error {
	e.mu.Lock()
	fp := (*fixtureProgram)(nil)
	if e.program != nil {
		fp = e.program.byID[fixtureId]
	}
	if fp == nil {
		e.mu.Unlock()
		return fmt.Errorf("there is no fixture %q in the active field profile", fixtureId)
	}
	e.identify[fixtureId] = e.now().Add(IdentifyDuration)
	e.mu.Unlock()
	return e.devices.Identify(fp.fixture.Device)
}

// Preview returns what every fixture of the active profile would show at a moment, as designed (conditions taken
// as true), with fills from the live score.
func (e *Engine) Preview(moment string) (map[string]*device.Command, error) {
	e.mu.Lock()
	prog, snapshot, winner := e.program, e.lastSnapshot, e.winner
	e.mu.Unlock()
	if prog == nil {
		return nil, fmt.Errorf("the lighting has not been loaded yet")
	}
	index, ok := prog.momentIndex[moment]
	if !ok {
		return nil, fmt.Errorf("%q is not a moment of the active game", moment)
	}
	active := []int{index}
	if idle := prog.momentIndex[MomentIdle]; idle != index {
		active = append(active, idle)
	}
	ev := &evaluator{}
	ev.begin(prog, snapshot, e.devices, true)
	r := renderer{prog: prog, eval: ev, active: active, winner: prog.winnerColor(winner)}
	commands := make(map[string]*device.Command, len(prog.fixtures))
	for _, fp := range prog.fixtures {
		if !fp.runnable || prog.byID[fp.fixture.ID] != fp {
			continue
		}
		if fp.kind == KindLEDStrip {
			commands[fp.fixture.ID] = r.strip(fp)
		} else {
			commands[fp.fixture.ID] = r.designedLevel(fp, r.single(fp))
		}
	}
	return commands, nil
}

// CellValue is the live value of a cell's expressions, for the swatches of the scene grid.
type CellValue struct {
	Fill    *float64 `json:"v,omitempty"`
	Applies bool     `json:"when"`
	Error   string   `json:"error,omitempty"`
}

// CellValues evaluates every cell of the active profile that has an expression against the live score: moment ->
// target -> value.
func (e *Engine) CellValues() map[string]map[string]CellValue {
	e.mu.Lock()
	prog, snapshot := e.program, e.lastSnapshot
	e.mu.Unlock()
	values := make(map[string]map[string]CellValue)
	if prog == nil {
		return values
	}
	ev := &evaluator{}
	ev.begin(prog, snapshot, e.devices, false)
	for _, cell := range prog.cells {
		if cell.when == nil && cell.v == nil {
			continue
		}
		state := ev.state(cell)
		value := CellValue{Applies: state.applies, Error: state.err}
		if cell.v != nil && state.err == "" {
			fill := state.fill
			value.Fill = &fill
		}
		moment := prog.moments[cell.moment].ID
		if values[moment] == nil {
			values[moment] = make(map[string]CellValue)
		}
		values[moment][cell.target.id] = value
	}
	return values
}

// Live returns what the hardware shows right now.
func (e *Engine) Live() Live {
	e.mu.Lock()
	defer e.mu.Unlock()
	live := e.live
	live.Active = append([]string{}, e.live.Active...)
	live.Identify = maps.Clone(e.live.Identify)
	live.Fixtures = make(map[string]*device.Command, len(e.live.Fixtures))
	for id, command := range e.live.Fixtures {
		live.Fixtures[id] = command.Clone()
	}
	live.Errors = append(Problems{}, e.live.Errors...)
	return live
}

// Status returns the state of the configuration in force.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked()
}

func (e *Engine) statusLocked() Status {
	status := Status{
		Profile: e.loaded.profile, Loaded: e.program != nil, Reloading: e.reloading, LoadError: e.loadError,
		Revision: e.revision,
		Problems: Problems{}, Claimed: maps.Clone(e.claimed), Conflicts: maps.Clone(e.conflicts),
	}
	if e.loaded.game != nil {
		status.Game = e.loaded.game.ID
	}
	if e.program != nil {
		status.Problems = append(status.Problems, e.program.problems...)
	}
	return status
}
