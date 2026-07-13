# Loopback Gateway — Actual Status

> **This file is the single source of truth for what actually works today.**
> Other docs ([FEATURE_MATRIX.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/FEATURE_MATRIX.md), [ROADMAP.md](https://github.com/haj/loopback-llm-gateway/blob/main/ROADMAP.md),
> and the [docs/research/](../research/) folder) describe **targets and plans**,
> not shipped functionality. When they disagree with this file, this file wins.
> The wave-by-wave scorecard of everything shipped on top of this baseline is
> **[DELIVERY.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/DELIVERY.md)**.

_Baseline verified: 2026-06-28. Features delivered after this date are tracked in [DELIVERY.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/DELIVERY.md)._

## TL;DR

Loopback Gateway is currently **upstream Bifrost + a thin, honest starting layer**.
The ambitious feature set (250+ providers, 40+ guardrails, 6 agent frameworks) is
**not built yet** — it is the roadmap, not the present.

## What the base is

- **Fork of [Bifrost](https://github.com/maximhq/bifrost)** (Apache-2.0), unmodified upstream.
  - Imported at upstream commit `71310328e` (upstream PR #4677).
  - Go module path still `github.com/maximhq/bifrost/*`, core line `v1.5.22`.
  - This is a **real, working, fast gateway** with ~25 providers, a plugin system,
    a UI, and enterprise features — all inherited from Bifrost.

## What we have actually added

| Item | State | Notes |
|------|-------|-------|
| `go.work` workspace | ✅ Done | Lets the sub-modules build against the local `./core` instead of the published `v1.5.22`. Without it, `framework`/`transports` fail to compile on a fresh clone. |
| `plugins/loopbackguard/` guardrail engine | ✅ Done | A **real, compiling, tested** plugin implementing Bifrost's actual `schemas.LLMPlugin` interface. Runs request-level and text-level guardrails; blocks on the first violation. Ships **15 guardrails ported from Portkey's MIT `plugins/default/`**: text — `contains` (any/all/none), `regexMatch`, `wordCount`, `characterCount`, `sentenceCount`, `endsWith`, `allLowercase`, `allUppercase`, `notNull`, `containsCode`, `validUrls`, `validJson`, `jsonKeys`; request-level — `modelWhitelist`, `allowedRequestTypes`. Plus an **in-process PII/secret redactor** (transforming guardrail) that rewrites message text in `PreRequestHook` (Bifrost's committed-mutation phase): default rules for email/SSN/credit-card/phone/IPv4, plus custom `regexReplace` rules. Plus a **Presidio connector** (`PresidioClient`) — the first external-sidecar integration: calls self-hosted Microsoft Presidio (`/analyze`) for NLP-grade PII (names, locations) and masks spans locally; fail-open; ships a `presidio.docker-compose.yml`. Plus **fail-closed PII blocking** in `PreLLMHook`: a `PIIDetector` pass (regex and/or Presidio) that short-circuits with a 403 when PII is found and, under fail-closed, also when the detector errors (can't verify → deny). 31 passing tests (network tests use httptest mocks). See [`plugins/loopbackguard/README.md`](https://github.com/haj/loopback-llm-gateway/blob/main/plugins/loopbackguard/README.md). |
| [`docs/adr/`](../adr/) | ✅ Done | Records the Go + fork-Bifrost + keep-module-path decisions, and the licensing position. |
| `NOTICE`, `THIRD_PARTY_LICENSES.md` | ✅ Done | Apache-2.0 §4 attribution + MIT notices for code ported later. |
| Quarantined stubs ([`docs/draft-stubs/`](../draft-stubs/)) | ✅ Done | 4 early draft files (~2,200 lines) that referenced **non-existent** Bifrost APIs and never compiled. Kept as design reference only — see that folder's README. |

## What is NOT built (roadmap, not reality)

| Capability | Target | Today |
|------------|--------|-------|
| Providers beyond Bifrost's | 250+ | inherited ~25 only |
| Guardrails | 40+ | 15 self-contained checks + in-process PII redaction (Portkey-derived) + 1 external connector (Presidio, NLP PII); other connectors (Lakera/Aporia/Bedrock) not yet built; jsonSchema + jwt deferred (need a lib/network) |
| Agent frameworks (AutoGen, CrewAI, …) | 6+ | 0 |
| LiteLLM provider adapter / local models | full | none |
| `<10µs` / `10,000+ RPS` headline numbers | targets | unmeasured in this fork |
| Trademark scrub / rebrand off "bifrost" module path | planned | not started |

## How to build & verify what exists

```bash
# Go 1.26 toolchain required (or `nix develop` from the repo root)

# Base builds (workspace mode via go.work):
( cd core      && go build ./... )   # ✅
( cd framework && go build ./... )   # ✅ (needs go.work)
( cd cli       && go build ./... )   # ✅
# transports libraries build; the bifrost-http BINARY also needs the UI built
# first (`//go:embed all:ui`) — a normal frontend build step, run `make`/UI build.

# The real POC plugin builds and passes tests:
( cd plugins/loopbackguard && go build ./... && go test ./... )   # ✅
```

## Honest next step

Grow `plugins/loopbackguard` into the real guardrail set, OR port the first batch of
Portkey providers — each as real, compiling, tested code wired through Bifrost's actual
extension points. Update this file as each lands. Do not mark a capability ✅ anywhere
until it builds and has a test.
