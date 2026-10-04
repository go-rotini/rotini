package rotini

import "context"

// Handler is the lifecycle interface every command's handler set implements. The runtime
// invokes the hooks in order, sharing one [Context] across the chain. Embed the Default types
// below to declare only the hooks a command actually uses.
//
// # Instance lifetime
//
// The runtime asks your ProgramHandlers — the aggregate interface codegen generates, and the
// value passed to [NewProgram] — for a command's handler ONCE PER COMMAND IN THE CHAIN, PER RUN, and
// the value it gets back serves that command's hooks for that run. Two consequences are worth
// knowing before reaching for a dependency:
//
//   - A FIELD carries state between one command's own hooks. The leaf's PreRun, Run and PostRun
//     share one value, and any command's CascadingPreRun and CascadingPostRun share one value. A
//     transaction opened in PreRun and committed in PostRun can simply live in a field: no key,
//     no lookup, and the compiler checks the type.
//   - A field CANNOT cross commands. `db`'s hooks and `db migrate`'s hooks are different values,
//     so state that travels down the chain is a dependency — [Context.SetDependency] for one
//     run, [Program.WithDependency] for every run.
//
// Where the state goes decides which tool fits:
//
//	state flows…                                  use
//	────────────                                  ───
//	between one command's own hooks               a field on the handler
//	between different commands in the chain       rtx.SetDependency(dep, v)
//	across every run of the program               p.WithDependency(dep, v)
//
// # If you supply your own ProgramHandlers
//
// Whether each run gets a FRESH handler is decided by your wiring method, not by the runtime —
// the runtime calls it and uses whatever it returns. Generated code returns a new value per
// call, so generated programs get a fresh handler per run and fields are per-run state.
//
// A wiring method that returns a SHARED value instead — a field on your aggregate, a package
// variable — makes that handler's fields shared across runs. For a stateless handler that is
// harmless and common. For one that keeps state in fields it is a bug, and under concurrent
// runs ([Program.Run] from several goroutines) it is a data race.
//
// So: if you write your own ProgramHandlers and your handlers keep state in fields, return a
// new value per call.
type Handler interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
	CascadingPostRun(ctx context.Context, rtx *Context)
}

// The four No* types below have empty method bodies, so `go tool cover` reports them at
// 0.0% forever: there are no statements to count. TestDefaultEmbedsAreNoOps executes all four
// regardless — "does nothing, safely, including with a nil context" is a real contract, since
// every generated stub embeds them.

// NoCascadingPreRun is an embeddable no-op [Handler.CascadingPreRun]. Embed [NoHooks] instead
// to take all four no-ops at once, which is what a hand-written handler usually wants.
type NoCascadingPreRun struct{}

// CascadingPreRun does nothing.
func (NoCascadingPreRun) CascadingPreRun(ctx context.Context, rtx *Context) {}

// NoPreRun is an embeddable no-op [Handler.PreRun]. Embed [NoHooks] instead to take
// all four no-ops at once, which is what a hand-written handler usually wants.
type NoPreRun struct{}

// PreRun does nothing.
func (NoPreRun) PreRun(ctx context.Context, rtx *Context) {}

// NoPostRun is an embeddable no-op [Handler.PostRun]. Embed [NoHooks] instead to take
// all four no-ops at once, which is what a hand-written handler usually wants.
type NoPostRun struct{}

// PostRun does nothing.
func (NoPostRun) PostRun(ctx context.Context, rtx *Context) {}

// NoCascadingPostRun is an embeddable no-op [Handler.CascadingPostRun]. Embed [NoHooks] instead
// to take all four no-ops at once, which is what a hand-written handler usually wants.
type NoCascadingPostRun struct{}

// CascadingPostRun does nothing.
func (NoCascadingPostRun) CascadingPostRun(ctx context.Context, rtx *Context) {}

// NoHooks is the four no-ops above in one embeddable, for a handler written by hand:
//
//	type handlers struct{ rotini.NoHooks }
//
//	func (*handlers) Run(ctx context.Context, rtx *rotini.Context) { … }
//
// It supplies CascadingPreRun, PreRun, PostRun and CascadingPostRun, and a method declared on
// the outer type still wins over the one promoted through here — so implementing a hook is the
// same act it always was: declare a method with that name, and leave this embedded.
//
// # It does not supply Run, on purpose
//
// There is no no-op Run, and NoHooks does not invent one. A command whose Run is missing
// or misspelled therefore fails the `var _ rotini.Handler` assertion every stub carries, at
// compile time, by name. That property is why `rotini generate`'s hook audit does not have to
// check Run at all, and collapsing the embeds must not cost it.
//
// # When to reach for it
//
// Generated stubs keep the four embeds written out: the stub is where the hook vocabulary is
// introduced, and four named types show a reader the menu that one name hides. NoHooks is
// for the handler you write yourself — a package behind a spec's `handler: {import,
// convention}`, shared by several CLIs, where the author already knows the menu and the four
// lines are noise.
type NoHooks struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}
