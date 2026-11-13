"""The Galaxia device: connection, manifest, status, heartbeat, field clock and command handling."""

import json
import logging
import os
import platform
import re
import sys
import threading
import time

from . import channels as ch
from .timing import FieldClock

__version__ = "0.1.0"
SDK = "galaxia-python/" + __version__

PREFIX = "galaxia/v1/"
KEEPALIVE_S = 15
STATUS_INTERVAL_MS = 5000
HEARTBEAT_INTERVAL_MS = 200
IDENTIFY_MS = 5000
TICK_S = 0.01

DEVICE_ID = re.compile(r"^[a-z0-9-]{1,40}$")
CHANNEL_ID = re.compile(r"^[a-z0-9_]{1,32}$")
RESERVED_IDS = ("plc", "inline")
OFFLINE = b'{"online":false}'

log = logging.getLogger("galaxia")


class Refused(ConnectionError):
    """The FMS refused the connection. `code` is the MQTT 3.1.1 return code: 2 (the id is invalid or reserved),
    4 (wrong token) or 5 (blocked). The SDK keeps trying, every 30 seconds at most, so a device that is approved
    again or given its token recovers by itself."""

    MESSAGES = {
        2: "the FMS refused the device id (invalid, or reserved: plc and inline are)",
        4: "the FMS refused the token: this id is approved with another token (copy it from the Devices page)",
        5: "the FMS refused the device: it is blocked on the Devices page",
    }

    def __init__(self, code, detail=""):
        self.code = code
        message = self.MESSAGES.get(code, "the FMS refused the connection (code %s)" % code)
        super().__init__(message + (" [%s]" % detail if detail else ""))


def _return_code(reason_code):
    """The MQTT 3.1.1 CONNACK return code for paho's reason code (paho reports 3.1.1 codes as MQTT 5 ones)."""
    value = getattr(reason_code, "value", reason_code)
    return {133: 2, 134: 4, 135: 5}.get(value, value)


def _json(payload):
    return json.dumps(payload, separators=(",", ":")).encode()


def _default_board():
    try:
        with open("/proc/device-tree/model", "rb") as model:
            return model.read().rstrip(b"\x00").decode(errors="replace").strip()
    except OSError:
        return "%s %s" % (platform.system(), platform.machine())


def paho_client(device_id):
    """Creates the paho-mqtt client the device uses (version 2 callback API, MQTT 3.1.1, clean session)."""
    import paho.mqtt.client as mqtt

    return mqtt.Client(mqtt.CallbackAPIVersion.VERSION2, client_id=device_id, protocol=mqtt.MQTTv311,
                       clean_session=True)


class FieldState:
    """The last field/state message."""

    def __init__(self, raw, received_ms):
        self.raw = raw
        self.received_ms = received_ms
        self.state = raw.get("state")
        self.phase = raw.get("phase")
        self.phase_remaining = ch._number(raw.get("phase_remaining"))
        self.match_remaining = ch._number(raw.get("match_remaining"))
        scores = raw.get("scores")
        self.scores = dict(scores) if isinstance(scores, dict) else {}

    @property
    def running(self):
        return self.state == "running"

    def phase_remaining_at(self, now_ms):
        """Seconds left in the phase, counted down locally between messages while the match runs."""
        if self.phase_remaining is None:
            return None
        if not self.running:
            return self.phase_remaining
        return max(0.0, self.phase_remaining - (now_ms - self.received_ms) / 1000.0)


class Device:
    """A Galaxia field device.

        dev = Device("beam-red-1", "Red goal beam break")
        balls = dev.counter("count")
        dev.connect("10.0.100.5", "device-token")
        balls.increment()
    """

    def __init__(self, device_id, name=None, fw="1.0.0", board=None, *, client_factory=None, monotonic=None,
                 wall=None):
        if not DEVICE_ID.match(device_id or ""):
            raise ValueError("a device id is 1 to 40 lowercase letters, digits and dashes: %r" % device_id)
        if device_id in RESERVED_IDS:
            raise ValueError("the device id %r is reserved by the FMS" % device_id)
        self.id = device_id
        self.name = name
        self.fw = fw
        self.board = board if board is not None else _default_board()
        self._client_factory = client_factory or paho_client
        self._monotonic = monotonic or time.monotonic
        self._wall = wall or time.time
        self._lock = threading.RLock()
        self._channels = {}
        self._clock = FieldClock(self._now_ms)
        self._start_ms = self._now_ms()
        self._seq = 0
        self._hb_seq = 0
        self._client = None
        self._connected = False
        self._approved = False
        self._subscribe_mid = None
        self._next_status = 0
        self._next_heartbeat = 0
        self._refusal = None
        self._state_changed = threading.Condition(self._lock)
        self._field_state = None
        self._field_callbacks = []
        self._field_message_callbacks = []
        self._command_callbacks = []
        self._identify_callbacks = []
        self._connect_callbacks = []
        self.on_reboot = None
        self._thread = None
        self._stop = threading.Event()

    # Channels

    def _add(self, channel):
        if not CHANNEL_ID.match(channel.id or ""):
            raise ValueError("a channel id is lowercase letters, digits and underscores: %r" % channel.id)
        with self._lock:
            if self._client is not None:
                raise RuntimeError("add channels before connect()")
            if channel.id in self._channels:
                raise ValueError("channel %r already exists" % channel.id)
            self._channels[channel.id] = channel
        return channel

    def counter(self, channel_id, name=None):
        return self._add(ch.Counter(self, channel_id, name))

    def digital(self, channel_id, name=None, debounce_ms=0, invert=False):
        return self._add(ch.Digital(self, channel_id, name, debounce_ms, invert))

    def analog(self, channel_id, name=None, unit=None, min=None, max=None, deadband=0.0, min_interval_ms=100):
        return self._add(ch.Analog(self, channel_id, name, unit, min, max, deadband, min_interval_ms))

    def encoder(self, channel_id, name=None, min_interval_ms=50):
        return self._add(ch.Encoder(self, channel_id, name, min_interval_ms))

    def text(self, channel_id, name=None):
        return self._add(ch.Text(self, channel_id, name))

    def safety_stop(self, channel_id, name=None, recover_ms=50):
        return self._add(ch.SafetyStop(self, channel_id, name, recover_ms))

    def digital_out(self, channel_id, name=None, on_change=None):
        return self._add(ch.DigitalOut(self, channel_id, name, on_change))

    def relay(self, channel_id, name=None, on_change=None):
        return self._add(ch.DigitalOut(self, channel_id, name, on_change, relay=True))

    def pwm(self, channel_id, name=None, on_change=None, unit=None, min=None, max=None):
        return self._add(ch.PwmOut(self, channel_id, name, on_change, unit, min, max))

    def led_strip(self, channel_id, pixels, name=None, on_frame=None, limit=1.0):
        return self._add(ch.LedStrip(self, channel_id, pixels, name, on_frame, limit))

    def display(self, channel_id, name=None, on_change=None):
        return self._add(ch.Display(self, channel_id, name, on_change))

    def sound(self, channel_id, name=None, on_play=None):
        return self._add(ch.Sound(self, channel_id, name, on_play))

    @property
    def channels(self):
        return list(self._channels.values())

    # Callbacks

    def on_field_state(self, callback):
        """callback(state: FieldState) on every field/state message."""
        self._field_callbacks.append(callback)
        return callback

    def on_field_message(self, callback):
        """callback(name, payload) for every message under field/ (name is "clock", "state" and so on)."""
        self._field_message_callbacks.append(callback)
        return callback

    def on_command(self, callback):
        """callback(command: dict) for cmd messages the SDK does not handle itself (it handles identify and
        reboot)."""
        self._command_callbacks.append(callback)
        return callback

    def on_identify(self, callback):
        """callback() when someone presses Identify. LED strips also flash white for five seconds."""
        self._identify_callbacks.append(callback)
        return callback

    def on_connect(self, callback):
        """callback() each time the device (re)connects to the FMS."""
        self._connect_callbacks.append(callback)
        return callback

    # State

    @property
    def connected(self):
        return self._connected

    @property
    def approved(self):
        """True once the FMS has accepted this device's output and command subscriptions, which it does for an
        approved device."""
        return self._approved

    @property
    def refusal(self):
        """The Refused error from the last connection attempt, or None if it was accepted."""
        return self._refusal

    def wait_connected(self, timeout=None):
        """Blocks until the device is connected (the SDK keeps retrying meanwhile). After `timeout` seconds it
        raises Refused if the FMS refused the last attempt (with the reason), and TimeoutError otherwise."""
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._lock:
            while not self._connected:
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    if self._refusal is not None:
                        raise self._refusal
                    raise TimeoutError("not connected to the FMS after %s s" % timeout)
                self._state_changed.wait(remaining if remaining is not None else 1.0)

    @property
    def field_state(self):
        return self._field_state

    def phase_remaining(self):
        """Seconds left in the current phase, or None before the first field/state message."""
        state = self._field_state
        return state.phase_remaining_at(self._now_ms()) if state else None

    @property
    def clock_synced(self):
        return self._clock.synced

    def field_time_ms(self):
        """The FMS clock in UTC milliseconds, from field/clock; the system clock until the first message."""
        now = self._clock.now()
        return now if now is not None else int(round(self._wall() * 1000))

    def uptime_ms(self):
        return self._now_ms() - self._start_ms

    def _now_ms(self):
        return int(round(self._monotonic() * 1000))

    def _topic(self, suffix):
        return PREFIX + "dev/" + self.id + "/" + suffix

    # Connection

    def manifest(self):
        manifest = {"proto": 1, "id": self.id}
        if self.name:
            manifest["name"] = self.name
        manifest["fw"] = self.fw
        manifest["sdk"] = SDK
        if self.board:
            manifest["board"] = self.board
        manifest["channels"] = [channel.manifest() for channel in self._channels.values()]
        return manifest

    def connect(self, host, token, port=1883, background=True):
        """Starts connecting to the FMS broker. Reconnection is automatic. With background=True (the default) a
        thread runs loop(); otherwise call loop() yourself at least every 10 ms."""
        with self._lock:
            if self._client is not None:
                raise RuntimeError("connect() was already called")
            client = self._client_factory(self.id)
            client.username_pw_set(self.id, token)
            client.will_set(self._topic("status"), OFFLINE, qos=0, retain=True)
            client.reconnect_delay_set(1, 30)
            client.on_connect = self._on_connect
            client.on_disconnect = self._on_disconnect
            client.on_message = self._on_message
            client.on_subscribe = self._on_subscribe
            self._client = client
        client.connect_async(host, port, keepalive=KEEPALIVE_S)
        client.loop_start()
        if background:
            self._stop.clear()
            self._thread = threading.Thread(target=self._run, name="galaxia-" + self.id, daemon=True)
            self._thread.start()

    def _run(self):
        while not self._stop.is_set():
            try:
                self.loop()
            except Exception:  # noqa: BLE001 - keep the device running
                log.exception("Error in the Galaxia loop")
            self._stop.wait(TICK_S)

    def run_forever(self):
        """Blocks forever (use it as the last line of a script that only reacts to callbacks)."""
        while True:
            if self._thread is None:
                self.loop()
                time.sleep(TICK_S)
            else:
                time.sleep(1)

    def disconnect(self):
        """Says goodbye to the FMS (status offline) and stops."""
        with self._lock:
            client = self._client
            if client is None:
                return
            if self._connected:
                client.publish(self._topic("status"), OFFLINE, qos=0, retain=True)
            self._connected = False
            self._approved = False
        self._stop.set()
        client.disconnect()
        client.loop_stop()
        if self._thread is not None and self._thread is not threading.current_thread():
            self._thread.join(timeout=1)
        self._thread = None
        with self._lock:
            self._client = None

    def _publish(self, suffix, payload, retain=False):
        client = self._client
        if client is None or not self._connected:
            return False
        data = payload if isinstance(payload, bytes) else _json(payload)
        client.publish(self._topic(suffix), data, qos=0, retain=retain)
        return True

    def _status(self):
        status = {"online": True, "up": self.uptime_ms() // 1000}
        try:
            sock = self._client.socket()
            if sock is not None:
                status["ip"] = sock.getsockname()[0]
        except (AttributeError, OSError):
            pass
        return status

    def _on_connect(self, client, userdata, flags, reason_code, properties):
        if reason_code.is_failure:
            # paho tries again by itself, waiting up to 30 s between attempts (reconnect_delay_set), so a device
            # that is approved again or given its token recovers without a restart.
            with self._lock:
                self._refusal = Refused(_return_code(reason_code), str(reason_code))
                self._state_changed.notify_all()
            log.error("%s; trying again", self._refusal)
            return
        with self._lock:
            self._refusal = None
            self._connected = True
            self._approved = False
            now = self._now_ms()
            self._publish("manifest", self.manifest(), retain=True)
            self._publish("status", self._status(), retain=True)
            # Every input's current value right after the manifest: the first total the FMS hears from a counter
            # only sets its starting point, so the next increment counts.
            self._republish_inputs()
            self._next_status = now + STATUS_INTERVAL_MS
            self._next_heartbeat = now
            client.subscribe(PREFIX + "field/#", 0)
            # A pending device is refused these two. The FMS drops the connection when it approves the device, and
            # the subscription succeeds on the next connection.
            _, self._subscribe_mid = client.subscribe([(self._topic("out/+"), 0), (self._topic("cmd"), 0)])
            self._state_changed.notify_all()
            log.info("Connected to the FMS as %s", self.id)
        for callback in self._connect_callbacks:
            ch.call(callback)

    def _on_disconnect(self, client, userdata, flags, reason_code, properties):
        with self._lock:
            was_connected = self._connected
            self._connected = False
            self._approved = False
            self._state_changed.notify_all()
        if was_connected:
            log.warning("Disconnected from the FMS (%s); reconnecting", reason_code)

    def _on_subscribe(self, client, userdata, mid, reason_codes, properties):
        with self._lock:
            if mid != self._subscribe_mid:
                return
            self._subscribe_mid = None
            if any(code.is_failure for code in reason_codes):
                log.info("Waiting for approval on the FMS Devices page")
                return
            self._approved = True

    def _republish_inputs(self):
        for channel in self._channels.values():
            if isinstance(channel, ch.Input):
                channel._republish()

    # Messages

    def _on_message(self, client, userdata, message):
        # paho re-raises callback errors, which would stop its network thread, so nothing may escape from here.
        try:
            self._handle_message(message)
        except Exception:  # noqa: BLE001
            log.exception("Error handling a message on %s", message.topic)

    def _handle_message(self, message):
        topic = message.topic
        try:
            payload = json.loads(message.payload) if message.payload else None
        except ValueError:
            log.warning("Ignoring a message that is not JSON on %s", topic)
            return
        if payload is not None and not isinstance(payload, dict):
            return
        with self._lock:
            if topic.startswith(PREFIX + "field/"):
                self._field_message(topic[len(PREFIX) + 6:], payload)
            elif topic.startswith(self._topic("out/")):
                channel = self._channels.get(topic[len(self._topic("out/")):])
                if isinstance(channel, ch.Output):
                    channel._command(payload, bool(message.retain), self.field_time_ms())
            elif topic == self._topic("cmd") and payload is not None:
                self._handle_command(payload)

    def _field_message(self, name, payload):
        if payload is None:
            return
        if name == "clock":
            t = ch._number(payload.get("t"))
            if t is not None:
                self._clock.sample(t)
        elif name == "state":
            self._field_state = FieldState(payload, self._now_ms())
            for callback in self._field_callbacks:
                ch.call(callback, self._field_state)
        for callback in self._field_message_callbacks:
            ch.call(callback, name, payload)

    def _handle_command(self, command):
        action = command.get("do")
        if action == "identify":
            self.identify()
        elif action == "reboot":
            self._reboot()
        else:
            for callback in self._command_callbacks:
                ch.call(callback, command)

    def identify(self):
        """Flashes the LED strips for five seconds and calls the on_identify callbacks."""
        now = self._now_ms()
        for channel in self._channels.values():
            if isinstance(channel, ch.LedStrip):
                channel.identify(now, IDENTIFY_MS)
        for callback in self._identify_callbacks:
            ch.call(callback)

    def _reboot(self):
        if self.on_reboot is not None:
            ch.call(self.on_reboot)
            return
        log.warning("Restarting the program, as the FMS asked")
        threading.Thread(target=self._restart_process, daemon=True).start()

    def _restart_process(self):
        self.disconnect()
        os.execv(sys.executable, [sys.executable] + sys.argv)

    # Inputs and periodic work

    def _send_input(self, channel, value, conf, ts):
        if not self._connected:
            return
        self._seq += 1
        payload = {"v": value, "seq": self._seq}
        if conf is not None:
            payload["conf"] = round(float(conf), 3)
        if ts is not None:
            payload["ts"] = int(ts)
        self._publish("in/" + channel.id, payload)

    def _heartbeat_now(self):
        """Sends a heartbeat at once when a safety channel changes, and restarts the 200 ms cadence from here."""
        if self._connected and self._send_heartbeat():
            self._next_heartbeat = self._now_ms() + HEARTBEAT_INTERVAL_MS

    def _send_heartbeat(self):
        states = {c.id: c.value for c in self._channels.values() if isinstance(c, ch.SafetyStop)}
        if not states:
            return False
        self._hb_seq += 1
        return self._publish("hb", {"seq": self._hb_seq, "ch": states})

    def loop(self):
        """Does the periodic work: debounce, scheduled outputs, animations, status, heartbeat. connect() runs it in
        a background thread unless you passed background=False."""
        with self._lock:
            now = self._now_ms()
            field_ms = self.field_time_ms()
            for channel in self._channels.values():
                channel._tick(now, field_ms)
            if not self._connected:
                return
            if now >= self._next_status:
                self._publish("status", self._status(), retain=True)
                self._next_status += STATUS_INTERVAL_MS
                if self._next_status <= now:
                    self._next_status = now + STATUS_INTERVAL_MS
            if now >= self._next_heartbeat and self._send_heartbeat():
                self._next_heartbeat += HEARTBEAT_INTERVAL_MS
                if self._next_heartbeat <= now:
                    self._next_heartbeat = now + HEARTBEAT_INTERVAL_MS
