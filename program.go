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
	"slices"
	"syscall"
)

// trapSignals are the signals trapped by default. It is a var so a test can substitute a
// benign signal.
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

// Program is a rotini CLI ready to run: the compiled command tree ([Definition]), the
// handlers that implement it, and the seams around them. The generated entrypoint builds one
// with [NewProgram] and calls [Program.Execute]. Every With method returns the receiver, so
// calls chain.
//
// The surface groups into eight jobs:
//
//   - run — [Program.Execute] exits, [Program.Run] returns the code, [Program.RunContext]
//     scopes one invocation, [Program.Complete] answers a completion request in a
//     [CompletionFormat]
//   - streams — [Program.WithStdin], [Program.WithStdout], [Program.WithStderr]
//   - process — [Program.WithExit], [Program.WithArgs], [Program.WithContext],
//     [Program.WithSignals], [Program.WithoutSignalHandling], [Program.WithCompletion]
//   - failure — [Program.WithTeardownOnPanic], [Program.WithPanicRecover], [Program.WithReporter]
//   - output — [Program.WithOutputChecks] checks every output written with [Context.WriteOutput]
//     against the command's declared contract
//   - the program's dependencies — [Program.WithDependency], or [Program.With] with the
//     [WithDependency] option to register several at once
//   - rotini's own seams — [Program.WithVersion], [Program.WithParser], [Program.WithInputReader],
//     and the two the generated code sets, [Program.WithInputSettings] and [Program.WithHelp]
//   - phase replacement — [Program.WithResolver], [Program.WithLifecycle]
//
// A setting that rotini reads (the runtime or generated code) is a typed option on the
// Program; a setting only the program's own code reads is a dependency. Typed options cannot
// be shadowed by a dependency name, and a wrong type is a compile error.
//
// A Program is reusable: [Program.Run] dispatches one invocation and returns, giving each
// call a fresh [Context].
//
// The With methods are not synchronized: configure before the first run. Applied between
// sequential runs, they take effect on the next one.
//
// Methods do not check for a nil receiver. The zero value is not usable; start from
// [NewProgram].
type Program struct {
	ctx        context.Context
	args       []string
	def        Definition
	handlers   any
	rtx        *Context  // seed dependencies: Program.WithDependency lands here; each run's Context clones them
	reporterFn Reporter  // nil → defaultReporter
	resolver   Resolver  // nil → DefaultResolver
	lifecycle  Lifecycle // nil → DefaultLifecycle
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	exit       func(int) // terminal action for Execute; defaults to os.Exit

	teardownOnPanic bool             // does teardown run after a panic? See WithTeardownOnPanic.
	panicRecover    bool             // is a panic reported or re-raised? See WithPanicRecover.
	signalMode      signalTrapMode   // see WithSignals / WithoutSignalHandling
	signalSet       []os.Signal      // signals trapped when on; empty → trapSignals
	completion      CompletionFormat // the format __complete answers in; nil → rotini's own. See WithCompletion.

	// rotini's own seams, kept out of the dependency store so a program's dependency can
	// never shadow them.
	meta     *InputSettings                   // WithInputSettings: the generated descriptor; nil → none
	readerFn func(InputSettings) *InputReader // WithInputReader: nil → NewInputReader
	version  string                           // WithVersion
	help     HelpFunc                         // WithHelp: nil → no pages
	parser   *Parser                          // WithParser: nil → a default, built per run

	outputChecks bool // WithOutputChecks: check every written output against its schema
}

// NewProgram wires a generated command tree and its aggregate handler set to the runtime.
// Dispatch calls the method of handlers named by each [Command.Handler] in def to obtain that
// command's [Handler]. A nil handlers value is accepted here; a run that reaches dispatch then
// fails with a [*WiringError].
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

// WithStdin overrides the program's standard input (default os.Stdin): the reader exposed as
// [Context.Stdin] and decoded for stdin inputs. A nil reader is ignored.
func (p *Program) WithStdin(r io.Reader) *Program {
	if r != nil {
		p.stdin = r
	}
	return p
}

// WithStdout overrides the program's standard output (default os.Stdout), where the runtime
// writes completion candidates and the default reporter writes infos and successes. A nil
// writer is ignored.
func (p *Program) WithStdout(w io.Writer) *Program {
	if w != nil {
		p.stdout = w
	}
	return p
}

// WithStderr overrides the program's standard error (default os.Stderr), where the default
// reporter writes warnings, errors and panics. A nil writer is ignored.
func (p *Program) WithStderr(w io.Writer) *Program {
	if w != nil {
		p.stderr = w
	}
	return p
}

// WithExit overrides what [Program.Execute] does with the resolved exit code (default
// [os.Exit]). The same function receives the forced exit code when a second trapped signal
// arrives. A nil function is ignored.
//
// When fn returns, Execute returns the run's error to its caller; under os.Exit it never
// does. This makes an end-to-end test or an embedding host possible:
//
//	code := -1
//	err := cmd.Program.
//		WithArgs(argv).WithStdout(&out).WithStderr(&errs).
//		WithExit(func(c int) { code = c }).
//		Execute()
func (p *Program) WithExit(fn func(int)) *Program {
	if fn != nil {
		p.exit = fn
	}
	return p
}

// WithTeardownOnPanic controls whether teardown runs when a hook panics. The default, true,
// halts forward progress and still runs the teardown of every begun setup hook, as deferred
// calls run during a panic. False skips the remaining teardown, as [Context.Exit] does.
//
// Where the panic goes is controlled separately by [Program.WithPanicRecover].
func (p *Program) WithTeardownOnPanic(enabled bool) *Program {
	p.teardownOnPanic = enabled
	return p
}

// WithPanicRecover controls where a hook panic goes. The default, true, recovers it and
// routes it to the reporter as a [*PanicError]. False re-raises it to the caller, for an
// embedding host's own recover, a crash reporter, or debugging.
//
// Combined with [Program.WithTeardownOnPanic]:
//   - recover=true: the panic reaches the reporter; teardown runs per WithTeardownOnPanic.
//   - recover=false, teardown=true: teardown runs, then the panic is re-raised (its stack
//     starts at the re-raise, not the original site).
//   - recover=false, teardown=false: the hook runs unguarded, so the panic propagates
//     immediately with its original stack and no teardown.
//
// Only panics on the hook goroutine are recovered; a panic in a goroutine a handler started
// crashes the process.
func (p *Program) WithPanicRecover(enabled bool) *Program {
	p.panicRecover = enabled
	return p
}

// WithArgs sets the argument vector [Program.Execute] runs (defaults to os.Args[1:]).
//
// Only Execute reads it. [Program.Run] and [Program.RunContext] use the argv they are passed,
// so `p.WithArgs(x).Run(nil)` runs with no arguments.
//
// A nil args is ignored; pass []string{} to run with none.
func (p *Program) WithArgs(args []string) *Program {
	if args != nil {
		p.args = args
	}
	return p
}

// WithContext sets the base context threaded to every lifecycle hook, the reporter, and any
// plugin exec, so a caller can cancel or time-bound the whole run. A nil context is ignored.
//
// Cancellation is cooperative: it does not preempt a running hook, but once the context is
// canceled no further forward hook starts, and the teardown of every begun setup hook runs in
// reverse. [ExitCause] attaches an exit code; without one the code is resolved as usual.
//
// Supplying a context disables rotini's signal trap by default, leaving signals to the caller
// (typically via [signal.NotifyContext]). [Program.WithSignals] re-enables the trap on top of
// a supplied context; [Program.WithoutSignalHandling] disables it without one.
func (p *Program) WithContext(ctx context.Context) *Program {
	if ctx != nil {
		p.ctx = ctx
	}
	return p
}

// WithoutSignalHandling disables rotini's signal trap; of it and [Program.WithSignals], the
// later call wins. rotini still owns the run context but calls no signal.Notify, so the
// program's own handling is the only one (signal.Notify registrations are additive).
//
// rotini exposes no cancel in this mode, so the program's signal handler cannot halt the run
// gracefully; for that, use [Program.WithContext] with [signal.NotifyContext].
func (p *Program) WithoutSignalHandling() *Program {
	p.signalMode = signalOff
	return p
}

// WithSignals enables rotini's signal trap for the given signals, whether or not a context
// was supplied. With a supplied context the trap cancels a derived child, never the caller's
// context. The first signal halts the run (teardown runs, exit 128+signum); a second exits
// immediately with 130 through the exit action. An empty list is ignored; use
// [Program.WithoutSignalHandling] to disable the trap.
func (p *Program) WithSignals(sigs ...os.Signal) *Program {
	if len(sigs) > 0 {
		p.signalMode = signalOn
		p.signalSet = sigs
	}
	return p
}

// Option is one configuration step as a value, applied by [Program.With]. [WithDependency]
// returns one; a program can define its own:
//
//	func devDefaults() rotini.Option {
//		return func(p *rotini.Program) { p.WithStdout(os.Stderr).WithoutSignalHandling() }
//	}
type Option func(*Program)

// With applies each Option in order and returns the program:
//
//	cmd.Program.
//		With(
//			rotini.WithDependency(tasks.Store, store),
//			rotini.WithDependency(tasks.Client, client),
//		).
//		WithVersion(version).
//		Execute()
//
// A later Option registering the same dependency replaces an earlier one. A nil Option is
// skipped.
func (p *Program) With(opts ...Option) *Program {
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// ── rotini's own seams ──────────────────────────────────────────────────────.

// WithInputSettings supplies the generated input descriptor: the configuration sources, the
// env prefix and the stdin schemas [Context.Inputs] reads from. The generated NewProgram calls
// it; a hand-built program calls it to enable those channels.
func (p *Program) WithInputSettings(meta InputSettings) *Program {
	p.meta = &meta
	return p
}

// WithInputReader replaces the input reader that [Context.Inputs] and the per-channel methods
// use. fn receives the program's [InputSettings], so a replacement starts from the generated
// descriptor and keeps the declared configuration sources:
//
//	p.WithInputReader(func(meta rotini.InputSettings) *rotini.InputReader {
//		meta.Sources = append(meta.Sources, mySource)
//		return rotini.NewInputReader(meta)
//	})
//
// A nil fn is ignored.
func (p *Program) WithInputReader(fn func(InputSettings) *InputReader) *Program {
	if fn != nil {
		p.readerFn = fn
	}
	return p
}

// WithVersion sets the program's version string, read with [Context.Version] and printed by
// the generated version command and --version flag.
//
//	var version = "0.0.0" // go build -ldflags "-X main.version=1.2.3"
//	cmd.Program.WithVersion(version).Execute()
func (p *Program) WithVersion(version string) *Program {
	p.version = version
	return p
}

// HelpFunc returns the help page of the command named by path (canonical names below the
// root; none for the root), or an error when there is no such command. Codegen generates a
// Help function of this type.
type HelpFunc func(path ...string) (string, error)

// WithHelp sets where [Context.Help] finds a command's help page. The generated NewProgram
// passes its own Help function. A nil help is ignored.
//
// Help is a program-level seam so that a command composed from another spec prints the page
// of the program it runs in, with that program's full command path and inherited flags.
func (p *Program) WithHelp(help HelpFunc) *Program {
	if help != nil {
		p.help = help
	}
	return p
}

// WithParser replaces the [Parser] that [Context.Parser] returns. [Context.Inputs] and the
// [InputReader] always use the default parser, so this changes only what a handler gets from
// [Context.Parser]. A nil parser is ignored.
func (p *Program) WithParser(parser *Parser) *Program {
	if parser != nil {
		p.parser = parser
	}
	return p
}

// newRunContext builds a fresh per-invocation [Context] seeded with a copy of the program's
// dependencies, its streams and its seams.
func (p *Program) newRunContext() *Context {
	rtx := newContext()
	if p.rtx != nil {
		if svcs := p.rtx.cloneServices(); svcs != nil {
			rtx.services = svcs
		}
	}
	rtx.Stdin, rtx.Stdout, rtx.Stderr = p.stdin, p.stdout, p.stderr
	rtx.meta, rtx.readerFn = p.meta, p.readerFn
	rtx.version, rtx.parser, rtx.help = p.version, p.parser, p.help
	rtx.outputChecks = p.outputChecks
	return rtx
}

// WithResolver overrides the resolve phase: argv to invocation target, plus the argv the
// parsers later see. A resolver that rewrites tokens should rewrite argv and delegate to
// [DefaultResolver], so routing and parsing agree. A resolver error is reported as a fault
// ([CategoryInternal] unless the error carries a category) and fails the run.
//
// Completion walks the [Definition], so an alias known only to the resolver is dispatchable
// but not completed. A nil resolver is ignored.
func (p *Program) WithResolver(fn Resolver) *Program {
	if fn != nil {
		p.resolver = fn
	}
	return p
}

// WithLifecycle overrides the run phase's plan: which hooks run, in what pairing and order
// (see [Lifecycle] and [DefaultLifecycle]). Halting, the reverse teardown unwind, panic
// handling and exit-code resolution are unchanged. A nil lifecycle is ignored.
func (p *Program) WithLifecycle(fn Lifecycle) *Program {
	if fn != nil {
		p.lifecycle = fn
	}
	return p
}

// Execute runs the arguments set by [Program.WithArgs] (default os.Args[1:]) through
// [Program.Run] and passes the resulting code to the exit action ([os.Exit] by default; see
// [Program.WithExit]).
//
// The returned error joins every [Context.RecordError] value and every captured fault with
// errors.Join, so errors.Is and errors.As reach each one. It is returned only when the exit
// action returns; under os.Exit the process ends first. The reporter has already reported
// the outcome by then; the error lets an embedding host act on the failure.
//
// # Signals
//
// With no [Program.WithContext], rotini traps os.Interrupt and syscall.SIGTERM. The first
// signal halts the lifecycle as [Context.HaltWithCode] does (forward progress stops, every
// begun teardown hook runs) with exit code 128+signum. A second signal calls the exit action
// with 130 immediately. See [Program.WithoutSignalHandling] and [Program.WithSignals].
func (p *Program) Execute() error {
	code, err := p.Run(p.args)
	p.exit(code)
	return err
}

// Run dispatches one invocation of argv and returns its exit code and error (see
// [Program.Execute]). It resolves the invoked command, executes a declared plugin if one was
// selected, and otherwise runs the lifecycle; inputs are parsed only when a handler calls
// [Context.Inputs]. Run never ends the process.
//
// Each call gets a fresh [Context]. Dependencies registered with [Program.WithDependency] are
// seeded into every run; one set with [Context.SetDependency] stays local to its run.
//
// With no supplied context, Run installs and removes the signal trap on every call (about
// 30µs). A host dispatching in a loop uses [Program.RunContext] or
// [Program.WithoutSignalHandling].
//
// # Concurrency
//
// Run is safe for concurrent use once configuration is complete. Each run's records, exit
// state and run-local dependencies are its own. Shared, and synchronized by the host:
//
//   - The handlers value given to [NewProgram], whose methods are called from each run's
//     goroutine.
//   - The program's streams.
//
// With no supplied context, each concurrent run installs its own trap and all of them observe
// a signal. A concurrent host passes its own context ([Program.RunContext]) or disables the
// trap with [Program.WithoutSignalHandling].
func (p *Program) Run(argv []string) (int, error) {
	if p.ctx != nil {
		return p.runWith(p.ctx, true, argv)
	}
	return p.runWith(context.Background(), false, argv)
}

// RunContext is [Program.Run] under ctx, for this invocation only; the program is not
// modified. As with [Program.WithContext], supplying a context leaves signal handling to the
// caller unless [Program.WithSignals] was set. A nil ctx returns exit code 1 and an
// [ErrInternal] error without running.
func (p *Program) RunContext(ctx context.Context, argv []string) (int, error) {
	if ctx == nil {
		return 1, InternalError(errors.New("rotini: RunContext called with a nil context"))
	}
	return p.runWith(ctx, true, argv)
}

// runWith is the shared core of Run and RunContext. hasCtx reports whether the caller
// supplied the context, which decides the default signal trap.
func (p *Program) runWith(runCtx context.Context, hasCtx bool, argv []string) (int, error) {
	if len(argv) > 0 && argv[0] == completeCommand {
		return p.Complete(argv[1:], p.completion)
	}

	// signalAuto traps iff the caller supplied no context; WithSignals and
	// WithoutSignalHandling override that.
	ctx := runCtx
	trap := !hasCtx
	switch p.signalMode {
	case signalOff:
		trap = false
	case signalOn:
		trap = true
	}

	// Derive a cancelable child only when rotini owns the context or the trap needs a cancel;
	// otherwise the caller's context passes through and cancellation is observed via ctx.Err().
	var cancel context.CancelCauseFunc
	if !hasCtx || trap {
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
	}

	if trap {
		defer p.installTrap(cancel)()
	}

	resolve := p.resolver
	if resolve == nil {
		resolve = DefaultResolver
	}
	rtx := p.newRunContext()

	res, err := resolve(p.def, argv)
	if err != nil {
		rtx.recordFault(asFault(internalUnlessTagged(fmt.Errorf("resolve: %w", err))))
		return p.settle(ctx, rtx)
	}
	if res.Plugin != nil {
		return p.execPlugin(ctx, rtx, res.Chain, res.Plugin)
	}
	if len(res.Chain) == 0 {
		// An empty chain violates the resolver contract.
		rtx.recordFault(asFault(InternalError(errors.New("resolver returned an empty chain; the root frame is always resolvable"))))
		return p.settle(ctx, rtx)
	}

	rtx.Argv = argv
	if res.Argv != nil {
		rtx.Argv = res.Argv
	}
	// Cloned so marking the invoked command never writes into a slice a custom resolver may
	// share between runs.
	chain := slices.Clone(res.Chain)
	markInvoked(chain)
	rtx.chain = chain
	return p.dispatch(ctx, chain, rtx)
}

// installTrap starts rotini's signal trap for one run and returns the function that removes
// it. The first signal cancels the run with the signal's exit code as the cause, so dispatch
// halts and teardown runs; a second calls the exit action with forceExitCode, skipping the
// remaining teardown.
func (p *Program) installTrap(cancel context.CancelCauseFunc) (stop func()) {
	sigs := p.signalSet
	if len(sigs) == 0 {
		sigs = trapSignals
	}
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, sigs...)

	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigCh:
			cancel(exitCodeError{code: signalExitCode(s)})
		case <-done:
			return
		}
		select {
		case <-sigCh:
			p.exit(forceExitCode)
		case <-done:
		}
	}()
	return func() {
		close(done)
		signal.Stop(sigCh)
	}
}

// internalUnlessTagged tags err [CategoryInternal] unless its producer already categorized it:
// a custom resolver may legitimately raise a usage error, and that must survive.
func internalUnlessTagged(err error) error {
	if CategoryOf(err) != CategoryNone {
		return err
	}
	return InternalError(err)
}

// asFault wraps a rotini-detected fault for the panics channel, with no stack.
func asFault(err error) *PanicError { return &PanicError{Value: err} }

// resolveHandlers obtains each command's [Handler] by calling the handler-set method named by
// its Handler field. A wiring failure is returned, never panicked.
func (p *Program) resolveHandlers(chain []Command) ([]Handler, *WiringError) {
	hv := reflect.ValueOf(p.handlers)
	if !hv.IsValid() {
		// MethodByName on the zero Value panics; report a nil handler set as wiring instead.
		return nil, &WiringError{
			Msg: "no handlers: NewProgram was given a nil handlers value",
		}
	}
	handlers := make([]Handler, len(chain))
	for i, f := range chain {
		m := hv.MethodByName(f.Handler)
		if !m.IsValid() {
			return nil, &WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("no handler for command %q (missing method %q)", f.Name, f.Handler),
			}
		}
		out := m.Call(nil)
		h, ok := reflect.TypeAssert[Handler](out[0])
		if !ok || h == nil {
			return nil, &WiringError{
				Command: f.Name, Handler: f.Handler,
				Msg: fmt.Sprintf("handler %q does not implement Handler", f.Handler),
			}
		}
		handlers[i] = h
	}
	return handlers, nil
}

// dispatch resolves each command in the chain to its [Handler], asks the lifecycle planner
// for the step plan, and executes it as a balanced LIFO setup/teardown:
//
//   - Forward: each step's Do in plan order, halting the moment a hook calls Halt, HaltWith,
//     HaltWithCode or Exit, panics, or ctx is canceled (by a trapped signal or the caller).
//   - Unwind: the Undo of every step whose Do began, in reverse, to completion. A panic or
//     HaltWithCode inside an Undo neither aborts the rest nor displaces the first failure; a
//     hard Exit skips what remains, and so does a panic under WithTeardownOnPanic(false).
//
// Recovered panics reach the reporter after teardown. A canceled run context becomes a
// [Context.HaltWithCode] between forward hooks (teardown still runs; the code is the
// cancellation cause's, or 0). The conversion happens on the dispatch goroutine, so rtx keeps
// a single writer.
func (p *Program) dispatch(ctx context.Context, chain []Command, rtx *Context) (int, error) {
	handlers, werr := p.resolveHandlers(chain)
	if werr != nil {
		rtx.recordFault(asFault(werr))
		return p.settle(ctx, rtx)
	}

	plan := p.lifecycle
	if plan == nil {
		plan = DefaultLifecycle
	}
	steps := plan(chain, handlers)

	// run wraps every hook so a panic leaves the lifecycle in control, except with
	// panicRecover=false and teardownOnPanic=false, where the hook runs unguarded and the panic
	// keeps its original stack. panicValue holds the first panic when it is to be re-raised
	// after teardown rather than reported.
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

	// halt reports whether forward progress should stop, converting a cancellation into
	// HaltWithCode with the cause's exit code.
	halt := func() bool {
		if stopped, _ := rtx.stopState(); !stopped && ctx.Err() != nil {
			rtx.HaltWithCode(canceledExitCode(ctx))
		}
		stopped, _ := rtx.stopState()
		return stopped || panicked
	}
	exitNow := func() bool {
		_, now := rtx.stopState()
		return now
	}

	// Forward. began records how far the plan got, so the unwind covers exactly the begun steps.
	began := 0
	for _, s := range steps {
		began++
		// A nil Do is a step with only a teardown.
		if s.Do != nil {
			run(s.Do)
		}
		if halt() {
			break
		}
	}

	// Unwind. Both guards are re-checked per step, so an Exit or panic from within a teardown
	// hook stops the rest too.
	for i := began - 1; i >= 0 && !exitNow() && (p.teardownOnPanic || !panicked); i-- {
		if steps[i].Undo != nil {
			run(steps[i].Undo)
		}
	}

	// panicRecover=false, teardownOnPanic=true: the panic was caught only so teardown could
	// run; re-raise it.
	if panicked && !p.panicRecover {
		panic(panicValue)
	}

	return p.settle(ctx, rtx)
}
