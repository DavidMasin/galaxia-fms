import json

import pytest
from paho.mqtt.client import CallbackAPIVersion, MQTTv311

from galaxia import SDK, Device, Refused
from galaxia.device import paho_client


def full_device(env):
    dev = env.device(fw="2.1.0")
    dev.counter("count", "Balls through goal")
    dev.digital("beam", "Beam state")
    dev.analog("weight", unit="kg", min=0, max=40)
    dev.encoder("turret")
    dev.text("tag")
    dev.safety_stop("stop", "E-stop")
    dev.digital_out("light")
    dev.relay("horn")
    dev.pwm("fan", unit="%", min=0, max=100)
    dev.led_strip("strip", 30, "Goal lights")
    dev.display("timer")
    dev.sound("buzzer")
    return dev


def test_manifest_content_and_retention(env):
    dev = full_device(env)
    env.start(dev)
    [message] = env.broker.messages("galaxia/v1/dev/beam-red-1/manifest")
    assert message.retain is True
    assert env.broker.retained["galaxia/v1/dev/beam-red-1/manifest"] == message.payload
    manifest = json.loads(message.payload)
    assert manifest == {
        "proto": 1,
        "id": "beam-red-1",
        "name": "Red goal beam break",
        "fw": "2.1.0",
        "sdk": SDK,
        "board": "test-board",
        "channels": [
            {"id": "count", "dir": "in", "type": "counter", "name": "Balls through goal"},
            {"id": "beam", "dir": "in", "type": "digital", "name": "Beam state"},
            {"id": "weight", "dir": "in", "type": "analog", "unit": "kg", "min": 0, "max": 40},
            {"id": "turret", "dir": "in", "type": "encoder"},
            {"id": "tag", "dir": "in", "type": "text"},
            {"id": "stop", "dir": "in", "type": "digital", "name": "E-stop", "safety": True},
            {"id": "light", "dir": "out", "type": "digital"},
            {"id": "horn", "dir": "out", "type": "relay"},
            {"id": "fan", "dir": "out", "type": "pwm", "unit": "%", "min": 0, "max": 100},
            {"id": "strip", "dir": "out", "type": "led_strip", "name": "Goal lights", "pixels": 30},
            {"id": "timer", "dir": "out", "type": "display"},
            {"id": "buzzer", "dir": "out", "type": "sound"},
        ],
    }
    assert SDK.startswith("galaxia-python/")


def test_connection_settings_and_last_will(env):
    dev = env.device()
    env.start(dev, token="tok-123")
    client = env.broker.clients["beam-red-1"]
    assert client.username == "beam-red-1"
    assert client.password == "tok-123"
    assert client.keepalive == 15
    assert client.address == ("10.0.100.5", 1883)
    assert client.will == ("galaxia/v1/dev/beam-red-1/status", b'{"online":false}', True)

    env.broker.drop("beam-red-1")
    env.broker.pump()
    assert env.broker.retained["galaxia/v1/dev/beam-red-1/status"] == b'{"online":false}'
    assert not dev.connected


def test_real_paho_client_is_configured_the_same_way():
    created = []

    def factory(device_id):
        client = paho_client(device_id)
        client.loop_start = lambda: None  # configure only; no network in this test
        created.append(client)
        return client

    dev = Device("vision-1", "Vision", board="x", client_factory=factory)
    try:
        dev.connect("127.0.0.1", "tok", port=1, background=False)
        client = created[0]
        assert client._callback_api_version == CallbackAPIVersion.VERSION2
        assert client._protocol == MQTTv311
        assert client._client_id == b"vision-1"
        assert client._username == b"vision-1"
        assert client._password == b"tok"
        assert client._will is True
        assert client._will_topic == b"galaxia/v1/dev/vision-1/status"
        assert client._will_payload == b'{"online":false}'
        assert client._will_retain is True
        assert client._keepalive == 15
    finally:
        dev.disconnect()


def test_status_every_five_seconds_retained(env):
    dev = env.device()
    env.start(dev)
    env.step(dev, 20_000)
    statuses = [m for m in env.broker.messages("galaxia/v1/dev/beam-red-1/status")]
    # One on connect, then at 5, 10, 15 and 20 seconds.
    assert len(statuses) == 5
    assert all(m.retain for m in statuses)
    bodies = [json.loads(m.payload) for m in statuses]
    assert all(body["online"] is True for body in bodies)
    assert [body["up"] for body in bodies] == [0, 5, 10, 15, 20]


def test_offline_status_on_clean_disconnect(env):
    dev = env.device()
    env.start(dev)
    dev.disconnect()
    env.broker.pump()
    assert env.broker.retained["galaxia/v1/dev/beam-red-1/status"] == b'{"online":false}'


def test_subscriptions(env):
    dev = env.device()
    env.start(dev)
    client = env.broker.clients["beam-red-1"]
    assert sorted(client.subscriptions) == [
        "galaxia/v1/dev/beam-red-1/cmd",
        "galaxia/v1/dev/beam-red-1/out/+",
        "galaxia/v1/field/#",
    ]
    assert dev.approved


def test_pending_device_gets_its_outputs_after_approval(env):
    dev = env.device()
    light = dev.digital_out("light")
    balls = dev.counter("count")
    env.fms("dev/beam-red-1/out/light", {"v": 1}, retain=True)
    env.start(dev, approve=False)
    assert dev.connected and not dev.approved
    assert env.broker.clients["beam-red-1"].subscriptions == ["galaxia/v1/field/#"]
    balls.increment()
    assert env.broker.refused[-1].topic == "galaxia/v1/dev/beam-red-1/in/count"
    env.step(dev, 30_000)
    assert not dev.approved  # no periodic asking: the FMS says when, by dropping the connection

    # Approving drops the connection; the device connects again and announces itself afresh.
    env.broker.approve("beam-red-1")
    env.broker.pump()
    assert dev.connected and dev.approved
    assert light.value is True  # the retained output arrives with the subscription
    assert len(env.broker.messages("galaxia/v1/dev/beam-red-1/manifest")) == 2
    assert env.sent(dev, "in/count")[-1]["v"] == 1


def test_counter_totals_follow_the_manifest_on_every_connection(env):
    dev = env.device()
    balls = dev.counter("count")
    env.start(dev)
    balls.increment(2)
    env.broker.drop(dev.id)
    env.broker.pump()
    dev._client.reconnect()
    env.broker.pump()
    topics = [m.topic.rsplit("/", 2)[-2:] for m in env.broker.published if m.client_id == dev.id]
    sequence = ["/".join(t) for t in topics if t[-1] in ("manifest", "count")]
    # manifest, baseline 0, increments, then after reconnecting: manifest, the total so far.
    assert sequence == ["beam-red-1/manifest", "in/count", "in/count", "beam-red-1/manifest", "in/count"]
    assert [m["v"] for m in env.sent(dev, "in/count")] == [0, 2, 2]


def test_reconnect_republishes_everything(env):
    dev = env.device()
    balls = dev.counter("count")
    env.start(dev)
    balls.increment(3)
    env.broker.drop("beam-red-1")
    env.broker.pump()
    balls.increment()  # not sent while disconnected; the total catches up on reconnect
    dev._client.reconnect()
    env.broker.pump()
    assert dev.connected
    assert len(env.broker.messages("galaxia/v1/dev/beam-red-1/manifest")) == 2
    assert env.broker.retained["galaxia/v1/dev/beam-red-1/status"].startswith(b'{"online":true')
    assert env.sent(dev, "in/count")[-1]["v"] == 4


def test_blocked_device_is_reported_and_recovers_when_unblocked(env, caplog):
    env.broker.blocked.add("beam-red-1")
    dev = env.device()
    dev.connect("10.0.100.5", "tok", background=False)
    env.broker.pump()
    assert not dev.connected
    assert isinstance(dev.refusal, Refused) and dev.refusal.code == 5
    assert "blocked" in caplog.text
    with pytest.raises(Refused, match="blocked"):
        dev.wait_connected(timeout=0.1)
    env.broker.blocked.discard("beam-red-1")
    env.broker.approve("beam-red-1")
    dev._client.reconnect()  # paho's own retry
    env.broker.pump()
    assert dev.connected and dev.refusal is None
    dev.wait_connected(timeout=0.1)


def test_wrong_token_is_reported(env, caplog):
    env.broker.approve("beam-red-1")
    env.broker.tokens["beam-red-1"] = "the-right-token"
    dev = env.device()
    dev.connect("10.0.100.5", "a-wrong-token", background=False)
    env.broker.pump()
    assert dev.refusal.code == 4
    assert "token" in caplog.text


def test_paho_retries_refusals_slowly():
    created = []

    def factory(device_id):
        client = paho_client(device_id)
        client.loop_start = lambda: None
        created.append(client)
        return client

    dev = Device("vision-1", board="x", client_factory=factory)
    dev.connect("127.0.0.1", "tok", port=1, background=False)
    try:
        # paho waits 1 s, then doubles up to this maximum, and only resets the wait after a successful CONNACK.
        assert created[0]._reconnect_max_delay == 30
        assert created[0]._reconnect_on_failure is True
    finally:
        dev.disconnect()


@pytest.mark.parametrize("bad", ["Beam", "beam_1", "", "x" * 41, "beam red", "plc", "inline"])
def test_device_id_is_checked(bad):
    with pytest.raises(ValueError):
        Device(bad)


def test_channel_ids_are_checked(env):
    dev = env.device()
    with pytest.raises(ValueError):
        dev.counter("Count")
    dev.counter("count")
    with pytest.raises(ValueError):
        dev.digital("count")
