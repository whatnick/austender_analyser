#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PID_DIR="$REPO_ROOT/.cache"
PID_FILE="$PID_DIR/austender_server.pid"
LOG_FILE="$PID_DIR/austender_server.log"
mkdir -p "$PID_DIR"

# Start server in background (or reuse an existing one) so Task doesn't block.
if [[ -f "$PID_FILE" ]]; then
	OLD_PID="$(cat "$PID_FILE" 2>/dev/null || true)"
	if [[ -n "$OLD_PID" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
		echo "[local] server already running (pid=$OLD_PID)"
	else
		rm -f "$PID_FILE"
	fi
fi

if [[ ! -f "$PID_FILE" ]]; then
	nohup "$SCRIPT_DIR/run-server.sh" >"$LOG_FILE" 2>&1 &
	SERVER_PID=$!
	echo "$SERVER_PID" >"$PID_FILE"
	echo "[local] server started (pid=$SERVER_PID, log=$LOG_FILE)"
fi

# Give the server a moment to start
sleep 1

# Open frontend
"$SCRIPT_DIR/run-frontend.sh"

echo "[local] to stop the server: kill \"$(cat "$PID_FILE")\" && rm -f \"$PID_FILE\""
