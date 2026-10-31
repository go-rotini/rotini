package rotini

import (
	"strconv"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// A locator finds where the value at path (its keys and list indexes, outermost first) sits in
// a document, as the 1-indexed line and column an editor shows. ok is false when the document
// doesn't parse or holds nothing at path.
type locator func(data []byte, path []string) (line, col int, ok bool)

// locateYAML is the [locator] for YAML: the first document, through mappings and sequences.
// An alias ends the search, since its target is written elsewhere.
func locateYAML(data []byte, path []string) (line, col int, ok bool) {
	f, err := yaml.Parse(data)
	if err != nil || len(f.Docs) == 0 || len(f.Docs[0].Children) == 0 {
		return 0, 0, false
	}
	n := f.Docs[0].Children[0]
	for _, seg := range path {
		switch n.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			for i := 0; i+1 < len(n.Children); i += 2 {
				if n.Children[i].Value == seg {
					next = n.Children[i+1]
				}
			}
			if next == nil {
				return 0, 0, false
			}
			n = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(n.Children) {
				return 0, 0, false
			}
			n = n.Children[i]
		default:
			return 0, 0, false
		}
	}
	return n.Pos.Line, n.Pos.Column, n.Pos.Line > 0
}

// locateJSONC is the [locator] for JSON and JSON with comments.
func locateJSONC(data []byte, path []string) (line, col int, ok bool) {
	f, err := jsonc.Parse(data)
	if err != nil || f.Root == nil {
		return 0, 0, false
	}
	n := f.Root
	for _, seg := range path {
		var next *jsonc.Node
		switch n.Kind {
		case jsonc.ObjectNode:
			for _, c := range n.Children {
				if c.Kind == jsonc.KeyValueNode && c.Key == seg && len(c.Children) == 1 {
					next = c.Children[0] // the last one wins, as decoding reads it
				}
			}
		case jsonc.ArrayNode:
			i, err := strconv.Atoi(seg)
			if err != nil {
				return 0, 0, false
			}
			for _, c := range n.Children {
				if c.Kind == jsonc.CommentNode {
					continue
				}
				if i == 0 {
					next = c
					break
				}
				i--
			}
		}
		if next == nil {
			return 0, 0, false
		}
		n = next
	}
	return n.Pos.Line, n.Pos.Column, n.Pos.Line > 0
}

// locateTOML is the [locator] for TOML: through tables, dotted keys, inline tables, arrays and
// arrays of tables.
func locateTOML(data []byte, path []string) (line, col int, ok bool) {
	f, err := toml.Parse(data)
	if err != nil || f.Root == nil {
		return 0, 0, false
	}
	n := f.Root
	for i := 0; i < len(path) && n != nil; i++ {
		switch n.Kind {
		case toml.TableNode, toml.ArrayTableNode, toml.InlineTableNode:
			var tables []*toml.Node
			n, tables = tomlMember(n, path[i])
			if len(tables) > 0 {
				// An array of tables: the next segment, if any, is the entry's index.
				n = tables[0]
				if i+1 < len(path) {
					i++
					n = tomlItem(tables, path[i])
				}
			}
		case toml.ArrayNode:
			n = tomlItem(n.Children, path[i])
		default:
			n = nil
		}
	}
	if n == nil {
		return 0, 0, false
	}
	at := n.Span.Start
	if at.Line == 0 {
		at = n.KeySpan.Start // a table implied by a dotted key has no text of its own
	}
	return at.Line, at.Column, at.Line > 0
}

// tomlMember is the value table t holds under key: a key's value or a sub-table, else the
// entries of an array of tables by that name.
func tomlMember(t *toml.Node, key string) (*toml.Node, []*toml.Node) {
	var found *toml.Node
	var tables []*toml.Node
	for _, c := range t.Children {
		if len(c.Key) != 1 || c.Key[0] != key {
			continue
		}
		switch c.Kind {
		case toml.KeyValueNode:
			if len(c.Children) == 1 {
				found = c.Children[0]
			}
		case toml.ArrayTableNode:
			tables = append(tables, c)
		default:
			found = c
		}
	}
	return found, tables
}

// tomlItem is the item of items at index seg, comments skipped, or nil.
func tomlItem(items []*toml.Node, seg string) *toml.Node {
	k, err := strconv.Atoi(seg)
	if err != nil {
		return nil
	}
	for _, c := range items {
		if c.Kind == toml.CommentNode {
			continue
		}
		if k == 0 {
			return c
		}
		k--
	}
	return nil
}
