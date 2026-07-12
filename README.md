# Loopback Gateway

**The open-source, self-hostable LLM gateway for European enterprises and the public sector.**

[![License](https://img.shields.io/github/license/haj/loopback-gateway)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/haj/loopback-gateway?filename=core%2Fgo.mod)](https://go.dev/dl/)
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

The focus is deliberately European. Organisations subject to the GDPR and the EU AI Act —
and public-sector bodies wary of routing prompts through US-hosted SaaS — need an LLM
gateway that runs entirely inside their own infrastructure, redacts personal data before it
leaves, and keeps verifiable logs of every AI interaction. That is the gap Loopback Gateway
is built for: existing open-source gateways are US-centric, and the EU-flavoured routers are
closed SaaS running on US hyperscalers.

## Why European teams

Self-hosting means prompts, completions, keys, and logs never transit a third-party service.
On top of that, fork features map onto concrete regulatory duties:

| Capability | Helps with |
|---|---|
| **Self-hosting** — single binary/container in your own infra or EU cloud | Data residency and sovereignty; no reliance on US-hosted SaaS routers (relevant given Schrems II and the continuing legal instability around EU–US transfer frameworks) |
| **PII redaction before egress** — in-process regex rules + [Presidio](https://microsoft.github.io/presidio/) connector, with a fail-closed blocking mode | GDPR data minimisation (Art. 5(1)(c)) and processor duties when prompts contain personal data |
| **Signed audit logs** — HMAC-chained events, export (JSONL/syslog), retention pruning | EU AI Act record-keeping and deployer logging obligations (Art. 12, Art. 26 — deployer obligations for high-risk systems expand on 2 August 2026) |
| **RBAC + SSO/SCIM** — Keycloak, Okta, Microsoft Entra; inbound SCIM 2.0 | Access control and oversight requirements; Keycloak is what much of the EU public sector already runs |
| **Guardrails + circuit breaker** | Operational risk controls and human-defined usage policies |

To be explicit about what this is **not**: Loopback Gateway carries no certifications and we
make no compliance guarantees — no "GDPR certified", no ISO 27001/SOC 2/BSI C5 claims. These
features *map to* obligations; your deployment and processes make you compliant, not the
software. A BSI C5 control-mapping document and compliance-log mode are on the
[roadmap](ROADMAP.md).

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
git clone https://github.com/haj/loopback-gateway.git
cd loopback-gateway
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
| [ROADMAP.md](ROADMAP.md) | Public milestone roadmap, including the EU sovereignty track |
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
