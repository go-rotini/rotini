//go:build unix

package rotini

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// swapTrapSignals replaces the default trapped signals with SIGUSR1, which does not disturb
// the test runner, and returns a restore func. These tests must not run in parallel.
func swapTrapSignals(sigs ...os.Signal) func() {
	prev := trapSignals
	trapSignals = sigs
	return func() { trapSignals = prev }
}

// sigRec records every lifecycle hook and runs fire in Run to raise a signal mid-lifecycle.
type sigRec struct {
	log  *[]string
	fire func(ctx context.Context)
}

func (h sigRec) CascadingPreRun(context.Context, *Context) {
	*h.log = append(*h.log, "CascadingPreRun")
}
func (h sigRec) PreRun(context.Context, *Context)  { *h.log = append(*h.log, "PreRun") }
func (h sigRec) PostRun(context.Context, *Context) { *h.log = append(*h.log, "PostRun") }
func (h sigRec) CascadingPostRun(context.Context, *Context) {
	*h.log = append(*h.log, "CascadingPostRun")
}
func (h sigRec) Run(ctx context.Context, _ *Context) {
	*h.log = append(*h.log, "Run")
	if h.fire != nil {
		h.fire(ctx)
	}
}

type sigAgg struct{ h sigRec }

func (a sigAgg) Main() Handler { return a.h }

func newSigProgram(h sigRec) *Program {
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, sigAgg{h})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return p
}

// With no WithContext, the first signal cancels the run context and halts the lifecycle:
// every begun teardown hook runs and the exit code is 128+signum (SIGUSR1's, here).
func TestDefaultSignals_gracefulShutdownRunsTeardown(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var log []string
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done(): // signal-canceled — shut down gracefully
		case <-time.After(2 * time.Second):
			t.Error("run context was not canceled by the trapped signal")
		}
	}}

	code, _ := newSigProgram(h).Run(nil)

	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d (128+signum graceful signal shutdown)", code, want)
	}
	for _, hook := range []string{"Run", "PostRun", "CascadingPostRun"} {
		if !contains(log, hook) {
			t.Errorf("teardown hook %q did not run after a mid-lifecycle signal; log=%v", hook, log)
		}
	}
}

// A second signal calls the exit action immediately, skipping remaining work, so a handler
// that ignores cancellation can still be interrupted.
func TestDefaultSignals_secondSignalForcesExit(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()

	var forced atomic.Int32 // the code the force path passes to p.exit (0 = not yet forced)
	log := []string{}
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done(): // first signal handled (ctx canceled)
		case <-time.After(2 * time.Second):
			t.Fatal("first signal did not cancel the context")
		}
		// Second signal → force exit. Resend until the force path fires, to absorb any
		// signal-delivery latency without depending on exact timing.
		deadline := time.After(2 * time.Second)
		for forced.Load() == 0 {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
			select {
			case <-deadline:
				t.Error("second signal did not force exit")
				return
			case <-time.After(30 * time.Millisecond):
			}
		}
	}}

	p := newSigProgram(h)
	p.exit = func(code int) { forced.Store(int32(code)) }
	p.Run(nil)

	if got := forced.Load(); got != forceExitCode {
		t.Errorf("force exit code = %d, want %d", got, forceExitCode)
	}
}

// With WithContext, rotini installs no signal trap, so a signal does not cancel the run.
func TestDefaultSignals_withContextOptsOut(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	// Ignore SIGUSR1 so the self-kill does not terminate the process (its default action).
	signal.Ignore(syscall.SIGUSR1)
	defer signal.Reset(syscall.SIGUSR1)

	delivered := make(chan struct{})
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
			t.Error("a user-supplied context must not be canceled by rotini's default trap (none should be installed)")
		case <-time.After(150 * time.Millisecond):
			// expected: no cancellation, the context is the caller's
		}
		close(delivered)
	}}

	newSigProgram(h).WithContext(context.Background()).Run(nil)
	<-delivered
}

// WithoutSignalHandling disables the trap without a supplied context, so a signal does not
// cancel the run.
func TestWithoutSignalHandling_suppressesTrap(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	// Ignore SIGUSR1 so the self-kill does not terminate the process (its default action).
	signal.Ignore(syscall.SIGUSR1)
	defer signal.Reset(syscall.SIGUSR1)

	delivered := make(chan struct{})
	h := sigRec{log: &[]string{}, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
			t.Error("WithoutSignalHandling must install no trap, yet the run context was canceled")
		case <-time.After(150 * time.Millisecond):
			// expected: no trap, so no cancellation
		}
		close(delivered)
	}}

	newSigProgram(h).WithoutSignalHandling().Run(nil)
	<-delivered
}

// WithSignals traps an explicit signal set with the same graceful halt and 128+signum exit.
func TestWithSignals_trapsCustomSet(t *testing.T) {
	var log []string
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("WithSignals(SIGUSR1) did not trap the signal")
		}
	}}

	code, _ := newSigProgram(h).WithSignals(syscall.SIGUSR1).Run(nil)

	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d (128+signum)", code, want)
	}
	for _, hook := range []string{"Run", "PostRun", "CascadingPostRun"} {
		if !contains(log, hook) {
			t.Errorf("teardown hook %q did not run; log=%v", hook, log)
		}
	}
}

// WithSignals with WithContext: the signal cancels a derived child of the caller's context,
// the lifecycle halts, and teardown runs.
func TestWithSignals_withContext_derivesChildAndTraps(t *testing.T) {
	var log []string
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("WithSignals atop WithContext did not trap (no child cancel drove the halt)")
		}
	}}

	code, _ := newSigProgram(h).WithContext(context.Background()).WithSignals(syscall.SIGUSR1).Run(nil)

	if want := 128 + int(syscall.SIGUSR1); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
	if !contains(log, "CascadingPostRun") {
		t.Errorf("teardown did not run after a signal under WithContext+WithSignals; log=%v", log)
	}
}

// With WithSignals and WithContext, canceling the caller's context still halts the run
// through the derived child. No signal is raised.
func TestWithSignals_withContext_parentCancelStillHalts(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	var log []string
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		cancel() // cancel the parent; must reach rotini's derived child
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("canceling the parent context did not propagate to rotini's derived child")
		}
	}}

	newSigProgram(h).WithContext(parent).WithSignals(syscall.SIGUSR1).Run(nil)

	if !contains(log, "CascadingPostRun") {
		t.Errorf("teardown did not run after parent cancel; log=%v", log)
	}
}

// signalExitCode maps any signal to 128+signum (SIGINT→130, SIGTERM→143, SIGHUP→129).
func TestSignalExitCode_mapping(t *testing.T) {
	cases := []struct {
		sig  syscall.Signal
		want int
	}{
		{syscall.SIGINT, 130},
		{syscall.SIGTERM, 143},
		{syscall.SIGHUP, 129},
	}
	for _, tc := range cases {
		if got := signalExitCode(tc.sig); got != tc.want {
			t.Errorf("signalExitCode(%v) = %d, want %d", tc.sig, got, tc.want)
		}
	}
}
