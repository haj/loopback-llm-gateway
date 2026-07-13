# Loopback Gateway

**The open-source LLM gateway with the enterprise tier built in.**

[![CI](https://github.com/haj/loopback-llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/haj/loopback-llm-gateway/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/haj/loopback-llm-gateway)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/haj/loopback-llm-gateway?filename=core%2Fgo.mod)](https://go.dev/dl/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

Loopback Gateway is a self-hosted AI gateway: one OpenAI-compatible endpoint in front of ~25
LLM providers, with virtual keys, budgets, rate limits, guardrails, PII redaction, RBAC,
signed audit logs, and SSO/SCIM — shipped as a single Go binary with an embedded admin UI.
Everything is Apache-2.0. There is no enterprise tier and no paid gate.

It is a fork of [Bifrost](https://github.com/maximhq/bifrost) by Maxim AI (Apache-2.0). We
keep Bifrost's high-performance gateway core and module paths (so upstream merges stay
clean — see [ADR 0001](docs/adr/0001-language-and-base.md)) and build the identity,
compliance, and safety layer on top: the cluster of features that gateway vendors typically
reserve for paid tiers.

The idea is simple. Across the LLM-gateway market, the same features keep landing behind
enterprise paywalls: SSO and SCIM provisioning, RBAC, audit logging, guardrails, PII
redaction, alerting, prompt management. Loopback Gateway gathers that paid tier from across
the ecosystem and ships it open source. A useful side effect of self-hosting it all:
prompts, completions, keys, and logs never transit a third-party service — the default
install makes **zero outbound calls** beyond the providers you configure.

To be explicit about what this is **not**: Loopback Gateway carries no certifications and
makes no compliance guarantees. Features like signed audit logs and PII redaction can
support your compliance work; your deployment and processes are what make you compliant.

## How it compares

The short version: gateway vendors give routing away and charge for governance. Loopback
Gateway ships the whole thing under Apache-2.0 — including the features our own upstream
sells as its enterprise tier.

| | **Loopback Gateway** | Bifrost (upstream) | LiteLLM | Portkey Gateway | LLMGateway | Hosted routers (OpenRouter etc.) |
|---|---|---|---|---|---|---|
| License | **Apache-2.0, everything included** | Apache-2.0 core; governance/identity features sold commercially | MIT core; SSO, audit & more in a paid enterprise tier | MIT gateway; platform features are a hosted product | AGPL-3.0 core; enterprise features under a commercial license | Closed SaaS |
| Self-hosted | ✅ single Go binary or container | ✅ | ✅ | ✅ (gateway) | ✅ | ❌ (hosted only) |
| Core language | Go | Go | Python | TypeScript | TypeScript | — |
| RBAC + SSO/SCIM | ✅ included (Keycloak, Okta, Entra; inbound SCIM 2.0) | Commercial | Paid tier | Hosted platform | Paid tier | Vendor-managed |
| Audit logging | ✅ HMAC-signed, export + retention, included | Commercial | Paid tier | Hosted platform | Paid tier | Vendor-side |
| Guardrails & PII redaction | ✅ 15 guardrails + regex/Presidio redaction, fail-closed option | Commercial | Partial | ✅ (some hosted) | Basic | Vendor-side |
| Alert channels | ✅ Slack, PagerDuty, signed webhooks | Commercial | Paid tier | Hosted platform | Partial | Vendor-side |
| Providers / models | ~25 providers; embedded catalog of 2,900+ models, offline by default | ~25 providers; catalog synced from vendor endpoint | 100+ providers | 250+ providers | 40+ providers | Large hosted catalogs |
| Outbound calls with default config | **None** (embedded catalog, no phone-home) | Catalog + update checks call vendor endpoints | Varies | Varies | Varies | All traffic transits vendor |

Where others are stronger today, honestly: LiteLLM and Portkey support far more providers,
OpenRouter has an unmatched hosted model catalog, hosted products ship dashboards and scale
you don't have to operate, and upstream Bifrost's commercial offering comes with a vendor
behind it. This table describes defaults and licensing as of July 2026, based on each
project's public documentation — if we got something wrong about your project, please
[open an issue](https://github.com/haj/loopback-llm-gateway/issues) and we will correct it.

## Features

### Gateway core (inherited from Bifrost)

- **~25 providers** — OpenAI, Anthropic, AWS Bedrock, Azure OpenAI, Google Vertex/Gemini,
  Mistral, Cohere, Groq, Ollama, OpenRouter, and more
- **OpenAI-compatible API** — point your SDK's base URL at the gateway; streaming, tool
  calling, embeddings, images, audio
- **Governance** — virtual keys, teams/customers, budgets, rate limits, routing rules,
  provider fallbacks and load balancing
- **MCP support** — connect Model Context Protocol tool servers through the gateway
- **Observability** — Prometheus metrics, OpenTelemetry plugin, request logs
- **Plugin system** — semantic caching, telemetry, mocking, and custom plugins
- **Offline model catalog** — pricing, context windows, and capability flags for **2,900+
  models** (including the major open-weights families on Groq, Fireworks, Ollama, vLLM,
  OpenRouter, and more), embedded from [LiteLLM's](https://github.com/BerriAI/litellm)
  MIT-licensed catalog. Loaded from the binary by default — **no phone-home** — with
  optional sync from any HTTP(S)/`file://` source in LiteLLM or datasheet format
- **Embedded admin UI** — served by the same binary

### Compliance & security layer (added by this fork)

All fork features are **additive and default-off**: an unconfigured gateway behaves exactly
like upstream. Shipped and tested (per-feature detail in
[DELIVERY.md](docs/project/DELIVERY.md)):

- **Guardrail engine** — 15 request/text guardrails (content checks, model allowlists,
  regex/JSON/URL validation, …) with a configuration UI
- **PII redaction** — in-process regex redactor (email, phone, SSN, credit card, IPv4,
  custom rules) plus a Presidio sidecar connector for NLP-grade detection; optional
  **fail-closed** blocking (can't verify → deny)
- **RBAC** — roles, permissions, assignments; deliberately **fail-open until you enable
  it**, with a guided secure-setup flow and one-click enforcement
- **Audit logging** — HMAC-signed events at every mutation point, live-tail export
  (JSONL/syslog), retention pruning with signed markers, verification endpoint
- **SSO + SCIM** — OIDC login via Keycloak, Okta, or Microsoft Entra; directory sync;
  inbound SCIM 2.0 endpoint (Users/Groups, RFC 7644 filters + PATCH); attribute → role/team
  mapping. Password auth always keeps working
- **JWT auth → virtual keys** — accept your IdP's JWTs on the data plane and map claims to
  virtual keys, no dashboard session needed
- **Alert channels** — Slack, PagerDuty, and HMAC-signed webhooks, fed by audit events,
  governance violations, and circuit-breaker transitions
- **Circuit breaker** — per-provider, opt-in, race-tested; request path is byte-for-byte
  unchanged until configured
- **Kafka telemetry connector** — publish completed request/response telemetry with
  batching and backpressure
- **Prompt repository** — stored prompt templates with weighted version deployments,
  resolved via request headers
- **MCP tool groups** — scope which MCP tools each key/team can see
- **Scoped admin API keys** and **HashiCorp Vault sync** — early first slices, still being
  deepened

## Quick start

### From source

Requirements: Go 1.26+, Node 22 (see `.nvmrc`) — or just `nix develop` if you use Nix.

```bash
git clone https://github.com/haj/loopback-llm-gateway.git
cd loopback-llm-gateway
make dev        # admin UI + API with hot reload, everything on http://localhost:8080
```

For a production-style binary without hot reload:

```bash
make run        # builds the UI, embeds it, builds and runs bifrost-http
```

Then send an OpenAI-style request through the gateway:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o-mini",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

Providers and keys are configured in the admin UI at `http://localhost:8080` (or via
`config.json` / the REST API).

### Docker

There is **no published container image yet** — build and run locally:

```bash
docker compose up --build gateway         # gateway + embedded admin UI on :8080
docker compose --profile full up --build  # additionally start Redis and PostgreSQL
```

Or via the Makefile:

```bash
make docker-image LOCAL=1                 # builds the UI + image from the local workspace
make docker-run CONFIG=./config.json      # runs it
```

## Documentation

| Where | What |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | How the gateway is put together: request flow, plugins, fork additions |
| [docs/README.md](docs/README.md) | Index of the full documentation tree |
| [docs/project/STATUS.md](docs/project/STATUS.md) | Ground truth for what actually builds and runs |
| [docs/project/DELIVERY.md](docs/project/DELIVERY.md) | Per-feature delivery scorecard (shipped vs first-slice vs deferred) |
| [ROADMAP.md](ROADMAP.md) | Public milestone roadmap |
| [TODO.md](TODO.md) | Near-term concrete task list — good place to find a first contribution |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Dev setup, tests, PR process |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Community standards |
| [docs/adr/](docs/adr/) | Architecture decision records (language/base choice, licensing) |

## Project status

This is a **young fork under active development**, delivered in audited milestones. We keep
an unusually strict line between shipped and planned:

- [docs/project/STATUS.md](docs/project/STATUS.md) is the single source of truth for what
  works; [docs/project/DELIVERY.md](docs/project/DELIVERY.md) scores every feature honestly
  (✅ shipped / 🚧 first-slice / ⏸ deferred).
- Latency and throughput have **not been independently measured in this fork**; upstream
  Bifrost publishes its own numbers. Reproducible benchmarks are a roadmap item.
- Presidio, Kafka, and Keycloak integrations are verified with hermetic mock-based tests;
  live sidecar verification is tracked in [TODO.md](TODO.md).
- No binary releases or container images are published yet.

If a claim in any doc conflicts with STATUS.md, STATUS.md wins.

## Contributing

Contributions are very welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for setup, test
targets, and the PR process (PRs target the `dev` branch). [TODO.md](TODO.md) lists
well-scoped near-term work.

## License

Apache License 2.0 — see [LICENSE](LICENSE). This project is a derivative work of
[Bifrost](https://github.com/maximhq/bifrost) (Copyright Maxim AI); attribution and the
list of modifications are in [NOTICE](NOTICE), and third-party notices for ported code are
in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md). "Bifrost", "Portkey", and "LiteLLM"
are trademarks of their respective owners, used only to describe provenance — see
[ADR 0002](docs/adr/0002-licensing-and-attribution.md).

Built on the excellent work of the [Bifrost](https://github.com/maximhq/bifrost) team.
