// This file contains HTTP handlers for scope-based admin API keys ("lbk_"
// bearer tokens for the Loopback Gateway management API). Admin API keys are
// entirely DISTINCT from governance virtual keys (x-bf-vk, inference-only):
// they authenticate dashboard/management routes and carry (resource, operation)
// scopes that reuse the RBAC permission vocabulary verbatim.
//
// Secret handling: the plaintext key is generated here, returned to the caller
// exactly once (on create and rotate), and only its SHA-256 hash is persisted
// (tables.TableAdminAPIKey.BeforeSave). List/get responses are redacted by the
// table's JSON tags — they carry the display prefix only, never hash or
// plaintext.
//
// It follows the rbac.go handler patterns (RegisterRoutes + per-resource CRUD
// with SendJSON/SendError, transaction-wrapped create with child rows) and
// records an audit event via recordAudit for every mutation.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// Audit action identifiers for admin API key lifecycle mutations. Kept here
// (same package as audit.go's constants) so this slice does not contend on
// audit.go with concurrent work; format matches the "<entity>.<verb>"
// convention.
const (
	AuditActionAPIKeyCreate = "api_key.create"
	AuditActionAPIKeyUpdate = "api_key.update"
	AuditActionAPIKeyRotate = "api_key.rotate"
	AuditActionAPIKeyRevoke = "api_key.revoke"
	AuditActionAPIKeyDelete = "api_key.delete"
)

// adminAPIKeyDisplayPrefixLen is how many characters of the plaintext key are
// retained (and shown in the UI) to help operators identify a key: "lbk_" plus
// a few hex characters. Short enough to be useless as a credential.
const adminAPIKeyDisplayPrefixLen = 10

// APIKeysHandler manages HTTP requests for admin API keys.
type APIKeysHandler struct {
	configStore configstore.ConfigStore
}

// NewAPIKeysHandler creates an admin API keys handler.
func NewAPIKeysHandler(configStore configstore.ConfigStore) (*APIKeysHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &APIKeysHandler{configStore: configStore}, nil
}

// RegisterRoutes wires the admin API key CRUD + rotate/revoke endpoints.
func (h *APIKeysHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/api-keys", lib.ChainMiddlewares(h.getAPIKeys, middlewares...))
	r.POST("/api/governance/api-keys", lib.ChainMiddlewares(h.createAPIKey, middlewares...))
	r.GET("/api/governance/api-keys/{id}", lib.ChainMiddlewares(h.getAPIKey, middlewares...))
	r.PUT("/api/governance/api-keys/{id}", lib.ChainMiddlewares(h.updateAPIKey, middlewares...))
	r.POST("/api/governance/api-keys/{id}/rotate", lib.ChainMiddlewares(h.rotateAPIKey, middlewares...))
	r.POST("/api/governance/api-keys/{id}/revoke", lib.ChainMiddlewares(h.revokeAPIKey, middlewares...))
	r.DELETE("/api/governance/api-keys/{id}", lib.ChainMiddlewares(h.deleteAPIKey, middlewares...))
}

// ---- request payloads ----

type createAPIKeyRequest struct {
	Name      string            `json:"name"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	Scopes    []permissionInput `json:"scopes,omitempty"`
}

type updateAPIKeyRequest struct {
	Name        *string            `json:"name,omitempty"`
	ExpiresAt   *time.Time         `json:"expires_at,omitempty"`
	ClearExpiry bool               `json:"clear_expiry,omitempty"`
	Scopes      *[]permissionInput `json:"scopes,omitempty"`
}

// ---- handlers ----

func (h *APIKeysHandler) getAPIKeys(ctx *fasthttp.RequestCtx) {
	params := configstore.APIKeysQueryParams{
		Search: string(ctx.QueryArgs().Peek("search")),
		Status: string(ctx.QueryArgs().Peek("status")),
	}
	if v := string(ctx.QueryArgs().Peek("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid limit parameter: must be a non-negative number")
			return
		}
		params.Limit = n
	}
	if v := string(ctx.QueryArgs().Peek("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid offset parameter: must be a non-negative number")
			return
		}
		params.Offset = n
	}

	keys, total, err := h.configStore.GetAPIKeys(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve api keys: %v", err)
		SendError(ctx, 500, "Failed to retrieve API keys")
		return
	}
	SendJSON(ctx, map[string]any{
		"api_keys": keys,
		"total":    total,
		"count":    len(keys),
		"offset":   params.Offset,
	})
}

func (h *APIKeysHandler) getAPIKey(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	key, err := h.configStore.GetAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "API key not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve API key")
		return
	}
	SendJSON(ctx, map[string]any{"api_key": key})
}

func (h *APIKeysHandler) createAPIKey(ctx *fasthttp.RequestCtx) {
	var req createAPIKeyRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		SendError(ctx, 400, "expires_at must be in the future")
		return
	}
	scopes, errMsg := normalizeAPIKeyScopes(req.Scopes)
	if errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	plaintext, err := generateAdminAPIKey()
	if err != nil {
		logger.Error("failed to generate api key secret: %v", err)
		SendError(ctx, 500, "Failed to generate API key")
		return
	}

	key := configstoreTables.TableAdminAPIKey{
		ID:        uuid.NewString(),
		Name:      req.Name,
		KeyPrefix: plaintext[:adminAPIKeyDisplayPrefixLen],
		Value:     plaintext,
		Status:    configstoreTables.AdminAPIKeyStatusActive,
		ExpiresAt: req.ExpiresAt,
		CreatedBy: auditActor(ctx),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := h.configStore.CreateAPIKey(ctx, &key, tx); err != nil {
			return err
		}
		return h.configStore.ReplaceAPIKeyScopes(ctx, key.ID, stampAPIKeyScopes(key.ID, scopes), tx)
	}); err != nil {
		logger.Error("failed to create api key: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create API key: %v", err))
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAPIKeyCreate, "success", key.ID)

	created, err := h.configStore.GetAPIKey(ctx, key.ID)
	if err != nil {
		key.Value = "" // never echo the secret through the fallback struct's transient field
		created = &key
	}
	// The plaintext is returned exactly once, here. It is not recoverable later.
	SendJSON(ctx, map[string]any{
		"message": "API key created successfully. Store the key now — it will not be shown again.",
		"api_key": created,
		"key":     plaintext,
	})
}

func (h *APIKeysHandler) updateAPIKey(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateAPIKeyRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	key, err := h.configStore.GetAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "API key not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve API key")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		key.Name = *req.Name
	}
	if req.ClearExpiry {
		key.ExpiresAt = nil
	} else if req.ExpiresAt != nil {
		key.ExpiresAt = req.ExpiresAt
	}

	var scopes []configstoreTables.TableAdminAPIKeyScope
	if req.Scopes != nil {
		normalized, errMsg := normalizeAPIKeyScopes(*req.Scopes)
		if errMsg != "" {
			SendError(ctx, 400, errMsg)
			return
		}
		scopes = normalized
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		key.UpdatedAt = time.Now()
		if err := h.configStore.UpdateAPIKey(ctx, key, tx); err != nil {
			return err
		}
		if req.Scopes != nil {
			return h.configStore.ReplaceAPIKeyScopes(ctx, key.ID, stampAPIKeyScopes(key.ID, scopes), tx)
		}
		return nil
	}); err != nil {
		logger.Error("failed to update api key: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update API key: %v", err))
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAPIKeyUpdate, "success", key.ID)

	updated, err := h.configStore.GetAPIKey(ctx, key.ID)
	if err != nil {
		updated = key
	}
	SendJSON(ctx, map[string]any{
		"message": "API key updated successfully",
		"api_key": updated,
	})
}

// rotateAPIKey mints a replacement secret for a key: a NEW key row is created
// (same name, expiry and scopes, RotatedFromID lineage pointing at the old row)
// and the old row is revoked in the same transaction, so the old secret stops
// working the moment the new one exists. The new plaintext is returned exactly
// once. Rotating a revoked key is the supported way to re-enable it.
func (h *APIKeysHandler) rotateAPIKey(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	old, err := h.configStore.GetAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "API key not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve API key")
		return
	}

	plaintext, err := generateAdminAPIKey()
	if err != nil {
		logger.Error("failed to generate api key secret: %v", err)
		SendError(ctx, 500, "Failed to generate API key")
		return
	}

	oldID := old.ID
	replacement := configstoreTables.TableAdminAPIKey{
		ID:            uuid.NewString(),
		Name:          old.Name,
		KeyPrefix:     plaintext[:adminAPIKeyDisplayPrefixLen],
		Value:         plaintext,
		Status:        configstoreTables.AdminAPIKeyStatusActive,
		ExpiresAt:     old.ExpiresAt,
		RotatedFromID: &oldID,
		CreatedBy:     auditActor(ctx),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	scopes := make([]configstoreTables.TableAdminAPIKeyScope, 0, len(old.Scopes))
	for _, s := range old.Scopes {
		scopes = append(scopes, configstoreTables.TableAdminAPIKeyScope{Resource: s.Resource, Operation: s.Operation})
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := h.configStore.CreateAPIKey(ctx, &replacement, tx); err != nil {
			return err
		}
		if err := h.configStore.ReplaceAPIKeyScopes(ctx, replacement.ID, stampAPIKeyScopes(replacement.ID, scopes), tx); err != nil {
			return err
		}
		// Invalidate the old secret: revoked keys are rejected by the middleware.
		old.Status = configstoreTables.AdminAPIKeyStatusRevoked
		old.UpdatedAt = time.Now()
		return h.configStore.UpdateAPIKey(ctx, old, tx)
	}); err != nil {
		logger.Error("failed to rotate api key: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to rotate API key: %v", err))
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAPIKeyRotate, "success", oldID)

	created, err := h.configStore.GetAPIKey(ctx, replacement.ID)
	if err != nil {
		replacement.Value = ""
		created = &replacement
	}
	SendJSON(ctx, map[string]any{
		"message": "API key rotated successfully. Store the new key now — it will not be shown again.",
		"api_key": created,
		"key":     plaintext,
	})
}

// revokeAPIKey soft-disables a key: the row (and its scopes) remain for audit
// and lineage, but the middleware rejects it. Re-enabling is only possible via
// rotate (which mints a fresh secret).
func (h *APIKeysHandler) revokeAPIKey(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	key, err := h.configStore.GetAPIKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "API key not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve API key")
		return
	}
	key.Status = configstoreTables.AdminAPIKeyStatusRevoked
	key.UpdatedAt = time.Now()
	if err := h.configStore.UpdateAPIKey(ctx, key); err != nil {
		logger.Error("failed to revoke api key: %v", err)
		SendError(ctx, 500, "Failed to revoke API key")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAPIKeyRevoke, "success", key.ID)
	SendJSON(ctx, map[string]any{
		"message": "API key revoked successfully",
		"api_key": key,
	})
}

func (h *APIKeysHandler) deleteAPIKey(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteAPIKey(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "API key not found")
			return
		}
		logger.Error("failed to delete api key: %v", err)
		SendError(ctx, 500, "Failed to delete API key")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAPIKeyDelete, "success", id)
	SendJSON(ctx, map[string]any{"message": "API key deleted successfully"})
}

// ---- helpers ----

// generateAdminAPIKey mints a new plaintext admin API key: the "lbk_" prefix
// followed by 32 bytes of crypto/rand entropy, hex encoded.
func generateAdminAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return configstoreTables.AdminAPIKeyPrefix + hex.EncodeToString(buf), nil
}

// normalizeAPIKeyScopes validates and de-duplicates the scope inputs (the same
// rules as RBAC permission inputs — see normalizePermissions in rbac.go). The
// second return value is a non-empty error message on validation failure.
func normalizeAPIKeyScopes(in []permissionInput) ([]configstoreTables.TableAdminAPIKeyScope, string) {
	out := make([]configstoreTables.TableAdminAPIKeyScope, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, p := range in {
		resource := strings.TrimSpace(p.Resource)
		operation := strings.TrimSpace(p.Operation)
		if resource == "" {
			return nil, "each scope must have a resource"
		}
		if !configstoreTables.IsValidRbacOperation(operation) {
			return nil, fmt.Sprintf("invalid scope operation %q", operation)
		}
		key := resource + "\x00" + operation
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, configstoreTables.TableAdminAPIKeyScope{
			Resource:  resource,
			Operation: operation,
		})
	}
	return out, ""
}

// stampAPIKeyScopes assigns a fresh ID, the owning key ID, and a creation
// timestamp to each scope before insert.
func stampAPIKeyScopes(keyID string, scopes []configstoreTables.TableAdminAPIKeyScope) []configstoreTables.TableAdminAPIKeyScope {
	now := time.Now()
	out := make([]configstoreTables.TableAdminAPIKeyScope, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, configstoreTables.TableAdminAPIKeyScope{
			ID:        uuid.NewString(),
			APIKeyID:  keyID,
			Resource:  s.Resource,
			Operation: s.Operation,
			CreatedAt: now,
		})
	}
	return out
}
