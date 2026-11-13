# Limit switch under a scoring platform, on a Pico W / Pico 2 W.

import galaxia

dev = galaxia.Device("platform-red", "Red station platform")

# Switch COM to ground, NO to GP13: 1 while pressed; a change must hold for 30 ms.
plate = dev.digital_pin("plate", 13, debounce_ms=30)

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
