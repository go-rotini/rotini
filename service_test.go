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
