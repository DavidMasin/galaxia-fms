Galaxia FMS [![CI](https://github.com/DavidMasin/galaxia-fms/actions/workflows/test.yml/badge.svg)](https://github.com/DavidMasin/galaxia-fms/actions)
===========

A field management system for an FRC practice field that can run any game from a definition file and treats
field electronics as plug-and-play devices.

Galaxia FMS is built on [Cheesy Arena](https://github.com/Team254/cheesy-arena) by Team 254. It keeps Cheesy
Arena's match control, driver station communication, network configuration, scheduling and publishing, and
replaces the parts written for one specific game. See [NOTICE.md](NOTICE.md).

## Status

Work in progress. The plan, the design and the milestone list are in [docs/PLAN.md](docs/PLAN.md); a report
for each finished milestone is in [docs/milestones/](docs/milestones/).

Until milestone 3 lands, the running application still plays the game that upstream Cheesy Arena ships with.

## Building and running

1. Install [Go](https://go.dev/dl/) 1.26.7 or later.
1. Clone this repository and open a terminal in it.
1. Build with `go build`.
1. Run `./galaxia-fms` (`galaxia-fms.exe` on Windows) from the repository directory.
1. Open http://localhost:8080 in a browser (Google Chrome recommended).

When running without robots, pass `-dev` so the driver station listeners bind to every address instead of
only `10.0.100.5`.

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
