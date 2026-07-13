package datasheet

import _ "embed"

// EmbeddedPricingURL selects the vendored LiteLLM catalog snapshot instead of
// a remote datasheet. It is the default pricing source so a fresh install has
// a complete model catalog without any outbound network access; set
// pricing_url in the model catalog config to sync from a remote or local
// datasheet instead (see embedded/README.md for the snapshot's provenance).
const EmbeddedPricingURL = "embedded://litellm"

//go:embed embedded/litellm_model_prices.json
var embeddedPricingData []byte
