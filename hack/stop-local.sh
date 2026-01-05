#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

# "local" = server (frontend is a static file open).
"$SCRIPT_DIR/stop-server.sh"
