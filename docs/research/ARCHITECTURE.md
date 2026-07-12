# Loopback Gateway Architecture

## Vision

Create an ultimate AI gateway that combines the best features from:
- **Portkey Gateway**: Provider coverage, agent integrations, guardrails
- **Bifrost AI Gateway**: Performance (11µs), Go concurrency, modular design
- **LiteLLM Gateway**: Local model support, simplicity, Admin UI

## Core Principles

1. **Performance First**: Sub-microsecond overhead for production workloads
2. **Universal Provider Support**: 200+ providers with optimized integrations
3. **Local Model Excellence**: Native support for llama.cpp, vLLM, Ollama
4. **Agent-First**: First-class support for Autogen, CrewAI, LangChain, etc.
5. **Enterprise Ready**: Security, compliance, observability built-in
6. **Developer Experience**: Simple config, beautiful UI, excellent docs

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Loopback Gateway                             │
├─────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  HTTP/WebSocket Transport Layer (FastHTTP - Go)              │   │
│  │  • Ultra-low latency routing (<5µs overhead)                 │   │
│  │  • Streaming support (SSE, HTTP/2)                            │   │
│  │  • Connection pooling & keep-alive                           │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                       │
│  ┌───────────────────────────▼──────────────────────────────────┐   │
│  │  Request Processing Pipeline (Middleware Chain)              │   │
│  │  ┌────────────────────────────────────────────────────────┐  │   │
│  │  │ Pre-Processing Hooks                                    │  │   │
│  │  │ • Authentication & Key Validation                       │  │   │
│  │  │ • Rate Limiting (token bucket)                          │  │   │
│  │  │ • IP/Geo Filtering (Enterprise)                         │  │   │
│  │  │ • Guardrails Check (input validation)                   │  │   │
│  │  │ • Semantic Cache Lookup (vector-based)                  │  │   │
│  │  │ • Budget & Quota Check                                  │  │   │
│  │  └────────────────────────────────────────────────────────┘  │   │
│  │                              │                                │   │
│  │  ┌──────────────────────────▼──────────────────────────────┐ │   │
│  │  │  Smart Routing Engine                                   │ │   │
│  │  │ • Model Selection (based on config, cost, latency)      │ │   │
│  │  │ • Provider Selection (weighted load balancing)          │ │   │
│  │  │ • Automatic Retry with Exponential Backoff              │ │   │
│  │  │ • Failover Routing (fallback chains)                    │ │   │
│  │  │ • Conditional Routing (based on request content)        │ │   │
│  │  └────────────────────────────────────────────────────────┘ │   │
│  │                              │                                │   │
│  │  ┌──────────────────────────▼──────────────────────────────┐ │   │
│  │  │  Provider Adapters (Interface-based)                    │ │   │
│  │  │ • OpenAI-compatible (90% of providers)                  │ │   │
│  │  │ • Anthropic-compatible                                  │ │   │
│  │  │ • AWS Bedrock (event-stream)                            │ │   │
│  │  │ • Google Gemini (JSON API)                              │ │   │
│  │  │ • Local Model Adapters (llama.cpp, vLLM, Ollama)        │ │   │
│  │  └────────────────────────────────────────────────────────┘ │   │
│  │                              │                                │   │
│  │  ┌──────────────────────────▼──────────────────────────────┐ │   │
│  │  │  Response Processing Pipeline                           │ │   │
│  │  │ • Guardrails Check (output validation)                  │ │   │
│  │  │ • PII Redaction (Enterprise)                            │ │   │
│  │  │ • Logging & Telemetry                                   │ │   │
│  │  │ • Semantic Cache Update                                 │ │   │
│  │  │ • Cost Tracking                                         │ │   │
│  │  └────────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  MCP Gateway (Model Context Protocol)                        │   │
│  │  • Unified tool calling interface                            │   │
│  │  • Agent orchestration (multi-turn tool calls)               │   │
│  │  • External tool integrations                                │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Admin & Developer Tools                                     │   │
│  │  • REST Admin API                                            │   │
│  │  • Web UI (React + Vite)                                     │   │
│  │  • CLI for config management                                 │   │
│  │  • Model registry & catalog                                  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Persistence Layer                                           │   │
│  │  • PostgreSQL (config, logging, metrics)                     │   │
│  │  • Redis (cache, rate limiting)                              │   │
│  │  • Vector Store (semantic caching - Weaviate/Qdrant)         │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                       │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Technology Stack

### Core Runtime
- **Language**: Go 1.26+ (performance, concurrency, single binary)
- **Web Framework**: FastHTTP (10x faster than net/http)
- **Async**: Go channels + sync.Pool for zero-allocation hot paths

### Configuration & Admin
- **Config Format**: YAML + JSON Schema validation
- **Admin API**: REST over HTTP
- **Web UI**: React + Vite + Tailwind CSS
- **CLI**: Cobra (Go CLI framework)

### Observability
- **Metrics**: Prometheus (native integration)
- **Tracing**: OpenTelemetry
- **Logging**: Structured JSON logs
- **Alerting**: Webhook-based notifications

### Data Stores
- **Primary**: PostgreSQL (configuration, logs, metrics)
- **Cache**: Redis (rate limiting, session state, cache)
- **Vector**: Weaviate/Qdrant/Pinecone (semantic caching)

---

## Key Features

### 1. Performance Optimizations

| Feature | Implementation |
|---------|----------------|
| **Ultra-low Latency** | Go channels + sync.Pool + zero-allocation hot paths |
| **Key Selection** | Weighted random in ~10ns |
| **Request Routing** | Pre-computed routing tables |
| **Connection Pooling** | Per-provider connection pools |
| **Streaming** | Chunked processing without buffering |
| **Caching** | Vector-based semantic cache with TTL |

### 2. Provider Integration

**Category 1: OpenAI-compatible (80+ providers)**
- Generic adapter with minimal code duplication
- Automatic support for any OpenAI-compatible endpoint
- Provider-specific optimizations (e.g., Anthropic's event stream)

**Category 2: Specialized protocols (15+ providers)**
- AWS Bedrock (SigV4 signing, HTTP/2)
- Google Gemini (direct JSON API)
- Cohere, Mistral, Perplexity (custom converters)

**Category 3: Local Models (10+ engines)**
- llama.cpp (direct HTTP)
- vLLM (OpenAI-compatible)
- Ollama (OpenAI-compatible)
- Text generation webui
- LM Studio

### 3. Agent Framework Support

| Framework | Integration Method |
|-----------|-------------------|
| **Autogen** | Direct SDK integration + telemetry |
| **CrewAI** | Direct SDK integration + telemetry |
| **LangChain** | Drop-in replacement |
| **LlamaIndex** | Drop-in replacement |
| **Phidata** | Direct SDK integration |
| **Control Flow** | Direct SDK integration |
| **Custom Agents** | Generic OpenAI-compatible interface |

### 4. Security & Compliance

| Feature | Description |
|---------|-------------|
| **PII Redaction** | Automatic detection and removal of PII |
| **RBAC** | Fine-grained access control |
| **API Keys** | Virtual keys with metadata |
| **Rate Limiting** | Per-key, per-model, per-team limits |
| **Audit Logging** | Complete request/response logging |
| **Encryption** | TLS everywhere, secrets management |
| **Compliance** | SOC2, HIPAA, GDPR ready |

### 5. Advanced Features

| Feature | Description |
|---------|-------------|
| **Guardrails** | 40+ pre-built + custom rules (input/output) |
| **Semantic Caching** | Context-aware caching with vector similarity |
| **Budget Management** | Hierarchical budget tracking |
| **Load Balancing** | Weighted distribution across providers/keys |
| **Automatic Retries** | Exponential backoff with jitter |
| **Fallback Chains** | Multiple fallback targets |
| **Conditional Routing** | Route based on model, cost, latency, region |

---

## Deployment Options

### 1. Single Binary (Development)
```bash
./loopback-gateway --config config.yaml
```

### 2. Docker (Production)
```bash
docker run -p 8080:8080 loopback-gateway:latest
```

### 3. Kubernetes
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loopback-gateway
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: gateway
        image: loopback-gateway:latest
        ports:
        - containerPort: 8080
```

### 4. Serverless (Cloudflare Workers)
```bash
wrangler deploy src/index.ts
```

### 5. Cloud Deployment
- AWS EC2/ECS/EKS
- Google Cloud Run/Anthos
- Azure Container Instances/AKS
- Digital Ocean App Platform

---

## Configuration Example

```yaml
# Loopback Gateway Configuration

gateway:
  host: "0.0.0.0"
  port: 8080
  base_url: "/v1"
  
  # Performance tuning
  max_concurrent_requests: 10000
  request_timeout: 300  # seconds
  stream_timeout: 60    # seconds for streaming
  
  # Logging
  log_level: "info"
  log_format: "json"
  
  # Observability
  metrics_enabled: true
  metrics_port: 9090
  tracing_enabled: true
  tracing_endpoint: "http://jaeger:14268/api/traces"

providers:
  # OpenAI-compatible providers
  openai:
    api_key: "${OPENAI_API_KEY}"
    models: ["gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"]
    
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"
    models: ["claude-3-opus", "claude-3-sonnet", "claude-3-haiku"]
    
  # Local models (via llama.cpp, vLLM, etc.)
  local-llama:
    api_base: "http://localhost:8080/v1"
    models: ["llama-3-70b", "mistral-7b", "codestral"]
    
  # AWS Bedrock
  bedrock:
    region: "us-east-1"
    models: ["anthropic.claude-3-opus", "amazon.titan"]

models:
  # Model aliases
  "gpt-4":
    provider: "openai"
    model: "gpt-4"
    priority: 1
    
  "claude-3":
    provider: "anthropic"
    model: "claude-3-opus"
    priority: 1
    
  "local-coder":
    provider: "local-llama"
    model: "codestral"
    priority: 2
    cost: 0.5  # cents per 1M tokens

routing:
  # Default fallback chain
  fallback_chains:
    - ["gpt-4", "gpt-4-turbo", "claude-3"]
    - ["claude-3", "gpt-4"]
    
  # Conditional routing based on model
  rules:
    - condition: "model.startswith('claude-3')"
      provider: "anthropic"
    - condition: "model.startswith('gpt-')"
      provider: "openai"

guardrails:
  enabled: true
  input_guardrails:
    - name: "pii-detection"
      type: "pii"
    - name: "injection-detection"
      type: "prompt-injection"
  output_guardrails:
    - name: "harmful-content"
      type: "harmful"
    - name: "hallucination"
      type: "hallucination"

caching:
  enabled: true
  type: "semantic"  # or "simple"
  ttl: 3600  # 1 hour
  vector_store:
    type: "qdrant"
    endpoint: "http://qdrant:6333"

budget:
  enabled: true
  limits:
    daily: 1000000  # tokens
    monthly: 30000000
  alerts:
    threshold: 0.8
    notify: "admin@example.com"
```

---

## Roadmap

### Phase 1: Core Gateway (Current)
- ✅ FastHTTP transport layer
- ✅ Provider adapter interface
- ✅ Request routing with fallbacks
- ✅ Basic guardrails
- ✅ Admin API + UI

### Phase 2: Advanced Features
- Semantic caching with vector stores
- MCP Gateway integration
- Budget management
- Advanced rate limiting
- Multi-tenant support

### Phase 3: Enterprise Features
- PII redaction
- RBAC & OIDC
- Compliance modules
- Custom plugins
- Clustering

### Phase 4: Optimization
- Edge deployment (Cloudflare Workers)
- Automatic provider optimization
- Predictive caching
- Cost-aware routing

---

## Getting Started

```bash
# Clone and build
git clone https://github.com/your-org/loopback-gateway.git
cd loopback-gateway

# Build
go build -o loopback-gateway ./cmd/gateway

# Run with default config
./loopback-gateway

# Or with custom config
./loopback-gateway --config config.yaml
```

Access:
- API: http://localhost:8080/v1
- Admin UI: http://localhost:8080/ui
- Metrics: http://localhost:9090/metrics

---

## Contributing

We welcome contributions! See `CONTRIBUTING.md` for details.

## License

MIT License - see `LICENSE` for details.

---

## Acknowledgments

This project builds on the strengths of:
- **Portkey Gateway** for provider coverage and agent integrations
- **Bifrost AI Gateway** for performance and Go architecture
- **LiteLLM** for local model support and simplicity

---

*Loopback Gateway - The Ultimate AI Gateway*
