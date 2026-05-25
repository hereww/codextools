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
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func defaultSettings() backendSettings {
	return backendSettings{
		CodexExtraArgs:      []string{},
		Language:            defaultLanguage,
		Enhancements:        true,
		LaunchMode:          "patch",
		RelayProfiles:       []relayProfile{defaultRelayProfile()},
		ActiveRelayID:       "default",
		RelayTestModel:      defaultRelayTestModel,
		CLIWrapperAPIKeyEnv: defaultAPIKeyEnvironment,
	}
}

func defaultRelayProfile() relayProfile {
	return relayProfile{
		ID:        "default",
		Name:      "默认中转",
		Protocol:  "responses",
		RelayMode: "official",
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
	return normalizeSettings(settings)
}

func normalizeSettings(settings backendSettings) backendSettings {
	if settings.CodexExtraArgs == nil {
		settings.CodexExtraArgs = []string{}
	}
	settings.Language = normalizeLanguage(settings.Language)
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
		switch settings.RelayProfiles[index].RelayMode {
		case "mixedApi":
			settings.RelayProfiles[index].OfficialMixAPIKey = true
		case "pureApi":
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
	}
	if settings.ActiveRelayID == "" {
		settings.ActiveRelayID = settings.RelayProfiles[0].ID
	}
	if settings.RelayTestModel == "" {
		settings.RelayTestModel = defaultRelayTestModel
	}
	if settings.CLIWrapperAPIKeyEnv == "" {
		settings.CLIWrapperAPIKeyEnv = defaultAPIKeyEnvironment
	}
	return settings
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
	settings.CodexExtraArgs = normalizeExtraArgs(settings.CodexExtraArgs)
	if settings.CodexAppPath != "" {
		if normalized := normalizeCodexAppPath(settings.CodexAppPath); normalized != "" {
			settings.CodexAppPath = normalized
		}
	}
	return atomicWriteJSON(settingsPath(), settings)
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
	return os.Rename(tmp, path)
}

func settingsPayload(message string) commandResult {
	return ok(message, settingsPayloadValue(loadSettings()))
}

func settingsPayloadValue(settings backendSettings) map[string]any {
	return map[string]any{
		"settings":      settings,
		"settings_path": settingsPath(),
		"user_scripts":  userScriptInventoryValue(),
	}
}

func (s *server) saveSettings(args map[string]any) commandResult {
	var settings backendSettings
	if err := remarshal(args["settings"], &settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	if err := saveSettings(settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(normalizeSettings(settings)))
	}
	return settingsPayload("设置已保存。")
}
