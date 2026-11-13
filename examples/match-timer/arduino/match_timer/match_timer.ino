// Match timer on a MAX7219 LED matrix: the time left in the current phase, from field/state.
// Needs no binding: every device receives the field state.

#include <Galaxia.h>
#include <MD_Parola.h>
#include <SPI.h>

GalaxiaDevice dev("timer-1", "Match timer");
MD_Parola matrix(MD_MAX72XX::FC16_HW, 5, 4);  // FC-16 modules, CS on GPIO 5, four 8x8 blocks

void setup() {
  Serial.begin(115200);
  matrix.begin();
  matrix.setIntensity(4);  // 0 to 15
  matrix.setTextAlignment(PA_CENTER);
  matrix.print("--:--");
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
  static char shown[16] = "";
  char text[16] = "--:--";
  float left = dev.field().phaseRemainingNow();  // counts down smoothly between the FMS's updates
  if (dev.field().valid && !isnan(left)) {
    int seconds = (int)ceilf(left);
    snprintf(text, sizeof(text), "%d:%02d", seconds / 60, seconds % 60);
  }
  if (strcmp(text, shown) != 0) {
    matrix.print(text);
    strcpy(shown, text);
  }
}
