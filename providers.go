package rotini

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
)

// WithProvider returns an [Option] that registers fn as the provider of dep; it is the
// composable form of [Program.WithProvider], for use with [Program.With]:
//
//	// in the cmd package, so main.go and the tests share it
//	var WithStore = rotini.WithProvider(Store, openStore)
//
//	cmd.NewProgram().With(cmd.WithStore).Execute()
func WithProvider[T any](dep Dependency[T], fn func(ctx context.Context, rtx *Context) (T, error)) Option {
	return func(p *Program) { p.WithProvider(dep, fn) }
}

// WithProvider registers fn as the provider of dep and returns p for chaining. A provider
// builds the dependency for one run, on the first request for it in that run:
//
//	func openStore(ctx context.Context, rtx *rotini.Context) (*store.Store, error) {
//		in, err := rtx.Inputs[TodoInputs]()
//		if err != nil {
//			return nil, err
//		}
//		return store.Open(ctx, in.Todo.Flags.Database)
//	}
//
//	cmd.NewProgram().WithProvider(cmd.Store, openStore).Execute()
//
// The rules:
//   - Lazy: fn runs only when a hook asks for dep ([Context.Dependency],
//     [Context.GetDependency] or [Context.MustGetDependency]), so a run that never asks, such
//     as a --help that halts in the root's CascadingPreRun, never builds it.
//   - Once per run: concurrent requests in one run wait for a single call and share its value
//     or its error, and every run gets its own value. fn receives the context of the hook that
//     asked first, and may read rtx.Inputs.
//   - A set value wins: a value registered with [Program.WithDependency],
//     [Context.SetDependency] or [Context.SetDependencyIfAbsent] is returned instead, and fn
//     is not called, so a test's double needs nothing more.
//   - Closed after teardown: a value fn built that implements [io.Closer] is closed once
//     every hook has finished, before the reporter runs, in reverse order of construction. A
//     Close error is recorded as an error. Values fn did not build are never closed.
//   - Errors keep their category: [Context.Dependency] returns fn's error as is, so
//     [UsageError] from fn is a usage error.
//
// A later registration under the same name replaces an earlier one, and a nil fn removes it.
// A provider that asks for its own dependency, directly or through another provider,
// deadlocks.
func (p *Program) WithProvider[T any](dep Dependency[T], fn func(ctx context.Context, rtx *Context) (T, error)) *Program {
	if fn == nil {
		p.deps.provide(dep.name, nil)
		return p
	}
	p.deps.provide(dep.name, func(rtx *Context) (any, error) {
		v, err := fn(rtx.Context(), rtx) // the context of the hook that asked
		if err != nil {
			return nil, err
		}
		return v, nil
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
type providerFunc func(rtx *Context) (any, error)

// providedValue is one provider's result in one run.
type providedValue struct {
	once sync.Once
	v    any
	err  error
}

// providedCloser is a provider-built value to close when the run settles.
type providedCloser struct {
	name string
	c    io.Closer
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

// provide runs fn for name and keeps its result in pv, queueing a value that implements
// io.Closer for closing. A provider that panics leaves an internal error behind for the
// requests after it; the panic itself reaches the hook that asked.
func (rtx *Context) provide(name string, fn providerFunc, pv *providedValue) {
	pv.err = InternalError(fmt.Errorf("rotini: the provider of the dependency %q panicked", name))
	v, err := fn(rtx)
	if err != nil {
		pv.err = err
		return
	}
	pv.v, pv.err = v, nil
	if c, ok := v.(io.Closer); ok && !isNilValue(v) {
		rtx.mu.Lock()
		rtx.closers = append(rtx.closers, providedCloser{name: name, c: c})
		rtx.mu.Unlock()
	}
}

// isNilValue reports whether v holds a nil pointer, map, slice, channel or func, which a
// method call on would likely fault.
func isNilValue(v any) bool {
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// closeProvided closes the values the run's providers built that implement io.Closer, in
// reverse order of construction, recording each Close error as an error and each Close panic
// as a fault. Each value is closed at most once.
func (rtx *Context) closeProvided() {
	rtx.mu.Lock()
	closers := rtx.closers
	rtx.closers = nil
	rtx.mu.Unlock()
	for _, pc := range slices.Backward(closers) {
		rtx.closeOne(pc)
	}
}

func (rtx *Context) closeOne(pc providedCloser) {
	defer func() {
		if r := recover(); r != nil {
			rtx.recordFault(&PanicError{Value: r, Stack: debug.Stack()})
		}
	}()
	if err := pc.c.Close(); err != nil {
		rtx.RecordError(fmt.Errorf("closing the dependency %q: %w", pc.name, err))
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
