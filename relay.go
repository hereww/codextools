package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *server) relayStatus() commandResult {
	status := relayStatusFromHome(codexHomeDir(), loadSettings())
	message := "未检测到 ChatGPT 登录状态，请先在 Codex/ChatGPT 中正常登录。"
	if boolFromAny(status["currentAuthenticated"]) {
		message = "已检测到当前 ChatGPT 登录状态。"
	} else if boolFromAny(status["boundOfficialAuthenticated"]) {
		message = "已检测到已绑定的官方账号。"
	}
	return ok(message, status)
}

func relayStatusFromHome(home string, settingsOpt ...backendSettings) map[string]any {
	auth := chatGPTAuthStatus(home)
	config := relayConfigStatus(home)
	settings := loadSettings()
	if len(settingsOpt) > 0 {
		settings = normalizeSettings(settingsOpt[0])
	}
	bound := boundOfficialAuthStatus(settings)
	officialAuthenticated := auth.Authenticated || bound.Authenticated
	officialAccountLabel := auth.AccountLabel
	if officialAccountLabel == "" {
		officialAccountLabel = bound.AccountLabel
	}
	officialAuthSource := auth.Source
	if officialAuthSource == "" {
		officialAuthSource = bound.Source
	}
	return map[string]any{
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
	if strings.TrimSpace(profile.OfficialAuthContents) == "" {
		return boundOfficialAuthSummary{}, false
	}
	status := chatGPTAuthStatusFromContents(profile.OfficialAuthContents, "settings:"+profile.ID)
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

func chatGPTAuthStatus(home string) authStatus {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return authStatus{}
	}
	return chatGPTAuthStatusFromContents(string(data), path)
}

func chatGPTAuthStatusFromContents(contents, source string) authStatus {
	var value map[string]any
	if json.Unmarshal([]byte(contents), &value) != nil {
		return authStatus{}
	}
	if !strings.EqualFold(stringFromAny(value["auth_mode"]), "chatgpt") {
		return authStatus{}
	}
	tokens, _ := value["tokens"].(map[string]any)
	if tokens == nil || (!hasToken(tokens, "access_token") && !hasToken(tokens, "id_token") && !hasToken(tokens, "refresh_token")) {
		return authStatus{}
	}
	return authStatus{Authenticated: true, Source: source, AccountLabel: accountLabelFromTokens(tokens)}
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
	status := chatGPTAuthStatusFromContents(contents, path)
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
	return ""
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

func (s *server) saveRelayFile(args map[string]any) commandResult {
	request := mapArg(args, "request")
	kind := stringArg(request, "kind")
	contents := stringArg(request, "contents")
	var path string
	switch kind {
	case "config":
		path = filepath.Join(codexHomeDir(), "config.toml")
	case "auth":
		path = filepath.Join(codexHomeDir(), "auth.json")
	default:
		return failed("保存配置文件失败：未知配置文件类型："+kind, relayFilesPayload(codexHomeDir()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failed("保存配置文件失败："+err.Error(), relayFilesPayload(codexHomeDir()))
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return failed("保存配置文件失败："+err.Error(), relayFilesPayload(codexHomeDir()))
	}
	return ok("配置文件已保存。", relayFilesPayload(codexHomeDir()))
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
	if !chatGPTAuthStatusFromContents(string(data), path).Authenticated {
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
	return ok("已备份并清除当前官方登录；现在可以在 Codex/ChatGPT 登录另一个账号。", payload)
}

func relayProfileIDArg(args map[string]any) string {
	request := mapArg(args, "request")
	if id := stringArg(request, "profileId"); id != "" {
		return id
	}
	return stringArg(args, "profileId")
}

func updateRelayProfileOfficialAuth(settings backendSettings, profileID string, snapshot officialAuthSnapshot) (backendSettings, bool) {
	found := false
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID != profileID {
			continue
		}
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
		settings.RelayProfiles[index].OfficialAuthContents = ""
		settings.RelayProfiles[index].OfficialAccountLabel = ""
		settings.RelayProfiles[index].OfficialAuthUpdatedAt = ""
		found = true
		break
	}
	return normalizeSettings(settings), found
}

func (s *server) applyRelayInjection(pure bool) commandResult {
	home := codexHomeDir()
	settings := loadSettings()
	relay := activeRelayProfile(settings)
	useSavedFiles := strings.TrimSpace(relay.ConfigContents) != "" &&
		(pure || strings.TrimSpace(relay.AuthContents) != "" || relay.RelayMode == "mixedApi")
	if !pure && relay.RelayMode == "mixedApi" {
		if err := writeOfficialAuthForRelay(home, relay); err != nil {
			return failed("切换官方混合 API 失败："+err.Error(), relayStatusFromHome(home))
		}
	}
	if useSavedFiles {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		configContents := relay.ConfigContents
		if pure {
			configContents = ensureConfigBearerToken(configContents, strings.TrimSpace(relay.APIKey))
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configContents), 0o644); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		if !pure && strings.TrimSpace(relay.AuthContents) != "" {
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(relay.AuthContents), 0o644); err != nil {
				return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
			}
		}
		repairResult := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true})
		payload := relayStatusFromHome(home)
		payload["pluginRepair"] = map[string]any{"status": repairResult.Status, "pluginCount": repairResult.PluginCount, "marketplaceCount": repairResult.MarketplaceCount, "backupPath": repairResult.BackupPath}
		if repairResult.Status == "failed" {
			return failed("已切换完整中转配置，但插件恢复失败："+repairResult.Message, payload)
		}
		if pure {
			return ok("中转 API 模式已写入：config.toml 使用当前供应商保存配置，auth.json 保持现有登录状态，并恢复插件配置。", payload)
		}
		return ok("已切换到当前中转的完整 config.toml / auth.json，并恢复插件配置。", payload)
	}
	if err := applyRelayConfig(home, relay, pure); err != nil {
		if pure {
			return failed("写入中转 API 模式失败："+err.Error(), relayStatusFromHome(home))
		}
		return failed("写入中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	repairResult := repairCodexConfig(home, codexConfigRepairOptions{Plugins: true})
	payload := relayStatusFromHome(home)
	payload["pluginRepair"] = map[string]any{"status": repairResult.Status, "pluginCount": repairResult.PluginCount, "marketplaceCount": repairResult.MarketplaceCount, "backupPath": repairResult.BackupPath}
	if repairResult.Status == "failed" {
		if pure {
			return failed("中转 API 模式已写入，但插件恢复失败："+repairResult.Message, payload)
		}
		return failed("中转配置已写入，但插件恢复失败："+repairResult.Message, payload)
	}
	if pure {
		return ok("中转 API 模式已写入：config.toml 已写入 CodexPlusPlus provider，auth.json 保持现有登录状态，并恢复插件配置。", payload)
	}
	return ok("中转配置已写入，密钥未在界面明文显示，并恢复插件配置。", payload)
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

func applyRelayConfig(home string, relay relayProfile, pure bool) error {
	if !pure && relay.RelayMode == "official" {
		return errors.New("官方登录模式不需要写入 API 配置")
	}
	baseURL := effectiveBaseURL(relay)
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("中转 Base URL 不能为空")
	}
	if strings.TrimSpace(relay.APIKey) == "" {
		return errors.New("中转 Key 不能为空")
	}
	if relay.ImageGenerationEnabled && relay.ImageGenerationUseSeparateAPI {
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
	updated := upsertModelProviderConfig(string(existing), baseURL, strings.TrimSpace(relay.APIKey), relay)
	return os.WriteFile(configPath, []byte(updated), 0o644)
}

func effectiveBaseURL(relay relayProfile) string {
	if relay.Protocol == "chatCompletions" {
		return protocolProxyBaseURL
	}
	if relay.Protocol == "responses" && (disablesImageGeneration(relay) || usesSeparateImageGenerationAPI(relay)) {
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
		providerLines = append(providerLines, "# codex_plus_image_api_key is stored only in Codex++ settings and used by the local relay proxy for image routes.")
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
	relay := activeRelayProfile(settings)
	if err := writeOfficialAuthForRelay(home, relay); err != nil {
		return failed("切换官方登录模式失败："+err.Error(), relayStatusFromHome(home))
	}
	_ = os.MkdirAll(home, 0o755)
	configPath := filepath.Join(home, "config.toml")
	data, _ := os.ReadFile(configPath)
	updated := removeRootKey(removeRootKey(removeTable(removeTable(string(data), "model_providers."+relayProvider), "model_providers."+legacyRelayProvider), "OPENAI_API_KEY"), "model_provider")
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return failed("清除中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	return ok("已切换到此供应商绑定的官方 ChatGPT 登录模式。", relayStatusFromHome(home))
}

func writeOfficialAuthForRelay(home string, relay relayProfile) error {
	contents := strings.TrimSpace(relay.OfficialAuthContents)
	if contents == "" {
		return errors.New("此供应商还没有绑定官方账号，请先登录目标 ChatGPT 账号并绑定当前登录")
	}
	status := chatGPTAuthStatusFromContents(relay.OfficialAuthContents, "settings:"+relay.ID)
	if !status.Authenticated {
		return errors.New("此供应商绑定的官方账号快照无效，请重新绑定")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "auth.json"), []byte(relay.OfficialAuthContents), 0o600)
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
	resp, err := http.DefaultClient.Do(req)
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
	return commandResult{"status": status, "message": fmt.Sprintf("已向「%s」用模型「%s」发送 hi，HTTP %d。%s", displayRelayName(profile), model, resp.StatusCode, detail), "httpStatus": resp.StatusCode, "endpoint": endpoint, "responsePreview": preview}
}

func relayTestPayload(profile relayProfile, model string) (string, map[string]any) {
	baseURL := relayProxyBaseURL(profile.BaseURL, profile.Protocol)
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
