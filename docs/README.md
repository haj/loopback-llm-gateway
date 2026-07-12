# Loopback Gateway documentation

Index of the `docs/` tree. Much of the reference content (`.mdx` files) is inherited from
upstream [Bifrost](https://github.com/maximhq/bifrost) and organised as a Mintlify site
(`docs.json`); it is accurate for the shared gateway core, but individual pages may still
use upstream naming. Fork-specific truth lives in [`project/`](project/) and the root-level
[README](../README.md), [ROADMAP](../ROADMAP.md), and [TODO](../TODO.md).

## Start here

| Doc | What it covers |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | How the gateway is put together: request flow, plugin system, fork additions |
| [overview.mdx](overview.mdx) | Product overview of the gateway core |
| [quickstart/](quickstart/) | Getting started — gateway (HTTP), Go SDK, and CLI |
| [adr/](adr/) | Architecture decision records: [language & base choice](adr/0001-language-and-base.md), [licensing & attribution](adr/0002-licensing-and-attribution.md) |

## Project status & planning (fork-specific)

| Doc | What it covers |
|---|---|
| [project/STATUS.md](project/STATUS.md) | **Ground truth** for what actually builds and runs |
| [project/DELIVERY.md](project/DELIVERY.md) | Per-feature delivery scorecard (shipped / first-slice / deferred) |
| [project/FEATURE_MATRIX.md](project/FEATURE_MATRIX.md) | Early competitive research (targets, not shipped features) |
| [project/GAP_ANALYSIS.md](project/GAP_ANALYSIS.md) | Market gap analysis behind the roadmap |
| [../ROADMAP.md](../ROADMAP.md) | Public milestone roadmap |
| [OSS_ROADMAP.md](OSS_ROADMAP.md) | Historical: the original Waves 1–3 implementation plan |
| [research/](research/) | Early research and planning notes |
| [specs/](specs/) | JSON config specs for fork features (guardrails, PII rules) |
| [draft-stubs/](draft-stubs/) | Quarantined non-compiling early drafts (design reference only) |

## Reference (gateway core)

| Doc | What it covers |
|---|---|
| [architecture/](architecture/) | Module-level internals: core, framework, transports, plugins |
| [features/](features/) | Feature guides (governance, caching, routing, and more) |
| [providers/](providers/) | Per-provider configuration |
| [models-catalog/](models-catalog/) | Model catalog and pricing data |
| [integrations/](integrations/) | Using the gateway from OpenAI-compatible SDKs and tools |
| [mcp/](mcp/) | Model Context Protocol support |
| [plugins/](plugins/) | Plugin reference |
| [cli-agents/](cli-agents/) | Using coding agents/CLIs through the gateway |
| [openapi/](openapi/) | OpenAPI specification for the management and inference APIs |
| [edge/](edge/) | Edge deployment reference |
| [enterprise/](enterprise/) | Upstream's enterprise-tier docs, kept as behavioral reference for the OSS reimplementations |

## Operations

| Doc | What it covers |
|---|---|
| [deployment-guides/](deployment-guides/) | Deploying the gateway (Docker, Kubernetes/Helm, cloud) |
| [migration-guides/](migration-guides/) | Migrating from other gateways |
| [benchmarking/](benchmarking/) | Benchmark methodology (upstream's; fork numbers not yet published) |
| [security.mdx](security.mdx) | Security overview |
| [release-cadence.mdx](release-cadence.mdx) | Release cadence |
| [changelogs/](changelogs/) | Per-module changelogs |

## Contributing

| Doc | What it covers |
|---|---|
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | Dev setup, tests, PR process |
| [contributing/](contributing/) | Focused guides: repo setup, code conventions, running tests, raising a PR, adding providers/plugins/stores |
