package rtk

import (
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

// TestHarness_capturesOutputAndParses proves the common handler-test flow: the bound
// Parser resolves the supplied argv, and output written via the bound Printer is split
// into the Out/Err capture buffers under the standard registry keys.
func TestHarness_capturesOutputAndParses(t *testing.T) {
	h := NewHarness(HarnessConfig{
		Def:  testDef(),
		Argv: []string{"run", "alice", "x", "y", "--count", "3"},
	})

	parser := rotini.MustGet[*Parser](h.Rtx, "parser")
	printer := rotini.MustGet[*Printer](h.Rtx, "printer")

	var in runInputs
	if err := parser.Parse(h.Rtx, &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Run.Arguments.Name != "alice" || in.Run.Flags.Count != 3 {
		t.Fatalf("parsed inputs unexpected: %+v", in.Run)
	}

	printer.Printf("built %s x%d\n", in.Run.Arguments.Name, in.Run.Flags.Count)
	printer.Error("boom")

	if got := h.Out.String(); got != "built alice x3\n" {
		t.Errorf("Out = %q, want %q", got, "built alice x3\n")
	}
	if got := h.Err.String(); !strings.Contains(got, "error: boom") {
		t.Errorf("Err = %q, want it to contain %q", got, "error: boom")
	}
}

// TestHarness_scriptedStdin proves the scripted Stdin reaches both the Prompter and the
// IO service, each from its own reader (so a handler using either gets the full input).
func TestHarness_scriptedStdin(t *testing.T) {
	h := NewHarness(HarnessConfig{Stdin: "alice\n"})

	pr := rotini.MustGet[*Prompter](h.Rtx, "prompt")
	got, err := pr.Line("Name")
	if err != nil || got != "alice" {
		t.Fatalf("Prompter.Line = %q, %v; want alice, nil", got, err)
	}
	if !strings.Contains(h.Out.String(), "Name") {
		t.Errorf("prompt text not captured: %q", h.Out.String())
	}

	line, err := rotini.MustGet[*IO](h.Rtx, "io").Stdin.ReadString()
	if err != nil || strings.TrimSpace(line) != "alice" {
		t.Errorf("IO.Stdin.ReadString() = %q, %v; want alice", line, err)
	}
}

// TestHarness_deterministicTerminal proves the Terminal double is side-effect-free and
// deterministic: never a terminal, with the forced color level (honored by the Printer
// too) and the default size.
func TestHarness_deterministicTerminal(t *testing.T) {
	h := NewHarness(HarnessConfig{Color: ColorTrueColor})

	term := rotini.MustGet[*Terminal](h.Rtx, "terminal")
	if term.IsTerminal() || term.StdinIsTerminal() || term.StderrIsTerminal() {
		t.Error("harness Terminal should report non-terminal on every stream")
	}
	if term.ColorLevel() != ColorTrueColor {
		t.Errorf("Terminal.ColorLevel() = %v, want forced ColorTrueColor", term.ColorLevel())
	}
	if term.Size() != DefaultSize {
		t.Errorf("Terminal.Size() = %v, want %v", term.Size(), DefaultSize)
	}
	if rotini.MustGet[*Printer](h.Rtx, "printer").ColorLevel() != ColorTrueColor {
		t.Error("Printer should honor the forced color level")
	}
}

// TestHarness_zeroConfig proves the zero config yields a usable, fully-bound harness
// (all standard keys present, Binder absent until requested).
func TestHarness_zeroConfig(t *testing.T) {
	h := NewHarness(HarnessConfig{})
	for _, key := range []string{"io", "terminal", "printer", "prompt", "progress", "parser"} {
		if !h.Rtx.Has(key) {
			t.Errorf("zero-config harness missing standard service %q", key)
		}
	}
	if h.Rtx.Has("binder") || h.Binder != nil {
		t.Error("binder should be absent unless HarnessConfig.Binder is set")
	}
}
