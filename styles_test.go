package rotini

import (
	"fmt"
	"testing"
)

func TestStyles_Sprint(t *testing.T) {
	st := NewStyles()

	if got, want := st.With(Bold).Sprint("hi"), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("Bold = %q, want %q", got, want)
	}
	// Composition joins SGR params with ';'.
	if got, want := st.With(Bold, FgRed).Sprint("hi"), "\x1b[1;31mhi\x1b[0m"; got != want {
		t.Errorf("Bold+FgRed = %q, want %q", got, want)
	}
	// The Raw escape hatch carries arbitrary SGR (256-color here).
	if got, want := st.With(Raw("38;5;208")).Sprint("hi"), "\x1b[38;5;208mhi\x1b[0m"; got != want {
		t.Errorf("Raw = %q, want %q", got, want)
	}
	// No attributes => identity.
	if got := st.With().Sprint("hi"); got != "hi" {
		t.Errorf("empty style = %q, want unchanged", got)
	}
	// Sprintf formats, then styles.
	if got, want := st.With(Italic).Sprintf("v%d", 2), "\x1b[3mv2\x1b[0m"; got != want {
		t.Errorf("Sprintf = %q, want %q", got, want)
	}
}

func TestStyles_Condition(t *testing.T) {
	on := NewStyles(WithCondition(func() bool { return true }))
	if got, want := on.With(Bold).Sprint("hi"), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("condition true = %q, want styled %q", got, want)
	}

	off := NewStyles(WithCondition(func() bool { return false }))
	if got := off.With(Bold, FgRed).Sprint("hi"); got != "hi" {
		t.Errorf("condition false = %q, want identity", got)
	}

	// The condition is consulted per Sprint, so a runtime-changing signal flips
	// styling without rebuilding the Style.
	enabled := true
	dyn := NewStyles(WithCondition(func() bool { return enabled }))
	style := dyn.With(Bold)
	if got := style.Sprint("a"); got != "\x1b[1ma\x1b[0m" {
		t.Errorf("dynamic on = %q, want styled", got)
	}
	enabled = false
	if got := style.Sprint("a"); got != "a" {
		t.Errorf("dynamic off = %q, want identity", got)
	}
}

func TestStripStyles(t *testing.T) {
	styled := "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[23m \x1b[38;5;208mcolor\x1b[39m"
	link := "see \x1b]8;;https://rotini.dev\x07rotini.dev\x1b]8;;\x07 docs"
	linkST := "see \x1b]8;;https://rotini.dev\x1b\\rotini.dev\x1b]8;;\x1b\\ docs"

	// No condition: always strips.
	if got, want := StripStyles(styled), "bold italic color"; got != want {
		t.Errorf("StripStyles(no condition) = %q, want %q", got, want)
	}
	// Hyperlinks keep their visible text in both terminator forms.
	if got, want := StripStyles(link), "see rotini.dev docs"; got != want {
		t.Errorf("StripStyles(BEL link) = %q, want %q", got, want)
	}
	if got, want := StripStyles(linkST), "see rotini.dev docs"; got != want {
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

func ExampleStyles() {
	// Whether to style is the program's call, expressed as a condition. With
	// it OFF the same Style passes text through unchanged — the no-color path.
	noColor := true
	st := NewStyles(WithCondition(func() bool { return !noColor }))

	emph := st.With(Bold, FgCyan)
	fmt.Println(emph.Sprint("ready")) // condition false → no escapes
	// Output: ready
}

func ExampleStripStyles() {
	help := "\x1b[1mNAME\x1b[0m\n    mycli - a demo"
	noStyles := func() bool { return true } // e.g. a --no-styles flag

	fmt.Println(StripStyles(help, noStyles))
	// Output:
	// NAME
	//     mycli - a demo
}
