package rotini

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrShutdownTimeout reports that a service's workers did not stop within the
// shutdown budget. The service returns rather than hanging, so a supervisor's own
// kill timer is never the thing that ends the process.
var ErrShutdownTimeout = InternalError(errors.New("rotini: shutdown timed out"))

// Service runs a set of long-lived workers until the context ends or one of them
// fails, then shuts them down in order.
//
// It is the daemon shape: the runtime already gives a rotini program a
// SIGINT/SIGTERM trap that cancels the run context (see [Program.WithSignals]),
// so a handler that builds a Service on its own ctx gets signal-driven graceful
// shutdown for free.
//
//	svc := rotini.NewService().
//	    Go("http", serveHTTP).
//	    Go("reconciler", reconcile).
//	    WithShutdown(closeDB)
//	if err := svc.Run(ctx); err != nil { rtx.RecordError(err) }
//
// Workers are plain funcs returning an error — not registered callbacks — so a
// failure travels back the normal Go way instead of into a closure that cannot
// return it.
//
// The zero value is usable: a Service with no workers runs nothing and returns nil.
type Service struct {
	workers  []serviceWorker
	shutdown []func(context.Context) error
	timeout  time.Duration
}

type serviceWorker struct {
	name string
	fn   func(context.Context) error
}

// NewService returns an empty service with a 10-second shutdown budget.
func NewService() *Service {
	return &Service{timeout: 10 * time.Second}
}

// Go registers a worker to run under [Service.Run]. name identifies it in a
// failure message. Nothing starts until Run. It returns the receiver to chain.
func (s *Service) Go(name string, fn func(context.Context) error) *Service {
	if fn != nil {
		s.workers = append(s.workers, serviceWorker{name: name, fn: fn})
	}
	return s
}

// WithShutdown registers a teardown func run after the workers stop. Hooks run in
// REVERSE registration order, like deferred calls, so a resource is released
// before whatever it depends on. It returns the receiver to chain.
func (s *Service) WithShutdown(fn func(context.Context) error) *Service {
	if fn != nil {
		s.shutdown = append(s.shutdown, fn)
	}
	return s
}

// WithShutdownTimeout bounds how long Run waits for workers to stop and for the
// shutdown hooks to finish (default 10s). Zero or less means wait forever, which
// only suits a program with its own outer deadline. It returns the receiver to chain.
func (s *Service) WithShutdownTimeout(d time.Duration) *Service {
	s.timeout = d
	return s
}

// Run starts every worker and blocks until the context is done, a worker fails,
// or all workers have returned.
//
// The FIRST worker error wins: it cancels the others and is what Run returns,
// wrapped with the worker's name. A worker returning nil has simply finished and
// does not disturb the rest. Shutdown hooks run in every case — a clean stop, a
// failure, and a canceled context alike — so cleanup is not conditional on
// success.
//
// A context canceled from outside is a graceful stop, not a failure: Run returns
// nil for it, and only a worker's own error or [ErrShutdownTimeout] is an error.
func (s *Service) Run(ctx context.Context) error {
	if len(s.workers) == 0 {
		return s.runShutdown(ctx, nil)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		once    sync.Once
		failure error
	)
	for _, w := range s.workers {
		wg.Add(1)
		go func(w serviceWorker) {
			defer wg.Done()
			if err := w.fn(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				once.Do(func() { failure = fmt.Errorf("rotini: service worker %q: %w", w.name, err) })
				cancel() // one failure stops the rest
			}
		}(w)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done: // every worker returned on its own
	case <-ctx.Done(): // a signal or the caller stopped us
		cancel()
		if err := s.await(done); err != nil {
			return s.runShutdown(context.WithoutCancel(ctx), err)
		}
	}
	return s.runShutdown(context.WithoutCancel(ctx), failure)
}

// await waits for the workers within the shutdown budget.
func (s *Service) await(done <-chan struct{}) error {
	if s.timeout <= 0 {
		<-done
		return nil
	}
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return ErrShutdownTimeout
	}
}

// runShutdown runs the teardown hooks in reverse order under a fresh budget —
// derived from a context WITHOUT the caller's cancellation, so cleanup still gets
// to run after a Ctrl-C. It returns prior if set, else the first hook error.
func (s *Service) runShutdown(ctx context.Context, prior error) error {
	if len(s.shutdown) == 0 {
		return prior
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	var first error
	for _, hook := range slices.Backward(s.shutdown) {
		if err := hook(ctx); err != nil && first == nil {
			first = fmt.Errorf("rotini: service shutdown: %w", err)
		}
	}
	if prior != nil {
		return prior
	}
	return first
}
