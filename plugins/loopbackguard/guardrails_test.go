package loopbackguard

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestContains(t *testing.T) {
	cases := []struct {
		op       ContainsOperator
		words    []string
		text     string
		wantPass bool
	}{
		{OpNone, []string{"bomb"}, "how to build a bomb", false},
		{OpNone, []string{"bomb"}, "how to build a house", true},
		{OpAny, []string{"cat", "dog"}, "i have a dog", true},
		{OpAny, []string{"cat", "dog"}, "i have a fish", false},
		{OpAll, []string{"cat", "dog"}, "cat and dog", true},
		{OpAll, []string{"cat", "dog"}, "only a cat", false},
	}
	for _, c := range cases {
		got := Contains{Words: c.words, Operator: c.op}.Check(c.text).Pass
		if got != c.wantPass {
			t.Errorf("Contains{%s,%v}.Check(%q).Pass = %t, want %t", c.op, c.words, c.text, got, c.wantPass)
		}
	}
}

func TestRegexMatch(t *testing.T) {
	g, err := NewRegexMatch(`\d{3}-\d{2}-\d{4}`, true) // Not: block when SSN-like pattern matches
	if err != nil {
		t.Fatal(err)
	}
	if g.Check("my ssn is 123-45-6789").Pass {
		t.Error("expected SSN-like text to fail the Not regex guardrail")
	}
	if !g.Check("no numbers here").Pass {
		t.Error("expected clean text to pass")
	}
	if _, err := NewRegexMatch("(", false); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestWordAndCharAndSentenceCount(t *testing.T) {
	if !(WordCount{Min: 1, Max: 3}).Check("one two three").Pass {
		t.Error("3 words should be in [1,3]")
	}
	if (WordCount{Min: 1, Max: 3}).Check("one two three four").Pass {
		t.Error("4 words should be out of [1,3]")
	}
	if !(CharacterCount{Min: 0, Max: 5}).Check("abc").Pass {
		t.Error("3 chars should be in [0,5]")
	}
	if (CharacterCount{Min: 0, Max: 5}).Check("abcdef").Pass {
		t.Error("6 chars should be out of [0,5]")
	}
	if !(SentenceCount{Min: 2, Max: 2}).Check("Hi there. How are you?").Pass {
		t.Error("expected 2 sentences")
	}
}

func TestEndsWith(t *testing.T) {
	if !(EndsWith{Suffix: "}"}).Check("{\"ok\":true}").Pass {
		t.Error("should end with }")
	}
	if !(EndsWith{Suffix: "done"}).Check("all done.").Pass {
		t.Error("should accept trailing period")
	}
	if (EndsWith{Suffix: "done"}).Check("not finished").Pass {
		t.Error("should fail when suffix absent")
	}
}

func TestPlugin_MultipleGuardrails(t *testing.T) {
	ssn, _ := NewRegexMatch(`\d{3}-\d{2}-\d{4}`, true)
	p := NewPlugin(
		Contains{Words: []string{"forbidden"}, Operator: OpNone, CaseInsensitive: true},
		ssn,
	)
	// blocked by keyword
	if _, sc, _ := p.PreLLMHook(nil, chatReq("the FORBIDDEN word")); sc == nil {
		t.Error("expected keyword guardrail to block")
	}
	// blocked by regex
	if _, sc, _ := p.PreLLMHook(nil, chatReq("ssn 123-45-6789")); sc == nil {
		t.Error("expected regex guardrail to block")
	}
	// clean passes
	if _, sc, _ := p.PreLLMHook(nil, chatReq("hello world")); sc != nil {
		t.Error("expected clean request to pass all guardrails")
	}
}

func TestCaseGuardrails(t *testing.T) {
	if !(AllLowercase{}).Check("hello, world! 123").Pass {
		t.Error("expected all-lowercase text to pass")
	}
	if (AllLowercase{}).Check("Hello").Pass {
		t.Error("expected mixed case to fail allLowercase")
	}
	if !(AllUppercase{}).Check("HELLO! 123").Pass {
		t.Error("expected all-uppercase to pass")
	}
}

func TestNotNull(t *testing.T) {
	if !(NotNull{}).Check("x").Pass {
		t.Error("non-empty should pass notNull")
	}
	if (NotNull{}).Check("   ").Pass {
		t.Error("whitespace-only should fail notNull")
	}
}

func TestContainsCode(t *testing.T) {
	withGo := "here:\n```go\nfmt.Println(1)\n```\n"
	if !(ContainsCode{Format: "Go"}).Check(withGo).Pass {
		t.Error("expected Go code block to be detected")
	}
	if (ContainsCode{Format: "Python"}).Check(withGo).Pass {
		t.Error("expected Python check to fail on Go-only block")
	}
	// Not: block when any code present
	if (ContainsCode{Format: "Go", Not: true}).Check(withGo).Pass {
		t.Error("Not should fail when target code present")
	}
}

func TestValidURLs(t *testing.T) {
	if !(ValidURLs{}).Check("see https://example.com/path here").Pass {
		t.Error("valid URL should pass")
	}
	if (ValidURLs{}).Check("no urls here").Pass {
		t.Error("no URLs should fail (Portkey semantics)")
	}
}

func TestValidJSONAndKeys(t *testing.T) {
	if !(ValidJSON{}).Check(`{"a":1,"b":2}`).Pass {
		t.Error("valid JSON should pass")
	}
	if (ValidJSON{}).Check(`{not json}`).Pass {
		t.Error("invalid JSON should fail")
	}
	body := "result:\n```json\n{\"name\":\"x\",\"age\":3}\n```"
	if !(JSONKeys{Keys: []string{"name", "age"}, Operator: OpAll}).Check(body).Pass {
		t.Error("expected all keys present")
	}
	if (JSONKeys{Keys: []string{"name", "missing"}, Operator: OpAll}).Check(body).Pass {
		t.Error("expected missing key to fail OpAll")
	}
	if !(JSONKeys{Keys: []string{"name", "missing"}, Operator: OpAny}).Check(body).Pass {
		t.Error("expected OpAny to pass with one key present")
	}
}

func TestRequestGuardrails(t *testing.T) {
	allowed := chatReq("hi")        // model "gpt-4"
	allowed.ChatRequest.Model = "gpt-4"
	mw := ModelWhitelist{Models: []string{"gpt-4", "claude-3"}}
	if v := mw.CheckRequest(allowed); !v.Pass {
		t.Errorf("gpt-4 should be whitelisted: %s", v.Explanation)
	}
	denied := chatReq("hi")
	denied.ChatRequest.Model = "gpt-3.5"
	if mw.CheckRequest(denied).Pass {
		t.Error("gpt-3.5 should be rejected")
	}

	art := AllowedRequestTypes{Blocked: []schemas.RequestType{schemas.EmbeddingRequest}}
	if !art.CheckRequest(allowed).Pass {
		t.Error("chat request should be allowed when only embeddings blocked")
	}

	// wired through the plugin
	p := NewPlugin().WithRequestGuardrails(mw)
	if _, sc, _ := p.PreLLMHook(nil, denied); sc == nil {
		t.Error("plugin should block non-whitelisted model")
	}
	if _, sc, _ := p.PreLLMHook(nil, allowed); sc != nil {
		t.Error("plugin should allow whitelisted model")
	}
}
