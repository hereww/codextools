package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func init() {
	detectProviderSyncActiveProcesses = func() ([]string, error) { return nil, nil }
	acquireProviderSyncLauncherGuard = func() (func(), error) { return func() {}, nil }
}

func TestProviderSyncSessionRewritePreservesAppendedTailAndMode(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "2026", "07", "rollout-tail.jsonl")
	firstLine := providerSyncTestSessionMeta("thread-tail", "openai")
	initialTail := `{"type":"response_item","payload":{"type":"message","role":"user","content":"before scan"}}` + "\r\n"
	lateTail := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":"appended after scan"}}` + "\r\n"
	providerSyncWriteTestFile(t, path, firstLine+"\r\n"+initialTail, 0o600)

	changes, err := collectSessionChanges(home, relayProvider)
	if err != nil || len(changes) != 1 {
		t.Fatalf("collectSessionChanges() = %#v, %v", changes, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open session for append: %v", err)
	}
	if _, err := file.WriteString(lateTail); err != nil {
		_ = file.Close()
		t.Fatalf("append late tail: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close appended session: %v", err)
	}

	if err := applySessionChanges(changes); err != nil {
		t.Fatalf("applySessionChanges() failed: %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated session: %v", err)
	}
	want := changes[0].NextFirstLine + "\r\n" + initialTail + lateTail
	if string(updated) != want {
		t.Fatalf("session tail changed or was lost:\n got: %s\nwant: %s", updated, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat updated session: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode changed: got %o want 600", info.Mode().Perm())
	}
}

func TestProviderSyncSessionRewriteFailureRollsBackAppliedFiles(t *testing.T) {
	home := t.TempDir()
	paths := []string{
		filepath.Join(home, "sessions", "rollout-1.jsonl"),
		filepath.Join(home, "sessions", "rollout-2.jsonl"),
	}
	originals := map[string]string{}
	for index, path := range paths {
		contents := providerSyncTestSessionMeta("thread-"+string(rune('1'+index)), "openai") + "\n" +
			`{"type":"response_item","payload":{"type":"message","role":"user","content":"keep this tail"}}` + "\n"
		originals[path] = contents
		providerSyncWriteTestFile(t, path, contents, 0o600)
	}
	changes, err := collectSessionChanges(home, relayProvider)
	if err != nil || len(changes) != 2 {
		t.Fatalf("collectSessionChanges() = %#v, %v", changes, err)
	}
	if err := os.Remove(changes[1].Path); err != nil {
		t.Fatalf("remove second session to force failure: %v", err)
	}

	err = applySessionChanges(changes)
	if err == nil || !strings.Contains(err.Error(), "已回滚") {
		t.Fatalf("expected a rolled-back write failure, got %v", err)
	}
	got, err := os.ReadFile(changes[0].Path)
	if err != nil {
		t.Fatalf("read first session after rollback: %v", err)
	}
	if string(got) != originals[changes[0].Path] {
		t.Fatalf("first session was not rolled back:\n got: %s\nwant: %s", got, originals[changes[0].Path])
	}
}

func TestProviderSyncSessionMetadataPreservesUnknownLargeNumbers(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "rollout-large-number.jsonl")
	firstLine := `{"type":"session_meta","unknown_top":9007199254740993,"payload":{"id":"thread-large","cwd":"/project","model_provider":"openai","unknown_integer":9007199254740995,"unknown_unsigned":18446744073709551615,"unknown_nested":{"exact":9223372036854775807},"unknown_array":[1,true,null,"keep"]}}`
	providerSyncWriteTestFile(t, path, firstLine+"\n", 0o600)

	changes, err := collectSessionChanges(home, relayProvider)
	if err != nil || len(changes) != 1 {
		t.Fatalf("collectSessionChanges() = %#v, %v", changes, err)
	}
	for _, exactNumber := range []string{"9007199254740993", "9007199254740995", "18446744073709551615", "9223372036854775807"} {
		if !strings.Contains(changes[0].NextFirstLine, exactNumber) {
			t.Fatalf("rewritten metadata lost exact integer %s: %s", exactNumber, changes[0].NextFirstLine)
		}
	}
	original := providerSyncDecodeJSONUseNumber(t, firstLine)
	next := providerSyncDecodeJSONUseNumber(t, changes[0].NextFirstLine)
	originalPayload, ok := original["payload"].(map[string]any)
	if !ok {
		t.Fatalf("original payload missing: %#v", original)
	}
	originalPayload["model_provider"] = relayProvider
	if !reflect.DeepEqual(original, next) {
		t.Fatalf("rewriting model_provider changed unknown metadata:\n original: %#v\nnext: %#v", original, next)
	}
}

func TestProviderSyncBackupCopiesCompleteSessionAndPropagatesCopyFailure(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "2026", "07", "rollout-backup.jsonl")
	contents := providerSyncTestSessionMeta("thread-backup", "openai") + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":"full history body"}}` + "\n"
	providerSyncWriteTestFile(t, path, contents, 0o600)
	changes, err := collectSessionChanges(home, relayProvider)
	if err != nil || len(changes) != 1 {
		t.Fatalf("collectSessionChanges() = %#v, %v", changes, err)
	}

	backupDir, err := createProviderSyncBackup(home, relayProvider, changes)
	if err != nil {
		t.Fatalf("createProviderSyncBackup() failed: %v", err)
	}
	relative, err := filepath.Rel(home, path)
	if err != nil {
		t.Fatalf("relative session path: %v", err)
	}
	backupPath := filepath.Join(backupDir, "history", relative)
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read complete session backup: %v", err)
	}
	if string(backup) != contents {
		t.Fatalf("session backup is incomplete:\n got: %s\nwant: %s", backup, contents)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat complete session backup: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode changed: got %o want 600", info.Mode().Perm())
	}
	var manifest []map[string]any
	if err := readJSON(filepath.Join(backupDir, "session-meta-backup.json"), &manifest); err != nil {
		t.Fatalf("read backup manifest: %v", err)
	}
	if len(manifest) != 1 || strings.TrimSpace(stringFromAny(manifest[0]["backupPath"])) == "" {
		t.Fatalf("backup manifest does not reference the complete history copy: %#v", manifest)
	}
	if _, legacyTail := manifest[0]["separator"]; legacyTail {
		t.Fatalf("backup manifest should not duplicate the complete history tail: %#v", manifest[0])
	}

	failedHome := t.TempDir()
	failedPath := filepath.Join(failedHome, "sessions", "rollout-missing.jsonl")
	providerSyncWriteTestFile(t, failedPath, providerSyncTestSessionMeta("thread-missing", "openai")+"\n", 0o600)
	failedChanges, err := collectSessionChanges(failedHome, relayProvider)
	if err != nil || len(failedChanges) != 1 {
		t.Fatalf("collect failure fixture = %#v, %v", failedChanges, err)
	}
	if err := os.Remove(failedPath); err != nil {
		t.Fatalf("remove backup source: %v", err)
	}
	if _, err := createProviderSyncBackup(failedHome, relayProvider, failedChanges); err == nil {
		t.Fatal("missing session backup source should fail instead of being ignored")
	}
	entries, readErr := os.ReadDir(filepath.Join(failedHome, "backups_state", "provider-sync"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read failed backup root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("partial failed backup should be cleaned up: %#v", entries)
	}
}

func TestProviderSyncSQLiteUpdatesRollbackAsOneTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	statements := []string{
		`CREATE TABLE threads (id TEXT PRIMARY KEY, model_provider TEXT, thread_source TEXT, has_user_event INTEGER, cwd TEXT)`,
		`INSERT INTO threads (id, model_provider, thread_source, has_user_event, cwd) VALUES ('thread-1', 'openai', '', 0, '/old')`,
		`CREATE TRIGGER fail_user_event BEFORE UPDATE OF has_user_event ON threads BEGIN SELECT RAISE(ABORT, 'forced transaction failure'); END`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("prepare sqlite fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite fixture: %v", err)
	}

	updated, err := applySQLiteUpdates(path, relayProvider, []sessionChange{{
		ThreadID:     "thread-1",
		CWD:          "/new",
		HasUserEvent: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "forced transaction failure") {
		t.Fatalf("expected forced transaction failure, got rows=%d err=%v", updated, err)
	}
	if updated != 0 {
		t.Fatalf("rolled-back transaction reported committed rows: %d", updated)
	}

	db, err = openSQLite(path)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer db.Close()
	var provider, source, cwd string
	var hasUser int
	if err := db.QueryRow(`SELECT model_provider, thread_source, has_user_event, cwd FROM threads WHERE id = 'thread-1'`).Scan(&provider, &source, &hasUser, &cwd); err != nil {
		t.Fatalf("read rolled-back sqlite row: %v", err)
	}
	if provider != "openai" || source != "" || hasUser != 0 || cwd != "/old" {
		t.Fatalf("sqlite transaction left partial updates: provider=%q source=%q hasUser=%d cwd=%q", provider, source, hasUser, cwd)
	}
}

func TestRunProviderSyncSkipsWithoutChangesWhenChatGPTIsActive(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-active.jsonl")
	original := providerSyncTestSessionMeta("thread-active", "openai") + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"stay untouched"}}` + "\n"
	providerSyncWriteTestFile(t, path, original, 0o600)
	globalPath := filepath.Join(home, ".codex-global-state.json")
	globalOriginal := `{"electron-saved-workspace-roots":["/old"]}` + "\n"
	providerSyncWriteTestFile(t, globalPath, globalOriginal, 0o600)

	previous := detectProviderSyncActiveProcesses
	detectProviderSyncActiveProcesses = func() ([]string, error) { return []string{"ChatGPT"}, nil }
	t.Cleanup(func() { detectProviderSyncActiveProcesses = previous })
	result := runProviderSync(home)

	if result.Status != "skipped" || !strings.Contains(result.Message, "仍在运行") {
		t.Fatalf("active ChatGPT should skip provider sync: %#v", result)
	}
	providerSyncAssertFileContents(t, path, original)
	providerSyncAssertFileContents(t, globalPath, globalOriginal)
	if fileExists(filepath.Join(home, "backups_state", "provider-sync")) {
		entries, err := os.ReadDir(filepath.Join(home, "backups_state", "provider-sync"))
		if err != nil {
			t.Fatalf("read provider sync backups: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("active-process skip should not create a backup: %#v", entries)
		}
	}
}

func TestRunProviderSyncGlobalFailureRestoresSessionAndLeavesSQLiteUntouched(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-global-failure.jsonl")
	original := providerSyncTestSessionMeta("thread-global", "openai") + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"restore me"}}` + "\n"
	providerSyncWriteTestFile(t, path, original, 0o600)
	globalPath := filepath.Join(home, ".codex-global-state.json")
	globalOriginal := `{"electron-saved-workspace-roots":["/old"],"project-order":["/old"]}` + "\n"
	providerSyncWriteTestFile(t, globalPath, globalOriginal, 0o600)
	dbPath := filepath.Join(home, "state_5.sqlite")
	providerSyncPrepareSafetyDB(t, dbPath, false)

	previous := applyProviderSyncGlobalStateUpdate
	applyProviderSyncGlobalStateUpdate = func(path string, changes []sessionChange) (int, error) {
		count, err := applyGlobalStateUpdate(path, changes)
		if err != nil {
			return count, err
		}
		return count, errors.New("forced global state failure")
	}
	t.Cleanup(func() { applyProviderSyncGlobalStateUpdate = previous })
	result := runProviderSync(home)

	if result.Status != "failed" || result.Partial || result.RollbackStatus != "rolled_back" || !strings.Contains(result.Message, "rolled back") {
		t.Fatalf("global-state failure should roll back provider sync: %#v", result)
	}
	providerSyncAssertFileContents(t, path, original)
	providerSyncAssertFileContents(t, globalPath, globalOriginal)
	provider, _, _, _ := providerSyncReadSafetyDBRow(t, dbPath)
	if provider != "openai" {
		t.Fatalf("SQLite changed before global-state step completed: %q", provider)
	}
}

func TestRunProviderSyncSQLiteFailureRestoresSessionAndGlobalState(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-sqlite-failure.jsonl")
	original := providerSyncTestSessionMeta("thread-1", "openai") + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"restore every store"}}` + "\n"
	providerSyncWriteTestFile(t, path, original, 0o600)
	globalPath := filepath.Join(home, ".codex-global-state.json")
	globalOriginal := `{"electron-saved-workspace-roots":["/old"],"project-order":["/old"]}` + "\n"
	providerSyncWriteTestFile(t, globalPath, globalOriginal, 0o600)
	dbPath := filepath.Join(home, "state_5.sqlite")
	providerSyncPrepareSafetyDB(t, dbPath, true)

	result := runProviderSync(home)

	if result.Status != "failed" || result.Partial || result.RollbackStatus != "rolled_back" || !strings.Contains(result.Message, "rolled back") {
		t.Fatalf("SQLite failure should roll back provider sync: %#v", result)
	}
	providerSyncAssertFileContents(t, path, original)
	providerSyncAssertFileContents(t, globalPath, globalOriginal)
	provider, source, hasUser, cwd := providerSyncReadSafetyDBRow(t, dbPath)
	if provider != "openai" || source != "" || hasUser != 0 || cwd != "/old" {
		t.Fatalf("SQLite transaction left mixed provider state: provider=%q source=%q hasUser=%d cwd=%q", provider, source, hasUser, cwd)
	}
}

func TestRunProviderSyncInvalidGlobalStateFailsClosed(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-invalid-global.jsonl")
	original := providerSyncTestSessionMeta("thread-invalid-global", "openai") + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"do not overwrite state"}}` + "\n"
	providerSyncWriteTestFile(t, path, original, 0o600)
	globalPath := filepath.Join(home, ".codex-global-state.json")
	invalidGlobal := `{"electron-saved-workspace-roots":["/old"],` + "\n"
	providerSyncWriteTestFile(t, globalPath, invalidGlobal, 0o600)

	result := runProviderSync(home)

	if result.Status != "skipped" || !strings.Contains(result.Message, "解析 global state 失败") {
		t.Fatalf("invalid global state should fail closed before backup: %#v", result)
	}
	if result.BackupDir != nil {
		t.Fatalf("invalid global state should stop before backup: %#v", result)
	}
	providerSyncAssertFileContents(t, path, original)
	providerSyncAssertFileContents(t, globalPath, invalidGlobal)
	if _, err := applyGlobalStateUpdate(globalPath, []sessionChange{{ThreadID: "thread-invalid-global", CWD: "/new", HasUserEvent: true}}); err == nil {
		t.Fatal("applyGlobalStateUpdate should not overwrite invalid existing JSON")
	}
}

func TestNormalizedGlobalStatePreservesProjectlessThreadIDs(t *testing.T) {
	projectless := []any{"thread-1", "thread-2", map[string]any{"legacy": true}}
	state := map[string]any{"projectless-thread-ids": projectless}

	got := normalizedGlobalState(state, nil)
	data, err := json.Marshal(got["projectless-thread-ids"])
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(projectless)
	if string(data) != string(want) {
		t.Fatalf("projectless thread IDs changed: got %s, want %s", data, want)
	}
}

func TestProviderSyncUnreadableGlobalStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex-global-state.json")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create unreadable global-state path: %v", err)
	}
	changes := []sessionChange{{ThreadID: "thread-directory", CWD: "/project", HasUserEvent: true}}
	if _, err := countGlobalStateUpdates(path, changes); err == nil {
		t.Fatal("countGlobalStateUpdates should fail on an unreadable existing path")
	}
	if _, err := applyGlobalStateUpdate(path, changes); err == nil {
		t.Fatal("applyGlobalStateUpdate should fail on an unreadable existing path")
	}
	if !isDir(path) {
		t.Fatal("fail-closed global-state handling replaced the existing path")
	}
}

func TestRunProviderSyncFailureAfterBackupIsFailedWithoutClaimingNoChanges(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-after-backup.jsonl")
	original := providerSyncTestSessionMeta("thread-after-backup", "openai") + "\n"
	providerSyncWriteTestFile(t, path, original, 0o600)

	previous := detectProviderSyncActiveProcesses
	checks := 0
	detectProviderSyncActiveProcesses = func() ([]string, error) {
		checks++
		if checks >= 2 {
			return []string{"ChatGPT"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { detectProviderSyncActiveProcesses = previous })
	result := runProviderSync(home)

	if result.Status != "failed" || result.Partial || result.RollbackStatus != "not_started" {
		t.Fatalf("post-backup writer check should be a failed, non-partial sync: %#v", result)
	}
	if strings.Contains(result.Message, "未修改") {
		t.Fatalf("post-backup failure must not claim that nothing was modified: %q", result.Message)
	}
	if result.BackupDir == nil || !isDir(*result.BackupDir) {
		t.Fatalf("post-backup failure should retain its complete backup: %#v", result)
	}
	providerSyncAssertFileContents(t, path, original)
}

func TestRunProviderSyncRollbackFailureReportsPartialState(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	path := filepath.Join(home, "sessions", "rollout-partial.jsonl")
	providerSyncWriteTestFile(t, path, providerSyncTestSessionMeta("thread-partial", "openai")+"\n"+
		`{"type":"event_msg","payload":{"type":"user_message","message":"force partial rollback"}}`+"\n", 0o600)
	globalPath := filepath.Join(home, ".codex-global-state.json")
	providerSyncWriteTestFile(t, globalPath, `{"project-order":["/old"]}`+"\n", 0o600)

	previous := applyProviderSyncGlobalStateUpdate
	applyProviderSyncGlobalStateUpdate = func(globalPath string, changes []sessionChange) (int, error) {
		count, err := applyGlobalStateUpdate(globalPath, changes)
		if err != nil {
			return count, err
		}
		if len(changes) > 0 {
			_ = os.Remove(changes[0].Path)
		}
		return count, errors.New("forced failure with missing rollback source")
	}
	t.Cleanup(func() { applyProviderSyncGlobalStateUpdate = previous })
	result := runProviderSync(home)

	if result.Status != "failed" || !result.Partial || result.RollbackStatus != "rollback_failed" {
		t.Fatalf("rollback failure should report a partial failed state: %#v", result)
	}
	if !strings.Contains(result.Message, "rollback failed") || !strings.Contains(result.Message, "partially synchronized") {
		t.Fatalf("rollback failure message is not explicit: %q", result.Message)
	}
}

func TestProviderSyncLockSerializesManualSaveAndModeSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	originalConfig := `model_provider = "openai"` + "\n"
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "config.toml"), originalConfig, 0o600)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure",
		Name:           "Pure",
		BaseURL:        "https://relay.example.test/v1",
		APIKey:         "key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: buildTestRelayConfig("https://relay.example.test/v1", "key"),
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("save mode-switch settings: %v", err)
	}

	ready := make(chan error, 1)
	releaseHolder := make(chan struct{})
	done := make(chan struct{})
	go func() {
		release, err := acquireProviderSyncLock(codexHome, "concurrency-test")
		ready <- err
		if err == nil {
			<-releaseHolder
			release()
		}
		close(done)
	}()
	if err := <-ready; err != nil {
		close(releaseHolder)
		<-done
		t.Fatalf("acquire concurrent provider lock: %v", err)
	}
	holderReleased := false
	defer func() {
		if !holderReleased {
			close(releaseHolder)
			<-done
		}
	}()

	syncResult := runProviderSync(codexHome)
	if syncResult.Status != "skipped" || syncResult.TargetProvider != "" || !strings.Contains(syncResult.Message, "lock exists") {
		t.Fatalf("manual sync should stop before reading target while lock is held: %#v", syncResult)
	}
	saveResult := (&server{}).saveRelayFile(map[string]any{"request": map[string]any{"kind": "config", "contents": `model_provider = "CodexPlusPlus"` + "\n"}})
	if stringFromAny(saveResult["status"]) != "failed" {
		t.Fatalf("manual config save should be serialized by provider lock: %#v", saveResult)
	}
	switchResult := (&server{}).applyRelayInjection(true)
	if stringFromAny(switchResult["status"]) != "failed" {
		t.Fatalf("mode switch should be serialized by provider lock: %#v", switchResult)
	}
	providerSyncAssertFileContents(t, filepath.Join(codexHome, "config.toml"), originalConfig)

	close(releaseHolder)
	<-done
	holderReleased = true
	after := runProviderSync(codexHome)
	if after.TargetProvider != "openai" {
		t.Fatalf("provider target should be read after the lock is acquired: %#v", after)
	}
}

func TestProviderSyncLauncherGuardOwnership(t *testing.T) {
	home := t.TempDir()
	providerSyncWriteTestFile(t, filepath.Join(home, "config.toml"), `model_provider = "openai"`+"\n", 0o600)
	previous := acquireProviderSyncLauncherGuard
	acquired := 0
	released := 0
	acquireProviderSyncLauncherGuard = func() (func(), error) {
		acquired++
		return func() { released++ }, nil
	}
	t.Cleanup(func() { acquireProviderSyncLauncherGuard = previous })

	result := runProviderSync(home)
	if result.Status != "synced" || acquired != 1 || released != 1 {
		t.Fatalf("manual sync should own one launcher guard: result=%#v acquired=%d released=%d", result, acquired, released)
	}
	result = runProviderSyncWithHeldLauncherGuard(home)
	if result.Status != "synced" || acquired != 1 || released != 1 {
		t.Fatalf("managed launcher sync should reuse its held guard: result=%#v acquired=%d released=%d", result, acquired, released)
	}
}

func TestSyncProvidersNowDoesNotRepairPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "config.toml"), `model_provider = "openai"`+"\n", 0o600)
	previous := runCodexPluginCommand
	pluginCalls := 0
	runCodexPluginCommand = func(home string, args ...string) codexCommandOutput {
		pluginCalls++
		return codexCommandOutput{}
	}
	t.Cleanup(func() { runCodexPluginCommand = previous })

	result := (&server{}).syncProvidersNow()

	if stringFromAny(result["status"]) != "ok" {
		t.Fatalf("history-only sync should succeed: %#v", result)
	}
	if pluginCalls != 0 {
		t.Fatalf("history-only sync unexpectedly repaired plugins %d times", pluginCalls)
	}
}

func TestSyncProvidersNowPostBackupFailureDoesNotClaimNoChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "config.toml"), `model_provider = "CodexPlusPlus"`+"\n", 0o600)
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "sessions", "rollout-command-failure.jsonl"), providerSyncTestSessionMeta("thread-command-failure", "openai")+"\n", 0o600)
	previous := detectProviderSyncActiveProcesses
	checks := 0
	detectProviderSyncActiveProcesses = func() ([]string, error) {
		checks++
		if checks >= 2 {
			return []string{"ChatGPT"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { detectProviderSyncActiveProcesses = previous })

	result := (&server{}).syncProvidersNow()

	if stringFromAny(result["status"]) != "failed" || stringFromAny(result["syncStatus"]) != "failed" {
		t.Fatalf("post-backup command failure should be failed: %#v", result)
	}
	if strings.Contains(stringFromAny(result["message"]), "未修改") {
		t.Fatalf("post-backup command failure claimed no changes: %q", result["message"])
	}
}

func TestRelayModePostBackupSyncFailureReturnsAppliedWarningWithBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	stubCodexPluginCommands(t, nil)
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "config.toml"), `model_provider = "openai"`+"\n", 0o600)
	providerSyncWriteTestFile(t, filepath.Join(codexHome, "sessions", "rollout-mode-failure.jsonl"), providerSyncTestSessionMeta("thread-mode-failure", "openai")+"\n", 0o600)
	settings := defaultSettings()
	settings.RelayProfiles = []relayProfile{{
		ID:             "pure",
		Name:           "Pure",
		BaseURL:        "https://relay.example.test/v1",
		APIKey:         "key",
		RelayMode:      "pureApi",
		Protocol:       "responses",
		ConfigContents: buildTestRelayConfig("https://relay.example.test/v1", "key"),
	}}
	settings.ActiveRelayID = "pure"
	if err := saveSettings(settings); err != nil {
		t.Fatalf("save relay failure settings: %v", err)
	}
	previous := detectProviderSyncActiveProcesses
	checks := 0
	detectProviderSyncActiveProcesses = func() ([]string, error) {
		checks++
		if checks >= 3 {
			return []string{"ChatGPT"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { detectProviderSyncActiveProcesses = previous })

	result := (&server{}).applyRelayInjection(true)

	if stringFromAny(result["status"]) != "not_checked" {
		t.Fatalf("applied mode with post-backup maintenance failure should be a warning: %#v", result)
	}
	message := stringFromAny(result["message"])
	if strings.Contains(message, "未修改") || !strings.Contains(message, "备份：") {
		t.Fatalf("mode failure message should be explicit and include its backup: %q", message)
	}
	syncPayload, _ := result["providerSync"].(map[string]any)
	if stringFromAny(syncPayload["status"]) != "failed" || stringFromAny(syncPayload["rollbackStatus"]) != "not_started" {
		t.Fatalf("mode failure history payload mismatch: %#v", syncPayload)
	}
	if got := stringFromAny(result["appliedMode"]); got != "pureApi" {
		t.Fatalf("live config was written but applied mode was not reported: %q", got)
	}
	if applied, _ := result["configApplied"].(bool); !applied {
		t.Fatalf("live config write should remain successful despite maintenance failure: %#v", result)
	}
	if got := stringFromAny(result["maintenanceStatus"]); got != "failed" {
		t.Fatalf("maintenance failure status mismatch: got %q", got)
	}
}

func providerSyncTestSessionMeta(id, provider string) string {
	payload := map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":             id,
			"cwd":            "/project",
			"model_provider": provider,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func providerSyncDecodeJSONUseNumber(t *testing.T, text string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON with UseNumber: %v", err)
	}
	return value
}

func providerSyncWriteTestFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func providerSyncAssertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s changed unexpectedly:\n got: %s\nwant: %s", path, got, want)
	}
}

func providerSyncPrepareSafetyDB(t *testing.T, path string, withFailureTrigger bool) {
	t.Helper()
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open sqlite fixture: %v", err)
	}
	defer db.Close()
	statements := []string{
		`CREATE TABLE threads (id TEXT PRIMARY KEY, model_provider TEXT, thread_source TEXT, has_user_event INTEGER, cwd TEXT)`,
		`INSERT INTO threads (id, model_provider, thread_source, has_user_event, cwd) VALUES ('thread-1', 'openai', '', 0, '/old')`,
	}
	if withFailureTrigger {
		statements = append(statements, `CREATE TRIGGER fail_user_event BEFORE UPDATE OF has_user_event ON threads BEGIN SELECT RAISE(ABORT, 'forced transaction failure'); END`)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare sqlite fixture: %v", err)
		}
	}
}

func providerSyncReadSafetyDBRow(t *testing.T, path string) (string, string, int, string) {
	t.Helper()
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open sqlite row: %v", err)
	}
	defer db.Close()
	var provider, source, cwd string
	var hasUser int
	if err := db.QueryRow(`SELECT model_provider, thread_source, has_user_event, cwd FROM threads WHERE id = 'thread-1'`).Scan(&provider, &source, &hasUser, &cwd); err != nil {
		t.Fatalf("read sqlite row: %v", err)
	}
	return provider, source, hasUser, cwd
}
