// MIT licence, copyright Galaxia FMS contributors.

#include "GalaxiaLed.h"

#include <math.h>
#include <string.h>

namespace galaxia {

namespace {

const double kPi = 3.141592653589793;  // Math.PI

struct Color {
  double r;
  double g;
  double b;
};

int hexDigit(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  if (c >= 'A' && c <= 'F') return c - 'A' + 10;
  return -1;
}

Color of(Rgb c) { return Color{(double)c.r, (double)c.g, (double)c.b}; }

uint8_t channel(double value, double brightness) {
  double rounded = jsRound(value * brightness);
  if (!(rounded > 0)) return 0;
  return rounded > 255 ? 255 : (uint8_t)rounded;
}

Color hueToRgb(double hue) {
  double h = jsMod(hue, 1) * 6;
  double x = 1 - fabs(jsMod(h, 2) - 1);
  int sector = (int)floor(h) % 6;
  const double table[6][3] = {{1, x, 0}, {x, 1, 0}, {0, 1, x}, {0, x, 1}, {x, 0, 1}, {1, 0, x}};
  return Color{table[sector][0] * 255, table[sector][1] * 255, table[sector][2] * 255};
}

// drawPattern in the reference: fills out[start, start + count) with one look.
void draw(Rgb* out, int32_t start, int32_t count, const Look& look, double t, double brightness) {
  double period = look.ms > 0 ? look.ms : defaultPeriod(look.pattern);
  double phase = jsMod(t, period);
  Color a = of(look.a);
  Color b = of(look.b);
  double head = floor(phase / period * count);
  double block = floor(count / 8.0);
  if (block < 1) block = 1;
  double lit = jsRound(look.v * count);
  for (int32_t i = 0; i < count; i++) {
    Color color = {0, 0, 0};
    switch (look.pattern) {
      case kSolid:
        color = a;
        break;
      case kBlink:
        color = phase < period / 2 ? a : b;
        break;
      case kPulse: {
        double k = (1 - cos(2 * kPi * phase / period)) / 2;
        color = Color{b.r + (a.r - b.r) * k, b.g + (a.g - b.g) * k, b.b + (a.b - b.b) * k};
        break;
      }
      case kChase:
        color = jsMod((double)i - head, (double)count) < block ? a : b;
        break;
      case kRainbow:
        color = hueToRgb((double)i / count + phase / period);
        break;
      case kProgress:
        color = i < lit ? a : b;
        break;
      default:
        break;
    }
    Rgb& pixel = out[start + i];
    pixel.r = channel(color.r, brightness);
    pixel.g = channel(color.g, brightness);
    pixel.b = channel(color.b, brightness);
  }
}

}  // namespace

bool Look::animated() const {
  return pattern == kBlink || pattern == kPulse || pattern == kChase || pattern == kRainbow;
}

bool StripCommand::animated() const {
  if (!segmented) return look.animated();
  for (int16_t i = 0; i < segmentCount; i++) {
    if (segments[i].look.animated()) return true;
  }
  return false;
}

double defaultPeriod(Pattern pattern) {
  switch (pattern) {
    case kBlink:
      return 500;
    case kPulse:
      return 1500;
    case kChase:
      return 1000;
    case kRainbow:
      return 3000;
    default:
      return 1000;
  }
}

Rgb parseColor(const char* text) {
  const Rgb black = {0, 0, 0};
  if (text == nullptr) return black;
  if (text[0] == '#') text++;
  if (strlen(text) != 6) return black;
  uint8_t bytes[3];
  for (int i = 0; i < 3; i++) {
    int high = hexDigit(text[2 * i]);
    int low = hexDigit(text[2 * i + 1]);
    if (high < 0 || low < 0) return black;
    bytes[i] = (uint8_t)(high * 16 + low);
  }
  return Rgb{bytes[0], bytes[1], bytes[2]};
}

Pattern parsePattern(const char* name) {
  if (name == nullptr) return kOff;
  if (strcmp(name, "solid") == 0) return kSolid;
  if (strcmp(name, "blink") == 0) return kBlink;
  if (strcmp(name, "pulse") == 0) return kPulse;
  if (strcmp(name, "chase") == 0) return kChase;
  if (strcmp(name, "rainbow") == 0) return kRainbow;
  if (strcmp(name, "progress") == 0) return kProgress;
  return kOff;
}

double jsRound(double x) {
  double whole = floor(x);
  return x - whole >= 0.5 ? whole + 1 : whole;
}

double jsMod(double a, double b) { return fmod(fmod(a, b) + b, b); }

void render(const StripCommand& command, Rgb* out, uint16_t pixels, double t, double limit) {
  for (uint16_t i = 0; i < pixels; i++) out[i] = Rgb{0, 0, 0};
  double brightness = command.brightness;
  if (limit != 1.0) brightness = brightness * (limit < 0 ? 0 : limit > 1 ? 1 : limit);
  if (command.segmented) {
    for (int16_t s = 0; s < command.segmentCount; s++) {
      const Segment& segment = command.segments[s];
      int32_t start = segment.start < 0 ? 0 : segment.start > pixels ? pixels : segment.start;
      int32_t count = segment.count > pixels - start ? pixels - start : segment.count;
      if (count > 0) draw(out, start, count, segment.look, t, brightness);
    }
  } else {
    draw(out, 0, pixels, command.look, t, brightness);
  }
}

}  // namespace galaxia
