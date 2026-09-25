package main

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

func normalizeDuplicateTomlDocument(contents string) string {
	bom, contents := splitTomlBOM(contents)
	parser := &unstable.Parser{}
	parser.Reset([]byte(contents))
	if !tomlHasDuplicateExpressions(parser) {
		if parser.Error() != nil {
			return bom + contents
		}
		return bom + normalizeConfigText(contents)
	}
	if parser.Error() != nil {
		return bom + contents
	}

	// Re-serialization is limited to documents with duplicate expressions.
	// Comments and formatting in those documents may be normalized; any parse or
	// merge failure leaves the original text untouched.
	root, err := mergeDuplicateTomlExpressions(contents)
	if err != nil {
		return bom + contents
	}
	data, err := toml.Marshal(root)
	if err != nil {
		return bom + contents
	}
	var verified map[string]any
	if err := toml.Unmarshal(data, &verified); err != nil {
		return bom + contents
	}
	return bom + normalizeConfigText(string(data))
}

func tomlHasDuplicateExpressions(parser *unstable.Parser) bool {
	seenTables := map[string]bool{}
	seenKeys := map[string]bool{}
	var currentTable []string
	var arrayScopes []tomlArrayTableScope
	nextArrayTableScope := 0

	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table:
			currentTable = tomlExpressionKey(expression)
			arrayScopes = activeTomlArrayScopes(arrayScopes, currentTable, false)
			if key := scopedTomlPath(arrayScopes, currentTable); seenTables[key] {
				return true
			} else {
				seenTables[key] = true
			}
		case unstable.ArrayTable:
			currentTable = tomlExpressionKey(expression)
			arrayScopes = activeTomlArrayScopes(arrayScopes, currentTable, true)
			nextArrayTableScope++
			arrayScopes = append(arrayScopes, tomlArrayTableScope{path: append([]string{}, currentTable...), id: nextArrayTableScope})
		case unstable.KeyValue:
			keys := append(append([]string{}, currentTable...), tomlExpressionKey(expression)...)
			key := scopedTomlPath(arrayScopes, keys)
			if seenKeys[key] {
				return true
			}
			seenKeys[key] = true
		}
	}
	return false
}

type tomlArrayTableScope struct {
	path []string
	id   int
}

func activeTomlArrayScopes(scopes []tomlArrayTableScope, path []string, startingArrayTable bool) []tomlArrayTableScope {
	active := scopes[:0]
	for _, scope := range scopes {
		prefixMatches := tomlPathHasPrefix(path, scope.path)
		if startingArrayTable && len(path) == len(scope.path) {
			prefixMatches = false
		}
		if prefixMatches {
			active = append(active, scope)
		}
	}
	return active
}

func mergeDuplicateTomlExpressions(contents string) (map[string]any, error) {
	root := map[string]any{}
	parser := &unstable.Parser{}
	parser.Reset([]byte(contents))
	var currentTable []string

	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table:
			currentTable = tomlExpressionKey(expression)
			if _, err := ensureTomlTable(root, currentTable); err != nil {
				return nil, err
			}
		case unstable.ArrayTable:
			currentTable = tomlExpressionKey(expression)
			if err := appendTomlArrayTable(root, currentTable); err != nil {
				return nil, err
			}
		case unstable.KeyValue:
			var decoded map[string]any
			if err := toml.Unmarshal(parser.Raw(expression.Raw), &decoded); err != nil {
				return nil, err
			}
			table, err := ensureTomlTable(root, currentTable)
			if err != nil {
				return nil, err
			}
			mergeTomlMaps(table, decoded)
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	return root, nil
}

func tomlExpressionKey(expression *unstable.Node) []string {
	if expression == nil {
		return nil
	}
	iterator := expression.Key()
	var path []string
	for iterator.Next() {
		path = append(path, string(iterator.Node().Data))
	}
	return path
}

func scopedTomlPath(scopes []tomlArrayTableScope, path []string) string {
	var scopeIDs []string
	for _, scope := range scopes {
		if tomlPathHasPrefix(path, scope.path) {
			scopeIDs = append(scopeIDs, fmt.Sprint(scope.id))
		}
	}
	return strings.Join(scopeIDs, "/") + "\x00" + strings.Join(path, "\x00")
}

func tomlPathHasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}

func ensureTomlTable(root map[string]any, path []string) (map[string]any, error) {
	current := root
	for _, part := range path {
		value, exists := current[part]
		if !exists {
			nested := map[string]any{}
			current[part] = nested
			current = nested
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			current = typed
		case []any:
			if len(typed) == 0 {
				return nil, fmt.Errorf("TOML 数组表 %q 没有元素", part)
			}
			nested, ok := typed[len(typed)-1].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("TOML 数组表 %q 结构无效", part)
			}
			current = nested
		default:
			return nil, fmt.Errorf("TOML 表路径 %q 与现有值冲突", part)
		}
	}
	return current, nil
}

func appendTomlArrayTable(root map[string]any, path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("TOML 数组表路径为空")
	}
	parent, err := ensureTomlTable(root, path[:len(path)-1])
	if err != nil {
		return err
	}
	name := path[len(path)-1]
	value, exists := parent[name]
	if !exists {
		value = []any{}
	}
	array, ok := value.([]any)
	if !ok {
		return fmt.Errorf("TOML 数组表 %q 与现有值冲突", name)
	}
	parent[name] = append(array, map[string]any{})
	return nil
}

func mergeTomlMaps(target, source map[string]any) {
	for key, value := range source {
		if sourceMap, ok := value.(map[string]any); ok {
			if targetMap, ok := target[key].(map[string]any); ok {
				mergeTomlMaps(targetMap, sourceMap)
				continue
			}
		}
		target[key] = value
	}
}
