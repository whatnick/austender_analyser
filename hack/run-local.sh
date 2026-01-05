#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PID_DIR="$REPO_ROOT/.cache"
PID_FILE="$PID_DIR/austender_server.pid"
LOG_FILE="$PID_DIR/austender_server.log"
mkdir -p "$PID_DIR"

# Prefer OpenAI locally when credentials are available. Attempt a simple env file lookup
# for convenience so users can drop their key in ~/.config/austender/openai.key or
# ~/.config/austender/openai.env (export format) instead of exporting it manually.
if [[ -z "${OPENAI_API_KEY:-}" ]]; then
	OPENAI_KEY_FILE="${AUSTENDER_OPENAI_KEY_FILE:-$HOME/.config/austender/openai.key}"
	OPENAI_ENV_FILE="${AUSTENDER_OPENAI_ENV_FILE:-$HOME/.config/austender/openai.env}"
	if [[ -f "$OPENAI_ENV_FILE" ]]; then
		source "$OPENAI_ENV_FILE"
		if [[ -n "${OPENAI_API_KEY:-}" ]]; then
			echo "[local] loaded OPENAI_API_KEY from $OPENAI_ENV_FILE"
		fi
	elif [[ -f "$OPENAI_KEY_FILE" ]]; then
		OPENAI_API_KEY="$(<"$OPENAI_KEY_FILE")"
		export OPENAI_API_KEY
		if [[ -n "$OPENAI_API_KEY" ]]; then
			echo "[local] loaded OPENAI_API_KEY from $OPENAI_KEY_FILE"
		fi
	fi
fi

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
	"$SCRIPT_DIR/run-server-bg.sh"
fi

# Give the server a moment to start
sleep 1

# Open frontend
"$SCRIPT_DIR/run-frontend.sh"

echo "[local] to stop the server: kill \"$(cat "$PID_FILE")\" && rm -f \"$PID_FILE\""
