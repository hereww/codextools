#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="$ROOT/dist/releases"
BUILD="$ROOT/dist/build/windows"
VERSION="${VERSION:-1.1.14}"
ZIP_PATH="$DIST/CodexTools-${VERSION}-windows-x64.zip"
SETUP_PATH="$DIST/CodexTools-${VERSION}-windows-x64-setup.exe"
PACKAGE_DIR="$BUILD/CodexTools-${VERSION}-windows-x64"
ICON_PNG="$ROOT/assets/icons/codextools-1024.png"
ICON_ICO="$BUILD/codextools.ico"
RESOURCE_PREFIX="$ROOT/codextools"
RESOURCE_SYSO="$ROOT/codextools_windows_amd64.syso"
NSIS_SCRIPT="$BUILD/codextools-installer.nsi"

rm -rf "$BUILD"
mkdir -p "$BUILD" "$DIST" "$PACKAGE_DIR"

cleanup() {
  rm -f "$RESOURCE_SYSO"
}
trap cleanup EXIT

version_quad() {
  local clean="${VERSION#v}"
  clean="${clean%%-*}"
  local major="0" minor="0" patch="0" build="0"
  IFS=. read -r major minor patch build <<<"$clean"
  printf '%s.%s.%s.%s' "${major:-0}" "${minor:-0}" "${patch:-0}" "${build:-0}"
}

tool_path() {
  local name="$1"
  local fallback="$2"
  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return
  fi
  if [[ -x "$fallback" ]]; then
    printf '%s\n' "$fallback"
    return
  fi
  return 1
}

GO_WINRES="$(tool_path go-winres "$(go env GOPATH)/bin/go-winres" || true)"
if [[ -z "$GO_WINRES" ]]; then
  go install github.com/tc-hib/go-winres@v0.3.3
  GO_WINRES="$(go env GOPATH)/bin/go-winres"
fi

if ! command -v makensis >/dev/null 2>&1; then
  echo "makensis is required to build the Windows installer." >&2
  exit 1
fi

if command -v magick >/dev/null 2>&1; then
  magick "$ICON_PNG" -define icon:auto-resize=256,128,64,48,32,16 "$ICON_ICO"
elif command -v sips >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
  ICON_TMP_DIR="$BUILD/icon-sizes"
  mkdir -p "$ICON_TMP_DIR"
  for size in 16 24 32 48 64 128 256; do
    sips -z "$size" "$size" "$ICON_PNG" --out "$ICON_TMP_DIR/icon-${size}.png" >/dev/null
  done
  node - "$ICON_ICO" "$ICON_TMP_DIR"/icon-*.png <<'NODE'
const fs = require("fs");

const [outFile, ...inputFiles] = process.argv.slice(2);
const entries = inputFiles
  .map((file) => {
    const match = file.match(/icon-(\d+)\.png$/);
    if (!match) throw new Error(`Cannot read icon size from ${file}`);
    return { size: Number(match[1]), data: fs.readFileSync(file) };
  })
  .sort((left, right) => left.size - right.size);

const headerSize = 6;
const directorySize = 16 * entries.length;
let offset = headerSize + directorySize;
const header = Buffer.alloc(headerSize);
header.writeUInt16LE(0, 0);
header.writeUInt16LE(1, 2);
header.writeUInt16LE(entries.length, 4);

const directories = entries.map((entry) => {
  const directory = Buffer.alloc(16);
  directory.writeUInt8(entry.size >= 256 ? 0 : entry.size, 0);
  directory.writeUInt8(entry.size >= 256 ? 0 : entry.size, 1);
  directory.writeUInt8(0, 2);
  directory.writeUInt8(0, 3);
  directory.writeUInt16LE(1, 4);
  directory.writeUInt16LE(32, 6);
  directory.writeUInt32LE(entry.data.length, 8);
  directory.writeUInt32LE(offset, 12);
  offset += entry.data.length;
  return directory;
});

fs.writeFileSync(outFile, Buffer.concat([header, ...directories, ...entries.map((entry) => entry.data)]));
NODE
else
  echo "ImageMagick, or sips plus node, is required to create the Windows installer icon." >&2
  exit 1
fi

build_resource() {
  local description="$1"
  local filename="$2"
  rm -f "$RESOURCE_SYSO"
  "$GO_WINRES" simply \
    --arch amd64 \
    --out "$RESOURCE_PREFIX" \
    --manifest gui \
    --icon "$ICON_ICO" \
    --file-description "$description" \
    --product-name "CodexTools" \
    --product-version "$(version_quad)" \
    --file-version "$(version_quad)" \
    --original-filename "$filename"
}

pushd "$ROOT/web" >/dev/null
npm install
npm run check
npm run vite:build
popd >/dev/null

pushd "$ROOT" >/dev/null
build_resource "CodexTools manager" "codextools.exe"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui -X main.binaryRole=manager" -o "$PACKAGE_DIR/codextools.exe" .
build_resource "CodexTools launcher" "codextools-launcher.exe"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui -X main.binaryRole=launcher" -o "$PACKAGE_DIR/codextools-launcher.exe" .
rm -f "$RESOURCE_SYSO"
popd >/dev/null

cp "$ROOT/assets/icons/codextools-1024.png" "$PACKAGE_DIR/codextools-icon.png"
cp "$ICON_ICO" "$PACKAGE_DIR/codextools-icon.ico"
cp "$ROOT/README.md" "$PACKAGE_DIR/README.md"
cp "$ROOT/README.zh-CN.md" "$PACKAGE_DIR/README.zh-CN.md"

cat > "$PACKAGE_DIR/START-HERE.txt" <<TXT
CodexTools Windows desktop package

1. The recommended release artifact is "CodexTools-${VERSION}-windows-x64-setup.exe"; it creates Start Menu shortcuts and an uninstall entry.
2. This folder can also be used as a portable desktop build.
3. "codextools.exe" opens a native Windows desktop window using WebView2, not a browser tab.
4. "codextools-launcher.exe" launches Codex through the Codex++ launcher.
TXT

rm -f "$ZIP_PATH"
(cd "$BUILD" && zip -qr -X "$ZIP_PATH" "$(basename "$PACKAGE_DIR")")

cat > "$NSIS_SCRIPT" <<NSI
Unicode true
Name "CodexTools"
OutFile "$SETUP_PATH"
InstallDir "\$LOCALAPPDATA\\CodexTools"
RequestExecutionLevel user
Icon "$ICON_ICO"
UninstallIcon "$ICON_ICO"
VIProductVersion "$(version_quad)"
VIAddVersionKey "ProductName" "CodexTools"
VIAddVersionKey "CompanyName" "hereww"
VIAddVersionKey "LegalCopyright" "Copyright hereww"
VIAddVersionKey "FileDescription" "CodexTools Windows Installer"
VIAddVersionKey "FileVersion" "$VERSION"
VIAddVersionKey "ProductVersion" "$VERSION"

!macro CloseCodexToolsProcesses
  nsExec::ExecToLog 'taskkill /IM codextools.exe /T /F'
  nsExec::ExecToLog 'taskkill /IM codextools-launcher.exe /T /F'
  nsExec::ExecToLog 'taskkill /IM "Codex++ 管理工具.exe" /T /F'
  nsExec::ExecToLog 'taskkill /IM "Codex++.exe" /T /F'
  Sleep 800
!macroend

Section "Install"
  SetShellVarContext current
  !insertmacro CloseCodexToolsProcesses
  Delete /REBOOTOK "\$INSTDIR\\Codex++ 管理工具.exe"
  Delete /REBOOTOK "\$INSTDIR\\Codex++.exe"
  SetOutPath "\$INSTDIR"
  File /r "$PACKAGE_DIR/*"
  WriteUninstaller "\$INSTDIR\\Uninstall.exe"

  CreateDirectory "\$SMPROGRAMS\\CodexTools"
  CreateShortcut "\$SMPROGRAMS\\CodexTools\\Codex++ 管理工具.lnk" "\$INSTDIR\\codextools.exe" "" "\$INSTDIR\\codextools.exe" 0
  CreateShortcut "\$SMPROGRAMS\\CodexTools\\Codex++.lnk" "\$INSTDIR\\codextools-launcher.exe" "" "\$INSTDIR\\codextools-launcher.exe" 0
  CreateShortcut "\$SMPROGRAMS\\CodexTools\\Uninstall CodexTools.lnk" "\$INSTDIR\\Uninstall.exe"
  CreateShortcut "\$DESKTOP\\Codex++ 管理工具.lnk" "\$INSTDIR\\codextools.exe" "" "\$INSTDIR\\codextools.exe" 0

  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "DisplayName" "CodexTools"
  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "DisplayVersion" "$VERSION"
  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "Publisher" "hereww"
  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "InstallLocation" "\$INSTDIR"
  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "DisplayIcon" "\$INSTDIR\\codextools.exe"
  WriteRegStr HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "UninstallString" '"\$INSTDIR\\Uninstall.exe"'
  WriteRegDWORD HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "NoModify" 1
  WriteRegDWORD HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  !insertmacro CloseCodexToolsProcesses
  Delete "\$DESKTOP\\Codex++ 管理工具.lnk"
  Delete "\$SMPROGRAMS\\CodexTools\\Codex++ 管理工具.lnk"
  Delete "\$SMPROGRAMS\\CodexTools\\Codex++.lnk"
  Delete "\$SMPROGRAMS\\CodexTools\\Uninstall CodexTools.lnk"
  RMDir "\$SMPROGRAMS\\CodexTools"
  DeleteRegKey HKCU "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\CodexTools"
  RMDir /r "\$INSTDIR"
SectionEnd
NSI

makensis -V2 "$NSIS_SCRIPT"
echo "$SETUP_PATH"
echo "$ZIP_PATH"
