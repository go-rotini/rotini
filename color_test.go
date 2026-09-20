package rotini

import (
	"strings"
	"testing"
)

// Color downsampling had no test at all before this file, which is the wrong place to have a
// gap: Profile downsampling is an advertised feature (a truecolor Style must render sensibly
// on a 256-color and a 16-color terminal), the arithmetic is the kind that is quietly wrong,
// and nothing about the output looks broken when it is.

// TestCode256ToRGB_ranges covers the three regions of the xterm-256 palette: the 16 standard
// colors, the 6×6×6 color cube, and the 24-step grayscale ramp.
func TestCode256ToRGB_ranges(t *testing.T) {
	cases := []struct {
		name       string
		code       uint8
		r, g, b    uint8
		regionNote string
	}{
		{"black, palette entry 0", 0, 0, 0, 0, "standard"},
		{"bright white, palette entry 15", 15, 255, 255, 255, "standard"},
		{"cube origin is black", 16, 0, 0, 0, "cube"},
		{"cube end is white", 231, 255, 255, 255, "cube"},
		{"cube axis: red at max", 196, 255, 0, 0, "cube"},
		{"cube axis: green at max", 46, 0, 255, 0, "cube"},
		{"cube axis: blue at max", 21, 0, 0, 255, "cube"},
		{"cube second level", 17, 0, 0, 95, "cube"},
		{"grayscale ramp start", 232, 8, 8, 8, "gray"},
		{"grayscale ramp end", 255, 238, 238, 238, "gray"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b := code256ToRGB(tc.code)
			if r != tc.r || g != tc.g || b != tc.b {
				t.Errorf("code256ToRGB(%d) = (%d,%d,%d), want (%d,%d,%d) [%s]",
					tc.code, r, g, b, tc.r, tc.g, tc.b, tc.regionNote)
			}
		})
	}
}

// TestColorDistance is the metric everything else keys on: squared euclidean distance, zero
// only for an exact match, and symmetric.
func TestColorDistance(t *testing.T) {
	if d := colorDistance(10, 20, 30, 10, 20, 30); d != 0 {
		t.Errorf("distance to itself = %d, want 0", d)
	}
	if d := colorDistance(0, 0, 0, 1, 2, 2); d != 9 {
		t.Errorf("distance = %d, want 9 (1+4+4)", d)
	}
	if colorDistance(0, 0, 0, 255, 255, 255) != colorDistance(255, 255, 255, 0, 0, 0) {
		t.Error("distance is not symmetric")
	}
	// The farthest possible pair, which must not overflow into a negative.
	if d := colorDistance(0, 0, 0, 255, 255, 255); d != 3*255*255 {
		t.Errorf("max distance = %d, want %d", d, 3*255*255)
	}
}

// TestRGBTo256_roundTrips is the property that matters: every palette entry the cube and ramp
// can express must map back to itself, or downsampling loses a color it did not need to.
func TestRGBTo256_roundTrips(t *testing.T) {
	for code := 16; code <= 255; code++ {
		r, g, b := code256ToRGB(uint8(code))
		if got := rgbTo256(r, g, b); got != uint8(code) {
			t.Errorf("rgbTo256(code256ToRGB(%d)) = %d, want %d", code, got, code)
		}
	}
}

// TestRGBTo256_picksNearest checks the search actually minimizes, on values that fall between
// palette entries.
func TestRGBTo256_picksNearest(t *testing.T) {
	cases := []struct {
		name    string
		r, g, b uint8
		want    uint8
	}{
		{"pure red", 255, 0, 0, 196},
		{"pure white", 255, 255, 255, 231},
		{"pure black", 0, 0, 0, 16},
		{"just off a cube level rounds to it", 254, 1, 1, 196},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rgbTo256(tc.r, tc.g, tc.b); got != tc.want {
				gr, gg, gb := code256ToRGB(got)
				t.Errorf("rgbTo256(%d,%d,%d) = %d (%d,%d,%d), want %d",
					tc.r, tc.g, tc.b, got, gr, gg, gb, tc.want)
			}
		})
	}
}

// TestRGBTo16 pins the nearest-of-sixteen search against the xterm palette.
func TestRGBTo16(t *testing.T) {
	cases := []struct {
		name    string
		r, g, b uint8
		want    ANSIColor
	}{
		{"black", 0, 0, 0, ANSIColorBlack},
		{"bright red", 255, 0, 0, ANSIColorBrightRed},
		{"dim red", 205, 0, 0, ANSIColorRed},
		{"bright white", 255, 255, 255, ANSIColorBrightWhite},
		{"mid gray", 127, 127, 127, ANSIColorBrightBlack},
		{"bright green", 0, 255, 0, ANSIColorBrightGreen},
		{"bright yellow", 255, 255, 0, ANSIColorBrightYellow},
		{"bright cyan", 0, 255, 255, ANSIColorBrightCyan},
		{"bright magenta", 255, 0, 255, ANSIColorBrightMagenta},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rgbTo16(tc.r, tc.g, tc.b); got != tc.want {
				t.Errorf("rgbTo16(%d,%d,%d) = %v, want %v", tc.r, tc.g, tc.b, got, tc.want)
			}
		})
	}
}

// TestCode256To16 is the two-hop path a 256-color style takes on a 16-color terminal.
func TestCode256To16(t *testing.T) {
	// Every one of the sixteen standard entries must survive the round trip unchanged:
	// downsampling a color the terminal already has must be a no-op.
	for code := range uint8(16) {
		if got := code256To16(code); got != ANSIColor(code) {
			t.Errorf("code256To16(%d) = %v, want %v", code, got, ANSIColor(code))
		}
	}
	if got := code256To16(196); got != ANSIColorBrightRed { // cube's pure red
		t.Errorf("code256To16(196) = %v, want bright red", got)
	}
}

// TestStyle_downsamplesThroughProfile drives the same math through the public surface, which
// is where it actually has to be right: one Style, rendered at each profile.
func TestStyle_downsamplesThroughProfile(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		want    string // the SGR parameters the foreground must render as
	}{
		{"truecolor", ProfileTrueColor, "38;2;255;0;0"}, // exact 24-bit
		{"ansi256", ProfileANSI256, "38;5;196"},         // nearest cube entry
		{"ansi16", ProfileANSI16, "91"},                 // bright red
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewStyle().ForegroundRGB(255, 0, 0).SetProfile(tc.profile).Sprint("x")
			if !strings.Contains(got, tc.want) {
				t.Errorf("rendered %q, want it to carry %q", got, tc.want)
			}
		})
	}

	// ProfileNoColor emits no escape at all.
	if got := NewStyle().ForegroundRGB(255, 0, 0).SetProfile(ProfileNoColor).Sprint("x"); got != "x" {
		t.Errorf("ProfileNoColor rendered %q, want the bare text", got)
	}
}

// TestStyler_SetProfile caps every style in the registry, not just the ones defined after.
func TestStyler_SetProfile(t *testing.T) {
	s := NewStyler()
	s.Define("danger").ForegroundRGB(255, 0, 0)
	s.SetProfile(ProfileANSI16)

	if got := s.Render("danger", "x"); !strings.Contains(got, "91") {
		t.Errorf("rendered %q, want the 16-color downsample (91)", got)
	}
}
