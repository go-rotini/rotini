package rotini

import (
	"os"
	"testing"
)

// The ioctl half cannot be exercised without a real terminal, and a test that allocated a pty to
// prove the kernel answers would be testing the kernel. What IS testable is everything around it:
// the COLUMNS/LINES override, the refusal to treat nonsense as an answer, and the shape of "no".

func TestTerminalSize_envOverridesWin(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	t.Setenv("LINES", "12")

	// A pipe is not a terminal, so only the override can be answering.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	cols, rows, ok := TerminalSize(w)
	if !ok || cols != 40 || rows != 12 {
		t.Errorf("TerminalSize = (%d, %d, %v), want (40, 12, true) from COLUMNS/LINES", cols, rows, ok)
	}
}

// One axis overridden and the other unknown still answers, because a caller asking for width
// should not lose it to an unanswerable height.
func TestTerminalSize_oneAxisOverridden(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	os.Unsetenv("LINES")

	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	cols, _, ok := TerminalSize(w)
	if !ok || cols != 40 {
		t.Errorf("TerminalSize = (%d, _, %v), want 40 columns from the override", cols, ok)
	}
}

// A value that is not a positive integer is not an answer. "0" is the dangerous one: a caller
// that trusted it would wrap to nothing or divide by it.
func TestTerminalSize_rejectsNonsenseOverrides(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	for _, bad := range []string{"0", "-1", "", "wide", "40.5"} {
		t.Setenv("COLUMNS", bad)
		t.Setenv("LINES", bad)
		if cols, rows, ok := TerminalSize(w); ok {
			t.Errorf("COLUMNS=%q was accepted as (%d, %d)", bad, cols, rows)
		}
	}
}

// Not a terminal, no override: the honest answer is no answer, and the caller's fallback runs.
func TestTerminalSize_notATerminal(t *testing.T) {
	os.Unsetenv("COLUMNS")
	os.Unsetenv("LINES")

	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	if cols, rows, ok := TerminalSize(w); ok {
		t.Errorf("a pipe reported a size: (%d, %d)", cols, rows)
	}
}

// A nil *os.File is a caller's mistake that must not panic — TerminalSize is exactly the call
// someone makes on a stream they have not checked.
func TestTerminalSize_nilFile(t *testing.T) {
	os.Unsetenv("COLUMNS")
	os.Unsetenv("LINES")

	if _, _, ok := TerminalSize(nil); ok {
		t.Error("nil reported a size")
	}
}

// The documented shape: a caller can always fall back in one line.
func TestTerminalSize_documentedFallbackShape(t *testing.T) {
	os.Unsetenv("COLUMNS")
	os.Unsetenv("LINES")

	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	cols, _, ok := TerminalSize(w)
	if !ok {
		cols = 80
	}
	if cols != 80 {
		t.Errorf("the fallback did not take: cols = %d", cols)
	}
}
