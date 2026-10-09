package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestProgram_nilRestoresDefault pins that a nil argument to each With option undoes an earlier
// setting, leaving what NewProgram set.
func TestProgram_nilRestoresDefault(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := NewProgram(testDef(), &testHandlers{log: new([]string)}).
		WithStdin(strings.NewReader("x")).WithStdout(&buf).WithStderr(&buf).
		WithExit(func(int) {}).
		WithArgs([]string{"run"}).
		WithContext(context.Background()).
		WithInputReader(NewInputReader).
		WithHelp(func(...string) (string, error) { return "page", nil }).
		WithParser(NewParser()).
		WithResolver(DefaultResolver).
		WithLifecycle(DefaultLifecycle)

	var noCtx context.Context // a nil context, the argument under test
	p.WithStdin(nil).WithStdout(nil).WithStderr(nil).WithExit(nil).WithArgs(nil).WithContext(noCtx).
		WithInputReader(nil).WithHelp(nil).WithParser(nil).WithResolver(nil).WithLifecycle(nil)

	if p.stdin != os.Stdin || p.stdout != os.Stdout || p.stderr != os.Stderr {
		t.Error("a nil stream did not restore the os stream")
	}
	if fmt.Sprintf("%p", p.exit) != fmt.Sprintf("%p", os.Exit) {
		t.Error("WithExit(nil) did not restore os.Exit")
	}
	if !slices.Equal(p.args, os.Args[1:]) {
		t.Errorf("WithArgs(nil) args = %q, want os.Args[1:]", p.args)
	}
	if p.ctx != nil {
		t.Error("WithContext(nil) kept the supplied context")
	}
	if p.readerFn != nil || p.help != nil || p.parser != nil || p.resolver != nil || p.lifecycle != nil {
		t.Error("a nil setting kept the earlier value")
	}
}

// TestProgram_withSignalsEmptyRestoresTheDefaultTrap: WithSignals() undoes both an earlier
// WithSignals(x) and WithoutSignalHandling.
func TestProgram_withSignalsEmptyRestoresTheDefaultTrap(t *testing.T) {
	t.Parallel()
	p := NewProgram(testDef(), nil).WithSignals(os.Interrupt).WithSignals()
	if p.signalMode != signalAuto || p.signalSet != nil {
		t.Errorf("after WithSignals(x).WithSignals(): mode %v, set %v", p.signalMode, p.signalSet)
	}
	p.WithoutSignalHandling().WithSignals()
	if p.signalMode != signalAuto {
		t.Errorf("after WithoutSignalHandling().WithSignals(): mode %v", p.signalMode)
	}
}

// TestProgram_withHelpNilRemovesThePages: WithHelp(nil) leaves Context.Help empty.
func TestProgram_withHelpNilRemovesThePages(t *testing.T) {
	t.Parallel()
	var page string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { page = rtx.Help() }}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithHelp(func(...string) (string, error) { return "page", nil }).WithHelp(nil)
	if _, err := p.Run(p.args); err != nil || page != "" {
		t.Errorf("Help() = %q (err %v), want no page", page, err)
	}
}

// TestProgram_withParserReachesTheRun: the parser set on the program is the one a handler gets.
func TestProgram_withParserReachesTheRun(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	var got *Parser
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.Parser() }}
	p, _, _ := newTestProgram(h, []string{"run"})
	if _, err := p.WithParser(parser).Run(p.args); err != nil || got != parser {
		t.Errorf("Parser() = %p (err %v), want the program's %p", got, err, parser)
	}
}

// TestContext_streamSetters: each setter replaces its stream, nil restores the os stream, and a
// nil receiver returns nil.
func TestContext_streamSetters(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	in := strings.NewReader("in")
	rtx := NewContextFor(testDef(), nil).WithStdin(in).WithStdout(&out).WithStderr(&errs)
	if rtx.Stdin != in || rtx.Stdout != &out || rtx.Stderr != &errs {
		t.Fatal("a stream setter did not replace its stream")
	}
	rtx.WithStdin(nil).WithStdout(nil).WithStderr(nil)
	if rtx.Stdin != os.Stdin || rtx.Stdout != os.Stdout || rtx.Stderr != os.Stderr {
		t.Error("a nil stream did not restore the os stream")
	}

	var nilCtx *Context
	if nilCtx.WithStdin(in) != nil || nilCtx.WithStdout(&out) != nil || nilCtx.WithStderr(&errs) != nil ||
		nilCtx.WithOutputChecks(true) != nil || nilCtx.WithHelp(nil) != nil || nilCtx.WithParser(nil) != nil ||
		nilCtx.WithInputReader(nil) != nil {
		t.Error("a With setter on a nil Context returned non-nil")
	}
}

// TestContext_settingsNilRestoreDefault: the standalone settings follow the nil rule too.
func TestContext_settingsNilRestoreDefault(t *testing.T) {
	t.Parallel()
	rtx := NewContextFor(testDef(), nil).
		WithHelp(func(...string) (string, error) { return "page", nil }).
		WithParser(NewParser()).
		WithInputReader(NewInputReader).
		WithOutputChecks(true)
	rtx.WithHelp(nil).WithParser(nil).WithInputReader(nil).WithOutputChecks(false)
	if rtx.help != nil || rtx.parser != nil || rtx.readerFn != nil || rtx.outputChecks {
		t.Error("a nil or false setting kept the earlier value")
	}
}

// TestContext_withStdinFeedsFlagStdin: a second WithStdin before the read wins.
func TestContext_withStdinFeedsFlagStdin(t *testing.T) {
	t.Parallel()
	rtx := NewContextFor(testDef(), nil).WithStdin(strings.NewReader("first"))
	_ = rtx.flagStdin()
	rtx.WithStdin(strings.NewReader("second"))
	b, err := io.ReadAll(rtx.flagStdin())
	if err != nil || string(b) != "second" {
		t.Errorf("read %q (err %v), want the later stream", b, err)
	}
}

// signalCtx is a run context canceled the way rotini's signal trap cancels one.
func signalCtx(code int) context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(exitCodeError{code: code, signal: true})
	return ctx
}

func reportDefault(ctx context.Context, out Outcome, code int) (stdout, stderr string, exit int) {
	var o, e bytes.Buffer
	p := NewProgram(testDef(), nil).WithStdout(&o).WithStderr(&e)
	rtx := p.newRunContext()
	rtx.exitCode = code
	rtx.setReporterStage(true)
	p.defaultReporter(ctx, rtx, out)
	return o.String(), e.String(), rtx.code()
}

// TestDefaultReporter_hidesTheRunsOwnSignal: an error the trap's cancellation caused prints
// nothing, and the exit stays the signal's code.
func TestDefaultReporter_hidesTheRunsOwnSignal(t *testing.T) {
	t.Parallel()
	ctx := signalCtx(130)
	cause := context.Cause(ctx)
	for name, err := range map[string]error{
		"the cause":           cause,
		"a wrapped cause":     &InputError{Msg: "reading stdin was interrupted", Cause: cause},
		"context.Canceled":    fmt.Errorf("fetch: %w", context.Canceled),
		"the context's error": ctx.Err(),
	} {
		for _, code := range []int{0, 130} {
			stdout, stderr, exit := reportDefault(ctx, Outcome{Errors: []error{err}}, code)
			if stdout != "" || stderr != "" || exit != 130 {
				t.Errorf("%s (code %d): stdout %q, stderr %q, exit %d; want nothing printed, exit 130", name, code, stdout, stderr, exit)
			}
		}
	}

	// Other errors still print, and keep the signal's code.
	_, stderr, exit := reportDefault(ctx, Outcome{Errors: []error{cause, errors.New("disk full")}}, 130)
	if stderr != "Error: disk full\n" || exit != 130 {
		t.Errorf("mixed: stderr %q, exit %d", stderr, exit)
	}
}

// TestDefaultReporter_showsOtherCancellations: a caller's ExitCause or plain cancellation is
// not the run's own signal, so its error prints.
func TestDefaultReporter_showsOtherCancellations(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ExitCause(3))
	_, stderr, exit := reportDefault(ctx, Outcome{Errors: []error{context.Cause(ctx)}}, 3)
	if !strings.HasPrefix(stderr, "Error: run canceled") || exit != 3 {
		t.Errorf("ExitCause: stderr %q, exit %d", stderr, exit)
	}

	plain, cancelPlain := context.WithCancel(context.Background())
	cancelPlain()
	_, stderr, exit = reportDefault(plain, Outcome{Errors: []error{plain.Err()}}, 0)
	if stderr != "Error: context canceled\n" || exit != 1 {
		t.Errorf("plain cancel: stderr %q, exit %d", stderr, exit)
	}
}

// TestStructuredReporter_hidesTheRunsOwnSignal: rotini's other reporter leaves it out too.
func TestStructuredReporter_hidesTheRunsOwnSignal(t *testing.T) {
	t.Parallel()
	ctx := signalCtx(130)
	for _, structured := range []func(*Context) bool{wantsJSON, nil} {
		rtx := NewContextFor(Definition{Name: "taskr", Commands: []CommandDef{{Name: "list"}}}, []string{"list", "--json"})
		var o, e bytes.Buffer
		rtx.WithStdout(&o).WithStderr(&e)
		StructuredReporter(structured)(ctx, rtx, Outcome{Errors: []error{context.Cause(ctx)}})
		if o.Len() != 0 || e.Len() != 0 || rtx.code() != 130 {
			t.Errorf("stdout %q, stderr %q, exit %d", o.String(), e.String(), rtx.code())
		}
	}
}
