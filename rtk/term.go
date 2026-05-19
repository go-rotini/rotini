package rtk

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ColorLevel describes the color depth a terminal supports.
//
// Generated render code consults the active Term's ColorLevel to
// decide whether to emit ANSI escape codes. The minimum useful level
// for rotini is [Color16] — enough for `Red(...)` to colorize an
// error message. The post-v1.0 [rtk.Term] expansion will add the full
// styling surface.
type ColorLevel int

const (
	// ColorNone disables ANSI escapes. Returned for non-TTY writers,
	// dumb terminals, and any time NO_COLOR is set in the environment.
	ColorNone ColorLevel = 0

	// Color16 is the basic 16 ANSI colors (8 foreground + 8 bright).
	Color16 ColorLevel = 16

	// Color256 is the 256-color extended ANSI palette.
	Color256 ColorLevel = 256

	// Color16M is the 24-bit truecolor palette (RGB).
	Color16M ColorLevel = 16777216
)

// Term is the rtk-default terminal-introspection service. Handlers
// and generated render code consult it to decide whether to emit ANSI
// escape codes and which depth to use.
//
// The default implementation ([NewTerm]) inspects the supplied writer
// (typically rtk.IO.Stderr or os.Stdout) and the process environment
// (NO_COLOR, COLORTERM, TERM) to compute its answers once at
// construction time. Replace via the registry for tests that need
// deterministic output:
//
//	reg.Bind("term", fakeTerm{level: rtk.ColorNone})
type Term interface {
	// IsTTY reports whether the underlying writer is a terminal.
	// Returns false for pipes, redirects, and io.Discard.
	IsTTY() bool

	// ColorLevel returns the supported color depth. ColorNone means
	// the caller should emit plain text (no ANSI escapes).
	ColorLevel() ColorLevel
}

// NewTerm returns a Term inspecting w. When w is nil, the result
// reports IsTTY=false and ColorLevel=ColorNone — safe pass-through.
//
// Color-level detection rules (first match wins):
//
//   - NO_COLOR set (any value, per https://no-color.org) → ColorNone
//   - w is not a TTY                                     → ColorNone
//   - TERM empty or "dumb"                               → ColorNone
//   - COLORTERM contains "truecolor" or "24bit"          → Color16M
//   - TERM contains "256color"                           → Color256
//   - otherwise                                          → Color16
func NewTerm(w io.Writer) Term {
	isTTY := writerIsTTY(w)
	level := detectColorLevel(isTTY, os.Getenv("NO_COLOR"), os.Getenv("TERM"), os.Getenv("COLORTERM"))
	return &defaultTerm{tty: isTTY, level: level}
}

// defaultTerm is the rtk-default [Term] implementation. Both fields
// are computed once at construction and never change — Term values
// are intended to be passed by interface, not mutated.
type defaultTerm struct {
	tty   bool
	level ColorLevel
}

// IsTTY satisfies [Term].
func (t *defaultTerm) IsTTY() bool { return t.tty }

// ColorLevel satisfies [Term].
func (t *defaultTerm) ColorLevel() ColorLevel { return t.level }

// writerIsTTY reports whether w is a terminal. nil and io.Discard
// return false. For *os.File, the answer comes from
// os.File.Stat().Mode()&os.ModeCharDevice — the same heuristic
// [Reader.isPiped] uses.
func writerIsTTY(w io.Writer) bool {
	if w == nil {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// detectColorLevel implements the color-level rules described on
// [NewTerm]. Split out from NewTerm so unit tests can exercise the
// rule table without setting/unsetting process env vars.
func detectColorLevel(isTTY bool, noColor, term, colorterm string) ColorLevel {
	if noColor != "" {
		return ColorNone
	}
	if !isTTY {
		return ColorNone
	}
	if term == "" || term == "dumb" {
		return ColorNone
	}
	low := strings.ToLower(colorterm)
	if strings.Contains(low, "truecolor") || strings.Contains(low, "24bit") {
		return Color16M
	}
	if strings.Contains(term, "256color") {
		return Color256
	}
	return Color16
}

// Style helpers — minimal subset for render.gen.go's colored-error
// output. Each helper is a no-op pass-through when t.ColorLevel() is
// ColorNone, so callers can write `rtk.Red(t, msg)` unconditionally.

// Red wraps s in red ANSI escapes if t supports color. Plain
// pass-through otherwise.
func Red(t Term, s string) string { return wrapANSI(t, "31", s) }

// Yellow wraps s in yellow ANSI escapes if t supports color.
func Yellow(t Term, s string) string { return wrapANSI(t, "33", s) }

// Green wraps s in green ANSI escapes if t supports color.
func Green(t Term, s string) string { return wrapANSI(t, "32", s) }

// Bold wraps s in the SGR-1 (bold/bright) ANSI escape if t supports
// color.
func Bold(t Term, s string) string { return wrapANSI(t, "1", s) }

// wrapANSI applies a single SGR code to s, with reset at the end.
// Returns s unchanged when t is nil or reports ColorNone.
func wrapANSI(t Term, code, s string) string {
	if t == nil || t.ColorLevel() == ColorNone {
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
}
