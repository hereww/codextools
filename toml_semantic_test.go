package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestNormalizeDuplicateTomlTablesSemanticallyMergesRepeatedTables(t *testing.T) {
	input := `[features]
goals = false
profile_only = true

[features]
goals = true
web_search = true
`

	got := normalizeDuplicateTomlTables(input)
	if strings.Count(got, "[features]") != 1 {
		t.Fatalf("expected one features table after merge:\n%s", got)
	}
	features := tableValues(got, "features")
	if features["goals"] != "true" || features["profile_only"] != "true" || features["web_search"] != "true" {
		t.Fatalf("merged features did not preserve the union with last-value-wins: %#v\n%s", features, got)
	}
}

func TestNormalizeDuplicateTomlTablesPreservesMCPParentAndNestedEnv(t *testing.T) {
	input := `[mcp_servers.node_repl.env]
NODE_OPTIONS = "--trace-warnings"

[mcp_servers]
enabled = true

[mcp_servers.node_repl.env]
DEBUG = "1"
`

	got := normalizeDuplicateTomlTables(input)
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("merged config is not valid TOML: %v\n%s", err, got)
	}
	servers := decoded["mcp_servers"].(map[string]any)
	nodeRepl := servers["node_repl"].(map[string]any)
	env := nodeRepl["env"].(map[string]any)
	if env["NODE_OPTIONS"] != "--trace-warnings" || env["DEBUG"] != "1" || servers["enabled"] != true {
		t.Fatalf("MCP parent/child values were lost: %#v", decoded)
	}
}

func TestNormalizeDuplicateTomlRootKeyLastValueWinsAndRetainsMultilineData(t *testing.T) {
	input := `model = "old-model"
permissions = [
  "read",
  "write",
]
model = "new-model"
`

	got := normalizeDuplicateTomlTables(input)
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("merged root config is not valid TOML: %v\n%s", err, got)
	}
	if decoded["model"] != "new-model" {
		t.Fatalf("later root value did not win: %#v", decoded["model"])
	}
	if !reflect.DeepEqual(decoded["permissions"], []any{"read", "write"}) {
		t.Fatalf("multiline root value changed: %#v", decoded["permissions"])
	}
}

func TestNormalizeDuplicateTomlTablesKeepsArrayTablesAndValidSourceFormatting(t *testing.T) {
	input := "# retained comment\n[[hooks]]\ncommand = \"first\"\n\n[[hooks]]\ncommand = \"second\"\n"
	got := normalizeDuplicateTomlTables(input)
	if got != input {
		t.Fatalf("valid config without duplicate table definitions should retain its text:\n got: %q\nwant: %q", got, input)
	}
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("array tables are not valid TOML: %v", err)
	}
	hooks := decoded["hooks"].([]any)
	if len(hooks) != 2 {
		t.Fatalf("repeated array table entries were collapsed: %#v", hooks)
	}
}

func TestNormalizeDuplicateTomlTablesScopesNestedTablesToTheirArrayEntry(t *testing.T) {
	input := `[[parent]]
id = 1
[[parent.children]]
value = 10
[parent.settings]
left = true
[parent.settings]
right = true
[[parent]]
id = 2
[[parent.children]]
value = 20
[parent.settings]
left = false
`

	got := normalizeDuplicateTomlTables(input)
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("merged nested array-table config is invalid: %v\n%s", err, got)
	}
	parents := decoded["parent"].([]any)
	if len(parents) != 2 {
		t.Fatalf("parent array entries were changed: %#v", parents)
	}
	first := parents[0].(map[string]any)
	second := parents[1].(map[string]any)
	firstSettings := first["settings"].(map[string]any)
	secondSettings := second["settings"].(map[string]any)
	if firstSettings["left"] != true || firstSettings["right"] != true || secondSettings["left"] != false {
		t.Fatalf("nested settings leaked across array entries: %#v", parents)
	}
	if len(first["children"].([]any)) != 1 || len(second["children"].([]any)) != 1 {
		t.Fatalf("nested child array tables changed: %#v", parents)
	}
}

func TestNormalizeDuplicateTomlTablesIsIdempotent(t *testing.T) {
	input := `[mcp_servers.tool]
command = "one"
[mcp_servers.tool]
args = ["two"]
`
	first := normalizeDuplicateTomlTables(input)
	second := normalizeDuplicateTomlTables(first)
	if second != first {
		t.Fatalf("normalization is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestNormalizeDuplicateTomlTablesLeavesUnparseableInputUntouched(t *testing.T) {
	input := "[broken\nkey = 1\n[broken\nother = 2"
	if got := normalizeDuplicateTomlTables(input); got != input {
		t.Fatalf("unparseable input was modified:\n got: %q\nwant: %q", got, input)
	}
}

func TestPreserveLiveHookStateAndRemoveStaleProfileState(t *testing.T) {
	target := `[features]
goals = true

[hooks]
state = false
`
	live := `[hooks]
state = true
`
	got := preserveLiveHookState(target, live)
	if state := tableValues(got, "hooks")["state"]; state != "true" {
		t.Fatalf("live hook state was not preserved: %q\n%s", state, got)
	}
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("preserved config is invalid TOML: %v\n%s", err, got)
	}

	withoutLiveState := `[hooks]
`
	got = preserveLiveHookState(target, withoutLiveState)
	if _, exists := tableValues(got, "hooks")["state"]; exists {
		t.Fatalf("stale profile hook state should be removed when live config has none:\n%s", got)
	}
}
