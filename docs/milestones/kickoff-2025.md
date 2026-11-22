# A kickoff exercise: the 2025 game from its manual alone

Done 2026-11-22. The task was to make Galaxia FMS run a game it had never seen, given only that game's manual, the
way a field crew gets a new game on kickoff day. The game is the 2025 FRC game. Nothing was taken from any other
source: not from Cheesy Arena's own 2025 code, not from the web, not from memory of the season.

The result is one game file, [`games/reefscape/game.yaml`](../../games/reefscape/game.yaml), its worked examples in
[`games/reefscape/tests.yaml`](../../games/reefscape/tests.yaml), and five additions to the engine that the game
needed and that are not about this game.

## What works

Choose **Reefscape** on Setup → Settings → Game and everything follows from the file:

- **The match**: 15 seconds of autonomous, the manual's 3 seconds for scoring, 2:15 of teleop, with a sound at each
  of the five moments the manual lists.
- **Scoring panels**: the reef is a grid of buttons, levels 4 to 2 from the top and branches A to L across, with
  the trough as a counter under it. Below is the same grid for what was on the reef when autonomous ended. Leave,
  algae and the barge are on a second panel, and there is a panel with everything for a single scorer.
- **The rules that are easy to get wrong** (section 6.5.1):
  - coral scored in autonomous keeps its autonomous value while it stays;
  - knocked off, it loses its points and still counts for the autonomous ranking point;
  - scored on the same branch again, it gets its autonomous value back;
  - coral leaving the trough is taken from the teleop coral first, and coral coming back counts as the autonomous
    coral first.
- **Ranking points**: win and tie, autonomous (every robot that was not bypassed left, and a coral was scored in
  autonomous), coral (enough on all four levels, or on three once both alliances have two algae in their
  processors), barge. The two thresholds the manual says may be raised are event parameters.
- **Fouls**: minor and major, and a referee's rule list of 40 entries in this project's own words. Three rules do
  more than give points, and calling them is enough: coral taken off the opponent's reef hands them the coral
  ranking point, contact with a cage or a climbing robot hands them the barge ranking point, and collusion makes
  an alliance ineligible for both.
- **Ranking order and playoff tiebreakers** as tables 10-1 and 10-3 give them.
- **Displays**: coral and algae counts beside the score, badges for co-op and the three bonus ranking points, the
  final score with a row for leave, autonomous coral, teleop coral, algae, barge and fouls. Team signs show the
  coral on each level.
- **Electronics**: the processor counter accepts a sensor, as the manual's own field does, and so does every
  branch. Two values are ready for lights: how many of the three barge lights are lit for each alliance, and
  whether the last 20 seconds have begun.

## How the manual became the file

| In the manual | In the file |
|---------------|-------------|
| 5.3, figure 5-8: branches tracked by place | 36 booleans, `l2_a` to `l4_l`, in a grid |
| 6.4: periods, and the 3 seconds between them | `match.phases`, with a `pause` of 3 |
| 6.5: assessment goes on for 3 seconds | `score_grace: 3`, and the latch is taken when teleop starts |
| 6.5.1: autonomous coral removed and scored again | 36 more booleans, `auto_l2_a` to `auto_l4_l`, and `auto_trough`, filled in by triggers |
| 6.5.2: leave, park, cages | a boolean and an enum for each robot |
| 6.5.3: coopertition | the field value `coopertition`, read by the coral ranking point and by the ranking order |
| Table 6-2: points and ranking points | `points` on elements, two derived values for coral, `ranking.points` |
| Table 6-3 and section 7: violations | `fouls.types` and `fouls.rules` |
| 10.5.3, table 10-1: ranking | `ranking.stats` and `ranking.order` |
| 10.6.2.1, table 10-3: playoff ties | `playoff.tiebreakers` |
| 5.4: lights on the barge | the live values `barge_lights` and `last_seconds` |

## What the engine needed

Each of these is general. None names anything of this game.

- **Who played.** "Every robot that was not bypassed" needs the score to know which robots took part. A robot in
  an expression now has `bypassed`. The arena records it when the match starts, for each team whose station is
  bypassed; a station without a team already counts. Match review can correct it.
- **A grid on the scoring panel.** 36 yes-or-no places as 36 separate controls is not something a person can score
  with. `ui.row` and `ui.column` put the booleans of a group in a grid of compact buttons, and match review edits
  the same grid as a table.
- **Taking away what an earlier phase scored.** A counter is kept per phase, and a removal was refused if the
  running phase held nothing: coral scored in autonomous could not be taken out of the trough in teleop. The
  count now comes off the phase that holds it.
- **Triggers that change nothing make no change, and the triggers of one moment are one step.** Found by playing
  the match: the 74 values this game copies when teleop starts were 74 changes, most of them an empty place set
  to empty. They pushed the scorer's own actions out of the list of recent changes, and each one computed the
  score again, in the pass that tells the robots teleop has begun: by the benchmark below about 34 ms, which
  was not timed in the match. Now only what changes is recorded and the score is computed twice, about 1 ms.
- **`game score` says when a state file has no `state:` key**, instead of scoring an empty match.

## Verification

- **Worked examples**: 31 cases, each worked out by hand from the manual's point table before it was run. All of
  them passed the first time they ran, which says the file and the hand calculation agree, not that both are
  right; read [`tests.yaml`](../../games/reefscape/tests.yaml) against the manual to check.
- **Go tests**, new ones: the latch through a played match with the real example game
  (`TestReefscapeLatchInAMatch`), bypassed teams and the autonomous ranking point (`TestBypassedTeamsAreRecorded`),
  removal reaching an earlier phase, `bypassed` in the engine, triggers that change nothing, the grid's
  validation, bypass edits in match review, the `game` command. `go test ./...` passes on all 20 packages, and
  the race detector reports nothing.
- **A match in a browser**, on a test copy with its own port, database and broker ports, scored through the real
  scoring panel and referee panel:

  | Step | Red score | What it shows |
  |------|-----------|---------------|
  | Autonomous: L4 A, L3 B, two in the trough, three robots leave; teleop begins | 28 | Latch taken: 7 + 6 + 2 × 3, and 9 for leave |
  | L4 B and a third coral in the trough | 35 | Teleop values: 5 and 2 |
  | L4 A knocked off | 28 | Its 7 points go; it still counts as scored in autonomous |
  | L4 A scored again | 35 | 7 again, not 5 |
  | Two coral leave the trough | 30 | The teleop coral first (2), then an autonomous one (3) |
  | Referee: blue took coral off red's reef | 36 | 6 points, and red has the coral ranking point |
  | Two algae in the processor, one in the net, deep cage, shallow cage, parked | 72 | 12 + 4 + 20, and the barge ranking point |
  | Scorer marks L4 B as scored in autonomous | 74 | The latch corrected during teleop: 7 instead of 5 |

  The final score screen showed 9, 23, 0, 16, 20 and 6, total 74. The start of teleop recorded 74 changes in
  that match. In a second match, after the trigger change, it recorded 3.
- **One computation of the score** takes 0.46 ms for this game, against 0.03 ms for the smallest example and
  0.09 ms for the 2026 one (`BenchmarkCompute`, on the development laptop).

## Where I had to decide

The manual says what a rule is, not how a field keeps track of it. These are my readings.

- **When the latch is taken**: at the start of teleop, three seconds after autonomous ends. A scorer who is later
  than that corrects the lower grid.
- **Autonomous coral in the trough** is the smaller of what is there now and what was there at the latch. The
  manual's two sentences about removal and return come out of that, and the phase a trough coral was counted in
  does not matter.
- **Coral scored in autonomous** for the ranking point includes trough coral that has since left, as it does for
  branches.
- **The coral ranking point** counts coral on the reef when the match is assessed, not coral that was there
  earlier.
- **A station without a team** counts as bypassed.
- **Being ineligible** (G206) takes away the coral and barge ranking points only, as the rule lists them.

## What is not there

- **The Blue Alliance**: no `tba` section. Its field names for 2025 are not in the manual.
- **Three seconds of barge lights after the match** (5.4): a live value cannot read the clock once the match has
  ended.
- **G419** makes one robot ineligible for cage points. The file cannot tie a foul to a robot's result; the scorer
  sets that robot's barge result, and the rule's text says so.
- **G418 "in a qualification match"**: the file cannot tell the kind of match. The foul type is the same in a
  playoff match, where ranking points are not awarded anyway. For the same reason the co-op badge also lights in
  a playoff match.
- **Cage heights** (6.3.5): which cage hangs shallow or deep for the next match is not recorded.
- **Near-side and far-side scorers**: the reef panel shows the whole reef. A crew that splits it needs two more
  panels and a choice of which branches each sees.
- **After the match, the trough counter asks which phase** a correction belongs to, as every counter does. For
  this game the answer does not change the score.
- **Lights and sensors are not bound** by the file. The values are there; binding them to devices is done on
  Setup → Bindings and Setup → Lighting, and none of it was tried on hardware.
- **Not looked at**: surrogates, the "count" pause before scores are final, and how cards carry over between
  matches. They are not game-specific and were not changed.
