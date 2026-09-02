package rotini

import "context"

// The handler contract: the [Handlers] interface every command's handler set
// implements, and the embeddable no-ops that let a handler declare only the hooks it
// actually uses.

// Handlers is the lifecycle interface every command's handler set
// implements. The runtime invokes the hooks in order, sharing one [Context]
// across the chain; handlers read their typed inputs with [Parser.Parse].
type Handlers interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
	CascadingPostRun(ctx context.Context, rtx *Context)
}

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
