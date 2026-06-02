package rtk

import "strings"

// ColorLevel is how much color a terminal supports. The levels are ordered, so they
// compare: level >= Color256 means "256 colors or better". A handler obtains the
// output's level from [Terminal.ColorLevel] and passes it to [Style] / [Color].
type ColorLevel int

const (
	ColorNone      ColorLevel = iota // monochrome — emit no color escapes
	Color16                          // the 8 + 8 bright ANSI colors
	Color256                         // the xterm 256-color palette
	ColorTrueColor                   // 24-bit truecolor
)

// String names the level (for logging and tests).
func (l ColorLevel) String() string {
	switch l {
	case ColorNone:
		return "none"
	case Color16:
		return "16"
	case Color256:
		return "256"
	case ColorTrueColor:
		return "truecolor"
	default:
		return "unknown"
	}
}

// detectColorLevel is the testable core behind [Terminal.ColorLevel]: isTTY is
// whether the output stream is a terminal, and look reads environment variables
// (os.LookupEnv in production, a fixed map in tests) so detection is deterministic
// without a real TTY or env. The priority order:
//
//   - NO_COLOR set to a non-empty value forces ColorNone (https://no-color.org).
//   - FORCE_COLOR pins a level: 0/false → none, 2 → 256, 3 → truecolor; any other
//     value (including "", "1", "true") forces color at the level the environment
//     otherwise implies (at least 16).
//   - CLICOLOR_FORCE other than "0" forces color even when the stream is not a TTY.
//   - CLICOLOR=0 disables color on a TTY unless one of the force flags is set.
//
// With no override, color is off unless the stream is a TTY, and the level is then
// read from COLORTERM (truecolor/24bit → truecolor) and TERM (*256color* → 256,
// *truecolor*/*direct* → truecolor, "dumb" → none, otherwise → 16).
func detectColorLevel(isTTY bool, look func(string) (string, bool)) ColorLevel {
	get := func(k string) string { v, _ := look(k); return v }

	if v, ok := look("NO_COLOR"); ok && v != "" {
		return ColorNone
	}

	forced := false
	if v, ok := look("FORCE_COLOR"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false":
			return ColorNone
		case "2":
			return Color256
		case "3":
			return ColorTrueColor
		default: // "", "1", "true", or anything else: forced, refine from env
			forced = true
		}
	}
	if v, ok := look("CLICOLOR_FORCE"); ok && v != "0" {
		forced = true
	}
	if get("CLICOLOR") == "0" && !forced {
		return ColorNone
	}

	if !forced && !isTTY {
		return ColorNone
	}
	return colorFromEnv(forced, get)
}

// colorFromEnv derives the level from COLORTERM/TERM once color is known to be on.
// forced raises the floor to Color16 for a "dumb" or absent TERM, since the caller
// explicitly asked for color.
func colorFromEnv(forced bool, get func(string) string) ColorLevel {
	switch strings.ToLower(get("COLORTERM")) {
	case "truecolor", "24bit":
		return ColorTrueColor
	}
	t := strings.ToLower(get("TERM"))
	switch {
	case t == "dumb":
		if forced {
			return Color16
		}
		return ColorNone
	case strings.Contains(t, "truecolor") || strings.Contains(t, "direct"):
		return ColorTrueColor
	case strings.Contains(t, "256color"):
		return Color256
	default:
		// A terminal with an unknown or empty TERM: 16 colors is the safe floor.
		return Color16
	}
}
