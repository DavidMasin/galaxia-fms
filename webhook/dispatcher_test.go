package webhook

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	testTimeout = 300 * time.Millisecond // How long the test client waits for a receiver.
	waitLimit   = 5 * time.Second        // How long a test waits for the dispatcher to get somewhere.
)

// testBackoff doubles from 20 ms, so that a test sees the delays grow without waiting for them.
func testBackoff(attempt int) time.Duration {
	return 20 * time.Millisecond << (attempt - 1)
}

// testClock is a clock a test moves by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Now()}
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func openTestDb(t *testing.T, path string) *model.Database {
	t.Helper()
	db, err := model.OpenDatabase(path)
	require.Nil(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestDb(t *testing.T) *model.Database {
	return openTestDb(t, filepath.Join(t.TempDir(), "test.db"))
}

// newTestDispatcher makes a dispatcher that retries within milliseconds and is stopped when the test ends.
func newTestDispatcher(t *testing.T, db *model.Database, b *bus.Bus, change func(*Options)) *Dispatcher {
	t.Helper()
	options := Options{
		Bus: b, Database: func() *model.Database { return db }, Backoff: testBackoff, Poll: 5 * time.Millisecond,
		Client: &http.Client{
			Timeout:       testTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if change != nil {
		change(&options)
	}
	d := NewDispatcher(options)
	t.Cleanup(d.Stop)
	return d
}

func createWebhook(t *testing.T, d *Dispatcher, url string, events ...string) View {
	t.Helper()
	view, err := d.Create(Changes{Url: &url, Events: events})
	require.Nil(t, err)
	return view
}

// secretOf returns a function giving a webhook's current secret, for a receiver made before the webhook.
func secretOf(d *Dispatcher, id *int) func() string {
	return func() string {
		view, err := d.Webhook(*id)
		if err != nil {
			return ""
		}
		return view.Secret
	}
}

func deliveriesOf(t *testing.T, d *Dispatcher, id int) []DeliveryView {
	t.Helper()
	deliveries, _, err := d.Deliveries(id, "", 0, 1000)
	require.Nil(t, err)
	return deliveries
}

// waitForDeliveries waits until a webhook's log satisfies a condition, and returns it.
func waitForDeliveries(t *testing.T, d *Dispatcher, id int, done func([]DeliveryView) bool) []DeliveryView {
	t.Helper()
	var deliveries []DeliveryView
	require.Eventually(t, func() bool {
		deliveries = deliveriesOf(t, d, id)
		return done(deliveries)
	}, waitLimit, 5*time.Millisecond)
	return deliveries
}

func allHaveStatus(count int, status string) func([]DeliveryView) bool {
	return func(deliveries []DeliveryView) bool {
		if len(deliveries) != count {
			return false
		}
		for _, delivery := range deliveries {
			if delivery.Status != status {
				return false
			}
		}
		return true
	}
}

// The milestone's acceptance test: a receiver that checks every signature fails the first attempts with errors
// and a timeout, and then accepts the delivery, which kept its id throughout.
func TestSignedDeliveryIsRetriedUntilAccepted(t *testing.T) {
	b := bus.New(100)
	db := newTestDb(t)
	var backoffMu sync.Mutex
	var backoffCalls []int
	d := newTestDispatcher(t, db, b, func(options *Options) {
		options.Backoff = func(attempt int) time.Duration {
			backoffMu.Lock()
			defer backoffMu.Unlock()
			backoffCalls = append(backoffCalls, attempt)
			return testBackoff(attempt)
		}
	})
	var hookId int
	receiver := newTestReceiver(t, secretOf(d, &hookId), time.Now, func(n int) (int, time.Duration) {
		switch n {
		case 1:
			return http.StatusInternalServerError, 0
		case 2:
			return http.StatusOK, 3 * testTimeout // Too late: the dispatcher has given up on this attempt.
		case 3:
			return http.StatusServiceUnavailable, 0
		}
		return http.StatusAccepted, 0
	})
	hookId = createWebhook(t, d, receiver.URL, "match").Id
	require.Nil(t, d.Start())

	b.Publish("score.changed", map[string]any{"points": 5}) // Not subscribed to.
	event := b.Publish("match.started", map[string]any{"time": "now"})
	deliveries := waitForDeliveries(t, d, hookId, allHaveStatus(1, model.WebhookSucceeded))
	delivery := deliveries[0]

	requests := receiver.received()
	require.Equal(t, 4, len(requests))
	for i, request := range requests {
		assert.Empty(t, request.Problem, "attempt %d", i+1)
		assert.Equal(t, "match.started", request.Event)
		assert.Equal(t, delivery.Id, request.Delivery, "the delivery id stays the same")
		assert.Equal(t, []string{"1", "2", "3", "4"}[i], request.Attempt)
		assert.Equal(t, string(requests[0].Body), string(request.Body), "every attempt sends the same body")
	}
	assert.Len(t, delivery.Id, 36)

	// The body is the event envelope, as the event stream sends it.
	var envelope bus.Event
	require.Nil(t, json.Unmarshal(requests[0].Body, &envelope))
	assert.Equal(t, event.Seq, envelope.Seq)
	assert.Equal(t, "match.started", envelope.Type)
	assert.Equal(t, map[string]any{"time": "now"}, envelope.Data)
	assert.True(t, event.Time.Equal(envelope.Time))

	// Each wait was the backoff for the attempt that had failed, and the backoffs grew.
	backoffMu.Lock()
	assert.Equal(t, []int{1, 2, 3}, backoffCalls)
	backoffMu.Unlock()
	for i := 1; i < len(requests); i++ {
		gap := requests[i].At.Sub(requests[i-1].At)
		assert.GreaterOrEqual(t, gap, testBackoff(i), "wait before attempt %d", i+1)
	}

	// The log has every attempt.
	assert.Equal(t, 4, delivery.Attempts)
	assert.Equal(t, http.StatusAccepted, delivery.ResponseCode)
	assert.Equal(t, "answer 4", delivery.ResponseBody)
	assert.Empty(t, delivery.Error)
	assert.Nil(t, delivery.NextAttemptAt)
	assert.Equal(t, uint64(event.Seq), delivery.Seq)
	if assert.Len(t, delivery.History, 4) {
		codes := []int{}
		for _, attempt := range delivery.History {
			codes = append(codes, attempt.ResponseCode)
		}
		assert.Equal(t, []int{500, 0, 503, 202}, codes)
		assert.Equal(t, "the receiver answered 500 Internal Server Error", delivery.History[0].Error)
		assert.Equal(t, "no answer within the time limit", delivery.History[1].Error)
		assert.GreaterOrEqual(t, delivery.History[1].LatencyMs, int(testTimeout.Milliseconds()))
		assert.Equal(t, "the receiver answered 503 Service Unavailable", delivery.History[2].Error)
		assert.Empty(t, delivery.History[3].Error)
	}
	assert.Equal(t, 0, d.OutboxDepth())
}

// The receiver's check refuses what it should.
func TestReceiverRefusesBadSignatures(t *testing.T) {
	secret := "a-secret-of-some-length"
	body := []byte(`{"seq":1,"type":"match.started"}`)
	now := time.Unix(1_800_000_000, 0)
	timestamp := "1800000000"
	header := http.Header{}
	header.Set("X-Galaxia-Timestamp", timestamp)
	header.Set("X-Galaxia-Signature", sign(secret, timestamp, body))
	assert.Nil(t, verifySignature(secret, header, body, now))
	assert.Nil(t, verifySignature(secret, header, body, now.Add(4*time.Minute)))

	assert.NotNil(t, verifySignature("another-secret-entirely", header, body, now))
	assert.NotNil(t, verifySignature(secret, header, []byte(`{"seq":2,"type":"match.started"}`), now))
	assert.NotNil(t, verifySignature(secret, header, body, now.Add(6*time.Minute)), "a replayed delivery")
	header.Set("X-Galaxia-Timestamp", "1800000001")
	assert.NotNil(t, verifySignature(secret, header, body, now), "a changed timestamp")

	// The signature is lowercase hex of HMAC-SHA256 over "<timestamp>.<body>", as computed independently by Python's
	// hmac module.
	assert.Equal(
		t, "sha256=91b5374b153842ad05b2c4eab9349b8321b14703165bd3fb8b034dfb8be98ae5", sign("key", "1", []byte("body")),
	)
}

// Only enabled webhooks get events, and only those their filters select; a test event goes to a disabled one.
func TestFiltersAndDisabledWebhooks(t *testing.T) {
	b := bus.New(100)
	db := newTestDb(t)
	d := newTestDispatcher(t, db, b, nil)
	secret := "one-secret-for-all-three"
	receiver := newTestReceiver(t, func() string { return secret }, time.Now, nil)
	create := func(events ...string) View {
		view, err := d.Create(Changes{Url: &receiver.URL, Events: events, Secret: &secret})
		require.Nil(t, err)
		return view
	}
	started := create("match.started")
	every := create("*")
	scores := create("score.", "device.input")
	disabled := false
	_, err := d.Update(scores.Id, Changes{Enabled: &disabled})
	require.Nil(t, err)
	require.Nil(t, d.Start())

	b.Publish("match.started", nil)
	b.Publish("match.started_late", nil) // A filter selects whole words only.
	b.Publish("score.changed", nil)
	b.Publish("device.input", nil)
	waitForDeliveries(t, d, every.Id, allHaveStatus(4, model.WebhookSucceeded))
	assert.Equal(t, []string{"match.started"}, eventsOf(deliveriesOf(t, d, started.Id)))
	assert.Equal(
		t, []string{"device.input", "score.changed", "match.started_late", "match.started"},
		eventsOf(deliveriesOf(t, d, every.Id)),
	)
	assert.Empty(t, deliveriesOf(t, d, scores.Id), "a disabled webhook gets no events")

	// A test event is sent to a disabled webhook all the same.
	test, err := d.SendTest(scores.Id)
	require.Nil(t, err)
	assert.Equal(t, TestEventType, test.Event)
	assert.True(t, test.Manual)
	deliveries := waitForDeliveries(t, d, scores.Id, allHaveStatus(1, model.WebhookSucceeded))
	var envelope map[string]any
	require.Nil(t, json.Unmarshal([]byte(deliveries[0].Body), &envelope))
	assert.Equal(t, TestEventType, envelope["type"])
	assert.Equal(t, float64(0), envelope["seq"])

	// Enabled again, it gets the events it subscribes to from then on.
	enabled := true
	_, err = d.Update(scores.Id, Changes{Enabled: &enabled})
	require.Nil(t, err)
	b.Publish("score.undone", nil)
	b.Publish("match.ended", nil)
	deliveries = waitForDeliveries(t, d, scores.Id, allHaveStatus(2, model.WebhookSucceeded))
	assert.Equal(t, []string{"score.undone", TestEventType}, eventsOf(deliveries))
	for _, request := range receiver.received() {
		assert.Empty(t, request.Problem)
	}
}

func eventsOf(deliveries []DeliveryView) []string {
	events := []string{}
	for _, delivery := range deliveries {
		events = append(events, delivery.Event)
	}
	return events
}
