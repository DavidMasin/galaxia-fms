// Load cell (scale) through an HX711. The device sends raw readings; the binding turns them into kilograms with a
// scale and an offset, so calibration happens in the FMS and the firmware never changes.

#include <Galaxia.h>
#include <HX711.h>

GalaxiaDevice dev("load-cell", "Blue payload scale");
Analog weight = dev.analog("weight", "Weight (raw HX711 counts)");
HX711 scale;
double smoothed = 0;

void setup() {
  Serial.begin(115200);
  scale.begin(32, 33);  // DT on GPIO 32, SCK on GPIO 33
  weight.deadband(50).minInterval(200);  // skip tiny changes; at most five readings a second
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
  if (scale.is_ready()) {  // a new reading about ten times a second; never wait for one
    long raw = scale.read();
    smoothed = smoothed == 0 ? raw : smoothed + (raw - smoothed) * 0.3;
    weight.set(round(smoothed));
  }
}
