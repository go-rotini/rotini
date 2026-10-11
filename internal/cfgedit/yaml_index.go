package cfgedit

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/go-rotini/yaml"
)

// ErrMultipleDocuments marks a YAML stream with more than one document, which cfgedit doesn't
// index or edit.
var ErrMultipleDocuments = errors.New("the file holds more than one YAML document")

// LayoutError is a construct the span index doesn't handle, such as a complex mapping key. It
// matches [ErrRefused].
type LayoutError struct {
	Line   int
	Reason string
}

func (e *LayoutError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Reason) }

// Is reports whether target is [ErrRefused].
func (*LayoutError) Is(target error) bool { return target == ErrRefused }

// YAMLDoc is the span index of a single-document YAML file: where each key, value and entry
// sits in Src, as byte offsets.
type YAMLDoc struct {
	Src  []byte
	Root *YAMLNode // nil when the document holds nothing but comments
}

// YAMLKind is the kind of a [YAMLNode].
type YAMLKind int

// The node kinds.
const (
	YAMLMapping YAMLKind = iota
	YAMLSequence
	YAMLScalar
	YAMLAlias
)

// YAMLNode is one node's place in the source.
type YAMLNode struct {
	Kind   YAMLKind
	Flow   bool             // a {…} or […] collection
	Style  yaml.ScalarStyle // a scalar's style
	Anchor string
	Tag    string
	Value  string // a scalar's decoded value; an alias's anchor name

	// Start is the node's first byte, including an anchor or tag written before it. Body is
	// the first byte after those: a scalar's first character, a flow collection's bracket, a
	// block collection's first key or dash. End is just past the last byte: a block scalar's
	// last body line, or a block collection's last entry. The line comment after a value is
	// never inside the span.
	Start, Body, End int

	Col   int  // a block collection's key or dash column
	Empty bool // a null written as nothing; its span is empty, just after the ':' or '-'

	Pairs []*YAMLPair // a mapping's entries, in source order
	Items []*YAMLItem // a sequence's entries
}

// YAMLPair is a mapping entry.
type YAMLPair struct {
	Key   *YAMLNode // always a scalar
	Value *YAMLNode
	Col   int  // the key's column
	Colon int  // the ':' offset
	Merge bool // a "<<" merge key
}

// YAMLItem is a sequence entry.
type YAMLItem struct {
	Dash  int // the '-' offset; -1 in a flow sequence
	Col   int // the dash's column
	Value *YAMLNode
}

// IndexYAML parses src and returns its span index. A stream with several documents returns
// [ErrMultipleDocuments]; a construct the index doesn't handle returns a [*LayoutError].
func IndexYAML(src []byte) (*YAMLDoc, error) {
	f, err := yaml.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	doc := &YAMLDoc{Src: src}
	if loneCR(src) || bytes.Contains(src, []byte("\u0085")) || bytes.Contains(src, []byte("\u2028")) || bytes.Contains(src, []byte("\u2029")) {
		return nil, &LayoutError{Line: 1, Reason: "the file uses a line break other than \\n or \\r\\n"}
	}
	if i := bytes.IndexFunc(src, func(r rune) bool { return r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f }); i >= 0 {
		return nil, &LayoutError{Line: bytes.Count(src[:i], []byte("\n")) + 1, Reason: "the file holds a control character YAML doesn't allow"}
	}
	if len(f.Docs) > 1 {
		return nil, ErrMultipleDocuments
	}
	if len(f.Docs) == 0 || len(f.Docs[0].Children) == 0 {
		return doc, nil
	}
	ix := yamlIndexer{src: src}
	root, err := ix.node(f.Docs[0].Children[0], -1, false)
	if err != nil {
		return nil, err
	}
	if !root.Flow && root.Kind != YAMLScalar && !yamlTrivia(src[:lineStart(src, root.Body)]) {
		return nil, ix.layout(root.Body, "content before the document's first entry")
	}
	doc.Root = root
	return doc, nil
}

// LineStart returns the offset of the first byte of off's line.
func (d *YAMLDoc) LineStart(off int) int { return lineStart(d.Src, off) }

// LineEnd returns the offset just past the last content byte of off's line, before its line
// ending.
func (d *YAMLDoc) LineEnd(off int) int { return lineEnd(d.Src, off) }

// NextLine returns the offset of the line after off's, or len(Src).
func (d *YAMLDoc) NextLine(off int) int { return nextLine(d.Src, off) }

// Column returns off's column in bytes.
func (d *YAMLDoc) Column(off int) int { return column(d.Src, off) }

type yamlIndexer struct {
	src []byte
}

func (ix *yamlIndexer) layout(off int, reason string) error {
	return &LayoutError{Line: bytes.Count(ix.src[:min(off, len(ix.src))], []byte("\n")) + 1, Reason: reason}
}

// node indexes n. parentCol is the indentation a continuation line must exceed: the owning
// key's or dash's column, or -1 at the root.
func (ix *yamlIndexer) node(n *yaml.Node, parentCol int, flow bool) (*YAMLNode, error) {
	off := n.Pos.Offset
	out := &YAMLNode{Anchor: n.Anchor, Tag: n.Tag, Value: n.Value, Style: n.Style, Flow: n.Flow, Start: off, Body: off}
	var err error
	switch n.Kind {
	case yaml.MappingNode:
		out.Kind = YAMLMapping
		err = ix.mapping(out, n, flow || n.Flow)
	case yaml.SequenceNode:
		out.Kind = YAMLSequence
		err = ix.sequence(out, n, flow || n.Flow)
	case yaml.ScalarNode:
		out.Kind = YAMLScalar
		out.End, err = ix.scalarEnd(n, parentCol, flow)
	case yaml.AliasNode:
		out.Kind = YAMLAlias
		out.Value = n.Alias
		out.End = ix.tokenEnd(off+1, flow)
	default:
		return nil, ix.layout(off, "unexpected node")
	}
	return out, err
}

func (ix *yamlIndexer) mapping(out *YAMLNode, n *yaml.Node, flow bool) error {
	if n.Flow {
		end, err := ix.matchBracket(n.Pos.Offset)
		if err != nil {
			return err
		}
		out.End = end
	} else {
		out.Col = column(ix.src, n.Pos.Offset)
		if err := ix.blockStart(n.Pos.Offset); err != nil {
			return err
		}
	}
	for i := 0; i+1 < len(n.Children); i += 2 {
		p, err := ix.pair(n.Children[i], n.Children[i+1], flow)
		if err != nil {
			return err
		}
		out.Pairs = append(out.Pairs, p)
	}
	if !n.Flow {
		if len(out.Pairs) == 0 {
			return ix.layout(n.Pos.Offset, "empty block mapping")
		}
		if out.Pairs[0].Key.Start != n.Pos.Offset {
			return ix.layout(n.Pos.Offset, "an explicit (?) mapping key")
		}
		out.End = out.Pairs[len(out.Pairs)-1].Value.End
	}
	return nil
}

// key indexes a mapping key, which must be a plain or quoted scalar with no properties, and
// in a block mapping alone on its line apart from indentation and sequence dashes.
func (ix *yamlIndexer) key(k *yaml.Node, flow bool) (*YAMLNode, error) {
	kOff := k.Pos.Offset
	if k.Kind != yaml.ScalarNode || k.Anchor != "" || k.Tag != "" || (k.Value == "" && k.Style == yaml.PlainStyle) {
		return nil, ix.layout(kOff, "a mapping key that isn't a plain or quoted scalar")
	}
	if !flow {
		if err := ix.blockStart(kOff); err != nil {
			return nil, err
		}
	}
	key := &YAMLNode{Kind: YAMLScalar, Style: k.Style, Value: k.Value, Start: kOff, Body: kOff}
	if k.Style != yaml.SingleQuotedStyle && k.Style != yaml.DoubleQuotedStyle {
		key.End = ix.plainKeyEnd(kOff, flow)
		return key, nil
	}
	var err error
	key.End, err = ix.quotedEnd(kOff)
	return key, err
}

func (ix *yamlIndexer) pair(k, v *yaml.Node, flow bool) (*YAMLPair, error) {
	key, err := ix.key(k, flow)
	if err != nil {
		return nil, err
	}
	kOff := key.Start
	p := &YAMLPair{Key: key, Col: column(ix.src, kOff), Merge: k.Value == "<<" && k.Style == yaml.PlainStyle}
	i := skipBlanks(ix.src, key.End)
	if i >= len(ix.src) || ix.src[i] != ':' {
		return nil, ix.layout(kOff, "a mapping key without ':' after it")
	}
	p.Colon = i
	if v.Kind == yaml.ScalarNode && v.Value == "" && v.Style == yaml.PlainStyle && v.Tag == "" && v.Anchor == "" && v.Pos.Offset <= kOff {
		at := p.Colon + 1
		p.Value = &YAMLNode{Kind: YAMLScalar, Empty: true, Start: at, Body: at, End: at}
		return p, nil
	}
	p.Value, err = ix.node(v, p.Col, flow)
	if err != nil {
		return nil, err
	}
	if val := p.Value; !val.Flow && ((val.Kind == YAMLMapping && val.Col <= p.Col) || (val.Kind == YAMLSequence && val.Col < p.Col)) {
		return nil, ix.layout(kOff, "a nested block that isn't indented under its key")
	}
	ix.properties(p.Value, p.Colon+1)
	return p, nil
}

func (ix *yamlIndexer) sequence(out *YAMLNode, n *yaml.Node, flow bool) error {
	if n.Flow {
		end, err := ix.matchBracket(n.Pos.Offset)
		if err != nil {
			return err
		}
		out.End = end
		for _, c := range n.Children {
			v, err := ix.node(c, -1, true)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, &YAMLItem{Dash: -1, Value: v})
		}
		return nil
	}
	// The sequence's position is its first dash, or, in a compact "- - a", the entry after it.
	from := n.Pos.Offset
	if ix.src[from] != '-' {
		i := from - 1
		for i > 0 && (ix.src[i] == ' ' || ix.src[i] == '\t') {
			i--
		}
		if i < 0 || ix.src[i] != '-' {
			return ix.layout(from, "can't find a sequence entry's '-'")
		}
		from = i
	}
	out.Col = column(ix.src, from)
	out.Body, out.Start = from, from
	if err := ix.blockStart(from); err != nil {
		return err
	}
	for _, c := range n.Children {
		dash, ok := ix.findDash(from, out.Col)
		if !ok {
			return ix.layout(from, "can't find a sequence entry's '-'")
		}
		item := &YAMLItem{Dash: dash, Col: out.Col}
		if c.Kind == yaml.ScalarNode && c.Value == "" && c.Style == yaml.PlainStyle && c.Tag == "" && c.Anchor == "" && c.Pos.Offset <= dash {
			item.Value = &YAMLNode{Kind: YAMLScalar, Empty: true, Start: dash + 1, Body: dash + 1, End: dash + 1}
		} else {
			v, err := ix.node(c, out.Col, flow)
			if err != nil {
				return err
			}
			ix.properties(v, dash+1)
			item.Value = v
		}
		out.Items = append(out.Items, item)
		from = item.Value.End
	}
	if len(out.Items) == 0 {
		return ix.layout(n.Pos.Offset, "empty block sequence")
	}
	out.End = out.Items[len(out.Items)-1].Value.End
	return nil
}

// blockStart checks that only indentation and sequence dashes come before a block
// collection's key or dash on its line.
func (ix *yamlIndexer) blockStart(off int) error {
	for _, c := range ix.src[lineStart(ix.src, off):off] {
		if c != ' ' && c != '-' {
			return ix.layout(off, "a block that starts after other content on its line")
		}
	}
	return nil
}

// findDash finds the first sequence-entry dash at col at or after from: the dash at from
// itself (which may follow another, as in "- - a"), or the first later line whose byte at col
// is a '-' followed by a space or the line's end, with only spaces before it.
func (ix *yamlIndexer) findDash(from, col int) (int, bool) {
	for ls := lineStart(ix.src, from); ls < len(ix.src); ls = nextLine(ix.src, ls) {
		at := ls + col
		end := lineEnd(ix.src, ls)
		if at < from || at >= end || ix.src[at] != '-' || (at != from && indentAt(ix.src, ls) < col) {
			continue
		}
		if at+1 == end || ix.src[at+1] == ' ' || ix.src[at+1] == '\t' {
			return at, true
		}
	}
	return 0, false
}

// properties moves v's Start back over an anchor or tag written between from (just after the
// ':' or '-') and its body.
func (ix *yamlIndexer) properties(v *YAMLNode, from int) {
	if v.Anchor == "" && v.Tag == "" {
		return
	}
	i := skipBlanks(ix.src, from)
	for i < len(ix.src) && (ix.src[i] == '\n' || ix.src[i] == '\r') {
		i = skipBlanks(ix.src, i+1)
	}
	if i < v.Body && (ix.src[i] == '&' || ix.src[i] == '!') {
		v.Start = i
	}
}

func (ix *yamlIndexer) scalarEnd(n *yaml.Node, parentCol int, flow bool) (int, error) {
	off := n.Pos.Offset
	switch n.Style {
	case yaml.SingleQuotedStyle, yaml.DoubleQuotedStyle:
		return ix.quotedEnd(off)
	case yaml.LiteralStyle, yaml.FoldedStyle:
		return ix.blockScalarEnd(off, parentCol)
	}
	if n.Value == "" {
		return 0, ix.layout(off, "an empty value with an anchor or tag")
	}
	if flow {
		return ix.flowPlainEnd(off), nil
	}
	return ix.plainEnd(off, parentCol, n.Value), nil
}

// skipBlanks returns the first offset at or after i that isn't a space or tab.
func skipBlanks(src []byte, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

// commentStart returns where a " #" comment starts in src[from:to], or to.
func commentStart(src []byte, from, to int) int {
	for i := from; i < to; i++ {
		if src[i] == '#' && (i == 0 || src[i-1] == ' ' || src[i-1] == '\t') {
			return i
		}
	}
	return to
}

// trimRight returns the offset after the last non-blank byte in src[from:to].
func trimRight(src []byte, from, to int) int {
	for to > from && (src[to-1] == ' ' || src[to-1] == '\t') {
		to--
	}
	return to
}

// plainEnd finds a block-context plain scalar's end. A scalar continued on later lines (its
// text on the first line differs from its value) runs over every following line indented past
// parentCol that isn't a comment.
func (ix *yamlIndexer) plainEnd(off, parentCol int, value string) int {
	le := lineEnd(ix.src, off)
	end := trimRight(ix.src, off, commentStart(ix.src, off, le))
	if string(ix.src[off:end]) == value {
		return end
	}
	for ls := nextLine(ix.src, end); ls < len(ix.src); ls = nextLine(ix.src, ls) {
		if blankLine(ix.src, ls) {
			continue
		}
		ind := indentAt(ix.src, ls)
		if ind <= parentCol || ix.src[ls+ind] == '#' || docMarker(ix.src, ls) {
			break
		}
		le := lineEnd(ix.src, ls)
		end = trimRight(ix.src, ls, commentStart(ix.src, ls+ind, le))
	}
	return end
}

// docMarker reports whether the line at ls is a "---" or "..." document marker.
func docMarker(src []byte, ls int) bool {
	rest := src[ls:lineEnd(src, ls)]
	return (bytes.HasPrefix(rest, []byte("---")) || bytes.HasPrefix(rest, []byte("..."))) &&
		(len(rest) == 3 || rest[3] == ' ' || rest[3] == '\t')
}

// flowPlainEnd finds a plain scalar's end inside a flow collection.
func (ix *yamlIndexer) flowPlainEnd(off int) int {
	i := off
	for i < len(ix.src) {
		c := ix.src[i]
		if c == ',' || c == ']' || c == '}' || c == '\n' || c == '\r' {
			break
		}
		if c == '#' && i > off && (ix.src[i-1] == ' ' || ix.src[i-1] == '\t') {
			break
		}
		if c == ':' && (i+1 == len(ix.src) || isFlowBreak(ix.src[i+1])) {
			break
		}
		i++
	}
	return trimRight(ix.src, off, i)
}

func isFlowBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ',', ']', '}', '[', '{':
		return true
	}
	return false
}

// plainKeyEnd finds a plain key's end: the ':' that ends it, trimmed.
func (ix *yamlIndexer) plainKeyEnd(off int, flow bool) int {
	if flow {
		return ix.flowPlainEnd(off)
	}
	le := lineEnd(ix.src, off)
	for i := off; i < le; i++ {
		if ix.src[i] == ':' && (i+1 == le || ix.src[i+1] == ' ' || ix.src[i+1] == '\t') {
			return trimRight(ix.src, off, i)
		}
	}
	return trimRight(ix.src, off, le)
}

// quotedEnd returns the offset just past the closing quote of the quoted scalar at off.
func (ix *yamlIndexer) quotedEnd(off int) (int, error) {
	end, ok := quotedEnd(ix.src, off)
	if !ok {
		return 0, ix.layout(off, "an unterminated quoted scalar")
	}
	return end, nil
}

func quotedEnd(src []byte, off int) (int, bool) {
	q := src[off]
	for i := off + 1; i < len(src); i++ {
		switch {
		case q == '"' && src[i] == '\\':
			i++
		case src[i] == q && q == '\'' && i+1 < len(src) && src[i+1] == '\'':
			i++
		case src[i] == q:
			return i + 1, true
		}
	}
	return 0, false
}

// blockScalarEnd finds a literal or folded scalar's end: its last body line, the body being
// the following lines indented at least as far as its first (or as its indicator says), and
// blank lines. With keep chomping (+) the trailing blank lines are content and included.
func (ix *yamlIndexer) blockScalarEnd(off, parentCol int) (int, error) {
	le := lineEnd(ix.src, off)
	hdr := off + 1
	for hdr < le && (ix.src[hdr] == '+' || ix.src[hdr] == '-' || (ix.src[hdr] >= '1' && ix.src[hdr] <= '9')) {
		hdr++
	}
	if rest := bytes.TrimLeft(ix.src[hdr:le], " \t"); len(rest) > 0 && (rest[0] != '#' || hdr == le-len(rest)) {
		return 0, ix.layout(off, "a block scalar header with more after it than a comment")
	}
	keep := bytes.IndexByte(ix.src[off:hdr], '+') >= 0
	end, blankEnd, content := hdr, hdr, -1
	if d := bytes.IndexAny(ix.src[off:hdr], "123456789"); d >= 0 {
		content = parentCol + int(ix.src[off+d]-'0')
	}
	for ls := nextLine(ix.src, off); ls < len(ix.src); ls = nextLine(ix.src, ls) {
		if blankLine(ix.src, ls) {
			blankEnd = lineEnd(ix.src, ls)
			// Whitespace past the body's indentation is content, not an empty line, and so is
			// a tab after the indentation.
			ind := indentAt(ix.src, ls)
			if (content >= 0 && ind > content) || (ind > parentCol && bytes.IndexByte(ix.src[ls:blankEnd], '\t') >= 0) {
				end = blankEnd
			}
			continue
		}
		ind := indentAt(ix.src, ls)
		if ind <= parentCol || (content >= 0 && ind < content) {
			break
		}
		if content < 0 {
			content = ind
		}
		end = lineEnd(ix.src, ls)
		blankEnd = end
	}
	if keep {
		return blankEnd, nil
	}
	return end, nil
}

// tokenEnd returns the end of an anchor or alias name starting at off.
func (ix *yamlIndexer) tokenEnd(off int, flow bool) int {
	i := off
	for i < len(ix.src) {
		c := ix.src[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || (flow && (c == ',' || c == ']' || c == '}')) {
			break
		}
		i++
	}
	return i
}

// matchBracket returns the offset just past the bracket closing the one at off, skipping
// quoted scalars and comments. A quote starts a quoted scalar only where a scalar starts: after
// a bracket, a comma or a ": "; elsewhere it is part of a plain scalar ("it's").
func (ix *yamlIndexer) matchBracket(off int) (int, error) {
	depth := 0
	atStart := true
	for i := off; i < len(ix.src); i++ {
		c := ix.src[i]
		if (c == '"' || c == '\'') && atStart {
			end, ok := quotedEnd(ix.src, i)
			if !ok {
				return 0, ix.layout(i, "an unterminated quoted scalar")
			}
			i, atStart = end-1, false
			continue
		}
		if c == '#' && i > 0 && (ix.src[i-1] == ' ' || ix.src[i-1] == '\t' || ix.src[i-1] == '\n') {
			i = lineEnd(ix.src, i) - 1
			continue
		}
		switch c {
		case '[', '{':
			depth++
			atStart = true
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
			atStart = false
		default:
			atStart = flowTokenStart(ix.src, i, atStart)
		}
	}
	return 0, ix.layout(off, "an unclosed flow collection")
}

// flowTokenStart reports whether a scalar may start after src[i] inside a flow collection: after
// a comma or a ": " (or a ':' right before a quote); blanks keep the current state.
func flowTokenStart(src []byte, i int, cur bool) bool {
	switch c := src[i]; c {
	case ',':
		return true
	case ':':
		return i+1 < len(src) && (isFlowBreak(src[i+1]) || src[i+1] == '"' || src[i+1] == '\'')
	case ' ', '\t', '\r', '\n':
		return cur
	}
	return false
}
