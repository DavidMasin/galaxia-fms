# Architecture: how a match runs on a game definition

This is the developer's map of the code that plays a match. It describes the state the arena keeps, how scoring
flows through it, and the exact messages browsers receive. The user-facing description of a game file is
[game-definition.md](game-definition.md); the overall plan is [PLAN.md](PLAN.md).

Status: sections marked **(M3b)** describe the arena running on the game engine. The Go core (`field`, `model`,
`tournament`, `playoff`, `partner`, `plc`) and the `web` package with its operator pages are built to them, and
upstream's hard-coded 2026 game (packages `game` and `led`) is gone.

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
| `device` | Field devices: registry, transports (MQTT, virtual), bindings, safety. Does not import `field`. |
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

A panel that connects is first sent the latest changes of the loaded match (at most twenty, oldest first) as
`scoreChange` messages, each with `undone` as it stands, so that its undo list survives a reload. They arrive
before the `game` message; a panel keeps them and draws its list once it has the game.

### `scorePosted`

The committed match, recomputed from the stored result with its own game snapshot:

```json
{
  "match": { },
  "gameId": "rebuilt-style",
  "gameHash": "…",
  "winner": "blue",
  "tiebreak": "",
  "allianceOrder": ["red", "blue"],
  "cards": [ { "id": "yellow", "name": "Yellow card", "color": "#fdd835", "secondBecomes": "red", "disqualifies": false } ],
  "alliances": {
    "red": {
      "name": "Red",
      "color": "#d32f2f",
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
| referee | `foul` | `{ "alliance", "robot", "type", "rule" }` adds; `{ "alliance", "remove": <foul id> }` removes; `{ "alliance", "update": <foul id>, "robot", "type", "rule" }` corrects |
| referee | `card` | `{ "alliance", "robot", "card" }`, empty card clears |
| scoring | `commitMatch` | none |

A refused command is answered with an `error` message that says why; nothing is recorded.

- `score` only changes scoring elements. Fouls, cards and the disqualification flag are refused there, because
  the commands of their own apply the rules that go with them. The source of a scoring panel's changes is
  `{kind: scorer, id: <panel id>, name: <panel name>}`; the referee panel's is `{kind: referee, id: referee}`.
- `foul` with `update` corrects the type, the rule or the robot of a foul that was already called. A foul is
  never modified in place: the old one is removed and the corrected one added, so it gets a new id and the audit
  log shows both. The new foul keeps the phase of the old one where the arena lets a referee name a phase (after
  the match). A rule is only kept if the game's catalogue lists it for the foul's type.
- `card` applies two rules from the game's card list before it changes anything. A card whose definition names a
  `second_becomes` card is replaced by that card when the team carries a card from an earlier match
  (`Team.YellowCard`, or `Alliance.YellowCard` in the playoffs) or already holds the same card in this match. A
  card with `disqualifies` cannot be a team's first card: asked for directly, it is refused unless the team
  already has a card in this match or carries one. In a playoff match `robot` may be 0, which gives the card to
  the alliance as a whole, that is to every robot position; that is what the referee panel does in the playoffs.
  The result editor of the match review page is not bound by these rules.

Scoring panel positions come from the game's `layout.scoring_panels`. A game that declares none gets one
panel per alliance, named by the alliance id, plus `field` if it has field elements. `Arena.ScoringPanels()`
returns them, and the pages are `/panels/scoring/<panel id>`. What a panel shows:

- an alliance or robot element, for each of the panel's `alliances` (all alliances if it lists none), if the
  element's `ui.group` is one of the panel's `groups` (any group if it lists none);
- a field element, if the panel lists no alliances and the group passes, or if the panel lists the element's
  group;
- nothing that has `ui.hidden`.

`/panels/scoring/<panel id>/manifest` is the web app manifest of a panel, for pinning it to a tablet's home
screen.

## Web pages and API (M3b)

- **Message order.** Every page that shows game content subscribes to `game` before `matchTime` and
  `realtimeScore`, so on connect the game always arrives first. A page must still cope with any order
  afterwards: when the game changes, the score of the new game and the new game can arrive either way round,
  and `realtimeScore.gameHash` says which game a score belongs to.
- **Colours.** `base.html` sets `--alliance-<index>-color` for the alliances of the active game; `game_view.js`
  keeps them up to date on pages that follow the game live. The classes `red-text`, `blue-text`, `bg-red` and
  `bg-blue` name the two sides of a page and take their colours from those variables.
- **Settings.** The settings page lists `Arena.Games.List()` and marks the games that have errors or do not
  pass `field.CheckGameSelectable`, with the reason. The event's overrides of the selected game's parameters are
  posted as `gameParam.<game id>.<param id>`; an empty value removes the override. While a match is in progress
  or uncommitted no other setting can be saved (reloading the settings reconnects the field hardware), but a
  changed game selection or changed parameters are stored at once. The web layer then calls
  `Arena.LoadSettings()` the next time a match is reset (load, commit or discard), which is what makes the
  arena take the selection into use once it is back in `PreMatch`.
- **Result editor** (`/match_review/<match id>/edit`, or `current` for the match on the field). The page embeds
  the view of the game the result was played with, the raw state and the computed summary; the form is built
  from them in the browser. Saving posts the edited raw state as `stateJson`. The server turns the difference
  into changes by source `{kind: admin, id: match_review}`: a counter is set phase by phase, a boolean or an
  enum is set in the phase the state names for it, a changed foul is removed and added again, and a part of
  the state that is left out is unchanged. For a stored result they are applied with `MatchResult.Edit`
  (`Phase: "post"`) and the match is committed again as an edit; for the match on the field they are applied
  with `Arena.ApplyScoreChange`. If any change is refused nothing is saved and the editor says why.
  `POST /match_review/<match id>/summary` takes the same JSON as its body and returns the summary without
  saving: `{ "winner", "winnerName", "winnerSlot", "tiebreak", "alliances": [ { "id", "name", "slot", "color",
  "teams", "score", "breakdown": [ { "id", "name", "value", "text" } ], "rankingPoints": [ ... ], "rpTotal",
  "result", "disqualified" } ], "errors": [ ] }`. `POST /match_review/<match id>/undo` with `seq` undoes one
  change of the audit log shown on the page, or brings an undone change back when given its undo record.
- **`GET /api/rankings`** returns `{ "Columns": [ { "Name", "Decimals", "Random" } ], "Rankings": [ { "TeamId",
  "Nickname", "Rank", "PreviousRank", "RP", "Played", "Wins", "Losses", "Ties", "Disqualifications", "Sort" } ],
  "HighestPlayedMatch" }`. `Columns` is the active game's ranking order and every ranking's `Sort` has exactly
  one value per column, whatever game the rankings were last calculated under.
- **`GET /api/matches/<type>`** returns each match with `Result`: the stored `MatchResult` and, under
  `Computed`, the `scoring.Result` it computes to with the game it was played with (`alliances` keyed by
  alliance id, `field`, `winner`, `tiebreak`). `Result` is null for a match without one.
- **Reports.** The rankings PDF and CSV print, between the ranking points and the win-loss-tie record, the
  columns of the active game's ranking order except its random tiebreaker, to the decimals the game asks for.
  The cycle time report measures referee time from `Game.Duration()` after the start of the match.

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

Field devices publish the `device.*` events listed at the end of [devices.md](devices.md).

## Devices (M5)

Field devices are specified in [devices.md](devices.md); this is how the code is put together. The package
`device` holds everything about them and never imports `field`: the arena drives it through the API below and
hands it the state of the match. Nothing in it names a game, a device or a channel.

### Types

| Type | What it is |
|------|------------|
| `device.Manager` | Registry, binding set in force, input processing, outputs, faults, stops. `Arena.Devices`. |
| `device.Manifest`, `Channel` | What a device says it has. `ParseManifest`, `Manifest.Validate`. |
| `device.Reading`, `Status`, `Heartbeat` | Input, status and heartbeat messages (`ParseReading` and so on). |
| `device.Command`, `Segment` | An output command; `nil` clears the output. `Transient` marks a sound: never retained. |
| `device.FieldState` | The `field/state` payload. |
| `device.BindingSet`, `InputBinding`, `Action`, `OutputBinding`, `Look` | Bindings, as YAML: `ParseBindings`, `YAML`. |
| `device.Environment`, `Problem`, `Problems` | What bindings are checked against (`Validate`), and what is wrong. |
| `device.Transport`, `Sink` | A transport carries device messages; the manager is its sink. |
| `device.MqttTransport` | The embedded broker. `Arena.DeviceMqtt`. |
| `device.VirtualTransport`, `VirtualDevice` | Devices inside the FMS. `Arena.DeviceVirtual`. |
| `device.Snapshot`, `StationState` | The state of the field handed to output evaluation. |
| `device.Stops`, `Fault`, `FieldAction` | What devices ask of the arena. |
| `device.Info`, `Presence`, `InputValue`, `Refusal` | A copy of everything known about a device, for the pages. |
| `model.Device`, `FieldProfile`, `BindingSet` | The stored records. A binding set is stored as its YAML. |

`EventSettings` has `FieldProfile` (the profile whose bindings apply, `default` for a new event), `DeviceMqttPort`
(1883) and `DeviceMqttWsPort` (8083); a port of 0 turns that listener off. A record written before devices
existed (its profile is empty) gets the defaults. `GetAllFieldProfiles` creates the `default` profile on first
use; renaming a profile carries its binding sets and the event's choice along; the active one cannot be deleted.

### The manager API

For the pages:

```go
Devices() []Info; Device(id) (Info, bool)
CreateDevice(id, name) (token, error); RegenerateToken(id) (token, error) // The token is returned once.
Approve(id); Block(id); Unblock(id); Delete(id)
SetRequired(id, bool); SetNotes(id, notes); SetName(id, name)
Identify(id); Reboot(id)
Faults() []Fault; Faulted() bool; ClearFaults(id) error; ClearAllFaults() error
Bindings() BindingsState // The set in force, with every problem it has now.
ValidateBindings(set) Problems; SaveBindings(set) (Problems, error)
ExportBindings() ([]byte, error); ImportBindings(yaml) (Problems, error)
Environment() Environment; Profile() string; TransportStatuses() []TransportStatus
```

For the arena, from its loop:

```go
SetGame(game, stations); SetProfile(profile) // From refreshGame and LoadSettings.
SetMatch(MatchInfo)                          // Every pass: running, phase, grace phase, whether scoring takes input.
Tick(now)                                    // Every pass: debounce timers and the heartbeat watchdog.
TakeFieldActions() []FieldAction; ReportFieldAction(action, err)
Stops() Stops; StartConditions() []string
UpdatePlc(plc); UpdateOutputs(*Snapshot)     // Every fifth pass: twenty times a second.
```

`Arena.Update` calls `updateDevices` before `handlePlcInputOutput`, so that a device stop acts in the same pass, and
`updateDeviceOutputs` after the live values are recomputed. Field actions run on the arena goroutine:
`start_match`, `abort_match`, `field_reset` and `volunteers` call the arena's own methods; `commit` and
`next_match` call the handlers the web layer registers with `Arena.SetFieldActionHandler(action, func() error)`,
on a worker goroutine, one at a time, because committing writes a database backup and may talk to The Blue
Alliance. An error from any of them is a refusal on the device that asked. Device stops are merged in
`handlePlcInputOutput`: with a PLC, a station is stopped if either stops it, and either's field stop aborts the
match; without one, only the stations some device can stop go through `handleTeamStop`, so an event without device
stops behaves exactly as before. `getStartMatchConditions` appends `StartConditions()`. The arena records
`match.start`, `match.end`, `match.abort`, `phase.<id>.start`, `phase.<id>.end`, `cue.<id>` and `score.posted`
(from `PublishMatchCommitted`) for the next snapshot. The transports start in `Arena.Run`, so a test arena never
opens a port; `LoadSettings` applies the profile and restarts the broker if its ports changed while it runs.

### Threading

- `Manager.mu` guards the registry, the compiled bindings and their state, the outputs, the faults and the field
  action queue. Nothing slow happens under it: score changes, bus events, sends to transports and database writes
  are collected while it is held and carried out once it is released (`deferredWork`, run by `Manager.unlock`).
- Database writes go through `Manager.save`, serialised by `storeMu`, which is taken before `mu` and never while
  `mu` is held; each write stores the record as it is in memory at that moment, so writes cannot land out of
  order. The arena loop never writes: transports (a new pending device, a counter total, going offline) and the
  pages do.
- Transports call the `Sink` methods from their own goroutines, in the order each device's messages arrived (MQTT
  hands every message to the manager on the publishing client's goroutine).
- Score changes go to `Arena.ApplyScoreChange`, which takes the score lock itself. A refused change takes `mu`
  again to record the refusal.
- Lock order: `storeMu`, then `Manager.mu`, then the arena's `scoreMutex` (output expressions run through
  `Snapshot.Eval`, which takes it). The arena never calls the manager while it holds `scoreMutex`.
- Sending never blocks: `SendOutput`, `SendCommand`, `SendField` and `AccessChanged` return at once. The MQTT
  transport queues messages in an outbox drained by a goroutine of its own; outputs coalesce per topic, so the
  latest command of a channel is never lost; device commands and sounds queue in order up to a limit. A
  disconnection it makes runs on a goroutine of its own.
- The MQTT transport's `presenceMu` is held while a connection is recorded and reported, so that `Online` and
  `Offline` reach the manager in the order they happened even when a device reconnects before its old connection
  is seen to go; only a device's current connection counts.
- The PLC is read on the arena goroutine (`UpdatePlc`), where the arena reads it anyway.

### Plugging in

- **A transport** (WebSocket, HTTP, serial) implements `device.Transport` and is added with `AddTransport`. When
  a device connects it calls `Authenticate` (or `AuthenticateLocal`, without a token) and then `Online`; it decodes
  messages with `ParseManifest`, `ParseStatus`, `ParseReading` and `ParseHeartbeat`, hands them to `Manifest`,
  `Status`, `Input` and `Heartbeat`, reports what it cannot read with `Malformed`, and calls `Offline` when the
  device goes. `OutputsOf(id)` is what to send a device that connects; `Access(id)` is what it may do now;
  `AccessChanged` asks the transport to make the device reconnect, or drop it. A transport that implements
  `StatusReporter` says how it is doing on the Devices page.
- **The Lighting engine** claims the output channels of its fixtures with `ClaimOutputs(owner, refs)` (refused
  for a channel the bindings drive; a binding to a claimed channel is refused in turn), registers
  `OnOutputTick(func(*Snapshot))`, which runs on the arena goroutine after the bindings are evaluated, and sends with
  `DriveOutput(owner, ref, command)`, which like the bindings only sends what changed. `ReleaseOutputs(owner)`
  clears its channels.
- **The pages** use the manager API, `Arena.DeviceVirtual.Connect(manifest)` for virtual devices, and the
  `device.*` bus events.
- The broker library (mochi-mqtt) is only used in `device/mqtt_mochi.go` and `device/mqtt_ws.go`, behind the
  `broker` interface. Replacing it means writing those again. `mochiBroker.Repair` works around a race in its
  session takeover that can leave a reconnected device deaf.
- Tests speak MQTT through `device/mqtttest`, a minimal MQTT 3.1.1 client, rather than a client library, so that
  `go.mod` gains only the broker.
