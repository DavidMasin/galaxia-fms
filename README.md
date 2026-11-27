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
file. Four example games ship in [games/](games/), two of them real FRC games. The 2025 game was written from its
game manual alone, as on a kickoff day; what that took is in
[docs/milestones/kickoff-2025.md](docs/milestones/kickoff-2025.md).
[docs/game-definition.md](docs/game-definition.md) explains how to write your own.

Field electronics are plug-and-play:
- Sensors, buttons, E-stops, stack lights and LED strips connect over Wi-Fi (MQTT, WebSocket or HTTP) or USB.
- They are approved on Setup → Devices, bound to the match on Setup → Bindings, and lit on Setup → Lighting.
- SDKs for the ESP32, the Pico W and Python, with nine example projects, are in [sdk/](sdk/) and
  [examples/](examples/).
- [docs/tutorial-first-sensor.md](docs/tutorial-first-sensor.md) walks through the first sensor, and
  [docs/devices.md](docs/devices.md) is the full reference.

A match starts with a countdown ("Drivers, behind the lines", 3, 2, 1, GO) that the audience display, the alliance
station displays, field devices and lighting all count down to the same moment; robots are enabled at the end of
it. A game says what is counted, and Setup → Settings → Game can shorten it or turn it off.

A read API, an event stream and data exports are under `/api/v1`, described in
[docs/openapi.yaml](docs/openapi.yaml). Every event is also written to a file per day in a `journal` folder
beside the database. Webhooks send the same events to a program of yours, signed and retried;
they are set up on Setup → Webhooks and explained in [docs/webhooks.md](docs/webhooks.md).

Logins are off until you want them: every page is open, as on a practice field nobody else joins.
- They turn on with an admin password (Setup → Settings) or with the first user (Setup → Users and API Tokens).
- Users have a role: admin, scorer (scoring panels and match review edits), referee (the referee panel) or
  read-only.
- Programs use the API with an API token, made on the same page.
- Displays, rankings, reports and match logs stay open, so a TV or a team's phone never needs a login.

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

Driver stations look for the FMS at `10.0.100.5`, an address fixed in the FRC Driver Station software, so on the
field the network adapter that goes to the field network needs that static address. Started on a computer that does
not have it, the FMS stops and says so. On Windows the address only exists while that adapter's cable is plugged in
and the switch at its other end is on. To run on a computer that is not on a field network, start with `-dev`.

A second copy can run beside the first for testing, for example
`./galaxia-fms -port 8090 -server-ip 127.0.0.2 -db test.db`. Give it its own device ports too (Setup → Settings →
Field Devices), since both copies would otherwise want MQTT ports 1883 and 8083.

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
