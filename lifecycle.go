package rotini

import "context"

// The lifecycle seam: Execute's two phases — resolve (argv → invocation target) and run
// (target → ordered hook invocations) — each with an exported default a program may wrap or
// replace. The engine enforces this contract for every plan, default or custom:
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
// first failure, and only a hard [Context.Exit] skips what remains. A panic anywhere is
// recovered and routed once, after all teardown, to the reporter. A custom lifecycle changes
// only the plan — the halting, unwind, reporter and exit-code semantics are not overridable.

// Resolution is the outcome of the resolve phase: the invoked command path
// (root → leaf), or a plugin dispatch that replaces local execution.
type Resolution struct {
	// Chain is the resolved command path the run phase dispatches (when
	// Plugin is nil). It must be non-empty — the root command is always there.
	Chain []Command
	// Plugin, when non-nil, short-circuits local dispatch: the runtime execs this binary
	// instead, stdio passed through and context honored.
	Plugin *PluginDispatch
	// Argv is the vector the run phase exposes as [Context.Argv]. A resolver that rewrites
	// tokens returns the rewritten vector here so parsing agrees with its routing; nil keeps
	// the original argv.
	//
	// Contrast [PluginDispatch.Args], which stays Args: those are the arguments handed to a
	// CHILD process, not this invocation's own vector.
	Argv []string
}

// Resolver is the resolve phase: argv against the [Definition], deciding what this invocation
// targets. An error is routed through the reporter and fails the run. See [DefaultResolver].
//
// # The Definition is READ-ONLY
//
// It arrives by value, but a Definition is mostly slices — Commands, Flags, Arguments — and
// those are the program's own, not a copy. Writing through one (`def.Commands[0].Name = …`)
// edits the command tree itself, and the edit OUTLIVES the run: the next invocation of the same
// [Program] sees it, which for a REPL means every line after the first.
//
// This is the same convention [Context.CommandChain] states for the commands it hands a
// handler, and it is what lets one Program serve many runs without rebuilding its tree. A
// resolver that wants a different tree should build its own rather than edit the one it was
// shown.
type Resolver func(def Definition, argv []string) (Resolution, error)

// DefaultResolver is rotini's resolve phase, exported so a custom [Resolver] can wrap rather
// than re-derive it: descend sub-commands by name or alias, skip flags and their values, stop
// at the first positional, and divert to a plugin dispatch for declared plugins and discovered
// plugins. It is deliberately lenient — bad input is the opt-in Parser's concern — and never
// errors.
func DefaultResolver(def Definition, argv []string) (Resolution, error) {
	chain, plugin := resolveChain(def, argv)
	return Resolution{Chain: chain, Plugin: plugin, Argv: argv}, nil
}

// LifecycleStep pairs one forward hook with its teardown — the unit of the run phase's plan.
//
// Either half may be nil. A nil Undo is a step with no teardown, as the default plan's Run step
// is; a nil Do is a step with no forward work, which is how a hand-built plan registers a
// teardown that pairs with nothing. A step counts as begun either way, so a teardown-only step
// still unwinds.
type LifecycleStep struct {
	Name string                                  // diagnostic label, e.g. "cascading:app", "prerun:build", "run:build"
	Do   func(ctx context.Context, rtx *Context) // the forward hook
	Undo func(ctx context.Context, rtx *Context) // the paired teardown; nil for none
}

// Lifecycle is the run phase's planner: given the resolved chain and each command's [Handler],
// index-aligned, it returns the ordered step plan the engine executes. It orders and pairs the
// declared hooks; the handler wiring rules hold before it is consulted. See [DefaultLifecycle].
//
// A plan built by wrapping [DefaultLifecycle] needs nothing further. One built from scratch
// should wrap each hook in [AsCommand] so [Context.Command] — and therefore [Context.Inputs]'s
// anchor — knows which command the hook belongs to; an unlabeled hook reports the invoked
// command, which is right for its own hooks and wrong for a cascading one.
type Lifecycle func(chain []Command, handlers []Handler) []LifecycleStep

// DefaultLifecycle is rotini's run-phase plan, exported so a custom [Lifecycle] can wrap it:
// one CascadingPreRun/CascadingPostRun pair per command, root → leaf, then the leaf's
// PreRun/PostRun pair, then the leaf's Run with no teardown. With the engine's reverse unwind
// this yields exactly the contract table above.
//
// Every hook is wrapped in [AsCommand], which is what lets a cascading hook know which command
// it belongs to — see [Context.Command] — and what lets [Context.Inputs] anchor an inputs
// struct on that command instead of guessing from its field count.
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
// [Context.Command] can answer "which command am I?" inside it and [Context.Inputs] can anchor
// an inputs struct on that command. [DefaultLifecycle] wraps every hook it plans; a custom
// [Lifecycle] that wraps DefaultLifecycle inherits this and needs to do nothing.
//
// A custom Lifecycle that builds steps from scratch should wrap its own hooks the same way. One
// that does not is not broken: an unlabeled hook reports the INVOKED command, which is what
// every non-cascading hook wants. The cost of skipping it falls only on a cascading hook that
// collects its own inputs.
//
// The previous command is restored on return, so nesting — a hook that drives another hook — does
// not leave the Context describing the wrong command.
//
// A nil hook yields a nil step half, which the engine skips.
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
