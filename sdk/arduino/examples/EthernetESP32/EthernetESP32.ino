// Wired Ethernet with the ESP32's own Ethernet controller, here on a WT32-ETH01 board (LAN8720 PHY). The same
// NetworkClient that Wi-Fi uses runs over Ethernet, passed to dev.ethernet(client).
// For another board, change the ETH.begin() line to match its PHY (see the board's documentation).

#include <ETH.h>
#include <Galaxia.h>

GalaxiaDevice dev("beam-red-1", "Red goal beam break (wired)");
Counter balls = dev.counterPin("count", 4);  // GPIO 4 is free on the WT32-ETH01

NetworkClient net;

IPAddress localIp() { return ETH.localIP(); }

void setup() {
  Serial.begin(115200);
  // WT32-ETH01: LAN8720 at PHY address 1, MDC GPIO 23, MDIO GPIO 18, PHY power GPIO 16, clock in on GPIO 0.
  ETH.begin(ETH_PHY_LAN8720, 1, 23, 18, 16, ETH_CLOCK_GPIO0_IN);
  dev.ethernet(net, localIp);
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
}
