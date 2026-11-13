# Milestone 6: device SDKs, examples and the first-sensor tutorial

Finished 2026-11-13. The SDKs are MIT licensed, so teams can reuse them in their own robots and fixtures; the FMS
itself stays under Team 254's licence.

## What works

- **Three SDKs** speak the protocol in [docs/devices.md](../devices.md):
  - **`sdk/arduino`**, for the ESP32 over Wi-Fi or Ethernet, as a PlatformIO library. It is built on
    arduino-mqtt 2.5.3, ArduinoJson 7.4.3 and Adafruit NeoPixel 1.15.5.
  - **`sdk/micropython`**, for the Pico W and Pico 2 W, on mqtt_as 0.8.5 and MicroPython 1.29.
  - **`sdk/python`**, the `galaxia-device` package for a Raspberry Pi or a vision program, on paho-mqtt 2.2.

  Each handles approval, reconnection, heartbeats for safety channels, counters that survive restarts, and
  outputs.
- **LED strips draw like the FMS.** Strips draw every pattern exactly as the Lighting page and the virtual
  devices draw it, from the shared field clock, so strips on different boards stay in step.
- **Nine examples**, each with a wiring diagram, a parts list and the binding to make:
  - a beam-break counter, an arcade button and a limit switch;
  - a stack light and an LED strip;
  - an E-stop box and a match timer;
  - a load cell and an RFID reader.
- **[The first-sensor tutorial](../tutorial-first-sensor.md)** goes from an ESP32 and a beam-break sensor to a
  counting score. It also shows how to try the same thing with no hardware.

## Verification

- **SDK tests**, all passing (run again on 2026-11-13 at 19:30):
  - Python SDK: 85 tests, including 3 with the real paho-mqtt over a local socket.
  - MicroPython SDK: 15 tests, run under CPython with stand-ins for the hardware.
  - Arduino library: 151 checks on a PC.
- **LED drawing** matches `static/js/led_patterns.js` on all 87 shared reference cases in every SDK.
- **Example builds.** All 12 sketches (the examples plus the library's own) build for the ESP32 with strict
  warnings, using `examples/build_all.py --strict`.
- **CI.** The CI workflow now has a job that runs all three SDK test suites and builds every example. It has not
  run yet, because GitHub Actions is locked on this account by a billing issue that only David can clear.

## Known limits

- **Not tried on hardware.** Nothing has run on a real ESP32 or Pico, or against the FMS's own broker over a real
  network. That is the first thing to do with a board in hand. Until then, the hardware side is checked only by
  the stand-ins and the builds above.
- **CI has never run** the new job (see above).
- **Protocol details settled after the SDKs were written.** `seq`, `ts`, `up` and several future `at` times are
  now written into the spec. The text describes what all three SDKs already do, except for one difference that
  is documented rather than changed: the Python SDK keeps 32 pending commands per channel, while Arduino and
  MicroPython keep 8.
