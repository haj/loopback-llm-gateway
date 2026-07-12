package sso

import (
	"encoding/json"
	"errors"
	"testing"
)

func mustParsePatch(t *testing.T, body string) *PatchRequest {
	t.Helper()
	req, err := ParsePatchRequest([]byte(body))
	if err != nil {
		t.Fatalf("parse patch: %v", err)
	}
	return req
}

func patchTestUser() map[string]any {
	raw := `{
		"userName": "bjensen@example.com",
		"active": true,
		"emails": [
			{"type": "work", "value": "bjensen@example.com"},
			{"type": "home", "value": "babs@personal.example"}
		]
	}`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func patchTestGroup() map[string]any {
	raw := `{
		"displayName": "Engineering",
		"members": [
			{"value": "u1", "display": "One"},
			{"value": "u2", "display": "Two"}
		]
	}`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}

func TestSCIMPatch_ReplaceSimpleAndNoPathForm(t *testing.T) {
	user := patchTestUser()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [
			{"op": "replace", "path": "active", "value": false},
			{"op": "replace", "value": {"userName": "new@example.com", "title": "Chief"}}
		]
	}`)
	if err := ApplyPatch(user, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if user["active"] != false {
		t.Fatalf("expected active=false, got %v", user["active"])
	}
	if user["userName"] != "new@example.com" || user["title"] != "Chief" {
		t.Fatalf("no-path replace failed: %v", user)
	}

	// Azure-style capitalized op and path.
	req = mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "Replace", "path": "Active", "value": true}]
	}`)
	if err := ApplyPatch(user, req); err != nil {
		t.Fatalf("apply capitalized: %v", err)
	}
	if user["active"] != true {
		t.Fatalf("case-insensitive path must hit the existing key, got %v", user)
	}
}

func TestSCIMPatch_FilteredSubAttribute(t *testing.T) {
	user := patchTestUser()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "replace", "path": "emails[type eq \"work\"].value", "value": "new-work@example.com"}]
	}`)
	if err := ApplyPatch(user, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	emails := user["emails"].([]any)
	work := emails[0].(map[string]any)
	home := emails[1].(map[string]any)
	if work["value"] != "new-work@example.com" {
		t.Fatalf("work email not replaced: %v", work)
	}
	if home["value"] != "babs@personal.example" {
		t.Fatalf("home email must be untouched: %v", home)
	}
}

func TestSCIMPatch_AddAppendsToMultiValued(t *testing.T) {
	group := patchTestGroup()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "add", "path": "members", "value": [{"value": "u3", "display": "Three"}]}]
	}`)
	if err := ApplyPatch(group, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	members := group["members"].([]any)
	if len(members) != 3 {
		t.Fatalf("expected 3 members after add, got %d", len(members))
	}
}

func TestSCIMPatch_RemoveMembers(t *testing.T) {
	// Filtered remove: members[value eq "u1"].
	group := patchTestGroup()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "remove", "path": "members[value eq \"u1\"]"}]
	}`)
	if err := ApplyPatch(group, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	members := group["members"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["value"] != "u2" {
		t.Fatalf("expected only u2 to remain, got %v", members)
	}

	// Azure-style remove-with-value payload.
	group = patchTestGroup()
	req = mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "remove", "path": "members", "value": [{"value": "u2"}]}]
	}`)
	if err := ApplyPatch(group, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	members = group["members"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["value"] != "u1" {
		t.Fatalf("expected only u1 to remain, got %v", members)
	}
}

func TestSCIMPatch_RemoveSimpleAttribute(t *testing.T) {
	user := patchTestUser()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "remove", "path": "userName"}]
	}`)
	if err := ApplyPatch(user, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, exists := user["userName"]; exists {
		t.Fatal("userName must be removed")
	}
}

func TestSCIMPatch_Errors(t *testing.T) {
	assertScimType := func(t *testing.T, err error, want string) {
		t.Helper()
		var patchErr *SCIMPatchError
		if !errors.As(err, &patchErr) {
			t.Fatalf("expected SCIMPatchError, got %v", err)
		}
		if patchErr.ScimType != want {
			t.Fatalf("expected scimType %q, got %q (%s)", want, patchErr.ScimType, patchErr.Detail)
		}
	}

	// Envelope errors.
	if _, err := ParsePatchRequest([]byte(`{nope`)); err == nil {
		t.Fatal("malformed JSON must error")
	}
	_, err := ParsePatchRequest([]byte(`{"schemas": ["wrong"], "Operations": [{"op": "add"}]}`))
	assertScimType(t, err, SCIMErrInvalidSyntax)
	_, err = ParsePatchRequest([]byte(`{"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"], "Operations": []}`))
	assertScimType(t, err, SCIMErrInvalidSyntax)

	// Unknown op.
	user := patchTestUser()
	req := mustParsePatch(t, `{"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"], "Operations": [{"op": "merge", "path": "x", "value": 1}]}`)
	assertScimType(t, ApplyPatch(user, req), SCIMErrInvalidSyntax)

	// remove without path.
	req = mustParsePatch(t, `{"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"], "Operations": [{"op": "remove"}]}`)
	assertScimType(t, ApplyPatch(user, req), SCIMErrInvalidPath)

	// Bad filter in path.
	req = mustParsePatch(t, `{"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"], "Operations": [{"op": "replace", "path": "emails[type xy \"w\"].value", "value": "x"}]}`)
	assertScimType(t, ApplyPatch(user, req), SCIMErrInvalidPath)

	// noTarget: filter matches nothing.
	req = mustParsePatch(t, `{"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"], "Operations": [{"op": "replace", "path": "emails[type eq \"fax\"].value", "value": "x"}]}`)
	assertScimType(t, ApplyPatch(user, req), SCIMErrNoTarget)
}

func TestSCIMPatch_NoPathAddMergesCaseInsensitively(t *testing.T) {
	user := patchTestUser()
	req := mustParsePatch(t, `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "add", "value": {"Active": false}}]
	}`)
	if err := ApplyPatch(user, req); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if user["active"] != false {
		t.Fatalf("expected the existing lowercase key to be updated, got %v", user)
	}
	if _, duplicated := user["Active"]; duplicated {
		t.Fatalf("must not create a duplicate differently-cased key: %v", user)
	}
}
