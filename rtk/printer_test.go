package rtk

import (
	"bytes"
	"testing"
)

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
