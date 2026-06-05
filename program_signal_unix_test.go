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

// swapTrapSignals temporarily replaces the default trapped signal set so a test can use
// a benign signal (SIGUSR1) instead of SIGINT/SIGTERM, which would disturb the test
// runner. It returns a restore func. These tests must not run in parallel.
func swapTrapSignals(sigs ...os.Signal) func() {
	prev := trapSignals
	trapSignals = sigs
	return func() { trapSignals = prev }
}

// sigRec records every lifecycle hook and runs a fire func in Run (used to raise a
// signal mid-lifecycle).
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

func (a sigAgg) Main() CommandHandlers { return a.h }

func newSigProgram(h sigRec) *Program {
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, sigAgg{h})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return p
}

// The default signal handler (installed because no WithContext is supplied) must, on the
// first signal, cancel the run context AND halt the lifecycle like rtx.Exit: a cooperative
// handler observes the cancellation and returns, every begun teardown hook still runs, and
// the run exits with the conventional signal code (130 here, since SIGUSR1 is not SIGTERM).
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

	code := newSigProgram(h).run(nil)

	if code != 130 {
		t.Errorf("exit code = %d, want 130 (graceful signal shutdown)", code)
	}
	for _, hook := range []string{"Run", "PostRun", "CascadingPostRun"} {
		if !contains(log, hook) {
			t.Errorf("teardown hook %q did not run after a mid-lifecycle signal; log=%v", hook, log)
		}
	}
}

// A second signal forces exit immediately, via the program's exit action (so it is
// captured here instead of terminating the test), skipping any remaining work — the
// escape hatch for a handler that would otherwise ignore the first signal's cancellation.
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
	p.run(nil)

	if got := forced.Load(); got != forceExitCode {
		t.Errorf("force exit code = %d, want %d", got, forceExitCode)
	}
}

// When the caller supplies its own context (WithContext), rotini installs no signal trap
// at all — the caller owns signal handling — so a trapped signal does not cancel the run.
func TestDefaultSignals_withContextOptsOut(t *testing.T) {
	defer swapTrapSignals(syscall.SIGUSR1)()
	// Ignore SIGUSR1 at the OS level for the duration so the self-kill is a no-op rather
	// than terminating the process (default SIGUSR1 disposition is terminate).
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

	newSigProgram(h).WithContext(context.Background()).run(nil)
	<-delivered
}
