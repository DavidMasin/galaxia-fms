// Copyright 2024 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"image/color"
	"testing"
	"time"
)

func TestTeamSign_GenerateInMatchRearText(t *testing.T) {
	// set is an administrator's correction, which may place a value in any phase whatever the clock says.
	set := func(alliance string, robot int, element string, value any, bucket string) scoring.Change {
		return scoring.Change{
			Alliance: alliance, Robot: robot, Element: element, Op: scoring.OpSet, Value: value, Bucket: bucket,
			Source: testAdmin,
		}
	}

	testCases := []struct {
		name             string
		matchType        model.MatchType
		matchTimeSec     float64
		countdown        string
		changes          []scoring.Change
		expectedRearText [4]string
	}{
		{
			name:         "qualification auto",
			matchType:    model.Qualification,
			matchTimeSec: 5,
			countdown:    "2:37",
			expectedRearText: [4]string{
				"          A15 0 2:37",
				"2:37       R000-B000",
				"          A15 0 2:37",
				"2:37       B000-R000",
			},
		},
		{
			name:         "playoff transition",
			matchType:    model.Playoff,
			matchTimeSec: 25,
			countdown:    "2:02",
			changes: []scoring.Change{
				set("red", 0, "fuel", 8, "auto"), set("red", 0, "fuel", 4, "transition"),
				set("red", 1, "auto_tower", "level1", "auto"), set("blue", 0, "fuel", 5, "auto"),
				set("blue", 1, "auto_tower", "level2", "auto"), set("blue", 2, "auto_tower", "level3", "auto"),
			},
			expectedRearText: [4]string{
				"  T08 R027-B035 2:02",
				"2:02       R027-B035",
				"  T08 B035-R027 2:02",
				"2:02       B035-R027",
			},
		},
		{
			// Blue won auto, so in shift 1 it is red whose fuel counts. The game shows each alliance its fuel points.
			name:         "qualification shift 1",
			matchType:    model.Qualification,
			matchTimeSec: 45,
			countdown:    "1:57",
			changes: []scoring.Change{
				set("", 0, "auto_winner", "blue", ""), set("red", 0, "fuel", 10, "auto"),
				set("red", 0, "fuel", 7, "transition"), set("red", 0, "fuel", 13, "shift1"),
				set("blue", 0, "fuel", 11, "auto"), set("blue", 0, "fuel", 6, "transition"),
				set("blue", 0, "fuel", 8, "shift1"), set("blue", 1, "auto_tower", "level1", "auto"),
			},
			expectedRearText: [4]string{
				"         S13 30 1:57",
				"1:57       R030-B032",
				"         S13 17 1:57",
				"1:57       B032-R030",
			},
		},
		{
			name:         "playoff endgame",
			matchType:    model.Playoff,
			matchTimeSec: 152,
			countdown:    "0:11",
			changes: []scoring.Change{
				set("red", 0, "fuel", 37, "auto"), set("blue", 0, "fuel", 63, "endgame"),
			},
			expectedRearText: [4]string{
				"  E11 R037-B063 0:11",
				"0:11       R037-B063",
				"  E11 B063-R037 0:11",
				"0:11       B063-R037",
			},
		},
		{
			name:         "qualification after match",
			matchType:    model.Qualification,
			matchTimeSec: 165,
			countdown:    "0:00",
			expectedRearText: [4]string{
				"              0 0:00",
				"0:00       R000-B000",
				"              0 0:00",
				"0:00       B000-R000",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(
			testCase.name, func(t *testing.T) {
				arena := setupTestArena(t)
				arena.CurrentMatch.Type = testCase.matchType
				startTestMatch(t, arena)
				for _, change := range testCase.changes {
					apply(t, arena, change)
				}
				runMatchTo(arena, testCase.matchTimeSec)

				actualRearText := [4]string{
					generateInMatchTeamRearText(arena, 0, testCase.countdown),
					generateInMatchTimerRearText(arena, 0, testCase.countdown),
					generateInMatchTeamRearText(arena, 1, testCase.countdown),
					generateInMatchTimerRearText(arena, 1, testCase.countdown),
				}
				assert.Equal(t, testCase.expectedRearText, actualRearText)
			},
		)
	}
}

func TestTeamSign_GameTextFallsBackToScore(t *testing.T) {
	arena := setupTestArena(t)

	// A game that does not say what to put on the signs gets the score of the alliance there.
	selectGame(t, arena, "ball-count", nil)
	startTestMatch(t, arena)
	for i := 0; i < 4; i++ {
		apply(t, arena, count("blue", "balls", testDevice))
	}
	runMatchTo(arena, 20)
	assert.Equal(t, "0", generateTeamSignGameText(arena, 0))
	assert.Equal(t, "8", generateTeamSignGameText(arena, 1))
	// The time left in the phase has two digits on the sign, so a long phase shows 99 until it gets there.
	assert.Equal(t, "          T99 0 1:57", generateInMatchTeamRearText(arena, 0, "1:57"))
	assert.Equal(t, "          T99 8 1:57", generateInMatchTeamRearText(arena, 1, "1:57"))
	assert.Equal(t, "B008-R000", generateTeamSignAllianceScores(arena, 1))
}

func TestTeamSign_GameTextGivesWayOnANarrowDisplay(t *testing.T) {
	arena := setupTestArena(t)
	dir := useScratchGames(t, arena)
	source := string(model.TestGame("ball-count").Source) +
		"\nlayout:\n  team_sign: \"'Balls scored so far: ' + string(own.balls.total)\"\n"
	writeGame(t, arena, dir, "ball-count", source)
	selectGame(t, arena, "ball-count", nil)
	startTestMatch(t, arena)
	runMatchTo(arena, 5)

	// The phase and the countdown are kept whole; the text of the game is cut to what is left of twenty characters.
	assert.Equal(t, "Balls scored so far: 0", generateTeamSignGameText(arena, 0))
	rearText := generateInMatchTeamRearText(arena, 0, "2:12")
	assert.Equal(t, "A10 Balls score 2:12", rearText)
	assert.Equal(t, teamSignRearTextLength, len(rearText))
}

func TestTeamSign_Countdown(t *testing.T) {
	arena := setupTestArena(t)
	signs := NewTeamSigns()
	signs.RedTimer.SetId(57)
	frontText := func() string {
		signs.Update(arena)
		return signs.RedTimer.frontText
	}

	// Before the match the timer shows the length of what is about to start.
	assert.Equal(t, "00:20", frontText())
	startTestMatch(t, arena)
	runMatchTo(arena, 5)
	assert.Equal(t, "00:15", frontText())

	// It shows zero through the pause, and then counts down the whole of teleop across its phases.
	runMatchTo(arena, 21)
	assert.Equal(t, "00:00", frontText())
	runMatchTo(arena, 63)
	assert.Equal(t, "01:40", frontText())
	assert.Equal(t, "1:40       R000-B000", signs.RedTimer.rearText)
	runMatchTo(arena, 163.05)
	assert.Equal(t, "00:00", frontText())

	// A timeout counts down its own length.
	assert.Nil(t, arena.ResetMatch())
	assert.Nil(t, arena.StartTimeout("Break", 300))
	arena.SetAllianceStationDisplayMode("timeout")
	setMatchTime(arena, 60)
	assert.Equal(t, "04:00", frontText())
	assert.Equal(t, "Field Break: 04:00", signs.RedTimer.rearText)
}

func TestTeamSign_Timer(t *testing.T) {
	arena := setupTestArena(t)
	sign := TeamSign{isTimer: true}

	// Should do nothing if no address is set.
	sign.update(arena, "", true, "12:34", "Rear Text")
	assert.Equal(t, [128]byte{}, sign.packetData)

	// Check some basics about the data but don't unit-test the whole packet.
	sign.SetId(56)
	sign.update(arena, "", true, "12:34", "Rear Text")
	assert.Equal(t, "CYPRX", string(sign.packetData[0:5]))
	assert.Equal(t, 56, int(sign.packetData[5]))
	assert.Equal(t, 0x04, int(sign.packetData[6]))
	assert.Equal(t, "12:34", string(sign.packetData[10:15]))
	assert.Equal(t, []byte{0, 0}, sign.packetData[15:17])
	assert.Equal(t, "Rear Text", string(sign.packetData[30:39]))
	assert.Equal(t, 40, sign.packetIndex)

	assertSign := func(expectedFrontText string, expectedFrontColor color.RGBA, expectedRearText string) {
		frontText, frontColor, rearText := generateTimerTexts(arena, "23:45", "Rear Text")
		assert.Equal(t, expectedFrontText, frontText)
		assert.Equal(t, expectedFrontColor, frontColor)
		assert.Equal(t, expectedRearText, rearText)
	}

	// Check field reset.
	arena.FieldReset = false
	assertSign("23:45", whiteColor, "Rear Text")
	arena.FieldReset = true
	assertSign("SAFE ", greenColor, "Rear Text")

	// Check timeout mode.
	arena.FieldReset = true
	arena.MatchState = TimeoutActive
	assertSign("23:45", whiteColor, "Field Break: 23:45")

	// Check blank mode.
	arena.AllianceStationDisplayMode = "blank"
	assertSign("     ", whiteColor, "")

	// Check alliance selection.
	arena.AllianceStationDisplayMode = "logo"
	arena.AudienceDisplayMode = "allianceSelection"
	arena.AllianceSelectionShowTimer = false
	assertSign("     ", whiteColor, "")
	arena.AllianceSelectionShowTimer = true
	assertSign("23:45", whiteColor, "")
	arena.AllianceStationDisplayMode = "blank"
	assertSign("     ", whiteColor, "")
}

func TestTeamSign_TeamNumber(t *testing.T) {
	arena := setupTestArena(t)
	allianceStation := arena.AllianceStations["R1"]
	arena.Database.CreateTeam(&model.Team{Id: 254})
	sign := &TeamSign{isTimer: false}

	// Should do nothing if no address is set.
	sign.update(arena, "R1", true, "12:34", "Rear Text")
	assert.Equal(t, [128]byte{}, sign.packetData)

	// Check some basics about the data but don't unit-test the whole packet.
	sign.SetId(53)
	sign.update(arena, "R1", true, "12:34", "Rear Text")
	assert.Equal(t, "CYPRX", string(sign.packetData[0:5]))
	assert.Equal(t, 53, int(sign.packetData[5]))
	assert.Equal(t, 0x04, int(sign.packetData[6]))
	assert.Equal(t, []byte{0x01, 53, 0x01}, sign.packetData[7:10])
	assert.Equal(t, "     ", string(sign.packetData[10:15]))
	assert.Equal(t, []byte{0, 0}, sign.packetData[15:17])
	assert.Equal(t, "No Team Assigned", string(sign.packetData[34:50]))
	assert.Equal(t, 51, sign.packetIndex)

	assertSign := func(isRed bool, expectedFrontText string, expectedFrontColor color.RGBA, expectedRearText string) {
		frontText, frontColor, rearText := sign.generateTeamNumberTexts(
			arena, "R1", isRed, "12:34", "Rear Text",
		)
		assert.Equal(t, expectedFrontText, frontText)
		assert.Equal(t, expectedRearText, rearText)

		// Modify front color to account for time-based blinking.
		frontColor.A = 255
		assert.Equal(t, expectedFrontColor, frontColor)
	}

	assertSign(true, "     ", whiteColor, "    No Team Assigned")
	arena.FieldReset = true
	arena.assignTeam(254, "R1")
	assertSign(true, "  254", greenColor, "254       Connect PC")
	assertSign(false, "  254", greenColor, "254       Connect PC")
	arena.FieldReset = false
	assertSign(true, "  254", redColor, "254       Connect PC")
	assertSign(false, "  254", blueColor, "254       Connect PC")

	// Check through pre-match sequence.
	arena.FieldReset = true
	allianceStation.Ethernet = true
	assertSign(true, "  254", greenColor, "254         Start DS")
	allianceStation.DsConn = &DriverStationConnection{}
	assertSign(true, "  254", greenColor, "254         No Radio")
	allianceStation.DsConn.WrongStation = "R1"
	assertSign(true, "  254", greenColor, "254     Move Station")
	allianceStation.DsConn.WrongStation = ""
	allianceStation.DsConn.RadioLinked = true
	assertSign(true, "  254", greenColor, "254           No Rio")
	allianceStation.DsConn.RioLinked = true
	assertSign(true, "  254", greenColor, "254          No Code")
	allianceStation.DsConn.RobotLinked = true
	assertSign(true, "  254", redColor, "254            Ready")

	arena.FieldReset = false
	assertSign(true, "  254", redColor, "254            Ready")
	allianceStation.DsConn.RobotLinked = false
	assertSign(true, "  254", redColor, "254          No Code")
	allianceStation.DsConn.RobotLinked = true
	allianceStation.Bypass = true
	assertSign(true, "  254", redColor, "254         Bypassed")

	// Check that timeout mode has no effect on the team sign.
	arena.MatchState = TimeoutActive
	assertSign(true, "  254", redColor, "254         Bypassed")
	arena.FieldReset = false

	// Check E-stop and A-stop.
	arena.MatchState = InMatch
	arena.PhaseIndex = 0
	assertSign(true, "  254", redColor, "Rear Text")
	allianceStation.AStop = true
	assertSign(true, "  254", orangeColor, "254           A-STOP")
	allianceStation.EStop = true
	assertSign(false, "  254", orangeColor, "254           E-STOP")
	allianceStation.EStop = false
	arena.PhaseIndex = 2
	assertSign(false, "  254", blueColor, "Rear Text")
	allianceStation.EStop = true
	assertSign(false, "  254", orangeColor, "254           E-STOP")
	arena.MatchState = PostMatch
	arena.PhaseIndex = -1
	assertSign(false, "  254", orangeColor, "254           E-STOP")
	allianceStation.EStop = false
	arena.FieldReset = true
	assertSign(false, "  254", greenColor, "Rear Text")
	allianceStation.EStop = true

	// Test preloading the team for the next match.
	sign.nextMatchTeamId = 1503
	assertSign(false, "  254", orangeColor, "Next Team Up: 1503")
	allianceStation.Bypass = false
	allianceStation.EStop = false
	allianceStation.Ethernet = false
	arena.MatchState = PreMatch
	arena.assignTeam(1503, "R1")
	assertSign(false, " 1503", greenColor, "1503      Connect PC")

	// Check blank mode.
	arena.AllianceStationDisplayMode = "blank"
	assertSign(true, "     ", whiteColor, "")

	// Check alliance selection.
	arena.AllianceStationDisplayMode = "logo"
	arena.AudienceDisplayMode = "allianceSelection"
	year := fmt.Sprintf("%5d", time.Now().Year())
	arena.AllianceSelectionShowTimer = false
	assertSign(true, year, redColor, "1503      Connect PC")
	arena.AllianceSelectionShowTimer = true
	assertSign(false, year, blueColor, "1503      Connect PC")
	arena.AllianceStationDisplayMode = "blank"
	assertSign(false, "     ", whiteColor, "")
}
