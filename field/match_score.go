// The live score of the match on the field: the audited list of changes, the raw state they fold to, and what the
// active game makes of it.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"log"
	"reflect"
	"time"
)

// MatchScore is the score of the match that is loaded. Only raw state is kept; Result is recomputed from it after
// every change and never edited.
type MatchScore struct {
	Changes []scoring.Change    // Every change, including undo records. The audit log.
	State   *scoring.State      // scoring.Fold(Changes): raw values only.
	Result  *scoring.Result     // scoring.Compute(State): points, breakdown, winner, ranking points.
	Live    *scoring.LiveValues // The game's live values at the last tick.
}

// ScoreChangeMessage is what scoring and referee panels are told about each change, so they can offer to undo it.
type ScoreChangeMessage struct {
	Change scoring.Change `json:"change"`
	Undone bool           `json:"undone"`
}

// resetScore gives the loaded match an empty score for the active game.
func (arena *Arena) resetScore() {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	arena.Score = &MatchScore{Changes: []scoring.Change{}, State: scoring.NewState(arena.Game)}
	arena.foulsCommitted = make(map[string]bool, len(arena.Game.Alliances.List))
	arena.liveErrorLogged = false
	if err := arena.recomputeScore(); err != nil {
		// Scoring an empty state can only fail if an expression of the game fails on it. The match can still be
		// played and scored by hand, so this is reported rather than fatal.
		log.Printf("Failed to compute the starting score of game %q: %v", arena.Game.ID, err)
	}
}

// scoringOptions returns the circumstances the loaded match is scored under.
func (arena *Arena) scoringOptions() scoring.Options {
	return scoring.Options{
		Params: arena.gameParams, Playoff: arena.CurrentMatch.UseTiebreakCriteria, Teams: arena.matchTeams(),
	}
}

// recomputeScore works out the result and the live values from the raw state. The caller holds the score lock. If
// the game's expressions fail, the previous result is kept and the error returned, so that one bad expression
// cannot blank the displays in the middle of a match.
func (arena *Arena) recomputeScore() error {
	result, err := model.ComputeResult(arena.Game, arena.Score.State, arena.CurrentMatch.Type, arena.scoringOptions())
	if err != nil {
		return err
	}
	live, err := result.Live(arena.clock())
	if err != nil {
		return err
	}
	arena.Score.Result = result
	arena.Score.Live = live
	return nil
}

// updateLiveValues re-evaluates the game's live values against the clock and tells browsers if any of them
// changed. The arena loop calls it on every pass, so a value that depends on the clock is never stale by more than
// one pass.
func (arena *Arena) updateLiveValues() {
	if len(arena.Game.Live) == 0 {
		return
	}
	arena.scoreMutex.Lock()
	changed := false
	if arena.Score.Result != nil {
		live, err := arena.Score.Result.Live(arena.clock())
		if err != nil {
			if !arena.liveErrorLogged {
				log.Printf("Failed to evaluate the live values of game %q: %v", arena.Game.ID, err)
				arena.liveErrorLogged = true
			}
		} else {
			changed = !reflect.DeepEqual(live, arena.Score.Live)
			arena.Score.Live = live
		}
	}
	arena.scoreMutex.Unlock()

	if changed {
		arena.RealtimeScoreNotifier.Notify()
	}
}

// ApplyScoreChange makes one change to the score of the loaded match. It fills in the change's Seq, Time, Phase,
// MatchTime and Bucket, checks that its source is allowed to make it now, applies it, recomputes the result, tells
// browsers and publishes score.changed. If the change is refused, nothing is recorded and the error says why. It
// takes the score lock itself and may be called from any goroutine.
func (arena *Arena) ApplyScoreChange(change scoring.Change) (scoring.Change, error) {
	arena.scoreMutex.Lock()
	change, err := arena.applyScoreChange(change)
	arena.scoreMutex.Unlock()
	if err != nil {
		return change, err
	}

	arena.RealtimeScoreNotifier.Notify()
	arena.ScoreChangeNotifier.NotifyWithMessage(ScoreChangeMessage{Change: change})
	arena.Bus.Publish("score.changed", change)
	return change, nil
}

// applyScoreChange is ApplyScoreChange without the notifications. The caller holds the score lock.
func (arena *Arena) applyScoreChange(change scoring.Change) (scoring.Change, error) {
	change.Seq = model.NextChangeSeq(arena.Score.Changes)
	change.Time = time.Now().UTC()
	change.Reverts = 0
	if err := arena.stampChange(&change); err != nil {
		return change, err
	}
	if err := arena.checkChangeAllowed(&change, change.Source.Kind); err != nil {
		return change, err
	}
	if err := arena.Score.State.Apply(arena.Game, &change); err != nil {
		return change, err
	}
	arena.Score.Changes = append(arena.Score.Changes, change)
	if err := arena.recomputeScore(); err != nil {
		log.Printf("Failed to recompute the score after change %d: %v", change.Seq, err)
	}
	return change, nil
}

// UndoScoreChange takes back the change with the given sequence number: it appends a change that reverts it and
// rebuilds the raw state without it. Undoing an undo brings the original change back. The source must be allowed
// to make the change it is undoing. It takes the score lock itself and may be called from any goroutine.
func (arena *Arena) UndoScoreChange(seq int, source scoring.Source) (scoring.Change, error) {
	arena.scoreMutex.Lock()
	revert, affected, undone, err := arena.undoScoreChange(seq, source)
	arena.scoreMutex.Unlock()
	if err != nil {
		return revert, err
	}

	arena.RealtimeScoreNotifier.Notify()
	arena.ScoreChangeNotifier.NotifyWithMessage(ScoreChangeMessage{Change: affected, Undone: undone})
	arena.Bus.Publish("score.undone", revert)
	return revert, nil
}

// undoScoreChange is UndoScoreChange without the notifications. The caller holds the score lock. Besides the
// reverting change it returns the scoring change whose effect was switched, and whether it is now undone.
func (arena *Arena) undoScoreChange(
	seq int, source scoring.Source,
) (revert scoring.Change, affected scoring.Change, undone bool, err error) {
	revert, err = model.NewRevert(arena.Score.Changes, seq, source)
	if err != nil {
		return
	}

	// Follow the chain of undo records down to the change that actually scored something. Each link flips whether
	// that change is in effect.
	bySeq := make(map[int]scoring.Change, len(arena.Score.Changes))
	reverted := make(map[int]bool)
	for _, change := range arena.Score.Changes {
		bySeq[change.Seq] = change
		if change.Reverts != 0 {
			reverted[change.Reverts] = true
		}
	}
	if reverted[seq] {
		err = fmt.Errorf("score change %d has already been undone", seq)
		return
	}
	affected, undone = bySeq[seq], true
	for affected.Reverts != 0 {
		affected, undone = bySeq[affected.Reverts], !undone
	}

	revert.Phase, revert.MatchTime = arena.changePhase()
	if err = arena.checkChangeAllowed(&affected, source.Kind); err != nil {
		return
	}
	arena.Score.Changes = append(arena.Score.Changes, revert)
	arena.Score.State, _ = scoring.Fold(arena.Game, arena.Score.Changes)
	if computeErr := arena.recomputeScore(); computeErr != nil {
		log.Printf("Failed to recompute the score after undoing change %d: %v", seq, computeErr)
	}
	return
}

// changePhase returns the phase and match time a change made right now is recorded with.
func (arena *Arena) changePhase() (phase string, matchTimeSec float64) {
	switch arena.MatchState {
	case InMatch:
		return arena.CurrentPhase().ID, arena.MatchTimeSec()
	case PostMatch:
		// The clock stopped when the match did.
		return scoring.PhasePost, arena.matchEndTimeSec
	}
	return "", 0
}

// stampChange fills in the phase the change happened in, the match time, and the phase it is credited to.
func (arena *Arena) stampChange(change *scoring.Change) error {
	g := arena.Game
	change.Phase, change.MatchTime = arena.changePhase()
	kind := change.Source.Kind
	element := g.Element(change.Element)
	requested := change.Bucket
	change.Bucket = ""

	switch arena.MatchState {
	case InMatch:
		// While the match runs, the clock decides what a panel's or a device's input is credited to; otherwise
		// naming a phase would be a way around the phases an element accepts changes in. Sources that are not
		// restricted may name one, to correct an earlier phase.
		if !restrictedSource(kind) && requested != "" {
			change.Bucket = requested
			break
		}
		// In the grace window after a phase ends, what was already on its way still counts for that phase: a
		// sensor reports late, and a scorer finishes entering what only that phase accepted.
		if ended := arena.gracePhase(); ended != nil && element != nil && element.AcceptsPhase(ended.ID) &&
			(kind == scoring.SourceDevice || !element.AcceptsPhase(change.Phase)) {
			change.Bucket = ended.ID
		}
	case PostMatch:
		if kind == scoring.SourceDevice {
			if arena.endPhaseIndex < 0 || time.Since(arena.matchStopTime).Seconds() >= g.Match.ScoreGrace {
				return fmt.Errorf("the match is over; input from %s is no longer counted", sourceName(change.Source))
			}
			change.Bucket = g.Match.Phases[arena.endPhaseIndex].ID
			break
		}
		change.Bucket = requested
		if element != nil && element.Type == gamedef.TypeCounter && requested == "" {
			return fmt.Errorf(
				"the match is over, and %s is counted per phase: say which phase this change is credited to",
				element.ID,
			)
		}
	default:
		change.Bucket = requested
	}

	if change.Bucket != "" && g.Phase(change.Bucket) == nil {
		return fmt.Errorf("%q is not a phase of this game", change.Bucket)
	}
	if change.Bucket == change.Phase {
		change.Bucket = ""
	}
	return nil
}

// checkChangeAllowed decides whether a source of the given kind may make the given (already stamped) change in the
// current state of the match.
func (arena *Arena) checkChangeAllowed(change *scoring.Change, kind string) error {
	switch kind {
	case scoring.SourceDevice, scoring.SourceScorer, scoring.SourceReferee, scoring.SourceAdmin, scoring.SourceRule,
		scoring.SourceAPI:
	default:
		return fmt.Errorf("%q is not a kind of source that can change the score", kind)
	}

	if arena.MatchState != InMatch && arena.MatchState != PostMatch && kind != scoring.SourceAdmin {
		return fmt.Errorf("the match has not started; nothing can be scored yet")
	}

	// Penalties are the referees' call, whatever else a panel or device may do.
	switch change.Element {
	case scoring.TargetFoul, scoring.TargetCard, scoring.TargetDisqualified:
		switch kind {
		case scoring.SourceReferee, scoring.SourceAdmin, scoring.SourceAPI:
			return nil
		}
		return fmt.Errorf("a %s source cannot change fouls or cards; that takes a referee", kind)
	}

	if !restrictedSource(kind) {
		return nil
	}
	element := arena.Game.Element(change.Element)
	if element == nil {
		return fmt.Errorf("there is no element %q", change.Element)
	}
	if arena.MatchState == PostMatch && kind != scoring.SourceDevice {
		// Once the match is over the people at the panels put the score right, whatever the element.
		return nil
	}
	if !element.EditableBy(kind) {
		return fmt.Errorf("%s cannot be changed by a %s during the match", element.ID, kind)
	}
	creditedTo := change.Bucket
	if creditedTo == "" {
		creditedTo = change.Phase
	}
	if !element.AcceptsPhase(creditedTo) {
		return fmt.Errorf("%s does not accept changes during the %s phase", element.ID, creditedTo)
	}
	return nil
}

// restrictedSource reports whether a kind of source is held to an element's edit list and phases during a match.
func restrictedSource(kind string) bool {
	return kind == scoring.SourceDevice || kind == scoring.SourceScorer || kind == scoring.SourceReferee
}

func sourceName(source scoring.Source) string {
	switch {
	case source.Name != "":
		return source.Name
	case source.ID != "":
		return source.ID
	}
	return "a " + source.Kind
}

// runTriggers applies the changes the game's triggers make at the given moment of the match.
func (arena *Arena) runTriggers(event scoring.TriggerEvent) {
	if len(arena.Game.Triggers) == 0 {
		return
	}
	arena.scoreMutex.Lock()
	changes, err := scoring.TriggerChanges(arena.Game, arena.Score.State, arena.scoringOptions(), event)
	arena.scoreMutex.Unlock()
	if err != nil {
		log.Printf("Failed to evaluate the triggers of game %q: %v", arena.Game.ID, err)
		return
	}
	for _, change := range changes {
		if _, err := arena.ApplyScoreChange(change); err != nil {
			log.Printf("Failed to apply a trigger of game %q to %s: %v", arena.Game.ID, change.Element, err)
		}
	}
}

// stopRunningTimers stops every timer element that is still counting, as the game's own rule would.
func (arena *Arena) stopRunningTimers() {
	source := scoring.Source{Kind: scoring.SourceRule, ID: "match-end", Name: "End of match"}
	var changes []scoring.Change
	arena.scoreMutex.Lock()
	for _, element := range arena.Game.Elements {
		if element.Type != gamedef.TypeTimer {
			continue
		}
		stop := scoring.Change{Element: element.ID, Op: scoring.OpTimerStop, Source: source}
		if element.Scope == gamedef.ScopeField {
			if arena.Score.State.Field[element.ID].Running {
				changes = append(changes, stop)
			}
			continue
		}
		for _, alliance := range arena.Game.Alliances.List {
			allianceState := arena.Score.State.Alliances[alliance.ID]
			stop.Alliance = alliance.ID
			if element.Scope == gamedef.ScopeAlliance {
				if allianceState.Elements[element.ID].Running {
					changes = append(changes, stop)
				}
				continue
			}
			for i, robot := range allianceState.Robots {
				if robot[element.ID].Running {
					stop.Robot = i + 1
					changes = append(changes, stop)
				}
			}
		}
	}
	arena.scoreMutex.Unlock()

	for _, change := range changes {
		if _, err := arena.ApplyScoreChange(change); err != nil {
			log.Printf("Failed to stop timer %s at the end of the match: %v", change.Element, err)
		}
	}
}

// BuildMatchResult returns the result of the loaded match as it stands, ready to be stored: the change list, the
// raw state, and everything needed to recompute it later (the game snapshot, which this saves, the parameters and
// the teams). The returned result shares nothing with the live score.
func (arena *Arena) BuildMatchResult() (*model.MatchResult, error) {
	if err := arena.Database.SaveGameSnapshot(arena.Game); err != nil {
		return nil, err
	}

	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	matchResult := model.NewMatchResult(arena.Game)
	matchResult.MatchId = arena.CurrentMatch.Id
	matchResult.MatchType = arena.CurrentMatch.Type
	for id, value := range arena.gameParams {
		matchResult.Params[id] = value
	}
	matchResult.Teams = arena.matchTeams()
	matchResult.Changes = append(matchResult.Changes, arena.Score.Changes...)
	matchResult.State, _ = scoring.Fold(arena.Game, matchResult.Changes)
	return matchResult, nil
}

// PublishMatchCommitted announces on the event bus that the result of a match was committed, or, if edited is true,
// that a committed result was changed afterwards.
func (arena *Arena) PublishMatchCommitted(match *model.Match, result *scoring.Result, edited bool) {
	scores := make(map[string]float64, len(result.Alliances))
	for allianceId, allianceResult := range result.Alliances {
		scores[allianceId] = allianceResult.Score
	}
	eventType := "match.committed"
	if edited {
		eventType = "match.result_edited"
	}
	arena.Bus.Publish(eventType, map[string]any{"id": match.Id, "winner": result.Winner, "scores": scores})
}

// RefereeScoreReady reports whether the referees have confirmed the fouls of every alliance.
func (arena *Arena) RefereeScoreReady() bool {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	for _, alliance := range arena.Game.Alliances.List {
		if !arena.foulsCommitted[alliance.ID] {
			return false
		}
	}
	return true
}

// FoulsCommitted reports whether the referees have confirmed the fouls called against the given alliance.
func (arena *Arena) FoulsCommitted(allianceId string) bool {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	return arena.foulsCommitted[allianceId]
}

// SetFoulsCommitted records whether the referees have confirmed the fouls called against the given alliance.
func (arena *Arena) SetFoulsCommitted(allianceId string, committed bool) error {
	if arena.AllianceIndex(allianceId) < 0 {
		return fmt.Errorf("there is no alliance %q", allianceId)
	}
	arena.scoreMutex.Lock()
	arena.foulsCommitted[allianceId] = committed
	arena.scoreMutex.Unlock()
	arena.ScoringStatusNotifier.Notify()
	return nil
}

// evalLive evaluates one of the game expressions that are compiled against the live match (the team sign text, the
// driver station game data) for an alliance, against the current score and clock.
func (arena *Arena) evalLive(expression *gamedef.Expr, allianceId string, extra map[string]any) (any, error) {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	if arena.Score.Result == nil {
		return nil, fmt.Errorf("the score could not be computed")
	}
	return arena.Score.Result.Eval(expression.Prog, allianceId, arena.clock(), extra)
}

// ScoreSnapshot returns the live score for reading: the result, the live values and the sequence number of the
// last change. A new result and new live values are made after each change, and their exported fields are never
// modified afterwards, so the caller may keep reading those.
func (arena *Arena) ScoreSnapshot() (result *scoring.Result, live *scoring.LiveValues, seq int) {
	arena.scoreMutex.Lock()
	defer arena.scoreMutex.Unlock()

	if changes := arena.Score.Changes; len(changes) > 0 {
		seq = changes[len(changes)-1].Seq
	}
	return arena.Score.Result, arena.Score.Live, seq
}
