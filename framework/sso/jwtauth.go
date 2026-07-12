// This file contains the generic, issuer-based JWT validator behind the
// "external-IdP JWTs on the LLM data plane" feature:
// callers present their IdP's JWT as the Bearer token, the HTTP transport
// validates it against the issuer's JWKS and maps its claims to a governance
// virtual key, so budgets / rate limits / team attribution apply without
// distributing sk-bf-* keys.
//
// It deliberately lives in package sso to reuse the unexported jwksCache
// (15-minute TTL, refetch-on-unknown-kid, outage fallback) and the
// getClaimPath / stringsClaim claim helpers shared with the Keycloak SSO
// validator. Unlike that validator, this one is IdP-agnostic: any issuer with
// a JWKS endpoint and RS256/384/512 signing works.
package sso

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTClaimVKMapping is one claim→virtual-key mapping rule. Rules are evaluated
// in order; the first match wins.
type JWTClaimVKMapping struct {
	// Claim is the dot-path into the verified claim set (e.g. "tenant" or
	// "resource_access.gateway.roles").
	Claim string `json:"claim"`
	// Value is the exact value to match. When the claim resolves to a string
	// array, any element matching counts. "*" matches any present, non-empty
	// value.
	Value string `json:"value"`
	// VirtualKeyID is the governance virtual key (by ID) the request is
	// attributed to when this rule matches.
	VirtualKeyID string `json:"virtual_key_id"`
}

// JWTAuthConfig describes one trusted external issuer.
type JWTAuthConfig struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	// Issuer is the exact expected `iss` claim value.
	Issuer string `json:"issuer"`
	// JWKSURL is the issuer's key-set endpoint.
	JWKSURL string `json:"jwks_url"`
	// Audience, when set, is enforced against the `aud` claim.
	Audience string `json:"audience"`
	// ClaimMappings map verified claims to virtual key IDs, first match wins.
	ClaimMappings []JWTClaimVKMapping `json:"claim_mappings"`
	// DefaultVirtualKeyID is the fallback attribution when no rule matches
	// (empty = no fallback; the request proceeds without a VK).
	DefaultVirtualKeyID string `json:"default_virtual_key_id"`
	// RejectInvalid makes the transport return 401 for tokens that look like
	// JWTs from this issuer but fail verification, instead of the default
	// fall-through (where governance still rejects if a VK is mandatory).
	RejectInvalid bool `json:"reject_invalid"`
	// JWKSCacheTTLSeconds overrides the 15-minute JWKS cache TTL (0 = default).
	JWKSCacheTTLSeconds int `json:"jwks_cache_ttl_seconds"`
}

// JWTAuthValidator verifies tokens for a single external issuer and maps their
// claims to a virtual key ID.
type JWTAuthValidator struct {
	cfg  JWTAuthConfig
	jwks *jwksCache
}

// NewJWTAuthValidator builds a validator for one issuer config. Returns
// (nil, nil) for a disabled config — the caller simply skips it — and an error
// for an enabled config that is unusable.
func NewJWTAuthValidator(cfg JWTAuthConfig, httpClient *http.Client) (*JWTAuthValidator, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	cfg.JWKSURL = strings.TrimSpace(cfg.JWKSURL)
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("sso: jwt auth config requires an issuer")
	}
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("sso: jwt auth config for issuer %q requires a jwks_url", cfg.Issuer)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	cache := newJWKSCache(cfg.JWKSURL, httpClient, time.Now)
	if cfg.JWKSCacheTTLSeconds > 0 {
		cache.ttl = time.Duration(cfg.JWKSCacheTTLSeconds) * time.Second
	}
	return &JWTAuthValidator{cfg: cfg, jwks: cache}, nil
}

// Issuer returns the exact `iss` value this validator serves.
func (v *JWTAuthValidator) Issuer() string { return v.cfg.Issuer }

// RejectInvalid reports whether failed verification should 401 instead of
// falling through.
func (v *JWTAuthValidator) RejectInvalid() bool { return v.cfg.RejectInvalid }

// Validate parses and cryptographically verifies rawToken (signature via the
// issuer's JWKS, issuer, required expiry, optional audience) and returns the
// verified claim set. Any failure returns an error and no claims, so callers
// can safely fall through to other auth paths.
func (v *JWTAuthValidator) Validate(ctx context.Context, rawToken string) (jwt.MapClaims, error) {
	if v == nil {
		return nil, fmt.Errorf("sso: jwt auth validator not configured")
	}
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, fmt.Errorf("sso: empty token")
	}

	keyFunc := func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("sso: token missing kid header")
		}
		return v.jwks.get(ctx, kid)
	}

	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithExpirationRequired(),
	}
	if v.cfg.Audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(v.cfg.Audience))
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(rawToken, claims, keyFunc, parserOpts...)
	if err != nil {
		return nil, fmt.Errorf("sso: jwt auth validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("sso: token is not valid")
	}
	return claims, nil
}

// MapToVirtualKeyID resolves the verified claims to a virtual key ID via the
// mapping rules (first match wins), falling back to DefaultVirtualKeyID.
// Returns "" when nothing matches and no default is configured.
func (v *JWTAuthValidator) MapToVirtualKeyID(claims jwt.MapClaims) string {
	raw := map[string]any(claims)
	for _, rule := range v.cfg.ClaimMappings {
		if rule.VirtualKeyID == "" {
			continue
		}
		val := getClaimPath(raw, rule.Claim)
		if val == nil {
			continue
		}
		if rule.Value == "*" {
			// Presence match: any non-empty scalar or non-empty array.
			if stringClaim(val) != "" || len(stringsClaim(val)) > 0 {
				return rule.VirtualKeyID
			}
			continue
		}
		if stringClaim(val) == rule.Value {
			return rule.VirtualKeyID
		}
		for _, s := range stringsClaim(val) {
			if s == rule.Value {
				return rule.VirtualKeyID
			}
		}
	}
	return v.cfg.DefaultVirtualKeyID
}
