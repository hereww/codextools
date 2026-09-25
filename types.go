package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type backendSettings struct {
	CodexAppPath                     string                  `json:"codexAppPath"`
	CodexExtraArgs                   []string                `json:"codexExtraArgs"`
	Language                         string                  `json:"language"`
	ProviderSync                     bool                    `json:"providerSyncEnabled"`
	ProviderSyncSavedProviders       []string                `json:"providerSyncSavedProviders"`
	ProviderSyncManualProviders      []string                `json:"providerSyncManualProviders"`
	ProviderSyncLastSelectedProvider string                  `json:"providerSyncLastSelectedProvider"`
	RelayProfilesEnabled             bool                    `json:"relayProfilesEnabled"`
	CCSLinkEnabled                   bool                    `json:"ccsLinkEnabled"`
	Enhancements                     bool                    `json:"enhancementsEnabled"`
	CodexAppPluginMarketplaceUnlock  bool                    `json:"codexAppPluginMarketplaceUnlock"`
	CodexAppPluginAutoExpand         bool                    `json:"codexAppPluginAutoExpand"`
	CodexAppModelWhitelistUnlock     bool                    `json:"codexAppModelWhitelistUnlock"`
	CodexAppSessionDelete            bool                    `json:"codexAppSessionDelete"`
	CodexAppMarkdownExport           bool                    `json:"codexAppMarkdownExport"`
	CodexAppPasteFix                 bool                    `json:"codexAppPasteFix"`
	CodexAppForceChineseLocale       bool                    `json:"codexAppForceChineseLocale"`
	CodexAppFastStartup              bool                    `json:"codexAppFastStartup"`
	CodexAppProjectMove              bool                    `json:"codexAppProjectMove"`
	CodexAppConversationTimeline     bool                    `json:"codexAppConversationTimeline"`
	CodexAppThreadIDBadge            bool                    `json:"codexAppThreadIdBadge"`
	CodexAppConversationView         bool                    `json:"codexAppConversationView"`
	CodexAppThreadScrollRestore      bool                    `json:"codexAppThreadScrollRestore"`
	CodexAppZedRemoteOpen            bool                    `json:"codexAppZedRemoteOpen"`
	CodexAppUpstreamWorktreeCreate   bool                    `json:"codexAppUpstreamWorktreeCreate"`
	CodexAppNativeMenuPlacement      bool                    `json:"codexAppNativeMenuPlacement"`
	CodexAppNativeMenuLocalization   bool                    `json:"codexAppNativeMenuLocalization"`
	CodexAppServiceTierControls      bool                    `json:"codexAppServiceTierControls"`
	CodexAppPetRealMouseLook         bool                    `json:"codexAppPetRealMouseLook"`
	CodexAppStepwiseEnabled          bool                    `json:"codexAppStepwiseEnabled"`
	CodexAppStepwiseDirectSend       bool                    `json:"codexAppStepwiseDirectSend"`
	CodexAppStepwiseBaseURL          string                  `json:"codexAppStepwiseBaseUrl"`
	CodexAppStepwiseAPIKey           string                  `json:"codexAppStepwiseApiKey"`
	CodexAppStepwiseAPIKeyEnv        string                  `json:"codexAppStepwiseApiKeyEnv"`
	CodexAppStepwiseModel            string                  `json:"codexAppStepwiseModel"`
	CodexAppStepwiseMaxItems         int                     `json:"codexAppStepwiseMaxItems"`
	CodexAppStepwiseMaxInputChars    int                     `json:"codexAppStepwiseMaxInputChars"`
	CodexAppStepwiseMaxOutputTokens  int                     `json:"codexAppStepwiseMaxOutputTokens"`
	CodexAppStepwiseTimeoutMS        int                     `json:"codexAppStepwiseTimeoutMs"`
	ComputerUseGuardEnabled          bool                    `json:"computerUseGuardEnabled"`
	ZedRemoteOpenStrategy            string                  `json:"zedRemoteOpenStrategy"`
	ZedRemoteProjectRegistryEnabled  bool                    `json:"zedRemoteProjectRegistryEnabled"`
	ZedRemoteSyncToZedSettings       bool                    `json:"zedRemoteSyncToZedSettings"`
	CodexAppImageOverlayEnabled      bool                    `json:"codexAppImageOverlayEnabled"`
	CodexAppImageOverlayPath         string                  `json:"codexAppImageOverlayPath"`
	CodexAppImageOverlayOpacity      int                     `json:"codexAppImageOverlayOpacity"`
	CodexAppImageOverlayFitMode      string                  `json:"codexAppImageOverlayFitMode"`
	CodexAppDreamSkinEnabled         bool                    `json:"codexAppDreamSkinEnabled"`
	CodexAppDreamSkinPaused          bool                    `json:"codexAppDreamSkinPaused"`
	CodexAppDreamSkinTheme           string                  `json:"codexAppDreamSkinTheme"`
	CodexAppDreamSkinThemeConfig     dreamSkinThemeConfig    `json:"codexAppDreamSkinThemeConfig"`
	CodexAppDreamSkinImagePath       string                  `json:"codexAppDreamSkinImagePath"`
	CodexGoalsEnabled                bool                    `json:"codexGoalsEnabled"`
	MobileControlEnabled             bool                    `json:"mobileControlEnabled"`
	MobileControlRelayURL            string                  `json:"mobileControlRelayUrl"`
	MobileControlRoom                string                  `json:"mobileControlRoom"`
	MobileControlKey                 string                  `json:"mobileControlKey"`
	OnboardingCompleted              bool                    `json:"onboardingCompleted"`
	OnboardingCompletedAt            string                  `json:"onboardingCompletedAt"`
	OnboardingCompletedPlatform      string                  `json:"onboardingCompletedPlatform"`
	LaunchMode                       string                  `json:"launchMode"`
	ProxyEnabled                     bool                    `json:"proxyEnabled"`
	ProxyURL                         string                  `json:"proxyUrl"`
	ProxyRelayEnabled                bool                    `json:"proxyRelayEnabled"`
	ProxyRemoteControlEnabled        bool                    `json:"proxyRemoteControlEnabled"`
	ProxyOfficialAuthEnabled         bool                    `json:"proxyOfficialAuthEnabled"`
	ProxyRealtimeEnabled             bool                    `json:"proxyRealtimeEnabled"`
	ProxyModelCatalogEnabled         bool                    `json:"proxyModelCatalogEnabled"`
	ProxyAudioEnabled                bool                    `json:"proxyAudioEnabled"`
	ProxyVLMEnabled                  bool                    `json:"proxyVlmEnabled"`
	ProxyStepwiseEnabled             bool                    `json:"proxyStepwiseEnabled"`
	ProxyNoProxy                     string                  `json:"proxyNoProxy"`
	RelayBaseURL                     string                  `json:"relayBaseUrl"`
	RelayAPIKey                      string                  `json:"relayApiKey"`
	RelayProfiles                    []relayProfile          `json:"relayProfiles"`
	RelayCommonConfigContents        string                  `json:"relayCommonConfigContents"`
	RelayContextConfigContents       string                  `json:"relayContextConfigContents"`
	ActiveRelayID                    string                  `json:"activeRelayId"`
	AggregateRelayProfiles           []aggregateRelayProfile `json:"aggregateRelayProfiles"`
	ActiveAggregateRelayID           string                  `json:"activeAggregateRelayId"`
	RelayTestModel                   string                  `json:"relayTestModel"`
	CLIWrapperEnabled                bool                    `json:"cliWrapperEnabled"`
	CLIWrapperBaseURL                string                  `json:"cliWrapperBaseUrl"`
	CLIWrapperAPIKey                 string                  `json:"cliWrapperApiKey"`
	CLIWrapperAPIKeyEnv              string                  `json:"cliWrapperApiKeyEnv"`
}

type relayProfile struct {
	ID                            string                `json:"id"`
	LinkedCCSProviderID           string                `json:"linkedCcsProviderId"`
	Name                          string                `json:"name"`
	Model                         string                `json:"model"`
	BaseURL                       string                `json:"baseUrl"`
	UpstreamBaseURL               string                `json:"upstreamBaseUrl"`
	APIKey                        string                `json:"apiKey"`
	ImageGenerationEnabled        bool                  `json:"imageGenerationEnabled"`
	ImageGenerationUseSeparateAPI bool                  `json:"imageGenerationUseSeparateApi"`
	ImageGenerationBaseURL        string                `json:"imageGenerationBaseUrl"`
	ImageGenerationAPIKey         string                `json:"imageGenerationApiKey"`
	Protocol                      string                `json:"protocol"`
	RelayMode                     string                `json:"relayMode"`
	OfficialMixAPIKey             bool                  `json:"officialMixApiKey"`
	HideOfficialUsageAlert        bool                  `json:"hideOfficialUsageAlert"`
	OfficialAuthContents          string                `json:"officialAuthContents"`
	OfficialAccountLabel          string                `json:"officialAccountLabel"`
	OfficialAuthUpdatedAt         string                `json:"officialAuthUpdatedAt"`
	TestModel                     string                `json:"testModel"`
	ConfigContents                string                `json:"configContents"`
	AuthContents                  string                `json:"authContents"`
	UseCommonConfig               bool                  `json:"useCommonConfig"`
	ContextSelection              relayContextSelection `json:"contextSelection"`
	ContextSelectionInitialized   bool                  `json:"contextSelectionInitialized"`
	ContextWindow                 string                `json:"contextWindow"`
	AutoCompactLimit              string                `json:"autoCompactLimit"`
	ModelInsertMode               string                `json:"modelInsertMode"`
	ModelList                     string                `json:"modelList"`
	ModelWindows                  string                `json:"modelWindows"`
	ModelAutoCompact              string                `json:"modelAutoCompact"`
	ModelVLM                      string                `json:"modelVlm"`
	VLMAPIKey                     string                `json:"vlmApiKey"`
	VLMModel                      string                `json:"vlmModel"`
	VLMBaseURL                    string                `json:"vlmBaseUrl"`
	UserAgent                     string                `json:"userAgent"`
	Sub2APIEnabled                bool                  `json:"sub2apiEnabled"`
	Sub2APIMultiplier             string                `json:"sub2apiMultiplier"`
	ProxyEnabled                  bool                  `json:"proxyEnabled"`
	ProxyURL                      string                `json:"proxyUrl"`
	ModelRoutes                   []relayModelRoute     `json:"modelRoutes,omitempty"`
}

type relayModelRoute struct {
	Model         string `json:"model"`
	TargetRelayID string `json:"targetRelayId"`
	TargetModel   string `json:"targetModel,omitempty"`
}

type dreamSkinThemeConfig map[string]any

type aggregateRelayProfile struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Strategy string                 `json:"strategy"`
	Members  []aggregateRelayMember `json:"members"`
}

type aggregateRelayMember struct {
	RelayID string `json:"relayId"`
	Weight  int    `json:"weight"`
}

type relayContextSelection struct {
	MCPServers []string `json:"mcpServers"`
	Skills     []string `json:"skills"`
	Plugins    []string `json:"plugins"`
}

type launchStatus struct {
	Status      string         `json:"status"`
	Message     string         `json:"message"`
	StartedAtMS uint64         `json:"started_at_ms"`
	DebugPort   *uint16        `json:"debug_port"`
	HelperPort  *uint16        `json:"helper_port"`
	CodexApp    *string        `json:"codex_app"`
	Detail      map[string]any `json:"detail,omitempty"`
}

type launcherRuntime struct {
	settingsMu          sync.RWMutex
	settings            backendSettings
	settingsRefreshMu   sync.Mutex
	settingsRefreshedAt time.Time
	realtimeMu          sync.Mutex
	realtimeDenied      officialRealtimeDenial
	debugPort           uint16
	codexAppPath        string
	helper              *http.Server
	relay               *http.Server
	mobileHost          *mobileRelayHostRuntime
	helperURL           string
	relayURL            string
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
	Partial             bool    `json:"partial"`
	RollbackStatus      string  `json:"rollbackStatus,omitempty"`
}

type codexConfigRepairResult struct {
	Status                    string
	Message                   string
	BackupPath                *string
	PluginCount               int
	MarketplaceCount          int
	MCPServerCount            int
	MarketplaceRefreshStatus  string
	MarketplaceRefreshSummary string
	MarketplaceRefreshError   string
	GoalsEnabled              bool
	PluginConfigChanged       bool
	GoalsConfigChanged        bool
}

type codexConfigRepairOptions struct {
	Plugins             bool
	Goals               bool
	RefreshMarketplaces bool
}

type pluginEnableSpec struct {
	Name        string
	Marketplace string
}

type marketplaceSpec struct {
	Name   string
	Source string
}

type computerUseStatus struct {
	Platform                string  `json:"platform"`
	Supported               bool    `json:"supported"`
	CodexHome               string  `json:"codexHome"`
	EnvEnabled              bool    `json:"envEnabled"`
	ProcessEnv              string  `json:"processEnv"`
	UserEnv                 string  `json:"userEnv"`
	MarketplaceRoot         string  `json:"marketplaceRoot"`
	MarketplaceManifestPath string  `json:"marketplaceManifestPath"`
	MarketplacePluginPath   string  `json:"marketplacePluginPath"`
	MarketplaceReady        bool    `json:"marketplaceReady"`
	MarketplaceManifest     bool    `json:"marketplaceManifest"`
	MarketplacePlugin       bool    `json:"marketplacePlugin"`
	CacheLatest             bool    `json:"cacheLatest"`
	CacheLatestPath         string  `json:"cacheLatestPath"`
	CacheVersion            string  `json:"cacheVersion"`
	ConfigReady             bool    `json:"configReady"`
	ConfigPath              string  `json:"configPath"`
	ConfigMarketplace       bool    `json:"configMarketplace"`
	ConfigPlugin            bool    `json:"configPlugin"`
	ConfigNodeRepl          bool    `json:"configNodeRepl"`
	ConfigWindows           bool    `json:"configWindows"`
	HelperTransport         bool    `json:"helperTransport"`
	HelperTransportPath     string  `json:"helperTransportPath"`
	BackupPath              *string `json:"backupPath,omitempty"`
	AllReady                bool    `json:"allReady"`
}

type skillMCPBackupInfo struct {
	ID                  string `json:"id"`
	CreatedAt           string `json:"createdAt"`
	Label               string `json:"label"`
	Path                string `json:"path"`
	HasSkills           bool   `json:"hasSkills"`
	HasPluginCache      bool   `json:"hasPluginCache"`
	HasBundledMarket    bool   `json:"hasBundledMarket"`
	HasConfigSnapshot   bool   `json:"hasConfigSnapshot"`
	ConfigSnapshotBytes int64  `json:"configSnapshotBytes"`
	SizeBytes           int64  `json:"sizeBytes"`
	RestoreSourceBackup string `json:"restoreSourceBackup,omitempty"`
	RestoreConfigBackup string `json:"restoreConfigBackup,omitempty"`
}

type sessionChange struct {
	Path                     string
	OriginalFirstLine        string
	NextFirstLine            string
	OriginalSessionMetaLines []string
	NextSessionMetaLines     []string
	Separator                string
	ThreadID                 string
	CWD                      string
	Source                   string
	Title                    string
	FirstUserMessage         string
	Preview                  string
	CreatedAt                int64
	UpdatedAt                int64
	CreatedAtMs              int64
	UpdatedAtMs              int64
	Archived                 bool
	CLIVersion               string
	Model                    string
	ReasoningEffort          string
	SandboxPolicy            string
	ApprovalMode             string
	HasUserEvent             bool
	RewriteNeeded            bool
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

type providerImportRequest struct {
	Name           string `json:"name"`
	BaseURL        string `json:"baseUrl"`
	APIKey         string `json:"apiKey"`
	WireAPI        string `json:"wireApi"`
	RelayMode      string `json:"relayMode"`
	ConfigContents string `json:"configContents"`
	AuthContents   string `json:"authContents"`
}

type providerImportResult struct {
	Imported    bool   `json:"imported"`
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

type codexToolsRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

type launchRequest struct {
	appPath    string
	debugPort  uint16
	helperPort uint16
	restart    bool
}

type launcherSingleInstanceLock interface {
	release()
	fallbackPath() string
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

	conversationHistoryRepairMu   sync.Mutex
	conversationHistoryRepairTask *conversationHistoryRepairTask
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
