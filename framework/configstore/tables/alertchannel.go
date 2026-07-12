package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// Alert channel destination types.
const (
	AlertChannelTypeSlack     = "slack"
	AlertChannelTypePagerDuty = "pagerduty"
	AlertChannelTypeWebhook   = "webhook"
)

// TableAlertChannel is one admin-configured alert destination for the
// framework/alerting dispatcher. ADDITIVE and DEFAULT-OFF: with zero rows the
// alerting pipeline is a no-op and the request path is unchanged.
//
// Secret holds the channel's sensitive credential (PagerDuty routing key,
// webhook HMAC signing key) and is encrypted at rest following the
// TableVirtualKey pattern (BeforeSave encrypts, AfterFind decrypts, gated on
// EncryptionStatus). It is never serialized to API responses.
type TableAlertChannel struct {
	ID   string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name string `gorm:"type:varchar(255);not null" json:"name"`
	// Type is the destination kind: "slack", "pagerduty", or "webhook".
	Type string `gorm:"type:varchar(50);not null" json:"type"`
	// Enabled toggles the channel without deleting its configuration.
	Enabled bool `gorm:"not null;default:true" json:"enabled"`

	// EndpointURL is the destination URL: the Slack incoming-webhook URL, the
	// generic webhook URL, or an optional PagerDuty Events API override (empty
	// means the public PagerDuty endpoint).
	EndpointURL string `gorm:"type:text" json:"endpoint_url"`
	// Secret is the channel credential (encrypted at rest, redacted from API
	// responses): PagerDuty routing key or webhook signing key.
	Secret           string `gorm:"type:text" json:"-"`
	EncryptionStatus string `gorm:"type:varchar(20);default:'plain_text'" json:"-"`

	// EventTypesJSON is the serialized []string filter. The runtime EventTypes
	// field is the source of truth on write (BeforeSave serializes it) and is
	// repopulated on read (AfterFind deserializes it). Empty means all events.
	EventTypesJSON string `gorm:"type:text" json:"-"`

	// Last delivery attempt bookkeeping, written best-effort by the dispatcher
	// so the UI can surface channel health without a per-delivery table.
	LastAttemptAt *time.Time `gorm:"index" json:"last_attempt_at,omitempty"`
	LastStatus    string     `gorm:"type:varchar(50)" json:"last_status,omitempty"`
	LastError     string     `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// EventTypes is the runtime, non-persisted parsed filter list.
	EventTypes []string `gorm:"-" json:"event_types"`
}

// TableName sets the table name.
func (TableAlertChannel) TableName() string { return "alert_channels" }

// BeforeSave validates the destination type, serializes the event-type filter,
// and encrypts the secret at rest.
func (c *TableAlertChannel) BeforeSave(tx *gorm.DB) error {
	c.Type = strings.TrimSpace(c.Type)
	switch c.Type {
	case AlertChannelTypeSlack, AlertChannelTypePagerDuty, AlertChannelTypeWebhook:
	default:
		return fmt.Errorf("alert channel type must be %q, %q or %q, got %q",
			AlertChannelTypeSlack, AlertChannelTypePagerDuty, AlertChannelTypeWebhook, c.Type)
	}

	if len(c.EventTypes) == 0 {
		c.EventTypesJSON = ""
	} else {
		raw, err := json.Marshal(c.EventTypes)
		if err != nil {
			return fmt.Errorf("failed to serialize alert channel event types: %w", err)
		}
		c.EventTypesJSON = string(raw)
	}

	if encrypt.IsEnabled() && c.Secret != "" {
		if err := encryptString(&c.Secret); err != nil {
			return fmt.Errorf("failed to encrypt alert channel secret: %w", err)
		}
		c.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

// AfterFind decrypts the secret and deserializes the event-type filter.
func (c *TableAlertChannel) AfterFind(tx *gorm.DB) error {
	if c.EncryptionStatus == EncryptionStatusEncrypted {
		if err := decryptString(&c.Secret); err != nil {
			return fmt.Errorf("failed to decrypt alert channel secret: %w", err)
		}
	}
	if c.EventTypesJSON != "" {
		if err := json.Unmarshal([]byte(c.EventTypesJSON), &c.EventTypes); err != nil {
			return fmt.Errorf("failed to parse alert channel event types: %w", err)
		}
	}
	return nil
}

// WantsEvent reports whether the channel's filter admits the event type. An
// empty filter admits everything.
func (c *TableAlertChannel) WantsEvent(eventType string) bool {
	if len(c.EventTypes) == 0 {
		return true
	}
	for _, t := range c.EventTypes {
		if t == eventType {
			return true
		}
	}
	return false
}
