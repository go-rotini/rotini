package tortellini

import (
	"fmt"
	"strings"
	"testing"
)

func TestStyle_fluent(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"bold", Style{}.Bold().Sprint("hi"), "\x1b[1mhi\x1b[0m"},
		{"bold+italic chain", Style{}.Bold().Italic().Sprint("hi"), "\x1b[1;3mhi\x1b[0m"},
		{"foreground color", Style{}.ForegroundANSI(Red).Sprint("hi"), "\x1b[31mhi\x1b[0m"},
		{"bold + foreground", Style{}.Bold().ForegroundANSI(Red).Sprint("hi"), "\x1b[1;31mhi\x1b[0m"},
		{"background color", Style{}.BackgroundANSI(Blue).Sprint("hi"), "\x1b[44mhi\x1b[0m"},
		{"bright fg + bright bg", Style{}.ForegroundANSI(BrightRed).BackgroundANSI(BrightWhite).Sprint("hi"), "\x1b[91;107mhi\x1b[0m"},
		{"truecolor foreground", Style{}.ForegroundRGB(255, 128, 0).Sprint("hi"), "\x1b[38;2;255;128;0mhi\x1b[0m"},
		{"truecolor background", Style{}.BackgroundRGB(0, 64, 128).Sprint("hi"), "\x1b[48;2;0;64;128mhi\x1b[0m"},
		{"bold + truecolor fg", Style{}.Bold().ForegroundRGB(10, 20, 30).Sprint("hi"), "\x1b[1;38;2;10;20;30mhi\x1b[0m"},
		{"conceal", Style{}.Conceal().Sprint("hi"), "\x1b[8mhi\x1b[0m"},
		{"256-color foreground", Style{}.Foreground256(208).Sprint("hi"), "\x1b[38;5;208mhi\x1b[0m"},
		{"256-color background", Style{}.Background256(17).Sprint("hi"), "\x1b[48;5;17mhi\x1b[0m"},
		{"hex foreground", Style{}.ForegroundHex("#ff8000").Sprint("hi"), "\x1b[38;2;255;128;0mhi\x1b[0m"},
		{"hex background short form", Style{}.BackgroundHex("#f80").Sprint("hi"), "\x1b[48;2;255;136;0mhi\x1b[0m"},
		{"invalid hex is ignored", Style{}.ForegroundHex("nope").Sprint("hi"), "hi"},
		{"raw escape hatch (256-color)", Style{}.Raw("38;5;208").Sprint("hi"), "\x1b[38;5;208mhi\x1b[0m"},
		{"every text attribute", Style{}.Bold().Faint().Italic().Underline().Blink().Reverse().Strikethrough().Sprint("x"), "\x1b[1;2;3;4;5;7;9mx\x1b[0m"},
		{"zero Style is identity", Style{}.Sprint("hi"), "hi"},
		{"Sprintf formats then styles", Style{}.Italic().Sprintf("v%d", 2), "\x1b[3mv2\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestStyle_immutableBranching(t *testing.T) {
	// Each method returns a new Style, so a base can be branched without bleed.
	base := Style{}.Bold()
	red := base.ForegroundANSI(Red)
	blue := base.ForegroundANSI(Blue)
	if got, want := red.Sprint("x"), "\x1b[1;31mx\x1b[0m"; got != want {
		t.Errorf("red branch = %q, want %q", got, want)
	}
	if got, want := blue.Sprint("x"), "\x1b[1;34mx\x1b[0m"; got != want {
		t.Errorf("blue branch = %q, want %q", got, want)
	}
	if got, want := base.Sprint("x"), "\x1b[1mx\x1b[0m"; got != want {
		t.Errorf("base mutated by a branch = %q, want %q", got, want)
	}
}

func TestStyle_merge(t *testing.T) {
	base := Style{}.Bold()
	highlight := Style{}.ForegroundANSI(Red).Underline()
	if got, want := base.Merge(highlight).Sprint("x"), "\x1b[1;31;4mx\x1b[0m"; got != want {
		t.Errorf("Merge = %q, want %q", got, want)
	}
	// Merging is non-destructive: the base is unchanged and reusable.
	if got, want := base.Sprint("x"), "\x1b[1mx\x1b[0m"; got != want {
		t.Errorf("base after Merge = %q, want %q", got, want)
	}
}

func TestStyle_output(t *testing.T) {
	var buf strings.Builder
	Style{}.Bold().Fprintln(&buf, "hi")
	if got, want := buf.String(), "\x1b[1mhi\x1b[0m\n"; got != want {
		t.Errorf("Fprintln = %q, want %q", got, want)
	}
	buf.Reset()
	Style{}.Italic().Fprintf(&buf, "v%d", 2)
	if got, want := buf.String(), "\x1b[3mv2\x1b[0m"; got != want {
		t.Errorf("Fprintf = %q, want %q", got, want)
	}
}

func TestStyler_condition(t *testing.T) {
	on := NewStyler(WithCondition(func() bool { return true }))
	if got, want := on.Style().Bold().Sprint("hi"), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("condition true = %q, want %q", got, want)
	}
	off := NewStyler(WithCondition(func() bool { return false }))
	if got := off.Style().Bold().ForegroundANSI(Red).Sprint("hi"); got != "hi" {
		t.Errorf("condition false = %q, want identity", got)
	}
	// The condition is consulted per Sprint, so a runtime signal flips styling
	// without rebuilding the Style.
	enabled := true
	dynamic := NewStyler(WithCondition(func() bool { return enabled }))
	style := dynamic.Style().Bold()
	if got := style.Sprint("a"); got != "\x1b[1ma\x1b[0m" {
		t.Errorf("dynamic on = %q, want styled", got)
	}
	enabled = false
	if got := style.Sprint("a"); got != "a" {
		t.Errorf("dynamic off = %q, want identity", got)
	}
}

func TestStyle_When(t *testing.T) {
	if got := (Style{}).Bold().When(func() bool { return false }).Sprint("hi"); got != "hi" {
		t.Errorf("When(false) = %q, want identity", got)
	}
	if got, want := (Style{}).Bold().When(func() bool { return true }).Sprint("hi"), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("When(true) = %q, want %q", got, want)
	}
}

func TestStyler_nilReceiver(t *testing.T) {
	var styler *Styler
	if got, want := styler.Style().Bold().Sprint("hi"), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("nil Styler.Style() = %q, want unconditional %q", got, want)
	}
}

func ExampleStyle() {
	// Build a style fluently; whether to apply it is the program's call, gated
	// here with When. With styling off the same Style passes text through.
	noColor := true
	emphasis := Style{}.Bold().ForegroundANSI(Cyan).When(func() bool { return !noColor })
	fmt.Println(emphasis.Sprint("ready")) // gated off → no escapes
	// Output: ready
}

func TestStripStyles(t *testing.T) {
	styled := "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[23m \x1b[38;5;208mcolor\x1b[39m"
	link := "see \x1b]8;;https://example.dev\x07example.dev\x1b]8;;\x07 docs"
	linkST := "see \x1b]8;;https://example.dev\x1b\\example.dev\x1b]8;;\x1b\\ docs"

	// No condition: always strips.
	if got, want := StripStyles(styled), "bold italic color"; got != want {
		t.Errorf("StripStyles(no condition) = %q, want %q", got, want)
	}
	// Hyperlinks keep their visible text in both terminator forms.
	if got, want := StripStyles(link), "see example.dev docs"; got != want {
		t.Errorf("StripStyles(BEL link) = %q, want %q", got, want)
	}
	if got, want := StripStyles(linkST), "see example.dev docs"; got != want {
		t.Errorf("StripStyles(ST link) = %q, want %q", got, want)
	}
	// Plain text is untouched.
	if got := StripStyles("plain"); got != "plain" {
		t.Errorf("StripStyles(plain) = %q, want unchanged", got)
	}

	// Conditions: strip iff at least one returns true.
	yes := func() bool { return true }
	no := func() bool { return false }
	if got := StripStyles(styled, no); got != styled {
		t.Errorf("StripStyles(all false) = %q, want unchanged", got)
	}
	if got, want := StripStyles(styled, no, yes), "bold italic color"; got != want {
		t.Errorf("StripStyles(any true) = %q, want %q", got, want)
	}
	// A nil condition is skipped, not panicked.
	if got := StripStyles(styled, nil); got != styled {
		t.Errorf("StripStyles(nil cond) = %q, want unchanged", got)
	}
}

func ExampleStripStyles() {
	help := "\x1b[1mNAME\x1b[0m\n    mycli - a demo"
	noStyles := func() bool { return true } // e.g. a --no-styles flag

	fmt.Println(StripStyles(help, noStyles))
	// Output:
	// NAME
	//     mycli - a demo
}
