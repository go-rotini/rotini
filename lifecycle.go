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
// completion: a panic or HaltWithCode inside teardown neither aborts the rest nor displaces the
// first failure. Only [Context.Exit], or a panic under WithTeardownOnPanic(false), skips what
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
			Do:   AsCommand(i, h.CascadingPreRun),
			Undo: AsCommand(i, h.CascadingPostRun),
		})
	}
	leafIdx := len(handlers) - 1
	leaf := handlers[leafIdx]
	leafName := chain[len(chain)-1].Name
	steps = append(steps,
		LifecycleStep{Name: "prerun:" + leafName, Do: AsCommand(leafIdx, leaf.PreRun), Undo: AsCommand(leafIdx, leaf.PostRun)},
		LifecycleStep{Name: "run:" + leafName, Do: AsCommand(leafIdx, leaf.Run)},
	)
	return steps
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
