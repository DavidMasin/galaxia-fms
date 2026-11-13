package devicecli

import (
	"bytes"
	"github.com/DavidMasin/galaxia-fms/device"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const wait = 5 * time.Second

const testManifest = `{"proto":1,"id":"sim-box","name":"Simulated box","channels":[
  {"id":"count","dir":"in","type":"counter"},
  {"id":"stop","dir":"in","type":"digital","safety":true},
  {"id":"tag","dir":"in","type":"text"},
  {"id":"lamp","dir":"out","type":"digital"}]}`

// syncBuffer collects what the simulator prints while the test reads it.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func waitUntil(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startBroker starts a device manager with the embedded MQTT broker on a free local port.
func startBroker(t *testing.T) (*device.Manager, string) {
	t.Helper()
	firstReconnect, approvalRetry = 50*time.Millisecond, 200*time.Millisecond
	manager, err := device.NewManager(device.Options{})
	require.Nil(t, err)
	mqtt := device.NewMqttTransport(device.MqttConfig{TCPAddress: "127.0.0.1:0"})
	require.Nil(t, manager.AddTransport(mqtt))
	require.Nil(t, manager.Start())
	t.Cleanup(manager.Stop)
	return manager, strings.TrimPrefix(mqtt.Addresses()[0], "tcp://")
}

func writeManifest(t *testing.T, manifest string) string {
	path := filepath.Join(t.TempDir(), "manifest.json")
	require.Nil(t, os.WriteFile(path, []byte(manifest), 0644))
	return path
}

// startSim runs the simulator with the given arguments, and returns where to type, what it prints, and its exit
// code once it has stopped.
func startSim(t *testing.T, args ...string) (io.WriteCloser, *syncBuffer, chan int) {
	stdin, typed := io.Pipe()
	output := &syncBuffer{}
	exit := make(chan int, 1)
	go func() {
		exit <- Run(append([]string{"sim"}, args...), stdin, output, output)
	}()
	t.Cleanup(func() { typed.Close() })
	return typed, output, exit
}

func printed(output *syncBuffer, text string) func() bool {
	return func() bool { return strings.Contains(output.String(), text) }
}

func inputValue(manager *device.Manager, id, channel string) any {
	info, _ := manager.Device(id)
	return info.Inputs[channel].Value
}

func TestSimulatedDevice(t *testing.T) {
	manager, address := startBroker(t)
	typed, output, exit := startSim(
		t, "--broker", address, "--id", "sim-box", "--token", "its-token", "--manifest", writeManifest(t, testManifest),
	)

	// A new device waits for approval; what is typed meanwhile is kept for later.
	waitUntil(t, printed(output, "Waiting for approval: approve sim-box"), "the device to wait for approval")
	info, _ := manager.Device("sim-box")
	assert.Equal(t, "pending", info.State)
	assert.Equal(t, "Simulated box", info.Name)
	_, err := io.WriteString(typed, "in count +2\n")
	require.Nil(t, err)
	waitUntil(t, printed(output, "Kept count = 2"), "the input to be kept")

	// Once approved, it reconnects, and the values it holds go to the FMS.
	require.Nil(t, manager.Approve("sim-box"))
	waitUntil(t, printed(output, "Approved: inputs count from now on."), "the approval")
	waitUntil(t, func() bool { return inputValue(manager, "sim-box", "count") == 2.0 }, "the counter")
	waitUntil(t, func() bool { return inputValue(manager, "sim-box", "stop") == 1.0 }, "the safety channel")

	for _, line := range []string{"in count +1", "in tag 04 a1", "in stop 0", "in nothing 1", "in count x"} {
		_, err = io.WriteString(typed, line+"\n")
		require.Nil(t, err)
	}
	waitUntil(t, func() bool { return inputValue(manager, "sim-box", "count") == 3.0 }, "the counter to add one")
	waitUntil(t, func() bool { return inputValue(manager, "sim-box", "tag") == "04 a1" }, "the text input")
	waitUntil(t, func() bool { return inputValue(manager, "sim-box", "stop") == 0.0 }, "the stop")
	waitUntil(t, printed(output, `sim-box has no input "nothing" (its inputs: count, stop, tag)`), "the refusal")
	waitUntil(t, printed(output, `count is a counter input; "x" is not a number`), "the bad value")

	// Outputs and commands are printed as they arrive.
	lamp := device.ChannelRef{Device: "sim-box", Channel: "lamp"}
	on := 1.0
	require.Nil(t, manager.ClaimOutputs("test", []device.ChannelRef{lamp}))
	require.Nil(t, manager.DriveOutput("test", lamp, &device.Command{V: &on}))
	waitUntil(t, printed(output, `out lamp {"v":1}`), "the output")
	manager.ReleaseOutputs("test")
	waitUntil(t, printed(output, "out lamp cleared"), "the output to be cleared")
	require.Nil(t, manager.Identify("sim-box"))
	waitUntil(t, printed(output, "cmd identify"), "the identify command")

	// The heartbeat stops and starts again on request.
	heartbeat := func() time.Time {
		info, _ := manager.Device("sim-box")
		return info.Heartbeat
	}
	_, err = io.WriteString(typed, "hb off\n")
	require.Nil(t, err)
	waitUntil(t, printed(output, "Heartbeat off."), "the heartbeat to stop")
	time.Sleep(2 * device.HeartbeatPeriod)
	stopped := heartbeat()
	time.Sleep(3 * device.HeartbeatPeriod)
	assert.Equal(t, stopped, heartbeat())
	_, err = io.WriteString(typed, "hb on\n")
	require.Nil(t, err)
	waitUntil(t, func() bool { return heartbeat().After(stopped) }, "the heartbeat to start again")

	_, err = io.WriteString(typed, "status\nquit\n")
	require.Nil(t, err)
	select {
	case code := <-exit:
		assert.Equal(t, 0, code)
	case <-time.After(wait):
		t.Fatal("the simulator did not quit")
	}
	assert.Contains(t, output.String(), "Connected to "+address+" and approved.")
	assert.Contains(t, output.String(), "  tag (text): 04 a1")
	waitUntil(t, func() bool {
		info, _ := manager.Device("sim-box")
		return !info.Online
	}, "the device to go offline")
}

func TestSimulatedDeviceWithAWrongToken(t *testing.T) {
	manager, address := startBroker(t)
	_, err := manager.CreateDevice("sim-box", "Box")
	require.Nil(t, err)
	typed, output, exit := startSim(
		t, "--broker", address, "--token", "not-its-token", "--manifest", writeManifest(t, testManifest),
	)
	waitUntil(t, printed(output, "the FMS refused the token (bad username or password)"), "the refusal")
	require.Nil(t, typed.Close())
	select {
	case code := <-exit:
		assert.Equal(t, 0, code, "the end of the input quits")
	case <-time.After(wait):
		t.Fatal("the simulator did not stop at the end of its input")
	}
	assert.Equal(t, 1, strings.Count(output.String(), "refused the token"), "a refusal is printed once")
}

func TestDeviceCommandErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(args, strings.NewReader(""), &stdout, &stderr)
	}
	manifest := writeManifest(t, testManifest)

	assert.Equal(t, 2, run())
	assert.Contains(t, stderr.String(), "galaxia-fms device sim --broker")
	assert.Equal(t, 0, run("help"))
	assert.Contains(t, stdout.String(), "in <channel> <value>")
	assert.Equal(t, 2, run("fly"))
	assert.Contains(t, stderr.String(), `unknown device command "fly"`)
	assert.Equal(t, 2, run("sim", "--token", "x"))
	assert.Equal(t, 2, run("sim", "--manifest", manifest))
	assert.Contains(t, stderr.String(), "--token is required")
	assert.Equal(t, 1, run("sim", "--manifest", manifest, "--id", "other", "--token", "x"))
	assert.Contains(t, stderr.String(), `The manifest is for "sim-box", not "other"`)
	assert.Equal(t, 1, run("sim", "--manifest", filepath.Join(t.TempDir(), "missing.json"), "--token", "x"))
	assert.Contains(t, stderr.String(), "Cannot read the manifest")
	assert.Equal(t, 1, run("sim", "--manifest", writeManifest(t, `{"proto":1,"id":"Bad Id"}`), "--token", "x"))
	assert.Contains(t, stderr.String(), "invalid manifest")
}

func TestParseValue(t *testing.T) {
	counter := &device.Channel{ID: "count", Dir: device.DirIn, Type: device.TypeCounter}
	digital := &device.Channel{ID: "button", Dir: device.DirIn, Type: device.TypeDigital}
	text := &device.Channel{ID: "tag", Dir: device.DirIn, Type: device.TypeText}
	analog := &device.Channel{ID: "level", Dir: device.DirIn, Type: device.TypeAnalog}
	for _, test := range []struct {
		channel *device.Channel
		text    string
		current any
		value   any
		err     string
	}{
		{counter, "+3", 2.0, 5.0, ""},
		{counter, "+1", nil, 1.0, ""},
		{counter, "7", 2.0, 7.0, ""},
		{counter, "-1", 2.0, nil, "cannot be negative"},
		{counter, "+x", 2.0, nil, "is not a number"},
		{digital, "on", nil, 1.0, ""},
		{digital, "false", nil, 0.0, ""},
		{digital, "2", nil, nil, "0 or 1"},
		{text, "007", nil, "007", ""},
		{analog, "-3.5", nil, -3.5, ""},
	} {
		value, err := parseValue(test.channel, test.text, test.current)
		if test.err != "" {
			assert.ErrorContains(t, err, test.err, test.text)
		} else if assert.Nil(t, err, test.text) {
			assert.Equal(t, test.value, value, test.text)
		}
	}
}
