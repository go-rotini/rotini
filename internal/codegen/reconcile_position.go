package codegen

// Mapping JSON pointers to line:column positions in the original source. Each format uses
// its codec's RFC 6901 pointer over a positioned AST; JSON uses the jsonc parser. Parsing is
// lazy and cached per document, since locators are consulted only when problems exist.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// sourceLocator resolves a JSON pointer ("/command/flags/0/schema") to a 1-based line and
// column in the original source. ok is false when the pointer does not resolve or the
// document does not parse.
type sourceLocator func(pointer string) (line, col int, ok bool)

// newSourceLocator builds the locator for one document, or nil for an unrecognized format.
func newSourceLocator(format fileFormat, data []byte) sourceLocator {
	switch format {
	case formatYAML:
		return yamlLocator(data)
	case formatJSON, formatJSONC:
		return jsoncLocator(data)
	case formatTOML:
		return tomlLocator(data)
	default:
		return nil
	}
}

// yamlLocator resolves a pointer via yaml.PathPointer over the parsed document.
func yamlLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*yaml.File, error) { return yaml.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || len(f.Docs) == 0 {
			return 0, 0, false
		}
		p, err := yaml.PathPointer(pointer)
		if err != nil {
			return 0, 0, false
		}
		positions, err := p.ReadPositions(f.Docs[0])
		if err != nil || len(positions) == 0 {
			return 0, 0, false
		}
		return positions[0].Line, positions[0].Column, true
	}
}

// jsoncLocator resolves a pointer via jsonc.PathPointer for JSON and JSONC documents.
func jsoncLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*jsonc.File, error) { return jsonc.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || f.Root == nil {
			return 0, 0, false
		}
		p, err := jsonc.PathPointer(pointer)
		if err != nil {
			return 0, 0, false
		}
		positions := p.ReadPositions(f.Root)
		if len(positions) == 0 {
			return 0, 0, false
		}
		return positions[0].Line, positions[0].Column, true
	}
}

// tomlLocator resolves a pointer over the TOML AST, placing it where its value starts, as the
// YAML and JSON locators do. It walks the tree itself because a TOML array of tables is a run
// of sibling [[name]] entries, which toml.PathPointer does not index as one array.
func tomlLocator(data []byte) sourceLocator {
	parse := sync.OnceValues(func() (*toml.File, error) { return toml.Parse(data) })
	return func(pointer string) (int, int, bool) {
		f, err := parse()
		if err != nil || f.Root == nil || !strings.HasPrefix(pointer, "/") {
			return 0, 0, false
		}
		n := tomlResolve(f.Root, strings.Split(pointer[1:], "/"))
		if n == nil {
			return 0, 0, false
		}
		pos := tomlStart(n)
		if pos.Line == 0 {
			return 0, 0, false
		}
		return pos.Line, pos.Column, true
	}
}

// pointerUnescaper decodes an RFC 6901 token.
var pointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")

// tomlResolve follows RFC 6901 tokens from a table to the node they name: a key's value, a
// table, an array element or one [[name]] entry. It returns nil when a token doesn't resolve.
func tomlResolve(n *toml.Node, tokens []string) *toml.Node {
	var run []*toml.Node // the entries of an array of tables awaiting an index
	for _, tok := range tokens {
		tok = pointerUnescaper.Replace(tok)
		if run != nil || n.Kind == toml.ArrayNode {
			items := run
			if items == nil {
				items = n.Children
			}
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(items) {
				return nil
			}
			n, run = items[i], nil
			continue
		}
		if n.Kind != toml.TableNode && n.Kind != toml.ArrayTableNode && n.Kind != toml.InlineTableNode {
			return nil
		}
		var next *toml.Node
		for _, c := range n.Children {
			if len(c.Key) == 0 || c.Key[len(c.Key)-1] != tok {
				continue
			}
			if c.Kind == toml.ArrayTableNode {
				run = append(run, c)
				continue
			}
			if next == nil {
				next = c
			}
		}
		switch {
		case run != nil:
			n = run[0]
		case next == nil:
			return nil
		case next.Kind == toml.KeyValueNode && len(next.Children) > 0:
			n = next.Children[0]
		default:
			n = next
		}
	}
	return n
}

// tomlStart is where n's source text starts, or, for a table with none of its own (implied by
// a dotted key or a nested header), where its first placed child starts.
func tomlStart(n *toml.Node) toml.Position {
	if !n.Span.IsZero() {
		return n.Span.Start
	}
	for _, c := range n.Children {
		if p := tomlStart(c); p.Line > 0 {
			return p
		}
	}
	return toml.Position{}
}
