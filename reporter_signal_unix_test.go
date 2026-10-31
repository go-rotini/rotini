//go:build unix

package rotini

import (
	"bytes"
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

// haltOnSignal raises SIGUSR1, waits for the trap to cancel the run, then fails the hook with
// the cancellation, as a handler whose blocking read was interrupted does.
func haltOnSignal(t *testing.T) func(ctx context.Context, rtx *Context) {
	return func(ctx context.Context, rtx *Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("the trapped signal did not cancel the run")
		}
		rtx.HaltWith(context.Cause(ctx))
	}
}

type signalHalter struct {
	sigRec
	run func(context.Context, *Context)
}

func (h signalHalter) Run(ctx context.Context, rtx *Context) { h.run(ctx, rtx) }

type signalHalterAgg struct{ h signalHalter }

func (a signalHalterAgg) Main() Handler { return a.h }

// The default reporter prints nothing for the error a trapped signal caused; the exit is
// 128+signum and Run still returns the recorded error.
func TestDefaultSignals_defaultReporterHidesTheSignalError(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	var out, errs bytes.Buffer
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, signalHalterAgg{signalHalter{sigRec{log: &[]string{}}, haltOnSignal(t)}}).
		WithStdout(&out).WithStderr(&errs)

	code, err := p.Run(nil)
	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
	if out.Len() != 0 || errs.Len() != 0 {
		t.Errorf("stdout %q, stderr %q; want nothing printed", out.String(), errs.String())
	}
	if _, ok := errors.AsType[exitCodeError](err); !ok {
		t.Error("Run did not return the recorded error")
	}
}

// A custom reporter still receives the error.
func TestDefaultSignals_customReporterReceivesTheSignalError(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	var got []error
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, signalHalterAgg{signalHalter{sigRec{log: &[]string{}}, haltOnSignal(t)}}).
		WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).
		WithReporter(func(_ context.Context, _ *Context, out Outcome) { got = out.Errors })

	code, _ := p.Run(nil)
	if len(got) != 1 {
		t.Errorf("reporter got %v, want the signal's error", got)
	}
	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
}

// The reporter sees the trapped signal and the code it set, and CausedBySignal picks out the
// error the signal caused, so a custom reporter can drop it and print the rest.
func TestDefaultSignals_outcomeCarriesTheSignal(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	var got Outcome
	var kept []error
	h := signalHalter{sigRec{log: &[]string{}}, func(ctx context.Context, rtx *Context) {
		rtx.RecordError(errors.New("cleanup skipped"))
		haltOnSignal(t)(ctx, rtx)
	}}
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, signalHalterAgg{h}).
		WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).
		WithReporter(func(ctx context.Context, _ *Context, out Outcome) {
			got = out
			for _, err := range out.Errors {
				if !CausedBySignal(ctx, err) {
					kept = append(kept, err)
				}
			}
		})

	code, _ := p.Run(nil)
	want := 128 + int(syscall.SIGUSR1)
	if got.Signal != syscall.SIGUSR1 || got.ExitCode != want || code != want {
		t.Errorf("Signal %v, ExitCode %d, exit %d; want SIGUSR1, %d, %d", got.Signal, got.ExitCode, code, want, want)
	}
	if len(got.Errors) != 2 || len(kept) != 1 || kept[0].Error() != "cleanup skipped" {
		t.Errorf("errors %v, kept %v; want the signal's error filtered out", got.Errors, kept)
	}
}
