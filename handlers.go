package rotini

import "context"

// Handlers is the lifecycle interface every command's handler set implements. The runtime
// invokes the hooks in order, sharing one [Context] across the chain. Embed the Default types
// below to declare only the hooks a command actually uses.
//
// # Instance lifetime
//
// The runtime asks your ProgramHandlers for a command's handler ONCE PER FRAME, PER RUN, and
// the value it gets back serves that frame's hooks for that run. Two consequences are worth
// knowing before reaching for the registry:
//
//   - A FIELD carries state between one command's own hooks. The leaf's PreRun, Run and PostRun
//     share one value, and any frame's CascadingPreRun and CascadingPostRun share one value. A
//     transaction opened in PreRun and committed in PostRun can simply live in a field: no key,
//     no lookup, and the compiler checks the type.
//   - A field CANNOT cross frames. `db`'s hooks and `db migrate`'s hooks are different values,
//     so state that travels down the chain belongs in the registry — [Key.BindTo] for one run,
//     [Provide] for every run.
//
// Where the state goes decides which tool fits:
//
//	state flows…                                  use
//	────────────                                  ───
//	between one command's own hooks               a field on the handler
//	between different commands in the chain       Key.BindTo(rtx, v)
//	across every run of the program               Provide / Program.Bind
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
// runs ([REPL], [StdioServer], or [Program.Run] from several goroutines) it is a data race.
//
// So: if you write your own ProgramHandlers and your handlers keep state in fields, return a
// new value per call.
type Handlers interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
	CascadingPostRun(ctx context.Context, rtx *Context)
}

// The four Default* types below have empty method bodies, so `go tool cover` reports them at
// 0.0% forever: there are no statements to count. TestDefaultHooksAreNoOps executes all four
// regardless — "does nothing, safely, including with a nil context" is a real contract, since
// every generated stub embeds them.

// DefaultCascadingPreRun is an embeddable no-op [Handlers.CascadingPreRun].
type DefaultCascadingPreRun struct{}

// CascadingPreRun does nothing.
func (DefaultCascadingPreRun) CascadingPreRun(ctx context.Context, rtx *Context) {}

// DefaultPreRun is an embeddable no-op [Handlers.PreRun].
type DefaultPreRun struct{}

// PreRun does nothing.
func (DefaultPreRun) PreRun(ctx context.Context, rtx *Context) {}

// DefaultPostRun is an embeddable no-op [Handlers.PostRun].
type DefaultPostRun struct{}

// PostRun does nothing.
func (DefaultPostRun) PostRun(ctx context.Context, rtx *Context) {}

// DefaultCascadingPostRun is an embeddable no-op [Handlers.CascadingPostRun].
type DefaultCascadingPostRun struct{}

// CascadingPostRun does nothing.
func (DefaultCascadingPostRun) CascadingPostRun(ctx context.Context, rtx *Context) {}
