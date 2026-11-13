Galaxia FMS [![CI](https://github.com/DavidMasin/galaxia-fms/actions/workflows/test.yml/badge.svg)](https://github.com/DavidMasin/galaxia-fms/actions)
===========

A field management system for an FRC practice field that can run any game from a definition file and treats
field electronics as plug-and-play devices.

Galaxia FMS is built on [Cheesy Arena](https://github.com/Team254/cheesy-arena) by Team 254. It keeps Cheesy
Arena's match control, driver station communication, network configuration, scheduling and publishing, and
replaces the parts written for one specific game. See [NOTICE.md](NOTICE.md).

## Status

Work in progress. The plan, the design and the milestone list are in [docs/PLAN.md](docs/PLAN.md); a report
for each finished milestone is in [docs/milestones/](docs/milestones/), and a running record of the work is in
[WORKLOG.md](WORKLOG.md).

Matches, scoring panels, the referee panel, match review, rankings and every display run from a game definition
file. Three example games ship in [games/](games/); [docs/game-definition.md](docs/game-definition.md) explains how
to write your own. Field electronics (sensors, buttons, LED strips) are the next milestone.

## Building and running

1. Install [Go](https://go.dev/dl/) 1.26.7 or later.
1. Clone this repository and open a terminal in it.
1. Build with `go build`.
1. Run `./galaxia-fms` (`galaxia-fms.exe` on Windows) from the repository directory.
1. Open http://localhost:8080 in a browser (Google Chrome recommended).

Startup options:

| Option | Default | Use |
|--------|---------|-----|
| `-dev` | off | Accept driver stations on every network address, for running without the field network |
| `-server-ip` | `10.0.100.5` | The address driver stations connect to |
| `-port` | `8080` | The web port |
| `-db` | `./event.db` | The event database file |

A second copy can run beside the first for testing, for example
`./galaxia-fms -port 8090 -server-ip 127.0.0.2 -db test.db`.

Choose the game under Setup → Settings and edit games under Setup → Games. To check game files from a terminal:
`./galaxia-fms game validate games` and `./galaxia-fms game test games`.

Run the tests with `go test ./...`.

## Networking

Three setups are supported from the Settings page:

* **Network security off** (the default): one ordinary network. Nothing is configured automatically.
* **Network security on**: Cheesy Arena's full isolated setup, with a Vivid-Hosting VH-113 access point and a
  Cisco Catalyst 3500-series switch giving each team its own SSID and VLAN. See upstream's
  [Advanced Networking wiki page](https://github.com/Team254/cheesy-arena/wiki/Advanced-Networking-Concepts).
* **Network security on, radio only**: the access point is configured for each match's teams and the switch
  is left alone. For fields that have the radio but no managed switch.

## Licence

Galaxia FMS is a modified version of Cheesy Arena and is covered by Team 254's licence terms in
[LICENSE](LICENSE), which allow use for testing, scrimmages, off-season events and evaluation, and restrict
redistribution of modifications. Read [NOTICE.md](NOTICE.md) before reusing this code.

## Acknowledgements

[Patrick Fairbank](https://github.com/patfair) and Team 254 wrote Cheesy Arena, with
[many contributors](https://github.com/Team254/cheesy-arena/graphs/contributors). Galaxia FMS exists because
they published it.
