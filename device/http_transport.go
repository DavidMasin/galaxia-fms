// The HTTP transport, for the simplest clients: a device that cannot hold a connection open announces its manifest
// with PUT, sends each input with POST and long-polls its outputs with GET, presenting its token as a Bearer token
// every time. The web server routes the requests to ServeManifest, ServeInput and ServeOutputs.

package device

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HttpTransportName is the name of the HTTP transport.
const HttpTransportName = "http"

const (
	// HttpPresence is how long an HTTP device counts as online after a request; a request that waits for outputs
	// keeps it online while it waits.
	HttpPresence = 15 * time.Second
	// HttpMaxWait is the longest an output request may wait; a longer wait is cut to it.
	HttpMaxWait       = 60 * time.Second
	httpBodyLimit     = 64 * 1024
	httpPresenceCheck = time.Second
	// A waiting request that is woken by a change waits this much longer, so that every output that changed in one
	// evaluation comes in one answer.
	httpSettle = 20 * time.Millisecond
	// Sounds and device commands are not retained: one waits this long, among the last few, for the device to poll.
	httpEventLife  = time.Minute
	httpEventLimit = 16
)

// errHttpSafety is why a manifest with a safety channel is refused over HTTP.
var errHttpSafety = errors.New(
	"HTTP devices cannot carry safety channels: a stop needs a heartbeat every 200 ms over a connection that stays " +
		"open; connect over MQTT, WebSocket or serial instead",
)

// HttpTransport is the HTTP transport.
type HttpTransport struct {
	presenceMu sync.Mutex // Held while a device's presence is changed and reported, so that reports are in order.

	mu       sync.Mutex
	sink     Sink
	running  bool
	stop     chan struct{}
	loops    sync.WaitGroup
	devices  map[string]*httpDevice
	firstSeq int64 // The sequence number every device starts this run with; one below it is from an earlier run.
	lastSeq  int64
	presence time.Duration
	now      func() time.Time
}

// httpDevice is what the transport keeps about one device.
type httpDevice struct {
	seq       int64         // Changes whenever its outputs change or a sound or command is sent to it.
	changed   chan struct{} // Closed, and replaced, to wake the requests waiting for its outputs.
	accessGen int           // Counts changes to its access, so that a waiting request knows to check it again.
	events    []httpEvent   // Sounds and device commands, oldest first.
	online    bool
	inFlight  int       // Requests being served.
	lastSeen  time.Time // When the last request ended.
}

// httpRequest is a request that has been let in.
type httpRequest struct {
	sink      Sink
	token     string
	access    Access
	accessGen int // The device's accessGen before it was authenticated.
}

// httpEvent is a sound (channel and command) or a device command (do), sent at seq.
type httpEvent struct {
	seq     int64
	time    time.Time
	channel string
	command *Command
	do      *DeviceCommand
}

// httpOutputs answers an output request.
type httpOutputs struct {
	Seq      int64               `json:"seq"`
	Outputs  map[string]*Command `json:"outputs"`
	Commands []DeviceCommand     `json:"commands,omitempty"`
}

func NewHttpTransport() *HttpTransport {
	// Sequence numbers start from the clock, so that a device that polls across a restart of the FMS is not left
	// waiting on a number the new run happens to have reached.
	first := time.Now().UnixMilli()
	return &HttpTransport{
		devices: make(map[string]*httpDevice), firstSeq: first, lastSeq: first, presence: HttpPresence, now: time.Now,
	}
}

func (t *HttpTransport) Name() string {
	return HttpTransportName
}

// Start begins accepting requests.
func (t *HttpTransport) Start(sink Sink) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return nil
	}
	t.sink, t.running, t.stop = sink, true, make(chan struct{})
	t.loops.Add(1)
	go t.expireLoop(t.stop)
	return nil
}

// Stop refuses new requests, answers the waiting ones and reports every device offline.
func (t *HttpTransport) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.stop)
	t.mu.Unlock()
	t.loops.Wait()

	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	var online []string
	for id, dev := range t.devices {
		if dev.online {
			dev.online = false
			online = append(online, id)
		}
	}
	sink := t.sink
	t.mu.Unlock()
	for _, id := range online {
		sink.Offline(HttpTransportName, id)
	}
}

// Status says whether the transport accepts requests.
func (t *HttpTransport) Status() TransportStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TransportStatus{Name: HttpTransportName, Running: t.running}
}

// SendOutput wakes the device's waiting requests. The outputs themselves are read from the sink when the answer is
// made, except a sound, which is kept for the next answer.
func (t *HttpTransport) SendOutput(deviceID, channel string, command *Command) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dev := t.device(deviceID)
	seq := t.bump(dev)
	if command.isTransient() {
		t.addEvent(dev, httpEvent{seq: seq, time: t.now(), channel: channel, command: command.Clone()})
	}
}

// SendCommand keeps a device command for the device's next answer.
func (t *HttpTransport) SendCommand(deviceID string, command DeviceCommand) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dev := t.device(deviceID)
	seq := t.bump(dev)
	t.addEvent(dev, httpEvent{seq: seq, time: t.now(), do: &command})
}

// SendField does nothing: HTTP devices are not told the state of the field.
func (t *HttpTransport) SendField(state FieldState) {}

// AccessChanged makes the device's waiting requests check its rights again. A device that may no longer connect
// (blocked or deleted) is offline at once, as it would be over a connection that the FMS drops.
func (t *HttpTransport) AccessChanged(deviceID string) {
	t.mu.Lock()
	dev, sink := t.device(deviceID), t.sink
	dev.accessGen++
	t.wake(dev)
	t.mu.Unlock()
	if sink == nil {
		return
	}
	go func() {
		if sink.Access(deviceID) != AccessNone {
			return
		}
		t.presenceMu.Lock()
		defer t.presenceMu.Unlock()
		t.mu.Lock()
		wasOnline := dev.online
		dev.online = false
		t.mu.Unlock()
		if wasOnline {
			sink.Offline(HttpTransportName, deviceID)
		}
	}()
}

// ServeManifest serves PUT /api/v1/devices/{id}/manifest: the device announces or updates its manifest, and is told
// whether it is approved.
func (t *HttpTransport) ServeManifest(w http.ResponseWriter, r *http.Request, deviceID string) {
	request, ok := t.begin(w, r, deviceID)
	if !ok {
		return
	}
	defer t.end(deviceID)
	sink := request.sink
	body, ok := readHttpBody(w, r)
	if !ok {
		return
	}
	manifest, err := ParseManifest(body)
	if err == nil && manifest.HasSafety() {
		err = errHttpSafety
	}
	if err != nil {
		sink.Malformed(HttpTransportName, deviceID, "manifest", err)
		status := http.StatusBadRequest
		if err == errHttpSafety {
			status = http.StatusUnprocessableEntity
		}
		writeHttpError(w, status, err.Error())
		return
	}
	if err := sink.Manifest(HttpTransportName, deviceID, manifest); err != nil {
		writeHttpRefusal(w, err)
		return
	}
	writeHttpJson(w, http.StatusOK, map[string]bool{"approved": request.access == AccessApproved})
}

// ServeInput serves POST /api/v1/devices/{id}/in/{ch}: one input, with the body of an MQTT input message.
func (t *HttpTransport) ServeInput(w http.ResponseWriter, r *http.Request, deviceID, channel string) {
	request, ok := t.begin(w, r, deviceID)
	if !ok {
		return
	}
	defer t.end(deviceID)
	sink := request.sink
	if request.access != AccessApproved {
		writeHttpRefusal(w, ErrNotApproved)
		return
	}
	body, ok := readHttpBody(w, r)
	if !ok {
		return
	}
	reading, err := ParseReading(body)
	if err != nil {
		sink.Malformed(HttpTransportName, deviceID, "in/"+channel, err)
		writeHttpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := sink.Input(HttpTransportName, deviceID, channel, reading); err != nil {
		writeHttpRefusal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServeOutputs serves GET /api/v1/devices/{id}/out?since=<seq>&wait=<seconds>: it answers at once unless since is
// the device's current sequence number, in which case it waits up to wait seconds for a change. The answer holds
// every output in force (a channel left out is off), and the sounds and commands sent since since.
func (t *HttpTransport) ServeOutputs(w http.ResponseWriter, r *http.Request, deviceID string) {
	request, ok := t.begin(w, r, deviceID)
	if !ok {
		return
	}
	defer t.end(deviceID)
	sink := request.sink
	if request.access != AccessApproved {
		writeHttpRefusal(w, ErrNotApproved)
		return
	}
	query := r.URL.Query()
	var since int64
	hasSince := query.Get("since") != ""
	if hasSince {
		var err error
		if since, err = strconv.ParseInt(query.Get("since"), 10, 64); err != nil {
			writeHttpError(w, http.StatusBadRequest, "since must be a sequence number from an earlier answer")
			return
		}
	}
	var wait time.Duration
	if query.Get("wait") != "" {
		seconds, err := strconv.ParseFloat(query.Get("wait"), 64)
		if err != nil || seconds < 0 || math.IsNaN(seconds) {
			writeHttpError(w, http.StatusBadRequest, "wait must be a number of seconds")
			return
		}
		wait = time.Duration(math.Min(seconds, HttpMaxWait.Seconds()) * float64(time.Second))
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	t.mu.Lock()
	stop := t.stop
	t.mu.Unlock()
	woken := false
	for waiting := hasSince && wait > 0; waiting; {
		t.mu.Lock()
		dev := t.device(deviceID)
		seq, changed, accessGen := dev.seq, dev.changed, dev.accessGen
		t.mu.Unlock()
		if accessGen != request.accessGen {
			// Its rights changed since it was let in.
			if err := t.recheck(sink, deviceID, request.token); err != nil {
				writeHttpRefusal(w, err)
				return
			}
			request.accessGen = accessGen
		}
		if since != seq {
			break
		}
		select {
		case <-changed:
			woken = true
		case <-timer.C:
			waiting = false
		case <-stop:
			waiting = false
		case <-r.Context().Done():
			return
		}
	}
	if woken {
		select {
		case <-time.After(httpSettle):
		case <-r.Context().Done():
			return
		}
	}

	// The sequence number is read before the outputs, so that an output that changes in between is sent again
	// rather than missed.
	t.mu.Lock()
	dev := t.device(deviceID)
	answer := httpOutputs{Seq: dev.seq}
	t.pruneEvents(dev)
	fromThisRun := hasSince && since >= t.firstSeq && since <= t.lastSeq
	var sounds []httpEvent
	for _, event := range dev.events {
		if !fromThisRun || event.seq <= since {
			continue
		}
		if event.do != nil {
			answer.Commands = append(answer.Commands, *event.do)
		} else {
			sounds = append(sounds, event)
		}
	}
	t.mu.Unlock()
	answer.Outputs = sink.OutputsOf(deviceID)
	for _, sound := range sounds {
		answer.Outputs[sound.channel] = sound.command
	}
	writeHttpJson(w, http.StatusOK, answer)
}

// begin authenticates a request and counts the device as online while it is served. Without it, the request has
// been answered with the reason.
func (t *HttpTransport) begin(w http.ResponseWriter, r *http.Request, deviceID string) (httpRequest, bool) {
	var request httpRequest
	if !ValidDeviceID(deviceID) {
		writeHttpRefusal(w, ErrInvalidID)
		return request, false
	}
	t.mu.Lock()
	sink, running := t.sink, t.running
	request.accessGen = t.device(deviceID).accessGen
	t.mu.Unlock()
	if !running {
		writeHttpError(w, http.StatusServiceUnavailable, "the HTTP device transport is not running")
		return request, false
	}
	token, ok := bearerToken(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeHttpError(w, http.StatusUnauthorized, "send the device's token as Authorization: Bearer <token>")
		return request, false
	}
	access, err := sink.Authenticate(HttpTransportName, deviceID, token)
	if err != nil {
		writeHttpRefusal(w, err)
		return request, false
	}
	request.sink, request.token, request.access = sink, token, access

	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		writeHttpError(w, http.StatusServiceUnavailable, "the HTTP device transport is not running")
		return request, false
	}
	dev := t.device(deviceID)
	dev.inFlight++
	appeared := !dev.online
	dev.online = true
	t.mu.Unlock()
	if appeared {
		sink.Online(HttpTransportName, deviceID, r.RemoteAddr)
	}
	return request, true
}

// end records that a request has been answered: the device stays online for the presence time from now.
func (t *HttpTransport) end(deviceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dev := t.device(deviceID)
	dev.inFlight--
	dev.lastSeen = t.now()
}

// recheck makes sure a device whose rights changed while its request waited may still have its outputs.
func (t *HttpTransport) recheck(sink Sink, deviceID, token string) error {
	switch sink.Access(deviceID) {
	case AccessApproved:
		// Its token may have been replaced.
		_, err := sink.Authenticate(HttpTransportName, deviceID, token)
		return err
	case AccessPending:
		return ErrNotApproved
	}
	return ErrBlocked
}

// expireLoop takes devices offline once they have made no request for the presence time.
func (t *HttpTransport) expireLoop(stop chan struct{}) {
	defer t.loops.Done()
	ticker := time.NewTicker(httpPresenceCheck)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			t.expire()
		}
	}
}

func (t *HttpTransport) expire() {
	t.presenceMu.Lock()
	defer t.presenceMu.Unlock()
	t.mu.Lock()
	now := t.now()
	var gone []string
	for id, dev := range t.devices {
		if dev.online && dev.inFlight == 0 && now.Sub(dev.lastSeen) >= t.presence {
			dev.online = false
			gone = append(gone, id)
		}
	}
	sink := t.sink
	t.mu.Unlock()
	for _, id := range gone {
		sink.Offline(HttpTransportName, id)
	}
}

// device returns what is kept about a device, creating it. The caller holds mu.
func (t *HttpTransport) device(deviceID string) *httpDevice {
	dev := t.devices[deviceID]
	if dev == nil {
		dev = &httpDevice{seq: t.firstSeq, changed: make(chan struct{})}
		t.devices[deviceID] = dev
	}
	return dev
}

// bump gives a device a new sequence number and wakes its waiting requests. The caller holds mu.
func (t *HttpTransport) bump(dev *httpDevice) int64 {
	t.lastSeq++
	dev.seq = t.lastSeq
	t.wake(dev)
	return dev.seq
}

// wake wakes the requests waiting for a device's outputs. The caller holds mu.
func (t *HttpTransport) wake(dev *httpDevice) {
	close(dev.changed)
	dev.changed = make(chan struct{})
}

// addEvent keeps a sound or device command for the device's next answer. The caller holds mu.
func (t *HttpTransport) addEvent(dev *httpDevice, event httpEvent) {
	dev.events = append(dev.events, event)
	t.pruneEvents(dev)
}

// pruneEvents forgets sounds and commands the device has not polled for in time. The caller holds mu.
func (t *HttpTransport) pruneEvents(dev *httpDevice) {
	now := t.now()
	start := max(0, len(dev.events)-httpEventLimit)
	for start < len(dev.events) && now.Sub(dev.events[start].time) > httpEventLife {
		start++
	}
	if start > 0 {
		dev.events = append([]httpEvent(nil), dev.events[start:]...)
	}
}

// bearerToken returns the token of an Authorization: Bearer header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// readHttpBody reads a request body of reasonable size. Without it, the request has been answered.
func readHttpBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeHttpError(w, http.StatusRequestEntityTooLarge, "the body is larger than 64 KiB")
		} else {
			writeHttpError(w, http.StatusBadRequest, "the body could not be read: "+err.Error())
		}
		return nil, false
	}
	return body, true
}

// writeHttpRefusal answers with why the FMS would not do what the device asked.
func writeHttpRefusal(w http.ResponseWriter, err error) {
	status := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, ErrInvalidID):
		status = http.StatusBadRequest
	case errors.Is(err, ErrBadToken):
		w.Header().Set("WWW-Authenticate", "Bearer")
		status = http.StatusUnauthorized
	case errors.Is(err, ErrReservedID), errors.Is(err, ErrBlocked), errors.Is(err, ErrNotApproved):
		status = http.StatusForbidden
	case errors.Is(err, ErrUnknown):
		status = http.StatusNotFound
	}
	writeHttpError(w, status, refusalReason(err))
}

func writeHttpError(w http.ResponseWriter, status int, message string) {
	writeHttpJson(w, status, map[string]string{"error": message})
}

func writeHttpJson(w http.ResponseWriter, status int, value any) {
	data, _ := json.Marshal(value)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
