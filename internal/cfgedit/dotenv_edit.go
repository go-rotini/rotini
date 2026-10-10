package cfgedit

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/go-rotini/dotenv"
)

// dotenvEditor replaces or removes whole lines. dotenv's entries are contiguous and each
// starts at its line, so an entry's extent is from its position to the next entry's. A
// replaced line keeps its indentation, its "export " prefix, its separator and its trailing
// comment; only the value is rewritten.
type dotenvEditor struct{}

// dotenvLine is one assignment's extent in the source.
type dotenvLine struct {
	start, end int // the line, line ending included
}

func dotenvLines(src []byte, key string) ([]dotenvLine, error) {
	f, err := dotenv.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse dotenv: %w", err)
	}
	shift := bomLen(src)
	entries := f.Entries()
	var out []dotenvLine
	for i, e := range entries {
		if e.Kind != dotenv.EntryAssignment || e.Key != key {
			continue
		}
		end := len(src)
		if i+1 < len(entries) {
			end = entries[i+1].Pos.Offset + shift
		}
		out = append(out, dotenvLine{start: e.Pos.Offset + shift, end: end})
	}
	return out, nil
}

func (dotenvEditor) set(src []byte, path []string, v any) ([]byte, error) {
	if len(path) != 1 {
		return nil, refused(path, "a dotenv key is a single variable name")
	}
	key := path[0]
	if !posixName(key) {
		return nil, fmt.Errorf("cfgedit: %q isn't a valid dotenv variable name", key)
	}
	text, err := scalarText(v)
	if err != nil {
		return nil, err
	}
	value := dotenvValue(text)
	lines, err := dotenvLines(src, key)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		text := key + "=" + value + newline(src)
		if !endsWithNewline(src) {
			text = newline(src) + text
		}
		return splice(src, len(src), len(src), text), nil
	}
	// The last assignment is the one a reader sees.
	l := lines[len(lines)-1]
	line := src[l.start:l.end]
	body, ending := trimLineEnding(line)
	if prefix, tail, ok := dotenvSplit(body, key); ok {
		out := splice(src, l.start, l.end, prefix+value+tail+ending)
		if _, err := dotenv.Parse(out); err == nil && lastValue(out, key) == text {
			return out, nil
		}
	}
	return splice(src, l.start, l.end, dotenvExport(body)+key+"="+value+ending), nil
}

func (dotenvEditor) unset(src []byte, path []string, _ int) ([]byte, error) {
	if len(path) != 1 {
		return src, nil
	}
	lines, err := dotenvLines(src, path[0])
	if err != nil {
		return nil, err
	}
	out := src
	for _, l := range slices.Backward(lines) {
		out = splice(out, l.start, l.end, "")
	}
	return out, nil
}

func lastValue(src []byte, key string) string {
	f, err := dotenv.Parse(src)
	if err != nil {
		return ""
	}
	v, _ := f.Get(key)
	return v
}

// trimLineEnding splits a line from its "\n" or "\r\n".
func trimLineEnding(line []byte) (body []byte, ending string) {
	switch {
	case bytes.HasSuffix(line, []byte("\r\n")):
		return line[:len(line)-2], "\r\n"
	case bytes.HasSuffix(line, []byte("\n")):
		return line[:len(line)-1], "\n"
	}
	return line, ""
}

// dotenvExport returns the line's indentation and "export " prefix, if any.
func dotenvExport(body []byte) string {
	i := skipBlanks(body, 0)
	if bytes.HasPrefix(body[i:], []byte("export")) {
		j := skipBlanks(body, i+len("export"))
		if j > i+len("export") {
			return string(body[:j])
		}
	}
	return string(body[:i])
}

// dotenvSplit splits an assignment line around its value: prefix is everything up to the value
// (indentation, "export ", the key, the separator and blanks), tail everything after it (blanks
// and a trailing comment). ok is false for a shape it doesn't recognize, such as a quoted value
// running over several lines.
func dotenvSplit(body []byte, key string) (prefix, tail string, ok bool) {
	i := len(dotenvExport(body))
	if !bytes.HasPrefix(body[i:], []byte(key)) {
		return "", "", false
	}
	i = skipBlanks(body, i+len(key))
	if i >= len(body) || (body[i] != '=' && body[i] != ':') {
		return "", "", false
	}
	i = skipBlanks(body, i+1)
	end := trimRight(body, i, commentStart(body, i, len(body)))
	if i < len(body) && (body[i] == '"' || body[i] == '\'' || body[i] == '`') {
		e, closed := quotedEnd(body, i)
		if !closed {
			return "", "", false
		}
		end = e
	}
	return string(body[:i]), string(body[end:]), true
}

// dotenvValue renders a value bare when that reads back unchanged, and double-quoted
// otherwise, as go-rotini/dotenv writes values.
func dotenvValue(s string) string {
	if !strings.ContainsAny(s, " \t\n\r\v\f#$\"'\\`") && !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(s) {
		switch c := s[i]; c {
		case '"', '\\', '$':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// posixName reports whether key is a portable variable name, [A-Za-z_][A-Za-z0-9_]*, the only
// names every dotenv reader accepts.
func posixName(key string) bool {
	for i, c := range key {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return key != ""
}
