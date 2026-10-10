package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/rotini/internal/cfgedit"
	"github.com/go-rotini/yaml"
)

// The document kinds `rotini fmt` knows the canonical key order of.
const (
	FormatKindSpec    = "spec"
	FormatKindConf    = "conf"
	FormatKindCommand = "command"
)

// fmtIndent is the canonical indentation step.
const fmtIndent = 2

// noReorder names keys whose mapping keeps the author's key order even though the schema
// declares its properties: the order is the author's to choose.
var noReorder = map[string]bool{"headings": true, "default": true}

// FormatYAML returns src, a rotini spec, conf or composed command document in YAML, in
// canonical form: keys in reading order (see fmtRanks), two-space indentation with
// sequences indented under their key, and runs of blank lines collapsed to one. Comments stay
// with the entries they sit above or beside; scalars, flow collections and block scalar bodies
// are copied byte for byte. kind is FormatKindSpec, FormatKindConf, FormatKindCommand, or ""
// to tell from the document's keys, then from name (the file's name).
func FormatYAML(src []byte, kind, name string) ([]byte, error) {
	doc, err := cfgedit.IndexYAML(src)
	if err != nil {
		if errors.Is(err, cfgedit.ErrMultipleDocuments) {
			return nil, errors.New("files with more than one YAML document can't be formatted")
		}
		return nil, fmt.Errorf("can't format: %w", err)
	}
	root := doc.Root
	if root == nil {
		return src, nil
	}
	if root.Kind != cfgedit.YAMLMapping || root.Flow {
		return nil, errors.New("the top level isn't a block mapping")
	}
	schemas, err := loadFmtSchemas()
	if err != nil {
		return nil, err
	}
	if kind == "" {
		if kind = detectFormatKind(root, name); kind == "" {
			return nil, errors.New("can't tell whether this is a spec, a conf or a composed command; pass --kind")
		}
	}
	var sch *fmtSchema
	switch kind {
	case FormatKindSpec:
		sch = schemas.spec
	case FormatKindConf:
		sch = schemas.conf
	case FormatKindCommand:
		sch = schemas.command
	default:
		return nil, fmt.Errorf("unknown document kind %q", kind)
	}
	p := &yamlPrinter{doc: doc, src: src, nl: lineEnding(src)}
	out, err := p.document(root, sch)
	if err != nil {
		return nil, err
	}
	if err := sameValues(src, out); err != nil {
		return nil, err
	}
	return out, nil
}

// detectFormatKind tells a document's kind from its top-level keys, then from its file name.
func detectFormatKind(root *cfgedit.YAMLNode, name string) string {
	keys := map[string]bool{}
	for _, p := range root.Pairs {
		keys[p.Key.Value] = true
	}
	switch {
	case keys["command"]:
		return FormatKindSpec
	case keys["generate"] || keys["validate"]:
		return FormatKindConf
	case keys["name"] || keys["$ref"]:
		return FormatKindCommand
	}
	base := filepath.Base(name)
	switch {
	case strings.Contains(base, ".rotini.spec."):
		return FormatKindSpec
	case strings.Contains(base, ".rotini.conf."):
		return FormatKindConf
	}
	return ""
}

// sameValues checks that the formatted document decodes to the same values as the original:
// formatting only moves text, so anything else is a printer bug, and nothing is written.
func sameValues(before, after []byte) error {
	var a, b any
	if err := yaml.Unmarshal(before, &a); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	if err := yaml.Unmarshal(after, &b); err != nil {
		return fmt.Errorf("formatting would break the file (%w); please report it", err)
	}
	if !reflect.DeepEqual(a, b) {
		return errors.New("formatting would change the file's values; please report it")
	}
	return nil
}

// lineEnding returns the line ending a file uses, from its first line.
func lineEnding(src []byte) string {
	i := bytes.IndexByte(src, '\n')
	if i > 0 && src[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// outLine is one line of formatted output. A verbatim line is content (inside a block scalar
// or a value spanning lines) and is never collapsed or dropped.
type outLine struct {
	text     string
	verbatim bool
}

// yamlPrinter re-emits a document line by line. Every source line belongs to one entry: its
// key line, the lines of its value, or the comment and blank lines around it. Entries move as
// whole blocks, and each line keeps its text after the indentation, which is rewritten.
type yamlPrinter struct {
	doc *cfgedit.YAMLDoc
	src []byte
	nl  string
}

// srcLine is a source line: its start offset, its indentation and its text after that.
type srcLine struct {
	start  int
	indent int
	text   string
}

func (l srcLine) blank() bool   { return l.text == "" }
func (l srcLine) comment() bool { return strings.HasPrefix(l.text, "#") }

// lines returns the source lines starting in [from, to).
func (p *yamlPrinter) lines(from, to int) []srcLine {
	var out []srcLine
	for ls := from; ls < to && ls < len(p.src); ls = p.doc.NextLine(ls) {
		raw := string(p.src[ls:p.doc.LineEnd(ls)])
		trimmed := strings.TrimLeft(raw, " ")
		out = append(out, srcLine{start: ls, indent: len(raw) - len(trimmed), text: strings.TrimRight(trimmed, " \t")})
	}
	return out
}

// gap returns the lines from the line after off's to the line holding next, checking they are
// only comments and blank lines.
func (p *yamlPrinter) gap(off, next int) ([]srcLine, error) {
	ls := p.doc.LineStart(next)
	from := p.doc.NextLine(off)
	if from > ls {
		return nil, nil
	}
	got := p.lines(from, ls)
	for _, l := range got {
		if !l.blank() && !l.comment() {
			return nil, p.unexpected(l.start)
		}
	}
	return got, nil
}

func (p *yamlPrinter) unexpected(off int) error {
	return fmt.Errorf("line %d: can't tell which entry this line belongs to", bytes.Count(p.src[:off], []byte("\n"))+1)
}

func (p *yamlPrinter) document(root *cfgedit.YAMLNode, sch *fmtSchema) ([]byte, error) {
	start := 0
	if bytes.HasPrefix(p.src, []byte("\xef\xbb\xbf")) {
		start = 3
	}
	var out []outLine
	for _, l := range p.lines(start, p.doc.LineStart(root.Body)) {
		out = append(out, outLine{text: spaces(l.indent) + l.text})
	}
	for _, pr := range root.Pairs {
		line := p.src[pr.Key.Start:p.doc.LineEnd(pr.Key.Start)]
		marker := (bytes.HasPrefix(line, []byte("---")) || bytes.HasPrefix(line, []byte("..."))) &&
			(len(line) == 3 || line[3] == ' ' || line[3] == '\t')
		if line[0] == '%' || marker {
			return nil, fmt.Errorf("the top-level key %q would read as a directive or document marker in the first column", pr.Key.Value)
		}
	}
	tail := p.lines(p.doc.NextLine(root.End), len(p.src))
	for _, l := range tail {
		if !l.blank() && !l.comment() && !strings.HasPrefix(l.text, "...") {
			return nil, p.unexpected(l.start)
		}
	}
	body, err := p.mapping(root, 0, sch, nil, tail, false)
	if err != nil {
		return nil, err
	}
	out = append(out, body...)
	var b bytes.Buffer
	b.Write(p.src[:start])
	for i, l := range collapse(out) {
		if i > 0 {
			b.WriteString(p.nl)
		}
		b.WriteString(l.text)
	}
	b.WriteString(p.nl)
	return b.Bytes(), nil
}

// collapse folds runs of blank lines into one and drops blank lines at the end. Verbatim lines
// are kept as they are.
func collapse(in []outLine) []outLine {
	out := make([]outLine, 0, len(in))
	for _, l := range in {
		if !l.verbatim && strings.TrimSpace(l.text) == "" {
			if len(out) == 0 || (!out[len(out)-1].verbatim && out[len(out)-1].text == "") {
				continue
			}
			l.text = ""
		}
		out = append(out, l)
	}
	for len(out) > 0 && !out[len(out)-1].verbatim && out[len(out)-1].text == "" {
		out = out[:len(out)-1]
	}
	return out
}

// splitGap splits the comment and blank lines between two entries at column col: the leading
// comments indented past col close the previous entry; the rest open the next.
func splitGap(lines []srcLine, col int) (foot, head []srcLine) {
	end := 0
	for i, l := range lines {
		if l.blank() {
			continue
		}
		if l.indent <= col {
			break
		}
		end = i + 1
	}
	return lines[:end], lines[end:]
}

// splitTrail splits the lines after a block's last entry: comments indented past col go with
// that entry, the others close the block. A blank line goes with the comment after it.
func splitTrail(lines []srcLine, col int) (deep, shallow []srcLine) {
	var pending []srcLine
	for _, l := range lines {
		if l.blank() {
			pending = append(pending, l)
			continue
		}
		if l.indent > col {
			deep = append(deep, pending...)
			deep = append(deep, l)
		} else {
			shallow = append(shallow, pending...)
			shallow = append(shallow, l)
		}
		pending = nil
	}
	return deep, shallow
}

// comments re-indents comment lines to col; blank lines stay blank.
func comments(lines []srcLine, col int, dropLeadingBlanks bool) []outLine {
	out := make([]outLine, 0, len(lines))
	for _, l := range lines {
		if l.blank() {
			if dropLeadingBlanks && len(out) == 0 {
				continue
			}
			out = append(out, outLine{})
			continue
		}
		out = append(out, outLine{text: spaces(col) + l.text})
	}
	return out
}

func spaces(n int) string { return strings.Repeat(" ", max(n, 0)) }

// mapping emits block mapping m with its keys at col. lead is the comment lines before its
// first entry, trail the comment lines after its last.
func (p *yamlPrinter) mapping(m *cfgedit.YAMLNode, col int, sch *fmtSchema, lead, trail []srcLine, nested bool) ([]outLine, error) {
	n := len(m.Pairs)
	heads := make([][]srcLine, n)
	trails := make([][]srcLine, n)
	heads[0] = lead
	for i := 1; i < n; i++ {
		g, err := p.gap(m.Pairs[i-1].Value.End, m.Pairs[i].Key.Start)
		if err != nil {
			return nil, err
		}
		trails[i-1], heads[i] = splitGap(g, m.Col)
	}
	deep, foot := splitTrail(trail, m.Col)
	trails[n-1] = deep
	var out []outLine
	for k, i := range p.order(m, sch) {
		out = append(out, comments(heads[i], col, k == 0 && nested)...)
		pair, err := p.pair(m.Pairs[i], col, sch.property(m.Pairs[i].Key.Value), trails[i])
		if err != nil {
			return nil, err
		}
		out = append(out, pair...)
	}
	return append(out, comments(foot, col, false)...), nil
}

// pair emits one mapping entry with its key at col.
func (p *yamlPrinter) pair(pr *cfgedit.YAMLPair, col int, sch *fmtSchema, trail []srcLine) ([]outLine, error) {
	keyLine := p.doc.LineStart(pr.Key.Start)
	v := pr.Value
	out := []outLine{{text: spaces(col) + p.firstLine(pr.Key.Start, v)}}
	if !v.Flow && (v.Kind == cfgedit.YAMLMapping || v.Kind == cfgedit.YAMLSequence) {
		lead, err := p.gap(keyLine, v.Body)
		if err != nil {
			return nil, err
		}
		if noReorder[pr.Key.Value] {
			sch = nil
		}
		body, err := p.block(v, col+fmtIndent, sch, lead, trail)
		return append(out, body...), err
	}
	out = append(out, p.continuation(v, keyLine, pr.Col, col)...)
	return append(out, comments(trail, col, false)...), nil
}

// block emits a nested block mapping or sequence at col.
func (p *yamlPrinter) block(v *cfgedit.YAMLNode, col int, sch *fmtSchema, lead, trail []srcLine) ([]outLine, error) {
	if v.Kind == cfgedit.YAMLMapping {
		return p.mapping(v, col, sch, lead, trail, true)
	}
	return p.sequence(v, col, sch.itemSchema(), lead, trail)
}

// continuation emits the lines after firstLine that hold the rest of scalar, alias or flow
// value v, whose owning key or dash moves from column from to column to. A continued plain,
// quoted or flow value keeps its lines' places relative to the owner, at least two columns in.
// A block scalar without an indentation indicator gets its body two columns past the owner;
// with one, the body keeps its place relative to the owner, as the indicator requires.
func (p *yamlPrinter) continuation(v *cfgedit.YAMLNode, firstLine, from, to int) []outLine {
	if v.Empty || p.doc.LineStart(v.End) == firstLine {
		return nil
	}
	lines := p.lines(p.doc.NextLine(firstLine), p.doc.NextLine(v.End))
	out := make([]outLine, 0, len(lines))
	if v.Style != yaml.LiteralStyle && v.Style != yaml.FoldedStyle {
		// A value that starts on a line of its own goes two columns in, and the lines after
		// that line two further.
		hdr := p.doc.LineStart(v.Body)
		for _, l := range lines {
			floor := to + fmtIndent
			if hdr != firstLine && l.start > hdr {
				floor += fmtIndent
			}
			if l.blank() {
				out = append(out, outLine{verbatim: true})
				continue
			}
			out = append(out, outLine{text: spaces(max(l.indent+to-from, floor)) + p.rawText(l), verbatim: true})
		}
		return out
	}
	// The header ("|", ">-") may sit on a line of its own below the key.
	if hdr := p.doc.LineStart(v.Body); hdr != firstLine {
		for len(lines) > 0 && lines[0].start <= hdr {
			if lines[0].blank() {
				out = append(out, outLine{verbatim: true})
			} else {
				out = append(out, outLine{text: spaces(to+fmtIndent) + p.rawText(lines[0]), verbatim: true})
			}
			lines = lines[1:]
		}
	}
	body := -1
	for _, l := range lines {
		if !l.blank() {
			body = l.indent
			break
		}
	}
	explicit := explicitIndent(p.src[v.Body:])
	shift := to - from
	if body >= 0 && !explicit {
		shift = to + fmtIndent - body
	}
	for _, l := range lines {
		raw := string(p.src[l.start:p.doc.LineEnd(l.start)])
		lead := len(raw) - len(strings.TrimLeft(raw, " "))
		if strings.TrimSpace(raw) == "" {
			// Whitespace past the body's indentation is content; keep it.
			text := ""
			if explicit || (body >= 0 && len(raw) > body) || strings.Contains(raw, "\t") {
				text = spaces(lead+shift) + raw[lead:]
			}
			out = append(out, outLine{text: text, verbatim: true})
			continue
		}
		out = append(out, outLine{text: spaces(lead+shift) + raw[lead:], verbatim: true})
	}
	return out
}

// firstLine returns the text of an entry's first line from off, its key or dash. Trailing
// blanks are dropped unless value v continues onto the next line, where they may be part of a
// quoted value.
func (p *yamlPrinter) firstLine(off int, v *cfgedit.YAMLNode) string {
	end := p.doc.LineEnd(off)
	text := string(p.src[off:end])
	if v.Empty || v.End <= end || (!v.Flow && (v.Kind == cfgedit.YAMLMapping || v.Kind == cfgedit.YAMLSequence)) {
		return strings.TrimRight(text, " \t")
	}
	return text
}

// rawText returns a source line's text after its indentation, trailing blanks included.
func (p *yamlPrinter) rawText(l srcLine) string {
	return string(p.src[l.start+l.indent : p.doc.LineEnd(l.start)])
}

// explicitIndent reports whether the block scalar header at the start of b has an indentation
// indicator (|2, >-1).
func explicitIndent(b []byte) bool {
	for i := 1; i < len(b) && i < 4; i++ {
		switch {
		case b[i] >= '1' && b[i] <= '9':
			return true
		case b[i] != '+' && b[i] != '-':
			return false
		}
	}
	return false
}

// sequence emits block sequence s with its dashes at col.
func (p *yamlPrinter) sequence(s *cfgedit.YAMLNode, col int, sch *fmtSchema, lead, trail []srcLine) ([]outLine, error) {
	n := len(s.Items)
	heads := make([][]srcLine, n)
	trails := make([][]srcLine, n)
	heads[0] = lead
	for i := 1; i < n; i++ {
		g, err := p.gap(s.Items[i-1].Value.End, s.Items[i].Dash)
		if err != nil {
			return nil, err
		}
		trails[i-1], heads[i] = splitGap(g, s.Col)
	}
	deep, foot := splitTrail(trail, s.Col)
	trails[n-1] = deep
	var out []outLine
	for i, it := range s.Items {
		out = append(out, comments(heads[i], col, i == 0)...)
		item, err := p.item(it, col, sch, trails[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item...)
	}
	return append(out, comments(foot, col, false)...), nil
}

// item emits one sequence entry with its dash at col.
func (p *yamlPrinter) item(it *cfgedit.YAMLItem, col int, sch *fmtSchema, trail []srcLine) ([]outLine, error) {
	v := it.Value
	dashLine := p.doc.LineStart(it.Dash)
	dashText := p.firstLine(it.Dash, v)
	if v.Flow || (v.Kind != cfgedit.YAMLMapping && v.Kind != cfgedit.YAMLSequence) {
		out := []outLine{{text: spaces(col) + dashText}}
		out = append(out, p.continuation(v, dashLine, it.Col, col)...)
		return append(out, comments(trail, col+fmtIndent, false)...), nil
	}
	if p.doc.LineStart(v.Body) != dashLine {
		lead, err := p.gap(dashLine, v.Body)
		if err != nil {
			return nil, err
		}
		body, err := p.block(v, col+fmtIndent, sch, lead, trail)
		return append([]outLine{{text: spaces(col) + dashText}}, body...), err
	}
	body, err := p.block(v, col+fmtIndent, sch, nil, trail)
	if err != nil {
		return nil, err
	}
	// The entry's first line joins the dash: "- name: x".
	body[0].text = spaces(col) + "- " + strings.TrimPrefix(body[0].text, spaces(col+fmtIndent))
	return body, nil
}

// order returns the indices of m's entries in canonical order: the known keys as fmtRanks
// orders them, then any others in their original order. A mapping keeps its order when it has
// no schema, holds a merge key, or when moving an entry would put an alias before the anchor
// it names.
func (p *yamlPrinter) order(m *cfgedit.YAMLNode, sch *fmtSchema) []int {
	idx := make([]int, len(m.Pairs))
	for i := range idx {
		idx[i] = i
	}
	if sch == nil || len(sch.props) == 0 {
		return idx
	}
	for _, pr := range m.Pairs {
		if pr.Merge {
			return idx
		}
	}
	rank := fmtRanks(sch)
	unknown := len(rank)
	sorted := slices.Clone(idx)
	slices.SortStableFunc(sorted, func(a, b int) int {
		ra, ok := rank[m.Pairs[a].Key.Value]
		if !ok {
			ra = unknown
		}
		rb, ok := rank[m.Pairs[b].Key.Value]
		if !ok {
			rb = unknown
		}
		return ra - rb
	})
	if !anchorsBeforeAliases(m, sorted) {
		return idx
	}
	return sorted
}

// anchorsBeforeAliases reports whether, in the given entry order, every alias in m's entries
// still comes after the anchor it names when both are in m.
func anchorsBeforeAliases(m *cfgedit.YAMLNode, order []int) bool {
	defined := map[string]int{}
	for pos, i := range order {
		walkAnchors(m.Pairs[i].Value, func(anchor, alias string) {
			if anchor != "" {
				defined[anchor] = pos
			}
		})
	}
	ok := true
	for pos, i := range order {
		walkAnchors(m.Pairs[i].Value, func(_, alias string) {
			if at, found := defined[alias]; alias != "" && found && at > pos {
				ok = false
			}
		})
	}
	return ok
}

func walkAnchors(n *cfgedit.YAMLNode, fn func(anchor, alias string)) {
	if n == nil {
		return
	}
	if n.Anchor != "" {
		fn(n.Anchor, "")
	}
	if n.Kind == cfgedit.YAMLAlias {
		fn("", n.Value)
	}
	for _, pr := range n.Pairs {
		walkAnchors(pr.Value, fn)
	}
	for _, it := range n.Items {
		walkAnchors(it.Value, fn)
	}
}
