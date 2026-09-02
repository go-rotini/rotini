package rotini

import (
	"os"
	"strings"
)

// Terminal and color detection. Every function here is OPT-IN: rotini never calls
// them for you (Pillar 1). A program decides whether to style its output and feeds
// the answer in — see [Style.SetEnabled], [Styler.SetProfile], and the deliberate
// exception documented on [Spinner], which declines to smear a non-terminal.

// EnvNoColor reports whether the environment asks for no color, honoring the
// NO_COLOR convention and its CLICOLOR_FORCE override (a non-empty, non-"0"
// CLICOLOR_FORCE wins, meaning "color anyway").
func EnvNoColor() bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return false
	}
	return os.Getenv("NO_COLOR") != ""
}

// DetectProfile guesses the richest color [Profile] the environment supports, from
// COLORTERM and TERM, and returns [ProfileNoColor] when color is unwanted (see
// [EnvNoColor]) or the terminal is dumb.
//
// It is a guess from environment variables, not a capability query — pass the
// result to [Style.SetProfile] or [Styler.SetProfile] if you want it, or ignore it
// and choose your own.
func DetectProfile() Profile {
	if EnvNoColor() {
		return ProfileNoColor
	}
	switch colorterm := os.Getenv("COLORTERM"); colorterm {
	case "truecolor", "24bit":
		return ProfileTrueColor
	}
	term := os.Getenv("TERM")
	switch {
	case strings.Contains(term, "truecolor"):
		return ProfileTrueColor
	case strings.Contains(term, "256color"):
		return ProfileANSI256
	case term == "" || term == "dumb":
		return ProfileNoColor
	default:
		return ProfileANSI16
	}
}

// IsTerminal reports whether file is a character device — a terminal rather than a
// pipe, a regular file, or /dev/null. A nil file is not a terminal.
//
// It is the check behind "is anyone watching this?": paging, animating, and
// prompting all become wrong when the answer is no.
func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
