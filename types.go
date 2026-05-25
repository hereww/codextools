package main

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"io/fs"
	"net/http"
	"sync"
)

type backendSettings struct {
	CodexAppPath        string         `json:"codexAppPath"`
	CodexExtraArgs      []string       `json:"codexExtraArgs"`
	Language            string         `json:"language"`
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
	OfficialAuthContents          string `json:"officialAuthContents"`
	OfficialAccountLabel          string `json:"officialAccountLabel"`
	OfficialAuthUpdatedAt         string `json:"officialAuthUpdatedAt"`
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

type codexConfigRepairResult struct {
	Status              string
	Message             string
	BackupPath          *string
	PluginCount         int
	MarketplaceCount    int
	MCPServerCount      int
	GoalsEnabled        bool
	PluginConfigChanged bool
	GoalsConfigChanged  bool
}

type codexConfigRepairOptions struct {
	Plugins bool
	Goals   bool
}

type pluginEnableSpec struct {
	Name        string
	Marketplace string
}

type marketplaceSpec struct {
	Name   string
	Source string
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

type launchRequest struct {
	appPath    string
	debugPort  uint16
	helperPort uint16
	restart    bool
}

type relayRouteDecision struct {
	useImageAPI       bool
	body              []byte
	route             string
	reason            string
	keySource         string
	strippedImageTool bool
}

type cdpSession struct {
	conn    *websocket.Conn
	handler func(string, json.RawMessage) map[string]any
	nextID  int64
	pending map[int64]chan cdpResponse
	mu      sync.Mutex
	writeMu sync.Mutex
}

type server struct {
	root   string
	dist   string
	distFS fs.FS
}

type watcherInstallPlan struct {
	LauncherPath string
	Arguments    string
	RunValue     string
	ShortcutPath string
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
