package lib

import (
	"context"

	"github.com/maximhq/bifrost/framework/sso"
)

// loadSSOConfig wires the optional SSO/SCIM (scim_config) section onto the
// runtime Config. It is DEFAULT-OFF and FAIL-SAFE: a nil/disabled section, an
// unimplemented provider (Okta/Entra), or any misconfiguration leaves
// config.SSOValidator nil. The auth middleware skips the JWT branch when the
// validator is nil, so a bad SSO config can never lock out password/session
// auth or prevent startup.
func loadSSOConfig(ctx context.Context, config *Config, configData *ConfigData) {
	if config == nil || configData == nil || configData.SCIMConfig == nil {
		return
	}
	scim := configData.SCIMConfig
	config.SCIMConfig = scim

	if !scim.Enabled {
		logger.Debug("[sso] scim_config present but disabled; SSO/JWT auth is OFF")
		return
	}
	if err := scim.Validate(); err != nil {
		// Do NOT fail startup — log and leave SSO disabled so password auth
		// keeps working and the operator can fix the config.
		logger.Warn("[sso] scim_config is enabled but invalid; SSO/JWT auth stays OFF: %v", err)
		return
	}
	validator, err := sso.NewValidator(scim, nil)
	if err != nil {
		logger.Warn("[sso] failed to build IdP JWT validator; SSO/JWT auth stays OFF: %v", err)
		return
	}
	if validator == nil {
		return
	}
	config.SSOValidator = validator
	logger.Info("[sso] SSO/JWT auth ENABLED (provider=%s). Password auth remains available.", scim.Provider)
}
