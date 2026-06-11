package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"sync/atomic"
	"syscall"
)

// trapSignals are the OS signals rotini's default handler traps when the caller does
// not supply its own context (see [Program.WithContext]). It is a package var, not a
// constant, so a white-box test can swap in a benign signal. The default action on the
// first signal is a graceful shutdown (cancel the run context + halt the lifecycle so
// teardown runs); a second signal forces exit with forceExitCode.
var trapSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// forceExitCode is the status a second signal exits with (128 + SIGINT), the
// conventional "interrupted" code.
const forceExitCode = 130

// signalExitCode maps a trapped signal to its conventional exit status (128 + signum).
func signalExitCode(s os.Signal) int {
	if s == syscall.SIGTERM {
		return 143 // 128 + 15
	}
	return forceExitCode // 128 + 2 (SIGINT); also the default for any other trapped signal
}

type Program struct {
	ctx              context.Context
	args             []string
	def              Definition
	handlers         any
	rtx              *Context                                           // pre-seeded registry; user Bind calls land here
	recoveredPanicFn func(ctx context.Context, rtx *Context, err error) // funnel for MustGet/panic failures; nil → defaultRecoveredPanicFn
	stdin            io.Reader
	stdout           io.Writer
	stderr           io.Writer
	exit             func(int)    // terminal action for Execute; defaults to os.Exit
	sigExit          atomic.Int32 // exit code a trapped signal requests; read by dispatch when the run context is canceled
}

// NewProgram wires a generated program's command tree (the rtg [Definition]) and
// its aggregate handler set (the rtg ProgramHandlers implementation) to the
// rotini runtime. handlers is any so the runtime need not import the generated
// framework package; dispatch resolves the per-command handlers from it at
// execution time, via the Handler names recorded in def.
func NewProgram(def Definition, handlers any) *Program {
	return &Program{
		args:     os.Args[1:],
		def:      def,
		handlers: handlers,
		rtx:      NewContext(),
		stdin:    os.Stdin,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		exit:     os.Exit,
	}
}

// WithStdin overrides the program's standard-input stream (defaults to os.Stdin) — the
// source a handler reads from via [Context.Stdin], and the source the Binder decodes a
// stdin channel from. Supply a reader (e.g. strings.NewReader) to drive an end-to-end test
// without piping real os.Stdin, or to embed rotini reading from somewhere else. A nil
// reader is ignored. It returns the receiver to chain.
func (p *Program) WithStdin(r io.Reader) *Program {
	if r != nil {
		p.stdin = r
	}
	return p
}

// WithStdout overrides the program's standard-output stream (defaults to os.Stdout) —
// the destination for the runtime's own writes (help/completion output, usage). A nil
// writer is ignored. It returns the receiver to chain.
func (p *Program) WithStdout(w io.Writer) *Program {
	if w != nil {
		p.stdout = w
	}
	return p
}

// WithStderr overrides the program's standard-error stream (defaults to os.Stderr) — the
// destination for the runtime's own diagnostics (dispatch errors, the default RecoveredPanicFn). A
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

// WithArgs overrides the argument vector (defaults to os.Args[1:]).
func (p *Program) WithArgs(args []string) *Program {
	if args != nil {
		p.args = args
	}
	return p
}

// WithContext sets the base [context.Context] threaded to every lifecycle hook (and to
// the RecoveredPanicFn funnel and any remote sub-command exec), so a caller — a server, a test,
// a parent process — can cancel or time-bound the whole run. A nil context is ignored. A
// handler observes cancellation by selecting on its hook's ctx.Done(); cancellation is
// cooperative (it cannot preempt a hook that ignores it), and teardown still runs.
//
// Supplying a context also opts OUT of rotini's default signal handling: the caller is
// declaring that it owns the run's lifecycle (including any Interrupt/SIGTERM trapping it
// wants, e.g. via [signal.NotifyContext] on the context it passes). With no WithContext,
// rotini installs its default trap — see [Program.Execute].
func (p *Program) WithContext(ctx context.Context) *Program {
	if ctx != nil {
		p.ctx = ctx
	}
	return p
}

// Bind registers a service on the program's registry under key, overwriting any
// prior binding, and returns the receiver so it chains with [Program.WithRecoveredPanicFn] and
// [program.WithArgs] before [program.Execute]. It is the dependency-injection
// seam: bind a real implementation in production or a double in tests, with the
// same handler code retrieving it via the typed [Get] / [MustGet]. Binding
// "parser" overrides the default [Parser].
func (p *Program) Bind(key string, value any) *Program {
	p.rtx.Bind(key, value)
	return p
}

// WithRecoveredPanicFn sets the funnel that handles any panic raised inside a hook (e.g. the
// [MustGet] on a missing service): the runtime recovers it during dispatch and calls
// fn(ctx, rtx, err), where fn decides the exit code by calling rtx.SignalExit. It is the
// single place to classify (errors.Is/errors.As), log (file, error-tracking service), and
// print errors in the CLI's own style. With no funnel set, the default prints the error to
// stderr and exits 1 (and the panic path floors a 0 to 1, so a funnel that forgets
// rtx.SignalExit still fails). It returns the receiver so it chains with [Program.Bind].
//
// The funnel sees only recovered panics — a deliberate [Context.SignalExit]/[Context.Exit]
// is not routed here. A hard Exit still does not bypass it, though: a panic already
// recovered is reported after the (skipped) teardown.
func (p *Program) WithRecoveredPanicFn(fn func(ctx context.Context, rtx *Context, err error)) *Program {
	p.recoveredPanicFn = fn
	return p
}

// Execute resolves the command, runs its lifecycle, and ends with the resulting status
// code via the program's exit action ([os.Exit] by default; override with
// [Program.WithExit] to capture the code or embed without terminating).
//
// Unless the caller supplied its own context ([Program.WithContext]), Execute installs
// rotini's default signal handling: the first os.Interrupt or syscall.SIGTERM cancels the
// run context AND halts the lifecycle like [Context.SignalExit] — forward progress stops and
// every begun teardown hook still runs — exiting 130 (SIGINT) or 143 (SIGTERM). A second
// signal forces exit immediately (code 130), skipping any remaining teardown, so a handler
// stuck ignoring the context can always be interrupted. A caller that wants different
// behavior supplies its own context and traps signals there.
func (p *Program) Execute() error {
	code, err := p.run(p.args)
	p.exit(code)
	return err
}

// run is the testable core of Execute: it resolves the invoked command (no eager
// flag parsing — that is the handler's opt-in via [Parse]), execs a remote
// sub-command if one was selected, otherwise builds the per-invocation [Rtx] and
// dispatches the lifecycle. It returns the process exit code instead of calling
// os.Exit. The only retained protocol intercept is the hidden __complete entry
// the generated shell scripts invoke.
func (p *Program) run(argv []string) (int, error) {
	if len(argv) > 0 && argv[0] == completeCommand {
		p.rtx.Stdin, p.rtx.Stdout, p.rtx.Stderr = p.stdin, p.stdout, p.stderr
		for _, c := range complete(p.def, argv[1:], p.handlers, p.rtx) {
			fmt.Fprintln(p.stdout, c)
		}
		return 0, nil
	}

	// The effective run context. When the caller supplied none (no WithContext), rotini
	// owns it: a cancelable context whose cancellation, plus a default signal trap,
	// drives graceful shutdown. When the caller supplied a context, it is used as-is and
	// rotini installs no trap — the caller owns signal handling.
	ctx := p.ctx
	trapped := ctx == nil
	if trapped {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, trapSignals...)
		defer signal.Stop(sigCh)

		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case s := <-sigCh: // first signal → graceful: cancel ctx; dispatch then halts the lifecycle
				p.sigExit.Store(int32(signalExitCode(s)))
				cancel()
			case <-done:
				return
			}
			select {
			case <-sigCh: // second signal → force exit, skipping any remaining teardown
				p.exit(forceExitCode)
			case <-done:
			}
		}()
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
	rtx.Stdin = p.stdin
	rtx.Stdout = p.stdout
	rtx.Stderr = p.stderr
	rtx.recoveredPanicFn = p.recoveredPanicFn
	if rtx.recoveredPanicFn == nil {
		rtx.recoveredPanicFn = p.defaultRecoveredPanicFn
	}
	return p.dispatch(ctx, chain, rtx, trapped)
}

// defaultRecoveredPanicFn is the RecoveredPanicFn funnel used when the program supplies none: it
// prints the error to stderr and fails with exit code 1.
func (p *Program) defaultRecoveredPanicFn(_ context.Context, rtx *Context, err error) {
	fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, err)
	rtx.SignalExit(1)
}

// dispatch resolves each command in the chain to its [CommandHandlers] (by the
// recorded Handler method name, via reflection on the aggregate handlers) and
// runs the lifecycle as a balanced, LIFO setup/teardown:
//
//   - Setup runs forward, root→leaf: CascadingPreRun for each command, then the
//     leaf's PreRun, then the leaf's Run (the innermost "work"). It halts the
//     moment a hook calls rtx.SignalExit/rtx.Exit or panics.
//   - Teardown runs in reverse for every hook that BEGAN: the leaf's PostRun (if
//     its PreRun began), then CascadingPostRun leaf→root for each command whose
//     CascadingPreRun began. Teardown otherwise runs to completion — a panic or
//     rtx.SignalExit inside a teardown hook neither aborts the rest nor displaces
//     the first failure or exit code; only a hard rtx.Exit skips what remains.
//
// rtx.SignalExit is a clean stop that still runs teardown; rtx.Exit is a hard stop that
// skips it. A panic anywhere (e.g. the [MustGet] on a missing service) is recovered, does
// not abort the remaining teardown, and is routed once — after all teardown — to the
// RecoveredPanicFn funnel, last.
//
// When trapped is set (the caller supplied no context, so rotini installed the default
// signal trap), a canceled run context is converted into a lifecycle [Context.SignalExit]
// between forward hooks: forward progress stops, teardown still runs, and the exit code
// is the conventional signal status. This conversion happens only on the dispatch
// goroutine, so rtx state stays single-writer; the signal goroutine only cancels ctx.
func (p *Program) dispatch(ctx context.Context, chain []ResolvedCommand, rtx *Context, trapped bool) (int, error) {
	hv := reflect.ValueOf(p.handlers)
	handlers := make([]CommandHandlers, len(chain))
	for i, f := range chain {
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			err := fmt.Errorf("no handler for command %q (missing method %q)", f.Name, f.Handler)
			fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, err)
			return 1, err
		}
		out := m.Call(nil)
		h, ok := out[0].Interface().(CommandHandlers)
		if !ok || h == nil {
			err := fmt.Errorf("handler %q does not implement CommandHandlers", f.Handler)
			fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, err)
			return 1, err
		}
		handlers[i] = h
	}
	leaf := handlers[len(handlers)-1]

	// failure is the first panic seen anywhere in the lifecycle. run wraps every
	// hook so a panic is recovered (keeping only the first) rather than unwinding —
	// this is what lets teardown still run and RecoveredPanicFn fire exactly once, last.
	var failure error
	run := func(hook func(context.Context, *Context)) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("%v", r)
			}
			if failure == nil {
				failure = err
			}
		}()
		hook(ctx, rtx)
	}

	// halt reports whether forward progress should stop — after a hook called
	// rtx.SignalExit/rtx.Exit or panicked, OR (when trapped) after a default-trapped signal
	// canceled the run context. In the signal case it records the conventional exit code via
	// rtx.SignalExit, so the cancellation becomes a clean lifecycle stop that still runs teardown.
	halt := func() bool {
		if trapped && !rtx.stopped && ctx.Err() != nil {
			rtx.SignalExit(int(p.sigExit.Load()))
		}
		return rtx.stopped || failure != nil
	}

	// Setup + work, forward — halting on rtx.SignalExit/rtx.Exit (rtx.stopped), a panic, or a signal.
	// began and preRan record how far setup got, so teardown unwinds only what began.
	began, preRan := 0, false
	func() {
		for i, h := range handlers {
			began = i + 1
			run(h.CascadingPreRun)
			if halt() {
				return
			}
		}
		preRan = true
		run(leaf.PreRun)
		if halt() {
			return
		}
		run(leaf.Run)
		halt() // record the signal exit code if Run returned because the context was canceled
	}()

	// Teardown, reverse — every begun hook's pair, to completion — unless a hard
	// [Context.Exit] asked to stop now, which skips any teardown still pending
	// (including one requested from within a teardown hook).
	if preRan && !rtx.exitNow {
		run(leaf.PostRun)
	}
	for i := began - 1; i >= 0 && !rtx.exitNow; i-- {
		run(handlers[i].CascadingPostRun)
	}

	// A panic is funneled to RecoveredPanicFn last, after teardown. The panic path is always
	// a failure: if the funnel left the code at 0, floor it to 1.
	if failure != nil {
		rtx.recoveredPanicFn(ctx, rtx, failure)
		if rtx.exitCode == 0 {
			rtx.exitCode = 1
		}
	}
	return rtx.exitCode, failure
}
