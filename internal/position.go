package internal

// This file maps JSON-pointer instance locations back to line:column positions
// in the ORIGINAL source bytes, so a validation problem can name the place the
// user actually typed — `.rotini.spec.yaml:12:7` — instead of only the pointer.
// YAML positions come from the yaml AST; JSON from a token walk over the raw
// bytes; JSONC from the same walk after comments and trailing commas are
// blanked IN PLACE (spaces preserve every offset and newline). TOML has no
// position support here — its problems degrade to pointer-only, by design.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/yaml"
)

// sourceLocator resolves a JSON-pointer instance location ("/command/inputs/
// flags/0/schema") to a 1-based line:column in the original source. ok is
// false when the pointer cannot be resolved (or the format has no positions);
// callers then degrade to pointer-only reporting.
type sourceLocator func(pointer string) (line, col int, ok bool)

// newSourceLocator builds the locator for one read document. It is cheap to
// build — parsing happens lazily, at most once, on the first lookup — because
// locators are only consulted on the failure path. A nil return means the
// format carries no positions (TOML).
func newSourceLocator(format fileFormat, data []byte) sourceLocator {
	switch format {
	case formatYAML:
		return yamlLocator(data)
	case formatJSON:
		return jsonLocator(data)
	case formatJSONC:
		return jsonLocator(blankJSONC(data))
	default:
		return nil
	}
}

// pointerSegments splits a JSON pointer into its unescaped reference tokens.
func pointerSegments(pointer string) []string {
	if pointer == "" || pointer == "/" {
		return nil
	}
	segs := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, s := range segs {
		s = strings.ReplaceAll(s, "~1", "/")
		segs[i] = strings.ReplaceAll(s, "~0", "~")
	}
	return segs
}

// ─── YAML ─────────────────────────────────────────────────────────────────────.

// yamlLocator walks the yaml AST by pointer segments; the resolved node's own
// position is returned (for a mapping value that is the value node, which for
// scalars sits on the key's line).
func yamlLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*yaml.File, error) { return yaml.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || len(f.Docs) == 0 {
			return 0, 0, false
		}
		node := f.Docs[0]
		if node.Kind == yaml.DocumentNode && len(node.Children) > 0 {
			node = node.Children[0]
		}
		for _, seg := range pointerSegments(pointer) {
			switch node.Kind {
			case yaml.MappingNode:
				var next *yaml.Node
				for i := 0; i+1 < len(node.Children); i += 2 {
					if node.Children[i].Value == seg {
						next = node.Children[i+1]
						break
					}
				}
				if next == nil {
					return 0, 0, false
				}
				node = next
			case yaml.SequenceNode:
				idx, err := strconv.Atoi(seg)
				if err != nil || idx < 0 || idx >= len(node.Children) {
					return 0, 0, false
				}
				node = node.Children[idx]
			default:
				return 0, 0, false
			}
		}
		return node.Pos.Line, node.Pos.Column, true
	}
}

// ─── JSON / JSONC ─────────────────────────────────────────────────────────────.

// jsonLocator resolves a pointer to the byte offset where the target value
// starts (a token walk over the raw bytes), then counts lines to it.
func jsonLocator(data []byte) sourceLocator {
	return func(pointer string) (int, int, bool) {
		dec := json.NewDecoder(bytes.NewReader(data))
		off, ok := seekPointer(dec, data, pointerSegments(pointer))
		if !ok {
			return 0, 0, false
		}
		line, col := lineColAt(data, off)
		return line, col, true
	}
}

// seekPointer consumes tokens until the value addressed by segs is next, then
// returns the offset of its first byte. With no segments left, the next value
// in the stream is the target.
func seekPointer(dec *json.Decoder, data []byte, segs []string) (int, bool) {
	if len(segs) == 0 {
		return valueStart(data, dec.InputOffset()), true
	}
	tok, err := dec.Token()
	if err != nil {
		return 0, false
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return 0, false
	}
	switch delim {
	case '{':
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return 0, false
			}
			if key, _ := keyTok.(string); key == segs[0] {
				return seekPointer(dec, data, segs[1:])
			}
			if !skipJSONValue(dec) {
				return 0, false
			}
		}
	case '[':
		idx, err := strconv.Atoi(segs[0])
		if err != nil || idx < 0 {
			return 0, false
		}
		for i := 0; dec.More(); i++ {
			if i == idx {
				return seekPointer(dec, data, segs[1:])
			}
			if !skipJSONValue(dec) {
				return 0, false
			}
		}
	}
	return 0, false
}

// skipJSONValue consumes exactly one value (scalar or whole container).
func skipJSONValue(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if d, ok := tok.(json.Delim); ok && (d == '{' || d == '[') {
		for dec.More() {
			if !skipJSONValue(dec) { // in objects this alternates key/value tokens — both are values to Token()
				return false
			}
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return false
		}
	}
	return true
}

// valueStart advances from a token boundary past whitespace and the key/value
// punctuation to the first byte of the next value.
func valueStart(data []byte, off int64) int {
	i := int(off)
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r', ':', ',':
			i++
		default:
			return i
		}
	}
	return len(data) - 1
}

// lineColAt counts 1-based line and column (in bytes) at offset.
func lineColAt(data []byte, off int) (int, int) {
	line, col := 1, 1
	for i := 0; i < off && i < len(data); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// blankJSONC returns a copy of data with // and /* */ comments and trailing
// commas overwritten by spaces — newlines kept — so the stdlib JSON tokenizer
// can walk it while every surviving byte keeps its original line and column.
func blankJSONC(data []byte) []byte {
	out := append([]byte(nil), data...)
	inStr := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			out[i] = ' '
			i++
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
		}
	}
	// Trailing commas (comments are gone now): a comma whose next non-blank
	// byte closes a container is JSONC-legal but chokes the tokenizer.
	inStr = false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case ',':
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
	}
	return out
}
