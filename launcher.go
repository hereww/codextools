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

	"github.com/gorilla/websocket"
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
	debugPort := options.debugPort
	if debugPort == 0 {
		debugPort = 9229
	}
	helperPort := options.helperPort
	if helperPort == 0 {
		helperPort = 57321
	}
	if appPath == "" {
		err := errors.New("未找到 Codex 安装目录，请先在管理器中设置 Codex App 路径")
		writeLaunchFailureStatus("启动 Codex 失败："+err.Error(), debugPort, helperPort, nil)
		appendDiagnosticLog("launcher.codex_app_missing", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "error": err.Error()})
		return err
	}
	singleInstanceLock, acquired, err := acquireLauncherSingleInstanceLock(debugPort)
	if err != nil {
		appendDiagnosticLog("launcher.single_instance_lock_failed", map[string]any{"debug_port": debugPort, "error": err.Error()})
	} else if !acquired {
		if !options.restart {
			status := launchStatus{
				Status:      "running",
				Message:     "Codex++ launcher 已在运行，已跳过本次重复启动。",
				StartedAtMS: uint64(time.Now().UnixMilli()),
				DebugPort:   &debugPort,
				HelperPort:  &helperPort,
				CodexApp:    &appPath,
			}
			_ = atomicWriteJSON(latestStatusPath(), status)
			appendDiagnosticLog("launcher.single_instance_skipped", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
			return nil
		}
		appendDiagnosticLog("launcher.restart_existing_codex", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
		if err := requestCodexShutdownViaCDP(debugPort, 10*time.Second); err != nil {
			writeLaunchFailureStatus("重启 Codex 失败："+err.Error(), debugPort, helperPort, &appPath)
			appendDiagnosticLog("launcher.restart_existing_codex_failed", map[string]any{"debug_port": debugPort, "error": err.Error()})
			return err
		}
		if helperNeeded(settings) {
			waitForTCPPortFree(helperPort, 5*time.Second)
		}
		if activeRelayProfile(settings).needsLocalRelayProxy() {
			waitForTCPPortFree(localRelayProxyPort, 5*time.Second)
		}
		singleInstanceLock, acquired, err = acquireLauncherSingleInstanceLock(debugPort)
		if err != nil {
			appendDiagnosticLog("launcher.single_instance_relock_failed", map[string]any{"debug_port": debugPort, "error": err.Error()})
		} else if !acquired {
			writeLaunchFailureStatus("重启 Codex 失败：旧启动器尚未退出，请关闭 Codex 后重试。", debugPort, helperPort, &appPath)
			appendDiagnosticLog("launcher.single_instance_restart_skipped", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
			return nil
		}
	}
	if singleInstanceLock != nil {
		defer singleInstanceLock.release()
	}
	if runtime.GOOS == "windows" && options.restart && cdpTargetsAvailable(debugPort, 800*time.Millisecond) {
		appendDiagnosticLog("launcher.restart_running_codex", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
		if err := requestCodexShutdownViaCDP(debugPort, 10*time.Second); err != nil {
			writeLaunchFailureStatus("重启 Codex 失败："+err.Error(), debugPort, helperPort, &appPath)
			appendDiagnosticLog("launcher.restart_running_codex_failed", map[string]any{"debug_port": debugPort, "error": err.Error()})
			return err
		}
		if helperNeeded(settings) {
			waitForTCPPortFree(helperPort, 5*time.Second)
		}
		if activeRelayProfile(settings).needsLocalRelayProxy() {
			waitForTCPPortFree(localRelayProxyPort, 5*time.Second)
		}
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

	attachExisting := shouldAttachToRunningCodex(debugPort, options.restart)
	launch := codexLaunchHandle(&existingCodexLaunch{debugPort: debugPort})
	if !attachExisting {
		var err error
		launch, err = startCodexApp(appPath, debugPort, settings.CodexExtraArgs)
		if err != nil {
			failure := launchStatus{
				Status:      "failed",
				Message:     "启动 Codex 失败：" + err.Error(),
				StartedAtMS: uint64(time.Now().UnixMilli()),
				DebugPort:   &debugPort,
				HelperPort:  &helperPort,
				CodexApp:    &appPath,
			}
			_ = atomicWriteJSON(latestStatusPath(), failure)
			appendDiagnosticLog("launcher.codex_start_failed", map[string]any{"error": err.Error(), "codex_app": appPath})
			return err
		}
	} else {
		appendDiagnosticLog("launcher.attach_existing_codex", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
	}
	ready := launchStatus{
		Status:      "running",
		Message:     "Codex++ launcher ready.",
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    &appPath,
	}
	if attachExisting {
		ready.Message = "Codex++ launcher 已附着到现有 Codex 进程。"
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
	appendDiagnosticLog("launcher.ready", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath, "launch": launch.logPayload()})
	return reapLauncherChild(launch, appPath, debugPort, helperPort)
}

func writeLaunchFailureStatus(message string, debugPort, helperPort uint16, appPath *string) {
	failure := launchStatus{
		Status:      "failed",
		Message:     message,
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    appPath,
	}
	_ = atomicWriteJSON(latestStatusPath(), failure)
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
	args := buildCodexArguments(debugPort, extraArgs)
	if runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(appPath), ".app") {
		command := []string{"open", "-W", "-a", appPath, "--args"}
		return append(command, args...)
	}
	executable := buildCodexExecutable(appPath)
	return append([]string{executable}, args...)
}

func buildCodexArguments(debugPort uint16, extraArgs []string) []string {
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
		fmt.Sprintf("--remote-allow-origins=http://127.0.0.1:%d", debugPort),
	}
	return append(args, normalizeExtraArgs(extraArgs)...)
}

func buildCodexExecutable(appPath string) string {
	if runtime.GOOS == "windows" {
		if isWindowsAppsExecutionAlias(appPath) {
			return appPath
		}
		if strings.EqualFold(filepath.Base(appPath), "Codex.exe") || strings.EqualFold(filepath.Base(appPath), "codex.exe") {
			return appPath
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
		if packagedWindowsAppUserModelID(appPath) != "" {
			return ""
		}
		return ""
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

func startCodexApp(appPath string, debugPort uint16, extraArgs []string) (codexLaunchHandle, error) {
	command := buildCodexLaunchCommand(appPath, debugPort, extraArgs)
	if runtime.GOOS == "windows" {
		if len(command) > 0 && strings.TrimSpace(command[0]) != "" && fileExists(command[0]) {
			handle, err := startCodexProcess(command)
			if err == nil {
				return handle, nil
			}
			if buildWindowsPackagedActivation(appPath, debugPort, extraArgs) == nil {
				return nil, err
			}
			appendDiagnosticLog("launcher.windows_direct_start_failed", map[string]any{"command": safeCommandForLog(command), "error": err.Error()})
		}
		if activation := buildWindowsPackagedActivation(appPath, debugPort, extraArgs); activation != nil {
			processID, activationErr := activateWindowsPackagedAppWithEnvironment(activation.appUserModelID, activation.arguments, codexLaunchEnvironment())
			if activationErr == nil {
				activation.processID = processID
				return activation, nil
			}
			explorerHandle, explorerErr := startWindowsPackagedAppViaExplorer(activation.appUserModelID, debugPort, buildCodexArguments(debugPort, extraArgs))
			if explorerErr == nil {
				return explorerHandle, nil
			}
			if len(command) == 0 || strings.TrimSpace(command[0]) == "" || !fileExists(command[0]) {
				return nil, fmt.Errorf("无法激活 Windows Codex 应用 %s：%w；explorer 兜底也失败：%v", activation.appUserModelID, activationErr, explorerErr)
			}
			handle, err := startCodexProcess(command)
			if err == nil {
				return handle, nil
			}
			return nil, fmt.Errorf("MSIX 激活 %s 失败：%v；explorer 兜底失败：%v；直接启动 %s 也失败：%w", activation.appUserModelID, activationErr, explorerErr, command[0], err)
		}
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("未找到 Codex.exe：%s", appPath)
	}
	if runtime.GOOS == "windows" && !isWindowsAppsExecutionAlias(command[0]) && !fileExists(command[0]) {
		return nil, fmt.Errorf("未找到 Codex.exe：%s", appPath)
	}
	handle, err := startCodexProcess(command)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

func buildWindowsPackagedActivation(appPath string, debugPort uint16, extraArgs []string) *windowsPackagedActivation {
	if runtime.GOOS != "windows" {
		return nil
	}
	appUserModelID := packagedWindowsAppUserModelID(appPath)
	if appUserModelID == "" {
		return nil
	}
	return &windowsPackagedActivation{
		appUserModelID: appUserModelID,
		arguments:      commandLineArguments(buildCodexArguments(debugPort, extraArgs)),
		debugPort:      debugPort,
	}
}

func windowsPackagedExplorerCommand(appUserModelID string, args []string) []string {
	command := []string{"explorer.exe", `shell:AppsFolder\` + appUserModelID}
	return append(command, args...)
}

func startWindowsPackagedAppViaExplorer(appUserModelID string, debugPort uint16, args []string) (codexLaunchHandle, error) {
	command := windowsPackagedExplorerCommand(appUserModelID, args)
	handle, err := startCodexProcess(command)
	if err != nil {
		return nil, err
	}
	return &windowsPackagedExplorerLaunch{codexLaunchHandle: handle, appUserModelID: appUserModelID, command: command, debugPort: debugPort}, nil
}

func commandLineArguments(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteWindowsArgument(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteWindowsArgument(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	var output strings.Builder
	output.WriteByte('"')
	backslashes := 0
	for _, ch := range arg {
		switch ch {
		case '\\':
			backslashes++
		case '"':
			output.WriteString(strings.Repeat("\\", backslashes*2+1))
			output.WriteRune(ch)
			backslashes = 0
		default:
			output.WriteString(strings.Repeat("\\", backslashes))
			output.WriteRune(ch)
			backslashes = 0
		}
	}
	output.WriteString(strings.Repeat("\\", backslashes*2))
	output.WriteByte('"')
	return output.String()
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

func shouldAttachToRunningCodex(debugPort uint16, restart bool) bool {
	return runtime.GOOS == "windows" && !restart && cdpTargetsAvailable(debugPort, 1200*time.Millisecond)
}

func cdpTargetsAvailable(debugPort uint16, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	targets, err := listCDPTargets(ctx, debugPort)
	return err == nil && len(targets) > 0
}

func requestCodexShutdownViaCDP(debugPort uint16, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	targets, err := listCDPTargets(ctx, debugPort)
	if err != nil {
		return fmt.Errorf("无法连接现有 Codex 调试端口 %d：%w", debugPort, err)
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"id": 1, "method": "Browser.close", "params": map[string]any{}}); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !tcpPortAccepting(debugPort) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 Codex 调试端口 %d 关闭超时", debugPort)
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

func terminateLaunchedCodex(launch codexLaunchHandle, appPath string) {
	if launch != nil {
		_ = launch.terminate()
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

func reapLauncherChild(launch codexLaunchHandle, appPath string, debugPort, helperPort uint16) error {
	err := launch.wait()
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

type existingCodexLaunch struct {
	debugPort uint16
}

func (launch *existingCodexLaunch) wait() error {
	for tcpPortAccepting(launch.debugPort) {
		time.Sleep(time.Second)
	}
	return nil
}

func (launch *existingCodexLaunch) terminate() error {
	return nil
}

func (launch *existingCodexLaunch) logPayload() map[string]any {
	return map[string]any{
		"type":       "existing_cdp",
		"debug_port": launch.debugPort,
	}
}

type codexLaunchHandle interface {
	wait() error
	terminate() error
	logPayload() map[string]any
}

type codexProcessLaunch struct {
	cmd     *exec.Cmd
	command []string
}

func startCodexProcess(command []string) (codexLaunchHandle, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), codexLaunchEnvironment()...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	hideSubprocessWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("无法启动 Codex 可执行文件 %s：%w", command[0], err)
	}
	return &codexProcessLaunch{cmd: cmd, command: append([]string(nil), command...)}, nil
}

func (launch *codexProcessLaunch) wait() error {
	return launch.cmd.Wait()
}

func (launch *codexProcessLaunch) terminate() error {
	if launch.cmd != nil && launch.cmd.Process != nil {
		return launch.cmd.Process.Kill()
	}
	return nil
}

func (launch *codexProcessLaunch) logPayload() map[string]any {
	return map[string]any{
		"type":    "process",
		"command": safeCommandForLog(launch.command),
	}
}

type windowsPackagedActivation struct {
	appUserModelID string
	arguments      string
	debugPort      uint16
	processID      uint32
}

type windowsPackagedExplorerLaunch struct {
	codexLaunchHandle
	appUserModelID string
	command        []string
	debugPort      uint16
}

func (launch *windowsPackagedExplorerLaunch) wait() error {
	if launch.codexLaunchHandle == nil {
		return nil
	}
	_ = launch.codexLaunchHandle.wait()
	if !waitForCDPPortAvailable(launch.debugPort, 30*time.Second) {
		return fmt.Errorf("explorer 已请求激活 %s，但未检测到 Codex 调试端口 %d", launch.appUserModelID, launch.debugPort)
	}
	for {
		if !tcpPortAccepting(launch.debugPort) {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func (launch *windowsPackagedExplorerLaunch) terminate() error {
	if launch.codexLaunchHandle == nil {
		return nil
	}
	return launch.codexLaunchHandle.terminate()
}

func (launch *windowsPackagedExplorerLaunch) logPayload() map[string]any {
	return map[string]any{
		"type":           "windows_packaged_explorer",
		"appUserModelId": launch.appUserModelID,
		"command":        safeCommandForLog(launch.command),
	}
}

func waitForCDPPortAvailable(port uint16, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tcpPortAccepting(port) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return tcpPortAccepting(port)
}

func tcpPortAccepting(port uint16) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (activation *windowsPackagedActivation) wait() error {
	if activation.processID == 0 {
		if !waitForCDPPortAvailable(activation.debugPort, 30*time.Second) {
			return fmt.Errorf("MSIX 已请求激活 %s，但未检测到 Codex 调试端口 %d", activation.appUserModelID, activation.debugPort)
		}
		for {
			if !tcpPortAccepting(activation.debugPort) {
				return nil
			}
			time.Sleep(time.Second)
		}
	}
	return waitForWindowsProcessID(activation.processID)
}

func (activation *windowsPackagedActivation) terminate() error {
	return terminateWindowsProcessID(activation.processID)
}

func (activation *windowsPackagedActivation) logPayload() map[string]any {
	return map[string]any{
		"type":           "windows_packaged_activation",
		"appUserModelId": activation.appUserModelID,
		"arguments":      activation.arguments,
		"processId":      activation.processID,
	}
}
