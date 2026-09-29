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

// esc begins every ANSI escape sequence, so text without one cannot match ansiSequences and
// Strip returns it untouched — a zero-allocation path for text that was never styled.
const esc = '\x1b'

// Strip removes every ANSI escape sequence from text, SGR styling and OSC alike, leaving the
// characters a terminal would display. It is what a program applies when a consumer asked for
// no styling, and what codegen applies to man and markdown pages.
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

// Width returns the display width of text in terminal cells: escape sequences and combining
// marks contribute nothing, wide East-Asian and emoji runes two, everything else one. It is a
// wcwidth-style approximation, not grapheme-cluster segmentation, sufficient for aligning
// styled output.
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

// ── wrapping and truncation ──────────────────────────────────────────────────.
//
// Both measure in display cells and both keep escape sequences intact. Measuring is the part
// [Width] already solved; the part it left to the caller is doing the cut without splitting a
// sequence, which would leave the terminal wearing whatever style the fragment half-opened.

// segments splits text into escape sequences and the runes between them, so a caller can walk it
// counting only what the terminal would display while carrying the styling along.
//
// A sequence is emitted as one segment with zero width. That is what makes "never cut inside an
// escape" fall out of the loop rather than being a special case in it.
type segment struct {
	text  string // an escape sequence, or a single rune
	width int    // display cells: always 0 for a sequence
}

func segments(text string) []segment {
	if !strings.ContainsRune(text, esc) {
		out := make([]segment, 0, len(text))
		for _, r := range text {
			out = append(out, segment{text: string(r), width: runeWidth(r)})
		}
		return out
	}
	var out []segment
	locs := ansiSequences.FindAllStringIndex(text, -1)
	prev := 0
	for _, loc := range locs {
		for _, r := range text[prev:loc[0]] {
			out = append(out, segment{text: string(r), width: runeWidth(r)})
		}
		out = append(out, segment{text: text[loc[0]:loc[1]]})
		prev = loc[1]
	}
	for _, r := range text[prev:] {
		out = append(out, segment{text: string(r), width: runeWidth(r)})
	}
	return out
}

// Truncate shortens text to at most cols display cells, marking the cut with ellipsis — which
// costs its own display width out of the budget. Styling is preserved and never cut mid-sequence.
//
// A cols of zero or less, or text already inside the budget, returns text unchanged: "no limit"
// and "fits" are the same answer. An ellipsis wider than the budget is dropped rather than
// overflowing it, because a truncation that grows the string is worse than a blunt one.
//
//	rotini.Truncate("a very long line", 10, "…")  // "a very lo…"
//	rotini.Truncate(styled, 10, "")               // a hard cut, styling intact
func Truncate(text string, cols int, ellipsis string) string {
	if cols <= 0 || Width(text) <= cols {
		return text
	}
	mark := ellipsis
	budget := cols - Width(mark)
	if budget < 0 {
		mark, budget = "", cols
	}

	var b strings.Builder
	used := 0
	for _, seg := range segments(text) {
		if used+seg.width > budget {
			if seg.width > 0 {
				break // a visible rune that would overflow: stop
			}
			b.WriteString(seg.text) // a trailing reset still belongs in the output
			continue
		}
		b.WriteString(seg.text)
		used += seg.width
	}
	return b.String() + mark
}

// Wrap breaks text into lines of at most cols display cells, at spaces where it can and inside a
// word when it must. Existing newlines are kept as paragraph breaks.
//
// Styling survives a wrap without being reopened, because a newline does not reset a terminal's
// state — a color opened on one line is still in effect on the next. Escape sequences are
// carried with the word they precede and cost nothing against the budget.
//
// A cols of zero or less returns text unchanged, the same "no limit" answer [Truncate] gives and
// [Table] gives an unbounded width.
//
//	fmt.Fprintln(rtx.Stdout, rotini.Wrap(description, cols))
func Wrap(text string, cols int) string {
	if cols <= 0 || text == "" {
		return text
	}
	var out strings.Builder
	for i, para := range strings.Split(text, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(wrapLine(para, cols))
	}
	return out.String()
}

// wrapLine wraps one newline-free run of text.
func wrapLine(text string, cols int) string {
	var out, word strings.Builder
	lineWidth, wordWidth := 0, 0

	flush := func() {
		if word.Len() == 0 {
			return
		}
		switch {
		case lineWidth == 0:
			// Nothing on this line yet: the word starts it, however long it is.
		case lineWidth+1+wordWidth <= cols:
			out.WriteByte(' ')
			lineWidth++
		default:
			out.WriteByte('\n')
			lineWidth = 0
		}
		out.WriteString(word.String())
		lineWidth += wordWidth
		word.Reset()
		wordWidth = 0
	}

	for _, seg := range segments(text) {
		if seg.width == 0 && seg.text != " " {
			word.WriteString(seg.text) // an escape sequence rides with its word
			continue
		}
		if seg.text == " " {
			flush()
			continue
		}
		// A word longer than the whole budget is broken rather than overflowing.
		if wordWidth+seg.width > cols && cols > 0 {
			flush()
			if lineWidth > 0 {
				out.WriteByte('\n')
				lineWidth = 0
			}
		}
		word.WriteString(seg.text)
		wordWidth += seg.width
	}
	flush()
	return out.String()
}
