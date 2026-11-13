# Field device examples

Nine small devices for a practice field, each with an Arduino sketch for the ESP32 (and a MicroPython version for
the Raspberry Pi Pico W where it makes sense), a parts list, a wiring table and diagram, and the binding to make in
the FMS. New to this? Start with [the first sensor tutorial](../docs/tutorial-first-sensor.md).

| Example | Channel | Does | MicroPython |
|---------|---------|------|-------------|
| [beam-break-counter](beam-break-counter/) | `counter` | Adds a ball to the score each time the beam is broken | yes |
| [arcade-button](arcade-button/) | `digital` | Starts the match | yes |
| [limit-switch](limit-switch/) | `digital` | Runs a timer element while a robot holds a platform down | yes |
| [stack-light](stack-light/) | 4 × `relay` | Red, amber, green and a buzzer that follow the match | yes |
| [led-strip](led-strip/) | `led_strip` | Every lighting pattern, set on the Lighting page | yes |
| [estop-box](estop-box/) | `digital`, safety | A wireless E-stop for one station (never the only E-stop path) | yes |
| [match-timer](match-timer/) | none (reads `field/state`) | Shows the time left on a MAX7219 LED matrix | no |
| [load-cell](load-cell/) | `analog` | Weighs a payload; kilograms come from the binding's scale and offset | no |
| [rfid-reader](rfid-reader/) | `text` | Sets an enum element from an RFID tag | no |

## Before you flash

Every sketch has the same three lines to change:

```cpp
dev.wifi("field-devices", "wifi-password");   // the field network's name and password
dev.connect("10.0.100.5", "device-token");    // the FMS computer's address, and this device's token
```

- **The FMS address** is the field computer's address on the network the device joins.
- **The token** is the device's password. Either create the device on Setup → Devices first and copy the token
  it shows, or make up a long random one (a password generator is fine): an unknown device is accepted as
  pending, and approving it keeps the token it presented.
- **The device id** (`"beam-red-1"` and so on) must be unique on the field: lowercase letters, digits and dashes.
  Give each copy of a device its own (`beam-red-1`, `beam-blue-1`).

## Building

Each folder is a PlatformIO project: install [PlatformIO](https://platformio.org/install), open a terminal in the
example's folder, and run `pio run -t upload`, then `pio device monitor` to watch it connect. The examples take
the Galaxia library straight from [sdk/arduino](../sdk/arduino/). With the Arduino IDE, open the `.ino` file in
`arduino/` and install the library as described in the SDK's README.

`python examples/build_all.py --strict` builds every example (and the library's own) in one go.

The MicroPython versions run on a Pico W or Pico 2 W with the SDK installed (see
[sdk/micropython](../sdk/micropython/)); copy `micropython/main.py` to the board.

## Bindings

Each README ends with the binding to make on Setup → Bindings, as YAML in the format of
[docs/devices.md](../docs/devices.md). Element names such as `balls` or `payload_mass` come from the example games
in [games/](../games/); use your own game's elements, which must list `device` in their `edit` list.

The code in this folder is MIT licensed ([LICENSE](LICENSE)); the FMS itself is under Team 254's Cheesy Arena
licence.
