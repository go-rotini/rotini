package rotini

import (
	"regexp"
	"strings"
)

// ansiSequences matches CSI sequences (SGR styling among them) and OSC sequences terminated by
// BEL or ST.
var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// esc begins every ANSI escape sequence; text without one is returned without running the regexp.
const esc = '\x1b'

// StripANSI removes every ANSI escape sequence from text, SGR styling and OSC alike, leaving the
// characters a terminal would display. rotini applies it to man pages, markdown pages and
// completion descriptions, whose consumers would print escapes literally.
func StripANSI(text string) string {
	if !strings.ContainsRune(text, esc) {
		return text
	}
	return ansiSequences.ReplaceAllString(text, "")
}
