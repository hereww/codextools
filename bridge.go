package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func (r *launcherRuntime) handleBridgeRequest(path string, payload json.RawMessage) map[string]any {
	started := time.Now()
	var payloadMap map[string]any
	_ = json.Unmarshal(payload, &payloadMap)
	appendDiagnosticLog("bridge.request", map[string]any{"path": path, "payload_keys": mapKeys(payloadMap)})
	var result map[string]any
	switch path {
	case "/backend/status", "/backend/repair":
		result = map[string]any{"status": "ok", "message": "后端已连接", "version": version}
	case "/settings/get":
		result = bridgeSettingsValue(loadSettings())
	case "/settings/set":
		result = r.setBridgeSettings(payloadMap)
	case "/diagnostics/log":
		r.logRendererDiagnostic(payload)
		result = map[string]any{"status": "ok", "message": "日志已记录"}
	case "/user-scripts/list":
		result = userScriptInventoryValue()
	case "/user-scripts/set-enabled":
		config := loadUserScriptConfig()
		config.Enabled = boolFromAny(payloadMap["enabled"])
		if err := saveUserScriptConfig(config); err != nil {
			result = map[string]any{"status": "failed", "message": err.Error()}
		} else {
			result = userScriptInventoryValue()
		}
	case "/user-scripts/set-script-enabled":
		key := strings.TrimSpace(stringFromAny(payloadMap["key"]))
		if key == "" {
			result = map[string]any{"status": "failed", "message": "脚本 key 不能为空"}
			break
		}
		config := loadUserScriptConfig()
		config.Scripts[key] = boolFromAny(payloadMap["enabled"])
		if err := saveUserScriptConfig(config); err != nil {
			result = map[string]any{"status": "failed", "message": err.Error()}
		} else {
			result = userScriptInventoryValue()
		}
	case "/user-scripts/reload":
		bundle := enabledUserScriptBundle()
		if strings.TrimSpace(bundle) != "" {
			if _, err := r.evaluateOnCodex(bundle, false); err != nil {
				result = map[string]any{"status": "failed", "message": err.Error()}
				break
			}
		}
		result = userScriptInventoryValue()
	case "/devtools/open":
		result = r.openDevTools()
	case "/manager/open":
		if err := openManagerApp(); err != nil {
			result = map[string]any{"status": "failed", "message": "打开管理工具失败：" + err.Error()}
		} else {
			result = map[string]any{"status": "ok", "message": "管理工具已打开"}
		}
	case "/codex-model-catalog", "/codex-config-model":
		result = codexModelCatalogValue()
	case "/zed-remote/status":
		result = zedRemoteStatusValue()
	case "/zed-remote/resolve-host", "/zed-remote/fallback-request", "/zed-remote/open":
		result = map[string]any{"status": "failed", "message": "Go 管理器暂未实现 Zed Remote 桥接"}
	case "/delete", "/undo", "/archived-thread", "/move-thread-workspace", "/thread-sort-key", "/thread-sort-keys":
		result = handleSessionDataRoute(path, payloadMap)
	case "/export-markdown":
		result = unsupportedBridgeDataRoute(path, payloadMap)
	default:
		result = map[string]any{"status": "failed", "message": "Unknown bridge path", "path": path}
		appendDiagnosticLog("bridge.unknown_path", map[string]any{"path": path})
	}
	appendDiagnosticLog("bridge.response", map[string]any{
		"path":       path,
		"elapsed_ms": time.Since(started).Milliseconds(),
		"status":     stringFromAny(result["status"]),
	})
	return result
}

func mapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bridgeSettingsValue(settings backendSettings) map[string]any {
	return map[string]any{
		"providerSyncEnabled": settings.ProviderSync,
		"enhancementsEnabled": settings.Enhancements,
		"launchMode":          settings.LaunchMode,
		"language":            settings.Language,
	}
}

func (r *launcherRuntime) setBridgeSettings(payload map[string]any) map[string]any {
	settings := loadSettings()
	if _, ok := payload["providerSyncEnabled"]; ok {
		settings.ProviderSync = boolFromAny(payload["providerSyncEnabled"])
	}
	if _, ok := payload["enhancementsEnabled"]; ok {
		settings.Enhancements = boolFromAny(payload["enhancementsEnabled"])
	}
	if value := strings.TrimSpace(stringFromAny(payload["launchMode"])); value == "patch" || value == "relay" {
		settings.LaunchMode = value
	}
	if _, ok := payload["language"]; ok {
		settings.Language = normalizeLanguage(stringFromAny(payload["language"]))
	}
	if err := saveSettings(settings); err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	r.settings = settings
	result := bridgeSettingsValue(settings)
	result["status"] = "ok"
	return result
}

func enabledUserScriptBundle() string {
	config := loadUserScriptConfig()
	if !config.Enabled {
		return ""
	}
	var parts []string
	inventory := scanUserScripts()
	for _, item := range inventory.Scripts {
		if !item.Enabled {
			continue
		}
		var dir string
		switch item.Source {
		case "builtin":
			dir = inventory.BuiltinDir
		case "user":
			dir = inventory.UserDir
		default:
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, item.Name))
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("\n;(() => {\n%s\n})();\n", string(data)))
	}
	return strings.Join(parts, "\n")
}

func unsupportedBridgeDataRoute(path string, payload map[string]any) map[string]any {
	sessionID := stringFromAny(payload["session_id"])
	if path == "/thread-sort-key" {
		return map[string]any{"status": "ok", "session_id": sessionID}
	}
	if path == "/thread-sort-keys" {
		return map[string]any{"status": "ok", "sessions": []any{}}
	}
	return map[string]any{"status": "failed", "session_id": sessionID, "message": "Go 管理器暂未实现该页面数据操作：" + path}
}

func zedRemoteStatusValue() map[string]any {
	return map[string]any{
		"status":            "ok",
		"platformSupported": runtime.GOOS == "darwin",
		"zedAppFound":       fileExists("/Applications/Zed.app"),
		"zedCliFound":       executableInPath("zed") != "",
	}
}

func executableInPath(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH") + string(os.PathListSeparator) + defaultGUIPath) {
		if candidate := filepath.Join(dir, name); fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func codexModelCatalogValue() map[string]any {
	configPath := filepath.Join(codexHomeDir(), "config.toml")
	data, _ := os.ReadFile(configPath)
	contents := string(data)
	modelProvider := rootKeyString(contents, "model_provider")
	model := rootKeyString(contents, "model")
	defaultModel := rootKeyString(contents, "default_model")
	providerValues := tableValues(contents, "model_providers."+modelProvider)
	providerName := unquoteToml(providerValues["name"])
	if providerName == "" {
		providerName = modelProvider
	}
	models := uniqueStrings([]string{model, defaultModel, defaultRelayTestModel})
	if len(models) == 0 {
		models = []string{defaultRelayTestModel}
	}
	if defaultModel == "" {
		defaultModel = firstNonEmpty(model, models[0])
	}
	return map[string]any{
		"status":         "ok",
		"model":          model,
		"default_model":  defaultModel,
		"model_provider": modelProvider,
		"provider_name":  providerName,
		"models":         models,
		"sources":        []any{},
		"responses_api":  map[string]any{"status": "unknown", "endpoint": "", "message": ""},
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (r *launcherRuntime) retryInjection(helperPort uint16) error {
	var lastErr error
	for attempt := 1; attempt <= 24; attempt++ {
		if err := r.inject(helperPort); err != nil {
			lastErr = err
			appendDiagnosticLog("inject.retry", map[string]any{"attempt": attempt, "debug_port": r.debugPort, "helper_port": helperPort, "error": err.Error()})
			time.Sleep(500 * time.Millisecond)
			continue
		}
		appendDiagnosticLog("inject.ok", map[string]any{"debug_port": r.debugPort, "helper_port": helperPort, "attempt": attempt})
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("Codex injection failed")
	}
	return lastErr
}

func (r *launcherRuntime) inject(helperPort uint16) error {
	targetCtx, targetCancel := context.WithTimeout(context.Background(), cdpConnectTimeout)
	defer targetCancel()
	targets, err := listCDPTargets(targetCtx, r.debugPort)
	if err != nil {
		return err
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return err
	}
	if target.WebSocketDebuggerURL == "" {
		return errors.New("selected CDP target has no websocket URL")
	}
	installCtx, installCancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer installCancel()
	return r.installBridge(installCtx, target.WebSocketDebuggerURL, helperPort)
}

func (r *launcherRuntime) bridgeWatchdog(helperPort uint16) {
	ticker := time.NewTicker(launcherCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		ok, err := r.bridgeHealthy()
		if err != nil {
			appendDiagnosticLog("bridge.health_error", map[string]any{"error": err.Error()})
		}
		if ok {
			continue
		}
		appendDiagnosticLog("bridge.reinject_start", map[string]any{"debug_port": r.debugPort, "helper_port": helperPort})
		if err := r.retryInjection(helperPort); err != nil {
			appendDiagnosticLog("bridge.reinject_failed", map[string]any{"error": err.Error()})
		}
	}
}

func (r *launcherRuntime) bridgeHealthy() (bool, error) {
	result, err := r.evaluateOnCodex(bridgeHealthCheckScript(), true)
	if err != nil {
		return false, err
	}
	return cdpResultBool(result), nil
}

func (r *launcherRuntime) openDevTools() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	targets, err := listCDPTargets(ctx, r.debugPort)
	if err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	if strings.TrimSpace(target.ID) == "" {
		return map[string]any{"status": "failed", "message": "CDP target id 为空"}
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/devtools/inspector.html?ws=127.0.0.1:%d/devtools/page/%s", r.debugPort, r.debugPort, target.ID)
	if err := openURL(url); err != nil {
		return map[string]any{"status": "failed", "message": "打开 DevTools 失败：" + err.Error(), "url": url}
	}
	return map[string]any{"status": "ok", "message": "DevTools 已打开", "url": url, "target_id": target.ID}
}

func (r *launcherRuntime) evaluateOnCodex(script string, awaitPromise bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targets, err := listCDPTargets(ctx, r.debugPort)
	if err != nil {
		return nil, err
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	session := newCDPSession(conn, nil)
	return session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(script, awaitPromise))
}

func listCDPTargets(ctx context.Context, debugPort uint16) ([]cdpTarget, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json", debugPort), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("CDP target list HTTP %d", resp.StatusCode)
	}
	var targets []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func pickCDPPageTarget(targets []cdpTarget) (cdpTarget, error) {
	var fallback *cdpTarget
	for i := range targets {
		target := targets[i]
		if target.WebSocketDebuggerURL == "" {
			continue
		}
		if isCodexCDPPageTarget(target) {
			return target, nil
		}
		if fallback == nil {
			fallback = &targets[i]
		}
	}
	for i := range targets {
		target := targets[i]
		if target.WebSocketDebuggerURL != "" && target.Type == "page" {
			return target, nil
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return cdpTarget{}, errors.New("未找到可注入的 Codex CDP 页面 target")
}

func isCodexCDPPageTarget(target cdpTarget) bool {
	if target.Type != "page" || target.WebSocketDebuggerURL == "" {
		return false
	}
	return strings.HasPrefix(target.URL, "app://-/") || strings.EqualFold(target.Title, "Codex")
}

func (r *launcherRuntime) installBridge(ctx context.Context, websocketURL string, helperPort uint16) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return err
	}
	handler := func(path string, payload json.RawMessage) map[string]any {
		return r.handleBridgeRequest(path, payload)
	}
	session := newCDPSession(conn, handler)
	if _, err := session.send(ctx, "Runtime.enable", map[string]any{}); err != nil {
		_ = conn.Close()
		return err
	}
	_, _ = session.send(ctx, "Runtime.removeBinding", map[string]any{"name": bridgeBindingName})
	if _, err := session.send(ctx, "Runtime.addBinding", map[string]any{"name": bridgeBindingName}); err != nil {
		_ = conn.Close()
		return err
	}
	bridge := bridgeScript(bridgeBindingName)
	if _, err := session.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": bridge}); err != nil {
		_ = conn.Close()
		return err
	}
	if _, err := session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(bridge, false)); err != nil {
		_ = conn.Close()
		return err
	}
	scripts := []string{injectionScript(helperPort)}
	if bundle := enabledUserScriptBundle(); strings.TrimSpace(bundle) != "" {
		scripts = append(scripts, bundle)
	}
	for _, script := range scripts {
		if _, err := session.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}); err != nil {
			_ = conn.Close()
			return err
		}
		if _, err := session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(script, false)); err != nil {
			_ = conn.Close()
			return err
		}
	}
	return nil
}

func runtimeEvaluateParams(script string, awaitPromise bool) map[string]any {
	return map[string]any{"expression": script, "awaitPromise": awaitPromise, "allowUnsafeEvalBlockedByCSP": true}
}

func injectionScript(helperPort uint16) string {
	helperURL := fmt.Sprintf("http://127.0.0.1:%d", helperPort)
	helperJSON, _ := json.Marshal(helperURL)
	versionJSON, _ := json.Marshal(version)
	buildJSON, _ := json.Marshal("go-20260524-1")
	return fmt.Sprintf("window.__CODEX_SESSION_DELETE_HELPER__ = %s;\nwindow.__CODEX_PLUS_VERSION__ = %s;\nwindow.__CODEX_PLUS_BUILD__ = %s;\n%s", helperJSON, versionJSON, buildJSON, rendererInjectScript)
}

func bridgeScript(bindingName string) string {
	return fmt.Sprintf(`
(() => {
  window.__codexSessionDeleteCallbacks = new Map();
  window.__codexSessionDeleteSeq = 0;
  window.__codexSessionDeleteResolve = (id, result) => {
    const callback = window.__codexSessionDeleteCallbacks.get(id);
    if (!callback) return;
    window.__codexSessionDeleteCallbacks.delete(id);
    callback.resolve(result);
  };
  window.__codexSessionDeleteReject = (id, message) => {
    const callback = window.__codexSessionDeleteCallbacks.get(id);
    if (!callback) return;
    window.__codexSessionDeleteCallbacks.delete(id);
    callback.resolve({ status: "failed", message });
  };
  window.__codexSessionDeleteBridge = (path, payload) => new Promise((resolve) => {
    const id = String(++window.__codexSessionDeleteSeq);
    window.__codexSessionDeleteCallbacks.set(id, { resolve });
    window.%s(JSON.stringify({ id, path, payload }));
  });
})();
`, bindingName)
}

func bridgeHealthCheckScript() string {
	return `
(() => {
  const bridge = window.__codexSessionDeleteBridge;
  if (typeof bridge !== "function") return false;
  try {
    return Promise.race([
      Promise.resolve(bridge("/backend/status", {})).then((result) => !!result && result.status === "ok"),
      new Promise((resolve) => setTimeout(() => resolve(false), 2000)),
    ]);
  } catch (error) {
    return false;
  }
})()
`
}

func cdpResultBool(result json.RawMessage) bool {
	var envelope struct {
		Result struct {
			Type  string `json:"type"`
			Value bool   `json:"value"`
		} `json:"result"`
	}
	return json.Unmarshal(result, &envelope) == nil && envelope.Result.Value
}

func newCDPSession(conn *websocket.Conn, handler func(string, json.RawMessage) map[string]any) *cdpSession {
	session := &cdpSession{
		conn:    conn,
		handler: handler,
		nextID:  1,
		pending: map[int64]chan cdpResponse{},
	}
	go session.readLoop()
	return session
}

func (s *cdpSession) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	responseCh := make(chan cdpResponse, 1)
	s.pending[id] = responseCh
	s.mu.Unlock()
	payload := map[string]any{"id": id, "method": method, "params": params}
	s.writeMu.Lock()
	if err := s.conn.WriteJSON(payload); err != nil {
		s.writeMu.Unlock()
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	s.writeMu.Unlock()
	timer := time.NewTimer(cdpCommandTimeout)
	defer timer.Stop()
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return nil, fmt.Errorf("CDP command %s failed: %s", method, response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-timer.C:
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("timed out waiting for CDP command %s", method)
	}
}

func (s *cdpSession) readLoop() {
	defer s.conn.Close()
	for {
		var response cdpResponse
		if err := s.conn.ReadJSON(&response); err != nil {
			return
		}
		if response.ID != 0 {
			s.mu.Lock()
			if ch, ok := s.pending[response.ID]; ok {
				delete(s.pending, response.ID)
				s.mu.Unlock()
				ch <- response
			} else {
				s.mu.Unlock()
			}
			continue
		}
		if response.Method == "Runtime.bindingCalled" && s.handler != nil {
			go s.handleBinding(response.Params)
			continue
		}
	}
}

func (s *cdpSession) handleBinding(params json.RawMessage) {
	var raw struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		appendDiagnosticLog("bridge.payload_parse_failed", map[string]any{"error": err.Error()})
		return
	}
	var payload bridgePayload
	if err := json.Unmarshal([]byte(raw.Payload), &payload); err != nil {
		appendDiagnosticLog("bridge.payload_parse_failed", map[string]any{"error": err.Error()})
		return
	}
	result := s.handler(payload.Path, payload.Payload)
	expr := bridgeResolveExpression(payload.ID, result)
	ctx, cancel := context.WithTimeout(context.Background(), cdpCommandTimeout)
	defer cancel()
	if _, err := s.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(expr, false)); err != nil {
		appendDiagnosticLog("bridge.resolve_failed", map[string]any{"request_id": payload.ID, "error": err.Error()})
	}
}

func bridgeResolveExpression(requestID string, result map[string]any) string {
	id, _ := json.Marshal(requestID)
	value, _ := json.Marshal(result)
	return fmt.Sprintf("window.__codexSessionDeleteResolve(%s, %s)", id, value)
}
