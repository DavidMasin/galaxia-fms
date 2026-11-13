# Galaxia device SDKs

Libraries that connect a sensor, button, light or LED strip to Galaxia FMS with a few lines of code. They speak the
MQTT protocol in [docs/devices.md](../docs/devices.md) and handle everything around it: reconnecting, the manifest,
status, heartbeat, the field clock, scheduled outputs and LED strip patterns.

| SDK | For | Folder |
|-----|-----|--------|
| Arduino / PlatformIO library `Galaxia` | ESP32 over Wi-Fi or Ethernet | [arduino/](arduino/) |
| MicroPython module `galaxia` | Raspberry Pi Pico W and Pico 2 W | [micropython/](micropython/) |
| Python package `galaxia-device` | Raspberry Pi, vision programs, scripts on any computer | [python/](python/) |

Start with the [first sensor tutorial](../docs/tutorial-first-sensor.md) and the [examples](../examples/).

The SDKs are released under the MIT licence ([LICENSE](LICENSE)), so they can go into any team's code. The FMS
itself is a modified Cheesy Arena and stays under Team 254's licence (see the repository's `LICENSE` and
`NOTICE.md`).

The three SDKs share one API shape:

```cpp
GalaxiaDevice dev("beam-red-1", "Red goal beam break");   // Arduino
Counter balls = dev.counterPin("count", 27);
```

```python
dev = galaxia.Device("beam-red-1", "Red goal beam break")  # MicroPython
balls = dev.counter_pin("count", 15)
```

```python
dev = Device("vision-red", "Red goal camera")             # Python
balls = dev.counter("count")
balls.increment(conf=0.93)
```

## Behaviour shared by every SDK

This section is for anyone writing another client, and for the FMS side. Where [docs/devices.md](../docs/devices.md)
leaves a choice open, the choice made here is marked **(SDK choice)**.

### Connection

- Client id and MQTT username are the device id; the password is the token. MQTT 3.1.1, clean session,
  keep-alive 15 s, Last Will `{"online":false}` retained (QoS 0) on `dev/{id}/status`. The ids `plc` and `inline`
  are reserved and refused by the SDKs themselves.
- Every publication is QoS 0; every subscription asks for QoS 0.
- After a lost connection the SDK reconnects by itself, waiting 1 s, then 2, 4, 8 … up to 30 s between attempts.
  (The MicroPython SDK leaves this to `mqtt_as`, which reconnects Wi-Fi as well.)
- On every connection, in this order: the manifest (retained), the status (retained), the current value of every
  input (see Inputs), a subscription to `field/#`, then subscriptions to `dev/{id}/out/+` and `dev/{id}/cmd`.
- **Pending devices.** The broker refuses the `out/+` and `cmd` subscriptions of a device waiting for approval, and
  the device does not ask again on that connection. Approving (or blocking, or re-tokening) a device makes the FMS
  drop its connection; the device reconnects and announces itself afresh, and this time the subscriptions are
  accepted. The Arduino MQTT library closes the connection itself when a subscription is refused, so a pending
  Arduino device connects twice in a row (once to be refused, once to stay); the others keep the connection.
- **Refused connections.** CONNACK return code 4 (wrong token), 5 (blocked) and 2 (invalid or reserved id) are
  reported plainly (Serial log, `print`, or the Python `Refused` exception and log) and retried every 30 s or so,
  so a device that is approved again or given its token recovers without a restart.
- A clean `disconnect()` (and a reboot) publishes `{"online":false}` retained first, since a clean disconnect
  does not fire the Last Will.

### Status

Every 5 seconds, retained: `{"online":true,"up":8123,"rssi":-58,"ip":"10.0.100.61","heap":182000}`.
`up` is whole seconds since the board started **(SDK choice: seconds)**. `rssi` is sent on Wi-Fi only, `heap`
(free bytes) by the microcontroller SDKs only, `ip` whenever it is known.

### Inputs

- Payload `{"v":…,"seq":n}`, never retained. `seq` is one counter per device, shared by all its input channels,
  starting at 1 when the program starts **(SDK choice)**; it restarts after a reboot.
- `conf` (Python and MicroPython) is rounded to three decimals. `ts` is only sent when the program passes one
  (Python); the SDK does not invent it, because the protocol does not say which clock it is in.
- Right after the manifest, on every connection, each input's current value is sent: every counter's total (0
  after a reboot), and the last value of digital, analog, encoder and safety channels once there is one. The FMS
  takes the first total it hears from a counter as its starting point, so this is what makes the next ball count.
  Totals keep growing across reconnections; only a reboot starts them at 0 again.
- Nothing is queued while the connection is down: a button pressed during an outage must not reach the FMS
  seconds later. The values sent on reconnecting cover what happened meanwhile, so a counter loses nothing.
- `counter`: the total since the program started; it only goes up. `counterPin` (Arduino) and `counter_pin`
  (MicroPython) count edges in an interrupt with a short dead time (2 ms by default) against contact bounce.
- `digital`: with a debounce time, a new level must hold that long before it is sent. The pin helpers default to
  20 ms and report a button to ground as 1 while pressed.
- `analog` and `encoder`: a change within the dead band is not sent, and at most one value is sent per minimum
  interval (100 ms for analog, 50 ms for encoder); the latest value goes out when the interval ends.
- `text`: every `set` is sent, even the same text twice; it is not sent again on reconnecting.

### Safety channels

- Always `"type":"digital"` with `"safety":true`. Wired normally closed: 1 while the circuit is closed (safe), 0
  when it opens. With a pin, the contact goes from the pin to ground with the internal pull-up, so a cut wire reads
  as a stop.
- A stop is sent at once. A return to safe must hold for 50 ms first. Until the first reading the channel reports 0.
- `dev/{id}/hb` carries `{"seq":n,"ch":{"stop":1}}` every 200 ms, plus one extra heartbeat at the moment a safety
  channel changes. Its `seq` is a separate counter from the inputs' **(SDK choice)**. The change is also sent on
  `in/{ch}`.
- On the ESP32, Wi-Fi power saving is turned off, because it delays packets by up to a few hundred milliseconds.

### Outputs

- A command is applied as it arrives, unless it has an `at` time in the future.
- **Scheduling (SDK choice).** Each output keeps a short list of commands waiting for their `at` time (8 on the
  microcontrollers, 32 in Python). A new command removes every waiting command due at or after its own time, then
  waits its turn; a command with no `at`, or with an `at` already past, is applied at once and clears the list.
  So commands sent ahead for successive moments (countdown beats) all play, while an immediate command (a
  cancelled countdown) cancels whatever was waiting. If the list is full, the earliest waiting command is dropped.
- Time comes from `field/clock`. Until the first message the microcontrollers apply every `at` at once, and the
  Python SDK uses the computer's clock.
- An empty message on `out/{ch}` (the output was unbound) turns the output off: `v` 0, strip dark, display blank.
- `sound` commands are never retained by the FMS; the Python and MicroPython SDKs also ignore any that arrive
  with the retained flag, so a restart can never replay one.
- `digital`/`relay`: any non-zero `v` is on. `pwm`: `v` clamped to 0 to 1. `display`: `text`, or `v` formatted
  with `%g` (`{"v":92}` shows `92`).

### LED strips

Patterns are drawn exactly as [docs/devices.md](../docs/devices.md) defines them ("Pattern definitions"), from the
field clock `t` itself, so strips on different devices stay in step and match the Lighting page's preview. Each
SDK follows the FMS's reference implementation, `static/js/led_patterns.js`, operation for operation in double
precision: the same default periods (blink 500, pulse 1500, chase 1000, rainbow 3000 ms), the same segment
clipping (a segment's `n` is cut to the strip, and its pattern is drawn over the LEDs that remain; an empty `seg`
list means the top-level look) and `Math.round` for the final colours. `at` only says when a command starts to
show; it does not restart the animation.

Notes on precision:

- **Arduino.** ArduinoJson 7 stores a short decimal such as `0.35` as a `float`, not the double the FMS sent. The
  library finds the shortest decimal that gives that float and reads it back as a double, so `br`, `v` and `ms`
  are the FMS's own values (exact for decimals of up to 7 significant digits).
- **MicroPython.** The Pico's floats are single precision. A fill or brightness that lands exactly on a half
  (`0.35 × 10`) is rounded up as the reference does, for values given to a thousandth as the FMS sends them; a
  pulse or rainbow colour can be one step (of 255) off the preview at the odd instant one lands on a half.
- `limit` (Arduino `strip.limit(x)`, Python and MicroPython `limit=`) is the SDKs' own brightness cap for small
  power supplies. It multiplies `br`; at its default of 1 nothing changes.
- Identify flashes the strip white at half brightness, 250 ms on and 250 ms off, for 5 s, then the look returns.

[testdata/led_cases.json](testdata/led_cases.json) holds 87 commands rendered by the reference implementation
(`node sdk/testdata/make_led_cases.js` writes it). The tests of all three SDKs check against it.

### Field clock

Each `field/clock` message gives one sample of FMS time minus local time. Network delay only ever makes a sample
too small, so the estimate is the largest of the last 8 samples. A sample more than 1 second away from the
estimate is ignored as a fluke, unless 3 arrive in a row, in which case the FMS clock really changed and the
estimate starts again from them.

### Identify and reboot

- `{"do":"identify"}`: for 5 seconds the on-board LED blinks at 4 Hz (ESP32: `LED_BUILTIN`; Pico W: `"LED"`)
  and every LED strip flashes white. A callback can add more.
- `{"do":"reboot"}`: status offline, disconnect, restart (`ESP.restart()`, `machine.reset()`; the Python SDK
  restarts the program with the same arguments). A callback can replace this.
- Any other `do` is passed to the program's command callback.

### field/state

Kept as the last message: `state`, `phase`, `phase_remaining`, `match_remaining` and `scores`. Between messages,
while `state` is `running`, the SDKs count `phase_remaining` down locally so a timer display moves smoothly.

## Tests

| What | Command | Needs |
|------|---------|-------|
| Python SDK: an in-memory broker for the protocol, the reference cases for LED patterns, and 3 tests with the real paho-mqtt over a local socket | `python -m pytest sdk/python` | `pip install paho-mqtt pytest gpiozero` |
| MicroPython SDK under CPython, with stand-ins for `machine`, `network`, `neopixel` and `mqtt_as` | `python -m pytest sdk/micropython/tests` | `pytest` |
| Arduino library logic on a PC: patterns against the reference cases, JSON reading, schedule, clock | `python sdk/arduino/test/native/run_native_tests.py --arduinojson <ArduinoJson/src>` | a C++17 compiler, or `pip install ziglang` |
| Every Arduino example builds for the ESP32, with no warnings in our code | `python examples/build_all.py --strict` | `pip install platformio` |
| The reference cases | `node sdk/testdata/make_led_cases.js` | Node.js |
