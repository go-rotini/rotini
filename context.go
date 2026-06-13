package rotini

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// ErrServiceNotFound is the sentinel reported when a registry key is unbound — the
// [MustGet] panics a [*ServiceError] wrapping it, which the runtime
// recovers and routes to the OnPanic funnel (a missing service is rotini's
// "should never happen", not the end-user's error). A funnel classifies it with errors.Is:
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
	// nil (a standalone [NewContextFor] defaults them to os.Std*).
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Args is the raw argument vector for this invocation — os.Args[1:], or the
	// override from [Program.WithArgs] — with everything after the resolved
	// command path still present, so a handler can run its own parser instead of
	// [Parser.Parse]. It is the live slice, not a copy: set before dispatch and
	// not mutated by the runtime thereafter; a handler that mutates it changes
	// what every later read (including the opt-in Parser/Binder) sees, and owns
	// the consequences.
	Args []string

	services  map[string]any
	chain     []ResolvedCommand // resolved command path, root → leaf
	exitCode  int               // process exit code requested via [Context.SignalExit]/[Context.Exit] (first non-zero wins)
	stopped   bool              // an exit was requested; forward progress (setup/PreRun/Run) halts
	exitNow   bool              // [Context.Exit] (hard) was called: skip remaining teardown too
	recorded  []error           // errors recorded this run via [Context.RecordError]; drained by the OnError funnel
	warnings  []error           // warnings recorded this run via [Context.RecordWarning]; drained by the OnWarning funnel
	successes []string          // successes recorded this run via [Context.RecordSuccess]; drained by the OnSuccess funnel
	faults    []*PanicError     // recovered panics + rotini-detected faults; set by the lifecycle (NOT publicly recordable); drained by the OnPanic funnel
}

// newContext returns an empty [Context] with an initialized registry and no
// resolved command. The runtime builds one per invocation and fills in the
// resolved chain before dispatch; [NewContextFor] is the public entry for
// tests and standalone tooling (pass an empty Definition for a bare registry).
func newContext() *Context {
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
//	rtx := rotini.NewContextFor(def, []string{"build", "x.yaml"}).Bind(rotini.KeyParser, rotini.NewParser())
//	var in appInputs
//	err := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser).Parse(rtx, &in)
//
// To drive a whole *generated* program end-to-end (the usual handler test), construct it
// with the generated NewProgram and run it under a recording exit + capture streams —
// see [Program.WithExit]/[Program.WithStdout]/[Program.WithStderr] — rather than building
// a context by hand; the generated command tree is unexported.
//
// A remote/co-located token resolves to as much of the chain as precedes it; the
// runtime would exec the sibling binary, which NewContextFor does not.
func NewContextFor(def Definition, argv []string) *Context {
	rtx := newContext()
	chain, _ := resolveChain(def, argv)
	rtx.Args = argv
	rtx.chain = chain
	return rtx
}

// Bind associates value with key, overwriting any prior binding. It returns the
// receiver so calls can be chained:
//
//	cmd.Program.Bind(rotini.KeyParser, customParser).Bind(rotini.KeyBinder, customBinder).Execute()
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

// BindIfAbsent binds value under key only if key is not already bound, and returns the
// receiver to chain. It is the registered-default form of [Bind]: a handler binds its work
// dependency's real implementation with BindIfAbsent so production materializes the real one
// in the registry, while a caller (e.g. a test) that bound a double under the same key
// earlier keeps it — and either way the dependency is resolvable from the registry (with the
// standard [MustGet]), not a value hidden inline. The check-and-set is atomic. Use [Bind] to
// overwrite unconditionally.
//
//	rtx.BindIfAbsent("generate", internal.Generate)
//	gen := rotini.MustGet[internal.GenerateFn](rtx, "generate")
func (rtx *Context) BindIfAbsent(key string, value any) *Context {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.services == nil {
		rtx.services = make(map[string]any)
	}
	if _, ok := rtx.services[key]; !ok {
		rtx.services[key] = value
	}
	return rtx
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
//	parser, ok := rtx.Value(rotini.KeyParser).(*rotini.Parser)
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// Value reports a miss as nil and never panics; prefer the typed [Get] (comma-ok)
// or [MustGet] (panics → OnPanic funnel) for type-safe retrieval.
func (rtx *Context) Value(key string) any {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.services[key]
}

// SignalExit records the program's exit code and stops the lifecycle's forward
// progress — no further setup hook (CascadingPreRun), PreRun, or Run runs.
// Teardown is unaffected: every PostRun/CascadingPostRun whose paired setup hook
// began still runs, in reverse, so cleanup is never skipped. The first non-zero
// code wins, so a later SignalExit (e.g. from a teardown hook) cannot change the
// verdict. SignalExit does not trigger OnError — it is a clean, deliberate stop,
// not an error. The process exits with the recorded code once the lifecycle,
// teardown included, completes. For an abort that skips pending teardown, use
// [Context.Exit].
func (rtx *Context) SignalExit(code int) {
	if rtx == nil {
		return
	}
	rtx.stopped = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// Exit records the program's exit code and stops the lifecycle immediately — no
// further hook runs, teardown included: any PostRun/CascadingPostRun still
// pending is skipped. Use it for an abort-now path where remaining cleanup must
// not run; prefer [Context.SignalExit] for an orderly stop that still unwinds
// every begun teardown hook. The first non-zero code wins, and Exit does not
// itself trigger the panic funnel — it is a deliberate stop, not an error.
//
// Exit skips teardown, not fault reporting: a panic already recovered before Exit
// is still routed to [Program.WithOnPanicFn] (and the code is never masked to 0),
// so Exit cannot silently swallow an in-flight panic.
func (rtx *Context) Exit(code int) {
	if rtx == nil {
		return
	}
	rtx.stopped = true
	rtx.exitNow = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// RecordError records err as one of THIS run's errors — the end-user's own
// failures, to be reported once the lifecycle settles. It does NOT print and
// does NOT stop the lifecycle — a handler accumulates one or more errors with
// RecordError (it may call it any number of times, across any hook), then
// chooses HOW to stop independently: [Context.SignalExit] for a graceful stop
// that still unwinds teardown, or [Context.Exit] to skip teardown. Either way —
// and even if neither is called — the program's OnError funnel
// ([Program.WithOnErrorFn]) fires once at the end whenever any error was
// recorded, draining them via [Context.Errors]. A nil err is ignored.
//
// This is the error channel of four outcome channels. Recovered panics and
// rotini-detected faults (a wiring mismatch, a resolver fault, a [MustGet] on a
// missing service) are NOT recorded here — the lifecycle captures them and the
// OnPanic funnel reports them; recorded successes/warnings have their own
// channels ([Context.RecordSuccess] / [Context.RecordWarning]).
//
//	inputs, err := rotini.Collect[cmdgen.MycliInputs](rtx)
//	if err != nil {
//	    rtx.RecordError(err)
//	    rtx.SignalExit(rotini.ExitUsage) // graceful; or rtx.Exit(…) to skip teardown
//	    return
//	}
func (rtx *Context) RecordError(err error) {
	if rtx == nil || err == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.recorded = append(rtx.recorded, err)
}

// Errors returns the errors recorded this run via [Context.RecordError] (and the
// framework/panic errors rotini records the same way), in recording order. It
// is how an OnError funnel drains and pretty-prints each error individually;
// the funnel's err argument is their [errors.Join], so one [CategoryOf] /
// errors.Is / errors.As call covers the whole set. The returned slice is a
// copy — mutating it does not affect the Context.
func (rtx *Context) Errors() []error {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.recorded) == 0 {
		return nil
	}
	out := make([]error, len(rtx.recorded))
	copy(out, rtx.recorded)
	return out
}

// RecordWarning records warn as a non-fatal warning of this run — something the
// end-user should know about that did NOT fail the command (a deprecation, a
// fallback, a skipped item). Like [Context.RecordError] it neither prints nor
// stops the lifecycle: it appends, and the program's OnWarning funnel
// ([Program.WithOnWarningFn]) fires once at the end whenever any warning was
// recorded, draining them via [Context.Warnings]. A warning is an error value
// (so it can be typed and branched with errors.As, and secrets stay redacted),
// but it never raises the exit code. A nil warn is ignored.
func (rtx *Context) RecordWarning(warn error) {
	if rtx == nil || warn == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.warnings = append(rtx.warnings, warn)
}

// RecordSuccess records msg as a success message of this run — what went right,
// for the program's OnSuccess funnel ([Program.WithOnSuccessFn]) to present.
// Like the other record calls it neither prints nor stops the lifecycle: it
// appends, and OnSuccess fires once at the end whenever any success was
// recorded, draining them via [Context.Successes]. Recording a success does not
// by itself set the exit code (a clean run is already 0). An empty msg is
// ignored.
func (rtx *Context) RecordSuccess(msg string) {
	if rtx == nil || msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.successes = append(rtx.successes, msg)
}

// Warnings returns the warnings recorded this run via [Context.RecordWarning],
// in recording order — how an OnWarning funnel drains and prints each. The
// returned slice is a copy; mutating it does not affect the Context.
func (rtx *Context) Warnings() []error {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.warnings) == 0 {
		return nil
	}
	out := make([]error, len(rtx.warnings))
	copy(out, rtx.warnings)
	return out
}

// Successes returns the success messages recorded this run via
// [Context.RecordSuccess], in recording order — how an OnSuccess funnel drains
// and prints each. The returned slice is a copy; mutating it does not affect the
// Context.
func (rtx *Context) Successes() []string {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.successes) == 0 {
		return nil
	}
	out := make([]string, len(rtx.successes))
	copy(out, rtx.successes)
	return out
}

// Panics returns the recovered panics and rotini-detected faults captured this
// run, in capture order — what an OnPanic funnel ([Program.WithOnPanicFn])
// reports. Unlike errors/warnings/successes there is NO public record call: the
// lifecycle captures a recovered panic (or routes a detected wiring/resolver
// fault) here itself, and that capture is the funnel's signal. The returned
// slice is a copy; mutating it does not affect the Context.
func (rtx *Context) Panics() []*PanicError {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.faults) == 0 {
		return nil
	}
	out := make([]*PanicError, len(rtx.faults))
	copy(out, rtx.faults)
	return out
}

// recordFault appends a recovered panic / rotini-detected fault to the private
// fault channel (drained by the OnPanic funnel). It is unexported on purpose:
// faults are the lifecycle's to capture, never the handler's to record.
func (rtx *Context) recordFault(pe *PanicError) {
	if rtx == nil || pe == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.faults = append(rtx.faults, pe)
}

// Get returns the service bound under key as T — the typed, comma-ok form of the
// raw [Context.Value] (which returns any). ok is false when no service is bound
// under key or the bound value is not a T:
//
//	parser, ok := rotini.Get[*rotini.Parser](rtx, rotini.KeyParser)
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// It never panics; use [MustGet] to route a missing/wrong-type service through the
// OnPanic funnel instead of handling it inline.
func Get[T any](rtx *Context, key string) (T, bool) {
	v, ok := rtx.Value(key).(T)
	return v, ok
}

// MustGet returns the service bound under key as T, or panics with a
// [*ServiceError] (unwrapping to [ErrServiceNotFound]) when it is absent or not a
// T. The panic is intentional and recoverable: the runtime recovers it inside
// dispatch and routes it through the program's OnPanic funnel — so a handler that
// cannot run without a service reaches for MustGet instead of handling a miss
// inline:
//
//	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)
//	var in rtg.MycliInputs
//	err := parser.Parse(rtx, &in)
func MustGet[T any](rtx *Context, key string) T {
	v, ok := Get[T](rtx, key)
	if !ok {
		panic(&ServiceError{Key: key})
	}
	return v
}
