// Reading led_strip commands from JSON. Depends only on ArduinoJson and GalaxiaLed, so it builds on a PC too.
// MIT licence, copyright Galaxia FMS contributors.

#ifndef GALAXIA_JSON_H
#define GALAXIA_JSON_H

#include <ArduinoJson.h>

#include "GalaxiaLed.h"

namespace galaxia {

// Fills `out` from a command such as {"p":"blink","c":["#ff0000"],"ms":500,"br":0.6}. Returns the number of
// segments that did not fit in GALAXIA_MAX_SEGMENTS (normally 0).
int parseStripCommand(JsonObjectConst source, StripCommand& out);

// A JSON number as the double the FMS and the browser have. ArduinoJson 7 keeps a short decimal such as 0.35 as a
// float, which is not the double 0.35; this finds the shortest decimal that gives that float and reads it as a
// double. `fallback` when the value is not a number.
double exactNumber(JsonVariantConst value, double fallback);

}  // namespace galaxia

#endif
