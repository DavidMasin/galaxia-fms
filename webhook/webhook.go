// Package webhook sends what is published on the event bus to the webhooks an admin has set up: signed HTTP POSTs,
// retried with exponential backoff for 24 hours. Every delivery is written to the database before it is sent, so the
// outbox survives a restart, and the same records are the delivery log the Webhooks page shows.
//
// The Dispatcher never holds up the bus or the arena: it reads events from a subscription of its own, and sending
// happens on worker goroutines.
package webhook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// How long a delivery is retried for, from when it was queued or last resent.
	DefaultRetryFor = 24 * time.Hour
	// How long a receiver has to answer one attempt.
	DefaultTimeout = 10 * time.Second
	// How many succeeded and failed deliveries the log keeps per webhook. Pending ones are always kept.
	DefaultKeepFinished = 500
	// How many deliveries may wait for one webhook. A receiver that is down for a day while it is sent every score
	// change would otherwise fill the database; past this the oldest waiting delivery is given up.
	DefaultMaxPending = 5000

	// The first retry waits this long, and every later one twice as long as the one before, up to maxRetryDelay.
	firstRetryDelay = 10 * time.Second
	maxRetryDelay   = time.Hour
	// Each delay is stretched or shrunk by up to this fraction, so that deliveries that failed together (a
	// receiver that was down) do not all come back at the same instant.
	retryJitter = 0.2

	// How much of a receiver's answer is kept for the log.
	maxResponseBytes = 2048
	// How many attempts a delivery remembers.
	maxHistory = 10

	maxNameLength   = 100
	maxUrlLength    = 2000
	maxFilters      = 50
	minSecretLength = 16
	maxSecretLength = 200
	secretBytes     = 32
)

// Event types of deliveries that are not bus events. They go to a webhook whatever its filters.
const (
	// A test delivery, sent from the Webhooks page or the API.
	TestEventType = "webhook.test"
	// Events were lost before they could be queued: the receiver should reload what it keeps from the API.
	GapEventType = "webhook.gap"
)

// EveryEvent, as a webhook's only filter, selects every event.
const EveryEvent = "*"

// ErrNotFound is returned for a webhook or delivery that does not exist.
var ErrNotFound = errors.New("not found")

// InvalidError is returned when what an admin asked for cannot be done as asked.
type InvalidError struct {
	message string
}

func (err *InvalidError) Error() string {
	return err.message
}

func invalid(format string, args ...any) error {
	return &InvalidError{message: fmt.Sprintf(format, args...)}
}

// EventType is one kind of event a webhook can subscribe to, for the Webhooks page.
type EventType struct {
	Type        string `json:"type"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Default     bool   `json:"default,omitempty"`  // Chosen for a new webhook.
	HighRate    bool   `json:"highRate,omitempty"` // Can come many times a second.
	Planned     bool   `json:"planned,omitempty"`  // Not published yet; a subscription receives it once it is.
}

// EventTypes lists the events a webhook can subscribe to. Any other type or prefix can be typed in as well.
var EventTypes = []EventType{
	{Type: "match.loaded", Group: "Match", Description: "A match was loaded onto the field, with its teams.",
		Default: true},
	{Type: "match.started", Group: "Match", Description: "The match started.", Default: true},
	{Type: "match.phase_changed", Group: "Match", Description: "A new phase of the match began.", Default: true},
	{Type: "match.cue", Group: "Match", Description: "A timed cue of the game fired."},
	{Type: "match.ended", Group: "Match", Description: "The match ran to its end.", Default: true},
	{Type: "match.aborted", Group: "Match", Description: "The match was stopped early.", Default: true},
	{Type: "match.committed", Group: "Match", Description: "The score was committed: the official result.",
		Default: true},
	{Type: "match.result_edited", Group: "Match", Description: "A committed result was edited.", Default: true},
	{Type: "score.changed", Group: "Score",
		Description: "One change to the live score: every point, foul and correction.", HighRate: true},
	{Type: "score.undone", Group: "Score", Description: "A score change was undone."},
	{Type: "station.status", Group: "Field",
		Description: "A driver station, robot, radio, stop or bypass changed at a station."},
	{Type: "field.reset", Group: "Field", Description: "The field was signalled safe for field reset."},
	{Type: "field.volunteers", Group: "Field", Description: "The field was signalled safe for volunteers."},
	{Type: "schedule.changed", Group: "Schedule and alliances", Description: "The match schedule changed.",
		Planned: true},
	{Type: "alliance_selection.updated", Group: "Schedule and alliances",
		Description: "A pick was made in alliance selection.", Planned: true},
	{Type: "alliance_selection.finalized", Group: "Schedule and alliances",
		Description: "The playoff alliances were finalized.", Planned: true},
	{Type: "rankings.updated", Group: "Schedule and alliances", Description: "The rankings were recalculated.",
		Planned: true},
	{Type: "device.online", Group: "Devices", Description: "A field device connected."},
	{Type: "device.offline", Group: "Devices", Description: "A field device went away."},
	{Type: "device.pending", Group: "Devices", Description: "A new device is waiting for approval."},
	{Type: "device.manifest", Group: "Devices", Description: "A device described its channels."},
	{Type: "device.fault", Group: "Devices", Description: "A safety binding faulted."},
	{Type: "device.fault_cleared", Group: "Devices", Description: "A device fault was cleared."},
	{Type: "device.refused", Group: "Devices", Description: "A binding refused a device's input."},
	{Type: "device.input", Group: "Devices", Description: "A device input changed.", HighRate: true},
	{Type: "device.output", Group: "Devices", Description: "An output command was sent to a device.",
		HighRate: true},
	{Type: "system.game_changed", Group: "System", Description: "A different game was selected."},
	{Type: "system.game_reloaded", Group: "System", Description: "A game definition file was reloaded."},
}

// DefaultEvents returns the filters a new webhook starts with: the match from loading to its committed result.
func DefaultEvents() []string {
	var events []string
	for _, eventType := range EventTypes {
		if eventType.Default {
			events = append(events, eventType.Type)
		}
	}
	return events
}

// Matches reports whether a webhook's filters select an event type. A filter selects the type it names and every
// type beneath it: "match" selects "match.started", and so does "match.". "*" selects everything.
func Matches(filters []string, eventType string) bool {
	for _, filter := range filters {
		if filter == EveryEvent {
			return true
		}
		filter = strings.TrimSuffix(filter, ".")
		if eventType == filter || strings.HasPrefix(eventType, filter+".") {
			return true
		}
	}
	return false
}

// DefaultBackoff is how long to wait after a delivery's attempt-th failed attempt: 10 seconds, doubling each time
// up to an hour, give or take a fifth. Over 24 hours that is about 30 attempts.
func DefaultBackoff(attempt int) time.Duration {
	delay := maxRetryDelay
	if attempt < 1 {
		attempt = 1
	}
	if attempt <= 20 {
		delay = min(firstRetryDelay<<(attempt-1), maxRetryDelay)
	}
	return time.Duration(float64(delay) * (1 + retryJitter*(2*mathrand.Float64()-1)))
}

// sign computes the X-Galaxia-Signature header: an HMAC-SHA256, keyed with the webhook's secret, of the timestamp,
// a full stop and the body. The timestamp is signed too so that a captured delivery cannot be replayed later.
func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newSecret makes a signing secret: 32 random bytes, written in URL-safe base64. Receivers use the text as it is.
func newSecret() (string, error) {
	data := make([]byte, secretBytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Changes is what an admin sets about a webhook. A field left nil is not changed, or on a new webhook takes its
// default: the URL's host as the name, the default events, enabled, and a new secret.
type Changes struct {
	Name    *string  `json:"name"`
	Url     *string  `json:"url"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
	Secret  *string  `json:"secret"`
}

var filterPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*\.?$`)

// checkUrl returns the URL to send to, or why it cannot be used.
func checkUrl(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", invalid("the URL is missing")
	}
	if len(text) > maxUrlLength {
		return "", invalid("the URL is longer than %d characters", maxUrlLength)
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return "", invalid("the URL is not valid: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", invalid("the URL must start with http:// or https://")
	}
	if parsed.Host == "" {
		return "", invalid("the URL has no host")
	}
	return text, nil
}

// checkEvents returns the filters to keep, or why they cannot be used.
func checkEvents(filters []string) ([]string, error) {
	var events []string
	seen := map[string]bool{}
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" || seen[filter] {
			continue
		}
		if filter == EveryEvent {
			return []string{EveryEvent}, nil
		}
		if !filterPattern.MatchString(filter) {
			return nil, invalid(
				"%q is not an event type or prefix: use lowercase words separated by dots, like match.started",
				filter,
			)
		}
		seen[filter] = true
		events = append(events, filter)
	}
	if len(events) == 0 {
		return nil, invalid("choose at least one event type, or * for every event")
	}
	if len(events) > maxFilters {
		return nil, invalid("a webhook can have at most %d event types", maxFilters)
	}
	return events, nil
}

func checkSecret(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < minSecretLength || len(secret) > maxSecretLength {
		return "", invalid("the secret must be %d to %d characters long", minSecretLength, maxSecretLength)
	}
	if strings.ContainsFunc(secret, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", invalid("the secret cannot contain spaces or control characters")
	}
	return secret, nil
}

func checkName(name, urlText string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		if parsed, err := url.Parse(urlText); err == nil {
			name = parsed.Host
		}
	}
	if len([]rune(name)) > maxNameLength {
		return "", invalid("the name is longer than %d characters", maxNameLength)
	}
	return name, nil
}
