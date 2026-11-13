// Limit switch under a scoring platform: 1 while a robot holds it down.

#include <Galaxia.h>

GalaxiaDevice dev("platform-red", "Red station platform");

// Switch COM to ground, NO to GPIO 26. Pressed reads 1. A change must hold for 30 ms to count, which rides out
// contact bounce and a robot rocking on the plate.
Digital plate = dev.digitalPin("plate", 26, INPUT_PULLUP, true, 30);

void setup() {
  Serial.begin(115200);
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
