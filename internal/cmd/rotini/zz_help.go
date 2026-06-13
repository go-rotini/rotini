package rotini

import "regexp"

// ansiSequences matches the terminal escape sequences styling introduces: CSI
// sequences (ESC [ … final byte — SGR bold/italic/color among them) and OSC
// sequences (ESC ] … terminated by BEL or ST — OSC-8 hyperlinks among them).
// Stripping an OSC-8 hyperlink keeps its visible text and drops the link.
var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// StripStyles returns text with every terminal styling escape sequence removed
// when any of shouldStripBools is true; otherwise text is returned unchanged.
// The variadic shape lets a caller hand in every no-styles signal at once —
// the --no-styles flag, the ROTINI_NO_STYLES env var — with any one sufficing:
//
//	fmt.Fprintln(rtx.Stdout, StripStyles(HelpRotini, flags.NoStyles, env.NoStyles))
func StripStyles(text string, shouldStripBools ...bool) string {
	shouldStrip := false
	for _, s := range shouldStripBools {
		if s {
			shouldStrip = true
			break
		}
	}

	if shouldStrip {
		// strip all ascii term styling -- bold, italic, colors, etc and return the cleaned text
		return ansiSequences.ReplaceAllString(text, "")
	}

	return text
}
