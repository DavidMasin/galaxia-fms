// MIT licence, copyright Galaxia FMS contributors.

#include "GalaxiaJson.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>

namespace galaxia {

// Within two steps of each other as floats. ArduinoJson's own float reading can be a step off the nearest float
// (it reads 0.05 as 0.049999997), so the decimal is matched with that much slack.
static bool closeFloats(float a, float b) {
  float up = a;
  float down = a;
  for (int step = 0; step <= 2; step++) {
    if (up == b || down == b) return true;
    up = nextafterf(up, INFINITY);
    down = nextafterf(down, -INFINITY);
  }
  return false;
}

double exactNumber(JsonVariantConst value, double fallback) {
  if (value.is<bool>()) return value.as<bool>() ? 1 : 0;  // as in JavaScript, true is 1
  if (!value.is<double>()) return fallback;
  double number = value.as<double>();
  float single = (float)number;
  if ((double)single != number) return number;  // more precise than a float: ArduinoJson kept the double
  // The shortest decimal (up to 7 significant digits, all a float holds) that reads as this float. FMS fills and
  // brightness are short decimals; a value written with 8 or more digits may come back a part in 10^8 out.
  char text[32];
  for (int digits = 1; digits <= 7; digits++) {
    snprintf(text, sizeof(text), "%.*g", digits, number);
    if (closeFloats(strtof(text, nullptr), single)) return strtod(text, nullptr);
  }
  return number;
}

static int32_t whole(JsonVariantConst value) {
  double number = exactNumber(value, 0);
  if (!(number > -2147483648.0 && number < 2147483647.0)) return 0;
  return (int32_t)number;
}

static void parseLook(JsonObjectConst source, Look& look) {
  look.pattern = parsePattern(source["p"].as<const char*>());
  JsonArrayConst colors = source["c"].as<JsonArrayConst>();
  look.a = parseColor(colors.size() > 0 ? colors[0].as<const char*>() : nullptr);
  look.b = parseColor(colors.size() > 1 ? colors[1].as<const char*>() : nullptr);
  look.ms = source["ms"].is<bool>() ? 0 : exactNumber(source["ms"], 0);
  double v = exactNumber(source["v"], 0);
  look.v = v > 0 ? (v > 1 ? 1 : v) : 0;
}

int parseStripCommand(JsonObjectConst source, StripCommand& out) {
  parseLook(source, out.look);
  JsonVariantConst br = source["br"];
  double brightness = br.isNull() ? 1 : exactNumber(br, 1);
  out.brightness = brightness > 0 ? (brightness > 1 ? 1 : brightness) : 0;
  out.segmentCount = 0;
  int dropped = 0;
  JsonVariantConst segments = source["seg"];
  out.segmented = segments.is<JsonArrayConst>() && segments.as<JsonArrayConst>().size() > 0;
  if (!out.segmented) return 0;
  for (JsonVariantConst item : segments.as<JsonArrayConst>()) {
    if (!item.is<JsonObjectConst>()) continue;  // drawing it would draw nothing
    JsonObjectConst entry = item.as<JsonObjectConst>();
    if (out.segmentCount >= GALAXIA_MAX_SEGMENTS) {
      dropped++;
      continue;
    }
    Segment& segment = out.segments[out.segmentCount++];
    segment.start = whole(entry["s"]);
    segment.count = whole(entry["n"]);
    parseLook(entry, segment.look);
  }
  return dropped;
}

}  // namespace galaxia
