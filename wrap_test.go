package rotini

import (
	"strings"
	"testing"
)

// Width already solved measuring styled text. Wrap and Truncate are the part it left to the
// caller: cutting without splitting an escape sequence, which would leave the terminal wearing
// whatever style the fragment half-opened.

const wrapRed = "\x1b[31m"
const wrapReset = "\x1b[0m"

func TestTruncate(t *testing.T) {
	for _, c := range []struct {
		name           string
		text           string
		cols           int
		ellipsis, want string
	}{
		{"fits, unchanged", "hello", 10, "…", "hello"},
		{"exact fit is not truncated", "hello", 5, "…", "hello"},
		{"cut with an ellipsis", "hello world", 8, "…", "hello w…"},
		{"the ellipsis costs its own width", "hello world", 8, "...", "hello..."},
		{"no ellipsis is a hard cut", "hello world", 5, "", "hello"},
		{"a non-positive budget means no limit", "hello world", 0, "…", "hello world"},
		{"an ellipsis wider than the budget is dropped", "hello world", 2, "[...]", "he"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Truncate(c.text, c.cols, c.ellipsis); got != c.want {
				t.Errorf("Truncate(%q, %d, %q) = %q, want %q", c.text, c.cols, c.ellipsis, got, c.want)
			}
		})
	}
}

// TestTruncate_keepsStylingWhole is the reason this is not strings.Cut: the budget counts cells,
// and an escape sequence costs none — so it must never be split, and must not be charged for.
func TestTruncate_keepsStylingWhole(t *testing.T) {
	styled := wrapRed + "hello world" + wrapReset
	got := Truncate(styled, 8, "…")

	if Width(got) != 8 {
		t.Errorf("Width = %d, want 8 — escape sequences must not count against the budget: %q", Width(got), got)
	}
	if !strings.HasPrefix(got, wrapRed) {
		t.Errorf("the opening sequence was dropped: %q", got)
	}
	if strings.Count(got, "\x1b") != strings.Count(got, "\x1b[") {
		t.Errorf("an escape sequence was cut in half: %q", got)
	}
	if Strip(got) != "hello w…" {
		t.Errorf("visible text = %q, want %q", Strip(got), "hello w…")
	}
}

// A wide rune costs two cells, so the budget must stop before it rather than in it.
func TestTruncate_wideRunes(t *testing.T) {
	got := Truncate("日本語テキスト", 5, "…")
	if Width(got) > 5 {
		t.Errorf("Width = %d, want at most 5: %q", Width(got), got)
	}
}

func TestWrap(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		cols int
		want string
	}{
		{"short text is untouched", "hello", 20, "hello"},
		{"wraps at a space", "one two three four", 8, "one two\nthree\nfour"},
		{"exact width does not wrap early", "abc def", 7, "abc def"},
		{"existing newlines are paragraph breaks", "one two\nthree four", 7, "one two\nthree\nfour"},
		{"a word longer than the budget is broken", "supercalifragilistic", 8, "supercal\nifragili\nstic"},
		{"a non-positive budget means no limit", "one two three", 0, "one two three"},
		{"empty stays empty", "", 10, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Wrap(c.text, c.cols); got != c.want {
				t.Errorf("Wrap(%q, %d) =\n %q\nwant\n %q", c.text, c.cols, got, c.want)
			}
		})
	}
}

// TestWrap_everyLineFitsTheBudget is the property that matters more than any single expectation.
func TestWrap_everyLineFitsTheBudget(t *testing.T) {
	text := "rotini measures styled text in display cells, which is what lets a table line up " +
		"when a column holds colour or a wide rune like 日本語."
	for _, cols := range []int{10, 20, 40, 80} {
		for _, line := range strings.Split(Wrap(text, cols), "\n") {
			if Width(line) > cols {
				t.Errorf("cols=%d: line of width %d exceeds the budget: %q", cols, Width(line), line)
			}
		}
	}
}

// TestWrap_stylingSurvivesWithoutReopening: a newline does not wrapReset a terminal's state, so a
// colour opened before the break is still in effect after it. That is why Wrap does not have to
// close and reopen sequences — and the test pins that it does not start inventing them.
func TestWrap_stylingSurvivesWithoutReopening(t *testing.T) {
	got := Wrap(wrapRed+"one two three four"+wrapReset, 8)

	if n := strings.Count(got, wrapRed); n != 1 {
		t.Errorf("the opening sequence appears %d times, want 1 — Wrap must not reopen styling: %q", n, got)
	}
	if n := strings.Count(got, wrapReset); n != 1 {
		t.Errorf("the wrapReset appears %d times, want 1: %q", n, got)
	}
	if Strip(got) != "one two\nthree\nfour" {
		t.Errorf("visible text = %q", Strip(got))
	}
}

// TestWrap_doesNotCountEscapesAgainstTheBudget: the whole point of measuring in cells.
func TestWrap_doesNotCountEscapesAgainstTheBudget(t *testing.T) {
	plain := Wrap("one two three four", 8)
	styled := Wrap(wrapRed+"one"+wrapReset+" "+wrapRed+"two"+wrapReset+" three four", 8)

	if Strip(styled) != plain {
		t.Errorf("styling changed where the breaks fall:\n plain  %q\n styled %q", plain, Strip(styled))
	}
}
