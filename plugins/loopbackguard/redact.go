// Redaction is a *transforming* guardrail: instead of blocking a request, it
// rewrites message text to mask PII/secrets before the request reaches the
// provider. It is the in-process, dependency-free complement to an external
// analyzer like Presidio — fast and self-contained, but pattern-based, so it
// catches *structured* PII (emails, SSNs, cards, phones, IPs) and not free-text
// entities such as names (which need NLP).
//
// The generic regex-replace mechanism is ported from Portkey's MIT regexReplace
// guardrail; the default PII rule set is original. See THIRD_PARTY_LICENSES.md.
//
// Redaction runs in PreRequestHook, which is Bifrost's committed-mutation phase
// (its changes are observed by the provider call and all fallbacks) — unlike
// PreLLMHook, whose mutations are not reliably propagated.

package loopbackguard

import (
	"context"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// RedactRule replaces every match of Re with Replacement. Label is for logging.
type RedactRule struct {
	Label       string
	Re          *regexp.Regexp
	Replacement string
}

// NewRegexReplaceRule builds a custom rule from a pattern string (Portkey's
// regexReplace). Returns an error if the pattern does not compile.
func NewRegexReplaceRule(label, pattern, replacement string) (RedactRule, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return RedactRule{}, err
	}
	if replacement == "" {
		replacement = "[REDACTED]"
	}
	return RedactRule{Label: label, Re: re, Replacement: replacement}, nil
}

// DefaultPIIRules returns the built-in structured-PII redaction rules. Order
// matters: more specific patterns run before broader ones.
func DefaultPIIRules() []RedactRule {
	return []RedactRule{
		{"email", regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), "[REDACTED_EMAIL]"},
		{"ssn", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "[REDACTED_SSN]"},
		{"credit_card", regexp.MustCompile(`\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{1,4}\b`), "[REDACTED_CC]"},
		{"phone", regexp.MustCompile(`\b(?:\+?1[ .\-]?)?\(?\d{3}\)?[ .\-]?\d{3}[ .\-]?\d{4}\b`), "[REDACTED_PHONE]"},
		{"ipv4", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), "[REDACTED_IP]"},
	}
}

// Redactor applies an ordered list of RedactRules to text.
type Redactor struct {
	rules []RedactRule
}

// NewRedactor builds a Redactor. With no rules, it uses DefaultPIIRules.
func NewRedactor(rules ...RedactRule) *Redactor {
	if len(rules) == 0 {
		rules = DefaultPIIRules()
	}
	return &Redactor{rules: rules}
}

// Name identifies the detector. (PIIDetector)
func (r *Redactor) Name() string { return "regex-pii" }

// Detect reports whether any rule matches, without mutating the text, returning
// the matched rule labels as detail. Never errors (pure, local). (PIIDetector)
func (r *Redactor) Detect(_ context.Context, text string) (bool, string, error) {
	var hits []string
	for _, rule := range r.rules {
		if rule.Re.MatchString(text) {
			hits = append(hits, rule.Label)
		}
	}
	if len(hits) == 0 {
		return false, "", nil
	}
	return true, strings.Join(hits, ","), nil
}

// Redact returns the redacted text and the number of rules that matched.
func (r *Redactor) Redact(text string) (string, int) {
	out := text
	hits := 0
	for _, rule := range r.rules {
		if rule.Re.MatchString(out) {
			out = rule.Re.ReplaceAllString(out, rule.Replacement)
			hits++
		}
	}
	return out, hits
}

// redactMessages rewrites every message's text content in place.
func redactMessages(msgs []schemas.ChatMessage, r *Redactor) {
	for i := range msgs {
		c := msgs[i].Content
		if c == nil {
			continue
		}
		if c.ContentStr != nil {
			if red, n := r.Redact(*c.ContentStr); n > 0 {
				c.ContentStr = &red
			}
		}
		for j := range c.ContentBlocks {
			if c.ContentBlocks[j].Text != nil {
				if red, n := r.Redact(*c.ContentBlocks[j].Text); n > 0 {
					c.ContentBlocks[j].Text = &red
				}
			}
		}
	}
}
