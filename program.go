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

// trapSignals are the signals trapped when signal handling is active. It is a var, not a
// const, so a white-box test can swap in a benign signal.
var trapSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// forceExitCode is the status a second signal exits with (128 + SIGINT).
const forceExitCode = 130

// signalExitCode maps a trapped signal to 128 + signum, falling back to forceExitCode for
// a signal carrying no numeric value.
func signalExitCode(s os.Signal) int {
	if sig, ok := s.(syscall.Signal); ok {
		return 128 + int(sig)
	}
	return forceExitCode
}

// signalTrapMode decides whether rotini installs its signal trap, independently of who owns
// the context.
type signalTrapMode int

const (
	signalAuto signalTrapMode = iota // trap iff no WithContext (default)
	signalOff                        // WithoutSignalHandling(): never trap
	signalOn                         // WithSignals(...): always trap
)

// ExitCode returns a context-cancellation cause that sets the process exit code used when
// that cancellation halts the run:
//
//	ctx, cancel := context.WithCancelCause(parent)
//	prog.WithContext(ctx)
//	cancel(rotini.ExitCode(3)) // exits 3
//
// Canceling without an ExitCode cause still halts cleanly, with the code falling through to
// the normal resolution. Cancellation never preempts a running hook, and teardown always
// runs. See [Program.WithContext].
func ExitCode(code int) error { return exitCodeError{code: code} }

// exitCodeError carries a process exit code as a context-cancellation cause.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string {
	return fmt.Sprintf("run canceled (exit code %d)", e.code)
}

// canceledExitCode returns the code carried by a run context's cancellation cause, or 0 when
// it carries none.
func canceledExitCode(ctx context.Context) int {
	if ec, ok := errors.AsType[exitCodeError](context.Cause(ctx)); ok {
		return ec.code
	}
	return 0
}

// Program is a rotini CLI ready to run: the compiled command tree ([Definition]), the
// handlers that implement it, and the seams around them. The generated entrypoint builds one
// with [NewProgram] and calls [Program.Execute]. Every With method returns the receiver, so
// they chain.
//
// The surface groups into seven jobs, and nothing outside them is worth hunting for:
//
//   - run it — [Program.Execute] exits, [Program.Run] returns the code, [Program.RunContext]
//     scopes one invocation
//   - streams — [Program.WithStdin], [Program.WithStdout], [Program.WithStderr]
//   - process — [Program.WithExit], [Program.WithArgs], [Program.WithContext],
//     [Program.WithSignals], [Program.WithoutSignalHandling]
//   - failure — [Program.WithTeardownOnPanic], [Program.WithPanicRecover], [Program.WithFunnel]
//   - YOUR dependencies — [Program.Bind] for a key you name, [Program.With] with [Provide]
//     for a type-checked one
//   - rotini's own seams — [Program.WithVersion], [Program.WithParser],
//     and the three the generated code handles for you,
//     [Program.WithBindMeta], [Program.WithBinder] and [Program.WithHelp]
//   - replace a phase — [Program.WithResolver], [Program.WithLifecycle]
//
// A Program is reusable: [Program.Run] dispatches one invocation and returns instead of
// exiting, giving each call a fresh [Context]. That is what lets a [REPL], a test, or a server
// answering a peer drive the same program many times.
//
// Configure before the first run. Every With method and [Program.Bind] mutates the Program
// without synchronization, so a concurrent host finishes configuring, then dispatches. Applied
// between sequential runs they simply take effect on the next one.
//
// A nil *Program is a caller bug, not a state to handle: [NewProgram] never returns one, and
// every method here dereferences rather than checking. That is deliberate — returning the nil
// receiver instead would carry it silently down the chain and panic somewhere later, which is
// strictly harder to debug than panicking at the call that was wrong.
//
// The zero value is not usable; start from [NewProgram].
type Program struct {
	ctx       context.Context
	args      []string
	def       Definition
	handlers  any
	rtx       *Context   // seed registry: Program.Bind lands here; each run's Context clones it
	funnelFn  FunnelFunc // nil → defaultFunnel
	resolver  Resolver   // nil → DefaultResolver
	lifecycle Lifecycle  // nil → DefaultLifecycle
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	exit      func(int) // terminal action for Execute; defaults to os.Exit

	teardownOnPanic bool           // does teardown run after a panic? See WithTeardownOnPanic.
	panicRecover    bool           // is a panic funneled or re-raised? See WithPanicRecover.
	signalMode      signalTrapMode // see WithSignals / WithoutSignalHandling
	signalSet       []os.Signal    // signals trapped when on; empty → trapSignals

	// rotini's own seams. These are NOT registry entries: the registry is the user's
	// namespace, and a value the runtime depends on has no business sharing a flat string
	// keyspace with the program's own services, where a name collision or a wrong type
	// would degrade an input channel in silence. See [Program.WithBindMeta].
	meta     *BindMeta              // WithBindMeta: the generated descriptor; nil → none
	binderFn func(BindMeta) *Binder // WithBinder: nil → NewBinder
	version  string                 // WithVersion
	help     HelpFunc               // WithHelp: nil → no pages
	parser   *Parser                // WithParser: nil → a default, built per run
}

// NewProgram wires a generated command tree and its aggregate handler set to the runtime.
// handlers is any so the runtime need not import the generated package; dispatch resolves the
// per-command handlers from it by the Handler names recorded in def.
func NewProgram(def Definition, handlers any) *Program {
	return &Program{
		args:            os.Args[1:],
		def:             def,
		handlers:        handlers,
		rtx:             newContext(),
		stdin:           os.Stdin,
		stdout:          os.Stdout,
		stderr:          os.Stderr,
		exit:            os.Exit,
		teardownOnPanic: true,
		panicRecover:    true,
	}
}

// WithStdin overrides the program's standard input (default os.Stdin) — what a handler reads
// via [Context.Stdin] and what the Binder decodes a stdin channel from. A nil reader is
// ignored.
func (p *Program) WithStdin(r io.Reader) *Program {
	if r != nil {
		p.stdin = r
	}
	return p
}

// WithStdout overrides the program's standard output (default os.Stdout), where the runtime
// writes help, completion output and usage. A nil writer is ignored.
func (p *Program) WithStdout(w io.Writer) *Program {
	if w != nil {
		p.stdout = w
	}
	return p
}

// WithStderr overrides the program's standard error (default os.Stderr), where the runtime
// writes its diagnostics and the default funnel reports. A nil writer is ignored.
func (p *Program) WithStderr(w io.Writer) *Program {
	if w != nil {
		p.stderr = w
	}
	return p
}

// WithExit overrides what [Program.Execute] does with the resolved exit code (default
// [os.Exit]) — supply a recording function to capture the code without terminating. A nil
// function is ignored.
//
// If fn returns, Execute returns to its caller, and that is the ONLY way to observe the error
// Execute reports: under the default os.Exit the process ends first and the return never runs.
// So this is the seam that makes an end-to-end test of a real CLI ordinary Go —
//
//	code := -1
//	err := cmd.Program.
//		WithArgs(argv).WithStdout(&out).WithStderr(&errs).
//		WithExit(func(c int) { code = c }).
//		Execute()
//
// — and equally the seam an embedder needs when a rotini CLI is one component of a larger
// process rather than the process itself.
func (p *Program) WithExit(fn func(int)) *Program {
	if fn != nil {
		p.exit = fn
	}
	return p
}

// WithTeardownOnPanic controls whether teardown runs when a hook panics. The default, true,
// halts forward progress but still runs every begun setup hook's teardown, mirroring how
// defers run during a panic unwind. Pass false to skip the remaining teardown, as a hard
// [Context.Exit] would.
//
// Where the panic GOES is the separate [Program.WithPanicRecover] knob; the two compose.
//
// This was called WithPanicForward, which named the wrong thing: "forward" reads as
// forwarding the panic onward, and forwarding the panic is what WithPanicRecover(false) does.
// These are the options someone reaches for while debugging a crash, so a name that has to be
// unlearned from its doc is worse than a long one.
func (p *Program) WithTeardownOnPanic(enabled bool) *Program {
	p.teardownOnPanic = enabled
	return p
}

// WithPanicRecover controls where a hook panic goes. The default, true, recovers it and routes
// it to the funnel, so users of the built CLI never see a raw stack dump. Pass false to re-raise
// it to the caller instead — for embedding rotini under your own recover, a crash reporter, or
// debugging.
//
// It composes with [Program.WithTeardownOnPanic], which is the separate question of whether
// teardown still runs:
//   - recover=true (the default)  → the panic never leaves rotini; it reaches the funnel as a
//     [*PanicError], and teardown runs or not according to WithTeardownOnPanic.
//   - recover=false, teardown=true  → teardown runs, THEN the panic is re-raised (its stack
//     roots at the re-raise, not the original site).
//   - recover=false, teardown=false → the hook runs unguarded, so the panic propagates
//     immediately with its original stack, skipping teardown.
//
// Only panics on the hook goroutine can be recovered; one in a goroutine a handler spawned
// crashes regardless.
func (p *Program) WithPanicRecover(enabled bool) *Program {
	p.panicRecover = enabled
	return p
}

// WithArgs sets the argument vector [Program.Execute] runs (defaults to os.Args[1:]).
//
// Execute is the ONLY entry point that consults it. [Program.Run] and [Program.RunContext]
// take argv as a parameter and use exactly what they were given — `p.WithArgs(x).Run(nil)`
// runs with no arguments, not with x. That is deliberate rather than an oversight: Run is the
// re-entrant core a REPL or a stdio server calls once per line, where the argv differs every
// time and silently inheriting a program-level default would be a bug that prints the wrong
// answer. A caller who wants the configured vector passes it: `p.Run(argv)`.
//
// A nil args is ignored, so the default survives; pass []string{} to run with none.
func (p *Program) WithArgs(args []string) *Program {
	if args != nil {
		p.args = args
	}
	return p
}

// WithContext sets the base context threaded to every lifecycle hook, the funnel, and any
// remote exec, so a caller can cancel or time-bound the whole run. A nil context is ignored.
//
// Cancellation is cooperative — it cannot preempt a hook that ignores it — but once the
// context is canceled rotini starts no further forward hook, and teardown for every begun
// setup hook still runs in reverse. Attach the process exit code with [ExitCode]; without one
// the code falls through to the normal resolution. To stop without canceling the context, use
// [Context.HaltWithCode] or [Context.Exit].
//
// Supplying a context also opts out of rotini's signal trap by default, on the assumption that
// the caller owns signals (typically via [signal.NotifyContext], which leaves it holding the
// cancel that halts the run). [Program.WithSignals] re-enables the trap on top of a supplied
// context; [Program.WithoutSignalHandling] suppresses it without one.
func (p *Program) WithContext(ctx context.Context) *Program {
	if ctx != nil {
		p.ctx = ctx
	}
	return p
}

// WithoutSignalHandling suppresses rotini's default trap without making the caller surrender
// the context: rotini still owns a cancelable run context but installs no signal.Notify, so
// the program's own handling is the only one. It matters because signal.Notify is additive —
// without this opt-out a caller's handler would stack with rotini's rather than replace it.
//
// In this mode rotini exposes no cancel, so a handler installed here cannot halt the run
// gracefully. For that, prefer [Program.WithContext] with [signal.NotifyContext].
func (p *Program) WithoutSignalHandling() *Program {
	p.signalMode = signalOff
	return p
}

// WithSignals forces rotini's graceful trap on for the given signals, whether or not the
// caller supplied a context. With a supplied context rotini derives a cancelable child to
// drive the trap, so the caller's context is never canceled. The action is fixed: the first
// signal cancels the run context for a graceful halt (teardown runs, exit 128+signum), a
// second forces exit. An empty signal list is ignored — use [Program.WithoutSignalHandling] to
// opt out.
func (p *Program) WithSignals(sigs ...os.Signal) *Program {
	if len(sigs) > 0 {
		p.signalMode = signalOn
		p.signalSet = sigs
	}
	return p
}

// Option is one configuration step, in a form that composes. Every seam below is also a
// method, and for a single step the method reads better; Option exists for the steps that
// cannot be methods.
//
// The case that forced it is the typed registry. Go does not allow type parameters on
// methods, so a type-checked bind cannot be written as p.Provide[T](key, value) — it has to
// take the program as an argument ([Key.Provide]), which ends the chain. [Provide] returns an
// Option instead, and [Program.With] applies any number of them without breaking it.
//
// An Option is an ordinary function, so a program can carry its own:
//
//	func devDefaults() rotini.Option {
//		return func(p *rotini.Program) { p.WithStdout(os.Stderr).WithoutSignalHandling() }
//	}
type Option func(*Program)

// With applies each Option in order and returns the program, so configuration that cannot be
// a method still chains with the configuration that can:
//
//	cmd.Program.
//		With(
//			rotini.Provide(tasks.StoreKey, store),
//			rotini.Provide(tasks.ClientKey, client),
//		).
//		WithVersion(version).
//		Execute()
//
// Options are applied left to right, so a later one overwrites an earlier one binding the
// same key — the same rule [Program.Bind] follows. A nil Option is skipped.
func (p *Program) With(opts ...Option) *Program {
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// ── rotini's own seams ──────────────────────────────────────────────────────.
//
// Everything in this block used to be a string key in the same registry [Program.Bind] writes
// to. That made rotini's internals indistinguishable from the program's own services: the keys
// were bare words ("parser", "version", "binder"), nothing reserved them, and every one was
// read with a discarded comma-ok — so a name collision, or a wrong type, silently produced a
// zero value instead of an error. The worst of it was reachable by accident: binding a
// [Binder] built from an empty [BindMeta], the only way it could be written without knowing
// about a key the user had never seen, turned the configuration-file channel off in silence.
//
// They are typed options now, and the registry belongs to the user alone.

// WithBindMeta supplies the generated binding descriptor — the configuration sources, the
// env prefix and the stdin schemas [Collect] reconciles from. The generated NewProgram calls
// it; a hand-built program calls it when it wants those channels.
//
// It is a description of the program, like [Definition], not a dependency — which is why it
// travels as a typed option rather than as a registry entry.
func (p *Program) WithBindMeta(meta BindMeta) *Program {
	p.meta = &meta
	return p
}

// WithBinder replaces the binder [Collect] and the per-channel helpers use. fn receives the
// program's [BindMeta], so a custom binder is built FROM the generated descriptor rather than
// having to reproduce it:
//
//	p.WithBinder(func(meta rotini.BindMeta) *rotini.Binder {
//		meta.Sources = append(meta.Sources, mySource)
//		return rotini.NewBinder(meta)
//	})
//
// That signature is the point. The previous shape — bind a *Binder under a key — meant the
// caller had to find the generated descriptor and pass it themselves, and a caller who did not
// silently lost every configuration file the spec declared. A nil fn is ignored.
func (p *Program) WithBinder(fn func(BindMeta) *Binder) *Program {
	if fn != nil {
		p.binderFn = fn
	}
	return p
}

// WithVersion sets what the program reports as its version — what a generated `version`
// command and a `--version` flag print, read back with [Context.Version].
//
//	var version = "0.0.0" // go build -ldflags "-X main.version=1.2.3"
//	cmd.Program.WithVersion(version).Execute()
func (p *Program) WithVersion(version string) *Program {
	p.version = version
	return p
}

// HelpFunc returns the help page of the command named by path — the canonical names below the
// root, none for the root itself — or an error when there is no such command. It is the shape
// of the Help function codegen generates.
type HelpFunc func(path ...string) (string, error)

// WithHelp sets where [Context.Help] finds a command's help page. The generated NewProgram
// passes its own Help function, so a program built from a spec has its pages without setting
// anything; a nil help is ignored.
//
// It is a seam on the Program rather than a page each handler holds because of composition: a
// command composed from another spec prints the page of the program it is RUNNING in — with the
// full command path and the flags its new ancestors pass down — and only the program knows it.
func (p *Program) WithHelp(help HelpFunc) *Program {
	if help != nil {
		p.help = help
	}
	return p
}

// WithParser replaces the [Parser] returned by [Context.Parser] and used by the argv channel.
// Parsing is never optional, so a program that sets nothing still has one; this overrides it.
// A nil parser is ignored.
func (p *Program) WithParser(parser *Parser) *Program {
	if parser != nil {
		p.parser = parser
	}
	return p
}

// Bind registers a service on the program's registry under key, overwriting any prior
// binding. It is the dependency-injection seam: bind a real implementation in production or a
// double in tests, and handler code retrieves either through [Context.Get] or
// [Context.MustGet].
//
// It is YOUR namespace. rotini's own seams — the binder, the parser, the version, the help
// pages, the generated [BindMeta] — are typed options on the Program, not
// entries here, so a key you choose can never shadow one of them and a type you get wrong can
// never degrade an input channel in silence.
func (p *Program) Bind(key string, value any) *Program {
	p.rtx.Bind(key, value)
	return p
}

// newRunContext builds the per-invocation [Context] — fresh outcome channels and exit state,
// the services bound via [Program.Bind], and the program's streams. Every run gets its own,
// which is what makes a Program re-entrant.
func (p *Program) newRunContext() *Context {
	rtx := newContext()
	if p.rtx != nil {
		if svcs := p.rtx.cloneServices(); svcs != nil {
			rtx.services = svcs
		}
	}
	rtx.Stdin, rtx.Stdout, rtx.Stderr = p.stdin, p.stdout, p.stderr
	rtx.meta, rtx.binderFn = p.meta, p.binderFn
	rtx.version, rtx.parser, rtx.help = p.version, p.parser, p.help
	return rtx
}

// Outcome is everything a run recorded, handed to the funnel in one value. Each slice is in
// recording order, and this struct is the only way the records surface — [Context] keeps them
// private so nothing can read a partial run.
//
// It is a struct rather than five parameters for two reasons, both of which matter to code
// that will be written against a frozen v1: at a call site the channels are named, so Infos
// and Successes (both []string) and Warnings and Errors (both []error) cannot be silently
// transposed; and a channel added later is an additive field rather than a breaking change to
// every custom funnel in existence.
type Outcome struct {
	// Infos are [Context.RecordInfo] messages: neutral output, no bearing on the exit code.
	Infos []string
	// Successes are [Context.RecordSuccess] messages.
	Successes []string
	// Warnings are [Context.RecordWarning] values: non-fatal, never raising the exit code.
	Warnings []error
	// Errors are [Context.RecordError] values — the end user's own failures.
	Errors []error
	// Panics are recovered panics and rotini-detected faults. There is no record call for
	// these: the lifecycle captures them, so a handler cannot fake or suppress one.
	Panics []*PanicError
}

// Empty reports whether the run recorded nothing at all — a silent success. The runtime skips
// the funnel entirely in that case, so a funnel never sees an empty Outcome.
func (o Outcome) Empty() bool {
	return len(o.Infos)+len(o.Successes)+len(o.Warnings)+len(o.Errors)+len(o.Panics) == 0
}

// Failed reports whether the run recorded an error or a panic — what the default funnel's
// exit floor keys on.
func (o Outcome) Failed() bool { return len(o.Errors) > 0 || len(o.Panics) > 0 }

// FunnelFunc is the program's outcome funnel. The runtime calls it once, after the lifecycle
// and its teardown settle, with everything the run recorded (see [Outcome]).
//
// The funnel decides what to print, where, in what order, and the final exit code: it is the
// last authority, so [Context.Exit] inside it overrides whatever the lifecycle set
// ([Context.HaltWithCode] is a no-op here).
//
// It is the last authority on the CODE, not on what the run recorded. The [Outcome] is the
// funnel's own copy to read; the error [Program.Run] returns is built before the funnel is
// called, so editing the slices it was handed changes nothing but the funnel's own view.
//
// Nothing recovers a panic from inside a funnel — it is the last thing a run does, and a funnel
// for the funnel is not a thing. A funnel that can fail should handle its own failure, write to
// rtx.Stderr and set a code with [Context.Exit]; recording there is dropped, because the Outcome
// was snapshotted before it ran.
type FunnelFunc func(ctx context.Context, rtx *Context, out Outcome)

// WithFunnel sets the program's outcome funnel — the one place a run's recorded channels are
// reported. It runs once per run, after the lifecycle settles, whenever any channel recorded
// something; a clean run never invokes it. See [FunnelFunc].
//
// The default prints info → warning → error → panic → success (infos and successes to stdout,
// the rest to stderr) and applies an exit floor: a recorded error or panic exits non-zero
// unless a handler already set a deliberate code. A custom funnel owns the exit entirely.
//
// A nil fn RESTORES the default, which is why this one seam accepts nil rather than ignoring
// it: "report the way rotini does" is a thing a host may want back, and there is no other way
// to ask for it. The seams that replace a value rather than a behavior — [Program.WithStdout],
// [Program.WithResolver] and the rest — ignore nil instead, so a conditional caller cannot
// erase a stream or a phase by passing one.
func (p *Program) WithFunnel(fn FunnelFunc) *Program {
	p.funnelFn = fn
	return p
}

// PanicError carries a panic recovered from a lifecycle hook to the funnel: Value is what was
// passed to panic, Stack the goroutine stack captured at the recovery point. Error renders
// Value alone, so default output stays one line; a funnel that wants the stack asks for it
// with errors.As.
type PanicError struct {
	Value any
	Stack []byte
}

// Error renders the panic value as a single line; the captured stack is deliberately not
// printed.
func (e *PanicError) Error() string { return fmt.Sprintf("%v", e.Value) }

// Unwrap exposes a panicked error value so errors.Is/As and [CategoryOf] see through it, and
// always exposes [ErrInternal] underneath.
//
// The floor matters. A recovered panic is a bug in the program by definition — [CategoryInternal]
// is literally "the end-user cannot fix it; the author must" — but a panic value is usually not
// an error at all (panic("boom")), and without the floor CategoryOf reported [CategoryNone] for
// it. That is not merely uninformative: none sorts BELOW usage, so a funnel keeping the most
// severe category across a run would rank a crash under a mistyped flag.
//
// A panicked error value still wins the classification, because [CategoryOf] tests [ErrUsage]
// before [ErrInternal] — panicking a [UsageError] reports usage, the floor only catches what
// nothing else classifies.
func (e *PanicError) Unwrap() []error {
	if err, ok := e.Value.(error); ok {
		return []error{err, ErrInternal}
	}
	return []error{ErrInternal}
}

// WiringError reports that the generated [Definition] and the handler set are out of sync — a
// resolved command names a handler method that does not exist, or whose return value does not
// implement [Handlers]. It is a build-time bug surfaced at run time, always
// [CategoryInternal], and names the offending command and method so a funnel need not match on
// the message.
type WiringError struct {
	Command string // the command whose handler wiring is broken
	Handler string // the handler method name the Definition referenced
	Msg     string // the human-readable failure
}

// Error renders the mismatch as a single line.
func (e *WiringError) Error() string { return e.Msg }

// Unwrap reports [ErrInternal]: a wiring mismatch is the author's bug, never the user's.
func (e *WiringError) Unwrap() error { return ErrInternal }

// WithResolver overrides the resolve phase — argv to invocation target, plus the argv the
// parsers later see. Wrap [DefaultResolver] rather than re-deriving it: a resolver that
// rewrites tokens should rewrite argv, hand it to the default, and return the result, so
// routing and parsing agree. A resolver error is routed through the funnel as a fault and
// fails the run.
//
// Completion candidates walk the [Definition], so a resolver-only alias is dispatchable but
// not completable; declare real aliases in the spec for that. A nil resolver is ignored.
func (p *Program) WithResolver(fn Resolver) *Program {
	if fn != nil {
		p.resolver = fn
	}
	return p
}

// WithLifecycle overrides the run phase's plan — which declared hooks run, in what pairing and
// order (see [Lifecycle] and [DefaultLifecycle]). The semantics around the plan — halting,
// the balanced reverse unwind, teardown to completion, the panic funnel, exit codes — stay
// fixed. Wrap [DefaultLifecycle] rather than re-deriving it. A nil lifecycle is ignored.
func (p *Program) WithLifecycle(fn Lifecycle) *Program {
	if fn != nil {
		p.lifecycle = fn
	}
	return p
}

// Execute resolves the command, runs its lifecycle, and ends with the resulting status code
// via the program's exit action ([os.Exit] by default; see [Program.WithExit]).
//
// # The returned error, and when it can arrive
//
// The error is the run's own failure: every [Context.RecordError] value and every recovered
// fault, joined with errors.Join — so errors.Is and errors.As reach each one, and a caller can
// branch on a [*ParseError] or a [*BindError] rather than on text.
//
// It is reachable only when the exit action RETURNS. Under the default action, os.Exit, the
// process is already gone by then and the return statement never runs, which is why the
// generated entrypoint discards it:
//
//	cmd.Program.WithVersion(version).Execute()   // the error cannot arrive here
//
// Supply a [Program.WithExit] that returns — a test capturing the code, a host embedding the
// CLI — and it does:
//
//	code := -1
//	err := cmd.Program.WithExit(func(c int) { code = c }).Execute()
//
// The error is not the reporting channel. By the time Execute returns, the funnel has already
// printed everything the run recorded ([Program.WithFunnel]). The return exists so an embedder
// can ACT on the failure — retry, wrap, classify with [CategoryOf] — without re-deriving it
// from what was written to a stream. A caller that only wants the number can use
// [Program.Run], which returns both and never exits.
//
// # Signals
//
// With no [Program.WithContext], Execute installs rotini's signal trap: the first
// os.Interrupt or syscall.SIGTERM halts the lifecycle like [Context.HaltWithCode] — forward
// progress stops, every begun teardown hook still runs — and exits 128+signum. A second signal
// forces exit immediately, so a handler that ignores the context can still be interrupted. See
// [Program.WithoutSignalHandling] and [Program.WithSignals].
func (p *Program) Execute() error {
	code, err := p.Run(p.args)
	p.exit(code)
	return err
}

// Run dispatches one invocation of argv and returns its exit code — the re-entrant core
// [Program.Execute] is built on. It resolves the invoked command (flag parsing stays the
// handler's opt-in via [Parser.Parse]), execs a remote sub-command if one was selected, and
// otherwise dispatches the lifecycle.
//
// Unlike Execute, Run never ends the process, which is what makes a Program reusable: a REPL,
// a daemon or a test can call it once per line and inspect the code.
//
// Each call gets a fresh [Context], so one invocation never inherits the previous one's
// records or status. Services bound with [Program.Bind] are seeded into every run; one a
// handler binds mid-run stays local to that run.
//
// For hosts that dispatch in a loop: with no supplied context Run installs and tears down the
// signal trap on every call, about 30µs — negligible once per process, but roughly 20x the
// dispatch itself when repeated. Prefer [Program.RunContext] or [Program.WithoutSignalHandling],
// as [REPL] does.
//
// # Concurrency
//
// Run is safe to call concurrently once the program is configured — every With* option and
// [Program.Bind] must happen before the first run, since none of them is synchronized. Each
// concurrent run has its own [Context], so records, exit state and mid-run bindings never
// cross between them.
//
// Two things stay SHARED, and a concurrent host owns both:
//
//   - The handlers value given to [NewProgram]. rotini calls its methods from each run's
//     goroutine, so mutable handler state needs its own synchronization.
//   - The program's streams. os.Stdout is safe for concurrent writes; an unguarded
//     bytes.Buffer in a test is not.
//
// Signal trapping is per-run: with no supplied context, every concurrent run installs its own
// handler and all of them observe one signal. A concurrent host passes its own context
// ([Program.RunContext]) or turns the trap off with [Program.WithoutSignalHandling].
func (p *Program) Run(argv []string) (int, error) {
	if p.ctx != nil {
		return p.runWith(p.ctx, true, argv)
	}
	return p.runWith(context.Background(), false, argv)
}

// RunContext is [Program.Run] under an explicit context, for this invocation only. Unlike
// [Program.WithContext] it does not modify the program, so a host dispatching many invocations
// can scope each one without permanently changing how the program handles signals.
//
// Supplying a context defers signal handling to the caller, as [Program.WithContext] does.
// ctx must not be nil; a nil context is reported as an [ErrInternal].
func (p *Program) RunContext(ctx context.Context, argv []string) (int, error) {
	if ctx == nil {
		return 1, InternalError(errors.New("rotini: RunContext called with a nil context"))
	}
	return p.runWith(ctx, true, argv)
}

// runWith is the shared core. hasCtx says whether a context was supplied, which is what
// decides the default signal trap.
func (p *Program) runWith(runCtx context.Context, hasCtx bool, argv []string) (int, error) {
	if len(argv) > 0 && argv[0] == completeCommand {
		rtx := p.newRunContext()
		for _, c := range complete(p.def, argv[1:], p.handlers, rtx) {
			fmt.Fprintln(p.stdout, c)
		}
		// The declarative hint, when the input being completed declares one, goes last:
		// a generated script reads the final line and translates it into that shell's
		// own path completion.
		if d := completionHint(p.def, argv[1:]); d != "" {
			fmt.Fprintln(p.stdout, d)
		}
		return 0, nil
	}

	// Run context and signal trapping are independent axes: signalAuto traps iff the caller
	// supplied no context, and WithSignals/WithoutSignalHandling override that.
	ctx := runCtx
	trap := !hasCtx // signalAuto: trap iff the caller supplied no context
	switch p.signalMode {
	case signalOff:
		trap = false
	case signalOn:
		trap = true
	}

	// Derive a cancelable child only when rotini must be the canceler (the trap) or owns the
	// context outright; otherwise the caller's context passes through untouched, and
	// cancellation is observed via ctx.Err().
	var cancel context.CancelCauseFunc
	if !hasCtx || trap {
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
	}

	if trap {
		sigs := p.signalSet
		if len(sigs) == 0 {
			sigs = trapSignals
		}
		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, sigs...)
		defer signal.Stop(sigCh)

		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case s := <-sigCh: // first signal → cancel with the signal's exit code; dispatch halts the lifecycle
				cancel(exitCodeError{code: signalExitCode(s)})
			case <-done:
				return
			}
			select {
			case <-sigCh: // second signal → force exit, skipping remaining teardown
				p.exit(forceExitCode)
			case <-done:
			}
		}()
	}

	resolve := p.resolver
	if resolve == nil {
		resolve = DefaultResolver
	}
	rtx := p.newRunContext()

	res, err := resolve(p.def, argv)
	if err != nil {
		// A resolver fault is rotini's domain, not the end-user's.
		rtx.recordFault(asFault(internalUnlessTagged(fmt.Errorf("resolve: %w", err))))
		return p.settle(ctx, rtx)
	}
	if res.Remote != nil {
		return p.execRemote(ctx, rtx, res.Remote)
	}
	if len(res.Chain) == 0 {
		// An empty chain violates the resolver contract.
		rtx.recordFault(asFault(InternalError(errors.New("resolver returned an empty chain — the root frame is always resolvable"))))
		return p.settle(ctx, rtx)
	}

	rtx.Argv = argv
	if res.Argv != nil {
		rtx.Argv = res.Argv
	}
	rtx.chain = res.Chain
	return p.dispatch(ctx, res.Chain, rtx)
}

// internalUnlessTagged tags err [CategoryInternal] unless its producer already categorized it:
// a custom resolver may legitimately raise a usage error, and that must survive.
func internalUnlessTagged(err error) error {
	if CategoryOf(err) != CategoryNone {
		return err
	}
	return InternalError(err)
}

// asFault wraps a rotini-detected fault so it rides the panics channel alongside recovered
// panics. Stack is empty: this was detected and routed, never unwound.
func asFault(err error) *PanicError { return &PanicError{Value: err} }

// settle is the single run tail: it hands every populated outcome channel to the funnel and
// resolves the exit code. Both the dispatch tail and the resolve/wiring/remote early returns
// go through it, so every run path reports the same way.
//
// A handler's explicit exit code is already in rtx.exitCode when the funnel runs, but the
// funnel is the final authority — it runs in the funnel stage, where rtx.Exit overrides. The
// exit floors live in defaultFunnel, so a custom funnel simply does not inherit them.
func (p *Program) settle(ctx context.Context, rtx *Context) (int, error) {
	// Snapshot the private channels once; they surface only as the funnel's argument.
	out := Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}

	// The run's error is built BEFORE the funnel sees the Outcome, and that ordering is the
	// point. Outcome is passed by value but its slices are headers over shared arrays, so a
	// funnel writing out.Errors[0] used to reach this line and change what Run returns —
	// while out.Errors = append(...) did not, because append reallocates. Aliasing that
	// propagates for an index write and vanishes for an append is a trap, not a feature.
	//
	// The funnel is the final authority on the EXIT CODE, through [Context.Exit], and that
	// still holds because rtx.exitCode is read after it runs. It is not an authority on what
	// the run recorded: that is the run's own account of itself.
	err := joinOutcome(out.Errors, out.Panics)

	// A clean run that recorded nothing never invokes the funnel.
	if !out.Empty() {
		fn := p.funnelFn
		if fn == nil {
			fn = p.defaultFunnel
		}
		rtx.funnelStage = true // rtx.Exit now overrides; rtx.HaltWithCode is a no-op
		fn(ctx, rtx, out)
		rtx.funnelStage = false
	}
	return rtx.exitCode, err
}

// joinOutcome is the error a run returns to its caller: every recorded error and captured
// fault, so errors.Is/As reach them all. It is nil for a clean run.
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

// defaultFunnel prints each channel in the order info → warning → error → panic → success,
// infos and successes to stdout and the rest to stderr, then applies the exit floor: an error
// or fault exits 1 unless a handler already set a deliberate code, which it never downgrades.
// A [*PanicError]'s Stack is never printed — it stays for an errors.As.
func (p *Program) defaultFunnel(_ context.Context, rtx *Context, out Outcome) {
	for _, s := range out.Infos {
		fmt.Fprintln(p.stdout, s)
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(p.stderr, "Warning: %s\n", w.Error())
	}
	for _, e := range out.Errors {
		fmt.Fprintf(p.stderr, "Error: %s\n", e.Error())
	}
	for _, pe := range out.Panics {
		fmt.Fprintf(p.stderr, "Fatal Error: %v\n", pe)
	}
	for _, s := range out.Successes {
		fmt.Fprintln(p.stdout, s)
	}
	// A deliberate handler exit is never downgraded.
	if rtx.exitCode == 0 && out.Failed() {
		rtx.exitCode = 1
	}
}

// dispatch resolves each command in the chain to its [Handlers], asks the lifecycle planner
// for the step plan, and executes it as a balanced LIFO setup/teardown:
//
//   - Forward: each step's Do in plan order, halting the moment a hook calls HaltWithCode or
//     Exit, panics, or a trapped signal cancels ctx.
//   - Unwind: the Undo of every step whose Do began, in reverse, to completion. A panic or
//     HaltWithCode inside an Undo neither aborts the rest nor displaces the first failure; a
//     hard Exit skips what remains, and so does a panic under WithTeardownOnPanic(false).
//
// A recovered panic is routed to the funnel once, after teardown. A canceled run context is
// converted into a [Context.HaltWithCode] between forward hooks — teardown still runs, and the
// exit code is the cancellation cause's or 0. That conversion happens only on the dispatch
// goroutine, so rtx stays single-writer.
func (p *Program) dispatch(ctx context.Context, chain []ResolvedCommand, rtx *Context) (int, error) {
	hv := reflect.ValueOf(p.handlers)
	if !hv.IsValid() {
		// A nil handlers value. reflect.ValueOf(nil) is the ZERO Value, and MethodByName on
		// it panics with a reflect-internal message — so the promise one line below was
		// broken for exactly this input, and the panic escaped Run rather than reaching the
		// funnel. nil is the one thing a caller can pass that is not a wiring mistake it can
		// see: the generated NewProgram takes an interface, so NewProgram(nil) compiles.
		rtx.recordFault(asFault(&WiringError{
			Msg: "no handlers: NewProgram was given a nil handlers value",
		}))
		return p.settle(ctx, rtx)
	}
	handlers := make([]Handlers, len(chain))
	for i, f := range chain {
		// A wiring failure is detected and routed as a fault, never panicked.
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			rtx.recordFault(asFault(&WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("no handler for command %q (missing method %q)", f.Name, f.Handler),
			}))
			return p.settle(ctx, rtx)
		}
		out := m.Call(nil)
		h, ok := reflect.TypeAssert[Handlers](out[0])
		if !ok || h == nil {
			rtx.recordFault(asFault(&WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("handler %q does not implement Handlers", f.Handler),
			}))
			return p.settle(ctx, rtx)
		}
		handlers[i] = h
	}

	// Wiring happened first, so a custom lifecycle orders already-resolved handlers and cannot
	// bypass the handler rules.
	plan := p.lifecycle
	if plan == nil {
		plan = DefaultLifecycle
	}
	steps := plan(chain, handlers)

	// run wraps every hook so a panic leaves the lifecycle in control. The one combination not
	// recovered is recover=false and forward=false, where the hook runs unguarded so the panic
	// propagates with its original stack. panicValue holds the first panic when it will be
	// re-panicked after teardown rather than funneled.
	panicked := false
	var panicValue any
	run := func(hook func(context.Context, *Context)) {
		if !p.panicRecover && !p.teardownOnPanic {
			hook(ctx, rtx) // unguarded: original stack, no teardown
			return
		}
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				if p.panicRecover {
					rtx.recordFault(&PanicError{Value: r, Stack: debug.Stack()})
				} else if panicValue == nil {
					panicValue = r
				}
			}
		}()
		hook(ctx, rtx)
	}

	// halt reports whether forward progress should stop. On cancellation it records the cause's
	// exit code via HaltWithCode, turning the cancellation into a clean stop that runs teardown.
	halt := func() bool {
		if !rtx.stopped && ctx.Err() != nil {
			rtx.HaltWithCode(canceledExitCode(ctx))
		}
		return rtx.stopped || panicked
	}

	// Forward. began records how far the plan got, so the unwind covers exactly the begun steps.
	began := 0
	for _, s := range steps {
		began++
		// A nil Do is a step with no forward work, mirroring the nil Undo the unwind below
		// already tolerates. That symmetry is the point: a hand-built plan may legitimately
		// register a teardown with no setup, and before this guard a nil Do was a nil
		// dereference reported as "invalid memory address" — an opaque diagnostic for what
		// is, at worst, a plain wiring mistake.
		if s.Do != nil {
			run(s.Do)
		}
		if halt() {
			break
		}
	}

	// Unwind. Both guards are re-checked per step, so an Exit or panic from within a teardown
	// hook stops the rest too.
	for i := began - 1; i >= 0 && !rtx.exitNow && (p.teardownOnPanic || !panicked); i-- {
		if steps[i].Undo != nil {
			run(steps[i].Undo)
		}
	}

	// recover=false, forward=true: the panic was caught only so teardown could run.
	if panicked && !p.panicRecover {
		panic(panicValue)
	}

	return p.settle(ctx, rtx)
}
