// The match-start countdown: "Drivers, behind the lines", 3, 2, 1, GO!
//
// Every frame is computed from the shared match clock, never from how many frames have passed: the server says
// when "GO!" happens (goAt, in server milliseconds), each display knows how far its own clock is from the server's
// (clockOffsetMs), and everything on screen is a pure function of "milliseconds until GO". Two displays therefore
// show the same beat at the same moment, and a display that drops a frame catches up instead of drifting.
//
// Usage:
//   const countdown = GalaxiaCountdown.create(containerElement, {transparent: false, lite: "auto"});
//   await countdown.preloadAudio("/static/audio/countdown/");     // only on the display that plays sound
//   countdown.arm({goAt, beats: 3, clockOffsetMs, colors: ["#d32f2f", "#1565c0"], audio: true});
//   countdown.cancel();                                          // abort or E-stop: fades out at once
//
// Sounds are scheduled on the Web Audio clock, mapped to the page's clock with getOutputTimestamp(), so they are
// timed by the audio hardware rather than by when JavaScript happens to run.

const GalaxiaCountdown = (function () {
  const BEAT_MS = 1000;
  const INTRO_LEAD_MS = 2600;   // How long before the first beat the intro line starts.
  const GO_HOLD_MS = 650;       // How long "GO!" holds before resolving.
  const RESOLVE_MS = 650;       // How long it takes to resolve into the match timer.
  const PARTICLES = 260;

  const clamp01 = function (t) {
    return t < 0 ? 0 : t > 1 ? 1 : t;
  };
  const progress = function (t, start, duration) {
    return clamp01((t - start) / duration);
  };
  const ease = {
    outExpo: function (t) {
      return t >= 1 ? 1 : 1 - Math.pow(2, -10 * t);
    },
    outCubic: function (t) {
      return 1 - Math.pow(1 - t, 3);
    },
    inCubic: function (t) {
      return t * t * t;
    },
    inOutSine: function (t) {
      return -(Math.cos(Math.PI * t) - 1) / 2;
    },
    outBack: function (t) {
      const s = 1.4;
      return 1 + (s + 1) * Math.pow(t - 1, 3) + s * Math.pow(t - 1, 2);
    },
  };

  const parseColor = function (hex) {
    const match = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(String(hex || ""));
    if (!match) {
      return [255, 255, 255];
    }
    return [parseInt(match[1], 16), parseInt(match[2], 16), parseInt(match[3], 16)];
  };
  const rgba = function (rgb, alpha) {
    return "rgba(" + rgb[0] + "," + rgb[1] + "," + rgb[2] + "," + alpha + ")";
  };

  // A small deterministic random number generator, so every display draws the same particle burst.
  const seededRandom = function (seed) {
    let state = seed >>> 0;
    return function () {
      state = (state + 0x6d2b79f5) >>> 0;
      let t = state;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  };

  // ------------------------------------------------------------------------------------------------------------
  // Audio

  const AudioEngine = function () {
    this.context = null;
    this.buffers = {};
    this.description = null;
    this.sources = [];
  };

  AudioEngine.prototype.ensureContext = function () {
    if (this.context === null) {
      const Context = window.AudioContext || window.webkitAudioContext;
      this.context = new Context({latencyHint: "interactive"});
    }
    if (this.context.state === "suspended") {
      this.context.resume();
    }
    return this.context;
  };

  AudioEngine.prototype.preload = function (baseUrl) {
    const that = this;
    const context = this.ensureContext();
    return fetch(baseUrl + "countdown.json").then(function (response) {
      return response.json();
    }).then(function (description) {
      that.description = description;
      const names = Object.keys(description.sounds);
      return Promise.all(names.map(function (name) {
        return fetch(baseUrl + description.sounds[name].file).then(function (response) {
          return response.arrayBuffer();
        }).then(function (data) {
          return context.decodeAudioData(data);
        }).then(function (buffer) {
          that.buffers[name] = buffer;
        });
      }));
    });
  };

  // Converts a time on the page's clock (performance.now() milliseconds) into a time on the audio clock, using the
  // audio hardware's own report of what it is playing now, so output latency is already accounted for.
  AudioEngine.prototype.contextTimeFor = function (performanceMs) {
    const context = this.context;
    if (context.getOutputTimestamp) {
      const stamp = context.getOutputTimestamp();
      if (stamp && stamp.performanceTime > 0) {
        return stamp.contextTime + (performanceMs - stamp.performanceTime) / 1000;
      }
    }
    return context.currentTime + (performanceMs - performance.now()) / 1000 - (context.outputLatency || 0);
  };

  AudioEngine.prototype.play = function (name, performanceMs, latencyMs) {
    const buffer = this.buffers[name];
    if (!buffer || this.context === null) {
      return;
    }
    const startAt = this.contextTimeFor(performanceMs + (latencyMs || 0));
    const now = this.context.currentTime;
    const late = now - startAt;
    if (late > 0.25) {
      return; // Too late to be in time; better silent than wrong.
    }
    const source = this.context.createBufferSource();
    source.buffer = buffer;
    source.connect(this.context.destination);
    if (late > 0) {
      source.start(now, late);
    } else {
      source.start(startAt);
    }
    this.sources.push(source);
  };

  AudioEngine.prototype.stopAll = function () {
    const now = this.context === null ? 0 : this.context.currentTime;
    this.sources.forEach(function (source) {
      try {
        source.stop(now);
      } catch (error) {
        // Already stopped.
      }
    });
    this.sources = [];
  };

  // ------------------------------------------------------------------------------------------------------------
  // The countdown

  const Countdown = function (container, options) {
    this.container = container;
    this.options = Object.assign({transparent: false, lite: "auto", maxPixelRatio: 1.5}, options || {});
    this.audio = new AudioEngine();
    this.spec = null;
    this.frameHandle = 0;
    this.frameTimes = [];
    this.renderTimes = [];
    this.frozenAt = null;
    this.lastFrame = 0;
    this.lite = this.options.lite === true;
    this.build();
    const that = this;
    this.resizeObserver = new ResizeObserver(function () {
      that.resize();
    });
    this.resizeObserver.observe(container);
    this.resize();
  };

  Countdown.prototype.build = function () {
    const root = document.createElement("div");
    root.className = "gc-root" + (this.options.transparent ? " gc-transparent" : "") + (this.lite ? " gc-lite" : "");
    root.innerHTML =
      "<div class='gc-dim'></div>" +
      "<div class='gc-stage'>" +
      "  <div class='gc-panel gc-panel-left'></div>" +
      "  <div class='gc-panel gc-panel-right'></div>" +
      "  <canvas class='gc-canvas'></canvas>" +
      "  <div class='gc-title'></div>" +
      "  <div class='gc-numeral-wrap'>" +
      "    <div class='gc-ghost'></div><div class='gc-ghost'></div><div class='gc-numeral'></div>" +
      "  </div>" +
      "</div>" +
      "<div class='gc-flash'></div>";
    this.container.appendChild(root);
    this.root = root;
    this.dim = root.querySelector(".gc-dim");
    this.stage = root.querySelector(".gc-stage");
    this.leftPanel = root.querySelector(".gc-panel-left");
    this.rightPanel = root.querySelector(".gc-panel-right");
    this.canvas = root.querySelector(".gc-canvas");
    this.context2d = this.canvas.getContext("2d");
    this.title = root.querySelector(".gc-title");
    this.numeral = root.querySelector(".gc-numeral");
    this.ghosts = root.querySelectorAll(".gc-ghost");
    this.flash = root.querySelector(".gc-flash");
  };

  Countdown.prototype.resize = function () {
    const rect = this.container.getBoundingClientRect();
    this.width = Math.max(1, rect.width);
    this.height = Math.max(1, rect.height);
    this.unit = Math.min(this.width, this.height) / 100;
    this.root.style.setProperty("--gc-unit", this.unit + "px");
    const ratio = Math.min(window.devicePixelRatio || 1, this.lite ? 1 : this.options.maxPixelRatio);
    this.pixelRatio = ratio;
    this.canvas.width = Math.round(this.width * ratio);
    this.canvas.height = Math.round(this.height * ratio);
  };

  Countdown.prototype.preloadAudio = function (baseUrl) {
    return this.audio.preload(baseUrl);
  };

  // Server time now, in milliseconds, from the page's monotonic clock.
  Countdown.prototype.serverNow = function () {
    return performance.timeOrigin + performance.now() + (this.spec ? this.spec.clockOffsetMs : 0);
  };

  // Converts a server time into the page's performance.now() clock.
  Countdown.prototype.toPerformance = function (serverMs) {
    return serverMs - (this.spec.clockOffsetMs || 0) - performance.timeOrigin;
  };

  // Starts the countdown. spec: {goAt, beats (default 3), intro (default true), clockOffsetMs (server minus local,
  // default 0), colors: [first alliance, second alliance], title, subtitle, audio (play sound on this display),
  // audioLatencyMs, resolveTarget (an element the "GO!" shrinks into), onGo, onDone}.
  Countdown.prototype.arm = function (spec) {
    this.cancel(true);
    this.spec = Object.assign({
      beats: 3, intro: true, clockOffsetMs: 0, colors: ["#d32f2f", "#1565c0"], title: "DRIVERS BEHIND THE LINES",
      subtitle: "", audio: false, audioLatencyMs: 0, resolveTarget: null, onGo: null, onDone: null,
    }, spec);
    const spec_ = this.spec;
    this.colors = spec_.colors.map(parseColor);
    this.root.style.setProperty("--gc-a", spec_.colors[0]);
    this.root.style.setProperty("--gc-b", spec_.colors[1]);
    this.root.style.setProperty("--gc-a-clear", rgba(this.colors[0], 0));
    this.root.style.setProperty("--gc-b-clear", rgba(this.colors[1], 0));
    this.root.style.setProperty("--gc-glow", rgba(this.mixedGlow(), 0.55));
    this.title.innerHTML = "";
    this.title.appendChild(document.createTextNode(spec_.title));
    if (spec_.subtitle) {
      const subtitle = document.createElement("span");
      subtitle.className = "gc-subtitle";
      subtitle.appendChild(document.createTextNode(spec_.subtitle));
      this.title.appendChild(subtitle);
    }
    this.firstBeat = -spec_.beats * BEAT_MS;
    this.introAt = spec_.intro ? this.firstBeat - INTRO_LEAD_MS : this.firstBeat - 400;
    this.goFired = false;
    this.cancelled = false;
    this.cancelledAt = 0;
    this.frozenAt = null;
    this.frameTimes = [];
    this.renderTimes = [];
    this.lastFrame = 0;
    this.makeParticles();
    this.root.style.opacity = "1";
    this.root.classList.add("gc-active");

    if (spec_.audio) {
      this.audio.ensureContext();
      const goPerformance = this.toPerformance(spec_.goAt);
      const latency = spec_.audioLatencyMs;
      if (spec_.intro) {
        this.audio.play("intro", goPerformance + this.introAt, latency);
      }
      for (let i = 0; i < spec_.beats; i++) {
        const number = String(spec_.beats - i);
        this.audio.play(this.audio.buffers[number] ? number : "1", goPerformance + this.firstBeat + i * BEAT_MS, latency);
      }
      if (-this.introAt >= 3000) {
        this.audio.play("riser", goPerformance - 3000, latency);
      }
      this.audio.play("go", goPerformance, latency);
    }

    const that = this;
    const frame = function (now) {
      that.frameHandle = requestAnimationFrame(frame);
      that.render(now);
    };
    this.frameHandle = requestAnimationFrame(frame);
  };

  // Stops at once: an abort or E-stop during the countdown. Fades out in a fifth of a second.
  Countdown.prototype.cancel = function (immediate) {
    if (this.spec === null) {
      return;
    }
    this.audio.stopAll();
    if (immediate) {
      this.finish();
      return;
    }
    if (!this.cancelled) {
      this.cancelled = true;
      this.cancelledAt = performance.now();
    }
  };

  Countdown.prototype.finish = function () {
    cancelAnimationFrame(this.frameHandle);
    this.frameHandle = 0;
    this.root.classList.remove("gc-active");
    const done = this.spec && this.spec.onDone;
    this.spec = null;
    if (done) {
      done();
    }
  };

  Countdown.prototype.isActive = function () {
    return this.spec !== null;
  };

  // Frame-time statistics for the last run, to measure whether the display holds 60 frames a second.
  Countdown.prototype.stats = function () {
    const times = this.frameTimes.slice().sort(function (a, b) {
      return a - b;
    });
    if (times.length === 0) {
      return {frames: 0};
    }
    const sum = times.reduce(function (a, b) {
      return a + b;
    }, 0);
    const renders = this.renderTimes.slice().sort(function (a, b) {
      return a - b;
    });
    return {
      renderP95Ms: renders.length > 0 ? renders[Math.floor(renders.length * 0.95)] : 0,
      renderWorstMs: renders.length > 0 ? renders[renders.length - 1] : 0,
      frames: times.length + 1,
      averageMs: sum / times.length,
      p95Ms: times[Math.floor(times.length * 0.95)],
      worstMs: times[times.length - 1],
      over20Ms: times.filter(function (t) {
        return t > 20;
      }).length,
      lite: this.lite,
    };
  };

  Countdown.prototype.mixedGlow = function () {
    const a = this.colors[0];
    const b = this.colors[1];
    return [Math.round((a[0] + b[0]) / 2), Math.round((a[1] + b[1]) / 2), Math.round((a[2] + b[2]) / 2)];
  };

  Countdown.prototype.makeParticles = function () {
    const random = seededRandom(Math.floor(this.spec.goAt) % 100000);
    this.particles = [];
    for (let i = 0; i < PARTICLES; i++) {
      const angle = random() * Math.PI * 2;
      const kind = random();
      this.particles.push({
        angle: angle,
        speed: 0.55 + random() * 1.25,    // In screen heights per second.
        drag: 2.2 + random() * 1.6,
        size: 0.18 + random() * 0.55,     // In units.
        life: 0.75 + random() * 0.75,     // Seconds.
        color: kind < 0.42 ? 0 : kind < 0.84 ? 1 : 2,
        twinkle: random() * Math.PI * 2,
      });
    }
  };

  // Downgrades to the light renderer when the first frames show the display cannot keep up.
  Countdown.prototype.checkPerformance = function () {
    if (this.options.lite !== "auto" || this.lite || this.frameTimes.length !== 30) {
      return;
    }
    const sorted = this.frameTimes.slice().sort(function (a, b) {
      return a - b;
    });
    if (sorted[15] > 22) {
      this.lite = true;
      this.root.classList.add("gc-lite");
      this.resize();
    }
  };

  // Shows one moment of an armed countdown and holds it there: t is milliseconds relative to "GO!". For previews,
  // screenshots and tests; the countdown does not move again until it is armed afresh.
  Countdown.prototype.freeze = function (t) {
    if (this.spec === null) {
      return;
    }
    cancelAnimationFrame(this.frameHandle);
    this.frameHandle = 0;
    this.frozenAt = t;
    this.render(performance.now());
  };

  // Draws one frame. t is milliseconds until "GO!" (negative before it).
  Countdown.prototype.render = function (now) {
    const renderStart = performance.now();
    if (this.lastFrame > 0 && this.frozenAt === null) {
      this.frameTimes.push(now - this.lastFrame);
      this.checkPerformance();
    }
    this.lastFrame = now;
    const spec = this.spec;
    const t = this.frozenAt !== null ? this.frozenAt : this.serverNow() - spec.goAt;
    this.drawFrame(t, now);
    this.renderTimes.push(performance.now() - renderStart);
  };

  Countdown.prototype.drawFrame = function (t, now) {
    const spec = this.spec;

    if (this.cancelled) {
      const fade = progress(now, this.cancelledAt, 200);
      this.root.style.opacity = String(1 - fade);
      if (fade >= 1) {
        this.root.style.opacity = "1";
        this.finish();
      }
      return;
    }
    if (t < this.introAt - 50) {
      return; // Armed early; nothing to show yet.
    }
    if (t >= 0 && !this.goFired && this.frozenAt === null) {
      this.goFired = true;
      if (spec.onGo) {
        spec.onGo();
      }
    }

    const endAt = GO_HOLD_MS + RESOLVE_MS;
    if (t >= endAt) {
      if (this.frozenAt === null) {
        this.finish();
      }
      return;
    }

    this.renderBackdrop(t);
    this.renderTitle(t);
    this.renderShake(t);
    this.renderNumeral(t);
    this.renderCanvas(t);
  };

  Countdown.prototype.renderBackdrop = function (t) {
    // Darken the scene as the intro begins, and clear it as "GO!" resolves.
    const fadeIn = ease.outCubic(progress(t, this.introAt, 500));
    const fadeOut = ease.inOutSine(progress(t, GO_HOLD_MS - 100, RESOLVE_MS));
    this.dim.style.opacity = String(fadeIn * (1 - fadeOut));

    // The alliance colours close in a step on every beat and meet on "GO!".
    const beats = this.spec.beats;
    let closeness = 0;
    for (let i = 0; i < beats; i++) {
      const beatAt = this.firstBeat + i * BEAT_MS;
      closeness += ease.outExpo(progress(t, beatAt, 420)) * (0.72 / beats);
    }
    closeness += ease.outExpo(progress(t, 0, 260)) * 0.28;
    const shift = (1 - closeness) * 100;
    const panelOpacity = ease.outCubic(progress(t, this.firstBeat - 300, 300)) * (1 - fadeOut);
    const goSurge = t >= 0 ? 1 + 0.06 * Math.exp(-t / 160) : 1;
    this.leftPanel.style.transform = "translate3d(" + (-shift) + "%,0,0) scaleY(" + goSurge + ")";
    this.rightPanel.style.transform = "translate3d(" + shift + "%,0,0) scaleY(" + goSurge + ")";
    this.leftPanel.style.opacity = String(panelOpacity * 0.85);
    this.rightPanel.style.opacity = String(panelOpacity * 0.85);

    // A white flash on "GO!".
    this.flash.style.opacity = String(t >= 0 ? 0.75 * Math.exp(-t / 110) : 0);
  };

  Countdown.prototype.renderTitle = function (t) {
    const appear = ease.outCubic(progress(t, this.introAt, 450));
    const leave = ease.inCubic(progress(t, this.firstBeat - 260, 220));
    const rise = (1 - appear) * 3 * this.unit;
    const spread = 0.92 + 0.08 * ease.outCubic(progress(t, this.introAt, 1800));
    this.title.style.opacity = String(appear * (1 - leave));
    this.title.style.transform = "translate3d(0," + rise + "px,0) scale(" + spread * (1 + 0.15 * leave) + ")";
  };

  // A camera pulse on every beat: a short damped shake and a breath of scale.
  Countdown.prototype.renderShake = function (t) {
    let x = 0;
    let y = 0;
    let scale = 1;
    const impulses = [];
    for (let i = 0; i < this.spec.beats; i++) {
      impulses.push({at: this.firstBeat + i * BEAT_MS, strength: 0.45 + 0.15 * i});
    }
    impulses.push({at: 0, strength: 1.4});
    impulses.forEach(function (impulse, index) {
      const dt = t - impulse.at;
      if (dt < 0 || dt > 600) {
        return;
      }
      const decay = Math.exp(-dt / 90);
      const phase = index * 1.7;
      x += impulse.strength * decay * Math.sin(dt / 1000 * 2 * Math.PI * 17 + phase);
      y += impulse.strength * decay * Math.cos(dt / 1000 * 2 * Math.PI * 13 + phase);
      scale += 0.018 * impulse.strength * Math.exp(-dt / 140);
    });
    const unit = this.unit;
    this.stage.style.transform = "translate3d(" + x * unit + "px," + y * unit + "px,0) scale(" + scale + ")";
  };

  // The numerals slam in from large and blurred, hold, then fall away; "GO!" slams harder and then resolves.
  Countdown.prototype.renderNumeral = function (t) {
    const beats = this.spec.beats;
    let text = "";
    let scale = 1;
    let opacity = 0;
    let trail = 0;
    let translateX = 0;
    let translateY = 0;
    if (t >= this.firstBeat && t < 0) {
      const index = Math.min(beats - 1, Math.floor((t - this.firstBeat) / BEAT_MS));
      const local = t - (this.firstBeat + index * BEAT_MS);
      text = String(beats - index);
      const slam = ease.outExpo(progress(local, 0, 190));
      const settle = ease.inOutSine(progress(local, 190, 640));
      const exit = ease.inCubic(progress(local, 830, 170));
      scale = (2.7 - 1.7 * slam) * (1 - 0.06 * settle) * (1 - 0.4 * exit);
      opacity = Math.min(1, local / 60) * (1 - exit);
      trail = 1 - slam;
    } else if (t >= 0) {
      text = "GO!";
      const slam = ease.outBack(progress(t, 0, 230));
      scale = 3.2 - 2.2 * slam;
      opacity = Math.min(1, t / 40);
      trail = 1 - clamp01(t / 230);
      const resolveProgress = progress(t, GO_HOLD_MS, RESOLVE_MS);
      if (resolveProgress > 0) {
        const travel = ease.outCubic(resolveProgress);
        const target = this.resolveTargetRect();
        if (target !== null) {
          translateX = (target.x - this.width / 2) * travel;
          translateY = (target.y - this.height / 2) * travel;
          scale *= 1 - (1 - target.scale) * travel;
        } else {
          scale *= 1 + 0.4 * travel;
        }
        opacity *= 1 - ease.inCubic(resolveProgress);
      }
    }
    if (this.numeral.textContent !== text) {
      this.numeral.textContent = text;
      for (let i = 0; i < this.ghosts.length; i++) {
        this.ghosts[i].textContent = text;
      }
    }
    const base = "translate(-50%,-50%) translate3d(" + translateX + "px," + translateY + "px,0) ";
    this.numeral.style.transform = base + "scale(" + scale + ")";
    this.numeral.style.opacity = String(opacity);
    if (!this.lite) {
      for (let i = 0; i < this.ghosts.length; i++) {
        const lag = (i + 1) * 0.35 * trail;
        this.ghosts[i].style.transform = base + "scale(" + scale * (1 + lag) + ")";
        this.ghosts[i].style.opacity = String(opacity * trail * (0.45 - 0.15 * i));
      }
    }
  };

  // Where the "GO!" shrinks to: the centre of the match timer, relative to the centre of the countdown.
  Countdown.prototype.resolveTargetRect = function () {
    const target = this.spec.resolveTarget;
    if (!target) {
      return null;
    }
    const container = this.container.getBoundingClientRect();
    const rect = target.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) {
      return null;
    }
    const numeralHeight = this.unit * 40;
    return {
      x: rect.left + rect.width / 2 - container.left,
      y: rect.top + rect.height / 2 - container.top,
      scale: Math.max(0.05, rect.height / numeralHeight),
    };
  };

  Countdown.prototype.renderCanvas = function (t) {
    const ctx = this.context2d;
    const ratio = this.pixelRatio;
    const width = this.width;
    const height = this.height;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);
    ctx.globalCompositeOperation = "lighter";
    const cx = width / 2;
    const cy = height / 2;
    const unit = this.unit;
    const a = this.colors[0];
    const b = this.colors[1];

    // The ring: a track, and an arc that sweeps from full to empty through each beat.
    const ringIn = ease.outCubic(progress(t, this.firstBeat - 350, 350));
    const ringOut = t >= 0 ? ease.outCubic(progress(t, 0, 180)) : 0;
    if (ringIn > 0 && ringOut < 1) {
      let pulse = 1;
      for (let i = 0; i < this.spec.beats; i++) {
        const dt = t - (this.firstBeat + i * BEAT_MS);
        if (dt >= 0 && dt < 400) {
          pulse += 0.06 * Math.exp(-dt / 110);
        }
      }
      const radius = 31 * unit * pulse * (0.85 + 0.15 * ringIn);
      const alpha = ringIn * (1 - ringOut);
      ctx.lineCap = "round";
      ctx.lineWidth = 0.9 * unit;
      ctx.strokeStyle = "rgba(255,255,255," + 0.12 * alpha + ")";
      ctx.beginPath();
      ctx.arc(cx, cy, radius, 0, Math.PI * 2);
      ctx.stroke();

      let remaining = 1;
      if (t >= this.firstBeat && t < 0) {
        const local = (t - this.firstBeat) % BEAT_MS;
        remaining = 1 - ease.inOutSine(local / BEAT_MS);
      } else if (t >= 0) {
        remaining = 0;
      }
      if (remaining > 0.001) {
        // The arc empties clockwise, like a clock hand sweeping round from twelve o'clock.
        const end = -Math.PI / 2 + Math.PI * 2;
        const start = end - remaining * Math.PI * 2;
        const gradient = ctx.createLinearGradient(cx - radius, cy, cx + radius, cy);
        gradient.addColorStop(0, rgba(a, alpha));
        gradient.addColorStop(1, rgba(b, alpha));
        // Glow is drawn as wider, fainter strokes underneath: much cheaper than shadowBlur.
        if (!this.lite) {
          [[4.2, 0.12], [2.4, 0.22]].forEach(function (layer) {
            ctx.lineWidth = layer[0] * unit;
            ctx.strokeStyle = gradient;
            ctx.globalAlpha = layer[1];
            ctx.beginPath();
            ctx.arc(cx, cy, radius, start, end);
            ctx.stroke();
          });
          ctx.globalAlpha = 1;
        }
        ctx.lineWidth = 1.2 * unit;
        ctx.strokeStyle = gradient;
        ctx.beginPath();
        ctx.arc(cx, cy, radius, start, end);
        ctx.stroke();
        // A bright head on the sweeping end of the arc.
        ctx.fillStyle = "rgba(255,255,255," + 0.9 * alpha + ")";
        ctx.beginPath();
        ctx.arc(cx + radius * Math.cos(start), cy + radius * Math.sin(start), 0.9 * unit, 0, Math.PI * 2);
        ctx.fill();
      }
    }

    // "GO!": two shockwaves in the alliance colours, and the particle burst.
    if (t >= 0) {
      const diagonal = Math.sqrt(width * width + height * height) / 2;
      [[a, 0, 2.4], [b, 70, 2.4], [[255, 255, 255], 30, 1.2]].forEach(function (wave) {
        const p = progress(t, wave[1], 760);
        if (p <= 0 || p >= 1) {
          return;
        }
        const radius = ease.outCubic(p) * diagonal * 1.1;
        ctx.lineWidth = (wave[2] * (1 - p) + 0.25) * unit;
        ctx.strokeStyle = rgba(wave[0], 0.7 * Math.pow(1 - p, 1.6));
        ctx.beginPath();
        ctx.arc(cx, cy, radius, 0, Math.PI * 2);
        ctx.stroke();
      });
      if (!this.lite) {
        this.renderParticles(ctx, t / 1000, cx, cy);
      }
    }
    ctx.globalCompositeOperation = "source-over";
  };

  // Particle positions are closed-form functions of time (velocity under exponential drag), so the burst looks
  // the same at any frame rate and on every display.
  Countdown.prototype.renderParticles = function (ctx, seconds, cx, cy) {
    const palette = [this.colors[0], this.colors[1], [255, 255, 255]];
    const scale = this.height;
    const unit = this.unit;
    for (let i = 0; i < this.particles.length; i++) {
      const particle = this.particles[i];
      if (seconds > particle.life) {
        continue;
      }
      const distance = particle.speed / particle.drag * (1 - Math.exp(-particle.drag * seconds)) * scale;
      const x = cx + Math.cos(particle.angle) * distance;
      const y = cy + Math.sin(particle.angle) * distance;
      const fade = 1 - seconds / particle.life;
      const flicker = 0.75 + 0.25 * Math.sin(particle.twinkle + seconds * 40);
      ctx.fillStyle = rgba(palette[particle.color], fade * flicker);
      const size = particle.size * unit * (0.6 + 0.4 * fade);
      ctx.beginPath();
      ctx.arc(x, y, size, 0, Math.PI * 2);
      ctx.fill();
    }
  };

  return {
    create: function (container, options) {
      return new Countdown(container, options);
    },
    beatMs: BEAT_MS,
    introLeadMs: INTRO_LEAD_MS,
    // How long before "GO!" the countdown first shows something, for a given number of beats.
    leadMs: function (beats, intro) {
      return beats * BEAT_MS + (intro === false ? 400 : INTRO_LEAD_MS);
    },
  };
})();
