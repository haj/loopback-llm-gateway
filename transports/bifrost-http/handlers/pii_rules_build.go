package handlers

import (
	"sort"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/loopbackguard"
)

// This file converts stored PII rules into the live loopback-guard primitives:
//   - regex rules    -> *loopbackguard.Redactor (redact.go) pushed via SetRedactor
//   - presidio rules -> []loopbackguard.TextTransformer (presidio.go) via SetTransformers
//
// Both run in the plugin's PreRequestHook (the committed-mutation phase), which
// rewrites message text BEFORE the provider call — i.e. true redaction/masking,
// not blocking. (Blocking on PII is a separate concern handled by the plugin's
// PIIDetector path in PreLLMHook; the redactor UI deliberately masks instead.)

// buildRedactorFromRules builds a single ordered Redactor from the enabled regex
// rules. Rules are applied in RuleOrder. Returns (nil, nil) when there are no
// enabled regex rules so the caller disables redaction rather than installing
// the plugin's hardcoded DefaultPIIRules (NewRedactor() with no args would).
// An invalid pattern aborts the build so the caller can reject the change.
func buildRedactorFromRules(rules []configstoreTables.TablePIIRule) (*loopbackguard.Redactor, error) {
	regexRules := make([]configstoreTables.TablePIIRule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled && r.Type == configstoreTables.PIIRuleTypeRegex {
			regexRules = append(regexRules, r)
		}
	}
	if len(regexRules) == 0 {
		return nil, nil
	}
	sort.SliceStable(regexRules, func(i, j int) bool { return regexRules[i].RuleOrder < regexRules[j].RuleOrder })

	built := make([]loopbackguard.RedactRule, 0, len(regexRules))
	for _, r := range regexRules {
		rule, err := loopbackguard.NewRegexReplaceRule(r.Name, r.RegexPattern, r.RegexReplacement)
		if err != nil {
			return nil, err
		}
		built = append(built, rule)
	}
	return loopbackguard.NewRedactor(built...), nil
}

// buildPresidioTransformersFromRules builds one Presidio text transformer per
// distinct analyzer base URL referenced by the enabled presidio rules. The
// PresidioClient masks every entity the analyzer reports at or above its score
// threshold, so per-URL we use the LOWEST threshold among that URL's rules
// (most permissive — catches everything any rule asked for). The PresidioEntity
// type is stored for the UI/grouping but the connector masks all reported
// entities. Returns nil when there are no enabled presidio rules.
func buildPresidioTransformersFromRules(rules []configstoreTables.TablePIIRule) []loopbackguard.TextTransformer {
	// Preserve first-seen URL order for deterministic output.
	order := make([]string, 0)
	minThreshold := make(map[string]float64)
	for _, r := range rules {
		if !r.Enabled || r.Type != configstoreTables.PIIRuleTypePresidio || r.PresidioBaseURL == "" {
			continue
		}
		th := r.PresidioScoreThreshold
		if th <= 0 || th > 1 {
			th = 0.5
		}
		if existing, ok := minThreshold[r.PresidioBaseURL]; ok {
			if th < existing {
				minThreshold[r.PresidioBaseURL] = th
			}
		} else {
			minThreshold[r.PresidioBaseURL] = th
			order = append(order, r.PresidioBaseURL)
		}
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]loopbackguard.TextTransformer, 0, len(order))
	for _, url := range order {
		client := loopbackguard.NewPresidioClient(url)
		client.ScoreThreshold = minThreshold[url]
		out = append(out, client)
	}
	return out
}
