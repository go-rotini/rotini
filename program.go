package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"runtime/debug"
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
	ctx       context.Context
	args      []string
	def       Definition
	handlers  any
	rtx       *Context                                           // pre-seeded registry; user Bind calls land here
	onErrorFn func(ctx context.Context, rtx *Context, err error) // funnel for framework diagnostics + recovered panics; nil → defaultOnError
	resolver  Resolver                                           // resolve phase override; nil → DefaultResolver
	lifecycle Lifecycle                                          // run-phase plan override; nil → DefaultLifecycle
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	exit      func(int)    // terminal action for Execute; defaults to os.Exit
	sigExit   atomic.Int32 // exit code a trapped signal requests; read by dispatch when the run context is canceled
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
		rtx:      newContext(),
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

// WithArgs overrides the argument vector (defaults to os.Args[1:]).
func (p *Program) WithArgs(args []string) *Program {
	if args != nil {
		p.args = args
	}
	return p
}

// WithContext sets the base [context.Context] threaded to every lifecycle hook (and to
// the OnError funnel and any remote sub-command exec), so a caller — a server, a test,
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
// prior binding, and returns the receiver so it chains with [Program.WithOnErrorFn] and
// [program.WithArgs] before [program.Execute]. It is the dependency-injection
// seam: bind a real implementation in production or a double in tests, with the
// same handler code retrieving it via the typed [Get] / [MustGet]. Binding
// "parser" overrides the default [Parser].
func (p *Program) Bind(key string, value any) *Program {
	p.rtx.Bind(key, value)
	return p
}

// WithOnErrorFn sets the program's OnError funnel: the SINGLE place an
// invocation's errors are reported. The runtime calls fn(ctx, rtx, err) once,
// at most once per run, whenever the run recorded ANY error — and fn decides
// the exit code (rtx.SignalExit), classifies (errors.Is/errors.As,
// [CategoryOf]), logs, and prints in the CLI's own style. With no funnel set,
// the default prints one clean line per recorded error to stderr (program-name
// prefixed) and exits by the most severe category present — [ExitInternal]
// (70) if any error is [CategoryInternal], else [ExitUsage] (2) if any is
// [CategoryUsage], else 1. It returns the receiver so it chains with
// [Program.Bind].
//
// Three error classes flow into this one funnel, all via the same recorded list
// ([Context.Errors]):
//
//   - Handler errors a handler chose to surface with [Context.RecordErr] — the
//     ordinary path. A handler records one or more errors and stops with
//     [Context.SignalExit] (graceful, teardown runs) or [Context.Exit] (skip
//     teardown); the funnel fires after the lifecycle settles. Recording
//     without an explicit exit still fires it.
//   - Framework diagnostics, BEFORE any hook has run: a custom resolver's error
//     (or an empty chain), a Definition↔handlers wiring mismatch, a remote
//     dispatch that cannot be carried out (binary not found, timeout, spawn
//     failure — a plugin's own non-zero exit is NOT routed; it already spoke
//     for itself). Pre-classified for [CategoryOf]: wiring-class is
//     [CategoryInternal] (a resolver error keeps its own tag), a DISCOVERED
//     plugin's missing binary is the user's typo and [CategoryUsage], a
//     declared remote's timeout is deliberately [CategoryNone].
//   - A recovered panic from any hook (e.g. [MustGet] on a missing service),
//     AFTER teardown unwinds — recorded as a [*PanicError] carrying the
//     goroutine stack (errors.As reaches it; Unwrap preserves the panicked
//     error's sentinels and [CategoryOf] tag).
//
// The err argument is the [errors.Join] of every recorded error, so one
// [CategoryOf] / errors.Is / errors.As call covers the whole set; fn can also
// drain [Context.Errors] to format each individually. A run that recorded any
// error never exits 0 — the code floors to 1 even if fn forgets rtx.SignalExit.
// A clean run (nothing recorded) never invokes fn. fn must not assume
// handler-initialized state — on the framework paths nothing has run.
func (p *Program) WithOnErrorFn(fn func(ctx context.Context, rtx *Context, err error)) *Program {
	p.onErrorFn = fn
	return p
}

// PanicError is how a panic recovered from a lifecycle hook reaches the
// [Program.WithOnErrorFn] funnel: Value is the value passed to panic, Stack is the
// goroutine stack captured at the recovery point (runtime/debug.Stack) — the
// information the recover would otherwise discard. Error renders Value alone, so
// default output stays one line; a funnel that wants the stack asks with errors.As.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("%v", e.Value) }

// Unwrap exposes a panicked error value so errors.Is/errors.As and [CategoryOf]
// see through the wrapping; it is nil for non-error panic values.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// WithResolver overrides the resolve phase — argv to invocation target (the
// command chain to dispatch, or a remote dispatch, plus the argv the parsers
// later see). Wrap [DefaultResolver] rather than re-deriving it: a resolver
// that rewrites tokens (an alias, a shorthand expansion) rewrites argv, hands
// it to the default, and returns the result — routing and parsing then agree.
// A resolver error is routed through the OnError funnel and fails the
// run. The hidden __complete protocol intercept runs before resolution, and
// completion candidates walk the Definition — a resolver-only alias is
// dispatchable but not completable (declare real aliases in the spec for
// that). A nil resolver is ignored. It returns the receiver to chain.
func (p *Program) WithResolver(fn Resolver) *Program {
	if fn != nil {
		p.resolver = fn
	}
	return p
}

// WithLifecycle overrides the run phase's PLAN — which declared hooks run, in
// what pairing and order (see [Lifecycle] and the contract in [DefaultLifecycle]).
// The execution semantics around the plan (halt on SignalExit/Exit/panic/
// signal, balanced reverse unwind of begun steps, teardown-to-completion, the
// panic funnel, exit codes) are rotini's and stay fixed. Wrap
// [DefaultLifecycle] to adjust rather than re-derive. A nil lifecycle is
// ignored. It returns the receiver to chain.
func (p *Program) WithLifecycle(fn Lifecycle) *Program {
	if fn != nil {
		p.lifecycle = fn
	}
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

	resolve := p.resolver
	if resolve == nil {
		resolve = DefaultResolver
	}
	rtx := p.rtx
	if rtx == nil {
		rtx = newContext()
	}
	rtx.Stdin = p.stdin
	rtx.Stdout = p.stdout
	rtx.Stderr = p.stderr
	rtx.onErrorFn = p.onErrorFn
	if rtx.onErrorFn == nil {
		rtx.onErrorFn = p.defaultOnError
	}

	res, err := resolve(p.def, argv)
	if err != nil {
		return p.wiringFailure(ctx, rtx, internalUnlessTagged(fmt.Errorf("resolve: %w", err)))
	}
	if res.Remote != nil {
		return p.execRemote(ctx, rtx, res.Remote)
	}
	if len(res.Chain) == 0 {
		return p.wiringFailure(ctx, rtx, InternalError(fmt.Errorf("resolver returned an empty chain — the root frame is always resolvable")))
	}

	rtx.Args = argv
	if res.Args != nil {
		rtx.Args = res.Args
	}
	rtx.chain = res.Chain
	return p.dispatch(ctx, res.Chain, rtx, trapped)
}

// internalUnlessTagged tags err [CategoryInternal] unless its producer already
// categorized it — a custom resolver may legitimately raise a usage error (the
// user's token was the problem), and that classification must survive.
func internalUnlessTagged(err error) error {
	if CategoryOf(err) != CategoryNone {
		return err
	}
	return InternalError(err)
}

// wiringFailure routes a Definition↔handlers mismatch through the funnel and
// floors the exit code to 1 (a wiring failure is always a failure, even when a
// custom funnel forgets rtx.SignalExit).
func (p *Program) wiringFailure(ctx context.Context, rtx *Context, err error) (int, error) {
	// Record it so OnError can drain rtx.Errors() uniformly on the framework
	// paths too (resolve happens before any lifecycle, so this is the only
	// recorded error). The err argument is the join, matching the dispatch path.
	rtx.RecordErr(err)
	joined := errors.Join(rtx.Errors()...)
	rtx.onErrorFn(ctx, rtx, joined)
	if rtx.exitCode == 0 {
		rtx.exitCode = 1
	}
	return rtx.exitCode, joined
}

// defaultOnError is the OnError funnel used when the program supplies none: it
// prints one clean line per recorded error to stderr (program-name prefixed)
// and exits by the most severe category present (see [defaultExitCode]). Each
// rotini error type renders a single non-leaky line — a [*PanicError] prints
// its value, never the stack — so the default needs no per-type formatting; a
// CLI that wants richer reporting (or the Suggestor's "did you mean") supplies
// its own via [Program.WithOnErrorFn].
func (p *Program) defaultOnError(_ context.Context, rtx *Context, _ error) {
	errs := rtx.Errors()
	for _, e := range errs {
		fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, e)
	}
	rtx.SignalExit(defaultExitCode(errs))
}

// defaultExitCode maps a recorded error set onto the conventional exit code the
// default OnError uses: the most severe category present wins. Any
// [CategoryInternal] error yields [ExitInternal] (a program bug outranks bad
// input), else any [CategoryUsage] yields [ExitUsage], else 1 — an
// unclassified failure is still a failure (edge 3: a recorded run never exits
// 0). It is only ever called with a non-empty set.
func defaultExitCode(errs []error) int {
	code := 1
	for _, e := range errs {
		switch CategoryOf(e) {
		case CategoryInternal:
			return ExitInternal
		case CategoryUsage:
			code = ExitUsage
		}
	}
	return code
}

// dispatch resolves each command in the chain to its [CommandHandlers] (by the
// recorded Handler method name, via reflection on the aggregate handlers),
// asks the lifecycle planner for the step plan (the default plan reproduces
// the contract table in lifecycle.go), and executes it as a balanced, LIFO
// setup/teardown:
//
//   - Forward: each step's Do, in plan order, halting the moment a hook calls
//     rtx.SignalExit/rtx.Exit or panics (or a trapped signal cancels ctx).
//   - Unwind: the Undo of every step whose Do BEGAN, in reverse, to
//     completion — a panic or rtx.SignalExit inside an Undo neither aborts the
//     rest nor displaces the first failure or exit code; only a hard rtx.Exit
//     skips what remains.
//
// rtx.SignalExit is a clean stop that still runs teardown; rtx.Exit is a hard stop that
// skips it. A panic anywhere (e.g. the [MustGet] on a missing service) is recovered, does
// not abort the remaining teardown, and is routed once — after all teardown — to the
// OnError funnel, last.
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
		// A wiring failure (generated Definition and handler set out of sync) is
		// routed through the OnError funnel — the single sink for every
		// framework diagnostic — so a custom funnel sees it too; the default
		// funnel prints it to stderr and exits 1.
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			return p.wiringFailure(ctx, rtx, InternalError(fmt.Errorf("no handler for command %q (missing method %q)", f.Name, f.Handler)))
		}
		out := m.Call(nil)
		h, ok := out[0].Interface().(CommandHandlers)
		if !ok || h == nil {
			return p.wiringFailure(ctx, rtx, InternalError(fmt.Errorf("handler %q does not implement CommandHandlers", f.Handler)))
		}
		handlers[i] = h
	}

	// The plan: which declared hooks run, paired and ordered. The wiring above
	// happened first, so a custom lifecycle orders resolved handlers — it
	// cannot bypass the handler rules.
	plan := p.lifecycle
	if plan == nil {
		plan = DefaultLifecycle
	}
	steps := plan(chain, handlers)

	// run wraps every hook so a panic is recovered and RECORDED (rtx.RecordErr)
	// rather than unwinding — this is what lets teardown still run and OnError
	// fire exactly once, last. panicked tracks whether a panic stopped FORWARD
	// progress; a panic in teardown is recorded too but does not re-trigger the
	// forward halt (the unwind runs to completion regardless).
	panicked := false
	run := func(hook func(context.Context, *Context)) {
		defer func() {
			if r := recover(); r != nil {
				rtx.RecordErr(&PanicError{Value: r, Stack: debug.Stack()})
				panicked = true
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
		return rtx.stopped || panicked
	}

	// Forward — every step's Do in plan order, halting on rtx.SignalExit/rtx.Exit
	// (rtx.stopped), a panic, or a signal. began records how far the plan got, so
	// the unwind covers exactly the begun steps. The final halt() call records the
	// signal exit code when the last hook returned because the context was canceled.
	began := 0
	for _, s := range steps {
		began++
		run(s.Do)
		if halt() {
			break
		}
	}

	// Unwind, reverse — every begun step's Undo, to completion — unless a hard
	// [Context.Exit] asked to stop now, which skips any teardown still pending
	// (including one requested from within a teardown hook).
	for i := began - 1; i >= 0 && !rtx.exitNow; i-- {
		if steps[i].Undo != nil {
			run(steps[i].Undo)
		}
	}

	// OnError is the single error sink: it fires once, last (after teardown),
	// whenever this run recorded ANY error — a handler's rtx.RecordErr, a
	// recovered panic, or a framework diagnostic. The err argument is their
	// join, so one CategoryOf/Is/As call covers the set. A run that recorded
	// errors never exits 0: floor to 1 even if the funnel forgot a code.
	recorded := rtx.Errors()
	joined := errors.Join(recorded...)
	if len(recorded) > 0 {
		rtx.onErrorFn(ctx, rtx, joined)
		if rtx.exitCode == 0 {
			rtx.exitCode = 1
		}
	}
	return rtx.exitCode, joined
}
