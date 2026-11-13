# Galaxia for MicroPython (Raspberry Pi Pico W and Pico 2 W)

`galaxia.py` connects a Pico W to Galaxia FMS. Same ideas and names as the Arduino library; the protocol is in
[docs/devices.md](../../docs/devices.md) and the behaviour shared by all SDKs in [sdk/README.md](../README.md).

This module is MIT licensed ([LICENSE](../LICENSE)); the FMS itself is under Team 254's Cheesy Arena licence.

```python
import galaxia

dev = galaxia.Device("beam-red-1", "Red goal beam break")
balls = dev.counter_pin("count", 15)        # beam-break receiver between GP15 and ground

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
```

## Install

Needs MicroPython 1.29 or newer on the board (download the Pico W or Pico 2 W firmware from micropython.org and
copy the `.uf2` onto the board while holding BOOTSEL), and `mpremote` on the computer (`pip install mpremote`).

With the board on USB and connected to the internet through the computer:

```sh
mpremote mip install github:DavidMasin/galaxia-fms/sdk/micropython/package.json@main
```

That installs `galaxia.py` and its one dependency, `mqtt_as` (peterhinch/micropython-mqtt), pinned to commit
`4dbce9ba8df169b1c1356deba14aec0bf7cde664` (2026-10-18; its `VERSION` is 0.8.5). Without internet on the board,
copy the files instead:

```sh
mpremote mip install github:peterhinch/micropython-mqtt@4dbce9ba8df169b1c1356deba14aec0bf7cde664
mpremote cp sdk/micropython/galaxia.py :lib/galaxia.py
```

Then copy your program as `main.py` so it runs at power-up: `mpremote cp main.py :main.py`, and watch it with
`mpremote repl`. The `neopixel` module used for LED strips is built into the Pico firmware.

## Why mqtt_as

`mqtt_as` is an asyncio MQTT client written for flaky Wi-Fi on microcontrollers: it notices a dead connection
itself, reconnects Wi-Fi and the broker without blocking the program, and supports the Last Will. The SDK uses its
event interface (`up`, `down`, `queue`).

Two details of `mqtt_as` matter for Galaxia, and `galaxia.py` subclasses its client for both:

- It treats a refused subscription (which the FMS sends to a device still pending approval) as a broken
  connection and reconnects, Wi-Fi included, again and again. The subclass notes the refusal and keeps the
  connection. When you approve the device, the FMS drops the connection; `mqtt_as` reconnects (Wi-Fi included,
  which takes about 7 seconds) and the outputs arrive. If a future `mqtt_as` reports refusals differently, the
  SDK falls back to asking at most every 30 seconds.
- A refused connection (wrong token, blocked device, reserved id) would be retried every few seconds. The subclass
  prints the reason (`dev.refusal` holds the code: 2, 4 or 5) and waits 30 seconds before the next attempt, so a
  device that is approved again or given its token recovers by itself.

Precision: the Pico's floats are single precision, while the FMS and its Lighting page preview draw LED patterns
in double precision. Fills and brightness still come out the same (see "LED strips" in [sdk/README.md](../README.md));
a pulse or rainbow colour can be one step of 255 away at the odd instant.

## API

| Call | |
|------|---|
| `dev = galaxia.Device(id, name=None, fw="1.0.0", board=None)` | `board` defaults to the MicroPython machine name. |
| `dev.wifi(ssid, password)`, `dev.connect(host, token, port=1883)` | Settings; the connection is made by `run()`. |
| `dev.run(loop=None, interval_ms=5)` | Runs forever, calling your `loop()` function every `interval_ms`. |
| `await dev.start()` | For asyncio programs: starts the SDK's tasks and returns. |
| `dev.connected`, `dev.approved`, `dev.refusal`, `dev.field_state`, `dev.phase_remaining()`, `dev.field_time_ms()` | State. `refusal` is the code of the last refused connection (2, 4 or 5) or None. `field_state` has `state`, `phase`, `phase_remaining`, `match_remaining`, `scores`. |
| `dev.on_field_state(fn)`, `dev.on_command(fn)`, `dev.on_identify(fn)`, `dev.on_reboot = fn` | Callbacks. |

Inputs:

| Call | |
|------|---|
| `c = dev.counter(id)`; `c.increment(n=1, conf=None)` | Running total since start. |
| `dev.counter_pin(id, pin, pull="up", edge="falling", debounce_ms=2)` | Counts edges in an interrupt. `edge` is `"falling"`, `"rising"` or `"both"`. |
| `d = dev.digital(id, debounce_ms=0, invert=False)`; `d.set(value, conf=None)` | 0 or 1 on change. |
| `dev.digital_pin(id, pin, pull="up", invert=True, debounce_ms=20)` | A button to ground reads 1 while pressed. |
| `a = dev.analog(id, unit=None, min=None, max=None, deadband=0, min_interval_ms=100)`; `a.set(x)` | A number. |
| `e = dev.encoder(id)`; `e.set(n)` | A signed position. |
| `t = dev.text(id)`; `t.set("04a1b2c3")` | A string, each time. |
| `dev.safety_stop(id, pin=None, pull="up", recover_ms=50)` | Normally-closed stop contact from the pin to ground, heartbeat every 200 ms. Without a pin, call `set(safe)`. |

Outputs:

| Call | |
|------|---|
| `dev.digital_out(id, pin=None, on_change=None, active_low=False)`, `dev.relay(...)` | `{"v":1}` drives the pin and/or calls `on_change(on)`. |
| `dev.pwm(id, pin=None, on_change=None, freq=1000)` | `{"v":0.35}` sets the duty cycle. |
| `dev.led_strip(id, pin, pixels, limit=1.0, on_frame=None)` | A WS2812 strip; every pattern drawn locally. |
| `dev.display(id, on_change=None)` | `on_change(text, number)`. |
| `dev.sound(id, on_play=None)` | `on_play(name)`. |

Every factory takes an optional `name` after the pin (or after the id when there is no pin).

## Tests

There is no board in the test setup, so `tests/` runs `galaxia.py` under CPython with small stand-ins for
`machine`, `network`, `neopixel` and `mqtt_as` (the `mqtt_as` stand-in copies the behaviour of 0.8.5 that the SDK
relies on, including its handling of refused subscriptions):

```sh
python -m pytest sdk/micropython/tests
```

`mpy-cross galaxia.py` (pip install mpy-cross) checks that the file compiles for MicroPython 1.29.

**Not tested here:** the module on a real Pico W or Pico 2 W, against the real `mqtt_as`, and the Pico 2 W
(which `mqtt_as` does not list as tested). Please report what you find.
