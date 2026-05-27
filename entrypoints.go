package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

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
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	hideSubprocessWindow(cmd)
	return cmd.Run()
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
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	hideSubprocessWindow(cmd)
	return cmd.Run()
}

func windowsRegAddCurrentUserString(key, name, value string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows registry is only supported on Windows")
	}
	cmd := exec.Command("reg", "add", key, "/v", name, "/t", "REG_SZ", "/d", value, "/f")
	hideSubprocessWindow(cmd)
	return cmd.Run()
}

func windowsRegDeleteCurrentUserValue(key, name string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows registry is only supported on Windows")
	}
	cmd := exec.Command("reg", "delete", key, "/v", name, "/f")
	hideSubprocessWindow(cmd)
	return cmd.Run()
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
	hideSubprocessWindow(cmd)
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
