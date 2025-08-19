#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Start server in background
"$SCRIPT_DIR/run-server.sh" &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null || true' EXIT

# Give the server a moment to start
sleep 1

# Open frontend
"$SCRIPT_DIR/run-frontend.sh"

echo "Server PID: $SERVER_PID"
wait $SERVER_PID
