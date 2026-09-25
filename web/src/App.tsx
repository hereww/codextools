import {
  closestCenter,
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  Activity,
  ArrowLeft,
  Bell,
  Check,
  CheckCircle2,
  Copy,
  Download,
  Edit3,
  FileCode2,
  GripVertical,
  Globe2,
  Image,
  Info,
  ExternalLink,
  Hammer,
  Laptop,
  KeyRound,
  LayoutDashboard,
  Link2,
  MessageCircle,
  Moon,
  Power,
  PowerOff,
  Plus,
  RefreshCw,
  Rocket,
  Save,
  ScrollText,
  Settings,
  ShieldCheck,
  Square,
  Smartphone,
  Sparkles,
  Stethoscope,
  Sun,
  TestTube,
  Trash2,
  Waypoints,
  Wrench,
  X,
  type LucideIcon,
} from "lucide-react";
import { Component, useEffect, useMemo, useRef, useState, type CSSProperties, type ErrorInfo, type ReactNode } from "react";

import { Badge as UiBadge } from "@/components/ui/badge";
import { backendInvoke, openFileDialog } from "@/backend";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { defaultLanguage, languageOptions, localizeDocument, normalizeLanguage, translateText, watchDocumentLocalization, type LanguageCode } from "@/i18n";
import {
  hasCompleteModelRoutes,
  normalizeRelayModelRoutes,
  relayModelRouteValidationMessage,
  type RelayModelRoute,
} from "@/model-routes";

type Status = "ok" | "failed" | "not_implemented" | "not_checked" | string;

type CommandResult<T> = T & {
  status: Status;
  message: string;
};

type PathState = {
  status: string;
  path: string | null;
  executable?: string;
  appUserModelId?: string;
};

type CodexLaunchStatus = {
  ready: boolean;
  method: string;
  methodLabel: string;
  path: string | null;
  executable?: string;
  appUserModelId?: string;
  message: string;
};

type PlatformGuide = {
  platform: string;
  platformLabel: string;
  title: string;
  systemDescription: string;
  desktopRuntime: string;
  desktopRuntimeDescription: string;
  installTitle: string;
  installActionLabel: string;
  installSourceLabel: string;
  installDescription: string;
  manualPrimaryLabel: string;
  manualPrimaryMode: "folder" | "file";
  manualSecondaryLabel?: string;
  manualSecondaryMode?: "folder" | "file" | "";
  detectionNote: string;
  pathHint: string;
  launchMethodLabel: string;
  launchTargetLabel: string;
  completionLabel: string;
  unsupported: boolean;
};

type InstallGuideConnectionStatus = {
  ready: boolean;
  mode: RelayMode;
  profileId: string;
  profileName: string;
  message: string;
  officialReady: boolean;
  currentOfficialReady: boolean;
  boundOfficialReady: boolean;
  apiReady: boolean;
  configured: boolean;
  accountLabel?: string;
  boundProfileId?: string;
  boundProfileName?: string;
};

type LaunchStatus = {
  status: string;
  message: string;
  started_at_ms: number;
  debug_port: number | null;
  helper_port: number | null;
  codex_app: string | null;
  detail?: Record<string, unknown>;
};

type TaskProgress = {
  id: "restart" | "relaySwitch" | "conversationHistoryRepair";
  label: string;
  detail: string;
  percent: number;
  status: "running" | "ok" | "failed" | "cancelled";
};

type OverviewResult = CommandResult<{
  codex_app: PathState;
  codex_version: string | null;
  silent_shortcut: PathState;
  management_shortcut: PathState;
  latest_launch: LaunchStatus | null;
  current_version: string;
  update_status: string;
  update?: UpdateResult;
  settings_path: string;
  logs_path: string;
}>;

type UpdateResult = CommandResult<{
  updateStatus: string;
  currentVersion: string;
  latestVersion?: string;
  releaseName?: string;
  tagName?: string;
  publishedAt?: string;
  projectUrl: string;
  releaseUrl: string;
  downloadsUrl?: string;
  platform: string;
  arch: string;
  assetName?: string;
  downloadUrl?: string;
  downloadedPath?: string;
  updateSource?: string;
  apiError?: string;
  fallbackError?: string;
  size?: number;
  contentType?: string;
  assetKind?: "pkg" | "dmg" | "installer" | "portable" | "archive" | string;
  installTarget?: string;
  installerDefault?: boolean;
  portable?: boolean;
}>;

type CodexLatestDownload = {
  status: string;
  source: "official" | "mirror" | string;
  projectUrl: string;
  releaseUrl: string;
  releaseName?: string;
  tagName?: string;
  publishedAt?: string;
  assetName?: string;
  downloadUrl?: string;
  size?: number;
  contentType?: string;
  message?: string;
};

type InstallGuideStatusResult = CommandResult<{
  platform: string;
  arch: string;
  platformLabel?: string;
  archLabel?: string;
  desktopRuntime?: string;
  desktopRuntimeStatus?: "desktop" | "browser" | string;
  platformGuide?: PlatformGuide;
  onboardingCompleted?: boolean;
  onboardingCompletedAt?: string;
  onboardingCompletedPlatform?: string;
  onboardingCompletedForCurrentPlatform?: boolean;
  onboardingPlatformMismatch?: boolean;
  codexApp: PathState;
  codexVersion: string | null;
  codexDetection?: {
    status: string;
    message: string;
    savedPath?: string | null;
    resolvedPath?: string | null;
    executable?: string;
    appUserModelId?: string;
    candidates?: string[];
  };
  codexLaunch?: CodexLaunchStatus;
  codexInstallUrl: string;
  codexInstallSource: string;
  codexLatestDownload: CodexLatestDownload;
  ccs: {
    installed: boolean;
    dbPath: string;
    dbPathCandidates?: string[];
    providerCount: number;
    readError: string;
  };
  settingsPath: string;
  activeMode: RelayMode;
  relay?: RelayResult;
  connection?: InstallGuideConnectionStatus;
}>;

type BackendSettings = {
  codexAppPath: string;
  codexExtraArgs: string[];
  language: LanguageCode;
  providerSyncEnabled: boolean;
  providerSyncSavedProviders: string[];
  providerSyncManualProviders: string[];
  providerSyncLastSelectedProvider: string;
  relayProfilesEnabled: boolean;
  ccsLinkEnabled: boolean;
  enhancementsEnabled: boolean;
  codexAppPluginMarketplaceUnlock: boolean;
  codexAppPluginAutoExpand: boolean;
  codexAppModelWhitelistUnlock: boolean;
  codexAppSessionDelete: boolean;
  codexAppMarkdownExport: boolean;
  codexAppPasteFix: boolean;
  codexAppForceChineseLocale: boolean;
  codexAppFastStartup: boolean;
  codexAppProjectMove: boolean;
  codexAppConversationTimeline: boolean;
  codexAppThreadIdBadge: boolean;
  codexAppConversationView: boolean;
  codexAppThreadScrollRestore: boolean;
  codexAppZedRemoteOpen: boolean;
  codexAppUpstreamWorktreeCreate: boolean;
  codexAppNativeMenuPlacement: boolean;
  codexAppNativeMenuLocalization: boolean;
  codexAppServiceTierControls: boolean;
  codexAppStepwiseEnabled: boolean;
  codexAppStepwiseDirectSend: boolean;
  codexAppStepwiseBaseUrl: string;
  codexAppStepwiseApiKey: string;
  codexAppStepwiseApiKeyEnv: string;
  codexAppStepwiseModel: string;
  codexAppStepwiseMaxItems: number;
  codexAppStepwiseMaxInputChars: number;
  codexAppStepwiseMaxOutputTokens: number;
  codexAppStepwiseTimeoutMs: number;
  codexAppPetRealMouseLook: boolean;
  codexAppDreamSkinEnabled: boolean;
  codexAppDreamSkinPaused: boolean;
  codexAppDreamSkinTheme: string;
  codexAppDreamSkinThemeConfig: Record<string, unknown>;
  codexAppDreamSkinImagePath: string;
  computerUseGuardEnabled: boolean;
  zedRemoteOpenStrategy: ZedOpenStrategy;
  zedRemoteProjectRegistryEnabled: boolean;
  zedRemoteSyncToZedSettings: boolean;
  proxyEnabled: boolean;
  proxyUrl: string;
  proxyRelayEnabled: boolean;
  proxyRemoteControlEnabled: boolean;
  proxyOfficialAuthEnabled: boolean;
  proxyRealtimeEnabled: boolean;
  proxyModelCatalogEnabled: boolean;
  proxyAudioEnabled: boolean;
  proxyVlmEnabled: boolean;
  proxyStepwiseEnabled: boolean;
  proxyNoProxy: string;
  codexAppImageOverlayEnabled: boolean;
  codexAppImageOverlayPath: string;
  codexAppImageOverlayOpacity: number;
  codexAppImageOverlayFitMode: "fit" | "fill" | "stretch" | "tile" | "center";
  codexGoalsEnabled: boolean;
  mobileControlEnabled: boolean;
  mobileControlRelayUrl: string;
  mobileControlRoom: string;
  mobileControlKey: string;
  onboardingCompleted: boolean;
  onboardingCompletedAt: string;
  onboardingCompletedPlatform: string;
  launchMode: LaunchMode;
  relayBaseUrl: string;
  relayApiKey: string;
  relayProfiles: RelayProfile[];
  relayCommonConfigContents: string;
  relayContextConfigContents: string;
  activeRelayId: string;
  aggregateRelayProfiles: AggregateRelayProfile[];
  activeAggregateRelayId: string;
  relayTestModel: string;
  cliWrapperEnabled: boolean;
  cliWrapperBaseUrl: string;
  cliWrapperApiKey: string;
  cliWrapperApiKeyEnv: string;
  officialRealtimeAvailable?: boolean;
  officialRealtimeReason?: string;
  officialRealtimeMessage?: string;
};

type LaunchMode = "patch" | "relay";

type RelayProfile = {
  id: string;
  linkedCcsProviderId: string;
  name: string;
  model: string;
  baseUrl: string;
  upstreamBaseUrl: string;
  apiKey: string;
  imageGenerationEnabled: boolean;
  imageGenerationUseSeparateApi: boolean;
  imageGenerationBaseUrl: string;
  imageGenerationApiKey: string;
  protocol: RelayProtocol;
  relayMode: RelayMode;
  officialMixApiKey: boolean;
  officialAuthContents: string;
  officialAccountLabel: string;
  officialAuthUpdatedAt: string;
  hideOfficialUsageAlert: boolean;
  testModel: string;
  configContents: string;
  authContents: string;
  useCommonConfig: boolean;
  contextSelection: RelayContextSelection;
  contextSelectionInitialized: boolean;
  contextWindow: string;
  autoCompactLimit: string;
  modelInsertMode: string;
  modelList: string;
  modelWindows: string;
  modelAutoCompact: string;
  modelRoutes: RelayModelRoute[];
  modelVlm: string;
  vlmApiKey: string;
  vlmModel: string;
  vlmBaseUrl: string;
  userAgent: string;
  sub2apiEnabled: boolean;
  sub2apiMultiplier: string;
  proxyEnabled: boolean;
  proxyUrl: string;
};

type AggregateRelayStrategy = "failover" | "conversationRoundRobin" | "requestRoundRobin" | "weightedRoundRobin";

type AggregateRelayMember = {
  relayId: string;
  weight: number;
};

type AggregateRelayProfile = {
  id: string;
  name: string;
  strategy: AggregateRelayStrategy;
  members: AggregateRelayMember[];
};

type RelayContextSelection = {
  mcpServers: string[];
  skills: string[];
  plugins: string[];
};

type ContextKind = "mcp" | "skill" | "plugin";

type CodexContextEntry = {
  id: string;
  kind: ContextKind;
  title: string;
  summary: string;
  tomlBody: string;
  enabled: boolean;
};

type CodexContextEntries = {
  mcpServers: CodexContextEntry[];
  skills: CodexContextEntry[];
  plugins: CodexContextEntry[];
};

type RelayProtocol = "responses" | "chatCompletions";
type RelayMode = "official" | "mixedApi" | "pureApi" | "aggregate";
type ZedOpenStrategy = "addToFocusedWorkspace" | "reuseWindow" | "newWindow" | "default";
type ZedRemoteProject = {
  id: string;
  label: string;
  hostId: string;
  ssh: { user: string; host: string; port?: number | null };
  path: string;
  url: string;
  source: "currentThread" | "codexRemoteProject" | "threadWorkspaceHint" | "sqliteThreadCwd" | "recent" | string;
  lastOpenedAtMs?: number | null;
  isCurrent: boolean;
};
type ZedRemoteProjectsResult = CommandResult<{
  projects: ZedRemoteProject[];
  removed?: number;
}>;
type ProviderPreset = {
  id: string;
  name: string;
  category: "official" | "aggregator" | "cn_official" | "third_party";
  baseUrl: string;
  upstreamBaseUrl?: string;
  protocol: RelayProtocol;
  model: string;
  testModel?: string;
  modelList?: string[];
  websiteUrl?: string;
};
const providerPresets: ProviderPreset[] = [
  {
    id: "openai-official",
    name: "OpenAI 官方登录",
    category: "official",
    baseUrl: "",
    protocol: "responses",
    model: "gpt-5",
    modelList: ["gpt-5", "gpt-5-mini", "gpt-5-codex"],
  },
  {
    id: "openai-compatible",
    name: "OpenAI Compatible",
    category: "third_party",
    baseUrl: "https://api.openai.com/v1",
    protocol: "responses",
    model: "gpt-5-mini",
    testModel: "gpt-5-mini",
    modelList: ["gpt-5", "gpt-5-mini", "gpt-5-codex"],
  },
  {
    id: "openrouter",
    name: "OpenRouter",
    category: "aggregator",
    baseUrl: "https://openrouter.ai/api/v1",
    protocol: "chatCompletions",
    model: "openai/gpt-5-mini",
    testModel: "openai/gpt-5-mini",
    modelList: ["openai/gpt-5", "openai/gpt-5-mini", "openai/gpt-5-codex"],
    websiteUrl: "https://openrouter.ai",
  },
  {
    id: "dashscope-compatible",
    name: "DashScope Compatible",
    category: "cn_official",
    baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    protocol: "chatCompletions",
    model: "qwen-plus",
    testModel: "qwen-plus",
    modelList: ["qwen-plus", "qwen-max", "qwen3-coder-plus"],
    websiteUrl: "https://dashscope.aliyun.com",
  },
];
const PROTOCOL_PROXY_BASE_URL = "http://127.0.0.1:57321/v1";
const LOCAL_RELAY_PROXY_BASE_URL = "http://127.0.0.1:57323/v1";
const SCRIPT_MARKET_REPOSITORY_URL = "https://github.com/BigPizzaV3/CodexPlusPlusScriptMarket";
const PROJECT_REPOSITORY_URL = "https://github.com/hereww/codextools";
const PROJECT_RELEASES_URL = "https://github.com/hereww/codextools/releases/latest";
const PROJECT_ISSUES_URL = "https://github.com/hereww/codextools/issues";
const TELEGRAM_COMMUNITY_URL = "https://t.me/wanai8";

const emptyContextSelection = (): RelayContextSelection => ({
  mcpServers: [],
  skills: [],
  plugins: [],
});

type UserScriptInventory = {
  enabled?: boolean;
  scripts?: Array<{
    key: string;
    name: string;
    source: string;
    enabled: boolean;
    status: string;
    error: string;
    market_id?: string;
    version?: string;
    installed?: boolean;
    source_url?: string;
    homepage?: string;
  }>;
};

type SettingsResult = CommandResult<{
  settings: BackendSettings;
  settings_path: string;
  user_scripts: UserScriptInventory;
  envConflicts?: EnvConflict[];
  pendingProviderImport?: ProviderImportRequest | null;
  codexInstallUrl?: string;
  repairCandidates?: string[];
}>;

type SettingsBackfillResult = CommandResult<{
  settings: BackendSettings;
}>;

type EnvConflict = {
  name: string;
  source: "process" | "user" | string;
  valuePresent: boolean;
};

type RelayResult = CommandResult<{
  activeMode?: RelayMode;
  selectedMode?: RelayMode;
  appliedMode?: RelayMode;
  configApplied?: boolean;
  maintenanceStatus?: string;
  authenticated: boolean;
  authSource: string;
  accountLabel: string | null;
  currentAuthenticated?: boolean;
  currentAuthSource?: string;
  currentAccountLabel?: string | null;
  officialAuthenticated?: boolean;
  officialAuthSource?: string;
  officialAccountLabel?: string | null;
  boundOfficialAuthenticated?: boolean;
  boundOfficialAuthSource?: string;
  boundOfficialAccountLabel?: string | null;
  boundOfficialProfileId?: string | null;
  boundOfficialProfileName?: string | null;
  officialRealtimeAvailable?: boolean;
  officialRealtimeReason?: string;
  officialRealtimeMessage?: string;
  configPath: string;
  configured: boolean;
  requiresOpenaiAuth: boolean;
  hasBearerToken: boolean;
  backupPath: string | null;
  pluginRepair?: {
    status: string;
    message?: string;
    pluginCount?: number;
    marketplaceCount?: number;
    mcpServerCount?: number;
    backupPath?: string | null;
    marketplaceRefreshStatus?: string;
    marketplaceRefreshSummary?: string;
    marketplaceRefreshError?: string;
  };
  providerSync?: {
    status: string;
    message?: string;
    targetProvider?: string;
    backupDir?: string | null;
    changedSessionFiles?: number;
    sqliteRowsUpdated?: number;
    partial?: boolean;
    rollbackStatus?: string;
  };
}>;

type RelayFilesResult = CommandResult<{
  configPath: string;
  authPath: string;
  configContents: string;
  authContents: string;
}>;

type ModeHistorySyncResult = CommandResult<{
  syncStatus: string;
  targetProvider: string;
  changedSessionFiles: number;
  sqliteRowsUpdated: number;
  backupDir: string | null;
  syncMessage: string;
  partial?: boolean;
  rollbackStatus?: string;
}>;

type ProviderSyncTarget = {
  id: string;
  sources: Array<"config" | "rollout" | "sqlite" | "manual" | string>;
  isCurrentProvider: boolean;
  isManual: boolean;
  isSaved: boolean;
};

type ProviderSyncTargetsResult = CommandResult<{
  currentProvider: string;
  targets: ProviderSyncTarget[];
}>;

type LocalSession = {
  id: string;
  title: string;
  cwd: string;
  modelProvider: string;
  archived: boolean;
  updatedAtMs: number;
  rolloutPath: string;
  dbPath: string;
};

type LocalSessionsResult = CommandResult<{
  dbPath: string;
  dbPaths: string[];
  sessions: LocalSession[];
  offset: number;
  limit: number;
  hasMore: boolean;
}>;

type SessionIndexCleanupCandidate = {
  id: string;
  threadName: string;
  updatedAt: string;
};

type SessionIndexCleanupPreviewResult = CommandResult<{
  snapshotSha256: string;
  candidates: SessionIndexCleanupCandidate[];
}>;

type SessionIndexCleanupApplyResult = CommandResult<{
  prunedEntries: number;
  backupDir?: string | null;
}>;

type RelayProfileTestResult = CommandResult<{
  httpStatus: number;
  endpoint: string;
  responsePreview: string;
}>;

type ContextEntriesResult = CommandResult<{
  settings: BackendSettings;
  entries: CodexContextEntries;
}>;

type LiveContextEntriesResult = CommandResult<{
  entries: CodexContextEntries;
}>;

type ExtractRelayCommonConfigResult = CommandResult<{
  commonConfigContents: string;
  contextConfigContents: string;
  profileConfigContents: string;
}>;

type RelayProfileModelsResult = CommandResult<{
  models: string[];
  endpoint: string;
}>;

type VLMTestResult = CommandResult<{
  vlmStatus: string;
  httpCode?: number;
  durationMs?: number;
  model?: string;
  description?: string;
  error?: string;
}>;

const VLM_TEST_MAX_IMAGE_BYTES = 10 * 1024 * 1024;

type Sub2APIBillingResult = CommandResult<{
  endpoint: string;
  groupRateMultiplier: number;
  userRateMultiplier: number | null;
  resolvedRateMultiplier: number;
  peakRateEnabled: boolean;
  peakRateMultiplier: number | null;
  appliedPeakMultiplier: number | null;
  effectiveRateMultiplier: number;
  observedAt: string;
}>;

type ProviderDoctorResult = CommandResult<{
  profileName: string;
  model: string;
  summary: string;
  recommendation: string;
  checks: Array<{
    id: string;
    title: string;
    status: "ok" | "warning" | "failed" | string;
    detail: string;
  }>;
}>;

type CcsProviderImport = {
  sourceId: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  protocol: RelayProtocol;
  configContents: string;
  authContents: string;
};

type ProviderImportRequest = {
  name: string;
  baseUrl: string;
  apiKey: string;
  wireApi: string;
  relayMode: string;
  configContents?: string;
  authContents?: string;
};

type ProviderImportResult = {
  imported: boolean;
  profileId: string;
  profileName: string;
};

type CcsProvidersResult = CommandResult<{
  dbPath: string;
  dbPathCandidates?: string[];
  providers: CcsProviderImport[];
}>;

type ComputerUseStatusResult = CommandResult<{
  platform: string;
  supported: boolean;
  codexHome: string;
  envEnabled: boolean;
  processEnv: string;
  userEnv: string;
  marketplaceRoot: string;
  marketplaceManifestPath: string;
  marketplacePluginPath: string;
  marketplaceReady: boolean;
  marketplaceManifest: boolean;
  marketplacePlugin: boolean;
  cacheLatest: boolean;
  cacheLatestPath: string;
  cacheVersion: string;
  configReady: boolean;
  configPath: string;
  configMarketplace: boolean;
  configPlugin: boolean;
  configNodeRepl: boolean;
  configWindows: boolean;
  helperTransport: boolean;
  helperTransportPath: string;
  backupPath?: string | null;
  allReady: boolean;
}>;

type SkillMCPBackupInfo = {
  id: string;
  createdAt: string;
  label: string;
  path: string;
  hasSkills: boolean;
  hasPluginCache: boolean;
  hasBundledMarket: boolean;
  hasConfigSnapshot: boolean;
  configSnapshotBytes: number;
  sizeBytes: number;
  restoreSourceBackup?: string;
  restoreConfigBackup?: string;
};

type SkillMCPBackupsResult = CommandResult<{
  backupRoot: string;
  backups: SkillMCPBackupInfo[];
  backup?: SkillMCPBackupInfo;
  currentBackup?: SkillMCPBackupInfo;
}>;

type LogsResult = CommandResult<{
  path: string;
  text: string;
  lines: number;
}>;

type DiagnosticsResult = CommandResult<{
  report: string;
}>;

type WatcherResult = CommandResult<{
  enabled: boolean;
  disabled_flag: string;
  platform?: string;
  install_supported?: boolean;
  run_value_name?: string;
  run_value?: string;
  run_value_installed?: boolean;
  startup_shortcut?: string;
  startup_shortcut_installed?: boolean;
  launcher_path?: string;
  launcher_arguments?: string;
}>;

type CodexConfigRepairResult = CommandResult<{
  backupPath?: string | null;
  pluginCount?: number;
  marketplaceCount?: number;
  mcpServerCount?: number;
  marketplaceRefreshStatus?: string;
  marketplaceRefreshSummary?: string;
  marketplaceRefreshError?: string;
  configChanged?: boolean;
  goalsEnabled?: boolean;
  configPath?: string;
  codexHome?: string;
}>;

type ConversationHistoryRepairResult = CommandResult<{
  taskId?: string;
  taskStatus?: "idle" | "running" | "cancelling" | "ok" | "failed" | "cancelled";
  phase?: string;
  percent?: number;
  detail?: string;
  cancelRequested?: boolean;
  processedFiles?: number;
  totalFiles?: number;
  processedBytes?: number;
  totalBytes?: number;
  currentFile?: string;
  scannedFiles?: number;
  scannedRecords?: number;
  invalidRecords?: number;
  changedFiles?: number;
  changedRecords?: number;
  repairedFiles?: number;
  repairedRecords?: number;
  changedBytes?: number;
  maxChangedFileBytes?: number;
  requiredSpaceBytes?: number;
  freeSpaceBytes?: number;
  activeProcesses?: string[];
  backupDir?: string | null;
}>;

type InstallResult = CommandResult<{
  silent_shortcut: { installed: boolean; path: string | null };
  management_shortcut: { installed: boolean; path: string | null };
}>;

type ScriptMarketItem = {
  id: string;
  name: string;
  description: string;
  version: string;
  author: string;
  tags: string[];
  homepage: string;
  script_url: string;
  sha256: string;
  installed: boolean;
  installedVersion: string;
  updateAvailable: boolean;
};

type ScriptMarketResult = CommandResult<{
  market: {
    status: string;
    message: string;
    indexUrl: string;
    updatedAt: string;
    scripts: ScriptMarketItem[];
  };
  user_scripts: UserScriptInventory;
}>;

function syncMarketInstalledState(current: ScriptMarketResult | null, userScripts: UserScriptInventory): ScriptMarketResult | null {
  if (!current) return current;
  const installed = new Map(
    (userScripts.scripts ?? [])
      .filter((script) => script.market_id)
      .map((script) => [script.market_id || "", script.version || ""]),
  );
  return {
    ...current,
    user_scripts: userScripts,
    market: {
      ...current.market,
      scripts: current.market.scripts.map((script) => {
        const installedVersion = installed.get(script.id) || "";
        return {
          ...script,
          installed: Boolean(installedVersion),
          installedVersion,
          updateAvailable: Boolean(installedVersion) && installedVersion !== script.version,
        };
      }),
    },
  };
}

type Route = "overview" | "installGuide" | "relay" | "proxy" | "context" | "enhance" | "userScripts" | "providerSync" | "maintenance" | "settings" | "logs" | "diagnostics" | "about";
type Theme = "dark" | "light";

type DreamSkinColorKey = "background" | "panel" | "accent" | "highlight" | "text" | "muted";

const dreamSkinDefaultColors: Record<DreamSkinColorKey, string> = {
  background: "#F7F4F5",
  panel: "#FFFFFF",
  accent: "#E25563",
  highlight: "#C93D4C",
  text: "#2B2224",
  muted: "#8A7A7D",
};

const defaultDreamSkinConfig = {
  schemaVersion: 1,
  id: "pink",
  name: "Dream Skin",
  appearance: "auto",
  colors: {
    ...dreamSkinDefaultColors,
    panelAlt: "#FFF7F8",
    accentAlt: "#F07A86",
    secondary: "#F3A8AF",
    line: "rgba(196, 120, 128, .22)",
  },
};

const routes: Array<{ id: Route; label: string; helper: string; group: "main" | "support"; icon: LucideIcon }> = [
  { id: "overview", label: "首页", helper: "启动和检查", group: "main", icon: LayoutDashboard },
  { id: "installGuide", label: "新手引导", helper: "安装和配置", group: "main", icon: Sparkles },
  { id: "relay", label: "连接服务", helper: "账号和 API", group: "main", icon: KeyRound },
  { id: "proxy", label: "代理设置", helper: "网络连接策略", group: "main", icon: Globe2 },
  { id: "context", label: "工具与插件", helper: "MCP / Skills / Plugins", group: "main", icon: FileCode2 },
  { id: "enhance", label: "界面功能", helper: "删除、导出、脚本", group: "main", icon: Hammer },
  { id: "userScripts", label: "脚本中心", helper: "市场和本地脚本", group: "main", icon: ScrollText },
  { id: "maintenance", label: "修复工具", helper: "入口和路径", group: "main", icon: Wrench },
  { id: "providerSync", label: "历史修复", helper: "旧对话可见", group: "support", icon: Link2 },
  { id: "settings", label: "高级设置", helper: "启动参数", group: "support", icon: Settings },
  { id: "logs", label: "运行日志", helper: "排查问题", group: "support", icon: ScrollText },
  { id: "diagnostics", label: "诊断报告", helper: "复制给开发者", group: "support", icon: Activity },
  { id: "about", label: "关于", helper: "版本和项目", group: "support", icon: Info },
];

const defaultSettings: BackendSettings = {
  codexAppPath: "",
  codexExtraArgs: [],
  language: defaultLanguage,
  providerSyncEnabled: false,
  providerSyncSavedProviders: [],
  providerSyncManualProviders: [],
  providerSyncLastSelectedProvider: "",
  relayProfilesEnabled: true,
  ccsLinkEnabled: false,
  enhancementsEnabled: true,
  codexAppPluginMarketplaceUnlock: true,
  codexAppPluginAutoExpand: false,
  codexAppModelWhitelistUnlock: true,
  codexAppSessionDelete: true,
  codexAppMarkdownExport: true,
  codexAppPasteFix: false,
  codexAppForceChineseLocale: true,
  codexAppFastStartup: false,
  codexAppProjectMove: true,
  codexAppConversationTimeline: false,
  codexAppThreadIdBadge: false,
  codexAppConversationView: false,
  codexAppThreadScrollRestore: true,
  codexAppZedRemoteOpen: true,
  codexAppUpstreamWorktreeCreate: true,
  codexAppNativeMenuPlacement: true,
  codexAppNativeMenuLocalization: true,
  codexAppServiceTierControls: false,
  codexAppStepwiseEnabled: false,
  codexAppStepwiseDirectSend: false,
  codexAppStepwiseBaseUrl: "",
  codexAppStepwiseApiKey: "",
  codexAppStepwiseApiKeyEnv: "CODEX_STEPWISE_API_KEY",
  codexAppStepwiseModel: "",
  codexAppStepwiseMaxItems: 6,
  codexAppStepwiseMaxInputChars: 6000,
  codexAppStepwiseMaxOutputTokens: 500,
  codexAppStepwiseTimeoutMs: 8000,
  codexAppPetRealMouseLook: false,
  codexAppDreamSkinEnabled: false,
  codexAppDreamSkinPaused: false,
  codexAppDreamSkinTheme: "pink",
  codexAppDreamSkinThemeConfig: defaultDreamSkinConfig,
  codexAppDreamSkinImagePath: "",
  computerUseGuardEnabled: false,
  zedRemoteOpenStrategy: "addToFocusedWorkspace",
  zedRemoteProjectRegistryEnabled: true,
  zedRemoteSyncToZedSettings: false,
  proxyEnabled: false,
  proxyUrl: "",
  proxyRelayEnabled: false,
  proxyRemoteControlEnabled: false,
  proxyOfficialAuthEnabled: false,
  proxyRealtimeEnabled: false,
  proxyModelCatalogEnabled: false,
  proxyAudioEnabled: false,
  proxyVlmEnabled: false,
  proxyStepwiseEnabled: false,
  proxyNoProxy: "127.0.0.1,localhost,[::1]",
  codexAppImageOverlayEnabled: false,
  codexAppImageOverlayPath: "",
  codexAppImageOverlayOpacity: 35,
  codexAppImageOverlayFitMode: "fit",
  codexGoalsEnabled: false,
  mobileControlEnabled: false,
  mobileControlRelayUrl: "",
  mobileControlRoom: "",
  mobileControlKey: "",
  onboardingCompleted: false,
  onboardingCompletedAt: "",
  onboardingCompletedPlatform: "",
  launchMode: "patch",
  relayBaseUrl: "",
  relayApiKey: "",
  relayProfiles: [
    {
      id: "default",
      linkedCcsProviderId: "",
      name: "默认中转",
      model: "",
      baseUrl: "",
      upstreamBaseUrl: "",
      apiKey: "",
      imageGenerationEnabled: false,
      imageGenerationUseSeparateApi: false,
      imageGenerationBaseUrl: "",
      imageGenerationApiKey: "",
      protocol: "responses",
      relayMode: "official",
      officialMixApiKey: false,
      officialAuthContents: "",
      officialAccountLabel: "",
      officialAuthUpdatedAt: "",
      hideOfficialUsageAlert: false,
      testModel: "",
      configContents: "",
      authContents: "",
      useCommonConfig: true,
      contextSelection: emptyContextSelection(),
      contextSelectionInitialized: true,
      contextWindow: "",
      autoCompactLimit: "",
      modelInsertMode: "patch",
      modelList: "",
      modelWindows: "",
      modelAutoCompact: "",
      modelRoutes: [],
      modelVlm: "",
      vlmApiKey: "",
      vlmModel: "",
      vlmBaseUrl: "",
      userAgent: "",
      sub2apiEnabled: false,
      sub2apiMultiplier: "",
      proxyEnabled: false,
      proxyUrl: "",
    },
  ],
  relayCommonConfigContents: "",
  relayContextConfigContents: "",
  activeRelayId: "default",
  aggregateRelayProfiles: [],
  activeAggregateRelayId: "",
  relayTestModel: "gpt-5-mini",
  cliWrapperEnabled: false,
  cliWrapperBaseUrl: "",
  cliWrapperApiKey: "",
  cliWrapperApiKeyEnv: "CUSTOM_OPENAI_API_KEY",
};

export function App() {
  const [theme, setTheme] = useState<Theme>(() => loadInitialTheme());
  const [route, setRoute] = useState<Route>(() => loadInitialRoute());
  const [notice, setNotice] = useState<{ title: string; message: string; status?: Status } | null>(null);
  const [overview, setOverview] = useState<OverviewResult | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateResult | null>(null);
  const [installGuideStatus, setInstallGuideStatus] = useState<InstallGuideStatusResult | null>(null);
  const [settings, setSettings] = useState<SettingsResult | null>(null);
  const [pendingProviderImport, setPendingProviderImport] = useState<ProviderImportRequest | null>(null);
  const [relay, setRelay] = useState<RelayResult | null>(null);
  const [relayFiles, setRelayFiles] = useState<RelayFilesResult | null>(null);
  const [ccsProviders, setCcsProviders] = useState<CcsProvidersResult | null>(null);
  const [computerUse, setComputerUse] = useState<ComputerUseStatusResult | null>(null);
  const [zedRemoteProjects, setZedRemoteProjects] = useState<ZedRemoteProjectsResult | null>(null);
  const [localSessions, setLocalSessions] = useState<LocalSessionsResult | null>(null);
  const [providerSyncTargets, setProviderSyncTargets] = useState<ProviderSyncTargetsResult | null>(null);
  const [selectedProviderSyncTarget, setSelectedProviderSyncTarget] = useState("");
  const [sessionIndexCleanup, setSessionIndexCleanup] = useState<SessionIndexCleanupPreviewResult | null>(null);
  const [skillMcpBackups, setSkillMcpBackups] = useState<SkillMCPBackupsResult | null>(null);
  const [liveContextEntries, setLiveContextEntries] = useState<CodexContextEntries | null>(null);
  const [logs, setLogs] = useState<LogsResult | null>(null);
  const [diagnostics, setDiagnostics] = useState<DiagnosticsResult | null>(null);
  const [watcher, setWatcher] = useState<WatcherResult | null>(null);
  const [scriptMarket, setScriptMarket] = useState<ScriptMarketResult | null>(null);
  const [launchForm, setLaunchForm] = useState({
    appPath: "",
    debugPort: "9229",
    helperPort: "57321",
  });
  const [settingsForm, setSettingsForm] = useState<BackendSettings>({ ...defaultSettings });
  const [restartProgress, setRestartProgress] = useState<TaskProgress | null>(null);
  const [relaySwitchProgress, setRelaySwitchProgress] = useState<TaskProgress | null>(null);
  const [modeHistorySyncInProgress, setModeHistorySyncInProgress] = useState(false);
  const [lastModeHistorySync, setLastModeHistorySync] = useState<ModeHistorySyncResult | null>(null);
  const [conversationHistoryRepair, setConversationHistoryRepair] = useState<ConversationHistoryRepairResult | null>(null);
  const [removeOwnedData, setRemoveOwnedData] = useState(false);
  const currentLanguage = normalizeLanguage(settingsForm.language);
  const restartInProgress = restartProgress?.status === "running";
  const relaySwitchInProgress = relaySwitchProgress?.status === "running";
  const conversationHistoryRepairInProgress = isConversationHistoryRepairActive(conversationHistoryRepair);
  const conversationHistoryRepairPollVersion = useRef(0);
  const conversationHistoryRepairMounted = useRef(false);
  const conversationHistoryRepairNoticeKey = useRef("");
  const modeHistorySyncRunningRef = useRef(false);
  const relayMutationGeneration = useRef(0);
  const relayRefreshRequestSequence = useRef(0);
  const languageRef = useRef(currentLanguage);
  languageRef.current = currentLanguage;
  const tr = (value: string) => translateText(value, languageRef.current);
  const confirmMessage = (message: string) => window.confirm(tr(message));

  const commitRelayResult = (result: RelayResult) => {
    relayMutationGeneration.current += 1;
    setRelay((current) => ({
      ...result,
      providerSync: result.providerSync ?? current?.providerSync,
    }));
    if (result.providerSync) setLastModeHistorySync(() => null);
  };

  const call = <T,>(command: string, args?: Record<string, unknown>) => backendInvoke<T>(command, args);

  const run = async <T,>(task: () => Promise<T>): Promise<T | null> => {
    try {
      return await task();
    } catch (error) {
      showNotice("调用失败", stringifyError(error), "failed");
      return null;
    }
  };

  const refreshOverview = async (silent = false) => {
    const result = await run(() => call<OverviewResult>("load_overview"));
    if (result) {
      setOverview(result);
      if (!silent) showResultNotice("概览已检查", result, { silentSuccess: true });
    }
    return result;
  };

  const checkUpdate = async (silent = false) => {
    const result = await run(() => call<UpdateResult>("check_update"));
    if (result) {
      setUpdateInfo(result);
      setOverview((current) =>
        current
          ? {
              ...current,
              update_status: result.updateStatus,
              update: result,
            }
          : current,
      );
      if (result.updateStatus === "available" || !silent) {
        showResultNotice("版本更新", result, { silentSuccess: result.updateStatus !== "available" });
      }
    }
    return result;
  };

  const installUpdate = async () => {
    const result = await run(() => call<UpdateResult>("install_update"));
    if (result) {
      setUpdateInfo(result);
      setOverview((current) =>
        current
          ? {
              ...current,
              update_status: result.updateStatus,
              update: result,
            }
          : current,
      );
      showResultNotice("版本更新", result);
    }
  };

  const refreshInstallGuideStatus = async (silent = false) => {
    const result = await run(() => call<InstallGuideStatusResult>("load_install_guide_status"));
    if (result) {
      setInstallGuideStatus(result);
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("新手引导", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshSettings = async (silent = false) => {
    const result = await run(() => call<SettingsResult>("load_settings"));
    if (result) {
      setSettings(result);
      setPendingProviderImport(result.pendingProviderImport ?? null);
      setSettingsForm(normalizeSettings(result.settings));
      setLaunchForm((current) => ({
        ...current,
        appPath: current.appPath,
      }));
      if (!silent) showResultNotice("设置已加载", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshScriptMarket = async (silent = false) => {
    const result = await run(() => call<ScriptMarketResult>("refresh_script_market"));
    if (result) {
      setScriptMarket(result);
      setSettings((current) => (current ? { ...current, user_scripts: result.user_scripts } : current));
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("脚本市场", result, { silentSuccess: true });
    }
  };

  const installMarketScript = async (id: string) => {
    const result = await run(() => call<ScriptMarketResult>("install_market_script", { id }));
    if (result) {
      setScriptMarket(result);
      setSettings((current) => (current ? { ...current, user_scripts: result.user_scripts } : current));
      showResultNotice("脚本市场", result);
    }
  };

  const setUserScriptEnabled = async (key: string, enabled: boolean) => {
    const result = await run(() => call<SettingsResult>("set_user_script_enabled", { key, enabled }));
    if (result) {
      setSettings(result);
      setScriptMarket((current) => syncMarketInstalledState(current, result.user_scripts));
      showResultNotice("本地脚本", result);
    }
  };

  const deleteUserScript = async (key: string) => {
    const script = settings?.user_scripts?.scripts?.find((item) => item.key === key);
    const name = script?.name || key;
    if (!confirmMessage(`删除脚本“${name}”？此操作会移除本地脚本文件。`)) return;
    const result = await run(() => call<SettingsResult>("delete_user_script", { key }));
    if (result) {
      setSettings(result);
      setScriptMarket((current) => syncMarketInstalledState(current, result.user_scripts));
      showResultNotice("本地脚本", result);
    }
  };

  const refreshRelay = async (silent = false) => {
    const generationAtRequest = relayMutationGeneration.current;
    const requestSequence = ++relayRefreshRequestSequence.current;
    const result = await run(() => call<RelayResult>("relay_status"));
    if (
      result &&
      relayMutationGeneration.current === generationAtRequest &&
      relayRefreshRequestSequence.current === requestSequence
    ) {
      setRelay((current) => ({
        ...result,
        providerSync: result.providerSync ?? current?.providerSync,
      }));
      if (!silent) showResultNotice("登录状态", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshRelayFiles = async (silent = false) => {
    const result = await run(() => call<RelayFilesResult>("read_relay_files"));
    if (result) {
      setRelayFiles(result);
      if (!silent) showResultNotice("配置文件", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshLiveContextEntries = async (silent = false) => {
    const result = await run(() => call<LiveContextEntriesResult>("read_live_context_entries"));
    if (result) {
      setLiveContextEntries(normalizeContextEntries(result.entries));
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("工具与插件", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshCcsProviders = async (silent = false) => {
    const result = await run(() => call<CcsProvidersResult>("load_ccs_providers"));
    if (result) {
      setCcsProviders(result);
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("CCS 供应商", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshComputerUse = async (silent = false) => {
    const result = await run(() => call<ComputerUseStatusResult>("load_computer_use_status"));
    if (result) {
      setComputerUse(result);
      if (!silent || result.status === "failed") showResultNotice("Computer Use", result, { silentSuccess: true });
    }
    return result;
  };

  const repairComputerUse = async () => {
    const result = await run(() => call<ComputerUseStatusResult>("repair_computer_use"));
    if (result) {
      setComputerUse(result);
      showResultNotice("Computer Use 修复", result);
      await refreshRelayFiles(true);
    }
  };

  const refreshLocalSessions = async (silent = false, offset = 0) => {
    const result = await run(() =>
      call<LocalSessionsResult>("list_local_sessions", { request: { offset, limit: 50 } }),
    );
    if (result) {
      if (!result.sessions.length && result.offset > 0) {
        return refreshLocalSessions(silent, Math.max(0, result.offset - result.limit));
      }
      setLocalSessions(result);
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("会话管理", result, { silentSuccess: true });
    }
    return result;
  };

  const refreshProviderSyncTargets = async (silent = false) => {
    const result = await run(() => call<ProviderSyncTargetsResult>("load_provider_sync_targets"));
    if (result) {
      setProviderSyncTargets(result);
      setSelectedProviderSyncTarget((current) => {
        if (result.targets.some((target) => target.id === current)) return current;
        const preferred = settingsForm.providerSyncLastSelectedProvider;
        if (result.targets.some((target) => target.id === preferred)) return preferred;
        return result.currentProvider || result.targets[0]?.id || "";
      });
      if (!silent || !isSuccessStatus(result.status)) {
        showResultNotice("Provider 同步目标", result, { silentSuccess: true });
      }
    }
    return result;
  };

  const deleteLocalSession = async (session: LocalSession) => {
    const title = session.title || session.id;
    if (!confirmMessage(`删除会话“${title}”？本地数据库和 rollout 文件会一并删除，并先创建恢复备份。`)) return;
    const result = await run(() =>
      call<CommandResult<{ undo_token?: string }>>("delete_local_session", {
        request: { sessionId: session.id, title: session.title, dbPath: session.dbPath },
      }),
    );
    if (result) {
      showResultNotice("会话删除", result);
      if (isSuccessStatus(result.status)) await refreshLocalSessions(true, localSessions?.offset ?? 0);
    }
  };

  const previewSessionIndexCleanup = async () => {
    const result = await run(() => call<SessionIndexCleanupPreviewResult>("preview_session_index_cleanup"));
    if (result) {
      setSessionIndexCleanup(result);
      showResultNotice("任务索引检查", result, { silentSuccess: result.candidates.length > 0 });
    }
    return result;
  };

  const applySessionIndexCleanup = async (threadIds: string[]) => {
    if (!sessionIndexCleanup || !threadIds.length) return;
    if (!confirmMessage(`清理选中的 ${threadIds.length} 条任务索引？必须先完全退出 ChatGPT 和 Codex，原索引会先完整备份。`)) return;
    const result = await run(() =>
      call<SessionIndexCleanupApplyResult>("apply_session_index_cleanup", {
        snapshotSha256: sessionIndexCleanup.snapshotSha256,
        threadIds,
      }),
    );
    if (result) {
      showResultNotice("任务索引清理", result);
      if (isSuccessStatus(result.status)) {
        await previewSessionIndexCleanup();
        await refreshLocalSessions(true, localSessions?.offset ?? 0);
      }
    }
  };

  const refreshZedRemoteProjects = async (silent = false) => {
    const result = await run(() => call<ZedRemoteProjectsResult>("zed_remote_projects"));
    if (result) {
      setZedRemoteProjects(result);
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("Zed Remote", result, { silentSuccess: true });
    }
    return result;
  };

  const openZedRemoteProject = async (project: ZedRemoteProject, strategy?: ZedOpenStrategy) => {
    const result = await run(() =>
      call<CommandResult<Record<string, unknown>>>("zed_remote_open", {
        ...project,
        ssh: project.ssh,
        path: project.path,
        hostId: project.hostId,
        label: project.label,
        strategy: strategy || settingsForm.zedRemoteOpenStrategy,
        remember: settingsForm.zedRemoteProjectRegistryEnabled !== false,
      }),
    );
    if (result) {
      showNotice("Zed Remote", result.message, result.status);
      await refreshZedRemoteProjects(true);
    }
  };

  const forgetZedRemoteProject = async (project: ZedRemoteProject) => {
    const result = await run(() => call<ZedRemoteProjectsResult>("zed_remote_forget_project", { id: project.id }));
    if (result) {
      setZedRemoteProjects(result);
      showResultNotice("Zed Remote", result);
      await refreshZedRemoteProjects(true);
    }
  };

  const chooseImageOverlayPath = async () => {
    const selected = await openFileDialog({
      directory: false,
      multiple: false,
      title: tr("选择覆盖图片"),
      filters: [{ name: tr("图片"), extensions: ["png", "jpg", "jpeg", "webp", "gif", "bmp"] }],
    });
    if (typeof selected !== "string" || !selected.trim()) {
      showNotice("图片覆盖层", "未选择图片。", "not_checked");
      return;
    }
    const next: BackendSettings = {
      ...settingsForm,
      codexAppImageOverlayEnabled: true,
      codexAppImageOverlayPath: selected.trim(),
    };
    setSettingsForm(next);
    const result = await saveSettingsValue(next, true);
    if (result) showNotice("图片覆盖层", "图片路径已保存，重启 ChatGPT 或刷新注入后生效。", result.status);
  };

  const resetImageOverlaySettings = async () => {
    const next: BackendSettings = {
      ...settingsForm,
      codexAppImageOverlayEnabled: false,
      codexAppImageOverlayPath: "",
      codexAppImageOverlayOpacity: 35,
      codexAppImageOverlayFitMode: "fit",
    };
    setSettingsForm(next);
    const result = await saveSettingsValue(next, true);
    if (result) showNotice("图片覆盖层", "图片覆盖设置已重置。", result.status);
  };

  const chooseDreamSkinImagePath = async () => {
    const selected = await openFileDialog({
      directory: false,
      multiple: false,
      title: tr("选择 DreamSkin 背景图片"),
      filters: [{ name: tr("图片"), extensions: ["png", "jpg", "jpeg", "heic", "tif", "tiff", "webp", "gif", "bmp"] }],
    });
    if (typeof selected !== "string" || !selected.trim()) {
      showNotice("DreamSkin", "未选择图片。", "not_checked");
      return;
    }
    const result = await run(() => call<SettingsResult>("import_dream_skin_image", { path: selected.trim() }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("DreamSkin", result.message, result.status);
    }
  };

  const refreshSkillMcpBackups = async (silent = false) => {
    const result = await run(() => call<SkillMCPBackupsResult>("list_skill_mcp_backups"));
    if (result) {
      setSkillMcpBackups(result);
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("Skill/MCP 备份", result, { silentSuccess: true });
    }
    return result;
  };

  const createSkillMcpBackup = async () => {
    const result = await run(() => call<SkillMCPBackupsResult>("create_skill_mcp_backup", { request: { label: "manual" } }));
    if (result) {
      setSkillMcpBackups(result);
      showResultNotice("Skill/MCP 备份", result);
    }
  };

  const restoreSkillMcpBackup = async (id: string) => {
    if (!confirmMessage(`恢复 Skill/MCP 备份“${id}”？恢复前会先备份当前状态。`)) return;
    const result = await run(() => call<SkillMCPBackupsResult>("restore_skill_mcp_backup", { request: { id } }));
    if (result) {
      setSkillMcpBackups(result);
      showResultNotice("Skill/MCP 恢复", result);
      await refreshComputerUse(true);
      await refreshRelayFiles(true);
    }
  };

  const deleteSkillMcpBackup = async (id: string) => {
    if (!confirmMessage(`删除 Skill/MCP 备份“${id}”？此操作不可撤销。`)) return;
    const result = await run(() => call<SkillMCPBackupsResult>("delete_skill_mcp_backup", { request: { id } }));
    if (result) {
      setSkillMcpBackups(result);
      showResultNotice("Skill/MCP 删除", result);
    }
  };

  const refreshToolHealth = async (silent = false) => {
    const [overviewResult, guideResult, relayResult, ccsResult] = await Promise.all([
      refreshOverview(true),
      refreshInstallGuideStatus(true),
      refreshRelay(true),
      refreshCcsProviders(true),
    ]);
    await refreshSettings(true);
    await refreshWatcher(true);
    if (!silent) {
      const readyParts = [
        overviewResult?.codex_app.status === "found" || guideResult?.codexLaunch?.ready ? "ChatGPT 可启动" : "ChatGPT 待修复",
        ccsResult?.providers?.length ? `CCSwitch ${ccsResult.providers.length} 个供应商` : guideResult?.ccs.installed ? "CCSwitch 已发现" : "CCSwitch 未发现",
        relayOfficialAuthenticated(relayResult ?? null) ? "官方账号已识别" : guideResult?.connection?.officialReady ? "官方绑定已识别" : "官方账号待绑定",
        relayResult?.configured || guideResult?.connection?.apiReady ? "服务器配置已识别" : "服务器配置待补充",
      ];
      showNotice("检查完成", readyParts.join("；") + "。", "ok");
    }
    return { overviewResult, guideResult, relayResult, ccsResult };
  };

  const refreshLogs = async (silent = false) => {
    const result = await run(() => call<LogsResult>("read_latest_logs", { request: { lines: 240 } }));
    if (result) {
      setLogs(result);
      if (!silent) showResultNotice("日志已刷新", result, { silentSuccess: true });
    }
  };

  const refreshDiagnostics = async (silent = false) => {
    const result = await run(() => call<DiagnosticsResult>("copy_diagnostics"));
    if (result) {
      setDiagnostics(result);
      if (!silent) showResultNotice("诊断已生成", result, { silentSuccess: true });
    }
  };

  const refreshWatcher = async (silent = false) => {
    const result = await run(() => call<WatcherResult>("load_watcher_state"));
    if (result) {
      setWatcher(result);
      if (!silent) showResultNotice("Watcher 状态", result, { silentSuccess: true });
    }
    return result;
  };

  const navigate = async (next: Route) => {
    setRoute(next);
    if (next === "overview") await refreshOverview(true);
    if (next === "installGuide") {
      await refreshInstallGuideStatus(true);
      await refreshOverview(true);
      await refreshSettings(true);
      await refreshRelay(true);
      await refreshCcsProviders(true);
    }
    if (next === "relay") {
      await refreshSettings(true);
      await refreshRelay(true);
      await refreshRelayFiles(true);
      await refreshCcsProviders(true);
    }
    if (next === "context") {
      await refreshSettings(true);
      await refreshRelayFiles(true);
      await refreshLiveContextEntries(true);
    }
    if (next === "userScripts") {
      await refreshSettings(true);
      await refreshScriptMarket(true);
    }
    if (next === "settings") await refreshSettings(true);
    if (next === "providerSync") {
      await refreshSettings(true);
      await refreshProviderSyncTargets(true);
      await refreshComputerUse(true);
      await refreshSkillMcpBackups(true);
      await refreshLocalSessions(true, localSessions?.offset ?? 0);
    }
    if (next === "enhance") {
      await refreshSettings(true);
      await refreshZedRemoteProjects(true);
    }
    if (next === "logs") await refreshLogs(true);
    if (next === "diagnostics") await refreshDiagnostics(true);
    if (next === "maintenance") {
      await refreshOverview(true);
      await refreshWatcher(true);
      void pollConversationHistoryRepair(false);
    }
    if (next === "about") {
      await refreshOverview(true);
      await checkUpdate(true);
    }
  };

  const launch = async () => {
    const result = await launchCommand("launch_codex_plus");
    if (result) {
      showNotice("启动任务", result.message, result.status);
      await refreshOverview(true);
    }
  };

  const restart = async () => {
    if (restartInProgress) return;
    setRestartProgress({
      id: "restart",
      label: "重启 ChatGPT",
      detail: "正在提交重启请求。",
      percent: 10,
      status: "running",
    });
    const result = await launchCommand("restart_codex_plus");
    if (!result) {
      setRestartProgress({
        id: "restart",
        label: "重启 ChatGPT",
        detail: "重启请求没有返回结果，请查看日志。",
        percent: 100,
        status: "failed",
      });
      return;
    }
    showNotice("重启 ChatGPT", result.message, result.status);
    if (!isSuccessStatus(result.status)) {
      setRestartProgress({
        id: "restart",
        label: "重启 ChatGPT",
        detail: result.message || "重启请求失败。",
        percent: 100,
        status: "failed",
      });
      await refreshOverview(true);
      return;
    }
    setRestartProgress({
      id: "restart",
      label: "重启 ChatGPT",
      detail: "正在等待旧实例退出并释放端口。",
      percent: 35,
      status: "running",
    });
    await trackRestartProgress();
  };

  const trackRestartProgress = async () => {
    const deadline = Date.now() + 65000;
    let bestPercent = 35;
    while (Date.now() < deadline) {
      await delay(900);
      const current = await refreshOverview(true);
      const latest = current?.latest_launch ?? null;
      const latestStatus = latest?.status || "";
      if (latestStatus === "failed") {
        setRestartProgress({
          id: "restart",
          label: "重启 ChatGPT",
          detail: latest?.message || "重启失败，请查看日志。",
          percent: 100,
          status: "failed",
        });
        return;
      }
      if (latestStatus === "running" || latestStatus === "degraded") {
        setRestartProgress({
          id: "restart",
          label: "重启 ChatGPT",
          detail: latestStatus === "degraded" ? "ChatGPT 已启动，增强注入正在后台重试。" : "ChatGPT 已重新启动。",
          percent: 100,
          status: "ok",
        });
        window.setTimeout(() => {
          setRestartProgress((current) => current?.id === "restart" && current.status === "ok" ? null : current);
        }, 2400);
        return;
      }
      if (latestStatus === "starting") {
        bestPercent = Math.max(bestPercent, 70);
        setRestartProgress({
          id: "restart",
          label: "重启 ChatGPT",
          detail: latest?.message || "新实例正在启动并等待注入。",
          percent: bestPercent,
          status: "running",
        });
        continue;
      }
      if (latestStatus === "restarting") {
        bestPercent = Math.max(bestPercent, 45);
        setRestartProgress({
          id: "restart",
          label: "重启 ChatGPT",
          detail: latest?.message || "正在关闭旧实例并释放端口。",
          percent: bestPercent,
          status: "running",
        });
        continue;
      }
      if (latestStatus === "accepted") {
        bestPercent = Math.max(bestPercent, 55);
        setRestartProgress({
          id: "restart",
          label: "重启 ChatGPT",
          detail: latest?.message || "启动任务已进入后台。",
          percent: bestPercent,
          status: "running",
        });
      }
    }
    setRestartProgress({
      id: "restart",
      label: "重启 ChatGPT",
      detail: "重启仍在后台启动，可打开日志确认最新状态。",
      percent: Math.max(bestPercent, 70),
      status: "failed",
    });
  };

  const launchCommand = async (command: "launch_codex_plus" | "restart_codex_plus") => {
    const result = await run(() =>
      call<CommandResult<Record<string, unknown>>>(command, {
        request: {
          appPath: launchForm.appPath,
          debugPort: numberOrDefault(launchForm.debugPort, 9229),
          helperPort: numberOrDefault(launchForm.helperPort, 57321),
        },
      }),
    );
    return result;
  };

  const repairBackend = async () => {
    const result = await run(() => call<SettingsResult>("repair_backend"));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("后端修复", result.message, result.status);
    }
  };

  const repairCodexApp = async () => {
    const result = await run(() => call<SettingsResult>("repair_codex_app"));
    if (result) {
      const normalized = normalizeSettings(result.settings);
      setSettings(result);
      setSettingsForm(normalized);
      setLaunchForm((current) => ({ ...current, appPath: "" }));
      const installUrl = result.codexInstallUrl;
      const shouldOpenInstaller = !isSuccessStatus(result.status) && Boolean(installUrl);
      showNotice("ChatGPT 应用修复", shouldOpenInstaller ? `${result.message} 正在打开官方下载页。` : result.message, result.status);
      if (shouldOpenInstaller && installUrl) {
        await openExternalUrl(installUrl);
      }
      await refreshOverview(true);
      await refreshInstallGuideStatus(true);
    }
  };

  const installEntrypoints = async () => {
    const result = await run(() => call<InstallResult>("install_entrypoints"));
    if (result) {
      showNotice("入口安装", result.message, result.status);
      await refreshOverview(true);
    }
  };

  const uninstallEntrypoints = async () => {
    const result = await run(() =>
      call<InstallResult>("uninstall_entrypoints", {
        options: { removeOwnedData },
      }),
    );
    if (result) {
      showNotice("入口卸载", result.message, result.status);
      await refreshOverview(true);
    }
  };

  const uninstallCodexTools = async () => {
    if (!confirmMessage("将启动 Windows 卸载程序，并先移除 ChatGPT Codex 入口和 watcher。继续？")) return;
    const result = await run(() =>
      call<CommandResult<Record<string, unknown>>>("uninstall_codextools", {
        options: { removeOwnedData },
      }),
    );
    if (result) {
      showNotice("Windows 卸载", result.message, result.status);
      await refreshOverview(true);
      await refreshWatcher(true);
    }
  };

  const repairShortcuts = async () => {
    const result = await run(() => call<InstallResult>("repair_shortcuts"));
    if (result) {
      showNotice("快捷方式修复", result.message, result.status);
      await refreshOverview(true);
    }
  };

  const watcherAction = async (command: string) => {
    const result = await run(() => call<WatcherResult>(command));
    if (result) {
      setWatcher(result);
      showNotice("Watcher 操作", result.message, result.status);
    }
  };

  const saveSettings = async () => {
    const result = await run(() => call<SettingsResult>("save_settings", { settings: settingsForm }));
    if (result) {
      setSettings(result);
      setPendingProviderImport(result.pendingProviderImport ?? null);
      if (isSuccessStatus(result.status)) setSettingsForm(normalizeSettings(result.settings));
      showNotice("设置保存", result.message, result.status);
    }
  };

  const saveSettingsValue = async (next: BackendSettings, silent = true) => {
    setSettingsForm(next);
    const result = await run(() => call<SettingsResult>("save_settings", { settings: next }));
    if (result) {
      setSettings(result);
      setPendingProviderImport(result.pendingProviderImport ?? null);
      if (isSuccessStatus(result.status)) setSettingsForm(normalizeSettings(result.settings));
      if (!silent || !isSuccessStatus(result.status)) showNotice("设置保存", result.message, result.status);
    }
    return result;
  };

  const checkEnvConflicts = async () => {
    const result = await run(() => call<SettingsResult>("check_env_conflicts"));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("环境变量冲突", result.message, result.status);
    }
    return result;
  };

  const removeEnvConflicts = async (names: string[]) => {
    const result = await run(() => call<SettingsResult>("remove_env_conflicts", { names }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("环境变量冲突", result.message, result.status);
    }
    return result;
  };

  const changeLanguage = async (language: LanguageCode) => {
    const next = { ...settingsForm, language: normalizeLanguage(language) };
    setSettingsForm(next);
    const result = await run(() => call<SettingsResult>("save_settings", { settings: next }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      if (!isSuccessStatus(result.status)) showNotice("语言", result.message, result.status);
    }
  };

  const importCcsProviders = async () => {
    const result = await run(() => call<SettingsResult>("import_ccs_providers"));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      await refreshCcsProviders(true);
      await refreshInstallGuideStatus(true);
      await refreshRelay(true);
      showResultNotice("导入 CCSwitch 配置", result);
    }
  };

  const confirmPendingProviderImport = async () => {
    const result = await run(() => call<SettingsResult & { providerImportResult?: ProviderImportResult }>("confirm_pending_provider_import"));
    if (result) {
      setSettings(result);
      setPendingProviderImport(result.pendingProviderImport ?? null);
      setSettingsForm(normalizeSettings(result.settings));
      await refreshRelay(true);
      showResultNotice("供应商导入", result);
    }
  };

  const dismissPendingProviderImport = async () => {
    const result = await run(() => call<SettingsResult>("dismiss_pending_provider_import"));
    if (result) {
      setSettings(result);
      setPendingProviderImport(result.pendingProviderImport ?? null);
      setSettingsForm(normalizeSettings(result.settings));
      showResultNotice("供应商导入", result);
    }
  };

  const resetSettings = async () => {
    const result = await run(() => call<SettingsResult>("reset_settings"));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("设置重置", result.message, result.status);
    }
  };

  const syncProvidersNow = async () => {
    if (modeHistorySyncRunningRef.current) return;
    if (
      !confirmMessage(
        "将按当前模式同步对话历史归属。必须先完全退出 ChatGPT 和 Codex；检测到仍在运行时会安全停止且不修改聊天记录。同步前会创建完整备份，只更新模式归属和本地索引，不会删除消息正文。是否继续？",
      )
    ) {
      return;
    }
    modeHistorySyncRunningRef.current = true;
    setModeHistorySyncInProgress(true);
    try {
      const result = await run(() => call<ModeHistorySyncResult>("sync_providers_now", {
        targetProvider: selectedProviderSyncTarget || undefined,
      }));
      if (result) {
        relayMutationGeneration.current += 1;
        setLastModeHistorySync(() => result);
        setRelay((current) => current
          ? {
              ...current,
              providerSync: {
                status: result.syncStatus,
                message: result.syncMessage || result.message,
                targetProvider: result.targetProvider,
                backupDir: result.backupDir,
                changedSessionFiles: result.changedSessionFiles,
                sqliteRowsUpdated: result.sqliteRowsUpdated,
                partial: result.partial,
                rollbackStatus: result.rollbackStatus,
              },
            }
          : current);
        const backup = result.backupDir ? ` 备份：${result.backupDir}` : "";
        showNotice("同步模式对话历史", `${result.message}${backup}`, result.status);
        if (isSuccessStatus(result.status)) {
          const syncedTarget = result.targetProvider || selectedProviderSyncTarget;
          if (syncedTarget) {
            const nextSettings = {
              ...settingsForm,
              providerSyncLastSelectedProvider: syncedTarget,
              providerSyncSavedProviders: uniqueStringList([
                ...settingsForm.providerSyncSavedProviders,
                syncedTarget,
              ]),
            };
            await saveSettingsValue(nextSettings, true);
          }
          await refreshProviderSyncTargets(true);
          await previewSessionIndexCleanup();
          await refreshLocalSessions(true, localSessions?.offset ?? 0);
        }
      }
    } finally {
      modeHistorySyncRunningRef.current = false;
      setModeHistorySyncInProgress(false);
    }
  };

  const showConversationHistoryRepairOutcome = (result: ConversationHistoryRepairResult) => {
    const taskStatus = result.taskStatus ?? (result.status === "failed" ? "failed" : "ok");
    const repairedFiles = result.repairedFiles ?? result.changedFiles ?? 0;
    const repairedRecords = result.repairedRecords ?? result.changedRecords ?? 0;
    const summary = tr(
      taskStatus === "ok"
        ? `扫描 ${result.scannedFiles ?? 0} 个文件、${result.scannedRecords ?? 0} 条记录；修改 ${repairedFiles} 个文件、修复 ${repairedRecords} 条记录。`
        : `扫描 ${result.scannedFiles ?? 0} 个文件、${result.scannedRecords ?? 0} 条记录；已修复 ${repairedFiles} 个文件、${repairedRecords} 条记录。`,
    );
    let outcome = result.message;
    let noticeStatus: Status = result.status;
    if (taskStatus === "ok") {
      outcome = tr(repairedFiles > 0 ? "对话历史兼容修复已完成。" : "未发现需要修复的对话历史。");
      noticeStatus = "ok";
    } else if (taskStatus === "cancelled") {
      outcome = tr(result.detail || "对话历史修复已取消，已完成且已备份的文件会保留。可以稍后重新执行以继续修复。");
      noticeStatus = "not_checked";
    } else if (taskStatus === "failed") {
      outcome = tr(result.detail || result.message);
      noticeStatus = "failed";
    }
    const backup = result.backupDir ? ` ${tr(`备份：${result.backupDir}`)}` : "";
    showNotice(tr("对话历史兼容修复"), `${outcome} ${summary}${backup}`.trim(), noticeStatus);
  };

  const rememberConversationHistoryRepair = (result: ConversationHistoryRepairResult) => {
    if (result.taskStatus === "idle") return;
    setConversationHistoryRepair(result);
  };

  const pollConversationHistoryRepair = async (announceTerminal: boolean) => {
    const version = ++conversationHistoryRepairPollVersion.current;
    let shouldAnnounceTerminal = announceTerminal;
    let firstRequest = true;
    while (conversationHistoryRepairMounted.current && version === conversationHistoryRepairPollVersion.current) {
      if (!firstRequest) await delay(650);
      if (!conversationHistoryRepairMounted.current || version !== conversationHistoryRepairPollVersion.current) return;

      let result: ConversationHistoryRepairResult;
      try {
        result = await call<ConversationHistoryRepairResult>("conversation_history_repair_status");
      } catch (error) {
        if (shouldAnnounceTerminal && version === conversationHistoryRepairPollVersion.current) {
          showNotice("对话历史兼容修复", `读取修复进度失败：${stringifyError(error)}`, "failed");
        }
        return;
      }
      if (!conversationHistoryRepairMounted.current || version !== conversationHistoryRepairPollVersion.current) return;
      rememberConversationHistoryRepair(result);
      if (isConversationHistoryRepairActive(result)) {
        shouldAnnounceTerminal = true;
        firstRequest = false;
        continue;
      }

      if (result.taskStatus !== "idle" && shouldAnnounceTerminal) {
        const noticeKey = `${result.taskId ?? "history-repair"}:${result.taskStatus ?? result.status}`;
        if (conversationHistoryRepairNoticeKey.current !== noticeKey) {
          conversationHistoryRepairNoticeKey.current = noticeKey;
          showConversationHistoryRepairOutcome(result);
        }
      }
      return;
    }
  };

  const repairConversationHistory = async () => {
    if (conversationHistoryRepairInProgress) return;
    if (
      !confirmMessage(
        "即将修复对话历史。必须先完全退出 ChatGPT 和 Codex；后端检测到活动进程时会拒绝执行。工具会先自动备份原始文件，并且只删除历史工具调用 response_item 的 payload 顶层 namespace；不会删除消息正文、工具输出或嵌套参数，也不会修改当前配置。是否继续？",
      )
    ) {
      return;
    }
    const result = await run(() => call<ConversationHistoryRepairResult>("repair_conversation_history"));
    if (!result) return;
    conversationHistoryRepairNoticeKey.current = "";
    rememberConversationHistoryRepair(result);
    if (isConversationHistoryRepairActive(result)) {
      void pollConversationHistoryRepair(true);
    } else {
      showConversationHistoryRepairOutcome(result);
    }
  };

  const cancelConversationHistoryRepair = async () => {
    if (!isConversationHistoryRepairActive(conversationHistoryRepair) || conversationHistoryRepair?.taskStatus === "cancelling") return;
    const result = await run(() =>
      call<ConversationHistoryRepairResult>("cancel_conversation_history_repair", {
        taskId: conversationHistoryRepair?.taskId,
      }),
    );
    if (!result) return;
    rememberConversationHistoryRepair(result);
    if (isConversationHistoryRepairActive(result)) {
      void pollConversationHistoryRepair(true);
    } else {
      showConversationHistoryRepairOutcome(result);
    }
  };

  const repairCodexPlugins = async () => {
    const result = await run(() => call<CodexConfigRepairResult>("repair_codex_plugins"));
    if (result) {
      showNotice("插件配置恢复", result.message, result.status);
      await refreshRelayFiles(true);
    }
  };

  const repairPluginMarketplace = async () => {
    const result = await run(() => call<CommandResult<Record<string, unknown>>>("repair_plugin_marketplace"));
    if (result) {
      showNotice("插件市场修复", result.message, result.status);
      await refreshRelayFiles(true);
    }
  };

  const repairRemotePluginMarketplace = async () => {
    const result = await run(() => call<CommandResult<Record<string, unknown>>>("repair_remote_plugin_marketplace"));
    if (result) {
      showNotice("官方远端插件缓存", result.message, result.status);
      await refreshRelayFiles(true);
    }
  };

  const repairCodexGoals = async () => {
    const result = await run(() => call<CodexConfigRepairResult>("repair_codex_goals"));
    if (result) {
      showNotice("追求目标修复", result.message, result.status);
      await refreshRelayFiles(true);
    }
  };

  const applyRelayInjection = async (silent = false) => {
    const settingsResult = await run(() => call<SettingsResult>("save_settings", { settings: settingsForm }));
    if (settingsResult) {
      setSettings(settingsResult);
      if (!isSuccessStatus(settingsResult.status)) {
        showNotice("设置保存", settingsResult.message, settingsResult.status);
        return false;
      }
      setSettingsForm(normalizeSettings(settingsResult.settings));
    } else {
      return false;
    }
    const result = await run(() => call<RelayResult>("apply_relay_injection"));
    if (result) {
      commitRelayResult(result);
      await refreshRelayFiles(true);
      await refreshInstallGuideStatus(true);
      if (!silent || !isSuccessStatus(result.status)) showNotice("官方混合 API", result.message, result.status);
    }
    return !!result && relayApplySucceeded(result) && result.configured;
  };

  const saveLaunchMode = async (launchMode: LaunchMode, silent = false, baseSettings: BackendSettings = settingsForm) => {
    const next = { ...baseSettings, launchMode };
    setSettingsForm(next);
    const result = await run(() => call<SettingsResult>("save_settings", { settings: next }));
    if (result) {
      setSettings(result);
      if (isSuccessStatus(result.status)) setSettingsForm(normalizeSettings(result.settings));
      if (!silent) showNotice("页面增强模式", result.message, result.status);
    }
    return result;
  };

  const applyPureApiInjection = async (silent = false) => {
    const settingsResult = await run(() => call<SettingsResult>("save_settings", { settings: settingsForm }));
    if (settingsResult) {
      setSettings(settingsResult);
      if (!isSuccessStatus(settingsResult.status)) {
        showNotice("设置保存", settingsResult.message, settingsResult.status);
        return false;
      }
      setSettingsForm(normalizeSettings(settingsResult.settings));
    } else {
      return false;
    }
    const result = await run(() => call<RelayResult>("apply_pure_api_injection"));
    if (result) {
      commitRelayResult(result);
      await refreshRelayFiles(true);
      await refreshInstallGuideStatus(true);
      if (!silent || !isSuccessStatus(result.status)) showNotice("中转 API 模式", result.message, result.status);
    }
    return !!result && relayApplySucceeded(result) && result.configured;
  };

  const clearRelayInjection = async (silent = false) => {
    const result = await run(() => call<RelayResult>("clear_relay_injection"));
    if (result) {
      commitRelayResult(result);
      await refreshRelayFiles(true);
      await refreshInstallGuideStatus(true);
      if (!silent || !isSuccessStatus(result.status)) showNotice("官方登录模式", result.message, result.status);
    }
    return !!result && relayApplySucceeded(result) && !result.configured;
  };

  const saveRelayFile = async (kind: "config" | "auth", contents: string, silent = false) => {
    const result = await run(() => call<RelayFilesResult>("save_relay_file", { request: { kind, contents } }));
    if (result) {
      setRelayFiles(result);
      if (!silent || !isSuccessStatus(result.status)) {
        showNotice(kind === "config" ? "config.toml" : "auth.json", result.message, result.status);
      }
      await refreshRelay(true);
    }
    return result;
  };

  const upsertContextEntry = async (next: BackendSettings, kind: ContextKind, id: string, tomlBody: string) => {
    const result = await run(() =>
      call<ContextEntriesResult>("upsert_context_entry", {
        request: { settings: next, kind, id, tomlBody },
      }),
    );
    if (!result) return null;
    let normalized = normalizeSettings(result.settings);
    const saveResult = await run(() => call<SettingsResult>("save_settings", { settings: normalized }));
    if (saveResult) {
      setSettings(saveResult);
      normalized = normalizeSettings(saveResult.settings);
    }
    setSettingsForm(normalized);
    if (!isSuccessStatus(result.status)) showResultNotice("工具与插件", result);
    return normalized;
  };

  const deleteContextEntry = async (next: BackendSettings, kind: ContextKind, id: string) => {
    const result = await run(() =>
      call<ContextEntriesResult>("delete_context_entry", {
        request: { settings: next, kind, id },
      }),
    );
    if (!result) return null;
    let normalized = normalizeSettings(result.settings);
    const saveResult = await run(() => call<SettingsResult>("save_settings", { settings: normalized }));
    if (saveResult) {
      setSettings(saveResult);
      normalized = normalizeSettings(saveResult.settings);
    }
    setSettingsForm(normalized);
    if (!isSuccessStatus(result.status)) showResultNotice("工具与插件", result);
    return normalized;
  };

  const syncLiveContextEntries = async (next: BackendSettings, silent = false) => {
    const result = await run(() =>
      call<LiveContextEntriesResult>("sync_live_context_entries", {
        request: { settings: next },
      }),
    );
    if (result) {
      setLiveContextEntries(normalizeContextEntries(result.entries));
      if (!silent || !isSuccessStatus(result.status)) showResultNotice("工具与插件同步", result, { silentSuccess: true });
      await refreshRelayFiles(true);
    }
    return result;
  };

  const extractRelayCommonConfig = async (configContents: string) => {
    const result = await run(() =>
      call<ExtractRelayCommonConfigResult>("extract_relay_common_config", {
        request: { configContents },
      }),
    );
    if (result) showResultNotice("通用配置文件", result);
    return result && isSuccessStatus(result.status) ? result : null;
  };

  const importCurrentRelayFiles = async (profileId: string) => {
    const result = await run(() => call<SettingsResult>("import_current_relay_files", { request: { profileId } }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("供应商快照", result.message, result.status);
      await refreshRelay(true);
      await refreshRelayFiles(true);
    }
    return result;
  };

  const bindOfficialAuth = async (profileId: string) => {
    const result = await run(() => call<SettingsResult>("bind_official_auth", { request: { profileId } }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("官方账号绑定", result.message, result.status);
      await refreshRelay(true);
    }
    return result;
  };

  const activateOfficialAuth = async (profileId: string) => {
    const result = await run(() => call<RelayResult>("activate_official_auth", { request: { profileId } }));
    if (result) {
      commitRelayResult(result);
      showNotice("绑定账号", result.message, result.status);
      await refreshRelayFiles(true);
      await refreshSettings(true);
    }
    return result;
  };

  const unbindOfficialAuth = async (profileId: string) => {
    const result = await run(() => call<SettingsResult>("unbind_official_auth", { request: { profileId } }));
    if (result) {
      setSettings(result);
      setSettingsForm(normalizeSettings(result.settings));
      showNotice("官方账号绑定", result.message, result.status);
      await refreshRelay(true);
    }
    return result;
  };

  const clearCurrentOfficialAuth = async () => {
    const result = await run(() => call<RelayResult>("clear_current_official_auth"));
    if (result) {
      commitRelayResult(result);
      showNotice("清除当前官方登录", result.message, result.status);
      await refreshRelayFiles(true);
      await refreshSettings(true);
    }
    return result;
  };

  const testRelayProfile = async (profile: RelayProfile) => {
    const result = await run(() => call<RelayProfileTestResult>("test_relay_profile", { profile }));
    if (result) showNotice("供应商测试", result.message, result.status);
  };

  const testStepwiseSettings = async (settings: BackendSettings) => {
    const result = await run(() => call<CommandResult<{ items?: Array<{ label?: string; prompt: string }> }>>("test_stepwise_settings", { settings }));
    if (result) showNotice("Stepwise 测试", result.message, result.status);
  };

  const testVLMProfile = async (profile: RelayProfile, imageDataUrl: string) =>
    run(() =>
      call<VLMTestResult>("test_vlm_profile", {
        request: {
          apiKey: profile.vlmApiKey,
          model: profile.vlmModel,
          baseUrl: profile.vlmBaseUrl,
          imageDataUrl,
          proxyEnabled: profile.proxyEnabled,
          proxyUrl: profile.proxyUrl,
        },
      }),
    );

  const fetchRelayProfileModels = async (profile: RelayProfile) => {
    const result = await run(() => call<RelayProfileModelsResult>("fetch_relay_profile_models", { profile }));
    if (result) showNotice("模型列表", result.message, result.status);
    return result && isSuccessStatus(result.status) ? result.models : null;
  };

  const fetchSub2APIBilling = async (profile: RelayProfile) => {
    const result = await run(() => call<Sub2APIBillingResult>("fetch_sub2api_billing", { profile }));
    if (result) showNotice("Sub2API", result.message, result.status);
    return result && isSuccessStatus(result.status) ? result : null;
  };

  const diagnoseRelayProfile = async (profile: RelayProfile) => {
    const result = await run(() => call<ProviderDoctorResult>("diagnose_relay_profile", { profile }));
    if (result) showNotice("Provider Doctor", result.message, result.status);
    return result;
  };

  const switchOfficialMode = async () => {
    const switched = await clearRelayInjection(false);
    if (!switched) return;
    const result = await saveLaunchMode("patch", true);
    if (result && !isSuccessStatus(result.status)) showNotice("页面增强模式", result.message, result.status);
  };

  const switchPureApiMode = async () => {
    const switched = await applyPureApiInjection(false);
    if (!switched) return;
    const result = await saveLaunchMode("patch", true);
    if (result && !isSuccessStatus(result.status)) showNotice("页面增强模式", result.message, result.status);
  };

  const switchRelayProfile = async (next: BackendSettings) => {
    if (relaySwitchInProgress) return false;
    const setSwitchProgress = (percent: number, detail: string, status: TaskProgress["status"] = "running") => {
      setRelaySwitchProgress({
        id: "relaySwitch",
        label: "供应商切换",
        detail,
        percent,
        status,
      });
    };
    const failSwitch = (detail: string) => {
      setSwitchProgress(100, detail, "failed");
      return false;
    };
    setSwitchProgress(8, "正在准备当前配置快照。");
    if (!next.relayProfilesEnabled) {
      setSwitchProgress(25, "供应商配置切换已关闭，正在保存设置。");
      const settingsResult = await run(() => call<SettingsResult>("save_settings", { settings: next }));
      if (settingsResult) {
        setSettings(settingsResult);
        if (isSuccessStatus(settingsResult.status)) setSettingsForm(normalizeSettings(settingsResult.settings));
        showNotice("供应商切换", "供应商配置切换已关闭：已保存设置，但没有写入当前 ChatGPT Codex 配置文件。", settingsResult.status);
        setSwitchProgress(100, settingsResult.message, isSuccessStatus(settingsResult.status) ? "ok" : "failed");
        if (isSuccessStatus(settingsResult.status)) {
          window.setTimeout(() => {
            setRelaySwitchProgress((current) => current?.id === "relaySwitch" && current.status === "ok" ? null : current);
          }, 2200);
        }
      } else {
        return failSwitch("保存供应商设置失败。");
      }
      return false;
    }
    const nextWithSnapshot = await snapshotActiveRelayFilesBeforeSwitch(prepareRelaySettingsForSwitch(next));
    if (!nextWithSnapshot) return failSwitch("读取当前配置快照失败，已停止切换。");

    const selectedBeforeSave = activeRelayProfile(nextWithSnapshot);
    const validationError = relayProfileSwitchValidation(selectedBeforeSave);
    if (validationError) {
      showNotice("供应商配置可能不正确", validationError, "failed");
      return failSwitch(validationError);
    }

    setSwitchProgress(35, "正在保存供应商设置。");
    let selectedSettings = nextWithSnapshot;
    const settingsResult = await run(() => call<SettingsResult>("save_settings", { settings: nextWithSnapshot }));
    if (settingsResult) {
      selectedSettings = normalizeSettings(settingsResult.settings);
      setSettings(settingsResult);
      if (!isSuccessStatus(settingsResult.status)) {
        showNotice("供应商切换", settingsResult.message, settingsResult.status);
        return failSwitch(settingsResult.message);
      }
      setSettingsForm(selectedSettings);
    } else {
      return failSwitch("保存供应商设置失败。");
    }

    setSwitchProgress(60, "正在写入供应商配置。");
    const selectedAfterSave = activeRelayProfile(selectedSettings);
    const command = relayProfileSwitchCommand(selectedAfterSave);
    const result = await run(() => call<RelayResult>(command));
    if (!result) return failSwitch("写入供应商配置失败。");

    commitRelayResult(result);
    await refreshRelayFiles(true);
    if (!relayApplySucceeded(result)) {
      showNotice("供应商切换", result.message || relayProfileReadinessText(selectedAfterSave, result), result.status);
      return failSwitch(result.message || relayProfileReadinessText(selectedAfterSave, result));
    }

    setSwitchProgress(82, "正在切换到完整增强。");
    const currentSelected = activeRelayProfile(selectedSettings);
    const modeResult = await saveLaunchMode("patch", true, selectedSettings);
    if (!modeResult) return failSwitch("完整增强设置保存失败。");
    await refreshInstallGuideStatus(true);
    setSwitchProgress(94, "正在刷新连接状态。");
    await refreshRelay(true);
    if (!isSuccessStatus(modeResult.status)) {
      showNotice("供应商切换", modeResult.message, modeResult.status);
      return failSwitch(modeResult.message);
    }
    const historyPending = result.providerSync?.status === "skipped";
    const historyFailed = result.providerSync?.status === "failed" || result.providerSync?.status === "partial";
    const maintenanceIncomplete = !isSuccessStatus(result.status) || historyFailed;
    setSwitchProgress(
      100,
      maintenanceIncomplete
        ? "模式已切换，但后续维护未完成；设置和当前模式已经生效。"
        : historyPending
        ? "模式已切换；聊天记录同步暂未完成，可在历史修复中重试。"
        : "供应商已切换，页面增强已设为完整增强。",
      "ok",
    );
    window.setTimeout(() => {
      setRelaySwitchProgress((current) => current?.id === "relaySwitch" && current.status === "ok" ? null : current);
    }, 2200);
    showNotice(
      "供应商切换",
      relayProfileModeSwitchedText(currentSelected, result),
      maintenanceIncomplete || historyPending ? "not_checked" : modeResult.status,
    );
    return true;
  };

  const snapshotActiveRelayFilesBeforeSwitch = async (next: BackendSettings): Promise<BackendSettings | null> => {
    const current = activeRelayProfile(settingsForm);
    const selected = activeRelayProfile(next);
    if (current.id === selected.id) return next;

    const backfill = await run(() =>
      call<SettingsBackfillResult>("backfill_relay_profile_from_live", {
        request: { settings: next, profileId: current.id },
      }),
    );
    if (backfill) {
      if (!isSuccessStatus(backfill.status)) {
        showNotice("供应商切换", backfill.message, backfill.status);
        return null;
      }
      return normalizeSettings(backfill.settings);
    }

    const files = await refreshRelayFiles(true);
    if (!files || !isSuccessStatus(files.status)) {
      showNotice("供应商切换", files?.message ?? "读取当前配置文件失败，已停止切换以避免覆盖用户改动。", files?.status ?? "failed");
      return null;
    }

    const currentSnapshot = { configContents: files.configContents, authContents: files.authContents };

    return syncLegacyRelayFields({
      ...next,
      relayProfiles: next.relayProfiles.map((profile) =>
        profile.id === current.id
          ? {
              ...profile,
              ...currentSnapshot,
            }
          : profile,
      ),
    });
  };


  const copyText = async (text: string, message: string) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch (error) {
      showNotice("复制失败", stringifyError(error), "failed");
    }
  };

  const openExternalUrl = async (url: string) => {
    const result = await run(() => call<CommandResult<Record<string, unknown>>>("open_external_url", { url }));
    if (result) {
      showResultNotice("打开链接", result, { silentSuccess: true });
    }
  };

  const showNotice = (title: string, message: string, status?: Status) => {
    setNotice({ title, message, status });
  };

  const showResultNotice = (
    title: string,
    result: Pick<CommandResult<unknown>, "message" | "status">,
    options: { silentSuccess?: boolean } = {},
  ) => {
    if (options.silentSuccess && isSuccessStatus(result.status)) return;
    showNotice(title, result.message, result.status);
  };

  useEffect(() => {
    conversationHistoryRepairMounted.current = true;
    void pollConversationHistoryRepair(false);
    return () => {
      conversationHistoryRepairMounted.current = false;
      conversationHistoryRepairPollVersion.current += 1;
    };
  }, []);

  useEffect(() => {
    void (async () => {
      await refreshOverview(true);
      await refreshSettings(true);
      await refreshRelay(true);
      await refreshInstallGuideStatus(true);
      await checkUpdate(true);
    })();
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.classList.toggle("light", theme === "light");
    window.localStorage.setItem("codex-plus-theme", theme);
  }, [theme]);

  useEffect(() => {
    document.documentElement.lang = currentLanguage;
    requestAnimationFrame(() => localizeDocument(document, currentLanguage));
  }, [currentLanguage]);

  useEffect(() => watchDocumentLocalization(document, () => languageRef.current), []);

  const saveCodexAppPath = async (appPath: string) => {
    const next = { ...settingsForm, codexAppPath: appPath };
    const result = await run(() => call<SettingsResult>("save_settings", { settings: next }));
    if (result) {
      setSettings(result);
      const normalized = normalizeSettings(result.settings);
      setSettingsForm(normalized);
      setLaunchForm((current) => ({ ...current, appPath: "" }));
      await refreshOverview(true);
    }
    return result;
  };

  const actions = useMemo(
    () => ({
      refreshCurrent: () => navigate(route),
      launch,
      restart,
      repairBackend,
      repairCodexApp,
      repairPluginMarketplace,
      repairRemotePluginMarketplace,
      installEntrypoints,
      uninstallEntrypoints,
      uninstallCodexTools,
      repairShortcuts,
      saveSettings,
      saveSettingsValue,
      copyText,
      checkEnvConflicts,
      removeEnvConflicts,
      resetSettings,
      changeLanguage,
      chooseCodexAppPath: async (mode: "folder" | "file") => {
        const selected = await openFileDialog(
          mode === "folder"
            ? { directory: true, multiple: false, title: tr("选择 ChatGPT 应用目录") }
            : {
                directory: false,
                multiple: false,
                title: tr("选择 ChatGPT.exe / Codex.exe"),
                filters: [{ name: tr("ChatGPT / Codex 应用"), extensions: ["exe", "app"] }],
              },
        );
        if (typeof selected === "string" && selected.trim()) {
          const result = await saveCodexAppPath(selected.trim());
          if (result) {
            showNotice("ChatGPT 应用路径", "应用路径已保存，之后启动会自动复用。", result.status);
            await refreshInstallGuideStatus(true);
          }
        } else {
          showNotice("ChatGPT 应用路径", "未选择路径。", "not_checked");
        }
      },
      clearCodexAppPath: async () => {
        const next = { ...settingsForm, codexAppPath: "" };
        const result = await run(() => call<SettingsResult>("save_settings", { settings: next }));
        if (result) {
          setSettings(result);
          setSettingsForm(normalizeSettings(result.settings));
          setLaunchForm((current) => ({ ...current, appPath: "" }));
          showNotice("ChatGPT 应用路径", "已清除保存路径，后续启动会回到自动探测。", result.status);
          await refreshOverview(true);
        }
      },
      saveManualCodexAppPath: async () => {
        const appPath = launchForm.appPath.trim();
        if (!appPath) {
          showNotice("ChatGPT 应用路径", "请先填写或选择应用路径。", "failed");
          return;
        }
        const result = await saveCodexAppPath(appPath);
        if (result) {
          showNotice("ChatGPT 应用路径", "应用路径已保存，之后启动会自动复用。", result.status);
        }
      },
      syncProvidersNow,
      refreshProviderSyncTargets,
      refreshLocalSessions,
      deleteLocalSession,
      previewSessionIndexCleanup,
      applySessionIndexCleanup,
      repairConversationHistory,
      cancelConversationHistoryRepair,
      repairCodexPlugins,
      repairCodexGoals,
      refreshComputerUse,
      repairComputerUse,
      refreshZedRemoteProjects,
      openZedRemoteProject,
      forgetZedRemoteProject,
      chooseImageOverlayPath,
      resetImageOverlaySettings,
      chooseDreamSkinImagePath,
      refreshSkillMcpBackups,
      createSkillMcpBackup,
      restoreSkillMcpBackup,
      deleteSkillMcpBackup,
      setLaunchMode: async (launchMode: LaunchMode) => {
        await saveLaunchMode(launchMode);
      },
      refreshRelay,
      refreshSettings,
      refreshInstallGuideStatus,
      refreshRelayFiles,
      refreshLiveContextEntries,
      refreshCcsProviders,
      importCcsProviders,
      confirmPendingProviderImport,
      dismissPendingProviderImport,
      refreshScriptMarket,
      installMarketScript,
      setUserScriptEnabled,
      deleteUserScript,
      openExternalUrl,
      applyRelayInjection,
      applyPureApiInjection,
      clearRelayInjection,
      saveRelayFile,
      upsertContextEntry,
      deleteContextEntry,
      syncLiveContextEntries,
      extractRelayCommonConfig,
      importCurrentRelayFiles,
      bindOfficialAuth,
      activateOfficialAuth,
      unbindOfficialAuth,
      clearCurrentOfficialAuth,
      showNotice,
      testRelayProfile,
      testStepwiseSettings,
      testVLMProfile,
      fetchRelayProfileModels,
      fetchSub2APIBilling,
      diagnoseRelayProfile,
      switchRelayProfile,
      switchOfficialMode,
      switchPureApiMode,
      refreshLogs,
      refreshDiagnostics,
      copyLogs: () => copyText(logs?.text ?? "", "日志已复制。"),
      copyDiagnostics: () => copyText(diagnostics?.report ?? "", "诊断报告已复制。"),
      goInstallGuide: () => navigate("installGuide"),
      goRelay: () => navigate("relay"),
      goMaintenance: () => navigate("maintenance"),
      goEnhance: () => navigate("enhance"),
      goLogs: () => navigate("logs"),
      checkHealth: async () => {
        await refreshToolHealth(false);
      },
      checkUpdate,
      installUpdate,
      installWatcher: () => watcherAction("install_watcher"),
      uninstallWatcher: () => watcherAction("uninstall_watcher"),
      enableWatcher: () => watcherAction("enable_watcher"),
      disableWatcher: () => watcherAction("disable_watcher"),
      toggleTheme: () => setTheme((current) => (current === "dark" ? "light" : "dark")),
      confirm: confirmMessage,
    }),
    [route, launchForm, settingsForm, settings, pendingProviderImport, removeOwnedData, logs, diagnostics, theme, relayFiles, updateInfo, relay, computerUse, skillMcpBackups, liveContextEntries, zedRemoteProjects, localSessions, providerSyncTargets, selectedProviderSyncTarget, sessionIndexCleanup, restartInProgress, relaySwitchInProgress, modeHistorySyncInProgress, conversationHistoryRepair],
  );

  return (
    <div className={`shell ${theme}`}>
      <ProviderImportDialog request={pendingProviderImport} actions={actions} />
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">C</div>
          <div className="brand-copy">
            <div className="brand-title-row">
              <div className="brand-title">ChatGPT Codex</div>
            </div>
            <div className="brand-subtitle">简单管理 Codex</div>
          </div>
        </div>
        <nav className="nav">
          <NavGroup
            activeRoute={route}
            group="main"
            label="常用"
            onNavigate={navigate}
          />
          <NavGroup
            activeRoute={route}
            group="support"
            label="更多"
            onNavigate={navigate}
          />
        </nav>
      </aside>
      <main className="workspace">
        <header className="topbar">
          <div>
            <h1>{routeTitle(route)}</h1>
            <p>{routeSubtitle(route)}</p>
          </div>
          <div className="topbar-actions">
            <label className="topbar-language" title="语言">
              <span>语言</span>
              <select
                aria-label="语言"
                value={currentLanguage}
                onChange={(event) => void actions.changeLanguage(normalizeLanguage(event.currentTarget.value))}
              >
                {languageOptions.map((language) => (
                  <option key={language.code} value={language.code}>
                    {language.nativeName}
                  </option>
                ))}
              </select>
            </label>
            <Button
              onClick={actions.toggleTheme}
              size="icon"
              title={theme === "dark" ? "切换到浅色" : "切换到深色"}
              variant="outline"
            >
              {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
            <div className="topbar-task">
              <Button disabled={restartInProgress} onClick={() => void actions.restart()} title="重启 ChatGPT" variant="outline">
                <Rocket className="h-4 w-4" />
                {restartInProgress ? "重启中" : "重启"}
              </Button>
              {restartProgress ? <InlineTaskProgress compact progress={restartProgress} /> : null}
            </div>
            <Button onClick={() => void actions.refreshCurrent()} size="icon" title="刷新当前页面" variant="outline">
              <RefreshCw className="h-4 w-4" />
            </Button>
          </div>
        </header>
        <section className="screen">
          {route === "overview" ? (
            <OverviewScreen
              overview={overview}
              updateInfo={updateInfo}
              settings={settings}
              installGuideStatus={installGuideStatus}
              relay={relay}
              actions={actions}
            />
          ) : null}
          {route === "installGuide" ? (
            <InstallGuideScreen
              status={installGuideStatus}
              settings={settings}
              relay={relay}
              ccsProviders={ccsProviders}
              form={settingsForm}
              actions={actions}
            />
          ) : null}
          {route === "relay" ? (
            <RelayScreen
              settings={settings}
              relay={relay}
              relayFiles={relayFiles}
              ccsProviders={ccsProviders}
              form={settingsForm}
              onFormChange={setSettingsForm}
              switchProgress={relaySwitchProgress}
              actions={actions}
            />
          ) : null}
          {route === "proxy" ? (
            <ProxyScreen form={settingsForm} onFormChange={setSettingsForm} actions={actions} />
          ) : null}
          {route === "context" ? (
            <ContextScreen
              form={settingsForm}
              liveEntries={liveContextEntries}
              relayFiles={relayFiles}
              onFormChange={setSettingsForm}
              actions={actions}
            />
          ) : null}
          {route === "enhance" ? (
            <EnhanceScreen form={settingsForm} onFormChange={setSettingsForm} zedRemoteProjects={zedRemoteProjects} actions={actions} />
          ) : null}
          {route === "userScripts" ? <UserScriptsScreen settings={settings} market={scriptMarket} actions={actions} /> : null}
          {route === "providerSync" ? (
            <ProviderSyncScreen
              settings={settings}
              relay={relay}
              computerUse={computerUse}
              localSessions={localSessions}
              sessionIndexCleanup={sessionIndexCleanup}
              skillMcpBackups={skillMcpBackups}
              providerSyncTargets={providerSyncTargets}
              selectedProviderSyncTarget={selectedProviderSyncTarget}
              onProviderSyncTargetChange={setSelectedProviderSyncTarget}
              lastModeHistorySync={lastModeHistorySync}
              modeHistorySyncInProgress={modeHistorySyncInProgress}
              form={settingsForm}
              onFormChange={setSettingsForm}
              actions={actions}
            />
          ) : null}
          {route === "maintenance" ? (
            <MaintenanceScreen
              overview={overview}
              watcher={watcher}
              settings={settings}
              conversationHistoryRepair={conversationHistoryRepair}
              conversationHistoryRepairInProgress={conversationHistoryRepairInProgress}
              launchForm={launchForm}
              onLaunchFormChange={setLaunchForm}
              removeOwnedData={removeOwnedData}
              onRemoveOwnedDataChange={setRemoveOwnedData}
              actions={actions}
            />
          ) : null}
          {route === "settings" ? (
            <SettingsScreen settings={settings} theme={theme} form={settingsForm} onFormChange={setSettingsForm} actions={actions} />
          ) : null}
          {route === "logs" ? <LogsScreen logs={logs} actions={actions} /> : null}
          {route === "diagnostics" ? (
            <DiagnosticsScreen diagnostics={diagnostics} actions={actions} />
          ) : null}
          {route === "about" ? <AboutScreen overview={overview} updateInfo={updateInfo} actions={actions} /> : null}
        </section>
      </main>
      {notice ? (
        <NoticeDialog
          key={`${notice.title}-${notice.message}-${notice.status ?? ""}`}
          notice={notice}
          onClose={() => setNotice(null)}
        />
      ) : null}
    </div>
  );
}

type AppErrorBoundaryState = {
  error: Error | null;
};

export class AppErrorBoundary extends Component<{ children: ReactNode }, AppErrorBoundaryState> {
  state: AppErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): AppErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("ChatGPT Codex Tools UI render failed", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return <StartupErrorScreen error={this.state.error} onRetry={() => this.setState({ error: null })} />;
  }
}

function StartupErrorScreen({ error, onRetry }: { error: Error; onRetry: () => void }) {
  return (
    <main className="startup-error">
      <section>
        <div className="startup-error-icon">
          <Bell className="h-6 w-6" />
        </div>
        <div>
          <p className="home-kicker">ChatGPT Codex Tools 启动保护</p>
          <h1>界面初始化遇到异常</h1>
          <p>管理工具没有白屏退出。你可以重试，或把这段错误发给开发者排查。</p>
          <code>{error.message || String(error)}</code>
          <div className="hero-actions">
            <Button onClick={onRetry} variant="secondary">
              <RefreshCw className="h-4 w-4" />
              重试界面
            </Button>
          </div>
        </div>
      </section>
    </main>
  );
}

type Actions = {
  refreshCurrent: () => Promise<void>;
  launch: () => Promise<void>;
  restart: () => Promise<void>;
  repairBackend: () => Promise<void>;
  repairPluginMarketplace: () => Promise<void>;
  repairRemotePluginMarketplace: () => Promise<void>;
  installEntrypoints: () => Promise<void>;
  uninstallEntrypoints: () => Promise<void>;
  uninstallCodexTools: () => Promise<void>;
  repairShortcuts: () => Promise<void>;
  repairCodexApp: () => Promise<void>;
  saveSettings: () => Promise<void>;
  saveSettingsValue: (settings: BackendSettings, silent?: boolean) => Promise<SettingsResult | null>;
  copyText: (text: string, message: string) => Promise<void>;
  checkEnvConflicts: () => Promise<SettingsResult | null>;
  removeEnvConflicts: (names: string[]) => Promise<SettingsResult | null>;
  resetSettings: () => Promise<void>;
  changeLanguage: (language: LanguageCode) => Promise<void>;
  chooseCodexAppPath: (mode: "folder" | "file") => Promise<void>;
  clearCodexAppPath: () => Promise<void>;
  saveManualCodexAppPath: () => Promise<void>;
  syncProvidersNow: () => Promise<void>;
  refreshProviderSyncTargets: (silent?: boolean) => Promise<ProviderSyncTargetsResult | null>;
  refreshLocalSessions: (silent?: boolean, offset?: number) => Promise<LocalSessionsResult | null>;
  deleteLocalSession: (session: LocalSession) => Promise<void>;
  previewSessionIndexCleanup: () => Promise<SessionIndexCleanupPreviewResult | null>;
  applySessionIndexCleanup: (threadIds: string[]) => Promise<void>;
  repairConversationHistory: () => Promise<void>;
  cancelConversationHistoryRepair: () => Promise<void>;
  repairCodexPlugins: () => Promise<void>;
  repairCodexGoals: () => Promise<void>;
  refreshComputerUse: () => Promise<ComputerUseStatusResult | null>;
  repairComputerUse: () => Promise<void>;
  refreshZedRemoteProjects: () => Promise<ZedRemoteProjectsResult | null>;
  openZedRemoteProject: (project: ZedRemoteProject, strategy?: ZedOpenStrategy) => Promise<void>;
  forgetZedRemoteProject: (project: ZedRemoteProject) => Promise<void>;
  chooseImageOverlayPath: () => Promise<void>;
  resetImageOverlaySettings: () => Promise<void>;
  chooseDreamSkinImagePath: () => Promise<void>;
  refreshSkillMcpBackups: () => Promise<SkillMCPBackupsResult | null>;
  createSkillMcpBackup: () => Promise<void>;
  restoreSkillMcpBackup: (id: string) => Promise<void>;
  deleteSkillMcpBackup: (id: string) => Promise<void>;
  setLaunchMode: (launchMode: LaunchMode) => Promise<void>;
  refreshRelay: () => Promise<RelayResult | null>;
  refreshSettings: () => Promise<SettingsResult | null>;
  refreshInstallGuideStatus: () => Promise<InstallGuideStatusResult | null>;
  refreshRelayFiles: () => Promise<RelayFilesResult | null>;
  refreshLiveContextEntries: () => Promise<LiveContextEntriesResult | null>;
  refreshCcsProviders: () => Promise<CcsProvidersResult | null>;
  importCcsProviders: () => Promise<void>;
  confirmPendingProviderImport: () => Promise<void>;
  dismissPendingProviderImport: () => Promise<void>;
  refreshScriptMarket: () => Promise<void>;
  installMarketScript: (id: string) => Promise<void>;
  setUserScriptEnabled: (key: string, enabled: boolean) => Promise<void>;
  deleteUserScript: (key: string) => Promise<void>;
  openExternalUrl: (url: string) => Promise<void>;
  checkUpdate: () => Promise<UpdateResult | null>;
  installUpdate: () => Promise<void>;
  applyRelayInjection: () => Promise<boolean>;
  applyPureApiInjection: () => Promise<boolean>;
  clearRelayInjection: () => Promise<boolean>;
  saveRelayFile: (kind: "config" | "auth", contents: string, silent?: boolean) => Promise<RelayFilesResult | null>;
  upsertContextEntry: (
    settings: BackendSettings,
    kind: ContextKind,
    id: string,
    tomlBody: string,
  ) => Promise<BackendSettings | null>;
  deleteContextEntry: (settings: BackendSettings, kind: ContextKind, id: string) => Promise<BackendSettings | null>;
  syncLiveContextEntries: (settings: BackendSettings, silent?: boolean) => Promise<LiveContextEntriesResult | null>;
  extractRelayCommonConfig: (configContents: string) => Promise<ExtractRelayCommonConfigResult | null>;
  importCurrentRelayFiles: (profileId: string) => Promise<SettingsResult | null>;
  bindOfficialAuth: (profileId: string) => Promise<SettingsResult | null>;
  activateOfficialAuth: (profileId: string) => Promise<RelayResult | null>;
  unbindOfficialAuth: (profileId: string) => Promise<SettingsResult | null>;
  clearCurrentOfficialAuth: () => Promise<RelayResult | null>;
  showNotice: (title: string, message: string, status?: Status) => void;
  testRelayProfile: (profile: RelayProfile) => Promise<void>;
  testStepwiseSettings: (settings: BackendSettings) => Promise<void>;
  testVLMProfile: (profile: RelayProfile, imageDataUrl: string) => Promise<VLMTestResult | null>;
  fetchRelayProfileModels: (profile: RelayProfile) => Promise<string[] | null>;
  fetchSub2APIBilling: (profile: RelayProfile) => Promise<Sub2APIBillingResult | null>;
  diagnoseRelayProfile: (profile: RelayProfile) => Promise<ProviderDoctorResult | null>;
  switchRelayProfile: (settings: BackendSettings) => Promise<boolean>;
  switchOfficialMode: () => Promise<void>;
  switchPureApiMode: () => Promise<void>;
  refreshLogs: () => Promise<void>;
  refreshDiagnostics: () => Promise<void>;
  copyLogs: () => Promise<void>;
  copyDiagnostics: () => Promise<void>;
  goInstallGuide: () => Promise<void>;
  goRelay: () => Promise<void>;
  goMaintenance: () => Promise<void>;
  goEnhance: () => Promise<void>;
  goLogs: () => Promise<void>;
  installWatcher: () => Promise<void>;
  uninstallWatcher: () => Promise<void>;
  enableWatcher: () => Promise<void>;
  disableWatcher: () => Promise<void>;
  toggleTheme: () => void;
  checkHealth: () => Promise<void>;
  confirm: (message: string) => boolean;
};

function ProviderImportDialog({ request, actions }: { request: ProviderImportRequest | null; actions: Actions }) {
  if (!request) return null;
  const protocol = request.wireApi === "chat" || request.wireApi === "chatCompletions" ? "Chat Completions" : "Responses";
  return (
    <div className="modal-backdrop">
      <div className="modal-panel provider-import-dialog">
        <div className="modal-head">
          <div>
            <h2>确认导入供应商</h2>
            <p>外部工具请求新增一个中转供应商，确认后会生成 ChatGPT Codex Tools relay profile。</p>
          </div>
          <UiBadge variant="secondary">Pending</UiBadge>
        </div>
        <div className="metric-grid compact">
          <Metric label="名称" value={request.name || "-"} />
          <Metric label="Base URL" value={request.baseUrl || "-"} />
          <Metric label="协议" value={protocol} />
          <Metric label="模式" value={request.relayMode || "pureApi"} />
        </div>
        <Toolbar>
          <Button onClick={() => void actions.confirmPendingProviderImport()}>
            <CheckCircle2 className="h-4 w-4" />
            确认导入
          </Button>
          <Button onClick={() => void actions.dismissPendingProviderImport()} variant="secondary">
            忽略
          </Button>
        </Toolbar>
      </div>
    </div>
  );
}

function OverviewScreen({
  overview,
  updateInfo,
  settings,
  installGuideStatus,
  relay,
  actions,
}: {
  overview: OverviewResult | null;
  updateInfo: UpdateResult | null;
  settings: SettingsResult | null;
  installGuideStatus: InstallGuideStatusResult | null;
  relay: RelayResult | null;
  actions: Actions;
}) {
  const launchMode = settings?.settings.launchMode ?? "patch";
  const activeProfile = activeRelayProfile(settings?.settings ?? defaultSettings);
  const mixedRelayEnhance = launchMode === "relay" && activeProfile.relayMode === "mixedApi";
  const apiMode = apiModeLabel(relay);
  const update = updateInfo ?? overview?.update ?? null;
  const codexApp = pathStateOrDefault(overview?.codex_app);
  const silentShortcut = pathStateOrDefault(overview?.silent_shortcut);
  const health = healthItems(overview, relay);
  const readyCount = health.filter((item) => item.ok).length;
  const allReady = health.every((item) => item.ok);
  const primaryIssue = health.find((item) => !item.ok);
  const guidePlatform = installGuideStatus?.platformLabel || platformLabel(settings?.settings.onboardingCompletedPlatform || installGuideStatus?.platform || "unknown");
  const guideCompleted = installGuideCompletedForCurrentPlatform(installGuideStatus, settings?.settings ?? defaultSettings);
  const guideMismatch = installGuideStatus?.onboardingPlatformMismatch === true;
  return (
    <>
      <section className="home-hero" aria-label="快速启动">
        <div className="home-hero-main">
          <div className="home-kicker">ChatGPT Codex 管理器</div>
          <h2>{allReady ? "一切就绪，可以开始使用" : "先处理一个小问题，再启动"}</h2>
          <p>
            这里保留最常用的操作。普通使用只需要点击启动；连接服务、修复入口和查看日志都放在看得见的位置。
          </p>
          <div className="home-command">
            <div>
              <span>推荐操作</span>
              <strong>{guideCompleted ? allReady ? "打开 ChatGPT Codex" : primaryIssue?.title ?? "检查状态" : "完成新手引导"}</strong>
              <small>
                {guideCompleted
                  ? allReady
                    ? "使用当前设置启动 ChatGPT Codex。"
                    : primaryIssue?.detail ?? "刷新状态并查看需要处理的项目。"
                  : guideMismatch
                    ? `当前设置曾在 ${platformLabel(settings?.settings.onboardingCompletedPlatform || "unknown")} 完成，请重新检查 ${guidePlatform}。`
                    : `按 ${guidePlatform} 流程完成安装、路径和连接配置。`}
              </small>
            </div>
            <Button
              onClick={() => void (guideCompleted ? actions.launch() : actions.goInstallGuide())}
              size="lg"
              className="home-primary-button"
            >
              {guideCompleted ? <Rocket className="h-4 w-4" /> : <Sparkles className="h-4 w-4" />}
              {guideCompleted ? "立即启动" : "打开引导"}
            </Button>
          </div>
          <div className="hero-actions">
            <Button onClick={() => void actions.checkHealth()} variant="secondary">
              <RefreshCw className="h-4 w-4" />
              检查状态
            </Button>
            <Button onClick={() => void actions.goInstallGuide()} variant="ghost">
              {guideCompleted ? `新手引导 · 已完成 ${guidePlatform}` : "新手引导"}
            </Button>
            <Button onClick={() => void actions.goRelay()} variant="ghost">
              连接服务
            </Button>
          </div>
        </div>
        <div className="home-hero-side">
          <div className="ready-ring">
            <strong>{readyCount}/{health.length}</strong>
            <span>项目正常</span>
          </div>
          <div className="side-status">
            <span>当前连接</span>
            <strong>{apiMode}</strong>
          </div>
          <div className="side-status">
            <span>界面功能</span>
            <strong>{mixedRelayEnhance ? "混合增强" : launchMode === "relay" ? "兼容模式" : "完整模式"}</strong>
          </div>
        </div>
      </section>

      <UpdateBanner update={update} currentVersion={overview?.current_version ?? "-"} actions={actions} />

      <div className="quick-grid">
        <HomeActionCard
          title="新手引导"
          value={guideCompleted ? `已完成 · ${guidePlatform}` : guideMismatch ? "需重新检查" : "待完成"}
          detail={guideCompleted ? "已完成当前系统的安装、路径和连接配置；可随时重新打开。" : `按 ${guidePlatform} 自动分支完成首次配置。`}
          tone={guideCompleted ? "good" : "warn"}
          icon={Sparkles}
          actionLabel={guideCompleted ? "重新打开" : "开始引导"}
          onAction={() => void actions.goInstallGuide()}
        />
        <HomeActionCard
          title="连接方式"
          value={apiMode}
          detail={relayProfileReadinessText(activeProfile, relay)}
          tone={relay?.configured || relayOfficialAuthenticated(relay) ? "good" : "warn"}
          icon={KeyRound}
          actionLabel="管理连接"
          onAction={() => void actions.goRelay()}
        />
        <HomeActionCard
          title="界面功能"
          value={mixedRelayEnhance ? "混合增强" : launchMode === "relay" ? "兼容增强" : "完整增强"}
          detail={settings?.settings.enhancementsEnabled === false ? "增强功能已关闭。" : mixedRelayEnhance ? "站点/插件市场、删除、导出、项目移动和脚本功能可用。" : "删除、导出、项目移动和脚本功能可用。"}
          tone={settings?.settings.enhancementsEnabled === false ? "warn" : "good"}
          icon={Hammer}
          actionLabel="查看功能"
          onAction={() => void actions.goEnhance()}
        />
        <HomeActionCard
          title="入口和路径"
          value={silentShortcut.status === "installed" ? "已安装" : "建议检查"}
          detail={codexApp.path || "如果启动失败，先到修复工具选择 ChatGPT 应用路径。"}
          tone={silentShortcut.status === "installed" && codexApp.status === "found" ? "good" : "warn"}
          icon={Wrench}
          actionLabel="修复工具"
          onAction={() => void actions.goMaintenance()}
        />
      </div>

      <div className="home-columns">
        <Panel className="simple-panel">
          <CardHead title="推荐流程" detail="按顺序完成即可，不需要理解技术细节" />
          <CardContent>
            <GuideList
              items={[
                guideCompleted ? "点击“立即启动”，用当前设置打开 ChatGPT Codex。" : `先进入“新手引导”，按 ${guidePlatform} 流程完成首次配置。`,
                "如果要使用 API，进入“连接服务”选择官方登录、官方混合 API 或中转 API。",
                "如果桌面入口、路径或启动异常，进入“修复工具”检查。",
              ]}
            />
          </CardContent>
        </Panel>
        <Panel className="simple-panel">
          <CardHead title="关键状态" detail="只显示会影响日常使用的项目" />
          <CardContent>
            <div className="health-grid compact-health">
              <div className={`health-item ${overview?.codex_version ? "ok" : "needs-fix"}`}>
                {overview?.codex_version ? <CheckCircle2 className="h-4 w-4" /> : <Bell className="h-4 w-4" />}
                <div>
                  <strong>ChatGPT 版本</strong>
                  <span>{overview?.codex_version ?? "未检测到 ChatGPT 应用版本。"}</span>
                </div>
                <Badge status={overview?.codex_version ? "ok" : "not_checked"} />
              </div>
              {health.map((item) => (
                <div className={`health-item ${item.ok ? "ok" : "needs-fix"}`} key={item.title}>
                  {item.ok ? <CheckCircle2 className="h-4 w-4" /> : <Bell className="h-4 w-4" />}
                  <div>
                    <strong>{item.title}</strong>
                    <span>{item.detail}</span>
                  </div>
                  <Badge status={item.status} />
                </div>
              ))}
            </div>
          </CardContent>
        </Panel>
      </div>

      <Panel>
        <CardHead title="最近一次启动" detail={overview?.latest_launch?.message ?? "还没有启动记录"} />
        <CardContent>
          <LatestLaunch status={overview?.latest_launch ?? null} />
          <Toolbar>
            <Button variant="secondary" onClick={() => void actions.restart()}>
              <Rocket className="h-4 w-4" />
              重启 ChatGPT
            </Button>
            <Button variant="ghost" onClick={() => void actions.goLogs()}>
              打开日志
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
    </>
  );
}

function HomeActionCard({
  title,
  value,
  detail,
  tone,
  icon: Icon,
  actionLabel,
  onAction,
}: {
  title: string;
  value: string;
  detail: string;
  tone: "good" | "warn" | "bad";
  icon: LucideIcon;
  actionLabel: string;
  onAction: () => void;
}) {
  return (
    <button className={`home-card ${tone}`} onClick={onAction} type="button">
      <span className="home-card-icon">
        <Icon className="h-5 w-5" />
      </span>
      <span className="home-card-copy">
        <span>{title}</span>
        <strong>{value}</strong>
        <small>{detail}</small>
      </span>
      <span className="home-card-action">{actionLabel}</span>
    </button>
  );
}

function UpdateBanner({
  update,
  currentVersion,
  actions,
}: {
  update: UpdateResult | null;
  currentVersion: string;
  actions: Actions;
}) {
  const available = update?.updateStatus === "available";
  const downloaded = update?.updateStatus === "downloaded";
  const checked = Boolean(update);
  const latestVersion = update?.latestVersion || update?.tagName || "-";
  return (
    <section className={`update-banner ${available ? "available" : checked ? "checked" : ""}`} aria-label="版本更新">
      <div className="update-banner-icon">
        {available ? <Download className="h-5 w-5" /> : <RefreshCw className="h-5 w-5" />}
      </div>
      <div className="update-banner-copy">
        <span>ChatGPT Codex Tools 更新</span>
        <strong>
          {available
            ? `发现新版本 ${latestVersion}`
            : downloaded
              ? "更新包已下载"
              : checked
                ? "当前版本状态已检查"
                : "可以检查是否有新版本"}
        </strong>
        <small>
          {available
            ? update?.assetName
              ? `${update.assetName}${update.size ? ` · ${formatBytes(update.size)}` : ""}${updateInstallHint(update) ? ` · ${updateInstallHint(update)}` : ""}`
              : update?.message || "发布页有新版本。"
            : update?.message || `当前版本：${currentVersion}`}
        </small>
      </div>
      <div className="update-banner-actions">
        <Button onClick={() => void actions.checkUpdate()} variant={available ? "secondary" : "outline"}>
          <RefreshCw className="h-4 w-4" />
          检查更新
        </Button>
        {available ? (
          <Button onClick={() => void actions.installUpdate()}>
            <Download className="h-4 w-4" />
            下载更新
          </Button>
        ) : null}
      </div>
    </section>
  );
}

type GuideStep = "platform" | "codex" | "ccs" | "mode" | "finish";

function InstallGuideScreen({
  status,
  settings,
  relay,
  ccsProviders,
  form,
  actions,
}: {
  status: InstallGuideStatusResult | null;
  settings: SettingsResult | null;
  relay: RelayResult | null;
  ccsProviders: CcsProvidersResult | null;
  form: BackendSettings;
  actions: Actions;
}) {
  const normalized = normalizeSettings(form);
  const active = activeRelayProfile(normalized);
  const [step, setStep] = useState<GuideStep>("platform");
  const [selectedMode, setSelectedMode] = useState<RelayMode>(active.relayMode);
  const [selectedProfileId, setSelectedProfileId] = useState(normalized.activeRelayId);
  const guideProfile = normalized.relayProfiles.find((profile) => profile.id === selectedProfileId) || active;
  const platformGuide = platformGuideFor(status);
  const codexDetected = status?.codexApp.status === "found";
  const codexLaunchReady = status?.codexLaunch?.ready === true;
  const guideCompleted = installGuideCompletedForCurrentPlatform(status, settings?.settings ?? normalized) && codexLaunchReady;
  const ccsInstalled = status?.ccs.installed === true;
  const importedProviderCount = normalized.relayProfiles.filter((profile) => profile.id.startsWith("ccs-")).length;

  useEffect(() => {
    setSelectedMode(active.relayMode);
    setSelectedProfileId(normalized.activeRelayId);
  }, [active.id, active.relayMode, normalized.activeRelayId]);

  const modeProfile = withGeneratedRelayFiles({
    ...guideProfile,
    relayMode: selectedMode,
    officialMixApiKey: selectedMode === "mixedApi",
  });
  const selectedProfileProblem = relayProfileSwitchValidation(modeProfile);
  const selectedProfileReady = relayProfileGuideReady(modeProfile, status?.connection, relay);

  const openInstall = async () => {
    const url = status?.codexInstallUrl || "https://chatgpt.com/download";
    await actions.openExternalUrl(url);
  };

  const markGuideCompleted = async () => {
    const refreshed = await actions.refreshSettings();
    const base = normalizeSettings(refreshed?.settings ?? form);
    const platform = status?.platform || base.onboardingCompletedPlatform || "";
    const result = await actions.saveSettingsValue(
      {
        ...base,
        onboardingCompleted: true,
        onboardingCompletedAt: new Date().toISOString(),
        onboardingCompletedPlatform: platform,
      },
      true,
    );
    if (!result || !isSuccessStatus(result.status)) return false;
    await actions.refreshInstallGuideStatus();
    return true;
  };

  const applyGuideMode = async () => {
    const next = syncLegacyRelayFields({
      ...normalized,
      activeRelayId: modeProfile.id,
      relayProfiles: normalized.relayProfiles.map((profile) => (profile.id === modeProfile.id ? modeProfile : profile)),
    });
    if (selectedMode === "official") {
      const switched = await actions.switchRelayProfile(next);
      if (switched && await markGuideCompleted()) setStep("finish");
      return;
    }
    if (!selectedProfileReady) {
      await actions.goRelay();
      return;
    }
    const switched = await actions.switchRelayProfile(next);
    if (switched && await markGuideCompleted()) setStep("finish");
  };

  const guideSteps: Array<{ id: GuideStep; title: string; done: boolean }> = [
    { id: "platform", title: "识别系统", done: !!status },
    { id: "codex", title: "安装 ChatGPT", done: codexLaunchReady },
    { id: "ccs", title: "导入 CCSwitch", done: !ccsInstalled || importedProviderCount > 0 },
    { id: "mode", title: "选择模式", done: selectedMode === active.relayMode && active.id === modeProfile.id },
    { id: "finish", title: "启动 ChatGPT Codex", done: guideCompleted },
  ];
  const completedStepCount = guideSteps.filter((item) => item.done).length;
  const guideProgress = Math.round((completedStepCount / guideSteps.length) * 100);

  return (
    <div className="onboarding-shell">
      <section className="onboarding-hero" aria-label="新手安装引导">
        <div className="onboarding-hero-copy">
          <h2>{platformGuide.title}</h2>
          <p>{platformGuide.systemDescription} 从 ChatGPT 安装、CCSwitch 导入到连接模式配置，按顺序完成后直接进入启动界面。</p>
        </div>
        <div className="onboarding-summary">
          <Metric label="系统" value={platformSummary(status)} />
          <Metric label="ChatGPT" value={codexLaunchReady ? "可启动" : codexDetected ? "已安装但需选择可执行路径" : "未检测到"} />
          <Metric label="引导" value={guideCompleted ? `已完成 · ${platformGuide.platformLabel}` : "待完成"} />
          <Metric label="当前模式" value={relayModeLabel(active.relayMode)} />
        </div>
      </section>

      <div className="onboarding-layout">
        <Panel className="simple-panel">
          <CardContent>
            <div className="onboarding-progress" aria-label={`引导进度 ${guideProgress}%`}>
              <div className="onboarding-progress-copy">
                <span>引导进度</span>
                <strong>{guideProgress}%</strong>
              </div>
              <div className="onboarding-progress-track" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={guideProgress}>
                <span style={{ width: `${guideProgress}%` }} />
              </div>
              <small>{completedStepCount} / {guideSteps.length} 步已完成</small>
            </div>
            <div className="onboarding-steps" aria-label="引导步骤">
              {guideSteps.map((item, index) => (
                <button
                  className={`onboarding-step ${step === item.id ? "active" : ""} ${item.done ? "done" : ""}`}
                  key={item.id}
                  onClick={() => setStep(item.id)}
                  type="button"
                >
                  <span>{item.done ? <Check className="h-4 w-4" /> : index + 1}</span>
                  <strong>{item.title}</strong>
                </button>
              ))}
            </div>
          </CardContent>
        </Panel>

        <Panel fill>
          <CardContent>
            {step === "platform" ? (
              <GuidePlatformStep status={status} platformGuide={platformGuide} actions={actions} onNext={() => setStep("codex")} />
            ) : null}
            {step === "codex" ? (
              <GuideCodexStep
                status={status}
                platformGuide={platformGuide}
                installed={codexLaunchReady}
                onInstall={openInstall}
                onChoosePath={(mode) => void actions.chooseCodexAppPath(mode)}
                onRepair={() => void actions.repairCodexApp()}
                onRefresh={() => void actions.refreshInstallGuideStatus()}
                onNext={() => setStep("ccs")}
              />
            ) : null}
            {step === "ccs" ? (
              <GuideCcsStep
                status={status}
                ccsProviders={ccsProviders}
                importedProviderCount={importedProviderCount}
                onImport={() => void actions.importCcsProviders()}
                onRefresh={() => void actions.refreshCcsProviders()}
                onNext={() => setStep("mode")}
              />
            ) : null}
            {step === "mode" ? (
              <GuideModeStep
                form={normalized}
                relay={relay}
                status={status}
                selectedMode={selectedMode}
                selectedProfileId={selectedProfileId}
                selectedProfileProblem={selectedProfileProblem}
                selectedProfileReady={selectedProfileReady}
                onModeChange={setSelectedMode}
                onProfileChange={setSelectedProfileId}
                onTestProfile={() => void actions.testRelayProfile(modeProfile)}
                onApply={applyGuideMode}
              />
            ) : null}
            {step === "finish" ? (
              <GuideFinishStep status={status} platformGuide={platformGuide} settings={settings} relay={relay} form={normalized} actions={actions} />
            ) : null}
          </CardContent>
        </Panel>
      </div>
    </div>
  );
}

function GuidePlatformStep({
  status,
  platformGuide,
  actions,
  onNext,
}: {
  status: InstallGuideStatusResult | null;
  platformGuide: PlatformGuide;
  actions: Actions;
  onNext: () => void;
}) {
  const ready = !!status;
  const desktopReady = status?.desktopRuntimeStatus === "desktop";
  return (
    <div className="guide-pane">
      <div className="guide-pane-head">
        {ready ? <Laptop className="h-5 w-5" /> : <RefreshCw className="h-5 w-5" />}
        <div>
          <h3>{ready ? "系统已识别" : "正在识别系统"}</h3>
          <p>{ready ? platformGuide.systemDescription : "正在读取本机平台、架构和桌面运行方式。"}</p>
        </div>
      </div>
      <div className="guide-facts">
        <Metric label="系统" value={status?.platformLabel || platformLabel(status?.platform ?? "unknown")} />
        <Metric label="架构" value={status?.archLabel || archLabel(status?.arch ?? "")} />
        <Metric label="运行框架" value={status?.desktopRuntime || platformGuide.desktopRuntime || "-"} />
        <Metric label="状态接口" value={status ? statusLabel(status.status) : "加载中"} />
      </div>
      {ready ? (
        <div className={`platform-note ${desktopReady ? "" : "limited"}`}>
          <Info className="h-4 w-4" />
          <span>{desktopReady ? `${platformSummary(status)}：${platformGuide.desktopRuntimeDescription}` : "当前环境未启用桌面窗口运行，会退回浏览器模式。"}</span>
        </div>
      ) : null}
      <Toolbar>
        <Button onClick={() => void actions.refreshInstallGuideStatus()} variant="secondary">
          <RefreshCw className="h-4 w-4" />
          重新检测
        </Button>
        <Button disabled={!ready} onClick={onNext}>下一步</Button>
      </Toolbar>
    </div>
  );
}

function GuideCodexStep({
  status,
  platformGuide,
  installed,
  onInstall,
  onChoosePath,
  onRepair,
  onRefresh,
  onNext,
}: {
  status: InstallGuideStatusResult | null;
  platformGuide: PlatformGuide;
  installed: boolean;
  onInstall: () => void;
  onChoosePath: (mode: "folder" | "file") => void;
  onRepair: () => void;
  onRefresh: () => void;
  onNext: () => void;
}) {
  const download = status?.codexLatestDownload;
  const isWindows = status?.platform === "windows";
  const isSupportedDesktop = status?.platform === "windows" || status?.platform === "darwin";
  const launchStatus = status?.codexLaunch;
  const detected = status?.codexApp.status === "found";
  const needsRepair = detected && !installed;
  const detectionMessage = status?.codexDetection?.message || (detected ? "已检测到 ChatGPT 桌面应用。" : "自动检测暂未找到 ChatGPT。");
  const detectionHints = status?.codexDetection?.candidates ?? [];
  const executableLabel = launchStatus?.method === "packaged_activation"
    ? launchStatus.appUserModelId || status?.codexApp.appUserModelId || status?.codexDetection?.appUserModelId || "MSIX 应用激活"
    : launchStatus?.executable || status?.codexApp.executable || status?.codexDetection?.executable || "未找到";
  const primaryAction = onInstall;
  const primaryLabel = platformGuide.installActionLabel;
  return (
    <div className="guide-pane">
      <div className="guide-pane-head">
        {installed ? <CheckCircle2 className="h-5 w-5" /> : <Download className="h-5 w-5" />}
        <div>
          <h3>{installed ? "ChatGPT 可启动" : needsRepair ? "ChatGPT 已安装但需要选择可执行路径" : platformGuide.installTitle}</h3>
          <p>{installed || needsRepair ? launchStatus?.message || status?.codexApp.path || detectionMessage : detectionMessage || platformGuide.installDescription}</p>
        </div>
      </div>
      {!installed && isSupportedDesktop ? (
        <div className="platform-note limited">
          <Info className="h-4 w-4" />
          <span>{platformGuide.detectionNote}</span>
        </div>
      ) : null}
      {platformGuide.unsupported ? (
        <div className="platform-note limited">
          <Info className="h-4 w-4" />
          <span>{platformGuide.installDescription}</span>
        </div>
      ) : null}
      <div className="install-card">
        <div>
          <strong>{detected ? "当前 ChatGPT" : platformGuide.installTitle}</strong>
          <span>{installed ? status?.codexVersion ?? "版本未读取" : needsRepair ? "已检测到受限安装形态，请选择可直接执行的 ChatGPT.exe" : installDownloadText(download)}</span>
        </div>
        <Button disabled={installed} onClick={primaryAction}>
          <ExternalLink className="h-4 w-4" />
          {primaryLabel}
        </Button>
      </div>
      <div className="guide-facts">
        <Metric label="检测路径" value={status?.codexApp.path ?? "未找到"} />
        <Metric label={platformGuide.launchMethodLabel} value={launchStatus?.methodLabel || (isWindows ? "未检测" : "可执行文件启动")} />
        {isWindows ? <Metric label={launchStatus?.method === "packaged_activation" ? "AppUserModelID" : "启动文件"} value={executableLabel} /> : null}
        {!isWindows ? <Metric label={platformGuide.launchTargetLabel} value={status?.codexApp.path || platformGuide.pathHint || "未找到"} /> : null}
        <Metric label="安装来源" value={platformGuide.installSourceLabel || "官方页面"} />
        <Metric label="最新版本" value={download?.releaseName || download?.tagName || "未获取"} />
      </div>
      {!installed && isWindows && detectionHints.length > 0 ? (
        <div className="detection-hints">
          <span>已尝试位置</span>
          {detectionHints.slice(0, 4).map((hint) => (
            <code key={hint}>{hint}</code>
          ))}
        </div>
      ) : null}
      <Toolbar>
        {isSupportedDesktop ? (
          <Button onClick={() => onChoosePath(platformGuide.manualPrimaryMode)} variant="secondary">
            <ExternalLink className="h-4 w-4" />
            {platformGuide.manualPrimaryLabel}
          </Button>
        ) : null}
        {isWindows && platformGuide.manualSecondaryMode ? (
          <Button onClick={() => onChoosePath(platformGuide.manualSecondaryMode as "folder" | "file")} variant="secondary">
            <ExternalLink className="h-4 w-4" />
            {platformGuide.manualSecondaryLabel || "选择应用目录"}
          </Button>
        ) : null}
        {isSupportedDesktop ? (
          <Button onClick={onRepair} variant="secondary">
            <Wrench className="h-4 w-4" />
            修复 ChatGPT 应用
          </Button>
        ) : null}
        <Button onClick={onRefresh} variant="secondary">
          <RefreshCw className="h-4 w-4" />
          重新检测
        </Button>
        <Button disabled={!installed} onClick={onNext}>下一步</Button>
      </Toolbar>
    </div>
  );
}

function GuideCcsStep({
  status,
  ccsProviders,
  importedProviderCount,
  onImport,
  onRefresh,
  onNext,
}: {
  status: InstallGuideStatusResult | null;
  ccsProviders: CcsProvidersResult | null;
  importedProviderCount: number;
  onImport: () => void;
  onRefresh: () => void;
  onNext: () => void;
}) {
  const installed = status?.ccs.installed === true;
  const providerCount = ccsProviders?.providers.length ?? status?.ccs.providerCount ?? 0;
  return (
    <div className="guide-pane">
      <div className="guide-pane-head">
        {installed ? <CheckCircle2 className="h-5 w-5" /> : <ArrowLeft className="h-5 w-5" />}
        <div>
          <h3>{installed ? "发现 CCSwitch" : "未发现 CCSwitch，跳过导入"}</h3>
          <p>{installed ? "可以把 CCSwitch 里的 Codex 供应商导入到 ChatGPT Codex Tools，后续在中转通道里选择。" : "没有检测到 CCSwitch 数据库，不需要安装，直接进入下一步。"}</p>
        </div>
      </div>
      <div className="guide-facts">
        <Metric label="数据库" value={status?.ccs.dbPath ?? "-"} />
        <Metric label="可导入供应商" value={`${providerCount} 个`} />
        <Metric label="已导入" value={`${importedProviderCount} 个`} />
      </div>
      {!installed || status?.ccs.readError ? (
        <div className={`platform-note ${status?.ccs.readError ? "limited" : ""}`}>
          <Info className="h-4 w-4" />
          <span>{status?.ccs.readError || `已检查路径：${ccsCandidateSummary(status, ccsProviders)}`}</span>
        </div>
      ) : null}
      {installed && providerCount > 0 ? (
        <div className="relay-import-row guide-import-row">
          <div>
            <strong>导入配置文件</strong>
            <span>会新增供应商，不会删除现有配置。</span>
          </div>
          <Button onClick={onImport}>
            <Download className="h-4 w-4" />
            导入配置文件
          </Button>
        </div>
      ) : null}
      <Toolbar>
        <Button onClick={onRefresh} variant="secondary">
          <RefreshCw className="h-4 w-4" />
          刷新 CCSwitch
        </Button>
        <Button onClick={onNext}>下一步</Button>
      </Toolbar>
    </div>
  );
}

function GuideModeStep({
  form,
  relay,
  status,
  selectedMode,
  selectedProfileId,
  selectedProfileProblem,
  selectedProfileReady,
  onModeChange,
  onProfileChange,
  onTestProfile,
  onApply,
}: {
  form: BackendSettings;
  relay: RelayResult | null;
  status: InstallGuideStatusResult | null;
  selectedMode: RelayMode;
  selectedProfileId: string;
  selectedProfileProblem: string | null;
  selectedProfileReady: boolean;
  onModeChange: (mode: RelayMode) => void;
  onProfileChange: (id: string) => void;
  onTestProfile: () => void;
  onApply: () => void;
}) {
  const selectedProfile = form.relayProfiles.find((profile) => profile.id === selectedProfileId) || activeRelayProfile(form);
  const selectedModeProfile = withGeneratedRelayFiles({
    ...selectedProfile,
    relayMode: selectedMode,
    officialMixApiKey: selectedMode === "mixedApi",
  });
  const connection = status?.connection;
  const readinessText = guideConnectionReadinessText(selectedModeProfile, connection, relay, selectedProfileProblem);
  const connectionFacts = guideConnectionFacts(selectedModeProfile, connection, relay);
  return (
    <div className="guide-pane">
      <div className="guide-pane-head">
        <KeyRound className="h-5 w-5" />
        <div>
          <h3>选择连接模式</h3>
          <p>官方/混合模式使用供应商绑定的官方账号；中转模式需要选择可用中转通道。</p>
        </div>
      </div>
      <div className="mode-switch-panel onboarding-mode-panel">
        {(["official", "mixedApi", "pureApi"] as RelayMode[]).map((mode) => (
          <button
            className={`mode-switch-button ${selectedMode === mode ? "active" : ""}`}
            key={mode}
            onClick={() => onModeChange(mode)}
            type="button"
          >
            <strong>{guideModeTitle(mode)}</strong>
            <span>{guideModeDescription(mode)}</span>
          </button>
        ))}
      </div>
      <div className="guide-facts">
        {connectionFacts.map((item) => (
          <Metric key={item.label} label={item.label} value={item.value} />
        ))}
      </div>
      {selectedMode === "mixedApi" ? (
        <div className="platform-note">
          <ShieldCheck className="h-4 w-4" />
          <span>混合 API 模式需要此供应商已绑定官方账号，并填写 Base URL / Key。</span>
        </div>
      ) : null}
      {selectedMode !== "official" ? (
        <>
          <Field label={selectedMode === "mixedApi" ? "混合 API 通道" : "中转通道"}>
            <select
              className="field-select"
              value={selectedProfile.id}
              onChange={(event) => onProfileChange(event.currentTarget.value)}
            >
              {form.relayProfiles.map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name || profile.id} · {relayProfileConfigBrief(profile)}
                </option>
              ))}
            </select>
          </Field>
          <div className={`platform-note ${selectedProfileReady ? "" : "limited"}`}>
            <Info className="h-4 w-4" />
            <span>{readinessText}</span>
          </div>
        </>
      ) : (
        <div className={`platform-note ${selectedProfileReady ? "" : "limited"}`}>
          <ShieldCheck className="h-4 w-4" />
          <span>{readinessText}</span>
        </div>
      )}
      <Toolbar>
        {selectedMode !== "official" ? (
          <Button disabled={!selectedProfileReady} onClick={() => void onTestProfile()} variant="secondary">
            <TestTube className="h-4 w-4" />
            测试服务器
          </Button>
        ) : null}
        <Button disabled={!selectedProfileReady} onClick={() => void onApply()}>
          <CheckCircle2 className="h-4 w-4" />
          完成配置
        </Button>
      </Toolbar>
    </div>
  );
}

function GuideFinishStep({
  status,
  platformGuide,
  settings,
  relay,
  form,
  actions,
}: {
  status: InstallGuideStatusResult | null;
  platformGuide: PlatformGuide;
  settings: SettingsResult | null;
  relay: RelayResult | null;
  form: BackendSettings;
  actions: Actions;
}) {
  const active = activeRelayProfile(form);
  const launchStatus = status?.codexLaunch;
  const launchTarget = launchStatus?.method === "packaged_activation"
    ? launchStatus.appUserModelId || status?.codexApp.appUserModelId || "AppUserModelID"
    : launchStatus?.executable || status?.codexApp.executable || status?.codexApp.path || platformGuide.pathHint || "-";
  return (
    <div className="guide-pane finish-pane">
      <div className="guide-pane-head">
        <Rocket className="h-5 w-5" />
        <div>
          <h3>{platformGuide.completionLabel}</h3>
          <p>配置已经写入并记录完成状态，可以用当前模式启动 ChatGPT Codex。</p>
        </div>
      </div>
      <div className="guide-facts">
        <Metric label="系统" value={platformSummary(status)} />
        <Metric label={platformGuide.launchMethodLabel} value={launchStatus?.methodLabel || "可执行文件启动"} />
        <Metric label={launchStatus?.method === "packaged_activation" ? "AppUserModelID" : platformGuide.launchTargetLabel} value={launchTarget} />
        <Metric label="当前模式" value={relayModeLabel(active.relayMode)} />
        <Metric label="当前供应商" value={active.name || "-"} />
        <Metric label="配置文件" value={settings?.settings_path ?? "-"} />
        <Metric label="登录状态" value={relayOfficialLoginLabel(relay)} />
      </div>
      <Toolbar>
        <Button onClick={() => void actions.launch()} size="lg">
          <Rocket className="h-4 w-4" />
          启动 ChatGPT Codex
        </Button>
        <Button onClick={() => void actions.goRelay()} variant="secondary">
          查看连接服务
        </Button>
      </Toolbar>
    </div>
  );
}

function guideModeTitle(mode: RelayMode) {
  if (mode === "mixedApi") return "混合 API 模式";
  if (mode === "pureApi") return "中转模式";
  return "官方模式";
}

function guideModeDescription(mode: RelayMode) {
  if (mode === "mixedApi") return "使用供应商绑定的官方号，再混入所选 API 通道；站点功能继续走官方登录能力。";
  if (mode === "pureApi") return "直接写入中转通道配置。";
  return "只使用供应商绑定的官方号。";
}

function installDownloadText(download?: CodexLatestDownload) {
  if (!download) return "正在读取最新安装入口。";
  if (download.assetName) return `${download.assetName}${download.size ? ` · ${formatBytes(download.size)}` : ""}`;
  return download.message || download.releaseName || download.tagName || "打开安装页面。";
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function isConversationHistoryRepairActive(result: ConversationHistoryRepairResult | null) {
  return result?.taskStatus === "running" || result?.taskStatus === "cancelling";
}

function conversationHistoryRepairPhaseLabel(phase?: string) {
  switch (phase) {
    case "starting":
    case "checking":
    case "checking_processes":
      return "检查运行状态";
    case "discovering":
      return "查找会话文件";
    case "scanning":
      return "扫描对话历史";
    case "preflight":
    case "preparing":
      return "检查备份空间";
    case "repairing":
    case "backing_up":
      return "备份并修复";
    case "finalizing":
    case "finishing":
      return "完成修复";
    case "completed":
      return "修复已完成";
    case "cancelled":
      return "修复已取消";
    case "failed":
      return "修复失败";
    default:
      return phase || "准备修复";
  }
}

function conversationHistoryTaskProgress(result: ConversationHistoryRepairResult | null): TaskProgress | null {
  if (!result?.taskStatus || result.taskStatus === "idle") return null;
  const status: TaskProgress["status"] =
    result.taskStatus === "ok"
      ? "ok"
      : result.taskStatus === "failed"
        ? "failed"
        : result.taskStatus === "cancelled"
          ? "cancelled"
          : "running";
  const label =
    result.taskStatus === "cancelling"
      ? "正在取消"
      : result.taskStatus === "cancelled"
        ? "修复已取消"
        : result.taskStatus === "ok"
          ? "修复已完成"
          : result.taskStatus === "failed"
            ? "修复失败"
            : conversationHistoryRepairPhaseLabel(result.phase);
  const terminalPercent = status === "running" ? 0 : 100;
  return {
    id: "conversationHistoryRepair",
    label,
    detail: result.detail || result.message || "正在处理对话历史。",
    percent: typeof result.percent === "number" && Number.isFinite(result.percent) ? result.percent : terminalPercent,
    status,
  };
}

function formatBackupTime(value: string) {
  if (!value) return "未知时间";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function NavGroup({
  activeRoute,
  group,
  label,
  onNavigate,
}: {
  activeRoute: Route;
  group: "main" | "support";
  label: string;
  onNavigate: (route: Route) => Promise<void>;
}) {
  return (
    <div className="nav-group">
      <div className="nav-group-label">{label}</div>
      {routes
        .filter((item) => item.group === group)
        .map((item) => {
          const Icon = item.icon;
          return (
            <button
              className={`nav-item ${activeRoute === item.id ? "active" : ""}`}
              key={item.id}
              onClick={() => void onNavigate(item.id)}
              title={`${item.label} · ${item.helper}`}
              type="button"
            >
              <span className="nav-icon">
                <Icon className="h-4 w-4" aria-hidden="true" />
              </span>
              <div>
                <span className="nav-label">{item.label}</span>
                <span className="nav-helper">{item.helper}</span>
              </div>
            </button>
          );
        })}
    </div>
  );
}

function RelayScreen({
  settings,
  relay,
  relayFiles,
  ccsProviders,
  form,
  onFormChange,
  switchProgress,
  actions,
}: {
  settings: SettingsResult | null;
  relay: RelayResult | null;
  relayFiles: RelayFilesResult | null;
  ccsProviders: CcsProvidersResult | null;
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  switchProgress: TaskProgress | null;
  actions: Actions;
}) {
  const normalized = normalizeSettings(form);
  const active = activeRelayProfile(normalized);
  const switching = switchProgress?.status === "running";
  const switchActiveMode = (relayMode: RelayMode) => {
    if (switching) return;
    const nextProfile = withGeneratedRelayFiles({
      ...active,
      relayMode,
      officialMixApiKey: relayMode === "mixedApi",
    });
    const aggregateRelayProfiles =
      relayMode === "aggregate" && !normalized.aggregateRelayProfiles.some((item) => item.id === active.id)
        ? [...normalized.aggregateRelayProfiles, defaultAggregateForProfile(normalized, active)]
        : normalized.aggregateRelayProfiles;
    const next = syncLegacyRelayFields({
      ...normalized,
      relayProfiles: normalized.relayProfiles.map((profile) => (profile.id === active.id ? nextProfile : profile)),
      activeRelayId: active.id,
      aggregateRelayProfiles,
      activeAggregateRelayId: relayMode === "aggregate" ? active.id : normalized.activeAggregateRelayId,
    });
    void actions.switchRelayProfile(next);
  };
  const [detailProfileId, setDetailProfileId] = useState<string | null>(null);
  const [newProfileDraft, setNewProfileDraft] = useState<RelayProfile | null>(null);
  const detailProfile = newProfileDraft || (detailProfileId
    ? normalized.relayProfiles.find((profile) => profile.id === detailProfileId) || null
    : null);
  const isNewProfile = !!newProfileDraft;
  const saveRelaySettings = (next: BackendSettings) => {
    onFormChange(next);
    void actions.saveSettingsValue(next, true);
  };
  useEffect(() => {
    if (!newProfileDraft && detailProfileId && !normalized.relayProfiles.some((profile) => profile.id === detailProfileId)) {
      setDetailProfileId(null);
    }
  }, [detailProfileId, newProfileDraft, normalized.relayProfiles]);
  useEffect(() => {
    if (!newProfileDraft && detailProfileId === normalized.activeRelayId) {
      void actions.refreshRelayFiles();
    }
  }, [detailProfileId, newProfileDraft, normalized.activeRelayId]);

  if (detailProfile) {
    return (
      <Panel fill>
        <CardHead title="供应商详情" detail="编辑这个供应商自己的 config.toml 和 auth.json 快照；切换或保存当前项时再写回当前环境" />
        <CardContent>
          <RelayProfileDetail
            profile={detailProfile}
            form={normalized}
            relay={relay}
            isNew={isNewProfile}
            onBack={() => {
              setNewProfileDraft(null);
              setDetailProfileId(null);
            }}
            onFormChange={saveRelaySettings}
            onSaved={() => {
              setNewProfileDraft(null);
              setDetailProfileId(null);
            }}
            actions={actions}
          />
        </CardContent>
      </Panel>
    );
  }

  return (
    <>
      <Panel>
        <CardHead title="当前供应商状态" detail={relay?.configPath ?? "运行状态跟随供应商列表里的当前配置"} />
        <CardContent>
          <div className="relay-grid">
            <Metric label="当前模式" value={apiModeLabel(relay)} />
            <Metric label="官方账号绑定" value={officialBindingStatusLabel(active)} />
            <Metric label="绑定账号" value={officialBindingLabel(active)} />
            <Metric label="当前官方登录" value={relayCurrentOfficialLabel(relay)} />
            <Metric label="当前供应商已保存账号" value={relayBoundOfficialLabel(relay)} />
            <Metric label="当前供应商 auth 快照" value={active.authContents.trim() ? "已保存" : "未保存"} />
            <Metric label="当前供应商" value={active.name || "-"} />
            <Metric label="接入模式" value={relayModeLabel(active.relayMode)} />
            <Metric label="上游协议" value={relayProtocolLabel(active.protocol)} />
            <Metric label="历史会话" value={relay?.configured ? "ChatGPT Codex Tools" : "openai"} />
            <Metric label="页面增强" value={normalized.launchMode === "relay" ? "兼容模式" : "完整模式"} />
            <Metric label="配置状态" value={relay?.configured ? "已写入" : "官方默认"} />
          </div>
          <div className="hint-line">
            <ShieldCheck className="h-4 w-4" />
            <span>{relayProfileReadinessText(active, relay)}</span>
          </div>
          {switchProgress ? <InlineTaskProgress progress={switchProgress} /> : null}
          <div className="mode-switch-panel" aria-label="切换当前模式">
            <button
              aria-pressed={active.relayMode === "official"}
              className={`mode-switch-button ${active.relayMode === "official" ? "active" : ""}`}
              disabled={switching}
              onClick={() => switchActiveMode("official")}
              type="button"
            >
              <strong>官方模式</strong>
              <span>只使用 ChatGPT 官方登录，不写入中转 API。</span>
            </button>
            <button
              aria-pressed={active.relayMode === "mixedApi"}
              className={`mode-switch-button ${active.relayMode === "mixedApi" ? "active" : ""}`}
              disabled={switching}
              onClick={() => switchActiveMode("mixedApi")}
              type="button"
            >
              <strong>混合 API 模式</strong>
              <span>保留官方登录，同时混入当前供应商 API Key。</span>
            </button>
            <button
              aria-pressed={active.relayMode === "pureApi"}
              className={`mode-switch-button ${active.relayMode === "pureApi" ? "active" : ""}`}
              disabled={switching}
              onClick={() => switchActiveMode("pureApi")}
              type="button"
            >
              <strong>中转模式</strong>
              <span>写入当前供应商的中转配置，保留现有 ChatGPT 登录，不删除聊天记录。</span>
            </button>
            <button
              aria-pressed={active.relayMode === "aggregate"}
              className={`mode-switch-button ${active.relayMode === "aggregate" ? "active" : ""}`}
              disabled={switching}
              onClick={() => switchActiveMode("aggregate")}
              type="button"
            >
              <strong>聚合轮转</strong>
              <span>写入本地代理地址，请求时按策略轮转多个 API 供应商。</span>
            </button>
          </div>
          {relay?.backupPath ? <div className="path-line compact-path">备份：{relay.backupPath}</div> : null}
        </CardContent>
      </Panel>
      <EnvConflictsPanel conflicts={settings?.envConflicts ?? []} actions={actions} />
      <ProviderPresetPanel
        onSelect={(preset) => {
          setNewProfileDraft(createRelayProfileFromPreset(normalized, preset));
          setDetailProfileId(null);
        }}
      />
      <Panel>
        <CardHead title="供应商列表" detail={`${normalized.relayProfiles.length} 个供应商配置；可拖动排序，点击供应商进入详情`} />
        <CardContent>
          <label className="switch-row relay-master-switch">
            <input
              checked={normalized.relayProfilesEnabled}
              onChange={(event) => saveRelaySettings({ ...normalized, relayProfilesEnabled: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>启用供应商配置切换</strong>
              <small>关闭后仍可保存配置，但不会从列表里切换写入当前 ChatGPT Codex 配置。</small>
            </span>
          </label>
          <label className="switch-row relay-link-switch">
            <input
              checked={normalized.ccsLinkEnabled}
              onChange={(event) => saveRelaySettings({ ...normalized, ccsLinkEnabled: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>联动 CCSwitch</strong>
              <small>开启后保留导入供应商的 CCSwitch 来源标记；当前 Go 版仍以本地配置保存为主。</small>
            </span>
          </label>
          <div className="relay-import-row">
            <div>
              <strong>CCSwitch 配置</strong>
              <span>{ccsProviderSummary(ccsProviders)}</span>
            </div>
            <Toolbar>
              <Button onClick={() => void actions.refreshCcsProviders()} size="sm" variant="ghost">
                <RefreshCw className="h-4 w-4" />
                刷新
              </Button>
              <Button
                disabled={!ccsProviders?.providers.length}
                onClick={() => void actions.importCcsProviders()}
                size="sm"
                variant="secondary"
              >
                <Download className="h-4 w-4" />
                导入 CCSwitch 配置
              </Button>
            </Toolbar>
          </div>
          <div className="relay-add-row">
            <Button
              variant="secondary"
              onClick={() => {
                setNewProfileDraft(createRelayProfile(normalized));
                setDetailProfileId(null);
              }}
            >
              <Plus className="h-4 w-4" />
              添加供应商
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setNewProfileDraft(createAggregateRelayProfile(normalized));
                setDetailProfileId(null);
              }}
            >
              <Waypoints className="h-4 w-4" />
              添加聚合供应商
            </Button>
          </div>
          <RelayProfileList
            form={normalized}
            onEdit={(profileId) => {
              setNewProfileDraft(null);
              setDetailProfileId(profileId);
            }}
            onFormChange={saveRelaySettings}
            switching={switching}
            actions={actions}
          />
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="配置文件" detail="当前环境仍只有一套 ~/.codex 文件；供应商详情里编辑的是各自独立快照" />
        <CardContent>
          <div className="path-line loose">ChatGPT Codex 设置：{settings?.settings_path ?? "未加载设置文件。"}</div>
          <div className="path-line loose">Codex config.toml：{relayFiles?.configPath ?? "-"}</div>
          <div className="path-line loose">Codex auth.json：{relayFiles?.authPath ?? "-"}</div>
        </CardContent>
      </Panel>
    </>
  );
}

function EnvConflictsPanel({ conflicts, actions }: { conflicts: EnvConflict[]; actions: Actions }) {
  const names = Array.from(new Set(conflicts.map((item) => item.name))).sort();
  const hasConflicts = names.length > 0;
  return (
    <Panel>
      <CardHead title="环境变量冲突" detail="检查 OPENAI_* 环境变量，避免它们覆盖当前供应商或官方登录配置" />
      <CardContent>
        <div className="hint-line">
          {hasConflicts ? <Bell className="h-4 w-4" /> : <CheckCircle2 className="h-4 w-4" />}
          <span>{hasConflicts ? `发现 ${names.length} 个 OPENAI_* 冲突变量。` : "未发现会影响 Codex 的 OPENAI_* 环境变量。"}</span>
        </div>
        {hasConflicts ? (
          <div className="env-conflict-list">
            {conflicts.map((conflict) => (
              <div className="env-conflict-row" key={`${conflict.source}:${conflict.name}`}>
                <div>
                  <strong>{conflict.name}</strong>
                  <span>{conflict.source === "user" ? "Windows 用户环境" : "当前进程环境"} · {conflict.valuePresent ? "有值" : "空值"}</span>
                </div>
                <Badge status="warn" />
              </div>
            ))}
          </div>
        ) : null}
        <Toolbar>
          <Button onClick={() => void actions.checkEnvConflicts()} variant="secondary">
            <RefreshCw className="h-4 w-4" />
            重新检测
          </Button>
          {hasConflicts ? (
            <Button onClick={() => void actions.removeEnvConflicts(names)} variant="outline">
              清理冲突变量
            </Button>
          ) : null}
        </Toolbar>
      </CardContent>
    </Panel>
  );
}

function EnhanceScreen({
  form,
  onFormChange,
  zedRemoteProjects,
  actions,
}: {
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  zedRemoteProjects: ZedRemoteProjectsResult | null;
  actions: Actions;
}) {
  const setEnhanceFlag = (key: keyof BackendSettings, value: boolean) => onFormChange({ ...form, [key]: value });
  const masterEnabled = form.enhancementsEnabled;
  const patchMode = form.launchMode === "patch";
  const activeMode = activeRelayProfile(form).relayMode;
  const mixedRelayMode = form.launchMode === "relay" && activeMode === "mixedApi";
  const pluginPatchAllowed = patchMode || mixedRelayMode;
  return (
    <>
      <Panel>
        <CardHead title="页面功能增强" detail="会话删除、导出、项目移动和用户脚本等界面能力" />
        <CardContent>
          <label className="switch-row">
            <input
              checked={form.enhancementsEnabled}
              onChange={(event) => onFormChange({ ...form, enhancementsEnabled: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>启用 ChatGPT Codex 页面增强</strong>
              <small>关闭后会停用删除、导出、项目移动和菜单位置增强。</small>
            </span>
          </label>
          <ModeSelector launchMode={form.launchMode} actions={actions} />
          {form.launchMode === "relay" ? (
            <div className="hint-line">
              <ShieldCheck className="h-4 w-4" />
              <span>{mixedRelayMode ? "当前为混合 API 增强：保留官方登录能力，插件市场解锁可继续使用。" : "当前为兼容增强模式：纯中转/聚合会关闭插件市场 patch，其它页面功能仍可用。"}</span>
            </div>
          ) : null}
          <div className="feature-switch-grid">
            <FeatureToggle title="插件市场解锁" detail="扩展 marketplace 请求并显示被客户端版本过滤的官方插件。" checked={form.codexAppPluginMarketplaceUnlock} disabled={!masterEnabled || !pluginPatchAllowed} onChange={(value) => setEnhanceFlag("codexAppPluginMarketplaceUnlock", value)} />
            <FeatureToggle title="模型白名单解锁" detail="读取本地和上游模型目录，补进 Codex 模型选择列表。" checked={form.codexAppModelWhitelistUnlock} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppModelWhitelistUnlock", value)} />
            <FeatureToggle title="Fast 按钮" detail="显示服务模式切换入口，控制 Standard / Fast / priority。" checked={form.codexAppServiceTierControls} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppServiceTierControls", value)} />
            <FeatureToggle title="粘贴修复" detail="修复部分输入框粘贴事件被 Codex 前端吞掉的问题。" checked={form.codexAppPasteFix} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppPasteFix", value)} />
            <FeatureToggle title="强制中文界面" detail="启动时传入 zh-CN locale，并在注入层补齐部分菜单本地化。" checked={form.codexAppForceChineseLocale} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppForceChineseLocale", value)} />
            <FeatureToggle title="快速启动参数" detail="限制 Statsig 初始化等待时间；默认关闭，遇到启动卡顿时再开启。" checked={form.codexAppFastStartup} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppFastStartup", value)} />
            <FeatureToggle title="会话删除" detail="在会话列表悬停显示删除按钮，并支持撤销。" checked={form.codexAppSessionDelete} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppSessionDelete", value)} />
            <FeatureToggle title="Markdown 导出" detail="导出带时间戳的 Markdown。" checked={form.codexAppMarkdownExport} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppMarkdownExport", value)} />
            <FeatureToggle title="Stepwise" detail="在 Codex 页面显示可拖动的后续建议浮层；建议由独立 API 配置生成。" checked={form.codexAppStepwiseEnabled} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppStepwiseEnabled", value)} />
            <FeatureToggle title="Stepwise 直接发送" detail="点击建议后自动发送；关闭时只填入输入框。" checked={form.codexAppStepwiseDirectSend} disabled={!masterEnabled || !form.codexAppStepwiseEnabled} onChange={(value) => setEnhanceFlag("codexAppStepwiseDirectSend", value)} />
            <FeatureToggle title="会话项目移动" detail="把会话移动到普通对话或其他本地项目。" checked={form.codexAppProjectMove} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppProjectMove", value)} />
            <FeatureToggle title="会话 ID 标识" detail="在侧边栏会话标题前显示短 ID 和 UUIDv7 创建时间。" checked={form.codexAppThreadIdBadge} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppThreadIdBadge", value)} />
            <FeatureToggle title="对话居中宽度" detail="把主对话和输入框限制到固定最大宽度。" checked={form.codexAppConversationView} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppConversationView", value)} />
            <FeatureToggle title="切换对话保留位置" detail="切换 thread 时恢复上一次浏览位置。" checked={form.codexAppThreadScrollRestore} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppThreadScrollRestore", value)} />
            <FeatureToggle title="Zed Remote open" detail="远程 SSH 文件引用可直接用 Zed Remote Development 打开。" checked={form.codexAppZedRemoteOpen} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppZedRemoteOpen", value)} />
            <FeatureToggle title="Upstream worktree" detail="从 upstream 分支创建 Git worktree。" checked={form.codexAppUpstreamWorktreeCreate} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppUpstreamWorktreeCreate", value)} />
            <FeatureToggle title="原生菜单栏位置" detail="把 ChatGPT Codex 菜单插入 ChatGPT 顶部原生菜单栏。" checked={form.codexAppNativeMenuPlacement} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppNativeMenuPlacement", value)} />
            <FeatureToggle title="原生菜单本地化" detail="启用上游 native menu localization inspector，修复原生菜单英文残留。" checked={form.codexAppNativeMenuLocalization} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("codexAppNativeMenuLocalization", value)} />
            <FeatureToggle title="Computer Use Guard" detail="启动 ChatGPT 前后保护 Windows Computer Use 插件、marketplace 和 js_repl 配置。" checked={form.computerUseGuardEnabled} onChange={(value) => setEnhanceFlag("computerUseGuardEnabled", value)} />
            <FeatureToggle title="Zed 项目记录" detail="记录成功打开的远程项目，并和当前 thread / global state 一起展示最近项目。" checked={form.zedRemoteProjectRegistryEnabled} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("zedRemoteProjectRegistryEnabled", value)} />
            <FeatureToggle title="同步 Zed 设置" detail="打开远程项目时同步到 Zed 项目记录，方便后续复用。" checked={form.zedRemoteSyncToZedSettings} disabled={!masterEnabled} onChange={(value) => setEnhanceFlag("zedRemoteSyncToZedSettings", value)} />
          </div>
          <Field label="Zed 打开策略">
            <select
              className="field-select"
              value={form.zedRemoteOpenStrategy}
              onChange={(event) => onFormChange({ ...form, zedRemoteOpenStrategy: event.currentTarget.value as ZedOpenStrategy })}
            >
              <option value="addToFocusedWorkspace">加入当前窗口</option>
              <option value="reuseWindow">复用窗口</option>
              <option value="newWindow">新窗口</option>
              <option value="default">默认策略</option>
            </select>
          </Field>
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>保存增强设置</Button>
            <Button onClick={() => void actions.repairPluginMarketplace()} variant="secondary">
              <Sparkles className="h-4 w-4" />
              修复插件市场
            </Button>
            <Button onClick={() => void actions.repairRemotePluginMarketplace()} variant="outline">
              <Download className="h-4 w-4" />
              释放官方远端插件缓存
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <DreamSkinPanel form={form} onFormChange={onFormChange} actions={actions} />
      <ZedRemoteProjectsPanel projects={zedRemoteProjects} form={form} actions={actions} />
      <MobileHelperPanel actions={actions} />
    </>
  );
}

function DreamSkinPanel({
  form,
  onFormChange,
  actions,
}: {
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  const config = form.codexAppDreamSkinThemeConfig && typeof form.codexAppDreamSkinThemeConfig === "object"
    ? form.codexAppDreamSkinThemeConfig
    : defaultDreamSkinConfig;
  const rawColors = config.colors && typeof config.colors === "object" ? config.colors as Record<string, unknown> : {};
  const colors = Object.fromEntries(
    Object.entries(dreamSkinDefaultColors).map(([key, fallback]) => [key, typeof rawColors[key] === "string" ? rawColors[key] : fallback]),
  ) as Record<DreamSkinColorKey, string>;
  const updateColor = (key: DreamSkinColorKey, value: string) => onFormChange({
    ...form,
    codexAppDreamSkinThemeConfig: {
      ...defaultDreamSkinConfig,
      ...config,
      colors: { ...defaultDreamSkinConfig.colors, ...rawColors, [key]: value },
    },
  });
  const resetTheme = () => onFormChange({
    ...form,
    codexAppDreamSkinTheme: "pink",
    codexAppDreamSkinThemeConfig: defaultDreamSkinConfig,
    codexAppDreamSkinImagePath: "",
  });
  return (
    <Panel>
      <CardHead title="DreamSkin" detail="为 ChatGPT 工作区应用本地主题和背景；主题数据只保存在本机" />
      <CardContent>
        <div className="feature-switch-grid">
          <FeatureToggle
            title="启用 DreamSkin"
            detail="启动时应用主题颜色和背景图；关闭后恢复 Codex 原始外观。"
            checked={form.codexAppDreamSkinEnabled}
            disabled={!form.enhancementsEnabled}
            onChange={(value) => onFormChange({ ...form, codexAppDreamSkinEnabled: value, codexAppDreamSkinPaused: value ? form.codexAppDreamSkinPaused : false })}
          />
          <FeatureToggle
            title="暂停主题"
            detail="保留主题配置，但暂时不向 Codex 页面应用。"
            checked={form.codexAppDreamSkinPaused}
            disabled={!form.codexAppDreamSkinEnabled}
            onChange={(value) => onFormChange({ ...form, codexAppDreamSkinPaused: value })}
          />
          <FeatureToggle
            title="宠物真实鼠标朝向"
            detail="主题宠物存在时使用真实指针方向计算视线。"
            checked={form.codexAppPetRealMouseLook}
            disabled={!form.codexAppDreamSkinEnabled}
            onChange={(value) => onFormChange({ ...form, codexAppPetRealMouseLook: value })}
          />
        </div>
        <div className="dream-skin-colors">
          {(Object.keys(dreamSkinDefaultColors) as DreamSkinColorKey[]).map((key) => (
            <Field key={key} label={{ background: "背景", panel: "面板", accent: "强调色", highlight: "高亮", text: "正文", muted: "次要文字" }[key]}>
              <div className="dream-skin-color-control">
                <input
                  aria-label={`${key} 颜色`}
                  type="color"
                  value={/^#[0-9a-f]{6}$/i.test(colors[key]) ? colors[key] : dreamSkinDefaultColors[key]}
                  onChange={(event) => updateColor(key, event.currentTarget.value)}
                />
                <Input value={colors[key]} onChange={(event) => updateColor(key, event.currentTarget.value)} />
              </div>
            </Field>
          ))}
        </div>
        <Field label="背景图片">
          <Input readOnly value={form.codexAppDreamSkinImagePath} placeholder="未选择，使用纯色主题" />
        </Field>
        <Toolbar>
          <Button onClick={() => void actions.chooseDreamSkinImagePath()} variant="secondary">
            <Image className="h-4 w-4" />
            选择背景
          </Button>
          <Button onClick={() => void (async () => { await actions.saveSettings(); await actions.restart(); })()}>
            <Rocket className="h-4 w-4" />
            保存并重启
          </Button>
          <Button onClick={resetTheme} variant="outline">恢复默认主题</Button>
        </Toolbar>
      </CardContent>
    </Panel>
  );
}

function MobileHelperPanel({ actions }: { actions: Actions }) {
  const mobileUrl = "http://127.0.0.1:57321/mobile";
  return (
    <Panel>
      <CardHead title="手机控制" detail="v1.2.24 起使用本地 helper 内置 mobile 入口，不再配置外部 relay 房间和密钥" />
      <CardContent>
        <div className="hint-line">
          <Smartphone className="h-4 w-4" />
          <span>从 ChatGPT Codex 启动 ChatGPT 后，helper 会提供 /mobile、/app-server/status 和 /app-server/ws。</span>
        </div>
        <div className="path-line compact-path">{mobileUrl}</div>
        <Toolbar>
          <Button onClick={() => void actions.copyText(mobileUrl, "手机控制入口已复制。")} variant="outline">
            <Copy className="h-4 w-4" />
            复制入口
          </Button>
        </Toolbar>
      </CardContent>
    </Panel>
  );
}

function ZedRemoteProjectsPanel({
  projects,
  form,
  actions,
}: {
  projects: ZedRemoteProjectsResult | null;
  form: BackendSettings;
  actions: Actions;
}) {
  const items = projects?.projects ?? [];
  return (
    <Panel>
      <CardHead title="Zed 最近项目" detail={projects?.message ?? "从当前会话、Codex global state、SQLite thread cwd 和本地记录汇总"} />
      <CardContent>
        <Toolbar>
          <Button onClick={() => void actions.refreshZedRemoteProjects()} variant="secondary">
            <RefreshCw className="h-4 w-4" />
            刷新项目
          </Button>
        </Toolbar>
        <div className="table zed-project-table">
          {items.length ? (
            items.map((project) => (
              <div className="zed-project-row" key={project.id}>
                <div>
                  <strong>{project.label || project.path}</strong>
                  <span>{zedProjectSourceLabel(project.source)} · {project.ssh.user ? `${project.ssh.user}@` : ""}{project.ssh.host}{project.ssh.port ? `:${project.ssh.port}` : ""}</span>
                  <small>{project.path}</small>
                </div>
                <div className="script-row-actions">
                  <Button onClick={() => void actions.openZedRemoteProject(project, form.zedRemoteOpenStrategy)} size="sm" variant="secondary">
                    <ExternalLink className="h-4 w-4" />
                    打开
                  </Button>
                  <Button onClick={() => void actions.forgetZedRemoteProject(project)} size="sm" variant="outline">
                    <Trash2 className="h-4 w-4" />
                    忘记
                  </Button>
                </div>
              </div>
            ))
          ) : (
            <div className="empty">暂无 Zed 远程项目。打开过项目后会自动记录，也可以从当前会话解析。</div>
          )}
        </div>
      </CardContent>
    </Panel>
  );
}

function UserScriptsScreen({ settings, market, actions }: { settings: SettingsResult | null; market: ScriptMarketResult | null; actions: Actions }) {
  const inventory = settings?.user_scripts;
  const scripts = inventory?.scripts ?? [];
  const marketScripts = market?.market.scripts ?? [];
  const installedCount = marketScripts.filter((script) => script.installed).length;
  return (
    <>
      <Panel>
        <CardHead title="脚本市场" detail={`${marketScripts.length} 个市场脚本，已安装 ${installedCount} 个，本地整体 ${inventory?.enabled === false ? "关闭" : "开启"}`} />
        <CardContent>
          <div className="metric-list">
            <Metric label="市场状态" value={market?.market.message ?? "尚未刷新"} />
            <Metric label="远程脚本" value={`${marketScripts.length} 个`} />
            <Metric label="已安装" value={`${installedCount} 个`} />
            <Metric label="本地整体" value={inventory?.enabled === false ? "关闭" : "开启"} />
          </div>
          <Toolbar>
            <Button onClick={() => void actions.refreshScriptMarket()}>
              <RefreshCw className="h-4 w-4" />
              刷新市场
            </Button>
            <Button onClick={() => void actions.openExternalUrl(SCRIPT_MARKET_REPOSITORY_URL)} variant="secondary">
              <ExternalLink className="h-4 w-4" />
              投稿
            </Button>
            <Button onClick={() => void actions.refreshCurrent()} variant="secondary">
              <RefreshCw className="h-4 w-4" />
              刷新本地
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="市场脚本" detail={market?.market.updatedAt ? `清单更新时间：${market.market.updatedAt}` : "从 GitHub 静态清单加载"} />
        <CardContent>
          {marketScripts.length ? (
            <div className="script-market-grid">
              {marketScripts.map((script) => (
                <MarketScriptCard key={script.id} script={script} actions={actions} />
              ))}
            </div>
          ) : (
            <div className="empty">{market?.status === "failed" ? market.message : "点击刷新市场加载远程脚本。"}</div>
          )}
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="本地脚本" detail="内置、手动和市场安装脚本；可在这里启停或删除用户脚本" />
        <CardContent>
          <div className="table">
            {scripts.length ? scripts.map((script) => <ScriptRow key={script.key} script={script} actions={actions} />) : <div className="empty">未发现用户脚本。</div>}
          </div>
        </CardContent>
      </Panel>
    </>
  );
}

function ProviderSyncScreen({
  settings,
  relay,
  computerUse,
  localSessions,
  sessionIndexCleanup,
  skillMcpBackups,
  providerSyncTargets,
  selectedProviderSyncTarget,
  onProviderSyncTargetChange,
  lastModeHistorySync,
  modeHistorySyncInProgress,
  form,
  onFormChange,
  actions,
}: {
  settings: SettingsResult | null;
  relay: RelayResult | null;
  computerUse: ComputerUseStatusResult | null;
  localSessions: LocalSessionsResult | null;
  sessionIndexCleanup: SessionIndexCleanupPreviewResult | null;
  skillMcpBackups: SkillMCPBackupsResult | null;
  providerSyncTargets: ProviderSyncTargetsResult | null;
  selectedProviderSyncTarget: string;
  onProviderSyncTargetChange: (value: string) => void;
  lastModeHistorySync: ModeHistorySyncResult | null;
  modeHistorySyncInProgress: boolean;
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  const backups = skillMcpBackups?.backups ?? [];
  const isWindows = computerUse?.platform === "windows";
  const selectedMode = activeRelayProfile(normalizeSettings(form)).relayMode;
  const appliedMode = normalizeRelayMode(relay?.appliedMode ?? relay?.activeMode ?? selectedMode);
  const historyTargetProvider = selectedProviderSyncTarget || (appliedMode === "official" ? "openai" : "CodexPlusPlus");
  const [selectedCleanupIds, setSelectedCleanupIds] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    setSelectedCleanupIds(new Set());
  }, [sessionIndexCleanup?.snapshotSha256]);
  const cleanupCandidates = sessionIndexCleanup?.candidates ?? [];
  const toggleCleanupCandidate = (id: string, checked: boolean) => {
    setSelectedCleanupIds((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  };
  const guideItems = [
    "自动修复只在 ChatGPT Codex 启动 ChatGPT 前运行，会整理旧对话归属、补回插件配置并重读插件市场。",
    "恢复插件配置会扫描本机已缓存插件，补回 plugins、marketplaces 和 node_repl MCP 配置，并执行 marketplace 刷新/重读。",
    ...(isWindows ? ["Computer Use 修复会写入本地兼容插件和用户环境变量，修复后需要重启 ChatGPT。"] : []),
    "Skill/MCP 恢复前会自动备份当前状态，并只替换 config.toml 里的 MCP、插件、市场和 features 表。",
    "切回官方时历史会话会整理为 openai；切到 API 时会整理为 ChatGPT Codex Tools 中转供应商。",
  ];
  return (
    <>
      <Panel>
        <CardHead title="历史会话修复" detail="切换官方或 API 后，让旧对话重新出现在当前模式下" />
        <CardContent>
          <label className="switch-row">
            <input
              checked={form.providerSyncEnabled}
              onChange={(event) => onFormChange({ ...form, providerSyncEnabled: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>启动前自动修复历史会话</strong>
              <small>模式切换时会自动尝试同步；若 ChatGPT 或 Codex 仍在运行会安全跳过。开启后每次启动前还会再检查一次。</small>
            </span>
          </label>
          <div className="relay-grid compact">
            <Metric label="当前模式" value={relayModeLabel(appliedMode)} />
            {selectedMode !== appliedMode ? <Metric label="已选模式" value={relayModeLabel(selectedMode)} /> : null}
            <Metric label="同步目标" value={historyTargetProvider} />
            <Metric label="最近同步" value={modeHistorySyncStatusLabel(lastModeHistorySync, relay, historyTargetProvider)} />
            <Metric label="自动修复" value={form.providerSyncEnabled ? "启动前执行" : "关闭"} />
            <Metric label="设置文件" value={settings?.settings_path ?? "未加载"} />
            <Metric label="页面增强" value={form.launchMode === "relay" ? "兼容模式" : "完整模式"} />
          </div>
          <div className="provider-sync-target-row">
            <label>
              <span>历史 Provider</span>
              <select
                className="field-select"
                disabled={!providerSyncTargets?.targets.length}
                onChange={(event) => onProviderSyncTargetChange(event.currentTarget.value)}
                value={selectedProviderSyncTarget}
              >
                {(providerSyncTargets?.targets ?? []).map((target) => (
                  <option key={target.id} value={target.id}>
                    {target.id}{target.isCurrentProvider ? "（当前）" : ""} · {providerSyncTargetSourcesLabel(target.sources)}
                  </option>
                ))}
              </select>
              <small>从 config、rollout、SQLite 与已保存记录汇总；同步只改归属字段，不删除消息正文。</small>
            </label>
            <Button onClick={() => void actions.refreshProviderSyncTargets(false)} size="icon" title="刷新同步目标" variant="outline">
              <RefreshCw className="h-4 w-4" />
            </Button>
          </div>
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>保存自动修复设置</Button>
            <Button disabled={modeHistorySyncInProgress} onClick={() => void actions.syncProvidersNow()} variant="outline">
              <RefreshCw className="h-4 w-4" />
              {modeHistorySyncInProgress ? "正在同步模式对话历史" : "同步模式对话历史"}
            </Button>
            <Button onClick={() => void actions.repairCodexPlugins()} variant="secondary">
              <Sparkles className="h-4 w-4" />
              恢复插件配置
            </Button>
            <Button onClick={() => void actions.repairCodexGoals()} variant="secondary">
              <CheckCircle2 className="h-4 w-4" />
              修复追求目标
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="本地会话与任务索引" detail="跨当前与旧版 SQLite 数据库分页读取，并安全处理失效索引" />
        <CardContent>
          <div className="relay-grid compact">
            <Metric label="当前页" value={localSessions ? `${Math.floor(localSessions.offset / localSessions.limit) + 1}` : "未加载"} />
            <Metric label="本页会话" value={`${localSessions?.sessions.length ?? 0} 个`} />
            <Metric label="候选数据库" value={`${localSessions?.dbPaths.length ?? 0} 个`} />
            <Metric label="后续页面" value={localSessions?.hasMore ? "有" : "无"} />
          </div>
          <Toolbar>
            <Button onClick={() => void actions.refreshLocalSessions(false, localSessions?.offset ?? 0)} variant="outline">
              <RefreshCw className="h-4 w-4" />
              刷新会话
            </Button>
            <Button
              disabled={!localSessions || localSessions.offset <= 0}
              onClick={() => void actions.refreshLocalSessions(true, Math.max(0, (localSessions?.offset ?? 0) - (localSessions?.limit ?? 50)))}
              variant="secondary"
            >
              <ArrowLeft className="h-4 w-4" />
              上一页
            </Button>
            <Button
              disabled={!localSessions?.hasMore}
              onClick={() => void actions.refreshLocalSessions(true, (localSessions?.offset ?? 0) + (localSessions?.limit ?? 50))}
              variant="secondary"
            >
              下一页
            </Button>
            <Button onClick={() => void actions.previewSessionIndexCleanup()} variant="outline">
              <ShieldCheck className="h-4 w-4" />
              检查任务索引
            </Button>
          </Toolbar>
          <div className="table backup-table">
            {localSessions?.sessions.length ? (
              localSessions.sessions.map((session) => (
                <div className="backup-row" key={session.id}>
                  <div>
                    <strong>{session.title || "未命名会话"}</strong>
                    <span>{session.modelProvider || "本地"} / {session.archived ? "已归档" : "活动"} / {formatTime(session.updatedAtMs)}</span>
                    <small>{session.cwd || session.id}</small>
                  </div>
                  <div className="script-row-actions">
                    <Button onClick={() => void actions.deleteLocalSession(session)} size="sm" variant="outline">
                      <Trash2 className="h-4 w-4" />
                      删除
                    </Button>
                  </div>
                </div>
              ))
            ) : (
              <div className="empty">{localSessions ? "当前页没有本地会话。" : "正在读取本地会话。"}</div>
            )}
          </div>
          {sessionIndexCleanup ? (
            <div className="session-index-cleanup-block">
              <div className="section-title-row">
                <div>
                  <strong>失效任务索引候选</strong>
                  <small>{cleanupCandidates.length ? `发现 ${cleanupCandidates.length} 条，请逐项核对` : "未发现仅存在于 session_index.jsonl 的记录"}</small>
                </div>
                {cleanupCandidates.length ? (
                  <label className="check-row compact-check-row">
                    <input
                      checked={selectedCleanupIds.size === cleanupCandidates.length}
                      onChange={(event) => setSelectedCleanupIds(event.currentTarget.checked ? new Set(cleanupCandidates.map((item) => item.id)) : new Set())}
                      type="checkbox"
                    />
                    <span>全选</span>
                  </label>
                ) : null}
              </div>
              <div className="table">
                {cleanupCandidates.map((candidate) => (
                  <label className="session-index-cleanup-row" key={candidate.id}>
                    <input
                      checked={selectedCleanupIds.has(candidate.id)}
                      onChange={(event) => toggleCleanupCandidate(candidate.id, event.currentTarget.checked)}
                      type="checkbox"
                    />
                    <span>
                      <strong>{candidate.threadName || "未命名任务"}</strong>
                      <small>{candidate.id} / {candidate.updatedAt}</small>
                    </span>
                  </label>
                ))}
              </div>
              {cleanupCandidates.length ? (
                <Toolbar>
                  <Button disabled={!selectedCleanupIds.size} onClick={() => void actions.applySessionIndexCleanup(Array.from(selectedCleanupIds))} variant="secondary">
                    <Trash2 className="h-4 w-4" />
                    清理已选 {selectedCleanupIds.size} 条
                  </Button>
                </Toolbar>
              ) : null}
            </div>
          ) : null}
        </CardContent>
      </Panel>
      {isWindows ? (
        <Panel>
          <CardHead title="Computer Use 修复" detail="Windows 本地修复 Computer Use 插件、marketplace、环境变量和 config.toml" />
          <CardContent>
            <div className="relay-grid compact">
              <Metric label="平台" value={platformLabel(computerUse?.platform ?? "unknown")} />
              <Metric label="总状态" value={computerUse?.allReady ? "可用" : "待修复"} />
              <Metric label="环境变量" value={computerUse?.envEnabled ? "已开启" : "未开启"} />
              <Metric label="插件缓存" value={computerUse?.cacheLatest ? "已写入" : "缺失"} />
              <Metric label="Marketplace" value={computerUse?.marketplaceManifest && computerUse?.marketplacePlugin ? "完整" : "待修复"} />
              <Metric label="Config 表" value={computerUse?.configReady ? "完整" : "待修复"} />
            </div>
            <div className="status-table">
              <StatusRow title="环境变量" status={computerUse?.envEnabled ? "ok" : "missing"} path={"User=" + (computerUse?.userEnv || "-") + " Process=" + (computerUse?.processEnv || "-")} />
              <StatusRow title="Codex home" status={computerUse?.codexHome ? "ok" : "missing"} path={computerUse?.codexHome ?? null} />
              <StatusRow title="Marketplace 根目录" status={computerUse?.marketplaceReady ? "ok" : "missing"} path={computerUse?.marketplaceRoot ?? null} />
              <StatusRow title="Marketplace manifest" status={computerUse?.marketplaceManifest ? "ok" : "missing"} path={computerUse?.marketplaceManifestPath ?? null} />
              <StatusRow title="Marketplace 插件" status={computerUse?.marketplacePlugin ? "ok" : "missing"} path={computerUse?.marketplacePluginPath ?? null} />
              <StatusRow title="插件缓存" status={computerUse?.cacheLatest ? "ok" : "missing"} path={computerUse?.cacheLatestPath ?? (computerUse?.cacheVersion ? "computer-use/" + computerUse.cacheVersion : null)} />
              <StatusRow title="Helper transport" status={computerUse?.helperTransport ? "ok" : "missing"} path={computerUse?.helperTransportPath ?? null} />
              <StatusRow title="config.toml" status={computerUse?.configReady ? "ok" : "missing"} path={computerUse?.configPath ?? null} />
            </div>
            {computerUse?.backupPath ? <div className="path-line compact-path">本次配置备份：{computerUse.backupPath}</div> : null}
            <div className="path-line compact-path">修复后请关闭所有 ChatGPT 窗口，再从 ChatGPT Codex 入口重新启动。</div>
            <Toolbar>
              <Button onClick={() => void actions.refreshComputerUse()} variant="outline">
                <RefreshCw className="h-4 w-4" />
                刷新状态
              </Button>
              <Button onClick={() => void actions.repairComputerUse()} variant="secondary">
                <Wrench className="h-4 w-4" />
                一键修复 Computer Use
              </Button>
            </Toolbar>
          </CardContent>
        </Panel>
      ) : null}
      <Panel>
        <CardHead title="Skill/MCP 备份" detail={skillMcpBackups?.backupRoot ?? "快照保存在 ~/.codex/backups_state/skill-mcp"} />
        <CardContent>
          <div className="relay-grid compact">
            <Metric label="备份数量" value={String(backups.length) + " 个"} />
            <Metric label="最近备份" value={backups[0]?.id ?? "暂无"} />
            <Metric label="保存范围" value="skills / plugins cache / marketplace / config 表" />
          </div>
          <Toolbar>
            <Button onClick={() => void actions.refreshSkillMcpBackups()} variant="outline">
              <RefreshCw className="h-4 w-4" />
              刷新
            </Button>
            <Button onClick={() => void actions.createSkillMcpBackup()} variant="secondary">
              <Save className="h-4 w-4" />
              创建备份
            </Button>
          </Toolbar>
          <div className="table backup-table">
            {backups.length ? (
              backups.map((backup) => (
                <div className="backup-row" key={backup.id}>
                  <div>
                    <strong>{backup.id}</strong>
                    <span>{backup.label || "manual"} / {formatBackupTime(backup.createdAt)} / {formatBytes(backup.sizeBytes)}</span>
                    <small>{backup.path}</small>
                  </div>
                  <div className="backup-flags">
                    <Badge status={backup.hasSkills ? "ok" : "missing"} />
                    <Badge status={backup.hasPluginCache ? "ok" : "missing"} />
                    <Badge status={backup.hasConfigSnapshot ? "ok" : "missing"} />
                  </div>
                  <div className="script-row-actions">
                    <Button onClick={() => void actions.restoreSkillMcpBackup(backup.id)} size="sm" variant="secondary">
                      <RefreshCw className="h-4 w-4" />
                      恢复
                    </Button>
                    <Button onClick={() => void actions.deleteSkillMcpBackup(backup.id)} size="sm" variant="outline">
                      <Trash2 className="h-4 w-4" />
                      删除
                    </Button>
                  </div>
                </div>
              ))
            ) : (
              <div className="empty">暂无 Skill/MCP 备份。</div>
            )}
          </div>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="说明" detail={isWindows ? "会话、插件、Computer Use 和 Skill/MCP 快照都可以在这里整理" : "会话、插件和 Skill/MCP 快照都可以在这里整理"} />
        <CardContent>
          <GuideList items={guideItems} />
        </CardContent>
      </Panel>
    </>
  );
}

function MaintenanceScreen({
  overview,
  watcher,
  settings,
  conversationHistoryRepair,
  conversationHistoryRepairInProgress,
  launchForm,
  onLaunchFormChange,
  removeOwnedData,
  onRemoveOwnedDataChange,
  actions,
}: {
  overview: OverviewResult | null;
  watcher: WatcherResult | null;
  settings: SettingsResult | null;
  conversationHistoryRepair: ConversationHistoryRepairResult | null;
  conversationHistoryRepairInProgress: boolean;
  launchForm: { appPath: string; debugPort: string; helperPort: string };
  onLaunchFormChange: (next: { appPath: string; debugPort: string; helperPort: string }) => void;
  removeOwnedData: boolean;
  onRemoveOwnedDataChange: (value: boolean) => void;
  actions: Actions;
}) {
  const savedCodexAppPath = settings?.settings.codexAppPath ?? "";
  const watcherPlatform = watcher?.platform ?? "unknown";
  const isWindows = watcherPlatform === "windows";
  const isMac = watcherPlatform === "darwin";
  const conversationHistoryProgress = conversationHistoryTaskProgress(conversationHistoryRepair);
  const conversationHistoryCancelling = conversationHistoryRepair?.taskStatus === "cancelling";
  const appPathPlaceholder = isWindows
    ? "选择 ChatGPT.exe、Codex.exe 或安装目录"
    : isMac
      ? "选择 ChatGPT.app"
      : "选择 ChatGPT 应用路径";
  const manualLaunchPlaceholder = savedCodexAppPath || (isWindows ? "OpenAI.Codex / OpenAI.ChatGPT，或 WindowsApps 外的 Codex.exe / ChatGPT.exe" : "/Applications/ChatGPT.app");
  return (
    <>
      <Panel>
        <CardHead title="检查与修复" detail={isWindows ? "检查入口、ChatGPT 应用和 Watcher 状态" : "检查入口和 ChatGPT 应用状态"} />
        <CardContent>
          <div className="status-table">
            <StatusRow title="ChatGPT 应用" status={overview?.codex_app.status} path={overview?.codex_app.path} />
            <StatusRow title="静默启动入口" status={overview?.silent_shortcut.status} path={overview?.silent_shortcut.path} />
            <StatusRow title="管理控制台入口" status={overview?.management_shortcut.status} path={overview?.management_shortcut.path} />
            {isWindows ? <StatusRow title="Watcher 自动接管" status={watcher?.enabled ? "ok" : "disabled"} path={watcher?.disabled_flag} /> : null}
          </div>
          <Toolbar>
            <Button onClick={() => void actions.checkHealth()}>检查</Button>
            <Button variant="secondary" onClick={() => void actions.repairCodexApp()}>
              <Wrench className="h-4 w-4" />
              修复 ChatGPT 应用
            </Button>
            <Button variant="secondary" onClick={() => void actions.repairShortcuts()}>修复快捷方式</Button>
            <Button variant="secondary" onClick={() => void actions.repairBackend()}>修复后端</Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="对话历史兼容修复" detail="清理旧对话中官方 Responses API 不接受的 namespace 字段" />
        <CardContent>
          <div className="platform-note limited">
            <ShieldCheck className="h-4 w-4" />
            <span>必须先完全退出 ChatGPT 和 Codex；后端检测到活动进程时会拒绝执行。执行前会自动备份原始会话文件；只删除 function_call / custom_tool_call 类型 response_item 的 payload 顶层 namespace，不会删除消息正文、工具输出或嵌套参数，也不会修改当前配置。</span>
          </div>
          <div className="relay-grid compact">
            <Metric label="扫描文件" value={conversationHistoryRepair ? `${conversationHistoryRepair.scannedFiles ?? 0} 个` : "尚未执行"} />
            <Metric label="扫描记录" value={conversationHistoryRepair ? `${conversationHistoryRepair.scannedRecords ?? 0} 条` : "尚未执行"} />
            <Metric label="需修复文件" value={conversationHistoryRepair ? `${conversationHistoryRepair.changedFiles ?? 0} 个` : "尚未执行"} />
            <Metric label="需修复记录" value={conversationHistoryRepair ? `${conversationHistoryRepair.changedRecords ?? 0} 条` : "尚未执行"} />
            <Metric label="跳过异常记录" value={conversationHistoryRepair ? `${conversationHistoryRepair.invalidRecords ?? 0} 条` : "尚未执行"} />
          </div>
          {conversationHistoryProgress ? (
            <div className="history-repair-progress">
              <InlineTaskProgress progress={conversationHistoryProgress} />
              <div className="history-repair-progress-meta" aria-live="polite">
                <span>
                  文件进度：{conversationHistoryRepair?.processedFiles ?? 0} / {conversationHistoryRepair?.totalFiles ?? 0}
                </span>
                <span>
                  数据进度：{formatBytes(conversationHistoryRepair?.processedBytes ?? 0) || "0 B"} / {formatBytes(conversationHistoryRepair?.totalBytes ?? 0) || "0 B"}
                </span>
                {conversationHistoryRepair?.currentFile ? <span className="history-repair-current-file">当前文件：{conversationHistoryRepair.currentFile}</span> : null}
              </div>
            </div>
          ) : null}
          {conversationHistoryRepair ? (
            <>
              {conversationHistoryRepair.requiredSpaceBytes ? (
                <div className="path-line compact-path">
                  完整备份数据量：{formatBytes(conversationHistoryRepair.changedBytes ?? 0)} · 预检所需空间：{formatBytes(conversationHistoryRepair.requiredSpaceBytes)} · 当前可用：{formatBytes(conversationHistoryRepair.freeSpaceBytes ?? 0) || "0 B"}
                </div>
              ) : null}
              <div className="path-line compact-path">
                备份目录：{conversationHistoryRepair.backupDir || (conversationHistoryRepair.taskStatus === "ok" ? "无需创建（未发现需修复记录）" : conversationHistoryRepairInProgress ? "尚未创建" : "未创建（执行已安全停止）")}
              </div>
            </>
          ) : null}
          <Toolbar>
            <Button disabled={conversationHistoryRepairInProgress} onClick={() => void actions.repairConversationHistory()} variant="secondary">
              <Wrench className="h-4 w-4" />
              {conversationHistoryCancelling ? "正在取消" : conversationHistoryRepairInProgress ? "修复中" : "修复对话历史格式"}
            </Button>
            {conversationHistoryRepairInProgress ? (
              <Button disabled={conversationHistoryCancelling} onClick={() => void actions.cancelConversationHistoryRepair()} variant="outline">
                <Square className="h-4 w-4" />
                {conversationHistoryCancelling ? "正在取消" : "取消修复"}
              </Button>
            ) : null}
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="入口管理" detail={isWindows ? "快捷方式写入系统实际桌面位置，不使用写死桌面路径" : "在 /Applications 写入 ChatGPT Codex 应用入口"} />
        <CardContent>
          <label className="check-row">
            <input checked={removeOwnedData} onChange={(event) => onRemoveOwnedDataChange(event.currentTarget.checked)} type="checkbox" />
            <span>卸载时移除 ChatGPT Codex 托管数据</span>
          </label>
          <Toolbar>
            <Button onClick={() => void actions.installEntrypoints()}>安装入口</Button>
            <Button variant="secondary" onClick={() => void actions.uninstallEntrypoints()}>卸载入口</Button>
            <Button variant="secondary" onClick={() => void actions.repairShortcuts()}>修复入口</Button>
            {watcherPlatform === "windows" ? (
              <Button variant="outline" onClick={() => void actions.uninstallCodexTools()}>
                <Trash2 className="h-4 w-4" />
                卸载 ChatGPT Codex
              </Button>
            ) : null}
          </Toolbar>
        </CardContent>
      </Panel>
      {isWindows ? (
        <Panel>
          <CardHead title="自动接管" detail="Windows watcher 用于保持 ChatGPT Codex 接管状态" />
          <CardContent>
            <div className="platform-note">
              <ShieldCheck className="h-4 w-4" />
              <span>Windows 使用当前用户 Run 注册表项保持 ChatGPT Codex 静默接管，并会清理旧版本启动目录快捷方式以避免重复启动。</span>
            </div>
            <div className="status-table compact-path">
              <StatusRow title="Run 项名称" status={watcher?.run_value_installed ? "installed" : "not_checked"} path={watcher?.run_value_name || "ChatGPTCodexToolsWatcher"} />
              <StatusRow title="旧启动快捷方式" status={watcher?.startup_shortcut_installed ? "found" : "ok"} path={watcher?.startup_shortcut || null} />
              <StatusRow title="启动器命令" status={watcher?.launcher_path ? "found" : "not_checked"} path={`${watcher?.launcher_path || ""} ${watcher?.launcher_arguments || ""}`.trim() || null} />
            </div>
            <Toolbar>
              <Button variant="secondary" onClick={() => void actions.installWatcher()}>安装 watcher</Button>
              <Button variant="secondary" onClick={() => void actions.uninstallWatcher()}>移除 watcher</Button>
              <Button variant="secondary" onClick={() => void actions.enableWatcher()}>启用</Button>
              <Button variant="secondary" onClick={() => void actions.disableWatcher()}>禁用</Button>
            </Toolbar>
          </CardContent>
        </Panel>
      ) : null}
      <Panel>
        <CardHead title="ChatGPT 应用路径" detail={isWindows ? "自动识别合并版 MSIX；也可选择 WindowsApps 外的 ChatGPT.exe / Codex.exe" : "选择 ChatGPT.app 后，之后静默启动会自动复用"} />
        <CardContent>
          <div className="status-table">
            <StatusRow title="保存路径" status={savedCodexAppPath ? "ok" : "not_checked"} path={savedCodexAppPath || null} />
            <StatusRow title="当前识别" status={overview?.codex_app.status} path={overview?.codex_app.path} />
          </div>
          <Field label="保存的应用路径">
            <Input
              value={settings?.settings.codexAppPath ?? ""}
              placeholder={appPathPlaceholder}
              readOnly
            />
          </Field>
          <Toolbar>
            <Button onClick={() => void actions.repairCodexApp()}>
              <Wrench className="h-4 w-4" />
              修复 ChatGPT 应用
            </Button>
            <Button onClick={() => void actions.chooseCodexAppPath("folder")}>{isWindows ? "选择应用目录" : "选择 ChatGPT.app"}</Button>
            {isWindows ? <Button variant="secondary" onClick={() => void actions.chooseCodexAppPath("file")}>选择 ChatGPT.exe / Codex.exe</Button> : null}
            <Button variant="secondary" onClick={() => void actions.clearCodexAppPath()}>清除保存路径</Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="手动启动" detail="应用路径留空时使用已保存路径；没有保存路径时使用自动探测" />
        <CardContent>
          <Field label="应用路径覆盖">
            <Input
              value={launchForm.appPath}
              onChange={(event) => onLaunchFormChange({ ...launchForm, appPath: event.currentTarget.value })}
              placeholder={manualLaunchPlaceholder}
            />
          </Field>
          <div className="form-row">
            <Field label="Debug 端口">
              <Input
                value={launchForm.debugPort}
                onChange={(event) => onLaunchFormChange({ ...launchForm, debugPort: event.currentTarget.value })}
              />
            </Field>
            <Field label="Helper 端口">
              <Input
                value={launchForm.helperPort}
                onChange={(event) => onLaunchFormChange({ ...launchForm, helperPort: event.currentTarget.value })}
              />
            </Field>
          </div>
          <Toolbar>
            <Button onClick={() => void actions.launch()}>启动 ChatGPT Codex</Button>
            <Button variant="secondary" onClick={() => void actions.saveManualCodexAppPath()}>
              保存为默认路径
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
    </>
  );
}

function AboutScreen({
  overview,
  updateInfo,
  actions,
}: {
  overview: OverviewResult | null;
  updateInfo: UpdateResult | null;
  actions: Actions;
}) {
  const update = updateInfo ?? overview?.update ?? null;
  return (
    <>
      <Panel>
        <CardHead title="关于 ChatGPT Codex Tools" detail="本地 ChatGPT Codex 增强、管理工具和安装包维护" />
        <CardContent>
          <div className="metric-list">
            <Metric label="ChatGPT Codex Tools 版本" value={overview?.current_version ?? "-"} />
            <Metric label="ChatGPT 版本" value={overview?.codex_version ?? "未检测到"} />
            <Metric label="项目地址" value="github.com/hereww/codextools" />
            <Metric label="电报群" value="t.me/wanai8" />
          </div>
          <Toolbar>
            <Button onClick={() => void actions.openExternalUrl(PROJECT_REPOSITORY_URL)} variant="secondary">
              <ExternalLink className="h-4 w-4" />
              打开项目主页
            </Button>
            <Button onClick={() => void actions.openExternalUrl(PROJECT_ISSUES_URL)} variant="secondary">
              <ExternalLink className="h-4 w-4" />
              反馈问题
            </Button>
            <Button onClick={() => void actions.openExternalUrl(TELEGRAM_COMMUNITY_URL)} variant="secondary">
              <MessageCircle className="h-4 w-4" />
              电报群
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="版本更新" detail={update?.message ?? "检查 GitHub 最新发布并下载当前系统安装包"} />
        <CardContent>
          <div className="metric-list">
            <Metric label="当前版本" value={overview?.current_version ?? update?.currentVersion ?? "-"} />
            <Metric label="最新版本" value={update?.latestVersion || update?.tagName || "未检查"} />
            <Metric label="更新状态" value={statusLabel(update?.updateStatus ?? "not_checked")} />
            <Metric label="安装包" value={update?.assetName || "未选择"} />
            {update ? <Metric label="安装方式" value={updateInstallHint(update) || "按系统默认安装"} /> : null}
            {update?.downloadedPath ? <Metric label="下载位置" value={update.downloadedPath} /> : null}
          </div>
          <Toolbar>
            <Button onClick={() => void actions.checkUpdate()} variant="secondary">
              <RefreshCw className="h-4 w-4" />
              检查更新
            </Button>
            {update?.updateStatus === "available" ? (
              <Button onClick={() => void actions.installUpdate()}>
                <Download className="h-4 w-4" />
                下载更新
              </Button>
            ) : null}
            <Button onClick={() => void actions.openExternalUrl(update?.releaseUrl || codexToolsReleaseUrl())} variant="secondary">
              <ExternalLink className="h-4 w-4" />
              发布页面
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
    </>
  );
}

function ProxyScreen({
  form,
  onFormChange,
  actions,
}: {
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  const update = (patch: Partial<BackendSettings>) => onFormChange({ ...form, ...patch });
  const toggleRows: Array<{ key: keyof BackendSettings; title: string; detail: string }> = [
    { key: "proxyRelayEnabled", title: "中转请求", detail: "Relay 上游请求使用该代理；供应商详情中的专用代理优先。图片、VLM、音频和 Stepwise 可在各自用途单独启用。" },
    { key: "proxyRemoteControlEnabled", title: "远程控制", detail: "启动 ChatGPT 时注入原生应用代理，帮助远程控制 WebSocket 通过代理连接；不会修改系统 WinHTTP。" },
    { key: "proxyOfficialAuthEnabled", title: "官方登录 / 账号绑定", detail: "为 ChatGPT 原生登录和账号绑定启用应用代理；与远控、语音共用原生应用的代理通道。" },
    { key: "proxyRealtimeEnabled", title: "ChatGPT 高级语音 / Realtime", detail: "官方 Realtime 信令与 WebSocket 使用此代理；语音仍只访问官方服务，并与远控/登录共用原生应用代理通道。" },
    { key: "proxyModelCatalogEnabled", title: "模型目录", detail: "管理器请求 /models 时使用代理。" },
    { key: "proxyAudioEnabled", title: "音频转写", detail: "中转模式下的音频转写请求使用代理。" },
    { key: "proxyVlmEnabled", title: "VLM 图片理解", detail: "图片理解请求使用代理；不会把图片内容写入诊断日志。" },
    { key: "proxyStepwiseEnabled", title: "Stepwise", detail: "Stepwise 建议请求使用代理。" },
  ];
  return (
    <>
      <Panel>
        <CardHead title="网络代理" detail="后端用途可独立控制；ChatGPT 原生应用代理按进程共享，不修改 Windows WinHTTP 或系统代理。" />
        <CardContent>
          <label className="switch-row">
            <input checked={form.proxyEnabled} onChange={(event) => update({ proxyEnabled: event.currentTarget.checked })} type="checkbox" />
            <span>
              <strong>启用全局代理配置</strong>
              <small>填写代理地址后，再选择需要代理的后端请求和原生应用能力。</small>
            </span>
          </label>
          <Field label="HTTP / HTTPS 代理地址">
            <Input
              value={form.proxyUrl}
              onChange={(event) => update({ proxyUrl: event.currentTarget.value })}
              placeholder="http://127.0.0.1:10809"
              spellCheck={false}
            />
          </Field>
          <Field label="不代理地址">
            <Input
              value={form.proxyNoProxy}
              onChange={(event) => update({ proxyNoProxy: event.currentTarget.value })}
              placeholder="127.0.0.1,localhost,[::1]"
              spellCheck={false}
            />
          </Field>
          <p className="field-hint">支持 http:// 和 https://，可带认证信息。原生应用用途需完全退出并重启 ChatGPT 才生效；代理作用于 ChatGPT 进程的网络栈，不能按 API 路径精确隔离。后端用途仍按开关分别处理。</p>
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>
              <Save className="h-4 w-4" />
              保存代理地址
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="代理用途" detail="后端请求用途独立生效。远程控制、官方登录与高级语音共享 ChatGPT 原生进程代理，开启其中一项可能让该进程的其他网络请求也经过代理。" />
        <CardContent>
          {toggleRows.map((row) => (
            <label className="switch-row" key={String(row.key)}>
              <input
                checked={Boolean(form[row.key])}
                disabled={!form.proxyEnabled}
                onChange={(event) => update({ [row.key]: event.currentTarget.checked } as Partial<BackendSettings>)}
                type="checkbox"
              />
              <span>
                <strong>{row.title}</strong>
                <small>{row.detail}</small>
              </span>
            </label>
          ))}
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>
              <Save className="h-4 w-4" />
              保存用途设置
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
    </>
  );
}

function SettingsScreen({
  settings,
  theme,
  form,
  onFormChange,
  actions,
}: {
  settings: SettingsResult | null;
  theme: Theme;
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  return (
    <>
      <Panel>
        <CardHead title="基础设置" detail={settings?.settings_path ?? ""} />
        <CardContent>
          <div className="language-note">
            <strong>多语言模块</strong>
            <span>语言切换已放到右上角工具区；其他维护者可以在 web/src/i18n/translations.ts 中继续补充翻译。</span>
          </div>
          <div className="theme-row">
            <div>
              <strong>界面主题</strong>
              <span>当前为{theme === "dark" ? "深色" : "浅色"}模式。</span>
            </div>
            <Button variant="secondary" onClick={actions.toggleTheme}>切换主题</Button>
          </div>
          <Field label="供应商测试模型">
            <Input
              value={form.relayTestModel}
              onChange={(event) => onFormChange({ ...form, relayTestModel: event.currentTarget.value })}
              placeholder="例如 gpt-5-mini"
            />
          </Field>
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>保存设置</Button>
            <Button variant="secondary" onClick={() => void actions.resetSettings()}>
              重置设置
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="Stepwise" detail="基于当前对话生成下一步建议，使用独立的 OpenAI-compatible Chat Completions API" />
        <CardContent>
          <div className="form-row">
            <Field label="Base URL">
              <Input
                value={form.codexAppStepwiseBaseUrl}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseBaseUrl: event.currentTarget.value })}
                placeholder="https://api.example.com/v1"
              />
            </Field>
            <Field label="Model">
              <Input
                value={form.codexAppStepwiseModel}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseModel: event.currentTarget.value })}
                placeholder="例如 gpt-5-mini"
              />
            </Field>
          </div>
          <Field label="API Key">
            <Input
              type="password"
              value={form.codexAppStepwiseApiKey}
              onChange={(event) => onFormChange({ ...form, codexAppStepwiseApiKey: event.currentTarget.value })}
              placeholder="留空时读取环境变量"
            />
          </Field>
          <div className="form-row">
            <Field label="API Key 环境变量">
              <Input
                value={form.codexAppStepwiseApiKeyEnv}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseApiKeyEnv: event.currentTarget.value })}
              />
            </Field>
            <Field label="最多建议数">
              <Input
                max={6}
                min={0}
                type="number"
                value={form.codexAppStepwiseMaxItems}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseMaxItems: clampNumber(Number(event.currentTarget.value), 0, 6) })}
              />
            </Field>
          </div>
          <div className="form-row">
            <Field label="最大输入字符">
              <Input
                max={24000}
                min={1000}
                type="number"
                value={form.codexAppStepwiseMaxInputChars}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseMaxInputChars: clampNumber(Number(event.currentTarget.value), 1000, 24000) })}
              />
            </Field>
            <Field label="最大输出 tokens">
              <Input
                max={4000}
                min={100}
                type="number"
                value={form.codexAppStepwiseMaxOutputTokens}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseMaxOutputTokens: clampNumber(Number(event.currentTarget.value), 100, 4000) })}
              />
            </Field>
            <Field label="超时毫秒">
              <Input
                max={60000}
                min={1000}
                type="number"
                value={form.codexAppStepwiseTimeoutMs}
                onChange={(event) => onFormChange({ ...form, codexAppStepwiseTimeoutMs: clampNumber(Number(event.currentTarget.value), 1000, 60000) })}
              />
            </Field>
          </div>
          <Toolbar>
            <Button onClick={() => void actions.testStepwiseSettings(form)} variant="secondary">
              <TestTube className="h-4 w-4" />
              测试连接
            </Button>
            <Button onClick={() => void actions.saveSettings()}>保存 Stepwise</Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="ChatGPT 启动参数" detail="启动 ChatGPT App 时追加到默认 CDP 参数后。留空则保持默认启动行为。" />
        <CardContent>
          <Field label="额外参数">
            <Textarea
              className="launch-args-input"
              placeholder="--force_high_performance_gpu"
              spellCheck={false}
              value={codexExtraArgsToInput(form.codexExtraArgs)}
              onChange={(event) =>
                onFormChange({
                  ...form,
                  codexExtraArgs: inputToCodexExtraArgs(event.currentTarget.value),
                })
              }
            />
          </Field>
          <p className="field-hint">每行一个参数，例如 --force_high_performance_gpu。不需要填写 open 或 --args。</p>
          <Toolbar>
            <Button onClick={() => void actions.saveSettings()}>保存设置</Button>
          </Toolbar>
        </CardContent>
      </Panel>
      <Panel>
        <CardHead title="图片覆盖层" detail="启动 ChatGPT 时在窗口上叠加一张本地参考图，适合按截图对齐界面。" />
        <CardContent>
          <label className="switch-row">
            <input
              checked={form.codexAppImageOverlayEnabled}
              onChange={(event) => onFormChange({ ...form, codexAppImageOverlayEnabled: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>启用图片覆盖层</strong>
              <small>覆盖层由本地 helper 提供，只读取你选择的图片文件；重启或刷新 Codex 注入后生效。</small>
            </span>
          </label>
          <Field label="覆盖图片路径">
            <Input
              value={form.codexAppImageOverlayPath}
              onChange={(event) => onFormChange({ ...form, codexAppImageOverlayPath: event.currentTarget.value })}
              placeholder="/path/to/reference.png"
            />
          </Field>
          <Field label={`透明度：${form.codexAppImageOverlayOpacity || 35}%`}>
            <input
              className="range-input"
              max={100}
              min={1}
              onChange={(event) => onFormChange({ ...form, codexAppImageOverlayOpacity: Number(event.currentTarget.value) || 35 })}
              type="range"
              value={form.codexAppImageOverlayOpacity || 35}
            />
          </Field>
          <Field label="图片适配方式">
            <select
              className="field-select"
              value={form.codexAppImageOverlayFitMode}
              onChange={(event) => onFormChange({
                ...form,
                codexAppImageOverlayFitMode: event.currentTarget.value as BackendSettings["codexAppImageOverlayFitMode"],
              })}
            >
              <option value="fit">完整显示</option>
              <option value="fill">填满窗口</option>
              <option value="stretch">拉伸</option>
              <option value="tile">平铺</option>
              <option value="center">原尺寸居中</option>
            </select>
          </Field>
          <Toolbar>
            <Button onClick={() => void actions.chooseImageOverlayPath()} variant="secondary">
              <Image className="h-4 w-4" />
              选择图片
            </Button>
            <Button onClick={() => void actions.saveSettings()}>保存覆盖设置</Button>
            <Button onClick={() => void actions.resetImageOverlaySettings()} variant="outline">
              重置覆盖设置
            </Button>
          </Toolbar>
        </CardContent>
      </Panel>
    </>
  );
}

function LogsScreen({ logs, actions }: { logs: LogsResult | null; actions: Actions }) {
  const lines = splitLogLines(logs?.text ?? "");
  return (
    <Panel fill className="support-panel">
      <CardHead title="最近日志" detail={logs?.path ?? ""} />
      <CardContent className="support-content">
        <div className="log-lines">
          {lines.length ? (
            lines.map((line, index) => (
              <div className="log-line" key={`${index}-${line.slice(0, 12)}`}>
                <span>{index + 1}</span>
                <code>{line || " "}</code>
              </div>
            ))
          ) : (
            <div className="empty">暂无日志。</div>
          )}
        </div>
        <Toolbar>
          <Button onClick={() => void actions.refreshLogs()}>刷新</Button>
          <Button variant="secondary" onClick={() => void actions.copyLogs()}>
            复制
          </Button>
        </Toolbar>
      </CardContent>
    </Panel>
  );
}

function DiagnosticsScreen({ diagnostics, actions }: { diagnostics: DiagnosticsResult | null; actions: Actions }) {
  return (
    <Panel fill className="support-panel">
      <CardHead title="诊断报告" detail="包含版本、路径、设置和平台信息" />
      <CardContent className="support-content">
        <Textarea className="log-view tall" readOnly value={diagnostics?.report ?? "尚未生成诊断报告。"} />
        <Toolbar>
          <Button onClick={() => void actions.refreshDiagnostics()}>重新生成</Button>
          <Button variant="secondary" onClick={() => void actions.copyDiagnostics()}>
            复制报告
          </Button>
        </Toolbar>
      </CardContent>
    </Panel>
  );
}

function RelayProfileList({
  form,
  onFormChange,
  onEdit,
  switching,
  actions,
}: {
  form: BackendSettings;
  onFormChange: (value: BackendSettings) => void;
  onEdit: (id: string) => void;
  switching: boolean;
  actions: Actions;
}) {
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const next = reorderRelayProfiles(form, String(active.id), String(over.id));
    if (next !== form) onFormChange(next);
  };
  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
      <SortableContext items={form.relayProfiles.map((profile) => profile.id)} strategy={verticalListSortingStrategy}>
        <div className="relay-profile-list">
          {form.relayProfiles.map((profile, index) => (
            <SortableRelayProfileCard
              actions={actions}
              form={form}
              index={index}
              key={profile.id}
              onEdit={onEdit}
              onFormChange={onFormChange}
              profile={profile}
              switching={switching}
            />
          ))}
        </div>
      </SortableContext>
    </DndContext>
  );
}

function ProviderPresetPanel({ onSelect }: { onSelect: (preset: ProviderPreset) => void }) {
  return (
    <Panel>
      <CardHead title="供应商预设" detail="选择后生成一个可编辑供应商草稿；不会自动写入 Key 或覆盖当前配置。" />
      <CardContent>
        <div className="provider-preset-grid">
          {providerPresets.map((preset) => (
            <button className="provider-preset-card" key={preset.id} onClick={() => onSelect(preset)} type="button">
              <span>
                <strong>{preset.name}</strong>
                <small>{providerPresetCategoryLabel(preset.category)} · {relayProtocolLabel(preset.protocol)}</small>
              </span>
              <code>{preset.model || "official"}</code>
            </button>
          ))}
        </div>
      </CardContent>
    </Panel>
  );
}

function SortableRelayProfileCard({
  form,
  profile,
  index,
  onFormChange,
  onEdit,
  switching,
  actions,
}: {
  form: BackendSettings;
  profile: RelayProfile;
  index: number;
  onFormChange: (value: BackendSettings) => void;
  onEdit: (id: string) => void;
  switching: boolean;
  actions: Actions;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: profile.id });
  const active = profile.id === form.activeRelayId;
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <div
      className={`relay-profile-card ${active ? "active" : ""} ${isDragging ? "dragging" : ""}`}
      data-relay-profile-id={profile.id}
      key={profile.id}
      onKeyDown={(event) => {
        if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) {
          event.preventDefault();
          onEdit(profile.id);
        }
      }}
      onClick={() => onEdit(profile.id)}
      ref={setNodeRef}
      role="button"
      style={style}
      tabIndex={0}
    >
      <button
        aria-label="拖动排序"
        className="relay-drag"
        title="拖动排序"
        type="button"
        onClick={(event) => event.stopPropagation()}
        {...attributes}
        {...listeners}
      >
        <GripVertical className="h-4 w-4" />
      </button>
      <span className="relay-index" title={profile.name || "未命名供应商"}>
        {providerInitial(profile.name)}
      </span>
      <span className="relay-summary">
        <strong>{profile.name || "未命名供应商"}</strong>
        <small>{relayModeLabel(profile.relayMode)} · {relayProtocolLabel(profile.protocol)} · {relayProfileConfigBrief(profile)}</small>
        <span className={`image-chip ${profile.imageGenerationEnabled ? "enabled" : ""}`}>
          <Image className="h-3.5 w-3.5" />
          {relayImageModeLabel(profile)}
        </span>
        <span className={`image-chip official-chip ${profile.officialAuthContents.trim() ? "enabled" : ""}`}>
          <KeyRound className="h-3.5 w-3.5" />
          {officialBindingStatusLabel(profile)}
        </span>
        {profile.sub2apiEnabled ? (
          <span className={`image-chip ${profile.sub2apiMultiplier.trim() ? "enabled" : ""}`}>
            Sub2API · {profile.sub2apiMultiplier.trim() ? `${profile.sub2apiMultiplier.trim()}x` : "未读取"}
          </span>
        ) : null}
      </span>
      <span className="relay-card-actions">
        <Button
          className={`relay-use-button ${active ? "active" : ""}`}
          disabled={switching}
          onClick={(event) => {
            event.stopPropagation();
            if (switching) return;
            const next = syncLegacyRelayFields({ ...form, activeRelayId: profile.id });
            void actions.switchRelayProfile(next);
          }}
          size="sm"
          title={active ? "重新应用当前供应商" : "设为当前"}
          variant={active ? "secondary" : "outline"}
        >
          <CheckCircle2 className="h-4 w-4" />
          {active ? "重新应用" : "使用"}
        </Button>
        <span className="relay-card-extra">
          <Button
            onClick={(event) => {
              event.stopPropagation();
              void actions.testRelayProfile(profile);
            }}
            size="icon"
            title="发送 hi 测试"
            variant="ghost"
          >
            <TestTube className="h-4 w-4" />
          </Button>
          <Button
            onClick={(event) => {
              event.stopPropagation();
              onEdit(profile.id);
            }}
            size="icon"
            title="编辑"
            variant="ghost"
          >
            <Edit3 className="h-4 w-4" />
          </Button>
          <Button
            onClick={(event) => {
              event.stopPropagation();
              onFormChange(duplicateRelayProfile(form, profile.id));
            }}
            size="icon"
            title="复制"
            variant="ghost"
          >
            <Copy className="h-4 w-4" />
          </Button>
          <Button
            disabled={form.relayProfiles.length <= 1}
            onClick={(event) => {
              event.stopPropagation();
              onFormChange(removeRelayProfile(form, profile.id));
            }}
            size="icon"
            title="删除供应商"
            variant="ghost"
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </span>
      </span>
    </div>
  );
}

function MarketScriptCard({ script, actions }: { script: ScriptMarketItem; actions: Actions }) {
  const status = script.updateAvailable ? "可更新" : script.installed ? `已安装 ${script.installedVersion}` : "未安装";
  return (
    <div className="script-market-card">
      <div className="script-market-title">
        <div>
          <strong>{script.name}</strong>
          <span>{script.author || "未知作者"}</span>
        </div>
        <UiBadge variant={script.updateAvailable ? "default" : script.installed ? "secondary" : "outline"}>{status}</UiBadge>
      </div>
      <p className="script-market-description">{script.description || "暂无描述。"}</p>
      <div className="script-market-tags">
        <span className="script-market-tag">v{script.version}</span>
        {script.tags.map((tag) => (
          <span className="script-market-tag" key={tag}>{tag}</span>
        ))}
      </div>
      <div className="script-market-actions">
        <Button onClick={() => void actions.installMarketScript(script.id)} size="sm">
          <Download className="h-4 w-4" />
          {script.updateAvailable ? "更新" : script.installed ? "重新安装" : "安装"}
        </Button>
        {script.homepage ? (
          <Button onClick={() => void actions.openExternalUrl(script.homepage)} size="sm" variant="secondary">
            <ExternalLink className="h-4 w-4" />
            主页
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function RelayProfileDetail({
  profile,
  form,
  relay,
  isNew = false,
  onBack,
  onFormChange,
  onSaved,
  actions,
}: {
  profile: RelayProfile;
  form: BackendSettings;
  relay: RelayResult | null;
  isNew?: boolean;
  onBack: () => void;
  onFormChange: (value: BackendSettings) => void;
  onSaved?: () => void;
  actions: Actions;
}) {
  const [draft, setDraft] = useState<RelayProfile>(profile);
  const [imageSettingsApplying, setImageSettingsApplying] = useState(false);
  const isActive = !isNew && profile.id === form.activeRelayId;
  useEffect(() => {
    setDraft(profile);
  }, [
    profile.id,
    profile.name,
    profile.relayMode,
    profile.baseUrl,
    profile.apiKey,
    profile.officialAuthContents,
    profile.officialAccountLabel,
    profile.officialAuthUpdatedAt,
    profile.configContents,
    profile.authContents,
    profile.imageGenerationEnabled,
    profile.imageGenerationUseSeparateApi,
    profile.imageGenerationBaseUrl,
    profile.imageGenerationApiKey,
    profile.proxyEnabled,
    profile.proxyUrl,
  ]);
  const saveDraftSettings = async () => {
    const next = isNew ? addRelayProfile(form, draft) : updateRelayProfile(form, profile.id, draft);
    const result = await actions.saveSettingsValue(next, true);
    return result ? normalizeSettings(result.settings) : null;
  };
  const syncDraftFromSettings = (settingsValue: BackendSettings | null) => {
    const updated = settingsValue?.relayProfiles.find((item) => item.id === profile.id);
    if (updated) setDraft(updated);
  };
  const bindCurrentOfficialAuth = async () => {
    if (isNew) {
      actions.showNotice("官方账号绑定", "请先保存新供应商，再绑定官方账号。", "failed");
      return;
    }
    await saveDraftSettings();
    const result = await actions.bindOfficialAuth(profile.id);
    if (result) syncDraftFromSettings(normalizeSettings(result.settings));
  };
  const unbindCurrentOfficialAuth = async () => {
    if (isNew) {
      actions.showNotice("官方账号绑定", "新供应商还没有官方账号绑定。", "failed");
      return;
    }
    const result = await actions.unbindOfficialAuth(profile.id);
    if (result) syncDraftFromSettings(normalizeSettings(result.settings));
  };
  const saveDraft = async () => {
    const candidateProfiles = isNew
      ? [...form.relayProfiles, draft]
      : form.relayProfiles.map((item) => (item.id === profile.id ? draft : item));
    const routeValidation = relayModelRouteValidationMessage(draft, candidateProfiles);
    if (routeValidation) {
      actions.showNotice("单模型路由", routeValidation, "failed");
      return;
    }
    const firstActiveRoute = isActive && !hasCompleteModelRoutes(profile) && hasCompleteModelRoutes(draft);
    if (firstActiveRoute && !window.confirm("首次启用单模型路由需要启动本地代理。保存后将重启 ChatGPT，是否继续？")) return;
    const next = isNew ? addRelayProfile(form, draft) : updateRelayProfile(form, profile.id, draft);
    const settingsResult = await actions.saveSettingsValue(next, true);
    if (!settingsResult) return;
    if (!isSuccessStatus(settingsResult.status)) {
      actions.showNotice("供应商保存", settingsResult.message, settingsResult.status);
      return;
    }
    const savedSettings = normalizeSettings(settingsResult.settings);
    onFormChange(savedSettings);
    syncDraftFromSettings(savedSettings);
    onSaved?.();
    if (firstActiveRoute) {
      await actions.restart();
      return;
    }
    if (isActive) {
      actions.showNotice("供应商保存", "保存成功，当前中转会读取新的服务器参数；如需重写模式文件，请退出 ChatGPT 后点击“重新应用”。", "ok");
    } else {
      actions.showNotice("供应商保存", "保存成功，已更新这个供应商自己的 config/auth 快照。", "ok");
    }
  };
  const switchDraft = () => {
    if (isNew) return;
    const next = syncLegacyRelayFields({
      ...form,
      relayProfiles: form.relayProfiles.map((item) => (item.id === profile.id ? draft : item)),
      activeRelayId: profile.id,
    });
    void actions.switchRelayProfile(next);
  };
  const saveAndApplyImageSettings = async () => {
    if (isNew) {
      actions.showNotice("图片生成路由", "请先保存新供应商，再应用图片路由。", "failed");
      return;
    }
    const next = updateRelayProfile(form, profile.id, draft);
    setImageSettingsApplying(true);
    try {
      if (isActive) {
        const applied = await actions.switchRelayProfile(next);
        if (applied) {
          actions.showNotice("图片生成路由", "图片路由已保存并应用；重启 ChatGPT 后 Imagegen CLI fallback 会自动使用独立图片中转。", "ok");
        }
        return;
      }
      const result = await actions.saveSettingsValue(next, true);
      if (!result) return;
      const savedSettings = normalizeSettings(result.settings);
      onFormChange(savedSettings);
      syncDraftFromSettings(savedSettings);
      actions.showNotice("图片生成路由", "图片路由已保存；切换到此供应商时生效。", result.status);
    } finally {
      setImageSettingsApplying(false);
    }
  };
  return (
    <div className="relay-detail-page">
      <Toolbar>
        <Button onClick={onBack} variant="secondary">
          <ArrowLeft className="h-4 w-4" />
          返回列表
        </Button>
        <Button onClick={() => void saveDraft()}>
          <Save className="h-4 w-4" />
          保存
        </Button>
      </Toolbar>
      <RelayProfileEditor
        profile={draft}
        form={form}
        isNew={isNew}
        onFormPatch={(patch) => onFormChange({ ...form, ...patch })}
        onProfileChange={setDraft}
        onApplyImageSettings={() => void saveAndApplyImageSettings()}
        imageSettingsApplying={imageSettingsApplying}
        onSwitch={switchDraft}
        actions={actions}
      />
      <OfficialAuthBindingPanel
        profile={draft}
        isNew={isNew}
        isActive={isActive}
        realtimeStatus={isActive ? relay : null}
        onBind={bindCurrentOfficialAuth}
        onActivate={() => void actions.activateOfficialAuth(profile.id)}
        onClearCurrent={() => void actions.clearCurrentOfficialAuth()}
        onUnbind={unbindCurrentOfficialAuth}
        onRefresh={() => {
          void actions.refreshRelay();
          void actions.refreshSettings();
          void actions.refreshRelayFiles();
        }}
      />
      <RelayFileEditors
        profile={draft}
        isActive={isActive}
        onImportCurrent={() => void actions.importCurrentRelayFiles(profile.id).then((result) => {
          if (result) syncDraftFromSettings(normalizeSettings(result.settings));
        })}
        onProfileChange={setDraft}
      />
    </div>
  );
}

function ContextScreen({
  form,
  liveEntries,
  relayFiles,
  onFormChange,
  actions,
}: {
  form: BackendSettings;
  liveEntries: CodexContextEntries | null;
  relayFiles: RelayFilesResult | null;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  const normalized = normalizeSettings(form);
  return (
    <Panel fill>
      <CardHead title="Codex 工具与插件" detail="统一管理 Codex MCP、Skills、Plugins；供应商切换时会合并到 config.toml" />
      <CardContent>
        <RelayContextManager
          form={normalized}
          liveEntries={liveEntries}
          relayFiles={relayFiles}
          onFormChange={onFormChange}
          actions={actions}
        />
      </CardContent>
    </Panel>
  );
}

function RelayContextManager({
  form,
  liveEntries,
  relayFiles,
  onFormChange,
  actions,
}: {
  form: BackendSettings;
  liveEntries: CodexContextEntries | null;
  relayFiles: RelayFilesResult | null;
  onFormChange: (value: BackendSettings) => void;
  actions: Actions;
}) {
  const entries = contextEntriesWithLiveEntries(form, liveEntries);
  const [activeKind, setActiveKind] = useState<ContextKind>("mcp");
  const [editor, setEditor] = useState<{ kind: ContextKind; entry?: CodexContextEntry } | null>(null);
  const visibleEntries = contextEntriesByKind(entries, activeKind);
  const label = contextKindLabel(activeKind);

  const saveEntry = async (kind: ContextKind, id: string, tomlBody: string) => {
    const next = await actions.upsertContextEntry(form, kind, id, tomlBody);
    if (!next) return;
    onFormChange(next);
    setEditor(null);
  };

  const toggleEntry = async (entry: CodexContextEntry) => {
    const next = await actions.upsertContextEntry(form, entry.kind, entry.id, setContextEntryEnabled(entry.tomlBody, !entry.enabled));
    if (!next) return;
    onFormChange(next);
    await actions.syncLiveContextEntries(next, true);
  };

  const deleteEntry = async (entry: CodexContextEntry) => {
    if (!actions.confirm(`删除 ${contextKindLabel(entry.kind)}「${entry.id}」？`)) return;
    const next = await actions.deleteContextEntry(form, entry.kind, entry.id);
    if (next) onFormChange(next);
  };

  return (
    <div className="relay-context-panel">
      <div className="relay-context-head">
        <div>
          <strong>全局 context 配置</strong>
          <span>{relayFiles?.configPath ?? "读取当前 Codex config.toml 后可同步 live 状态。"}</span>
        </div>
        <Toolbar>
          <Button onClick={() => void actions.refreshLiveContextEntries()} size="sm" variant="outline">
            <RefreshCw className="h-4 w-4" />
            读取 live
          </Button>
          <Button onClick={() => void actions.syncLiveContextEntries(form)} size="sm" variant="secondary">
            <Save className="h-4 w-4" />
            同步到 live
          </Button>
          <Button onClick={() => setEditor({ kind: activeKind })} size="sm">
            <Plus className="h-4 w-4" />
            新增{label}
          </Button>
        </Toolbar>
      </div>
      <div className="segmented">
        {contextKindOptions.map((option) => (
          <button
            className={activeKind === option.kind ? "active" : ""}
            key={option.kind}
            onClick={() => setActiveKind(option.kind)}
            type="button"
          >
            <span>{option.label}</span>
            <small>{contextEntriesByKind(entries, option.kind).length}</small>
          </button>
        ))}
      </div>
      <div className="relay-context-summary">
        当前共有 {visibleEntries.length} 个{label}；禁用会写入 `enabled = false`，删除会同时从各供应商选择中移除。
      </div>
      <div className="relay-context-list">
        {visibleEntries.length ? (
          visibleEntries.map((entry) => (
            <div className="relay-context-row" key={`${entry.kind}-${entry.id}`}>
              <div>
                <strong>{entry.title || entry.id}</strong>
                <span>{entry.summary || "无摘要"}</span>
              </div>
              <div className="relay-context-actions">
                <button
                  aria-checked={entry.enabled}
                  className={`context-enabled-switch ${entry.enabled ? "active" : ""}`}
                  onClick={() => void toggleEntry(entry)}
                  role="switch"
                  title={entry.enabled ? "禁用" : "启用"}
                  type="button"
                >
                  <span className="context-switch-track" aria-hidden="true">
                    <span className="context-switch-thumb" />
                  </span>
                </button>
                <Button onClick={() => setEditor({ kind: entry.kind, entry })} size="icon" title="编辑" variant="ghost">
                  <Edit3 className="h-4 w-4" />
                </Button>
                <Button onClick={() => void deleteEntry(entry)} size="icon" title="删除" variant="ghost">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))
        ) : (
          <div className="empty">暂无{label}，可以新增或先从 live config 读取。</div>
        )}
      </div>
      <div className="relay-common-config">
        <Field label="通用配置（非 MCP/Skills/Plugins 表）">
          <Textarea
            className="relay-file-textarea compact"
            value={form.relayCommonConfigContents}
            onChange={(event) => onFormChange({ ...form, relayCommonConfigContents: event.currentTarget.value })}
            placeholder={'例如：\nmodel_reasoning_effort = "medium"'}
            spellCheck={false}
          />
        </Field>
        <Toolbar>
          <Button onClick={() => void actions.saveSettingsValue(form, false)} size="sm" variant="secondary">
            保存通用配置
          </Button>
          <Button
            disabled={!relayFiles?.configContents}
            onClick={async () => {
              const extracted = await actions.extractRelayCommonConfig(relayFiles?.configContents ?? "");
              if (!extracted) return;
              onFormChange({
                ...form,
                relayCommonConfigContents: extracted.commonConfigContents,
                relayContextConfigContents: joinTomlSectionsRootFirst([
                  form.relayContextConfigContents,
                  extracted.contextConfigContents,
                ]),
              });
            }}
            size="sm"
            variant="outline"
          >
            从当前 config 提取
          </Button>
        </Toolbar>
      </div>
      {editor ? (
        <ContextEntryEditor
          entry={editor.entry}
          kind={editor.kind}
          onCancel={() => setEditor(null)}
          onSave={(kind, id, tomlBody) => void saveEntry(kind, id, tomlBody)}
        />
      ) : null}
    </div>
  );
}

function ContextEntryEditor({
  kind,
  entry,
  onCancel,
  onSave,
}: {
  kind: ContextKind;
  entry?: CodexContextEntry;
  onCancel: () => void;
  onSave: (kind: ContextKind, id: string, tomlBody: string) => void;
}) {
  const [draftKind, setDraftKind] = useState<ContextKind>(entry?.kind ?? kind);
  const [id, setId] = useState(entry?.id ?? "");
  const [tomlBody, setTomlBody] = useState(entry?.tomlBody ?? "");
  const canSave = id.trim().length > 0;

  return (
    <div className="context-editor">
      <div className="context-editor-fields">
        <Field label="类型">
          <select
            className="field-select"
            disabled={!!entry}
            value={draftKind}
            onChange={(event) => setDraftKind(event.currentTarget.value as ContextKind)}
          >
            {contextKindOptions.map((option) => (
              <option key={option.kind} value={option.kind}>{option.label}</option>
            ))}
          </select>
        </Field>
        <Field label="ID">
          <Input
            disabled={!!entry}
            value={id}
            onChange={(event) => setId(event.currentTarget.value.trim())}
            placeholder="例如 context7"
          />
        </Field>
      </div>
      <Field label="TOML 配置体">
        <Textarea
          className="context-editor-textarea"
          value={tomlBody}
          onChange={(event) => setTomlBody(event.currentTarget.value)}
          placeholder={'只填写表头下面的内容，例如：\ncommand = "npx"\nargs = ["-y", "@upstash/context7-mcp"]'}
          spellCheck={false}
        />
      </Field>
      <Toolbar>
        <Button disabled={!canSave} onClick={() => onSave(draftKind, id.trim(), tomlBody)} size="sm">
          <Save className="h-4 w-4" />
          保存
        </Button>
        <Button onClick={onCancel} size="sm" variant="secondary">取消</Button>
      </Toolbar>
    </div>
  );
}

function RelayContextSelectionEditor({
  profile,
  form,
  onChange,
  onUseCommonConfigChange,
}: {
  profile: RelayProfile;
  form: BackendSettings;
  onChange: (selection: RelayContextSelection) => void;
  onUseCommonConfigChange: (enabled: boolean) => void;
}) {
  const entries = contextEntriesFromSettings(form);
  const hasEntries = contextKindOptions.some((option) => contextEntriesByKind(entries, option.kind).length > 0);
  return (
    <div className="relay-context-selection">
      <label className="switch-row compact-switch">
        <input
          checked={profile.useCommonConfig}
          onChange={(event) => onUseCommonConfigChange(event.currentTarget.checked)}
          type="checkbox"
        />
        <span>
          <strong>合并通用配置和工具插件</strong>
          <small>关闭后此供应商只写入自己的 config.toml 快照。</small>
        </span>
      </label>
      {profile.useCommonConfig && hasEntries ? (
        <div className="context-selection-grid">
          {contextKindOptions.map((option) => {
            const items = contextEntriesByKind(entries, option.kind);
            if (!items.length) return null;
            return (
              <div className="context-selection-column" key={option.kind}>
                <strong>{option.label}</strong>
                {items.map((entry) => {
                  const checked = contextSelectionIds(profile.contextSelection, option.kind).includes(entry.id);
                  return (
                    <label className="context-selection-item" key={entry.id}>
                      <input
                        checked={checked}
                        onChange={(event) => onChange(setContextSelectionId(profile.contextSelection, option.kind, entry.id, event.currentTarget.checked))}
                        type="checkbox"
                      />
                      <span>{entry.id}</span>
                    </label>
                  );
                })}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

const contextKindOptions: Array<{ kind: ContextKind; label: string; tableName: string }> = [
  { kind: "mcp", label: "MCP", tableName: "mcp_servers" },
  { kind: "skill", label: "Skill", tableName: "skills" },
  { kind: "plugin", label: "Plugin", tableName: "plugins" },
];

function contextKindLabel(kind: ContextKind) {
  return contextKindOptions.find((option) => option.kind === kind)?.label ?? "扩展项";
}

function contextEntriesFromSettings(settings: BackendSettings): CodexContextEntries {
  const config = normalizeDuplicateTomlTables(settings.relayContextConfigContents || "");
  return {
    mcpServers: parseContextEntries(config, "mcp", "mcp_servers"),
    skills: parseContextEntries(config, "skill", "skills"),
    plugins: parseContextEntries(config, "plugin", "plugins"),
  };
}

function contextEntriesWithLiveEntries(settings: BackendSettings, liveEntries: Partial<CodexContextEntries> | null): CodexContextEntries {
  const commonEntries = contextEntriesFromSettings(settings);
  if (!liveEntries) return commonEntries;
  const normalizedLiveEntries = normalizeContextEntries(liveEntries);
  const liveByKind: Record<ContextKind, Map<string, CodexContextEntry>> = {
    mcp: new Map(normalizedLiveEntries.mcpServers.map((entry) => [entry.id, entry])),
    skill: new Map(normalizedLiveEntries.skills.map((entry) => [entry.id, entry])),
    plugin: new Map(normalizedLiveEntries.plugins.map((entry) => [entry.id, entry])),
  };
  return {
    mcpServers: mergeLiveContextEntries(commonEntries.mcpServers, liveByKind.mcp),
    skills: mergeLiveContextEntries(commonEntries.skills, liveByKind.skill),
    plugins: mergeLiveContextEntries(commonEntries.plugins, liveByKind.plugin),
  };
}

function mergeLiveContextEntries(entries: CodexContextEntry[], liveEntries: Map<string, CodexContextEntry>): CodexContextEntry[] {
  const uniqueEntries = dedupeContextEntryList(entries);
  const merged = uniqueEntries.map((entry) => {
    const live = liveEntries.get(entry.id);
    return live ? { ...entry, enabled: live.enabled } : entry;
  });
  const knownIds = new Set(uniqueEntries.map((entry) => entry.id));
  for (const liveEntry of liveEntries.values()) {
    if (!knownIds.has(liveEntry.id)) merged.push(liveEntry);
  }
  return merged;
}

function contextEntriesByKind(entries: Partial<CodexContextEntries>, kind: ContextKind): CodexContextEntry[] {
  if (kind === "mcp") return dedupeContextEntryList(entries.mcpServers);
  if (kind === "skill") return dedupeContextEntryList(entries.skills);
  return dedupeContextEntryList(entries.plugins);
}

function parseContextEntries(config: string, kind: ContextKind, tableName: string): CodexContextEntry[] {
  const entries = new Map<string, CodexContextEntry>();
  let currentId = "";
  let body: string[] = [];
  const flush = () => {
    if (!currentId) return;
    const tomlBody = normalizeConfigText(body.join("\n"));
    entries.set(currentId, {
      id: currentId,
      kind,
      title: currentId,
      summary: contextEntrySummary(tomlBody),
      tomlBody,
      enabled: contextEntryEnabled(tomlBody),
    });
  };
  for (const line of config.split(/\r?\n/)) {
    const path = tomlTablePathFromLine(line);
    if (path?.[0] === tableName && path.length >= 2) {
      if (currentId && currentId === path[1] && path.length > 2) {
        body.push(`[${path.slice(2).map(tomlKey).join(".")}]`);
        continue;
      }
      flush();
      currentId = path[1];
      body = [];
      continue;
    }
    if (currentId && /^\s*\[[^\]]+\]\s*$/.test(line)) {
      flush();
      currentId = "";
      body = [];
      continue;
    }
    if (currentId) body.push(line);
  }
  flush();
  return Array.from(entries.values()).sort((a, b) => a.id.localeCompare(b.id));
}

function tomlTablePathFromLine(line: string): string[] | null {
  const match = /^\s*\[([^\]]+)\]\s*$/.exec(line);
  if (!match) return null;
  return parseTomlDottedPath(match[1].trim());
}

function parseTomlDottedPath(path: string): string[] | null {
  const parts: string[] = [];
  let current = "";
  let quote: '"' | "'" | null = null;
  let escaping = false;
  for (const char of path) {
    if (quote) {
      if (quote === '"' && escaping) {
        current += char;
        escaping = false;
      } else if (quote === '"' && char === "\\") {
        escaping = true;
      } else if (char === quote) {
        quote = null;
      } else {
        current += char;
      }
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === ".") {
      if (!current.trim()) return null;
      parts.push(current.trim());
      current = "";
      continue;
    }
    current += char;
  }
  if (quote || escaping || !current.trim()) return null;
  parts.push(current.trim());
  return parts;
}

function contextEntrySummary(tomlBody: string) {
  return tomlBody
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find((line) => line && !line.startsWith("#") && !/^enabled\s*=/.test(line))
    ?.slice(0, 96) ?? "";
}

function contextEntryEnabled(tomlBody: string) {
  return !tomlBody.split(/\r?\n/).some((line) => /^\s*enabled\s*=\s*false\s*(#.*)?$/i.test(line));
}

function setContextEntryEnabled(tomlBody: string, enabled: boolean) {
  const lines = tomlBody.trimEnd().split(/\r?\n/);
  const nextValue = `enabled = ${enabled ? "true" : "false"}`;
  let replaced = false;
  const next = lines.map((line) => {
    if (/^\s*enabled\s*=/.test(line)) {
      replaced = true;
      return nextValue;
    }
    return line;
  });
  if (!replaced) next.unshift(nextValue);
  return normalizeConfigText(next.join("\n"));
}

function dedupeContextEntryList(entries: unknown): CodexContextEntry[] {
  const byId = new Map<string, CodexContextEntry>();
  if (!Array.isArray(entries)) return [];
  for (const entry of entries) {
    if (!entry || typeof entry !== "object") continue;
    const candidate = entry as Partial<CodexContextEntry>;
    const id = String(candidate.id ?? "").trim();
    if (!id) continue;
    byId.set(id, {
      id,
      kind: candidate.kind === "skill" || candidate.kind === "plugin" ? candidate.kind : "mcp",
      title: String(candidate.title ?? id),
      summary: String(candidate.summary ?? ""),
      tomlBody: String(candidate.tomlBody ?? ""),
      enabled: candidate.enabled !== false,
    });
  }
  return Array.from(byId.values());
}

function normalizeContextEntries(entries?: Partial<CodexContextEntries> | null): CodexContextEntries {
  return {
    mcpServers: dedupeContextEntryList(entries?.mcpServers).map((entry) => ({ ...entry, kind: "mcp" })),
    skills: dedupeContextEntryList(entries?.skills).map((entry) => ({ ...entry, kind: "skill" })),
    plugins: dedupeContextEntryList(entries?.plugins).map((entry) => ({ ...entry, kind: "plugin" })),
  };
}

function normalizeContextSelection(selection?: Partial<RelayContextSelection>): RelayContextSelection {
  return {
    mcpServers: uniqueStringList(selection?.mcpServers),
    skills: uniqueStringList(selection?.skills),
    plugins: uniqueStringList(selection?.plugins),
  };
}

function contextSelectionForAllEntries(settings: BackendSettings): RelayContextSelection {
  const entries = contextEntriesFromSettings(settings);
  return {
    mcpServers: entries.mcpServers.map((entry) => entry.id),
    skills: entries.skills.map((entry) => entry.id),
    plugins: entries.plugins.map((entry) => entry.id),
  };
}

function contextSelectionIds(selection: RelayContextSelection, kind: ContextKind): string[] {
  if (kind === "mcp") return selection.mcpServers;
  if (kind === "skill") return selection.skills;
  return selection.plugins;
}

function setContextSelectionId(selection: RelayContextSelection, kind: ContextKind, id: string, checked: boolean): RelayContextSelection {
  const next = {
    mcpServers: [...selection.mcpServers],
    skills: [...selection.skills],
    plugins: [...selection.plugins],
  };
  const list = contextSelectionIds(next, kind);
  const normalizedId = id.trim();
  const exists = list.includes(normalizedId);
  if (checked && normalizedId && !exists) list.push(normalizedId);
  if (!checked && exists) list.splice(list.indexOf(normalizedId), 1);
  return next;
}

function uniqueStringList(values: unknown): string[] {
  if (!Array.isArray(values)) return [];
  return Array.from(new Set(values.map(String).map((value) => value.trim()).filter(Boolean)));
}

function normalizeConfigText(config: string) {
  const trimmed = config.replace(/\r\n/g, "\n").trim();
  return trimmed ? `${trimmed}\n` : "";
}

function joinTomlSectionsRootFirst(sections: string[]): string {
  const rootParts: string[] = [];
  const tableParts: string[] = [];
  for (const section of sections) {
    const { root, tables } = splitTomlRootAndTables(section);
    if (root.trim()) rootParts.push(root.trim());
    if (tables.trim()) tableParts.push(tables.trim());
  }
  return normalizeDuplicateTomlTables(joinTomlSections([...dedupeTomlRootLines(rootParts), ...tableParts]));
}

function joinTomlSections(sections: string[]): string {
  return normalizeConfigText(sections.map((section) => section.trim()).filter(Boolean).join("\n\n"));
}

function normalizeDuplicateTomlTables(contents: string): string {
  const seenHeaders = new Set<string>();
  const kept: string[] = [];
  let skipping = false;
  for (const line of contents.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (/^\[[^\]]+\]$/.test(trimmed)) {
      skipping = seenHeaders.has(trimmed);
      seenHeaders.add(trimmed);
      if (skipping) continue;
    }
    if (!skipping) kept.push(line);
  }
  return normalizeConfigText(kept.join("\n"));
}

function dedupeTomlRootLines(rootParts: string[]): string[] {
  const rootSeen = new Set<string>();
  const kept: string[] = [];
  const lines = rootParts.join("\n").split(/\r?\n/);
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index];
    const key = tomlRootKeyFromLine(line.trim());
    if (key) {
      if (rootSeen.has(key)) continue;
      rootSeen.add(key);
    }
    kept.push(line);
  }
  const normalized = kept.reverse().join("\n").trim();
  return normalized ? [normalized] : [];
}

function splitTomlRootAndTables(section: string): { root: string; tables: string } {
  const lines = section.trim().split(/\r?\n/);
  const firstTable = lines.findIndex((line) => /^\s*\[[^\]]+\]\s*$/.test(line));
  if (firstTable < 0) return { root: lines.join("\n"), tables: "" };
  return { root: lines.slice(0, firstTable).join("\n"), tables: lines.slice(firstTable).join("\n") };
}

function tomlRootKeyFromLine(line: string): string | null {
  if (!line || line.startsWith("#")) return null;
  const index = line.indexOf("=");
  if (index < 0) return null;
  return line.slice(0, index).trim() || null;
}

function tomlKey(key: string): string {
  return /^[A-Za-z0-9_-]+$/.test(key) ? key : `"${tomlString(key)}"`;
}

function tomlFeatureBoolean(contents: string, key: string): boolean | null {
  let inFeatures = false;
  for (const line of contents.split(/\r?\n/)) {
    const tablePath = tomlTablePathFromLine(line);
    if (tablePath) {
      inFeatures = tablePath.length === 1 && tablePath[0] === "features";
      continue;
    }
    if (!inFeatures) continue;
    const match = /^\s*([A-Za-z0-9_-]+)\s*=\s*(true|false)\s*(?:#.*)?$/i.exec(line);
    if (match?.[1] === key) return match[2].toLowerCase() === "true";
  }
  return null;
}

function setTomlFeatureBoolean(contents: string, key: string, enabled: boolean): string {
  const lines = contents.replace(/\r\n/g, "\n").trimEnd().split("\n");
  const nextValue = `${tomlKey(key)} = ${enabled ? "true" : "false"}`;
  let inFeatures = false;
  let featuresEnd = -1;
  let replaced = false;
  const kept: string[] = [];

  for (const line of lines) {
    const tablePath = tomlTablePathFromLine(line);
    if (tablePath) {
      if (inFeatures && featuresEnd < 0) featuresEnd = kept.length;
      inFeatures = tablePath.length === 1 && tablePath[0] === "features";
      kept.push(line);
      continue;
    }
    if (inFeatures) {
      const assignment = /^\s*([A-Za-z0-9_-]+)\s*=/.exec(line);
      if (assignment?.[1] === key) {
        if (!replaced) {
          kept.push(nextValue);
          replaced = true;
        }
        continue;
      }
    }
    kept.push(line);
  }

  if (!replaced) {
    if (inFeatures) featuresEnd = kept.length;
    if (featuresEnd >= 0) {
      kept.splice(featuresEnd, 0, nextValue);
    } else {
      if (kept.some((line) => line.trim())) kept.push("");
      kept.push("[features]", nextValue);
    }
  }
  return normalizeConfigText(kept.join("\n"));
}

function AggregateRelayEditor({
  aggregate,
  form,
  isNew,
  profile,
  onAggregateChange,
}: {
  aggregate: AggregateRelayProfile;
  form: BackendSettings;
  isNew: boolean;
  profile: RelayProfile;
  onAggregateChange: (value: AggregateRelayProfile) => void;
}) {
  const candidates = form.relayProfiles.filter((item) => item.id !== profile.id && item.relayMode === "pureApi" && item.baseUrl.trim() && item.apiKey.trim());
  const updateMember = (index: number, patch: Partial<AggregateRelayMember>) => {
    onAggregateChange({
      ...aggregate,
      members: aggregate.members.map((member, memberIndex) => memberIndex === index ? { ...member, ...patch } : member),
    });
  };
  const addMember = () => {
    const firstCandidate = candidates.find((candidate) => !aggregate.members.some((member) => member.relayId === candidate.id));
    if (!firstCandidate) return;
    onAggregateChange({
      ...aggregate,
      members: [...aggregate.members, { relayId: firstCandidate.id, weight: 1 }],
    });
  };
  return (
    <div className="aggregate-editor">
      <div className="aggregate-editor-head">
        <div>
          <strong>聚合供应商</strong>
          <span>{isNew ? "保存后会启用成员配置；成员仅支持中转 API 供应商。" : "按策略从成员 API 供应商中选择实际上游。"}</span>
        </div>
        <select
          className="field-select"
          value={aggregate.strategy}
          onChange={(event) => onAggregateChange({ ...aggregate, strategy: event.currentTarget.value as AggregateRelayStrategy })}
        >
          <option value="failover">失败切换</option>
          <option value="conversationRoundRobin">按会话轮询</option>
          <option value="requestRoundRobin">按请求轮询</option>
          <option value="weightedRoundRobin">按权重轮询</option>
        </select>
      </div>
      {aggregate.members.length ? (
        <div className="aggregate-member-list">
          {aggregate.members.map((member, index) => (
            <div className="aggregate-member-row" key={`${member.relayId}-${index}`}>
              <select
                className="field-select"
                value={member.relayId}
                onChange={(event) => updateMember(index, { relayId: event.currentTarget.value })}
              >
                <option value="">选择成员供应商</option>
                {candidates.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>{candidate.name || candidate.id}</option>
                ))}
              </select>
              <Input
                inputMode="numeric"
                min={1}
                value={member.weight}
                onChange={(event) => updateMember(index, { weight: Math.max(1, Number(event.currentTarget.value) || 1) })}
              />
              <Button
                onClick={() => onAggregateChange({ ...aggregate, members: aggregate.members.filter((_, memberIndex) => memberIndex !== index) })}
                size="icon"
                type="button"
                variant="ghost"
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <div className="hint-line limited">
          <Info className="h-4 w-4" />
          <span>{candidates.length ? "还没有添加成员。" : "还没有可作为成员的中转 API 供应商；请先添加至少一个 Base URL / Key 完整的中转供应商。"}</span>
        </div>
      )}
      <Button disabled={!candidates.length} onClick={addMember} size="sm" type="button" variant="secondary">
        <Plus className="h-4 w-4" />
        添加成员
      </Button>
    </div>
  );
}

function RelayProfileEditor({
  profile,
  form,
  isNew = false,
  onFormPatch,
  onProfileChange,
  onApplyImageSettings,
  imageSettingsApplying = false,
  onSwitch,
  actions,
}: {
  profile: RelayProfile;
  form: BackendSettings;
  isNew?: boolean;
  onFormPatch: (patch: Partial<BackendSettings>) => void;
  onProfileChange: (value: RelayProfile) => void;
  onApplyImageSettings: () => void;
  imageSettingsApplying?: boolean;
  onSwitch: () => void;
  actions: Actions;
}) {
  const showApiFields = profile.relayMode !== "official" && profile.relayMode !== "aggregate";
  const aggregate = aggregateRelayProfileFor(form, profile);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [doctorRunning, setDoctorRunning] = useState(false);
  const [doctorResult, setDoctorResult] = useState<ProviderDoctorResult | null>(null);
  const [vlmImageDataUrl, setVlmImageDataUrl] = useState<string | null>(null);
  const [vlmTestResult, setVlmTestResult] = useState<VLMTestResult | null>(null);
  const [vlmTestRunning, setVlmTestRunning] = useState(false);
  const [vlmTestError, setVlmTestError] = useState("");
  const vlmFileInput = useRef<HTMLInputElement>(null);
  const imageHandling = parseModelVlmJSON(profile.modelVlm);
  const profileModels = relayProfileModelNames(profile);
  const profileGoals = tomlFeatureBoolean(profile.configContents, "goals");
  const inheritedGoals = profile.useCommonConfig ? tomlFeatureBoolean(form.relayCommonConfigContents, "goals") : null;
  const goalsEnabled = profileGoals ?? inheritedGoals ?? false;
  const modelRoutes = normalizeRelayModelRoutes(profile.modelRoutes);
  const modelRouteTargets = form.relayProfiles.filter(
    (candidate) => candidate.id !== profile.id && candidate.relayMode !== "aggregate" && candidate.protocol === "responses",
  );
  const hasVlmMode = Object.values(imageHandling).includes("vlm");
  const sub2APIBaseURL = profile.upstreamBaseUrl.trim() || profile.baseUrl.trim();
  const canFetchSub2APIRate = profile.sub2apiEnabled && Boolean(sub2APIBaseURL && profile.apiKey.trim());
  const updateDraft = (patch: Partial<RelayProfile>) => {
    const shouldRegenerateFiles = [
      "model",
      "baseUrl",
      "apiKey",
      "protocol",
      "relayMode",
      "officialMixApiKey",
      "imageGenerationEnabled",
      "imageGenerationUseSeparateApi",
      "imageGenerationBaseUrl",
      "imageGenerationApiKey",
      "contextWindow",
      "autoCompactLimit",
      "modelAutoCompact",
      "modelRoutes",
      "proxyEnabled",
      "proxyUrl",
    ].some((key) => key in patch);
    const canonicalPatch = "baseUrl" in patch ? { ...patch, upstreamBaseUrl: patch.baseUrl ?? "" } : patch;
    const updated = { ...profile, ...canonicalPatch };
    onProfileChange(shouldRegenerateFiles ? withGeneratedRelayFiles(updated) : updated);
  };
  const fetchSub2APIRate = async () => {
    const result = await actions.fetchSub2APIBilling(profile);
    if (!result) return;
    updateDraft({
      sub2apiEnabled: true,
      sub2apiMultiplier: formatMultiplierValue(result.effectiveRateMultiplier),
    });
  };
  const runProviderDoctor = async () => {
    if (doctorRunning) return;
    setDoctorRunning(true);
    try {
      setDoctorResult(await actions.diagnoseRelayProfile(profile));
    } finally {
      setDoctorRunning(false);
    }
  };
  const runVLMTest = async (imageDataUrl = vlmImageDataUrl) => {
    if (!imageDataUrl || vlmTestRunning) return;
    setVlmTestRunning(true);
    setVlmTestError("");
    setVlmTestResult(null);
    try {
      const result = await actions.testVLMProfile(profile, imageDataUrl);
      if (result) setVlmTestResult(result);
    } finally {
      setVlmTestRunning(false);
    }
  };
  const chooseVLMTestImage = (file?: File) => {
    setVlmTestError("");
    setVlmTestResult(null);
    if (!file) return;
    if (!file.type.startsWith("image/")) {
      setVlmTestError("请选择图片文件。");
      return;
    }
    if (file.size > VLM_TEST_MAX_IMAGE_BYTES) {
      setVlmTestError("图片超过 10MB，请换一张较小的图片。");
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => setVlmTestError("读取图片失败，请重新选择。");
    reader.onload = () => {
      const value = typeof reader.result === "string" ? reader.result : "";
      if (!value) {
        setVlmTestError("读取图片失败，请重新选择。");
        return;
      }
      setVlmImageDataUrl(value);
      void runVLMTest(value);
    };
    reader.readAsDataURL(file);
  };
  return (
    <div className="relay-profile-editor">
      <div className="relay-editor-head">
        <div>
          <strong>{profile.name || "未命名供应商"}</strong>
          <span>{isNew ? "新建供应商需要先保存到列表" : profile.id === form.activeRelayId ? "当前正在使用" : "编辑后保存列表，再切换模式时会使用新配置"}</span>
        </div>
        <div className="relay-editor-actions">
          {showApiFields ? (
            <Button disabled={doctorRunning} onClick={() => void runProviderDoctor()} type="button" variant="secondary">
              <Stethoscope className="h-4 w-4" />
              {doctorRunning ? "诊断中" : "诊断供应商"}
            </Button>
          ) : null}
          {isNew ? null : (
            <Button
              onClick={onSwitch}
              variant={profile.id === form.activeRelayId ? "secondary" : "default"}
            >
              {profile.id === form.activeRelayId ? "重新应用" : "设为当前"}
            </Button>
          )}
        </div>
      </div>
      {doctorResult ? (
        <section className="provider-doctor" aria-label="供应商诊断结果">
          <div className="provider-doctor-head">
            <div>
              <strong>{doctorResult.summary}</strong>
              <span>测试模型：{doctorResult.model || "未指定"}</span>
            </div>
            <Button aria-label="关闭诊断结果" onClick={() => setDoctorResult(null)} size="icon" type="button" variant="ghost">
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
          <div className="provider-doctor-checks">
            {doctorResult.checks.map((check) => (
              <div className={`provider-doctor-check ${check.status}`} key={check.id}>
                <span>{check.status === "ok" ? "通过" : check.status === "warning" ? "确认" : "失败"}</span>
                <div><strong>{check.title}</strong><small>{check.detail}</small></div>
              </div>
            ))}
          </div>
          <p>{doctorResult.recommendation}</p>
        </section>
      ) : null}
      <div className="relay-fields">
        <Field className="relay-field-name" label="名称">
          <Input
            value={profile.name}
            onChange={(event) => updateDraft({ name: event.currentTarget.value })}
          />
        </Field>
        <Field className="relay-field-mode" label="接入模式">
          <select
            className="field-select"
            value={profile.relayMode}
            onChange={(event) => {
              const relayMode = event.currentTarget.value as RelayMode;
              updateDraft({
                relayMode,
                officialMixApiKey: relayMode === "mixedApi",
              });
            }}
          >
            <option value="official">官方登录</option>
            <option value="mixedApi">官方混合 API</option>
            <option value="pureApi">中转 API</option>
            <option value="aggregate">聚合轮转</option>
          </select>
        </Field>
        <Field className="relay-field-config-model" label="配置模型">
          <Input
            value={profile.model}
            onChange={(event) => updateDraft({ model: event.currentTarget.value })}
            placeholder="写入 config.toml 的 model，例如 gpt-5"
          />
        </Field>
        <label className="switch-row compact-switch relay-field-goals">
          <input
            checked={goalsEnabled}
            onChange={(event) => updateDraft({ configContents: setTomlFeatureBoolean(profile.configContents, "goals", event.currentTarget.checked) })}
            type="checkbox"
          />
          <span>
            <strong>Codex 目标</strong>
            <small>{profileGoals === null && inheritedGoals !== null ? "当前继承公共配置；修改后将保存为此供应商的独立设置。" : "仅为此供应商写入 features.goals。"}</small>
          </span>
        </label>
        {profile.relayMode === "official" ? (
          <label className="switch-row compact-switch relay-field-official-usage-alert">
            <input
              checked={profile.hideOfficialUsageAlert}
              onChange={(event) => updateDraft({ hideOfficialUsageAlert: event.currentTarget.checked })}
              type="checkbox"
            />
            <span>
              <strong>隐藏官方低额度提示</strong>
              <small>仅隐藏侧边栏提示卡；官方剩余额度仍可从账户菜单查看。</small>
            </span>
          </label>
        ) : null}
        <Field className="relay-field-model-insert" label="模型写入方式">
          <select
            className="field-select"
            value={profile.modelInsertMode || "patch"}
            onChange={(event) => updateDraft({ modelInsertMode: event.currentTarget.value })}
          >
            <option value="patch">补丁合并</option>
            <option value="replace">替换列表</option>
          </select>
        </Field>
        <Field className="relay-field-test-model" label="测试模型">
          <Input
            value={profile.testModel}
            onChange={(event) => updateDraft({ testModel: event.currentTarget.value })}
            placeholder={`留空使用默认：${form.relayTestModel || defaultSettings.relayTestModel}`}
          />
        </Field>
        <div className="relay-advanced-toggle">
          <Button
            aria-expanded={showAdvanced}
            onClick={() => setShowAdvanced((current) => !current)}
            size="sm"
            type="button"
            variant="secondary"
          >
            <Settings className="h-4 w-4" />
            更多选项
          </Button>
        </div>
        {showAdvanced ? (
          <div className="relay-advanced-fields">
            <Field label="上下文窗口">
              <Input
                inputMode="numeric"
                value={profile.contextWindow}
                onChange={(event) => updateDraft({ contextWindow: event.currentTarget.value.replace(/[^\d]/g, "") })}
                placeholder="留空不改写，例如 200000"
              />
            </Field>
            <Field label="自动压缩限制">
              <Input
                inputMode="numeric"
                value={profile.autoCompactLimit}
                onChange={(event) => updateDraft({ autoCompactLimit: event.currentTarget.value.replace(/[^\d]/g, "") })}
                placeholder="留空不改写，例如 160000"
              />
            </Field>
            <Field label="User-Agent">
              <Input
                value={profile.userAgent}
                onChange={(event) => updateDraft({ userAgent: event.currentTarget.value })}
                placeholder="留空使用默认值"
              />
            </Field>
          </div>
        ) : null}
        {profile.relayMode === "aggregate" ? (
          <AggregateRelayEditor
            aggregate={aggregate}
            form={form}
            isNew={isNew}
            profile={profile}
            onAggregateChange={(nextAggregate) => {
              const nextProfiles = form.aggregateRelayProfiles.filter((item) => item.id !== nextAggregate.id);
              onFormPatch({
                aggregateRelayProfiles: [...nextProfiles, nextAggregate],
                activeAggregateRelayId: profile.id,
              });
            }}
          />
        ) : null}
        {showApiFields ? (
          <>
            <Field className="relay-field-base-url" label="Base URL">
              <Input
                value={profile.baseUrl}
                onChange={(event) => updateDraft({ baseUrl: event.currentTarget.value })}
                placeholder="填写中转服务 Base URL"
              />
            </Field>
            <Field className="relay-field-key" label="Key">
              <Input
                type="password"
                value={profile.apiKey}
                onChange={(event) => updateDraft({ apiKey: event.currentTarget.value })}
                placeholder="输入中转服务的 API Key"
              />
            </Field>
            <Field className="relay-field-protocol" label="上游协议">
              <div className="protocol-options">
                <button
                  className={`protocol-option ${profile.protocol === "responses" ? "active" : ""}`}
                  onClick={() => updateDraft({ protocol: "responses" })}
                  type="button"
                >
                  Responses API
                </button>
                <button
                  className={`protocol-option ${profile.protocol === "chatCompletions" ? "active" : ""}`}
                  onClick={() => updateDraft({ protocol: "chatCompletions" })}
                  type="button"
                >
                  Chat Completions
                </button>
              </div>
            </Field>
            <Field className="relay-field-sub2api" label="Sub2API 倍率">
              <div className="sub2api-controls">
                <label className="inline-check">
                  <input
                    checked={profile.sub2apiEnabled}
                    onChange={(event) => {
                      const enabled = event.currentTarget.checked;
                      updateDraft({
                        sub2apiEnabled: enabled,
                        sub2apiMultiplier: enabled ? profile.sub2apiMultiplier : "",
                      });
                      if (enabled && sub2APIBaseURL && profile.apiKey.trim()) void fetchSub2APIRate();
                    }}
                    type="checkbox"
                  />
                  <span>读取并显示供应商倍率</span>
                </label>
                <Button
                  disabled={!canFetchSub2APIRate}
                  onClick={() => void fetchSub2APIRate()}
                  size="sm"
                  type="button"
                  variant="secondary"
                >
                  <Download className="h-4 w-4" />
                  获取倍率
                </Button>
              </div>
              <small className="field-hint">
                {profile.sub2apiEnabled
                  ? profile.sub2apiMultiplier.trim()
                    ? `当前缓存倍率：${profile.sub2apiMultiplier.trim()}x`
                    : "可从 /v1/sub2api/billing 读取上游倍率。"
                  : "非 Sub2API 供应商不会请求或显示倍率。"}
              </small>
            </Field>
          </>
        ) : null}
      </div>
      {showApiFields ? (
        <div className="relay-model-list-tools">
          <Field label="模型列表">
            <Textarea
              className="relay-model-list-textarea"
              value={profile.modelList}
              onChange={(event) => updateDraft({ modelList: event.currentTarget.value })}
              placeholder="每行一个模型，例如 gpt-5-mini；也兼容 deepseek-v4[1M]"
              spellCheck={false}
            />
          </Field>
          <Field label="模型窗口">
            <Textarea
              className="relay-model-list-textarea"
              value={profile.modelWindows}
              onChange={(event) => updateDraft({ modelWindows: event.currentTarget.value })}
              placeholder={'JSON，例如 {"deepseek-v4":"1M","qwen-plus":"200K"}'}
              spellCheck={false}
            />
          </Field>
          <Field label="每模型自动压缩 (%)">
            <Textarea
              className="relay-model-list-textarea"
              value={profile.modelAutoCompact}
              onChange={(event) => updateDraft({ modelAutoCompact: event.currentTarget.value })}
              placeholder={'JSON，例如 {"gpt-5.6":"90%","qwen-plus":"84.5"}'}
              spellCheck={false}
            />
          </Field>
          <Button
            onClick={async () => {
              const models = await actions.fetchRelayProfileModels(profile);
              if (models?.length) updateDraft({ modelList: models.join("\n") });
            }}
            size="sm"
            type="button"
            variant="secondary"
          >
            <Download className="h-4 w-4" />
            从上游获取
          </Button>
        </div>
      ) : null}
      {showApiFields ? (
        <section className="relay-model-routes" aria-label="单模型路由">
          <div className="relay-model-routes-head">
            <div>
              <strong>单模型路由</strong>
              <span>精确匹配模型名，并使用目标 Responses 供应商的 URL 与 Key。</span>
            </div>
            <Button
              disabled={!modelRouteTargets.length}
              onClick={() => updateDraft({ modelRoutes: [...modelRoutes, { model: "", targetRelayId: "", targetModel: "" }] })}
              size="sm"
              title={modelRouteTargets.length ? "添加模型路由" : "请先创建 Responses API 目标供应商"}
              type="button"
              variant="secondary"
            >
              <Plus className="h-4 w-4" />
              添加路由
            </Button>
          </div>
          {modelRoutes.length ? (
            <div className="relay-model-route-list">
              <div className="relay-model-route-row relay-model-route-labels">
                <span>匹配模型</span>
                <span>目标供应商</span>
                <span>目标模型（可选）</span>
              </div>
              {modelRoutes.map((route, index) => (
                <div className="relay-model-route-row" key={`model-route-${index}`}>
                  <Input
                    value={route.model}
                    onChange={(event) => updateDraft({
                      modelRoutes: modelRoutes.map((item, routeIndex) => routeIndex === index ? { ...item, model: event.currentTarget.value } : item),
                    })}
                    placeholder="例如 gpt-5.6-luna"
                  />
                  <select
                    className="field-select"
                    value={route.targetRelayId}
                    onChange={(event) => updateDraft({
                      modelRoutes: modelRoutes.map((item, routeIndex) => routeIndex === index ? { ...item, targetRelayId: event.currentTarget.value } : item),
                    })}
                  >
                    <option value="">选择 Responses 供应商</option>
                    {modelRouteTargets.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name || candidate.id}</option>)}
                  </select>
                  <Input
                    value={route.targetModel}
                    onChange={(event) => updateDraft({
                      modelRoutes: modelRoutes.map((item, routeIndex) => routeIndex === index ? { ...item, targetModel: event.currentTarget.value } : item),
                    })}
                    placeholder="留空保持原模型名"
                  />
                  <Button
                    aria-label="删除模型路由"
                    onClick={() => updateDraft({ modelRoutes: modelRoutes.filter((_, routeIndex) => routeIndex !== index) })}
                    size="icon"
                    title="删除模型路由"
                    type="button"
                    variant="ghost"
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              ))}
            </div>
          ) : <p className="field-hint">没有配置路由时，所有模型继续使用当前供应商。</p>}
        </section>
      ) : null}
      {showApiFields && profileModels.length ? (
        <section className="relay-vlm-section" aria-label="模型图片处理">
          <div className="image-relay-heading">
            <div>
              <strong>
                <Image className="h-4 w-4" />
                模型图片处理
              </strong>
              <span>每个模型独立选择原样发送、移除图片或先由 VLM 分析。</span>
            </div>
          </div>
          <div className="table">
            {profileModels.map((model) => (
              <div className="table-row" key={model}>
                <div>
                  <strong>{model}</strong>
                  <small>{imageHandling[model] === "vlm" ? "图片将转换为文字描述" : imageHandling[model] === "strip" ? "图片将替换为省略提示" : "保留原始图片块"}</small>
                </div>
                <select
                  aria-label={`${model} 图片处理方式`}
                  className="field-select"
                  value={imageHandling[model] ?? "send-as-is"}
                  onChange={(event) => updateDraft({ modelVlm: updateModelImageHandling(profile, model, event.currentTarget.value as ImageHandlingMode) })}
                >
                  <option value="send-as-is">原样发送</option>
                  <option value="strip">移除图片</option>
                  <option value="vlm">VLM 分析</option>
                </select>
              </div>
            ))}
          </div>
          {hasVlmMode ? (
            <div className="relay-fields">
              <Field label="VLM API Key">
                <Input type="password" value={profile.vlmApiKey} onChange={(event) => updateDraft({ vlmApiKey: event.currentTarget.value })} placeholder="sk-..." />
              </Field>
              <Field label="VLM Model">
                <Input value={profile.vlmModel} onChange={(event) => updateDraft({ vlmModel: event.currentTarget.value })} placeholder="qwen-vl-plus" />
              </Field>
              <Field label="VLM Base URL">
                <Input value={profile.vlmBaseUrl} onChange={(event) => updateDraft({ vlmBaseUrl: event.currentTarget.value })} placeholder="https://api.example.com/v1" />
              </Field>
              {!profile.vlmApiKey.trim() || !profile.vlmModel.trim() || !profile.vlmBaseUrl.trim() ? (
                <p className="field-hint warn">VLM 配置不完整时将保留原始图片，不会静默丢失图片内容。</p>
              ) : null}
              <div className="relay-vlm-test">
                <input
                  ref={vlmFileInput}
                  accept="image/*"
                  aria-label="选择 VLM 测试图片"
                  style={{ display: "none" }}
                  onChange={(event) => {
                    chooseVLMTestImage(event.currentTarget.files?.[0]);
                    event.currentTarget.value = "";
                  }}
                  type="file"
                />
                <Button
                  disabled={vlmTestRunning || !profile.vlmApiKey.trim() || !profile.vlmModel.trim() || !profile.vlmBaseUrl.trim()}
                  onClick={() => vlmFileInput.current?.click()}
                  size="sm"
                  type="button"
                  variant="secondary"
                >
                  <Image className="h-4 w-4" />
                  {vlmImageDataUrl ? "换图并测试" : "选择图片并测试 VLM"}
                </Button>
                {vlmImageDataUrl ? (
                  <>
                    <img className="vlm-test-preview" src={vlmImageDataUrl} alt="VLM 测试图片预览" />
                    <Button disabled={vlmTestRunning} onClick={() => void runVLMTest()} size="sm" type="button" variant="ghost">
                      {vlmTestRunning ? "正在测试…" : "重测"}
                    </Button>
                  </>
                ) : null}
                {vlmTestError ? <p className="field-hint warn" role="alert">{vlmTestError}</p> : null}
                {vlmTestResult ? (
                  <div className={`vlm-test-result ${isSuccessStatus(vlmTestResult.status) ? "is-success" : "is-error"}`} role="status">
                    <strong>{vlmTestResult.message}</strong>
                    {vlmTestResult.description ? <p>{vlmTestResult.description}</p> : null}
                    {vlmTestResult.httpCode ? <small>HTTP {vlmTestResult.httpCode} · {vlmTestResult.durationMs ?? 0} ms</small> : null}
                  </div>
                ) : null}
              </div>
            </div>
          ) : null}
        </section>
      ) : null}
      <RelayContextSelectionEditor profile={profile} form={form} onChange={(contextSelection) => updateDraft({ contextSelection })} onUseCommonConfigChange={(useCommonConfig) => updateDraft({ useCommonConfig })} />
      <ImageRelaySettings
        applying={imageSettingsApplying}
        isNew={isNew}
        onApply={onApplyImageSettings}
        onChange={updateDraft}
        profile={profile}
      />
      {showApiFields && profile.protocol === "chatCompletions" ? (
        <div className="hint-line relay-protocol-hint">
          <MessageCircle className="h-4 w-4" />
          <span>此上游会通过本地 127.0.0.1:57321 转成 Responses API，需要从 ChatGPT Codex 启动 ChatGPT。</span>
        </div>
      ) : null}
      <div className="hint-line relay-protocol-hint">
        <ShieldCheck className="h-4 w-4" />
        <span>{relayProfileModeHelp(profile)}</span>
      </div>
    </div>
  );
}

function ImageRelaySettings({
  profile,
  onChange,
  onApply,
  applying,
  isNew,
}: {
  profile: RelayProfile;
  onChange: (patch: Partial<RelayProfile>) => void;
  onApply: () => void;
  applying: boolean;
  isNew: boolean;
}) {
  const apiMode = profile.relayMode === "mixedApi" || profile.relayMode === "pureApi";
  const separateApiSupported = profile.protocol === "responses";
  const controlsDisabled = !apiMode;
  const separateFieldsVisible = profile.imageGenerationUseSeparateApi && separateApiSupported;
  const proxyStatus = !apiMode
    ? "仅混合 API 或中转 API 可配置"
    : profile.proxyEnabled && profile.proxyUrl.trim()
      ? "默认中转与图片 API 共用"
      : profile.proxyEnabled
        ? "代理 URL 为空，保持直连"
        : "默认中转与图片 API 均直连";

  return (
    <section className="image-relay-settings" aria-label="图片生成路由">
      <div className="image-relay-heading">
        <div>
          <strong>
            <Image className="h-4 w-4" />
            图片生成路由
          </strong>
          <span>{`当前供应商：${profile.name || profile.id}`}</span>
        </div>
        <span className={`image-chip ${profile.imageGenerationEnabled ? "enabled" : ""}`}>
          {relayImageModeLabel(profile)}
        </span>
      </div>
      <div className="relay-grid compact image-route-status">
        <Metric label="普通请求" value="当前默认中转" />
        <Metric label="HTTP 代理" value={proxyStatus} />
      </div>
      {!apiMode ? (
        <div className="hint-line image-route-hint">
          <Info className="h-4 w-4" />
          <span>官方模式和聚合轮转不在这里配置图片 API；请先切换为混合 API 或中转 API。</span>
        </div>
      ) : null}
      <label className="switch-row compact-switch">
        <input
          checked={profile.imageGenerationEnabled}
          disabled={controlsDisabled}
          onChange={(event) => onChange({ imageGenerationEnabled: event.currentTarget.checked })}
          type="checkbox"
        />
        <span>
          <strong>允许当前中转使用图片生成</strong>
          <small>关闭时会通过本地代理裁剪 image_generation 工具；已填写的独立图片 API 和 Key 会保留。</small>
        </span>
      </label>
      <label className="switch-row compact-switch">
        <input
          checked={profile.imageGenerationUseSeparateApi}
          disabled={controlsDisabled || !profile.imageGenerationEnabled || !separateApiSupported}
          onChange={(event) => onChange({ imageGenerationUseSeparateApi: event.currentTarget.checked })}
          type="checkbox"
        />
        <span>
          <strong>图片生成使用独立 API 和 Key</strong>
          <small>只有明确的图片生成请求使用独立图片 API；代码开发、普通对话和其他工具请求继续走当前默认中转。</small>
        </span>
      </label>
      {!separateApiSupported && apiMode ? (
        <div className="hint-line image-route-hint">
          <Info className="h-4 w-4" />
          <span>独立图片 API 仅支持 Responses API 上游；当前 Chat Completions 仍可让图片生成走默认中转。</span>
        </div>
      ) : null}
      {separateFieldsVisible ? (
        <div className="relay-fields image-fields">
          <Field label="图片 Base URL">
            <Input
              disabled={controlsDisabled || !profile.imageGenerationEnabled}
              value={profile.imageGenerationBaseUrl}
              onChange={(event) => onChange({ imageGenerationBaseUrl: event.currentTarget.value })}
              placeholder="填写支持图片生成的 Base URL"
            />
          </Field>
          <Field label="图片 Key">
            <Input
              disabled={controlsDisabled || !profile.imageGenerationEnabled}
              type="password"
              value={profile.imageGenerationApiKey}
              onChange={(event) => onChange({ imageGenerationApiKey: event.currentTarget.value })}
              placeholder="输入图片生成 API Key"
            />
          </Field>
        </div>
      ) : null}
      <div className="image-proxy-settings">
        <label className="switch-row compact-switch relay-proxy-switch">
          <input
            checked={profile.proxyEnabled}
            disabled={controlsDisabled}
            onChange={(event) => onChange({ proxyEnabled: event.currentTarget.checked })}
            type="checkbox"
          />
          <span>
            <strong>图片与默认中转共用 HTTP 代理</strong>
            <small>供应商测试、模型列表、默认中转请求和独立图片请求共用此代理；关闭或 URL 为空时保持直连。</small>
          </span>
        </label>
        <Field className="relay-proxy-url-field" label="代理 URL">
          <Input
            disabled={controlsDisabled || !profile.proxyEnabled}
            value={profile.proxyUrl}
            onChange={(event) => onChange({ proxyUrl: event.currentTarget.value })}
            placeholder="http://127.0.0.1:7890"
          />
        </Field>
      </div>
      <div className="hint-line image-route-hint">
        <Save className="h-4 w-4" />
        <span>独立图片 Key 只由本地图片代理读取；Imagegen fallback 仅把图片接口请求交给它，普通对话继续走默认中转。</span>
      </div>
      <Toolbar>
        <Button disabled={controlsDisabled || applying || isNew} onClick={onApply} type="button">
          <Save className="h-4 w-4" />
          {applying ? "正在应用图片路由" : "保存并应用图片路由"}
        </Button>
        {isNew ? <span className="image-apply-hint">请先保存新供应商。</span> : null}
      </Toolbar>
    </section>
  );
}

function OfficialAuthBindingPanel({
  profile,
  isNew,
  isActive,
  realtimeStatus,
  onBind,
  onActivate,
  onClearCurrent,
  onUnbind,
  onRefresh,
}: {
  profile: RelayProfile;
  isNew: boolean;
  isActive: boolean;
  realtimeStatus: RelayResult | null;
  onBind: () => void;
  onActivate: () => void;
  onClearCurrent: () => void;
  onUnbind: () => void;
  onRefresh: () => void;
}) {
  const bound = profile.officialAuthContents.trim().length > 0;
  const realtimeAvailable = isActive && realtimeStatus?.officialRealtimeAvailable === true;
  const realtimeMessage = !isActive
    ? "切换为当前供应商后检测官方语音状态。"
    : realtimeStatus?.officialRealtimeMessage || (bound ? "重启 ChatGPT Codex 后检测官方语音凭据。" : "绑定官方账号后才能使用语音聊天。");
  return (
    <div className="relay-profile-editor official-auth-panel">
      <div className="relay-editor-head">
        <div>
          <strong>官方账号绑定</strong>
          <span>{bound ? `已绑定：${officialBindingLabel(profile)}` : "未绑定；官方/混合模式会阻止切换。"}</span>
        </div>
        <UiBadge variant={bound ? "secondary" : "outline"}>{bound ? "已绑定" : "未绑定"}</UiBadge>
      </div>
      <div className="official-auth-meta">
        <Metric label="绑定账号" value={officialBindingLabel(profile)} />
        <Metric label="更新时间" value={formatOfficialAuthTime(profile.officialAuthUpdatedAt)} />
        <Metric label="官方语音" value={realtimeAvailable ? "可用" : isActive ? "不可用" : "待检测"} />
      </div>
      <div className="hint-line relay-protocol-hint">
        <ShieldCheck className="h-4 w-4" />
        <span>{realtimeMessage}</span>
      </div>
      <Toolbar>
        <Button disabled={isNew} onClick={onBind} variant="secondary">
          <KeyRound className="h-4 w-4" />
          绑定当前登录
        </Button>
        <Button disabled={isNew || !bound} onClick={onActivate} variant="secondary">
          <KeyRound className="h-4 w-4" />
          绑定账号
        </Button>
        <Button onClick={onRefresh} variant="outline">
          <RefreshCw className="h-4 w-4" />
          刷新
        </Button>
        <Button onClick={onClearCurrent} variant="outline">
          <PowerOff className="h-4 w-4" />
          清除当前官方登录
        </Button>
        <Button disabled={isNew || !bound} onClick={onUnbind} variant="ghost">
          <Trash2 className="h-4 w-4" />
          清除已保存绑定
        </Button>
      </Toolbar>
    </div>
  );
}

function RelayFileEditors({
  profile,
  isActive,
  onImportCurrent,
  onProfileChange,
}: {
  profile: RelayProfile;
  isActive: boolean;
  onImportCurrent: () => void;
  onProfileChange: (value: RelayProfile) => void;
}) {
  return (
    <div className="relay-file-grid">
      <div className="relay-file-panel">
        <div className="relay-file-head">
          <div>
            <strong>config.toml</strong>
            <span>{isActive ? "当前使用中：保存后会立刻写回 ~/.codex/config.toml" : "切换到此供应商时会用这份快照覆盖 ~/.codex/config.toml"}</span>
          </div>
          <Toolbar>
            <Button onClick={onImportCurrent} size="sm" variant="outline">
              <Download className="h-4 w-4" />
              从当前环境导入
            </Button>
          </Toolbar>
        </div>
        <Textarea
          className="relay-file-textarea"
          value={profile.configContents}
          onChange={(event) => onProfileChange({ ...profile, configContents: event.currentTarget.value })}
          spellCheck={false}
        />
      </div>
      <div className="relay-file-panel">
        <div className="relay-file-head">
          <div>
            <strong>auth.json</strong>
            <span>
              {profile.relayMode === "pureApi"
                ? profile.authContents.trim()
                  ? "这份供应商 auth 快照会在切换时一并恢复；为空时兼容保留当前登录。"
                  : "当前供应商还没有保存 auth 快照；切换时会兼容保留当前登录。"
                : "这份供应商 auth 快照会在切换时写回 ~/.codex/auth.json。"}
            </span>
          </div>
        </div>
        <Textarea
          className="relay-file-textarea"
          value={profile.authContents}
          onChange={(event) => onProfileChange({ ...profile, authContents: event.currentTarget.value })}
          spellCheck={false}
        />
      </div>
    </div>
  );
}

function ModeSelector({ launchMode, actions }: { launchMode: LaunchMode; actions: Actions }) {
  return (
    <div className="mode-grid">
      <button
        className={`mode-option ${launchMode === "relay" ? "active" : ""}`}
        onClick={() => void actions.setLaunchMode("relay")}
        type="button"
      >
        <strong>兼容增强</strong>
        <span>纯中转/聚合的兜底路径会关闭站点/插件解锁；混合 API 会保留官方登录能力并继续启用站点功能。</span>
      </button>
      <button
        className={`mode-option ${launchMode === "patch" ? "active" : ""}`}
        onClick={() => void actions.setLaunchMode("patch")}
        type="button"
      >
        <strong>完整增强</strong>
        <span>供应商切换后的默认模式；启用会话删除、导出、项目移动等全部页面能力。</span>
      </button>
    </div>
  );
}

function FeatureItem({ title, detail, enabled }: { title: string; detail: string; enabled: boolean }) {
  return (
    <div className="feature-item">
      <div>
        <strong>{title}</strong>
        <span>{detail}</span>
      </div>
      <Badge status={enabled ? "ok" : "disabled"} />
    </div>
  );
}

function FeatureToggle({
  title,
  detail,
  checked,
  disabled = false,
  onChange,
}: {
  title: string;
  detail: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className={`feature-toggle ${disabled ? "disabled" : ""}`}>
      <input
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.currentTarget.checked)}
        type="checkbox"
      />
      <span>
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
    </label>
  );
}

function GuideList({ items }: { items: string[] }) {
  return (
    <div className="guide-list">
      {items.map((item, index) => (
        <div className="guide-step" key={item}>
          <span>{index + 1}</span>
          <p>{item}</p>
        </div>
      ))}
    </div>
  );
}

function InlineTaskProgress({ progress, compact = false }: { progress: TaskProgress; compact?: boolean }) {
  const value = Math.max(0, Math.min(100, Math.round(progress.percent)));
  return (
    <div className={`task-progress ${compact ? "compact" : ""} ${progress.status}`} aria-label={`${progress.label} ${value}%`}>
      <div className="task-progress-copy">
        <span>{progress.label}</span>
        <strong>{value}%</strong>
      </div>
      <div className="task-progress-track" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={value}>
        <span style={{ width: `${value}%` }} />
      </div>
      <small>{progress.detail}</small>
    </div>
  );
}

function NoticeDialog({
  notice,
  onClose,
}: {
  notice: { title: string; message: string; status?: Status };
  onClose: () => void;
}) {
  const failed = notice.status === "failed";
  const warning = notice.status === "not_checked" || notice.status === "not_implemented";
  useEffect(() => {
    const timer = window.setTimeout(onClose, 4200);
    return () => window.clearTimeout(timer);
  }, []);

  return (
    <div className="toast-wrap" role="status" aria-live="polite">
      <div className={`toast-card ${failed ? "failed" : warning ? "warning" : ""}`}>
        <div className="toast-progress" />
        <div className="toast-icon">
          {failed ? <Bell className="h-5 w-5" /> : warning ? <Info className="h-5 w-5" /> : <CheckCircle2 className="h-5 w-5" />}
        </div>
        <div className="toast-body">
          <h2>{notice.title}</h2>
          <p>{notice.message}</p>
        </div>
        <button className="toast-close" onClick={onClose} type="button">×</button>
      </div>
    </div>
  );
}

function Panel({ children, fill = false, className = "" }: { children: React.ReactNode; fill?: boolean; className?: string }) {
  return (
    <Card className={`panel ${fill ? "fill" : ""} ${className}`}>
      {children}
    </Card>
  );
}

function CardHead({ title, detail }: { title: string; detail: string }) {
  return (
    <CardHeader className="panel-head">
      <CardTitle>{title}</CardTitle>
      <CardDescription>{detail}</CardDescription>
    </CardHeader>
  );
}

function Toolbar({ children }: { children: React.ReactNode }) {
  return <div className="toolbar">{children}</div>;
}

function Field({ label, children, className = "" }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <Label className={`field ${className}`}>
      <span>{label}</span>
      {children}
    </Label>
  );
}

function StatusRow({ title, status = "unknown", path }: { title: string; status?: string; path?: string | null }) {
  return (
    <div className="status-row">
      <span>{title}</span>
      <Badge status={status} />
      <code>{path || "未记录路径"}</code>
    </div>
  );
}

function Badge({ status }: { status: string }) {
  return <UiBadge className={statusClass(status)} variant="secondary">{statusLabel(status)}</UiBadge>;
}

function LatestLaunch({ status }: { status: LaunchStatus | null }) {
  if (!status) return <div className="empty">暂无启动状态。</div>;
  const recommendedAction = typeof status.detail?.recommended_action === "string" ? status.detail.recommended_action : "";
  const activationMethod = typeof status.detail?.activation_method === "string" ? status.detail.activation_method : "";
  const appUserModelId = typeof status.detail?.appUserModelId === "string" ? status.detail.appUserModelId : "";
  return (
    <div className="metric-list">
      <Metric label="状态" value={status.status} />
      <Metric label="消息" value={status.message} />
      <Metric label="Debug" value={String(status.debug_port ?? "-")} />
      <Metric label="Helper" value={String(status.helper_port ?? "-")} />
      {activationMethod ? <Metric label="启动方式" value={activationMethod} /> : null}
      {appUserModelId ? <Metric label="AppUserModelID" value={appUserModelId} /> : null}
      {recommendedAction ? <Metric label="建议处理" value={recommendedAction} /> : null}
      <Metric label="时间" value={formatTime(status.started_at_ms)} />
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ScriptRow({ script, actions }: { script: NonNullable<UserScriptInventory["scripts"]>[number]; actions: Actions }) {
  const source = script.market_id ? `市场 · ${script.version || "未知版本"}` : script.source === "builtin" ? "内置" : "用户";
  const canDelete = script.source === "user";
  return (
    <div className="table-row">
      <span>{script.name}</span>
      <span>{source}</span>
      <span>{script.enabled ? "启用" : "关闭"}</span>
      <span>{script.status}</span>
      <div className="script-row-actions">
        <Button onClick={() => void actions.setUserScriptEnabled(script.key, !script.enabled)} size="sm" variant="secondary">
          {script.enabled ? <PowerOff className="h-4 w-4" /> : <Power className="h-4 w-4" />}
          {script.enabled ? "禁用" : "启用"}
        </Button>
        {canDelete ? (
          <Button onClick={() => void actions.deleteUserScript(script.key)} size="sm" variant="outline">
            <Trash2 className="h-4 w-4" />
            删除
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function routeTitle(route: Route) {
  return routes.find((item) => item.id === route)?.label ?? "概览";
}

function routeSubtitle(route: Route) {
  const subtitles: Record<Route, string> = {
    overview: "启动、连接和修复都从这里开始",
    installGuide: "按新手流程完成安装、导入和模式配置",
    relay: "选择官方登录或 API 服务，不必手动找配置文件",
    proxy: "为中转、远程控制、官方登录和语音分别选择代理策略",
    context: "统一管理 Codex MCP、Skills 和 Plugins 配置",
    enhance: "打开会话删除、导出、项目移动和脚本能力",
    userScripts: "管理脚本市场、本地脚本和启停状态",
    providerSync: "切换模式后，让旧对话重新出现在列表里",
    maintenance: "找不到入口、路径不对或启动异常时使用",
    settings: "主题、命令包装器和启动参数，普通使用可忽略",
    logs: "查看最近运行记录",
    diagnostics: "生成可复制的问题报告",
    about: "查看版本、项目链接和更新状态",
  };
  return subtitles[route];
}

function ccsProviderSummary(result: CcsProvidersResult | null) {
  if (!result) return "尚未读取 CCS 数据库。";
  if (!isSuccessStatus(result.status)) return result.message;
  if (!result.providers.length) return `未发现 CCS Codex 供应商：${result.dbPath}`;
  return `发现 ${result.providers.length} 个 CCS Codex 供应商：${result.dbPath}`;
}

function ccsCandidateSummary(status: InstallGuideStatusResult | null, result: CcsProvidersResult | null) {
  const candidates = result?.dbPathCandidates ?? status?.ccs.dbPathCandidates ?? [];
  if (!candidates.length) return "-";
  return candidates.slice(0, 3).join(" ｜ ");
}

function providerInitial(name: string) {
  const trimmed = (name || "供应商").trim();
  return Array.from(trimmed)[0]?.toUpperCase() || "供";
}

function statusLabel(status: string) {
  const labels: Record<string, string> = {
    found: "已找到",
    limited: "受限",
    missing: "缺失",
    installed: "已安装",
    available: "可用",
    unsupported: "不支持",
    ok: "正常",
    running: "运行中",
    failed: "失败",
    accepted: "已受理",
    not_checked: "未检查",
    not_implemented: "未实现",
    disabled: "已禁用",
    unknown: "未知",
    up_to_date: "已最新",
    missing_asset: "缺少安装包",
    downloaded: "已下载",
    opened_release: "已打开发布页",
    degraded: "稍后再查",
  };
  return labels[status] ?? status;
}

function statusClass(status: string) {
  if (["found", "installed", "ok", "running", "available", "up_to_date", "downloaded", "opened_release"].includes(status)) return "good";
  if (["failed", "missing"].includes(status)) return "bad";
  return "warn";
}

function platformLabel(platform: string) {
  const labels: Record<string, string> = {
    darwin: "macOS",
    windows: "Windows",
    linux: "Linux",
    unknown: "未知",
  };
  return labels[platform] ?? platform;
}

function platformGuideFor(status: InstallGuideStatusResult | null): PlatformGuide {
  if (status?.platformGuide) return status.platformGuide;
  const platform = status?.platform ?? "unknown";
  const label = status?.platformLabel || platformLabel(platform);
  const base: PlatformGuide = {
    platform,
    platformLabel: label,
    title: `${label} 新手引导`,
    systemDescription: "安装包、路径检测和桌面运行方式会根据当前系统自动切换。",
    desktopRuntime: status?.desktopRuntime || "桌面窗口",
    desktopRuntimeDescription: "当前系统使用桌面窗口运行管理器。",
    installTitle: "安装 ChatGPT",
    installActionLabel: "打开安装入口",
    installSourceLabel: "安装入口",
    installDescription: "按当前系统打开 ChatGPT 桌面应用安装入口。",
    manualPrimaryLabel: "手动选择应用",
    manualPrimaryMode: "folder",
    manualSecondaryLabel: "",
    manualSecondaryMode: "",
    detectionNote: "自动检测暂未找到 ChatGPT 时，可以手动选择实际安装位置。",
    pathHint: "",
    launchMethodLabel: "启动方式",
    launchTargetLabel: "启动文件",
    completionLabel: "已完成当前系统引导",
    unsupported: false,
  };
  if (platform === "darwin") {
    return {
      ...base,
      title: "macOS 新手引导",
      systemDescription: "macOS 会检查 ChatGPT.app、官方安装页和 WebKit 桌面窗口运行状态。",
      desktopRuntime: status?.desktopRuntime || "macOS WebKit 桌面窗口",
      desktopRuntimeDescription: "macOS 使用 WebKit 桌面窗口运行管理器。",
      installTitle: "macOS 官方安装",
      installActionLabel: "打开官方安装页",
      installSourceLabel: "官方页面",
      manualPrimaryLabel: "选择 ChatGPT.app",
      manualPrimaryMode: "folder",
      detectionNote: "macOS 已安装但未识别时，选择 /Applications/ChatGPT.app 或实际放置 ChatGPT.app 的应用目录即可。",
      pathHint: "/Applications/ChatGPT.app",
      launchTargetLabel: "ChatGPT.app",
    };
  }
  if (platform === "windows") {
    return {
      ...base,
      title: "Windows 新手引导",
      systemDescription: "Windows 会同时识别 Codex 与 ChatGPT 合并后的 OpenAI.Codex / OpenAI.ChatGPT MSIX 包，以及 Codex.exe / ChatGPT.exe。",
      desktopRuntime: status?.desktopRuntime || "Windows WebView2 桌面窗口",
      desktopRuntimeDescription: "Windows 使用 WebView2 桌面窗口运行管理器。",
      installTitle: "Windows 官方安装",
      installActionLabel: "打开 ChatGPT 下载页",
      installSourceLabel: "官方页面",
      installDescription: "请安装新版 ChatGPT 桌面应用；原 Codex 应用更新后也会成为该合并版。自动检测失败时可选择 Codex.exe、ChatGPT.exe 或安装目录。",
      manualPrimaryLabel: "选择 ChatGPT.exe / Codex.exe",
      manualPrimaryMode: "file",
      manualSecondaryLabel: "选择安装目录",
      manualSecondaryMode: "folder",
      detectionNote: "Microsoft Store/MSIX 版不会扫描或信任任意 WindowsApps 路径；仅使用已注册官方包清单声明的 App Execution Alias 或 FullTrust 入口，并以真实 AppUserModelID、进程包身份和调试端口完成验证。",
      pathHint: "OpenAI.Codex / OpenAI.ChatGPT，或 WindowsApps 外的 Codex.exe / ChatGPT.exe",
      launchTargetLabel: "AppUserModelID 或可执行文件",
    };
  }
  return { ...base, unsupported: true };
}

function archLabel(arch: string) {
  const labels: Record<string, string> = {
    amd64: "x64",
    arm64: "ARM64",
    "386": "x86",
  };
  return labels[arch] ?? (arch || "-");
}

function platformSummary(status: InstallGuideStatusResult | null) {
  if (!status) return "识别中";
  return `${status.platformLabel || platformLabel(status.platform)} · ${status.archLabel || archLabel(status.arch)}`;
}

function installGuideCompletedForCurrentPlatform(status: InstallGuideStatusResult | null, settings: BackendSettings) {
  if (status?.onboardingCompletedForCurrentPlatform !== undefined) return status.onboardingCompletedForCurrentPlatform;
  const platform = status?.platform || settings.onboardingCompletedPlatform;
  return settings.onboardingCompleted && !!platform && settings.onboardingCompletedPlatform === platform;
}

function codexToolsReleaseUrl() {
  return PROJECT_RELEASES_URL;
}

function isSuccessStatus(status?: Status) {
  return status === "ok" || status === "accepted";
}

function relayApplySucceeded(result: RelayResult) {
  return isSuccessStatus(result.status) || result.configApplied === true;
}

function apiModeLabel(relay: RelayResult | null) {
  if (relay?.appliedMode) return relayModeLabel(normalizeRelayMode(relay.appliedMode));
  if (relay?.activeMode) return relayModeLabel(normalizeRelayMode(relay.activeMode));
  if (!relay?.configured) return "官方登录";
  return relayOfficialAuthenticated(relay) ? "官方混合 API" : "中转 API";
}

function healthItems(overview: OverviewResult | null, relay: RelayResult | null) {
  const codexApp = pathStateOrDefault(overview?.codex_app);
  const silentShortcut = pathStateOrDefault(overview?.silent_shortcut);
  const managementShortcut = pathStateOrDefault(overview?.management_shortcut);
  return [
    {
      title: "ChatGPT 应用",
      status: codexApp.status,
      ok: codexApp.status === "found",
      detail: codexApp.path || "尚未检查 ChatGPT 应用路径。",
    },
    {
      title: "静默启动入口",
      status: silentShortcut.status,
      ok: silentShortcut.status === "installed",
      detail: silentShortcut.path || "缺少 ChatGPT Codex 静默启动快捷方式时可在安装维护页修复。",
    },
    {
      title: "管理工具入口",
      status: managementShortcut.status,
      ok: managementShortcut.status === "installed",
      detail: managementShortcut.path || "缺少管理工具快捷方式时可在安装维护页修复。",
    },
    {
      title: "ChatGPT 登录",
      status: relayOfficialAuthenticated(relay) ? "ok" : "missing",
      ok: relayOfficialAuthenticated(relay),
      detail: relayOfficialAccountLabel(relay) || relay?.officialAuthSource || "官方混合 API 需要官方登录；中转 API 可不用官方登录。",
    },
  ];
}

function pathStateOrDefault(state: PathState | null | undefined): PathState {
  return state ?? { status: "not_checked", path: null };
}

function clampNumber(value: number, minimum: number, maximum: number): number {
  if (!Number.isFinite(value)) return minimum;
  return Math.min(maximum, Math.max(minimum, Math.round(value)));
}

type ImageHandlingMode = "send-as-is" | "strip" | "vlm";

function normalizeImageHandlingMode(value: unknown): ImageHandlingMode {
  return value === "strip" || value === "vlm" ? value : "send-as-is";
}

function parseModelVlmJSON(value: string): Record<string, ImageHandlingMode> {
  try {
    const parsed = JSON.parse(value || "{}") as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed)
        .map(([model, mode]) => [model.trim(), normalizeImageHandlingMode(mode)] as const)
        .filter(([model]) => Boolean(model)),
    );
  } catch {
    return {};
  }
}

function normalizeModelVlmJSON(value: string): string {
  const modes = parseModelVlmJSON(value);
  return Object.keys(modes).length ? JSON.stringify(modes) : "";
}

function relayProfileModelNames(profile: RelayProfile): string[] {
  return Array.from(
    new Set(
      [profile.model, ...profile.modelList.split(/[\r\n,]+/)]
        .map((model) => model.trim().replace(/\[[^\]]+\]$/, "").trim())
        .filter(Boolean),
    ),
  );
}

function updateModelImageHandling(profile: RelayProfile, model: string, mode: ImageHandlingMode): string {
  return JSON.stringify({ ...parseModelVlmJSON(profile.modelVlm), [model]: mode });
}

function normalizeSettings(settings: BackendSettings): BackendSettings {
  const relayCommonConfigContents = normalizeConfigText(settings.relayCommonConfigContents || "");
  const relayContextConfigContents = normalizeConfigText(settings.relayContextConfigContents || "");
  const defaultContextSelection = contextSelectionForAllEntries({
    ...settings,
    relayCommonConfigContents,
    relayContextConfigContents,
  });
  const profiles =
    settings.relayProfiles?.length
      ? settings.relayProfiles.map((profile, index) => normalizeRelayProfile(profile, index, defaultContextSelection))
      : [
          {
            id: settings.activeRelayId || "default",
            linkedCcsProviderId: "",
            name: "默认中转",
            model: "",
            baseUrl: settings.relayBaseUrl || defaultSettings.relayBaseUrl,
            upstreamBaseUrl: settings.relayBaseUrl || defaultSettings.relayBaseUrl,
            apiKey: settings.relayApiKey || "",
            imageGenerationEnabled: false,
            imageGenerationUseSeparateApi: false,
            imageGenerationBaseUrl: "",
            imageGenerationApiKey: "",
            protocol: "responses" as RelayProtocol,
            relayMode: "official" as RelayMode,
            officialMixApiKey: false,
            officialAuthContents: "",
            officialAccountLabel: "",
            officialAuthUpdatedAt: "",
            hideOfficialUsageAlert: false,
            testModel: "",
            configContents: "",
            authContents: "",
            useCommonConfig: true,
            contextSelection: defaultContextSelection,
            contextSelectionInitialized: true,
            contextWindow: "",
            autoCompactLimit: "",
            modelInsertMode: "patch",
            modelList: "",
            modelWindows: "",
            modelAutoCompact: "",
            modelRoutes: [],
            modelVlm: "",
            vlmApiKey: "",
            vlmModel: "",
            vlmBaseUrl: "",
            userAgent: "",
            sub2apiEnabled: false,
            sub2apiMultiplier: "",
            proxyEnabled: false,
            proxyUrl: "",
          },
        ];
  const activeRelayId = profiles.some((profile) => profile.id === settings.activeRelayId)
    ? settings.activeRelayId
    : profiles[0]?.id || "default";
  const aggregateRelayProfiles = (settings.aggregateRelayProfiles ?? []).map((profile, index) => normalizeAggregateRelayProfile(profile, index));
  const activeAggregateRelayId = aggregateRelayProfiles.some((profile) => profile.id === settings.activeAggregateRelayId)
    ? settings.activeAggregateRelayId
    : activeRelayId;
  return syncLegacyRelayFields({
    ...defaultSettings,
    ...settings,
    providerSyncSavedProviders: settings.providerSyncSavedProviders ?? [],
    providerSyncManualProviders: settings.providerSyncManualProviders ?? [],
    providerSyncLastSelectedProvider: settings.providerSyncLastSelectedProvider || "",
    relayProfilesEnabled: settings.relayProfilesEnabled !== false,
    ccsLinkEnabled: settings.ccsLinkEnabled === true,
    language: normalizeLanguage(settings.language),
    codexAppStepwiseApiKeyEnv: settings.codexAppStepwiseApiKeyEnv?.trim() || "CODEX_STEPWISE_API_KEY",
    codexAppStepwiseMaxItems: clampNumber(settings.codexAppStepwiseMaxItems ?? 6, 0, 6),
    codexAppStepwiseMaxInputChars: clampNumber(settings.codexAppStepwiseMaxInputChars || 6000, 1000, 24000),
    codexAppStepwiseMaxOutputTokens: clampNumber(settings.codexAppStepwiseMaxOutputTokens || 500, 100, 4000),
    codexAppStepwiseTimeoutMs: clampNumber(settings.codexAppStepwiseTimeoutMs || 8000, 1000, 60000),
    relayCommonConfigContents,
    relayContextConfigContents,
    relayProfiles: profiles,
    activeRelayId,
    aggregateRelayProfiles,
    activeAggregateRelayId,
    mobileControlRelayUrl: settings.mobileControlRelayUrl || "",
    mobileControlRoom: settings.mobileControlRoom || "",
    mobileControlKey: settings.mobileControlKey || "",
  });
}

function normalizeAggregateRelayProfile(profile: AggregateRelayProfile, index = 0): AggregateRelayProfile {
  return {
    id: profile.id || `aggregate-${index + 1}`,
    name: profile.name || profile.id || `聚合供应商 ${index + 1}`,
    strategy: normalizeAggregateRelayStrategy(profile.strategy),
    members: (profile.members ?? []).map((member) => ({
      relayId: member.relayId || "",
      weight: Math.max(1, Number.isFinite(Number(member.weight)) ? Number(member.weight) : 1),
    })),
  };
}

function normalizeAggregateRelayStrategy(strategy: string): AggregateRelayStrategy {
  if (strategy === "conversationRoundRobin" || strategy === "requestRoundRobin" || strategy === "weightedRoundRobin") return strategy;
  return "failover";
}

function codexExtraArgsToInput(args: string[] | undefined) {
  return (args ?? []).join("\n");
}

function inputToCodexExtraArgs(value: string) {
  return value === "" ? [] : value.split(/\r?\n/);
}

function normalizeRelayProfile(profile: RelayProfile, index = 0, defaultContextSelection = emptyContextSelection()): RelayProfile {
  const relayMode =
    profile.relayMode === "pureApi"
      ? "pureApi"
      : profile.relayMode === "aggregate"
        ? "aggregate"
      : profile.relayMode === "mixedApi" || profile.officialMixApiKey === true
        ? "mixedApi"
        : normalizeRelayMode(profile.relayMode);
  const baseUrl = (profile.baseUrl || profile.upstreamBaseUrl || "").trim();
  const normalized: RelayProfile = {
    ...defaultSettings.relayProfiles[0],
    ...profile,
    id: profile.id || `relay-${index + 1}`,
    linkedCcsProviderId: profile.linkedCcsProviderId || "",
    name: profile.name || "",
    model: profile.model || "",
    baseUrl,
    upstreamBaseUrl: baseUrl,
    apiKey: profile.apiKey || "",
    imageGenerationEnabled: Boolean(profile.imageGenerationEnabled),
    imageGenerationUseSeparateApi: Boolean(profile.imageGenerationUseSeparateApi),
    imageGenerationBaseUrl: profile.imageGenerationBaseUrl || "",
    imageGenerationApiKey: profile.imageGenerationApiKey || "",
    protocol: profile.protocol === "chatCompletions" ? "chatCompletions" : "responses",
    relayMode,
    officialMixApiKey: relayMode === "mixedApi",
    officialAuthContents: profile.officialAuthContents || "",
    officialAccountLabel: profile.officialAccountLabel || "",
    officialAuthUpdatedAt: profile.officialAuthUpdatedAt || "",
    hideOfficialUsageAlert: Boolean(profile.hideOfficialUsageAlert),
    testModel: profile.testModel || "",
    configContents: profile.configContents || "",
    authContents: profile.authContents || "",
    useCommonConfig: profile.useCommonConfig !== false,
    contextSelection: profile.contextSelectionInitialized
      ? normalizeContextSelection(profile.contextSelection)
      : normalizeContextSelection(defaultContextSelection),
    contextSelectionInitialized: true,
    contextWindow: profile.contextWindow || "",
    autoCompactLimit: profile.autoCompactLimit || "",
    modelInsertMode: profile.modelInsertMode || "patch",
    modelList: profile.modelList || "",
    modelWindows: profile.modelWindows || "",
    modelAutoCompact: profile.modelAutoCompact || "",
    modelRoutes: normalizeRelayModelRoutes(profile.modelRoutes),
    modelVlm: normalizeModelVlmJSON(profile.modelVlm || ""),
    vlmApiKey: profile.vlmApiKey || "",
    vlmModel: profile.vlmModel || "",
    vlmBaseUrl: profile.vlmBaseUrl || "",
    userAgent: profile.userAgent || "",
    sub2apiEnabled: profile.sub2apiEnabled === true,
    sub2apiMultiplier: profile.sub2apiEnabled === true ? profile.sub2apiMultiplier || "" : "",
    proxyEnabled: Boolean(profile.proxyEnabled),
    proxyUrl: profile.proxyUrl || "",
  };
  if (!normalized.configContents.trim()) {
    return withGeneratedRelayFiles(normalized);
  }
  return normalized;
}

function activeRelayProfile(settings: BackendSettings): RelayProfile {
  return (
    settings.relayProfiles.find((profile) => profile.id === settings.activeRelayId) ||
    settings.relayProfiles[0] ||
    defaultSettings.relayProfiles[0]
  );
}

function aggregateRelayProfileFor(settings: BackendSettings, profile: RelayProfile): AggregateRelayProfile {
  return settings.aggregateRelayProfiles.find((item) => item.id === profile.id) || defaultAggregateForProfile(settings, profile);
}

function defaultAggregateForProfile(settings: BackendSettings, profile: RelayProfile): AggregateRelayProfile {
  const members = settings.relayProfiles
    .filter((item) => item.id !== profile.id && item.relayMode === "pureApi" && item.baseUrl.trim() && item.apiKey.trim())
    .slice(0, 2)
    .map((item) => ({ relayId: item.id, weight: 1 }));
  return {
    id: profile.id,
    name: profile.name || profile.id,
    strategy: "failover",
    members,
  };
}

function relayProtocolLabel(protocol: RelayProtocol): string {
  return protocol === "chatCompletions" ? "Chat Completions 转 Responses" : "Responses API";
}

function normalizeRelayMode(mode: RelayMode | undefined): RelayMode {
  if (mode === "aggregate") return mode;
  if (mode === "pureApi") return mode;
  if (mode === "mixedApi") return mode;
  return "official";
}

function relayModeLabel(mode: RelayMode): string {
  if (mode === "aggregate") return "聚合轮转";
  if (mode === "pureApi") return "中转 API";
  if (mode === "mixedApi") return "官方混合 API";
  return "官方登录";
}

function providerSyncTargetSourcesLabel(sources: string[]): string {
  const labels: Record<string, string> = {
    config: "配置",
    rollout: "会话",
    sqlite: "索引",
    manual: "已保存",
  };
  return sources.map((source) => labels[source] || source).join(" / ") || "自动发现";
}

function modeHistorySyncStatusLabel(last: ModeHistorySyncResult | null, relay: RelayResult | null, targetProvider: string): string {
  const lastForTarget = last && (!last.targetProvider || last.targetProvider === targetProvider) ? last : null;
  const relaySync = relay?.providerSync?.targetProvider === targetProvider ? relay.providerSync : null;
  const status = lastForTarget?.syncStatus || relaySync?.status || "";
  const changedFiles = lastForTarget?.changedSessionFiles ?? relaySync?.changedSessionFiles ?? 0;
  const sqliteRows = lastForTarget?.sqliteRowsUpdated ?? relaySync?.sqliteRowsUpdated ?? 0;
  if (status === "synced") {
    return changedFiles || sqliteRows
      ? `已同步 · ${changedFiles} 个文件 / ${sqliteRows} 行索引`
      : "已检查，无需更新";
  }
  if (status === "failed" || status === "partial") return "同步失败，请查看提示";
  if (status === "skipped") return "待重试";
  return "尚未执行";
}

function relayProfileConfigBrief(profile: RelayProfile): string {
  if (profile.relayMode === "official") return "不写 API 文件";
  if (profile.relayMode === "mixedApi") return "混入 API Key";
  return profile.baseUrl || "未填写 URL";
}

function relayImageModeLabel(profile: RelayProfile): string {
  if (!profile.imageGenerationEnabled) return "图片关闭";
  if (profile.imageGenerationUseSeparateApi && profile.protocol === "responses") return "图片独立 API";
  return "图片走当前中转";
}

function formatMultiplierValue(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "";
  return value.toFixed(4).replace(/0+$/, "").replace(/\.$/, "");
}

function updateInstallHint(update: UpdateResult | null | undefined): string {
  if (!update) return "";
  if (update.platform === "darwin") {
    if (update.installerDefault) return `安装到 ${update.installTarget || "/Applications"} 并覆盖旧版`;
    if (update.portable) return "便携 zip，仅备用";
    if (update.assetKind === "dmg") return "拖入 /Applications 覆盖旧版";
  }
  if (update.assetKind === "installer") return "安装器";
  if (update.portable) return "便携包";
  return "";
}

function zedProjectSourceLabel(source: string): string {
  switch (source) {
    case "currentThread":
      return "当前会话";
    case "codexRemoteProject":
      return "Codex remote project";
    case "threadWorkspaceHint":
      return "Thread workspace";
    case "sqliteThreadCwd":
      return "SQLite thread cwd";
    case "recent":
      return "最近打开";
    default:
      return source || "未知来源";
  }
}

function officialBindingStatusLabel(profile: RelayProfile): string {
  return (profile.authContents.trim() || profile.officialAuthContents.trim()) ? "官方号已绑定" : "官方号未绑定";
}

function officialBindingLabel(profile: RelayProfile): string {
  return profile.officialAccountLabel || ((profile.authContents.trim() || profile.officialAuthContents.trim()) ? "已绑定" : "-");
}

function relayOfficialAuthenticated(relay: RelayResult | null): boolean {
  return !!(relay?.officialAuthenticated || relay?.authenticated || relay?.boundOfficialAuthenticated);
}

function relayOfficialAccountLabel(relay: RelayResult | null): string {
  return relay?.officialAccountLabel || relay?.accountLabel || relay?.boundOfficialAccountLabel || "";
}

function relayCurrentOfficialLabel(relay: RelayResult | null): string {
  if (relay?.currentAuthenticated || relay?.authenticated) return relay?.currentAccountLabel || relay?.accountLabel || "已登录";
  return "未检测到";
}

function relayBoundOfficialLabel(relay: RelayResult | null): string {
  if (!relay?.boundOfficialAuthenticated) return "未绑定";
  const label = relay.boundOfficialAccountLabel || "已绑定";
  return relay.boundOfficialProfileName ? `${label} · ${relay.boundOfficialProfileName}` : label;
}

function relayOfficialLoginLabel(relay: RelayResult | null): string {
  if (relayOfficialAuthenticated(relay)) return relayOfficialAccountLabel(relay) || "已检测到官方账号";
  return "未检测到官方登录";
}

function formatOfficialAuthTime(value: string): string {
  if (!value) return "-";
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return value;
  return time.toLocaleString("zh-CN");
}

function relayProfileModeHelp(profile: RelayProfile): string {
  if (profile.relayMode === "official") {
    return "此供应商会切回官方登录模式，使用它绑定的 ChatGPT 官方账号，不写入 API Key。";
  }
  if (profile.relayMode === "mixedApi") {
    return "此供应商会使用它绑定的官方账号，并把请求混入当前 API Key；切换后页面增强会设为完整增强。";
  }
  if (profile.relayMode === "pureApi") {
    return "此供应商会按中转 API 模式写入 config.toml，并保留现有 auth.json 登录状态。";
  }
  if (profile.relayMode === "aggregate") {
    return "此供应商会写入本地协议代理地址，并按策略在多个 API 供应商之间轮转。";
  }
  return "";
}

function relayProfileReadinessText(profile: RelayProfile, relay: RelayResult | null): string {
  const hasAuthSnapshot = profile.authContents.trim() || profile.officialAuthContents.trim();
  if (profile.relayMode === "aggregate") {
    return "聚合供应商就绪：请求会由本地协议代理按成员策略选择实际 API 供应商。";
  }
  if (profile.relayMode === "official") {
    return hasAuthSnapshot
      ? `此供应商已绑定官方账号：${officialBindingLabel(profile)}。`
      : "此供应商还没有绑定官方账号；请先登录目标 ChatGPT 账号并绑定当前登录。";
  }
  if (profile.relayMode === "mixedApi") {
    const hasApiFields = profile.baseUrl.trim() && profile.apiKey.trim();
    const hasOfficialBinding = hasAuthSnapshot;
    if (!hasOfficialBinding && !hasApiFields) return "此供应商未绑定官方账号，也未配置混入 API 的 Base URL / Key。";
    if (!hasOfficialBinding) return "此供应商还没有绑定官方账号；官方混合 API 需要先绑定。";
    if (!hasApiFields) return "当前还没有填写混入 API 的 Base URL / Key。";
    return `官方账号已绑定：${officialBindingLabel(profile)}，会混入当前 API Key。`;
  }
  const hasConfig = profile.configContents.trim();
  if (!hasConfig) return "当前中转还没有完整 config.toml。";
  if (!hasAuthSnapshot) return "中转 API 就绪：会写入此供应商的 config.toml；因为未保存 auth 快照，切换时会兼容保留当前登录。";
  return "中转 API 就绪：会按此供应商自己的 config.toml 和 auth.json 快照恢复当前环境。";
}

function relayProfileGuideReady(profile: RelayProfile, connection?: InstallGuideConnectionStatus, relay?: RelayResult | null): boolean {
  if (connection && connection.mode === profile.relayMode && connection.profileId === profile.id) {
    return connection.ready;
  }
  const officialReady = profile.authContents.trim().length > 0 || profile.officialAuthContents.trim().length > 0 || relayOfficialAuthenticated(relay ?? null);
  const apiReady = profile.baseUrl.trim().length > 0 && profile.apiKey.trim().length > 0 &&
    (profile.protocol !== "responses" || !profile.imageGenerationEnabled || !profile.imageGenerationUseSeparateApi ||
      (!!profile.imageGenerationBaseUrl.trim() && !!profile.imageGenerationApiKey.trim()));
  if (profile.relayMode === "aggregate") return true;
  if (profile.relayMode === "official") return officialReady;
  if (profile.relayMode === "mixedApi") return officialReady && apiReady;
  return apiReady;
}

function guideConnectionReadinessText(
  profile: RelayProfile,
  connection: InstallGuideConnectionStatus | undefined,
  relay: RelayResult | null,
  validationProblem: string | null,
): string {
  if (connection && connection.mode === profile.relayMode && connection.profileId === profile.id && connection.message) {
    return connection.message;
  }
  if (relayProfileGuideReady(profile, connection, relay)) return relayProfileReadinessText(profile, relay);
  return validationProblem || relayProfileReadinessText(profile, relay);
}

function guideConnectionFacts(profile: RelayProfile, connection: InstallGuideConnectionStatus | undefined, relay: RelayResult | null) {
  const officialReady = connection?.profileId === profile.id
    ? connection.officialReady
    : profile.officialAuthContents.trim().length > 0 || relayOfficialAuthenticated(relay);
  const apiReady = connection?.profileId === profile.id
    ? connection.apiReady
    : profile.baseUrl.trim().length > 0 && profile.apiKey.trim().length > 0;
  return [
    { label: "官方账号", value: officialReady ? officialBindingLabel(profile) || relayOfficialLoginLabel(relay) : "未绑定" },
    { label: "服务器参数", value: profile.relayMode === "official" || profile.relayMode === "aggregate" ? "不需要" : apiReady ? "已填写" : "缺少 URL / Key" },
    { label: "当前写入", value: relay?.configured || connection?.configured ? "已配置" : profile.relayMode === "official" ? "官方登录" : "未写入" },
    { label: "供应商", value: profile.name || profile.id },
  ];
}

function prepareRelaySettingsForSwitch(settings: BackendSettings): BackendSettings {
  const activeId = activeRelayProfile(settings).id;
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles: settings.relayProfiles.map((profile) => (
      profile.id === activeId ? withGeneratedRelayFiles(profile) : profile
    )),
  });
}

function relayProfileSwitchCommand(profile: RelayProfile): "clear_relay_injection" | "apply_relay_injection" | "apply_pure_api_injection" {
  if (profile.relayMode === "aggregate") return "apply_pure_api_injection";
  if (profile.relayMode === "pureApi") return "apply_pure_api_injection";
  if (profile.relayMode === "mixedApi") return "apply_relay_injection";
  return "clear_relay_injection";
}

function relayProfileModeSwitchedText(profile: RelayProfile, relay?: RelayResult | null): string {
  const base = profile.relayMode === "pureApi"
    ? "已按此供应商切换到中转 API；页面增强已设为完整增强。"
    : profile.relayMode === "aggregate"
      ? "已按此聚合供应商切换到轮转代理；页面增强已设为完整增强。"
    : profile.relayMode === "mixedApi"
      ? "已按此供应商使用官方登录，并混入 API Key；页面增强已设为完整增强。"
      : "已按此供应商切回官方登录；页面增强已设为完整增强。";
  const repairMessage = relay?.pluginRepair?.message?.trim();
  const historySync = relay?.providerSync;
  const historyMessage = historySync?.status === "synced"
    ? (historySync.changedSessionFiles || historySync.sqliteRowsUpdated)
      ? `聊天记录已同步：${historySync.changedSessionFiles ?? 0} 个会话文件，${historySync.sqliteRowsUpdated ?? 0} 行索引。`
      : "聊天记录已检查，无需更新。"
      : historySync?.status === "skipped"
      ? "模式已切换，但聊天记录同步暂未完成；请到“历史修复”点击“同步模式对话历史”重试。"
      : historySync?.status === "failed" || historySync?.status === "partial"
        ? `模式已切换，但聊天记录同步失败：${historySync.message || "请使用备份恢复后重试。"}`
      : "";
  const backupMessage = historySync?.backupDir ? `聊天记录备份：${historySync.backupDir}` : "";
  return [base, historyMessage, backupMessage, repairMessage].filter(Boolean).join(" ");
}

function withGeneratedRelayFiles(profile: RelayProfile): RelayProfile {
  if (profile.relayMode === "aggregate") {
    return {
      ...profile,
      officialMixApiKey: false,
      configContents: buildRelayConfigTomlWithProfileOverrides(profile),
      authContents: profile.authContents,
    };
  }
  if (profile.relayMode === "official") {
    return {
      ...profile,
      officialMixApiKey: false,
      configContents: profile.configContents,
      authContents: profile.authContents,
    };
  }
  if (profile.relayMode === "mixedApi") {
    return {
      ...profile,
      officialMixApiKey: true,
      configContents: buildRelayConfigTomlWithProfileOverrides(profile),
      authContents: profile.authContents,
    };
  }
  return {
    ...profile,
    officialMixApiKey: false,
    configContents: buildRelayConfigTomlWithProfileOverrides(profile),
    authContents: profile.authContents,
  };
}

function buildRelayConfigTomlWithProfileOverrides(profile: RelayProfile): string {
  const generated = buildRelayConfigToml(profile);
  const goals = tomlFeatureBoolean(profile.configContents, "goals");
  return goals === null ? generated : setTomlFeatureBoolean(generated, "goals", goals);
}

function buildRelayConfigToml(
  profile: Pick<
    RelayProfile,
    | "model"
    | "baseUrl"
    | "apiKey"
    | "protocol"
    | "relayMode"
    | "imageGenerationEnabled"
    | "imageGenerationUseSeparateApi"
    | "imageGenerationBaseUrl"
    | "contextWindow"
    | "autoCompactLimit"
    | "proxyEnabled"
    | "proxyUrl"
    | "modelRoutes"
  >,
): string {
  const isAggregate = profile.relayMode === "aggregate";
  const usesImageProxy =
    !isAggregate &&
    profile.protocol === "responses" &&
    (!profile.imageGenerationEnabled || (profile.imageGenerationUseSeparateApi && profile.imageGenerationBaseUrl.trim()));
  const usesHttpProxy = !isAggregate && profile.relayMode !== "official" && profile.protocol === "responses" && profile.proxyEnabled && profile.proxyUrl.trim() !== "";
  const usesModelRoutes = !isAggregate && profile.protocol === "responses" && hasCompleteModelRoutes(profile as RelayProfile);
  const baseUrl = isAggregate
    ? PROTOCOL_PROXY_BASE_URL
    : usesImageProxy || usesHttpProxy || usesModelRoutes
    ? LOCAL_RELAY_PROXY_BASE_URL
    : profile.protocol === "chatCompletions"
      ? PROTOCOL_PROXY_BASE_URL
      : profile.baseUrl.trim();
  const apiKey = isAggregate ? "codex-plus-aggregate" : profile.apiKey.trim();
  const lines = [
    profile.model.trim() ? `model = "${tomlString(profile.model.trim())}"` : "",
    profile.contextWindow.trim() ? `model_context_window = ${profile.contextWindow.trim()}` : "",
    profile.autoCompactLimit.trim() ? `model_auto_compact_token_limit = ${profile.autoCompactLimit.trim()}` : "",
    'model_provider = "CodexPlusPlus"',
    "",
    "[model_providers.CodexPlusPlus]",
    'name = "CodexPlusPlus"',
    'wire_api = "responses"',
    "requires_openai_auth = true",
    `base_url = "${tomlString(baseUrl)}"`,
  ].filter(Boolean);
  if (profile.protocol === "responses" && !profile.imageGenerationEnabled) {
    lines.push('disabled_tools = ["image_generation"]');
  }
  if (usesImageProxy) {
    lines.push(`codex_plus_text_base_url = "${tomlString(normalizeRelayBaseUrl(profile.baseUrl))}"`);
  }
  if (profile.protocol === "responses" && profile.imageGenerationEnabled && profile.imageGenerationUseSeparateApi) {
    lines.push(`codex_plus_image_base_url = "${tomlString(normalizeRelayBaseUrl(profile.imageGenerationBaseUrl))}"`);
    lines.push("# codex_plus_image_api_key 只保存在 ChatGPT Codex 设置里，图片路由命中时由本地代理使用。");
  }
  lines.push(`experimental_bearer_token = "${tomlString(apiKey)}"`, "");
  return lines.join("\n");
}

function relayProfileSwitchValidation(profile: RelayProfile): string | null {
  const hasAuthSnapshot = profile.authContents.trim() || profile.officialAuthContents.trim();
  if (profile.relayMode === "official") {
    if (!hasAuthSnapshot) {
      return `供应商「${profile.name || profile.id}」还没有绑定官方账号，已停止切换。`;
    }
    return null;
  }
  if (profile.relayMode === "mixedApi" && !hasAuthSnapshot) {
    return `供应商「${profile.name || profile.id}」还没有绑定官方账号，已停止切换。`;
  }
  if (profile.relayMode === "aggregate") {
    return null;
  }
  if (!profile.baseUrl.trim()) {
    return `供应商「${profile.name || profile.id}」缺少 Base URL，已停止切换。`;
  }
  if (!profile.apiKey.trim()) {
    return `供应商「${profile.name || profile.id}」缺少 API Key，已停止切换。`;
  }
  if (profile.protocol === "responses" && profile.imageGenerationEnabled && profile.imageGenerationUseSeparateApi) {
    if (!profile.imageGenerationBaseUrl.trim()) return `供应商「${profile.name || profile.id}」缺少图片 Base URL，已停止切换。`;
    if (!profile.imageGenerationApiKey.trim()) return `供应商「${profile.name || profile.id}」缺少图片 Key，已停止切换。`;
  }
  return null;
}

function tomlString(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function normalizeRelayBaseUrl(value: string): string {
  const trimmed = value.trim().replace(/\/+$/, "");
  if (!trimmed) return trimmed;
  const [, rest = trimmed] = trimmed.split("://");
  if (rest.includes("/")) return trimmed;
  return `${trimmed}/v1`;
}

function syncLegacyRelayFields(settings: BackendSettings): BackendSettings {
  const active = activeRelayProfile(settings);
  return {
    ...settings,
    activeRelayId: active.id,
    relayBaseUrl: active.baseUrl,
    relayApiKey: active.apiKey,
  };
}

function updateRelayProfile(settings: BackendSettings, id: string, patch: Partial<RelayProfile>): BackendSettings {
  const shouldRegenerateFiles = [
    "baseUrl",
    "apiKey",
    "protocol",
    "relayMode",
    "officialMixApiKey",
    "imageGenerationEnabled",
    "imageGenerationUseSeparateApi",
    "imageGenerationBaseUrl",
    "imageGenerationApiKey",
    "proxyEnabled",
    "proxyUrl",
  ].some((key) => key in patch);
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles: settings.relayProfiles.map((profile) => {
      if (profile.id !== id) return profile;
      const canonicalPatch = "baseUrl" in patch ? { ...patch, upstreamBaseUrl: patch.baseUrl ?? "" } : patch;
      const updated = { ...profile, ...canonicalPatch };
      const nextProfile = shouldRegenerateFiles ? withGeneratedRelayFiles(updated) : updated;
      return deriveOfficialAuthFields(nextProfile);
    }),
  });
}

function createRelayProfile(settings: BackendSettings): RelayProfile {
  const id = `relay-${Date.now().toString(36)}`;
  const contextSelection = contextSelectionForAllEntries(settings);
  const next = {
    id,
    linkedCcsProviderId: "",
    name: `供应商 ${settings.relayProfiles.length + 1}`,
    model: "",
    baseUrl: defaultSettings.relayBaseUrl,
    upstreamBaseUrl: defaultSettings.relayBaseUrl,
    apiKey: "",
    imageGenerationEnabled: false,
    imageGenerationUseSeparateApi: false,
    imageGenerationBaseUrl: "",
    imageGenerationApiKey: "",
    protocol: "responses" as RelayProtocol,
    relayMode: "official" as RelayMode,
    officialMixApiKey: false,
    officialAuthContents: "",
    officialAccountLabel: "",
    officialAuthUpdatedAt: "",
    hideOfficialUsageAlert: false,
    testModel: "",
    configContents: "",
    authContents: "",
    useCommonConfig: true,
    contextSelection,
    contextSelectionInitialized: true,
    contextWindow: "",
    autoCompactLimit: "",
    modelInsertMode: "patch",
    modelList: "",
    modelWindows: "",
    modelAutoCompact: "",
    modelRoutes: [],
    modelVlm: "",
    vlmApiKey: "",
    vlmModel: "",
    vlmBaseUrl: "",
    userAgent: "",
    sub2apiEnabled: false,
    sub2apiMultiplier: "",
    proxyEnabled: false,
    proxyUrl: "",
  };
  return deriveOfficialAuthFields(withGeneratedRelayFiles(next));
}

function createAggregateRelayProfile(settings: BackendSettings): RelayProfile {
  const profile = createRelayProfile(settings);
  return withGeneratedRelayFiles({
    ...profile,
    id: `aggregate-${Date.now().toString(36)}`,
    name: `聚合供应商 ${settings.aggregateRelayProfiles.length + 1}`,
    relayMode: "aggregate",
    protocol: "responses",
    baseUrl: PROTOCOL_PROXY_BASE_URL,
    upstreamBaseUrl: PROTOCOL_PROXY_BASE_URL,
    apiKey: "codex-plus-aggregate",
  });
}

function createRelayProfileFromPreset(settings: BackendSettings, preset: ProviderPreset): RelayProfile {
  const relayMode: RelayMode = preset.category === "official" ? "official" : "pureApi";
  const profile: RelayProfile = {
    ...createRelayProfile(settings),
    id: `preset-${preset.id}-${Date.now().toString(36)}`,
    name: preset.name,
    model: preset.model,
    baseUrl: preset.baseUrl,
    upstreamBaseUrl: preset.upstreamBaseUrl || preset.baseUrl,
    protocol: preset.protocol,
    relayMode,
    officialMixApiKey: false,
    testModel: preset.testModel || preset.model,
    modelList: (preset.modelList ?? []).join("\n"),
  };
  return deriveOfficialAuthFields(withGeneratedRelayFiles(profile));
}

function providerPresetCategoryLabel(category: ProviderPreset["category"]): string {
  switch (category) {
    case "official":
      return "官方";
    case "aggregator":
      return "聚合";
    case "cn_official":
      return "国内官方";
    case "third_party":
      return "兼容";
    default:
      return category;
  }
}

function addRelayProfile(settings: BackendSettings, profile: RelayProfile): BackendSettings {
  const nextWithFiles = profile.configContents.trim() || profile.authContents.trim() ? deriveOfficialAuthFields(profile) : deriveOfficialAuthFields(withGeneratedRelayFiles(profile));
  const activeId = settings.relayProfiles.some((item) => item.id === settings.activeRelayId)
    ? settings.activeRelayId
    : activeRelayProfile(settings).id;
  const aggregateRelayProfiles =
    nextWithFiles.relayMode === "aggregate" && !settings.aggregateRelayProfiles.some((item) => item.id === nextWithFiles.id)
      ? [...settings.aggregateRelayProfiles, defaultAggregateForProfile({ ...settings, relayProfiles: [...settings.relayProfiles, nextWithFiles] }, nextWithFiles)]
      : settings.aggregateRelayProfiles;
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles: [...settings.relayProfiles, nextWithFiles],
    aggregateRelayProfiles,
    activeRelayId: activeId,
  });
}

function duplicateRelayProfile(settings: BackendSettings, id: string): BackendSettings {
  const sourceIndex = settings.relayProfiles.findIndex((profile) => profile.id === id);
  const source = settings.relayProfiles[sourceIndex] || activeRelayProfile(settings);
  const nextId = `relay-${Date.now().toString(36)}`;
  const next = {
    ...source,
    id: nextId,
    name: `${source.name || "未命名供应商"} 副本`,
  };
  const relayProfiles = [...settings.relayProfiles];
  relayProfiles.splice(sourceIndex >= 0 ? sourceIndex + 1 : relayProfiles.length, 0, next);
  const aggregateRelayProfiles = source.relayMode === "aggregate"
    ? [
        ...settings.aggregateRelayProfiles,
        {
          ...aggregateRelayProfileFor(settings, source),
          id: nextId,
          name: next.name,
        },
      ]
    : settings.aggregateRelayProfiles;
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles: relayProfiles.map((profile) => deriveOfficialAuthFields(profile)),
    aggregateRelayProfiles,
  });
}

function reorderRelayProfiles(settings: BackendSettings, sourceId: string, targetId: string): BackendSettings {
  if (sourceId === targetId) return settings;
  const sourceIndex = settings.relayProfiles.findIndex((profile) => profile.id === sourceId);
  const targetIndex = settings.relayProfiles.findIndex((profile) => profile.id === targetId);
  if (sourceIndex < 0 || targetIndex < 0) return settings;
  const relayProfiles = [...settings.relayProfiles];
  const [moved] = relayProfiles.splice(sourceIndex, 1);
  relayProfiles.splice(targetIndex, 0, moved);
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles,
  });
}

function removeRelayProfile(settings: BackendSettings, id: string): BackendSettings {
  const profiles = settings.relayProfiles.filter((profile) => profile.id !== id);
  return syncLegacyRelayFields({
    ...settings,
    relayProfiles: profiles.length ? profiles : defaultSettings.relayProfiles,
    aggregateRelayProfiles: settings.aggregateRelayProfiles
      .filter((profile) => profile.id !== id)
      .map((profile) => ({ ...profile, members: profile.members.filter((member) => member.relayId !== id) })),
    activeAggregateRelayId: settings.activeAggregateRelayId === id ? "" : settings.activeAggregateRelayId,
    activeRelayId: settings.activeRelayId === id ? profiles[0]?.id || "default" : settings.activeRelayId,
  });
}

function deriveOfficialAuthFields(profile: RelayProfile): RelayProfile {
  const contents = profile.authContents.trim() || profile.officialAuthContents.trim();
  if (!contents) {
    return {
      ...profile,
      authContents: "",
      officialAuthContents: "",
      officialAccountLabel: "",
      officialAuthUpdatedAt: "",
    };
  }
  const label = decodeOfficialAccountLabel(contents);
  return {
    ...profile,
    authContents: contents,
    officialAuthContents: label ? contents : "",
    officialAccountLabel: label || "",
    officialAuthUpdatedAt: label ? profile.officialAuthUpdatedAt || new Date().toISOString() : "",
  };
}

function decodeOfficialAccountLabel(contents: string): string {
  try {
    const parsed = JSON.parse(contents) as Record<string, unknown>;
    if (String(parsed.auth_mode || "").toLowerCase() !== "chatgpt") return "";
    const tokens = parsed.tokens as Record<string, unknown> | undefined;
    if (!tokens) return "";
    for (const key of ["id_token", "access_token"] as const) {
      const raw = String(tokens[key] || "");
      const label = decodeJwtEmail(raw);
      if (label) return label;
    }
    return "";
  } catch {
    return "";
  }
}

function decodeJwtEmail(token: string): string {
  const parts = token.split(".");
  if (parts.length < 2) return "";
  try {
    const payload = JSON.parse(atob(parts[1].replace(/-/g, "+").replace(/_/g, "/"))) as Record<string, unknown>;
    const direct = String(payload.email || "").trim();
    if (direct) return direct;
    const profile = payload["https://api.openai.com/profile"] as Record<string, unknown> | undefined;
    return String(profile?.email || payload.name || "").trim();
  } catch {
    return "";
  }
}

function numberOrDefault(value: string, fallback: number) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function delay(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms));
}

function splitLogLines(text: string) {
  return text.trimEnd().split(/\r?\n/).filter((line, index, lines) => line.length > 0 || index < lines.length - 1);
}

function formatTime(value: number) {
  if (!value) return "-";
  return new Date(value).toLocaleString("zh-CN");
}

function stringifyError(error: unknown) {
  if (error instanceof Error) return error.message;
  return String(error);
}

function loadInitialTheme(): Theme {
  if (typeof window === "undefined") return "light";
  return window.localStorage.getItem("codex-plus-theme") === "dark" ? "dark" : "light";
}

function loadInitialRoute(): Route {
  if (typeof window === "undefined") return "overview";
  return "overview";
}
