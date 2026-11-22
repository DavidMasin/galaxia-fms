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

## 2026-11-13 19:35 - Milestones 5 and 6 finished: acceptance checks, reports, SDK CI job

**The third acceptance check of milestone 5, in a browser.** I made a virtual E-stop box, approved it and bound it
to station R1, then started a match. Switching its heartbeat off during autonomous:
- E-stopped R1;
- made Match Play list "no heartbeat for 1.0 seconds" within 2.5 s;
- turned the lighting to its E-stop scene.

With the heartbeat back on, "Clear device faults" cleared it. All three of the plan's checks for milestone 5 have
now passed in a browser.

**Protocol details settled in `docs/devices.md`:**
- `seq` is one counter per device, across all its input channels.
- `ts` is in field-clock milliseconds and is informational.
- `up` is in seconds.
- Several future `at` times queue in order, and a new command replaces those due at or after it.

This is what the FMS and the SDKs already do. The one difference between the SDKs, 32 queued commands in Python
against 8 elsewhere, is documented.

**CI** gains a job for the SDKs:
- the Python, MicroPython and Arduino test suites;
- a strict ESP32 build of every example;
- the action versions checked against GitHub: setup-python v7, cache v6, ArduinoJson tag v7.4.3.

It cannot run until the account's billing lock is cleared. Locally, all three suites pass again: 85, 15 and 151
checks.

**Reports:** [docs/milestones/05-devices.md](docs/milestones/05-devices.md) and
[docs/milestones/06-sdks.md](docs/milestones/06-sdks.md). The README now describes the device features.

**Next:** milestone 4's remaining pieces (users and roles, API tokens, webhooks), then the countdown on the field
(milestone 9).

## 2026-11-21 21:29 - Users, roles, API tokens and webhooks (milestone 4)

Work resumed after a week. David stopped the session mid-task on 2026-11-13; that also stopped the helper agent
that was building webhooks, and a stopped agent cannot be resumed. I finished its work myself, starting from the
files it left. No new agent was started.

**Logins, roles and API tokens** (mine)
- Logins stay **off** until the settings have an admin password or a user exists. Until then every page is open,
  as before.
- **Setup → Users and API Tokens** (new page) adds users with a role, changes roles and passwords, and makes and
  revokes API tokens.
- Four roles:
  - admin: everything;
  - scorer: scoring panels and match review edits;
  - referee: the referee panel;
  - read-only: every API read.
- Displays, rankings, reports, match logs and the match review list stay open, so a TV or a team's phone that
  scans a QR code needs no login.
- **Passwords** are stored as salted PBKDF2-HMAC-SHA256 hashes, 600,000 rounds. **API tokens** are stored only as
  a SHA-256 hash, and the token is shown once.
- **The API** now needs a login or a token while logins are on, and answers 401 or 403 in JSON instead of
  redirecting to the login page. The clock, health, the description and a new "who am I" route
  (`/api/v1/auth`) stay open.
- **Lockout guards.** The FMS refuses to make a first user that is not an admin, to demote or delete the last
  admin, or to clear the admin password while no user is an admin.
- The login cookie is now `HttpOnly` and `SameSite=Lax`, the login page only redirects to this server, and there
  is a Log out link. The navigation bar shows who is logged in.
- One deviation from the plan: committing a match stays with the admin on Match Play. The plan listed "commit"
  under scorer; splitting Match Play by role is a bigger change than this milestone needed.

**Webhooks**
- Kept from the agent after reading it: package `webhook` (the dispatcher with its outbox in the database), its
  storage, the arena wiring, the API handlers and 15 tests.
- Written by me: the Webhooks page and the delivery log page, their script and styles, handler tests, the
  description in `docs/openapi.yaml`, and `docs/webhooks.md`.
- Each delivery is a signed POST: `X-Galaxia-Signature` is an HMAC-SHA256 of the timestamp, a full stop and the
  body. Failures are retried with growing waits for 24 hours, about 33 attempts, then kept in the log with a
  Resend button.
- Two fixes in what the agent left:
  - A test of the backoff overflowed its own arithmetic and failed. The code was right; the test now caps the
    doubling as the code does.
  - The test's "computed independently in Python" signature really matches: I recomputed it in Python and in
    Node.
- One mistake of my own, caught by reading the code against my text: I had written that a disabled webhook still
  queues events. It does not; only deliveries already waiting are held. Page, script and guide corrected.

**The API description is now enforced two ways**
- Every `/api/v1` route the server registers, wherever it is registered, must be in `docs/openapi.yaml`, and the
  reverse. The router now records its routes for this. This found four device routes missing from the
  description; they are described now.
- A new test sends about 60 real requests and validates each answer against the schema given for its status.
  I checked that it can fail: with a required field added to one schema and a type changed in another, it
  reported both. `/api/v1/openapi.json` serves the description as JSON.

**A flaky test fixed.** `TestAllianceStationDisplayWebsocket` assumed the clock message is among the first three
after a match starts. It failed about one run in six, and on every cold start during this work. It now waits for
the message it needs. Six cold starts in a row pass.

**Checks**
- Formatting and vet are clean. `go test -count=1 ./...` passes in all 19 packages; the signed-delivery test
  passed 8 runs in a row.
- In a browser, on the test copy (port 8090, scratch database):
  - Making the first admin turned logins on and logged me in as that user.
  - A scorer was sent to the login page, came back to the scoring panel, and got "Not allowed" on Settings.
  - A read-only token read the API (200), was refused changes (403), and without it the API said 401.
  - A token name containing HTML was shown as text.
  - Two webhooks, one to a Python receiver and one to a Node receiver, both running the code printed in
    `docs/webhooks.md`. Both verified the signatures of a test event and of a match (started, three phase
    changes, aborted), and both refused a forged and an unsigned delivery with 401.
  - With the Python receiver stopped, the log showed "Retrying, no answer" with a countdown. After restarting it,
    Resend delivered on the third attempt, and the details listed all three.
- The scratch database had been reset during the week, so the test copy briefly took broker ports 1883 and 8083
  again. I moved it to 18840/18841 at once. David's FMS was not running at the time.

**Not done in milestone 4 at that point:** the on-disk event journal. It is in the next entry.

## 2026-11-21 21:42 - The event journal; milestone 4 finished

Commits `8ed3390` (the webhook dispatcher) and `52bd768` (logins, pages, checked API description) are pushed.

**The journal** (the last piece of milestone 4)
- Every event is appended to a file as one JSON object per line: one file per day, in a `journal` folder beside
  the database, named by the FMS computer's own date.
- It starts when the arena starts running, first writing what the bus still remembers, so the whole run is there.
  Events reach the file within a second. 30 days are kept.
- If it ever falls behind, it reads the missed events back from the bus's memory. Anything the bus has also
  forgotten is marked with a `journal.gap` line instead of silently missing.
- A new event, `system.started`, is published once per run. Sequence numbers begin again with each run, and this
  is how a reader of the journal, the event stream or a webhook can tell.
- `GET /api/v1/journal` lists the days and `GET /api/v1/journal/{day}` downloads one.
- The repo's `.gitignore` now ignores `/journal`, since David's own copy will write there.

**The API description, tightened again**
- Every operation that needs a login or token now lists its 401 answer, and the admin ones their 403. A small
  script added them (63 lines); running it twice changes nothing.
- The conformance test now sends about 80 requests, including each read route without a token.
- My first version of that check opened the live event stream with a valid token and hung, because a stream never
  ends. I stopped the run and left the two streams out of that one check.

**Checks**
- Journal tests, 3 runs in a row:
  - a file per day;
  - catching up after starting late;
  - the gap line;
  - pruning (only journal files are deleted);
  - a folder that cannot be created;
  - refusing anything that is not a date as a file name.
- A journal test first failed for a good reason: only the callers prevented writing an event twice. The write
  itself now refuses a repeat.
- `go test ./...` passes in all 19 packages; formatting and vet are clean.
- On the test copy: after a restart, today's file held the run's three events, ending with `system.started`, and
  downloaded through the API. The folder was beside the scratch database, not in the repo.

**Report:** [docs/milestones/04-events-api.md](docs/milestones/04-events-api.md), with what the API does not do
yet stated plainly (it reads, and manages games and webhooks; it does not run a match).

**Next:** milestone 9's countdown on the field, or milestone 7's practice modes.

## 2026-11-21 21:45 - Correction: CI has been running since 13 November

**What I got wrong.** Several entries and reports above say GitHub Actions cannot run because of a billing lock on
the account. That was true until 2026-11-13 at 17:30 Israel time, when David cleared the lock. I never checked
again, so the entry of 2026-11-13 19:35 and the reports for milestones 4 and 6 repeated it after it had stopped
being true. I found out today by listing the runs of this repository rather than assuming.

**What is actually true**
- The first successful run was for commit `a764ddb` on 2026-11-13. Every push since has passed.
- That includes the SDK job from its first run (commit `2674d16`): the Python, MicroPython and Arduino test
  suites and a strict ESP32 build of every example, on Linux. So milestone 6's check, "every example builds in
  CI", was met on 13 November.
- Today's commits `8ed3390` and `52bd768` passed: formatting, vet, build, every test and four cross-compiles.
  For `99b4b47`, build and tests have passed and the SDK job was still running when I wrote this.
- One trap: `gh run list` in this folder shows the upstream Cheesy Arena repository's runs unless it is told
  `--repo DavidMasin/galaxia-fms`.

**Corrected:** the reports for milestones 4 and 6. Milestones 1 and 2, which were right when written, have a dated
note. This log is left as it was written, with this entry beside it.

**New: a race detector job in CI.** The detector needs a C compiler, which this laptop lacks, so the threading of
the device framework, the lighting engine, the webhook dispatcher and the journal has only been reasoned about.
The Linux runner has one. The job runs `go test -race ./...` on its own, so the plain test job stays as fast as
it was. Its first result goes in the next entry.

## 2026-11-21 21:50 - The race detector's first run, and a brake on password guessing

**CI results.** The runs for `99b4b47` and `c897c54` finished: formatting, vet, build, every test, four
cross-compiles and the SDK job all passed on Linux. The new race detector job failed, which is what it was added
to find out.

**What the race detector found: 47 reports, 37 distinct pairs of places, in four packages.**
- **Clean:** everything built in milestones 4, 5 and 6 that runs on several goroutines: the device framework, the
  lighting engine, the webhook dispatcher, the event bus and its journal, and storage.
- **Not clean:** `field`, `web`, `websocket` and `network`, all inherited from Cheesy Arena. Two kinds:
  1. The arena's state has no single owner. Its loop and the web handlers, the driver station listeners and the
     field-action worker all read and write the match state, the stations and the display modes with no lock.
     Only the live score has one.
  2. Some upstream tests share variables with their own mock servers.
- Why it matters: these have not been seen to misbehave, which is why upstream runs this way. But they are
  undefined behaviour in Go, and the fields involved include the ones that enable and stop robots.
- The plan had an "arena command queue" in milestone 3 for exactly this. It was never built, and no report said
  so. That is a gap in my milestone 3 report.
- Written up in `docs/architecture.md` under "Known data races".

**CI is now honest about it rather than red.** The race job has two steps:
- required: every package except those four, so new packages are covered without being listed;
- reported but not required: the four inherited packages, until the arena has one owner.

**A brake on password guessing.** The login page had none, and the settings' admin password is compared
instantly. After ten failed logins from one address within a minute, that address now gets 429 until the minute
passes. A header claiming another address is not believed. Tested, including the wait ending and a successful
login wiping the slate.

**One race of my own, fixed before CI saw it:** a journal test moved a shared clock variable while the journal's
goroutine read it. The test's clock now has a lock.

**Next, my recommendation:** give the arena one owner (the command queue) before building more on top of it. It
touches every web handler that changes a match, so it is a milestone-sized change.

## 2026-11-21 21:57 - CI green with the reworked race job; the arena fix sized

**CI for `dca2f6e`: every job passed.** I read the race job's log rather than trust its green tick, because a
step that is allowed to fail shows as passed either way:
- the required step passed for all 15 packages with tests outside the four inherited ones;
- the reporting step still found the inherited races: 41 reports in 29 pairs this run, against 47 in 37 on the
  first. The count varies from run to run, as races do.

**How big the arena fix is.** Web handlers call 40 different arena methods from 85 places, and read arena fields
directly in a few hundred more (settings, the game, the current match). The contained way to give the arena one
owner is one arena lock, taken by each pass of the loop and by each of those entry points, rather than a queue.
The tests that poke arena fields while a test server runs would need the same lock.

**Milestone 3's report** now says the command queue was not built. It should have said so from the start.

**Stopped here.** The test copy and both receivers are stopped; nothing of mine is left listening.

## 2026-11-21 23:06 - The arena has one owner: every package is free of data races

David chose to fix the arena's data races before the countdown. Done with one arena lock, as sized in the last
entry. The race detector, run four times over, reports nothing in any package. Written up in
`docs/architecture.md` under "The arena's lock".

**The rule.** Whoever touches the arena's state holds `Arena.Lock`: each pass of the loop, each web handler, the
driver station listeners, the timers. Nothing waits on a browser, the network or a long computation while holding
it; code that must wait lets go with `Arena.Unlocked` and checks again afterwards.

**What was built, layer by layer.**
- `websocket`: a notifier's message is encoded when it is sent, by the sender, so listeners get bytes rather than
  pointers into the arena. A websocket can hold back its writes (`Hold`, `Release`) so that nothing is written to
  a slow browser under the lock. Closing a listener and sending to it can no longer collide.
- `plc`, `network`: the PLC, the access point and the switches each have a lock for their status; their network
  traffic happens outside it.
- `field`: the lock itself (`field/arena_lock.go`); the driver station listeners, the periodic tasks, delayed
  work, the game file watcher, the field-action worker and the audience display automation take it; the bus and
  the webhook dispatcher read copies that `Unlock` keeps current.
- `web`: the router runs every handler with the lock held. The request body is read first and the answer is sent
  after the lock is released. A websocket handler lets go when it takes the connection over, then locks per
  message. Streams (events, journal, database download) let go once they have what they need. Five routes are
  unlocked on purpose: the four device transport endpoints and the database restore, whose upload can be large.
- Waiting moved off the lock: The Blue Alliance, Nexus, password hashing and checking, schedule generation,
  writing a PDF out, database backups, asking Windows for its addresses.

**Faults found on the way.**
- Mine, in the uncommitted first half of this work: a debounced device input is committed by the device
  manager's timers, which the arena loop runs. The manager then called back into the arena, which would have
  tried to take the lock the loop already held and frozen the field. Device score changes are now queued by the
  manager and applied by the loop on its next pass (10 ms at most). Tested in both packages.
- Inherited: the SCC switch client wrote SSH output from two goroutines into one buffer; two driver station
  goroutines could overwrite each other's station; The Blue Alliance client's event name cache was written from
  several requests at once; the field testing page built a PLC message without the PLC's lock.
- Committing a match and loading the next one let go of the lock in the middle (backup, Nexus). A second commit
  or load in that gap is now refused with a message instead of running inside the first. `LoadMatch` asks Nexus
  before it changes anything, so the field is never half loaded while Nexus answers.

**Found and not fixed.** After restoring a database, the device manager and the lighting engine keep using the
old one: the restored event's devices, bindings and lighting are not loaded, and saving them fails, until the FMS
is restarted. This is from milestones 5 and 6, not from this work. It is in the limits in `architecture.md`.

**New in the log.** `Warning: the arena was held for N ms by GET /page` names whatever keeps the lock longer than
50 ms. The older "Arena loop iteration took a long time" now measures the loop's own work only.

**Tests.** New: the router wrapper (lock held during a handler, released on a panic, on streaming and on a
websocket upgrade; a body over 10 MB refused), the match flow guard, deferred device scoring, a debounced input
scored from the loop. The shared web test helpers take the lock; five tests that read the arena while their
server ran now take their turn.

**The race detector runs on this laptop now.** It needs a C toolchain, and Zig serves: `pip install ziglang` in a
virtual environment, then
`CGO_ENABLED=1 CC="<venv>/Scripts/python.exe -m ziglang cc" go test -race -ldflags '-extldflags "-lapi-ms-win-core-synch-l1-2-0 -Wl,--no-dynamicbase"' ./...`.
The first flag supplies `WaitOnAddress`; the second turns off address randomisation, which the detector's runtime
needs on Windows.

**Checked in a browser** on the test copy (port 8090, its own database, broker ports 18840 and 18841; nothing of
David's was running or touched):
- a full match with the ball-count game: bypassed all stations, started, scored from the red scoring panel in
  autonomous and teleop, undid one change, committed after the end. Final Score came up and the next match loaded;
- 78 page and API requests during a running match: all answered, most in under 10 ms, the clock kept running, and
  the log had no warning of either kind;
- a virtual counter and a debounced virtual plate bound to the score: both scored in a running match;
- the audience display followed the match (TELEOP 1:57, blue 6).

**CI.** The race job is one step again: `go test -race ./...`, required for every package.

**Commands run.** `go build`, `go vet` (three analyzers off as before), `go run cmd/gofmt -l .` (clean),
`go test ./...` (19 packages pass), `galaxia-fms game validate games`, `galaxia-fms game test games`, and the race
detector as above: once over everything, four times over the six packages changed, twice more over everything on
the final code.

## 2026-11-21 23:16 - CI for the arena lock: every job passed, the race job on all packages

Commit `58265d7`, run 44950045156. All seven jobs passed. I read the race job's log as well as its result: it ran
`go test -race ./...` on Linux, all 19 packages with tests passed, and the log has no data race report. This is
the first run in which the detector is required for `field`, `web`, `websocket` and `network`.

Proposed as a separate task, not started: making the device manager and the lighting engine follow a restored
database (see the previous entry, "Found and not fixed").

Nothing of mine is left running: the test copy is stopped and its ports are free.

**Next:** the countdown on the field (milestone 9), which David put after this.

## 2026-11-22 10:42 - The device manager and the lighting engine follow a restored database

Branch `claude/ecstatic-lalande-5168fa`, not pushed and not merged. This is the task proposed at the end of the
previous entry.

**What was wrong.** Restoring a backup (Setup → Settings) opens the uploaded file as a new `Arena.Database`. The
device manager and the lighting engine kept the database they were built with. After a restore the Devices,
Bindings and Lighting pages still showed the old event, and saving anything answered "database not open", until
the FMS was restarted.

**How it works now.** `LoadSettings`, which every restore ends with, settles the game and then gives both of
them the database: `Manager.Configure` with the database, the field profile and the game, and
`Engine.SetDatabase(db, game)`. With the database they already have, nothing changes. With another one they read
it before returning, so the pages show the restored event as soon as the restore has answered. No restart.

**Decisions.** They are written up in `docs/architecture.md` ("A restored database" under the arena's lock, "A
replaced database" under Devices) and for operators in `docs/devices.md` ("Restoring a backup").
- *Told, not asking.* The webhook dispatcher asks for the database each time it uses it. I did not copy that:
  the pages must show the restored event at once, the arena loop must not be what notices and reads a database,
  and the transports run without the arena's lock, where the shared copy of the pointer is not yet current.
- *No generation counter.* Everything in the manager and the engine that touches the database holds their
  `storeMu`, and so does the swap. Loading and saving a binding set did not hold it before; they do now.
- *Devices.* One the backup has is what the backup says, and keeps its connections, inputs, outputs and
  heartbeat. A connected one the backup does not have is pending again, with the token it connected with. One
  that is neither is forgotten. A device whose state or token changed is reported to the transports, which make
  it reconnect.
- *Counters.* The running total is that of whoever heard the device last, which is the manager for a connected
  device. Taking the backup's older total would have scored the difference a second time.
- *Stops and faults.* Nothing is reset. A stop level is carried over or read again from the channel's last
  value. A fault stays on its binding, or moves to the binding that reads the same channel if the backup has the
  stop under another id. A fault goes only when the backup binds that channel to no stop, and
  `device.fault_cleared` is published. A device that loses its approval in the restore faults every stop bound
  to it, as blocking does.
- *One step.* The manager gets the database, the profile and the game together and reads the binding set once.
  For this `LoadSettings` now configures the devices after the game is settled, not before, and `refreshGame`
  passes the database and the profile along with a new game. The arena no longer calls `SetGame` or `SetProfile`.
- *Errors.* A database the manager cannot read changes nothing in it, and the settings page says so.

**Two things I got wrong on the way and changed.**
- My first version passed only the profile with the database, and left the game to change in a second step. A
  restore that also selects another game would then have put the old game's bindings from the new database in
  force for a moment, and dropped latched faults. I found it while writing the docs, not from a test.
- For a database that cannot be read, I first kept the old database but took the new profile. A test I wrote for
  it showed that this drops a fault as well. Now nothing changes.

**Tests.** New, in four packages:
- `device`: reading from and writing to the new database with the old one closed; six kinds of connected and
  stored devices in one restore, with what the transports were told; counters, connected and offline; stops and
  faults in six cases, one of them a restore that changes the game; a database that cannot be read; and a swap
  back and forth while a device reports, the loop ticks and a page saves.
- `lighting`: the restored lighting in force without a tick, the old fixture cleared, saving to the new
  database; a database with no lighting; a swap while a page reads and reloads.
- `web`: `TestSetupSettingsRestoreDbDevicesBindingsAndLighting` uploads a backup through the real handler, then
  reads and saves through the three pages' own requests.
- `field`: `TestLoadSettingsWithAnotherDatabase` swaps in the database of an event with another game and another
  profile, and checks the devices, the bindings and the lighting the arena ends up with.

With the arena wired as it was before (the profile and the game, never the database), the `web` and `field`
tests both fail with the old symptoms: the old devices, and "database not open". I checked that on the final
code and put the wiring back; the diff was identical afterwards.

One of my new tests was wrong at first: it expected a note written by a background goroutine to be in the final
database, which depends on timing. It failed once in a full run. It now writes the note after the goroutines
have stopped.

**Commands run**, all on the final code:
- `go build`, `go vet -composites=false -hostport=false -structtag=false ./...`, `go run cmd/gofmt -l .` (no
  output), `go test ./...`: all 19 packages with tests pass.
- `go test -race ./...` with Zig as the C toolchain, as in `architecture.md`: 19 packages pass, no data race
  reported. I added the `field` test after that run, and ran the detector on `field` again: it passes, no race.
- The new tests of `device` and `lighting` 30 times over and the `web` one 10 times, before the last two changes.
- `galaxia-fms game validate games` (3 ok) and `galaxia-fms game test games` (29 pass).

Not done: nothing was pushed, so CI has not run on this. I did not try it in a browser; the web test goes
through the same handler and the same page requests.

**Found and not fixed.**
- `replaceDatabase` assigns the result of `OpenDatabase` straight to `Arena.Database`. If opening the restored
  file failed at that point, after the old file was removed, the arena would be left with no database. The file
  has been opened once already as a check, so this is unlikely.
- A restore is not refused while a match is running. It is now in the limits in `architecture.md`.
- A fault names the device its binding had when it latched. If a binding keeps its id and is moved to another
  device while faulted, the fault still names the old one. This is from milestone 5 and is the same after
  `SaveBindings`.
