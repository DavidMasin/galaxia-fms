"""Input and output channels. Create them with the Device methods (dev.counter(...), dev.led_strip(...) and so on)."""

import logging
import math

from .led import BLACK, animated, render
from .timing import Schedule

log = logging.getLogger("galaxia")

SCHEDULE_SIZE = 32
RENDER_INTERVAL_MS = 20
IDENTIFY_FLASH_MS = 250
IDENTIFY_COLOR = (128, 128, 128)


def _number(value, default=None):
    if isinstance(value, bool):
        return 1 if value else 0
    if isinstance(value, (int, float)) and not (isinstance(value, float) and math.isnan(value)):
        return value
    return default


def call(callback, *args):
    """Runs a user callback, logging rather than propagating its errors so the SDK's threads keep running."""
    if callback is None:
        return
    try:
        callback(*args)
    except Exception:  # noqa: BLE001 - user code must not stop the device
        log.exception("Error in a Galaxia callback")


class Channel:
    direction = "in"
    type = ""

    def __init__(self, device, channel_id, name=None, **extra):
        self._device = device
        self.id = channel_id
        self.name = name
        self._extra = {key: value for key, value in extra.items() if value is not None}

    def manifest(self):
        entry = {"id": self.id, "dir": self.direction, "type": self.type}
        if self.name:
            entry["name"] = self.name
        entry.update(self._extra)
        return entry

    def _tick(self, now_ms, field_ms):
        pass


# Inputs


class Input(Channel):
    direction = "in"
    republish = True

    def __init__(self, device, channel_id, name=None, **extra):
        super().__init__(device, channel_id, name, **extra)
        self._value = None

    @property
    def value(self):
        """The last value this channel reported."""
        return self._value

    def _send(self, value, conf=None, ts=None):
        self._value = value
        self._device._send_input(self, value, conf, ts)

    def _republish(self):
        if self.republish and self._value is not None:
            self._device._send_input(self, self._value, None, None)


class Counter(Input):
    """A running total since the program started. The FMS works from differences, so no count is lost."""

    type = "counter"

    def __init__(self, device, channel_id, name=None):
        super().__init__(device, channel_id, name)
        self._value = 0

    @property
    def total(self):
        return self._value

    def increment(self, n=1, conf=None, ts=None):
        if not isinstance(n, int) or n < 0:
            raise ValueError("a counter only goes up; use a whole number of 0 or more")
        if n == 0:
            return
        with self._device._lock:
            self._send(self._value + n, conf, ts)


class Digital(Input):
    """0 or 1. With debounce_ms, a new value must hold that long before it is reported."""

    type = "digital"

    def __init__(self, device, channel_id, name=None, debounce_ms=0, invert=False, **extra):
        super().__init__(device, channel_id, name, **extra)
        self.debounce_ms = debounce_ms
        self.invert = invert
        self._candidate = None
        self._candidate_conf = None
        self._since = 0

    def set(self, value, conf=None, ts=None):
        state = 1 if value else 0
        if self.invert:
            state ^= 1
        with self._device._lock:
            if self.debounce_ms <= 0:
                if state != self._value:
                    self._send(state, conf, ts)
                return
            now = self._device._now_ms()
            if state != self._candidate:
                self._candidate = state
                self._since = now
            self._candidate_conf = conf
            self._tick(now, None)

    def _tick(self, now_ms, field_ms):
        if self._candidate is None or self._candidate == self._value:
            return
        if now_ms - self._since >= self.debounce_ms:
            self._send(self._candidate, self._candidate_conf)


class SafetyStop(Input):
    """A normally-closed stop circuit: 1 while closed (safe), 0 when open (stop pressed or wire broken).

    A stop is reported at once; a return to safe must hold for recover_ms. Until the first reading the channel
    reports 0. The device sends a heartbeat with this state every 200 ms.
    """

    type = "digital"

    def __init__(self, device, channel_id, name=None, recover_ms=50):
        super().__init__(device, channel_id, name, safety=True)
        self.recover_ms = recover_ms
        self._value = 0
        self._candidate = None
        self._since = 0

    @property
    def safe(self):
        return self._value == 1

    def set(self, safe):
        with self._device._lock:
            now = self._device._now_ms()
            if not safe:
                self._candidate = None
                if self._value != 0:
                    self._send(0)
                    self._device._heartbeat_now()
                return
            if self._value == 1:
                self._candidate = None
                return
            if self._candidate != 1:
                self._candidate = 1
                self._since = now
            self._tick(now, None)

    def _tick(self, now_ms, field_ms):
        if self._candidate == 1 and now_ms - self._since >= self.recover_ms:
            self._candidate = None
            self._send(1)
            self._device._heartbeat_now()


class Analog(Input):
    """A number. Changes within `deadband` are not reported, nor more than one value every min_interval_ms."""

    type = "analog"

    def __init__(self, device, channel_id, name=None, unit=None, min=None, max=None, deadband=0.0,
                 min_interval_ms=100):
        super().__init__(device, channel_id, name, unit=unit, min=min, max=max)
        self.deadband = deadband
        self.min_interval_ms = min_interval_ms
        self._pending = None
        self._last_sent_ms = None

    def set(self, value, conf=None, ts=None):
        number = _number(value)
        if number is None:
            raise ValueError("an analog value must be a number")
        with self._device._lock:
            self._pending = (number, conf, ts)
            self._tick(self._device._now_ms(), None)

    def _changed(self, value):
        if self._value is None:
            return True
        if self.deadband > 0:
            return abs(value - self._value) > self.deadband
        return value != self._value

    def _tick(self, now_ms, field_ms):
        if self._pending is None:
            return
        value, conf, ts = self._pending
        if not self._changed(value):
            self._pending = None
            return
        if self._last_sent_ms is not None and now_ms - self._last_sent_ms < self.min_interval_ms:
            return
        self._pending = None
        self._last_sent_ms = now_ms
        self._send(value, conf, ts)


class Encoder(Analog):
    """A signed position."""

    type = "encoder"

    def __init__(self, device, channel_id, name=None, min_interval_ms=50):
        super().__init__(device, channel_id, name, min_interval_ms=min_interval_ms)

    def set(self, value, conf=None, ts=None):
        super().set(int(value), conf, ts)


class Text(Input):
    """A string such as an RFID tag or a barcode. Every set() is reported, even a repeat."""

    type = "text"
    republish = False

    def set(self, text, conf=None, ts=None):
        with self._device._lock:
            self._send(str(text), conf, ts)


# Outputs


class Output(Channel):
    direction = "out"
    ignore_retained = False

    def __init__(self, device, channel_id, name=None, **extra):
        super().__init__(device, channel_id, name, **extra)
        self._schedule = Schedule(SCHEDULE_SIZE)

    def _command(self, payload, retained, field_ms):
        """Handles a message on this channel's out/ topic. payload is None when the retained output was cleared."""
        if payload is None:
            self._schedule.clear()
            self._apply(None, None)
            return
        if retained and self.ignore_retained:
            return
        at = _number(payload.get("at"))
        at = int(at) if at is not None else None
        if self._schedule.offer(at, field_ms, payload):
            self._apply(payload, at)

    def _tick(self, now_ms, field_ms):
        for at, payload in self._schedule.due(field_ms):
            self._apply(payload, at)

    def _apply(self, payload, at):
        raise NotImplementedError


class DigitalOut(Output):
    """On or off: {"v":1} or {"v":0}. on_change(on) is called when the state changes."""

    def __init__(self, device, channel_id, name=None, on_change=None, relay=False):
        super().__init__(device, channel_id, name)
        self.type = "relay" if relay else "digital"
        self.on_change = on_change
        self.value = None

    def _apply(self, payload, at):
        on = bool(payload) and bool(_number(payload.get("v"), 0))
        if on != self.value:
            self.value = on
            call(self.on_change, on)


class PwmOut(Output):
    """A level from 0 to 1: {"v":0.35}. on_change(level) is called when it changes."""

    type = "pwm"

    def __init__(self, device, channel_id, name=None, on_change=None, unit=None, min=None, max=None):
        super().__init__(device, channel_id, name, unit=unit, min=min, max=max)
        self.on_change = on_change
        self.value = None

    def _apply(self, payload, at):
        level = float(_number(payload.get("v"), 0)) if payload else 0.0
        level = 0.0 if level < 0 else 1.0 if level > 1 else level
        if level != self.value:
            self.value = level
            call(self.on_change, level)


class Display(Output):
    """Text or a number: {"text":"1:32"} or {"v":92}. on_change(text, number) gets the text to show and the number
    (None for text)."""

    type = "display"

    def __init__(self, device, channel_id, name=None, on_change=None):
        super().__init__(device, channel_id, name)
        self.on_change = on_change
        self.text = None
        self.number = None

    def _apply(self, payload, at):
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
            call(self.on_change, text, number)


class Sound(Output):
    """{"play":"buzzer"}: on_play(name) is called for each command. A retained command (one that was already
    there when the device subscribed) is not replayed."""

    type = "sound"
    ignore_retained = True

    def __init__(self, device, channel_id, name=None, on_play=None):
        super().__init__(device, channel_id, name)
        self.on_play = on_play

    def _apply(self, payload, at):
        if payload and isinstance(payload.get("play"), str):
            call(self.on_play, payload["play"])


class LedStrip(Output):
    """An addressable strip. The SDK draws every pattern itself from the field clock, as docs/devices.md defines
    them; on_frame(pixels) gets a list of (r, g, b) tuples whenever the picture changes. `limit` caps the
    brightness (0 to 1), for example to stay within a USB supply."""

    type = "led_strip"

    def __init__(self, device, channel_id, pixels, name=None, on_frame=None, limit=1.0):
        if not isinstance(pixels, int) or pixels <= 0:
            raise ValueError("an LED strip needs at least one pixel")
        super().__init__(device, channel_id, name, pixels=pixels)
        self.pixels = pixels
        self.on_frame = on_frame
        self.limit = limit
        self.command = {"p": "off"}
        self.frame = None
        self._identify_until = None
        self._identify_start = 0
        self._last_render_ms = None

    def _apply(self, payload, at):
        # Patterns run on the field clock itself, so strips on different devices stay in step; "at" only says when
        # the command starts to show.
        self.command = payload if payload else {"p": "off"}
        self._render(self._device._now_ms(), self._device.field_time_ms())

    def identify(self, now_ms, duration_ms):
        self._identify_start = now_ms
        self._identify_until = now_ms + duration_ms
        self._render(now_ms, self._device.field_time_ms())

    def _render(self, now_ms, field_ms):
        self._last_render_ms = now_ms
        if self._identify_until is not None and now_ms < self._identify_until:
            lit = ((now_ms - self._identify_start) // IDENTIFY_FLASH_MS) % 2 == 0
            frame = [IDENTIFY_COLOR if lit else BLACK] * self.pixels
        else:
            self._identify_until = None
            frame = render(self.command, self.pixels, field_ms, self.limit)
        if frame != self.frame:
            self.frame = frame
            call(self.on_frame, frame)

    def _tick(self, now_ms, field_ms):
        super()._tick(now_ms, field_ms)
        moving = animated(self.command) or self._identify_until is not None
        if moving and (self._last_render_ms is None or now_ms - self._last_render_ms >= RENDER_INTERVAL_MS):
            self._render(now_ms, field_ms)
