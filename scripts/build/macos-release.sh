#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="$ROOT/dist/releases"
BUILD="$ROOT/dist/build/macos"
VERSION="${VERSION:-1.1.8}"
ARCH="$(uname -m)"
case "$ARCH" in
  arm64|aarch64) ARCH_LABEL="arm64" ;;
  x86_64|amd64) ARCH_LABEL="x64" ;;
  *) ARCH_LABEL="$ARCH" ;;
esac

APP_NAME="Codex++ 管理工具"
LAUNCHER_NAME="Codex++"
APP_DIR="$BUILD/$APP_NAME.app"
LAUNCHER_APP_DIR="$BUILD/$LAUNCHER_NAME.app"
ZIP_PATH="$DIST/CodexTools-${VERSION}-macos-${ARCH_LABEL}.zip"
PACKAGE_DIR="$BUILD/CodexTools-${VERSION}-macos-${ARCH_LABEL}"

rm -rf "$BUILD"
mkdir -p "$BUILD" "$DIST"

pushd "$ROOT/web" >/dev/null
npm install
npm run check
npm run vite:build
popd >/dev/null

pushd "$ROOT" >/dev/null
go build -ldflags "-X main.binaryRole=manager" -o "$BUILD/codextools" .
go build -ldflags "-X main.binaryRole=launcher" -o "$BUILD/codextools-launcher" .
popd >/dev/null

create_app() {
  local app_dir="$1"
  local display_name="$2"
  local executable_name="$3"
  local binary_path="$4"
  local bundle_id="$5"
  local lsui="$6"

  mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"
  cp "$binary_path" "$app_dir/Contents/MacOS/$executable_name"
  cp "$ROOT/assets/icons/codextools.icns" "$app_dir/Contents/Resources/codextools.icns"
  chmod +x "$app_dir/Contents/MacOS/$executable_name"
  cat > "$app_dir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>$display_name</string>
  <key>CFBundleDisplayName</key>
  <string>$display_name</string>
  <key>CFBundleIdentifier</key>
  <string>$bundle_id</string>
  <key>CFBundleVersion</key>
  <string>$VERSION</string>
  <key>CFBundleShortVersionString</key>
  <string>$VERSION</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleExecutable</key>
  <string>$executable_name</string>
  <key>CFBundleIconFile</key>
  <string>codextools.icns</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
  <key>LSUIElement</key>
  <$lsui/>
</dict>
</plist>
PLIST
}

create_app "$APP_DIR" "$APP_NAME" "codextools" "$BUILD/codextools" "com.hereww.codextools" "false"
create_app "$LAUNCHER_APP_DIR" "$LAUNCHER_NAME" "codextools-launcher" "$BUILD/codextools-launcher" "com.hereww.codextools.launcher" "true"

cp "$BUILD/codextools-launcher" "$APP_DIR/Contents/MacOS/codextools-launcher"
cp "$BUILD/codextools" "$LAUNCHER_APP_DIR/Contents/MacOS/codextools"
mkdir -p "$PACKAGE_DIR"
cp -R "$LAUNCHER_APP_DIR" "$PACKAGE_DIR/"
cp -R "$APP_DIR" "$PACKAGE_DIR/"
cp "$ROOT/README.md" "$PACKAGE_DIR/README.md"
cp "$ROOT/README.zh-CN.md" "$PACKAGE_DIR/README.zh-CN.md"
cat > "$PACKAGE_DIR/START-HERE.txt" <<TXT
CodexTools macOS package

1. Open "Codex++ 管理工具.app" to configure and manage Codex++.
2. Open "Codex++.app" to launch Codex directly through the Codex++ launcher.
3. If macOS blocks the app, right-click the app and choose Open.
TXT

rm -f "$ZIP_PATH"
ditto -c -k --sequesterRsrc --keepParent "$PACKAGE_DIR" "$ZIP_PATH"
echo "$ZIP_PATH"
