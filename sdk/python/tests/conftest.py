import json
import os
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))
sys.path.insert(0, os.path.dirname(__file__))

from fake_broker import FakeBroker  # noqa: E402
from galaxia import Device  # noqa: E402

FIELD_EPOCH_MS = 1_794_510_000_000


class Clock:
    """A monotonic clock (seconds) that only moves when a test says so."""

    def __init__(self):
        self.ms = 100_000

    @property
    def seconds(self):
        return self.ms / 1000.0

    def __call__(self):
        return self.ms / 1000.0

    def advance_ms(self, ms):
        self.ms += ms


class Env:
    def __init__(self):
        self.broker = FakeBroker()
        self.clock = Clock()
        # The system clock is deliberately wrong, so tests notice if field/clock is not used once it arrives.
        self.wall = lambda: 1_600_000_000.0 + self.clock.seconds

    def device(self, device_id="beam-red-1", name="Red goal beam break", **kwargs):
        kwargs.setdefault("board", "test-board")
        return Device(device_id, name, client_factory=self.broker.client, monotonic=self.clock, wall=self.wall,
                      **kwargs)

    def start(self, dev, token="secret-token", approve=True):
        if approve:
            self.broker.approve(dev.id)
        dev.connect("10.0.100.5", token, background=False)
        self.broker.pump()
        dev.loop()
        self.broker.pump()

    def step(self, dev, ms, tick=10):
        """Moves time forward in loop-sized ticks, letting the network and the device run in between."""
        elapsed = 0
        while elapsed < ms:
            delta = min(tick, ms - elapsed)
            self.clock.advance_ms(delta)
            elapsed += delta
            self.broker.pump()
            dev.loop()
            self.broker.pump()

    def fms(self, topic, payload, retain=False):
        data = payload if isinstance(payload, (bytes, str)) else json.dumps(payload)
        self.broker.publish("galaxia/v1/" + topic, data, retain)
        self.broker.pump()

    def sent(self, dev, suffix):
        """The JSON payloads the device published on dev/{id}/{suffix}."""
        topic = "galaxia/v1/dev/%s/%s" % (dev.id, suffix)
        return [json.loads(m.payload) for m in self.broker.messages(topic)]


@pytest.fixture
def env():
    return Env()
