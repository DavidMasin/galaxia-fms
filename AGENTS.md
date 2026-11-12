# Repository Guidelines

Galaxia FMS is a field management system for FRC practice fields, built on Team 254's Cheesy Arena. It runs any game
from a definition file. Read `docs/PLAN.md` for the plan and `docs/architecture.md` for how a match runs in code.

## Project Structure & Module Organization
`main.go` is the entry point for the Go web server and for the `galaxia-fms game ...` commands.

| Path | What lives there |
|------|------------------|
| `gamedef/`, `rules/`, `scoring/` | The game engine: definition files, expressions, raw score state and computation |
| `games/` | Example game definitions (`<id>/game.yaml`, `<id>/tests.yaml`), embedded in the binary |
| `bus/` | The internal event bus |
| `field/` | The arena: stations, driver stations, the phase clock, the live score, notifiers |
| `model/` | Stored records (BoltDB) |
| `tournament/`, `playoff/` | Schedules, rankings, brackets |
| `partner/`, `network/`, `plc/` | The Blue Alliance, Nexus, access point and switch, PLC |
| `web/`, `templates/`, `static/` | HTTP handlers, pages, browser code |
| `docs/` | Plan, architecture, game definition reference, milestone reports |

BoltDB data is stored in `event.db` beside the binary. Pre-generated schedules are in `schedules/`.

## The one rule that matters most
No game-specific identifier may appear in Go, templates, JavaScript or CSS: no element, phase, foul or ranking point
of any particular game. Everything of that kind comes from the game definition (in the browser, through `GameView`
in `static/js/game_view.js`). The only exceptions are tests that explicitly load an example game.

## Build, Test, and Development Commands
See `go.mod` for what version of Go to use.
1. `go build`
   Builds the `galaxia-fms` binary in the repo root.
1. `./galaxia-fms -dev`
   Runs the server without requiring the field IP address; open `http://localhost:8080` in a browser.
1. `go test ./...`
   Runs all Go tests across packages. Should be run after making any code changes to ensure nothing is broken.
1. `go fmt ./...`
   Formats all Go code in the repo. Should be run after making any code changes to ensure consistent style.
1. `./galaxia-fms game validate games` and `./galaxia-fms game test games`
   Check the game definitions and run their worked examples.
1. `go vet -composites=false -hostport=false -structtag=false ./...`
   The three analyzers are off because of code inherited from upstream; do not add new findings.

## Coding Style & Naming Conventions
Follow standard Go style: tabs for indentation, exported names in `CamelCase`, unexported in `camelCase`, lines up to
120 columns. Format code with `gofmt` before submitting changes; line endings are LF everywhere (see
`.gitattributes`). If you update a set of enum-style constants, run `go generate ./...` to refresh the generated enum
string helpers. Keep package names short and domain-focused (matching existing directories like `field`, `scoring`,
`partner`). Comments explain why, not what.

Order imports alphabetically without any grouping or empty lines between them or special treatment of standard
library vs third-party imports. Update any files that don't adhere to this standard if editing them for other
reasons. Don't use goimports.

Browser code is plain JavaScript with jQuery and no build step. The UI, documentation and code are English only.

## Testing Guidelines
Tests are Go `*_test.go` files co-located with packages. Use `go test ./...` for the full suite and
`go test ./field -run TestName` to target specific areas. When adding new behavior, add or update tests in the same
package and prefer table-driven tests for coverage. Tests that need a game load one of the examples from `games/`;
use `ball-count` when a tiny game makes the test clearer. A rule that is easy to get wrong in a game belongs in that
game's `tests.yaml`.

## Commit & Pull Request Guidelines
Commit messages are short, imperative sentences (for example "Fix driver station TCP reads"), with a body when the
change needs explaining. Update `docs/` in the same commit as the behaviour it describes.

PRs should include:
1. A clear summary of the change.
1. Test notes (exact commands run, for example `go test ./...`).
1. UI screenshots when changing pages in `web/`, `static/`, or `templates/`.

## Configuration & Ops Notes
Galaxia FMS runs as a local web server and uses BoltDB for data. For field networking and hardware integrations, see
the project README and the relevant `field/`, `network/` or `plc/` code before making behavioral changes.

## Licence
This is a modified version of Cheesy Arena. Keep `LICENSE` and every Team 254 copyright header unchanged on files
that came from upstream; do not add those headers to new files. See `NOTICE.md`.
