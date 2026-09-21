package rotini

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// [Service]: the daemon shape — long-lived workers supervised until the context ends
// or one fails, with shutdown hooks that run in every case. [Scheduler] is built on it.

// ErrShutdownTimeout reports that a service did not tear itself down within the shutdown
// budget — either its workers did not stop, or its shutdown hooks did not finish. The budget
// covers both halves (see [Service.WithShutdownTimeout]) and so does this error, because the
// question a caller is asking is the same one in both cases: did teardown complete, or is this
// process exiting with work possibly unflushed? A supervisor acts on that, not on which half
// ran long.
//
// The service returns rather than hanging, so a supervisor's own kill timer is never the thing
// that ends the process.
var ErrShutdownTimeout = InternalError(errors.New("rotini: shutdown timed out"))

// Service runs a set of long-lived workers until the context ends or one of them fails, then
// shuts them down in order. It is the daemon shape: the runtime's signal trap already cancels
// the run context, so a handler that builds a Service on its own ctx gets graceful shutdown
// for free.
//
//	svc := rotini.NewService().
//	    Go("http", serveHTTP).
//	    Go("reconciler", reconcile).
//	    WithShutdown(closeDB)
//	if err := svc.Run(ctx); err != nil { rtx.RecordError(err) }
//
// Workers are plain funcs returning an error, so a failure travels back the normal Go way.
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
// failure message. Nothing starts until Run.
func (s *Service) Go(name string, fn func(context.Context) error) *Service {
	if fn != nil {
		s.workers = append(s.workers, serviceWorker{name: name, fn: fn})
	}
	return s
}

// WithShutdown registers a teardown func run after the workers stop. Hooks run in
// REVERSE registration order, like deferred calls, so a resource is released
// before whatever it depends on.
func (s *Service) WithShutdown(fn func(context.Context) error) *Service {
	if fn != nil {
		s.shutdown = append(s.shutdown, fn)
	}
	return s
}

// WithShutdownTimeout bounds how long Run waits for workers to stop and for the
// shutdown hooks to finish (default 10s). Zero or less means wait forever, which
// only suits a program with its own outer deadline.
func (s *Service) WithShutdownTimeout(d time.Duration) *Service {
	s.timeout = d
	return s
}

// Run starts every worker and blocks until the context is done, a worker fails, or all workers
// have returned.
//
// The first worker error wins: it cancels the others and is what Run returns, wrapped with the
// worker's name. A worker returning nil has simply finished. Shutdown hooks run in every case,
// so cleanup is not conditional on success.
//
// A context canceled from outside is a graceful stop rather than a failure, so Run returns nil
// for it; only a worker's own error or [ErrShutdownTimeout] is an error.
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
	// The budget applies to the hooks as well as to the workers, so overrunning it here is
	// the condition ErrShutdownTimeout names. Without this it was reported only for the
	// worker half: a hook that ran long returned whatever it happened to return — usually
	// ctx.Err(), which is plain context.DeadlineExceeded — so a caller could not tell a
	// dirty teardown from a bounded run reaching its own deadline normally, and the one
	// question the sentinel exists to answer had a typed answer for half the budget.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if first != nil {
			first = fmt.Errorf("%w: %w", ErrShutdownTimeout, first)
		} else {
			first = ErrShutdownTimeout
		}
	}
	// A worker's own failure still wins: it is the cause, and a teardown cut short is
	// usually its consequence.
	if prior != nil {
		return prior
	}
	return first
}
