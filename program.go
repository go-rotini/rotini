package rotini

import (
	"context"
	"errors"
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

// Execute parses the arguments, dispatches to the resolved command, runs its
// lifecycle, and exits the process with the resulting status code.
func (p *program) Execute() {
	os.Exit(p.run(p.args))
}

// run is the testable core of Execute: it returns the process exit code instead
// of calling os.Exit. 0 = success, 2 = usage error, 1 = other failure.
func (p *program) run(argv []string) int {
	res, err := parse(p.def, argv)
	if err != nil {
		fmt.Fprintf(p.stderr, "%s: %s\n", p.def.Name, err)
		var ue *usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(p.stderr, "\nRun '%s --help' for usage.\n", p.def.Name)
			return 2
		}
		return 1
	}

	if res.help || bareNamespace(p.def, argv) {
		printUsage(p.stdout, res.chain)
		return 0
	}

	rtx := NewRtx()
	rtx.bindParsed(res.parsed)
	return p.dispatch(res.chain, rtx)
}

// bareNamespace reports whether the program was invoked with no arguments while
// the root command branches into sub-commands — the conventional "print help"
// case (git, kubectl). Non-root commands always dispatch and decide for
// themselves.
func bareNamespace(def Definition, argv []string) bool {
	return len(argv) == 0 && len(def.Commands) > 0
}

// dispatch resolves each command in the chain to its [CommandHandlers] (by the
// recorded Handler method name, via reflection on the aggregate handlers) and
// runs the lifecycle: CascadingPreRun root→leaf, then the leaf's PreRun, Run,
// and PostRun, then CascadingPostRun leaf→root.
func (p *program) dispatch(chain []frame, rtx *Rtx) int {
	hv := reflect.ValueOf(p.handlers)
	handlers := make([]CommandHandlers, len(chain))
	for i, f := range chain {
		m := hv.MethodByName(f.handler)
		if !m.IsValid() {
			fmt.Fprintf(p.stderr, "%s: no handler for command %q (missing method %q)\n", p.def.Name, f.name, f.handler)
			return 1
		}
		out := m.Call(nil)
		h, ok := out[0].Interface().(CommandHandlers)
		if !ok || h == nil {
			fmt.Fprintf(p.stderr, "%s: handler %q does not implement CommandHandlers\n", p.def.Name, f.handler)
			return 1
		}
		handlers[i] = h
	}

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

// Exit records a non-zero exit code for the program and stops the current
// command's remaining leaf hooks (PreRun/Run/PostRun); CascadingPostRun still
// runs so cleanup is not skipped. The process exits with code once the lifecycle
// completes. It is the handler-facing way to fail a command until lifecycle
// hooks themselves return errors.
func Exit(rtx Context, code int) {
	if rtx == nil {
		return
	}
	rtx.exitCode = code
	rtx.stopped = true
}
