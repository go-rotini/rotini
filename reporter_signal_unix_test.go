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
