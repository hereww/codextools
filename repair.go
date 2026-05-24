package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *server) syncProvidersNow() commandResult {
	result := runProviderSync(codexHomeDir())
	repairResult := repairCodexConfig(codexHomeDir(), codexConfigRepairOptions{Plugins: true})
	payload := map[string]any{
		"syncStatus":          result.Status,
		"targetProvider":      result.TargetProvider,
		"changedSessionFiles": result.ChangedSessionFiles,
		"sqliteRowsUpdated":   result.SQLiteRowsUpdated,
		"backupDir":           result.BackupDir,
		"syncMessage":         result.Message,
	}
	status := "ok"
	if result.Status == "skipped" {
		status = "not_checked"
	}
	if repairResult.Status == "failed" {
		status = "failed"
	}
	message := fmt.Sprintf("供应商已同步一次：%d 个会话文件，%d 行索引。%s", result.ChangedSessionFiles, result.SQLiteRowsUpdated, providerSyncExtraMessage(result.Message))
	if strings.TrimSpace(repairResult.Message) != "" {
		message = strings.TrimSpace(message + " " + repairResult.Message)
	}
	return commandResult{
		"status":              status,
		"message":             message,
		"syncStatus":          payload["syncStatus"],
		"targetProvider":      payload["targetProvider"],
		"changedSessionFiles": payload["changedSessionFiles"],
		"sqliteRowsUpdated":   payload["sqliteRowsUpdated"],
		"backupDir":           payload["backupDir"],
		"syncMessage":         payload["syncMessage"],
		"pluginCount":         repairResult.PluginCount,
		"marketplaceCount":    repairResult.MarketplaceCount,
		"pluginBackupPath":    repairResult.BackupPath,
	}
}

func (s *server) repairCodexPlugins() commandResult {
	result := repairCodexConfig(codexHomeDir(), codexConfigRepairOptions{Plugins: true})
	status := "ok"
	if result.Status == "failed" {
		status = "failed"
	}
	return commandResult{
		"status":           status,
		"message":          result.Message,
		"backupPath":       result.BackupPath,
		"pluginCount":      result.PluginCount,
		"marketplaceCount": result.MarketplaceCount,
		"mcpServerCount":   result.MCPServerCount,
		"configChanged":    result.PluginConfigChanged,
		"goalsEnabled":     result.GoalsEnabled,
		"configPath":       filepath.Join(codexHomeDir(), "config.toml"),
		"codexHome":        codexHomeDir(),
	}
}

func (s *server) repairCodexGoals() commandResult {
	result := repairCodexConfig(codexHomeDir(), codexConfigRepairOptions{Goals: true})
	status := "ok"
	if result.Status == "failed" {
		status = "failed"
	}
	return commandResult{
		"status":        status,
		"message":       result.Message,
		"backupPath":    result.BackupPath,
		"goalsEnabled":  result.GoalsEnabled,
		"configChanged": result.GoalsConfigChanged,
		"configPath":    filepath.Join(codexHomeDir(), "config.toml"),
		"codexHome":     codexHomeDir(),
	}
}

func providerSyncExtraMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || message == "Provider sync complete" || message == "Provider sync already up to date" {
		return ""
	}
	return message
}

func repairCodexConfig(home string, options codexConfigRepairOptions) codexConfigRepairResult {
	if !isDir(home) {
		return codexConfigRepairResult{Status: "failed", Message: "Codex home 不存在：" + home}
	}
	configPath := filepath.Join(home, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return codexConfigRepairResult{Status: "failed", Message: "读取 config.toml 失败：" + err.Error()}
	}
	original := string(data)
	updated := original
	result := codexConfigRepairResult{Status: "ok"}
	if options.Plugins {
		var pluginCount, marketplaceCount, mcpCount int
		updated, pluginCount, marketplaceCount, mcpCount = repairCodexPluginConfig(home, updated)
		result.PluginCount = pluginCount
		result.MarketplaceCount = marketplaceCount
		result.MCPServerCount = mcpCount
		result.PluginConfigChanged = updated != original
	}
	beforeGoals := updated
	if options.Goals {
		updated = repairCodexGoalsConfig(updated)
		result.GoalsEnabled = true
		result.GoalsConfigChanged = updated != beforeGoals
	}
	if updated != original {
		backupPath, err := backupCodexConfig(configPath, "config-repair")
		if err != nil {
			return codexConfigRepairResult{Status: "failed", Message: "备份 config.toml 失败：" + err.Error()}
		}
		result.BackupPath = &backupPath
		if err := atomicWrite(configPath, []byte(updated)); err != nil {
			return codexConfigRepairResult{Status: "failed", Message: "写入 config.toml 失败：" + err.Error(), BackupPath: &backupPath}
		}
	}
	result.Message = codexConfigRepairMessage(result, options, updated != original)
	return result
}

func repairCodexPluginConfig(home, contents string) (string, int, int, int) {
	updated := contents
	marketplaces := discoverCodexMarketplaces(home)
	for _, marketplace := range marketplaces {
		if strings.TrimSpace(marketplace.Source) == "" {
			continue
		}
		if !hasTable(updated, "marketplaces."+marketplace.Name) {
			updated = appendTomlBlock(updated, []string{
				"[marketplaces." + marketplace.Name + "]",
				"last_updated = " + quoteToml(time.Now().UTC().Format(time.RFC3339)),
				`source_type = "local"`,
				"source = " + quoteToml(marketplace.Source),
			})
		}
	}
	plugins := discoverCachedPluginEnables(home)
	for _, plugin := range plugins {
		table := fmt.Sprintf("plugins.%s", quoteToml(plugin.Name+"@"+plugin.Marketplace))
		if !hasTable(updated, table) {
			updated = appendTomlBlock(updated, []string{
				"[" + table + "]",
				"enabled = true",
			})
		}
	}
	updated, mcpCount := repairNodeReplMCPConfig(home, updated)
	return updated, len(plugins), len(marketplaces), mcpCount
}

func repairCodexGoalsConfig(contents string) string {
	return upsertTableKey(contents, "features", "goals", "true")
}

func codexConfigRepairMessage(result codexConfigRepairResult, options codexConfigRepairOptions, changed bool) string {
	var parts []string
	if options.Plugins {
		if result.PluginCount == 0 {
			parts = append(parts, "未发现可恢复的插件缓存")
		} else if result.PluginConfigChanged {
			parts = append(parts, fmt.Sprintf("已恢复插件配置：%d 个插件、%d 个市场源", result.PluginCount, result.MarketplaceCount))
		} else {
			parts = append(parts, fmt.Sprintf("插件配置已完整：%d 个插件、%d 个市场源", result.PluginCount, result.MarketplaceCount))
		}
	}
	if options.Goals {
		if result.GoalsConfigChanged {
			parts = append(parts, "已开启追求目标功能 features.goals")
		} else {
			parts = append(parts, "追求目标功能已开启")
		}
	}
	if changed && result.BackupPath != nil {
		parts = append(parts, "已备份原配置："+*result.BackupPath)
	}
	if len(parts) == 0 {
		return "没有需要修复的配置。"
	}
	return strings.Join(parts, "；") + "。"
}

func backupCodexConfig(configPath, label string) (string, error) {
	backupPath := fmt.Sprintf("%s.before-%s-%s.bak", configPath, label, time.Now().Format("20060102150405"))
	if fileExists(backupPath) {
		for index := 2; ; index++ {
			candidate := fmt.Sprintf("%s.before-%s-%s-%d.bak", configPath, label, time.Now().Format("20060102150405"), index)
			if !fileExists(candidate) {
				backupPath = candidate
				break
			}
		}
	}
	return backupPath, copyFileIfExists(configPath, backupPath)
}

func discoverCodexMarketplaces(home string) []marketplaceSpec {
	paths := []marketplaceSpec{
		{Name: "openai-bundled", Source: filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled")},
		{Name: "openai-curated", Source: filepath.Join(home, ".tmp", "plugins")},
	}
	if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
		paths = append(paths, marketplaceSpec{Name: "openai-primary-runtime", Source: filepath.Join(userHome, ".cache", "codex-runtimes", "codex-primary-runtime", "plugins", "openai-primary-runtime")})
	}
	var marketplaces []marketplaceSpec
	for _, marketplace := range paths {
		if codexMarketplaceExists(marketplace.Source) {
			marketplaces = append(marketplaces, marketplace)
		}
	}
	return marketplaces
}

func codexMarketplaceExists(path string) bool {
	if !isDir(path) {
		return false
	}
	return fileExists(filepath.Join(path, ".agents", "plugins", "marketplace.json")) || isDir(filepath.Join(path, "plugins"))
}

func discoverCachedPluginEnables(home string) []pluginEnableSpec {
	cacheRoot := filepath.Join(home, "plugins", "cache")
	marketplaces := []string{"openai-curated", "openai-primary-runtime", "openai-bundled"}
	var plugins []pluginEnableSpec
	seen := map[string]bool{}
	for _, marketplace := range marketplaces {
		root := filepath.Join(cacheRoot, marketplace)
		if !isDir(root) {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !cachedPluginExists(filepath.Join(root, name)) {
				continue
			}
			key := name + "@" + marketplace
			if seen[key] {
				continue
			}
			seen[key] = true
			plugins = append(plugins, pluginEnableSpec{Name: name, Marketplace: marketplace})
		}
	}
	sort.Slice(plugins, func(i, j int) bool {
		left := plugins[i].Marketplace + "/" + plugins[i].Name
		right := plugins[j].Marketplace + "/" + plugins[j].Name
		return left < right
	})
	return plugins
}

func cachedPluginExists(path string) bool {
	if fileExists(filepath.Join(path, ".codex-plugin", "plugin.json")) {
		return true
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(path, entry.Name(), ".codex-plugin", "plugin.json")) {
			return true
		}
	}
	return false
}

func repairNodeReplMCPConfig(home, contents string) (string, int) {
	resourcesDir := codexResourcesDir()
	nodeReplPath := filepath.Join(resourcesDir, "node_repl")
	nodePath := filepath.Join(resourcesDir, "node")
	codexCLIPath := filepath.Join(resourcesDir, "codex")
	if !fileExists(nodeReplPath) || !fileExists(nodePath) {
		return contents, 0
	}
	updated := removeTable(removeTable(contents, "mcp_servers.node_repl"), "mcp_servers.node_repl.env")
	lines := []string{
		"[mcp_servers.node_repl]",
		"args = []",
		"command = " + quoteToml(nodeReplPath),
		"startup_timeout_sec = 120",
		"",
		"[mcp_servers.node_repl.env]",
		`BROWSER_USE_AVAILABLE_BACKENDS = "chrome,iab"`,
		`BROWSER_USE_MARKETPLACE_NAME = "openai-bundled"`,
		"CODEX_CLI_PATH = " + quoteToml(codexCLIPath),
		"CODEX_HOME = " + quoteToml(home),
		`NODE_REPL_NATIVE_PIPE_CONNECT_TIMEOUT_MS = "1000"`,
		`NODE_REPL_NODE_MODULE_DIRS = ""`,
		"NODE_REPL_NODE_PATH = " + quoteToml(nodePath),
	}
	if hashes := trustedBrowserClientHashes(home); len(hashes) > 0 {
		lines = append(lines, "NODE_REPL_TRUSTED_BROWSER_CLIENT_SHA256S = "+quoteToml(strings.Join(hashes, ",")))
	}
	lines = append(lines,
		"NODE_REPL_TRUSTED_CODE_PATHS = "+quoteToml(home),
		`NODE_REPL_UNTRUSTED_ENV_ALLOWLIST = "BROWSER_USE_MARKETPLACE_NAME"`,
	)
	if servicePath := bundledComputerUseServicePath(home); servicePath != "" {
		lines = append(lines, "SKY_CUA_SERVICE_PATH = "+quoteToml(servicePath))
	}
	return appendTomlBlock(updated, lines), 1
}

func trustedBrowserClientHashes(home string) []string {
	var hashes []string
	seen := map[string]bool{}
	pattern := filepath.Join(home, "plugins", "cache", "openai-bundled", "browser", "*", "scripts", "browser-client.mjs")
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if !seen[hash] {
			seen[hash] = true
			hashes = append(hashes, hash)
		}
	}
	return hashes
}

func bundledComputerUseServicePath(home string) string {
	candidates := []string{
		filepath.Join(home, ".tmp", "bundled-marketplaces", "openai-bundled", "plugins", "computer-use", "Codex Computer Use.app"),
	}
	matches, _ := filepath.Glob(filepath.Join(home, "plugins", "cache", "openai-bundled", "computer-use", "*", "Codex Computer Use.app"))
	sort.Strings(matches)
	candidates = append(candidates, matches...)
	for i := len(candidates) - 1; i >= 0; i-- {
		if isDir(candidates[i]) {
			return candidates[i]
		}
	}
	return ""
}

func codexResourcesDir() string {
	if path := resolveCodexApp(loadSettings().CodexAppPath); path != "" && runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(path), ".app") {
		return filepath.Join(path, "Contents", "Resources")
	}
	if runtime.GOOS == "darwin" && isDir("/Applications/Codex.app/Contents/Resources") {
		return "/Applications/Codex.app/Contents/Resources"
	}
	return filepath.Dir(companionBinaryPath("codex"))
}

func appendTomlBlock(contents string, lines []string) string {
	trimmedLines := append([]string{}, lines...)
	for len(trimmedLines) > 0 && strings.TrimSpace(trimmedLines[len(trimmedLines)-1]) == "" {
		trimmedLines = trimmedLines[:len(trimmedLines)-1]
	}
	if len(trimmedLines) == 0 {
		return ensureTrailingNewline(contents)
	}
	updated := strings.TrimRight(contents, "\n")
	if strings.TrimSpace(updated) != "" {
		updated += "\n\n"
	}
	updated += strings.Join(trimmedLines, "\n")
	return ensureTrailingNewline(updated)
}

func hasTable(contents, table string) bool {
	header := "[" + table + "]"
	for _, line := range splitLines(contents) {
		if strings.TrimSpace(line) == header {
			return true
		}
	}
	return false
}

func upsertTableKey(contents, table, key, value string) string {
	lines := splitLines(contents)
	header := "[" + table + "]"
	tableStart := -1
	tableEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if tableStart >= 0 {
				tableEnd = i
				break
			}
			if trimmed == header {
				tableStart = i
			}
		}
	}
	if tableStart < 0 {
		return appendTomlBlock(contents, []string{header, key + " = " + value})
	}
	for i := tableStart + 1; i < tableEnd; i++ {
		if rootLineKey(lines[i]) == key {
			lines[i] = key + " = " + value
			return ensureTrailingNewline(strings.Join(lines, "\n"))
		}
	}
	lines = append(lines[:tableEnd], append([]string{key + " = " + value}, lines[tableEnd:]...)...)
	return ensureTrailingNewline(strings.Join(lines, "\n"))
}

func runProviderSync(home string) providerSyncResult {
	if !isDir(home) {
		return providerSyncResult{Status: "skipped", Message: "Codex home not found: " + home, TargetProvider: "openai"}
	}
	targetProvider := readCurrentProvider(filepath.Join(home, "config.toml"))
	lockDir := filepath.Join(home, "tmp", "provider-sync.lock")
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync lock exists: " + lockDir, TargetProvider: targetProvider}
	}
	defer os.RemoveAll(lockDir)
	_ = os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(fmt.Sprintf(`{"pid":%d,"startedAt":%d}`, os.Getpid(), time.Now().Unix())), 0o644)

	changes, err := collectSessionChanges(home, targetProvider)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	var rewriteChanges []sessionChange
	threadIDs := map[string]bool{}
	cwdByThreadID := map[string]string{}
	for _, change := range changes {
		if change.RewriteNeeded {
			rewriteChanges = append(rewriteChanges, change)
		}
		if change.HasUserEvent && change.ThreadID != "" {
			threadIDs[change.ThreadID] = true
		}
		if change.ThreadID != "" && change.CWD != "" {
			cwdByThreadID[change.ThreadID] = change.CWD
		}
	}
	sqliteCount := countSQLiteUpdates(filepath.Join(home, "state_5.sqlite"), targetProvider, threadIDs, cwdByThreadID)
	globalCount := countGlobalStateUpdates(filepath.Join(home, ".codex-global-state.json"))
	if len(rewriteChanges) == 0 && sqliteCount == 0 && globalCount == 0 {
		return providerSyncResult{Status: "synced", Message: "Provider sync already up to date", TargetProvider: targetProvider}
	}
	backupDir, err := createProviderSyncBackup(home, targetProvider, rewriteChanges)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	if err := applySessionChanges(rewriteChanges); err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider, BackupDir: &backupDir}
	}
	sqliteRows, sqliteErr := applySQLiteUpdates(filepath.Join(home, "state_5.sqlite"), targetProvider, threadIDs, cwdByThreadID)
	if sqliteErr != nil {
		_ = restoreSessionChanges(rewriteChanges)
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + sqliteErr.Error(), TargetProvider: targetProvider, BackupDir: &backupDir}
	}
	if _, err := applyGlobalStateUpdate(filepath.Join(home, ".codex-global-state.json")); err != nil {
		_ = restoreSessionChanges(rewriteChanges)
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider, BackupDir: &backupDir}
	}
	pruneProviderSyncBackups(home)
	return providerSyncResult{Status: "synced", Message: "Provider sync complete", TargetProvider: targetProvider, BackupDir: &backupDir, ChangedSessionFiles: len(rewriteChanges), SQLiteRowsUpdated: sqliteRows}
}

func readCurrentProvider(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "openai"
	}
	provider := rootKeyString(string(data), "model_provider")
	if provider == "" {
		return "openai"
	}
	return provider
}

func collectSessionChanges(home, targetProvider string) ([]sessionChange, error) {
	var files []string
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
			if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	changes := make([]sessionChange, 0, len(files))
	for _, path := range files {
		textBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		firstLine, separator := splitFirstLine(string(textBytes))
		if strings.TrimSpace(firstLine) == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(firstLine), &record) != nil {
			continue
		}
		payload, ok := record["payload"].(map[string]any)
		if !ok {
			continue
		}
		threadID := stringFromAny(payload["id"])
		cwd := toDesktopWorkspacePath(stringFromAny(payload["cwd"]))
		rewriteNeeded := stringFromAny(payload["model_provider"]) != targetProvider
		if rewriteNeeded {
			payload["model_provider"] = targetProvider
		}
		nextFirstLine := firstLine
		if rewriteNeeded {
			data, err := json.Marshal(record)
			if err != nil {
				return nil, err
			}
			nextFirstLine = string(data)
		}
		changes = append(changes, sessionChange{
			Path: path, OriginalFirstLine: firstLine, NextFirstLine: nextFirstLine, Separator: separator,
			ThreadID: threadID, CWD: cwd, HasUserEvent: strings.Contains(separator, `"user_message"`) || strings.Contains(separator, `"user_input"`), RewriteNeeded: rewriteNeeded,
		})
	}
	return changes, nil
}

func splitFirstLine(text string) (string, string) {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index], text[index:]
	}
	return text, ""
}

func toDesktopWorkspacePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, `\\?\unc\`) {
		return `\\` + strings.ReplaceAll(value[8:], "/", `\`)
	}
	if strings.HasPrefix(lower, `\\?\`) {
		return strings.ReplaceAll(value[4:], `\`, "/")
	}
	return value
}

func createProviderSyncBackup(home, targetProvider string, changes []sessionChange) (string, error) {
	root := filepath.Join(home, "backups_state", "provider-sync")
	name := time.Now().Format("20060102150405")
	backupDir := filepath.Join(root, name)
	for suffix := 0; fileExists(backupDir); suffix++ {
		backupDir = filepath.Join(root, fmt.Sprintf("%s-%d", name, suffix+1))
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{"config.toml", ".codex-global-state.json", ".codex-global-state.json.bak"} {
		_ = copyFileIfExists(filepath.Join(home, name), filepath.Join(backupDir, name))
	}
	dbDir := filepath.Join(backupDir, "db")
	for _, name := range []string{"state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm"} {
		if fileExists(filepath.Join(home, name)) {
			_ = os.MkdirAll(dbDir, 0o755)
			_ = copyFileIfExists(filepath.Join(home, name), filepath.Join(dbDir, name))
		}
	}
	manifest := make([]map[string]any, 0, len(changes))
	for _, change := range changes {
		manifest = append(manifest, map[string]any{"path": change.Path, "originalFirstLine": change.OriginalFirstLine, "separator": change.Separator})
	}
	if err := atomicWriteJSON(filepath.Join(backupDir, "session-meta-backup.json"), manifest); err != nil {
		return "", err
	}
	if err := atomicWriteJSON(filepath.Join(backupDir, "metadata.json"), map[string]any{"managedBy": "Codex++ provider sync", "targetProvider": targetProvider}); err != nil {
		return "", err
	}
	return backupDir, nil
}

func copyFileIfExists(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

func applySessionChanges(changes []sessionChange) error {
	for _, change := range changes {
		if err := os.WriteFile(change.Path, []byte(change.NextFirstLine+change.Separator), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func restoreSessionChanges(changes []sessionChange) error {
	for _, change := range changes {
		if err := os.WriteFile(change.Path, []byte(change.OriginalFirstLine+change.Separator), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func countSQLiteUpdates(path, targetProvider string, threadIDs map[string]bool, cwdByThreadID map[string]string) int {
	count, _ := sqliteScalarInt(path, "SELECT COUNT(*) FROM threads WHERE COALESCE(model_provider, '') <> ?", targetProvider)
	if sqliteHasColumn(path, "threads", "has_user_event") {
		for id := range threadIDs {
			n, _ := sqliteScalarInt(path, "SELECT COUNT(*) FROM threads WHERE id = ? AND COALESCE(has_user_event, 0) <> 1", id)
			count += n
		}
	}
	if sqliteHasColumn(path, "threads", "cwd") {
		for id, cwd := range cwdByThreadID {
			n, _ := sqliteScalarInt(path, "SELECT COUNT(*) FROM threads WHERE id = ? AND COALESCE(cwd, '') <> ?", id, cwd)
			count += n
		}
	}
	return count
}

func applySQLiteUpdates(path, targetProvider string, threadIDs map[string]bool, cwdByThreadID map[string]string) (int, error) {
	if !fileExists(path) || !sqliteHasColumn(path, "threads", "model_provider") {
		return 0, nil
	}
	providerRows, err := sqliteExecRows(path, "UPDATE threads SET model_provider = ? WHERE COALESCE(model_provider, '') <> ?", targetProvider, targetProvider)
	if err != nil {
		return 0, err
	}
	if sqliteHasColumn(path, "threads", "has_user_event") {
		for id := range threadIDs {
			if _, err := sqliteExecRows(path, "UPDATE threads SET has_user_event = 1 WHERE id = ? AND COALESCE(has_user_event, 0) <> 1", id); err != nil {
				return providerRows, err
			}
		}
	}
	if sqliteHasColumn(path, "threads", "cwd") {
		for id, cwd := range cwdByThreadID {
			if _, err := sqliteExecRows(path, "UPDATE threads SET cwd = ? WHERE id = ? AND COALESCE(cwd, '') <> ?", cwd, id, cwd); err != nil {
				return providerRows, err
			}
		}
	}
	return providerRows, nil
}

func sqliteHasColumn(path, table, column string) bool {
	if !fileExists(path) {
		return false
	}
	out, err := sqliteQuery(path, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) > 1 && parts[1] == column {
			return true
		}
	}
	return false
}

func sqliteScalarInt(path, query string, args ...string) (int, error) {
	out, err := sqliteQuery(path, query, args...)
	if err != nil {
		return 0, err
	}
	value, _ := strconv.Atoi(strings.TrimSpace(out))
	return value, nil
}

func sqliteExecRows(path, query string, args ...string) (int, error) {
	script := "BEGIN;\n" + sqliteStatement(query, args...) + ";\nSELECT changes();\nCOMMIT;\n"
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(script)
	result, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(result)))
	}
	lines := strings.Split(strings.TrimSpace(string(result)), "\n")
	if len(lines) == 0 {
		return 0, nil
	}
	rows, _ := strconv.Atoi(strings.TrimSpace(lines[len(lines)-1]))
	return rows, nil
}

func sqliteQuery(path, query string, args ...string) (string, error) {
	cmd := exec.Command("sqlite3", path, sqliteStatement(query, args...))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func sqliteStatement(query string, args ...string) string {
	statement := query
	for _, arg := range args {
		statement = strings.Replace(statement, "?", quoteSQLiteValue(arg), 1)
	}
	return statement
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteSQLiteValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func loadGlobalState(path string) map[string]any {
	var state map[string]any
	if err := readJSON(path, &state); err != nil || state == nil {
		return map[string]any{}
	}
	return state
}

func normalizedGlobalState(state map[string]any) map[string]any {
	next := map[string]any{}
	if value, ok := state["electron-saved-workspace-roots"]; ok {
		next["electron-saved-workspace-roots"] = dedupePaths(pathArray(value))
	}
	if value, ok := state["project-order"]; ok {
		next["project-order"] = dedupePaths(pathArray(value))
	}
	if value, ok := state["active-workspace-roots"]; ok {
		normalized := dedupePaths(pathArray(value))
		if _, array := value.([]any); array {
			next["active-workspace-roots"] = normalized
		} else if len(normalized) > 0 {
			next["active-workspace-roots"] = normalized[0]
		} else {
			next["active-workspace-roots"] = value
		}
	}
	if value, ok := state["electron-workspace-root-labels"].(map[string]any); ok {
		labels := map[string]any{}
		for key, item := range value {
			normalized := toDesktopWorkspacePath(key)
			if normalized == "" {
				normalized = key
			}
			labels[normalized] = item
		}
		next["electron-workspace-root-labels"] = labels
	}
	return next
}

func countGlobalStateUpdates(path string) int {
	state := loadGlobalState(path)
	next := normalizedGlobalState(state)
	count := 0
	for key, value := range next {
		if !jsonEqual(state[key], value) {
			count++
		}
	}
	return count
}

func applyGlobalStateUpdate(path string) (int, error) {
	state := loadGlobalState(path)
	next := normalizedGlobalState(state)
	count := 0
	for key, value := range next {
		if !jsonEqual(state[key], value) {
			state[key] = value
			count++
		}
	}
	if count > 0 {
		return count, atomicWriteJSON(path, state)
	}
	return 0, nil
}

func pathArray(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return typed
	case string:
		if strings.TrimSpace(typed) != "" {
			return []string{typed}
		}
	}
	return nil
}

func dedupePaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		desktop := toDesktopWorkspacePath(path)
		if desktop == "" {
			continue
		}
		comparable := strings.ToLower(strings.TrimRight(strings.ReplaceAll(desktop, "/", `\`), `\`))
		if seen[comparable] {
			continue
		}
		seen[comparable] = true
		out = append(out, desktop)
	}
	return out
}

func jsonEqual(left, right any) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return bytes.Equal(leftBytes, rightBytes)
}

func pruneProviderSyncBackups(home string) {
	root := filepath.Join(home, "backups_state", "provider-sync")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var managed []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var meta map[string]any
		if readJSON(filepath.Join(path, "metadata.json"), &meta) == nil && stringFromAny(meta["managedBy"]) == "Codex++ provider sync" {
			managed = append(managed, path)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(managed)))
	if len(managed) <= 5 {
		return
	}
	for _, path := range managed[5:] {
		_ = os.RemoveAll(path)
	}
}
