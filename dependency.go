package rotini

import (
	"errors"
	"fmt"
	"reflect"
)

// Dependency is a typed handle for one of the program's dependencies: the name it is stored
// under and the type it is stored as, so the two cannot drift apart and a handler needs
// neither a string nor a type assertion:
//
//	// package-level, declared once beside the thing it names
//	var Store = rotini.NewDependency[*store.Store]("taskr.store")
//
//	// main.go — the type is checked here, where the value is supplied
//	cmd.Program.WithDependency(tasks.Store, store.New()).Execute()
//
//	// any handler — no string, no assertion, no comma-ok
//	s := rtx.MustGetDependency(tasks.Store)
//
// Dependencies are registered at run time, so a handle cannot make a forgotten
// [Program.WithDependency] a compile error. What it removes is the duplicated string literal,
// the type assertion, and the per-handler miss check — and a value of the wrong type fails at
// the call that supplies it rather than inside a handler.
//
// Two scopes exist, and both are used:
//   - program-wide: [Program.WithDependency] (or the [WithDependency] option) in main.go,
//     seeded into every run
//   - this run only: [Context.SetDependency] or [Context.SetDependencyIfAbsent] from a hook,
//     visible to the hooks that run after it
//
// A handle whose name is only known at run time is built the same way, as a
// Dependency[any] when there is no type to check: rotini.NewDependency[any](name).
//
// The zero Dependency is named "" — usable, but shared by every zero Dependency of any type;
// build handles with [NewDependency].
type Dependency[T any] struct{ name string }

// NewDependency returns a typed handle for a dependency stored under name. The name must be
// unique within a program; an empty name is not rejected, but is the same entry as the zero
// Dependency.
func NewDependency[T any](name string) Dependency[T] { return Dependency[T]{name: name} }

// Name returns the name the dependency is stored under.
func (d Dependency[T]) Name() string { return d.name }

// String implements [fmt.Stringer], so a handle renders as its name in a message.
func (d Dependency[T]) String() string { return d.name }

// WithDependency is the composable form of [Program.WithDependency]: it returns an [Option]
// that registers value under dep, for [Program.With] to apply.
//
// Options compose: several dependencies go into one [Program.With] call and read as a group,
// and a helper can hand back a set of them for a caller to apply:
//
//	cmd.Program.
//		With(
//			rotini.WithDependency(tasks.Store, store),
//			rotini.WithDependency(tasks.Client, client),
//		).
//		WithVersion(version).
//		Execute()
//
// The type is checked at this call, where the value is supplied, exactly as
// [Program.WithDependency] checks it.
func WithDependency[T any](dep Dependency[T], value T) Option {
	return func(p *Program) { p.WithDependency(dep, value) }
}

// WithDependency registers value under dep for the whole program, so every run sees it, and
// returns the program so configuration chains:
//
//	cmd.Program.
//		WithDependency(tasks.Store, store).
//		WithVersion(version).
//		Execute()
//
// The type is checked here, at the one place the value is supplied. A later registration
// under the same name replaces an earlier one.
//
// Go infers T from both arguments, so for a handle declared over an INTERFACE the value must
// already have that interface type. Convert a concrete value, or name the type:
//
//	var Store = rotini.NewDependency[store.Store]("taskr.store") // an interface
//
//	p.WithDependency(Store, store.Store(store.NewMem()))
//	p.WithDependency[store.Store](Store, store.NewMem())
//
// The dependencies are YOURS alone. rotini's own seams — the input reader, the parser, the
// version, the help pages — are typed options on the Program, so a name you choose can never
// shadow one of them.
func (p *Program) WithDependency[T any](dep Dependency[T], value T) *Program {
	p.rtx.setDependency(dep.name, value)
	return p
}

// ErrDependencyNotFound is the sentinel reported when a dependency is not registered, or is
// registered as a different type, which [Context.MustGetDependency] cannot hand back either.
// MustGetDependency panics a [*DependencyError] wrapping it, which the runtime recovers and
// routes to the reporter.
var ErrDependencyNotFound = errors.New("rotini: dependency not found")

// DependencyError reports a dependency that was requested but not registered, or registered
// as a different type. It unwraps to [ErrDependencyNotFound]; recover the name with errors.As.
type DependencyError struct {
	Name string // the name the dependency was requested under

	// got and want are the registered value's type and the requested one, when the name IS
	// registered — as a different type. Both empty: nothing is registered.
	got, want string
}

// Error renders the fault as a single line, saying which of the two it is: a missing
// dependency sends the reader to the code that registers it, a wrong type to the declaration.
func (e *DependencyError) Error() string {
	if e.got != "" {
		return fmt.Sprintf("rotini: the dependency %q is a %s, not the %s requested", e.Name, e.got, e.want)
	}
	return fmt.Sprintf("rotini: no dependency registered as %q", e.Name)
}

// Unwrap exposes both [ErrDependencyNotFound] and [ErrInternal], so [CategoryOf] classifies a
// missing dependency as [CategoryInternal] — a wiring bug, not the user's fault.
func (e *DependencyError) Unwrap() []error { return []error{ErrDependencyNotFound, ErrInternal} }

// GetDependency returns the value registered under dep, and whether one is registered as type
// T. It never panics; use [Context.MustGetDependency] to route a miss through the reporter
// instead of handling it inline.
//
//	client, ok := rtx.GetDependency(tasks.Client)
func (rtx *Context) GetDependency[T any](dep Dependency[T]) (T, bool) {
	v, ok := rtx.dependency(dep.name).(T)
	return v, ok
}

// MustGetDependency returns the value registered under dep, or panics with a
// [*DependencyError] when it is missing or not a T. The panic is intentional: the runtime
// recovers it inside dispatch and routes it through the reporter, so a handler that cannot run
// without a dependency reaches for MustGetDependency rather than handling a miss inline.
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

// SetDependency registers value under dep for this run, replacing any value already there, so
// the hooks that run after this one see it — a dependency a hook computes per run, such as a
// client built from the inputs. Program-wide dependencies belong in
// [Program.WithDependency], which seeds every run. It is safe for concurrent use.
func (rtx *Context) SetDependency[T any](dep Dependency[T], value T) {
	rtx.setDependency(dep.name, value)
}

// SetDependencyIfAbsent registers value under dep for this run only if nothing is registered
// there yet, atomically. It is the registered-default form of [Context.SetDependency]: a
// handler registers the real implementation, but a test that registered a double under the
// same name earlier keeps it. Either way the dependency is reachable by name rather than
// hidden inline.
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
