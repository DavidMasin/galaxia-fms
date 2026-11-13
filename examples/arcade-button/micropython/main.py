# Arcade button that starts the match, on a Pico W / Pico 2 W.

import galaxia

dev = galaxia.Device("arcade-1", "Start match button")

# The button's NO contact between GP14 and ground: 1 while pressed, debounced for 20 ms.
button = dev.digital_pin("button", 14)

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
