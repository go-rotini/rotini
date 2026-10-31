package rotini

import (
	"context"
	"slices"
	"strconv"
)

// HookKind names one of a [Handler]'s five hooks.
type HookKind uint8

// The five hooks, in the order a run reaches them.
const (
	HookCascadingPreRun HookKind = iota
	HookPreRun
	HookRun
	HookPostRun
	HookCascadingPostRun
)

// String returns the hook's method name, such as "CascadingPreRun".
func (k HookKind) String() string {
	switch k {
	case HookCascadingPreRun:
		return "CascadingPreRun"
	case HookPreRun:
		return "PreRun"
	case HookRun:
		return "Run"
	case HookPostRun:
		return "PostRun"
	case HookCascadingPostRun:
		return "CascadingPostRun"
	}
	return "HookKind(" + strconv.Itoa(int(k)) + ")"
}

// teardown reports whether k is a teardown hook, whose error doesn't stop the run.
func (k HookKind) teardown() bool { return k == HookPostRun || k == HookCascadingPostRun }

// Hook is the hook invocation a [Middleware] wraps: which hook, and the command it belongs to.
// For a cascading hook that is the ancestor whose hook runs, as [Context.Command] reports it;
// rtx.Command().Invoked tells it from the command the user ran.
type Hook struct {
	Kind    HookKind
	Command Command
}

// HookFunc runs a hook, or the rest of the middleware chain around it, and returns its error:
// what an [HandlerE] hook returned, or nil for a [Handler] hook, which records through rtx.
type HookFunc func(ctx context.Context, rtx *Context) error

// Middleware wraps a hook invocation: it returns the function rotini calls in place of next.
// It sees the hook before and after it runs and the error it returns, and may change all
// three:
//
//   - Returning an error is the same as the hook returning it: the error is recorded and, from
//     a CascadingPreRun, PreRun or Run, forward progress stops. Teardown hooks still run.
//   - Not calling next skips the hook.
//   - Calling next with a derived context gives that context to the hook. It reaches this
//     hook only; [Context.SetContext] hands a context on to the hooks after it.
//
// An error a hook has just recorded itself, with [Context.HaltWith] or [Context.RecordError],
// isn't recorded twice when a middleware returns it.
//
// A middleware with nothing to do for a hook returns next itself; it never returns nil, which
// panics:
//
//	func confirmDestructive(h rotini.Hook, next rotini.HookFunc) rotini.HookFunc {
//		if h.Kind != rotini.HookRun {
//			return next
//		}
//		return func(ctx context.Context, rtx *rotini.Context) error {
//			if rotini.EffectsOf(rtx).Kind == "destructive" && !confirmed(rtx) {
//				return errors.New("refusing a destructive run without --yes")
//			}
//			return next(ctx, rtx)
//		}
//	}
//
// See [Program.WithMiddleware].
type Middleware func(h Hook, next HookFunc) HookFunc

// WithMiddleware wraps every hook invocation of every run in mw: setup and teardown hooks, for
// every command of the chain, composed commands' included. The first middleware registered is
// the outermost, so with WithMiddleware(a, b) a hook runs as a(b(hook)); later calls append,
// inside the earlier ones. A nil middleware is skipped.
//
// Middleware runs inside the run's panic handling: a panic in it is handled as a panic in the
// hook it wraps. Under a custom [Program.WithLifecycle] plan it wraps the [Handler] hooks the
// plan takes from [DefaultLifecycle]; a step function the plan writes itself is not a hook and
// runs as it is, so [Hook.Kind] always names one of a Handler's hooks.
//
//	cmd.NewProgram().
//		WithMiddleware(timing, confirmDestructive).
//		Execute()
func (p *Program) WithMiddleware(mw ...Middleware) *Program {
	for _, m := range mw {
		if m != nil {
			p.middleware = append(p.middleware, m)
		}
	}
	return p
}

// callWrapped runs hook kind of h inside the run's middleware, then applies its error as an
// [HandlerE]'s is applied, so the middleware sees the error before it is recorded.
func callWrapped(ctx context.Context, rtx *Context, h Handler, kind HookKind) {
	next := HookFunc(func(ctx context.Context, rtx *Context) error {
		restore := rtx.hookContext(ctx)
		defer restore()
		return invokeHook(ctx, rtx, h, kind)
	})
	hook := Hook{Kind: kind, Command: rtx.Command()}
	for _, mw := range slices.Backward(rtx.middleware) {
		if next = mw(hook, next); next == nil {
			panic("rotini: a middleware returned a nil HookFunc for " + kind.String())
		}
	}
	rtx.returned(next(ctx, rtx), !kind.teardown())
}

// invokeHook calls hook kind of h and returns its error, without recording it: an
// [HandlerE] adapted by [FromHandlerE] returns its own, a [Handler] nil.
func invokeHook(ctx context.Context, rtx *Context, h Handler, kind HookKind) error {
	if eh, ok := h.(handlerE); ok {
		switch kind {
		case HookCascadingPreRun:
			return eh.h.CascadingPreRun(ctx, rtx) //nolint:wrapcheck // the hook's own error, as it returned it
		case HookPreRun:
			return eh.h.PreRun(ctx, rtx) //nolint:wrapcheck // the hook's own error, as it returned it
		case HookRun:
			return eh.h.Run(ctx, rtx) //nolint:wrapcheck // the hook's own error, as it returned it
		case HookPostRun:
			return eh.h.PostRun(ctx, rtx) //nolint:wrapcheck // the hook's own error, as it returned it
		case HookCascadingPostRun:
			return eh.h.CascadingPostRun(ctx, rtx) //nolint:wrapcheck // the hook's own error, as it returned it
		}
		return nil
	}
	switch kind {
	case HookCascadingPreRun:
		h.CascadingPreRun(ctx, rtx)
	case HookPreRun:
		h.PreRun(ctx, rtx)
	case HookRun:
		h.Run(ctx, rtx)
	case HookPostRun:
		h.PostRun(ctx, rtx)
	case HookCascadingPostRun:
		h.CascadingPostRun(ctx, rtx)
	}
	return nil
}
