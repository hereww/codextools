#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="$ROOT/dist/releases"
BUILD="$ROOT/dist/build/macos"
VERSION="${VERSION:-1.1.18}"
TARGET_ARCHES="${TARGET_ARCHES:-arm64 amd64}"
export COPYFILE_DISABLE=1

APP_NAME="Codex++ 管理工具"
LAUNCHER_NAME="Codex++"

rm -rf "$BUILD"
mkdir -p "$BUILD" "$DIST"

arch_label() {
  case "$1" in
    arm64|aarch64) printf 'arm64' ;;
    amd64|x86_64) printf 'x64' ;;
    *) printf '%s' "$1" ;;
  esac
}

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

write_start_here() {
  local target="$1"
  local label="$2"
  cat > "$target" <<TXT
CodexTools macOS package (${label})

1. Open "Codex++ 管理工具.app" to configure and manage Codex++.
2. Open "Codex++.app" to launch Codex directly through the Codex++ launcher.
3. The installer package installs both apps into /Applications.
4. If macOS blocks the app because this build is not notarized, run:

   xattr -cr "/Applications/Codex++ 管理工具.app"
   xattr -cr "/Applications/Codex++.app"

5. You can also right-click the app and choose Open.
TXT
}

build_arch() {
  local goarch="$1"
  local label
  label="$(arch_label "$goarch")"
  local arch_build="$BUILD/$label"
  local app_dir="$arch_build/$APP_NAME.app"
  local launcher_app_dir="$arch_build/$LAUNCHER_NAME.app"
  local package_name="CodexTools-${VERSION}-macos-${label}"
  local package_dir="$arch_build/$package_name"
  local zip_path="$DIST/${package_name}.zip"
  local pkg_root="$arch_build/pkg-root"
  local component_pkg="$arch_build/${package_name}-component.pkg"
  local pkg_path="$DIST/${package_name}.pkg"
  local pkg_resources="$arch_build/pkg-resources"
  local distribution_xml="$arch_build/distribution.xml"

  rm -rf "$arch_build"
  mkdir -p "$arch_build" "$package_dir"

  pushd "$ROOT" >/dev/null
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 go build -ldflags "-X main.binaryRole=manager" -o "$arch_build/codextools" .
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 go build -ldflags "-X main.binaryRole=launcher" -o "$arch_build/codextools-launcher" .
  popd >/dev/null

  create_app "$app_dir" "$APP_NAME" "codextools" "$arch_build/codextools" "com.hereww.codextools" "false"
  create_app "$launcher_app_dir" "$LAUNCHER_NAME" "codextools-launcher" "$arch_build/codextools-launcher" "com.hereww.codextools.launcher" "true"

  cp "$arch_build/codextools-launcher" "$app_dir/Contents/MacOS/codextools-launcher"
  cp "$arch_build/codextools" "$launcher_app_dir/Contents/MacOS/codextools"
  cp -R "$launcher_app_dir" "$package_dir/"
  cp -R "$app_dir" "$package_dir/"
  cp "$ROOT/README.md" "$package_dir/README.md"
  cp "$ROOT/README.zh-CN.md" "$package_dir/README.zh-CN.md"
  write_start_here "$package_dir/START-HERE.txt" "$label"

  rm -f "$zip_path"
  ditto -c -k --norsrc --keepParent "$package_dir" "$zip_path"

  rm -rf "$pkg_root"
  mkdir -p "$pkg_root/Applications" "$pkg_resources"
  cp -R "$launcher_app_dir" "$pkg_root/Applications/"
  cp -R "$app_dir" "$pkg_root/Applications/"
  xattr -cr "$pkg_root"
  find "$pkg_root" -name '._*' -delete
  cat > "$pkg_resources/ReadMe.html" <<HTML
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <style>
      body { font: -apple-system-body; line-height: 1.5; }
      code { background: #f1f5f9; border-radius: 6px; padding: 2px 5px; }
      pre { background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 10px; padding: 12px; }
    </style>
  </head>
  <body>
    <h1>CodexTools ${VERSION}</h1>
    <p>This installer places <strong>Codex++ 管理工具.app</strong> and <strong>Codex++.app</strong> in <code>/Applications</code>.</p>
    <p>If macOS blocks the apps because this build is not notarized, open Terminal and run:</p>
    <pre>xattr -cr "/Applications/Codex++ 管理工具.app"
xattr -cr "/Applications/Codex++.app"</pre>
    <p>You can also right-click each app and choose <strong>Open</strong>.</p>
  </body>
</html>
HTML
  pkgbuild \
    --root "$pkg_root" \
    --identifier "com.hereww.codextools.pkg.${label}" \
    --version "$VERSION" \
    --install-location "/" \
    "$component_pkg" >/dev/null
  cat > "$distribution_xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="1">
  <title>CodexTools ${VERSION}</title>
  <readme file="ReadMe.html"/>
  <options customize="never" require-scripts="false"/>
  <choices-outline>
    <line choice="default"/>
  </choices-outline>
  <choice id="default" title="CodexTools">
    <pkg-ref id="com.hereww.codextools.pkg.${label}"/>
  </choice>
  <pkg-ref id="com.hereww.codextools.pkg.${label}" version="${VERSION}" onConclusion="none">${package_name}-component.pkg</pkg-ref>
</installer-gui-script>
XML
  productbuild \
    --distribution "$distribution_xml" \
    --package-path "$arch_build" \
    --resources "$pkg_resources" \
    "$pkg_path" >/dev/null

  echo "$pkg_path"
  echo "$zip_path"
}

pushd "$ROOT/web" >/dev/null
npm install
npm run check
npm run vite:build
popd >/dev/null

for arch in $TARGET_ARCHES; do
  build_arch "$arch"
done
