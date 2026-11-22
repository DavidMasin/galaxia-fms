# Milestone 9, first part: the countdown on the field

Finished 2026-11-22. How it works is in [docs/architecture.md](../architecture.md#the-countdown-m9); the game file's
part is in [docs/game-definition.md](../game-definition.md#match) and the devices' part in
[docs/devices.md](../devices.md).

This is the countdown only. The rest of milestone 9 (OBS automation, camera recording, theming) is not started;
see [What is left](#what-is-left).

## What works

- **Pressing Start begins a countdown; the match starts at the end of it.** With the example games that is
  "Drivers, behind the lines", then 3, 2, 1, GO: six seconds from Start to the match. Robots stay disabled until
  GO and are enabled in the same pass of the arena loop that starts the match.
- **Everything counts down to one moment.** The arena fixes the moment of the start when Start is pressed and
  tells everything at once. Each display and device counts down to it on its own clock, set against the server's.
  - Audience display: the full animation, with sound. GO shrinks into the match timer.
  - Alliance station displays: the same animation, without sound.
  - Field devices: `field/state` says `countdown`, with the moment of the start and when each beat falls.
  - Output bindings can pulse on `countdown.start`, `countdown.beat`, `countdown.go` and `countdown.cancel`.
  - Lighting has three new moments, `countdown`, `countdown_beat` and `countdown_go`. A new fixture starts with
    looks for them: off during the countdown, a white flash on each beat, green for a second at GO.
  - The event bus and webhooks get `match.countdown` and `match.countdown_cancelled`.
- **A start can be called off.** Abort, a field stop, or a team's E-stop during the countdown puts the field back
  to before the match. Nothing was played, so there is nothing to commit, and Start works again.
- **An event chooses its countdown** on Setup → Settings → Game: as the game says, the game's quick countdown
  (one beat for the example games, nothing said first, 1.8 seconds), or off. A game can also have none.
- **Each display has its own options**, set on Setup → Displays:
  - `countdown=overlay` (drawn over what is on screen; the audience display starts with this), `full` (the
    screen is dimmed behind it; the alliance station display starts with this), `light` (no particles or
    trailing copies, for slow hardware) or `off`;
  - `sound=false` to keep an audience display silent, and `audioLatencyMs` to shift its sound.
- **A display that connects late joins in on the beat**, and one that was disconnected when a start was called
  off stops when it reconnects.

## How to try it

1. Start the FMS and open Match Play and, in another window, an audience display.
2. Click once anywhere on the audience display. A browser plays no sound on a page nobody has touched, unless it
   was started with `--autoplay-policy=no-user-gesture-required`.
3. Bypass the stations and press Start. Press Abort during the countdown to call it off, then Start again.
4. To see how a particular screen draws it, open `/static/countdown_preview.html` on that screen. It plays the
   countdown alone and reports frame times.

## Verification

All measured on this laptop (Intel Core i7-1185G7, Iris Xe graphics, Windows 11) on 2026-11-22.

**The start.**
- In the tests, with the loop run as the server runs it, the match started 0.25 to 0.55 ms after its moment, and
  a driver station on the same computer heard "enabled" 0.5 to 1.1 ms after it and never before
  (`TestArenaLoopWakesForTheStart`, `TestRobotsAreEnabledAtTheStart`).
- On a running test copy, over 13 starts, the arena loop started the match a median 0.67 ms after its moment.
  The worst was 4.8 ms, while two browser windows were drawing in software on the same computer; the next worst
  was 1.5 ms. The server reports this for every match as `lateMs` in the `match.started` event.
- What the plan asked for, "robots in the simulator enable at `t0`", is checked at the level of the packet: the
  test reads the control packets a driver station would receive. There is no driver station simulator yet.

**The displays**, in headless Chrome 158 at 1920×1080 using the graphics card, showing the real audience display
of a test copy on the same computer:
- With two audience displays and an alliance station display open at once, the second countdown drew 411 frames
  in each window: median 17.7 ms, 99% within 18.8 ms, the longest 19.3 ms, none over 20 ms.
- Headless Chrome paces an empty page at a median 17.9 ms a frame, about 56 a second, so the countdown ran at
  the rate this browser allows. **Whether a real screen holds 60 frames a second was not measured**: that needs
  the display hardware, and the preview page reports it there.
- Two of the five countdowns measured after the changes below had one long frame, of 0.13 and 0.2 s. Both fell
  in the 0.4 seconds of notice, before the countdown has drawn anything, so nothing is seen of them.
- Two windows measured the server's clock to within 0.15 to 0.43 ms of each other, over round trips of 4 to 6 ms.
  The first frame each drew after a beat differed by at most 13 ms, less than one frame, over three runs.
- The audience display heard that the match had started 8 ms after its moment.

**What the measurements changed.** The first runs showed the countdown holding frames, and the cause in each
case was the browser drawing something afresh in the middle of the animation:
- the large glowing numerals were drawn again whenever they changed or changed size; "GO!" held a frame for
  about 0.22 s on every countdown. They are now drawn once into bitmaps and only moved, scaled and faded;
- the first countdown after a page load stalled for about 0.6 s in all during its first beat, on work a browser
  does only the first time. A display now plays one countdown invisibly when it loads;
- switching the countdown's layers in when a countdown was announced held a frame for 0.2 to 0.45 s on the
  audience display, which has much else on it. The layers now stay in place, faded out.

**Tests.**
- Go: the plan of the countdown for each setting, a run to the start, calling it off by abort, by a team's
  E-stop and by the field stop, a match without a countdown, the loop waking for the start, the enable packet,
  what devices are told, the lighting moments, the display and API messages, and the setting.
- Browser code, run under Node in CI (new): what a display does with the server's clock and with each countdown
  message, including reconnecting during a countdown and after one was called off.
- SDKs: `t0` and `beats` in the Python and MicroPython libraries.
- `go test ./...` passes on all 19 packages, and the race detector is clean on every package.

## Known limits

- **Not heard.** I cannot listen to the sound. The voice and effects were made on 2026-11-13 and checked as
  waveforms only. With the example games the first phase's start cue plays its own sound at the same moment as
  "GO!"; whether the two together sound right is for a person to judge. Removing `sound: match_start` from the
  game's first cue leaves "GO!" alone.
- **Not seen on real display hardware**, and not in OBS. The in-app browser draws about one frame a second while
  its pane is hidden, which is why the measurements above were made in headless Chrome.
- **Sound on more than one display.** Every audience display plays sound unless told `sound=false`; nothing
  stops two in one room from both playing.
- **A display in a hidden browser tab** does not draw, so a countdown stays on it until the tab is shown again.
- **Lighting already set up** has no looks for the three new moments and shows its idle look during the
  countdown, until looks are given on the Lighting page.
- **Devices get beats within 50 ms**, the period at which outputs are evaluated. A device that needs better
  reads `t0` from `field/state` and keeps the field clock. The Arduino library does not read `t0` yet.
- **Existing events get the countdown** the first time they run this version: six seconds between Start and the
  match with the example games. Setup → Settings → Game turns it off.
- **The API's name for the state changed** from `starting` to `countdown`.

## What is left

Of milestone 9: OBS automation (scenes by match state, recording per match), camera recording with ffmpeg,
theming (logo, colours, fonts and replaceable sounds from the settings page), and choosing the spoken line from
the settings page rather than the game file. The plan's "exactly one display plays audio" is a per-display
option, not enforced.
