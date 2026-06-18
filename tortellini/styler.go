package tortellini

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

const KeyStyler = "styler"

const reset = "\x1b[0m"

type Styler struct {
	enabled bool
	profile Profile
	styles  map[string]*Style
}

func NewStyler() *Styler {
	return &Styler{
		enabled: true,
		profile: ProfileTrueColor,
		styles:  map[string]*Style{},
	}
}

func (s *Styler) SetEnabled(enabled bool) *Styler {
	s.enabled = enabled
	return s
}

func (s *Styler) SetProfile(profile Profile) *Styler {
	s.profile = profile
	return s
}

func (s *Styler) Define(key string) *Style {
	style := NewStyle()
	s.Set(key, style)
	return style
}

func (s *Styler) Get(key string) (*Style, bool) {
	style, ok := s.styles[key]
	return style, ok
}

func (s *Styler) Set(key string, style *Style) *Styler {
	s.styles[key] = style
	return s
}

func (s *Styler) Delete(key string) *Styler {
	delete(s.styles, key)
	return s
}

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

func (s *Styler) Renderf(key, format string, args ...any) string {
	return s.Render(key, fmt.Sprintf(format, args...))
}

func (s *Styler) Fprint(w io.Writer, key string, a ...any) {
	_, _ = fmt.Fprint(w, s.Render(key, fmt.Sprint(a...)))
}

func (s *Styler) Fprintln(w io.Writer, key string, a ...any) {
	_, _ = fmt.Fprintln(w, s.Render(key, fmt.Sprint(a...)))
}

func (s *Styler) Fprintf(w io.Writer, key, format string, a ...any) {
	_, _ = fmt.Fprint(w, s.Renderf(key, format, a...))
}

type Style struct {
	enabled bool
	profile Profile
	attrs   []string
	fg      color
	bg      color
}

func NewStyle() *Style {
	return &Style{enabled: true, profile: ProfileTrueColor}
}

func (s *Style) SetEnabled(enabled bool) *Style {
	s.enabled = enabled
	return s
}

func (s *Style) SetProfile(profile Profile) *Style {
	s.profile = profile
	return s
}

func (s *Style) Clone() *Style {
	clone := *s
	clone.attrs = slices.Clone(s.attrs)
	return &clone
}

func (s *Style) Bold() *Style {
	return s.addAttr("1")
}

func (s *Style) Faint() *Style {
	return s.addAttr("2")
}

func (s *Style) Italic() *Style {
	return s.addAttr("3")
}

func (s *Style) Underline() *Style {
	return s.addAttr("4")
}

func (s *Style) DoubleUnderline() *Style {
	return s.addAttr("21")
}

func (s *Style) Blink() *Style {
	return s.addAttr("5")
}

func (s *Style) RapidBlink() *Style {
	return s.addAttr("6")
}

func (s *Style) Reverse() *Style {
	return s.addAttr("7")
}

func (s *Style) Conceal() *Style {
	return s.addAttr("8")
}

func (s *Style) Strikethrough() *Style {
	return s.addAttr("9")
}

func (s *Style) Overline() *Style {
	return s.addAttr("53")
}

func (s *Style) ForegroundANSI(c ANSIColor) *Style {
	s.fg = colorANSI(c)
	return s
}

func (s *Style) BackgroundANSI(c ANSIColor) *Style {
	s.bg = colorANSI(c)
	return s
}

func (s *Style) ForegroundRGB(red, green, blue uint8) *Style {
	s.fg = colorRGB{red, green, blue}
	return s
}

func (s *Style) BackgroundRGB(red, green, blue uint8) *Style {
	s.bg = colorRGB{red, green, blue}
	return s
}

func (s *Style) Foreground256(code uint8) *Style {
	s.fg = color256(code)
	return s
}

func (s *Style) Background256(code uint8) *Style {
	s.bg = color256(code)
	return s
}

func (s *Style) ForegroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		s.fg = colorRGB{red, green, blue}
	}
	return s
}

func (s *Style) BackgroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		s.bg = colorRGB{red, green, blue}
	}
	return s
}

func (s *Style) Raw(sgr string) *Style {
	if sgr != "" {
		s.attrs = append(s.attrs, sgr)
	}
	return s
}

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

func (s *Style) Sprint(text string) string {
	return s.renderAt(s.profile, text)
}

func (s *Style) Sprintf(format string, args ...any) string {
	return s.Sprint(fmt.Sprintf(format, args...))
}

func (s *Style) Fprint(w io.Writer, a ...any) {
	_, _ = fmt.Fprint(w, s.Sprint(fmt.Sprint(a...)))
}

func (s *Style) Fprintln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, s.Sprint(fmt.Sprint(a...)))
}

func (s *Style) Fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprint(w, s.Sprintf(format, a...))
}
