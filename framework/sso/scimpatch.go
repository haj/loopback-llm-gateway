// This file contains a pure RFC 7644 §3.5.2 SCIM PatchOp parser and applier.
// ParsePatchRequest validates the PatchOp envelope; ApplyPatch mutates a SCIM
// resource JSON map with add/replace/remove operations, including the
// omitted-path map form and filtered multi-valued paths like
// `emails[type eq "work"].value` and `members[value eq "id"]`.
//
// Errors carry an RFC 7644 scimType (invalidPath / invalidSyntax / noTarget)
// so the HTTP layer can build a spec-compliant error body.
package sso

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PatchOp schema URN (RFC 7644 §3.5.2).
const PatchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"

// SCIM error scimType values used by the patch applier.
const (
	SCIMErrInvalidPath   = "invalidPath"
	SCIMErrInvalidSyntax = "invalidSyntax"
	SCIMErrNoTarget      = "noTarget"
)

// SCIMPatchError is a patch failure with its RFC 7644 scimType.
type SCIMPatchError struct {
	ScimType string
	Detail   string
}

func (e *SCIMPatchError) Error() string { return e.Detail }

func patchErr(scimType, format string, args ...any) *SCIMPatchError {
	return &SCIMPatchError{ScimType: scimType, Detail: fmt.Sprintf(format, args...)}
}

// PatchOperation is one entry of Operations[].
type PatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// PatchRequest is the parsed PatchOp envelope.
type PatchRequest struct {
	Schemas    []string         `json:"schemas"`
	Operations []PatchOperation `json:"Operations"`
}

// ParsePatchRequest parses and validates a PatchOp body.
func ParsePatchRequest(body []byte) (*PatchRequest, error) {
	var req PatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, patchErr(SCIMErrInvalidSyntax, "malformed PatchOp body: %v", err)
	}
	hasSchema := false
	for _, s := range req.Schemas {
		if s == PatchOpSchema {
			hasSchema = true
			break
		}
	}
	if !hasSchema {
		return nil, patchErr(SCIMErrInvalidSyntax, "PatchOp body must declare schema %s", PatchOpSchema)
	}
	if len(req.Operations) == 0 {
		return nil, patchErr(SCIMErrInvalidSyntax, "PatchOp body must contain at least one operation")
	}
	return &req, nil
}

// patchPath is a parsed PATCH path: attribute, optional value filter, optional
// sub-attribute (e.g. emails[type eq "work"].value).
type patchPath struct {
	attr    string
	filter  *SCIMFilter // nil when no [ ... ] selector
	subAttr string
}

// parsePatchPath parses the PATCH path grammar.
func parsePatchPath(path string) (*patchPath, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, patchErr(SCIMErrInvalidPath, "empty path")
	}
	open := strings.IndexByte(path, '[')
	if open < 0 {
		// Simple attr or attr.subAttr.
		if dot := strings.IndexByte(path, '.'); dot >= 0 {
			return &patchPath{attr: path[:dot], subAttr: path[dot+1:]}, nil
		}
		return &patchPath{attr: path}, nil
	}
	closeIdx := strings.LastIndexByte(path, ']')
	if closeIdx < open {
		return nil, patchErr(SCIMErrInvalidPath, "unbalanced brackets in path %q", path)
	}
	filter, err := ParseSCIMFilter(path[open+1 : closeIdx])
	if err != nil {
		return nil, patchErr(SCIMErrInvalidPath, "invalid value filter in path %q: %v", path, err)
	}
	pp := &patchPath{attr: path[:open], filter: filter}
	rest := path[closeIdx+1:]
	if rest != "" {
		if !strings.HasPrefix(rest, ".") || len(rest) < 2 {
			return nil, patchErr(SCIMErrInvalidPath, "invalid sub-attribute in path %q", path)
		}
		pp.subAttr = rest[1:]
	}
	return pp, nil
}

// canonicalKey finds the existing key matching attr case-insensitively, or
// returns attr unchanged for a fresh insert.
func canonicalKey(m map[string]any, attr string) string {
	if _, ok := m[attr]; ok {
		return attr
	}
	for k := range m {
		if strings.EqualFold(k, attr) {
			return k
		}
	}
	return attr
}

// ApplyPatch applies every operation in order, mutating resource. The first
// failing operation aborts (per RFC the entire patch is atomic — callers must
// apply to a copy or discard on error; the HTTP layer re-loads on failure).
func ApplyPatch(resource map[string]any, req *PatchRequest) error {
	for i := range req.Operations {
		op := req.Operations[i]
		if err := applyOne(resource, &op); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(resource map[string]any, op *PatchOperation) error {
	switch strings.ToLower(strings.TrimSpace(op.Op)) {
	case "add":
		return applyAddOrReplace(resource, op, true)
	case "replace":
		return applyAddOrReplace(resource, op, false)
	case "remove":
		return applyRemove(resource, op)
	default:
		return patchErr(SCIMErrInvalidSyntax, "unsupported patch op %q", op.Op)
	}
}

func applyAddOrReplace(resource map[string]any, op *PatchOperation, isAdd bool) error {
	// Omitted path: value must be a map of attributes to set.
	if strings.TrimSpace(op.Path) == "" {
		values, ok := op.Value.(map[string]any)
		if !ok {
			return patchErr(SCIMErrInvalidSyntax, "%s without a path requires an object value", op.Op)
		}
		for k, v := range values {
			resource[canonicalKey(resource, k)] = v
		}
		return nil
	}

	pp, err := parsePatchPath(op.Path)
	if err != nil {
		return err
	}
	key := canonicalKey(resource, pp.attr)

	// No filter: simple attribute (or attr.subAttr) set/append.
	if pp.filter == nil {
		if pp.subAttr == "" {
			if isAdd {
				// add on a multi-valued attribute appends.
				if existing, ok := resource[key].([]any); ok {
					resource[key] = append(existing, toList(op.Value)...)
					return nil
				}
			}
			resource[key] = op.Value
			return nil
		}
		sub, ok := resource[key].(map[string]any)
		if !ok {
			if !isAdd {
				return patchErr(SCIMErrNoTarget, "path %q has no target", op.Path)
			}
			sub = map[string]any{}
			resource[key] = sub
		}
		sub[canonicalKey(sub, pp.subAttr)] = op.Value
		return nil
	}

	// Filtered multi-valued path: apply to every matching element.
	list, ok := resource[key].([]any)
	if !ok {
		return patchErr(SCIMErrNoTarget, "path %q has no target", op.Path)
	}
	matched := false
	for _, elem := range list {
		m, isMap := elem.(map[string]any)
		if !isMap || !pp.filter.Matches(m) {
			continue
		}
		matched = true
		if pp.subAttr == "" {
			// Replace the whole element's attributes with the value object.
			values, isObj := op.Value.(map[string]any)
			if !isObj {
				return patchErr(SCIMErrInvalidSyntax, "replacing a filtered element requires an object value")
			}
			for k, v := range values {
				m[canonicalKey(m, k)] = v
			}
		} else {
			m[canonicalKey(m, pp.subAttr)] = op.Value
		}
	}
	if !matched {
		return patchErr(SCIMErrNoTarget, "no element matches the filter in path %q", op.Path)
	}
	return nil
}

func applyRemove(resource map[string]any, op *PatchOperation) error {
	if strings.TrimSpace(op.Path) == "" {
		return patchErr(SCIMErrInvalidPath, "remove requires a path")
	}
	pp, err := parsePatchPath(op.Path)
	if err != nil {
		return err
	}
	key := canonicalKey(resource, pp.attr)

	if pp.filter == nil && pp.subAttr == "" {
		// Remove-with-value on a multi-valued attribute removes the listed
		// elements (Azure AD sends Group member removals this way).
		if list, isList := resource[key].([]any); isList && op.Value != nil {
			keep := removeMatchingValues(list, op.Value)
			resource[key] = keep
			return nil
		}
		delete(resource, key)
		return nil
	}

	if pp.filter == nil && pp.subAttr != "" {
		sub, ok := resource[key].(map[string]any)
		if !ok {
			return patchErr(SCIMErrNoTarget, "path %q has no target", op.Path)
		}
		delete(sub, canonicalKey(sub, pp.subAttr))
		return nil
	}

	list, ok := resource[key].([]any)
	if !ok {
		return patchErr(SCIMErrNoTarget, "path %q has no target", op.Path)
	}
	var keep []any
	matched := false
	for _, elem := range list {
		m, isMap := elem.(map[string]any)
		if isMap && pp.filter.Matches(m) {
			matched = true
			if pp.subAttr != "" {
				delete(m, canonicalKey(m, pp.subAttr))
				keep = append(keep, elem)
			}
			continue
		}
		keep = append(keep, elem)
	}
	if !matched {
		return patchErr(SCIMErrNoTarget, "no element matches the filter in path %q", op.Path)
	}
	if keep == nil {
		keep = []any{}
	}
	resource[key] = keep
	return nil
}

// toList coerces a patch value to a list for multi-valued append.
func toList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return []any{v}
}

// removeMatchingValues filters out list elements referenced by a
// remove-with-value payload: each value entry's "value" sub-attribute
// identifies the element to drop (the Group members convention).
func removeMatchingValues(list []any, value any) []any {
	targets := map[string]bool{}
	for _, entry := range toList(value) {
		if m, ok := entry.(map[string]any); ok {
			if id, ok := lookupCaseInsensitive(m, "value"); ok {
				if s, ok := id.(string); ok {
					targets[strings.ToLower(s)] = true
				}
			}
		}
	}
	keep := []any{}
	for _, elem := range list {
		if m, ok := elem.(map[string]any); ok {
			if id, ok := lookupCaseInsensitive(m, "value"); ok {
				if s, ok := id.(string); ok && targets[strings.ToLower(s)] {
					continue
				}
			}
		}
		keep = append(keep, elem)
	}
	return keep
}
