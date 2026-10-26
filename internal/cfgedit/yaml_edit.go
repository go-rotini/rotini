package cfgedit

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/yaml"
)

// yamlEditor splices YAML through the span index.
type yamlEditor struct{}

type yamlEdit struct {
	src  []byte
	doc  *YAMLDoc
	path []string
	nl   string
	step int
}

func newYAMLEdit(src []byte, path []string) (*yamlEdit, error) {
	doc, err := IndexYAML(src)
	if err != nil {
		var le *LayoutError
		switch {
		case errors.Is(err, ErrMultipleDocuments):
			return nil, refused(path, "the file holds more than one YAML document")
		case errors.As(err, &le):
			return nil, refused(path, "%s", le.Error())
		}
		return nil, err
	}
	if doc.Root != nil && docMarker(src, lineStart(src, doc.Root.Start)) {
		return nil, refused(path, "the document's content starts on its --- line")
	}
	return &yamlEdit{src: src, doc: doc, path: path, nl: newline(src), step: yamlIndentStep(doc.Root)}, nil
}

func (yamlEditor) set(src []byte, path []string, v any) ([]byte, error) {
	if slices.Contains(path, "<<") {
		return nil, refused(path, "a key named << reads as a merge key")
	}
	ed, err := newYAMLEdit(src, path)
	if err != nil {
		return nil, err
	}
	root := ed.doc.Root
	if root == nil {
		if !yamlTrivia(src) {
			return nil, refused(path, "the file has content that isn't a mapping")
		}
		return ed.insertAt(len(src), 0, path, v), nil
	}
	if root.Kind != YAMLMapping {
		return nil, refused(path, "the file's top level isn't a mapping")
	}
	if ls, le := lineStart(src, root.Start), nextLine(src, root.End); root.Flow && len(root.Pairs) == 0 &&
		len(bytes.TrimSpace(src[ls:lineEnd(src, root.End)])) == 2 {
		// A top-level {} alone on its line, as unset leaves under a "---" line, becomes a
		// block mapping.
		out := *ed
		out.src = splice(src, ls, le, "")
		return out.insertAt(ls, 0, path, v), nil
	}
	m := root
	for i, seg := range path {
		if err := ed.checkMapping(m); err != nil {
			return nil, err
		}
		p := findYAMLPair(m, seg)
		if p == nil {
			if m.Flow {
				return nil, refused(path, "%s is written in flow style ({…})", ed.where(i))
			}
			return ed.insertAt(ed.mappingEnd(m), m.Col, path[i:], v), nil
		}
		if p.Value.Anchor != "" || p.Value.Kind == YAMLAlias {
			return nil, refused(path, "the path goes through an anchor or alias")
		}
		if i < len(path)-1 && p.Value.Tag != "" {
			return nil, refused(path, "the path goes through a tagged value")
		}
		if i == len(path)-1 {
			return ed.replace(m, p, v)
		}
		switch {
		case p.Value.Kind == YAMLMapping && len(p.Value.Pairs) == 0 && !m.Flow:
			// An empty {} under a block key, as unset leaves a kept mapping, becomes a block
			// mapping.
			out := *ed
			out.src = splice(src, p.Colon+1, p.Value.End, "")
			return out.insertAt(nextLine(out.src, p.Colon), p.Col+ed.step, path[i+1:], v), nil
		case p.Value.Kind == YAMLMapping:
			m = p.Value
		case p.Value.Empty && !m.Flow:
			return ed.insertAt(nextLine(src, p.Value.End), p.Col+ed.step, path[i+1:], v), nil
		default:
			return nil, refused(path, "%s holds a value, not a mapping", strings.Join(path[:i+1], "."))
		}
	}
	return src, nil
}

// where names the mapping at depth i of the path, for messages.
func (ed *yamlEdit) where(i int) string {
	if i == 0 {
		return "the top-level mapping"
	}
	return strings.Join(ed.path[:i], ".")
}

// checkMapping refuses a mapping with a merge key, where a key it doesn't hold may come from
// the merged mapping, and one that holds a key twice, where readers disagree on which wins.
func (ed *yamlEdit) checkMapping(m *YAMLNode) error {
	seen := make(map[string]bool, len(m.Pairs))
	for _, p := range m.Pairs {
		if p.Merge {
			return refused(ed.path, "the path goes through a mapping with a << merge key")
		}
		if seen[p.Key.Value] {
			return refused(ed.path, "the key %q appears twice in one mapping", p.Key.Value)
		}
		seen[p.Key.Value] = true
	}
	return nil
}

func findYAMLPair(m *YAMLNode, key string) *YAMLPair {
	var found *YAMLPair
	for _, p := range m.Pairs {
		if p.Key.Value == key {
			found = p
		}
	}
	return found
}

// underIndented reports whether p's value starts on a later line than its key without being
// indented past it. A block sequence may start at the key's own column (`items:\n- a`).
func (ed *yamlEdit) underIndented(p *YAMLPair) bool {
	old := p.Value
	if old.Empty || (old.Kind == YAMLSequence && !old.Flow) {
		return false
	}
	ls := lineStart(ed.src, old.Start)
	return ls != lineStart(ed.src, p.Colon) && old.Start-ls <= p.Col
}

// replace writes v as p's value.
func (ed *yamlEdit) replace(m *YAMLNode, p *YAMLPair, v any) ([]byte, error) {
	old := p.Value
	if old.Kind == YAMLMapping {
		return nil, refused(ed.path, "it holds a mapping, not a value")
	}
	if ed.underIndented(p) {
		return nil, refused(ed.path, "its value starts on the next line, not indented past the key")
	}
	if old.Kind == YAMLScalar && old.Style == yaml.PlainStyle && !old.Empty && bytes.ContainsAny(ed.src[old.Body:old.End], "\n") {
		return nil, refused(ed.path, "its value is written over several lines without quotes")
	}
	inFlow := m.Flow || (old.Kind == YAMLSequence && old.Flow)
	if inFlow || !isCollection(v) {
		// Inside a flow mapping everything is in flow context; a flow sequence in block context
		// keeps its brackets, but a scalar replacing it is in block context.
		text := yamlLeaf(v)
		if m.Flow || isCollection(v) {
			text = yamlFlow(v)
		}
		if str, ok := v.(string); ok && old.Kind == YAMLScalar {
			text = keepQuoting(old.Style, str, text)
		}
		if m.Flow {
			var err error
			if text, err = ed.quoteBeforeBlankLine(old, v, text); err != nil {
				return nil, err
			}
		}
		switch {
		case old.Empty, m.Flow && old.Start == p.Colon+1:
			// A value right after a flow mapping's colon parsed only because it started with a
			// flow indicator ({a:[]}); without a space, {a:"0"} reads as one plain key.
			return splice(ed.src, old.Start, old.End, " "+text), nil
		case old.Kind == YAMLSequence && !old.Flow:
			return splice(ed.src, p.Colon+1, old.End, " "+text), nil
		}
		return splice(ed.src, old.Start, old.End, text), nil
	}
	if _, isList := v.([]any); isList && old.Kind == YAMLSequence {
		text := strings.TrimSuffix(yamlBlock(v, old.Col, ed.nl), ed.nl)
		return splice(ed.src, old.Body, old.End, strings.TrimLeft(text, " ")), nil
	}
	block := ed.nl + strings.TrimSuffix(yamlBlock(v, p.Col+ed.step, ed.nl), ed.nl)
	if old.Empty {
		at := lineEnd(ed.src, old.End)
		return splice(ed.src, at, at, block), nil
	}
	if !bytes.ContainsAny(ed.src[p.Colon:old.End], "\n") {
		// One line: put the block after the line's comment, then drop the old value.
		at := lineEnd(ed.src, old.End)
		out := splice(ed.src, at, at, block)
		return splice(out, p.Colon+1, old.End, ""), nil
	}
	return splice(ed.src, p.Colon+1, old.End, block), nil
}

// mappingEnd returns where a new entry for block mapping m goes: the line after its last entry
// and any comment lines indented at least as far as its keys that directly follow it.
func (ed *yamlEdit) mappingEnd(m *YAMLNode) int {
	at := nextLine(ed.src, m.End)
	for at < len(ed.src) && commentLine(ed.src, at, "#") && indentAt(ed.src, at) >= m.Col {
		at = nextLine(ed.src, at)
	}
	return at
}

// yamlTrivia reports whether src holds only blank lines, comments, directives and document
// markers.
func yamlTrivia(src []byte) bool {
	for ls := bomLen(src); ls < len(src); ls = nextLine(src, ls) {
		line := bytes.TrimLeft(src[ls:lineEnd(src, ls)], " \t") // a line of tabs is as blank as one of spaces
		if len(line) != 0 && line[0] != '#' && line[0] != '%' && !docMarker(src, ls) {
			return false
		}
	}
	return true
}

// insertAt inserts the nested entries for path, ending in v, at offset at with the first key at
// col.
func (ed *yamlEdit) insertAt(at, col int, path []string, v any) []byte {
	var b strings.Builder
	if at == len(ed.src) && !endsWithNewline(ed.src) {
		b.WriteString(ed.nl)
	}
	for i, seg := range path {
		line := spaces(col+i*ed.step) + yamlKey(seg) + ":"
		switch {
		case i < len(path)-1:
			line += ed.nl
		case isCollection(v):
			line += ed.nl + yamlBlock(v, col+(i+1)*ed.step, ed.nl)
		default:
			line += " " + yamlLeaf(v) + ed.nl
		}
		b.WriteString(line)
	}
	return splice(ed.src, at, at, b.String())
}

// yamlLeaf renders a value written on its key's line: a scalar, or an empty list or map.
func yamlLeaf(v any) string {
	switch v.(type) {
	case []any:
		return "[]"
	case map[string]any:
		return "{}"
	}
	return yamlScalar(v, false)
}

// yamlIndentStep returns how far the file indents a nested mapping under its key, from the
// first one it finds; 2 when it has none.
func yamlIndentStep(n *YAMLNode) int {
	if n == nil {
		return 2
	}
	for _, p := range n.Pairs {
		if p.Value.Kind == YAMLMapping && !p.Value.Flow && p.Value.Col > p.Col {
			return p.Value.Col - p.Col
		}
		if step := yamlIndentStep(p.Value); step != 2 {
			return step
		}
	}
	return 2
}

func (yamlEditor) unset(src []byte, path []string, keep int) ([]byte, error) {
	ed, err := newYAMLEdit(src, path)
	if err != nil {
		return nil, err
	}
	if ed.doc.Root == nil || ed.doc.Root.Kind != YAMLMapping {
		return src, nil
	}
	var maps []*YAMLNode
	var pairs []*YAMLPair
	m := ed.doc.Root
	for i, seg := range path {
		if err := ed.checkMapping(m); err != nil {
			return nil, err
		}
		p := findYAMLPair(m, seg)
		if p == nil {
			return src, nil
		}
		if p.Value.Anchor != "" || p.Value.Kind == YAMLAlias {
			return nil, refused(path, "the path goes through an anchor or alias")
		}
		if i < len(path)-1 && p.Value.Tag != "" {
			return nil, refused(path, "the path goes through a tagged value")
		}
		maps, pairs = append(maps, m), append(pairs, p)
		if i < len(path)-1 {
			if p.Value.Kind != YAMLMapping {
				return src, nil
			}
			m = p.Value
		}
	}
	t := len(path) - 1
	for t > 0 && t > keep && len(maps[t].Pairs) == 1 {
		t--
	}
	if maps[t].Flow {
		return nil, refused(path, "%s is written in flow style ({…})", ed.where(t))
	}
	out, err := ed.removePair(pairs[t])
	if err != nil {
		return nil, err
	}
	if t == 0 && len(maps[0].Pairs) == 1 && ed.startMarked() {
		// A document holding only "---" reads as null, not as a mapping.
		at := lineStart(ed.src, pairs[0].Key.Start)
		out = splice(out, at, at, "{}"+ed.nl)
	}
	if t > 0 && len(maps[t].Pairs) == 1 {
		// A kept mapping left empty is written as {} so it still reads as a mapping.
		at := pairs[t-1].Colon + 1
		out = splice(out, at, at, " {}")
	}
	return out, nil
}

// startMarked reports whether a "---" line comes before the document's root.
func (ed *yamlEdit) startMarked() bool {
	end := lineStart(ed.src, ed.doc.Root.Start)
	for ls := bomLen(ed.src); ls < end; ls = nextLine(ed.src, ls) {
		if docMarker(ed.src, ls) && ed.src[ls] == '-' {
			return true
		}
	}
	return false
}

// removePair removes p's lines, from its key's line through the end of its value, line ending
// included.
func (ed *yamlEdit) removePair(p *YAMLPair) ([]byte, error) {
	ls := lineStart(ed.src, p.Key.Start)
	if len(bytes.TrimLeft(ed.src[ls:p.Key.Start], " ")) != 0 {
		return nil, refused(ed.path, "the key shares its line with other content")
	}
	end := nextLine(ed.src, p.Value.End)
	if p.Value.Kind == YAMLMapping && !p.Value.Flow {
		// Comment lines indented under the mapping, after its last entry, go with it.
		for end < len(ed.src) && commentLine(ed.src, end, "#") && leadAt(ed.src, end) > p.Col {
			end = nextLine(ed.src, end)
		}
	}
	return splice(ed.src, ls, end, ""), nil
}

// keepQuoting writes a string that replaces a quoted one in the same quotes, so the edit keeps
// the author's style; otherwise it returns text, the default rendering.
func keepQuoting(style yaml.ScalarStyle, s, text string) string {
	switch {
	case style == yaml.DoubleQuotedStyle:
		return strconv.Quote(s)
	case style == yaml.SingleQuotedStyle && !strings.ContainsAny(s, "\n\r") && !strings.ContainsFunc(s, yamlEscapeOnly):
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return text
}

// refuseYAMLStream refuses a YAML file holding more than one document before anything decodes
// it, so the refusal reads the same whatever the decoder makes of the extra documents.
func refuseYAMLStream(format Format, src []byte, path []string) error {
	if format != YAML {
		return nil
	}
	if f, err := yaml.Parse(src[bomLen(src):]); err == nil && len(f.Docs) > 1 {
		return refused(path, "the file holds more than one YAML document")
	}
	return nil
}

// blankLineFollows reports whether the white space that starts at offset at holds a blank line.
func blankLineFollows(src []byte, at int) bool {
	breaks := 0
	for ; at < len(src); at++ {
		switch src[at] {
		case '\n':
			breaks++
			if breaks > 1 {
				return true
			}
		case ' ', '\t', '\r':
		default:
			return false
		}
	}
	return false
}

// quoteBeforeBlankLine returns text, v as written in place of old inside {…}, quoted when a
// blank line follows old: the yaml reader keeps those line breaks at the end of a plain value,
// so only a quoted one reads back as itself. A value that isn't a string is refused there.
func (ed *yamlEdit) quoteBeforeBlankLine(old *YAMLNode, v any, text string) (string, error) {
	if isCollection(v) || strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'") || !blankLineFollows(ed.src, old.End) {
		return text, nil
	}
	str, ok := v.(string)
	if !ok {
		return "", refused(ed.path, "a blank line follows its value inside {…}")
	}
	return strconv.Quote(str), nil
}
