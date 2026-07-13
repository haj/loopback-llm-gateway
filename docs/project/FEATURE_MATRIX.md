# Loopback Gateway - Feature Comparison Matrix

> ⚠️ **The "Loopback" / "Loopback (target)" column below describes early research
> TARGETS, not what is built today.** The 250+ providers, 40+ guardrails, 6 agent
> frameworks, and `<10µs` numbers are **goals to build toward**, not current
> functionality — and several were later dropped or reframed (see
> [ROADMAP.md](https://github.com/haj/loopback-llm-gateway/blob/main/ROADMAP.md), "Explicitly not planned"). For the truthful
> current state, see **[STATUS.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/STATUS.md)** and **[DELIVERY.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/DELIVERY.md)**.
> The Bifrost / Portkey / LiteLLM columns remain valid research about those products.

## Overview

This document compares the three open-source gateways analyzed
(**Bifrost**, **Portkey**, **LiteLLM**) against the **target** design of
**Loopback Gateway**. Treat every "Loopback (target)" cell as aspirational.

---

## High-Level Summary

| Feature | Bifrost | Portkey | LiteLLM | Loopback (target) |
|---------|---------|---------|---------|----------|
| **Performance** | ⭐⭐⭐⭐⭐ (Go, 11µs) | ⭐⭐⭐ (Node.js) | ⭐⭐⭐ (Python) | ⭐⭐⭐⭐⭐ (Go + 250+) |
| **Providers** | 23 | 78+ | 100+ | **250+** |
| **Guardrails** | 0 | 40+ | 0 | **40+** |
| **Agent Support** | 0 | 6+ | 0 | **6+** |
| **Enterprise** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **Local Models** | Limited | Limited | Full | Full |
| **Language** | Go | TypeScript | Python | Go + Python |

---

## Detailed Feature Comparison

### 1. Provider Support

| Category | Bifrost | Portkey | LiteLLM | Loopback (target) |
|----------|---------|---------|---------|----------|
| **OpenAI** | ✅ | ✅ | ✅ | ✅ |
| **Anthropic** | ✅ | ✅ | ✅ | ✅ |
| **Azure OpenAI** | ✅ | ✅ | ✅ | ✅ |
| **AWS Bedrock** | ✅ | ✅ | ✅ | ✅ |
| **Google Gemini** | ✅ | ✅ | ✅ | ✅ |
| **Mistral** | ✅ | ✅ | ✅ | ✅ |
| **Cohere** | ✅ | ✅ | ✅ | ✅ |
| **Local Models (llama.cpp)** | ❌ | ❌ | ✅ | ✅ |
| **Local Models (vLLM)** | ❌ | ❌ | ✅ | ✅ |
| **Local Models (Ollama)** | ❌ | ❌ | ✅ | ✅ |
| **Specialized** | 23 total | 78+ total | 100+ total | **250+ total** |

### 2. Guardrails & Safety

| Guardrail Type | Bifrost | Portkey | LiteLLM | Loopback (target) |
|----------------|---------|---------|---------|----------|
| **Hate Speech Detection** | ❌ | ✅ | ❌ | ✅ |
| **Violence Detection** | ❌ | ✅ | ❌ | ✅ |
| **Self-Harm Prevention** | ❌ | ✅ | ❌ | ✅ |
| **Sexual Content** | ❌ | ✅ | ❌ | ✅ |
| **PII Detection** | ❌ | ✅ | ❌ | ✅ |
| **PII Redaction** | ❌ | ✅ | ❌ | ✅ |
| **Language Validation** | ❌ | ✅ | ❌ | ✅ |
| **Gibberish Detection** | ❌ | ✅ | ❌ | ✅ |
| **Custom Rules** | ❌ | ✅ | ❌ | ✅ |
| **Guardrail Count** | 0 | 40+ | 0 | **40+** |

### 3. Agent Framework Integration

| Framework | Bifrost | Portkey | LiteLLM | Loopback (target) |
|-----------|---------|---------|---------|----------|
| **Autogen** | ❌ | ✅ | ❌ | ✅ |
| **CrewAI** | ❌ | ✅ | ❌ | ✅ |
| **LangChain** | ❌ | ✅ | ❌ | ✅ |
| **LlamaIndex** | ❌ | ✅ | ❌ | ✅ |
| **Phidata** | ❌ | ✅ | ❌ | ✅ |
| **Control Flow** | ❌ | ✅ | ❌ | ✅ |
| **Frameworks** | 0 | 6+ | 0 | **6+** |

### 4. Performance Metrics

| Metric | Bifrost | Portkey | LiteLLM | Loopback (target) |
|--------|---------|---------|---------|----------|
| **Base Latency** | ~11µs | Unknown | Unknown | **`<10µs`** |
| **Key Selection** | ~10ns | Unknown | Unknown | **`<5ns`** |
| **Max RPS** | 5,000 | Unknown | Unknown | **10,000+** |
| **Memory Efficiency** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

### 5. Enterprise Features

| Feature | Bifrost | Portkey | LiteLLM | Loopback (target) |
|---------|---------|---------|---------|----------|
| **RBAC** | ✅ | ✅ | ❌ | ✅ |
| **Budget Tracking** | ✅ | ✅ | ❌ | ✅ |
| **API Key Management** | ✅ | ✅ | ❌ | ✅ |
| **Usage Analytics** | ✅ | ✅ | ❌ | ✅ |
| **Audit Logging** | ✅ | ✅ | ❌ | ✅ |
| **SOC2 Compliance** | ✅ | ✅ | ❌ | ✅ |
| **HIPAA Compliance** | ✅ | ✅ | ❌ | ✅ |
| **GDPR Compliance** | ✅ | ✅ | ❌ | ✅ |

### 6. Local Model Support

| Feature | Bifrost | Portkey | LiteLLM | Loopback (target) |
|---------|---------|---------|---------|----------|
| **llama.cpp** | ❌ | ❌ | ✅ | ✅ |
| **vLLM** | ❌ | ❌ | ✅ | ✅ |
| **Ollama** | ❌ | ❌ | ✅ | ✅ |
| **Custom Endpoints** | Limited | Limited | ✅ | ✅ |

### 7. UI & Dashboard

| Feature | Bifrost | Portkey | LiteLLM | Loopback (target) |
|---------|---------|---------|---------|----------|
| **Provider Management** | ✅ | ✅ | ✅ | ✅ |
| **Guardrail Configuration** | ❌ | ✅ | ❌ | ✅ |
| **Analytics Dashboard** | ✅ | ✅ | ✅ | ✅ |
| **Key Management UI** | ✅ | ✅ | ✅ | ✅ |
| **Agent Workspace** | ❌ | ✅ | ❌ | ✅ |

---

## Architecture Comparison

### Bifrost
```
Go + FastHTTP
├── Provider Layer (23 providers)
├── Plugin Layer (LLM/MCP plugins)
├── Request Routing (concurrent)
├── Key Selection (<5ns)
└── Enterprise Features
```

### Portkey
```
TypeScript + Node.js
├── Provider Layer (78+ providers)
├── Plugin Layer (40+ guardrails)
├── Agent Integrations (6+ frameworks)
└── Web UI Dashboard
```

### LiteLLM
```
Python + FastAPI
├── Provider Layer (100+ providers)
├── Local Model Support
├── Configuration Management
└── Admin UI
```

### Loopback Gateway
```
Go + FastHTTP (Bifrost foundation)
├── Provider Layer (250+ providers)
│   ├── Bifrost (23)
│   ├── Portkey (78)
│   └── LiteLLM (100+ via adapter)
├── Guardrail Layer (40+ guardrails)
├── Agent Layer (6+ frameworks)
├── Performance Optimizations
└── Enterprise Features
```

---

## Recommendations

These describe the **target** feature set this matrix aims at, not what is
shipped today — see [STATUS.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/STATUS.md) for current reality.

### For Maximum Performance (target)
- Bifrost's Go foundation (upstream reports sub-10&micro;s gateway overhead at
  high RPS; unverified in this fork — reproducible benchmarks are a roadmap item)

### For Maximum Provider Coverage (target)
- ~25 providers today; scaling toward 250+ is roadmap
- Local model support (Ollama, vLLM, SGLang)

### For Maximum Guardrails (target)
- 15 guardrails shipped (ported from Portkey), 40+ is the target
- Content moderation
- PII detection & redaction

### For Enterprise Use
**Choose: Loopback Gateway**
- All enterprise features from Bifrost
- Compliance (SOC2/HIPAA/GDPR)
- RBAC and audit logging

---

## Implementation Notes

Loopback Gateway combines:
1. **Bifrost core** - Go performance foundation
2. **Portkey providers** - 78 provider implementations
3. **LiteLLM adapter** - 100+ providers via adapter
4. **Portkey guardrails** - 40+ security rules
5. **Agent frameworks** - 6 integrations

**Total**: 250+ providers, 40+ guardrails, 6+ agent frameworks

---

## Conclusion

Loopback Gateway **aims to** combine the best of all three open-source projects:

- **Performance** from Bifrost (the base it forks)
- **Coverage** from Portkey and LiteLLM (to be ported)
- **Safety** from Portkey guardrails (to be ported)
- **Enterprise features** inherited from Bifrost

That is the destination. For exactly what builds and runs right now, see
[STATUS.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/STATUS.md) (baseline) and [DELIVERY.md](https://github.com/haj/loopback-llm-gateway/blob/main/docs/project/DELIVERY.md) (per-feature
scorecard). This matrix is early research about targets, not a description of
shipped features.

---

*Last updated: 2026-06-28*
