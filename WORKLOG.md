# Galaxia FMS work log

A running record of the work on Galaxia FMS: what was done, why, what was checked, and what is left. Newest
entries are at the bottom. Times are Israel time, from the development laptop.

- The plan and its decisions: [docs/PLAN.md](docs/PLAN.md)
- How a match runs in code: [docs/architecture.md](docs/architecture.md)
- Writing a game: [docs/game-definition.md](docs/game-definition.md)
- Per-milestone reports: [docs/milestones/](docs/milestones/)

## Before this log started (2026-11-12 to 2026-11-13)

A summary of the work done before David asked for a log.

**Setup**
- Installed Go 1.27.4 with winget into `C:\Program Files\Go`.
- Windows Smart App Control blocks `gofmt.exe` and sometimes freshly built test binaries. Formatting uses
  `go run cmd/gofmt` instead. The setting itself was left alone.
- GitHub Actions cannot run: GitHub reports the account is locked for a billing issue. Only David can clear it,
  from github.com → Settings → Billing and plans. Until then all testing is local.

**Decisions (D1-D10 in docs/PLAN.md)**
- Work on `main` of the fork, renamed to `DavidMasin/galaxia-fms`. The previous fork content is kept on the branch
  `legacy/community-2026`. Only the radio configuration change was carried over from it.
- The game definition schema and the device protocol in the plan were approved as drafted.
- Expressions use `expr-lang/expr`.
- Both alliance formats: the official 2 x 3 with brackets, and 1-4 alliances of 1-4 robots without brackets.
- A dedicated Lighting page for LEDs (David: "very important").

**Milestone 1 (on main):** fork reset and rename, notice file, line endings, modernised CI workflow, the
"radio only" network setting.

**Milestone 2 (on main):** the game engine (`gamedef`, `rules`, `scoring`), the three example games with worked
examples, the `galaxia-fms game validate|test|score|schema` commands, the game definition reference.

**Milestone 3, first part (on main):** the event bus, the game registry with hot reload, and the browser game
editor with a test-match sandbox (Setup → Games).

**Milestone 3, second part (local branch `wip/m3b-core`, not merged yet)**
- The arena runs matches from the selected game: phases, cues, sounds, triggers, game data to robots.
- Live scoring through audited changes with undo; results stored with a copy of the game they were played with.
- Scoring and referee panels generated from the game; match review editor with the audit log.
- Every display renders from the game.
- Rankings and The Blue Alliance breakdown follow the game.
- The hard-coded `game` and `led` packages are deleted.
- All Go tests pass. A browser check was half done: starting a match, scoring, undo, the audience overlay and the
  referee panel worked. Seen but not yet investigated: the "Autonomous winner" buttons on the scoring panel did not
  show a selection during teleop.

---

## 2026-11-13 17:13 - Work log started; devices move ahead of the API

**Asked:** David chose to build the device framework next, before the API milestone, and asked for everything to
be documented in a log file in the FMS folder.

**Decided**
- This file, `WORKLOG.md` in the repository root, is that log. It is committed with the code, so it is also
  backed up on GitHub.
- Milestone order is now: finish milestone 3, then devices (milestone 5) and firmware (milestone 6), then the API
  (milestone 4), then the rest.
- Running three- and four-alliance games on the field (the planned "milestone 3c", generic alliances and
  stations) moves to after the device work. Reason: it is a large change to the match record, schedule generator
  and network code, and electronics matter more for the tent field. The device framework is written against
  alliance ids and robot positions, so it will not need changing when that work lands.

**Next:** finish the milestone 3 browser check, starting with the autonomous winner display.
