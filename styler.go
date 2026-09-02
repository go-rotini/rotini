package rotini

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Text styling: [Style], a chainable set of SGR attributes, and [Styler], a registry
// that renders text by INTENT ("warning", "path") so a CLI's look lives in one place.
//
// The color model and its downsampling are in color.go; escape-aware measuring and
// stripping are in text.go. Nothing here detects anything — see detect.go for the
// opt-in helpers a program uses to decide.

// KeyStyler is the conventional registry key a program binds a [Styler] under, so
// handlers reach one shared set of named styles with [MustGet].
const KeyStyler = "styler"

const reset = "\x1b[0m"

// Styler renders text by INTENT rather than by appearance: a program defines what
// "warning" or "path" looks like once, and handlers ask for it by name. Restyling a
// CLI is then one place, not every call site.
//
// A disabled Styler STRIPS instead of passing through, so turning styling off yields
// clean text even if a style was baked into the string it was given.
//
// The zero value is not usable; start from [NewStyler].
type Styler struct {
	enabled bool
	profile Profile
	styles  map[string]*Style
}

// NewStyler returns an empty Styler, enabled, at [ProfileTrueColor]. Narrow the
// profile with [Styler.SetProfile] if the terminal cannot render that much.
func NewStyler() *Styler {
	return &Styler{
		enabled: true,
		profile: ProfileTrueColor,
		styles:  map[string]*Style{},
	}
}

// SetEnabled turns styling on or off for every style in the registry. Disabled, the
// Styler strips escapes from whatever it renders. It returns the receiver to chain.
func (s *Styler) SetEnabled(enabled bool) *Styler {
	s.enabled = enabled
	return s
}

// SetProfile caps the color depth of every style in the registry, downsampling
// richer colors to fit. It returns the receiver to chain.
func (s *Styler) SetProfile(profile Profile) *Styler {
	s.profile = profile
	return s
}

// Define creates (or replaces) the style registered under key and returns it for
// chaining, already carrying the registry's enabled state and profile.
func (s *Styler) Define(key string) *Style {
	style := NewStyle()
	s.Set(key, style)
	return style
}

// Get returns the style registered under key, and whether one was registered.
func (s *Styler) Get(key string) (*Style, bool) {
	style, ok := s.styles[key]
	return style, ok
}

// Set registers style under key, replacing any prior one. It returns the receiver
// to chain.
func (s *Styler) Set(key string, style *Style) *Styler {
	s.styles[key] = style
	return s
}

// Delete removes the style registered under key. Rendering an unregistered key
// leaves the text unstyled rather than failing. It returns the receiver to chain.
func (s *Styler) Delete(key string) *Styler {
	delete(s.styles, key)
	return s
}

// Render applies the style registered under key to text. An unknown key, or a
// disabled Styler, returns the text stripped of styling rather than erroring —
// a missing style should never break output.
func (s *Styler) Render(key, text string) string {
	if !s.enabled {
		return Strip(text)
	}
	style, ok := s.styles[key]
	if !ok {
		return text
	}
	return style.renderAt(s.profile, text)
}

// Renderf is [Styler.Render] over a formatted string.
func (s *Styler) Renderf(key, format string, args ...any) string {
	return s.Render(key, fmt.Sprintf(format, args...))
}

// Fprint writes a to w, styled by key.
func (s *Styler) Fprint(w io.Writer, key string, a ...any) {
	_, _ = fmt.Fprint(w, s.Render(key, fmt.Sprint(a...)))
}

// Fprintln writes a to w followed by a newline, styled by key.
func (s *Styler) Fprintln(w io.Writer, key string, a ...any) {
	_, _ = fmt.Fprintln(w, s.Render(key, fmt.Sprint(a...)))
}

// Fprintf writes a formatted string to w, styled by key.
func (s *Styler) Fprintf(w io.Writer, key, format string, a ...any) {
	_, _ = fmt.Fprint(w, s.Renderf(key, format, a...))
}

// Style is a set of SGR attributes — weight, decoration, foreground and background
// color — built by chaining and applied with [Style.Sprint] or the Fprint family.
//
// A DISABLED style passes text through untouched (it adds nothing), which is the
// difference from a disabled [Styler] (which strips). Color is emitted at whatever
// the [Profile] allows, so one Style renders correctly on a truecolor terminal and
// a 16-color one alike.
//
// The zero value is not usable; start from [NewStyle].
type Style struct {
	enabled bool
	profile Profile
	attrs   []string
	fg      color
	bg      color
}

// NewStyle returns an empty style, enabled, at [ProfileTrueColor].
func NewStyle() *Style {
	return &Style{enabled: true, profile: ProfileTrueColor}
}

// SetEnabled turns this style on or off. A disabled style passes its text through
// unchanged. It returns the receiver to chain.
func (s *Style) SetEnabled(enabled bool) *Style {
	s.enabled = enabled
	return s
}

// SetProfile caps this style's color depth, downsampling richer colors to fit. It
// returns the receiver to chain.
func (s *Style) SetProfile(profile Profile) *Style {
	s.profile = profile
	return s
}

// Clone returns an independent copy, so a base style can be varied without the
// variant's changes reaching back.
func (s *Style) Clone() *Style {
	clone := *s
	clone.attrs = slices.Clone(s.attrs)
	return &clone
}

// Bold adds the bold attribute. It returns the receiver to chain.
func (s *Style) Bold() *Style {
	return s.addAttr("1")
}

// Faint adds the faint (dim) attribute. It returns the receiver to chain.
func (s *Style) Faint() *Style {
	return s.addAttr("2")
}

// Italic adds the italic attribute. It returns the receiver to chain.
func (s *Style) Italic() *Style {
	return s.addAttr("3")
}

// Underline adds the underline attribute. It returns the receiver to chain.
func (s *Style) Underline() *Style {
	return s.addAttr("4")
}

// DoubleUnderline adds the double-underline attribute. It returns the receiver to chain.
func (s *Style) DoubleUnderline() *Style {
	return s.addAttr("21")
}

// Blink adds the slow-blink attribute. It returns the receiver to chain.
func (s *Style) Blink() *Style {
	return s.addAttr("5")
}

// RapidBlink adds the rapid-blink attribute. It returns the receiver to chain.
func (s *Style) RapidBlink() *Style {
	return s.addAttr("6")
}

// Reverse adds the reverse-video attribute, swapping foreground and background.
// It returns the receiver to chain.
func (s *Style) Reverse() *Style {
	return s.addAttr("7")
}

// Conceal adds the conceal attribute. It returns the receiver to chain.
func (s *Style) Conceal() *Style {
	return s.addAttr("8")
}

// Strikethrough adds the strikethrough attribute. It returns the receiver to chain.
func (s *Style) Strikethrough() *Style {
	return s.addAttr("9")
}

// Overline adds the overline attribute. It returns the receiver to chain.
func (s *Style) Overline() *Style {
	return s.addAttr("53")
}

// ForegroundANSI sets the foreground to one of the 16 standard colors. It returns
// the receiver to chain.
func (s *Style) ForegroundANSI(c ANSIColor) *Style {
	s.fg = colorANSI(c)
	return s
}

// BackgroundANSI sets the background to one of the 16 standard colors. It returns
// the receiver to chain.
func (s *Style) BackgroundANSI(c ANSIColor) *Style {
	s.bg = colorANSI(c)
	return s
}

// ForegroundRGB sets a 24-bit foreground color, downsampled when the profile cannot
// render it. It returns the receiver to chain.
func (s *Style) ForegroundRGB(red, green, blue uint8) *Style {
	s.fg = colorRGB{red, green, blue}
	return s
}

// BackgroundRGB sets a 24-bit background color, downsampled when the profile cannot
// render it. It returns the receiver to chain.
func (s *Style) BackgroundRGB(red, green, blue uint8) *Style {
	s.bg = colorRGB{red, green, blue}
	return s
}

// Foreground256 sets a foreground color from the 256-color palette. It returns the
// receiver to chain.
func (s *Style) Foreground256(code uint8) *Style {
	s.fg = color256(code)
	return s
}

// Background256 sets a background color from the 256-color palette. It returns the
// receiver to chain.
func (s *Style) Background256(code uint8) *Style {
	s.bg = color256(code)
	return s
}

// ForegroundHex sets a 24-bit foreground color from a "#rrggbb" or "rrggbb" string.
// An unparseable value is ignored. It returns the receiver to chain.
func (s *Style) ForegroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		s.fg = colorRGB{red, green, blue}
	}
	return s
}

// BackgroundHex sets a 24-bit background color from a "#rrggbb" or "rrggbb" string.
// An unparseable value is ignored. It returns the receiver to chain.
func (s *Style) BackgroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		s.bg = colorRGB{red, green, blue}
	}
	return s
}

// Raw appends a literal SGR parameter string, for an attribute rotini has no method
// for. The value is emitted verbatim — you own its correctness. It returns the
// receiver to chain.
func (s *Style) Raw(sgr string) *Style {
	if sgr != "" {
		s.attrs = append(s.attrs, sgr)
	}
	return s
}

// Merge layers other's attributes and colors on top of this style's, with other
// winning on conflict. It returns the receiver to chain.
func (s *Style) Merge(other *Style) *Style {
	if other == nil {
		return s
	}
	s.attrs = append(s.attrs, other.attrs...)
	if other.fg != nil {
		s.fg = other.fg
	}
	if other.bg != nil {
		s.bg = other.bg
	}
	return s
}

func (s *Style) addAttr(parameter string) *Style {
	s.attrs = append(s.attrs, parameter)
	return s
}

func (s *Style) sgrParams(profile Profile) string {
	params := make([]string, 0, len(s.attrs)+2)
	params = append(params, s.attrs...)
	if s.fg != nil {
		if p := s.fg.sgr(profile, false); p != "" {
			params = append(params, p)
		}
	}
	if s.bg != nil {
		if p := s.bg.sgr(profile, true); p != "" {
			params = append(params, p)
		}
	}
	return strings.Join(params, ";")
}

func (s *Style) renderAt(profile Profile, text string) string {
	if !s.enabled {
		return text
	}
	params := s.sgrParams(profile)
	if params == "" {
		return text
	}
	open := "\x1b[" + params + "m"
	if strings.Contains(text, reset) {
		text = strings.ReplaceAll(text, reset, reset+open)
	}
	return open + text + reset
}

// Sprint returns text wrapped in this style's escape sequences, or text unchanged
// when the style is disabled or carries no attributes.
func (s *Style) Sprint(text string) string {
	return s.renderAt(s.profile, text)
}

// Sprintf is [Style.Sprint] over a formatted string.
func (s *Style) Sprintf(format string, args ...any) string {
	return s.Sprint(fmt.Sprintf(format, args...))
}

// Fprint writes a to w in this style.
func (s *Style) Fprint(w io.Writer, a ...any) {
	_, _ = fmt.Fprint(w, s.Sprint(fmt.Sprint(a...)))
}

// Fprintln writes a to w in this style, followed by a newline.
func (s *Style) Fprintln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, s.Sprint(fmt.Sprint(a...)))
}

// Fprintf writes a formatted string to w in this style.
func (s *Style) Fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprint(w, s.Sprintf(format, a...))
}
