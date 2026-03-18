package webhooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/knadh/listmonk/models"
)

// postalWebhookEnvelope is the outer wrapper sent by Postal (event, payload, uuid).
type postalWebhookEnvelope struct {
	Event     string             `json:"event"`
	Timestamp float64            `json:"timestamp"`
	Payload   *postalBounceNotif  `json:"payload"`
	UUID      string             `json:"uuid"`
}

// See: https://docs.postalserver.io/developer/webhooks
// Unified struct for both webhook types: MessageBounced (original_message+bounce) and
// delivery status (message+status e.g. HardFail/SoftFail).
type postalBounceNotif struct {
	// Bounce format: incoming bounce report (DSN)
	OriginalMessage *postalMessage `json:"original_message"`
	Bounce          *postalMessage `json:"bounce"`
	// Delivery failure format: SMTP delivery result
	Message   *postalMessage `json:"message"`
	Status    string         `json:"status"` // e.g. "HardFail", "SoftFail"
	Details   string         `json:"details"`
	Output    string         `json:"output"`
	Timestamp float64        `json:"timestamp"`
}

type postalMessage struct {
	ID         int     `json:"id"`
	Token      string  `json:"token"`
	Direction  string  `json:"direction"`
	MessageID  string  `json:"message_id"`
	To         string  `json:"to"`
	From       string  `json:"from"`
	Subject    string  `json:"subject"`
	Timestamp  float64 `json:"timestamp"`
	SpamStatus string  `json:"spam_status"`
	Tag        *string `json:"tag"` // can be null
}

var reUUID = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

// Postal handles webhook notifications for Postal (MessageBounced events).
type Postal struct{}

// NewPostal returns a new Postal webhook handler.
func NewPostal() *Postal {
	return &Postal{}
}

// ProcessBounce processes a Postal webhook and returns a Bounce.
// Supports two payload types, both treated as bounces:
//   - Bounce: original_message + bounce (incoming DSN report)
//   - Delivery failure: message + status (HardFail/SoftFail from SMTP result)
func (p *Postal) ProcessBounce(body []byte) ([]models.Bounce, error) {
	var env postalWebhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("error unmarshalling postal webhook: %v", err)
	}
	// Webhook data is inside payload (wrapped envelope).
	if env.Payload == nil {
		return nil, errors.New("postal webhook: missing payload")
	}
	n := env.Payload

	var msg *postalMessage
	var typ string
	var ts float64

	if n.OriginalMessage != nil && n.Bounce != nil {
		// Bounce format: recipient is in original_message
		msg = n.OriginalMessage
		typ = models.BounceTypeHard
		if msg.Timestamp > 0 {
			ts = msg.Timestamp
		} else if n.Bounce.Timestamp > 0 {
			ts = n.Bounce.Timestamp
		}
	} else if n.Message != nil {
		// Delivery failure format
		msg = n.Message
		typ = models.BounceTypeHard
		if strings.EqualFold(n.Status, "SoftFail") {
			typ = models.BounceTypeSoft
		}
		if n.Timestamp > 0 {
			ts = n.Timestamp
		} else if msg.Timestamp > 0 {
			ts = msg.Timestamp
		}
	}

	if msg == nil || msg.To == "" {
		return nil, errors.New("postal webhook: unrecognized payload or missing message/to (need original_message+bounce or message+status)")
	}

	var campUUID string
	if msg.Tag != nil {
		campUUID = strings.TrimSpace(*msg.Tag)
	}
	if campUUID != "" && !reUUID.MatchString(campUUID) {
		campUUID = ""
	}

	createdAt := time.Now()
	if ts > 0 {
		sec := int64(ts)
		nsec := int64((ts - float64(sec)) * 1e9)
		createdAt = time.Unix(sec, nsec)
	}

	return []models.Bounce{{
		Email:        strings.ToLower(msg.To),
		CampaignUUID: campUUID,
		Type:         typ,
		Source:       "postal",
		Meta:         json.RawMessage(body),
		CreatedAt:    createdAt,
	}}, nil
}
