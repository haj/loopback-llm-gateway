package tables

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AuditLogSettingsID is the fixed primary key of the singleton settings row.
// Exactly one row may exist; readers and writers always address this ID.
const AuditLogSettingsID = "audit-log-settings"

// Audit export destination types.
const (
	AuditExportTypeFile   = "file"
	AuditExportTypeSyslog = "syslog"
)

// TableAuditLogSettings is the singleton configuration row for audit-log
// retention and export. It is ADDITIVE and DEFAULT-OFF: when no row exists (or
// every field is zero) the retention worker no-ops and no export sink is
// installed, so the audit trail behaves exactly as before this feature landed.
//
// Retention prunes whole rows only. Because each audit row carries an
// independent per-row HMAC-SHA256 signature over its canonical event (see
// TableAuditLog.CanonicalEvent — there is no hash chain linking rows), deleting
// a subset of rows leaves every surviving row's signature verifiable. The
// retention worker additionally appends a signed "audit_log.prune" marker event
// anchoring what was deleted (count, cutoff, digest over the deleted rows'
// signatures) so the deletion itself stays tamper-evident.
//
// Follows the column conventions of TableCircuitBreakerConfig.
type TableAuditLogSettings struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`

	// RetentionMaxAgeDays deletes audit rows older than this many days.
	// 0 = unlimited (no age-based pruning).
	RetentionMaxAgeDays int `gorm:"not null;default:0" json:"retention_max_age_days"`
	// RetentionMaxRows trims the table to at most this many rows, deleting
	// oldest-first. 0 = unlimited (no row-count trimming).
	RetentionMaxRows int64 `gorm:"not null;default:0" json:"retention_max_rows"`

	// ExportEnabled gates the live-tail export pipeline. When false no export
	// sink is installed regardless of the destination fields below.
	ExportEnabled bool `gorm:"not null;default:false" json:"export_enabled"`
	// ExportType selects the destination: "file" (append-only JSONL) or
	// "syslog". Empty is valid only while ExportEnabled is false.
	ExportType string `gorm:"type:varchar(50)" json:"export_type"`
	// ExportFilePath is the JSONL file path for ExportType "file".
	ExportFilePath string `gorm:"type:varchar(1024)" json:"export_file_path"`
	// SyslogNetwork is "" (local syslog socket), "udp", or "tcp".
	SyslogNetwork string `gorm:"type:varchar(50)" json:"syslog_network"`
	// SyslogAddress is the remote syslog host:port (required when
	// SyslogNetwork is "udp" or "tcp").
	SyslogAddress string `gorm:"type:varchar(255)" json:"syslog_address"`
	// SyslogTag is the syslog tag/program name; defaults to
	// "loopback-gateway-audit" when empty.
	SyslogTag string `gorm:"type:varchar(255)" json:"syslog_tag"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName sets the table name.
func (TableAuditLogSettings) TableName() string { return "governance_audit_log_settings" }

// BeforeSave pins the singleton ID, clamps negative retention values to 0
// (unlimited) and validates the export destination enums so a malformed row can
// never install a nonsensical export sink or a negative retention window.
func (s *TableAuditLogSettings) BeforeSave(tx *gorm.DB) error {
	s.ID = AuditLogSettingsID
	if s.RetentionMaxAgeDays < 0 {
		s.RetentionMaxAgeDays = 0
	}
	if s.RetentionMaxRows < 0 {
		s.RetentionMaxRows = 0
	}
	s.ExportType = strings.TrimSpace(s.ExportType)
	switch s.ExportType {
	case "", AuditExportTypeFile, AuditExportTypeSyslog:
	default:
		return fmt.Errorf("audit log settings export_type must be %q or %q, got %q", AuditExportTypeFile, AuditExportTypeSyslog, s.ExportType)
	}
	s.SyslogNetwork = strings.TrimSpace(s.SyslogNetwork)
	switch s.SyslogNetwork {
	case "", "udp", "tcp":
	default:
		return fmt.Errorf("audit log settings syslog_network must be empty, \"udp\" or \"tcp\", got %q", s.SyslogNetwork)
	}
	return nil
}
