package rtk

import "testing"

func TestRGBTo256_knownPoints(t *testing.T) {
	cases := []struct {
		r, g, b uint8
		want    int
	}{
		{0, 0, 0, 16},        // black → cube black
		{255, 255, 255, 231}, // white → cube white
		{255, 0, 0, 196},     // pure red → cube 16+36*5
		{0, 255, 0, 46},      // pure green → cube 16+6*5
		{0, 0, 255, 21},      // pure blue → cube 16+5
		{128, 128, 128, 244}, // mid gray → grayscale ramp
	}
	for _, c := range cases {
		if got := rgbTo256(c.r, c.g, c.b); got != c.want {
			t.Errorf("rgbTo256(%d,%d,%d) = %d, want %d", c.r, c.g, c.b, got, c.want)
		}
	}
}

func TestPalette256RGB_roundtrips(t *testing.T) {
	if got := palette256RGB(196); got != [3]uint8{255, 0, 0} {
		t.Errorf("palette256RGB(196) = %v, want [255 0 0]", got)
	}
	if got := palette256RGB(244); got != [3]uint8{128, 128, 128} {
		t.Errorf("palette256RGB(244) = %v, want [128 128 128]", got)
	}
	if got := palette256RGB(1); got != ansi16RGB[1] {
		t.Errorf("palette256RGB(1) = %v, want %v", got, ansi16RGB[1])
	}
}

func TestNearestANSI16(t *testing.T) {
	cases := []struct {
		r, g, b uint8
		want    int
	}{
		{0, 0, 0, 0},        // black
		{255, 255, 255, 15}, // bright white
		{255, 0, 0, 9},      // bright red (exact)
		{0, 255, 0, 10},     // bright green (exact)
		{0, 0, 255, 4},      // normal blue (0,0,238) is nearer than bright (92,92,255)
	}
	for _, c := range cases {
		if got := nearestANSI16(c.r, c.g, c.b); got != c.want {
			t.Errorf("nearestANSI16(%d,%d,%d) = %d, want %d", c.r, c.g, c.b, got, c.want)
		}
	}
}
