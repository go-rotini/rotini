package rotini

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestProgram_WithExit_capturesCode proves WithExit makes Execute hand the resolved code
// to a callback instead of calling os.Exit, so Execute returns and a test can assert on
// the code — here a handler's rtx.SignalExit(1).
func TestProgram_WithExit_capturesCode(t *testing.T) {
	var code int
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.SignalExit(1) }}

	NewProgram(testDef(), h).
		WithArgs([]string{"run", "x"}).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 1 {
		t.Errorf("WithExit captured code = %d, want 1", code)
	}
}

// TestProgram_WithStderr_capturesDiagnostics proves WithStderr redirects the runtime's
// own diagnostics: a panicking hook is funneled to the default OnPanic, which writes to
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
		t.Errorf("panic exit code = %d, want %d", code, 1)
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

// TestProgram_Run_isReentrant proves the contract [Program.Run] exists for: a Program is
// reusable across invocations (the REPL / daemon / stdio-server case). Each call gets a
// fresh Context, so neither the recorded outcomes nor the exit code of one run leak into
// the next.
func TestProgram_Run_isReentrant(t *testing.T) {
	var n int
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		n++
		if n == 1 { // only the FIRST run fails
			rtx.RecordError(errors.New("first run failed"))
			rtx.SignalExit(3)
		}
	}}
	p, _, errb := newTestProgram(h, nil)

	if code, _ := p.Run([]string{"run", "x"}); code != 3 {
		t.Fatalf("run 1 code = %d, want 3", code)
	}
	if !strings.Contains(errb.String(), "first run failed") {
		t.Errorf("run 1 stderr = %q, want the recorded error", errb.String())
	}

	errb.Reset()
	code, err := p.Run([]string{"run", "x"})
	if code != 0 || err != nil {
		t.Errorf("run 2 = (%d, %v), want (0, nil) — run 1's exit code and errors must not carry over", code, err)
	}
	if errb.String() != "" {
		t.Errorf("run 2 stderr = %q, want empty — run 1's records must not be re-reported", errb.String())
	}
}

// TestProgram_Run_seedsBoundServicesPerRun pins the registry half of the re-entrancy
// contract: services bound with [Program.Bind] reach EVERY run, while a binding a handler
// makes DURING a run is local to that run.
func TestProgram_Run_seedsBoundServicesPerRun(t *testing.T) {
	var seeded, leaked []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_, ok := Get[string](rtx, "seeded")
		seeded = append(seeded, ok)
		_, ok = Get[string](rtx, "per-run")
		leaked = append(leaked, ok)
		rtx.Bind("per-run", "bound during this run")
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Bind("seeded", "bound before any run")

	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if want := []bool{true, true}; !slices.Equal(seeded, want) {
		t.Errorf("Program.Bind visibility = %v, want %v — a seeded service reaches every run", seeded, want)
	}
	if want := []bool{false, false}; !slices.Equal(leaked, want) {
		t.Errorf("in-run Bind visibility = %v, want %v — a run's own binding must not leak forward", leaked, want)
	}
}

// TestProgram_RunContext_scopesOneInvocation pins why RunContext exists alongside
// WithContext: a host that dispatches many invocations (a REPL, a stdio server)
// must be able to scope EACH one without permanently changing the program.
func TestProgram_RunContext_scopesOneInvocation(t *testing.T) {
	var seen []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {}}
	p, _, _ := newTestProgram(h, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := p.RunContext(ctx, []string{"run", "x"}); err != nil {
		t.Fatalf("RunContext = %v", err)
	}
	cancel()
	// The program was NOT mutated: a later plain Run still owns its own context.
	if p.ctx != nil {
		t.Error("RunContext mutated the program's context")
	}
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Errorf("Run after RunContext = %v, want nil", err)
	}
	_ = seen
}

// A canceled context halts the invocation.
func TestProgram_RunContext_cancellationHalts(t *testing.T) {
	var ran bool
	h := &testHandlers{log: new([]string), onRun: func(*Context) { ran = true }}
	p, _, _ := newTestProgram(h, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.RunContext(ctx, []string{"run", "x"}); err != nil {
		t.Fatalf("RunContext on a canceled context = %v", err)
	}
	if ran {
		t.Error("the handler ran under an already-canceled context")
	}
}

// A nil context is a wiring mistake, reported rather than panicking deeper.
func TestProgram_RunContext_nilContext(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	code, err := p.RunContext(nil, []string{"run", "x"})
	if !errors.Is(err, ErrInternal) || code == 0 {
		t.Errorf("RunContext(nil) = (%d, %v), want a non-zero code and an internal error", code, err)
	}
}
