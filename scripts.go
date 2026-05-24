package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
