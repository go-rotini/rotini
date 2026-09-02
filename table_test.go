package rotini

import (
	"bytes"
	"strings"
	"testing"
)

func TestTable_alignsColumns(t *testing.T) {
	got := NewTable("NAME", "SIZE").
		Row("alpha", "1").
		Row("b", "1000").
		Render()
	want := strings.Join([]string{
		"NAME   SIZE",
		"alpha  1",
		"b      1000",
	}, "\n")
	if got != want {
		t.Errorf("Render() =\n%s\nwant\n%s", got, want)
	}
}

// Trailing padding on the last column would be invisible but real — it shows up in
// golden files and in `diff`, so it is trimmed.
func TestTable_trimsTrailingPadding(t *testing.T) {
	for _, line := range strings.Split(NewTable("A", "B").Row("x", "y").Row("longer", "z").Render(), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}

func TestTable_align(t *testing.T) {
	got := NewTable("N", "V").
		WithAlign(AlignLeft, AlignRight).
		Row("a", "1").
		Row("b", "1000").
		Render()
	if !strings.Contains(got, "a     1\n") {
		t.Errorf("right-aligned column not padded on the left:\n%s", got)
	}
}

// Widths are measured in DISPLAY cells, so a styled cell aligns with a plain one
// of the same visible length rather than being pushed out by its escape bytes.
func TestTable_measuresDisplayWidthNotBytes(t *testing.T) {
	styled := NewStyle().Bold().Sprint("ab")
	got := NewTable().Row(styled, "x").Row("ab", "y").Render()
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	if Width(lines[0]) != Width(lines[1]) {
		t.Errorf("styled and plain rows differ in display width:\n%q (%d)\n%q (%d)",
			lines[0], Width(lines[0]), lines[1], Width(lines[1]))
	}
}

// A wide-East-Asian rune occupies two cells; alignment must account for that.
func TestTable_wideRunes(t *testing.T) {
	got := NewTable().Row("日本", "x").Row("ab", "y").Render()
	lines := strings.Split(got, "\n")
	if Width(lines[0]) != Width(lines[1]) {
		t.Errorf("wide-rune row misaligned:\n%q (%d)\n%q (%d)", lines[0], Width(lines[0]), lines[1], Width(lines[1]))
	}
}

func TestTable_widthBudgetTruncates(t *testing.T) {
	got := NewTable("NAME", "DESCRIPTION").
		Row("alpha", "a very long description indeed").
		WithWidth(24).
		Render()
	for _, line := range strings.Split(got, "\n") {
		if w := Width(line); w > 24 {
			t.Errorf("line %q is %d cells, over the 24 budget", line, w)
		}
	}
	if !strings.Contains(got, "…") {
		t.Errorf("truncated table has no ellipsis:\n%s", got)
	}
}

// Truncating styled text drops its escapes rather than cutting one in half, which
// would leave the terminal in an unknown state.
func TestTable_truncateStyledLeavesNoPartialEscape(t *testing.T) {
	styled := NewStyle().Bold().Sprint("abcdefghij")
	got := NewTable().Row(styled).WithWidth(5).Render()
	if strings.Contains(got, "\x1b") {
		t.Errorf("truncated cell retains an escape sequence: %q", got)
	}
	if Width(got) > 5 {
		t.Errorf("truncated cell is %d cells, over budget: %q", Width(got), got)
	}
}

// An empty table prints NOTHING — not a blank line — so a command with no results
// stays quiet.
func TestTable_emptyPrintsNothing(t *testing.T) {
	if got := NewTable().Render(); got != "" {
		t.Errorf("empty Render() = %q, want empty", got)
	}
	var buf bytes.Buffer
	if n, err := NewTable().Fprint(&buf); err != nil || n != 0 || buf.Len() != 0 {
		t.Errorf("empty Fprint wrote %d bytes (%q), err=%v", n, buf.String(), err)
	}
}

func TestTable_fprintEndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	if _, err := NewTable("A").Row("x").Fprint(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("Fprint output %q does not end in a newline", buf.String())
	}
}

// Ragged rows are legal: a short row is padded with empty cells.
func TestTable_raggedRows(t *testing.T) {
	got := NewTable("A", "B", "C").Row("1").Row("1", "2", "3").Render()
	if len(strings.Split(got, "\n")) != 3 {
		t.Errorf("ragged table lost a row:\n%s", got)
	}
}

// Styling stays optional: a table with no Styler renders plain (rule 8 — each
// battery is usable alone).
func TestTable_stylerIsOptional(t *testing.T) {
	plain := NewTable("A").Row("x").Render()
	if strings.Contains(plain, "\x1b") {
		t.Errorf("table without a Styler emitted escapes: %q", plain)
	}
	styler := NewStyler()
	styler.Define("header").Bold()
	styled := NewTable("A").Row("x").WithStyler(styler).Render()
	if !strings.Contains(styled, "\x1b") {
		t.Errorf("table with a Styler did not style its header: %q", styled)
	}
	if Strip(styled) != plain {
		t.Errorf("styling changed the layout:\n%q\nvs\n%q", Strip(styled), plain)
	}
}
