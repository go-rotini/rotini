package rotini

import (
	"fmt"
	"regexp"
	"strings"
)

// This file is rotini's ONLY output-formatting surface, and it is deliberately
// narrow: SGR text styling (bold/italic/color) and stripping. It does NOT
// reopen the deleted UX layer — no tables, prompts, progress bars, spinners,
// or terminal control. Styling text before it reaches a writer, and removing
// styling that arrived already-applied (spec-authored ANSI in help/man keys),
// is the whole job.

// KeyStyles is the conventional registry key a program binds a [Styles] service
// under, for handlers to retrieve. Like every rotini service it is opt-in:
// nothing styles output unless a handler asks a bound Styles to.
const KeyStyles = "styles"

// Attribute is one SGR styling parameter — a text attribute ([Bold], [Italic],
// [Underline], …) or a color ([FgRed], [BgBlue], …). Compose attributes into a
// reusable [Style] with [Styles.With]. For a parameter rotini does not name
// (256-color, truecolor), use [Raw].
type Attribute string

// Text attributes.
const (
	Bold          Attribute = "1"
	Faint         Attribute = "2"
	Italic        Attribute = "3"
	Underline     Attribute = "4"
	Blink         Attribute = "5"
	Reverse       Attribute = "7"
	Strikethrough Attribute = "9"
)

// Foreground colors — the 16-color palette (8 normal + 8 bright).
const (
	FgBlack   Attribute = "30"
	FgRed     Attribute = "31"
	FgGreen   Attribute = "32"
	FgYellow  Attribute = "33"
	FgBlue    Attribute = "34"
	FgMagenta Attribute = "35"
	FgCyan    Attribute = "36"
	FgWhite   Attribute = "37"

	FgBrightBlack   Attribute = "90"
	FgBrightRed     Attribute = "91"
	FgBrightGreen   Attribute = "92"
	FgBrightYellow  Attribute = "93"
	FgBrightBlue    Attribute = "94"
	FgBrightMagenta Attribute = "95"
	FgBrightCyan    Attribute = "96"
	FgBrightWhite   Attribute = "97"
)

// Background colors — the 16-color palette (8 normal + 8 bright).
const (
	BgBlack   Attribute = "40"
	BgRed     Attribute = "41"
	BgGreen   Attribute = "42"
	BgYellow  Attribute = "43"
	BgBlue    Attribute = "44"
	BgMagenta Attribute = "45"
	BgCyan    Attribute = "46"
	BgWhite   Attribute = "47"

	BgBrightBlack   Attribute = "100"
	BgBrightRed     Attribute = "101"
	BgBrightGreen   Attribute = "102"
	BgBrightYellow  Attribute = "103"
	BgBrightBlue    Attribute = "104"
	BgBrightMagenta Attribute = "105"
	BgBrightCyan    Attribute = "106"
	BgBrightWhite   Attribute = "107"
)

// Raw is the escape hatch for an SGR parameter rotini does not name: a 256-color
// foreground ("38;5;208"), a truecolor background ("48;2;255;128;0"), or any
// other valid SGR sequence body. The string is the parameter(s) between the
// CSI "\x1b[" and the "m" — rotini does not validate it.
func Raw(sgr string) Attribute { return Attribute(sgr) }

// Styles is rotini's opt-in text-styling service. It carries one piece of
// state — an optional condition deciding whether styling is applied — and mints
// reusable [Style] values via [Styles.With]:
//
//	st := rotini.NewStyles(rotini.WithCondition(func() bool { return !noColor }))
//	errStyle := st.With(rotini.Bold, rotini.FgRed)
//	fmt.Fprintln(rtx.Stderr, errStyle.Sprint("something went wrong"))
//
// rotini ships NO automatic terminal/NO_COLOR detection — whether to style is
// the program's call, expressed through [WithCondition] (which can check
// $NO_COLOR, $CI, a --no-styles flag, whether the writer is a terminal, or
// anything else). With no condition, a Styles always applies its attributes.
type Styles struct {
	enabled func() bool // nil => always on; else style iff enabled() is true
}

// StylesOption configures a [Styles].
type StylesOption func(*Styles)

// WithCondition gates a [Styles]: the styles it mints apply their attributes
// only when fn returns true, and pass text through unchanged otherwise. fn is
// consulted on every [Style.Sprint], so it can reflect runtime state — a
// --no-styles flag, $NO_COLOR, $CI, whether output is a terminal — and rotini
// imposes none of those policies itself. With no condition a Styles always
// styles.
func WithCondition(fn func() bool) StylesOption {
	return func(s *Styles) { s.enabled = fn }
}

// NewStyles returns a [Styles] ready to bind under a registry key
// (conventionally [KeyStyles]) or use directly.
func NewStyles(opts ...StylesOption) *Styles {
	s := &Styles{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// With composes attrs into a reusable [Style]. Empty attributes are skipped;
// With() with no attributes yields a no-op Style.
func (s *Styles) With(attrs ...Attribute) Style {
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		if a != "" {
			parts = append(parts, string(a))
		}
	}
	return Style{sgr: strings.Join(parts, ";"), enabled: s.enabled}
}

// Style is a reusable set of SGR attributes bound to its [Styles]' condition.
// Build one with [Styles.With] and apply it with [Style.Sprint] / [Style.Sprintf].
// The zero Style (and any Style with no attributes) applies nothing.
type Style struct {
	sgr     string
	enabled func() bool
}

// Sprint returns text wrapped in the Style's SGR sequence and a reset — or text
// unchanged when the Style has no attributes or its [Styles]' condition reports
// styling off.
func (st Style) Sprint(text string) string {
	if st.sgr == "" || (st.enabled != nil && !st.enabled()) {
		return text
	}
	return "\x1b[" + st.sgr + "m" + text + "\x1b[0m"
}

// Sprintf is [Style.Sprint] over a formatted string.
func (st Style) Sprintf(format string, a ...any) string {
	return st.Sprint(fmt.Sprintf(format, a...))
}

// ansiSequences matches the terminal escape sequences styling introduces: CSI
// sequences (ESC [ … final byte — SGR bold/italic/color among them) and OSC
// sequences (ESC ] … terminated by BEL or ST — OSC-8 hyperlinks among them).
// Stripping an OSC-8 hyperlink keeps its visible text and drops the link.
var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// StripStyles removes all terminal styling — CSI and OSC escape sequences — from
// text. It is the counterpart to [Styles] for text that arrives ALREADY styled:
// spec-authored ANSI in help/man/markdown keys, or any pre-styled string headed
// for a writer that should not see escapes.
//
// With no condition it always strips. With one or more conditions it strips
// only when at least one returns true — the gate a handler uses to honor a
// --no-styles flag, $NO_COLOR, or any signal it chooses:
//
//	noStyles := func() bool { return flags.NoStyles || env.NoStyles }
//	fmt.Fprintln(rtx.Stdout, rotini.StripStyles(HelpText, noStyles))
func StripStyles(text string, conditions ...func() bool) string {
	if len(conditions) == 0 {
		return ansiSequences.ReplaceAllString(text, "")
	}
	for _, cond := range conditions {
		if cond != nil && cond() {
			return ansiSequences.ReplaceAllString(text, "")
		}
	}
	return text
}
