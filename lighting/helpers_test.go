package lighting

import (
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/rules"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

var testStations = []string{"R1", "R2", "R3", "B1", "B2", "B3"}

// fakeClock is a clock tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// harness is an engine on a device manager with the virtual transport, a database and the ball-count game, with
// the field as each test sets it.
type harness struct {
	t       *testing.T
	db      *model.Database
	bus     *bus.Bus
	devices *device.Manager
	virtual *device.VirtualTransport
	game    *gamedef.Game
	engine  *Engine
	clock   *fakeClock
	info    FieldInfo
	score   *scoring.State
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		t: t, db: model.SetupTestDb(t), bus: bus.New(100), virtual: device.NewVirtualTransport(),
		clock: &fakeClock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)},
	}
	h.game = model.TestGame("ball-count")
	h.score = scoring.NewState(h.game)
	var err error
	h.devices, err = device.NewManager(device.Options{Database: h.db, Bus: h.bus, Now: h.clock.Now})
	require.Nil(t, err)
	require.Nil(t, h.devices.AddTransport(h.virtual))
	require.Nil(t, h.devices.Start())
	t.Cleanup(h.devices.Stop)
	h.devices.SetGame(h.game, testStations)
	_, err = h.db.GetAllFieldProfiles()
	require.Nil(t, err)
	// The engine is given no bus here, so that what it does in the background (a reload when a device sends its
	// manifest) never races a test; the tests that need the bus subscribe it themselves.
	h.engine = NewEngine(Options{
		Devices: h.devices, Database: h.db, Field: func() FieldInfo { return h.info }, Now: h.clock.Now,
	})
	t.Cleanup(h.waitForReloads)
	return h
}

// connect connects an approved virtual device.
func (h *harness) connect(id string, channels ...*device.Channel) *device.VirtualDevice {
	h.t.Helper()
	if h.devices.Access(id) != device.AccessApproved {
		_, err := h.devices.AuthenticateLocal(device.VirtualTransportName, id)
		require.Nil(h.t, err)
		require.Nil(h.t, h.devices.Approve(id))
	}
	dev, err := h.virtual.Connect(&device.Manifest{Proto: 1, ID: id, Name: "Test " + id, Channels: channels})
	require.Nil(h.t, err)
	return dev
}

// connectGoal connects the device most tests light: a ten-LED strip, a digital lamp and a pwm dimmer.
func (h *harness) connectGoal() *device.VirtualDevice {
	return h.connect("goal", strip("strip", 10), out("lamp", device.TypeDigital), out("dimmer", device.TypePWM),
		out("horn", device.TypeRelay))
}

func strip(id string, pixels int) *device.Channel {
	return &device.Channel{ID: id, Dir: device.DirOut, Type: device.TypeLEDStrip, Pixels: pixels}
}

func out(id, channelType string) *device.Channel {
	return &device.Channel{ID: id, Dir: device.DirOut, Type: channelType}
}

// load imports a configuration for the active profile, which must have no errors.
func (h *harness) load(source string) {
	h.t.Helper()
	problems, err := h.engine.Import(h.game, model.DefaultFieldProfile, []byte(source))
	require.Nil(h.t, err, "%v", problems)
}

// waitForReloads waits for the reloads the engine started in the background to finish.
func (h *harness) waitForReloads() {
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.engine.mu.Lock()
		reloading := h.engine.reloading
		h.engine.mu.Unlock()
		if !reloading || time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// snapshot builds the state of the field: the match state ("idle", "running", "post"), the phase and seconds into
// it while running, and the match events since the last snapshot.
func (h *harness) snapshot(state, phase string, elapsed float64, events ...string) *device.Snapshot {
	h.t.Helper()
	result, err := scoring.Compute(h.game, h.score, scoring.Options{})
	require.Nil(h.t, err)
	clock := scoring.Clock{State: state, MatchRemaining: h.game.Duration()}
	if state == "running" {
		clock = scoring.ClockAt(h.game, phase, elapsed)
	}
	_, err = result.Live(clock)
	require.Nil(h.t, err)
	stations := make(map[string]device.StationState)
	for i, id := range testStations {
		stations[id] = device.StationState{Team: 100 + i, Connected: true, Ready: true}
	}
	scores := make(map[string]float64)
	for id, alliance := range result.Alliances {
		scores[id] = alliance.Score
	}
	return &device.Snapshot{
		Time: h.clock.Now(), Game: h.game, Clock: clock, Scores: scores, Stations: stations, Events: events,
		Eval: func(program *rules.Program, own string, extra map[string]any) (any, error) {
			return result.Eval(program, own, clock, extra)
		},
	}
}

// tick runs one output tick, as the arena does twenty times a second.
func (h *harness) tick(snapshot *device.Snapshot) {
	h.devices.UpdateOutputs(snapshot)
}

// idle runs a tick of the field before a match.
func (h *harness) idle(events ...string) {
	h.tick(h.snapshot("idle", "", 0, events...))
}

// running runs a tick of a running match.
func (h *harness) running(phase string, elapsed float64, events ...string) {
	h.tick(h.snapshot("running", phase, elapsed, events...))
}

func solid(colors ...string) *device.Command {
	return &device.Command{P: device.PatternSolid, C: colors}
}

func number(value float64) *float64 {
	return &value
}

// assertOutput checks the command in force on a channel of a virtual device.
func assertOutput(
	t *testing.T, expected *device.Command, dev *device.VirtualDevice, channel string, msgAndArgs ...any,
) {
	t.Helper()
	assert.Equal(t, expected, dev.Output(channel), msgAndArgs...)
}

// outputCount returns how many commands a channel of a virtual device has received.
func outputCount(dev *device.VirtualDevice, channel string) int {
	count := 0
	for _, command := range dev.History() {
		if command.Channel == channel {
			count++
		}
	}
	return count
}
