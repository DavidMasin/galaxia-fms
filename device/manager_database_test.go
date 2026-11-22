package device

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// otherDatabase returns a database that another manager, with the test game and a virtual transport of its own,
// has filled: the stand-in for a backup that is restored. That manager's clock starts at the given time, and it is
// stopped before the database is returned.
func otherDatabase(
	t *testing.T, at time.Time, fill func(m *Manager, virtual *VirtualTransport, clock *fakeClock),
) *model.Database {
	t.Helper()
	db := model.SetupTestDb(t)
	clock := &fakeClock{now: at}
	m, err := NewManager(Options{Database: db, Now: clock.Now})
	require.Nil(t, err)
	virtual := NewVirtualTransport()
	require.Nil(t, m.AddTransport(virtual))
	require.Nil(t, m.Start())
	m.SetGame(testGame(t), testStations)
	fill(m, virtual, clock)
	m.Stop()
	return db
}

// approveIn approves a virtual device in a manager and connects it.
func approveIn(t *testing.T, m *Manager, virtual *VirtualTransport, id string, channels ...*Channel) *VirtualDevice {
	t.Helper()
	_, err := m.AuthenticateLocal(VirtualTransportName, id)
	require.Nil(t, err)
	require.Nil(t, m.Approve(id))
	dev, err := virtual.Connect(&Manifest{Proto: 1, ID: id, Name: "Test " + id, Channels: channels})
	require.Nil(t, err)
	return dev
}

// bindIn saves a binding set given as YAML in a manager.
func bindIn(t *testing.T, m *Manager, source string) {
	t.Helper()
	set, err := ParseBindings([]byte(source))
	require.Nil(t, err)
	problems, err := m.SaveBindings(set)
	require.Nil(t, err, "%v", problems)
}

// accessRecorder is a transport that only notes which devices it was told had their access changed.
type accessRecorder struct {
	mu      sync.Mutex
	changed []string
}

func (r *accessRecorder) Name() string                        { return "recorder" }
func (r *accessRecorder) Start(Sink) error                    { return nil }
func (r *accessRecorder) Stop()                               {}
func (r *accessRecorder) SendOutput(string, string, *Command) {}
func (r *accessRecorder) SendCommand(string, DeviceCommand)   {}
func (r *accessRecorder) SendField(FieldState)                {}

func (r *accessRecorder) AccessChanged(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changed = append(r.changed, deviceID)
}

func (r *accessRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := r.changed
	r.changed = nil
	sort.Strings(changed)
	return changed
}

// restore gives the harness's manager another database the way restoring a backup does: the old one is closed
// first. The manager is stopped before the database it now uses is closed, as it is for the one it started with.
func (h *harness) restore(db *model.Database, profile string) {
	h.t.Helper()
	require.Nil(h.t, h.db.Close())
	require.Nil(h.t, h.m.Configure(Setup{Database: db, Profile: profile, Game: h.game, Stations: testStations}))
	h.t.Cleanup(h.m.Stop)
}

func deviceIds(m *Manager) []string {
	var ids []string
	for _, info := range m.Devices() {
		ids = append(ids, info.ID)
	}
	return ids
}

func counter(id string) *Channel {
	return &Channel{ID: id, Dir: DirIn, Type: TypeCounter}
}

func TestSetDatabase(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.CreateDevice("old-1", "Only in the old database")
	require.Nil(t, err)
	h.connect("arcade-1", digital("button")).Disconnect()
	h.bind(`
inputs:
  - { id: old-button, device: arcade-1, channel: button, action: { field: start_match } }
`)

	hourAgo := h.clock.Now().Add(-time.Hour)
	restored := otherDatabase(t, hourAgo, func(m *Manager, virtual *VirtualTransport, _ *fakeClock) {
		approveIn(t, m, virtual, "arcade-1", digital("button"), digital("second"))
		require.Nil(t, m.SetName("arcade-1", "Arcade button"))
		require.Nil(t, m.SetNotes("arcade-1", "By the scoring table"))
		require.Nil(t, m.SetRequired("arcade-1", true))
		bindIn(t, m, `
inputs:
  - { id: default-button, device: arcade-1, channel: second, action: { field: start_match } }
`)
		m.SetProfile("tent")
		bindIn(t, m, `
inputs:
  - { id: tent-button, device: arcade-1, channel: second, action: { field: field_reset } }
`)
	})

	// The manager is given the new database with the field profile the restored event uses.
	h.restore(restored, "tent")

	// The devices and the binding set in force are the restored database's.
	assert.Equal(t, []string{"arcade-1"}, deviceIds(h.m))
	info, _ := h.m.Device("arcade-1")
	assert.Equal(t, "Arcade button", info.Name)
	assert.Equal(t, "By the scoring table", info.Notes)
	assert.True(t, info.Required)
	assert.Equal(t, model.DeviceApproved, info.State)
	assert.False(t, info.Online)
	assert.NotNil(t, info.Manifest.Channel("second"), "an offline device has the manifest the restored database holds")
	bindings := h.m.Bindings()
	assert.Equal(t, "tent", bindings.Profile)
	assert.Empty(t, bindings.Problems)
	if assert.Equal(t, 1, len(bindings.Set.Inputs)) {
		assert.Equal(t, "tent-button", bindings.Set.Inputs[0].ID)
	}
	h.m.SetProfile("default")
	if bindings = h.m.Bindings(); assert.Equal(t, 1, len(bindings.Set.Inputs)) {
		assert.Equal(t, "default-button", bindings.Set.Inputs[0].ID)
	}

	// And everything is saved to it. The old database is closed, so nothing can have gone there.
	require.Nil(t, h.m.SetNotes("arcade-1", "Moved to the gate"))
	record, _ := restored.GetDeviceByDeviceId("arcade-1")
	assert.Equal(t, "Moved to the gate", record.Notes)
	_, err = h.m.CreateDevice("new-1", "Made after the restore")
	require.Nil(t, err)
	record, _ = restored.GetDeviceByDeviceId("new-1")
	if assert.NotNil(t, record) {
		assert.Equal(t, "Made after the restore", record.Name)
	}
	_, err = h.m.Authenticate(MqttTransportName, "walk-in", "its-token")
	require.Nil(t, err)
	record, _ = restored.GetDeviceByDeviceId("walk-in")
	if assert.NotNil(t, record) {
		assert.Equal(t, model.DevicePending, record.State)
	}
	h.bind(`
inputs:
  - { id: saved-after, device: arcade-1, channel: button, action: { field: volunteers } }
`)
	stored, err := restored.GetBindingSet(h.game.ID, "default")
	require.Nil(t, err)
	assert.Contains(t, stored.Yaml, "saved-after")
	stored, _ = restored.GetBindingSet(h.game.ID, "tent")
	assert.Contains(t, stored.Yaml, "tent-button", "the other profile's set is untouched")
	require.Nil(t, h.m.Delete("new-1"))
	record, _ = restored.GetDeviceByDeviceId("new-1")
	assert.Nil(t, record)

	// A manager started on the restored database finds what this one left there.
	restarted, err := NewManager(Options{Database: restored})
	require.Nil(t, err)
	assert.Equal(t, []string{"arcade-1", "walk-in"}, deviceIds(restarted))

	// Given the database it already has, the manager only takes the profile and the game.
	require.Nil(t, h.m.Configure(Setup{Database: restored, Profile: "tent", Game: h.game, Stations: testStations}))
	assert.Equal(t, "tent", h.m.Profile())
	assert.Equal(t, "tent-button", h.m.Bindings().Set.Inputs[0].ID)
	assert.Equal(t, []string{"arcade-1", "walk-in"}, deviceIds(h.m))
}

func TestSetDatabaseWithConnectedDevices(t *testing.T) {
	h := newHarness(t)
	recorder := &accessRecorder{}
	require.Nil(t, h.m.AddTransport(recorder))

	// Approved here and in the restored database.
	known := h.connect("known", digital("button"), output("lamp", TypeDigital))
	require.Nil(t, h.m.SetName("known", "Old name"))
	require.Nil(t, known.SetInput("button", 1))
	// Approved here, unknown there; one without a token and one with.
	stranger := h.connect("stranger", digital("button"))
	_, err := h.m.Authenticate(MqttTransportName, "mq-stranger", "secret")
	require.Nil(t, err)
	require.Nil(t, h.m.Approve("mq-stranger"))
	h.m.Online(MqttTransportName, "mq-stranger", "10.0.100.7:50123")
	// Unknown there and not connected.
	h.connect("away", digital("button")).Disconnect()
	// Pending here, approved there.
	newcomer, err := h.virtual.Connect(&Manifest{Proto: 1, ID: "newcomer", Channels: []*Channel{digital("button")}})
	require.Nil(t, err)
	// Approved here, blocked there.
	banned := h.connect("banned", digital("button"))
	// Approved in both, with another token there.
	_, err = h.m.Authenticate(MqttTransportName, "mq-known", "token-here")
	require.Nil(t, err)
	require.Nil(t, h.m.Approve("mq-known"))
	h.m.Online(MqttTransportName, "mq-known", "10.0.100.8:50124")
	h.eventsOf("device.pending")
	recorder.take()

	hourAgo := h.clock.Now().Add(-time.Hour)
	restored := otherDatabase(t, hourAgo, func(m *Manager, virtual *VirtualTransport, _ *fakeClock) {
		approveIn(t, m, virtual, "known", digital("button"))
		require.Nil(t, m.SetName("known", "Restored name"))
		approveIn(t, m, virtual, "newcomer", digital("button"))
		approveIn(t, m, virtual, "banned", digital("button"))
		require.Nil(t, m.Block("banned"))
		approveIn(t, m, virtual, "stored-only", digital("button"))
		_, err := m.Authenticate(MqttTransportName, "mq-known", "token-there")
		require.Nil(t, err)
		require.Nil(t, m.Approve("mq-known"))
	})
	h.restore(restored, "default")
	assert.Equal(
		t, []string{"banned", "known", "mq-known", "mq-stranger", "newcomer", "stored-only", "stranger"}, deviceIds(h.m),
	)
	// The transports were told about every device whose rights are not what it connected with, and no other.
	assert.Equal(t, []string{"away", "banned", "mq-known", "mq-stranger", "newcomer", "stranger"}, recorder.take())

	// A device both know is what the restored database says, and keeps what no database holds.
	info, _ := h.m.Device("known")
	assert.Equal(t, "Restored name", info.Name)
	assert.Equal(t, model.DeviceApproved, info.State)
	assert.True(t, info.Online)
	assert.Equal(t, 1.0, info.Inputs["button"].Value)
	assert.NotNil(t, info.Manifest.Channel("lamp"), "what it says it is now, not what the backup remembers")
	record, _ := restored.GetDeviceByDeviceId("known")
	assert.Contains(t, record.Manifest, "lamp", "and the restored database is told")
	require.Nil(t, known.SetInput("button", 0))

	// A connected device the restored database does not have is pending again, as a new device would be.
	for _, id := range []string{"stranger", "mq-stranger"} {
		info, _ = h.m.Device(id)
		assert.Equal(t, model.DevicePending, info.State, id)
		assert.True(t, info.Online, id)
		record, _ = restored.GetDeviceByDeviceId(id)
		if assert.NotNil(t, record, id) {
			assert.Equal(t, model.DevicePending, record.State, id)
		}
	}
	pending := h.eventsOf("device.pending")
	if assert.Equal(t, 2, len(pending)) {
		assert.Equal(t, "mq-stranger", pending[0]["id"])
		assert.Equal(t, "stranger", pending[1]["id"])
	}
	assert.Equal(t, ErrNotApproved, stranger.SetInput("button", 1))
	require.Nil(t, h.m.Approve("stranger"))
	require.Nil(t, stranger.SetInput("button", 1))
	// The token it connected with is the one approving it pins.
	access, err := h.m.Authenticate(MqttTransportName, "mq-stranger", "secret")
	assert.Nil(t, err)
	assert.Equal(t, AccessPending, access)
	require.Nil(t, h.m.Approve("mq-stranger"))
	access, _ = h.m.Authenticate(MqttTransportName, "mq-stranger", "secret")
	assert.Equal(t, AccessApproved, access)
	_, err = h.m.Authenticate(MqttTransportName, "mq-stranger", "another")
	assert.Equal(t, ErrBadToken, err)

	// A device that is neither connected nor in the restored database is forgotten.
	_, ok := h.m.Device("away")
	assert.False(t, ok)

	// The restored database's word on approval and tokens holds.
	info, _ = h.m.Device("newcomer")
	assert.Equal(t, model.DeviceApproved, info.State)
	require.Nil(t, newcomer.SetInput("button", 1))
	info, _ = h.m.Device("banned")
	assert.Equal(t, model.DeviceBlocked, info.State)
	assert.False(t, banned.Connected(), "its transport dropped it")
	_, err = h.m.Authenticate(MqttTransportName, "mq-known", "token-here")
	assert.Equal(t, ErrBadToken, err)
	access, err = h.m.Authenticate(MqttTransportName, "mq-known", "token-there")
	assert.Nil(t, err)
	assert.Equal(t, AccessApproved, access)

	// A device only the restored database knows is there, offline.
	info, _ = h.m.Device("stored-only")
	assert.Equal(t, model.DeviceApproved, info.State)
	assert.False(t, info.Online)
}

func TestSetDatabaseKeepsCounting(t *testing.T) {
	h := newHarness(t)
	bindings := `
inputs:
  - { id: goal, device: beam, channel: count, action: { score: balls, op: increment, alliance: red } }
  - { id: spare, device: spare, channel: count, action: { score: balls, op: increment, alliance: red } }
  - { id: roaming, device: roaming, channel: count, action: { score: balls, op: increment, alliance: red } }
`
	beam := h.connect("beam", counter("count"))
	spare := h.connect("spare", counter("count"))
	roaming := h.connect("roaming", counter("count"))
	h.bind(bindings)
	require.Nil(t, beam.SetInput("count", 40))
	require.Nil(t, beam.SetInput("count", 50))
	require.Nil(t, spare.SetInput("count", 12))
	require.Nil(t, roaming.SetInput("count", 5))
	spare.Disconnect()
	roaming.Disconnect()
	assert.Equal(t, 1, len(h.scorer.take()))

	// The backup heard beam and spare an hour ago, when their totals were other ones. Roaming went to another field
	// since, where the backup comes from: it was heard there an hour after it was last heard here.
	hourAgo := h.clock.Now().Add(-time.Hour)
	restored := otherDatabase(t, hourAgo, func(m *Manager, virtual *VirtualTransport, clock *fakeClock) {
		theirBeam := approveIn(t, m, virtual, "beam", counter("count"))
		theirSpare := approveIn(t, m, virtual, "spare", counter("count"))
		theirRoaming := approveIn(t, m, virtual, "roaming", counter("count"))
		bindIn(t, m, bindings)
		require.Nil(t, theirBeam.SetInput("count", 30))
		require.Nil(t, theirSpare.SetInput("count", 20))
		theirBeam.Disconnect()
		theirSpare.Disconnect()
		clock.Advance(2 * time.Hour)
		require.Nil(t, theirRoaming.SetInput("count", 9))
	})
	h.restore(restored, "default")

	// A connected counter goes on from the total the manager last heard: 5 more, not the 25 since the backup.
	require.Nil(t, beam.SetInput("count", 55))
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", scoring.OpIncrement, 5.0)
	}
	record, _ := restored.GetDeviceByDeviceId("beam")
	assert.Equal(t, map[string]float64{"count": 55}, record.CounterTotals)

	// An offline one goes on from the total of whoever heard it last: the manager for spare, the backup for roaming.
	spare = h.connect("spare", counter("count"))
	require.Nil(t, spare.SetInput("count", 14))
	if changes = h.scorer.take(); assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", scoring.OpIncrement, 2.0)
	}
	roaming = h.connect("roaming", counter("count"))
	require.Nil(t, roaming.SetInput("count", 10))
	if changes = h.scorer.take(); assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", scoring.OpIncrement, 1.0)
	}
}

func TestSetDatabaseKeepsStopsAndFaults(t *testing.T) {
	const pressedReason = "its safety channel stop read 0: the stop was pressed or its circuit opened"
	channels := []*Channel{safety("stop"), digital("auto"), digital("field")}

	// stopped returns a harness in which a fault holds the E-stop of R1, pressed and released a minute ago, and an
	// input that is still active holds the A-stop of B2.
	stopped := func(t *testing.T) (*harness, *VirtualDevice, time.Time) {
		h := newHarness(t)
		box := stopBox(h)
		h.bind(stopBindings)
		require.Nil(t, box.Heartbeat())
		require.Nil(t, box.SetInput("stop", 0))
		require.Nil(t, box.SetInput("stop", 1))
		require.Nil(t, box.SetInput("auto", 1))
		latched := h.clock.Now()
		h.clock.Advance(time.Minute)
		require.Nil(t, box.Heartbeat())
		h.eventsOf("device.fault")
		return h, box, latched
	}
	held := Stops{Stations: map[string]StationStop{"R1": {EStop: true}, "B2": {AStop: true}}}
	restore := func(t *testing.T, h *harness, fill func(m *Manager, virtual *VirtualTransport)) {
		restored := otherDatabase(
			t, h.clock.Now().Add(-time.Hour), func(m *Manager, virtual *VirtualTransport, _ *fakeClock) { fill(m, virtual) },
		)
		h.restore(restored, "default")
	}

	t.Run("the same stop bindings", func(t *testing.T) {
		h, box, latched := stopped(t)
		restore(t, h, func(m *Manager, virtual *VirtualTransport) {
			approveIn(t, m, virtual, "stopbox", channels...)
			bindIn(t, m, stopBindings)
		})

		assert.Equal(t, held, h.m.Stops())
		assert.Equal(
			t, []Fault{{Device: "stopbox", Binding: "estop-r1", Reason: pressedReason, Time: latched}}, h.m.Faults(),
		)
		assert.Empty(t, h.eventsOf("device.fault"))
		assert.Empty(t, h.eventsOf("device.fault_cleared"))
		assert.Contains(t, h.m.StartConditions(), `stop device stopbox "Test stopbox" (E-stop R1, A-stop B2, field stop) `+
			`has a fault (`+pressedReason+`); clear it on the Devices page once it is safe`)

		// The fault is the operator's to clear, as it was, and the input lets go when it is released.
		assert.EqualError(t, h.m.ClearFaults("stopbox"), `device "stopbox" still reports its stop (auto) as active`)
		require.Nil(t, box.SetInput("auto", 0))
		require.Nil(t, h.m.ClearFaults("stopbox"))
		assert.Equal(t, Stops{Stations: map[string]StationStop{"R1": {}, "B2": {}}}, h.m.Stops())
	})

	t.Run("the stops under other ids", func(t *testing.T) {
		h, box, latched := stopped(t)
		restore(t, h, func(m *Manager, virtual *VirtualTransport) {
			approveIn(t, m, virtual, "stopbox", channels...)
			bindIn(t, m, `
inputs:
  - { id: r1, device: stopbox, channel: stop, action: { field: estop, station: R1 } }
  - { id: b2, device: stopbox, channel: auto, action: { field: astop, station: B2 } }
`)
		})

		// The fault moved to the binding that reads the same channel, with its reason and time; the level of the
		// input was read again from what the device last reported.
		assert.Equal(t, held, h.m.Stops())
		assert.Equal(t, []Fault{{Device: "stopbox", Binding: "r1", Reason: pressedReason, Time: latched}}, h.m.Faults())
		assert.Equal(
			t, []map[string]any{{"id": "stopbox", "binding": "r1", "reason": pressedReason}}, h.eventsOf("device.fault"),
		)
		assert.Equal(t, []map[string]any{
			{"id": "stopbox", "binding": "estop-r1", "reason": "the binding was removed"},
		}, h.eventsOf("device.fault_cleared"))
		require.Nil(t, box.SetInput("auto", 0))
		assert.Equal(t, StationStop{}, h.m.Stops().Stations["B2"])
		assert.True(t, h.m.Stops().Stations["R1"].EStop)
	})

	t.Run("a stop device the restored database does not have", func(t *testing.T) {
		h, box, latched := stopped(t)
		restore(t, h, func(m *Manager, virtual *VirtualTransport) {
			bindIn(t, m, stopBindings)
		})

		// It is pending, so nothing it reports counts any more: every stop bound to it is held by a fault.
		const reason = "the restored database does not have it, so it has to be approved again"
		assert.Equal(t, Stops{
			Stations: map[string]StationStop{"R1": {EStop: true}, "B2": {AStop: true}}, FieldStop: true,
		}, h.m.Stops())
		now := h.clock.Now()
		assert.Equal(t, []Fault{
			{Device: "stopbox", Binding: "estop-r1", Reason: pressedReason, Time: latched},
			{Device: "stopbox", Binding: "astop-b2", Reason: reason, Time: now},
			{Device: "stopbox", Binding: "field", Reason: reason, Time: now},
		}, h.m.Faults())
		assert.Equal(t, 2, len(h.eventsOf("device.fault")))
		assert.Empty(t, h.eventsOf("device.fault_cleared"))
		conditions := h.m.StartConditions()
		assert.Contains(
			t, conditions, `stop device stopbox "Test stopbox" (E-stop R1, A-stop B2, field stop) is waiting for approval`,
		)
		assert.EqualError(t, h.m.ClearFaults("stopbox"), `device "stopbox" is not approved`)
		assert.True(t, box.Connected())

		// Approved again, and reporting safe, its faults can be cleared.
		require.Nil(t, h.m.Approve("stopbox"))
		require.Nil(t, box.Heartbeat())
		require.Nil(t, box.SetInput("auto", 0))
		require.Nil(t, h.m.ClearFaults("stopbox"))
		assert.Equal(t, Stops{Stations: map[string]StationStop{"R1": {}, "B2": {}}}, h.m.Stops())
		assert.Empty(t, h.m.StartConditions())
	})

	t.Run("a stop device the restored database blocks", func(t *testing.T) {
		h, box, _ := stopped(t)
		restore(t, h, func(m *Manager, virtual *VirtualTransport) {
			approveIn(t, m, virtual, "stopbox", channels...)
			bindIn(t, m, stopBindings)
			require.Nil(t, m.Block("stopbox"))
		})

		assert.False(t, box.Connected())
		stops := h.m.Stops()
		assert.True(t, stops.Stations["R1"].EStop)
		assert.True(t, stops.Stations["B2"].AStop)
		assert.True(t, stops.FieldStop)
		faults := h.m.Faults()
		if assert.Equal(t, 3, len(faults)) {
			assert.Equal(t, pressedReason, faults[0].Reason)
			assert.Equal(t, "the restored database has it as blocked", faults[1].Reason)
		}
	})

	t.Run("another game as well", func(t *testing.T) {
		h, _, latched := stopped(t)
		source := strings.Replace(testGameSource, "id: device-test", "id: other-test", 1)
		other, diagnostics := gamedef.Parse([]byte(source))
		require.NotNil(t, other, "%v", diagnostics)
		hourAgo := h.clock.Now().Add(-time.Hour)
		restored := otherDatabase(t, hourAgo, func(m *Manager, virtual *VirtualTransport, _ *fakeClock) {
			approveIn(t, m, virtual, "stopbox", channels...)
			m.SetGame(other, testStations)
			bindIn(t, m, stopBindings)
		})

		// The restored event plays another game, and its database has bindings for that game only. Given the
		// database and the game together, the manager goes straight to them. The old game's set in the new database
		// has no stops: put in force on the way, it would have dropped the fault.
		require.Nil(t, h.db.Close())
		require.Nil(t, h.m.Configure(Setup{Database: restored, Game: other, Stations: testStations}))
		t.Cleanup(h.m.Stop)
		assert.Equal(t, "other-test", h.m.Bindings().Game)
		assert.Equal(t, held, h.m.Stops())
		assert.Equal(
			t, []Fault{{Device: "stopbox", Binding: "estop-r1", Reason: pressedReason, Time: latched}}, h.m.Faults(),
		)
		assert.Empty(t, h.eventsOf("device.fault_cleared"))
	})

	t.Run("no stop bindings", func(t *testing.T) {
		h, _, _ := stopped(t)
		restore(t, h, func(m *Manager, virtual *VirtualTransport) {
			approveIn(t, m, virtual, "stopbox", channels...)
		})

		// With no stop bound, there is nothing for a fault to hold. It does not go without a word.
		assert.Equal(t, Stops{Stations: map[string]StationStop{}}, h.m.Stops())
		assert.Empty(t, h.m.Faults())
		assert.Equal(t, []map[string]any{
			{"id": "stopbox", "binding": "estop-r1", "reason": "the binding was removed"},
		}, h.eventsOf("device.fault_cleared"))
	})
}

// A database that cannot be read is refused, and nothing changes: not the devices, not the profile, and so not the
// bindings in force, whose stops and faults stay whole.
func TestSetDatabaseThatCannotBeRead(t *testing.T) {
	h := newHarness(t)
	box := stopBox(h)
	h.bind(stopBindings)
	require.Nil(t, box.Heartbeat())
	require.Nil(t, box.SetInput("stop", 0))

	closed := model.SetupTestDb(t)
	require.Nil(t, closed.Close())
	err := h.m.Configure(Setup{Database: closed, Profile: "tent", Game: h.game, Stations: testStations})
	assert.ErrorContains(t, err, "failed to load the devices")
	assert.Equal(t, []string{"stopbox"}, deviceIds(h.m))
	assert.Equal(t, "default", h.m.Profile())
	assert.Equal(t, 3, len(h.m.Bindings().Set.Inputs))
	assert.Equal(t, 1, len(h.m.Faults()))
	assert.True(t, h.m.Stops().Stations["R1"].EStop)

	// It still stores where it did.
	require.Nil(t, h.m.SetNotes("stopbox", "Still here"))
	record, _ := h.db.GetDeviceByDeviceId("stopbox")
	assert.Equal(t, "Still here", record.Notes)

	// And the next call, with a database that can be read, goes through.
	readable := model.SetupTestDb(t)
	require.Nil(t, h.m.Configure(Setup{Database: readable, Profile: "tent", Game: h.game, Stations: testStations}))
	t.Cleanup(h.m.Stop)
	assert.Equal(t, "tent", h.m.Profile())
	info, _ := h.m.Device("stopbox")
	assert.Equal(t, model.DevicePending, info.State)
}

// A database is replaced while devices report, the arena's loop runs and a page saves: nothing deadlocks, and the
// race detector finds nothing.
func TestSetDatabaseWhileInUse(t *testing.T) {
	h := newHarness(t)
	bindings := `
inputs:
  - { id: goal, device: beam, channel: count, action: { score: balls, op: increment, alliance: red } }
outputs:
  - { id: lamp, device: beam, channel: lamp, when: "true" }
`
	beam := h.connect("beam", counter("count"), output("lamp", TypeDigital))
	h.bind(bindings)
	other := otherDatabase(t, h.clock.Now(), func(m *Manager, virtual *VirtualTransport, _ *fakeClock) {
		approveIn(t, m, virtual, "beam", counter("count"), output("lamp", TypeDigital))
		bindIn(t, m, strings.Replace(bindings, "id: goal", "id: other-goal", 1))
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	run := func(work func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; ; i++ {
				select {
				case <-stop:
					return
				default:
					work(i)
				}
			}
		}()
	}
	snapshot := h.snapshot("teleop", 1)
	run(func(i int) { _ = beam.SetInput("count", i) })
	run(func(i int) {
		h.m.Tick(h.clock.Now())
		h.m.ApplyScoreChanges()
		h.m.Stops()
		h.m.UpdateOutputs(snapshot)
	})
	run(func(i int) {
		_ = h.m.SetNotes("beam", "note")
		h.m.Devices()
		set := h.m.Bindings().Set
		_, _ = h.m.SaveBindings(set)
	})
	for i := 0; i < 10; i++ {
		require.Nil(t, h.m.SetDatabase(other))
		require.Nil(t, h.m.SetDatabase(h.db))
	}
	close(stop)
	wg.Wait()

	// The device is still there and connected, and what is saved now goes to the database given last.
	info, ok := h.m.Device("beam")
	if assert.True(t, ok) {
		assert.Equal(t, model.DeviceApproved, info.State)
		assert.True(t, info.Online)
	}
	require.Nil(t, h.m.SetNotes("beam", "after it all"))
	record, _ := h.db.GetDeviceByDeviceId("beam")
	assert.Equal(t, "after it all", record.Notes)
	h.bind(bindings)
	stored, _ := h.db.GetBindingSet(h.game.ID, "default")
	assert.Contains(t, stored.Yaml, "id: goal")
}
