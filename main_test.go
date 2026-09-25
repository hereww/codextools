package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func contextWithCanceledNetwork() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestParseLaunchRequestReadsRestartFlag(t *testing.T) {
	request := parseLaunchRequest([]string{"--launcher", "--debug-port", "9229", "--helper-port", "57321", "--restart"})

	if !request.restart {
		t.Fatal("restart flag should be true")
	}
	if request.debugPort != 9229 {
		t.Fatalf("debug port mismatch: %d", request.debugPort)
	}
	if request.helperPort != 57321 {
		t.Fatalf("helper port mismatch: %d", request.helperPort)
	}
}

func TestRuntimeVersionMatchesReleaseBuild(t *testing.T) {
	if version != "1.2.10" {
		t.Fatalf("runtime version should identify the release build, got %q", version)
	}
}

func TestBuildManagerLauncherCommandUsesChatGPTCodexLauncher(t *testing.T) {
	command := buildManagerLauncherCommand(`C:\Tools\codextools-launcher.exe`, `C:\ChatGPT\app`, 9229, 57321, true)

	expected := []string{
		`C:\Tools\codextools-launcher.exe`,
		"--launcher",
		"--debug-port",
		"9229",
		"--helper-port",
		"57321",
		"--app-path",
		`C:\ChatGPT\app`,
		"--restart",
	}
	if strings.Join(command, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("manager launch command should target ChatGPT Codex launcher:\n got: %#v\nwant: %#v", command, expected)
	}
	payload := managerLauncherPayload(command[0], command, `C:\ChatGPT\app`, 9229, 57321)
	if got := stringFromAny(payload["launch_chain"]); got != "codex_plus_launcher" {
		t.Fatalf("launch chain mismatch: %q", got)
	}
	if got := stringFromAny(payload["launcher_path"]); !strings.Contains(strings.ToLower(got), "codextools-launcher") {
		t.Fatalf("launcher path should reference codextools-launcher, got %q", got)
	}
}

func TestPreferredMacOSLauncherPathPrefersSiblingLauncherApp(t *testing.T) {
	root := t.TempDir()
	managerExecutable := filepath.Join(root, "ChatGPT Codex 管理工具.app", "Contents", "MacOS", "ChatGPTCodexManager")
	managerNestedLauncher := filepath.Join(root, "ChatGPT Codex 管理工具.app", "Contents", "MacOS", "ChatGPTCodex")
	launcherExecutable := filepath.Join(root, "ChatGPT Codex.app", "Contents", "MacOS", "ChatGPTCodex")
	for _, path := range []string{managerExecutable, managerNestedLauncher, launcherExecutable} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write executable failed: %v", err)
		}
	}

	got := preferredMacOSAppExecutableFromCandidates(
		managerNestedLauncher,
		[]string{filepath.Join(root, "ChatGPT Codex.app")},
		"ChatGPT Codex.app",
		[]string{"ChatGPTCodex", "codextools-launcher"},
	)
	if got != launcherExecutable {
		t.Fatalf("manager launch should use standalone launcher app:\n got: %q\nwant: %q", got, launcherExecutable)
	}
}

func TestPreferredMacOSLauncherPathRejectsManagerBundleFallback(t *testing.T) {
	root := t.TempDir()
	managerNestedLauncher := filepath.Join(root, "ChatGPT Codex 管理工具.app", "Contents", "MacOS", "ChatGPTCodex")
	if err := os.MkdirAll(filepath.Dir(managerNestedLauncher), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(managerNestedLauncher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable failed: %v", err)
	}
	expectedMissingStandalone := filepath.Join(root, "ChatGPT Codex.app", "Contents", "MacOS", "ChatGPTCodex")

	got := preferredMacOSAppExecutableFromCandidates(
		managerNestedLauncher,
		[]string{filepath.Join(root, "ChatGPT Codex.app")},
		"ChatGPT Codex.app",
		[]string{"ChatGPTCodex", "codextools-launcher"},
	)
	if got != expectedMissingStandalone {
		t.Fatalf("manager bundle launcher fallback should be rejected:\n got: %q\nwant: %q", got, expectedMissingStandalone)
	}
}

func TestLauncherRestartBusyLockUsesFullRestartFlow(t *testing.T) {
	data, err := os.ReadFile("launcher.go")
	if err != nil {
		t.Fatalf("read launcher.go failed: %v", err)
	}
	source := string(data)
	for _, expected := range []string{
		"if !acquired {\n\t\tif options.restart {",
		"prepareFullRestartBeforeLaunch(appPath, debugPort, helperPort, true)",
		"waitForLauncherSingleInstanceLock(debugPort, 15*time.Second)",
		"launcher.restart_existing_launcher",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("restart busy-lock flow missing %q", expected)
		}
	}
	if strings.Contains(source, `runtime.GOOS == "windows" && options.restart`) {
		t.Fatal("restart must not use the old Windows-only partial restart shortcut")
	}
}

func TestWriteLaunchRestartingStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	debugPort := uint16(9229)
	helperPort := uint16(57321)
	appPath := filepath.Join(home, "ChatGPT.app")

	writeLaunchRestartingStatus("正在关闭旧 ChatGPT Codex 后端并释放端口。", debugPort, helperPort, &appPath)

	var status launchStatus
	if err := readJSON(latestStatusPath(), &status); err != nil {
		t.Fatalf("restart status should be readable: %v", err)
	}
	if status.Status != "restarting" {
		t.Fatalf("status mismatch: %q", status.Status)
	}
	if status.Message != "正在关闭旧 ChatGPT Codex 后端并释放端口。" {
		t.Fatalf("message mismatch: %q", status.Message)
	}
	if status.DebugPort == nil || *status.DebugPort != debugPort {
		t.Fatalf("debug port mismatch: %#v", status.DebugPort)
	}
	if status.HelperPort == nil || *status.HelperPort != helperPort {
		t.Fatalf("helper port mismatch: %#v", status.HelperPort)
	}
	if status.CodexApp == nil || *status.CodexApp != appPath {
		t.Fatalf("codex app mismatch: %#v", status.CodexApp)
	}
}

func TestReapLauncherChildKeepsBackendAliveWhileCDPPortIsOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/json" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"chatgpt","type":"page","url":"app://-/index.html","title":"ChatGPT","webSocketDebuggerUrl":"ws://127.0.0.1/devtools/page/chatgpt"}]`))
	}))
	parsed, err := url.Parse(cdp.URL)
	if err != nil {
		t.Fatalf("parse cdp URL failed: %v", err)
	}
	portValue, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse cdp port failed: %v", err)
	}
	debugPort := uint16(portValue)
	helperPort := uint16(57321)
	done := make(chan error, 1)

	go func() {
		done <- reapLauncherChild(fakeCodexLaunch{}, filepath.Join(home, "ChatGPT.exe"), debugPort, helperPort)
	}()

	select {
	case err := <-done:
		t.Fatalf("launcher reaper should keep running while CDP port is open, returned %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	cdp.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launcher reaper returned error after CDP close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("launcher reaper should return after CDP port closes")
	}

	var status launchStatus
	if err := readJSON(latestStatusPath(), &status); err != nil {
		t.Fatalf("read latest status failed: %v", err)
	}
	if status.Status != "exited" {
		t.Fatalf("final status should be exited after CDP closes, got %#v", status)
	}
	logs := readFile(diagnosticLogPath())
	if !strings.Contains(logs, "launcher.child_exited_cdp_alive") {
		t.Fatalf("launcher reaper should log CDP-alive child exit, logs=%s", logs)
	}
}

type fakeCodexLaunch struct{}

func (fakeCodexLaunch) wait() error { return nil }

func (fakeCodexLaunch) terminate() error { return nil }

func (fakeCodexLaunch) logPayload() map[string]any { return map[string]any{"type": "fake"} }

func TestProviderSwitchForcesCompleteEnhancementMode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx failed: %v", err)
	}
	source := string(data)
	for _, expected := range []string{
		`const modeResult = await saveLaunchMode("patch", true, selectedSettings);`,
		`const result = await saveLaunchMode("patch", true);`,
		`供应商已切换，页面增强已设为完整增强。`,
		`已按此供应商切回官方登录；页面增强已设为完整增强。`,
		`已按此供应商使用官方登录，并混入 API Key；页面增强已设为完整增强。`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("provider switch should force complete enhancement; missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		`页面增强已设为兼容增强。`,
		`适合官方登录或官方混合 API`,
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("provider switch text should not mention compatibility default: %q", forbidden)
		}
	}
}

func TestRelayProfileHTTPProxyUIAndStateAreWired(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx failed: %v", err)
	}
	source := string(data)
	for _, expected := range []string{
		`proxyEnabled: boolean;`,
		`proxyUrl: string;`,
		`proxyEnabled: false,`,
		`proxyUrl: "",`,
		`"proxyEnabled",`,
		`"proxyUrl",`,
		`function ImageRelaySettings({`,
		`图片生成路由`,
		`允许当前中转使用图片生成`,
		`图片生成使用独立 API 和 Key`,
		`图片与默认中转共用 HTTP 代理`,
		`独立图片 Key 只由本地图片代理读取`,
		`保存并应用图片路由`,
		`onApply={onApplyImageSettings}`,
		`onClick={() => onEdit(profile.id)}`,
		`代理 URL`,
		`http://127.0.0.1:7890`,
		`LOCAL_RELAY_PROXY_BASE_URL`,
		`profile.proxyEnabled && profile.proxyUrl.trim() !== ""`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("relay profile HTTP proxy UI/state should include %q", expected)
		}
	}
	mainScreenStart := strings.Index(source, "function RelayScreen({")
	detailStart := strings.Index(source, "function RelayProfileDetail({")
	if mainScreenStart < 0 || detailStart <= mainScreenStart {
		t.Fatal("relay screen/provider detail boundaries were not found")
	}
	if strings.Contains(source[mainScreenStart:detailStart], "<ImageRelaySettings") {
		t.Fatal("image relay settings must live in provider details, not the relay overview")
	}
}

func TestImageRelayCLIEnvironmentUsesLocalProxyWithoutExposingImageKey(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                            "image-relay",
		Name:                          "Image relay",
		RelayMode:                     "pureApi",
		Protocol:                      "responses",
		BaseURL:                       "https://text.example.test/v1",
		APIKey:                        "text-secret",
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example.test/v1",
		ImageGenerationAPIKey:         "image-secret",
	}}
	settings.ActiveRelayID = "image-relay"

	environment := imageRelayCLIEnvironment(settings)
	joined := strings.Join(environment, "\n")
	for _, expected := range []string{
		"OPENAI_API_KEY=" + imageRelayCLIAPIKey,
		fmt.Sprintf("OPENAI_BASE_URL=http://127.0.0.1:%d/v1", localRelayProxyPort),
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("image CLI environment missing %q: %#v", expected, environment)
		}
	}
	for _, secret := range []string{"text-secret", "image-secret", "image.example.test"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("image CLI environment exposed provider secret %q: %#v", secret, environment)
		}
	}
}

func TestImageRelayCLIEnvironmentDisabledForNormalRelay(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                     "text-relay",
		RelayMode:              "pureApi",
		Protocol:               "responses",
		BaseURL:                "https://text.example.test/v1",
		APIKey:                 "text-secret",
		ImageGenerationEnabled: true,
	}}
	settings.ActiveRelayID = "text-relay"

	if environment := imageRelayCLIEnvironment(settings); len(environment) != 0 {
		t.Fatalf("normal relay must not inject Imagegen CLI environment: %#v", environment)
	}
}

func TestCodexLaunchEnvironmentDoesNotInjectImageRelayIntoDesktopApp(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                            "image-relay",
		Name:                          "Image relay",
		RelayMode:                     "pureApi",
		Protocol:                      "responses",
		BaseURL:                       "https://text.example.test/v1",
		APIKey:                        "text-secret",
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example.test/v1",
		ImageGenerationAPIKey:         "image-secret",
	}}
	settings.ActiveRelayID = "image-relay"

	environment := codexLaunchEnvironment(settings)
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"OPENAI_API_KEY=", "OPENAI_BASE_URL=", "image-secret", "image.example.test"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("desktop app launch environment must not contain %q: %#v", forbidden, environment)
		}
	}
}

func TestMergeLaunchEnvironmentReplacesExistingOpenAIValues(t *testing.T) {
	base := []string{"PATH=/usr/bin", "OPENAI_API_KEY=real-secret", "OPENAI_BASE_URL=https://api.openai.com/v1", "OTHER=value"}
	overrides := []string{"OPENAI_API_KEY=" + imageRelayCLIAPIKey, "OPENAI_BASE_URL=http://127.0.0.1:57323/v1"}
	merged := mergeLaunchEnvironment(base, overrides)
	joined := strings.Join(merged, "\n")

	if strings.Contains(joined, "real-secret") || strings.Contains(joined, "api.openai.com") {
		t.Fatalf("existing OpenAI credentials survived launch environment merge: %#v", merged)
	}
	for _, expected := range append([]string{"PATH=/usr/bin", "OTHER=value"}, overrides...) {
		if !strings.Contains(joined, expected) {
			t.Fatalf("merged launch environment missing %q: %#v", expected, merged)
		}
	}
}

func TestWindowsUIDoesNotSuggestWindowsAppsLaunchPath(t *testing.T) {
	appData, err := os.ReadFile(filepath.Join("web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx failed: %v", err)
	}
	translationsData, err := os.ReadFile(filepath.Join("web", "src", "i18n", "translations.ts"))
	if err != nil {
		t.Fatalf("read translations.ts failed: %v", err)
	}
	source := string(appData) + "\n" + string(translationsData)
	for _, expected := range []string{
		`不要选择 Program Files\\WindowsApps 包目录`,
		`C:\\Users\\你\\AppData\\Local\\Programs\\ChatGPT\\ChatGPT.exe`,
		`选择 ChatGPT.exe`,
		`打开 ChatGPT 下载页`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("Windows UI should guide users to ChatGPT desktop app; missing %q", expected)
		}
	}
	if strings.Contains(source, `C:\\Program Files\\WindowsApps\\OpenAI.ChatGPT_...\\app`) {
		t.Fatal("Windows UI must not suggest Program Files\\WindowsApps as a launch path")
	}
}

func TestBuildCodexLaunchCommandKeepsDebugArgumentsForExecutable(t *testing.T) {
	command := buildCodexLaunchCommand(filepath.Join(t.TempDir(), "ChatGPT.exe"), 9229, []string{"--force_high_performance_gpu"})

	if len(command) != 4 {
		t.Fatalf("command length mismatch: %#v", command)
	}
	if !strings.EqualFold(filepath.Base(command[0]), "ChatGPT.exe") {
		t.Fatalf("command should target ChatGPT.exe: %#v", command)
	}
	if command[1] != "--remote-debugging-port=9229" {
		t.Fatalf("debug port argument mismatch: %#v", command)
	}
	if command[2] != "--remote-allow-origins=http://127.0.0.1:9229" {
		t.Fatalf("remote origin argument mismatch: %#v", command)
	}
	if command[3] != "--force_high_performance_gpu" {
		t.Fatalf("extra argument mismatch: %#v", command)
	}
}

func TestManagerLaunchAppPathSkipsPackagedOverrideWithoutExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch path fallback only applies on Windows")
	}
	requested := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	if got := managerLaunchAppPath(requested, defaultSettings()); got != "" {
		t.Fatalf("packaged override without direct executable should be skipped, got %q", got)
	}
}

func TestManagerLaunchAppPathKeepsDirectExecutableOverride(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch path fallback only applies on Windows")
	}
	appDir := filepath.Join(t.TempDir(), "ChatGPT")
	exe := filepath.Join(appDir, "ChatGPT.exe")
	writeTestFile(t, exe, "binary")

	if got := managerLaunchAppPath(exe, defaultSettings()); got != appDir {
		t.Fatalf("direct executable override should be normalized and kept, got %q", got)
	}
}

func TestWindowsProtectedPackagePathDetectionIsPathBased(t *testing.T) {
	packageApp := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.623.19656.0_x64__2p2nqsd0c76g0\app`
	packageExe := packageApp + `\ChatGPT.exe`

	if !isWindowsProtectedAppPackagePath(packageApp) {
		t.Fatalf("package app dir should be treated as protected: %q", packageApp)
	}
	if !isWindowsProtectedAppPackagePath(packageExe) {
		t.Fatalf("package executable should be treated as protected: %q", packageExe)
	}
	if !isWindowsProtectedCodexDirectLaunchPath(packageApp, packageExe) {
		t.Fatal("protected app/executable pair should block direct launch")
	}

	alias := `C:\Users\Alice\AppData\Local\Microsoft\WindowsApps\ChatGPT.exe`
	localRuntime := `C:\Users\Alice\AppData\Local\Programs\ChatGPT\ChatGPT.exe`
	if isWindowsProtectedAppPackagePath(alias) {
		t.Fatalf("execution alias should not be treated as protected package path: %q", alias)
	}
	if isWindowsProtectedAppPackagePath(localRuntime) {
		t.Fatalf("local runtime should not be treated as protected package path: %q", localRuntime)
	}
}

func TestWindowsLauncherUsesValidatedPackagedLaunchStrategies(t *testing.T) {
	data, err := os.ReadFile("launcher.go")
	if err != nil {
		t.Fatalf("read launcher.go failed: %v", err)
	}
	source := string(data)
	for _, expected := range []string{
		"directLaunchBlocked",
		"isWindowsProtectedCodexDirectLaunchPath(appPath, command[0])",
		"windowsProtectedMSIXLaunchError(appPath, command[0])",
		"windowsPackagedExecutionAlias",
		"windowsPackagedDirectExecutable",
		"startVerifiedWindowsPackagedProcess",
		"activateWindowsPackagedAppWithEnvironment",
		"ChatGPT.exe",
		"Codex.exe",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("launcher should use validated packaged launch strategies; missing %q", expected)
		}
	}
	blockIndex := strings.Index(source, "if directLaunchBlocked {\n\t\t\treturn nil, windowsProtectedMSIXLaunchError(appPath, command[0])\n\t\t}")
	aliasIndex := strings.Index(source, "windowsPackagedExecutionAlias")
	directIndex := strings.Index(source, "windowsPackagedDirectExecutable")
	activationIndex := strings.Index(source, "activateWindowsPackagedAppWithEnvironment")
	if blockIndex < 0 || aliasIndex < 0 || directIndex < 0 || activationIndex < 0 || aliasIndex > directIndex || directIndex > activationIndex || activationIndex > blockIndex {
		t.Fatal("protected MSIX launch order must be manifest alias, registered FullTrust executable, COM activation, then protected-path rejection")
	}
	for _, forbidden := range []string{"installWindowsCodexMirror", "launcher.codex_app_auto_repaired"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("launcher must not keep old standalone Codex repair flow: %q", forbidden)
		}
	}
}

func TestWindowsManagerAcceptsRegisteredMSIXLaunch(t *testing.T) {
	data, err := os.ReadFile("manager.go")
	if err != nil {
		t.Fatalf("read manager.go failed: %v", err)
	}
	source := string(data)
	for _, expected := range []string{
		`if runtime.GOOS != "windows" {`,
		`if appPath != "" && windowsCodexPathSupportsLaunch(appPath) {`,
		`if runtime.GOOS == "windows" && !windowsCodexPathSupportsLaunch(path) {`,
		`return packagedWindowsAppUserModelID(appPath) != ""`,
		`payload["method"] = "packaged_activation"`,
		`payload["directLaunchBlocked"] = protectedPackage`,
		`windowsPackagedDirectExecutable`,
		`Get-AppxPackage -Name OpenAI.Codex`,
		`Get-AppxPackage -Name OpenAI.ChatGPT`,
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("Windows manager should keep registered MSIX launch support; missing %q", expected)
		}
	}
	if strings.Contains(source, `runtime.GOOS != "windows" || appPath == ""`) {
		t.Fatal("Windows launch path selection must fall back to saved/detected executable when request path is empty")
	}
	if strings.Contains(source, "installWindowsCodexMirror") {
		t.Fatal("Windows manager must not keep old standalone Codex mirror repair")
	}
}

func TestNormalizeCodexAppPathRejectsCodexToolsMacApps(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app bundle filtering only applies on darwin")
	}
	root := t.TempDir()
	for _, name := range []string{"ChatGPT Codex.app", "ChatGPT Codex 管理工具.app", "ChatGPT Codex Tools.app", "Codex.app", "Codex++.app", "Codex++ 管理工具.app", "CodexTools.app"} {
		if got := normalizeCodexAppPath(filepath.Join(root, name)); got != "" {
			t.Fatalf("non-ChatGPT app should not be accepted as ChatGPT desktop app: %q", got)
		}
	}
	random := filepath.Join(root, "Renamed.app")
	writeTestFile(t, filepath.Join(random, "Contents", "Info.plist"), `<plist><dict><key>CFBundleIdentifier</key><string>com.hereww.codextools.manager</string></dict></plist>`)
	if got := normalizeCodexAppPath(random); got != "" {
		t.Fatalf("ChatGPT Codex Tools bundle id should not be accepted as ChatGPT desktop app: %q", got)
	}
}

func TestRendererInjectionUsesVersionGatedMarketplaceStrategy(t *testing.T) {
	for _, required := range []string{
		`pluginMarketplaceUnlock`,
		`pluginPatchDisabledInRelayMode`,
		`activeRelayModeSupportsOfficialSites`,
		`activeRelayMode: "official"`,
		`codexAppVersion`,
		`pasteFix`,
		`codexAppPasteFix`,
		`installPasteFix`,
		`__CODEX_PLUS_FAST_STARTUP__`,
		`__CODEX_PLUS_FORCE_CHINESE_LOCALE__`,
		`__CODEX_PLUS_NATIVE_MENU_LOCALIZATION__`,
		`codexAppNativeMenuLocalization`,
		`codexPluginLegacyEntryUnlockBeforeVersion`,
		`26.601.2237`,
		`parseCodexVersionParts`,
		`compareCodexVersions`,
		`codexPluginUnlockStrategy`,
		`plugin_unlock_strategy_selected`,
		`pluginUnlockStrategy === "modern"`,
		`pluginUnlockStrategy === "unknown"`,
		`installPluginBuildFlavorFilterPatch`,
		`installPluginMarketplaceBridgePatch`,
		`installPluginMarketplaceWindowEventPatchOnly`,
		`installPluginMarketplaceRequestPatch`,
		`mergeLocalPluginMarketplaces`,
		`__CODEX_PLUS_PLUGIN_MARKETPLACES__`,
		`plugin_marketplace_local_merged`,
		`threadIdBadge`,
		`showSaveFilePicker`,
		`modelJsonResponseLooksPatchable`,
		`window.addEventListener("codex-message-from-view"`,
	} {
		if !strings.Contains(rendererInjectScript, required) {
			t.Fatalf("renderer injection should keep supported plugin controls; missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`data-codex-plus-setting="pluginAutoExpand"`,
		`data-codex-plus-setting="conversationTimeline"`,
		`forcePluginInstall`,
		`unblockPluginInstallButtons`,
		`强制安装`,
		`/ads`,
		`load_ads`,
		`RecommendationsScreen`,
		`codexPlusAds`,
		`codex-plus-ad`,
		`赞助商推荐`,
		`推荐内容`,
		`请作者喝咖啡`,
	} {
		if strings.Contains(rendererInjectScript, forbidden) {
			t.Fatalf("renderer injection should not include removed force-install patches or ads; found %q", forbidden)
		}
	}
}

func TestRendererInjectionUsesHTTPFirstForBackendStatusAndKeepsBridgeFallback(t *testing.T) {
	for _, expected := range []string{
		`async function fetchFromHelper(path, payload)`,
		`const backendStatusRoute = path === "/backend/status" || path === "/backend/repair";`,
		`if (codexPlusBackendCheckInFlight) return codexPlusBackendCheckInFlight;`,
		`if (codexPlusBackendCheckInFlight === request) codexPlusBackendCheckInFlight = null;`,
		`if (backendStatusRoute) {`,
		`const helperResult = await fetchFromHelperOrFailure(path, payload);`,
		`backend_status_http_failed_bridge_fallback_ok`,
		`if (window.__codexSessionDeleteBridge)`,
		`const bridgeResult = await bridgeWithTimeout(path, payload`,
		`backend_helper_and_bridge_missing`,
	} {
		if !strings.Contains(rendererInjectScript, expected) {
			t.Fatalf("renderer injection should use a resilient backend health check; missing %q", expected)
		}
	}
	if strings.Contains(rendererInjectScript, `withBackendTimeout(postJson("/backend/status"`) {
		t.Fatal("backend status should not have a second outer timeout around postJson")
	}
	for _, forbidden := range []string{
		`桥接不可用，请重启启动器`,
		`bridge_missing_for_route`,
		`backend_helper_failed_using_bridge`,
	} {
		if strings.Contains(rendererInjectScript, forbidden) {
			t.Fatalf("renderer injection should not fail non-status routes only because CDP bridge is missing; found %q", forbidden)
		}
	}
}

func TestRendererInjectionServiceTierMissingAssetsAreNonFatal(t *testing.T) {
	for _, expected := range []string{
		`function isCodexServiceTierUnavailableError(error)`,
		`function markCodexServiceTierUnavailable(error)`,
		`service_tier_unavailable`,
		`codexAppAssetUrl("vscode-api-")`,
		`window.__codexServiceTierRequestOverrideInstalled = codexServiceTierRequestOverrideVersion`,
	} {
		if !strings.Contains(rendererInjectScript, expected) {
			t.Fatalf("renderer injection should downgrade missing service tier assets; missing %q", expected)
		}
	}
}

func TestRendererInjectionExtendsThreadSortKeysTimeout(t *testing.T) {
	expected := `postJson("/thread-sort-keys", { sessions: refs }, { timeoutMs: 15000 })`
	if !strings.Contains(rendererInjectScript, expected) {
		t.Fatalf("thread sort key bridge call should have an extended timeout; missing %q", expected)
	}
}

func TestRendererInjectionUsesDeleteSpecificTimeoutAndDeduplication(t *testing.T) {
	for _, expected := range []string{
		`postJson("/delete", ref, { timeoutMs: 60000 })`,
		`status === "delete_in_progress"`,
		`function setDeleteButtonBusy(button, busy)`,
		`删除仍在处理中，请勿重复点击`,
	} {
		if !strings.Contains(rendererInjectScript, expected) {
			t.Fatalf("renderer injection should protect slow delete requests; missing %q", expected)
		}
	}
}

func TestHelperHTTPServesBridgeBackendRoutes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runtime := &launcherRuntime{settings: defaultSettings()}

	for _, path := range []string{"/backend/status", "/settings/get", "/diagnostics/runtime", "/user-scripts/list"} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:57321"+path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		runtime.handleHelperHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s should be served by helper backend route, got HTTP %d body=%s", path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s helper response should be JSON: %v body=%s", path, err, rec.Body.String())
		}
		if stringFromAny(payload["status"]) == "failed" {
			t.Fatalf("%s helper route should not fail: %#v", path, payload)
		}
		if stringFromAny(payload["transport"]) != "http-helper" {
			t.Fatalf("%s helper route should mark http-helper transport: %#v", path, payload)
		}
		if got := rec.Header().Get("access-control-allow-private-network"); got != "true" {
			t.Fatalf("%s helper route should allow private network CORS, got %q", path, got)
		}
	}
}

func TestManagerOpenRequiresExplicitCodexMenuSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	called := 0
	original := openManagerAppFunc
	openManagerAppFunc = func() error {
		called++
		return nil
	}
	defer func() { openManagerAppFunc = original }()
	runtime := &launcherRuntime{}

	blocked := runtime.handleBridgeRequest("/manager/open", json.RawMessage(`{}`))
	if blocked["status"] != "failed" {
		t.Fatalf("manager open without source should fail: %#v", blocked)
	}
	if called != 0 {
		t.Fatalf("manager should not open without explicit source, called %d times", called)
	}

	allowed := runtime.handleBridgeRequest("/manager/open", json.RawMessage(`{"source":"codex_plus_menu"}`))
	if allowed["status"] != "ok" {
		t.Fatalf("manager open from menu should succeed: %#v", allowed)
	}
	if called != 1 {
		t.Fatalf("manager should open once from menu source, called %d times", called)
	}
}

func TestSupportScreensUseStableToolbarLayout(t *testing.T) {
	appSource, err := os.ReadFile(filepath.Join("web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx failed: %v", err)
	}
	cssSource, err := os.ReadFile(filepath.Join("web", "src", "styles.css"))
	if err != nil {
		t.Fatalf("read styles.css failed: %v", err)
	}
	app := string(appSource)
	css := string(cssSource)
	for _, expected := range []string{
		`<Panel fill className="support-panel">`,
		`<CardContent className="support-content">`,
	} {
		if !strings.Contains(app, expected) {
			t.Fatalf("support screens should use stable support layout; missing %q", expected)
		}
	}
	for _, expected := range []string{
		`.support-panel`,
		`flex-direction: column;`,
		`.support-content .toolbar`,
		`.log-view.tall`,
		`resize: none;`,
	} {
		if !strings.Contains(css, expected) {
			t.Fatalf("support screen CSS should keep log/diagnostic toolbar stable; missing %q", expected)
		}
	}
}

func TestComputerUseGuardConfigPreservesExistingFeatureKeys(t *testing.T) {
	updated := computerUseGuardConfigText("[features]\nfoo = true\n", "/tmp/marketplace", "/tmp/notify")

	for _, expected := range []string{
		`[features]`,
		`foo = true`,
		`js_repl = true`,
		`[plugins."browser@openai-bundled"]`,
		`[marketplaces.openai-bundled]`,
		`notify = ["/tmp/notify", "turn-ended"]`,
	} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("computer use guard config missing %q:\n%s", expected, updated)
		}
	}
}

func TestBuildWatcherInstallPlanMatchesOriginalWindowsShape(t *testing.T) {
	plan := buildWatcherInstallPlan(`C:\Tools\ChatGPTCodex.exe`, 9229, `C:\Users\A\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\ChatGPTCodexToolsWatcher.lnk`)

	if plan.LauncherPath != `C:\Tools\ChatGPTCodex.exe` {
		t.Fatalf("launcher path mismatch: %q", plan.LauncherPath)
	}
	if plan.Arguments != "--debug-port 9229" {
		t.Fatalf("arguments mismatch: %q", plan.Arguments)
	}
	if plan.RunValue != `"C:\Tools\ChatGPTCodex.exe" --debug-port 9229` {
		t.Fatalf("run value mismatch: %q", plan.RunValue)
	}
	if plan.ShortcutPath == "" {
		t.Fatal("shortcut path should be preserved")
	}
}

func TestMacOSReleasePackageForcesApplicationsInstallLocation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "build", "macos-release.sh"))
	if err != nil {
		t.Fatalf("failed to read macOS release script: %v", err)
	}
	script := string(data)
	for _, expected := range []string{
		`pkgbuild --analyze --root "$pkg_root" "$component_plist"`,
		`:BundleIsRelocatable" bool false`,
		`:BundleIsVersionChecked" bool false`,
		`:BundleOverwriteAction" string upgrade`,
		`--install-location "/"`,
		`<domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>`,
		`--scripts "$pkg_scripts"`,
		`if [ "$install_location" != "/" ] || [ "$target_volume" != "/" ]; then`,
		`must be installed on the system volume at /Applications`,
		`--component-plist "$component_plist"`,
		`ditto --norsrc --noextattr --noacl --noqtn`,
		`"$pkg_root/Applications"`,
		`verify_pkg_payload_root()`,
		`verify_pkg_payload_root "$pkg_root"`,
		`"$pkg_root/Applications/$LAUNCHER_NAME.app/Contents/Info.plist"`,
		`"$pkg_root/Applications/$APP_NAME.app/Contents/Info.plist"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("macOS pkg script must force /Applications install; missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		`cp -R "$launcher_app_dir" "$pkg_root/Applications/"`,
		`cp -R "$app_dir" "$pkg_root/Applications/"`,
		`cp "$arch_build/codextools-launcher" "$app_dir/Contents/MacOS/codextools-launcher"`,
		`cp "$arch_build/codextools" "$launcher_app_dir/Contents/MacOS/codextools"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("macOS pkg script should stage app bundles with ditto --norsrc, found %q", forbidden)
		}
	}
}

func TestParseWindowsUninstallRegistryValue(t *testing.T) {
	output := `HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Uninstall\ChatGPT Codex Tools
    DisplayName    REG_SZ    ChatGPT Codex Tools
    UninstallString    REG_SZ    "C:\Users\A\AppData\Local\ChatGPT Codex Tools\Uninstall.exe"
`
	value := parseWindowsRegQueryValue(output, "UninstallString")
	if value != `"C:\Users\A\AppData\Local\ChatGPT Codex Tools\Uninstall.exe"` {
		t.Fatalf("uninstall registry value mismatch: %q", value)
	}
}

func TestWindowsExecutableFromCommandParsesQuotedPath(t *testing.T) {
	command := `"C:\Users\A\AppData\Local\ChatGPT Codex Tools\Uninstall.exe" /S`
	if got := windowsExecutableFromCommand(command); got != `C:\Users\A\AppData\Local\ChatGPT Codex Tools\Uninstall.exe` {
		t.Fatalf("quoted command path mismatch: %q", got)
	}
}

func TestNormalizeSettingsLanguage(t *testing.T) {
	settings := normalizeSettings(backendSettings{Language: "ja"})

	if settings.Language != "ja-JP" {
		t.Fatalf("language should normalize to ja-JP, got %q", settings.Language)
	}

	settings = normalizeSettings(backendSettings{Language: "unsupported"})
	if settings.Language != defaultLanguage {
		t.Fatalf("unsupported language should fall back to %q, got %q", defaultLanguage, settings.Language)
	}
}

func TestListContextEntriesJSONUsesEmptyArrays(t *testing.T) {
	entries := listContextEntriesFromConfig("")
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("failed to marshal entries: %v", err)
	}
	text := string(data)

	for _, expected := range []string{`"mcpServers":[]`, `"skills":[]`, `"plugins":[]`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("empty context entries should marshal arrays, missing %s in %s", expected, text)
		}
	}
	if strings.Contains(text, "null") {
		t.Fatalf("empty context entries should not marshal null lists: %s", text)
	}
}

func TestOfficialAuthStatusFromContentsReadsEmail(t *testing.T) {
	status := officialAuthStatusFromContents(fakeChatGPTAuthJSON(t, "alpha@example.com"), "test")

	if !status.Authenticated {
		t.Fatal("official auth should be authenticated")
	}
	if status.AccountLabel != "alpha@example.com" {
		t.Fatalf("account label mismatch: %q", status.AccountLabel)
	}
}

func TestOfficialAuthStatusFromContentsUsesFallbackForUnknownEmail(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"refresh_token": "refresh-token",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal auth json: %v", err)
	}

	status := officialAuthStatusFromContents(string(data), "test")

	if !status.Authenticated {
		t.Fatal("official auth should be authenticated")
	}
	if status.AccountLabel != "官方账号" {
		t.Fatalf("fallback account label mismatch: %q", status.AccountLabel)
	}
}

func TestOfficialAuthStatusFromContentsReadsProfileEmail(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"refresh_token": "refresh-token",
			"profile": map[string]any{
				"email": "profile@example.com",
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal auth json: %v", err)
	}

	status := officialAuthStatusFromContents(string(data), "test")

	if !status.Authenticated {
		t.Fatal("official auth should be authenticated")
	}
	if status.AccountLabel != "profile@example.com" {
		t.Fatalf("profile account label mismatch: %q", status.AccountLabel)
	}
}

func TestOfficialAuthStatusFromContentsRejectsMissingTokens(t *testing.T) {
	status := officialAuthStatusFromContents(`{"auth_mode":"chatgpt","tokens":{}}`, "test")

	if status.Authenticated {
		t.Fatalf("auth without tokens should not be authenticated: %#v", status)
	}
}

func TestOfficialAuthStatusFromContentsRejectsAPIKeyMode(t *testing.T) {
	status := officialAuthStatusFromContents(`{"auth_mode":"apikey","tokens":{"refresh_token":"refresh-token"}}`, "test")

	if status.Authenticated {
		t.Fatalf("apikey auth should not be treated as official auth: %#v", status)
	}
}

func TestLoadSettingsMigratesCurrentOfficialAuthToActiveProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{
		{ID: "first", Name: "First", RelayMode: "official", Protocol: "responses"},
		{ID: "second", Name: "Second", RelayMode: "official", Protocol: "responses"},
	}
	settings.ActiveRelayID = "second"
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), fakeChatGPTAuthJSON(t, "active@example.com"))
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "openai"`+"\n")
	if err := atomicWriteJSON(settingsPath(), settings); err != nil {
		t.Fatalf("failed to write settings: %v", err)
	}

	loaded := loadSettings()

	if loaded.RelayProfiles[0].OfficialAuthContents != "" {
		t.Fatal("inactive profile should not receive migrated official auth")
	}
	active := activeRelayProfile(loaded)
	if active.ID != "second" {
		t.Fatalf("active profile mismatch: %q", active.ID)
	}
	if active.OfficialAccountLabel != "active@example.com" {
		t.Fatalf("official account label mismatch: %q", active.OfficialAccountLabel)
	}
	if active.OfficialAuthContents == "" {
		t.Fatal("official auth contents should be migrated")
	}
	if active.AuthContents == "" {
		t.Fatal("auth contents should be migrated")
	}
	if active.ConfigContents != "" {
		t.Fatalf("official pure profile should not migrate live config contents: %q", active.ConfigContents)
	}
}

func TestBridgeSettingsIncludesThreadIDBadge(t *testing.T) {
	settings := defaultSettings()
	settings.CodexAppThreadIDBadge = true
	runtime := &launcherRuntime{}

	value := runtime.bridgeSettingsValue(settings)

	if !boolFromAny(value["codexAppThreadIdBadge"]) {
		t.Fatalf("bridge settings should include codexAppThreadIdBadge: %#v", value)
	}
}

func TestBridgeSettingsIncludesActiveRelayModeForMixedSites(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{
		{ID: "official", Name: "Official", RelayMode: "official"},
		{ID: "mixed", Name: "Mixed", RelayMode: "mixedApi"},
	}
	settings.ActiveRelayID = "mixed"
	runtime := &launcherRuntime{}

	value := runtime.bridgeSettingsValue(settings)

	if got := stringFromAny(value["activeRelayMode"]); got != "mixedApi" {
		t.Fatalf("bridge settings should expose active mixed relay mode for Sites support, got %q in %#v", got, value)
	}
	if got := stringFromAny(value["activeRelayID"]); got != "mixed" {
		t.Fatalf("bridge settings should expose active relay id, got %q in %#v", got, value)
	}
}

func TestSettingsDefaultsIncludeCodexPlusV1224Fields(t *testing.T) {
	settings := defaultSettings()
	value := (&launcherRuntime{}).bridgeSettingsValue(settings)

	for _, key := range []string{
		"codexAppForceChineseLocale",
		"codexAppNativeMenuLocalization",
		"codexAppPluginMarketplaceUnlock",
	} {
		if !boolFromAny(value[key]) {
			t.Fatalf("bridge settings should default %s to true: %#v", key, value)
		}
	}
	if boolFromAny(value["codexAppPluginAutoExpand"]) {
		t.Fatalf("deprecated plugin auto-expand should default false: %#v", value)
	}
	if boolFromAny(value["codexAppPasteFix"]) {
		t.Fatalf("paste fix should default false: %#v", value)
	}
	if boolFromAny(value["codexAppFastStartup"]) {
		t.Fatalf("fast startup should default false: %#v", value)
	}
	if got := stringFromAny(value["codexAppImageOverlayFitMode"]); got != "fit" {
		t.Fatalf("image overlay fit mode should default to fit, got %q", got)
	}
}

func TestDeprecatedPluginSettingsAreNotExposedByBridge(t *testing.T) {
	value := (&launcherRuntime{}).bridgeSettingsValue(defaultSettings())
	for _, key := range []string{"codexAppPluginEntryUnlock", "codexAppForcePluginInstall"} {
		if _, ok := value[key]; ok {
			t.Fatalf("deprecated setting %s should not be exposed: %#v", key, value)
		}
	}
}

func TestSettingsPreserveAggregateRelayAndMobileControl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.MobileControlEnabled = true
	settings.MobileControlRelayURL = "ws://example.test:57323"
	settings.MobileControlRoom = "room-a"
	settings.MobileControlKey = "key-a"
	settings.RelayProfiles = []relayProfile{
		{ID: "a", Name: "A", RelayMode: "pureApi", Protocol: "responses", BaseURL: "https://a.example/v1", APIKey: "sk-a"},
		{ID: "b", Name: "B", RelayMode: "pureApi", Protocol: "responses", BaseURL: "https://b.example/v1", APIKey: "sk-b"},
		{ID: "agg", Name: "Agg", RelayMode: "aggregate", Protocol: "responses"},
	}
	settings.ActiveRelayID = "agg"
	settings.AggregateRelayProfiles = []aggregateRelayProfile{{
		ID:       "agg",
		Name:     "Agg",
		Strategy: "weightedRoundRobin",
		Members:  []aggregateRelayMember{{RelayID: "a", Weight: 1}, {RelayID: "b", Weight: 3}},
	}}
	settings.ActiveAggregateRelayID = "agg"

	if err := saveSettings(settings); err != nil {
		t.Fatalf("save settings failed: %v", err)
	}
	loaded := loadSettings()
	if loaded.MobileControlRelayURL != "ws://example.test:57323" || loaded.MobileControlRoom != "room-a" || loaded.MobileControlKey != "key-a" {
		t.Fatalf("mobile settings mismatch: %#v", loaded)
	}
	aggregate, ok := activeAggregateRelayProfile(loaded)
	if !ok {
		t.Fatal("active aggregate should resolve")
	}
	if aggregate.Strategy != "weightedRoundRobin" || aggregate.Members[1].Weight != 3 {
		t.Fatalf("aggregate mismatch: %#v", aggregate)
	}
	if !activeRelayUsesProtocolProxy(loaded) {
		t.Fatal("aggregate relay should use protocol proxy")
	}
}

func TestRelayProfileHTTPProxyDefaultsAndNormalization(t *testing.T) {
	defaultProfile := defaultRelayProfile()
	if defaultProfile.ProxyEnabled {
		t.Fatal("default relay profile should not enable HTTP proxy")
	}
	if defaultProfile.ProxyURL != "" {
		t.Fatalf("default relay proxy URL should be empty, got %q", defaultProfile.ProxyURL)
	}

	settings := normalizeSettings(backendSettings{RelayProfiles: []relayProfile{{
		ID:           "relay",
		Name:         "Relay",
		RelayMode:    "pureApi",
		Protocol:     "responses",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "sk-test",
		ProxyEnabled: true,
		ProxyURL:     "  http://127.0.0.1:7890  ",
	}}})
	if got := settings.RelayProfiles[0].ProxyURL; got != "http://127.0.0.1:7890" {
		t.Fatalf("normalizeSettings should trim relay proxy URL, got %q", got)
	}
}

func TestRelayRotationSelectorStrategies(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{
		{ID: "a", Name: "A", RelayMode: "pureApi", Protocol: "responses", BaseURL: "https://a.example/v1", APIKey: "sk-a"},
		{ID: "b", Name: "B", RelayMode: "pureApi", Protocol: "responses", BaseURL: "https://b.example/v1", APIKey: "sk-b"},
		{ID: "agg", Name: "Agg", RelayMode: "aggregate", Protocol: "responses"},
	}
	settings.ActiveRelayID = "agg"
	settings.ActiveAggregateRelayID = "agg"
	settings.AggregateRelayProfiles = []aggregateRelayProfile{{
		ID:       "agg",
		Name:     "Agg",
		Strategy: "weightedRoundRobin",
		Members:  []aggregateRelayMember{{RelayID: "a", Weight: 1}, {RelayID: "b", Weight: 2}},
	}}
	clearRelayRotationSelector()
	first, err := selectRelayForRequest(settings, rotationContext{})
	if err != nil {
		t.Fatalf("select first failed: %v", err)
	}
	second, _ := selectRelayForRequest(settings, rotationContext{})
	third, _ := selectRelayForRequest(settings, rotationContext{})
	if first.ID != "a" || second.ID != "b" || third.ID != "b" {
		t.Fatalf("weighted sequence mismatch: %s %s %s", first.ID, second.ID, third.ID)
	}

	settings.AggregateRelayProfiles[0].Strategy = "conversationRoundRobin"
	clearRelayRotationSelector()
	convoA1, _ := selectRelayForRequest(settings, rotationContext{ConversationID: "c1"})
	convoB, _ := selectRelayForRequest(settings, rotationContext{ConversationID: "c2"})
	convoA2, _ := selectRelayForRequest(settings, rotationContext{ConversationID: "c1"})
	if convoA1.ID != convoA2.ID || convoA1.ID == convoB.ID {
		t.Fatalf("conversation assignments mismatch: %s %s %s", convoA1.ID, convoB.ID, convoA2.ID)
	}
}

func TestMobileRelayShareURL(t *testing.T) {
	settings := defaultSettings()
	settings.MobileControlRelayURL = "wss://relay.example.test"
	settings.MobileControlRoom = "项目 A"
	settings.MobileControlKey = "a+b&c"

	url := mobileRelayShareURL(settings)
	if url != "https://relay.example.test/mobile?key=a%2Bb%26c&room=%E9%A1%B9%E7%9B%AE+A" {
		t.Fatalf("mobile share URL mismatch: %s", url)
	}
}

func TestModelSuffixAndWindowsWriteCatalogContextWindow(t *testing.T) {
	home := t.TempDir()
	relay := defaultRelayProfile()
	relay.Model = "deepseek-v4[1M]"
	relay.ModelList = "qwen-plus\nbad[nope]\nqwen-plus"
	relay.ModelWindows = `{"qwen-plus":"200K"}`

	if err := writeRelayModelCatalog(home, relay); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(relayModelCatalogRelativePath(relay.ID))))
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}
	var catalog map[string][]map[string]any
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("catalog json invalid: %v", err)
	}
	models := catalog["models"]
	if len(models) != 3 {
		t.Fatalf("catalog model count mismatch: %#v", models)
	}
	if stringFromAny(models[0]["slug"]) != "deepseek-v4" || int(models[0]["context_window"].(float64)) != 1000000 {
		t.Fatalf("suffix model mismatch: %#v", models[0])
	}
	if stringFromAny(models[1]["slug"]) != "qwen-plus" || int(models[1]["context_window"].(float64)) != 200000 {
		t.Fatalf("model window mismatch: %#v", models[1])
	}
	if stringFromAny(models[2]["slug"]) != "bad[nope]" {
		t.Fatalf("invalid suffix should remain in slug: %#v", models[2])
	}
}

func TestRelayModelCatalogKeepsAstraMetadataAndProfileIsolation(t *testing.T) {
	home := t.TempDir()
	profile := relayProfile{ID: strings.Repeat("profile-", 40), Model: "gpt-6-astra", ModelList: "gpt-6-astra"}
	path := relayModelCatalogRelativePath(profile.ID)
	if len(filepath.Base(path)) > 80 {
		t.Fatalf("profile catalog filename was not bounded: %q", path)
	}
	if err := writeRelayModelCatalog(home, profile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("catalog models = %#v", catalog.Models)
	}
	model := catalog.Models[0]
	if uint64FromAny(model["context_window"], 0) != 272000 || uint64FromAny(model["max_context_window"], 0) != 872000 {
		t.Fatalf("Astra context metadata missing: %#v", model)
	}
	if !strings.Contains(string(data), `"service_tiers"`) {
		t.Fatalf("Astra service tiers missing: %s", data)
	}
	if isManagedRelayModelCatalog("model-catalogs/../../outside.json") {
		t.Fatal("catalog traversal path must not be treated as app-managed")
	}
}

func TestRelayModelAutoCompactWritesRoundedPerModelThreshold(t *testing.T) {
	data, ok, err := relayModelCatalogData(relayProfile{
		ID: "compact", ModelList: "model-a\nmodel-b", ModelWindows: `{"model-a":"200K","model-b":"18446744073709551615"}`,
		ModelAutoCompact: `{"model-a":"84.5%","model-b":"100%"}`,
	})
	if err != nil || !ok {
		t.Fatalf("catalog generation = %v, %v", ok, err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if got := uint64FromAny(catalog.Models[0]["auto_compact_token_limit"], 0); got != 169000 {
		t.Fatalf("fractional compact threshold = %d", got)
	}
	if got := uint64FromAny(catalog.Models[1]["auto_compact_token_limit"], 0); got != math.MaxUint64 {
		t.Fatalf("maximum-window 100%% threshold = %d", got)
	}
}

func TestPendingProviderImportConfirmCreatesRelayProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	request := providerImportRequest{
		Name:      "External Provider",
		BaseURL:   "https://relay.example/v1/",
		APIKey:    "sk-test",
		WireAPI:   "chat",
		RelayMode: "pureApi",
	}
	if err := savePendingProviderImport(request); err != nil {
		t.Fatalf("save pending import failed: %v", err)
	}
	pending, err := loadPendingProviderImport()
	if err != nil || pending == nil {
		t.Fatalf("load pending import failed: %#v %v", pending, err)
	}
	result, err := confirmPendingProviderImport()
	if err != nil {
		t.Fatalf("confirm pending import failed: %v", err)
	}
	if result == nil || !result.Imported {
		t.Fatalf("import result mismatch: %#v", result)
	}
	settings := loadSettings()
	profile := activeRelayProfile(settings)
	if profile.Name != "External Provider" || profile.Protocol != "chatCompletions" || profile.BaseURL != "https://relay.example/v1" {
		t.Fatalf("imported profile mismatch: %#v", profile)
	}
	if next, err := loadPendingProviderImport(); err != nil || next != nil {
		t.Fatalf("pending import should be cleared: %#v %v", next, err)
	}
}

func TestDeprecatedMobileRelaySettingsDoNotEnableHelperRuntime(t *testing.T) {
	settings := defaultSettings()
	settings.Enhancements = false
	settings.RelayProfiles = []relayProfile{{ID: "official", Name: "Official", RelayMode: "official", Protocol: "responses", ImageGenerationEnabled: true}}
	settings.ActiveRelayID = "official"
	settings.MobileControlEnabled = true
	settings.MobileControlRelayURL = "ws://relay.example.test"
	settings.MobileControlRoom = "room-a"
	settings.MobileControlKey = "key-a"

	if helperNeeded(settings) {
		t.Fatal("deprecated mobile relay settings should not require helper runtime")
	}
}

func TestMobileRelayPayloadEncryptionRoundTrip(t *testing.T) {
	block := mobileRelayCipher("secret-key")
	payload := map[string]any{
		"type":   "httpRequest",
		"id":     "req-1",
		"path":   "/backend/status",
		"method": "GET",
	}

	envelope, err := encryptMobileRelayPayload(block, payload)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if envelope["type"] != "encrypted" {
		t.Fatalf("expected encrypted envelope: %#v", envelope)
	}
	decoded, err := decryptMobileRelayRequest(block, envelope)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if decoded["type"] != "httpRequest" || decoded["id"] != "req-1" || decoded["path"] != "/backend/status" {
		t.Fatalf("decoded payload mismatch: %#v", decoded)
	}
}

func TestMobileRelayPlaintextEnvelope(t *testing.T) {
	block := mobileRelayCipher("secret-key")
	payload := map[string]any{"type": "appServerClose", "sessionId": "s1"}

	envelope, err := encodeMobileRelayPayload(block, true, payload)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	decoded, err := decryptMobileRelayRequest(block, envelope)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded["type"] != "appServerClose" || decoded["sessionId"] != "s1" {
		t.Fatalf("plaintext decoded mismatch: %#v", decoded)
	}
}

func TestMobileAppServerPortFromURLRequiresLoopback(t *testing.T) {
	cases := map[string]uint16{
		"ws://127.0.0.1:40123/rpc": 40123,
		"http://localhost:40124":   40124,
		"ws://example.test:40123":  0,
		"wss://127.0.0.1:40123":    0,
		"not a url":                0,
	}
	for raw, want := range cases {
		if got := mobileAppServerPortFromURL(raw); got != want {
			t.Fatalf("mobileAppServerPortFromURL(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestCodexHomeDirIgnoresMissingCODEXHOME(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", missing)

	if got := codexHomeDir(); got != filepath.Join(home, ".codex") {
		t.Fatalf("codexHomeDir should ignore missing CODEX_HOME: got %q", got)
	}
}

func TestEnvConflictsDetectsOpenAIOnly(t *testing.T) {
	conflicts := detectedEnvConflictsFromPairs(map[string]string{
		"OPENAI_API_KEY":        "sk-test",
		"OPENAI_BASE_URL":       "",
		"CODEX_HOME":            "/tmp/codex",
		"CUSTOM_OPENAI_API_KEY": "sk-custom",
	}, "process")

	var names []string
	for _, conflict := range conflicts {
		names = append(names, conflict.Name)
	}
	if strings.Join(names, ",") != "OPENAI_API_KEY,OPENAI_BASE_URL" {
		t.Fatalf("env conflict names mismatch: %#v", names)
	}
	if !conflicts[0].ValuePresent || conflicts[1].ValuePresent {
		t.Fatalf("value presence mismatch: %#v", conflicts)
	}
}

func TestRemoveEnvConflictsBacksUpAndRemovesProcessEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CUSTOM_OPENAI_API_KEY", "sk-custom")
	backupDir := filepath.Join(t.TempDir(), "backups")

	result, err := removeEnvConflicts([]string{"OPENAI_API_KEY", "CUSTOM_OPENAI_API_KEY"}, backupDir)
	if err != nil {
		t.Fatalf("remove env conflicts failed: %v", err)
	}
	if _, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		t.Fatal("OPENAI_API_KEY should be removed from process env")
	}
	if got := os.Getenv("CUSTOM_OPENAI_API_KEY"); got != "sk-custom" {
		t.Fatalf("custom API key should not be removed, got %q", got)
	}
	if backupPath := stringFromAny(result["backupPath"]); backupPath == "" || !fileExists(backupPath) {
		t.Fatalf("backup should be written: %#v", result)
	}
}

func TestSaveSettingsPreservesUnknownTopLevelFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, settingsPath(), `{
  "futureFeature": {"enabled": true, "mode": "next"},
  "codexAppPluginEntryUnlock": true,
  "codexAppForcePluginInstall": true,
  "codexAppPath": "",
  "relayProfiles": [{"id":"default","name":"Default","protocol":"responses","relayMode":"official"}],
  "activeRelayId": "default"
}`)

	settings := loadSettings()
	settings.Language = "en-US"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := readJSON(settingsPath(), &raw); err != nil {
		t.Fatalf("failed to read saved settings: %v", err)
	}
	if string(raw["futureFeature"]) == "" {
		t.Fatal("unknown top-level field should be preserved")
	}
	if !strings.Contains(string(raw["futureFeature"]), `"mode": "next"`) {
		t.Fatalf("unknown field value changed: %s", string(raw["futureFeature"]))
	}
	for _, key := range []string{"codexAppPluginEntryUnlock", "codexAppForcePluginInstall"} {
		if string(raw[key]) != "true" {
			t.Fatalf("deprecated unknown field %s should remain byte-compatible, got %s", key, raw[key])
		}
	}
}

func TestSaveSettingsPersistsOnboardingCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.OnboardingCompleted = true
	settings.OnboardingCompletedAt = "2026-06-09T12:00:00Z"
	settings.OnboardingCompletedPlatform = "windows"

	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	loaded := loadSettings()
	if !loaded.OnboardingCompleted {
		t.Fatal("onboarding completion should persist")
	}
	if loaded.OnboardingCompletedAt != "2026-06-09T12:00:00Z" {
		t.Fatalf("completion time mismatch: %q", loaded.OnboardingCompletedAt)
	}
	if loaded.OnboardingCompletedPlatform != "windows" {
		t.Fatalf("completion platform mismatch: %q", loaded.OnboardingCompletedPlatform)
	}
}

func TestRelayStatusDetectsBoundOfficialAuthWithoutCurrentAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		OfficialAuthContents: fakeChatGPTAuthJSON(t, "bound@example.com"),
		OfficialAccountLabel: "bound@example.com",
	}}
	settings.ActiveRelayID = "official"

	status := relayStatusFromHome(filepath.Join(home, ".codex"), settings)

	if boolFromAny(status["currentAuthenticated"]) {
		t.Fatal("current auth file should not be detected")
	}
	if !boolFromAny(status["boundOfficialAuthenticated"]) {
		t.Fatalf("bound official auth should be detected: %#v", status)
	}
	if !boolFromAny(status["officialAuthenticated"]) {
		t.Fatalf("overall official auth should be detected: %#v", status)
	}
	if got := stringFromAny(status["boundOfficialAccountLabel"]); got != "bound@example.com" {
		t.Fatalf("bound account label mismatch: %q", got)
	}
	if got := stringFromAny(status["boundOfficialProfileId"]); got != "official" {
		t.Fatalf("bound profile id mismatch: %q", got)
	}
}

func TestInstallGuideConnectionDetectsBoundOfficialAuth(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		OfficialAuthContents: fakeChatGPTAuthJSON(t, "bound@example.com"),
		OfficialAccountLabel: "bound@example.com",
	}}
	settings.ActiveRelayID = "official"
	relayStatus := relayStatusFromHome(filepath.Join(t.TempDir(), ".codex"), settings)

	payload := installGuideConnectionPayload(settings, relayStatus)

	if !boolFromAny(payload["ready"]) {
		t.Fatalf("bound official auth should make guide connection ready: %#v", payload)
	}
	if !boolFromAny(payload["officialReady"]) {
		t.Fatalf("official account should be ready: %#v", payload)
	}
	if got := stringFromAny(payload["profileId"]); got != "official" {
		t.Fatalf("profile id mismatch: %q", got)
	}
}

func TestInstallGuideConnectionRequiresMixedApiFields(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "mixed",
		Name:                 "Mixed",
		RelayMode:            "mixedApi",
		Protocol:             "responses",
		OfficialAuthContents: fakeChatGPTAuthJSON(t, "mixed@example.com"),
		OfficialAccountLabel: "mixed@example.com",
	}}
	settings.ActiveRelayID = "mixed"
	relayStatus := relayStatusFromHome(filepath.Join(t.TempDir(), ".codex"), settings)

	payload := installGuideConnectionPayload(settings, relayStatus)

	if boolFromAny(payload["ready"]) {
		t.Fatalf("mixed API without base URL/key should not be ready: %#v", payload)
	}
	if !boolFromAny(payload["officialReady"]) || boolFromAny(payload["apiReady"]) {
		t.Fatalf("mixed API readiness flags mismatch: %#v", payload)
	}
}

func TestDefaultCCSDBPathPrefersExistingHomeDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expected := filepath.Join(home, ".cc-switch", "cc-switch.db")
	writeTestFile(t, expected, "sqlite")

	if got := defaultCCSDBPath(); got != expected {
		t.Fatalf("ccswitch database path mismatch: %q", got)
	}
}

func TestPlatformAndArchDisplayNames(t *testing.T) {
	if got := platformDisplayName("windows"); got != "Windows" {
		t.Fatalf("windows platform label mismatch: %q", got)
	}
	if got := archDisplayName("amd64"); got != "x64" {
		t.Fatalf("amd64 arch label mismatch: %q", got)
	}
	if got := archDisplayName("arm64"); got != "ARM64" {
		t.Fatalf("arm64 arch label mismatch: %q", got)
	}
}

func TestInstallGuidePlatformGuideSeparatesMacAndWindows(t *testing.T) {
	mac := installGuidePlatformGuide("darwin")
	if got := stringFromAny(mac["title"]); !strings.Contains(got, "macOS") {
		t.Fatalf("mac guide should be labeled macOS: %#v", mac)
	}
	for _, expected := range []string{"ChatGPT.app", "官方"} {
		if !strings.Contains(stringFromAny(mac["detectionNote"])+stringFromAny(mac["installTitle"])+stringFromAny(mac["installActionLabel"]), expected) {
			t.Fatalf("mac guide should mention %q: %#v", expected, mac)
		}
	}
	if got := stringFromAny(mac["desktopRuntime"]); !strings.Contains(got, "WebKit") {
		t.Fatalf("mac runtime should mention WebKit: %q", got)
	}
	if got := stringFromAny(mac["manualPrimaryMode"]); got != "folder" {
		t.Fatalf("mac manual mode mismatch: %q", got)
	}

	windows := installGuidePlatformGuide("windows")
	if got := stringFromAny(windows["title"]); !strings.Contains(got, "Windows") {
		t.Fatalf("windows guide should be labeled Windows: %#v", windows)
	}
	combined := stringFromAny(windows["systemDescription"]) + stringFromAny(windows["detectionNote"]) + stringFromAny(windows["launchTargetLabel"])
	for _, expected := range []string{"ChatGPT.exe", "WindowsApps", "AppUserModelID", "MSIX"} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("windows guide should mention %q: %#v", expected, windows)
		}
	}
	if got := stringFromAny(windows["desktopRuntime"]); !strings.Contains(got, "WebView2") {
		t.Fatalf("windows runtime should mention WebView2: %q", got)
	}
	if got := stringFromAny(windows["manualPrimaryMode"]); got != "file" {
		t.Fatalf("windows manual mode mismatch: %q", got)
	}
	if got := stringFromAny(windows["manualSecondaryMode"]); got != "folder" {
		t.Fatalf("windows secondary manual mode mismatch: %q", got)
	}
}

func TestLoadInstallGuideStatusReportsCurrentPlatformCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.OnboardingCompleted = true
	settings.OnboardingCompletedAt = "2026-06-09T12:00:00Z"
	settings.OnboardingCompletedPlatform = runtime.GOOS
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).loadInstallGuideStatus(contextWithCanceledNetwork())

	if !boolFromAny(result["onboardingCompleted"]) {
		t.Fatalf("raw onboarding completion should be reported: %#v", result)
	}
	if !boolFromAny(result["onboardingCompletedForCurrentPlatform"]) {
		t.Fatalf("current platform completion should be true: %#v", result)
	}
	if boolFromAny(result["onboardingPlatformMismatch"]) {
		t.Fatalf("current platform should not mismatch: %#v", result)
	}
	guide, ok := result["platformGuide"].(map[string]any)
	if !ok {
		t.Fatalf("platform guide missing: %#v", result["platformGuide"])
	}
	if got := stringFromAny(guide["platform"]); got != runtime.GOOS {
		t.Fatalf("platform guide should match runtime platform: %q", got)
	}
}

func TestLoadInstallGuideStatusReportsPlatformMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	otherPlatform := "windows"
	if runtime.GOOS == "windows" {
		otherPlatform = "darwin"
	}
	settings := defaultSettings()
	settings.OnboardingCompleted = true
	settings.OnboardingCompletedAt = "2026-06-09T12:00:00Z"
	settings.OnboardingCompletedPlatform = otherPlatform
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).loadInstallGuideStatus(contextWithCanceledNetwork())

	if !boolFromAny(result["onboardingCompleted"]) {
		t.Fatalf("raw onboarding completion should be reported: %#v", result)
	}
	if boolFromAny(result["onboardingCompletedForCurrentPlatform"]) {
		t.Fatalf("mismatched platform should not be current-platform complete: %#v", result)
	}
	if !boolFromAny(result["onboardingPlatformMismatch"]) {
		t.Fatalf("mismatched platform should be reported: %#v", result)
	}
}

func TestListCCSProvidersReadsSQLiteDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, name TEXT, settings_config TEXT, app_type TEXT, sort_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("failed to create providers table: %v", err)
	}
	config := `{"base_url":"https://relay.example.com/v1","api_key":"relay-key"}`
	if _, err := db.Exec(`INSERT INTO providers (id, name, settings_config, app_type, sort_index, created_at) VALUES (?, ?, ?, ?, ?, ?)`, "provider-1", "Relay One", config, "codex", 1, "2026-05-27"); err != nil {
		t.Fatalf("failed to insert provider: %v", err)
	}

	providers, err := listCCSProviders(dbPath)
	if err != nil {
		t.Fatalf("failed to list providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count mismatch: %d", len(providers))
	}
	if providers[0].SourceID != "provider-1" || providers[0].BaseURL != "https://relay.example.com/v1" || providers[0].APIKey != "relay-key" {
		t.Fatalf("provider mismatch: %#v", providers[0])
	}
}

func TestListCCSProvidersReadsSettingsConfigVariants(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (providerId TEXT, displayName TEXT, settingsConfig TEXT)`); err != nil {
		t.Fatalf("failed to create providers table: %v", err)
	}
	configText := strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		`[model_providers.CodexPlusPlus]`,
		`wire_api = "chat"`,
		`base_url = "https://relay.example.com/v1/"`,
		`experimental_bearer_token = "toml-key"`,
	}, "\n")
	settingsConfig, _ := json.Marshal(map[string]any{
		"config": configText,
		"auth":   `{"tokens":{"id_token":"official-token"}}`,
	})
	if _, err := db.Exec(`INSERT INTO providers (providerId, displayName, settingsConfig) VALUES (?, ?, ?)`, "provider-2", "Relay Two", string(settingsConfig)); err != nil {
		t.Fatalf("failed to insert provider: %v", err)
	}

	providers, err := listCCSProviders(dbPath)
	if err != nil {
		t.Fatalf("failed to list providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count mismatch: %d", len(providers))
	}
	provider := providers[0]
	if provider.SourceID != "provider-2" || provider.Name != "Relay Two" {
		t.Fatalf("provider identity mismatch: %#v", provider)
	}
	if provider.BaseURL != "https://relay.example.com/v1" || provider.APIKey != "toml-key" || provider.Protocol != "chatCompletions" {
		t.Fatalf("provider settings mismatch: %#v", provider)
	}
	if provider.ConfigContents != configText {
		t.Fatalf("config contents mismatch:\n%s", provider.ConfigContents)
	}
	if provider.AuthContents != `{"tokens":{"id_token":"official-token"}}` {
		t.Fatalf("auth contents mismatch: %q", provider.AuthContents)
	}
}

func TestListCCSProvidersReadsRawTomlConfigColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, name TEXT, config TEXT, app_type TEXT)`); err != nil {
		t.Fatalf("failed to create providers table: %v", err)
	}
	configText := strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		`[model_providers.CodexPlusPlus]`,
		`base_url = "https://relay.example.com/v1"`,
		`experimental_bearer_token = "raw-key"`,
	}, "\n")
	if _, err := db.Exec(`INSERT INTO providers (id, name, config, app_type) VALUES (?, ?, ?, ?)`, "provider-3", "Relay Three", configText, "codex"); err != nil {
		t.Fatalf("failed to insert provider: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO providers (id, name, config, app_type) VALUES (?, ?, ?, ?)`, "provider-4", "Other", configText, "claude"); err != nil {
		t.Fatalf("failed to insert non-codex provider: %v", err)
	}

	providers, err := listCCSProviders(dbPath)
	if err != nil {
		t.Fatalf("failed to list providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count mismatch: %d", len(providers))
	}
	if providers[0].SourceID != "provider-3" || providers[0].APIKey != "raw-key" || providers[0].ConfigContents != configText {
		t.Fatalf("provider mismatch: %#v", providers[0])
	}
}

func TestListCCSProvidersReadsMetaAPIFormatAndFillsBearerToken(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, name TEXT, settings_config TEXT, meta TEXT, app_type TEXT)`); err != nil {
		t.Fatalf("failed to create providers table: %v", err)
	}
	configText := strings.Join([]string{
		`model_provider = "custom"`,
		`[model_providers.custom]`,
		`name = "Custom"`,
		`wire_api = "responses"`,
		`base_url = "https://relay.example.com/v1"`,
	}, "\n")
	settingsConfig, _ := json.Marshal(map[string]any{
		"config": configText,
		"auth":   map[string]any{"OPENAI_API_KEY": "auth-key"},
	})
	meta, _ := json.Marshal(map[string]any{"apiFormat": "openai_chat"})
	if _, err := db.Exec(`INSERT INTO providers (id, name, settings_config, meta, app_type) VALUES (?, ?, ?, ?, ?)`, "provider-5", "Relay Five", string(settingsConfig), string(meta), "codex"); err != nil {
		t.Fatalf("failed to insert provider: %v", err)
	}

	providers, err := listCCSProviders(dbPath)
	if err != nil {
		t.Fatalf("failed to list providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count mismatch: %d", len(providers))
	}
	provider := providers[0]
	if provider.Protocol != "chatCompletions" || provider.APIKey != "auth-key" {
		t.Fatalf("provider metadata mismatch: %#v", provider)
	}
	if !strings.Contains(provider.ConfigContents, `experimental_bearer_token = "auth-key"`) {
		t.Fatalf("config should be filled with bearer token:\n%s", provider.ConfigContents)
	}
}

func TestOfficialModeRequiresBoundOfficialAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{ID: "official", Name: "Official", RelayMode: "official", Protocol: "responses"}}
	settings.ActiveRelayID = "official"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).clearRelayInjection()

	if result["status"] != "failed" {
		t.Fatalf("official switch without bound auth should fail: %#v", result)
	}
}

func TestOfficialModeWritesBoundOfficialAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "official@example.com")
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "official@example.com",
	}}
	settings.ActiveRelayID = "official"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "CodexPlusPlus"`+"\n\n[model_providers.CodexPlusPlus]\nbase_url = \"https://api.example.com\"\n")
	result := (&server{}).clearRelayInjection()

	if result["status"] != "ok" {
		t.Fatalf("official switch should succeed: %#v", result)
	}
	status := officialAuthStatus(filepath.Join(home, ".codex"))
	if !status.Authenticated || status.AccountLabel != "official@example.com" {
		t.Fatalf("bound official auth was not written: %#v", status)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(string(config), "CodexPlusPlus") {
		t.Fatalf("official mode should clear relay provider config:\n%s", string(config))
	}
	if !strings.Contains(string(config), `model_provider = "openai"`) {
		t.Fatalf("official mode should explicitly restore openai provider:\n%s", string(config))
	}
}

func TestActivateOfficialAuthWritesBoundOfficialAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "bound@example.com")
	currentAuth := fakeChatGPTAuthJSON(t, "current@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), currentAuth)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "bound@example.com",
	}}
	settings.ActiveRelayID = "official"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).activateOfficialAuth(map[string]any{
		"request": map[string]any{"profileId": "official"},
	})

	if result["status"] != "ok" {
		t.Fatalf("activate official auth should succeed: %#v", result)
	}
	status := officialAuthStatus(filepath.Join(home, ".codex"))
	if !status.Authenticated || status.AccountLabel != "bound@example.com" {
		t.Fatalf("bound official auth was not activated: %#v", status)
	}
}

func TestImportCurrentRelayFilesUpdatesTargetProfileSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), "model_provider = \"openai\"\n")
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), fakeChatGPTAuthJSON(t, "import@example.com"))
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{ID: "one", Name: "One", RelayMode: "official", Protocol: "responses"}}
	settings.ActiveRelayID = "one"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).importCurrentRelayFiles(map[string]any{
		"request": map[string]any{"profileId": "one"},
	})

	if result["status"] != "ok" {
		t.Fatalf("import current relay files should succeed: %#v", result)
	}
	loaded := loadSettings()
	profile := activeRelayProfile(loaded)
	if !strings.Contains(profile.ConfigContents, `model_provider = "openai"`) {
		t.Fatalf("config snapshot mismatch:\n%s", profile.ConfigContents)
	}
	if profile.AuthContents == "" || profile.OfficialAuthContents == "" {
		t.Fatalf("auth snapshot should be imported: %#v", profile)
	}
	if profile.OfficialAccountLabel != "import@example.com" {
		t.Fatalf("official account label mismatch: %q", profile.OfficialAccountLabel)
	}
}

func TestClearRelayInjectionFallsBackToLegacyOfficialAuthContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "legacy@example.com")
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "legacy",
		Name:                 "Legacy",
		RelayMode:            "official",
		Protocol:             "responses",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "legacy@example.com",
		AuthContents:         "",
		ConfigContents:       "",
	}}
	settings.ActiveRelayID = "legacy"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")

	result := (&server{}).clearRelayInjection()

	if result["status"] != "ok" {
		t.Fatalf("official switch should succeed with legacy auth fallback: %#v", result)
	}
	auth, _ := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if officialAuthStatusFromContents(string(auth), "auth").AccountLabel != "legacy@example.com" {
		t.Fatalf("legacy official auth should be written to live auth.json, got:\n%s", string(auth))
	}
}

func TestMixedModeWritesBoundOfficialAuthAndRelayConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "mixed@example.com")
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "mixed",
		Name:                 "Mixed",
		BaseURL:              "https://api.example.com",
		APIKey:               "relay-key",
		RelayMode:            "mixedApi",
		Protocol:             "responses",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "mixed@example.com",
	}}
	settings.ActiveRelayID = "mixed"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(false)

	if result["status"] != "ok" {
		t.Fatalf("mixed switch should succeed: %#v", result)
	}
	status := officialAuthStatus(filepath.Join(home, ".codex"))
	if !status.Authenticated || status.AccountLabel != "mixed@example.com" {
		t.Fatalf("bound mixed official auth was not written: %#v", status)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(config), `experimental_bearer_token = "relay-key"`) {
		t.Fatalf("mixed relay config missing bearer token:\n%s", string(config))
	}
	auth, _ := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if officialAuthStatusFromContents(string(auth), "auth").AccountLabel != "mixed@example.com" {
		t.Fatalf("mixed relay should use profile auth snapshot, got:\n%s", string(auth))
	}
}

func TestMixedModeRegeneratesStaleOfficialSnapshotAndRestoresHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubCodexPluginCommands(t, nil)
	officialAuth := fakeChatGPTAuthJSON(t, "mixed@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "openai"`+"\n")
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, strings.Join([]string{
		strings.Replace(testSessionRolloutLine(sessionID, "/project", "official chat"), "CodexPlusPlus", "openai", 1),
		testRolloutResponseMessage("user", "官方模式里的历史对话"),
	}, "\n")+"\n")
	createProviderSyncThreadsTable(t, filepath.Join(home, ".codex", "state_5.sqlite"), true)
	insertProviderSyncThread(t, filepath.Join(home, ".codex", "state_5.sqlite"), map[string]any{
		"id":                 sessionID,
		"rollout_path":       rolloutPath,
		"created_at":         1779962400,
		"updated_at":         1779962500,
		"source":             "vscode",
		"model_provider":     "openai",
		"cwd":                "/project",
		"title":              "official chat",
		"sandbox_policy":     `{"type":"danger-full-access"}`,
		"approval_mode":      "never",
		"tokens_used":        0,
		"has_user_event":     0,
		"archived":           0,
		"cli_version":        "",
		"first_user_message": "",
		"memory_mode":        "enabled",
		"created_at_ms":      1779962400000,
		"updated_at_ms":      1779962500000,
		"preview":            "",
	})
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "mixed",
		Name:                 "Mixed",
		BaseURL:              "https://api.example.com",
		APIKey:               "relay-key",
		RelayMode:            "mixedApi",
		Protocol:             "responses",
		ConfigContents:       `model_provider = "openai"` + "\n",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "mixed@example.com",
	}}
	settings.ActiveRelayID = "mixed"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(false)

	if result["status"] != "ok" {
		t.Fatalf("mixed switch should succeed with stale official snapshot: %#v", result)
	}
	config := readFile(filepath.Join(home, ".codex", "config.toml"))
	for _, expected := range []string{
		`model_provider = "CodexPlusPlus"`,
		`[model_providers.CodexPlusPlus]`,
		`experimental_bearer_token = "relay-key"`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("mixed switch should regenerate relay config missing %q:\n%s", expected, config)
		}
	}
	row := providerSyncThreadRow(t, filepath.Join(home, ".codex", "state_5.sqlite"), sessionID)
	if got := stringFromAny(row["model_provider"]); got != "CodexPlusPlus" {
		t.Fatalf("provider sync should move official history to mixed provider, got %q", got)
	}
	if got := stringFromAny(row["thread_source"]); got != "user" {
		t.Fatalf("thread_source should be restored for mixed history, got %q", got)
	}
	if got := int64FromFlexible(row["has_user_event"]); got != 1 {
		t.Fatalf("has_user_event should be restored for mixed history, got %#v", row["has_user_event"])
	}
	data := readFile(rolloutPath)
	if !strings.Contains(data, `"model_provider":"CodexPlusPlus"`) {
		t.Fatalf("rollout metadata should be rewritten to mixed provider:\n%s", data)
	}
}

func TestPureAPIModeKeepsCurrentAuthWhenProfileSnapshotMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "stored@example.com")
	currentAuth := fakeChatGPTAuthJSON(t, "current@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), currentAuth)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "pure",
		Name:                 "Pure",
		BaseURL:              "https://api.example.com",
		APIKey:               "pure-key",
		RelayMode:            "pureApi",
		Protocol:             "responses",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "stored@example.com",
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(true)

	if result["status"] != "ok" {
		t.Fatalf("pure API switch should succeed: %#v", result)
	}
	auth, _ := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if officialAuthStatusFromContents(string(auth), "auth").AccountLabel != "current@example.com" {
		t.Fatalf("pure API mode should preserve auth.json, got:\n%s", string(auth))
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(config), `experimental_bearer_token = "pure-key"`) {
		t.Fatalf("pure API config should carry provider bearer token:\n%s", string(config))
	}
	loaded := loadSettings()
	if activeRelayProfile(loaded).OfficialAccountLabel != "stored@example.com" {
		t.Fatal("pure API mode should not remove stored official binding")
	}
}

func TestPureAPIModePreservesCurrentAuthWhenProfileSnapshotPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentAuth := fakeChatGPTAuthJSON(t, "current@example.com")
	profileAuth := fakeChatGPTAuthJSON(t, "profile@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), currentAuth)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure",
		Name:           "Pure",
		BaseURL:        "https://api.example.com",
		APIKey:         "pure-key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: buildTestRelayConfig("https://api.example.com", "pure-key"),
		AuthContents:   profileAuth,
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(true)

	if result["status"] != "ok" {
		t.Fatalf("pure API switch should succeed: %#v", result)
	}
	auth, _ := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if officialAuthStatusFromContents(string(auth), "auth").AccountLabel != "current@example.com" {
		t.Fatalf("pure API should preserve current auth snapshot, got:\n%s", string(auth))
	}
}

func TestMixedModeRefreshesBoundOfficialAuthFromCurrentLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentAuth := fakeChatGPTAuthJSON(t, "same@example.com")
	staleAuth := strings.Replace(fakeChatGPTAuthJSON(t, "same@example.com"), "refresh-token", "old-refresh-token", 1)
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), currentAuth)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "mixed",
		Name:                 "Mixed",
		BaseURL:              "https://api.example.com",
		APIKey:               "relay-key",
		RelayMode:            "mixedApi",
		Protocol:             "responses",
		OfficialAuthContents: staleAuth,
		OfficialAccountLabel: "same@example.com",
		AuthContents:         staleAuth,
	}}
	settings.ActiveRelayID = "mixed"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(false)

	if result["status"] != "ok" {
		t.Fatalf("mixed switch should succeed: %#v", result)
	}
	auth := readFile(filepath.Join(home, ".codex", "auth.json"))
	if strings.TrimSpace(auth) != strings.TrimSpace(currentAuth) {
		t.Fatalf("mixed switch should keep refreshed current auth, got:\n%s", auth)
	}
	loaded := activeRelayProfile(loadSettings())
	if loaded.AuthContents != currentAuth || loaded.OfficialAuthContents != currentAuth {
		t.Fatalf("mixed profile should refresh stored auth from current login: %#v", loaded)
	}
}

func TestPureAPIModeWritesImportedConfigWithoutAuthOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentAuth := fakeChatGPTAuthJSON(t, "current@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), currentAuth)
	importedConfig := strings.Join([]string{
		`model_provider = "custom"`,
		`[model_providers.custom]`,
		`name = "Custom"`,
		`wire_api = "responses"`,
		`base_url = "https://imported.example.com/v1"`,
	}, "\n")
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure-imported",
		Name:           "Pure Imported",
		BaseURL:        "https://fallback.example.com",
		APIKey:         "imported-key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: importedConfig,
	}}
	settings.ActiveRelayID = "pure-imported"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(true)

	if result["status"] != "ok" {
		t.Fatalf("pure API switch should succeed: %#v", result)
	}
	auth, _ := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if officialAuthStatusFromContents(string(auth), "auth").AccountLabel != "current@example.com" {
		t.Fatalf("pure API mode should preserve auth.json, got:\n%s", string(auth))
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	configText := string(config)
	if !strings.Contains(configText, `base_url = "https://imported.example.com/v1"`) {
		t.Fatalf("pure API should write imported config.toml:\n%s", configText)
	}
	if strings.Contains(configText, "fallback.example.com") {
		t.Fatalf("pure API should not regenerate over imported config:\n%s", configText)
	}
	if !strings.Contains(configText, `experimental_bearer_token = "imported-key"`) {
		t.Fatalf("pure API should ensure bearer token in imported config:\n%s", configText)
	}
}

func TestPureAPISwitchRestoresPluginsAfterSnapshotOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubCodexPluginCommands(t, nil)
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), fakeChatGPTAuthJSON(t, "current@example.com"))
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), strings.Join([]string{
		`model_provider = "openai"`,
		``,
		`[marketplaces.openai-curated]`,
		`source_type = "local"`,
		`source = "` + filepath.Join(home, ".codex", ".tmp", "plugins") + `"`,
		``,
		`[plugins."github@openai-curated"]`,
		`enabled = true`,
		``,
	}, "\n"))
	writeTestFile(t, filepath.Join(home, ".codex", ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)
	writeTestFile(t, filepath.Join(home, ".codex", "plugins", "cache", "openai-curated", "github", "v1", ".codex-plugin", "plugin.json"), `{"name":"github"}`)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure",
		Name:           "Pure",
		BaseURL:        "https://api.example.com",
		APIKey:         "pure-key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: buildTestRelayConfig("https://api.example.com", "pure-key"),
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(true)

	if result["status"] != "ok" {
		t.Fatalf("pure API switch should succeed: %#v", result)
	}
	config := readFile(filepath.Join(home, ".codex", "config.toml"))
	for _, expected := range []string{
		`[marketplaces.openai-curated]`,
		`source = "` + filepath.Join(home, ".codex", ".tmp", "plugins") + `"`,
		`[plugins."github@openai-curated"]`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config missing %q after pure API switch:\n%s", expected, config)
		}
	}
	repair := result["pluginRepair"].(map[string]any)
	if stringFromAny(repair["marketplaceRefreshStatus"]) != "ok" {
		t.Fatalf("plugin repair should refresh marketplace: %#v", repair)
	}
}

func TestOfficialSwitchRestoresPluginsAfterSnapshotOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubCodexPluginCommands(t, nil)
	officialAuth := fakeChatGPTAuthJSON(t, "official@example.com")
	writeTestFile(t, filepath.Join(home, ".codex", ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)
	writeTestFile(t, filepath.Join(home, ".codex", "plugins", "cache", "openai-curated", "github", "v1", ".codex-plugin", "plugin.json"), `{"name":"github"}`)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		ConfigContents:       `model_provider = "openai"` + "\n",
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "official@example.com",
	}}
	settings.ActiveRelayID = "official"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).clearRelayInjection()

	if result["status"] != "ok" {
		t.Fatalf("official switch should succeed: %#v", result)
	}
	config := readFile(filepath.Join(home, ".codex", "config.toml"))
	for _, expected := range []string{
		`[marketplaces.openai-curated]`,
		`[plugins."github@openai-curated"]`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config missing %q after official switch:\n%s", expected, config)
		}
	}
}

func TestRelaySwitchRunsProviderSync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubCodexPluginCommands(t, nil)
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), fakeChatGPTAuthJSON(t, "current@example.com"))
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "openai"`+"\n")
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, testSessionRolloutLine(sessionID, "/project", "legacy title")+"\n")
	createProviderSyncThreadsTable(t, filepath.Join(home, ".codex", "state_5.sqlite"), true)
	insertProviderSyncThread(t, filepath.Join(home, ".codex", "state_5.sqlite"), map[string]any{
		"id":                 sessionID,
		"rollout_path":       rolloutPath,
		"created_at":         1779962400,
		"updated_at":         1779962500,
		"source":             "vscode",
		"model_provider":     "openai",
		"cwd":                "/project",
		"title":              "legacy title",
		"sandbox_policy":     `{"type":"danger-full-access"}`,
		"approval_mode":      "never",
		"tokens_used":        0,
		"has_user_event":     0,
		"archived":           0,
		"cli_version":        "",
		"first_user_message": "",
		"memory_mode":        "enabled",
		"created_at_ms":      1779962400000,
		"updated_at_ms":      1779962500000,
		"preview":            "",
	})
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure",
		Name:           "Pure",
		BaseURL:        "https://api.example.com",
		APIKey:         "pure-key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: buildTestRelayConfig("https://api.example.com", "pure-key"),
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).applyRelayInjection(true)

	if result["status"] != "ok" {
		t.Fatalf("pure API switch should succeed: %#v", result)
	}
	row := providerSyncThreadRow(t, filepath.Join(home, ".codex", "state_5.sqlite"), sessionID)
	if got := stringFromAny(row["model_provider"]); got != "CodexPlusPlus" {
		t.Fatalf("provider sync should update sqlite provider, got %q", got)
	}
	syncPayload, _ := result["providerSync"].(map[string]any)
	if int64FromFlexible(syncPayload["sqliteRowsUpdated"]) == 0 {
		t.Fatalf("provider sync payload should report sqlite updates: %#v", syncPayload)
	}
}

func TestOfficialSwitchRunsProviderSyncToOpenAI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubCodexPluginCommands(t, nil)
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, testSessionRolloutLine(sessionID, "/project", "official history")+"\n")
	createProviderSyncThreadsTable(t, filepath.Join(home, ".codex", "state_5.sqlite"), true)
	insertProviderSyncThread(t, filepath.Join(home, ".codex", "state_5.sqlite"), map[string]any{
		"id":                 sessionID,
		"rollout_path":       rolloutPath,
		"created_at":         1779962400,
		"updated_at":         1779962500,
		"source":             "vscode",
		"model_provider":     "CodexPlusPlus",
		"cwd":                "/project",
		"title":              "official history",
		"sandbox_policy":     `{"type":"danger-full-access"}`,
		"approval_mode":      "never",
		"tokens_used":        0,
		"has_user_event":     0,
		"archived":           0,
		"cli_version":        "",
		"first_user_message": "",
		"memory_mode":        "enabled",
		"created_at_ms":      1779962400000,
		"updated_at_ms":      1779962500000,
		"preview":            "",
	})
	officialAuth := fakeChatGPTAuthJSON(t, "official@example.com")
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:                   "official",
		Name:                 "Official",
		RelayMode:            "official",
		Protocol:             "responses",
		ConfigContents:       buildTestRelayConfig("https://api.example.com", "relay-key"),
		OfficialAuthContents: officialAuth,
		OfficialAccountLabel: "official@example.com",
	}}
	settings.ActiveRelayID = "official"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	result := (&server{}).clearRelayInjection()

	if result["status"] != "ok" {
		t.Fatalf("official switch should succeed: %#v", result)
	}
	config := readFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(config, `model_provider = "openai"`) {
		t.Fatalf("official switch should write openai provider:\n%s", config)
	}
	row := providerSyncThreadRow(t, filepath.Join(home, ".codex", "state_5.sqlite"), sessionID)
	if got := stringFromAny(row["model_provider"]); got != "openai" {
		t.Fatalf("provider sync should update sqlite provider to openai, got %q", got)
	}
}

func TestRepairCodexGoalsConfigEnablesGoalsFeature(t *testing.T) {
	contents := strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		"",
		"[features]",
		"remote_connections = true",
		"",
	}, "\n")

	updated := repairCodexGoalsConfig(contents)

	if !strings.Contains(updated, "[features]\nremote_connections = true\ngoals = true") {
		t.Fatalf("goals feature was not added to features table:\n%s", updated)
	}
	if strings.Count(updated, "goals = true") != 1 {
		t.Fatalf("goals feature should be written exactly once:\n%s", updated)
	}
}

func TestRepairCodexPluginConfigRestoresCachedPluginTables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, filepath.Join(home, ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)
	writeTestFile(t, filepath.Join(home, "plugins", "cache", "openai-curated", "github", "6188456f", ".codex-plugin", "plugin.json"), `{"name":"github"}`)
	writeTestFile(t, filepath.Join(home, "plugins", "cache", "openai-bundled", "browser", "26.519.41501", ".codex-plugin", "plugin.json"), `{"name":"browser"}`)

	updated, pluginCount, marketplaceCount, _ := repairCodexPluginConfig(home, `model_provider = "CodexPlusPlus"`+"\n")

	if pluginCount != 2 {
		t.Fatalf("plugin count mismatch: %d", pluginCount)
	}
	if marketplaceCount != 2 {
		t.Fatalf("marketplace count mismatch: %d", marketplaceCount)
	}
	for _, expected := range []string{
		`[marketplaces.openai-curated]`,
		`[marketplaces.openai-api-curated]`,
		`source = "` + filepath.Join(home, ".tmp", "plugins") + `"`,
		`[plugins."browser@openai-bundled"]`,
		`[plugins."github@openai-curated"]`,
		`enabled = true`,
	} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("updated config missing %q:\n%s", expected, updated)
		}
	}
}

func TestRepairCodexPluginConfigCorrectsMarketplaceSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, filepath.Join(home, ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)

	updated, _, marketplaceCount, _ := repairCodexPluginConfig(home, strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		``,
		`[marketplaces.openai-curated]`,
		`last_updated = "old"`,
		`source_type = "local"`,
		`source = "/stale/plugins"`,
		`custom_flag = "keep"`,
		``,
	}, "\n"))

	if marketplaceCount != 2 {
		t.Fatalf("marketplace count mismatch: %d", marketplaceCount)
	}
	if strings.Contains(updated, `/stale/plugins`) {
		t.Fatalf("stale marketplace source should be replaced:\n%s", updated)
	}
	if !strings.Contains(updated, `source = "`+filepath.Join(home, ".tmp", "plugins")+`"`) {
		t.Fatalf("marketplace source should point to discovered local marketplace:\n%s", updated)
	}
	if !strings.Contains(updated, `custom_flag = "keep"`) {
		t.Fatalf("marketplace repair should preserve unrelated table keys:\n%s", updated)
	}
}

func TestRefreshCodexMarketplacesRereadsLocalMarketplaceWithoutGitSource(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	writeTestFile(t, filepath.Join(home, ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)
	var commands []string
	stubCodexPluginCommands(t, nil, func(args []string) {
		commands = append(commands, strings.Join(args, " "))
	})

	result := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true, RefreshMarketplaces: true})

	if result.Status != "ok" {
		t.Fatalf("local marketplace refresh should not require git source: %#v", result)
	}
	expected := []string{"plugin marketplace upgrade", "plugin marketplace list", "plugin list"}
	if strings.Join(commands, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("refresh commands mismatch:\n got: %#v\nwant: %#v", commands, expected)
	}
	if result.MarketplaceRefreshStatus != "ok" {
		t.Fatalf("marketplace refresh should be ok: %#v", result)
	}
}

func TestCodexCLIExecutablePrefersLocalRuntimeAndSkipsWindowsApps(t *testing.T) {
	localAppData := t.TempDir()
	userProfile := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("USERPROFILE", userProfile)
	t.Setenv("CODEX_CLI_PATH", filepath.Join(t.TempDir(), "Program Files", "WindowsApps", "OpenAI.ChatGPT", "app", "resources", "codex.exe"))
	writeTestFile(t, os.Getenv("CODEX_CLI_PATH"), "blocked")
	runtimeCLI := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "fb2111b91430cb17", "codex.exe")
	writeTestFile(t, runtimeCLI, "runtime")
	olderCLI := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "0000000000000000", "codex.exe")
	writeTestFile(t, olderCLI, "older")

	originalGOOS := currentRuntimeGOOS
	currentRuntimeGOOS = func() string { return "windows" }
	t.Cleanup(func() {
		currentRuntimeGOOS = originalGOOS
	})

	if got := codexCLIExecutable(); got != runtimeCLI {
		t.Fatalf("codex CLI should prefer user-local runtime and skip WindowsApps alias:\n got: %q\nwant: %q", got, runtimeCLI)
	}
}

func TestCodexCLIExecutableReturnsEmptyWhenOnlyWindowsAppsAliasExists(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	windowsAppsDir := filepath.Join(t.TempDir(), "WindowsApps")
	t.Setenv("PATH", windowsAppsDir)
	t.Setenv("CODEX_CLI_PATH", filepath.Join(windowsAppsDir, "OpenAI.ChatGPT", "app", "resources", "codex.exe"))
	writeTestFile(t, os.Getenv("CODEX_CLI_PATH"), "blocked")
	writeTestFile(t, filepath.Join(windowsAppsDir, "codex.exe"), "blocked")

	originalGOOS := currentRuntimeGOOS
	currentRuntimeGOOS = func() string { return "windows" }
	t.Cleanup(func() {
		currentRuntimeGOOS = originalGOOS
	})

	if got := codexCLIExecutable(); got != "" {
		t.Fatalf("codex CLI should not fall back to an unusable WindowsApps alias, got %q", got)
	}
}

func TestIsWindowsAppsPathRecognizesPathSegments(t *testing.T) {
	for _, path := range []string{
		`C:\Program Files\WindowsApps\OpenAI.ChatGPT\codex.exe`,
		`C:\Users\A\AppData\Local\Microsoft\WindowsApps\codex.exe`,
		filepath.Join(t.TempDir(), "WindowsApps", "OpenAI.ChatGPT", "codex.exe"),
	} {
		if !isWindowsAppsPath(path) {
			t.Fatalf("path should be recognized as WindowsApps alias: %q", path)
		}
	}
	if isWindowsAppsPath(filepath.Join(t.TempDir(), "OpenAI", "Codex", "bin", "codex.exe")) {
		t.Fatal("runtime codex path should not be treated as WindowsApps alias")
	}
}

func TestRepairCodexConfigRefreshFailureKeepsRestoredPluginTables(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	writeTestFile(t, filepath.Join(home, ".tmp", "plugins", ".agents", "plugins", "marketplace.json"), `{"name":"openai-curated"}`)
	writeTestFile(t, filepath.Join(home, "plugins", "cache", "openai-curated", "github", "v1", ".codex-plugin", "plugin.json"), `{"name":"github"}`)
	stubCodexPluginCommands(t, map[string]error{
		"plugin marketplace upgrade": errors.New("boom"),
	})

	result := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true, RefreshMarketplaces: true})

	if result.Status != "failed" {
		t.Fatalf("refresh failure should fail repair result: %#v", result)
	}
	if !strings.Contains(result.MarketplaceRefreshError, "boom") {
		t.Fatalf("refresh error should mention command failure: %#v", result)
	}
	config := readFile(filepath.Join(home, "config.toml"))
	for _, expected := range []string{
		`[marketplaces.openai-curated]`,
		`[plugins."github@openai-curated"]`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config should keep restored plugin table %q after refresh failure:\n%s", expected, config)
		}
	}
}

func TestWriteCodexConfigWithBackupCreatesUniqueBackup(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := "model_provider = \"openai\"\n"
	writeTestFile(t, configPath, original)

	firstBackup, err := writeCodexConfigWithBackup(configPath, "model_provider = \"one\"\n", "unit")
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if firstBackup == nil {
		t.Fatal("first write should return a backup path")
	}
	firstData, _ := os.ReadFile(*firstBackup)
	if string(firstData) != original {
		t.Fatalf("first backup content mismatch:\n%s", string(firstData))
	}
	secondBackup, err := writeCodexConfigWithBackup(configPath, "model_provider = \"two\"\n", "unit")
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if secondBackup == nil || *secondBackup == *firstBackup {
		t.Fatalf("second write should create a unique backup: first=%v second=%v", firstBackup, secondBackup)
	}
	secondData, _ := os.ReadFile(*secondBackup)
	if string(secondData) != "model_provider = \"one\"\n" {
		t.Fatalf("second backup content mismatch:\n%s", string(secondData))
	}
}

func TestSaveRelayFileConfigReturnsBackupPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".codex", "config.toml")
	writeTestFile(t, configPath, "before = true\n")

	result := (&server{}).saveRelayFile(map[string]any{
		"request": map[string]any{"kind": "config", "contents": "after = true\n"},
	})

	if result["status"] != "ok" {
		t.Fatalf("save config should succeed: %#v", result)
	}
	backupPath := stringFromAny(result["backupPath"])
	if backupPath == "" {
		t.Fatalf("save config should return backupPath: %#v", result)
	}
	backup, _ := os.ReadFile(backupPath)
	if string(backup) != "before = true\n" {
		t.Fatalf("backup content mismatch:\n%s", string(backup))
	}
}

func TestRelaySwitchesReturnBackupPath(t *testing.T) {
	for _, pure := range []bool{false, true} {
		t.Run(fmt.Sprintf("pure=%v", pure), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), "before = true\n")
			settings := defaultSettings()
			settings.RelayProfiles = []relayProfile{{
				ID:        "relay",
				Name:      "Relay",
				BaseURL:   "https://api.example.com",
				APIKey:    "relay-key",
				RelayMode: "pureApi",
				Protocol:  "responses",
			}}
			settings.ActiveRelayID = "relay"
			if err := saveSettings(settings); err != nil {
				t.Fatalf("failed to save settings: %v", err)
			}
			var result commandResult
			if pure {
				result = (&server{}).applyRelayInjection(true)
			} else {
				result = (&server{}).applyRelayInjection(false)
			}
			if result["status"] != "ok" {
				t.Fatalf("relay switch should succeed: %#v", result)
			}
			backupPath := stringFromAny(result["backupPath"])
			if backupPath == "" {
				t.Fatalf("relay switch should return backupPath: %#v", result)
			}
			backup, _ := os.ReadFile(backupPath)
			if string(backup) != "before = true\n" {
				t.Fatalf("relay backup content mismatch:\n%s", string(backup))
			}
		})
	}
}

func TestRepairComputerUseBuildsWindowsCompatibilityTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE", "1")
	writeTestFile(t, filepath.Join(home, "config.toml"), "model_provider = \"openai\"\n")

	status, err := repairComputerUse(home, "windows", false)
	if err != nil {
		t.Fatalf("repair computer use failed: %v", err)
	}
	if !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport || !status.ConfigReady {
		t.Fatalf("computer use status incomplete: %#v", status)
	}
	if status.BackupPath == nil {
		t.Fatal("computer use repair should backup existing config")
	}
	for _, path := range []string{
		filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled"),
		filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", ".agents", "plugins", "marketplace.json"),
		filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", "plugins", "computer-use", ".codex-plugin", "plugin.json"),
		filepath.Join(home, "plugins", "cache", "openai-bundled", "computer-use", "latest", ".codex-plugin", "plugin.json"),
		filepath.Join(home, "plugins", "cache", "openai-bundled", "computer-use", "latest", "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js"),
	} {
		if !fileExists(path) {
			t.Fatalf("expected generated path: %s", path)
		}
	}
	config := readFile(filepath.Join(home, "config.toml"))
	for _, expected := range []string{
		"[marketplaces.openai-bundled]",
		`[plugins."computer-use@openai-bundled"]`,
		"[mcp_servers.node_repl]",
		`BROWSER_USE_MARKETPLACE_NAME = "openai-bundled"`,
		"CODEX_HOME = " + quoteToml(home),
		"[windows]",
		`sandbox = "unelevated"`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config missing %q:\n%s", expected, config)
		}
	}
}

func TestRepairComputerUsePreservesOfficialMarketplacePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE", "1")
	writeTestFile(t, filepath.Join(home, "config.toml"), "model_provider = \"openai\"\n")
	officialPlugin := filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", "plugins", "computer-use")
	writeTestFile(t, filepath.Join(officialPlugin, ".codex-plugin", "plugin.json"), `{"name":"computer-use","version":"26.527.31326"}`)
	writeTestFile(t, filepath.Join(officialPlugin, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js"), "official-helper")
	writeTestFile(t, filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", ".agents", "plugins", "marketplace.json"), `{"name":"openai-bundled","plugins":[{"name":"computer-use","source":{"source":"bundled","path":"./plugins/computer-use"}}]}`)

	status, err := repairComputerUse(home, "windows", false)
	if err != nil {
		t.Fatalf("repair computer use failed: %v", err)
	}
	if !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport || !status.ConfigReady {
		t.Fatalf("computer use files should be ready with official plugin: %#v", status)
	}
	if got := readFile(filepath.Join(officialPlugin, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js")); got != "official-helper" {
		t.Fatalf("official marketplace plugin should not be overwritten: %q", got)
	}
	config := readFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(config, `source = "`+filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled")+`"`) {
		t.Fatalf("config should point to local bundled marketplace:\n%s", config)
	}
}

func TestRepairComputerUsePreservesOfficialCachedPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE", "1")
	writeTestFile(t, filepath.Join(home, "config.toml"), "model_provider = \"openai\"\n")
	officialCache := filepath.Join(home, "plugins", "cache", "openai-bundled", "computer-use", "26.527.31326")
	writeTestFile(t, filepath.Join(officialCache, ".codex-plugin", "plugin.json"), `{"name":"computer-use","version":"26.527.31326"}`)
	writeTestFile(t, filepath.Join(officialCache, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js"), "official-cache-helper")

	status, err := repairComputerUse(home, "windows", false)
	if err != nil {
		t.Fatalf("repair computer use failed: %v", err)
	}
	if !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport || !status.ConfigReady {
		t.Fatalf("computer use files should be ready with official cache: %#v", status)
	}
	if got := readFile(filepath.Join(officialCache, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js")); got != "official-cache-helper" {
		t.Fatalf("official cached plugin should not be overwritten: %q", got)
	}
	if strings.Contains(readFile(filepath.Join(officialCache, ".codex-plugin", "plugin.json")), computerUsePluginVersion) {
		t.Fatalf("official cache manifest should not be replaced by local fallback")
	}
}

func TestRepairComputerUseMaterializesMissingMarketplaceFromCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE", "1")
	writeTestFile(t, filepath.Join(home, "config.toml"), "model_provider = \"openai\"\n")
	cacheLatest := filepath.Join(home, "plugins", "cache", "openai-bundled", "computer-use", "latest")
	writeTestFile(t, filepath.Join(cacheLatest, ".codex-plugin", "plugin.json"), `{"name":"computer-use","version":"26.527.31326"}`)
	writeTestFile(t, filepath.Join(cacheLatest, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js"), "official-cache-helper")

	status, err := repairComputerUse(home, "windows", false)
	if err != nil {
		t.Fatalf("repair computer use failed: %v", err)
	}
	if !status.MarketplaceManifest || !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport || !status.ConfigReady {
		t.Fatalf("computer use files should be ready when marketplace is restored from cache: %#v", status)
	}
	marketplacePlugin := filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", "plugins", "computer-use")
	if got := readFile(filepath.Join(marketplacePlugin, "node_modules", "@oai", "sky", "dist", "project", "cua", "sky_js", "src", "targets", "windows", "internal", "helper_transport.js")); got != "official-cache-helper" {
		t.Fatalf("marketplace plugin should be copied from official cache: %q", got)
	}
	manifest := readFile(filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", ".agents", "plugins", "marketplace.json"))
	for _, expected := range []string{`"name": "computer-use"`, `"path": "./plugins/computer-use"`} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("marketplace manifest missing %q:\n%s", expected, manifest)
		}
	}
	if strings.Contains(readFile(filepath.Join(marketplacePlugin, ".codex-plugin", "plugin.json")), computerUsePluginVersion) {
		t.Fatalf("marketplace plugin should not be replaced by local fallback")
	}
}

func TestRepairComputerUseReturnsFailureWhenFinalStatusIncomplete(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), "model_provider = \"openai\"\n")

	status, err := repairComputerUse(home, "windows", false)
	if err == nil {
		t.Fatal("repair computer use should fail when environment variable was not enabled")
	}
	if !strings.Contains(err.Error(), "CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE") {
		t.Fatalf("failure should include missing env detail: %v", err)
	}
	if !status.MarketplaceManifest || !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport || !status.ConfigReady {
		t.Fatalf("repair should still report completed file state: %#v", status)
	}
}

func TestRepairComputerUseFailureReloadsPartialStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_ELECTRON_ENABLE_WINDOWS_COMPUTER_USE", "1")
	configPath := filepath.Join(home, "config.toml")
	if err := os.MkdirAll(configPath, 0o755); err != nil {
		t.Fatalf("create config directory failed: %v", err)
	}

	status, err := repairComputerUse(home, "windows", false)
	if err == nil {
		t.Fatal("repair computer use should fail when config.toml is a directory")
	}
	if !status.MarketplaceReady || !status.MarketplaceManifest || !status.MarketplacePlugin || !status.CacheLatest || !status.HelperTransport {
		t.Fatalf("partial status should be reloaded after failure: %#v", status)
	}
}

func TestCodexHomeDirHonorsCODEXHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if got := codexHomeDir(); got != filepath.Clean(home) {
		t.Fatalf("codexHomeDir should honor CODEX_HOME: got %q want %q", got, filepath.Clean(home))
	}
}

func TestAtomicWriteUsesReplaceFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows replaceFile behavior is covered by cross-compiled source check")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatalf("seed file failed: %v", err)
	}
	if err := atomicWrite(path, []byte("after")); err != nil {
		t.Fatalf("atomic write should replace existing file on windows: %v", err)
	}
	if got := readFile(path); got != "after" {
		t.Fatalf("atomic write content mismatch: %q", got)
	}
}

func TestWindowsAtomicWriteUsesReplaceExistingRename(t *testing.T) {
	data, err := os.ReadFile("atomic_rename_windows.go")
	if err != nil {
		t.Fatalf("read atomic_rename_windows.go failed: %v", err)
	}
	matched, err := regexp.Match(`windows\.Rename\(\s*source,\s*target\s*\)`, data)
	if err != nil {
		t.Fatalf("regexp failed: %v", err)
	}
	if !matched {
		t.Fatalf("windows replaceFile must use windows.Rename so existing targets are replaced:\n%s", string(data))
	}
}

func TestSkillMCPBackupRestoreOnlyReplacesManagedState(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "skills", "alpha", "SKILL.md"), "alpha")
	writeTestFile(t, filepath.Join(home, "plugins", "cache", "openai-bundled", "browser", "v1", ".codex-plugin", "plugin.json"), "{}")
	writeTestFile(t, filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", ".agents", "plugins", "marketplace.json"), "{}")
	writeTestFile(t, filepath.Join(home, "config.toml"), strings.Join([]string{
		`model_provider = "openai"`,
		`OPENAI_API_KEY = "keep"`,
		``,
		`[model_providers.openai]`,
		`name = "OpenAI"`,
		``,
		`[mcp_servers.old]`,
		`command = "old"`,
		``,
		`[plugins."old@market"]`,
		`enabled = true`,
		``,
		`[features]`,
		`goals = true`,
		``,
	}, "\n"))
	backup, err := createSkillMCPBackup(home, "first")
	if err != nil {
		t.Fatalf("create backup failed: %v", err)
	}
	if !backup.HasSkills || !backup.HasPluginCache || !backup.HasBundledMarket || !backup.HasConfigSnapshot {
		t.Fatalf("backup missing expected parts: %#v", backup)
	}

	_ = os.RemoveAll(filepath.Join(home, "skills"))
	writeTestFile(t, filepath.Join(home, "skills", "beta", "SKILL.md"), "beta")
	_ = os.RemoveAll(filepath.Join(home, "plugins", "cache"))
	writeTestFile(t, filepath.Join(home, "plugins", "cache", "other", "plugin", ".codex-plugin", "plugin.json"), "{}")
	writeTestFile(t, filepath.Join(home, "config.toml"), strings.Join([]string{
		`model_provider = "custom"`,
		`OPENAI_API_KEY = "preserve"`,
		``,
		`[model_providers.custom]`,
		`name = "Custom"`,
		``,
		`[mcp_servers.new]`,
		`command = "new"`,
		``,
		`[plugins."new@market"]`,
		`enabled = true`,
		``,
		`[windows]`,
		`sandbox = "danger-full-access"`,
		``,
	}, "\n"))

	current, restored, err := restoreSkillMCPBackup(home, backup.ID)
	if err != nil {
		t.Fatalf("restore backup failed: %v", err)
	}
	if current.ID == "" || restored.RestoreSourceBackup != current.ID {
		t.Fatalf("restore should create current backup: current=%#v restored=%#v", current, restored)
	}
	if !fileExists(filepath.Join(home, "skills", "alpha", "SKILL.md")) || fileExists(filepath.Join(home, "skills", "beta", "SKILL.md")) {
		t.Fatalf("skills directory was not restored")
	}
	config := readFile(filepath.Join(home, "config.toml"))
	for _, expected := range []string{
		`model_provider = "custom"`,
		`OPENAI_API_KEY = "preserve"`,
		`[model_providers.custom]`,
		`[mcp_servers.old]`,
		`[plugins."old@market"]`,
		`[features]`,
		`goals = true`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("restored config missing %q:\n%s", expected, config)
		}
	}
	for _, unexpected := range []string{
		`[mcp_servers.new]`,
		`[plugins."new@market"]`,
		`sandbox = "danger-full-access"`,
		`[model_providers.openai]`,
	} {
		if strings.Contains(config, unexpected) {
			t.Fatalf("restored config should not contain %q:\n%s", unexpected, config)
		}
	}
}

func TestSkillMCPBackupRejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	if _, err := resolveSkillMCPBackupDir(home, "../escape"); err == nil {
		t.Fatal("resolve should reject traversal id")
	}
	if err := deleteSkillMCPBackup(home, "../escape"); err == nil {
		t.Fatal("delete should reject traversal id")
	}
	if _, _, err := restoreSkillMCPBackup(home, "../escape"); err == nil {
		t.Fatal("restore should reject traversal id")
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
}

func stubCodexPluginCommands(t *testing.T, failures map[string]error, observers ...func([]string)) {
	t.Helper()
	original := runCodexPluginCommand
	runCodexPluginCommand = func(home string, args ...string) codexCommandOutput {
		for _, observer := range observers {
			observer(append([]string{}, args...))
		}
		key := strings.Join(args, " ")
		if err := failures[key]; err != nil {
			return codexCommandOutput{Command: "codex " + key, Output: "failed " + key, Err: err}
		}
		return codexCommandOutput{Command: "codex " + key, Output: "ok " + key}
	}
	t.Cleanup(func() {
		runCodexPluginCommand = original
	})
}

func createProviderSyncThreadsTable(t *testing.T, dbPath string, includeThreadSource bool) {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	threadSourceColumn := ""
	if includeThreadSource {
		threadSourceColumn = "thread_source TEXT,"
	}
	if _, err := db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		source TEXT NOT NULL,
		model_provider TEXT NOT NULL,
		cwd TEXT NOT NULL,
		title TEXT NOT NULL,
		sandbox_policy TEXT NOT NULL,
		approval_mode TEXT NOT NULL,
		tokens_used INTEGER NOT NULL DEFAULT 0,
		has_user_event INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		archived_at INTEGER,
		git_sha TEXT,
		git_branch TEXT,
		git_origin_url TEXT,
		cli_version TEXT NOT NULL DEFAULT '',
		first_user_message TEXT NOT NULL DEFAULT '',
		agent_nickname TEXT,
		agent_role TEXT,
		memory_mode TEXT NOT NULL DEFAULT 'enabled',
		model TEXT,
		reasoning_effort TEXT,
		agent_path TEXT,
		created_at_ms INTEGER,
		updated_at_ms INTEGER,
		` + threadSourceColumn + `
		preview TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("failed to create provider sync threads table: %v", err)
	}
}

func insertProviderSyncThread(t *testing.T, dbPath string, values map[string]any) {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil {
		t.Fatalf("failed to inspect test sqlite db: %v", err)
	}
	var insertColumns []string
	var args []any
	for _, column := range columns {
		if value, ok := values[column]; ok {
			insertColumns = append(insertColumns, column)
			args = append(args, value)
		}
	}
	quoted := make([]string, len(insertColumns))
	for index, column := range insertColumns {
		quoted[index] = quoteSQLiteIdentifier(column)
	}
	if _, err := db.Exec("INSERT INTO threads ("+strings.Join(quoted, ", ")+") VALUES ("+sqlitePlaceholders(len(insertColumns))+")", args...); err != nil {
		t.Fatalf("failed to insert provider sync thread: %v", err)
	}
}

func providerSyncThreadRow(t *testing.T, dbPath, sessionID string) map[string]any {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	rows, err := querySessionSQLiteRows(db, "SELECT * FROM threads WHERE id = ?", sessionID)
	if err != nil {
		t.Fatalf("failed to query thread row: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one thread row, got %d", len(rows))
	}
	return rows[0].Values
}

func buildTestRelayConfig(baseURL, apiKey string) string {
	return strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		``,
		`[model_providers.CodexPlusPlus]`,
		`name = "CodexPlusPlus"`,
		`wire_api = "responses"`,
		`requires_openai_auth = true`,
		`base_url = "` + baseURL + `"`,
		`experimental_bearer_token = "` + apiKey + `"`,
		``,
	}, "\n")
}

func fakeChatGPTAuthJSON(t *testing.T, email string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		t.Fatalf("failed to marshal token payload: %v", err)
	}
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	data, err := json.MarshalIndent(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"id_token":      token,
			"refresh_token": "refresh-token",
		},
	}, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal auth json: %v", err)
	}
	return string(data) + "\n"
}

func TestNormalizeCodexAppPathAcceptsWindowsExecutableAndAppDir(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "OpenAI.ChatGPT_1.2.3.0_x64__test", "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("failed to create app dir: %v", err)
	}
	exe := filepath.Join(appDir, "ChatGPT.exe")
	writeTestFile(t, exe, "binary")

	if got := normalizeCodexAppPath(exe); got != appDir {
		t.Fatalf("executable should normalize to app dir: %q", got)
	}
	if got := normalizeCodexAppPath(filepath.Dir(appDir)); got != appDir {
		t.Fatalf("package root should normalize to nested app dir: %q", got)
	}
	if got := normalizeCodexAppPath(appDir); got != appDir {
		t.Fatalf("app dir should stay app dir: %q", got)
	}
}

func TestPackagedWindowsAppUserModelIDMatchesOriginalLauncherShape(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	if got := windowsAppUserModelIDFromPackagePath(path); got != "OpenAI.ChatGPT_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", got)
	}
}

func TestPackagedWindowsAppUserModelIDSupportsCodexBetaPackage(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.CodexBeta_26.619.2200.0_x64__2p2nqsd0c76g0\app`

	if got := windowsAppUserModelIDFromPackagePath(path); got != "OpenAI.CodexBeta_2p2nqsd0c76g0!App" {
		t.Fatalf("beta app user model id mismatch: %q", got)
	}
	if got := windowsPackageVersionFromPath(path); got != "26.619.2200.0" {
		t.Fatalf("beta package version mismatch: %q", got)
	}
}

func TestWindowsIntegratedChatGPTPackageIdentityCompatibility(t *testing.T) {
	cases := []struct {
		name     string
		identity string
	}{
		{name: "new integrated app", identity: "OpenAI.Codex"},
		{name: "new integrated beta", identity: "OpenAI.CodexBeta"},
		{name: "legacy ChatGPT", identity: "OpenAI.ChatGPT"},
		{name: "legacy ChatGPT beta", identity: "OpenAI.ChatGPTBeta"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			packageName := test.identity + `_26.623.19656.0_x64__2p2nqsd0c76g0`
			identity, version, publisher, ok := windowsCodexPackageParts(packageName)
			if !ok {
				t.Fatalf("supported package should parse: %q", packageName)
			}
			if identity != test.identity || version != "26.623.19656.0" || publisher != "2p2nqsd0c76g0" {
				t.Fatalf("unexpected package parts: identity=%q version=%q publisher=%q", identity, version, publisher)
			}
		})
	}
	for _, unsupported := range []string{
		`OpenAI.CodexTools_26.623.19656.0_x64__2p2nqsd0c76g0`,
		`OpenAI.ChatGPTUnexpected_26.623.19656.0_x64__2p2nqsd0c76g0`,
	} {
		if _, _, _, ok := windowsCodexPackageParts(unsupported); ok {
			t.Fatalf("lookalike package identity must be rejected: %q", unsupported)
		}
	}
}

func TestWindowsIntegratedChatGPTManifestProvidesRealAUMIDAndExecutable(t *testing.T) {
	packageRoot := filepath.Join(t.TempDir(), "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0")
	manifest := `<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10">
  <Identity Name="OpenAI.Codex" />
  <Applications>
    <Application Id="Updater" Executable="updater.exe" />
    <Application Id="ChatGPTDesktop" Executable="app\Codex.exe" EntryPoint="Windows.FullTrustApplication" />
  </Applications>
</Package>`
	writeTestFile(t, filepath.Join(packageRoot, "AppxManifest.xml"), manifest)
	writeTestFile(t, filepath.Join(packageRoot, "app", "Codex.exe"), "binary")

	metadata, ok := readWindowsPackageManifestMetadata(packageRoot)
	if !ok {
		t.Fatal("integrated app manifest should parse")
	}
	if metadata.ApplicationID != "ChatGPTDesktop" || metadata.Executable != "app/Codex.exe" {
		t.Fatalf("unexpected manifest target: %#v", metadata)
	}
	appDir := filepath.Join(packageRoot, "app")
	if got := windowsAppUserModelIDFromPackagePath(appDir); got != "OpenAI.Codex_2p2nqsd0c76g0!ChatGPTDesktop" {
		t.Fatalf("AUMID must use the manifest Application Id, got %q", got)
	}
	if got := normalizeWindowsPackageAppPath(packageRoot); got != appDir {
		t.Fatalf("manifest executable directory mismatch: got %q want %q", got, appDir)
	}
}

func TestWindowsIntegratedChatGPTManifestParsesExecutionAliasAndVFSExecutable(t *testing.T) {
	packageRoot := filepath.Join(t.TempDir(), "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0")
	manifest := `<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10" xmlns:uap3="http://schemas.microsoft.com/appx/manifest/uap/windows10/3" xmlns:desktop="http://schemas.microsoft.com/appx/manifest/desktop/windows10">
  <Identity Name="OpenAI.Codex" />
  <Applications>
    <Application Id="App">
      <Extensions>
        <uap3:Extension Category="windows.appExecutionAlias" Executable="VFS\ProgramFilesX64\OpenAI\Codex\Codex.exe" EntryPoint="Windows.FullTrustApplication">
          <uap3:AppExecutionAlias><desktop:ExecutionAlias Alias="Codex.exe" /></uap3:AppExecutionAlias>
        </uap3:Extension>
      </Extensions>
    </Application>
  </Applications>
</Package>`
	writeTestFile(t, filepath.Join(packageRoot, "AppxManifest.xml"), manifest)
	executable := filepath.Join(packageRoot, "VFS", "ProgramFilesX64", "OpenAI", "Codex", "Codex.exe")
	writeTestFile(t, executable, "binary")

	metadata, ok := readWindowsPackageManifestMetadata(packageRoot)
	if !ok {
		t.Fatal("manifest execution alias should identify the unified desktop app")
	}
	if metadata.ApplicationID != "App" || metadata.Executable != "VFS/ProgramFilesX64/OpenAI/Codex/Codex.exe" || metadata.EntryPoint != "Windows.FullTrustApplication" {
		t.Fatalf("unexpected VFS manifest metadata: %#v", metadata)
	}
	if len(metadata.ExecutionAliases) != 1 || metadata.ExecutionAliases[0] != "Codex.exe" {
		t.Fatalf("execution alias mismatch: %#v", metadata.ExecutionAliases)
	}
	if got := normalizeWindowsPackageAppPath(packageRoot); got != filepath.Dir(executable) {
		t.Fatalf("VFS package normalization mismatch: got %q want %q", got, filepath.Dir(executable))
	}
	if got := windowsAppUserModelIDFromPackagePath(executable); got != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("deep VFS executable should retain the package AUMID: %q", got)
	}
}

func TestWindowsPackageRootRecoveryScansDeepVFSPath(t *testing.T) {
	packageRoot := `C:\Program Files\WindowsApps\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0`
	executable := packageRoot + `\VFS\ProgramFilesX64\OpenAI\Codex\Codex.exe`
	if got := windowsPackageNameFromPath(executable); got != `OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0` {
		t.Fatalf("deep VFS package name mismatch: %q", got)
	}
	if got := windowsPackageRootFromPath(executable); got != packageRoot {
		t.Fatalf("deep VFS package root mismatch: got %q want %q", got, packageRoot)
	}
}

func TestWindowsManifestRejectsAmbiguousDesktopApplications(t *testing.T) {
	packageRoot := filepath.Join(t.TempDir(), "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0")
	manifest := `<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10">
  <Identity Name="OpenAI.Codex" />
  <Applications>
    <Application Id="App" Executable="app\Codex.exe" EntryPoint="Windows.FullTrustApplication" />
    <Application Id="Second" Executable="second\ChatGPT.exe" EntryPoint="Windows.FullTrustApplication" />
  </Applications>
</Package>`
	writeTestFile(t, filepath.Join(packageRoot, "AppxManifest.xml"), manifest)
	if metadata, ok := readWindowsPackageManifestMetadata(packageRoot); ok {
		t.Fatalf("ambiguous desktop applications must not be guessed: %#v", metadata)
	}
}

func TestNormalizeWindowsRegisteredPackageLaunchInfoRequiresOfficialExactPackage(t *testing.T) {
	installLocation := filepath.Join(t.TempDir(), "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0")
	raw := windowsRegisteredPackageLaunchInfo{
		AppUserModelID:    "OpenAI.Codex_2p2nqsd0c76g0!App",
		PackageFamilyName: "OpenAI.Codex_2p2nqsd0c76g0",
		PackageFullName:   "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0",
		InstallLocation:   installLocation,
		ApplicationID:     "App",
		Executable:        `app\Codex.exe`,
		EntryPoint:        "Windows.FullTrustApplication",
		ExecutionAliases:  []string{"Codex.exe", "unrelated.exe", "codex.exe"},
	}
	info, ok := normalizeWindowsRegisteredPackageLaunchInfo(raw, raw.AppUserModelID)
	if !ok {
		t.Fatal("official registered package launch info should validate")
	}
	if info.ExecutablePath != filepath.Join(installLocation, "app", "Codex.exe") {
		t.Fatalf("registered executable path mismatch: %q", info.ExecutablePath)
	}
	if len(info.ExecutionAliases) != 1 || info.ExecutionAliases[0] != "Codex.exe" {
		t.Fatalf("registered aliases must be filtered and deduplicated: %#v", info.ExecutionAliases)
	}
	raw.PackageFamilyName = "OpenAI.ChatGPT_2p2nqsd0c76g0"
	if _, ok := normalizeWindowsRegisteredPackageLaunchInfo(raw, raw.AppUserModelID); ok {
		t.Fatal("mismatched package family must be rejected")
	}
	raw.PackageFamilyName = "OpenAI.Codex_2p2nqsd0c76g0"
	raw.Executable = `..\evil\Codex.exe`
	if _, ok := normalizeWindowsRegisteredPackageLaunchInfo(raw, raw.AppUserModelID); ok {
		t.Fatal("package-relative traversal executable must be rejected")
	}
}

func TestWindowsIntegratedChatGPTProtectedPathsCoverBothPackageIdentities(t *testing.T) {
	for _, path := range []string{
		`C:\Program Files\WindowsApps\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0\app\Codex.exe`,
		`C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`,
		`C:\Program Files\WindowsApps\.\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0\app\Codex.exe`,
		`C:\Program Files\WindowsApps.\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0\app\Codex.exe`,
	} {
		if !isWindowsProtectedAppPackagePath(path) {
			t.Fatalf("known Store package must remain protected from direct execution: %q", path)
		}
	}
	if isWindowsProtectedAppPackagePath(`C:\Program Files\WindowsApps\Unrelated.App_1.0.0.0_x64__publisher\app.exe`) {
		t.Fatal("unrelated WindowsApps package must not be classified as an OpenAI desktop target")
	}
}

func TestWindowsIntegratedChatGPTDiagnosticPathNormalizesWithoutFileAccess(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0\app`
	if got := normalizeWindowsPackageAppPath(path); got != path {
		t.Fatalf("the integrated app path from the Windows diagnostic should normalize without reading WindowsApps: got %q want %q", got, path)
	}
	if got := windowsAppUserModelIDFromPackagePath(path); got != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("fallback AUMID mismatch for the diagnostic package shape: %q", got)
	}
}

func TestWindowsIntegratedChatGPTStableAUMIDReference(t *testing.T) {
	reference := `aumid:openai.codex_2p2nqsd0c76g0!ChatGPTDesktop`
	wantReference := `aumid:OpenAI.Codex_2p2nqsd0c76g0!ChatGPTDesktop`
	if got := normalizeWindowsPackagedAppReference(reference); got != wantReference {
		t.Fatalf("stable AUMID reference mismatch: got %q want %q", got, wantReference)
	}
	if got := strings.TrimPrefix(normalizeWindowsPackagedAppReference(wantReference), "aumid:"); got != `OpenAI.Codex_2p2nqsd0c76g0!ChatGPTDesktop` {
		t.Fatalf("AUMID extraction mismatch: %q", got)
	}
	for _, invalid := range []string{
		`aumid:OpenAI.CodexTools_2p2nqsd0c76g0!App`,
		`aumid:OpenAI.Codex_2p2nqsd0c76g0!..\evil`,
		`OpenAI.Codex_2p2nqsd0c76g0!App`,
	} {
		if got := normalizeWindowsPackagedAppReference(invalid); got != "" {
			t.Fatalf("invalid AUMID reference should be rejected: %q -> %q", invalid, got)
		}
	}
}

func TestWindowsStableAUMIDBuildsActivationWithoutGuessingPackagePath(t *testing.T) {
	activation := newWindowsPackagedActivation(`OpenAI.Codex_2p2nqsd0c76g0!ChatGPTDesktop`, 9229, nil, backendSettings{})
	if activation == nil || activation.appUserModelID != `OpenAI.Codex_2p2nqsd0c76g0!ChatGPTDesktop` {
		t.Fatalf("stable AUMID should build an activation target: %#v", activation)
	}
	if activation.packageFamilyName != `OpenAI.Codex_2p2nqsd0c76g0` {
		t.Fatalf("package family mismatch: %q", activation.packageFamilyName)
	}
}

func TestWindowsIntegratedChatGPTExecutableNamesKeepLegacyCompatibility(t *testing.T) {
	for _, name := range []string{"ChatGPT.exe", "chatgpt.exe", "Codex.exe", "codex.exe"} {
		if !isWindowsTargetAppExecutableName(name) {
			t.Fatalf("integrated Windows target should accept %q", name)
		}
	}
	if isWindowsTargetAppExecutableName("CodexTools.exe") {
		t.Fatal("manager executable must not be accepted as the target app")
	}
	for _, family := range []string{"OpenAI.Codex_2p2nqsd0c76g0", "OpenAI.ChatGPTBeta_2p2nqsd0c76g0"} {
		if !isOpenAIDesktopPackageFamily(family) {
			t.Fatalf("official desktop package family should be accepted: %q", family)
		}
	}
	for _, family := range []string{"", "OpenAI.Codex_testpublisher", "OpenAI.CodexTools_2p2nqsd0c76g0"} {
		if isOpenAIDesktopPackageFamily(family) {
			t.Fatalf("unofficial desktop package family should be rejected: %q", family)
		}
	}
}

func TestDiagnosticSettingsValueRedactsCredentialsAndAccountLabels(t *testing.T) {
	settings := backendSettings{
		Language:                  "zh-CN",
		RelayAPIKey:               "relay-secret-value",
		MobileControlKey:          "mobile-secret-value",
		RelayCommonConfigContents: `experimental_bearer_token = "config-secret-value"`,
		RelayProfiles: []relayProfile{{
			Name:                 "provider-name-kept",
			APIKey:               "profile-secret-value",
			OfficialAuthContents: `{"access_token":"oauth-secret-value"}`,
			OfficialAccountLabel: "private-account@example.invalid",
			ConfigContents:       `api_key = "nested-config-secret-value"`,
		}},
	}

	data, err := json.Marshal(diagnosticSettingsValue(settings))
	if err != nil {
		t.Fatalf("marshal redacted diagnostics failed: %v", err)
	}
	report := string(data)
	for _, secret := range []string{
		"relay-secret-value",
		"mobile-secret-value",
		"config-secret-value",
		"profile-secret-value",
		"oauth-secret-value",
		"private-account@example.invalid",
		"nested-config-secret-value",
	} {
		if strings.Contains(report, secret) {
			t.Fatalf("diagnostic settings leaked %q: %s", secret, report)
		}
	}
	for _, expected := range []string{"zh-CN", `"relayProfileCount":1`, `"apiKeyConfigured":true`, `"officialAuthConfigured":true`, `"configConfigured":true`} {
		if !strings.Contains(report, expected) {
			t.Fatalf("diagnostic settings should retain safe context %q: %s", expected, report)
		}
	}
}

func TestDiagnosticLaunchStatusUsesSafeAllowlist(t *testing.T) {
	appPath := filepath.Join(string(filepath.Separator), "Users", "private-user", "ChatGPT.app")
	debugPort := uint16(9229)
	status := launchStatus{
		Status:    "failed",
		Message:   "启动失败 Bearer private-access-token",
		DebugPort: &debugPort,
		CodexApp:  &appPath,
		Detail: map[string]any{
			"activation_method":  "packaged_activation",
			"appUserModelId":     "OpenAI.Codex_2p2nqsd0c76g0!App",
			"cdp_port_available": false,
			"error":              "Incorrect function private-error-detail",
			"apiKey":             "private-api-key",
			"executionAlias":     filepath.Join("private-user", "WindowsApps", "Codex.exe"),
			"windows_launch_attempts": []any{
				map[string]any{"method": "app_execution_alias", "outcome": "debug_port_unavailable", "error": "private-attempt-error"},
				map[string]any{"method": "application_activation_manager", "outcome": "activation_incorrect_function"},
			},
		},
	}
	data, err := json.Marshal(diagnosticLaunchStatusValue(status))
	if err != nil {
		t.Fatalf("marshal diagnostic launch status failed: %v", err)
	}
	report := string(data)
	for _, secret := range []string{"private-user", "private-access-token", "private-error-detail", "private-api-key", "private-attempt-error"} {
		if strings.Contains(report, secret) {
			t.Fatalf("diagnostic launch status leaked %q: %s", secret, report)
		}
	}
	for _, expected := range []string{"application_activation_incorrect_function", "OpenAI.Codex_2p2nqsd0c76g0!App", "packaged_activation", "ChatGPT.app", "app_execution_alias", "debug_port_unavailable"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("diagnostic launch status should retain %q: %s", expected, report)
		}
	}
}

func TestWindowsPackagedAttemptOutcomeIsStructured(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{err: errors.New("Incorrect function."), want: "activation_incorrect_function"},
		{err: errors.New("未检测到调试端口 9229"), want: "debug_port_unavailable"},
		{err: errors.New("无法启动 ChatGPT 可执行文件"), want: "process_start_failed"},
		{err: errors.New("access denied"), want: "activation_failed"},
	}
	for _, test := range cases {
		if got := windowsPackagedAttemptOutcome(test.err); got != test.want {
			t.Fatalf("attempt outcome mismatch for %q: got %q want %q", test.err, got, test.want)
		}
	}
}

func TestPackagedWindowsAppUserModelIDIsCaseInsensitive(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app`
	mistypedCase := strings.Replace(path, "OpenAI.ChatGPT_", "OpenAl.Codex_", 1)

	if got := windowsAppUserModelIDFromPackagePath(strings.ToLower(path)); got != "OpenAI.ChatGPT_2p2nqsd0c76g0!App" {
		t.Fatalf("lowercase package path should still resolve app id: %q", got)
	}
	if got := windowsAppUserModelIDFromPackagePath(mistypedCase); got != "" {
		t.Fatalf("non Codex package identity should not resolve app id: %q", got)
	}
}

func TestWindowsPackagePathRejectsUnofficialPublisher(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_0.0.0.1_x64__testpublisher\app\ChatGPT.exe`
	if got := windowsAppUserModelIDFromPackagePath(path); got != "" {
		t.Fatalf("unofficial package publisher must not produce an AUMID: %q", got)
	}
}

func TestWindowsOfficialPackageShapeProducesStableAUMIDCandidate(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0`
	want := `OpenAI.Codex_2p2nqsd0c76g0!App`
	if got := windowsAppUserModelIDFromPackagePath(path); got != want {
		t.Fatalf("official package shape should produce a stable AUMID candidate: got %q want %q", got, want)
	}
}

func TestMissingWindowsExecutionAliasDoesNotNormalize(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("execution alias guard only applies on Windows")
	}
	path := filepath.Join(t.TempDir(), "Microsoft", "WindowsApps", "ChatGPT.exe")

	if got := normalizeCodexAppPath(path); got != "" {
		t.Fatalf("missing Windows execution alias should not normalize: %q", got)
	}
}

func TestWindowsPlainDirectoryWithoutCodexExecutableDoesNotNormalize(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows directory normalization only applies on Windows")
	}
	dir := filepath.Join(t.TempDir(), "Codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create plain directory: %v", err)
	}

	if got := normalizeCodexAppPath(dir); got != "" {
		t.Fatalf("plain directory without ChatGPT.exe should not normalize: %q", got)
	}
}

func TestBuildCodexExecutableRejectsWindowsPlainDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable lookup only applies on Windows")
	}
	dir := filepath.Join(t.TempDir(), "Codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create plain directory: %v", err)
	}

	if got := buildCodexExecutable(dir); got != "" {
		t.Fatalf("plain directory should not be treated as executable: %q", got)
	}
}

func TestBuildWindowsPackagedActivationArguments(t *testing.T) {
	activation := newWindowsPackagedActivation("OpenAI.ChatGPT_2p2nqsd0c76g0!App", 9229, []string{"--force_high_performance_gpu"}, backendSettings{})

	if activation == nil {
		t.Fatal("activation should be built")
	}
	if activation.appUserModelID != "OpenAI.ChatGPT_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", activation.appUserModelID)
	}
	if activation.arguments != "--remote-debugging-port=9229 --remote-allow-origins=http://127.0.0.1:9229 --force_high_performance_gpu" {
		t.Fatalf("activation arguments mismatch: %q", activation.arguments)
	}
}

func TestProviderSyncRestoresThreadSourceForLegacyRows(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, strings.Join([]string{
		testSessionRolloutLine(sessionID, "/project", "legacy title"),
		testRolloutResponseMessage("user", "修复历史对话"),
	}, "\n")+"\n")
	createProviderSyncThreadsTable(t, filepath.Join(home, "state_5.sqlite"), true)
	insertProviderSyncThread(t, filepath.Join(home, "state_5.sqlite"), map[string]any{
		"id":                 sessionID,
		"rollout_path":       rolloutPath,
		"created_at":         1779962400,
		"updated_at":         1779962500,
		"source":             "vscode",
		"model_provider":     "openai",
		"cwd":                "/project",
		"title":              "legacy title",
		"sandbox_policy":     `{"type":"danger-full-access"}`,
		"approval_mode":      "never",
		"tokens_used":        0,
		"has_user_event":     0,
		"archived":           0,
		"cli_version":        "",
		"first_user_message": "",
		"memory_mode":        "enabled",
		"created_at_ms":      1779962400000,
		"updated_at_ms":      1779962500000,
		"preview":            "",
	})

	result := runProviderSync(home)

	if result.Status != "synced" {
		t.Fatalf("sync should succeed: %#v", result)
	}
	if result.SQLiteRowsUpdated < 3 {
		t.Fatalf("sync should update provider, user flag, and thread source: %#v", result)
	}
	row := providerSyncThreadRow(t, filepath.Join(home, "state_5.sqlite"), sessionID)
	if got := stringFromAny(row["model_provider"]); got != "CodexPlusPlus" {
		t.Fatalf("provider mismatch: %q", got)
	}
	if got := stringFromAny(row["thread_source"]); got != "user" {
		t.Fatalf("thread_source should be restored, got %q", got)
	}
	if got := int64FromFlexible(row["has_user_event"]); got != 1 {
		t.Fatalf("has_user_event should be restored, got %#v", row["has_user_event"])
	}
}

func TestProviderSyncRemovesStaleLockAndContinues(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	lockDir := filepath.Join(home, "tmp", "provider-sync.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("failed to create stale lock: %v", err)
	}
	writeTestFile(t, filepath.Join(lockDir, "owner.json"), fmt.Sprintf(`{"pid":99999999,"startedAt":%d}`, time.Now().Add(-providerSyncLockTTL-time.Minute).Unix()))

	result := runProviderSync(home)

	if result.Status != "synced" {
		t.Fatalf("sync should continue after stale lock removal: %#v", result)
	}
	if isDir(lockDir) {
		t.Fatalf("stale lock should be removed after sync: %s", lockDir)
	}
}

func TestProviderSyncKeepsActiveLockSkipped(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	lockDir := filepath.Join(home, "tmp", "provider-sync.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("failed to create active lock: %v", err)
	}
	writeTestFile(t, filepath.Join(lockDir, "owner.json"), fmt.Sprintf(`{"pid":%d,"startedAt":%d}`, os.Getpid(), time.Now().Unix()))
	defer os.RemoveAll(lockDir)

	result := runProviderSync(home)

	if result.Status != "skipped" || !strings.Contains(result.Message, "Provider sync lock exists") {
		t.Fatalf("active lock should skip sync: %#v", result)
	}
	if !isDir(lockDir) {
		t.Fatalf("active lock should remain: %s", lockDir)
	}
}

func TestProviderSyncRestoresMissingThreadRowsFromRollouts(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n")
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, strings.Join([]string{
		testSessionRolloutLine(sessionID, "/Users/test/project", ""),
		testRolloutResponseMessage("user", "# Context from my IDE setup:\n\n## My request for Codex:\n恢复这个历史项目会话"),
	}, "\n")+"\n")
	createProviderSyncThreadsTable(t, filepath.Join(home, "state_5.sqlite"), true)
	writeTestFile(t, filepath.Join(home, ".codex-global-state.json"), `{"electron-saved-workspace-roots":["/existing"],"project-order":["/existing"]}`+"\n")

	result := runProviderSync(home)

	if result.Status != "synced" {
		t.Fatalf("sync should succeed: %#v", result)
	}
	if result.SQLiteRowsUpdated == 0 {
		t.Fatalf("sync should insert missing thread row: %#v", result)
	}
	row := providerSyncThreadRow(t, filepath.Join(home, "state_5.sqlite"), sessionID)
	for key, expected := range map[string]string{
		"model_provider":     "CodexPlusPlus",
		"thread_source":      "user",
		"cwd":                "/Users/test/project",
		"title":              "恢复这个历史项目会话",
		"first_user_message": "恢复这个历史项目会话",
		"preview":            "恢复这个历史项目会话",
	} {
		if got := stringFromAny(row[key]); got != expected {
			t.Fatalf("%s mismatch: got %q want %q (row=%#v)", key, got, expected, row)
		}
	}
	var state map[string]any
	if err := readJSON(filepath.Join(home, ".codex-global-state.json"), &state); err != nil {
		t.Fatalf("global state should be readable: %v", err)
	}
	if !containsAnyString(state["electron-saved-workspace-roots"], "/Users/test/project") {
		t.Fatalf("workspace roots should include restored project: %#v", state)
	}
	hints, _ := state["thread-workspace-root-hints"].(map[string]any)
	if got := stringFromAny(hints[sessionID]); got != "/Users/test/project" {
		t.Fatalf("workspace hint mismatch: %q state=%#v", got, state)
	}
}

func TestCodexLaunchPayloadActivatesProtectedWindowsAppsWithoutDirectLaunch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows protected package handling only applies on Windows")
	}
	appDir := filepath.Join(t.TempDir(), "Program Files", "WindowsApps", "OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0", "app")
	exe := filepath.Join(appDir, "ChatGPT.exe")
	writeTestFile(t, exe, "binary")

	payload := codexLaunchPayload(appDir)

	if !boolFromAny(payload["ready"]) {
		t.Fatalf("protected WindowsApps package should be activation-ready: %#v", payload)
	}
	if got := stringFromAny(payload["method"]); got != "packaged_activation" {
		t.Fatalf("protected WindowsApps package should use packaged activation, got %q payload=%#v", got, payload)
	}
	if !boolFromAny(payload["directLaunchBlocked"]) {
		t.Fatalf("protected WindowsApps executable must remain blocked from direct launch: %#v", payload)
	}
	if got := stringFromAny(payload["executable"]); got != exe {
		t.Fatalf("executable mismatch: %q", got)
	}
}

func TestCodexLaunchPayloadAcceptsUnpackedCodexExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable preference only applies on Windows")
	}
	appDir := filepath.Join(t.TempDir(), "CodexAppMirror", "OpenAI.Codex_26.623.19656.0_x64__2p2nqsd0c76g0", "app")
	exe := filepath.Join(appDir, "Codex.exe")
	writeTestFile(t, exe, "binary")

	payload := codexLaunchPayload(appDir)

	if got := stringFromAny(payload["method"]); got != "executable" {
		t.Fatalf("unpacked Codex app dir should prefer direct executable launch: %#v", payload)
	}
	if got := stringFromAny(payload["executable"]); got != exe {
		t.Fatalf("executable mismatch: %q", got)
	}
}

func TestWindowsPackagedExplorerCommandShape(t *testing.T) {
	command := windowsPackagedExplorerCommand("OpenAI.ChatGPT_abc!App", []string{"--remote-debugging-port=9229", "--remote-allow-origins=http://127.0.0.1:9229"})

	if len(command) != 2 {
		t.Fatalf("command length mismatch: %#v", command)
	}
	if command[0] != "explorer.exe" || command[1] != `shell:AppsFolder\OpenAI.ChatGPT_abc!App` {
		t.Fatalf("command shape mismatch: %#v", command)
	}
	for _, part := range command {
		if strings.Contains(part, "127.0.0.1:9229") || strings.Contains(part, "--remote-debugging-port") {
			t.Fatalf("explorer fallback must not receive CDP arguments that can be opened as a URL: %#v", command)
		}
	}
}

func TestPackagedCodexDebugPortErrorGivesActionableGuidance(t *testing.T) {
	err := packagedCodexDebugPortError("OpenAI.ChatGPT_abc!App", 9229, "explorer")
	message := err.Error()

	for _, expected := range []string{"Windows Store/MSIX", "调试端口 9229", "应用已安装", "增强注入暂不可用", "--remote-debugging-port"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("error should contain %q, got %q", expected, message)
		}
	}
}

func TestLaunchFailureDetailIncludesRecommendedAction(t *testing.T) {
	detail := launchFailureDetail(`C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app`, 9229, 57321, errors.New("no cdp"))

	if got := stringFromAny(detail["recommended_action"]); !strings.Contains(got, "ChatGPT.exe") {
		t.Fatalf("recommended action should mention ChatGPT.exe, got %q", got)
	}
	if got := stringFromAny(detail["error"]); got != "no cdp" {
		t.Fatalf("error mismatch: %q", got)
	}
	if got := stringFromAny(detail["codex_app"]); got == "" {
		t.Fatal("codex app should be included")
	}
}

func TestCodexLaunchPayloadAcceptsProtectedPackagedActivationShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("packaged activation is only used on Windows")
	}
	path := `C:\Program Files\WindowsApps\OpenAI.ChatGPT_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	payload := codexLaunchPayload(path)

	if !boolFromAny(payload["ready"]) {
		t.Fatalf("protected packaged app should be activation-ready: %#v", payload)
	}
	if got := stringFromAny(payload["method"]); got != "packaged_activation" {
		t.Fatalf("launch method mismatch: %q", got)
	}
	if got := stringFromAny(payload["appUserModelId"]); got != "OpenAI.ChatGPT_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", got)
	}
}

func TestCompareVersionsHandlesSemverTags(t *testing.T) {
	if compareVersions("v1.1.13", "1.1.12") <= 0 {
		t.Fatal("v1.1.13 should be newer than 1.1.12")
	}
	if compareVersions("1.2.0", "1.10.0") >= 0 {
		t.Fatal("1.2.0 should be older than 1.10.0")
	}
	if compareVersions("ChatGPT Codex Tools 1.1.12", "1.1.12") != 0 {
		t.Fatal("release name prefix should be ignored")
	}
}

func TestSelectCodexToolsAssetPrefersPlatformAndArchitecture(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]releaseAsset{
		{Name: "ChatGPT-Codex-Tools-1.1.13-windows-x64.zip", BrowserDownloadURL: "https://example.com/windows.zip"},
		{Name: "ChatGPT-Codex-Tools-1.1.13-macos-x64.zip", BrowserDownloadURL: "https://example.com/macos-x64.zip"},
		{Name: "ChatGPT-Codex-Tools-1.1.13-macos-arm64.zip", BrowserDownloadURL: "https://example.com/macos-arm64.zip"},
		{Name: "SHA256SUMS.txt", BrowserDownloadURL: "https://example.com/SHA256SUMS.txt"},
	}, "darwin", "arm64")

	if !ok {
		t.Fatal("expected a matching ChatGPT Codex Tools asset")
	}
	if asset.Name != "ChatGPT-Codex-Tools-1.1.13-macos-arm64.zip" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestSelectCodexToolsAssetPrefersMacOSInstaller(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]releaseAsset{
		{Name: "ChatGPT-Codex-Tools-1.1.19-macos-arm64.zip", BrowserDownloadURL: "https://example.com/macos-arm64.zip"},
		{Name: "ChatGPT-Codex-Tools-1.1.19-macos-arm64.pkg", BrowserDownloadURL: "https://example.com/macos-arm64.pkg"},
	}, "darwin", "arm64")

	if !ok {
		t.Fatal("expected a matching ChatGPT Codex Tools asset")
	}
	if asset.Name != "ChatGPT-Codex-Tools-1.1.19-macos-arm64.pkg" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestLatestAppxInstallLocationFromOutput(t *testing.T) {
	if got := latestAppxInstallLocationFromOutput("\nC:\\Program Files\\WindowsApps\\OpenAI.ChatGPTBeta_1.2.3.0_x64__2p2nqsd0c76g0\\app\n"); got != `C:\Program Files\WindowsApps\OpenAI.ChatGPTBeta_1.2.3.0_x64__2p2nqsd0c76g0\app` {
		t.Fatalf("latest appx install location mismatch: %q", got)
	}
}

func TestSelectCodexToolsAssetPrefersWindowsSetup(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]releaseAsset{
		{Name: "ChatGPT-Codex-Tools-1.1.13-windows-x64.zip", BrowserDownloadURL: "https://example.com/windows.zip"},
		{Name: "ChatGPT-Codex-Tools-1.1.13-windows-x64-setup.exe", BrowserDownloadURL: "https://example.com/windows-setup.exe"},
	}, "windows", "amd64")

	if !ok {
		t.Fatal("expected a matching ChatGPT Codex Tools asset")
	}
	if asset.Name != "ChatGPT-Codex-Tools-1.1.13-windows-x64-setup.exe" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestCodexToolsDownloadsAssetsParsesGitHubPagesLinks(t *testing.T) {
	assets := codexToolsDownloadsAssets(`
		<a href="./releases/ChatGPT-Codex-Tools-1.1.26-macos-arm64.pkg">Apple Silicon</a>
		<a href="./releases/ChatGPT-Codex-Tools-1.1.26-macos-arm64.zip">Apple Silicon zip</a>
		<a href="./releases/ChatGPT-Codex-Tools-1.1.26-windows-x64-setup.exe">Windows</a>
		<a href="./releases/ChatGPT-Codex-Tools-1.1.26-macos-arm64.pkg">Duplicate</a>
	`)

	if len(assets) != 3 {
		t.Fatalf("expected 3 unique assets, got %d: %#v", len(assets), assets)
	}
	if assets[0].Name != "ChatGPT-Codex-Tools-1.1.26-macos-arm64.pkg" {
		t.Fatalf("asset name mismatch: %q", assets[0].Name)
	}
	if assets[0].BrowserDownloadURL != codexToolsPagesBaseURL+"releases/ChatGPT-Codex-Tools-1.1.26-macos-arm64.pkg" {
		t.Fatalf("absolute download URL mismatch: %q", assets[0].BrowserDownloadURL)
	}
	asset, ok := selectCodexToolsAsset(assets, "darwin", "arm64")
	if !ok {
		t.Fatal("expected Apple Silicon asset from downloads page")
	}
	if latest := codexToolsAssetVersion(asset.Name); latest != "1.1.26" {
		t.Fatalf("selected version mismatch: %q", latest)
	}
}

func TestCodexToolsDownloadsAssetsSelectsMacOSInstallerFromFallbackPage(t *testing.T) {
	assets := codexToolsDownloadsAssets(`
		<a href="./releases/ChatGPT-Codex-Tools-1.1.27-macos-arm64.zip">Portable Apple Silicon zip</a>
		<a href="./releases/ChatGPT-Codex-Tools-1.1.27-macos-arm64.pkg">Download Apple Silicon installer</a>
		<a href="./releases/ChatGPT-Codex-Tools-1.1.27-macos-x64.pkg">Download Intel installer</a>
	`)

	asset, ok := selectCodexToolsAsset(assets, "darwin", "arm64")
	if !ok {
		t.Fatal("expected a matching macOS asset")
	}
	if asset.Name != "ChatGPT-Codex-Tools-1.1.27-macos-arm64.pkg" {
		t.Fatalf("selected wrong fallback asset: %q", asset.Name)
	}
}

func TestSelectCodexToolsAssetDoesNotCrossArchitectures(t *testing.T) {
	assets := []releaseAsset{
		{Name: "ChatGPT-Codex-Tools-1.1.26-macos-arm64.pkg", BrowserDownloadURL: "https://example.com/macos-arm64.pkg"},
		{Name: "ChatGPT-Codex-Tools-1.1.25-macos-x64.pkg", BrowserDownloadURL: "https://example.com/macos-x64.pkg"},
	}

	asset, ok := selectCodexToolsAsset(assets, "darwin", "amd64")
	if !ok {
		t.Fatal("expected an Intel macOS asset")
	}
	if asset.Name != "ChatGPT-Codex-Tools-1.1.25-macos-x64.pkg" {
		t.Fatalf("selected cross-architecture asset: %q", asset.Name)
	}
}

func TestPickCDPPageTargetPrefersCodexAppPage(t *testing.T) {
	target, err := pickCDPPageTarget([]cdpTarget{
		{ID: "worker", Type: "worker", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/worker"},
		{ID: "blank-page", Type: "page", URL: "about:blank", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/blank"},
		{ID: "codex-page", Type: "page", URL: "app://-/index.html", Title: "Codex", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/codex"},
	})

	if err != nil {
		t.Fatalf("pickCDPPageTarget returned error: %v", err)
	}
	if target.ID != "codex-page" {
		t.Fatalf("selected wrong target: %q", target.ID)
	}
}

func TestPickCDPPageTargetFallsBackToChatGPTTitle(t *testing.T) {
	target, err := pickCDPPageTarget([]cdpTarget{
		{ID: "blank-page", Type: "page", URL: "about:blank", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/blank"},
		{ID: "chatgpt-page", Type: "page", URL: "https://example.com", Title: "ChatGPT", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/chatgpt"},
	})

	if err != nil {
		t.Fatalf("pickCDPPageTarget returned error: %v", err)
	}
	if target.ID != "chatgpt-page" {
		t.Fatalf("selected wrong target: %q", target.ID)
	}
}

func TestCDPPageTargetRejectsStandaloneCodexTitle(t *testing.T) {
	if isCodexCDPPageTarget(cdpTarget{Type: "page", Title: "Codex", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/codex"}) {
		t.Fatal("standalone Codex title must not be treated as a ChatGPT CDP target")
	}
}

func TestPickCDPPageTargetFallsBackToFirstPage(t *testing.T) {
	target, err := pickCDPPageTarget([]cdpTarget{
		{ID: "worker", Type: "worker", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/worker"},
		{ID: "page", Type: "page", URL: "https://example.com", WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/page"},
	})

	if err != nil {
		t.Fatalf("pickCDPPageTarget returned error: %v", err)
	}
	if target.ID != "page" {
		t.Fatalf("selected wrong fallback target: %q", target.ID)
	}
}

func TestDecideRelayRouteKeepsToolDeclarationOnTextRoute(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"hi","tools":[{"type":"web_search"},{"type":"image_generation"}]}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if decision.useImageAPI {
		t.Fatal("plain text with image tool declaration should use text relay")
	}
	if !decision.strippedImageTool {
		t.Fatal("image_generation tool should be stripped from text relay requests")
	}
	if hasImageGenerationTool(t, decision.body) {
		t.Fatal("stripped request body still contains image_generation tool")
	}
}

func TestDecideRelayRouteKeepsSiteToolsInMixedTextRoute(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"open the site","tools":[{"type":"site"},{"type":"web_app"},{"type":"hosted_site"}]}`)
	decision := decideRelayRoute(body, relayProfile{
		RelayMode:                     "mixedApi",
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if decision.useImageAPI {
		t.Fatal("site tools in mixed mode should stay on the text/official-capable route")
	}
	if decision.strippedImageTool {
		t.Fatal("site tools must not be stripped as image tools")
	}
	for _, expected := range []string{"site", "web_app", "hosted_site"} {
		if !hasToolType(t, decision.body, expected) {
			t.Fatalf("mixed route lost site tool %q in body %s", expected, string(decision.body))
		}
	}
}

func TestRelayProxyBaseURLAddsV1ForBareResponsesHost(t *testing.T) {
	baseURL := relayProxyBaseURL("https://api.example.com/", "responses")

	if baseURL != "https://api.example.com/v1" {
		t.Fatalf("responses relay should append /v1 for bare hosts, got %q", baseURL)
	}
}

func TestRelayProxyBaseURLKeepsExistingResponsesPath(t *testing.T) {
	baseURL := relayProxyBaseURL("https://api.example.com/openai/", "responses")

	if baseURL != "https://api.example.com/openai" {
		t.Fatalf("responses relay should preserve existing paths, got %q", baseURL)
	}
}

func TestChatCompletionsIgnoresStoredSeparateImageSettingsForReadiness(t *testing.T) {
	profile := relayProfile{
		Protocol:                      "chatCompletions",
		BaseURL:                       "https://api.example.com/v1",
		APIKey:                        "text-key",
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
	}

	if !relayProfileAPIReady(profile) {
		t.Fatal("Chat Completions readiness must ignore Responses-only separate image settings")
	}
}

func TestChatCompletionsApplyIgnoresStoredSeparateImageSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	profile := relayProfile{
		ID:                            "chat-completions",
		Name:                          "Chat Completions",
		RelayMode:                     "pureApi",
		Protocol:                      "chatCompletions",
		BaseURL:                       "https://api.example.com/v1",
		APIKey:                        "text-key",
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
	}

	if err := applyRelayConfig(filepath.Join(home, ".codex"), profile, true); err != nil {
		t.Fatalf("Chat Completions apply must ignore Responses-only separate image settings: %v", err)
	}
}

func TestEffectiveBaseURLUsesLocalProxyWhenHTTPProxyEnabledForResponses(t *testing.T) {
	profile := relayProfile{
		ID:           "relay",
		Name:         "Relay",
		RelayMode:    "pureApi",
		Protocol:     "responses",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "sk-test",
		ProxyEnabled: true,
		ProxyURL:     "http://127.0.0.1:7890",
	}

	if got, want := effectiveBaseURL(profile), fmt.Sprintf("http://127.0.0.1:%d/v1", localRelayProxyPort); got != want {
		t.Fatalf("responses relay with HTTP proxy should write local relay base URL, got %q want %q", got, want)
	}
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{profile}
	settings.ActiveRelayID = profile.ID
	if !activeRelayNeedsLocalProxy(settings) {
		t.Fatal("HTTP proxy enabled relay should start the local relay proxy")
	}
}

func TestOfficialMixedRelayStartsRemoteControlProxy(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfilesEnabled = true
	settings.RelayProfiles = []relayProfile{{
		ID:                "official-mixed",
		Name:              "Official mixed",
		RelayMode:         "official",
		Protocol:          "responses",
		OfficialMixAPIKey: true,
	}}
	settings.ActiveRelayID = "official-mixed"

	if !activeRelayNeedsLocalProxy(settings) {
		t.Fatal("official mixed relay must start the local Remote Control proxy")
	}
	if !helperNeeded(settings) {
		t.Fatal("official mixed relay must start the helper runtime")
	}

	settings.RelayProfilesEnabled = false
	if activeRelayNeedsLocalProxy(settings) {
		t.Fatal("disabled relay profiles must not enable the mixed Remote Control proxy")
	}
}

func TestRelayHTTPClientRejectsInvalidProxyURL(t *testing.T) {
	_, err := relayHTTPClient(relayProfile{ProxyEnabled: true, ProxyURL: "socks5://127.0.0.1:1080"})
	if err == nil {
		t.Fatal("relayHTTPClient should reject non-http proxy URLs")
	}
	if !strings.Contains(err.Error(), "HTTP 代理地址无效") {
		t.Fatalf("invalid proxy error should be explicit, got %v", err)
	}
}

func TestTestRelayProfileReportsInvalidProxyURL(t *testing.T) {
	result := (&server{}).testRelayProfile(context.Background(), map[string]any{"profile": relayProfile{
		ID:           "relay",
		Name:         "Relay",
		RelayMode:    "pureApi",
		Protocol:     "responses",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "sk-test",
		ProxyEnabled: true,
		ProxyURL:     "ftp://127.0.0.1:21",
	}})

	if result["status"] != "failed" {
		t.Fatalf("invalid proxy URL should fail relay profile test: %#v", result)
	}
	if !strings.Contains(stringFromAny(result["message"]), "HTTP 代理地址无效") {
		t.Fatalf("invalid proxy failure should mention proxy URL, got %#v", result)
	}
}

func TestRelayProxyAttemptUsesConfiguredHTTPProxy(t *testing.T) {
	upstreamPath := ""
	proxyHits := 0
	upstream, proxy := newForwardingProxyPair(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamPath = req.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}), func() { proxyHits++ })
	defer upstream.Close()
	defer proxy.Close()

	profile := relayProfile{
		ID:           "relay",
		Name:         "Relay",
		RelayMode:    "pureApi",
		Protocol:     "responses",
		BaseURL:      upstream.URL + "/v1",
		APIKey:       "sk-test",
		ProxyEnabled: true,
		ProxyURL:     proxy.URL,
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:57323/v1/responses", strings.NewReader(`{"input":"hi"}`))
	rec := httptest.NewRecorder()

	if !forwardRelayProxyAttempt(defaultSettings(), rec, req, []byte(`{"input":"hi"}`), profile, 1, 1) {
		t.Fatal("relay proxy attempt should complete after successful upstream response")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("relay proxy should return upstream status, got %d body=%s", rec.Code, rec.Body.String())
	}
	if proxyHits != 1 {
		t.Fatalf("configured HTTP proxy should be used exactly once, got %d hits", proxyHits)
	}
	if upstreamPath != "/v1/responses" {
		t.Fatalf("upstream path mismatch: %q", upstreamPath)
	}
}

func TestFetchModelsFromSourceUsesConfiguredHTTPProxy(t *testing.T) {
	upstreamPath := ""
	proxyHits := 0
	upstream, proxy := newForwardingProxyPair(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamPath = req.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}), func() { proxyHits++ })
	defer upstream.Close()
	defer proxy.Close()

	models, status := fetchModelsFromSource(context.Background(), codexModelSource{
		ID:           "relay-profile:relay",
		Type:         "relay_profile",
		Name:         "Relay",
		BaseURL:      upstream.URL + "/v1",
		APIKey:       "sk-test",
		ProxyEnabled: true,
		ProxyURL:     proxy.URL,
	})

	if stringFromAny(status["status"]) != "ok" {
		t.Fatalf("model fetch should succeed through proxy: %#v", status)
	}
	if len(models) != 1 || models[0] != "model-a" {
		t.Fatalf("model fetch parsed wrong models: %#v", models)
	}
	if proxyHits != 1 {
		t.Fatalf("configured HTTP proxy should be used for model fetch, got %d hits", proxyHits)
	}
	if upstreamPath != "/v1/models" {
		t.Fatalf("upstream model path mismatch: %q", upstreamPath)
	}
}

func TestAggregateRelayUsesSelectedMemberHTTPProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	clearRelayRotationSelector()
	t.Cleanup(clearRelayRotationSelector)
	proxyHits := 0
	upstream, proxy := newForwardingProxyPair(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}), func() { proxyHits++ })
	defer upstream.Close()
	defer proxy.Close()
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{
		{
			ID:           "member",
			Name:         "Member",
			RelayMode:    "pureApi",
			Protocol:     "responses",
			BaseURL:      upstream.URL + "/v1",
			APIKey:       "sk-test",
			ProxyEnabled: true,
			ProxyURL:     proxy.URL,
		},
		{ID: "agg", Name: "Agg", RelayMode: "aggregate", Protocol: "responses"},
	}
	settings.ActiveRelayID = "agg"
	settings.ActiveAggregateRelayID = "agg"
	settings.AggregateRelayProfiles = []aggregateRelayProfile{{
		ID:       "agg",
		Name:     "Agg",
		Strategy: "failover",
		Members:  []aggregateRelayMember{{RelayID: "member", Weight: 1}},
	}}
	if err := saveSettings(settings); err != nil {
		t.Fatalf("save aggregate relay settings: %v", err)
	}
	runtime := &launcherRuntime{settings: settings}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:57321/v1/responses", strings.NewReader(`{"input":"hi"}`))
	rec := httptest.NewRecorder()

	runtime.forwardRelayProxy(rec, req, []byte(`{"input":"hi"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("aggregate relay should return upstream status, got %d body=%s", rec.Code, rec.Body.String())
	}
	if proxyHits != 1 {
		t.Fatalf("aggregate selected member HTTP proxy should be used, got %d hits", proxyHits)
	}
}

func newForwardingProxyPair(t *testing.T, upstreamHandler http.Handler, onProxy func()) (*httptest.Server, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if onProxy != nil {
			onProxy()
		}
		forwardProxyRequest(t, upstream.URL, w, req)
	}))
	return upstream, proxy
}

func forwardProxyRequest(t *testing.T, upstreamRawURL string, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	if req.URL.Scheme == "" || req.URL.Host == "" {
		t.Fatalf("proxy should receive absolute-form upstream URL, got %q", req.URL.String())
	}
	upstreamURL, err := url.Parse(upstreamRawURL)
	if err != nil {
		t.Fatalf("parse upstream URL failed: %v", err)
	}
	forwarded := req.Clone(req.Context())
	forwarded.URL.Scheme = upstreamURL.Scheme
	forwarded.URL.Host = upstreamURL.Host
	forwarded.Host = upstreamURL.Host
	forwarded.RequestURI = ""
	resp, err := http.DefaultTransport.RoundTrip(forwarded)
	if err != nil {
		t.Fatalf("proxy upstream round trip failed: %v", err)
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func TestSetRelayProxyUserAgentForwardsCodexClientAgent(t *testing.T) {
	source := http.Header{"User-Agent": []string{"codex-cli/1.2.3"}}
	target := http.Header{"User-Agent": []string{"ChatGPTCodexTools-GoRelay/" + version}}

	setRelayProxyUserAgent("", source, target)

	if got := target.Get("User-Agent"); got != "codex-cli/1.2.3" {
		t.Fatalf("relay proxy should forward Codex user agent, got %q", got)
	}
}

func TestSetRelayProxyUserAgentFallsBackToCodex(t *testing.T) {
	target := http.Header{"User-Agent": []string{"ChatGPTCodexTools-GoRelay/" + version}}

	setRelayProxyUserAgent("", http.Header{}, target)

	if got := target.Get("User-Agent"); got != "Codex" {
		t.Fatalf("relay proxy should not expose GoRelay user agent, got %q", got)
	}
}

func TestSetRelayProxyUserAgentPrefersConfiguredProfileAgent(t *testing.T) {
	source := http.Header{"User-Agent": []string{"codex-cli/1.2.3"}}
	target := http.Header{"User-Agent": []string{"ChatGPTCodexTools-GoRelay/" + version}}

	setRelayProxyUserAgent("custom-agent/9", source, target)

	if got := target.Get("User-Agent"); got != "custom-agent/9" {
		t.Fatalf("relay proxy should prefer configured profile user agent, got %q", got)
	}
}

func TestCopyProxyHeadersSkipsAcceptEncoding(t *testing.T) {
	source := http.Header{
		"Accept-Encoding": []string{"gzip, br"},
		"Content-Type":    []string{"application/json"},
	}
	target := http.Header{}

	copyProxyHeaders(source, target)

	if got := target.Get("Accept-Encoding"); got != "" {
		t.Fatalf("relay proxy should control upstream encoding, got %q", got)
	}
	if got := target.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type should still be copied, got %q", got)
	}
}

func TestRelayTestPayloadNormalizesResponsesBaseURL(t *testing.T) {
	endpoint, _ := relayTestPayload(relayProfile{BaseURL: "https://api.example.com", Protocol: "responses"}, "gpt-test")

	if endpoint != "https://api.example.com/v1/responses" {
		t.Fatalf("relay test should use normalized responses endpoint, got %q", endpoint)
	}
}

func TestDecideRelayRouteUsesImageForExplicitToolChoice(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"make it","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if !decision.useImageAPI {
		t.Fatal("explicit image_generation tool_choice should use image relay")
	}
	if decision.strippedImageTool {
		t.Fatal("image relay requests should keep image_generation tool")
	}
	if !hasImageGenerationTool(t, decision.body) {
		t.Fatal("image relay request lost image_generation tool")
	}
}

func TestDecideRelayRouteUsesImageForChineseImageIntent(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"帮我生成一个猫猫图标","tools":[{"type":"image_generation"}]}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if !decision.useImageAPI {
		t.Fatal("clear Chinese image generation intent should use image relay")
	}
	if decision.reason != "latest_user_image_intent" {
		t.Fatalf("unexpected reason: %q", decision.reason)
	}
}

func TestDecideRelayRouteIgnoresOlderImageIntentHistory(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":[{"role":"user","content":"帮我生成一个猫猫图标"},{"role":"assistant","content":"好的"},{"role":"user","content":"检查图片中转逻辑 / 图标中转配置"}],"tools":[{"type":"image_generation"}]}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if decision.useImageAPI {
		t.Fatal("older image intent in history should not route latest text request to image relay")
	}
	if decision.keySource != "default" {
		t.Fatalf("text route should use default key, got %q", decision.keySource)
	}
}

func TestDecideRelayRouteDoesNotUseImageForRelayConfigDiscussion(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"检查图片中转逻辑 / 图标中转配置","tools":[{"type":"image_generation"}]}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        true,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if decision.useImageAPI {
		t.Fatal("discussion about image relay config should not use image relay")
	}
	if !decision.strippedImageTool {
		t.Fatal("image_generation tool should be stripped for config discussion")
	}
}

func TestDecideRelayRouteStripsImageToolWhenImageDisabled(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"帮我生成一个猫猫图标","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`)
	decision := decideRelayRoute(body, relayProfile{
		ImageGenerationEnabled:        false,
		ImageGenerationUseSeparateAPI: true,
		ImageGenerationBaseURL:        "https://image.example/v1",
		ImageGenerationAPIKey:         "image-key",
	})

	if decision.useImageAPI {
		t.Fatal("disabled image generation should always use text relay")
	}
	if !decision.strippedImageTool {
		t.Fatal("disabled image generation should strip image_generation tool")
	}
	if hasImageGenerationTool(t, decision.body) {
		t.Fatal("disabled image generation request still contains image_generation tool")
	}
	if hasToolChoice(t, decision.body) {
		t.Fatal("disabled image generation request still contains image tool_choice")
	}
}

func hasImageGenerationTool(t *testing.T, body []byte) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	tools, _ := value["tools"].([]any)
	for _, tool := range tools {
		if object, ok := tool.(map[string]any); ok && object["type"] == "image_generation" {
			return true
		}
	}
	return false
}

func hasToolType(t *testing.T, body []byte, toolType string) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	tools, _ := value["tools"].([]any)
	for _, tool := range tools {
		if object, ok := tool.(map[string]any); ok && object["type"] == toolType {
			return true
		}
	}
	return false
}

func hasToolChoice(t *testing.T, body []byte) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	_, ok := value["tool_choice"]
	return ok
}
