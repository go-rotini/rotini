package rotini

import (
	"regexp"
	"strings"
)

// Stripping ANSI escape sequences out of text.
//
// This is the one piece of escape handling rotini keeps, because rotini generates text that has
// to arrive unstyled: a man page, a markdown page and a completion description are read by
// something that would print the escapes literally. Measuring display width, wrapping and
// truncating styled text belong to whatever library draws it — muesli/reflow and lipgloss do
// them properly, with grapheme-cluster segmentation rotini would only approximate.

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
