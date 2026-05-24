package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

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
