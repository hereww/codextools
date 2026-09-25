package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type codexCommandOutput struct {
	Command string
	Output  string
	Err     error
}

var runCodexPluginCommand = defaultRunCodexPluginCommand
var currentRuntimeGOOS = func() string { return runtime.GOOS }
var detectProviderSyncActiveProcesses = defaultConversationHistoryDirectProcesses
var applyProviderSyncGlobalStateUpdate = applyGlobalStateUpdate
var acquireProviderSyncLauncherGuard = defaultConversationHistoryLauncherGuard

const (
	providerSyncLockTTL             = 30 * time.Minute
	providerSyncUnknownOwnerLockTTL = 2 * time.Minute
	providerSyncMetadataReadLimit   = 512 * 1024
)

func (s *server) syncProvidersNow(argSets ...map[string]any) commandResult {
	args := map[string]any{}
	if len(argSets) > 0 && argSets[0] != nil {
		args = argSets[0]
	}
	targetProvider := strings.TrimSpace(firstNonEmpty(stringArg(args, "targetProvider"), stringArg(mapArg(args, "request"), "targetProvider")))
	result := runProviderSyncTarget(codexHomeDir(), targetProvider)
	status := "ok"
	if result.Status == "skipped" {
		status = "not_checked"
	} else if result.Status == "failed" {
		status = "failed"
	}
	message := "模式对话历史已检查，无需更新。"
	if result.Status == "skipped" {
		message = "模式对话历史同步暂未执行且未修改聊天记录，可重试。"
		if detail := providerSyncExtraMessage(result.Message); detail != "" {
			message += " " + detail
		}
	} else if result.Status == "failed" {
		switch {
		case result.Partial:
			message = "模式对话历史同步失败且回滚失败，可能处于部分同步状态。 " + result.Message
		case result.RollbackStatus == "rolled_back":
			message = "模式对话历史同步失败，已回滚本次聊天记录改动。 " + result.Message
		default:
			message = "模式对话历史同步失败，未完成同步。 " + result.Message
		}
	} else if result.ChangedSessionFiles > 0 || result.SQLiteRowsUpdated > 0 {
		message = fmt.Sprintf("模式对话历史已同步：%d 个会话文件，%d 行索引。", result.ChangedSessionFiles, result.SQLiteRowsUpdated)
	}
	return commandResult{
		"status":              status,
		"message":             strings.TrimSpace(message),
		"syncStatus":          result.Status,
		"targetProvider":      result.TargetProvider,
		"changedSessionFiles": result.ChangedSessionFiles,
		"sqliteRowsUpdated":   result.SQLiteRowsUpdated,
		"backupDir":           result.BackupDir,
		"syncMessage":         result.Message,
		"partial":             result.Partial,
		"rollbackStatus":      result.RollbackStatus,
	}
}

func (s *server) repairCodexPlugins() commandResult {
	result := repairCodexConfig(codexHomeDir(), codexConfigRepairOptions{Plugins: true, RefreshMarketplaces: true})
	status := "ok"
	if result.Status == "failed" {
		status = "failed"
	}
	return commandResult{
		"status":                    status,
		"message":                   result.Message,
		"backupPath":                result.BackupPath,
		"pluginCount":               result.PluginCount,
		"marketplaceCount":          result.MarketplaceCount,
		"mcpServerCount":            result.MCPServerCount,
		"marketplaceRefreshStatus":  result.MarketplaceRefreshStatus,
		"marketplaceRefreshSummary": result.MarketplaceRefreshSummary,
		"marketplaceRefreshError":   result.MarketplaceRefreshError,
		"configChanged":             result.PluginConfigChanged,
		"goalsEnabled":              result.GoalsEnabled,
		"configPath":                filepath.Join(codexHomeDir(), "config.toml"),
		"codexHome":                 codexHomeDir(),
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
	changed := updated != original
	if updated != original {
		backupPath, err := writeCodexConfigWithBackup(configPath, updated, "config-repair")
		if err != nil {
			return codexConfigRepairResult{Status: "failed", Message: "写入 config.toml 失败：" + err.Error(), BackupPath: backupPath}
		}
		result.BackupPath = backupPath
	}
	if options.Plugins && options.RefreshMarketplaces {
		refresh := refreshCodexMarketplaces(home)
		result.MarketplaceRefreshStatus = refresh.Status
		result.MarketplaceRefreshSummary = refresh.Summary
		result.MarketplaceRefreshError = refresh.Error
		if refresh.Status == "failed" {
			result.Status = "failed"
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			result.Status = "failed"
			result.MarketplaceRefreshError = strings.TrimSpace(result.MarketplaceRefreshError + "；刷新后读取 config.toml 失败：" + err.Error())
		} else {
			refreshedOriginal := string(data)
			refreshedUpdated, pluginCount, marketplaceCount, mcpCount := repairCodexPluginConfig(home, refreshedOriginal)
			result.PluginCount = pluginCount
			result.MarketplaceCount = marketplaceCount
			result.MCPServerCount = mcpCount
			if refreshedUpdated != refreshedOriginal {
				backupPath, err := writeCodexConfigWithBackup(configPath, refreshedUpdated, "config-repair")
				if err != nil {
					return codexConfigRepairResult{Status: "failed", Message: "写入刷新后的 config.toml 失败：" + err.Error(), BackupPath: backupPath}
				}
				if result.BackupPath == nil {
					result.BackupPath = backupPath
				}
				result.PluginConfigChanged = true
				changed = true
			}
		}
	}
	result.Message = codexConfigRepairMessage(result, options, changed)
	return result
}

func repairCodexPluginConfig(home, contents string) (string, int, int, int) {
	updated := contents
	marketplaces := discoverCodexMarketplaces(home)
	for _, marketplace := range marketplaces {
		if strings.TrimSpace(marketplace.Source) == "" {
			continue
		}
		updated = repairCodexMarketplaceTable(updated, marketplace)
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

func repairCodexMarketplaceTable(contents string, marketplace marketplaceSpec) string {
	table := "marketplaces." + marketplace.Name
	lastUpdated := quoteToml(time.Now().UTC().Format(time.RFC3339))
	if !hasTable(contents, table) {
		return appendTomlBlock(contents, []string{
			"[" + table + "]",
			"last_updated = " + lastUpdated,
			`source_type = "local"`,
			"source = " + quoteToml(marketplace.Source),
		})
	}
	values := tableValues(contents, table)
	sourceType := strings.TrimSpace(unquoteToml(values["source_type"]))
	source := strings.TrimSpace(unquoteToml(values["source"]))
	if sourceType == "local" && samePath(source, marketplace.Source) {
		return contents
	}
	updated := upsertTableKey(contents, table, "last_updated", lastUpdated)
	updated = upsertTableKey(updated, table, "source_type", quoteToml("local"))
	updated = upsertTableKey(updated, table, "source", quoteToml(marketplace.Source))
	return updated
}

type marketplaceRefreshResult struct {
	Status  string
	Summary string
	Error   string
}

func refreshCodexMarketplaces(home string) marketplaceRefreshResult {
	if !isDir(home) {
		return marketplaceRefreshResult{Status: "skipped", Summary: "Codex home 不存在，已跳过 marketplace 刷新。"}
	}
	if !hasRefreshableCodexMarketplaces(home) {
		return marketplaceRefreshResult{Status: "skipped", Summary: "未发现已配置或本地可用的 Codex marketplace。"}
	}
	commands := [][]string{
		{"plugin", "marketplace", "upgrade"},
		{"plugin", "marketplace", "list"},
		{"plugin", "list"},
	}
	var summaries []string
	var failures []string
	for _, args := range commands {
		output := runCodexPluginCommand(home, args...)
		label := "codex " + strings.Join(args, " ")
		if strings.TrimSpace(output.Command) != "" {
			label = output.Command
		}
		if output.Err != nil {
			failures = append(failures, label+": "+output.Err.Error()+outputPreview(output.Output))
			continue
		}
		summaries = append(summaries, label+outputPreview(output.Output))
	}
	if len(failures) > 0 {
		return marketplaceRefreshResult{
			Status:  "failed",
			Summary: strings.Join(summaries, "；"),
			Error:   strings.Join(failures, "；"),
		}
	}
	return marketplaceRefreshResult{Status: "ok", Summary: strings.Join(summaries, "；")}
}

func hasRefreshableCodexMarketplaces(home string) bool {
	if len(discoverCodexMarketplaces(home)) > 0 {
		return true
	}
	contents := readFile(filepath.Join(home, "config.toml"))
	for _, line := range splitLines(contents) {
		if strings.HasPrefix(strings.TrimSpace(line), "[marketplaces.") {
			return true
		}
	}
	return false
}

func defaultRunCodexPluginCommand(home string, args ...string) codexCommandOutput {
	command := codexCLIExecutable()
	label := "codex " + strings.Join(args, " ")
	if strings.TrimSpace(command) == "" {
		return codexCommandOutput{Command: label, Err: fmt.Errorf("未找到可用的 Codex CLI（已跳过 WindowsApps alias）")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	hideSubprocessWindow(cmd)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = ctx.Err()
	}
	return codexCommandOutput{Command: label, Output: string(out), Err: err}
}

func codexCLIExecutable() string {
	if path := usableCodexCLIExecutable(); path != "" {
		return path
	}
	if path := bundledCodexCLIExecutable(); path != "" {
		return path
	}
	if path, err := exec.LookPath("codex"); err == nil {
		if usableCodexCLIPath(path) {
			return path
		}
	}
	if currentRuntimeGOOS() != "windows" {
		return "codex"
	}
	return ""
}

func bundledCodexCLIExecutable() string {
	resourcesDir := codexResourcesDir()
	candidate := filepath.Join(resourcesDir, "codex")
	if currentRuntimeGOOS() == "windows" {
		candidate += ".exe"
	}
	if usableCodexCLIPath(candidate) {
		return candidate
	}
	return ""
}

func usableCodexCLIExecutable() string {
	if explicit := strings.TrimSpace(os.Getenv("CODEX_CLI_PATH")); usableCodexCLIPath(explicit) {
		return explicit
	}
	if currentRuntimeGOOS() != "windows" {
		return ""
	}
	for _, candidate := range discoverWindowsCodexRuntimeCLIs() {
		if usableCodexCLIPath(candidate) {
			return candidate
		}
	}
	if path, err := exec.LookPath("codex.exe"); err == nil && usableCodexCLIPath(path) {
		return path
	}
	return ""
}

func discoverWindowsCodexRuntimeCLIs() []string {
	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path != "" {
			candidates = append(candidates, path)
		}
	}
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		binRoot := filepath.Join(root, "OpenAI", "Codex", "bin")
		matches, _ := filepath.Glob(filepath.Join(binRoot, "*", "codex.exe"))
		sort.Strings(matches)
		for i := len(matches) - 1; i >= 0; i-- {
			add(matches[i])
		}
		add(filepath.Join(binRoot, "codex.exe"))
	}
	return candidates
}

func usableCodexCLIPath(path string) bool {
	if strings.TrimSpace(path) == "" || !fileExists(path) {
		return false
	}
	if currentRuntimeGOOS() == "windows" && isWindowsAppsPath(path) {
		return false
	}
	return true
}

func isWindowsAppsPath(path string) bool {
	normalized := strings.ReplaceAll(filepath.Clean(path), `\`, `/`)
	normalized = strings.ToLower(filepath.ToSlash(normalized))
	for _, part := range strings.Split(normalized, "/") {
		if part == "windowsapps" {
			return true
		}
	}
	return false
}

func outputPreview(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	output = strings.Join(strings.Fields(output), " ")
	if len(output) > 240 {
		output = output[:240] + "..."
	}
	return "（" + output + "）"
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
		if options.RefreshMarketplaces {
			switch result.MarketplaceRefreshStatus {
			case "ok":
				parts = append(parts, "Codex 插件市场已刷新/重读")
			case "skipped":
				parts = append(parts, "Codex 插件市场刷新已跳过")
			case "failed":
				if strings.TrimSpace(result.MarketplaceRefreshError) != "" {
					parts = append(parts, "Codex 插件市场刷新失败："+result.MarketplaceRefreshError)
				} else {
					parts = append(parts, "Codex 插件市场刷新失败")
				}
			}
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
		{Name: "openai-api-curated", Source: filepath.Join(home, ".tmp", "plugins")},
		{Name: "openai-curated-remote", Source: filepath.Join(home, ".tmp", "plugins-remote")},
		{Name: "role-specific-plugins", Source: filepath.Join(home, ".tmp", "marketplaces", "role-specific-plugins")},
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
	marketplaces := []string{"openai-curated", "openai-api-curated", "openai-curated-remote", "role-specific-plugins", "openai-primary-runtime", "openai-bundled"}
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
	if !fileExists(nodeReplPath) {
		nodeReplPath = companionBinaryPath(managerBinary)
	}
	if !fileExists(nodePath) {
		nodePath = companionBinaryPath(managerBinary)
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
		"CODEX_HOME = " + quoteToml(home),
		`NODE_REPL_NATIVE_PIPE_CONNECT_TIMEOUT_MS = "1000"`,
		`NODE_REPL_NODE_MODULE_DIRS = ""`,
		"NODE_REPL_NODE_PATH = " + quoteToml(nodePath),
	}
	if codexCLIPath := codexCLIExecutable(); codexCLIPath != "" && fileExists(codexCLIPath) {
		lines = append(lines[:8], append([]string{"CODEX_CLI_PATH = " + quoteToml(codexCLIPath)}, lines[8:]...)...)
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
	if path := resolveCodexApp(loadSettings().CodexAppPath); path != "" && runtime.GOOS == "windows" {
		if resources := filepath.Join(path, "resources"); isDir(resources) {
			return resources
		}
		if resources := filepath.Join(path, "app", "resources"); isDir(resources) {
			return resources
		}
	}
	if runtime.GOOS == "darwin" && isDir("/Applications/ChatGPT.app/Contents/Resources") {
		return "/Applications/ChatGPT.app/Contents/Resources"
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

func removeTableKey(contents, table, key string) string {
	bom, contents := splitTomlBOM(contents)
	lines := splitLines(contents)
	header := "[" + table + "]"
	tableStart := -1
	tableEnd := len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if tableStart >= 0 {
				tableEnd = index
				break
			}
			if trimmed == header {
				tableStart = index
			}
		}
	}
	if tableStart < 0 {
		return bom + contents
	}
	filtered := make([]string, 0, len(lines)-1)
	for index, line := range lines {
		if index > tableStart && index < tableEnd && rootLineKey(line) == key {
			continue
		}
		filtered = append(filtered, line)
	}
	return bom + strings.Join(filtered, "\n")
}

func runProviderSync(home string) providerSyncResult {
	return runProviderSyncWithLock(home, true, "")
}

func runProviderSyncTarget(home, targetProvider string) providerSyncResult {
	return runProviderSyncWithLock(home, true, targetProvider)
}

// runProviderSyncWithHeldLauncherGuard is used by the managed launcher, which
// already holds the launcher single-instance guard for its full lifetime.
func runProviderSyncWithHeldLauncherGuard(home string) providerSyncResult {
	return runProviderSyncWithLock(home, false, "")
}

func runProviderSyncWithLock(home string, acquireLauncherGuard bool, targetProvider string) providerSyncResult {
	if !isDir(home) {
		return providerSyncResult{Status: "skipped", Message: "Codex home not found: " + home, TargetProvider: "openai"}
	}
	releaseLock, err := acquireProviderSyncLock(home, "provider-sync")
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error()}
	}
	defer releaseLock()
	if acquireLauncherGuard {
		releaseLauncherGuard, err := acquireProviderSyncLauncherGuard()
		if err != nil {
			return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error()}
		}
		defer releaseLauncherGuard()
	}
	return runProviderSyncLocked(home, targetProvider)
}

// runProviderSyncLocked synchronizes history while the caller holds the
// provider-sync lock. It reads the provider only after that lock is held so a
// mode switch cannot change config.toml between provider selection and sync.
func runProviderSyncLocked(home, explicitTargetProvider string) providerSyncResult {
	targetProvider := strings.TrimSpace(explicitTargetProvider)
	if targetProvider == "" {
		targetProvider = readCurrentProvider(filepath.Join(home, "config.toml"))
	} else if !validProviderSyncTargetID(targetProvider) {
		return providerSyncResult{Status: "skipped", Message: fmt.Sprintf("Invalid provider sync target: %q", targetProvider), TargetProvider: targetProvider}
	}
	changes, err := collectSessionChanges(home, targetProvider)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	globalState, err := readCodexGlobalState(home)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	projectless := map[string]bool{}
	for _, id := range stringsFromAnySlice(globalState["projectless-thread-ids"]) {
		projectless[id] = true
		projectless[bareSessionID(id)] = true
	}
	for index := range changes {
		if projectless[changes[index].ThreadID] || projectless[bareSessionID(changes[index].ThreadID)] {
			changes[index].CWD = ""
		}
	}
	var rewriteChanges []sessionChange
	for _, change := range changes {
		if change.RewriteNeeded {
			rewriteChanges = append(rewriteChanges, change)
		}
	}
	sqlitePaths := codexSessionDBPaths(home)
	sqliteCount := 0
	for _, path := range sqlitePaths {
		sqliteCount += countSQLiteUpdates(path, targetProvider, changes)
	}
	globalCount, err := countGlobalStateUpdates(filepath.Join(home, ".codex-global-state.json"), changes)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	if len(rewriteChanges) == 0 && sqliteCount == 0 && globalCount == 0 {
		return providerSyncResult{Status: "synced", Message: "Provider sync already up to date", TargetProvider: targetProvider}
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	backupDir, err := createProviderSyncBackup(home, targetProvider, rewriteChanges)
	if err != nil {
		return providerSyncResult{Status: "skipped", Message: "Provider sync skipped: " + err.Error(), TargetProvider: targetProvider}
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return providerSyncFailureBeforeMutation(targetProvider, backupDir, err)
	}
	globalPath := filepath.Join(home, ".codex-global-state.json")
	globalSnapshot, err := captureProviderSyncFileSnapshot(globalPath)
	if err != nil {
		return providerSyncFailureBeforeMutation(targetProvider, backupDir, err)
	}
	databaseSnapshots, err := captureProviderSyncDatabaseSnapshots(sqlitePaths)
	if err != nil {
		return providerSyncFailureBeforeMutation(targetProvider, backupDir, err)
	}
	if err := applySessionChanges(rewriteChanges); err != nil {
		return providerSyncSessionFailure(targetProvider, backupDir, err)
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		rollbackErr := rollbackProviderSyncFiles(rewriteChanges, globalSnapshot, databaseSnapshots)
		return providerSyncRollbackFailure(targetProvider, backupDir, err, rollbackErr)
	}
	if _, err := applyProviderSyncGlobalStateUpdate(globalPath, changes); err != nil {
		rollbackErr := rollbackProviderSyncFiles(rewriteChanges, globalSnapshot, databaseSnapshots)
		return providerSyncRollbackFailure(targetProvider, backupDir, err, rollbackErr)
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		rollbackErr := rollbackProviderSyncFiles(rewriteChanges, globalSnapshot, databaseSnapshots)
		return providerSyncRollbackFailure(targetProvider, backupDir, err, rollbackErr)
	}
	sqliteRows := 0
	for _, path := range sqlitePaths {
		updated, sqliteErr := applySQLiteUpdates(path, targetProvider, changes)
		if sqliteErr != nil {
			rollbackErr := rollbackProviderSyncFiles(rewriteChanges, globalSnapshot, databaseSnapshots)
			return providerSyncRollbackFailure(targetProvider, backupDir, sqliteErr, rollbackErr)
		}
		sqliteRows += updated
	}
	pruneProviderSyncBackups(home)
	return providerSyncResult{Status: "synced", Message: "Provider sync complete", TargetProvider: targetProvider, BackupDir: &backupDir, ChangedSessionFiles: len(rewriteChanges), SQLiteRowsUpdated: sqliteRows}
}

func acquireProviderSyncLock(home, operation string) (func(), error) {
	lockDir := filepath.Join(home, "tmp", "provider-sync.lock")
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return nil, err
	}
	lockAcquired := false
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		if stale, reason := providerSyncLockStale(lockDir, time.Now()); stale {
			appendDiagnosticLog("provider_sync.stale_lock_removed", map[string]any{"lock": lockDir, "reason": reason})
			_ = os.RemoveAll(lockDir)
			if retryErr := os.Mkdir(lockDir, 0o755); retryErr == nil {
				lockAcquired = true
			}
		}
		if !lockAcquired {
			return nil, fmt.Errorf("Provider sync lock exists: %s", lockDir)
		}
	} else {
		lockAcquired = true
	}
	release := func() { _ = os.RemoveAll(lockDir) }
	owner := map[string]any{"pid": os.Getpid(), "startedAt": time.Now().Unix(), "operation": strings.TrimSpace(operation)}
	ownerData, err := json.Marshal(owner)
	if err != nil {
		release()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), ownerData, 0o644); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func acquireProviderSyncMutationGuards(home, operation string) (func(), error) {
	releaseProviderLock, err := acquireProviderSyncLock(home, operation)
	if err != nil {
		return nil, err
	}
	releaseLauncherGuard, err := acquireProviderSyncLauncherGuard()
	if err != nil {
		releaseProviderLock()
		return nil, err
	}
	return func() {
		releaseLauncherGuard()
		releaseProviderLock()
	}, nil
}

func ensureProviderSyncWritersStopped() error {
	active, err := detectProviderSyncActiveProcesses()
	if err != nil {
		return fmt.Errorf("检查 ChatGPT/Codex 运行状态失败：%w", err)
	}
	active = uniqueConversationHistoryProcessNames(active)
	if len(active) > 0 {
		return fmt.Errorf("请先完全退出 ChatGPT 和 Codex 后重试（仍在运行：%s）", strings.Join(active, "、"))
	}
	return nil
}

func providerSyncFailureBeforeMutation(targetProvider, backupDir string, operationErr error) providerSyncResult {
	return providerSyncResult{
		Status:         "failed",
		Message:        "Provider sync failed before history changes: " + operationErr.Error(),
		TargetProvider: targetProvider,
		BackupDir:      &backupDir,
		RollbackStatus: "not_started",
	}
}

func providerSyncSessionFailure(targetProvider, backupDir string, operationErr error) providerSyncResult {
	var mutationErr *providerSyncMutationError
	if errors.As(operationErr, &mutationErr) {
		if mutationErr.RollbackErr == nil {
			rollbackStatus := "rolled_back"
			message := "Provider sync failed: " + mutationErr.OperationErr.Error() + "; provider sync changes were rolled back"
			if mutationErr.AppliedFiles == 0 {
				rollbackStatus = "not_started"
				message = "Provider sync failed before a session file was changed: " + mutationErr.OperationErr.Error()
			}
			return providerSyncResult{
				Status:         "failed",
				Message:        message,
				TargetProvider: targetProvider,
				BackupDir:      &backupDir,
				RollbackStatus: rollbackStatus,
			}
		}
		return providerSyncResult{
			Status:         "failed",
			Message:        "Provider sync failed: " + mutationErr.OperationErr.Error() + "; rollback failed and history may be partially synchronized: " + mutationErr.RollbackErr.Error(),
			TargetProvider: targetProvider,
			BackupDir:      &backupDir,
			Partial:        true,
			RollbackStatus: "rollback_failed",
		}
	}
	return providerSyncResult{
		Status:         "failed",
		Message:        "Provider sync failed and history state may be partial: " + operationErr.Error(),
		TargetProvider: targetProvider,
		BackupDir:      &backupDir,
		Partial:        true,
		RollbackStatus: "rollback_unknown",
	}
}

func providerSyncRollbackFailure(targetProvider, backupDir string, operationErr, rollbackErr error) providerSyncResult {
	message := "Provider sync failed: " + operationErr.Error()
	if rollbackErr == nil {
		message += "; provider sync changes were rolled back"
		return providerSyncResult{Status: "failed", Message: message, TargetProvider: targetProvider, BackupDir: &backupDir, RollbackStatus: "rolled_back"}
	}
	message += "; rollback failed and history may be partially synchronized: " + rollbackErr.Error()
	return providerSyncResult{Status: "failed", Message: message, TargetProvider: targetProvider, BackupDir: &backupDir, Partial: true, RollbackStatus: "rollback_failed"}
}

func rollbackProviderSyncFiles(changes []sessionChange, globalSnapshot providerSyncFileSnapshot, databaseSnapshots []providerSyncFileSnapshot) error {
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return fmt.Errorf("rollback not attempted because a history writer is active: %w", err)
	}
	databaseErr := restoreProviderSyncDatabaseSnapshots(databaseSnapshots)
	globalErr := restoreProviderSyncFileSnapshot(globalSnapshot)
	sessionErr := restoreSessionChanges(changes)
	return errors.Join(databaseErr, globalErr, sessionErr)
}

func captureProviderSyncDatabaseSnapshots(paths []string) ([]providerSyncFileSnapshot, error) {
	snapshots := make([]providerSyncFileSnapshot, 0, len(paths)*3)
	for _, path := range paths {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			snapshot, err := captureProviderSyncFileSnapshot(path + suffix)
			if err != nil {
				return nil, fmt.Errorf("capture SQLite snapshot %s: %w", path+suffix, err)
			}
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots, nil
}

func restoreProviderSyncDatabaseSnapshots(snapshots []providerSyncFileSnapshot) error {
	var restoreErrors []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		if err := restoreProviderSyncFileSnapshot(snapshots[index]); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("%s: %w", snapshots[index].Path, err))
		}
	}
	return errors.Join(restoreErrors...)
}

func providerSyncLockStale(lockDir string, now time.Time) (bool, string) {
	var owner struct {
		PID       int   `json:"pid"`
		StartedAt int64 `json:"startedAt"`
	}
	if err := readJSON(filepath.Join(lockDir, "owner.json"), &owner); err == nil {
		if owner.PID <= 0 && owner.StartedAt <= 0 {
			return providerSyncUnknownOwnerLockStale(lockDir, now, "owner_invalid")
		}
		if owner.PID > 0 {
			if providerSyncOwnerProcessRunning(owner.PID) {
				return false, "owner_active"
			}
			return true, "owner_process_missing"
		}
		startedAt := time.Unix(owner.StartedAt, 0)
		if owner.StartedAt > 0 && now.Sub(startedAt) > providerSyncLockTTL {
			return true, "owner_timeout"
		}
		return false, "owner_active"
	}
	return providerSyncUnknownOwnerLockStale(lockDir, now, "owner_missing")
}

func providerSyncUnknownOwnerLockStale(lockDir string, now time.Time, reason string) (bool, string) {
	info, err := os.Stat(lockDir)
	if err != nil {
		return true, "lock_stat_failed"
	}
	if now.Sub(info.ModTime()) > providerSyncUnknownOwnerLockTTL {
		return true, reason + "_timeout"
	}
	return false, reason + "_recent"
}

func providerSyncOwnerProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	if pid == os.Getpid() {
		return true
	}
	running, err := processIDRunning(pid)
	if err != nil {
		// A detection failure must not make another process's active lock look
		// stale, because deleting a live maintenance lock can corrupt history.
		return true
	}
	return running
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
		textBytes, truncated, err := readProviderSyncMetadata(path)
		if err != nil {
			return nil, err
		}
		text := string(textBytes)
		record, payload, originalMetaLines, nextMetaLines, rewriteNeeded, err := providerSyncSessionMetaRewrites(text, targetProvider)
		if err != nil {
			return nil, err
		}
		if len(originalMetaLines) == 0 {
			continue
		}
		threadID := stringFromAny(payload["id"])
		cwd := toDesktopWorkspacePath(stringFromAny(payload["cwd"]))
		meta := sessionChangeMetadata(path, text, record, payload)
		if truncated && !meta.HasUserEvent {
			// A transcript larger than the metadata window necessarily contains
			// history beyond session_meta. Preserve it as a real user session when
			// a missing SQLite catalog row needs to be reconstructed.
			meta.HasUserEvent = true
		}
		changes = append(changes, sessionChange{
			Path: path, OriginalFirstLine: originalMetaLines[0], NextFirstLine: nextMetaLines[0],
			OriginalSessionMetaLines: originalMetaLines, NextSessionMetaLines: nextMetaLines,
			ThreadID: threadID, CWD: firstString(cwd, meta.CWD), Source: meta.Source, Title: meta.Title, FirstUserMessage: meta.FirstUserMessage, Preview: meta.Preview,
			CreatedAt: meta.CreatedAt, UpdatedAt: meta.UpdatedAt, CreatedAtMs: meta.CreatedAtMs, UpdatedAtMs: meta.UpdatedAtMs,
			Archived: meta.Archived, CLIVersion: meta.CLIVersion, Model: meta.Model, ReasoningEffort: meta.ReasoningEffort,
			SandboxPolicy: meta.SandboxPolicy, ApprovalMode: meta.ApprovalMode,
			HasUserEvent: meta.HasUserEvent, RewriteNeeded: rewriteNeeded,
		})
	}
	return changes, nil
}

func readProviderSyncMetadata(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, providerSyncMetadataReadLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) <= providerSyncMetadataReadLimit {
		return data, false, nil
	}
	data = data[:providerSyncMetadataReadLimit]
	lastNewline := bytes.LastIndexByte(data, '\n')
	if lastNewline < 0 {
		return nil, true, fmt.Errorf("session metadata line exceeds %d bytes: %s", providerSyncMetadataReadLimit, path)
	}
	return data[:lastNewline+1], true, nil
}

func providerSyncSessionMetaRewrites(text, targetProvider string) (map[string]any, map[string]any, []string, []string, bool, error) {
	var firstRecord map[string]any
	var firstPayload map[string]any
	originals := []string{}
	replacements := []string{}
	rewriteNeeded := false
	for _, segment := range strings.SplitAfter(text, "\n") {
		line := strings.TrimSuffix(segment, "\n")
		body, _ := providerSyncFirstLineParts(line)
		if strings.TrimSpace(body) == "" {
			continue
		}
		var record map[string]any
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if decoder.Decode(&record) != nil || stringFromAny(record["type"]) != "session_meta" {
			continue
		}
		payload, ok := record["payload"].(map[string]any)
		if !ok {
			continue
		}
		if firstRecord == nil {
			firstRecord = record
			firstPayload = payload
		}
		originals = append(originals, body)
		nextLine := body
		if stringFromAny(payload["model_provider"]) != targetProvider {
			payload["model_provider"] = targetProvider
			data, err := json.Marshal(record)
			if err != nil {
				return nil, nil, nil, nil, false, err
			}
			nextLine = string(data)
			rewriteNeeded = true
		}
		replacements = append(replacements, nextLine)
	}
	return firstRecord, firstPayload, originals, replacements, rewriteNeeded, nil
}

func sessionChangeMetadata(path, text string, record, payload map[string]any) sessionChange {
	createdAtMs := timestampMsFromAny(firstString(payload["timestamp"], record["timestamp"]))
	if createdAtMs == 0 {
		createdAtMs = uuidV7TimestampMs(stringFromAny(payload["id"]))
	}
	updatedAtMs := timestampMsFromAny(firstString(record["timestamp"], payload["timestamp"]))
	meta := sessionChange{
		Source:          firstString(payload["source"], payload["originator"], "vscode"),
		CLIVersion:      stringFromAny(payload["cli_version"]),
		CreatedAtMs:     createdAtMs,
		UpdatedAtMs:     updatedAtMs,
		CreatedAt:       timestampMsToSeconds(createdAtMs),
		UpdatedAt:       timestampMsToSeconds(updatedAtMs),
		Archived:        strings.Contains(filepath.ToSlash(path), "/archived_sessions/"),
		HasUserEvent:    strings.Contains(text, `"user_message"`) || strings.Contains(text, `"user_input"`),
		SandboxPolicy:   `{"type":"danger-full-access"}`,
		ApprovalMode:    "never",
		ReasoningEffort: "",
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		itemPayload, _ := item["payload"].(map[string]any)
		if itemPayload == nil {
			itemPayload = item
		}
		if ms := timestampMsFromAny(firstString(item["timestamp"], itemPayload["timestamp"])); ms > meta.UpdatedAtMs {
			meta.UpdatedAtMs = ms
			meta.UpdatedAt = timestampMsToSeconds(ms)
		}
		if stringFromAny(item["type"]) == "turn_context" {
			if cwd := toDesktopWorkspacePath(stringFromAny(itemPayload["cwd"])); cwd != "" && meta.CWD == "" {
				meta.CWD = cwd
			}
			if model := stringFromAny(itemPayload["model"]); model != "" {
				meta.Model = model
			}
			if effort := stringFromAny(itemPayload["effort"]); effort != "" {
				meta.ReasoningEffort = effort
			}
			if approval := stringFromAny(itemPayload["approval_policy"]); approval != "" {
				meta.ApprovalMode = approval
			}
			if sandbox := compactJSONOrString(itemPayload["sandbox_policy"]); sandbox != "" {
				meta.SandboxPolicy = sandbox
			}
		}
		message := markdownMessageFromRolloutRecord(item)
		if message.Role != "user" {
			continue
		}
		text := sessionDisplayText(message.Text)
		if text == "" || strings.HasPrefix(text, "<environment_context>") {
			continue
		}
		meta.HasUserEvent = true
		if meta.FirstUserMessage == "" {
			meta.FirstUserMessage = text
			meta.Title = sessionTitleFromMessage(text)
			meta.Preview = sessionPreviewFromMessage(text)
		}
	}
	if meta.UpdatedAtMs == 0 {
		if info, err := os.Stat(path); err == nil {
			meta.UpdatedAtMs = info.ModTime().UnixMilli()
			meta.UpdatedAt = timestampMsToSeconds(meta.UpdatedAtMs)
		}
	}
	if meta.UpdatedAtMs == 0 {
		meta.UpdatedAtMs = meta.CreatedAtMs
		meta.UpdatedAt = meta.CreatedAt
	}
	if meta.Title == "" {
		meta.Title = firstString(payload["title"], record["title"], meta.FirstUserMessage, stringFromAny(payload["id"]))
	}
	if meta.Preview == "" {
		meta.Preview = firstString(meta.FirstUserMessage, meta.Title)
	}
	meta.Title = truncateRunes(meta.Title, 120)
	meta.Preview = truncateRunes(meta.Preview, 240)
	return meta
}

func timestampMsToSeconds(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return value / 1000
}

func compactJSONOrString(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return stringFromAny(value)
	}
	return string(data)
}

func sessionDisplayText(value string) string {
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	var out []string
	skipContext := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# Context from my IDE setup:") {
			continue
		}
		if strings.HasPrefix(trimmed, "## My request for Codex:") {
			continue
		}
		if strings.HasPrefix(trimmed, "<environment_context>") {
			skipContext = true
			continue
		}
		if skipContext {
			if strings.HasPrefix(trimmed, "</environment_context>") {
				skipContext = false
			}
			continue
		}
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " ")
}

func sessionTitleFromMessage(value string) string {
	value = normalizedSessionTitle(value)
	if value == "" {
		return ""
	}
	return truncateRunes(value, 80)
}

func sessionPreviewFromMessage(value string) string {
	return truncateRunes(normalizedSessionTitle(value), 180)
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
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
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	for _, name := range []string{"config.toml", ".codex-global-state.json", ".codex-global-state.json.bak"} {
		if _, err := copyProviderSyncBackupFileIfExists(filepath.Join(home, name), filepath.Join(backupDir, name)); err != nil {
			return fail(fmt.Errorf("备份 %s 失败：%w", name, err))
		}
	}
	databaseManifest := make([]map[string]any, 0)
	for index, dbPath := range codexSessionDBPaths(home) {
		name := fmt.Sprintf("%02d-%s", index+1, filepath.Base(dbPath))
		for _, suffix := range []string{"", "-wal", "-shm"} {
			backupRelative := filepath.Join("db", name+suffix)
			copied, err := copyProviderSyncBackupFileIfExists(dbPath+suffix, filepath.Join(backupDir, backupRelative))
			if err != nil {
				return fail(fmt.Errorf("备份 %s 失败：%w", dbPath+suffix, err))
			}
			if copied {
				databaseManifest = append(databaseManifest, map[string]any{
					"path":       dbPath + suffix,
					"backupPath": filepath.ToSlash(backupRelative),
				})
			}
		}
	}
	manifest := make([]map[string]any, 0, len(changes))
	for _, change := range changes {
		relative, err := filepath.Rel(home, change.Path)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if err == nil {
				err = fmt.Errorf("会话文件不在 Codex home 内")
			}
			return fail(fmt.Errorf("无法为 %s 创建安全备份路径：%w", change.Path, err))
		}
		backupRelative := filepath.Join("history", relative)
		backupPath := filepath.Join(backupDir, backupRelative)
		if err := copyProviderSyncBackupFile(change.Path, backupPath); err != nil {
			return fail(fmt.Errorf("备份会话文件 %s 失败：%w", change.Path, err))
		}
		info, err := os.Stat(backupPath)
		if err != nil {
			return fail(fmt.Errorf("读取会话备份状态 %s 失败：%w", backupPath, err))
		}
		manifest = append(manifest, map[string]any{
			"path":                     change.Path,
			"backupPath":               filepath.ToSlash(backupRelative),
			"originalFirstLine":        change.OriginalFirstLine,
			"originalSessionMetaLines": change.OriginalSessionMetaLines,
			"size":                     info.Size(),
			"mode":                     uint32(info.Mode().Perm()),
		})
	}
	if err := atomicWriteJSON(filepath.Join(backupDir, "session-meta-backup.json"), manifest); err != nil {
		return fail(err)
	}
	if err := atomicWriteJSON(filepath.Join(backupDir, "metadata.json"), map[string]any{
		"managedBy":       "ChatGPT Codex Tools provider sync",
		"targetProvider":  targetProvider,
		"databaseBackups": databaseManifest,
	}); err != nil {
		return fail(err)
	}
	return backupDir, nil
}

func copyProviderSyncBackupFileIfExists(source, target string) (bool, error) {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s 是目录", source)
	}
	return true, copyProviderSyncBackupFile(source, target)
}

func copyProviderSyncBackupFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	inputClosed := false
	defer func() {
		if !inputClosed {
			_ = input.Close()
		}
	}()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("%s 不是普通文件", source)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".provider-sync-backup-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	after, err := input.Stat()
	if err != nil {
		_ = temp.Close()
		return err
	}
	pathInfo, err := os.Stat(source)
	if err != nil || !os.SameFile(before, pathInfo) || after.Size() != before.Size() || after.ModTime().UnixNano() != before.ModTime().UnixNano() || pathInfo.Size() != before.Size() || pathInfo.ModTime().UnixNano() != before.ModTime().UnixNano() {
		_ = temp.Close()
		if err != nil {
			return err
		}
		return fmt.Errorf("%s 在备份期间发生变化", source)
	}
	if err := input.Close(); err != nil {
		_ = temp.Close()
		return err
	}
	inputClosed = true
	if err := prepareConversationHistoryTemp(temp, before.Mode()); err != nil {
		return err
	}
	return replaceFile(tempPath, target)
}

type providerSyncFileSnapshot struct {
	Path     string
	Exists   bool
	Contents []byte
	Mode     os.FileMode
}

func captureProviderSyncFileSnapshot(path string) (providerSyncFileSnapshot, error) {
	snapshot := providerSyncFileSnapshot{Path: path}
	before, err := os.Stat(path)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if !before.Mode().IsRegular() {
		return snapshot, fmt.Errorf("%s 不是普通文件", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return snapshot, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime().UnixNano() != after.ModTime().UnixNano() {
		return snapshot, fmt.Errorf("%s 在读取期间发生变化", path)
	}
	snapshot.Exists = true
	snapshot.Contents = contents
	snapshot.Mode = before.Mode()
	return snapshot, nil
}

func restoreProviderSyncFileSnapshot(snapshot providerSyncFileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(snapshot.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeProviderSyncAtomicFile(snapshot.Path, snapshot.Contents, snapshot.Mode)
}

func writeProviderSyncAtomicFile(path string, contents []byte, mode os.FileMode) error {
	if mode.Perm() == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".provider-sync-file-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(contents); err != nil {
		_ = temp.Close()
		return err
	}
	if err := prepareConversationHistoryTemp(temp, mode); err != nil {
		return err
	}
	return replaceFile(tempPath, path)
}

func copyFileIfExists(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

type providerSyncMutationError struct {
	OperationErr error
	RollbackErr  error
	AppliedFiles int
}

func (e *providerSyncMutationError) Error() string {
	if e.RollbackErr != nil {
		return fmt.Sprintf("%v; rollback failed: %v", e.OperationErr, e.RollbackErr)
	}
	if e.AppliedFiles > 0 {
		return e.OperationErr.Error() + "；已回滚本次已修改文件"
	}
	return e.OperationErr.Error()
}

func (e *providerSyncMutationError) Unwrap() error {
	return e.OperationErr
}

func applySessionChanges(changes []sessionChange) error {
	applied := make([]sessionChange, 0, len(changes))
	for _, change := range changes {
		if err := rewriteProviderSyncSessionChange(change, false); err != nil {
			rollbackErr := restoreSessionChanges(applied)
			return &providerSyncMutationError{
				OperationErr: fmt.Errorf("更新会话文件 %s 失败：%w", change.Path, err),
				RollbackErr:  rollbackErr,
				AppliedFiles: len(applied),
			}
		}
		applied = append(applied, change)
	}
	return nil
}

func restoreSessionChanges(changes []sessionChange) error {
	var rollbackErrors []error
	for index := len(changes) - 1; index >= 0; index-- {
		change := changes[index]
		if err := rewriteProviderSyncSessionChange(change, true); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("%s: %w", change.Path, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func rewriteProviderSyncSessionChange(change sessionChange, restore bool) error {
	expected := change.OriginalSessionMetaLines
	replacements := change.NextSessionMetaLines
	if restore {
		expected, replacements = replacements, expected
	}
	if len(expected) == 0 || len(expected) != len(replacements) {
		if restore {
			return rewriteProviderSyncSessionFirstLine(change.Path, change.NextFirstLine, change.OriginalFirstLine)
		}
		return rewriteProviderSyncSessionFirstLine(change.Path, change.OriginalFirstLine, change.NextFirstLine)
	}
	return rewriteProviderSyncSessionMetaLines(change.Path, expected, replacements)
}

func rewriteProviderSyncSessionMetaLines(path string, expectedLines, replacementLines []string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	inputClosed := false
	defer func() {
		if !inputClosed {
			_ = input.Close()
		}
	}()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	contents, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || after.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在读取期间发生变化", path)
	}
	segments := strings.SplitAfter(string(contents), "\n")
	metaSegmentIndexes := make([]int, 0, len(expectedLines))
	for index, segment := range segments {
		line := strings.TrimSuffix(segment, "\n")
		body, _ := providerSyncFirstLineParts(line)
		var record map[string]any
		if json.Unmarshal([]byte(body), &record) == nil && stringFromAny(record["type"]) == "session_meta" {
			metaSegmentIndexes = append(metaSegmentIndexes, index)
		}
	}
	if len(metaSegmentIndexes) < len(expectedLines) {
		return fmt.Errorf("%s 的会话元数据数量已变化", path)
	}
	for rewriteIndex := range expectedLines {
		segmentIndex := metaSegmentIndexes[rewriteIndex]
		segment := segments[segmentIndex]
		hasNewline := strings.HasSuffix(segment, "\n")
		line := strings.TrimSuffix(segment, "\n")
		body, suffix := providerSyncFirstLineParts(line)
		expectedBody, _ := providerSyncFirstLineParts(expectedLines[rewriteIndex])
		replacementBody, _ := providerSyncFirstLineParts(replacementLines[rewriteIndex])
		if body != expectedBody && body != replacementBody {
			return fmt.Errorf("%s 的第 %d 条会话元数据已变化", path, rewriteIndex+1)
		}
		if body == expectedBody && expectedBody != replacementBody {
			line = replacementBody + suffix
			if hasNewline {
				line += "\n"
			}
			segments[segmentIndex] = line
		}
	}
	nextContents := strings.Join(segments, "")
	if nextContents == string(contents) {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".provider-sync-session-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.WriteString(temp, nextContents); err != nil {
		_ = temp.Close()
		return err
	}
	if err := prepareConversationHistoryTemp(temp, before.Mode()); err != nil {
		return err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, pathInfo) || pathInfo.Size() != before.Size() || pathInfo.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在原子替换前发生变化", path)
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return err
	}
	finalInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, finalInfo) || finalInfo.Size() != before.Size() || finalInfo.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在最终原子替换前发生变化", path)
	}
	if err := input.Close(); err != nil {
		return err
	}
	inputClosed = true
	return replaceFile(tempPath, path)
}

func rewriteProviderSyncSessionFirstLine(path, expectedFirstLine, replacementFirstLine string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	inputClosed := false
	defer func() {
		if !inputClosed {
			_ = input.Close()
		}
	}()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	contents, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || after.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在读取期间发生变化", path)
	}
	firstLine, separator := splitFirstLine(string(contents))
	firstLineBody, firstLineSuffix := providerSyncFirstLineParts(firstLine)
	expectedBody, _ := providerSyncFirstLineParts(expectedFirstLine)
	replacementBody, _ := providerSyncFirstLineParts(replacementFirstLine)
	if firstLineBody == replacementBody {
		return nil
	}
	if firstLineBody != expectedBody {
		return fmt.Errorf("%s 的会话元数据已变化", path)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".provider-sync-session-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.WriteString(temp, replacementBody+firstLineSuffix+separator); err != nil {
		_ = temp.Close()
		return err
	}
	if err := prepareConversationHistoryTemp(temp, before.Mode()); err != nil {
		return err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, pathInfo) || pathInfo.Size() != before.Size() || pathInfo.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在原子替换前发生变化", path)
	}
	if err := ensureProviderSyncWritersStopped(); err != nil {
		return err
	}
	finalInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, finalInfo) || finalInfo.Size() != before.Size() || finalInfo.ModTime().UnixNano() != before.ModTime().UnixNano() {
		return fmt.Errorf("%s 在最终原子替换前发生变化", path)
	}
	if err := input.Close(); err != nil {
		return err
	}
	inputClosed = true
	return replaceFile(tempPath, path)
}

func providerSyncFirstLineParts(line string) (string, string) {
	if strings.HasSuffix(line, "\r") {
		return strings.TrimSuffix(line, "\r"), "\r"
	}
	return line, ""
}

func countSQLiteUpdates(path, targetProvider string, changes []sessionChange) int {
	if !fileExists(path) {
		return 0
	}
	db, err := openSQLite(path)
	if err != nil {
		return 0
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil || !containsString(columns, "id") {
		return 0
	}
	selected := []string{"id"}
	for _, name := range []string{"model_provider", "thread_source", "has_user_event", "cwd"} {
		if containsString(columns, name) {
			selected = append(selected, name)
		}
	}
	quoted := make([]string, len(selected))
	for index, name := range selected {
		quoted[index] = quoteSQLiteIdentifier(name)
	}
	rows, err := db.Query("SELECT " + strings.Join(quoted, ", ") + " FROM threads")
	if err != nil {
		return 0
	}
	defer rows.Close()
	changeByID := make(map[string]sessionChange, len(changes))
	for _, change := range changes {
		if change.ThreadID != "" {
			changeByID[change.ThreadID] = change
		}
	}
	seen := make(map[string]bool, len(changeByID))
	count := 0
	for rows.Next() {
		values := make([]any, len(selected))
		dest := make([]any, len(values))
		for index := range values {
			dest[index] = &values[index]
		}
		if rows.Scan(dest...) != nil {
			return 0
		}
		row := make(map[string]any, len(selected))
		for index, name := range selected {
			row[name] = values[index]
		}
		id := strings.TrimSpace(stringFromAny(row["id"]))
		change, hasChange := changeByID[id]
		if hasChange {
			seen[id] = true
		}
		if _, ok := row["model_provider"]; ok && stringFromAny(row["model_provider"]) != targetProvider {
			count++
		}
		if hasChange && change.HasUserEvent && stringFromAny(row["thread_source"]) == "" {
			if _, ok := row["thread_source"]; ok {
				count++
			}
		}
		if hasChange && change.HasUserEvent {
			if value, ok := row["has_user_event"]; ok && int64FromFlexible(value) != 1 {
				count++
			}
		}
		if hasChange && change.CWD != "" {
			if value, ok := row["cwd"]; ok && stringFromAny(value) != change.CWD {
				count++
			}
		}
	}
	if rows.Err() != nil {
		return 0
	}
	for id, change := range changeByID {
		if change.HasUserEvent && !seen[id] {
			count++
		}
	}
	return count
}

func countSQLiteThreadSourceUpdates(path string, changes []sessionChange) int {
	seen := map[string]bool{}
	count := 0
	for _, change := range changes {
		if !change.HasUserEvent || change.ThreadID == "" || seen[change.ThreadID] {
			continue
		}
		seen[change.ThreadID] = true
		n, _ := sqliteScalarInt(path, "SELECT COUNT(*) FROM threads WHERE id = ? AND COALESCE(thread_source, '') = ''", change.ThreadID)
		count += n
	}
	return count
}

func countMissingSQLiteThreads(path string, changes []sessionChange) int {
	seen := map[string]bool{}
	count := 0
	for _, change := range changes {
		if change.ThreadID == "" || !change.HasUserEvent || seen[change.ThreadID] {
			continue
		}
		seen[change.ThreadID] = true
		n, _ := sqliteScalarInt(path, "SELECT COUNT(*) FROM threads WHERE id = ?", change.ThreadID)
		if n == 0 {
			count++
		}
	}
	return count
}

func applySQLiteUpdates(path, targetProvider string, changes []sessionChange) (int, error) {
	if !fileExists(path) {
		return 0, nil
	}
	db, err := openSQLite(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	columns, err := sqliteTableColumns(db, "threads")
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 || !containsString(columns, "id") {
		return 0, nil
	}
	columnSet := map[string]bool{}
	for _, column := range columns {
		columnSet[column] = true
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	execRows := func(query string, args ...any) (int, error) {
		result, execErr := tx.Exec(query, args...)
		if execErr != nil {
			return 0, execErr
		}
		rows, _ := result.RowsAffected()
		return int(rows), nil
	}
	totalRows := 0
	if columnSet["model_provider"] {
		rows, err := execRows("UPDATE threads SET model_provider = ? WHERE COALESCE(model_provider, '') <> ?", targetProvider, targetProvider)
		if err != nil {
			return 0, err
		}
		totalRows += rows
	}
	if columnSet["thread_source"] {
		for _, change := range changes {
			if !change.HasUserEvent || change.ThreadID == "" {
				continue
			}
			rows, err := execRows("UPDATE threads SET thread_source = 'user' WHERE id = ? AND COALESCE(thread_source, '') = ''", change.ThreadID)
			if err != nil {
				return 0, err
			}
			totalRows += rows
		}
	}
	if columnSet["has_user_event"] {
		for _, change := range changes {
			if !change.HasUserEvent || change.ThreadID == "" {
				continue
			}
			rows, err := execRows("UPDATE threads SET has_user_event = 1 WHERE id = ? AND COALESCE(has_user_event, 0) <> 1", change.ThreadID)
			if err != nil {
				return 0, err
			}
			totalRows += rows
		}
	}
	if columnSet["cwd"] {
		for _, change := range changes {
			if change.ThreadID == "" || change.CWD == "" {
				continue
			}
			rows, err := execRows("UPDATE threads SET cwd = ? WHERE id = ? AND COALESCE(cwd, '') <> ?", change.CWD, change.ThreadID, change.CWD)
			if err != nil {
				return 0, err
			}
			totalRows += rows
		}
	}
	inserted, err := insertMissingSQLiteThreadsTx(tx, columns, targetProvider, changes)
	if err != nil {
		return 0, err
	}
	totalRows += inserted
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return totalRows, nil
}

func insertMissingSQLiteThreadsTx(tx *sql.Tx, columns []string, targetProvider string, changes []sessionChange) (int, error) {
	columnSet := map[string]bool{}
	for _, column := range columns {
		columnSet[column] = true
	}
	inserted := 0
	seen := map[string]bool{}
	for _, change := range changes {
		if change.ThreadID == "" || !change.HasUserEvent || seen[change.ThreadID] {
			continue
		}
		seen[change.ThreadID] = true
		var exists int
		if err := tx.QueryRow("SELECT COUNT(*) FROM threads WHERE id = ?", change.ThreadID).Scan(&exists); err != nil {
			return inserted, err
		}
		if exists > 0 {
			continue
		}
		row := sqliteThreadRowFromChange(change, targetProvider)
		var insertColumns []string
		var args []any
		for _, column := range columns {
			if !columnSet[column] {
				continue
			}
			if value, ok := row[column]; ok {
				insertColumns = append(insertColumns, column)
				args = append(args, value)
			}
		}
		if len(insertColumns) == 0 {
			continue
		}
		quoted := make([]string, len(insertColumns))
		for index, column := range insertColumns {
			quoted[index] = quoteSQLiteIdentifier(column)
		}
		query := "INSERT INTO threads (" + strings.Join(quoted, ", ") + ") VALUES (" + sqlitePlaceholders(len(insertColumns)) + ")"
		if _, err := tx.Exec(query, args...); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

func sqliteThreadRowFromChange(change sessionChange, targetProvider string) map[string]any {
	createdAtMs := change.CreatedAtMs
	if createdAtMs == 0 {
		createdAtMs = uuidV7TimestampMs(change.ThreadID)
	}
	updatedAtMs := change.UpdatedAtMs
	if updatedAtMs == 0 {
		updatedAtMs = createdAtMs
	}
	createdAt := change.CreatedAt
	if createdAt == 0 {
		createdAt = timestampMsToSeconds(createdAtMs)
	}
	updatedAt := change.UpdatedAt
	if updatedAt == 0 {
		updatedAt = timestampMsToSeconds(updatedAtMs)
	}
	title := firstString(change.Title, change.FirstUserMessage, change.ThreadID)
	preview := firstString(change.Preview, change.FirstUserMessage, title)
	threadSource := ""
	if change.HasUserEvent {
		threadSource = "user"
	}
	archived := 0
	if change.Archived {
		archived = 1
	}
	return map[string]any{
		"id":                 change.ThreadID,
		"rollout_path":       change.Path,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
		"source":             firstString(change.Source, "vscode"),
		"model_provider":     targetProvider,
		"cwd":                change.CWD,
		"title":              title,
		"sandbox_policy":     firstString(change.SandboxPolicy, `{"type":"danger-full-access"}`),
		"approval_mode":      firstString(change.ApprovalMode, "never"),
		"tokens_used":        0,
		"has_user_event":     boolInt(change.HasUserEvent),
		"archived":           archived,
		"archived_at":        nil,
		"git_sha":            nil,
		"git_branch":         nil,
		"git_origin_url":     nil,
		"cli_version":        change.CLIVersion,
		"first_user_message": firstString(change.FirstUserMessage, preview),
		"agent_nickname":     nil,
		"agent_role":         nil,
		"memory_mode":        "enabled",
		"model":              nullableString(change.Model),
		"reasoning_effort":   nullableString(change.ReasoningEffort),
		"agent_path":         nil,
		"created_at_ms":      createdAtMs,
		"updated_at_ms":      updatedAtMs,
		"thread_source":      nullableString(threadSource),
		"preview":            preview,
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
	db, err := openSQLite(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	result, err := db.Exec(query, sqliteArgs(args)...)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

func sqliteQuery(path, query string, args ...string) (string, error) {
	db, err := openSQLite(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.Query(query, sqliteArgs(args)...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	var lines []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		scan := make([]any, len(columns))
		for index := range values {
			scan[index] = &values[index]
		}
		if err := rows.Scan(scan...); err != nil {
			return "", err
		}
		parts := make([]string, len(columns))
		for index, value := range values {
			if value.Valid {
				parts[index] = value.String
			}
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 2000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sqliteArgs(args []string) []any {
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = arg
	}
	return values
}

func loadGlobalState(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 global state 失败：%w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var state map[string]any
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("解析 global state 失败：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("存在多余 JSON 值")
		}
		return nil, fmt.Errorf("解析 global state 失败：%w", err)
	}
	if state == nil {
		return nil, errors.New("解析 global state 失败：根值必须是 JSON 对象")
	}
	return state, nil
}

func normalizedGlobalState(state map[string]any, changes []sessionChange) map[string]any {
	next := map[string]any{}
	if projectless, exists := state["projectless-thread-ids"]; exists {
		next["projectless-thread-ids"] = projectless
	}
	historyRoots := providerSyncWorkspaceRoots(changes)
	if value, ok := state["electron-saved-workspace-roots"]; ok {
		next["electron-saved-workspace-roots"] = dedupePaths(append(pathArray(value), historyRoots...))
	} else if len(historyRoots) > 0 {
		next["electron-saved-workspace-roots"] = historyRoots
	}
	if value, ok := state["project-order"]; ok {
		next["project-order"] = dedupePaths(append(pathArray(value), historyRoots...))
	} else if len(historyRoots) > 0 {
		next["project-order"] = historyRoots
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
	hints := providerSyncWorkspaceHints(state["thread-workspace-root-hints"], changes)
	if len(hints) > 0 {
		next["thread-workspace-root-hints"] = hints
	}
	return next
}

func countGlobalStateUpdates(path string, changes []sessionChange) (int, error) {
	state, err := loadGlobalState(path)
	if err != nil {
		return 0, err
	}
	next := normalizedGlobalState(state, changes)
	count := 0
	for key, value := range next {
		if !jsonEqual(state[key], value) {
			count++
		}
	}
	return count, nil
}

func applyGlobalStateUpdate(path string, changes []sessionChange) (int, error) {
	state, err := loadGlobalState(path)
	if err != nil {
		return 0, err
	}
	next := normalizedGlobalState(state, changes)
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

func providerSyncWorkspaceRoots(changes []sessionChange) []string {
	seen := map[string]bool{}
	var roots []string
	for _, change := range changes {
		if change.ThreadID == "" || !change.HasUserEvent || !providerSyncShouldRestoreWorkspace(change.CWD) {
			continue
		}
		key := strings.ToLower(strings.TrimRight(strings.ReplaceAll(change.CWD, "/", `\`), `\`))
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, change.CWD)
	}
	return roots
}

func providerSyncShouldRestoreWorkspace(cwd string) bool {
	cwd = toDesktopWorkspacePath(cwd)
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	slash := filepath.ToSlash(cwd)
	lower := strings.ToLower(slash)
	if strings.Contains(lower, "/.codex/worktrees/") || strings.Contains(lower, "/documents/codex/") {
		return false
	}
	if base := strings.ToLower(filepath.Base(cwd)); base == "outputs" || strings.HasPrefix(base, "new-chat") {
		return false
	}
	return true
}

func providerSyncWorkspaceHints(existing any, changes []sessionChange) map[string]any {
	hints := mapFromAny(existing)
	for _, change := range changes {
		if change.ThreadID == "" || !change.HasUserEvent || !providerSyncShouldRestoreWorkspace(change.CWD) {
			continue
		}
		for _, id := range sessionIDVariants(change.ThreadID) {
			if strings.HasPrefix(id, "local:") {
				continue
			}
			hints[id] = change.CWD
		}
	}
	return hints
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
		if readJSON(filepath.Join(path, "metadata.json"), &meta) == nil && isProviderSyncBackupManager(stringFromAny(meta["managedBy"])) {
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

func isProviderSyncBackupManager(value string) bool {
	switch strings.TrimSpace(value) {
	case "ChatGPT Codex Tools provider sync", "Codex++ provider sync":
		return true
	default:
		return false
	}
}
