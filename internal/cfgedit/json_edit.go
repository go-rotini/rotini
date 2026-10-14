package cfgedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rotini/jsonc"
)

// jsonEditor splices JSON and JSONC through jsonc's positions. strict is plain JSON: no
// comments or trailing commas are ever written.
type jsonEditor struct {
	strict bool
}

type jsonNode struct {
	kind       jsonc.NodeKind
	start, end int
	members    []*jsonMember
}

type jsonMember struct {
	key      string
	keyStart int
	value    *jsonNode
}

type jsonEdit struct {
	src  []byte
	path []string
	nl   string
	step int
	tab  bool // the file indents with tabs
	root *jsonNode
}

func newJSONEdit(src []byte, path []string, strict bool) (*jsonEdit, error) {
	ed := &jsonEdit{src: src, path: path, nl: newline(src), step: 2}
	body := src[bomLen(src):]
	switch {
	case strict && len(bytes.TrimSpace(body)) == 0, !strict && jsonBlank(src):
		return ed, nil
	case strict && !json.Valid(body):
		return nil, errors.New("parse json: the file isn't valid JSON")
	}
	f, err := jsonc.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	ed.root, err = ed.index(f.Root, bomLen(src))
	if err != nil {
		return nil, err
	}
	ed.step = ed.indentStep(ed.root)
	ed.tab = ed.indentsWithTabs(ed.root)
	return ed, nil
}

// indentsWithTabs reports whether the first member written on its own line is indented with a
// tab.
func (ed *jsonEdit) indentsWithTabs(n *jsonNode) bool {
	if n == nil {
		return false
	}
	for _, m := range n.members {
		if ls := lineStart(ed.src, m.keyStart); ls != lineStart(ed.src, n.start) && m.keyStart > ls {
			return ed.src[ls] == '\t'
		}
		if ed.indentsWithTabs(m.value) {
			return true
		}
	}
	return false
}

// pad is n bytes of indentation in the file's style.
func (ed *jsonEdit) pad(n int) string {
	if ed.tab {
		return strings.Repeat("\t", n)
	}
	return spaces(n)
}

// leadAt counts the spaces and tabs that start the line at start.
func leadAt(src []byte, start int) int {
	n := 0
	for start+n < len(src) && (src[start+n] == ' ' || src[start+n] == '\t') {
		n++
	}
	return n
}

// index builds the node tree. jsonc reports offsets past a byte-order mark, so shift adds it
// back.
func (ed *jsonEdit) index(n *jsonc.Node, shift int) (*jsonNode, error) {
	out := &jsonNode{kind: n.Kind, start: n.Pos.Offset + shift}
	switch n.Kind {
	case jsonc.ObjectNode, jsonc.ArrayNode:
		end, ok := jsonMatch(ed.src, out.start)
		if !ok {
			return nil, refused(ed.path, "an unclosed object or array")
		}
		out.end = end
	default:
		out.end = out.start + len(n.RawValue)
		if n.RawValue == "" {
			out.end = jsonTokenEnd(ed.src, out.start)
		}
	}
	if n.Kind != jsonc.ObjectNode {
		return out, nil
	}
	for _, c := range n.Children {
		if c.Kind != jsonc.KeyValueNode || len(c.Children) != 1 {
			continue
		}
		v, err := ed.index(c.Children[0], shift)
		if err != nil {
			return nil, err
		}
		out.members = append(out.members, &jsonMember{key: c.Key, keyStart: c.Pos.Offset + shift, value: v})
	}
	return out, nil
}

// indentStep returns how far the file indents an object's members past its opening line; 2
// when no object spans lines.
func (ed *jsonEdit) indentStep(n *jsonNode) int {
	if n == nil || n.kind != jsonc.ObjectNode {
		return 2
	}
	if len(n.members) > 0 {
		first := n.members[0].keyStart
		if lineStart(ed.src, first) != lineStart(ed.src, n.start) {
			if step := column(ed.src, first) - leadAt(ed.src, lineStart(ed.src, n.start)); step > 0 {
				return step
			}
		}
	}
	for _, m := range n.members {
		if step := ed.indentStep(m.value); step != 2 {
			return step
		}
	}
	return 2
}

// checkObject refuses an object that holds a key twice, where readers disagree on which wins.
func (ed *jsonEdit) checkObject(n *jsonNode) error {
	seen := make(map[string]bool, len(n.members))
	for _, m := range n.members {
		if seen[m.key] {
			return refused(ed.path, "the key %q appears twice in one object", m.key)
		}
		seen[m.key] = true
	}
	return nil
}

func findJSONMember(n *jsonNode, key string) (int, *jsonMember) {
	at := -1
	for i, m := range n.members {
		if m.key == key {
			at = i
		}
	}
	if at < 0 {
		return -1, nil
	}
	return at, n.members[at]
}

func (e jsonEditor) set(src []byte, path []string, v any) ([]byte, error) {
	ed, err := newJSONEdit(src, path, e.strict)
	if err != nil {
		return nil, err
	}
	if ed.root == nil {
		text := "{" + ed.nl + ed.pad(ed.step) + jsonString(path[0]) + ": " + ed.nested(path[1:], v, ed.step, true) + ed.nl + "}" + ed.nl
		if !endsWithNewline(src) {
			text = ed.nl + text
		}
		return splice(src, len(src), len(src), text), nil
	}
	if ed.root.kind != jsonc.ObjectNode {
		return nil, refused(path, "the file's top level isn't an object")
	}
	obj := ed.root
	for i, seg := range path {
		if err := ed.checkObject(obj); err != nil {
			return nil, err
		}
		_, m := findJSONMember(obj, seg)
		if m == nil {
			return ed.insert(obj, path[i:], v, e.strict), nil
		}
		old := m.value
		switch {
		case i == len(path)-1 && old.kind == jsonc.ObjectNode:
			return nil, refused(path, "it holds an object, not a value")
		case i == len(path)-1:
			return splice(src, old.start, old.end, jsonValue(v)), nil
		case old.kind == jsonc.ObjectNode:
			obj = old
		case old.kind == jsonc.NullNode:
			col := column(src, m.keyStart)
			return splice(src, old.start, old.end, ed.nested(path[i+1:], v, col, ed.multiline(obj))), nil
		default:
			return nil, refused(path, "%s holds a value, not an object", strings.Join(path[:i+1], "."))
		}
	}
	return src, nil
}

// nested renders the value for path ending in v: v itself, or objects nesting it. col is the
// column of the member holding the value.
func (ed *jsonEdit) nested(path []string, v any, col int, multiline bool) string {
	if len(path) == 0 {
		return jsonValue(v)
	}
	inner := jsonString(path[0]) + ": " + ed.nested(path[1:], v, col+ed.step, multiline)
	if !multiline {
		return "{" + inner + "}"
	}
	return "{" + ed.nl + ed.pad(col+ed.step) + inner + ed.nl + ed.pad(col) + "}"
}

// multiline reports whether obj writes its members on their own lines.
func (ed *jsonEdit) multiline(obj *jsonNode) bool {
	if len(obj.members) > 0 {
		return lineStart(ed.src, obj.members[0].keyStart) != lineStart(ed.src, obj.start)
	}
	return obj == ed.root || bytes.ContainsAny(ed.src[obj.start:obj.end], "\n")
}

// insert adds the member for path to obj, after its last member, keeping the object's layout
// and (in JSONC) its trailing-comma style.
func (ed *jsonEdit) insert(obj *jsonNode, path []string, v any, strict bool) []byte {
	multi := ed.multiline(obj)
	openIndent := leadAt(ed.src, lineStart(ed.src, obj.start))
	col := openIndent + ed.step
	if len(obj.members) > 0 {
		col = column(ed.src, obj.members[len(obj.members)-1].keyStart)
	}
	member := jsonString(path[0]) + ": " + ed.nested(path[1:], v, col, multi)
	if len(obj.members) == 0 {
		inner := ed.src[obj.start+1 : obj.end-1]
		switch {
		case len(bytes.TrimSpace(inner)) != 0:
			return splice(ed.src, obj.start+1, obj.start+1, ed.nl+ed.pad(col)+member)
		case multi:
			return splice(ed.src, obj.start+1, obj.end-1, ed.nl+ed.pad(col)+member+ed.nl+ed.pad(openIndent))
		}
		return splice(ed.src, obj.start+1, obj.end-1, member)
	}
	last := obj.members[len(obj.members)-1].value
	next := jsonSkipTrivia(ed.src, last.end)
	trailing := !strict && next < len(ed.src) && ed.src[next] == ','
	if trailing {
		if !multi {
			return splice(ed.src, next+1, next+1, " "+member+",")
		}
		at := ed.lineTail(next + 1)
		return splice(ed.src, at, at, ed.nl+ed.pad(col)+member+",")
	}
	if !multi {
		return splice(ed.src, last.end, last.end, ", "+member)
	}
	at := ed.lineTail(last.end)
	if at == last.end {
		return splice(ed.src, last.end, last.end, ","+ed.nl+ed.pad(col)+member)
	}
	out := splice(ed.src, at, at, ed.nl+ed.pad(col)+member)
	return splice(out, last.end, last.end, ",")
}

// lineTail returns where an insert after off goes so the comments after off stay with off's
// line: the end of the line when the rest of it is only blanks, block comments and a line
// comment, following a block comment that runs onto later lines to the end of the line it closes
// on. When other text follows, it is off.
func (ed *jsonEdit) lineTail(off int) int {
	at := off
	for {
		at = skipBlanks(ed.src, at)
		if !bytes.HasPrefix(ed.src[at:], []byte("/*")) {
			break
		}
		end := bytes.Index(ed.src[at+2:], []byte("*/"))
		if end < 0 {
			return off
		}
		next := at + 2 + end + 2
		if bytes.ContainsAny(ed.src[at:next], "\n") {
			if rest := skipBlanks(ed.src, next); rest != lineEnd(ed.src, next) && !bytes.HasPrefix(ed.src[rest:], []byte("//")) {
				return off
			}
		}
		at = next
	}
	if at == lineEnd(ed.src, at) || bytes.HasPrefix(ed.src[at:], []byte("//")) {
		return lineEnd(ed.src, at)
	}
	return off
}

func (e jsonEditor) unset(src []byte, path []string, keep int) ([]byte, error) {
	ed, err := newJSONEdit(src, path, e.strict)
	if err != nil {
		return nil, err
	}
	if ed.root == nil || ed.root.kind != jsonc.ObjectNode {
		return src, nil
	}
	objs := make([]*jsonNode, 0, len(path))
	idx := make([]int, 0, len(path))
	obj := ed.root
	for i, seg := range path {
		if err := ed.checkObject(obj); err != nil {
			return nil, err
		}
		k, m := findJSONMember(obj, seg)
		if m == nil {
			return src, nil
		}
		objs, idx = append(objs, obj), append(idx, k)
		if i < len(path)-1 {
			if m.value.kind != jsonc.ObjectNode {
				return src, nil
			}
			obj = m.value
		}
	}
	t := len(path) - 1
	for t > 0 && t > keep && len(objs[t].members) == 1 {
		t--
	}
	return ed.remove(objs[t], idx[t]), nil
}

// remove deletes obj's member k with the comma that separates it, and its whole line when it
// sits alone on it.
func (ed *jsonEdit) remove(obj *jsonNode, k int) []byte {
	m := obj.members[k]
	start, end := m.keyStart, m.value.end
	prevComma := -1
	if c := jsonSkipTrivia(ed.src, end); c < len(ed.src) && ed.src[c] == ',' {
		end = c + 1
	} else if k > 0 {
		if c := jsonSkipTrivia(ed.src, obj.members[k-1].value.end); c < len(ed.src) && ed.src[c] == ',' {
			prevComma = c
		}
	}
	ls := lineStart(ed.src, start)
	switch {
	case len(bytes.TrimSpace(ed.src[ls:start])) == 0 && ed.lineTail(end) == lineEnd(ed.src, end):
		start, end = ls, nextLine(ed.src, end)
	case prevComma < 0:
		// The blanks after the comma went with the member: {"b": 2}, not { "b": 2}.
		for end < len(ed.src) && (ed.src[end] == ' ' || ed.src[end] == '\t') {
			end++
		}
	default:
		// And the blanks before it: {"a": 1}, not {"a": 1 }.
		for start > prevComma+1 && (ed.src[start-1] == ' ' || ed.src[start-1] == '\t') {
			start--
		}
	}
	out := splice(ed.src, start, end, "")
	if prevComma >= 0 {
		out = splice(out, prevComma, prevComma+1, "")
	}
	return out
}

// jsonBlank reports whether src holds no JSON value: only whitespace and comments.
func jsonBlank(src []byte) bool {
	return jsonSkipTrivia(src, bomLen(src)) == len(src)
}

// jsonSkipTrivia returns the first offset at or after i that isn't whitespace or a comment.
func jsonSkipTrivia(src []byte, i int) int {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n':
			i++
		case bytes.HasPrefix(src[i:], []byte("//")):
			i = lineEnd(src, i)
		case bytes.HasPrefix(src[i:], []byte("/*")):
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return i // unterminated: not trivia
			}
			i += 2 + end + 2
		default:
			return i
		}
	}
	return i
}

// jsonMatch returns the offset just past the bracket closing the one at off.
func jsonMatch(src []byte, off int) (int, bool) {
	depth := 0
	for i := off; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"':
			end, ok := quotedEnd(src, i)
			if !ok {
				return 0, false
			}
			i = end - 1
		case c == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			i = jsonSkipTrivia(src, i) - 1
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// jsonTokenEnd returns the end of a bare literal (true, false, null, a number) at off.
func jsonTokenEnd(src []byte, off int) int {
	i := off
	for i < len(src) && !strings.ContainsRune(" \t\r\n,}]/", rune(src[i])) {
		i++
	}
	return i
}
