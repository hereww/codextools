package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func shouldRunLauncher(args []string) bool {
	if binaryRole == "launcher" {
		return true
	}
	if len(args) > 0 {
		base := strings.ToLower(filepath.Base(args[0]))
		if strings.Contains(base, "launcher") {
			return true
		}
	}
	for _, arg := range args[1:] {
		if arg == "--launcher" {
			return true
		}
	}
	return false
}

func runLauncher(args []string) error {
	settings := loadSettings()
	options := parseLaunchRequest(args)
	appPath := resolveCodexApp(options.appPath)
	if appPath == "" {
		appPath = resolveCodexApp(settings.CodexAppPath)
	}
	if appPath == "" {
		return errors.New("未找到 Codex 安装目录，请先在管理器中设置 Codex App 路径")
	}
	debugPort := options.debugPort
	if debugPort == 0 {
		debugPort = 9229
	}
	helperPort := options.helperPort
	if helperPort == 0 {
		helperPort = 57321
	}
	runtimeState := &launcherRuntime{settings: settings, debugPort: debugPort}
	if shouldQuitRunningCodexBeforeLaunch(appPath, debugPort, options.restart) {
		appendDiagnosticLog("launcher.quit_existing_codex", map[string]any{"codex_app": appPath, "debug_port": debugPort, "restart": options.restart})
		if err := quitMacOSApp(appPath); err != nil {
			appendDiagnosticLog("launcher.quit_existing_codex_failed", map[string]any{"codex_app": appPath, "error": err.Error()})
		}
		if !waitForMacOSAppExit(appPath, 8*time.Second) {
			appendDiagnosticLog("launcher.force_kill_existing_codex", map[string]any{"codex_app": appPath})
			_ = forceKillMacOSApp(appPath)
			_ = waitForMacOSAppExit(appPath, 4*time.Second)
		}
		if activeRelayProfile(settings).needsLocalRelayProxy() {
			waitForTCPPortFree(localRelayProxyPort, 5*time.Second)
		}
	}
	if settings.ProviderSync {
		result := runProviderSync(codexHomeDir())
		repairResult := repairCodexConfig(codexHomeDir(), codexConfigRepairOptions{Plugins: true})
		appendDiagnosticLog("provider_sync."+result.Status, map[string]any{
			"targetProvider":      result.TargetProvider,
			"changedSessionFiles": result.ChangedSessionFiles,
			"sqliteRowsUpdated":   result.SQLiteRowsUpdated,
			"message":             result.Message,
		})
		appendDiagnosticLog("codex_plugin_repair."+repairResult.Status, map[string]any{
			"pluginCount":      repairResult.PluginCount,
			"marketplaceCount": repairResult.MarketplaceCount,
			"changed":          repairResult.PluginConfigChanged,
			"message":          repairResult.Message,
		})
	}
	if helperNeeded(settings) {
		if err := runtimeState.startHelper(helperPort); err != nil {
			failure := launchStatus{
				Status:      "failed",
				Message:     "启动 Codex++ helper 失败：" + err.Error(),
				StartedAtMS: uint64(time.Now().UnixMilli()),
				DebugPort:   &debugPort,
				HelperPort:  &helperPort,
				CodexApp:    &appPath,
			}
			_ = atomicWriteJSON(latestStatusPath(), failure)
			appendDiagnosticLog("launcher.helper_failed", map[string]any{"helper_port": helperPort, "error": err.Error()})
			return err
		}
		defer runtimeState.shutdownHelper()
	}
	if activeRelayProfile(settings).needsLocalRelayProxy() {
		if err := runtimeState.startRelayProxy(localRelayProxyPort); err != nil {
			failure := launchStatus{
				Status:      "failed",
				Message:     "启动 Codex++ 本地中转代理失败：" + err.Error(),
				StartedAtMS: uint64(time.Now().UnixMilli()),
				DebugPort:   &debugPort,
				HelperPort:  &helperPort,
				CodexApp:    &appPath,
			}
			_ = atomicWriteJSON(latestStatusPath(), failure)
			appendDiagnosticLog("launcher.relay_proxy_failed", map[string]any{"port": localRelayProxyPort, "error": err.Error()})
			return err
		}
		defer runtimeState.shutdownRelayProxy()
	}
	status := launchStatus{
		Status:      "starting",
		Message:     "Codex++ launcher starting Codex and waiting for injection.",
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    &appPath,
	}
	_ = atomicWriteJSON(latestStatusPath(), status)
	appendDiagnosticLog("launcher.starting", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath, "enhancements": settings.Enhancements})

	command := buildCodexLaunchCommand(appPath, debugPort, settings.CodexExtraArgs)
	if len(command) == 0 {
		return errors.New("无法构建 Codex 启动命令")
	}
	if runtime.GOOS == "windows" && !fileExists(command[0]) {
		err := fmt.Errorf("未找到 Codex.exe：%s", command[0])
		failure := launchStatus{
			Status:      "failed",
			Message:     "启动 Codex 失败：" + err.Error(),
			StartedAtMS: uint64(time.Now().UnixMilli()),
			DebugPort:   &debugPort,
			HelperPort:  &helperPort,
			CodexApp:    &appPath,
		}
		_ = atomicWriteJSON(latestStatusPath(), failure)
		appendDiagnosticLog("launcher.codex_executable_missing", map[string]any{"command": safeCommandForLog(command), "codex_app": appPath})
		return err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), codexLaunchEnvironment()...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	hideSubprocessWindow(cmd)
	if err := cmd.Start(); err != nil {
		failure := launchStatus{
			Status:      "failed",
			Message:     "启动 Codex 失败：" + err.Error(),
			StartedAtMS: uint64(time.Now().UnixMilli()),
			DebugPort:   &debugPort,
			HelperPort:  &helperPort,
			CodexApp:    &appPath,
		}
		_ = atomicWriteJSON(latestStatusPath(), failure)
		appendDiagnosticLog("launcher.codex_start_failed", map[string]any{"error": err.Error(), "command": safeCommandForLog(command)})
		return err
	}
	ready := launchStatus{
		Status:      "running",
		Message:     "Codex++ launcher ready.",
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    &appPath,
	}
	if settings.Enhancements {
		if err := runtimeState.retryInjection(helperPort); err != nil {
			ready.Status = "degraded"
			ready.Message = "Codex 已启动，但 Codex++ 增强菜单暂时注入失败；中转代理会继续运行，并在后台重试注入：" + err.Error()
			appendDiagnosticLog("launcher.inject_degraded", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "error": err.Error()})
		}
		go runtimeState.bridgeWatchdog(helperPort)
	}
	_ = atomicWriteJSON(latestStatusPath(), ready)
	appendDiagnosticLog("launcher.ready", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
	return reapLauncherChild(cmd, appPath, debugPort, helperPort)
}

func parseLaunchRequest(args []string) launchRequest {
	var request launchRequest
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-path":
			if i+1 < len(args) {
				request.appPath = strings.TrimSpace(args[i+1])
				i++
			}
		case "--debug-port":
			if i+1 < len(args) {
				if value, err := strconv.ParseUint(args[i+1], 10, 16); err == nil {
					request.debugPort = uint16(value)
				}
				i++
			}
		case "--helper-port":
			if i+1 < len(args) {
				if value, err := strconv.ParseUint(args[i+1], 10, 16); err == nil {
					request.helperPort = uint16(value)
				}
				i++
			}
		case "--restart":
			request.restart = true
		}
	}
	return request
}

func buildCodexLaunchCommand(appPath string, debugPort uint16, extraArgs []string) []string {
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
		fmt.Sprintf("--remote-allow-origins=http://127.0.0.1:%d", debugPort),
	}
	args = append(args, normalizeExtraArgs(extraArgs)...)
	if runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(appPath), ".app") {
		command := []string{"open", "-W", "-a", appPath, "--args"}
		return append(command, args...)
	}
	executable := buildCodexExecutable(appPath)
	return append([]string{executable}, args...)
}

func buildCodexExecutable(appPath string) string {
	if runtime.GOOS == "windows" {
		if isWindowsAppsExecutionAlias(appPath) && fileExists(appPath) {
			return appPath
		}
		if strings.EqualFold(filepath.Base(appPath), "Codex.exe") || strings.EqualFold(filepath.Base(appPath), "codex.exe") {
			if fileExists(appPath) {
				return appPath
			}
		}
		candidates := []string{
			filepath.Join(appPath, "Codex.exe"),
			filepath.Join(appPath, "codex.exe"),
			filepath.Join(appPath, "app", "Codex.exe"),
			filepath.Join(appPath, "app", "codex.exe"),
			filepath.Join(appPath, "VFS", "ProgramFilesX64", "Codex", "Codex.exe"),
			filepath.Join(appPath, "VFS", "ProgramFilesX64", "Codex", "codex.exe"),
			filepath.Join(appPath, "VFS", "ProgramFilesX64", "OpenAI", "Codex", "Codex.exe"),
			filepath.Join(appPath, "VFS", "ProgramFilesX64", "OpenAI", "Codex", "codex.exe"),
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	if strings.EqualFold(filepath.Ext(appPath), ".app") {
		name := strings.TrimSuffix(filepath.Base(appPath), ".app")
		candidates := []string{
			filepath.Join(appPath, "Contents", "MacOS", name),
			filepath.Join(appPath, "Contents", "MacOS", "Codex"),
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return appPath
}

func codexLaunchEnvironment() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"PATH=" + defaultGUIPath}
	default:
		return nil
	}
}

func shouldQuitRunningCodexBeforeLaunch(appPath string, debugPort uint16, restart bool) bool {
	if runtime.GOOS != "darwin" || !strings.EqualFold(filepath.Ext(appPath), ".app") {
		return false
	}
	if !macOSAppRunning(appPath) {
		return false
	}
	if restart {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	if _, err := listCDPTargets(ctx, debugPort); err == nil {
		return false
	}
	return true
}

func waitForTCPPortFree(port uint16, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		listener, err := net.Listen("tcp", address)
		if err == nil {
			_ = listener.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func macOSAppRunning(appPath string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if strings.TrimSpace(name) == "" {
		name = "Codex"
	}
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`application "%s" is running`, strings.ReplaceAll(name, `"`, `\"`)))
	hideSubprocessWindow(cmd)
	out, err := cmd.Output()
	return err == nil && strings.EqualFold(strings.TrimSpace(string(out)), "true")
}

func quitMacOSApp(appPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if strings.TrimSpace(name) == "" {
		name = "Codex"
	}
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`tell application "%s" to quit`, strings.ReplaceAll(name, `"`, `\"`)))
	hideSubprocessWindow(cmd)
	return cmd.Run()
}

func waitForMacOSAppExit(appPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !macOSAppRunning(appPath) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return !macOSAppRunning(appPath)
}

func forceKillMacOSApp(appPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	executable := buildCodexExecutable(appPath)
	if executable != "" {
		cmd := exec.Command("pkill", "-x", filepath.Base(executable))
		hideSubprocessWindow(cmd)
		_ = cmd.Run()
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if strings.TrimSpace(name) != "" {
		cmd := exec.Command("pkill", "-x", name)
		hideSubprocessWindow(cmd)
		_ = cmd.Run()
	}
	return nil
}

func terminateLaunchedCodex(cmd *exec.Cmd, command []string, appPath string) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if runtime.GOOS == "darwin" && len(command) > 0 && command[0] == "open" {
		_ = quitMacOSApp(appPath)
	}
}

func safeCommandForLog(command []string) []string {
	out := append([]string(nil), command...)
	for i, part := range out {
		if strings.Contains(strings.ToLower(part), "key") || strings.Contains(strings.ToLower(part), "token") {
			out[i] = "[redacted]"
		}
	}
	return out
}

func reapLauncherChild(cmd *exec.Cmd, appPath string, debugPort, helperPort uint16) error {
	err := cmd.Wait()
	message := "Codex exited."
	statusText := "exited"
	if err != nil {
		message = "Codex exited with error: " + err.Error()
		statusText = "failed"
	}
	status := launchStatus{
		Status:      statusText,
		Message:     message,
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    &appPath,
	}
	_ = atomicWriteJSON(latestStatusPath(), status)
	appendDiagnosticLog("launcher."+statusText, map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath, "message": message})
	return err
}

func helperNeeded(settings backendSettings) bool {
	return settings.Enhancements || activeRelayProfile(settings).Protocol == "chatCompletions" || activeRelayProfile(settings).needsLocalRelayProxy()
}
