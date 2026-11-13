"""A tiny MQTT 3.1.1 broker on a local socket, so one test can run the SDK with the real paho-mqtt client.

It supports what the SDK uses (QoS 0, retained messages, Last Will, + and # filters, keep-alive pings) and applies
the access rules of docs/devices.md for pending and approved devices. It is a test fixture, not a broker.
"""

import socket
import struct
import threading

from fake_broker import FakeBroker, topic_matches


def _read_exact(sock, count):
    data = b""
    while len(data) < count:
        chunk = sock.recv(count - len(data))
        if not chunk:
            raise ConnectionError("closed")
        data += chunk
    return data


def _read_packet(sock):
    header = _read_exact(sock, 1)[0]
    length, shift = 0, 0
    while True:
        byte = _read_exact(sock, 1)[0]
        length |= (byte & 0x7F) << shift
        shift += 7
        if not byte & 0x80:
            break
    return header, _read_exact(sock, length) if length else b""


def _encode_length(length):
    out = bytearray()
    while True:
        byte = length & 0x7F
        length >>= 7
        out.append(byte | (0x80 if length else 0))
        if not length:
            return bytes(out)


def _string(data, offset):
    (length,) = struct.unpack_from("!H", data, offset)
    return data[offset + 2:offset + 2 + length], offset + 2 + length


def _publish_packet(topic, payload, retain):
    topic_bytes = topic.encode()
    body = struct.pack("!H", len(topic_bytes)) + topic_bytes + payload
    return bytes([0x30 | (1 if retain else 0)]) + _encode_length(len(body)) + body


class Session:
    def __init__(self, sock):
        self.sock = sock
        self.client_id = None
        self.will = None
        self.subscriptions = []
        self.lock = threading.Lock()

    def send(self, data):
        with self.lock:
            self.sock.sendall(data)


class MiniBroker:
    def __init__(self):
        self.rules = FakeBroker()  # reuse its access rules
        self.retained = {}
        self.published = []
        self.sessions = {}
        self.lock = threading.Lock()
        self.server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.server.bind(("127.0.0.1", 0))
        self.server.listen()
        self.port = self.server.getsockname()[1]
        self.running = True
        threading.Thread(target=self._accept, daemon=True).start()

    def approve(self, client_id):
        """Approves a device and, like the FMS, drops its connection so that it connects again with its new rights."""
        self.rules.approved.add(client_id)
        self.kick(client_id)

    def block(self, client_id, blocked=True):
        if blocked:
            self.rules.blocked.add(client_id)
            self.kick(client_id)
        else:
            self.rules.blocked.discard(client_id)

    def close(self):
        self.running = False
        self.server.close()
        for session in list(self.sessions.values()):
            session.sock.close()

    def kick(self, client_id):
        """Breaks a client's connection without a DISCONNECT, as a network failure would."""
        session = self.sessions.get(client_id)
        if session:
            session.sock.shutdown(socket.SHUT_RDWR)

    def publish(self, topic, payload, retain=False, sender=None):
        with self.lock:
            self.published.append((sender, topic, payload, retain))
            if retain:
                if payload:
                    self.retained[topic] = payload
                else:
                    self.retained.pop(topic, None)
            targets = [s for s in self.sessions.values() if any(topic_matches(p, topic) for p in s.subscriptions)]
        for session in targets:
            try:
                session.send(_publish_packet(topic, payload, False))
            except OSError:
                pass

    def payloads(self, topic):
        with self.lock:
            return [entry[2] for entry in self.published if entry[1] == topic]

    def _accept(self):
        while self.running:
            try:
                sock, _ = self.server.accept()
            except OSError:
                return
            threading.Thread(target=self._serve, args=(Session(sock),), daemon=True).start()

    def _serve(self, session):
        clean = False
        try:
            while True:
                header, body = _read_packet(session.sock)
                kind = header >> 4
                if kind == 1:
                    self._connect(session, body)
                elif kind == 3:
                    topic, offset = _string(body, 0)
                    if header & 0x06:
                        offset += 2
                    topic = topic.decode()
                    if self.rules.may_publish(session.client_id, topic):
                        self.publish(topic, body[offset:], bool(header & 0x01), session.client_id)
                elif kind == 8:
                    self._subscribe(session, body)
                elif kind == 12:
                    session.send(b"\xd0\x00")
                elif kind == 14:
                    clean = True
                    return
        except (ConnectionError, OSError):
            pass
        finally:
            with self.lock:
                if self.sessions.get(session.client_id) is session:
                    del self.sessions[session.client_id]
            session.sock.close()
            if not clean and session.will is not None:
                self.publish(*session.will, sender=session.client_id)

    def _connect(self, session, body):
        _, offset = _string(body, 0)
        flags = body[offset + 1]
        offset += 4
        client_id, offset = _string(body, offset)
        session.client_id = client_id.decode()
        if flags & 0x04:
            will_topic, offset = _string(body, offset)
            will_payload, offset = _string(body, offset)
            session.will = (will_topic.decode(), will_payload, bool(flags & 0x20))
        password = None
        if flags & 0x80:
            _, offset = _string(body, offset)
        if flags & 0x40:
            password, offset = _string(body, offset)
        refusal = 0
        if session.client_id in ("plc", "inline"):
            refusal = 2
        elif session.client_id in self.rules.blocked:
            refusal = 5
        elif session.client_id in self.rules.approved and self.rules.tokens.get(session.client_id) not in (
                None, (password or b"").decode()):
            refusal = 4
        if refusal:
            session.send(bytes([0x20, 0x02, 0x00, refusal]))
            session.will = None
            raise ConnectionError("refused")
        with self.lock:
            old = self.sessions.get(session.client_id)
            self.sessions[session.client_id] = session
        if old is not None:
            old.sock.close()
        session.send(b"\x20\x02\x00\x00")

    def _subscribe(self, session, body):
        packet_id = body[:2]
        offset = 2
        codes = bytearray()
        granted = []
        while offset < len(body):
            pattern, offset = _string(body, offset)
            offset += 1
            pattern = pattern.decode()
            if self.rules.may_subscribe(session.client_id, pattern):
                codes.append(0)
                session.subscriptions.append(pattern)
                granted.append(pattern)
            else:
                codes.append(0x80)
        session.send(bytes([0x90]) + _encode_length(2 + len(codes)) + packet_id + bytes(codes))
        with self.lock:
            retained = list(self.retained.items())
        for topic, payload in retained:
            if any(topic_matches(p, topic) for p in granted):
                session.send(_publish_packet(topic, payload, True))
