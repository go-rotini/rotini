package tortellini

import (
	"fmt"
	"testing"
)

func TestStyle_fluent(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"bold", NewStyle().Bold().Sprint("hi"), "\x1b[1mhi\x1b[0m"},
		{"bold+italic chain", NewStyle().Bold().Italic().Sprint("hi"), "\x1b[1;3mhi\x1b[0m"},
		{"foreground color", NewStyle().ForegroundANSI(ANSIColorRed).Sprint("hi"), "\x1b[31mhi\x1b[0m"},
		{"bold + foreground", NewStyle().Bold().ForegroundANSI(ANSIColorRed).Sprint("hi"), "\x1b[1;31mhi\x1b[0m"},
		{"background color", NewStyle().BackgroundANSI(ANSIColorBlue).Sprint("hi"), "\x1b[44mhi\x1b[0m"},
		{"bright fg + bright bg", NewStyle().ForegroundANSI(ANSIColorBrightRed).BackgroundANSI(ANSIColorBrightWhite).Sprint("hi"), "\x1b[91;107mhi\x1b[0m"},
		{"truecolor foreground", NewStyle().ForegroundRGB(255, 128, 0).Sprint("hi"), "\x1b[38;2;255;128;0mhi\x1b[0m"},
		{"truecolor background", NewStyle().BackgroundRGB(0, 64, 128).Sprint("hi"), "\x1b[48;2;0;64;128mhi\x1b[0m"},
		{"bold + truecolor fg", NewStyle().Bold().ForegroundRGB(10, 20, 30).Sprint("hi"), "\x1b[1;38;2;10;20;30mhi\x1b[0m"},
		{"256-color foreground", NewStyle().Foreground256(208).Sprint("hi"), "\x1b[38;5;208mhi\x1b[0m"},
		{"256-color background", NewStyle().Background256(17).Sprint("hi"), "\x1b[48;5;17mhi\x1b[0m"},
		{"hex foreground", NewStyle().ForegroundHex("#ff8000").Sprint("hi"), "\x1b[38;2;255;128;0mhi\x1b[0m"},
		{"hex background short form", NewStyle().BackgroundHex("#f80").Sprint("hi"), "\x1b[48;2;255;136;0mhi\x1b[0m"},
		{"invalid hex is ignored", NewStyle().ForegroundHex("nope").Sprint("hi"), "hi"},
		{"raw escape hatch (256-color)", NewStyle().Raw("38;5;208").Sprint("hi"), "\x1b[38;5;208mhi\x1b[0m"},
		{"every text attribute", NewStyle().Bold().Faint().Italic().Underline().Blink().RapidBlink().Reverse().Conceal().Strikethrough().Sprint("x"), "\x1b[1;2;3;4;5;6;7;8;9mx\x1b[0m"},
		{"no attributes is identity", NewStyle().Sprint("hi"), "hi"},
		{"Sprintf formats then styles", NewStyle().Italic().Sprintf("v%d", 2), "\x1b[3mv2\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestStyle_merge(t *testing.T) {
	base := NewStyle().Bold()
	highlight := NewStyle().ForegroundANSI(ANSIColorRed).Underline()
	if got, want := base.Merge(highlight).Sprint("x"), "\x1b[1;31;4mx\x1b[0m"; got != want {
		t.Errorf("Merge = %q, want %q", got, want)
	}
}

func TestStyle_clone(t *testing.T) {
	base := NewStyle().Bold()
	heading := base.Clone().ForegroundANSI(ANSIColorCyan)
	if got, want := heading.Sprint("x"), "\x1b[1;36mx\x1b[0m"; got != want {
		t.Errorf("clone branch = %q, want %q", got, want)
	}
	// Cloning is non-destructive: the base is untouched.
	if got, want := base.Sprint("x"), "\x1b[1mx\x1b[0m"; got != want {
		t.Errorf("base mutated by a clone = %q, want %q", got, want)
	}
}

func TestStyler_set(t *testing.T) {
	styler := NewStyler().
		Set("error", NewStyle().Bold().ForegroundANSI(ANSIColorRed)).
		Set("hint", NewStyle().Faint())
	if got, want := styler.Render("error", "boom"), "\x1b[1;31mboom\x1b[0m"; got != want {
		t.Errorf("Render(error) = %q, want %q", got, want)
	}
	if got, want := styler.Render("hint", "psst"), "\x1b[2mpsst\x1b[0m"; got != want {
		t.Errorf("Render(hint) = %q, want %q", got, want)
	}
}

func TestStyler_defineWritesThrough(t *testing.T) {
	styler := NewStyler()
	// Define returns a live pointer into the registry; chaining updates it in place.
	styler.Define("warning").ForegroundHex("#fcba03").Bold()
	if got, want := styler.Render("warning", "careful"), "\x1b[38;2;252;186;3;1mcareful\x1b[0m"; got != want {
		t.Errorf("Render(warning) = %q, want %q", got, want)
	}
}

func TestStyler_renderUnknownKey(t *testing.T) {
	styler := NewStyler().Set("known", NewStyle().Bold())
	if got := styler.Render("missing", "plain"); got != "plain" {
		t.Errorf("Render(missing) = %q, want unchanged", got)
	}
}

func TestStyler_setEnabled(t *testing.T) {
	styler := NewStyler().Set("error", NewStyle().Bold().ForegroundANSI(ANSIColorRed))

	// Global switch off: nothing is styled, regardless of the registered style.
	styler.SetEnabled(false)
	if got := styler.Render("error", "boom"); got != "boom" {
		t.Errorf("disabled Render = %q, want identity", got)
	}
	// Back on: styling resumes.
	styler.SetEnabled(true)
	if got, want := styler.Render("error", "boom"), "\x1b[1;31mboom\x1b[0m"; got != want {
		t.Errorf("re-enabled Render = %q, want %q", got, want)
	}
}

func TestStyler_getAndDelete(t *testing.T) {
	styler := NewStyler().Set("error", NewStyle().Bold().ForegroundANSI(ANSIColorRed))

	style, ok := styler.Get("error")
	if !ok {
		t.Fatalf("Get(error) ok = false, want true")
	}
	if got, want := style.Sprint("x"), "\x1b[1;31mx\x1b[0m"; got != want {
		t.Errorf("got style = %q, want %q", got, want)
	}

	styler.Delete("error")
	if _, ok := styler.Get("error"); ok {
		t.Errorf("Get(error) after Delete ok = true, want false")
	}
}

func ExampleStyle() {
	s := NewStyle().Bold().ForegroundANSI(ANSIColorCyan)
	// %q makes the escape sequences visible; in a terminal these render as color.
	fmt.Printf("%q\n", s.Sprint("ready"))
	// Output: "\x1b[1;36mready\x1b[0m"
}

func ExampleStyler() {
	styler := NewStyler()
	styler.Define("warning").ForegroundANSI(ANSIColorYellow)
	styler.Set("error", NewStyle().Bold().ForegroundANSI(ANSIColorRed))

	fmt.Printf("%q\n", styler.Render("warning", "disk almost full"))
	fmt.Printf("%q\n", styler.Render("error", "out of memory"))
	// Output:
	// "\x1b[33mdisk almost full\x1b[0m"
	// "\x1b[1;31mout of memory\x1b[0m"
}

func TestStyle_disabledStrips(t *testing.T) {
	// A disabled style removes any ANSI already in the text, so partially-styled
	// input comes out clean — even attributes the style itself never set.
	styled := "\x1b[1mbold\x1b[0m and \x1b[31mred\x1b[0m"
	if got, want := NewStyle().Bold().SetEnabled(false).Sprint(styled), "bold and red"; got != want {
		t.Errorf("disabled Sprint = %q, want %q", got, want)
	}
}

func TestStyler_disabledStrips(t *testing.T) {
	// The global switch off strips, regardless of the key (or whether it exists).
	styler := NewStyler().Set("error", NewStyle().Bold())
	styler.SetEnabled(false)
	styled := "\x1b[1mloud\x1b[0m"
	if got := styler.Render("error", styled); got != "loud" {
		t.Errorf("disabled Render(known) = %q, want stripped", got)
	}
	if got := styler.Render("missing", styled); got != "loud" {
		t.Errorf("disabled Render(missing) = %q, want stripped", got)
	}
}

func TestStripStyles(t *testing.T) {
	styled := "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[23m \x1b[38;5;208mcolor\x1b[39m"
	link := "see \x1b]8;;https://example.dev\x07example.dev\x1b]8;;\x07 docs"
	linkST := "see \x1b]8;;https://example.dev\x1b\\example.dev\x1b]8;;\x1b\\ docs"

	if got, want := stripStyles(styled), "bold italic color"; got != want {
		t.Errorf("stripStyles(CSI) = %q, want %q", got, want)
	}
	// Hyperlinks keep their visible text in both terminator forms.
	if got, want := stripStyles(link), "see example.dev docs"; got != want {
		t.Errorf("stripStyles(BEL link) = %q, want %q", got, want)
	}
	if got, want := stripStyles(linkST), "see example.dev docs"; got != want {
		t.Errorf("stripStyles(ST link) = %q, want %q", got, want)
	}
	// Plain text is untouched.
	if got := stripStyles("plain"); got != "plain" {
		t.Errorf("stripStyles(plain) = %q, want unchanged", got)
	}
}
