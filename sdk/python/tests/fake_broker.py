"""An in-memory stand-in for the FMS broker and for the paho-mqtt client the SDK talks to.

The broker enforces the access rules of docs/devices.md (pending devices may only publish their manifest and status
and subscribe to field/#), keeps retained messages, and publishes a client's Last Will when its connection drops.
Callbacks are queued and run by pump(), so tests decide exactly when each network event happens. The callback
arguments are paho's own types (ReasonCode, ConnectFlags, DisconnectFlags, MQTTMessage) with the version 2 callback
signatures.
"""

from collections import namedtuple

from paho.mqtt.client import ConnectFlags, DisconnectFlags, MQTTMessage
from paho.mqtt.packettypes import PacketTypes
from paho.mqtt.reasoncodes import ReasonCode

PREFIX = "galaxia/v1/"

Published = namedtuple("Published", "client_id topic payload retain")


def topic_matches(pattern, topic):
    pattern_parts = pattern.split("/")
    topic_parts = topic.split("/")
    for i, part in enumerate(pattern_parts):
        if part == "#":
            return True
        if i >= len(topic_parts):
            return False
        if part != "+" and part != topic_parts[i]:
            return False
    return len(pattern_parts) == len(topic_parts)


class PublishInfo:
    def __init__(self, rc, mid):
        self.rc = rc
        self.mid = mid


class FakeBroker:
    def __init__(self):
        self.retained = {}
        self.published = []
        self.refused = []
        self.clients = {}
        self.approved = set()
        self.blocked = set()
        self.tokens = {}
        self._queue = []

    # Access rules

    def _device_topic(self, client_id, suffix):
        return PREFIX + "dev/" + client_id + "/" + suffix

    def may_publish(self, client_id, topic):
        allowed = ["manifest", "status"]
        if client_id in self.approved:
            allowed += ["in/+", "hb"]
        return any(topic_matches(self._device_topic(client_id, suffix), topic) for suffix in allowed)

    def may_subscribe(self, client_id, pattern):
        if pattern == PREFIX + "field/#":
            return True
        if client_id in self.approved:
            return pattern in (self._device_topic(client_id, "out/+"), self._device_topic(client_id, "cmd"))
        return False

    # Event queue

    def later(self, function, *args):
        self._queue.append((function, args))

    def pump(self):
        while self._queue:
            function, args = self._queue.pop(0)
            function(*args)

    # Broker behaviour

    def client(self, client_id):
        """Factory with the same shape as galaxia.device.paho_client."""
        return FakeClient(self, client_id)

    def approve(self, client_id):
        """Approves a device. Like the FMS, this drops a connected device's connection, and the client (as paho
        does) connects again by itself."""
        self.approved.add(client_id)
        client = self.clients.get(client_id)
        if client is not None and client.connected:
            self.drop(client_id)
            self.later(client.reconnect)

    def block(self, client_id):
        self.blocked.add(client_id)
        self.approved.discard(client_id)
        client = self.clients.get(client_id)
        if client is not None and client.connected:
            self.drop(client_id)
            self.later(client.reconnect)

    def connect(self, client):
        if client.client_id in self.blocked:
            self.later(client._connected, ReasonCode(PacketTypes.CONNACK, "Not authorized"))
            return
        token = self.tokens.get(client.client_id)
        if client.client_id in self.approved and token is not None and token != client.password:
            self.later(client._connected, ReasonCode(PacketTypes.CONNACK, "Bad user name or password"))
            return
        old = self.clients.get(client.client_id)
        if old is not None and old is not client:
            self.drop(client.client_id)
        self.clients[client.client_id] = client
        client.connected = True
        client.subscriptions = []
        self.later(client._connected, ReasonCode(PacketTypes.CONNACK, "Success"))

    def publish(self, topic, payload, retain=False, client_id=None):
        """Publishes a message as the FMS would (client_id None) or on behalf of a client."""
        if isinstance(payload, str):
            payload = payload.encode()
        self.published.append(Published(client_id, topic, payload, retain))
        if retain:
            if payload:
                self.retained[topic] = payload
            else:
                self.retained.pop(topic, None)
        for client in list(self.clients.values()):
            if client.connected and any(topic_matches(p, topic) for p in client.subscriptions):
                self.later(client._deliver, topic, payload, False)

    def subscribe(self, client, patterns):
        codes = []
        granted = []
        for pattern in patterns:
            if self.may_subscribe(client.client_id, pattern):
                codes.append(ReasonCode(PacketTypes.SUBACK, identifier=0))
                if pattern not in client.subscriptions:
                    client.subscriptions.append(pattern)
                granted.append(pattern)
            else:
                codes.append(ReasonCode(PacketTypes.SUBACK, identifier=0x80))
        return codes, granted

    def deliver_retained(self, client, patterns):
        for topic, payload in list(self.retained.items()):
            if any(topic_matches(p, topic) for p in patterns):
                self.later(client._deliver, topic, payload, True)

    def drop(self, client_id):
        """The connection breaks without a DISCONNECT: the broker publishes the Last Will."""
        client = self.clients.pop(client_id, None)
        if client is None or not client.connected:
            return
        client.connected = False
        if client.will is not None:
            topic, payload, retain = client.will
            self.publish(topic, payload, retain, client_id)
        self.later(client._disconnected, False, ReasonCode(PacketTypes.DISCONNECT, "Unspecified error"))

    def messages(self, topic):
        return [m for m in self.published if m.topic == topic]


class FakeClient:
    """The subset of paho.mqtt.client.Client that the SDK uses."""

    def __init__(self, broker, client_id):
        self.broker = broker
        self.client_id = client_id
        self.username = None
        self.password = None
        self.will = None
        self.reconnect_delay = None
        self.address = None
        self.keepalive = None
        self.loop_running = False
        self.connected = False
        self.subscriptions = []
        self._mid = 0
        self.on_connect = None
        self.on_disconnect = None
        self.on_message = None
        self.on_subscribe = None

    def username_pw_set(self, username, password=None):
        self.username = username
        self.password = password

    def will_set(self, topic, payload=None, qos=0, retain=False, properties=None):
        if isinstance(payload, str):
            payload = payload.encode()
        self.will = (topic, payload, retain)

    def reconnect_delay_set(self, min_delay=1, max_delay=120):
        self.reconnect_delay = (min_delay, max_delay)

    def connect_async(self, host, port=1883, keepalive=60, **kwargs):
        self.address = (host, port)
        self.keepalive = keepalive

    def loop_start(self):
        self.loop_running = True
        self.broker.connect(self)

    def loop_stop(self):
        self.loop_running = False

    def reconnect(self):
        """What paho's network thread does after a dropped connection."""
        self.broker.connect(self)

    def is_connected(self):
        return self.connected

    def socket(self):
        return None

    def _next_mid(self):
        self._mid += 1
        return self._mid

    def publish(self, topic, payload=None, qos=0, retain=False, properties=None):
        mid = self._next_mid()
        if not self.connected:
            return PublishInfo(4, mid)
        if isinstance(payload, str):
            payload = payload.encode()
        if self.broker.may_publish(self.client_id, topic):
            self.broker.publish(topic, payload or b"", retain, self.client_id)
        else:
            self.broker.refused.append(Published(self.client_id, topic, payload, retain))
        return PublishInfo(0, mid)

    def subscribe(self, topic, qos=0, options=None, properties=None):
        if isinstance(topic, str):
            patterns = [topic]
        elif isinstance(topic, tuple):
            patterns = [topic[0]]
        else:
            patterns = [entry[0] for entry in topic]
        mid = self._next_mid()
        if not self.connected:
            return 4, mid
        codes, granted = self.broker.subscribe(self, patterns)
        self.broker.later(self._subscribed, mid, codes)
        self.broker.deliver_retained(self, granted)
        return 0, mid

    def disconnect(self, reasoncode=None, properties=None):
        if self.connected:
            self.connected = False
            self.broker.clients.pop(self.client_id, None)
            self.broker.later(self._disconnected, True, ReasonCode(PacketTypes.DISCONNECT, "Normal disconnection"))
        return 0

    # Callbacks, in paho's version 2 shapes

    def _connected(self, reason_code):
        if self.on_connect:
            self.on_connect(self, None, ConnectFlags(session_present=False), reason_code, None)

    def _disconnected(self, from_server, reason_code):
        if self.on_disconnect:
            flags = DisconnectFlags(is_disconnect_packet_from_server=from_server)
            self.on_disconnect(self, None, flags, reason_code, None)

    def _subscribed(self, mid, codes):
        if self.on_subscribe:
            self.on_subscribe(self, None, mid, codes, None)

    def _deliver(self, topic, payload, retained):
        if not self.connected or not self.on_message:
            return
        message = MQTTMessage(topic=topic.encode())
        message.payload = payload
        message.retain = retained
        self.on_message(self, None, message)
