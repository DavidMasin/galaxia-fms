package field

import (
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"testing"
	"time"
)

// useCountdown has the arena play the countdown of its game, which the test arena otherwise leaves out.
func useCountdown(arena *Arena, mode string) {
	arena.EventSettings.CountdownMode = mode
}

func TestCountdownPlan(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	now := time.Now()
	lead := func() time.Duration {
		return arena.planCountdown(now).goAt.Sub(now)
	}

	// The game counts three beats after a spoken line.
	useCountdown(arena, model.CountdownGame)
	countdown := arena.planCountdown(now)
	assert.Equal(t, 3, countdown.beats)
	assert.True(t, countdown.intro)
	assert.Equal(t, "drivers_behind_lines", countdown.voice)
	assert.Equal(t, 6*time.Second, lead(), "the notice, the line, and three beats")
	assert.Equal(t, []int64{-3000, -2000, -1000, 0}, countdown.beatOffsetsMs())

	// The quick countdown is the game's quick beats, with nothing said before them.
	useCountdown(arena, model.CountdownQuick)
	countdown = arena.planCountdown(now)
	assert.Equal(t, 1, countdown.beats)
	assert.False(t, countdown.intro)
	assert.Equal(t, "", countdown.voice)
	assert.Equal(t, 1800*time.Millisecond, lead())
	assert.Equal(t, []int64{-1000, 0}, countdown.beatOffsetsMs())

	// An event can do without.
	useCountdown(arena, model.CountdownOff)
	countdown = arena.planCountdown(now)
	assert.Equal(t, 0, countdown.beats)
	assert.Equal(t, now, countdown.goAt)

	// So can a game, whatever the event says, and a game can count without a line.
	useCountdown(arena, model.CountdownGame)
	arena.Game.Match.Countdown.Voice = ""
	countdown = arena.planCountdown(now)
	assert.Equal(t, 3, countdown.beats)
	assert.False(t, countdown.intro)
	assert.Equal(t, 3800*time.Millisecond, lead())
	arena.Game.Match.Countdown.Enabled = false
	assert.Equal(t, time.Duration(0), lead())
	useCountdown(arena, model.CountdownQuick)
	assert.Equal(t, time.Duration(0), lead())
}

func TestCountdownRunsToTheStart(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	useCountdown(arena, model.CountdownGame)
	bypassAllStations(arena)
	arena.AudienceDisplayMode = "intro"
	arena.FieldReset = true
	events := arena.Bus.Subscribe([]string{"match"}, 100)
	defer events.Close()

	before := time.Now()
	require.Nil(t, arena.StartMatch())
	assert.Equal(t, Countdown, arena.MatchState)
	goAt := arena.countdown.goAt
	assert.WithinDuration(t, before.Add(6*time.Second), goAt, 500*time.Millisecond)

	// Everyone is told the moment of the start at once.
	assert.Equal(
		t,
		CountdownMessage{
			State: "armed", GoAt: goAt.UnixMilli(), Beats: 3, Intro: true, Voice: "drivers_behind_lines", Sound: true,
		},
		arena.generateCountdownMessage(),
	)
	arena.MuteMatchSounds = true
	assert.False(t, arena.generateCountdownMessage().(CountdownMessage).Sound)
	arena.MuteMatchSounds = false
	assert.Equal(t, &CountdownInfo{GoAt: goAt, Beats: 3}, arena.CountdownInfo())
	received := drainEvents(events)
	if assert.Equal(t, []string{"match.countdown"}, eventTypes(received)) {
		assert.Equal(
			t,
			map[string]any{
				"t0": goAt.UnixMilli(), "beats": []int64{-3000, -2000, -1000, 0}, "intro": true,
				"voice": "drivers_behind_lines",
			},
			received[0].Data,
		)
		assert.Equal(t, "T", received[0].Match)
	}
	assert.Equal(t, []string{device.EventCountdownStart}, arena.takeDeviceEvents())
	snapshot := arena.deviceSnapshot()
	assert.Equal(t, "countdown", snapshot.Clock.State)
	assert.Equal(t, goAt, snapshot.CountdownGoAt)
	assert.Equal(t, 3, snapshot.CountdownBeats)

	// The field is the match's from now on, and the robots wait, disabled, in the mode of the first phase.
	assert.Equal(t, "match", arena.AudienceDisplayMode)
	assert.False(t, arena.FieldReset)
	assert.Equal(t, goAt, arena.CurrentMatch.StartedAt)
	auto, enabled := arena.robotMode()
	assert.True(t, auto)
	assert.False(t, enabled)
	assert.Equal(t, MatchTimeMessage{MatchState: Countdown, PhaseIndex: -1}, arena.generateMatchTimeMessage())
	assert.Contains(t, arena.checkCanStartMatch().Error(), "a match is still in progress")
	assert.NotNil(t, arena.LoadTestMatch(), "the match on the field cannot be changed any more")

	// The beats come due one by one.
	arena.Update()
	assert.Equal(t, Countdown, arena.MatchState)
	assert.Empty(t, arena.takeDeviceEvents())
	assert.False(t, arena.advanceCountdown(goAt.Add(-2500*time.Millisecond)))
	assert.Equal(t, []string{device.EventCountdownBeat}, arena.takeDeviceEvents())
	assert.False(t, arena.advanceCountdown(goAt.Add(-2100*time.Millisecond)))
	assert.Empty(t, arena.takeDeviceEvents())
	assert.False(t, arena.advanceCountdown(goAt.Add(-time.Millisecond)), "a moment before the start is not the start")
	assert.Equal(t, []string{device.EventCountdownBeat, device.EventCountdownBeat}, arena.takeDeviceEvents())
	assert.Empty(t, drainEvents(events), "the bus is told once, at the beginning")

	// At its moment the match starts, on the clock everyone counted down to.
	goAt = time.Now().Add(-2 * time.Millisecond)
	arena.countdown.goAt = goAt
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, 0, arena.PhaseIndex)
	assert.Equal(t, goAt, arena.MatchStartTime)
	_, enabled = arena.robotMode()
	assert.True(t, enabled)
	received = drainEvents(events)
	if assert.Equal(t, []string{"match.started", "match.cue", "match.phase_changed"}, eventTypes(received)) {
		started := received[0].Data.(map[string]any)
		assert.Equal(t, goAt.UTC(), started["time"])
		assert.GreaterOrEqual(t, started["lateMs"], 2.0)
		assert.Less(t, started["lateMs"], 1000.0)
	}
	assert.Equal(
		t,
		[]string{device.EventCountdownGo, device.EventMatchStart, "phase.auto.start"},
		arena.takeDeviceEvents(),
	)
	assert.Equal(t, CountdownMessage{State: "idle"}, arena.generateCountdownMessage())
	assert.Nil(t, arena.CountdownInfo())
	assert.Equal(t, "running", arena.clock().State)
}

func TestCountdownIsCalledOff(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	useCountdown(arena, model.CountdownGame)
	bypassAllStations(arena)
	events := arena.Bus.Subscribe([]string{"match"}, 100)
	defer events.Close()
	cancelled := func(reason string) {
		t.Helper()
		assert.Equal(t, PreMatch, arena.MatchState)
		received := drainEvents(events)
		if assert.Equal(t, []string{"match.countdown", "match.countdown_cancelled"}, eventTypes(received)) {
			assert.Equal(t, map[string]any{"reason": reason}, received[1].Data)
			assert.Equal(t, "T", received[1].Match, "the event names the match whose start was called off")
		}
		assert.Equal(t, CountdownMessage{State: "idle"}, arena.generateCountdownMessage())
		assert.Equal(t, "idle", arena.clock().State)
	}

	// Aborting during the countdown puts the arena back to before the match, with nothing to commit.
	startedBefore := time.Now().Add(-time.Hour).Truncate(time.Second)
	arena.CurrentMatch.StartedAt = startedBefore
	require.Nil(t, arena.StartMatch())
	assert.NotEqual(t, startedBefore, arena.CurrentMatch.StartedAt)
	require.Nil(t, arena.AbortMatch())
	assert.Equal(t, []string{device.EventCountdownStart, device.EventCountdownCancel}, arena.takeDeviceEvents())
	cancelled("the start was aborted")
	assert.Equal(t, startedBefore, arena.CurrentMatch.StartedAt, "the match never started")
	assert.NotNil(t, arena.AbortMatch(), "there is nothing left to abort")
	arena.Update()
	assert.Equal(t, PreMatch, arena.MatchState)

	// A team's emergency stop calls it off too.
	require.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, Countdown, arena.MatchState)
	arena.AllianceStations["R2"].EStop = true
	arena.AllianceStations["B1"].EStop = true
	arena.Update()
	cancelled("an emergency stop is active (B1, R2)")
	assert.Contains(t, arena.StartMatch().Error(), "an emergency stop is active")
	arena.AllianceStations["R2"].EStop = false
	arena.AllianceStations["B1"].EStop = false

	// So does the field's, whether the arena loop or an abort gets to it first.
	plc := &FakePlc{isEnabled: true, ftaReady: true}
	arena.Plc = plc
	arena.Update()
	require.Nil(t, arena.StartMatch())
	plc.fieldEStop = true
	arena.Update()
	cancelled("the field emergency stop is active")
	plc.fieldEStop = false
	arena.Update()
	require.Nil(t, arena.StartMatch())
	plc.fieldEStop = true
	require.Nil(t, arena.AbortMatch())
	cancelled("the field emergency stop is active")
}

func TestMatchWithoutCountdownStartsOnTheNextPass(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(t, model.CountdownOff, arena.EventSettings.CountdownMode, "the test arena does without")
	bypassAllStations(arena)
	events := arena.Bus.Subscribe([]string{"match"}, 100)
	defer events.Close()

	require.Nil(t, arena.StartMatch())
	assert.Equal(t, Countdown, arena.MatchState)
	assert.Equal(t, CountdownMessage{State: "idle"}, arena.generateCountdownMessage())
	assert.Nil(t, arena.CountdownInfo())
	assert.Equal(t, "idle", arena.clock().State, "nothing is counted, so nothing is told of a countdown")
	assert.Empty(t, drainEvents(events))
	assert.Empty(t, arena.takeDeviceEvents())

	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.WithinDuration(t, time.Now(), arena.MatchStartTime, 500*time.Millisecond)
	assert.Equal(t, []string{device.EventMatchStart, "phase.auto.start"}, arena.takeDeviceEvents())
	assert.Equal(t, []string{"match.started", "match.cue", "match.phase_changed"}, eventTypes(drainEvents(events)))
}

// The arena loop sleeps between passes. It must wake for the start of the match rather than up to a period after
// it: the robots are enabled in that pass.
func TestArenaLoopWakesForTheStart(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	useCountdown(arena, model.CountdownQuick)
	bypassAllStations(arena)
	period := arenaLoopPeriodMs * time.Millisecond
	assert.Equal(t, period, arena.passDelay())

	require.Nil(t, arena.StartMatch())
	assert.Equal(t, period, arena.passDelay(), "the start is far off")
	arena.countdown.goAt = time.Now().Add(4 * time.Millisecond)
	delay := arena.passDelay()
	assert.Greater(t, delay, time.Duration(0))
	assert.LessOrEqual(t, delay, 4*time.Millisecond)
	arena.countdown.goAt = time.Now().Add(-time.Second)
	assert.Equal(t, time.Duration(0), arena.passDelay(), "a start that is overdue is not waited for")

	// Run the loop as Run does, through a short countdown, and see when the match starts.
	goAt := time.Now().Add(120 * time.Millisecond)
	arena.countdown.goAt = goAt
	for arena.MatchState == Countdown {
		arena.Update()
		time.Sleep(arena.passDelay())
	}
	late := arena.matchStartedLate
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, goAt, arena.MatchStartTime)
	assert.GreaterOrEqual(t, late, time.Duration(0), "never before its moment")
	// A loaded test machine can be slow to wake; a field computer is not. The number is logged for the curious.
	assert.Less(t, late, 100*time.Millisecond)
	t.Logf("the match started %.2f ms after its moment", float64(late.Microseconds())/1000)
	assert.Equal(t, period, arena.passDelay())
}

// A driver station hears "enabled" in the pass of the arena loop that starts the match: never before the moment
// that was announced, and at once after it.
func TestRobotsAreEnabledAtTheStart(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	useCountdown(arena, model.CountdownQuick)
	bypassAllStations(arena)

	// A driver station of our own: a UDP socket that notes when each control packet arrives and what it says.
	driverStation, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.Nil(t, err)
	defer driverStation.Close()
	arena.DriverStationUdpSocket, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.Nil(t, err)
	defer arena.DriverStationUdpSocket.Close()
	type heard struct {
		at            time.Time
		auto, enabled bool
	}
	packets := make(chan heard, 100)
	go func() {
		buffer := make([]byte, 100)
		for {
			length, _, err := driverStation.ReadFromUDP(buffer)
			if err != nil {
				close(packets)
				return
			}
			if length >= 4 {
				packets <- heard{at: time.Now(), auto: buffer[3]&0x02 != 0, enabled: buffer[3]&0x04 != 0}
			}
		}
	}()

	tcpConn := setupFakeTcpConnection(t)
	defer tcpConn.Close()
	port := driverStation.LocalAddr().(*net.UDPAddr).Port
	dsConn, err := newDriverStationConnection(254, "R1", tcpConn, port, false)
	require.Nil(t, err)
	dsConn.RobotLinked = true
	station := arena.AllianceStations["R1"]
	station.DsConn, station.Bypass, station.Team = dsConn, false, &model.Team{Id: 254}

	require.Nil(t, arena.StartMatch())
	goAt := time.Now().Add(150 * time.Millisecond)
	arena.countdown.goAt = goAt
	for arena.MatchState == Countdown {
		arena.Update()
		time.Sleep(arena.passDelay())
	}
	require.Equal(t, InMatch, arena.MatchState)

	// Before the start the robot is held disabled in autonomous; the first packet after it enables the robot.
	var before int
	var enabledAt time.Time
	timeout := time.After(2 * time.Second)
	for enabledAt.IsZero() {
		select {
		case packet := <-packets:
			assert.True(t, packet.auto)
			if packet.enabled {
				enabledAt = packet.at
			} else {
				before++
				assert.True(t, packet.at.Before(goAt.Add(time.Millisecond)), "a disabled packet after the start")
			}
		case <-timeout:
			require.Fail(t, "the driver station was never told to enable")
		}
	}
	assert.GreaterOrEqual(t, before, 1, "the countdown is spent disabled")
	late := enabledAt.Sub(goAt)
	assert.GreaterOrEqual(t, late, time.Duration(0), "never before the moment that was announced")
	// A loaded test machine can be slow to wake; a field computer is not. The number is logged for the curious.
	assert.Less(t, late, 100*time.Millisecond)
	t.Logf("the driver station heard \"enabled\" %.2f ms after the start", float64(late.Microseconds())/1000)
}
