# galaxia-device: Galaxia FMS devices in Python

For a Raspberry Pi with buttons and lights, a vision program that counts game pieces, or any script that should
feed the field. Same ideas and names as the Arduino and MicroPython SDKs; the protocol is in
[docs/devices.md](../../docs/devices.md) and the behaviour shared by all SDKs in [sdk/README.md](../README.md).

This package is MIT licensed ([LICENSE](LICENSE)); the FMS itself is under Team 254's Cheesy Arena licence.

```python
from galaxia import Device

dev = Device("vision-red", "Red goal camera")
balls = dev.counter("count", "Balls seen")
dev.connect("10.0.100.5", "device-token")   # connects and reconnects in the background

for detection in camera_detections():        # your vision loop
    balls.increment(conf=detection.confidence)
```

## Install

Python 3.9 or newer, with [paho-mqtt](https://pypi.org/project/paho-mqtt/) 2.x (installed automatically).

```sh
pip install "galaxia-device @ git+https://github.com/DavidMasin/galaxia-fms#subdirectory=sdk/python"
# with the Raspberry Pi GPIO helpers:
pip install "galaxia-device[gpio] @ git+https://github.com/DavidMasin/galaxia-fms#subdirectory=sdk/python"
# or from a copy of this repository:
pip install ./sdk/python
```

## The device

| Call | |
|------|---|
| `Device(id, name=None, fw="1.0.0", board=None)` | `id`: lowercase letters, digits and dashes, at most 40, not `plc` or `inline`. `board` defaults to the Raspberry Pi model, or the OS and machine. |
| `dev.connect(host, token, port=1883, background=True)` | Starts connecting. A background thread keeps the connection, status, heartbeat and outputs going; with `background=False` call `dev.loop()` yourself at least every 10 ms. |
| `dev.wait_connected(timeout=None)` | Blocks until connected. After `timeout` seconds it raises `galaxia.Refused` (with `.code` 2, 4 or 5 and a plain message) if the FMS refused the device, or `TimeoutError`. |
| `dev.connected`, `dev.approved`, `dev.refusal` | State. `approved` is true once the FMS accepts the device's outputs. |
| `dev.run_forever()`, `dev.disconnect()` | Block forever; or say goodbye (status offline) and stop. |
| `dev.field_state`, `dev.phase_remaining()`, `dev.field_time_ms()` | The last `field/state` (`state`, `phase`, `phase_remaining`, `match_remaining`, `scores`), the phase time counted down locally, and the FMS clock in UTC ms. |
| `dev.on_field_state(fn)`, `dev.on_field_message(fn)`, `dev.on_command(fn)`, `dev.on_identify(fn)`, `dev.on_connect(fn)`, `dev.on_reboot = fn` | Callbacks. They run on the SDK's threads: keep them short. Without `on_reboot`, a reboot command restarts the program. |

## Channels

Inputs (add them before `connect()`):

| Call | Then |
|------|------|
| `c = dev.counter(id, name=None)` | `c.increment(n=1, conf=None, ts=None)`, `c.total` |
| `d = dev.digital(id, name=None, debounce_ms=0, invert=False)` | `d.set(True, conf=None)` |
| `a = dev.analog(id, name=None, unit=None, min=None, max=None, deadband=0, min_interval_ms=100)` | `a.set(12.5)` |
| `e = dev.encoder(id, name=None)` | `e.set(-3)` |
| `t = dev.text(id, name=None)` | `t.set("04a1b2c3")` |
| `s = dev.safety_stop(id, name=None, recover_ms=50)` | `s.set(safe)`: `True` while the stop circuit is closed |

`conf` is a confidence from 0 to 1, for vision programs. `ts` is passed through as given.

Outputs:

| Call | Callback |
|------|----------|
| `dev.digital_out(id, on_change=fn)`, `dev.relay(id, on_change=fn)` | `fn(on)` |
| `dev.pwm(id, on_change=fn, unit=None, min=None, max=None)` | `fn(level)`, 0 to 1 |
| `dev.led_strip(id, pixels, on_frame=fn, limit=1.0)` | `fn(frame)`: a list of `(r, g, b)` tuples each time the picture changes. Every pattern of docs/devices.md is drawn by the SDK from the field clock, exactly as the Lighting page previews them; send the frame to your strip driver (for example `rpi_ws281x`). |
| `dev.display(id, on_change=fn)` | `fn(text, number)`; `number` is `None` for text |
| `dev.sound(id, on_play=fn)` | `fn(name)` |

`galaxia.render(command, pixels, t)` draws any `led_strip` command, for previews and tests.

## Raspberry Pi GPIO

`galaxia.gpio` wires channels to pins with [gpiozero](https://gpiozero.readthedocs.io) (BCM numbers):

```python
from galaxia import Device, gpio

dev = Device("arcade-1", "Start button")
gpio.digital_input(dev, "button", 17)            # button from GPIO 17 to ground: 1 while pressed
gpio.counter_input(dev, "count", 27)              # counts presses (a beam break pulling the pin low)
gpio.safety_input(dev, "stop", 22)                # normally-closed stop contact from GPIO 22 to ground
gpio.digital_output(dev, "lamp", 5)               # an LED or a relay; active_high=False for most relay boards
gpio.pwm_output(dev, "fan", 13)
dev.connect("10.0.100.5", "device-token")
dev.run_forever()
```

## Tests

```sh
pip install paho-mqtt pytest gpiozero
python -m pytest sdk/python
```

The tests run without the FMS: `tests/fake_broker.py` is an in-memory broker with the FMS's access rules (and
paho's own callback types), `tests/mini_broker.py` a small MQTT broker on a local socket for three tests with the
real paho-mqtt client, and the LED tests compare against the cases rendered by the FMS's reference implementation.
