package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveThreadWorkspaceUpdatesRolloutAndSQLite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	rolloutPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	writeTestFile(t, rolloutPath, testSessionRolloutLine(sessionID, "/old/project", "Move me")+"\n{\"type\":\"user_message\"}\n")
	createTestThreadsTable(t, filepath.Join(home, ".codex", "state_5.sqlite"), sessionID, rolloutPath, "/old/project", "Move me")

	result := handleSessionDataRoute("/move-thread-workspace", map[string]any{"session_id": "local:" + sessionID, "target_cwd": "/new/project"})

	if result["status"] != "moved" {
		t.Fatalf("move should succeed: %#v", result)
	}
	data, _ := os.ReadFile(rolloutPath)
	firstLine, _ := splitFirstLine(string(data))
	var record map[string]any
	if err := json.Unmarshal([]byte(firstLine), &record); err != nil {
		t.Fatalf("rollout first line should stay json: %v", err)
	}
	payload := record["payload"].(map[string]any)
	if got := stringFromAny(payload["cwd"]); got != "/new/project" {
		t.Fatalf("rollout cwd mismatch: %q", got)
	}
	if got := testThreadCWD(t, filepath.Join(home, ".codex", "state_5.sqlite"), sessionID); got != "/new/project" {
		t.Fatalf("sqlite cwd mismatch: %q", got)
	}
}

func TestDeleteThreadAndUndoRestoresRolloutAndSQLite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := "019a61dd-9748-7743-9ce9-92b8663a935b"
	dbPath := filepath.Join(home, ".codex", "state_5.sqlite")
	rolloutPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "28", "rollout-"+sessionID+".jsonl")
	contents := testSessionRolloutLine(sessionID, "/project", "Delete me") + "\n{\"type\":\"user_message\"}\n"
	writeTestFile(t, rolloutPath, contents)
	createTestThreadsTable(t, dbPath, sessionID, rolloutPath, "/project", "Delete me")

	deleted := handleSessionDataRoute("/delete", map[string]any{"session_id": sessionID, "title": "Delete me"})

	if deleted["status"] != "local_deleted" {
		t.Fatalf("delete should succeed: %#v", deleted)
	}
	if fileExists(rolloutPath) {
		t.Fatal("rollout file should be removed after delete")
	}
	if count := testThreadCount(t, dbPath, sessionID); count != 0 {
		t.Fatalf("sqlite row should be removed, count=%d", count)
	}
	token := stringFromAny(deleted["undo_token"])
	if token == "" {
		t.Fatal("delete should return undo token")
	}

	restored := handleSessionDataRoute("/undo", map[string]any{"undo_token": token})

	if restored["status"] != "ok" {
		t.Fatalf("undo should succeed: %#v", restored)
	}
	restoredData, err := os.ReadFile(rolloutPath)
	if err != nil {
		t.Fatalf("rollout file should be restored: %v", err)
	}
	if string(restoredData) != contents {
		t.Fatalf("restored rollout mismatch:\n%s", string(restoredData))
	}
	if count := testThreadCount(t, dbPath, sessionID); count != 1 {
		t.Fatalf("sqlite row should be restored, count=%d", count)
	}
}

func testSessionRolloutLine(sessionID, cwd, title string) string {
	data, _ := json.Marshal(map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":             sessionID,
			"cwd":            cwd,
			"title":          title,
			"model_provider": "CodexPlusPlus",
			"timestamp":      "2026-05-28T10:00:00Z",
		},
		"timestamp": "2026-05-28T10:00:00Z",
	})
	return string(data)
}

func createTestThreadsTable(t *testing.T, dbPath, sessionID, rolloutPath, cwd, title string) {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		model_provider TEXT NOT NULL,
		cwd TEXT NOT NULL,
		title TEXT NOT NULL,
		archived INTEGER NOT NULL DEFAULT 0,
		created_at_ms INTEGER,
		updated_at_ms INTEGER
	)`); err != nil {
		t.Fatalf("failed to create threads table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads (id, rollout_path, created_at, updated_at, model_provider, cwd, title, archived, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`, sessionID, rolloutPath, 1779962400, 1779962500, "CodexPlusPlus", cwd, title, 1779962400000, 1779962500000); err != nil {
		t.Fatalf("failed to insert thread row: %v", err)
	}
}

func testThreadCWD(t *testing.T, dbPath, sessionID string) string {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	var cwd string
	if err := db.QueryRow(`SELECT cwd FROM threads WHERE id = ?`, sessionID).Scan(&cwd); err != nil {
		t.Fatalf("failed to read cwd: %v", err)
	}
	return cwd
}

func testThreadCount(t *testing.T, dbPath, sessionID string) int {
	t.Helper()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM threads WHERE id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatalf("failed to count thread rows: %v", err)
	}
	return count
}
