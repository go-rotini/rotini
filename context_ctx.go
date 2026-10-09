package rotini

import "context"

// Context returns the [context.Context] the running hook received: the run's context, or one
// an earlier hook passed to [Context.SetContext]. It is never nil. Read it in the hook; from a
// goroutine that outlives the hook it returns whatever is current.
func (rtx *Context) Context() context.Context {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if rtx.ctx == nil {
		return context.Background()
	}
	return rtx.ctx
}

// SetContext hands a derived context to the rest of the run: the hooks after the calling one,
// [Handler.Run] included, receive ctx, as do the teardowns of those later hooks. A nil ctx is
// ignored.
//
//	type appHandler struct{ cancel context.CancelFunc }
//
//	func (h *appHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
//		ctx, h.cancel = context.WithTimeout(ctx, 30*time.Second)
//		rtx.SetContext(ctx)
//	}
//
//	func (h *appHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
//		h.cancel()
//	}
//
// The rules:
//   - a teardown receives the context its own forward hook received, so the root's
//     CascadingPostRun above gets the run's context, not the one its pre-run set, and never a
//     deadline that has already passed;
//   - a call from a teardown or the reporter changes nothing, and the reporter always receives
//     the run's context;
//   - the caller owns the cancel function: keep it in a handler field and call it in the
//     matching teardown (handlers are created once per run);
//   - canceling the run's context still reaches every derived one, but a derived context
//     ending does not halt the run, and an [ExitCause] on it sets no exit code: a hook that
//     sees the derived context end stops with [Context.HaltWith] or [Context.HaltWithCode].
func (rtx *Context) SetContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if !rtx.ctxFrozen {
		rtx.ctx = ctx
	}
}

// enterHook sets the context the next hook runs with. frozen makes SetContext a no-op until
// the next call, for teardowns and the reporter.
func (rtx *Context) enterHook(ctx context.Context, frozen bool) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.ctx = ctx
	rtx.ctxFrozen = frozen
}
