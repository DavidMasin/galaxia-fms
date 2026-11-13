package device

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startHttp starts an HTTP transport behind a test server routed as the web server routes it, and returns it with
// the server's base URL. configure, if given, changes the transport before it starts.
func startHttp(t *testing.T, h *harness, configure func(*HttpTransport)) (*HttpTransport, string) {
	t.Helper()
	transport := NewHttpTransport()
	if configure != nil {
		configure(transport)
	}
	require.Nil(t, h.m.AddTransport(transport))
	t.Cleanup(transport.Stop)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/devices/{id}/manifest", func(w http.ResponseWriter, r *http.Request) {
		transport.ServeManifest(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/v1/devices/{id}/in/{ch}", func(w http.ResponseWriter, r *http.Request) {
		transport.ServeInput(w, r, r.PathValue("id"), r.PathValue("ch"))
	})
	mux.HandleFunc("GET /api/v1/devices/{id}/out", func(w http.ResponseWriter, r *http.Request) {
		transport.ServeOutputs(w, r, r.PathValue("id"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return transport, server.URL
}

// httpCall makes a request as a device and returns the status and the decoded answer, if there is one.
func httpCall(t *testing.T, method, url, token, body string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	require.Nil(t, err)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	require.Nil(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.Nil(t, err)
	var answer map[string]any
	if len(data) > 0 {
		require.Nil(t, json.Unmarshal(data, &answer), string(data))
	}
	return response.StatusCode, answer
}

// httpManifestJson is a manifest an HTTP device may have: no safety channel.
func httpManifestJson(id string) string {
	return fmt.Sprintf(`{"proto":1,"id":%q,"name":"Panel","channels":[
		{"id":"count","dir":"in","type":"counter"},{"id":"lamp","dir":"out","type":"digital"},
		{"id":"horn","dir":"out","type":"sound"}]}`, id)
}

func TestHttpDevice(t *testing.T) {
	h := newHarness(t)
	_, base := startHttp(t, h, nil)
	device := base + "/api/v1/devices/panel-1"

	// Every request needs a token.
	status, answer := httpCall(t, "PUT", device+"/manifest", "", httpManifestJson("panel-1"))
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, answer["error"], "Authorization: Bearer <token>")

	// A new device is let in as pending: it may announce itself, nothing more.
	status, answer = httpCall(t, "PUT", device+"/manifest", "secret", httpManifestJson("panel-1"))
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{"approved": false}, answer)
	info, _ := h.m.Device("panel-1")
	assert.Equal(t, "pending", info.State)
	assert.True(t, info.Online)
	assert.Equal(t, "Panel", info.Name)
	assert.True(t, strings.HasPrefix(info.Presence[HttpTransportName].Remote, "127.0.0.1:"))
	status, answer = httpCall(t, "POST", device+"/in/count", "secret", `{"v":1}`)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "the device is waiting for approval", answer["error"])
	status, _ = httpCall(t, "GET", device+"/out", "secret", "")
	assert.Equal(t, http.StatusForbidden, status)

	// Once approved its inputs count, with no need to connect again.
	require.Nil(t, h.m.Approve("panel-1"))
	h.bind(`
inputs:
  - { id: goal, device: panel-1, channel: count, action: { score: balls, op: increment, alliance: red } }
outputs:
  - { id: lamp, device: panel-1, channel: lamp, when: "phase == 'teleop'" }
  - { id: horn, device: panel-1, channel: horn, on: match.start, look: { play: horn } }
`)
	status, _ = httpCall(t, "PUT", device+"/manifest", "secret", httpManifestJson("panel-1"))
	assert.Equal(t, http.StatusOK, status)
	status, _ = httpCall(t, "POST", device+"/in/count", "secret", `{"v":0}`)
	assert.Equal(t, http.StatusNoContent, status)
	status, _ = httpCall(t, "POST", device+"/in/count", "secret", `{"v":2,"seq":2,"conf":0.5}`)
	assert.Equal(t, http.StatusNoContent, status)
	changes := h.scorer.take()
	if assert.Equal(t, 1, len(changes)) {
		assertChange(t, changes[0], "red", 0, "balls", "increment", 2.0)
	}
	assert.Empty(t, h.m.Faults())

	// An input the FMS will not take is refused, saying why, and shows on the device.
	status, answer = httpCall(t, "POST", device+"/in/count", "secret", `{"v":`)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, answer["error"], "the input is not valid JSON")
	info, _ = h.m.Device("panel-1")
	assert.Contains(t, info.Refusal.Reason, "in/count: the input is not valid JSON")
	status, answer = httpCall(t, "POST", device+"/in/lamp", "secret", `{"v":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, `"lamp" is not an input channel of the manifest`, answer["error"])

	// Outputs: the answer holds every output in force and the sequence number to wait from.
	h.m.UpdateOutputs(h.snapshot("teleop", 1))
	status, answer = httpCall(t, "GET", device+"/out", "secret", "")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{"lamp": map[string]any{"v": 1.0}}, answer["outputs"])
	seq := int64(answer["seq"].(float64))

	// A request that waits is answered as soon as something changes.
	type result struct {
		status int
		answer map[string]any
	}
	done := make(chan result)
	go func() {
		status, answer := httpCall(t, "GET", fmt.Sprintf("%s/out?since=%d&wait=10", device, seq), "secret", "")
		done <- result{status, answer}
	}()
	time.Sleep(100 * time.Millisecond)
	h.m.UpdateOutputs(h.snapshot("auto", 1, "match.start"))
	var got result
	select {
	case got = <-done:
	case <-time.After(wait):
		t.Fatal("the waiting request was not answered when the outputs changed")
	}
	assert.Equal(t, http.StatusOK, got.status)
	// The lamp is off in auto, and the horn sounds once.
	assert.Equal(t, map[string]any{"lamp": map[string]any{"v": 0.0}, "horn": map[string]any{"play": "horn"}},
		got.answer["outputs"])
	newSeq := int64(got.answer["seq"].(float64))
	assert.Greater(t, newSeq, seq)

	// With nothing new, a request waits as long as it asks to, and the sound is not played again.
	start := time.Now()
	status, answer = httpCall(t, "GET", fmt.Sprintf("%s/out?since=%d&wait=0.3", device, newSeq), "secret", "")
	assert.Equal(t, http.StatusOK, status)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	assert.Equal(t, float64(newSeq), answer["seq"])
	assert.Equal(t, map[string]any{"lamp": map[string]any{"v": 0.0}}, answer["outputs"])

	// A device command comes with the next answer.
	require.Nil(t, h.m.Identify("panel-1"))
	status, answer = httpCall(t, "GET", fmt.Sprintf("%s/out?since=%d&wait=5", device, newSeq), "secret", "")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, []any{map[string]any{"do": "identify"}}, answer["commands"])

	// A device that asks with a number from another run (it restarted, or the FMS did) is answered at once, with
	// its outputs but no old sounds or commands.
	status, answer = httpCall(t, "GET", device+"/out?since=3&wait=5", "secret", "")
	assert.Equal(t, http.StatusOK, status)
	assert.Nil(t, answer["commands"])
	assert.Equal(t, map[string]any{"lamp": map[string]any{"v": 0.0}}, answer["outputs"])

	// Bad numbers are refused.
	status, _ = httpCall(t, "GET", device+"/out?since=x", "secret", "")
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = httpCall(t, "GET", device+"/out?wait=-1", "secret", "")
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestHttpRefusals(t *testing.T) {
	h := newHarness(t)
	transport, base := startHttp(t, h, nil)
	token, err := h.m.CreateDevice("arcade-1", "")
	require.Nil(t, err)
	_, _ = h.m.Authenticate(HttpTransportName, "blocked-1", "x")
	require.Nil(t, h.m.Block("blocked-1"))

	cases := []struct {
		id, token string
		status    int
		reason    string
	}{
		{"arcade-1", "wrong", http.StatusUnauthorized, "bad token"},
		{"blocked-1", "x", http.StatusForbidden, "the device is blocked"},
		{"Not_Valid", "x", http.StatusBadRequest, "the id is not a valid device id"},
		{"plc", "x", http.StatusForbidden, "the device id is reserved"},
	}
	for _, c := range cases {
		status, answer := httpCall(t, "GET", base+"/api/v1/devices/"+c.id+"/out", c.token, "")
		assert.Equal(t, c.status, status, c.id)
		assert.Contains(t, answer["error"], c.reason, c.id)
	}

	// Safety channels need a heartbeat over a connection that stays open, which HTTP is not.
	status, answer := httpCall(t, "PUT", base+"/api/v1/devices/arcade-1/manifest", token, string(manifestJson("arcade-1")))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, answer["error"], "HTTP devices cannot carry safety channels")
	info, _ := h.m.Device("arcade-1")
	assert.Nil(t, info.Manifest)
	assert.Contains(t, info.Refusal.Reason, "manifest: HTTP devices cannot carry safety channels")

	// A manifest for another device, and one that is not valid, are refused.
	status, answer = httpCall(t, "PUT", base+"/api/v1/devices/arcade-1/manifest", token, httpManifestJson("other-1"))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, `the manifest is for "other-1", but it came from "arcade-1"`, answer["error"])
	status, answer = httpCall(t, "PUT", base+"/api/v1/devices/arcade-1/manifest", token, `{"proto":2}`)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, answer["error"], "proto must be 1, not 2")
	status, _ = httpCall(t, "PUT", base+"/api/v1/devices/arcade-1/manifest", token, strings.Repeat(" ", 70000))
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)

	// A request waiting for outputs is refused at once when the device is blocked, which takes it offline.
	done := make(chan int)
	go func() {
		status, _ := httpCall(t, "GET", fmt.Sprintf("%s/api/v1/devices/arcade-1/out?since=%d&wait=10", base,
			transport.firstSeq), token, "")
		done <- status
	}()
	eventually(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return transport.devices["arcade-1"].inFlight == 1
	}, "the request to wait")
	require.Nil(t, h.m.Block("arcade-1"))
	select {
	case status := <-done:
		assert.Equal(t, http.StatusForbidden, status)
	case <-time.After(wait):
		t.Fatal("the waiting request was not refused when the device was blocked")
	}
	eventually(t, func() bool { return !deviceOnline(h, "arcade-1") }, "the device to go offline")

	// A stopped transport turns requests away.
	transport.Stop()
	status, _ = httpCall(t, "GET", base+"/api/v1/devices/arcade-1/out", token, "")
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestHttpPresence(t *testing.T) {
	h := newHarness(t)
	transport, base := startHttp(t, h, func(transport *HttpTransport) { transport.presence = 300 * time.Millisecond })
	token, _ := h.m.CreateDevice("panel-1", "")
	device := base + "/api/v1/devices/panel-1"

	// A device is online while a request waits, and for the presence time after the last one.
	status, _ := httpCall(t, "PUT", device+"/manifest", token, httpManifestJson("panel-1"))
	require.Equal(t, http.StatusOK, status)
	start := time.Now()
	status, _ = httpCall(t, "GET", fmt.Sprintf("%s/out?since=%d&wait=1.5", device, transport.firstSeq), token, "")
	require.Equal(t, http.StatusOK, status)
	assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond)
	assert.True(t, deviceOnline(h, "panel-1"), "online while the request waited")
	eventually(t, func() bool { return !deviceOnline(h, "panel-1") }, "the device to go offline")
	h.eventsOf("device.online")
	status, _ = httpCall(t, "POST", device+"/in/count", token, `{"v":0}`)
	assert.Equal(t, http.StatusNoContent, status)
	assert.True(t, deviceOnline(h, "panel-1"))
	assert.Equal(t, 1, len(h.eventsOf("device.online")))

	// Stopping the transport takes its devices offline.
	transport.Stop()
	assert.False(t, deviceOnline(h, "panel-1"))
}
