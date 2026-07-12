# loopbackguard

The Loopback Gateway guardrail plugin. It implements Bifrost's real
`schemas.LLMPlugin` interface and provides three kinds of protection:

1. **Blocking guardrails** (run in `PreLLMHook`, short-circuit with a 403 `BifrostError`)
2. **PII/secret redaction** (runs in `PreRequestHook`, the committed-mutation phase)
3. **Request-level guardrails** (model whitelist, allowed request types)

All checks are pure Go and dependency-free except the optional Presidio connector,
which calls a self-hosted sidecar. Guardrail logic is ported from Portkey's
MIT-licensed `plugins/default/` (see `../../THIRD_PARTY_LICENSES.md`).

## Guardrails

| Kind | Guardrails |
|------|-----------|
| Text (block) | `Contains` (any/all/none), `RegexMatch`, `WordCount`, `CharacterCount`, `SentenceCount`, `EndsWith`, `AllLowercase`, `AllUppercase`, `NotNull`, `ContainsCode`, `ValidURLs`, `ValidJSON`, `JSONKeys` |
| Request (block) | `ModelWhitelist`, `AllowedRequestTypes` |
| Transform (redact) | `Redactor` (regex PII: email/SSN/card/phone/IPv4 + custom rules), `PresidioClient` (NLP PII via sidecar) |
| PII blocking | `PIIDetector` pass in `PreLLMHook` — block on detection; fail-closed or fail-open on detector error (`Redactor` and `PresidioClient` both implement it) |

## Usage

```go
import "github.com/maximhq/bifrost/plugins/loopbackguard"

ssn, _ := loopbackguard.NewRegexMatch(`\d{3}-\d{2}-\d{4}`, true) // block on match

plugin := loopbackguard.NewPlugin(
        loopbackguard.Contains{Words: []string{"forbidden"}, Operator: loopbackguard.OpNone, CaseInsensitive: true},
        ssn,
        loopbackguard.ValidJSON{}, // require JSON output, etc.
    ).
    WithRequestGuardrails(
        loopbackguard.ModelWhitelist{Models: []string{"gpt-4", "claude-3"}},
    ).
    WithRedactor(loopbackguard.NewRedactor()).              // fast regex PII redaction
    WithTransformers(loopbackguard.NewPresidioClient("http://localhost:5002")) // + NLP PII

// register `plugin` with Bifrost like any other schemas.LLMPlugin
```

Execution order per request:
1. `PreRequestHook` — regex redactor, then transformers (Presidio). Mutations are
   committed and reach the provider + all fallbacks.
2. `PreLLMHook` — request-level guardrails, then the PII blocking pass, then text
   guardrails; first violation short-circuits with a 403.

### Fail-closed PII blocking

To **deny** (rather than redact) requests containing PII:

```go
plugin := loopbackguard.NewPlugin().
    WithPIIBlocking(true, // failClosed: detector errors also block
        loopbackguard.NewRedactor(),                                  // regex PII
        loopbackguard.NewPresidioClient("http://localhost:5002"),     // NLP PII
    )
```

- **failClosed = true**: if a detector finds PII *or* cannot be reached, the
  request is blocked (403). Safest for compliance; couples request success to the
  detector's availability and adds its latency to the critical path.
- **failClosed = false**: a detector error is logged and skipped (the request
  proceeds); only positive detections block.
- ⚠️ Do **not** also redact the same PII you block — `PreRequestHook` redaction
  runs first and would mask it before the blocking pass sees it. Redaction and
  blocking are alternative policies for the same data.

## Presidio connector (optional sidecar)

[Microsoft Presidio](https://github.com/microsoft/presidio) is open-source (MIT)
and self-hosted — no third-party API, no cost, data stays on your infra. It adds
NLP-grade PII detection (names, locations, …) that regex cannot catch.

Stand up the sidecar:

```bash
docker compose -f plugins/loopbackguard/presidio.docker-compose.yml up -d
# analyzer now on http://localhost:5002
```

Then attach the connector with `WithTransformers(NewPresidioClient("http://localhost:5002"))`.

Behavior notes:
- The connector calls `POST /analyze`, then masks each entity span locally in Go
  (`[REDACTED_<TYPE>]`) using rune-accurate offsets. The separate anonymizer
  service is not required.
- It is **fail-open**: if the analyzer is unreachable the original text is passed
  through and the error is logged. Fail-closed/blocking-on-detection belongs in
  `PreLLMHook` and is a planned follow-up.
- Default score threshold is 0.5 and language `en`; both are configurable on the
  client.

## Tests

```bash
go test ./...   # 26 tests, hermetic (Presidio calls use httptest mocks)
```
