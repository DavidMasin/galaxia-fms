// Beam-break ball counter: every ball that breaks the beam adds to the alliance's score.
// Wiring and the binding to make are in the README next to this folder.

#include <Galaxia.h>

GalaxiaDevice dev("beam-red-1", "Red goal beam break");

// The receiver's signal wire on GPIO 27. It pulls the pin to ground while the beam is broken, so each ball is one
// falling edge. Edges are counted in an interrupt: no ball is missed, even while the Wi-Fi reconnects.
Counter balls = dev.counterPin("count", 27, INPUT_PULLUP, FALLING);

void setup() {
  Serial.begin(115200);
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
