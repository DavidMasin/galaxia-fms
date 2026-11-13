// Wireless E-stop box for one driver station.
//
// WARNING: a wireless E-stop must never be the only E-stop path. The driver station E-stops and the wired field
// stops remain the primary stops; this box is a convenience on top of them. It is not a safety-rated device.
//
// The FMS stops the robot (and latches a fault) if the button is pressed, if a wire breaks, if this board stops
// sending its heartbeat for one second, or if it disconnects.

#include <Galaxia.h>

GalaxiaDevice dev("estop-box-red", "Red 1 E-stop box");

// The E-stop's normally-closed contact between GPIO 25 and ground: closed is safe (1), open is stop (0).
// The library sends the state at once on every change and as a heartbeat every 200 ms.
SafetyStop stop = dev.safetyStop("stop", 25, INPUT_PULLUP, "E-stop");

void setup() {
  Serial.begin(115200);
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
