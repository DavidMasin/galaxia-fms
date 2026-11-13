# Stack light on a 4-channel relay board, on a Pico W / Pico 2 W.

import galaxia

dev = galaxia.Device("stack-light", "Field stack light")

# Relay board inputs are usually active low.
dev.relay("red", 16, "Red lamp", active_low=True)
dev.relay("amber", 17, "Amber lamp", active_low=True)
dev.relay("green", 18, "Green lamp", active_low=True)
dev.relay("buzzer", 19, "Buzzer", active_low=True)

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
