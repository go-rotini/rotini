package rotini

import (
	"context"
)

// The run phase turns the resolved command chain into ordered hook invocations. [DefaultLifecycle]
// is the default plan, which a program may wrap or replace. The engine enforces this contract for
// every plan, default or custom:
//
//	Phase     Order                    Hook                Halts forward on        Runs during unwind when
//	─────     ─────                    ────                ────────────────        ───────────────────────
//	setup     root → leaf              CascadingPreRun     Halt*/Exit, a panic,    —
//	setup     leaf                     PreRun              or a canceled context   —
//	work      leaf                     Run                 (e.g. a trapped signal) —
//	teardown  leaf                     PostRun             —                       its PreRun began
//	teardown  leaf → root              CascadingPostRun    —                       its CascadingPreRun began
//
// Teardown unwinds in reverse for exactly the steps whose forward hook began, and runs to
// completion: a panic or [Context.HaltWithCode] inside teardown neither aborts the rest nor
// displaces the first failure. Only [Context.Exit], or a panic under
// [Program.WithTeardownOnPanic](false), skips what
// remains. Recovered panics reach the reporter after all teardown. A custom lifecycle changes
// only the plan; halting, unwind, reporting and exit-code semantics are fixed.

// LifecycleStep pairs one forward hook with its teardown; a plan is a slice of them.
//
// Either half may be nil. A nil Undo is a step with no teardown, like the default plan's Run
// step; a nil Do is a teardown-only step. A step counts as begun when the engine reaches it,
// so a teardown-only step still unwinds.
type LifecycleStep struct {
	Name string                                  // diagnostic label, e.g. "cascading:app", "prerun:build", "run:build"
	Do   func(ctx context.Context, rtx *Context) // the forward hook
	Undo func(ctx context.Context, rtx *Context) // the paired teardown; nil for none
}

// Lifecycle is the run phase's planner: given the resolved chain and each command's [Handler],
// index-aligned, it returns the ordered steps the engine executes. Handlers are resolved
// before it is called. See [DefaultLifecycle].
//
// A plan built from scratch should wrap each hook in [AsCommand] so [Context.Command] and
// [Context.Inputs] anchor on the hook's own command; an unwrapped hook reports the invoked
// command, which is wrong for a cascading hook.
//
// [Program.WithMiddleware] wraps the hooks a plan takes from DefaultLifecycle's steps; a step
// function the plan writes itself runs without middleware.
type Lifecycle func(chain []Command, handlers []Handler) []LifecycleStep

// DefaultLifecycle is rotini's run-phase plan, exported for a custom [Lifecycle] to wrap: one
// CascadingPreRun/CascadingPostRun pair per command, root → leaf, then the leaf's
// PreRun/PostRun pair, then the leaf's Run with no teardown. Every hook is wrapped in
// [AsCommand].
func DefaultLifecycle(chain []Command, handlers []Handler) []LifecycleStep {
	steps := make([]LifecycleStep, 0, len(handlers)+2)
	for i, h := range handlers {
		steps = append(steps, LifecycleStep{
			Name: "cascading:" + chain[i].Name,
			Do:   hookOf(i, h, HookCascadingPreRun),
			Undo: hookOf(i, h, HookCascadingPostRun),
		})
	}
	leafIdx := len(handlers) - 1
	leaf := handlers[leafIdx]
	leafName := chain[len(chain)-1].Name
	steps = append(steps,
		LifecycleStep{Name: "prerun:" + leafName, Do: hookOf(leafIdx, leaf, HookPreRun), Undo: hookOf(leafIdx, leaf, HookPostRun)},
		LifecycleStep{Name: "run:" + leafName, Do: hookOf(leafIdx, leaf, HookRun)},
	)
	return steps
}

// hookOf is AsCommand(i, h.<kind>) as one closure: the method value AsCommand would wrap is
// a second allocation per hook.
func hookOf(i int, h Handler, kind HookKind) func(context.Context, *Context) {
	return func(ctx context.Context, rtx *Context) { callHook(ctx, rtx, i, h, kind) }
}

// callHook runs hook kind of h as the hook of chain frame i, as [AsCommand] labels it, inside
// the program's middleware when it has any.
func callHook(ctx context.Context, rtx *Context, i int, h Handler, kind HookKind) {
	prev := rtx.setFrame(i)
	defer rtx.setFrame(prev)
	if len(rtx.middleware) > 0 {
		callWrapped(ctx, rtx, h, kind)
		return
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
}

// defaultStep is step k of [DefaultLifecycle]'s plan for handlers, without building the plan:
// the frame and handler its hooks belong to, its forward hook, and its teardown, if any.
func defaultStep(handlers []Handler, k int) (frame int, h Handler, do, undo HookKind, hasUndo bool) {
	leaf := len(handlers) - 1
	switch {
	case k <= leaf:
		return k, handlers[k], HookCascadingPreRun, HookCascadingPostRun, true
	case k == leaf+1:
		return leaf, handlers[leaf], HookPreRun, HookPostRun, true
	default:
		return leaf, handlers[leaf], HookRun, 0, false
	}
}

// AsCommand labels a hook with the chain index of the command it belongs to, so that
// [Context.Command] and [Context.Inputs] inside it refer to that command. The previous label is
// restored when the hook returns. [DefaultLifecycle] wraps every hook it plans. An unlabeled
// hook reports the invoked command.
//
// A nil hook returns nil, which the engine skips.
func AsCommand(i int, hook func(context.Context, *Context)) func(context.Context, *Context) {
	if hook == nil {
		return nil
	}
	return func(ctx context.Context, rtx *Context) {
		prev := rtx.setFrame(i)
		defer rtx.setFrame(prev)
		hook(ctx, rtx)
	}
}

// hasUndo reports whether step k has a teardown: of the custom plan steps, or of the default
// plan for handlers.
func hasUndo(custom bool, steps []LifecycleStep, handlers []Handler, k int) bool {
	if custom {
		return steps[k].Undo != nil
	}
	return k <= len(handlers) // every defaultStep but the last, the leaf's Run
}
