#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="$ROOT/dist/releases"
BUILD="$ROOT/dist/build/windows"
VERSION="${VERSION:-1.1.7}"
TARGET="${TARGET:-windows}"
ZIP_PATH="$DIST/CodexTools-${VERSION}-windows-x64.zip"
PACKAGE_DIR="$BUILD/CodexTools-${VERSION}-windows-x64"

rm -rf "$BUILD"
mkdir -p "$BUILD" "$DIST" "$PACKAGE_DIR"

pushd "$ROOT/web" >/dev/null
npm install
npm run check
npm run vite:build
popd >/dev/null

pushd "$ROOT" >/dev/null
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.binaryRole=manager" -o "$PACKAGE_DIR/Codex++ 管理工具.exe" .
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.binaryRole=launcher" -o "$PACKAGE_DIR/Codex++.exe" .
popd >/dev/null

cp "$ROOT/assets/icons/codextools-1024.png" "$PACKAGE_DIR/codextools-icon.png"
cp "$ROOT/README.md" "$PACKAGE_DIR/README.md"
cp "$ROOT/README.zh-CN.md" "$PACKAGE_DIR/README.zh-CN.md"

cat > "$PACKAGE_DIR/START-HERE.txt" <<TXT
CodexTools Windows package

1. Double-click "Codex++ 管理工具.exe" to configure and manage Codex++.
2. Double-click "Codex++.exe" to launch Codex directly through the Codex++ launcher.
3. Set your Codex installation path in the manager if it is not detected automatically.
TXT

rm -f "$ZIP_PATH"
ditto -c -k --sequesterRsrc --keepParent "$PACKAGE_DIR" "$ZIP_PATH"
echo "$ZIP_PATH"
