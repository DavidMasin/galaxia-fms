// Package mqtttest is a minimal MQTT 3.1.1 client for tests: CONNECT with a will, PUBLISH and SUBSCRIBE at QoS 0,
// PINGREQ, DISCONNECT, and dropping the connection without one. It exists so that the tests of the device broker
// speak the wire protocol a real device speaks, with the exact packets a test needs (a refused CONNACK, an abrupt
// drop), without adding a client library to the module. It is only imported by tests.
package mqtttest

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Return codes of a CONNACK in MQTT 3.1.1.
const (
	ConnAccepted          = 0x00
	ConnIdentifierRefused = 0x02
	ConnBadCredentials    = 0x04
	ConnNotAuthorized     = 0x05
)

// SubackFailure is the SUBACK return code of a refused subscription.
const SubackFailure = 0x80

// Options describe a connection.
type Options struct {
	ClientID    string
	Username    string
	Password    string
	KeepAlive   uint16 // Seconds; 15 if zero, as devices use.
	WillTopic   string
	WillPayload []byte
	WillRetain  bool
}

// Message is a PUBLISH the client received.
type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

// Client is one connection.
type Client struct {
	conn     net.Conn
	writeMu  sync.Mutex // Also guards skipped.
	messages chan Message
	skipped  []Message   // Messages WaitFor passed over.
	acks     chan []byte // SUBACK payloads.
	pongs    chan struct{}
	done     chan struct{}
	packetID uint16
}

// Dial connects over TCP and sends CONNECT. It returns the CONNACK return code; the client is nil unless the
// connection was accepted.
func Dial(address string, options Options) (*Client, byte, error) {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, 0, err
	}
	return Connect(conn, options)
}

// Connect sends CONNECT over an open connection (a WebSocket, say) and waits for the CONNACK.
func Connect(conn net.Conn, options Options) (*Client, byte, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		return nil, 0, err
	}
	keepAlive := options.KeepAlive
	if keepAlive == 0 {
		keepAlive = 15
	}
	flags := byte(0x02) // Clean session.
	var payload []byte
	payload = appendString(payload, options.ClientID)
	if options.WillTopic != "" {
		flags |= 0x04
		if options.WillRetain {
			flags |= 0x20
		}
		payload = appendString(payload, options.WillTopic)
		payload = appendBytes(payload, options.WillPayload)
	}
	if options.Username != "" {
		flags |= 0x80
		payload = appendString(payload, options.Username)
	}
	if options.Password != "" {
		flags |= 0x40
		payload = appendString(payload, options.Password)
	}
	variable := appendString(nil, "MQTT")
	variable = append(variable, 4, flags, byte(keepAlive>>8), byte(keepAlive))
	if err := writePacket(conn, 0x10, append(variable, payload...)); err != nil {
		conn.Close()
		return nil, 0, err
	}
	reader := bufio.NewReader(conn)
	header, body, err := readPacket(reader)
	if err != nil {
		conn.Close()
		return nil, 0, fmt.Errorf("no CONNACK: %w", err)
	}
	if header>>4 != 2 || len(body) != 2 {
		conn.Close()
		return nil, 0, fmt.Errorf("expected a CONNACK, got packet type %d", header>>4)
	}
	if body[1] != ConnAccepted {
		conn.Close()
		return nil, body[1], nil
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, 0, err
	}
	client := &Client{
		conn: conn, messages: make(chan Message, 1024), acks: make(chan []byte, 16), pongs: make(chan struct{}, 16),
		done: make(chan struct{}),
	}
	go client.readLoop(reader)
	return client, ConnAccepted, nil
}

// Publish sends a QoS 0 message.
func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	header := byte(0x30)
	if retain {
		header |= 0x01
	}
	return c.write(header, append(appendString(nil, topic), payload...))
}

// Subscribe subscribes at QoS 0 and returns the SUBACK return code: 0 if granted, SubackFailure if refused.
func (c *Client) Subscribe(filter string, timeout time.Duration) (byte, error) {
	c.writeMu.Lock()
	c.packetID++
	id := c.packetID
	c.writeMu.Unlock()
	body := []byte{byte(id >> 8), byte(id)}
	body = append(appendString(body, filter), 0)
	if err := c.write(0x82, body); err != nil {
		return 0, err
	}
	select {
	case ack := <-c.acks:
		if len(ack) < 3 {
			return 0, fmt.Errorf("short SUBACK")
		}
		return ack[2], nil
	case <-c.done:
		return 0, io.EOF
	case <-time.After(timeout):
		return 0, fmt.Errorf("no SUBACK within %v", timeout)
	}
}

// Ping sends PINGREQ and waits for PINGRESP. The broker handles a client's packets in order, so once Ping returns,
// everything sent before it has been handled.
func (c *Client) Ping(timeout time.Duration) error {
	if err := c.write(0xC0, nil); err != nil {
		return err
	}
	select {
	case <-c.pongs:
		return nil
	case <-c.done:
		return io.EOF
	case <-time.After(timeout):
		return fmt.Errorf("no PINGRESP within %v", timeout)
	}
}

// Next returns the next message received, including any WaitFor passed over.
func (c *Client) Next(timeout time.Duration) (Message, error) {
	c.writeMu.Lock()
	if len(c.skipped) > 0 {
		message := c.skipped[0]
		c.skipped = c.skipped[1:]
		c.writeMu.Unlock()
		return message, nil
	}
	c.writeMu.Unlock()
	return c.receive(timeout)
}

func (c *Client) receive(timeout time.Duration) (Message, error) {
	select {
	case message := <-c.messages:
		return message, nil
	case <-time.After(timeout):
		return Message{}, fmt.Errorf("no message within %v", timeout)
	}
}

// WaitFor returns the first message on the given topic. Messages on other topics are kept for later calls.
func (c *Client) WaitFor(topic string, timeout time.Duration) (Message, error) {
	c.writeMu.Lock()
	for i, message := range c.skipped {
		if message.Topic == topic {
			c.skipped = append(c.skipped[:i], c.skipped[i+1:]...)
			c.writeMu.Unlock()
			return message, nil
		}
	}
	c.writeMu.Unlock()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Message{}, fmt.Errorf("no message on %s within %v", topic, timeout)
		}
		message, err := c.receive(remaining)
		if err != nil {
			return Message{}, fmt.Errorf("no message on %s within %v", topic, timeout)
		}
		if message.Topic == topic {
			return message, nil
		}
		c.writeMu.Lock()
		c.skipped = append(c.skipped, message)
		c.writeMu.Unlock()
	}
}

// Done is closed when the connection ends, from either side.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Close sends DISCONNECT and closes the connection: a clean disconnection, without the will.
func (c *Client) Close() {
	_ = c.write(0xE0, nil)
	_ = c.conn.Close()
	<-c.done
}

// Drop closes the connection without DISCONNECT, as a device that loses power does; the broker sends the will.
func (c *Client) Drop() {
	_ = c.conn.Close()
	<-c.done
}

func (c *Client) write(header byte, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writePacket(c.conn, header, body)
}

func (c *Client) readLoop(reader *bufio.Reader) {
	defer close(c.done)
	for {
		header, body, err := readPacket(reader)
		if err != nil {
			return
		}
		switch header >> 4 {
		case 3: // PUBLISH at QoS 0: topic, then payload.
			if len(body) < 2 {
				return
			}
			length := int(binary.BigEndian.Uint16(body))
			if len(body) < 2+length {
				return
			}
			message := Message{
				Topic: string(body[2 : 2+length]), Payload: append([]byte(nil), body[2+length:]...),
				Retain: header&0x01 != 0,
			}
			select {
			case c.messages <- message:
			default:
			}
		case 9: // SUBACK
			c.acks <- body
		case 13: // PINGRESP
			c.pongs <- struct{}{}
		}
	}
}

func appendString(buffer []byte, s string) []byte {
	return appendBytes(buffer, []byte(s))
}

func appendBytes(buffer []byte, b []byte) []byte {
	buffer = append(buffer, byte(len(b)>>8), byte(len(b)))
	return append(buffer, b...)
}

func writePacket(w io.Writer, header byte, body []byte) error {
	packet := []byte{header}
	length := len(body)
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		packet = append(packet, digit)
		if length == 0 {
			break
		}
	}
	_, err := w.Write(append(packet, body...))
	return err
}

func readPacket(reader *bufio.Reader) (byte, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, multiplier := 0, 1
	for i := 0; ; i++ {
		digit, err := reader.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		length += int(digit&0x7F) * multiplier
		multiplier *= 128
		if digit&0x80 == 0 {
			break
		}
		if i == 3 {
			return 0, nil, errors.New("malformed remaining length")
		}
	}
	body := make([]byte, length)
	if _, err = io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return header, body, nil
}
