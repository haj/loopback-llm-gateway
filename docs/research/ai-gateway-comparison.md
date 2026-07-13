# AI Gateway Feature Comparison Matrix

## Overview

This document compares three open-source AI gateways available in the parent project directory:
1. **Portkey Gateway** (`/home/hassan-ajan/Projects/gateway`)
2. **Bifrost AI Gateway** (`/home/hassan-ajan/Projects/bifrost`)
3. **LiteLLM Gateway** (`/home/hassan-ajan/Projects/litellm-gateway`)

---

## Quick Summary

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Primary Language** | TypeScript | Go | Python (LiteLLM) |
| **License** | MIT | Apache 2.0 | MIT |
| **Deployment** | Node.js, Cloudflare, Docker | Go binary, Docker, Kubernetes | Python, Docker |
| **Enterprise Edition** | Available | Available (Maxim) | Available (LiteLLM Enterprise) |
| **Provider Count** | 200+ | 23+ | 100+ |
| **Community Size** | Large (npm downloads) | Growing | Large (13k+ GitHub stars) |

---

## Detailed Feature Matrix

### Core Infrastructure

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **OpenAI-compatible API** | ✅ | ✅ | ✅ |
| **Anthropic-compatible API** | ✅ | ✅ | ✅ |
| **Multi-provider routing** | ✅ | ✅ | ✅ |
| **Load balancing** | ✅ | ✅ | ✅ |
| **Automatic retries** | ✅ (exponential backoff) | ✅ | ✅ |
| **Request fallbacks** | ✅ | ✅ | ✅ |
| **Request timeouts** | ✅ | ✅ | ✅ |
| **Streaming support** | ✅ | ✅ | ✅ |

### Advanced Features

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Semantic caching** | ✅* | ✅ | ✅ |
| **Guardrails** | ✅ (40+ pre-built) | ✅ | ✅ |
| **MCP Gateway** | ✅ | ✅ | ❌ |
| **Multi-modal support** | ✅ (text, image, audio, vision) | ✅ (text, image, audio) | ✅ |
| **Budget management** | ✅* | ✅ | ✅ |
| **Rate limiting** | ✅* | ✅ | ✅ |
| **Virtual keys** | ✅* | ✅ | ✅ |

### Security & Governance

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Role-based access control (RBAC)** | ✅* | ✅ | ✅ |
| **PII redaction** | ✅* | ❌ | ❌ |
| **Secure key management** | ✅ | ✅ | ✅ |
| **Inbound rules/IP filtering** | ✅* | ❌ | ❌ |
| **SOC2 compliance** | ✅* | ❌ | ❌ |
| **HIPAA compliance** | ✅* | ❌ | ❌ |
| **GDPR compliance** | ✅* | ❌ | ❌ |
| **Custom plugins** | ✅ | ✅ | ✅ |

### Developer Experience

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Web UI** | ✅ (Console) | ✅ (React web interface) | ✅ (Admin UI) |
| **Zero-config startup** | ✅ | ✅ | ✅ |
| **Drop-in replacement** | ✅ | ✅ | ✅ |
| **SDK integrations** | ✅ (JS, Python, LangChain, etc.) | ✅ (OpenAI, Anthropic, Bedrock, etc.) | ✅ (All major SDKs) |
| **Observability** | ✅* | ✅ (Prometheus metrics) | ✅ |

### Performance

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Latency overhead** | `<1ms` | ~11µs | Varies |
| **Max RPS tested** | 10B+ tokens/day | 5,000 RPS | Not specified |
| **Memory footprint** | 122kb | Small | Varies |

### Integration Capabilities

| Feature | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|---------|----------------|-------------------|-----------------|
| **Autogen** | ✅ | ❌ | ❌ |
| **CrewAI** | ✅ | ❌ | ❌ |
| **LangChain** | ✅ | ✅ | ✅ |
| **LlamaIndex** | ✅ | ❌ | ❌ |
| **Phidata** | ✅ | ❌ | ❌ |
| **Control Flow** | ✅ | ❌ | ❌ |
| **LiteLLM SDK** | ✅ | ❌ | N/A |
| **PydanticAI** | ❌ | ✅ | ❌ |

---

## Provider Support

### Portkey Gateway
- **Supported providers**: 200+ including OpenAI, Anthropic, AWS Bedrock, Google Gemini, Azure OpenAI, Cohere, Mistral, Perplexity, Ollama, and many more
- **Special focus**: Broadest provider coverage with optimized integrations

### Bifrost AI Gateway
- **Supported providers**: 23+ including OpenAI, Anthropic, AWS Bedrock, Google Vertex, Azure, Cerebras, Cohere, Mistral, Ollama, Groq, and more
- **Go ecosystem focus**: Strong native Go SDK support

### LiteLLM Gateway
- **Supported providers**: 100+ including OpenAI, Anthropic, Bedrock, Azure, Vertex, HuggingFace, and local models
- **Local-first**: Excellent support for self-hosted models via llama.cpp, vLLM, etc.

---

## Pricing & Commercial Model

| Aspect | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|--------|----------------|-------------------|-----------------|
| **Open Source License** | MIT | Apache 2.0 | MIT |
| **Hosted Option** | ✅ Portkey Cloud | ✅ Maxim Cloud | ✅ LiteLLM Cloud |
| **Self-hosting** | ✅ | ✅ | ✅ |
| **Enterprise Features** | Advanced security, compliance, governance | Clustering, custom plugins, advanced observability | Custom support, SLA, enterprise features |
| **Free Tier** | Available | Available | Available |

---

## Installation & Deployment

### Portkey Gateway
```bash
# Local development
npx @portkey-ai/gateway

# Docker
docker run -p 8787:8787 portkeyai/gateway

# Cloudflare Workers
wrangler deploy src/index.ts
```

### Bifrost AI Gateway
```bash
# NPX (Node.js)
npx -y @maximhq/bifrost

# Docker
docker run -p 8080:8080 maximhq/bifrost

# Docker with persistence
docker run -p 8080:8080 -v $(pwd)/data:/app/data maximhq/bifrost
```

### LiteLLM Gateway
```bash
# Direct Python
litellm --model gpt-4

# Docker
docker run -v /path/to/config.yaml:/app/config.yaml -p 4000:4000 litellm litellm --config /app/config.yaml --host 0.0.0.0

# Docker Compose (with PostgreSQL)
docker compose -p litellm-gateway up -d
```

---

## Community & Support

| Aspect | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|--------|----------------|-------------------|-----------------|
| **GitHub Stars** | 4k+ | 2k+ | 13k+ |
| **Discord** | Active community | Active Discord | Large community |
| **Documentation** | Comprehensive | Good | Excellent |
| **Contributions** | Open to community | Open to community | Open to community |

---

## Recommendation Guide

### Choose Portkey Gateway if you:
- Need maximum provider coverage (200+ providers)
- Require advanced security features (PII redaction, compliance)
- Want strong agent framework integrations (Autogen, CrewAI, etc.)
- Need comprehensive guardrails
- Prefer TypeScript/JavaScript ecosystem

### Choose Bifrost AI Gateway if you:
- Prefer Go for performance-critical applications
- Need advanced enterprise features (clustering, custom plugins)
- Want low-latency gateway (~11µs overhead)
- Need strong Go SDK integration
- Want built-in observability with Prometheus

### Choose LiteLLM Gateway if you:
- Want maximum flexibility with local models
- Prefer Python ecosystem
- Need strong community support
- Want a simple, well-documented solution
- Need excellent self-hosted model support

---

## Notes

- **\* Enterprise-only features**: Available only in hosted or enterprise deployments
- **\* LiteLLM Gateway**: Built on LiteLLM core, includes PostgreSQL for persistence
- **\* Bifrost**: Requires Go 1.26.1+ and uses a multi-module workspace
- **\* Portkey**: Offers Cloudflare Workers deployment for edge computing use cases

---

*Last updated: 2026-06-24*
