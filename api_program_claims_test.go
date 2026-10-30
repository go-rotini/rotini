package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claims of the Program, Reporter, Resolver and error godoc, checked against a hand-built
// program.

type claimHandler struct {
	NoHooks
	run func(rtx *Context)
}

func (h *claimHandler) Run(_ context.Context, rtx *Context) {
	if h.run != nil {
		h.run(rtx)
	}
}

// claimProgram is `app go`, every command's Run calling run, with captured streams and no trap.
func claimProgram(run func(rtx *Context)) (*Program, *bytes.Buffer, *bytes.Buffer) {
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "go", Handler: "Go"}}}
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgramFunc(def, func(string) (Handler, bool) {
		return &claimHandler{run: run}, true
	}).WithStdout(out).WithStderr(errb).WithoutSignalHandling()
	return p, out, errb
}

// TestProgramClaims_nilArgumentsRestoreDefaults: every With method that takes a nil restores
// its default, so a program configured with nils runs as a fresh one does.
func TestProgramClaims_nilArgumentsRestoreDefaults(t *testing.T) {
	t.Parallel()
	var help string
	var parser *Parser
	var stdout, stderr, stdin any
	p, _, _ := claimProgram(func(rtx *Context) {
		help, parser = rtx.Help(), rtx.Parser()
		stdout, stderr, stdin = rtx.Stdout, rtx.Stderr, rtx.Stdin
	})
	var noContext context.Context // nil, which WithContext reads as "restore the default"
	p.With(nil).
		WithHelp(nil).WithParser(nil).WithInputReader(nil).WithLifecycle(nil).WithResolver(nil).
		WithCompletion(nil).WithClock(nil).WithExit(nil).WithCompletionScript(nil).
		WithCompletionMessages(nil).WithCompletionDescriptions(nil).WithContext(noContext).WithReporter(nil).
		WithStdout(nil).WithStderr(nil).WithStdin(nil).WithoutSignalHandling()
	if code, err := p.Run([]string{"go"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if help != "" || parser == nil {
		t.Errorf("Help %q, Parser %v; want no page and a default parser", help, parser)
	}
	if stdout != any(os.Stdout) || stderr != any(os.Stderr) || stdin != any(os.Stdin) {
		t.Error("nil streams did not restore os.Stdout, os.Stderr and os.Stdin")
	}
}

// TestProgramClaims_resolverErrorCategories: a resolver's usage error is a recorded error, any
// other error a fault classified internal, and the partial chain names the command.
func TestProgramClaims_resolverErrorCategories(t *testing.T) {
	t.Parallel()
	p, _, _ := claimProgram(nil)
	var out Outcome
	var path string
	p.WithReporter(func(_ context.Context, rtx *Context, o Outcome) { out, path = o, rtx.CommandPath() })

	p.WithResolver(func(def Definition, _ []string) (Resolution, error) {
		r, _ := DefaultResolver(def, []string{"go"})
		return r, errors.New("plain")
	})
	_, err := p.Run([]string{"go"})
	if len(out.Panics) != 1 || len(out.Errors) != 0 || CategoryOf(err) != CategoryInternal || path != "app go" {
		t.Errorf("plain error: %+v, err %v (%v), path %q", out, err, CategoryOf(err), path)
	}

	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, UsageError(errors.New("bad"))
	})
	_, err = p.Run([]string{"go"})
	if len(out.Errors) != 1 || len(out.Panics) != 0 || CategoryOf(err) != CategoryUsage || path != "app" {
		t.Errorf("usage error: %+v, err %v (%v), path %q", out, err, CategoryOf(err), path)
	}
}

// TestProgramClaims_nilHandlersAreWiringErrors: every constructor accepts a nil handler set and
// fails the run with an internal *WiringError whose Command and Handler are empty.
func TestProgramClaims_nilHandlersAreWiringErrors(t *testing.T) {
	t.Parallel()
	def := Definition{Name: "app", Handler: "App"}
	for name, p := range map[string]*Program{
		"NewProgram":      NewProgram(def, nil),
		"NewProgramFunc":  NewProgramFunc(def, nil),
		"NewWiredProgram": NewWiredProgram(def, nil),
	} {
		var stderr bytes.Buffer
		code, err := p.WithStderr(&stderr).WithoutSignalHandling().Run(nil)
		we, ok := errors.AsType[*WiringError](err)
		if code != 1 || !ok || CategoryOf(err) != CategoryInternal || we.Command != "" || we.Handler != "" ||
			!strings.Contains(stderr.String(), name) {
			t.Errorf("%s: code %d, err %v (%+v), stderr %q", name, code, err, we, stderr.String())
		}
	}
}

// TestProgramClaims_categoryHelpersOnNil: the tagging helpers pass nil through and keep a
// message as is; AsCommand of a nil hook is nil.
func TestProgramClaims_categoryHelpersOnNil(t *testing.T) {
	t.Parallel()
	if UsageError(nil) != nil || InternalError(nil) != nil || CategoryOf(nil) != CategoryNone || AsCommand(0, nil) != nil {
		t.Error("a nil argument is not passed through")
	}
	if UsageError(errors.New("x")).Error() != "x" || InternalError(errors.New("y")).Error() != "y" {
		t.Error("tagging changed the message")
	}
	if (&PanicError{Value: "s"}).Unwrap()[0] != ErrInternal || CategoryOf(&PanicError{Value: UsageError(errors.New("u"))}) != CategoryUsage {
		t.Error("PanicError.Unwrap does not classify as its doc says")
	}
}

// TestProgramClaims_exitCauseOnEveryRun: an ExitCause sets the code of the run it cancels, and
// of every later run under the same canceled context, whose Run hook never starts.
func TestProgramClaims_exitCauseOnEveryRun(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	runs := 0
	p, _, _ := claimProgram(func(*Context) { runs++; cancel(ExitCause(3)) })
	p.WithContext(ctx)
	for range 2 {
		if code, err := p.Run([]string{"go"}); code != 3 || err != nil {
			t.Errorf("Run = %d, %v; want 3", code, err)
		}
	}
	if runs != 1 {
		t.Errorf("Run hook ran %d times, want once", runs)
	}
}

// TestProgramClaims_executeJoinsRecordedErrors: Execute hands the floored code to the exit
// action and returns every recorded error.
func TestProgramClaims_executeJoinsRecordedErrors(t *testing.T) {
	t.Parallel()
	e1, e2 := errors.New("e1"), errors.New("e2")
	p, _, _ := claimProgram(func(rtx *Context) { rtx.RecordError(e1); rtx.RecordError(e2) })
	code := -1
	err := p.WithArgs([]string{"go"}).WithExit(func(c int) { code = c }).Execute()
	if code != 1 || !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Errorf("Execute: code %d, err %v", code, err)
	}
}

// TestProgramClaims_reporterSkippedForAnEmptyOutcome: a run that records nothing does not call
// the reporter, and keeps a code set with HaltWithCode.
func TestProgramClaims_reporterSkippedForAnEmptyOutcome(t *testing.T) {
	t.Parallel()
	p, _, _ := claimProgram(func(rtx *Context) { rtx.HaltWithCode(4) })
	called := false
	p.WithReporter(func(context.Context, *Context, Outcome) { called = true })
	if code, err := p.Run([]string{"go"}); called || code != 4 || err != nil {
		t.Errorf("reporter called %v, code %d, err %v", called, code, err)
	}
}

// TestProgramClaims_reporterExitOverrides: inside the reporter, Exit replaces the code, the
// latest call winning.
func TestProgramClaims_reporterExitOverrides(t *testing.T) {
	t.Parallel()
	p, _, _ := claimProgram(func(rtx *Context) { rtx.HaltWithCode(4); rtx.RecordError(errors.New("x")) })
	p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) { rtx.Exit(9); rtx.Exit(11) })
	if code, _ := p.Run([]string{"go"}); code != 11 {
		t.Errorf("code %d, want 11", code)
	}
}

// TestProgramClaims_structuredReporterNilRule: a nil rule reports as the default reporter does.
func TestProgramClaims_structuredReporterNilRule(t *testing.T) {
	t.Parallel()
	p, _, errb := claimProgram(func(rtx *Context) { rtx.RecordError(errors.New("boom")) })
	p.WithReporter(StructuredReporter(nil))
	if code, _ := p.Run([]string{"go"}); code != 1 || errb.String() != "Error: boom\n" {
		t.Errorf("code %d, stderr %q", code, errb)
	}
}

// TestProgramClaims_haltWithNilHalts: HaltWith(nil) records nothing and still stops forward
// progress, and the begun teardown runs.
func TestProgramClaims_haltWithNilHalts(t *testing.T) {
	t.Parallel()
	var log []string
	p := NewProgramFunc(Definition{Name: "app", Handler: "App"}, func(string) (Handler, bool) {
		return &actHandler{name: "app", log: &log, act: act{at: "PreRun", do: func(rtx *Context) { rtx.HaltWith(nil) }}}, true
	}).WithoutSignalHandling().WithStderr(io.Discard)
	code, err := p.Run(nil)
	want := "app.CascadingPreRun,app.PreRun,app.PostRun,app.CascadingPostRun"
	if code != 0 || err != nil || strings.Join(log, ",") != want {
		t.Errorf("code %d, err %v, hooks %v", code, err, log)
	}
}

// TestProgramClaims_panickedUsageErrorKeepsItsCategory: a panic whose value is a usage error
// reaches the reporter with its stack and classifies as usage.
func TestProgramClaims_panickedUsageErrorKeepsItsCategory(t *testing.T) {
	t.Parallel()
	p, _, _ := claimProgram(func(*Context) { panic(UsageError(errors.New("u"))) })
	var out Outcome
	p.WithReporter(func(_ context.Context, _ *Context, o Outcome) { out = o })
	_, err := p.Run([]string{"go"})
	if _, ok := errors.AsType[*PanicError](err); !ok || CategoryOf(err) != CategoryUsage || len(out.Panics) != 1 || out.Panics[0].Stack == nil {
		t.Errorf("err %v, outcome %+v", err, out)
	}
}

// TestProgramClaims_nilDependencyIsRegistered: WithDependency registers a nil value too.
func TestProgramClaims_nilDependencyIsRegistered(t *testing.T) {
	t.Parallel()
	dep := NewDependency[*int]("p")
	var ok bool
	p, _, _ := claimProgram(func(rtx *Context) { _, ok = rtx.GetDependency(dep) })
	if _, err := p.WithDependency(dep, nil).Run([]string{"go"}); err != nil || !ok {
		t.Errorf("registered %v, err %v", ok, err)
	}
}

// TestProgramClaims_completeAnswersAndFails: no words completes a new first word, and a format
// that fails to write gives exit code 1 and its error.
func TestProgramClaims_completeAnswersAndFails(t *testing.T) {
	t.Parallel()
	p, out, _ := claimProgram(nil)
	if code, err := p.Complete(nil, PluginCompletion); code != 0 || err != nil || out.String() != "go\n:0\n" {
		t.Errorf("Complete = %d, %v, %q", code, err, out)
	}
	failed := errors.New("x")
	if code, err := p.Complete(nil, func(io.Writer, CompletionResult) error { return failed }); code != 1 || !errors.Is(err, failed) {
		t.Errorf("failing format: %d, %v", code, err)
	}
}

// TestProgramClaims_bufferedFlushFailureFails: a buffered stdout whose final flush fails records
// the failure, and the default reporter exits 1.
func TestProgramClaims_bufferedFlushFailureFails(t *testing.T) {
	t.Parallel()
	p, _, errb := claimProgram(func(rtx *Context) { fmt.Fprint(rtx.Stdout, "hi") })
	p.WithStdout(failingWriter{errors.New("broken pipe")}).WithBufferedOutput(true)
	if code, err := p.Run([]string{"go"}); code != 1 || err == nil || errb.String() != "Error: write output: broken pipe\n" {
		t.Errorf("code %d, err %v, stderr %q", code, err, errb)
	}
}

// TestProgramClaims_concurrentRuns runs one configured program from many goroutines, with and
// without rotini's signal trap, through Run, RunContext and Complete, under the race detector.
func TestProgramClaims_concurrentRuns(t *testing.T) {
	t.Parallel()
	dep := NewDependency[int]("n")
	run := func(rtx *Context) {
		v := rtx.MustGetDependency(dep)
		rtx.SetDependency(dep, v+1)
		rtx.RecordWarning(errors.New("w"))
		rtx.RecordInfo("i")
		_, _, _, _ = rtx.CommandPath(), rtx.CommandChain(), rtx.Environ(), rtx.Now()
	}
	for _, trap := range []bool{false, true} {
		p, _, _ := claimProgram(run)
		p.WithStderr(&syncWriter{w: io.Discard}).WithStdout(&syncWriter{w: io.Discard}).WithDependency(dep, 1).
			WithEnviron([]string{"A=1"}).WithClock(time.Now).WithBufferedOutput(true).
			WithReporter(StructuredReporter(func(*Context) bool { return true }))
		if trap {
			p.WithSignals()
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if code, _ := p.Run([]string{"go"}); code != 0 {
					t.Errorf("Run = %d", code)
				}
				if code, _ := p.RunContext(t.Context(), []string{"go"}); code != 0 {
					t.Errorf("RunContext = %d", code)
				}
				if code, _ := p.Complete([]string{""}, PluginCompletion); code != 0 {
					t.Errorf("Complete = %d", code)
				}
			})
		}
		wg.Wait()
	}
}
