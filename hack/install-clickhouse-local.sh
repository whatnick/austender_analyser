#!/usr/bin/env bash
set -euo pipefail

VERSION="${CLICKHOUSE_LOCAL_VERSION:-26.3.3.20}"
RELEASE_TAG="${CLICKHOUSE_LOCAL_RELEASE_TAG:-v${VERSION}-lts}"
ASSET="clickhouse-common-static-${VERSION}-amd64.tgz"
INSTALL_ROOT="${CLICKHOUSE_LOCAL_INSTALL_DIR:-$HOME/.local/share/clickhouse-local}"
BIN_DIR="${CLICKHOUSE_LOCAL_BIN_DIR:-$HOME/.local/bin}"
TARGET_BIN="$INSTALL_ROOT/clickhouse"
LAUNCHER="$BIN_DIR/clickhouse-local"

if command -v clickhouse-local >/dev/null 2>&1; then
	if clickhouse-local --version 2>/dev/null | grep -q "$VERSION"; then
		echo "[clickhouse-local] version $VERSION already available"
		exit 0
	fi
fi

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

archive="$tmpdir/$ASSET"
url="https://github.com/ClickHouse/ClickHouse/releases/download/${RELEASE_TAG}/${ASSET}"

echo "[clickhouse-local] downloading $url"
curl -fsSL "$url" -o "$archive"
tar -xzf "$archive" -C "$tmpdir"

src_bin="$tmpdir/clickhouse-common-static-${VERSION}/usr/bin/clickhouse"
install -m 0755 "$src_bin" "$TARGET_BIN"

cat > "$LAUNCHER" <<EOF
#!/usr/bin/env bash
exec "$TARGET_BIN" local "\$@"
EOF
chmod +x "$LAUNCHER"

echo "[clickhouse-local] installed to $LAUNCHER"
"$LAUNCHER" --version