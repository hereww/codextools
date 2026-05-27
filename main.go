package main

import (
	"embed"
	"fmt"
	"os"
	"time"
)

const (
	appName                  = "CodexTools"
	silentName               = "Codex++"
	managerName              = "Codex++ 管理工具"
	silentBinary             = "codextools-launcher"
	managerBinary            = "codextools"
	version                  = "1.1.14"
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
	codexToolsLatestAPIURL   = "https://api.github.com/repos/hereww/codextools/releases/latest"
	codexToolsReleaseURL     = "https://github.com/hereww/codextools/releases/latest"
	codexToolsProjectURL     = "https://github.com/hereww/codextools"
	codexOfficialInstallURL  = "https://openai.com/codex/"
	defaultRelayTestModel    = "gpt-5-mini"
	defaultAPIKeyEnvironment = "CUSTOM_OPENAI_API_KEY"
	defaultLanguage          = "zh-CN"
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
