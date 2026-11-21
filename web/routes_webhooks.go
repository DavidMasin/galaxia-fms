// Routes for webhooks: the Webhooks page, its delivery log, and the webhook API.

package web

// addWebhookRoutes registers the Webhooks page and the webhook API. The pages are built in the browser from the API.
func (web *Web) addWebhookRoutes(mux *routeMux) {
	mux.HandleFunc("GET /setup/webhooks", web.webhooksPageHandler)
	mux.HandleFunc("GET /setup/webhooks/{id}", web.webhookLogPageHandler)

	mux.HandleFunc("GET /api/v1/webhooks", web.apiWebhooksHandler)
	mux.HandleFunc("POST /api/v1/webhooks", web.apiWebhookCreateHandler)
	mux.HandleFunc("GET /api/v1/webhooks/{id}", web.apiWebhookHandler)
	mux.HandleFunc("PUT /api/v1/webhooks/{id}", web.apiWebhookUpdateHandler)
	mux.HandleFunc("DELETE /api/v1/webhooks/{id}", web.apiWebhookDeleteHandler)
	mux.HandleFunc("POST /api/v1/webhooks/{id}/secret", web.apiWebhookSecretHandler)
	mux.HandleFunc("POST /api/v1/webhooks/{id}/test", web.apiWebhookTestHandler)
	mux.HandleFunc("GET /api/v1/webhooks/{id}/deliveries", web.apiWebhookDeliveriesHandler)
	mux.HandleFunc("POST /api/v1/webhooks/{id}/deliveries/{deliveryId}/resend", web.apiWebhookResendHandler)
}
