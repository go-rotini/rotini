package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func sameFunc(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }

// TestReporter_defaultIsDefaultReporter: a program without WithReporter, and one given nil,
// reports with DefaultReporter; a set reporter is returned as set.
func TestReporter_defaultIsDefaultReporter(t *testing.T) {
	t.Parallel()
	if r := NewProgram(testDef(), nil).Reporter(); !sameFunc(r, DefaultReporter) {
		t.Error("a new program's Reporter is not DefaultReporter")
	}
	custom := Reporter(func(context.Context, *Context, Outcome) {})
	p := NewProgram(testDef(), nil).WithReporter(custom)
	if !sameFunc(p.Reporter(), custom) {
		t.Error("Reporter did not return the reporter WithReporter set")
	}
	if !sameFunc(p.WithReporter(nil).Reporter(), DefaultReporter) {
		t.Error("WithReporter(nil) did not restore DefaultReporter")
	}
}

// TestReporter_composesTheDefault: a reporter that calls DefaultReporter and adds a line keeps
// the default report and its exit floor.
func TestReporter_composesTheDefault(t *testing.T) {
	var stdout, stderr strings.Builder
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) {
		rtx.RecordWarning(errors.New("careful"))
		rtx.RecordError(errors.New("bad"))
	}}).WithStdout(&stdout).WithStderr(&stderr).
		WithReporter(func(ctx context.Context, rtx *Context, out Outcome) {
			DefaultReporter(ctx, rtx, out)
			if out.Failed() {
				fmt.Fprintln(rtx.Stderr, "Run 'app --help' for usage.")
			}
		})
	code, err := p.Run([]string{"run", "x"})
	if code != 1 || err == nil {
		t.Errorf("Run = (%d, %v), want the default floor of 1 and the error", code, err)
	}
	if want := "Warning: careful\nError: bad\nRun 'app --help' for usage.\n"; stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("stdout %q, stderr %q; want stderr %q", stdout.String(), stderr.String(), want)
	}
}

// TestReporter_wrapsTheProgramsReporter: wrapping Reporter() keeps a custom reporter's output.
func TestReporter_wrapsTheProgramsReporter(t *testing.T) {
	var stderr strings.Builder
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) { rtx.RecordInfo("hi") }}).
		WithStdout(io.Discard).WithStderr(&stderr).
		WithReporter(func(_ context.Context, rtx *Context, _ Outcome) { fmt.Fprintln(rtx.Stderr, "custom") })
	report := p.Reporter()
	var seen []string
	p.WithReporter(func(ctx context.Context, rtx *Context, out Outcome) {
		seen = out.Infos
		report(ctx, rtx, out)
	})
	if code, _ := p.Run([]string{"run", "x"}); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if stderr.String() != "custom\n" || len(seen) != 1 || seen[0] != "hi" {
		t.Errorf("stderr %q, seen %q; want the custom report and the info", stderr.String(), seen)
	}
}

// TestOutcome_exitCode: Outcome.ExitCode is the code the lifecycle set before the reporter,
// 0 when none; the reporter still decides the final code.
func TestOutcome_exitCode(t *testing.T) {
	for name, c := range map[string]struct {
		do   func(*Context)
		want int
	}{
		"no code":        {func(rtx *Context) { rtx.RecordError(errors.New("bad")) }, 0},
		"HaltWithCode":   {func(rtx *Context) { rtx.RecordError(errors.New("bad")); rtx.HaltWithCode(3) }, 3},
		"Exit":           {func(rtx *Context) { rtx.RecordWarning(errors.New("w")); rtx.Exit(4) }, 4},
		"first one wins": {func(rtx *Context) { rtx.RecordInfo("i"); rtx.HaltWithCode(5); rtx.Exit(6) }, 5},
	} {
		t.Run(name, func(t *testing.T) {
			var got Outcome
			p := NewProgram(testDef(), ocProgram{do: c.do}).WithStdout(io.Discard).WithStderr(io.Discard).
				WithReporter(func(_ context.Context, rtx *Context, out Outcome) {
					got = out
					rtx.Exit(9)
				})
			code, _ := p.Run([]string{"run", "x"})
			if got.ExitCode != c.want || got.Signal != nil {
				t.Errorf("ExitCode %d, Signal %v; want %d and nil", got.ExitCode, got.Signal, c.want)
			}
			if code != 9 {
				t.Errorf("exit = %d, want the reporter's 9", code)
			}
		})
	}
}

// TestOutcome_exitCodeFromAnExitCause: a caller's ExitCause shows in Outcome.ExitCode and is
// no signal.
func TestOutcome_exitCodeFromAnExitCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	var got Outcome
	p := NewProgram(testDef(), ocProgram{do: func(rtx *Context) {
		rtx.RecordInfo("i")
		cancel(ExitCause(7))
	}}).WithStdout(io.Discard).WithStderr(io.Discard).WithContext(ctx).
		WithReporter(func(_ context.Context, _ *Context, out Outcome) { got = out })
	code, _ := p.Run([]string{"run", "x"})
	if code != 7 || got.ExitCode != 7 || got.Signal != nil {
		t.Errorf("exit %d, ExitCode %d, Signal %v; want 7, 7, nil", code, got.ExitCode, got.Signal)
	}
}

// TestSignalCaused: only an error the trap's own cancellation caused counts.
func TestSignalCaused(t *testing.T) {
	t.Parallel()
	sig := signalCtx(130)
	plain, cancel := context.WithCancel(context.Background())
	cancel()
	caller, cancelCause := context.WithCancelCause(context.Background())
	cancelCause(ExitCause(130))
	for name, c := range map[string]struct {
		ctx  context.Context
		err  error
		want bool
	}{
		"the trap's cause":           {sig, context.Cause(sig), true},
		"a wrapped cause":            {sig, fmt.Errorf("read: %w", context.Cause(sig)), true},
		"context.Canceled":           {sig, fmt.Errorf("fetch: %w", context.Canceled), true},
		"the context's error":        {sig, sig.Err(), true},
		"an unrelated error":         {sig, errors.New("disk full"), false},
		"a nil error":                {sig, nil, false},
		"a plain cancel":             {plain, context.Canceled, false},
		"a caller's ExitCause":       {caller, context.Cause(caller), false},
		"a live context":             {context.Background(), context.Canceled, false},
		"a nil context":              {nil, context.Canceled, false},
		"DeadlineExceeded after it":  {sig, context.DeadlineExceeded, false},
		"a trap cause as a value":    {sig, ExitCause(130), true}, // Is matches on the code
		"another code's exit cause":  {sig, ExitCause(3), false},
		"canceled joined with other": {sig, errors.Join(errors.New("x"), context.Canceled), true},
	} {
		if got := SignalCaused(c.ctx, c.err); got != c.want {
			t.Errorf("%s: SignalCaused = %v, want %v", name, got, c.want)
		}
	}
	if s := runSignal(sig); s == nil {
		t.Error("runSignal of the trap's context is nil")
	}
}
