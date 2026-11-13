# Field devices

How electronics connect to Galaxia FMS: the device protocol, how inputs and outputs are bound to the match, the
Lighting page, and safety. This is the reference for firmware and for the FMS code alike. The "add your first
sensor" tutorial builds on it.

Status: the specification for milestones 5 (device framework) and 6 (firmware SDKs). It follows the protocol David
approved in [PLAN.md](PLAN.md) section 7. Additions made while writing it are marked "added". The core of the
framework is built (package `device`, described in [architecture.md](architecture.md#devices-m5)): the registry,
the MQTT broker, virtual devices, the PLC as a device, bindings, outputs and safety. Not yet: the WebSocket, HTTP and
serial transports, the Devices, Bindings and Lighting pages, and the CLI for virtual devices.

## Overview

A **device** is anything that sends field inputs to the FMS or shows field outputs: an ESP32 with a beam-break
sensor, a Pico W driving an LED strip, a Raspberry Pi running a vision program, a browser page pretending to be a
device, or the field PLC.

1. A device connects and sends its **manifest**: its id, name, firmware version and **channels**. A channel is
   one input (a button, a counter, a reading) or one output (a light, a strip, a display).
2. A device the FMS has not seen before is **pending** until someone approves it on the Devices page.
3. **Bindings**, made on the Bindings page, say what each input does (add to a score, start the match, stop a
   robot) and what each output shows. LEDs and stack lights are set on the **Lighting** page, which turns its
   scenes into output bindings.
4. Bindings are stored per game and per **field profile** (a named physical setup, such as "tent"), and can be
   exported and imported as YAML.

The FMS code never names a particular device or channel. Everything is data.

## Transports

All transports carry the same messages into the same device registry.

| Transport | Address | For |
|-----------|---------|-----|
| MQTT 3.1.1 or 5 | TCP port 1883 | ESP32, Pico W, Raspberry Pi, vision programs. The primary transport. |
| MQTT over WebSocket | `ws://<fms>:8083/mqtt` | Browser-based devices |
| WebSocket | `ws://<fms>:8080/api/v1/devices/ws` | Scripts that would rather not use MQTT |
| HTTP | `http://<fms>:8080/api/v1/devices/...` | The simplest clients: inputs only, plus long-polled outputs |
| USB serial | a port chosen on the Devices page, 115200 baud | Boards with no network |
| Virtual | in the FMS | Development and tests, driven from a web page and the command line |

The MQTT broker runs inside the FMS. The ports are settings (`DeviceMqttPort`, `DeviceMqttWsPort`); a port of 0
turns that listener off (added). MQTT over WebSocket accepts any path; `/mqtt` is the one browser clients use by
default (added).

## MQTT

### Connecting

- Client id = MQTT username = the device id: lowercase letters, digits and dashes, at most 40 characters.
- Password = the device's token.
- Set a Last Will on the status topic: payload `{"online":false}`, retained.
- Keep-alive 15 seconds.

The FMS decides as follows:

| The id is | The token | Result |
|-----------|-----------|--------|
| Unknown | anything | Accepted as **pending**. The FMS remembers the token it presented. |
| Pending | anything | Accepted as pending. The remembered token is replaced. |
| Approved | matches | Accepted. |
| Approved | does not match | Refused (bad username or password). |
| Blocked | anything | Refused (not authorised). |

Approving a pending device pins the token it last presented. A device can also be created on the Devices page
first, in which case the FMS generates the token (32 random bytes in URL-safe base64, without padding) and shows a
ready-to-paste snippet for each SDK.

Tokens are stored as SHA-256 hashes. They are long random strings, so a slow hash adds nothing.

Added:

- The refusal codes are, for MQTT 3.1.1 and 5: bad username or password `0x04` / `0x86`, not authorised `0x05` /
  `0x87`. A client id that is not a valid device id, or is reserved, is refused with `0x02` / `0x85`. The reserved
  ids are `plc` (the field PLC) and `inline` (the broker's own client). A username other than the client id is a bad
  username.
- Approving, blocking or deleting a connected device, or giving it a new token, drops its connection, so that it
  connects again with its new rights (a device that was pending must subscribe to its outputs again, which it may
  now do). An SDK must reconnect by itself. Losing the connection within five seconds of being approved is not a
  fault.
- A device that was approved without ever presenting a token (a virtual or serial one) cannot connect over MQTT
  until it is given one on the Devices page.
- The FMS takes a device to be offline when the broker sees its connection go, which is also when the broker sends
  its Last Will. The will is for other subscribers; `{"online":false}` on the status topic changes nothing by itself.
- A session a device leaves without a clean start is forgotten after 60 seconds. Devices should connect clean.

### Topics

Every topic starts with `galaxia/v1/`. `{id}` is the device id, `{ch}` a channel id.

| Topic | Direction | Retained | Payload |
|-------|-----------|----------|---------|
| `dev/{id}/manifest` | device → FMS | yes | The manifest, below |
| `dev/{id}/status` | device → FMS | yes | `{"online":true,"up":8123,"rssi":-58,"ip":"10.0.100.61","heap":182000}` every 5 s; Last Will `{"online":false}` |
| `dev/{id}/in/{ch}` | device → FMS | no | `{"v":1,"seq":42}`, optionally `"conf":0.93` and `"ts":123456` |
| `dev/{id}/hb` | device → FMS | no | `{"seq":9,"ch":{"stop":1}}` every 200 ms, from devices with safety channels |
| `dev/{id}/out/{ch}` | FMS → device | yes | An output command, below |
| `dev/{id}/cmd` | FMS → device | no | `{"do":"identify"}`, `{"do":"reboot"}` |
| `field/clock` | FMS → all | no | `{"t":1794510000123}`: UTC milliseconds, every second |
| `field/state` | FMS → all | yes | `{"state":"running","phase":"teleop","phase_remaining":93.4,"match_remaining":120.1,"scores":{"red":41,"blue":38}}` on every change and once a second |

What a device may do:

| | Publish | Subscribe |
|---|---------|-----------|
| Approved | `dev/{id}/manifest`, `dev/{id}/status`, `dev/{id}/in/+`, `dev/{id}/hb` | `dev/{id}/out/+`, `dev/{id}/cmd`, `field/#` |
| Pending | `dev/{id}/manifest`, `dev/{id}/status` | `field/#` |

Anything else is refused by the broker. A device that only wants the match clock or state needs no bindings: it
subscribes to `field/#`.

Outputs are retained, so a device that restarts gets its current outputs as soon as it subscribes. Removing a
binding clears the retained output.

Added:

- Clearing an output publishes an empty retained message, which removes the retained one; a device that receives
  an empty message on `out/{ch}` turns that output off.
- A `sound` command is not retained, so that a device that restarts does not play it again.
- `field/state` is published when the state, the phase or a score changes, and once a second otherwise; the
  remaining times alone do not count as a change. When the broker (re)starts it publishes every output in force and
  the last field state again.

### Manifest

```json
{
  "proto": 1,
  "id": "beam-red-1",
  "name": "Red goal beam break",
  "fw": "1.0.0",
  "sdk": "galaxia-arduino/0.1.0",
  "board": "esp32dev",
  "channels": [
    { "id": "count", "dir": "in", "type": "counter", "name": "Balls through goal" },
    { "id": "beam", "dir": "in", "type": "digital", "name": "Beam state" },
    { "id": "stop", "dir": "in", "type": "digital", "name": "E-stop", "safety": true },
    { "id": "strip", "dir": "out", "type": "led_strip", "name": "Goal lights", "pixels": 30 }
  ]
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `proto` | yes | Always 1 |
| `id` | yes | Must equal the MQTT client id |
| `name`, `fw`, `sdk`, `board` | no | Shown on the Devices page |
| `channels[].id` | yes | Lowercase letters, digits and underscores; unique within the device |
| `channels[].dir` | yes | `in` or `out` |
| `channels[].type` | yes | See the tables below |
| `channels[].name` | no | Shown in the binding editors |
| `channels[].safety` | no | `digital` inputs only: a normally-closed stop circuit with a heartbeat. See [Safety](#safety). |
| `channels[].pixels` | `led_strip` | Number of LEDs, 1 or more; not allowed on other types |
| `channels[].unit`, `min`, `max` | no | For `analog` inputs and `pwm` outputs only: shown in the editors |

A manifest that breaks any of these rules is refused as a whole, with every problem listed, and shows as the
device's last refusal on the Devices page (added: the "only" rules above are checked).

**Input types**

| Type | `v` | Notes |
|------|-----|-------|
| `digital` | 0 or 1 | The FMS detects edges. |
| `counter` | running total since the device started | The FMS works from differences, so a lost message loses nothing. A total lower than the last one means the device restarted, and the new total is counted in full. |
| `analog` | number | Scaling, offset and thresholds are set in the binding, not the firmware. |
| `encoder` | signed number | A position. |
| `text` | string | RFID tags, barcodes. |

Added:

- `v` may also be `true` or `false` (1 or 0). A digital value other than 0 or 1, a negative counter total, or a
  value of the wrong kind is refused and shows on the Devices page; a number sent on a `text` channel is taken as
  its text.
- The first total the FMS ever receives from a counter only sets the starting point: the FMS cannot tell how much
  of it was counted while it was not listening (before the device was approved, say). A device should report each
  counter, at 0, right after its manifest when it starts. The last total of every counter is stored, so a restart of
  the FMS counts nothing again.
- An input from a pending device, or sent before the manifest, is refused.

**Output types and commands**

| Type | Command |
|------|---------|
| `digital`, `relay` | `{"v":1}` or `{"v":0}` |
| `pwm` | `{"v":0.35}`, from 0 to 1 |
| `led_strip` | `{"p":"blink","c":["#ff0000"],"ms":500,"br":0.6}`; see below |
| `display` | `{"text":"1:32"}` or `{"v":92}` |
| `sound` | `{"play":"buzzer"}` |

An LED strip command:

| Key | Meaning |
|-----|---------|
| `p` | Pattern: `off`, `solid`, `blink`, `pulse`, `chase`, `rainbow`, `progress` |
| `c` | Colours as `#rrggbb`. `blink`, `pulse` and `chase` use the first, alternating with the second if there is one (black otherwise). `progress` fills with the first over the second. |
| `ms` | Period in milliseconds for `blink`, `pulse`, `chase` and `rainbow` |
| `v` | Fill fraction for `progress`, 0 to 1 |
| `br` | Brightness, 0 to 1 (default 1) |
| `seg` (added) | Optional list of segments, each `{"s":0,"n":10,"p":...,"c":[...],"ms":...,"v":...}` with its own pattern; LEDs not in any segment are off. When present, the top-level pattern is ignored. |

Any command may carry `"at": <UTC milliseconds>` to say when to show it. Devices keep their clock from
`field/clock` (or NTP). A command whose time has passed is shown at once.

## WebSocket

`ws://<fms>:8080/api/v1/devices/ws`. JSON text frames, one message each.

1. The device sends `{"type":"hello","id":"...","token":"...","manifest":{...}}`.
2. The FMS answers `{"type":"welcome","approved":true}`, or `{"type":"welcome","approved":false}` for a pending
   device (which may then only send `status`), or closes the socket with a reason.
3. The device sends `{"type":"status",...}`, `{"type":"in","ch":"count","v":3}` and `{"type":"hb","seq":9,"ch":{...}}`
   with the same fields as the MQTT payloads.
4. The FMS sends `{"type":"out","ch":"strip",...command}`, `{"type":"cmd","do":"identify"}`, and
   `{"type":"field",...}` with the `field/state` payload. On connect it sends every current output.

Closing the socket marks the device offline.

## HTTP

For a client that cannot hold a connection open. Every request carries `Authorization: Bearer <token>`.

| Request | Purpose |
|---------|---------|
| `PUT /api/v1/devices/{id}/manifest` | Announce or update the manifest |
| `POST /api/v1/devices/{id}/in/{ch}` | One input, body `{"v":1}` |
| `GET /api/v1/devices/{id}/out?since=<seq>&wait=30` | Waits up to `wait` seconds for an output newer than `since`, then returns `{"seq":12,"outputs":{"strip":{...}}}` |

An HTTP device counts as online for 15 seconds after each request. HTTP devices cannot carry safety channels.

## USB serial

One JSON object per line, 115200 baud, `\n` line ends.

- Device to FMS: `{"t":"manifest",...}`, `{"t":"status",...}`, `{"t":"in","ch":"count","v":3}`,
  `{"t":"hb","seq":9,"ch":{...}}`.
- FMS to device: `{"t":"out","ch":"strip",...command}`, `{"t":"cmd","do":"identify"}`,
  `{"t":"clock","t_ms":1794510000123}` every second.

A serial device needs no token: being plugged into the FMS computer is the credential. It still has to be
approved the first time.

## The PLC

The field PLC appears on the Devices page as a device named `plc`, with one `digital` input channel per PLC input
and one read-only output channel per coil. For safety, the station stops, the field stop and the "FTA ready"
switch on the PLC keep working exactly as in Cheesy Arena, directly in the arena code; they are shown on the
Devices page but are not bindings, and the coils stay driven by the arena. A PLC that is configured but cannot be
reached is a device fault and blocks the match start, as before.

Added: the `plc` device is there while a PLC address is set, and online while the PLC answers; it is never stored
and cannot be edited, blocked or deleted. Its channel ids are the PLC's names in snake case (`red1EStop` becomes
`red1_e_stop`). Its inputs may be bound like any device's; its coils cannot. The coils shown are the states the
arena drives them to; overrides made for field testing are not shown. A stop bound to a PLC input faults if the PLC
stops answering.

## Bindings

### Input bindings

```yaml
inputs:
  - id: red-goal
    device: beam-red-1
    channel: count
    action: { score: balls, op: increment, alliance: red }
    phases: [auto, teleop]          # only while one of these phases is running; empty means any time
  - id: red-climb-sensor
    device: climb-red
    channel: plate
    edge: rising                    # rising (default), falling or both: digital inputs only
    invert: false
    debounce_ms: 30                 # the input must hold its new value this long to count
    min_interval_ms: 500            # ignore further triggers for this long after one
    action: { score: climb, op: set, value: high, alliance: red, robot: 1 }
  - id: hold-timer
    device: platform-red
    channel: level
    action: { score: balance, op: hold, alliance: red }   # timer runs while the input is 1
  - id: scale
    device: load-cell
    channel: kg
    scale: 0.001                    # analog: value = raw * scale + offset
    offset: -0.2
    action: { score: payload, op: set, alliance: blue }
  - id: start-button
    device: arcade-1
    channel: button
    action: { field: start_match }
  - id: estop-red-1
    device: estop-box-red
    channel: stop
    action: { field: estop, station: R1 }
```

**Score actions** apply a change to the live score, with the device as its source. The game must allow devices
to edit the element (its `edit` list) and the element must accept the current phase; the FMS refuses the change
otherwise and shows the refusal on the Devices page.

| Op | Input types | Effect |
|----|-------------|--------|
| `increment`, `decrement` | digital (per edge), counter (by the difference) | Changes a counter, or a number by `value` (default 1) |
| `set` | digital (per edge), analog, encoder, text | Sets a boolean, enum or number. With a digital input `value` is required. With analog and encoder, the scaled reading is the value. With text, the text must be an option id of the enum. |
| `toggle` | digital (per edge) | Flips a boolean |
| `hold` | digital | Starts a timer on 1 and stops it on 0 |
| `timer_start`, `timer_stop` | digital (per edge) | Starts or stops a timer |

`alliance` is required unless the element is field-scope; `robot` (1 to the alliance size) is required for
robot-scope elements. An `analog` binding may set `threshold` (and `hysteresis`) to act as a digital input instead.

**Field actions**

| Action | Effect |
|--------|--------|
| `start_match` | Starts the match, if every start condition is met |
| `abort_match` | Aborts the match |
| `estop`, `astop` | Emergency or autonomous stop of `station`, latched as for a driver station stop |
| `field_stop` | Aborts the match and holds every robot disabled, like the field E-stop |
| `field_reset` | Signals field reset (the "field is safe to enter" lights) |
| `volunteers` | Signals volunteers to the field |
| `commit` | Commits and posts the score, as the Commit & Post button does |
| `next_match` | Loads the next match, if the current one is committed |

Field actions fire on the input's active edge. `estop`, `astop` and `field_stop` are held while the input is
active (0 on a safety channel, 1 otherwise) and latch like the station stops do.

Added, about input bindings:

- Binding ids are 1 to 64 letters, digits, dots, dashes and underscores, unique among the inputs and outputs of a
  set. Unknown keys in a binding are errors, so that a typo is not silently ignored.
- A binding to a device the FMS has not heard from yet is kept, with a warning, and checked when the device sends
  its manifest. A binding that does not fit a device's manifest (a channel it does not have, of the wrong direction
  or type, or an op that does not suit it) is an error: the set is not saved, and a saved binding that stops fitting
  when a device's manifest changes stops working and is listed as a problem until it fits again.
- A digital input rests at 0 (inactive): a first reading of 0 is no edge, a first reading of 1 is a rising one. A
  binding saved while its input is active takes that level without acting on it (a stop is held at once).
- `edge`, `invert` and `debounce_ms` are for digital inputs, and analog or encoder ones with a `threshold`. A
  threshold turns on when the scaled reading reaches it and off when it falls below `threshold - hysteresis`. A
  safety channel cannot be inverted. A debounced level that holds for `debounce_ms` counts even if no further message
  arrives. `min_interval_ms` does not apply to counters. A stop takes no `edge`, `min_interval_ms` or `phases`: it
  is held at any time. `hold` takes no `edge`.
- `scale` (default 1), `offset` (default 0) and `threshold` are for analog and encoder inputs.
- With a counter input, `increment` and `decrement` change the element by the difference and take no `value`.
  With a digital input, `value` is the amount (a positive number; a whole one for a counter element). `set` with an
  analog or encoder input sets a number element; with a text input, an enum. Such a `set` is applied only when the
  reading differs from the last one applied (a refused one is tried again with the next reading).
- Outside a match, and once the score grace after it has passed, score actions do nothing and are not reported as
  refusals. `phases` lets through input in the score grace after one of its phases ended, as the score credits it to
  that phase.
- `field_reset` and `volunteers` are refused while a match is in progress. `commit` and `next_match` are carried out
  by the web layer, off the arena loop; if it refuses, the refusal shows on the device.
- A refusal shows on the device (with how many times in a row it happened) and is published as `device.refused` at
  most once a second for the same device, binding and reason.
- The change's source is `{kind: device, id: <device id>, name: "<device name> · <channel name or id>"}`, with the
  input's `conf` as its confidence.

### Output bindings

```yaml
outputs:
  - id: stack-green
    device: stack-light
    channel: green
    when: "match_state == 'running'"          # on while true
  - id: goal-strip
    device: goal-red
    channel: strip
    alliance: red                              # "own" in expressions
    when: "own.hub_active"
    look: { p: blink, c: ["alliance"], ms: 400 }
    else: { p: solid, c: ["#101010"] }
  - id: fill
    device: timer-strip
    channel: strip
    look: { p: progress, c: ["#00c853", "#202020"], v: "phase_remaining / phase_duration" }
  - id: buzzer
    device: stack-light
    channel: buzzer
    on: phase.teleop.start                     # an event instead of a condition
    pulse_ms: 300
```

- `when` and the expressions inside `look` use the game's live expression names (see
  [game-definition.md](game-definition.md#live)), plus `stations` (by station id: `team`, `connected`, `ready`,
  `estop`, `astop`, `bypass`), `field_reset`, `volunteers` and `fault`.
- `alliance` sets `own`. The colour `"alliance"` means that alliance's colour from the game.
- `on` fires a pulse on an event instead: `match.start`, `match.end`, `match.abort`, `phase.<id>.start`,
  `phase.<id>.end`, `cue.<id>`, `score.posted`. `pulse_ms` is how long the output stays on.
- `digital`, `relay` and `pwm` outputs take `when` (on/off) or `value` (an expression giving 0 to 1 for `pwm`).
  The FMS blinks a digital output itself when its look says `blink`.
- An output channel can be driven by one binding, or by the Lighting page, not both.

The FMS evaluates output bindings 20 times a second and sends a command only when it changes.

Added, about output bindings:

- `stations` is keyed by the field's stations, `R1` to `B3`, the ones the game leaves unused included. `connected`
  means the driver station is connected and its robot linked; `ready` that nothing at the station keeps the match
  from starting.
- `on` and `when` cannot be used together. `on` needs `pulse_ms` (except on a `sound` output), and `pulse_ms` is
  only for `on`. An event that fires during a pulse starts it again.
- A `digital`, `relay` or `pwm` output needs `when`, `value` or `on`. With `value` and `when`, the value applies
  while the condition holds. Its `look` and `else` may only set `p` (`off`, `solid` or `blink`) and `ms` (the
  blink period, 1000 by default); `else` is how it looks while the condition is false (off without one). Blinking
  starts on, from the moment the condition last changed. A `pwm` value is clamped to 0..1.
- A `led_strip` output needs a `look`; without an `else` it is off while the condition is false. Colours are
  `#rrggbb` or `alliance`, which needs the binding's `alliance`. Fills (`v`, a number or an expression) are clamped
  to 0..1 and rounded to a thousandth, so that a fill that follows the clock is not sent on every evaluation.
- A `display` output needs a `value`: a string is shown as `{"text":...}`, a number as `{"v":...}`. While `when` is
  false (or after a pulse) it shows `{"text":""}`.
- A `sound` output takes `look: { play: <sound> }` and `on` or `when`: it plays when the event fires or the
  condition turns true. Nothing else applies to it.
- The PLC's coils cannot be bound. A binding to a device whose manifest is not known yet sends nothing until it is.
- An output whose expression fails keeps its last command; the failure is logged once.
- A digital output that blinks sends `{"v":1}` and `{"v":0}` in turn, and each is a `device.output` event.

## The Lighting page

LEDs and stack lights are configured on Setup → Lighting, without writing bindings.

- **Fixtures** (per field profile): every `led_strip`, `digital`, `relay` and `pwm` output that should show
  lighting. Each has a name, an optional alliance, a brightness limit, an optional reversed direction, and named
  **segments** (a start LED and a count, each with an optional alliance of its own).
- **Scenes** (per game and field profile): a grid with field moments down the side and fixtures or segments
  across the top. Each cell is a look: pattern, colours, period, optional progress value, and an optional
  `when` condition. Colours can be fixed, `alliance` (the fixture's or segment's alliance), `winner` (the
  winning alliance's colour) or a theme colour.
- **Moments**, highest priority first:

  | Moment | When |
  |--------|------|
  | `estop` | Any station E-stop or the field stop is active |
  | `fault` | A device fault is latched |
  | `cue.<id>` | For 2 seconds after the cue fires |
  | `phase.<id>` | While that phase runs |
  | `score_posted` | For 10 seconds after a score is posted |
  | `match_end` | After the match, until the score is posted |
  | `field_reset` | Field reset signalled |
  | `volunteers` | Volunteers signalled |
  | `teams_ready` | Before the match, with every station ready |
  | `awards` | The alliance station displays show the logo |
  | `idle` | Otherwise |

  The countdown moments (`countdown.beat`, `countdown.go`) arrive with the countdown in milestone 9.
- A cell left empty falls through to the next moment down that has a look for it.
- **Preview** shows each fixture as a simulated strip. **Test** sends a scene to the hardware. **Identify**
  flashes one fixture.
- A new fixture gets a starting set of scenes: alliance colour during the match, green on field reset, purple for
  volunteers, red blinking on E-stop, orange blinking on a fault, white at idle.

## Safety

- A `safety` input is wired normally closed: the circuit is closed, and the input reads 1, while things are
  safe. A broken wire reads as a stop.
- A device with safety channels sends `hb` every 200 ms with the state of each.
- If a device bound to a stop is silent for 1 second, disconnects, or reports a safety channel as 0, the FMS acts
  as if the stop was pressed and **latches a fault**. The fault stays until an operator clears it on the Devices
  page or Match Play, and only clears if the device is back and reporting safe.
- A match cannot start while a device bound to a stop is offline or faulted, or while any device marked
  **required** is offline.
- **A wireless device must never be the only E-stop path.** The driver station E-stops and the wired field stops
  are the primary stops. A wireless stop box is a convenience on top of them.

Added:

- The heartbeat watchdog watches every approved, connected device that has a safety channel bound to a stop.
  Silence is counted from the last heartbeat, or from the moment the device connected; when a device falls silent,
  every stop bound to it faults, since none of them can be trusted.
- A safety channel that reads 0 latches a fault on its stop, which holds the stop after it reads 1 again. A plain
  digital input bound to a stop holds the stop while it is active and latches nothing.
- Faults are also latched on every stop of a device that disconnects (unless it is reconnecting because it was just
  approved), that connects again before its old connection was seen to go, or that is blocked.
- A fault is cleared on request only if the device is approved and connected, has sent a heartbeat within the last
  second (if it has a safety channel bound to a stop), and reports every stop bound to it as released; otherwise the
  request says why. Removing a stop binding drops its fault.
- A stop device that is pending, blocked or has never connected also keeps a match from starting, and so does an
  active field stop from a device. Each condition names the device and the stops bound to it.
- Without a PLC, only the stations a device can stop are handed device stops; the others behave as before. With a
  PLC, a station is stopped if either stops it, and either's field stop aborts the match.

## Field profiles

A field profile is a named physical setup, such as "tent" or "school gym". Bindings and lighting scenes are kept
per game and per profile; fixtures are kept per profile. The active profile is chosen in Settings. A new event
starts with one profile, `default`.

Added: a profile's name is free text, unique; renaming a profile keeps its bindings, and the active profile cannot
be deleted. Binding sets are stored as the YAML the Bindings page exports.

## Events

Devices publish on the event bus:

| Type | Data |
|------|------|
| `device.online`, `device.offline` | `id`, `transport` |
| `device.pending` | `id`, `name` |
| `device.manifest` | `id`, the manifest |
| `device.input` | `id`, `channel`, `value`, `conf` |
| `device.output` | `id`, `channel`, the command |
| `device.fault`, `device.fault_cleared` | `id`, `binding`, `reason` |
| `device.refused` | `id`, `binding`, `reason`: an input the FMS would not apply |

Added: `device.pending` is published when an unknown device first connects, with its id as its name (the manifest
comes later). `device.input` is published for every input message, and for a heartbeat's channel only when its
value changes; `conf` is left out when the input had none. `device.output` is published for every command sent,
including clearing (a null command). `device.refused` also covers a malformed message, a manifest that is refused
and a field action that failed (`binding` is empty when no binding was involved); it is published at most once a
second for the same device, binding and reason.
