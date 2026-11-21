// The Webhooks page and the webhook API behind it: adding, changing and deleting webhooks, sending a test event,
// and the delivery log with Resend. The pages are drawn in the browser from the API, so the two cannot drift apart.

package web

import (
	"encoding/json"
	"errors"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/webhook"
	"net/http"
	"strconv"
)

const (
	// The largest request the webhook API takes: a name, a URL, a secret and a list of event types.
	maxWebhookRequestBytes = 64 << 10
	defaultDeliveryPage    = 50
	maxDeliveryPage        = 500
)

// Shows the list of webhooks.
func (web *Web) webhooksPageHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.renderWebhooksPage(w, "templates/setup_webhooks.html", 0)
}

// Shows one webhook's delivery log.
func (web *Web) webhookLogPageHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err == nil {
		_, err = web.arena.Webhooks.Webhook(id)
	}
	if err != nil {
		http.Error(w, "There is no such webhook.", http.StatusNotFound)
		return
	}
	web.renderWebhooksPage(w, "templates/setup_webhooks_log.html", id)
}

func (web *Web) renderWebhooksPage(w http.ResponseWriter, page string, webhookId int) {
	template, err := web.parseFiles(page, "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		WebhookId int
	}{web.arena.EventSettings, webhookId}
	if err = template.ExecuteTemplate(w, "base", data); err != nil {
		handleWebErr(w, err)
	}
}

// Lists the webhooks, with the event types they can subscribe to and how many deliveries are waiting.
func (web *Web) apiWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	webhooks, err := web.arena.Webhooks.Webhooks()
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusOK, map[string]any{
		"webhooks":      webhooks,
		"outboxDepth":   web.arena.Webhooks.OutboxDepth(),
		"eventTypes":    webhook.EventTypes,
		"defaultEvents": webhook.DefaultEvents(),
	})
}

// Adds a webhook. The answer has its secret, which the receiver needs.
func (web *Web) apiWebhookCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	var changes webhook.Changes
	if !decodeWebhookRequest(w, r, &changes) {
		return
	}
	view, err := web.arena.Webhooks.Create(changes)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusCreated, view)
}

// Returns one webhook, with its secret.
func (web *Web) apiWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	view, err := web.arena.Webhooks.Webhook(id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusOK, view)
}

// Changes a webhook. Fields left out of the request keep their value.
func (web *Web) apiWebhookUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	var changes webhook.Changes
	if !decodeWebhookRequest(w, r, &changes) {
		return
	}
	view, err := web.arena.Webhooks.Update(id, changes)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusOK, view)
}

// Deletes a webhook with its outbox and delivery log.
func (web *Web) apiWebhookDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	if err := web.arena.Webhooks.Delete(id); err != nil {
		writeWebhookError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Gives a webhook a new secret. Deliveries already waiting are signed with it from their next attempt.
func (web *Web) apiWebhookSecretHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	view, err := web.arena.Webhooks.RegenerateSecret(id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusOK, view)
}

// Queues a test delivery.
func (web *Web) apiWebhookTestHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	delivery, err := web.arena.Webhooks.SendTest(id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusAccepted, delivery)
}

// Returns a page of a webhook's delivery log, newest first.
func (web *Web) apiWebhookDeliveriesHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	offset, limit := 0, defaultDeliveryPage
	var err error
	if value := query.Get("offset"); value != "" {
		if offset, err = strconv.Atoi(value); err != nil || offset < 0 {
			writeJsonError(w, http.StatusBadRequest, "offset must be a number of at least 0")
			return
		}
	}
	if value := query.Get("limit"); value != "" {
		if limit, err = strconv.Atoi(value); err != nil || limit < 1 || limit > maxDeliveryPage {
			writeJsonError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(maxDeliveryPage))
			return
		}
	}
	deliveries, total, err := web.arena.Webhooks.Deliveries(id, query.Get("status"), offset, limit)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusOK, map[string]any{"deliveries": deliveries, "total": total})
}

// Sends a delivery again, with the same id.
func (web *Web) apiWebhookResendHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, ok := webhookIdParameter(w, r)
	if !ok {
		return
	}
	delivery, err := web.arena.Webhooks.Resend(id, r.PathValue("deliveryId"))
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJson(w, http.StatusAccepted, delivery)
}

// webhookIdParameter reads the webhook id from the path, answering the request itself if it is not a number.
func webhookIdParameter(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJsonError(w, http.StatusBadRequest, "the webhook id must be a number")
		return 0, false
	}
	return id, true
}

// decodeWebhookRequest reads a JSON request body, refusing fields it does not know so that a misspelt one is not
// silently ignored. It answers the request itself and returns false if the body cannot be read.
func decodeWebhookRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebhookRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJsonError(w, http.StatusBadRequest, "the request is not valid: "+err.Error())
		return false
	}
	return true
}

func writeWebhookError(w http.ResponseWriter, err error) {
	var invalid *webhook.InvalidError
	switch {
	case errors.Is(err, webhook.ErrNotFound):
		writeJsonError(w, http.StatusNotFound, "there is no such webhook or delivery")
	case errors.As(err, &invalid):
		writeJsonError(w, http.StatusBadRequest, err.Error())
	default:
		writeJsonError(w, http.StatusInternalServerError, err.Error())
	}
}
