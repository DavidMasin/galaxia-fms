// RFID reader (MFRC522): sends a text for each tag held to it. The FMS sets an enum element to that text, so each
// known tag is mapped here to one of the game's option ids. Unknown tags are sent as their UID: the Devices page
// shows it, ready to copy into the table below.

#include <Galaxia.h>
#include <MFRC522.h>
#include <SPI.h>

GalaxiaDevice dev("rfid-red", "Red dock tag reader");
Text tag = dev.text("tag", "Tag");
MFRC522 reader(5, 22);  // SS on GPIO 5, RST on GPIO 22

struct KnownTag {
  const char* uid;
  const char* option;
};
const KnownTag known[] = {{"04a1b2c3", "low"}, {"04d5e6f7", "high"}};

void setup() {
  Serial.begin(115200);
  SPI.begin();
  reader.PCD_Init();
  dev.wifi("field-devices", "wifi-password");
  dev.connect("10.0.100.5", "device-token");
}

void loop() {
  dev.loop();
  if (!reader.PICC_IsNewCardPresent() || !reader.PICC_ReadCardSerial()) return;
  char uid[21] = "";
  for (byte i = 0; i < reader.uid.size && i < 10; i++) sprintf(uid + 2 * i, "%02x", reader.uid.uidByte[i]);
  const char* text = uid;
  for (const KnownTag& entry : known) {
    if (strcmp(entry.uid, uid) == 0) text = entry.option;
  }
  Serial.printf("tag %s -> %s\n", uid, text);
  tag.set(text);
  reader.PICC_HaltA();  // one read per tap: the tag must leave the field before it is read again
}
