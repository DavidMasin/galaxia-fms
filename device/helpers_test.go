package device

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
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

// testGameSource is a small game with an element of every type that devices may change, so that every score
// operation can be tried.
const testGameSource = `
schema: 1
id: device-test
name: Device test
alliances:
  size: 3
  list:
    - { id: red, name: Red, short: R, color: "#d32f2f" }
    - { id: blue, name: Blue, short: B, color: "#1565c0" }
match:
  phases:
    - id: auto
      name: Auto
      duration: 15
      robots: auto
      cues: [{ at: start, sound: match_start, id: go }]
    - { id: teleop, name: Teleop, duration: 120, robots: teleop }
  score_grace: 2
elements:
  - { id: balls, name: Balls, type: counter, scope: alliance, points: 1, edit: [device, scorer] }
  - { id: lit, name: Lit, type: boolean, scope: alliance, points: 5, edit: [device] }
  - id: climb
    name: Climb
    type: enum
    scope: robot
    edit: [device]
    options: [{ id: none, name: None }, { id: low, name: Low, points: 3 }, { id: high, name: High, points: 6 }]
  - { id: balance, name: Balance, type: timer, scope: alliance, edit: [device] }
  - { id: payload, name: Payload, type: number, scope: alliance, edit: [device] }
  - { id: comets, name: Comets, type: counter, scope: field, edit: [device] }
  - { id: manual, name: Manual, type: counter, scope: alliance, edit: [scorer] }
live:
  - { id: hub_active, name: Hub active, scope: alliance, expr: "phase == 'teleop'" }
`

var testStations = []string{"R1", "R2", "R3", "B1", "B2", "B3"}

func testGame(t *testing.T) *gamedef.Game {
	game, diagnostics := gamedef.Parse([]byte(testGameSource))
	require.NotNil(t, game, "%v", diagnostics)
	return game
}

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

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// fakeScorer records the score changes it is given, and refuses them while refuse is set.
type fakeScorer struct {
	mu      sync.Mutex
	changes []scoring.Change
	refuse  error
}

func (s *fakeScorer) ApplyScoreChange(change scoring.Change) (scoring.Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse != nil {
		return change, s.refuse
	}
	s.changes = append(s.changes, change)
	return change, nil
}

func (s *fakeScorer) take() []scoring.Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	changes := s.changes
	s.changes = nil
	return changes
}

// harness is a manager with a database, a bus, a fake clock and score, the test game, and the virtual transport.
type harness struct {
	t       *testing.T
	m       *Manager
	db      *model.Database
	bus     *bus.Bus
	events  *bus.Subscription
	backlog []bus.Event
	clock   *fakeClock
	scorer  *fakeScorer
	virtual *VirtualTransport
	game    *gamedef.Game
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		t: t, db: model.SetupTestDb(t), bus: bus.New(1000), scorer: &fakeScorer{},
		clock: &fakeClock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}, virtual: NewVirtualTransport(),
		game: testGame(t),
	}
	h.events = h.bus.Subscribe(nil, 1000)
	var err error
	h.m, err = NewManager(Options{Database: h.db, Bus: h.bus, Scorer: h.scorer, Now: h.clock.Now})
	require.Nil(t, err)
	require.Nil(t, h.m.AddTransport(h.virtual))
	require.Nil(t, h.m.Start())
	t.Cleanup(h.m.Stop)
	h.m.SetGame(h.game, testStations)
	h.running("teleop")
	return h
}

// running puts the match in the given phase.
func (h *harness) running(phase string) {
	h.m.SetMatch(MatchInfo{Running: true, Phase: phase, Scoring: true})
}

// connect connects a virtual device that has already been approved.
func (h *harness) connect(id string, channels ...*Channel) *VirtualDevice {
	h.t.Helper()
	if h.m.Access(id) != AccessApproved {
		_, err := h.m.AuthenticateLocal(VirtualTransportName, id)
		require.Nil(h.t, err)
		require.Nil(h.t, h.m.Approve(id))
	}
	dev, err := h.virtual.Connect(&Manifest{Proto: 1, ID: id, Name: "Test " + id, Channels: channels})
	require.Nil(h.t, err)
	return dev
}

// bind saves a binding set given as YAML, which must have no errors.
func (h *harness) bind(source string) {
	h.t.Helper()
	set, err := ParseBindings([]byte(source))
	require.Nil(h.t, err)
	problems, err := h.m.SaveBindings(set)
	require.Nil(h.t, err, "%v", problems)
}

// eventsOf returns the data of the events of the given type published since they were last asked for. Events of
// other types are kept for later calls.
func (h *harness) eventsOf(kind string) []map[string]any {
	for done := false; !done; {
		select {
		case event := <-h.events.C:
			h.backlog = append(h.backlog, event)
		default:
			done = true
		}
	}
	var found []map[string]any
	var rest []bus.Event
	for _, event := range h.backlog {
		if event.Type == kind {
			data, _ := event.Data.(map[string]any)
			found = append(found, data)
		} else {
			rest = append(rest, event)
		}
	}
	h.backlog = rest
	return found
}

// snapshot builds a snapshot of the test game with the given phase and elapsed time, scored from an empty state.
func (h *harness) snapshot(phase string, elapsed float64, events ...string) *Snapshot {
	h.t.Helper()
	state := scoring.NewState(h.game)
	return h.snapshotOf(state, phase, elapsed, events...)
}

func (h *harness) snapshotOf(state *scoring.State, phase string, elapsed float64, events ...string) *Snapshot {
	h.t.Helper()
	result, err := scoring.Compute(h.game, state, scoring.Options{})
	require.Nil(h.t, err)
	clock := scoring.ClockAt(h.game, phase, elapsed)
	if phase == "" {
		clock = scoring.Clock{State: "idle", MatchRemaining: h.game.Duration()}
	}
	_, err = result.Live(clock)
	require.Nil(h.t, err)
	scores := make(map[string]float64)
	for id, alliance := range result.Alliances {
		scores[id] = alliance.Score
	}
	stations := make(map[string]StationState)
	for i, id := range testStations {
		stations[id] = StationState{Team: 100 + i, Connected: true, Ready: true}
	}
	return &Snapshot{
		Time: h.clock.Now(), Game: h.game, Clock: clock, Scores: scores, Stations: stations, Events: events,
		Eval: func(program *rules.Program, own string, extra map[string]any) (any, error) {
			return result.Eval(program, own, clock, extra)
		},
	}
}

func digital(id string) *Channel {
	return &Channel{ID: id, Dir: DirIn, Type: TypeDigital}
}

func safety(id string) *Channel {
	return &Channel{ID: id, Dir: DirIn, Type: TypeDigital, Safety: true}
}

func output(id, channelType string) *Channel {
	channel := &Channel{ID: id, Dir: DirOut, Type: channelType}
	if channelType == TypeLEDStrip {
		channel.Pixels = 30
	}
	return channel
}

// assertChange checks one score change, ignoring its source.
func assertChange(t *testing.T, change scoring.Change, alliance string, robot int, element, op string, value any) {
	t.Helper()
	assert.Equal(
		t, fmt.Sprintf("%s/%d/%s/%s/%v", alliance, robot, element, op, value),
		fmt.Sprintf("%s/%d/%s/%s/%v", change.Alliance, change.Robot, change.Element, change.Op, change.Value),
	)
}
