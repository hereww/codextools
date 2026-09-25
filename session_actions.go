package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionDeleteBackupVersion = 2

const maxRolloutMetadataLineBytes = 4 * 1024 * 1024

var sessionDeleteState = struct {
	sync.Mutex
	inFlight map[string]struct{}
}{inFlight: map[string]struct{}{}}

type sessionRolloutFile struct {
	Path        string
	FirstLine   string
	Separator   string
	Record      map[string]any
	SessionID   string
	Title       string
	CWD         string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type sessionSQLiteRow struct {
	Columns []string       `json:"columns"`
	Values  map[string]any `json:"values"`
	DBPath  string         `json:"db_path,omitempty"`
	Table   string         `json:"table,omitempty"`
}

type sessionLookupResult struct {
	RequestedID string
	CanonicalID string
	Variants    []string
	Files       []sessionRolloutFile
	DBRows      []sessionSQLiteRow
}

type deletedSessionManifest struct {
	Version   int                        `json:"version"`
	SessionID string                     `json:"session_id"`
	DeletedAt string                     `json:"deleted_at"`
	Files     []deletedSessionFileBackup `json:"files"`
	Rows      []sessionSQLiteRow         `json:"rows"`
}

type deletedSessionFileBackup struct {
	OriginalPath string `json:"original_path"`
	BackupName   string `json:"backup_name"`
}

type sessionSortKey struct {
	SessionID   string
	UpdatedAt   int64
	UpdatedAtMs int64
	CreatedAtMs int64
}

type sessionMarkdownExport struct {
	Filename string
	Markdown string
}

type sessionMarkdownMessage struct {
	Role      string
	Text      string
	Timestamp string
}

func isSessionDataRoute(path string) bool {
	switch path {
	case "/delete", "/undo", "/archived-thread", "/move-thread-workspace", "/move-thread-projectless", "/export-markdown", "/thread-usage-history", "/thread-sort-key", "/thread-sort-keys":
		return true
	default:
		return false
	}
}

func handleSessionDataRoute(path string, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	switch path {
	case "/delete":
		return deleteSessionDataRoute(payload)
	case "/undo":
		return undoSessionDataRoute(payload)
	case "/archived-thread":
		return archivedThreadDataRoute(payload)
	case "/move-thread-workspace":
		return moveThreadWorkspaceDataRoute(payload)
	case "/move-thread-projectless":
		return moveThreadProjectlessDataRoute(payload)
	case "/export-markdown":
		return exportMarkdownDataRoute(payload)
	case "/thread-usage-history":
		return threadUsageHistoryDataRoute(payload)
	case "/thread-sort-key":
		return threadSortKeyDataRoute(payload)
	case "/thread-sort-keys":
		return threadSortKeysDataRoute(payload)
	default:
		return unsupportedBridgeDataRoute(path, payload)
	}
}

func threadUsageHistoryDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "读取用量历史失败：未找到会话 ID", "history": []any{}}
	}
	lookup, err := lookupSession(codexHomeDir(), sessionID, "", false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "读取用量历史失败：" + err.Error(), "history": []any{}}
	}
	rolloutPath := ""
	if len(lookup.Files) > 0 {
		rolloutPath = lookup.Files[0].Path
	}
	if rolloutPath == "" {
		for _, row := range lookup.DBRows {
			if candidate := strings.TrimSpace(stringFromAny(row.Values["rollout_path"])); candidate != "" {
				rolloutPath = candidate
				break
			}
		}
	}
	if rolloutPath == "" || !fileExists(rolloutPath) {
		return map[string]any{"status": "failed", "session_id": lookup.canonicalOr(sessionID), "message": "读取用量历史失败：会话 rollout 文件不存在", "history": []any{}}
	}
	history, err := readRolloutUsageHistory(rolloutPath, lookup.canonicalOr(sessionID))
	if err != nil {
		return map[string]any{"status": "failed", "session_id": lookup.canonicalOr(sessionID), "message": "读取用量历史失败：" + err.Error(), "history": []any{}}
	}
	return map[string]any{
		"status":       "ok",
		"session_id":   lookup.canonicalOr(sessionID),
		"rollout_path": rolloutPath,
		"history":      history,
	}
}

func readRolloutUsageHistory(path, threadID string) ([]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	history := []any{}
	currentTurnID := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		payload, _ := record["payload"].(map[string]any)
		switch strings.TrimSpace(stringFromAny(record["type"])) {
		case "turn_context":
			currentTurnID = strings.TrimSpace(stringFromAny(payload["turn_id"]))
		case "event_msg":
			if strings.TrimSpace(stringFromAny(payload["type"])) != "token_count" {
				continue
			}
			info, _ := payload["info"].(map[string]any)
			last, _ := info["last_token_usage"].(map[string]any)
			total, _ := info["total_token_usage"].(map[string]any)
			inputTokens := int64FromFlexible(last["input_tokens"])
			outputTokens := int64FromFlexible(last["output_tokens"])
			totalTokens := int64FromFlexible(last["total_tokens"])
			if totalTokens == 0 {
				totalTokens = int64FromFlexible(total["total_tokens"])
			}
			cachedTokens := int64FromFlexible(last["cached_input_tokens"])
			contextUsed := int64FromFlexible(total["total_tokens"])
			if contextUsed == 0 {
				contextUsed = totalTokens
			}
			if inputTokens <= 0 && outputTokens <= 0 && totalTokens <= 0 && contextUsed <= 0 {
				continue
			}
			history = append(history, map[string]any{
				"source":          "rollout-history",
				"conversation_id": "local:" + strings.TrimPrefix(threadID, "local:"),
				"turn_id":         currentTurnID,
				"observed_at":     stringFromAny(record["timestamp"]),
				"usage": map[string]any{
					"inputTokens":         inputTokens,
					"outputTokens":        outputTokens,
					"totalTokens":         totalTokens,
					"cachedTokens":        cachedTokens,
					"cacheReadTokens":     int64(0),
					"cacheCreationTokens": int64(0),
					"contextUsed":         contextUsed,
					"contextLimit":        int64FromFlexible(info["model_context_window"]),
					"hasBreakdown":        inputTokens > 0 || outputTokens > 0 || cachedTokens > 0,
				},
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return history, nil
}

func deleteSessionDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "删除失败：未找到会话 ID"}
	}
	deleteKey := bareSessionID(sessionID)
	if !beginSessionDelete(deleteKey) {
		appendDiagnosticLog("session_delete.in_progress", map[string]any{"session_id_present": true})
		return map[string]any{
			"status":  "delete_in_progress",
			"code":    "delete_in_progress",
			"message": "该会话正在删除，请稍候，不要重复点击。",
		}
	}
	defer endSessionDelete(deleteKey)
	home := codexHomeDir()
	lookup, err := lookupSession(home, sessionID, "", false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：" + err.Error()}
	}
	undoToken, err := createSessionDeleteBackup(lookup)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：创建备份失败：" + err.Error()}
	}
	if err := deleteSessionLookupRows(lookup); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：更新会话索引失败：" + err.Error()}
	}
	for _, file := range lookup.Files {
		if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = restoreSessionSQLiteRows(lookup.DBRows)
			return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：移除会话文件失败：" + err.Error(), "undo_token": undoToken}
		}
	}
	return map[string]any{
		"status":     "local_deleted",
		"session_id": lookup.canonicalOr(sessionID),
		"message":    "已删除本地会话。",
		"undo_token": undoToken,
	}
}

func beginSessionDelete(sessionID string) bool {
	sessionID = strings.TrimSpace(bareSessionID(sessionID))
	if sessionID == "" {
		return true
	}
	sessionDeleteState.Lock()
	defer sessionDeleteState.Unlock()
	if _, exists := sessionDeleteState.inFlight[sessionID]; exists {
		return false
	}
	sessionDeleteState.inFlight[sessionID] = struct{}{}
	return true
}

func endSessionDelete(sessionID string) {
	sessionID = strings.TrimSpace(bareSessionID(sessionID))
	if sessionID == "" {
		return
	}
	sessionDeleteState.Lock()
	delete(sessionDeleteState.inFlight, sessionID)
	sessionDeleteState.Unlock()
}

func undoSessionDataRoute(payload map[string]any) map[string]any {
	token := strings.TrimSpace(stringFromAny(payload["undo_token"]))
	if token == "" {
		return map[string]any{"status": "failed", "message": "撤销失败：缺少撤销令牌"}
	}
	backupDir, err := sessionDeleteBackupDir(token)
	if err != nil {
		return map[string]any{"status": "failed", "message": "撤销失败：" + err.Error()}
	}
	var manifest deletedSessionManifest
	if err := readJSON(filepath.Join(backupDir, "manifest.json"), &manifest); err != nil {
		return map[string]any{"status": "failed", "message": "撤销失败：备份不存在或已损坏"}
	}
	if err := validateDeletedSessionRestorePaths(manifest); err != nil {
		return map[string]any{"status": "failed", "session_id": manifest.SessionID, "message": "撤销失败：" + err.Error()}
	}
	for _, file := range manifest.Files {
		source := filepath.Join(backupDir, file.BackupName)
		if err := copyFileIfExists(source, file.OriginalPath); err != nil {
			return map[string]any{"status": "failed", "session_id": manifest.SessionID, "message": "撤销失败：恢复会话文件失败：" + err.Error()}
		}
	}
	if err := restoreSessionSQLiteRows(manifest.Rows); err != nil {
		return map[string]any{"status": "failed", "session_id": manifest.SessionID, "message": "撤销失败：恢复会话索引失败：" + err.Error()}
	}
	return map[string]any{"status": "ok", "session_id": manifest.SessionID, "message": "已恢复会话。"}
}

func archivedThreadDataRoute(payload map[string]any) map[string]any {
	title := strings.TrimSpace(stringFromAny(payload["title"]))
	if title == "" {
		return map[string]any{"status": "failed", "message": "未找到归档会话标题"}
	}
	lookup, err := lookupSession(codexHomeDir(), "", title, true)
	if err != nil {
		return map[string]any{"status": "failed", "message": "未找到归档会话：" + err.Error()}
	}
	sessionID := lookup.canonicalOr("")
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "未找到归档会话 ID"}
	}
	return map[string]any{"status": "ok", "session_id": sessionID, "title": title}
}

func moveThreadWorkspaceDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	targetCWD := toDesktopWorkspacePath(stringFromAny(payload["target_cwd"]))
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "移动失败：未找到会话 ID"}
	}
	if strings.TrimSpace(targetCWD) == "" {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：目标项目路径为空"}
	}
	home := codexHomeDir()
	lookup, err := lookupSession(home, sessionID, "", false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：" + err.Error()}
	}
	for _, file := range lookup.Files {
		if err := rewriteRolloutWorkspace(file, targetCWD); err != nil {
			return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：更新会话文件失败：" + err.Error()}
		}
	}
	if err := updateSessionLookupWorkspace(lookup, targetCWD); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：更新会话索引失败：" + err.Error()}
	}
	if err := updateCodexGlobalStateForWorkspaceMove(home, lookup, targetCWD); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：更新 Codex 全局状态失败：" + err.Error()}
	}
	key := sortKeyForSession(home, sessionID)
	result := sortKeyPayload(key)
	result["status"] = "moved"
	result["session_id"] = lookup.canonicalOr(sessionID)
	result["target_cwd"] = targetCWD
	result["message"] = "已移动到项目。"
	return result
}

func moveThreadProjectlessDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "移动失败：未找到会话 ID"}
	}
	home := codexHomeDir()
	lookup, err := lookupSession(home, sessionID, "", false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：" + err.Error()}
	}
	if err := updateCodexGlobalStateForProjectlessMove(home, lookup); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：更新 Codex 全局状态失败：" + err.Error()}
	}
	key := sortKeyForSession(home, sessionID)
	result := sortKeyPayload(key)
	result["status"] = "moved"
	result["session_id"] = lookup.canonicalOr(sessionID)
	result["target_cwd"] = ""
	result["message"] = "已移动到普通对话。"
	return result
}

func exportMarkdownDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	title := strings.TrimSpace(stringFromAny(payload["title"]))
	if sessionID == "" && title == "" {
		return map[string]any{"status": "failed", "message": "导出失败：未找到会话"}
	}
	home := codexHomeDir()
	lookup, err := lookupSession(home, sessionID, title, false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "导出失败：" + err.Error()}
	}
	export, err := buildSessionMarkdownExport(lookup)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": lookup.canonicalOr(sessionID), "message": "导出失败：" + err.Error()}
	}
	return map[string]any{
		"status":     "exported",
		"session_id": lookup.canonicalOr(sessionID),
		"filename":   export.Filename,
		"markdown":   export.Markdown,
		"message":    "已导出 Markdown。",
	}
}

func threadSortKeyDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	key := sortKeyForSession(codexHomeDir(), sessionID)
	result := sortKeyPayload(key)
	result["status"] = "ok"
	result["session_id"] = sessionID
	return result
}

func threadSortKeysDataRoute(payload map[string]any) map[string]any {
	rawSessions, _ := payload["sessions"].([]any)
	sortKeys := make([]any, 0, len(rawSessions))
	for _, item := range rawSessions {
		sessionMap, _ := item.(map[string]any)
		sessionID := strings.TrimSpace(stringFromAny(sessionMap["session_id"]))
		key := sortKeyForSession(codexHomeDir(), sessionID)
		value := sortKeyPayload(key)
		value["session_id"] = sessionID
		sortKeys = append(sortKeys, value)
	}
	return map[string]any{"status": "ok", "sort_keys": sortKeys, "sessions": sortKeys}
}

func lookupSession(home, sessionID, title string, archivedOnly bool) (sessionLookupResult, error) {
	var lookup sessionLookupResult
	lookup.RequestedID = strings.TrimSpace(sessionID)
	lookup.Variants = sessionIDVariants(sessionID)
	var rows []sessionSQLiteRow
	var err error
	for _, dbPath := range codexSessionDBPaths(home) {
		var candidate []sessionSQLiteRow
		if len(lookup.Variants) > 0 {
			candidate, err = sqliteThreadRowsByIDs(dbPath, lookup.Variants)
			if err == nil && len(candidate) == 0 {
				candidate, err = sqliteAutomationRunRowsByIDs(dbPath, lookup.Variants)
			}
		} else if strings.TrimSpace(title) != "" {
			candidate, err = sqliteThreadRowsByTitle(dbPath, title, archivedOnly)
			if err == nil && len(candidate) == 0 {
				candidate, err = sqliteAutomationRunRowsByTitle(dbPath, title, archivedOnly)
			}
		}
		if err != nil {
			return lookup, err
		}
		rows = append(rows, candidate...)
	}
	lookup.DBRows = rows
	for _, row := range rows {
		id := strings.TrimSpace(stringFromAny(row.Values["id"]))
		if id != "" {
			lookup.Variants = appendSessionIDVariants(lookup.Variants, id)
			if lookup.CanonicalID == "" {
				lookup.CanonicalID = bareSessionID(id)
			}
		}
		threadID := strings.TrimSpace(stringFromAny(row.Values["thread_id"]))
		if threadID != "" {
			lookup.Variants = appendSessionIDVariants(lookup.Variants, threadID)
			if lookup.CanonicalID == "" {
				lookup.CanonicalID = bareSessionID(threadID)
			}
		}
	}
	var files []sessionRolloutFile
	seenFiles := map[string]bool{}
	for _, row := range rows {
		path := normalizeRolloutPath(home, stringFromAny(row.Values["rollout_path"]))
		if path == "" || seenFiles[path] || !fileExists(path) {
			continue
		}
		file, err := readRolloutFile(path)
		if err != nil {
			continue
		}
		files = append(files, file)
		seenFiles[path] = true
	}
	walked, err := findRolloutFiles(home, lookup.Variants)
	if err != nil {
		return lookup, err
	}
	for _, file := range walked {
		if seenFiles[file.Path] {
			continue
		}
		files = append(files, file)
		seenFiles[file.Path] = true
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	lookup.Files = files
	for _, file := range files {
		if file.SessionID != "" {
			lookup.Variants = appendSessionIDVariants(lookup.Variants, file.SessionID)
			if lookup.CanonicalID == "" {
				lookup.CanonicalID = bareSessionID(file.SessionID)
			}
		}
	}
	if lookup.CanonicalID == "" && len(lookup.Variants) > 0 {
		lookup.CanonicalID = bareSessionID(lookup.Variants[0])
	}
	if len(lookup.DBRows) == 0 && len(lookup.Files) == 0 {
		return lookup, errors.New("未在本地索引或会话文件中找到该会话")
	}
	return lookup, nil
}

func (l sessionLookupResult) canonicalOr(fallback string) string {
	if strings.TrimSpace(l.CanonicalID) != "" {
		return l.CanonicalID
	}
	if len(l.Variants) > 0 {
		return bareSessionID(l.Variants[0])
	}
	return bareSessionID(fallback)
}

func (l sessionLookupResult) allIDs() []string {
	ids := append([]string{}, l.Variants...)
	for _, row := range l.DBRows {
		ids = appendSessionIDVariants(ids, stringFromAny(row.Values["id"]))
	}
	for _, file := range l.Files {
		ids = appendSessionIDVariants(ids, file.SessionID)
	}
	return uniqueNonEmptyStrings(ids)
}

func annotateSessionRows(rows []sessionSQLiteRow, dbPath, table string) []sessionSQLiteRow {
	for index := range rows {
		rows[index].DBPath = filepath.Clean(dbPath)
		rows[index].Table = table
	}
	return rows
}

func rowsByDatabase(rows []sessionSQLiteRow) map[string][]sessionSQLiteRow {
	grouped := map[string][]sessionSQLiteRow{}
	for _, row := range rows {
		path := strings.TrimSpace(row.DBPath)
		if path == "" {
			path = codexPreferredSessionDBPath(codexHomeDir())
		}
		grouped[filepath.Clean(path)] = append(grouped[filepath.Clean(path)], row)
	}
	return grouped
}

func deleteSessionLookupRows(lookup sessionLookupResult) error {
	ids := lookup.allIDs()
	tablesByPath := map[string]map[string]bool{}
	for _, row := range lookup.DBRows {
		if (row.Table == "threads" || row.Table == "automation_runs") && strings.TrimSpace(row.DBPath) != "" {
			path := filepath.Clean(row.DBPath)
			if tablesByPath[path] == nil {
				tablesByPath[path] = map[string]bool{}
			}
			tablesByPath[path][row.Table] = true
		}
	}
	if len(tablesByPath) == 0 {
		for _, path := range codexSessionDBPaths(codexHomeDir()) {
			tablesByPath[path] = map[string]bool{"threads": true}
		}
	}
	for path, tables := range tablesByPath {
		for table := range tables {
			if err := deleteSQLiteSessionRows(path, table, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

func updateSessionLookupWorkspace(lookup sessionLookupResult, targetCWD string) error {
	ids := lookup.allIDs()
	paths := map[string]bool{}
	for _, row := range lookup.DBRows {
		if row.Table == "threads" && strings.TrimSpace(row.DBPath) != "" {
			paths[filepath.Clean(row.DBPath)] = true
		}
	}
	if len(paths) == 0 {
		for _, path := range codexSessionDBPaths(codexHomeDir()) {
			paths[path] = true
		}
	}
	for path := range paths {
		if err := updateSQLiteThreadWorkspace(path, ids, targetCWD); err != nil {
			return err
		}
	}
	return nil
}

func restoreSessionSQLiteRows(rows []sessionSQLiteRow) error {
	for dbPath, group := range rowsByDatabase(rows) {
		byTable := map[string][]sessionSQLiteRow{}
		for _, row := range group {
			table := row.Table
			if table == "" {
				table = "threads"
			}
			byTable[table] = append(byTable[table], row)
		}
		for table, tableRows := range byTable {
			if err := restoreSQLiteSessionRows(dbPath, table, tableRows); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDeletedSessionRestorePaths(manifest deletedSessionManifest) error {
	allowed := map[string]bool{}
	for _, row := range manifest.Rows {
		if path := strings.TrimSpace(stringFromAny(row.Values["rollout_path"])); path != "" {
			allowed[filepath.Clean(normalizeRolloutPath(codexHomeDir(), path))] = true
		}
	}
	for _, file := range manifest.Files {
		original := filepath.Clean(strings.TrimSpace(file.OriginalPath))
		if original == "." || original == "" {
			return errors.New("备份包含空的会话恢复路径")
		}
		if allowed[original] {
			continue
		}
		if manifest.Version >= 2 && manifestHasAutomationRows(manifest) && sessionRestorePathMatchesManifest(original, manifest) {
			continue
		}
		// Legacy v1 backups did not store DB provenance; constrain them to Codex-owned rollout roots.
		if manifest.Version <= 1 && (pathWithin(filepath.Join(codexHomeDir(), "sessions"), original) || pathWithin(filepath.Join(codexHomeDir(), "archived_sessions"), original)) {
			continue
		}
		return fmt.Errorf("备份包含未授权的恢复路径：%s", original)
	}
	return nil
}

func manifestHasAutomationRows(manifest deletedSessionManifest) bool {
	for _, row := range manifest.Rows {
		if row.Table == "automation_runs" {
			return true
		}
	}
	return false
}

func sessionRestorePathMatchesManifest(path string, manifest deletedSessionManifest) bool {
	if !pathWithin(filepath.Join(codexHomeDir(), "sessions"), path) && !pathWithin(filepath.Join(codexHomeDir(), "archived_sessions"), path) {
		return false
	}
	id := bareSessionID(manifest.SessionID)
	return id != "" && strings.Contains(filepath.Base(path), id)
}

func sessionIDVariants(sessionID string) []string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	bare := bareSessionID(sessionID)
	return uniqueNonEmptyStrings([]string{sessionID, bare, "local:" + bare})
}

func appendSessionIDVariants(ids []string, sessionID string) []string {
	return append(ids, sessionIDVariants(sessionID)...)
}

func bareSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	return strings.TrimPrefix(sessionID, "local:")
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
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

func normalizeRolloutPath(home, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = toDesktopWorkspacePath(value)
	if strings.HasPrefix(value, "~/") {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			return filepath.Join(userHome, value[2:])
		}
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(home, value)
}

func readRolloutFile(path string) (sessionRolloutFile, error) {
	fileHandle, err := os.Open(path)
	if err != nil {
		return sessionRolloutFile{}, err
	}
	defer fileHandle.Close()
	reader := bufio.NewReaderSize(io.LimitReader(fileHandle, maxRolloutMetadataLineBytes+1), 64*1024)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return sessionRolloutFile{}, err
	}
	if len(line) > maxRolloutMetadataLineBytes {
		return sessionRolloutFile{}, fmt.Errorf("rollout metadata line exceeds %d bytes", maxRolloutMetadataLineBytes)
	}
	firstLine, separator := splitFirstLine(line)
	var record map[string]any
	if err := json.Unmarshal([]byte(firstLine), &record); err != nil {
		return sessionRolloutFile{}, err
	}
	payload, _ := record["payload"].(map[string]any)
	file := sessionRolloutFile{
		Path:        path,
		FirstLine:   firstLine,
		Separator:   separator,
		Record:      record,
		SessionID:   strings.TrimSpace(stringFromAny(payload["id"])),
		Title:       firstString(payload["title"], record["title"]),
		CWD:         toDesktopWorkspacePath(stringFromAny(payload["cwd"])),
		CreatedAtMs: timestampMsFromAny(firstString(payload["timestamp"], record["timestamp"])),
		UpdatedAtMs: timestampMsFromAny(firstString(record["timestamp"], payload["timestamp"])),
	}
	if file.CreatedAtMs == 0 {
		file.CreatedAtMs = uuidV7TimestampMs(file.SessionID)
	}
	if file.UpdatedAtMs == 0 {
		if info, statErr := os.Stat(path); statErr == nil {
			file.UpdatedAtMs = info.ModTime().UnixMilli()
		}
	}
	return file, nil
}

func rolloutFileMatchesAnyID(path string, ids map[string]bool) bool {
	fileHandle, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fileHandle.Close()
	reader := bufio.NewReaderSize(fileHandle, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) == nil {
				payload, _ := record["payload"].(map[string]any)
				if payload == nil {
					payload = record
				}
				candidates := []string{
					stringFromAny(payload["id"]),
					stringFromAny(payload["session_id"]),
					stringFromAny(payload["thread_id"]),
					stringFromAny(record["id"]),
					stringFromAny(record["session_id"]),
					stringFromAny(record["thread_id"]),
				}
				if stringFromAny(record["type"]) != "session_meta" && stringFromAny(payload["type"]) != "session_meta" {
					candidates = candidates[:1]
				}
				for _, candidate := range candidates {
					for _, variant := range sessionIDVariants(candidate) {
						if ids[variant] || ids[bareSessionID(variant)] {
							return true
						}
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false
		}
	}
	return false
}

func findRolloutFiles(home string, ids []string) ([]sessionRolloutFile, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var files []sessionRolloutFile
	idSet := map[string]bool{}
	for _, id := range ids {
		for _, variant := range sessionIDVariants(id) {
			idSet[variant] = true
			idSet[bareSessionID(variant)] = true
		}
	}
	for _, dirname := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(home, dirname)
		if !isDir(root) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			// Codex rollout names normally contain the UUID. Avoid reading every
			// historical transcript just to find one session.
			nameMatches := rolloutFileNameMatchesAnyID(name, idSet)
			if !nameMatches && len(idSet) == 0 {
				return nil
			}
			file, err := readRolloutFile(path)
			if err != nil {
				return nil
			}
			if nameMatches || idSet[file.SessionID] || idSet[bareSessionID(file.SessionID)] {
				files = append(files, file)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func rolloutFileNameMatchesAnyID(name string, ids map[string]bool) bool {
	for id := range ids {
		id = bareSessionID(id)
		if len(id) < 8 {
			continue
		}
		if strings.Contains(name, id) {
			return true
		}
	}
	return false
}

func rewriteRolloutWorkspace(file sessionRolloutFile, targetCWD string) error {
	record := file.Record
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		return errors.New("会话文件缺少 payload")
	}
	if toDesktopWorkspacePath(stringFromAny(payload["cwd"])) == targetCWD {
		return nil
	}
	payload["cwd"] = targetCWD
	nextFirstLine, err := json.Marshal(record)
	if err != nil {
		return err
	}
	source, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	reader := bufio.NewReaderSize(source, 64*1024)
	if _, err := reader.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
		_ = source.Close()
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(file.Path), ".codextools-rollout-*")
	if err != nil {
		_ = source.Close()
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if info, statErr := source.Stat(); statErr == nil {
		_ = temp.Chmod(info.Mode().Perm())
	}
	if _, err := temp.Write(nextFirstLine); err != nil {
		_ = temp.Close()
		_ = source.Close()
		return err
	}
	if file.Separator != "" {
		if _, err := temp.WriteString("\n"); err != nil {
			_ = temp.Close()
			_ = source.Close()
			return err
		}
	}
	if _, err := io.Copy(temp, reader); err != nil {
		_ = temp.Close()
		_ = source.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = source.Close()
		return err
	}
	if err := source.Close(); err != nil {
		return err
	}
	return replaceFile(tempPath, file.Path)
}

func buildSessionMarkdownExport(lookup sessionLookupResult) (sessionMarkdownExport, error) {
	if len(lookup.Files) == 0 {
		return sessionMarkdownExport{}, errors.New("未找到可导出的会话文件")
	}
	file := lookup.Files[len(lookup.Files)-1]
	messages, err := rolloutMarkdownMessages(file.Path)
	if err != nil {
		return sessionMarkdownExport{}, err
	}
	if len(messages) == 0 {
		return sessionMarkdownExport{}, errors.New("会话中没有可导出的用户或助手消息")
	}
	title := firstString(file.Title, lookup.CanonicalID, lookup.RequestedID, "Codex Conversation")
	sessionID := firstString(file.SessionID, lookup.canonicalOr(""))
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(markdownLine(title))
	builder.WriteString("\n\n")
	if sessionID != "" {
		builder.WriteString("- Session ID: `")
		builder.WriteString(markdownInlineCode(sessionID))
		builder.WriteString("`\n")
	}
	if file.CWD != "" {
		builder.WriteString("- Workspace: `")
		builder.WriteString(markdownInlineCode(file.CWD))
		builder.WriteString("`\n")
	}
	builder.WriteString("- Exported: ")
	builder.WriteString(time.Now().UTC().Format(time.RFC3339))
	builder.WriteString("\n\n")
	for _, message := range messages {
		builder.WriteString("## ")
		builder.WriteString(markdownRoleLabel(message.Role))
		if message.Timestamp != "" {
			builder.WriteString(" · ")
			builder.WriteString(markdownLine(message.Timestamp))
		}
		builder.WriteString("\n\n")
		builder.WriteString(strings.TrimSpace(message.Text))
		builder.WriteString("\n\n")
	}
	return sessionMarkdownExport{
		Filename: exportMarkdownFilename(title, time.Now()),
		Markdown: strings.TrimRight(builder.String(), "\n") + "\n",
	}, nil
}

func rolloutMarkdownMessages(path string) ([]sessionMarkdownMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var messages []sessionMarkdownMessage
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		message := markdownMessageFromRolloutRecord(record)
		if message.Role == "" || strings.TrimSpace(message.Text) == "" {
			continue
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func markdownMessageFromRolloutRecord(record map[string]any) sessionMarkdownMessage {
	payload, _ := record["payload"].(map[string]any)
	if payload == nil {
		payload = record
	}
	timestamp := firstString(record["timestamp"], payload["timestamp"])
	switch stringFromAny(record["type"]) {
	case "event_msg":
		switch stringFromAny(payload["type"]) {
		case "user_message", "user_input":
			return sessionMarkdownMessage{Role: "user", Text: stringFromAny(payload["message"]), Timestamp: timestamp}
		case "agent_message":
			if phase := strings.TrimSpace(stringFromAny(payload["phase"])); phase != "" && phase != "final_answer" && phase != "commentary" {
				return sessionMarkdownMessage{}
			}
			return sessionMarkdownMessage{Role: "assistant", Text: stringFromAny(payload["message"]), Timestamp: timestamp}
		default:
			return sessionMarkdownMessage{}
		}
	case "response_item":
		if stringFromAny(payload["type"]) != "message" {
			return sessionMarkdownMessage{}
		}
		role := strings.TrimSpace(stringFromAny(payload["role"]))
		if role != "user" && role != "assistant" {
			return sessionMarkdownMessage{}
		}
		text := markdownTextFromContent(payload["content"])
		return sessionMarkdownMessage{Role: role, Text: text, Timestamp: timestamp}
	default:
		return sessionMarkdownMessage{}
	}
}

func markdownTextFromContent(value any) string {
	items, ok := value.([]any)
	if !ok {
		return stringFromAny(value)
	}
	var parts []string
	for _, item := range items {
		content, _ := item.(map[string]any)
		if content == nil {
			continue
		}
		switch stringFromAny(content["type"]) {
		case "input_text", "output_text", "text":
			if text := strings.TrimSpace(stringFromAny(content["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func markdownRoleLabel(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	default:
		return markdownLine(role)
	}
}

func markdownLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func markdownInlineCode(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func exportMarkdownFilename(title string, exportedAt time.Time) string {
	name := sanitizeExportFilename(title)
	if name == "" {
		name = "codex-conversation"
	}
	return fmt.Sprintf("%s-%s.md", name, exportedAt.Format("20060102-150405"))
}

func sanitizeExportFilename(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if r < 32 || r == ' ' || r == '\t' || strings.ContainsRune(`<>:"/\|?*`, r) {
			if !lastDash {
				builder.WriteRune('-')
				lastDash = true
			}
		} else {
			builder.WriteRune(r)
			lastDash = false
		}
		if builder.Len() >= 80 {
			break
		}
	}
	return strings.Trim(builder.String(), "-._ ")
}

func codexGlobalStatePath(home string) string {
	return filepath.Join(home, ".codex-global-state.json")
}

func readCodexGlobalState(home string) (map[string]any, error) {
	path := codexGlobalStatePath(home)
	state := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return nil, fmt.Errorf("读取 global state 失败：%w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 global state 失败：%w", err)
	}
	if state == nil {
		state = map[string]any{}
	}
	return state, nil
}

func writeCodexGlobalState(home string, state map[string]any) error {
	return atomicWriteJSON(codexGlobalStatePath(home), state)
}

func updateCodexGlobalStateForProjectlessMove(home string, lookup sessionLookupResult) error {
	return updateCodexGlobalStateForSession(home, lookup, "", true)
}

func updateCodexGlobalStateForWorkspaceMove(home string, lookup sessionLookupResult, targetCWD string) error {
	return updateCodexGlobalStateForSession(home, lookup, targetCWD, false)
}

func updateCodexGlobalStateForSession(home string, lookup sessionLookupResult, targetCWD string, projectless bool) error {
	state, err := readCodexGlobalState(home)
	if err != nil {
		return err
	}
	ids := lookup.allIDs()
	canonical := lookup.canonicalOr("")
	if canonical != "" {
		ids = appendSessionIDVariants(ids, canonical)
	}
	ids = uniqueNonEmptyStrings(ids)
	bareIDs := uniqueBareSessionIDs(ids)
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
		idSet[bareSessionID(id)] = true
	}

	if projectless {
		existing := stringsFromAnySlice(state["projectless-thread-ids"])
		state["projectless-thread-ids"] = uniqueNonEmptyStrings(append(existing, bareIDs...))
	} else {
		var next []string
		for _, id := range stringsFromAnySlice(state["projectless-thread-ids"]) {
			if !idSet[id] {
				next = append(next, id)
			}
		}
		if len(next) > 0 {
			state["projectless-thread-ids"] = next
		} else {
			delete(state, "projectless-thread-ids")
		}
	}

	removeGlobalStateMapEntries(state, "thread-workspace-root-hints", idSet)
	removeGlobalStateMapEntries(state, "thread-project-assignments", idSet)
	if !projectless && strings.TrimSpace(targetCWD) != "" {
		ensureGlobalStateWorkspaceRoot(state, "electron-saved-workspace-roots", targetCWD)
		ensureGlobalStateWorkspaceRoot(state, "project-order", targetCWD)
		hints := mapFromAny(state["thread-workspace-root-hints"])
		for _, id := range bareIDs {
			hints[id] = targetCWD
		}
		state["thread-workspace-root-hints"] = hints
	}
	return writeCodexGlobalState(home, state)
}

func ensureGlobalStateWorkspaceRoot(state map[string]any, key, targetCWD string) {
	targetCWD = toDesktopWorkspacePath(targetCWD)
	if strings.TrimSpace(targetCWD) == "" {
		return
	}
	roots := pathArray(state[key])
	state[key] = dedupePaths(append(roots, targetCWD))
}

func removeGlobalStateMapEntries(state map[string]any, key string, ids map[string]bool) {
	values := mapFromAny(state[key])
	if len(values) == 0 {
		return
	}
	for id := range ids {
		delete(values, id)
	}
	if len(values) == 0 {
		delete(state, key)
		return
	}
	state[key] = values
}

func mapFromAny(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok || typed == nil {
		return map[string]any{}
	}
	next := map[string]any{}
	for key, item := range typed {
		next[key] = item
	}
	return next
}

func stringsFromAnySlice(value any) []string {
	switch items := value.(type) {
	case []any:
		values := make([]string, 0, len(items))
		for _, item := range items {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				values = append(values, text)
			}
		}
		return values
	case []string:
		values := make([]string, 0, len(items))
		for _, item := range items {
			if text := strings.TrimSpace(item); text != "" {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func uniqueBareSessionIDs(ids []string) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, bareSessionID(id))
	}
	return uniqueNonEmptyStrings(values)
}

func createSessionDeleteBackup(lookup sessionLookupResult) (string, error) {
	root := filepath.Join(stateDir(), "deleted-sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tokenBase := time.Now().Format("20060102150405")
	if id := sanitizeBackupTokenPart(lookup.canonicalOr("session")); id != "" {
		tokenBase += "-" + id
	}
	token := tokenBase
	backupDir := filepath.Join(root, token)
	for suffix := 2; fileExists(backupDir); suffix++ {
		token = fmt.Sprintf("%s-%d", tokenBase, suffix)
		backupDir = filepath.Join(root, token)
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	manifest := deletedSessionManifest{
		Version:   sessionDeleteBackupVersion,
		SessionID: lookup.canonicalOr(""),
		DeletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Rows:      lookup.DBRows,
	}
	for index, file := range lookup.Files {
		backupName := filepath.Join("files", fmt.Sprintf("%02d-%s", index+1, filepath.Base(file.Path)))
		if err := copyFileIfExists(file.Path, filepath.Join(backupDir, backupName)); err != nil {
			return "", err
		}
		manifest.Files = append(manifest.Files, deletedSessionFileBackup{OriginalPath: file.Path, BackupName: backupName})
	}
	if len(manifest.Files) == 0 && len(manifest.Rows) == 0 {
		return "", errors.New("没有可备份的会话数据")
	}
	if err := atomicWriteJSON(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		return "", err
	}
	return token, nil
}

func sanitizeBackupTokenPart(value string) string {
	value = bareSessionID(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
		if builder.Len() >= 32 {
			break
		}
	}
	return builder.String()
}

func sessionDeleteBackupDir(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" || filepath.Base(token) != token || strings.Contains(token, string(filepath.Separator)) {
		return "", errors.New("撤销令牌无效")
	}
	return filepath.Join(stateDir(), "deleted-sessions", token), nil
}

func sqliteThreadRowsByIDs(dbPath string, ids []string) ([]sessionSQLiteRow, error) {
	ids = uniqueNonEmptyStrings(ids)
	if len(ids) == 0 || !fileExists(dbPath) {
		return nil, nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil || len(columns) == 0 || !containsString(columns, "id") {
		return nil, err
	}
	query := "SELECT * FROM threads WHERE id IN (" + sqlitePlaceholders(len(ids)) + ")"
	rows, err := querySessionSQLiteRows(db, query, stringsToAny(ids)...)
	return annotateSessionRows(rows, dbPath, "threads"), err
}

func sqliteThreadRowsByTitle(dbPath, title string, archivedOnly bool) ([]sessionSQLiteRow, error) {
	title = strings.TrimSpace(title)
	if title == "" || !fileExists(dbPath) {
		return nil, nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil || len(columns) == 0 || !containsString(columns, "title") {
		return nil, err
	}
	where := "WHERE COALESCE(title, '') = ?"
	if archivedOnly && containsString(columns, "archived") {
		where += " AND COALESCE(archived, 0) <> 0"
	}
	order := sqliteSortOrder(columns)
	rows, err := querySessionSQLiteRows(db, "SELECT * FROM threads "+where+order+" LIMIT 5", title)
	rows = annotateSessionRows(rows, dbPath, "threads")
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	// Archived rows can be rendered with trimmed or decorated text in the UI, so keep a conservative normalized fallback.
	rows, err = sqliteThreadRowsByNormalizedTitle(db, title, archivedOnly, columns)
	return annotateSessionRows(rows, dbPath, "threads"), err
}

func sqliteAutomationRunRowsByIDs(dbPath string, ids []string) ([]sessionSQLiteRow, error) {
	ids = uniqueNonEmptyStrings(ids)
	if len(ids) == 0 || !fileExists(dbPath) {
		return nil, nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "automation_runs")
	if err != nil || len(columns) == 0 || !containsString(columns, "thread_id") {
		return nil, err
	}
	query := "SELECT * FROM automation_runs WHERE thread_id IN (" + sqlitePlaceholders(len(ids)) + ")"
	rows, err := querySessionSQLiteRows(db, query, stringsToAny(ids)...)
	return annotateSessionRows(rows, dbPath, "automation_runs"), err
}

func sqliteAutomationRunRowsByTitle(dbPath, title string, archivedOnly bool) ([]sessionSQLiteRow, error) {
	title = strings.TrimSpace(title)
	if title == "" || !fileExists(dbPath) {
		return nil, nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "automation_runs")
	if err != nil || len(columns) == 0 || !containsString(columns, "thread_id") {
		return nil, err
	}
	titleColumn := ""
	if containsString(columns, "thread_title") {
		titleColumn = "thread_title"
	} else if containsString(columns, "title") {
		titleColumn = "title"
	}
	if titleColumn == "" {
		return nil, nil
	}
	where := "WHERE COALESCE(" + quoteSQLiteIdentifier(titleColumn) + ", '') = ?"
	if archivedOnly && containsString(columns, "status") {
		where += " AND COALESCE(status, '') = 'archived'"
	}
	rows, err := querySessionSQLiteRows(db, "SELECT * FROM automation_runs "+where+" LIMIT 5", title)
	rows = annotateSessionRows(rows, dbPath, "automation_runs")
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	rows, err = sqliteAutomationRunRowsByNormalizedTitle(db, title, archivedOnly, columns, titleColumn)
	return annotateSessionRows(rows, dbPath, "automation_runs"), err
}

func sqliteAutomationRunRowsByNormalizedTitle(db *sql.DB, title string, archivedOnly bool, columns []string, titleColumn string) ([]sessionSQLiteRow, error) {
	where := ""
	if archivedOnly && containsString(columns, "status") {
		where = "WHERE COALESCE(status, '') = 'archived'"
	}
	rows, err := querySessionSQLiteRows(db, "SELECT * FROM automation_runs "+where+" LIMIT 200")
	if err != nil {
		return nil, err
	}
	target := normalizedSessionTitle(title)
	for _, row := range rows {
		if normalizedSessionTitle(stringFromAny(row.Values[titleColumn])) == target {
			return []sessionSQLiteRow{row}, nil
		}
	}
	return nil, nil
}

func sqliteThreadRowsByNormalizedTitle(db *sql.DB, title string, archivedOnly bool, columns []string) ([]sessionSQLiteRow, error) {
	where := ""
	if archivedOnly && containsString(columns, "archived") {
		where = "WHERE COALESCE(archived, 0) <> 0"
	}
	order := sqliteSortOrder(columns)
	rows, err := querySessionSQLiteRows(db, "SELECT * FROM threads "+where+order+" LIMIT 200")
	if err != nil {
		return nil, err
	}
	target := normalizedSessionTitle(title)
	for _, row := range rows {
		if normalizedSessionTitle(stringFromAny(row.Values["title"])) == target {
			return []sessionSQLiteRow{row}, nil
		}
	}
	return nil, nil
}

func normalizedSessionTitle(title string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
}

func sqliteTableColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query("PRAGMA table_info(" + quoteSQLiteIdentifier(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func querySessionSQLiteRows(db *sql.DB, query string, args ...any) ([]sessionSQLiteRow, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result []sessionSQLiteRow
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		scan := make([]any, len(columns))
		for index := range values {
			scan[index] = &values[index]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, err
		}
		row := sessionSQLiteRow{Columns: append([]string{}, columns...), Values: map[string]any{}}
		for index, column := range columns {
			if values[index].Valid {
				row.Values[column] = values[index].String
			} else {
				row.Values[column] = nil
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func deleteSQLiteThreadRows(dbPath string, ids []string) error {
	return deleteSQLiteSessionRows(dbPath, "threads", ids)
}

func deleteSQLiteSessionRows(dbPath, table string, ids []string) error {
	ids = uniqueNonEmptyStrings(ids)
	if len(ids) == 0 || !fileExists(dbPath) {
		return nil
	}
	idColumn := ""
	switch table {
	case "threads":
		idColumn = "id"
	case "automation_runs":
		idColumn = "thread_id"
	default:
		return fmt.Errorf("unsupported session SQLite table %q", table)
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, table)
	if err != nil || len(columns) == 0 || !containsString(columns, idColumn) {
		return err
	}
	_, err = db.Exec("DELETE FROM "+quoteSQLiteIdentifier(table)+" WHERE "+quoteSQLiteIdentifier(idColumn)+" IN ("+sqlitePlaceholders(len(ids))+")", stringsToAny(ids)...)
	return err
}

func updateSQLiteThreadWorkspace(dbPath string, ids []string, targetCWD string) error {
	ids = uniqueNonEmptyStrings(ids)
	if len(ids) == 0 || !fileExists(dbPath) {
		return nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil || len(columns) == 0 || !containsString(columns, "id") || !containsString(columns, "cwd") {
		return err
	}
	args := append([]any{targetCWD}, stringsToAny(ids)...)
	_, err = db.Exec("UPDATE threads SET cwd = ? WHERE id IN ("+sqlitePlaceholders(len(ids))+")", args...)
	return err
}

func restoreSQLiteThreadRows(dbPath string, rows []sessionSQLiteRow) error {
	return restoreSQLiteSessionRows(dbPath, "threads", rows)
}

func restoreSQLiteSessionRows(dbPath, table string, rows []sessionSQLiteRow) error {
	if len(rows) == 0 {
		return nil
	}
	if table != "threads" && table != "automation_runs" {
		return fmt.Errorf("unsupported session SQLite table %q", table)
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	currentColumns, err := sqliteTableColumns(db, table)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, column := range currentColumns {
		current[column] = true
	}
	for _, row := range rows {
		var columns []string
		var args []any
		for _, column := range row.Columns {
			if !current[column] {
				continue
			}
			columns = append(columns, column)
			args = append(args, row.Values[column])
		}
		if len(columns) == 0 {
			continue
		}
		quoted := make([]string, len(columns))
		for index, column := range columns {
			quoted[index] = quoteSQLiteIdentifier(column)
		}
		query := "INSERT OR REPLACE INTO " + quoteSQLiteIdentifier(table) + " (" + strings.Join(quoted, ", ") + ") VALUES (" + sqlitePlaceholders(len(columns)) + ")"
		if _, err := db.Exec(query, args...); err != nil {
			return err
		}
	}
	return nil
}

func sortKeyForSession(home, sessionID string) sessionSortKey {
	key := sessionSortKey{SessionID: sessionID}
	lookup, err := lookupSession(home, sessionID, "", false)
	if err == nil {
		key.SessionID = lookup.canonicalOr(sessionID)
		for _, row := range lookup.DBRows {
			key = newerSortKey(key, sortKeyFromSQLiteRow(row, key.SessionID))
		}
		for _, file := range lookup.Files {
			key = newerSortKey(key, sessionSortKey{SessionID: file.SessionID, UpdatedAtMs: file.UpdatedAtMs, CreatedAtMs: file.CreatedAtMs})
		}
	}
	if key.CreatedAtMs == 0 {
		key.CreatedAtMs = uuidV7TimestampMs(sessionID)
	}
	if key.UpdatedAtMs == 0 {
		key.UpdatedAtMs = key.CreatedAtMs
	}
	return key
}

func sortKeyFromSQLiteRow(row sessionSQLiteRow, fallbackID string) sessionSortKey {
	key := sessionSortKey{SessionID: firstString(row.Values["id"], fallbackID)}
	key.UpdatedAt = int64FromFlexible(row.Values["updated_at"])
	key.UpdatedAtMs = int64FromFlexible(row.Values["updated_at_ms"])
	key.CreatedAtMs = int64FromFlexible(row.Values["created_at_ms"])
	if key.UpdatedAtMs == 0 && key.UpdatedAt > 0 {
		key.UpdatedAtMs = timestampValueToMs(key.UpdatedAt)
	}
	if key.CreatedAtMs == 0 {
		key.CreatedAtMs = timestampValueToMs(int64FromFlexible(row.Values["created_at"]))
	}
	if key.CreatedAtMs == 0 {
		key.CreatedAtMs = uuidV7TimestampMs(key.SessionID)
	}
	return key
}

func newerSortKey(left, right sessionSortKey) sessionSortKey {
	if right.SessionID != "" && left.SessionID == "" {
		left.SessionID = right.SessionID
	}
	if right.CreatedAtMs > 0 && left.CreatedAtMs == 0 {
		left.CreatedAtMs = right.CreatedAtMs
	}
	if right.UpdatedAt > 0 && left.UpdatedAt == 0 {
		left.UpdatedAt = right.UpdatedAt
	}
	if right.UpdatedAtMs > left.UpdatedAtMs {
		return right
	}
	return left
}

func sortKeyPayload(key sessionSortKey) map[string]any {
	result := map[string]any{}
	if key.UpdatedAt > 0 {
		result["updated_at"] = key.UpdatedAt
	}
	if key.UpdatedAtMs > 0 {
		result["updated_at_ms"] = key.UpdatedAtMs
	}
	if key.CreatedAtMs > 0 {
		result["created_at_ms"] = key.CreatedAtMs
	}
	return result
}

func timestampValueToMs(value int64) int64 {
	if value <= 0 {
		return 0
	}
	if value < 1000000000000 {
		return value * 1000
	}
	return value
}

func timestampMsFromAny(value any) int64 {
	text := strings.TrimSpace(stringFromAny(value))
	if text == "" {
		return 0
	}
	if parsed := int64FromFlexible(text); parsed > 0 {
		return timestampValueToMs(parsed)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed.UnixMilli()
	}
	return 0
}

func uuidV7TimestampMs(sessionID string) int64 {
	id := strings.ReplaceAll(bareSessionID(sessionID), "-", "")
	if len(id) < 12 {
		return 0
	}
	for _, r := range id[:12] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return 0
		}
	}
	value, err := strconv.ParseInt(id[:12], 16, 64)
	if err != nil {
		return 0
	}
	return value
}

func int64FromFlexible(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return 0
		}
		if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
			return int64(parsed)
		}
	}
	return 0
}

func sqlitePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for index := range parts {
		parts[index] = "?"
	}
	return strings.Join(parts, ", ")
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for index, value := range values {
		args[index] = value
	}
	return args
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func sqliteSortOrder(columns []string) string {
	switch {
	case containsString(columns, "updated_at_ms"):
		return " ORDER BY COALESCE(updated_at_ms, 0) DESC"
	case containsString(columns, "updated_at"):
		return " ORDER BY COALESCE(updated_at, 0) DESC"
	case containsString(columns, "created_at_ms"):
		return " ORDER BY COALESCE(created_at_ms, 0) DESC"
	case containsString(columns, "created_at"):
		return " ORDER BY COALESCE(created_at, 0) DESC"
	default:
		return ""
	}
}
