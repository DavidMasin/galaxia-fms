# Wireless E-stop box on a Pico W / Pico 2 W.
#
# WARNING: a wireless E-stop must never be the only E-stop path. The driver station E-stops and the wired field
# stops remain the primary stops; this box is a convenience on top of them. It is not a safety-rated device.

import galaxia

dev = galaxia.Device("estop-box-red", "Red 1 E-stop box")

# The normally-closed contact between GP16 and ground: closed is safe, open (pressed or a cut wire) is stop.
stop = dev.safety_stop("stop", 16, "E-stop")

dev.wifi("field-devices", "wifi-password")
dev.connect("10.0.100.5", "device-token")
dev.run()
