// The arena's side of webhooks: it builds the dispatcher that sends bus events to the webhooks set up on the
// Webhooks page.

package field

import (
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/DavidMasin/galaxia-fms/webhook"
	"log"
)

// arenaWebhooks is embedded in Arena, so that the dispatcher is reached as arena.Webhooks.
type arenaWebhooks struct {
	Webhooks *webhook.Dispatcher // Sends events to webhooks from an outbox in the database.
}

// newWebhooks builds the webhook dispatcher. Nothing runs until startWebhooks, so that an arena made for a test
// leaves no goroutines behind; the events published in between are caught up from the bus then.
func (arena *Arena) newWebhooks() {
	arena.Webhooks = webhook.NewDispatcher(webhook.Options{
		Bus: arena.Bus,
		// Asked each time, because restoring a backup replaces the database.
		Database: func() *model.Database { return arena.Database },
	})
}

// startWebhooks starts sending. A database that cannot be read is retried in the background.
func (arena *Arena) startWebhooks() {
	if err := arena.Webhooks.Start(); err != nil {
		log.Printf("Webhooks: %v", err)
	}
}
