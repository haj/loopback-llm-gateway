//go:build !windows

// Syslog destination for the audit-log export pipeline (see auditexport.go).
// Uses the stdlib log/syslog client behind a tiny writer interface so tests can
// substitute a fake connection (or a loopback net.Listen server) instead of a
// real syslog daemon. Build-tagged: log/syslog does not exist on Windows — see
// auditsyslogwindows.go for the stub.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/syslog"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// defaultAuditSyslogTag is the syslog tag used when the settings row leaves it
// empty.
const defaultAuditSyslogTag = "loopback-gateway-audit"

// syslogConn is the minimal syslog client surface the destination depends on.
// *syslog.Writer satisfies it; tests substitute a fake.
type syslogConn interface {
	Info(m string) error
	Close() error
}

// dialSyslog is swapped in tests; production dials the real syslog client.
var dialSyslog = func(network, address, tag string) (syslogConn, error) {
	return syslog.Dial(network, address, syslog.LOG_INFO|syslog.LOG_DAEMON, tag)
}

// syslogDestination writes each audit event as one JSON-encoded INFO message.
// The signature field rides along, so downstream collectors archive
// independently verifiable records.
type syslogDestination struct {
	conn syslogConn
}

// newSyslogDestination dials the syslog daemon. network is "" (local socket),
// "udp", or "tcp"; address is required for the remote networks.
func newSyslogDestination(network, address, tag string) (*syslogDestination, error) {
	if network != "" && address == "" {
		return nil, fmt.Errorf("syslog address is required for network %q", network)
	}
	if tag == "" {
		tag = defaultAuditSyslogTag
	}
	conn, err := dialSyslog(network, address, tag)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to syslog: %w", err)
	}
	return &syslogDestination{conn: conn}, nil
}

func (d *syslogDestination) Name() string { return "syslog" }

func (d *syslogDestination) Write(_ context.Context, events []configstoreTables.TableAuditLog) error {
	for i := range events {
		line, err := json.Marshal(&events[i])
		if err != nil {
			return fmt.Errorf("failed to marshal audit event %s: %w", events[i].ID, err)
		}
		if err := d.conn.Info(string(line)); err != nil {
			return err
		}
	}
	return nil
}

func (d *syslogDestination) Close() error {
	return d.conn.Close()
}

// syslogExportSupported reports whether the syslog export destination is
// available on this platform. Used by the settings handler to reject
// export_type=syslog on Windows before persisting it.
const syslogExportSupported = true
