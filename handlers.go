package rotini

import "context"

// The Default* types are embeddable no-op implementations of the individual lifecycle
// hooks. Embed the ones a command does not use so its handler still satisfies
// [CommandHandlers] without spelling out every empty hook — leaving only the hooks
// that actually do something:
//
//	type fooHandlers struct {
//	    rotini.DefaultCascadingPreRun
//	    rotini.DefaultPreRun
//	    rotini.DefaultPostRun
//	    rotini.DefaultCascadingPostRun
//	}
//
//	var _ rotini.CommandHandlers = (*fooHandlers)(nil)
//
//	func (*fooHandlers) Run(ctx context.Context, rtx *rotini.Context) { /* the work */ }
//
// They mix and match: a command that needs both PreRun hooks but no PostRun hooks
// embeds only [DefaultPostRun] and [DefaultCascadingPostRun] and writes the rest. For
// the common "I only care about Run" case, embed [DefaultHooks] (all four at once).
//
// There is intentionally no DefaultRun. Every command must supply its own Run — a
// command without one does no work — so omitting the default makes that a
// compile-time requirement: a handler that embeds defaults but forgets Run fails the
// `var _ CommandHandlers = …` assertion (and the program wiring) with a clear
// "missing method Run", rather than silently dispatching to a do-nothing command.
//
// The methods use value receivers, so they promote whether the handler is embedded by
// value (as generated handlers are) and addressed by pointer.

// DefaultCascadingPreRun is an embeddable no-op [CommandHandlers.CascadingPreRun].
type DefaultCascadingPreRun struct{}

// CascadingPreRun does nothing.
func (DefaultCascadingPreRun) CascadingPreRun(ctx context.Context, rtx *Context) {}

// DefaultPreRun is an embeddable no-op [CommandHandlers.PreRun].
type DefaultPreRun struct{}

// PreRun does nothing.
func (DefaultPreRun) PreRun(ctx context.Context, rtx *Context) {}

// DefaultPostRun is an embeddable no-op [CommandHandlers.PostRun].
type DefaultPostRun struct{}

// PostRun does nothing.
func (DefaultPostRun) PostRun(ctx context.Context, rtx *Context) {}

// DefaultCascadingPostRun is an embeddable no-op [CommandHandlers.CascadingPostRun].
type DefaultCascadingPostRun struct{}

// CascadingPostRun does nothing.
func (DefaultCascadingPostRun) CascadingPostRun(ctx context.Context, rtx *Context) {}

// DefaultHooks bundles all four non-Run defaults for the common case where a command
// only needs Run: embed it and supply Run. It deliberately does not include Run, so
// the compile-time "Run is required" guarantee still holds.
//
//	type fooHandlers struct{ rotini.DefaultHooks }
//
//	var _ rotini.CommandHandlers = (*fooHandlers)(nil)
//
//	func (*fooHandlers) Run(ctx context.Context, rtx *rotini.Context) { /* the work */ }
type DefaultHooks struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}
