"""LED strip patterns, as defined in docs/devices.md ("Pattern definitions").

This follows the FMS's reference implementation, static/js/led_patterns.js, operation for operation and in double
precision, so a strip driven from Python shows exactly what the Lighting page's preview shows. Every SDK's tests
check against sdk/testdata/led_cases.json, which is rendered by that reference.
"""

import math

BLACK = (0, 0, 0)
DEFAULT_PERIODS = {"blink": 500, "pulse": 1500, "chase": 1000, "rainbow": 3000}
ANIMATED = ("blink", "pulse", "chase", "rainbow")


def _is_number(value):
    return isinstance(value, (int, float)) and not (isinstance(value, float) and math.isnan(value))


def js_round(x):
    """JavaScript's Math.round: the nearest integer, halves towards +infinity (Python's round() rounds halves to
    even, and floor(x + 0.5) is wrong for 0.49999999999999994)."""
    whole = math.floor(x)
    return int(whole + 1) if x - whole >= 0.5 else int(whole)


def js_mod(a, b):
    """The reference's mod(a, b) = ((a % b) + b) % b, with JavaScript's % (the sign of the dividend)."""
    return math.fmod(math.fmod(a, b) + b, b)


def parse_color(text):
    """"#rrggbb" (or "rrggbb", any case) to an (r, g, b) tuple; anything else is black."""
    if not isinstance(text, str):
        return BLACK
    if text.startswith("#"):
        text = text[1:]
    if len(text) != 6 or not all(c in "0123456789abcdefABCDEF" for c in text):
        return BLACK
    value = int(text, 16)
    return ((value >> 16) & 0xFF, (value >> 8) & 0xFF, value & 0xFF)


def _hue_to_rgb(hue):
    h = js_mod(hue, 1) * 6
    x = 1 - abs(js_mod(h, 2) - 1)
    sector = int(math.floor(h)) % 6
    table = ((1, x, 0), (x, 1, 0), (0, 1, x), (0, x, 1), (x, 0, 1), (1, 0, x))
    return tuple(value * 255 for value in table[sector])


def _mix(start, end, k):
    return tuple(start[n] + (end[n] - start[n]) * k for n in range(3))


def _draw(out, start, count, spec, t):
    """Fills out[start:start + count] with one pattern (drawPattern in the reference). Colours stay unrounded."""
    pattern = spec.get("p") or "off"
    colors = spec.get("c")
    if not isinstance(colors, list):
        colors = []
    a = parse_color(colors[0]) if len(colors) > 0 else BLACK
    b = parse_color(colors[1]) if len(colors) > 1 else BLACK
    ms = spec.get("ms")
    period = float(ms) if _is_number(ms) and not isinstance(ms, bool) and ms > 0 else \
        float(DEFAULT_PERIODS.get(pattern, 1000))
    phase = js_mod(float(t), period)
    for i in range(count):
        if pattern == "solid":
            color = a
        elif pattern == "blink":
            color = a if phase < period / 2 else b
        elif pattern == "pulse":
            color = _mix(b, a, (1 - math.cos(2 * math.pi * phase / period)) / 2)
        elif pattern == "chase":
            head = math.floor(phase / period * count)
            color = a if js_mod(i - head, count) < max(1, math.floor(count / 8)) else b
        elif pattern == "rainbow":
            color = _hue_to_rgb(i / count + phase / period)
        elif pattern == "progress":
            v = spec.get("v")
            value = float(v) if _is_number(v) else 0.0
            value = max(0.0, min(1.0, value))
            color = a if i < js_round(value * count) else b
        else:
            color = BLACK
        out[start + i] = color


def _whole(value):
    return int(value) if _is_number(value) else 0


def render(command, pixels, t, limit=1.0):
    """The colours of `pixels` LEDs for an led_strip command at field time `t` (UTC milliseconds): a list of
    (r, g, b) tuples. `limit` is this SDK's own brightness cap (1 means none); it multiplies `br`."""
    out = [BLACK] * pixels
    if not isinstance(command, dict):
        return out
    segments = command.get("seg")
    if isinstance(segments, list) and len(segments) > 0:
        for segment in segments:
            if not isinstance(segment, dict):
                continue
            start = max(0, min(pixels, _whole(segment.get("s"))))
            count = max(0, min(pixels - start, _whole(segment.get("n"))))
            if count > 0:
                _draw(out, start, count, segment, t)
    else:
        _draw(out, 0, pixels, command, t)
    br = command.get("br")
    brightness = 1.0 if br is None else max(0.0, min(1.0, float(br) if _is_number(br) else 1.0))
    if limit != 1.0:
        brightness = brightness * max(0.0, min(1.0, limit))
    return [(js_round(c[0] * brightness), js_round(c[1] * brightness), js_round(c[2] * brightness)) for c in out]


def animated(command):
    """Whether the command changes with time (so the strip has to be redrawn as the clock runs)."""
    if not isinstance(command, dict):
        return False
    segments = command.get("seg")
    if isinstance(segments, list) and len(segments) > 0:
        return any(isinstance(s, dict) and s.get("p") in ANIMATED for s in segments)
    return command.get("p") in ANIMATED
