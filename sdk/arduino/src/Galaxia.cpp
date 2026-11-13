// MIT licence, copyright Galaxia FMS contributors.

#include "Galaxia.h"

#include <WiFi.h>

#include "GalaxiaJson.h"
#include <esp_timer.h>
#include <stdarg.h>

using galaxia::Rgb;
using galaxia::StripCommand;
using namespace galaxia::detail;

namespace {

const char kPrefix[] = "galaxia/v1/";
const char kOffline[] = "{\"online\":false}";
const uint16_t kKeepAliveS = 15;
const uint32_t kStatusMs = 5000;
const uint32_t kHeartbeatMs = 200;
const uint32_t kRefusedRetryMs = 30000;
const uint32_t kMaxBackoffMs = 30000;
const uint32_t kIdentifyMs = 5000;
const uint32_t kRenderMs = 20;

char* copyString(const char* text) {
  if (text == nullptr) return nullptr;
  size_t length = strlen(text) + 1;
  char* copy = static_cast<char*>(malloc(length));
  if (copy != nullptr) memcpy(copy, text, length);
  return copy;
}

bool validId(const char* text, char extra, size_t limit) {
  size_t length = text ? strlen(text) : 0;
  if (length == 0 || length > limit) return false;
  for (size_t i = 0; i < length; i++) {
    char c = text[i];
    if (!((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == extra)) return false;
  }
  return true;
}

bool after(uint32_t now, uint32_t when) { return (int32_t)(now - when) >= 0; }

#if CONFIG_IDF_TARGET_ESP32
// GPIO 34 to 39 on the original ESP32 are inputs without internal pull resistors.
bool inputOnlyWithoutPull(int pin) { return pin >= 34 && pin <= 39; }
#else
bool inputOnlyWithoutPull(int) { return false; }
#endif

}  // namespace

// FieldState

float FieldState::phaseRemainingNow() const {
  if (!valid || isnan(phaseRemaining)) return phaseRemaining;
  if (!running()) return phaseRemaining;
  float left = phaseRemaining - (millis() - receivedMs) / 1000.0f;
  return left > 0 ? left : 0;
}

float FieldState::score(const char* alliance) const {
  for (uint8_t i = 0; i < scoreCount; i++) {
    if (strcmp(scores[i].alliance, alliance) == 0) return scores[i].points;
  }
  return NAN;
}

// Channel

Channel::Channel(GalaxiaDevice* device, const char* id, const char* name, const char* type, bool output)
    : device_(device), id_(copyString(id)), name_(copyString(name)), type_(type), output_(output) {}

Channel::~Channel() {
  free(id_);
  free(name_);
  free(unit);
}

void Channel::describe(JsonObject entry) const {
  entry["id"] = id_;
  entry["dir"] = output_ ? "out" : "in";
  entry["type"] = type_;
  if (name_ != nullptr && name_[0] != '\0') entry["name"] = name_;
  if (unit != nullptr) entry["unit"] = unit;
  if (hasMin) entry["min"] = min;
  if (hasMax) entry["max"] = max;
  describeExtra(entry);
}

// Counter

CounterChannel::CounterChannel(GalaxiaDevice* device, const char* id, const char* name)
    : Channel(device, id, name, "counter", false) {}

void CounterChannel::attachPin(int pin, uint8_t mode, int edge, uint32_t debounceUs) {
  pin_ = pin;
  mode_ = mode;
  edge_ = edge;
  debounceUs_ = debounceUs;
}

void IRAM_ATTR CounterChannel::isr(void* arg) {
  CounterChannel* self = static_cast<CounterChannel*>(arg);
  uint32_t now = micros();
  if (now - self->lastEdgeUs_ >= self->debounceUs_) self->isrCount_ = self->isrCount_ + 1;
  self->lastEdgeUs_ = now;
}

void CounterChannel::begin() {
  if (pin_ < 0) return;
  if (mode_ != INPUT && inputOnlyWithoutPull(pin_)) {
    device_->logf("galaxia: GPIO %d has no internal pull resistor; add an external one", pin_);
  }
  pinMode(pin_, mode_);
  lastEdgeUs_ = micros();
  attachInterruptArg(digitalPinToInterrupt(pin_), isr, this, edge_);
}

void CounterChannel::increment(uint32_t n) {
  manual_ += n;
  tick(0);
}

void CounterChannel::tick(uint32_t) {
  uint32_t current = total();
  if (sentOnce_ && current == sent_) return;
  sent_ = current;
  sentOnce_ = true;
  device_->sendInput(this, current);
}

void CounterChannel::republish() { device_->sendInput(this, total()); }

// Digital

DigitalChannel::DigitalChannel(GalaxiaDevice* device, const char* id, const char* name)
    : Channel(device, id, name, "digital", false) {}

void DigitalChannel::begin() {
  if (pin < 0) return;
  if (mode != INPUT && inputOnlyWithoutPull(pin)) {
    device_->logf("galaxia: GPIO %d has no internal pull resistor; add an external one", pin);
  }
  pinMode(pin, mode);
}

void DigitalChannel::set(bool raw) {
  int state = (raw ? 1 : 0) ^ (invert ? 1 : 0);
  if (debounceMs == 0) {
    if (state != value_) {
      value_ = state;
      device_->sendInput(this, value_);
    }
    return;
  }
  uint32_t now = millis();
  if (state != candidate_) {
    candidate_ = state;
    since_ = now;
  }
  promote(now);
}

void DigitalChannel::promote(uint32_t now) {
  if (candidate_ < 0 || candidate_ == value_) return;
  if (now - since_ >= debounceMs) {
    value_ = candidate_;
    device_->sendInput(this, value_);
  }
}

void DigitalChannel::tick(uint32_t now) {
  if (pin >= 0) {
    set(digitalRead(pin) == HIGH);
  } else {
    promote(now);
  }
}

void DigitalChannel::republish() {
  if (value_ >= 0) device_->sendInput(this, value_);
}

// Safety stop

SafetyChannel::SafetyChannel(GalaxiaDevice* device, const char* id, const char* name)
    : Channel(device, id, name, "digital", false) {}

void SafetyChannel::describeExtra(JsonObject entry) const { entry["safety"] = true; }

void SafetyChannel::begin() {
  if (pin < 0) return;
  if (mode != INPUT && inputOnlyWithoutPull(pin)) {
    device_->logf("galaxia: GPIO %d has no internal pull resistor; add an external one", pin);
  }
  pinMode(pin, mode);
}

void SafetyChannel::set(bool safe) {
  uint32_t now = millis();
  if (!safe) {
    candidate_ = false;
    if (value_ != 0) {
      value_ = 0;
      device_->sendInput(this, 0);
      device_->heartbeatNow();
    }
    return;
  }
  if (value_ == 1) {
    candidate_ = false;
    return;
  }
  if (!candidate_) {
    candidate_ = true;
    since_ = now;
  }
  promote(now);
}

void SafetyChannel::promote(uint32_t now) {
  if (candidate_ && now - since_ >= recoverMs) {
    candidate_ = false;
    value_ = 1;
    device_->sendInput(this, 1);
    device_->heartbeatNow();
  }
}

void SafetyChannel::tick(uint32_t now) {
  if (pin >= 0) {
    // With the pull-up, a closed contact to ground reads LOW: that is safe.
    bool closed = digitalRead(pin) == LOW;
    set(closed != invert);
  } else {
    promote(now);
  }
}

void SafetyChannel::republish() { device_->sendInput(this, value_); }

// Analog and encoder

AnalogChannel::AnalogChannel(GalaxiaDevice* device, const char* id, const char* name, const char* type)
    : Channel(device, id, name, type, false) {}

void AnalogChannel::set(double value) {
  if (isnan(value)) return;
  pending_ = integer ? (double)(int32_t)value : value;
  hasPending_ = true;
  tick(millis());
}

void AnalogChannel::tick(uint32_t now) {
  if (!hasPending_) return;
  if (hasValue_) {
    double change = fabs(pending_ - value_);
    bool changed = deadband > 0 ? change > deadband : pending_ != value_;
    if (!changed) {
      hasPending_ = false;
      return;
    }
  }
  if (sentOnce_ && now - lastSent_ < minIntervalMs) return;
  hasPending_ = false;
  lastSent_ = now;
  sentOnce_ = true;
  send(pending_);
}

void AnalogChannel::send(double value) {
  value_ = value;
  hasValue_ = true;
  if (integer) {
    device_->sendInput(this, (int32_t)value);
  } else {
    device_->sendInput(this, value);
  }
}

void AnalogChannel::republish() {
  if (hasValue_) send(value_);
}

// Text

TextChannel::TextChannel(GalaxiaDevice* device, const char* id, const char* name)
    : Channel(device, id, name, "text", false) {}

void TextChannel::set(const char* text) {
  strlcpy(value_, text ? text : "", sizeof(value_));
  device_->sendInput(this, (const char*)value_);
}

void TextChannel::republish() {
  // Text is an event (a tag read), not a state, so it is not sent again after reconnecting. A text set inside a
  // callback is still delivered, once.
  if (deferred) device_->sendInput(this, (const char*)value_);
}

// Outputs

template <typename T>
void OutputChannel<T>::receive(JsonObjectConst command) {
  T parsed;
  if (!parse(command, parsed)) return;
  JsonVariantConst at = command["at"];
  bool hasAt = at.is<double>();
  int64_t when = hasAt ? (int64_t)at.as<double>() : 0;
  if (schedule_.offer(hasAt, when, device_->clockSynced(), device_->fieldTimeMs(), parsed)) {
    apply(parsed, hasAt, when);
  }
}

template <typename T>
void OutputChannel<T>::tick(uint32_t) {
  T due;
  int64_t at;
  while (schedule_.popDue(device_->clockSynced(), device_->fieldTimeMs(), due, at)) apply(due, true, at);
}

template <typename T>
void OutputChannel<T>::clear() {
  schedule_.clear();
  T off;
  apply(off, false, 0);
}

DigitalOutChannel::DigitalOutChannel(GalaxiaDevice* device, const char* id, const char* name, bool relay)
    : OutputChannel<OnOff>(device, id, name, relay ? "relay" : "digital") {}

void DigitalOutChannel::begin() {
  if (pin < 0) return;
  digitalWrite(pin, activeLow ? HIGH : LOW);
  pinMode(pin, OUTPUT);
  digitalWrite(pin, activeLow ? HIGH : LOW);
}

bool DigitalOutChannel::parse(JsonObjectConst command, OnOff& out) {
  JsonVariantConst v = command["v"];
  out.on = v.is<double>() ? v.as<double>() != 0 : v.as<bool>();
  return true;
}

void DigitalOutChannel::apply(const OnOff& command, bool, int64_t) {
  if (applied_ && command.on == on_) return;
  applied_ = true;
  on_ = command.on;
  if (pin >= 0) digitalWrite(pin, (on_ != activeLow) ? HIGH : LOW);
  if (handler) handler(on_);
}

PwmChannel::PwmChannel(GalaxiaDevice* device, const char* id, const char* name)
    : OutputChannel<Level>(device, id, name, "pwm") {}

void PwmChannel::begin() {
  if (pin < 0) return;
  if (!ledcAttach(pin, frequency, 10)) device_->logf("galaxia: cannot use GPIO %d for PWM", pin);
  ledcWrite(pin, 0);
}

bool PwmChannel::parse(JsonObjectConst command, Level& out) {
  double level = galaxia::exactNumber(command["v"], 0);
  out.level = (float)(level > 0 ? (level > 1 ? 1 : level) : 0);
  return true;
}

void PwmChannel::apply(const Level& command, bool, int64_t) {
  if (command.level == level_) return;
  level_ = command.level;
  if (pin >= 0) ledcWrite(pin, (uint32_t)(level_ * 1023 + 0.5f));
  if (handler) handler(level_);
}

DisplayChannel::DisplayChannel(GalaxiaDevice* device, const char* id, const char* name)
    : OutputChannel<DisplayCommand>(device, id, name, "display") {}

bool DisplayChannel::parse(JsonObjectConst command, DisplayCommand& out) {
  JsonVariantConst text = command["text"];
  if (!text.isNull()) {
    strlcpy(out.text, text.is<const char*>() ? text.as<const char*>() : "", sizeof(out.text));
    out.isNumber = false;
    return true;
  }
  JsonVariantConst v = command["v"];
  if (!v.is<double>()) return true;  // nothing to show: blank
  out.number = v.as<double>();
  out.isNumber = true;
  snprintf(out.text, sizeof(out.text), "%g", out.number);
  return true;
}

void DisplayChannel::apply(const DisplayCommand& command, bool, int64_t) {
  if (applied_ && command.isNumber == current_.isNumber && strcmp(command.text, current_.text) == 0) return;
  applied_ = true;
  current_ = command;
  changed = true;
  if (handler) handler(current_.text);
}

SoundChannel::SoundChannel(GalaxiaDevice* device, const char* id, const char* name)
    : OutputChannel<SoundCommand>(device, id, name, "sound") {}

bool SoundChannel::parse(JsonObjectConst command, SoundCommand& out) {
  const char* name = command["play"];
  if (name == nullptr) return false;
  strlcpy(out.name, name, sizeof(out.name));
  return true;
}

void SoundChannel::apply(const SoundCommand& command, bool, int64_t) {
  if (command.name[0] != '\0' && handler) handler(command.name);
}

// LED strip

LedStripChannel::LedStripChannel(GalaxiaDevice* device, const char* id, const char* name, int pin, uint16_t pixels,
                                 neoPixelType type)
    : OutputChannel<StripCommand>(device, id, name, "led_strip"),
      strip_(pixels, pin, type),
      count_(pixels),
      frame_(new Rgb[pixels]()),
      next_(new Rgb[pixels]()) {}

LedStripChannel::~LedStripChannel() {
  delete[] frame_;
  delete[] next_;
}

void LedStripChannel::describeExtra(JsonObject entry) const { entry["pixels"] = count_; }

void LedStripChannel::begin() {
  strip_.begin();
  strip_.clear();
  strip_.show();
}

bool LedStripChannel::parse(JsonObjectConst command, StripCommand& out) {
  int dropped = galaxia::parseStripCommand(command, out);
  if (dropped > 0) {
    device_->logf("galaxia: %s: %d segments over the limit of %d were ignored", id_, dropped, GALAXIA_MAX_SEGMENTS);
  }
  return true;
}

void LedStripChannel::apply(const StripCommand& command, bool, int64_t) {
  // Patterns run on the field clock itself, so strips on different devices stay in step; "at" only says when the
  // command starts to show.
  current_ = command;
  draw(millis());
}

void LedStripChannel::identify(uint32_t now) {
  identifying_ = true;
  identifyStart_ = now;
  identifyUntil_ = now + kIdentifyMs;
  draw(now);
}

void LedStripChannel::draw(uint32_t now) {
  lastDraw_ = now;
  if (identifying_ && !after(now, identifyUntil_)) {
    bool lit = ((now - identifyStart_) / 250) % 2 == 0;
    Rgb color = lit ? Rgb{128, 128, 128} : Rgb{0, 0, 0};
    for (uint16_t i = 0; i < count_; i++) next_[i] = color;
  } else {
    identifying_ = false;
    // Until the first field/clock message the board's own clock stands in, so animations still move.
    int64_t t = device_->clockSynced() ? device_->fieldTimeMs() : device_->monotonicMs();
    galaxia::render(current_, next_, count_, (double)t, limit);
  }
  if (drawn_ && memcmp(next_, frame_, sizeof(Rgb) * count_) == 0) return;
  drawn_ = true;
  memcpy(frame_, next_, sizeof(Rgb) * count_);
  for (uint16_t i = 0; i < count_; i++) strip_.setPixelColor(i, frame_[i].r, frame_[i].g, frame_[i].b);
  strip_.show();
}

void LedStripChannel::tick(uint32_t now) {
  OutputChannel<StripCommand>::tick(now);
  if ((current_.animated() || identifying_) && now - lastDraw_ >= kRenderMs) draw(now);
}

// Handles

Analog& Analog::unit(const char* text) {
  if (c_) {
    free(c_->unit);
    c_->unit = copyString(text);
  }
  return *this;
}

Analog& Analog::range(double low, double high) {
  if (c_) {
    c_->hasMin = c_->hasMax = true;
    c_->min = low;
    c_->max = high;
  }
  return *this;
}

Pwm& Pwm::unit(const char* text) {
  if (c_) {
    free(c_->unit);
    c_->unit = copyString(text);
  }
  return *this;
}

Pwm& Pwm::range(double low, double high) {
  if (c_) {
    c_->hasMin = c_->hasMax = true;
    c_->min = low;
    c_->max = high;
  }
  return *this;
}

// The device

GalaxiaDevice::GalaxiaDevice(const char* id, const char* name, const char* firmware)
    : name_(name), firmware_(firmware), board_(ARDUINO_BOARD), mqtt_(GALAXIA_MQTT_BUFFER), log_(&Serial) {
  idValid_ = validId(id, '-', 40) && strcmp(id, "plc") != 0 && strcmp(id, "inline") != 0;
  strlcpy(id_, id ? id : "", sizeof(id_));
#ifdef LED_BUILTIN
  identifyPin_ = LED_BUILTIN;
#else
  identifyPin_ = -1;
#endif
}

void GalaxiaDevice::logf(const char* format, ...) {
  if (log_ == nullptr) return;
  char line[160];
  va_list args;
  va_start(args, format);
  vsnprintf(line, sizeof(line), format, args);
  va_end(args);
  log_->println(line);
}

template <typename T>
T* GalaxiaDevice::add(T* channel) {
  if (!validId(channel->id(), '_', 32)) {
    logf("galaxia: channel id \"%s\" must be lowercase letters, digits and underscores", channel->id());
  }
  for (uint8_t i = 0; i < channelCount_; i++) {
    if (strcmp(channels_[i]->id(), channel->id()) == 0) logf("galaxia: channel \"%s\" exists twice", channel->id());
  }
  if (channelCount_ >= GALAXIA_MAX_CHANNELS) {
    logf("galaxia: too many channels (GALAXIA_MAX_CHANNELS is %d)", GALAXIA_MAX_CHANNELS);
    delete channel;
    return nullptr;
  }
  channels_[channelCount_++] = channel;
  if (begun_) channel->begin();
  return channel;
}

Counter GalaxiaDevice::counter(const char* id, const char* name) {
  return Counter(add(new CounterChannel(this, id, name)));
}

Counter GalaxiaDevice::counterPin(const char* id, int pin, uint8_t mode, int edge, uint32_t debounceUs,
                                  const char* name) {
  CounterChannel* channel = new CounterChannel(this, id, name);
  channel->attachPin(pin, mode, edge, debounceUs);
  return Counter(add(channel));
}

Digital GalaxiaDevice::digital(const char* id, const char* name) {
  return Digital(add(new DigitalChannel(this, id, name)));
}

Digital GalaxiaDevice::digitalPin(const char* id, int pin, uint8_t mode, bool invert, uint16_t debounceMs,
                                  const char* name) {
  DigitalChannel* channel = new DigitalChannel(this, id, name);
  channel->pin = pin;
  channel->mode = mode;
  channel->invert = invert;
  channel->debounceMs = debounceMs;
  return Digital(add(channel));
}

Analog GalaxiaDevice::analog(const char* id, const char* name) {
  return Analog(add(new AnalogChannel(this, id, name, "analog")));
}

Encoder GalaxiaDevice::encoder(const char* id, const char* name) {
  AnalogChannel* channel = new AnalogChannel(this, id, name, "encoder");
  channel->integer = true;
  channel->minIntervalMs = 50;
  return Encoder(add(channel));
}

Text GalaxiaDevice::text(const char* id, const char* name) { return Text(add(new TextChannel(this, id, name))); }

SafetyStop GalaxiaDevice::safetyStop(const char* id, int pin, uint8_t mode, const char* name) {
  SafetyChannel* channel = new SafetyChannel(this, id, name);
  channel->pin = pin;
  channel->mode = mode;
  safety_ = true;
  return SafetyStop(add(channel));
}

DigitalOut GalaxiaDevice::digitalOut(const char* id, int pin, bool activeLow, const char* name) {
  DigitalOutChannel* channel = new DigitalOutChannel(this, id, name, false);
  channel->pin = pin;
  channel->activeLow = activeLow;
  return DigitalOut(add(channel));
}

DigitalOut GalaxiaDevice::digitalOut(const char* id, DigitalHandler onChange, const char* name) {
  DigitalOutChannel* channel = new DigitalOutChannel(this, id, name, false);
  channel->handler = onChange;
  return DigitalOut(add(channel));
}

DigitalOut GalaxiaDevice::relay(const char* id, int pin, bool activeLow, const char* name) {
  DigitalOutChannel* channel = new DigitalOutChannel(this, id, name, true);
  channel->pin = pin;
  channel->activeLow = activeLow;
  return DigitalOut(add(channel));
}

DigitalOut GalaxiaDevice::relay(const char* id, DigitalHandler onChange, const char* name) {
  DigitalOutChannel* channel = new DigitalOutChannel(this, id, name, true);
  channel->handler = onChange;
  return DigitalOut(add(channel));
}

Pwm GalaxiaDevice::pwm(const char* id, int pin, uint32_t frequency, const char* name) {
  PwmChannel* channel = new PwmChannel(this, id, name);
  channel->pin = pin;
  channel->frequency = frequency;
  return Pwm(add(channel));
}

Pwm GalaxiaDevice::pwm(const char* id, PwmHandler onChange, const char* name) {
  PwmChannel* channel = new PwmChannel(this, id, name);
  channel->handler = onChange;
  return Pwm(add(channel));
}

LedStrip GalaxiaDevice::ledStrip(const char* id, int pin, uint16_t pixels, neoPixelType type, const char* name) {
  if (pixels == 0) {
    logf("galaxia: LED strip \"%s\" needs at least one pixel", id);
    pixels = 1;
  }
  return LedStrip(add(new LedStripChannel(this, id, name, pin, pixels, type)));
}

Display GalaxiaDevice::display(const char* id, TextHandler onText, const char* name) {
  DisplayChannel* channel = new DisplayChannel(this, id, name);
  channel->handler = onText;
  return Display(add(channel));
}

Sound GalaxiaDevice::sound(const char* id, TextHandler onPlay, const char* name) {
  SoundChannel* channel = new SoundChannel(this, id, name);
  channel->handler = onPlay;
  return Sound(add(channel));
}

// Network

void GalaxiaDevice::wifi(const char* ssid, const char* password) {
  NetworkClient* client = new NetworkClient();
  client->setConnectionTimeout(2000);
  client_ = client;
  useWifi_ = true;
  WiFi.mode(WIFI_STA);
  // Modem sleep adds up to a few hundred milliseconds of latency, too much for a stop heartbeat.
  WiFi.setSleep(false);
  WiFi.setAutoReconnect(true);
  WiFi.begin(ssid, password);
}

void GalaxiaDevice::ethernet(Client& client, IPAddress (*localIp)()) {
  client_ = &client;
  useWifi_ = false;
  localIp_ = localIp;
}

void GalaxiaDevice::connect(const char* host, const char* token, uint16_t port) {
  if (log_ == &Serial && !Serial) Serial.begin(115200);
  if (!idValid_) {
    logf("galaxia: device id \"%s\" must be 1 to 40 lowercase letters, digits and dashes, and not plc or inline",
         id_);
  }
  host_ = host;
  token_ = token;
  port_ = port;
  if (client_ == nullptr) logf("galaxia: call dev.wifi(...) or dev.ethernet(...) before dev.connect(...)");
  beginChannels();
}

void GalaxiaDevice::beginChannels() {
  if (begun_) return;
  begun_ = true;
  if (identifyPin_ >= 0) pinMode(identifyPin_, OUTPUT);
  for (uint8_t i = 0; i < channelCount_; i++) channels_[i]->begin();
}

bool GalaxiaDevice::networkUp() {
  if (client_ == nullptr || host_ == nullptr) return false;
  return !useWifi_ || WiFi.status() == WL_CONNECTED;
}

int64_t GalaxiaDevice::monotonicMs() const { return esp_timer_get_time() / 1000; }

int64_t GalaxiaDevice::fieldTimeMs() const { return clock_.valid() ? monotonicMs() + clock_.offset() : 0; }

void GalaxiaDevice::topic(char* out, size_t size, const char* suffix) const {
  snprintf(out, size, "%sdev/%s/%s", kPrefix, id_, suffix);
}

void GalaxiaDevice::loop() {
  beginChannels();
  uint32_t now = millis();

  if (rebootRequested_) reboot();

  // Inputs first, so a press is read even while the network is down.
  for (uint8_t i = 0; i < channelCount_; i++) channels_[i]->tick(now);

  if (identifying_) {
    if (after(now, identifyUntil_)) {
      identifying_ = false;
      if (identifyPin_ >= 0) digitalWrite(identifyPin_, LOW);
    } else if (identifyPin_ >= 0) {
      digitalWrite(identifyPin_, ((identifyUntil_ - now) / 125) % 2 ? HIGH : LOW);
    }
  }

  if (!networkUp()) {
    if (wasConnected_) onDisconnected(now);
    return;
  }
  if (!mqtt_.connected()) {
    if (wasConnected_) onDisconnected(now);
    if (after(now, nextAttempt_)) {
      // Retained outputs can arrive (and reach user callbacks) while subscribing.
      inCallback_ = true;
      tryConnect(now);
      inCallback_ = false;
    }
    return;
  }
  inCallback_ = true;
  mqtt_.loop();
  inCallback_ = false;
  if (!mqtt_.connected()) return;

  for (uint8_t i = 0; i < channelCount_; i++) {
    if (channels_[i]->deferred) {
      channels_[i]->deferred = false;
      channels_[i]->republish();
    }
  }
  if (heartbeatDeferred_) {
    heartbeatDeferred_ = false;
    heartbeatNow();
  }
  if (after(now, nextStatus_)) {
    publishStatus();
    nextStatus_ += kStatusMs;
    if (after(now, nextStatus_)) nextStatus_ = now + kStatusMs;
  }
  if (safety_ && after(now, nextHeartbeat_)) {
    sendHeartbeat();
    nextHeartbeat_ += kHeartbeatMs;
    if (after(now, nextHeartbeat_)) nextHeartbeat_ = now + kHeartbeatMs;
  }
}

void GalaxiaDevice::tryConnect(uint32_t now) {
  if (!mqttBegun_) {
    mqttBegun_ = true;
    mqtt_.begin(host_, port_, *client_);
    mqtt_.setKeepAlive(kKeepAliveS);
    mqtt_.setCleanSession(true);
    mqtt_.setTimeout(1000);
    mqtt_.dropOverflow(true);
    char will[96];
    topic(will, sizeof(will), "status");
    mqtt_.setWill(will, kOffline, true, 0);
    mqtt_.onMessageAdvanced(
        [this](MQTTClient*, char topicName[], char bytes[], int length) { onMessage(topicName, bytes, length); });
  }
  if (useWifi_) static_cast<NetworkClient*>(client_)->setNoDelay(true);
  if (mqtt_.connect(id_, id_, token_)) {
    backoff_ = 1000;
    onConnected(now);
    return;
  }
  lwmqtt_return_code_t code = mqtt_.returnCode();
  if (mqtt_.lastError() == LWMQTT_CONNECTION_DENIED &&
      (code == LWMQTT_IDENTIFIER_REJECTED || code == LWMQTT_BAD_USERNAME_OR_PASSWORD || code == LWMQTT_NOT_AUTHORIZED)) {
    // These change only when someone acts on the Devices page (or the sketch is fixed), so retry slowly: a device
    // that is approved again or given its token then recovers by itself.
    refusal_ = (int)code;
    const char* why = code == LWMQTT_NOT_AUTHORIZED            ? "the device is blocked on the Devices page"
                      : code == LWMQTT_BAD_USERNAME_OR_PASSWORD ? "wrong token: this id is approved with another token"
                                                                : "the device id is invalid or reserved";
    logf("galaxia: the FMS refused %s (code %d): %s; trying again in %u s", id_, (int)code, why,
         (unsigned)(kRefusedRetryMs / 1000));
    nextAttempt_ = now + kRefusedRetryMs;
    return;
  }
  logf("galaxia: cannot reach the FMS at %s:%u (error %d); retrying in %u s", host_, port_, (int)mqtt_.lastError(),
       (unsigned)(backoff_ / 1000));
  nextAttempt_ = now + backoff_ + (uint32_t)random(500);
  backoff_ = backoff_ * 2 > kMaxBackoffMs ? kMaxBackoffMs : backoff_ * 2;
}

void GalaxiaDevice::onConnected(uint32_t now) {
  wasConnected_ = true;
  approved_ = false;
  refusal_ = 0;
  logf("galaxia: connected to the FMS as %s", id_);
  publishManifest();
  publishStatus();
  // Every input's current value right after the manifest: the first total the FMS hears from a counter only sets
  // its starting point, so the next increment counts. Nothing has been subscribed yet, so this is safe to send now.
  bool inside = inCallback_;
  inCallback_ = false;
  for (uint8_t i = 0; i < channelCount_; i++) {
    if (!channels_[i]->output()) channels_[i]->republish();
  }
  inCallback_ = inside;
  nextStatus_ = now + kStatusMs;
  nextHeartbeat_ = now;
  char filter[64];
  snprintf(filter, sizeof(filter), "%sfield/#", kPrefix);
  if (!mqtt_.subscribe(filter)) return;
  // A pending device is refused its outputs. The FMS drops the connection when it approves the device, and the
  // subscription succeeds on the next connection.
  if (probe_) subscribeOutputs(now);
}

bool GalaxiaDevice::subscribeOutputs(uint32_t now) {
  char filter[96];
  topic(filter, sizeof(filter), "out/+");
  bool ok = mqtt_.subscribe(filter);
  if (ok) {
    topic(filter, sizeof(filter), "cmd");
    ok = mqtt_.subscribe(filter);
  }
  if (!ok) {
    handleRefusal(now);
    return false;
  }
  approved_ = true;
  refusedLogged_ = false;
  return true;
}

void GalaxiaDevice::handleRefusal(uint32_t now) {
  if (mqtt_.lastError() != LWMQTT_FAILED_SUBSCRIPTION) return;  // a network error: reconnect as usual
  // The FMS refuses output subscriptions while the device is pending approval, and the MQTT library closes the
  // connection when that happens. Reconnect at once without asking for outputs; when the FMS approves the device it
  // drops that connection, and the next one asks again.
  if (!refusedLogged_) {
    logf("galaxia: waiting for approval on the FMS Devices page");
    refusedLogged_ = true;
  }
  probe_ = false;
  nextAttempt_ = now;
  wasConnected_ = false;
}

void GalaxiaDevice::onDisconnected(uint32_t now) {
  wasConnected_ = false;
  approved_ = false;
  // A connection lost for any other reason than a refused subscription may mean the FMS reconnected us after
  // approving the device, so ask for outputs again straight away.
  probe_ = true;
  logf("galaxia: connection to the FMS lost; reconnecting");
  if (!after(now, nextAttempt_)) return;
  nextAttempt_ = now;
}

void GalaxiaDevice::disconnect() {
  if (mqtt_.connected()) {
    publishRaw("status", kOffline, strlen(kOffline), true);
    mqtt_.disconnect();
  }
  wasConnected_ = false;
  host_ = nullptr;
}

bool GalaxiaDevice::publishRaw(const char* suffix, const char* payload, size_t length, bool retained) {
  if (!mqtt_.connected()) return false;
  char name[112];
  topic(name, sizeof(name), suffix);
  return mqtt_.publish(name, payload, (int)length, retained, 0);
}

void GalaxiaDevice::publishJson(const char* suffix, JsonDocument& doc, bool retained) {
  char payload[GALAXIA_MQTT_BUFFER - 128];
  size_t length = serializeJson(doc, payload, sizeof(payload));
  if (length == 0 || length >= sizeof(payload) - 1) {
    logf("galaxia: the %s message is too large (raise GALAXIA_MQTT_BUFFER)", suffix);
    return;
  }
  publishRaw(suffix, payload, length, retained);
}

void GalaxiaDevice::publishManifest() {
  JsonDocument doc;
  doc["proto"] = 1;
  doc["id"] = (const char*)id_;
  if (name_ != nullptr) doc["name"] = name_;
  if (firmware_ != nullptr) doc["fw"] = firmware_;
  doc["sdk"] = "galaxia-arduino/" GALAXIA_SDK_VERSION;
  if (board_ != nullptr) doc["board"] = board_;
  JsonArray list = doc["channels"].to<JsonArray>();
  for (uint8_t i = 0; i < channelCount_; i++) channels_[i]->describe(list.add<JsonObject>());
  publishJson("manifest", doc, true);
}

void GalaxiaDevice::publishStatus() {
  JsonDocument doc;
  doc["online"] = true;
  doc["up"] = (uint32_t)(monotonicMs() / 1000);
  if (useWifi_) {
    doc["rssi"] = WiFi.RSSI();
    doc["ip"] = WiFi.localIP().toString();
  } else if (localIp_ != nullptr) {
    doc["ip"] = localIp_().toString();
  }
  doc["heap"] = ESP.getFreeHeap();
  publishJson("status", doc, true);
}

bool GalaxiaDevice::acceptInput(Channel* channel) {
  // Inputs are not queued while the connection is down: a button pressed during an outage must not start a match
  // seconds later. The current values are sent again once the connection is back.
  if (channel == nullptr || !mqtt_.connected()) return false;
  if (inCallback_) {
    // The MQTT library must not be used from inside its own callback; loop() sends the value afterwards.
    channel->deferred = true;
    return false;
  }
  return true;
}

void GalaxiaDevice::sendInputDocument(Channel* channel, JsonDocument& doc) {
  doc["seq"] = ++seq_;
  char suffix[48];
  snprintf(suffix, sizeof(suffix), "in/%s", channel->id());
  publishJson(suffix, doc, false);
}

void GalaxiaDevice::heartbeatNow() {
  if (!safety_ || !mqtt_.connected()) return;
  if (inCallback_) {
    heartbeatDeferred_ = true;
    return;
  }
  sendHeartbeat();
  nextHeartbeat_ = millis() + kHeartbeatMs;
}

void GalaxiaDevice::sendHeartbeat() {
  JsonDocument doc;
  doc["seq"] = ++heartbeatSeq_;
  JsonObject states = doc["ch"].to<JsonObject>();
  for (uint8_t i = 0; i < channelCount_; i++) {
    int state = channels_[i]->safetyState();
    if (state >= 0) states[channels_[i]->id()] = state;
  }
  publishJson("hb", doc, false);
}

// Messages from the FMS

void GalaxiaDevice::onMessage(char* topicName, char* bytes, int length) {
  size_t prefixLength = strlen(kPrefix);
  if (strncmp(topicName, kPrefix, prefixLength) != 0) return;
  const char* rest = topicName + prefixLength;

  JsonDocument doc;
  bool empty = length <= 0;
  if (!empty) {
    DeserializationError error = deserializeJson(doc, bytes, (size_t)length);
    if (error || !doc.is<JsonObject>()) {
      logf("galaxia: ignoring a message on %s that is not a JSON object", topicName);
      return;
    }
  }
  JsonObjectConst payload = doc.as<JsonObjectConst>();

  if (strncmp(rest, "field/", 6) == 0) {
    if (!empty) handleField(rest + 6, payload);
    return;
  }
  char mine[64];
  snprintf(mine, sizeof(mine), "dev/%s/", id_);
  size_t mineLength = strlen(mine);
  if (strncmp(rest, mine, mineLength) != 0) return;
  rest += mineLength;
  if (strncmp(rest, "out/", 4) == 0) {
    const char* name = rest + 4;
    for (uint8_t i = 0; i < channelCount_; i++) {
      Channel* channel = channels_[i];
      if (!channel->output() || strcmp(channel->id(), name) != 0) continue;
      if (empty) {
        channel->clear();
      } else {
        channel->receive(payload);
      }
    }
  } else if (strcmp(rest, "cmd") == 0 && !empty) {
    handleCommand(payload);
  }
}

void GalaxiaDevice::sampleClock(double fieldMs) { clock_.sample((int64_t)fieldMs - monotonicMs()); }

void GalaxiaDevice::handleField(const char* name, JsonObjectConst payload) {
  if (strcmp(name, "clock") == 0) {
    JsonVariantConst t = payload["t"];
    if (t.is<double>()) sampleClock(t.as<double>());
  } else if (strcmp(name, "state") == 0) {
    field_.valid = true;
    field_.receivedMs = millis();
    strlcpy(field_.state, payload["state"] | "", sizeof(field_.state));
    strlcpy(field_.phase, payload["phase"] | "", sizeof(field_.phase));
    JsonVariantConst phaseLeft = payload["phase_remaining"];
    field_.phaseRemaining = phaseLeft.is<double>() ? phaseLeft.as<float>() : NAN;
    JsonVariantConst matchLeft = payload["match_remaining"];
    field_.matchRemaining = matchLeft.is<double>() ? matchLeft.as<float>() : NAN;
    field_.scoreCount = 0;
    for (JsonPairConst score : payload["scores"].as<JsonObjectConst>()) {
      if (field_.scoreCount >= 4) break;
      FieldState::Score& entry = field_.scores[field_.scoreCount++];
      strlcpy(entry.alliance, score.key().c_str(), sizeof(entry.alliance));
      entry.points = score.value().as<float>();
    }
    if (fieldHandler_) fieldHandler_(field_);
  }
}

void GalaxiaDevice::handleCommand(JsonObjectConst payload) {
  const char* action = payload["do"] | "";
  if (strcmp(action, "identify") == 0) {
    identify();
  } else if (strcmp(action, "reboot") == 0) {
    rebootRequested_ = true;  // from loop(): the MQTT library must not publish inside its callback
  } else if (commandHandler_) {
    commandHandler_(action, payload);
  }
}

void GalaxiaDevice::identify() {
  uint32_t now = millis();
  identifying_ = true;
  identifyUntil_ = now + kIdentifyMs;
  for (uint8_t i = 0; i < channelCount_; i++) channels_[i]->identify(now);
  if (identifyHandler_) identifyHandler_();
}

void GalaxiaDevice::reboot() {
  rebootRequested_ = false;
  if (rebootHandler_) {
    rebootHandler_();
    return;
  }
  logf("galaxia: restarting, as the FMS asked");
  disconnect();
  delay(100);
  ESP.restart();
}
