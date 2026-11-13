// LED strip patterns for Galaxia FMS devices, as defined in docs/devices.md ("Pattern definitions"). This follows
// the FMS's reference implementation, static/js/led_patterns.js, step for step in double precision, so a strip
// shows exactly what the Lighting page's preview shows. Plain C++ with no Arduino dependency, so it is also built
// and tested on a PC (test/native).
// MIT licence, copyright Galaxia FMS contributors.

#ifndef GALAXIA_LED_H
#define GALAXIA_LED_H

#include <stddef.h>
#include <stdint.h>

#ifndef GALAXIA_MAX_SEGMENTS
#define GALAXIA_MAX_SEGMENTS 16
#endif

namespace galaxia {

struct Rgb {
  uint8_t r;
  uint8_t g;
  uint8_t b;
};

enum Pattern : uint8_t {
  kOff,
  kSolid,
  kBlink,
  kPulse,
  kChase,
  kRainbow,
  kProgress,
};

struct Look {
  Pattern pattern = kOff;
  Rgb a = {0, 0, 0};  // the first colour
  Rgb b = {0, 0, 0};  // the second colour, black when absent
  double ms = 0;      // the period; 0 or less means the pattern's default
  double v = 0;       // the fill for progress, 0 to 1

  bool animated() const;
};

struct Segment {
  int32_t start = 0;
  int32_t count = 0;
  Look look;
};

struct StripCommand {
  Look look;
  double brightness = 1.0;
  bool segmented = false;  // a non-empty "seg" list: only the segments are drawn (an empty list means no segments)
  int16_t segmentCount = 0;
  Segment segments[GALAXIA_MAX_SEGMENTS];

  bool animated() const;
};

// The default period of a pattern: blink 500, pulse 1500, chase 1000, rainbow 3000 (others 1000).
double defaultPeriod(Pattern pattern);
// "#rrggbb" or "rrggbb" (any case) to a colour; anything else is black.
Rgb parseColor(const char* text);
// A pattern name; unknown names are kOff.
Pattern parsePattern(const char* name);
// JavaScript's Math.round: the nearest integer, halves towards +infinity.
double jsRound(double x);
// The reference's mod(a, b) = ((a % b) + b) % b, with JavaScript's % (fmod).
double jsMod(double a, double b);
// Renders a command onto `pixels` LEDs at field time `t` (UTC milliseconds). `limit` is the device's own brightness
// cap; it multiplies the command's brightness (1 changes nothing).
void render(const StripCommand& command, Rgb* out, uint16_t pixels, double t, double limit);

}  // namespace galaxia

#endif
