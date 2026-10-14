package rotini

import "strings"

// esc begins every ANSI escape sequence; text without one is returned as it is.
const esc = '\x1b'

// StripANSI removes every ANSI escape sequence from text, SGR styling and OSC alike, leaving the
// characters a terminal would display. Rotini applies it to man pages, markdown pages and
// completion descriptions, whose consumers would print escapes literally.
//
// The sequences are ECMA-48's: a CSI sequence is ESC "[", parameter bytes (0x30–0x3f),
// intermediate bytes (0x20–0x2f) and one final byte (0x40–0x7e); an OSC, DCS, SOS, PM or APC
// string is ESC and one of "]", "P", "X", "^", "_", then a body ended by BEL or by ESC "\"; any
// other sequence is ESC, intermediate bytes, and one final byte (0x30–0x7e), such as ESC "(B"
// or ESC "c". An ESC that starts none of these, or a sequence cut off by the end of text, is
// kept.
func StripANSI(text string) string {
	if !strings.ContainsRune(text, esc) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == esc && i+1 < len(text) {
			if end, ok := ansiSequenceEnd(text, i); ok {
				i = end
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ansiSequenceEnd reports where the escape sequence starting at text[i] (an ESC) ends: the index
// of its last byte.
func ansiSequenceEnd(text string, i int) (int, bool) {
	switch text[i+1] {
	case '[':
		return csiEnd(text, i+2)
	case ']', 'P', 'X', '^', '_':
		return controlStringEnd(text, i+2)
	}
	j := skipBytes(text, i+1, 0x20, 0x2f) // ESC, intermediate bytes, then a final byte
	if j < len(text) && text[j] >= 0x30 && text[j] <= 0x7e {
		return j, true
	}
	return 0, false
}

// csiEnd is where a CSI sequence whose parameters start at text[j] ends: parameter bytes,
// intermediate bytes, then a final byte.
func csiEnd(text string, j int) (int, bool) {
	j = skipBytes(text, skipBytes(text, j, 0x30, 0x3f), 0x20, 0x2f)
	if j < len(text) && text[j] >= 0x40 && text[j] <= 0x7e {
		return j, true
	}
	return 0, false
}

// controlStringEnd is where an OSC, DCS, SOS, PM or APC string whose body starts at text[j]
// ends: at BEL, or at the ST (ESC \) that closes it.
func controlStringEnd(text string, j int) (int, bool) {
	for j < len(text) && text[j] != 0x07 && text[j] != esc {
		j++
	}
	switch {
	case j < len(text) && text[j] == 0x07:
		return j, true
	case j+1 < len(text) && text[j] == esc && text[j+1] == '\\':
		return j + 1, true
	}
	return 0, false
}

// skipBytes is the index of the first byte of text from j that is outside lo..hi.
func skipBytes(text string, j int, lo, hi byte) int {
	for j < len(text) && text[j] >= lo && text[j] <= hi {
		j++
	}
	return j
}

// Page is one generated documentation page for one command: a man page or a markdown reference
// page. With the man feature on, codegen emits a ManPages function returning every command's man
// page; with the markdown feature on, MarkdownPages. Both list the visible commands in tree
// order, root first.
//
//	for _, p := range cmd.ManPages() {
//		path := filepath.Join(dir, p.Name+"."+cmd.ManSection)
//		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
//			return err
//		}
//	}
type Page struct {
	// Name is the command path joined with "-" and lowercased ("taskr-add"): the name `man`
	// looks the page up by, and the man page's file name without its extension.
	Name string
	// Path is the command path below the root, ["add"]; nil for the root command.
	Path []string
	// Content is the page itself: roff for a man page, markdown for a markdown page.
	Content string
	// Topic is true for a help topic's page (the spec's `topics:`), listed after the commands'
	// pages; its Path is the topic's name.
	Topic bool
}
