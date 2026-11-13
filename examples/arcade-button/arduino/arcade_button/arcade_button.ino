// Arcade button that starts the match. The FMS still checks every start condition first.

#include <Galaxia.h>

GalaxiaDevice dev("arcade-1", "Start match button");

// The button's NO contact between GPIO 13 and ground. Reported as 1 while pressed, debounced for 20 ms.
Digital button = dev.digitalPin("button", 13);

void setup() {
  Serial.begin(115200);
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
