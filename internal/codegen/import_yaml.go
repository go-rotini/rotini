package codegen

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/go-rotini/yaml"
)

// yamlDoc is a small block-style YAML writer for the spec an import builds. Keys are written
// in the order they are added; comments sit on their own lines above the entry they describe.
type yamlDoc struct {
	b bytes.Buffer
}

// yamlMap is an ordered mapping under construction.
type yamlMap struct {
	pairs    []yamlPair
	comments []string // written above the mapping's first key
}

type yamlPair struct {
	key   string
	value any // string, bool, json.Number, int, []any, *yamlMap
}

func newYAMLMap() *yamlMap { return &yamlMap{} }

// put writes parts in order.
func (d *yamlDoc) put(parts ...string) {
	for _, p := range parts {
		d.b.WriteString(p)
	}
}

// set adds key with value, skipping zero values (empty strings and lists, false, nil maps).
func (m *yamlMap) set(key string, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		if v == "" {
			return
		}
	case bool:
		if !v {
			return
		}
	case []any:
		if len(v) == 0 {
			return
		}
	case []string:
		if len(v) == 0 {
			return
		}
		items := make([]any, len(v))
		for i, s := range v {
			items[i] = s
		}
		value = items
	case *yamlMap:
		if v == nil {
			return
		}
	}
	m.pairs = append(m.pairs, yamlPair{key: key, value: value})
}

// writeMap writes m's comments, then its pairs, at indent.
func (d *yamlDoc) writeMap(m *yamlMap, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, c := range m.comments {
		d.put(pad, "# ", c, "\n")
	}
	for _, p := range m.pairs {
		d.put(pad, p.key, ":")
		d.writeValue(p.value, indent)
	}
}

// writeValue writes a value that follows "key:" on the current line.
func (d *yamlDoc) writeValue(v any, indent int) {
	switch t := v.(type) {
	case *yamlMap:
		d.b.WriteString("\n")
		d.writeMap(t, indent+2)
	case []any:
		d.b.WriteString("\n")
		pad := strings.Repeat(" ", indent+2)
		for _, item := range t {
			if m, ok := item.(*yamlMap); ok {
				for _, c := range m.comments {
					d.put(pad, "# ", c, "\n")
				}
				d.put(pad, "- ")
				inner := *m
				inner.comments = nil
				var sub yamlDoc
				sub.writeMap(&inner, indent+4)
				d.b.WriteString(strings.TrimPrefix(sub.b.String(), strings.Repeat(" ", indent+4)))
				continue
			}
			d.put(pad, "-")
			d.writeScalar(item, indent+2)
		}
	default:
		d.writeScalar(v, indent)
	}
}

// writeScalar writes " <scalar>\n": plain when the text reads back as the same string, a
// literal block for multi-line text, else double-quoted.
func (d *yamlDoc) writeScalar(v any, indent int) {
	switch t := v.(type) {
	case bool:
		if t {
			d.b.WriteString(" true\n")
		} else {
			d.b.WriteString(" false\n")
		}
		return
	case json.Number:
		d.put(" ", t.String(), "\n")
		return
	case int:
		d.put(" ", yamlJSONText(t), "\n")
		return
	case string:
		d.put(" ", yamlString(t, indent), "\n")
		return
	}
	d.put(" ", yamlJSONText(v), "\n")
}

// yamlString renders s as a YAML scalar at indent.
func yamlString(s string, indent int) string {
	if !strings.ContainsAny(s, "\n\r\t") && plainReadsBack(s) {
		return s
	}
	if lit := literalBlock(s, indent); lit != "" {
		return lit
	}
	return yamlJSONText(s)
}

// plainReadsBack reports whether s, written plain as a mapping value, reads back as the
// string s (not a number, bool, null, or a different string).
func plainReadsBack(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return false
	}
	switch strings.ToLower(s) {
	case "yes", "no", "on", "off", "y", "n":
		return false // YAML 1.1 readers take these as booleans
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte("k: "+s+"\n"), &got); err != nil {
		return false
	}
	str, ok := got["k"].(string)
	return ok && str == s
}

// literalBlock renders multi-line s as a "|-" block at indent, or "" when a block can't
// carry it exactly.
func literalBlock(s string, indent int) string {
	if !strings.Contains(s, "\n") || strings.Contains(s, "\r") || strings.HasSuffix(s, "\n") ||
		strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\n") {
		return ""
	}
	pad := strings.Repeat(" ", indent+2)
	var b strings.Builder
	b.WriteString("|-")
	for line := range strings.SplitSeq(s, "\n") {
		b.WriteString("\n")
		if line != "" {
			b.WriteString(pad)
			b.WriteString(line)
		}
	}
	block := b.String()
	var got map[string]any
	if err := yaml.Unmarshal([]byte("k: "+strings.ReplaceAll(block, "\n"+pad, "\n  ")+"\n"), &got); err != nil {
		return ""
	}
	if str, ok := got["k"].(string); !ok || str != s {
		return ""
	}
	return block
}

// yamlJSONText is v as JSON, which YAML reads as the same value (a double-quoted string for a
// string).
func yamlJSONText(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return `""`
	}
	return strings.TrimSuffix(b.String(), "\n")
}
