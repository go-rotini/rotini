package rotini

import (
	"math"
	"strconv"
	"strings"
)

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

type Profile int

const (
	ProfileNoColor Profile = iota
	ProfileANSI16
	ProfileANSI256
	ProfileTrueColor
)

type color interface {
	sgr(profile Profile, background bool) string
}

type colorANSI ANSIColor

func (c colorANSI) sgr(profile Profile, background bool) string {
	if profile == ProfileNoColor {
		return ""
	}
	base := 30
	if background {
		base = 40
	}
	return strconv.Itoa(colorSGR(ANSIColor(c), base))
}

type color256 uint8

func (c color256) sgr(profile Profile, background bool) string {
	switch profile {
	case ProfileNoColor:
		return ""
	case ProfileANSI16:
		return colorANSI(code256To16(uint8(c))).sgr(profile, background)
	default:
		prefix := "38;5;"
		if background {
			prefix = "48;5;"
		}
		return prefix + strconv.Itoa(int(c))
	}
}

type colorRGB struct {
	r, g, b uint8
}

func (c colorRGB) sgr(profile Profile, background bool) string {
	switch profile {
	case ProfileNoColor:
		return ""
	case ProfileANSI16:
		return colorANSI(rgbTo16(c.r, c.g, c.b)).sgr(profile, background)
	case ProfileANSI256:
		return color256(rgbTo256(c.r, c.g, c.b)).sgr(profile, background)
	default:
		prefix := "38;2;"
		if background {
			prefix = "48;2;"
		}
		return prefix + strconv.Itoa(int(c.r)) + ";" + strconv.Itoa(int(c.g)) + ";" + strconv.Itoa(int(c.b))
	}
}

func colorSGR(color ANSIColor, base int) int {
	if color >= ANSIColorBrightBlack {
		return base + 60 + int(color) - int(ANSIColorBrightBlack)
	}
	return base + int(color)
}

// palette16 holds the conventional xterm RGB values for the sixteen ANSI colors,
// used to pick the nearest palette entry when downsampling to ProfileANSI16.
var palette16 = [16][3]uint8{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
	{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

func code256ToRGB(code uint8) (red, green, blue uint8) {
	switch {
	case code < 16:
		p := palette16[code]
		return p[0], p[1], p[2]
	case code < 232:
		c := code - 16
		return cubeLevels[c/36], cubeLevels[(c%36)/6], cubeLevels[c%6]
	default:
		v := uint8(8 + (int(code)-232)*10)
		return v, v, v
	}
}

func colorDistance(r1, g1, b1, r2, g2, b2 uint8) int {
	dr, dg, db := int(r1)-int(r2), int(g1)-int(g2), int(b1)-int(b2)
	return dr*dr + dg*dg + db*db
}

func rgbTo256(red, green, blue uint8) uint8 {
	best, bestDistance := uint8(16), math.MaxInt
	for code := 16; code <= 255; code++ {
		cr, cg, cb := code256ToRGB(uint8(code))
		if d := colorDistance(red, green, blue, cr, cg, cb); d < bestDistance {
			best, bestDistance = uint8(code), d
		}
	}
	return best
}

func rgbTo16(red, green, blue uint8) ANSIColor {
	best, bestDistance := 0, math.MaxInt
	for code, p := range palette16 {
		if d := colorDistance(red, green, blue, p[0], p[1], p[2]); d < bestDistance {
			best, bestDistance = code, d
		}
	}
	return ANSIColor(best)
}

func code256To16(code uint8) ANSIColor {
	return rgbTo16(code256ToRGB(code))
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
