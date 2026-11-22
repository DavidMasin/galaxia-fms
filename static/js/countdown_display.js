// Plays the match-start countdown on a display when the server announces one.
//
// The server says when the match will start (the "countdown" websocket message; see field/countdown.go). This
// file turns that into a countdown drawn by countdown.js, timed against the server's clock as clock_sync.js
// measures it. A display that connects in the middle of a countdown is told the same moment and joins in on the
// beat; one that was away while a countdown was called off stops when it hears there is none.
//
// Load countdown.js and clock_sync.js first. Usage:
//   CountdownDisplay.init({container: element, style: "overlay", sound: true, audioLatencyMs: 0,
//     colors: function () { return ["#d32f2f", "#1565c0"]; }, resolveTarget: function () { return element; }});
//   ... and route the "countdown" websocket message to CountdownDisplay.handle.

const CountdownDisplay = (function () {
  const audioBaseUrl = "/static/audio/countdown/";
  // How long after the start the countdown is still on screen; a display told of a start further back than this
  // has nothing left to show.
  const afterGoMs = 1300;

  let settings = null;
  let countdown = null;
  let audioReady = null;
  let wanted = null;     // The countdown the server last announced, until it is armed or called off.
  let armedGoAt = null;  // The start the countdown on screen is counting down to.

  // settings:
  //   container: the element the countdown covers.
  //   style: "overlay" (drawn over whatever is on screen), "full" (the screen is dimmed behind it) or "light"
  //     (an overlay without the costly effects, for slow hardware). Anything else turns the countdown off.
  //   sound: whether this display plays the countdown's sound. Only one display in a room should.
  //   audioLatencyMs: plays this display's sound that much later (or sooner, if negative), to line it up with a
  //     picture or a speaker that lags.
  //   colors: returns the two colours that close in from the sides, the left one first.
  //   resolveTarget: returns the element the last beat shrinks into, usually the match timer, or null.
  const init = function (options) {
    settings = Object.assign({
      style: "overlay", sound: false, audioLatencyMs: 0,
      colors: function () {
        return null;
      },
      resolveTarget: function () {
        return null;
      },
    }, options);
    if (["overlay", "full", "light"].indexOf(settings.style) === -1) {
      return;
    }
    settings.container.classList.add("gc-host");
    countdown = GalaxiaCountdown.create(settings.container, {
      transparent: settings.style !== "full",
      lite: settings.style === "light" ? true : "auto",
    });
    GalaxiaClock.start();
    // Draw the numerals as soon as the game, with its colours and its count, is known: nothing is then left to
    // draw when a countdown is announced.
    if (typeof GameView !== "undefined") {
      GameView.onChange(function (view) {
        const beats = view.countdown ? Math.max(view.countdown.beats || 0, view.countdown.quickBeats || 0) : 3;
        const colors = settings.colors() || undefined;
        countdown.prepare(beats, colors).then(function () {
          countdown.warm(beats, colors);
        });
      });
    }
    if (settings.sound) {
      audioReady = countdown.preloadAudio(audioBaseUrl).catch(function (error) {
        console.log("The countdown's sounds could not be loaded: " + error);
      });
      // A browser only lets a page make sound once someone has touched it, unless it was started with autoplay
      // allowed. Until then the countdown is silent; the first touch or key press gives it its voice.
      const unlock = function () {
        countdown.audio.ensureContext();
      };
      document.addEventListener("pointerdown", unlock);
      document.addEventListener("keydown", unlock);
    }
  };

  const arm = function (message) {
    if (wanted !== message) {
      return;  // Called off, or replaced, while the clock was being measured.
    }
    wanted = null;
    if (GalaxiaClock.now() - message.goAt > afterGoMs) {
      return;
    }
    const spec = {
      goAt: message.goAt,
      beats: message.beats,
      intro: message.intro,
      voice: message.voice || "",
      clockOffsetMs: GalaxiaClock.offsetMs(),
      audio: settings.sound && message.sound !== false,
      audioLatencyMs: settings.audioLatencyMs,
      resolveTarget: settings.resolveTarget(),
      onDone: function () {
        armedGoAt = null;
      },
    };
    const colors = settings.colors();
    if (colors) {
      spec.colors = colors;
    }
    if (!message.intro) {
      spec.title = "";
    }
    armedGoAt = message.goAt;
    countdown.arm(spec);
  };

  // Handles the "countdown" websocket message.
  const handle = function (message) {
    if (countdown === null) {
      return;
    }
    switch (message.state) {
      case "armed":
        if (armedGoAt === message.goAt && countdown.isActive()) {
          return;  // Told again after reconnecting; it is already counting down to that moment.
        }
        wanted = message;
        Promise.all([GalaxiaClock.ready, audioReady]).then(function () {
          arm(message);
        });
        break;
      case "cancelled":
        wanted = null;
        armedGoAt = null;
        countdown.cancel();
        break;
      default:
        // No countdown is running. If this display is still counting down to a start, that start was called off
        // while it was not listening; once the start has passed, the rest plays out by itself.
        wanted = null;
        if (armedGoAt !== null && GalaxiaClock.now() < armedGoAt) {
          armedGoAt = null;
          countdown.cancel();
        }
    }
  };

  return {
    init: init,
    handle: handle,
    // For a display's diagnostics and for tests.
    status: function () {
      return {
        enabled: countdown !== null,
        active: countdown !== null && countdown.isActive(),
        showing: countdown === null ? "" : countdown.showing(),
        armedGoAt: armedGoAt,
        clock: GalaxiaClock.status(),
        frames: countdown === null ? null : countdown.stats(),
        audio: countdown === null || countdown.audio.context === null ? "none" : countdown.audio.context.state,
      };
    },
  };
})();
