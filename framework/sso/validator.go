package sso

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is the trusted result of validating an IdP-issued JWT. It is the
// minimal projection the auth middleware and provisioning logic need; the full
// claim set is retained in Raw for mapping rules.
type Identity struct {
	Provider    string
	Subject     string
	Email       string
	UserName    string
	DisplayName string
	Groups      []string
	Roles       []string
	Raw         map[string]any
}

// jwksCache fetches and caches a provider's JSON Web Key Set, mapping key id
// (kid) to the parsed RSA public key. It refetches on an unknown kid (key
// rotation) and after a TTL. Safe for concurrent use.
type jwksCache struct {
	url        string
	httpClient *http.Client
	ttl        time.Duration

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	now       func() time.Time
}

func newJWKSCache(url string, httpClient *http.Client, now func() time.Time) *jwksCache {
	return &jwksCache{
		url:        url,
		httpClient: httpClient,
		ttl:        15 * time.Minute,
		keys:       map[string]*rsa.PublicKey{},
		now:        now,
	}
}

// jwk is a single JSON Web Key (RSA only — Keycloak's default signing keys).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

func (c *jwksCache) get(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	fresh := c.now().Sub(c.fetchedAt) < c.ttl
	c.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}
	if err := c.refresh(ctx); err != nil {
		// On refresh failure, fall back to a cached key if we have one rather
		// than rejecting every request during a transient JWKS outage.
		if ok {
			return key, nil
		}
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	key, ok = c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("sso: signing key %q not found in JWKS", kid)
	}
	return key, nil
}

func (c *jwksCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sso: failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sso: JWKS endpoint returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("sso: failed to parse JWKS: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if !strings.EqualFold(k.Kty, "RSA") {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("sso: JWKS contained no usable RSA keys")
	}
	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = c.now()
	c.mu.Unlock()
	return nil
}

// rsaPublicKeyFromJWK builds an *rsa.PublicKey from a JWK's base64url modulus
// (n) and exponent (e).
func rsaPublicKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	// e is a big-endian byte slice; left-pad to 8 bytes for binary decoding.
	var eInt uint64
	if len(eBytes) > 8 {
		return nil, fmt.Errorf("sso: JWK exponent too large")
	}
	padded := make([]byte, 8)
	copy(padded[8-len(eBytes):], eBytes)
	eInt = binary.BigEndian.Uint64(padded)
	if eInt == 0 {
		return nil, fmt.Errorf("sso: JWK exponent is zero")
	}
	return &rsa.PublicKey{N: n, E: int(eInt)}, nil
}

// Validator validates IdP-issued JWTs against the provider JWKS and maps the
// trusted claims onto an Identity. It enforces: RS256 signature against a JWKS
// key, the configured issuer, token expiry, and (when configured) audience.
// Provider-agnostic: any OIDCProvider (Keycloak, Okta, Entra) plugs in.
type Validator struct {
	oidc OIDCProvider
	jwks *jwksCache
	now  func() time.Time
}

// NewValidator builds a JWT validator for the configured provider. Returns
// (nil, nil) for a nil/disabled config (default-OFF). httpClient may be nil
// (a default is used).
func NewValidator(scim *SCIMConfig, httpClient *http.Client) (*Validator, error) {
	p, err := NewProvider(scim)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil // default-OFF: no validator
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	now := time.Now
	return &Validator{
		oidc: p,
		jwks: newJWKSCache(p.JWKSURL(), httpClient, now),
		now:  now,
	}, nil
}

// Provider returns the provider identifier this validator serves.
func (v *Validator) Provider() string { return v.oidc.Name() }

// Mappings returns the provider's attribute→role/team/BU rules (applied by
// ProvisionFromIdentity's callers).
func (v *Validator) Mappings() AttributeMappings { return v.oidc.Mappings() }

// Validate parses and cryptographically verifies rawToken, then projects its
// trusted claims into an Identity. Any failure (bad signature, wrong issuer,
// expired, unknown key, wrong audience) returns an error and NO identity, so a
// caller can safely fall through to other auth paths.
func (v *Validator) Validate(ctx context.Context, rawToken string) (*Identity, error) {
	if v == nil {
		return nil, fmt.Errorf("sso: validator not configured")
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
		jwt.WithIssuer(v.oidc.IssuerURL()),
		jwt.WithExpirationRequired(),
	}
	if aud := v.oidc.ExpectedAudience(); aud != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(aud))
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(rawToken, claims, keyFunc, parserOpts...)
	if err != nil {
		return nil, fmt.Errorf("sso: token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("sso: token is not valid")
	}

	return v.identityFromClaims(claims), nil
}

// identityFromClaims maps verified claims into an Identity using the configured
// field names (with dot-path support, e.g. "realm_access.roles").
func (v *Validator) identityFromClaims(claims jwt.MapClaims) *Identity {
	raw := map[string]any(claims)
	fields := v.oidc.ClaimFields()
	id := &Identity{
		Provider:    v.oidc.Name(),
		Subject:     stringClaim(getClaimPath(raw, fields.UserID)),
		Email:       stringClaim(raw["email"]),
		UserName:    stringClaim(raw["preferred_username"]),
		DisplayName: stringClaim(raw["name"]),
		Groups:      stringsClaim(getClaimPath(raw, fields.TeamIDs)),
		Roles:       stringsClaim(getClaimPath(raw, fields.Roles)),
		Raw:         raw,
	}
	if id.Subject == "" {
		id.Subject = stringClaim(raw["sub"])
	}
	if id.UserName == "" {
		id.UserName = id.Email
	}
	if id.DisplayName == "" {
		id.DisplayName = id.UserName
	}
	return id
}

// getClaimPath resolves a possibly dotted path (e.g. "realm_access.roles")
// against a nested claim map. Returns nil when any segment is missing.
func getClaimPath(claims map[string]any, path string) any {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var cur any = claims
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[p]
		if !ok {
			return nil
		}
	}
	return cur
}

func stringClaim(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// stringsClaim coerces a claim into a []string, accepting either a JSON array of
// strings or a single string value.
func stringsClaim(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{strings.TrimSpace(t)}
	default:
		return nil
	}
}
