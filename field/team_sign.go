// Copyright 2024 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Models and logic for controlling a Cypress team number / timer sign.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"image/color"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Represents a collection of team number and timer signs.
type TeamSigns struct {
	Red1      TeamSign
	Red2      TeamSign
	Red3      TeamSign
	RedTimer  TeamSign
	Blue1     TeamSign
	Blue2     TeamSign
	Blue3     TeamSign
	BlueTimer TeamSign
}

// Represents a team number or timer sign.
type TeamSign struct {
	isTimer         bool
	address         byte
	nextMatchTeamId int
	frontText       string
	frontColor      color.RGBA
	rearText        string
	lastFrontText   string
	lastFrontColor  color.RGBA
	lastRearText    string
	udpConn         net.Conn
	packetData      [128]byte
	packetIndex     int
	lastPacketTime  time.Time
}

const (
	teamSignAddressPrefix            = "10.0.100."
	teamSignPort                     = 10011
	teamSignPacketMagicString        = "CYPRX"
	teamSignPacketHeaderLength       = 7
	teamSignCommandSetDisplay        = 0x04
	teamSignAddressSingle            = 0x01
	teamSignPacketTypeFrontText      = 0x01
	teamSignPacketTypeRearText       = 0x02
	teamSignPacketTypeFrontIntensity = 0x03
	teamSignPacketTypeColor          = 0x04
	teamSignPacketPeriodMs           = 5000
	teamSignBlinkPeriodMs            = 750
	teamSignRearTextLength           = 20
)

// Predefined colors for the team sign front text. The "A" channel is used as the intensity.
var redColor = color.RGBA{255, 0, 0, 255}
var blueColor = color.RGBA{0, 50, 255, 255}
var greenColor = color.RGBA{0, 255, 0, 255}
var orangeColor = color.RGBA{255, 50, 0, 255}
var purpleColor = color.RGBA{255, 0, 240, 255}
var whiteColor = color.RGBA{255, 200, 180, 255}

// Creates a new collection of team signs.
func NewTeamSigns() *TeamSigns {
	signs := new(TeamSigns)
	signs.RedTimer.isTimer = true
	signs.BlueTimer.isTimer = true
	return signs
}

// Updates the state of all signs with the latest data and sends packets to the signs if anything has changed.
func (signs *TeamSigns) Update(arena *Arena) {
	// Generate the countdown string which is used in multiple places.
	var countdownSec int
	switch arena.MatchState {
	case PreMatch:
		if arena.AudienceDisplayMode == "allianceSelection" {
			countdownSec = arena.AllianceSelectionTimeRemainingSec
		} else {
			countdownSec = int(math.Ceil(arena.modeRemainingSec()))
		}
	case StartMatch, InMatch:
		countdownSec = int(math.Ceil(arena.modeRemainingSec()))
	case TimeoutActive:
		countdownSec = arena.TimeoutDurationSec - int(arena.MatchTimeSec())
	default:
		countdownSec = 0
	}
	countdown := fmt.Sprintf("%02d:%02d", countdownSec/60, countdownSec%60)
	rearCountdown := fmt.Sprintf("%d:%02d", countdownSec/60, countdownSec%60)

	// Generate the in-match rear text which is common to a whole alliance. The red signs belong to the first
	// alliance of the game and the blue signs to the second.
	redInMatchTeamRearText := generateInMatchTeamRearText(arena, 0, rearCountdown)
	redInMatchTimerRearText := generateInMatchTimerRearText(arena, 0, rearCountdown)
	blueInMatchTeamRearText := generateInMatchTeamRearText(arena, 1, rearCountdown)
	blueInMatchTimerRearText := generateInMatchTimerRearText(arena, 1, rearCountdown)

	signs.Red1.update(arena, "R1", true, countdown, redInMatchTeamRearText)
	signs.Red2.update(arena, "R2", true, countdown, redInMatchTeamRearText)
	signs.Red3.update(arena, "R3", true, countdown, redInMatchTeamRearText)
	signs.RedTimer.update(arena, "", true, countdown, redInMatchTimerRearText)
	signs.Blue1.update(arena, "B1", false, countdown, blueInMatchTeamRearText)
	signs.Blue2.update(arena, "B2", false, countdown, blueInMatchTeamRearText)
	signs.Blue3.update(arena, "B3", false, countdown, blueInMatchTeamRearText)
	signs.BlueTimer.update(arena, "", false, countdown, blueInMatchTimerRearText)
}

// Sets the team numbers for the next match on all signs.
func (signs *TeamSigns) SetNextMatchTeams(teams [6]int) {
	signs.Red1.nextMatchTeamId = teams[0]
	signs.Red2.nextMatchTeamId = teams[1]
	signs.Red3.nextMatchTeamId = teams[2]
	signs.Blue1.nextMatchTeamId = teams[3]
	signs.Blue2.nextMatchTeamId = teams[4]
	signs.Blue3.nextMatchTeamId = teams[5]
}

// Sets the IP address of the sign.
func (sign *TeamSign) SetId(id int) {
	if sign.udpConn != nil {
		if err := sign.udpConn.Close(); err != nil {
			log.Printf("Failed to close team sign connection: %v", err)
		}
	}
	sign.address = byte(id)
	if id == 0 {
		// The sign is not configured.
		return
	}
	ipAddress := fmt.Sprintf("%s%d", teamSignAddressPrefix, id)

	var err error
	sign.udpConn, err = net.Dial("udp4", fmt.Sprintf("%s:%d", ipAddress, teamSignPort))
	if err != nil {
		log.Printf("Failed to connect to team sign at %s: %v", ipAddress, err)
		return
	}
	addressParts := strings.Split(ipAddress, ".")
	if len(addressParts) != 4 {
		log.Printf("Failed to configure team sign: invalid IP address: %s", ipAddress)
		return
	}
	address, err := strconv.Atoi(addressParts[3])
	if err != nil {
		log.Printf("Failed to configure team sign: invalid IP address: %s", ipAddress)
		return
	}
	sign.address = byte(address)

	// Reset the sign's state to ensure that the next packet sent will update the sign.
	sign.packetIndex = 0
	sign.lastPacketTime = time.Time{}
}

// Updates the sign's internal state with the latest data and sends packets to the sign if anything has changed.
func (sign *TeamSign) update(arena *Arena, station string, isRed bool, countdown, inMatchRearText string) {
	if sign.address == 0 {
		// Don't do anything if there is no sign configured in this position.
		return
	}

	if sign.isTimer {
		sign.frontText, sign.frontColor, sign.rearText = generateTimerTexts(arena, countdown, inMatchRearText)
	} else {
		sign.frontText, sign.frontColor, sign.rearText = sign.generateTeamNumberTexts(
			arena, station, isRed, countdown, inMatchRearText,
		)
	}

	if err := sign.sendPacket(); err != nil {
		log.Printf("Failed to send team sign packet: %v", err)
	}
}

// Returns the in-match rear text for the team number display that is common to the whole given alliance: the
// phase and the time left in it, then what the game wants its drive teams to see, then the countdown. In the
// playoffs the middle part is the two scores instead, because by then only winning matters.
func generateInMatchTeamRearText(arena *Arena, allianceIndex int, countdown string) string {
	periodText := generateTeamSignPeriodText(arena)
	var gameText string
	if arena.CurrentMatch.Type == model.Playoff {
		gameText = generateTeamSignAllianceScores(arena, allianceIndex)
	} else {
		gameText = generateTeamSignGameText(arena, allianceIndex)
	}

	// The display is only so wide. The phase and the countdown matter most, so the game's text gives way.
	fixedLength := len(countdown)
	if periodText != "" {
		fixedLength += len(periodText) + 1
	}
	if maxLength := teamSignRearTextLength - fixedLength - 1; len(gameText) > maxLength {
		gameText = gameText[:max(maxLength, 0)]
	}
	var parts []string
	for _, part := range []string{periodText, gameText, countdown} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return formatTeamSignRearText(strings.Join(parts, " "))
}

// Returns the in-match rear text for the timer display for the given alliance.
func generateInMatchTimerRearText(arena *Arena, allianceIndex int, countdown string) string {
	allianceScores := generateTeamSignAllianceScores(arena, allianceIndex)
	return fmt.Sprintf("%s%*s", countdown, teamSignRearTextLength-len(countdown), allianceScores)
}

// Returns what the game puts on the signs of the given alliance: the text of its layout.team_sign expression, or
// the score of the alliance if the game has none or it fails.
func generateTeamSignGameText(arena *Arena, allianceIndex int) string {
	allianceId := arena.AllianceId(allianceIndex)
	if expression := arena.Game.Layout.TeamSign; expression != nil && allianceId != "" {
		value, err := arena.evalLive(expression, allianceId, nil)
		if err == nil && value != nil {
			if text, ok := value.(string); ok {
				return text
			}
			return fmt.Sprint(value)
		}
		if err != nil {
			arena.logExpressionError(expression, "team sign text", err)
		}
	}
	return strconv.Itoa(teamSignAllianceScore(arena, allianceIndex))
}

// Returns the live score string for the given alliance: its own score first, then the other side.
func generateTeamSignAllianceScores(arena *Arena, allianceIndex int) string {
	opponentIndex := 1 - allianceIndex
	return fmt.Sprintf(
		"%s%03d-%s%03d",
		teamSignAllianceLetter(arena, allianceIndex), teamSignAllianceScore(arena, allianceIndex),
		teamSignAllianceLetter(arena, opponentIndex), teamSignAllianceScore(arena, opponentIndex),
	)
}

// Returns the current score of the alliance at the given index, in whole points.
func teamSignAllianceScore(arena *Arena, allianceIndex int) int {
	result, _, _ := arena.ScoreSnapshot()
	if result == nil || result.Alliances[arena.AllianceId(allianceIndex)] == nil {
		return 0
	}
	return int(math.Round(result.Alliances[arena.AllianceId(allianceIndex)].Score))
}

// Returns the one-letter code the game gives the alliance at the given index.
func teamSignAllianceLetter(arena *Arena, allianceIndex int) string {
	if arena.AllianceId(allianceIndex) == "" {
		return "?"
	}
	return arena.Game.Alliances.List[allianceIndex].Short
}

// Returns the rear text right-justified to fill the physical display width.
func formatTeamSignRearText(text string) string {
	return fmt.Sprintf("%*s", teamSignRearTextLength, text)
}

// Returns the match period indicator shown at the start of the team sign rear text: the first letter of the name
// of the phase that is running and the seconds left in it. It is empty when no phase is running.
func generateTeamSignPeriodText(arena *Arena) string {
	phase := arena.CurrentPhase()
	if phase == nil {
		return ""
	}
	prefix := "-"
	for _, letter := range phase.Name {
		if unicode.IsLetter(letter) && letter < unicode.MaxASCII {
			prefix = string(unicode.ToUpper(letter))
			break
		}
	}
	return fmt.Sprintf("%s%02d", prefix, min(int(math.Ceil(arena.phaseRemainingSec())), 99))
}

// Returns the front text, front color, and rear text to display on the timer display.
func generateTimerTexts(arena *Arena, countdown, inMatchRearText string) (string, color.RGBA, string) {
	if arena.AllianceStationDisplayMode == "blank" {
		return "     ", whiteColor, ""
	}
	if arena.AudienceDisplayMode == "allianceSelection" {
		if arena.AllianceSelectionShowTimer {
			return countdown, whiteColor, ""
		} else {
			return "     ", whiteColor, ""
		}
	}

	var frontText string
	var frontColor color.RGBA
	rearText := inMatchRearText
	if arena.AllianceStationDisplayMode == "logo" {
		frontText = fmt.Sprintf("%5d", time.Now().Year())
		frontColor = whiteColor
	} else if arena.AllianceStationDisplayMode == "timeout" {
		frontText = countdown
		frontColor = whiteColor
	} else if arena.FieldReset && arena.MatchState != TimeoutActive {
		frontText = "SAFE "
		frontColor = greenColor
	} else if arena.FieldVolunteers && arena.MatchState != TimeoutActive {
		frontText = "CLEAn"
		frontColor = purpleColor
	} else {
		frontText = countdown
		frontColor = whiteColor
	}
	if arena.MatchState == TimeoutActive {
		rearText = fmt.Sprintf("Field Break: %s", countdown)
	}
	return frontText, frontColor, rearText
}

// Returns the front text, front color, and rear text to display on the sign for the given alliance station.
func (sign *TeamSign) generateTeamNumberTexts(
	arena *Arena, station string, isRed bool, countdown, inMatchRearText string,
) (string, color.RGBA, string) {
	allianceStation := arena.AllianceStations[station]
	allianceColor := redColor
	if !isRed {
		allianceColor = blueColor
	}

	if arena.AllianceStationDisplayMode == "blank" {
		return "     ", whiteColor, ""
	}

	var frontText string
	var frontColor color.RGBA
	if arena.AllianceStationDisplayMode == "logo" {
		frontText = fmt.Sprintf("%5d", time.Now().Year())
		frontColor = allianceColor
	} else {
		if allianceStation.Team == nil {
			return "     ", whiteColor, fmt.Sprintf("%20s", "No Team Assigned")
		}

		frontText = fmt.Sprintf("%5d", allianceStation.Team.Id)

		if allianceStation.EStop {
			frontColor = orangeColor
		} else if allianceStation.AStop && arena.inAutoPhase() {
			frontColor = blinkColor(orangeColor)
		} else if arena.MatchState == PreMatch && station != "" && arena.checkAllianceStationsReady(station) == nil {
			frontColor = allianceColor
		} else if arena.FieldReset {
			frontColor = greenColor
		} else if arena.FieldVolunteers {
			frontColor = purpleColor
		} else if allianceStation.DsConn != nil && !allianceStation.DsConn.RobotLinked && arena.MatchState == InMatch {
			// Blink the display to indicate that the robot is not linked while the match is in progress.
			frontColor = blinkColor(allianceColor)
		} else {
			frontColor = allianceColor
		}
	}

	var message string
	if allianceStation.EStop {
		message = "E-STOP"
	} else if allianceStation.AStop && arena.inAutoPhase() {
		message = "A-STOP"
	} else if arena.MatchState == PreMatch || arena.MatchState == TimeoutActive {
		if allianceStation.Bypass {
			message = "Bypassed"
		} else if !allianceStation.Ethernet {
			message = "Connect PC"
		} else if allianceStation.DsConn == nil {
			message = "Start DS"
		} else if allianceStation.DsConn.WrongStation != "" {
			message = "Move Station"
		} else if !allianceStation.DsConn.RadioLinked {
			message = "No Radio"
		} else if !allianceStation.DsConn.RioLinked {
			message = "No Rio"
		} else if !allianceStation.DsConn.RobotLinked {
			message = "No Code"
		} else {
			message = "Ready"
		}
	}

	var rearText string
	if arena.MatchState == PostMatch && sign.nextMatchTeamId > 0 && sign.nextMatchTeamId != allianceStation.Team.Id {
		// Show the next match team number on the rear display before the score is committed so that queueing teams know
		// where to go.
		rearText = fmt.Sprintf("Next Team Up: %d", sign.nextMatchTeamId)
	} else if len(message) > 0 {
		teamId := 0
		if allianceStation.Team != nil {
			teamId = allianceStation.Team.Id
		}
		rearText = fmt.Sprintf("%-5d %14s", teamId, message)
	} else {
		rearText = inMatchRearText
	}

	return frontText, frontColor, rearText
}

// Sends a UDP packet to the sign if its state has changed.
func (sign *TeamSign) sendPacket() error {
	if sign.packetIndex == 0 {
		// Write the static packet header the first time this method is invoked.
		sign.writePacketData([]byte(teamSignPacketMagicString))
		sign.writePacketData([]byte{sign.address, teamSignCommandSetDisplay})
	} else {
		// Reset the write index to just after the header.
		sign.packetIndex = teamSignPacketHeaderLength
	}

	isStale := time.Now().Sub(sign.lastPacketTime).Milliseconds() >= teamSignPacketPeriodMs

	if sign.frontText != sign.lastFrontText || isStale {
		sign.writePacketData([]byte{teamSignAddressSingle, sign.address, teamSignPacketTypeFrontText})
		sign.writePacketData([]byte(sign.frontText))
		sign.writePacketData([]byte{0, 0}) // Second byte is "show decimal point".
		sign.lastFrontText = sign.frontText
	}

	if sign.frontColor != sign.lastFrontColor || isStale {
		sign.writePacketData([]byte{teamSignAddressSingle, sign.address, teamSignPacketTypeColor})
		sign.writePacketData([]byte{sign.frontColor.R, sign.frontColor.G, sign.frontColor.B})
		sign.writePacketData([]byte{teamSignAddressSingle, sign.address, teamSignPacketTypeFrontIntensity})
		sign.writePacketData([]byte{sign.frontColor.A})
		sign.lastFrontColor = sign.frontColor
	}

	if sign.rearText != sign.lastRearText || isStale {
		sign.writePacketData([]byte{teamSignAddressSingle, sign.address, teamSignPacketTypeRearText})
		sign.writePacketData([]byte(sign.rearText))
		sign.writePacketData([]byte{0})
		sign.lastRearText = sign.rearText
	}

	if sign.packetIndex > teamSignPacketHeaderLength && sign.udpConn != nil {
		sign.lastPacketTime = time.Now()
		if _, err := sign.udpConn.Write(sign.packetData[:sign.packetIndex]); err != nil {
			return err
		}
	}

	return nil
}

// Writes the given data to the packet buffer and advances the write index.
func (sign *TeamSign) writePacketData(data []byte) {
	for _, value := range data {
		sign.packetData[sign.packetIndex] = value
		sign.packetIndex++
	}
}

// Periodically modifies the given color to zero brightness to create a blinking effect.
func blinkColor(originalColor color.RGBA) color.RGBA {
	if time.Now().UnixMilli()%teamSignBlinkPeriodMs < teamSignBlinkPeriodMs/2 {
		return originalColor
	}
	return color.RGBA{originalColor.R, originalColor.G, originalColor.B, 0}
}
