// Stack light (red, amber, green and a buzzer) switched by a 4-channel relay board.
// What each lamp means is set in the FMS (Setup -> Lighting, or output bindings), not here.

#include <Galaxia.h>

GalaxiaDevice dev("stack-light", "Field stack light");

// Most relay boards switch on when their input is pulled low, hence activeLow = true.
DigitalOut red = dev.relay("red", 16, true, "Red lamp");
DigitalOut amber = dev.relay("amber", 17, true, "Amber lamp");
DigitalOut green = dev.relay("green", 18, true, "Green lamp");
DigitalOut buzzer = dev.relay("buzzer", 19, true, "Buzzer");

void setup() {
  Serial.begin(115200);
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
