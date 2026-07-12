package tables

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Audit log outcome values. Outcome records whether the audited action
// succeeded or failed, so a reviewer can distinguish attempted from effective
// changes.
const (
	AuditOutcomeSuccess = "success"
	AuditOutcomeFailure = "failure"
)

// TableAuditLog is an append-only record of a governance mutation (virtual key,
// team, customer, user, guardrail, or PII rule create/update/delete). Rows are
// never updated or deleted through the ConfigStore API; the table is a
// tamper-evident trail.
//
// Each row carries an HMAC-SHA256 Signature computed over its canonical event
// representation (see CanonicalEvent). Because the signing key is held by the
// gateway and not stored in the row, an attacker with only database write access
// cannot forge or silently alter a record without invalidating the signature.
type TableAuditLog struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`

	// Action is the audited operation identifier, e.g. "virtual_key.create" or
	// "guardrail.delete".
	Action string `gorm:"type:varchar(255);not null;index" json:"action"`
	// Outcome is "success" or "failure".
	Outcome string `gorm:"type:varchar(50);not null;index" json:"outcome"`
	// Actor identifies who performed the action (user name, user ID, or
	// "local-admin" for the password-authenticated local admin).
	Actor string `gorm:"type:varchar(255);index" json:"actor"`
	// IP is the originating client IP (forwarded header when present, otherwise
	// the TCP peer).
	IP string `gorm:"type:varchar(255)" json:"ip"`
	// Target identifies the entity the action operated on (typically its ID).
	Target string `gorm:"type:varchar(255);index" json:"target"`
	// Timestamp is when the action occurred.
	Timestamp time.Time `gorm:"index;not null" json:"timestamp"`

	// Signature is the hex-encoded HMAC-SHA256 over CanonicalEvent, providing
	// tamper-evidence for the row.
	Signature string `gorm:"type:varchar(255)" json:"signature"`
}

// TableName sets the table name.
func (TableAuditLog) TableName() string { return "governance_audit_logs" }

// CanonicalEvent returns the stable string representation of the audited event
// that the Signature is computed over. The field order and separators are fixed
// so the signature is reproducible across processes; Signature itself is
// excluded. Timestamp is rendered in UTC RFC3339Nano for byte-stable output.
func (a *TableAuditLog) CanonicalEvent() string {
	return fmt.Sprintf(
		"id=%s\naction=%s\noutcome=%s\nactor=%s\nip=%s\ntarget=%s\ntimestamp=%s",
		a.ID,
		a.Action,
		a.Outcome,
		a.Actor,
		a.IP,
		a.Target,
		a.Timestamp.UTC().Format(time.RFC3339Nano),
	)
}

// ComputeSignature returns the hex-encoded HMAC-SHA256 of CanonicalEvent using
// the supplied key.
func (a *TableAuditLog) ComputeSignature(key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(a.CanonicalEvent()))
	return hex.EncodeToString(mac.Sum(nil))
}

// Sign computes and stores the row's Signature using the supplied key.
func (a *TableAuditLog) Sign(key []byte) {
	a.Signature = a.ComputeSignature(key)
}

// VerifySignature reports whether the stored Signature matches a freshly
// computed HMAC over the row's canonical event. Uses a constant-time compare to
// avoid leaking timing information.
func (a *TableAuditLog) VerifySignature(key []byte) bool {
	expected := a.ComputeSignature(key)
	return hmac.Equal([]byte(expected), []byte(a.Signature))
}
