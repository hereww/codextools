package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStepwiseDefaultsPublicSettingsAndExtraction(t *testing.T) {
	settings := defaultSettings()
	if settings.CodexAppStepwiseEnabled || settings.CodexAppStepwiseAPIKeyEnv != defaultStepwiseAPIKeyEnv || settings.CodexAppStepwiseMaxItems != 6 || settings.CodexAppStepwiseTimeoutMS != 8000 {
		t.Fatalf("stepwise defaults = %#v", settings)
	}
	settings.CodexAppStepwiseAPIKey = "secret-direct-key"
	settings.CodexAppStepwiseBaseURL = "https://stepwise.example/v1"
	public := stepwisePublicSettingsValue(settings)
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-direct-key") || !strings.Contains(string(encoded), `"apiKeyConfigured":true`) {
		t.Fatalf("public Stepwise settings leaked or omitted key state: %s", encoded)
	}
	items := extractStepwiseItems(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": `{"items":[{"label":"Next","prompt":"  run   tests  "},{"prompt":"run tests"},{"text":"ship it"}]}`}}},
	}, 2)
	if len(items) != 2 || items[0].Prompt != "run tests" || items[1].Prompt != "ship it" {
		t.Fatalf("Stepwise extraction = %#v", items)
	}
	clamped := normalizeSettings(backendSettings{
		CodexAppStepwiseMaxItems:        99,
		CodexAppStepwiseMaxInputChars:   1,
		CodexAppStepwiseMaxOutputTokens: 9000,
		CodexAppStepwiseTimeoutMS:       1,
	})
	if clamped.CodexAppStepwiseMaxItems != 6 || clamped.CodexAppStepwiseMaxInputChars != 1000 || clamped.CodexAppStepwiseMaxOutputTokens != 4000 || clamped.CodexAppStepwiseTimeoutMS != 1000 {
		t.Fatalf("Stepwise clamps = %#v", clamped)
	}
}

func TestStepwiseGenerateUsesConfiguredEndpointAndAuthorization(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("authorization") != "Bearer stepwise-key" {
			t.Errorf("Stepwise request path/auth = %q / %q", r.URL.Path, r.Header.Get("authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["model"] != "stepwise-model" {
			t.Errorf("Stepwise model = %#v", payload["model"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"items\":[{\"prompt\":\"继续实现\"}]}"}}]}`)
	}))
	defer upstream.Close()
	settings := defaultSettings()
	settings.CodexAppStepwiseEnabled = true
	settings.CodexAppStepwiseBaseURL = upstream.URL + "/v1"
	settings.CodexAppStepwiseAPIKey = "stepwise-key"
	settings.CodexAppStepwiseModel = "stepwise-model"
	result := generateStepwise(context.Background(), stepwiseRequest{LastUserMessage: "继续"}, settings)
	if result["status"] != "ok" || calls.Load() != 1 {
		t.Fatalf("Stepwise result = %#v, calls = %d", result, calls.Load())
	}
	items, ok := result["items"].([]stepwiseItem)
	if !ok || len(items) != 1 || items[0].Prompt != "继续实现" {
		t.Fatalf("Stepwise items = %#v", result["items"])
	}
	settings.CodexAppStepwiseEnabled = false
	result = generateStepwise(context.Background(), stepwiseRequest{}, settings)
	if result["disabled"] != true || calls.Load() != 1 {
		t.Fatalf("disabled Stepwise made a request: %#v, calls = %d", result, calls.Load())
	}
}

func TestVisionModesStripVLMCacheAndFailClosed(t *testing.T) {
	if imageHandlingMode("text-model", `{"text-model":"strip"}`) != "strip" || imageHandlingMode("other", `{"text-model":"vlm"}`) != "send-as-is" {
		t.Fatal("image handling mode parsing failed")
	}
	stripBody := visionRequestBody("strip-model", "data:image/png;base64,strip")
	stripped, err := applyVisionHandling(context.Background(), relayProfile{ModelVLM: `{"strip-model":"strip"}`}, stripBody)
	if err != nil || bytes.Contains(stripped, []byte("base64,strip")) || !bytes.Contains(stripped, []byte("图片已省略")) {
		t.Fatalf("strip mode = %s, %v", stripped, err)
	}

	resetVLMCacheForTest()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("authorization") != "Bearer vlm-key" {
			t.Errorf("VLM authorization = %q", r.Header.Get("authorization"))
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"a terminal showing successful tests"}}]}`)
	}))
	defer upstream.Close()
	profile := relayProfile{
		ID:         "vlm",
		ModelVLM:   `{"text-model":"vlm"}`,
		VLMAPIKey:  "vlm-key",
		VLMModel:   "vision-model",
		VLMBaseURL: upstream.URL + "/v1",
	}
	body := visionRequestBody("text-model", "data:image/png;base64,same-image")
	first, err := applyVisionHandling(context.Background(), profile, body)
	if err != nil || bytes.Contains(first, []byte("same-image")) || !bytes.Contains(first, []byte("successful tests")) {
		t.Fatalf("VLM output = %s, %v", first, err)
	}
	second, err := applyVisionHandling(context.Background(), profile, body)
	if err != nil || !bytes.Contains(second, []byte("successful tests")) || calls.Load() != 1 {
		t.Fatalf("VLM cache output = %s, err = %v, calls = %d", second, err, calls.Load())
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no vision", http.StatusBadRequest) }))
	defer failing.Close()
	profile.VLMBaseURL = failing.URL + "/v1"
	failBody := visionRequestBody("text-model", "data:image/png;base64,new-image")
	failed, err := applyVisionHandling(context.Background(), profile, failBody)
	if err != nil || !bytes.Equal(failed, failBody) {
		t.Fatalf("current-round VLM failure should preserve original body: %s, %v", failed, err)
	}
	profile.VLMBaseURL = ""
	incomplete, err := applyVisionHandling(context.Background(), profile, failBody)
	if err != nil || !bytes.Equal(incomplete, failBody) {
		t.Fatalf("incomplete VLM config should preserve original body: %s, %v", incomplete, err)
	}
}

func TestAudioTranscriptionsProxyPreservesMultipartRequestAndRawResponse(t *testing.T) {
	var capturedBody []byte
	var capturedType, capturedAuth, capturedUA, capturedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedType = r.Header.Get("content-type")
		capturedAuth = r.Header.Get("authorization")
		capturedUA = r.Header.Get("user-agent")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":"audio rejected"}`)
	}))
	defer upstream.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("RIFF-test-audio"))
	_ = writer.WriteField("model", "gpt-4o-mini-transcribe")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/v1/audio/transcriptions", bytes.NewReader(body.Bytes()))
	request.Header.Set("content-type", writer.FormDataContentType())
	request.Header.Set("user-agent", "Original-Codex-UA")
	recorder := httptest.NewRecorder()
	profile := relayProfile{ID: "audio", Protocol: "chatCompletions", BaseURL: upstream.URL, APIKey: "audio-key"}
	if !forwardRelayProxyAttempt(defaultSettings(), recorder, request, body.Bytes(), profile, 1, 1) {
		t.Fatal("audio request was not handled")
	}
	if capturedPath != "/v1/audio/transcriptions" || capturedType != writer.FormDataContentType() || capturedAuth != "Bearer audio-key" || capturedUA != "Original-Codex-UA" || !bytes.Equal(capturedBody, body.Bytes()) {
		t.Fatalf("audio request mismatch: path=%q type=%q auth=%q ua=%q body=%q", capturedPath, capturedType, capturedAuth, capturedUA, capturedBody)
	}
	if recorder.Code != http.StatusUnprocessableEntity || recorder.Body.String() != `{"error":"audio rejected"}` {
		t.Fatalf("raw audio response = %d %q", recorder.Code, recorder.Body.String())
	}
	for _, path := range []string{"/audio/transcriptions", "/v1/audio/transcriptions", "/v1/v1/audio/transcriptions", "/codex/v1/audio/transcriptions"} {
		if !isAudioTranscriptionsProxyPath(path) {
			t.Fatalf("audio route not recognized: %s", path)
		}
	}
}

func TestGPT56MetadataCatalogAndProtocolOverride(t *testing.T) {
	for _, slug := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		entry := gpt56CatalogEntry(slug)
		if entry == nil || int64FromFlexible(entry["context_window"]) != 272000 {
			t.Fatalf("GPT-5.6 metadata missing for %s: %#v", slug, entry)
		}
	}
	metadata := modelMetadataForNames([]string{"gpt-5.6-sol", "other"})
	if metadata["gpt-5.6-sol"] == nil || metadata["other"] != nil {
		t.Fatalf("renderer metadata = %#v", metadata)
	}

	for _, test := range []struct {
		protocol string
		wantLite bool
	}{
		{protocol: "responses", wantLite: false},
		{protocol: "chatCompletions", wantLite: true},
	} {
		home := t.TempDir()
		profile := relayProfile{
			ID:           "gpt56",
			Protocol:     test.protocol,
			Model:        "gpt-5.6-sol",
			ModelList:    "gpt-5.6-sol\ngpt-5.6-terra",
			ModelWindows: `{"gpt-5.6-sol":"1M"}`,
		}
		if err := writeRelayModelCatalog(home, profile); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(relayModelCatalogRelativePath(profile.ID))))
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		if len(catalog.Models) != 2 || boolFromAny(catalog.Models[0]["use_responses_lite"]) != test.wantLite || int64FromFlexible(catalog.Models[0]["context_window"]) != 1000000 {
			t.Fatalf("GPT-5.6 %s catalog = %#v", test.protocol, catalog.Models)
		}
	}
}

func TestV1244BridgeRoutesAndAdsExclusion(t *testing.T) {
	runtime := &launcherRuntime{}
	for _, path := range []string{"/stepwise/settings", "/stepwise/generate", "/stepwise/test"} {
		result := runtime.handleBridgeRequest(path, json.RawMessage(`{}`))
		if result["path"] == path && result["message"] == "Unknown bridge path" {
			t.Fatalf("bridge route is missing: %s", path)
		}
	}
	ads := runtime.handleBridgeRequest("/ads", json.RawMessage(`{}`))
	if ads["status"] != "failed" || ads["message"] != "Unknown bridge path" {
		t.Fatalf("ads route should remain excluded: %#v", ads)
	}
}

func visionRequestBody(model, imageURL string) []byte {
	data, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "describe this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
			},
		}},
	})
	return data
}

func resetVLMCacheForTest() {
	vlmCacheMu.Lock()
	defer vlmCacheMu.Unlock()
	vlmCache = map[string]vlmCacheEntry{}
}
