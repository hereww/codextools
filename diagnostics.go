package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
