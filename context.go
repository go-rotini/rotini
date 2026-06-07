package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// ErrServiceNotFound is the sentinel reported when a registry key is unbound — the
// [MustGet] panics a [*ServiceError] wrapping it, which the runtime
// recovers and routes to OnError. A funnel classifies it with errors.Is:
//
//	case errors.Is(err, rotini.ErrServiceNotFound):
var ErrServiceNotFound = errors.New("rotini: service not found")

// ServiceError reports a registry key that was requested but unbound (or bound to
// the wrong type) — the [MustGet] panics it. It unwraps to
// [ErrServiceNotFound]; recover the key with errors.As.
type ServiceError struct {
	Key string // the registry key that was requested
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("rotini: no service bound under key %q", e.Key)
}

// Unwrap exposes both the [ErrServiceNotFound] sentinel and [ErrInternal], so a missing
// service matches errors.Is for either — and [CategoryOf] classifies it as
// [CategoryInternal] (a wiring bug, not the end-user's fault).
func (e *ServiceError) Unwrap() []error { return []error{ErrServiceNotFound, ErrInternal} }

// Context is rotini's per-invocation context: the service registry plus the bits
// the runtime resolves before dispatch — the raw argument vector (read via
// [Context.Args]) and the resolved command chain (read via [Context.Chain]). One
// is built per invocation and passed (as *Context) into every handler hook, so all
// hooks share the same bindings and exit state.
//
// The registry is the dependency-injection seam: bind any service with
// [Context.Bind] (a real implementation in production, a double in tests) and
// retrieve it with [Context.Value] (or the typed [Get]/[MustGet]).
// Bindings persist for the lifetime of the Context. Opt-in input parsing (the
// [Parser]) reads [Context.Args]/[Context.Chain]; the runtime itself never
// parses flags.
//
// A Context is safe for concurrent registry access; reads and writes are guarded
// by an internal sync.RWMutex. Always pass it as a pointer — it must not be copied.
type Context struct {
	mu sync.RWMutex

	// Stdin, Stdout, and Stderr are the program's streams, mirroring those set via
	// [Program.WithStdin] / [Program.WithStdout] / [Program.WithStderr] (default os.Stdin /
	// os.Stdout / os.Stderr). A handler reads input and writes its output/diagnostics
	// through these rather than os.Std* directly, so the same handler code is exercised in
	// a test by configuring the Program's streams (the Binder reads its stdin channel from
	// [Context.Stdin] too). They are set before dispatch and not mutated thereafter; never
	// nil (a standalone [NewContext] defaults them to os.Std*).
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	services map[string]any
	args     []string                                           // raw argument vector for this invocation
	chain    []ResolvedCommand                                  // resolved command path, root → leaf
	onError  func(ctx context.Context, rtx *Context, err error) // funnel for MustGet/panic failures (see Program.OnError)
	exitCode int                                                // process exit code requested via [Context.Exit] (first non-zero wins)
	stopped  bool                                               // [Context.Exit] was called; forward progress (setup/PreRun/Run) halts, teardown still runs
}

// NewContext returns an empty [Context] with an initialized registry and no
// resolved command. The runtime builds one per invocation and fills in the
// resolved chain before dispatch; tests and standalone tooling can use it
// directly, or [NewContextFor] to also resolve a command.
func NewContext() *Context {
	return &Context{
		services: make(map[string]any),
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
}

// NewContextFor builds a [Context] with argv resolved against an explicit def — the
// same context the runtime hands a handler at dispatch (raw [Context.Args] + the
// resolved [Context.Chain]). It is for exercising the [Parser]/[Usage] helpers, or a
// single hook, against a Definition you construct:
//
//	def := rotini.Definition{Name: "app", Handler: "App", Commands: []rotini.CommandDef{ … }}
//	rtx := rotini.NewContextFor(def, []string{"build", "x.yaml"}).Bind("parser", rotini.NewParser())
//	var in appInputs
//	err := rotini.MustGet[*rotini.Parser](rtx, "parser").Parse(rtx, &in)
//
// To drive a whole *generated* program end-to-end (the usual handler test), construct it
// with the generated NewProgram and run it under a recording exit + capture streams —
// see [Program.WithExit]/[Program.WithStdout]/[Program.WithStderr] — rather than building
// a context by hand; the generated command tree is unexported.
//
// A remote/co-located token resolves to as much of the chain as precedes it; the
// runtime would exec the sibling binary, which NewContextFor does not.
func NewContextFor(def Definition, argv []string) *Context {
	rtx := NewContext()
	chain, _ := resolveChain(def, argv)
	rtx.args = argv
	rtx.chain = chain
	return rtx
}

// Bind associates value with key, overwriting any prior binding. It returns the
// receiver so calls can be chained:
//
//	cmd.Program.Bind("parser", customParser).Bind("binder", customBinder).Execute()
//
// Bind is safe for concurrent use.
func (rtx *Context) Bind(key string, value any) *Context {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.services == nil {
		rtx.services = make(map[string]any)
	}
	rtx.services[key] = value
	return rtx
}

// Has reports whether a binding exists under key.
func (rtx *Context) Has(key string) bool {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	_, ok := rtx.services[key]
	return ok
}

// Args returns the raw argument vector for this invocation: everything after the
// resolved command path is still present, so a handler can run its own parser
// instead of [Parser.Parse]. The slice is the runtime's; treat it as
// read-only.
func (rtx *Context) Args() []string {
	if rtx == nil {
		return nil
	}
	return rtx.args
}

// Chain returns the resolved command path for this invocation, root → leaf — the
// command tree the runtime descended to choose this handler. Opt-in tooling (the
// [Parser] and [Usage]) reads it to bind inputs and render help against
// the exact command whose handler ran. The slice is the runtime's; treat it as
// read-only.
func (rtx *Context) Chain() []ResolvedCommand {
	if rtx == nil {
		return nil
	}
	return rtx.chain
}

// Value returns the service bound under key, or nil if none is bound — the raw
// accessor, mirroring [context.Context.Value]. Callers type-assert to the expected
// type, using the comma-ok form to handle an unbound (or wrong-type) service:
//
//	parser, ok := rtx.Value("parser").(*rotini.Parser)
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// Value reports a miss as nil and never panics; prefer the typed [Get] (comma-ok)
// or [MustGet] (panics → OnError funnel) for type-safe retrieval.
func (rtx *Context) Value(key string) any {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.services[key]
}

// Exit records the program's exit code and stops the lifecycle's forward
// progress — no further setup hook (CascadingPreRun), PreRun, or Run runs.
// Teardown is unaffected: every PostRun/CascadingPostRun whose paired setup hook
// began still runs, in reverse, so cleanup is never skipped. The first non-zero
// code wins, so a later Exit (e.g. from a teardown hook) cannot change the
// verdict. Exit does not trigger OnError — it is a clean, deliberate stop, not an
// error. The process exits with the recorded code once the lifecycle, teardown
// included, completes.
func (rtx *Context) Exit(code int) {
	if rtx == nil {
		return
	}
	rtx.stopped = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// exitNow is the sentinel [Context.ExitNow] panics with; dispatch recovers it to
// unwind the entire handler chain — skipping teardown and OnError — and return the
// recorded exit code.
type exitNow struct{}

// ExitNow is the hard counterpart to [Context.Exit]: it aborts the lifecycle
// immediately, abandoning the current hook and skipping the rest of the chain —
// no remaining setup, work, OR teardown hook runs, and OnError is not called. The
// code is recorded as the final verdict (it overrides any prior Exit and is not
// floored) and the process exits with it.
//
// Prefer [Context.Exit], which stops forward progress but still runs teardown for
// cleanup; reach for ExitNow only when cleanup must be skipped. It is implemented
// as a recovered sentinel panic, so a handler that blanket-recover()s panics could
// swallow it — don't.
func (rtx *Context) ExitNow(code int) {
	if rtx == nil {
		return
	}
	rtx.exitCode = code
	rtx.stopped = true
	panic(exitNow{})
}

// Get returns the service bound under key as T — the typed, comma-ok form of the
// raw [Context.Value] (which returns any). ok is false when no service is bound
// under key or the bound value is not a T:
//
//	parser, ok := rotini.Get[*rotini.Parser](rtx, "parser")
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// It never panics; use [MustGet] to route a missing/wrong-type service through the
// OnError funnel instead of handling it inline.
func Get[T any](rtx *Context, key string) (T, bool) {
	v, ok := rtx.Value(key).(T)
	return v, ok
}

// MustGet returns the service bound under key as T, or panics with a
// [*ServiceError] (unwrapping to [ErrServiceNotFound]) when it is absent or not a
// T. The panic is intentional and recoverable: the runtime recovers it inside
// dispatch and routes it through the program's OnError funnel — so a handler that
// cannot run without a service reaches for MustGet instead of handling a miss
// inline:
//
//	parser := rotini.MustGet[*rotini.Parser](rtx, "parser")
//	var in rtg.MycliInputs
//	err := parser.Parse(rtx, &in)
func MustGet[T any](rtx *Context, key string) T {
	v, ok := Get[T](rtx, key)
	if !ok {
		panic(&ServiceError{Key: key})
	}
	return v
}
