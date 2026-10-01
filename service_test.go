package rotini

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestService_runsWorkersUntilContextEnds(t *testing.T) {
	var started atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	worker := func(ctx context.Context) error {
		started.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}
	go func() {
		for started.Load() < 2 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	// A context canceled from outside is a graceful stop, not a failure.
	if err := NewService().Go("a", worker).Go("b", worker).Run(ctx); err != nil {
		t.Errorf("Run on a canceled context = %v, want nil", err)
	}
	if started.Load() != 2 {
		t.Errorf("started %d workers, want 2", started.Load())
	}
}

// The FIRST worker error wins, cancels the others, and is what Run returns —
// named, so a multi-worker daemon says which one broke.
func TestService_firstFailureStopsTheRest(t *testing.T) {
	boom := errors.New("boom")
	var otherStopped atomic.Bool
	err := NewService().
		Go("failing", func(context.Context) error { return boom }).
		Go("other", func(ctx context.Context) error {
			<-ctx.Done()
			otherStopped.Store(true)
			return ctx.Err()
		}).
		Run(context.Background())

	if !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want the worker's error", err)
	}
	if !strings.Contains(err.Error(), "failing") {
		t.Errorf("error %q does not name the failing worker", err)
	}
	if !otherStopped.Load() {
		t.Error("the surviving worker was not canceled by the failure")
	}
}

// A worker returning nil has finished its job; it must not disturb the others.
func TestService_cleanWorkerExitDoesNotStopOthers(t *testing.T) {
	var longRan atomic.Bool
	err := NewService().
		Go("quick", func(context.Context) error { return nil }).
		Go("long", func(ctx context.Context) error {
			time.Sleep(20 * time.Millisecond)
			longRan.Store(true)
			return nil
		}).
		Run(context.Background())
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if !longRan.Load() {
		t.Error("a worker exiting cleanly cut the others short")
	}
}

// Cleanup is not conditional on success: hooks run on a clean stop, a failure,
// and a cancellation alike — in REVERSE order, like deferred calls.
func TestService_shutdownHooksAlwaysRunInReverse(t *testing.T) {
	record := func() (*[]string, func(string) func(context.Context) error) {
		var mu sync.Mutex
		order := &[]string{}
		return order, func(name string) func(context.Context) error {
			return func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				*order = append(*order, name)
				return nil
			}
		}
	}

	for _, tc := range []struct {
		name   string
		worker func(context.Context) error
	}{
		{"clean exit", func(context.Context) error { return nil }},
		{"failure", func(context.Context) error { return errors.New("boom") }},
	} {
		order, hook := record()
		NewService().Go("w", tc.worker).
			WithShutdown(hook("first")).
			WithShutdown(hook("second")).
			Run(context.Background())
		if len(*order) != 2 || (*order)[0] != "second" || (*order)[1] != "first" {
			t.Errorf("%s: shutdown order = %v, want [second first]", tc.name, *order)
		}
	}
}

// A worker's error outranks a shutdown hook's: the cause of the failure is more
// useful than a symptom of the cleanup.
func TestService_workerErrorOutranksShutdownError(t *testing.T) {
	boom := errors.New("boom")
	err := NewService().
		Go("w", func(context.Context) error { return boom }).
		WithShutdown(func(context.Context) error { return errors.New("cleanup failed") }).
		Run(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Run = %v, want the worker error to win", err)
	}
}

func TestService_shutdownErrorSurfacesWhenWorkersSucceed(t *testing.T) {
	cleanup := errors.New("cleanup failed")
	err := NewService().
		Go("w", func(context.Context) error { return nil }).
		WithShutdown(func(context.Context) error { return cleanup }).
		Run(context.Background())
	if !errors.Is(err, cleanup) {
		t.Errorf("Run = %v, want the shutdown error", err)
	}
}

// A worker that ignores cancellation must not hang the process forever.
func TestService_shutdownTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stuck := make(chan struct{})
	defer close(stuck)

	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	err := NewService().
		WithShutdownTimeout(50*time.Millisecond).
		Go("stuck", func(context.Context) error { <-stuck; return nil }).
		Run(ctx)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Errorf("Run = %v, want ErrShutdownTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Run took %v — it waited past the budget", elapsed)
	}
}

// Cleanup runs after a Ctrl-C, so shutdown hooks get a live context even though
// the caller's was canceled.
func TestService_shutdownGetsALiveContextAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var hookCtxErr error
	NewService().
		Go("w", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }).
		WithShutdown(func(ctx context.Context) error { hookCtxErr = ctx.Err(); return nil }).
		Run(ctx)
	if hookCtxErr != nil {
		t.Errorf("shutdown hook got an already-canceled context (%v) — cleanup could not run", hookCtxErr)
	}
}

func TestService_emptyServiceStillRunsShutdown(t *testing.T) {
	var ran bool
	if err := NewService().WithShutdown(func(context.Context) error { ran = true; return nil }).
		Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("an empty service skipped its shutdown hooks")
	}
}

// TestService_shutdownHookOverrunIsATimeout covers the half of the shutdown budget that did
// not report the sentinel named after it.
//
// WithShutdownTimeout is documented to bound "how long Run waits for workers to stop AND for
// the shutdown hooks to finish", but only the worker half produced ErrShutdownTimeout. A hook
// that ran past the budget returned whatever it happened to return — typically ctx.Err(),
// which is plain context.DeadlineExceeded — so a caller running a service under its own
// deadline could not distinguish a dirty teardown from a bounded run ending normally. That
// ambiguity is what made example-daemon report a cut-off shutdown as a success.
func TestService_shutdownHookOverrunIsATimeout(t *testing.T) {
	t.Parallel()

	t.Run("a hook that overruns reports the timeout", func(t *testing.T) {
		t.Parallel()
		var ran []string
		err := NewService().
			WithShutdownTimeout(40*time.Millisecond).
			Go("w", func(ctx context.Context) error { return nil }).
			// Registered first, so it runs LAST — after the slow one has already
			// exhausted the budget. It must still run: a cut-short budget is not a
			// licence to skip teardown.
			WithShutdown(func(context.Context) error { ran = append(ran, "flush"); return nil }).
			WithShutdown(func(ctx context.Context) error {
				<-ctx.Done()
				ran = append(ran, "slow")
				return ctx.Err()
			}).
			Run(context.Background())

		if !errors.Is(err, ErrShutdownTimeout) {
			t.Errorf("err = %v, want it to match ErrShutdownTimeout", err)
		}
		// The detail survives alongside the sentinel.
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want the hook's own cause to remain reachable", err)
		}
		if len(ran) != 2 || ran[0] != "slow" || ran[1] != "flush" {
			t.Errorf("hooks ran %v, want [slow flush] — reverse order, both of them", ran)
		}
	})

	// A hook that does NOT watch its ctx — the ordinary `conns.Wait()` — used to hold Run for as
	// long as it blocked: the budget was checked only after the hook returned, so the promise
	// that "the service returns rather than hanging" held for workers and not for hooks.
	t.Run("a hook that ignores its ctx cannot hold Run past the budget", func(t *testing.T) {
		t.Parallel()
		release := make(chan struct{})
		defer close(release)
		var flushed atomic.Bool
		start := time.Now()
		err := NewService().
			WithShutdownTimeout(30*time.Millisecond).
			Go("w", func(ctx context.Context) error { return nil }).
			WithShutdown(func(context.Context) error { flushed.Store(true); return nil }).
			WithShutdown(func(context.Context) error { <-release; return nil }). // ignores ctx
			Run(context.Background())

		if took := time.Since(start); took > time.Second {
			t.Fatalf("Run took %v with a 30ms budget — the stuck hook held it", took)
		}
		if !errors.Is(err, ErrShutdownTimeout) {
			t.Errorf("err = %v, want ErrShutdownTimeout", err)
		}
		if err == nil || !strings.Contains(err.Error(), "shutdown hook 2 (of 2, in registration order) did not finish") {
			t.Errorf("err = %v, want it to name the hook that overran", err)
		}
		if !flushed.Load() {
			t.Error("the hook after the stuck one never ran — an overrun must not skip the rest of teardown")
		}
	})

	t.Run("hooks that finish in time report nothing", func(t *testing.T) {
		t.Parallel()
		err := NewService().
			WithShutdownTimeout(time.Second).
			Go("w", func(ctx context.Context) error { return nil }).
			WithShutdown(func(context.Context) error { return nil }).
			Run(context.Background())
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("a hook's own failure is not a timeout", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		err := NewService().
			WithShutdownTimeout(time.Second).
			Go("w", func(ctx context.Context) error { return nil }).
			WithShutdown(func(context.Context) error { return boom }).
			Run(context.Background())
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want it to wrap the hook's error", err)
		}
		if errors.Is(err, ErrShutdownTimeout) {
			t.Errorf("a hook failing promptly was reported as a timeout: %v", err)
		}
	})

	t.Run("a worker's failure still wins", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("disk full")
		err := NewService().
			WithShutdownTimeout(30*time.Millisecond).
			Go("w", func(ctx context.Context) error { return boom }).
			WithShutdown(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }).
			Run(context.Background())
		// The worker is the cause; a teardown cut short is usually its consequence.
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the worker's error", err)
		}
	})
}

// ── robustness: the four ways a daemon used to end badly ─────────────────────
//
// Service exists for the ENDING, so every one of these is about what happens on the way out.
// All four were found by probing after Scheduler was removed, and each had the same shape: the
// failure mode was invisible until the worst possible moment.

// TestService_workerPanicDoesNotKillTheProcess is the most serious of the four.
//
// A worker panic used to propagate off its own goroutine and terminate the process outright:
// no shutdown hooks, no flush, no funnel, no exit code. Program.WithPanicRecover cannot help —
// it guards the dispatch goroutine, and a goroutine's panic is unrecoverable from anywhere but
// itself — so the Service that spawned the goroutine has to be the one to guard it.
//
// A panicking worker is precisely when the journal most needs flushing.
func TestService_workerPanicDoesNotKillTheProcess(t *testing.T) {
	var flushed bool
	err := NewService().
		Go("boom", func(context.Context) error { panic("worker exploded") }).
		WithShutdown(func(context.Context) error { flushed = true; return nil }).
		Run(context.Background())

	if err == nil {
		t.Fatal("a panicking worker reported success")
	}
	if !flushed {
		t.Error("teardown was skipped — the panic ended the run before the flush")
	}

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *PanicError carrying the value and the stack", err)
	}
	if pe.Value != "worker exploded" {
		t.Errorf("PanicError.Value = %v, want the panicked value", pe.Value)
	}
	if len(pe.Stack) == 0 {
		t.Error("no stack captured — the one thing that makes a recovered panic debuggable")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %q, want it to name the worker that panicked", err)
	}
	// A recovered panic is a bug in the program, and the funnel has to price it as one.
	if got := CategoryOf(err); got != CategoryInternal {
		t.Errorf("CategoryOf = %v, want internal", got)
	}
}

// TestService_hookPanicStillRunsTheRestOfTeardown is worse than a worker panic, because it
// happens DURING the flush: an escaping panic took out the close after the flush and the unlock
// after the close.
//
// Teardown is the one phase where best-effort beats fail-fast.
func TestService_hookPanicStillRunsTheRestOfTeardown(t *testing.T) {
	var ran []string
	err := NewService().
		WithShutdown(func(context.Context) error { ran = append(ran, "unlock"); return nil }).
		WithShutdown(func(context.Context) error { panic("hook exploded") }).
		WithShutdown(func(context.Context) error { ran = append(ran, "flush"); return nil }).
		Run(context.Background())

	// Reverse order: flush, then the panicking hook, then unlock.
	if want := []string{"flush", "unlock"}; !slices.Equal(ran, want) {
		t.Errorf("hooks ran %v, want %v — a panicking hook cost the ones after it", ran, want)
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Errorf("err = %v, want the panic reported as a *PanicError", err)
	}
}

// TestService_aDeadlineIsAsGracefulAsACancel pins the symmetry.
//
// `<-ctx.Done(); return ctx.Err()` is the idiomatic worker body. Under a CANCELLED context that
// was graceful; under a context with a DEADLINE the identical worker failed the run, because
// only context.Canceled was filtered. Both mean "the context you gave me is over", and a
// bounded run is not a failed one.
//
// example-daemon's schedule handler carried `!errors.Is(err, context.DeadlineExceeded)` to
// paper over exactly this.
func TestService_aDeadlineIsAsGracefulAsACancel(t *testing.T) {
	idiomatic := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(10 * time.Millisecond); cancel() }()
		if err := NewService().Go("w", idiomatic).Run(ctx); err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := NewService().Go("w", idiomatic).Run(ctx); err != nil {
			t.Errorf("Run = %v, want nil — a bounded run that reached its bound succeeded", err)
		}
	})
}

// TestService_shutdownTimeoutNamesTheWorker: ErrShutdownTimeout exists to drive a decision, and
// "shutdown timed out" on its own sends an operator to read the whole binary.
func TestService_shutdownTimeoutNamesTheWorker(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()

	err := NewService().
		WithShutdownTimeout(20*time.Millisecond).
		Go("well-behaved", func(ctx context.Context) error { <-ctx.Done(); return nil }).
		Go("stuck", func(context.Context) error { <-stop; return nil }).
		Run(ctx)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("err = %v, want ErrShutdownTimeout", err)
	}
	if !strings.Contains(err.Error(), `"stuck"`) {
		t.Errorf("err = %q, want it to name the worker that hung", err)
	}
	if strings.Contains(err.Error(), "well-behaved") {
		t.Errorf("err = %q, named a worker that stopped in time", err)
	}
}

// TestService_isReusableAndConcurrent backs the doc's claim. Run keeps all its mutable state on
// the stack, which is what lets one configured Service serve several runs — and what makes the
// "configure before running" rule the only rule.
func TestService_isReusableAndConcurrent(t *testing.T) {
	var runs atomic.Int64
	svc := NewService().Go("w", func(context.Context) error { runs.Add(1); return nil })

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Run(context.Background()); err != nil {
				t.Errorf("Run = %v", err)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 8 {
		t.Errorf("worker ran %d times across 8 runs", runs.Load())
	}
}
