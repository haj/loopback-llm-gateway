# Draft stubs — DO NOT BUILD, reference only

These `.go.txt` files were AI-drafted earlier as a first attempt at the Loopback
feature layer. They are kept **only as design reference**. They are deliberately
suffixed `.go.txt` so the Go toolchain ignores them.

## Why they are quarantined

They do **not compile** and were **never wired into the gateway**. They reference
Bifrost APIs that do not exist (hallucinated against an approximate SDK):

| Used in stubs | Reality in Bifrost |
|---------------|--------------------|
| `schemas.NewBifrostError(...)` | no such constructor — errors are `&schemas.BifrostError{...}` literals |
| `schemas.ErrorCodeGuardrail` | no `ErrorCode*` constants exist in `schemas` |
| `schemas.Choice` | the type is `schemas.BifrostChatResponseChoice` |

Nothing in the repo imported these packages, so they were dead code as well as
broken code.

## Files

| File | Was | Original size |
|------|-----|---------------|
| `loopback-provider.go.txt` | `core/providers/loopback-provider.go` | ~537 lines |
| `loopback-openai.go.txt` | `core/providers/loopback-openai/loopback-openai.go` | ~127 lines |
| `loopback-guardrails.go.txt` | `core/guardrails/loopback-guardrails.go` | ~1,130 lines |
| `agent-integrations.go.txt` | `core/agents/agent-integrations.go` | ~392 lines |

## What replaces them

A small but **real** plugin: `plugins/loopbackguard/`. It implements Bifrost's
actual `schemas.LLMPlugin` interface, compiles, and has tests. Mine these drafts
for ideas (guardrail categories, provider-wrapper shape) but re-implement against
the real API — do not un-quarantine them as-is.
