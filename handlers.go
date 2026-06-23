package rotini

import "context"

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
