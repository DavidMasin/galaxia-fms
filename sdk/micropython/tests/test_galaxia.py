"""Runs sdk/micropython/galaxia.py under CPython, with stand-ins for machine, network, neopixel and mqtt_as.

    python -m pytest sdk/micropython/tests
"""

import asyncio
import json
import os
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "stubs"))
sys.path.insert(0, os.path.dirname(HERE))

import galaxia  # noqa: E402
import machine  # noqa: E402
import neopixel  # noqa: E402
from mqtt_as import BROKER  # noqa: E402

CASES = os.path.join(HERE, "..", "..", "testdata", "led_cases.json")
FIELD_MS = 1_794_510_000_000
RED = (255, 0, 0)
BLACK = (0, 0, 0)


class Clock:
    def __init__(self):
        self.ms = 50_000

    def __call__(self):
        return self.ms


@pytest.fixture(autouse=True)
def clean(monkeypatch):
    BROKER.reset()
    machine.Pin.pins.clear()
    machine.resets.clear()
    neopixel.strips.clear()
    clock = Clock()
    monkeypatch.setattr(galaxia, "_ticks_ms", clock)
    monkeypatch.setattr(galaxia, "_ticks_diff", lambda a, b: a - b)
    return clock


@pytest.fixture
def clock(clean):
    return clean


async def settle():
    for _ in range(60):
        await asyncio.sleep(0)


class Harness:
    def __init__(self, clock):
        self.clock = clock

    async def start(self, dev, approve=True):
        if approve:
            BROKER.approved.add(dev.id)
        dev.wifi("field-devices", "pw")
        dev.connect("10.0.100.5", "tok")
        await dev.start(ticker=False)
        await settle()
        dev.tick()
        await settle()

    async def step(self, dev, ms, tick=5):
        done = 0
        while done < ms:
            delta = min(tick, ms - done)
            self.clock.ms += delta
            done += delta
            dev.tick()
            await settle()

    async def fms(self, topic, payload, retain=False):
        data = payload if isinstance(payload, (bytes, str)) else json.dumps(payload)
        BROKER.publish("galaxia/v1/" + topic, data, retain)
        await settle()

    @staticmethod
    def sent(dev, suffix):
        return [json.loads(entry[2]) for entry in BROKER.messages("galaxia/v1/dev/%s/%s" % (dev.id, suffix))]


def run(clock, scenario):
    asyncio.run(scenario(Harness(clock)))


def test_mqtt_settings_manifest_and_status(clock):
    async def scenario(h):
        dev = galaxia.Device("beam-red-1", "Red goal beam break", fw="1.2.0")
        dev.counter("count", "Balls")
        dev.safety_stop("stop", 15)
        dev.led_strip("strip", 16, 30)
        dev.pwm("fan", 14, unit="%", min=0, max=100)
        await h.start(dev)
        client = BROKER.clients[0]
        assert client.config["client_id"] == "beam-red-1"
        assert client.config["user"] == "beam-red-1"
        assert client.config["password"] == "tok"
        assert client.config["keepalive"] == 15
        assert client.config["ssid"] == "field-devices"
        assert client.config["will"] == ("galaxia/v1/dev/beam-red-1/status", b'{"online":false}', True, 0)
        [(_, _, payload, retain)] = BROKER.messages("galaxia/v1/dev/beam-red-1/manifest")
        assert retain is True
        manifest = json.loads(payload)
        assert manifest["proto"] == 1 and manifest["id"] == "beam-red-1" and manifest["fw"] == "1.2.0"
        assert manifest["sdk"] == galaxia.SDK
        assert manifest["channels"] == [
            {"id": "count", "dir": "in", "type": "counter", "name": "Balls"},
            {"id": "stop", "dir": "in", "type": "digital", "safety": True},
            {"id": "strip", "dir": "out", "type": "led_strip", "pixels": 30},
            {"id": "fan", "dir": "out", "type": "pwm", "unit": "%", "min": 0, "max": 100},
        ]
        await h.step(dev, 10_000)
        statuses = BROKER.messages("galaxia/v1/dev/beam-red-1/status")
        assert len(statuses) == 3 and all(entry[3] for entry in statuses)
        status = json.loads(statuses[-1][2])
        assert status["online"] is True and status["up"] == 10
        assert status["rssi"] == -58 and status["ip"] == "10.0.100.61"
        assert sorted(client.subscriptions) == [
            "galaxia/v1/dev/beam-red-1/cmd",
            "galaxia/v1/dev/beam-red-1/out/+",
            "galaxia/v1/field/#",
        ]

    run(clock, scenario)


def test_counter_and_counter_pin(clock):
    async def scenario(h):
        dev = galaxia.Device("beam-red-1")
        balls = dev.counter_pin("count", 27, debounce_ms=2)
        manual = dev.counter("manual")
        await h.start(dev)
        pin = machine.Pin.pins[27]
        assert pin.pull == machine.Pin.PULL_UP and pin.trigger == machine.Pin.IRQ_FALLING
        for _ in range(3):
            pin.drive(0)
            clock.ms += 1
            pin.drive(1)
            clock.ms += 5
        pin.drive(0)  # a bounce 1 ms after the last falling edge is not a ball
        clock.ms += 1
        pin.drive(1)
        clock.ms += 1
        pin.drive(0)
        await h.step(dev, 5)
        assert balls.total == 4
        manual.increment(2)
        await settle()
        assert [m["v"] for m in h.sent(dev, "in/count")] == [0, 4]
        assert [m["v"] for m in h.sent(dev, "in/manual")] == [0, 2]
        seqs = [m["seq"] for m in h.sent(dev, "in/count") + h.sent(dev, "in/manual")]
        assert sorted(seqs) == list(range(1, 5))

    run(clock, scenario)


def test_digital_pin_debounce_and_invert(clock):
    async def scenario(h):
        dev = galaxia.Device("arcade-1")
        dev.digital_pin("button", 12, debounce_ms=20)
        await h.start(dev)
        await h.step(dev, 25)
        pin = machine.Pin.pins[12]
        for level in (0, 1, 0, 1, 0):  # bounce
            pin.drive(level)
            await h.step(dev, 5)
        await h.step(dev, 10)
        assert [m["v"] for m in h.sent(dev, "in/button")] == [0]
        await h.step(dev, 10)
        assert [m["v"] for m in h.sent(dev, "in/button")] == [0, 1]  # pressed (pin low) is 1

    run(clock, scenario)


def test_safety_pin_heartbeat(clock):
    async def scenario(h):
        dev = galaxia.Device("estop-box-red")
        stop = dev.safety_stop("stop", 15)
        pin = machine.Pin.pins[15]
        pin.drive(0)  # normally-closed contact to ground: closed
        await h.start(dev)
        await h.step(dev, 60)
        assert stop.safe
        before = len(h.sent(dev, "hb"))
        await h.step(dev, 1000)
        beats = h.sent(dev, "hb")[before:]
        assert len(beats) == 5 and all(b["ch"] == {"stop": 1} for b in beats)
        pin.drive(1)  # pressed or wire cut
        await h.step(dev, 5)
        assert h.sent(dev, "in/stop")[-1]["v"] == 0
        assert h.sent(dev, "hb")[-1]["ch"] == {"stop": 0}
        seqs = [b["seq"] for b in h.sent(dev, "hb")]
        assert seqs == list(range(1, len(seqs) + 1))

    run(clock, scenario)


def test_outputs_pins_and_at(clock):
    async def scenario(h):
        dev = galaxia.Device("stack-light")
        dev.relay("red", 2, active_low=True)
        dev.digital_out("green", 3)
        fan = dev.pwm("fan", 4)
        texts = []
        dev.display("timer", on_change=lambda text, number: texts.append(text))
        plays = []
        dev.sound("buzzer", on_play=plays.append)
        await h.fms("dev/stack-light/out/buzzer", {"play": "buzzer"}, retain=True)
        await h.start(dev)
        assert plays == []  # retained sounds are not replayed
        assert machine.Pin.pins[2].value() == 1  # active-low relay starts off
        await h.fms("dev/stack-light/out/red", {"v": 1}, retain=True)
        await h.fms("dev/stack-light/out/green", {"v": 1}, retain=True)
        await h.fms("dev/stack-light/out/fan", {"v": 0.5}, retain=True)
        assert machine.Pin.pins[2].value() == 0
        assert machine.Pin.pins[3].value() == 1
        assert fan._pwm.duty == 32768
        await h.fms("dev/stack-light/out/buzzer", {"play": "horn"})
        assert plays == ["horn"]

        # Without a field clock an "at" command is shown at once.
        await h.fms("dev/stack-light/out/timer", {"text": "now", "at": FIELD_MS + 99_999})
        await h.fms("field/clock", {"t": FIELD_MS})
        await h.fms("dev/stack-light/out/timer", {"text": "3", "at": FIELD_MS + 1000})
        await h.fms("dev/stack-light/out/timer", {"text": "GO", "at": FIELD_MS + 3000})
        await h.step(dev, 995)
        assert texts == ["now"]
        await h.step(dev, 5)
        assert texts == ["now", "3"]
        await h.step(dev, 2000)
        assert texts == ["now", "3", "GO"]
        await h.fms("dev/stack-light/out/timer", b"", retain=True)
        assert texts[-1] == ""

    run(clock, scenario)


def test_led_strip_on_neopixel(clock):
    async def scenario(h):
        dev = galaxia.Device("goal-red")
        strip = dev.led_strip("strip", 16, 4, limit=1.0)
        await h.start(dev)
        np = neopixel.strips[0]
        assert np.shown == [BLACK] * 4
        await h.fms("field/clock", {"t": FIELD_MS})
        # Blink runs on the field clock: red for the first half of each 200 ms period; "at" only delays the start.
        await h.fms("dev/goal-red/out/strip", {"p": "blink", "c": ["#ff0000"], "ms": 200, "at": FIELD_MS + 400})
        await h.step(dev, 395)
        assert np.shown == [BLACK] * 4
        await h.step(dev, 5)
        assert np.shown == [RED] * 4
        await h.step(dev, 100)
        assert np.shown == [BLACK] * 4
        writes = np.writes
        await h.step(dev, 20)
        assert np.writes == writes  # nothing changed, nothing written
        await h.fms("dev/goal-red/cmd", {"do": "identify"})
        assert np.shown == [(128, 128, 128)] * 4
        assert strip.frame == np.shown

    run(clock, scenario)


def test_pending_device_keeps_its_connection_and_is_approved_later(clock):
    async def scenario(h):
        dev = galaxia.Device("beam-red-1")
        lights = []
        dev.digital_out("light", on_change=lights.append)
        dev.counter("count")
        await h.fms("dev/beam-red-1/out/light", {"v": 1}, retain=True)
        await h.start(dev, approve=False)
        await h.step(dev, 30_000)
        assert dev.connected and not dev.approved
        assert BROKER.connects == 1  # no reconnection loop, and no asking again
        assert BROKER.messages("galaxia/v1/dev/beam-red-1/status")[-1][2].startswith(b'{"online":true')
        # Approval drops the connection; mqtt_as reconnects and the device announces itself again.
        BROKER.approve("beam-red-1")
        await h.step(dev, 10)
        assert dev.approved
        assert BROKER.connects == 2
        assert lights == [True]
        assert len(BROKER.messages("galaxia/v1/dev/beam-red-1/manifest")) == 2
        assert h.sent(dev, "in/count")[-1]["v"] == 0

    run(clock, scenario)


def test_counter_total_follows_the_manifest(clock):
    async def scenario(h):
        dev = galaxia.Device("beam-red-1")
        balls = dev.counter("count")
        await h.start(dev)
        balls.increment(3)
        await settle()
        BROKER.drop(BROKER.clients[0])
        await h.step(dev, 10)
        order = [entry[1].rsplit("/", 1)[-1] for entry in BROKER.published
                 if entry[0] == "beam-red-1" and entry[1].rsplit("/", 1)[-1] in ("manifest", "count")]
        assert order == ["manifest", "count", "count", "manifest", "count"]
        assert [m["v"] for m in h.sent(dev, "in/count")] == [0, 3, 3]

    run(clock, scenario)


def test_unrecognised_refusal_backs_off_instead_of_looping(clock):
    async def scenario(h):
        BROKER.suback_message = "something else 0x%x"  # a future mqtt_as that words it differently
        dev = galaxia.Device("beam-red-1")
        dev.digital_out("light")
        await h.start(dev, approve=False)
        await h.step(dev, 1000)
        assert BROKER.connects == 2  # one drop, then it stays connected without asking
        assert dev.connected and not dev.approved
        await h.step(dev, 28_000)
        assert BROKER.connects == 2
        BROKER.approve("beam-red-1")  # drops the connection; the next request waits for the 30 s
        await h.step(dev, 1100)
        assert dev.approved
        assert BROKER.connects == 3

    run(clock, scenario)


def test_refused_connections_are_reported_and_retried_slowly(clock, monkeypatch, capsys):
    monkeypatch.setattr(galaxia, "REFUSED_RETRY_MS", 1)

    async def scenario(h):
        BROKER.blocked.add("arcade-1")
        dev = galaxia.Device("arcade-1")
        dev.digital("button")
        dev.wifi("field-devices", "pw")
        dev.connect("10.0.100.5", "tok")
        await dev.start(ticker=False)
        await asyncio.sleep(0.05)
        assert dev.refusal == 5 and not dev.connected
        assert "blocked on the Devices page" in capsys.readouterr().out
        BROKER.blocked.discard("arcade-1")
        BROKER.approved.add("arcade-1")
        await asyncio.sleep(2.5)  # the SDK's own backoff: 1 s, then 2 s
        assert dev.connected and dev.refusal is None

        BROKER.tokens["arcade-1"] = "another-token"
        BROKER.drop(BROKER.clients[0])
        await asyncio.sleep(0.05)
        assert dev.refusal == 4
        assert "wrong token" in capsys.readouterr().out

    run(clock, scenario)
    with pytest.raises(ValueError):
        galaxia.Device("plc")


def test_reconnect_republishes_and_drops_stale_inputs(clock):
    async def scenario(h):
        dev = galaxia.Device("arcade-1")
        balls = dev.counter("count")
        await h.start(dev)
        balls.increment()
        await settle()
        BROKER.reachable = False
        BROKER.drop(BROKER.clients[0])
        await settle()
        assert BROKER.retained["galaxia/v1/dev/arcade-1/status"] == b'{"online":false}'
        balls.increment()  # not sent while down
        await settle()
        assert [m["v"] for m in h.sent(dev, "in/count")] == [0, 1]
        BROKER.reachable = True
        await h.step(dev, 10)
        assert dev.connected and dev.approved
        assert [m["v"] for m in h.sent(dev, "in/count")] == [0, 1, 2]
        assert len(BROKER.messages("galaxia/v1/dev/arcade-1/manifest")) == 2

    run(clock, scenario)


def test_field_state_clock_and_reboot(clock):
    async def scenario(h):
        dev = galaxia.Device("timer-1")
        await h.start(dev, approve=False)
        await h.fms("field/state", {"state": "running", "phase": "auto", "phase_remaining": 15.0,
                                    "scores": {"red": 1, "blue": 2}}, retain=True)
        await h.step(dev, 2500)
        assert dev.phase_remaining() == 12.5
        assert dev.field_state.scores == {"red": 1, "blue": 2}
        await h.fms("field/clock", {"t": FIELD_MS})
        assert dev.field_time_ms() == FIELD_MS
        await h.step(dev, 250)
        assert dev.field_time_ms() == FIELD_MS + 250
        BROKER.approve("timer-1")
        await h.step(dev, 10)
        await h.fms("dev/timer-1/cmd", {"do": "reboot"})
        assert machine.resets == [True]
        assert BROKER.retained["galaxia/v1/dev/timer-1/status"] == b'{"online":false}'

    run(clock, scenario)


def test_renderer_matches_the_shared_cases():
    with open(CASES) as handle:
        cases = json.load(handle)
    for case in cases:
        frame = galaxia.render(case["cmd"], case["pixels"], case["t"])
        assert [list(c) for c in frame] == case["frame"], case


def _single(value):
    """Rounds every float in a command to single precision, as MicroPython on the Pico stores them."""
    import struct

    if isinstance(value, float):
        return struct.unpack("f", struct.pack("f", value))[0]
    if isinstance(value, dict):
        return {key: _single(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_single(item) for item in value]
    return value


def test_single_precision_numbers(monkeypatch):
    """The Pico reads JSON numbers as single-precision floats, so 0.35 x 10 can come out as 3.4999998 where the
    reference has 3.5. With the rounding the module uses on such boards, fills and brightness still match exactly.
    Pulse and rainbow colours are themselves fractions: at the odd instant one lands on a half, a channel can be one
    step (of 255) away from the preview."""
    with open(CASES) as handle:
        cases = json.load(handle)
    monkeypatch.setattr(galaxia, "_ROUND_HALF", 0.5)
    exact_rule_misses = sum(galaxia.render(_single(c["cmd"]), c["pixels"], c["t"]) != [tuple(p) for p in c["frame"]]
                            for c in cases)
    assert exact_rule_misses > 0  # the problem is real
    monkeypatch.setattr(galaxia, "_ROUND_HALF", 0.4999)
    for case in cases:
        frame = galaxia.render(_single(case["cmd"]), case["pixels"], case["t"])
        looks = [case["cmd"]] + [s for s in case["cmd"].get("seg", []) if isinstance(s, dict)]
        tolerance = 1 if any(look.get("p") in ("pulse", "rainbow") for look in looks) else 0
        worst = max(abs(got - want) for color, reference in zip(frame, case["frame"])
                    for got, want in zip(color, reference))
        assert worst <= tolerance, case


def test_ids_are_checked():
    with pytest.raises(ValueError):
        galaxia.Device("Beam")
    dev = galaxia.Device("beam")
    with pytest.raises(ValueError):
        dev.counter("Count")
