package tortellini

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const KeyStyler = "styler"

var ansiSequences = regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

type ANSIColor int

const (
	ANSIColorBlack ANSIColor = iota
	ANSIColorRed
	ANSIColorGreen
	ANSIColorYellow
	ANSIColorBlue
	ANSIColorMagenta
	ANSIColorCyan
	ANSIColorWhite
	ANSIColorBrightBlack
	ANSIColorBrightRed
	ANSIColorBrightGreen
	ANSIColorBrightYellow
	ANSIColorBrightBlue
	ANSIColorBrightMagenta
	ANSIColorBrightCyan
	ANSIColorBrightWhite
)

type Styler struct {
	enabled bool
	styles  map[string]*Style
}

func NewStyler() *Styler {
	return &Styler{
		enabled: true,
		styles:  map[string]*Style{},
	}
}

func (s *Styler) SetEnabled(enabled bool) *Styler {
	s.enabled = enabled
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
		return ansiSequences.ReplaceAllString(text, "")
	}
	style, ok := s.styles[key]
	if !ok {
		return text
	}
	return style.Sprint(text)
}

func (s *Styler) Renderf(key, format string, args ...any) string {
	return s.Render(key, fmt.Sprintf(format, args...))
}

type Style struct {
	sgr     string
	enabled bool
}

func NewStyle() *Style {
	return &Style{enabled: true}
}

func (s *Style) SetEnabled(enabled bool) *Style {
	s.enabled = enabled
	return s
}

func (s *Style) Clone() *Style {
	clone := *s
	return &clone
}

func (s *Style) Bold() *Style {
	return s.add("1")
}

func (s *Style) Faint() *Style {
	return s.add("2")
}

func (s *Style) Italic() *Style {
	return s.add("3")
}

func (s *Style) Underline() *Style {
	return s.add("4")
}

func (s *Style) Blink() *Style {
	return s.add("5")
}

func (s *Style) RapidBlink() *Style {
	return s.add("6")
}

func (s *Style) Reverse() *Style {
	return s.add("7")
}

func (s *Style) Conceal() *Style {
	return s.add("8")
}

func (s *Style) Strikethrough() *Style {
	return s.add("9")
}

func (s *Style) ForegroundANSI(color ANSIColor) *Style {
	return s.add(strconv.Itoa(colorSGR(color, 30)))
}

func (s *Style) BackgroundANSI(color ANSIColor) *Style {
	return s.add(strconv.Itoa(colorSGR(color, 40)))
}

func (s *Style) ForegroundRGB(red, green, blue uint8) *Style {
	return s.add(fmt.Sprintf("38;2;%d;%d;%d", red, green, blue))
}

func (s *Style) BackgroundRGB(red, green, blue uint8) *Style {
	return s.add(fmt.Sprintf("48;2;%d;%d;%d", red, green, blue))
}

func (s *Style) Foreground256(code uint8) *Style {
	return s.add("38;5;" + strconv.Itoa(int(code)))
}

func (s *Style) Background256(code uint8) *Style {
	return s.add("48;5;" + strconv.Itoa(int(code)))
}

func (s *Style) ForegroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		return s.ForegroundRGB(red, green, blue)
	}
	return s
}

func (s *Style) BackgroundHex(hex string) *Style {
	if red, green, blue, ok := parseHex(hex); ok {
		return s.BackgroundRGB(red, green, blue)
	}
	return s
}

func (s *Style) Raw(sgr string) *Style {
	return s.add(sgr)
}

func (s *Style) Merge(other *Style) *Style {
	if other != nil {
		return s.add(other.sgr)
	}
	return s
}

func (s *Style) add(parameter string) *Style {
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

func (s *Style) Sprint(text string) string {
	if !s.enabled {
		return ansiSequences.ReplaceAllString(text, "")
	}
	if s.sgr == "" {
		return text
	}
	return "\x1b[" + s.sgr + "m" + text + "\x1b[0m"
}

func (s *Style) Sprintf(format string, args ...any) string {
	return s.Sprint(fmt.Sprintf(format, args...))
}

func colorSGR(color ANSIColor, base int) int {
	if color >= ANSIColorBrightBlack {
		return base + 60 + int(color) - int(ANSIColorBrightBlack)
	}
	return base + int(color)
}

func parseHex(hex string) (red, green, blue uint8, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(value >> 16), uint8(value >> 8), uint8(value), true
}
