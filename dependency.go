package rotini

import (
	"errors"
	"fmt"
	"reflect"
)

// Dependency is a typed handle for one of the program's dependencies, pairing the name it is
// stored under with the type it is stored as, so a handler needs neither a string key nor a
// type assertion:
//
//	// declared once, beside the thing it names
//	var Store = rotini.NewDependency[*store.Store]("taskr.store")
//
//	// main.go: the value's type is checked at compile time here
//	cmd.NewProgram(cmd.Handlers()).WithDependency(tasks.Store, store.New()).Execute()
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
//     to the hooks that run after it
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
//	cmd.NewProgram(cmd.Handlers()).
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
//	cmd.NewProgram(cmd.Handlers()).
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
	p.rtx.setDependency(dep.name, value)
	return p
}

// ErrDependencyNotFound is the sentinel for a dependency that is not registered, or is
// registered as a different type. [Context.MustGetDependency] panics with a [*DependencyError]
// wrapping it, which the runtime recovers and routes to the reporter.
var ErrDependencyNotFound = errors.New("rotini: dependency not found")

// DependencyError reports a dependency that was requested but not registered, or registered
// as a different type. It unwraps to [ErrDependencyNotFound]; recover the name with errors.As.
type DependencyError struct {
	Name string // the name the dependency was requested under

	// got and want are the registered and requested types when the name is registered as a
	// different type; both are empty when nothing is registered.
	got, want string
}

// Error returns a one-line message stating whether the dependency is missing or registered as
// a different type.
func (e *DependencyError) Error() string {
	if e.got != "" {
		return fmt.Sprintf("rotini: the dependency %q is a %s, not the %s requested", e.Name, e.got, e.want)
	}
	return fmt.Sprintf("rotini: no dependency registered as %q", e.Name)
}

// Unwrap returns [ErrDependencyNotFound] and [ErrInternal], so [CategoryOf] classifies the
// error as [CategoryInternal].
func (e *DependencyError) Unwrap() []error { return []error{ErrDependencyNotFound, ErrInternal} }

// GetDependency returns the value registered under dep and whether one is registered as type
// T. It never panics.
//
//	client, ok := rtx.GetDependency(tasks.Client)
func (rtx *Context) GetDependency[T any](dep Dependency[T]) (T, bool) {
	v, ok := rtx.dependency(dep.name).(T)
	return v, ok
}

// MustGetDependency returns the value registered under dep, or panics with a
// [*DependencyError] when it is missing or not a T. The runtime recovers the panic during
// dispatch and routes it to the reporter.
//
//	s := rtx.MustGetDependency(tasks.Store)
func (rtx *Context) MustGetDependency[T any](dep Dependency[T]) T {
	v, ok := rtx.GetDependency(dep)
	if !ok {
		de := &DependencyError{Name: dep.name}
		if registered := rtx.dependency(dep.name); registered != nil {
			de.got, de.want = fmt.Sprintf("%T", registered), reflect.TypeFor[T]().String()
		}
		panic(de)
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

// dependency returns the value stored under name, or nil when there is none.
func (rtx *Context) dependency(name string) any {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.services[name]
}
