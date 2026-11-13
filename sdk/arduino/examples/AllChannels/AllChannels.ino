// Every kind of channel on one ESP32, as a reference. Each example in examples/ uses one or two of these.

#include <Galaxia.h>

GalaxiaDevice dev("demo-all", "Every channel type", "1.0.0");

// Inputs
Counter balls = dev.counterPin("count", 27);            // beam break between GPIO 27 and ground
Digital button = dev.digitalPin("button", 26);          // button to ground, 1 while pressed
SafetyStop stop = dev.safetyStop("stop", 25);           // normally-closed stop contact to ground
Analog weight = dev.analog("weight", "Weight");         // set from your own code
Encoder turret = dev.encoder("turret");
Text tag = dev.text("tag");

// Outputs
DigitalOut green = dev.relay("green", 16, true);        // active-low relay board on GPIO 16
DigitalOut horn = dev.digitalOut("horn", [](bool on) { Serial.printf("horn %s\n", on ? "on" : "off"); });
Pwm fan = dev.pwm("fan", 17);
LedStrip strip = dev.ledStrip("strip", 4, 60);          // WS2812 data on GPIO 4
Display timer = dev.display("timer", [](const char* text) { Serial.printf("display: %s\n", text); });
Sound buzzer = dev.sound("buzzer", [](const char* name) { Serial.printf("play %s\n", name); });

void setup() {
  Serial.begin(115200);
  weight.unit("kg").range(0, 40).deadband(0.05);
  strip.limit(0.5);
  dev.onFieldState([](const FieldState& field) {
    Serial.printf("%s %s %.1f s, red %.0f\n", field.state, field.phase, (double)field.phaseRemaining,
                  (double)field.score("red"));
  });
  dev.onCommand([](const char* action, JsonObjectConst) { Serial.printf("command %s\n", action); });
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
  weight.set(analogRead(34) * 0.01);
  turret.set((int32_t)(millis() / 1000));
  if (Serial.available()) {
    String line = Serial.readStringUntil('\n');
    tag.set(line.c_str());
  }
}
