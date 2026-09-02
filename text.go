package rotini

import (
	"regexp"
	"strings"
	"unicode"
)

// Escape-aware text measurement: stripping ANSI sequences, measuring DISPLAY width in
// terminal cells, and OSC 8 hyperlinks. Used wherever styled text must line up —
// table columns above all.

var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// esc begins every ANSI escape sequence. Text without one cannot match
// ansiSequences, so Strip returns it untouched — a zero-allocation path for the
// overwhelmingly common case of text that was never styled. Width is built on Strip
// and inherits it.
const esc = '\x1b'

// Strip removes every ANSI escape sequence from text — SGR styling and OSC
// sequences alike — leaving the characters a terminal would actually display. It is
// what a program applies when a consumer asked for no styling, and what codegen
// applies to man and markdown pages, which have no place for terminal escapes.
func Strip(text string) string {
	if !strings.ContainsRune(text, esc) {
		return text
	}
	return ansiSequences.ReplaceAllString(text, "")
}

// Hyperlink wraps text in an OSC 8 terminal hyperlink pointing at url. Terminals
// that support it render text as a clickable link; the rest display text unchanged,
// so it is always safe to emit.
func Hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
}

// Width returns the display width of text in terminal cells: ANSI escape
// sequences contribute nothing, combining marks zero, wide East-Asian and emoji
// runes two, everything else one. It is a wcwidth-style approximation (not full
// grapheme-cluster segmentation), sufficient for aligning styled output.
func Width(text string) int {
	width := 0
	for _, r := range Strip(text) { // unstyled text costs no allocation here
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E,   // CJK radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF,   // Hiragana, Katakana, CJK symbols
		r >= 0x3400 && r <= 0x4DBF,   // CJK Extension A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF,   // Yi
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE4F,   // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60,   // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,   // Fullwidth signs
		r >= 0x1F300 && r <= 0x1FAFF, // emoji and symbols
		r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B+
		return true
	default:
		return false
	}
}
