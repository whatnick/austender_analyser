#!/usr/bin/env bash
set -euo pipefail

# Build collector binary into dist/collector
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"
mkdir -p dist
cd collector
go build -o "$repo_root/dist/collector" .

echo "collector binary written to $repo_root/dist/collector"
