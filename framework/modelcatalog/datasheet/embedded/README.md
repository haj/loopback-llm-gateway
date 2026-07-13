# Embedded model catalog

`litellm_model_prices.json` is a vendored snapshot of LiteLLM's
[`model_prices_and_context_window.json`](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json)
(MIT license, Copyright (c) Berri AI — see `THIRD_PARTY_LICENSES.md` at the
repository root). It is minified but otherwise unmodified.

- Source commit: `BerriAI/litellm@10d5804b3ef4` (2026-07-12)
- Entries: 2,963 models (including the `sample_spec` template entry, which the
  loader skips)

This snapshot is what the gateway loads by default (`embedded://litellm`), so a
fresh install has a full model catalog — pricing, context windows, capability
flags — without any network access. Deployments can instead point
`pricing_url` at any HTTP(S) or `file://` source serving either this LiteLLM
format or the transformed datasheet format (`provider` instead of
`litellm_provider`).

To refresh the snapshot:

```bash
scripts/update-model-catalog.sh
```

then update the source commit line above and re-run
`cd framework && go test ./modelcatalog/datasheet/`.
