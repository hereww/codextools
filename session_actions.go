package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sessionDeleteBackupVersion = 1

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

func isSessionDataRoute(path string) bool {
	switch path {
	case "/delete", "/undo", "/archived-thread", "/move-thread-workspace", "/thread-sort-key", "/thread-sort-keys":
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
	case "/thread-sort-key":
		return threadSortKeyDataRoute(payload)
	case "/thread-sort-keys":
		return threadSortKeysDataRoute(payload)
	default:
		return unsupportedBridgeDataRoute(path, payload)
	}
}

func deleteSessionDataRoute(payload map[string]any) map[string]any {
	sessionID := strings.TrimSpace(stringFromAny(payload["session_id"]))
	if sessionID == "" {
		return map[string]any{"status": "failed", "message": "删除失败：未找到会话 ID"}
	}
	home := codexHomeDir()
	lookup, err := lookupSession(home, sessionID, "", false)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：" + err.Error()}
	}
	undoToken, err := createSessionDeleteBackup(lookup)
	if err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：创建备份失败：" + err.Error()}
	}
	dbPath := filepath.Join(home, "state_5.sqlite")
	if err := deleteSQLiteThreadRows(dbPath, lookup.allIDs()); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "删除失败：更新会话索引失败：" + err.Error()}
	}
	for _, file := range lookup.Files {
		if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = restoreSQLiteThreadRows(dbPath, lookup.DBRows)
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
	for _, file := range manifest.Files {
		source := filepath.Join(backupDir, file.BackupName)
		if err := copyFileIfExists(source, file.OriginalPath); err != nil {
			return map[string]any{"status": "failed", "session_id": manifest.SessionID, "message": "撤销失败：恢复会话文件失败：" + err.Error()}
		}
	}
	if err := restoreSQLiteThreadRows(filepath.Join(codexHomeDir(), "state_5.sqlite"), manifest.Rows); err != nil {
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
	if err := updateSQLiteThreadWorkspace(filepath.Join(home, "state_5.sqlite"), lookup.allIDs(), targetCWD); err != nil {
		return map[string]any{"status": "failed", "session_id": sessionID, "message": "移动失败：更新会话索引失败：" + err.Error()}
	}
	key := sortKeyForSession(home, sessionID)
	result := sortKeyPayload(key)
	result["status"] = "moved"
	result["session_id"] = lookup.canonicalOr(sessionID)
	result["target_cwd"] = targetCWD
	result["message"] = "已移动到项目。"
	return result
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
	dbPath := filepath.Join(home, "state_5.sqlite")
	var rows []sessionSQLiteRow
	var err error
	if len(lookup.Variants) > 0 {
		rows, err = sqliteThreadRowsByIDs(dbPath, lookup.Variants)
	} else if strings.TrimSpace(title) != "" {
		rows, err = sqliteThreadRowsByTitle(dbPath, title, archivedOnly)
	}
	if err != nil {
		return lookup, err
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
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionRolloutFile{}, err
	}
	firstLine, separator := splitFirstLine(string(data))
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
			file, err := readRolloutFile(path)
			if err != nil {
				return nil
			}
			if idSet[file.SessionID] || idSet[bareSessionID(file.SessionID)] {
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
	return atomicWrite(file.Path, append(nextFirstLine, []byte(file.Separator)...))
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
	return querySessionSQLiteRows(db, query, stringsToAny(ids)...)
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
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	// Archived rows can be rendered with trimmed or decorated text in the UI, so keep a conservative normalized fallback.
	return sqliteThreadRowsByNormalizedTitle(db, title, archivedOnly, columns)
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
	if err != nil || len(columns) == 0 || !containsString(columns, "id") {
		return err
	}
	_, err = db.Exec("DELETE FROM threads WHERE id IN ("+sqlitePlaceholders(len(ids))+")", stringsToAny(ids)...)
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
	if len(rows) == 0 {
		return nil
	}
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	currentColumns, err := sqliteTableColumns(db, "threads")
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
		query := "INSERT OR REPLACE INTO threads (" + strings.Join(quoted, ", ") + ") VALUES (" + sqlitePlaceholders(len(columns)) + ")"
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
