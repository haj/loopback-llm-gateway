# Third-Party Licenses

Loopback Gateway derives from and/or incorporates the following projects.
Retain these notices in any distribution. As code is ported in, add the specific
files/paths affected next to each entry.

---

## Bifrost — Apache License 2.0 (base / fork)

Copyright Maxim AI. Source: https://github.com/maximhq/bifrost
Full license text: see the `LICENSE` file in this repository.
This entire repository is a derivative of Bifrost; see `NOTICE` for modifications.

---

## Portkey Gateway — MIT License

Copyright (c) 2024 Portkey, Inc

> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all
> copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
> SOFTWARE.

Ported files (logic reimplemented in Go from the MIT-licensed `plugins/default/` source):
- `plugins/loopbackguard/guardrails.go` — contains, regexMatch, wordCount,
  characterCount, sentenceCount, endsWith, allLowercase, allUppercase, notNull,
  containsCode (incl. the language map), validUrls, validJson, jsonKeys.
- `plugins/loopbackguard/request_guardrails.go` — modelWhitelist, allowedRequestTypes.
- `plugins/loopbackguard/redact.go` — the generic regex-replace redaction mechanism
  is ported from Portkey's regexReplace; the default PII rule set is original.

---

## LiteLLM — MIT License

Copyright (c) LiteLLM authors. Source: https://github.com/BerriAI/litellm

> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all
> copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, ... (full MIT text).

> ⚠️ Caveat: the upstream BerriAI/LiteLLM repo keeps some code under a separate
> proprietary license in its `enterprise/` directory. Do NOT port from that
> directory. Everything outside it is MIT.

Ported files: _(none yet)_

---

## Runtime services (called over the network, NOT bundled)

These are optional external services the gateway *calls*; their code is not
included or distributed in this repository.

- **Microsoft Presidio** — MIT (Copyright (c) Microsoft).
  Source: https://github.com/microsoft/presidio
  Used by `plugins/loopbackguard/presidio.go` via its HTTP `/analyze` API. Run it
  yourself (see `plugins/loopbackguard/presidio.docker-compose.yml`). No Presidio
  code is bundled; only its public REST contract is used.
