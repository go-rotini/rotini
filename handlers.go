package rotini

import "context"

// Handlers is the lifecycle interface every command's handler set implements. The runtime
// invokes the hooks in order, sharing one [Context] across the chain. Embed the Default types
// below to declare only the hooks a command actually uses.
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
