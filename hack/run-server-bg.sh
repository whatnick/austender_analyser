#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PID_DIR="$REPO_ROOT/.cache"
PID_FILE="$PID_DIR/austender_server.pid"
LOG_FILE="$PID_DIR/austender_server.log"
BIN_FILE="$PID_DIR/austender_server"
mkdir -p "$PID_DIR"

# Optimize cache reuse for local runs.
: "${AUSTENDER_CACHE_DIR:=$REPO_ROOT/.cache/austender}"
: "${AUSTENDER_USE_CACHE:=true}"
: "${AUSTENDER_CACHE_TZ:=Australia/Sydney}"
export AUSTENDER_CACHE_DIR AUSTENDER_USE_CACHE AUSTENDER_CACHE_TZ
mkdir -p "$AUSTENDER_CACHE_DIR"

# Reuse running server if PID file is valid.
if [[ -f "$PID_FILE" ]]; then
	OLD_PID="$(cat "$PID_FILE" 2>/dev/null || true)"
	if [[ -n "$OLD_PID" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
		echo "[server-bg] server already running (pid=$OLD_PID)"
		exit 0
	fi
	rm -f "$PID_FILE"
fi

# Build a stable binary so the PID we cache is the actual listener.
cd "$REPO_ROOT/server"
echo "[server-bg] building server binary -> $BIN_FILE"
go build -o "$BIN_FILE" .

echo "[server-bg] starting on :8080 (AUSTENDER_MODE=local)"
export AUSTENDER_MODE=local

nohup "$BIN_FILE" >"$LOG_FILE" 2>&1 &
SERVER_PID=$!
echo "$SERVER_PID" >"$PID_FILE"
echo "[server-bg] server started (pid=$SERVER_PID, log=$LOG_FILE)"
