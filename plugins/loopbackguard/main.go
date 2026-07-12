// Package loopbackguard is the Loopback Gateway's first real, wired-in plugin: an
// input-guardrail engine that runs a configurable list of guardrails over the
// chat input and short-circuits when one is violated.
//
// It implements Bifrost's actual plugin contract (schemas.LLMPlugin = BasePlugin
// + the LLM hooks) and short-circuits with a genuine *schemas.BifrostError — no
// invented APIs. The guardrail set (see guardrails.go) is ported from Portkey's
// MIT-licensed self-contained "default" guardrails.
//
// This intentionally replaces the earlier AI-drafted 1,130-line guardrails stub
// (see docs/draft-stubs/) which referenced symbols that do not exist in Bifrost
// (schemas.NewBifrostError, schemas.ErrorCodeGuardrail, schemas.Choice) and never
// compiled. Small but real beats large but fake.
package loopbackguard

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// TextTransformer rewrites message text (e.g. redaction). Implementations may
// call external services, so Transform takes a context. On error, returning the
// original text keeps the pipeline fail-open.
type TextTransformer interface {
	Name() string
	Transform(ctx context.Context, text string) (string, error)
}

// PIIDetector reports whether text contains PII, without mutating it. Used by
// the PreLLMHook blocking pass. detail is a short, loggable summary (e.g. the
// matched entity types). A non-nil err means detection could not be performed —
// the plugin's fail-closed policy decides whether that blocks the request.
type PIIDetector interface {
	Name() string
	Detect(ctx context.Context, text string) (found bool, detail string, err error)
}

// PluginName is the stable identifier returned by GetName().
const PluginName = "loopback-guard"

// DefaultBlockedKeywords is a placeholder blocklist used when Config.BlockedKeywords
// is empty. Replace/extend via configuration for real deployments.
var DefaultBlockedKeywords = []string{"bomb", "malware"}

// Config is the persistence-friendly configuration for the built-in plugin
// entrypoint Init. It is intentionally minimal: an empty Config produces a
// no-op plugin that blocks nothing, so the plugin can be registered as a
// built-in and later configured from the UI. JSON tags mirror the wire format
// used by the Loopback Gateway config store.
//
// Config also drives the convenience keyword-blocklist constructor New().
type Config struct {
	// BlockedKeywords are substrings that, if present in any message, block the request.
	BlockedKeywords []string `json:"blocked_keywords,omitempty"`
	// CaseSensitive controls whether keyword matching is case-sensitive (default: false).
	CaseSensitive bool `json:"case_sensitive,omitempty"`
}

// Plugin implements schemas.LLMPlugin by running request-level and text-level
// guardrails. The request is blocked if any guardrail returns Pass == false.
//
// The guardrail/redactor/PII-detector set can be REPLACED at runtime via the
// Set* methods (e.g. when the UI saves a new configuration). All reads in the
// hooks and all writes in the setters are serialized by mu, so a live request
// always observes a consistent snapshot.
type Plugin struct {
	mu            sync.RWMutex
	guardrails    []Guardrail
	reqGuards     []RequestGuardrail
	redactor      *Redactor
	transformers  []TextTransformer
	piiDetectors  []PIIDetector
	piiFailClosed bool
	logger        schemas.Logger
}

// Compile-time assertion that we satisfy Bifrost's real plugin interface.
var _ schemas.LLMPlugin = (*Plugin)(nil)

// NewPlugin builds a Plugin from an explicit list of text guardrails. Add
// request-level guardrails with WithRequestGuardrails. Text is passed to
// guardrails verbatim; case handling is each guardrail's own concern (e.g.
// Contains.CaseInsensitive).
func NewPlugin(guardrails ...Guardrail) *Plugin {
	return &Plugin{guardrails: guardrails}
}

// WithRequestGuardrails attaches request-level guardrails (model whitelist,
// allowed request types, ...) and returns the plugin for chaining.
func (p *Plugin) WithRequestGuardrails(g ...RequestGuardrail) *Plugin {
	p.reqGuards = append(p.reqGuards, g...)
	return p
}

// WithRedactor attaches a PII/secret redactor that rewrites message text in
// PreRequestHook (before the provider call). Returns the plugin for chaining.
func (p *Plugin) WithRedactor(r *Redactor) *Plugin {
	p.redactor = r
	return p
}

// WithTransformers attaches text transformers (e.g. the Presidio connector) that
// rewrite message text in PreRequestHook, after the local redactor. Returns the
// plugin for chaining.
func (p *Plugin) WithTransformers(t ...TextTransformer) *Plugin {
	p.transformers = append(p.transformers, t...)
	return p
}

// WithPIIBlocking attaches detectors that BLOCK (403) the request in PreLLMHook
// when PII is found. When failClosed is true, a detector error also blocks (the
// request is denied because PII could not be verified); when false, a detector
// error is logged and skipped (fail-open).
//
// Note: PreRequestHook redaction runs BEFORE this blocking pass, so do not
// redact the same PII you intend to block — redaction would mask it first and
// the block would never fire. Blocking and redaction are alternative policies.
func (p *Plugin) WithPIIBlocking(failClosed bool, detectors ...PIIDetector) *Plugin {
	p.piiFailClosed = failClosed
	p.piiDetectors = append(p.piiDetectors, detectors...)
	return p
}

// New is a convenience constructor that builds a keyword blocklist guardrail
// (Contains with the "none" operator) from cfg, falling back to
// DefaultBlockedKeywords. Retained for the simple "ban these words" case.
func New(cfg Config) *Plugin {
	kw := cfg.BlockedKeywords
	if len(kw) == 0 {
		kw = DefaultBlockedKeywords
	}
	words := make([]string, 0, len(kw))
	for _, k := range kw {
		if k != "" {
			words = append(words, k)
		}
	}
	return NewPlugin(Contains{
		Words:           words,
		Operator:        OpNone,
		CaseInsensitive: !cfg.CaseSensitive,
	})
}

// Init is the config-driven, built-in entrypoint used by the Loopback Gateway
// server to register loopbackguard as a built-in plugin. It returns a plugin
// whose policy can be replaced at runtime via the Set* methods.
//
// An empty (or nil) config yields a no-op plugin that blocks nothing — this is
// the safe default so registering the built-in never changes request behavior
// until a real policy is configured from the UI. When config.BlockedKeywords is
// non-empty, a keyword blocklist guardrail is installed (unlike New, Init does
// NOT fall back to DefaultBlockedKeywords).
func Init(config *Config, logger schemas.Logger) (schemas.LLMPlugin, error) {
	p := &Plugin{logger: logger}
	if config != nil && len(config.BlockedKeywords) > 0 {
		words := make([]string, 0, len(config.BlockedKeywords))
		for _, k := range config.BlockedKeywords {
			if k != "" {
				words = append(words, k)
			}
		}
		if len(words) > 0 {
			p.guardrails = []Guardrail{Contains{
				Words:           words,
				Operator:        OpNone,
				CaseInsensitive: !config.CaseSensitive,
			}}
		}
	}
	return p, nil
}

// SetGuardrails replaces the text-level guardrail set under the write lock.
func (p *Plugin) SetGuardrails(guardrails []Guardrail) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.guardrails = guardrails
}

// SetRequestGuardrails replaces the request-level guardrail set under the write lock.
func (p *Plugin) SetRequestGuardrails(reqGuards ...RequestGuardrail) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqGuards = reqGuards
}

// SetRedactor replaces the PreRequestHook redactor under the write lock. Pass
// nil to disable redaction.
func (p *Plugin) SetRedactor(r *Redactor) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.redactor = r
}

// SetTransformers replaces the text transformers under the write lock.
func (p *Plugin) SetTransformers(t ...TextTransformer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transformers = t
}

// SetPIIDetectors replaces the PII-blocking detector set and fail-closed policy
// under the write lock.
func (p *Plugin) SetPIIDetectors(failClosed bool, detectors ...PIIDetector) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.piiFailClosed = failClosed
	p.piiDetectors = detectors
}

// snapshot returns a consistent, lock-free copy of the policy fields the hooks
// need, taken under the read lock. Slices are copied so a concurrent Set* call
// cannot mutate what an in-flight request is iterating.
func (p *Plugin) snapshot() (guardrails []Guardrail, reqGuards []RequestGuardrail, redactor *Redactor, transformers []TextTransformer, piiDetectors []PIIDetector, piiFailClosed bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	guardrails = append([]Guardrail(nil), p.guardrails...)
	reqGuards = append([]RequestGuardrail(nil), p.reqGuards...)
	redactor = p.redactor
	transformers = append([]TextTransformer(nil), p.transformers...)
	piiDetectors = append([]PIIDetector(nil), p.piiDetectors...)
	piiFailClosed = p.piiFailClosed
	return
}

// GetName returns the plugin name. (BasePlugin)
func (p *Plugin) GetName() string { return PluginName }

// Cleanup releases resources on shutdown. (BasePlugin)
func (p *Plugin) Cleanup() error { return nil }

// PreRequestHook redacts PII/secrets from the chat input. This is Bifrost's
// committed-mutation phase, so the rewritten text reaches the provider and all
// fallbacks. No-op when no redactor is configured. (LLMPlugin)
func (p *Plugin) PreRequestHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	if req == nil || req.ChatRequest == nil {
		return nil
	}
	_, _, redactor, transformers, _, _ := p.snapshot()
	if redactor != nil {
		redactMessages(req.ChatRequest.Input, redactor)
	}
	if len(transformers) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return applyTransformers(ctx, transformers, req.ChatRequest.Input)
	}
	return nil
}

// applyTransformers runs every transformer over every message's text. It is
// fail-open: a transformer error leaves that text unchanged and is returned
// (Bifrost logs it as a warning) without aborting the request.
func applyTransformers(ctx context.Context, transformers []TextTransformer, msgs []schemas.ChatMessage) error {
	var firstErr error
	apply := func(s string) string {
		for _, t := range transformers {
			out, err := t.Transform(ctx, s)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			s = out
		}
		return s
	}
	for i := range msgs {
		c := msgs[i].Content
		if c == nil {
			continue
		}
		if c.ContentStr != nil {
			out := apply(*c.ContentStr)
			c.ContentStr = &out
		}
		for j := range c.ContentBlocks {
			if c.ContentBlocks[j].Text != nil {
				out := apply(*c.ContentBlocks[j].Text)
				c.ContentBlocks[j].Text = &out
			}
		}
	}
	return firstErr
}

// PreLLMHook runs every guardrail over the chat input and short-circuits with a
// 403 BifrostError on the first violation; otherwise passes the request through.
func (p *Plugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if req == nil {
		return req, nil, nil
	}

	guardrails, reqGuards, _, _, piiDetectors, piiFailClosed := p.snapshot()

	// Request-level guardrails (model, request type) run regardless of payload.
	for _, g := range reqGuards {
		if v := g.CheckRequest(req); !v.Pass {
			return req, p.block(req, g.Name(), v.Explanation), nil
		}
	}

	// Text-level checks apply to chat input only.
	if req.ChatRequest == nil {
		return req, nil, nil
	}
	text := extractText(req.ChatRequest.Input)

	// PII blocking pass (fail-closed configurable).
	if len(piiDetectors) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, d := range piiDetectors {
			found, detail, err := d.Detect(ctx, text)
			if err != nil {
				if piiFailClosed {
					return req, p.block(req, d.Name()+" (fail-closed)", "PII could not be verified: "+err.Error()), nil
				}
				continue // fail-open: skip this detector
			}
			if found {
				return req, p.block(req, d.Name(), "PII detected: "+detail), nil
			}
		}
	}

	// Text-level guardrails.
	for _, g := range guardrails {
		if v := g.Check(text); !v.Pass {
			return req, p.block(req, g.Name(), v.Explanation), nil
		}
	}
	return req, nil, nil
}

// PostLLMHook passes the response through unchanged. (LLMPlugin)
func (p *Plugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

// block builds a short-circuit 403 error naming the guardrail that fired.
func (p *Plugin) block(req *schemas.BifrostRequest, guardrail, explanation string) *schemas.LLMPluginShortCircuit {
	provider, model, _ := req.GetRequestFields()
	status := http.StatusForbidden
	errType := "loopback_guardrail_blocked"
	noFallback := false
	return &schemas.LLMPluginShortCircuit{
		Error: &schemas.BifrostError{
			IsBifrostError: true,
			StatusCode:     &status,
			Error: &schemas.ErrorField{
				Type:    &errType,
				Message: "request blocked by " + PluginName + " guardrail " + guardrail + ": " + explanation,
			},
			AllowFallbacks: &noFallback,
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            req.RequestType,
				Provider:               provider,
				OriginalModelRequested: model,
			},
		},
	}
}

// extractText concatenates all message text (string content and text blocks)
// verbatim. Case handling is left to individual guardrails.
func extractText(msgs []schemas.ChatMessage) string {
	var b strings.Builder
	for i := range msgs {
		content := msgs[i].Content
		if content == nil {
			continue
		}
		if content.ContentStr != nil {
			b.WriteString(*content.ContentStr)
			b.WriteByte('\n')
		}
		for _, block := range content.ContentBlocks {
			if block.Text != nil {
				b.WriteString(*block.Text)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
