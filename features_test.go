package rotini

import (
	"os"
	"strings"
	"testing"
)

func TestStyle_profileDownsampling(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		got     string
		want    string
	}{
		{"truecolor keeps rgb", ProfileTrueColor, NewStyle().ForegroundRGB(255, 0, 0).SetProfile(ProfileTrueColor).Sprint("x"), "\x1b[38;2;255;0;0mx\x1b[0m"},
		{"rgb to 256", ProfileANSI256, NewStyle().ForegroundRGB(255, 0, 0).SetProfile(ProfileANSI256).Sprint("x"), "\x1b[38;5;196mx\x1b[0m"},
		{"rgb to 16", ProfileANSI16, NewStyle().ForegroundRGB(255, 0, 0).SetProfile(ProfileANSI16).Sprint("x"), "\x1b[91mx\x1b[0m"},
		{"hex to 16 (amber→yellow)", ProfileANSI16, NewStyle().ForegroundHex("#fcba03").SetProfile(ProfileANSI16).Sprint("x"), "\x1b[33mx\x1b[0m"},
		{"256 to 16", ProfileANSI16, NewStyle().Foreground256(196).SetProfile(ProfileANSI16).Sprint("x"), "\x1b[91mx\x1b[0m"},
		{"no-color drops color only", ProfileNoColor, NewStyle().Bold().ForegroundRGB(255, 0, 0).SetProfile(ProfileNoColor).Sprint("x"), "\x1b[1mx\x1b[0m"},
		{"no-color with only color is identity", ProfileNoColor, NewStyle().ForegroundRGB(255, 0, 0).SetProfile(ProfileNoColor).Sprint("x"), "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestStyler_profile(t *testing.T) {
	styler := NewStyler().SetProfile(ProfileANSI256)
	styler.Set("error", NewStyle().ForegroundRGB(255, 0, 0))
	if got, want := styler.Render("error", "x"), "\x1b[38;5;196mx\x1b[0m"; got != want {
		t.Errorf("Render at ANSI256 = %q, want %q", got, want)
	}
}

func TestStyle_newAttributes(t *testing.T) {
	if got, want := NewStyle().DoubleUnderline().Sprint("x"), "\x1b[21mx\x1b[0m"; got != want {
		t.Errorf("DoubleUnderline = %q, want %q", got, want)
	}
	if got, want := NewStyle().Overline().Sprint("x"), "\x1b[53mx\x1b[0m"; got != want {
		t.Errorf("Overline = %q, want %q", got, want)
	}
}

func TestStyle_nestedReset(t *testing.T) {
	// An interior reset re-opens the style so the tail stays styled.
	inner := "a" + reset + "b"
	got := NewStyle().Bold().Sprint(inner)
	want := "\x1b[1ma\x1b[0m\x1b[1mb\x1b[0m"
	if got != want {
		t.Errorf("nested reset = %q, want %q", got, want)
	}
}

func TestHyperlink(t *testing.T) {
	link := Hyperlink("https://example.dev", "docs")
	if want := "\x1b]8;;https://example.dev\x07docs\x1b]8;;\x07"; link != want {
		t.Errorf("Hyperlink = %q, want %q", link, want)
	}
	// Strip keeps the visible label.
	if got := Strip(link); got != "docs" {
		t.Errorf("Strip(Hyperlink) = %q, want %q", got, "docs")
	}
}

func TestWidth(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"ascii ignores escapes", "\x1b[1mhi\x1b[0m", 2},
		{"wide CJK counts double", "日本", 4},
		{"combining mark is zero width", "é", 1},
		{"emoji counts double", "a\U0001F600b", 4},
		{"plain", "hello", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Width(tc.text); got != tc.want {
				t.Errorf("Width(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

func TestStyle_writeHelpers(t *testing.T) {
	var buf strings.Builder
	NewStyle().Bold().Fprintln(&buf, "hi")
	if got, want := buf.String(), "\x1b[1mhi\x1b[0m\n"; got != want {
		t.Errorf("Style.Fprintln = %q, want %q", got, want)
	}
	buf.Reset()
	NewStyle().Italic().Fprintf(&buf, "v%d", 2)
	if got, want := buf.String(), "\x1b[3mv2\x1b[0m"; got != want {
		t.Errorf("Style.Fprintf = %q, want %q", got, want)
	}
}

func TestStyler_writeHelpers(t *testing.T) {
	styler := NewStyler()
	styler.Set("error", NewStyle().Bold().ForegroundANSI(ANSIColorRed))
	var buf strings.Builder
	styler.Fprintln(&buf, "error", "boom")
	if got, want := buf.String(), "\x1b[1;31mboom\x1b[0m\n"; got != want {
		t.Errorf("Styler.Fprintln = %q, want %q", got, want)
	}
}

func TestEnvNoColor(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "1")
	if !EnvNoColor() {
		t.Errorf("EnvNoColor with NO_COLOR=1 = false, want true")
	}
	// CLICOLOR_FORCE overrides NO_COLOR.
	t.Setenv("CLICOLOR_FORCE", "1")
	if EnvNoColor() {
		t.Errorf("EnvNoColor with CLICOLOR_FORCE=1 = true, want false")
	}
}

func TestDetectProfile(t *testing.T) {
	cases := []struct {
		name      string
		noColor   string
		colorterm string
		term      string
		want      Profile
	}{
		{"no_color wins", "1", "truecolor", "xterm-256color", ProfileNoColor},
		{"colorterm truecolor", "", "truecolor", "xterm", ProfileTrueColor},
		{"term 256color", "", "", "xterm-256color", ProfileANSI256},
		{"term basic", "", "", "xterm", ProfileANSI16},
		{"term dumb", "", "", "dumb", ProfileNoColor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", "")
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("COLORTERM", tc.colorterm)
			t.Setenv("TERM", tc.term)
			if got := DetectProfile(); got != tc.want {
				t.Errorf("DetectProfile = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(nil) {
		t.Errorf("IsTerminal(nil) = true, want false")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if IsTerminal(r) {
		t.Errorf("IsTerminal(pipe) = true, want false")
	}
}
