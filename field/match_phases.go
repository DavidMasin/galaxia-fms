// The match clock: walking through the phases the active game defines, and firing their cues and triggers.

package field

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/partner"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"math"
	"sort"
	"time"
)

// The cue id that tells Companion the endgame has begun. It is a convention between game files and the Companion
// settings, not a rule of any game: a game that wants the button pressed gives one of its cues this id.
const companionEndgameCueId = "endgame"

// Names a game file may use for a cue's sound that play a differently named file. They keep game files readable:
// "match_start" says more than "start" does.
var soundAliases = map[string]string{"match_start": "start", "phase_end": "end", "match_end": "end"}

// CurrentPhase returns the phase of the active game that is running, or nil if no match is in progress.
func (arena *Arena) CurrentPhase() *gamedef.Phase {
	if arena.MatchState != InMatch || arena.PhaseIndex < 0 || arena.PhaseIndex >= len(arena.Game.Match.Phases) {
		return nil
	}
	return arena.Game.Match.Phases[arena.PhaseIndex]
}

// MatchTimeSec returns the fractional number of seconds since the match started while it is running, or since the
// timeout started while one is counting down. It is zero whenever no clock is running.
func (arena *Arena) MatchTimeSec() float64 {
	switch arena.MatchState {
	case InMatch, TimeoutActive, PostTimeout:
		return time.Since(arena.MatchStartTime).Seconds()
	}
	return 0
}

// PhaseTimeSec returns the fractional number of seconds into the current phase, or zero outside a match.
func (arena *Arena) PhaseTimeSec() float64 {
	if arena.CurrentPhase() == nil {
		return 0
	}
	return arena.MatchTimeSec() - arena.phaseStartSec(arena.PhaseIndex)
}

// phaseStartSec returns how many seconds into the match the phase with the given index begins.
func (arena *Arena) phaseStartSec(phaseIndex int) float64 {
	start := 0.0
	for i := 0; i < phaseIndex && i < len(arena.Game.Match.Phases); i++ {
		start += arena.Game.Match.Phases[i].Duration
	}
	return start
}

// inAutoPhase reports whether the robots are currently being told to run autonomously.
func (arena *Arena) inAutoPhase() bool {
	phase := arena.CurrentPhase()
	return phase != nil && phase.Robots == gamedef.RobotsAuto
}

// robotMode returns what the driver stations are told: whether robots are in autonomous mode and whether they are
// enabled. Before the match they are held disabled in the mode of the first phase, which is what lets a robot
// prepare its autonomous routine.
func (arena *Arena) robotMode() (auto, enabled bool) {
	switch arena.MatchState {
	case PreMatch, StartMatch:
		phases := arena.Game.Match.Phases
		return len(phases) > 0 && phases[0].Robots == gamedef.RobotsAuto, false
	case InMatch:
		phase := arena.CurrentPhase()
		return phase.Robots == gamedef.RobotsAuto, phase.Robots != gamedef.RobotsDisabled
	}
	return false, false
}

// modeRemainingSec returns the number shown on the field timers: the time left until the robots are next told to
// do something different, which is how a match is traditionally counted down (autonomous, then the whole of
// teleop, whatever phases the game splits it into). A pause between the two shows zero. Before the match it is the
// length of the first such stretch.
func (arena *Arena) modeRemainingSec() float64 {
	phases := arena.Game.Match.Phases
	index, elapsed := 0, 0.0
	switch arena.MatchState {
	case PreMatch, StartMatch:
	case InMatch:
		index, elapsed = arena.PhaseIndex, arena.PhaseTimeSec()
	default:
		return 0
	}
	if index >= len(phases) || (arena.MatchState == InMatch && phases[index].Robots == gamedef.RobotsDisabled) {
		return 0
	}
	remaining := -elapsed
	for i := index; i < len(phases) && phases[i].Robots == phases[index].Robots; i++ {
		remaining += phases[i].Duration
	}
	return math.Max(0, remaining)
}

// phaseRemainingSec returns the time left in the current phase, or zero outside a match.
func (arena *Arena) phaseRemainingSec() float64 {
	phase := arena.CurrentPhase()
	if phase == nil {
		return 0
	}
	return math.Max(0, phase.Duration-arena.PhaseTimeSec())
}

// gracePhase returns the phase that input arriving right now may still be credited to: the latest phase before the
// current one that ended less than the score_grace of the game ago. A phase in which robots were disabled is passed
// over, because nothing can still be on its way from a phase in which nothing moved; the window of the phase before
// it carries on through it. It returns nil when no window is open.
func (arena *Arena) gracePhase() *gamedef.Phase {
	if arena.CurrentPhase() == nil {
		return nil
	}
	phases := arena.Game.Match.Phases
	matchTimeSec := arena.MatchTimeSec()
	for i := arena.PhaseIndex - 1; i >= 0; i-- {
		if matchTimeSec-arena.phaseStartSec(i+1) >= arena.Game.Match.ScoreGrace {
			break
		}
		if phases[i].Robots != gamedef.RobotsDisabled {
			return phases[i]
		}
	}
	return nil
}

// dsSecondsRemaining returns the countdown sent to driver stations: the whole seconds left in the current phase.
// Before the match it is the length of the first phase, so that a driver station shows what is about to start.
func (arena *Arena) dsSecondsRemaining() int {
	switch arena.MatchState {
	case InMatch:
		return int(math.Ceil(arena.phaseRemainingSec()))
	case PostMatch:
		return 0
	}
	if phases := arena.Game.Match.Phases; len(phases) > 0 {
		return int(math.Ceil(phases[0].Duration))
	}
	return 0
}

// clock returns the match clock as the game's live expressions see it.
func (arena *Arena) clock() scoring.Clock {
	switch arena.MatchState {
	case InMatch:
		if phase := arena.CurrentPhase(); phase != nil {
			return scoring.ClockAt(arena.Game, phase.ID, arena.PhaseTimeSec())
		}
	case PostMatch:
		return scoring.Clock{
			State: "post", MatchTime: arena.matchEndTimeSec,
			MatchRemaining: math.Max(0, arena.Game.Duration()-arena.matchEndTimeSec),
		}
	case TimeoutActive, PostTimeout:
		return scoring.Clock{State: "timeout", MatchRemaining: arena.Game.Duration()}
	}
	return scoring.Clock{State: "idle", MatchRemaining: arena.Game.Duration()}
}

// beginMatch starts the clock and enters the first phase.
func (arena *Arena) beginMatch() {
	arena.MatchStartTime = time.Now()
	arena.LastMatchTimeSec = -1
	arena.MatchState = InMatch
	arena.matchAborted = false
	arena.Bus.Publish("match.started", map[string]any{"time": arena.MatchStartTime.UTC()})
	arena.enterPhase(0)
}

// advancePhases moves the match on to wherever the clock says it is: it fires the cues of the current phase that
// have come due and, when the phase is over, leaves it and enters the next, or ends the match after the last. It
// catches up through any number of phases in one call, in order, so that no cue or trigger is ever skipped. It
// reports whether the phase or the match state changed.
func (arena *Arena) advancePhases(matchTimeSec float64) bool {
	changed := false
	for arena.MatchState == InMatch {
		phase := arena.CurrentPhase()
		phaseTimeSec := matchTimeSec - arena.phaseStartSec(arena.PhaseIndex)
		arena.fireCues(phase, phaseTimeSec)
		if phaseTimeSec < phase.Duration {
			break
		}

		arena.runTriggers(scoring.TriggerEvent{PhaseEnd: phase.ID})
		changed = true
		if arena.PhaseIndex+1 < len(arena.Game.Match.Phases) {
			arena.enterPhase(arena.PhaseIndex + 1)
		} else {
			arena.endMatch(false)
		}
	}
	return changed
}

// enterPhase makes the phase with the given index the current one.
func (arena *Arena) enterPhase(phaseIndex int) {
	arena.PhaseIndex = phaseIndex
	arena.cuesFired = make(map[*gamedef.Cue]bool)
	phase := arena.Game.Match.Phases[phaseIndex]
	arena.fireCues(phase, 0)
	arena.runTriggers(scoring.TriggerEvent{PhaseStart: phase.ID})
	arena.Bus.Publish(
		"match.phase_changed",
		map[string]any{"phase": phase.ID, "index": phaseIndex, "robots": phase.Robots, "duration": phase.Duration},
	)

	// Companion's "teleop start" button is pressed when the drivers first take control.
	if phase.Robots == gamedef.RobotsTeleop {
		first := true
		for _, earlier := range arena.Game.Match.Phases[:phaseIndex] {
			first = first && earlier.Robots != gamedef.RobotsTeleop
		}
		if first {
			go arena.CompanionClient.SendEvent(partner.EventTeleopStart)
		}
	}
}

// fireCues fires the cues of the given phase whose moment has come and that have not fired yet, earliest first.
func (arena *Arena) fireCues(phase *gamedef.Phase, phaseTimeSec float64) {
	var due []*gamedef.Cue
	for _, cue := range phase.Cues {
		if !arena.cuesFired[cue] && cue.Offset(phase.Duration) <= phaseTimeSec {
			due = append(due, cue)
		}
	}
	sort.SliceStable(
		due,
		func(i, j int) bool {
			return due[i].Offset(phase.Duration) < due[j].Offset(phase.Duration)
		},
	)
	for _, cue := range due {
		arena.cuesFired[cue] = true
		if cue.Sound != "" {
			arena.PlaySound(soundFileName(cue.Sound))
		}
		if cue.ID != "" {
			arena.runTriggers(scoring.TriggerEvent{Cue: cue.ID})
		}
		arena.Bus.Publish("match.cue", map[string]any{"cue": cue.ID, "sound": cue.Sound, "phase": phase.ID})
		if cue.ID == companionEndgameCueId {
			go arena.CompanionClient.SendEvent(partner.EventEndgameStart)
		}
	}
}

// soundFileName returns the name of the audio file, without its extension, that is played for a cue's sound.
func soundFileName(sound string) string {
	if file, ok := soundAliases[sound]; ok {
		return file
	}
	return sound
}

// MatchSounds returns the name of every sound the arena can ask a display to play, without file extensions: the
// ones the active game's cues use, then the ones the arena itself plays outside the flow of a match. Displays
// preload them.
func (arena *Arena) MatchSounds() []string {
	var sounds []string
	seen := make(map[string]bool)
	add := func(sound string) {
		if !seen[sound] {
			seen[sound] = true
			sounds = append(sounds, sound)
		}
	}
	for _, phase := range arena.Game.Match.Phases {
		for _, cue := range phase.Cues {
			if cue.Sound != "" {
				add(soundFileName(cue.Sound))
			}
		}
	}
	for _, sound := range []string{"abort", "match_result", "pick_clock", "pick_clock_expired", "field_reset"} {
		add(sound)
	}
	return sounds
}

// endMatch stops the clock, either because the last phase ran out or because the match was aborted.
func (arena *Arena) endMatch(aborted bool) {
	arena.endPhaseIndex = arena.PhaseIndex
	if aborted {
		arena.matchEndTimeSec = arena.MatchTimeSec()
		arena.matchStopTime = time.Now()
	} else {
		// The match ended when its time ran out, which is a moment before the arena loop noticed.
		arena.matchEndTimeSec = arena.Game.Duration()
		arena.matchStopTime = arena.MatchStartTime.Add(time.Duration(arena.matchEndTimeSec * float64(time.Second)))
	}
	arena.MatchState = PostMatch
	arena.PhaseIndex = -1
	arena.matchAborted = aborted

	// A timer nobody stopped would otherwise never count the time it ran.
	arena.stopRunningTimers()

	eventType := "match.ended"
	if aborted {
		eventType = "match.aborted"
	}
	arena.Bus.Publish(eventType, map[string]any{"matchTime": arena.matchEndTimeSec})
}
