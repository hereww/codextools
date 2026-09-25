package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type llmProxyRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper llmProxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestV1247SettingsDefaultsAndModelRoutes(t *testing.T) {
	settings := defaultSettings()
	if settings.CodexAppDreamSkinTheme != "pink" || len(settings.CodexAppDreamSkinThemeConfig) == 0 {
		t.Fatalf("DreamSkin defaults missing: %#v", settings.CodexAppDreamSkinThemeConfig)
	}
	profile := defaultRelayProfile()
	profile.ModelRoutes = []relayModelRoute{
		{Model: " gpt-route ", TargetRelayID: " target ", TargetModel: " upstream "},
		{Model: "gpt-route", TargetRelayID: "duplicate"},
		{Model: "", TargetRelayID: "target"},
	}
	normalized := normalizeSettings(backendSettings{RelayProfiles: []relayProfile{profile}, ActiveRelayID: profile.ID})
	if len(normalized.RelayProfiles[0].ModelRoutes) != 3 {
		t.Fatalf("model routes were not normalized: %#v", normalized.RelayProfiles[0].ModelRoutes)
	}
	route := normalized.RelayProfiles[0].ModelRoutes[0]
	if route.Model != "gpt-route" || route.TargetRelayID != "target" || route.TargetModel != "upstream" {
		t.Fatalf("model route mismatch: %#v", route)
	}
	if normalized.RelayProfiles[0].ModelRoutes[2].Model != "" {
		t.Fatalf("incomplete route draft should be preserved: %#v", normalized.RelayProfiles[0].ModelRoutes)
	}
}

func TestV1247ModelCatalogReportsProfileAwareServiceTier(t *testing.T) {
	home := t.TempDir()
	config := "service_tier = \"flex\"\nprofile = \"work\"\n\n[profiles.work]\nservice_tier = \"priority\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	value := codexModelCatalogFromHome(context.Background(), home)
	if value["service_tier"] != "priority" {
		t.Fatalf("service_tier = %#v", value["service_tier"])
	}
}

func TestV1247DreamSkinRuntimeConfigSanitizesThemeAndLoadsLocalArt(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "skin.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.CodexAppDreamSkinEnabled = true
	settings.CodexAppDreamSkinImagePath = imagePath
	settings.CodexAppDreamSkinThemeConfig = dreamSkinThemeConfig{
		"id": "custom",
		"colors": map[string]any{
			"accent":     "#123456",
			"background": "red; background:url(https://bad.example)",
		},
	}
	config := dreamSkinRuntimeConfig(settings)
	theme := config["config"].(dreamSkinThemeConfig)
	colors := theme["colors"].(map[string]any)
	if colors["accent"] != "#123456" || colors["background"] != "#F7F4F5" {
		t.Fatalf("DreamSkin color sanitization mismatch: %#v", colors)
	}
	if !strings.HasPrefix(stringFromAny(config["imageData"]), "data:image/png;base64,") {
		t.Fatalf("DreamSkin image was not embedded: %#v", config)
	}
	script := injectionScript(57321, settings)
	for _, marker := range []string{
		"__CODEX_PLUS_DREAM_SKIN__",
		"codex-plus-dream-skin-style",
		"ApplicationMenuTopBar",
		"data-codex-plus-usage-alert-hidden",
		"codexServiceTierSupportedFastModels",
		"configServiceTier",
		"loadAppServerRequestCandidates",
		"model_app_server_request_patch_installed",
		"installCodexRemoteSessionDispatcherSubscription",
		"isClientNewThreadId",
		"reactConversationIdFromRow",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("injection missing current upstream marker %q", marker)
		}
	}
}

func TestV1247DreamSkinImportCopiesValidatedImageIntoManagedStorage(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.png")
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, png, 0o600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	managed, err := importDreamSkinImage(context.Background(), source, state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(managed, filepath.Join(state, "dream-skin", "theme")+string(os.PathSeparator)) || !fileExists(managed) {
		t.Fatalf("managed DreamSkin path mismatch: %q", managed)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if !fileExists(managed) {
		t.Fatal("managed image should survive removal of the selected source")
	}
	unsupported := filepath.Join(t.TempDir(), "theme.svg")
	if err := os.WriteFile(unsupported, []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importDreamSkinImage(context.Background(), unsupported, state); err == nil {
		t.Fatal("unsupported DreamSkin image was accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "linked.png")
		if err := os.Symlink(managed, link); err != nil {
			t.Fatal(err)
		}
		if _, err := importDreamSkinImage(context.Background(), link, state); err == nil {
			t.Fatal("DreamSkin symlink source was accepted")
		}
	}
}

func TestV1247PetRealMouseRuntimeAndAvatarTargetIsolation(t *testing.T) {
	avatar := cdpTarget{
		ID:                   "avatar",
		Type:                 "page",
		Title:                "ChatGPT",
		URL:                  "app://-/index.html?initialRoute=%2Favatar-overlay",
		WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/avatar",
	}
	main := cdpTarget{
		ID:                   "main",
		Type:                 "page",
		Title:                "ChatGPT",
		URL:                  "app://-/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:9229/devtools/page/main",
	}
	if !isAvatarOverlayCDPPageTarget(avatar) || isPrimaryCodexCDPPageTarget(avatar) {
		t.Fatalf("avatar target classification failed: %#v", avatar)
	}
	selected, err := pickCDPPageTarget([]cdpTarget{avatar, main})
	if err != nil || selected.ID != "main" {
		t.Fatalf("main target selection = %#v, %v", selected, err)
	}
	if _, err := pickCDPPageTarget([]cdpTarget{avatar}); err == nil {
		t.Fatal("avatar-only target list should not receive the main bridge")
	}
	for _, marker := range []string{
		"__codexPlusPetRealMouseLook",
		"avatar-overlay-computer-use-cursor-changed",
		"cdp-push",
		"releasePointerCapture",
	} {
		if !strings.Contains(petRealMouseInjectScript, marker) {
			t.Fatalf("pet runtime missing marker %q", marker)
		}
	}
	probe := petRealMouseCapabilityProbeScript()
	if !strings.Contains(probe, "1536") || !strings.Contains(probe, "2288") || !strings.Contains(probe, "vscode-api-") {
		t.Fatalf("pet V2 capability probe is incomplete: %s", probe)
	}
	update := petRealMouseUpdateScript(-125, 640)
	if !strings.Contains(update, "x: -125") || !strings.Contains(update, "y: 640") || !strings.Contains(update, "updateScreenPoint") {
		t.Fatalf("pet cursor update script is incomplete: %s", update)
	}
}

func TestV1247ModelRouteSelection(t *testing.T) {
	source := defaultRelayProfile()
	source.ID = "source"
	source.RelayMode = "pureApi"
	source.BaseURL = "https://source.example/v1"
	source.APIKey = "source-key"
	source.ModelRoutes = []relayModelRoute{{Model: "gpt-luna", TargetRelayID: "target", TargetModel: "provider-luna"}}
	target := defaultRelayProfile()
	target.ID = "target"
	target.Name = "Target"
	target.RelayMode = "pureApi"
	target.BaseURL = "https://target.example/v1"
	target.APIKey = "target-key"
	settings := normalizeSettings(backendSettings{RelayProfiles: []relayProfile{source, target}, ActiveRelayID: source.ID})
	body := []byte(`{"model":"gpt-luna","input":"hello"}`)
	selected, routedBody, route, err := selectRelayModelRoute(settings, map[string]any{"model": "gpt-luna", "input": "hello"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != target.ID || route == nil || route.UpstreamModel != "provider-luna" {
		t.Fatalf("unexpected route selection: selected=%#v route=%#v", selected, route)
	}
	var routed map[string]any
	if err := json.Unmarshal(routedBody, &routed); err != nil || routed["model"] != "provider-luna" {
		t.Fatalf("routed model was not rewritten: %s (%v)", routedBody, err)
	}
	settings.RelayProfiles[1].Protocol = "chatCompletions"
	if _, _, _, err := selectRelayModelRoute(settings, map[string]any{"model": "gpt-luna"}, body); err == nil {
		t.Fatal("chat completions target should be rejected")
	}
}

func TestV1247RelayCommonFeaturesPreserveProviderGoalsOverride(t *testing.T) {
	profile := "[features]\ngoals = false\nprofile_only = true\n"
	common := "[features]\ngoals = true\nweb_search = true\n"
	merged := mergeRelayCommonConfig(profile, common)
	features := tableValues(merged, "features")
	if features["goals"] != "false" {
		t.Fatalf("provider goals override was not preserved: %q", features["goals"])
	}
	if features["web_search"] != "true" {
		t.Fatalf("common feature was not merged: %q", features["web_search"])
	}
	if features["profile_only"] != "true" {
		t.Fatalf("provider-only feature was lost: %q", features["profile_only"])
	}
	if strings.Count(merged, "[features]") != 1 {
		t.Fatalf("features table should be emitted once:\n%s", merged)
	}
}

func TestV1247RelayModelCatalogUsesProfileCatalogAfterTakingOverCCSwitch(t *testing.T) {
	settings := defaultSettings()
	profile := defaultRelayProfile()
	profile.ModelList = "gpt-route"
	profile.ConfigContents = "model_catalog_json = \"C:\\\\Users\\\\test\\\\.codex\\\\cc-switch-model-catalog.json\"\n"
	ccSwitch, err := relayConfigWithCommonAndLimits(settings, profile, profile.ConfigContents)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rootKeyString(ccSwitch, "model_catalog_json"), relayModelCatalogRelativePath(profile.ID); got != want {
		t.Fatalf("CCSwitch catalog was not replaced:\n%s", ccSwitch)
	}

	profile.ConfigContents = "model_catalog_json = \"my-company-catalog.json\"\n"
	external, err := relayConfigWithCommonAndLimits(settings, profile, profile.ConfigContents)
	if err != nil {
		t.Fatal(err)
	}
	if rootKeyString(external, "model_catalog_json") != "my-company-catalog.json" {
		t.Fatalf("external catalog was overwritten:\n%s", external)
	}
}

func TestV1247PureAPIKeySurvivesLiveSnapshotImport(t *testing.T) {
	settings := defaultSettings()
	profile := defaultRelayProfile()
	profile.ID = "pure"
	profile.RelayMode = "pureApi"
	profile.APIKey = "stored-provider-key"
	settings.RelayProfiles = []relayProfile{profile}
	settings.ActiveRelayID = profile.ID

	updated, found := updateRelayProfileSnapshot(settings, profile.ID, "model = \"gpt-test\"\n", `{ "OPENAI_API_KEY": "live-auth-key" }`)
	if !found {
		t.Fatal("profile snapshot target was not found")
	}
	if updated.RelayProfiles[0].APIKey != "stored-provider-key" {
		t.Fatalf("pure API key was replaced by live auth: %#v", updated.RelayProfiles[0])
	}
}

func TestV1247Sub2APIBillingEndpointAndStrictResponse(t *testing.T) {
	for rawURL, expected := range map[string]string{
		"https://example.test":                    "https://example.test/v1/sub2api/billing",
		"https://example.test/v1/":                "https://example.test/v1/sub2api/billing",
		"https://example.test/v2/sub2api/billing": "https://example.test/v2/sub2api/billing",
	} {
		if actual := sub2APIBillingEndpoint(rawURL); actual != expected {
			t.Fatalf("sub2api endpoint for %q = %q, want %q", rawURL, actual, expected)
		}
	}

	var requestCount int
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.URL.Path != "/v1/sub2api/billing" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("authorization") != "Bearer billing-key" || request.Header.Get("x-api-key") != "billing-key" {
			t.Errorf("billing credentials missing")
		}
		if request.Header.Get("user-agent") != "BillingClient/1.0" {
			t.Errorf("user-agent = %q", request.Header.Get("user-agent"))
		}
		response.Header().Set("content-type", "application/json")
		_, _ = response.Write([]byte(`{
			"object":"sub2api.key_billing",
			"schema_version":1,
			"billing_scope":"token",
			"group_rate_multiplier":0.8,
			"user_rate_multiplier":0.6,
			"resolved_rate_multiplier":0.6,
			"peak_rate_enabled":true,
			"peak_rate_multiplier":1.5,
			"applied_peak_multiplier":1.5,
			"effective_rate_multiplier":0.9,
			"observed_at":"2026-08-15T10:00:00Z"
		}`))
	}))
	defer upstream.Close()
	profile := defaultRelayProfile()
	profile.Name = "Billing"
	profile.RelayMode = "pureApi"
	profile.BaseURL = upstream.URL + "/v1"
	profile.UpstreamBaseURL = profile.BaseURL
	profile.UserAgent = "BillingClient/1.0"
	missingKey := (&server{}).fetchSub2APIBilling(context.Background(), map[string]any{"profile": profile})
	if missingKey["status"] != "failed" || requestCount != 0 {
		t.Fatalf("missing key should fail without an upstream request: %#v, requests = %d", missingKey, requestCount)
	}
	profile.APIKey = "billing-key"
	result := (&server{}).fetchSub2APIBilling(context.Background(), map[string]any{"profile": profile})
	if result["status"] != "ok" || result["effectiveRateMultiplier"] != 0.9 || requestCount != 1 {
		t.Fatalf("billing result = %#v, requests = %d", result, requestCount)
	}

	invalid := sub2APIBillingResponse{
		Object:                  "wrong.object",
		SchemaVersion:           1,
		BillingScope:            "token",
		GroupRateMultiplier:     1,
		ResolvedRateMultiplier:  1,
		EffectiveRateMultiplier: 1,
		ObservedAt:              "now",
	}
	if err := validateSub2APIBillingResponse(invalid); err == nil {
		t.Fatal("invalid Sub2API schema was accepted")
	}
}

func TestV1247ProviderDoctorChecksModelsAndRealRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("authorization") != "Bearer doctor-key" {
			t.Errorf("authorization header missing")
		}
		response.Header().Set("content-type", "application/json")
		switch request.URL.Path {
		case "/v1/models":
			_, _ = response.Write([]byte(`{"data":[{"id":"doctor-model"}]}`))
		case "/v1/responses":
			_, _ = response.Write([]byte(`{"id":"resp_doctor","status":"completed","output":[]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer upstream.Close()
	profile := defaultRelayProfile()
	profile.Name = "Doctor"
	profile.RelayMode = "pureApi"
	profile.BaseURL = upstream.URL + "/v1"
	profile.UpstreamBaseURL = profile.BaseURL
	profile.APIKey = "doctor-key"
	profile.TestModel = "doctor-model"
	result := (&server{}).diagnoseRelayProfile(context.Background(), map[string]any{"profile": profile})
	if result["status"] != "ok" || !strings.Contains(stringFromAny(result["summary"]), "诊断通过") {
		t.Fatalf("doctor result = %#v", result)
	}
	var checks []providerDoctorCheck
	if err := remarshal(result["checks"], &checks); err != nil || len(checks) != 3 {
		t.Fatalf("doctor checks = %#v, %v", result["checks"], err)
	}
	for _, check := range checks {
		if check.Status != "ok" {
			t.Fatalf("doctor check failed: %#v", check)
		}
	}

	missing := profile
	missing.APIKey = ""
	failed := (&server{}).diagnoseRelayProfile(context.Background(), map[string]any{"profile": missing})
	if failed["status"] != "failed" || !strings.Contains(stringFromAny(failed["recommendation"]), "API Key") {
		t.Fatalf("missing-config doctor result = %#v", failed)
	}
}

func TestV1247LLMProxyValidationAndForwarding(t *testing.T) {
	for _, rawURL := range []string{
		"http://api.example.test/v1",
		"https://localhost/v1",
		"https://127.0.0.1/v1",
		"https://10.0.0.1/v1",
		"https://100.64.0.1/v1",
		"https://192.0.2.1/v1",
		"https://[::1]/v1",
		"https://user:pass@example.test/v1",
	} {
		if _, err := validateLLMProxyURL(rawURL); err == nil {
			t.Fatalf("blocked URL accepted: %s", rawURL)
		}
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if request.Header.Get("authorization") != "Bearer test" {
			t.Errorf("authorization header missing")
		}
		if request.Header.Get("x-not-allowed") != "" {
			t.Errorf("disallowed header leaked")
		}
		w.Header().Set("content-type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"answer":true}`))
	}))
	defer server.Close()
	originalFactory := newLLMProxyHTTPClient
	newLLMProxyHTTPClient = func(timeout time.Duration) *http.Client {
		serverTransport := server.Client().Transport
		return &http.Client{
			Timeout: timeout,
			Transport: llmProxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				clone := request.Clone(request.Context())
				clone.URL.Scheme = "https"
				clone.URL.Host = strings.TrimPrefix(server.URL, "https://")
				return serverTransport.RoundTrip(clone)
			}),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	defer func() { newLLMProxyHTTPClient = originalFactory }()

	result := llmProxyValue(map[string]any{
		"url":     "https://api.example.test/v1/responses",
		"method":  "POST",
		"body":    `{"model":"test"}`,
		"headers": map[string]any{"authorization": "Bearer test", "x-not-allowed": "secret"},
	})
	if result["status"] != "ok" || result["http_status"] != http.StatusOK || result["body_json"] == nil {
		t.Fatalf("LLM proxy result mismatch: %#v", result)
	}
}

func TestV1247ThreadUsageHistoryAndToolImages(t *testing.T) {
	temp := t.TempDir()
	rollout := filepath.Join(temp, "rollout.jsonl")
	contents := strings.Join([]string{
		`{"timestamp":"2026-08-15T10:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1"}}`,
		`{"timestamp":"2026-08-15T10:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1200,"output_tokens":120,"total_tokens":1320,"cached_input_tokens":900},"total_token_usage":{"total_tokens":2400},"model_context_window":258400}}}`,
	}, "\n")
	if err := os.WriteFile(rollout, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	history, err := readRolloutUsageHistory(rollout, "thread-1")
	if err != nil || len(history) != 1 {
		t.Fatalf("usage history mismatch: history=%#v err=%v", history, err)
	}
	entry := history[0].(map[string]any)
	if entry["turn_id"] != "turn-1" || entry["conversation_id"] != "local:thread-1" {
		t.Fatalf("usage entry mismatch: %#v", entry)
	}

	messages := []any{
		map[string]any{"role": "user", "content": "inspect the browser"},
		map[string]any{"role": "tool", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/tool.png"}}}},
	}
	rounds := collectRecentVLMImageMessages(messages, 10)
	if len(rounds) != 1 || rounds[0].Message != 1 || latestVLMImageMessageIndex(messages) != 1 {
		t.Fatalf("tool image was not collected: %#v", rounds)
	}
}

func TestV1247BridgeRoutesStayAdFree(t *testing.T) {
	runtime := &launcherRuntime{settings: defaultSettings()}
	originalOpenTransient := openTransientManagerAppFunc
	openTransientManagerAppFunc = func() error { return nil }
	defer func() { openTransientManagerAppFunc = originalOpenTransient }()
	for _, route := range []string{"/llm-proxy", "/thread-usage-history", "/manager/open-transient", "/remote-control-session/recover"} {
		result := runtime.handleBridgeRequest(route, json.RawMessage(`{}`))
		if result["message"] == "Unknown bridge path" {
			t.Fatalf("bridge route missing: %s", route)
		}
		if !backendRouteKnown(route) {
			t.Fatalf("HTTP helper route missing: %s", route)
		}
	}
	ads := runtime.handleBridgeRequest("/ads", json.RawMessage(`{}`))
	if ads["message"] != "Unknown bridge path" {
		t.Fatalf("ads route should remain absent: %#v", ads)
	}
}

func TestV1247RemoteControlSessionRecoveryCatalogThenRollout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	threadID := "0198f7f0-1111-7777-8000-111111111111"
	rollout := filepath.Join(codexHome, "sessions", "rollout-remote.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"` + threadID + `","cwd":"/project","model_provider":"openai","timestamp":"2026-08-15T10:00:00Z"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"remote"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT, model_provider TEXT, cwd TEXT, title TEXT, has_user_event INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.RelayProfiles[0].RelayMode = "mixedApi"
	settings.RelayProfiles[0].ConfigContents = "model_provider = \"vendor_remote\"\n"
	settings.ActiveRelayID = settings.RelayProfiles[0].ID
	if err := atomicWriteJSON(settingsPath(), settings); err != nil {
		t.Fatal(err)
	}
	result := recoverRemoteControlSessionValue(map[string]any{"thread_id": threadID})
	if result["status"] != "ok" {
		t.Fatalf("catalog recovery failed: %#v", result)
	}
	db, err = openSQLite(filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var provider string
	if err := db.QueryRow(`SELECT model_provider FROM threads WHERE id = ?`, threadID).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if provider != "vendor_remote" {
		t.Fatalf("catalog provider = %q", provider)
	}
	completed, err := runPendingRemoteControlRecoveries(codexHome)
	if err != nil || completed != 1 {
		t.Fatalf("pending recovery = %d, %v", completed, err)
	}
	data, err := os.ReadFile(rollout)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := splitFirstLine(string(data))
	if providerFromSessionFirstLine(first) != "vendor_remote" {
		t.Fatalf("rollout provider not recovered: %s", first)
	}
}

func TestV1247ProviderSyncTargetsCombineConfigRolloutSQLiteAndSaved(t *testing.T) {
	home := t.TempDir()
	config := "model_provider = \"current_vendor\"\n\n[model_providers.config_vendor]\nbase_url = \"https://example.test/v1\"\n"
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), config, 0o600)
	rollout := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"thread-one","model_provider":"rollout_vendor"}}`,
		`{"type":"session_meta","payload":{"id":"thread-one","model_provider":"second_rollout"}}`,
	}, "\n") + "\n"
	providerSyncWriteTestFile(t, filepath.Join(home, "archived_sessions", "rollout-targets.jsonl"), rollout, 0o600)

	dbPath := filepath.Join(home, "state_5.sqlite")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, model_provider TEXT); INSERT INTO threads VALUES ('one', 'sqlite_vendor')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE local_thread_catalog (id TEXT PRIMARY KEY, model_provider TEXT); INSERT INTO local_thread_catalog VALUES ('two', 'catalog_vendor')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	settings := defaultSettings()
	settings.ProviderSyncManualProviders = []string{"manual.vendor", "invalid provider"}
	settings.ProviderSyncSavedProviders = []string{"saved-vendor"}
	result := discoverProviderSyncTargets(home, settings)
	if result.currentProvider != "current_vendor" || len(result.targets) == 0 || result.targets[0].ID != "current_vendor" {
		t.Fatalf("current provider was not sorted first: %#v", result)
	}
	want := []string{"openai", "config_vendor", "rollout_vendor", "second_rollout", "sqlite_vendor", "catalog_vendor", "manual.vendor", "saved-vendor"}
	for _, id := range want {
		found := false
		for _, target := range result.targets {
			if target.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("provider target %q missing from %#v", id, result.targets)
		}
	}
	for _, target := range result.targets {
		if target.ID == "invalid provider" {
			t.Fatalf("invalid provider target was accepted: %#v", result.targets)
		}
	}
	if invalid := runProviderSyncTarget(home, "bad provider"); invalid.Status != "skipped" {
		t.Fatalf("invalid explicit provider should be skipped: %#v", invalid)
	}
}

func TestV1247ProviderSyncRewritesEverySessionMetaAndPreservesLateTail(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "rollout-multiple-meta.jsonl")
	original := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"notice","message":"prefix"}}`,
		`{"type":"session_meta","payload":{"id":"thread-multiple","cwd":"/project","model_provider":"target_vendor"}}`,
		`{"type":"session_meta","payload":{"id":"thread-multiple","cwd":"/project","model_provider":"openai"}}`,
		`{"type":"session_meta","payload":{"id":"thread-multiple","cwd":"/project","model_provider":"legacy_vendor"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"keep"}}`,
	}, "\r\n") + "\r\n"
	providerSyncWriteTestFile(t, path, original, 0o600)
	changes, err := collectSessionChanges(home, "target_vendor")
	if err != nil || len(changes) != 1 || len(changes[0].OriginalSessionMetaLines) != 3 {
		t.Fatalf("multi-meta collection = %#v, %v", changes, err)
	}
	lateTail := `{"type":"event_msg","payload":{"type":"assistant_message","message":"late"}}` + "\r\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(lateTail)
	_ = file.Close()
	if err := applySessionChanges(changes); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), lateTail) || strings.Count(string(updated), `"model_provider":"target_vendor"`) != 3 {
		t.Fatalf("all session metadata was not rewritten or tail was lost:\n%s", updated)
	}
	if err := restoreSessionChanges(changes); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(restored), `"model_provider":"target_vendor"`) != 1 || !strings.Contains(string(restored), `"model_provider":"openai"`) || !strings.Contains(string(restored), `"model_provider":"legacy_vendor"`) || !strings.Contains(string(restored), lateTail) {
		t.Fatalf("multi-meta rollback mismatch:\n%s", restored)
	}
}
