package main

import (
	"fmt"
	"sort"
	"strings"
)

type codexContextEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	TomlBody string `json:"tomlBody"`
	Enabled  bool   `json:"enabled"`
}

type codexContextEntries struct {
	MCPServers []codexContextEntry `json:"mcpServers"`
	Skills     []codexContextEntry `json:"skills"`
	Plugins    []codexContextEntry `json:"plugins"`
}

func emptyCodexContextEntries() codexContextEntries {
	return codexContextEntries{
		MCPServers: []codexContextEntry{},
		Skills:     []codexContextEntry{},
		Plugins:    []codexContextEntry{},
	}
}

func splitContextConfigSections(config string) (string, string) {
	var common []string
	var context []string
	inContext := false
	for _, line := range splitLines(config) {
		trimmed := strings.TrimSpace(line)
		if isTomlHeader(trimmed) {
			inContext = isContextTableHeader(trimmed)
		}
		if inContext {
			context = append(context, line)
		} else {
			common = append(common, line)
		}
	}
	return normalizeConfigText(strings.Join(common, "\n")), normalizeConfigText(strings.Join(context, "\n"))
}

func joinConfigSections(sections ...string) string {
	var parts []string
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section != "" {
			parts = append(parts, section)
		}
	}
	return normalizeConfigText(strings.Join(parts, "\n\n"))
}

func joinConfigSectionsRootFirst(sections ...string) string {
	var rootParts []string
	var tableParts []string
	for _, section := range sections {
		root, tables := splitTomlRootAndTables(section)
		if strings.TrimSpace(root) != "" {
			rootParts = append(rootParts, root)
		}
		if strings.TrimSpace(tables) != "" {
			tableParts = append(tableParts, tables)
		}
	}
	return normalizeDuplicateTomlTables(joinConfigSections(append(dedupeRootTomlLines(rootParts), tableParts...)...))
}

func splitTomlRootAndTables(section string) (string, string) {
	lines := splitLines(section)
	firstTable := -1
	for index, line := range lines {
		if isTomlHeader(strings.TrimSpace(line)) {
			firstTable = index
			break
		}
	}
	if firstTable < 0 {
		return strings.Join(lines, "\n"), ""
	}
	return strings.Join(lines[:firstTable], "\n"), strings.Join(lines[firstTable:], "\n")
}

func dedupeRootTomlLines(parts []string) []string {
	var lines []string
	for _, part := range parts {
		lines = append(lines, splitLines(part)...)
	}
	seen := map[string]bool{}
	var kept []string
	for _, line := range lines {
		key := rootLineKey(line)
		if key != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		kept = append(kept, line)
	}
	text := strings.TrimSpace(strings.Join(kept, "\n"))
	if text == "" {
		return nil
	}
	return []string{text}
}

func normalizeDuplicateTomlTables(contents string) string {
	return normalizeDuplicateTomlDocument(contents)
}

func normalizeConfigText(config string) string {
	config = strings.TrimSpace(strings.ReplaceAll(config, "\r\n", "\n"))
	if config == "" {
		return ""
	}
	return config + "\n"
}

func isTomlHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

func isContextTableHeader(header string) bool {
	table := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(header), "["), "]")
	return strings.HasPrefix(table, "mcp_servers.") ||
		strings.HasPrefix(table, "skills.") ||
		strings.HasPrefix(table, "plugins.")
}

func contextSelectionForAllEntries(config string) relayContextSelection {
	entries := listContextEntriesFromConfig(config)
	return relayContextSelection{
		MCPServers: contextEntryIDs(entries.MCPServers),
		Skills:     contextEntryIDs(entries.Skills),
		Plugins:    contextEntryIDs(entries.Plugins),
	}
}

func normalizeContextSelection(selection relayContextSelection) relayContextSelection {
	return relayContextSelection{
		MCPServers: uniqueStrings(selection.MCPServers),
		Skills:     uniqueStrings(selection.Skills),
		Plugins:    uniqueStrings(selection.Plugins),
	}
}

func listContextEntriesFromConfig(config string) codexContextEntries {
	return codexContextEntries{
		MCPServers: parseContextEntries(config, "mcp", "mcp_servers"),
		Skills:     parseContextEntries(config, "skill", "skills"),
		Plugins:    parseContextEntries(config, "plugin", "plugins"),
	}
}

func parseContextEntries(config, kind, tableName string) []codexContextEntry {
	lines := splitLines(config)
	entries := []codexContextEntry{}
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if !isTomlHeader(trimmed) {
			i++
			continue
		}
		table := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
		parts := splitTomlTablePath(table)
		if len(parts) != 2 || parts[0] != tableName {
			i++
			continue
		}
		id := unquoteToml(parts[1])
		start := i + 1
		i++
		for i < len(lines) && !isTomlHeader(strings.TrimSpace(lines[i])) {
			i++
		}
		bodyLines := append([]string{}, lines[start:i]...)
		body := normalizeConfigText(strings.Join(bodyLines, "\n"))
		entries = append(entries, codexContextEntry{
			ID:       id,
			Kind:     kind,
			Title:    id,
			Summary:  contextEntrySummary(body),
			TomlBody: body,
			Enabled:  contextEntryEnabled(body),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

func splitTomlTablePath(table string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, ch := range table {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			current.WriteRune(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			current.WriteRune(ch)
			inQuote = !inQuote
			continue
		}
		if ch == '.' && !inQuote {
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts
}

func contextEntrySummary(body string) string {
	for _, line := range splitLines(body) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "enabled") {
			continue
		}
		return trimmed
	}
	return "已配置"
}

func contextEntryEnabled(body string) bool {
	for _, line := range splitLines(body) {
		left, right, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(left) != "enabled" {
			continue
		}
		value := strings.TrimSpace(strings.ToLower(unquoteToml(right)))
		return value != "false" && value != "0"
	}
	return true
}

func contextEntryIDs(entries []codexContextEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

func filterCommonConfigForSelection(config string, selection relayContextSelection) string {
	selected := map[string]map[string]bool{
		"mcp_servers": stringSet(selection.MCPServers),
		"skills":      stringSet(selection.Skills),
		"plugins":     stringSet(selection.Plugins),
	}
	lines := splitLines(config)
	var out []string
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if !isTomlHeader(trimmed) {
			out = append(out, lines[i])
			i++
			continue
		}
		table := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
		parts := splitTomlTablePath(table)
		start := i
		i++
		for i < len(lines) && !isTomlHeader(strings.TrimSpace(lines[i])) {
			i++
		}
		if len(parts) == 2 {
			if ids, ok := selected[parts[0]]; ok {
				id := unquoteToml(parts[1])
				if !ids[id] {
					continue
				}
				body := strings.Join(lines[start+1:i], "\n")
				if !contextEntryEnabled(body) {
					continue
				}
			}
		}
		out = append(out, lines[start:i]...)
	}
	return normalizeConfigText(strings.Join(out, "\n"))
}

func upsertContextEntryInConfig(config, kind, id, body string) (string, error) {
	tableName, err := contextKindTable(kind)
	if err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("上下文 id 不能为空")
	}
	config = deleteContextEntryFromConfig(config, kind, id)
	header := "[" + tableName + "." + quoteToml(id) + "]"
	return joinConfigSections(config, header+"\n"+strings.TrimSpace(body)), nil
}

func deleteContextEntryFromConfig(config, kind, id string) string {
	tableName, err := contextKindTable(kind)
	if err != nil {
		return normalizeConfigText(config)
	}
	target := tableName + "." + quoteToml(strings.TrimSpace(id))
	return removeTomlTablesMatching(config, func(table string) bool { return table == target })
}

func contextKindTable(kind string) (string, error) {
	switch strings.TrimSpace(kind) {
	case "mcp", "mcp_server", "mcpServers":
		return "mcp_servers", nil
	case "skill", "skills":
		return "skills", nil
	case "plugin", "plugins":
		return "plugins", nil
	default:
		return "", fmt.Errorf("未知上下文类型：%s", kind)
	}
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}
