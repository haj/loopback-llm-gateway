# Contributing to Loopback Gateway

Thanks for your interest in contributing! This document covers the practical bits: getting a
dev environment running, testing, style, and how PRs flow. For a code-level orientation,
read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) first — it will save you a lot of time.

## Development setup

### Prerequisites

- **Go 1.26+**
- **Node 22** (exact version in [`.nvmrc`](.nvmrc)) for the admin UI
- Or, with Nix: `nix develop` from the repo root provides the pinned toolchain.

### Running locally

```bash
make dev
```

This installs UI dependencies, sets up the Go workspace (`go.work`), and starts the UI
(:3000) and API with hot reload, everything proxied at **http://localhost:8080**.

Other useful targets:

```bash
make build          # Build the bifrost-http binary (LOCAL=1 to build against go.work)
make run            # Build and run without hot reload
make lint           # golangci-lint run ./...
make fmt            # gofmt + goimports (Go)
make format ui      # Format UI code (Oxfmt)
```

> **Note on `go.work`:** the checked-in `go.work` is load-bearing — it makes all sub-modules
> build against the local `./core` instead of the published release, and carries `replace`
> directives for the fork plugins. `make setup-workspace` (run automatically by `make dev`)
> regenerates it; if you touch workspace setup, verify the fork plugin entries survive.

## Testing

Always prefer the `make` targets over raw `go test` — they wire environment variables from
`.env`:

```bash
make test-core PROVIDER=openai TESTCASE=TestSimpleChat   # provider tests (hit live APIs)
make test-mcp TESTCASE=TestAgentLoop                     # MCP/agent tests (mock-based)
make test-plugins                                        # all plugin tests
make test-governance PATTERN=RateLimit                   # governance plugin tests
make test-framework                                      # framework module tests
make test-http-transport                                 # HTTP transport tests
make run-e2e FLOW=providers                              # Playwright E2E (needs running dev server)
make test-all                                            # core + framework + plugins + transport + cli
```

The fork's own plugins have hermetic unit tests that need no live APIs:

```bash
cd plugins/loopbackguard && go test ./...
cd plugins/loopbackkafka && go test ./...
```

Known pre-existing upstream test debt (do not chase as regressions): the configstore
full-migration test and a governance secret-var API drift in the transport test file.

## Code style

- **Go:** `make fmt` (gofmt + goimports) and `make lint` must pass.
- **UI:** `make format ui` (Oxfmt). The UI lives in `ui/` (React + Vite).
- Detailed conventions: [docs/contributing/code-conventions.mdx](docs/contributing/code-conventions.mdx).

The `docs/contributing/` folder has focused guides inherited from upstream that still apply:
[setting up the repo](docs/contributing/setting-up-repo.mdx),
[running tests](docs/contributing/running-tests.mdx),
[raising a PR](docs/contributing/raising-a-pr.mdx),
[adding a provider](docs/contributing/adding-a-provider.mdx),
[adding a plugin](docs/contributing/adding-a-plugin.mdx),
[adding a configstore](docs/contributing/adding-a-configstore.mdx),
[adding a logstore](docs/contributing/adding-a-logstore.mdx), and
[adding a vectorstore](docs/contributing/adding-a-vectorstore.mdx).

## Pull requests

- **Target the `dev` branch** (not `main`).
- Keep PRs focused; include tests for new behavior.
- A DCO sign-off is **not** required; standard GitHub PR flow applies.
- Fill in the PR template (`.github/pull_request_template.md`).

### Ground rules specific to this fork

1. **Do not rename Go module paths.** All modules intentionally remain
   `github.com/maximhq/bifrost/*` to keep upstream merges clean
   ([ADR 0001](docs/adr/0001-language-and-base.md)).
2. **Don't break the safety invariants:**
   - RBAC is fail-open until explicitly enabled and at least one role assignment exists;
     the local admin must always be allowed.
   - The circuit-breaker registry is nil until configured — with no config, the request
     path must remain byte-for-byte unchanged.
   - SSO/SCIM is additive and default-off — password auth must keep working.
   New enterprise-style features should follow the same pattern: additive, default-off,
   zero impact when unconfigured.
3. **Truth in docs.** [docs/project/STATUS.md](docs/project/STATUS.md) and
   [docs/project/DELIVERY.md](docs/project/DELIVERY.md) must reflect reality. Never document
   a capability as shipped until it builds and has tests.
4. **Licensing.** The project is Apache-2.0 with attribution in [NOTICE](NOTICE). Any code
   ported from MIT-licensed projects (e.g. Portkey, LiteLLM) needs an entry in
   [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md). Never port code from proprietary or
   enterprise-licensed sources.

## Where to start

[TODO.md](TODO.md) is a curated list of well-scoped near-term tasks, and
[ROADMAP.md](ROADMAP.md) shows where the project is heading. If you want to work on
something larger, open an issue first so we can align on the approach.

## Reporting security issues

Please do not open public issues for vulnerabilities — see [SECURITY.md](SECURITY.md).
