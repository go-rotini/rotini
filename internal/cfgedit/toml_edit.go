package cfgedit

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/toml"
)

// tomlEditor splices TOML through go-rotini/toml's node spans (Node.Span, Node.KeySpan).
type tomlEditor struct{}

type tomlEdit struct {
	src  []byte
	path []string
	nl   string
	root *toml.Node
}

func newTOMLEdit(src []byte, path []string) (*tomlEdit, error) {
	f, err := toml.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}
	return &tomlEdit{src: src, path: path, nl: newline(src), root: f.Root}, nil
}

// tomlChild returns t's last child named key.
func tomlChild(t *toml.Node, key string) *toml.Node {
	var found *toml.Node
	for _, c := range t.Children {
		if len(c.Key) > 0 && c.Key[len(c.Key)-1] == key {
			found = c
		}
	}
	return found
}

func (tomlEditor) set(src []byte, path []string, v any) ([]byte, error) {
	ed, err := newTOMLEdit(src, path)
	if err != nil {
		return nil, err
	}
	t := ed.root
	for i, seg := range path {
		c := tomlChild(t, seg)
		switch {
		case c == nil:
			return ed.insert(t, i, v)
		case c.Kind == toml.ArrayTableNode:
			return nil, refused(path, "the path goes through an array of tables")
		case c.Kind == toml.TableNode:
			if i == len(path)-1 {
				return nil, refused(path, "it holds a table, not a value")
			}
			t = c
		case c.Kind != toml.KeyValueNode || len(c.Children) != 1:
			return nil, refused(path, "an unexpected node")
		case i < len(path)-1 && c.Children[0].Kind == toml.InlineTableNode:
			t = c.Children[0] // a value inside it can be replaced, but nothing inserted
		case i == len(path)-1:
			old := c.Children[0]
			if old.Kind == toml.InlineTableNode {
				return nil, refused(path, "it holds a table, not a value")
			}
			return splice(src, old.Span.Start.Offset, old.Span.End.Offset, tomlValue(v)), nil
		default:
			return nil, refused(path, "%s holds a value, not a table", strings.Join(path[:i+1], "."))
		}
	}
	return src, nil
}

// explicit reports whether t is a table written with its own [header].
func explicit(t *toml.Node) bool { return t.Kind == toml.TableNode && !t.Span.IsZero() }

// headerImplied reports whether a table without a header exists because a nested [a.b] header
// names it, rather than through dotted keys.
func headerImplied(t *toml.Node) bool {
	for _, c := range t.Children {
		if c.Kind == toml.ArrayTableNode || explicit(c) || (c.Kind == toml.TableNode && headerImplied(c)) {
			return true
		}
	}
	return false
}

// dottedImplied reports whether t holds key/value pairs written as dotted keys in an
// enclosing section.
func dottedImplied(t *toml.Node) bool {
	for _, c := range t.Children {
		if c.Kind == toml.KeyValueNode || (c.Kind == toml.TableNode && c.Span.IsZero() && dottedImplied(c)) {
			return true
		}
	}
	return false
}

// insert adds path[depth:] = v under table t, the deepest table on the path that exists.
func (ed *tomlEdit) insert(t *toml.Node, depth int, v any) ([]byte, error) {
	if t.Kind == toml.InlineTableNode {
		return nil, refused(ed.path, "%s is an inline table", strings.Join(ed.path[:depth], "."))
	}
	if t == ed.root || explicit(t) {
		return ed.insertInSection(t, ed.path[depth:], v), nil
	}
	// t has no header. Written by dotted keys, it extends in the section that holds them: the
	// nearest table up the path with a header, or the top. Named only by nested headers, it
	// gets its own new section.
	if headerImplied(t) {
		if dottedImplied(t) {
			return nil, refused(ed.path, "%s is defined both by dotted keys and by headers", strings.Join(ed.path[:depth], "."))
		}
		text := ed.nl + "[" + tomlKeyPath(ed.path[:depth]) + "]" + ed.nl + tomlKeyPath(ed.path[depth:]) + " = " + tomlValue(v) + ed.nl
		if !endsWithNewline(ed.src) {
			text = ed.nl + text
		}
		return splice(ed.src, len(ed.src), len(ed.src), text), nil
	}
	for i, t := range slices.Backward(ed.tables(depth)) {
		if t == ed.root || explicit(t) {
			return ed.insertInSection(t, ed.path[i:], v), nil
		}
	}
	return nil, refused(ed.path, "can't find where %s is written", strings.Join(ed.path[:depth], "."))
}

// tables returns the tables along the path down to depth: root, then path[0], and so on.
func (ed *tomlEdit) tables(depth int) []*toml.Node {
	chain := make([]*toml.Node, 0, depth+1)
	chain = append(chain, ed.root)
	t := ed.root
	for _, seg := range ed.path[:depth] {
		t = tomlChild(t, seg)
		chain = append(chain, t)
	}
	return chain
}

// sectionEnd returns the end of the last key/value pair written in t's section: its direct
// pairs and those of tables its dotted keys create. -1 when there is none.
func sectionEnd(t *toml.Node) int {
	end := -1
	for _, c := range t.Children {
		switch {
		case c.Kind == toml.KeyValueNode && !c.Span.IsZero():
			end = max(end, c.Span.End.Offset)
		case c.Kind == toml.TableNode && c.Span.IsZero() && !headerImplied(c):
			end = max(end, sectionEnd(c))
		}
	}
	return end
}

// insertInSection writes `path = v` in table t's section, after its last pair, or right after
// its header. A key for the top level of a file whose top level holds no pairs goes above the
// first header and the comment block over it.
func (ed *tomlEdit) insertInSection(t *toml.Node, path []string, v any) []byte {
	line := tomlKeyPath(path) + " = " + tomlValue(v) + ed.nl
	var at int
	switch end := sectionEnd(t); {
	case end >= 0:
		at = nextLine(ed.src, end)
	case t != ed.root:
		at = nextLine(ed.src, t.Span.End.Offset)
	default:
		at = ed.firstHeaderBlock()
	}
	if at == len(ed.src) && !endsWithNewline(ed.src) {
		line = ed.nl + line
	}
	return splice(ed.src, at, at, line)
}

// firstHeaderBlock returns the start of the comment lines directly above the file's first
// header, or the end of the file when it has no header.
func (ed *tomlEdit) firstHeaderBlock() int {
	first := -1
	for _, c := range ed.root.Children {
		if s := firstHeader(c); s >= 0 && (first < 0 || s < first) {
			first = s
		}
	}
	if first < 0 {
		return len(ed.src)
	}
	at := lineStart(ed.src, first)
	for at > 0 {
		prev := lineStart(ed.src, at-1)
		if !commentLine(ed.src, prev, "#") {
			break
		}
		at = prev
	}
	return at
}

func firstHeader(n *toml.Node) int {
	if (n.Kind == toml.TableNode || n.Kind == toml.ArrayTableNode) && !n.Span.IsZero() {
		return n.Span.Start.Offset
	}
	first := -1
	if n.Kind == toml.TableNode {
		for _, c := range n.Children {
			if s := firstHeader(c); s >= 0 && (first < 0 || s < first) {
				first = s
			}
		}
	}
	return first
}

func (tomlEditor) unset(src []byte, path []string, keep int) ([]byte, error) {
	ed, err := newTOMLEdit(src, path)
	if err != nil {
		return nil, err
	}
	chain := []*toml.Node{ed.root}
	t := ed.root
	var kv *toml.Node
	for i, seg := range path {
		c := tomlChild(t, seg)
		switch {
		case c == nil:
			return src, nil
		case c.Kind == toml.ArrayTableNode:
			return nil, refused(path, "the path goes through an array of tables")
		case i == len(path)-1:
			kv = c
		case c.Kind == toml.TableNode:
			t = c
			chain = append(chain, c)
		case len(c.Children) == 1 && c.Children[0].Kind == toml.InlineTableNode:
			return nil, refused(path, "%s is an inline table", strings.Join(path[:i+1], "."))
		default:
			return src, nil
		}
	}
	if kv.Kind == toml.TableNode {
		return nil, refused(path, "it holds a table, not a value")
	}
	k := len(path) - 1
	for k > 0 && k > keep && len(chain[k].Children) == 1 {
		k--
	}
	if k > 0 && len(chain[k].Children) == 1 && !explicit(chain[k]) {
		return nil, refused(path, "removing it would also remove %s, which is kept", strings.Join(path[:k], "."))
	}
	return ed.removeEntry(chain, k, kv)
}

// removeEntry removes the pair kv, or, when the removal empties the tables below level k of
// chain, the outermost of them: its header line and the pair's line.
func (ed *tomlEdit) removeEntry(chain []*toml.Node, k int, kv *toml.Node) ([]byte, error) {
	out, err := ed.removeLine(ed.src, kv.Span)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i > k; i-- {
		if explicit(chain[i]) {
			if out, err = ed.removeLine(out, chain[i].Span); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// removeLine removes the line holding span, which must be the line's only content apart from
// blanks and a trailing comment.
func (ed *tomlEdit) removeLine(src []byte, span toml.Span) ([]byte, error) {
	start, end := span.Start.Offset, span.End.Offset
	ls := lineStart(src, start)
	rest := strings.TrimLeft(string(src[end:lineEnd(src, end)]), " \t")
	if strings.TrimSpace(string(src[ls:start])) != "" || (rest != "" && !strings.HasPrefix(rest, "#")) {
		return nil, refused(ed.path, "the key shares its line with other content")
	}
	return splice(src, ls, nextLine(src, end), ""), nil
}

var bareKey = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9_-]+$`) })

func tomlKey(k string) string {
	if bareKey().MatchString(k) {
		return k
	}
	return tomlString(k)
}

func tomlKeyPath(path []string) string {
	parts := make([]string, len(path))
	for i, k := range path {
		parts[i] = tomlKey(k)
	}
	return strings.Join(parts, ".")
}

// tomlValue renders v as a TOML value: a scalar, an array, or an inline table.
func tomlValue(v any) string {
	switch x := v.(type) {
	case string:
		return tomlString(x)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = tomlValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		if len(x) == 0 {
			return "{}"
		}
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = tomlKey(k) + " = " + tomlValue(x[k])
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	s, _ := formatNumber(v)
	return s
}

// tomlString renders a basic (double-quoted) TOML string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
