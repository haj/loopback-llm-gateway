package loopbackguard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockAnalyzer returns a Presidio-like /analyze server that emits the given
// entities for any request.
func mockAnalyzer(t *testing.T, entities []PresidioEntity) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analyze" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entities)
	}))
}

func TestMaskEntities(t *testing.T) {
	// "John Smith lives in Paris" -> PERSON 0-10, LOCATION 20-25
	text := "John Smith lives in Paris"
	ents := []PresidioEntity{
		{EntityType: "PERSON", Start: 0, End: 10, Score: 0.9},
		{EntityType: "LOCATION", Start: 20, End: 25, Score: 0.85},
	}
	got := maskEntities(text, ents, 0.5)
	if strings.Contains(got, "John Smith") || strings.Contains(got, "Paris") {
		t.Errorf("entities not masked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED_PERSON]") || !strings.Contains(got, "[REDACTED_LOCATION]") {
		t.Errorf("missing tokens: %q", got)
	}
}

func TestMaskEntities_ThresholdAndOverlap(t *testing.T) {
	text := "hello world"
	// low score is ignored; overlapping span is skipped
	ents := []PresidioEntity{
		{EntityType: "X", Start: 0, End: 5, Score: 0.2},  // below threshold
		{EntityType: "A", Start: 6, End: 11, Score: 0.9}, // "world"
		{EntityType: "B", Start: 6, End: 9, Score: 0.9},  // overlaps A
	}
	got := maskEntities(text, ents, 0.5)
	if got != "hello [REDACTED_A]" {
		t.Errorf("unexpected mask result: %q", got)
	}
}

func TestPresidioClient_Transform(t *testing.T) {
	srv := mockAnalyzer(t, []PresidioEntity{{EntityType: "PERSON", Start: 0, End: 4, Score: 0.99}})
	defer srv.Close()

	c := NewPresidioClient(srv.URL)
	out, err := c.Transform(context.Background(), "Jane went to the park")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Jane") || !strings.HasPrefix(out, "[REDACTED_PERSON]") {
		t.Errorf("expected name redacted, got %q", out)
	}
}

func TestPresidioClient_FailOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewPresidioClient(srv.URL)
	in := "Jane went to the park"
	out, err := c.Transform(context.Background(), in)
	if err == nil {
		t.Error("expected an error when analyzer fails")
	}
	if out != in {
		t.Errorf("fail-open must return original text, got %q", out)
	}
}

func TestPlugin_PresidioTransformer_Wired(t *testing.T) {
	srv := mockAnalyzer(t, []PresidioEntity{{EntityType: "PERSON", Start: 0, End: 4, Score: 0.99}})
	defer srv.Close()

	p := NewPlugin().WithTransformers(NewPresidioClient(srv.URL))
	req := chatReq("Jane is here")
	if err := p.PreRequestHook(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := *req.ChatRequest.Input[0].Content.ContentStr
	if strings.Contains(got, "Jane") || !strings.Contains(got, "[REDACTED_PERSON]") {
		t.Errorf("transformer not applied to committed request: %q", got)
	}
}
