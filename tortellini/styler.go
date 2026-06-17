package tortellini

import (
	"fmt"
	"regexp"
	"strconv"
)

// KeyStyler is the conventional key under which a [Styler] is registered in a
// service registry — used, for example, by a rotini CLI whose handlers retrieve
// it to style output. Styling is opt-in: nothing is styled unless a handler asks.
const KeyStyler = "styler"

// Color is one of the sixteen standard ANSI palette colors (eight normal plus
// eight bright), applied with [Style.Foreground] or [Style.Background].
type Color int

const (
	Black Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
	BrightBlack
	BrightRed
	BrightGreen
	BrightYellow
	BrightBlue
	BrightMagenta
	BrightCyan
	BrightWhite
)

// Styler is an opt-in text-styling service. It carries one piece of state — an
// optional condition deciding whether styling is applied at all — and mints
// fluent [Style] builders bound to that condition via [Styler.Style].
//
// It ships no automatic terminal or NO_COLOR detection: whether to style is the
// program's call, expressed through [WithCondition] (which can consult $NO_COLOR,
// $CI, a --no-color flag, whether the writer is a terminal, or anything else).
// With no condition a Styler always styles.
//
//	styler := tortellini.NewStyler(tortellini.WithCondition(func() bool { return !noColor }))
//	fmt.Println(styler.Style().Bold().Foreground(tortellini.Red).Sprint("error"))
type Styler struct {
	enabled func() bool
}

// StylerOption configures a [Styler].
type StylerOption func(*Styler)

// WithCondition gates a [Styler]: the styles it mints apply their attributes only
// when condition returns true, and pass text through unchanged otherwise. It is
// consulted on every [Style.Sprint], so it can reflect live state. With no
// condition a Styler always styles.
func WithCondition(condition func() bool) StylerOption {
	return func(s *Styler) { s.enabled = condition }
}

// NewStyler returns a [Styler] ready to bind under a registry key (conventionally
// [KeyStyler]) or use directly.
func NewStyler(options ...StylerOption) *Styler {
	styler := &Styler{}
	for _, option := range options {
		option(styler)
	}
	return styler
}

// Style returns a fresh [Style] bound to the Styler's condition, ready to chain:
//
//	styler.Style().Bold().Foreground(tortellini.Cyan).Sprint("ready")
//
// A nil Styler yields an unconditional [Style].
func (s *Styler) Style() Style {
	if s == nil {
		return Style{}
	}
	return Style{enabled: s.enabled}
}

// Style is a fluent builder for an ANSI SGR styling sequence. Chain attribute
// methods to compose it, then apply it with [Style.Sprint] / [Style.Sprintf]:
//
//	tortellini.Style{}.Bold().Italic().Foreground(tortellini.Red).Sprint("oops")
//
// The zero Style applies nothing (its Sprint returns text unchanged), so it is a
// safe no-op default. Style values are immutable — each method returns a new
// Style — so a partially-built style is safe to reuse as a base for several
// variants. A Style minted by [Styler.Style] also carries the Styler's condition;
// build one directly for unconditional styling, or gate one with [Style.When].
type Style struct {
	sgr     string
	enabled func() bool
}

// Bold adds the bold attribute.
func (s Style) Bold() Style { return s.add("1") }

// Faint adds the faint (dim) attribute.
func (s Style) Faint() Style { return s.add("2") }

// Italic adds the italic attribute.
func (s Style) Italic() Style { return s.add("3") }

// Underline adds the underline attribute.
func (s Style) Underline() Style { return s.add("4") }

// Blink adds the blink attribute.
func (s Style) Blink() Style { return s.add("5") }

// Reverse adds the reverse-video (swap foreground/background) attribute.
func (s Style) Reverse() Style { return s.add("7") }

// Strikethrough adds the strikethrough attribute.
func (s Style) Strikethrough() Style { return s.add("9") }

// Foreground sets the text color.
func (s Style) Foreground(color Color) Style { return s.add(strconv.Itoa(colorSGR(color, 30))) }

// Background sets the background color.
func (s Style) Background(color Color) Style { return s.add(strconv.Itoa(colorSGR(color, 40))) }

// Raw appends a literal SGR parameter for something this package does not name —
// a 256-color foreground ("38;5;208"), a truecolor background ("48;2;255;128;0"),
// or any valid SGR body. It is not validated.
func (s Style) Raw(sgr string) Style { return s.add(sgr) }

// When gates this Style: it applies its attributes only when condition returns
// true (consulted per [Style.Sprint]), otherwise passing text through unchanged.
// It overrides any condition inherited from a [Styler]; a nil condition clears
// the gate (always styles).
func (s Style) When(condition func() bool) Style {
	s.enabled = condition
	return s
}

// add appends one SGR parameter, returning a new Style; an empty parameter is a
// no-op.
func (s Style) add(parameter string) Style {
	if parameter == "" {
		return s
	}
	if s.sgr == "" {
		s.sgr = parameter
	} else {
		s.sgr += ";" + parameter
	}
	return s
}

// Sprint returns text wrapped in the Style's SGR sequence and a reset — or text
// unchanged when the Style has no attributes or its condition reports styling off.
func (s Style) Sprint(text string) string {
	if s.sgr == "" || (s.enabled != nil && !s.enabled()) {
		return text
	}
	return "\x1b[" + s.sgr + "m" + text + "\x1b[0m"
}

// Sprintf is [Style.Sprint] over a formatted string.
func (s Style) Sprintf(format string, args ...any) string {
	return s.Sprint(fmt.Sprintf(format, args...))
}

// colorSGR maps a [Color] to its SGR parameter for the given base (30 foreground,
// 40 background): normal colors are base+color, bright colors base+60+offset.
func colorSGR(color Color, base int) int {
	if color >= BrightBlack {
		return base + 60 + int(color) - int(BrightBlack)
	}
	return base + int(color)
}

// ansiSequences matches the terminal escape sequences styling introduces: CSI
// sequences (ESC [ … final byte — SGR bold/italic/color among them) and OSC
// sequences (ESC ] … terminated by BEL or ST — OSC-8 hyperlinks among them).
// Stripping an OSC-8 hyperlink keeps its visible text and drops the link.
var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// StripStyles removes all terminal styling — CSI and OSC escape sequences — from
// text. It is the counterpart to [Style], for text that arrives ALREADY styled:
// spec-authored ANSI in help/man/markdown content, or any pre-styled string
// headed for a writer that should not see escapes.
//
// With no condition it always strips. With one or more conditions it strips only
// when at least one returns true — the gate a handler uses to honor a --no-styles
// flag, $NO_COLOR, or any signal it chooses:
//
//	noStyles := func() bool { return flags.NoStyles || env.NoStyles }
//	fmt.Fprintln(out, tortellini.StripStyles(helpText, noStyles))
func StripStyles(text string, conditions ...func() bool) string {
	if len(conditions) == 0 {
		return ansiSequences.ReplaceAllString(text, "")
	}
	for _, condition := range conditions {
		if condition != nil && condition() {
			return ansiSequences.ReplaceAllString(text, "")
		}
	}
	return text
}
