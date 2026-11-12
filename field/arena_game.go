// The arena's active game: which game definition the field is running, when it may change, and how the game's
// alliances map onto the two sides of the field.

package field

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"log"
	"maps"
	"time"
)

const (
	// The field has two sets of three stations, and match records, brackets and schedules have two sides of three
	// teams. Until those are generic, a game has to fit them.
	fieldAllianceCount   = 2
	fieldAllianceMaxSize = 3
)

// Station ids by alliance index and robot position. They name the physical stations of the field and the Red* and
// Blue* fields of a match record; a game's own alliance names and short codes do not change them.
var fieldStationIds = [fieldAllianceCount][fieldAllianceMaxSize]string{{"R1", "R2", "R3"}, {"B1", "B2", "B3"}}

// CheckGameSelectable returns nil if the given game can be played on this field, and otherwise an error that says
// what does not fit.
func CheckGameSelectable(g *gamedef.Game) error {
	if len(g.Alliances.List) != fieldAllianceCount || g.Alliances.Size > fieldAllianceMaxSize {
		return fmt.Errorf(
			"game %q has %d alliances of %d robots; for now only games with exactly two alliances of at most three "+
				"robots can be selected",
			g.ID, len(g.Alliances.List), g.Alliances.Size,
		)
	}
	return nil
}

// AllianceId returns the id of the active game's alliance at the given index, or the empty string if there is no
// such alliance. Index 0 occupies the Red* fields of a match record and stations R1..R3; index 1 the Blue* fields
// and B1..B3.
func (arena *Arena) AllianceId(index int) string {
	if arena.Game == nil || index < 0 || index >= len(arena.Game.Alliances.List) {
		return ""
	}
	return arena.Game.Alliances.List[index].ID
}

// AllianceIndex returns the index of the active game's alliance with the given id, or -1 if there is none.
func (arena *Arena) AllianceIndex(id string) int {
	if arena.Game == nil {
		return -1
	}
	for index, alliance := range arena.Game.Alliances.List {
		if alliance.ID == id {
			return index
		}
	}
	return -1
}

// AllianceStationIds returns the ids of the stations the given alliance of the active game plays from, by robot
// position. A game with fewer than three robots per alliance leaves the last stations unused.
func (arena *Arena) AllianceStationIds(allianceIndex int) []string {
	if arena.Game == nil || allianceIndex < 0 || allianceIndex >= fieldAllianceCount {
		return nil
	}
	return fieldStationIds[allianceIndex][:min(arena.Game.Alliances.Size, fieldAllianceMaxSize)]
}

// activeStationIds returns the ids of every station the active game uses, first alliance first.
func (arena *Arena) activeStationIds() []string {
	var stationIds []string
	for allianceIndex := range fieldStationIds {
		stationIds = append(stationIds, arena.AllianceStationIds(allianceIndex)...)
	}
	return stationIds
}

// stationPlace returns which alliance of the active game and which robot position (1..n) a station belongs to. ok
// is false for a station the active game does not use.
func (arena *Arena) stationPlace(stationId string) (allianceIndex, position int, ok bool) {
	for allianceIndex := range fieldStationIds {
		for i, id := range arena.AllianceStationIds(allianceIndex) {
			if id == stationId {
				return allianceIndex, i + 1, true
			}
		}
	}
	return 0, 0, false
}

// matchTeams returns the team number at each robot position of each alliance, as currently assigned to the
// stations. A position without a team holds zero.
func (arena *Arena) matchTeams() map[string][]int {
	teams := make(map[string][]int, len(arena.Game.Alliances.List))
	for allianceIndex, alliance := range arena.Game.Alliances.List {
		teams[alliance.ID] = make([]int, arena.Game.Alliances.Size)
		for i, stationId := range arena.AllianceStationIds(allianceIndex) {
			if team := arena.AllianceStations[stationId].Team; team != nil {
				teams[alliance.ID][i] = team.Id
			}
		}
	}
	return teams
}

// ScoringPanels returns the scoring panel positions of the active game: the ones its layout declares, or else one
// per alliance, named by the alliance id, plus "field" if the game has anything scored for the field as a whole.
func (arena *Arena) ScoringPanels() []*gamedef.Panel {
	if len(arena.Game.Layout.ScoringPanels) > 0 {
		return arena.Game.Layout.ScoringPanels
	}
	var panels []*gamedef.Panel
	for _, alliance := range arena.Game.Alliances.List {
		panels = append(panels, &gamedef.Panel{ID: alliance.ID, Name: alliance.Name, Alliances: []string{alliance.ID}})
	}
	for _, element := range arena.Game.Elements {
		if element.Scope == gamedef.ScopeField {
			panels = append(panels, &gamedef.Panel{ID: "field", Name: "Field"})
			break
		}
	}
	return panels
}

// refreshGame makes the game selected in the event settings, as it is on disk right now, the active game, together
// with the event's overrides of its parameters. A game belongs to a match from the moment the match starts until
// its result is committed, so outside PreMatch this only remembers that a refresh is due; Update and LoadMatch
// carry it out once the arena is back in PreMatch.
//
// A selection that cannot be used (missing, invalid, or not fitting the field) never replaces a working game: the
// error is returned and the arena keeps the game it has. Only at startup, when it has none, does it fall back to
// the first usable game in the registry.
func (arena *Arena) refreshGame() error {
	if arena.Game != nil && arena.MatchState != PreMatch {
		arena.gameRefreshPending.Store(true)
		return nil
	}
	arena.gameRefreshPending.Store(false)

	selectedId := arena.EventSettings.GameId
	selected := arena.Games.Get(selectedId)
	var err error
	if selected == nil {
		err = fmt.Errorf("game %q is missing or has errors", selectedId)
	} else {
		err = CheckGameSelectable(selected)
	}
	if err != nil {
		if arena.Game != nil {
			return fmt.Errorf("%w; still playing %q", err, arena.Game.ID)
		}
		if selected = arena.firstSelectableGame(); selected == nil {
			return fmt.Errorf("%w, and no other game in %q can be played on this field", err, gamesDir)
		}
		log.Printf("Cannot use the selected game: %v. Falling back to %q.", err, selected.ID)
	}

	// Only the parameters the game declares are kept, so that what is stored with a result is what applied to it.
	params := make(map[string]float64)
	for _, param := range selected.Params {
		if value, ok := arena.EventSettings.GameParams[param.ID]; ok {
			params[param.ID] = value
		}
	}
	if arena.Game != nil && arena.Game.Hash == selected.Hash && maps.Equal(arena.gameParams, params) {
		return nil
	}
	changedGame := arena.Game == nil || arena.Game.Hash != selected.Hash
	arena.Game = selected
	arena.gameParams = params

	// The raw score state has the shape of the game, so the loaded match starts over with the new one. Nothing is
	// lost: in PreMatch nothing has been scored yet. Browsers are told about the game first, so that they know what
	// the score that follows is made of.
	arena.GameNotifier.Notify()
	if arena.CurrentMatch != nil {
		arena.resetScore()
		arena.ScoringStatusNotifier.Notify()
		arena.MatchTimeNotifier.Notify()
		arena.RealtimeScoreNotifier.Notify()
	}
	if changedGame {
		log.Printf("The active game is now %q (%s).", selected.ID, selected.Hash[:12])
		arena.Bus.Publish("system.game_changed", map[string]any{"id": selected.ID, "hash": selected.Hash})
	}
	return nil
}

// firstSelectableGame returns the first game in the registry, by id, that loads and fits the field, or nil.
func (arena *Arena) firstSelectableGame() *gamedef.Game {
	for _, summary := range arena.Games.List() {
		if g := arena.Games.Get(summary.ID); g != nil && CheckGameSelectable(g) == nil {
			return g
		}
	}
	return nil
}

// applyPendingGame carries out a game refresh that was put off because a match was running. It must only be called
// in PreMatch.
func (arena *Arena) applyPendingGame() {
	if !arena.gameRefreshPending.Load() {
		return
	}
	if err := arena.refreshGame(); err != nil {
		log.Printf("Not changing the active game: %v", err)
	}
}

// watchGames notices game definition files that change on disk and reloads them, so a game can be edited while
// the system runs. It loops forever.
func (arena *Arena) watchGames() {
	for {
		time.Sleep(gamesPollPeriodSec * time.Second)
		arena.ReloadGames()
	}
}

// ReloadGames re-reads any game definition that has changed on disk and announces each one on the event bus. If
// the active game is among them, the new version is taken into use the next time the arena is in PreMatch; the
// arena loop does that, because this runs on its own goroutine.
func (arena *Arena) ReloadGames() []string {
	changed, err := arena.Games.Poll()
	if err != nil {
		log.Printf("Failed to check the games directory: %v", err)
		return nil
	}
	for _, id := range changed {
		valid := arena.Games.Get(id) != nil
		log.Printf("Game definition %q changed on disk (valid: %t).", id, valid)
		arena.Bus.Publish("system.game_reloaded", map[string]any{"id": id, "valid": valid})
		if arena.EventSettings != nil && id == arena.EventSettings.GameId {
			arena.gameRefreshPending.Store(true)
		}
	}
	return changed
}
