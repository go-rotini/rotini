package rotini

import "regexp"

// ansiSequences matches the terminal escape sequences styling introduces — CSI
// (ESC [ … final byte) and OSC (ESC ] … BEL/ST) — so the runtime can keep its
// shell-completion candidates plain. The public, end-user-facing counterpart is
// tortellini.StripStyles in the opt-in subpackage; core cannot import that
// subpackage (it depends on core), so this private helper carries the same
// pattern for core's own internal use.
var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// stripANSI removes CSI and OSC escape sequences from text.
func stripANSI(text string) string {
	return ansiSequences.ReplaceAllString(text, "")
}
