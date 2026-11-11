// Package notify decides which notification events fire after a price check
// (Evaluate), and delivers them asynchronously (Dispatcher) through a
// Notifier such as Gotify. Service glues both to the store and is called by
// the scheduler after every recorded check.
package notify

import "context"

// Event kinds, stored in notifications.kind and exposed by the API.
const (
	KindTargetReached = "target_reached"
	KindPriceDrop     = "price_drop"
	KindPriceRise     = "price_rise"
	KindNewLow        = "new_low" // also used for the merged new_low + price_drop message
	KindTrendDown     = "trend_down"
	KindTrendUp       = "trend_up"
	KindBackInStock   = "back_in_stock"
	KindOutOfStock    = "out_of_stock"
	KindScrapeFailing = "scrape_failing"
)

// Priorities per kind (Gotify scale 0..10).
var priorities = map[string]int{
	KindTargetReached: 8,
	KindPriceDrop:     6,
	KindNewLow:        6,
	KindBackInStock:   6,
	KindScrapeFailing: 5,
	KindTrendDown:     4,
	KindPriceRise:     3,
	KindOutOfStock:    3,
	KindTrendUp:       2,
}

// Message is one notification ready to send. Body is Markdown.
type Message struct {
	Kind     string
	Title    string
	Body     string
	Priority int
	// ClickURL is opened when the notification is clicked (optional).
	ClickURL string
}

// Notifier delivers a message. Errors may implement Retryable() bool
// (see IsRetryable).
type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// TestMessage is the message sent by POST /api/notifications/test.
func TestMessage(publicURL string) Message {
	return Message{
		Kind:     "test",
		Title:    "PriceRadar: Testbenachrichtigung",
		Body:     "Wenn du diese Nachricht siehst, funktioniert die **Gotify**-Anbindung von PriceRadar.",
		Priority: 5,
		ClickURL: publicURL,
	}
}
