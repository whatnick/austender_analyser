#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Ensure scripts are executable
chmod +x "$SCRIPT_DIR"/test-collector.sh "$SCRIPT_DIR"/test-server.sh "$SCRIPT_DIR"/test-infra.sh

FAILURES=0

run() {
  local name=$1
  shift
  echo "===== Running $name tests ====="
  if ! "$@"; then
    echo "[ERROR] $name tests failed"
    FAILURES=$((FAILURES+1))
  else
    echo "[OK] $name tests passed"
  fi
  echo
}

run collector "$SCRIPT_DIR/test-collector.sh"
run server "$SCRIPT_DIR/test-server.sh"
run infra "$SCRIPT_DIR/test-infra.sh"

if [[ $FAILURES -ne 0 ]]; then
  echo "Some test suites failed: $FAILURES"
  exit 1
fi

echo "All tests passed"
