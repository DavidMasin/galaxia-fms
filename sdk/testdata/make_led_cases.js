// Writes led_cases.json by rendering commands with the FMS's reference implementation, static/js/led_patterns.js
// (the one the Lighting page's preview and the virtual devices use). Every SDK's tests compare against the result.
//
//   node sdk/testdata/make_led_cases.js

const fs = require("fs");
const path = require("path");
const LedPatterns = require(path.join(__dirname, "..", "..", "static", "js", "led_patterns.js"));

const T0 = 1794510000000;  // a field clock time, UTC milliseconds

const commands = [
  [{p: "off", c: ["#ff0000"]}, 3, [T0]],
  [{p: "solid", c: ["#ff8000"]}, 3, [T0]],
  [{p: "solid"}, 2, [T0]],
  [{p: "sparkle", c: ["#ff0000"]}, 2, [T0]],
  [{}, 2, [T0]],
  [{p: "solid", c: ["FF8000", "#00ff00"]}, 2, [T0]],
  [{p: "solid", c: ["#ff80"]}, 1, [T0]],
  // Brightness, including the values where c x br lands on a half.
  [{p: "solid", c: ["#ffffff"], br: 0.1}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 0.3}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 0.5}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 0.7}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 0.9}, 1, [T0]],
  [{p: "solid", c: ["#c0ffee"], br: 0.35}, 1, [T0]],
  [{p: "solid", c: ["#c0ffee"], br: 0.6}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 0}, 1, [T0]],
  [{p: "solid", c: ["#ffffff"], br: 7}, 1, [T0]],
  // Blink: default period 500, half on, half off.
  [{p: "blink", c: ["#ff0000", "#0000ff"], ms: 400}, 2, [T0, T0 + 199, T0 + 200, T0 + 399, T0 + 400, T0 + 1234567]],
  [{p: "blink", c: ["#00ff00"]}, 1, [T0, T0 + 249, T0 + 250, T0 + 499]],
  [{p: "blink", c: ["#00ff00"], ms: 333}, 1, [T0 + 166, T0 + 167]],
  // Pulse: default period 1500, from the second colour to the first and back.
  [{p: "pulse", c: ["#ff0000", "#0000ff"], ms: 1000}, 1, [T0, T0 + 100, T0 + 250, T0 + 333, T0 + 500, T0 + 750, T0 + 999]],
  [{p: "pulse", c: ["#ffffff"]}, 1, [T0, T0 + 375, T0 + 750, T0 + 1125, T0 + 777]],
  [{p: "pulse", c: ["#ffffff"], br: 0.5}, 1, [T0 + 300]],
  [{p: "pulse", c: ["#12abef", "#fe9801"], ms: 1234}, 1, [T0 + 1, T0 + 617, T0 + 999]],
  // Chase: a block of max(1, n / 8) LEDs runs along the strip once per period (default 1000).
  [{p: "chase", c: ["#ff0000", "#0000ff"], ms: 800}, 16, [T0, T0 + 50, T0 + 100, T0 + 399, T0 + 400, T0 + 799]],
  [{p: "chase", c: ["#00ffff"]}, 7, [T0, T0 + 143, T0 + 300, T0 + 857]],
  [{p: "chase", c: ["#00ffff"]}, 30, [T0 + 100, T0 + 300, T0 + 700, T0 + 999]],
  [{p: "chase", c: ["#00ffff"]}, 1, [T0 + 500]],
  [{p: "chase", c: ["#ff00ff"], ms: 777}, 10, [T0 + 77, T0 + 233, T0 + 777 * 3 + 600]],
  // Rainbow: default period 3000.
  [{p: "rainbow"}, 12, [T0, T0 + 1000, T0 + 2999]],
  [{p: "rainbow", ms: 1000, br: 0.8}, 5, [T0 + 123]],
  [{p: "rainbow", ms: 2500}, 7, [T0 + 1, T0 + 1666]],
  // Progress: round(v x n) LEDs, halves rounded up.
  [{p: "progress", c: ["#00c853", "#202020"], v: 0.62}, 30, [T0]],
  [{p: "progress", c: ["#ff0000", "#0000ff"], v: 0.05}, 10, [T0]],
  [{p: "progress", c: ["#ff0000", "#0000ff"], v: 0.15}, 10, [T0]],
  [{p: "progress", c: ["#ff0000", "#0000ff"], v: 0.25}, 10, [T0]],
  [{p: "progress", c: ["#ff0000", "#0000ff"], v: 0.35}, 10, [T0]],
  [{p: "progress", c: ["#ff0000", "#0000ff"], v: 0.7}, 10, [T0]],
  [{p: "progress", c: ["#ff0000"], v: 1.5}, 4, [T0]],
  [{p: "progress", c: ["#ff0000"], v: -1}, 4, [T0]],
  [{p: "progress", c: ["#ff0000"]}, 4, [T0]],
  [{p: "progress", c: ["#ff0000"], v: 0.417}, 60, [T0]],
  // Segments: each with its own pattern; LEDs outside every segment are off; later segments win.
  [{
    br: 0.8,
    seg: [
      {s: 0, n: 4, p: "blink", c: ["#ff0000"], ms: 400},
      {s: 4, n: 4, p: "chase", c: ["#0000ff"], ms: 100},
      {s: 9, n: 5, p: "progress", c: ["#ffffff", "#100000"], v: 0.5},
    ],
  }, 12, [T0, T0 + 150, T0 + 250, T0 + 75]],
  [{p: "solid", c: ["#ffffff"], seg: []}, 3, [T0]],
  [{p: "solid", c: ["#ffffff"], seg: [{s: 1, n: 0, p: "solid", c: ["#ff0000"]}]}, 3, [T0]],
  [{seg: [{s: -2, n: 2, p: "solid", c: ["#123456"]}, {s: 2, n: 9, p: "solid", c: ["#654321"]}]}, 4, [T0]],
  [{seg: [{s: 0, n: 6, p: "solid", c: ["#ff0000"]}, {s: 2, n: 2, p: "pulse", c: ["#00ff00"]}]}, 6, [T0 + 400]],
  [{seg: [{s: 5, n: 3, p: "solid", c: ["#ff0000"]}]}, 4, [T0]],
];

const cases = [];
for (const [cmd, pixels, times] of commands) {
  for (const t of times) {
    cases.push({cmd: cmd, pixels: pixels, t: t, frame: LedPatterns.render(cmd, pixels, t)});
  }
}
const out = path.join(__dirname, "led_cases.json");
fs.writeFileSync(out, JSON.stringify(cases, null, 1) + "\n");
console.log("wrote " + cases.length + " cases to " + out);
