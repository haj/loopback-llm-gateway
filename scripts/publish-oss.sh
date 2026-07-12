#!/usr/bin/env bash
# Export the current committed tree into a fresh single-commit repository for
# public release, without carrying over this repository's git history.
#
# Usage: scripts/publish-oss.sh [target-dir]
#   target-dir defaults to ../loopback-gateway-public
#
# The private repository (full history) stays untouched; run this again any
# time to regenerate the export. Push the result to the public GitHub repo:
#   cd <target-dir>
#   git remote add origin git@github.com:<org>/loopback-gateway.git
#   git push -u origin main
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-"$REPO_ROOT/../loopback-gateway-public"}"

cd "$REPO_ROOT"

if ! git diff-index --quiet HEAD --; then
  echo "error: uncommitted changes present; commit or stash first" >&2
  exit 1
fi

if [ -e "$TARGET" ] && [ -n "$(ls -A "$TARGET" 2>/dev/null)" ]; then
  echo "error: target $TARGET exists and is not empty; remove it first" >&2
  exit 1
fi

mkdir -p "$TARGET"
echo "Exporting tracked tree of HEAD ($(git rev-parse --short HEAD)) to $TARGET"
git archive HEAD | tar -x -C "$TARGET"

echo "Running release gates..."
FAIL=0

# Gate 1: no development-workflow artifacts in the export.
for f in CLAUDE.md AGENTS.md .claude .remember docs/plans; do
  if [ -e "$TARGET/$f" ]; then
    echo "GATE FAIL: $f present in export" >&2
    FAIL=1
  fi
done

# Gate 2: no AI-assistant workflow traces in text files (provider/client
# integration docs legitimately mention Claude; match workflow-only markers).
TRACE_GREP=(grep -rn --exclude-dir=node_modules --exclude=publish-oss.sh -e 'Co-Authored-By: Claude' -e 'claude\.ai/code' -e 'Generated with \[Claude' "$TARGET")
if "${TRACE_GREP[@]}" >/dev/null 2>&1; then
  echo "GATE FAIL: AI-workflow markers found:" >&2
  "${TRACE_GREP[@]}" | head -20 >&2
  FAIL=1
fi

# Gate 3: required OSS files present.
for f in LICENSE NOTICE README.md SECURITY.md CODE_OF_CONDUCT.md CONTRIBUTING.md THIRD_PARTY_LICENSES.md; do
  if [ ! -f "$TARGET/$f" ]; then
    echo "GATE FAIL: missing $f" >&2
    FAIL=1
  fi
done

# Gate 4: secret scan (requires gitleaks; install with `brew install gitleaks`).
if command -v gitleaks >/dev/null 2>&1; then
  if ! gitleaks dir "$TARGET" --no-banner --exit-code 1 >/dev/null 2>&1; then
    echo "NOTE: gitleaks reported findings; review them (docs placeholders are expected):" >&2
    gitleaks dir "$TARGET" --no-banner 2>&1 | tail -5 >&2 || true
  fi
else
  echo "WARNING: gitleaks not installed; skipping secret scan gate" >&2
fi

if [ "$FAIL" -ne 0 ]; then
  echo "Release gates failed; aborting before git init. Export left at $TARGET for inspection." >&2
  exit 1
fi

cd "$TARGET"
git init -q -b main
git add -A
git commit -q -m "Initial public release of Loopback Gateway

Loopback Gateway is an open-source, self-hostable LLM gateway based on
Bifrost (https://github.com/maximhq/bifrost), extended with guardrails,
PII redaction, RBAC, audit logging, SSO/SCIM, circuit breaking, and data
connectors. See NOTICE and THIRD_PARTY_LICENSES.md for attribution."

echo
echo "Done. Fresh repository created at $TARGET"
echo "  commit: $(git rev-parse --short HEAD) ($(git ls-files | wc -l | tr -d ' ') files)"
echo
echo "Next steps:"
echo "  1. Review: cd $TARGET && git show --stat HEAD | head"
echo "  2. Create the public GitHub repo, then:"
echo "     git remote add origin git@github.com:<org>/loopback-gateway.git"
echo "     git push -u origin main"
