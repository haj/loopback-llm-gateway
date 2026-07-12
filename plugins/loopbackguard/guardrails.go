// This file contains Go ports of Portkey Gateway's self-contained ("default")
// guardrails, which are MIT-licensed (Copyright (c) 2024 Portkey, Inc).
// Source: github.com/Portkey-AI/gateway plugins/default/*.ts
// The detection logic is reimplemented in Go; see THIRD_PARTY_LICENSES.md.
//
// Each guardrail returns a Verdict. Pass == false means the content violated the
// guardrail and the request should be blocked.

package loopbackguard

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
)

// Verdict is the outcome of evaluating a single guardrail against some text.
type Verdict struct {
	Pass        bool   // true = content satisfied the guardrail
	Explanation string // human-readable reason
}

// Guardrail inspects text and returns a Verdict.
type Guardrail interface {
	Name() string
	Check(text string) Verdict
}

// ---- contains (operator any|all|none over substrings) ----

// ContainsOperator selects how Contains evaluates its word list.
type ContainsOperator string

const (
	OpAny  ContainsOperator = "any"  // pass if at least one word is present
	OpAll  ContainsOperator = "all"  // pass if all words are present
	OpNone ContainsOperator = "none" // pass if none of the words are present
)

// Contains passes/fails based on the presence of the configured words.
// A keyword blocklist is Contains{Operator: OpNone, Words: banned}.
type Contains struct {
	Words           []string
	Operator        ContainsOperator
	CaseInsensitive bool // fold case of both text and words before matching
}

func (c Contains) Name() string { return "contains" }

func (c Contains) Check(text string) Verdict {
	hay := text
	if c.CaseInsensitive {
		hay = strings.ToLower(text)
	}
	var found, missing []string
	for _, w := range c.Words {
		if w == "" {
			continue
		}
		needle := w
		if c.CaseInsensitive {
			needle = strings.ToLower(w)
		}
		if strings.Contains(hay, needle) {
			found = append(found, w)
		} else {
			missing = append(missing, w)
		}
	}
	var pass bool
	switch c.Operator {
	case OpAny:
		pass = len(found) > 0
	case OpAll:
		pass = len(missing) == 0
	case OpNone:
		pass = len(found) == 0
	default:
		pass = len(found) == 0 // default to "none" (blocklist) semantics
	}
	return Verdict{
		Pass:        pass,
		Explanation: fmt.Sprintf("contains[%s]: found=%v missing=%v", c.Operator, found, missing),
	}
}

// ---- regexMatch ----

// RegexMatch passes when the pattern matches (or, with Not, when it does not).
type RegexMatch struct {
	re  *regexp.Regexp
	src string
	Not bool
}

// NewRegexMatch compiles pattern; returns an error if the pattern is invalid.
func NewRegexMatch(pattern string, not bool) (*RegexMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("loopbackguard: invalid regex %q: %w", pattern, err)
	}
	return &RegexMatch{re: re, src: pattern, Not: not}, nil
}

func (r *RegexMatch) Name() string { return "regexMatch" }

func (r *RegexMatch) Check(text string) Verdict {
	matched := r.re.MatchString(text)
	pass := matched
	if r.Not {
		pass = !matched
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("regexMatch %q (not=%t): matched=%t", r.src, r.Not, matched)}
}

// ---- wordCount / characterCount / sentenceCount (range checks) ----

// WordCount passes when the whitespace-delimited word count is within [Min,Max].
type WordCount struct {
	Min, Max int
	Not      bool
}

func (w WordCount) Name() string { return "wordCount" }

func (w WordCount) Check(text string) Verdict {
	n := len(strings.Fields(text))
	return rangeVerdict("wordCount", n, w.Min, w.Max, w.Not)
}

// CharacterCount passes when len(text) is within [Min,Max].
type CharacterCount struct {
	Min, Max int
	Not      bool
}

func (c CharacterCount) Name() string { return "characterCount" }

func (c CharacterCount) Check(text string) Verdict {
	return rangeVerdict("characterCount", len(text), c.Min, c.Max, c.Not)
}

// SentenceCount passes when the number of sentences is within [Min,Max].
// Sentences are delimited by '.', '!' or '?', matching Portkey's heuristic.
type SentenceCount struct {
	Min, Max int
	Not      bool
}

func (s SentenceCount) Name() string { return "sentenceCount" }

func (s SentenceCount) Check(text string) Verdict {
	n := strings.Count(text, ".") + strings.Count(text, "!") + strings.Count(text, "?")
	return rangeVerdict("sentenceCount", n, s.Min, s.Max, s.Not)
}

func rangeVerdict(name string, n, min, max int, not bool) Verdict {
	inRange := n >= min && n <= max
	pass := inRange
	if not {
		pass = !inRange
	}
	return Verdict{
		Pass:        pass,
		Explanation: fmt.Sprintf("%s: count=%d range=[%d,%d] not=%t", name, n, min, max, not),
	}
}

// ---- endsWith ----

// EndsWith passes when text ends with Suffix (optionally followed by '.').
type EndsWith struct {
	Suffix string
	Not    bool
}

func (e EndsWith) Name() string { return "endsWith" }

func (e EndsWith) Check(text string) Verdict {
	ew := strings.HasSuffix(text, e.Suffix) || strings.HasSuffix(text, e.Suffix+".")
	pass := ew
	if e.Not {
		pass = !ew
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("endsWith %q (not=%t): %t", e.Suffix, e.Not, ew)}
}

// ---- allLowercase / allUppercase ----

// onlyLetters strips every non-ASCII-letter rune.
func onlyLetters(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return -1
	}, s)
}

// AllLowercase passes when every alphabetic character is lowercase.
type AllLowercase struct{ Not bool }

func (a AllLowercase) Name() string { return "allLowercase" }

func (a AllLowercase) Check(text string) Verdict {
	letters := onlyLetters(text)
	isLower := letters == strings.ToLower(letters)
	pass := isLower
	if a.Not {
		pass = !isLower
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("allLowercase (not=%t): isLower=%t", a.Not, isLower)}
}

// AllUppercase passes when every alphabetic character is uppercase.
type AllUppercase struct{ Not bool }

func (a AllUppercase) Name() string { return "allUppercase" }

func (a AllUppercase) Check(text string) Verdict {
	letters := onlyLetters(text)
	isUpper := letters == strings.ToUpper(letters)
	pass := isUpper
	if a.Not {
		pass = !isUpper
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("allUppercase (not=%t): isUpper=%t", a.Not, isUpper)}
}

// ---- notNull ----

// NotNull passes when the text is non-empty after trimming whitespace.
type NotNull struct{ Not bool }

func (n NotNull) Name() string { return "notNull" }

func (n NotNull) Check(text string) Verdict {
	isNull := strings.TrimSpace(text) == ""
	pass := !isNull
	if n.Not {
		pass = isNull
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("notNull (not=%t): isNull=%t", n.Not, isNull)}
}

// ---- containsCode ----

// codeLanguageMap maps markdown code-fence languages to canonical names,
// ported from Portkey's containsCode language map.
var codeLanguageMap = map[string]string{
	"sql": "SQL", "py": "Python", "python": "Python", "gyp": "Python",
	"ts": "TypeScript", "tsx": "TypeScript", "typescript": "TypeScript",
	"js": "JavaScript", "jsx": "JavaScript", "javascript": "JavaScript",
	"java": "Java", "cs": "C#", "cpp": "C++", "c": "C", "rb": "Ruby",
	"php": "PHP", "swift": "Swift", "kt": "Kotlin", "go": "Go", "rs": "Rust",
	"scala": "Scala", "r": "R", "pl": "Perl", "sh": "Shell", "html": "HTML",
	"css": "CSS", "xml": "XML", "json": "JSON", "yml": "YAML", "md": "Markdown",
	"dockerfile": "Dockerfile",
}

var codeFenceRe = regexp.MustCompile("(?s)```(\\w+)\\n.*?\\n```")

// ContainsCode passes when a fenced code block of the given Format (canonical
// language name, e.g. "Python") is present. With no code blocks at all, the
// verdict equals Not (matching Portkey).
type ContainsCode struct {
	Format string
	Not    bool
}

func (c ContainsCode) Name() string { return "containsCode" }

func (c ContainsCode) Check(text string) Verdict {
	matches := codeFenceRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return Verdict{Pass: c.Not, Explanation: "containsCode: no code blocks found"}
	}
	hasFormat := false
	found := make([]string, 0, len(matches))
	for _, m := range matches {
		lang := strings.ToLower(m[1])
		if canonical, ok := codeLanguageMap[lang]; ok {
			lang = canonical
		}
		found = append(found, lang)
		if lang == c.Format {
			hasFormat = true
		}
	}
	pass := hasFormat
	if c.Not {
		pass = !hasFormat
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("containsCode want=%q found=%v not=%t", c.Format, found, c.Not)}
}

// ---- validUrls ----

var urlRe = regexp.MustCompile(`https?://[^\s,"'{}\[\]]+`)

// ValidURLs passes when every http(s) URL found in the text is syntactically
// valid (scheme + host). With no URLs found, the verdict is false (matching
// Portkey). Note: live DNS/HTTP reachability checks are intentionally NOT done
// here — those require network I/O and belong in a connector, not a pure check.
type ValidURLs struct{ Not bool }

func (v ValidURLs) Name() string { return "validUrls" }

func (v ValidURLs) Check(text string) Verdict {
	urls := urlRe.FindAllString(text, -1)
	if len(urls) == 0 {
		return Verdict{Pass: false, Explanation: "validUrls: no URLs found"}
	}
	allValid := true
	var invalid []string
	for _, raw := range urls {
		u, err := neturl.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			allValid = false
			invalid = append(invalid, raw)
		}
	}
	pass := allValid
	if v.Not {
		pass = !allValid
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("validUrls (not=%t): checked=%d invalid=%v", v.Not, len(urls), invalid)}
}

// ---- validJson / jsonKeys ----

// jsonObjectRe loosely extracts brace-delimited JSON-like substrings.
var jsonObjectRe = regexp.MustCompile(`(?s)\{.*?\}`)
var jsonFenceRe = regexp.MustCompile("(?s)```+(?:json)?\\s*(.*?)```+")

// ValidJSON passes when the whole text (trimmed) parses as JSON.
type ValidJSON struct{ Not bool }

func (j ValidJSON) Name() string { return "validJson" }

func (j ValidJSON) Check(text string) Verdict {
	ok := json.Valid([]byte(strings.TrimSpace(text)))
	pass := ok
	if j.Not {
		pass = !ok
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("validJson (not=%t): valid=%t", j.Not, ok)}
}

// JSONKeys checks that JSON found in the text contains the configured keys,
// evaluated with the any/all/none operator (ported from Portkey jsonKeys).
type JSONKeys struct {
	Keys     []string
	Operator ContainsOperator
}

func (k JSONKeys) Name() string { return "jsonKeys" }

func (k JSONKeys) Check(text string) Verdict {
	candidates := extractJSONCandidates(text)
	if len(candidates) == 0 {
		return Verdict{Pass: false, Explanation: "jsonKeys: no JSON found"}
	}
	// Pick the parsed object with the most matching keys (Portkey's "best match").
	var bestPresent map[string]bool
	bestScore := -1
	for _, c := range candidates {
		var obj map[string]any
		if err := json.Unmarshal([]byte(c), &obj); err != nil {
			continue
		}
		present := make(map[string]bool, len(k.Keys))
		score := 0
		for _, key := range k.Keys {
			if _, ok := obj[key]; ok {
				present[key] = true
				score++
			}
		}
		if score > bestScore {
			bestScore, bestPresent = score, present
		}
	}
	if bestPresent == nil {
		return Verdict{Pass: false, Explanation: "jsonKeys: no parseable JSON object"}
	}
	foundCount := 0
	for _, key := range k.Keys {
		if bestPresent[key] {
			foundCount++
		}
	}
	var pass bool
	switch k.Operator {
	case OpAny:
		pass = foundCount > 0
	case OpAll:
		pass = foundCount == len(k.Keys)
	case OpNone:
		pass = foundCount == 0
	default:
		pass = foundCount == len(k.Keys)
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("jsonKeys[%s]: %d/%d keys present", k.Operator, foundCount, len(k.Keys))}
}

// extractJSONCandidates pulls JSON strings from fenced code blocks and
// brace-delimited substrings, mirroring Portkey's extractJson.
func extractJSONCandidates(text string) []string {
	var out []string
	for _, m := range jsonFenceRe.FindAllStringSubmatch(text, -1) {
		if s := strings.TrimSpace(m[1]); s != "" {
			out = append(out, s)
		}
	}
	out = append(out, jsonObjectRe.FindAllString(text, -1)...)
	return out
}
