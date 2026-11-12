// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Model and datastore CRUD methods for the results (score and fouls) from a match at an event.

package model

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"time"
)

// MatchResult is what happened in one play of a match: the audited list of score changes and the raw state they
// fold to. It never holds a total. Every number shown to a human is recomputed from it with the game definition
// the match was played with, which is kept as a GameSnapshot.
type MatchResult struct {
	Id         int `db:"id"`
	MatchId    int
	PlayNumber int
	MatchType  MatchType
	GameId     string
	GameHash   string             // Identifies the GameSnapshot the match was played with.
	Params     map[string]float64 // Event parameter overrides in force.
	Teams      map[string][]int   // Alliance id -> team number by robot position.
	Changes    []scoring.Change   // The audit log.
	State      *scoring.State     // Fold of Changes. Raw values, never totals.
}

// NewMatchResult returns an empty result for a match of the given game: nothing scored, no fouls, no cards.
func NewMatchResult(g *gamedef.Game) *MatchResult {
	matchResult := &MatchResult{
		GameId:   g.ID,
		GameHash: g.Hash,
		Params:   make(map[string]float64),
		Teams:    make(map[string][]int, len(g.Alliances.List)),
		Changes:  []scoring.Change{},
		State:    scoring.NewState(g),
	}
	for _, alliance := range g.Alliances.List {
		matchResult.Teams[alliance.ID] = make([]int, g.Alliances.Size)
	}
	return matchResult
}

func (database *Database) CreateMatchResult(matchResult *MatchResult) error {
	return database.matchResultTable.create(matchResult)
}

func (database *Database) GetMatchResultForMatch(matchId int) (*MatchResult, error) {
	matchResults, err := database.matchResultTable.getAll()
	if err != nil {
		return nil, err
	}

	var mostRecentMatchResult *MatchResult
	for i, matchResult := range matchResults {
		if matchResult.MatchId == matchId &&
			(mostRecentMatchResult == nil || matchResult.PlayNumber > mostRecentMatchResult.PlayNumber) {
			mostRecentMatchResult = &matchResults[i]
		}
	}
	return mostRecentMatchResult, nil
}

func (database *Database) UpdateMatchResult(matchResult *MatchResult) error {
	return database.matchResultTable.update(matchResult)
}

func (database *Database) DeleteMatchResult(id int) error {
	return database.matchResultTable.delete(id)
}

func (database *Database) TruncateMatchResults() error {
	return database.matchResultTable.truncate()
}

// Compute recomputes the points, breakdown, winner and ranking points of this result, using the game definition
// it was played with rather than whatever the game looks like today. playoff turns on the game's playoff
// tiebreakers; pass the match's UseTiebreakCriteria. It also returns the game, because nearly every caller needs
// its names to present the result.
func (matchResult *MatchResult) Compute(database *Database, playoff bool) (*scoring.Result, *gamedef.Game, error) {
	g, err := database.GetSnapshotGame(matchResult.GameHash)
	if err != nil {
		return nil, nil, err
	}
	state := matchResult.State
	if state == nil {
		state, _ = scoring.Fold(g, matchResult.Changes)
	}
	options := scoring.Options{Params: matchResult.Params, Playoff: playoff, Teams: matchResult.Teams}
	result, err := ComputeResult(g, state, matchResult.MatchType, options)
	if err != nil {
		return nil, nil, err
	}
	return result, g, nil
}

// ComputeResult computes a result from raw state for a match of the given type. It is scoring.Compute plus the one
// rule that depends on the kind of match: in a playoff match a card that disqualifies a team disqualifies its
// whole alliance, because a playoff alliance wins or loses as one.
func ComputeResult(
	g *gamedef.Game, state *scoring.State, matchType MatchType, options scoring.Options,
) (*scoring.Result, error) {
	if matchType == Playoff {
		state = disqualifyCardedAlliances(g, state)
	}
	return scoring.Compute(g, state, options)
}

// disqualifyCardedAlliances returns a copy of the state in which every alliance holding a disqualifying card is
// disqualified. The stored state is left alone, so removing the card later undoes the disqualification.
func disqualifyCardedAlliances(g *gamedef.Game, state *scoring.State) *scoring.State {
	adjusted := &scoring.State{
		Alliances: make(map[string]*scoring.AllianceState, len(state.Alliances)), Field: state.Field,
	}
	for id, allianceState := range state.Alliances {
		adjusted.Alliances[id] = allianceState
		if allianceState.Disqualified || len(DisqualifiedPositions(g, allianceState)) == 0 {
			continue
		}
		disqualified := *allianceState
		disqualified.Disqualified = true
		adjusted.Alliances[id] = &disqualified
	}
	return adjusted
}

// DisqualifiedPositions returns the robot positions (1..n) of an alliance that hold a card whose definition
// disqualifies the team from the match.
func DisqualifiedPositions(g *gamedef.Game, allianceState *scoring.AllianceState) []int {
	var positions []int
	for position := 1; position <= len(allianceState.Robots); position++ {
		if card := g.Card(allianceState.Cards[position]); card != nil && card.Disqualifies {
			positions = append(positions, position)
		}
	}
	return positions
}

// Edit appends a change made after the match was played, such as a correction from the match review page, and
// applies it to the raw state. The change is stamped with the next sequence number, the current time and the
// "post" phase; a change to a counter must therefore name the phase it is credited to in Bucket. If the change
// does not make sense for the game, the result is left untouched and the error says why.
func (matchResult *MatchResult) Edit(g *gamedef.Game, change scoring.Change) (scoring.Change, error) {
	if matchResult.State == nil {
		matchResult.State, _ = scoring.Fold(g, matchResult.Changes)
	}
	change.Seq = NextChangeSeq(matchResult.Changes)
	change.Time = time.Now().UTC()
	change.Phase = scoring.PhasePost
	change.Reverts = 0
	if err := matchResult.State.Apply(g, &change); err != nil {
		return change, err
	}
	matchResult.Changes = append(matchResult.Changes, change)
	return change, nil
}

// Undo appends a record that reverts the change with the given sequence number and rebuilds the raw state without
// it. Undoing an undo record brings the original change back.
func (matchResult *MatchResult) Undo(g *gamedef.Game, seq int, source scoring.Source) (scoring.Change, error) {
	revert, err := NewRevert(matchResult.Changes, seq, source)
	if err != nil {
		return revert, err
	}
	revert.Phase = scoring.PhasePost
	matchResult.Changes = append(matchResult.Changes, revert)
	matchResult.State, _ = scoring.Fold(g, matchResult.Changes)
	return revert, nil
}

// NextChangeSeq returns the sequence number for the next change appended to a log. Sequence numbers start at 1,
// because zero in Change.Reverts means "reverts nothing".
func NextChangeSeq(changes []scoring.Change) int {
	if len(changes) == 0 {
		return 1
	}
	return changes[len(changes)-1].Seq + 1
}

// NewRevert builds the record that undoes the change with the given sequence number, without appending it. It
// repeats what the reverted change targeted, so that the audit log reads sensibly on its own.
func NewRevert(changes []scoring.Change, seq int, source scoring.Source) (scoring.Change, error) {
	for _, target := range changes {
		if target.Seq == seq {
			return scoring.Change{
				Seq: NextChangeSeq(changes), Time: time.Now().UTC(), Alliance: target.Alliance, Robot: target.Robot,
				Element: target.Element, Op: target.Op, Value: target.Value, Rule: target.Rule, Bucket: target.Bucket,
				Reverts: seq, Source: source,
			}, nil
		}
	}
	return scoring.Change{}, fmt.Errorf("there is no score change with sequence number %d", seq)
}

// TeamCards returns the card held by each team of an alliance in this result, keyed by team number. Positions
// without a team or without a card are left out.
func (matchResult *MatchResult) TeamCards(allianceId string) map[int]string {
	cards := make(map[int]string)
	if matchResult.State == nil || matchResult.State.Alliances[allianceId] == nil {
		return cards
	}
	teams := matchResult.Teams[allianceId]
	for position, card := range matchResult.State.Alliances[allianceId].Cards {
		if card != "" && position >= 1 && position <= len(teams) && teams[position-1] != 0 {
			cards[teams[position-1]] = card
		}
	}
	return cards
}

// AllianceIds returns the ids of the alliances in this result that correspond to the red and blue sides of the
// match record: the first and second alliance of the game it was played with.
func (matchResult *MatchResult) AllianceIds(database *Database) (red, blue string, err error) {
	g, err := database.GetSnapshotGame(matchResult.GameHash)
	if err != nil {
		return "", "", err
	}
	if len(g.Alliances.List) != 2 {
		return "", "", fmt.Errorf(
			"game %q has %d alliances; a match record holds exactly two", g.ID, len(g.Alliances.List),
		)
	}
	return g.Alliances.List[0].ID, g.Alliances.List[1].ID, nil
}
