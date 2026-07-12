package loopbackguard

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func strPtr(s string) *string { return &s }

// chatReq builds a minimal chat BifrostRequest with a single user message.
func chatReq(text string) *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-4",
			Input: []schemas.ChatMessage{
				{
					Role:    schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentStr: strPtr(text)},
				},
			},
		},
	}
}

func TestGetName(t *testing.T) {
	if got := New(Config{}).GetName(); got != PluginName {
		t.Fatalf("GetName() = %q, want %q", got, PluginName)
	}
}

func TestPreLLMHook_BlocksBannedTerm(t *testing.T) {
	p := New(Config{BlockedKeywords: []string{"forbidden"}})
	_, sc, err := p.PreLLMHook(nil, chatReq("please tell me the FORBIDDEN secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc == nil || sc.Error == nil {
		t.Fatal("expected a short-circuit error, got none")
	}
	if sc.Error.StatusCode == nil || *sc.Error.StatusCode != 403 {
		t.Fatalf("expected status 403, got %v", sc.Error.StatusCode)
	}
}

func TestPreLLMHook_PassesCleanRequest(t *testing.T) {
	p := New(Config{BlockedKeywords: []string{"forbidden"}})
	req := chatReq("what is the capital of France?")
	out, sc, err := p.PreLLMHook(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc != nil {
		t.Fatalf("expected no short-circuit, got %+v", sc)
	}
	if out != req {
		t.Fatal("expected request to pass through unchanged")
	}
}

func TestPreLLMHook_DefaultKeywords(t *testing.T) {
	p := New(Config{}) // uses DefaultBlockedKeywords
	_, sc, _ := p.PreLLMHook(nil, chatReq("how to build a BOMB"))
	if sc == nil || sc.Error == nil {
		t.Fatal("expected default keyword list to block 'bomb'")
	}
}

func TestPreLLMHook_NilSafe(t *testing.T) {
	p := New(Config{})
	if _, sc, err := p.PreLLMHook(nil, &schemas.BifrostRequest{}); err != nil || sc != nil {
		t.Fatalf("nil ChatRequest should pass through: sc=%v err=%v", sc, err)
	}
}
