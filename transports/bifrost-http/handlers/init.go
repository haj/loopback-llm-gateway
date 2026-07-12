package handlers

import (
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/alerting"
)

var version string
var logger schemas.Logger

// alertPublisher is the optional alerting bridge (mirrors the logger wiring).
// nil default = no alert events are produced anywhere in this package.
var alertPublisher alerting.Publisher

// SetLogger sets the logger for the application.
func SetLogger(l schemas.Logger) {
	logger = l
}

// SetAlertPublisher installs the alert publisher used by recordAudit's
// audit.mutation events. Called once at server boot, before traffic; may be
// nil (the default-off state).
func SetAlertPublisher(p alerting.Publisher) {
	alertPublisher = p
}

// SetVersion sets the version for the application.
func SetVersion(v string) {
	version = v
}

func GetVersion() string {
	return version
}
