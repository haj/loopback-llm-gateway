package handlers

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/loopbackguard"
)

// guardrailTypeKey normalizes a guardrail type identifier so the API accepts
// both the canonical lowerCamel form returned by the plugin (e.g. "regexMatch")
// and the PascalCase form used in some docs (e.g. "RegexMatch"). We compare on
// the fully lowercased, separator-stripped form.
func guardrailTypeKey(t string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(t))
}

// knownGuardrailTypes is the set of the 15 supported guardrail type keys.
var knownGuardrailTypes = map[string]bool{
	"contains":            true,
	"regexmatch":          true,
	"wordcount":           true,
	"charactercount":      true,
	"sentencecount":       true,
	"endswith":            true,
	"alllowercase":        true,
	"alluppercase":        true,
	"notnull":             true,
	"containscode":        true,
	"validurls":           true,
	"validjson":           true,
	"jsonkeys":            true,
	"modelwhitelist":      true,
	"allowedrequesttypes": true,
}

// isKnownGuardrailType reports whether t names a supported guardrail type.
func isKnownGuardrailType(t string) bool {
	return knownGuardrailTypes[guardrailTypeKey(t)]
}

// buildGuardrailsFromItems converts stored guardrail items into live
// loopback-guard guardrails, partitioned into text-level and request-level
// lists. Disabled items are skipped. An error in any item (e.g. an invalid
// regex) aborts the whole build so the caller can reject the configuration.
func buildGuardrailsFromItems(items []configstoreTables.GuardrailItem) ([]loopbackguard.Guardrail, []loopbackguard.RequestGuardrail, error) {
	var text []loopbackguard.Guardrail
	var reqs []loopbackguard.RequestGuardrail
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		g, rg, err := buildOne(item)
		if err != nil {
			return nil, nil, err
		}
		if g != nil {
			text = append(text, g)
		}
		if rg != nil {
			reqs = append(reqs, rg)
		}
	}
	return text, reqs, nil
}

func buildOne(item configstoreTables.GuardrailItem) (loopbackguard.Guardrail, loopbackguard.RequestGuardrail, error) {
	p := params(item.Params)
	switch guardrailTypeKey(item.Type) {
	case "contains":
		return loopbackguard.Contains{
			Words:           p.stringSlice("words"),
			Operator:        containsOperator(p.str("operator")),
			CaseInsensitive: p.boolDefault("caseInsensitive", true),
		}, nil, nil
	case "regexmatch":
		re, err := loopbackguard.NewRegexMatch(p.str("pattern"), p.boolDefault("not", false))
		if err != nil {
			return nil, nil, err
		}
		return re, nil, nil
	case "wordcount":
		return loopbackguard.WordCount{Min: p.intDefault("min", 0), Max: p.intDefault("max", maxInt), Not: p.boolDefault("not", false)}, nil, nil
	case "charactercount":
		return loopbackguard.CharacterCount{Min: p.intDefault("min", 0), Max: p.intDefault("max", maxInt), Not: p.boolDefault("not", false)}, nil, nil
	case "sentencecount":
		return loopbackguard.SentenceCount{Min: p.intDefault("min", 0), Max: p.intDefault("max", maxInt), Not: p.boolDefault("not", false)}, nil, nil
	case "endswith":
		return loopbackguard.EndsWith{Suffix: p.str("suffix"), Not: p.boolDefault("not", false)}, nil, nil
	case "alllowercase":
		return loopbackguard.AllLowercase{Not: p.boolDefault("not", false)}, nil, nil
	case "alluppercase":
		return loopbackguard.AllUppercase{Not: p.boolDefault("not", false)}, nil, nil
	case "notnull":
		return loopbackguard.NotNull{Not: p.boolDefault("not", false)}, nil, nil
	case "containscode":
		return loopbackguard.ContainsCode{Format: p.str("format"), Not: p.boolDefault("not", false)}, nil, nil
	case "validurls":
		return loopbackguard.ValidURLs{Not: p.boolDefault("not", false)}, nil, nil
	case "validjson":
		return loopbackguard.ValidJSON{Not: p.boolDefault("not", false)}, nil, nil
	case "jsonkeys":
		return loopbackguard.JSONKeys{Keys: p.stringSlice("keys"), Operator: containsOperator(p.str("operator"))}, nil, nil
	case "modelwhitelist":
		return nil, loopbackguard.ModelWhitelist{Models: p.stringSlice("models"), Not: p.boolDefault("not", false)}, nil
	case "allowedrequesttypes":
		return nil, loopbackguard.AllowedRequestTypes{
			Allowed: requestTypes(p.stringSlice("allowed")),
			Blocked: requestTypes(p.stringSlice("blocked")),
		}, nil
	default:
		return nil, nil, fmt.Errorf("unknown guardrail type %q", item.Type)
	}
}

const maxInt = int(^uint(0) >> 1)

func containsOperator(s string) loopbackguard.ContainsOperator {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "any":
		return loopbackguard.OpAny
	case "all":
		return loopbackguard.OpAll
	case "none":
		return loopbackguard.OpNone
	default:
		return loopbackguard.OpNone
	}
}

func requestTypes(vals []string) []schemas.RequestType {
	if len(vals) == 0 {
		return nil
	}
	out := make([]schemas.RequestType, 0, len(vals))
	for _, v := range vals {
		out = append(out, schemas.RequestType(v))
	}
	return out
}

// params wraps a polymorphic param map with typed, defaulting accessors.
type params map[string]any

func (p params) str(key string) string {
	if v, ok := p[key].(string); ok {
		return v
	}
	return ""
}

func (p params) boolDefault(key string, def bool) bool {
	if v, ok := p[key].(bool); ok {
		return v
	}
	return def
}

// intDefault tolerates JSON numbers (float64) and numeric strings.
func (p params) intDefault(key string, def int) int {
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return def
}

func (p params) stringSlice(key string) []string {
	raw, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
