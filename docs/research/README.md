# AI Gateway Comparison (Research)

> 📁 **This folder (`loopback-gatway/`, note the missing "e") is the RESEARCH/PLANNING workspace** — comparisons and design docs only, no application code.
> The actual application lives in the sibling **`../loopback-gateway/`** (a fork of Bifrost). For its real current status, see `../loopback-gateway/STATUS.md`.

This repository contains comparisons of open-source AI gateways found in the parent directory.

## Documents

| Document | Description |
|----------|-------------|
| `ai-gateway-comparison.md` | Feature comparison matrix |
| `technical-deep-dive.md` | Architecture and technical details |

## Quick Comparison

| Feature | Portkey Gateway | Bifrost | LiteLLM |
|---------|----------------|---------|---------|
| Language | TypeScript | Go | Python |
| Providers | 200+ | 23+ | 100+ |
| Performance | `<1ms` | ~11µs | Variable |
| Enterprise | ✅ | ✅ | ✅ |
| Agent Integrations | Autogen, CrewAI, etc. | LangChain | LangChain |
| Best For | Max providers, agent frameworks | Go performance, enterprise | Local models, Python |

## Quick Start

### Portkey Gateway
```bash
npx @portkey-ai/gateway
```

### Bifrost AI Gateway
```bash
npx -y @maximhq/bifrost
```

### LiteLLM Gateway
```bash
./start.sh  # Uses custom setup for local models
```

## Directory Structure

```
loopback-gatway/
├── README.md                  # This file
├── ai-gateway-comparison.md   # Feature matrix
└── technical-deep-dive.md     # Architecture details

../ (parent directory)
├── gateway/                   # Portkey Gateway (4k+ stars)
├── bifrost/                   # Bifrost AI Gateway (2k+ stars)
└── litellm-gateway/           # LiteLLM Gateway (custom setup)
```

## Key Findings

1. **Portkey Gateway**: Best for broadest provider support (200+) and agent framework integrations
2. **Bifrost AI Gateway**: Best for Go performance (11µs overhead) and enterprise features
3. **LiteLLM Gateway**: Best for local model support and Python ecosystem

## Notes

- All three offer free/open-source versions
- All three offer enterprise/hosted options
- All three support OpenAI-compatible APIs
