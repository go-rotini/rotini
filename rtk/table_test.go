package rtk

import (
	"bytes"
	"strings"
	"testing"
)

func TestTable_renderPlain(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out) // ColorNone

	got := p.NewTable("NAME", "AGE").
		Row("alice", "30").
		Row("bob", "100").
		String()

	want := "NAME   AGE\n" +
		"alice  30\n" +
		"bob    100\n"
	if got != want {
		t.Errorf("table =\n%q\nwant\n%q", got, want)
	}
}

func TestTable_renderWritesToOutput(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)
	p.NewTable("A").Row("x").Render()
	if want := "A\nx\n"; out.String() != want {
		t.Errorf("Render wrote %q, want %q", out.String(), want)
	}
}

func TestTable_alignsByVisibleWidthIgnoringANSI(t *testing.T) {
	// A pre-styled cell (with ANSI escapes) must align by its printable width, not its
	// byte length, so the next column lines up.
	styled := Style("hi", ColorTrueColor, Red) // visible width 2, many bytes
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out).WithColorLevel(ColorNone)

	got := p.NewTable().
		Row(styled, "a").
		Row("xyz", "b").
		String()

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), got)
	}
	l0, l1 := StripANSI(lines[0]), StripANSI(lines[1])
	if strings.IndexByte(l0, 'a') != strings.IndexByte(l1, 'b') {
		t.Errorf("columns not aligned by visible width:\n%q\n%q", l0, l1)
	}
}
