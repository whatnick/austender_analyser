#!/usr/bin/env bash
set -euo pipefail

# Run tests for the collector module
SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT/collector"
echo "[collector] go test ./..."
go test ./...
