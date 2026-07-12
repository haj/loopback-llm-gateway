# Loopback Gateway — Open-Source Implementation Roadmap

## Honest summary

We audited **10 enterprise feature areas** locked in the OSS build. The reality is sobering: almost nothing is a free "flip a flag" unlock.

- **True UI-only unlocks (backend already complete): 1.** Only **large-payload streaming** is fully implemented in Go and merely needs its UI un-stubbed. Everything else needs backend work.
- **Partial backend (engine or schema exists, management/API/DB layer missing): 6.** User & Org Management, SSO/SCIM, Guardrails Config UI, PII Redactor UI, Audit Logs & Alert Channels, Resilience & Routing (beyond large-payload), MCP Enterprise.
- **No backend at all (build from scratch): 3.** RBAC & Access Control, Data Connectors, Prompt Deployments.

Two features genuinely reuse work we already shipped: **Guardrails Config UI** and **PII Redactor UI** both sit on top of the already-compiled, tested `loopbackguard` plugin (15 guardrails + regex/Presidio PII redaction). They need a configuration layer (DB + REST + UI), not a new engine. **User & Org Management** reuses the existing governance budget/rate-limit framework and `ModelConfigScopeUser`.

**Be realistic:** even the "reuse" features are multi-week efforts because the missing piece — persistent config schema, CRUD handlers, runtime hot-reload, and real UI replacing upsell stubs — is most of the actual product work. The "L" items (RBAC, SSO/SCIM, Data Connectors, full Resilience suite) are multi-month, multi-PR initiatives. There is no quick path to "feature parity."

## Feature matrix

| Feature | Backend support | OSS effort | Reuses our work | Priority (wave) |
|---|---|---|---|---|
| Large-payload streaming (UI only) | full | S | core (built-in) | Wave 1 |
| Guardrails configuration UI | partial (engine done) | M | Yes — loopbackguard | Wave 1 |
| PII Redactor UI | partial (engine done) | M | Yes — loopbackguard | Wave 1 |
| User & Org Management (users/BUs/rankings) | partial | M | Yes — governance | Wave 2 |
| RBAC & Access Control | none | L | No | Wave 2 |
| Audit Logs & Alert Channels | partial (audit ~50%) | L | No | Wave 2 |
| MCP Enterprise (auth config & tool groups) | partial | M | Partial | Wave 2 |
| Prompt Deployments | none | M | Builds on prompt infra | Wave 2/3 |
| Resilience & Routing (CB/LB/adaptive/cluster) | partial | L | Partial — governance/core | Wave 3 |
| SSO & SCIM Provisioning | partial | L | No | Wave 3 |
| Data Connectors (BigQuery/Datadog/Kafka/PubSub) | none | L | Parallels OTEL plugin | Wave 3 |

Effort key: S = days, M = 1–3 weeks, L = 1–3 months, XL = quarter+. Estimates assume one engineer and include backend + API + UI + tests.

---

## Wave 1 — Quick wins (reuse loopbackguard + trivial unlocks)

These deliver visible value fast and lean on code we already own.

### 1. Large-payload streaming (UI only) — effort S
- **What:** Backend (`core/bifrost.go`, 11 context keys, middleware threshold, integration hydration) is fully done; OSS UI currently returns `null`.
- **First step:** Remove the `null` fallback for the large-payload settings panel and wire the existing config field (threshold) to the live backend handler in `middlewares.go` lines 277–285; render the toggle + threshold input.

### 2. Guardrails Configuration UI — effort M
- **What:** `loopbackguard` (15 guardrails) runs today, but there is no DB schema, no `/api/governance/guardrails` endpoints, and only an upsell stub.
- **First step:** Add a `TableGuardrailConfig` GORM model in `framework/configstore/tables/` with a polymorphic JSON `parameters` column + scope (global/VK/team/customer), then a migration. This persistence layer unblocks handlers and UI.

### 3. PII Redactor UI — effort M
- **What:** `redact.go` (5 regex patterns) + `presidio.go` (NLP connector) are production-grade and fail-open/closed aware; missing only rule CRUD + table.
- **First step:** Add a `TablePIIRule` model (regex-or-Presidio discriminator, enable flag, scope) and a `pii_rules` handler file mirroring the existing VK CRUD pattern in `governance.go`; load rules dynamically into the existing `Redactor`/`PresidioClient`.

---

## Wave 2 — Medium (governance, access control, observability of admin actions)

Foundational platform features. RBAC is sequenced here because Wave 3 (SSO/SCIM) depends on a user/role model existing first.

### 4. User & Org Management — effort M
- **What:** Teams/customers/VKs + per-user budgets (`ModelConfigScopeUser`) and user rankings already exist; missing `TableUser`/`TableBusinessUnit`, their CRUD handlers, and VK-user attachment.
- **First step:** Define `TableUser` and `TableBusinessUnit` models + migrations, then add `GET/POST/PUT/DELETE /api/governance/users` handlers next to the existing team/customer handlers (`governance.go` ~963–1010). Start with a **flat** BU structure (no nesting).

### 5. RBAC & Access Control — effort L
- **What:** Zero backend (no user/role/permission tables, no middleware); OSS context is fail-open (`isAllowed=true` always). Depends on Wave-2 user model.
- **First step:** Design and migrate the `roles` / `permissions` / `role_assignments` tables keyed to the new `TableUser`, then implement an authorization middleware that replaces the dummy `rbacContext` and gates the mutating governance/config/provider routes. Fail-closed by default.

### 6. Audit Logs & Alert Channels — effort L
- **What:** Audit ~50% scaffolded (config schema, `EntityAuditLog` enum) but no handlers/tables/hooks; Alert Channels 0%.
- **First step:** Add a `TableAuditLog` model (HMAC-signed event, action/outcome/actor/IP/timestamp) and a single shared `recordAudit()` helper, then call it from each of the 13+ mutation points in `governance.go`. Defer alert channels until audit capture is solid.

### 7. MCP Enterprise (auth config & tool groups) — effort M
- **What:** Per-user OAuth flows and `oauth_configs`/`oauth_tokens` tables exist; `mcp_tool_group_config` is in the schema but not in Go; no CRUD endpoints.
- **First step:** Add `TableMCPToolGroup` (group → tool refs, scoped to VK/team) + migration, then CRUD handlers that integrate with existing visibility filtering (`EntityMCPToolGroup`).

### 8. Prompt Deployments — effort M (overflows into Wave 3)
- **What:** Only `EntityPromptDeployment` visibility enum exists; no table, no ConfigStore methods, no routes, no router.
- **First step:** Add `TablePromptDeployment` (named deployment → weighted version refs) and ConfigStore CRUD methods, then implement a `PromptResolver` in `plugins/prompts/main.go` (it already supports custom resolvers at line 449) that does weighted version selection with fallback when a pinned version is deleted.

---

## Wave 3 — Heavy (distributed systems & identity federation)

Multi-month efforts, external dependencies, and significant operational surface. Do not start these until Waves 1–2 are stable.

### 9. Resilience & Routing (circuit-breaker, load-balancer, adaptive-routing, cluster) — effort L
- **What:** Only schema constants exist (`RoutingEngineCircuitBreaker`, `RoutingEngineLoadbalancing`); large-payload already shipped in Wave 1. Clustering needs distributed consensus.
- **First step:** Ship a **per-provider circuit-breaker** first (smallest blast radius): add a state-machine table/config, hook it into the existing retry/key-rotation path in `core/bifrost.go`, and trip to fallback providers via the governance routing-rule chains. Defer clustering (Redis/etcd/raft) to last.

### 10. SSO & SCIM Provisioning — effort L
- **What:** Provider schemas (Okta/Entra/Keycloak) and OAuth2 handler infra exist; SCIM routes are whitelisted but unimplemented; session handler is password-only. Hard dependency on the Wave-2 user/role model.
- **First step:** Implement the SCIM user/group provisioning tables + sync engine for **one** provider (Keycloak, self-hostable for testing), plus a strict JWT validator and the auth-middleware branch in `session.go`. Keep password auth working in parallel to avoid breaking existing deployments.

### 11. Data Connectors (BigQuery / Datadog / Kafka / PubSub) — effort L
- **What:** Plugin system, config schema, and `TablePlugin` JSON storage are ready; zero Go implementation for the four exporters. The 39KB `plugins/otel/main.go` is the reference architecture.
- **First step:** Build **one** connector end-to-end as the template — Kafka (`segmentio/kafka-go`) is the simplest — following the OTEL plugin pattern (client lifecycle → span conversion → transport → filter), with batching/backpressure and retries. Then clone the pattern for the other three.

---

## Licensing & trademark note

- All work here is a **clean-room, Apache-2.0 reimplementation.** We are building *new* code that provides equivalent functionality — we are **not** copying, decompiling, or porting any proprietary Bifrost Enterprise source.
- **Do not** reference, paste, or paraphrase enterprise-licensed code, internal docs marked enterprise-only, or the `bifrost-enterprise/*` module paths referenced in the OSS tree (e.g. the `loadbalancing/plugin.go` reference in `keyconfig/store.go`). Implement against the **public config schema and observable behavior** only.
- **Scrub "Bifrost" branding** from anything we add or surface: UI strings, doc links (replace `docs.getbifrost.ai` / `getbifrost.ai` references in the upsell stubs), package names, and user-facing identifiers should read **Loopback Gateway**. Retain the upstream Apache-2.0 LICENSE and NOTICE attributions for the original fork.
- When in doubt about provenance of a snippet, rewrite it from the spec rather than adapting an enterprise artifact.
