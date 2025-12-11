#!/usr/bin/env bash
set -euo pipefail

# Prime the lake/catalog by running collector cache with provided args (filters optional)
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root/collector"

cache_dir="${AUSTENDER_CACHE_DIR:-$HOME/.cache/austender}"
echo "Priming lake into ${cache_dir}..."
go run . cache "$@"
echo "Reindexing lake catalog..."
go run . reindex-lake --cache-dir "$cache_dir"
