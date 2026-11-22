// Tests of what a display does with the server's clock and with the "countdown" message.
//
// Run with: node --test static/js/countdown_display_test.js

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = function (name) {
  return fs.readFileSync(path.join(__dirname, name), "utf8");
};

// Objects made inside the sandbox the scripts run in have its prototypes, not this file's; copied through JSON they
// compare by what they hold.
const plain = function (value) {
  return JSON.parse(JSON.stringify(value));
};

// Lets everything that is waiting on a promise run.
const settle = async function () {
  for (let i = 0; i < 20; i++) {
    await Promise.resolve();
  }
};

// A page whose clock the test moves by hand, and whose requests for the time take as long as the test says.
const loadClock = function (trueOffsetMs, trips) {
  const state = {now: 5000, timers: [], asked: 0};
  const context = vm.createContext({
    performance: {
      timeOrigin: 1700000000000,
      now: function () {
        return state.now;
      },
    },
    document: {
      visibilityState: "visible",
      addEventListener: function () {
      },
    },
    setTimeout: function (callback) {
      state.timers.push(callback);
      return state.timers.length;
    },
    clearTimeout: function () {
    },
    fetch: function (url, options) {
      assert.equal(url, "/api/v1/time");
      assert.equal(options.cache, "no-store");
      const trip = trips[Math.min(state.asked, trips.length - 1)];
      state.asked++;
      if (trip === null) {
        return Promise.reject(new Error("unreachable"));
      }
      state.now += trip.up;
      const serverMs = 1700000000000 + state.now + trueOffsetMs;
      state.now += trip.down;
      return Promise.resolve({
        ok: true,
        json: function () {
          return Promise.resolve({t: Math.floor(serverMs), us: Math.round(serverMs * 1000)});
        },
      });
    },
    Promise: Promise,
    Math: Math,
    Error: Error,
  });
  vm.runInContext(source("clock_sync.js"), context);
  state.clock = vm.runInContext("GalaxiaClock", context);
  // Runs the timer the clock set, which asks the server again.
  state.tick = async function () {
    const timers = state.timers;
    state.timers = [];
    timers.forEach(function (callback) {
      callback();
    });
    await settle();
  };
  return state;
};

test("the clock believes the answer that came back fastest", async function () {
  // The first two answers were held up on the way there, which makes the server look further ahead than it is.
  const state = loadClock(250, [{up: 40, down: 2}, {up: 12, down: 2}, {up: 2, down: 2}, {up: 30, down: 3}]);
  const clock = state.clock;
  assert.equal(clock.offsetMs(), 0);
  assert.equal(clock.status().measured, false);
  let ready = false;
  clock.ready.then(function () {
    ready = true;
  });

  clock.start();
  clock.start();  // Starting twice asks no more often.
  await settle();
  assert.equal(state.asked, 1);
  assert.ok(Math.abs(clock.offsetMs() - 269) < 0.01, "the first answer is all there is: " + clock.offsetMs());
  assert.equal(ready, false, "one answer is not enough to count down by");

  await state.tick();
  assert.ok(Math.abs(clock.offsetMs() - 255) < 0.01, "a faster answer is believed over a slower one");
  await state.tick();
  await settle();
  assert.equal(ready, true);
  assert.ok(Math.abs(clock.offsetMs() - 250) < 0.01, "the fastest answer is right: " + clock.offsetMs());
  assert.equal(clock.status().roundTripMs, 4);

  // A later, slower answer does not displace it.
  await state.tick();
  assert.ok(Math.abs(clock.offsetMs() - 250) < 0.01);
  assert.equal(clock.status().samples, 4);
  assert.ok(Math.abs(clock.now() - (1700000000000 + state.now + 250)) < 0.01);
});

test("a display that cannot ask the server counts on its own clock", async function () {
  const state = loadClock(250, [null]);
  let ready = false;
  state.clock.ready.then(function () {
    ready = true;
  });
  state.clock.start();
  await settle();
  for (let i = 0; i < 5 && !ready; i++) {
    assert.equal(ready, false);
    await state.tick();
  }
  assert.equal(ready, true, "it does not wait for ever");
  assert.equal(state.clock.offsetMs(), 0);
  assert.equal(state.clock.status().measured, false);
});

// A display with a countdown that only notes what it is asked to do.
const loadDisplay = function (settings) {
  const state = {serverNow: 1000000, armed: [], cancelled: 0, active: false, created: null, prepared: []};
  const context = vm.createContext({
    GalaxiaClock: {
      start: function () {
        state.clockStarted = true;
      },
      ready: Promise.resolve(),
      now: function () {
        return state.serverNow;
      },
      offsetMs: function () {
        return 7.5;
      },
      status: function () {
        return {};
      },
    },
    GalaxiaCountdown: {
      create: function (container, options) {
        state.created = options;
        return {
          audio: {
            context: null,
            ensureContext: function () {
            },
          },
          preloadAudio: function (baseUrl) {
            state.audioFrom = baseUrl;
            return Promise.resolve();
          },
          prepare: function (beats, colors) {
            state.prepared.push({beats: beats, colors: colors});
            return Promise.resolve();
          },
          warm: function () {
          },
          arm: function (spec) {
            state.armed.push(spec);
            state.active = true;
          },
          cancel: function () {
            state.cancelled++;
            state.active = false;
          },
          isActive: function () {
            return state.active;
          },
          showing: function () {
            return "";
          },
          stats: function () {
            return {};
          },
        };
      },
    },
    document: {
      addEventListener: function () {
      },
    },
    console: console,
    Promise: Promise,
    Object: Object,
    Math: Math,
  });
  vm.runInContext(source("countdown_display.js"), context);
  state.display = vm.runInContext("CountdownDisplay", context);
  state.display.init(Object.assign({
    container: {
      classList: {
        add: function () {
        },
      },
    },
  }, settings));
  return state;
};

const armedMessage = function (goAt, changes) {
  return Object.assign({state: "armed", goAt: goAt, beats: 3, intro: true, voice: "a_line", sound: true}, changes);
};

test("a display counts down to the moment the server names", async function () {
  const timer = {id: "timer"};
  const state = loadDisplay({
    style: "full", sound: true, audioLatencyMs: -40,
    colors: function () {
      return ["#111111", "#222222"];
    },
    resolveTarget: function () {
      return timer;
    },
  });
  assert.deepEqual(plain(state.created), {transparent: false, lite: "auto"});
  assert.equal(state.clockStarted, true);
  assert.equal(state.audioFrom, "/static/audio/countdown/");

  state.display.handle({state: "idle"});
  await settle();
  assert.equal(state.armed.length, 0);
  assert.equal(state.cancelled, 0);

  const message = armedMessage(state.serverNow + 6000);
  state.display.handle(message);
  await settle();
  assert.equal(state.armed.length, 1);
  const spec = state.armed[0];
  assert.equal(spec.goAt, message.goAt);
  assert.equal(spec.beats, 3);
  assert.equal(spec.intro, true);
  assert.equal(spec.voice, "a_line");
  assert.equal(spec.clockOffsetMs, 7.5);
  assert.equal(spec.audio, true);
  assert.equal(spec.audioLatencyMs, -40);
  assert.deepEqual(spec.colors, ["#111111", "#222222"]);
  assert.equal(spec.resolveTarget, timer);
  assert.equal("title" in spec, false, "the line is shown as well as spoken");
  assert.equal(state.display.status().armedGoAt, message.goAt);

  // Told again after reconnecting, it carries on rather than starting over.
  state.display.handle(armedMessage(message.goAt));
  await settle();
  assert.equal(state.armed.length, 1);

  // Once the match has started, hearing that no countdown is running changes nothing: the rest plays out.
  state.serverNow = message.goAt + 200;
  state.display.handle({state: "idle"});
  assert.equal(state.cancelled, 0);
  spec.onDone();
  assert.equal(state.display.status().armedGoAt, null);
});

test("a start that is called off stops the countdown", async function () {
  const state = loadDisplay({});
  assert.deepEqual(plain(state.created), {transparent: true, lite: "auto"}, "an overlay unless told otherwise");
  assert.equal(state.audioFrom, undefined, "a display plays no sound unless told to");

  state.display.handle(armedMessage(state.serverNow + 6000));
  await settle();
  assert.equal(state.armed.length, 1);
  assert.equal(state.armed[0].audio, false);
  state.display.handle({state: "cancelled"});
  assert.equal(state.cancelled, 1);
  assert.equal(state.display.status().armedGoAt, null);

  // A display that was away when the start was called off learns of it when it hears there is no countdown.
  state.display.handle(armedMessage(state.serverNow + 6000));
  await settle();
  assert.equal(state.armed.length, 2);
  state.serverNow += 2000;
  state.display.handle({state: "idle"});
  assert.equal(state.cancelled, 2);

  // A countdown called off before the display had armed it is never shown.
  const late = armedMessage(state.serverNow + 6000);
  state.display.handle(late);
  state.display.handle({state: "cancelled"});
  await settle();
  assert.equal(state.armed.length, 2);
});

test("what the message says about sound, the line and lateness is respected", async function () {
  const state = loadDisplay({style: "light", sound: true});
  assert.deepEqual(plain(state.created), {transparent: true, lite: true});

  // The match's sounds are muted.
  state.display.handle(armedMessage(state.serverNow + 1800, {beats: 1, intro: false, voice: "", sound: false}));
  await settle();
  assert.equal(state.armed.length, 1);
  assert.equal(state.armed[0].audio, false);
  assert.equal(state.armed[0].title, "", "a countdown without a line shows none");
  assert.equal(state.armed[0].beats, 1);

  // A display that hears of a start long past has nothing left to show.
  state.active = false;
  state.display.handle(armedMessage(state.serverNow - 5000));
  await settle();
  assert.equal(state.armed.length, 1);
  // One that hears of it just after the start still shows the end of it.
  state.display.handle(armedMessage(state.serverNow - 300));
  await settle();
  assert.equal(state.armed.length, 2);
});

test("a display with the countdown turned off ignores it", async function () {
  const state = loadDisplay({style: "off", sound: true});
  assert.equal(state.created, null);
  state.display.handle(armedMessage(state.serverNow + 6000));
  state.display.handle({state: "cancelled"});
  await settle();
  assert.equal(state.armed.length, 0);
  assert.equal(state.display.status().enabled, false);
});
