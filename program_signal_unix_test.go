//go:build unix

package rotini

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"time"
)

// On a Unix host, WithSignals must cancel the run context when the configured signal
// arrives, so a long-running handler that selects on ctx.Done() can shut down
// gracefully. SIGUSR1 is used (not SIGINT/SIGTERM) so the test never disturbs the
// test runner; signal.NotifyContext catches it, so it does not terminate the process.
func TestProgram_WithSignals_cancelsContextOnSignal(t *testing.T) {
	done := make(chan struct{})
	h := ctxRec{run: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done():
			close(done)
		case <-time.After(2 * time.Second):
		}
	}}
	newCtxProgram(h).WithSignals(syscall.SIGUSR1).run(nil)

	select {
	case <-done:
		// the signal canceled the run context — graceful-shutdown wiring works
	default:
		t.Error("WithSignals did not cancel the run context when the configured signal arrived")
	}
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

// A signal that cancels the run context mid-lifecycle is a graceful stop, not a hard
// abort: the handler observes the cancellation and returns, and the runtime still runs
// every begun teardown hook. (Contrast ExitNow, which skips teardown.)
func TestProgram_WithSignals_teardownRunsAfterSignal(t *testing.T) {
	var log []string
	h := sigRec{log: &log, fire: func(ctx context.Context) {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
		select {
		case <-ctx.Done(): // signal-canceled; shut down gracefully
		case <-time.After(2 * time.Second):
		}
	}}
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, sigAgg{h})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	p.WithSignals(syscall.SIGUSR1).run(nil)

	for _, hook := range []string{"Run", "PostRun", "CascadingPostRun"} {
		if !contains(log, hook) {
			t.Errorf("teardown hook %q did not run after a mid-lifecycle signal; log=%v", hook, log)
		}
	}
}
