# Technical Deep Dive: AI Gateway Architecture

## Architecture Overview

### Portkey Gateway

#### Technology Stack
- **Runtime**: Node.js (TypeScript)
- **Deployment Targets**: 
  - Node.js server (Express/Hono)
  - Cloudflare Workers (Deno-based)
  - Docker containers
  - Replit
  - EC2 (AWS CloudFormation)

#### Core Components
```
gateway/
├── src/                    # Core gateway logic
├── plugins/               # Plugin system
├── build/                 # Compiled output
├── build/public/          # Web console
├── tests/                 # Test suite
└── cookbook/              # Usage examples
```

#### Key Features
1. **Lightweight Design**: 122kb footprint
2. **Multi-runtime**: Single codebase for Node.js and Cloudflare Workers
3. **Plugin System**: Customizable middleware architecture
4. **Config Management**: JSON-based configuration
5. **Observability**: Built-in logging and metrics

#### Request Flow
```
Client Request → API Key Validation → Routing → Provider Call → Response
                ↓              ↓           ↓           ↓
          Guardrails       Fallbacks   Retries    Caching
```

### Bifrost AI Gateway

#### Technology Stack
- **Runtime**: Go 1.26.1+
- **Database**: PostgreSQL (optional)
- **Vector Stores**: Weaviate, Qdrant, Redis, Pinecone
- **Deployment**: Go binary, Docker, Kubernetes, Terraform

#### Core Components
```
bifrost/
├── core/                  # Go core library
│   ├── bifrost.go         # Main struct, request queuing
│   ├── inference.go       # Inference routing, fallbacks
│   └── providers/         # 20+ provider implementations
├── framework/             # Data persistence, streaming
├── transports/            # HTTP gateway layer
│   └── bifrost-http/      # HTTP transport implementation
├── plugins/               # Extensible plugins
├── ui/                    # React web interface
└── tests/e2e/             # Playwright tests
```

#### Key Features
1. **High Performance**: ~11µs overhead at 5,000 RPS
2. **Modular Architecture**: Clean separation of concerns
3. **Plugin System**: LLMPlugin, MCPPlugin, HTTPTransportPlugin, ObservabilityPlugin
4. **Channel-based Async**: Go channels for request routing
5. **Object Pooling**: sync.Pool for memory efficiency
6. **Custom Context**: BifrostContext with mutable values

#### Request Flow
```
Client HTTP Request
  → FastHTTP Transport (parsing, validation ~2µs)
    → SDK Integration Layer (OpenAI/Anthropic format → Bifrost format)
      → Middleware Chain
        → PreLLMHook Pipeline (auth, rate-limit, cache check)
          → MCP Tool Discovery
            → Provider Queue (per-provider isolation)
              → Worker picks up request
                → Key Selection (~10ns weighted random)
                  → Provider API Call
                    → Response / SSE Stream
              → PostLLMHook Pipeline (reverse order)
```

### LiteLLM Gateway

#### Technology Stack
- **Runtime**: Python 3.9+
- **Web Framework**: FastAPI
- **Database**: PostgreSQL (for persistence)
- **Deployment**: Python, Docker, Docker Compose

#### Core Components
```
litellm-gateway/
├── litellm-config.yaml    # Main configuration
├── litellm-guardrails.yaml # Guardrails configuration
├── docker-compose.yml     # Docker setup
├── models.sh             # Model management scripts
└── models/               # Model server scripts
```

#### Key Features
1. **Simple Configuration**: YAML-based config
2. **Model Aliasing**: Easy model name mapping
3. **Admin UI**: Built-in web interface
4. **Database Backed**: Persistent model configuration
5. **Local Model Support**: Seamless integration with llama.cpp, vLLM

#### Request Flow
```
Client Request → LiteLLM → Model Selection → Provider Call → Response
                 ↓            ↓              ↓           ↓
           Config Validation  Key Routing  Retries    Guardrails
```

---

## Configuration Comparison

### Portkey Gateway Configuration
```json
{
  "model": "openai/gpt-4",
  "api_key": "sk-***",
  "retry": {
    "attempts": 5,
    "retryOn": ["500", "503"]
  },
  "fallbacks": [
    {
      "model": "openai/gpt-3.5-turbo",
      "provider": "openai"
    }
  ],
  "output_guardrails": [
    {
      "default.contains": {
        "operator": "none",
        "words": ["Apple"]
      },
      "deny": true
    }
  ]
}
```

### Bifrost AI Gateway Configuration
```json
{
  "providers": {
    "openai": {
      "api_key": "sk-***",
      "models": ["gpt-4", "gpt-3.5-turbo"]
    },
    "anthropic": {
      "api_key": "sk-***",
      "models": ["claude-3-opus", "claude-3-sonnet"]
    }
  },
  "models": {
    "my-gpt-4": {
      "provider": "openai",
      "model": "gpt-4"
    }
  },
  "plugins": {
    "governance": {
      "enabled": true
    },
    "semanticcache": {
      "enabled": true
    }
  }
}
```

### LiteLLM Gateway Configuration
```yaml
model_list:
  - model_name: gemma-moe
    litellm_params:
      model: openai/gemma
      api_base: http://127.0.0.1:8081/v1
      api_key: dummy

litellm_settings:
  drop_params: true
  num_retries: 1
  request_timeout: 600

general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
  database_url: os.environ/DATABASE_URL
```

---

## Performance Comparison

### Latency Benchmarks

| Metric | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|--------|----------------|-------------------|-----------------|
| **Base Overhead** | `<1ms` | ~11µs | ~1-10ms |
| **Key Selection** | N/A | ~10ns | N/A |
| **Success Rate @ 5k RPS** | N/A | 100% | N/A |
| **Queue Wait Time** | N/A | 1.67µs | N/A |

### Throughput

| Scenario | Portkey Gateway | Bifrost AI Gateway | LiteLLM Gateway |
|----------|----------------|-------------------|-----------------|
| **Max RPS** | 10B+ tokens/day | 5,000 RPS | Variable |
| **Concurrent Requests** | High | High | Medium |
| **Streaming Performance** | Excellent | Excellent | Good |

---

## Security Features

### Portkey Gateway Enterprise
- PII Redaction
- Role-based Access Control (RBAC)
- Inbound Rules (IP/Geo filtering)
- SOC2 Compliance
- HIPAA Compliance
- GDPR Compliance
- Secure Key Management

### Bifrost AI Gateway Enterprise
- Role-based Access Control (RBAC)
- Budget Management
- Rate Limiting
- Custom Plugins
- Clustering (multi-node)
- OIDC User Provisioning
- Secrets Management

### LiteLLM Gateway Enterprise
- API Key Management
- Rate Limiting
- Budget Management
- Custom Auth
- Audit Logging
- Enterprise Support

---

## Deployment Options

### Portkey Gateway
| Platform | Command |
|----------|---------|
| **Node.js** | `npx @portkey-ai/gateway` |
| **Docker** | `docker run -p 8787:8787 portkeyai/gateway` |
| **Cloudflare Workers** | `wrangler deploy src/index.ts` |
| **EC2** | AWS CloudFormation template |
| **Replit** | Import from GitHub |

### Bifrost AI Gateway
| Platform | Command |
|----------|---------|
| **NPX** | `npx -y @maximhq/bifrost` |
| **Docker** | `docker run -p 8080:8080 maximhq/bifrost` |
| **Docker Persistent** | `docker run -v $(pwd)/data:/app/data maximhq/bifrost` |
| **Go SDK** | `go get github.com/maximhq/bifrost/core` |
| **Kubernetes** | Helm chart available |

### LiteLLM Gateway
| Platform | Command |
|----------|---------|
| **Python** | `litellm --model gpt-4` |
| **Docker** | `docker run -p 4000:4000 litellm` |
| **Docker Compose** | `docker compose -p litellm-gateway up -d` |

---

## Community & Ecosystem

### Portkey Gateway
- **GitHub Stars**: 4k+
- **npm Downloads**: High
- **Discord**: Active
- **Integrations**: Autogen, CrewAI, LangChain, LlamaIndex, Phidata
- **Cookbooks**: 100+ examples

### Bifrost AI Gateway
- **GitHub Stars**: 2k+
- **Go Report Card**: A+
- **Discord**: Active
- **Integrations**: OpenAI, Anthropic, Bedrock SDKs, LangChain
- **E2E Tests**: Playwright-based

### LiteLLM Gateway
- **GitHub Stars**: 13k+
- **PyPI Downloads**: High
- **Discord**: Large
- **Integrations**: All major LLM SDKs
- **Documentation**: Comprehensive

---

## Migration Considerations

### From OpenAI Direct Integration

| Gateway | Migration Steps |
|---------|----------------|
| **Portkey** | 1. Install @portkey-ai/gateway<br>2. Set up local gateway<br>3. Update API base URL<br>4. Configure providers |
| **Bifrost** | 1. Install Bifrost<br>2. Configure providers in UI/API<br>3. Update API base URL<br>4. Optional: Use Go SDK for better performance |
| **LiteLLM** | 1. Install LiteLLM<br>2. Configure model_list<br>3. Update API base URL<br>4. Optional: Use Admin UI for model management |

### From Another Gateway

| Migration Scenario | Considerations |
|--------------------|---------------|
| **Portkey → Bifrost** | Rewrite config JSON, update provider implementations, migrate plugins |
| **Portkey → LiteLLM** | Convert JSON config to YAML, update provider API keys, migrate guardrails |
| **Bifrost → Portkey** | Convert Go plugins to JS/TS, update routing rules, migrate database |
| **LiteLLM → Bifrost** | Convert YAML to JSON config, migrate database, update SDK integrations |

---

## Future Roadmap

### Portkey Gateway
- MCP Gateway expansion
- Enhanced agent framework integrations
- More guardrails
- Improved caching
- Enterprise feature parity

### Bifrost AI Gateway
- More provider integrations
- Advanced clustering
- Enhanced observability
- Custom plugin marketplace
- Edge computing optimizations

### LiteLLM Gateway
- More model support
- Improved UI
- Enhanced guardrails
- Better streaming
- Enterprise features

---

*Last updated: 2026-06-24*
