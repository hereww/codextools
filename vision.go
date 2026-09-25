package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	vlmBatchSize           = 5
	vlmGoldenWindowDepth   = 10
	vlmAnalyzeDepthLimit   = 50
	vlmAverageDescBudget   = uint64(100)
	vlmCacheLimit          = 500
	vlmCacheTTL            = 24 * time.Hour
	vlmRequestLimit        = 30 * time.Second
	vlmContextSafetyMargin = 0.9
	vlmTestImageLimit      = 10 * 1024 * 1024
)

func (s *server) testVLMProfile(ctx context.Context, args map[string]any) (result commandResult) {
	request := mapArg(args, "request")
	apiKey := strings.TrimSpace(stringFromAny(request["apiKey"]))
	defer func() { result = redactVLMTestResult(result, apiKey) }()

	model := strings.TrimSpace(stringFromAny(request["model"]))
	baseURL := strings.TrimRight(strings.TrimSpace(stringFromAny(request["baseUrl"])), "/")
	imageDataURL := strings.TrimSpace(stringFromAny(request["imageDataUrl"]))
	if apiKey == "" || model == "" || baseURL == "" {
		return failed("VLM 测试配置不完整：请填写 API Key、Model 和 Base URL。", map[string]any{"vlmStatus": "client_error", "model": model})
	}
	_, mimeType, err := decodeVLMTestImage(imageDataURL)
	if err != nil {
		return failed(err.Error(), map[string]any{"vlmStatus": "client_error", "model": model})
	}
	endpoint, err := vlmTestEndpoint(baseURL)
	if err != nil {
		return failed("VLM Base URL 无效：请输入不含凭据的 http:// 或 https:// 地址。", map[string]any{"vlmStatus": "client_error", "model": model})
	}
	settings := loadSettings()
	profile := relayProfile{
		ID: "vlm-test", VLMAPIKey: apiKey, VLMModel: model, VLMBaseURL: baseURL,
		ProxyEnabled: boolFromAny(request["proxyEnabled"]), ProxyURL: strings.TrimSpace(stringFromAny(request["proxyUrl"])),
	}
	payload, err := buildVLMTestPayload(model, mimeType, imageDataURL)
	if err != nil {
		return failed("构造 VLM 测试请求失败。", map[string]any{"vlmStatus": "client_error", "model": model})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return failed("构造 VLM 测试请求失败。", map[string]any{"vlmStatus": "client_error", "model": model})
	}
	testCtx, cancel := context.WithTimeout(ctx, vlmRequestLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(testCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return failed("VLM Base URL 无效。", map[string]any{"vlmStatus": "client_error", "model": model})
	}
	req.Header.Set("authorization", "Bearer "+apiKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "CodexTools-VLM-Test/"+version)
	client, err := relayHTTPClientForSettings(settings, profile, proxyPurposeVLM)
	if err != nil {
		return failed("VLM 代理配置无效："+redactVLMTestText(err.Error(), apiKey), map[string]any{"vlmStatus": "client_error", "model": model})
	}
	started := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		status, message := "send_error", "VLM 请求失败，请检查 Base URL、网络和代理设置。"
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			status, message = "timeout", "VLM 请求超时，请检查服务地址和网络代理。"
		}
		return commandResult{"status": "failed", "message": message, "vlmStatus": status, "httpCode": 0, "durationMs": duration, "model": model, "error": message}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if readErr != nil || len(responseBody) > 4*1024*1024 {
		return commandResult{"status": "failed", "message": "VLM 响应读取失败或超过 4MB 限制。", "vlmStatus": "http_error", "httpCode": resp.StatusCode, "durationMs": duration, "model": model}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := stepwiseShortText(redactVLMTestText(string(responseBody), apiKey), 320)
		message := fmt.Sprintf("VLM 服务返回 HTTP %d。", resp.StatusCode)
		if preview != "" {
			message += " " + preview
		}
		return commandResult{"status": "failed", "message": message, "vlmStatus": "http_error", "httpCode": resp.StatusCode, "durationMs": duration, "model": model, "error": message}
	}
	var decoded map[string]any
	if json.Unmarshal(responseBody, &decoded) != nil {
		return commandResult{"status": "failed", "message": "VLM 返回了无效 JSON。", "vlmStatus": "json_error", "httpCode": resp.StatusCode, "durationMs": duration, "model": model}
	}
	choices, _ := decoded["choices"].([]any)
	if len(choices) == 0 {
		return commandResult{"status": "failed", "message": "VLM 响应中没有 choices。", "vlmStatus": "no_text", "httpCode": resp.StatusCode, "durationMs": duration, "model": model}
	}
	choice, _ := choices[0].(map[string]any)
	messageValue, _ := choice["message"].(map[string]any)
	description := strings.TrimSpace(stringFromAny(messageValue["content"]))
	if description == "" {
		return commandResult{"status": "failed", "message": "VLM 没有返回图片描述。", "vlmStatus": "no_text", "httpCode": resp.StatusCode, "durationMs": duration, "model": model}
	}
	description = redactVLMTestText(description, apiKey)
	return commandResult{"status": "ok", "message": "VLM 图片识别测试成功。", "vlmStatus": "ok", "httpCode": resp.StatusCode, "durationMs": duration, "model": model, "description": description}
}

func vlmTestEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid VLM base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func decodeVLMTestImage(value string) ([]byte, string, error) {
	metadata, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(metadata), "data:image/") || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return nil, "", errors.New("请选择有效的 base64 图片文件。")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(vlmTestImageLimit)+4 {
		return nil, "", errors.New("图片超过 10MB，请换一张较小的图片。")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", errors.New("图片数据无法解码，请重新选择。")
	}
	if len(data) == 0 || len(data) > vlmTestImageLimit {
		return nil, "", errors.New("图片为空或超过 10MB 限制。")
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(metadata, "data:"), ";base64")
	return data, mimeType, nil
}

func buildVLMTestPayload(model, mimeType, imageDataURL string) (map[string]any, error) {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(mimeType) == "" || imageDataURL == "" {
		return nil, errors.New("missing VLM test request data")
	}
	return map[string]any{
		"model": model,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageDataURL}},
			map[string]any{"type": "text", "text": "请描述图片内容。如包含文字，请精确提取图片中的文字。"},
		}}},
		"stream": false,
	}, nil
}

func redactVLMTestText(value, apiKey string) string {
	if apiKey != "" {
		for _, candidate := range []string{
			apiKey,
			url.QueryEscape(apiKey),
			base64.StdEncoding.EncodeToString([]byte(apiKey)),
			base64.RawStdEncoding.EncodeToString([]byte(apiKey)),
			base64.URLEncoding.EncodeToString([]byte(apiKey)),
			base64.RawURLEncoding.EncodeToString([]byte(apiKey)),
		} {
			if candidate != "" {
				value = strings.ReplaceAll(value, candidate, "[已隐藏]")
			}
		}
	}
	return value
}

func redactVLMTestResult(result commandResult, apiKey string) commandResult {
	for key, value := range result {
		result[key] = redactVLMTestValue(value, apiKey)
	}
	return result
}

func redactVLMTestValue(value any, apiKey string) any {
	switch typed := value.(type) {
	case string:
		return redactVLMTestText(typed, apiKey)
	case map[string]any:
		for key, nested := range typed {
			typed[key] = redactVLMTestValue(nested, apiKey)
		}
		return typed
	case []any:
		for index, nested := range typed {
			typed[index] = redactVLMTestValue(nested, apiKey)
		}
		return typed
	default:
		return value
	}
}

type vlmCacheEntry struct {
	Description string
	CreatedAt   time.Time
}

var (
	vlmCacheMu   sync.Mutex
	vlmCache     = map[string]vlmCacheEntry{}
	vlmGlobalSem = make(chan struct{}, 5)
)

func normalizeModelVLM(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var input map[string]string
	if json.Unmarshal([]byte(raw), &input) != nil {
		return raw
	}
	output := map[string]string{}
	for model, mode := range input {
		model = strings.TrimSpace(model)
		mode = normalizeImageHandling(mode)
		if model != "" {
			output[model] = mode
		}
	}
	data, err := json.Marshal(output)
	if err != nil {
		return raw
	}
	return string(data)
}

func normalizeImageHandling(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "strip":
		return "strip"
	case "vlm":
		return "vlm"
	default:
		return "send-as-is"
	}
}

func imageHandlingMode(model, raw string) string {
	var modes map[string]string
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &modes) != nil {
		return "send-as-is"
	}
	return normalizeImageHandling(modes[strings.TrimSpace(model)])
}

func applyVisionHandling(ctx context.Context, profile relayProfile, body []byte) ([]byte, error) {
	if strings.TrimSpace(profile.ModelVLM) == "" || len(body) == 0 {
		return body, nil
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("invalid relay request for image handling: %w", err)
	}
	model := strings.TrimSpace(stringFromAny(request["model"]))
	mode := imageHandlingMode(model, profile.ModelVLM)
	if mode == "send-as-is" || model == "" {
		return body, nil
	}
	keys := []string{"messages", "input"}
	switch mode {
	case "strip":
		for _, key := range keys {
			if messages, ok := request[key].([]any); ok {
				stripImagesOnly(messages)
			}
		}
	case "vlm":
		if strings.TrimSpace(profile.VLMAPIKey) == "" || strings.TrimSpace(profile.VLMModel) == "" || strings.TrimSpace(profile.VLMBaseURL) == "" {
			appendDiagnosticLog("vlm.config_incomplete", map[string]any{"relay_id": profile.ID, "model": model})
			return body, nil
		}
		for _, key := range keys {
			messages, ok := request[key].([]any)
			if !ok {
				continue
			}
			changed, err := analyzeAndReplaceImages(ctx, messages, profile, model)
			if err != nil {
				appendDiagnosticLog("vlm.fail_closed", map[string]any{"relay_id": profile.ID, "model": model, "error": err.Error()})
				return body, nil
			}
			if changed {
				request[key] = messages
			}
		}
	}
	return json.Marshal(request)
}

func stripImagesOnly(messages []any) {
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		next := make([]any, 0, len(parts))
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if isImagePart(part) {
				next = append(next, map[string]any{"type": "text", "text": "[图片已省略]"})
				continue
			}
			next = append(next, rawPart)
		}
		message["content"] = next
	}
}

type vlmImageRef struct {
	Message int
	URL     string
}

func analyzeAndReplaceImages(ctx context.Context, messages []any, profile relayProfile, requestModel string) (bool, error) {
	rounds := collectRecentVLMImageMessages(messages, vlmAnalyzeDepthLimit)
	if len(rounds) == 0 {
		stripAllImages(messages)
		return false, nil
	}
	contextWindow, textTokens, available := visionContextCapacity(messages, profile, requestModel)
	currentIndex := latestVLMImageMessageIndex(messages)
	if available <= 1 {
		imageCount := 0
		if currentIndex >= 0 {
			imageCount = len(collectVLMImageURLs(messages[currentIndex]))
		}
		stripAllImages(messages)
		if imageCount > 0 {
			injectVLMText(messages, currentIndex, fmt.Sprintf("\n[系统：当前轮次有 %d 张图片因上下文已满未完成 VLM 分析，图片已被清理以释放空间]", imageCount))
		}
		appendDiagnosticLog("vlm.context_overflow", map[string]any{
			"context_window":             contextWindow,
			"text_only_estimated_tokens": textTokens,
			"skipped_images":             imageCount,
			"model":                      requestModel,
		})
		return true, nil
	}
	xBudget := int(available / vlmAverageDescBudget)
	goldenCutoff := goldenVLMUserCutoff(messages, vlmGoldenWindowDepth)
	goldenTotal, deepTotal := 0, 0
	for _, round := range rounds {
		if round.Message >= goldenCutoff {
			goldenTotal += len(round.URLs)
		} else {
			deepTotal += len(round.URLs)
		}
	}
	appendDiagnosticLog("vlm.strip_entry", map[string]any{
		"image_rounds": len(rounds),
		"golden_total": goldenTotal,
		"deep_total":   deepTotal,
		"x_budget":     xBudget,
		"model":        requestModel,
	})

	descriptions := map[int][]string{}
	analyzeRound := func(round vlmImageMessage) error {
		pending := make([]string, 0, len(round.URLs))
		for _, imageURL := range round.URLs {
			if cached, ok := getVLMCache(imageURL); ok {
				descriptions[round.Message] = append(descriptions[round.Message], "[图片描述] "+cached)
			} else {
				pending = append(pending, imageURL)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		analyzed, err := analyzeVLMURLs(ctx, pending, profile)
		if err != nil {
			return err
		}
		for _, imageURL := range pending {
			description := strings.TrimSpace(analyzed[imageURL])
			if description == "" {
				description = "[部分图片无法识别]"
			}
			descriptions[round.Message] = append(descriptions[round.Message], "[图片描述] "+description)
		}
		return nil
	}

	for _, round := range rounds {
		if round.Message != currentIndex {
			continue
		}
		if err := analyzeRound(round); err != nil {
			appendDiagnosticLog("vlm.current_round_fail_closed", map[string]any{
				"round_url_count": len(round.URLs),
				"model":           requestModel,
				"error":           err.Error(),
			})
			return false, errors.New("current-round VLM analysis failed")
		}
	}

	cap := goldenTotal
	if xBudget <= vlmGoldenWindowDepth {
		if cap > xBudget {
			cap = xBudget
		}
	} else if cap > vlmGoldenWindowDepth {
		cap = vlmGoldenWindowDepth
	}
	goldenInjected := 0
	for _, round := range rounds {
		if round.Message == currentIndex || round.Message < goldenCutoff || goldenInjected >= cap {
			continue
		}
		remaining := cap - goldenInjected
		selected := round.URLs
		if len(selected) > remaining {
			selected = selected[:remaining]
		}
		goldenInjected += len(selected)
		_ = analyzeRound(vlmImageMessage{Message: round.Message, URLs: selected})
	}

	historicalInjected := goldenInjected
	if historicalInjected < xBudget {
		remaining := xBudget - historicalInjected
		for _, round := range rounds {
			if round.Message == currentIndex || round.Message >= goldenCutoff || remaining == 0 {
				continue
			}
			for _, imageURL := range round.URLs {
				if remaining == 0 {
					break
				}
				if cached, ok := getVLMCache(imageURL); ok {
					descriptions[round.Message] = append(descriptions[round.Message], "[图片描述] "+cached)
					remaining--
					historicalInjected++
				}
			}
		}
	}

	backgroundURLs := []string{}
	if xBudget > vlmGoldenWindowDepth {
		backgroundTarget := xBudget - goldenInjected
		appendBackground := func(round vlmImageMessage) {
			for _, imageURL := range round.URLs {
				if len(backgroundURLs) >= backgroundTarget {
					return
				}
				if _, cached := getVLMCache(imageURL); !cached && !containsString(backgroundURLs, imageURL) {
					backgroundURLs = append(backgroundURLs, imageURL)
				}
			}
		}
		for _, round := range rounds {
			if round.Message != currentIndex && round.Message >= goldenCutoff {
				appendBackground(round)
			}
		}
		for _, round := range rounds {
			if round.Message != currentIndex && round.Message < goldenCutoff {
				appendBackground(round)
			}
		}
	}

	truncateVLMDescriptions(descriptions, available*2)
	stripAllImages(messages)
	for index, values := range descriptions {
		injectVLMText(messages, index, "\n"+strings.Join(values, "\n"))
	}
	appendDiagnosticLog("vlm.strip_done", map[string]any{
		"descriptions_injected": len(descriptions),
		"historical_injected":   historicalInjected,
		"x_budget":              xBudget,
		"model":                 requestModel,
	})
	if len(backgroundURLs) > 0 {
		backgroundProfile := profile
		go func(urls []string) {
			analyzed, _ := analyzeVLMURLs(context.Background(), urls, backgroundProfile)
			appendDiagnosticLog("vlm.phase2_done", map[string]any{"urls_analyzed": len(analyzed), "urls_requested": len(urls)})
		}(append([]string(nil), backgroundURLs...))
	}
	return true, nil
}

type vlmImageMessage struct {
	Message int
	URLs    []string
}

func collectRecentVLMImageMessages(messages []any, depthLimit int) []vlmImageMessage {
	result := []vlmImageMessage{}
	messageCount := 0
	for index := len(messages) - 1; index >= 0 && messageCount < depthLimit; index-- {
		message, _ := messages[index].(map[string]any)
		if !isVLMMessageRole(message) {
			continue
		}
		messageCount++
		if urls := collectVLMImageURLs(messages[index]); len(urls) > 0 {
			result = append(result, vlmImageMessage{Message: index, URLs: urls})
		}
	}
	return result
}

func isVLMMessageRole(message map[string]any) bool {
	role := stringFromAny(message["role"])
	return role == "user" || role == "tool"
}

func latestVLMImageMessageIndex(messages []any) int {
	for index := len(messages) - 1; index >= 0; index-- {
		message, _ := messages[index].(map[string]any)
		if isVLMMessageRole(message) && len(collectVLMImageURLs(messages[index])) > 0 {
			return index
		}
	}
	return -1
}

func collectVLMImageURLs(rawMessage any) []string {
	message, _ := rawMessage.(map[string]any)
	parts, _ := message["content"].([]any)
	urls := []string{}
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		if isImagePart(part) {
			if imageURL := imagePartURL(part); imageURL != "" {
				urls = append(urls, imageURL)
			}
		}
	}
	return urls
}

func lastUserMessageIndex(messages []any) int {
	for index := len(messages) - 1; index >= 0; index-- {
		message, _ := messages[index].(map[string]any)
		if stringFromAny(message["role"]) == "user" {
			return index
		}
	}
	return -1
}

func goldenVLMUserCutoff(messages []any, depth int) int {
	cutoff := 0
	found := 0
	for index := len(messages) - 1; index >= 0 && found < depth; index-- {
		message, _ := messages[index].(map[string]any)
		if isVLMMessageRole(message) {
			cutoff = index
			found++
		}
	}
	return cutoff
}

func truncateVLMDescriptions(descriptions map[int][]string, availableChars uint64) {
	keys := make([]int, 0, len(descriptions))
	for key := range descriptions {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(keys)))
	remaining := int(availableChars)
	for _, key := range keys {
		joined := strings.Join(descriptions[key], "\n")
		runes := []rune(joined)
		if remaining <= 0 {
			delete(descriptions, key)
			continue
		}
		if len(runes) <= remaining {
			remaining -= len(runes)
			continue
		}
		marker := []rune("\n[历史图片描述已省略]")
		keep := remaining - len(marker)
		if keep < 1 {
			delete(descriptions, key)
			remaining = 0
			continue
		}
		descriptions[key] = []string{string(runes[:keep]) + string(marker)}
		remaining = 0
	}
}

func collectVLMImageRefs(messages []any) []vlmImageRef {
	var refs []vlmImageRef
	for messageIndex, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		parts, _ := message["content"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if !isImagePart(part) {
				continue
			}
			if imageURL := imagePartURL(part); imageURL != "" {
				refs = append(refs, vlmImageRef{Message: messageIndex, URL: imageURL})
			}
		}
	}
	return refs
}

func isImagePart(part map[string]any) bool {
	typeName := stringFromAny(part["type"])
	return typeName == "image_url" || typeName == "input_image"
}

func imagePartURL(part map[string]any) string {
	if image, ok := part["image_url"].(map[string]any); ok {
		return strings.TrimSpace(stringFromAny(image["url"]))
	}
	return firstNonEmpty(stringFromAny(part["image_url"]), stringFromAny(part["url"]))
}

func stripAllImages(messages []any) {
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		next := parts[:0]
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if !isImagePart(part) {
				next = append(next, rawPart)
			}
		}
		message["content"] = next
	}
}

func injectVLMText(messages []any, index int, text string) {
	if index < 0 || index >= len(messages) {
		return
	}
	message, _ := messages[index].(map[string]any)
	switch content := message["content"].(type) {
	case []any:
		message["content"] = append(content, map[string]any{"type": "text", "text": text})
	case string:
		message["content"] = []any{map[string]any{"type": "text", "text": content}, map[string]any{"type": "text", "text": text}}
	}
}

func visionContextFull(messages []any, profile relayProfile, model string) bool {
	_, _, available := visionContextCapacity(messages, profile, model)
	return available <= 1
}

func visionContextCapacity(messages []any, profile relayProfile, model string) (uint64, uint64, uint64) {
	window := uint64(0)
	if configured := parseModelWindows(profile.ModelWindows)[model]; configured != "" {
		window, _ = parseModelWindowToken(configured)
	}
	if window == 0 {
		window, _ = parseModelWindowToken(profile.ContextWindow)
	}
	if window == 0 {
		window = 272000
	}
	copyMessages, _ := remarshalValue[[]any](messages)
	stripAllImages(copyMessages)
	data, _ := json.Marshal(copyMessages)
	estimatedTokens := uint64(len(data) / 2)
	effectiveWindow := uint64(float64(window) * vlmContextSafetyMargin)
	available := uint64(0)
	if estimatedTokens < effectiveWindow {
		available = effectiveWindow - estimatedTokens
	}
	return window, estimatedTokens, available
}

func analyzeVLMURLs(ctx context.Context, urls []string, profile relayProfile) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	type result struct {
		index       int
		description string
		err         error
	}
	batches := (len(urls) + vlmBatchSize - 1) / vlmBatchSize
	results := make(chan result, batches)
	localSem := make(chan struct{}, 3)
	for index := 0; index < batches; index++ {
		start := index * vlmBatchSize
		end := start + vlmBatchSize
		if end > len(urls) {
			end = len(urls)
		}
		batch := append([]string(nil), urls[start:end]...)
		go func(index int) {
			select {
			case localSem <- struct{}{}:
				defer func() { <-localSem }()
			case <-ctx.Done():
				results <- result{index: index, err: ctx.Err()}
				return
			}
			select {
			case vlmGlobalSem <- struct{}{}:
				defer func() { <-vlmGlobalSem }()
			case <-ctx.Done():
				results <- result{index: index, err: ctx.Err()}
				return
			}
			description, err := callVLMBatchWithRetry(ctx, batch, profile)
			results <- result{index: index, description: description, err: err}
		}(index)
	}
	ordered := make([]result, batches)
	for range batches {
		select {
		case item := <-results:
			ordered[item.index] = item
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	output := map[string]string{}
	var failures []string
	for index, item := range ordered {
		start := index * vlmBatchSize
		end := start + vlmBatchSize
		if end > len(urls) {
			end = len(urls)
		}
		if item.err != nil {
			failures = append(failures, item.err.Error())
			continue
		}
		for _, imageURL := range urls[start:end] {
			output[imageURL] = item.description
			putVLMCache(imageURL, item.description)
		}
	}
	if len(output) == 0 && len(failures) > 0 {
		return nil, errors.New(failures[0])
	}
	return output, nil
}

func callVLMBatchWithRetry(ctx context.Context, urls []string, profile relayProfile) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		description, err := callVLMBatch(ctx, urls, profile)
		if err == nil {
			return description, nil
		}
		lastErr = err
		if !vlmRetryable(err) || attempt == 2 {
			break
		}
		delay := 500 * time.Millisecond * time.Duration(1<<attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", lastErr
}

func callVLMBatch(parent context.Context, urls []string, profile relayProfile) (string, error) {
	parts := make([]any, 0, len(urls)+1)
	for _, imageURL := range urls {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
	}
	parts = append(parts, map[string]any{"type": "text", "text": "请描述图片内容。如包含文字，请精确提取图片中的文字。"})
	payload, _ := json.Marshal(map[string]any{
		"model":    profile.VLMModel,
		"messages": []any{map[string]any{"role": "user", "content": parts}},
		"stream":   false,
	})
	ctx, cancel := context.WithTimeout(parent, vlmRequestLimit)
	defer cancel()
	endpoint := strings.TrimRight(profile.VLMBaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+profile.VLMAPIKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", firstNonEmpty(profile.UserAgent, "CodexTools-VLM/"+version))
	client, err := relayHTTPClientForSettings(loadSettings(), profile, proxyPurposeVLM)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("VLM request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := stepwiseShortText(string(body), 256)
		return "", fmt.Errorf("VLM API %d: %s", resp.StatusCode, preview)
	}
	var response map[string]any
	if json.Unmarshal(body, &response) != nil {
		return "", errors.New("VLM response is not valid JSON")
	}
	choices, _ := response["choices"].([]any)
	if len(choices) == 0 {
		return "", errors.New("VLM response has no choices")
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	description := strings.TrimSpace(stringFromAny(message["content"]))
	if description == "" {
		return "", errors.New("VLM response has no content")
	}
	return description, nil
}

func vlmRetryable(err error) bool {
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "timeout") || strings.Contains(value, "request failed") ||
		strings.Contains(value, "vlm api 429") || strings.Contains(value, "vlm api 502") ||
		strings.Contains(value, "vlm api 503") || strings.Contains(value, "vlm api 504")
}

func vlmCacheKey(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:16])
}

func getVLMCache(url string) (string, bool) {
	vlmCacheMu.Lock()
	defer vlmCacheMu.Unlock()
	key := vlmCacheKey(url)
	entry, ok := vlmCache[key]
	if !ok || time.Since(entry.CreatedAt) > vlmCacheTTL {
		delete(vlmCache, key)
		return "", false
	}
	return entry.Description, true
}

func putVLMCache(url, description string) {
	vlmCacheMu.Lock()
	defer vlmCacheMu.Unlock()
	if len(vlmCache) >= vlmCacheLimit {
		now := time.Now()
		for key, entry := range vlmCache {
			if now.Sub(entry.CreatedAt) > vlmCacheTTL {
				delete(vlmCache, key)
			}
		}
	}
	if len(vlmCache) >= vlmCacheLimit {
		type pair struct {
			key string
			at  time.Time
		}
		entries := make([]pair, 0, len(vlmCache))
		for key, entry := range vlmCache {
			entries = append(entries, pair{key: key, at: entry.CreatedAt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
		for _, entry := range entries[:len(entries)/4] {
			delete(vlmCache, entry.key)
		}
	}
	vlmCache[vlmCacheKey(url)] = vlmCacheEntry{Description: description, CreatedAt: time.Now()}
}

func remarshalValue[T any](value any) (T, error) {
	var output T
	data, err := json.Marshal(value)
	if err != nil {
		return output, err
	}
	err = json.Unmarshal(data, &output)
	return output, err
}
