// The server's clock, as this page sees it.
//
// A display counts down to a moment the server names (see countdown.js), so it has to know how far its own clock is
// from the server's. It asks the server the time (GET /api/v1/time) a few times and believes the answer that came
// back fastest: an offset measured over a round trip is wrong by at most half the trip, so the shortest trip gives
// the best estimate. It asks again every few seconds, because clocks drift, and straight away when the page comes
// back from being hidden, because a hidden page's timers are slowed down.
//
// Usage:
//   GalaxiaClock.start();
//   GalaxiaClock.ready.then(function () { ... GalaxiaClock.offsetMs() ... });
//
// The offset is the server's clock minus this page's, in milliseconds, where this page's clock is
// performance.timeOrigin + performance.now(): a clock that never jumps, unlike Date.now().

const GalaxiaClock = (function () {
  const BURST = 6;           // Samples taken straight away.
  const BURST_GAP_MS = 150;
  const PERIOD_MS = 5000;    // Between samples afterwards.
  const KEEP = 8;            // How many recent samples the best is chosen from.
  const MAX_AGE_MS = 60000;  // A sample older than this is no longer believed.

  let samples = [];
  let best = null;
  let started = false;
  let timer = 0;
  let failures = 0;
  let resolveReady;
  const ready = new Promise(function (resolve) {
    resolveReady = resolve;
  });

  const choose = function () {
    const now = performance.now();
    samples = samples.filter(function (sample) {
      return now - sample.at <= MAX_AGE_MS;
    }).slice(-KEEP);
    best = null;
    samples.forEach(function (sample) {
      if (best === null || sample.roundTripMs < best.roundTripMs) {
        best = sample;
      }
    });
  };

  // Asks the server the time once.
  const sample = function () {
    const sent = performance.now();
    return fetch("/api/v1/time", {cache: "no-store"}).then(function (response) {
      if (!response.ok) {
        throw new Error("HTTP " + response.status);
      }
      return response.json();
    }).then(function (data) {
      const received = performance.now();
      // Microseconds where the server gives them; whole milliseconds would add up to one of error.
      const serverMs = typeof data.us === "number" ? data.us / 1000 : data.t;
      samples.push({
        offsetMs: serverMs - (performance.timeOrigin + (sent + received) / 2),
        roundTripMs: received - sent,
        at: received,
      });
      failures = 0;
      choose();
      if (samples.length >= Math.min(3, BURST)) {
        resolveReady();
      }
    }).catch(function () {
      failures++;
      if (failures >= BURST) {
        // The server cannot be asked. A display on the same computer is still right, and one elsewhere is better
        // off counting on its own clock than not counting at all.
        resolveReady();
      }
    });
  };

  const burst = function (remaining) {
    clearTimeout(timer);
    sample().then(function () {
      timer = setTimeout(function () {
        if (remaining > 1) {
          burst(remaining - 1);
        } else {
          burst(1);
        }
      }, remaining > 1 ? BURST_GAP_MS : PERIOD_MS);
    });
  };

  return {
    // Starts measuring. Calling it again does nothing.
    start: function () {
      if (started) {
        return;
      }
      started = true;
      burst(BURST);
      document.addEventListener("visibilitychange", function () {
        if (document.visibilityState === "visible") {
          burst(BURST);
        }
      });
    },

    // Resolves once the offset has been measured, or cannot be.
    ready: ready,

    // The server's clock minus this page's, in milliseconds; zero until it has been measured.
    offsetMs: function () {
      return best === null ? 0 : best.offsetMs;
    },

    // The server's time now, in milliseconds since 1970.
    now: function () {
      return performance.timeOrigin + performance.now() + this.offsetMs();
    },

    // What the measurement rests on, for a display's diagnostics: the offset, the round trip it was measured over
    // (its error is at most half of that), and how many samples are in hand.
    status: function () {
      return {
        measured: best !== null,
        offsetMs: best === null ? 0 : best.offsetMs,
        roundTripMs: best === null ? null : best.roundTripMs,
        samples: samples.length,
      };
    },
  };
})();
