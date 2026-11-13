// A small MQTT 3.1.1 client, enough for a simulated device: CONNECT with a will, PUBLISH and SUBSCRIBE at QoS 0,
// keep-alive pings and DISCONNECT. The FMS asks nothing more of a device, and it keeps go.mod free of a client
// library. (device/mqtttest is a client for tests, built to send the odd packets a test needs; this one reconnects
// as a device does and is used by the command line.)

package devicecli

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

// CONNACK return codes of MQTT 3.1.1 that the FMS uses to refuse a device.
const (
	connAccepted          = 0x00
	connIdentifierRefused = 0x02
	connBadCredentials    = 0x04
	connNotAuthorized     = 0x05
)

// subackFailure is the SUBACK return code of a refused subscription.
const subackFailure = 0x80

type mqttOptions struct {
	ClientID    string
	Username    string
	Password    string
	KeepAlive   time.Duration
	WillTopic   string
	WillPayload []byte
	WillRetain  bool
}

type mqttMessage struct {
	Topic   string
	Payload []byte
}

// connectRefused is a CONNACK that refused the connection.
type connectRefused struct {
	code byte
}

func (err connectRefused) Error() string {
	switch err.code {
	case connIdentifierRefused:
		return "the FMS refused the device id: it must be 1 to 40 lowercase letters, digits and dashes, and not plc"
	case connBadCredentials:
		return "the FMS refused the token (bad username or password): an approved device must present the token " +
			"it was approved with; the Devices page can give it a new one"
	case connNotAuthorized:
		return "the FMS refused the device (not authorised): it is blocked on the Devices page"
	}
	return fmt.Sprintf("the FMS refused the connection (return code %d)", err.code)
}

// mqttClient is one connection to the broker. Its methods may be called from any goroutine.
type mqttClient struct {
	conn      net.Conn
	keepAlive time.Duration
	writeMu   sync.Mutex
	packetID  uint16
	messages  chan mqttMessage
	subacks   chan byte
	done      chan struct{} // Closed when the connection ends, from either side.
	closeOnce sync.Once
	err       error // Why the connection ended; set before done is closed.
}

// dialMqtt connects to the broker and waits for its CONNACK.
func dialMqtt(address string, options mqttOptions) (*mqttClient, error) {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	flags := byte(0x02) // Clean session: a device starts afresh each time.
	payload := appendMqttString(nil, options.ClientID)
	if options.WillTopic != "" {
		flags |= 0x04
		if options.WillRetain {
			flags |= 0x20
		}
		payload = appendMqttBytes(appendMqttString(payload, options.WillTopic), options.WillPayload)
	}
	if options.Username != "" {
		flags |= 0x80
		payload = appendMqttString(payload, options.Username)
	}
	if options.Password != "" {
		flags |= 0x40
		payload = appendMqttString(payload, options.Password)
	}
	keepAlive := uint16(options.KeepAlive / time.Second)
	variable := append(appendMqttString(nil, "MQTT"), 4, flags, byte(keepAlive>>8), byte(keepAlive))
	if err = writeMqttPacket(conn, 0x10, append(variable, payload...)); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	header, body, err := readMqttPacket(reader)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("the broker did not answer: %w", err)
	}
	if header>>4 != 2 || len(body) != 2 {
		conn.Close()
		return nil, fmt.Errorf("the broker answered with packet type %d instead of CONNACK", header>>4)
	}
	if body[1] != connAccepted {
		conn.Close()
		return nil, connectRefused{code: body[1]}
	}
	client := &mqttClient{
		conn: conn, keepAlive: options.KeepAlive, messages: make(chan mqttMessage, 256), subacks: make(chan byte, 4),
		done: make(chan struct{}),
	}
	go client.readLoop(reader)
	return client, nil
}

// publish sends a message at QoS 0.
func (c *mqttClient) publish(topic string, payload []byte, retain bool) error {
	header := byte(0x30)
	if retain {
		header |= 0x01
	}
	return c.write(header, append(appendMqttString(nil, topic), payload...))
}

// subscribe asks for a subscription at QoS 0 and reports whether the broker granted it.
func (c *mqttClient) subscribe(filter string, timeout time.Duration) (bool, error) {
	c.writeMu.Lock()
	c.packetID++
	if c.packetID == 0 {
		c.packetID = 1
	}
	id := c.packetID
	c.writeMu.Unlock()
	body := append(appendMqttString([]byte{byte(id >> 8), byte(id)}, filter), 0)
	if err := c.write(0x82, body); err != nil {
		return false, err
	}
	select {
	case code := <-c.subacks:
		return code != subackFailure, nil
	case <-c.done:
		return false, c.err
	case <-time.After(timeout):
		return false, fmt.Errorf("the broker did not answer the subscription to %s", filter)
	}
}

// ping keeps the connection alive; the broker's answer only resets the read deadline.
func (c *mqttClient) ping() error {
	return c.write(0xC0, nil)
}

// disconnect says goodbye, so that the broker does not send the will, and closes the connection.
func (c *mqttClient) disconnect() {
	_ = c.write(0xE0, nil)
	c.close(errors.New("disconnected"))
}

func (c *mqttClient) close(err error) {
	c.closeOnce.Do(func() {
		c.err = err
		_ = c.conn.Close()
		close(c.done)
	})
}

func (c *mqttClient) write(header byte, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return writeMqttPacket(c.conn, header, body)
}

func (c *mqttClient) readLoop(reader *bufio.Reader) {
	for {
		// The broker answers every ping, and pings go out at half the keep-alive: silence for longer than the
		// keep-alive means the connection is gone.
		if c.keepAlive > 0 {
			_ = c.conn.SetReadDeadline(time.Now().Add(c.keepAlive * 3 / 2))
		}
		header, body, err := readMqttPacket(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("the FMS closed the connection")
			}
			c.close(err)
			return
		}
		switch header >> 4 {
		case 3: // PUBLISH
			message, ok := parseMqttPublish(header, body)
			if !ok {
				c.close(errors.New("the broker sent a malformed message"))
				return
			}
			select {
			case c.messages <- message:
			case <-c.done:
				return
			}
		case 9: // SUBACK: packet id, then one return code per filter.
			if len(body) >= 3 {
				select {
				case c.subacks <- body[2]:
				default:
				}
			}
		}
	}
}

// parseMqttPublish reads a PUBLISH: topic, a packet id at QoS 1 or 2 (the broker sends QoS 0 to a QoS 0
// subscription, but this does not rely on it), then the payload.
func parseMqttPublish(header byte, body []byte) (mqttMessage, bool) {
	if len(body) < 2 {
		return mqttMessage{}, false
	}
	length := int(binary.BigEndian.Uint16(body))
	rest := body[2:]
	if len(rest) < length {
		return mqttMessage{}, false
	}
	topic, rest := string(rest[:length]), rest[length:]
	if (header>>1)&0x03 > 0 {
		if len(rest) < 2 {
			return mqttMessage{}, false
		}
		rest = rest[2:]
	}
	return mqttMessage{Topic: topic, Payload: append([]byte(nil), rest...)}, true
}

func appendMqttString(buffer []byte, s string) []byte {
	return appendMqttBytes(buffer, []byte(s))
}

func appendMqttBytes(buffer []byte, b []byte) []byte {
	buffer = append(buffer, byte(len(b)>>8), byte(len(b)))
	return append(buffer, b...)
}

func writeMqttPacket(w io.Writer, header byte, body []byte) error {
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

func readMqttPacket(reader *bufio.Reader) (byte, []byte, error) {
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
