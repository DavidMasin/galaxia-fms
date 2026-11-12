// Turns an edited raw score state into audited score changes, and describes the changes of a result for the audit
// log. Used by the result editor of the match review page. All of it works from the game definition a result was
// played with.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"strconv"
	"strings"
)

// Who the audit log says made the changes that come from the result editor.
var matchReviewSource = scoring.Source{Kind: scoring.SourceAdmin, ID: "match_review", Name: "Match review"}

// auditLogEntry is one change of a result as the audit log shows it.
type auditLogEntry struct {
	Seq         int
	Time        string
	Source      string
	Phase       string
	Bucket      string
	MatchTime   string
	Description string
	Reverts     int  // The change this record undoes, or zero if it is not an undo record.
	RevertedBy  int  // The record that undid this change, or zero if nothing did.
	InEffect    bool // Whether the change counts towards the raw state. Always false for an undo record.
}

// stateChanges returns the changes that turn the raw state "from" into the raw state "to", in the order they should
// be applied. Anything "to" leaves out is taken as unchanged, so a browser can send back only what it understands.
//
// A counter is set phase by phase. A boolean or an enum is set in the phase "to" names for it, if that is a phase
// of the game. A foul whose type, rule or robot changed is removed and added again, keeping its phase.
func stateChanges(g *gamedef.Game, from, to *scoring.State, source scoring.Source) []scoring.Change {
	var changes []scoring.Change
	add := func(change scoring.Change) {
		change.Source = source
		changes = append(changes, change)
	}
	phaseOf := func(phase string) string {
		if g.Phase(phase) == nil {
			return ""
		}
		return phase
	}

	valueChanges := func(element *gamedef.Element, allianceId string, robot int, old, new *scoring.Value) {
		if old == nil || new == nil {
			return
		}
		change := scoring.Change{Alliance: allianceId, Robot: robot, Element: element.ID, Op: scoring.OpSet}
		switch element.Type {
		case gamedef.TypeCounter:
			for _, phase := range g.Match.Phases {
				if new.Counts[phase.ID] != old.Counts[phase.ID] {
					change.Value, change.Bucket = float64(new.Counts[phase.ID]), phase.ID
					add(change)
				}
			}
		case gamedef.TypeBoolean:
			phase := phaseOf(new.Phase)
			if new.Bool != old.Bool || (new.Bool && phase != "" && phase != old.Phase) {
				change.Value, change.Bucket = new.Bool, phase
				add(change)
			}
		case gamedef.TypeEnum:
			phase := phaseOf(new.Phase)
			if new.Option != old.Option || (phase != "" && phase != old.Phase) {
				change.Value, change.Bucket = new.Option, phase
				add(change)
			}
		case gamedef.TypeTimer:
			if new.Seconds != old.Seconds {
				change.Value = new.Seconds
				add(change)
			}
		case gamedef.TypeNumber:
			if new.Number != old.Number {
				change.Value = new.Number
				add(change)
			}
		}
	}

	for _, element := range g.Elements {
		if element.Scope == gamedef.ScopeField {
			valueChanges(element, "", 0, from.Field[element.ID], to.Field[element.ID])
			continue
		}
		for _, alliance := range g.Alliances.List {
			oldAlliance, newAlliance := from.Alliances[alliance.ID], to.Alliances[alliance.ID]
			if oldAlliance == nil || newAlliance == nil {
				continue
			}
			if element.Scope == gamedef.ScopeAlliance {
				valueChanges(
					element, alliance.ID, 0, oldAlliance.Elements[element.ID], newAlliance.Elements[element.ID],
				)
				continue
			}
			for i := range oldAlliance.Robots {
				if i < len(newAlliance.Robots) {
					valueChanges(
						element, alliance.ID, i+1, oldAlliance.Robots[i][element.ID], newAlliance.Robots[i][element.ID],
					)
				}
			}
		}
	}

	for _, alliance := range g.Alliances.List {
		oldAlliance, newAlliance := from.Alliances[alliance.ID], to.Alliances[alliance.ID]
		if oldAlliance == nil || newAlliance == nil {
			continue
		}

		// Fouls. A nil list means the browser sent none, which is different from sending an empty one.
		if newAlliance.Fouls != nil {
			oldFouls := make(map[int]scoring.Foul, len(oldAlliance.Fouls))
			for _, foul := range oldAlliance.Fouls {
				oldFouls[foul.ID] = foul
			}
			removal := func(foulId int) scoring.Change {
				return scoring.Change{
					Alliance: alliance.ID, Element: scoring.TargetFoul, Op: scoring.OpRemove, Value: float64(foulId),
				}
			}
			kept := make(map[int]bool, len(oldAlliance.Fouls)) // Fouls of "from" that "to" still lists.
			var removals, additions []scoring.Change
			for _, foul := range newAlliance.Fouls {
				addition := scoring.Change{
					Alliance: alliance.ID, Robot: foul.Robot, Element: scoring.TargetFoul, Op: scoring.OpAdd,
					Value: foul.Type, Rule: foul.Rule, Bucket: phaseOf(foul.Phase),
				}
				if old, ok := oldFouls[foul.ID]; ok && !kept[foul.ID] {
					kept[foul.ID] = true
					if old.Type == foul.Type && old.Rule == foul.Rule && old.Robot == foul.Robot {
						continue
					}
					// A corrected foul is removed and added again, in the phase it was called in.
					removals = append(removals, removal(foul.ID))
					addition.Bucket = phaseOf(old.Phase)
				}
				additions = append(additions, addition)
			}
			for _, foul := range oldAlliance.Fouls {
				if !kept[foul.ID] {
					removals = append(removals, removal(foul.ID))
				}
			}
			for _, change := range append(removals, additions...) {
				add(change)
			}
		}

		// Cards, by robot position.
		if newAlliance.Cards != nil {
			for position := 1; position <= len(oldAlliance.Robots); position++ {
				if newAlliance.Cards[position] != oldAlliance.Cards[position] {
					add(scoring.Change{
						Alliance: alliance.ID, Robot: position, Element: scoring.TargetCard, Op: scoring.OpSet,
						Value: newAlliance.Cards[position],
					})
				}
			}
		}

		if newAlliance.Disqualified != oldAlliance.Disqualified {
			add(scoring.Change{
				Alliance: alliance.ID, Element: scoring.TargetDisqualified, Op: scoring.OpSet,
				Value: newAlliance.Disqualified,
			})
		}
	}
	return changes
}

// buildAuditLog lists every change of a result, oldest first, saying for each whether it is still in effect.
func buildAuditLog(g *gamedef.Game, matchResult *model.MatchResult) []auditLogEntry {
	inEffect := make(map[int]bool)
	for _, change := range scoring.Live(matchResult.Changes) {
		inEffect[change.Seq] = true
	}
	// The latest record that undoes each change. A change can be undone once, so there is at most one.
	revertedBy := make(map[int]int)
	for _, change := range matchResult.Changes {
		if change.Reverts != 0 {
			revertedBy[change.Reverts] = change.Seq
		}
	}

	entries := make([]auditLogEntry, 0, len(matchResult.Changes))
	for _, change := range matchResult.Changes {
		entry := auditLogEntry{
			Seq: change.Seq, Time: change.Time.Local().Format("Jan 2 15:04:05"), Source: describeSource(change.Source),
			Phase: phaseName(g, change.Phase), Bucket: phaseName(g, change.Bucket),
			MatchTime:   strconv.FormatFloat(change.MatchTime, 'f', 1, 64),
			Description: describeChange(g, &change, matchResult.Teams), Reverts: change.Reverts,
			RevertedBy: revertedBy[change.Seq], InEffect: change.Reverts == 0 && inEffect[change.Seq],
		}
		entries = append(entries, entry)
	}
	return entries
}

// phaseName returns the name of a phase for the audit log. The phase recorded after the match and the absence of
// one are not phases of any game and are shown as they are.
func phaseName(g *gamedef.Game, phaseId string) string {
	if phase := g.Phase(phaseId); phase != nil && phase.Name != "" {
		return phase.Name
	}
	return phaseId
}

func describeSource(source scoring.Source) string {
	description := source.Kind
	switch {
	case source.Name != "":
		description += ": " + source.Name
	case source.ID != "":
		description += ": " + source.ID
	}
	return description
}

// describeChange says in words what a change did.
func describeChange(g *gamedef.Game, change *scoring.Change, teams map[string][]int) string {
	var target []string
	if alliance := g.Alliance(change.Alliance); alliance != nil {
		target = append(target, alliance.Name)
	}
	if change.Robot > 0 {
		robot := "robot " + strconv.Itoa(change.Robot)
		if allianceTeams := teams[change.Alliance]; change.Robot <= len(allianceTeams) &&
			allianceTeams[change.Robot-1] != 0 {
			robot += fmt.Sprintf(" (team %d)", allianceTeams[change.Robot-1])
		}
		target = append(target, robot)
	}

	var action string
	switch change.Element {
	case scoring.TargetFoul:
		if change.Op == scoring.OpRemove {
			action = fmt.Sprintf("foul #%v removed", change.Value)
			break
		}
		typeName := fmt.Sprint(change.Value)
		if foulType := g.FoulType(typeName); foulType != nil {
			typeName = foulType.Name
		}
		action = typeName + " called"
		if change.Rule != "" {
			action += " (" + change.Rule + ")"
		}
	case scoring.TargetCard:
		cardId, _ := change.Value.(string)
		if cardId == "" {
			action = "card cleared"
		} else if card := g.Card(cardId); card != nil {
			action = card.Name + " given"
		} else {
			action = "card " + cardId + " given"
		}
	case scoring.TargetDisqualified:
		if disqualified, _ := change.Value.(bool); disqualified {
			action = "alliance disqualified"
		} else {
			action = "alliance disqualification lifted"
		}
	default:
		name := change.Element
		element := g.Element(change.Element)
		if element != nil {
			name = element.Name
		}
		value := fmt.Sprint(change.Value)
		if element != nil && element.Type == gamedef.TypeEnum {
			if option := element.Option(value); option != nil {
				value = option.Name
			}
		}
		switch change.Op {
		case scoring.OpIncrement, scoring.OpDecrement:
			sign := "+"
			if change.Op == scoring.OpDecrement {
				sign = "-"
			}
			if change.Value == nil {
				value = "1"
			}
			action = name + " " + sign + value
		case scoring.OpSet:
			action = name + " set to " + value
		case scoring.OpToggle:
			action = name + " toggled"
		case scoring.OpTimerStart:
			action = name + " started"
		case scoring.OpTimerStop:
			action = name + " stopped"
		default:
			action = name + " " + change.Op
		}
	}
	if change.Reverts != 0 {
		action = fmt.Sprintf("Undo of #%d (%s)", change.Reverts, action)
	}
	target = append(target, action)
	return strings.Join(target, " · ")
}
