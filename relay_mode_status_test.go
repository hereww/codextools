package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayStatusReportsConfiguredModeDirectly(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{
		{ID: "relay", Name: "Relay", RelayMode: "pureApi", Protocol: "responses"},
	}
	settings.ActiveRelayID = "relay"

	home := t.TempDir()
	status := relayStatusFromHome(home, settings)
	if got := stringFromAny(status["activeMode"]); got != "pureApi" {
		t.Fatalf("relay status active mode mismatch: got %q want %q", got, "pureApi")
	}
	if got := stringFromAny(status["selectedMode"]); got != "pureApi" {
		t.Fatalf("relay status selected mode mismatch: got %q want %q", got, "pureApi")
	}
	if got := stringFromAny(status["appliedMode"]); got != "official" {
		t.Fatalf("missing live relay config should report official as applied: got %q", got)
	}

	config := `model_provider = "CodexPlusPlus"

[model_providers.CodexPlusPlus]
requires_openai_auth = true
experimental_bearer_token = "key"
base_url = "https://relay.example/v1"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write live relay config: %v", err)
	}
	status = relayStatusFromHome(home, settings)
	if got := stringFromAny(status["appliedMode"]); got != "pureApi" {
		t.Fatalf("unmarked live relay config should conservatively report pure API as applied: got %q", got)
	}
	settings.RelayProfiles[0].RelayMode = "official"
	status = relayStatusFromHome(home, settings)
	if got := stringFromAny(status["appliedMode"]); got != "pureApi" {
		t.Fatalf("unmarked live relay config should not follow the selected mode: got %q", got)
	}
}

func TestRelayStatusReadsAppliedModeFromLiveConfigMarker(t *testing.T) {
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{ID: "selected", Name: "Selected", RelayMode: "official", Protocol: "responses"}}
	settings.ActiveRelayID = "selected"
	apiConfig := `model_provider = "CodexPlusPlus"

[model_providers.CodexPlusPlus]
requires_openai_auth = true
experimental_bearer_token = "key"
base_url = "https://relay.example/v1"
`

	for _, mode := range []string{"mixedApi", "pureApi", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			contents := withRelayAppliedModeMarker(apiConfig, mode)
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(contents), 0o600); err != nil {
				t.Fatalf("write marked live relay config: %v", err)
			}
			status := relayStatusFromHome(home, settings)
			if got := stringFromAny(status["appliedMode"]); got != mode {
				t.Fatalf("marked live relay config applied mode mismatch: got %q want %q", got, mode)
			}
		})
	}

	home := t.TempDir()
	officialConfig := withRelayAppliedModeMarker(`model_provider = "openai"`+"\n", "official")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(officialConfig), 0o600); err != nil {
		t.Fatalf("write marked official config: %v", err)
	}
	status := relayStatusFromHome(home, settings)
	if got := stringFromAny(status["appliedMode"]); got != "official" {
		t.Fatalf("official live config applied mode mismatch: got %q", got)
	}
}

func TestRelayAppliedModeMarkerPreservesBOMCRLFAndDeduplicates(t *testing.T) {
	contents := "\ufeff" + relayAppliedModeMarkerPrefix + "pureApi\r\n" +
		"# existing header\r\n" +
		`model_provider = "CodexPlusPlus"` + "\r\n" +
		relayAppliedModeMarkerPrefix + "aggregate\r\n"

	updated := withRelayAppliedModeMarker(contents, "mixedApi")
	if !strings.HasPrefix(updated, "\ufeff"+relayAppliedModeMarkerPrefix+"mixedApi\r\n") {
		t.Fatalf("updated marker did not preserve BOM and CRLF: %q", updated)
	}
	if got := strings.Count(updated, relayAppliedModeMarkerPrefix); got != 1 {
		t.Fatalf("updated config should contain exactly one marker, got %d in %q", got, updated)
	}
	if strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatalf("updated config introduced a bare LF into CRLF content: %q", updated)
	}
	if !strings.Contains(updated, "# existing header\r\n") || !strings.Contains(updated, `model_provider = "CodexPlusPlus"`+"\r\n") {
		t.Fatalf("updated marker did not preserve config content: %q", updated)
	}
	if got := relayAppliedModeMarker(updated); got != "mixedApi" {
		t.Fatalf("updated marker parse mismatch: got %q", got)
	}
	if again := withRelayAppliedModeMarker(updated, "mixedApi"); again != updated {
		t.Fatalf("updating an existing marker should be idempotent:\nfirst:  %q\nsecond: %q", updated, again)
	}
}

func TestRelaySnapshotBOMRootKeyAcrossModes(t *testing.T) {
	const sourceConfig = "\ufeff" + `model_provider = "CodexPlusPlus"` + "\r\n" +
		"# keep this profile comment\r\n\r\n" +
		"[model_providers.CodexPlusPlus]\r\n" +
		`name = "Previous relay"` + "\r\n" +
		`wire_api = "responses"` + "\r\n" +
		`requires_openai_auth = true` + "\r\n" +
		`base_url = "https://previous.example/v1"` + "\r\n" +
		`experimental_bearer_token = "previous-key"` + "\r\n"

	tests := []struct {
		mode             string
		expectedProvider string
	}{
		{mode: "official", expectedProvider: "openai"},
		{mode: "mixedApi", expectedProvider: relayProvider},
		{mode: "aggregate", expectedProvider: relayProvider},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			settings := defaultSettings()
			relay := relayProfile{
				ID:             test.mode,
				Name:           test.mode,
				BaseURL:        "https://relay.example/v1",
				APIKey:         "relay-key",
				Protocol:       "responses",
				RelayMode:      test.mode,
				ConfigContents: sourceConfig,
			}
			if test.mode == "official" {
				// The official switch first derives its profile snapshot from the
				// currently applied relay config before relayConfigForWrite runs.
				relay = ensureRelaySnapshot(relay, sourceConfig, false)
			}

			generated, err := relayConfigForWrite(settings, relay)
			if err != nil {
				t.Fatalf("generate %s relay config: %v", test.mode, err)
			}
			assertBOMRelayConfig(t, generated, test.expectedProvider)
			if strings.Contains(generated, relayAppliedModeMarkerPrefix) {
				t.Fatalf("relayConfigForWrite should not add the applied-mode marker: %q", generated)
			}

			home := t.TempDir()
			if _, err := writeRelaySnapshot(home, settings, relay, false); err != nil {
				t.Fatalf("write %s relay snapshot: %v", test.mode, err)
			}
			written, err := os.ReadFile(filepath.Join(home, "config.toml"))
			if err != nil {
				t.Fatalf("read %s relay snapshot: %v", test.mode, err)
			}
			contents := string(written)
			assertBOMRelayConfig(t, contents, test.expectedProvider)
			if !strings.HasPrefix(contents, "\ufeff"+relayAppliedModeMarkerPrefix+test.mode+"\n") {
				t.Fatalf("written %s snapshot has an invalid BOM/marker prefix: %q", test.mode, contents)
			}
			if got := strings.Count(contents, relayAppliedModeMarkerPrefix); got != 1 {
				t.Fatalf("written %s snapshot has %d applied-mode markers: %q", test.mode, got, contents)
			}
		})
	}
}

func TestRelaySnapshotRollsBackFilesWhenAuthWriteFails(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	catalogPath := filepath.Join(home, filepath.FromSlash(relayModelCatalogRelativePath("relay")))
	authPath := filepath.Join(home, "auth.json")
	before := map[string]string{
		configPath:  "model_provider = \"openai\"\n",
		catalogPath: "{\"models\":[{\"slug\":\"existing\"}]}\n",
		authPath:    fakeChatGPTAuthJSON(t, "before@example.com"),
	}
	for path, contents := range before {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create parent for %s: %v", filepath.Base(path), err)
		}
		mode := os.FileMode(0o600)
		if path != authPath {
			mode = 0o644
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatalf("write initial %s: %v", filepath.Base(path), err)
		}
	}

	settings := defaultSettings()
	relay := relayProfile{
		ID:             "relay",
		BaseURL:        "https://relay.example/v1",
		APIKey:         "relay-key",
		Protocol:       "responses",
		RelayMode:      "mixedApi",
		ModelList:      "gpt-5.6",
		AuthContents:   fakeChatGPTAuthJSON(t, "after@example.com"),
		ConfigContents: before[configPath],
	}
	_, err := writeRelaySnapshotWithWriter(home, settings, relay, false, func(write relaySnapshotWrite) error {
		if write.Path == authPath {
			return errors.New("injected auth write failure")
		}
		return writeRelaySnapshotFile(write)
	})
	if err == nil || !strings.Contains(err.Error(), "injected auth write failure") {
		t.Fatalf("expected injected auth write failure, got %v", err)
	}
	for path, expected := range before {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read restored %s: %v", filepath.Base(path), readErr)
		}
		if string(got) != expected {
			t.Fatalf("failed relay apply did not restore %s:\n got: %s\nwant: %s", filepath.Base(path), got, expected)
		}
	}
}

func assertBOMRelayConfig(t *testing.T, contents, expectedProvider string) {
	t.Helper()
	if !strings.HasPrefix(contents, "\ufeff") || strings.Count(contents, "\ufeff") != 1 {
		t.Fatalf("config should preserve exactly one leading UTF-8 BOM: %q", contents)
	}
	// Structural TOML rewrites use splitLines; this pipeline intentionally emits
	// a trailing LF with all input CRLF line endings normalized to LF.
	if strings.Contains(contents, "\r") || !strings.HasSuffix(contents, "\n") {
		t.Fatalf("rewritten config should use LF with a trailing newline: %q", contents)
	}
	if got := rootKeyString(contents, "model_provider"); got != expectedProvider {
		t.Fatalf("rootKeyString(model_provider) = %q, want %q", got, expectedProvider)
	}

	values := []string{}
	for _, line := range strings.Split(strings.TrimPrefix(contents, "\ufeff"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(left) == "model_provider" {
			values = append(values, strings.Trim(strings.TrimSpace(right), `"`))
		}
	}
	if len(values) != 1 || values[0] != expectedProvider {
		t.Fatalf("config model_provider root keys = %#v, want exactly [%q]:\n%s", values, expectedProvider, contents)
	}
}

func TestFailedAPIModeSwitchKeepsPreviouslyAppliedMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("create codex home: %v", err)
	}

	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:        "relay",
		Name:      "Relay",
		BaseURL:   "https://relay.example/v1",
		APIKey:    "key",
		RelayMode: "pureApi",
		Protocol:  "responses",
	}}
	settings.ActiveRelayID = "relay"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("save initial pure API settings: %v", err)
	}
	if _, err := writeRelaySnapshot(codexHome, settings, settings.RelayProfiles[0], true); err != nil {
		t.Fatalf("write initial pure API snapshot: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read initial pure API snapshot: %v", err)
	}

	settings.RelayProfiles[0].RelayMode = "mixedApi"
	settings.RelayProfiles[0].OfficialMixAPIKey = true
	settings.RelayProfiles[0].AuthContents = fakeChatGPTAuthJSON(t, "mixed@example.com")
	if err := saveSettings(settings); err != nil {
		t.Fatalf("save selected mixed API settings: %v", err)
	}
	previousDetector := detectProviderSyncActiveProcesses
	detectProviderSyncActiveProcesses = func() ([]string, error) {
		return []string{"ChatGPT"}, nil
	}
	t.Cleanup(func() { detectProviderSyncActiveProcesses = previousDetector })

	result := (&server{}).applyRelayInjection(false)
	if got := stringFromAny(result["status"]); got != "failed" {
		t.Fatalf("mixed API switch with an active history writer should fail: %#v", result)
	}
	if got := stringFromAny(result["selectedMode"]); got != "mixedApi" {
		t.Fatalf("selected mode mismatch after failed switch: got %q", got)
	}
	if got := stringFromAny(result["appliedMode"]); got != "pureApi" {
		t.Fatalf("failed switch must keep the previously applied mode: got %q", got)
	}
	after, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read config after failed switch: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed switch changed live config:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestModeSwitchAndHistorySyncCommandsAllowCompleteBackups(t *testing.T) {
	for _, command := range []string{
		"sync_providers_now",
		"apply_relay_injection",
		"apply_pure_api_injection",
		"clear_relay_injection",
	} {
		if got := commandTimeout(command); got != 5*time.Minute {
			t.Fatalf("%s timeout mismatch: got %s want %s", command, got, 5*time.Minute)
		}
	}
}
