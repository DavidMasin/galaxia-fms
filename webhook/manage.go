// What an admin does with webhooks (add, change, delete, test, resend) and how the API and the Webhooks page show
// them. Changes go through the dispatcher, which keeps the webhooks in memory to match every event against them.

package webhook

import (
	"encoding/json"
	"github.com/DavidMasin/galaxia-fms/bus"
	"github.com/DavidMasin/galaxia-fms/model"
	"slices"
	"strings"
	"time"
)

// The status of a delivery whose request is under way, which only the API shows.
const statusSending = "sending"

// View is a webhook as the API shows it.
type View struct {
	Id      int      `json:"id"`
	Name    string   `json:"name"`
	Url     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
	// Only in the answer about one webhook, so that a list does not spread every secret around.
	Secret       string        `json:"secret,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	Pending      int           `json:"pending"` // Deliveries waiting to be sent.
	LastDelivery *DeliveryView `json:"lastDelivery"`
}

// DeliveryView is a delivery as the API and the delivery log show it.
type DeliveryView struct {
	Id            string        `json:"id"` // The X-Galaxia-Delivery header.
	Webhook       int           `json:"webhook"`
	Event         string        `json:"event"`
	Seq           uint64        `json:"seq"`
	Status        string        `json:"status"` // pending, sending, succeeded or failed.
	Manual        bool          `json:"manual"`
	Attempts      int           `json:"attempts"`
	CreatedAt     time.Time     `json:"createdAt"`
	LastAttemptAt *time.Time    `json:"lastAttemptAt"`
	NextAttemptAt *time.Time    `json:"nextAttemptAt"`
	Deadline      *time.Time    `json:"deadline"`
	ResponseCode  int           `json:"responseCode"`
	LatencyMs     int           `json:"latencyMs"`
	Error         string        `json:"error"`
	ResponseBody  string        `json:"responseBody"`
	Body          string        `json:"body"`
	History       []AttemptView `json:"history"`
}

// AttemptView is one try at a delivery.
type AttemptView struct {
	Time         time.Time `json:"time"`
	ResponseCode int       `json:"responseCode"`
	LatencyMs    int       `json:"latencyMs"`
	Error        string    `json:"error"`
}

// Webhooks returns every webhook, without its secret.
func (d *Dispatcher) Webhooks() ([]View, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadLocked(); err != nil {
		return nil, err
	}
	views := []View{}
	for _, hook := range d.sortedHooksLocked() {
		views = append(views, d.viewLocked(hook, false))
	}
	return views, nil
}

// Webhook returns one webhook, with its secret.
func (d *Dispatcher) Webhook(id int) (View, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hook, err := d.hookLocked(id)
	if err != nil {
		return View{}, err
	}
	return d.viewLocked(hook, true), nil
}

// Create adds a webhook.
func (d *Dispatcher) Create(changes Changes) (View, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadLocked(); err != nil {
		return View{}, err
	}
	now := d.now()
	hook := &model.Webhook{Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := applyChanges(hook, changes, true); err != nil {
		return View{}, err
	}
	if hook.Secret == "" {
		var err error
		if hook.Secret, err = newSecret(); err != nil {
			return View{}, err
		}
	}
	if err := d.db.CreateWebhook(hook); err != nil {
		return View{}, err
	}
	d.hooks[hook.Id] = hook
	return d.viewLocked(hook, true), nil
}

// Update changes a webhook. Deliveries already waiting go to its new URL, signed with its new secret.
func (d *Dispatcher) Update(id int, changes Changes) (View, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hook, err := d.hookLocked(id)
	if err != nil {
		return View{}, err
	}
	updated := *hook
	updated.Events = slices.Clone(hook.Events)
	if err = applyChanges(&updated, changes, false); err != nil {
		return View{}, err
	}
	updated.UpdatedAt = d.now()
	if err = d.db.UpdateWebhook(&updated); err != nil {
		return View{}, err
	}
	*hook = updated
	// Enabling it lets what was held back go.
	d.wakeUp()
	return d.viewLocked(hook, true), nil
}

// RegenerateSecret gives a webhook a new random secret.
func (d *Dispatcher) RegenerateSecret(id int) (View, error) {
	secret, err := newSecret()
	if err != nil {
		return View{}, err
	}
	return d.Update(id, Changes{Secret: &secret})
}

// Delete deletes a webhook, its outbox and its delivery log.
func (d *Dispatcher) Delete(id int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.hookLocked(id); err != nil {
		return err
	}
	if err := d.db.DeleteWebhook(id); err != nil {
		return err
	}
	delete(d.hooks, id)
	delete(d.latest, id)
	for recordId, delivery := range d.pending {
		if delivery.WebhookId == id {
			delete(d.pending, recordId)
		}
	}
	return nil
}

// SendTest queues a test delivery for a webhook. It is sent even while the webhook is disabled, so that a receiver
// can be tried before it is switched on.
func (d *Dispatcher) SendTest(id int) (DeliveryView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hook, err := d.hookLocked(id)
	if err != nil {
		return DeliveryView{}, err
	}
	body, err := json.Marshal(bus.Event{
		Time: d.now(), Type: TestEventType,
		Data: map[string]any{
			"webhook": hook.Id, "name": hook.Name, "message": "This is a test delivery from Galaxia FMS.",
		},
	})
	if err != nil {
		return DeliveryView{}, err
	}
	delivery, err := d.queueLocked(hook, 0, TestEventType, body, true)
	if err != nil {
		return DeliveryView{}, err
	}
	d.wakeUp()
	return d.deliveryViewLocked(delivery, true), nil
}

// Deliveries returns a page of a webhook's delivery log, newest first, and how many entries there are in all.
// status, if not empty, keeps only the deliveries with that status.
func (d *Dispatcher) Deliveries(id int, status string, offset, limit int) ([]DeliveryView, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.hookLocked(id); err != nil {
		return nil, 0, err
	}
	switch status {
	case "", model.WebhookPending, model.WebhookSucceeded, model.WebhookFailed:
	default:
		return nil, 0, invalid("the status must be pending, succeeded or failed")
	}
	deliveries, err := d.db.GetWebhookDeliveries(id)
	if err != nil {
		return nil, 0, err
	}
	views := []DeliveryView{}
	total := 0
	for i := range deliveries {
		delivery := &deliveries[i]
		if current := d.pending[delivery.Id]; current != nil {
			delivery = current
		}
		if status != "" && delivery.Status != status {
			continue
		}
		if total >= offset && len(views) < limit {
			views = append(views, d.deliveryViewLocked(delivery, true))
		}
		total++
	}
	return views, total, nil
}

// Resend sends a delivery again, with the same id and body. It is retried for another 24 hours if it fails, and
// sent even while the webhook is disabled. A delivery that is still waiting is tried at once.
func (d *Dispatcher) Resend(id int, deliveryId string) (DeliveryView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.hookLocked(id); err != nil {
		return DeliveryView{}, err
	}
	var delivery *model.WebhookDelivery
	for _, pending := range d.pending {
		if pending.WebhookId == id && pending.DeliveryId == deliveryId {
			delivery = pending
		}
	}
	if delivery == nil {
		deliveries, err := d.db.GetWebhookDeliveries(id)
		if err != nil {
			return DeliveryView{}, err
		}
		for i := range deliveries {
			if deliveries[i].DeliveryId == deliveryId {
				found := deliveries[i]
				delivery = &found
			}
		}
	}
	if delivery == nil {
		return DeliveryView{}, ErrNotFound
	}
	if d.sending[delivery.Id] {
		return DeliveryView{}, invalid("the delivery is being sent right now")
	}
	now := d.now()
	updated := *delivery
	updated.Status = model.WebhookPending
	updated.Manual = true
	updated.NextAttemptAt = now
	updated.Deadline = now.Add(d.options.RetryFor)
	updated.Error = ""
	if err := d.db.UpdateWebhookDelivery(&updated); err != nil {
		return DeliveryView{}, err
	}
	*delivery = updated
	d.pending[delivery.Id] = delivery
	if latest := d.latest[id]; latest != nil && latest.Id == delivery.Id {
		d.latest[id] = delivery
	}
	d.wakeUp()
	return d.deliveryViewLocked(delivery, true), nil
}

func (d *Dispatcher) hookLocked(id int) (*model.Webhook, error) {
	if err := d.loadLocked(); err != nil {
		return nil, err
	}
	hook := d.hooks[id]
	if hook == nil {
		return nil, ErrNotFound
	}
	return hook, nil
}

func (d *Dispatcher) viewLocked(hook *model.Webhook, withSecret bool) View {
	view := View{
		Id: hook.Id, Name: hook.Name, Url: hook.Url, Events: slices.Clone(hook.Events), Enabled: hook.Enabled,
		CreatedAt: hook.CreatedAt, UpdatedAt: hook.UpdatedAt,
	}
	if withSecret {
		view.Secret = hook.Secret
	}
	for _, delivery := range d.pending {
		if delivery.WebhookId == hook.Id {
			view.Pending++
		}
	}
	if latest := d.latest[hook.Id]; latest != nil {
		last := d.deliveryViewLocked(latest, false)
		view.LastDelivery = &last
	}
	return view
}

// deliveryViewLocked shows a delivery; full adds the bodies and the attempts.
func (d *Dispatcher) deliveryViewLocked(delivery *model.WebhookDelivery, full bool) DeliveryView {
	view := DeliveryView{
		Id: delivery.DeliveryId, Webhook: delivery.WebhookId, Event: delivery.EventType, Seq: delivery.EventSeq,
		Status: delivery.Status, Manual: delivery.Manual, Attempts: delivery.Attempts, CreatedAt: delivery.CreatedAt,
		LastAttemptAt: optionalTime(delivery.LastAttemptAt), ResponseCode: delivery.ResponseCode,
		LatencyMs: delivery.LatencyMs, Error: delivery.Error, History: []AttemptView{},
	}
	if delivery.Status == model.WebhookPending {
		view.NextAttemptAt = optionalTime(delivery.NextAttemptAt)
		view.Deadline = optionalTime(delivery.Deadline)
		if d.sending[delivery.Id] {
			view.Status = statusSending
		}
	}
	if full {
		view.Body = delivery.Body
		view.ResponseBody = delivery.ResponseBody
		for _, attempt := range delivery.History {
			view.History = append(view.History, AttemptView{
				Time: attempt.Time, ResponseCode: attempt.ResponseCode, LatencyMs: attempt.LatencyMs,
				Error: attempt.Error,
			})
		}
	}
	return view
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// applyChanges checks and makes an admin's changes to a webhook, leaving it as it was if any is refused.
func applyChanges(hook *model.Webhook, changes Changes, creating bool) error {
	updated := *hook
	if changes.Url != nil {
		url, err := checkUrl(*changes.Url)
		if err != nil {
			return err
		}
		updated.Url = url
	} else if creating {
		return invalid("the URL is missing")
	}
	name := updated.Name
	if changes.Name != nil {
		name = *changes.Name
	}
	var err error
	if updated.Name, err = checkName(name, updated.Url); err != nil {
		return err
	}
	if changes.Events != nil {
		if updated.Events, err = checkEvents(changes.Events); err != nil {
			return err
		}
	} else if creating {
		updated.Events = DefaultEvents()
	}
	if changes.Enabled != nil {
		updated.Enabled = *changes.Enabled
	}
	if changes.Secret != nil && strings.TrimSpace(*changes.Secret) != "" {
		if updated.Secret, err = checkSecret(*changes.Secret); err != nil {
			return err
		}
	}
	*hook = updated
	return nil
}
