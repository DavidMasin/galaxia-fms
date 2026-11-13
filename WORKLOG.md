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

## 2026-11-13 17:17 - Milestone 3 browser check, part 2

**Found:** David is running the FMS himself (galaxia-fms.exe on port 8080, using the repository's event.db). To test
without disturbing it, a second copy has to run on the same laptop.

**Done:** three startup options, so a second copy can run alongside:
- `-port` sets the web port (default 8080).
- `-db` sets the event database file (default `./event.db`).
- `-server-ip` sets the address the driver station listeners bind to (default 10.0.100.5). This brings back the
  `-serverIP` option from David's old fork under the new naming.

Failing to serve the web interface is now fatal instead of leaving a running field that nobody can operate.
The test copy runs with `-port 8090 -server-ip 127.0.0.2` and a scratch database outside the repository.

**Checked in the browser (rebuilt-style game, test match, all stations bypassed):**
- Scoring panel, red near side: 3 fuel scored in autonomous. When teleop began, the game's rule set
  "Autonomous winner = Red". It appears in the panel's change list, and the Red button is shown selected.
  The earlier suspicion that this display was broken was wrong: the first check read the wrong attribute.
- Referee panel: a major foul called against red appears in the foul list, with the rule choices filtered to
  major-foul rules. Blue's score rose by 15, and Match Play's breakdown shows Fouls 15 for blue.
- Commit & Post on the test match: the audience final-score screen showed the game's breakdown rows (Auto fuel,
  Auto tower, Teleop fuel, Endgame tower, Fouls, Total) with red 3, blue 15 and WINNER on blue. Measured at
  1920x1080, the centre logo sits between the two scores and clears the rows.
- Practice match: created six teams and a practice schedule, played P1 with 4 fuel and a level 3 climb, aborted
  and committed it. It was stored with 5 changes, a copy of its game, and red winning 34-0.
- Match Review → Edit on P1: the audit log listed all 5 changes, with team numbers. Undoing the climb dropped red
  to 4. Setting blue's Shift 1 fuel to 50 and saving made blue the winner, 50-4. Both corrections are in the log
  with source "admin: Match review" and phase "post".
- Switched the active game to ball-count in Settings. Match Play picked up the new game straight away, the
  scoring panels became "red" and "blue", and P1's review page still showed its own game.

## 2026-11-13 17:25 - Milestone 3 finished

**Fixed while checking**
- The four logo images on the displays and in printed reports were the 2026 game's artwork. They are replaced by
  a neutral Galaxia logo with the same sizes, drawn by `tools/brand/make_logos.py` (Pillow, Segoe UI), so they
  can be regenerated.
- The audit log called an undo record "Undo" in its state column, next to a "Redo" button. It now says
  "Reverses #N".

**Checked:** `go test ./...` passes in every package; `game test` passes all 29 examples.

**Written:** [docs/milestones/03-generated-panels-and-displays.md](docs/milestones/03-generated-panels-and-displays.md),
the README (status, startup options).

**Next:** merge `wip/m3b-core` into `main` and push, then start the device framework.

## 2026-11-13 17:30 - Milestone 3 merged; device framework started

**Done:** `wip/m3b-core` fast-forwarded into `main` and pushed (`959314d`). The local branch was deleted after
the merge.

**Written:** [docs/devices.md](docs/devices.md), the device reference and the specification for milestones 5 and
6. It follows the protocol approved in the plan, with two additions:
- LED strip commands may carry segments, each with its own pattern, so one strip can show several things.
- The PLC appears as a read-only device. Its station stops, field stop and "FTA ready" switch stay wired into the
  arena code exactly as in Cheesy Arena, because they are the primary safety path and are already proven. Only
  new devices go through bindings.

**Decided**
- Field actions from devices (start, abort, stops, commit) are queued and run on the arena's own loop, not on the
  network threads, so a button press can never race the match clock.
- Device tokens are stored as SHA-256 hashes. They are long random strings, so a slow password hash adds nothing.
- An unknown device that connects is held as "pending" and may only announce itself. Approving it pins the token
  it presented. A device can also be created first on the Devices page, with a generated token.

**Started, in parallel**
- The device framework core: registry, embedded MQTT broker, virtual devices, bindings, safety watchdog, and the
  arena integration.
- The firmware SDKs (Arduino/PlatformIO for ESP32, MicroPython for Pico W, Python for Raspberry Pi), nine example
  sketches with wiring, and the "add your first sensor" tutorial. These go in `sdk/`, `examples/` and
  `docs/tutorial-first-sensor.md`.

**Next, after the core lands:** the Lighting engine and page, the Devices and Bindings pages, the virtual device
page, and the WebSocket, HTTP and serial transports.

## 2026-11-13 18:18 - Countdown audio made (milestone 9 work, done early)

**Why now:** the device work is running in the background and the audio does not touch any of the same files.

**Done**
- Installed Piper 1.8.0 (offline text-to-speech) in an isolated Python environment outside the repository, and
  downloaded the `en_US-ljspeech-high` voice. Its model card says the dataset is public domain and the model was
  trained from scratch, so audio made with it can ship in the repository. Piper itself (GPL) is not shipped.
- Wrote `tools/audio/make_countdown_audio.py`. It speaks "Drivers, behind the lines", "Three!", "Two!", "One!"
  and "Go!", puts the voice in a light room reverb, and synthesises a hit under each number (heavier towards
  "Go!"), a shockwave on "Go!", and a 3-second riser that ends on "Go!".
- Output: `static/audio/countdown/` (intro, 3, 2, 1, go, riser), plus `countdown.json` describing when each one
  is played relative to its beat, and a README with the sources.

**Checked:** every beat file starts within 0.2 ms of its first sample, peaks at -1 dBFS, and contains no invalid
samples. The riser is exactly 3.00 s. Waveform plots look as intended.

**Not checked:** how it sounds. I cannot listen; David should, once the countdown plays in milestone 9. All the
files can be replaced with recordings.

## 2026-11-13 18:26 - Countdown animation built (milestone 9 work, done early)

**Done:** the match-start countdown as a standalone module, so the displays can use it once the server side
exists. It touches none of the files the device work was changing.
- `static/js/countdown.js` and `static/css/countdown.css`. The sequence is "DRIVERS BEHIND THE LINES", then 3, 2
  and 1 slamming in with trailing copies for motion blur and a glow. A ring empties clockwise through each beat.
  The two alliance colours close in from the sides on every beat and meet on "GO!", with a camera pulse on each
  beat. On "GO!" there is a white flash, three shockwaves and a 260-particle burst, then "GO!" shrinks into the
  match timer.
- Every frame is computed from the shared clock ("milliseconds until GO"), never from a frame count. Displays
  therefore agree on the beat, and a dropped frame catches up instead of drifting.
- Sounds are scheduled on the Web Audio clock, mapped with the audio hardware's own timestamp. A per-display audio
  latency offset is supported, and a sound more than 0.25 s late is skipped rather than played out of time.
- There is a transparent version for OBS, and a light renderer (no particles, no blur copies). The light
  renderer switches on by itself if the first 30 frames run slow, or can be forced.
- An abort fades everything out in 0.2 s and stops the sound.
- `static/countdown_preview.html` plays it on its own with sound, colour, transparency, light-renderer and
  latency controls, and reports frame times.

**Checked**
- Frames captured in headless Chrome at 1920x1080 at eleven moments, and in the transparent version. Adjusted
  after looking at them: a stronger backdrop, thinner shockwaves, the ring now empties clockwise, and "GO!"
  reaches the timer earlier.
- Live run in headless Chrome at 1920x1080: average frame 16.9 ms (60 fps), 95th percentile 16.9 ms, 3 frames
  over 20 ms out of about 420 (at start-up). Script time per frame: 95th percentile 1.4 ms.

**Not checked:** the frame rate on the real field PC and TV with a GPU, and the sound. The in-app browser pane
cannot measure it while the app window is in the background. Open
`http://<fms>:8080/static/countdown_preview.html` on the display hardware to see the numbers.

## 2026-11-13 18:30 - Device framework core landed; pages and transports started

**Landed:** the core, in four commits (`ecfd7a8` … `decdb44`), pushed with the countdown.
- The new `device` package: manifests, the device registry (pending, approved, blocked), tokens, and the
  embedded MQTT broker (mochi-mqtt v2.7.9) with per-device logins and topic permissions.
- Virtual devices, the PLC shown as a read-only device, input and output bindings, and the safety watchdog.
- Device stops merged with the PLC stops, and device start conditions.
- New tables for devices, field profiles and binding sets; new settings for the broker ports.

**Checked by me:** formatting is clean, `go vet` is clean, and `go test ./...` passes in all 14 packages
(the `device` package is about 7,500 lines with its tests). No game- or device-specific names appear in the device
package code.

**Worth knowing from the core's report**
- A counter's first total only sets a starting point. Devices send their total once after announcing themselves,
  so nothing is double-counted after a restart of either side.
- Approving, blocking or re-tokening a connected device deliberately drops its connection, so it reconnects with
  its new rights. That reconnection does not count as a safety fault.
- The broker library has known session-takeover bugs. A test reconnects the same device 40 times in a row
  without hanging.
- The FMS now starts its MQTT broker on ports 1883 and 8083. A second test copy on the same laptop must use other
  ports, set in its own settings.

**Added by me:** exact LED pattern definitions in [docs/devices.md](docs/devices.md) ("Pattern definitions"), and
`static/js/led_patterns.js`, which draws them in the browser. 14 checks of it pass. The SDK work was told about
the definitions and the other details the core settled, so physical strips, previews and virtual devices match.

**Started, in parallel**, each on separate files:
- the Lighting engine and page;
- the Devices, Bindings and virtual device pages, plus a `galaxia-fms device sim` command and the commit and
  next-match device actions;
- the WebSocket, HTTP and USB serial transports.

Each page got its own route file and its menu entry beforehand, so the three cannot overwrite each other.

## 2026-11-13 18:55 - Firmware SDKs landed (milestone 6); API started (milestone 4)

**Landed, commit `85549ef`: SDKs, examples and tutorial**
- `sdk/arduino`: ESP32 over Wi-Fi or Ethernet, built on arduino-mqtt 2.5.3, ArduinoJson 7.4.3 and Adafruit
  NeoPixel 1.15.5 (versions checked against the PlatformIO registry).
- `sdk/micropython`: Pico W and Pico 2 W, on mqtt_as 0.8.5 and MicroPython 1.29.
- `sdk/python`: the `galaxia-device` package, on paho-mqtt 2.2.
- Nine examples with wiring diagrams, parts lists and the binding to make: beam-break counter, arcade button,
  limit switch, stack light, LED strip, E-stop box, match timer, load cell, RFID reader.
- `docs/tutorial-first-sensor.md`.

What the SDK work reported checking:
- Python SDK: 85 tests; MicroPython: 15 tests under CPython with hardware stand-ins.
- Arduino library: 151 native checks.
- LED drawing matches `static/js/led_patterns.js` on all 87 shared reference cases.
- All 12 sketches build for esp32dev with strict warnings.

I checked that the folders contain only sources (no build output, 94 files, under 700 KB).

The new SDK and example code is MIT licensed, so teams can reuse it in their own robots and fixtures. The FMS
itself stays under Team 254's licence.

**Not verified:** nothing has run on a real ESP32 or Pico yet, or against the FMS's own broker. That is the first
thing to try with hardware.

**Protocol gaps the SDK work had to decide on**, to settle in the spec:
- whether the `seq` counter is per device or per channel;
- which clock `ts` is in;
- the unit of `up` (taken as seconds);
- what happens when several commands carry future `at` times.

**Started milestone 4, the API (on my own files, while the device pages are being built)**
- Read API under `/api/v1`: time, health, the active game, the current match (with clock and live score),
  matches with results, one match with its full audit log, rankings with the game's columns, teams, alliances.
- Team Wi-Fi keys and FTA notes are deliberately left out of the public teams list.
- The event stream: `/api/v1/events` to poll, `/api/v1/events/stream` for Server-Sent Events (resumes after a
  reconnect), and `/api/v1/events/ws`. A `stream.gap` notice says when events were lost, so a client knows to
  reload rather than silently miss something.
- `docs/openapi.yaml` (OpenAPI 3.1) describes every v1 route. A test fails if a route is undescribed or the
  description lists one that does not exist.
- 9 new tests pass. Not committed yet: they hook into the same route registration as the device pages, which are
  still being written.

## 2026-11-13 19:00 - Exports, win probability and The Blue Alliance check (milestones 4 and 10, early)

Done on files no other job is touching, while the device pages are being built.

**Exports** (`web/api_exports.go`, not committed yet; same reason as the rest of the API):
`/api/v1/exports/{matches,scores,rankings,audit}.{csv,json}`, optionally filtered by `?type=`.
- Game-specific columns come from the game each match was played with. In `scores.csv` they are named
  `breakdown.<row>`, `points.<element>` and `rp.<id>`.
- The audit export lists every score change: time, phase, the phase it was credited to, source, and the
  confidence a device reported.
- Its test found a wrong expectation in the test itself, not in the code: blue's 20 tower points beat red's
  12 fuel.

**Win probability** (`predict`, commit `5a33db9`; not shown on any screen yet)
- Before a match: Statbotics' own formula for two alliances. For three or four alliances, the chance of
  having the highest of independent normal scores, computed numerically. The two-alliance case of that
  calculation is checked against the exact formula.
- During a match: score so far plus the share of each alliance's rating still to come, with a spread that
  shrinks as time runs out.
- Ratings come from a Statbotics-compatible URL and are cached in a file, so predictions work offline after one
  fetch. Both shapes Statbotics has used for its rating field are accepted; it changed in June 2026.
- 6 tests, including fetching from a fake server, then working offline from the cache.

**The Blue Alliance check** (commit `1fba4a5`)
- The Blue Alliance rejects a whole publish for a single breakdown key it does not know. Before publishing, each
  game's mapping is now checked against the official keys for its year, kept as data in `partner/tbakeys/`
  (2026's 38 keys come from upstream).
- Unknown keys, a missing year, more than two alliances, or a mapping that fails to evaluate block the publish
  with a message saying exactly why. Keys left unmapped are a warning.
- Result for the examples:
  - `rebuilt-style` passes with one warning (some official keys unmapped);
  - `ball-count` publishes scores without a breakdown;
  - `orbital-rush` (three alliances) cannot be published.

## 2026-11-13 19:15 - USB, WebSocket and HTTP devices; connection reports; team QR codes

**Transports** (milestone 5, commit `5d13d8c`). Devices can now also connect three other ways besides MQTT:
- **WebSocket** at `/api/v1/devices/ws`.
- **HTTP** with long polling under `/api/v1/devices/{id}/...`.
- **USB serial**, with ports chosen on the new page `/setup/devices/serial`.

How they behave:
- All three authenticate, take over and go offline the same way MQTT does.
- Approving, blocking or deleting a device takes effect on an open connection.
- A serial port that is unplugged is retried every second, so plugging the board back in reconnects it.
- The serial library is `go.bug.st/serial` v1.8.0 (BSD-3).

Tests and documentation:
- Serial is tested with in-memory port pairs; no real board was tried yet.
- The rules each transport added are now in `docs/devices.md`; the threading is in `docs/architecture.md`.

**Connection reports** (milestone 8, early; committed with the API in `d5557de`). Package `diag` turns a team's
match log into a report:
- whether and when the robot lost its connection;
- battery low points (brownout risk below 7 V) and when they happened;
- estimated packet loss, from 50 packets a second;
- round-trip times and radio signal-to-noise;
- a verdict of good, watch or poor, with findings in plain sentences.

Where it shows:
- On each match log page, above the charts.
- At `/api/v1/diagnostics/connection/{match}/{station}` (in `docs/openapi.yaml`).
- `/reports/connection/team/{team}` always leads to the team's latest match.
- 3 tests for the analysis; 2 for the routes.

**Team QR codes on the field monitor.** With `?qr=true` (a new display option, also on the Displays page and
in the Displays menu), each team box shows a QR code in its corner. Scanning it opens the team's latest
connection report. The address is per team, so the code stays valid as matches go by. Checked in the browser
with four teams loaded; the team numbers stay readable.

**Offline first:**
- Chart.js 4.6.1, used by the match log page, is now served from `static/js/lib` instead of a CDN, so the page
  works on a field network without internet.
- qrcode-generator 2.0.4 is vendored the same way.
- Both are MIT licensed.

## 2026-11-13 19:16 - Device pages: Devices, Bindings, Virtual devices, and `device sim`

Built by a helper agent; reviewed and committed by me. The five-minute flow from the plan now works end to end
in a browser: a virtual counter appears as pending, Approve, a binding saved in a few clicks, and a +1 in
autonomous makes the score 2 on Match Play.

The pages:
- **Setup → Devices**:
  - devices waiting for approval come first, with a big Approve button;
  - every device's connection, health, live inputs, outputs as they show now, faults and its last refusal;
  - Add device shows the token once, with a snippet for each SDK;
  - outputs can be tested by hand unless a binding or the Lighting page drives them.
- **Setup → Bindings**: cards that read as sentences, or YAML. The FMS checks the set about 300 ms after each
  change, and a set with errors cannot be saved.
- **Virtual devices**: seven templates, clickable inputs, live outputs.
- **`galaxia-fms device sim`**: a device from a terminal, over MQTT.
- **Match Play**: a "Device faults" panel with a clear button. The `commit` and `next_match` field actions now
  work; `next_match` is refused while a played match waits to be committed.

My review:
- Removed a game element name from a comment in `setup_bindings.js`. The rule is that no game is named in
  browser code.
- Linked the USB serial page from the Devices page.
- The agent reported that the SDKs draw pulse and chase differently from the browser. I checked all three SDKs
  against `led_patterns.js` and the spec: they match, and the note came from an older copy.

**Settings → Field Devices** (new): the two MQTT broker ports can now be set on the Settings page instead of only
in the database.
- 0 turns a listener off.
- Both ports are checked before anything changes.
- A form without the fields keeps the ports as they are. Without this, an old form or script would save 0 and
  turn the broker off.
- 1 new test.

Checks:
- `go build ./...`, vet and `go test -count=1 ./...` pass in all 18 packages.
- Each of the three commits was built, vetted and tested on its own, from an export of the index.
- The OpenAPI consistency test was checked by renaming one route in the spec. It failed both ways: the route
  missing from the spec, and the spec entry with no route. Restoring the spec made it pass again.

## 2026-11-13 19:26 - The Lighting page (milestone 5): LEDs configured from the browser

The place to configure LEDs that David asked for is done: **Setup → Lighting**. It was built by a helper agent;
I reviewed it, closed the two gaps it left, and checked it in a browser.

What it does:
- **Fixtures**: any LED strip, light, relay or dimmer output of an approved device. Each has an optional
  alliance, a brightness limit, a reversed direction, and segments you make by dragging along a picture of the
  strip.
- **Scenes**: a grid of field moments (E-stop, device fault, each cue and phase of the game, score posted, match
  over, field reset, volunteers, teams ready, awards, idle) by fixture and segment.
  - Each cell is a look: off, solid, blink, pulse, chase, rainbow or progress.
  - Colours can be fixed, the LEDs' alliance, or the winner.
  - A look can have a condition, an expression on the live match.
  - An empty cell falls through to the next moment down, shown faded.
- **Live** shows what every fixture shows now. **Preview** shows any moment without touching the field. **Test
  on the field** shows a moment on the real hardware for 10 seconds. **Identify** blinks one fixture.
- A new fixture starts with useful scenes:
  - the alliance colour during the match;
  - green on field reset and purple for volunteers;
  - red blinking on E-stop and orange blinking on a fault;
  - white at idle.
- Export and import as YAML, per field profile.

The engine (package `lighting`) runs on the arena's output tick at 20 Hz and sends only what changed. It does
not take channels that bindings drive, and bindings cannot take its channels.

Browser check on a second copy (port 8090, scratch database):
- I made a virtual 30-LED strip and a stack light and approved both.
- I added the strip as a fixture with a red and a blue segment, made by dragging.
- I set a cell to pulse in the winner's colour. A misspelt condition was refused with its error shown in the
  cell editor.
- I played a whole test match. "Now" followed it: teams ready (after bypassing every station), autonomous,
  teleop, the endgame cue for 2 s, teleop, match over, then score posted for 10 s after committing, then idle.
- During autonomous the virtual strip itself received two segments in the game's alliance colours, so the
  engine drives the device, not only the page.
- Test on the field turned the strip red and blinking for 10 seconds.
- At tablet width the page still works.

What I changed in review:
- **Choosing the active field profile** (a gap: nothing in the UI could). Settings → Field Devices now has a
  Field Profile box: type an existing name to switch to it, or a new name to make a new, empty profile. 1 new
  test. In the browser check, I used it to make and switch to a "tent" profile.
- **Deleting a field profile** now deletes its lighting too, instead of leaving it orphaned. Test extended.
- **A clearer error.** A condition using `own` or `opp` on LEDs without an alliance now says to give the fixture
  or segment an alliance first, instead of only "unknown name". Tested.
- Removed a game-style id from a code comment.
- The test copy's device broker had taken ports 1883 and 8083, which would have blocked David's own FMS. I moved
  it to 18840/18841 with the new Settings fields. The broker moved without a restart, and 1883 and 8083 are free.

Known limits, written in `docs/devices.md`:
- Theme colours are not offered yet; they come with milestone 9.
- With `reverse` set, chase and rainbow still run in the device's own direction.
- The countdown moments arrive with the countdown.
