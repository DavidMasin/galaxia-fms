# Galaxia FMS device SDK for MicroPython (Raspberry Pi Pico W and Pico 2 W).
# MIT licence, copyright Galaxia FMS contributors. See sdk/README.md for the protocol and the shared behaviour.
#
#   import galaxia
#   dev = galaxia.Device("beam-red-1", "Red goal beam break")
#   balls = dev.counter_pin("count", 27)
#   dev.wifi("field-devices", "wifi-password")
#   dev.connect("10.0.100.5", "device-token")
#   dev.run()

import asyncio
import gc
import json
import math
import sys
import time

import network
from machine import PWM, Pin, reset
from mqtt_as import MQTTClient as _MQTTClient
from mqtt_as import config as _mqtt_defaults

VERSION = "0.1.0"
SDK = "galaxia-micropython/" + VERSION
PREFIX = "galaxia/v1/"
KEEPALIVE_S = 15
STATUS_MS = 5000
HEARTBEAT_MS = 200
PROBE_AFTER_DROP_MS = 30000
REFUSED_RETRY_MS = 30000
RESERVED_IDS = ("plc", "inline")
IDENTIFY_MS = 5000
RENDER_MS = 20
SCHEDULE_SIZE = 8
OUTBOX_SIZE = 32
BLACK = (0, 0, 0)
DEFAULT_PERIODS = {"blink": 500, "pulse": 1500, "chase": 1000, "rainbow": 3000}
ANIMATED = ("blink", "pulse", "chase", "rainbow")
OFFLINE = b'{"online":false}'


# Hardware and platform access, kept together so the logic can be tested on a PC with stand-ins.

_ticks_ms = getattr(time, "ticks_ms", None) or (lambda: int(time.monotonic() * 1000))
_ticks_diff = getattr(time, "ticks_diff", None) or (lambda a, b: a - b)


async def _sleep_ms(ms):
    sleep_ms = getattr(asyncio, "sleep_ms", None)
    if sleep_ms is not None:
        await sleep_ms(ms)
    else:
        await asyncio.sleep(ms / 1000)


def _pull(pull):
    if pull == "up":
        return Pin.PULL_UP
    if pull == "down":
        return Pin.PULL_DOWN
    return None


def _input_pin(pin, pull):
    mode = _pull(pull)
    return Pin(pin, Pin.IN, mode) if mode is not None else Pin(pin, Pin.IN)


def _neopixel(pin, pixels):
    import neopixel

    return neopixel.NeoPixel(Pin(pin, Pin.OUT), pixels)


def _wlan_info():
    info = {}
    try:
        wlan = network.WLAN(network.STA_IF)
        info["rssi"] = wlan.status("rssi")
        try:
            info["ip"] = wlan.ipconfig("addr4")[0]
        except (AttributeError, OSError, ValueError):
            info["ip"] = wlan.ifconfig()[0]
    except (AttributeError, OSError, ValueError, TypeError):
        pass
    return info


def _free_heap():
    mem_free = getattr(gc, "mem_free", None)
    return mem_free() if mem_free else None


def _board():
    return getattr(sys.implementation, "_machine", sys.platform)


def _dumps(payload):
    try:
        return json.dumps(payload, separators=(",", ":")).encode()
    except TypeError:
        return json.dumps(payload).encode()


class _Monotonic:
    """Milliseconds since start, without the wrap-around of ticks_ms."""

    def __init__(self):
        self._last = _ticks_ms()
        self._ms = 0

    def __call__(self):
        now = _ticks_ms()
        self._ms += _ticks_diff(now, self._last)
        self._last = now
        return self._ms


# LED strip patterns, as defined in docs/devices.md ("Pattern definitions"), following the FMS's reference
# implementation (static/js/led_patterns.js) step for step. The Pico's floats are single precision, so at the rare
# instants where a value lands exactly on a rounding boundary a colour channel can differ by one from the preview.


def _is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and value == value


def _number(value, default=None):
    if isinstance(value, bool):
        return 1 if value else 0
    if isinstance(value, (int, float)):
        if isinstance(value, float) and value != value:
            return default
        return value
    return default


# On a board whose floats are single precision (the Pico), a fill or brightness that the reference's doubles put
# exactly on a half (0.35 x 10 = 3.5) can come out just below it. Counting from 0.4999 rounds those up as the
# reference does; for fills and brightness given to a thousandth, as the FMS sends them, nothing else is that close
# to a half. Where floats are doubles (tests on a PC) the exact rule applies.
_ROUND_HALF = 0.4999 if 1.0 + 1e-10 == 1.0 else 0.5


def js_round(x):
    """JavaScript's Math.round: halves go towards +infinity."""
    whole = math.floor(x)
    return whole + 1 if x - whole >= _ROUND_HALF else whole


def js_mod(a, b):
    """The reference's ((a % b) + b) % b. Exact for whole numbers, which keeps the field clock exact."""
    if isinstance(a, int) and isinstance(b, int):
        return a % b
    return math.fmod(math.fmod(a, b) + b, b)


def parse_color(text):
    if not isinstance(text, str):
        return BLACK
    if text.startswith("#"):
        text = text[1:]
    if len(text) != 6:
        return BLACK
    for char in text:
        if char not in "0123456789abcdefABCDEF":
            return BLACK
    value = int(text, 16)
    return ((value >> 16) & 0xFF, (value >> 8) & 0xFF, value & 0xFF)


def _hue_to_rgb(hue):
    h = js_mod(hue, 1) * 6
    x = (1 - abs(js_mod(h, 2) - 1)) * 255
    sector = math.floor(h) % 6
    if sector == 0:
        return (255, x, 0)
    if sector == 1:
        return (x, 255, 0)
    if sector == 2:
        return (0, 255, x)
    if sector == 3:
        return (0, x, 255)
    if sector == 4:
        return (x, 0, 255)
    return (255, 0, x)


def _draw(out, start, count, spec, t):
    pattern = spec.get("p") or "off"
    colors = spec.get("c")
    if not isinstance(colors, list):
        colors = []
    a = parse_color(colors[0]) if len(colors) > 0 else BLACK
    b = parse_color(colors[1]) if len(colors) > 1 else BLACK
    ms = spec.get("ms")
    period = ms if _is_number(ms) and ms > 0 else DEFAULT_PERIODS.get(pattern, 1000)
    phase = js_mod(t, period)
    if pattern == "chase":
        head = math.floor(phase / period * count)
        block = max(1, count // 8)
        for i in range(count):
            out[start + i] = a if js_mod(i - head, count) < block else b
        return
    if pattern == "rainbow":
        for i in range(count):
            out[start + i] = _hue_to_rgb(i / count + phase / period)
        return
    if pattern == "solid":
        color = a
    elif pattern == "blink":
        color = a if phase < period / 2 else b
    elif pattern == "pulse":
        k = (1 - math.cos(2 * math.pi * phase / period)) / 2
        color = (b[0] + (a[0] - b[0]) * k, b[1] + (a[1] - b[1]) * k, b[2] + (a[2] - b[2]) * k)
    elif pattern == "progress":
        value = _number(spec.get("v"), 0)
        value = 0 if value < 0 else 1 if value > 1 else value
        lit = js_round(value * count)
        for i in range(count):
            out[start + i] = a if i < lit else b
        return
    else:
        color = BLACK
    for i in range(count):
        out[start + i] = color


def _whole(value):
    return int(value) if _is_number(value) else 0


def render(command, pixels, t, limit=1.0):
    """The colours of `pixels` LEDs for an led_strip command at field time `t` (milliseconds): (r, g, b) tuples.
    `limit` is this device's own brightness cap; it multiplies `br`."""
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
    brightness = 1.0 if br is None else max(0.0, min(1.0, float(_number(br, 1))))
    if limit != 1.0:
        brightness = brightness * max(0.0, min(1.0, limit))
    return [(js_round(c[0] * brightness), js_round(c[1] * brightness), js_round(c[2] * brightness)) for c in out]


def animated(command):
    if not isinstance(command, dict):
        return False
    segments = command.get("seg")
    if isinstance(segments, list) and len(segments) > 0:
        for segment in segments:
            if isinstance(segment, dict) and segment.get("p") in ANIMATED:
                return True
        return False
    return command.get("p") in ANIMATED


# Field clock and scheduled commands (the same rules as the Python SDK's galaxia.timing).


class FieldClock:
    WINDOW = 8
    JUMP_MS = 1000
    OUTLIERS = 3

    def __init__(self, monotonic_ms):
        self._mono = monotonic_ms
        self._samples = []
        self._outliers = []
        self.offset = None

    def sample(self, field_ms):
        offset = int(field_ms) - self._mono()
        if self.offset is not None and abs(offset - self.offset) > self.JUMP_MS:
            self._outliers.append(offset)
            if len(self._outliers) < self.OUTLIERS:
                return
            self._samples = self._outliers
            self._outliers = []
        else:
            self._outliers = []
            self._samples.append(offset)
        self._samples = self._samples[-self.WINDOW:]
        self.offset = max(self._samples)

    def now(self):
        if self.offset is None:
            return None
        return self._mono() + self.offset


class Schedule:
    def __init__(self, size):
        self.size = size
        self.pending = []

    def offer(self, at, now, command):
        if at is None or now is None or at <= now:
            self.pending = []
            return True
        self.pending = [entry for entry in self.pending if entry[0] < at]
        self.pending.append((at, command))
        if len(self.pending) > self.size:
            self.pending.pop(0)
        return False

    def due(self, now):
        if now is None:
            ready, self.pending = self.pending, []
            return ready
        count = 0
        while count < len(self.pending) and self.pending[count][0] <= now:
            count += 1
        ready = self.pending[:count]
        self.pending = self.pending[count:]
        return ready


def _call(callback, *args):
    if callback is None:
        return
    try:
        callback(*args)
    except Exception as error:  # user code must not stop the device
        print("galaxia: error in a callback:", repr(error))


# Channels


class Channel:
    direction = "in"
    type = ""

    def __init__(self, device, channel_id, name=None, extra=None):
        self._device = device
        self.id = channel_id
        self.name = name
        self._extra = {}
        for key, value in (extra or {}).items():
            if value is not None:
                self._extra[key] = value

    def manifest(self):
        entry = {"id": self.id, "dir": self.direction, "type": self.type}
        if self.name:
            entry["name"] = self.name
        entry.update(self._extra)
        return entry

    def tick(self, now, field_ms):
        pass


class Input(Channel):
    republish = True

    def __init__(self, device, channel_id, name=None, extra=None):
        Channel.__init__(self, device, channel_id, name, extra)
        self.value = None

    def _send(self, value, conf=None):
        self.value = value
        self._device._send_input(self, value, conf)

    def _republish(self):
        if self.republish and self.value is not None:
            self._device._send_input(self, self.value, None)


class Counter(Input):
    type = "counter"

    def __init__(self, device, channel_id, name=None):
        Input.__init__(self, device, channel_id, name)
        self.value = 0
        self._isr_total = 0

    @property
    def total(self):
        return self.value

    def increment(self, n=1, conf=None):
        if n < 0:
            raise ValueError("a counter only goes up")
        if n:
            self._isr_total += n
            self._send(self.value + n, conf)


class CounterPin(Counter):
    """Counts edges on a pin in an interrupt, so no edge is lost while the network is busy."""

    def __init__(self, device, channel_id, pin, name=None, pull="up", edge="falling", debounce_ms=2):
        Counter.__init__(self, device, channel_id, name)
        self._debounce = debounce_ms
        self._last_edge = _ticks_ms()
        self._pin = _input_pin(pin, pull)
        trigger = Pin.IRQ_FALLING if edge == "falling" else Pin.IRQ_RISING
        if edge == "both":
            trigger = Pin.IRQ_FALLING | Pin.IRQ_RISING
        self._pin.irq(handler=self._edge, trigger=trigger)

    def _edge(self, _pin):
        now = _ticks_ms()
        if _ticks_diff(now, self._last_edge) >= self._debounce:
            self._isr_total += 1
        self._last_edge = now

    def tick(self, now, field_ms):
        total = self._isr_total
        if total != self.value:
            self._send(total)


class Digital(Input):
    type = "digital"

    def __init__(self, device, channel_id, name=None, debounce_ms=0, invert=False, extra=None):
        Input.__init__(self, device, channel_id, name, extra)
        self.debounce_ms = debounce_ms
        self.invert = invert
        self._candidate = None
        self._since = 0
        self._pin = None

    def set(self, value, conf=None):
        state = 1 if value else 0
        if self.invert:
            state ^= 1
        if self.debounce_ms <= 0:
            if state != self.value:
                self._send(state, conf)
            return
        now = self._device._now()
        if state != self._candidate:
            self._candidate = state
            self._since = now
        self._promote(now)

    def _promote(self, now):
        if self._candidate is not None and self._candidate != self.value:
            if now - self._since >= self.debounce_ms:
                self._send(self._candidate)

    def tick(self, now, field_ms):
        if self._pin is not None:
            self.set(self._pin.value())
        else:
            self._promote(now)


class SafetyStop(Input):
    type = "digital"

    def __init__(self, device, channel_id, name=None, recover_ms=50):
        Input.__init__(self, device, channel_id, name, {"safety": True})
        self.recover_ms = recover_ms
        self.value = 0
        self._candidate = None
        self._since = 0
        self._pin = None
        self._invert = False

    @property
    def safe(self):
        return self.value == 1

    def set(self, safe):
        now = self._device._now()
        if not safe:
            self._candidate = None
            if self.value != 0:
                self._send(0)
                self._device._heartbeat_now()
            return
        if self.value == 1:
            self._candidate = None
            return
        if self._candidate != 1:
            self._candidate = 1
            self._since = now
        self._promote(now)

    def _promote(self, now):
        if self._candidate == 1 and now - self._since >= self.recover_ms:
            self._candidate = None
            self._send(1)
            self._device._heartbeat_now()

    def tick(self, now, field_ms):
        if self._pin is not None:
            # With the pull-up, a closed contact to ground reads 0: that is safe.
            closed = self._pin.value() == 0
            self.set(closed != self._invert)
        else:
            self._promote(now)


class Analog(Input):
    type = "analog"

    def __init__(self, device, channel_id, name=None, unit=None, min=None, max=None, deadband=0, min_interval_ms=100):
        Input.__init__(self, device, channel_id, name, {"unit": unit, "min": min, "max": max})
        self.deadband = deadband
        self.min_interval_ms = min_interval_ms
        self._pending = None
        self._last_sent = None

    def set(self, value, conf=None):
        number = _number(value)
        if number is None:
            raise ValueError("an analog value must be a number")
        self._pending = (number, conf)
        self.tick(self._device._now(), None)

    def tick(self, now, field_ms):
        if self._pending is None:
            return
        value, conf = self._pending
        if self.value is not None:
            changed = abs(value - self.value) > self.deadband if self.deadband > 0 else value != self.value
            if not changed:
                self._pending = None
                return
        if self._last_sent is not None and now - self._last_sent < self.min_interval_ms:
            return
        self._pending = None
        self._last_sent = now
        self._send(value, conf)


class Encoder(Analog):
    type = "encoder"

    def __init__(self, device, channel_id, name=None, min_interval_ms=50):
        Analog.__init__(self, device, channel_id, name, min_interval_ms=min_interval_ms)

    def set(self, value, conf=None):
        Analog.set(self, int(value), conf)


class Text(Input):
    type = "text"
    republish = False

    def set(self, text, conf=None):
        self._send(str(text), conf)


class Output(Channel):
    direction = "out"
    ignore_retained = False

    def __init__(self, device, channel_id, name=None, extra=None):
        Channel.__init__(self, device, channel_id, name, extra)
        self._schedule = Schedule(SCHEDULE_SIZE)

    def handle(self, payload, retained, field_ms):
        if payload is None:
            self._schedule.pending = []
            self.apply(None, None)
            return
        if retained and self.ignore_retained:
            return
        at = _number(payload.get("at"))
        at = int(at) if at is not None else None
        if self._schedule.offer(at, field_ms, payload):
            self.apply(payload, at)

    def tick(self, now, field_ms):
        for at, payload in self._schedule.due(field_ms):
            self.apply(payload, at)

    def apply(self, payload, at):
        pass


class DigitalOut(Output):
    def __init__(self, device, channel_id, pin=None, name=None, on_change=None, active_low=False, relay=False):
        Output.__init__(self, device, channel_id, name)
        self.type = "relay" if relay else "digital"
        self.on_change = on_change
        self.active_low = active_low
        self.value = None
        self._pin = Pin(pin, Pin.OUT, value=1 if active_low else 0) if pin is not None else None

    def apply(self, payload, at):
        on = bool(payload) and bool(_number(payload.get("v"), 0))
        if on != self.value:
            self.value = on
            if self._pin is not None:
                self._pin.value(1 if on != self.active_low else 0)
            _call(self.on_change, on)


class PwmOut(Output):
    type = "pwm"

    def __init__(self, device, channel_id, pin=None, name=None, on_change=None, freq=1000, unit=None, min=None,
                 max=None):
        Output.__init__(self, device, channel_id, name, {"unit": unit, "min": min, "max": max})
        self.on_change = on_change
        self.value = None
        self._pwm = None
        if pin is not None:
            self._pwm = PWM(Pin(pin))
            self._pwm.freq(freq)
            self._pwm.duty_u16(0)

    def apply(self, payload, at):
        level = float(_number(payload.get("v"), 0)) if payload else 0.0
        level = 0.0 if level < 0 else 1.0 if level > 1 else level
        if level != self.value:
            self.value = level
            if self._pwm is not None:
                self._pwm.duty_u16(int(level * 65535 + 0.5))
            _call(self.on_change, level)


class Display(Output):
    type = "display"

    def __init__(self, device, channel_id, name=None, on_change=None):
        Output.__init__(self, device, channel_id, name)
        self.on_change = on_change
        self.text = None
        self.number = None

    def apply(self, payload, at):
        text, number = "", None
        if payload:
            if "text" in payload:
                text = str(payload["text"])
            else:
                number = _number(payload.get("v"))
                text = "%g" % number if number is not None else ""
        if text != self.text or number != self.number:
            self.text = text
            self.number = number
            _call(self.on_change, text, number)


class Sound(Output):
    type = "sound"
    ignore_retained = True

    def __init__(self, device, channel_id, name=None, on_play=None):
        Output.__init__(self, device, channel_id, name)
        self.on_play = on_play

    def apply(self, payload, at):
        if payload and isinstance(payload.get("play"), str):
            _call(self.on_play, payload["play"])


class LedStrip(Output):
    type = "led_strip"

    def __init__(self, device, channel_id, pin, pixels, name=None, limit=1.0, on_frame=None):
        if pixels <= 0:
            raise ValueError("an LED strip needs at least one pixel")
        Output.__init__(self, device, channel_id, name, {"pixels": pixels})
        self.pixels = pixels
        self.limit = limit
        self.on_frame = on_frame
        self.command = {"p": "off"}
        self.frame = None
        self._identify_until = None
        self._identify_start = 0
        self._last_render = None
        self._np = _neopixel(pin, pixels) if pin is not None else None
        self._show([BLACK] * pixels)

    def apply(self, payload, at):
        # Patterns run on the field clock itself, so strips on different devices stay in step; "at" only says when
        # the command starts to show.
        self.command = payload if payload else {"p": "off"}
        self._render(self._device._now(), self._device._clock.now())

    def identify(self, now):
        self._identify_start = now
        self._identify_until = now + IDENTIFY_MS
        self._render(now, self._device._clock.now())

    def _render(self, now, field_ms):
        self._last_render = now
        if self._identify_until is not None and now < self._identify_until:
            lit = ((now - self._identify_start) // 250) % 2 == 0
            frame = [(128, 128, 128) if lit else BLACK] * self.pixels
        else:
            self._identify_until = None
            # Until the first field/clock message the board's own clock stands in, so animations still move.
            t = field_ms if field_ms is not None else now
            frame = render(self.command, self.pixels, t, self.limit)
        if frame != self.frame:
            self._show(frame)

    def _show(self, frame):
        self.frame = frame
        if self._np is not None:
            for i in range(self.pixels):
                self._np[i] = frame[i]
            self._np.write()
        _call(self.on_frame, frame)

    def tick(self, now, field_ms):
        Output.tick(self, now, field_ms)
        if animated(self.command) or self._identify_until is not None:
            if self._last_render is None or now - self._last_render >= RENDER_MS:
                self._render(now, field_ms)


class FieldState:
    def __init__(self, raw, received):
        self.raw = raw
        self.received = received
        self.state = raw.get("state")
        self.phase = raw.get("phase")
        self.phase_remaining = _number(raw.get("phase_remaining"))
        self.match_remaining = _number(raw.get("match_remaining"))
        scores = raw.get("scores")
        self.scores = scores if isinstance(scores, dict) else {}

    def phase_remaining_at(self, now):
        if self.phase_remaining is None:
            return None
        if self.state != "running":
            return self.phase_remaining
        return max(0.0, self.phase_remaining - (now - self.received) / 1000)


class _Client(_MQTTClient):
    """mqtt_as treats a refused subscription as a broken connection and reconnects (Wi-Fi included). The FMS refuses
    the output subscriptions of a device that is still pending approval, so this notes the refusal instead and keeps
    the connection. The SUBACK has been read in full when mqtt_as raises, so the stream stays in step."""

    refused = False
    on_refused = None

    async def wait_msg(self):
        try:
            await _MQTTClient.wait_msg(self)
        except OSError as error:
            if "SUBACK" not in repr(error.args):
                raise
            self.refused = True
            # Galaxia publishes at QoS 0 and subscribes one topic at a time, so the only id still awaited is that
            # of the refused SUBSCRIBE; releasing it lets subscribe() return.
            self.rcv_pids.clear()

    async def _connect(self, clean):
        # A refused connection (wrong token, blocked, reserved id) only changes when someone acts on the Devices
        # page, so it is reported and retried slowly instead of at mqtt_as's usual pace.
        try:
            await _MQTTClient._connect(self, clean)
        except OSError as error:
            code = _connack_code(error)
            if code is None:
                raise
            if self.on_refused is not None:
                self.on_refused(code)
            self._close()
            await _sleep_ms(REFUSED_RETRY_MS)
            raise


def _connack_code(error):
    """The return code in mqtt_as's "CONNACK reason code 0x.." error, as an MQTT 3.1.1 code, or None."""
    text = repr(error.args)
    marker = "CONNACK reason code 0x"
    at = text.find(marker)
    if at < 0:
        return None
    digits = ""
    for char in text[at + len(marker):]:
        if char not in "0123456789abcdefABCDEF":
            break
        digits += char
    if not digits:
        return None
    code = int(digits, 16)
    return {0x85: 2, 0x86: 4, 0x87: 5}.get(code, code)


REFUSALS = {
    2: "the device id is invalid or reserved (plc and inline are)",
    4: "wrong token: this id is approved with another token (copy it from the Devices page)",
    5: "the device is blocked on the Devices page",
}


def _check_id(value, allowed, limit, what):
    if not value or len(value) > limit:
        raise ValueError("bad " + what + ": " + repr(value))
    for char in value:
        if not ("a" <= char <= "z" or "0" <= char <= "9" or char in allowed):
            raise ValueError("bad " + what + ": " + repr(value))


class Device:
    def __init__(self, device_id, name=None, fw="1.0.0", board=None):
        _check_id(device_id, "-", 40, "device id (lowercase letters, digits and dashes)")
        if device_id in RESERVED_IDS:
            raise ValueError("the device id " + device_id + " is reserved by the FMS")
        self.id = device_id
        self.refusal = None
        self.name = name
        self.fw = fw
        self.board = board if board is not None else _board()
        self._now = _Monotonic()
        self._clock = FieldClock(self._now)
        self._channels = []
        self._ssid = None
        self._wifi_password = None
        self._host = None
        self._port = 1883
        self._token = None
        self._client = None
        self._connected = False
        self._approved = False
        self._seq = 0
        self._hb_seq = 0
        self._outbox = []
        self._wake = asyncio.Event()
        self._sender_task = None
        self._subscribe_task = None
        self._up_task = None
        self._next_status = 0
        self._next_heartbeat = 0
        self._next_resubscribe = None
        self._probe_after = 0
        self._field_state = None
        self._field_callbacks = []
        self._command_callbacks = []
        self._identify_callbacks = []
        self._identify_until = None
        self._led = None
        self.on_reboot = None

    # Setup

    def wifi(self, ssid, password):
        self._ssid = ssid
        self._wifi_password = password

    def connect(self, host, token, port=1883):
        """Remembers where the FMS is. The connection itself is made by run() (or start())."""
        self._host = host
        self._token = token
        self._port = port

    def _add(self, channel):
        _check_id(channel.id, "_", 32, "channel id (lowercase letters, digits and underscores)")
        for existing in self._channels:
            if existing.id == channel.id:
                raise ValueError("channel " + channel.id + " already exists")
        self._channels.append(channel)
        return channel

    def counter(self, channel_id, name=None):
        return self._add(Counter(self, channel_id, name))

    def counter_pin(self, channel_id, pin, name=None, pull="up", edge="falling", debounce_ms=2):
        return self._add(CounterPin(self, channel_id, pin, name, pull, edge, debounce_ms))

    def digital(self, channel_id, name=None, debounce_ms=0, invert=False):
        return self._add(Digital(self, channel_id, name, debounce_ms, invert))

    def digital_pin(self, channel_id, pin, name=None, pull="up", invert=True, debounce_ms=20):
        """A button or switch read by the SDK. With the default pull-up and invert, pressed (pin to ground) is 1."""
        channel = Digital(self, channel_id, name, debounce_ms, invert)
        channel._pin = _input_pin(pin, pull)
        return self._add(channel)

    def analog(self, channel_id, name=None, unit=None, min=None, max=None, deadband=0, min_interval_ms=100):
        return self._add(Analog(self, channel_id, name, unit, min, max, deadband, min_interval_ms))

    def encoder(self, channel_id, name=None, min_interval_ms=50):
        return self._add(Encoder(self, channel_id, name, min_interval_ms))

    def text(self, channel_id, name=None):
        return self._add(Text(self, channel_id, name))

    def safety_stop(self, channel_id, pin=None, name=None, pull="up", invert=False, recover_ms=50):
        """A normally-closed stop circuit. With a pin, wire the contact between the pin and ground: closed reads
        safe (1), open (pressed, or a broken wire) reads stop (0). Without a pin, call set(safe) yourself."""
        channel = SafetyStop(self, channel_id, name, recover_ms)
        if pin is not None:
            channel._pin = _input_pin(pin, pull)
            channel._invert = invert
        return self._add(channel)

    def digital_out(self, channel_id, pin=None, name=None, on_change=None, active_low=False):
        return self._add(DigitalOut(self, channel_id, pin, name, on_change, active_low))

    def relay(self, channel_id, pin=None, name=None, on_change=None, active_low=False):
        return self._add(DigitalOut(self, channel_id, pin, name, on_change, active_low, relay=True))

    def pwm(self, channel_id, pin=None, name=None, on_change=None, freq=1000, unit=None, min=None, max=None):
        return self._add(PwmOut(self, channel_id, pin, name, on_change, freq, unit, min, max))

    def led_strip(self, channel_id, pin, pixels, name=None, limit=1.0, on_frame=None):
        """A WS2812 (NeoPixel) strip on `pin`. limit caps the brightness (0 to 1), for example on USB power."""
        return self._add(LedStrip(self, channel_id, pin, pixels, name, limit, on_frame))

    def display(self, channel_id, name=None, on_change=None):
        return self._add(Display(self, channel_id, name, on_change))

    def sound(self, channel_id, name=None, on_play=None):
        return self._add(Sound(self, channel_id, name, on_play))

    def on_field_state(self, callback):
        self._field_callbacks.append(callback)
        return callback

    def on_command(self, callback):
        self._command_callbacks.append(callback)
        return callback

    def on_identify(self, callback):
        self._identify_callbacks.append(callback)
        return callback

    # State

    @property
    def connected(self):
        return self._connected

    @property
    def approved(self):
        return self._approved

    @property
    def field_state(self):
        return self._field_state

    def phase_remaining(self):
        state = self._field_state
        return state.phase_remaining_at(self._now()) if state else None

    def field_time_ms(self):
        """The FMS clock in UTC milliseconds, or None until the first field/clock message."""
        return self._clock.now()

    def _topic(self, suffix):
        return PREFIX + "dev/" + self.id + "/" + suffix

    def manifest(self):
        manifest = {"proto": 1, "id": self.id}
        if self.name:
            manifest["name"] = self.name
        manifest["fw"] = self.fw
        manifest["sdk"] = SDK
        if self.board:
            manifest["board"] = self.board
        manifest["channels"] = [channel.manifest() for channel in self._channels]
        return manifest

    def _status(self):
        status = {"online": True, "up": self._now() // 1000}
        status.update(_wlan_info())
        heap = _free_heap()
        if heap is not None:
            status["heap"] = heap
        return status

    # Running

    def run(self, loop=None, interval_ms=5):
        """Connects and runs forever, calling loop() (if given) every interval_ms."""
        asyncio.run(self._main(loop, interval_ms))

    async def _main(self, loop, interval_ms):
        await self.start()
        while True:
            if loop is not None:
                try:
                    loop()
                except Exception as error:  # keep the device on the field
                    print("galaxia: error in loop():", repr(error))
            await _sleep_ms(interval_ms)

    async def start(self, ticker=True):
        """Starts the SDK's tasks in a running asyncio program."""
        if self._host is None:
            raise ValueError("call connect(host, token) first")
        config = dict(_mqtt_defaults)
        config["ssid"] = self._ssid
        config["wifi_pw"] = self._wifi_password
        config["server"] = self._host
        config["port"] = self._port
        config["client_id"] = self.id
        config["user"] = self.id
        config["password"] = self._token
        config["keepalive"] = KEEPALIVE_S
        config["clean"] = True
        config["clean_init"] = True
        config["will"] = (self._topic("status"), OFFLINE, True, 0)
        config["queue_len"] = 16
        self._client = _Client(config)
        self._client.on_refused = self._refused
        asyncio.create_task(self._connect_with_backoff())
        asyncio.create_task(self._up_events())
        asyncio.create_task(self._down_events())
        asyncio.create_task(self._messages())
        if ticker:
            asyncio.create_task(self._ticker())

    async def _connect_with_backoff(self):
        delay = 1000
        while True:
            try:
                await self._client.connect()
                return  # from here on mqtt_as reconnects by itself
            except OSError as error:
                if _connack_code(error) is None:
                    print("galaxia: cannot reach the FMS yet:", repr(error))
            await _sleep_ms(delay + (self._now() % 500))
            delay = min(delay * 2, 30000)

    def _refused(self, code):
        """The broker refused the connection: 2 (id invalid or reserved), 4 (wrong token) or 5 (blocked)."""
        self.refusal = code
        print("galaxia: the FMS refused the connection (code %d): %s. Trying again in %d s."
              % (code, REFUSALS.get(code, "see the Devices page"), REFUSED_RETRY_MS // 1000))

    async def _ticker(self):
        while True:
            self.tick()
            await _sleep_ms(5)

    async def _up_events(self):
        client = self._client
        while True:
            await client.up.wait()
            client.up.clear()
            self._up_task = asyncio.create_task(self._on_up())

    async def _down_events(self):
        client = self._client
        while True:
            await client.down.wait()
            client.down.clear()
            self._on_down()

    async def _messages(self):
        async for topic, message, retained in self._client.queue:
            try:
                self._on_message(topic, message, retained)
            except Exception as error:  # one bad message must not stop the device listening
                print("galaxia: error handling", topic, repr(error))

    async def _on_up(self):
        client = self._client
        self._connected = True
        self._approved = False
        self._outbox = []
        self.refusal = None
        await client.publish(self._topic("manifest"), _dumps(self.manifest()), True, 0)
        await client.publish(self._topic("status"), _dumps(self._status()), True, 0)
        now = self._now()
        self._next_status = now + STATUS_MS
        self._next_heartbeat = now
        self._sender_task = asyncio.create_task(self._sender())
        # Every input's current value right after the manifest: the first total the FMS hears from a counter only
        # sets its starting point, so the next increment counts.
        for channel in self._channels:
            if isinstance(channel, Input):
                channel._republish()
        await client.subscribe(PREFIX + "field/#", 0)
        # A pending device is refused its outputs. The FMS drops the connection when it approves the device, and
        # the subscription succeeds after mqtt_as reconnects.
        if now >= self._probe_after:
            self._subscribe_task = asyncio.create_task(self._subscribe_outputs())
        else:
            self._next_resubscribe = self._probe_after
        print("galaxia: connected to the FMS as", self.id)

    def _on_down(self):
        self._connected = False
        self._approved = False
        self._outbox = []
        # A publish that was waiting for the connection must not go out late (a start button pressed during an
        # outage, say), so the tasks that publish are cancelled; the inputs are sent again after reconnecting.
        for task in (self._up_task, self._sender_task):
            if task is not None:
                task.cancel()
        self._up_task = None
        self._sender_task = None
        if self._subscribe_task is not None:
            # The connection broke during the output subscription: either the network failed, or this mqtt_as
            # reports a refusal differently. Do not try again at once, to avoid a reconnection loop.
            self._subscribe_task.cancel()
            self._subscribe_task = None
            self._probe_after = self._now() + PROBE_AFTER_DROP_MS
        print("galaxia: connection to the FMS lost; reconnecting")

    async def _subscribe_outputs(self):
        client = self._client
        self._next_resubscribe = None
        client.refused = False
        await client.subscribe(self._topic("out/+"), 0)
        if not client.refused:
            await client.subscribe(self._topic("cmd"), 0)
        self._subscribe_task = None
        if client.refused:
            print("galaxia: waiting for approval on the FMS Devices page")
            return
        self._approved = True

    async def _sender(self):
        client = self._client
        while True:
            while self._outbox:
                topic, data, retain = self._outbox.pop(0)
                await client.publish(topic, data, retain, 0)
            self._wake.clear()
            await self._wake.wait()

    def _queue(self, suffix, payload, retain=False):
        if not self._connected:
            return False
        if len(self._outbox) >= OUTBOX_SIZE:
            self._outbox.pop(0)
        self._outbox.append((self._topic(suffix), _dumps(payload), retain))
        self._wake.set()
        return True

    def _send_input(self, channel, value, conf):
        if not self._connected:
            return
        self._seq += 1
        payload = {"v": value, "seq": self._seq}
        if conf is not None:
            payload["conf"] = round(conf, 3)
        self._queue("in/" + channel.id, payload)

    def _send_heartbeat(self):
        states = {}
        for channel in self._channels:
            if isinstance(channel, SafetyStop):
                states[channel.id] = channel.value
        if not states:
            return False
        self._hb_seq += 1
        return self._queue("hb", {"seq": self._hb_seq, "ch": states})

    def _heartbeat_now(self):
        if self._connected and self._send_heartbeat():
            self._next_heartbeat = self._now() + HEARTBEAT_MS

    def tick(self):
        """The periodic work: pins, debounce, scheduled outputs, animations, status and heartbeat."""
        now = self._now()
        field_ms = self._clock.now()
        for channel in self._channels:
            channel.tick(now, field_ms)
        self._tick_identify(now)
        if not self._connected:
            return
        if now >= self._next_status:
            self._queue("status", self._status(), retain=True)
            self._next_status += STATUS_MS
            if self._next_status <= now:
                self._next_status = now + STATUS_MS
        if now >= self._next_heartbeat and self._send_heartbeat():
            self._next_heartbeat += HEARTBEAT_MS
            if self._next_heartbeat <= now:
                self._next_heartbeat = now + HEARTBEAT_MS
        if self._next_resubscribe is not None and now >= self._next_resubscribe and self._subscribe_task is None:
            self._subscribe_task = asyncio.create_task(self._subscribe_outputs())

    # Messages from the FMS

    def _on_message(self, topic, message, retained):
        if isinstance(topic, bytes):
            topic = topic.decode()
        payload = None
        if message:
            try:
                payload = json.loads(message.decode() if isinstance(message, (bytes, bytearray)) else message)
            except ValueError:
                return
            if not isinstance(payload, dict):
                return
        if topic.startswith(PREFIX + "field/"):
            self._field_message(topic[len(PREFIX) + 6:], payload)
            return
        out = self._topic("out/")
        if topic.startswith(out):
            name = topic[len(out):]
            for channel in self._channels:
                if channel.id == name and isinstance(channel, Output):
                    channel.handle(payload, retained, self._clock.now())
            return
        if topic == self._topic("cmd") and payload is not None:
            self._handle_command(payload)

    def _field_message(self, name, payload):
        if payload is None:
            return
        if name == "clock":
            t = _number(payload.get("t"))
            if t is not None:
                self._clock.sample(t)
        elif name == "state":
            self._field_state = FieldState(payload, self._now())
            for callback in self._field_callbacks:
                _call(callback, self._field_state)

    def _handle_command(self, command):
        action = command.get("do")
        if action == "identify":
            self.identify()
        elif action == "reboot":
            if self.on_reboot is not None:
                _call(self.on_reboot)
            else:
                asyncio.create_task(self._reboot())
        else:
            for callback in self._command_callbacks:
                _call(callback, command)

    def identify(self):
        """Blinks the on-board LED and flashes the LED strips for five seconds."""
        now = self._now()
        self._identify_until = now + IDENTIFY_MS
        for channel in self._channels:
            if isinstance(channel, LedStrip):
                channel.identify(now)
        for callback in self._identify_callbacks:
            _call(callback)

    def _tick_identify(self, now):
        if self._identify_until is None:
            return
        try:
            if self._led is None:
                self._led = Pin("LED", Pin.OUT)
            if now < self._identify_until:
                self._led.value(1 if (self._identify_until - now) // 125 % 2 else 0)
            else:
                self._led.value(0)
        except (ValueError, TypeError, OSError):
            pass  # a board without a named LED pin
        if now >= self._identify_until:
            self._identify_until = None

    async def _reboot(self):
        print("galaxia: restarting, as the FMS asked")
        try:
            await self._client.publish(self._topic("status"), OFFLINE, True, 0)
            await self._client.disconnect()
        except OSError:
            pass
        reset()
