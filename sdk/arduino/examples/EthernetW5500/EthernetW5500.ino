// Wired Ethernet through a W5500 module (Arduino Ethernet library). Any Arduino Client works the same way: the
// library only needs dev.ethernet(client) instead of dev.wifi(...).
//
// W5500 wiring: SCK GPIO 18, MISO GPIO 19, MOSI GPIO 23, CS GPIO 5, 3.3 V and GND.

#include <Ethernet.h>
#include <Galaxia.h>
#include <SPI.h>

GalaxiaDevice dev("beam-red-1", "Red goal beam break (wired)");
Counter balls = dev.counterPin("count", 27);

EthernetClient net;
// A locally administered MAC address. Give every board on the field a different one.
byte mac[] = {0x02, 0x47, 0x41, 0x4c, 0x00, 0x01};

IPAddress localIp() { return Ethernet.localIP(); }

void setup() {
  Serial.begin(115200);
  Ethernet.init(5);
  if (Ethernet.begin(mac, 10000) == 0) {
    Serial.println("No DHCP answer; using 10.0.100.61");
    Ethernet.begin(mac, IPAddress(10, 0, 100, 61));
  }
  dev.ethernet(net, localIp);
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  Ethernet.maintain();
  dev.loop();
}
