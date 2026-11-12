# Game definition reference

A game is one YAML file, `games/<id>/game.yaml`. It describes the alliances, the phases of a match, what can be
scored, the fouls, the rules that turn raw scoring into points and ranking points, and how teams are ranked.
Nothing about a game lives in Go code.

Three examples ship with Galaxia FMS:

| Game | Read it for |
|------|-------------|
| [`games/ball-count`](../games/ball-count/game.yaml) | The smallest useful game. Start here. |
| [`games/orbital-rush`](../games/orbital-rush/game.yaml) | Every feature of the schema, each with a comment. |
| [`games/rebuilt-style`](../games/rebuilt-style/game.yaml) | A real FRC game (2026) expressed as data. |

## Working on a game from the terminal

```bash
galaxia-fms game validate games/my-game
```

```bash
galaxia-fms game test games/my-game
```

```bash
galaxia-fms game score games/my-game state.yaml
```

- `validate` checks the file and prints each problem with its line number.
- `test` runs the worked examples in `games/<id>/tests.yaml` ([format below](#worked-examples)).
- `score` prints the breakdown, score and ranking points for one raw state. Add `-json` for everything.
- `game schema` prints the JSON Schema, for editors that validate YAML as you type.

A file is checked in two passes. The JSON Schema ([`gamedef/game.schema.json`](../gamedef/game.schema.json))
checks the structure. Then every expression is compiled and every name it reads is checked against the game, so
`own.bals.total` is reported as `"own" has no property "bals" (did you mean "balls"?)` when the file is loaded,
not discovered during a match.

## The file, section by section

Only `schema`, `id`, `name`, `alliances` and `match` are required.

### Header

```yaml
schema: 1
id: ball-count            # lowercase letters, digits and dashes; must equal the directory name
name: Ball Count
version: "1.0"
description: Robots score balls.
```

### alliances

```yaml
alliances:
  size: 3                 # robots per alliance, 1 to 4
  list:                   # 1 to 4 alliances
    - { id: red, name: Red, short: R, color: "#d32f2f", ds: red }
    - { id: blue, name: Blue, short: B, color: "#1565c0", ds: blue }
```

- `short` is one capital letter. Station ids are built from it: `R1`, `R2`, `B1`.
- `ds` is the colour the driver station and robot are told, `red` or `blue`. The driver station protocol has
  no other colours. If you leave it out, alliances alternate red, blue, red, blue.

### params

Constants an event can change from the settings page without editing the file.

```yaml
params:
  bonus_threshold: { name: Bonus threshold, type: int, default: 20, min: 0, max: 99 }
  overdrive: { type: bool, default: true }
```

`type` is `int`, `number` or `bool`; it is inferred from `default` if omitted. Read them as
`params.bonus_threshold`.

### match

```yaml
match:
  countdown: { enabled: true, voice: drivers_behind_lines, beats: 3, quick_beats: 1 }
  phases:
    - id: auto
      name: Autonomous
      duration: 15
      robots: auto          # auto | teleop | disabled
      cues:
        - { at: start, sound: match_start }
        - { at: end, sound: phase_end }
    - { id: pause, duration: 2, robots: disabled }
    - id: teleop
      duration: 120
      robots: teleop
      cues:
        - { remaining: 20, sound: warning, id: endgame }
  score_grace: 2
  ds_game_data: "field.gate_open ? 'G' : ''"
```

- Any number of phases in any order. A `disabled` phase is a pause.
- A cue fires at `at: start`, `at: end`, `remaining: <seconds>` or `elapsed: <seconds>`. It can play a `sound`.
  Give it an `id` to let triggers and device outputs react to it.
- `score_grace` is how many seconds after a phase ends an input is still credited to that phase. It covers a
  ball that was in the air at the buzzer.
- `ds_game_data` is an expression giving the game-specific string sent to each driver station. It can read
  `station.id`, `station.alliance`, `station.position` and `station.team`.
- Phase ids `total`, `expr`, `post`, `idle` and `countdown` are reserved.

### elements

What can be scored. Each element has a `type` and a `scope`.

| Type | Stores | In an expression |
|------|--------|------------------|
| `counter` | A count for each phase | `own.balls.total`, `own.balls.auto` (one name per phase) |
| `boolean` | True or false, and the phase it became true in | `own.beacon` |
| `enum` | One of its `options`, and the phase it was set in | `own.shield == 'full'` |
| `timer` | Seconds accumulated between start and stop | `own.hold.seconds`, `own.hold.running` |
| `number` | Any number, such as a weight | `own.payload` |

| Scope | One value per | Read as |
|-------|---------------|---------|
| `alliance` | alliance | `own.balls` |
| `robot` | robot position | `own.robots[0].climb`, or `count(own.robots, #.climb == 'high')` |
| `field` | match | `field.storm` |

```yaml
elements:
  - id: balls
    name: Balls
    type: counter
    scope: alliance
    min: 0
    max: 99
    points: { auto: 4, teleop: 2 }
    edit: [device, scorer]
    phases: [auto, teleop]
    cycle: true
    ui: { group: Scoring, widget: stepper, order: 1 }
```

`points` takes one of three forms:

| Form | Meaning |
|------|---------|
| `points: 3` | Counter: 3 per count. Boolean: 3 if true. Timer: 3 per second. Number: 3 times the value. |
| `points: { auto: 4, teleop: 2 }` | Counter: per count, by the phase it was scored in. Boolean: by the phase it became true in. Phases not listed earn nothing. |
| `points: { expr: "..." }` | Any expression. `value` is this element's value; for a robot element `robot` is the robot. |

An `enum` puts points on its options instead, as a number or a by-phase map:

```yaml
    options:
      - { id: none, points: 0 }
      - { id: low, points: 4 }
      - { id: high, points: { teleop: 10, endgame: 6 } }
```

Other keys:

- `edit`: who may change the element while the match is running: `device`, `scorer`, `referee`. Default: all.
  A scorer can always edit every element after the match.
- `phases`: the phases in which live changes are accepted. Default: all.
- `initial`: starting value for a boolean, enum or number. An enum otherwise starts at its first option.
- `min`, `max`: limits for a counter's total, a number, or a timer (`max` only).
- `step`, `unit`: hints for number inputs.
- `cycle: true`: a change to this element marks the end of a scoring cycle in practice statistics.
- `ui`: `group`, `widget` (`stepper`, `toggle`, `buttons`, `select`, `timer`, `slider`, `input`), `order`,
  `hidden`. Hints for the generated scoring panel.

A field element cannot have `points`, because it belongs to no alliance. Use a derived value.

### fouls and cards

```yaml
fouls:
  types:
    - { id: minor, name: Foul, points: 2, to: opponents }
    - { id: major, name: Tech foul, points: 8 }
    - { id: warning, name: Warning, points: 0, to: none }
  rules:
    - { id: G1, name: Pinning, foul: minor, text: Holding an opponent in place for more than five seconds. }
cards:
  - { id: yellow, color: "#fdd835", second_becomes: red }
  - { id: red, color: "#d32f2f", disqualifies: true }
```

- `to: opponents` (the default) credits the points to every other alliance. `to: none` is a penalty with no
  points that rules can still read.
- `rules` is the catalogue the referee panel offers. The same rule id may appear once per foul type.
- Read fouls as `own.fouls.count`, `own.fouls.major.count`, `own.fouls.major.points`. `own.foul_points` is
  what an alliance received from the others.
- `own.cards` is the list of card ids held by the alliance's robots.

### derived

Values computed from raw state. They are how you write bonuses, thresholds, multipliers and cooperative
conditions.

```yaml
derived:
  - id: high_climbs                   # a value: read it as own.high_climbs
    scope: alliance
    expr: "count(own.robots, #.climb == 'high')"

  - id: climb_bonus                   # with "points" it adds to the score: read it as own.points.climb_bonus
    scope: alliance
    points: "own.high_climbs >= 2 ? 10 : 0"

  - id: coop                          # field scope: read it as field.coop
    scope: field
    expr: "all(alliances, #.balls.total >= params.coop_threshold)"
```

A derived value has either `expr` or `points`, not both. Values may read each other; they are evaluated in
dependency order, and a circle is an error.

### triggers

A trigger writes a value into raw state once, at a moment in the match. Use one when the answer cannot be
recomputed later, such as a coin flip.

```yaml
triggers:
  - on: { phase_start: teleop }       # or phase_end: <phase>, or cue: <cue id>
    set:
      element: auto_winner
      value: "red.balls.auto > blue.balls.auto ? 'red' : blue.balls.auto > red.balls.auto ? 'blue' : pick('red', 'blue')"
```

- The target is a boolean, enum or number element of field or alliance scope. For an alliance element the
  expression runs once per alliance, with `own` set.
- `pick(a, b, ...)` returns one argument at random. It is only allowed here.
- The result is recorded like any other score change, with the rule as its source, so it appears in the audit
  log and a scorer can override it.

### live

Values that may read the clock. They are for displays and device outputs. They can never affect the score: a
scoring expression that mentions `phase` is rejected.

```yaml
live:
  - id: goal_open
    scope: alliance
    expr: "match_state == 'running' and (phase == 'auto' or field.auto_winner == own.id)"
```

Extra names here: `match_state` (`idle`, `countdown`, `running`, `post`, `timeout`), `phase`, `phase_elapsed`,
`phase_remaining`, `phase_duration`, `match_time`, `match_remaining`. A live value may read the ones above it.

### breakdown

The rows of the final score screen, the announcer view and exports.

```yaml
breakdown:
  - { id: auto_points, name: Auto, expr: "own.phase_points.balls.auto + own.points.leave" }
  - { id: foul_points, name: Fouls, expr: "own.foul_points" }
```

The alliance score is always the points of every element, plus every derived value with points, plus
`foul_points`. The breakdown is presentation only. A row may read the rows above it as `own.breakdown.<id>`.

### ranking

```yaml
ranking:
  points:                             # per match, per alliance; a true condition counts as 1
    - { id: win, name: Win, expr: "result == 'win' ? 2 : result == 'tie' ? 1 : 0" }
    - { id: bonus, name: Ball bonus, expr: "own.balls.total >= params.bonus_threshold" }
  stats:                              # summed per team over the matches it played
    - { id: match_points, expr: "own.score - own.foul_points" }
  order:                              # how teams are sorted: first key, then tiebreakers
    - { name: Ranking score, expr: "rp / played", decimals: 2 }
    - { name: Avg match, expr: "match_points / played", decimals: 1 }
    - { name: Fewest fouls, expr: "fouls", dir: asc }
    - { random: true }
```

- `result` is `win`, `loss` or `tie`. With more than two alliances, the single highest score wins and everyone
  else loses; alliances sharing the highest score tie.
- `order` expressions read a team's totals: `rp`, `played`, `wins`, `losses`, `ties`, `disqualifications`, and
  each stat by id. Higher is better unless `dir: asc`. `random: true` is a coin flip stored per team.
- A team with no matches played sorts last.
- A team disqualified from a match (a card with `disqualifies: true`) has it counted as played and earns
  nothing from it.
- If `order` is left out, teams are ranked by ranking points per match, then at random.

### playoff

```yaml
playoff:
  tiebreakers:
    - { name: Fewer tech fouls, expr: "-own.fouls.major.count" }
    - { name: Auto points, expr: "own.breakdown.auto_points" }
```

Applied in order when a playoff match is tied; the higher value wins. If all are equal the match is a tie.

### layout

Hints for the generated screens.

```yaml
layout:
  scoring_panels:                     # one per scorer tablet
    - { id: red_near, name: Red near side, alliances: [red], groups: [Scoring, Climb] }
  audience:
    live: [balls.total, high_climbs]  # shown beside the score during the match
    indicators: [goal_open]           # live or derived values shown as lit or unlit badges
  team_sign: "string(own.balls.total)"
```

### tba

Maps the game onto The Blue Alliance's official score breakdown keys for a year. The Blue Alliance rejects
keys it does not know, so leave this out for an invented game.

```yaml
tba:
  year: 2026
  breakdown:
    totalAutoPoints: "own.breakdown.auto_points"
    foulPoints: "own.foul_points"
```

## Expressions

Expressions use the [expr language](https://expr-lang.org/docs/language-definition). They cannot loop forever,
assign, or reach outside the values listed here.

```text
own.balls.total >= 20 ? 5 : 0
count(own.robots, #.climb == 'high') >= 2
sum(alliances, #.balls.total)
all(alliances, #.balls.total >= params.coop_threshold)
max(map(opponents, #.balls.total))
own.beacon and not own.momentum
floor(value.seconds / 5) * 2
let n = count(own.robots, #.parked); n * n
```

Inside `count`, `sum`, `all`, `any`, `filter`, `map` and `find`, `#` is the current item.

| Name | What it is |
|------|------------|
| `own` | The alliance being evaluated |
| `opp` | The other alliance. Only exists in two-alliance games. |
| `opponents` | List of every other alliance |
| `alliances` | List of all alliances |
| `red`, `blue`, ... | Each alliance by its id |
| `field` | Field elements and field derived values |
| `params` | Event parameters |
| `value`, `robot` | Inside an element's own `points` expression |
| `result` | In ranking expressions |

Properties of an alliance:

| Property | Meaning |
|----------|---------|
| `id`, `name` | |
| `<element id>` | An alliance element's value |
| `robots` | List of robots; each has `position`, `team` and the robot elements |
| `<derived id>` | An alliance derived value |
| `points.<id>` | Points earned by an element (summed over robots) or by a derived value with points |
| `phase_points.<element>.<phase>` | The part of an element's points earned in one phase |
| `fouls.count`, `fouls.<type>.count`, `fouls.<type>.points` | Fouls called against this alliance |
| `foul_points` | Points this alliance received from the others' fouls |
| `cards`, `disqualified` | |
| `score`, `breakdown.<row>`, `rp.<id>`, `rp.total` | Only after the score exists: in breakdown, ranking, playoff, live and tba expressions |

Functions beyond expr's own: `clamp(value, low, high)` and, in triggers, `pick(...)`.

Things to know:

- `/` always gives a decimal. Use `floor()` when you want whole points.
- A condition used as a number counts as 1 when true.
- Element, derived and live ids share one namespace and cannot be `id`, `name`, `robots`, `points`,
  `phase_points`, `fouls`, `foul_points`, `cards`, `disqualified`, `breakdown`, `score`, `rp`, `position`,
  `team`, `total`, `seconds`, `running` or `count`.

## Worked examples

`games/<id>/tests.yaml` holds raw states and the results they must produce. Write one whenever a rule is easy
to get wrong.

```yaml
cases:
  - name: Red wins on balls
    params: { bonus_threshold: 10 }   # optional parameter overrides
    playoff: false                    # true applies the playoff tiebreakers
    state:
      red:
        balls: { auto: 3, teleop: 10 }            # counter: count per phase
        hold: 12.5                                # timer: seconds
        payload: 11                               # number
        beacon: true                              # boolean
        shield: { value: full, phase: teleop }    # boolean or enum with the phase it was set in
        robots:
          - { leave: true, climb: high }
          - { climb: low }
        fouls: [minor, { type: major, rule: G1, robot: 2 }]
        cards: { 1: yellow }
        disqualified: false
      blue: { balls: { teleop: 9 } }
      field: { auto_winner: red }
    expect:
      winner: red                     # an alliance id, or "none" for a tie
      tiebreak: Auto points
      field: { coop: false }
      red:
        score: 44
        result: win
        foul_points: 0
        points: { balls: 32 }
        breakdown: { auto_points: 15 }
        values: { high_climbs: 1 }
        rp: { win: 2 }
        rp_total: 3
        stats: { match_points: 44 }
```

Only what you list under `expect` is checked. The `state` form is the same one `game score` reads.
