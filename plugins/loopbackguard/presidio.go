// Presidio connector — the first external-service (sidecar) integration.
//
// Microsoft Presidio (https://github.com/microsoft/presidio) is open source
// (MIT) and self-hosted: you run presidio-analyzer as a container and the
// gateway calls its REST API over localhost. It detects PII via NLP +
// recognizers, catching free-text entities such as PERSON names that the
// pattern-based Redactor (redact.go) cannot.
//
// This connector calls /analyze to get entity spans, then masks them locally in
// Go (no need for the separate anonymizer service). It is a TextTransformer and
// runs in PreRequestHook (the committed-mutation phase). It is FAIL-OPEN: if the
// analyzer is unreachable, the original text is passed through and the error is
// logged as a warning — blocking-on-failure belongs in PreLLMHook and is a
// deliberate follow-up.

package loopbackguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// PresidioEntity is one PII span returned by presidio-analyzer /analyze.
type PresidioEntity struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"` // rune (code-point) offset, inclusive
	End        int     `json:"end"`   // rune offset, exclusive
	Score      float64 `json:"score"`
}

// PresidioClient calls a self-hosted Presidio analyzer.
type PresidioClient struct {
	BaseURL        string // e.g. http://localhost:5002
	Language       string // default "en"
	ScoreThreshold float64
	HTTPClient     *http.Client
}

// NewPresidioClient returns a client with sensible defaults (en, 0.5 threshold,
// 5s timeout).
func NewPresidioClient(baseURL string) *PresidioClient {
	return &PresidioClient{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		Language:       "en",
		ScoreThreshold: 0.5,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Name identifies the transformer. (TextTransformer)
func (c *PresidioClient) Name() string { return "presidio" }

// Analyze posts text to {BaseURL}/analyze and returns detected entities.
func (c *PresidioClient) Analyze(ctx context.Context, text string) ([]PresidioEntity, error) {
	lang := c.Language
	if lang == "" {
		lang = "en"
	}
	payload, err := json.Marshal(map[string]any{
		"text":            text,
		"language":        lang,
		"score_threshold": c.ScoreThreshold,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/analyze", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presidio analyze: unexpected status %d", resp.StatusCode)
	}
	var entities []PresidioEntity
	if err := json.NewDecoder(resp.Body).Decode(&entities); err != nil {
		return nil, fmt.Errorf("presidio analyze: decode: %w", err)
	}
	return entities, nil
}

// Detect reports whether the analyzer finds any entity at or above the score
// threshold, returning the distinct entity types as detail. A transport/analyzer
// error is propagated so the plugin's fail-closed policy can decide. (PIIDetector)
func (c *PresidioClient) Detect(ctx context.Context, text string) (bool, string, error) {
	if strings.TrimSpace(text) == "" {
		return false, "", nil
	}
	entities, err := c.Analyze(ctx, text)
	if err != nil {
		return false, "", err
	}
	seen := map[string]bool{}
	var types []string
	for _, e := range entities {
		if e.Score >= c.ScoreThreshold && !seen[e.EntityType] {
			seen[e.EntityType] = true
			types = append(types, e.EntityType)
		}
	}
	if len(types) == 0 {
		return false, "", nil
	}
	return true, strings.Join(types, ","), nil
}

// Transform analyzes the text and masks every detected entity. On any analyzer
// error it returns the ORIGINAL text plus the error (fail-open). (TextTransformer)
func (c *PresidioClient) Transform(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	entities, err := c.Analyze(ctx, text)
	if err != nil {
		return text, err
	}
	return maskEntities(text, entities, c.ScoreThreshold), nil
}

// maskEntities replaces each entity span (rune offsets) with [REDACTED_<TYPE>],
// processing spans right-to-left so offsets stay valid, and skipping spans that
// overlap one already masked.
func maskEntities(text string, entities []PresidioEntity, threshold float64) string {
	runes := []rune(text)
	n := len(runes)

	valid := make([]PresidioEntity, 0, len(entities))
	for _, e := range entities {
		if e.Score >= threshold && e.Start >= 0 && e.End <= n && e.Start < e.End {
			valid = append(valid, e)
		}
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Start > valid[j].Start })

	lastStart := n + 1
	for _, e := range valid {
		if e.End > lastStart { // overlaps a span already masked
			continue
		}
		token := []rune("[REDACTED_" + e.EntityType + "]")
		tail := append(token, runes[e.End:]...)
		runes = append(runes[:e.Start], tail...)
		lastStart = e.Start
	}
	return string(runes)
}
