package rtk

// This file holds the color math behind level-aware rendering: the canonical RGB for
// the 16 ANSI colors and the 256-palette, and the downsampling from richer
// representations to poorer ones (RGB → 256-cube, RGB/256 → nearest of 16).

// ansi16RGB is the xterm default RGB for each of the 16 ANSI colors (0-7 normal,
// 8-15 bright). Used to render a basic color as truecolor and to find the nearest
// 16-color for a downsample.
var ansi16RGB = [16][3]uint8{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
	{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// cubeSteps are the six component values of the xterm 6×6×6 color cube.
var cubeSteps = [6]int{0, 95, 135, 175, 215, 255}

// palette256RGB returns the RGB for an xterm 256-palette index: the 16 ANSI colors,
// the 6×6×6 cube (16-231), and the 24-step grayscale ramp (232-255).
func palette256RGB(n uint8) [3]uint8 {
	switch {
	case n < 16:
		return ansi16RGB[n]
	case n < 232:
		i := int(n) - 16
		r := cubeSteps[(i/36)%6]
		g := cubeSteps[(i/6)%6]
		b := cubeSteps[i%6]
		return [3]uint8{uint8(r), uint8(g), uint8(b)}
	default:
		gray := 8 + (int(n)-232)*10
		return [3]uint8{uint8(gray), uint8(gray), uint8(gray)}
	}
}

// rgbTo256 maps an RGB triple to the nearest xterm 256-palette index, choosing the
// grayscale ramp for near-neutral colors and the color cube otherwise.
func rgbTo256(r, g, b uint8) int {
	if r == g && g == b { // grayscale ramp for true neutrals
		switch {
		case r < 8:
			return 16 // cube black
		case r > 238:
			return 231 // cube white
		default:
			return 232 + clampInt((int(r)-8+5)/10, 0, 23)
		}
	}
	ri := nearestCubeIndex(r)
	gi := nearestCubeIndex(g)
	bi := nearestCubeIndex(b)
	cube := 16 + 36*ri + 6*gi + bi

	// A close grayscale ramp entry can beat the cube for muted colors; pick the nearer.
	avg := (int(r) + int(g) + int(b)) / 3
	grayIdx := 232 + clampInt((avg-8+5)/10, 0, 23)
	cr, cg, cb := cubeSteps[ri], cubeSteps[gi], cubeSteps[bi]
	gp := palette256RGB(uint8(grayIdx))
	if dist2(r, g, b, gp[0], gp[1], gp[2]) < dist2(r, g, b, uint8(cr), uint8(cg), uint8(cb)) {
		return grayIdx
	}
	return cube
}

// nearestANSI16 returns the index of the closest of the 16 ANSI colors to an RGB
// triple, by squared Euclidean distance.
func nearestANSI16(r, g, b uint8) int {
	best, bestDist := 0, 1<<31
	for i, p := range ansi16RGB {
		if d := dist2(r, g, b, p[0], p[1], p[2]); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// nearestCubeIndex returns the index (0-5) of the cube step closest to v.
func nearestCubeIndex(v uint8) int {
	best, bestDist := 0, 1<<31
	for i, s := range cubeSteps {
		if d := absInt(int(v) - s); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

func dist2(r1, g1, b1, r2, g2, b2 uint8) int {
	dr := int(r1) - int(r2)
	dg := int(g1) - int(g2)
	db := int(b1) - int(b2)
	return dr*dr + dg*dg + db*db
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
