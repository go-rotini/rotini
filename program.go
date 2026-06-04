package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"
)

type Program struct {
	ctx      context.Context
	signals  []os.Signal // when set, the run's context cancels on one of these (graceful shutdown)
	args     []string
	def      Definition
	handlers any
	rtx      *Context                                           // pre-seeded registry; user Bind calls land here
	onError  func(ctx context.Context, rtx *Context, err error) // funnel for MustGet/panic failures; nil → defaultOnError
	observer Observer                                           // opt-in control-flow observation seam; nil → emit nothing
	stdout   io.Writer
	stderr   io.Writer
	exit     func(int) // terminal action for Execute; defaults to os.Exit
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
		exit:     os.Exit,
	}
}

// WithStdout overrides the program's standard-output stream (defaults to os.Stdout) —
// the destination for the runtime's own writes (help/completion output, usage). A nil
// writer is ignored. Pair it with binding an IO/Printer service wired to the same writer
// to capture everything a run emits (e.g. in a test). It returns the receiver to chain.
func (p *Program) WithStdout(w io.Writer) *Program {
	if w != nil {
		p.stdout = w
	}
	return p
}

// WithStderr overrides the program's standard-error stream (defaults to os.Stderr) — the
// destination for the runtime's own diagnostics (dispatch errors, the default OnError). A
// nil writer is ignored. It returns the receiver to chain.
func (p *Program) WithStderr(w io.Writer) *Program {
	if w != nil {
		p.stderr = w
	}
	return p
}

// WithExit overrides the terminal action [Program.Execute] takes with the resolved exit
// code — defaults to [os.Exit]. Supply a recording function to capture the code without
// terminating the process (a test), or a custom exit when embedding rotini in a larger
// program/REPL. If the supplied function returns, Execute returns to its caller. A nil
// function is ignored. It returns the receiver to chain.
func (p *Program) WithExit(fn func(int)) *Program {
	if fn != nil {
		p.exit = fn
	}
	return p
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

// WithObserver sets the opt-in observability seam: fn receives an [Event] at each of the
// runtime's control-flow transitions — the command resolving, each lifecycle hook starting
// and finishing (with its wall-clock and any panic), a remote/plugin exec, and the run's
// exit code settling. It is the single place to trace or meter the framework-internal flow a
// handler cannot see from its own hooks. rotini emits DATA only and never logs, formats, or
// adds a flag itself (Pillar 1); with no observer set the runtime emits nothing and pays no
// cost. A nil fn clears any prior observer. It returns the receiver so it chains with
// [Program.OnError] and [Program.Bind]. See [Observer].
func (p *Program) WithObserver(fn Observer) *Program {
	p.observer = fn
	return p
}

// emit delivers an Event to the observer when one is set; a no-op otherwise (the common
// case), so observation costs nothing unless opted into.
func (p *Program) emit(e Event) {
	if p.observer != nil {
		p.observer(e)
	}
}

// Execute resolves the command, runs its lifecycle, and ends with the resulting status
// code via the program's exit action ([os.Exit] by default; override with
// [Program.WithExit] to capture the code or embed without terminating).
func (p *Program) Execute() {
	p.exit(p.run(p.args))
}

// run is the testable core of Execute: it resolves the invoked command (no eager
// flag parsing — that is the handler's opt-in via [Parse]), execs a remote
// sub-command if one was selected, otherwise builds the per-invocation [Rtx] and
// dispatches the lifecycle. It returns the process exit code instead of calling
// os.Exit. The only retained protocol intercept is the hidden __complete entry
// the generated shell scripts invoke.
func (p *Program) run(argv []string) int {
	if len(argv) > 0 && argv[0] == completeCommand {
		for _, c := range complete(p.def, argv[1:], p.handlers, p.rtx) {
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
	if p.observer != nil {
		p.observer(Event{Phase: PhaseResolved, Command: chain[len(chain)-1].Name, Path: chainPath(chain)})
	}
	if remote != nil {
		p.emit(Event{Phase: PhaseRemoteExec, Command: remote.def.Name})
		code := p.execRemote(ctx, remote)
		p.emit(Event{Phase: PhaseExit, Command: remote.def.Name, Code: code})
		return code
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
	code := p.dispatch(ctx, chain, rtx)
	p.emit(Event{Phase: PhaseExit, Command: chain[len(chain)-1].Name, Code: code})
	return code
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
	//
	// run also brackets each hook with PhaseHookStart/PhaseHookEnd observation (when an
	// observer is set), reporting the per-hook wall-clock and any panic — independent of
	// the first-failure bookkeeping the funnel uses.
	leafName := chain[len(chain)-1].Name
	var failure error
	run := func(cmd, name string, hook func(context.Context, *Context)) {
		var start time.Time
		if p.observer != nil {
			p.observer(Event{Phase: PhaseHookStart, Command: cmd, Hook: name})
			start = time.Now()
		}
		var hookErr error
		defer func() {
			if p.observer != nil {
				p.observer(Event{Phase: PhaseHookEnd, Command: cmd, Hook: name, Duration: time.Since(start), Err: hookErr})
			}
		}()
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if _, ok := r.(exitNow); ok {
				panic(r) // ExitNow: skip teardown and OnError (the HookEnd defer still runs)
			}
			if err, ok := r.(error); ok {
				hookErr = err
			} else {
				hookErr = fmt.Errorf("%v", r)
			}
			if failure == nil {
				failure = hookErr
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
			if run(chain[i].Name, "CascadingPreRun", h.CascadingPreRun); rtx.stopped || failure != nil {
				return
			}
		}
		preRan = true
		if run(leafName, "PreRun", leaf.PreRun); rtx.stopped || failure != nil {
			return
		}
		run(leafName, "Run", leaf.Run)
	}()

	// Teardown, reverse — every begun hook's pair, always to completion.
	if preRan {
		run(leafName, "PostRun", leaf.PostRun)
	}
	for i := began - 1; i >= 0; i-- {
		run(chain[i].Name, "CascadingPostRun", handlers[i].CascadingPostRun)
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
