# TODO — near-term task list

Concrete, contributor-sized tasks distilled from the deferred rows in
[docs/project/DELIVERY.md](docs/project/DELIVERY.md) and the open-sourcing backlog. Larger
themes live in [ROADMAP.md](ROADMAP.md). If you pick one up, open an issue first so we can
align on scope.

## Deepen shipped first-slices

- [ ] **Scoped admin API keys — finish the slice.** Backend, middleware, and UI exist, but
      enforcement coverage across all admin routes needs auditing, plus tests and docs.
- [ ] **HashiCorp Vault sync — finish the slice.** KV fetch + refresh worker + UI exist;
      add provider-key rotation without restart, then AWS/GCP/Azure secret-manager backends.
- [ ] **Alert channels: per-action filtering.** Today a channel receives all event types;
      let operators subscribe a channel to specific actions/severities.
- [ ] **Alert channels: delivery history.** Persist and display per-channel delivery
      attempts (success/failure/retries) in the UI.
- [ ] **JWT auth: ES256 support.** The issuer-based validator currently covers RS256 JWKS;
      add ES256.
- [ ] **JWT auth: `config.json` seeding.** Issuers/claim-mappings are DB/UI-driven only;
      allow declaring them in `config.json` like other subsystems.
- [ ] **SCIM: revocation on claim removal.** Attribute → role/team/BU mapping is
      additive-only; when a group/claim disappears from the IdP, the corresponding
      assignment should be revocable (documented drift today).

## Testing & verification

- [ ] **Playwright E2E flows for the newer workspace pages:** guardrails, pii-redactor,
      rbac, scim, audit-logs, circuit-breaker, alert-channels, jwt-auth, prompt-repo,
      mcp-tool-groups (see `ui/app/workspace/`). The `make run-e2e` harness and existing
      specs in `tests/e2e/` are the pattern to follow.
- [ ] **Live sidecar verification.** Presidio, Kafka, and Keycloak are verified with
      hermetic mock-based tests only. Run them against real containers
      (`plugins/loopbackguard/presidio.docker-compose.yml` for Presidio) and record results
      in [docs/project/DELIVERY.md](docs/project/DELIVERY.md).

## Packaging & distribution

- [ ] **Fork-owned release automation.** Upstream's release workflows (removed in the fork
      cleanup) published the Helm chart and npm launcher under upstream namespaces; build
      fork-owned automation to publish `helm-charts/` and the `npx/` launcher under the
      fork's registries/namespaces.
- [ ] **Publish a container image.** No public image exists; build via
      `make docker-image LOCAL=1` in CI and push to GHCR under the fork org.
- [ ] **Branding: logo asset swap.** The text rebrand is done across ~67 UI files, but the
      logo `.webp` still shows the Bifrost mark.

## Open-source launch

- [ ] **Set up GitHub Discussions and issue labels** (good-first-issue, help-wanted,
      area/* labels matching the workspace pages) and review the inherited issue templates.
- [ ] **Public CI.** Get build + test workflows green on the public repo, then add the
      build badge to README.md (deliberately omitted until CI is public).
- [ ] **Docs site decision.** `docs/` is a Mintlify site (`docs.json`) inherited from
      upstream and still contains upstream-hosted asset references; decide between
      self-hosting a docs site under the fork's domain or trimming to in-repo Markdown.
