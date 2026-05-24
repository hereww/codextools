package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func (r relayProfile) needsLocalRelayProxy() bool {
	return r.Protocol == "responses" && (disablesImageGeneration(r) || usesSeparateImageGenerationAPI(r))
}

func (r *launcherRuntime) startHelper(helperPort uint16) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handleHelperHTTP)
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", helperPort), Handler: mux}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	r.helper = server
	r.helperURL = "http://" + server.Addr
	appendDiagnosticLog("helper.listening", map[string]any{"helper_port": helperPort, "address": r.helperURL})
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appendDiagnosticLog("helper.failed", map[string]any{"helper_port": helperPort, "error": err.Error()})
		}
	}()
	return nil
}

func (r *launcherRuntime) shutdownHelper() {
	if r.helper == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.helper.Shutdown(ctx)
	appendDiagnosticLog("helper.shutdown", map[string]any{"address": r.helperURL})
}

func (r *launcherRuntime) startRelayProxy(port uint16) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions {
			writeCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
		_ = req.Body.Close()
		r.forwardRelayProxy(w, req, body)
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	r.relay = server
	r.relayURL = "http://" + server.Addr
	appendDiagnosticLog("relay_proxy.listening", map[string]any{"port": port, "address": r.relayURL})
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appendDiagnosticLog("relay_proxy.failed", map[string]any{"port": port, "error": err.Error()})
		}
	}()
	return nil
}

func (r *launcherRuntime) shutdownRelayProxy() {
	if r.relay == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.relay.Shutdown(ctx)
	appendDiagnosticLog("relay_proxy.shutdown", map[string]any{"address": r.relayURL})
}

func (r *launcherRuntime) handleHelperHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		writeCORSHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
	_ = req.Body.Close()
	appendDiagnosticLog("helper.request", map[string]any{
		"method":     req.Method,
		"path":       req.URL.Path,
		"body_bytes": len(body),
		"remote":     req.RemoteAddr,
	})
	switch req.URL.Path {
	case "/backend/status", "/backend/repair":
		writeHelperJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "后端已连接", "version": version, "transport": "http-helper"})
	case "/diagnostics/log":
		payload := json.RawMessage(body)
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		r.logRendererDiagnostic(payload)
		writeHelperJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "日志已记录"})
	case "/v1/responses", "/responses", "/v1/responses/compact", "/responses/compact", "/v1/models", "/models":
		r.forwardRelayProxy(w, req, body)
	default:
		writeHelperJSON(w, http.StatusNotFound, map[string]any{"status": "failed", "message": "未知后端路径"})
	}
}

func (r *launcherRuntime) forwardRelayProxy(w http.ResponseWriter, req *http.Request, body []byte) {
	profile := activeRelayProfile(r.settings)
	baseURL := strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
	apiKey := strings.TrimSpace(profile.APIKey)
	decision := relayRouteDecision{body: body, route: "text", reason: "default_text"}
	if profile.Protocol == "responses" && profile.needsLocalRelayProxy() {
		decision = decideRelayRoute(body, profile)
		body = decision.body
		if decision.useImageAPI && usesSeparateImageGenerationAPI(profile) {
			baseURL = strings.TrimRight(strings.TrimSpace(profile.ImageGenerationBaseURL), "/")
			apiKey = strings.TrimSpace(profile.ImageGenerationAPIKey)
			decision.keySource = "image"
		} else {
			decision.keySource = "default"
		}
	}
	if baseURL == "" || apiKey == "" {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "Codex++ relay proxy missing base URL or API key"}})
		return
	}
	target := relayTargetURL(baseURL, req.URL.Path)
	ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
	defer cancel()
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	upstreamReq.Header.Set("authorization", "Bearer "+apiKey)
	upstreamReq.Header.Set("user-agent", "CodexPlusPlus-GoRelay/"+version)
	copyProxyHeaders(req.Header, upstreamReq.Header)
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "Codex++ relay proxy request failed: " + err.Error()}})
		return
	}
	defer resp.Body.Close()
	writeCORSHeaders(w)
	for _, name := range []string{"content-type", "cache-control", "openai-request-id", "x-request-id"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	appendDiagnosticLog("relay_proxy.response", map[string]any{
		"path":                req.URL.Path,
		"status":              resp.StatusCode,
		"target":              target,
		"route":               decision.route,
		"reason":              decision.reason,
		"key_source":          decision.keySource,
		"stripped_image_tool": decision.strippedImageTool,
	})
}

func relayTargetURL(baseURL, path string) string {
	path = "/" + strings.TrimLeft(path, "/")
	switch {
	case strings.HasSuffix(path, "/responses") || path == "/responses":
		return baseURL + "/responses"
	case strings.HasSuffix(path, "/models") || path == "/models":
		return baseURL + "/models"
	default:
		return baseURL + path
	}
}

func copyProxyHeaders(source http.Header, target http.Header) {
	for name, values := range source {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "host" || lower == "connection" || lower == "content-length" {
			continue
		}
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

func decideRelayRoute(body []byte, profile relayProfile) relayRouteDecision {
	decision := relayRouteDecision{body: body, route: "text", reason: "default_text", keySource: "default"}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		decision.reason = "invalid_json"
		return decision
	}

	if !profile.ImageGenerationEnabled {
		decision.reason = "image_disabled"
		decision.body, decision.strippedImageTool = stripImageGenerationTools(value, body)
		return decision
	}

	if usesSeparateImageGenerationAPI(profile) {
		if relayToolChoiceRequestsImage(value["tool_choice"]) {
			decision.useImageAPI = true
			decision.route = "image"
			decision.reason = "tool_choice_image"
			decision.keySource = "image"
			return decision
		}
		if relayBodyContainsImageGenerationCall(value) {
			decision.useImageAPI = true
			decision.route = "image"
			decision.reason = "image_generation_call"
			decision.keySource = "image"
			return decision
		}
		if relayLatestUserInputRequestsImage(value["input"]) {
			decision.useImageAPI = true
			decision.route = "image"
			decision.reason = "latest_user_image_intent"
			decision.keySource = "image"
			return decision
		}
	}

	decision.body, decision.strippedImageTool = stripImageGenerationTools(value, body)
	if decision.strippedImageTool {
		decision.reason = "text_with_image_tool_stripped"
	}
	return decision
}

func stripImageGenerationTools(value map[string]any, fallback []byte) ([]byte, bool) {
	stripped := false
	if tools, ok := value["tools"].([]any); ok && len(tools) > 0 {
		filtered := make([]any, 0, len(tools))
		for _, tool := range tools {
			if relayToolIsImageGeneration(tool) {
				stripped = true
				continue
			}
			filtered = append(filtered, tool)
		}
		if stripped {
			value["tools"] = filtered
		}
	}
	if relayToolChoiceRequestsImage(value["tool_choice"]) {
		delete(value, "tool_choice")
		stripped = true
	}
	if !stripped {
		return fallback, false
	}
	updated, err := json.Marshal(value)
	if err != nil {
		return fallback, false
	}
	return updated, true
}

func relayToolIsImageGeneration(tool any) bool {
	object, ok := tool.(map[string]any)
	if !ok {
		return false
	}
	return relayImageKind(stringFromAny(firstNonNil(object["type"], object["name"])))
}

func relayToolChoiceRequestsImage(choice any) bool {
	switch value := choice.(type) {
	case string:
		return relayImageKind(value)
	case map[string]any:
		return relayImageKind(stringFromAny(firstNonNil(value["type"], value["name"])))
	default:
		return false
	}
}

func relayBodyContainsImageGenerationCall(value map[string]any) bool {
	for key, item := range value {
		if key == "tools" || key == "tool_choice" {
			continue
		}
		if relayNodeContainsImageGenerationCall(item) {
			return true
		}
	}
	return false
}

func relayNodeContainsImageGenerationCall(node any) bool {
	switch value := node.(type) {
	case map[string]any:
		kind := strings.ToLower(stringFromAny(firstNonNil(value["type"], value["name"])))
		if strings.Contains(kind, "image_generation_call") {
			return true
		}
		for key, item := range value {
			if key == "tools" || key == "tool_choice" {
				continue
			}
			if relayNodeContainsImageGenerationCall(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if relayNodeContainsImageGenerationCall(item) {
				return true
			}
		}
	}
	return false
}

func relayLatestUserInputRequestsImage(input any) bool {
	texts := relayLatestUserTextFragments(input)
	if len(texts) == 0 {
		texts = relayTextFragments(input)
	}
	for _, text := range texts {
		if relayTextRequestsImage(text) {
			return true
		}
	}
	return false
}

func relayLatestUserTextFragments(input any) []string {
	messages, ok := input.([]any)
	if !ok {
		return nil
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message, ok := messages[index].(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(stringFromAny(message["role"])) != "user" {
			continue
		}
		texts := relayTextFragments(firstNonNil(message["content"], message["text"], message["input"], message["prompt"]))
		if len(texts) > 0 {
			return texts
		}
	}
	return nil
}

func relayTextFragments(node any) []string {
	var fragments []string
	var walk func(any)
	walk = func(item any) {
		switch value := item.(type) {
		case string:
			fragments = append(fragments, value)
		case []any:
			for _, child := range value {
				walk(child)
			}
		case map[string]any:
			if kind := strings.ToLower(stringFromAny(value["type"])); kind != "" && strings.Contains(kind, "image") && !strings.Contains(kind, "text") {
				return
			}
			for key, child := range value {
				switch key {
				case "text", "content", "input", "prompt":
					walk(child)
				}
			}
		}
	}
	walk(node)
	return fragments
}

func relayTextRequestsImage(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	chineseActions := []string{"生成", "画", "绘制", "创建", "设计", "做"}
	chineseTargets := []string{"图片", "图像", "图标", "logo"}
	for _, action := range chineseActions {
		if strings.Contains(normalized, action) {
			for _, target := range chineseTargets {
				if strings.Contains(normalized, target) {
					return true
				}
			}
		}
	}
	englishActions := []string{"generate", "create", "draw", "make", "design"}
	englishTargets := []string{"image", "picture", "icon", "logo", "illustration"}
	for _, action := range englishActions {
		if !strings.Contains(normalized, action) {
			continue
		}
		for _, target := range englishTargets {
			if strings.Contains(normalized, target) {
				return true
			}
		}
	}
	return false
}

func relayImageKind(kind string) bool {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	return normalized == "image_generation" || normalized == "image_generation_call"
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (r *launcherRuntime) logRendererDiagnostic(raw json.RawMessage) {
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		detail = map[string]any{"parse_error": err.Error(), "raw_bytes": len(raw)}
	}
	event := sanitizeDiagnosticEvent(stringFromAny(detail["event"]))
	appendDiagnosticLog("renderer."+event, detail)
}

func writeCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-allow-methods", "GET, POST, OPTIONS")
	w.Header().Set("access-control-allow-headers", "Content-Type, Authorization")
}

func writeHelperJSON(w http.ResponseWriter, status int, value any) {
	writeCORSHeaders(w)
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func sanitizeDiagnosticEvent(event string) string {
	var b strings.Builder
	for _, ch := range event {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "event"
	}
	return out
}
