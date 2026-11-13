# Galaxia library for ESP32 (Arduino and PlatformIO)

Connects an ESP32 to Galaxia FMS: wire a sensor, button or light, add a few lines, approve the device on the FMS
and bind it to the match. The library handles the connection and reconnection, the manifest, status, heartbeat,
field clock, scheduled outputs and LED strip patterns. The protocol is in [docs/devices.md](../../docs/devices.md);
behaviour shared with the other SDKs is in [sdk/README.md](../README.md).

This library is MIT licensed ([LICENSE](LICENSE)); the FMS itself is under Team 254's Cheesy Arena licence.

```cpp
#include <Galaxia.h>

GalaxiaDevice dev("beam-red-1", "Red goal beam break");
Counter balls = dev.counterPin("count", 27);   // beam-break receiver between GPIO 27 and ground

void setup() {
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
```

## Install

**PlatformIO** (recommended). In `platformio.ini`:

```ini
[env:esp32dev]
platform = espressif32@8.0.0
board = esp32dev
framework = arduino
lib_deps =
  symlink://path/to/galaxia-fms/sdk/arduino
```

The dependencies below are installed automatically from `library.json`. The examples in
[examples/](../../examples/) each have a ready `platformio.ini`: `pio run -t upload` builds and flashes.

**Arduino IDE.** Zip this `sdk/arduino` folder and use Sketch → Include Library → Add .ZIP Library, then install
"MQTT" by Joel Gaehwiler, "ArduinoJson" by Benoit Blanchon and "Adafruit NeoPixel" from the Library Manager.
Select an ESP32 board (Arduino-ESP32 core 3.x).

## Dependencies

Pinned in `library.json`, with versions read from the PlatformIO registry on 2026-11-13:

| Library | Version | Why |
|---------|---------|-----|
| `256dpi/MQTT` (arduino-mqtt) | ~2.5.3 (2026-04-16) | MQTT 3.1.1 with Last Will over any Arduino `Client`, so Wi-Fi and Ethernet share one code path. Created with 2 KB buffers (`GALAXIA_MQTT_BUFFER`) for the manifest. |
| `bblanchon/ArduinoJson` | ~7.4.3 (2026-03-02) | Reading commands and writing messages. |
| `adafruit/Adafruit NeoPixel` | ~1.15.5 (2026-05-12) | LED strips. |

**Why Adafruit NeoPixel rather than FastLED.** FastLED takes the data pin as a compile-time template argument
(`FastLED.addLeds<WS2812B, 4>`), so `dev.ledStrip("strip", pin, pixels)` could not take the pin as an ordinary
argument. Adafruit NeoPixel takes the pin and length at run time, has a small and stable API, supports RGB and RGBW
strips, and drives the ESP32's RMT peripheral on the 3.x core. The library does all pattern maths itself, so none
of FastLED's colour functions are needed.

Built and checked with the `espressif32@8.0.0` PlatformIO platform (Arduino-ESP32 core 3.3.12, GCC 14.2).

## API

### The device

| Call | |
|------|---|
| `GalaxiaDevice dev(id, name, firmware)` | `id`: lowercase letters, digits and dashes, at most 40, not `plc` or `inline`. `name` and `firmware` (default `"1.0.0"`) show on the Devices page. |
| `dev.wifi(ssid, password)` | Use the ESP32's Wi-Fi (power saving off, automatic reconnection). |
| `dev.ethernet(client, localIpFunction)` | Use any Arduino `Client`: a W5500 `EthernetClient` or a `NetworkClient` over the ESP32's own Ethernet. See [examples/EthernetW5500](examples/EthernetW5500) and [examples/EthernetESP32](examples/EthernetESP32). |
| `dev.connect(host, token, port = 1883)` | Where the FMS is. Call in `setup()`; the connection itself is made by `loop()`. |
| `dev.loop()` | Call on every pass of `loop()`. Avoid `delay()` in the sketch: it holds up the heartbeat and the outputs. |
| `dev.disconnect()` | Status offline, then close. |
| `dev.connected()`, `dev.approved()` | Connected to the broker; output subscriptions accepted (the device is approved). |
| `dev.refusal()` | Why the FMS refused the last attempt: 2 (id invalid or reserved), 4 (wrong token), 5 (blocked), or 0. The reason is also logged, and the attempt repeated every 30 s. |
| `dev.field()` | The last `field/state`: `valid`, `state`, `phase`, `phaseRemaining`, `matchRemaining`, `phaseRemainingNow()` (counted down locally while running), `score("red")`. |
| `dev.onFieldState(fn)` | `fn(const FieldState&)` on each `field/state`. |
| `dev.fieldTimeMs()`, `dev.clockSynced()` | FMS time (UTC ms) from `field/clock`, 0 before the first message. |
| `dev.onCommand(fn)` | `fn(const char* action, JsonObjectConst command)` for commands other than identify and reboot. |
| `dev.onIdentify(fn)`, `dev.identifyPin(pin)` | Extra identify behaviour; the LED that blinks (default `LED_BUILTIN`, -1 for none). |
| `dev.onReboot(fn)` | Replace the restart. |
| `dev.setBoard(text)`, `dev.setFirmware(text)`, `dev.logTo(&Serial or nullptr)` | Manifest fields (board defaults to the Arduino board name) and where log lines go (Serial, started at 115200 if needed). |

### Inputs

| Call | Sends |
|------|-------|
| `Counter c = dev.counter(id)`; `c.increment(n = 1)`; `c.total()` | The running total since boot. |
| `Counter c = dev.counterPin(id, pin, mode = INPUT_PULLUP, edge = FALLING, debounceUs = 2000)` | Counts edges in an interrupt, so none is missed while the network is busy. |
| `Digital d = dev.digital(id)`; `d.set(bool)` | 0 or 1, on change. |
| `Digital d = dev.digitalPin(id, pin, mode = INPUT_PULLUP, invert = true, debounceMs = 20)` | Reads the pin itself; a button to ground reads 1 while pressed. |
| `Analog a = dev.analog(id)`; `a.set(double)`; `a.deadband(x).minInterval(ms).unit("kg").range(0, 40)` | A number; scaling belongs in the binding. |
| `Encoder e = dev.encoder(id)`; `e.set(int32_t)` | A signed position. |
| `Text t = dev.text(id)`; `t.set("04a1b2c3")` | A string, each time. |
| `SafetyStop s = dev.safetyStop(id, pin = -1, mode = INPUT_PULLUP)`; `s.safe()`; `s.set(bool)` without a pin | A normally-closed stop with a heartbeat every 200 ms. |

Every factory takes an optional `name` as its last argument, shown in the FMS binding editors.

### Outputs

| Call | Receives |
|------|----------|
| `dev.digitalOut(id, pin, activeLow = false)` or `dev.digitalOut(id, [](bool on) {...})` | `{"v":1}` / `{"v":0}` |
| `dev.relay(...)` | The same, declared as a relay. |
| `dev.pwm(id, pin, frequency = 5000)` or `dev.pwm(id, [](float level) {...})` | `{"v":0.35}` (10-bit LEDC on a pin) |
| `LedStrip s = dev.ledStrip(id, pin, pixels, type = NEO_GRB + NEO_KHZ800)`; `s.limit(0.5)`; `s.pixels()` | Every pattern of docs/devices.md, segments, brightness and `at`, drawn by the library from the field clock, exactly as the Lighting page previews them. `limit` caps the brightness for small power supplies. |
| `Display d = dev.display(id, [](const char* text) {...})`; `d.text()`, `d.number()`, `d.isNumber()`, `d.changed()` | `{"text":"1:32"}` or `{"v":92}` |
| `dev.sound(id, [](const char* name) {...})` | `{"play":"buzzer"}` |

### Limits

Set with `build_flags = -DNAME=value`: `GALAXIA_MAX_CHANNELS` (24), `GALAXIA_MQTT_BUFFER` (2048 bytes for the
manifest and each message), `GALAXIA_QUEUE` (8 waiting `at` commands per output), `GALAXIA_MAX_SEGMENTS` (16),
`GALAXIA_TEXT_MAX` (64 characters for text inputs and displays).

## Things to know

- On the original ESP32, GPIO 34 to 39 are inputs without internal pull resistors: the library warns if a pull-up
  is asked for there. Avoid GPIO 6 to 11 (flash) and be careful with strapping pins 0, 2, 12 and 15.
- Channels can be created at global scope, as in the examples; the pins are set up when `connect()` runs.
- Callbacks run inside `dev.loop()`. Keep them short.
- A pending device connects twice in a row: the MQTT library closes the connection when the FMS refuses the output
  subscriptions, and the library then reconnects without asking for them. When you approve the device the FMS
  drops that connection and the next one gets the outputs.
- ArduinoJson keeps short decimals as `float`; the library reads `br`, `v` and `ms` back as the doubles the FMS
  sent (see "LED strips" in [sdk/README.md](../README.md)), so the strip matches the Lighting page's preview.

## Tests

`test/native/` checks the parts that need no board (rendering against the shared cases, JSON reading, the
schedule, the clock estimate) with any C++17 compiler:

```sh
python sdk/arduino/test/native/run_native_tests.py --arduinojson <path to ArduinoJson/src>
```

`python examples/build_all.py --strict` builds every example and the sketches in [examples/](examples/) with
`-Wall -Wextra`.
