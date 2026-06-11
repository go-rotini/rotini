package rotini

import (
	"bytes"
	"strings"
	"testing"
)

// TestProgram_WithExit_capturesCode proves WithExit makes Execute hand the resolved code
// to a callback instead of calling os.Exit, so Execute returns and a test can assert on
// the code — here a handler's rtx.SignalExit(2).
func TestProgram_WithExit_capturesCode(t *testing.T) {
	var code int
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.SignalExit(2) }}

	NewProgram(testDef(), h).
		WithArgs([]string{"run", "x"}).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 2 {
		t.Errorf("WithExit captured code = %d, want 2", code)
	}
}

// TestProgram_WithStderr_capturesDiagnostics proves WithStderr redirects the runtime's
// own diagnostics: a panicking hook is funneled to the default RecoveredPanicFn, which writes to
// the program's stderr and exits 1.
func TestProgram_WithStderr_capturesDiagnostics(t *testing.T) {
	var code int
	errb := &bytes.Buffer{}
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { panic("boom") }}

	NewProgram(testDef(), h).
		WithArgs([]string{"run", "x"}).
		WithStderr(errb).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 1 {
		t.Errorf("panic exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("stderr = %q, want it to contain the panic message", errb.String())
	}
}

// TestProgram_WithStdout_capturesRuntimeOutput proves WithStdout redirects what the
// runtime writes directly — here the hidden __complete entry's candidates.
func TestProgram_WithStdout_capturesRuntimeOutput(t *testing.T) {
	out := &bytes.Buffer{}
	code := -1

	NewProgram(testDef(), &testHandlers{log: new([]string)}).
		WithArgs([]string{"__complete", "ru"}).
		WithStdout(out).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 0 || strings.TrimSpace(out.String()) != "run" {
		t.Errorf("__complete via Execute: out=%q code=%d, want out=%q code=0", out.String(), code, "run")
	}
}
