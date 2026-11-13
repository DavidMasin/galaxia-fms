// Galaxia FMS device library for ESP32 (Arduino / PlatformIO).
// MIT licence, copyright Galaxia FMS contributors. The protocol is in docs/devices.md; the behaviour shared by all
// Galaxia SDKs is in sdk/README.md.
//
//   #include <Galaxia.h>
//   GalaxiaDevice dev("beam-red-1", "Red goal beam break");
//   Counter balls = dev.counterPin("count", 27);
//   void setup() { dev.wifi("field-devices", "password"); dev.connect("10.0.100.5", "device-token"); }
//   void loop() { dev.loop(); }

#ifndef GALAXIA_H
#define GALAXIA_H

#include <Adafruit_NeoPixel.h>
#include <Arduino.h>
#include <ArduinoJson.h>
#include <Client.h>
#include <IPAddress.h>
#include <MQTT.h>
#include <functional>

#include "GalaxiaLed.h"
#include "GalaxiaSchedule.h"

#define GALAXIA_SDK_VERSION "0.1.0"

#ifndef GALAXIA_MAX_CHANNELS
#define GALAXIA_MAX_CHANNELS 24
#endif
#ifndef GALAXIA_MQTT_BUFFER
#define GALAXIA_MQTT_BUFFER 2048
#endif
#ifndef GALAXIA_QUEUE
#define GALAXIA_QUEUE 8
#endif
#ifndef GALAXIA_TEXT_MAX
#define GALAXIA_TEXT_MAX 64
#endif

class GalaxiaDevice;

// The last field/state message: the match state, phase, time left and scores.
struct FieldState {
  struct Score {
    char alliance[16];
    float points;
  };

  bool valid = false;
  char state[16] = "";  // idle, countdown, running, post, timeout
  char phase[32] = "";  // a phase id of the game, such as "auto"
  float phaseRemaining = NAN;
  float matchRemaining = NAN;
  uint8_t scoreCount = 0;
  Score scores[4];
  uint32_t receivedMs = 0;

  bool running() const { return strcmp(state, "running") == 0; }
  // Seconds left in the phase, counted down locally between messages while the match runs.
  float phaseRemainingNow() const;
  // An alliance's score, or NAN.
  float score(const char* alliance) const;
};

using DigitalHandler = std::function<void(bool on)>;
using PwmHandler = std::function<void(float level)>;
using TextHandler = std::function<void(const char* text)>;
using FieldStateHandler = std::function<void(const FieldState& state)>;
using CommandHandler = std::function<void(const char* action, JsonObjectConst command)>;
using EventHandler = std::function<void()>;

namespace galaxia {
namespace detail {

class Channel {
 public:
  Channel(GalaxiaDevice* device, const char* id, const char* name, const char* type, bool output);
  virtual ~Channel();
  Channel(const Channel&) = delete;
  Channel& operator=(const Channel&) = delete;

  const char* id() const { return id_; }
  bool output() const { return output_; }
  void describe(JsonObject entry) const;
  virtual void begin() {}
  virtual void tick(uint32_t now) { (void)now; }
  // Inputs: send the current value again (after reconnecting, or when it was changed inside a callback).
  virtual void republish() {}
  // Outputs: a command arrived, or the retained output was cleared.
  virtual void receive(JsonObjectConst command) { (void)command; }
  virtual void clear() {}
  virtual void identify(uint32_t now) { (void)now; }
  // Safety channels: 0 or 1 for the heartbeat; -1 for every other channel.
  virtual int safetyState() const { return -1; }

  bool deferred = false;
  char* unit = nullptr;
  bool hasMin = false;
  bool hasMax = false;
  double min = 0;
  double max = 0;

 protected:
  virtual void describeExtra(JsonObject entry) const { (void)entry; }
  GalaxiaDevice* device_;
  char* id_;
  char* name_;
  const char* type_;
  bool output_;
};

class CounterChannel : public Channel {
 public:
  CounterChannel(GalaxiaDevice* device, const char* id, const char* name);
  void begin() override;
  void tick(uint32_t now) override;
  void republish() override;
  void increment(uint32_t n);
  uint32_t total() const { return isrCount_ + manual_; }
  void attachPin(int pin, uint8_t mode, int edge, uint32_t debounceUs);

 private:
  static void IRAM_ATTR isr(void* arg);
  volatile uint32_t isrCount_ = 0;
  volatile uint32_t lastEdgeUs_ = 0;
  uint32_t debounceUs_ = 0;
  uint32_t manual_ = 0;
  uint32_t sent_ = 0;
  bool sentOnce_ = false;
  int pin_ = -1;
  uint8_t mode_ = INPUT_PULLUP;
  int edge_ = FALLING;
};

class DigitalChannel : public Channel {
 public:
  DigitalChannel(GalaxiaDevice* device, const char* id, const char* name);
  void begin() override;
  void tick(uint32_t now) override;
  void republish() override;
  void set(bool value);
  int value() const { return value_; }
  int pin = -1;
  uint8_t mode = INPUT_PULLUP;
  bool invert = false;
  uint16_t debounceMs = 0;

 private:
  void promote(uint32_t now);
  int value_ = -1;
  int candidate_ = -1;
  uint32_t since_ = 0;
};

class SafetyChannel : public Channel {
 public:
  SafetyChannel(GalaxiaDevice* device, const char* id, const char* name);
  void begin() override;
  void tick(uint32_t now) override;
  void republish() override;
  void set(bool safe);
  bool safe() const { return value_ == 1; }
  int safetyState() const override { return value_; }
  int pin = -1;
  uint8_t mode = INPUT_PULLUP;
  bool invert = false;
  uint16_t recoverMs = 50;

 protected:
  void describeExtra(JsonObject entry) const override;

 private:
  void promote(uint32_t now);
  int value_ = 0;
  bool candidate_ = false;
  uint32_t since_ = 0;
};

class AnalogChannel : public Channel {
 public:
  AnalogChannel(GalaxiaDevice* device, const char* id, const char* name, const char* type);
  void tick(uint32_t now) override;
  void republish() override;
  void set(double value);
  double value() const { return value_; }
  double deadband = 0;
  uint32_t minIntervalMs = 100;
  bool integer = false;

 private:
  void send(double value);
  double value_ = 0;
  bool hasValue_ = false;
  double pending_ = 0;
  bool hasPending_ = false;
  uint32_t lastSent_ = 0;
  bool sentOnce_ = false;
};

class TextChannel : public Channel {
 public:
  TextChannel(GalaxiaDevice* device, const char* id, const char* name);
  void republish() override;
  void set(const char* text);
  const char* value() const { return value_; }

 private:
  char value_[GALAXIA_TEXT_MAX] = "";
};

// Outputs keep a schedule of commands waiting for their "at" time.
template <typename T>
class OutputChannel : public Channel {
 public:
  OutputChannel(GalaxiaDevice* device, const char* id, const char* name, const char* type)
      : Channel(device, id, name, type, true) {}
  void receive(JsonObjectConst command) override;
  void tick(uint32_t now) override;
  void clear() override;

 protected:
  virtual bool parse(JsonObjectConst command, T& out) = 0;
  virtual void apply(const T& command, bool hasAt, int64_t at) = 0;
  Schedule<T, GALAXIA_QUEUE> schedule_;
};

struct OnOff {
  bool on = false;
};

class DigitalOutChannel : public OutputChannel<OnOff> {
 public:
  DigitalOutChannel(GalaxiaDevice* device, const char* id, const char* name, bool relay);
  void begin() override;
  bool on() const { return on_; }
  int pin = -1;
  bool activeLow = false;
  DigitalHandler handler;

 protected:
  bool parse(JsonObjectConst command, OnOff& out) override;
  void apply(const OnOff& command, bool hasAt, int64_t at) override;

 private:
  bool on_ = false;
  bool applied_ = false;
};

struct Level {
  float level = 0;
};

class PwmChannel : public OutputChannel<Level> {
 public:
  PwmChannel(GalaxiaDevice* device, const char* id, const char* name);
  void begin() override;
  float level() const { return level_; }
  int pin = -1;
  uint32_t frequency = 5000;
  PwmHandler handler;

 protected:
  bool parse(JsonObjectConst command, Level& out) override;
  void apply(const Level& command, bool hasAt, int64_t at) override;

 private:
  float level_ = -1;
};

struct DisplayCommand {
  char text[GALAXIA_TEXT_MAX] = "";
  double number = 0;
  bool isNumber = false;
};

class DisplayChannel : public OutputChannel<DisplayCommand> {
 public:
  DisplayChannel(GalaxiaDevice* device, const char* id, const char* name);
  const DisplayCommand& current() const { return current_; }
  bool changed = false;
  TextHandler handler;

 protected:
  bool parse(JsonObjectConst command, DisplayCommand& out) override;
  void apply(const DisplayCommand& command, bool hasAt, int64_t at) override;

 private:
  DisplayCommand current_;
  bool applied_ = false;
};

struct SoundCommand {
  char name[32] = "";
};

class SoundChannel : public OutputChannel<SoundCommand> {
 public:
  SoundChannel(GalaxiaDevice* device, const char* id, const char* name);
  TextHandler handler;

 protected:
  bool parse(JsonObjectConst command, SoundCommand& out) override;
  void apply(const SoundCommand& command, bool hasAt, int64_t at) override;
};

class LedStripChannel : public OutputChannel<StripCommand> {
 public:
  LedStripChannel(GalaxiaDevice* device, const char* id, const char* name, int pin, uint16_t pixels,
                  neoPixelType type);
  ~LedStripChannel() override;
  void begin() override;
  void tick(uint32_t now) override;
  void identify(uint32_t now) override;
  Adafruit_NeoPixel& pixels() { return strip_; }
  const StripCommand& current() const { return current_; }
  double limit = 1.0;

 protected:
  void describeExtra(JsonObject entry) const override;
  bool parse(JsonObjectConst command, StripCommand& out) override;
  void apply(const StripCommand& command, bool hasAt, int64_t at) override;

 private:
  void draw(uint32_t now);
  Adafruit_NeoPixel strip_;
  uint16_t count_;
  Rgb* frame_;
  Rgb* next_;
  StripCommand current_;
  uint32_t lastDraw_ = 0;
  bool drawn_ = false;
  uint32_t identifyStart_ = 0;
  uint32_t identifyUntil_ = 0;
  bool identifying_ = false;
};

}  // namespace detail
}  // namespace galaxia

// Channel handles. They are small and can be copied: Counter balls = dev.counter("count");

class Counter {
 public:
  Counter(galaxia::detail::CounterChannel* channel = nullptr) : c_(channel) {}
  // Adds to the running total and sends it.
  void increment(uint32_t n = 1) {
    if (c_) c_->increment(n);
  }
  uint32_t total() const { return c_ ? c_->total() : 0; }

 private:
  galaxia::detail::CounterChannel* c_;
};

class Digital {
 public:
  Digital(galaxia::detail::DigitalChannel* channel = nullptr) : c_(channel) {}
  void set(bool value) {
    if (c_) c_->set(value);
  }
  // 0, 1, or -1 before the first reading.
  int value() const { return c_ ? c_->value() : -1; }

 private:
  galaxia::detail::DigitalChannel* c_;
};

class SafetyStop {
 public:
  SafetyStop(galaxia::detail::SafetyChannel* channel = nullptr) : c_(channel) {}
  // For a stop circuit read by your own code: true while the circuit is closed (safe).
  void set(bool safe) {
    if (c_) c_->set(safe);
  }
  bool safe() const { return c_ && c_->safe(); }

 private:
  galaxia::detail::SafetyChannel* c_;
};

class Analog {
 public:
  Analog(galaxia::detail::AnalogChannel* channel = nullptr) : c_(channel) {}
  void set(double value) {
    if (c_) c_->set(value);
  }
  double value() const { return c_ ? c_->value() : 0; }
  // Changes smaller than this are not sent.
  Analog& deadband(double amount) {
    if (c_) c_->deadband = amount;
    return *this;
  }
  // At most one message per this many milliseconds (the latest value is sent when it is up).
  Analog& minInterval(uint32_t ms) {
    if (c_) c_->minIntervalMs = ms;
    return *this;
  }
  Analog& unit(const char* unit);
  Analog& range(double min, double max);

 private:
  galaxia::detail::AnalogChannel* c_;
};

class Encoder {
 public:
  Encoder(galaxia::detail::AnalogChannel* channel = nullptr) : c_(channel) {}
  void set(int32_t position) {
    if (c_) c_->set(position);
  }
  int32_t value() const { return c_ ? (int32_t)c_->value() : 0; }

 private:
  galaxia::detail::AnalogChannel* c_;
};

class Text {
 public:
  Text(galaxia::detail::TextChannel* channel = nullptr) : c_(channel) {}
  // Sends a string (an RFID tag, a barcode). Every call is sent, even a repeat.
  void set(const char* text) {
    if (c_) c_->set(text);
  }

 private:
  galaxia::detail::TextChannel* c_;
};

class DigitalOut {
 public:
  DigitalOut(galaxia::detail::DigitalOutChannel* channel = nullptr) : c_(channel) {}
  bool on() const { return c_ && c_->on(); }

 private:
  galaxia::detail::DigitalOutChannel* c_;
};

class Pwm {
 public:
  Pwm(galaxia::detail::PwmChannel* channel = nullptr) : c_(channel) {}
  float level() const { return c_ ? (c_->level() < 0 ? 0 : c_->level()) : 0; }
  Pwm& unit(const char* unit);
  Pwm& range(double min, double max);

 private:
  galaxia::detail::PwmChannel* c_;
};

class LedStrip {
 public:
  LedStrip(galaxia::detail::LedStripChannel* channel = nullptr) : c_(channel) {}
  // Caps the brightness (0 to 1), for example to stay within a USB power supply.
  LedStrip& limit(double fraction) {
    if (c_) c_->limit = fraction;
    return *this;
  }
  Adafruit_NeoPixel* pixels() { return c_ ? &c_->pixels() : nullptr; }

 private:
  galaxia::detail::LedStripChannel* c_;
};

class Display {
 public:
  Display(galaxia::detail::DisplayChannel* channel = nullptr) : c_(channel) {}
  // What to show: the text, or a number formatted as text.
  const char* text() const { return c_ ? c_->current().text : ""; }
  double number() const { return c_ ? c_->current().number : 0; }
  bool isNumber() const { return c_ && c_->current().isNumber; }
  // True once after each change, for sketches that poll instead of using a callback.
  bool changed() {
    if (!c_ || !c_->changed) return false;
    c_->changed = false;
    return true;
  }

 private:
  galaxia::detail::DisplayChannel* c_;
};

class Sound {
 public:
  Sound(galaxia::detail::SoundChannel* channel = nullptr) : c_(channel) {}

 private:
  galaxia::detail::SoundChannel* c_;
};

class GalaxiaDevice {
 public:
  // id: lowercase letters, digits and dashes, at most 40 characters. name and firmware are shown on the Devices page.
  explicit GalaxiaDevice(const char* id, const char* name = nullptr, const char* firmware = "1.0.0");
  GalaxiaDevice(const GalaxiaDevice&) = delete;
  GalaxiaDevice& operator=(const GalaxiaDevice&) = delete;

  // Network: either the ESP32's own Wi-Fi, or any Arduino Client (W5500 EthernetClient, or a NetworkClient over the
  // ESP32's own Ethernet). localIp, if given, is reported in the status.
  void wifi(const char* ssid, const char* password);
  void ethernet(Client& client, IPAddress (*localIp)() = nullptr);
  // Where the FMS is. Call once in setup(), after wifi() or ethernet().
  void connect(const char* host, const char* token, uint16_t port = 1883);
  // Call on every pass of loop(). Avoid delay() in your sketch: it holds up the heartbeat and the outputs.
  void loop();
  // Says goodbye to the FMS (status offline) and closes the connection.
  void disconnect();

  // Inputs
  Counter counter(const char* id, const char* name = nullptr);
  // Counts edges on a pin in an interrupt, so no ball is missed even while the network is busy.
  Counter counterPin(const char* id, int pin, uint8_t mode = INPUT_PULLUP, int edge = FALLING,
                     uint32_t debounceUs = 2000, const char* name = nullptr);
  Digital digital(const char* id, const char* name = nullptr);
  // Reads a pin and reports changes. With the defaults (pull-up, inverted) a button to ground reads 1 when pressed.
  Digital digitalPin(const char* id, int pin, uint8_t mode = INPUT_PULLUP, bool invert = true,
                     uint16_t debounceMs = 20, const char* name = nullptr);
  Analog analog(const char* id, const char* name = nullptr);
  Encoder encoder(const char* id, const char* name = nullptr);
  Text text(const char* id, const char* name = nullptr);
  // A normally-closed stop circuit between the pin and ground: closed is safe (1), open (pressed, or a broken wire)
  // is a stop (0). The device then sends a heartbeat every 200 ms. With pin -1, call set(safe) yourself.
  SafetyStop safetyStop(const char* id, int pin = -1, uint8_t mode = INPUT_PULLUP, const char* name = nullptr);

  // Outputs
  DigitalOut digitalOut(const char* id, int pin, bool activeLow = false, const char* name = nullptr);
  DigitalOut digitalOut(const char* id, DigitalHandler onChange, const char* name = nullptr);
  DigitalOut relay(const char* id, int pin, bool activeLow = false, const char* name = nullptr);
  DigitalOut relay(const char* id, DigitalHandler onChange, const char* name = nullptr);
  Pwm pwm(const char* id, int pin, uint32_t frequency = 5000, const char* name = nullptr);
  Pwm pwm(const char* id, PwmHandler onChange, const char* name = nullptr);
  LedStrip ledStrip(const char* id, int pin, uint16_t pixels, neoPixelType type = NEO_GRB + NEO_KHZ800,
                    const char* name = nullptr);
  Display display(const char* id, TextHandler onText = nullptr, const char* name = nullptr);
  Sound sound(const char* id, TextHandler onPlay, const char* name = nullptr);

  // The field
  const FieldState& field() const { return field_; }
  void onFieldState(FieldStateHandler handler) { fieldHandler_ = handler; }
  // FMS time in UTC milliseconds (from field/clock), or 0 before the first message.
  int64_t fieldTimeMs() const;
  bool clockSynced() const { return clock_.valid(); }

  // Commands: identify blinks the identify LED and flashes the strips for five seconds; reboot restarts the board.
  // onCommand receives any other command.
  void onCommand(CommandHandler handler) { commandHandler_ = handler; }
  void onIdentify(EventHandler handler) { identifyHandler_ = handler; }
  void onReboot(EventHandler handler) { rebootHandler_ = handler; }
  void identifyPin(int pin) { identifyPin_ = pin; }

  // State
  bool connected() { return mqtt_.connected(); }
  // True once the FMS accepts this device's output subscriptions, which it does after approval.
  bool approved() const { return approved_; }
  // Why the FMS refused the last connection attempt (MQTT return code): 2 the id is invalid or reserved, 4 wrong
  // token, 5 blocked; 0 if it was not refused. Refused attempts are repeated every 30 s.
  int refusal() const { return refusal_; }

  // Settings
  void setBoard(const char* board) { board_ = board; }
  void setFirmware(const char* firmware) { firmware_ = firmware; }
  // Log messages go to Serial by default; pass nullptr to silence them.
  void logTo(Print* out) { log_ = out; }

  // Used by the channels.
  template <typename T>
  void sendInput(galaxia::detail::Channel* channel, T value) {
    if (!acceptInput(channel)) return;
    JsonDocument doc;
    doc["v"] = value;
    sendInputDocument(channel, doc);
  }
  bool acceptInput(galaxia::detail::Channel* channel);
  void sendInputDocument(galaxia::detail::Channel* channel, JsonDocument& doc);
  void heartbeatNow();
  uint32_t now() const { return millis(); }
  int64_t monotonicMs() const;
  void logf(const char* format, ...);

 private:
  template <typename T>
  T* add(T* channel);
  void beginChannels();
  bool networkUp();
  void tryConnect(uint32_t now);
  void onConnected(uint32_t now);
  void onDisconnected(uint32_t now);
  bool subscribeOutputs(uint32_t now);
  void handleRefusal(uint32_t now);
  void onMessage(char* topic, char* bytes, int length);
  void handleField(const char* name, JsonObjectConst payload);
  void handleCommand(JsonObjectConst payload);
  void identify();
  void reboot();
  void publishJson(const char* suffix, JsonDocument& doc, bool retained);
  bool publishRaw(const char* suffix, const char* payload, size_t length, bool retained);
  void publishManifest();
  void publishStatus();
  void sendHeartbeat();
  void topic(char* out, size_t size, const char* suffix) const;
  void sampleClock(double fieldMs);

  char id_[41];
  const char* name_;
  const char* firmware_;
  const char* board_;
  bool idValid_ = true;
  galaxia::detail::Channel* channels_[GALAXIA_MAX_CHANNELS];
  uint8_t channelCount_ = 0;
  bool safety_ = false;
  bool begun_ = false;

  Client* client_ = nullptr;
  bool useWifi_ = false;
  IPAddress (*localIp_)() = nullptr;
  const char* host_ = nullptr;
  const char* token_ = nullptr;
  uint16_t port_ = 1883;
  MQTTClient mqtt_;
  bool mqttBegun_ = false;
  bool wasConnected_ = false;
  bool approved_ = false;
  bool probe_ = true;
  int refusal_ = 0;
  bool refusedLogged_ = false;
  uint32_t nextAttempt_ = 0;
  uint32_t backoff_ = 1000;
  uint32_t nextStatus_ = 0;
  uint32_t nextHeartbeat_ = 0;
  bool inCallback_ = false;
  bool heartbeatDeferred_ = false;
  bool rebootRequested_ = false;
  uint32_t seq_ = 0;
  uint32_t heartbeatSeq_ = 0;

  galaxia::ClockEstimator clock_;

  FieldState field_;
  FieldStateHandler fieldHandler_;
  CommandHandler commandHandler_;
  EventHandler identifyHandler_;
  EventHandler rebootHandler_;
  int identifyPin_;
  uint32_t identifyUntil_ = 0;
  bool identifying_ = false;
  Print* log_;
};

#endif
