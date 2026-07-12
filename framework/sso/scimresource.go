// This file contains the SCIM 2.0 JSON representations (RFC 7643) of the
// configstore SCIM mirror rows, plus the ListResponse and error envelopes
// (RFC 7644). Pure conversion — no I/O — so the HTTP layer, filter evaluator,
// and PATCH applier all operate on the same map shape.
package sso

import (
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// SCIM schema URNs.
const (
	SCIMUserSchema         = "urn:ietf:params:scim:schemas:core:2.0:User"
	SCIMGroupSchema        = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SCIMListResponseSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	SCIMErrorSchema        = "urn:ietf:params:scim:api:messages:2.0:Error"
)

// SCIMUserResource renders a mirror row as a SCIM User resource.
func SCIMUserResource(u *tables.TableSCIMUser) map[string]any {
	resource := map[string]any{
		"schemas":     []any{SCIMUserSchema},
		"id":          u.ID,
		"externalId":  u.ExternalID,
		"userName":    u.UserName,
		"displayName": u.DisplayName,
		"active":      u.Active,
		"meta": map[string]any{
			"resourceType": "User",
			"created":      u.CreatedAt.UTC().Format(time.RFC3339),
			"lastModified": u.UpdatedAt.UTC().Format(time.RFC3339),
		},
	}
	if u.Email != "" {
		resource["emails"] = []any{
			map[string]any{"value": u.Email, "type": "work", "primary": true},
		}
	}
	if len(u.Groups) > 0 {
		groups := make([]any, 0, len(u.Groups))
		for _, g := range u.Groups {
			groups = append(groups, map[string]any{"display": g})
		}
		resource["groups"] = groups
	}
	return resource
}

// ApplySCIMUserResource extracts the writable SCIM User attributes from a
// resource map onto the mirror row (id/meta are server-owned and ignored).
func ApplySCIMUserResource(u *tables.TableSCIMUser, resource map[string]any) {
	if v, ok := lookupCaseInsensitive(resource, "externalId"); ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			u.ExternalID = strings.TrimSpace(s)
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "userName"); ok {
		if s, ok := v.(string); ok {
			u.UserName = strings.TrimSpace(s)
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "displayName"); ok {
		if s, ok := v.(string); ok {
			u.DisplayName = strings.TrimSpace(s)
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "active"); ok {
		if b, ok := v.(bool); ok {
			u.Active = b
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "emails"); ok {
		if list, ok := v.([]any); ok && len(list) > 0 {
			// Prefer the primary (or first) email's value.
			pick := ""
			for _, e := range list {
				m, ok := e.(map[string]any)
				if !ok {
					continue
				}
				val, _ := lookupCaseInsensitive(m, "value")
				s, _ := val.(string)
				if s == "" {
					continue
				}
				if pick == "" {
					pick = s
				}
				if primary, ok := lookupCaseInsensitive(m, "primary"); ok {
					if p, ok := primary.(bool); ok && p {
						pick = s
						break
					}
				}
			}
			if pick != "" {
				u.Email = strings.ToLower(strings.TrimSpace(pick))
			}
		}
	}
}

// SCIMGroupResource renders a mirror row as a SCIM Group resource.
func SCIMGroupResource(g *tables.TableSCIMGroup) map[string]any {
	members := make([]any, 0, len(g.Members))
	for _, m := range g.Members {
		members = append(members, map[string]any{"value": m})
	}
	return map[string]any{
		"schemas":     []any{SCIMGroupSchema},
		"id":          g.ID,
		"externalId":  g.ExternalID,
		"displayName": g.DisplayName,
		"members":     members,
		"meta": map[string]any{
			"resourceType": "Group",
			"created":      g.CreatedAt.UTC().Format(time.RFC3339),
			"lastModified": g.UpdatedAt.UTC().Format(time.RFC3339),
		},
	}
}

// ApplySCIMGroupResource extracts the writable SCIM Group attributes onto the
// mirror row.
func ApplySCIMGroupResource(g *tables.TableSCIMGroup, resource map[string]any) {
	if v, ok := lookupCaseInsensitive(resource, "externalId"); ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			g.ExternalID = strings.TrimSpace(s)
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "displayName"); ok {
		if s, ok := v.(string); ok {
			g.DisplayName = strings.TrimSpace(s)
		}
	}
	if v, ok := lookupCaseInsensitive(resource, "members"); ok {
		if list, ok := v.([]any); ok {
			members := make([]string, 0, len(list))
			for _, e := range list {
				if m, ok := e.(map[string]any); ok {
					if val, ok := lookupCaseInsensitive(m, "value"); ok {
						if s, ok := val.(string); ok && s != "" {
							members = append(members, s)
						}
					}
				}
			}
			g.Members = members
		}
	}
}

// SCIMListResponse builds the RFC 7644 list envelope.
func SCIMListResponse(resources []map[string]any, totalResults, startIndex int) map[string]any {
	if resources == nil {
		resources = []map[string]any{}
	}
	return map[string]any{
		"schemas":      []any{SCIMListResponseSchema},
		"totalResults": totalResults,
		"startIndex":   startIndex,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	}
}

// SCIMErrorBody builds the RFC 7644 error envelope (status is a string per
// the RFC examples). scimType may be empty.
func SCIMErrorBody(status int, scimType, detail string) map[string]any {
	body := map[string]any{
		"schemas": []any{SCIMErrorSchema},
		"status":  strconv.Itoa(status),
		"detail":  detail,
	}
	if scimType != "" {
		body["scimType"] = scimType
	}
	return body
}
