// The Dispatcher: it turns bus events into deliveries in the outbox, and sends what is due.

package webhook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultWorkers    = 4
	defaultPoll       = time.Second
	defaultPruneEvery = time.Minute
	defaultBuffer     = 1024
	userAgent         = "Galaxia-FMS-Webhooks/1"
)

// Options configures a Dispatcher. Everything but Database has a default; tests replace the clock and the backoff
// to run in milliseconds.
type Options struct {
	// The bus to read events from. Without one, only tests and resends are delivered.
	Bus *bus.Bus
	// Returns the database. It is asked every time rather than kept, because restoring a backup replaces it.
	Database func() *model.Database
	// Sends the requests. The default gives up after DefaultTimeout and does not follow redirects: a redirected
	// POST loses its body, and the URL is better corrected on the page.
	Client *http.Client
	// The clock the outbox is scheduled on and timestamps are signed with.
	Now func() time.Time
	// How long to wait after a delivery's attempt-th failed attempt.
	Backoff func(attempt int) time.Duration
	// Makes the X-Galaxia-Delivery id of a new delivery.
	NewId func() string
	// How long a delivery is retried for.
	RetryFor time.Duration
	// How many requests may be under way at once, across all webhooks. One webhook never has more than one, so a
	// receiver that is down cannot hold up the others.
	Workers int
	// The longest the scheduler sleeps before looking at the outbox again.
	Poll time.Duration
	// How often the log is pruned.
	PruneEvery   time.Duration
	KeepFinished int
	MaxPending   int
	// How many events the bus may hold for the dispatcher before dropping them.
	Buffer int
}

// Dispatcher keeps the webhooks' outbox and sends it.
type Dispatcher struct {
	options  Options
	wake     chan struct{}
	startSeq uint64 // The bus's last event when the dispatcher was made: Start catches up from here.
	lastSeq  uint64 // The last event queued. Only the listening goroutine uses it.

	mu         sync.Mutex
	db         *model.Database // The database the fields below were loaded from.
	generation int             // Counts reloads, so that a send started before one does not touch what came after.
	hooks      map[int]*model.Webhook
	pending    map[int]*model.WebhookDelivery // The outbox, by record id.
	latest     map[int]*model.WebhookDelivery // The newest delivery of each webhook, by webhook id.
	sending    map[int]bool                   // Deliveries being sent now, by record id.
	busy       map[int]bool                   // Webhooks with a request under way, by webhook id.
	active     int                            // Requests under way.
	lastPrune  time.Time
	loadFailed bool // The last load failed and was logged; a success is logged once more.
	state      int
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

const (
	stateNew = iota
	stateRunning
	stateStopped
)

// NewDispatcher makes a dispatcher. Nothing runs until Start, but the events published from now on are delivered.
func NewDispatcher(options Options) *Dispatcher {
	if options.Client == nil {
		options.Client = &http.Client{
			Timeout: DefaultTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Backoff == nil {
		options.Backoff = DefaultBackoff
	}
	if options.NewId == nil {
		options.NewId = uuid.NewString
	}
	if options.RetryFor <= 0 {
		options.RetryFor = DefaultRetryFor
	}
	if options.Workers < 1 {
		options.Workers = defaultWorkers
	}
	if options.Poll <= 0 {
		options.Poll = defaultPoll
	}
	if options.PruneEvery <= 0 {
		options.PruneEvery = defaultPruneEvery
	}
	if options.KeepFinished < 1 {
		options.KeepFinished = DefaultKeepFinished
	}
	if options.MaxPending < 1 {
		options.MaxPending = DefaultMaxPending
	}
	if options.Buffer < 1 {
		options.Buffer = defaultBuffer
	}
	d := &Dispatcher{options: options, wake: make(chan struct{}, 1)}
	if options.Bus != nil {
		d.startSeq = options.Bus.LastSeq()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	return d
}

// Start loads the outbox and starts sending. Events published since the dispatcher was made are queued first.
func (d *Dispatcher) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != stateNew {
		return errors.New("the webhook dispatcher was already started")
	}
	d.state = stateRunning
	// A database that cannot be read now is tried again on every pass of the scheduler, so start regardless.
	err := d.loadLocked()
	var sub *bus.Subscription
	if d.options.Bus != nil {
		sub = d.options.Bus.Subscribe(nil, d.options.Buffer)
	}
	d.wg.Add(2)
	go d.listen(sub)
	go d.schedule()
	return err
}

// Stop stops sending and waits for the requests under way to end. A delivery cut short stays in the outbox as it
// was, and is sent again after the next Start: receivers may see it twice, which X-Galaxia-Delivery tells them.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	running := d.state == stateRunning
	d.state = stateStopped
	d.mu.Unlock()
	d.cancel()
	if running {
		d.wg.Wait()
	}
}

// OutboxDepth returns how many deliveries are waiting to be sent.
func (d *Dispatcher) OutboxDepth() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loadLocked() != nil {
		return 0
	}
	return len(d.pending)
}

func (d *Dispatcher) now() time.Time {
	return d.options.Now().UTC()
}

func (d *Dispatcher) wakeUp() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// loadLocked reads the webhooks and the outbox, if they have not been read from the current database yet.
func (d *Dispatcher) loadLocked() error {
	db := d.options.Database()
	if db == nil {
		return errors.New("there is no database")
	}
	if db == d.db {
		return nil
	}
	webhooks, err := db.GetAllWebhooks()
	var deliveries []model.WebhookDelivery
	if err == nil {
		deliveries, err = db.GetAllWebhookDeliveries()
	}
	if err != nil {
		if !d.loadFailed {
			log.Printf("Webhooks: failed to read the outbox: %v", err)
		}
		d.loadFailed = true
		return err
	}
	if d.loadFailed {
		log.Printf("Webhooks: the outbox was read")
		d.loadFailed = false
	}
	d.db = db
	d.generation++
	d.hooks = make(map[int]*model.Webhook, len(webhooks))
	for i := range webhooks {
		d.hooks[webhooks[i].Id] = &webhooks[i]
	}
	// Only what is kept is copied out of the slice, which may hold the whole log.
	d.pending = map[int]*model.WebhookDelivery{}
	d.latest = map[int]*model.WebhookDelivery{}
	for _, delivery := range deliveries {
		pending := delivery.Status == model.WebhookPending
		newest := d.latest[delivery.WebhookId] == nil
		if !pending && !newest {
			continue
		}
		record := delivery
		if pending {
			d.pending[record.Id] = &record
		}
		if newest {
			d.latest[record.WebhookId] = &record
		}
	}
	d.sending = map[int]bool{}
	d.busy = map[int]bool{}
	return nil
}

// listen queues a delivery for every event a webhook subscribes to. It takes every event, not only those the
// webhooks want, so that sequence numbers run without a break and a jump means events were lost.
func (d *Dispatcher) listen(sub *bus.Subscription) {
	defer d.wg.Done()
	if sub == nil {
		return
	}
	defer sub.Close()
	d.lastSeq = d.startSeq
	d.catchUp(nil)
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-sub.C:
			if !ok {
				return
			}
			d.receive(sub, event)
		}
	}
}

// receive queues one event from the subscription, filling in any the bus dropped because the dispatcher fell
// behind.
func (d *Dispatcher) receive(sub *bus.Subscription, event bus.Event) {
	if sub.Dropped() > 0 {
		// Reset before reading the bus's memory: anything dropped after this is counted again and caught next time,
		// and anything read twice is skipped by its sequence number.
		sub.ResetDropped()
		d.catchUp(&event)
	} else if event.Seq > d.lastSeq {
		d.enqueueNext(event)
	}
}

// catchUp queues, in order, the events the bus remembers after the last one queued, together with the event in
// hand if there is one: it was waiting in the subscription when the events after it were dropped, so it can be
// older than all of them, and old enough for the bus to have forgotten it.
func (d *Dispatcher) catchUp(current *bus.Event) {
	events, _ := d.options.Bus.Since(d.lastSeq, nil)
	if current != nil && current.Seq > d.lastSeq {
		i, found := slices.BinarySearchFunc(events, current.Seq, func(event bus.Event, seq uint64) int {
			return cmp.Compare(event.Seq, seq)
		})
		if !found {
			events = slices.Insert(events, i, *current)
		}
	}
	for _, event := range events {
		d.enqueueNext(event)
	}
}

// enqueueNext queues the next event, after a notice of the events between it and the last one queued, which were
// lost.
func (d *Dispatcher) enqueueNext(event bus.Event) {
	if event.Seq > d.lastSeq+1 {
		d.enqueueGap(d.lastSeq, event.Seq)
	}
	d.enqueue(event)
}

// enqueue queues an event for every enabled webhook that subscribes to it.
func (d *Dispatcher) enqueue(event bus.Event) {
	d.lastSeq = max(d.lastSeq, event.Seq)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loadLocked() != nil {
		log.Printf("Webhooks: event %d (%s) was not queued: the outbox cannot be read", event.Seq, event.Type)
		return
	}
	var body []byte
	queued := false
	for _, hook := range d.sortedHooksLocked() {
		if !hook.Enabled || !Matches(hook.Events, event.Type) {
			continue
		}
		if body == nil {
			var err error
			if body, err = json.Marshal(event); err != nil {
				log.Printf("Webhooks: event %d (%s) cannot be written as JSON: %v", event.Seq, event.Type, err)
				return
			}
		}
		if _, err := d.queueLocked(hook, event.Seq, event.Type, body, false); err != nil {
			log.Printf("Webhooks: failed to queue event %d for webhook %d: %v", event.Seq, hook.Id, err)
			continue
		}
		queued = true
	}
	if queued {
		d.wakeUp()
	}
}

// enqueueGap tells every enabled webhook that the events after one sequence number and before another were lost.
// It goes to every webhook because nobody can know which of them the lost events were for.
func (d *Dispatcher) enqueueGap(after, before uint64) {
	log.Printf("Webhooks: events %d to %d were lost before they could be queued", after+1, before-1)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loadLocked() != nil {
		return
	}
	body, err := json.Marshal(bus.Event{
		Time: d.now(), Type: GapEventType, Data: map[string]any{"after": after, "before": before},
	})
	if err != nil {
		return
	}
	for _, hook := range d.sortedHooksLocked() {
		if hook.Enabled {
			if _, err = d.queueLocked(hook, 0, GapEventType, body, false); err != nil {
				log.Printf("Webhooks: failed to queue a gap notice for webhook %d: %v", hook.Id, err)
			}
		}
	}
	d.wakeUp()
}

func (d *Dispatcher) sortedHooksLocked() []*model.Webhook {
	hooks := make([]*model.Webhook, 0, len(d.hooks))
	for _, hook := range d.hooks {
		hooks = append(hooks, hook)
	}
	slices.SortFunc(hooks, func(a, b *model.Webhook) int { return a.Id - b.Id })
	return hooks
}

// queueLocked writes a new delivery to the outbox. A manual one (a test) is sent even while the webhook is
// disabled.
func (d *Dispatcher) queueLocked(
	hook *model.Webhook, seq uint64, eventType string, body []byte, manual bool,
) (*model.WebhookDelivery, error) {
	now := d.now()
	delivery := &model.WebhookDelivery{
		DeliveryId: d.options.NewId(), WebhookId: hook.Id, EventSeq: seq, EventType: eventType, Body: string(body),
		Status: model.WebhookPending, Manual: manual, CreatedAt: now, NextAttemptAt: now,
		Deadline: now.Add(d.options.RetryFor),
	}
	if err := d.db.CreateWebhookDelivery(delivery); err != nil {
		return nil, err
	}
	d.pending[delivery.Id] = delivery
	d.latest[hook.Id] = delivery
	if len(d.pending) > d.options.MaxPending {
		d.limitOutboxLocked(hook.Id)
	}
	return delivery, nil
}

// limitOutboxLocked gives up the oldest waiting deliveries of a webhook that has more than MaxPending.
func (d *Dispatcher) limitOutboxLocked(webhookId int) {
	var waiting []*model.WebhookDelivery
	for _, delivery := range d.pending {
		if delivery.WebhookId == webhookId {
			waiting = append(waiting, delivery)
		}
	}
	if len(waiting) <= d.options.MaxPending {
		return
	}
	slices.SortFunc(waiting, func(a, b *model.WebhookDelivery) int { return a.Id - b.Id })
	excess := len(waiting) - d.options.MaxPending
	for _, delivery := range waiting {
		if excess == 0 {
			break
		}
		if d.sending[delivery.Id] {
			continue
		}
		d.finishLocked(delivery, model.WebhookFailed, fmt.Sprintf(
			"given up: more than %d deliveries were waiting for this webhook", d.options.MaxPending,
		))
		excess--
	}
}

// finishLocked takes a delivery out of the outbox with its final status.
func (d *Dispatcher) finishLocked(delivery *model.WebhookDelivery, status, message string) {
	delivery.Status = status
	delivery.NextAttemptAt = time.Time{}
	if message != "" {
		delivery.Error = message
	}
	delete(d.pending, delivery.Id)
	if err := d.db.UpdateWebhookDelivery(delivery); err != nil {
		log.Printf("Webhooks: failed to store delivery %s: %v", delivery.DeliveryId, err)
	}
}

// schedule starts sending whatever is due, then sleeps until something else is, or until it is woken.
func (d *Dispatcher) schedule() {
	defer d.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		timer.Reset(d.dispatchDue())
		select {
		case <-d.ctx.Done():
			return
		case <-d.wake:
		case <-timer.C:
		}
	}
}

// dispatchDue starts a request for each due delivery that may be sent now, and returns how long the scheduler may
// sleep. Due deliveries go oldest first, so that a healthy receiver gets events in order.
func (d *Dispatcher) dispatchDue() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	wait := d.options.Poll
	if d.ctx.Err() != nil || d.loadLocked() != nil {
		return wait
	}
	now := d.now()
	if d.lastPrune.IsZero() || now.Sub(d.lastPrune) >= d.options.PruneEvery || now.Before(d.lastPrune) {
		d.lastPrune = now
		d.pruneLocked()
	}

	order := make([]*model.WebhookDelivery, 0, len(d.pending))
	for _, delivery := range d.pending {
		order = append(order, delivery)
	}
	slices.SortFunc(order, func(a, b *model.WebhookDelivery) int {
		if c := a.NextAttemptAt.Compare(b.NextAttemptAt); c != 0 {
			return c
		}
		return a.Id - b.Id
	})
	for _, delivery := range order {
		if d.sending[delivery.Id] {
			continue
		}
		hook := d.hooks[delivery.WebhookId]
		if hook == nil {
			delete(d.pending, delivery.Id)
			continue
		}
		if !hook.Enabled && !delivery.Manual {
			if !now.Before(delivery.Deadline) {
				d.finishLocked(delivery, model.WebhookFailed, fmt.Sprintf(
					"given up: the webhook was disabled for %s", formatDuration(d.options.RetryFor),
				))
			}
			continue
		}
		if d.busy[hook.Id] {
			continue
		}
		if delivery.NextAttemptAt.After(now) {
			wait = min(wait, delivery.NextAttemptAt.Sub(now))
			continue
		}
		if d.active >= d.options.Workers {
			// A request that ends wakes the scheduler.
			break
		}
		d.busy[hook.Id] = true
		d.sending[delivery.Id] = true
		d.active++
		d.wg.Add(1)
		go d.send(d.generation, *delivery, *hook)
	}
	return wait
}

// send makes one attempt at a delivery and records how it went.
func (d *Dispatcher) send(generation int, delivery model.WebhookDelivery, hook model.Webhook) {
	defer d.wg.Done()
	attempt, responseBody, ok := d.attempt(&delivery, &hook)

	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.wakeUp()
	d.active--
	if generation != d.generation {
		// The database was replaced while the request was under way; its record went with it.
		return
	}
	delete(d.busy, hook.Id)
	delete(d.sending, delivery.Id)
	if d.ctx.Err() != nil {
		// Stopping: leave the delivery as it was, to be sent again after a restart.
		return
	}
	current := d.pending[delivery.Id]
	if current == nil {
		// The webhook was deleted meanwhile.
		return
	}
	current.Attempts++
	current.LastAttemptAt = attempt.Time
	current.ResponseCode = attempt.ResponseCode
	current.ResponseBody = responseBody
	current.LatencyMs = attempt.LatencyMs
	current.Error = attempt.Error
	current.History = append(current.History, attempt)
	if len(current.History) > maxHistory {
		current.History = slices.Clone(current.History[len(current.History)-maxHistory:])
	}
	if ok {
		d.finishLocked(current, model.WebhookSucceeded, "")
		return
	}
	now := d.now()
	if !now.Before(current.Deadline) {
		d.finishLocked(current, model.WebhookFailed, fmt.Sprintf(
			"given up after %d attempts over %s: %s", current.Attempts, formatDuration(d.options.RetryFor),
			attempt.Error,
		))
		return
	}
	// The last retry is made at the deadline itself rather than skipped.
	current.NextAttemptAt = now.Add(d.options.Backoff(current.Attempts))
	if current.NextAttemptAt.After(current.Deadline) {
		current.NextAttemptAt = current.Deadline
	}
	if err := d.db.UpdateWebhookDelivery(current); err != nil {
		log.Printf("Webhooks: failed to store delivery %s: %v", current.DeliveryId, err)
	}
}

// attempt sends a delivery once, signed with the time of sending. It reports whether the receiver accepted it.
func (d *Dispatcher) attempt(
	delivery *model.WebhookDelivery, hook *model.Webhook,
) (attempt model.WebhookAttempt, responseBody string, ok bool) {
	attempt.Time = d.now()
	timestamp := strconv.FormatInt(attempt.Time.Unix(), 10)
	body := []byte(delivery.Body)
	request, err := http.NewRequestWithContext(d.ctx, http.MethodPost, hook.Url, bytes.NewReader(body))
	if err != nil {
		attempt.Error = err.Error()
		return attempt, "", false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("X-Galaxia-Event", delivery.EventType)
	request.Header.Set("X-Galaxia-Delivery", delivery.DeliveryId)
	request.Header.Set("X-Galaxia-Timestamp", timestamp)
	request.Header.Set("X-Galaxia-Signature", sign(hook.Secret, timestamp, body))
	request.Header.Set("X-Galaxia-Attempt", strconv.Itoa(delivery.Attempts+1))

	// Latency is measured on the real clock even when the outbox runs on another.
	start := time.Now()
	response, err := d.options.Client.Do(request)
	attempt.LatencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		attempt.Error = describeError(err)
		return attempt, "", false
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	responseBody = strings.ToValidUTF8(string(data), "�")
	attempt.ResponseCode = response.StatusCode
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return attempt, responseBody, true
	}
	attempt.Error = fmt.Sprintf("the receiver answered %d %s", response.StatusCode, http.StatusText(response.StatusCode))
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		attempt.Error += fmt.Sprintf(" to %s; redirects are not followed", response.Header.Get("Location"))
	}
	return attempt, responseBody, false
}

// describeError says why a request got no answer, without repeating the method and URL the log already shows.
func describeError(err error) string {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		if urlError.Timeout() {
			return "no answer within the time limit"
		}
		return urlError.Err.Error()
	}
	return err.Error()
}

// pruneLocked deletes the oldest succeeded and failed deliveries of each webhook beyond KeepFinished, and any left
// behind by a deleted webhook.
func (d *Dispatcher) pruneLocked() {
	deliveries, err := d.db.GetAllWebhookDeliveries()
	if err != nil {
		log.Printf("Webhooks: failed to read the delivery log: %v", err)
		return
	}
	kept := map[int]int{}
	var remove []int
	for _, delivery := range deliveries {
		if d.hooks[delivery.WebhookId] == nil {
			remove = append(remove, delivery.Id)
			delete(d.pending, delivery.Id)
			continue
		}
		if delivery.Status == model.WebhookPending {
			continue
		}
		kept[delivery.WebhookId]++
		if kept[delivery.WebhookId] > d.options.KeepFinished {
			remove = append(remove, delivery.Id)
		}
	}
	if err = d.db.DeleteWebhookDeliveries(remove); err != nil {
		log.Printf("Webhooks: failed to prune the delivery log: %v", err)
	}
}

func formatDuration(duration time.Duration) string {
	switch {
	case duration == time.Hour:
		return "1 hour"
	case duration%time.Hour == 0:
		return fmt.Sprintf("%d hours", duration/time.Hour)
	}
	return duration.String()
}
