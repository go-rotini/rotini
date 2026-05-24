package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
)

type program struct {
	ctx      context.Context
	args     []string
	def      Definition
	handlers any
	rtx      *Rtx                                              // pre-seeded registry; user Bind calls land here
	onError  func(ctx context.Context, rtx Context, err error) // funnel for MustGet/panic failures; nil → defaultOnError
	stdout   io.Writer
	stderr   io.Writer
}

// NewProgram wires a generated program's command tree (the rtg [Definition]) and
// its aggregate handler set (the rtg ProgramHandlers implementation) to the
// rotini runtime. handlers is any so the runtime need not import the generated
// framework package; dispatch resolves the per-command handlers from it at
// execution time, via the Handler names recorded in def.
func NewProgram(def Definition, handlers any) *program {
	return &program{
		ctx:      context.Background(),
		args:     os.Args[1:],
		def:      def,
		handlers: handlers,
		rtx:      NewRtx(),
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
}

// WithArguments overrides the argument vector (defaults to os.Args[1:]).
func (p *program) WithArguments(args []string) *program {
	if args != nil {
		p.args = args
	}
	return p
}

// Bind registers a service on the program's registry under key, overwriting any
// prior binding, and returns the receiver so it chains with [program.OnError] and
// [program.WithArguments] before [program.Execute]. It is the dependency-injection
// seam: bind a real implementation in production or a double in tests, with the
// same handler code retrieving it via [Get]/[MustGet]. Binding "parser" overrides
// the default [Parser] that [Parse] uses.
func (p *program) Bind(key string, value any) *program {
	p.rtx.Bind(key, value)
	return p
}

// OnError sets the funnel that handles a [MustGet] failure or any panic raised
// inside a hook: the runtime recovers it during dispatch, calls fn(rtx, err), and
// exits the process with the int fn returns. It is the single place to classify
// (errors.Is/errors.As), log (file, error-tracking service), and print errors in
// the CLI's own style. With no funnel set, the default prints the error to stderr
// and returns 1. OnError returns the receiver so it chains with [program.Bind].
func (p *program) OnError(fn func(ctx context.Context, rtx Context, err error)) *program {
	p.onError = fn
	return p
}

// Execute resolves the command, runs its lifecycle, and exits the process with
// the resulting status code.
func (p *program) Execute() {
	os.Exit(p.run(p.args))
}

// run is the testable core of Execute: it resolves the invoked command (no eager
// flag parsing — that is the handler's opt-in via [Parse]), execs a remote
// sub-command if one was selected, otherwise builds the per-invocation [Rtx] and
// dispatches the lifecycle. It returns the process exit code instead of calling
// os.Exit. The only retained protocol intercept is the hidden __complete entry
// the generated shell scripts invoke.
func (p *program) run(argv []string) int {
	if len(argv) > 0 && argv[0] == completeCommand {
		for _, c := range complete(p.def, argv[1:]) {
			fmt.Fprintln(p.stdout, c)
		}
		return 0
	}

	chain, remote := resolveChain(p.def, argv)
	if remote != nil {
		return p.execRemote(remote)
	}

	rtx := p.rtx
	if rtx == nil {
		rtx = NewRtx()
	}
	rtx.args = argv
	rtx.chain = chain
	rtx.onError = p.onError
	if rtx.onError == nil {
		rtx.onError = p.defaultOnError
	}
	return p.dispatch(chain, rtx)
}

// defaultOnError is the OnError funnel used when the program supplies none: it
// prints the error to stderr and fails with exit code 1.
func (p *program) defaultOnError(_ context.Context, rtx Context, err error) {
	fmt.Fprintf(p.stderr, "%s: %v\n", p.def.Name, err)
	rtx.Exit(1)
}

// dispatch resolves each command in the chain to its [CommandHandlers] (by the
// recorded Handler method name, via reflection on the aggregate handlers) and
// runs the lifecycle: CascadingPreRun root→leaf, then the leaf's PreRun, Run,
// and PostRun, then CascadingPostRun leaf→root. A [MustGet] failure or any panic
// raised inside a hook is recovered and routed through the registry's OnError
// funnel, whose returned code becomes the process exit code.
func (p *program) dispatch(chain []ResolvedCommand, rtx *Rtx) (code int) {
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

	// A MustGet failure or any panic from a hook unwinds to here, skipping the
	// `return rtx.exitCode` below — so the funnel runs and we lift its exit code
	// (set via rtx.Exit) into the named return ourselves. The panic path is always
	// a failure: if the funnel left the code at 0, floor it to 1.
	defer func() {
		if r := recover(); r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("%v", r)
			}
			rtx.onError(p.ctx, rtx, err)
			code = rtx.exitCode
			if code == 0 {
				code = 1
			}
		}
	}()

	leaf := handlers[len(handlers)-1]
	// CascadingPreRun (root→leaf), then the leaf's Pre/Run/Post — short-circuited
	// once a hook calls Exit. CascadingPostRun (leaf→root) always runs for cleanup.
	func() {
		for _, h := range handlers {
			if h.CascadingPreRun(p.ctx, rtx); rtx.stopped {
				return
			}
		}
		if leaf.PreRun(p.ctx, rtx); rtx.stopped {
			return
		}
		if leaf.Run(p.ctx, rtx); rtx.stopped {
			return
		}
		leaf.PostRun(p.ctx, rtx)
	}()
	for i := len(handlers) - 1; i >= 0; i-- {
		handlers[i].CascadingPostRun(p.ctx, rtx)
	}
	return rtx.exitCode
}
