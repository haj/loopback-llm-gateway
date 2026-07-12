package sso

import (
	"reflect"
	"testing"
)

func mappingIdentity(raw map[string]any) *Identity {
	return &Identity{Provider: ProviderKeycloak, Subject: "u1", Raw: raw}
}

func TestApplyMappings_RoleFirstMatchWins(t *testing.T) {
	mappings := AttributeMappings{Roles: []AttributeRoleMapping{
		{Attribute: "department", Value: "platform", Role: "admin"},
		{Attribute: "department", Value: "platform", Role: "editor"}, // shadowed
		{Attribute: "groups", Value: "ops", Role: "viewer"},
	}}

	out := ApplyMappings(mappingIdentity(map[string]any{"department": "platform", "groups": []any{"ops"}}), mappings)
	if out.RoleName != "admin" {
		t.Fatalf("expected first matching rule to win, got %q", out.RoleName)
	}

	out = ApplyMappings(mappingIdentity(map[string]any{"groups": []any{"ops"}}), mappings)
	if out.RoleName != "viewer" {
		t.Fatalf("expected later rule to match when earlier ones do not, got %q", out.RoleName)
	}
}

func TestApplyMappings_DotPathClaims(t *testing.T) {
	mappings := AttributeMappings{Roles: []AttributeRoleMapping{
		{Attribute: "realm_access.roles", Value: "gateway-admin", Role: "admin"},
	}}
	raw := map[string]any{"realm_access": map[string]any{"roles": []any{"user", "gateway-admin"}}}
	out := ApplyMappings(mappingIdentity(raw), mappings)
	if out.RoleName != "admin" {
		t.Fatalf("expected dot-path array membership to match, got %q", out.RoleName)
	}
}

func TestApplyMappings_TeamsAllMatchesAndPassThrough(t *testing.T) {
	mappings := AttributeMappings{Teams: []AttributeTeamMapping{
		{Attribute: "groups", Value: "core-eng", Team: "platform-team"},
		{Attribute: "groups", Value: "*"}, // pass-through of every group value
		{Attribute: "department", Value: "sales", Team: "sales-team"},
	}}
	raw := map[string]any{"groups": []any{"core-eng", "sre"}, "department": "sales"}
	out := ApplyMappings(mappingIdentity(raw), mappings)

	want := []string{"platform-team", "core-eng", "sre", "sales-team"}
	if !reflect.DeepEqual(out.TeamRefs, want) {
		t.Fatalf("expected all matching team rules to apply with dedup (want %v, got %v)", want, out.TeamRefs)
	}
}

func TestApplyMappings_ValueWithoutTeamNameMapsTheValue(t *testing.T) {
	mappings := AttributeMappings{Teams: []AttributeTeamMapping{
		{Attribute: "groups", Value: "core-eng"}, // no Team: the value is the ref
	}}
	out := ApplyMappings(mappingIdentity(map[string]any{"groups": []any{"core-eng"}}), mappings)
	if !reflect.DeepEqual(out.TeamRefs, []string{"core-eng"}) {
		t.Fatalf("expected the matched value as the team ref, got %v", out.TeamRefs)
	}
}

func TestApplyMappings_BusinessUnitFirstMatch(t *testing.T) {
	mappings := AttributeMappings{BusinessUnits: []AttributeBusinessUnitMapping{
		{Attribute: "division", Value: "emea", BusinessUnit: "emea-bu"},
		{Attribute: "division", Value: "*", BusinessUnit: "global-bu"},
	}}
	out := ApplyMappings(mappingIdentity(map[string]any{"division": "emea"}), mappings)
	if out.BusinessUnitName != "emea-bu" {
		t.Fatalf("expected first BU match, got %q", out.BusinessUnitName)
	}
	out = ApplyMappings(mappingIdentity(map[string]any{"division": "apac"}), mappings)
	if out.BusinessUnitName != "global-bu" {
		t.Fatalf("expected wildcard BU fallback, got %q", out.BusinessUnitName)
	}
}

func TestApplyMappings_NoMatchLeavesOutcomeEmpty(t *testing.T) {
	mappings := AttributeMappings{
		Roles:         []AttributeRoleMapping{{Attribute: "x", Value: "y", Role: "admin"}},
		Teams:         []AttributeTeamMapping{{Attribute: "groups", Value: "nope", Team: "t"}},
		BusinessUnits: []AttributeBusinessUnitMapping{{Attribute: "z", Value: "*", BusinessUnit: "bu"}},
	}
	out := ApplyMappings(mappingIdentity(map[string]any{"groups": []any{"other"}}), mappings)
	if !out.Empty() {
		t.Fatalf("expected empty outcome, got %+v", out)
	}

	// Nil identity / raw claims never panic.
	out = ApplyMappings(nil, mappings)
	if !out.Empty() {
		t.Fatalf("expected empty outcome for nil identity, got %+v", out)
	}
}
