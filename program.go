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

// ExitCode returns a context-cancellation cause that tells rotini which process
// exit code to use when that cancellation halts the run. Pass it to the cancel
// function of a [Program.WithContext] context (Go 1.21+):
//
//	ctx, cancel := context.WithCancelCause(parent)
//	prog.WithContext(ctx)
//	cancel(rotini.ExitCode(3)) // exits 3
//
// It also composes with timeouts, e.g. exit 124 on a deadline:
//
//	ctx, cancel := context.WithTimeoutCause(parent, 5*time.Second, rotini.ExitCode(124))
//
// A handler reaches the same cancel function via the registry — bind it in main
// (`Bind("cancel", cancel)`) and call `MustGet[context.CancelCauseFunc](rtx,
// "cancel")(rotini.ExitCode(3))`. Canceling WITHOUT an ExitCode cause (a plain
// cancel or deadline) still halts cleanly, with the code falling through to the
// normal resolution (0 unless a handler recorded an error or fault). Cancellation
// is cooperative — it never preempts a running hook — and teardown always runs.
// See [Program.WithContext].
func ExitCode(code int) error { return exitCodeError{code: code} }

// exitCodeError carries a process exit code as a context-cancellation cause.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string {
	return fmt.Sprintf("rotini: run canceled (exit code %d)", e.code)
}

// canceledExitCode resolves the exit code recorded when a canceled run context
// halts the lifecycle: the code carried by the cancellation cause — the caller's
// cancel (via [ExitCode]) or the default signal trap attach one — or 0 (a clean
// stop that defers to the normal exit-code resolution) when none was.
func canceledExitCode(ctx context.Context) int {
	var ec exitCodeError
	if errors.As(context.Cause(ctx), &ec) {
		return ec.code
	}
	return 0
}

type Program struct {
	ctx         context.Context
	args        []string
	def         Definition
	handlers    any
	rtx         *Context                                                      // pre-seeded registry; user Bind calls land here
	onErrorFn   func(ctx context.Context, rtx *Context, errs []error)         // OnError funnel (recorded errors); nil → defaultOnError
	onSuccessFn func(ctx context.Context, rtx *Context, results []string)     // OnSuccess funnel (recorded successes); nil → defaultOnSuccess
	onWarningFn func(ctx context.Context, rtx *Context, warnings []error)     // OnWarning funnel (recorded warnings); nil → defaultOnWarning
	onPanicFn   func(ctx context.Context, rtx *Context, panics []*PanicError) // OnPanic funnel (recovered panics + rotini-detected faults); nil → defaultOnPanic
	resolver    Resolver                                                      // resolve phase override; nil → DefaultResolver
	lifecycle   Lifecycle                                                     // run-phase plan override; nil → DefaultLifecycle
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	exit        func(int) // terminal action for Execute; defaults to os.Exit
	// panicForward: when true (default), a recovered panic halts forward progress but lets
	// teardown still run; when false, a recovered panic hard-stops, skipping remaining teardown.
	panicForward bool
	// panicRecover: when true (default), a hook panic is recovered and routed to OnPanic; when
	// false, hooks run unguarded so a panic propagates raw.
	panicRecover bool
}

// NewProgram wires a generated program's command tree (the rtg [Definition]) and
// its aggregate handler set (the rtg ProgramHandlers implementation) to the
// rotini runtime. handlers is any so the runtime need not import the generated
// framework package; dispatch resolves the per-command handlers from it at
// execution time, via the Handler names recorded in def.
func NewProgram(def Definition, handlers any) *Program {
	return &Program{
		args:         os.Args[1:],
		def:          def,
		handlers:     handlers,
		rtx:          newContext(),
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		exit:         os.Exit,
		panicForward: true,
		panicRecover: true,
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

// WithPanicForward controls whether teardown runs when a hook panics. The default is true: a
// panic in a hook halts forward progress (no further setup/PreRun/Run) but every begun setup
// hook's teardown (PostRun/CascadingPostRun) still runs — cleanup on failure, mirroring how
// `defer`s run during a panic unwind. Pass false for a HARD STOP: remaining teardown is SKIPPED
// (as a hard [Context.Exit] would).
//
// Where the panic ultimately goes — the [Program.WithOnPanicFn] funnel (the default) or raw to
// the caller — is the separate [Program.WithPanicRecover] knob; the two compose. Keep forward on
// when teardown must release resources / roll back regardless of a panic; turn it off when a
// panic means the program is too broken to clean up safely (e.g. a wiring fault). The exit code
// is never masked to 0. It returns the receiver to chain.
func (p *Program) WithPanicForward(enabled bool) *Program {
	p.panicForward = enabled
	return p
}

// WithPanicRecover controls where a hook panic ultimately goes. The default is true: every hook
// panic is recovered and routed to [Program.WithOnPanicFn], so consumers of the built CLI never
// see a raw stack dump. Pass false to re-raise the panic RAW to the caller instead of funneling
// it — the original value crashes the process (exit 2, no OnPanic), plain-Go behavior, for e.g.
// embedding rotini under your own top-level recover, a crash reporter, or debugging.
//
// It composes with [Program.WithPanicForward], which still decides teardown:
//   - recover=false, forward=true  → teardown runs, THEN the panic is re-raised (its stack roots
//     at the re-raise, not the original site).
//   - recover=false, forward=false → "panic now": the hook runs unguarded, so the panic
//     propagates immediately with its ORIGINAL stack, skipping teardown.
//
// Note: rotini can only recover panics in the hook goroutine — a panic in a goroutine a handler
// spawned crashes regardless. It returns the receiver to chain.
func (p *Program) WithPanicRecover(enabled bool) *Program {
	p.panicRecover = enabled
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
// a parent process — can cancel or time-bound the whole run. A nil context is ignored.
//
// Cancellation is cooperative: it cannot preempt a hook that ignores it (a handler
// observes it via its ctx.Done()), but once the context is canceled rotini starts no
// further setup hook (CascadingPreRun), PreRun, or Run — and teardown (PostRun/
// CascadingPostRun for every begun setup hook) still runs, in reverse. Attach the process
// exit code to the cancellation with [ExitCode] — e.g. cancel(rotini.ExitCode(3)) via a
// [context.WithCancelCause]/[context.WithTimeoutCause] function; without one, a canceled
// run stops cleanly and the exit code falls through to the normal resolution (0 unless a
// handler recorded an error). A handler can cancel the run by holding this context's cancel
// function — bind it (`Bind("cancel", cancel)`) and call it, e.g.
// `MustGet[context.CancelCauseFunc](rtx, "cancel")(rotini.ExitCode(3))` — or, to stop without
// canceling the context (leaving it live for teardown), use [Context.SignalExit]/[Context.Exit].
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

// WithOnErrorFn sets the program's OnError funnel: where the END-USER's recorded
// errors are reported. The runtime calls fn(ctx, rtx, errs) once per run, after
// the lifecycle settles, whenever a handler recorded ANY error with
// [Context.RecordError] (a remote dispatch failure rotini records on the
// handler's behalf reaches it too) — errs is the recorded set, in order. The
// records are PRIVATE: errs is the only way fn sees them (there is no drainable
// accessor on rtx). fn decides the exit code (rtx.SignalExit), classifies each
// (errors.Is/errors.As, [CategoryOf]), logs, and prints in the CLI's own style.
// With no funnel set, the default prints one clean line per error to stderr
// (program-name prefixed) and exits 1. It returns the receiver so it chains with
// [Program.Bind].
//
// OnError is ONE of four outcome funnels. It is for the end-user's own errors
// only — rotini's "this should never have happened" faults (a wiring mismatch,
// a resolver fault, a recovered panic, a [MustGet] on a missing service) go to
// [Program.WithOnPanicFn] instead; recorded successes and warnings go to
// [Program.WithOnSuccessFn] / [Program.WithOnWarningFn]. A run that recorded any
// error never exits 0; a run with no recorded error never invokes fn.
func (p *Program) WithOnErrorFn(fn func(ctx context.Context, rtx *Context, errs []error)) *Program {
	p.onErrorFn = fn
	return p
}

// WithOnSuccessFn sets the program's OnSuccess funnel: where a handler's
// recorded successes ([Context.RecordSuccess]) are reported. The runtime calls
// fn(ctx, rtx, results) once per run, after the lifecycle settles, whenever any
// success was recorded — results is the recorded messages, in order (the only
// way fn sees them; the records are private). With no funnel set, the default
// prints each result to stdout and does not change the exit code. A run with no
// recorded success never invokes fn. It returns the receiver to chain.
func (p *Program) WithOnSuccessFn(fn func(ctx context.Context, rtx *Context, results []string)) *Program {
	p.onSuccessFn = fn
	return p
}

// WithOnWarningFn sets the program's OnWarning funnel: where a handler's
// recorded non-fatal warnings ([Context.RecordWarning]) are reported. The
// runtime calls fn(ctx, rtx, warnings) once per run, after the lifecycle
// settles, whenever any warning was recorded — warnings is the recorded set, in
// order (the only way fn sees them; the records are private). With no funnel
// set, the default prints each warning to stderr and does NOT change the exit
// code (warnings are non-fatal). A run with no recorded warning never invokes
// fn. It returns the receiver to chain.
func (p *Program) WithOnWarningFn(fn func(ctx context.Context, rtx *Context, warnings []error)) *Program {
	p.onWarningFn = fn
	return p
}

// WithOnPanicFn sets the program's OnPanic funnel: rotini's "this should never
// have happened" sink. The runtime calls fn(ctx, rtx, panics) once per run,
// after the lifecycle settles, whenever a hook PANICKED and was recovered, or
// rotini DETECTED an internal fault (a [*WiringError] from a Definition vs.
// handlers mismatch, a resolver fault, a [MustGet] on a missing service). There
// is no public record call — the lifecycle captures the fault, and panics is the
// captured set, in order (the only way fn sees them). With no funnel set, the
// default prints one clean line per fault to stderr (the stack rides each
// [*PanicError] for errors.As, never printed) and exits 1 — a fault is never
// masked to 0. A run with no fault never invokes fn. It returns the receiver to
// chain.
func (p *Program) WithOnPanicFn(fn func(ctx context.Context, rtx *Context, panics []*PanicError)) *Program {
	p.onPanicFn = fn
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

// WiringError reports that the generated [Definition] and the handler set are
// out of sync — a resolved command names a handler method that does not exist,
// or whose return value does not implement [CommandHandlers]. It is a build-
// time bug surfaced at run time (always [CategoryInternal]); the structured
// fields name the offending command and method so a funnel can report it
// precisely without matching the message:
//
//	var we *rotini.WiringError
//	if errors.As(err, &we) { log.Fatalf("wire %s → %s", we.Command, we.Handler) }
type WiringError struct {
	Command string // the command whose handler wiring is broken
	Handler string // the handler method name the Definition referenced
	Msg     string // the human-readable failure
}

func (e *WiringError) Error() string { return e.Msg }

// Unwrap reports [ErrInternal]: a wiring mismatch is always the author's bug,
// never the end-user's.
func (e *WiringError) Unwrap() error { return ErrInternal }

// WithResolver overrides the resolve phase — argv to invocation target (the
// command chain to dispatch, or a remote dispatch, plus the argv the parsers
// later see). Wrap [DefaultResolver] rather than re-deriving it: a resolver
// that rewrites tokens (an alias, a shorthand expansion) rewrites argv, hands
// it to the default, and returns the result — routing and parsing then agree.
// A resolver error is a resolution-phase fault, routed through the OnPanic
// funnel, and fails the run. The hidden __complete protocol intercept runs before resolution, and
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

	// The effective run context. When the caller supplied none (no WithContext), rotini owns
	// it: a cancelable context whose cancellation — by the default signal trap — drives graceful
	// shutdown. When the caller supplied a context, it is used as-is and rotini installs no trap:
	// the caller owns cancellation and signal handling (cancel it with [ExitCode] for a code). A
	// canceled run context halts the lifecycle either way — see [Program.dispatch].
	ctx := p.ctx
	trapped := ctx == nil
	if trapped {
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(context.Background())
		defer cancel(nil)

		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, trapSignals...)
		defer signal.Stop(sigCh)

		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case s := <-sigCh: // first signal → graceful: cancel ctx with the signal's exit code; dispatch then halts the lifecycle
				cancel(exitCodeError{code: signalExitCode(s)})
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

	res, err := resolve(p.def, argv)
	if err != nil {
		// A resolver fault is rotini's domain, not the end-user's → OnPanic.
		rtx.recordFault(asFault(internalUnlessTagged(fmt.Errorf("resolve: %w", err))))
		return p.settle(ctx, rtx)
	}
	if res.Remote != nil {
		return p.execRemote(ctx, rtx, res.Remote)
	}
	if len(res.Chain) == 0 {
		// An empty chain is a resolver contract violation → OnPanic.
		rtx.recordFault(asFault(InternalError(errors.New("resolver returned an empty chain — the root frame is always resolvable"))))
		return p.settle(ctx, rtx)
	}

	rtx.Args = argv
	if res.Args != nil {
		rtx.Args = res.Args
	}
	rtx.chain = res.Chain
	return p.dispatch(ctx, res.Chain, rtx)
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

// asFault wraps a rotini-detected fault error (a wiring mismatch, a resolver
// fault) into a [*PanicError] so it rides the OnPanic channel uniformly with
// recovered panics. Value holds the error (errors.As/Is and [CategoryOf] see
// through it); Stack is empty — this is a detected fault, not a real panic
// (detect-and-route, no stack unwind).
func asFault(err error) *PanicError { return &PanicError{Value: err} }

// settle is the single run tail: after the lifecycle (or an early framework
// fault) has populated the Context's outcome channels, it fires each non-empty
// channel's funnel — Warning → Success → Error → Panic — and resolves the exit
// code. It is shared by the dispatch tail and the resolve/wiring/remote early
// returns, so every run path reports through the same funnels.
//
// Exit code: a handler's (or a custom funnel's) explicit rtx.SignalExit/rtx.Exit
// wins (first non-zero, already in rtx.exitCode). Otherwise any recorded error
// or captured fault exits 1 — a run that recorded an error or faulted never
// exits 0. Success and warning never change the code. rotini holds no named
// exit-code constants; a CLI that wants other codes sets them in its funnels.
func (p *Program) settle(ctx context.Context, rtx *Context) (int, error) {
	// Snapshot the private channels once; the records surface only here, as the
	// slice each funnel receives — there is no public drainable accessor.
	warnings := rtx.copyWarnings()
	successes := rtx.copySuccesses()
	errs := rtx.copyErrors()
	faults := rtx.copyFaults()

	if len(warnings) > 0 {
		fn := p.onWarningFn
		if fn == nil {
			fn = p.defaultOnWarning
		}
		fn(ctx, rtx, warnings)
	}
	if len(successes) > 0 {
		fn := p.onSuccessFn
		if fn == nil {
			fn = p.defaultOnSuccess
		}
		fn(ctx, rtx, successes)
	}
	if len(errs) > 0 {
		fn := p.onErrorFn
		if fn == nil {
			fn = p.defaultOnError
		}
		fn(ctx, rtx, errs)
	}
	if len(faults) > 0 {
		fn := p.onPanicFn
		if fn == nil {
			fn = p.defaultOnPanic
		}
		fn(ctx, rtx, faults)
	}

	if rtx.exitCode == 0 && (len(errs) > 0 || len(faults) > 0) {
		rtx.exitCode = 1
	}
	return rtx.exitCode, joinOutcome(errs, faults)
}

// joinOutcome is the error a run returns to its caller (and tests): every
// recorded error AND every captured fault, so errors.Is/As reach them all. It
// is nil for a clean run.
func joinOutcome(errs []error, faults []*PanicError) error {
	if len(errs) == 0 && len(faults) == 0 {
		return nil
	}
	all := make([]error, 0, len(errs)+len(faults))
	all = append(all, errs...)
	for _, f := range faults {
		all = append(all, f)
	}
	return errors.Join(all...)
}

// defaultOnError is the OnError funnel used when the program supplies none: it
// prints one clean line per recorded error to stderr (program-name prefixed).
// The exit code is [settle]'s to set (1). Each rotini error type
// renders a single non-leaky line; a CLI that wants richer reporting supplies
// its own via [Program.WithOnErrorFn].
func (p *Program) defaultOnError(_ context.Context, _ *Context, errs []error) {
	for _, e := range errs {
		fmt.Fprintf(p.stderr, "Error: %s\n", e.Error())
	}
}

// defaultOnSuccess prints each recorded success to stdout; the exit code is
// unchanged (a clean run is already 0).
func (p *Program) defaultOnSuccess(_ context.Context, _ *Context, results []string) {
	for _, s := range results {
		fmt.Fprintln(p.stdout, s)
	}
}

// defaultOnWarning prints each recorded warning to stderr (program-name
// prefixed); the exit code is unchanged (warnings are non-fatal).
func (p *Program) defaultOnWarning(_ context.Context, _ *Context, warnings []error) {
	for _, w := range warnings {
		fmt.Fprintf(p.stderr, "Warning: %s\n", w.Error())
	}
}

// defaultOnPanic prints one clean line per captured fault to stderr (the
// [*PanicError]'s Value, never its Stack — that stays for an errors.As). The
// exit is [settle]'s to set (1); a fault is never masked to 0.
func (p *Program) defaultOnPanic(_ context.Context, _ *Context, panics []*PanicError) {
	for _, pe := range panics {
		fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, pe)
	}
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
//     rest nor displaces the first failure or exit code; a hard rtx.Exit skips
//     what remains, and so does a panic when [Program.WithPanicForward] is false.
//
// rtx.SignalExit is a clean stop that still runs teardown; rtx.Exit is a hard stop that
// skips it. A panic anywhere (e.g. the [MustGet] on a missing service) is recovered as a
// fault and routed once — after teardown — to the OnPanic funnel, last. By default it does
// not abort the remaining teardown (cleanup still runs); [Program.WithPanicForward](false)
// makes a panic a hard stop that skips the rest of teardown.
//
// A canceled run context — by the caller of a WithContext context (directly or via a bound
// cancel a handler calls) or by the default signal trap — is converted into a lifecycle
// [Context.SignalExit] between forward hooks: forward progress stops, teardown still runs, and
// the exit code is the cancellation cause's ([ExitCode]) or 0. The conversion happens only on
// the dispatch goroutine, so rtx state stays single-writer; the canceler only cancels ctx.
func (p *Program) dispatch(ctx context.Context, chain []ResolvedCommand, rtx *Context) (int, error) {
	hv := reflect.ValueOf(p.handlers)
	handlers := make([]CommandHandlers, len(chain))
	for i, f := range chain {
		// A wiring failure (generated Definition and handler set out of sync) is
		// rotini's "this should never have happened" — a detected fault routed to
		// the OnPanic funnel (detect-and-route, no literal panic).
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			rtx.recordFault(asFault(&WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("no handler for command %q (missing method %q)", f.Name, f.Handler),
			}))
			return p.settle(ctx, rtx)
		}
		out := m.Call(nil)
		h, ok := out[0].Interface().(CommandHandlers)
		if !ok || h == nil {
			rtx.recordFault(asFault(&WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("handler %q does not implement CommandHandlers", f.Handler),
			}))
			return p.settle(ctx, rtx)
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

	// run wraps every hook so a panic is RECOVERED and the lifecycle stays in control — teardown
	// can still run and the panic is dealt with once, last. Two independent knobs decide the rest:
	//   - [Program.WithPanicForward] (default true): does begun-step teardown run after a panic?
	//   - [Program.WithPanicRecover] (default true): is the panic funneled to OnPanic (true), or
	//     re-panicked raw to the caller after the lifecycle settles (false)?
	// The one combination rotini does NOT recover is recover=false AND forward=false — "panic now":
	// the hook runs unguarded so the panic propagates immediately with its original stack, skipping
	// teardown. panicked records that a panic stopped forward progress; panicValue holds the first
	// panic when it will be re-panicked (recover=false, forward=true) instead of funneled.
	panicked := false
	var panicValue any
	run := func(hook func(context.Context, *Context)) {
		if !p.panicRecover && !p.panicForward {
			hook(ctx, rtx) // panic now: unguarded, original stack, no teardown
			return
		}
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				if p.panicRecover {
					rtx.recordFault(&PanicError{Value: r, Stack: debug.Stack()})
				} else if panicValue == nil {
					panicValue = r // re-panicked after teardown
				}
			}
		}()
		hook(ctx, rtx)
	}

	// halt reports whether forward progress should stop — after a hook called
	// rtx.SignalExit/rtx.Exit or panicked, OR after the run context was canceled (by the caller
	// of a WithContext context or the default signal trap). On cancellation it records the exit
	// code via rtx.SignalExit — the cancellation cause's code ([ExitCode]) or 0 — so the
	// cancellation becomes a clean lifecycle stop that still runs teardown.
	halt := func() bool {
		if !rtx.stopped && ctx.Err() != nil {
			rtx.SignalExit(canceledExitCode(ctx))
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
	// [Context.Exit] asked to stop now (skipping any teardown still pending), or a panic
	// occurred and [Program.WithPanicForward] is false (a hard stop on panic). Both guards are
	// re-checked per step, so an Exit or panic from within a teardown hook stops the rest too.
	for i := began - 1; i >= 0 && !rtx.exitNow && (p.panicForward || !panicked); i-- {
		if steps[i].Undo != nil {
			run(steps[i].Undo)
		}
	}

	// Recovery off (recover=false, forward=true): the panic was caught only so teardown could
	// run; surface it raw now — after teardown — instead of funneling it to OnPanic.
	if panicked && !p.panicRecover {
		panic(panicValue)
	}

	// settle fires whatever outcome channels this run populated (warnings,
	// successes, recorded errors, captured faults), in order, once, after
	// teardown — and resolves the exit code.
	return p.settle(ctx, rtx)
}
