// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web handlers for the referee interface.

package web

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/DavidMasin/galaxia-fms/websocket"
	"github.com/mitchellh/mapstructure"
)

// Who the audit log says made the changes that come from the referee panel.
var refereeSource = scoring.Source{Kind: scoring.SourceReferee, ID: "referee", Name: "Referee"}

// refereeFoulOption is one choice offered for a property of a foul.
type refereeFoulOption struct {
	Id       string
	Name     string
	Text     string
	Selected bool
}

// refereeFoulTeam is one robot position a foul can be attributed to.
type refereeFoulTeam struct {
	Position int
	TeamId   int
	Selected bool
}

// refereeFoul is one foul of the loaded match as the referee panel lists it.
type refereeFoul struct {
	scoring.Foul
	Number        int // Counts the fouls of the alliance from 1.
	AllianceId    string
	AllianceIndex int
	TypeName      string
	Types         []refereeFoulOption
	Rules         []refereeFoulOption
	Teams         []refereeFoulTeam
}

// Renders the referee interface for assigning fouls.
func (web *Web) refereePanelHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	template, err := web.parseFiles("templates/referee_panel.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	data := struct {
		*model.EventSettings
	}{web.arena.EventSettings}
	err = template.ExecuteTemplate(w, "base_no_navbar", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Renders a partial template for when the foul list is updated.
func (web *Web) refereePanelFoulListHandler(w http.ResponseWriter, r *http.Request) {
	template, err := web.parseFiles("templates/referee_panel_foul_list.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	matchResult, err := web.arena.BuildMatchResult()
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		Fouls []refereeFoul
	}{buildRefereeFouls(web.arena.Game, matchResult)}
	err = template.ExecuteTemplate(w, "referee_panel_foul_list", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// buildRefereeFouls lists the fouls of a result, alliance by alliance, with the choices the game offers for each.
func buildRefereeFouls(g *gamedef.Game, matchResult *model.MatchResult) []refereeFoul {
	fouls := []refereeFoul{}
	for index, alliance := range g.Alliances.List {
		allianceState := matchResult.State.Alliances[alliance.ID]
		if allianceState == nil {
			continue
		}
		teams := matchResult.Teams[alliance.ID]
		for i, foul := range allianceState.Fouls {
			listed := refereeFoul{
				Foul: foul, Number: i + 1, AllianceId: alliance.ID, AllianceIndex: index, TypeName: foul.Type,
			}
			for _, foulType := range g.Fouls.Types {
				if foulType.ID == foul.Type {
					listed.TypeName = foulType.Name
				}
				listed.Types = append(
					listed.Types,
					refereeFoulOption{Id: foulType.ID, Name: foulType.Name, Selected: foulType.ID == foul.Type},
				)
			}
			for _, rule := range g.Fouls.Rules {
				if rule.Foul == foul.Type {
					listed.Rules = append(
						listed.Rules,
						refereeFoulOption{Id: rule.ID, Name: rule.Name, Text: rule.Text, Selected: rule.ID == foul.Rule},
					)
				}
			}
			for position := 1; position <= g.Alliances.Size; position++ {
				teamId := 0
				if position <= len(teams) {
					teamId = teams[position-1]
				}
				listed.Teams = append(
					listed.Teams,
					refereeFoulTeam{Position: position, TeamId: teamId, Selected: foul.Robot == position},
				)
			}
			fouls = append(fouls, listed)
		}
	}
	return fouls
}

// The websocket endpoint for the refereee interface client to send control commands and receive status updates.
func (web *Web) refereePanelWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	ws, err := websocket.NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer closeWebsocket(ws)

	// Tell the panel what has been scored so far, so that it can offer to undo it.
	web.writeScoreChangeHistory(ws)

	// Subscribe the websocket to the notifiers whose messages will be passed on to the client, in a separate goroutine.
	go ws.HandleNotifiers(
		web.arena.GameNotifier,
		web.arena.MatchLoadNotifier,
		web.arena.MatchTimeNotifier,
		web.arena.RealtimeScoreNotifier,
		web.arena.ScoreChangeNotifier,
		web.arena.ScoringStatusNotifier,
		web.arena.ReloadDisplaysNotifier,
		web.arena.ArenaStatusNotifier,
	)

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, data, err := ws.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Println(err)
			return
		}

		switch messageType {
		case "score", "undo":
			if err = web.handleScoreCommand(messageType, data, refereeSource); err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "foul":
			args := struct {
				Alliance string
				Robot    int
				Type     string
				Rule     string
				Remove   int
				Update   int
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			switch {
			case args.Remove != 0:
				err = web.removeFoul(args.Alliance, args.Remove)
			case args.Update != 0:
				err = web.updateFoul(args.Alliance, args.Update, args.Type, args.Rule, args.Robot)
			default:
				err = web.addFoul(args.Alliance, args.Type, args.Rule, args.Robot, "")
			}
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "card":
			args := struct {
				Alliance string
				Robot    int
				Card     string
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
			if err = web.giveCard(args.Alliance, args.Robot, args.Card); err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "toggleBypass":
			station, ok := data.(string)
			if !ok {
				writeWebsocketError(ws, fmt.Sprintf("Failed to parse '%s' message.", messageType))
				continue
			}
			err = web.arena.ToggleBypass(station)
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		case "signalVolunteers":
			web.arena.SignalVolunteers()
		case "signalReset":
			web.arena.SignalReset()
		case "commitAndPost":
			if web.arena.MatchState != field.PostMatch {
				// Don't allow committing the fouls until the match is over.
				continue
			}
			for _, alliance := range web.arena.Game.Alliances.List {
				if err = web.arena.SetFoulsCommitted(alliance.ID, true); err != nil {
					log.Println(err)
				}
			}

			err = web.commitPostAndLoadNextMatch()
			if err != nil {
				writeWebsocketError(ws, err.Error())
				continue
			}
		default:
			writeWebsocketError(ws, fmt.Sprintf("Invalid message type '%s'.", messageType))
		}
	}
}

// foulRule returns the rule a referee cited if the game's catalogue lists it for the given type of foul, and
// otherwise no rule: the same rule number can stand for different fouls, and a citation that does not go with the
// foul's type would be read as something the referee did not call.
func foulRule(g *gamedef.Game, foulType, rule string) string {
	for _, candidate := range g.Fouls.Rules {
		if candidate.ID == rule && candidate.Foul == foulType {
			return rule
		}
	}
	return ""
}

// addFoul calls a foul of the given type against an alliance. robot is the position of the offending robot, or zero
// if the foul is not attributed. phase, if not empty, is the phase the foul is recorded in; the arena only lets a
// referee name one once the match is over.
func (web *Web) addFoul(allianceId, foulType, rule string, robot int, phase string) error {
	_, err := web.arena.ApplyScoreChange(
		scoring.Change{
			Alliance: allianceId, Robot: robot, Element: scoring.TargetFoul, Op: scoring.OpAdd, Value: foulType,
			Rule: foulRule(web.arena.Game, foulType, rule), Bucket: phase, Source: refereeSource,
		},
	)
	return err
}

// removeFoul takes back the foul with the given id.
func (web *Web) removeFoul(allianceId string, foulId int) error {
	_, err := web.arena.ApplyScoreChange(
		scoring.Change{
			Alliance: allianceId, Element: scoring.TargetFoul, Op: scoring.OpRemove, Value: foulId,
			Source: refereeSource,
		},
	)
	return err
}

// updateFoul changes the type, the rule cited or the robot of a foul that has already been called. A foul is never
// modified in place, so that the audit log shows what was called and when it was corrected: the old foul is removed
// and the corrected one added, which gives it a new id.
func (web *Web) updateFoul(allianceId string, foulId int, foulType, rule string, robot int) error {
	matchResult, err := web.arena.BuildMatchResult()
	if err != nil {
		return err
	}
	allianceState := matchResult.State.Alliances[allianceId]
	if allianceState == nil {
		return fmt.Errorf("there is no alliance %q", allianceId)
	}
	var existing *scoring.Foul
	for i := range allianceState.Fouls {
		if allianceState.Fouls[i].ID == foulId {
			existing = &allianceState.Fouls[i]
		}
	}
	if existing == nil {
		return fmt.Errorf("there is no foul %d on alliance %s", foulId, allianceId)
	}
	if web.arena.Game.FoulType(foulType) == nil {
		return fmt.Errorf("there is no foul type %q", foulType)
	}

	removal, err := web.arena.ApplyScoreChange(
		scoring.Change{
			Alliance: allianceId, Element: scoring.TargetFoul, Op: scoring.OpRemove, Value: foulId,
			Source: refereeSource,
		},
	)
	if err != nil {
		return err
	}
	// The corrected foul stays in the phase it was called in where the arena allows a referee to say so.
	phase := ""
	if web.arena.Game.Phase(existing.Phase) != nil {
		phase = existing.Phase
	}
	if err = web.addFoul(allianceId, foulType, rule, robot, phase); err != nil {
		// Put the foul back as it was rather than lose it.
		if _, undoErr := web.arena.UndoScoreChange(removal.Seq, refereeSource); undoErr != nil {
			log.Printf("Failed to restore foul %d after a failed correction: %v", foulId, undoErr)
		}
		return err
	}
	return nil
}

// resolveCard works out which card a referee's request for the given card actually gives. current is the card the
// team already holds in this match and carried says whether it carries a card from an earlier match.
//
// A card whose definition names a second_becomes card turns into that card when it is given to a team that carries
// a card or already holds the same card in this match. A card that disqualifies cannot be a team's first: it can
// only be asked for directly for a team that already has a card, from this match or an earlier one.
func resolveCard(g *gamedef.Game, requested, current string, carried bool) (string, error) {
	if requested == "" {
		return "", nil
	}
	card := g.Card(requested)
	if card == nil {
		return "", fmt.Errorf("there is no card %q", requested)
	}
	if card.SecondBecomes != "" && (carried || current == card.ID) {
		return card.SecondBecomes, nil
	}
	if card.Disqualifies && current == "" && !carried {
		return "", fmt.Errorf("%s can only be given to a team that already has a card", card.Name)
	}
	return card.ID, nil
}

// giveCard gives a card to the robot at the given position of an alliance, or clears it if card is empty. In a
// playoff match an alliance wins or loses as one, so position zero gives the card to the alliance as a whole: to
// every one of its robot positions.
func (web *Web) giveCard(allianceId string, robot int, requested string) error {
	g := web.arena.Game
	allianceIndex := web.arena.AllianceIndex(allianceId)
	if allianceIndex < 0 {
		return fmt.Errorf("there is no alliance %q", allianceId)
	}
	isPlayoff := web.arena.CurrentMatch.Type == model.Playoff
	if robot == 0 && !isPlayoff {
		return fmt.Errorf("a card is given to a robot position; only in a playoff match can an alliance be given one")
	}
	if robot < 0 || robot > g.Alliances.Size {
		return fmt.Errorf("robot position %d is out of range", robot)
	}

	matchResult, err := web.arena.BuildMatchResult()
	if err != nil {
		return err
	}
	held := matchResult.State.Alliances[allianceId].Cards
	positions := []int{robot}
	current := held[robot]
	if robot == 0 {
		positions = positions[:0]
		for position := 1; position <= g.Alliances.Size; position++ {
			positions = append(positions, position)
			if current == "" {
				current = held[position]
			}
		}
	}

	card, err := resolveCard(g, requested, current, web.carriesCard(allianceIndex, positions[0]))
	if err != nil {
		return err
	}
	for _, position := range positions {
		if held[position] == card {
			continue
		}
		_, err = web.arena.ApplyScoreChange(
			scoring.Change{
				Alliance: allianceId, Robot: position, Element: scoring.TargetCard, Op: scoring.OpSet, Value: card,
				Source: refereeSource,
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// carriesCard reports whether the team at the given robot position (1..n) of the loaded match comes into it with a
// card from an earlier match. In the playoffs it is the alliance that carries a card, whichever of its teams was
// given it.
func (web *Web) carriesCard(allianceIndex, position int) bool {
	match := web.arena.CurrentMatch
	if match.Type == model.Playoff {
		playoffAllianceIds := []int{match.PlayoffRedAlliance, match.PlayoffBlueAlliance}
		if allianceIndex >= len(playoffAllianceIds) {
			return false
		}
		alliance, err := web.arena.Database.GetAllianceById(playoffAllianceIds[allianceIndex])
		if err != nil {
			log.Printf("Failed to read alliance %d: %v", playoffAllianceIds[allianceIndex], err)
			return false
		}
		return alliance != nil && alliance.YellowCard
	}

	stationIds := web.arena.AllianceStationIds(allianceIndex)
	if position < 1 || position > len(stationIds) {
		return false
	}
	team := web.arena.AllianceStations[stationIds[position-1]].Team
	return team != nil && team.YellowCard
}
