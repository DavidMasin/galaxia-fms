// Package devicecli implements the "galaxia-fms device ..." commands. "device sim" pretends to be a field device of
// a running FMS from a terminal: it connects to the FMS's MQTT broker with a manifest, as firmware would, sends the
// inputs typed at it and prints the outputs the FMS sends back. It is for trying bindings before any hardware is
// wired, and for scripting tests of a field.
package devicecli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/device"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const usage = `Pretend to be a field device of a running Galaxia FMS.

Usage:
  galaxia-fms device sim --broker <host:port> --id <id> --token <token> --manifest <file.json>

The simulator connects to the FMS's MQTT broker as the device the manifest describes (docs/devices.md), then
reads commands, one per line:
  in <channel> <value>   Report an input: 0 or 1 (or true and false) for a digital input, a number for an analog
                         or encoder input, the running total of a counter (or +n to add n to it), any text for a
                         text input.
  hb on|off              Send heartbeats with the safety channels, or stop as a device that hangs would. They are
                         on from the start when the manifest has a safety channel.
  status                 Say whether the device is connected and approved, and what its inputs read.
  help                   Show the commands.
  quit                   Disconnect and exit; so does the end of the input.
Outputs and commands the FMS sends are printed as they arrive. A new device waits for approval on the Devices page.
`

const commandHelp = `in <channel> <value>   report an input (+n adds to a counter)
hb on|off              start or stop the heartbeat
status                 show the connection and the inputs
quit                   disconnect and exit
`

// Timing of the simulated device. They are variables so that the tests can run faster.
var (
	keepAlive          = 15 * time.Second
	statusPeriod       = 5 * time.Second
	approvalRetry      = 10 * time.Second
	maxReconnectDelay  = 5 * time.Second
	firstReconnect     = time.Second
	subscriptionAnswer = 5 * time.Second
)

// Run executes a device command and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "sim":
		return sim(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown device command %q\n\n%s", args[0], usage)
	return 2
}

func sim(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("device sim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	broker := flags.String("broker", "localhost:1883", "the FMS's MQTT broker, host:port")
	id := flags.String("id", "", "the device id (by default the manifest's)")
	token := flags.String("token", "", "the device's token, from the Devices page")
	manifestPath := flags.String("manifest", "", "the device's manifest, a JSON file")
	flags.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifestPath == "" || flags.NArg() > 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "Cannot read the manifest: %v\n", err)
		return 1
	}
	manifest, err := device.ParseManifest(data)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", *manifestPath, err)
		return 1
	}
	if *id == "" {
		*id = manifest.ID
	}
	if *id != manifest.ID {
		fmt.Fprintf(stderr, "The manifest is for %q, not %q: a device's manifest names the device itself.\n",
			manifest.ID, *id)
		return 1
	}
	if *token == "" {
		fmt.Fprint(stderr, "--token is required: use the token the Devices page gave the device, or for a new "+
			"device any secret of your own, which is pinned when the device is approved.\n")
		return 2
	}

	s := newSimulator(manifest, *broker, *token, stdout)
	return s.run(stdin)
}

// simulator is one simulated device. Everything runs on the goroutine of run, apart from reading the input lines.
type simulator struct {
	out       io.Writer
	manifest  *device.Manifest
	broker    string
	token     string
	prefix    string // Topic prefix of the device's own topics.
	client    *mqttClient
	approved  bool
	waitShown bool
	lastError string
	values    map[string]any // The value of each input, as the device holds it.
	seq       int64
	hbSeq     int64
	heartbeat bool
	started   time.Time
}

func newSimulator(manifest *device.Manifest, broker, token string, out io.Writer) *simulator {
	s := &simulator{
		out: out, manifest: manifest, broker: broker, token: token, values: make(map[string]any),
		heartbeat: manifest.HasSafety(), started: time.Now(),
		prefix: device.TopicPrefix + "dev/" + manifest.ID + "/",
	}
	for _, channel := range manifest.Channels {
		switch {
		case channel.Dir != device.DirIn:
		case channel.Safety:
			// A stop circuit is closed, and safe, until something opens it.
			s.values[channel.ID] = 1.0
		case channel.Type == device.TypeCounter:
			// Firmware reports each counter at 0 when it starts, so that the FMS counts from there.
			s.values[channel.ID] = 0.0
		}
	}
	return s
}

func (s *simulator) printf(format string, args ...any) {
	fmt.Fprintf(s.out, format+"\n", args...)
}

func (s *simulator) run(stdin io.Reader) int {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	s.printf("Device %s (%d channels), connecting to %s. Type help for the commands.", s.manifest.ID,
		len(s.manifest.Channels), s.broker)
	reconnect := time.NewTimer(0)
	delay := firstReconnect
	status := time.NewTicker(statusPeriod)
	heartbeat := time.NewTicker(device.HeartbeatPeriod)
	ping := time.NewTicker(keepAlive / 2)
	retry := time.NewTicker(approvalRetry)
	defer func() {
		reconnect.Stop()
		status.Stop()
		heartbeat.Stop()
		ping.Stop()
		retry.Stop()
		if s.client != nil {
			s.client.close(errors.New("the simulator stopped"))
		}
	}()

	for {
		var messages <-chan mqttMessage
		var done <-chan struct{}
		if s.client != nil {
			messages, done = s.client.messages, s.client.done
		}
		select {
		case line, ok := <-lines:
			if !ok || !s.command(line) {
				s.quit()
				return 0
			}
		case <-reconnect.C:
			fatal, err := s.connect()
			if fatal {
				s.printf("%v", err)
				return 1
			}
			if err != nil {
				if err.Error() != s.lastError {
					s.printf("Cannot connect: %v. Trying again.", err)
					s.lastError = err.Error()
				}
				reconnect.Reset(delay)
				delay = min(2*delay, maxReconnectDelay)
				continue
			}
			s.lastError, delay = "", firstReconnect
		case message := <-messages:
			s.received(message)
		case <-done:
			s.printf("Connection lost: %v. Reconnecting.", s.client.err)
			s.client, s.approved = nil, false
			reconnect.Reset(firstReconnect)
		case <-status.C:
			s.sendStatus()
		case <-heartbeat.C:
			if s.heartbeat {
				s.sendHeartbeat()
			}
		case <-ping.C:
			if s.client != nil {
				_ = s.client.ping()
			}
		case <-retry.C:
			if s.client != nil && !s.approved {
				s.subscribe()
			}
		}
	}
}

// connect connects and announces the device: manifest and status (retained), then the subscriptions, which the
// broker refuses while the device waits for approval. It reports a refusal that trying again cannot cure as fatal.
func (s *simulator) connect() (fatal bool, err error) {
	client, err := dialMqtt(s.broker, mqttOptions{
		ClientID: s.manifest.ID, Username: s.manifest.ID, Password: s.token, KeepAlive: keepAlive,
		WillTopic: s.prefix + "status", WillPayload: []byte(`{"online":false}`), WillRetain: true,
	})
	if err != nil {
		var refused connectRefused
		return errors.As(err, &refused) && refused.code == connIdentifierRefused, err
	}
	manifest, _ := json.Marshal(s.manifest)
	if err = client.publish(s.prefix+"manifest", manifest, true); err != nil {
		client.close(err)
		return false, err
	}
	s.client, s.approved, s.waitShown = client, false, false
	s.sendStatus()
	s.printf("Connected to %s.", s.broker)
	s.subscribe()
	return false, nil
}

// subscribe asks for the device's outputs and commands. Being granted them means the device is approved: its inputs
// count from now on, and the values it holds are sent again so that the FMS starts from the truth.
func (s *simulator) subscribe() {
	granted, err := s.client.subscribe(s.prefix+"out/+", subscriptionAnswer)
	if err == nil && granted {
		granted, err = s.client.subscribe(s.prefix+"cmd", subscriptionAnswer)
	}
	switch {
	case err != nil:
		s.printf("Cannot subscribe: %v.", err)
	case !granted:
		if !s.waitShown {
			s.printf("Waiting for approval: approve %s on the Devices page (Setup > Devices).", s.manifest.ID)
			s.waitShown = true
		}
	default:
		s.approved = true
		s.printf("Approved: inputs count from now on.")
		for _, channel := range sortedKeys(s.values) {
			if c := s.manifest.Channel(channel); c != nil && c.Type != device.TypeText {
				s.sendInput(channel)
			}
		}
		if s.heartbeat {
			s.sendHeartbeat()
		}
	}
}

// command carries out one line of input. It returns false to quit.
func (s *simulator) command(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return true
	}
	switch fields[0] {
	case "quit", "exit":
		return false
	case "help", "?":
		fmt.Fprint(s.out, commandHelp)
	case "status":
		s.showStatus()
	case "hb":
		if len(fields) != 2 || (fields[1] != "on" && fields[1] != "off") {
			s.printf("Usage: hb on|off")
			break
		}
		s.heartbeat = fields[1] == "on"
		if !s.manifest.HasSafety() {
			s.printf("Note: %s has no safety channel; its heartbeat carries nothing.", s.manifest.ID)
		}
		s.printf("Heartbeat %s.", fields[1])
	case "in":
		if len(fields) < 3 {
			s.printf("Usage: in <channel> <value>")
			break
		}
		// The value is the rest of the line, so that a text input may have spaces.
		rest := strings.TrimSpace(strings.TrimSpace(line)[len(fields[0]):])
		s.input(fields[1], strings.TrimSpace(rest[len(fields[1]):]))
	default:
		s.printf("Unknown command %q; type help for the commands.", fields[0])
	}
	return true
}

// input sets an input to the value typed and reports it.
func (s *simulator) input(channelID, text string) {
	channel := s.manifest.Channel(channelID)
	if channel == nil || channel.Dir != device.DirIn {
		var inputs []string
		for _, candidate := range s.manifest.Channels {
			if candidate.Dir == device.DirIn {
				inputs = append(inputs, candidate.ID)
			}
		}
		s.printf("%s has no input %q (its inputs: %s).", s.manifest.ID, channelID, strings.Join(inputs, ", "))
		return
	}
	value, err := parseValue(channel, text, s.values[channelID])
	if err != nil {
		s.printf("%v", err)
		return
	}
	s.values[channelID] = value
	if s.sendInput(channelID) {
		s.printf("Sent %s = %v.", channelID, value)
	} else {
		s.printf("Kept %s = %v; it is sent once the device is connected and approved.", channelID, value)
	}
	if channel.Safety && s.heartbeat {
		// A stop goes out at once, not with the next heartbeat.
		s.sendHeartbeat()
	}
}

// parseValue reads a typed value for a channel: text as it is, a counter's +n as an addition to its total, and
// numbers, true and false (on and off) for the rest.
func parseValue(channel *device.Channel, text string, current any) (any, error) {
	if channel.Type == device.TypeText {
		return text, nil
	}
	if channel.Type == device.TypeCounter && strings.HasPrefix(text, "+") {
		add, err := strconv.ParseFloat(text[1:], 64)
		if err != nil {
			return nil, fmt.Errorf("+n adds to a counter's total: %q is not a number", text[1:])
		}
		total, _ := current.(float64)
		return total + add, nil
	}
	switch strings.ToLower(text) {
	case "true", "on":
		text = "1"
	case "false", "off":
		text = "0"
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, fmt.Errorf("%s is a %s input; %q is not a number", channel.ID, channel.Type, text)
	}
	switch channel.Type {
	case device.TypeDigital:
		if number != 0 && number != 1 {
			return nil, fmt.Errorf("%s is a digital input: 0 or 1", channel.ID)
		}
	case device.TypeCounter:
		if number < 0 {
			return nil, fmt.Errorf("%s is a counter: its running total cannot be negative", channel.ID)
		}
	}
	return number, nil
}

// sendInput reports the value of an input, if the device is approved. It reports whether it was sent.
func (s *simulator) sendInput(channel string) bool {
	if s.client == nil || !s.approved {
		return false
	}
	s.seq++
	payload, _ := json.Marshal(map[string]any{"v": s.values[channel], "seq": s.seq})
	return s.client.publish(s.prefix+"in/"+channel, payload, false) == nil
}

func (s *simulator) sendStatus() {
	if s.client == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"online": true, "up": int(time.Since(s.started).Seconds())})
	_ = s.client.publish(s.prefix+"status", payload, true)
}

func (s *simulator) sendHeartbeat() {
	if s.client == nil || !s.approved {
		return
	}
	s.hbSeq++
	channels := make(map[string]any)
	for _, channel := range s.manifest.Channels {
		if channel.Safety {
			channels[channel.ID] = s.values[channel.ID]
		}
	}
	payload, _ := json.Marshal(map[string]any{"seq": s.hbSeq, "ch": channels})
	_ = s.client.publish(s.prefix+"hb", payload, false)
}

// received prints what the FMS sent: an output command, a cleared output, or a device command.
func (s *simulator) received(message mqttMessage) {
	rest, ok := strings.CutPrefix(message.Topic, s.prefix)
	switch {
	case !ok:
	case rest == "cmd":
		var command device.DeviceCommand
		if json.Unmarshal(message.Payload, &command) == nil {
			s.printf("cmd %s", command.Do)
		}
	case strings.HasPrefix(rest, "out/"):
		channel := strings.TrimPrefix(rest, "out/")
		if len(message.Payload) == 0 {
			s.printf("out %s cleared", channel)
		} else {
			s.printf("out %s %s", channel, message.Payload)
		}
	}
}

func (s *simulator) showStatus() {
	switch {
	case s.client == nil:
		s.printf("Not connected (trying %s).", s.broker)
	case !s.approved:
		s.printf("Connected to %s, waiting for approval.", s.broker)
	default:
		s.printf("Connected to %s and approved.", s.broker)
	}
	heartbeat := "off"
	if s.heartbeat {
		heartbeat = "on"
	}
	s.printf("Heartbeat %s.", heartbeat)
	for _, channel := range s.manifest.Channels {
		if channel.Dir != device.DirIn {
			continue
		}
		value, set := s.values[channel.ID]
		if !set {
			value = "(not set)"
		}
		s.printf("  %s (%s): %v", channel.ID, channel.Type, value)
	}
}

// quit says goodbye: the status goes offline, since a clean disconnection does not fire the will.
func (s *simulator) quit() {
	if s.client != nil {
		_ = s.client.publish(s.prefix+"status", []byte(`{"online":false}`), true)
		s.client.disconnect()
		s.client = nil
	}
	s.printf("Disconnected.")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
