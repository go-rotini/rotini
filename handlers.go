package rotini

import "context"

// Handler is the lifecycle interface every command's handler implements. The runtime invokes
// the hooks in order, sharing one [Context] across the chain. Embed the No* types below to
// declare only the hooks a command uses.
//
// # Instance lifetime
//
// Per run, the runtime asks the program's handlers (the generated ProgramHandlers, through
// [NewWiredProgram], [NewProgramFunc] or [NewProgram]) once for each command in the chain, and
// that value serves the command's hooks for the run:
//
//   - A field carries state between one command's own hooks. The leaf's PreRun, Run and
//     PostRun share one value, as do a command's CascadingPreRun and CascadingPostRun.
//   - A field cannot cross commands. State that travels down the chain is a dependency.
//
// Where the state flows decides the tool:
//
//	state flows…                                  use
//	────────────                                  ───
//	between one command's own hooks               a field on the handler
//	between different commands in the chain       rtx.SetDependency(dep, v)
//	built once per run, on first use              p.WithProvider(dep, fn)
//	across every run of the program               p.WithDependency(dep, v)
//
// # Hand-written handler sets
//
// Generated wiring methods return a new handler per call, so fields are per-run state. A
// hand-written wiring method that returns a shared value makes that handler's fields shared
// across runs, and a data race under concurrent [Program.Run]. Handlers that keep state in
// fields must be returned fresh on every call.
type Handler interface {
	// CascadingPreRun runs for every command in the chain, root first, before the invoked
	// command's PreRun.
	CascadingPreRun(ctx context.Context, rtx *Context)
	// PreRun runs for the invoked command only, before Run.
	PreRun(ctx context.Context, rtx *Context)
	// Run does the invoked command's work.
	Run(ctx context.Context, rtx *Context)
	// PostRun is the invoked command's teardown; it runs when its PreRun began.
	PostRun(ctx context.Context, rtx *Context)
	// CascadingPostRun is each command's teardown, leaf first; it runs when that command's
	// CascadingPreRun began.
	CascadingPostRun(ctx context.Context, rtx *Context)
}

// NoCascadingPreRun is an embeddable no-op [Handler.CascadingPreRun]. [NoHooks] embeds all four
// no-ops at once.
type NoCascadingPreRun struct{}

// CascadingPreRun does nothing.
func (NoCascadingPreRun) CascadingPreRun(ctx context.Context, rtx *Context) {}

// NoPreRun is an embeddable no-op [Handler.PreRun]. [NoHooks] embeds all four no-ops at once.
type NoPreRun struct{}

// PreRun does nothing.
func (NoPreRun) PreRun(ctx context.Context, rtx *Context) {}

// NoPostRun is an embeddable no-op [Handler.PostRun]. [NoHooks] embeds all four no-ops at
// once.
type NoPostRun struct{}

// PostRun does nothing.
func (NoPostRun) PostRun(ctx context.Context, rtx *Context) {}

// NoCascadingPostRun is an embeddable no-op [Handler.CascadingPostRun]. [NoHooks] embeds all
// four no-ops at once.
type NoCascadingPostRun struct{}

// CascadingPostRun does nothing.
func (NoCascadingPostRun) CascadingPostRun(ctx context.Context, rtx *Context) {}

// NoHooks embeds the four no-op hooks, for a hand-written handler:
//
//	type handlers struct{ rotini.NoHooks }
//
//	func (*handlers) Run(ctx context.Context, rtx *rotini.Context) { … }
//
// A method declared on the outer type takes precedence over the promoted no-op.
//
// NoHooks does not supply Run, so a handler with a missing or misspelled Run fails the
// `var _ rotini.Handler` assertion at compile time. Generated stubs embed the No* types
// they need individually.
type NoHooks struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}

// ErrorHandler is [Handler] with hooks that return an error. A non-nil error is recorded as
// [Context.RecordError] records it; from CascadingPreRun or PreRun it also stops forward
// progress, as [Context.HaltWith] does. Teardown still runs, and an error from PostRun or
// CascadingPostRun does not stop the rest of it.
//
//	func (*addHandler) Run(ctx context.Context, rtx *rotini.Context) error {
//	    inputs, err := rtx.Inputs[AddInputs]()
//	    if err != nil {
//	        return err
//	    }
//	    return store.Add(inputs.Add.Arguments.Title)
//	}
//
// Returning an error a hook has just recorded with HaltWith or RecordError does not record it
// twice. A [Middleware] sees the returned error before it is recorded. Everything else is
// unchanged: [Context.RecordError] records and continues, and
// [Context.HaltWithCode] sets a code before a `return nil`.
//
// A type is one shape or the other, since Go allows only one Run method per type. Wrap an
// ErrorHandler with [FromErrorHandler] where a Handler is expected; generated wiring does this
// for stubs that assert ErrorHandler, and [NewProgram] does it for a method that returns one.
// Embed the No*E types below to declare only the hooks a command uses.
type ErrorHandler interface {
	// CascadingPreRun runs for every command in the chain, root first, before the invoked
	// command's PreRun.
	CascadingPreRun(ctx context.Context, rtx *Context) error
	// PreRun runs for the invoked command only, before Run.
	PreRun(ctx context.Context, rtx *Context) error
	// Run does the invoked command's work.
	Run(ctx context.Context, rtx *Context) error
	// PostRun is the invoked command's teardown; it runs when its PreRun began.
	PostRun(ctx context.Context, rtx *Context) error
	// CascadingPostRun is each command's teardown, leaf first; it runs when that command's
	// CascadingPreRun began.
	CascadingPostRun(ctx context.Context, rtx *Context) error
}

// NoCascadingPreRunE is an embeddable no-op [ErrorHandler.CascadingPreRun]. [NoHooksE] embeds
// all four no-ops at once.
type NoCascadingPreRunE struct{}

// CascadingPreRun does nothing.
func (NoCascadingPreRunE) CascadingPreRun(ctx context.Context, rtx *Context) error { return nil }

// NoPreRunE is an embeddable no-op [ErrorHandler.PreRun]. [NoHooksE] embeds all four no-ops at
// once.
type NoPreRunE struct{}

// PreRun does nothing.
func (NoPreRunE) PreRun(ctx context.Context, rtx *Context) error { return nil }

// NoPostRunE is an embeddable no-op [ErrorHandler.PostRun]. [NoHooksE] embeds all four no-ops
// at once.
type NoPostRunE struct{}

// PostRun does nothing.
func (NoPostRunE) PostRun(ctx context.Context, rtx *Context) error { return nil }

// NoCascadingPostRunE is an embeddable no-op [ErrorHandler.CascadingPostRun]. [NoHooksE] embeds
// all four no-ops at once.
type NoCascadingPostRunE struct{}

// CascadingPostRun does nothing.
func (NoCascadingPostRunE) CascadingPostRun(ctx context.Context, rtx *Context) error { return nil }

// NoHooksE is [NoHooks] for an [ErrorHandler]: the four no-op hooks, leaving Run to the outer
// type, so a missing or misspelled Run fails the `var _ rotini.ErrorHandler` assertion.
type NoHooksE struct {
	NoCascadingPreRunE
	NoPreRunE
	NoPostRunE
	NoCascadingPostRunE
}

// FromErrorHandler returns h as a [Handler], applying the rules [ErrorHandler] describes to
// each returned error. A nil h returns nil.
func FromErrorHandler(h ErrorHandler) Handler {
	if h == nil {
		return nil
	}
	return errorHandler{h}
}

// errorHandler adapts an ErrorHandler to the Handler the engine runs.
type errorHandler struct{ h ErrorHandler }

func (a errorHandler) CascadingPreRun(ctx context.Context, rtx *Context) {
	rtx.returned(a.h.CascadingPreRun(ctx, rtx), true)
}

func (a errorHandler) PreRun(ctx context.Context, rtx *Context) {
	rtx.returned(a.h.PreRun(ctx, rtx), true)
}

func (a errorHandler) Run(ctx context.Context, rtx *Context) {
	rtx.returned(a.h.Run(ctx, rtx), true)
}

func (a errorHandler) PostRun(ctx context.Context, rtx *Context) {
	rtx.returned(a.h.PostRun(ctx, rtx), false)
}

func (a errorHandler) CascadingPostRun(ctx context.Context, rtx *Context) {
	rtx.returned(a.h.CascadingPostRun(ctx, rtx), false)
}
