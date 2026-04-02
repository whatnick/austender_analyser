#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if ! command -v clickhouse-local >/dev/null 2>&1; then
	echo "clickhouse-local is required for smoke tests" >&2
	exit 1
fi

echo "[smoke] collector ClickHouse query"
cd "$REPO_ROOT/collector"
go test ./... -run TestClickHouseLocalSmoke -count=1

echo "[smoke] server search path"
cd "$REPO_ROOT/server"
go test ./... -run TestSearchHandler_ClickHouseCacheSmoke -count=1