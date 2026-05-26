package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestBuildWatcherInstallPlanMatchesOriginalWindowsShape(t *testing.T) {
	plan := buildWatcherInstallPlan(`C:\Tools\Codex++.exe`, 9229, `C:\Users\A\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\CodexPlusPlusWatcher.lnk`)

	if plan.LauncherPath != `C:\Tools\Codex++.exe` {
		t.Fatalf("launcher path mismatch: %q", plan.LauncherPath)
	}
	if plan.Arguments != "--debug-port 9229" {
		t.Fatalf("arguments mismatch: %q", plan.Arguments)
	}
	if plan.RunValue != `"C:\Tools\Codex++.exe" --debug-port 9229` {
		t.Fatalf("run value mismatch: %q", plan.RunValue)
	}
	if plan.ShortcutPath == "" {
		t.Fatal("shortcut path should be preserved")
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

func TestChatGPTAuthStatusFromContentsReadsEmail(t *testing.T) {
	status := chatGPTAuthStatusFromContents(fakeChatGPTAuthJSON(t, "alpha@example.com"), "test")

	if !status.Authenticated {
		t.Fatal("official auth should be authenticated")
	}
	if status.AccountLabel != "alpha@example.com" {
		t.Fatalf("account label mismatch: %q", status.AccountLabel)
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
	status := chatGPTAuthStatus(filepath.Join(home, ".codex"))
	if !status.Authenticated || status.AccountLabel != "official@example.com" {
		t.Fatalf("bound official auth was not written: %#v", status)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(string(config), "CodexPlusPlus") {
		t.Fatalf("official mode should clear relay provider config:\n%s", string(config))
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
	status := chatGPTAuthStatus(filepath.Join(home, ".codex"))
	if !status.Authenticated || status.AccountLabel != "mixed@example.com" {
		t.Fatalf("bound mixed official auth was not written: %#v", status)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(config), `experimental_bearer_token = "relay-key"`) {
		t.Fatalf("mixed relay config missing bearer token:\n%s", string(config))
	}
}

func TestPureAPIModeKeepsOfficialBindingInactive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	officialAuth := fakeChatGPTAuthJSON(t, "stored@example.com")
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
	if !strings.Contains(string(auth), `"OPENAI_API_KEY": "pure-key"`) {
		t.Fatalf("pure API auth should use API key, got:\n%s", string(auth))
	}
	loaded := loadSettings()
	if activeRelayProfile(loaded).OfficialAccountLabel != "stored@example.com" {
		t.Fatal("pure API mode should not remove stored official binding")
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
	if marketplaceCount != 1 {
		t.Fatalf("marketplace count mismatch: %d", marketplaceCount)
	}
	for _, expected := range []string{
		`[marketplaces.openai-curated]`,
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

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
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

func TestSelectCodexMirrorAssetPrefersWindowsInstaller(t *testing.T) {
	asset, ok := selectCodexMirrorAsset([]codexAppMirrorAsset{
		{Name: "release-manifest.json", BrowserDownloadURL: "https://example.com/release-manifest.json"},
		{Name: "SHA256SUMS-windows.txt", BrowserDownloadURL: "https://example.com/SHA256SUMS-windows.txt"},
		{Name: "OpenAI.Codex_26.519.5221.0_x64__2p2nqsd0c76g0.Msix", BrowserDownloadURL: "https://example.com/OpenAI.Codex_26.519.5221.0_x64__2p2nqsd0c76g0.Msix"},
	}, "windows", "amd64")

	if !ok {
		t.Fatal("expected a windows asset")
	}
	if asset.Name != "OpenAI.Codex_26.519.5221.0_x64__2p2nqsd0c76g0.Msix" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestSelectCodexMirrorAssetPrefersMacArchitecture(t *testing.T) {
	asset, ok := selectCodexMirrorAsset([]codexAppMirrorAsset{
		{Name: "Codex-mac-x64.dmg", BrowserDownloadURL: "https://example.com/Codex-mac-x64.dmg"},
		{Name: "Codex-mac-arm64.dmg", BrowserDownloadURL: "https://example.com/Codex-mac-arm64.dmg"},
	}, "darwin", "arm64")

	if !ok {
		t.Fatal("expected a macOS asset")
	}
	if asset.Name != "Codex-mac-arm64.dmg" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestCompareVersionsHandlesSemverTags(t *testing.T) {
	if compareVersions("v1.1.13", "1.1.12") <= 0 {
		t.Fatal("v1.1.13 should be newer than 1.1.12")
	}
	if compareVersions("1.2.0", "1.10.0") >= 0 {
		t.Fatal("1.2.0 should be older than 1.10.0")
	}
	if compareVersions("CodexTools 1.1.12", "1.1.12") != 0 {
		t.Fatal("release name prefix should be ignored")
	}
}

func TestSelectCodexToolsAssetPrefersPlatformAndArchitecture(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]codexAppMirrorAsset{
		{Name: "CodexTools-1.1.13-windows-x64.zip", BrowserDownloadURL: "https://example.com/windows.zip"},
		{Name: "CodexTools-1.1.13-macos-x64.zip", BrowserDownloadURL: "https://example.com/macos-x64.zip"},
		{Name: "CodexTools-1.1.13-macos-arm64.zip", BrowserDownloadURL: "https://example.com/macos-arm64.zip"},
		{Name: "SHA256SUMS.txt", BrowserDownloadURL: "https://example.com/SHA256SUMS.txt"},
	}, "darwin", "arm64")

	if !ok {
		t.Fatal("expected a matching CodexTools asset")
	}
	if asset.Name != "CodexTools-1.1.13-macos-arm64.zip" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
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

func hasToolChoice(t *testing.T, body []byte) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	_, ok := value["tool_choice"]
	return ok
}
