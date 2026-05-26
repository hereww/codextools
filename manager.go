package main

import (
	"bytes"
	"context"
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
			return exec.Command("open", "-a", app).Start()
		}
	}
	return exec.Command(companionBinaryPath(managerBinary)).Start()
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
	case "bind_official_auth":
		return s.bindOfficialAuth(args)
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
		"codex_app":           pathState(codexApp),
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

func (s *server) loadInstallGuideStatus(ctx context.Context) commandResult {
	settings := loadSettings()
	codexApp := resolveCodexApp(settings.CodexAppPath)
	ccsDBPath := defaultCCSDBPath()
	ccsDBPathCandidates := ccsDBPathCandidates()
	ccsProviders, ccsErr := listCCSProviders(ccsDBPath)
	download := latestCodexDownload(ctx, runtime.GOOS, runtime.GOARCH)
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
		"codexApp":                    codexPathState(codexApp),
		"codexVersion":                codexAppVersion(codexApp),
		"codexDetection":              codexDetectionPayload(settings.CodexAppPath, codexApp),
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
	}
	return ok(message, payload)
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
		if installed := resolveWindowsCodexFromInstalledApps(); installed != "" {
			return installed
		}
		if local := resolveWindowsCodexFromCommonPaths(); local != "" {
			return local
		}
		roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("LOCALAPPDATA"), `C:\Program Files\WindowsApps`}
		var matches []string
		for _, root := range roots {
			if root == "" {
				continue
			}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(strings.ToLower(entry.Name()), "openai.codex_") {
					app := filepath.Join(root, entry.Name(), "app")
					if isDir(app) {
						matches = append(matches, app)
					} else {
						matches = append(matches, filepath.Join(root, entry.Name()))
					}
				}
			}
		}
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[len(matches)-1]
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
		out, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
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
		addCandidate(filepath.Join(root, "Microsoft", "WindowsApps", "Codex.exe"))
		addCandidate(filepath.Join(root, "Microsoft", "WindowsApps", "codex.exe"))
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
	nested := filepath.Join(path, "app")
	if isDir(nested) && (fileExists(filepath.Join(nested, "Codex.exe")) || fileExists(filepath.Join(nested, "codex.exe"))) {
		return nested
	}
	if isDir(path) {
		return path
	}
	return ""
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
		return payload
	}
	if runtime.GOOS == "windows" {
		payload["message"] = "Windows 自动探测没有找到 Codex。若 Codex 已安装，请手动选择 Codex.exe 或安装目录。"
		payload["candidates"] = windowsCodexDetectionHints()
	}
	return payload
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
	appPath := stringArg(request, "appPath")
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
	if err := cmd.Start(); err != nil {
		return failed("启动静默入口失败："+err.Error(), map[string]any{"debugPort": debugPort, "helperPort": helperPort})
	}
	status := launchStatus{
		Status:      "accepted",
		Message:     "Go 管理器已启动静默入口。",
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
	}
	if appPath != "" {
		status.CodexApp = &appPath
	}
	_ = atomicWriteJSON(latestStatusPath(), status)
	message := "启动任务已在后台开始，可稍后查看概览状态。"
	if restart {
		message = "Codex 已请求重启，启动任务正在后台运行。"
	}
	return commandResult{"status": "accepted", "message": message, "debugPort": debugPort, "helperPort": helperPort}
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
	query := `SELECT id, name, settings_config
FROM providers
WHERE app_type = 'codex'
ORDER BY COALESCE(sort_index, 999999), created_at ASC, id ASC`
	out, err := sqliteQuery(path, query)
	if err != nil {
		return nil, err
	}
	var providers []ccsProviderImport
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		var config any
		if json.Unmarshal([]byte(parts[2]), &config) != nil {
			continue
		}
		if provider, ok := importFromCCSValue(parts[0], parts[1], config); ok {
			providers = append(providers, provider)
		}
	}
	return providers, nil
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
		valueAt(valueAt(config, "config"), "base_url"),
		valueAt(valueAt(config, "config"), "baseURL"),
		extractTomlString(stringFromAny(valueAt(config, "config")), "base_url"),
	), "/")
}

func extractCCSAPIKey(config any) string {
	return firstString(
		valuePointer(config, "env", "OPENAI_API_KEY"),
		valuePointer(config, "auth", "OPENAI_API_KEY"),
		valueAt(config, "apiKey"),
		valueAt(config, "api_key"),
		valueAt(valueAt(config, "config"), "apiKey"),
		valueAt(valueAt(config, "config"), "api_key"),
	)
}

func extractCCSProtocol(config any) string {
	apiFormat := firstString(valueAt(config, "api_format"), valueAt(config, "apiFormat"))
	wireAPI := extractTomlString(stringFromAny(valueAt(config, "config")), "wire_api")
	if isChatProtocol(apiFormat) || isChatProtocol(wireAPI) || strings.HasSuffix(strings.ToLower(extractCCSBaseURL(config)), "/chat/completions") {
		return "chatCompletions"
	}
	return "responses"
}

func extractCCSConfigContents(config any) string {
	return stringFromAny(valueAt(config, "config"))
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
		RelayMode: "pureApi", ConfigContents: provider.ConfigContents, AuthContents: provider.AuthContents,
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
