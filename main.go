package main

import (
	"embed"
	"fmt"
	"os"
	"time"
)

const (
	appName                  = "ChatGPT Codex Tools"
	silentName               = "ChatGPT Codex"
	managerName              = "ChatGPT Codex 管理工具"
	silentBinary             = "codextools-launcher"
	managerBinary            = "codextools"
	version                  = "1.2.10"
	stateDirName             = ".codex-session-delete"
	settingsFileName         = "settings.json"
	latestStatusFileName     = "latest-status.json"
	diagnosticLogFileName    = "codex-plus.log"
	relayProvider            = "CodexPlusPlus"
	legacyRelayProvider      = "CodexPP"
	aggregateRelayAPIKey     = "codex-plus-aggregate"
	imageRelayCLIAPIKey      = "codex-plus-image-relay"
	localRelayProxyPort      = 57323
	protocolProxyBaseURL     = "http://127.0.0.1:57321/v1"
	defaultMobileRelayURL    = "ws://127.0.0.1:57323"
	scriptMarketIndexURL     = "https://raw.githubusercontent.com/BigPizzaV3/CodexPlusPlusScriptMarket/main/index.json"
	codexToolsLatestAPIURL   = "https://api.github.com/repos/hereww/codextools/releases/latest"
	codexToolsReleaseURL     = "https://github.com/hereww/codextools/releases/latest"
	codexToolsProjectURL     = "https://github.com/hereww/codextools"
	codexToolsDownloadsURL   = "https://hereww.github.io/codextools/downloads.html"
	codexToolsPagesBaseURL   = "https://hereww.github.io/codextools/"
	codexOfficialInstallURL  = "https://chatgpt.com/download/"
	defaultRelayTestModel    = "gpt-5-mini"
	defaultAPIKeyEnvironment = "CUSTOM_OPENAI_API_KEY"
	defaultStepwiseAPIKeyEnv = "CODEX_STEPWISE_API_KEY"
	defaultLanguage          = "zh-CN"
	defaultGUIPath           = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	cdpConnectTimeout        = 5 * time.Second
	cdpCommandTimeout        = 5 * time.Second
	launcherCheckInterval    = 5 * time.Second
	bridgeBindingName        = "codexSessionDeleteV2"
	launcherGuardPort        = 57320
	defaultWatcherDebugPort  = 9229
	watcherRunName           = "ChatGPTCodexToolsWatcher"
	watcherRunKey            = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	watcherStartupLinkName   = "ChatGPTCodexToolsWatcher.lnk"
)

var binaryRole = "manager"

//go:embed all:web/dist
var embeddedDist embed.FS

//go:embed assets/inject/renderer-inject.js
var rendererInjectScript string

//go:embed assets/inject/stepwise-inject.js
var stepwiseInjectScript string

//go:embed assets/inject/pet-real-mouse-inject.js
var petRealMouseInjectScript string

//go:embed assets/gpt56-model-metadata-compat.json
var gpt56ModelMetadataJSON []byte

//go:embed assets/astra-model-metadata-compat.json
var astraModelMetadataJSON []byte

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
