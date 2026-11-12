# Architecture: how a match runs on a game definition

This is the developer's map of the code that plays a match. It describes the state the arena keeps, how scoring
flows through it, and the exact messages browsers receive. The user-facing description of a game file is
[game-definition.md](game-definition.md); the overall plan is [PLAN.md](PLAN.md).

Status: sections marked **(M3b)** describe the arena running on the game engine. The Go core (`field`, `model`,
`tournament`, `playoff`, `partner`, `plc`) is built to them, and upstream's hard-coded 2026 game (packages `game`
and `led`) is gone. The `web` package, the templates and the JavaScript are the next stage and are written
against this document; until they land, `web` does not compile.

## Packages

| Package | Knows about |
|---------|-------------|
| `gamedef` | Game files: types, schema, validation, registry, the browser `View` |
| `rules` | Expressions |
| `scoring` | Raw state, changes, undo, computing results and rankings |
| `bus` | Events |
| `field` | The arena: stations, driver stations, the phase clock, the live score, notifiers |
| `model` | Stored records |
| `tournament`, `playoff` | Schedules, rankings across matches, brackets |
| `partner` | The Blue Alliance, Nexus, Blackmagic, Companion |
| `plc` | The field PLC: stops, stack lights, field reset. No game-specific inputs or outputs. |
| `web` | HTTP handlers, templates, WebSockets |

Nothing outside `games/` may name a scoring element, phase or rule of a particular game.

## The active game (M3b)

- `EventSettings.GameId` selects the game (`model.DefaultGameId`, `rebuilt-style`, for a new event or a record
  written before games were selectable); `EventSettings.GameParams` overrides its parameter defaults. Only
  parameters the game declares are used.
- `Arena.Game` is the active `*gamedef.Game`. It never changes while a match is loaded past `PreMatch`:
  a new selection, changed parameters, or a changed file on disk, is applied by `Arena.refreshGame()` the next
  time the arena is in `PreMatch`. `Arena.LoadSettings()` calls it directly; a file change only marks a refresh
  as due, and the arena loop (`Update`) or `LoadMatch` carries it out, so the game is only ever replaced on the
  arena goroutine or by an operator's request. Replacing the game gives the loaded match a fresh, empty score.
- A selection that cannot be used never replaces a valid game: the arena keeps the game it has, and
  `LoadSettings()` returns the error after applying every other setting. That covers a missing game, an invalid
  file, and a game that does not fit the field.
- If the selected game cannot be used at startup, the arena falls back to the first game in the registry, by
  id, that loads and fits the field, and logs it. With no such game at all, startup fails.
- Until generic alliances land (milestone 3c) only games with exactly two alliances of at most three robots
  can be selected; `field.CheckGameSelectable(game)` says so in its error. Alliance index 0 occupies the `Red*`
  fields of `model.Match` and stations `R1..R3`; index 1 the `Blue*` fields and `B1..B3`, whatever the game calls
  its alliances. `Arena.AllianceId(index)` and `Arena.AllianceIndex(id)` convert, and
  `Arena.AllianceStationIds(index)` lists an alliance's stations by robot position. A game with fewer than three
  robots per alliance leaves the last stations unused; they are not needed to start a match.

## Match state machine (M3b)

`field.MatchState`:

| Value | Name | Meaning |
|-------|------|---------|
| 0 | `PreMatch` | A match is loaded and has not started |
| 1 | `StartMatch` | Transitional: set by `StartMatch()`, consumed by the next `Update()` |
| 2 | `InMatch` | A phase is running; see `Arena.PhaseIndex` |
| 3 | `PostMatch` | The match ended or was aborted; scores are pending |
| 4 | `TimeoutActive` | A timeout or break is counting down |
| 5 | `PostTimeout` | Brief dwell after a timeout |

- While `InMatch`, `Arena.PhaseIndex` indexes `Game.Match.Phases`; in every other state it is -1.
  `Arena.CurrentPhase()` returns the phase, or nil in any other state. `Arena.MatchTimeSec()` is seconds since
  the match started while it is running, and seconds since the timeout started during `TimeoutActive` and
  `PostTimeout`; it is zero in every other state. `Arena.PhaseTimeSec()` is seconds into the current phase.
- Robots are enabled when the phase's `robots` is `auto` or `teleop`, and told they are in autonomous when it
  is `auto`. Before the match they are held disabled in the mode of the first phase. Station E-stop, A-stop
  (autonomous phases only) and bypass mask the enable as before. An E-stop stays latched for as long as the
  state is `InMatch`.
- On entering a phase: fire its `at: start` cues, run `phase_start` triggers, publish `match.phase_changed`.
  On leaving: fire `at: end` cues, run `phase_end` triggers. Cues with `remaining`/`elapsed` fire when their
  offset passes. A cue plays its sound, runs triggers bound to its id, and publishes `match.cue`. If the arena
  loop falls behind by more than a phase, it catches up through every phase in order in one pass, so no cue or
  trigger is skipped.
- When the last phase ends the state becomes `PostMatch`, running timers are stopped by rule changes, and
  `match.ended` is published. `AbortMatch()` does the same and publishes `match.aborted`.
- The seconds-remaining field sent to driver stations is the time left until the robots' state next changes:
  consecutive phases with the same `robots` value count down as one stretch, so a teleop split into several
  phases is one countdown, as at an official event. During a pause, before the match and during a timeout it
  is the length of the stretch that comes next.
- Game data sent to a driver station is the game's `ds_game_data` expression evaluated for that station, or
  empty if the game has none. It is evaluated every time a driver station packet is sent.
- Sound names: a cue's `sound` is played as `static/audio/<sound>.wav`. Three aliases keep the example games
  readable: `match_start` plays `start`, and `phase_end` and `match_end` play `end`. `Arena.MatchSounds()` lists
  every file name (without extension) a display may be asked to play, for preloading.
- Companion: "teleop start" is sent on entering the first phase whose `robots` is `teleop`; "endgame start" on
  any cue whose id is `endgame`.
- Field timers (team signs) count down the time until the robots are next told to do something different: the
  stretch of consecutive phases with the same `robots` value, and zero during a `disabled` phase. The rear text
  of a team sign is the first letter of the phase name with the seconds left in the phase, the game's
  `layout.team_sign` text for that alliance (the alliance score if the game has none; both scores in a playoff
  match), and that countdown.

Timeouts, test matches, field reset and volunteer signals, bypass, and the pre-start checks are unchanged
from Cheesy Arena. `Arena.TimeoutDurationSec` is the length of the timeout in progress.

## Live score (M3b)

`Arena.Score` is a `*field.MatchScore`:

```go
type MatchScore struct {
	Changes []scoring.Change   // Every change, including undo records. The audit log.
	State   *scoring.State     // scoring.Fold(Changes): raw values only.
	Result  *scoring.Result    // scoring.Compute(State): points, breakdown, winner, ranking points.
	Live    *scoring.LiveValues // The game's live values at the last tick.
}
```

All scoring goes through two methods. They take the arena's score lock themselves and may be called from any
goroutine; code outside the `field` package reads the score through `Arena.ScoreSnapshot()` and
`Arena.BuildMatchResult()` rather than through `Arena.Score`.

```go
func (arena *Arena) ApplyScoreChange(change scoring.Change) (scoring.Change, error)
func (arena *Arena) UndoScoreChange(seq int, source scoring.Source) (scoring.Change, error)
```

`ApplyScoreChange` fills in `Seq`, `Time` (UTC), `Phase`, `MatchTime` and `Bucket`, checks that the source is
allowed, applies the change, recomputes, notifies browsers and publishes `score.changed`. A refused change is
not recorded; the error says why.

- **Phase and bucket.** While `InMatch`, `Phase` is the current phase. For `score_grace` seconds after a phase
  has ended, a change to an element that the ended phase accepted but the current one does not, or any device
  input to an element the ended phase accepted, is credited to the ended phase (`Bucket`). A phase with
  `robots: disabled` has no window of its own, because nothing can still be on its way from a phase in which
  nothing moved; the window of the phase before it carries on through it. While the match runs, a `Bucket`
  named by a `device`, `scorer` or `referee` is ignored, so that it cannot be used to get around an element's
  `phases`; `admin`, `rule` and `api` sources may name one. In `PostMatch`, `Phase` is `post` and `MatchTime`
  is the time at which the match stopped; device inputs arriving within `score_grace` seconds of the end are
  credited to the phase that was running when the match stopped, and later ones are refused; human edits to
  counters must name the `Bucket` themselves. In every other state changes are rejected, except from `admin`.
- **Who may change what.** For `device`, `scorer` and `referee` sources during `InMatch`, the element must
  list that kind in `edit` and must accept the bucket phase. In `PostMatch` scorers and referees may change
  any element. `admin`, `rule` and `api` sources are not restricted. Fouls, cards and the alliance
  disqualification flag may be changed by `referee`, `admin` and `api` only.
- **Undo.** `UndoScoreChange` appends a change with `Reverts` set and rebuilds `State` with `scoring.Fold`.
  The source needs the permission it would need to make the change it undoes. A change can be undone once;
  undoing the undo record brings it back. It publishes `score.undone`.
- **Triggers** produce ordinary changes with source kind `rule`.

Live values are re-evaluated on every pass of the arena loop (every 10 ms); if any changed, browsers are
notified.

The referees' confirmation of each alliance's fouls (`Arena.FoulsCommitted(id)`, `SetFoulsCommitted`,
`RefereeScoreReady()`) and the scoring panels' committed flags (`Arena.ScoringPanelRegistry`, keyed by panel id)
are workflow, not score: they are arena state and are reset when a match is loaded.

## Stored results (M3b)

```go
type MatchResult struct {
	Id         int `db:"id"`
	MatchId    int
	PlayNumber int
	MatchType  MatchType
	GameId     string
	GameHash   string                // Identifies the GameSnapshot the match was played with.
	Params     map[string]float64    // Event parameter overrides in force.
	Teams      map[string][]int      // Alliance id -> team number by robot position.
	Changes    []scoring.Change      // The audit log.
	State      *scoring.State        // Fold of Changes. Raw values, never totals.
}
```

- `GameSnapshot{Id, Hash, GameId, Yaml}` stores each distinct game file a result was played with
  (`Database.SaveGameSnapshot`, which stores a hash once; `GetGameSnapshot`; `GetSnapshotGame`, which parses
  each snapshot once). A result is always recomputed with its own snapshot, so editing a game later does not
  rewrite history.
- `Arena.BuildMatchResult()` returns the result of the loaded match as it stands (everything but `PlayNumber`)
  and saves the snapshot. `model.NewMatchResult(game)` is an empty result.
- `MatchResult.Compute(db, playoff)` loads the snapshot and returns the `scoring.Result` and the game.
  `playoff` turns on the game's playoff tiebreakers; callers pass the match's `UseTiebreakCriteria`, which the
  bracket sets for the matches that may not end level.
- Editing a result after the match appends changes with `Phase: "post"` (`MatchResult.Edit(game, change)`,
  `MatchResult.Undo(game, seq, source)`) and saves; the match winner, the rankings and the playoff bracket are
  then recalculated exactly as on first commit.
- A team is disqualified from a match when its robot position holds a card whose definition has
  `disqualifies: true` (`model.DisqualifiedPositions`). In playoffs that disqualifies the alliance:
  `Compute` does this for a result whose `MatchType` is `Playoff`, without changing the stored state.
- A team carries a card into its next match (`Team.YellowCard`) when its robot position held any card in the
  latest result of a completed qualification match; a playoff alliance (`Alliance.YellowCard`) when any robot
  position on its side did in a completed playoff match.
- `model.MatchStatus` (scheduled, hidden, red won, blue won, tie) is stored on the match and derived from
  `Result.Winner` by `model.MatchStatusFromResult(game, result)`: alliance index 0 is "red", index 1 is "blue".
- `model.Ranking` wraps `scoring.TeamRanking` and is keyed by `TeamId`. Rankings are rebuilt from every
  committed qualification result by `tournament.CalculateRankings(db, game, preservePreviousRank)`, which
  scores each result with its own snapshot and sorts with the event's active game.

## WebSocket messages (M3b)

Browsers connect to a page's `/websocket` endpoint and receive `{type, data}` messages. The ones that carry
game content:

### `game`

Sent on connect and whenever the active game changes. `data` is the `gamedef.View`: alliances, stations,
phases with start times and cues, elements with their widgets, foul types, rules, cards, breakdown rows,
ranking point names, ranking order columns, and layout hints. Browser code builds every game-dependent
control and readout from this and never hard-codes a name.

The old `matchTiming` message is gone: phase lengths are in the game view.

### `matchTime`

```json
{ "MatchState": 2, "MatchTimeSec": 37, "PhaseIndex": 2, "PhaseId": "teleop",
  "PhaseTimeSec": 20, "PhaseRemainingSec": 100, "TimeoutDurationSec": 0 }
```

`PhaseIndex` is -1 and `PhaseId` is empty outside `InMatch`. `TimeoutDurationSec` is the length of the timeout
or break that is counting down (or of the last one); during `TimeoutActive`, `MatchTimeSec` counts up towards
it. Sent once a second and on every state or phase change.

### `realtimeScore`

```json
{
  "gameHash": "…",
  "seq": 41,
  "winner": "red",
  "tiebreak": "",
  "field": { "state": { }, "values": { }, "live": { } },
  "alliances": {
    "red": {
      "score": 16,
      "foulPoints": 0,
      "points": { "balls": 16 },
      "phasePoints": { "balls": { "auto": 6, "teleop": 10 } },
      "values": { },
      "breakdown": [ { "id": "auto_points", "name": "Auto", "value": 6 } ],
      "result": "win",
      "rp": { "win": 2 },
      "rpTotal": 2,
      "live": { },
      "state": {
        "elements": { "balls": { "counts": { "auto": 3, "teleop": 10 } } },
        "robots": [ { }, { }, { } ],
        "fouls": [ { "id": 7, "type": "foul", "phase": "teleop" } ],
        "cards": { },
        "disqualified": false
      }
    }
  }
}
```

`state` is the raw `scoring.AllianceState` (empty properties of a value or a foul are left out; `cards` is keyed
by robot position); everything else is computed. `seq` is the sequence number of the last change, 0 before the
first. Scoring panels render from `state`; displays render from the computed fields.

### `scoreChange`

Sent to scoring and referee panels for each change, so they can show an undo list:
`{ "change": <scoring.Change>, "undone": false }`. After an undo, `change` is the scoring change whose effect
was switched and `undone` says whether it is now out of effect.

### `scorePosted`

The committed match, recomputed from the stored result with its own game snapshot:

```json
{
  "match": { },
  "gameId": "rebuilt-style",
  "gameHash": "…",
  "winner": "blue",
  "tiebreak": "",
  "alliances": {
    "red": {
      "teams": [101, 102, 103],
      "offFieldTeams": [],
      "score": 133,
      "breakdown": [ { "id": "auto_fuel_points", "name": "Auto fuel", "value": 18 } ],
      "rankingPoints": [ { "id": "win", "name": "Win", "value": 0 } ],
      "rpTotal": 0,
      "cards": { "1": "yellow" },
      "fouls": [ { "id": 36, "type": "major", "rule": "G418", "robot": 3, "phase": "endgame",
                   "typeName": "Major foul", "points": 15, "team": 103,
                   "ruleName": "Extended pinning", "ruleText": "…" } ],
      "result": "loss",
      "disqualified": false,
      "rankings": { "101": { "rank": 2, "previousRank": 1 } },
      "wins": 0,
      "destination": ""
    }
  }
}
```

`match` is the `model.Match` record. `teams` is by robot position and `cards` is keyed by robot position.
`rankings` holds each on-field team's rank before and after, for qualification matches only. `wins`,
`destination` and `offFieldTeams` describe the playoff series and are empty otherwise. `winner` is an alliance
id, or empty for a tie.

### `scoringStatus`

`{ "RefereeScoreReady": false, "PositionStatuses": { "<panel id>": { "Ready", "NumPanels", "NumPanelsReady" } } }`,
with one entry for every scoring panel position of the game.

### Commands from panels

| Panel | Command | Data |
|-------|---------|------|
| scoring, referee | `score` | `{ "alliance", "robot", "element", "op", "value", "bucket" }` |
| scoring, referee | `undo` | `{ "seq" }` |
| referee | `foul` | `{ "alliance", "robot", "type", "rule" }` adds; `{ "alliance", "remove": <foul id> }` removes |
| referee | `card` | `{ "alliance", "robot", "card" }`, empty card clears |
| scoring | `commitMatch` | none |

Scoring panel positions come from the game's `layout.scoring_panels`. A game that declares none gets one
panel per alliance, named by the alliance id, plus `field` if it has field elements. `Arena.ScoringPanels()`
returns them.

## The Blue Alliance (M3b)

- A played match is published with each alliance's computed score, and with a `score_breakdown` built from the
  game's `tba.breakdown` map: every expression is evaluated for each alliance, and a key containing dots
  (`hubScore.autoCount`) becomes a nested object. A game with no `tba` section is published without
  `score_breakdown`. TBA's "red" and "blue" are the first and second alliance of the game the match was played
  with.
- `TbaClient.PublishRankings(db, game)` publishes the active game's ranking order columns by name (the random
  key is left out), each team's stored sort value under each, and wins, losses, ties, dqs and played.

## Events on the bus (M3b)

| Type | Data |
|------|------|
| `match.loaded` | `id`, `type`, `shortName`, `teams` (station id -> team number) |
| `match.started` | `time` (UTC) |
| `match.phase_changed` | `phase`, `index`, `robots`, `duration` |
| `match.cue` | `cue`, `sound`, `phase` |
| `match.aborted`, `match.ended` | `matchTime` |
| `match.committed`, `match.result_edited` | `id`, `winner`, `scores` (alliance id -> score) |
| `score.changed` | the `scoring.Change` |
| `score.undone` | the reverting `scoring.Change` |
| `station.status` | `station`, `team`, `changed` (which of the following changed), `driverStation`, `robot`, `radio`, `eStop`, `aStop`, `bypass` |
| `field.reset`, `field.volunteers` | none |
| `system.game_reloaded` | `id`, `valid` |
| `system.game_changed` | `id`, `hash` of the newly active game |

`match.committed` and `match.result_edited` are published by `Arena.PublishMatchCommitted(match, result,
edited)`, which the code that commits a result calls.
