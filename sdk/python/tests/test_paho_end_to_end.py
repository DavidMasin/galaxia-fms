"""The SDK with the real paho-mqtt client over a real socket, against the small broker in mini_broker.py."""

import json
import time

import pytest

from galaxia import Device, Refused, device as device_module
from mini_broker import MiniBroker


def wait_for(condition, timeout=5.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return True
        time.sleep(0.01)
    return False


@pytest.fixture
def broker():
    instance = MiniBroker()
    yield instance
    instance.close()


@pytest.fixture
def fast(monkeypatch):
    monkeypatch.setattr(device_module, "STATUS_INTERVAL_MS", 500)


def test_pending_then_approved_with_real_paho(broker, fast):
    dev = Device("estop-box-red", "E-stop box", board="test")
    stop = dev.safety_stop("stop")
    lights = []
    dev.digital_out("light", on_change=lights.append)
    broker.publish("galaxia/v1/dev/estop-box-red/out/light", b'{"v":1}', retain=True)
    stop.set(True)
    dev.connect("127.0.0.1", "tok", port=broker.port)
    try:
        assert wait_for(lambda: dev.connected)
        manifest = json.loads(broker.retained["galaxia/v1/dev/estop-box-red/manifest"])
        assert manifest["channels"][0] == {"id": "stop", "dir": "in", "type": "digital", "safety": True}
        time.sleep(0.5)
        assert not dev.approved
        assert lights == []
        assert broker.payloads("galaxia/v1/dev/estop-box-red/hb") == []  # refused while pending

        # Approval drops the connection; paho connects again by itself (after about a second).
        broker.approve("estop-box-red")
        assert wait_for(lambda: dev.approved, timeout=10)
        assert wait_for(lambda: lights == [True])
        assert len(broker.payloads("galaxia/v1/dev/estop-box-red/manifest")) == 2
        before = len(broker.payloads("galaxia/v1/dev/estop-box-red/hb"))
        time.sleep(1.0)
        beats = broker.payloads("galaxia/v1/dev/estop-box-red/hb")[before:]
        assert 4 <= len(beats) <= 6
        assert json.loads(beats[-1])["ch"] == {"stop": 1}
        statuses = broker.payloads("galaxia/v1/dev/estop-box-red/status")
        assert len(statuses) >= 3
        assert json.loads(statuses[-1])["ip"] == "127.0.0.1"
    finally:
        dev.disconnect()
    assert broker.retained["galaxia/v1/dev/estop-box-red/status"] == b'{"online":false}'


def test_last_will_and_reconnect_with_real_paho(broker, fast):
    broker.approve("beam-red-1")
    dev = Device("beam-red-1", "Red goal beam break", board="test")
    balls = dev.counter("count")
    dev.connect("127.0.0.1", "tok", port=broker.port)
    try:
        assert wait_for(lambda: dev.approved)
        # The baseline right after the manifest, then the increments.
        assert broker.payloads("galaxia/v1/dev/beam-red-1/in/count")[0] == b'{"v":0,"seq":1}'
        balls.increment(2)
        assert wait_for(lambda: b'{"v":2,"seq":2}' in broker.payloads("galaxia/v1/dev/beam-red-1/in/count"))
        broker.kick("beam-red-1")
        assert wait_for(lambda: broker.retained["galaxia/v1/dev/beam-red-1/status"] == b'{"online":false}')
        assert wait_for(lambda: dev.approved and broker.retained["galaxia/v1/dev/beam-red-1/status"]
                        .startswith(b'{"online":true'), timeout=10)
        assert len(broker.payloads("galaxia/v1/dev/beam-red-1/manifest")) == 2
        assert json.loads(broker.payloads("galaxia/v1/dev/beam-red-1/in/count")[-1])["v"] == 2
    finally:
        dev.disconnect()


def test_refusals_with_real_paho(broker):
    broker.block("arcade-1")
    dev = Device("arcade-1", "Start button", board="test")
    dev.digital("button")
    dev.connect("127.0.0.1", "tok", port=broker.port)
    try:
        assert wait_for(lambda: dev.refusal is not None)
        assert dev.refusal.code == 5
        with pytest.raises(Refused, match="blocked"):
            dev.wait_connected(timeout=1)
        # Unblocked: paho's next retry (it waits 1, 2, 4 ... seconds) gets in.
        broker.block("arcade-1", blocked=False)
        dev.wait_connected(timeout=10)
        assert dev.refusal is None
    finally:
        dev.disconnect()

    broker.approve("vision-1")
    broker.rules.tokens["vision-1"] = "right"
    other = Device("vision-1", board="test")
    other.connect("127.0.0.1", "wrong", port=broker.port)
    try:
        assert wait_for(lambda: other.refusal is not None)
        assert other.refusal.code == 4
    finally:
        other.disconnect()
