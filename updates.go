package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func (s *server) checkUpdate(ctx context.Context) commandResult {
	payload, err := latestCodexToolsUpdate(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		payload["updateStatus"] = "failed"
		payload["message"] = "检查更新失败：" + err.Error()
		return failed("检查更新失败："+err.Error(), payload)
	}
	if stringFromAny(payload["updateStatus"]) == "available" {
		return ok("发现新版本 "+stringFromAny(payload["latestVersion"])+"。", payload)
	}
	if stringFromAny(payload["updateStatus"]) == "up_to_date" {
		return ok("当前已是最新版本。", payload)
	}
	return ok(stringFromAny(payload["message"]), payload)
}

func (s *server) installUpdate(ctx context.Context) commandResult {
	update, err := latestCodexToolsUpdate(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		update["updateStatus"] = "failed"
		update["message"] = "获取更新失败：" + err.Error()
		return failed("获取更新失败："+err.Error(), update)
	}
	if stringFromAny(update["updateStatus"]) != "available" {
		message := stringFromAny(update["message"])
		if message == "" {
			message = "当前没有可安装的新版本。"
		}
		return ok(message, update)
	}
	downloadURL := stringFromAny(update["downloadUrl"])
	if downloadURL == "" {
		if releaseURL := stringFromAny(update["releaseUrl"]); releaseURL != "" {
			if err := openURL(releaseURL); err != nil {
				update["updateStatus"] = "failed"
				update["message"] = "打开发布页面失败：" + err.Error()
				return failed("打开发布页面失败："+err.Error(), update)
			}
			update["updateStatus"] = "opened_release"
			update["message"] = "已打开发布页面，请选择当前系统的安装包。"
			return ok("已打开发布页面，请选择当前系统的安装包。", update)
		}
		update["updateStatus"] = "failed"
		update["message"] = "最新版本没有可下载地址。"
		return failed("最新版本没有可下载地址。", update)
	}
	data, err := getBytes(ctx, downloadURL)
	if err != nil {
		update["updateStatus"] = "failed"
		update["message"] = "下载更新失败：" + err.Error()
		return failed("下载更新失败："+err.Error(), update)
	}
	target := filepath.Join(downloadsDir(), safeUpdateFilename(stringFromAny(update["assetName"]), stringFromAny(update["latestVersion"])))
	if err := atomicWrite(target, data); err != nil {
		update["updateStatus"] = "failed"
		update["message"] = "保存更新包失败：" + err.Error()
		return failed("保存更新包失败："+err.Error(), update)
	}
	update["downloadedPath"] = target
	if err := openPath(target); err != nil {
		update["updateStatus"] = "downloaded"
		update["message"] = "更新包已下载到 " + target + "，但自动打开失败：" + err.Error()
		return ok("更新包已下载到 "+target+"，但自动打开失败："+err.Error(), update)
	}
	update["updateStatus"] = "downloaded"
	update["message"] = "更新包已下载并打开，请按系统提示替换当前版本。"
	return ok("更新包已下载并打开，请按系统提示替换当前版本。", update)
}

func latestCodexToolsUpdate(ctx context.Context, goos, goarch string) (map[string]any, error) {
	payload := map[string]any{
		"updateStatus":   "not_checked",
		"currentVersion": version,
		"projectUrl":     codexToolsProjectURL,
		"releaseUrl":     codexToolsReleaseURL,
		"platform":       goos,
		"arch":           goarch,
	}
	release, err := getJSON[codexToolsRelease](ctx, codexToolsLatestAPIURL)
	if err != nil {
		return payload, err
	}
	latest := cleanVersion(firstString(release.TagName, release.Name))
	payload["latestVersion"] = latest
	payload["releaseName"] = release.Name
	payload["tagName"] = release.TagName
	payload["publishedAt"] = release.PublishedAt
	if release.HTMLURL != "" {
		payload["releaseUrl"] = release.HTMLURL
	}
	if latest == "" {
		payload["updateStatus"] = "failed"
		payload["message"] = "最新发布没有版本号。"
		return payload, nil
	}
	if compareVersions(latest, version) <= 0 {
		payload["updateStatus"] = "up_to_date"
		payload["message"] = "当前已是最新版本。"
		return payload, nil
	}
	asset, ok := selectCodexToolsAsset(release.Assets, goos, goarch)
	if !ok {
		payload["updateStatus"] = "missing_asset"
		payload["message"] = "发现新版本，但没有找到当前系统对应安装包。"
		return payload, nil
	}
	payload["updateStatus"] = "available"
	payload["assetName"] = asset.Name
	payload["downloadUrl"] = asset.BrowserDownloadURL
	payload["size"] = asset.Size
	payload["contentType"] = asset.ContentType
	payload["message"] = "发现新版本，可下载更新包。"
	return payload, nil
}

func selectCodexToolsAsset(assets []codexAppMirrorAsset, goos, goarch string) (codexAppMirrorAsset, bool) {
	var best codexAppMirrorAsset
	bestScore := -1_000_000
	for _, asset := range assets {
		if strings.TrimSpace(asset.BrowserDownloadURL) == "" {
			continue
		}
		score := codexToolsAssetScore(asset.Name, goos, goarch)
		if score > bestScore {
			best = asset
			bestScore = score
		}
	}
	return best, bestScore > 0
}

func codexToolsAssetScore(name, goos, goarch string) int {
	lower := strings.ToLower(name)
	score := 0
	if !strings.Contains(lower, "codextools") {
		score -= 20
	}
	switch goos {
	case "darwin":
		if strings.Contains(lower, "macos") || strings.Contains(lower, "darwin") {
			score += 40
		}
	case "windows":
		if strings.Contains(lower, "windows") || strings.Contains(lower, "win") {
			score += 40
		}
		if strings.Contains(lower, "setup") || strings.Contains(lower, "installer") {
			score += 20
		}
	default:
		if strings.Contains(lower, goos) {
			score += 30
		}
	}
	switch goarch {
	case "arm64":
		if strings.Contains(lower, "arm64") || strings.Contains(lower, "aarch64") {
			score += 25
		}
	case "amd64":
		if strings.Contains(lower, "x64") || strings.Contains(lower, "amd64") || strings.Contains(lower, "x86_64") {
			score += 25
		}
	}
	if strings.HasSuffix(lower, ".zip") {
		score += 12
	}
	if strings.HasSuffix(lower, ".exe") {
		score += 18
	}
	if strings.Contains(lower, "sha256") || strings.Contains(lower, "manifest") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".json") {
		score -= 100
	}
	return score
}

func compareVersions(a, b string) int {
	left := versionParts(a)
	right := versionParts(b)
	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}
	for i := 0; i < maxLen; i++ {
		var lv, rv int
		if i < len(left) {
			lv = left[i]
		}
		if i < len(right) {
			rv = right[i]
		}
		if lv > rv {
			return 1
		}
		if lv < rv {
			return -1
		}
	}
	return 0
}

func versionParts(value string) []int {
	value = cleanVersion(value)
	var parts []int
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+'
	}) {
		if part == "" {
			continue
		}
		number := strings.Builder{}
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			number.WriteRune(r)
		}
		if number.Len() == 0 {
			continue
		}
		parsed, err := strconv.Atoi(number.String())
		if err != nil {
			continue
		}
		parts = append(parts, parsed)
	}
	return parts
}

func cleanVersion(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "release ")
	value = strings.TrimPrefix(value, "codextools ")
	value = strings.TrimPrefix(value, "v")
	return strings.TrimSpace(value)
}

func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return stateDir()
	}
	path := filepath.Join(home, "Downloads")
	if isDir(path) {
		return path
	}
	return stateDir()
}

func safeUpdateFilename(assetName, latestVersion string) string {
	name := filepath.Base(strings.TrimSpace(assetName))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = fmt.Sprintf("CodexTools-%s-%s-%s.zip", cleanVersion(latestVersion), runtime.GOOS, runtime.GOARCH)
	}
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	return name
}
