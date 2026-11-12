package field

import (
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/gamedef"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/scoring"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testDevice  = scoring.Source{Kind: scoring.SourceDevice, ID: "sensor-1", Name: "Test sensor"}
	testScorer  = scoring.Source{Kind: scoring.SourceScorer, ID: "panel-1"}
	testReferee = scoring.Source{Kind: scoring.SourceReferee, ID: "head-referee"}
	testAdmin   = scoring.Source{Kind: scoring.SourceAdmin, ID: "admin"}
)

// setMatchTime moves the start of the match so that it began the given number of seconds ago. The arena only
// notices on its next Update.
func setMatchTime(arena *Arena, seconds float64) {
	arena.MatchStartTime = time.Now().Add(-time.Duration(seconds * float64(time.Second)))
}

// runMatchTo moves the match clock to the given time and lets the arena catch up.
func runMatchTo(arena *Arena, seconds float64) {
	setMatchTime(arena, seconds)
	arena.Update()
}

// selectGame makes the game with the given id the event's game, with the given parameter overrides.
func selectGame(t *testing.T, arena *Arena, id string, params map[string]float64) {
	arena.EventSettings.GameId = id
	arena.EventSettings.GameParams = params
	assert.Nil(t, arena.Database.UpdateEventSettings(arena.EventSettings))
	assert.Nil(t, arena.LoadSettings())
	if assert.NotNil(t, arena.Game) {
		assert.Equal(t, id, arena.Game.ID)
	}
}

// useScratchGames points the arena at an empty games directory of its own, so that a test can write game files.
func useScratchGames(t *testing.T, arena *Arena) string {
	dir := t.TempDir()
	arena.Games = gamedef.NewRegistry(dir)
	assert.Nil(t, arena.Games.Load())
	return dir
}

// writeGame writes a game definition into a games directory and has the arena notice it.
func writeGame(t *testing.T, arena *Arena, dir, id, source string) {
	assert.Nil(t, os.MkdirAll(filepath.Join(dir, id), 0755))
	path := filepath.Join(dir, id, gamedef.GameFile)
	assert.Nil(t, os.WriteFile(path, []byte(source), 0644))

	// The registry tells files apart by size and modification time. Two versions written within one tick of a
	// coarse file system clock could look the same, so each write gets a time of its own.
	testGameFileTime = testGameFileTime.Add(time.Second)
	assert.Nil(t, os.Chtimes(path, testGameFileTime, testGameFileTime))
	assert.Contains(t, arena.ReloadGames(), id)
}

var testGameFileTime = time.Now()

// ballCountWithTimer is the ball-count example game with a timer element added for each alliance, worth a point a
// second.
func ballCountWithTimer() string {
	return strings.Replace(
		string(model.TestGame("ball-count").Source),
		"\nfouls:",
		"\n  - { id: hold, name: Hold, type: timer, scope: alliance, points: 1, edit: [device, scorer] }\n\nfouls:",
		1,
	)
}

func bypassAllStations(arena *Arena) {
	for _, allianceStation := range arena.AllianceStations {
		allianceStation.Bypass = true
	}
}

// startTestMatch starts the loaded match with every station bypassed and runs the arena into its first phase.
func startTestMatch(t *testing.T, arena *Arena) {
	bypassAllStations(arena)
	assert.Nil(t, arena.StartMatch())
	arena.Update()
	assert.Equal(t, InMatch, arena.MatchState)
	assert.Equal(t, 0, arena.PhaseIndex)
}

// drainEvents returns the events waiting in a subscription, without blocking.
func drainEvents(subscription *bus.Subscription) []bus.Event {
	var events []bus.Event
	for {
		select {
		case event := <-subscription.C:
			events = append(events, event)
		default:
			return events
		}
	}
}

// eventTypes returns the types of the given events, in order.
func eventTypes(events []bus.Event) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

// apply makes a score change that the test expects to be accepted.
func apply(t *testing.T, arena *Arena, change scoring.Change) scoring.Change {
	applied, err := arena.ApplyScoreChange(change)
	assert.Nil(t, err)
	return applied
}

// count is shorthand for a change that adds one to a counter element of an alliance.
func count(alliance, element string, source scoring.Source) scoring.Change {
	return scoring.Change{Alliance: alliance, Element: element, Op: scoring.OpIncrement, Source: source}
}

// allianceScore returns the current score of an alliance.
func allianceScore(arena *Arena, allianceId string) float64 {
	result, _, _ := arena.ScoreSnapshot()
	return result.Alliances[allianceId].Score
}
