package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// sharedRunCmd is a root command whose Run calls fn.
type sharedRunCmd struct {
	NoHooksE
	fn func(ctx context.Context, rtx *Context) error
}

func (c sharedRunCmd) Run(ctx context.Context, rtx *Context) error { return c.fn(ctx, rtx) }

// sharedProgram is a one-command program whose Run calls fn, fresh handlers per run.
func sharedProgram(fn func(ctx context.Context, rtx *Context) error) (*Program, *bytes.Buffer) {
	var errs bytes.Buffer
	p := NewProgramFunc(Definition{Name: "app", Handler: "App"}, func(string) (Handler, bool) {
		return FromHandlerE(sharedRunCmd{fn: fn}), true
	}).WithStdout(io.Discard).WithStderr(&errs).WithoutSignalHandling()
	return p, &errs
}

// A shared provider builds once for the program: every run gets the same value, no run
// releases it, Close calls its cleanup once, and a request after Close builds it again.
func TestSharedProvider_builtOncePerProgram(t *testing.T) {
	dep := NewDependency[*provClient]("shared.client")
	var calls atomic.Int32
	var closed []string
	var seen []*provClient
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		c, err := rtx.Dependency(dep)
		seen = append(seen, c)
		return err
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
		calls.Add(1)
		c := &provClient{name: "client", closeLog: &closed}
		return c, c.Close, nil
	})
	for range 3 {
		if code, err := p.Run(nil); code != 0 || err != nil {
			t.Fatalf("Run = %d, %v", code, err)
		}
	}
	if calls.Load() != 1 || len(seen) != 3 || seen[0] != seen[1] || seen[1] != seen[2] || len(closed) != 0 {
		t.Fatalf("provider ran %d times, cleaned up %q; want one value for three runs, still open", calls.Load(), closed)
	}
	if err := p.Close(); err != nil || !slices.Equal(closed, []string{"client"}) {
		t.Fatalf("Close = %v, cleaned up %q; want the client closed once", err, closed)
	}
	if err := p.Close(); err != nil || len(closed) != 1 {
		t.Fatalf("second Close = %v, cleaned up %q; want nothing more", err, closed)
	}
	if code, err := p.Run(nil); code != 0 || err != nil {
		t.Fatalf("Run after Close = %d, %v", code, err)
	}
	if calls.Load() != 2 || seen[3] == seen[0] {
		t.Errorf("provider ran %d times; want a fresh value after Close", calls.Load())
	}
}

// Concurrent runs, each asking from several goroutines, share one provider call.
func TestSharedProvider_concurrentRunsShareOneCall(t *testing.T) {
	dep := NewDependency[*int]("shared.n")
	var calls atomic.Int32
	release := make(chan struct{})
	var mu sync.Mutex
	var seen []*int
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				v := rtx.MustDependency(dep)
				mu.Lock()
				seen = append(seen, v)
				mu.Unlock()
			})
		}
		wg.Wait()
		return nil
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (*int, func() error, error) {
		calls.Add(1)
		<-release // hold the call open so the other runs wait on it
		return new(int), nil, nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if code, err := p.RunContext(context.Background(), nil); code != 0 || err != nil {
				t.Errorf("RunContext = %d, %v", code, err)
			}
		})
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 || len(seen) != 32 {
		t.Fatalf("provider ran %d times, %d requests answered; want one call for 32", calls.Load(), len(seen))
	}
	for _, v := range seen {
		if v != seen[0] {
			t.Fatal("concurrent runs got different values")
		}
	}
}

// A failed build is the run's error for the rest of that run and is retried by the next run;
// a panic is too.
func TestSharedProvider_failureRetriedNextRun(t *testing.T) {
	errDial := errors.New("dial failed")
	dep := NewDependency[string]("shared.retry")
	var calls atomic.Int32
	var results []error
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		_, err1 := rtx.Dependency(dep)
		_, err2 := rtx.Dependency(dep)
		if err1 != err2 { // the same error value, cached for the run
			t.Errorf("one run saw two results: %v, %v", err1, err2)
		}
		results = append(results, err1)
		return nil
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (string, func() error, error) {
		switch calls.Add(1) {
		case 1:
			return "", nil, UsageError(errDial)
		case 2:
			panic("boom")
		}
		return "ok", nil, nil
	})
	if code, err := p.Run(nil); code != 0 || err != nil {
		t.Fatalf("first Run = %d, %v", code, err)
	}
	if !errors.Is(results[0], errDial) || CategoryOf(results[0]) != CategoryUsage {
		t.Errorf("first run's error = %v; want the provider's usage error as is", results[0])
	}
	code, err := p.Run(nil)
	var pe *PanicError
	if code != 1 || !errors.As(err, &pe) || pe.Value != "boom" {
		t.Fatalf("second Run = %d, %v; want the provider's panic as a fault", code, err)
	}
	if code, err := p.Run(nil); code != 0 || err != nil || calls.Load() != 3 || results[len(results)-1] != nil {
		t.Errorf("third Run = %d, %v after %d calls, last result %v; want a successful retry", code, err, calls.Load(), results[len(results)-1])
	}
	if code, err := p.Run(nil); code != 0 || err != nil || calls.Load() != 3 {
		t.Errorf("fourth Run = %d, %v after %d calls; want the built value reused", code, err, calls.Load())
	}
}

// cancelKey carries a run's cancel func to its handler.
type cancelKey struct{}

// A run waiting on another run's build stops waiting when its own context ends.
func TestSharedProvider_waiterCanceled(t *testing.T) {
	dep := NewDependency[int]("shared.slow")
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var errs []error
	p, _ := sharedProgram(func(ctx context.Context, rtx *Context) error {
		if cancel, ok := ctx.Value(cancelKey{}).(context.CancelFunc); ok {
			cancel() // the waiting run's context ends while the build is in flight
		}
		_, err := rtx.Dependency(dep)
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
		return nil
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (int, func() error, error) {
		close(started)
		<-release
		return 7, nil, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run(nil)
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = p.RunContext(context.WithValue(ctx, cancelKey{}, cancel), nil)
	close(release)
	<-done
	if len(errs) != 2 || !errors.Is(errs[0], context.Canceled) || errs[1] != nil {
		t.Errorf("errors = %v; want the canceled waiter's context error, then the builder's success", errs)
	}
}

// A value set with WithDependency wins, and the shared provider never runs.
func TestSharedProvider_setValueWins(t *testing.T) {
	dep := NewDependency[string]("shared.set")
	var got string
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		got = rtx.MustDependency(dep)
		return nil
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (string, func() error, error) {
		t.Error("the shared provider ran despite a set value")
		return "built", nil, nil
	}).WithDependency(dep, "double")
	if code, err := p.Run(nil); code != 0 || err != nil || got != "double" {
		t.Errorf("Run = %d, %v, got %q; want the double", code, err, got)
	}
}

// Close releases newest first and joins every cleanup error and panic; a later registration
// replaces a shared provider, and a nil one removes it, while what was built stays for Close.
func TestSharedProvider_closeOrderErrorsAndReplacement(t *testing.T) {
	errFlush := errors.New("flush failed")
	a, b, c := NewDependency[int]("a"), NewDependency[int]("b"), NewDependency[int]("c")
	var log []string
	p, _ := sharedProgram(func(_ context.Context, rtx *Context) error {
		rtx.MustDependency(a)
		rtx.MustDependency(b)
		rtx.MustDependency(c)
		return nil
	})
	p.WithSharedProvider(a, func(context.Context, *Context) (int, func() error, error) {
		return 1, func() error { log = append(log, "a"); return errFlush }, nil
	})
	p.WithSharedProvider(b, func(context.Context, *Context) (int, func() error, error) {
		return 2, func() error { log = append(log, "b"); panic("cleanup boom") }, nil
	})
	p.WithSharedProvider(c, func(context.Context, *Context) (int, func() error, error) {
		return 3, func() error { log = append(log, "c"); return nil }, nil
	})
	if code, err := p.Run(nil); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	var perRun atomic.Int32
	p.WithProvider(a, func(context.Context, *Context) (int, func() error, error) { perRun.Add(1); return 10, nil, nil })
	p.WithSharedProvider(c, nil)
	_, err := p.Run(nil)
	var pe *PanicError
	if !errors.As(err, &pe) || perRun.Load() != 1 {
		t.Fatalf("Run after replacing = %v, per-run builds %d; want c missing (a fault) and a built per run", err, perRun.Load())
	}
	err = p.Close()
	if !slices.Equal(log, []string{"c", "b", "a"}) {
		t.Errorf("cleanup order = %q, want newest first", log)
	}
	if !errors.Is(err, errFlush) || !strings.Contains(err.Error(), `closing the shared dependency "a": flush failed`) ||
		!errors.As(err, &pe) || pe.Value != "cleanup boom" {
		t.Errorf("Close = %v; want a's error and b's panic joined", err)
	}
}

// Execute releases shared values after the hooks and before the reporter, which reports a
// cleanup error; a Run never does.
func TestSharedProvider_executeCloses(t *testing.T) {
	errFlush := errors.New("flush failed")
	dep := NewDependency[int]("shared.exec")
	var log []string
	p, errs := sharedProgram(func(_ context.Context, rtx *Context) error {
		rtx.MustDependency(dep)
		return nil
	})
	p.WithSharedProvider(dep, func(context.Context, *Context) (int, func() error, error) {
		return 1, func() error { log = append(log, "closed"); return errFlush }, nil
	})
	var reported Outcome
	report := p.Reporter()
	p.WithReporter(func(ctx context.Context, rtx *Context, out Outcome) {
		log = append(log, "reported")
		reported = out
		report(ctx, rtx, out)
	})
	if code, err := p.Run(nil); code != 0 || err != nil || len(log) != 0 {
		t.Fatalf("Run = %d, %v, log %q; want nothing released", code, err, log)
	}
	code := -1
	err := p.WithArgs(nil).WithExit(func(c int) { code = c }).Execute()
	if code != 1 || !errors.Is(err, errFlush) || !slices.Equal(log, []string{"closed", "reported"}) {
		t.Fatalf("Execute = %d, %v, log %q; want the cleanup's error reported, after the release", code, err, log)
	}
	if len(reported.Errors) != 1 || !strings.Contains(errs.String(), `Error: closing the shared dependency "shared.exec": flush failed`) {
		t.Errorf("reported %v, stderr %q", reported.Errors, errs.String())
	}
}
