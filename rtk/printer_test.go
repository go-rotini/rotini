package rtk

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrinter_YAML(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)
	if err := p.YAML(map[string]int{"count": 5}); err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if got := out.String(); got != "count: 5\n" {
		t.Errorf("YAML = %q, want %q", got, "count: 5\n")
	}
}

func TestPrinter_Wrap(t *testing.T) {
	p := NewPrinter().WithOutput(&bytes.Buffer{}).WithWidth(10)
	got := p.Wrap("the quick brown fox jumps")
	want := "the quick\nbrown fox\njumps"
	if got != want {
		t.Errorf("Wrap =\n%q\nwant\n%q", got, want)
	}
	// Existing newlines are preserved (each line wrapped independently).
	if got := p.Wrap("short\nalso short"); got != "short\nalso short" {
		t.Errorf("Wrap(multiline) = %q", got)
	}
	// A word longer than the width overflows rather than splitting.
	if got := p.Wrap("supercalifragilistic ok"); !strings.HasPrefix(got, "supercalifragilistic\n") {
		t.Errorf("Wrap(long word) = %q, want the long word on its own line", got)
	}
}

func TestPrinter_Truncate(t *testing.T) {
	p := NewPrinter().WithOutput(&bytes.Buffer{})
	if got := p.Truncate("hello world", 8); got != "hello w…" {
		t.Errorf("Truncate = %q, want %q", got, "hello w…")
	}
	if got := p.Truncate("hi", 8); got != "hi" {
		t.Errorf("Truncate(within max) = %q, want %q", got, "hi")
	}
	// Visible-width aware: a styled string truncates by what the eye sees (styling dropped).
	styled := Style("hello world", Color16, Red)
	if got := p.Truncate(styled, 8); visibleWidth(got) != 8 {
		t.Errorf("Truncate(styled) visible width = %d, want 8 (got %q)", visibleWidth(got), got)
	}
}

func TestPrinter_plainAndSemanticStreams(t *testing.T) {
	var out, errb bytes.Buffer
	p := NewPrinter().WithOutput(&out).WithError(&errb)

	// A buffer is not a terminal → ColorNone, so output is plain.
	if p.ColorLevel() != ColorNone {
		t.Fatalf("level for a buffer = %s, want none", p.ColorLevel())
	}

	p.Println("hello")
	p.Success("done")
	p.Info("fyi")
	p.Warning("careful %d", 1)
	p.Error("boom %d", 42)

	if wantOut := "hello\ndone\nfyi\n"; out.String() != wantOut {
		t.Errorf("out = %q, want %q", out.String(), wantOut)
	}
	if wantErr := "warning: careful 1\nerror: boom 42\n"; errb.String() != wantErr {
		t.Errorf("err = %q, want %q", errb.String(), wantErr)
	}
}

func TestPrinter_styledAtForcedLevel(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out).WithColorLevel(ColorTrueColor)

	p.Success("ok")
	if want := Style("ok", ColorTrueColor, Green) + "\n"; out.String() != want {
		t.Errorf("Success styled = %q, want %q", out.String(), want)
	}
	if got, want := p.Style("x", Red, Bold), Style("x", ColorTrueColor, Red, Bold); got != want {
		t.Errorf("Printer.Style = %q, want %q", got, want)
	}
}

func TestPrinter_JSON(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter().WithOutput(&out)
	if err := p.JSON(map[string]int{"a": 1}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if want := "{\n  \"a\": 1\n}\n"; out.String() != want {
		t.Errorf("JSON = %q, want %q", out.String(), want)
	}
}

func TestPrinter_detectForBufferAndWidthOverride(t *testing.T) {
	if level, width := detectFor(&bytes.Buffer{}); level != ColorNone || width != DefaultSize.Cols {
		t.Errorf("detectFor(buffer) = (%s, %d), want (none, %d)", level, width, DefaultSize.Cols)
	}
	p := NewPrinter().WithOutput(&bytes.Buffer{}).WithWidth(120)
	if p.Width() != 120 {
		t.Errorf("Width after override = %d, want 120", p.Width())
	}
}
