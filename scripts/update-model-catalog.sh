#!/usr/bin/env bash
# Refresh the embedded model catalog snapshot from LiteLLM's
# model_prices_and_context_window.json (MIT — see THIRD_PARTY_LICENSES.md).
#
# Usage: scripts/update-model-catalog.sh
# After running: update the source-commit line in
# framework/modelcatalog/datasheet/embedded/README.md, run the datasheet
# tests, and commit both files together.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="$REPO_ROOT/framework/modelcatalog/datasheet/embedded/litellm_model_prices.json"
SOURCE_URL="https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

echo "Fetching $SOURCE_URL"
curl -fsSL "$SOURCE_URL" -o "$TMP"

# Validate and minify; refuse suspicious shrinkage so a bad upstream state
# can't silently gut the catalog.
python3 - "$TMP" "$TARGET" <<'EOF'
import json, sys
tmp, target = sys.argv[1], sys.argv[2]
data = json.load(open(tmp))
if "sample_spec" not in data:
    sys.exit("refusing update: sample_spec marker missing — format may have changed")
if len(data) < 2500:
    sys.exit(f"refusing update: only {len(data)} entries (expected >= 2500)")
json.dump(data, open(target, "w"), separators=(",", ":"), sort_keys=True)
print(f"wrote {target}: {len(data)} entries")
EOF

echo "Latest upstream commit (update embedded/README.md with this):"
curl -fsSL --max-time 10 "https://api.github.com/repos/BerriAI/litellm/commits/main" \
  | python3 -c "import json,sys; c=json.load(sys.stdin); print(' ', c['sha'][:12], c['commit']['committer']['date'])" \
  || echo "  (could not fetch — look it up manually)"

echo "Now run: cd framework && go test ./modelcatalog/datasheet/"
