package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

const openAIDesktopPublisherID = "2p2nqsd0c76g0"

var (
	windowsPackagedAUMIDCache      sync.Map
	windowsPackagedLaunchInfoCache sync.Map
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
		windowErr := runManagerDesktopWindow(managerName, url)
		repairShutdownCtx, repairShutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := manager.shutdownConversationHistoryRepair(repairShutdownCtx); err != nil {
			appendDiagnosticLog("manager.conversation_history_repair_shutdown_timeout", map[string]any{"error": err.Error()})
		}
		repairShutdownCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err, ok := <-serverErr; ok {
			return err
		}
		if windowErr != nil {
			return windowErr
		}
		return nil
	}
	_ = openURL(url)
	return http.Serve(listener, mux)
}

func openManagerApp() error {
	if runtime.GOOS == "darwin" {
		app := preferredMacOSManagerAppPath()
		if fileExists(app) {
			cmd := exec.Command("open", app)
			hideSubprocessWindow(cmd)
			return cmd.Start()
		}
	}
	cmd := exec.Command(companionBinaryPath(managerBinary))
	hideSubprocessWindow(cmd)
	return cmd.Start()
}

var openManagerAppFunc = openManagerApp

func openTransientManagerApp() error {
	if runtime.GOOS == "darwin" {
		app := preferredMacOSManagerAppPath()
		if fileExists(app) {
			cmd := exec.Command("open", "-n", app, "--args", "--transient")
			hideSubprocessWindow(cmd)
			return cmd.Start()
		}
	}
	cmd := exec.Command(companionBinaryPath(managerBinary), "--transient")
	hideSubprocessWindow(cmd)
	return cmd.Start()
}

var openTransientManagerAppFunc = openTransientManagerApp

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
	switch command {
	case "install_update", "sync_providers_now", "apply_session_index_cleanup", "apply_relay_injection", "apply_pure_api_injection", "clear_relay_injection":
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
	case "import_dream_skin_image":
		return s.importDreamSkinImage(ctx, args)
	case "check_env_conflicts":
		return s.checkEnvConflicts()
	case "remove_env_conflicts":
		return s.removeEnvConflicts(args)
	case "load_ccs_providers":
		return s.loadCCSProviders()
	case "import_ccs_providers":
		return s.importCCSProviders()
	case "load_pending_provider_import":
		return s.loadPendingProviderImport()
	case "save_pending_provider_import":
		return s.savePendingProviderImport(args)
	case "confirm_pending_provider_import":
		return s.confirmPendingProviderImport()
	case "dismiss_pending_provider_import":
		return s.dismissPendingProviderImport()
	case "sync_providers_now":
		return s.syncProvidersNow(args)
	case "load_provider_sync_targets":
		return s.loadProviderSyncTargets()
	case "list_local_sessions":
		return s.listLocalSessions(args)
	case "delete_local_session":
		return s.deleteLocalSession(args)
	case "preview_session_index_cleanup":
		return s.previewSessionIndexCleanup()
	case "apply_session_index_cleanup":
		return s.applySessionIndexCleanup(args)
	case "test_stepwise_settings":
		return s.testStepwiseSettings(args)
	case "test_vlm_profile":
		return s.testVLMProfile(ctx, args)
	case "repair_conversation_history":
		return s.repairConversationHistory()
	case "conversation_history_repair_status":
		return s.conversationHistoryRepairStatus(args)
	case "cancel_conversation_history_repair":
		return s.cancelConversationHistoryRepair(args)
	case "repair_codex_plugins":
		return s.repairCodexPlugins()
	case "plugin_marketplace_status":
		return s.pluginMarketplaceStatus()
	case "repair_plugin_marketplace":
		return s.repairPluginMarketplace(ctx)
	case "remote_plugin_marketplace_status":
		return s.remotePluginMarketplaceStatus()
	case "repair_remote_plugin_marketplace":
		return s.repairRemotePluginMarketplace()
	case "repair_codex_goals":
		return s.repairCodexGoals()
	case "load_computer_use_status":
		return s.loadComputerUseStatus()
	case "repair_computer_use":
		return s.repairComputerUse()
	case "zed_remote_projects":
		return s.zedRemoteProjects(args)
	case "zed_remote_open":
		return s.zedRemoteOpen(args)
	case "zed_remote_forget_project":
		return s.zedRemoteForgetProject(args)
	case "list_skill_mcp_backups":
		return s.listSkillMCPBackups()
	case "create_skill_mcp_backup":
		return s.createSkillMCPBackup(args)
	case "restore_skill_mcp_backup":
		return s.restoreSkillMCPBackup(args)
	case "delete_skill_mcp_backup":
		return s.deleteSkillMCPBackup(args)
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
	case "read_live_context_entries":
		return s.readLiveContextEntries()
	case "sync_live_context_entries":
		return s.syncLiveContextEntries(args)
	case "upsert_context_entry":
		return s.upsertContextEntry(args)
	case "delete_context_entry":
		return s.deleteContextEntry(args)
	case "extract_relay_common_config":
		return s.extractRelayCommonConfig(args)
	case "fetch_relay_profile_models":
		return s.fetchRelayProfileModels(ctx, args)
	case "fetch_sub2api_billing":
		return s.fetchSub2APIBilling(ctx, args)
	case "diagnose_relay_profile":
		return s.diagnoseRelayProfile(ctx, args)
	case "install_entrypoints", "repair_shortcuts":
		return s.installEntrypoints()
	case "uninstall_entrypoints":
		return s.uninstallEntrypoints(args)
	case "uninstall_codextools":
		return s.uninstallCodexTools(args)
	case "repair_codex_app":
		return s.repairCodexApp(ctx)
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
	case "backfill_relay_profile_from_live":
		return s.backfillRelayProfileFromLive(args)
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

func warning(message string, payload map[string]any) commandResult {
	result := commandResult{"status": "not_checked", "message": message}
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

func (s *server) repairCodexApp(ctx context.Context) commandResult {
	settings := loadSettings()
	candidates := codexAppRepairCandidates(settings.CodexAppPath)
	download := latestCodexDownload(ctx, runtime.GOOS, runtime.GOARCH)
	if len(candidates) == 0 {
		payload := settingsPayloadValue(settings)
		attachChatGPTInstallPayload(payload, candidates, download)
		if runtime.GOOS == "windows" {
			return failed("未找到 Codex 与 ChatGPT 合并后的 Windows 新版应用。请先从 Microsoft Store 更新/安装 ChatGPT；也可选择 WindowsApps 外的 ChatGPT.exe 或 Codex.exe。", payload)
		}
		return failed("未找到可直接启动的 ChatGPT 桌面应用。请安装 ChatGPT 桌面应用，或手动选择 ChatGPT.app / ChatGPT.exe。", payload)
	}
	selected := candidates[0]
	settings.CodexAppPath = selected
	if err := saveSettings(settings); err != nil {
		return failed("修复 ChatGPT 应用路径失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	payload := settingsPayloadValue(loadSettings())
	payload["codexApp"] = codexPathState(resolveCodexApp(selected))
	payload["repairCandidates"] = candidates
	attachChatGPTInstallPayload(payload, candidates, download)
	return ok("已修复 ChatGPT 应用路径："+selected, payload)
}

func attachChatGPTInstallPayload(payload map[string]any, candidates []string, download map[string]any) {
	payload["codexInstallUrl"] = codexOfficialInstallURL
	payload["repairCandidates"] = candidates
	payload["codexLatestDownload"] = download
}

func codexAppRepairCandidates(saved string) []string {
	candidates := []string{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if runtime.GOOS == "windows" && !windowsCodexPathSupportsLaunch(path) {
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
		if installed := resolveWindowsCodexFromInstalledApps(); installed != "" {
			add(installed)
		}
		if local := resolveWindowsCodexFromCommonPaths(); local != "" {
			add(local)
		}
	}
	if installed := resolveCodexApp(""); installed != "" {
		add(installed)
	}
	return candidates
}

func resolveLaunchableCodexApp(saved string) string {
	if runtime.GOOS != "windows" {
		return resolveCodexApp(saved)
	}
	candidates := codexAppRepairCandidates(saved)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
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
	if len(warnings) > 0 {
		message = "系统和本地安装状态已读取；" + strings.Join(warnings, "；") + "。"
	}
	onboardingCompletedForCurrentPlatform := settings.OnboardingCompleted && strings.EqualFold(settings.OnboardingCompletedPlatform, runtime.GOOS)
	onboardingPlatformMismatch := settings.OnboardingCompleted && settings.OnboardingCompletedPlatform != "" && !strings.EqualFold(settings.OnboardingCompletedPlatform, runtime.GOOS)
	payload := map[string]any{
		"platform":                              runtime.GOOS,
		"arch":                                  runtime.GOARCH,
		"platformLabel":                         platformDisplayName(runtime.GOOS),
		"archLabel":                             archDisplayName(runtime.GOARCH),
		"desktopRuntime":                        desktopRuntimeName(),
		"desktopRuntimeStatus":                  desktopRuntimeStatus(),
		"platformGuide":                         installGuidePlatformGuide(runtime.GOOS),
		"onboardingCompleted":                   settings.OnboardingCompleted,
		"onboardingCompletedAt":                 settings.OnboardingCompletedAt,
		"onboardingCompletedPlatform":           settings.OnboardingCompletedPlatform,
		"onboardingCompletedForCurrentPlatform": onboardingCompletedForCurrentPlatform,
		"onboardingPlatformMismatch":            onboardingPlatformMismatch,
		"codexApp":                              codexPathState(codexApp),
		"codexVersion":                          codexAppVersion(codexApp),
		"codexDetection":                        codexDetectionPayload(settings.CodexAppPath, codexApp),
		"codexLaunch":                           codexLaunchPayload(codexApp),
		"codexInstallUrl":                       codexInstallURL(download),
		"codexInstallSource":                    codexInstallSource(download),
		"codexLatestDownload":                   download,
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

func installGuidePlatformGuide(goos string) map[string]any {
	guide := map[string]any{
		"platform":                  goos,
		"platformLabel":             platformDisplayName(goos),
		"title":                     platformDisplayName(goos) + " 新手引导",
		"systemDescription":         "安装包、路径检测和桌面运行方式会根据当前系统自动切换。",
		"desktopRuntime":            desktopRuntimeNameFor(goos),
		"desktopRuntimeDescription": "当前系统使用桌面窗口运行；未启用桌面窗口时会回退到浏览器模式。",
		"installTitle":              "安装 ChatGPT",
		"installActionLabel":        "打开安装入口",
		"installSourceLabel":        "安装入口",
		"installDescription":        "按当前系统打开 ChatGPT 桌面应用安装入口。",
		"manualPrimaryLabel":        "手动选择应用",
		"manualPrimaryMode":         "folder",
		"manualSecondaryLabel":      "",
		"manualSecondaryMode":       "",
		"detectionNote":             "自动检测暂未找到 ChatGPT 时，可以手动选择实际安装位置。",
		"pathHint":                  "",
		"launchMethodLabel":         "启动方式",
		"launchTargetLabel":         "启动文件",
		"completionLabel":           "已完成当前系统引导",
		"unsupported":               false,
	}
	switch goos {
	case "darwin":
		guide["title"] = "macOS 新手引导"
		guide["systemDescription"] = "macOS 会检查 ChatGPT.app、官方安装页和 WebKit 桌面窗口运行状态。"
		guide["desktopRuntimeDescription"] = "macOS 使用 WebKit 桌面窗口运行管理器。"
		guide["installTitle"] = "macOS 官方安装"
		guide["installActionLabel"] = "打开官方安装页"
		guide["installSourceLabel"] = "官方页面"
		guide["installDescription"] = "macOS 默认打开 ChatGPT 官方下载页面。"
		guide["manualPrimaryLabel"] = "选择 ChatGPT.app"
		guide["manualPrimaryMode"] = "folder"
		guide["detectionNote"] = "macOS 已安装但未识别时，选择 /Applications/ChatGPT.app 或实际放置 ChatGPT.app 的应用目录即可。"
		guide["pathHint"] = "/Applications/ChatGPT.app"
		guide["launchMethodLabel"] = "启动方式"
		guide["launchTargetLabel"] = "ChatGPT.app"
	case "windows":
		guide["title"] = "Windows 新手引导"
		guide["systemDescription"] = "Windows 会同时识别 Codex 与 ChatGPT 合并后的 OpenAI.Codex / OpenAI.ChatGPT MSIX 包，以及 Codex.exe / ChatGPT.exe。"
		guide["desktopRuntimeDescription"] = "Windows 使用 WebView2 桌面窗口运行管理器。"
		guide["installTitle"] = "Windows 官方安装"
		guide["installActionLabel"] = "打开 ChatGPT 下载页"
		guide["installSourceLabel"] = "官方页面"
		guide["installDescription"] = "请安装新版 ChatGPT 桌面应用；原 Codex 应用更新后也会成为该合并版。自动检测失败时可选择 Codex.exe、ChatGPT.exe 或安装目录。"
		guide["manualPrimaryLabel"] = "选择 ChatGPT.exe / Codex.exe"
		guide["manualPrimaryMode"] = "file"
		guide["manualSecondaryLabel"] = "选择安装目录"
		guide["manualSecondaryMode"] = "folder"
		guide["detectionNote"] = "Microsoft Store/MSIX 版不会扫描或信任任意 WindowsApps 路径；仅使用已注册官方包清单声明的 App Execution Alias 或 FullTrust 入口，并以真实 AppUserModelID、进程包身份和调试端口完成验证。"
		guide["pathHint"] = `OpenAI.Codex / OpenAI.ChatGPT，或 WindowsApps 外的 Codex.exe / ChatGPT.exe`
		guide["launchMethodLabel"] = "启动方式"
		guide["launchTargetLabel"] = "AppUserModelID 或可执行文件"
	default:
		guide["title"] = platformDisplayName(goos) + " 受限引导"
		guide["systemDescription"] = "当前平台只提供基础状态检查；完整新手引导重点支持 macOS 和 Windows。"
		guide["desktopRuntimeDescription"] = "当前平台会按可用能力使用桌面窗口或浏览器模式。"
		guide["installDescription"] = "请根据当前平台手动安装 ChatGPT，并在修复工具里填写可启动路径。"
		guide["detectionNote"] = "此平台不在本次完整引导范围内，路径检测能力有限。"
		guide["unsupported"] = true
	}
	return guide
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
	return desktopRuntimeNameFor(runtime.GOOS)
}

func desktopRuntimeNameFor(goos string) string {
	switch goos {
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
	return codexOfficialInstallURL
}

func codexInstallSource(download map[string]any) string {
	if source := stringFromAny(download["source"]); source != "" {
		return source
	}
	return "official"
}

func latestCodexDownload(ctx context.Context, goos, goarch string) map[string]any {
	_ = ctx
	_ = goos
	_ = goarch
	payload := map[string]any{
		"status":      "available",
		"source":      "official",
		"downloadUrl": codexOfficialInstallURL,
		"message":     "打开 ChatGPT 官方下载页面安装桌面应用。",
	}
	return payload
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
	state["appKind"] = codexAppKind(path)
	state["appDisplayName"] = codexAppDisplayName(path)
	if path != "" && runtime.GOOS == "windows" {
		executable := buildCodexExecutable(path)
		state["executable"] = executable
		if appUserModelID := packagedWindowsAppUserModelID(path); appUserModelID != "" {
			state["appUserModelId"] = appUserModelID
		}
		if isWindowsPackagedAppReference(path) {
			state["status"] = "found"
			state["message"] = "已保存新版 Windows ChatGPT 的稳定 AppUserModelID；应用更新后无需跟随版本目录变化。"
		}
		if isWindowsProtectedCodexDirectLaunchPath(path, executable) {
			if stringFromAny(state["appUserModelId"]) != "" {
				state["status"] = "found"
				state["message"] = "已检测到新版 Windows Store/MSIX ChatGPT；将通过应用标识启动并验证调试端口。"
			} else {
				state["status"] = "limited"
				state["message"] = "已检测到 Windows Store/MSIX 应用，但未能解析应用启动标识。"
			}
		}
	}
	return state
}

func codexAppKind(path string) string {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if strings.Contains(lower, "chatgpt") || strings.EqualFold(base, "chatgpt") ||
		isWindowsTargetAppExecutableName(filepath.Base(path)) || isWindowsCodexPackageName(windowsPackageNameFromPath(path)) ||
		isWindowsPackagedAppReference(path) {
		return "chatgpt"
	}
	return "unknown"
}

func codexAppDisplayName(path string) string {
	switch codexAppKind(path) {
	case "chatgpt":
		return "ChatGPT 桌面应用"
	default:
		return "ChatGPT 桌面应用"
	}
}

func isWindowsTargetAppExecutableName(name string) bool {
	return strings.EqualFold(name, "ChatGPT.exe") ||
		strings.EqualFold(name, "chatgpt.exe") ||
		strings.EqualFold(name, "Codex.exe") ||
		strings.EqualFold(name, "codex.exe")
}

func windowsTargetAppExecutableNames() []string {
	return []string{"ChatGPT.exe", "chatgpt.exe", "Codex.exe", "codex.exe"}
}

func hasWindowsTargetAppExecutable(path string) bool {
	for _, name := range windowsTargetAppExecutableNames() {
		if fileExists(filepath.Join(path, name)) {
			return true
		}
	}
	return false
}

func normalizeOpenAIAppPackageIdentity(identity string) (string, bool) {
	trimmed := strings.TrimSpace(identity)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "openai.chatgpt":
		return "OpenAI.ChatGPT", true
	case "openai.chatgptbeta":
		return "OpenAI.ChatGPTBeta", true
	case "openai.codex":
		return "OpenAI.Codex", true
	case "openai.codexbeta":
		return "OpenAI.CodexBeta", true
	}
	return "", false
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
		if runtime.GOOS != "windows" || windowsCodexPathSupportsLaunch(normalized) {
			return normalized
		}
	}
	if runtime.GOOS == "darwin" {
		candidates := []string{"/Applications/ChatGPT.app"}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, "Applications", "ChatGPT.app"),
			)
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
	}
	return ""
}

func resolveWindowsCodexFromInstalledApps() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	commands := [][]string{
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -ieq 'OpenAI.Codex_2p2nqsd0c76g0' } | Sort-Object Version -Descending | Select-Object -First 1 -ExpandProperty InstallLocation`},
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -Name OpenAI.ChatGPT -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -ieq 'OpenAI.ChatGPT_2p2nqsd0c76g0' } | Sort-Object Version -Descending | Select-Object -First 1 -ExpandProperty InstallLocation`},
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -Name OpenAI.CodexBeta -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -ieq 'OpenAI.CodexBeta_2p2nqsd0c76g0' } | Sort-Object Version -Descending | Select-Object -First 1 -ExpandProperty InstallLocation`},
		{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", `Get-AppxPackage -Name OpenAI.ChatGPTBeta -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -ieq 'OpenAI.ChatGPTBeta_2p2nqsd0c76g0' } | Sort-Object Version -Descending | Select-Object -First 1 -ExpandProperty InstallLocation`},
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
		if location := latestAppxInstallLocationFromOutput(string(out)); location != "" {
			if normalized := normalizeCodexAppPath(location); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func latestAppxInstallLocationFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
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
	for _, key := range []string{"CHATGPT_APP_PATH", "CHATGPT_PATH", "CHATGPT_DESKTOP_PATH", "CODEX_APP_PATH", "CODEX_PATH", "CODEX_DESKTOP_PATH"} {
		addCandidate(os.Getenv(key))
	}
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if root == "" {
			continue
		}
		addCandidate(filepath.Join(root, "Programs", "ChatGPT"))
		addCandidate(filepath.Join(root, "ChatGPT"))
		addCandidate(filepath.Join(root, "OpenAI", "ChatGPT"))
		addCandidate(filepath.Join(root, "OpenAI ChatGPT"))
		addCandidate(filepath.Join(root, "Programs", "Codex"))
		addCandidate(filepath.Join(root, "Codex"))
		addCandidate(filepath.Join(root, "OpenAI", "Codex"))
		addCandidate(filepath.Join(root, "OpenAI Codex"))
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
		if reference := normalizeWindowsPackagedAppReference(path); reference != "" {
			if packagedWindowsAppUserModelID(reference) != "" {
				return reference
			}
			return ""
		}
		if normalized := normalizeWindowsPackageAppPath(path); normalized != "" {
			if isWindowsProtectedAppPackagePath(normalized) {
				if appUserModelID := packagedWindowsAppUserModelID(normalized); appUserModelID != "" {
					return windowsPackagedAppReference(appUserModelID)
				}
			}
			return normalized
		}
	}
	if runtime.GOOS == "windows" && isWindowsAppsExecutionAlias(path) {
		return ""
	}
	if isWindowsTargetAppExecutableName(filepath.Base(path)) {
		return filepath.Dir(path)
	}
	if strings.EqualFold(filepath.Ext(path), ".app") {
		if isCodexToolsAppPath(path) {
			return ""
		}
		if strings.EqualFold(filepath.Base(path), "ChatGPT.app") {
			return path
		}
		return ""
	}
	if fileExists(path) && !isDir(path) {
		if runtime.GOOS == "darwin" && !strings.EqualFold(filepath.Base(path), "ChatGPT") {
			return ""
		}
		return filepath.Dir(path)
	}
	if hasWindowsTargetAppExecutable(path) {
		return path
	}
	for _, subdir := range []string{
		"app",
		"VFS",
		filepath.Join("VFS", "ProgramFilesX64", "ChatGPT"),
		filepath.Join("VFS", "ProgramFilesX64", "OpenAI", "ChatGPT"),
		filepath.Join("VFS", "ProgramFilesX64", "Codex"),
		filepath.Join("VFS", "ProgramFilesX64", "OpenAI", "Codex"),
	} {
		candidate := filepath.Join(path, subdir)
		if hasWindowsTargetAppExecutable(candidate) {
			return candidate
		}
	}
	nested := filepath.Join(path, "app")
	if isDir(nested) && hasWindowsTargetAppExecutable(nested) {
		return nested
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return ""
	}
	if isDir(path) {
		return path
	}
	return ""
}

func isCodexToolsAppPath(path string) bool {
	if runtime.GOOS != "darwin" || !strings.EqualFold(filepath.Ext(path), ".app") {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(path), ".app")
	for _, ownName := range []string{silentName, managerName, appName, "Codex++", "Codex++ 管理工具", "CodexTools"} {
		if strings.EqualFold(name, ownName) {
			return true
		}
	}
	infoPlist := filepath.Join(path, "Contents", "Info.plist")
	data, err := os.ReadFile(infoPlist)
	if err != nil {
		return false
	}
	text := string(data)
	bundleID := strings.ToLower(plistStringAfterKey(text, "CFBundleIdentifier"))
	if strings.HasPrefix(bundleID, "com.hereww.chatgptcodextools") ||
		strings.HasPrefix(bundleID, "com.hereww.codextools") ||
		strings.HasPrefix(bundleID, "com.bigpizzav3.codexplusplus") {
		return true
	}
	executable := strings.ToLower(plistStringAfterKey(text, "CFBundleExecutable"))
	return strings.HasPrefix(executable, "codextools")
}

func isWindowsAppsExecutionAlias(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	base := filepath.Base(path)
	if !isWindowsTargetAppExecutableName(base) {
		return false
	}
	dir := strings.ToLower(filepath.ToSlash(filepath.Dir(path)))
	return strings.Contains(dir, "/microsoft/windowsapps") || strings.HasSuffix(dir, "/windowsapps")
}

func isWindowsProtectedAppPackagePath(path string) bool {
	if windowsPathContainsProtectedAppPackage(path) {
		return true
	}
	if runtime.GOOS == "windows" {
		if resolved, err := filepath.EvalSymlinks(path); err == nil && !strings.EqualFold(resolved, path) {
			return windowsPathContainsProtectedAppPackage(resolved)
		}
	}
	return false
}

func windowsPathContainsProtectedAppPackage(path string) bool {
	cleaned := pathpkg.Clean(strings.ReplaceAll(strings.TrimSpace(path), `\`, "/"))
	parts := splitPathParts(cleaned)
	for index, part := range parts {
		part = strings.TrimRight(part, " .")
		if !strings.EqualFold(part, "WindowsApps") || index+1 >= len(parts) {
			continue
		}
		packageName := strings.TrimRight(parts[index+1], " .")
		if isWindowsCodexPackageName(packageName) {
			return true
		}
	}
	return false
}

func isWindowsProtectedCodexDirectLaunchPath(appPath, executable string) bool {
	return isWindowsProtectedAppPackagePath(appPath) || isWindowsProtectedAppPackagePath(executable)
}

func normalizeWindowsPackageAppPath(path string) string {
	packageName := windowsPackageNameFromPath(path)
	if !isWindowsCodexPackageName(packageName) {
		return ""
	}
	packageRoot := windowsPackageRootFromPath(path)
	if packageRoot == "" {
		return ""
	}
	if metadata, ok := readWindowsPackageManifestMetadata(packageRoot); ok && metadata.Executable != "" {
		return pathDirLike(path, joinPathLike(packageRoot, metadata.Executable))
	}
	return joinPathLike(packageRoot, "app")
}

func windowsPackagedExecutionAlias(appUserModelID string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	launchInfo, ok := resolveWindowsRegisteredPackageLaunchInfo(appUserModelID)
	if !ok {
		return ""
	}
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		for _, aliasName := range launchInfo.ExecutionAliases {
			for _, alias := range []string{
				filepath.Join(root, "Microsoft", "WindowsApps", launchInfo.PackageFamilyName, aliasName),
				filepath.Join(root, "Microsoft", "WindowsApps", aliasName),
			} {
				if _, err := os.Lstat(alias); err == nil && isWindowsAppsExecutionAlias(alias) {
					return alias
				}
			}
		}
	}
	for _, aliasName := range launchInfo.ExecutionAliases {
		alias, err := exec.LookPath(aliasName)
		if err == nil && strings.EqualFold(filepath.Base(alias), aliasName) && isWindowsAppsExecutionAlias(alias) {
			return alias
		}
	}
	return ""
}

func windowsPackagedDirectExecutable(appUserModelID string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	launchInfo, ok := resolveWindowsRegisteredPackageLaunchInfo(appUserModelID)
	if !ok || !strings.EqualFold(strings.TrimSpace(launchInfo.EntryPoint), "Windows.FullTrustApplication") {
		return ""
	}
	return launchInfo.ExecutablePath
}

func packagedWindowsAppUserModelID(path string) string {
	if reference := normalizeWindowsPackagedAppReference(path); reference != "" {
		appUserModelID := strings.TrimPrefix(reference, "aumid:")
		if runtime.GOOS != "windows" {
			return appUserModelID
		}
		cacheKey := strings.ToLower(reference)
		if cached, found := windowsPackagedAUMIDCache.Load(cacheKey); found {
			return stringFromAny(cached)
		}
		resolved := validateWindowsAppUserModelIDViaPowerShell(appUserModelID)
		if resolved != "" {
			windowsPackagedAUMIDCache.Store(cacheKey, resolved)
		}
		return resolved
	}
	fallback := windowsAppUserModelIDFromPackagePath(path)
	if fallback == "" {
		return ""
	}
	if runtime.GOOS != "windows" {
		return fallback
	}
	packageName := windowsPackageNameFromPath(path)
	identity, _, publisherID, _ := windowsCodexPackageParts(packageName)
	cacheKey := strings.ToLower(packageName)
	if cached, found := windowsPackagedAUMIDCache.Load(cacheKey); found {
		return stringFromAny(cached)
	}
	resolved := resolveWindowsAppUserModelIDViaPowerShell(packageName, identity, publisherID)
	if resolved != "" {
		windowsPackagedAUMIDCache.Store(cacheKey, resolved)
	}
	return resolved
}

func windowsAppUserModelIDFromPackagePath(path string) string {
	packageName := windowsPackageNameFromPath(path)
	identity, _, publisherID, ok := windowsCodexPackageParts(packageName)
	if !ok || !strings.EqualFold(publisherID, openAIDesktopPublisherID) {
		return ""
	}
	applicationID := "App"
	if packageRoot := windowsPackageRootFromPath(path); packageRoot != "" {
		if metadata, manifestOK := readWindowsPackageManifestMetadata(packageRoot); manifestOK && metadata.ApplicationID != "" {
			applicationID = metadata.ApplicationID
		}
	}
	return identity + "_" + publisherID + "!" + applicationID
}

func windowsPackagedAppReference(appUserModelID string) string {
	if normalized := normalizeWindowsPackagedAppReference("aumid:" + strings.TrimSpace(appUserModelID)); normalized != "" {
		return normalized
	}
	return ""
}

func isWindowsPackagedAppReference(value string) bool {
	return normalizeWindowsPackagedAppReference(value) != ""
}

func normalizeWindowsPackagedAppReference(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= len("aumid:") || !strings.EqualFold(trimmed[:len("aumid:")], "aumid:") {
		return ""
	}
	aumid := strings.TrimSpace(trimmed[len("aumid:"):])
	packageFamily, applicationID, foundApplication := strings.Cut(aumid, "!")
	identityPart, publisherID, foundPublisher := strings.Cut(packageFamily, "_")
	identity, supported := normalizeOpenAIAppPackageIdentity(identityPart)
	if !foundApplication || !foundPublisher || !supported || !strings.EqualFold(publisherID, openAIDesktopPublisherID) || !isWindowsAppIdentifierPart(applicationID) {
		return ""
	}
	return "aumid:" + identity + "_" + publisherID + "!" + applicationID
}

func isWindowsAppIdentifierPart(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func windowsPackageFamilyFromAppUserModelID(appUserModelID string) string {
	reference := normalizeWindowsPackagedAppReference("aumid:" + strings.TrimSpace(appUserModelID))
	if reference == "" {
		return ""
	}
	packageFamily, _, found := strings.Cut(strings.TrimPrefix(reference, "aumid:"), "!")
	if !found {
		return ""
	}
	return packageFamily
}

func isOpenAIDesktopPackageFamily(packageFamily string) bool {
	identityPart, publisherID, found := strings.Cut(strings.TrimSpace(packageFamily), "_")
	_, supported := normalizeOpenAIAppPackageIdentity(identityPart)
	return found && supported && strings.EqualFold(publisherID, openAIDesktopPublisherID)
}

func normalizeWindowsRegisteredPackageLaunchInfo(raw windowsRegisteredPackageLaunchInfo, expectedAUMID string) (windowsRegisteredPackageLaunchInfo, bool) {
	reference := normalizeWindowsPackagedAppReference("aumid:" + strings.TrimSpace(expectedAUMID))
	if reference == "" {
		return windowsRegisteredPackageLaunchInfo{}, false
	}
	canonicalAUMID := strings.TrimPrefix(reference, "aumid:")
	expectedFamily := windowsPackageFamilyFromAppUserModelID(canonicalAUMID)
	if expectedFamily == "" || !strings.EqualFold(strings.TrimSpace(raw.AppUserModelID), canonicalAUMID) ||
		!strings.EqualFold(strings.TrimSpace(raw.PackageFamilyName), expectedFamily) {
		return windowsRegisteredPackageLaunchInfo{}, false
	}
	identity, _, publisherID, ok := windowsCodexPackageParts(strings.TrimSpace(raw.PackageFullName))
	expectedIdentity, _, _ := strings.Cut(expectedFamily, "_")
	if !ok || !strings.EqualFold(identity, expectedIdentity) || !strings.EqualFold(publisherID, openAIDesktopPublisherID) {
		return windowsRegisteredPackageLaunchInfo{}, false
	}
	applicationID := strings.TrimSpace(raw.ApplicationID)
	if !isWindowsAppIdentifierPart(applicationID) || !strings.EqualFold(expectedFamily+"!"+applicationID, canonicalAUMID) {
		return windowsRegisteredPackageLaunchInfo{}, false
	}
	installLocation := strings.TrimSpace(raw.InstallLocation)
	executable := normalizeWindowsPackageRelativePath(raw.Executable)
	parts := splitPathParts(executable)
	if installLocation == "" || len(parts) == 0 || !isWindowsTargetAppExecutableName(parts[len(parts)-1]) {
		return windowsRegisteredPackageLaunchInfo{}, false
	}
	aliases := make([]string, 0, len(raw.ExecutionAliases))
	for _, candidate := range raw.ExecutionAliases {
		alias := strings.TrimSpace(candidate)
		if isWindowsTargetAppExecutableName(alias) {
			aliases = appendUniqueFold(aliases, alias)
		}
	}
	raw.AppUserModelID = canonicalAUMID
	raw.PackageFamilyName = expectedFamily
	raw.InstallLocation = installLocation
	raw.ApplicationID = applicationID
	raw.Executable = executable
	raw.EntryPoint = strings.TrimSpace(raw.EntryPoint)
	raw.ExecutionAliases = aliases
	raw.ExecutablePath = joinPathLike(installLocation, executable)
	return raw, true
}

func resolveWindowsAppUserModelIDViaPowerShell(packageName, identity, publisherID string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	const script = `$package = Get-AppxPackage -Name $env:CODEX_DESKTOP_PACKAGE_IDENTITY -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -ieq $env:CODEX_DESKTOP_PACKAGE_FAMILY } | Sort-Object Version -Descending | Select-Object -First 1; if ($null -eq $package) { exit 0 }; $apps = @((Get-AppxPackageManifest -Package $package).Package.Applications.Application); $matches = @($apps | Where-Object { $_.Executable -match '(?i)(^|[\\/])(ChatGPT|Codex)\.exe$' }); if ($matches.Count -eq 0) { $matches = @($apps | Where-Object { $_.Id -eq 'App' }) }; if ($matches.Count -eq 1 -and -not [string]::IsNullOrWhiteSpace([string]$matches[0].Id)) { [Console]::Out.Write($package.PackageFamilyName + '!' + $matches[0].Id) }`
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(),
		"CODEX_DESKTOP_PACKAGE_IDENTITY="+identity,
		"CODEX_DESKTOP_PACKAGE_FAMILY="+identity+"_"+publisherID,
	)
	hideSubprocessWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(out))
	expectedPrefix := identity + "_" + publisherID + "!"
	if !strings.HasPrefix(strings.ToLower(value), strings.ToLower(expectedPrefix)) || len(value) <= len(expectedPrefix) || strings.ContainsAny(value, "\r\n\t ") {
		return ""
	}
	return value
}

func validateWindowsAppUserModelIDViaPowerShell(appUserModelID string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	launchInfo, ok := resolveWindowsRegisteredPackageLaunchInfo(appUserModelID)
	if !ok {
		return ""
	}
	return launchInfo.AppUserModelID
}

func isWindowsCodexPackageName(name string) bool {
	_, _, _, ok := windowsCodexPackageParts(name)
	return ok
}

func windowsPackageNameFromPath(path string) string {
	parts := splitPathParts(path)
	if len(parts) == 0 {
		return ""
	}
	for _, part := range parts {
		candidate := strings.TrimRight(part, " .")
		if isWindowsCodexPackageName(candidate) {
			return candidate
		}
	}
	last := parts[len(parts)-1]
	if isWindowsTargetAppExecutableName(last) {
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

type windowsPackageManifest struct {
	Identity struct {
		Name string `xml:"Name,attr"`
	} `xml:"Identity"`
	Applications []windowsPackageManifestApplication `xml:"Applications>Application"`
}

type windowsPackageManifestApplication struct {
	ID         string                            `xml:"Id,attr"`
	Executable string                            `xml:"Executable,attr"`
	EntryPoint string                            `xml:"EntryPoint,attr"`
	Extensions []windowsPackageManifestExtension `xml:"Extensions>Extension"`
}

type windowsPackageManifestExtension struct {
	Category          string                                    `xml:"Category,attr"`
	Executable        string                                    `xml:"Executable,attr"`
	EntryPoint        string                                    `xml:"EntryPoint,attr"`
	AppExecutionAlias windowsPackageManifestAppExecutionAliases `xml:"AppExecutionAlias"`
}

type windowsPackageManifestAppExecutionAliases struct {
	Aliases []windowsPackageManifestExecutionAlias `xml:"ExecutionAlias"`
}

type windowsPackageManifestExecutionAlias struct {
	Alias string `xml:"Alias,attr"`
}

type windowsPackageAppMetadata struct {
	ApplicationID    string
	Executable       string
	EntryPoint       string
	ExecutionAliases []string
}

type windowsRegisteredPackageLaunchInfo struct {
	AppUserModelID    string   `json:"appUserModelId"`
	PackageFamilyName string   `json:"packageFamilyName"`
	PackageFullName   string   `json:"packageFullName"`
	InstallLocation   string   `json:"installLocation"`
	ApplicationID     string   `json:"applicationId"`
	Executable        string   `json:"executable"`
	EntryPoint        string   `json:"entryPoint"`
	ExecutionAliases  []string `json:"executionAliases"`
	ExecutablePath    string   `json:"-"`
}

func windowsPackageRootFromPath(path string) string {
	packageName := windowsPackageNameFromPath(path)
	if !isWindowsCodexPackageName(packageName) {
		return ""
	}
	parts := splitPathParts(path)
	for index, part := range parts {
		if strings.EqualFold(part, packageName) {
			return joinPathPartsLike(path, parts[:index+1])
		}
	}
	return ""
}

func joinPathPartsLike(original string, parts []string) string {
	separator := string(os.PathSeparator)
	if strings.Contains(original, `\`) {
		separator = `\`
	}
	joined := strings.Join(parts, separator)
	if separator == "/" && strings.HasPrefix(strings.TrimSpace(original), "/") {
		joined = "/" + joined
	}
	return joined
}

func joinPathLike(root, relative string) string {
	relative = strings.TrimSpace(relative)
	if relative == "" {
		return root
	}
	cleaned := normalizeWindowsPackageRelativePath(relative)
	if cleaned == "" {
		return root
	}
	if strings.Contains(root, `\`) {
		return strings.TrimRight(root, `\/`) + `\` + strings.ReplaceAll(cleaned, "/", `\`)
	}
	return filepath.Join(root, filepath.FromSlash(cleaned))
}

func normalizeWindowsPackageRelativePath(relative string) string {
	cleaned := pathpkg.Clean(strings.ReplaceAll(strings.TrimSpace(relative), `\`, "/"))
	if cleaned == "." || pathpkg.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func appendUniqueFold(values []string, candidate string) []string {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return values
		}
	}
	return append(values, candidate)
}

func pathDirLike(original, path string) string {
	if strings.Contains(original, `\`) {
		cleaned := strings.TrimRight(path, `\/`)
		if index := strings.LastIndexAny(cleaned, `\/`); index >= 0 {
			return cleaned[:index]
		}
		return ""
	}
	return filepath.Dir(path)
}

func readWindowsPackageManifestMetadata(packageRoot string) (windowsPackageAppMetadata, bool) {
	manifestPath := joinPathLike(packageRoot, "AppxManifest.xml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return windowsPackageAppMetadata{}, false
	}
	var manifest windowsPackageManifest
	if err := xml.Unmarshal(data, &manifest); err != nil {
		return windowsPackageAppMetadata{}, false
	}
	manifestIdentity, supported := normalizeOpenAIAppPackageIdentity(manifest.Identity.Name)
	packageIdentity, _, _, packageOK := windowsCodexPackageParts(windowsPackageNameFromPath(packageRoot))
	if !supported || !packageOK || !strings.EqualFold(manifestIdentity, packageIdentity) {
		return windowsPackageAppMetadata{}, false
	}
	applications := manifest.Applications
	if len(applications) == 0 {
		return windowsPackageAppMetadata{}, false
	}
	type manifestMatch struct {
		application windowsPackageManifestApplication
		executable  string
		entryPoint  string
		aliases     []string
	}
	var executableMatches []manifestMatch
	for _, application := range applications {
		executable := normalizeWindowsPackageRelativePath(application.Executable)
		entryPoint := strings.TrimSpace(application.EntryPoint)
		var aliases []string
		for _, extension := range application.Extensions {
			if !strings.EqualFold(strings.TrimSpace(extension.Category), "windows.appExecutionAlias") {
				continue
			}
			if executable == "" {
				executable = normalizeWindowsPackageRelativePath(extension.Executable)
			}
			if entryPoint == "" {
				entryPoint = strings.TrimSpace(extension.EntryPoint)
			}
			for _, executionAlias := range extension.AppExecutionAlias.Aliases {
				alias := strings.TrimSpace(executionAlias.Alias)
				if isWindowsTargetAppExecutableName(alias) {
					aliases = appendUniqueFold(aliases, alias)
				}
			}
		}
		parts := splitPathParts(executable)
		if len(parts) > 0 && isWindowsTargetAppExecutableName(parts[len(parts)-1]) {
			executableMatches = append(executableMatches, manifestMatch{
				application: application,
				executable:  executable,
				entryPoint:  entryPoint,
				aliases:     aliases,
			})
		}
	}
	if len(executableMatches) == 0 {
		for _, application := range applications {
			if strings.EqualFold(application.ID, "App") {
				executableMatches = append(executableMatches, manifestMatch{application: application})
			}
		}
	}
	if len(executableMatches) != 1 {
		return windowsPackageAppMetadata{}, false
	}
	selected := executableMatches[0]
	applicationID := strings.TrimSpace(selected.application.ID)
	if !isWindowsAppIdentifierPart(applicationID) {
		return windowsPackageAppMetadata{}, false
	}
	return windowsPackageAppMetadata{
		ApplicationID:    applicationID,
		Executable:       selected.executable,
		EntryPoint:       selected.entryPoint,
		ExecutionAliases: selected.aliases,
	}, true
}

func windowsPackageVersionFromPath(path string) string {
	return windowsPackageVersionFromName(windowsPackageNameFromPath(path))
}

func windowsPackageVersionFromName(name string) string {
	_, version, _, ok := windowsCodexPackageParts(name)
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

func windowsCodexPackageParts(name string) (identity string, version string, publisherID string, ok bool) {
	trimmed := strings.TrimSpace(name)
	identityPart, rest, hasIdentity := strings.Cut(trimmed, "_")
	if !hasIdentity || identityPart == "" {
		return "", "", "", false
	}
	normalizedIdentity, supported := normalizeOpenAIAppPackageIdentity(identityPart)
	if !supported {
		return "", "", "", false
	}
	packageVersion, rest, hasArch := strings.Cut(rest, "_")
	if !hasArch || packageVersion == "" {
		return "", "", "", false
	}
	_, publisher, hasPublisher := strings.Cut(rest, "__")
	if !hasPublisher || publisher == "" {
		return "", "", "", false
	}
	return normalizedIdentity, packageVersion, publisher, true
}

func splitPathParts(path string) []string {
	return strings.FieldsFunc(filepath.ToSlash(strings.TrimSpace(path)), func(r rune) bool {
		return r == '/' || r == '\\'
	})
}

func codexDetectionPayload(saved, resolved string) map[string]any {
	payload := map[string]any{
		"savedPath":      nullableString(saved),
		"resolvedPath":   nullableString(resolved),
		"status":         "missing",
		"message":        "未检测到 ChatGPT 桌面应用。",
		"candidates":     []string{},
		"appKind":        codexAppKind(resolved),
		"appDisplayName": codexAppDisplayName(resolved),
	}
	if resolved != "" {
		payload["status"] = "found"
		payload["message"] = "已检测到 " + codexAppDisplayName(resolved) + "。"
		payload["executable"] = buildCodexExecutable(resolved)
		if appUserModelID := packagedWindowsAppUserModelID(resolved); appUserModelID != "" {
			payload["appUserModelId"] = appUserModelID
		}
		if runtime.GOOS == "windows" && isWindowsPackagedAppReference(resolved) {
			payload["message"] = "已通过稳定 AppUserModelID 识别 Codex 与 ChatGPT 合并后的 Windows 新版应用。"
		}
		if runtime.GOOS == "windows" && isWindowsProtectedCodexDirectLaunchPath(resolved, stringFromAny(payload["executable"])) {
			if stringFromAny(payload["appUserModelId"]) != "" {
				payload["status"] = "found"
				payload["message"] = "已检测到 Codex 与 ChatGPT 合并后的 Windows Store/MSIX 新版应用；将通过 AppUserModelID 启动。"
			} else {
				payload["status"] = "limited"
				payload["message"] = "已检测到新版 Windows Store/MSIX 应用，但未能解析 AppUserModelID。"
			}
		}
		return payload
	}
	if runtime.GOOS == "windows" {
		payload["message"] = "Windows 自动探测没有找到合并后的 ChatGPT/Codex 应用。若已安装，请选择 ChatGPT.exe、Codex.exe 或安装目录。"
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
		"message":        "未检测到 ChatGPT 桌面应用，无法启动。",
		"appKind":        codexAppKind(appPath),
		"appDisplayName": codexAppDisplayName(appPath),
	}
	if appPath == "" {
		return payload
	}
	if runtime.GOOS == "windows" {
		executable := buildCodexExecutable(appPath)
		protectedPackage := isWindowsProtectedCodexDirectLaunchPath(appPath, executable)
		if strings.TrimSpace(executable) != "" && fileExists(executable) && !protectedPackage {
			payload["ready"] = true
			payload["method"] = "executable"
			payload["methodLabel"] = "可执行文件启动"
			payload["executable"] = executable
			payload["message"] = "将直接启动 " + filepath.Base(executable) + " 并附加调试端口参数。"
			return payload
		}
		if appUserModelID := packagedWindowsAppUserModelID(appPath); appUserModelID != "" {
			payload["appUserModelId"] = appUserModelID
			payload["executable"] = executable
			payload["method"] = "packaged_activation"
			payload["methodLabel"] = "MSIX 应用激活"
			payload["ready"] = true
			if alias := windowsPackagedExecutionAlias(appUserModelID); alias != "" {
				payload["executionAlias"] = alias
				payload["methodLabel"] = "App Execution Alias"
				payload["message"] = "将通过包清单声明的 Windows App Execution Alias 启动合并版 ChatGPT，并验证调试端口、进程树和包身份。"
			} else if packagedExecutable := windowsPackagedDirectExecutable(appUserModelID); packagedExecutable != "" {
				payload["packagedExecutableAvailable"] = true
				payload["methodLabel"] = "MSIX FullTrust 入口"
				payload["message"] = "将通过已注册官方包清单声明的 FullTrust 桌面入口启动合并版 ChatGPT，并验证调试端口、进程树和包身份。"
			} else {
				payload["message"] = "将通过 AppUserModelID 兼容激活合并版 ChatGPT，并验证调试端口和进程归属。"
			}
			payload["directLaunchBlocked"] = protectedPackage
			return payload
		}
	}
	executable := buildCodexExecutable(appPath)
	if strings.TrimSpace(executable) == "" {
		payload["message"] = "已识别到 ChatGPT 目录，但没有找到可执行文件。"
		return payload
	}
	if runtime.GOOS == "windows" && !isWindowsAppsExecutionAlias(executable) && !fileExists(executable) {
		payload["method"] = "executable_missing"
		payload["executable"] = executable
		payload["message"] = "已推断应用可执行文件位置，但文件不存在。"
		return payload
	}
	payload["ready"] = true
	payload["method"] = "executable"
	payload["methodLabel"] = "可执行文件启动"
	payload["executable"] = executable
	payload["message"] = "将通过可执行文件启动 " + codexAppDisplayName(appPath) + "。"
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
	if profile.Protocol == "responses" && profile.ImageGenerationEnabled && profile.ImageGenerationUseSeparateAPI {
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
			filepath.Join(local, "Programs", "ChatGPT"),
			filepath.Join(local, "Microsoft", "WindowsApps", "ChatGPT.exe"),
			filepath.Join(local, "Programs", "Codex"),
			filepath.Join(local, "Microsoft", "WindowsApps", "Codex.exe"),
		)
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		hints = append(hints,
			filepath.Join(programFiles, "WindowsApps", "OpenAI.ChatGPT*"),
			filepath.Join(programFiles, "WindowsApps", "OpenAI.Codex*"),
			filepath.Join(programFiles, "OpenAI", "ChatGPT"),
			filepath.Join(programFiles, "OpenAI", "Codex"),
		)
	}
	hints = append(hints, "Get-AppxPackage OpenAI.Codex / OpenAI.ChatGPT")
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
		if _, version, _, ok := windowsCodexPackageParts(parts[i]); ok && version != "" {
			return &version
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
	appPath := managerLaunchAppPath(stringArg(request, "appPath"), loadSettings())
	debugPort := uint16Arg(request, "debugPort", 9229)
	helperPort := uint16Arg(request, "helperPort", 57321)
	launcher := managerLauncherBinaryPath()
	command := buildManagerLauncherCommand(launcher, appPath, debugPort, helperPort, restart)
	launcherPayload := managerLauncherPayload(launcher, command, appPath, debugPort, helperPort)
	if !fileExists(launcher) {
		return failed("启动 ChatGPT Codex 失败：未找到静默启动器 "+launcher, launcherPayload)
	}
	cmd := exec.Command(command[0], command[1:]...)
	hideSubprocessWindow(cmd)
	if err := cmd.Start(); err != nil {
		return failed("启动 ChatGPT Codex 静默启动器失败："+err.Error(), launcherPayload)
	}
	latest := waitForLaunchStatusAfter(time.Now().Add(-200*time.Millisecond), managerLaunchWaitTimeout())
	if latest != nil && latest.Status == "failed" {
		payload := cloneStringAnyMap(launcherPayload)
		payload["latest_launch"] = latest
		return failed(latest.Message, payload)
	}
	if latest == nil {
		accepted := launchStatus{
			Status:      "accepted",
			Message:     "Go 管理器已启动 ChatGPT Codex 静默启动器。",
			StartedAtMS: uint64(time.Now().UnixMilli()),
			DebugPort:   &debugPort,
			HelperPort:  &helperPort,
			Detail:      cloneStringAnyMap(launcherPayload),
		}
		if appPath != "" {
			accepted.CodexApp = &appPath
		}
		_ = atomicWriteJSON(latestStatusPath(), accepted)
		latest = &accepted
	}
	message := "启动任务已在后台开始，可稍后查看概览状态。"
	if restart {
		message = "ChatGPT Codex 已请求重启，启动任务正在后台运行。"
	}
	result := commandResult{"status": "accepted", "message": message, "debugPort": debugPort, "helperPort": helperPort, "latest_launch": latest}
	for key, value := range launcherPayload {
		result[key] = value
	}
	return result
}

func managerLaunchAppPath(requested string, settings backendSettings) string {
	appPath := normalizeCodexAppPath(requested)
	if runtime.GOOS != "windows" {
		return appPath
	}
	if appPath != "" && windowsCodexPathSupportsLaunch(appPath) {
		return appPath
	}
	if saved := normalizeCodexAppPath(settings.CodexAppPath); saved != "" && windowsCodexPathSupportsLaunch(saved) {
		return saved
	}
	if detected := resolveCodexApp(""); detected != "" && windowsCodexPathSupportsLaunch(detected) {
		return detected
	}
	return ""
}

func windowsCodexPathSupportsDirectLaunch(appPath string) bool {
	if runtime.GOOS != "windows" {
		return strings.TrimSpace(buildCodexExecutable(appPath)) != ""
	}
	executable := buildCodexExecutable(appPath)
	if isWindowsProtectedCodexDirectLaunchPath(appPath, executable) {
		return false
	}
	return strings.TrimSpace(executable) != "" && fileExists(executable)
}

func windowsCodexPathSupportsLaunch(appPath string) bool {
	if runtime.GOOS != "windows" {
		return strings.TrimSpace(buildCodexExecutable(appPath)) != ""
	}
	if windowsCodexPathSupportsDirectLaunch(appPath) {
		return true
	}
	return packagedWindowsAppUserModelID(appPath) != ""
}

func waitForLaunchStatusAfter(after time.Time, timeout time.Duration) *launchStatus {
	deadline := time.Now().Add(timeout)
	var latest *launchStatus
	for time.Now().Before(deadline) {
		var status launchStatus
		if readJSON(latestStatusPath(), &status) == nil && status.StartedAtMS > 0 {
			started := time.UnixMilli(int64(status.StartedAtMS))
			if !started.Before(after) {
				current := status
				latest = &current
				if current.Status != "starting" {
					return latest
				}
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	return latest
}

func managerLaunchWaitTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 20 * time.Second
	}
	return 2 * time.Second
}

func buildManagerLauncherCommand(launcher, appPath string, debugPort, helperPort uint16, restart bool) []string {
	command := []string{launcher, "--launcher", "--debug-port", strconv.Itoa(int(debugPort)), "--helper-port", strconv.Itoa(int(helperPort))}
	if appPath != "" {
		command = append(command, "--app-path", appPath)
	}
	if restart {
		command = append(command, "--restart")
	}
	return command
}

func managerLauncherPayload(launcher string, command []string, appPath string, debugPort, helperPort uint16) map[string]any {
	payload := map[string]any{
		"launch_chain":  "codex_plus_launcher",
		"launcher_path": launcher,
		"launcher_args": []string{},
		"debugPort":     debugPort,
		"helperPort":    helperPort,
	}
	if len(command) > 1 {
		payload["launcher_args"] = append([]string(nil), command[1:]...)
	}
	if appPath != "" {
		payload["codex_app"] = appPath
	}
	return payload
}

func managerLauncherBinaryPath() string {
	launcher := companionBinaryPath(silentBinary)
	switch runtime.GOOS {
	case "windows":
		return launcher + ".exe"
	case "darwin":
		executable, _ := os.Executable()
		return preferredMacOSLauncherBinaryPath(launcher, executable)
	default:
		return launcher
	}
}

func preferredMacOSLauncherBinaryPath(fallback, currentExecutable string) string {
	return preferredMacOSAppExecutableFromCandidates(
		fallback,
		macOSCompanionAppCandidates(currentExecutable, silentName+".app"),
		silentName+".app",
		[]string{"ChatGPTCodex", silentBinary},
	)
}

func preferredMacOSManagerAppPath() string {
	executable, _ := os.Executable()
	for _, candidate := range macOSCompanionAppCandidates(executable, managerName+".app") {
		if fileExists(candidate) {
			return candidate
		}
	}
	return entrypointPath(true)
}

func preferredMacOSAppExecutableFromCandidates(fallback string, appCandidates []string, targetAppBundleName string, executableNames []string) string {
	for _, appPath := range appCandidates {
		if executable := firstExistingMacOSAppExecutable(appPath, executableNames); executable != "" {
			return executable
		}
	}
	if fallback != "" {
		if bundle := macOSAppBundleForPath(fallback); bundle != "" && !strings.EqualFold(filepath.Base(bundle), targetAppBundleName) {
			if len(appCandidates) > 0 && len(executableNames) > 0 {
				return filepath.Join(appCandidates[0], "Contents", "MacOS", executableNames[0])
			}
		}
		return fallback
	}
	if len(appCandidates) > 0 && len(executableNames) > 0 {
		return filepath.Join(appCandidates[0], "Contents", "MacOS", executableNames[0])
	}
	return fallback
}

func firstExistingMacOSAppExecutable(appPath string, executableNames []string) string {
	for _, name := range executableNames {
		candidate := filepath.Join(appPath, "Contents", "MacOS", name)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func macOSCompanionAppCandidates(currentExecutable, targetAppBundleName string) []string {
	var candidates []string
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, path)
	}
	if currentApp := macOSAppBundleForPath(currentExecutable); currentApp != "" {
		add(filepath.Join(filepath.Dir(currentApp), targetAppBundleName))
	}
	add(filepath.Join(defaultInstallRoot(), targetAppBundleName))
	return candidates
}

func macOSAppBundleForPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return ""
	}
	for {
		if strings.EqualFold(filepath.Ext(path), ".app") {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func cloneStringAnyMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
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

func (s *server) loadPendingProviderImport() commandResult {
	return settingsPayload("待确认供应商导入已读取。")
}

func (s *server) savePendingProviderImport(args map[string]any) commandResult {
	request := mapArg(args, "request")
	if rawURL := strings.TrimSpace(stringArg(request, "url")); rawURL != "" {
		parsed, err := providerImportRequestFromURL(rawURL)
		if err != nil {
			return failed("保存供应商导入请求失败："+err.Error(), settingsPayloadValue(loadSettings()))
		}
		if err := savePendingProviderImport(parsed); err != nil {
			return failed("保存供应商导入请求失败："+err.Error(), settingsPayloadValue(loadSettings()))
		}
		return settingsPayload("供应商导入请求已保存，等待确认。")
	}
	var importRequest providerImportRequest
	if err := remarshal(request, &importRequest); err != nil {
		return failed("保存供应商导入请求失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	if err := savePendingProviderImport(importRequest); err != nil {
		return failed("保存供应商导入请求失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	return settingsPayload("供应商导入请求已保存，等待确认。")
}

func (s *server) confirmPendingProviderImport() commandResult {
	result, err := confirmPendingProviderImport()
	payload := settingsPayloadValue(loadSettings())
	if err != nil {
		return failed("确认供应商导入失败："+err.Error(), payload)
	}
	if result == nil {
		return ok("没有待确认的供应商导入。", payload)
	}
	payload["providerImportResult"] = result
	if result.Imported {
		return ok("供应商已导入："+result.ProfileName, payload)
	}
	return ok("供应商已存在："+result.ProfileName, payload)
}

func (s *server) dismissPendingProviderImport() commandResult {
	if err := clearPendingProviderImport(); err != nil {
		return failed("忽略供应商导入失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	return settingsPayload("已忽略待确认供应商导入。")
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
