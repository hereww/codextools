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
)

func (s *server) relayStatus() commandResult {
	status := relayStatusFromHome(codexHomeDir())
	message := "未检测到 ChatGPT 登录状态，请先在 Codex/ChatGPT 中正常登录。"
	if boolFromAny(status["authenticated"]) {
		message = "已检测到 ChatGPT 登录状态。"
	}
	return ok(message, status)
}

func relayStatusFromHome(home string) map[string]any {
	auth := chatGPTAuthStatus(home)
	config := relayConfigStatus(home)
	return map[string]any{
		"authenticated":      auth.Authenticated,
		"authSource":         auth.Source,
		"accountLabel":       nullableString(auth.AccountLabel),
		"configPath":         config.ConfigPath,
		"configured":         config.Configured,
		"requiresOpenaiAuth": config.RequiresOpenAIAuth,
		"hasBearerToken":     config.HasBearerToken,
		"backupPath":         nil,
	}
}

func chatGPTAuthStatus(home string) authStatus {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return authStatus{}
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return authStatus{}
	}
	if !strings.EqualFold(stringFromAny(value["auth_mode"]), "chatgpt") {
		return authStatus{}
	}
	tokens, _ := value["tokens"].(map[string]any)
	if tokens == nil || (!hasToken(tokens, "access_token") && !hasToken(tokens, "id_token") && !hasToken(tokens, "refresh_token")) {
		return authStatus{}
	}
	return authStatus{Authenticated: true, Source: path, AccountLabel: accountLabelFromTokens(tokens)}
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

func (s *server) applyRelayInjection(pure bool) commandResult {
	home := codexHomeDir()
	settings := loadSettings()
	relay := activeRelayProfile(settings)
	useSavedFiles := strings.TrimSpace(relay.ConfigContents) != "" &&
		(strings.TrimSpace(relay.AuthContents) != "" || relay.RelayMode == "mixedApi")
	if !pure && relay.RelayMode == "mixedApi" && !chatGPTAuthStatus(home).Authenticated {
		return failed("未检测到 ChatGPT 登录状态，已停止写入中转配置。", relayStatusFromHome(home))
	}
	if !pure && useSavedFiles {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(relay.ConfigContents), 0o644); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		if strings.TrimSpace(relay.AuthContents) != "" {
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
		return ok("中转 API 模式已写入：auth.json 已切换为 OPENAI_API_KEY，config.toml 已写入 CodexPlusPlus provider，并恢复插件配置。", payload)
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
	if pure {
		authPayload, _ := json.MarshalIndent(map[string]string{"OPENAI_API_KEY": strings.TrimSpace(relay.APIKey)}, "", "  ")
		if err := os.WriteFile(filepath.Join(home, "auth.json"), authPayload, 0o644); err != nil {
			return err
		}
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
	_ = os.MkdirAll(home, 0o755)
	clearPureAPIAuth(filepath.Join(home, "auth.json"))
	configPath := filepath.Join(home, "config.toml")
	data, _ := os.ReadFile(configPath)
	updated := removeRootKey(removeRootKey(removeTable(removeTable(string(data), "model_providers."+relayProvider), "model_providers."+legacyRelayProvider), "OPENAI_API_KEY"), "model_provider")
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return failed("清除中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	return ok("已清除 CodexPlusPlus 中转 API 模式，并切换到官方 ChatGPT 登录模式。", relayStatusFromHome(home))
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
