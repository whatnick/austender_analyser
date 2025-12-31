#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT/server"
echo "[server] starting on :8080 (AUSTENDER_MODE=local)"
export AUSTENDER_MODE=local

# Optimize cache reuse for local runs.
# - Collector cache persists across runs in AUSTENDER_CACHE_DIR.
# - Server adds an in-memory per-day cache keyed by AUSTENDER_CACHE_TZ.
: "${AUSTENDER_CACHE_DIR:=$REPO_ROOT/.cache/austender}"
: "${AUSTENDER_USE_CACHE:=true}"
: "${AUSTENDER_CACHE_TZ:=Australia/Sydney}"
export AUSTENDER_CACHE_DIR AUSTENDER_USE_CACHE AUSTENDER_CACHE_TZ
mkdir -p "$AUSTENDER_CACHE_DIR"

exec go run .
