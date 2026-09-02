package rotini

// Typed registry keys: a [Key] carries the type of the service it names, so the
// string and the type assertion cannot drift apart and a handler needs neither.
//
// The untyped [Context.Bind] / [Get] / [MustGet] remain for dynamic cases; this is
// the ergonomic path for the dependency a CLI passes to every handler.

// Key names a service in the registry AND the type it is bound as. Declaring one is
// the idiomatic way to share a store, a client or a logger across handlers:
//
//	// package-level, declared once beside the thing it names
//	var StoreKey = rotini.NewKey[Store]("store")
//
//	// main.go — the type is checked HERE, where the value is supplied
//	tasks.StoreKey.Provide(cmd.Program, tasks.NewMemStore()).Execute()
//
//	// any handler — no string, no assertion, no comma-ok
//	store := tasks.StoreKey.MustGet(rtx)
//
// The registry is populated at run time, so a key cannot make a FORGOTTEN Provide a
// compile error. What it does remove is the rest: the duplicated string literal, the
// type assertion, and the per-handler miss check — and a value bound as the wrong type
// now fails at the binding site rather than inside a handler.
type Key[T any] struct{ name string }

// NewKey returns a typed registry key. The name is the registry key the value is
// stored under, so it must be unique within a program — the same convention the
// untyped [Context.Bind] follows.
func NewKey[T any](name string) Key[T] { return Key[T]{name: name} }

// Name returns the underlying registry key, for interoperating with the untyped
// [Context.Bind] / [Get] surface.
func (k Key[T]) Name() string { return k.name }

// String implements [fmt.Stringer], so a key renders as its name in a message.
func (k Key[T]) String() string { return k.name }

// Get returns the value bound under k, and whether one was bound as type T.
func (k Key[T]) Get(rtx *Context) (T, bool) { return rtx.Get[T](k.name) }

// MustGet returns the value bound under k, or routes a miss to the outcome funnel as
// a [*ServiceError] — the same contract as [MustGet], with the type supplied by the
// key rather than at the call site.
func (k Key[T]) MustGet(rtx *Context) T { return rtx.MustGet[T](k.name) }

// BindTo binds value under k on THIS invocation's context — for a service a hook
// computes per run (a request-scoped client, say). Bind program-wide services with
// [Key.Provide] instead, so they are seeded into every run.
func (k Key[T]) BindTo(rtx *Context, value T) { rtx.Bind(k.name, value) }

// Provide binds value on the program's registry, so every invocation sees it. It
// returns the program, so it chains like [Program.Bind]:
//
//	tasks.StoreKey.Provide(cmd.Program, store).
//		Bind(rotini.KeyParser, rotini.NewParser()).
//		Execute()
//
// The value's type is checked HERE, at the one place it is supplied — and because the
// key fixes T, any value ASSIGNABLE to T is accepted, so a constructor returning a
// concrete type satisfies a key declared over an interface.
func (k Key[T]) Provide(p *Program, value T) *Program {
	if p == nil {
		return nil
	}
	return p.Bind(k.name, value)
}
