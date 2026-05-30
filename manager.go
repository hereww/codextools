package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runManager() error {
	root, _ := repoRoot()
	distFS, distLabel, err := managerDistFS(root)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	manager := &server{root: root, dist: distLabel, distFS: distFS}
	mux.HandleFunc("/api/commands/", manager.handleCommand)
	mux.HandleFunc("/api/dialog/open", manager.handleOpenDialog)
	mux.HandleFunc("/", manager.handleStatic)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	url := "http://" + listener.Addr().String()
	fmt.Printf("%s Go manager: %s\n", appName, url)
	if defaultManagerDesktop() {
		server := &http.Server{Handler: mux}
		serverErr := make(chan error, 1)
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				serverErr <- err
			}
			close(serverErr)
		}()
		if err := runManagerDesktopWindow(managerName, url); err != nil {
			return err
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err, ok := <-serverErr; ok {
			return err
		}
		return nil
	}
	_ = openURL(url)
	return http.Serve(listener, mux)
}

func openManagerApp() error {
	if runtime.GOOS == "darwin" {
		app := entrypointPath(true)
		if fileExists(app) {
			cmd := exec.Command("open", "-a", app)
			hideSubprocessWindow(cmd)
			return cmd.Start()
		}
	}
	cmd := exec.Command(companionBinaryPath(managerBinary))
	hideSubprocessWindow(cmd)
	return cmd.Start()
}

func (s *server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	command := strings.TrimPrefix(r.URL.Path, "/api/commands/")
	command, _ = urlPathUnescape(command)
	var args map[string]any
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, failed("请求参数 JSON 解析失败："+err.Error(), map[string]any{}))
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), commandTimeout(command))
	defer cancel()
	result := s.dispatch(ctx, command, args)
	writeJSON(w, result)
}

func commandTimeout(command string) time.Duration {
	if command == "install_update" {
		return 5 * time.Minute
	}
	return 45 * time.Second
}

func (s *server) handleOpenDialog(w http.ResponseWriter, r *http.Request) {
	var opts map[string]any
	_ = json.NewDecoder(r.Body).Decode(&opts)
	title := "选择路径"
	if value, ok := opts["title"].(string); ok && strings.TrimSpace(value) != "" {
		title = value
	}
	directory, _ := opts["directory"].(bool)
	selected := os.Getenv("CODEX_PLUS_SELECTED_PATH")
	if selected == "" {
		selected = strings.TrimSpace(promptPath(title, directory))
	}
	if selected == "" {
		writeJSON(w, nil)
		return
	}
	writeJSON(w, selected)
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	assetPath := strings.TrimPrefix(pathpkg.Clean("/"+r.URL.Path), "/")
	if assetPath == "" || assetPath == "." {
		s.serveIndex(w)
		return
	}
	info, err := fs.Stat(s.distFS, assetPath)
	if err != nil || info.IsDir() {
		s.serveIndex(w)
		return
	}
	http.FileServer(http.FS(s.distFS)).ServeHTTP(w, r)
}

func (s *server) serveIndex(w http.ResponseWriter) {
	index, err := fs.ReadFile(s.distFS, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	injected := bytes.Replace(index, []byte("<head>"), []byte(`<head><script>window.__CODEX_PLUS_GO_MANAGER__={apiBase:""};</script>`), 1)
	w.Header().Set("content-type", "text/html; charset=utf-8")
	_, _ = w.Write(injected)
}

func (s *server) dispatch(ctx context.Context, command string, args map[string]any) commandResult {
	switch command {
	case "backend_version":
		return ok("后端版本已读取。", map[string]any{"version": version})
	case "load_overview":
		return s.loadOverview()
	case "check_update":
		return s.checkUpdate(ctx)
	case "install_update":
		return s.installUpdate(ctx)
	case "load_install_guide_status":
		return s.loadInstallGuideStatus(ctx)
	case "launch_codex_plus":
		return s.launchCodex(args, false)
	case "restart_codex_plus":
		return s.launchCodex(args, true)
	case "load_settings":
		return settingsPayload("设置已加载。")
	case "save_settings":
		return s.saveSettings(args)
	case "load_ccs_providers":
		return s.loadCCSProviders()
	case "import_ccs_providers":
		return s.importCCSProviders()
	case "sync_providers_now":
		return s.syncProvidersNow()
	case "repair_codex_plugins":
		return s.repairCodexPlugins()
	case "repair_codex_goals":
		return s.repairCodexGoals()
	case "refresh_script_market":
		return s.refreshScriptMarket(ctx)
	case "install_market_script":
		return s.installMarketScript(ctx, stringArg(args, "id"))
	case "set_user_script_enabled":
		return s.setUserScriptEnabled(stringArg(args, "key"), boolArg(args, "enabled"))
	case "delete_user_script":
		return s.deleteUserScript(stringArg(args, "key"))
	case "open_external_url":
		return s.openExternalURL(stringArg(args, "url"))
	case "install_entrypoints", "repair_shortcuts":
		return s.installEntrypoints()
	case "uninstall_entrypoints":
		return s.uninstallEntrypoints(args)
	case "uninstall_codextools":
		return s.uninstallCodexTools(args)
	case "repair_codex_app":
		return s.repairCodexApp()
	case "repair_backend":
		return settingsPayload("后端已修复；Go 管理器当前复用设置文件，命令包装器仍由 Rust core 处理。")
	case "load_watcher_state":
		return ok("watcher 状态已加载。", watcherPayload())
	case "install_watcher":
		return s.installWatcher()
	case "uninstall_watcher":
		return s.uninstallWatcher()
	case "enable_watcher":
		return s.setWatcherDisabled(false)
	case "disable_watcher":
		return s.setWatcherDisabled(true)
	case "read_latest_logs":
		return s.readLatestLogs(args)
	case "copy_diagnostics":
		return ok("诊断报告已生成。", map[string]any{"report": s.diagnosticsReport()})
	case "reset_settings":
		if err := saveSettings(defaultSettings()); err != nil {
			return failed("重置设置失败："+err.Error(), settingsPayloadValue(defaultSettings()))
		}
		return settingsPayload("设置已重置为默认值。")
	case "relay_status":
		return s.relayStatus()
	case "read_relay_files":
		return s.readRelayFiles()
	case "save_relay_file":
		return s.saveRelayFile(args)
	case "import_current_relay_files":
		return s.importCurrentRelayFiles(args)
	case "bind_official_auth":
		return s.bindOfficialAuth(args)
	case "activate_official_auth":
		return s.activateOfficialAuth(args)
	case "unbind_official_auth":
		return s.unbindOfficialAuth(args)
	case "clear_current_official_auth":
		return s.clearCurrentOfficialAuth()
	case "test_relay_profile":
		return s.testRelayProfile(ctx, args)
	case "apply_relay_injection":
		return s.applyRelayInjection(false)
	case "apply_pure_api_injection":
		return s.applyRelayInjection(true)
	case "clear_relay_injection":
		return s.clearRelayInjection()
	default:
		return failed("未知命令："+command, map[string]any{})
	}
}

func ok(message string, payload map[string]any) commandResult {
	result := commandResult{"status": "ok", "message": message}
	for key, value := range payload {
		result[key] = value
	}
	return result
}

func failed(message string, payload map[string]any) commandResult {
	result := commandResult{"status": "failed", "message": message}
	for key, value := range payload {
		result[key] = value
	}
	return result
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func (s *server) loadOverview() commandResult {
	settings := loadSettings()
	codexApp := resolveCodexApp(settings.CodexAppPath)
	var latest *launchStatus
	_ = readJSON(latestStatusPath(), &latest)
	payload := map[string]any{
		"codex_app":           codexPathState(codexApp),
		"codex_version":       codexAppVersion(codexApp),
		"silent_shortcut":     shortcutState(entrypointPath(false)),
		"management_shortcut": shortcutState(entrypointPath(true)),
		"latest_launch":       latest,
		"current_version":     version,
		"update_status":       "not_checked",
		"settings_path":       settingsPath(),
		"logs_path":           diagnosticLogPath(),
	}
	return ok("概览已加载。", payload)
}

func (s *server) repairCodexApp() commandResult {
	settings := loadSettings()
	candidates := codexAppRepairCandidates(settings.CodexAppPath)
	if len(candidates) == 0 {
		return failed("未找到可启动的 Codex 程序。请确认 Microsoft Store 中的 Codex 已安装，或手动选择 Codex.exe / Codex 安装目录。", settingsPayloadValue(settings))
	}
	selected := candidates[0]
	settings.CodexAppPath = selected
	if err := saveSettings(settings); err != nil {
		return failed("修复 Codex 程序失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	payload := settingsPayloadValue(loadSettings())
	payload["codexApp"] = codexPathState(resolveCodexApp(selected))
	payload["repairCandidates"] = candidates
	return ok("已修复 Codex 程序路径："+selected, payload)
}

func codexAppRepairCandidates(saved string) []string {
	candidates := []string{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range candidates {
			if strings.EqualFold(existing, path) {
				return
			}
		}
		candidates = append(candidates, path)
	}
	if normalized := normalizeCodexAppPath(saved); normalized != "" {
		add(normalized)
	}
	if runtime.GOOS == "windows" {
		if local := resolveWindowsCodexFromCommonPaths(); local != "" {
			add(local)
		}
		if installed := resolveWindowsCodexFromInstalledApps(); installed != "" {
			add(installed)
		}
		if latest := findLatestWindowsCodexAppDirFromRoots(windowsAppPackageRoots()); latest != "" {
			add(latest)
		}
		if alias := windowsCodexExecutionAlias(); alias != "" && fileExists(alias) {
			add(alias)
		}
	}
	if installed := resolveCodexApp(""); installed != "" {
		add(installed)
	}
	return candidates
}

func (s *server) loadInstallGuideStatus(ctx context.Context) commandResult {
	settings := loadSettings()
	codexApp := resolveCodexApp(settings.CodexAppPath)
	ccsDBPath := defaultCCSDBPath()
	ccsDBPathCandidates := ccsDBPathCandidates()
	ccsProviders, ccsErr := listCCSProviders(ccsDBPath)
	download := latestCodexDownload(ctx, runtime.GOOS, runtime.GOARCH)
	relayStatus := relayStatusFromHome(codexHomeDir(), settings)
	message := "新手引导状态已读取。"
	var warnings []string
	if ccsErr != nil {
		warnings = append(warnings, "CCSwitch 数据库读取失败："+ccsErr.Error())
	}
	if runtime.GOOS == "windows" && stringFromAny(download["status"]) == "failed" {
		warnings = append(warnings, "Windows 安装包信息暂时获取失败，可稍后刷新")
	}
	if len(warnings) > 0 {
		message = "系统和本地安装状态已读取；" + strings.Join(warnings, "；") + "。"
	}
	payload := map[string]any{
		"platform":                    runtime.GOOS,
		"arch":                        runtime.GOARCH,
		"platformLabel":               platformDisplayName(runtime.GOOS),
		"archLabel":                   archDisplayName(runtime.GOARCH),
		"desktopRuntime":              desktopRuntimeName(),
		"desktopRuntimeStatus":        desktopRuntimeStatus(),
		"codexApp":                    codexPathState(codexApp),
		"codexVersion":                codexAppVersion(codexApp),
		"codexDetection":              codexDetectionPayload(settings.CodexAppPath, codexApp),
		"codexLaunch":                 codexLaunchPayload(codexApp),
		"codexInstallUrl":             codexInstallURL(download),
		"codexInstallSource":          codexInstallSource(download),
		"codexMirrorProjectUrl":       codexAppMirrorProjectURL,
		"codexMirrorLatestReleaseUrl": codexMirrorLatestReleaseURL(download),
		"codexLatestDownload":         download,
		"ccs": map[string]any{
			"installed":        fileExists(ccsDBPath),
			"dbPath":           ccsDBPath,
			"dbPathCandidates": ccsDBPathCandidates,
			"providerCount":    len(ccsProviders),
			"readError":        optionalErrorString(ccsErr),
		},
		"settingsPath": settingsPath(),
		"activeMode":   activeRelayProfile(settings).RelayMode,
		"relay":        relayStatus,
		"connection":   installGuideConnectionPayload(settings, relayStatus),
	}
	return ok(message, payload)
}

func platformDisplayName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return goos
	}
}

func archDisplayName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "ARM64"
	case "386":
		return "x86"
	default:
		return goarch
	}
}

func desktopRuntimeName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows WebView2 桌面窗口"
	case "darwin":
		return "macOS WebKit 桌面窗口"
	default:
		if defaultManagerDesktop() {
			return "桌面窗口"
		}
		return "浏览器模式"
	}
}

func desktopRuntimeStatus() string {
	if defaultManagerDesktop() {
		return "desktop"
	}
	return "browser"
}

func codexInstallURL(download map[string]any) string {
	if url := stringFromAny(download["downloadUrl"]); url != "" {
		return url
	}
	if runtime.GOOS == "darwin" {
		return codexOfficialInstallURL
	}
	return codexAppMirrorReleaseURL
}

func codexInstallSource(download map[string]any) string {
	if source := stringFromAny(download["source"]); source != "" {
		return source
	}
	if runtime.GOOS == "darwin" {
		return "official"
	}
	return "mirror"
}

func codexMirrorLatestReleaseURL(download map[string]any) string {
	if url := stringFromAny(download["releaseUrl"]); url != "" {
		return url
	}
	return codexAppMirrorReleaseURL
}

func latestCodexDownload(ctx context.Context, goos, goarch string) map[string]any {
	payload := map[string]any{
		"status":     "not_checked",
		"source":     "mirror",
		"projectUrl": codexAppMirrorProjectURL,
		"releaseUrl": codexAppMirrorReleaseURL,
	}
	if goos == "darwin" {
		payload["status"] = "available"
		payload["source"] = "official"
		payload["downloadUrl"] = codexOfficialInstallURL
		payload["message"] = "macOS 默认打开 Codex 官方安装页面。"
	}
	release, err := getJSON[codexAppMirrorRelease](ctx, codexAppMirrorAPIURL)
	if err != nil {
		payload["status"] = "failed"
		payload["message"] = "获取镜像最新版本失败：" + err.Error()
		return payload
	}
	payload["releaseName"] = release.Name
	payload["tagName"] = release.TagName
	payload["publishedAt"] = release.PublishedAt
	if release.HTMLURL != "" {
		payload["releaseUrl"] = release.HTMLURL
	}
	if goos == "darwin" {
		return payload
	}
	asset, ok := selectCodexMirrorAsset(release.Assets, goos, goarch)
	if !ok {
		payload["status"] = "missing"
		payload["message"] = "最新镜像版本没有找到当前系统对应安装包。"
		return payload
	}
	payload["status"] = "available"
	payload["source"] = "mirror"
	payload["assetName"] = asset.Name
	payload["downloadUrl"] = asset.BrowserDownloadURL
	payload["size"] = asset.Size
	payload["contentType"] = asset.ContentType
	payload["message"] = "已找到镜像项目最新对应系统安装包。"
	return payload
}

func selectCodexMirrorAsset(assets []codexAppMirrorAsset, goos, goarch string) (codexAppMirrorAsset, bool) {
	var candidates []codexAppMirrorAsset
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		url := strings.ToLower(asset.BrowserDownloadURL)
		value := name + " " + url
		if asset.BrowserDownloadURL == "" {
			continue
		}
		switch goos {
		case "windows":
			if strings.HasSuffix(name, ".msix") || strings.HasSuffix(name, ".appx") || strings.Contains(value, "windows") || strings.Contains(value, "win") {
				candidates = append(candidates, asset)
			}
		case "darwin":
			if strings.HasSuffix(name, ".dmg") && (strings.Contains(value, "mac") || strings.Contains(value, "darwin")) {
				candidates = append(candidates, asset)
			}
		}
	}
	if len(candidates) == 0 {
		return codexAppMirrorAsset{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return codexAssetScore(candidates[i].Name, goarch) > codexAssetScore(candidates[j].Name, goarch)
	})
	return candidates[0], true
}

func codexAssetScore(name, goarch string) int {
	lower := strings.ToLower(name)
	score := 0
	switch goarch {
	case "arm64":
		if strings.Contains(lower, "arm64") || strings.Contains(lower, "aarch64") {
			score += 20
		}
	case "amd64":
		if strings.Contains(lower, "x64") || strings.Contains(lower, "amd64") || strings.Contains(lower, "x86_64") {
			score += 20
		}
	}
	if strings.HasSuffix(lower, ".msix") || strings.HasSuffix(lower, ".dmg") {
		score += 10
	}
	if strings.Contains(lower, "sha256") || strings.Contains(lower, "manifest") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".json") {
		score -= 100
	}
	return score
}

func errorString(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func optionalErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func pathState(path string) map[string]any {
	if path == "" {
		return map[string]any{"status": "missing", "path": nil}
	}
	return map[string]any{"status": "found", "path": path}
}

func codexPathState(path string) map[string]any {
	state := pathState(path)
	if path != "" && runtime.GOOS == "windows" {
		state["executable"] = buildCodexExecutable(path)
		if appUserModelID := packagedWindowsAppUserModelID(path); appUserModelID != "" {
			state["appUserModelId"] = appUserModelID
		}
	}
	return state
}

func shortcutState(path string) map[string]any {
	if path == "" {
		return map[string]any{"status": "missing", "path": nil}
	}
	if !fileExists(path) {
		return map[string]any{"status": "missing", "path": path}
	}
	return map[string]any{"status": "installed", "path": path}
}

func resolveCodexApp(saved string) string {
	if normalized := normalizeCodexAppPath(saved); normalized != "" {
		return normalized
	}
	if runtime.GOOS == "darwin" {
		candidates := []string{"/Applications/Codex.app"}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "Applications", "Codex.app"))
		}
		for _, candidate := range candidates {
			if isDir(candidate) {
				return candidate
			}
		}
	}
	if runtime.GOOS == "windows" {
		if local := resolveWindowsCodexFromCommonPaths(); local != "" {
			return local
		}
		if installed := resolveWindowsCodexFromInstalledApps(); installed != "" {
			return installed
		}
		if latest := findLatestWindowsCodexAppDirFromRoots(windowsAppPackageRoots()); latest != "" {
			return latest
		}
	}
	return ""
}

func resolveWindowsCodexFromInstalledApps() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	commands := [][]string{
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue | Sort-Object Version | Select-Object -Last 1 -ExpandProperty InstallLocation`},
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq 'OpenAI.Codex' -or $_.PackageFullName -like 'OpenAI.Codex_*' } | Sort-Object Version | Select-Object -Last 1 -ExpandProperty InstallLocation`},
	}
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		hideSubprocessWindow(cmd)
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if normalized := normalizeCodexAppPath(line); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func resolveWindowsCodexFromCommonPaths() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	var candidates []string
	addCandidate := func(path string) {
		path = strings.TrimSpace(path)
		if path != "" {
			candidates = append(candidates, path)
		}
	}
	for _, key := range []string{"CODEX_APP_PATH", "CODEX_PATH", "CODEX_DESKTOP_PATH"} {
		addCandidate(os.Getenv(key))
	}
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if root == "" {
			continue
		}
		addCandidate(filepath.Join(root, "Programs", "Codex"))
		addCandidate(filepath.Join(root, "Codex"))
		addCandidate(filepath.Join(root, "OpenAI", "Codex"))
		addCandidate(filepath.Join(root, "OpenAI Codex"))
		for _, alias := range []string{filepath.Join(root, "Microsoft", "WindowsApps", "Codex.exe"), filepath.Join(root, "Microsoft", "WindowsApps", "codex.exe")} {
			if fileExists(alias) {
				addCandidate(alias)
			}
		}
	}
	for _, candidate := range candidates {
		if normalized := normalizeCodexAppPath(candidate); normalized != "" {
			return normalized
		}
	}
	return ""
}

func normalizeCodexAppPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		if normalized := normalizeWindowsPackageAppPath(path); normalized != "" {
			return normalized
		}
	}
	if runtime.GOOS == "windows" && isWindowsAppsExecutionAlias(path) {
		if fileExists(path) {
			return path
		}
		return ""
	}
	if strings.EqualFold(filepath.Base(path), "Codex.exe") || strings.EqualFold(filepath.Base(path), "codex.exe") {
		return filepath.Dir(path)
	}
	if strings.EqualFold(filepath.Ext(path), ".app") {
		return path
	}
	if fileExists(path) && !isDir(path) {
		return filepath.Dir(path)
	}
	if fileExists(filepath.Join(path, "Codex.exe")) || fileExists(filepath.Join(path, "codex.exe")) {
		return path
	}
	for _, subdir := range []string{"app", "VFS", filepath.Join("VFS", "ProgramFilesX64", "Codex"), filepath.Join("VFS", "ProgramFilesX64", "OpenAI", "Codex")} {
		candidate := filepath.Join(path, subdir)
		if fileExists(filepath.Join(candidate, "Codex.exe")) || fileExists(filepath.Join(candidate, "codex.exe")) {
			return candidate
		}
	}
	nested := filepath.Join(path, "app")
	if isDir(nested) && (fileExists(filepath.Join(nested, "Codex.exe")) || fileExists(filepath.Join(nested, "codex.exe"))) {
		return nested
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	if isDir(path) {
		return path
	}
	return ""
}

func isWindowsAppsExecutionAlias(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	base := filepath.Base(path)
	if !strings.EqualFold(base, "Codex.exe") && !strings.EqualFold(base, "codex.exe") {
		return false
	}
	dir := strings.ToLower(filepath.ToSlash(filepath.Dir(path)))
	return strings.Contains(dir, "/microsoft/windowsapps") || strings.HasSuffix(dir, "/windowsapps")
}

func isWindowsProtectedAppPackagePath(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	normalized := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(normalized, "/program files/windowsapps/openai.codex_") ||
		strings.HasPrefix(normalized, "c:/program files/windowsapps/openai.codex_")
}

func normalizeWindowsPackageAppPath(path string) string {
	packageName := windowsPackageNameFromPath(path)
	if !isWindowsCodexPackageName(packageName) {
		return ""
	}
	parts := splitPathParts(path)
	for i, part := range parts {
		if strings.EqualFold(part, packageName) {
			prefix := strings.Join(parts[:i+1], string(os.PathSeparator))
			if strings.Contains(path, `\`) {
				prefix = strings.Join(parts[:i+1], `\`)
			}
			if strings.HasSuffix(strings.ToLower(filepath.ToSlash(path)), "/app") || strings.EqualFold(filepath.Base(path), "Codex.exe") || strings.EqualFold(filepath.Base(path), "codex.exe") {
				return filepath.Join(prefix, "app")
			}
			return filepath.Join(prefix, "app")
		}
	}
	if strings.EqualFold(filepath.Base(path), "app") {
		return path
	}
	return filepath.Join(path, "app")
}

func windowsCodexExecutionAlias() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if alias := strings.TrimSpace(os.Getenv("CODEX_APP_EXECUTION_ALIAS")); alias != "" {
		return alias
	}
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		return filepath.Join(root, "Microsoft", "WindowsApps", "Codex.exe")
	}
	return ""
}

func windowsAppPackageRoots() []string {
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range roots {
			if strings.EqualFold(existing, path) {
				return
			}
		}
		roots = append(roots, path)
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if root != "" {
			add(filepath.Join(root, "WindowsApps"))
		}
	}
	add(`C:\Program Files\WindowsApps`)
	return roots
}

func findLatestWindowsCodexAppDirFromRoots(roots []string) string {
	var best string
	for _, root := range roots {
		if candidate := findLatestWindowsCodexAppDir(root); candidate != "" {
			if best == "" || compareVersions(windowsPackageVersionFromPath(candidate), windowsPackageVersionFromPath(best)) > 0 {
				best = candidate
			}
		}
	}
	return best
}

func findLatestWindowsCodexAppDir(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var best string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isWindowsCodexPackageName(name) || windowsPackageVersionFromName(name) == "" {
			continue
		}
		path := filepath.Join(root, name)
		if app := filepath.Join(path, "app"); isDir(app) {
			path = app
		}
		if best == "" || compareVersions(windowsPackageVersionFromPath(path), windowsPackageVersionFromPath(best)) > 0 {
			best = path
		}
	}
	return best
}

func packagedWindowsAppUserModelID(path string) string {
	packageName := windowsPackageNameFromPath(path)
	if !isWindowsCodexPackageName(packageName) {
		return ""
	}
	_, publisherID, ok := strings.Cut(packageName, "__")
	if !ok || publisherID == "" {
		return ""
	}
	return "OpenAI.Codex_" + publisherID + "!App"
}

func isWindowsCodexPackageName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(lower, "openai.codex_") && strings.Contains(name, "__")
}

func windowsPackageNameFromPath(path string) string {
	parts := splitPathParts(path)
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if strings.EqualFold(last, "Codex.exe") || strings.EqualFold(last, "codex.exe") {
		if len(parts) >= 3 && strings.EqualFold(parts[len(parts)-2], "app") {
			return parts[len(parts)-3]
		}
		if len(parts) < 2 {
			return ""
		}
		return parts[len(parts)-2]
	}
	if strings.EqualFold(last, "app") {
		if len(parts) < 2 {
			return ""
		}
		return parts[len(parts)-2]
	}
	return last
}

func windowsPackageVersionFromPath(path string) string {
	return windowsPackageVersionFromName(windowsPackageNameFromPath(path))
}

func windowsPackageVersionFromName(name string) string {
	if !isWindowsCodexPackageName(name) {
		return ""
	}
	rest := strings.TrimSpace(name)[len("OpenAI.Codex_"):]
	version, _, ok := strings.Cut(rest, "_")
	if !ok || version == "" {
		return ""
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return ""
		}
		if _, err := strconv.Atoi(part); err != nil {
			return ""
		}
	}
	return version
}

func splitPathParts(path string) []string {
	return strings.FieldsFunc(filepath.ToSlash(strings.TrimSpace(path)), func(r rune) bool {
		return r == '/' || r == '\\'
	})
}

func codexDetectionPayload(saved, resolved string) map[string]any {
	payload := map[string]any{
		"savedPath":    nullableString(saved),
		"resolvedPath": nullableString(resolved),
		"status":       "missing",
		"message":      "未检测到 Codex 应用。",
		"candidates":   []string{},
	}
	if resolved != "" {
		payload["status"] = "found"
		payload["message"] = "已检测到 Codex 应用。"
		payload["executable"] = buildCodexExecutable(resolved)
		if appUserModelID := packagedWindowsAppUserModelID(resolved); appUserModelID != "" {
			payload["appUserModelId"] = appUserModelID
		}
		return payload
	}
	if runtime.GOOS == "windows" {
		payload["message"] = "Windows 自动探测没有找到 Codex。若 Codex 已安装，请手动选择 Codex.exe 或安装目录。"
		payload["candidates"] = windowsCodexDetectionHints()
	}
	return payload
}

func codexLaunchPayload(appPath string) map[string]any {
	payload := map[string]any{
		"ready":          false,
		"method":         "missing",
		"methodLabel":    "未检测到启动方式",
		"path":           nullableString(appPath),
		"executable":     "",
		"appUserModelId": "",
		"message":        "未检测到 Codex 应用，无法启动。",
	}
	if appPath == "" {
		return payload
	}
	if runtime.GOOS == "windows" {
		if executable := buildCodexExecutable(appPath); strings.TrimSpace(executable) != "" && fileExists(executable) {
			payload["ready"] = true
			payload["method"] = "executable"
			payload["methodLabel"] = "可执行文件启动"
			payload["executable"] = executable
			payload["message"] = "将直接启动 Codex.exe 并附加调试端口参数。"
			return payload
		}
		if appUserModelID := packagedWindowsAppUserModelID(appPath); appUserModelID != "" {
			payload["ready"] = true
			payload["method"] = "packaged_activation"
			payload["methodLabel"] = "MSIX 应用激活"
			payload["appUserModelId"] = appUserModelID
			payload["executable"] = buildCodexExecutable(appPath)
			payload["message"] = "将通过 AppUserModelID 激活 Windows Store/MSIX 版。"
			return payload
		}
	}
	executable := buildCodexExecutable(appPath)
	if strings.TrimSpace(executable) == "" {
		payload["message"] = "已识别到 Codex 目录，但没有找到可执行文件。"
		return payload
	}
	if runtime.GOOS == "windows" && !isWindowsAppsExecutionAlias(executable) && !fileExists(executable) {
		payload["method"] = "executable_missing"
		payload["executable"] = executable
		payload["message"] = "已推断 Codex.exe 位置，但文件不存在。"
		return payload
	}
	payload["ready"] = true
	payload["method"] = "executable"
	payload["methodLabel"] = "可执行文件启动"
	payload["executable"] = executable
	payload["message"] = "将通过可执行文件启动 Codex。"
	return payload
}

func installGuideConnectionPayload(settings backendSettings, relayStatus map[string]any) map[string]any {
	active := activeRelayProfile(settings)
	mode := active.RelayMode
	if mode != "mixedApi" && mode != "pureApi" && mode != "official" {
		mode = "official"
	}
	payload := map[string]any{
		"ready":                false,
		"mode":                 mode,
		"profileId":            active.ID,
		"profileName":          active.Name,
		"message":              "",
		"officialReady":        boolFromAny(relayStatus["officialAuthenticated"]),
		"currentOfficialReady": boolFromAny(relayStatus["currentAuthenticated"]) || boolFromAny(relayStatus["authenticated"]),
		"boundOfficialReady":   boolFromAny(relayStatus["boundOfficialAuthenticated"]),
		"apiReady":             relayProfileAPIReady(active),
		"configured":           boolFromAny(relayStatus["configured"]),
		"accountLabel":         stringFromAny(relayStatus["officialAccountLabel"]),
		"boundProfileId":       stringFromAny(relayStatus["boundOfficialProfileId"]),
		"boundProfileName":     stringFromAny(relayStatus["boundOfficialProfileName"]),
	}
	switch mode {
	case "official":
		ready := relayProfileOfficialReady(active) || boolFromAny(relayStatus["officialAuthenticated"])
		payload["ready"] = ready
		payload["officialReady"] = ready
		if ready {
			payload["message"] = "官方账号已就绪，可切回官方登录。"
		} else {
			payload["message"] = "当前供应商还没有绑定官方账号，请先在连接服务里绑定。"
		}
	case "mixedApi":
		officialReady := relayProfileOfficialReady(active)
		apiReady := relayProfileAPIReady(active)
		payload["officialReady"] = officialReady || boolFromAny(relayStatus["officialAuthenticated"])
		payload["apiReady"] = apiReady
		payload["ready"] = officialReady && apiReady
		if officialReady && apiReady {
			payload["message"] = "官方账号和混合 API 均已就绪。"
		} else if !officialReady && !apiReady {
			payload["message"] = "当前供应商缺少官方账号绑定和 Base URL / Key。"
		} else if !officialReady {
			payload["message"] = "当前供应商还没有绑定官方账号。"
		} else {
			payload["message"] = "当前供应商缺少 Base URL / Key。"
		}
	case "pureApi":
		apiReady := relayProfileAPIReady(active)
		payload["apiReady"] = apiReady
		payload["ready"] = apiReady
		if apiReady {
			payload["message"] = "中转 API 参数已就绪。"
		} else {
			payload["message"] = "当前中转供应商缺少 Base URL / Key。"
		}
	default:
		payload["message"] = "未知连接模式。"
	}
	if payload["profileName"] == "" {
		payload["profileName"] = active.ID
	}
	return payload
}

func relayProfileOfficialReady(profile relayProfile) bool {
	status, ok := relayProfileOfficialAuthStatus(profile)
	return ok && status.Authenticated
}

func relayProfileAPIReady(profile relayProfile) bool {
	if strings.TrimSpace(profile.BaseURL) == "" || strings.TrimSpace(profile.APIKey) == "" {
		return false
	}
	if profile.ImageGenerationEnabled && profile.ImageGenerationUseSeparateAPI {
		return strings.TrimSpace(profile.ImageGenerationBaseURL) != "" && strings.TrimSpace(profile.ImageGenerationAPIKey) != ""
	}
	return true
}

func windowsCodexDetectionHints() []string {
	if runtime.GOOS != "windows" {
		return []string{}
	}
	var hints []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		hints = append(hints,
			filepath.Join(local, "Programs", "Codex"),
			filepath.Join(local, "Microsoft", "WindowsApps", "Codex.exe"),
		)
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		hints = append(hints,
			filepath.Join(programFiles, "WindowsApps", "OpenAI.Codex_*"),
			filepath.Join(programFiles, "OpenAI", "Codex"),
		)
	}
	hints = append(hints, "Get-AppxPackage OpenAI.Codex")
	return hints
}

func codexAppVersion(path string) *string {
	if path == "" {
		return nil
	}
	if runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(path), ".app") {
		data, err := os.ReadFile(filepath.Join(path, "Contents", "Info.plist"))
		if err != nil {
			return nil
		}
		text := string(data)
		for _, key := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
			if value := plistStringAfterKey(text, key); value != "" {
				return &value
			}
		}
		return nil
	}
	parts := strings.FieldsFunc(filepath.ToSlash(path), func(r rune) bool { return r == '/' || r == '\\' })
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.ToLower(parts[i]), "openai.codex_") {
			fields := strings.Split(parts[i], "_")
			if len(fields) > 1 {
				version := fields[1]
				return &version
			}
		}
	}
	return nil
}

func plistStringAfterKey(text, key string) string {
	idx := strings.Index(text, "<key>"+key+"</key>")
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(rest[start+len("<string>") : end])
}

func (s *server) launchCodex(args map[string]any, restart bool) commandResult {
	request := mapArg(args, "request")
	appPath := normalizeCodexAppPath(stringArg(request, "appPath"))
	debugPort := uint16Arg(request, "debugPort", 9229)
	helperPort := uint16Arg(request, "helperPort", 57321)
	launcher := companionBinaryPath(silentBinary)
	if runtime.GOOS == "windows" {
		launcher += ".exe"
	}
	if !fileExists(launcher) {
		return failed("启动静默入口失败：未找到 "+launcher, map[string]any{"debugPort": debugPort, "helperPort": helperPort})
	}
	cmd := exec.Command(launcher, "--launcher", "--debug-port", strconv.Itoa(int(debugPort)), "--helper-port", strconv.Itoa(int(helperPort)))
	if appPath != "" {
		cmd.Args = append(cmd.Args, "--app-path", appPath)
	}
	if restart {
		cmd.Args = append(cmd.Args, "--restart")
	}
	hideSubprocessWindow(cmd)
	if err := cmd.Start(); err != nil {
		return failed("启动静默入口失败："+err.Error(), map[string]any{"debugPort": debugPort, "helperPort": helperPort})
	}
	latest := waitForLaunchStatusAfter(time.Now().Add(-200*time.Millisecond), 2*time.Second)
	if latest != nil && latest.Status == "failed" {
		return failed(latest.Message, map[string]any{"debugPort": debugPort, "helperPort": helperPort, "latest_launch": latest})
	}
	if latest == nil {
		accepted := launchStatus{
			Status:      "accepted",
			Message:     "Go 管理器已启动静默入口。",
			StartedAtMS: uint64(time.Now().UnixMilli()),
			DebugPort:   &debugPort,
			HelperPort:  &helperPort,
		}
		if appPath != "" {
			accepted.CodexApp = &appPath
		}
		_ = atomicWriteJSON(latestStatusPath(), accepted)
		latest = &accepted
	}
	message := "启动任务已在后台开始，可稍后查看概览状态。"
	if restart {
		message = "Codex 已请求重启，启动任务正在后台运行。"
	}
	return commandResult{"status": "accepted", "message": message, "debugPort": debugPort, "helperPort": helperPort, "latest_launch": latest}
}

func waitForLaunchStatusAfter(after time.Time, timeout time.Duration) *launchStatus {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status launchStatus
		if readJSON(latestStatusPath(), &status) == nil && status.StartedAtMS > 0 {
			started := time.UnixMilli(int64(status.StartedAtMS))
			if !started.Before(after) {
				return &status
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	return nil
}

func companionBinaryPath(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "..", name),
		filepath.Join(dir, "..", "..", name),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return filepath.Join(dir, name)
}

func (s *server) loadCCSProviders() commandResult {
	dbPath := defaultCCSDBPath()
	candidates := ccsDBPathCandidates()
	providers, err := listCCSProviders(dbPath)
	if err != nil {
		return failed("读取 CCS 供应商失败："+err.Error(), map[string]any{"dbPath": dbPath, "dbPathCandidates": candidates, "providers": []ccsProviderImport{}})
	}
	return ok(fmt.Sprintf("已读取 CCS Codex 供应商：%d 个。", len(providers)), map[string]any{"dbPath": dbPath, "dbPathCandidates": candidates, "providers": providers})
}

func (s *server) importCCSProviders() commandResult {
	providers, err := listCCSProviders(defaultCCSDBPath())
	if err != nil {
		return failed("读取 CCS 供应商失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	settings := loadSettings()
	existingKeys := map[string]bool{}
	existingIDs := map[string]bool{}
	for _, profile := range settings.RelayProfiles {
		existingKeys[ccsImportKey(profile.Name, profile.BaseURL)] = true
		existingIDs[profile.ID] = true
	}
	imported := 0
	for _, provider := range providers {
		key := ccsImportKey(provider.Name, provider.BaseURL)
		if existingKeys[key] {
			continue
		}
		settings.RelayProfiles = append(settings.RelayProfiles, relayProfileFromCCS(provider, existingIDs))
		existingKeys[key] = true
		imported++
	}
	if imported == 0 {
		return settingsPayload("没有新的 CCSwitch 供应商需要导入。")
	}
	if err := saveSettings(settings); err != nil {
		return failed("保存 CCS 供应商失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	return settingsPayload(fmt.Sprintf("已导入 CCSwitch 供应商：%d 个。", imported))
}

func defaultCCSDBPath() string {
	candidates := ccsDBPathCandidates()
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(".cc-switch", "cc-switch.db")
}

func ccsDBPathCandidates() []string {
	home, _ := os.UserHomeDir()
	candidates := []string{}
	addPath := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		for _, existing := range candidates {
			if strings.EqualFold(existing, path) {
				return
			}
		}
		candidates = append(candidates, path)
	}
	addDir := func(parts ...string) {
		dir := filepath.Join(parts...)
		for _, name := range []string{"cc-switch.db", "database.db", "ccswitch.db", "cc-switch.sqlite", "ccswitch.sqlite"} {
			addPath(filepath.Join(dir, name))
		}
	}
	if home != "" {
		addDir(home, ".cc-switch")
		addDir(home, ".config", "cc-switch")
		addDir(home, "AppData", "Roaming", "cc-switch")
		addDir(home, "AppData", "Roaming", "CCSwitch")
		addDir(home, "AppData", "Local", "cc-switch")
		addDir(home, "AppData", "Local", "CCSwitch")
		addDir(home, "AppData", "Local", "com.cc-switch.app")
	}
	for _, root := range []string{os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		addDir(root, "cc-switch")
		addDir(root, "CCSwitch")
		addDir(root, "com.cc-switch.app")
		addDir(root, "ccswitch")
	}
	return candidates
}

func listCCSProviders(path string) ([]ccsProviderImport, error) {
	if !fileExists(path) {
		return []ccsProviderImport{}, nil
	}
	idColumn := ccsFirstColumn(path, "id", "provider_id", "providerId")
	nameColumn := ccsFirstColumn(path, "name", "display_name", "displayName")
	configColumn := ccsFirstColumn(path, "settings_config", "settingsConfig", "config")
	metaColumn := ccsFirstColumn(path, "meta")
	if idColumn == "" || nameColumn == "" || configColumn == "" {
		return nil, fmt.Errorf("CCSwitch providers 表缺少必要列 id/name/settings_config")
	}
	query := fmt.Sprintf("SELECT %s, %s, %s FROM providers", quoteSQLiteIdentifier(idColumn), quoteSQLiteIdentifier(nameColumn), quoteSQLiteIdentifier(configColumn))
	if metaColumn != "" {
		query = fmt.Sprintf("SELECT %s, %s, %s, %s FROM providers", quoteSQLiteIdentifier(idColumn), quoteSQLiteIdentifier(nameColumn), quoteSQLiteIdentifier(configColumn), quoteSQLiteIdentifier(metaColumn))
	}
	if sqliteHasColumn(path, "providers", "app_type") {
		query += " WHERE lower(app_type) = 'codex'"
	}
	var orderParts []string
	if sqliteHasColumn(path, "providers", "sort_index") {
		orderParts = append(orderParts, "COALESCE(sort_index, 999999)")
	}
	if sqliteHasColumn(path, "providers", "created_at") {
		orderParts = append(orderParts, "created_at ASC")
	}
	orderParts = append(orderParts, quoteSQLiteIdentifier(idColumn)+" ASC")
	query += " ORDER BY " + strings.Join(orderParts, ", ")
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []ccsProviderImport
	for rows.Next() {
		var id, name, rawConfig, rawMeta sql.NullString
		if metaColumn != "" {
			if err := rows.Scan(&id, &name, &rawConfig, &rawMeta); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&id, &name, &rawConfig); err != nil {
				return nil, err
			}
		}
		config, ok := decodeCCSSettingsConfig(rawConfig.String)
		if !ok {
			continue
		}
		if meta, ok := decodeCCSSettingsConfig(rawMeta.String); ok {
			config = attachCCSMeta(config, meta)
		}
		if provider, ok := importFromCCSValue(id.String, name.String, config); ok {
			providers = append(providers, provider)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return providers, nil
}

func ccsFirstColumn(path string, names ...string) string {
	for _, name := range names {
		if sqliteHasColumn(path, "providers", name) {
			return name
		}
	}
	return ""
}

func decodeCCSSettingsConfig(raw string) (any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var config any
	if err := json.Unmarshal([]byte(raw), &config); err == nil {
		return config, true
	}
	return map[string]any{"config": raw}, true
}

func attachCCSMeta(config any, meta any) any {
	metaMap, ok := meta.(map[string]any)
	if !ok || len(metaMap) == 0 {
		return config
	}
	configMap, ok := config.(map[string]any)
	if !ok {
		return map[string]any{"config": config, "meta": metaMap}
	}
	merged := make(map[string]any, len(configMap)+2)
	for key, value := range configMap {
		merged[key] = value
	}
	merged["meta"] = metaMap
	if _, ok := merged["apiFormat"]; !ok {
		if apiFormat := valueAt(metaMap, "apiFormat"); apiFormat != nil {
			merged["apiFormat"] = apiFormat
		}
	}
	if _, ok := merged["api_format"]; !ok {
		if apiFormat := valueAt(metaMap, "api_format"); apiFormat != nil {
			merged["api_format"] = apiFormat
		}
	}
	return merged
}

func importFromCCSValue(sourceID, name string, config any) (ccsProviderImport, bool) {
	baseURL := extractCCSBaseURL(config)
	if baseURL == "" {
		return ccsProviderImport{}, false
	}
	apiKey := extractCCSAPIKey(config)
	protocol := extractCCSProtocol(config)
	configContents := extractCCSConfigContents(config)
	if strings.TrimSpace(configContents) == "" {
		configContents = buildCCSConfigToml(baseURL, apiKey, protocol)
	} else {
		configContents = ensureConfigBearerToken(configContents, apiKey)
	}
	authContents := extractCCSAuthContents(config)
	if strings.TrimSpace(authContents) == "" {
		authContents = buildCCSAuthJSON(apiKey)
	}
	return ccsProviderImport{SourceID: sourceID, Name: name, BaseURL: baseURL, APIKey: apiKey, Protocol: protocol, ConfigContents: configContents, AuthContents: authContents}, true
}

func extractCCSBaseURL(config any) string {
	return strings.TrimRight(firstString(
		valueAt(config, "base_url"),
		valueAt(config, "baseURL"),
		valueAt(config, "apiEndpoint"),
		valueAt(valueAt(config, "config"), "base_url"),
		valueAt(valueAt(config, "config"), "baseURL"),
		extractTomlString(extractCCSConfigText(config), "base_url"),
	), "/")
}

func extractCCSAPIKey(config any) string {
	return firstString(
		valuePointer(config, "env", "OPENAI_API_KEY"),
		valuePointer(config, "auth", "OPENAI_API_KEY"),
		extractCCSAuthJSONKey(config),
		valueAt(config, "apiKey"),
		valueAt(config, "api_key"),
		valueAt(valueAt(config, "config"), "apiKey"),
		valueAt(valueAt(config, "config"), "api_key"),
		extractTomlString(extractCCSConfigText(config), "experimental_bearer_token"),
	)
}

func extractCCSProtocol(config any) string {
	apiFormat := firstString(
		valueAt(config, "api_format"),
		valueAt(config, "apiFormat"),
		valuePointer(config, "meta", "api_format"),
		valuePointer(config, "meta", "apiFormat"),
	)
	wireAPI := extractTomlString(extractCCSConfigText(config), "wire_api")
	if isChatProtocol(apiFormat) || isChatProtocol(wireAPI) || strings.HasSuffix(strings.ToLower(extractCCSBaseURL(config)), "/chat/completions") {
		return "chatCompletions"
	}
	return "responses"
}

func extractCCSConfigContents(config any) string {
	return extractCCSConfigText(config)
}

func extractCCSAuthContents(config any) string {
	auth := valueAt(config, "auth")
	if auth == nil {
		return ""
	}
	if _, ok := auth.(map[string]any); ok {
		data, _ := json.MarshalIndent(auth, "", "  ")
		return string(data) + "\n"
	}
	return stringFromAny(auth)
}

func extractCCSConfigText(config any) string {
	if text := stringFromAny(valueAt(config, "config")); strings.TrimSpace(text) != "" {
		return text
	}
	text, _ := config.(string)
	return text
}

func extractCCSAuthJSONKey(config any) string {
	authText := strings.TrimSpace(stringFromAny(valueAt(config, "auth")))
	if authText == "" {
		return ""
	}
	var auth map[string]any
	if json.Unmarshal([]byte(authText), &auth) != nil {
		return ""
	}
	return stringFromAny(auth["OPENAI_API_KEY"])
}

func ensureConfigBearerToken(configText, apiKey string) string {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(extractTomlString(configText, "experimental_bearer_token")) != "" {
		return configText
	}
	tokenLine := `experimental_bearer_token = "` + tomlEscape(apiKey) + `"`
	lines := strings.Split(configText, "\n")
	insertAt := -1
	if provider := extractTomlString(configText, "model_provider"); provider != "" {
		insertAt = ccsProviderTableInsertIndex(lines, provider)
	}
	if insertAt < 0 {
		insertAt = ccsFirstModelProviderTableInsertIndex(lines)
	}
	if insertAt >= 0 {
		lines = append(lines[:insertAt], append([]string{tokenLine}, lines[insertAt:]...)...)
		return strings.Join(lines, "\n")
	}
	return strings.TrimRight(configText, "\r\n") + "\n" + tokenLine + "\n"
}

func ccsProviderTableInsertIndex(lines []string, provider string) int {
	quoted := `[model_providers."` + strings.ReplaceAll(provider, `"`, `\"`) + `"]`
	unquoted := "[model_providers." + provider + "]"
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == unquoted || trimmed == quoted {
			return ccsTableEndIndex(lines, index)
		}
	}
	return -1
}

func ccsFirstModelProviderTableInsertIndex(lines []string) int {
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[model_providers.") {
			return ccsTableEndIndex(lines, index)
		}
	}
	return -1
}

func ccsTableEndIndex(lines []string, tableStart int) int {
	for index := tableStart + 1; index < len(lines); index++ {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "[") {
			return index
		}
	}
	return len(lines)
}

func buildCCSConfigToml(baseURL, apiKey, protocol string) string {
	wireAPI := "responses"
	if protocol == "chatCompletions" {
		wireAPI = "chat"
	}
	return strings.Join([]string{
		`model_provider = "CodexPlusPlus"`,
		"",
		`[model_providers.CodexPlusPlus]`,
		`name = "CodexPlusPlus"`,
		`wire_api = "` + wireAPI + `"`,
		`requires_openai_auth = true`,
		`base_url = "` + tomlEscape(baseURL) + `"`,
		`experimental_bearer_token = "` + tomlEscape(apiKey) + `"`,
		"",
	}, "\n")
}

func buildCCSAuthJSON(apiKey string) string {
	data, _ := json.MarshalIndent(map[string]string{"OPENAI_API_KEY": apiKey}, "", "  ")
	return string(data) + "\n"
}

func relayProfileFromCCS(provider ccsProviderImport, existingIDs map[string]bool) relayProfile {
	id := uniqueProfileID("ccs-"+sanitizeID(provider.SourceID), existingIDs)
	existingIDs[id] = true
	return relayProfile{
		ID: id, Name: provider.Name, BaseURL: provider.BaseURL, APIKey: provider.APIKey, Protocol: provider.Protocol,
		RelayMode: "pureApi", ConfigContents: provider.ConfigContents, AuthContents: "",
	}
}

func ccsImportKey(name, baseURL string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "\n" + strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
}

func sanitizeID(value string) string {
	var out strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			out.WriteByte(byte(strings.ToLower(string(ch))[0]))
		} else if out.Len() > 0 && !strings.HasSuffix(out.String(), "-") {
			out.WriteByte('-')
		}
	}
	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "provider"
	}
	return result
}

func uniqueProfileID(base string, existingIDs map[string]bool) string {
	if !existingIDs[base] {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if !existingIDs[candidate] {
			return candidate
		}
	}
}

func isChatProtocol(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "chat_completions", "chat-completions", "openai_chat", "openai-chat":
		return true
	default:
		return false
	}
}

func extractTomlString(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		_, rest, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if len(rest) < 2 {
			continue
		}
		quote := rest[0]
		if quote != '"' && quote != '\'' {
			continue
		}
		rest = rest[1:]
		if index := strings.IndexByte(rest, quote); index >= 0 {
			return rest[:index]
		}
	}
	return ""
}

func valueAt(value any, key string) any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return object[key]
}

func valuePointer(value any, path ...string) any {
	current := value
	for _, key := range path {
		current = valueAt(current, key)
		if current == nil {
			return nil
		}
	}
	return current
}
