package rotini

import "context"

// Handler is the lifecycle interface every command's handler implements. The runtime invokes
// the hooks in order, sharing one [Context] across the chain. Embed the No* types below to
// declare only the hooks a command uses.
//
// # Instance lifetime
//
// Per run, the runtime asks the handler set given to [NewProgram] (the generated
// ProgramHandlers) once for each command in the chain, and that value serves the command's
// hooks for the run:
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
//	across every run of the program               p.WithDependency(dep, v)
//
// # Hand-written handler sets
//
// Generated wiring methods return a new handler per call, so fields are per-run state. A
// hand-written wiring method that returns a shared value makes that handler's fields shared
// across runs, and a data race under concurrent [Program.Run]. Handlers that keep state in
// fields must be returned fresh on every call.
type Handler interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
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
// `var _ rotini.Handler` assertion at compile time. Generated stubs embed the four types
// individually.
type NoHooks struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}
