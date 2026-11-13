# WS2812B LED strip on a Pico W / Pico 2 W. The SDK draws every look the FMS sends.

import galaxia

dev = galaxia.Device("goal-red", "Red goal lights")

# 60 LEDs, data on GP0 through a 330 ohm resistor; at most half brightness.
strip = dev.led_strip("strip", 0, 60, "Goal lights", limit=0.5)

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
