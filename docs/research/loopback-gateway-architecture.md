# Loopback Gateway - Architecture Proposal

## Vision

A unified AI gateway that combines the best features from:
- **Portkey**: Provider coverage, agent integrations, guardrails
- **Bifrost**: Performance, Go concurrency, plugin system
- **LiteLLM**: Local model support, simple config, Python ecosystem

---

## Target Architecture

### Core Stack

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Loopback Gateway                              │
├─────────────────────────────────────────────────────────────────────┤
│  Runtime: Go 1.26+ (for performance + concurrency)                   │
│  API Layer: FastAPI-compatible (for Python ecosystem compatibility) │
│  Config: YAML + JSON hybrid (flexibility)                            │
│  Storage: PostgreSQL + Redis (hybrid persistence)                    │
│  Observability: OpenTelemetry (vendor-agnostic)                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Why Go as Primary Runtime?

| Factor | Go | TypeScript/Node | Python |
|--------|----|-----------------|--------|
| **Latency** | ⭐⭐⭐⭐⭐ (~11µs) | ⭐⭐⭐ (<1ms) | ⭐⭐⭐⭐ (variable) |
| **Throughput** | ⭐⭐⭐⭐⭐ (5k+ RPS) | ⭐⭐⭐⭐ (high) | ⭐⭐⭐ (medium) |
| **Concurrency** | ⭐⭐⭐⭐⭐ (goroutines) | ⭐⭐⭐ (event loop) | ⭐⭐ (GIL) |
| **Memory** | ⭐⭐⭐⭐⭐ (efficient) | ⭐⭐⭐ (moderate) | ⭐⭐⭐ (high) |
| **Ecosystem** | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **Local Model Support** | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐⭐ |

**Decision**: Go provides the best performance foundation while we can integrate Python for specific use cases (LLM inference, ML operations).

---

## Feature Map: Best from Each Platform

### 1. Core Infrastructure (Bifrost + Portkey)

| Feature | Source | Implementation |
|---------|--------|----------------|
| **Request Routing** | Bifrost | Go channel-based queue |
| **Load Balancing** | Bifrost | Weighted key selection (~10ns) |
| **Automatic Retries** | Portkey | Exponential backoff |
| **Fallback Chains** | Portkey | Configurable fallbacks per model |
| **Request Timeouts** | Portkey | Per-request timeout config |

### 2. Provider Support (Portkey + LiteLLM)

| Provider Type | Source | Implementation |
|---------------|--------|----------------|
| **Cloud Providers** | Portkey | 200+ provider implementations |
| **Local Models** | LiteLLM | llama.cpp, vLLM, Ollama integration |
| **Custom Providers** | Bifrost | Plugin-based provider registration |

**Target**: 250+ providers (Portkey's 200+ + LiteLLM's 100+ with deduplication)

### 3. Security & Governance (Portkey + Bifrost)

| Feature | Source | Implementation |
|---------|--------|----------------|
| **RBAC** | Bifrost | Role-based access control |
| **Virtual Keys** | Bifrost | Per-customer API keys |
| **PII Redaction** | Portkey | Sensitive data masking |
| **Rate Limiting** | Bifrost | Per-key and global limits |
| **Budget Management** | Bifrost | Hierarchical cost control |
| **IP/Geo Filtering** | Portkey | Inbound rules |

### 4. Caching (Bifrost + Portkey)

| Type | Source | Implementation |
|------|--------|----------------|
| **Response Caching** | Bifrost | Redis-backed |
| **Semantic Caching** | Bifrost | Vector-based similarity |
| **LLM Caching** | Portkey | Provider-native caching |

### 5. Guardrails (Portkey + Bifrost)

| Type | Source | Implementation |
|------|--------|----------------|
| **Input Guardrails** | Portkey | 40+ pre-built rules |
| **Output Guardrails** | Portkey | Content filtering |
| **Custom Guardrails** | Bifrost | Plugin system |
| **LLM-as-Judge** | Portkey | Guardrail evaluation |

### 6. Observability (Bifrost + Portkey)

| Feature | Source | Implementation |
|---------|--------|----------------|
| **Metrics** | Bifrost | Prometheus exporters |
| **Tracing** | Bifrost | OpenTelemetry |
| **Logging** | Portkey | Request/response audit |
| **Dashboard** | Bifrost | React web UI |

### 7. Agent Frameworks (Portkey)

| Framework | Integration |
|-----------|-------------|
| **Autogen** | Native support |
| **CrewAI** | Native support |
| **LangChain** | Native support |
| **LlamaIndex** | Native support |
| **Phidata** | Native support |
| **Control Flow** | Native support |

### 8. Developer Experience (All Platforms)

| Feature | Source | Implementation |
|---------|--------|----------------|
| **Config UI** | Bifrost | React web interface |
| **Zero-config** | Portkey | Auto-configure providers |
| **Drop-in Replacement** | Bifrost | SDK compatibility layers |
| **CLI Tool** | Portkey | `loopback` CLI |
| **Documentation** | LiteLLM | Comprehensive guides |

---

## Proposed Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        Loopback Gateway                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │  FastHTTP    │  │  SDK Layer   │  │  Middleware  │  │  Plugin      │ │
│  │  Transport   │→ │  (OpenAI/    │→ │  Pipeline    │→ │  System      │ │
│  │  (~2µs)      │  │  Anthropic)  │  │  Chain       │  │  Registry    │ │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘ │
│          │                 │                 │                 │          │
│          ▼                 ▼                 ▼                 ▼          │
│  ┌────────────────────────────────────────────────────────────────┐    │
│  │                        Bifrost Core                             │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │    │
│  │  │ Provider     │  │ Inference    │  │ Request      │          │    │
│  │  │ Registry     │  │ Engine       │  │ Queue        │          │    │
│  │  │ (250+       │  │ (Routing,    │  │ (Channel-    │          │    │
│  │  │  providers)  │  │  Fallbacks)  │  │  Based)      │          │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘          │    │
│  └────────────────────────────────────────────────────────────────┘    │
│          │                 │                 │                 │          │
│          ▼                 ▼                 ▼                 ▼          │
│  ┌────────────────────────────────────────────────────────────────┐    │
│  │                      Enterprise Layer                            │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │    │
│  │  │ Governance   │  │ Caching      │  │ Guardrails   │          │    │
│  │  │ (RBAC, Keys) │  │ (Redis +     │  │ (40+ rules,  │          │    │
│  │  │              │  │  Vector)     │  │  Custom)     │          │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘          │    │
│  └────────────────────────────────────────────────────────────────┘    │
│          │                 │                 │                 │          │
│          ▼                 ▼                 ▼                 ▼          │
│  ┌────────────────────────────────────────────────────────────────┐    │
│  │                      Extension Layer                             │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │    │
│  │  │ MCP Gateway  │  │ Agent        │  │ Observability  │          │    │
│  │  │ Integration  │  │ Frameworks   │  │ (OTEL,       │          │    │
│  │  │              │  │ (Native)     │  │  Prometheus)   │          │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘          │    │
│  └────────────────────────────────────────────────────────────────┘    │
│                                                                           │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Component Breakdown

### 1. Core Engine (Go) - 80% of features

```
core/
├── bifrost.go              # Main struct, request queuing
├── inference.go            # Inference routing, fallbacks
├── providers/              # Provider implementations
│   ├── openai/            # OpenAI-compatible
│   ├── anthropic/         # Anthropic
│   ├── bedrock/           # AWS
│   ├── gemini/            # Google
│   ├── litellm-adapter/   # LiteLLM provider wrapper
│   └── local/             # Local model adapters
│       ├── llama_cpp/     # llama.cpp integration
│       ├── vllm/          # vLLM integration
│       └── ollama/        # Ollama integration
├── mcp/                    # MCP Gateway integration
└── pool/                   # Object pooling
```

**Key Decisions**:
- Use Bifrost's Go core as foundation
- Add LiteLLM provider adapter for 100+ providers
- Add local model adapters for offline support

### 2. API Layer (Go + FastHTTP) - Performance layer

```
transports/
├── bifrost-http/           # HTTP gateway
│   ├── server/            # Server lifecycle
│   ├── handlers/          # HTTP endpoints
│   └── integrations/      # SDK compatibility
│       ├── openai.go      # OpenAI SDK
│       ├── anthropic.go   # Anthropic SDK
│       └── litellm.go     # LiteLLM compatibility
└── config.schema.json     # Configuration schema
```

**Key Decisions**:
- FastHTTP for raw performance
- OpenTelemetry for observability
- Schema-based configuration

### 3. Plugin System (Go) - Extensibility

```
plugins/
├── governance/             # RBAC, virtual keys, budget
├── semanticcache/          # Vector-based caching
├── guardrails/             # 40+ guardrail rules
├── logging/                # Request/response logging
├── telemetry/              # Prometheus metrics
└── mcp/                    # MCP Gateway integration
```

**Key Decisions**:
- Bifrost's plugin interface
- Portkey's guardrail implementations
- Extensible for custom rules

### 4. Python Runtime (Optional) - LLM operations

```
python/
├── llm_ops/                # Python-based operations
│   ├── guardrail_eval/     # LLM-as-judge for guardrails
│   ├── rag/                # RAG operations
│   └── eval/               # Model evaluation
├── adapters/               # Python ↔ Go adapters
│   └── llm_client.py      # LLM client for Python ops
└── pyproject.toml         # Python dependencies
```

**Key Decisions**:
- Only for operations requiring Python (LLM evaluation)
- Go ↔ Python IPC for communication
- Avoid Python for core routing

### 5. Web UI (React) - Developer experience

```
ui/
├── app/                    # Feature pages
│   ├── workspace/         # Provider management
│   ├── guardrails/        # Guardrail configuration
│   ├── caching/           # Cache management
│   └── analytics/         # Observability
├── components/             # Shared components
└── lib/                    # Utilities
```

**Key Decisions**:
- Bifrost's React UI as base
- Portkey's dashboard features
- LiteLLM's admin UI enhancements

### 6. Configuration System (YAML + JSON)

```yaml
# Example configuration
providers:
  cloud:
    openai:
      api_key: ${OPENAI_API_KEY}
    anthropic:
      api_key: ${ANTHROPIC_API_KEY}
  local:
    llama_cpp:
      models:
        - model: /models/gemma-4-26b-a4b.Q4_K_M.gguf
          base_url: http://localhost:8081/v1
        - model: /models/qwen3.6-35b-a3b.Q4_K_M.gguf
          base_url: http://localhost:8080/v1

models:
  my-gpt-4:
    provider: openai
    model: gpt-4
  my-local-gemma:
    provider: llama_cpp
    model: gemma-4-26b-a4b

routing:
  default:
    fallbacks:
      - provider: anthropic
        model: claude-3-sonnet
  retry:
    attempts: 5
    backoff: exponential

guardrails:
  input:
    - default.contains:
        words: ["sensitive", "confidential"]
        deny: true
  output:
    - default.contains:
        words: ["PII", "SSN"]
        deny: true

caching:
  semantic:
    enabled: true
    vector_store: redis
  response:
    enabled: true
    ttl: 3600

observability:
  tracing:
    enabled: true
    exporter: otel
  metrics:
    enabled: true
    exporter: prometheus
```

---

## Performance Targets

| Metric | Target | Bifrost | Portkey | Improvement |
|--------|--------|---------|---------|-------------|
| **Base Latency** | <5µs | ~11µs | <1ms | 2x faster |
| **Key Selection** | <5ns | ~10ns | N/A | 2x faster |
| **Request Overhead** | <10µs | ~11µs | ~500µs | 50x faster |
| **Max RPS** | 10,000+ | 5,000 | 10B+ tokens/day | 2x higher |
| **Memory Footprint** | <50MB | ~100MB | ~122KB | 500x smaller |
| **Success Rate @ 5k RPS** | 99.9% | 100% | Not specified | Match |

**Note**: Memory footprint optimization through Go's efficient memory management and object pooling.

---

## Implementation Roadmap

### Phase 1: Core (Months 1-2)
- [ ] Bifrost core integration
- [ ] Provider registry (250+ providers)
- [ ] Request routing and fallbacks
- [ ] Basic load balancing

### Phase 2: Security (Months 2-3)
- [ ] RBAC implementation
- [ ] Virtual keys
- [ ] PII redaction
- [ ] Rate limiting
- [ ] Budget management

### Phase 3: Advanced Features (Months 3-4)
- [ ] Semantic caching
- [ ] Guardrails (40+ rules)
- [ ] MCP Gateway integration
- [ ] Agent framework support

### Phase 4: Observability (Months 4-5)
- [ ] OpenTelemetry tracing
- [ ] Prometheus metrics
- [ ] Request logging
- [ ] Web UI

### Phase 5: Python Extensions (Months 5-6)
- [ ] LLM-as-judge guardrail evaluation
- [ ] RAG operations
- [ ] Model evaluation tools

### Phase 6: Local Models (Months 6-7)
- [ ] llama.cpp integration
- [ ] vLLM integration
- [ ] Ollama integration
- [ ] Model serving orchestration

### Phase 7: Production Ready (Months 7-8)
- [ ] Performance optimization
- [ ] Documentation
- [ ] Testing
- [ ] Deployment guides

---

## Conclusion

The Loopback Gateway aims to be:

1. **Fastest**: Go-based with <10µs overhead
2. **Most Compatible**: 250+ providers (Portkey + LiteLLM)
3. **Most Secure**: Enterprise-grade security from both platforms
4. **Most Extensible**: Plugin-based architecture
5. **Most Flexible**: Python for ML operations, Go for performance

This creates a "best-of-both-worlds" solution that excels in both performance and features.

---

*Last updated: 2026-06-24*
