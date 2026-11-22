// The countdown to the start of a match: a spoken line, "three, two, one", and the start itself.
//
// Pressing Start does not start the match. It fixes the moment the match will start, a few seconds ahead, and tells
// everything on the field that moment at once: displays (the countdown message), devices (field/state) and the
// event bus (match.countdown). Each counts down to it on its own clock, set against the server's, so the last beat
// lands for everyone at the same instant however long the news took to reach them. The robots stay disabled until
// that moment and are enabled in the pass of the arena loop in which it arrives.
//
// A game without a countdown, or an event that has turned it off, gets a countdown with no beats whose moment is
// now: the next pass of the loop starts the match.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/DavidMasin/galaxia-fms/model"
	"log"
	"sort"
	"strings"
	"time"
)

const (
	countdownBeatPeriod = time.Second
	// How long before the first beat the spoken line begins, and how long before it the countdown first shows when
	// no line is spoken. Displays draw with the same two numbers (static/js/countdown.js).
	countdownIntroLead = 2600 * time.Millisecond
	countdownPlainLead = 400 * time.Millisecond
	// Time allowed for the news of the countdown to reach every display and device before anything is due.
	countdownNotice = 400 * time.Millisecond
	// A start later than this after its moment is worth a line in the log.
	countdownLateWarning = 20 * time.Millisecond
)

// matchCountdown is a countdown that has been started: when the match begins, and what is counted on the way.
type matchCountdown struct {
	goAt       time.Time
	beats      int    // Beats counted before the start; zero when the match starts at once.
	intro      bool   // Whether a line is spoken before the first beat.
	voice      string // Which line.
	beatsFired int
	// The match's start time before this countdown, put back if it is cancelled.
	previousStartedAt time.Time
}

// CountdownMessage is the countdown as browsers receive it. A display that connects in the middle of one is told
// the same moment as the rest and joins in on the beat.
type CountdownMessage struct {
	State string `json:"state"` // armed, cancelled, or idle when there is none.
	GoAt  int64  `json:"goAt"`  // When the match starts, in UTC milliseconds.
	Beats int    `json:"beats"`
	Intro bool   `json:"intro"`
	Voice string `json:"voice"`
	Sound bool   `json:"sound"` // False when the sounds of this match are muted.
}

// CountdownInfo is the countdown as the API describes it.
type CountdownInfo struct {
	GoAt  time.Time
	Beats int
}

// planCountdown works out the countdown of a match started now, from the active game and the event's settings.
func (arena *Arena) planCountdown(now time.Time) matchCountdown {
	settings := arena.Game.Match.Countdown
	countdown := matchCountdown{goAt: now}
	switch {
	case !settings.Enabled || arena.EventSettings.CountdownMode == model.CountdownOff:
		return countdown
	case arena.EventSettings.CountdownMode == model.CountdownQuick:
		countdown.beats = settings.QuickBeats
	default:
		countdown.beats = settings.Beats
		countdown.intro, countdown.voice = settings.Voice != "", settings.Voice
	}
	if countdown.beats <= 0 {
		countdown.beats, countdown.intro = 0, false
		return countdown
	}
	lead := countdownNotice + time.Duration(countdown.beats)*countdownBeatPeriod
	if countdown.intro {
		lead += countdownIntroLead
	} else {
		lead += countdownPlainLead
	}
	countdown.goAt = now.Add(lead)
	return countdown
}

// beatOffsetsMs returns when each beat of the countdown falls, in milliseconds relative to the start of the match,
// ending with the start itself at zero.
func (countdown *matchCountdown) beatOffsetsMs() []int64 {
	offsets := make([]int64, 0, countdown.beats+1)
	for beat := countdown.beats; beat >= 0; beat-- {
		offsets = append(offsets, -int64(beat)*countdownBeatPeriod.Milliseconds())
	}
	return offsets
}

// startCountdown puts the arena into the countdown that ends with the start of the loaded match. The caller has
// checked that the match may start.
func (arena *Arena) startCountdown() {
	countdown := arena.planCountdown(time.Now())
	countdown.previousStartedAt = arena.CurrentMatch.StartedAt

	// Save the match start time to the database for posterity. It is the moment the match will start, which is
	// known already; a cancelled countdown takes it back.
	arena.CurrentMatch.StartedAt = countdown.goAt
	if arena.CurrentMatch.Type != model.Test {
		arena.Database.UpdateMatch(arena.CurrentMatch)
	}
	for _, allianceStation := range arena.AllianceStations {
		// Save the teams that have successfully connected to the field.
		if allianceStation.Team != nil && !allianceStation.Team.HasConnected && allianceStation.DsConn != nil &&
			allianceStation.DsConn.RobotLinked {
			allianceStation.Team.HasConnected = true
			arena.Database.UpdateTeam(allianceStation.Team)
		}
	}

	arena.countdown = countdown
	arena.MatchState = Countdown
	arena.refreshBusContext()

	// From here on nobody belongs on the field, and the audience watches the match.
	arena.SetAudienceDisplayMode("match")
	arena.SetAllianceStationDisplayMode("match")
	go arena.BlackmagicClient.StartRecording()
	arena.Plc.ResetMatch()
	arena.FieldVolunteers = false
	arena.FieldReset = false

	if countdown.beats > 0 {
		arena.Bus.Publish(
			"match.countdown",
			map[string]any{
				"t0": countdown.goAt.UnixMilli(), "beats": countdown.beatOffsetsMs(), "intro": countdown.intro,
				"voice": countdown.voice,
			},
		)
		arena.recordDeviceEvent(device.EventCountdownStart)
		arena.CountdownNotifier.Notify()
	}
}

// advanceCountdown fires the beats of the countdown that have come due and reports whether the moment of the start
// has arrived.
func (arena *Arena) advanceCountdown(now time.Time) bool {
	countdown := &arena.countdown
	for countdown.beatsFired < countdown.beats {
		beatAt := countdown.goAt.Add(-time.Duration(countdown.beats-countdown.beatsFired) * countdownBeatPeriod)
		if now.Before(beatAt) {
			break
		}
		countdown.beatsFired++
		arena.recordDeviceEvent(device.EventCountdownBeat)
	}
	return !now.Before(countdown.goAt)
}

// finishCountdown returns the moment the match starts, now that it has come: the moment that was announced, so that
// the match clock agrees with every display that counted down to it. It notes how late the arena loop was.
func (arena *Arena) finishCountdown() time.Time {
	countdown := &arena.countdown
	now := time.Now()
	if countdown.beats == 0 {
		// Nothing was announced; the match starts when the loop gets to it.
		return now
	}
	if late := now.Sub(countdown.goAt); late > countdownLateWarning {
		log.Printf("Warning: the match started %d ms after the end of its countdown", late.Milliseconds())
	}
	arena.recordDeviceEvent(device.EventCountdownGo)
	return countdown.goAt
}

// cancelCountdown calls the start off and puts the arena back to before the match.
func (arena *Arena) cancelCountdown(reason string) {
	countdown := arena.countdown
	if countdown.beats > 0 {
		// Published while the match is still the context of the bus, so that the event names it.
		arena.Bus.Publish("match.countdown_cancelled", map[string]any{"reason": reason})
		arena.recordDeviceEvent(device.EventCountdownCancel)
		arena.PlaySound("abort")
		log.Printf("The countdown was cancelled: %s.", reason)
	}
	arena.MatchState = PreMatch
	arena.refreshBusContext()
	arena.CurrentMatch.StartedAt = countdown.previousStartedAt
	if arena.CurrentMatch.Type != model.Test {
		arena.Database.UpdateMatch(arena.CurrentMatch)
	}
	go arena.BlackmagicClient.StopRecording()
	if countdown.beats > 0 {
		arena.CountdownNotifier.NotifyWithMessage(CountdownMessage{State: "cancelled"})
	}
}

// countdownStopReason says, in words, which stop is active that keeps a match from starting, or nothing if none is.
func (arena *Arena) countdownStopReason() string {
	if arena.Plc.IsEnabled() && arena.Plc.GetFieldEStop() {
		return "the field emergency stop is active"
	}
	if arena.Devices.Stops().FieldStop {
		return "a field stop is active"
	}
	var stopped []string
	for _, stationId := range arena.activeStationIds() {
		if arena.AllianceStations[stationId].EStop {
			stopped = append(stopped, stationId)
		}
	}
	if len(stopped) > 0 {
		sort.Strings(stopped)
		return fmt.Sprintf("an emergency stop is active (%s)", strings.Join(stopped, ", "))
	}
	return ""
}

// passDelay returns how long the arena loop waits before its next pass: its period, or less when the match is to
// start sooner than that, so that the pass which starts it runs at that moment and not up to a period after it.
func (arena *Arena) passDelay() time.Duration {
	delay := arenaLoopPeriodMs * time.Millisecond
	if arena.MatchState == Countdown {
		if untilGo := time.Until(arena.countdown.goAt); untilGo < delay {
			delay = max(untilGo, 0)
		}
	}
	return delay
}

// CountdownInfo returns the countdown that is running, or nil when none is.
func (arena *Arena) CountdownInfo() *CountdownInfo {
	if arena.MatchState != Countdown || arena.countdown.beats == 0 {
		return nil
	}
	return &CountdownInfo{GoAt: arena.countdown.goAt, Beats: arena.countdown.beats}
}

func (arena *Arena) generateCountdownMessage() any {
	if arena.MatchState != Countdown || arena.countdown.beats == 0 {
		return CountdownMessage{State: "idle"}
	}
	countdown := &arena.countdown
	return CountdownMessage{
		State: "armed", GoAt: countdown.goAt.UnixMilli(), Beats: countdown.beats, Intro: countdown.intro,
		Voice: countdown.voice, Sound: !arena.MuteMatchSounds,
	}
}
