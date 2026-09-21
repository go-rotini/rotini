package rotini

import (
	"context"
	"errors"
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
