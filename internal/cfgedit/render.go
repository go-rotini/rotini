package cfgedit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-rotini/yaml"
)

// formatNumber renders an integer or float the way every format reads it back as the same
// number. A float always shows a fraction or exponent, so it never reads back as an integer.
func formatNumber(v any) (string, bool) {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s, true
	}
	return "", false
}

// yamlScalar renders a scalar for YAML. flow says whether it sits inside a flow collection,
// where more characters need quoting.
func yamlScalar(v any, flow bool) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case string:
		if yamlPlainOK(x, flow) {
			return x
		}
		return strconv.Quote(x)
	}
	s, _ := formatNumber(v)
	return s
}

// yamlPlainOK reports whether s can be written unquoted and read back as the same string.
func yamlPlainOK(s string, flow bool) bool {
	if s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\"'") || strings.ContainsFunc(s, unicode.IsControl) {
		return false
	}
	doc := "k: " + s
	if flow {
		doc = "{k: " + s + "}"
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(doc), &got); err != nil {
		return false
	}
	back, ok := got["k"].(string)
	return ok && back == s && len(got) == 1
}

// yamlKey renders a mapping key, quoting it when it wouldn't read back as itself.
func yamlKey(k string) string {
	if k != "" && strings.TrimSpace(k) == k && !strings.ContainsAny(k, "\"'") && !strings.ContainsFunc(k, unicode.IsControl) {
		var got map[string]any
		if yaml.Unmarshal([]byte(k+": 1"), &got) == nil && len(got) == 1 {
			if _, ok := got[k]; ok {
				return k
			}
		}
	}
	return strconv.Quote(k)
}

// yamlFlow renders a list or map in flow style: [a, b] or {k: v}.
func yamlFlow(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = yamlScalar(item, true)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = yamlKey(k) + ": " + yamlScalar(x[k], true)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return yamlScalar(v, true)
}

// yamlBlock renders a non-empty list or map as block lines at col, each ending in nl.
func yamlBlock(v any, col int, nl string) string {
	var b strings.Builder
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			for _, part := range []string{spaces(col), "- ", yamlScalar(item, false), nl} {
				b.WriteString(part)
			}
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			for _, part := range []string{spaces(col), yamlKey(k), ": ", yamlScalar(x[k], false), nl} {
				b.WriteString(part)
			}
		}
	}
	return b.String()
}

// isCollection reports whether v is a non-empty list or map, which YAML writes as a block.
func isCollection(v any) bool {
	switch x := v.(type) {
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return false
}

// jsonValue renders v as compact JSON text with a space after each comma and colon.
func jsonValue(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = jsonValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = jsonString(k) + ": " + jsonValue(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case string:
		return jsonString(x)
	case bool:
		return strconv.FormatBool(x)
	}
	s, _ := formatNumber(v)
	return s
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return string(b)
}

// scalarText renders a scalar as plain text, for dotenv.
func scalarText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	if s, ok := formatNumber(v); ok {
		return s, nil
	}
	return "", fmt.Errorf("a dotenv value must be a scalar, not %T", v)
}
