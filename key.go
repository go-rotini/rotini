package rotini

// Key names a service in the registry and the type it is bound as, so the string and the type
// assertion cannot drift apart and a handler needs neither:
//
//	// package-level, declared once beside the thing it names
//	var StoreKey = rotini.NewKey[Store]("store")
//
//	// main.go — the type is checked here, where the value is supplied
//	tasks.StoreKey.Provide(cmd.Program, tasks.NewMemStore()).Execute()
//
//	// any handler — no string, no assertion, no comma-ok
//	store := tasks.StoreKey.MustGet(rtx)
//
// The registry is populated at run time, so a key cannot make a forgotten Provide a compile
// error. What it removes is the duplicated string literal, the type assertion, and the
// per-handler miss check — and a value bound as the wrong type now fails at the binding site
// rather than inside a handler. The untyped [Context.Bind] remains for dynamic cases.
type Key[T any] struct{ name string }

// NewKey returns a typed registry key. name is what the value is stored under, so it must be
// unique within a program.
func NewKey[T any](name string) Key[T] { return Key[T]{name: name} }

// Name returns the underlying registry key, for interoperating with the untyped
// [Context.Bind] / [Get] surface.
func (k Key[T]) Name() string { return k.name }

// String implements [fmt.Stringer], so a key renders as its name in a message.
func (k Key[T]) String() string { return k.name }

// Get returns the value bound under k, and whether one was bound as type T.
func (k Key[T]) Get(rtx *Context) (T, bool) { return rtx.Get[T](k.name) }

// MustGet returns the value bound under k, or routes a miss to the funnel as a
// [*ServiceError] — [Context.MustGet]'s contract, with the type supplied by the key.
func (k Key[T]) MustGet(rtx *Context) T { return rtx.MustGet[T](k.name) }

// BindTo binds value under k on this invocation's context, for a service a hook computes per
// run. Use [Key.Provide] for program-wide services, so they are seeded into every run.
func (k Key[T]) BindTo(rtx *Context, value T) { rtx.Bind(k.name, value) }

// Provide is the composable form of [Key.Provide]: it returns an [Option] that binds value
// under k, for [Program.With] to apply.
//
// It is a function rather than a method because Go does not allow type parameters on methods —
// p.Provide[T](k, v) cannot be written. [Key.Provide] solves that by taking the program as an
// argument, which type-checks correctly but ends the chain, so a program with two services had
// to abandon the fluent form the generated entrypoint teaches. This keeps both:
//
//	cmd.Program.
//		With(
//			rotini.Provide(tasks.StoreKey, store),
//			rotini.Provide(tasks.ClientKey, client),
//		).
//		Bind(rotini.KeyVersion, version).
//		Execute()
//
// The type is checked at this call, where the value is supplied, exactly as [Key.Provide]
// checks it. Reach for [Key.Provide] when there is one service and no chain to keep.
func Provide[T any](k Key[T], value T) Option {
	return func(p *Program) { p.Bind(k.name, value) }
}

// Provide binds value on the program's registry, so every invocation sees it, and returns the
// program so it chains like [Program.Bind]:
//
//	tasks.StoreKey.Provide(cmd.Program, store).
//		Bind(rotini.KeyVersion, version).
//		Execute()
//
// The type is checked here, at the one place the value is supplied. Because the key fixes T,
// any value assignable to T is accepted, so a constructor returning a concrete type satisfies
// a key declared over an interface.
func (k Key[T]) Provide(p *Program, value T) *Program {
	if p == nil {
		return nil
	}
	return p.Bind(k.name, value)
}
