package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const vlmTestImageDataURL = "data:image/png;base64,AQID"

func isolateVLMTestHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
}

func vlmTestCommandArgs(baseURL, apiKey, imageDataURL string) map[string]any {
	return map[string]any{"request": map[string]any{
		"apiKey": apiKey, "model": "vision-test", "baseUrl": baseURL, "imageDataUrl": imageDataURL,
	}}
}

func TestVLMProfileTestCommandSendsImageAndRedactsEchoedKey(t *testing.T) {
	isolateVLMTestHome(t)
	const apiKey = "sk-vlm-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("Content-Type = %q", got)
		}
		var payload struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "vision-test" || payload.Stream || len(payload.Messages) != 1 || len(payload.Messages[0].Content) != 2 {
			t.Errorf("payload = %#v", payload)
		}
		imagePart := payload.Messages[0].Content[0]
		imageURL, _ := imagePart["image_url"].(map[string]any)
		if imagePart["type"] != "image_url" || imageURL["url"] != vlmTestImageDataURL {
			t.Errorf("image payload = %#v", imagePart)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":"recognized %s"}}]}`, apiKey)
	}))
	defer upstream.Close()

	result := (&server{}).dispatch(context.Background(), "test_vlm_profile", vlmTestCommandArgs(upstream.URL+"/v1", apiKey, vlmTestImageDataURL))
	if result["status"] != "ok" || result["vlmStatus"] != "ok" {
		t.Fatalf("VLM dispatch failed: %#v", result)
	}
	if result["description"] != "recognized [已隐藏]" {
		t.Fatalf("description was not redacted: %#v", result["description"])
	}
	assertVLMResultDoesNotContainSecret(t, result, apiKey)
}

func TestVLMProfileTestRejectsInvalidAndOversizedImageWithoutRequest(t *testing.T) {
	isolateVLMTestHome(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer upstream.Close()

	oversized := "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(vlmTestImageLimit+1))
	for _, image := range []string{"not-an-image", "data:image/png;base64,%%%", oversized} {
		result := (&server{}).testVLMProfile(context.Background(), vlmTestCommandArgs(upstream.URL, "sk-test", image))
		if result["status"] != "failed" || result["vlmStatus"] != "client_error" {
			t.Fatalf("invalid image result = %#v", result)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid image sent %d upstream requests", requests.Load())
	}
}

func TestVLMProfileTestRejectsUnsafeBaseURL(t *testing.T) {
	for _, raw := range []string{
		"file:///tmp/vlm",
		"https://user:password@example.test/v1",
		"https://example.test/v1?token=secret",
		"https://example.test/v1#fragment",
	} {
		if endpoint, err := vlmTestEndpoint(raw); err == nil {
			t.Errorf("vlmTestEndpoint(%q) = %q, want error", raw, endpoint)
		}
	}
	if endpoint, err := vlmTestEndpoint("https://example.test/v1/"); err != nil || endpoint != "https://example.test/v1/chat/completions" {
		t.Fatalf("normal endpoint = %q, %v", endpoint, err)
	}
}

func TestVLMProfileTestRedactsUpstreamErrorsAndMalformedResponses(t *testing.T) {
	isolateVLMTestHome(t)
	const apiKey = "sk-vlm-secret"
	encodedKey := base64.StdEncoding.EncodeToString([]byte(apiKey))
	for _, test := range []struct {
		name         string
		status       int
		body         string
		wantVLM      string
		wantHTTPCode int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"invalid ` + apiKey + ` / ` + encodedKey + `"}`, wantVLM: "http_error", wantHTTPCode: http.StatusUnauthorized},
		{name: "invalid json", status: http.StatusOK, body: "not-json", wantVLM: "json_error", wantHTTPCode: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()
			result := (&server{}).testVLMProfile(context.Background(), vlmTestCommandArgs(upstream.URL, apiKey, vlmTestImageDataURL))
			if result["status"] != "failed" || result["vlmStatus"] != test.wantVLM || result["httpCode"] != test.wantHTTPCode {
				t.Fatalf("result = %#v", result)
			}
			assertVLMResultDoesNotContainSecret(t, result, apiKey)
		})
	}
}

func TestVLMProfileTestHonorsCallerTimeout(t *testing.T) {
	isolateVLMTestHome(t)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result := (&server{}).testVLMProfile(ctx, vlmTestCommandArgs(upstream.URL, "sk-test", vlmTestImageDataURL))
	if result["status"] != "failed" || result["vlmStatus"] != "timeout" {
		t.Fatalf("timeout result = %#v", result)
	}
	if result["message"] != "VLM 请求超时，请检查服务地址和网络代理。" {
		t.Fatalf("timeout message = %#v", result["message"])
	}
}

func assertVLMResultDoesNotContainSecret(t *testing.T, result commandResult, apiKey string) {
	t.Helper()
	encodedKey := base64.StdEncoding.EncodeToString([]byte(apiKey))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), apiKey) || strings.Contains(string(data), encodedKey) {
		t.Fatalf("VLM result leaked credential: %s", data)
	}
}
