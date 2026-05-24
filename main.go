package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
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
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	appName                  = "CodexTools"
	silentName               = "Codex++"
	managerName              = "Codex++ 管理工具"
	silentBinary             = "codextools-launcher"
	managerBinary            = "codextools"
	version                  = "1.1.7"
	stateDirName             = ".codex-session-delete"
	settingsFileName         = "settings.json"
	latestStatusFileName     = "latest-status.json"
	diagnosticLogFileName    = "codex-plus.log"
	relayProvider            = "CodexPlusPlus"
	legacyRelayProvider      = "CodexPP"
	localRelayProxyPort      = 57323
	protocolProxyBaseURL     = "http://127.0.0.1:57321/v1"
	scriptMarketIndexURL     = "https://raw.githubusercontent.com/BigPizzaV3/CodexPlusPlusScriptMarket/main/index.json"
	codexAppMirrorAPIURL     = "https://api.github.com/repos/Wangnov/codex-app-mirror/releases/latest"
	codexAppMirrorReleaseURL = "https://github.com/Wangnov/codex-app-mirror/releases/latest"
	codexAppMirrorProjectURL = "https://github.com/Wangnov/codex-app-mirror"
	codexOfficialInstallURL  = "https://openai.com/codex/"
	defaultRelayTestModel    = "gpt-5-mini"
	defaultAPIKeyEnvironment = "CUSTOM_OPENAI_API_KEY"
	defaultGUIPath           = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	cdpConnectTimeout        = 5 * time.Second
	cdpCommandTimeout        = 5 * time.Second
	launcherCheckInterval    = 5 * time.Second
	bridgeBindingName        = "codexSessionDeleteV2"
	defaultWatcherDebugPort  = 9229
	watcherRunName           = "CodexPlusPlusWatcher"
	watcherRunKey            = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	watcherStartupLinkName   = "CodexPlusPlusWatcher.lnk"
)

var binaryRole = "manager"

//go:embed all:web/dist
var embeddedDist embed.FS

//go:embed assets/inject/renderer-inject.js
var rendererInjectScript string

type commandResult map[string]any

type backendSettings struct {
	CodexAppPath        string         `json:"codexAppPath"`
	CodexExtraArgs      []string       `json:"codexExtraArgs"`
	ProviderSync        bool           `json:"providerSyncEnabled"`
	Enhancements        bool           `json:"enhancementsEnabled"`
	LaunchMode          string         `json:"launchMode"`
	RelayBaseURL        string         `json:"relayBaseUrl"`
	RelayAPIKey         string         `json:"relayApiKey"`
	RelayProfiles       []relayProfile `json:"relayProfiles"`
	ActiveRelayID       string         `json:"activeRelayId"`
	RelayTestModel      string         `json:"relayTestModel"`
	CLIWrapperEnabled   bool           `json:"cliWrapperEnabled"`
	CLIWrapperBaseURL   string         `json:"cliWrapperBaseUrl"`
	CLIWrapperAPIKey    string         `json:"cliWrapperApiKey"`
	CLIWrapperAPIKeyEnv string         `json:"cliWrapperApiKeyEnv"`
}

type relayProfile struct {
	ID                            string `json:"id"`
	Name                          string `json:"name"`
	BaseURL                       string `json:"baseUrl"`
	APIKey                        string `json:"apiKey"`
	ImageGenerationEnabled        bool   `json:"imageGenerationEnabled"`
	ImageGenerationUseSeparateAPI bool   `json:"imageGenerationUseSeparateApi"`
	ImageGenerationBaseURL        string `json:"imageGenerationBaseUrl"`
	ImageGenerationAPIKey         string `json:"imageGenerationApiKey"`
	Protocol                      string `json:"protocol"`
	RelayMode                     string `json:"relayMode"`
	OfficialMixAPIKey             bool   `json:"officialMixApiKey"`
	TestModel                     string `json:"testModel"`
	ConfigContents                string `json:"configContents"`
	AuthContents                  string `json:"authContents"`
}

type launchStatus struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	StartedAtMS uint64  `json:"started_at_ms"`
	DebugPort   *uint16 `json:"debug_port"`
	HelperPort  *uint16 `json:"helper_port"`
	CodexApp    *string `json:"codex_app"`
}

type launcherRuntime struct {
	settings  backendSettings
	debugPort uint16
	helper    *http.Server
	relay     *http.Server
	helperURL string
	relayURL  string
}

type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpResponse struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type bridgePayload struct {
	ID      string          `json:"id"`
	Path    string          `json:"path"`
	Payload json.RawMessage `json:"payload"`
}

type userScriptInventory struct {
	Enabled    bool                      `json:"enabled"`
	BuiltinDir string                    `json:"builtin_dir"`
	UserDir    string                    `json:"user_dir"`
	Scripts    []userScriptInventoryItem `json:"scripts"`
	Error      string                    `json:"error,omitempty"`
}

type userScriptInventoryItem struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Enabled   bool   `json:"enabled"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	MarketID  string `json:"market_id,omitempty"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Homepage  string `json:"homepage,omitempty"`
}

type userScriptConfig struct {
	Enabled bool                           `json:"enabled"`
	Scripts map[string]bool                `json:"scripts"`
	Market  map[string]marketScriptInstall `json:"market,omitempty"`
}

type marketScriptInstall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	ScriptURL   string `json:"script_url"`
	Homepage    string `json:"homepage"`
	InstalledAt string `json:"installed_at"`
}

type providerSyncResult struct {
	Status              string  `json:"syncStatus"`
	Message             string  `json:"syncMessage"`
	TargetProvider      string  `json:"targetProvider"`
	BackupDir           *string `json:"backupDir"`
	ChangedSessionFiles int     `json:"changedSessionFiles"`
	SQLiteRowsUpdated   int     `json:"sqliteRowsUpdated"`
}

type sessionChange struct {
	Path              string
	OriginalFirstLine string
	NextFirstLine     string
	Separator         string
	ThreadID          string
	CWD               string
	HasUserEvent      bool
	RewriteNeeded     bool
}

type marketManifest struct {
	Version   uint64         `json:"version"`
	UpdatedAt string         `json:"updated_at"`
	Scripts   []marketScript `json:"scripts"`
}

type marketScript struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Homepage    string   `json:"homepage"`
	ScriptURL   string   `json:"script_url"`
	SHA256      string   `json:"sha256"`
}

type ccsProviderImport struct {
	SourceID       string `json:"sourceId"`
	Name           string `json:"name"`
	BaseURL        string `json:"baseUrl"`
	APIKey         string `json:"apiKey"`
	Protocol       string `json:"protocol"`
	ConfigContents string `json:"configContents"`
	AuthContents   string `json:"authContents"`
}

type codexAppMirrorRelease struct {
	TagName     string                `json:"tag_name"`
	Name        string                `json:"name"`
	HTMLURL     string                `json:"html_url"`
	PublishedAt string                `json:"published_at"`
	Assets      []codexAppMirrorAsset `json:"assets"`
}

type codexAppMirrorAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

func main() {
	var err error
	if shouldRunLauncher(os.Args) {
		err = runLauncher(os.Args[1:])
	} else {
		if defaultManagerDesktop() {
			lockManagerDesktopThread()
		}
		err = runManager()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

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
	if settings.ProviderSync {
		result := runProviderSync(codexHomeDir())
		appendDiagnosticLog("provider_sync."+result.Status, map[string]any{
			"targetProvider":      result.TargetProvider,
			"changedSessionFiles": result.ChangedSessionFiles,
			"sqliteRowsUpdated":   result.SQLiteRowsUpdated,
			"message":             result.Message,
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
	if shouldQuitRunningCodexBeforeLaunch(appPath, debugPort) {
		appendDiagnosticLog("launcher.quit_existing_codex", map[string]any{"codex_app": appPath, "debug_port": debugPort})
		if err := quitMacOSApp(appPath); err != nil {
			appendDiagnosticLog("launcher.quit_existing_codex_failed", map[string]any{"codex_app": appPath, "error": err.Error()})
		}
		if !waitForMacOSAppExit(appPath, 8*time.Second) {
			appendDiagnosticLog("launcher.force_kill_existing_codex", map[string]any{"codex_app": appPath})
			_ = forceKillMacOSApp(appPath)
			_ = waitForMacOSAppExit(appPath, 4*time.Second)
		}
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), codexLaunchEnvironment()...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
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
	if settings.Enhancements {
		if err := runtimeState.retryInjection(helperPort); err != nil {
			failure := launchStatus{
				Status:      "failed",
				Message:     "Codex++ 注入失败：" + err.Error(),
				StartedAtMS: uint64(time.Now().UnixMilli()),
				DebugPort:   &debugPort,
				HelperPort:  &helperPort,
				CodexApp:    &appPath,
			}
			_ = atomicWriteJSON(latestStatusPath(), failure)
			appendDiagnosticLog("launcher.inject_failed", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "error": err.Error()})
			terminateLaunchedCodex(cmd, command, appPath)
			_ = cmd.Wait()
			return err
		}
		go runtimeState.bridgeWatchdog(helperPort)
	}
	ready := launchStatus{
		Status:      "running",
		Message:     "Codex++ launcher ready.",
		StartedAtMS: uint64(time.Now().UnixMilli()),
		DebugPort:   &debugPort,
		HelperPort:  &helperPort,
		CodexApp:    &appPath,
	}
	_ = atomicWriteJSON(latestStatusPath(), ready)
	appendDiagnosticLog("launcher.ready", map[string]any{"debug_port": debugPort, "helper_port": helperPort, "codex_app": appPath})
	return reapLauncherChild(cmd, appPath, debugPort, helperPort)
}

type launchRequest struct {
	appPath    string
	debugPort  uint16
	helperPort uint16
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
		candidates := []string{
			filepath.Join(appPath, "Codex.exe"),
			filepath.Join(appPath, "codex.exe"),
			filepath.Join(appPath, "app", "Codex.exe"),
			filepath.Join(appPath, "app", "codex.exe"),
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

func shouldQuitRunningCodexBeforeLaunch(appPath string, debugPort uint16) bool {
	if runtime.GOOS != "darwin" || !strings.EqualFold(filepath.Ext(appPath), ".app") {
		return false
	}
	if !macOSAppRunning(appPath) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	if _, err := listCDPTargets(ctx, debugPort); err == nil {
		return false
	}
	return true
}

func macOSAppRunning(appPath string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if strings.TrimSpace(name) == "" {
		name = "Codex"
	}
	out, err := exec.Command("osascript", "-e", fmt.Sprintf(`application "%s" is running`, strings.ReplaceAll(name, `"`, `\"`))).Output()
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
	return exec.Command("osascript", "-e", fmt.Sprintf(`tell application "%s" to quit`, strings.ReplaceAll(name, `"`, `\"`))).Run()
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
		_ = exec.Command("pkill", "-x", filepath.Base(executable)).Run()
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if strings.TrimSpace(name) != "" {
		_ = exec.Command("pkill", "-x", name).Run()
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

func (r relayProfile) needsLocalRelayProxy() bool {
	return r.Protocol == "responses" && (disablesImageGeneration(r) || usesSeparateImageGenerationAPI(r))
}

func (r *launcherRuntime) startHelper(helperPort uint16) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handleHelperHTTP)
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", helperPort), Handler: mux}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	r.helper = server
	r.helperURL = "http://" + server.Addr
	appendDiagnosticLog("helper.listening", map[string]any{"helper_port": helperPort, "address": r.helperURL})
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appendDiagnosticLog("helper.failed", map[string]any{"helper_port": helperPort, "error": err.Error()})
		}
	}()
	return nil
}

func (r *launcherRuntime) shutdownHelper() {
	if r.helper == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.helper.Shutdown(ctx)
	appendDiagnosticLog("helper.shutdown", map[string]any{"address": r.helperURL})
}

func (r *launcherRuntime) startRelayProxy(port uint16) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions {
			writeCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
		_ = req.Body.Close()
		r.forwardRelayProxy(w, req, body)
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	r.relay = server
	r.relayURL = "http://" + server.Addr
	appendDiagnosticLog("relay_proxy.listening", map[string]any{"port": port, "address": r.relayURL})
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appendDiagnosticLog("relay_proxy.failed", map[string]any{"port": port, "error": err.Error()})
		}
	}()
	return nil
}

func (r *launcherRuntime) shutdownRelayProxy() {
	if r.relay == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.relay.Shutdown(ctx)
	appendDiagnosticLog("relay_proxy.shutdown", map[string]any{"address": r.relayURL})
}

func (r *launcherRuntime) handleHelperHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodOptions {
		writeCORSHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(req.Body, 32*1024*1024))
	_ = req.Body.Close()
	appendDiagnosticLog("helper.request", map[string]any{
		"method":     req.Method,
		"path":       req.URL.Path,
		"body_bytes": len(body),
		"remote":     req.RemoteAddr,
	})
	switch req.URL.Path {
	case "/backend/status", "/backend/repair":
		writeHelperJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "后端已连接", "version": version, "transport": "http-helper"})
	case "/diagnostics/log":
		payload := json.RawMessage(body)
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		r.logRendererDiagnostic(payload)
		writeHelperJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "日志已记录"})
	case "/v1/responses", "/responses", "/v1/responses/compact", "/responses/compact", "/v1/models", "/models":
		r.forwardRelayProxy(w, req, body)
	default:
		writeHelperJSON(w, http.StatusNotFound, map[string]any{"status": "failed", "message": "未知后端路径"})
	}
}

func (r *launcherRuntime) forwardRelayProxy(w http.ResponseWriter, req *http.Request, body []byte) {
	profile := activeRelayProfile(r.settings)
	baseURL := strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
	apiKey := strings.TrimSpace(profile.APIKey)
	if profile.Protocol == "responses" && profile.needsLocalRelayProxy() {
		if isImageRequest(body) && usesSeparateImageGenerationAPI(profile) {
			baseURL = strings.TrimRight(strings.TrimSpace(profile.ImageGenerationBaseURL), "/")
			apiKey = strings.TrimSpace(profile.ImageGenerationAPIKey)
		}
	}
	if baseURL == "" || apiKey == "" {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "Codex++ relay proxy missing base URL or API key"}})
		return
	}
	target := relayTargetURL(baseURL, req.URL.Path)
	ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
	defer cancel()
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	upstreamReq.Header.Set("authorization", "Bearer "+apiKey)
	upstreamReq.Header.Set("user-agent", "CodexPlusPlus-GoRelay/"+version)
	copyProxyHeaders(req.Header, upstreamReq.Header)
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		writeHelperJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "Codex++ relay proxy request failed: " + err.Error()}})
		return
	}
	defer resp.Body.Close()
	writeCORSHeaders(w)
	for _, name := range []string{"content-type", "cache-control", "openai-request-id", "x-request-id"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	appendDiagnosticLog("relay_proxy.response", map[string]any{"path": req.URL.Path, "status": resp.StatusCode, "target": target})
}

func relayTargetURL(baseURL, path string) string {
	path = "/" + strings.TrimLeft(path, "/")
	switch {
	case strings.HasSuffix(path, "/responses") || path == "/responses":
		return baseURL + "/responses"
	case strings.HasSuffix(path, "/models") || path == "/models":
		return baseURL + "/models"
	default:
		return baseURL + path
	}
}

func copyProxyHeaders(source http.Header, target http.Header) {
	for name, values := range source {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "host" || lower == "connection" || lower == "content-length" {
			continue
		}
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

func isImageRequest(body []byte) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	tools, _ := value["tools"].([]any)
	for _, tool := range tools {
		if object, ok := tool.(map[string]any); ok {
			kind := strings.ToLower(stringFromAny(firstNonNil(object["type"], object["name"])))
			if strings.Contains(kind, "image") {
				return true
			}
		}
	}
	return strings.Contains(strings.ToLower(string(body)), "image_generation")
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (r *launcherRuntime) logRendererDiagnostic(raw json.RawMessage) {
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		detail = map[string]any{"parse_error": err.Error(), "raw_bytes": len(raw)}
	}
	event := sanitizeDiagnosticEvent(stringFromAny(detail["event"]))
	appendDiagnosticLog("renderer."+event, detail)
}

func writeCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-allow-methods", "GET, POST, OPTIONS")
	w.Header().Set("access-control-allow-headers", "Content-Type, Authorization")
}

func writeHelperJSON(w http.ResponseWriter, status int, value any) {
	writeCORSHeaders(w)
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func sanitizeDiagnosticEvent(event string) string {
	var b strings.Builder
	for _, ch := range event {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "event"
	}
	return out
}

func (r *launcherRuntime) handleBridgeRequest(path string, payload json.RawMessage) map[string]any {
	started := time.Now()
	var payloadMap map[string]any
	_ = json.Unmarshal(payload, &payloadMap)
	appendDiagnosticLog("bridge.request", map[string]any{"path": path, "payload_keys": mapKeys(payloadMap)})
	var result map[string]any
	switch path {
	case "/backend/status", "/backend/repair":
		result = map[string]any{"status": "ok", "message": "后端已连接", "version": version}
	case "/settings/get":
		result = bridgeSettingsValue(loadSettings())
	case "/settings/set":
		result = r.setBridgeSettings(payloadMap)
	case "/diagnostics/log":
		r.logRendererDiagnostic(payload)
		result = map[string]any{"status": "ok", "message": "日志已记录"}
	case "/user-scripts/list":
		result = userScriptInventoryValue()
	case "/user-scripts/set-enabled":
		config := loadUserScriptConfig()
		config.Enabled = boolFromAny(payloadMap["enabled"])
		if err := saveUserScriptConfig(config); err != nil {
			result = map[string]any{"status": "failed", "message": err.Error()}
		} else {
			result = userScriptInventoryValue()
		}
	case "/user-scripts/set-script-enabled":
		key := strings.TrimSpace(stringFromAny(payloadMap["key"]))
		if key == "" {
			result = map[string]any{"status": "failed", "message": "脚本 key 不能为空"}
			break
		}
		config := loadUserScriptConfig()
		config.Scripts[key] = boolFromAny(payloadMap["enabled"])
		if err := saveUserScriptConfig(config); err != nil {
			result = map[string]any{"status": "failed", "message": err.Error()}
		} else {
			result = userScriptInventoryValue()
		}
	case "/user-scripts/reload":
		bundle := enabledUserScriptBundle()
		if strings.TrimSpace(bundle) != "" {
			if _, err := r.evaluateOnCodex(bundle, false); err != nil {
				result = map[string]any{"status": "failed", "message": err.Error()}
				break
			}
		}
		result = userScriptInventoryValue()
	case "/devtools/open":
		result = r.openDevTools()
	case "/manager/open":
		if err := openManagerApp(); err != nil {
			result = map[string]any{"status": "failed", "message": "打开管理工具失败：" + err.Error()}
		} else {
			result = map[string]any{"status": "ok", "message": "管理工具已打开"}
		}
	case "/codex-model-catalog", "/codex-config-model":
		result = codexModelCatalogValue()
	case "/zed-remote/status":
		result = zedRemoteStatusValue()
	case "/zed-remote/resolve-host", "/zed-remote/fallback-request", "/zed-remote/open":
		result = map[string]any{"status": "failed", "message": "Go 管理器暂未实现 Zed Remote 桥接"}
	case "/delete", "/undo", "/export-markdown", "/archived-thread", "/move-thread-workspace", "/thread-sort-key", "/thread-sort-keys":
		result = unsupportedBridgeDataRoute(path, payloadMap)
	default:
		result = map[string]any{"status": "failed", "message": "Unknown bridge path", "path": path}
		appendDiagnosticLog("bridge.unknown_path", map[string]any{"path": path})
	}
	appendDiagnosticLog("bridge.response", map[string]any{
		"path":       path,
		"elapsed_ms": time.Since(started).Milliseconds(),
		"status":     stringFromAny(result["status"]),
	})
	return result
}

func mapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bridgeSettingsValue(settings backendSettings) map[string]any {
	return map[string]any{
		"providerSyncEnabled": settings.ProviderSync,
		"enhancementsEnabled": settings.Enhancements,
		"launchMode":          settings.LaunchMode,
	}
}

func (r *launcherRuntime) setBridgeSettings(payload map[string]any) map[string]any {
	settings := loadSettings()
	if _, ok := payload["providerSyncEnabled"]; ok {
		settings.ProviderSync = boolFromAny(payload["providerSyncEnabled"])
	}
	if _, ok := payload["enhancementsEnabled"]; ok {
		settings.Enhancements = boolFromAny(payload["enhancementsEnabled"])
	}
	if value := strings.TrimSpace(stringFromAny(payload["launchMode"])); value == "patch" || value == "relay" {
		settings.LaunchMode = value
	}
	if err := saveSettings(settings); err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	r.settings = settings
	result := bridgeSettingsValue(settings)
	result["status"] = "ok"
	return result
}

func enabledUserScriptBundle() string {
	config := loadUserScriptConfig()
	if !config.Enabled {
		return ""
	}
	var parts []string
	inventory := scanUserScripts()
	for _, item := range inventory.Scripts {
		if !item.Enabled {
			continue
		}
		var dir string
		switch item.Source {
		case "builtin":
			dir = inventory.BuiltinDir
		case "user":
			dir = inventory.UserDir
		default:
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, item.Name))
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("\n;(() => {\n%s\n})();\n", string(data)))
	}
	return strings.Join(parts, "\n")
}

func unsupportedBridgeDataRoute(path string, payload map[string]any) map[string]any {
	sessionID := stringFromAny(payload["session_id"])
	if path == "/thread-sort-key" {
		return map[string]any{"status": "ok", "session_id": sessionID}
	}
	if path == "/thread-sort-keys" {
		return map[string]any{"status": "ok", "sessions": []any{}}
	}
	return map[string]any{"status": "failed", "session_id": sessionID, "message": "Go 管理器暂未实现该页面数据操作：" + path}
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

func zedRemoteStatusValue() map[string]any {
	return map[string]any{
		"status":            "ok",
		"platformSupported": runtime.GOOS == "darwin",
		"zedAppFound":       fileExists("/Applications/Zed.app"),
		"zedCliFound":       executableInPath("zed") != "",
	}
}

func executableInPath(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH") + string(os.PathListSeparator) + defaultGUIPath) {
		if candidate := filepath.Join(dir, name); fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func codexModelCatalogValue() map[string]any {
	configPath := filepath.Join(codexHomeDir(), "config.toml")
	data, _ := os.ReadFile(configPath)
	contents := string(data)
	modelProvider := rootKeyString(contents, "model_provider")
	model := rootKeyString(contents, "model")
	defaultModel := rootKeyString(contents, "default_model")
	providerValues := tableValues(contents, "model_providers."+modelProvider)
	providerName := unquoteToml(providerValues["name"])
	if providerName == "" {
		providerName = modelProvider
	}
	models := uniqueStrings([]string{model, defaultModel, defaultRelayTestModel})
	if len(models) == 0 {
		models = []string{defaultRelayTestModel}
	}
	if defaultModel == "" {
		defaultModel = firstNonEmpty(model, models[0])
	}
	return map[string]any{
		"status":         "ok",
		"model":          model,
		"default_model":  defaultModel,
		"model_provider": modelProvider,
		"provider_name":  providerName,
		"models":         models,
		"sources":        []any{},
		"responses_api":  map[string]any{"status": "unknown", "endpoint": "", "message": ""},
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendDiagnosticLog(event string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	redacted := redactForLog(detail)
	record := map[string]any{
		"timestamp_ms": time.Now().UnixMilli(),
		"pid":          os.Getpid(),
		"event":        event,
		"detail":       redacted,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	path := diagnosticLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}

func redactForLog(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "authorization") || strings.Contains(lower, "secret") {
				out[key] = "[redacted]"
			} else {
				out[key] = redactForLog(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactForLog(item)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactForLog(item)
		}
		return out
	case string:
		if strings.HasPrefix(typed, "sk-") || strings.HasPrefix(typed, "Bearer ") {
			return "[redacted]"
		}
		return typed
	default:
		return typed
	}
}

func (r *launcherRuntime) retryInjection(helperPort uint16) error {
	var lastErr error
	for attempt := 1; attempt <= 24; attempt++ {
		if err := r.inject(helperPort); err != nil {
			lastErr = err
			appendDiagnosticLog("inject.retry", map[string]any{"attempt": attempt, "debug_port": r.debugPort, "helper_port": helperPort, "error": err.Error()})
			time.Sleep(500 * time.Millisecond)
			continue
		}
		appendDiagnosticLog("inject.ok", map[string]any{"debug_port": r.debugPort, "helper_port": helperPort, "attempt": attempt})
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("Codex injection failed")
	}
	return lastErr
}

func (r *launcherRuntime) inject(helperPort uint16) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	targets, err := listCDPTargets(ctx, r.debugPort)
	if err != nil {
		return err
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return err
	}
	if target.WebSocketDebuggerURL == "" {
		return errors.New("selected CDP target has no websocket URL")
	}
	return r.installBridge(ctx, target.WebSocketDebuggerURL, helperPort)
}

func (r *launcherRuntime) bridgeWatchdog(helperPort uint16) {
	ticker := time.NewTicker(launcherCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		ok, err := r.bridgeHealthy()
		if err != nil {
			appendDiagnosticLog("bridge.health_error", map[string]any{"error": err.Error()})
		}
		if ok {
			continue
		}
		appendDiagnosticLog("bridge.reinject_start", map[string]any{"debug_port": r.debugPort, "helper_port": helperPort})
		if err := r.retryInjection(helperPort); err != nil {
			appendDiagnosticLog("bridge.reinject_failed", map[string]any{"error": err.Error()})
		}
	}
}

func (r *launcherRuntime) bridgeHealthy() (bool, error) {
	result, err := r.evaluateOnCodex(bridgeHealthCheckScript(), true)
	if err != nil {
		return false, err
	}
	return cdpResultBool(result), nil
}

func (r *launcherRuntime) openDevTools() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	targets, err := listCDPTargets(ctx, r.debugPort)
	if err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return map[string]any{"status": "failed", "message": err.Error()}
	}
	if strings.TrimSpace(target.ID) == "" {
		return map[string]any{"status": "failed", "message": "CDP target id 为空"}
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/devtools/inspector.html?ws=127.0.0.1:%d/devtools/page/%s", r.debugPort, r.debugPort, target.ID)
	if err := openURL(url); err != nil {
		return map[string]any{"status": "failed", "message": "打开 DevTools 失败：" + err.Error(), "url": url}
	}
	return map[string]any{"status": "ok", "message": "DevTools 已打开", "url": url, "target_id": target.ID}
}

func (r *launcherRuntime) evaluateOnCodex(script string, awaitPromise bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targets, err := listCDPTargets(ctx, r.debugPort)
	if err != nil {
		return nil, err
	}
	target, err := pickCDPPageTarget(targets)
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	session := newCDPSession(conn, nil)
	return session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(script, awaitPromise))
}

func listCDPTargets(ctx context.Context, debugPort uint16) ([]cdpTarget, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json", debugPort), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("CDP target list HTTP %d", resp.StatusCode)
	}
	var targets []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func pickCDPPageTarget(targets []cdpTarget) (cdpTarget, error) {
	var fallback *cdpTarget
	for i := range targets {
		target := targets[i]
		if target.WebSocketDebuggerURL == "" {
			continue
		}
		if fallback == nil {
			fallback = &targets[i]
		}
		if target.Type == "page" {
			return target, nil
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return cdpTarget{}, errors.New("未找到可注入的 Codex CDP 页面 target")
}

func (r *launcherRuntime) installBridge(ctx context.Context, websocketURL string, helperPort uint16) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return err
	}
	handler := func(path string, payload json.RawMessage) map[string]any {
		return r.handleBridgeRequest(path, payload)
	}
	session := newCDPSession(conn, handler)
	if _, err := session.send(ctx, "Runtime.enable", map[string]any{}); err != nil {
		_ = conn.Close()
		return err
	}
	_, _ = session.send(ctx, "Runtime.removeBinding", map[string]any{"name": bridgeBindingName})
	if _, err := session.send(ctx, "Runtime.addBinding", map[string]any{"name": bridgeBindingName}); err != nil {
		_ = conn.Close()
		return err
	}
	bridge := bridgeScript(bridgeBindingName)
	if _, err := session.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": bridge}); err != nil {
		_ = conn.Close()
		return err
	}
	if _, err := session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(bridge, false)); err != nil {
		_ = conn.Close()
		return err
	}
	scripts := []string{injectionScript(helperPort)}
	if bundle := enabledUserScriptBundle(); strings.TrimSpace(bundle) != "" {
		scripts = append(scripts, bundle)
	}
	for _, script := range scripts {
		if _, err := session.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}); err != nil {
			_ = conn.Close()
			return err
		}
		if _, err := session.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(script, false)); err != nil {
			_ = conn.Close()
			return err
		}
	}
	return nil
}

func runtimeEvaluateParams(script string, awaitPromise bool) map[string]any {
	return map[string]any{"expression": script, "awaitPromise": awaitPromise, "allowUnsafeEvalBlockedByCSP": true}
}

func injectionScript(helperPort uint16) string {
	helperURL := fmt.Sprintf("http://127.0.0.1:%d", helperPort)
	helperJSON, _ := json.Marshal(helperURL)
	versionJSON, _ := json.Marshal(version)
	buildJSON, _ := json.Marshal("go-20260524-1")
	return fmt.Sprintf("window.__CODEX_SESSION_DELETE_HELPER__ = %s;\nwindow.__CODEX_PLUS_VERSION__ = %s;\nwindow.__CODEX_PLUS_BUILD__ = %s;\n%s", helperJSON, versionJSON, buildJSON, rendererInjectScript)
}

func bridgeScript(bindingName string) string {
	return fmt.Sprintf(`
(() => {
  window.__codexSessionDeleteCallbacks = new Map();
  window.__codexSessionDeleteSeq = 0;
  window.__codexSessionDeleteResolve = (id, result) => {
    const callback = window.__codexSessionDeleteCallbacks.get(id);
    if (!callback) return;
    window.__codexSessionDeleteCallbacks.delete(id);
    callback.resolve(result);
  };
  window.__codexSessionDeleteReject = (id, message) => {
    const callback = window.__codexSessionDeleteCallbacks.get(id);
    if (!callback) return;
    window.__codexSessionDeleteCallbacks.delete(id);
    callback.resolve({ status: "failed", message });
  };
  window.__codexSessionDeleteBridge = (path, payload) => new Promise((resolve) => {
    const id = String(++window.__codexSessionDeleteSeq);
    window.__codexSessionDeleteCallbacks.set(id, { resolve });
    window.%s(JSON.stringify({ id, path, payload }));
  });
})();
`, bindingName)
}

func bridgeHealthCheckScript() string {
	return `
(() => {
  const bridge = window.__codexSessionDeleteBridge;
  if (typeof bridge !== "function") return false;
  try {
    return Promise.race([
      Promise.resolve(bridge("/backend/status", {})).then((result) => !!result && result.status === "ok"),
      new Promise((resolve) => setTimeout(() => resolve(false), 2000)),
    ]);
  } catch (error) {
    return false;
  }
})()
`
}

func cdpResultBool(result json.RawMessage) bool {
	var envelope struct {
		Result struct {
			Type  string `json:"type"`
			Value bool   `json:"value"`
		} `json:"result"`
	}
	return json.Unmarshal(result, &envelope) == nil && envelope.Result.Value
}

type cdpSession struct {
	conn    *websocket.Conn
	handler func(string, json.RawMessage) map[string]any
	nextID  int64
	pending map[int64]chan cdpResponse
	mu      sync.Mutex
	writeMu sync.Mutex
}

func newCDPSession(conn *websocket.Conn, handler func(string, json.RawMessage) map[string]any) *cdpSession {
	session := &cdpSession{
		conn:    conn,
		handler: handler,
		nextID:  1,
		pending: map[int64]chan cdpResponse{},
	}
	go session.readLoop()
	return session
}

func (s *cdpSession) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	responseCh := make(chan cdpResponse, 1)
	s.pending[id] = responseCh
	s.mu.Unlock()
	payload := map[string]any{"id": id, "method": method, "params": params}
	s.writeMu.Lock()
	if err := s.conn.WriteJSON(payload); err != nil {
		s.writeMu.Unlock()
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	s.writeMu.Unlock()
	timer := time.NewTimer(cdpCommandTimeout)
	defer timer.Stop()
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return nil, fmt.Errorf("CDP command %s failed: %s", method, response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-timer.C:
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("timed out waiting for CDP command %s", method)
	}
}

func (s *cdpSession) readLoop() {
	defer s.conn.Close()
	for {
		var response cdpResponse
		if err := s.conn.ReadJSON(&response); err != nil {
			return
		}
		if response.ID != 0 {
			s.mu.Lock()
			if ch, ok := s.pending[response.ID]; ok {
				delete(s.pending, response.ID)
				s.mu.Unlock()
				ch <- response
			} else {
				s.mu.Unlock()
			}
			continue
		}
		if response.Method == "Runtime.bindingCalled" && s.handler != nil {
			go s.handleBinding(response.Params)
			continue
		}
	}
}

func (s *cdpSession) handleBinding(params json.RawMessage) {
	var raw struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		appendDiagnosticLog("bridge.payload_parse_failed", map[string]any{"error": err.Error()})
		return
	}
	var payload bridgePayload
	if err := json.Unmarshal([]byte(raw.Payload), &payload); err != nil {
		appendDiagnosticLog("bridge.payload_parse_failed", map[string]any{"error": err.Error()})
		return
	}
	result := s.handler(payload.Path, payload.Payload)
	expr := bridgeResolveExpression(payload.ID, result)
	ctx, cancel := context.WithTimeout(context.Background(), cdpCommandTimeout)
	defer cancel()
	if _, err := s.send(ctx, "Runtime.evaluate", runtimeEvaluateParams(expr, false)); err != nil {
		appendDiagnosticLog("bridge.resolve_failed", map[string]any{"request_id": payload.ID, "error": err.Error()})
	}
}

func bridgeResolveExpression(requestID string, result map[string]any) string {
	id, _ := json.Marshal(requestID)
	value, _ := json.Marshal(result)
	return fmt.Sprintf("window.__codexSessionDeleteResolve(%s, %s)", id, value)
}

type server struct {
	root   string
	dist   string
	distFS fs.FS
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
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result := s.dispatch(ctx, command, args)
	writeJSON(w, result)
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

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "web", "package.json")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("unable to locate codextools repository root")
		}
		dir = parent
	}
}

func managerDistFS(root string) (fs.FS, string, error) {
	if root != "" {
		dist := filepath.Join(root, "web", "dist")
		if fileExists(filepath.Join(dist, "index.html")) {
			return os.DirFS(dist), dist, nil
		}
	}
	dist, err := fs.Sub(embeddedDist, "web/dist")
	if err == nil {
		if _, statErr := fs.Stat(dist, "index.html"); statErr == nil {
			return dist, "embedded:web/dist", nil
		}
	}
	if root != "" {
		return nil, filepath.Join(root, "web", "dist"), fmt.Errorf("未找到前端构建产物 %s，请先运行 npm --prefix web run vite:build 并重新构建下载包", filepath.Join(root, "web", "dist"))
	}
	return nil, "embedded:web/dist", errors.New("内嵌前端资源缺失，请先运行 npm --prefix web run vite:build 后重新执行 go build")
}

func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return stateDirName
	}
	return filepath.Join(home, stateDirName)
}

func defaultInstallRoot() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Applications"
	case "windows":
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			return filepath.Join(userProfile, "Desktop")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, "Desktop")
}

func settingsPath() string {
	return filepath.Join(stateDir(), settingsFileName)
}

func latestStatusPath() string {
	return filepath.Join(stateDir(), latestStatusFileName)
}

func diagnosticLogPath() string {
	return filepath.Join(stateDir(), diagnosticLogFileName)
}

func codexHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func defaultSettings() backendSettings {
	return backendSettings{
		CodexExtraArgs:      []string{},
		Enhancements:        true,
		LaunchMode:          "patch",
		RelayProfiles:       []relayProfile{defaultRelayProfile()},
		ActiveRelayID:       "default",
		RelayTestModel:      defaultRelayTestModel,
		CLIWrapperAPIKeyEnv: defaultAPIKeyEnvironment,
	}
}

func defaultRelayProfile() relayProfile {
	return relayProfile{
		ID:        "default",
		Name:      "默认中转",
		Protocol:  "responses",
		RelayMode: "official",
	}
}

func loadSettings() backendSettings {
	settings := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return defaultSettings()
	}
	return normalizeSettings(settings)
}

func normalizeSettings(settings backendSettings) backendSettings {
	if settings.CodexExtraArgs == nil {
		settings.CodexExtraArgs = []string{}
	}
	if settings.LaunchMode != "patch" && settings.LaunchMode != "relay" {
		settings.LaunchMode = "patch"
	}
	if len(settings.RelayProfiles) == 0 {
		settings.RelayProfiles = []relayProfile{defaultRelayProfile()}
	}
	for index := range settings.RelayProfiles {
		if settings.RelayProfiles[index].ID == "" {
			settings.RelayProfiles[index].ID = fmt.Sprintf("relay-%d", index+1)
		}
		if settings.RelayProfiles[index].Protocol == "" {
			settings.RelayProfiles[index].Protocol = "responses"
		}
		switch settings.RelayProfiles[index].RelayMode {
		case "mixedApi":
			settings.RelayProfiles[index].OfficialMixAPIKey = true
		case "pureApi":
			settings.RelayProfiles[index].OfficialMixAPIKey = false
		case "official":
			if settings.RelayProfiles[index].OfficialMixAPIKey {
				settings.RelayProfiles[index].RelayMode = "mixedApi"
			}
		default:
			if settings.RelayProfiles[index].OfficialMixAPIKey {
				settings.RelayProfiles[index].RelayMode = "mixedApi"
			} else {
				settings.RelayProfiles[index].RelayMode = "official"
				settings.RelayProfiles[index].OfficialMixAPIKey = false
			}
		}
	}
	if settings.ActiveRelayID == "" {
		settings.ActiveRelayID = settings.RelayProfiles[0].ID
	}
	if settings.RelayTestModel == "" {
		settings.RelayTestModel = defaultRelayTestModel
	}
	if settings.CLIWrapperAPIKeyEnv == "" {
		settings.CLIWrapperAPIKeyEnv = defaultAPIKeyEnvironment
	}
	return settings
}

func saveSettings(settings backendSettings) error {
	settings = normalizeSettings(settings)
	settings.CodexExtraArgs = normalizeExtraArgs(settings.CodexExtraArgs)
	if settings.CodexAppPath != "" {
		if normalized := normalizeCodexAppPath(settings.CodexAppPath); normalized != "" {
			settings.CodexAppPath = normalized
		}
	}
	return atomicWriteJSON(settingsPath(), settings)
}

func normalizeExtraArgs(args []string) []string {
	var normalized []string
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			normalized = append(normalized, arg)
		}
	}
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func settingsPayload(message string) commandResult {
	return ok(message, settingsPayloadValue(loadSettings()))
}

func settingsPayloadValue(settings backendSettings) map[string]any {
	return map[string]any{
		"settings":      settings,
		"settings_path": settingsPath(),
		"user_scripts":  userScriptInventoryValue(),
	}
}

func (s *server) saveSettings(args map[string]any) commandResult {
	var settings backendSettings
	if err := remarshal(args["settings"], &settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	if err := saveSettings(settings); err != nil {
		return failed("保存设置失败："+err.Error(), settingsPayloadValue(normalizeSettings(settings)))
	}
	return settingsPayload("设置已保存。")
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
	ccsProviders, ccsErr := listCCSProviders(ccsDBPath)
	download := latestCodexDownload(ctx, runtime.GOOS, runtime.GOARCH)
	payload := map[string]any{
		"platform":                    runtime.GOOS,
		"arch":                        runtime.GOARCH,
		"codexApp":                    pathState(codexApp),
		"codexVersion":                codexAppVersion(codexApp),
		"codexInstallUrl":             codexInstallURL(download),
		"codexInstallSource":          codexInstallSource(download),
		"codexMirrorProjectUrl":       codexAppMirrorProjectURL,
		"codexMirrorLatestReleaseUrl": codexMirrorLatestReleaseURL(download),
		"codexLatestDownload":         download,
		"ccs": map[string]any{
			"installed":     fileExists(ccsDBPath),
			"dbPath":        ccsDBPath,
			"providerCount": len(ccsProviders),
			"readError":     optionalErrorString(ccsErr),
		},
		"settingsPath": settingsPath(),
		"activeMode":   activeRelayProfile(settings).RelayMode,
	}
	if ccsErr != nil {
		return failed("新手引导状态已读取，但 CCSwitch 数据库读取失败："+ccsErr.Error(), payload)
	}
	return ok("新手引导状态已读取。", payload)
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

func entrypointPath(manager bool) string {
	root := defaultInstallRoot()
	name := silentName
	if manager {
		name = managerName
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(root, name+".app")
	case "windows":
		return filepath.Join(root, name+".lnk")
	default:
		return filepath.Join(root, name+".desktop")
	}
}

func pathState(path string) map[string]any {
	if path == "" {
		return map[string]any{"status": "missing", "path": nil}
	}
	return map[string]any{"status": "found", "path": path}
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
		roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), `C:\Program Files\WindowsApps`}
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
	providers, err := listCCSProviders(dbPath)
	if err != nil {
		return failed("读取 CCS 供应商失败："+err.Error(), map[string]any{"dbPath": dbPath, "providers": []ccsProviderImport{}})
	}
	return ok(fmt.Sprintf("已读取 CCS Codex 供应商：%d 个。", len(providers)), map[string]any{"dbPath": dbPath, "providers": providers})
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

func (s *server) syncProvidersNow() commandResult {
	result := runProviderSync(codexHomeDir())
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
	return commandResult{
		"status":              status,
		"message":             fmt.Sprintf("供应商已同步一次：%d 个会话文件，%d 行索引。%s", result.ChangedSessionFiles, result.SQLiteRowsUpdated, providerSyncExtraMessage(result.Message)),
		"syncStatus":          payload["syncStatus"],
		"targetProvider":      payload["targetProvider"],
		"changedSessionFiles": payload["changedSessionFiles"],
		"sqliteRowsUpdated":   payload["sqliteRowsUpdated"],
		"backupDir":           payload["backupDir"],
		"syncMessage":         payload["syncMessage"],
	}
}

func providerSyncExtraMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || message == "Provider sync complete" || message == "Provider sync already up to date" {
		return ""
	}
	return message
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

func defaultCCSDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cc-switch", "cc-switch.db")
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

func (s *server) refreshScriptMarket(ctx context.Context) commandResult {
	manifest, err := fetchMarketManifest(ctx)
	if err != nil {
		return failed("脚本市场加载失败："+err.Error(), failedScriptMarketPayload("脚本市场加载失败："+err.Error()))
	}
	return ok("脚本市场已刷新。", scriptMarketPayload(manifest, "ok", "脚本市场已刷新。"))
}

func (s *server) installMarketScript(ctx context.Context, id string) commandResult {
	id = strings.TrimSpace(id)
	if id == "" {
		return failed("脚本 id 不能为空。", failedScriptMarketPayload("脚本 id 不能为空。"))
	}
	manifest, err := fetchMarketManifest(ctx)
	if err != nil {
		return failed("脚本市场加载失败："+err.Error(), failedScriptMarketPayload("脚本市场加载失败："+err.Error()))
	}
	var selected *marketScript
	for i := range manifest.Scripts {
		if manifest.Scripts[i].ID == id {
			selected = &manifest.Scripts[i]
			break
		}
	}
	if selected == nil {
		return failed("市场清单中未找到该脚本。", scriptMarketPayload(manifest, "failed", "市场清单中未找到该脚本。"))
	}
	content, err := getBytes(ctx, selected.ScriptURL)
	if err != nil {
		return failed("安装脚本失败："+err.Error(), scriptMarketPayload(manifest, "failed", "安装脚本失败："+err.Error()))
	}
	if err := verifySHA256(*selected, content); err != nil {
		return failed("安装脚本失败："+err.Error(), scriptMarketPayload(manifest, "failed", "安装脚本失败："+err.Error()))
	}
	if err := installMarketScriptContent(*selected, content); err != nil {
		return failed("安装脚本失败："+err.Error(), scriptMarketPayload(manifest, "failed", "安装脚本失败："+err.Error()))
	}
	return ok("脚本已安装。", scriptMarketPayload(manifest, "ok", "脚本已安装。"))
}

func fetchMarketManifest(ctx context.Context) (marketManifest, error) {
	var raw map[string]any
	if err := getJSONInto(ctx, scriptMarketIndexURL, &raw); err != nil {
		return marketManifest{}, err
	}
	var manifest marketManifest
	manifest.Version = uint64FromAny(raw["version"], 1)
	manifest.UpdatedAt = stringFromAny(raw["updated_at"])
	if list, ok := raw["scripts"].([]any); ok {
		for _, item := range list {
			var script marketScript
			if err := remarshal(item, &script); err != nil {
				continue
			}
			if strings.TrimSpace(script.ID) != "" && strings.TrimSpace(script.Name) != "" && strings.TrimSpace(script.Version) != "" && strings.TrimSpace(script.ScriptURL) != "" {
				manifest.Scripts = append(manifest.Scripts, script)
			}
		}
	}
	return manifest, nil
}

func failedScriptMarketPayload(message string) map[string]any {
	return map[string]any{
		"market": map[string]any{
			"status": "failed", "message": message, "indexUrl": scriptMarketIndexURL, "updatedAt": "", "scripts": []any{},
		},
		"user_scripts": userScriptInventoryValue(),
	}
}

func scriptMarketPayload(manifest marketManifest, status string, message string) map[string]any {
	inventory := userScriptInventoryValue()
	installed := installedMarketVersions(inventory)
	scripts := make([]map[string]any, 0, len(manifest.Scripts))
	for _, script := range manifest.Scripts {
		installedVersion := installed[script.ID]
		scripts = append(scripts, map[string]any{
			"id": script.ID, "name": script.Name, "description": script.Description, "version": script.Version,
			"author": script.Author, "tags": script.Tags, "homepage": script.Homepage, "script_url": script.ScriptURL, "sha256": script.SHA256,
			"installed": installedVersion != "", "installedVersion": installedVersion, "updateAvailable": installedVersion != "" && installedVersion != script.Version,
		})
	}
	return map[string]any{
		"market":       map[string]any{"status": status, "message": message, "indexUrl": scriptMarketIndexURL, "updatedAt": manifest.UpdatedAt, "scripts": scripts},
		"user_scripts": inventory,
	}
}

func installedMarketVersions(inventory map[string]any) map[string]string {
	out := map[string]string{}
	items, _ := inventory["scripts"].([]userScriptInventoryItem)
	for _, script := range items {
		if script.MarketID != "" {
			out[script.MarketID] = script.Version
		}
	}
	return out
}

func verifySHA256(script marketScript, content []byte) error {
	expected := strings.ToLower(strings.TrimSpace(script.SHA256))
	if expected == "" {
		return nil
	}
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	if actual != expected {
		return fmt.Errorf("checksum mismatch for market script %s", script.ID)
	}
	return nil
}

func installMarketScriptContent(script marketScript, content []byte) error {
	path := filepath.Join(userScriptsDir(), marketScriptFilename(script.ID))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicWrite(path, content); err != nil {
		return err
	}
	config := loadUserScriptConfig()
	key := "user:" + marketScriptFilename(script.ID)
	if _, ok := config.Scripts[key]; !ok {
		config.Scripts[key] = true
	}
	config.Market[key] = marketScriptInstall{
		ID: script.ID, Name: script.Name, Version: script.Version, ScriptURL: script.ScriptURL, Homepage: script.Homepage, InstalledAt: strconv.FormatInt(time.Now().Unix(), 10),
	}
	return saveUserScriptConfig(config)
}

func userScriptsConfigDir() string {
	if runtime.GOOS == "windows" && os.Getenv("APPDATA") != "" {
		return filepath.Join(os.Getenv("APPDATA"), "Codex++")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "Codex++")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "Codex++")
	}
	return filepath.Join(home, ".config", "Codex++")
}

func userScriptsDir() string {
	return filepath.Join(userScriptsConfigDir(), "user_scripts")
}

func userScriptsConfigPath() string {
	return filepath.Join(userScriptsConfigDir(), "user_scripts.json")
}

func builtinUserScriptsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "user_scripts"
	}
	return filepath.Join(filepath.Dir(exe), "user_scripts")
}

func loadUserScriptConfig() userScriptConfig {
	config := userScriptConfig{Enabled: true, Scripts: map[string]bool{}, Market: map[string]marketScriptInstall{}}
	_ = readJSON(userScriptsConfigPath(), &config)
	if config.Scripts == nil {
		config.Scripts = map[string]bool{}
	}
	if config.Market == nil {
		config.Market = map[string]marketScriptInstall{}
	}
	return config
}

func saveUserScriptConfig(config userScriptConfig) error {
	if config.Scripts == nil {
		config.Scripts = map[string]bool{}
	}
	if config.Market == nil {
		config.Market = map[string]marketScriptInstall{}
	}
	return atomicWriteJSON(userScriptsConfigPath(), config)
}

func userScriptInventoryValue() map[string]any {
	inventory := scanUserScripts()
	return map[string]any{
		"enabled": inventory.Enabled, "builtin_dir": inventory.BuiltinDir, "user_dir": inventory.UserDir, "scripts": inventory.Scripts,
	}
}

func scanUserScripts() userScriptInventory {
	config := loadUserScriptConfig()
	inventory := userScriptInventory{Enabled: config.Enabled, BuiltinDir: builtinUserScriptsDir(), UserDir: userScriptsDir(), Scripts: []userScriptInventoryItem{}}
	_ = os.MkdirAll(userScriptsDir(), 0o755)
	appendScripts := func(source, dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
				continue
			}
			key := source + ":" + entry.Name()
			enabled, ok := config.Scripts[key]
			if !ok {
				enabled = true
			}
			status := "not_loaded"
			if !config.Enabled || !enabled {
				status = "disabled"
			}
			item := userScriptInventoryItem{Key: key, Name: entry.Name(), Source: source, Enabled: enabled, Status: status, Error: ""}
			if market, ok := config.Market[key]; ok {
				item.MarketID = market.ID
				item.Version = market.Version
				item.Installed = true
				item.SourceURL = market.ScriptURL
				item.Homepage = market.Homepage
			}
			inventory.Scripts = append(inventory.Scripts, item)
		}
	}
	appendScripts("builtin", inventory.BuiltinDir)
	appendScripts("user", inventory.UserDir)
	return inventory
}

func (s *server) setUserScriptEnabled(key string, enabled bool) commandResult {
	key = strings.TrimSpace(key)
	if key == "" {
		return failed("脚本 key 不能为空。", settingsPayloadValue(loadSettings()))
	}
	config := loadUserScriptConfig()
	config.Scripts[key] = enabled
	if err := saveUserScriptConfig(config); err != nil {
		return failed("脚本启停失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	if enabled {
		return settingsPayload("脚本已启用。")
	}
	return settingsPayload("脚本已禁用。")
}

func (s *server) deleteUserScript(key string) commandResult {
	key = strings.TrimSpace(key)
	if key == "" {
		return failed("脚本 key 不能为空。", settingsPayloadValue(loadSettings()))
	}
	fileName, ok := strings.CutPrefix(key, "user:")
	if !ok || fileName == "" || strings.ContainsAny(fileName, `/\`) || fileName == "." || fileName == ".." {
		return failed("脚本删除失败：only user scripts can be deleted", settingsPayloadValue(loadSettings()))
	}
	path := filepath.Join(userScriptsDir(), fileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed("脚本删除失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	config := loadUserScriptConfig()
	delete(config.Scripts, key)
	delete(config.Market, key)
	if err := saveUserScriptConfig(config); err != nil {
		return failed("脚本删除失败："+err.Error(), settingsPayloadValue(loadSettings()))
	}
	return settingsPayload("脚本已删除。")
}

func marketScriptFilename(id string) string {
	var b strings.Builder
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('-')
		}
	}
	safe := strings.Trim(b.String(), "-")
	if safe == "" {
		safe = "script"
	}
	return "market-" + safe + ".js"
}

func (s *server) openExternalURL(rawURL string) commandResult {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return failed("只允许打开 http 或 https 链接。", map[string]any{})
	}
	if err := openURL(rawURL); err != nil {
		return failed("打开链接失败："+err.Error(), map[string]any{"url": rawURL})
	}
	return ok("已在系统浏览器打开链接。", map[string]any{"url": rawURL})
}

func (s *server) installEntrypoints() commandResult {
	err := installEntrypoints()
	if err != nil {
		return installActionResult("failed", err.Error())
	}
	return installActionResult("ok", "入口已安装。")
}

func (s *server) uninstallEntrypoints(args map[string]any) commandResult {
	options := mapArg(args, "options")
	removeOwnedData := boolArg(options, "removeOwnedData")
	err := uninstallEntrypoints()
	if err == nil && removeOwnedData {
		_ = os.RemoveAll(stateDir())
	}
	if err != nil {
		return installActionResult("failed", err.Error())
	}
	return installActionResult("ok", "入口已卸载。")
}

func installActionResult(status, message string) commandResult {
	return commandResult{
		"status":              status,
		"message":             message,
		"silent_shortcut":     shortcutInstallState(entrypointPath(false)),
		"management_shortcut": shortcutInstallState(entrypointPath(true)),
	}
}

func shortcutInstallState(path string) map[string]any {
	return map[string]any{"installed": fileExists(path), "path": path}
}

func installEntrypoints() error {
	switch runtime.GOOS {
	case "darwin":
		if err := writeMacOSAppBundle(false); err != nil {
			return err
		}
		return writeMacOSAppBundle(true)
	case "windows":
		if err := createWindowsShortcut(entrypointPath(false), companionBinaryPath(silentBinary+".exe"), "Launch Codex++ silently"); err != nil {
			return err
		}
		return createWindowsShortcut(entrypointPath(true), companionBinaryPath(managerBinary+".exe"), "Open Codex++ management tool")
	default:
		if err := writeDesktopEntry(false); err != nil {
			return err
		}
		return writeDesktopEntry(true)
	}
}

func uninstallEntrypoints() error {
	var firstErr error
	for _, path := range []string{entrypointPath(false), entrypointPath(true)} {
		if err := os.RemoveAll(path); err != nil && firstErr == nil && !errors.Is(err, os.ErrNotExist) {
			firstErr = err
		}
	}
	return firstErr
}

func writeMacOSAppBundle(manager bool) error {
	appPath := entrypointPath(manager)
	contents := filepath.Join(appPath, "Contents")
	macos := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		return err
	}
	displayName := silentName
	executableName := "CodexPlusPlus"
	binary := silentBinary
	identifierSuffix := ""
	if manager {
		displayName = managerName
		executableName = "CodexPlusPlusManager"
		binary = managerBinary
		identifierSuffix = ".manager"
	}
	plist := macOSInfoPlist(displayName, executableName, identifierSuffix)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		return err
	}
	target := companionBinaryPath(binary)
	script := fmt.Sprintf("#!/bin/sh\nexport PATH=\"${PATH:-%s}:%s\"\nexec %q\n", defaultGUIPath, defaultGUIPath, target)
	executable := filepath.Join(macos, executableName)
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		return err
	}
	_ = copyFirstExistingFile([]string{
		filepath.Join(filepath.Dir(target), "codex-plus-plus.icns"),
		filepath.Join(filepath.Dir(target), "codex-plus-plus.png"),
	}, resources)
	return nil
}

func macOSInfoPlist(displayName, executableName, identifierSuffix string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>%s</string>
  <key>CFBundleDisplayName</key>
  <string>%s</string>
  <key>CFBundleIdentifier</key>
  <string>com.bigpizzav3.codexplusplus%s</string>
  <key>CFBundleVersion</key>
  <string>%s</string>
  <key>CFBundleShortVersionString</key>
  <string>%s</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleExecutable</key>
  <string>%s</string>
  <key>CFBundleIconFile</key>
  <string>codex-plus-plus</string>
  <key>LSUIElement</key>
  <true/>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
</dict>
</plist>`, displayName, displayName, identifierSuffix, version, version, executableName)
}

func copyFirstExistingFile(candidates []string, resources string) error {
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		return os.WriteFile(filepath.Join(resources, filepath.Base(candidate)), data, 0o644)
	}
	return nil
}

func createWindowsShortcut(shortcutPath, target, description string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows shortcuts are only supported on Windows")
	}
	if err := os.MkdirAll(filepath.Dir(shortcutPath), 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut(%s)
$shortcut.TargetPath = %s
$shortcut.WorkingDirectory = %s
$shortcut.Description = %s
$shortcut.IconLocation = %s
$shortcut.Save()
`, psQuote(shortcutPath), psQuote(target), psQuote(filepath.Dir(target)), psQuote(description), psQuote(target))
	return exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).Run()
}

func createWindowsShortcutWithArgs(shortcutPath, target, arguments, description string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows shortcuts are only supported on Windows")
	}
	if err := os.MkdirAll(filepath.Dir(shortcutPath), 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut(%s)
$shortcut.TargetPath = %s
$shortcut.Arguments = %s
$shortcut.WorkingDirectory = %s
$shortcut.Description = %s
$shortcut.IconLocation = %s
$shortcut.WindowStyle = 7
$shortcut.Save()
`, psQuote(shortcutPath), psQuote(target), psQuote(arguments), psQuote(filepath.Dir(target)), psQuote(description), psQuote(target))
	return exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).Run()
}

func windowsRegAddCurrentUserString(key, name, value string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows registry is only supported on Windows")
	}
	return exec.Command("reg", "add", key, "/v", name, "/t", "REG_SZ", "/d", value, "/f").Run()
}

func windowsRegDeleteCurrentUserValue(key, name string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows registry is only supported on Windows")
	}
	return exec.Command("reg", "delete", key, "/v", name, "/f").Run()
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func writeDesktopEntry(manager bool) error {
	path := entrypointPath(manager)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	name := silentName
	binary := silentBinary
	if manager {
		name = managerName
		binary = managerBinary
	}
	desktop := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=%s\nTerminal=false\n", name, companionBinaryPath(binary))
	return os.WriteFile(path, []byte(desktop), 0o755)
}

func watcherPayload() map[string]any {
	flag := filepath.Join(stateDir(), "watcher.disabled")
	install := watcherInstallState()
	return map[string]any{
		"enabled":            !fileExists(flag),
		"disabled_flag":      flag,
		"platform":           runtime.GOOS,
		"install_supported":  runtime.GOOS == "windows",
		"run_value_name":     watcherRunName,
		"run_value":          install.RunValue,
		"startup_shortcut":   install.ShortcutPath,
		"launcher_path":      install.LauncherPath,
		"launcher_arguments": install.Arguments,
	}
}

type watcherInstallPlan struct {
	LauncherPath string
	Arguments    string
	RunValue     string
	ShortcutPath string
}

func watcherInstallState() watcherInstallPlan {
	launcher := companionBinaryPath(silentBinary)
	if runtime.GOOS == "windows" {
		launcher += ".exe"
	}
	return buildWatcherInstallPlan(launcher, defaultWatcherDebugPort, watcherStartupShortcutPath())
}

func buildWatcherInstallPlan(launcherPath string, debugPort int, shortcutPath string) watcherInstallPlan {
	arguments := fmt.Sprintf("--debug-port %d", debugPort)
	return watcherInstallPlan{
		LauncherPath: launcherPath,
		Arguments:    arguments,
		RunValue:     fmt.Sprintf("\"%s\" %s", strings.ReplaceAll(launcherPath, `"`, `\"`), arguments),
		ShortcutPath: shortcutPath,
	}
}

func watcherStartupShortcutPath() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", watcherStartupLinkName)
}

func (s *server) installWatcher() commandResult {
	payload := watcherPayload()
	if runtime.GOOS != "windows" {
		return failed("watcher 安装仅支持 Windows；macOS 只能手动从 Codex++ 入口启动并用启用/禁用控制本地标志。", payload)
	}
	install := watcherInstallState()
	if install.ShortcutPath == "" {
		return failed("安装 watcher 失败：无法定位 Windows 启动目录。", watcherPayload())
	}
	if !fileExists(install.LauncherPath) {
		return failed("安装 watcher 失败：未找到静默启动器 "+install.LauncherPath, watcherPayload())
	}
	if err := windowsRegAddCurrentUserString(watcherRunKey, watcherRunName, install.RunValue); err != nil {
		return failed("安装 watcher 失败："+err.Error(), watcherPayload())
	}
	if err := createWindowsShortcutWithArgs(install.ShortcutPath, install.LauncherPath, install.Arguments, "Codex++ watcher"); err != nil {
		return failed("安装 watcher 失败："+err.Error(), watcherPayload())
	}
	spawnWatcherLauncher(install.LauncherPath, defaultWatcherDebugPort)
	return ok("watcher 已安装。", watcherPayload())
}

func (s *server) uninstallWatcher() commandResult {
	if runtime.GOOS != "windows" {
		return ok("watcher 安装仅支持 Windows；当前平台没有需要移除的自动启动项。", watcherPayload())
	}
	if err := windowsRegDeleteCurrentUserValue(watcherRunKey, watcherRunName); err != nil {
		// reg delete returns an error when the value does not exist; removal should remain idempotent.
		_ = err
	}
	if shortcut := watcherStartupShortcutPath(); shortcut != "" {
		_ = os.Remove(shortcut)
	}
	return ok("watcher 已移除。", watcherPayload())
}

func spawnWatcherLauncher(launcherPath string, debugPort int) {
	if runtime.GOOS != "windows" {
		return
	}
	cmd := exec.Command(launcherPath, "--debug-port", strconv.Itoa(debugPort))
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Start()
}

func (s *server) setWatcherDisabled(disabled bool) commandResult {
	flag := filepath.Join(stateDir(), "watcher.disabled")
	if disabled {
		if err := os.MkdirAll(filepath.Dir(flag), 0o755); err != nil {
			return failed("禁用 watcher 失败："+err.Error(), watcherPayload())
		}
		if err := os.WriteFile(flag, []byte("disabled"), 0o644); err != nil {
			return failed("禁用 watcher 失败："+err.Error(), watcherPayload())
		}
		return ok("watcher 已禁用。", watcherPayload())
	}
	if err := os.Remove(flag); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed("启用 watcher 失败："+err.Error(), watcherPayload())
	}
	return ok("watcher 已启用。", watcherPayload())
}

func (s *server) readLatestLogs(args map[string]any) commandResult {
	request := mapArg(args, "request")
	lines := intArg(request, "lines", 200)
	path := diagnosticLogPath()
	text, err := tailFile(path, lines)
	payload := map[string]any{"path": path, "text": text, "lines": lines}
	if err != nil {
		return failed("读取日志失败："+err.Error(), payload)
	}
	return ok("日志已读取。", payload)
}

func tailFile(path string, maxLines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}

func (s *server) diagnosticsReport() string {
	overview := s.loadOverview()
	settings := loadSettings()
	report := map[string]any{
		"generatedAtMs": time.Now().UnixMilli(),
		"version":       version,
		"overview":      map[string]any(overview),
		"settings":      settings,
		"logs": map[string]any{
			"diagnosticLogPath": diagnosticLogPath(),
			"latestStatusPath":  latestStatusPath(),
		},
		"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH},
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "诊断报告序列化失败：" + err.Error()
	}
	return string(data)
}

func (s *server) relayStatus() commandResult {
	status := relayStatusFromHome(codexHomeDir())
	message := "未检测到 ChatGPT 登录状态，请先在 Codex/ChatGPT 中正常登录。"
	if boolFromAny(status["authenticated"]) {
		message = "已检测到 ChatGPT 登录状态。"
	}
	return ok(message, status)
}

func relayStatusFromHome(home string) map[string]any {
	auth := chatGPTAuthStatus(home)
	config := relayConfigStatus(home)
	return map[string]any{
		"authenticated":      auth.Authenticated,
		"authSource":         auth.Source,
		"accountLabel":       nullableString(auth.AccountLabel),
		"configPath":         config.ConfigPath,
		"configured":         config.Configured,
		"requiresOpenaiAuth": config.RequiresOpenAIAuth,
		"hasBearerToken":     config.HasBearerToken,
		"backupPath":         nil,
	}
}

type authStatus struct {
	Authenticated bool
	Source        string
	AccountLabel  string
}

type configStatus struct {
	Configured         bool
	RequiresOpenAIAuth bool
	HasBearerToken     bool
	ConfigPath         string
}

func chatGPTAuthStatus(home string) authStatus {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return authStatus{}
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return authStatus{}
	}
	if !strings.EqualFold(stringFromAny(value["auth_mode"]), "chatgpt") {
		return authStatus{}
	}
	tokens, _ := value["tokens"].(map[string]any)
	if tokens == nil || (!hasToken(tokens, "access_token") && !hasToken(tokens, "id_token") && !hasToken(tokens, "refresh_token")) {
		return authStatus{}
	}
	return authStatus{Authenticated: true, Source: path, AccountLabel: accountLabelFromTokens(tokens)}
}

func hasToken(tokens map[string]any, key string) bool {
	return strings.TrimSpace(stringFromAny(tokens[key])) != ""
}

func accountLabelFromTokens(tokens map[string]any) string {
	for _, key := range []string{"id_token", "access_token"} {
		if label := accountLabelFromJWT(stringFromAny(tokens[key])); label != "" {
			return label
		}
	}
	return ""
}

func accountLabelFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return ""
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	if email := strings.TrimSpace(stringFromAny(value["email"])); email != "" {
		return email
	}
	if profile, ok := value["https://api.openai.com/profile"].(map[string]any); ok {
		if email := strings.TrimSpace(stringFromAny(profile["email"])); email != "" {
			return email
		}
	}
	return strings.TrimSpace(stringFromAny(value["name"]))
}

func relayConfigStatus(home string) configStatus {
	path := filepath.Join(home, "config.toml")
	data, _ := os.ReadFile(path)
	contents := string(data)
	providerActive := rootKeyString(contents, "model_provider") == relayProvider
	provider := tableValues(contents, "model_providers."+relayProvider)
	requiresAuth := strings.TrimSpace(provider["requires_openai_auth"]) == "true"
	hasBearer := strings.TrimSpace(unquoteToml(provider["experimental_bearer_token"])) != ""
	hasBaseURL := strings.TrimSpace(unquoteToml(provider["base_url"])) != ""
	return configStatus{Configured: providerActive && requiresAuth && hasBearer && hasBaseURL, RequiresOpenAIAuth: requiresAuth, HasBearerToken: hasBearer, ConfigPath: path}
}

func (s *server) readRelayFiles() commandResult {
	payload := relayFilesPayload(codexHomeDir())
	return ok("配置文件内容已读取。", payload)
}

func relayFilesPayload(home string) map[string]any {
	configPath := filepath.Join(home, "config.toml")
	authPath := filepath.Join(home, "auth.json")
	config, _ := os.ReadFile(configPath)
	auth, _ := os.ReadFile(authPath)
	return map[string]any{"configPath": configPath, "authPath": authPath, "configContents": string(config), "authContents": string(auth)}
}

func (s *server) saveRelayFile(args map[string]any) commandResult {
	request := mapArg(args, "request")
	kind := stringArg(request, "kind")
	contents := stringArg(request, "contents")
	var path string
	switch kind {
	case "config":
		path = filepath.Join(codexHomeDir(), "config.toml")
	case "auth":
		path = filepath.Join(codexHomeDir(), "auth.json")
	default:
		return failed("保存配置文件失败：未知配置文件类型："+kind, relayFilesPayload(codexHomeDir()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failed("保存配置文件失败："+err.Error(), relayFilesPayload(codexHomeDir()))
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return failed("保存配置文件失败："+err.Error(), relayFilesPayload(codexHomeDir()))
	}
	return ok("配置文件已保存。", relayFilesPayload(codexHomeDir()))
}

func (s *server) applyRelayInjection(pure bool) commandResult {
	home := codexHomeDir()
	settings := loadSettings()
	relay := activeRelayProfile(settings)
	useSavedFiles := strings.TrimSpace(relay.ConfigContents) != "" &&
		(strings.TrimSpace(relay.AuthContents) != "" || relay.RelayMode == "mixedApi")
	if !pure && relay.RelayMode == "mixedApi" && !chatGPTAuthStatus(home).Authenticated {
		return failed("未检测到 ChatGPT 登录状态，已停止写入中转配置。", relayStatusFromHome(home))
	}
	if !pure && useSavedFiles {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(relay.ConfigContents), 0o644); err != nil {
			return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
		}
		if strings.TrimSpace(relay.AuthContents) != "" {
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(relay.AuthContents), 0o644); err != nil {
				return failed("切换完整中转配置失败："+err.Error(), relayStatusFromHome(home))
			}
		}
		return ok("已切换到当前中转的完整 config.toml / auth.json。", relayStatusFromHome(home))
	}
	if err := applyRelayConfig(home, relay, pure); err != nil {
		if pure {
			return failed("写入中转 API 模式失败："+err.Error(), relayStatusFromHome(home))
		}
		return failed("写入中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	if pure {
		return ok("中转 API 模式已写入：auth.json 已切换为 OPENAI_API_KEY，config.toml 已写入 CodexPlusPlus provider。", relayStatusFromHome(home))
	}
	return ok("中转配置已写入，密钥未在界面明文显示。", relayStatusFromHome(home))
}

func activeRelayProfile(settings backendSettings) relayProfile {
	for _, profile := range settings.RelayProfiles {
		if profile.ID == settings.ActiveRelayID {
			return profile
		}
	}
	if len(settings.RelayProfiles) > 0 {
		return settings.RelayProfiles[0]
	}
	return defaultRelayProfile()
}

func applyRelayConfig(home string, relay relayProfile, pure bool) error {
	if !pure && relay.RelayMode == "official" {
		return errors.New("官方登录模式不需要写入 API 配置")
	}
	baseURL := effectiveBaseURL(relay)
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("中转 Base URL 不能为空")
	}
	if strings.TrimSpace(relay.APIKey) == "" {
		return errors.New("中转 Key 不能为空")
	}
	if relay.ImageGenerationEnabled && relay.ImageGenerationUseSeparateAPI {
		if strings.TrimSpace(relay.ImageGenerationBaseURL) == "" {
			return errors.New("图片 Base URL 不能为空")
		}
		if strings.TrimSpace(relay.ImageGenerationAPIKey) == "" {
			return errors.New("图片 Key 不能为空")
		}
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if pure {
		authPayload, _ := json.MarshalIndent(map[string]string{"OPENAI_API_KEY": strings.TrimSpace(relay.APIKey)}, "", "  ")
		if err := os.WriteFile(filepath.Join(home, "auth.json"), authPayload, 0o644); err != nil {
			return err
		}
	}
	configPath := filepath.Join(home, "config.toml")
	existing, _ := os.ReadFile(configPath)
	updated := upsertModelProviderConfig(string(existing), baseURL, strings.TrimSpace(relay.APIKey), relay)
	return os.WriteFile(configPath, []byte(updated), 0o644)
}

func effectiveBaseURL(relay relayProfile) string {
	if relay.Protocol == "chatCompletions" {
		return protocolProxyBaseURL
	}
	if relay.Protocol == "responses" && (disablesImageGeneration(relay) || usesSeparateImageGenerationAPI(relay)) {
		return fmt.Sprintf("http://127.0.0.1:%d/v1", localRelayProxyPort)
	}
	return strings.TrimSpace(relay.BaseURL)
}

func disablesImageGeneration(relay relayProfile) bool {
	return !relay.ImageGenerationEnabled
}

func usesSeparateImageGenerationAPI(relay relayProfile) bool {
	return relay.ImageGenerationEnabled && relay.ImageGenerationUseSeparateAPI && strings.TrimSpace(relay.ImageGenerationBaseURL) != "" && strings.TrimSpace(relay.ImageGenerationAPIKey) != ""
}

func upsertModelProviderConfig(contents, baseURL, bearerToken string, relay relayProfile) string {
	updated := upsertRootKey(contents, "model_provider", quoteToml(relayProvider))
	updated = removeTable(updated, "model_providers."+relayProvider)
	updated = removeTable(updated, "model_providers."+legacyRelayProvider)
	lines := splitLines(updated)
	insertAt := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[model_providers.") {
			insertAt = i
			break
		}
	}
	providerLines := []string{
		"[model_providers." + relayProvider + "]",
		"name = " + quoteToml(relayProvider),
		`wire_api = "responses"`,
		"requires_openai_auth = true",
		"base_url = " + quoteToml(baseURL),
	}
	if disablesImageGeneration(relay) {
		providerLines = append(providerLines, `disabled_tools = ["image_generation"]`)
	}
	if relay.Protocol == "responses" && (disablesImageGeneration(relay) || usesSeparateImageGenerationAPI(relay)) {
		providerLines = append(providerLines, "codex_plus_text_base_url = "+quoteToml(normalizeResponsesBaseURL(relay.BaseURL)))
	}
	if usesSeparateImageGenerationAPI(relay) {
		providerLines = append(providerLines, "codex_plus_image_base_url = "+quoteToml(normalizeResponsesBaseURL(relay.ImageGenerationBaseURL)))
	}
	providerLines = append(providerLines, "experimental_bearer_token = "+quoteToml(bearerToken), "")
	lines = append(lines[:insertAt], append(providerLines, lines[insertAt:]...)...)
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func (s *server) clearRelayInjection() commandResult {
	home := codexHomeDir()
	_ = os.MkdirAll(home, 0o755)
	clearPureAPIAuth(filepath.Join(home, "auth.json"))
	configPath := filepath.Join(home, "config.toml")
	data, _ := os.ReadFile(configPath)
	updated := removeRootKey(removeRootKey(removeTable(removeTable(string(data), "model_providers."+relayProvider), "model_providers."+legacyRelayProvider), "OPENAI_API_KEY"), "model_provider")
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return failed("清除中转配置失败："+err.Error(), relayStatusFromHome(home))
	}
	return ok("已清除 CodexPlusPlus 中转 API 模式，并切换到官方 ChatGPT 登录模式。", relayStatusFromHome(home))
}

func clearPureAPIAuth(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return
	}
	if _, ok := value["OPENAI_API_KEY"]; !ok {
		return
	}
	delete(value, "OPENAI_API_KEY")
	data, _ = json.MarshalIndent(value, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

func (s *server) testRelayProfile(ctx context.Context, args map[string]any) commandResult {
	var profile relayProfile
	if err := remarshal(args["profile"], &profile); err != nil {
		return failed("供应商参数错误："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": "", "responsePreview": ""})
	}
	settings := loadSettings()
	model := strings.TrimSpace(profile.TestModel)
	if model == "" {
		model = strings.TrimSpace(settings.RelayTestModel)
	}
	if model == "" {
		model = defaultRelayTestModel
	}
	endpoint, payload := relayTestPayload(profile, model)
	if strings.TrimSpace(profile.APIKey) == "" {
		return failed("测试「"+displayRelayName(profile)+"」失败：API Key 不能为空", map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return failed("测试「"+displayRelayName(profile)+"」失败："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+profile.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failed("测试「"+displayRelayName(profile)+"」失败："+err.Error(), map[string]any{"httpStatus": 0, "endpoint": endpoint, "responsePreview": ""})
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	preview := string([]rune(string(text))[:minRunes(string(text), 320)])
	status := "ok"
	if resp.StatusCode >= 400 {
		status = "failed"
	}
	detail := "响应内容为空"
	if strings.TrimSpace(preview) != "" {
		detail = "响应：" + strings.TrimSpace(preview)
	}
	return commandResult{"status": status, "message": fmt.Sprintf("已向「%s」用模型「%s」发送 hi，HTTP %d。%s", displayRelayName(profile), model, resp.StatusCode, detail), "httpStatus": resp.StatusCode, "endpoint": endpoint, "responsePreview": preview}
}

func relayTestPayload(profile relayProfile, model string) (string, map[string]any) {
	baseURL := strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
	if profile.Protocol == "chatCompletions" {
		return baseURL + "/chat/completions", map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 16}
	}
	return baseURL + "/responses", map[string]any{"model": model, "input": "hi", "max_output_tokens": 16}
}

func displayRelayName(profile relayProfile) string {
	if strings.TrimSpace(profile.Name) == "" {
		return "未命名供应商"
	}
	return strings.TrimSpace(profile.Name)
}

func rootKeyString(contents, key string) string {
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			return ""
		}
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(left) == key {
			return unquoteToml(right)
		}
	}
	return ""
}

func upsertRootKey(contents, key, value string) string {
	lines := splitLines(contents)
	rootEnd := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			rootEnd = i
			break
		}
	}
	for i := 0; i < rootEnd; i++ {
		if rootLineKey(lines[i]) == key {
			lines[i] = key + " = " + value
			return ensureTrailingNewline(strings.Join(lines, "\n"))
		}
	}
	lines = append(lines[:rootEnd], append([]string{key + " = " + value}, lines[rootEnd:]...)...)
	return ensureTrailingNewline(strings.Join(lines, "\n"))
}

func removeRootKey(contents, key string) string {
	var lines []string
	inRoot := true
	for _, line := range splitLines(contents) {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			inRoot = false
		}
		if inRoot && rootLineKey(line) == key {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func rootLineKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	left, _, ok := strings.Cut(trimmed, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(left)
}

func tableValues(contents, table string) map[string]string {
	values := map[string]string{}
	header := "[" + table + "]"
	inTable := false
	for _, line := range splitLines(contents) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inTable {
				break
			}
			inTable = trimmed == header
			continue
		}
		if !inTable || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if ok {
			values[strings.TrimSpace(left)] = strings.TrimSpace(right)
		}
	}
	return values
}

func removeTable(contents, table string) string {
	header := "[" + table + "]"
	var lines []string
	skipping := false
	for _, line := range splitLines(contents) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == header {
				skipping = true
				continue
			}
			skipping = false
		}
		if !skipping {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func unquoteToml(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, `"`)
	value = strings.TrimSuffix(value, `"`)
	return value
}

func quoteToml(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func tomlEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func normalizeResponsesBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" || baseURLHasPathAfterHost(trimmed) {
		return trimmed
	}
	return trimmed + "/v1"
}

func baseURLHasPathAfterHost(baseURL string) bool {
	after := baseURL
	if parts := strings.SplitN(baseURL, "://", 2); len(parts) == 2 {
		after = parts[1]
	}
	_, path, ok := strings.Cut(after, "/")
	return ok && strings.Trim(path, "/") != ""
}

func splitLines(contents string) []string {
	contents = strings.ReplaceAll(contents, "\r\n", "\n")
	if contents == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
}

func ensureTrailingNewline(value string) string {
	if !strings.HasSuffix(value, "\n") {
		return value + "\n"
	}
	return value
}

func getJSON[T any](ctx context.Context, rawURL string) (T, error) {
	var out T
	err := getJSONInto(ctx, rawURL, &out)
	return out, err
}

func getJSONInto(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", "CodexPlusPlus-GoManager/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func getBytes(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", "CodexPlusPlus-GoManager/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func openURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func promptPath(title string, directory bool) string {
	if runtime.GOOS == "darwin" {
		choose := "file"
		if directory {
			choose = "folder"
		}
		script := fmt.Sprintf(`POSIX path of (choose %s with prompt %q)`, choose, title)
		out, err := exec.Command("osascript", "-e", script).Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func remarshal(in any, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func mapArg(args map[string]any, key string) map[string]any {
	value, _ := args[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func stringArg(args map[string]any, key string) string {
	return strings.TrimSpace(stringFromAny(args[key]))
}

func boolArg(args map[string]any, key string) bool {
	return boolFromAny(args[key])
}

func intArg(args map[string]any, key string, fallback int) int {
	switch value := args[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func uint16Arg(args map[string]any, key string, fallback uint16) uint16 {
	value := intArg(args, key, int(fallback))
	if value <= 0 || value > 65535 {
		return fallback
	}
	return uint16(value)
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1"
	default:
		return false
	}
}

func uint64FromAny(value any, fallback uint64) uint64 {
	switch typed := value.(type) {
	case float64:
		return uint64(typed)
	case uint64:
		return typed
	case int:
		return uint64(typed)
	case string:
		if parsed, err := strconv.ParseUint(typed, 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringFromAny(value)); text != "" {
			return text
		}
	}
	return ""
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func minRunes(value string, max int) int {
	count := 0
	for range value {
		if count >= max {
			return count
		}
		count++
	}
	return count
}

func urlPathUnescape(value string) (string, error) {
	return strings.ReplaceAll(value, "%2F", "/"), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
