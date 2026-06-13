package rotini

import "context"

// This file is the lifecycle seam: the two phases of Execute — *resolve*
// (argv → invocation target) and *run* (target → ordered hook invocations) —
// each with an exported default a program may wrap or replace
// ([Program.WithResolver], [Program.WithLifecycle]).
//
// The run contract, pinned (this table IS the product; the engine below
// enforces it for every plan, default or custom):
//
//	Phase     Order                    Hook                Halts forward on        Runs during unwind when
//	─────     ─────                    ────                ────────────────        ───────────────────────
//	setup     root → leaf              CascadingPreRun     SignalExit/Exit,        —
//	setup     leaf                     PreRun              a panic, or a           —
//	work      leaf                     Run                 trapped signal          —
//	teardown  leaf                     PostRun             —                       its PreRun began
//	teardown  leaf → root              CascadingPostRun    —                       its CascadingPreRun began
//
// Teardown unwinds in reverse for exactly the steps whose forward hook BEGAN,
// and runs to completion — a panic or SignalExit inside teardown neither
// aborts the rest nor displaces the first failure/exit code; only a hard
// [Context.Exit] skips what remains. A panic anywhere is recovered and routed
// once, after all teardown, to the OnError funnel (exit floored
// to 1). A custom lifecycle changes only the *plan* — which hooks, in what
// pairing and order; the halting, balanced-unwind, panic-funnel, and
// exit-code semantics above are rotini's and not overridable.

// Resolution is the outcome of the resolve phase: the invoked command path
// (root → leaf), or a remote dispatch that replaces local execution.
type Resolution struct {
	// Chain is the resolved command path the run phase dispatches (when
	// Remote is nil). It must be non-empty — the root frame is always there.
	Chain []ResolvedCommand
	// Remote, when non-nil, short-circuits local dispatch: the runtime execs
	// this remote/plugin binary instead (stdio passed through, context
	// honored), exactly as for declared remote_commands.
	Remote *RemoteDispatch
	// Args is the argument vector the run phase exposes as [Context.Args] —
	// what the opt-in Parser/Binder later read. A resolver that rewrites
	// tokens (say, an alias) returns the rewritten vector here so parsing
	// agrees with its routing; nil keeps the original argv.
	Args []string
}

// Resolver is the resolve phase: argv against the Definition, deciding what
// this invocation targets. An error is routed through the OnError
// funnel and fails the run (exit floored to 1). See [DefaultResolver].
type Resolver func(def Definition, argv []string) (Resolution, error)

// DefaultResolver is rotini's resolve phase, exported so a custom [Resolver]
// can wrap it rather than re-derive it: descend sub-commands by name/alias,
// skip flags and their values, stop at the first positional (or "--", or a
// dash-prefixed value), and divert to a remote dispatch for declared
// remote_commands / discovered plugins. It is intentionally lenient — unknown
// flags and bad input are the opt-in Parser's concern, not routing's — and it
// never errors.
func DefaultResolver(def Definition, argv []string) (Resolution, error) {
	chain, remote := resolveChain(def, argv)
	return Resolution{Chain: chain, Remote: remote, Args: argv}, nil
}

// LifecycleStep pairs one forward hook with its teardown — the unit of the run
// phase's execution plan. The engine runs Do hooks forward, halting per the
// contract above, then runs the Undo of every step whose Do began, in reverse.
// A nil Undo is a step with no teardown (the default plan's Run step).
type LifecycleStep struct {
	Name string                                  // diagnostic label, e.g. "cascading:app", "prerun:build", "run:build"
	Do   func(ctx context.Context, rtx *Context) // the forward hook
	Undo func(ctx context.Context, rtx *Context) // the paired teardown; nil for none
}

// Lifecycle is the run phase's planner: given the resolved chain and each
// frame's [CommandHandlers] (index-aligned with chain), it returns the ordered
// step plan the engine executes. It orders and pairs the declared hooks — it
// cannot change the engine's halting/unwind/funnel semantics, and the handler
// wiring rules (every frame's handler resolved from the generated set) hold
// before it is consulted. See [DefaultLifecycle].
type Lifecycle func(chain []ResolvedCommand, handlers []CommandHandlers) []LifecycleStep

// DefaultLifecycle is rotini's run-phase plan, exported so a custom
// [Lifecycle] can wrap or reshape it: one CascadingPreRun/CascadingPostRun
// pair per frame (root → leaf), then the leaf's PreRun/PostRun pair, then the
// leaf's Run with no teardown. With the engine's reverse unwind this yields
// exactly the contract table above.
func DefaultLifecycle(chain []ResolvedCommand, handlers []CommandHandlers) []LifecycleStep {
	steps := make([]LifecycleStep, 0, len(handlers)+2)
	for i, h := range handlers {
		steps = append(steps, LifecycleStep{Name: "cascading:" + chain[i].Name, Do: h.CascadingPreRun, Undo: h.CascadingPostRun})
	}
	leaf := handlers[len(handlers)-1]
	leafName := chain[len(chain)-1].Name
	steps = append(steps,
		LifecycleStep{Name: "prerun:" + leafName, Do: leaf.PreRun, Undo: leaf.PostRun},
		LifecycleStep{Name: "run:" + leafName, Do: leaf.Run},
	)
	return steps
}
