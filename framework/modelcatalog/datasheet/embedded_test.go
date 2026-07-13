package datasheet

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// TestLoadEmbeddedCatalog verifies the default pricing source: a store built
// with a zero Config must load the vendored LiteLLM snapshot from the binary
// with no network access, skip the sample_spec template entry, and resolve
// providers from the litellm_provider field.
func TestLoadEmbeddedCatalog(t *testing.T) {
	s := New(nil, nil, Config{})
	if got := s.URL(); got != EmbeddedPricingURL {
		t.Fatalf("default URL = %q, want %q", got, EmbeddedPricingURL)
	}

	if err := s.LoadFromURLIntoMemory(context.Background()); err != nil {
		t.Fatalf("LoadFromURLIntoMemory(embedded): %v", err)
	}

	s.mu.RLock()
	n := len(s.pricingData)
	s.mu.RUnlock()
	if n < 2500 {
		t.Fatalf("embedded catalog loaded %d rows, want >= 2500", n)
	}

	// sample_spec is LiteLLM's template entry and must never become a row.
	for _, key := range []string{
		makeKey("sample_spec", "", "chat"),
		makeKey("sample_spec", "sample_spec", "chat"),
	} {
		s.mu.RLock()
		_, ok := s.pricingData[key]
		s.mu.RUnlock()
		if ok {
			t.Errorf("sample_spec leaked into pricing data as %q", key)
		}
	}

	// Spot-check a stable OpenAI model and an open-weights model: provider
	// must come from litellm_provider and the provider/ key prefix must be
	// stripped from the model name.
	if e := s.Get("gpt-4o", schemas.OpenAI, schemas.ChatCompletionRequest); e == nil {
		t.Errorf("gpt-4o (openai, chat) not found in embedded catalog")
	} else if e.InputCostPerToken == nil || *e.InputCostPerToken <= 0 {
		t.Errorf("gpt-4o has no input cost: %+v", e.InputCostPerToken)
	}
	openWeights := []struct {
		model    string
		provider schemas.ModelProvider
	}{
		{"llama-3.3-70b-versatile", schemas.Groq},
		{"mistral-large-latest", schemas.Mistral},
	}
	for _, tc := range openWeights {
		if e := s.Get(tc.model, tc.provider, schemas.ChatCompletionRequest); e == nil {
			t.Errorf("%s (%s, chat) not found in embedded catalog", tc.model, tc.provider)
		}
	}
}

// TestEntryLitellmProviderFallback verifies both provider field spellings
// decode, with the transformed-datasheet "provider" field winning when both
// are present.
func TestEntryLitellmProviderFallback(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"litellm format", `{"litellm_provider":"groq","mode":"chat"}`, "groq"},
		{"datasheet format", `{"provider":"openai","mode":"chat"}`, "openai"},
		{"both set", `{"provider":"openai","litellm_provider":"groq","mode":"chat"}`, "openai"},
	}
	for _, tc := range cases {
		var e Entry
		if err := json.Unmarshal([]byte(tc.raw), &e); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if e.Provider != tc.want {
			t.Errorf("%s: Provider = %q, want %q", tc.name, e.Provider, tc.want)
		}
	}
}

// TestNormalizeProviderLiteLLMSlugs covers the LiteLLM provider slugs that
// differ from bifrost's canonical names.
func TestNormalizeProviderLiteLLMSlugs(t *testing.T) {
	cases := map[string]string{
		"azure_ai":                  string(schemas.Azure),
		"bedrock_converse":          string(schemas.Bedrock),
		"vertex_ai-language-models": string(schemas.Vertex),
		"fireworks_ai":              string(schemas.Fireworks),
		"groq":                      string(schemas.Groq),
		"ollama":                    string(schemas.Ollama),
	}
	for in, want := range cases {
		if got := normalizeProvider(in); got != want {
			t.Errorf("normalizeProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestModelParamsSyncDisabledByDefault verifies that with a zero Config the
// model-parameters sync is a no-op instead of a fatal network call — the
// air-gapped default must boot without reaching any remote host.
func TestModelParamsSyncDisabledByDefault(t *testing.T) {
	s := New(nil, nil, Config{})
	if got := s.ModelParametersURL(); got != "" {
		t.Fatalf("default ModelParametersURL = %q, want empty (disabled)", got)
	}
	if err := s.LoadModelParamsFromURLIntoMemory(context.Background()); err != nil {
		t.Fatalf("LoadModelParamsFromURLIntoMemory with sync disabled: %v", err)
	}
	if err := s.SyncModelParamsFromURL(context.Background()); err != nil {
		t.Fatalf("SyncModelParamsFromURL with sync disabled: %v", err)
	}
}
