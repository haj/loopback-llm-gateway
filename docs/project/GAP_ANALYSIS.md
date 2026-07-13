# Loopback Gateway — Gap Analysis & Competitive Position

_Research date: 2026-07-02. Market claims verified against official docs/pricing pages where possible; re-verify before acting on them (the Portkey 2.0 situation in particular is in flux)._

This document answers three questions:

1. What does upstream Bifrost gate behind its enterprise tier, and how much have we already replaced?
2. What do Portkey, LiteLLM, and the rest of the market gate behind paid tiers?
3. Where does our own roadmap over-claim relative to shipped code?

It feeds the milestone plan in [ROADMAP.md](../../ROADMAP.md). Truth hierarchy is unchanged: [STATUS.md](STATUS.md) ≻ [DELIVERY.md](DELIVERY.md) ≻ this document ≻ marketing copy.

---

## 1. Bifrost enterprise gate — replaced vs. missing

### Already replaced by Waves 1–3 (shipped, tested, OSS)

| Feature | Our implementation | Depth caveats |
|---|---|---|
| Audit logs | `transports/bifrost-http/handlers/audit.go`, HMAC-signed, append-only | No export/egress or retention policy yet |
| RBAC + row-level scoping | `handlers/rbac.go`, `rbac_middleware.go`, scoped queries | Fail-open until enabled (by design) |
| Guardrails engine + config UI | `plugins/loopbackguard` (31 tests) + `handlers/guardrails.go` | ~15 checks, not 40+ (see §3) |
| PII redaction | regex + Presidio connector, fail-closed blocking | Presidio needs a Docker sidecar |
| Circuit breaker | `core/circuitbreaker.go` (`-race` tested), per-provider | No 4xx/5xx discrimination, per-instance state only |
| SSO + SCIM | `framework/sso/` (Keycloak, 12–13 tests) | Keycloak only; SCIM lacks PATCH/filter |
| Users/teams/customers/business units | governance handlers + tables | Flat hierarchy |
| MCP tool groups, prompt deployments, large-payload streaming, Kafka export | shipped per DELIVERY.md | Kafka is mock-tested, not live-verified |

### Still missing — enterprise stubs with no backend in this repo

Evidence: docs in `docs/enterprise/`, upsell UI in `ui/app/_fallbacks/enterprise/components/`, hooks in core marked "wired by enterprise startup".

| Feature | What Bifrost EE ships | Evidence of the gap | Effort (rough) |
|---|---|---|---|
| **Clustering / HA** | memberlist gossip + gRPC sync of ~30 entity types, leader election, 6 discovery methods (K8s, Consul, etcd, DNS, broadcast, mDNS), zero-downtime rolling upgrades, broker mode | `docs/enterprise/clustering.mdx` (56KB), `BifrostContextKeyClusterNodeID` hook, no code | 8–12 wk |
| **Adaptive load balancing** | predictive provider health scoring, automatic traffic shifting | `docs/enterprise/adaptive-load-balancing.mdx`, UI stub only | 4–6 wk |
| **Vault / secret-manager sync** | HashiCorp Vault, AWS/GCP/Azure secret managers, auto key rotation | hooks in `core/schemas/vault.go`, no backend | 3–4 wk |
| **Access profiles** | policy templates auto-provisioning VKs with budgets/limits | UI stub only | 3–4 wk |
| **Alert channels** | Slack/PagerDuty/webhook alerting on thresholds | UI stub; also our own ⏸ in DELIVERY.md | 2–3 wk |
| **Data connectors: Datadog, BigQuery, Pub/Sub** | native telemetry export | UI views exist under `ui/app/workspace/observability/`, no handlers | ~2 wk each |
| **Log exports (S3/GCS/data lake)** | multi-destination export | `docs/enterprise/log-exports.mdx`; only Kafka of 4 destinations exists | 2–3 wk |
| **Scope-based API keys** | per-key permissions independent of dashboard roles | basic auth only in OSS | 1–2 wk |
| **MCP federated auth** | per-MCP-server credentials/OAuth scopes | UI stub only | 2–3 wk |
| User rankings / behavior analytics | usage analytics per org | UI stub only | 1–2 wk |
| Encryption key rotation | managed rotation | hooks in `framework/encrypt/` | 1–2 wk |

---

## 2. What the rest of the market gates behind paid tiers

### LiteLLM (BerriAI) — enterprise-only

Admin-UI SSO (Okta/Azure AD/Google, OIDC/SAML), JWT auth, audit logs + retention, RBAC with org hierarchy, IP ACLs, key rotation, secret managers (AWS KMS, Azure Key Vault, Vault, CyberArk), tag/model-specific budgets, temporary budget increases, per-team logging controls, GCS/Azure log export, enterprise moderation hooks (LLM Guard, LlamaGuard, Google Text Moderation, secrets detection), prompt-injection detection, custom branding, multi-region under one license. Third-party reports put pricing at ~$250/mo up to ~$30k/yr.

### Portkey — the March 2026 discontinuity

- Pre-2.0: OSS gateway was routing only. Logs/traces, caching, prompt management, guardrails, RBAC, SCIM, audit, data-lake export were Pro ($49/mo) or Enterprise.
- **2026-03-24: Gateway 2.0 open-sourced the enterprise gateway under Apache 2.0** (governance, OAuth 2.1 MCP auth, cost controls, circuit breakers, model catalog). Rollout appears incomplete on GitHub as of this writing.
- **2026-05-29: acquisition by Palo Alto Networks closed.** Portkey folds into Prisma AIRS; its OSS neutrality and roadmap independence are now uncertain.

### Kong AI Gateway — enterprise/Konnect-only

Semantic caching, semantic routing, cost/latency-aware `ai-proxy-advanced`, token-aware rate limiting. (Things we ship OSS or can.)

### The cross-vendor paid-only wedge

Features gated behind paid tiers at (nearly) **all** of Bifrost, LiteLLM, Portkey (pre-2.0), and Kong — with no complete OSS implementation anywhere today:

| Feature | Who has it OSS today |
|---|---|
| Clustering/HA + discovery + zero-downtime upgrades | **nobody** (Envoy AI Gateway leans on K8s primitives only) |
| Adaptive/predictive load balancing | **nobody** |
| SSO (SAML/OIDC) for the gateway admin plane | **nobody** (we ship Keycloak-based OIDC) |
| SCIM provisioning | **nobody** (ours is basic) |
| Immutable audit logs + retention | **nobody** (ours lacks retention/export) |
| Vault/secret-manager sync + key rotation | **nobody** |
| Log export to data lakes | **nobody** complete (our Kafka plugin is a start) |
| JWT auth → virtual keys | **nobody** |
| RBAC custom roles, MCP OAuth 2.1 auth | Portkey 2.0 claims both; verify what actually landed |

**The universal paid gate is identity + compliance + HA.** Everything else (routing, caching, budgets, token rate limits) someone already gives away.

### Other reference points

- **Helicone**: gateway + observability fully Apache 2.0 (Rust, sub-5ms); no enterprise governance layer. Strongest OSS observability.
- **Cloudflare AI Gateway / OpenRouter / Vercel AI Gateway**: hosted-only; monetize via 5–5.5% credit fees. Vercel's 0%-fee BYOK makes "no markup, ever" the winning OSS message. Unified billing is not worth chasing self-hosted.
- **Envoy AI Gateway**: CNCF, 1.0 stable, K8s-native; no UI/prompt-management/business features.
- **TensorZero**: repo archived 2026-06-12. Its inference→eval→routing optimization loop is unclaimed territory; also a warning that OSS gateways need a sustainability story.
- **LiteLLM security year (2026)**: PyPI supply-chain compromise (Mar, v1.82.7/8), SQLi CVE-2026-42208 CVSS 9.3 exploited in ~36h (Apr), CVSS 9.9 three-CVE privesc→RCE chain (May), MCP command-injection CVE-2026-42271 on CISA KEV (Jun). Security posture is now a first-order competitive axis.

---

## 3. Our own roadmap vs. reality

Claims in the early README/FEATURE_MATRIX/roadmap drafts that code does not back up:

| Claim | Reality | Concrete gap |
|---|---|---|
| 250+ providers | **25** dirs in `core/providers/` | No Portkey porting or LiteLLM-adapter started; check upstream custom-provider support before porting individually |
| 40+ guardrails | **~15** in `plugins/loopbackguard` | No prompt-injection/jailbreak, toxicity, or moderation-service connectors (Lakera, Aporia, Patronus, AWS Comprehend, Azure Content Safety, OpenAI Moderation, Llama Guard, Bedrock Guardrails) |
| 6 agent-framework integrations | **1** — LangChain, as an OpenAI-compatible route alias | AutoGen, CrewAI, LlamaIndex, Phidata, ControlFlow absent |
| Data connectors | Kafka only | BigQuery/Datadog/PubSub UIs have no backends — saving fails |
| White-label | 67 files rebranded | Logo asset still Bifrost; no theming API |
| SLA monitoring, `<10µs,` 10k RPS | unverified | No benchmarks run; upstream's numbers are self-published |

Quality flags: RBAC and circuit breaker are fail-open/opt-in (documented, deliberate — but fresh installs are permissive until configured); Kafka/Keycloak/Presidio are hermetically tested only, not yet live-verified against real sidecars.

---

## 4. Strategic position

1. **Ship the compliance wedge OSS** (SSO/SCIM depth, scoped API keys, JWT auth, audit retention/export, vault sync, alert channels). Every vendor charges for this cluster; we already have the skeleton.
2. **Clustering/HA is the moat.** No OSS gateway has it. It also unlocks cluster-shared circuit breakers and adaptive load balancing.
3. **Position as the independent, security-first, fully-OSS gateway.** Portkey is now a Palo Alto product; LiteLLM has a CVE trust problem; Bifrost gates everything that matters. Concretely: memory-safe Go, minimal deps, signed releases, SBOM, a real SECURITY.md process, and zero paid gates — ever.
4. **Don't chase**: unified billing/credit fees (hosted-only economics), full 250-provider porting (use an OpenAI-compatible custom-provider escape hatch for the long tail).

The milestone plan implementing this lives in [ROADMAP.md](../../ROADMAP.md).
