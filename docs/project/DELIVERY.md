# Loopback Gateway — Delivery Scorecard

Honest per-feature status for the open-source platform build (forked from
Bifrost / Apache-2.0). This reflects what **actually builds, tests, and
renders** — not aspirations. Legend:
**✅ production** = backend + API + UI + tests, builds green, endpoint returns JSON, page renders ·
**🚧 first-slice** = real & building, but breadth deliberately deferred (flagged below) ·
**⏸ deferred** = not built yet.

Features were delivered in numbered waves (Wave 1 → Wave 4); the wave/slice
numbers below (e.g. "4.1") refer to that sequence. The forward plan lives in
[ROADMAP.md](https://github.com/haj/loopback-llm-gateway/blob/main/ROADMAP.md).

## Scorecard

| # | Feature | Status | Notes |
|---|---------|--------|-------|
| — | Foundation (go.work, build, quarantine stubs) | ✅ | baseline |
| — | `loopbackguard` engine (15 guardrails + PII redaction + Presidio + fail-closed) | ✅ | registered as built-in plugin |
| 1 | Large-payload streaming | ✅ | ConfigStore endpoint + settings UI |
| 2 | Guardrails Configuration UI | ✅ | DB+API+UI over the live engine |
| 3 | PII Redactor UI | ✅ | regex + Presidio rules, `/test` endpoint |
| 4 | User & Org Management | ✅ | users + business units (flat) |
| 5 | RBAC & Access Control | ✅ | **fail-open until enabled**, admin seeded |
| 6 | Audit Logs | ✅ | signed events at mutation points |
| 6b | Alert Channels | ✅ | shipped as slice 4.1 — see below |
| 7 | MCP Tool Groups | ✅ | integrates MCP visibility filtering |
| 8 | Prompt Deployments | ✅ | weighted resolver + nested routes |
| 9 | Circuit Breaker (per-provider) | 🚧 | **opt-in/default-off**, `-race` tested; clustering ⏸ |
| 10 | SSO + SCIM (Keycloak) | ✅ | additive/default-off, password auth intact; Okta/Entra + SCIM PATCH/filter shipped as slice 4.5 — see below |
| 11 | Kafka data connector | 🚧 | plugin w/ batching+retries; BigQuery/Datadog/PubSub ⏸ |
| 4.1 | Alert channels (Slack/PagerDuty/webhook) | ✅ | framework/alerting dispatcher (bounded queue, 5m dedup, retry/backoff, HMAC-signed webhooks, secrets encrypted at rest), fed by audit mutations + governance violations + circuit-breaker transitions; CRUD+test-fire API, management UI; default-off; per-action filtering & delivery history ⏸ |
| 4.2 | Audit export + retention | ✅ | live-tail export (JSONL file/syslog), NDJSON backfill download, scheduled+manual prune with signed markers, verify endpoint, settings UI; default-off; S3/GCS ⏸. NOTE: batch-1 shipped the endpoints unwired — server wiring (routes, export load, retention worker lifecycle) landed with 4.1 |
| 4.3 | Scoped admin API keys | 🚧 | backend + middleware + UI (Wave 4 batch 1) |
| 4.4 | JWT auth → virtual keys | ✅ | issuer-based validator (framework/sso, JWKS reuse), inference middleware (default-off nil snapshot, caller x-bf-vk wins, fall-through or opt-in 401), claim→VK mapping w/ dot-paths + wildcard, CRUD API + UI; config.json section & ES256 ⏸ (DB/UI-driven like circuit breaker) |
| 4.6 | Vault sync (HashiCorp) | 🚧 | KV fetch + refresh worker + UI (Wave 4 batch 1) |
| 4.5 | SCIM depth + IdP breadth | ✅ | OIDCProvider abstraction (Keycloak refactored, Okta + Entra added w/ directory sync); inbound /scim/v2 (Users/Groups/ServiceProviderConfig, RFC 7644 filter parser + PATCH applier, constant-time bearer auth, 404-when-off); attribute→role/team/BU mapping engine shared by JWT login, pull sync, and inbound SCIM; provider badge + provisioning card + mapped columns in UI. Mapping is additive-only (no revocation) — documented drift |
| 4.7 | Secure-setup flow | ✅ | setup-status endpoint (RBAC/auth/inference posture), one-click enforce (seeds editor/viewer roles + admin assignment + persisted flag + live toggle, transactional, idempotent, local-admin bypass intact, env-pinned 409), guided wizard UI + fail-open banner + onboarding step. Also fixes a latent bug: RBACHandler and the enforcing middleware were separate instances |
| — | Rebrand → "Loopback Gateway" | ✅* | 67 UI files: strings, titles, doc links, upsell/promo removed. *Logo `.webp` asset still shows "Bifrost" (binary asset swap pending — a text rebrand can't redraw an image). |

## Safety properties (so new features can't brick a running gateway)
- **RBAC** is fail-OPEN until explicitly enabled *and* ≥1 assignment exists; local admin always allowed; admin role seeded.
- **Circuit breaker** registry is nil until a provider is configured → existing request path byte-for-byte unchanged; never panics (`-race` verified).
- **SSO/SCIM** is additive and default-off; password auth is preserved.

## Verification
- **Builds:** `core`, `framework`, `cli`, `transports`, `plugins/loopbackguard`, `plugins/loopbackkafka` all `go build` green; `ui` `vite build` green; binary runs.
- **Unit tests:** `loopbackguard` (31), `core` circuit-breaker (5, incl. `-race`), `framework/sso` (12), `loopbackkafka` (offline) — pass.
- **Endpoints (live, SQLite):** guardrails, pii-rules, users, business-units, rbac/roles, audit-logs, mcp-tool-groups, circuit-breakers, scim/* all return JSON; existing `/api/config` still 200 (RBAC fail-open confirmed).
- **Live integration:** not yet run against real sidecars (the original development environment had no container-registry access). Presidio, Kafka, and Keycloak are therefore **build + offline-mock verified**: each has hermetic tests with mocked sidecars (Presidio `httptest` analyzer; Kafka `messageWriter` interface; SSO mock JWKS + mock Keycloak admin) that pass. On a network with registry access, `docker compose -f plugins/loopbackguard/presidio.docker-compose.yml up -d` + the `pii-rules/{id}/test` endpoint exercises Presidio live.

## Known pre-existing upstream test debt (NOT introduced by this work)
These fail identically on the clean baseline (local core is ahead of these tests):
- `framework/configstore` `TestFullMigration_EncryptPlaintextRows`.
- `transports/.../handlers/governance_test.go` — `SecretVar` API drift (build error in that test file).
- The `transports/.../lib` test **package** now compiles (mock embeds the interface) but the full suite is very long-running; not run to completion here.

## What remains for true parity
Circuit-breaker clustering & 4xx/5xx discrimination; BigQuery/Datadog/PubSub connectors (clone the Kafka pattern); full branding asset swap (logo image). The concrete near-term list is [TODO.md](https://github.com/haj/loopback-llm-gateway/blob/main/TODO.md); the milestone plan is [ROADMAP.md](https://github.com/haj/loopback-llm-gateway/blob/main/ROADMAP.md).
