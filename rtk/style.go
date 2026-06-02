package rtk

import "strconv"

// Attr is an SGR text attribute. Pass several to [Style] to combine them.
type Attr int

// The portable text attributes. (Blink and others are omitted as poorly supported.)
const (
	Bold      Attr = 1
	Dim       Attr = 2
	Italic    Attr = 3
	Underline Attr = 4
	Reverse   Attr = 7
	Strike    Attr = 9
)

// colorKind tags how a [Color] was specified, so rendering can downsample it.
type colorKind uint8

const (
	kindDefault colorKind = iota // the terminal's default color
	kindBasic                    // a 0..15 ANSI index
	kindPalette                  // a 0..255 xterm palette index
	kindRGB                      // 24-bit truecolor
)

// Color is a terminal color: the terminal default (the zero value), one of the 16
// ANSI colors, a 256-palette index, or a 24-bit RGB triple. A Color renders itself to
// the best escape for a given [ColorLevel], downsampling as needed, so one Color value
// is correct on every terminal.
type Color struct {
	kind    colorKind
	n       uint8 // basic (0..15) or palette (0..255) index
	r, g, b uint8 // RGB components
}

// Basic returns one of the 16 ANSI colors (0..15: 0-7 normal, 8-15 bright). Values
// out of range are clamped.
func Basic(n int) Color { return Color{kind: kindBasic, n: uint8(clampInt(n, 0, 15))} }

// Palette returns an xterm 256-color palette index (0..255).
func Palette(n int) Color { return Color{kind: kindPalette, n: uint8(clampInt(n, 0, 255))} }

// RGB returns a 24-bit truecolor, downsampled automatically on terminals that
// support fewer colors.
func RGB(r, g, b uint8) Color { return Color{kind: kindRGB, r: r, g: g, b: b} }

// The 16 named ANSI colors, for convenience.
var (
	Black   = Basic(0)
	Red     = Basic(1)
	Green   = Basic(2)
	Yellow  = Basic(3)
	Blue    = Basic(4)
	Magenta = Basic(5)
	Cyan    = Basic(6)
	White   = Basic(7)

	BrightBlack   = Basic(8)
	BrightRed     = Basic(9)
	BrightGreen   = Basic(10)
	BrightYellow  = Basic(11)
	BrightBlue    = Basic(12)
	BrightMagenta = Basic(13)
	BrightCyan    = Basic(14)
	BrightWhite   = Basic(15)
)

// Foreground returns the SGR parameters that set this color as the foreground at the
// given level, or "" for the terminal default and for [ColorNone].
func (c Color) Foreground(level ColorLevel) string { return c.sgr(level, false) }

// Background returns the SGR parameters that set this color as the background at the
// given level, or "" for the terminal default and for [ColorNone].
func (c Color) Background(level ColorLevel) string { return c.sgr(level, true) }

func (c Color) sgr(level ColorLevel, bg bool) string {
	if level == ColorNone || c.kind == kindDefault {
		return ""
	}
	switch level {
	case ColorTrueColor:
		r, g, b := c.rgb()
		base := "38;2;"
		if bg {
			base = "48;2;"
		}
		return base + itoa(int(r)) + ";" + itoa(int(g)) + ";" + itoa(int(b))
	case Color256:
		base := "38;5;"
		if bg {
			base = "48;5;"
		}
		return base + strconv.Itoa(c.index256())
	default: // Color16
		return basic16SGR(c.index16(), bg)
	}
}

func (c Color) rgb() (uint8, uint8, uint8) {
	switch c.kind {
	case kindRGB:
		return c.r, c.g, c.b
	case kindBasic:
		p := ansi16RGB[c.n]
		return p[0], p[1], p[2]
	case kindPalette:
		p := palette256RGB(c.n)
		return p[0], p[1], p[2]
	default:
		return 0, 0, 0
	}
}

func (c Color) index256() int {
	switch c.kind {
	case kindPalette, kindBasic:
		return int(c.n)
	case kindRGB:
		return rgbTo256(c.r, c.g, c.b)
	default:
		return 0
	}
}

func (c Color) index16() int {
	if c.kind == kindBasic {
		return int(c.n)
	}
	r, g, b := c.rgb()
	return nearestANSI16(r, g, b)
}

// Style wraps s in the foreground color and attributes for the given level. At
// [ColorNone] it returns s unchanged (no color and no attribute escapes — what
// NO_COLOR, a dumb terminal, and redirected output all want), so the same call is
// safe whether or not the destination supports color. For a background color, compose
// [SGR] with [Color.Background] directly.
func Style(s string, level ColorLevel, fg Color, attrs ...Attr) string {
	if level == ColorNone {
		return s
	}
	codes := make([]string, 0, len(attrs)+1)
	for _, a := range attrs {
		codes = append(codes, strconv.Itoa(int(a)))
	}
	codes = append(codes, fg.Foreground(level))
	open := SGR(codes...)
	if open == "" {
		return s
	}
	return open + s + Reset
}

func basic16SGR(idx int, bg bool) string {
	// 0..7 → 30..37 (fg) / 40..47 (bg); 8..15 → 90..97 / 100..107 (bright).
	switch {
	case idx < 8 && !bg:
		return itoa(30 + idx)
	case idx < 8:
		return itoa(40 + idx)
	case !bg:
		return itoa(90 + (idx - 8))
	default:
		return itoa(100 + (idx - 8))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
