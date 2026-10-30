package codegen

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// roff escaping for the man feature's template.
//
// A man page is roff source: a line starting with "." or "'" is a request and "\" starts an
// escape. Spec-supplied text must be made inert before it reaches the page. These helpers are
// in every template's FuncMap, so a custom man template can use them too.
//
// The rules:
//   - "\" becomes "\e", which prints a backslash.
//   - "-" becomes "\-", so --verbose stays a copyable hyphen-minus, not a typographic hyphen.
//   - "'" and "`" become "\(aq" and "\(ga", so quotes in code are not curled.
//   - A line that would still start with "." gets a leading "\&", which makes it text.
//   - Non-ASCII characters become "\[uXXXX]", which groff and mandoc both read.
//   - Tabs become spaces; other control characters are dropped.

// roffEscapeLine escapes one line of text for a roff text line.
func roffEscapeLine(s string) string {
	return textLine(roffEscape(s))
}

// textLine keeps an escaped line that would start with "." from being read as a request.
func textLine(out string) string {
	if strings.HasPrefix(out, ".") {
		out = `\&` + out
	}
	return out
}

// roffCode is roffInline for text holding markdown code spans, as the pages' rules do: each
// span is set in bold, without its backtick fence (a fence of two or more backticks lets the
// code hold one). A backtick that opens no span stays literal.
func roffCode(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '`')
		if open < 0 {
			break
		}
		n := backtickRun(s, open)
		end := closingRun(s, open+n, n)
		if end < 0 {
			b.WriteString(roffEscape(s[:open+n]))
			s = s[open+n:]
			continue
		}
		code := s[open+n : end]
		if len(code) > 1 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
			code = code[1 : len(code)-1]
		}
		b.WriteString(roffEscape(s[:open]))
		b.WriteString(`\fB`)
		b.WriteString(roffEscape(code))
		b.WriteString(`\fR`)
		s = s[end+n:]
	}
	b.WriteString(roffEscape(s))
	return textLine(b.String())
}

// backtickRun is the length of the run of backticks starting at s[i].
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// closingRun is where the first run of exactly n backticks at or after s[from] starts, or -1.
func closingRun(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := backtickRun(s, i)
		if run == n {
			return i
		}
		i += run
	}
	return -1
}

// roffEscape escapes text for a roff text line, leaving the leading "." check to textLine.
func roffEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch {
		case r == '\\':
			b.WriteString(`\e`)
		case r == '-':
			b.WriteString(`\-`)
		case r == '\'':
			b.WriteString(`\(aq`)
		case r == '`':
			b.WriteString(`\(ga`)
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Dropped.
		case r > 0x7f:
			fmt.Fprintf(&b, `\[u%04X]`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// roffInline escapes text for use inside one roff line: line breaks become spaces.
func roffInline(s string) string {
	return roffEscapeLine(strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " "))
}

// roffLines escapes each line of s separately, keeping the line breaks, for text inside a
// no-fill (.nf) block such as a usage override or an example.
func roffLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = roffEscapeLine(strings.TrimRight(l, " \t"))
	}
	return strings.Join(lines, "\n")
}

// roffBlock escapes a paragraph-structured block such as a description. Each line is escaped,
// and a run of blank lines becomes one ".PP", since a blank roff line is stray vertical space,
// not a paragraph break. Indented lines (a command table, a code sample) go in a .nf no-fill
// block so roff keeps their columns instead of filling them into the paragraph.
func roffBlock(s string) string { return roffParagraphs(s, ".PP") }

// roffIndented escapes a block that sits inside a tagged paragraph (.TP), such as an option's
// description: like roffBlock, but paragraphs are separated by ".sp", since ".PP" would end the
// tagged paragraph and its indent.
func roffIndented(s string) string { return roffParagraphs(s, ".sp") }

// roffParagraphs is roffBlock with brk as the paragraph break request.
func roffParagraphs(s, brk string) string {
	var out []string
	pending, noFill := false, false
	for l := range strings.SplitSeq(strings.Trim(s, "\n"), "\n") {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if noFill {
				out = append(out, ".fi")
				noFill = false
			}
			pending = len(out) > 0
			continue
		}
		indented := l[0] == ' ' || l[0] == '\t'
		if pending {
			out = append(out, brk)
			pending = false
		}
		switch {
		case indented && !noFill:
			out = append(out, ".nf")
			noFill = true
		case !indented && noFill:
			out = append(out, ".fi")
			noFill = false
		}
		out = append(out, roffEscapeLine(strings.ReplaceAll(l, "\t", "    ")))
	}
	if noFill {
		out = append(out, ".fi")
	}
	return strings.Join(out, "\n")
}

// roffArg makes s one double-quoted argument of a roff request such as .TH or .SH: escaped as
// text, on one line, with any double quote written as "\(dq" so it cannot end the argument.
func roffArg(s string) string {
	return `"` + strings.ReplaceAll(roffInline(s), `"`, `\(dq`) + `"`
}
