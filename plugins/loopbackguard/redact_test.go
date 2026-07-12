package loopbackguard

import (
	"strings"
	"testing"
)

func TestRedactor_DefaultPII(t *testing.T) {
	r := NewRedactor() // default PII rules
	cases := []struct {
		in       string
		mustGone string
		token    string
	}{
		{"email me at jane.doe@example.com please", "jane.doe@example.com", "[REDACTED_EMAIL]"},
		{"ssn 123-45-6789", "123-45-6789", "[REDACTED_SSN]"},
		{"card 4111 1111 1111 1111", "4111 1111 1111 1111", "[REDACTED_CC]"},
		{"call 415-555-0132", "415-555-0132", "[REDACTED_PHONE]"},
		{"host 192.168.0.1", "192.168.0.1", "[REDACTED_IP]"},
	}
	for _, c := range cases {
		out, n := r.Redact(c.in)
		if n == 0 {
			t.Errorf("expected a match for %q", c.in)
		}
		if strings.Contains(out, c.mustGone) {
			t.Errorf("%q still present after redaction: %q", c.mustGone, out)
		}
		if !strings.Contains(out, c.token) {
			t.Errorf("expected token %q in %q", c.token, out)
		}
	}
}

func TestRedactor_NoMatchUnchanged(t *testing.T) {
	r := NewRedactor()
	out, n := r.Redact("just a normal sentence")
	if n != 0 || out != "just a normal sentence" {
		t.Errorf("clean text should be unchanged, got %q (n=%d)", out, n)
	}
}

func TestRegexReplaceRule_Custom(t *testing.T) {
	rule, err := NewRegexReplaceRule("apikey", `sk-[A-Za-z0-9]{8,}`, "[REDACTED_KEY]")
	if err != nil {
		t.Fatal(err)
	}
	out, n := NewRedactor(rule).Redact("key sk-abcdef123456 end")
	if n != 1 || strings.Contains(out, "sk-abcdef123456") {
		t.Errorf("custom rule failed: %q (n=%d)", out, n)
	}
	if _, err := NewRegexReplaceRule("bad", "(", ""); err == nil {
		t.Error("expected compile error for invalid pattern")
	}
}

func TestPreRequestHook_MutatesRequest(t *testing.T) {
	p := NewPlugin().WithRedactor(NewRedactor())
	req := chatReq("contact me at bob@corp.com or 123-45-6789")

	if err := p.PreRequestHook(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := *req.ChatRequest.Input[0].Content.ContentStr
	if strings.Contains(got, "bob@corp.com") || strings.Contains(got, "123-45-6789") {
		t.Errorf("PII not redacted in committed request: %q", got)
	}
	if !strings.Contains(got, "[REDACTED_EMAIL]") || !strings.Contains(got, "[REDACTED_SSN]") {
		t.Errorf("expected redaction tokens, got %q", got)
	}
}

func TestPreRequestHook_NoRedactorNoop(t *testing.T) {
	p := NewPlugin() // no redactor
	req := chatReq("email bob@corp.com")
	if err := p.PreRequestHook(nil, req); err != nil {
		t.Fatal(err)
	}
	if got := *req.ChatRequest.Input[0].Content.ContentStr; !strings.Contains(got, "bob@corp.com") {
		t.Errorf("without a redactor text must be untouched, got %q", got)
	}
}
