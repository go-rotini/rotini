package rotini

import (
	"errors"
	"fmt"
	"maps"
	"sync"
)

// Dependency is a typed handle for one of the program's dependencies, pairing the name it is
// stored under with the type it is stored as, so a handler needs neither a string key nor a
// type assertion:
//
//	// declared once, beside the thing it names
//	var Store = rotini.NewDependency[*store.Store]("taskr.store")
//
//	// main.go: the value's type is checked at compile time here
//	cmd.NewProgram().WithDependency(tasks.Store, store.New()).Execute()
//
//	// any handler
//	s := rtx.MustGetDependency(tasks.Store)
//
// Registration happens at run time, so a missing registration is not a compile error; it is
// reported when a handler requests the dependency.
//
// Dependencies have two scopes:
//   - program-wide: [Program.WithDependency] or the [WithDependency] option, seeded into
//     every run
//   - one run: [Context.SetDependency] or [Context.SetDependencyIfAbsent] from a hook, visible
//     to the hooks that run after it, or a provider registered with [Program.WithProvider],
//     which builds the run's value on first request and closes it after teardown
//
// A handle whose name is known only at run time can be built as
// rotini.NewDependency[any](name).
//
// The zero Dependency is named "" and is shared by every zero Dependency of any type; build
// handles with [NewDependency].
type Dependency[T any] struct{ name string }

// NewDependency returns a typed handle for a dependency stored under name. The name must be
// unique within a program; an empty name is accepted and refers to the same entry as the zero
// Dependency.
func NewDependency[T any](name string) Dependency[T] { return Dependency[T]{name: name} }

// Name returns the name the dependency is stored under.
func (d Dependency[T]) Name() string { return d.name }

// String implements [fmt.Stringer], returning the dependency's name.
func (d Dependency[T]) String() string { return d.name }

// WithDependency returns an [Option] that registers value under dep for the whole program; it
// is the composable form of [Program.WithDependency], for use with [Program.With]:
//
//	cmd.NewProgram().
//		With(
//			rotini.WithDependency(tasks.Store, store),
//			rotini.WithDependency(tasks.Client, client),
//		).
//		WithVersion(version).
//		Execute()
func WithDependency[T any](dep Dependency[T], value T) Option {
	return func(p *Program) { p.WithDependency(dep, value) }
}

// WithDependency registers value under dep for the whole program, so every run sees it, and
// returns p for chaining:
//
//	cmd.NewProgram().
//		WithDependency(tasks.Store, store).
//		WithVersion(version).
//		Execute()
//
// A later registration under the same name replaces an earlier one.
//
// Go infers T from both arguments, so for a handle declared over an interface the value must
// already have that interface type. Convert the value, or name the type:
//
//	var Store = rotini.NewDependency[store.Store]("taskr.store") // an interface
//
//	p.WithDependency(Store, store.Store(store.NewMem()))
//	p.WithDependency[store.Store](Store, store.NewMem())
//
// The dependency namespace belongs to the application. Rotini's own settings (input reader,
// parser, version, help) are typed Program options, so no dependency name can shadow them.
func (p *Program) WithDependency[T any](dep Dependency[T], value T) *Program {
	p.deps.set(dep.name, value)
	return p
}

// ErrDependencyNotFound is the sentinel for a dependency that is not registered or provided,
// or is registered as a different type. [Context.MustGetDependency] panics with a
// [*DependencyError] wrapping it, which the runtime recovers and routes to the reporter.
var ErrDependencyNotFound = errors.New("rotini: dependency not found")

// DependencyError reports a dependency that was requested but not registered, registered as
// a different type, or whose provider failed. It unwraps to [ErrDependencyNotFound], or to
// the provider's error when that failed; recover the name with errors.As.
type DependencyError struct {
	Name string // the name the dependency was requested under

	// err is the provider's error when the provider failed.
	err error

	// got and want are the registered and requested types when the name is registered as a
	// different type, got "nil" for a nil registered as an interface; both are empty when
	// nothing is registered.
	got, want string
}

// Error returns a one-line message stating whether the dependency is missing, registered as
// nil, registered as a different type, or failed in its provider, whose message it includes.
func (e *DependencyError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("rotini: the dependency %q could not be provided: %v", e.Name, e.err)
	}
	if e.got == "nil" {
		return fmt.Sprintf("rotini: the dependency %q is registered as nil, not the %s requested", e.Name, e.want)
	}
	if e.got != "" {
		return fmt.Sprintf("rotini: the dependency %q is a %s, not the %s requested", e.Name, e.got, e.want)
	}
	return fmt.Sprintf("rotini: no dependency registered as %q", e.Name)
}

// Unwrap returns [ErrDependencyNotFound] and [ErrInternal], so [CategoryOf] classifies the
// error as [CategoryInternal]. When the provider failed it returns the provider's error in
// place of ErrDependencyNotFound, so the error keeps that error's category, if it has one.
func (e *DependencyError) Unwrap() []error {
	if e.err != nil {
		return []error{e.err, ErrInternal}
	}
	return []error{ErrDependencyNotFound, ErrInternal}
}

// GetDependency returns the value of dep in this run and whether one is available as type T,
// building it with dep's provider ([Program.WithProvider]) on first request. A provider's
// error reads as unavailable and is not recorded; use [Context.Dependency] to get it.
//
//	client, ok := rtx.GetDependency(tasks.Client)
func (rtx *Context) GetDependency[T any](dep Dependency[T]) (T, bool) {
	v, _, err := lookupDependency(rtx, dep)
	return v, err == nil
}

// MustGetDependency returns the value of dep in this run, building it with dep's provider
// ([Program.WithProvider]) on first request, or panics with a [*DependencyError] when it is
// missing, not a T, or its provider failed; for a failed provider the DependencyError wraps
// the provider's error, which errors.As reaches. The runtime recovers the panic during
// dispatch and routes it to the reporter.
//
//	s := rtx.MustGetDependency(tasks.Store)
func (rtx *Context) MustGetDependency[T any](dep Dependency[T]) T {
	v, failed, err := lookupDependency(rtx, dep)
	switch {
	case failed:
		panic(&DependencyError{Name: dep.name, err: err})
	case err != nil:
		panic(err)
	}
	return v
}

// SetDependency registers value under dep for this run, replacing any existing value, so hooks
// that run later see it. Use it for per-run values such as a client built from the inputs;
// program-wide values belong in [Program.WithDependency]. It is safe for concurrent use.
func (rtx *Context) SetDependency[T any](dep Dependency[T], value T) {
	rtx.setDependency(dep.name, value)
}

// SetDependencyIfAbsent registers value under dep for this run only if nothing is registered
// there yet, atomically. A handler can register its real implementation this way while a test
// that registered a double under the same name keeps the double.
//
//	rtx.SetDependencyIfAbsent(Clock, time.Now)
//	now := rtx.MustGetDependency(Clock)
func (rtx *Context) SetDependencyIfAbsent[T any](dep Dependency[T], value T) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.services == nil {
		rtx.services = make(map[string]any)
	}
	if _, ok := rtx.services[dep.name]; !ok {
		rtx.services[dep.name] = value
	}
}

// setDependency stores value under name, replacing any prior value. It is safe for concurrent
// use.
func (rtx *Context) setDependency(name string, value any) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.services == nil {
		rtx.services = make(map[string]any)
	}
	rtx.services[name] = value
}

// dependency returns the value stored under name and whether anything is stored there.
func (rtx *Context) dependency(name string) (any, bool) {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	v, ok := rtx.services[name]
	return v, ok
}

// dependencyStore is the dependencies a program seeds each run with, and its providers. Each
// run gets its own copy of the values, so a dependency set during a run stays local to it. It
// is safe for concurrent use.
type dependencyStore struct {
	mu        sync.RWMutex
	m         map[string]any
	providers map[string]providerFunc
}

// set stores value under name, replacing any prior value.
func (d *dependencyStore) set(name string, value any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = make(map[string]any)
	}
	d.m[name] = value
}

// clone copies the store for a run; nil when nothing is stored.
func (d *dependencyStore) clone() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return maps.Clone(d.m)
}
