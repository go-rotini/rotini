package codegen

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Key lookup for `rotini explain`: what a spec or conf key means, read from the embedded
// schemas, the same source as the generated reference pages. A key path is the keys from the
// document's top, dot-separated, with list positions left out: command.flags.schema.from.

// ExplainFn is the signature of [Processor.Explain]. The explain handler fetches it as an
// injectable service so tests can substitute a double.
type ExplainFn = func(path string) ([]KeyInfo, error)

// KeyInfo describes one spec or conf key.
type KeyInfo struct {
	Document    string   // "spec" or "conf"
	Path        string   // the key path, as given
	Description string   // what the key means
	Type        string   // its JSON type or shape: string, boolean, array of string, Effects, …
	Enum        []string // the values it allows; nil when it allows any of its type
	Default     string   // its default as JSON; "" for none
	Examples    []string // example values as JSON
	Pattern     string   // the regular expression a string value must match; "" for none
	Hint        string   // what to write instead when a value is the wrong shape; "" for none
	Keys        []string // the keys below it; nil for a leaf
}

// Explain describes the spec or conf key at path. A key both documents have (version,
// $schema) gets one entry per document. An unknown key is an error naming the nearest one.
func (p *Processor) Explain(path string) ([]KeyInfo, error) { return ExplainKey(path) }

// ExplainKey is [Processor.Explain] without a processor.
func ExplainKey(path string) ([]KeyInfo, error) {
	segs := strings.Split(strings.Trim(path, "."), ".")
	if path == "" || slices.Contains(segs, "") {
		return nil, fmt.Errorf("%q is not a key path; write the keys from the document's top, separated by dots (command.flags.schema.from)", path)
	}
	var out []KeyInfo
	var nearest []string
	for i, doc := range schemaDocuments() {
		name := []string{"spec", "conf"}[i]
		node, unknown, known := walkKeyPath(doc, doc, segs)
		if node == nil {
			if unknown >= 0 {
				nearest = append(nearest, explainMiss(segs, unknown, known))
			}
			continue
		}
		out = append(out, keyInfo(doc, node, name, path))
	}
	if len(out) == 0 {
		// The miss deepest into the path is the most useful one.
		slices.SortStableFunc(nearest, func(a, b string) int { return len(b) - len(a) })
		if len(nearest) == 0 {
			return nil, fmt.Errorf("no spec or conf key %q", path)
		}
		return nil, fmt.Errorf("%s", nearest[0])
	}
	return out, nil
}

// explainMiss says which segment of a key path is unknown, with the nearest key there.
func explainMiss(segs []string, at int, known []string) string {
	under := "the document's top"
	if at > 0 {
		under = strings.Join(segs[:at], ".")
	}
	msg := fmt.Sprintf("no key %q under %s", segs[at], under)
	if s := closestName(segs[at], known); s != "" {
		msg += fmt.Sprintf("; did you mean %q?", strings.Join(append(slices.Clone(segs[:at]), s), "."))
	}
	return msg
}

// ExplainCandidates completes a key path being typed: every key under the path's last full
// segment that starts with the partial one, as a full path, ending in "." when it has keys of
// its own.
func ExplainCandidates(partial string) []string {
	segs := strings.Split(partial, ".")
	prefix, last := segs[:len(segs)-1], segs[len(segs)-1]
	seen := map[string]bool{}
	var out []string
	for _, doc := range schemaDocuments() {
		node := doc
		if len(prefix) > 0 {
			var unknown int
			node, unknown, _ = walkKeyPath(doc, doc, prefix)
			if node == nil || unknown >= 0 {
				continue
			}
		}
		children := schemaChildren(doc, node)
		for _, k := range slices.Sorted(maps.Keys(children)) {
			if !strings.HasPrefix(k, last) {
				continue
			}
			c := strings.Join(append(slices.Clone(prefix), k), ".")
			if len(schemaChildren(doc, children[k])) > 0 {
				c += "."
			}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// walkKeyPath follows segs from node. It returns the node reached, or nil with the index of
// the first unknown segment and the keys known there (-1 when the path ends inside a value
// with no keys).
func walkKeyPath(doc, node any, segs []string) (any, int, []string) {
	for i, seg := range segs {
		children := schemaChildren(doc, node)
		next, ok := children[seg]
		if !ok {
			return nil, i, slices.Sorted(maps.Keys(children))
		}
		node = next
	}
	return node, -1, nil
}

// schemaChildren returns the keys a schema node allows below it, looking through $ref,
// allOf, anyOf and oneOf, and through lists (items) and maps (additionalProperties), which a
// key path doesn't name.
func schemaChildren(doc, node any) map[string]any {
	out := map[string]any{}
	var visit func(n any, depth int)
	visit = func(n any, depth int) {
		m, ok := n.(map[string]any)
		if !ok || depth > 16 {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			visit(resolveSchemaRef(doc, ref), depth+1)
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for k, v := range props {
				if _, dup := out[k]; !dup {
					out[k] = v
				}
			}
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			if list, ok := m[key].([]any); ok {
				for _, e := range list {
					visit(e, depth+1)
				}
			}
		}
		visit(m["items"], depth+1)
		if ap, ok := m["additionalProperties"].(map[string]any); ok {
			visit(ap, depth+1)
		}
	}
	visit(node, 0)
	return out
}

// resolveSchemaRef returns the node a local "#/…" reference names in doc, or nil.
func resolveSchemaRef(doc any, ref string) any {
	rest, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	node := doc
	for seg := range strings.SplitSeq(rest, "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[unescapePointer(seg)]
	}
	return node
}

// keyInfo describes one schema node.
func keyInfo(doc, node any, document, path string) KeyInfo {
	info := KeyInfo{Document: document, Path: path}
	m, _ := node.(map[string]any)
	info.Description, _ = m["description"].(string)
	info.Hint, _ = m["x-hint"].(string)
	info.Pattern, _ = m["pattern"].(string)
	info.Type = schemaTypeName(m)
	target := m
	if ref, ok := m["$ref"].(string); ok {
		if r, ok := resolveSchemaRef(doc, ref).(map[string]any); ok {
			target = r
			if info.Description == "" {
				info.Description, _ = r["description"].(string)
			}
		}
	}
	for _, e := range enumOfNode(target) {
		info.Enum = append(info.Enum, jsonText(e))
	}
	if d, ok := m["default"]; ok {
		info.Default = jsonText(d)
	}
	if ex, ok := m["examples"].([]any); ok {
		for _, e := range ex {
			info.Examples = append(info.Examples, jsonText(e))
		}
	}
	info.Keys = slices.Sorted(maps.Keys(schemaChildren(doc, node)))
	return info
}

// enumOfNode returns a node's enum, or its items' enum for a list.
func enumOfNode(m map[string]any) []any {
	if e, ok := m["enum"].([]any); ok {
		return e
	}
	if items, ok := m["items"].(map[string]any); ok {
		if e, ok := items["enum"].([]any); ok {
			return e
		}
	}
	return nil
}

// schemaTypeName names a node's shape for people: its JSON type, "array of <type>", a
// definition's name for a $ref, or alternatives joined with "or".
func schemaTypeName(m map[string]any) string {
	if ref, ok := m["$ref"].(string); ok {
		return ref[strings.LastIndex(ref, "/")+1:]
	}
	switch t := m["type"].(type) {
	case string:
		if t == "array" {
			if items, ok := m["items"].(map[string]any); ok {
				if inner := schemaTypeName(items); inner != "" {
					return "array of " + inner
				}
			}
		}
		return t
	case []any:
		var parts []string
		for _, e := range t {
			if s, ok := e.(string); ok && s != "null" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " or ")
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if list, ok := m[key].([]any); ok {
			var parts []string
			for _, e := range list {
				if em, ok := e.(map[string]any); ok {
					if name := schemaTypeName(em); name != "" && !slices.Contains(parts, name) {
						parts = append(parts, name)
					}
				}
			}
			return strings.Join(parts, " or ")
		}
	}
	return ""
}

// jsonText renders a decoded JSON value compactly.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
