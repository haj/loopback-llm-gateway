// This file contains the data-plane JWT→virtual-key auth middleware: callers
// present their external IdP's JWT as the Bearer token,
// the middleware verifies it against the configured issuer's JWKS, maps its
// claims to a governance virtual key, and injects the VK's plaintext value as
// the x-bf-vk request header — so the existing header extraction
// (lib/ctx.go) and the governance plugin (budgets, rate limits, team
// attribution via VK.TeamID) run completely unchanged.
//
// SAFETY (this sits on the inference hot path):
//   - DEFAULT-OFF. The config snapshot is a nil atomic pointer until an
//     enabled issuer row exists; while nil the middleware is one atomic load
//     and the request is byte-for-byte unchanged.
//   - Non-JWT traffic pays only cheap string checks: requests with an
//     existing x-bf-vk header, sk-bf-* bearer keys, and non-3-segment tokens
//     all skip before any parsing.
//   - Fall-through by default: a token that fails verification proceeds
//     without a VK (governance still rejects it if a VK is mandatory). The
//     per-issuer reject_invalid flag opts into a hard 401 instead.
//   - A caller-supplied x-bf-vk header ALWAYS wins; the middleware never
//     overwrites it.
package handlers

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/sso"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

// jwtVKValueTTL bounds how long a resolved VK plaintext value is cached. Kept
// short so VK rotation propagates quickly; the value never leaves process
// memory (never logged, never serialized).
const jwtVKValueTTL = 60 * time.Second

// vkResolver is the narrow configstore surface the middleware needs.
// configstore.ConfigStore satisfies it; tests substitute a fake.
type vkResolver interface {
	GetVirtualKey(ctx context.Context, id string) (*configstoreTables.TableVirtualKey, error)
}

// vkCacheEntry is one cached VK plaintext value.
type vkCacheEntry struct {
	value  string
	expiry time.Time
}

// JWTVKAuthMiddleware validates external-IdP JWTs on inference routes and
// injects the mapped virtual key. Construct once, call SetConfigs whenever the
// issuer rows change, and append Middleware() to the inference chain.
type JWTVKAuthMiddleware struct {
	resolver vkResolver

	// snapshot maps issuer → validator. nil = feature off.
	snapshot atomic.Pointer[map[string]*sso.JWTAuthValidator]

	cacheMu sync.Mutex
	vkCache map[string]vkCacheEntry
	now     func() time.Time
}

// NewJWTVKAuthMiddleware creates a middleware with an empty (off) snapshot.
func NewJWTVKAuthMiddleware(resolver vkResolver) *JWTVKAuthMiddleware {
	return &JWTVKAuthMiddleware{
		resolver: resolver,
		vkCache:  map[string]vkCacheEntry{},
		now:      time.Now,
	}
}

// SetConfigs rebuilds the issuer snapshot from the given rows. Disabled rows
// are skipped; a row that fails to compile is logged and skipped so one bad
// issuer never disables the others. Zero usable rows store a nil snapshot
// (the default-off state). The VK value cache is cleared so mapping changes
// take effect immediately.
func (m *JWTVKAuthMiddleware) SetConfigs(configs []configstoreTables.TableJWTAuthConfig) {
	byIssuer := map[string]*sso.JWTAuthValidator{}
	for i := range configs {
		row := &configs[i]
		if !row.Enabled {
			continue
		}
		mappings := make([]sso.JWTClaimVKMapping, 0, len(row.ClaimMappings))
		for _, cm := range row.ClaimMappings {
			mappings = append(mappings, sso.JWTClaimVKMapping{Claim: cm.Claim, Value: cm.Value, VirtualKeyID: cm.VirtualKeyID})
		}
		validator, err := sso.NewJWTAuthValidator(sso.JWTAuthConfig{
			Enabled:             true,
			Name:                row.Name,
			Issuer:              row.Issuer,
			JWKSURL:             row.JWKSURL,
			Audience:            row.Audience,
			ClaimMappings:       mappings,
			DefaultVirtualKeyID: row.DefaultVirtualKeyID,
			RejectInvalid:       row.RejectInvalid,
		}, nil)
		if err != nil {
			if logger != nil {
				logger.Warn("skipping jwt auth config %s (issuer %q): %v", row.ID, row.Issuer, err)
			}
			continue
		}
		if validator != nil {
			byIssuer[validator.Issuer()] = validator
		}
	}

	if len(byIssuer) == 0 {
		m.snapshot.Store(nil)
	} else {
		m.snapshot.Store(&byIssuer)
	}

	m.cacheMu.Lock()
	m.vkCache = map[string]vkCacheEntry{}
	m.cacheMu.Unlock()
}

// resolveVKValue returns the plaintext value for a VK ID via the 60s TTL
// cache. Returns "" when the VK is missing or inactive.
func (m *JWTVKAuthMiddleware) resolveVKValue(ctx context.Context, vkID string) string {
	now := m.now()
	m.cacheMu.Lock()
	if entry, ok := m.vkCache[vkID]; ok && now.Before(entry.expiry) {
		m.cacheMu.Unlock()
		return entry.value
	}
	m.cacheMu.Unlock()

	if m.resolver == nil {
		return ""
	}
	vk, err := m.resolver.GetVirtualKey(ctx, vkID)
	if err != nil || vk == nil {
		if logger != nil {
			logger.Warn("jwt auth: failed to resolve virtual key %s: %v", vkID, err)
		}
		return ""
	}
	if !vk.IsActiveValue() {
		return ""
	}

	m.cacheMu.Lock()
	m.vkCache[vkID] = vkCacheEntry{value: vk.Value, expiry: now.Add(jwtVKValueTTL)}
	m.cacheMu.Unlock()
	return vk.Value
}

// Middleware returns the fasthttp middleware. See the file header for the
// skip/fall-through contract.
func (m *JWTVKAuthMiddleware) Middleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			snap := m.snapshot.Load()
			if snap == nil {
				next(ctx)
				return
			}
			// A caller-supplied VK always wins.
			if len(ctx.Request.Header.Peek(string(schemas.BifrostContextKeyVirtualKey))) > 0 {
				next(ctx)
				return
			}
			auth := string(ctx.Request.Header.Peek("Authorization"))
			if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				next(ctx)
				return
			}
			token := strings.TrimSpace(auth[7:])
			// sk-bf-* bearer tokens are virtual keys already (lib/ctx.go
			// extracts them); session/opaque tokens are not JWTs.
			if strings.HasPrefix(strings.ToLower(token), "sk-bf-") || strings.Count(token, ".") != 2 {
				next(ctx)
				return
			}

			// Select the validator by the (unverified) iss claim, then fully
			// verify. The unverified parse only routes — trust comes from
			// Validate.
			unverified := jwt.MapClaims{}
			if _, _, err := jwt.NewParser().ParseUnverified(token, unverified); err != nil {
				next(ctx)
				return
			}
			iss, _ := unverified["iss"].(string)
			validator := (*snap)[iss]
			if validator == nil {
				next(ctx)
				return
			}

			claims, err := validator.Validate(ctx, token)
			if err != nil {
				if validator.RejectInvalid() {
					SendError(ctx, 401, "Invalid JWT")
					return
				}
				next(ctx)
				return
			}

			vkID := validator.MapToVirtualKeyID(claims)
			if vkID == "" {
				next(ctx)
				return
			}
			value := m.resolveVKValue(ctx, vkID)
			if value == "" {
				// Valid token but unresolvable VK: configuration problem, not
				// an auth failure — fall through and let governance decide.
				next(ctx)
				return
			}
			ctx.Request.Header.Set(string(schemas.BifrostContextKeyVirtualKey), value)
			next(ctx)
		}
	}
}
