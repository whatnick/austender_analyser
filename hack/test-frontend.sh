#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/frontend"

if ! command -v python3 >/dev/null 2>&1; then
	echo "python3 is required for frontend smoke tests" >&2
	exit 1
fi

port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

log_file="$(mktemp)"
cleanup() {
	if [[ -n "${server_pid:-}" ]]; then
		kill "$server_pid" >/dev/null 2>&1 || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -f "$log_file"
}
trap cleanup EXIT

cd "$FRONTEND_DIR"
python3 -m http.server "$port" >"$log_file" 2>&1 &
server_pid=$!
sleep 1

index_html="$(curl -fsS "http://127.0.0.1:${port}/index.html")"
search_html="$(curl -fsS "http://127.0.0.1:${port}/search.html")"

grep -q 'config.local.js' <<<"$index_html"
grep -q '/api/llm' <<<"$index_html"
grep -q 'ClickHouse-backed cache' <<<"$index_html"

grep -q '/api/search' <<<"$search_html"
grep -q 'ClickHouse-backed Parquet lake cache' <<<"$search_html"

echo "[frontend] smoke test passed"