package webhook

import (
	"encoding/json"
	"errors"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A delivery the receiver keeps refusing is tried for the last time at the 24-hour mark, and then given up.
func TestDeliveryIsGivenUpAfter24Hours(t *testing.T) {
	b := bus.New(100)
	db := newTestDb(t)
	clock := newTestClock()
	d := newTestDispatcher(t, db, b, func(options *Options) {
		options.Now = clock.Now
		options.Backoff = func(int) time.Duration { return time.Hour }
	})
	var hookId int
	receiver := newTestReceiver(t, secretOf(d, &hookId), clock.Now, always(http.StatusBadGateway))
	hookId = createWebhook(t, d, receiver.URL, "match").Id
	require.Nil(t, d.Start())
	queued := clock.Now()
	b.Publish("match.started", nil)

	attempts := func(count int) func([]DeliveryView) bool {
		return func(deliveries []DeliveryView) bool {
			return len(deliveries) == 1 && deliveries[0].Attempts == count && deliveries[0].Status != statusSending
		}
	}
	delivery := waitForDeliveries(t, d, hookId, attempts(1))[0]
	assert.Equal(t, model.WebhookPending, delivery.Status)
	assert.WithinDuration(t, queued.Add(time.Hour), *delivery.NextAttemptAt, time.Second)
	assert.WithinDuration(t, queued.Add(24*time.Hour), *delivery.Deadline, time.Second)

	// Nearly a day later the next attempt fails too, and the one after is due at the deadline rather than an hour on.
	clock.Advance(24*time.Hour - time.Minute)
	delivery = waitForDeliveries(t, d, hookId, attempts(2))[0]
	assert.Equal(t, model.WebhookPending, delivery.Status)
	assert.Equal(t, *delivery.Deadline, *delivery.NextAttemptAt)

	clock.Advance(time.Minute)
	delivery = waitForDeliveries(t, d, hookId, attempts(3))[0]
	assert.Equal(t, model.WebhookFailed, delivery.Status)
	assert.Equal(t, "given up after 3 attempts over 24 hours: the receiver answered 502 Bad Gateway", delivery.Error)
	assert.Nil(t, delivery.NextAttemptAt)
	assert.Equal(t, 0, d.OutboxDepth())

	clock.Advance(48 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	assert.Len(t, receiver.received(), 3, "nothing more is sent")
	for _, request := range receiver.received() {
		assert.Empty(t, request.Problem, "the timestamp follows the dispatcher's clock")
	}

	// A resend starts another 24 hours, and the receiver has recovered.
	receiver.setAnswer(always(http.StatusOK))
	resent, err := d.Resend(hookId, delivery.Id)
	require.Nil(t, err)
	assert.Equal(t, model.WebhookPending, resent.Status)
	assert.True(t, resent.Manual)
	assert.WithinDuration(t, clock.Now().Add(24*time.Hour), *resent.Deadline, time.Second)
	delivery = waitForDeliveries(t, d, hookId, allHaveStatus(1, model.WebhookSucceeded))[0]
	assert.Equal(t, 4, delivery.Attempts)
	assert.Empty(t, delivery.Error)
	requests := receiver.received()
	assert.Equal(t, requests[0].Delivery, requests[3].Delivery, "a resend keeps the delivery id")
	assert.NotEqual(t, requests[0].Timestamp, requests[3].Timestamp, "and is signed with a new timestamp")
}

// Deliveries held for a disabled webhook are given up once they are a day old.
func TestHeldDeliveriesAreGivenUpAfter24Hours(t *testing.T) {
	b := bus.New(100)
	db := newTestDb(t)
	clock := newTestClock()
	d := newTestDispatcher(t, db, b, func(options *Options) { options.Now = clock.Now })
	hook := createWebhook(t, d, "http://127.0.0.1:9/never", "match")
	require.Nil(t, d.Start())
	disabled := false
	_, err := d.Update(hook.Id, Changes{Enabled: &disabled})
	require.Nil(t, err)

	// A delivery queued just before the webhook was disabled waits.
	db.CreateWebhookDelivery(&model.WebhookDelivery{
		DeliveryId: "held", WebhookId: hook.Id, EventType: "match.started", Status: model.WebhookPending,
		CreatedAt: clock.Now(), NextAttemptAt: clock.Now(), Deadline: clock.Now().Add(24 * time.Hour),
	})
	d2 := newTestDispatcher(t, db, b, func(options *Options) { options.Now = clock.Now })
	d.Stop()
	require.Nil(t, d2.Start())
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, model.WebhookPending, deliveriesOf(t, d2, hook.Id)[0].Status)
	assert.Equal(t, 0, deliveriesOf(t, d2, hook.Id)[0].Attempts)

	clock.Advance(24 * time.Hour)
	delivery := waitForDeliveries(t, d2, hook.Id, allHaveStatus(1, model.WebhookFailed))[0]
	assert.Equal(t, "given up: the webhook was disabled for 24 hours", delivery.Error)
	assert.Equal(t, 0, delivery.Attempts)
}

// The outbox is in the database: what was waiting when the FMS stopped is sent when it starts again, under the
// same delivery ids.
func TestOutboxSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.db")
	db := openTestDb(t, path)
	b := bus.New(100)
	slow := func(options *Options) { options.Backoff = func(int) time.Duration { return time.Hour } }
	d := newTestDispatcher(t, db, b, slow)
	var hookId int
	var current *Dispatcher
	receiver := newTestReceiver(t, func() string { return secretOf(current, &hookId)() }, time.Now,
		always(http.StatusInternalServerError))
	current = d
	hookId = createWebhook(t, d, receiver.URL, "match").Id
	require.Nil(t, d.Start())
	b.Publish("match.started", nil)
	b.Publish("match.ended", nil)
	waitForDeliveries(t, d, hookId, func(deliveries []DeliveryView) bool {
		return len(deliveries) == 2 && deliveries[0].Attempts == 1 && deliveries[1].Attempts == 1 &&
			deliveries[0].Status == model.WebhookPending && deliveries[1].Status == model.WebhookPending
	})
	d.Stop()
	// Queued after the dispatcher stopped, so never attempted.
	_, err := d.SendTest(hookId)
	require.Nil(t, err)
	before := deliveriesOf(t, d, hookId)
	require.Nil(t, db.Close())

	// The FMS starts again an hour and a bit later, and the receiver is back.
	receiver.setAnswer(always(http.StatusOK))
	db = openTestDb(t, path)
	later := func() time.Time { return time.Now().Add(61 * time.Minute) }
	receiver.now = later
	d = newTestDispatcher(t, db, bus.New(100), func(options *Options) {
		slow(options)
		options.Now = later
	})
	current = d
	assert.Equal(t, 3, d.OutboxDepth())
	require.Nil(t, d.Start())
	after := waitForDeliveries(t, d, hookId, allHaveStatus(3, model.WebhookSucceeded))
	for i := range after {
		assert.Equal(t, before[i].Id, after[i].Id)
		assert.Equal(t, before[i].Attempts+1, after[i].Attempts)
	}
	assert.Equal(t, []string{TestEventType, "match.ended", "match.started"}, eventsOf(after))
	requests := receiver.received()
	require.Len(t, requests, 5)
	for _, request := range requests {
		assert.Empty(t, request.Problem)
	}
	assert.Equal(t, 0, d.OutboxDepth())
}

// The log keeps the newest finished deliveries of each webhook, all the waiting ones, and nothing of a deleted one.
func TestLogIsPruned(t *testing.T) {
	db := newTestDb(t)
	d := newTestDispatcher(t, db, nil, func(options *Options) { options.KeepFinished = 3 })
	kept := createWebhook(t, d, "http://127.0.0.1:9/a", "match")
	other := createWebhook(t, d, "http://127.0.0.1:9/b", "match")
	disabled := false
	for _, id := range []int{kept.Id, other.Id} {
		_, err := d.Update(id, Changes{Enabled: &disabled})
		require.Nil(t, err)
	}
	add := func(webhookId int, status string) int {
		now := time.Now()
		delivery := &model.WebhookDelivery{
			DeliveryId: "d", WebhookId: webhookId, Status: status, CreatedAt: now, NextAttemptAt: now,
			Deadline: now.Add(time.Hour),
		}
		require.Nil(t, db.CreateWebhookDelivery(delivery))
		return delivery.Id
	}
	var finished, waiting []int
	for i := 0; i < 6; i++ {
		status := model.WebhookSucceeded
		if i%2 == 1 {
			status = model.WebhookFailed
		}
		finished = append(finished, add(kept.Id, status))
		if i < 2 {
			waiting = append(waiting, add(kept.Id, model.WebhookPending))
		}
	}
	otherIds := []int{add(other.Id, model.WebhookSucceeded), add(other.Id, model.WebhookSucceeded)}
	orphan := add(99, model.WebhookFailed)

	// A fresh dispatcher prunes on its first pass.
	d = newTestDispatcher(t, db, nil, func(options *Options) { options.KeepFinished = 3 })
	require.Nil(t, d.Start())
	require.Eventually(t, func() bool {
		all, err := db.GetAllWebhookDeliveries()
		return err == nil && len(all) == 3+2+2
	}, waitLimit, 5*time.Millisecond)
	all, err := db.GetAllWebhookDeliveries()
	require.Nil(t, err)
	var ids []int
	for _, delivery := range all {
		ids = append(ids, delivery.Id)
	}
	assert.ElementsMatch(t, append(append(append([]int{}, finished[3:]...), waiting...), otherIds...), ids)
	assert.NotContains(t, ids, orphan)
	assert.Equal(t, 2, d.OutboxDepth())
}

// One webhook can only have so many deliveries waiting; beyond that the oldest are given up.
func TestOutboxIsLimitedPerWebhook(t *testing.T) {
	db := newTestDb(t)
	d := newTestDispatcher(t, db, nil, func(options *Options) { options.MaxPending = 2 })
	full := createWebhook(t, d, "http://127.0.0.1:9/full", "match")
	quiet := createWebhook(t, d, "http://127.0.0.1:9/quiet", "match")
	_, err := d.SendTest(quiet.Id)
	require.Nil(t, err)
	for i := 0; i < 4; i++ {
		_, err = d.SendTest(full.Id)
		require.Nil(t, err)
	}
	deliveries := deliveriesOf(t, d, full.Id)
	var statuses []string
	for _, delivery := range deliveries {
		statuses = append(statuses, delivery.Status)
	}
	assert.Equal(
		t, []string{model.WebhookPending, model.WebhookPending, model.WebhookFailed, model.WebhookFailed}, statuses,
	)
	assert.Equal(t, "given up: more than 2 deliveries were waiting for this webhook", deliveries[3].Error)
	assert.Equal(t, model.WebhookPending, deliveriesOf(t, d, quiet.Id)[0].Status)
	assert.Equal(t, 3, d.OutboxDepth())
}

// A new secret is used from the next delivery on, and a webhook can be given a secret of its own.
func TestSecrets(t *testing.T) {
	b := bus.New(100)
	db := newTestDb(t)
	d := newTestDispatcher(t, db, b, nil)
	var hookId int
	receiver := newTestReceiver(t, secretOf(d, &hookId), time.Now, nil)
	created := createWebhook(t, d, receiver.URL, "match")
	hookId = created.Id
	assert.Len(t, created.Secret, 43)
	require.Nil(t, d.Start())

	regenerated, err := d.RegenerateSecret(hookId)
	require.Nil(t, err)
	assert.Len(t, regenerated.Secret, 43)
	assert.NotEqual(t, created.Secret, regenerated.Secret)
	_, err = d.SendTest(hookId)
	require.Nil(t, err)
	waitForDeliveries(t, d, hookId, allHaveStatus(1, model.WebhookSucceeded))
	request := receiver.received()[0]
	assert.Empty(t, request.Problem)
	header := http.Header{}
	header.Set("X-Galaxia-Timestamp", request.Timestamp)
	header.Set("X-Galaxia-Signature", sign(regenerated.Secret, request.Timestamp, request.Body))
	assert.NotNil(t, verifySignature(created.Secret, header, request.Body, time.Now()), "the old secret is gone")

	custom := "  my-own-secret-of-24-chars  "
	view, err := d.Update(hookId, Changes{Secret: &custom})
	require.Nil(t, err)
	assert.Equal(t, "my-own-secret-of-24-chars", view.Secret)
	for _, secret := range []string{"short", "has a space in it, so no", strings.Repeat("x", 201)} {
		_, err = d.Update(hookId, Changes{Secret: &secret})
		var invalidError *InvalidError
		assert.True(t, errors.As(err, &invalidError), secret)
	}
	view, err = d.Webhook(hookId)
	require.Nil(t, err)
	assert.Equal(t, "my-own-secret-of-24-chars", view.Secret, "a refused change changes nothing")

	// A list leaves the secrets out.
	views, err := d.Webhooks()
	require.Nil(t, err)
	assert.Empty(t, views[0].Secret)
}

// Events the bus dropped for a slow dispatcher are filled in from its memory, in order, and a notice is queued for
// those it has forgotten.
func TestDroppedEventsAreCaughtUp(t *testing.T) {
	for _, test := range []struct {
		name   string
		ring   int
		events []string // What the webhook receives, in order.
	}{
		{"all remembered", 100, []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}},
		{"some forgotten", 3, []string{"1", "gap 1-8", "8", "9", "10"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := bus.New(test.ring)
			db := newTestDb(t)
			d := newTestDispatcher(t, db, b, nil)
			hook := createWebhook(t, d, "http://127.0.0.1:9/", "*")
			sub := b.Subscribe(nil, 1)
			for i := 0; i < 10; i++ {
				b.Publish("match.cue", nil)
			}
			require.Equal(t, uint64(9), sub.Dropped())
			d.receive(sub, <-sub.C)
			assert.Equal(t, uint64(0), sub.Dropped())
			d.receive(sub, bus.Event{Seq: 10, Type: "match.cue"}) // Already queued from the bus's memory.

			var got []string
			deliveries := deliveriesOf(t, d, hook.Id)
			for i := len(deliveries) - 1; i >= 0; i-- {
				delivery := deliveries[i]
				if delivery.Event == GapEventType {
					var envelope struct{ Data struct{ After, Before int } }
					require.Nil(t, json.Unmarshal([]byte(delivery.Body), &envelope))
					got = append(got, "gap "+itoa(envelope.Data.After)+"-"+itoa(envelope.Data.Before))
				} else {
					got = append(got, itoa(int(delivery.Seq)))
				}
			}
			assert.Equal(t, test.events, got)
		})
	}
}

func itoa(value int) string {
	return strings.TrimSpace(strings.Replace(time.Duration(value).String(), "ns", "", 1))
}

// Events published before the dispatcher started are sent once it does.
func TestEventsBeforeStartAreSent(t *testing.T) {
	b := bus.New(100)
	b.Publish("match.loaded", nil) // Before the dispatcher existed: not its business.
	db := newTestDb(t)
	d := newTestDispatcher(t, db, b, nil)
	var hookId int
	receiver := newTestReceiver(t, secretOf(d, &hookId), time.Now, nil)
	hookId = createWebhook(t, d, receiver.URL, "match").Id
	b.Publish("match.started", nil)
	require.Nil(t, d.Start())
	b.Publish("match.ended", nil)
	deliveries := waitForDeliveries(t, d, hookId, allHaveStatus(2, model.WebhookSucceeded))
	assert.Equal(t, []string{"match.ended", "match.started"}, eventsOf(deliveries))
}

func TestDefaultBackoff(t *testing.T) {
	for attempt, base := range map[int]time.Duration{
		1: 10 * time.Second, 2: 20 * time.Second, 3: 40 * time.Second, 6: 320 * time.Second,
		9: 2560 * time.Second, 10: time.Hour, 30: time.Hour, 1000: time.Hour,
	} {
		for i := 0; i < 50; i++ {
			delay := DefaultBackoff(attempt)
			assert.GreaterOrEqual(t, delay, base*8/10, "attempt %d", attempt)
			assert.LessOrEqual(t, delay, base*12/10, "attempt %d", attempt)
		}
	}
	// About 30 attempts fit in a day: nine delays that double from 10 seconds, then one an hour. The doubling is
	// only worked out for the early attempts, as in DefaultBackoff, because it overflows after some thirty.
	total, attempts := time.Duration(0), 1
	for ; total < DefaultRetryFor; attempts++ {
		delay := maxRetryDelay
		if attempts <= 20 {
			delay = min(firstRetryDelay<<(attempts-1), maxRetryDelay)
		}
		total += delay
	}
	assert.Equal(t, 33, attempts)
}

func TestMatches(t *testing.T) {
	for _, test := range []struct {
		filters []string
		event   string
		want    bool
	}{
		{[]string{"*"}, "device.input", true},
		{[]string{"match"}, "match.started", true},
		{[]string{"match."}, "match.started", true},
		{[]string{"match.started"}, "match.started", true},
		{[]string{"match.started"}, "match.started_late", false},
		{[]string{"match"}, "matches.started", false},
		{[]string{"score", "device.fault"}, "device.fault_cleared", false},
		{[]string{"score", "device.fault"}, "device.fault", true},
		{[]string{"score", "device.fault"}, "score.changed", true},
		{nil, "match.started", false},
	} {
		assert.Equal(t, test.want, Matches(test.filters, test.event), "%v %s", test.filters, test.event)
	}
}

func TestChangesAreChecked(t *testing.T) {
	db := newTestDb(t)
	d := newTestDispatcher(t, db, nil, nil)
	text := func(value string) *string { return &value }

	view, err := d.Create(Changes{Url: text(" https://scores.example.org:8443/hooks/galaxia ")})
	require.Nil(t, err)
	assert.Equal(t, "https://scores.example.org:8443/hooks/galaxia", view.Url)
	assert.Equal(t, "scores.example.org:8443", view.Name, "the host when no name is given")
	assert.Equal(t, DefaultEvents(), view.Events)
	assert.Contains(t, view.Events, "match.committed")
	assert.NotContains(t, view.Events, "score.changed")
	assert.True(t, view.Enabled)

	view, err = d.Update(view.Id, Changes{Name: text("Scoreboard"), Events: []string{"match.", " score ", "match."}})
	require.Nil(t, err)
	assert.Equal(t, "Scoreboard", view.Name)
	assert.Equal(t, []string{"match.", "score"}, view.Events)
	view, err = d.Update(view.Id, Changes{Events: []string{"match", "*"}})
	require.Nil(t, err)
	assert.Equal(t, []string{"*"}, view.Events)

	for _, test := range []struct {
		changes Changes
		message string
	}{
		{Changes{}, "the URL is missing"},
		{Changes{Url: text("  ")}, "the URL is missing"},
		{Changes{Url: text("ftp://example.org/")}, "the URL must start with http:// or https://"},
		{Changes{Url: text("http:///path")}, "the URL has no host"},
		{Changes{Url: text("http://example.org/"), Events: []string{}}, "choose at least one event type"},
		{Changes{Url: text("http://example.org/"), Events: []string{"Match.Started"}}, "is not an event type"},
		{Changes{Url: text("http://example.org/"), Events: []string{"match..started"}}, "is not an event type"},
		{Changes{Url: text("http://example.org/"), Name: text(strings.Repeat("n", 101))}, "the name is longer"},
	} {
		_, err := d.Create(test.changes)
		var invalidError *InvalidError
		if assert.True(t, errors.As(err, &invalidError), test.message) {
			assert.Contains(t, err.Error(), test.message)
		}
	}
	views, err := d.Webhooks()
	require.Nil(t, err)
	assert.Len(t, views, 1)

	_, err = d.Webhook(99)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = d.Update(99, Changes{})
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, d.Delete(99), ErrNotFound)
	_, err = d.SendTest(99)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = d.Resend(view.Id, "no-such-delivery")
	assert.ErrorIs(t, err, ErrNotFound)
	_, _, err = d.Deliveries(view.Id, "lost", 0, 10)
	assert.Error(t, err)
}

// Deleting a webhook deletes its outbox and log.
func TestDeleteWebhook(t *testing.T) {
	db := newTestDb(t)
	d := newTestDispatcher(t, db, nil, nil)
	gone := createWebhook(t, d, "http://127.0.0.1:9/gone", "match")
	stays := createWebhook(t, d, "http://127.0.0.1:9/stays", "match")
	for _, id := range []int{gone.Id, gone.Id, stays.Id} {
		_, err := d.SendTest(id)
		require.Nil(t, err)
	}
	assert.Equal(t, 3, d.OutboxDepth())
	require.Nil(t, d.Delete(gone.Id))
	assert.Equal(t, 1, d.OutboxDepth())
	all, err := db.GetAllWebhookDeliveries()
	require.Nil(t, err)
	assert.Len(t, all, 1)
	views, err := d.Webhooks()
	require.Nil(t, err)
	if assert.Len(t, views, 1) {
		assert.Equal(t, stays.Id, views[0].Id)
		assert.Equal(t, 1, views[0].Pending)
		assert.Equal(t, TestEventType, views[0].LastDelivery.Event)
		assert.Empty(t, views[0].LastDelivery.Body, "a list leaves the bodies out")
	}
}
