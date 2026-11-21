// Model and datastore methods for webhooks: the subscriptions, and their deliveries. A delivery record is both the
// outbox (while it is pending) and the delivery log (once it has succeeded or failed), so that what the log shows is
// exactly what was, or will be, sent. The webhook package does the sending.

package model

import (
	"go.etcd.io/bbolt"
	"sort"
	"time"
)

// Statuses of a webhook delivery.
const (
	WebhookPending   = "pending"
	WebhookSucceeded = "succeeded"
	WebhookFailed    = "failed"
)

// Webhook is one subscription: where to send events, which ones, and the secret they are signed with.
type Webhook struct {
	Id     int `db:"id"`
	Name   string
	Url    string
	Secret string // Kept in the clear: signing needs it, and the admin needs to copy it into the receiver.
	// Event type prefixes, as the event stream's "types" filter takes them; "*" alone means every event.
	Events    []string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WebhookDelivery is one event to send to one webhook, with what happened when it was sent.
type WebhookDelivery struct {
	Id         int    `db:"id"`
	DeliveryId string // The X-Galaxia-Delivery header: a UUID that stays the same across retries and resends.
	WebhookId  int
	EventSeq   uint64 // Zero for deliveries that are not bus events: a test, or a notice of lost events.
	EventType  string
	Body       string // Exactly the bytes sent and signed.
	Status     string // WebhookPending, WebhookSucceeded or WebhookFailed.
	// Asked for by an admin (a test or a resend): sent even while the webhook is disabled.
	Manual    bool
	Attempts  int
	CreatedAt time.Time
	// When the delivery is next tried, while pending.
	NextAttemptAt time.Time
	// When retrying stops: 24 hours after the delivery was queued or last resent.
	Deadline      time.Time
	LastAttemptAt time.Time
	ResponseCode  int // Of the last attempt; zero when it got no response.
	ResponseBody  string
	LatencyMs     int
	Error         string
	History       []WebhookAttempt // The latest attempts, oldest first.
}

// WebhookAttempt is the outcome of one try at a delivery.
type WebhookAttempt struct {
	Time         time.Time
	ResponseCode int
	LatencyMs    int
	Error        string
}

func (database *Database) CreateWebhook(webhook *Webhook) error {
	return database.webhookTable.create(webhook)
}

func (database *Database) GetWebhookById(id int) (*Webhook, error) {
	return database.webhookTable.getById(id)
}

func (database *Database) UpdateWebhook(webhook *Webhook) error {
	return database.webhookTable.update(webhook)
}

// DeleteWebhook deletes a webhook and its deliveries: the log is of no use without the webhook it belongs to.
func (database *Database) DeleteWebhook(id int) error {
	deliveries, err := database.GetWebhookDeliveries(id)
	if err != nil {
		return err
	}
	ids := make([]int, len(deliveries))
	for i, delivery := range deliveries {
		ids[i] = delivery.Id
	}
	if err = database.DeleteWebhookDeliveries(ids); err != nil {
		return err
	}
	return database.webhookTable.delete(id)
}

// GetAllWebhooks returns every webhook, ordered by id.
func (database *Database) GetAllWebhooks() ([]Webhook, error) {
	webhooks, err := database.webhookTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(webhooks, func(i, j int) bool { return webhooks[i].Id < webhooks[j].Id })
	return webhooks, nil
}

func (database *Database) TruncateWebhooks() error {
	if err := database.webhookDeliveryTable.truncate(); err != nil {
		return err
	}
	return database.webhookTable.truncate()
}

func (database *Database) CreateWebhookDelivery(delivery *WebhookDelivery) error {
	return database.webhookDeliveryTable.create(delivery)
}

func (database *Database) GetWebhookDeliveryById(id int) (*WebhookDelivery, error) {
	return database.webhookDeliveryTable.getById(id)
}

func (database *Database) UpdateWebhookDelivery(delivery *WebhookDelivery) error {
	return database.webhookDeliveryTable.update(delivery)
}

// GetWebhookDeliveries returns the deliveries of one webhook, newest first.
func (database *Database) GetWebhookDeliveries(webhookId int) ([]WebhookDelivery, error) {
	return database.filterWebhookDeliveries(func(delivery *WebhookDelivery) bool {
		return delivery.WebhookId == webhookId
	})
}

// GetAllWebhookDeliveries returns the deliveries of every webhook, newest first.
func (database *Database) GetAllWebhookDeliveries() ([]WebhookDelivery, error) {
	return database.filterWebhookDeliveries(func(delivery *WebhookDelivery) bool { return true })
}

// GetPendingWebhookDeliveries returns the outbox: every delivery still to be sent, oldest first.
func (database *Database) GetPendingWebhookDeliveries() ([]WebhookDelivery, error) {
	deliveries, err := database.filterWebhookDeliveries(func(delivery *WebhookDelivery) bool {
		return delivery.Status == WebhookPending
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].Id < deliveries[j].Id })
	return deliveries, nil
}

func (database *Database) filterWebhookDeliveries(keep func(*WebhookDelivery) bool) ([]WebhookDelivery, error) {
	all, err := database.webhookDeliveryTable.getAll()
	if err != nil {
		return nil, err
	}
	deliveries := []WebhookDelivery{}
	for i := range all {
		if keep(&all[i]) {
			deliveries = append(deliveries, all[i])
		}
	}
	// Ids grow with time, and the table orders them as strings.
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].Id > deliveries[j].Id })
	return deliveries, nil
}

// DeleteWebhookDeliveries deletes deliveries by id, all in one transaction: pruning the log deletes hundreds at a
// time. Ids that do not exist are ignored.
func (database *Database) DeleteWebhookDeliveries(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	table := database.webhookDeliveryTable
	return table.bolt.Update(func(tx *bbolt.Tx) error {
		bucket, err := table.getBucket(tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = bucket.Delete(idToKey(id)); err != nil {
				return err
			}
		}
		return nil
	})
}
