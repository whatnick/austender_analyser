#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PID_DIR="$REPO_ROOT/.cache"
PID_FILE="$PID_DIR/austender_server.pid"
BIN_FILE="$PID_DIR/austender_server"

port_is_listening() {
	local port="$1"
	if command -v ss >/dev/null 2>&1; then
		# LISTEN sockets only; avoid needing process info.
		ss -ltn 2>/dev/null | awk '{print $1" "$4}' | grep -E '^LISTEN '".*:${port}"'$' >/dev/null 2>&1
		return $?
	fi

	if command -v lsof >/dev/null 2>&1; then
		lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
		return $?
	fi

	if command -v netstat >/dev/null 2>&1; then
		netstat -ltn 2>/dev/null | awk '{print $4" "$6}' | grep -E ".*:${port} .*LISTEN" >/dev/null 2>&1
		return $?
	fi

	echo "[stop-server] no ss/lsof/netstat available to verify port ${port}" >&2
	return 2
}

wait_for_pid_exit() {
	local pid="$1"
	local timeout_seconds="$2"
	local i
	for ((i=0; i<timeout_seconds; i++)); do
		if ! kill -0 "$pid" 2>/dev/null; then
			return 0
		fi
		sleep 1
	done
	return 1
}

wait_for_port_free() {
	local port="$1"
	local timeout_seconds="$2"
	local i
	for ((i=0; i<timeout_seconds; i++)); do
		if ! port_is_listening "$port"; then
			return 0
		fi
		sleep 1
	done
	return 1
}

print_port_diagnostics() {
	local port="$1"
	if command -v ss >/dev/null 2>&1; then
		echo "[stop-server] ss output (best-effort):" >&2
		ss -ltnp 2>/dev/null | grep -E ":${port}\\b" >&2 || true
		return
	fi

	if command -v lsof >/dev/null 2>&1; then
		echo "[stop-server] lsof output:" >&2
		lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2 || true
		return
	fi

	if command -v netstat >/dev/null 2>&1; then
		echo "[stop-server] netstat output:" >&2
		netstat -ltnp 2>/dev/null | grep -E ":${port}\\b" >&2 || true
		return
	fi
}

try_stop_by_port_if_ours() {
	local port="$1"
	# Only attempt this if the cached binary exists, to avoid killing unrelated listeners.
	if [[ ! -x "$BIN_FILE" ]]; then
		return 1
	fi
	if ! command -v ss >/dev/null 2>&1; then
		return 1
	fi
	local pid
	pid="$(ss -ltnp 2>/dev/null | grep -E ":${port}\\b" | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | head -n 1)"
	if [[ -z "${pid:-}" ]]; then
		return 1
	fi
	local args
	args="$(ps -p "$pid" -o args= 2>/dev/null || true)"
	if [[ "$args" == "$BIN_FILE"* ]]; then
		echo "[stop-server] stopping listener on :${port} (pid=$pid, bin=$BIN_FILE)" >&2
		kill -TERM "$pid" 2>/dev/null || true
		kill -TERM -- "-$pid" 2>/dev/null || true
		wait_for_port_free "$port" 10 || true
		return 0
	fi
	return 1
}

mkdir -p "$PID_DIR"

if [[ -f "$PID_FILE" ]]; then
	PID="$(cat "$PID_FILE" 2>/dev/null || true)"
	if [[ -n "${PID:-}" ]] && kill -0 "$PID" 2>/dev/null; then
		echo "[stop-server] stopping server (pid=$PID)"
		# `go run` often spawns a child process to run the compiled binary.
		# Terminate the whole process group to ensure the listener goes away.
		kill -TERM "$PID" 2>/dev/null || true
		kill -TERM -- "-$PID" 2>/dev/null || true

		if ! wait_for_pid_exit "$PID" 10; then
			echo "[stop-server] server did not exit after 10s; sending SIGKILL (pid=$PID)" >&2
			kill -KILL "$PID" 2>/dev/null || true
			kill -KILL -- "-$PID" 2>/dev/null || true
			wait_for_pid_exit "$PID" 5 || true
		fi
	else
		echo "[stop-server] pid file present but process not running; cleaning up"
	fi
else
	echo "[stop-server] no pid file found at $PID_FILE"
fi

# Verify port 8080 is not listening.
if ! wait_for_port_free 8080 10; then
	# If we don't have a PID file, try a safe best-effort stop when it's clearly our cached binary.
	if [[ ! -f "$PID_FILE" ]] && try_stop_by_port_if_ours 8080; then
		if wait_for_port_free 8080 10; then
			echo "[stop-server] ok: nothing listening on :8080"
			exit 0
		fi
	fi

	echo "[stop-server] port 8080 is still listening" >&2
	print_port_diagnostics 8080
	echo "[stop-server] if this isn't our server, stop that process and retry" >&2
	exit 1
fi

if [[ -f "$PID_FILE" ]]; then
	rm -f "$PID_FILE"
fi

echo "[stop-server] ok: nothing listening on :8080"
