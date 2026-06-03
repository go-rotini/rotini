package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"syscall"
)

type Program struct {
	ctx      context.Context
	signals  []os.Signal // when set, the run's context cancels on one of these (graceful shutdown)
	args     []string
	def      Definition
	handlers any
	rtx      *Context                                           // pre-seeded registry; user Bind calls land here
	onError  func(ctx context.Context, rtx *Context, err error) // funnel for MustGet/panic failures; nil → defaultOnError
	stdout   io.Writer
	stderr   io.Writer
}

// NewProgram wires a generated program's command tree (the rtg [Definition]) and
// its aggregate handler set (the rtg ProgramHandlers implementation) to the
// rotini runtime. handlers is any so the runtime need not import the generated
// framework package; dispatch resolves the per-command handlers from it at
// execution time, via the Handler names recorded in def.
func NewProgram(def Definition, handlers any) *Program {
	return &Program{
		ctx:      context.Background(),
		args:     os.Args[1:],
		def:      def,
		handlers: handlers,
		rtx:      NewContext(),
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
}

// WithArguments overrides the argument vector (defaults to os.Args[1:]).
func (p *Program) WithArguments(args []string) *Program {
	if args != nil {
		p.args = args
	}
	return p
}

// WithContext sets the base [context.Context] threaded to every lifecycle hook (and to
// the OnError funnel and any remote sub-command exec), so a caller — a server, a test,
// a parent process — can cancel or time-bound the whole run. Defaults to
// [context.Background]. A nil context is ignored. A handler observes cancellation by
// selecting on its hook's ctx.Done(); cancellation is cooperative (it cannot preempt a
// hook that ignores it), and teardown still runs.
func (p *Program) WithContext(ctx context.Context) *Program {
	if ctx != nil {
		p.ctx = ctx
	}
	return p
}

// WithSignals makes the run's context cancel when one of the given OS signals arrives —
// the standard graceful-shutdown wiring. With no arguments it traps SIGINT and SIGTERM.
// On the first signal the context is canceled (so cooperative handlers stop and
// teardown still runs); a second signal restores the default OS behavior (force-quit),
// via [signal.NotifyContext]. It composes with [Program.WithContext] (the signal
// context derives from the base). It is independent of the rtk Signals service — use
// one or the other for a given signal unless you deliberately want both.
func (p *Program) WithSignals(sigs ...os.Signal) *Program {
	if len(sigs) == 0 {
		sigs = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}
	p.signals = sigs
	return p
}

// Bind registers a service on the program's registry under key, overwriting any
// prior binding, and returns the receiver so it chains with [program.OnError] and
// [program.WithArguments] before [program.Execute]. It is the dependency-injection
// seam: bind a real implementation in production or a double in tests, with the
// same handler code retrieving it via [Rtx.Get] (or the rtk package's typed
// Get/MustGet). Binding "parser" overrides the default parser the rtk package uses.
func (p *Program) Bind(key string, value any) *Program {
	p.rtx.Bind(key, value)
	return p
}

// OnError sets the funnel that handles any panic raised inside a hook (e.g. the
// rtk package's MustGet on a missing service): the runtime recovers it during
// dispatch and calls fn(ctx, rtx, err), where fn decides the exit code by calling
// rtx.Exit. It is the single place to classify (errors.Is/errors.As), log (file,
// error-tracking service), and print errors in the CLI's own style. With no funnel
// set, the default prints the error to stderr and exits 1 (and the panic path
// floors a 0 to 1, so a funnel that forgets rtx.Exit still fails). OnError returns
// the receiver so it chains with [program.Bind].
func (p *Program) OnError(fn func(ctx context.Context, rtx *Context, err error)) *Program {
	p.onError = fn
	return p
}

// Execute resolves the command, runs its lifecycle, and exits the process with
// the resulting status code.
func (p *Program) Execute() {
	os.Exit(p.run(p.args))
}

// run is the testable core of Execute: it resolves the invoked command (no eager
// flag parsing — that is the handler's opt-in via [Parse]), execs a remote
// sub-command if one was selected, otherwise builds the per-invocation [Rtx] and
// dispatches the lifecycle. It returns the process exit code instead of calling
// os.Exit. The only retained protocol intercept is the hidden __complete entry
// the generated shell scripts invoke.
func (p *Program) run(argv []string) int {
	if len(argv) > 0 && argv[0] == completeCommand {
		for _, c := range complete(p.def, argv[1:]) {
			fmt.Fprintln(p.stdout, c)
		}
		return 0
	}

	// The effective run context: the base context (WithContext, else Background),
	// made cancelable on the configured signals (WithSignals) for graceful shutdown.
	ctx := p.ctx
	if len(p.signals) > 0 {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(p.ctx, p.signals...)
		defer stop()
	}

	chain, remote := resolveChain(p.def, argv)
	if remote != nil {
		return p.execRemote(ctx, remote)
	}

	rtx := p.rtx
	if rtx == nil {
		rtx = NewContext()
	}
	rtx.args = argv
	rtx.chain = chain
	rtx.onError = p.onError
	if rtx.onError == nil {
		rtx.onError = p.defaultOnError
	}
	return p.dispatch(ctx, chain, rtx)
}

// defaultOnError is the OnError funnel used when the program supplies none: it
// prints the error to stderr and fails with exit code 1.
func (p *Program) defaultOnError(_ context.Context, rtx *Context, err error) {
	fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, err)
	rtx.Exit(1)
}

// dispatch resolves each command in the chain to its [CommandHandlers] (by the
// recorded Handler method name, via reflection on the aggregate handlers) and
// runs the lifecycle as a balanced, LIFO setup/teardown:
//
//   - Setup runs forward, root→leaf: CascadingPreRun for each command, then the
//     leaf's PreRun, then the leaf's Run (the innermost "work"). It halts the
//     moment a hook calls rtx.Exit or panics.
//   - Teardown runs in reverse for every hook that BEGAN: the leaf's PostRun (if
//     its PreRun began), then CascadingPostRun leaf→root for each command whose
//     CascadingPreRun began. Teardown always runs to completion — a panic or
//     rtx.Exit inside a teardown hook neither aborts the rest nor displaces the
//     first failure or exit code.
//
// rtx.Exit is a clean stop. A panic anywhere (e.g. the rtk package's MustGet on a
// missing service) is recovered, does not abort the remaining teardown, and is
// routed once — after all teardown — to the OnError funnel, last. See
// .docs/ROTINI_RTX_EXIT.md.
func (p *Program) dispatch(ctx context.Context, chain []ResolvedCommand, rtx *Context) (code int) {
	hv := reflect.ValueOf(p.handlers)
	handlers := make([]CommandHandlers, len(chain))
	for i, f := range chain {
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			fmt.Fprintf(p.stderr, "%s: no handler for command %q (missing method %q)\n", p.def.Name, f.Name, f.Handler)
			return 1
		}
		out := m.Call(nil)
		h, ok := out[0].Interface().(CommandHandlers)
		if !ok || h == nil {
			fmt.Fprintf(p.stderr, "%s: handler %q does not implement CommandHandlers\n", p.def.Name, f.Handler)
			return 1
		}
		handlers[i] = h
	}
	leaf := handlers[len(handlers)-1]

	// rtx.ExitNow aborts the whole chain at once: it panics a sentinel that run
	// re-throws (bypassing teardown) so it lands here, returning its code as-is.
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitNow); ok {
				code = rtx.exitCode
				return
			}
			panic(r)
		}
	}()

	// failure is the first panic seen anywhere in the lifecycle. run wraps every
	// hook so a panic is recovered (keeping only the first) rather than unwinding —
	// this is what lets teardown still run and OnError fire exactly once, last. The
	// ExitNow sentinel is the exception: run re-throws it to abort everything.
	var failure error
	run := func(hook func(context.Context, *Context)) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if _, ok := r.(exitNow); ok {
				panic(r) // ExitNow: skip teardown and OnError
			}
			if failure == nil {
				if err, ok := r.(error); ok {
					failure = err
				} else {
					failure = fmt.Errorf("%v", r)
				}
			}
		}()
		hook(ctx, rtx)
	}

	// Setup + work, forward — halting on rtx.Exit (rtx.stopped) or a panic. began
	// and preRan record how far setup got, so teardown unwinds only what began.
	began, preRan := 0, false
	func() {
		for i, h := range handlers {
			began = i + 1
			if run(h.CascadingPreRun); rtx.stopped || failure != nil {
				return
			}
		}
		preRan = true
		if run(leaf.PreRun); rtx.stopped || failure != nil {
			return
		}
		run(leaf.Run)
	}()

	// Teardown, reverse — every begun hook's pair, always to completion.
	if preRan {
		run(leaf.PostRun)
	}
	for i := began - 1; i >= 0; i-- {
		run(handlers[i].CascadingPostRun)
	}

	// A panic is funneled to OnError last, after teardown. The panic path is always
	// a failure: if the funnel left the code at 0, floor it to 1.
	if failure != nil {
		rtx.onError(ctx, rtx, failure)
		if rtx.exitCode == 0 {
			rtx.exitCode = 1
		}
	}
	return rtx.exitCode
}
