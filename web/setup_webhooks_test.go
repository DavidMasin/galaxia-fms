package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// receivedDelivery is one POST a test receiver got.
type receivedDelivery struct {
	Event, Delivery, Timestamp, Signature string
	Body                                  []byte
}

// webhookReceiver is a program that receives webhooks, as a third party would write it.
type webhookReceiver struct {
	*httptest.Server
	mu         sync.Mutex
	deliveries []receivedDelivery
	status     int // What to answer with; 200 unless set.
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	receiver := &webhookReceiver{status: http.StatusOK}
	receiver.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		receiver.deliveries = append(receiver.deliveries, receivedDelivery{
			Event: r.Header.Get("X-Galaxia-Event"), Delivery: r.Header.Get("X-Galaxia-Delivery"),
			Timestamp: r.Header.Get("X-Galaxia-Timestamp"), Signature: r.Header.Get("X-Galaxia-Signature"),
			Body: body,
		})
		w.WriteHeader(receiver.status)
		w.Write([]byte("<b>thanks</b>"))
	}))
	t.Cleanup(receiver.Close)
	return receiver
}

func (receiver *webhookReceiver) setStatus(status int) {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	receiver.status = status
}

// wait returns the deliveries once there are at least count of them.
func (receiver *webhookReceiver) wait(t *testing.T, count int) []receivedDelivery {
	deadline := time.Now().Add(5 * time.Second)
	for {
		receiver.mu.Lock()
		deliveries := append([]receivedDelivery(nil), receiver.deliveries...)
		receiver.mu.Unlock()
		if len(deliveries) >= count {
			return deliveries
		}
		if time.Now().After(deadline) {
			require.Failf(t, "the receiver was not sent enough", "got %d deliveries, want %d", len(deliveries), count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// signed reports whether a delivery carries the signature the documentation describes, for the given secret.
func (delivery receivedDelivery) signed(secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(delivery.Timestamp + "."))
	mac.Write(delivery.Body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	seconds, err := strconv.ParseInt(delivery.Timestamp, 10, 64)
	recent := err == nil && time.Since(time.Unix(seconds, 0)).Abs() < 5*time.Minute
	return recent && hmac.Equal([]byte(want), []byte(delivery.Signature))
}

func jsonBody(t *testing.T, value any) string {
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

// waitForDelivery polls the delivery log until its newest entry has the wanted status.
func waitForDelivery(t *testing.T, web *Web, webhookId int, status string) map[string]any {
	deadline := time.Now().Add(5 * time.Second)
	for {
		log := decodeJson(t, web.getHttpResponse(fmt.Sprintf("/api/v1/webhooks/%d/deliveries", webhookId)))
		deliveries := log["deliveries"].([]any)
		if len(deliveries) > 0 && deliveries[0].(map[string]any)["status"] == status {
			return deliveries[0].(map[string]any)
		}
		if time.Now().After(deadline) {
			require.Failf(t, "the delivery did not get there", "want status %s, log %v", status, deliveries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebhookPages(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/setup/webhooks")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Webhooks - Untitled Event - Galaxia FMS")
	assert.Contains(t, recorder.Body.String(), "WebhooksPage.startList()")
	assert.Equal(t, 404, web.getHttpResponse("/setup/webhooks/1").Code)
	assert.Equal(t, 404, web.getHttpResponse("/setup/webhooks/x").Code)

	recorder = web.request("POST", "/api/v1/webhooks", `{"url":"http://127.0.0.1:9/hook"}`, nil)
	require.Equal(t, 201, recorder.Code)
	recorder = web.getHttpResponse("/setup/webhooks/1")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "WebhooksPage.startLog(1)")

	// Admins only.
	web.arena.EventSettings.AdminPassword = "the admin password"
	assert.Equal(t, 307, web.getHttpResponse("/setup/webhooks").Code)
	assert.Equal(t, 307, web.getHttpResponse("/setup/webhooks/1").Code)
	_, err := web.arena.Database.CreateUser("sam", "password-of-sam", model.RoleScorer)
	require.NoError(t, err)
	assert.Equal(t, 403, web.request("GET", "/setup/webhooks", "", loginAs(t, web, "sam", "password-of-sam")).Code)
}

func TestWebhookApi(t *testing.T) {
	web := setupTestWeb(t)

	list := decodeJson(t, web.getHttpResponse("/api/v1/webhooks"))
	assert.Empty(t, list["webhooks"])
	assert.Equal(t, float64(0), list["outboxDepth"])
	assert.NotEmpty(t, list["eventTypes"])
	assert.Contains(t, list["defaultEvents"], "match.committed")

	// A new webhook takes its name from its address, the default events, and a secret of its own.
	recorder := web.request("POST", "/api/v1/webhooks", `{"url":" http://overlay.local:9000/hook "}`, nil)
	require.Equal(t, 201, recorder.Code, recorder.Body.String())
	created := decodeJson(t, recorder)
	assert.Equal(t, "overlay.local:9000", created["name"])
	assert.Equal(t, "http://overlay.local:9000/hook", created["url"])
	assert.Equal(t, true, created["enabled"])
	assert.Equal(t, list["defaultEvents"], created["events"])
	secret := created["secret"].(string)
	assert.GreaterOrEqual(t, len(secret), 40)

	// The list does not spread secrets around; the answer about one webhook has it.
	list = decodeJson(t, web.getHttpResponse("/api/v1/webhooks"))
	listed := list["webhooks"].([]any)[0].(map[string]any)
	assert.NotContains(t, listed, "secret")
	assert.Equal(t, secret, decodeJson(t, web.getHttpResponse("/api/v1/webhooks/1"))["secret"])

	// Changing some fields leaves the others.
	recorder = web.request(
		"PUT", "/api/v1/webhooks/1", `{"name":"Overlay","events":["match","score.changed"],"enabled":false}`, nil,
	)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	changed := decodeJson(t, recorder)
	assert.Equal(t, "Overlay", changed["name"])
	assert.Equal(t, []any{"match", "score.changed"}, changed["events"])
	assert.Equal(t, false, changed["enabled"])
	assert.Equal(t, "http://overlay.local:9000/hook", changed["url"])
	assert.Equal(t, secret, changed["secret"])

	recorder = web.request("POST", "/api/v1/webhooks/1/secret", "", nil)
	require.Equal(t, 200, recorder.Code)
	newSecret := decodeJson(t, recorder)["secret"].(string)
	assert.NotEqual(t, secret, newSecret)

	// What cannot be done says why, and changes nothing.
	for _, test := range []struct{ method, path, body, message string }{
		{"POST", "/api/v1/webhooks", `{}`, "the URL is missing"},
		{"POST", "/api/v1/webhooks", `{"url":"ftp://overlay.local/hook"}`, "the URL must start with http:// or https://"},
		{"POST", "/api/v1/webhooks", `{"url":"http://overlay.local","events":["Match Started"]}`,
			"is not an event type or prefix"},
		{"POST", "/api/v1/webhooks", `{"url":"http://overlay.local","events":[]}`, "choose at least one event type"},
		{"POST", "/api/v1/webhooks", `{"url":"http://overlay.local","secret":"short"}`, "the secret must be"},
		{"POST", "/api/v1/webhooks", `{"url":"http://overlay.local","adress":"x"}`, "unknown field"},
		{"POST", "/api/v1/webhooks", `not json`, "the request is not valid"},
		{"PUT", "/api/v1/webhooks/1", `{"url":""}`, "the URL is missing"},
		{"GET", "/api/v1/webhooks/1/deliveries?status=lost", ``, "the status must be pending, succeeded or failed"},
		{"GET", "/api/v1/webhooks/1/deliveries?offset=-1", ``, "offset must be a number of at least 0"},
	} {
		recorder = web.request(test.method, test.path, test.body, nil)
		assert.Equal(t, 400, recorder.Code, test.body)
		assert.Contains(t, decodeJson(t, recorder)["error"], test.message, test.body)
	}
	list = decodeJson(t, web.getHttpResponse("/api/v1/webhooks"))
	assert.Len(t, list["webhooks"], 1)

	assert.Equal(t, 204, web.request("DELETE", "/api/v1/webhooks/1", "", nil).Code)
	for _, test := range []struct{ method, path string }{
		{"GET", "/api/v1/webhooks/1"}, {"PUT", "/api/v1/webhooks/1"}, {"DELETE", "/api/v1/webhooks/1"},
		{"POST", "/api/v1/webhooks/1/secret"}, {"POST", "/api/v1/webhooks/1/test"},
		{"GET", "/api/v1/webhooks/1/deliveries"}, {"POST", "/api/v1/webhooks/1/deliveries/x/resend"},
	} {
		assert.Equal(t, 404, web.request(test.method, test.path, "{}", nil).Code, test.method+" "+test.path)
	}
}

func TestWebhookDeliversSignedEvents(t *testing.T) {
	web := setupTestWeb(t)
	require.NoError(t, web.arena.Webhooks.Start())
	defer web.arena.Webhooks.Stop()
	receiver := newWebhookReceiver(t)

	recorder := web.request("POST", "/api/v1/webhooks", jsonBody(t, map[string]any{
		"url": receiver.URL + "/hook", "name": "Overlay", "events": []string{"match.started"},
	}), nil)
	require.Equal(t, 201, recorder.Code, recorder.Body.String())
	secret := decodeJson(t, recorder)["secret"].(string)

	// A test event is delivered at once and signed with the webhook's secret.
	recorder = web.request("POST", "/api/v1/webhooks/1/test", "", nil)
	require.Equal(t, 202, recorder.Code)
	testId := decodeJson(t, recorder)["id"].(string)
	first := receiver.wait(t, 1)[0]
	assert.Equal(t, "webhook.test", first.Event)
	assert.Equal(t, testId, first.Delivery)
	assert.True(t, first.signed(secret))
	assert.False(t, first.signed(secret+"x"))
	logged := waitForDelivery(t, web, 1, "succeeded")
	assert.Equal(t, float64(200), logged["responseCode"])
	assert.Equal(t, float64(1), logged["attempts"])
	// What the receiver answered is kept as text for the log, whatever it looks like.
	assert.Equal(t, "<b>thanks</b>", logged["responseBody"])

	// What happens on the field goes to the webhooks whose filters select it, in the envelope the event stream uses.
	web.arena.Bus.Publish("score.changed", map[string]any{"points": 2})
	web.arena.Bus.Publish("match.started", map[string]any{"match": "Q1"})
	second := receiver.wait(t, 2)[1]
	assert.Equal(t, "match.started", second.Event)
	assert.True(t, second.signed(secret))
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(second.Body, &envelope))
	assert.Equal(t, "match.started", envelope["type"])
	assert.Equal(t, map[string]any{"match": "Q1"}, envelope["data"])
	assert.NotZero(t, envelope["seq"])
	waitForDelivery(t, web, 1, "succeeded")

	// Resending sends the same delivery again, under the same id.
	recorder = web.request("POST", "/api/v1/webhooks/1/deliveries/"+second.Delivery+"/resend", "", nil)
	require.Equal(t, 202, recorder.Code, recorder.Body.String())
	third := receiver.wait(t, 3)[2]
	assert.Equal(t, second.Delivery, third.Delivery)
	assert.Equal(t, second.Body, third.Body)
	assert.True(t, third.signed(secret))

	// A receiver that fails leaves the delivery waiting for its next try, with why.
	receiver.setStatus(http.StatusInternalServerError)
	web.arena.Bus.Publish("match.started", map[string]any{"match": "Q2"})
	receiver.wait(t, 4)
	waiting := waitForDelivery(t, web, 1, "pending")
	assert.Equal(t, float64(500), waiting["responseCode"])
	assert.Contains(t, waiting["error"], "the receiver answered 500")
	assert.NotNil(t, waiting["nextAttemptAt"])
	list := decodeJson(t, web.getHttpResponse("/api/v1/webhooks"))
	assert.Equal(t, float64(1), list["outboxDepth"])
	assert.Equal(t, float64(1), list["webhooks"].([]any)[0].(map[string]any)["pending"])

	// Once the receiver is back, a resend gets it there without waiting for the retry.
	receiver.setStatus(http.StatusOK)
	recorder = web.request("POST", "/api/v1/webhooks/1/deliveries/"+waiting["id"].(string)+"/resend", "", nil)
	require.Equal(t, 202, recorder.Code)
	receiver.wait(t, 5)
	delivered := waitForDelivery(t, web, 1, "succeeded")
	assert.Equal(t, waiting["id"], delivered["id"])
	assert.Equal(t, float64(2), delivered["attempts"])
	assert.Equal(t, float64(0), decodeJson(t, web.getHttpResponse("/api/v1/webhooks"))["outboxDepth"])
}
