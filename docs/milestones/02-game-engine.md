# Milestone 2: game engine

Finished 2026-11-12.

## What works

A game is now a YAML file, and the engine that reads it knows nothing about any particular game.

- **`gamedef`** loads `games/<id>/game.yaml`. It validates the structure against a JSON Schema
  (`gamedef/game.schema.json`, draft 2020-12), then checks everything the schema cannot: duplicate and reserved
  ids, references to phases, elements, foul types and cards that do not exist, and every expression. Problems
  are reported with a line number.
- **`rules`** compiles expressions with `expr-lang/expr` and checks every name an expression reads against the
  game. A typo gets a "did you mean" suggestion at load time. Scoring expressions cannot read the clock.
- **`scoring`** holds the raw state (counts per phase, flags, selections, timers, numbers, fouls, cards), the
  audited change list, undo and redo, and the computation of points, score, breakdown, winner, playoff
  tiebreakers, ranking points, ranking stats and the ranking order. It also evaluates triggers and live values.
- **Three example games**, each with worked examples:

  | Game | Shape | Worked examples |
  |------|-------|-----------------|
  | `ball-count` | 2 alliances of 3, 3 phases, 1 element | 4 |
  | `orbital-rush` | 3 alliances of 2, 5 phases, 16 elements; uses every schema feature | 10 |
  | `rebuilt-style` | 2 alliances of 3, 8 phases; the 2026 FRC game as data | 15 |

- **`galaxia-fms game ...`** commands: `validate`, `test`, `score`, `schema`.
- **Reference documentation**: [docs/game-definition.md](../game-definition.md).

`rebuilt-style` reproduces the cases from Cheesy Arena's own 2026 score tests (`TestScoreSummary`, the auto
tower table, the post-match fuel case, the bonus thresholds, the collusion rule, playoff disqualification and
the three playoff tiebreakers) and gets the same numbers from the generic engine.

## How to try it

```bash
go build
```

```bash
./galaxia-fms game validate games
```

```bash
./galaxia-fms game test games
```

To score a state of your own, write a file like this and pass it to `game score`:

```yaml
state:
  red: { balls: { auto: 3, teleop: 10 }, fouls: [foul] }
  blue: { balls: { teleop: 12 } }
```

```bash
./galaxia-fms game score games/ball-count state.yaml
```

To see the validator at work, misspell a name inside any expression in a copy of a game and run `validate`.

## Verification

- 53 Go tests pass locally across `rules`, `gamedef` and `scoring`, including 28 cases that each break a valid
  game in one way and check the message and line number.
- All 29 worked examples pass through both the Go test and the `game test` command.
- A test walks the JSON Schema and fails if any property is not used by an example game; `orbital-rush` alone
  must use everything except the Blue Alliance mapping.
- I confirmed the tools report failures: a deliberately wrong expectation and a deliberately broken game are
  both caught.
- Not verified: anything on Linux, because CI cannot run (see [milestone 1](01-fork-setup.md)).

## Differences from the approved draft

Small ones, found while implementing:

- A boolean or enum is read as a plain value (`own.beacon`, `own.shield == 'full'`). The draft also offered a
  `.phase` property on them; a value cannot be both. The phase is still stored and used for by-phase points.
- Per-phase points are read as `own.phase_points.<element>.<phase>`, separate from `own.points.<element>`, for
  the same reason.
- The Blue Alliance mapping is exercised by `rebuilt-style`, not by `orbital-rush`: a three-alliance game
  cannot be published there.

## What is left

- The running application does not use the engine yet. It still plays upstream's hard-coded 2026 game until
  milestone 3 replaces the arena's scoring, panels and displays.
- Hot reload and choosing a game per event arrive with milestone 3, when there is something to reload into.
- Per-game template and CSS overrides are in the plan but have no renderer to override until milestone 3.
- Nested Blue Alliance keys (the 2026 `hubScore` block) and the publish-time validator are milestone 10.
