package rtk

import "testing"

func TestStyle_noColorIsNoop(t *testing.T) {
	if got := Style("err", ColorNone, RGB(255, 0, 0), Bold); got != "err" {
		t.Errorf("Style at ColorNone = %q, want unchanged %q", got, "err")
	}
}

func TestStyle_wrapsAttrsAndForeground(t *testing.T) {
	got := Style("x", ColorTrueColor, RGB(10, 20, 30), Bold, Underline)
	want := "\x1b[1;4;38;2;10;20;30mx" + Reset
	if got != want {
		t.Errorf("Style = %q, want %q", got, want)
	}
}

func TestStyle_attrsOnlyDefaultColor(t *testing.T) {
	got := Style("x", ColorTrueColor, Color{}, Bold)
	want := "\x1b[1mx" + Reset
	if got != want {
		t.Errorf("Style(attrs only) = %q, want %q", got, want)
	}
}

func TestColor_foregroundPerLevel(t *testing.T) {
	cases := []struct {
		name  string
		color Color
		level ColorLevel
		want  string
	}{
		{"truecolor exact", RGB(10, 20, 30), ColorTrueColor, "38;2;10;20;30"},
		{"palette at 256", Palette(200), Color256, "38;5;200"},
		{"basic normal at 16", Basic(1), Color16, "31"},
		{"basic bright at 16", Basic(9), Color16, "91"},
		{"rgb downsampled to 16", RGB(255, 0, 0), Color16, "91"}, // → bright red (9)
		{"default color → empty", Color{}, ColorTrueColor, ""},
		{"none level → empty", RGB(1, 2, 3), ColorNone, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.color.Foreground(c.level); got != c.want {
				t.Errorf("Foreground = %q, want %q", got, c.want)
			}
		})
	}
}

func TestColor_background(t *testing.T) {
	if got := Basic(2).Background(Color16); got != "42" {
		t.Errorf("Background(green, 16) = %q, want 42", got)
	}
	if got := Basic(10).Background(Color16); got != "102" {
		t.Errorf("Background(bright green, 16) = %q, want 102", got)
	}
	if got := RGB(0, 0, 0).Background(ColorTrueColor); got != "48;2;0;0;0" {
		t.Errorf("Background(black rgb, truecolor) = %q, want 48;2;0;0;0", got)
	}
}

func TestBasic_clampsOutOfRange(t *testing.T) {
	if got := Basic(99).Foreground(Color16); got != "97" { // clamped to 15 → bright white
		t.Errorf("Basic(99).Foreground(16) = %q, want 97 (clamped to 15)", got)
	}
}

func TestSGR_skipsEmptyAndJoins(t *testing.T) {
	if got := SGR("1", "", "38;5;200"); got != "\x1b[1;38;5;200m" {
		t.Errorf("SGR = %q, want \\x1b[1;38;5;200m", got)
	}
	if got := SGR("", ""); got != "" {
		t.Errorf("SGR(all empty) = %q, want empty", got)
	}
}
