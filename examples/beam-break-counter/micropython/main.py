# Beam-break ball counter on a Raspberry Pi Pico W / Pico 2 W.
# Copy galaxia.py and mqtt_as to the board first (sdk/micropython/README.md), then this file as main.py.

import galaxia

dev = galaxia.Device("beam-red-1", "Red goal beam break")

# The receiver's signal wire on GP15; each ball pulls it to ground once. Counted in an interrupt.
balls = dev.counter_pin("count", 15, pull="up", edge="falling")

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
