# ADR 0001 — Language = Go, base = fork Bifrost, keep upstream module path

- Status: Accepted
- Date: 2026-06-28

## Context

Loopback Gateway aims to combine the strengths of three open-source AI gateways:
Bifrost (Go), Portkey (TypeScript), and LiteLLM (Python). Two foundational
questions had to be settled before feature work: **which language**, and
**build from scratch or fork**.

## Decision

1. **Language: Go.** Keep building on the Bifrost fork.
2. **Base: fork Bifrost** (Apache-2.0) rather than Portkey or LiteLLM.
3. **Module path: keep `github.com/maximhq/bifrost/*` for now.** Do **not**
   mass-rename across the ~900 Go files yet.

## Rationale

### Why Go (vs Rust / Python)

An AI gateway is **I/O-bound**: it accepts a request, transforms JSON, forwards
to an upstream provider, and streams the response back. The gateway's own
overhead is microseconds (Bifrost cites ~11µs); the upstream LLM call is
**100ms–10s+**. So the proxy's language has a negligible effect on user-visible
latency — the bottleneck is always the provider.

- **Rust**: marginally lower tail latency and no GC, but the gain is largely
  theoretical for I/O-bound proxying, and there is no mature Rust gateway to
  fork — choosing it means rebuilding Bifrost's providers, UI, plugin system,
  and enterprise features from zero. Not worth discarding a working base.
  Escape hatch: any genuinely CPU-bound module (custom tokenizer, vector math)
  can later be written in Rust and loaded via Bifrost's **WASM/`.so` plugin**
  support — no rewrite required.
- **Python**: easiest ecosystem, but GIL + interpreter overhead make it the
  weakest choice for a high-concurrency, low-latency gateway — which is exactly
  what LiteLLM already is. Building a Python gateway would mean re-implementing
  LiteLLM. Contradicts the stated "performance-first" goal.
- **Go**: goroutines are ideal for many concurrent streaming connections;
  Bifrost is already a working, fast, feature-rich Go base. Choosing Go costs
  nothing and inherits everything.

### Why keep the upstream module path (for now)

The whole strategy depends on continuing to **merge improvements from upstream
Bifrost**. Renaming the module path across ~900 files breaks clean merges,
provides zero functional benefit, and risks breaking the build. Defer any
rename until the fork has meaningfully diverged — and even then prefer a thin
namespace shim over a global rewrite.

> Note: this intentionally overrides the original research-folder docs, which
> instructed renaming the Go module to a new namespace immediately.

## Consequences

- Feature work targets Bifrost's real extension points (providers, plugins).
- The product is branded "Loopback Gateway" but the internal Go import path
  remains `bifrost` until a deliberate, tooling-assisted rename later.
- Trademark obligations still require scrubbing "Bifrost"/"Portkey"/"LiteLLM"
  from user-facing branding — tracked separately (see ADR 0002).
