package rotini

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
)

// WithProvider registers fn as the provider of dep and returns p for chaining. A provider
// builds the dependency for one run, on the first request for it in that run, and returns
// the value, the cleanup that releases it after the run (nil when there is nothing to
// release), and an error:
//
//	func openStore(ctx context.Context, rtx *rotini.Context) (*store.Store, func() error, error) {
//		in, err := rtx.Inputs[TodoInputs]()
//		if err != nil {
//			return nil, nil, err
//		}
//		s, err := store.Open(ctx, in.Todo.Flags.Database)
//		if err != nil {
//			return nil, nil, err
//		}
//		return s, s.Close, nil
//	}
//
//	cmd.NewProgram().WithProvider(cmd.Store, openStore).Execute()
//
// A provider that hands out a value it does not own, such as a pool opened in main.go,
// returns a nil cleanup, so the value outlives the run:
//
//	p.WithProvider(cmd.DB, func(context.Context, *rotini.Context) (*sql.DB, func() error, error) {
//		return db, nil, nil
//	})
//
// The rules:
//   - Lazy: fn runs only when a hook asks for dep ([Context.Dependency],
//     [Context.LookupDependency] or [Context.MustDependency]), so a run that never asks, such
//     as a --help that halts in the root's CascadingPreRun, never builds it.
//   - Once per run: concurrent requests in one run wait for a single call and share its value
//     or its error, and every run gets its own value. fn receives the context of the hook that
//     asked first, and may read rtx.Inputs.
//   - A set value wins: a value registered with [Program.WithDependency],
//     [Context.SetDependency] or [Context.SetDependencyIfAbsent] is returned instead, and fn
//     is not called, so a test's double needs nothing more.
//   - Cleaned up after teardown: the cleanup fn returned is called once every hook has
//     finished, before the reporter runs, in reverse order of construction. A cleanup error
//     is recorded as an error and a cleanup panic as a fault. Rotini releases nothing else:
//     a value with a nil cleanup is left as it is. When fn returns an error, its value and
//     cleanup are discarded, so a provider that fails releases what it built itself.
//   - Errors keep their category: [Context.Dependency] returns fn's error as is, so
//     [UsageError] from fn is a usage error.
//
// A later registration under the same name replaces an earlier one, and a nil fn removes it.
// A provider that asks for its own dependency, directly or through another provider,
// deadlocks.
func (p *Program) WithProvider[T any](dep Dependency[T], fn func(ctx context.Context, rtx *Context) (T, func() error, error)) *Program {
	if fn == nil {
		p.deps.provide(dep.name, nil)
		return p
	}
	p.deps.provide(dep.name, func(rtx *Context) (any, func() error, error) {
		v, cleanup, err := fn(rtx.Context(), rtx) // the context of the hook that asked
		if err != nil {
			return nil, nil, err
		}
		return v, cleanup, nil
	})
	return p
}

// Dependency returns the value of dep in this run, building it with the provider registered
// by [Program.WithProvider] on first request, or the error that kept it from being had:
//
//	s, err := rtx.Dependency(tasks.Store)
//	if err != nil {
//		return err
//	}
//
// The error is the provider's own, as it returned it, or a [*DependencyError] when nothing is
// registered or provided under dep's name, or the value is not a T.
func (rtx *Context) Dependency[T any](dep Dependency[T]) (T, error) {
	v, _, err := lookupDependency(rtx, dep)
	return v, err
}

// lookupDependency returns the value of dep, or the provider's error with failed set, or a
// *DependencyError when dep is missing or not a T.
func lookupDependency[T any](rtx *Context, dep Dependency[T]) (_ T, failed bool, _ error) {
	var zero T
	stored, found, err := rtx.resolveDependency(dep.name)
	if err != nil {
		return zero, true, err
	}
	if v, ok := stored.(T); ok {
		return v, false, nil
	}
	if !found {
		return zero, false, &DependencyError{Name: dep.name}
	}
	// A nil registered under an interface type is stored as a nil any, which no assertion
	// matches.
	want := reflect.TypeFor[T]()
	if stored == nil {
		if want.Kind() == reflect.Interface {
			return zero, false, nil
		}
		return zero, false, &DependencyError{Name: dep.name, got: "nil", want: want.String()}
	}
	return zero, false, &DependencyError{Name: dep.name, got: fmt.Sprintf("%T", stored), want: want.String()}
}

// providerFunc is a provider with its value type erased, called with the run's Context.
type providerFunc func(rtx *Context) (any, func() error, error)

// providedValue is one provider's result in one run.
type providedValue struct {
	once sync.Once
	v    any
	err  error
}

// providedCleanup is a provider's cleanup to call when the run settles.
type providedCleanup struct {
	name    string
	cleanup func() error
}

// resolveDependency returns what name resolves to in this run: a value set on the run (seeded
// from the program or set by a hook), else the provider's value, built on first request.
// found reports whether either exists; err is the provider's error.
func (rtx *Context) resolveDependency(name string) (v any, found bool, err error) {
	if v, ok := rtx.dependency(name); ok {
		return v, true, nil
	}
	if rtx.providers == nil {
		return nil, false, nil
	}
	fn := rtx.providers.provider(name)
	if fn == nil {
		return nil, false, nil
	}
	rtx.mu.Lock()
	pv := rtx.provided[name]
	if pv == nil {
		if rtx.provided == nil {
			rtx.provided = make(map[string]*providedValue)
		}
		pv = &providedValue{}
		rtx.provided[name] = pv
	}
	rtx.mu.Unlock()
	pv.once.Do(func() { rtx.provide(name, fn, pv) })
	return pv.v, true, pv.err
}

// provide runs fn for name and keeps its result in pv, queueing its cleanup, if any. A
// provider that panics leaves an internal error behind for the requests after it; the panic
// itself reaches the hook that asked.
func (rtx *Context) provide(name string, fn providerFunc, pv *providedValue) {
	pv.err = InternalError(fmt.Errorf("rotini: the provider of the dependency %q panicked", name))
	v, cleanup, err := fn(rtx)
	if err != nil {
		pv.err = err
		return
	}
	pv.v, pv.err = v, nil
	if cleanup != nil {
		rtx.mu.Lock()
		rtx.cleanups = append(rtx.cleanups, providedCleanup{name: name, cleanup: cleanup})
		rtx.mu.Unlock()
	}
}

// cleanUpProvided calls the cleanups the run's providers returned, in reverse order of
// construction, recording each error as an error and each panic as a fault. Each cleanup is
// called at most once.
func (rtx *Context) cleanUpProvided() {
	rtx.mu.Lock()
	cleanups := rtx.cleanups
	rtx.cleanups = nil
	rtx.mu.Unlock()
	for _, pc := range slices.Backward(cleanups) {
		rtx.cleanUpOne(pc)
	}
}

func (rtx *Context) cleanUpOne(pc providedCleanup) {
	defer func() {
		if r := recover(); r != nil {
			rtx.recordFault(&PanicError{Value: r, Stack: debug.Stack()})
		}
	}()
	if err := pc.cleanup(); err != nil {
		rtx.RecordError(fmt.Errorf("cleaning up the dependency %q: %w", pc.name, err))
	}
}

// provide registers fn as the provider of name, replacing any prior one; a nil fn removes it.
func (d *dependencyStore) provide(name string, fn providerFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if fn == nil {
		delete(d.providers, name)
		return
	}
	if d.providers == nil {
		d.providers = make(map[string]providerFunc)
	}
	d.providers[name] = fn
}

// provider returns the provider registered under name, or nil.
func (d *dependencyStore) provider(name string) providerFunc {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.providers[name]
}

// WithSharedProvider registers fn as the program-wide provider of dep and returns p for
// chaining. Where [Program.WithProvider] builds a value for each run, a shared provider builds
// one value for the program, on the first request in any run, and every later run gets that
// same value. It suits what is costly to open and safe to share, such as a connection pool or
// a client, in a program that runs many times in one process:
//
//	p.WithSharedProvider(cmd.DB, func(ctx context.Context, rtx *rotini.Context) (*sql.DB, func() error, error) {
//		db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
//		if err != nil {
//			return nil, nil, err
//		}
//		return db, db.Close, nil
//	})
//
// The rules:
//   - Lazy, once per program: fn runs only when a hook asks for dep, and concurrent requests,
//     from one run or from runs in flight together, wait for a single call and share its value
//     or its error. The value is not rebuilt until [Program.Close] releases it.
//   - A failure is retried: when fn returns an error or panics, the requests waiting on that
//     call get the error, the run keeps it for its own later requests, and the next run to ask
//     calls fn again.
//   - fn runs inside the run that asked first: it receives that hook's context and Context,
//     and may read rtx.Inputs. The value outlives the run, so it must not keep either.
//   - A set value wins, as for [Program.WithProvider]: a value registered with
//     [Program.WithDependency], [Context.SetDependency] or [Context.SetDependencyIfAbsent] is
//     returned instead, and fn is not called.
//   - Released by Close, not by a run: the cleanup fn returns is called by [Program.Close],
//     which [Program.Execute] calls for its run. [Program.Run] and [Program.RunContext] never
//     release a shared value, so a host that calls them repeatedly calls Close when it is done.
//     A value with a nil cleanup is left as it is.
//   - Errors keep their category: [Context.Dependency] returns fn's error as is.
//
// A later registration under the same name, shared or per run, replaces an earlier one, and a
// nil fn removes it; a value already built stays until Close releases it. A provider that asks
// for its own dependency, directly or through another provider, deadlocks.
func (p *Program) WithSharedProvider[T any](dep Dependency[T], fn func(ctx context.Context, rtx *Context) (T, func() error, error)) *Program {
	if fn == nil {
		p.deps.provide(dep.name, nil)
		return p
	}
	sp := &sharedProvider{name: dep.name, owner: &p.shared, build: func(ctx context.Context, rtx *Context) (any, func() error, error) {
		v, cleanup, err := fn(ctx, rtx)
		if err != nil {
			return nil, nil, err
		}
		return v, cleanup, nil
	}}
	p.deps.provide(dep.name, sp.get)
	return p
}

// Close releases the values the program's shared providers built ([Program.WithSharedProvider]),
// calling their cleanups in reverse order of construction, and returns their errors joined, a
// panicking cleanup as a [*PanicError]. A shared dependency asked for after Close is built
// again, by its provider, on the first request.
//
// [Program.Execute] calls Close for its run after every hook has finished, before the
// reporter, so a cleanup error is reported as a recorded error, and again before it exits, for
// anything built later; a second trapped signal, which exits at once, skips it. A host that
// calls [Program.Run] or [Program.RunContext] repeatedly calls Close once the last run has
// returned:
//
//	p := cmd.NewProgram().WithSharedProvider(cmd.DB, openDB)
//	defer p.Close()
//	for req := range requests {
//		p.RunContext(req.Context(), req.Argv)
//	}
//
// Close is safe to call more than once, and with nothing built it returns nil.
func (p *Program) Close() error {
	var errs []error
	p.closeShared(func(err error) { errs = append(errs, err) }, func(pe *PanicError) { errs = append(errs, pe) })
	return errors.Join(errs...)
}

// closeShared releases every built shared value, newest first, passing each cleanup error to
// record and each cleanup panic to fault.
func (p *Program) closeShared(record func(error), fault func(*PanicError)) {
	built := p.shared.take()
	for _, sp := range slices.Backward(built) {
		if cleanup := sp.release(); cleanup != nil {
			closeSharedOne(sp.name, cleanup, record, fault)
		}
	}
}

func closeSharedOne(name string, cleanup func() error, record func(error), fault func(*PanicError)) {
	defer func() {
		if r := recover(); r != nil {
			fault(&PanicError{Value: r, Stack: debug.Stack()})
		}
	}()
	if err := cleanup(); err != nil {
		record(fmt.Errorf("closing the shared dependency %q: %w", name, err))
	}
}

// sharedValues is the program's list of built shared values, in order of construction.
type sharedValues struct {
	mu    sync.Mutex
	built []*sharedProvider
}

// take empties the list and returns what it held.
func (s *sharedValues) take() []*sharedProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	built := s.built
	s.built = nil
	return built
}

// sharedProvider is one shared provider and the value it built, if any. Its lock is taken
// before its owner's.
type sharedProvider struct {
	name  string
	owner *sharedValues
	build func(ctx context.Context, rtx *Context) (any, func() error, error)

	mu      sync.Mutex
	built   bool
	v       any
	cleanup func() error
	attempt *sharedAttempt // the call in flight, or nil
}

// sharedAttempt is one call of a shared provider; done is closed when it returns or panics.
type sharedAttempt struct {
	done chan struct{}
	v    any
	err  error
}

// get is the shared provider's providerFunc: the built value, or the result of the call in
// flight, or a new call. The run gets no cleanup; Close releases the value.
func (sp *sharedProvider) get(rtx *Context) (any, func() error, error) {
	ctx := rtx.Context()
	sp.mu.Lock()
	if sp.built {
		v := sp.v
		sp.mu.Unlock()
		return v, nil, nil
	}
	if a := sp.attempt; a != nil {
		sp.mu.Unlock()
		select {
		case <-a.done:
			return a.v, nil, a.err
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("rotini: waiting for the shared dependency %q: %w", sp.name, ctx.Err())
		}
	}
	a := &sharedAttempt{done: make(chan struct{})}
	sp.attempt = a
	sp.mu.Unlock()

	var cleanup func() error
	a.err = InternalError(fmt.Errorf("rotini: the shared provider of the dependency %q panicked", sp.name))
	defer func() {
		sp.mu.Lock()
		sp.attempt = nil
		if a.err == nil {
			sp.built, sp.v, sp.cleanup = true, a.v, cleanup
			sp.owner.mu.Lock()
			sp.owner.built = append(sp.owner.built, sp)
			sp.owner.mu.Unlock()
		}
		sp.mu.Unlock()
		close(a.done)
	}()
	v, c, err := sp.build(ctx, rtx)
	if err != nil {
		a.err = err
		return nil, nil, err
	}
	a.v, a.err, cleanup = v, nil, c
	return v, nil, nil
}

// release forgets the built value, so the next request builds it again, and returns its
// cleanup.
func (sp *sharedProvider) release() func() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	cleanup := sp.cleanup
	sp.built, sp.v, sp.cleanup = false, nil, nil
	return cleanup
}
