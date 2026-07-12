# ADR 0002 — Licensing & attribution

- Status: Accepted
- Date: 2026-06-28

## Context

Loopback Gateway forks/derives from three projects and intends to be a product
(potentially commercial, potentially closed-source additions). We verified the
actual `LICENSE` files on disk.

## Findings

| Project | License (verified on disk) | Commercial fork? | Copyleft? |
|---------|----------------------------|------------------|-----------|
| Bifrost (base) | **Apache-2.0** | ✅ | No |
| Portkey gateway | **MIT** | ✅ | No |
| LiteLLM (control-plane on disk) | **MIT** | ✅ | No |

- Scanned Bifrost source for non-Apache headers (BUSL/SSPL/Elastic/"Enterprise
  Edition"): **none found**. Bifrost's enterprise features live in-repo under the
  same Apache-2.0 license; the vendor's commercial play is the hosted platform.
- No `NOTICE` file existed upstream to propagate.

## Decision

1. The combined work ships under **Apache-2.0** (MIT is one-way compatible into
   Apache-2.0). MIT-licensed portions retain their MIT notices.
2. Additions may be kept **closed-source** — permissive licenses allow it.
3. Obligations we will meet:
   - Retain upstream `LICENSE` and all copyright notices.
   - `NOTICE` states this is a fork of Bifrost and that files were modified
     (Apache-2.0 §4b).
   - `THIRD_PARTY_LICENSES.md` carries MIT notices for any Portkey/LiteLLM code
     ported in later.
   - **Trademark scrub**: Apache/MIT grant copyright + patent rights, **not**
     trademark. Do not brand with "Bifrost"/"Portkey"/"LiteLLM" or their logos.
     Product name "Loopback Gateway" is correct; remaining task is to scrub their
     names/logos from the UI and (eventually) the module path.

## Caveat to watch

The **upstream BerriAI/LiteLLM** repo (not the MIT control-plane we have) keeps
some code in an `enterprise/` directory under a **separate proprietary** license.
If provider code is later pulled from upstream LiteLLM, **avoid the `enterprise/`
directory** — everything outside it is MIT.

## Disclaimer

This is a practical reading of license files, not formal legal advice. Before a
funded commercial launch, get a short IP-lawyer review of the attribution file.
