package rotini

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// [Service]: the daemon shape — long-lived workers supervised until the context ends
// or one fails, with shutdown hooks that run in every case — the part of a daemon that is not
// about doing the work, but about ending.

// ErrShutdownTimeout reports that a service did not tear itself down within the shutdown
// budget — either its workers did not stop, or its shutdown hooks did not finish. The budget
// covers both halves (see [Service.WithShutdownTimeout]) and so does this error, because the
// question a caller is asking is the same one in both cases: did teardown complete, or is this
// process exiting with work possibly unflushed? A supervisor acts on that, not on which half
// ran long.
//
// The service returns rather than hanging, so a supervisor's own kill timer is never the thing
// that ends the process.
//
// When workers are what overran, the error NAMES THEM — "shutdown timed out: worker
// \"indexer\" did not stop". An operator reading a log at 3am needs to know which worker to go
// and fix, and "shutdown timed out" on its own sends them to read the whole binary.
var ErrShutdownTimeout = InternalError(errors.New("shutdown timed out"))

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
// Workers are plain funcs returning an error, so a failure travels back the normal Go way. A
// worker that PANICS does not take the process with it: the panic is recovered on its own
// goroutine, becomes the service's failure as a [*PanicError], and teardown still runs. That is
// not a nicety — a panicking worker is exactly when the journal most needs flushing, and a
// process that dies on a goroutine rotini spawned would skip every hook, every outcome and every
// exit code on the way out. [Program.WithPanicRecover] cannot help here: it guards the dispatch
// goroutine, and a goroutine's panic is unrecoverable from anywhere but itself.
//
// Configure before running. [Service.Go], [Service.WithShutdown] and
// [Service.WithShutdownTimeout] are not synchronized, so calling one while [Service.Run] is in
// flight is a data race. A configured Service may be run more than once, and concurrently: Run
// keeps all of its mutable state on the stack.
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
// A context ENDED from outside is a graceful stop rather than a failure, so Run returns nil for
// it; only a worker's own error or [ErrShutdownTimeout] is an error. "Ended" covers both
// cancellation and a deadline, and the symmetry is deliberate: a worker that writes the
// idiomatic `<-ctx.Done(); return ctx.Err()` must not fail a bounded run merely because the
// bound was a timeout rather than a cancel. Both mean "the context you gave me is over".
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
	var running sync.Map // worker name -> struct{}, for naming whoever overruns the budget
	for _, w := range s.workers {
		wg.Add(1)
		running.Store(w.name, struct{}{})
		go func(w serviceWorker) {
			defer wg.Done()
			defer running.Delete(w.name)
			if err := runWorker(runCtx, w); err != nil {
				once.Do(func() { failure = fmt.Errorf("worker %q: %w", w.name, err) })
				cancel() // one failure stops the rest
			}
		}(w)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done: // every worker returned on its own
	case <-ctx.Done(): // a signal, a deadline, or the caller stopped us
		cancel()
		if err := s.await(done, &running); err != nil {
			return s.runShutdown(context.WithoutCancel(ctx), err)
		}
	}
	return s.runShutdown(context.WithoutCancel(ctx), failure)
}

// runWorker calls one worker, containing a panic and normalizing the errors that mean "the
// context is over" to nil.
//
// Recovering here rather than around Run is the only place it CAN be done: a panic unwinds its
// own goroutine and nothing else can catch it, so a Service that spawned the goroutine has to be
// the one to guard it. The recovered value becomes a [*PanicError], which carries the stack and
// classifies as [CategoryInternal], so the funnel prices it as the bug it is.
func runWorker(ctx context.Context, w serviceWorker) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 8192)
			stack = stack[:runtime.Stack(stack, false)]
			err = &PanicError{Value: r, Stack: stack}
		}
	}()
	if err := w.fn(ctx); err != nil && !contextEnded(err) {
		return err
	}
	return nil
}

// contextEnded reports whether err is just the context saying it is over. Cancellation and a
// deadline are the same answer to a worker: stop. Treating only the first as graceful made a
// bounded run fail for writing `return ctx.Err()`, which is the idiomatic thing to write.
func contextEnded(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// await waits for the workers within the shutdown budget, naming whoever is still running when
// it runs out.
func (s *Service) await(done <-chan struct{}, running *sync.Map) error {
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
		return fmt.Errorf("%w: %s", ErrShutdownTimeout, stuckWorkers(running))
	}
}

// runHook calls one shutdown hook, containing a panic the same way [runWorker] does.
//
// A panicking hook is strictly worse than a panicking worker: it happens DURING the flush, so
// letting it escape would kill the process with the remaining hooks unrun — the close after the
// flush, the unlock after the close. Recovering turns it into this hook's error and lets the
// rest of teardown finish.
func runHook(ctx context.Context, hook func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 8192)
			stack = stack[:runtime.Stack(stack, false)]
			err = &PanicError{Value: r, Stack: stack}
		}
	}()
	return hook(ctx)
}

// stuckWorkers renders the workers that have not returned, sorted so the message is stable.
func stuckWorkers(running *sync.Map) string {
	var names []string
	running.Range(func(k, _ any) bool {
		if name, ok := k.(string); ok {
			names = append(names, fmt.Sprintf("%q", name))
		}
		return true
	})
	if len(names) == 0 {
		return "the workers did not stop"
	}
	sort.Strings(names)
	if len(names) == 1 {
		return "worker " + names[0] + " did not stop"
	}
	return "workers " + strings.Join(names, ", ") + " did not stop"
}

// runShutdown runs the teardown hooks in reverse order under a fresh budget —
// derived from a context WITHOUT the caller's cancellation, so cleanup still gets
// to run after a Ctrl-C. It returns prior if set, else the first hook error.
//
// Every hook runs, whatever the ones before it did. A hook that fails or panics must not cost
// the ones after it: they are the flush, the unlock and the close, and teardown is the one phase
// where best-effort beats fail-fast.
//
// The budget is still a promise to the CALLER: Run returns within it. A hook that does not
// watch its ctx — waiting on a WaitGroup, a lock, a connection draining — used to hold Run for
// as long as it blocked, since the budget was only checked once the hook returned. Each hook now
// runs on its own goroutine and is waited for only until the budget runs out (and, past it, a
// short grace for a hook to return the ctx error it was just handed). One that is still running
// then is abandoned — it keeps going until it returns or the process exits — and named in the
// ErrShutdownTimeout. The hooks after it still run, each with the same grace, so an overrun
// never skips the quick unlock or close that follows it.
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
	var stuck []string
	for i, hook := range slices.Backward(s.shutdown) {
		finished, err := s.awaitHook(ctx, hook)
		if !finished {
			stuck = append(stuck, strconv.Itoa(i+1))
			continue
		}
		if err != nil && first == nil {
			first = fmt.Errorf("shutdown: %w", err)
		}
	}
	if len(stuck) > 0 {
		what := fmt.Sprintf("shutdown hook %s", stuck[0])
		if len(stuck) > 1 {
			what = "shutdown hooks " + strings.Join(stuck, ", ")
		}
		what += fmt.Sprintf(" (of %d, in registration order) did not finish", len(s.shutdown))
		if first != nil {
			return firstOf(prior, fmt.Errorf("%w: %s: %w", ErrShutdownTimeout, what, first))
		}
		return firstOf(prior, fmt.Errorf("%w: %s", ErrShutdownTimeout, what))
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
	return firstOf(prior, first)
}

// firstOf returns prior when set, else err. A worker's own failure wins over anything teardown
// reports: it is the cause, and a teardown cut short is usually its consequence.
func firstOf(prior, err error) error {
	if prior != nil {
		return prior
	}
	return err
}

// hookGrace is how long a hook gets, once the budget is over, to return with the ctx error it
// was just handed. It separates a hook that honors its ctx from one that is stuck; either way
// the result is ErrShutdownTimeout, and this only decides which one the message describes.
const hookGrace = 10 * time.Millisecond

// awaitHook runs one hook and waits for it within ctx's budget. finished is false when the
// budget ran out first; the hook is then left running. With no budget it simply calls the hook.
func (s *Service) awaitHook(ctx context.Context, hook func(context.Context) error) (finished bool, err error) {
	if s.timeout <= 0 {
		return true, runHook(ctx, hook)
	}
	done := make(chan error, 1) // buffered: an abandoned hook must be able to finish and exit
	go func() { done <- runHook(ctx, hook) }()
	select {
	case err := <-done:
		return true, err
	case <-ctx.Done():
		select {
		case err := <-done:
			return true, err
		case <-time.After(hookGrace):
			return false, nil
		}
	}
}
