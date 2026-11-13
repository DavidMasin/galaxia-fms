"""Stand-in for mqtt_as 0.8.5 (peterhinch/micropython-mqtt), with an in-memory broker.

It copies the behaviour of the real client that galaxia.py depends on, as read from mqtt_as/__init__.py:
- the event interface (config["queue_len"] > 0): .up and .down events and an async .queue of
  (topic, message, retained);
- publish() and subscribe() wait for the connection; subscribe() retries after a reconnection;
- a SUBACK with a failure code makes wait_msg() raise OSError(-1, "SUBACK reason code 0x80") after reading the
  whole packet; the message task treats any error as a broken connection (down, then an automatic reconnection),
  and a subscribe() still waiting for its packet id then fails and tries again;
- the Last Will is published by the broker when the connection breaks, not on disconnect().
"""

import asyncio

config = {
    "client_id": b"0102",
    "server": None,
    "port": 0,
    "user": "",
    "password": "",
    "keepalive": 60,
    "ping_interval": 0,
    "ssl": False,
    "ssl_params": {},
    "response_time": 10,
    "clean_init": True,
    "clean": True,
    "max_repubs": 4,
    "will": None,
    "subs_cb": lambda *_: None,
    "wifi_coro": None,
    "connect_coro": None,
    "ssid": None,
    "wifi_pw": None,
    "queue_len": 0,
    "gateway": False,
    "mqttv5": False,
    "mqttv5_con_props": None,
}

PREFIX = "galaxia/v1/"


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


def _text(value):
    return value.decode() if isinstance(value, (bytes, bytearray)) else value


def _bytes(value):
    return value.encode() if isinstance(value, str) else bytes(value)


class Broker:
    def __init__(self):
        self.reset()

    def reset(self):
        self.approved = set()
        self.blocked = set()
        self.tokens = {}
        self.reachable = True
        self.retained = {}
        self.published = []  # (client_id, topic, payload, retain)
        self.refused = []
        self.clients = []
        self.connects = 0
        self.suback_message = "SUBACK reason code 0x%x"

    def may_publish(self, client_id, topic):
        allowed = ["manifest", "status"] + (["in/+", "hb"] if client_id in self.approved else [])
        return any(topic_matches(PREFIX + "dev/" + client_id + "/" + s, topic) for s in allowed)

    def may_subscribe(self, client_id, pattern):
        if pattern == PREFIX + "field/#":
            return True
        mine = (PREFIX + "dev/" + client_id + "/out/+", PREFIX + "dev/" + client_id + "/cmd")
        return client_id in self.approved and pattern in mine

    def publish(self, topic, payload, retain=False, client_id=None):
        payload = _bytes(payload)
        self.published.append((client_id, topic, payload, retain))
        if retain:
            if payload:
                self.retained[topic] = payload
            else:
                self.retained.pop(topic, None)
        for client in self.clients:
            if client._isconnected and any(topic_matches(p, topic) for p in client.subscriptions):
                client.queue.put(topic.encode(), payload, False)

    def drop(self, client):
        """The network fails under the client (or the FMS closes the connection)."""
        client._reconnect()

    def approve(self, client_id):
        """Like the FMS: approving drops a connected device so that it reconnects with its new rights."""
        self.approved.add(client_id)
        for client in self.clients:
            if client.client_id == client_id and client._isconnected:
                self.drop(client)

    def connack(self, client):
        """The CONNACK return code the FMS would send (MQTT 3.1.1)."""
        if client.client_id in ("plc", "inline"):
            return 2
        if client.client_id in self.blocked:
            return 5
        token = self.tokens.get(client.client_id)
        if client.client_id in self.approved and token is not None and token != client.config["password"]:
            return 4
        return 0

    def messages(self, topic):
        return [entry for entry in self.published if entry[1] == topic]


BROKER = Broker()


class MsgQueue:
    def __init__(self, size):
        self._items = []
        self._size = max(size, 1)
        self._event = asyncio.Event()
        self.discards = 0

    def put(self, *item):
        self._items.append(item)
        if len(self._items) > self._size:
            self._items.pop(0)
            self.discards += 1
        self._event.set()

    def __aiter__(self):
        return self

    async def __anext__(self):
        while not self._items:
            self._event.clear()
            await self._event.wait()
        return self._items.pop(0)


class MQTT_base:
    DEBUG = False

    def __init__(self, cfg):
        assert cfg["queue_len"] > 0, "galaxia uses the event interface"
        self.config = dict(cfg)
        self.client_id = _text(cfg["client_id"])
        self.up = asyncio.Event()
        self.down = asyncio.Event()
        self.queue = MsgQueue(cfg["queue_len"])
        self.rcv_pids = set()
        self.subscriptions = []
        self._pid = 0
        self._isconnected = False
        self._suback = None
        self.disconnected_cleanly = False
        BROKER.clients.append(self)

    def isconnected(self):
        return self._isconnected

    async def wait_msg(self):
        """Reads the SUBACK of the last SUBSCRIBE, as the real wait_msg() does."""
        if self._suback is None:
            return
        pid, code = self._suback
        self._suback = None
        if code >= 0x80:
            raise OSError(-1, BROKER.suback_message % code)
        self.rcv_pids.discard(pid)


    async def _connect(self, clean):
        """Sends CONNECT and reads CONNACK; a refusal raises as in the real client."""
        code = BROKER.connack(self)
        if code:
            raise OSError(-1, "CONNACK reason code 0x%x" % code)

    def _close(self):
        self.closed = getattr(self, "closed", 0) + 1


class MQTTClient(MQTT_base):
    async def connect(self, *, quick=False):
        if not BROKER.reachable:
            raise OSError(-1, "Wi-Fi connect timed out")
        BROKER.attempts = getattr(BROKER, "attempts", 0) + 1
        try:
            await self._connect(True)
        except Exception:
            self._close()
            raise
        BROKER.connects += 1
        self.subscriptions = []
        self._isconnected = True
        self.up.set()

    async def _connection(self):
        while not self._isconnected:
            await asyncio.sleep(0)

    def _reconnect(self):
        if not self._isconnected:
            return
        self._isconnected = False
        will = self.config["will"]
        if will is not None:
            topic, message, retain, _ = will
            BROKER.publish(_text(topic), message, retain, self.client_id)
        self.down.set()
        asyncio.create_task(self._keep_connected())

    async def _keep_connected(self):
        while not self._isconnected:
            await asyncio.sleep(0)
            if BROKER.reachable:
                try:
                    await self.connect()
                except OSError:
                    pass  # the real client also just tries again

    async def publish(self, topic, msg, retain=False, qos=0, properties=None):
        await self._connection()
        topic = _text(topic)
        if BROKER.may_publish(self.client_id, topic):
            BROKER.publish(topic, msg, retain, self.client_id)
        else:
            BROKER.refused.append((self.client_id, topic, _bytes(msg), retain))

    async def subscribe(self, topic, qos=0, properties=None):
        topic = _text(topic)
        while True:
            await self._connection()
            self._pid += 1
            pid = self._pid
            self.rcv_pids.add(pid)
            granted = BROKER.may_subscribe(self.client_id, topic)
            self._suback = (pid, 0 if granted else 0x80)
            try:
                await self.wait_msg()  # the real client's message task reads the SUBACK
            except OSError:
                self._reconnect()  # what _handle_msg() does with any error
                continue  # and MQTTClient.subscribe() tries again once reconnected
            if pid in self.rcv_pids:
                self.rcv_pids.discard(pid)
                self._reconnect()  # no SUBACK in time: the real client gives up the connection
                continue
            if granted:
                self.subscriptions.append(topic)
                for retained_topic, payload in list(BROKER.retained.items()):
                    if topic_matches(topic, retained_topic):
                        self.queue.put(retained_topic.encode(), payload, True)
            return

    async def disconnect(self):
        self._isconnected = False
        self.disconnected_cleanly = True
