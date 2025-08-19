#!/usr/bin/env bash
set -euo pipefail

# Run tests for the infra module
SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT/infra"
echo "[infra] go test ./..."
go test ./...
