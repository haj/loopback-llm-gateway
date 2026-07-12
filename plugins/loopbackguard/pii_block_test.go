package loopbackguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactorDetect(t *testing.T) {
	r := NewRedactor()
	found, detail, err := r.Detect(context.Background(), "email a@b.com and ssn 123-45-6789")
	if err != nil || !found {
		t.Fatalf("expected PII found, got found=%t err=%v", found, err)
	}
	if !strings.Contains(detail, "email") || !strings.Contains(detail, "ssn") {
		t.Errorf("detail should list matched labels, got %q", detail)
	}
	if f, _, _ := r.Detect(context.Background(), "nothing sensitive"); f {
		t.Error("clean text should not be detected as PII")
	}
}

func TestPreLLMHook_BlocksOnRegexPII(t *testing.T) {
	p := NewPlugin().WithPIIBlocking(true, NewRedactor())
	_, sc, err := p.PreLLMHook(nil, chatReq("my ssn is 123-45-6789"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc == nil || sc.Error == nil {
		t.Fatal("expected request with PII to be blocked")
	}
	if !strings.Contains(sc.Error.Error.Message, "PII detected") {
		t.Errorf("unexpected block message: %q", sc.Error.Error.Message)
	}
	// clean request passes
	if _, sc, _ := p.PreLLMHook(nil, chatReq("hello there")); sc != nil {
		t.Error("clean request should pass")
	}
}

func TestPreLLMHook_FailClosedBlocksOnDetectorError(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	// fail-closed: detector error must block.
	p := NewPlugin().WithPIIBlocking(true, NewPresidioClient(down.URL))
	_, sc, _ := p.PreLLMHook(nil, chatReq("Jane Doe was here"))
	if sc == nil || sc.Error == nil {
		t.Fatal("fail-closed: unreachable detector must block the request")
	}
	if !strings.Contains(sc.Error.Error.Message, "fail-closed") {
		t.Errorf("expected fail-closed message, got %q", sc.Error.Error.Message)
	}
}

func TestPreLLMHook_FailOpenPassesOnDetectorError(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	// fail-open: detector error must NOT block.
	p := NewPlugin().WithPIIBlocking(false, NewPresidioClient(down.URL))
	if _, sc, _ := p.PreLLMHook(nil, chatReq("Jane Doe was here")); sc != nil {
		t.Error("fail-open: unreachable detector must not block")
	}
}

func TestPreLLMHook_PresidioDetectBlocks(t *testing.T) {
	srv := mockAnalyzer(t, []PresidioEntity{{EntityType: "PERSON", Start: 0, End: 4, Score: 0.99}})
	defer srv.Close()

	p := NewPlugin().WithPIIBlocking(true, NewPresidioClient(srv.URL))
	_, sc, _ := p.PreLLMHook(nil, chatReq("Jane is here"))
	if sc == nil {
		t.Fatal("expected Presidio-detected PII to block")
	}
	if !strings.Contains(sc.Error.Error.Message, "PERSON") {
		t.Errorf("expected entity type in message, got %q", sc.Error.Error.Message)
	}
}
