package field

import (
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// The phases of the rebuilt-style example game and when each begins: auto 0, pause 20, transition 23, shift1 33,
// shift2 58, shift3 83, shift4 108, endgame 133; the match ends at 163.

func TestArenaMatchFlow(t *testing.T) {
	arena := setupTestArena(t)

	arena.Database.CreateTeam(&model.Team{Id: 254})
	assert.Nil(t, arena.assignTeam(254, "B3"))
	dummyDs := &DriverStationConnection{TeamId: 254}
	arena.AllianceStations["B3"].DsConn = dummyDs
	arena.Database.CreateTeam(&model.Team{Id: 1678})
	assert.Nil(t, arena.assignTeam(254, "R2"))
	dummyDs = &DriverStationConnection{TeamId: 1678}
	arena.AllianceStations["R2"].DsConn = dummyDs

	// Check pre-match state and packet timing. The robots are held in the mode of the first phase.
	assert.Equal(t, PreMatch, arena.MatchState)
	assert.Equal(t, -1, arena.PhaseIndex)
	assert.Nil(t, arena.CurrentPhase())
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-300 * time.Millisecond)
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	lastPacketCount := arena.AllianceStations["B3"].DsConn.packetCount
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-10 * time.Millisecond)
	arena.Update()
	assert.Equal(t, lastPacketCount, arena.AllianceStations["B3"].DsConn.packetCount)
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, lastPacketCount+1, arena.AllianceStations["B3"].DsConn.packetCount)

	// Check match start, autonomous and transition to teleop.
	arena.AllianceStations["R1"].Bypass = true
	arena.AllianceStations["R2"].DsConn.RobotLinked = true
	arena.AllianceStations["R3"].Bypass = true
	arena.AllianceStations["B1"].Bypass = true
	arena.AllianceStations["B2"].Bypass = true
	arena.AllianceStations["B3"].DsConn.RobotLinked = true
	assert.Nil(t, arena.StartMatch())
	assert.Equal(t, StartMatch, arena.MatchState)
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, "auto", arena.CurrentPhase().ID)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.Update()
	assert.Equal(t, "auto", arena.CurrentPhase().ID)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)
	runMatchTo(arena, 19.5)
	assert.Equal(t, "auto", arena.CurrentPhase().ID)
	assert.InDelta(t, 19.5, arena.PhaseTimeSec(), 0.2)
	runMatchTo(arena, 20.05)
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	assert.Equal(t, 1, arena.PhaseIndex)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.Update()
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	runMatchTo(arena, 23.05)
	assert.Equal(t, "transition", arena.CurrentPhase().ID)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.Update()
	assert.Equal(t, "transition", arena.CurrentPhase().ID)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)
	assert.InDelta(t, 23.05, arena.MatchTimeSec(), 0.2)
	assert.InDelta(t, 0.05, arena.PhaseTimeSec(), 0.2)

	// Check E-stop and bypass.
	arena.AllianceStations["B3"].EStop = true
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.AllianceStations["B3"].Bypass = true
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.AllianceStations["B3"].EStop = false
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.AllianceStations["B3"].Bypass = false
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)

	// The robots stay enabled through every teleop phase.
	runMatchTo(arena, 100)
	assert.Equal(t, "shift3", arena.CurrentPhase().ID)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Enabled)

	// Check match end.
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.Equal(t, -1, arena.PhaseIndex)
	assert.Nil(t, arena.CurrentPhase())
	assert.Equal(t, 0.0, arena.MatchTimeSec())
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	arena.Update()
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)

	arena.AllianceStations["R1"].Bypass = true
	arena.ResetMatch()
	arena.lastDsPacketTime = arena.lastDsPacketTime.Add(-550 * time.Millisecond)
	arena.Update()
	assert.Equal(t, PreMatch, arena.MatchState)
	assert.Equal(t, true, arena.AllianceStations["B3"].DsConn.Auto)
	assert.Equal(t, false, arena.AllianceStations["B3"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R1"].Bypass)
}

func TestArenaStateEnforcement(t *testing.T) {
	arena := setupTestArena(t)
	bypassAllStations(arena)

	err := arena.LoadMatch(new(model.Match))
	assert.Nil(t, err)
	err = arena.AbortMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "cannot abort match when")
	}
	err = arena.StartMatch()
	assert.Nil(t, err)
	for _, state := range []MatchState{StartMatch, InMatch, PostMatch} {
		arena.MatchState = state
		err = arena.LoadMatch(new(model.Match))
		if assert.NotNil(t, err) {
			assert.Contains(t, err.Error(), "cannot load match while")
		}
		err = arena.StartMatch()
		if assert.NotNil(t, err) {
			assert.Contains(t, err.Error(), "cannot start match: a match is still in progress")
		}
		if state != PostMatch {
			err = arena.ResetMatch()
			if assert.NotNil(t, err) {
				assert.Contains(t, err.Error(), "cannot reset match while")
			}
		}
	}
	err = arena.AbortMatch()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "cannot abort match when")
	}

	err = arena.ResetMatch()
	assert.Nil(t, err)
	assert.Equal(t, PreMatch, arena.MatchState)
	err = arena.ResetMatch()
	assert.Nil(t, err)
	err = arena.LoadMatch(new(model.Match))
	assert.Nil(t, err)
}

func TestArenaTimeout(t *testing.T) {
	arena := setupTestArena(t)

	// Test regular ending of timeout.
	timeoutDurationSec := 9
	assert.Nil(t, arena.StartTimeout("Break 1", timeoutDurationSec))
	assert.Equal(t, timeoutDurationSec, arena.TimeoutDurationSec)
	assert.Equal(t, TimeoutActive, arena.MatchState)
	assert.Equal(t, "Break 1", arena.breakDescription)
	assert.Equal(t, "Test Match", arena.breakNextMatchName)
	setMatchTime(arena, 4.5)
	message := arena.generateMatchTimeMessage().(MatchTimeMessage)
	assert.Equal(
		t,
		MatchTimeMessage{MatchState: TimeoutActive, MatchTimeSec: 4, PhaseIndex: -1, TimeoutDurationSec: 9},
		message,
	)
	runMatchTo(arena, float64(timeoutDurationSec))
	assert.Equal(t, PostTimeout, arena.MatchState)
	runMatchTo(arena, float64(timeoutDurationSec+postTimeoutSec))
	assert.Equal(t, PreMatch, arena.MatchState)

	// Test ad-hoc timeout display text.
	timeoutDurationSec = 14
	assert.Nil(t, arena.StartAdHocTimeout("Repair Break", "", timeoutDurationSec))
	assert.Equal(t, "Repair Break", arena.breakDescription)
	assert.Equal(t, "", arena.breakNextMatchName)
	arena.SetTimeoutDisplay("Inspection Break", "Practice 1")
	assert.Equal(t, "Inspection Break", arena.breakDescription)
	assert.Equal(t, "Practice 1", arena.breakNextMatchName)
	runMatchTo(arena, float64(timeoutDurationSec))
	assert.Equal(t, PostTimeout, arena.MatchState)
	runMatchTo(arena, float64(timeoutDurationSec+postTimeoutSec))
	assert.Equal(t, PreMatch, arena.MatchState)

	// Test early cancellation of timeout.
	timeoutDurationSec = 28
	assert.Nil(t, arena.StartTimeout("Break 2", timeoutDurationSec))
	assert.Equal(t, "Break 2", arena.breakDescription)
	assert.Equal(t, TimeoutActive, arena.MatchState)
	assert.Equal(t, timeoutDurationSec, arena.TimeoutDurationSec)
	assert.Nil(t, arena.AbortMatch())
	arena.Update()
	assert.Equal(t, PostTimeout, arena.MatchState)
	runMatchTo(arena, float64(timeoutDurationSec+postTimeoutSec))
	assert.Equal(t, PreMatch, arena.MatchState)

	// Test that timeout can't be started during a match.
	bypassAllStations(arena)
	assert.Nil(t, arena.StartMatch())
	arena.Update()
	assert.NotNil(t, arena.StartTimeout("Timeout", 1))
	assert.NotEqual(t, TimeoutActive, arena.MatchState)
	assert.Equal(t, timeoutDurationSec, arena.TimeoutDurationSec)
	runMatchTo(arena, 100)
	assert.Equal(t, InMatch, arena.MatchState)
	assert.NotNil(t, arena.StartTimeout("Timeout", 1))
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.NotNil(t, arena.StartTimeout("Timeout", 1))

	// Test that a match can be loaded during a timeout.
	assert.Nil(t, arena.ResetMatch())
	assert.Nil(t, arena.LoadTestMatch())
	assert.Nil(t, arena.StartTimeout("Break 2", 10))
	assert.Equal(t, TimeoutActive, arena.MatchState)
	match := model.Match{
		Type: model.Playoff, ShortName: "F1", Red1: 1, Red2: 2, Red3: 3, Blue1: 4, Blue2: 5, Blue3: 6,
	}
	assert.Nil(t, arena.Database.CreateMatch(&match))
	assert.Nil(t, arena.LoadMatch(&match))
	assert.Equal(t, TimeoutActive, arena.MatchState)
	assert.Equal(t, match, *arena.CurrentMatch)
}

func TestMatchPhasesCuesAndEvents(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"match"}, 100)
	defer events.Close()

	phaseChanged := func(id string, index int, robots string, duration float64) map[string]any {
		return map[string]any{"phase": id, "index": index, "robots": robots, "duration": duration}
	}
	cue := func(id, sound, phase string) map[string]any {
		return map[string]any{"cue": id, "sound": sound, "phase": phase}
	}
	// expect checks the events since the last call, apart from the first few whose data is checked by the caller.
	expect := func(skip int, expected ...map[string]any) []bus.Event {
		received := drainEvents(events)
		if assert.Equal(t, skip+len(expected), len(received), "%v", eventTypes(received)) {
			for i, data := range expected {
				assert.Equal(t, data, received[skip+i].Data, received[skip+i].Type)
			}
		}
		return received
	}

	// Starting the match enters the first phase: its opening cue fires, then the phase is announced.
	startTestMatch(t, arena)
	received := expect(1, cue("", "match_start", "auto"), phaseChanged("auto", 0, "auto", 20))
	assert.Equal(t, []string{"match.started", "match.cue", "match.phase_changed"}, eventTypes(received))
	assert.Equal(t, map[string]any{"time": arena.MatchStartTime.UTC()}, received[0].Data)
	assert.Equal(t, "T", received[0].Match, "events carry the match they belong to")
	assert.Equal(
		t,
		MatchTimeMessage{MatchState: InMatch, PhaseIndex: 0, PhaseId: "auto", PhaseRemainingSec: 20},
		arena.generateMatchTimeMessage(),
	)

	// Nothing happens inside a phase that has no cues of its own.
	runMatchTo(arena, 9.5)
	expect(0)
	assert.Equal(
		t,
		MatchTimeMessage{
			MatchState: InMatch, MatchTimeSec: 9, PhaseIndex: 0, PhaseId: "auto", PhaseTimeSec: 9, PhaseRemainingSec: 11,
		},
		arena.generateMatchTimeMessage(),
	)
	runMatchTo(arena, 19.9)
	expect(0)

	// Leaving a phase fires its closing cue; a phase without cues is entered silently.
	runMatchTo(arena, 20.05)
	expect(0, cue("", "phase_end", "auto"), phaseChanged("pause", 1, "disabled", 3))
	runMatchTo(arena, 23.05)
	expect(0, cue("", "resume", "transition"), phaseChanged("transition", 2, "teleop", 10))
	message := arena.generateMatchTimeMessage().(MatchTimeMessage)
	assert.Equal(t, 23, message.MatchTimeSec)
	assert.Equal(t, "transition", message.PhaseId)
	assert.Equal(t, 0, message.PhaseTimeSec)
	assert.Equal(t, 10, message.PhaseRemainingSec)

	for i, start := range []float64{33, 58, 83, 108} {
		runMatchTo(arena, start-0.5)
		expect(0)
		runMatchTo(arena, start+0.05)
		id := []string{"shift1", "shift2", "shift3", "shift4"}[i]
		expect(0, cue("", "shift_change", id), phaseChanged(id, 3+i, "teleop", 25))
		arena.Update()
		expect(0)
	}

	// A cue with an id is announced under it, so that devices and Companion can react.
	runMatchTo(arena, 133.05)
	expect(0, cue("endgame", "warning", "endgame"), phaseChanged("endgame", 7, "teleop", 30))
	runMatchTo(arena, 162.9)
	expect(0)

	// The last phase ending ends the match.
	runMatchTo(arena, 163.05)
	received = expect(0, cue("", "match_end", "endgame"), map[string]any{"matchTime": 163.0})
	assert.Equal(t, []string{"match.cue", "match.ended"}, eventTypes(received))
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.False(t, arena.matchAborted)
	assert.Equal(
		t, MatchTimeMessage{MatchState: PostMatch, PhaseIndex: -1}, arena.generateMatchTimeMessage(),
	)
	arena.Update()
	expect(0)
}

func TestMatchCatchesUpThroughPhases(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	apply(t, arena, count("blue", "fuel", testDevice))
	events := arena.Bus.Subscribe([]string{"match"}, 100)
	defer events.Close()

	// If the arena loop stalls across several phase boundaries, every cue and trigger still happens, in order.
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	var phases, sounds []string
	for _, event := range drainEvents(events) {
		data, _ := event.Data.(map[string]any)
		switch event.Type {
		case "match.phase_changed":
			phases = append(phases, data["phase"].(string))
		case "match.cue":
			sounds = append(sounds, data["sound"].(string))
		}
	}
	assert.Equal(t, []string{"pause", "transition", "shift1", "shift2", "shift3", "shift4", "endgame"}, phases)
	assert.Equal(
		t,
		[]string{
			"phase_end", "resume", "shift_change", "shift_change", "shift_change", "shift_change", "warning", "match_end",
		},
		sounds,
	)
	assert.Equal(t, "blue", arena.Score.State.Field["auto_winner"].Option)
}

func TestMidPhaseCueFiresOnce(t *testing.T) {
	arena := setupTestArena(t)
	selectGame(t, arena, "ball-count", nil)
	startTestMatch(t, arena)
	runMatchTo(arena, 17.05)
	assert.Equal(t, "teleop", arena.CurrentPhase().ID)
	events := arena.Bus.Subscribe([]string{"match.cue"}, 10)
	defer events.Close()

	// The warning comes twenty seconds before the end of a 120 second teleop that began at 17 seconds.
	runMatchTo(arena, 116.5)
	assert.Empty(t, drainEvents(events))
	runMatchTo(arena, 117.05)
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, map[string]any{"cue": "endgame", "sound": "warning", "phase": "teleop"}, received[0].Data)
	}
	arena.Update()
	runMatchTo(arena, 125)
	assert.Empty(t, drainEvents(events))
	assert.Equal(t, "teleop", arena.CurrentPhase().ID)
}

func TestSoundNames(t *testing.T) {
	arena := setupTestArena(t)

	// A cue's sound is the name of an audio file, apart from three aliases that read better in a game file.
	assert.Equal(t, "start", soundFileName("match_start"))
	assert.Equal(t, "end", soundFileName("phase_end"))
	assert.Equal(t, "end", soundFileName("match_end"))
	assert.Equal(t, "resume", soundFileName("resume"))
	assert.Equal(t, "my_own_sound", soundFileName("my_own_sound"))

	assert.Equal(
		t,
		[]string{
			"start", "end", "resume", "shift_change", "warning", "abort", "match_result", "pick_clock",
			"pick_clock_expired", "field_reset",
		},
		arena.MatchSounds(),
	)
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(
		t,
		[]string{
			"start", "end", "resume", "warning", "abort", "match_result", "pick_clock", "pick_clock_expired",
			"field_reset",
		},
		arena.MatchSounds(),
	)
}

func TestAbortMatch(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	runMatchTo(arena, 40)
	assert.Equal(t, "shift1", arena.CurrentPhase().ID)
	events := arena.Bus.Subscribe([]string{"match"}, 10)
	defer events.Close()

	assert.Nil(t, arena.AbortMatch())
	assert.Equal(t, PostMatch, arena.MatchState)
	assert.True(t, arena.matchAborted)
	assert.Equal(t, -1, arena.PhaseIndex)
	assert.Equal(t, 3, arena.endPhaseIndex)
	received := drainEvents(events)
	if assert.Equal(t, []string{"match.aborted"}, eventTypes(received)) {
		assert.InDelta(t, 40, received[0].Data.(map[string]any)["matchTime"], 0.2)
	}

	// No cue of the abandoned phases fires afterwards.
	arena.Update()
	runMatchTo(arena, 200)
	assert.Empty(t, drainEvents(events))
	assert.Equal(t, PostMatch, arena.MatchState)
}

func TestAutoWinnerDecidedAtTeleopStart(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		redFuel          int
		blueFuel         int
		expectedWinners  []string
		expectedGameData map[string]string
	}{
		{"red scores more", 3, 1, []string{"red"}, map[string]string{"red": "R"}},
		{"blue scores more", 0, 2, []string{"blue"}, map[string]string{"blue": "B"}},
		{"level, so a coin is flipped", 2, 2, []string{"red", "blue"}, map[string]string{"red": "R", "blue": "B"}},
	} {
		t.Run(
			testCase.name, func(t *testing.T) {
				arena := setupTestArena(t)
				arena.Database.CreateTeam(&model.Team{Id: 254})
				assert.Nil(t, arena.assignTeam(254, "R1"))
				startTestMatch(t, arena)
				for i := 0; i < testCase.redFuel; i++ {
					apply(t, arena, count("red", "fuel", testDevice))
				}
				for i := 0; i < testCase.blueFuel; i++ {
					apply(t, arena, count("blue", "fuel", testDevice))
				}

				// Until teleop begins nobody has won auto, and the robots are told nothing.
				runMatchTo(arena, 22.9)
				assert.Equal(t, "pause", arena.CurrentPhase().ID)
				assert.Equal(t, "none", arena.Score.State.Field["auto_winner"].Option)
				arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
				arena.Update()
				for _, allianceStation := range arena.AllianceStations {
					assert.Equal(t, "", allianceStation.GameData)
				}
				changesBefore := len(arena.Score.Changes)

				// The game's trigger decides it the moment the first teleop phase starts.
				runMatchTo(arena, 23.05)
				assert.Equal(t, "transition", arena.CurrentPhase().ID)
				winner := arena.Score.State.Field["auto_winner"].Option
				assert.Contains(t, testCase.expectedWinners, winner)
				if assert.Equal(t, changesBefore+1, len(arena.Score.Changes)) {
					change := arena.Score.Changes[changesBefore]
					assert.Equal(t, "auto_winner", change.Element)
					assert.Equal(t, winner, change.Value)
					assert.Equal(t, scoring.SourceRule, change.Source.Kind)
					assert.Equal(t, "transition", change.Phase)
					assert.Equal(t, "", change.Bucket)
				}

				// Every driver station is sent the answer as game data, in the same pass of the arena loop.
				for stationId, allianceStation := range arena.AllianceStations {
					assert.Equal(t, testCase.expectedGameData[winner], allianceStation.GameData, stationId)
				}

				// It is decided once: later phases do not decide it again.
				runMatchTo(arena, 60)
				assert.Equal(t, changesBefore+1, len(arena.Score.Changes))
				assert.Equal(t, winner, arena.Score.State.Field["auto_winner"].Option)

				// A scorer can overrule it, and the robots are told the new answer.
				other := map[string]string{"red": "blue", "blue": "red"}[winner]
				apply(
					t, arena,
					scoring.Change{Element: "auto_winner", Op: scoring.OpSet, Value: other, Source: testScorer},
				)
				arena.lastDsPacketTime = time.Unix(0, 0)
				arena.Update()
				assert.Equal(t, map[string]string{"red": "R", "blue": "B"}[other], arena.AllianceStations["R1"].GameData)
			},
		)
	}
}

func TestStationGameData(t *testing.T) {
	arena := setupTestArena(t)

	// A game without a game data expression sends nothing.
	selectGame(t, arena, "ball-count", nil)
	startTestMatch(t, arena)
	arena.lastDsPacketTime = time.Unix(0, 0)
	arena.Update()
	for _, allianceStation := range arena.AllianceStations {
		assert.Equal(t, "", allianceStation.GameData)
	}
	assert.Equal(t, "", arena.stationGameData("R1"))
	assert.Equal(t, "", arena.stationGameData("X9"))
}

func TestRobotModeAndCountdowns(t *testing.T) {
	arena := setupTestArena(t)

	// Before the match the field counts down the first stretch of the match and driver stations show its length.
	auto, enabled := arena.robotMode()
	assert.True(t, auto)
	assert.False(t, enabled)
	assert.Equal(t, 20.0, arena.modeRemainingSec())
	assert.Equal(t, 20, arena.dsSecondsRemaining())

	startTestMatch(t, arena)
	runMatchTo(arena, 5)
	auto, enabled = arena.robotMode()
	assert.True(t, auto)
	assert.True(t, enabled)
	assert.True(t, arena.inAutoPhase())
	assert.InDelta(t, 15, arena.modeRemainingSec(), 0.2)
	assert.Equal(t, 15, arena.dsSecondsRemaining())

	// A pause shows zero on the field timers, while driver stations see the pause run out.
	runMatchTo(arena, 21)
	auto, enabled = arena.robotMode()
	assert.False(t, auto)
	assert.False(t, enabled)
	assert.False(t, arena.inAutoPhase())
	assert.Equal(t, 0.0, arena.modeRemainingSec())
	assert.Equal(t, 2, arena.dsSecondsRemaining())

	// Teleop is counted down as a whole on the field, whatever phases the game splits it into.
	runMatchTo(arena, 63)
	assert.Equal(t, "shift2", arena.CurrentPhase().ID)
	auto, enabled = arena.robotMode()
	assert.False(t, auto)
	assert.True(t, enabled)
	assert.InDelta(t, 100, arena.modeRemainingSec(), 0.2)
	assert.Equal(t, 20, arena.dsSecondsRemaining())

	runMatchTo(arena, 163.05)
	auto, enabled = arena.robotMode()
	assert.False(t, auto)
	assert.False(t, enabled)
	assert.Equal(t, 0.0, arena.modeRemainingSec())
	assert.Equal(t, 0, arena.dsSecondsRemaining())
}

func TestPlcEStopAStop(t *testing.T) {
	arena := setupTestArena(t)
	var plc FakePlc
	plc.isEnabled = true
	plc.ftaReady = true
	arena.Plc = &plc

	arena.Database.CreateTeam(&model.Team{Id: 254})
	err := arena.assignTeam(254, "R1")
	assert.Nil(t, err)
	dummyDs := &DriverStationConnection{TeamId: 254}
	arena.AllianceStations["R1"].DsConn = dummyDs
	arena.Database.CreateTeam(&model.Team{Id: 148})
	err = arena.assignTeam(148, "R2")
	assert.Nil(t, err)
	dummyDs = &DriverStationConnection{TeamId: 148}
	arena.AllianceStations["R2"].DsConn = dummyDs

	arena.AllianceStations["R1"].DsConn.RobotLinked = true
	arena.AllianceStations["R1"].aStopReset = true
	arena.AllianceStations["R2"].DsConn.RobotLinked = true
	arena.AllianceStations["R2"].aStopReset = true
	arena.AllianceStations["R3"].Bypass = true
	arena.AllianceStations["R3"].aStopReset = true
	arena.AllianceStations["B1"].Bypass = true
	arena.AllianceStations["B1"].aStopReset = true
	arena.AllianceStations["B2"].Bypass = true
	arena.AllianceStations["B2"].aStopReset = true
	arena.AllianceStations["B3"].Bypass = true
	arena.AllianceStations["B3"].aStopReset = true
	err = arena.StartMatch()
	assert.Nil(t, err)
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, true, arena.AllianceStations["R1"].DsConn.Enabled)

	// Press the R1 A-stop.
	plc.redAStops[0] = true
	plc.redEStops[0] = false
	plc.redAStops[1] = false
	plc.redEStops[1] = false
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].EStop)
	arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.EStop)
	assert.Equal(t, true, arena.AllianceStations["R1"].DsConn.AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].DsConn.Enabled)

	// Unpress the R1 A-stop and press the R2 E-stop.
	plc.redAStops[0] = false
	plc.redEStops[0] = false
	plc.redAStops[1] = false
	plc.redEStops[1] = true
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].EStop)
	arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.EStop)
	assert.Equal(t, true, arena.AllianceStations["R1"].DsConn.AStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].DsConn.Enabled)
	assert.Equal(t, true, arena.AllianceStations["R2"].DsConn.EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].DsConn.AStop)

	// Unpress the R2 E-stop.
	plc.redAStops[0] = false
	plc.redEStops[0] = false
	plc.redAStops[1] = false
	plc.redEStops[1] = false
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].EStop)
	arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R2"].DsConn.Enabled)

	// The A-stop stays latched for as long as the robots are in autonomous mode, and is released by the pause.
	runMatchTo(arena, 19.5)
	assert.Equal(t, true, arena.AllianceStations["R1"].AStop)
	runMatchTo(arena, 20.05)
	assert.Equal(t, "pause", arena.CurrentPhase().ID)
	assert.Equal(t, false, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].EStop)

	// Transition into the teleop period without any stops.
	runMatchTo(arena, 23.05)
	assert.Equal(t, false, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].EStop)
	arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
	arena.Update()
	assert.Equal(t, "transition", arena.CurrentPhase().ID)
	assert.Equal(t, true, arena.AllianceStations["R1"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R2"].DsConn.Enabled)

	// Press the R1 E-stop and the R2 A-stop.
	plc.redAStops[0] = false
	plc.redEStops[0] = true
	plc.redAStops[1] = true
	plc.redEStops[1] = false
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R1"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R2"].EStop)
	arena.lastDsPacketTime = time.Unix(0, 0) // Force a DS packet.
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R1"].DsConn.Enabled)
	assert.Equal(t, false, arena.AllianceStations["R2"].DsConn.Enabled)

	// Ensure the other stations A-stops are working as well.
	plc.redAStops[2] = true
	plc.redEStops[2] = false
	plc.blueAStops[0] = true
	plc.blueEStops[0] = false
	plc.blueAStops[1] = true
	plc.blueEStops[1] = false
	plc.blueAStops[2] = true
	plc.blueEStops[2] = false
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["R3"].AStop)
	assert.Equal(t, false, arena.AllianceStations["R3"].EStop)
	assert.Equal(t, true, arena.AllianceStations["B1"].AStop)
	assert.Equal(t, false, arena.AllianceStations["B1"].EStop)
	assert.Equal(t, true, arena.AllianceStations["B2"].AStop)
	assert.Equal(t, false, arena.AllianceStations["B2"].EStop)
	assert.Equal(t, true, arena.AllianceStations["B3"].AStop)
	assert.Equal(t, false, arena.AllianceStations["B3"].EStop)

	// Ensure the other stations E-stops are working as well.
	plc.redAStops[2] = false
	plc.redEStops[2] = true
	plc.blueAStops[0] = false
	plc.blueEStops[0] = true
	plc.blueAStops[1] = false
	plc.blueEStops[1] = true
	plc.blueAStops[2] = false
	plc.blueEStops[2] = true
	arena.Update()
	assert.Equal(t, false, arena.AllianceStations["R3"].AStop)
	assert.Equal(t, true, arena.AllianceStations["R3"].EStop)
	assert.Equal(t, false, arena.AllianceStations["B1"].AStop)
	assert.Equal(t, true, arena.AllianceStations["B1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["B2"].AStop)
	assert.Equal(t, true, arena.AllianceStations["B2"].EStop)
	assert.Equal(t, false, arena.AllianceStations["B3"].AStop)
	assert.Equal(t, true, arena.AllianceStations["B3"].EStop)

	// Ensure unpressed E-stops are cleared at the end of the match.
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	plc.blueEStops[2] = false
	arena.Update()
	assert.Equal(t, true, arena.AllianceStations["R1"].EStop)
	assert.Equal(t, false, arena.AllianceStations["R2"].EStop)
	assert.Equal(t, true, arena.AllianceStations["R3"].EStop)
	assert.Equal(t, true, arena.AllianceStations["B1"].EStop)
	assert.Equal(t, true, arena.AllianceStations["B2"].EStop)
	assert.Equal(t, false, arena.AllianceStations["B3"].EStop)
}

func TestPlcEStopLatchedFromTheFirstInstant(t *testing.T) {
	arena := setupTestArena(t)
	var plc FakePlc
	plc.isEnabled = true
	plc.ftaReady = true
	arena.Plc = &plc
	for _, allianceStation := range arena.AllianceStations {
		allianceStation.Bypass = true
		allianceStation.aStopReset = true
	}
	assert.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)

	// An E-stop pressed and released in the very first instant of the match stays latched. The latch asks the match
	// state, not the clock: on a system with a coarse clock the match time can still read exactly zero here.
	plc.redEStops[0] = true
	arena.handlePlcInputOutput()
	assert.Equal(t, true, arena.AllianceStations["R1"].EStop)
	plc.redEStops[0] = false
	arena.handlePlcInputOutput()
	assert.Equal(t, true, arena.AllianceStations["R1"].EStop)

	// It is released once the match is over.
	assert.Nil(t, arena.AbortMatch())
	arena.handlePlcInputOutput()
	assert.Equal(t, false, arena.AllianceStations["R1"].EStop)
}

func TestSignalVolunteers(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"field"}, 10)
	defer events.Close()

	// Test that SignalVolunteers only works in PreMatch and PostMatch states.
	for _, state := range []MatchState{StartMatch, InMatch, PostTimeout} {
		arena.MatchState = state
		arena.FieldVolunteers = false
		arena.SignalVolunteers()
		assert.False(t, arena.FieldVolunteers)
		assert.NotEqual(t, "signalCount", arena.AllianceStationDisplayMode)
	}
	assert.Empty(t, drainEvents(events))

	// Test SignalVolunteers in PreMatch state.
	arena.MatchState = PreMatch
	arena.FieldReset = true
	arena.AllianceStationDisplayMode = "match"
	arena.SignalVolunteers()
	assert.True(t, arena.FieldVolunteers)
	assert.False(t, arena.FieldReset)
	assert.Equal(t, "signalCount", arena.AllianceStationDisplayMode)
	assert.Equal(t, []string{"field.volunteers"}, eventTypes(drainEvents(events)))

	// Test SignalVolunteers in PostMatch state.
	arena.MatchState = PostMatch
	arena.FieldVolunteers = false
	arena.FieldReset = false
	arena.AllianceStationDisplayMode = "match"
	arena.SignalVolunteers()
	assert.True(t, arena.FieldVolunteers)
	assert.False(t, arena.FieldReset)
	assert.Equal(t, "signalCount", arena.AllianceStationDisplayMode)
	assert.Equal(t, []string{"field.volunteers"}, eventTypes(drainEvents(events)))
}

func TestSignalReset(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"field"}, 10)
	defer events.Close()

	// Test that SignalReset only works in PreMatch and PostMatch states.
	for _, state := range []MatchState{StartMatch, InMatch, PostTimeout} {
		arena.MatchState = state
		arena.FieldReset = false
		arena.FieldVolunteers = false
		arena.AllianceStationDisplayMode = "match"
		arena.SignalReset()
		assert.False(t, arena.FieldReset)
		assert.False(t, arena.FieldVolunteers)
		assert.NotEqual(t, "fieldReset", arena.AllianceStationDisplayMode)
	}
	assert.Empty(t, drainEvents(events))

	// Test SignalReset in PreMatch state.
	arena.MatchState = PreMatch
	arena.FieldReset = false
	arena.FieldVolunteers = true
	arena.AllianceStationDisplayMode = "match"
	arena.SignalReset()
	assert.False(t, arena.FieldVolunteers)
	assert.True(t, arena.FieldReset)
	assert.Equal(t, "fieldReset", arena.AllianceStationDisplayMode)
	assert.Equal(t, []string{"field.reset"}, eventTypes(drainEvents(events)))

	// Test SignalReset in PostMatch state.
	arena.MatchState = PostMatch
	arena.FieldReset = false
	arena.FieldVolunteers = true
	arena.AllianceStationDisplayMode = "match"
	arena.SignalReset()
	assert.False(t, arena.FieldVolunteers)
	assert.True(t, arena.FieldReset)
	assert.Equal(t, "fieldReset", arena.AllianceStationDisplayMode)
	assert.Equal(t, []string{"field.reset"}, eventTypes(drainEvents(events)))
}

func TestStationStatusEvents(t *testing.T) {
	arena := setupTestArena(t)
	arena.Database.CreateTeam(&model.Team{Id: 254})
	assert.Nil(t, arena.assignTeam(254, "B2"))
	arena.Update()
	events := arena.Bus.Subscribe([]string{"station"}, 10)
	defer events.Close()

	// Nothing is published while nothing changes.
	arena.Update()
	assert.Empty(t, drainEvents(events))

	// A change is published once, naming the station and what changed.
	arena.AllianceStations["B2"].Bypass = true
	arena.AllianceStations["B2"].DsConn = &DriverStationConnection{
		TeamId: 254, DsLinked: true, RadioLinked: true, lastPacketTime: time.Now().Add(time.Hour),
	}
	arena.Update()
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, "station.status", received[0].Type)
		assert.Equal(
			t,
			map[string]any{
				"station": "B2", "team": 254, "changed": []string{"driverStation", "radio", "bypass"},
				"driverStation": true, "robot": false, "radio": true, "eStop": false, "aStop": false, "bypass": true,
			},
			received[0].Data,
		)
	}
	arena.Update()
	assert.Empty(t, drainEvents(events))

	arena.AllianceStations["B2"].DsConn.RobotLinked = true
	arena.AllianceStations["R1"].EStop = true
	arena.Update()
	received = drainEvents(events)
	if assert.Equal(t, 2, len(received)) {
		assert.Equal(t, "R1", received[0].Data.(map[string]any)["station"])
		assert.Equal(t, []string{"eStop"}, received[0].Data.(map[string]any)["changed"])
		assert.Equal(t, "B2", received[1].Data.(map[string]any)["station"])
		assert.Equal(t, []string{"robot"}, received[1].Data.(map[string]any)["changed"])
	}
}

func TestMatchLoadedEvent(t *testing.T) {
	arena := setupTestArena(t)
	for teamId := 101; teamId <= 106; teamId++ {
		arena.Database.CreateTeam(&model.Team{Id: teamId})
	}
	events := arena.Bus.Subscribe([]string{"match.loaded"}, 10)
	defer events.Close()

	match := model.Match{
		Type: model.Practice, ShortName: "P3", Red1: 101, Red2: 102, Red3: 103, Blue1: 104, Blue2: 105,
	}
	assert.Nil(t, arena.Database.CreateMatch(&match))
	assert.Nil(t, arena.LoadMatch(&match))
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(
			t,
			map[string]any{
				"id": match.Id, "type": "Practice", "shortName": "P3",
				"teams": map[string]int{"R1": 101, "R2": 102, "R3": 103, "B1": 104, "B2": 105, "B3": 0},
			},
			received[0].Data,
		)
		assert.Equal(t, "", received[0].Match, "the match has not started")
	}

	// A substitution changes who plays, so it is announced again.
	assert.Nil(t, arena.SubstituteTeams(101, 102, 103, 104, 105, 106))
	received = drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, 106, received[0].Data.(map[string]any)["teams"].(map[string]int)["B3"])
	}
}
