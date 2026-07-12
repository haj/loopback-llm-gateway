package sso

import "testing"

// scimTestUser is the RFC 7643-shaped resource the filter tests run against.
func scimTestUser() map[string]any {
	return map[string]any{
		"userName":   "bjensen@example.com",
		"externalId": "ext-1",
		"active":     true,
		"title":      "Tour Guide",
		"loginCount": float64(7),
		"name": map[string]any{
			"familyName": "Jensen",
			"givenName":  "Barbara",
		},
		"emails": []any{
			map[string]any{"type": "work", "value": "bjensen@example.com"},
			map[string]any{"type": "home", "value": "babs@personal.example"},
		},
		"groups": []any{"eng", "sre"},
	}
}

func TestSCIMFilter_Matches(t *testing.T) {
	cases := []struct {
		filter string
		want   bool
	}{
		// RFC 7644 §3.4.2.2 examples (adapted to the test resource).
		{`userName eq "bjensen@example.com"`, true},
		{`userName eq "BJENSEN@EXAMPLE.COM"`, true}, // caseExact=false
		{`userName eq "other"`, false},
		{`name.familyName co "en"`, true},
		{`userName sw "bjensen"`, true},
		{`userName ew "example.com"`, true},
		{`title pr`, true},
		{`missing pr`, false},
		{`userName ne "other"`, true},
		{`missing ne "anything"`, true},
		{`active eq true`, true},
		{`active eq false`, false},
		{`loginCount gt 5`, true},
		{`loginCount le 7`, true},
		{`loginCount lt 7`, false},
		// Logical composition and grouping.
		{`title pr and userName sw "bjensen"`, true},
		{`title pr and userName sw "nope"`, false},
		{`title pr or userName sw "nope"`, true},
		{`not (userName eq "other")`, true},
		{`not (userName eq "bjensen@example.com")`, false},
		{`(userName eq "other" or title pr) and active eq true`, true},
		// Value paths.
		{`emails[type eq "work"]`, true},
		{`emails[type eq "work" and value co "@example.com"]`, true},
		{`emails[type eq "work" and value co "@personal"]`, false},
		{`emails[type eq "fax"]`, false},
		// Multi-valued plain attribute: any element matches.
		{`groups eq "sre"`, true},
		{`groups eq "sales"`, false},
		// Case-insensitive attribute names.
		{`USERNAME eq "bjensen@example.com"`, true},
		// URN-prefixed attribute.
		{`urn:ietf:params:scim:schemas:core:2.0:User:userName eq "bjensen@example.com"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			f, err := ParseSCIMFilter(tc.filter)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := f.Matches(scimTestUser()); got != tc.want {
				t.Fatalf("filter %q: want %v, got %v", tc.filter, tc.want, got)
			}
		})
	}
}

func TestSCIMFilter_MalformedInputs(t *testing.T) {
	cases := []string{
		``,
		`userName`,
		`userName eq`,
		`userName xy "v"`,
		`userName eq "unterminated`,
		`(userName eq "x"`,
		`emails[type eq "work"`,
		`userName eq "a" bogus`,
		`not userName eq "x"`, // not requires parens
	}
	for _, filter := range cases {
		t.Run(filter, func(t *testing.T) {
			if _, err := ParseSCIMFilter(filter); err == nil {
				t.Fatalf("expected parse error for %q", filter)
			}
		})
	}
}
