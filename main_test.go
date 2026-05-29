package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	if string(auth) != currentAuth {
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
	if string(auth) != currentAuth {
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

func TestNormalizeCodexAppPathAcceptsWindowsExecutableAndAppDir(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "OpenAI.Codex_1.2.3.0_x64__test", "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("failed to create app dir: %v", err)
	}
	exe := filepath.Join(appDir, "Codex.exe")
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
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	if got := packagedWindowsAppUserModelID(path); got != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", got)
	}
}

func TestPackagedWindowsAppUserModelIDIsCaseInsensitive(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`
	mistypedCase := strings.Replace(path, "OpenAI.Codex_", "OpenAl.Codex_", 1)

	if got := packagedWindowsAppUserModelID(strings.ToLower(path)); got != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("lowercase package path should still resolve app id: %q", got)
	}
	if got := packagedWindowsAppUserModelID(mistypedCase); got != "" {
		t.Fatalf("non Codex package identity should not resolve app id: %q", got)
	}
}

func TestWindowsPackagePathNormalizesToAppDirOnWindows(t *testing.T) {
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app\Codex.exe`

	if runtime.GOOS != "windows" {
		if got := normalizeCodexAppPath(path); got != "" {
			t.Fatalf("Windows package paths should not normalize outside Windows: %q", got)
		}
		return
	}
	want := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`
	if got := normalizeCodexAppPath(path); got != want {
		t.Fatalf("Windows package path should normalize to app dir: %q", got)
	}
}

func TestWindowsPackageShapeNormalizesWithoutReadableExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows package normalization only applies on Windows")
	}
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0`
	want := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	if got := normalizeCodexAppPath(path); got != want {
		t.Fatalf("package shape should normalize without file access: %q", got)
	}
}

func TestMissingWindowsExecutionAliasDoesNotNormalize(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("execution alias guard only applies on Windows")
	}
	path := filepath.Join(t.TempDir(), "Microsoft", "WindowsApps", "Codex.exe")

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
		t.Fatalf("plain directory without Codex.exe should not normalize: %q", got)
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
	if runtime.GOOS != "windows" {
		t.Skip("packaged activation is only built on Windows")
	}
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	activation := buildWindowsPackagedActivation(path, 9229, []string{"--force_high_performance_gpu"})

	if activation == nil {
		t.Fatal("activation should be built")
	}
	if activation.appUserModelID != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", activation.appUserModelID)
	}
	if activation.arguments != "--remote-debugging-port=9229 --remote-allow-origins=http://127.0.0.1:9229 --force_high_performance_gpu" {
		t.Fatalf("activation arguments mismatch: %q", activation.arguments)
	}
}

func TestCodexLaunchPayloadPrefersPackagedActivationWhenReadable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows packaged activation preference only applies on Windows")
	}
	appDir := filepath.Join(t.TempDir(), "OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0", "app")
	exe := filepath.Join(appDir, "Codex.exe")
	writeTestFile(t, exe, "binary")

	payload := codexLaunchPayload(appDir)

	if got := stringFromAny(payload["method"]); got != "packaged_activation" {
		t.Fatalf("readable MSIX app dir should prefer packaged activation: %#v", payload)
	}
	if got := stringFromAny(payload["executable"]); got != exe {
		t.Fatalf("executable mismatch: %q", got)
	}
}

func TestWindowsPackagedExplorerCommandShape(t *testing.T) {
	command := windowsPackagedExplorerCommand("OpenAI.Codex_abc!App", []string{"--remote-debugging-port=9229"})

	if len(command) != 3 {
		t.Fatalf("command length mismatch: %#v", command)
	}
	if command[0] != "explorer.exe" || command[1] != `shell:AppsFolder\OpenAI.Codex_abc!App` || command[2] != "--remote-debugging-port=9229" {
		t.Fatalf("command shape mismatch: %#v", command)
	}
}

func TestCodexLaunchPayloadUsesPackagedActivationShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("packaged activation is only used on Windows")
	}
	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.519.11010.0_x64__2p2nqsd0c76g0\app`

	payload := codexLaunchPayload(path)

	if !boolFromAny(payload["ready"]) {
		t.Fatalf("packaged app should be launch-ready: %#v", payload)
	}
	if got := stringFromAny(payload["method"]); got != "packaged_activation" {
		t.Fatalf("launch method mismatch: %q", got)
	}
	if got := stringFromAny(payload["appUserModelId"]); got != "OpenAI.Codex_2p2nqsd0c76g0!App" {
		t.Fatalf("app user model id mismatch: %q", got)
	}
}

func TestFindLatestWindowsCodexAppDirPrefersHighestVersion(t *testing.T) {
	root := t.TempDir()
	oldApp := filepath.Join(root, "OpenAI.Codex_1.2.3.0_x64__abc", "app")
	newApp := filepath.Join(root, "OpenAI.Codex_26.519.11010.0_x64__abc", "app")
	if err := os.MkdirAll(oldApp, 0o755); err != nil {
		t.Fatalf("failed to create old app dir: %v", err)
	}
	if err := os.MkdirAll(newApp, 0o755); err != nil {
		t.Fatalf("failed to create new app dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "OpenAI.Codex_not-a-version_x64__abc"), 0o755); err != nil {
		t.Fatalf("failed to create invalid app dir: %v", err)
	}

	if got := findLatestWindowsCodexAppDir(root); got != newApp {
		t.Fatalf("latest app dir mismatch: %q", got)
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

func TestSelectCodexToolsAssetPrefersMacOSInstaller(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]codexAppMirrorAsset{
		{Name: "CodexTools-1.1.19-macos-arm64.zip", BrowserDownloadURL: "https://example.com/macos-arm64.zip"},
		{Name: "CodexTools-1.1.19-macos-arm64.pkg", BrowserDownloadURL: "https://example.com/macos-arm64.pkg"},
	}, "darwin", "arm64")

	if !ok {
		t.Fatal("expected a matching CodexTools asset")
	}
	if asset.Name != "CodexTools-1.1.19-macos-arm64.pkg" {
		t.Fatalf("selected wrong asset: %q", asset.Name)
	}
}

func TestSelectCodexToolsAssetPrefersWindowsSetup(t *testing.T) {
	asset, ok := selectCodexToolsAsset([]codexAppMirrorAsset{
		{Name: "CodexTools-1.1.13-windows-x64.zip", BrowserDownloadURL: "https://example.com/windows.zip"},
		{Name: "CodexTools-1.1.13-windows-x64-setup.exe", BrowserDownloadURL: "https://example.com/windows-setup.exe"},
	}, "windows", "amd64")

	if !ok {
		t.Fatal("expected a matching CodexTools asset")
	}
	if asset.Name != "CodexTools-1.1.13-windows-x64-setup.exe" {
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

func TestSetRelayProxyUserAgentForwardsCodexClientAgent(t *testing.T) {
	source := http.Header{"User-Agent": []string{"codex-cli/1.2.3"}}
	target := http.Header{"User-Agent": []string{"CodexPlusPlus-GoRelay/" + version}}

	setRelayProxyUserAgent(source, target)

	if got := target.Get("User-Agent"); got != "codex-cli/1.2.3" {
		t.Fatalf("relay proxy should forward Codex user agent, got %q", got)
	}
}

func TestSetRelayProxyUserAgentFallsBackToCodex(t *testing.T) {
	target := http.Header{"User-Agent": []string{"CodexPlusPlus-GoRelay/" + version}}

	setRelayProxyUserAgent(http.Header{}, target)

	if got := target.Get("User-Agent"); got != "Codex" {
		t.Fatalf("relay proxy should not expose GoRelay user agent, got %q", got)
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

func hasToolChoice(t *testing.T, body []byte) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	_, ok := value["tool_choice"]
	return ok
}
