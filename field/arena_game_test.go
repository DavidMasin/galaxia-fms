package field

import (
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArenaStartsWithTheDefaultGame(t *testing.T) {
	arena := setupTestArena(t)
	if assert.NotNil(t, arena.Game) {
		assert.Equal(t, "rebuilt-style", arena.Game.ID)
	}
	assert.Equal(t, arena.Games.Get("rebuilt-style").Hash, arena.Game.Hash)
	assert.Empty(t, arena.gameParams)

	// The score, the saved result shown before any match is committed, and the game message all belong to it.
	assert.Contains(t, arena.Score.State.Alliances["red"].Elements, "fuel")
	assert.Equal(t, arena.Game.Hash, arena.SavedMatchResult.GameHash)
	snapshot, err := arena.Database.GetGameSnapshot(arena.Game.Hash)
	assert.Nil(t, err)
	assert.NotNil(t, snapshot)
	view := arena.generateGameMessage().(*gamedef.View)
	assert.Equal(t, "rebuilt-style", view.ID)
	assert.Equal(t, arena.Game.Hash, view.Hash)
	assert.Equal(t, 8, len(view.Phases))
}

func TestAllianceHelpers(t *testing.T) {
	arena := setupTestArena(t)

	assert.Equal(t, "red", arena.AllianceId(0))
	assert.Equal(t, "blue", arena.AllianceId(1))
	assert.Equal(t, "", arena.AllianceId(2))
	assert.Equal(t, "", arena.AllianceId(-1))
	assert.Equal(t, 0, arena.AllianceIndex("red"))
	assert.Equal(t, 1, arena.AllianceIndex("blue"))
	assert.Equal(t, -1, arena.AllianceIndex("green"))
	assert.Equal(t, -1, arena.AllianceIndex(""))

	assert.Equal(t, []string{"R1", "R2", "R3"}, arena.AllianceStationIds(0))
	assert.Equal(t, []string{"B1", "B2", "B3"}, arena.AllianceStationIds(1))
	assert.Nil(t, arena.AllianceStationIds(2))
	assert.Equal(t, []string{"R1", "R2", "R3", "B1", "B2", "B3"}, arena.activeStationIds())

	// Whatever a game calls its alliances, its first plays from the R stations and its second from the B stations.
	dir := useScratchGames(t, arena)
	source := string(model.TestGame("ball-count").Source)
	source = strings.Replace(source, "{ id: red, name: Red, short: R,", "{ id: gold, name: Gold, short: G,", 1)
	source = strings.Replace(source, "{ id: blue, name: Blue, short: B,", "{ id: silver, name: Silver, short: S,", 1)
	source = strings.Replace(source, "size: 3", "size: 2", 1)
	writeGame(t, arena, dir, "ball-count", source)
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(t, "gold", arena.AllianceId(0))
	assert.Equal(t, 1, arena.AllianceIndex("silver"))
	assert.Equal(t, -1, arena.AllianceIndex("red"))
	assert.Equal(t, []string{"B1", "B2"}, arena.AllianceStationIds(1))
	assert.Equal(t, []string{"R1", "R2", "B1", "B2"}, arena.activeStationIds())

	// A game with two robots per alliance leaves the third stations out of everything.
	arena.Database.CreateTeam(&model.Team{Id: 254})
	assert.Nil(t, arena.assignTeam(254, "B2"))
	assert.Equal(t, map[string][]int{"gold": {0, 0}, "silver": {0, 254}}, arena.matchTeams())
	for _, stationId := range []string{"R1", "R2", "B1", "B2"} {
		arena.AllianceStations[stationId].Bypass = true
	}
	assert.Nil(t, arena.checkCanStartMatch())
}

func TestScoringPanels(t *testing.T) {
	arena := setupTestArena(t)
	panelIds := func() []string {
		var ids []string
		for _, panel := range arena.ScoringPanels() {
			ids = append(ids, panel.ID)
		}
		return ids
	}

	// A game that lays out its own panels gets those.
	assert.Equal(t, []string{"red_near", "red_far", "blue_near", "blue_far"}, panelIds())

	// A game that declares none gets one per alliance.
	selectGame(t, arena, "ball-count", nil)
	assert.Equal(t, []string{"red", "blue"}, panelIds())
	assert.Equal(t, []string{"blue"}, arena.ScoringPanels()[1].Alliances)

	// And one for the field, if anything is scored for the field as a whole.
	dir := useScratchGames(t, arena)
	source := strings.Replace(
		string(model.TestGame("ball-count").Source),
		"\nfouls:",
		"\n  - { id: storm, name: Storm, type: boolean, scope: field }\n\nfouls:",
		1,
	)
	writeGame(t, arena, dir, "ball-count", source)
	arena.Update()
	assert.Equal(t, []string{"red", "blue", "field"}, panelIds())
}

func TestRejectGamesThatDoNotFitTheField(t *testing.T) {
	arena := setupTestArena(t)

	// Three alliances cannot be played on a field with two sides.
	threeAlliances := arena.Games.Get("orbital-rush")
	if assert.NotNil(t, threeAlliances) {
		err := CheckGameSelectable(threeAlliances)
		if assert.NotNil(t, err) {
			assert.Equal(
				t,
				"game \"orbital-rush\" has 3 alliances of 2 robots; for now only games with exactly two alliances of at "+
					"most three robots can be selected",
				err.Error(),
			)
		}
	}
	assert.Nil(t, CheckGameSelectable(arena.Games.Get("ball-count")))
	assert.Nil(t, CheckGameSelectable(arena.Games.Get("rebuilt-style")))

	// Selecting such a game is refused with that explanation, and the arena keeps the game it has.
	events := arena.Bus.Subscribe([]string{"system.game_changed"}, 10)
	defer events.Close()
	arena.EventSettings.GameId = "orbital-rush"
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	err := arena.LoadSettings()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "only games with exactly two alliances of at most three robots can be selected")
		assert.Contains(t, err.Error(), "still playing \"rebuilt-style\"")
	}
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
	assert.Empty(t, drainEvents(events))
	assert.NotNil(t, arena.PlayoffTournament, "the rest of the settings still took effect")

	// Four robots per alliance do not fit three stations.
	dir := useScratchGames(t, arena)
	fourRobots := strings.Replace(string(model.TestGame("ball-count").Source), "size: 3", "size: 4", 1)
	writeGame(t, arena, dir, "ball-count", fourRobots)
	arena.EventSettings.GameId = "ball-count"
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	err = arena.LoadSettings()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "game \"ball-count\" has 2 alliances of 4 robots")
	}
	assert.Equal(t, "rebuilt-style", arena.Game.ID)

	// So does a game that does not exist.
	arena.EventSettings.GameId = "no-such-game"
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	err = arena.LoadSettings()
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "game \"no-such-game\" is missing or has errors")
	}
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
}

func TestSelectGameInPreMatch(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"system.game_changed"}, 10)
	defer events.Close()
	previousHash := arena.Game.Hash

	// In PreMatch a new selection takes effect at once, and the loaded match gets a score of the new shape.
	selectGame(t, arena, "ball-count", nil)
	assert.NotEqual(t, previousHash, arena.Game.Hash)
	assert.Contains(t, arena.Score.State.Alliances["red"].Elements, "balls")
	assert.NotContains(t, arena.Score.State.Alliances["red"].Elements, "fuel")
	assert.Equal(t, "ball-count", arena.generateGameMessage().(*gamedef.View).ID)
	assert.Equal(t, arena.Game.Hash, arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage).GameHash)
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, map[string]any{"id": "ball-count", "hash": arena.Game.Hash}, received[0].Data)
	}

	// Loading the settings again without a change does nothing.
	score := arena.Score
	assert.Nil(t, arena.LoadSettings())
	assert.Same(t, score, arena.Score)
	assert.Empty(t, drainEvents(events))
}

func TestGameParametersApplyInPreMatch(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"system.game_changed"}, 10)
	defer events.Close()

	// Overrides of the game's parameters are taken from the settings; names the game does not have are dropped.
	selectGame(t, arena, "rebuilt-style", map[string]float64{"traversal_threshold": 0, "bogus": 1})
	assert.Equal(t, map[string]float64{"traversal_threshold": 0}, arena.gameParams)
	assert.Empty(t, drainEvents(events), "the game itself did not change")

	// They are what the live score is computed with: with the tower bonus switched off a win is worth two points.
	startTestMatch(t, arena)
	apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, 2.0, arena.Score.Result.Alliances["red"].RP["win"])

	// A change made during a match waits for the next one.
	arena.EventSettings.GameParams = map[string]float64{"traversal_threshold": 40}
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	assert.Nil(t, arena.LoadSettings())
	assert.Equal(t, map[string]float64{"traversal_threshold": 0}, arena.gameParams)
	apply(t, arena, count("red", "fuel", testDevice))
	assert.Equal(t, 2.0, arena.Score.Result.Alliances["red"].RP["win"])
	assert.Nil(t, arena.AbortMatch())
	assert.Nil(t, arena.ResetMatch())
	arena.Update()
	assert.Equal(t, map[string]float64{"traversal_threshold": 40}, arena.gameParams)
}

func TestRefreshGameDeferredWhileMatchRuns(t *testing.T) {
	arena := setupTestArena(t)
	events := arena.Bus.Subscribe([]string{"system.game_changed"}, 10)
	defer events.Close()
	startTestMatch(t, arena)
	apply(t, arena, count("red", "fuel", testDevice))

	// A game selected while a match is running does not touch that match.
	arena.EventSettings.GameId = "ball-count"
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	assert.Nil(t, arena.LoadSettings())
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
	arena.Update()
	runMatchTo(arena, 100)
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
	assert.Equal(t, "shift3", arena.CurrentPhase().ID)
	apply(t, arena, count("red", "fuel", testDevice))

	// Nor while its result is still pending: what is committed is the match as it was played.
	runMatchTo(arena, 163.05)
	assert.Equal(t, PostMatch, arena.MatchState)
	arena.Update()
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
	matchResult, err := arena.BuildMatchResult()
	assert.Nil(t, err)
	assert.Equal(t, "rebuilt-style", matchResult.GameId)
	assert.Equal(t, 2, matchResult.State.Alliances["red"].Elements["fuel"].Total())
	assert.Empty(t, drainEvents(events))

	// It takes effect once the arena is back in PreMatch.
	assert.Nil(t, arena.ResetMatch())
	assert.Equal(t, "rebuilt-style", arena.Game.ID)
	arena.Update()
	assert.Equal(t, "ball-count", arena.Game.ID)
	assert.Contains(t, arena.Score.State.Alliances["red"].Elements, "balls")
	assert.Empty(t, arena.Score.Changes)
	received := drainEvents(events)
	if assert.Equal(t, 1, len(received)) {
		assert.Equal(t, map[string]any{"id": "ball-count", "hash": arena.Game.Hash}, received[0].Data)
	}
}

func TestRefreshGameDeferredUntilNextMatchLoads(t *testing.T) {
	arena := setupTestArena(t)
	startTestMatch(t, arena)
	arena.EventSettings.GameId = "ball-count"
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	assert.Nil(t, arena.LoadSettings())
	assert.Nil(t, arena.AbortMatch())
	assert.Nil(t, arena.ResetMatch())
	assert.Equal(t, "rebuilt-style", arena.Game.ID)

	// Loading the next match does not wait for the arena loop to come round.
	assert.Nil(t, arena.LoadTestMatch())
	assert.Equal(t, "ball-count", arena.Game.ID)
	assert.Contains(t, arena.Score.State.Alliances["red"].Elements, "balls")
}

func TestChangedGameFileIsTakenIntoUseInPreMatch(t *testing.T) {
	arena := setupTestArena(t)
	dir := useScratchGames(t, arena)
	original := string(model.TestGame("ball-count").Source)
	writeGame(t, arena, dir, "ball-count", original)
	selectGame(t, arena, "ball-count", nil)
	originalHash := arena.Game.Hash
	events := arena.Bus.Subscribe([]string{"system"}, 10)
	defer events.Close()

	// The file is edited so that a teleop ball is worth ten points. The arena loop picks it up.
	richer := strings.Replace(original, "points: { auto: 2, teleop: 1 }", "points: { auto: 2, teleop: 10 }", 1)
	writeGame(t, arena, dir, "ball-count", richer)
	assert.Equal(t, originalHash, arena.Game.Hash, "the game only changes on the arena loop")
	arena.Update()
	richerHash := arena.Game.Hash
	assert.NotEqual(t, originalHash, richerHash)
	assert.Equal(
		t, []string{"system.game_reloaded", "system.game_changed"}, eventTypes(drainEvents(events)),
	)

	// While a match is running, another edit is held back.
	startTestMatch(t, arena)
	runMatchTo(arena, 30)
	apply(t, arena, count("red", "balls", testDevice))
	assert.Equal(t, 10.0, allianceScore(arena, "red"))
	writeGame(t, arena, dir, "ball-count", strings.Replace(original, "teleop: 1 }", "teleop: 100 }", 1))
	arena.Update()
	assert.Equal(t, richerHash, arena.Game.Hash)
	apply(t, arena, count("red", "balls", testDevice))
	assert.Equal(t, 20.0, allianceScore(arena, "red"))
	runMatchTo(arena, 137.05)
	arena.Update()
	assert.Equal(t, richerHash, arena.Game.Hash)
	assert.Equal(t, []string{"system.game_reloaded"}, eventTypes(drainEvents(events)))

	// The match is stored with the game it was played with.
	matchResult, err := arena.BuildMatchResult()
	assert.Nil(t, err)
	assert.Equal(t, richerHash, matchResult.GameHash)

	assert.Nil(t, arena.ResetMatch())
	arena.Update()
	assert.NotEqual(t, richerHash, arena.Game.Hash)
	assert.Equal(t, 100.0, arena.Game.Element("balls").Points.ByPhase["teleop"])
	assert.Equal(t, []string{"system.game_changed"}, eventTypes(drainEvents(events)))

	// The stored result is still scored the way the match was played.
	result, _, err := matchResult.Compute(arena.Database, false)
	assert.Nil(t, err)
	assert.Equal(t, 20.0, result.Alliances["red"].Score)
}

func TestInvalidGameFileNeverReplacesValidGame(t *testing.T) {
	arena := setupTestArena(t)
	dir := useScratchGames(t, arena)
	original := string(model.TestGame("ball-count").Source)
	writeGame(t, arena, dir, "ball-count", original)
	selectGame(t, arena, "ball-count", nil)
	validGame := arena.Game
	events := arena.Bus.Subscribe([]string{"system"}, 10)
	defer events.Close()

	// The file is saved with a mistake in it: an expression reads an element that does not exist.
	broken := strings.Replace(original, "own.phase_points.balls.auto", "own.phase_points.bals.auto", 1)
	writeGame(t, arena, dir, "ball-count", broken)
	assert.Nil(t, arena.Games.Get("ball-count"))
	arena.Update()
	assert.Same(t, validGame, arena.Game)
	received := drainEvents(events)
	if assert.Equal(t, []string{"system.game_reloaded"}, eventTypes(received)) {
		assert.Equal(t, map[string]any{"id": "ball-count", "valid": false}, received[0].Data)
	}

	// Matches go on being played and scored with the last valid version.
	startTestMatch(t, arena)
	apply(t, arena, count("red", "balls", testDevice))
	assert.Equal(t, 2.0, allianceScore(arena, "red"))
	assert.Nil(t, arena.AbortMatch())
	assert.Nil(t, arena.ResetMatch())
	arena.Update()
	assert.Same(t, validGame, arena.Game)

	// Once the file is fixed, it is taken into use.
	fixed := strings.Replace(original, "duration: 120", "duration: 90", 1)
	writeGame(t, arena, dir, "ball-count", fixed)
	arena.Update()
	assert.NotSame(t, validGame, arena.Game)
	assert.Equal(t, 107.0, arena.Game.Duration())
}

// setupGamesBaseDir points the application at a base directory of the test's own, holding the given games, and
// returns the path for a database in it.
func setupGamesBaseDir(t *testing.T, gameIds ...string) string {
	previousBaseDir := model.BaseDir
	model.BaseDir = ".."
	baseDir := t.TempDir()
	assert.Nil(t, os.MkdirAll(filepath.Join(baseDir, gamesDir), 0755))
	for _, id := range gameIds {
		assert.Nil(t, os.MkdirAll(filepath.Join(baseDir, gamesDir, id), 0755))
		assert.Nil(
			t,
			os.WriteFile(filepath.Join(baseDir, gamesDir, id, gamedef.GameFile), model.TestGame(id).Source, 0644),
		)
	}
	model.BaseDir = baseDir
	t.Cleanup(
		func() {
			model.BaseDir = previousBaseDir
		},
	)
	return filepath.Join(baseDir, "test.db")
}

func TestStartupFallsBackToFirstUsableGame(t *testing.T) {
	dbPath := setupGamesBaseDir(t, "orbital-rush", "rebuilt-style", "ball-count")

	// The event is set to a game that is not there.
	database, err := model.OpenDatabase(dbPath)
	assert.Nil(t, err)
	settings, err := database.GetEventSettings()
	assert.Nil(t, err)
	settings.GameId = "last-years-game"
	assert.Nil(t, database.UpdateEventSettings(settings))
	assert.Nil(t, database.Close())

	// The arena starts anyway, on the first game by id that loads and fits the field.
	arena, err := NewArena(dbPath)
	if assert.Nil(t, err) {
		defer arena.Database.Close()
		assert.Equal(t, "ball-count", arena.Game.ID)
		assert.Equal(t, "last-years-game", arena.EventSettings.GameId, "the setting itself is left alone")
		assert.Contains(t, arena.Score.State.Alliances["red"].Elements, "balls")
	}
}

func TestStartupSkipsGamesThatDoNotFitTheField(t *testing.T) {
	dbPath := setupGamesBaseDir(t, "orbital-rush", "rebuilt-style")
	database, err := model.OpenDatabase(dbPath)
	assert.Nil(t, err)
	settings, err := database.GetEventSettings()
	assert.Nil(t, err)
	settings.GameId = "orbital-rush"
	assert.Nil(t, database.UpdateEventSettings(settings))
	assert.Nil(t, database.Close())

	arena, err := NewArena(dbPath)
	if assert.Nil(t, err) {
		defer arena.Database.Close()
		assert.Equal(t, "rebuilt-style", arena.Game.ID)
	}
}

func TestStartupFailsWithoutAnyUsableGame(t *testing.T) {
	dbPath := setupGamesBaseDir(t, "orbital-rush")
	arena, err := NewArena(dbPath)
	assert.Nil(t, arena)
	if assert.NotNil(t, err) {
		assert.Contains(t, err.Error(), "game \"rebuilt-style\" is missing or has errors")
		assert.Contains(t, err.Error(), "no other game")
	}

	// The database is not left open by the failed start.
	database, err := model.OpenDatabase(dbPath)
	if assert.Nil(t, err) {
		assert.Nil(t, database.Close())
	}
}

func TestMatchResultOfAnotherGameStillComputes(t *testing.T) {
	arena := setupTestArena(t)

	// A match played with one game stays computable after the event moves on to another.
	startTestMatch(t, arena)
	apply(t, arena, count("blue", "fuel", testDevice))
	runMatchTo(arena, 163.05)
	matchResult, err := arena.BuildMatchResult()
	assert.Nil(t, err)
	assert.Nil(t, arena.ResetMatch())
	selectGame(t, arena, "ball-count", nil)
	result, g, err := matchResult.Compute(arena.Database, false)
	assert.Nil(t, err)
	assert.Equal(t, "rebuilt-style", g.ID)
	assert.Equal(t, 1.0, result.Alliances["blue"].Score)
	assert.Equal(t, model.BlueWonMatch, model.MatchStatusFromResult(g, result))
	assert.Equal(t, scoring.ResultWin, result.Alliances["blue"].Result)
}
