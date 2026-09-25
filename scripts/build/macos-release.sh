#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="$ROOT/dist/releases"
BUILD="$ROOT/dist/build/macos"
VERSION="${VERSION:-1.2.10}"
ARTIFACT_VERSION="${ARTIFACT_VERSION:-$VERSION}"
TARGET_ARCHES="${TARGET_ARCHES:-arm64 amd64}"
MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-12.0}"
NPM_CACHE="${NPM_CACHE:-$ROOT/dist/cache/npm}"
export COPYFILE_DISABLE=1
export MACOSX_DEPLOYMENT_TARGET

macos_min_flag="-mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
export CGO_CFLAGS="${CGO_CFLAGS:-} ${macos_min_flag}"
export CGO_CXXFLAGS="${CGO_CXXFLAGS:-} ${macos_min_flag}"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} ${macos_min_flag}"

APP_NAME="ChatGPT Codex 管理工具"
LAUNCHER_NAME="ChatGPT Codex"

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
  <string>$MACOSX_DEPLOYMENT_TARGET</string>
  <key>LSUIElement</key>
  <$lsui/>
</dict>
</plist>
PLIST
}

mach_o_minos() {
  local binary="$1"
  local minos=""

  if command -v vtool >/dev/null 2>&1; then
    minos="$(vtool -show-build "$binary" 2>/dev/null | awk '/minos/ { print $2; exit }')"
  fi
  if [[ -z "$minos" ]] && command -v otool >/dev/null 2>&1; then
    minos="$(otool -l "$binary" | awk '
      /LC_BUILD_VERSION/ { in_build = 1; in_min = 0; next }
      in_build && /minos/ { print $2; exit }
      /LC_VERSION_MIN_MACOSX/ { in_min = 1; in_build = 0; next }
      in_min && /version/ { print $2; exit }
    ')"
  fi

  printf '%s' "$minos"
}

verify_macos_deployment_target() {
  local app_dir="$1"
  local binary
  while IFS= read -r binary; do
    local minos
    minos="$(mach_o_minos "$binary")"
    if [[ "$minos" != "$MACOSX_DEPLOYMENT_TARGET" ]]; then
      echo "error: $binary has macOS minos '$minos', expected '$MACOSX_DEPLOYMENT_TARGET'" >&2
      echo "Set MACOSX_DEPLOYMENT_TARGET before building so macOS 15 and older supported targets can launch." >&2
      return 1
    fi
  done < <(find "$app_dir/Contents/MacOS" -type f -perm -111 -print)
}

write_start_here() {
  local target="$1"
  local label="$2"
  cat > "$target" <<TXT
ChatGPT Codex Tools macOS package (${label})

1. Open "ChatGPT Codex 管理工具.app" to configure and manage ChatGPT Codex.
2. Open "ChatGPT Codex.app" to launch ChatGPT through the ChatGPT Codex launcher.
3. The installer package installs both apps into /Applications.
4. The macOS packages are unsigned community builds, including the pkg installer.
   If macOS blocks the first launch, run:

   xattr -cr "/Applications/ChatGPT Codex 管理工具.app"
   xattr -cr "/Applications/ChatGPT Codex.app"
   xattr -cr "/Applications/Codex++ 管理工具.app"
   xattr -cr "/Applications/Codex++.app"

5. You can also right-click the app and choose Open.
TXT
}

copy_app_bundle() {
  local source_app="$1"
  local target_app="$2"

  rm -rf "$target_app"
  ditto --norsrc --noextattr --noacl --noqtn "$source_app" "$target_app"
  xattr -cr "$target_app" 2>/dev/null || true
  find "$target_app" -name '._*' -delete
}

plist_set_or_add() {
  local plist="$1"
  local path="$2"
  local type="$3"
  local value="$4"

  /usr/libexec/PlistBuddy -c "Set $path $value" "$plist" >/dev/null 2>&1 || \
    /usr/libexec/PlistBuddy -c "Add $path $type $value" "$plist" >/dev/null
}

force_component_bundle_policy() {
  local plist="$1"
  local index=0

  while /usr/libexec/PlistBuddy -c "Print :$index" "$plist" >/dev/null 2>&1; do
    plist_set_or_add "$plist" ":$index:BundleIsRelocatable" bool false
    plist_set_or_add "$plist" ":$index:BundleIsVersionChecked" bool false
    plist_set_or_add "$plist" ":$index:BundleOverwriteAction" string upgrade
    index=$((index + 1))
  done

  if [[ "$index" -eq 0 ]]; then
    echo "error: pkgbuild did not detect any app bundles in $plist" >&2
    return 1
  fi
}

verify_pkg_payload_root() {
  local pkg_root="$1"
  local launcher_plist="$pkg_root/Applications/$LAUNCHER_NAME.app/Contents/Info.plist"
  local manager_plist="$pkg_root/Applications/$APP_NAME.app/Contents/Info.plist"

  if [[ ! -s "$launcher_plist" ]]; then
    echo "error: pkg payload missing /Applications/$LAUNCHER_NAME.app" >&2
    return 1
  fi
  if [[ ! -s "$manager_plist" ]]; then
    echo "error: pkg payload missing /Applications/$APP_NAME.app" >&2
    return 1
  fi
  if ! find "$pkg_root/Applications" -mindepth 1 -maxdepth 1 -name '*.app' -print -quit | grep -q .; then
    echo "error: pkg payload has no app bundles under /Applications" >&2
    return 1
  fi
}

build_arch() {
  local goarch="$1"
  local label
  label="$(arch_label "$goarch")"
  local arch_build="$BUILD/$label"
  local app_dir="$arch_build/$APP_NAME.app"
  local launcher_app_dir="$arch_build/$LAUNCHER_NAME.app"
  local package_name="ChatGPT-Codex-Tools-${ARTIFACT_VERSION}-macos-${label}"
  local package_dir="$arch_build/$package_name"
  local zip_path="$DIST/${package_name}.zip"
  local pkg_root="$arch_build/pkg-root"
  local component_pkg="$arch_build/${package_name}-component.pkg"
  local component_plist="$arch_build/component.plist"
  local pkg_path="$DIST/${package_name}.pkg"
  local checksum_path="$DIST/${package_name}.sha256"
  local checksum_tmp_path="$arch_build/${package_name}.sha256.tmp"
  local pkg_resources="$arch_build/pkg-resources"
  local pkg_scripts="$arch_build/pkg-scripts"
  local distribution_xml="$arch_build/distribution.xml"

  rm -rf "$arch_build"
  mkdir -p "$arch_build" "$package_dir"

  pushd "$ROOT" >/dev/null
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 go build -buildvcs=false -ldflags "-X main.binaryRole=manager" -o "$arch_build/codextools" .
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 go build -buildvcs=false -ldflags "-X main.binaryRole=launcher" -o "$arch_build/codextools-launcher" .
  popd >/dev/null

  create_app "$app_dir" "$APP_NAME" "codextools" "$arch_build/codextools" "com.hereww.chatgptcodextools" "false"
  create_app "$launcher_app_dir" "$LAUNCHER_NAME" "codextools-launcher" "$arch_build/codextools-launcher" "com.hereww.chatgptcodextools.launcher" "true"

  verify_macos_deployment_target "$app_dir"
  verify_macos_deployment_target "$launcher_app_dir"
  copy_app_bundle "$launcher_app_dir" "$package_dir/$LAUNCHER_NAME.app"
  copy_app_bundle "$app_dir" "$package_dir/$APP_NAME.app"
  cp "$ROOT/README.md" "$package_dir/README.md"
  cp "$ROOT/README.zh-CN.md" "$package_dir/README.zh-CN.md"
  write_start_here "$package_dir/START-HERE.txt" "$label"

  rm -f "$zip_path"
  ditto -c -k --norsrc --keepParent "$package_dir" "$zip_path"

  rm -rf "$pkg_root"
  mkdir -p "$pkg_root/Applications" "$pkg_resources" "$pkg_scripts"
  copy_app_bundle "$launcher_app_dir" "$pkg_root/Applications/$LAUNCHER_NAME.app"
  copy_app_bundle "$app_dir" "$pkg_root/Applications/$APP_NAME.app"
  xattr -cr "$pkg_root" 2>/dev/null || true
  find "$pkg_root" -name '._*' -delete
  if command -v dot_clean >/dev/null 2>&1; then
    dot_clean -m "$pkg_root" >/dev/null 2>&1 || true
  fi
  verify_pkg_payload_root "$pkg_root"
  cat > "$pkg_scripts/preinstall" <<'SCRIPT'
#!/bin/sh
set -eu

install_location="${2:-}"
target_volume="${3:-}"
if [ "$install_location" != "/" ] || [ "$target_volume" != "/" ]; then
  echo "ChatGPT Codex Tools must be installed on the system volume at /Applications." >&2
  exit 1
fi
SCRIPT
  chmod 0755 "$pkg_scripts/preinstall"
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
    <h1>ChatGPT Codex Tools ${VERSION}</h1>
    <p>This installer places <strong>ChatGPT Codex 管理工具.app</strong> and <strong>ChatGPT Codex.app</strong> in <code>/Applications</code>.</p>
    <p>The macOS packages are unsigned community builds, including this pkg installer. If macOS blocks the first launch, open Terminal and run:</p>
    <pre>xattr -cr "/Applications/ChatGPT Codex 管理工具.app"
xattr -cr "/Applications/ChatGPT Codex.app"
xattr -cr "/Applications/Codex++ 管理工具.app"
xattr -cr "/Applications/Codex++.app"</pre>
    <p>You can also right-click each app and choose <strong>Open</strong>.</p>
  </body>
</html>
HTML
  pkgbuild --analyze --root "$pkg_root" "$component_plist" >/dev/null
  force_component_bundle_policy "$component_plist"
  pkgbuild \
    --root "$pkg_root" \
    --identifier "com.hereww.codextools.pkg.${label}" \
    --version "$VERSION" \
    --install-location "/" \
    --scripts "$pkg_scripts" \
    --component-plist "$component_plist" \
    --filter '/\.DS_Store$' \
    --filter '/\._[^/]*$' \
    --filter '/CVS$' \
    --filter '/\.svn$' \
    "$component_pkg" >/dev/null
  cat > "$distribution_xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="1">
  <title>ChatGPT Codex Tools ${VERSION}</title>
  <readme file="ReadMe.html"/>
  <options customize="never" require-scripts="false"/>
  <domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
  <choices-outline>
    <line choice="default"/>
  </choices-outline>
  <choice id="default" title="ChatGPT Codex Tools">
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

  (
    cd "$DIST"
    shasum -a 256 "$(basename "$pkg_path")" "$(basename "$zip_path")" > "$checksum_tmp_path"
  )
  mv -f "$checksum_tmp_path" "$checksum_path"

  echo "$pkg_path"
  echo "$zip_path"
  echo "$checksum_path"
}

pushd "$ROOT/web" >/dev/null
npm ci --cache "$NPM_CACHE"
npm run check
npm run vite:build
popd >/dev/null

for arch in $TARGET_ARCHES; do
  build_arch "$arch"
done
