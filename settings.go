package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "web", "package.json")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("unable to locate codextools repository root")
		}
		dir = parent
	}
}

func managerDistFS(root string) (fs.FS, string, error) {
	if root != "" {
		dist := filepath.Join(root, "web", "dist")
		if fileExists(filepath.Join(dist, "index.html")) {
			return os.DirFS(dist), dist, nil
		}
	}
	dist, err := fs.Sub(embeddedDist, "web/dist")
	if err == nil {
		if _, statErr := fs.Stat(dist, "index.html"); statErr == nil {
			return dist, "embedded:web/dist", nil
		}
	}
	if root != "" {
		return nil, filepath.Join(root, "web", "dist"), fmt.Errorf("未找到前端构建产物 %s，请先运行 npm --prefix web run vite:build 并重新构建下载包", filepath.Join(root, "web", "dist"))
	}
	return nil, "embedded:web/dist", errors.New("内嵌前端资源缺失，请先运行 npm --prefix web run vite:build 后重新执行 go build")
}

func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return stateDirName
	}
	return filepath.Join(home, stateDirName)
}

func defaultInstallRoot() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Applications"
	case "windows":
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			return filepath.Join(userProfile, "Desktop")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, "Desktop")
}

func settingsPath() string {
	return filepath.Join(stateDir(), settingsFileName)
}

func latestStatusPath() string {
	return filepath.Join(stateDir(), latestStatusFileName)
}

func diagnosticLogPath() string {
	return filepath.Join(stateDir(), diagnosticLogFileName)
}

func codexHomeDir() string {
	if custom := strings.TrimSpace(os.Getenv("CODEX_HOME")); custom != "" {
		expanded := filepath.Clean(os.ExpandEnv(custom))
		if isDir(expanded) {
			return expanded
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func defaultSettings() backendSettings {
	return backendSettings{
		CodexExtraArgs:                  []string{},
		Language:                        defaultLanguage,
		ProviderSyncSavedProviders:      []string{},
		ProviderSyncManualProviders:     []string{},
		RelayProfilesEnabled:            true,
		Enhancements:                    true,
		CodexAppPluginMarketplaceUnlock: true,
		CodexAppModelWhitelistUnlock:    true,
		CodexAppSessionDelete:           true,
		CodexAppMarkdownExport:          true,
		CodexAppForceChineseLocale:      true,
		CodexAppFastStartup:             false,
		CodexAppProjectMove:             true,
		CodexAppThreadScrollRestore:     true,
		CodexAppZedRemoteOpen:           true,
		CodexAppUpstreamWorktreeCreate:  true,
		CodexAppNativeMenuPlacement:     true,
		CodexAppNativeMenuLocalization:  true,
		CodexAppDreamSkinTheme:          "pink",
		CodexAppDreamSkinThemeConfig:    defaultDreamSkinThemeConfig(),
		CodexAppStepwiseAPIKeyEnv:       defaultStepwiseAPIKeyEnv,
		CodexAppStepwiseMaxItems:        6,
		CodexAppStepwiseMaxInputChars:   6000,
		CodexAppStepwiseMaxOutputTokens: 500,
		CodexAppStepwiseTimeoutMS:       8000,
		ZedRemoteOpenStrategy:           "addToFocusedWorkspace",
		ZedRemoteProjectRegistryEnabled: true,
		CodexAppImageOverlayOpacity:     35,
		CodexAppImageOverlayFitMode:     "fit",
		LaunchMode:                      "patch",
		ProxyNoProxy:                    "127.0.0.1,localhost,[::1]",
		RelayProfiles:                   []relayProfile{defaultRelayProfile()},
		AggregateRelayProfiles:          []aggregateRelayProfile{},
		ActiveRelayID:                   "default",
		RelayTestModel:                  defaultRelayTestModel,
		CLIWrapperAPIKeyEnv:             defaultAPIKeyEnvironment,
	}
}

func defaultRelayProfile() relayProfile {
	return relayProfile{
		ID:                          "default",
		Name:                        "默认中转",
		Protocol:                    "responses",
		RelayMode:                   "official",
		UseCommonConfig:             true,
		ContextSelection:            relayContextSelection{},
		ContextSelectionInitialized: true,
		ModelInsertMode:             "patch",
		ModelRoutes:                 []relayModelRoute{},
	}
}

func defaultDreamSkinThemeConfig() dreamSkinThemeConfig {
	return dreamSkinThemeConfig{
		"schemaVersion": 1,
		"id":            "pink",
		"name":          "Dream Skin",
		"brandSubtitle": "CODEX DREAM SKIN",
		"tagline":       "把喜欢的画面变成可交互的 Codex 工作台。",
		"projectPrefix": "选择项目 · ",
		"projectLabel":  "选择项目",
		"statusText":    "THEME ONLINE",
		"quote":         "Make something wonderful",
		"appearance":    "auto",
		"colors": map[string]any{
			"background": "#F7F4F5",
			"panel":      "#FFFFFF",
			"panelAlt":   "#FFF7F8",
			"accent":     "#E25563",
			"accentAlt":  "#F07A86",
			"secondary":  "#F3A8AF",
			"highlight":  "#C93D4C",
			"text":       "#2B2224",
			"muted":      "#8A7A7D",
			"line":       "rgba(196, 120, 128, .22)",
		},
	}
}

func loadSettings() backendSettings {
	settings := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return defaultSettings()
	}
	settings = normalizeSettings(settings)
	settingsChanged := false
	if runtime.GOOS == "windows" && strings.TrimSpace(settings.CodexAppPath) != "" {
		originalPath := settings.CodexAppPath
		normalized := normalizeCodexAppPath(originalPath)
		packageReference := isWindowsPackagedAppReference(originalPath) || isWindowsProtectedAppPackagePath(originalPath) || isWindowsProtectedAppPackagePath(normalized)
		if packageReference && (normalized == "" || !windowsCodexPathSupportsLaunch(normalized)) {
			if detected := resolveWindowsCodexFromInstalledApps(); detected != "" {
				normalized = detected
			}
		}
		if packageReference && normalized == "" {
			// Keep the last stable AUMID when PowerShell is temporarily unavailable.
			settings.CodexAppPath = originalPath
		} else if normalized != "" && normalized != originalPath {
			settings.CodexAppPath = normalized
			settingsChanged = true
		}
	}
	if migrated, changed := migrateOfficialAuthBinding(settings); changed {
		settings = migrated
		settingsChanged = true
	}
	if settingsChanged {
		_ = atomicWriteSettingsPreservingUnknown(settingsPath(), settings)
	}
	return settings
}

func normalizeSettings(settings backendSettings) backendSettings {
	if settings.CodexExtraArgs == nil {
		settings.CodexExtraArgs = []string{}
	}
	if settings.ProviderSyncSavedProviders == nil {
		settings.ProviderSyncSavedProviders = []string{}
	}
	if settings.ProviderSyncManualProviders == nil {
		settings.ProviderSyncManualProviders = []string{}
	}
	settings.Language = normalizeLanguage(settings.Language)
	settings.ProxyURL = strings.TrimSpace(settings.ProxyURL)
	settings.ProxyNoProxy = strings.TrimSpace(settings.ProxyNoProxy)
	if settings.ProxyNoProxy == "" {
		settings.ProxyNoProxy = "127.0.0.1,localhost,[::1]"
	}
	settings.ZedRemoteOpenStrategy = normalizeZedOpenStrategy(settings.ZedRemoteOpenStrategy)
	if settings.CodexAppImageOverlayOpacity <= 0 {
		settings.CodexAppImageOverlayOpacity = 35
	}
	if settings.CodexAppImageOverlayOpacity > 100 {
		settings.CodexAppImageOverlayOpacity = 100
	}
	if settings.CodexAppImageOverlayOpacity < 1 {
		settings.CodexAppImageOverlayOpacity = 1
	}
	settings.CodexAppImageOverlayPath = strings.TrimSpace(settings.CodexAppImageOverlayPath)
	settings.CodexAppImageOverlayFitMode = normalizeImageOverlayFitMode(settings.CodexAppImageOverlayFitMode)
	settings.CodexAppDreamSkinTheme = strings.TrimSpace(settings.CodexAppDreamSkinTheme)
	if settings.CodexAppDreamSkinTheme == "" {
		settings.CodexAppDreamSkinTheme = "pink"
	}
	if len(settings.CodexAppDreamSkinThemeConfig) == 0 {
		settings.CodexAppDreamSkinThemeConfig = defaultDreamSkinThemeConfig()
	}
	settings.CodexAppDreamSkinImagePath = strings.TrimSpace(settings.CodexAppDreamSkinImagePath)
	settings.CodexAppStepwiseBaseURL = strings.TrimRight(strings.TrimSpace(settings.CodexAppStepwiseBaseURL), "/")
	settings.CodexAppStepwiseAPIKey = strings.TrimSpace(settings.CodexAppStepwiseAPIKey)
	settings.CodexAppStepwiseAPIKeyEnv = strings.TrimSpace(settings.CodexAppStepwiseAPIKeyEnv)
	if settings.CodexAppStepwiseAPIKeyEnv == "" {
		settings.CodexAppStepwiseAPIKeyEnv = defaultStepwiseAPIKeyEnv
	}
	settings.CodexAppStepwiseModel = strings.TrimSpace(settings.CodexAppStepwiseModel)
	settings.CodexAppStepwiseMaxItems = clampInt(settings.CodexAppStepwiseMaxItems, 0, 6)
	settings.CodexAppStepwiseMaxInputChars = clampInt(settings.CodexAppStepwiseMaxInputChars, 1000, 24000)
	settings.CodexAppStepwiseMaxOutputTokens = clampInt(settings.CodexAppStepwiseMaxOutputTokens, 100, 4000)
	settings.CodexAppStepwiseTimeoutMS = clampInt(settings.CodexAppStepwiseTimeoutMS, 1000, 60000)
	settings.MobileControlRelayURL = strings.TrimSpace(settings.MobileControlRelayURL)
	settings.MobileControlRoom = strings.TrimSpace(settings.MobileControlRoom)
	settings.MobileControlKey = strings.TrimSpace(settings.MobileControlKey)
	common, extractedContext := splitContextConfigSections(settings.RelayCommonConfigContents)
	settings.RelayCommonConfigContents = sanitizeCommonRelayConfig(common)
	settings.RelayContextConfigContents = normalizeConfigText(joinConfigSections(settings.RelayContextConfigContents, extractedContext))
	if settings.LaunchMode != "patch" && settings.LaunchMode != "relay" {
		settings.LaunchMode = "patch"
	}
	if len(settings.RelayProfiles) == 0 {
		settings.RelayProfiles = []relayProfile{defaultRelayProfile()}
	}
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID == "" {
			settings.RelayProfiles[index].ID = fmt.Sprintf("relay-%d", index+1)
		}
		if settings.RelayProfiles[index].Protocol == "" {
			settings.RelayProfiles[index].Protocol = "responses"
		}
		if settings.RelayProfiles[index].Name == "" {
			settings.RelayProfiles[index].Name = settings.RelayProfiles[index].ID
		}
		baseURL := strings.TrimSpace(settings.RelayProfiles[index].BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(settings.RelayProfiles[index].UpstreamBaseURL)
		}
		settings.RelayProfiles[index].BaseURL = baseURL
		settings.RelayProfiles[index].UpstreamBaseURL = baseURL
		if settings.RelayProfiles[index].ModelInsertMode == "" {
			settings.RelayProfiles[index].ModelInsertMode = "patch"
		}
		settings.RelayProfiles[index].ProxyURL = strings.TrimSpace(settings.RelayProfiles[index].ProxyURL)
		settings.RelayProfiles[index].ModelList, settings.RelayProfiles[index].ModelWindows = normalizeModelListAndWindows(settings.RelayProfiles[index].ModelList, settings.RelayProfiles[index].ModelWindows)
		settings.RelayProfiles[index].ModelAutoCompact = strings.TrimSpace(settings.RelayProfiles[index].ModelAutoCompact)
		settings.RelayProfiles[index].ModelVLM = normalizeModelVLM(settings.RelayProfiles[index].ModelVLM)
		settings.RelayProfiles[index].VLMAPIKey = strings.TrimSpace(settings.RelayProfiles[index].VLMAPIKey)
		settings.RelayProfiles[index].VLMModel = strings.TrimSpace(settings.RelayProfiles[index].VLMModel)
		settings.RelayProfiles[index].VLMBaseURL = strings.TrimRight(strings.TrimSpace(settings.RelayProfiles[index].VLMBaseURL), "/")
		settings.RelayProfiles[index].Sub2APIMultiplier = strings.TrimSpace(settings.RelayProfiles[index].Sub2APIMultiplier)
		if !settings.RelayProfiles[index].Sub2APIEnabled {
			settings.RelayProfiles[index].Sub2APIMultiplier = ""
		}
		settings.RelayProfiles[index].ModelRoutes = normalizeRelayModelRoutes(settings.RelayProfiles[index].ModelRoutes)
		if !settings.RelayProfiles[index].ContextSelectionInitialized {
			settings.RelayProfiles[index].ContextSelection = contextSelectionForAllEntries(settings.RelayContextConfigContents)
			settings.RelayProfiles[index].ContextSelectionInitialized = true
		}
		settings.RelayProfiles[index].ContextSelection = normalizeContextSelection(settings.RelayProfiles[index].ContextSelection)
		switch settings.RelayProfiles[index].RelayMode {
		case "mixedApi":
			settings.RelayProfiles[index].OfficialMixAPIKey = true
		case "pureApi":
			settings.RelayProfiles[index].OfficialMixAPIKey = false
		case "aggregate":
			settings.RelayProfiles[index].OfficialMixAPIKey = false
		case "official":
			if settings.RelayProfiles[index].OfficialMixAPIKey {
				settings.RelayProfiles[index].RelayMode = "mixedApi"
			}
		default:
			if settings.RelayProfiles[index].OfficialMixAPIKey {
				settings.RelayProfiles[index].RelayMode = "mixedApi"
			} else {
				settings.RelayProfiles[index].RelayMode = "official"
				settings.RelayProfiles[index].OfficialMixAPIKey = false
			}
		}
		if strings.TrimSpace(settings.RelayProfiles[index].AuthContents) != "" {
			settings.RelayProfiles[index] = syncOfficialAuthMetadataFromAuth(settings.RelayProfiles[index])
		} else if strings.TrimSpace(settings.RelayProfiles[index].OfficialAuthContents) == "" {
			settings.RelayProfiles[index].OfficialAccountLabel = ""
			settings.RelayProfiles[index].OfficialAuthUpdatedAt = ""
		} else if settings.RelayProfiles[index].OfficialAccountLabel == "" {
			status := officialAuthStatusFromContents(settings.RelayProfiles[index].OfficialAuthContents, "settings")
			settings.RelayProfiles[index].OfficialAccountLabel = status.AccountLabel
		}
	}
	activeRelayFound := false
	for _, profile := range settings.RelayProfiles {
		if profile.ID == settings.ActiveRelayID {
			activeRelayFound = true
			break
		}
	}
	if !activeRelayFound {
		settings.ActiveRelayID = settings.RelayProfiles[0].ID
	}
	if settings.AggregateRelayProfiles == nil {
		settings.AggregateRelayProfiles = []aggregateRelayProfile{}
	}
	for index := range settings.AggregateRelayProfiles {
		settings.AggregateRelayProfiles[index] = normalizeAggregateRelayProfile(settings.AggregateRelayProfiles[index], index)
	}
	if settings.ActiveAggregateRelayID == "" && activeRelayProfile(settings).RelayMode == "aggregate" {
		settings.ActiveAggregateRelayID = settings.ActiveRelayID
	}
	if settings.RelayTestModel == "" {
		settings.RelayTestModel = defaultRelayTestModel
	}
	if settings.CLIWrapperAPIKeyEnv == "" {
		settings.CLIWrapperAPIKeyEnv = defaultAPIKeyEnvironment
	}
	if !settings.OnboardingCompleted {
		settings.OnboardingCompletedAt = ""
		settings.OnboardingCompletedPlatform = ""
	} else if settings.OnboardingCompletedPlatform == "" {
		settings.OnboardingCompletedPlatform = runtime.GOOS
	}
	return settings
}

func normalizeRelayModelRoutes(routes []relayModelRoute) []relayModelRoute {
	if routes == nil {
		return []relayModelRoute{}
	}
	normalized := make([]relayModelRoute, 0, len(routes))
	for _, route := range routes {
		route.Model = strings.TrimSpace(route.Model)
		route.TargetRelayID = strings.TrimSpace(route.TargetRelayID)
		route.TargetModel = strings.TrimSpace(route.TargetModel)
		normalized = append(normalized, route)
	}
	return normalized
}

func normalizeAggregateRelayProfile(profile aggregateRelayProfile, index int) aggregateRelayProfile {
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		profile.ID = fmt.Sprintf("aggregate-%d", index+1)
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	profile.Strategy = normalizeAggregateRelayStrategy(profile.Strategy)
	if profile.Members == nil {
		profile.Members = []aggregateRelayMember{}
	}
	for memberIndex := range profile.Members {
		profile.Members[memberIndex].RelayID = strings.TrimSpace(profile.Members[memberIndex].RelayID)
		if profile.Members[memberIndex].Weight <= 0 {
			profile.Members[memberIndex].Weight = 1
		}
	}
	return profile
}

func normalizeAggregateRelayStrategy(value string) string {
	switch strings.TrimSpace(value) {
	case "failover", "conversationRoundRobin", "requestRoundRobin", "weightedRoundRobin":
		return strings.TrimSpace(value)
	default:
		return "failover"
	}
}

func normalizeZedOpenStrategy(value string) string {
	switch strings.TrimSpace(value) {
	case "reuseWindow", "newWindow", "default", "addToFocusedWorkspace":
		return strings.TrimSpace(value)
	default:
		return "addToFocusedWorkspace"
	}
}

func normalizeImageOverlayFitMode(value string) string {
	switch strings.TrimSpace(value) {
	case "fit", "fill", "stretch", "tile", "center":
		return strings.TrimSpace(value)
	default:
		return "fit"
	}
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func migrateOfficialAuthBinding(settings backendSettings) (backendSettings, bool) {
	activeID := activeRelayProfile(settings).ID
	currentConfig := readFile(filepath.Join(codexHomeDir(), "config.toml"))
	currentAuth := readFile(filepath.Join(codexHomeDir(), "auth.json"))
	changed := false
	for index := range settings.RelayProfiles {
		profile := settings.RelayProfiles[index]
		if profile.ID == activeID && relayProfileUsesLiveFiles(profile) && strings.TrimSpace(profile.ConfigContents) == "" && strings.TrimSpace(currentConfig) != "" {
			settings.RelayProfiles[index].ConfigContents = currentConfig
			changed = true
		}
		if profile.ID == activeID {
			if strings.TrimSpace(profile.AuthContents) == "" && strings.TrimSpace(profile.OfficialAuthContents) == "" && strings.TrimSpace(currentAuth) != "" {
				settings.RelayProfiles[index].AuthContents = currentAuth
				settings.RelayProfiles[index].OfficialAuthUpdatedAt = timeNowRFC3339()
				changed = true
			}
		}
		if strings.TrimSpace(settings.RelayProfiles[index].AuthContents) != "" {
			normalized := syncOfficialAuthMetadataFromAuth(settings.RelayProfiles[index])
			if normalized.AuthContents != settings.RelayProfiles[index].AuthContents ||
				normalized.OfficialAuthContents != settings.RelayProfiles[index].OfficialAuthContents ||
				normalized.OfficialAccountLabel != settings.RelayProfiles[index].OfficialAccountLabel ||
				normalized.OfficialAuthUpdatedAt != settings.RelayProfiles[index].OfficialAuthUpdatedAt {
				settings.RelayProfiles[index] = normalized
				changed = true
			}
		}
	}
	return settings, changed
}

func relayProfileUsesLiveFiles(profile relayProfile) bool {
	return profile.RelayMode != "official" || profile.OfficialMixAPIKey
}

func timeNowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

func normalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "zh", "zh-cn", "zh_cn", "cn", "chinese":
		return "zh-CN"
	case "en", "en-us", "en_us", "english":
		return "en-US"
	case "ko", "ko-kr", "ko_kr", "kr", "korean":
		return "ko-KR"
	case "ja", "ja-jp", "ja_jp", "jp", "japanese":
		return "ja-JP"
	default:
		return defaultLanguage
	}
}

func saveSettings(settings backendSettings) error {
	settings = normalizeSettings(settings)
	if settings.ProxyEnabled {
		if strings.TrimSpace(settings.ProxyURL) == "" {
			return errors.New("代理已启用，但代理地址为空")
		}
		if _, err := parseConfiguredProxyURL(settings.ProxyURL); err != nil {
			return err
		}
	}
	settings.CodexExtraArgs = normalizeExtraArgs(settings.CodexExtraArgs)
	if settings.CodexAppPath != "" {
		if normalized := normalizeCodexAppPath(settings.CodexAppPath); normalized != "" {
			settings.CodexAppPath = normalized
		} else if runtime.GOOS == "windows" {
			settings.CodexAppPath = ""
		}
	}
	return atomicWriteSettingsPreservingUnknown(settingsPath(), settings)
}

func loadRuntimeRelaySettings(fallback backendSettings) backendSettings {
	settings := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return fallback
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fallback
	}
	return normalizeSettings(settings)
}

func atomicWriteSettingsPreservingUnknown(path string, settings backendSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	var next map[string]json.RawMessage
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	var merged map[string]json.RawMessage
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		_ = json.Unmarshal(existing, &merged)
	}
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	for key, value := range next {
		merged[key] = value
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, out)
}

func normalizeExtraArgs(args []string) []string {
	var normalized []string
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			normalized = append(normalized, arg)
		}
	}
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

func atomicWriteWithMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := file.Chmod(mode.Perm()); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

func settingsPayload(message string) commandResult {
	return ok(message, settingsPayloadValue(loadSettings()))
}

func settingsPayloadValue(settings backendSettings) map[string]any {
	return map[string]any{
		"settings":              settings,
		"settings_path":         settingsPath(),
		"user_scripts":          userScriptInventoryValue(),
		"envConflicts":          detectEnvConflicts(),
		"pendingProviderImport": pendingProviderImportPayload(),
	}
}

func pendingProviderImportPayload() any {
	request, err := loadPendingProviderImport()
	if err != nil || request == nil {
		return nil
	}
	return request
}

func (s *server) saveSettings(args map[string]any) commandResult {
	settings := loadSettings()
	if err := remarshal(args["settings"], &settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	if err := saveSettings(settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	return settingsPayload("设置已保存。")
}

func (s *server) checkEnvConflicts() commandResult {
	payload := settingsPayloadValue(loadSettings())
	payload["envConflicts"] = detectEnvConflicts()
	return ok("环境变量冲突已检测。", payload)
}

func (s *server) removeEnvConflicts(args map[string]any) commandResult {
	names := stringSliceArg(args, "names")
	if len(names) == 0 {
		for _, conflict := range detectEnvConflicts() {
			names = append(names, conflict.Name)
		}
	}
	removal, err := removeEnvConflicts(names, filepath.Join(stateDir(), "env-conflict-backups"))
	payload := settingsPayloadValue(loadSettings())
	if err != nil {
		return failed("环境变量清理失败："+err.Error(), payload)
	}
	payload["envConflictRemoval"] = removal
	payload["envConflicts"] = detectEnvConflicts()
	return ok("环境变量冲突已清理；如 Codex 已在运行，请重启 Codex。", payload)
}
