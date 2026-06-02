package rtk

import (
	"os"
	"testing"
)

// nonTTYFile returns an *os.File that is definitely not a terminal (a pipe), for the
// negative paths of the Terminal service.
func nonTTYFile(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r
}

func TestTerminal_isTerminalPerStream(t *testing.T) {
	f := nonTTYFile(t)
	term := NewTerminal().WithStdin(f).WithStdout(f).WithStderr(f)
	if term.IsTerminal() {
		t.Error("pipe stdout should not be a terminal")
	}
	if term.StdinIsTerminal() {
		t.Error("pipe stdin should not be a terminal")
	}
	if term.StderrIsTerminal() {
		t.Error("pipe stderr should not be a terminal")
	}
}

func TestTerminal_size(t *testing.T) {
	f := nonTTYFile(t)

	t.Setenv("COLUMNS", "")
	t.Setenv("LINES", "")
	if got := NewTerminal().WithStdout(f).Size(); got != DefaultSize {
		t.Errorf("Size(non-tty, no hints) = %+v, want %+v", got, DefaultSize)
	}

	t.Setenv("COLUMNS", "120")
	t.Setenv("LINES", "40")
	if got := NewTerminal().WithStdout(f).Size(); got.Cols != 120 || got.Rows != 40 {
		t.Errorf("Size(env hints) = %+v, want {120 40}", got)
	}

	// A forced size wins over detection and env.
	if got := NewTerminal().WithStdout(f).WithSize(Size{Cols: 100, Rows: 50}).Size(); got != (Size{Cols: 100, Rows: 50}) {
		t.Errorf("Size(forced) = %+v, want {100 50}", got)
	}
}

func TestTerminal_colorLevel(t *testing.T) {
	f := nonTTYFile(t)

	// Forced level bypasses detection entirely.
	if got := NewTerminal().WithStdout(f).WithColorLevel(ColorTrueColor).ColorLevel(); got != ColorTrueColor {
		t.Errorf("forced ColorLevel = %s, want truecolor", got)
	}

	// NO_COLOR is the first check, so it wins deterministically regardless of any
	// FORCE_COLOR the surrounding test environment may set.
	t.Setenv("NO_COLOR", "1")
	if got := NewTerminal().WithStdout(f).ColorLevel(); got != ColorNone {
		t.Errorf("ColorLevel(NO_COLOR) = %s, want none", got)
	}
}

func TestTerminal_makeRawNonTerminalAndRestoreNil(t *testing.T) {
	if _, err := NewTerminal().WithStdin(nonTTYFile(t)).MakeRaw(); err == nil {
		t.Error("MakeRaw on a non-terminal stdin should error")
	}
	// Restore with a nil state is a safe no-op (the deferred-restore pattern).
	if err := NewTerminal().Restore(nil); err != nil {
		t.Errorf("Restore(nil) = %v, want nil", err)
	}
}
