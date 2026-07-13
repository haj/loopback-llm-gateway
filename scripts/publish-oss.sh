#!/usr/bin/env bash
# Export the current committed tree into a clean repository for public
# release, without carrying over this repository's git history.
#
# Usage: scripts/publish-oss.sh [target-dir] [--push -m "commit message"]
#   target-dir defaults to ../loopback-gateway-public
#
# Without --push: creates a fresh single-commit repository at target-dir
# (first-publish flow; add a remote and push manually).
#
# With --push: syncs the export into a persistent clone of PUBLIC_REMOTE
# (default git@github.com:haj/loopback-llm-gateway.git) kept at target-dir,
# commits the delta as one commit with the given message, and pushes. The
# clone is reused across releases, so only the incremental fetch/push hits
# the network. This is the routine release flow.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PUBLIC_REMOTE="${PUBLIC_REMOTE:-git@github.com:haj/loopback-llm-gateway.git}"

TARGET=""
PUSH=0
MSG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --push) PUSH=1; shift ;;
    -m) MSG="${2:?-m requires a message}"; shift 2 ;;
    *) TARGET="$1"; shift ;;
  esac
done
TARGET="${TARGET:-"$REPO_ROOT/../loopback-gateway-public"}"
if [ "$PUSH" -eq 1 ] && [ -z "$MSG" ]; then
  echo "error: --push requires -m \"commit message\"" >&2
  exit 1
fi

cd "$REPO_ROOT"

if ! git diff-index --quiet HEAD --; then
  echo "error: uncommitted changes present; commit or stash first" >&2
  exit 1
fi

# Export the tracked tree of HEAD to a scratch directory and run the release
# gates there, before anything touches the target.
EXPORT="$(mktemp -d)"
trap 'rm -rf "$EXPORT"' EXIT

echo "Exporting tracked tree of HEAD ($(git rev-parse --short HEAD))"
git archive HEAD | tar -x -C "$EXPORT"

echo "Running release gates..."
FAIL=0

# Gate 1: no development-workflow artifacts in the export.
for f in CLAUDE.md AGENTS.md .claude .remember docs/plans; do
  if [ -e "$EXPORT/$f" ]; then
    echo "GATE FAIL: $f present in export" >&2
    FAIL=1
  fi
done

# Gate 2: no AI-assistant workflow traces in text files (provider/client
# integration docs legitimately mention Claude; match workflow-only markers).
TRACE_GREP=(grep -rn --exclude-dir=node_modules --exclude=publish-oss.sh -e 'Co-Authored-By: Claude' -e 'claude\.ai/code' -e 'Generated with \[Claude' "$EXPORT")
if "${TRACE_GREP[@]}" >/dev/null 2>&1; then
  echo "GATE FAIL: AI-workflow markers found:" >&2
  "${TRACE_GREP[@]}" | head -20 >&2
  FAIL=1
fi

# Gate 3: required OSS files present.
for f in LICENSE NOTICE README.md SECURITY.md CODE_OF_CONDUCT.md CONTRIBUTING.md THIRD_PARTY_LICENSES.md; do
  if [ ! -f "$EXPORT/$f" ]; then
    echo "GATE FAIL: missing $f" >&2
    FAIL=1
  fi
done

# Gate 4: secret scan (requires gitleaks; install with `brew install gitleaks`).
if command -v gitleaks >/dev/null 2>&1; then
  if ! gitleaks dir "$EXPORT" --no-banner --exit-code 1 >/dev/null 2>&1; then
    echo "NOTE: gitleaks reported findings; review them (docs placeholders are expected):" >&2
    gitleaks dir "$EXPORT" --no-banner 2>&1 | tail -5 >&2 || true
  fi
else
  echo "WARNING: gitleaks not installed; skipping secret scan gate" >&2
fi

if [ "$FAIL" -ne 0 ]; then
  KEEP="$REPO_ROOT/../loopback-gateway-export-failed"
  rm -rf "$KEEP" && mv "$EXPORT" "$KEEP"
  trap - EXIT
  echo "Release gates failed. Export left at $KEEP for inspection." >&2
  exit 1
fi

if [ "$PUSH" -eq 1 ]; then
  # Routine release: sync the export into a persistent clone of the public
  # repo and push the delta as one commit.
  if [ -d "$TARGET/.git" ]; then
    if [ "$(git -C "$TARGET" remote get-url origin 2>/dev/null)" != "$PUBLIC_REMOTE" ]; then
      echo "error: $TARGET exists but origin is not $PUBLIC_REMOTE" >&2
      exit 1
    fi
    echo "Fetching public main (incremental)..."
    git -C "$TARGET" fetch origin main
    git -C "$TARGET" checkout -q -B main origin/main
    git -C "$TARGET" reset -q --hard origin/main
    git -C "$TARGET" clean -qfdx
  else
    echo "Cloning $PUBLIC_REMOTE (first run with this target)..."
    rm -rf "$TARGET"
    git clone -q "$PUBLIC_REMOTE" "$TARGET"
  fi

  rsync -a --delete --exclude=.git "$EXPORT/" "$TARGET/"
  git -C "$TARGET" add -A
  if git -C "$TARGET" diff --cached --quiet; then
    echo "No changes vs public main — nothing to publish."
    exit 0
  fi
  git -C "$TARGET" commit -q -m "$MSG"
  git -C "$TARGET" push origin main
  echo
  echo "Published $(git -C "$TARGET" rev-parse --short HEAD) to $PUBLIC_REMOTE"
  git -C "$TARGET" show --stat HEAD | head -5
  exit 0
fi

# First-publish flow: fresh single-commit repository at target-dir.
if [ -e "$TARGET" ] && [ -n "$(ls -A "$TARGET" 2>/dev/null)" ]; then
  echo "error: target $TARGET exists and is not empty; remove it first" >&2
  exit 1
fi
mkdir -p "$TARGET"
rsync -a "$EXPORT/" "$TARGET/"
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
