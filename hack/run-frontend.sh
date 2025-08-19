#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

FRONTEND_FILE="$REPO_ROOT/frontend/index.html"

if [[ ! -f "$FRONTEND_FILE" ]]; then
  echo "frontend/index.html not found"
  exit 1
fi

echo "[frontend] opening $FRONTEND_FILE"
# Try available openers
if command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$FRONTEND_FILE" >/dev/null 2>&1 &
elif command -v gnome-open >/dev/null 2>&1; then
  gnome-open "$FRONTEND_FILE" >/dev/null 2>&1 &
else
  echo "Open $FRONTEND_FILE in your browser."
fi
