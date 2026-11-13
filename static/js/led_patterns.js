// Draws LED strip commands exactly as devices do, for previews in the browser: the Lighting page's simulated
// strips and the virtual devices. The patterns are defined in docs/devices.md ("Pattern definitions"); keep the two
// in step.
//
// LedPatterns.render(command, pixels, t) returns an array of pixels colours, each [r, g, b] from 0 to 255, for an
// output command such as {"p": "blink", "c": ["#ff0000"], "ms": 500, "br": 0.6} at field time t (milliseconds).

const LedPatterns = (function () {
  const DEFAULT_PERIODS = {blink: 500, pulse: 1500, chase: 1000, rainbow: 3000};
  const BLACK = [0, 0, 0];

  const parseColor = function (hex) {
    const match = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(String(hex || ""));
    if (!match) {
      return BLACK;
    }
    return [parseInt(match[1], 16), parseInt(match[2], 16), parseInt(match[3], 16)];
  };

  const mod = function (a, b) {
    return ((a % b) + b) % b;
  };

  const mix = function (from, to, k) {
    return [from[0] + (to[0] - from[0]) * k, from[1] + (to[1] - from[1]) * k, from[2] + (to[2] - from[2]) * k];
  };

  const hueToRgb = function (hue) {
    const h = mod(hue, 1) * 6;
    const x = 1 - Math.abs(mod(h, 2) - 1);
    const sector = Math.floor(h);
    const table = [[1, x, 0], [x, 1, 0], [0, 1, x], [0, x, 1], [x, 0, 1], [1, 0, x]];
    return table[sector % 6].map(function (value) {
      return value * 255;
    });
  };

  // Fills pixels [start, start + count) of out with one pattern.
  const drawPattern = function (out, start, count, spec, t) {
    const pattern = spec.p || "off";
    const colors = spec.c || [];
    const a = parseColor(colors[0]);
    const b = colors.length > 1 ? parseColor(colors[1]) : BLACK;
    const period = spec.ms > 0 ? spec.ms : (DEFAULT_PERIODS[pattern] || 1000);
    const phase = mod(t, period);
    for (let i = 0; i < count; i++) {
      let color = BLACK;
      switch (pattern) {
        case "solid":
          color = a;
          break;
        case "blink":
          color = phase < period / 2 ? a : b;
          break;
        case "pulse":
          color = mix(b, a, (1 - Math.cos(2 * Math.PI * phase / period)) / 2);
          break;
        case "chase": {
          const head = Math.floor(phase / period * count);
          color = mod(i - head, count) < Math.max(1, Math.floor(count / 8)) ? a : b;
          break;
        }
        case "rainbow":
          color = hueToRgb(i / count + phase / period);
          break;
        case "progress": {
          const value = Math.max(0, Math.min(1, Number(spec.v) || 0));
          color = i < Math.round(value * count) ? a : b;
          break;
        }
        default:
          color = BLACK;
      }
      out[start + i] = color;
    }
  };

  const render = function (command, pixels, t) {
    const out = [];
    for (let i = 0; i < pixels; i++) {
      out.push(BLACK);
    }
    if (!command) {
      return out;
    }
    if (Array.isArray(command.seg) && command.seg.length > 0) {
      command.seg.forEach(function (segment) {
        const start = Math.max(0, Math.min(pixels, segment.s || 0));
        const count = Math.max(0, Math.min(pixels - start, segment.n || 0));
        if (count > 0) {
          drawPattern(out, start, count, segment, t);
        }
      });
    } else {
      drawPattern(out, 0, pixels, command, t);
    }
    const brightness = command.br === undefined || command.br === null ? 1 : Math.max(0, Math.min(1, command.br));
    return out.map(function (color) {
      return [
        Math.round(color[0] * brightness), Math.round(color[1] * brightness), Math.round(color[2] * brightness),
      ];
    });
  };

  // The colour a digital, relay or pwm output shows for a command: on, off, or a pwm level, as one swatch.
  const renderSingle = function (command, t) {
    if (!command) {
      return BLACK;
    }
    if (command.p !== undefined) {
      return render(command, 1, t)[0];
    }
    const value = Math.max(0, Math.min(1, Number(command.v) || 0));
    return [Math.round(255 * value), Math.round(255 * value), Math.round(255 * value)];
  };

  return {render: render, renderSingle: renderSingle, defaultPeriods: DEFAULT_PERIODS};
})();

if (typeof module !== "undefined") {
  module.exports = LedPatterns;
}
