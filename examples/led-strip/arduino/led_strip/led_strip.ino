// WS2812B LED strip. The FMS sends looks (solid, blink, pulse, chase, rainbow, progress, segments); the library
// draws them here, in step with the field clock. Set the looks on Setup -> Lighting.

#include <Galaxia.h>

GalaxiaDevice dev("goal-red", "Red goal lights");

// 60 LEDs, data on GPIO 4 (through a 330 ohm resistor). For RGBW strips pass NEO_GRBW + NEO_KHZ800.
LedStrip strip = dev.ledStrip("strip", 4, 60, NEO_GRB + NEO_KHZ800, "Goal lights");

void setup() {
  Serial.begin(115200);
  strip.limit(0.5);  // at most half brightness: 60 LEDs then draw under 2 A at full white
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
