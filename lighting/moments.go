// Field moments: the rows of the scene grid, and which of them apply at any time.

package lighting

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"strings"
	"time"
)

// The moments every game has. A game adds cue.<id> for each of its cues that has an id and phase.<id> for each of
// its phases.
const (
	MomentEStop       = "estop"
	MomentFault       = "fault"
	MomentScorePosted = "score_posted"
	MomentMatchEnd    = "match_end"
	MomentFieldReset  = "field_reset"
	MomentVolunteers  = "volunteers"
	MomentTeamsReady  = "teams_ready"
	MomentAwards      = "awards"
	MomentIdle        = "idle"

	cuePrefix   = "cue."
	phasePrefix = "phase."
)

// How long a cue and a posted score hold their moment.
const (
	CueDuration         = 2 * time.Second
	ScorePostedDuration = 10 * time.Second
)

// Kinds of moment, for the page.
const (
	MomentKindField = "field"
	MomentKindCue   = "cue"
	MomentKindPhase = "phase"
)

// Moment is one row of the scene grid.
type Moment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	When string `json:"when"` // When it applies, in words.
}

// Moments returns every moment of a game, highest priority first: estop, fault, the cues, the phases,
// score_posted, match_end, field_reset, volunteers, teams_ready, awards and idle.
func Moments(game *gamedef.Game) []Moment {
	moments := []Moment{
		{ID: MomentEStop, Name: "E-stop", Kind: MomentKindField, When: "Any station E-stop or the field stop is active"},
		{ID: MomentFault, Name: "Device fault", Kind: MomentKindField, When: "A device fault is latched"},
	}
	if game != nil {
		seen := make(map[string]bool)
		for _, phase := range game.Match.Phases {
			for _, cue := range phase.Cues {
				if cue.ID == "" || seen[cue.ID] {
					continue
				}
				seen[cue.ID] = true
				moments = append(moments, Moment{
					ID: cuePrefix + cue.ID, Name: "Cue: " + displayName(cue.ID), Kind: MomentKindCue,
					When: fmt.Sprintf("For 2 seconds after the cue fires (%s, %s)", phase.Name, cueTime(cue)),
				})
			}
		}
		for _, phase := range game.Match.Phases {
			moments = append(moments, Moment{
				ID: phasePrefix + phase.ID, Name: phase.Name, Kind: MomentKindPhase,
				When: fmt.Sprintf("While %s runs (%g s)", phase.Name, phase.Duration),
			})
		}
	}
	return append(moments,
		Moment{
			ID: MomentScorePosted, Name: "Score posted", Kind: MomentKindField,
			When: "For 10 seconds after a score is posted",
		},
		Moment{
			ID: MomentMatchEnd, Name: "Match over", Kind: MomentKindField,
			When: "After the match, until the score is posted",
		},
		Moment{ID: MomentFieldReset, Name: "Field reset", Kind: MomentKindField, When: "Field reset signalled"},
		Moment{ID: MomentVolunteers, Name: "Volunteers", Kind: MomentKindField, When: "Volunteers signalled"},
		Moment{
			ID: MomentTeamsReady, Name: "Teams ready", Kind: MomentKindField,
			When: "Before the match, with every station ready",
		},
		Moment{
			ID: MomentAwards, Name: "Awards", Kind: MomentKindField, When: "The alliance station displays show the logo",
		},
		Moment{ID: MomentIdle, Name: "Idle", Kind: MomentKindField, When: "Otherwise"},
	)
}

// displayName turns an id such as "field_reset" into "Field reset".
func displayName(id string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(id))
	if len(words) == 0 {
		return id
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

// cueTime says when in its phase a cue fires.
func cueTime(cue *gamedef.Cue) string {
	switch {
	case cue.Remaining != nil:
		return fmt.Sprintf("%g s left", *cue.Remaining)
	case cue.Elapsed != nil:
		return fmt.Sprintf("after %g s", *cue.Elapsed)
	case cue.At == "end":
		return "at the end"
	}
	return "at the start"
}

// fieldMoments is what the engine keeps between output ticks to work out the moments: when each cue fired, when the
// last score was posted, and whether it was posted since the match ended. Only the arena goroutine touches it.
type fieldMoments struct {
	cueTimes       map[string]time.Time // By moment id.
	postedAt       time.Time
	postedSinceEnd bool
}

// FieldInfo is what the engine needs to know about the field beyond the device snapshot. The arena fills it in on
// its own goroutine for every output tick.
type FieldInfo struct {
	FieldStop  bool // The field stop is active, from the PLC or a device.
	TeamsReady bool // Every station the game uses is ready to start.
	Awards     bool // The alliance station displays show the logo.
}
