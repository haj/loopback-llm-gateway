//go:build windows

// Windows stub for the audit-log export syslog destination: the stdlib
// log/syslog package does not exist on Windows, so the constructor fails
// cleanly and the settings handler rejects export_type=syslog up front. See
// auditsyslog.go for the real implementation.
package handlers

import (
	"context"
	"fmt"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// syslogDestination is unavailable on Windows; the type exists only so shared
// code compiles.
type syslogDestination struct{}

// newSyslogDestination always fails on Windows.
func newSyslogDestination(network, address, tag string) (*syslogDestination, error) {
	return nil, fmt.Errorf("syslog export is not supported on windows")
}

func (d *syslogDestination) Name() string { return "syslog" }

func (d *syslogDestination) Write(_ context.Context, _ []configstoreTables.TableAuditLog) error {
	return fmt.Errorf("syslog export is not supported on windows")
}

func (d *syslogDestination) Close() error { return nil }

// syslogExportSupported reports whether the syslog export destination is
// available on this platform.
const syslogExportSupported = false
