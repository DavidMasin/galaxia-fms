# Milestone 3: matches, panels and displays run from the game

Finished 2026-11-13. Generic alliances and stations, first planned as the end of this milestone, moved to after
the device work (see [WORKLOG.md](../../WORKLOG.md)).

## What works

The whole application now runs from the selected game definition. The hard-coded 2026 game (the `game` package)
and the 2026 hub lighting (the `led` package) are deleted.

- **Match flow.** The phases, robot modes, cues, sounds and rules of a match come from the game. Rules that fire
  at a moment in the match work; in `rebuilt-style` the autonomous winner is decided when teleop starts and sent
  to the robots as game data. Robots see one countdown for each stretch in which their mode is the same, so a
  teleop split into several phases counts down once.
- **Choosing the game.** Setup → Settings lists every game, marks the ones that cannot be played on the field
  yet, and has one input per game parameter. A change made during a match takes effect at the next pre-match.
- **Scoring panels.** One per entry in the game's `layout.scoring_panels`, or one per alliance by default. Each
  control is built from the element's type and widget. Controls lock outside the phases the element accepts.
  Robot rows show team numbers. Every change appears in a "Recent changes" list with its own Undo button.
- **Referee panel.** Fouls by type, with an optional rule (filtered to rules for that foul type) and robot. Cards
  come from the game's list, and a second card of the same kind upgrades when the game says so.
- **Audit log and corrections.** Every score change records time, source, phase and the phase it was credited
  to. Run → Match Review → Edit shows the full log with Undo and Redo, and edits any value, foul or card. Saving
  recomputes the winner, rankings and bracket.
- **Results keep their game.** Each stored result carries a copy of the game file it was played with, and is
  always recomputed with it.
- **Displays.** The audience overlay, final score, wall, alliance station, both field monitors, announcer,
  rankings and queueing displays read names, colours, breakdown rows, ranking columns and live indicators from
  the game.
- **Rankings and The Blue Alliance** follow the game's ranking points, stats and tiebreakers, and its `tba`
  mapping.
- **Startup options** `-port`, `-db` and `-server-ip`, so a second copy can run beside the first for testing.
  `-server-ip` restores the server-address option from the old fork.
- **Neutral artwork.** The 2026 game logos on the displays and in printed reports are replaced by a Galaxia
  logo, drawn by `tools/brand/make_logos.py`.

## How to try it

```bash
go build
```

```bash
./galaxia-fms -dev
```

1. Open http://localhost:8080. Setup → Settings: pick a game (`ball-count` is the simplest).
2. Run → Match Play: tick Bypass on every station, then Start Match.
3. Open a scoring panel from the Panel menu and Display → Audience in other tabs, and score.
4. After the match, Commit & Post, then Run → Match Review → Edit to see the audit log.

## Verification

- **Go tests.** `go test ./...` passes in every package. That includes the two upstream display tests and the
  E-stop test that used to fail on Windows.
- **Game examples.** All 29 worked examples in the three games pass.
- **Browser check, with the 2026-style game:**
  - a test match with scoring and undo on the scoring panel, and the autonomous winner rule firing;
  - a major foul from the referee panel crediting the other alliance;
  - commit, and the final-score screen;
  - a practice match committed and stored with its audit log, then corrected in match review (an undo, and an
    edit that changed the winner);
  - switching the active game to `ball-count` while the old result stayed on its own game.

  These ran on a second copy of the server, on its own port and database, so David's running copy was not
  touched.

## Known limits

- Only games with two alliances of up to three robots can be played on the field. Three- and four-alliance games
  work in the editor sandbox. Generic alliances and stations come after the device work.
- Results stored by earlier builds, or by Cheesy Arena, are not converted. A fresh event database is needed.
- The generic scoring-window rule differs from the official 2026 rule in one corner: fuel arriving just after an
  inactive shift ends is not credited. The schema cannot express the official rule. Details are in
  [docs/architecture.md](../architecture.md).
- The audience overlay shows points scored after the buzzer immediately; the old overlay held them back until the
  score was posted. There is no "progress towards a threshold" readout yet.
- Not checked in a browser yet:
  - sound playback;
  - animations at full speed on a real 1080p display;
  - the bracket, alliance selection, sponsor and lower-third screens with a game other than `rebuilt-style`;
  - cards given in playoff matches.
