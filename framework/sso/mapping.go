// This file contains the attribute→role/team/business-unit mapping engine.
// It is a pure function over a validated Identity and the
// operator-configured mapping rules; the caller (ProvisionFromIdentity) owns
// all store side effects. One engine serves all three provisioning entry
// points: JWT login, pull sync, and inbound SCIM.
package sso

import "strings"

// AttributeMappings bundles the three mapping rule lists (mirrors the
// attributeRoleMappings / attributeTeamMappings / attributeBusinessUnitMappings
// arrays every provider config carries).
type AttributeMappings struct {
	Roles         []AttributeRoleMapping
	Teams         []AttributeTeamMapping
	BusinessUnits []AttributeBusinessUnitMapping
}

// Empty reports whether no rules are configured (mapping is a no-op).
func (m AttributeMappings) Empty() bool {
	return len(m.Roles) == 0 && len(m.Teams) == 0 && len(m.BusinessUnits) == 0
}

// MappingOutcome is the result of evaluating the mapping rules against one
// identity. Empty fields mean "no rule matched" — the caller changes nothing.
type MappingOutcome struct {
	// RoleName is the gateway role to assign (first matching role rule wins).
	RoleName string
	// TeamRefs are team references to link (ALL matching team rules apply,
	// deduplicated, order preserved). A rule with value "*" passes the claim
	// values themselves through as refs (e.g. IdP group names).
	TeamRefs []string
	// BusinessUnitName is the business unit to place the user in (first
	// matching rule wins).
	BusinessUnitName string
}

// Empty reports whether nothing matched.
func (o MappingOutcome) Empty() bool {
	return o.RoleName == "" && len(o.TeamRefs) == 0 && o.BusinessUnitName == ""
}

// claimValues resolves a dot-path attribute against the identity's raw claims
// and coerces the result into a string slice (a scalar string becomes a
// one-element slice).
func claimValues(identity *Identity, attribute string) []string {
	if identity == nil || identity.Raw == nil {
		return nil
	}
	val := getClaimPath(identity.Raw, attribute)
	if val == nil {
		return nil
	}
	if s := stringClaim(val); s != "" {
		return []string{s}
	}
	return stringsClaim(val)
}

// attributeMatches reports whether the attribute's value(s) match the rule
// value. "*" matches any present, non-empty value.
func attributeMatches(values []string, ruleValue string) bool {
	if len(values) == 0 {
		return false
	}
	if ruleValue == "*" {
		return true
	}
	for _, v := range values {
		if v == ruleValue {
			return true
		}
	}
	return false
}

// ApplyMappings evaluates the mapping rules against the identity. Pure: no
// store access, no side effects.
func ApplyMappings(identity *Identity, mappings AttributeMappings) MappingOutcome {
	var outcome MappingOutcome

	// Roles: first match wins.
	for _, rule := range mappings.Roles {
		if strings.TrimSpace(rule.Role) == "" {
			continue
		}
		if attributeMatches(claimValues(identity, rule.Attribute), rule.Value) {
			outcome.RoleName = rule.Role
			break
		}
	}

	// Teams: every matching rule applies; "*" passes claim values through.
	seen := map[string]bool{}
	for _, rule := range mappings.Teams {
		values := claimValues(identity, rule.Attribute)
		if !attributeMatches(values, rule.Value) {
			continue
		}
		var refs []string
		switch {
		case strings.TrimSpace(rule.Team) != "":
			refs = []string{strings.TrimSpace(rule.Team)}
		case rule.Value == "*":
			// Pass-through: the claim values themselves are the team refs.
			refs = values
		default:
			// A specific value with no team name maps the value itself.
			refs = []string{rule.Value}
		}
		for _, ref := range refs {
			if ref != "" && !seen[ref] {
				seen[ref] = true
				outcome.TeamRefs = append(outcome.TeamRefs, ref)
			}
		}
	}

	// Business unit: first match wins.
	for _, rule := range mappings.BusinessUnits {
		if strings.TrimSpace(rule.BusinessUnit) == "" {
			continue
		}
		if attributeMatches(claimValues(identity, rule.Attribute), rule.Value) {
			outcome.BusinessUnitName = strings.TrimSpace(rule.BusinessUnit)
			break
		}
	}

	return outcome
}
