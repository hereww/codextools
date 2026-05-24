package main

import "strings"

func rootKeyString(contents, key string) string {
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			return ""
		}
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(left) == key {
			return unquoteToml(right)
		}
	}
	return ""
}

func upsertRootKey(contents, key, value string) string {
	lines := splitLines(contents)
	rootEnd := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			rootEnd = i
			break
		}
	}
	for i := 0; i < rootEnd; i++ {
		if rootLineKey(lines[i]) == key {
			lines[i] = key + " = " + value
			return ensureTrailingNewline(strings.Join(lines, "\n"))
		}
	}
	lines = append(lines[:rootEnd], append([]string{key + " = " + value}, lines[rootEnd:]...)...)
	return ensureTrailingNewline(strings.Join(lines, "\n"))
}

func removeRootKey(contents, key string) string {
	var lines []string
	inRoot := true
	for _, line := range splitLines(contents) {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			inRoot = false
		}
		if inRoot && rootLineKey(line) == key {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func rootLineKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	left, _, ok := strings.Cut(trimmed, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(left)
}

func tableValues(contents, table string) map[string]string {
	values := map[string]string{}
	header := "[" + table + "]"
	inTable := false
	for _, line := range splitLines(contents) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inTable {
				break
			}
			inTable = trimmed == header
			continue
		}
		if !inTable || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		left, right, ok := strings.Cut(trimmed, "=")
		if ok {
			values[strings.TrimSpace(left)] = strings.TrimSpace(right)
		}
	}
	return values
}

func removeTable(contents, table string) string {
	header := "[" + table + "]"
	var lines []string
	skipping := false
	for _, line := range splitLines(contents) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == header {
				skipping = true
				continue
			}
			skipping = false
		}
		if !skipping {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func unquoteToml(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, `"`)
	value = strings.TrimSuffix(value, `"`)
	return value
}

func quoteToml(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func tomlEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func normalizeResponsesBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" || baseURLHasPathAfterHost(trimmed) {
		return trimmed
	}
	return trimmed + "/v1"
}

func baseURLHasPathAfterHost(baseURL string) bool {
	after := baseURL
	if parts := strings.SplitN(baseURL, "://", 2); len(parts) == 2 {
		after = parts[1]
	}
	_, path, ok := strings.Cut(after, "/")
	return ok && strings.Trim(path, "/") != ""
}

func splitLines(contents string) []string {
	contents = strings.ReplaceAll(contents, "\r\n", "\n")
	if contents == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
}

func ensureTrailingNewline(value string) string {
	if !strings.HasSuffix(value, "\n") {
		return value + "\n"
	}
	return value
}
