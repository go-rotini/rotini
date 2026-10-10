package cfgedit

import (
	"bytes"
	"strings"
)

// bom is the UTF-8 byte-order mark some editors write at the start of a file.
const bom = "\xef\xbb\xbf"

func bomLen(src []byte) int {
	if bytes.HasPrefix(src, []byte(bom)) {
		return len(bom)
	}
	return 0
}

// lineStart returns the offset of the first byte of the line holding off. A byte-order mark
// isn't part of the first line.
func lineStart(src []byte, off int) int {
	ls := bytes.LastIndexByte(src[:off], '\n') + 1
	if ls == 0 && off >= bomLen(src) {
		return bomLen(src)
	}
	return ls
}

// lineEnd returns the offset just past the last content byte of the line holding off: the
// position of its "\r\n" or "\n", or the end of src.
func lineEnd(src []byte, off int) int {
	i := bytes.IndexByte(src[off:], '\n')
	if i < 0 {
		return len(src)
	}
	end := off + i
	if end > 0 && src[end-1] == '\r' {
		end--
	}
	return end
}

// nextLine returns the offset of the line after the one holding off, or len(src).
func nextLine(src []byte, off int) int {
	i := bytes.IndexByte(src[off:], '\n')
	if i < 0 {
		return len(src)
	}
	return off + i + 1
}

// newline returns the line ending src uses, from its first line: "\r\n" or "\n".
func newline(src []byte) string {
	i := bytes.IndexByte(src, '\n')
	if i > 0 && src[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// column returns off's column: the bytes between the start of its line and off.
func column(src []byte, off int) int {
	return off - lineStart(src, off)
}

// indentAt returns the number of spaces opening the line that starts at start.
func indentAt(src []byte, start int) int {
	n := 0
	for start+n < len(src) && src[start+n] == ' ' {
		n++
	}
	return n
}

// blankLine reports whether the line starting at start holds only spaces and tabs.
func blankLine(src []byte, start int) bool {
	return len(bytes.TrimLeft(src[start:lineEnd(src, start)], " \t")) == 0
}

// commentLine reports whether the line starting at start is a comment line under the given
// marker ("#" or "//"), ignoring indentation.
func commentLine(src []byte, start int, marker string) bool {
	return bytes.HasPrefix(bytes.TrimLeft(src[start:lineEnd(src, start)], " \t"), []byte(marker))
}

// splice returns src with [start, end) replaced by text.
func splice(src []byte, start, end int, text string) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(text))
	out = append(out, src[:start]...)
	out = append(out, text...)
	return append(out, src[end:]...)
}

// spaces returns n spaces.
func spaces(n int) string { return strings.Repeat(" ", max(n, 0)) }

// endsWithNewline reports whether src is empty (a byte-order mark aside) or its last line is
// terminated.
func endsWithNewline(src []byte) bool {
	return len(src) == bomLen(src) || src[len(src)-1] == '\n'
}

// loneCR reports whether src has a carriage return that isn't part of a "\r\n" line ending.
func loneCR(src []byte) bool {
	for i := bytes.IndexByte(src, '\r'); i >= 0; {
		if i+1 >= len(src) || src[i+1] != '\n' {
			return true
		}
		next := bytes.IndexByte(src[i+1:], '\r')
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
}
