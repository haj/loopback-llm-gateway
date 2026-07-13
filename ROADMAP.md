# Loopback Gateway — Roadmap

> **Everything in this document is PLANNED work, not shipped features.** The single source
> of truth for what works today is [docs/project/STATUS.md](docs/project/STATUS.md); the
> per-feature scorecard is [docs/project/DELIVERY.md](docs/project/DELIVERY.md). The
> concrete near-term task list distilled from this roadmap is [TODO.md](TODO.md).

## Mission

Build the **independent, security-first, fully open-source LLM gateway** — no enterprise
tier, no paid gates, no usage markup.

The strategy in two lines:

1. **Open-source the paid gate.** The feature cluster every gateway vendor charges for is
   identity + compliance + high availability (SSO/SCIM, RBAC, audit retention, vault sync,
   clustering). We ship it under Apache-2.0, on top of upstream Bifrost's performance core,
   staying merge-compatible with upstream ([ADR 0001](docs/adr/0001-language-and-base.md)).
2. **Stay self-contained.** Everything runs in your infrastructure with zero phone-home
   by default. Data-control features (residency routing, retention policy, compliance-grade
   log export) are first-class citizens rather than a hosted upsell.

Market evidence behind this plan: [docs/project/GAP_ANALYSIS.md](docs/project/GAP_ANALYSIS.md).

## Delivered so far (Milestones 1–3)

For honesty these are listed only summarily — the audited detail is in
[DELIVERY.md](docs/project/DELIVERY.md). (Historical note: internal docs call these
"waves"; slice numbers like "4.1" in DELIVERY.md refer to that numbering.)

- **Milestone 1 — Guardrails & data protection:** guardrail engine (15 checks) with config
  UI, PII redaction (regex + Presidio, fail-closed option), large-payload streaming UI.
- **Milestone 2 — Governance & access:** users & business units, RBAC (fail-open until
  enabled), signed audit logs, MCP tool groups, prompt deployments.
- **Milestone 3 — Identity & resilience:** SSO/SCIM (Keycloak, Okta, Entra + inbound SCIM
  2.0), JWT auth → virtual keys, alert channels (Slack/PagerDuty/webhook), audit export +
  retention, per-provider circuit breaker (opt-in), Kafka telemetry connector,
  secure-setup flow, UI rebrand.

Everything below is the plan.

---

## Milestone 4 — Compliance hardening (in progress)

Finish the depth deliberately deferred from the first slices:

- Scoped admin API keys: full enforcement coverage across all admin routes, tests, docs.
- Vault sync: rotation without restart; AWS/GCP/Azure secret managers after HashiCorp.
- Alert channels: per-action filtering and a delivery-history view.
- JWT auth: ES256 support and `config.json` seeding of issuers.
- SCIM: revocation when claims/group memberships are removed (mapping is currently
  additive-only).
- Audit export to S3/GCS object storage.

## Milestone 5 — Data-control & compliance features

- **Residency routing policy** — tag providers/endpoints with a region (e.g. `eu`, `us`,
  `on-prem`) and define a policy that routes requests **only** to matching providers, with
  explicit, audit-logged exceptions. One switch to answer "can any prompt leave region X?".
- **Broader regional provider coverage** — deepen and document region-hosted model
  providers beyond the US hyperscalers: Mistral (already supported upstream), OVHcloud AI
  Endpoints, Scaleway Generative APIs, IONOS AI Model Hub, Nebius (already supported
  upstream), Aleph Alpha.
- **Compliance-log mode** — retention policy per log class (request logs vs audit events vs
  telemetry) and an export format suitable for regulatory record-keeping regimes.
- **Data-processing documentation templates** — deployment-specific templates operators can
  hand to their privacy/compliance teams.

## Milestone 6 — Telemetry connectors & guardrail breadth

- BigQuery, Datadog, and Google Pub/Sub connectors (cloning the `loopbackkafka` pattern —
  the UI views exist today but have no backends).
- S3/GCS log export with partitioning.
- Guardrails: prompt-injection/jailbreak detection (heuristic + model-based).
- Guardrails: moderation-service connectors behind one common interface — OpenAI
  Moderation, Azure Content Safety, AWS Bedrock Guardrails, Llama Guard, Lakera, Patronus.
- Guardrails: secrets detection (keys/tokens/credentials) with a fail-closed option.

Target: an honestly countable "40+ guardrails" by the end of this milestone.

## Milestone 7 — High availability & adaptive routing

No OSS gateway ships this today; every vendor charges for it.

- Cluster membership (gossip; Kubernetes label-selector + DNS discovery first).
- State sync: governance counters, rate limits, circuit-breaker state; leader election.
- Zero-downtime rolling upgrades (mixed-version capability negotiation).
- Cluster-shared circuit breakers, plus 4xx/5xx discrimination.
- Adaptive load balancing: provider health scoring and traffic shifting, with UI.
- Access profiles: policy templates that auto-provision virtual keys.

## Milestone 8 — Breadth & ecosystem (ongoing)

- **Providers:** an OpenAI-compatible custom-provider escape hatch for the long tail;
  prioritized native providers beyond the inherited ~25.
- **Agent frameworks:** integration recipes and examples for AutoGen, CrewAI, LangChain,
  LlamaIndex, and friends — mostly documentation over the OpenAI-compatible API, not
  gateway code. (Today there are **zero** shipped agent-framework integrations.)
- **Benchmarks:** published, third-party-reproducible latency/throughput numbers. Until
  then we make no performance claims of our own.
- **Security posture as a feature:** signed releases, SBOM, minimal dependencies, hardened
  defaults, a public security process.
- **Branding completion:** logo asset swap; optional theming API.
- **Optimization loop:** inference → eval → routing feedback (unclaimed territory in OSS).

## Explicitly not planned

- **Usage markup or unified billing** — hosted-router economics; our message is "no markup,
  ever".
- **Blind porting of 250+ providers** — the custom-provider escape hatch plus top-N native
  providers covers real demand.
- **Renaming Go module paths** — deferred per [ADR 0001](docs/adr/0001-language-and-base.md)
  to keep upstream merges clean.

## Success criteria

- [ ] Zero features behind any gate that isn't in this repo (no paid tiers, no stub upsells)
- [ ] Residency routing policy shipped and documented
- [ ] 40+ guardrails, honestly countable in `plugins/loopbackguard`
- [ ] 3-node cluster survives a node kill with no dropped state (demo + test)
- [ ] Published reproducible benchmark
- [ ] README claims ⊆ shipped code, always

---

*This roadmap absorbs the earlier internal planning docs (`PROJECT_PLAN.md`, now removed,
and [docs/OSS_ROADMAP.md](docs/OSS_ROADMAP.md), kept for historical reference). Sequencing
is indicative, not a commitment; milestones ship when they meet the bar in
[DELIVERY.md](docs/project/DELIVERY.md).*
