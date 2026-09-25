package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const relayAppliedModeMarkerPrefix = "# chatgpt-codex-tools: applied-relay-mode="

func (s *server) relayStatus() commandResult {
	status := relayStatusFromHome(codexHomeDir(), loadSettings())
	message := "未检测到 ChatGPT 登录状态，请先在 ChatGPT 中正常登录。"
	if boolFromAny(status["currentAuthenticated"]) {
		message = "已检测到当前 ChatGPT 登录状态。"
	} else if boolFromAny(status["boundOfficialAuthenticated"]) {
		message = "已检测到已绑定的官方账号。"
	}
	return ok(message, status)
}

func relayStatusFromHome(home string, settingsOpt ...backendSettings) map[string]any {
	auth := officialAuthStatus(home)
	config := relayConfigStatus(home)
	settings := loadSettings()
	if len(settingsOpt) > 0 {
		settings = normalizeSettings(settingsOpt[0])
	}
	bound := boundOfficialAuthStatus(settings)
	active := activeRelayProfile(settings)
	selectedMode := active.RelayMode
	appliedMode := "official"
	if config.Configured {
		appliedMode = relayAppliedModeMarker(readFile(config.ConfigPath))
		if appliedMode == "" || appliedMode == "official" {
			// API configs written before the marker existed cannot reliably distinguish
			// mixed, pure, and aggregate modes. Treat them conservatively as pure API.
			appliedMode = "pureApi"
		}
	}
	officialAuthenticated := auth.Authenticated || bound.Authenticated
	officialAccountLabel := auth.AccountLabel
	if officialAccountLabel == "" {
		officialAccountLabel = bound.AccountLabel
	}
	officialAuthSource := auth.Source
	if officialAuthSource == "" {
		officialAuthSource = bound.Source
	}
	realtime := applyStoredOfficialRealtimeDenial(officialRealtimeCapabilityForSettings(settings, home, time.Now()))
	return map[string]any{
		"activeMode":                 selectedMode,
		"selectedMode":               selectedMode,
		"appliedMode":                appliedMode,
		"authenticated":              auth.Authenticated,
		"authSource":                 auth.Source,
		"accountLabel":               nullableString(auth.AccountLabel),
		"currentAuthenticated":       auth.Authenticated,
		"currentAuthSource":          auth.Source,
		"currentAccountLabel":        nullableString(auth.AccountLabel),
		"officialAuthenticated":      officialAuthenticated,
		"officialAuthSource":         officialAuthSource,
		"officialAccountLabel":       nullableString(officialAccountLabel),
		"boundOfficialAuthenticated": bound.Authenticated,
		"boundOfficialAuthSource":    bound.Source,
		"boundOfficialAccountLabel":  nullableString(bound.AccountLabel),
		"boundOfficialProfileId":     nullableString(bound.ProfileID),
		"boundOfficialProfileName":   nullableString(bound.ProfileName),
		"officialRealtimeAvailable":  realtime.Available,
		"officialRealtimeReason":     realtime.Reason,
		"officialRealtimeMessage":    realtime.Message,
		"configPath":                 config.ConfigPath,
		"configured":                 config.Configured,
		"requiresOpenaiAuth":         config.RequiresOpenAIAuth,
		"hasBearerToken":             config.HasBearerToken,
		"backupPath":                 nil,
	}
}

type boundOfficialAuthSummary struct {
	Authenticated bool
	Source        string
	AccountLabel  string
	ProfileID     string
	ProfileName   string
}

func boundOfficialAuthStatus(settings backendSettings) boundOfficialAuthSummary {
	activeID := activeRelayProfile(settings).ID
	for _, profile := range settings.RelayProfiles {
		if profile.ID == activeID {
			if summary, ok := relayProfileOfficialAuthStatus(profile); ok {
				return summary
			}
			break
		}
	}
	for _, profile := range settings.RelayProfiles {
		if profile.ID == activeID {
			continue
		}
		if summary, ok := relayProfileOfficialAuthStatus(profile); ok {
			return summary
		}
	}
	return boundOfficialAuthSummary{}
}

func relayProfileOfficialAuthStatus(profile relayProfile) (boundOfficialAuthSummary, bool) {
	contents := runtimeAuthContents(profile)
	if contents == "" {
		return boundOfficialAuthSummary{}, false
	}
	status := officialAuthStatusFromContents(contents, "settings:"+profile.ID)
	if !status.Authenticated {
		return boundOfficialAuthSummary{}, false
	}
	label := status.AccountLabel
	if label == "" {
		label = strings.TrimSpace(profile.OfficialAccountLabel)
	}
	return boundOfficialAuthSummary{
		Authenticated: true,
		Source:        status.Source,
		AccountLabel:  label,
		ProfileID:     profile.ID,
		ProfileName:   profile.Name,
	}, true
}

func officialAuthStatus(home string) authStatus {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return authStatus{}
	}
	return officialAuthStatusFromContents(string(data), path)
}

func officialAuthStatusFromContents(contents, source string) authStatus {
	var value map[string]any
	if json.Unmarshal([]byte(contents), &value) != nil {
		return authStatus{}
	}
	authMode := strings.ToLower(strings.TrimSpace(stringFromAny(value["auth_mode"])))
	if authMode != "" && authMode != "chatgpt" && authMode != "openai" {
		return authStatus{}
	}
	tokens, _ := value["tokens"].(map[string]any)
	if tokens == nil || (!hasToken(tokens, "access_token") && !hasToken(tokens, "id_token") && !hasToken(tokens, "refresh_token")) {
		return authStatus{}
	}
	label := accountLabelFromTokens(tokens)
	if label == "" {
		label = "官方账号"
	}
	return authStatus{Authenticated: true, Source: source, AccountLabel: label}
}

type officialAuthSnapshot struct {
	Contents     string
	AccountLabel string
	UpdatedAt    string
}

func currentOfficialAuthSnapshot(home string) (officialAuthSnapshot, bool) {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return officialAuthSnapshot{}, false
	}
	contents := string(data)
	status := officialAuthStatusFromContents(contents, path)
	if !status.Authenticated {
		return officialAuthSnapshot{}, false
	}
	return officialAuthSnapshot{
		Contents:     contents,
		AccountLabel: status.AccountLabel,
		UpdatedAt:    time.Now().Format(time.RFC3339),
	}, true
}

func hasToken(tokens map[string]any, key string) bool {
	return strings.TrimSpace(stringFromAny(tokens[key])) != ""
}

func accountLabelFromTokens(tokens map[string]any) string {
	for _, key := range []string{"id_token", "access_token"} {
		if label := accountLabelFromJWT(stringFromAny(tokens[key])); label != "" {
			return label
		}
	}
	for _, key := range []string{"email", "profile.email", "user.email", "account.email", "openai.email"} {
		if label := nestedStringFromAnyMap(tokens, strings.Split(key, ".")...); label != "" {
			return label
		}
	}
	return ""
}

func nestedStringFromAnyMap(value map[string]any, path ...string) string {
	var current any = value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return strings.TrimSpace(stringFromAny(current))
}

func accountLabelFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return ""
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	if email := strings.TrimSpace(stringFromAny(value["email"])); email != "" {
		return email
	}
	if profile, ok := value["https://api.openai.com/profile"].(map[string]any); ok {
		if email := strings.TrimSpace(stringFromAny(profile["email"])); email != "" {
			return email
		}
	}
	return strings.TrimSpace(stringFromAny(value["name"]))
}

func relayConfigStatus(home string) configStatus {
	path := filepath.Join(home, "config.toml")
	data, _ := os.ReadFile(path)
	contents := string(data)
	providerActive := rootKeyString(contents, "model_provider") == relayProvider
	provider := tableValues(contents, "model_providers."+relayProvider)
	requiresAuth := strings.TrimSpace(provider["requires_openai_auth"]) == "true"
	hasBearer := strings.TrimSpace(unquoteToml(provider["experimental_bearer_token"])) != ""
	hasBaseURL := strings.TrimSpace(unquoteToml(provider["base_url"])) != ""
	return configStatus{Configured: providerActive && requiresAuth && hasBearer && hasBaseURL, RequiresOpenAIAuth: requiresAuth, HasBearerToken: hasBearer, ConfigPath: path}
}

func (s *server) readRelayFiles() commandResult {
	payload := relayFilesPayload(codexHomeDir())
	return ok("配置文件内容已读取。", payload)
}

func relayFilesPayload(home string) map[string]any {
	configPath := filepath.Join(home, "config.toml")
	authPath := filepath.Join(home, "auth.json")
	config, _ := os.ReadFile(configPath)
	auth, _ := os.ReadFile(authPath)
	return map[string]any{"configPath": configPath, "authPath": authPath, "configContents": string(config), "authContents": string(auth)}
}

func currentRelayFileSnapshot(home string) relayProfile {
	payload := relayFilesPayload(home)
	return relayProfile{
		ConfigContents: stringFromAny(payload["configContents"]),
		AuthContents:   stringFromAny(payload["authContents"]),
	}
}

func canonicalAuthContents(profile relayProfile) string {
	return strings.TrimSpace(profile.AuthContents)
}

func runtimeAuthContents(profile relayProfile) string {
	if contents := strings.TrimSpace(profile.AuthContents); contents != "" {
		return contents
	}
	return strings.TrimSpace(profile.OfficialAuthContents)
}

func promoteLegacyOfficialAuth(profile relayProfile) relayProfile {
	if strings.TrimSpace(profile.AuthContents) == "" && strings.TrimSpace(profile.OfficialAuthContents) != "" {
		status := officialAuthStatusFromContents(profile.OfficialAuthContents, "settings:"+profile.ID)
		if status.Authenticated {
			profile.AuthContents = profile.OfficialAuthContents
		}
	}
	if strings.TrimSpace(profile.AuthContents) != "" {
		return syncOfficialAuthMetadataFromAuth(profile)
	}
	return profile
}

func syncOfficialAuthMetadataFromAuth(profile relayProfile) relayProfile {
	contents := strings.TrimSpace(profile.AuthContents)
	if contents == "" {
		profile.OfficialAuthContents = ""
		profile.OfficialAccountLabel = ""
		profile.OfficialAuthUpdatedAt = ""
		return profile
	}
	status := officialAuthStatusFromContents(contents, "settings:"+profile.ID)
	if !status.Authenticated {
		profile.OfficialAuthContents = ""
		profile.OfficialAccountLabel = ""
		profile.OfficialAuthUpdatedAt = ""
		return profile
	}
	profile.OfficialAuthContents = profile.AuthContents
	profile.OfficialAccountLabel = status.AccountLabel
	if strings.TrimSpace(profile.OfficialAuthUpdatedAt) == "" {
		profile.OfficialAuthUpdatedAt = time.Now().Format(time.RFC3339)
	}
	return profile
}

func ensureRelaySnapshot(profile relayProfile, currentConfig string, allowLegacyAuthFallback bool) relayProfile {
	if profile.RelayMode == "official" {
		configContents := profile.ConfigContents
		if strings.TrimSpace(configContents) == "" {
			configContents = currentConfig
		}
		profile.ConfigContents = officialRelayConfigSnapshot(configContents)
	} else if strings.TrimSpace(profile.ConfigContents) == "" {
		profile.ConfigContents = upsertModelProviderConfig(currentConfig, effectiveBaseURL(profile), strings.TrimSpace(profile.APIKey), profile)
	}
	if allowLegacyAuthFallback {
		profile = promoteLegacyOfficialAuth(profile)
	} else if strings.TrimSpace(profile.AuthContents) != "" {
		profile = syncOfficialAuthMetadataFromAuth(profile)
	}
	return profile
}

func writeRelaySnapshot(home string, settings backendSettings, relay relayProfile, pure bool) (*string, error) {
	return writeRelaySnapshotWithWriter(home, settings, relay, pure, writeRelaySnapshotFile)
}

type relaySnapshotFile struct {
	Path       string
	Exists     bool
	Data       []byte
	Mode       os.FileMode
	IsSymlink  bool
	LinkTarget string
}

type relaySnapshotWrite struct {
	Path    string
	Data    []byte
	Mode    os.FileMode
	Private bool
}

func writeRelaySnapshotWithWriter(home string, settings backendSettings, relay relayProfile, pure bool, writeFile func(relaySnapshotWrite) error) (*string, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	configContents, err := relayConfigForWrite(settings, relay)
	if err != nil {
		return nil, err
	}
	if liveConfig, readErr := os.ReadFile(filepath.Join(home, "config.toml")); readErr == nil {
		configContents = preserveLiveHookState(configContents, string(liveConfig))
	}
	if pure {
		configContents = ensureConfigBearerToken(configContents, strings.TrimSpace(relay.APIKey))
	}
	configContents = withRelayAppliedModeMarker(configContents, relay.RelayMode)

	writes := make([]relaySnapshotWrite, 0, 3)
	catalogData, hasCatalog, err := relayModelCatalogData(relay)
	if err != nil {
		return nil, err
	}
	if hasCatalog {
		writes = append(writes, relaySnapshotWrite{Path: filepath.Join(home, filepath.FromSlash(relayModelCatalogRelativePath(relay.ID))), Data: catalogData, Mode: 0o644})
	}
	configPath := filepath.Join(home, "config.toml")
	writes = append(writes, relaySnapshotWrite{Path: configPath, Data: []byte(configContents), Mode: 0o644})
	authContents := canonicalAuthContents(relay)
	if !pure && strings.TrimSpace(authContents) != "" {
		writes = append(writes, relaySnapshotWrite{Path: filepath.Join(home, "auth.json"), Data: []byte(authContents), Mode: 0o600, Private: true})
	}

	snapshots, err := captureRelaySnapshotFiles(writes)
	if err != nil {
		return nil, err
	}
	var backupPath *string
	for _, snapshot := range snapshots {
		if snapshot.Path != configPath || !snapshot.Exists {
			continue
		}
		path, backupErr := backupCodexConfig(configPath, "relay")
		if backupErr != nil {
			return nil, backupErr
		}
		backupPath = &path
		break
	}

	for index, write := range writes {
		if err := writeFile(write); err != nil {
			rollbackErr := restoreRelaySnapshotFiles(snapshots[:index+1])
			if rollbackErr != nil {
				return backupPath, errors.Join(err, fmt.Errorf("回滚 relay 文件失败：%w", rollbackErr))
			}
			return backupPath, err
		}
	}
	return backupPath, nil
}

func preserveLiveHookState(targetConfig, liveConfig string) string {
	bom, targetText := splitTomlBOM(targetConfig)
	_, liveText := splitTomlBOM(liveConfig)
	var target, live map[string]any
	if toml.Unmarshal([]byte(targetText), &target) != nil || toml.Unmarshal([]byte(liveText), &live) != nil {
		return targetConfig
	}
	liveHooks, _ := live["hooks"].(map[string]any)
	state, hasLiveState := liveHooks["state"]
	if hasLiveState {
		encoded, err := toml.Marshal(map[string]any{"hooks": map[string]any{"state": state}})
		if err != nil {
			return targetConfig
		}
		value := tableValues(string(encoded), "hooks")["state"]
		if value == "" {
			return targetConfig
		}
		return bom + upsertTableKey(targetText, "hooks", "state", value)
	}
	if _, exists := tableValues(targetText, "hooks")["state"]; exists {
		return bom + removeTableKey(targetText, "hooks", "state")
	}
	return targetConfig
}

func writeRelaySnapshotFile(write relaySnapshotWrite) error {
	if write.Private {
		return atomicWriteWithMode(write.Path, write.Data, write.Mode)
	}
	return atomicWrite(write.Path, write.Data)
}

func captureRelaySnapshotFiles(writes []relaySnapshotWrite) ([]relaySnapshotFile, error) {
	snapshots := make([]relaySnapshotFile, 0, len(writes))
	for _, write := range writes {
		snapshot := relaySnapshotFile{Path: write.Path}
		info, err := os.Lstat(write.Path)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, snapshot)
			continue
		}
		if err != nil {
			return nil, err
		}
		snapshot.Exists = true
		snapshot.Mode = info.Mode()
		if info.Mode()&os.ModeSymlink != 0 {
			snapshot.IsSymlink = true
			snapshot.LinkTarget, err = os.Readlink(write.Path)
			if err != nil {
				return nil, err
			}
		} else {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%s 不是普通文件，拒绝切换中转配置", write.Path)
			}
			snapshot.Data, err = os.ReadFile(write.Path)
			if err != nil {
				return nil, err
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func restoreRelaySnapshotFiles(snapshots []relaySnapshotFile) error {
	var restoreErrors []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		if !snapshot.Exists {
			if err := os.Remove(snapshot.Path); err != nil && !os.IsNotExist(err) {
				restoreErrors = append(restoreErrors, err)
			}
			continue
		}
		if snapshot.IsSymlink {
			if err := os.Remove(snapshot.Path); err != nil && !os.IsNotExist(err) {
				restoreErrors = append(restoreErrors, err)
				continue
			}
			if err := os.Symlink(snapshot.LinkTarget, snapshot.Path); err != nil {
				restoreErrors = append(restoreErrors, err)
			}
			continue
		}
		if err := atomicWriteWithMode(snapshot.Path, snapshot.Data, snapshot.Mode); err != nil {
			restoreErrors = append(restoreErrors, err)
		}
	}
	return errors.Join(restoreErrors...)
}

func relayAppliedModeMarker(contents string) string {
	contents = strings.TrimPrefix(contents, "\ufeff")
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(trimmed, relayAppliedModeMarkerPrefix) {
			continue
		}
		mode := strings.TrimSpace(strings.TrimPrefix(trimmed, relayAppliedModeMarkerPrefix))
		switch mode {
		case "official", "mixedApi", "pureApi", "aggregate":
			return mode
		default:
			return ""
		}
	}
	return ""
}

func withRelayAppliedModeMarker(contents, mode string) string {
	switch mode {
	case "official", "mixedApi", "pureApi", "aggregate":
	default:
		mode = "official"
	}

	bom := ""
	if strings.HasPrefix(contents, "\ufeff") {
		bom = "\ufeff"
		contents = strings.TrimPrefix(contents, "\ufeff")
	}
	newline := "\n"
	if strings.Contains(contents, "\r\n") {
		newline = "\r\n"
	}

	var body strings.Builder
	for _, line := range strings.SplitAfter(contents, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(trimmed, relayAppliedModeMarkerPrefix) {
			continue
		}
		body.WriteString(line)
	}
	return bom + relayAppliedModeMarkerPrefix + mode + newline + body.String()
}

func (s *server) saveRelayFile(args map[string]any) commandResult {
	request := mapArg(args, "request")
	kind := stringArg(request, "kind")
	contents := stringArg(request, "contents")
	home := codexHomeDir()
	var path string
	switch kind {
	case "config":
		path = filepath.Join(home, "config.toml")
	case "auth":
		path = filepath.Join(home, "auth.json")
	default:
		return failed("保存配置文件失败：未知配置文件类型："+kind, relayFilesPayload(home))
	}
	if kind == "config" {
		releaseLock, err := acquireProviderSyncMutationGuards(home, "manual-config-save")
		if err != nil {
			return failed("保存 config.toml 失败：另一个模式切换或历史同步正在运行："+err.Error(), relayFilesPayload(home))
		}
		defer releaseLock()
		if err := ensureProviderSyncWritersStopped(); err != nil {
			return failed("保存 config.toml 失败："+err.Error(), relayFilesPayload(home))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failed("保存配置文件失败："+err.Error(), relayFilesPayload(home))
	}
	var backupPath *string
	var err error
	if kind == "config" {
		backupPath, err = writeCodexConfigWithBackup(path, contents, "manual-save")
	} else {
		err = os.WriteFile(path, []byte(contents), 0o644)
	}
	if err != nil {
		payload := relayFilesPayload(home)
		payload["backupPath"] = nullableStringPtr(backupPath)
		return failed("保存配置文件失败："+err.Error(), payload)
	}
	payload := relayFilesPayload(home)
	payload["backupPath"] = nullableStringPtr(backupPath)
	return ok("配置文件已保存。", payload)
}

func (s *server) importCurrentRelayFiles(args map[string]any) commandResult {
	profileID := relayProfileIDArg(args)
	settings := loadSettings()
	if profileID == "" {
		profileID = activeRelayProfile(settings).ID
	}
	snapshot := currentRelayFileSnapshot(codexHomeDir())
	updated, found := updateRelayProfileSnapshot(settings, profileID, snapshot.ConfigContents, snapshot.AuthContents)
	if !found {
		return failed("导入当前环境失败：未找到供应商。", settingsPayloadValue(settings))
	}
	if err := saveSettings(updated); err != nil {
		return failed("导入当前环境失败："+err.Error(), settingsPayloadValue(settings))
	}
	return ok("已把当前 ~/.codex/config.toml 和 auth.json 导入到此供应商。", settingsPayloadValue(loadSettings()))
}

func (s *server) backfillRelayProfileFromLive(args map[string]any) commandResult {
	request := mapArg(args, "request")
	var settings backendSettings
	if err := remarshal(request["settings"], &settings); err != nil {
		return failed("回填当前供应商配置失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	settings = normalizeSettings(settings)
	profileID := strings.TrimSpace(stringFromAny(request["profileId"]))
	if profileID == "" {
		profileID = activeRelayProfile(settings).ID
	}
	configContents := readFile(filepath.Join(codexHomeDir(), "config.toml"))
	authContents := readFile(filepath.Join(codexHomeDir(), "auth.json"))
	updated, found := updateRelayProfileSnapshot(settings, profileID, configContents, authContents)
	if !found {
		return failed("当前供应商已不在配置列表中，已停止切换以避免覆盖用户改动。", map[string]any{"settings": settings})
	}
	payload := map[string]any{"settings": normalizeSettings(updated)}
	return ok("当前供应商配置已从 live 文件回填。", payload)
}

func (s *server) bindOfficialAuth(args map[string]any) commandResult {
	profileID := relayProfileIDArg(args)
	settings := loadSettings()
	if profileID == "" {
		profileID = activeRelayProfile(settings).ID
	}
	snapshot, snapshotOK := currentOfficialAuthSnapshot(codexHomeDir())
	if !snapshotOK {
		return failed("未检测到当前 ChatGPT 官方登录，无法绑定到供应商。", settingsPayloadValue(settings))
	}
	updated, found := updateRelayProfileOfficialAuth(settings, profileID, snapshot)
	if !found {
		return failed("绑定官方账号失败：未找到供应商。", settingsPayloadValue(settings))
	}
	if err := saveSettings(updated); err != nil {
		return failed("绑定官方账号失败："+err.Error(), settingsPayloadValue(settings))
	}
	label := snapshot.AccountLabel
	if label == "" {
		label = "已检测"
	}
	return ok("已将当前官方账号绑定到供应商："+label, settingsPayloadValue(loadSettings()))
}

func (s *server) activateOfficialAuth(args map[string]any) commandResult {
	profileID := relayProfileIDArg(args)
	settings := loadSettings()
	relay := activeRelayProfile(settings)
	if profileID != "" {
		found := false
		for _, profile := range settings.RelayProfiles {
			if profile.ID == profileID {
				relay = profile
				found = true
				break
			}
		}
		if !found {
			return failed("绑定官方账号失败：未找到供应商。", relayStatusFromHome(codexHomeDir(), settings))
		}
	}
	relay = promoteLegacyOfficialAuth(relay)
	if err := persistRelayProfileSnapshot(settings, relay); err != nil {
		return failed("绑定官方账号失败："+err.Error(), relayStatusFromHome(codexHomeDir(), settings))
	}
	if err := writeOfficialAuthForRelay(codexHomeDir(), relay); err != nil {
		return failed("绑定官方账号失败："+err.Error(), relayStatusFromHome(codexHomeDir(), settings))
	}
	return ok("已将此供应商绑定的官方账号写入当前登录："+relayDisplayOfficialAuthLabel(relay), relayStatusFromHome(codexHomeDir(), settings))
}

func (s *server) unbindOfficialAuth(args map[string]any) commandResult {
	profileID := relayProfileIDArg(args)
	settings := loadSettings()
	if profileID == "" {
		profileID = activeRelayProfile(settings).ID
	}
	updated, found := clearRelayProfileOfficialAuth(settings, profileID)
	if !found {
		return failed("解除官方账号绑定失败：未找到供应商。", settingsPayloadValue(settings))
	}
	if err := saveSettings(updated); err != nil {
		return failed("解除官方账号绑定失败："+err.Error(), settingsPayloadValue(settings))
	}
	return ok("已解除此供应商的官方账号绑定。", settingsPayloadValue(loadSettings()))
}

func (s *server) clearCurrentOfficialAuth() commandResult {
	home := codexHomeDir()
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		payload := relayStatusFromHome(home)
		payload["backupPath"] = nil
		return ok("当前没有可清除的官方登录文件。", payload)
	}
	if !officialAuthStatusFromContents(string(data), path).Authenticated {
		payload := relayStatusFromHome(home)
		payload["backupPath"] = nil
		return failed("当前 auth.json 不是 ChatGPT 官方登录，为避免误删已停止清除。", payload)
	}
	backupDir := filepath.Join(stateDir(), "official-auth-backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return failed("备份官方登录失败："+err.Error(), relayStatusFromHome(home))
	}
	backupPath := filepath.Join(backupDir, "auth-"+time.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return failed("备份官方登录失败："+err.Error(), relayStatusFromHome(home))
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return failed("清除当前官方登录失败："+err.Error(), relayStatusFromHome(home))
	}
	payload := relayStatusFromHome(home)
	payload["backupPath"] = backupPath
	return ok("已备份并清除当前官方登录；现在可以在 ChatGPT 中登录另一个账号。", payload)
}

func relayProfileIDArg(args map[string]any) string {
	request := mapArg(args, "request")
	if id := stringArg(request, "profileId"); id != "" {
		return id
	}
	return stringArg(args, "profileId")
}

func updateRelayProfileSnapshot(settings backendSettings, profileID, configContents, authContents string) (backendSettings, bool) {
	found := false
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID != profileID {
			continue
		}
		settings.RelayProfiles[index].ConfigContents = configContents
		settings.RelayProfiles[index].AuthContents = authContents
		settings.RelayProfiles[index].OfficialAuthUpdatedAt = time.Now().Format(time.RFC3339)
		settings.RelayProfiles[index] = syncOfficialAuthMetadataFromAuth(settings.RelayProfiles[index])
		found = true
		break
	}
	return normalizeSettings(settings), found
}

func updateRelayProfileOfficialAuth(settings backendSettings, profileID string, snapshot officialAuthSnapshot) (backendSettings, bool) {
	found := false
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID != profileID {
			continue
		}
		settings.RelayProfiles[index].AuthContents = snapshot.Contents
		settings.RelayProfiles[index].OfficialAuthContents = snapshot.Contents
		settings.RelayProfiles[index].OfficialAccountLabel = snapshot.AccountLabel
		settings.RelayProfiles[index].OfficialAuthUpdatedAt = snapshot.UpdatedAt
		found = true
		break
	}
	return normalizeSettings(settings), found
}

func clearRelayProfileOfficialAuth(settings backendSettings, profileID string) (backendSettings, bool) {
	found := false
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID != profileID {
			continue
		}
		settings.RelayProfiles[index].AuthContents = ""
		settings.RelayProfiles[index].OfficialAuthContents = ""
		settings.RelayProfiles[index].OfficialAccountLabel = ""
		settings.RelayProfiles[index].OfficialAuthUpdatedAt = ""
		found = true
		break
	}
	return normalizeSettings(settings), found
}

func persistRelayProfileSnapshot(settings backendSettings, relay relayProfile) error {
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID != relay.ID {
			continue
		}
		if valuesJSONEqual(settings.RelayProfiles[index], relay) {
			return nil
		}
		settings.RelayProfiles[index] = relay
		return saveSettings(settings)
	}
	return nil
}

func (s *server) applyRelayInjection(pure bool) commandResult {
	home := codexHomeDir()
	settings := loadSettings()
	relay := ensureRelaySnapshot(activeRelayProfile(settings), readFile(filepath.Join(home, "config.toml")), !pure)
	if !pure {
		relay = refreshRelayOfficialAuthFromCurrent(home, relay)
	}
	if !pure && relay.RelayMode == "mixedApi" && !officialAuthStatusFromContents(canonicalAuthContents(relay), "settings:"+relay.ID).Authenticated {
		return failed("切换官方混合 API 失败：此供应商尚未保存 auth.json 快照。", relayStatusFromHome(home))
	}
	if relay.RelayMode == "aggregate" {
		if _, err := selectRelayForProbe(settings); err != nil {
			return failed("切换聚合供应商失败："+err.Error(), relayStatusFromHome(home))
		}
	}
	if err := persistRelayProfileSnapshot(settings, relay); err != nil {
		return failed("保存供应商快照失败："+err.Error(), relayStatusFromHome(home))
	}
	settings = loadSettings()
	relay = activeRelayProfile(settings)
	releaseLock, lockErr := acquireProviderSyncMutationGuards(home, "relay-mode-switch")
	if lockErr != nil {
		return failed("切换模式失败：另一个模式切换或历史同步正在运行："+lockErr.Error(), relayStatusFromHome(home))
	}
	defer releaseLock()
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return failed("切换模式失败："+err.Error(), relayStatusFromHome(home))
	}
	backupPath, err := writeRelaySnapshot(home, settings, relay, pure)
	if err != nil {
		if pure {
			return failed("写入中转 API 模式失败："+err.Error(), relayStatusFromHome(home))
		}
		return failed("写入中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	repairResult := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true, RefreshMarketplaces: true})
	payload := relayStatusFromHome(home)
	payload["backupPath"] = nullableStringPtr(backupPath)
	payload["configApplied"] = true
	payload["pluginRepair"] = relayPluginRepairPayload(repairResult)
	syncResult := runProviderSyncLocked(home, "")
	payload["providerSync"] = relayProviderSyncPayload(syncResult)
	if repairResult.Status == "failed" || syncResult.Status == "failed" {
		payload["maintenanceStatus"] = "failed"
		modeLabel := "中转配置"
		if pure {
			modeLabel = "中转 API 模式"
		}
		return warning(modeLabel+"已写入，但后续维护未完成："+relaySwitchRepairMessage(repairResult, syncResult), payload)
	}
	payload["maintenanceStatus"] = "ok"
	if pure {
		return ok("中转 API 模式已写入：config.toml 使用当前供应商快照，auth.json 保留当前 ChatGPT 登录；"+relaySwitchRepairMessage(repairResult, syncResult), payload)
	}
	return ok("中转配置已按当前供应商快照写入；"+relaySwitchRepairMessage(repairResult, syncResult), payload)
}

func refreshRelayOfficialAuthFromCurrent(home string, relay relayProfile) relayProfile {
	snapshot, ok := currentOfficialAuthSnapshot(home)
	if !ok {
		return relay
	}
	currentLabel := strings.TrimSpace(snapshot.AccountLabel)
	storedLabel := strings.TrimSpace(relay.OfficialAccountLabel)
	if storedLabel == "" {
		status := officialAuthStatusFromContents(runtimeAuthContents(relay), "settings:"+relay.ID)
		storedLabel = strings.TrimSpace(status.AccountLabel)
	}
	if storedLabel != "" && currentLabel != "" && !strings.EqualFold(storedLabel, currentLabel) {
		return relay
	}
	relay.AuthContents = snapshot.Contents
	relay.OfficialAuthContents = snapshot.Contents
	relay.OfficialAccountLabel = snapshot.AccountLabel
	relay.OfficialAuthUpdatedAt = snapshot.UpdatedAt
	return relay
}

func relayPluginRepairPayload(result codexConfigRepairResult) map[string]any {
	return map[string]any{
		"status":                    result.Status,
		"message":                   result.Message,
		"pluginCount":               result.PluginCount,
		"marketplaceCount":          result.MarketplaceCount,
		"mcpServerCount":            result.MCPServerCount,
		"backupPath":                result.BackupPath,
		"marketplaceRefreshStatus":  result.MarketplaceRefreshStatus,
		"marketplaceRefreshSummary": result.MarketplaceRefreshSummary,
		"marketplaceRefreshError":   result.MarketplaceRefreshError,
	}
}

func relayProviderSyncPayload(result providerSyncResult) map[string]any {
	return map[string]any{
		"status":              result.Status,
		"message":             result.Message,
		"targetProvider":      result.TargetProvider,
		"backupDir":           result.BackupDir,
		"changedSessionFiles": result.ChangedSessionFiles,
		"sqliteRowsUpdated":   result.SQLiteRowsUpdated,
		"partial":             result.Partial,
		"rollbackStatus":      result.RollbackStatus,
	}
}

func relayPluginRepairMessage(result codexConfigRepairResult) string {
	message := strings.TrimSpace(result.Message)
	if message == "" {
		return "插件配置已恢复，Codex 插件市场已刷新/重读。"
	}
	return message
}

func relaySwitchRepairMessage(repairResult codexConfigRepairResult, syncResult providerSyncResult) string {
	messages := []string{relayPluginRepairMessage(repairResult)}
	if syncResult.Status == "synced" && (syncResult.ChangedSessionFiles > 0 || syncResult.SQLiteRowsUpdated > 0) {
		messages = append(messages, fmt.Sprintf("历史会话已同步：%d 个会话文件，%d 行索引。", syncResult.ChangedSessionFiles, syncResult.SQLiteRowsUpdated))
	}
	if syncResult.Status == "skipped" {
		messages = append(messages, "历史会话同步暂未执行且未修改聊天记录，可重试："+syncResult.Message)
	}
	if syncResult.Status == "failed" {
		message := "历史会话同步失败，未完成同步：" + syncResult.Message
		if syncResult.Partial {
			message = "历史会话同步失败且回滚失败，可能处于部分同步状态：" + syncResult.Message
		} else if syncResult.RollbackStatus == "rolled_back" {
			message = "历史会话同步失败，已回滚本次历史改动：" + syncResult.Message
		}
		if syncResult.BackupDir != nil && strings.TrimSpace(*syncResult.BackupDir) != "" {
			message += " 备份：" + strings.TrimSpace(*syncResult.BackupDir)
		}
		messages = append(messages, message)
	}
	return strings.Join(messages, " ")
}

func activeRelayProfile(settings backendSettings) relayProfile {
	for _, profile := range settings.RelayProfiles {
		if profile.ID == settings.ActiveRelayID {
			return profile
		}
	}
	if len(settings.RelayProfiles) > 0 {
		return settings.RelayProfiles[0]
	}
	return defaultRelayProfile()
}

func activeAggregateRelayProfile(settings backendSettings) (aggregateRelayProfile, bool) {
	active := activeRelayProfile(settings)
	if active.RelayMode != "aggregate" {
		return aggregateRelayProfile{}, false
	}
	activeID := strings.TrimSpace(settings.ActiveAggregateRelayID)
	if activeID == "" {
		activeID = active.ID
	}
	if activeID != active.ID {
		return aggregateRelayProfile{}, false
	}
	for _, aggregate := range settings.AggregateRelayProfiles {
		if aggregate.ID == activeID {
			return normalizeAggregateRelayProfile(aggregate, 0), true
		}
	}
	return aggregateRelayProfile{}, false
}

func activeRelayUsesProtocolProxy(settings backendSettings) bool {
	active := activeRelayProfile(settings)
	return active.Protocol == "chatCompletions" || active.RelayMode == "aggregate" || hasRelayModelRoutes(active)
}

func relayConfigForWrite(settings backendSettings, relay relayProfile) (string, error) {
	configContents := strings.TrimSpace(relay.ConfigContents)
	if relay.RelayMode == "mixedApi" || relay.RelayMode == "aggregate" {
		bearerToken := strings.TrimSpace(relay.APIKey)
		if relay.RelayMode == "aggregate" {
			bearerToken = aggregateRelayAPIKey
		}
		configContents = upsertModelProviderConfig(configContents, effectiveBaseURL(relay), bearerToken, relay)
	}
	if configContents == "" && relay.RelayMode != "official" {
		configContents = upsertModelProviderConfig("", effectiveBaseURL(relay), strings.TrimSpace(relay.APIKey), relay)
	}
	if configContents == "" && relay.RelayMode != "official" {
		return "", errors.New("config.toml 内容为空")
	}
	return relayConfigWithCommonAndLimits(settings, relay, configContents)
}

func relayConfigWithCommonAndLimits(settings backendSettings, relay relayProfile, configContents string) (string, error) {
	profileConfig, _ := splitContextConfigSections(configContents)
	profileConfig = applyRemoteControlOpenAIBaseURL(profileConfig, relay)
	if strings.TrimSpace(relay.ModelAutoCompact) != "" {
		if _, err := parseModelAutoCompact(relay.ModelAutoCompact); err != nil {
			return "", fmt.Errorf("model_auto_compact: %w", err)
		}
	}
	existingCatalog := strings.TrimSpace(rootKeyString(profileConfig, "model_catalog_json"))
	managedCatalog := isManagedRelayModelCatalog(existingCatalog)
	hasModelConfig := strings.TrimSpace(relay.ModelList) != "" || strings.TrimSpace(relay.ModelWindows) != "" || strings.TrimSpace(relay.ModelAutoCompact) != ""
	if hasModelConfig && existingCatalog != "" && !managedCatalog {
		entries := collectModelCatalogEntriesWithAutoCompact(relay.ModelList, relay.ModelWindows, relay.ModelAutoCompact, relay.Model)
		for _, entry := range entries {
			if entry.Window > 0 || entry.HasAutoCompactPercent {
				return "", errors.New("当前配置使用外部 model_catalog_json，无法同时应用每模型窗口或自动压缩；请先备份并切换为 CodexTools 管理的目录")
			}
		}
	}
	if hasModelConfig {
		if existingCatalog == "" || managedCatalog {
			profileConfig = upsertRootKey(profileConfig, "model_catalog_json", quoteToml(relayModelCatalogRelativePath(relay.ID)))
		}
	} else if managedCatalog {
		profileConfig = removeRootKey(profileConfig, "model_catalog_json")
	}
	if model := strings.TrimSpace(relay.Model); model != "" {
		if slug, _, ok := parseModelSuffix(model); ok {
			model = slug
		}
		profileConfig = upsertRootKey(profileConfig, "model", quoteToml(model))
	}
	profileConfig, err := applyContextLimitsToConfig(profileConfig, relay.ContextWindow, relay.AutoCompactLimit)
	if err != nil {
		return "", err
	}
	if !relay.UseCommonConfig {
		return applyOfficialRealtimeConfig(normalizeDuplicateTomlTables(profileConfig), relay), nil
	}
	commonConfig := joinConfigSectionsRootFirst(
		sanitizeCommonRelayConfig(settings.RelayCommonConfigContents),
		filterCommonConfigForSelection(settings.RelayContextConfigContents, relay.ContextSelection),
	)
	return applyOfficialRealtimeConfig(mergeRelayCommonConfig(profileConfig, commonConfig), relay), nil
}

func isManagedRelayModelCatalog(path string) bool {
	name := strings.ToLower(strings.TrimSpace(path))
	name = strings.ReplaceAll(name, `\`, "/")
	baseName := name
	if separator := strings.LastIndex(baseName, "/"); separator >= 0 {
		baseName = baseName[separator+1:]
	}
	if baseName == "cc-switch-model-catalog.json" {
		return true
	}
	if filepath.IsAbs(path) || (len(name) > 2 && name[1] == ':' && name[2] == '/') || strings.HasPrefix(name, "/") {
		return false
	}
	name = strings.TrimPrefix(name, "./")
	if name == "codex-models.json" {
		return true
	}
	if !strings.HasPrefix(name, "model-catalogs/") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) != name {
		return false
	}
	parts := strings.Split(name, "/")
	return len(parts) == 2 && parts[1] != "" && strings.HasSuffix(parts[1], ".json")
}

func relayModelCatalogRelativePath(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = "default"
	}
	hash := sha256.Sum256([]byte(profileID))
	fileID := sanitizeID(profileID)
	if len(fileID) > 48 {
		fileID = strings.Trim(fileID[:48], "-")
	}
	return fmt.Sprintf("model-catalogs/%s-%x.json", fileID, hash[:4])
}

func mergeRelayCommonConfig(profileConfig, commonConfig string) string {
	profileGoals, profileHasGoals := tableValues(profileConfig, "features")["goals"]
	merged := joinConfigSectionsRootFirst(profileConfig, commonConfig)
	commonFeatures := tableValues(commonConfig, "features")
	featureKeys := make([]string, 0, len(commonFeatures))
	for key := range commonFeatures {
		featureKeys = append(featureKeys, key)
	}
	sort.Strings(featureKeys)
	for _, key := range featureKeys {
		merged = upsertTableKey(merged, "features", key, commonFeatures[key])
	}
	if profileHasGoals {
		merged = upsertTableKey(merged, "features", "goals", profileGoals)
	}
	return normalizeDuplicateTomlTables(merged)
}

func applyOfficialRealtimeConfig(contents string, relay relayProfile) string {
	contents = removeRootKey(contents, "experimental_realtime_webrtc_call_base_url")
	contents = removeRootKey(contents, "experimental_realtime_ws_base_url")
	if relay.RelayMode == "official" {
		bom, config := splitTomlBOM(contents)
		return bom + normalizeConfigText(config)
	}
	contents = upsertRootKey(contents, "experimental_realtime_webrtc_call_base_url", quoteToml(officialRealtimeLocalBaseURL))
	return upsertRootKey(contents, "experimental_realtime_ws_base_url", quoteToml(officialRealtimeLocalBaseURL))
}

// Codex's Remote Control session starts on the official provider and then
// follows openai_base_url for the model request. In official-mix mode point
// that one transport at our local relay; preserve an explicit user value.
func applyRemoteControlOpenAIBaseURL(contents string, relay relayProfile) string {
	managed := fmt.Sprintf("http://127.0.0.1:%d/v1", localRelayProxyPort)
	current := strings.TrimSpace(rootKeyString(contents, "openai_base_url"))
	enabled := relay.RelayMode == "mixedApi" || (relay.RelayMode == "official" && relay.OfficialMixAPIKey)
	if enabled {
		if current == "" || current == managed {
			return upsertRootKey(contents, "openai_base_url", quoteToml(managed))
		}
		return contents
	}
	if current == managed {
		return removeRootKey(contents, "openai_base_url")
	}
	return contents
}

func applyContextLimitsToConfig(configContents, contextWindow, autoCompactLimit string) (string, error) {
	var err error
	if strings.TrimSpace(contextWindow) != "" {
		if _, err = parsePositiveUintString(contextWindow, "上下文大小"); err != nil {
			return "", err
		}
		configContents = upsertRootKey(configContents, "model_context_window", strings.TrimSpace(contextWindow))
	}
	if strings.TrimSpace(autoCompactLimit) != "" {
		if _, err = parsePositiveUintString(autoCompactLimit, "自动压缩限制"); err != nil {
			return "", err
		}
		configContents = upsertRootKey(configContents, "model_auto_compact_token_limit", strings.TrimSpace(autoCompactLimit))
	}
	return configContents, nil
}

func parsePositiveUintString(value, label string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s必须是正整数", label)
	}
	return parsed, nil
}

func writeRelayModelCatalog(home string, relay relayProfile) error {
	data, shouldWrite, err := relayModelCatalogData(relay)
	if err != nil || !shouldWrite {
		return err
	}
	return atomicWrite(filepath.Join(home, filepath.FromSlash(relayModelCatalogRelativePath(relay.ID))), data)
}

func relayModelCatalogData(relay relayProfile) ([]byte, bool, error) {
	entries := collectModelCatalogEntriesWithAutoCompact(relay.ModelList, relay.ModelWindows, relay.ModelAutoCompact, relay.Model)
	if len(entries) == 0 {
		return nil, false, nil
	}
	if _, err := parseModelAutoCompact(relay.ModelAutoCompact); err != nil {
		return nil, false, fmt.Errorf("model_auto_compact: %w", err)
	}
	fallbackWindow := uint64(0)
	var err error
	if strings.TrimSpace(relay.ContextWindow) != "" {
		fallbackWindow, err = parsePositiveUintString(relay.ContextWindow, "上下文大小")
		if err != nil {
			return nil, false, err
		}
	}
	items := make([]map[string]any, 0, len(entries))
	for index, entry := range entries {
		item := bundledModelCatalogEntry(entry.Slug)
		if item == nil {
			item = map[string]any{}
		}
		item["slug"] = entry.Slug
		if _, ok := item["display_name"]; !ok {
			item["display_name"] = entry.DisplayName
		}
		if _, ok := item["description"]; !ok {
			item["description"] = entry.DisplayName
		}
		for key, value := range map[string]any{
			"supported_in_api":                 true,
			"visibility":                       "list",
			"priority":                         1000 + index,
			"effective_context_window_percent": 100,
			"auto_compact_token_limit":         nil,
		} {
			item[key] = value
		}
		metadataWindow := uint64FromAny(item["context_window"], 0)
		metadataMaxWindow := uint64FromAny(item["max_context_window"], 0)
		contextWindow := firstPositiveUint(entry.Window, fallbackWindow, metadataWindow, 272000)
		maxContextWindow := firstPositiveUint(entry.Window, fallbackWindow, metadataMaxWindow, contextWindow)
		item["context_window"] = contextWindow
		item["max_context_window"] = maxContextWindow
		if entry.HasAutoCompactPercent {
			item["auto_compact_token_limit"] = compactPercentTokenLimit(contextWindow, entry.AutoCompactPercent)
		} else {
			item["auto_compact_token_limit"] = nil
		}
		if isGPT56Model(entry.Slug) {
			item["use_responses_lite"] = relay.Protocol == "chatCompletions"
		}
		items = append(items, item)
	}
	data, err := json.MarshalIndent(map[string]any{"models": items}, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func firstPositiveUint(values ...uint64) uint64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func compactPercentTokenLimit(contextWindow uint64, percent uint32) uint64 {
	const denominator = uint64(100_000_000)
	if contextWindow == 0 || percent == 0 || uint64(percent) > denominator {
		return 0
	}
	whole := (contextWindow / denominator) * uint64(percent)
	remainder := (contextWindow % denominator) * uint64(percent)
	return whole + (remainder+denominator/2)/denominator
}

func applyRelayConfig(home string, relay relayProfile, pure bool) error {
	if !pure && relay.RelayMode == "official" {
		return errors.New("官方登录模式不需要写入 API 配置")
	}
	baseURL := effectiveBaseURL(relay)
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("中转 Base URL 不能为空")
	}
	if strings.TrimSpace(relay.APIKey) == "" && relay.RelayMode != "aggregate" {
		return errors.New("中转 Key 不能为空")
	}
	if relay.Protocol == "responses" && relay.ImageGenerationEnabled && relay.ImageGenerationUseSeparateAPI {
		if strings.TrimSpace(relay.ImageGenerationBaseURL) == "" {
			return errors.New("图片 Base URL 不能为空")
		}
		if strings.TrimSpace(relay.ImageGenerationAPIKey) == "" {
			return errors.New("图片 Key 不能为空")
		}
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(home, "config.toml")
	existing, _ := os.ReadFile(configPath)
	settings := loadSettings()
	bearerToken := strings.TrimSpace(relay.APIKey)
	if relay.RelayMode == "aggregate" {
		bearerToken = aggregateRelayAPIKey
	}
	updated := upsertModelProviderConfig(string(existing), baseURL, bearerToken, relay)
	updated, err := relayConfigWithCommonAndLimits(settings, relay, updated)
	if err != nil {
		return err
	}
	if err := writeRelayModelCatalog(home, relay); err != nil {
		return err
	}
	_, err = writeCodexConfigWithBackup(configPath, updated, "relay-apply")
	return err
}

func effectiveBaseURL(relay relayProfile) string {
	if relay.Protocol == "chatCompletions" || relay.RelayMode == "aggregate" {
		return protocolProxyBaseURL
	}
	if relay.Protocol == "responses" && (disablesImageGeneration(relay) || usesSeparateImageGenerationAPI(relay) || relayProfileUsesHTTPProxy(relay) || hasRelayModelRoutes(relay)) {
		return fmt.Sprintf("http://127.0.0.1:%d/v1", localRelayProxyPort)
	}
	return strings.TrimSpace(relay.BaseURL)
}

func disablesImageGeneration(relay relayProfile) bool {
	return !relay.ImageGenerationEnabled
}

func usesSeparateImageGenerationAPI(relay relayProfile) bool {
	return relay.ImageGenerationEnabled && relay.ImageGenerationUseSeparateAPI && strings.TrimSpace(relay.ImageGenerationBaseURL) != "" && strings.TrimSpace(relay.ImageGenerationAPIKey) != ""
}

func upsertModelProviderConfig(contents, baseURL, bearerToken string, relay relayProfile) string {
	updated := upsertRootKey(contents, "model_provider", quoteToml(relayProvider))
	if model := strings.TrimSpace(relay.Model); model != "" {
		if slug, _, ok := parseModelSuffix(model); ok {
			model = slug
		}
		updated = upsertRootKey(updated, "model", quoteToml(model))
	}
	updated = removeTable(updated, "model_providers."+relayProvider)
	updated = removeTable(updated, "model_providers."+legacyRelayProvider)
	lines := splitLines(updated)
	insertAt := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[model_providers.") {
			insertAt = i
			break
		}
	}
	providerLines := []string{
		"[model_providers." + relayProvider + "]",
		"name = " + quoteToml(relayProvider),
		`wire_api = "responses"`,
		"requires_openai_auth = true",
		"base_url = " + quoteToml(baseURL),
	}
	if disablesImageGeneration(relay) {
		providerLines = append(providerLines, `disabled_tools = ["image_generation"]`)
	}
	if relay.Protocol == "responses" && (disablesImageGeneration(relay) || usesSeparateImageGenerationAPI(relay)) {
		providerLines = append(providerLines, "codex_plus_text_base_url = "+quoteToml(normalizeResponsesBaseURL(relay.BaseURL)))
	}
	if usesSeparateImageGenerationAPI(relay) {
		providerLines = append(providerLines, "codex_plus_image_base_url = "+quoteToml(normalizeResponsesBaseURL(relay.ImageGenerationBaseURL)))
		providerLines = append(providerLines, "# codex_plus_image_api_key is stored only in ChatGPT Codex Tools settings and used by the local relay proxy for image routes.")
	}
	providerLines = append(providerLines, "experimental_bearer_token = "+quoteToml(bearerToken), "")
	lines = append(lines[:insertAt], append(providerLines, lines[insertAt:]...)...)
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func (s *server) clearRelayInjection() commandResult {
	home := codexHomeDir()
	settings := loadSettings()
	currentConfig := readFile(filepath.Join(home, "config.toml"))
	relay := ensureRelaySnapshot(activeRelayProfile(settings), currentConfig, true)
	relay = refreshRelayOfficialAuthFromCurrent(home, relay)
	if !officialAuthStatusFromContents(canonicalAuthContents(relay), "settings:"+relay.ID).Authenticated {
		return failed("切换官方登录模式失败：此供应商尚未保存 auth.json 快照。", relayStatusFromHome(home))
	}
	if err := persistRelayProfileSnapshot(settings, relay); err != nil {
		return failed("保存供应商快照失败："+err.Error(), relayStatusFromHome(home))
	}
	settings = loadSettings()
	relay = activeRelayProfile(settings)
	releaseLock, lockErr := acquireProviderSyncMutationGuards(home, "official-mode-switch")
	if lockErr != nil {
		return failed("切换官方登录模式失败：另一个模式切换或历史同步正在运行："+lockErr.Error(), relayStatusFromHome(home))
	}
	defer releaseLock()
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return failed("切换官方登录模式失败："+err.Error(), relayStatusFromHome(home))
	}
	backupPath, err := writeRelaySnapshot(home, settings, relay, false)
	if err != nil {
		return failed("切换官方登录模式失败："+err.Error(), relayStatusFromHome(home))
	}
	payload := relayStatusFromHome(home)
	payload["backupPath"] = nullableStringPtr(backupPath)
	payload["configApplied"] = true
	repairResult := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true, RefreshMarketplaces: true})
	payload["pluginRepair"] = relayPluginRepairPayload(repairResult)
	syncResult := runProviderSyncLocked(home, "")
	payload["providerSync"] = relayProviderSyncPayload(syncResult)
	if repairResult.Status == "failed" || syncResult.Status == "failed" {
		payload["maintenanceStatus"] = "failed"
		return warning("官方登录模式已写入，但后续维护未完成："+relaySwitchRepairMessage(repairResult, syncResult), payload)
	}
	payload["maintenanceStatus"] = "ok"
	return ok("已切换到此供应商绑定的官方 ChatGPT 登录模式；"+relaySwitchRepairMessage(repairResult, syncResult), payload)
}

func writeOfficialAuthForRelay(home string, relay relayProfile) error {
	contents := runtimeAuthContents(relay)
	if contents == "" {
		return errors.New("此供应商还没有绑定官方账号，请先登录目标 ChatGPT 账号并绑定当前登录")
	}
	status := officialAuthStatusFromContents(contents, "settings:"+relay.ID)
	if !status.Authenticated {
		return errors.New("此供应商绑定的官方账号快照无效，请重新绑定")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "auth.json"), []byte(contents), 0o600)
}

func relayDisplayOfficialAuthLabel(relay relayProfile) string {
	label := strings.TrimSpace(relay.OfficialAccountLabel)
	if label == "" {
		contents := runtimeAuthContents(relay)
		status := officialAuthStatusFromContents(contents, "settings:"+relay.ID)
		label = strings.TrimSpace(status.AccountLabel)
	}
	if label == "" {
		return "已检测账号"
	}
	return label
}

func officialRelayConfigSnapshot(currentConfig string) string {
	officialConfig := removeRootKey(currentConfig, "experimental_realtime_webrtc_call_base_url")
	officialConfig = removeRootKey(officialConfig, "experimental_realtime_ws_base_url")
	officialConfig = applyRemoteControlOpenAIBaseURL(officialConfig, relayProfile{RelayMode: "official"})
	officialConfig = removeRootKey(
		removeRootKey(
			removeTable(
				removeTable(officialConfig, "model_providers."+relayProvider),
				"model_providers."+legacyRelayProvider,
			),
			"OPENAI_API_KEY",
		),
		"model_provider",
	)
	return upsertRootKey(officialConfig, "model_provider", quoteToml("openai"))
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func clearPureAPIAuth(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return
	}
	if _, ok := value["OPENAI_API_KEY"]; !ok {
		return
	}
	delete(value, "OPENAI_API_KEY")
	data, _ = json.MarshalIndent(value, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

func (s *server) testRelayProfile(ctx context.Context, args map[string]any) commandResult {
	var profile relayProfile
	if err := remarshal(args["profile"], &profile); err != nil {
		return failed("供应商参数错误："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": "", "responsePreview": ""})
	}
	settings := loadSettings()
	model := strings.TrimSpace(profile.TestModel)
	if model == "" {
		model = strings.TrimSpace(settings.RelayTestModel)
	}
	if model == "" {
		model = defaultRelayTestModel
	}
	endpoint, payload := relayTestPayload(profile, model)
	if strings.TrimSpace(profile.APIKey) == "" {
		return failed("测试「"+displayRelayName(profile)+"」失败：API Key 不能为空", map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return failed("测试「"+displayRelayName(profile)+"」失败："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+profile.APIKey)
	client, err := relayHTTPClient(profile)
	if err != nil {
		return failed("测试「"+displayRelayName(profile)+"」失败："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	resp, err := client.Do(req)
	if err != nil {
		return failed("测试「"+displayRelayName(profile)+"」失败："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	preview := string([]rune(string(text))[:minRunes(string(text), 320)])
	status := "ok"
	if resp.StatusCode >= 400 {
		status = "failed"
	}
	detail := "响应内容为空"
	if strings.TrimSpace(preview) != "" {
		detail = "响应：" + strings.TrimSpace(preview)
	}
	proxyNote := ""
	if profile.ProxyEnabled {
		if strings.TrimSpace(profile.ProxyURL) == "" {
			proxyNote = " HTTP 代理已启用但代理 URL 为空，已按直连测试。"
		} else {
			proxyNote = " 已通过 HTTP 代理测试。"
		}
	}
	return commandResult{"status": status, "message": fmt.Sprintf("已向「%s」用模型「%s」发送 hi，HTTP %d。%s%s", displayRelayName(profile), model, resp.StatusCode, detail, proxyNote), "httpStatus": resp.StatusCode, "endpoint": endpoint, "responsePreview": preview}
}

func relayTestPayload(profile relayProfile, model string) (string, map[string]any) {
	baseURL := relayProxyBaseURL(effectiveUpstreamBaseURL(profile), profile.Protocol)
	if profile.Protocol == "chatCompletions" {
		return baseURL + "/chat/completions", map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 16}
	}
	return baseURL + "/responses", map[string]any{"model": model, "input": "hi", "max_output_tokens": 16}
}

func displayRelayName(profile relayProfile) string {
	if strings.TrimSpace(profile.Name) == "" {
		return "未命名供应商"
	}
	return strings.TrimSpace(profile.Name)
}
