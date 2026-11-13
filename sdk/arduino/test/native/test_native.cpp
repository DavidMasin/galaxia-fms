// Host-side tests for the parts of the Arduino library that do not need a board: LED rendering (against the cases
// every SDK shares, sdk/testdata/led_cases.json), JSON reading, the "at" schedule and the field clock estimate.
// Build and run with sdk/arduino/test/native/run_native_tests.py.

#include <ArduinoJson.h>
#include <stdio.h>
#include <stdlib.h>

#include <string>

#include "GalaxiaJson.h"
#include "GalaxiaLed.h"
#include "GalaxiaSchedule.h"

static int failures = 0;
static int checks = 0;

#define CHECK(condition)                                                    \
  do {                                                                      \
    checks++;                                                               \
    if (!(condition)) {                                                     \
      failures++;                                                           \
      fprintf(stderr, "%s:%d: CHECK failed: %s\n", __FILE__, __LINE__, #condition); \
    }                                                                       \
  } while (0)

static std::string readFile(const char* path) {
  FILE* file = fopen(path, "rb");
  if (file == nullptr) {
    fprintf(stderr, "cannot open %s\n", path);
    exit(2);
  }
  std::string text;
  char buffer[4096];
  size_t n;
  while ((n = fread(buffer, 1, sizeof(buffer), file)) > 0) text.append(buffer, n);
  fclose(file);
  return text;
}

static void testSharedCases(const char* path) {
  std::string text = readFile(path);
  JsonDocument doc;
  DeserializationError error = deserializeJson(doc, text);
  CHECK(!error);
  int count = 0;
  for (JsonObjectConst testCase : doc.as<JsonArrayConst>()) {
    galaxia::StripCommand command;
    galaxia::parseStripCommand(testCase["cmd"].as<JsonObjectConst>(), command);
    uint16_t pixels = testCase["pixels"].as<uint16_t>();
    double t = testCase["t"].as<double>();
    galaxia::Rgb frame[64];
    galaxia::render(command, frame, pixels, t, 1.0);
    JsonArrayConst expected = testCase["frame"].as<JsonArrayConst>();
    bool same = expected.size() == pixels;
    for (uint16_t i = 0; same && i < pixels; i++) {
      JsonArrayConst color = expected[i].as<JsonArrayConst>();
      same = color[0].as<int>() == frame[i].r && color[1].as<int>() == frame[i].g && color[2].as<int>() == frame[i].b;
    }
    if (!same) {
      std::string cmd;
      serializeJson(testCase["cmd"], cmd);
      fprintf(stderr, "case %d differs: %s t=%.0f\n", count, cmd.c_str(), t);
    }
    CHECK(same);
    count++;
  }
  CHECK(count > 80);
  printf("shared LED cases: %d\n", count);
}

static void testJsonAssumptions() {
  JsonDocument doc;
  deserializeJson(doc, "{\"at\":1794510000123,\"v\":1,\"f\":0.35,\"s\":\"x\",\"b\":true}");
  // The SDK reads every number with is<double>() / as<double>(): integers included, large ones exact.
  CHECK(doc["at"].is<double>());
  CHECK((int64_t)doc["at"].as<double>() == 1794510000123LL);
  CHECK(doc["v"].is<double>());
  // ArduinoJson 7 keeps a short decimal such as 0.35 as a float, not the double 0.35 the FMS and the browser use;
  // exactNumber() gets the double back.
  CHECK(doc["f"].as<double>() != 0.35);
  CHECK(galaxia::exactNumber(doc["f"], -1) == 0.35);
  CHECK(galaxia::exactNumber(doc["at"], -1) == 1794510000123.0);
  CHECK(galaxia::exactNumber(doc["v"], -1) == 1);
  CHECK(galaxia::exactNumber(doc["b"], -1) == 1);
  CHECK(galaxia::exactNumber(doc["s"], -1) == -1);
  const char* decimals[] = {"0.1", "0.3", "0.7", "0.9", "0.05", "0.15", "0.62", "0.417", "93.4", "0.001", "1234.5"};
  for (const char* decimal : decimals) {
    JsonDocument number;
    deserializeJson(number, decimal);
    CHECK(galaxia::exactNumber(number.as<JsonVariantConst>(), -1) == strtod(decimal, nullptr));
  }
  CHECK(!doc["s"].is<double>());
  CHECK(!doc["missing"].is<double>());
  CHECK(doc["missing"].as<const char*>() == nullptr);

  JsonDocument out;
  out["v"] = 7u;
  out["seq"] = 1u;
  std::string text;
  serializeJson(out, text);
  CHECK(text == "{\"v\":7,\"seq\":1}");
}

static void testParse() {
  JsonDocument doc;
  deserializeJson(doc, "{\"p\":\"chase\",\"c\":[\"#ff0000\",\"00ff00\"],\"ms\":40.9,\"br\":7,\"v\":-1}");
  galaxia::StripCommand command;
  CHECK(galaxia::parseStripCommand(doc.as<JsonObjectConst>(), command) == 0);
  CHECK(command.look.pattern == galaxia::kChase);
  CHECK(command.look.a.r == 255 && command.look.b.g == 255);
  CHECK(command.look.ms == 40.9);
  CHECK(command.brightness == 1.0);
  CHECK(command.look.v == 0);
  CHECK(!command.segmented);

  std::string many = "{\"seg\":[";
  for (int i = 0; i < GALAXIA_MAX_SEGMENTS + 3; i++) {
    if (i) many += ",";
    many += "{\"s\":" + std::to_string(i) + ",\"n\":1,\"p\":\"solid\",\"c\":[\"#ffffff\"]}";
  }
  many += ",{\"s\":0,\"n\":0}]}";
  JsonDocument segments;
  deserializeJson(segments, many);
  CHECK(galaxia::parseStripCommand(segments.as<JsonObjectConst>(), command) == 4);
  CHECK(command.segmented && command.segmentCount == GALAXIA_MAX_SEGMENTS);

  JsonDocument empty;
  deserializeJson(empty, "{\"p\":\"solid\",\"seg\":[]}");
  galaxia::parseStripCommand(empty.as<JsonObjectConst>(), command);
  CHECK(!command.segmented);  // an empty list: the top-level look
}

static void testSchedule() {
  galaxia::Schedule<int, 3> schedule;
  // Without "at", or without a clock, or with a time already past: at once, and the queue is cleared.
  CHECK(schedule.offer(false, 0, true, 1000, 1));
  CHECK(schedule.offer(true, 5000, false, 1000, 2));
  CHECK(schedule.offer(true, 900, true, 1000, 3));
  // Commands sent ahead queue up in time order.
  CHECK(!schedule.offer(true, 2000, true, 1000, 20));
  CHECK(!schedule.offer(true, 3000, true, 1000, 30));
  CHECK(!schedule.offer(true, 4000, true, 1000, 40));
  int value;
  int64_t at;
  CHECK(!schedule.popDue(true, 1999, value, at));
  CHECK(schedule.popDue(true, 2000, value, at) && value == 20 && at == 2000);
  CHECK(!schedule.popDue(true, 2500, value, at));
  // A command for an earlier time replaces those due at or after it.
  CHECK(!schedule.offer(true, 3000, true, 2500, 31));
  CHECK(schedule.size() == 1);
  // An immediate command cancels everything pending.
  CHECK(schedule.offer(false, 0, true, 2600, 99));
  CHECK(schedule.size() == 0);
  // When full, the earliest pending command is dropped.
  for (int i = 1; i <= 4; i++) schedule.offer(true, 10000 + i, true, 0, i);
  CHECK(schedule.size() == 3 && schedule.dropped() == 1);
  CHECK(schedule.popDue(true, 20000, value, at) && value == 2);
}

static void testClock() {
  galaxia::ClockEstimator clock;
  CHECK(!clock.valid());
  clock.sample(995000);
  CHECK(clock.valid() && clock.offset() == 995000);
  clock.sample(994960);  // 40 ms late
  CHECK(clock.offset() == 995000);
  for (int i = 0; i < 8; i++) clock.sample(994980);
  CHECK(clock.offset() == 994980);  // the old best has left the window
  clock.sample(994980 + 60000);     // one wild sample is ignored
  CHECK(clock.offset() == 994980);
  clock.sample(994980);
  for (int i = 0; i < 3; i++) clock.sample(994980 - 3600000);  // the FMS clock really moved
  CHECK(clock.offset() == 994980 - 3600000);
}

static void testRenderBasics() {
  galaxia::Rgb red = galaxia::parseColor("#ff0000");
  CHECK(red.r == 255 && red.g == 0 && red.b == 0);
  galaxia::Rgb bad = galaxia::parseColor("#ff00");
  CHECK(bad.r == 0 && bad.g == 0 && bad.b == 0);
  CHECK(galaxia::jsRound(2.5) == 3 && galaxia::jsRound(-2.5) == -2 && galaxia::jsRound(0.49999999999999994) == 0);
  CHECK(galaxia::jsMod(-1, 8) == 7);
  CHECK(galaxia::defaultPeriod(galaxia::kPulse) == 1500);

  // The device's own brightness cap multiplies br, and a cap of 1 leaves the reference untouched.
  galaxia::StripCommand command;
  command.look.pattern = galaxia::kSolid;
  command.look.a = galaxia::parseColor("#ff8000");
  command.brightness = 0.5;
  galaxia::Rgb pixel;
  galaxia::render(command, &pixel, 1, 0, 0.5);
  CHECK(pixel.r == 64 && pixel.g == 32 && pixel.b == 0);
  galaxia::render(command, &pixel, 1, 0, 1.0);
  CHECK(pixel.r == 128 && pixel.g == 64);
}

int main(int argc, char** argv) {
  if (argc < 2) {
    fprintf(stderr, "usage: test_native <path to led_cases.json>\n");
    return 2;
  }
  testJsonAssumptions();
  testParse();
  testSchedule();
  testClock();
  testRenderBasics();
  testSharedCases(argv[1]);
  printf("%d checks, %d failures\n", checks, failures);
  return failures == 0 ? 0 : 1;
}
