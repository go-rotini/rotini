package rotini

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sync"
)

// The per-invocation [Context]: the service registry, the program's streams, the
// resolved chain, and the outcome recording a handler reports through. One is built
// per [Program.Run] — records and exit state never leak between invocations.

// ErrServiceNotFound is the sentinel reported when a registry key is unbound — the
// [MustGet] panics a [*ServiceError] wrapping it, which the runtime
// recovers and routes to the funnel (a missing service is rotini's
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

// Error renders the missing- or mistyped-service fault as a single line.
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

	services    map[string]any
	chain       []ResolvedCommand // resolved command path, root → leaf
	exitCode    int               // process exit code requested via [Context.SignalExit]/[Context.Exit] (first non-zero wins; the funnel overrides)
	stopped     bool              // an exit was requested; forward progress (setup/PreRun/Run) halts
	exitNow     bool              // [Context.Exit] (hard) was called: skip remaining teardown too
	funnelStage bool              // the run has settled and the funnel is executing: [Context.Exit] now OVERRIDES the exit code (the funnel is the final authority); [Context.SignalExit] is a no-op
	infos       []string          // informational messages recorded this run via [Context.RecordInfo]; handed to the funnel
	recorded    []error           // errors recorded this run via [Context.RecordError]; handed to the funnel
	warnings    []error           // warnings recorded this run via [Context.RecordWarning]; handed to the funnel
	successes   []string          // successes recorded this run via [Context.RecordSuccess]; handed to the funnel
	faults      []*PanicError     // recovered panics + rotini-detected faults; set by the lifecycle (NOT publicly recordable); handed to the funnel
}

// cloneServices returns a snapshot of the registry's bindings. The runtime seeds
// every run's Context from the Program's registry through it (see
// [Program.newRunContext]), so a service bound BEFORE the run ([Program.Bind]) is
// visible to every run, while one bound DURING a run stays local to that run.
func (rtx *Context) cloneServices() map[string]any {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return maps.Clone(rtx.services)
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
// or [MustGet] (panics → the funnel) for type-safe retrieval.
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
// verdict. SignalExit records no error — it is a clean, deliberate stop,
// not an error. The process exits with the recorded code once the lifecycle,
// teardown included, completes. For an abort that skips pending teardown, use
// [Context.Exit].
//
// SignalExit is a NO-OP inside the funnel ([Program.WithFunnel]): the lifecycle and
// its teardown have already run, so there is no forward progress to stop. To set the
// exit code from the funnel, use [Context.Exit] (which the funnel is allowed to use to
// override any code the lifecycle set).
func (rtx *Context) SignalExit(code int) {
	if rtx == nil || rtx.funnelStage {
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
// is still captured and handed to the funnel ([Program.WithFunnel]), so a handler's
// Exit cannot silently swallow an in-flight panic — though the FUNNEL itself, as the
// final authority, may.
//
// Inside the funnel, Exit is the way to set the final exit code and OVERRIDES any code
// the lifecycle set (the funnel is the last word — first-non-zero-wins no longer
// applies). During the lifecycle it keeps first-non-zero-wins.
func (rtx *Context) Exit(code int) {
	if rtx == nil {
		return
	}
	rtx.stopped = true
	rtx.exitNow = true
	if rtx.funnelStage {
		rtx.exitCode = code // the funnel is the final authority — override any prior code
		return
	}
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// RecordInfo records msg as an informational message of this run — neutral
// output the end-user wants surfaced (progress, context, a note), distinct from a
// success message only by intent. Like the other record calls it neither prints
// nor stops the lifecycle: it appends, and the program's funnel
// ([Program.WithFunnel]) receives the recorded infos as a slice once the run
// settles. Recording an info never sets the exit code. An empty msg is ignored.
func (rtx *Context) RecordInfo(msg string) {
	if rtx == nil || msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.infos = append(rtx.infos, msg)
}

// copyInfos snapshots the informational messages recorded this run (via
// [Context.RecordInfo]), in recording order, as a copy. Unexported: infos are
// PRIVATE and surface only as the slice handed to the funnel.
func (rtx *Context) copyInfos() []string {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.infos) == 0 {
		return nil
	}
	out := make([]string, len(rtx.infos))
	copy(out, rtx.infos)
	return out
}

// RecordError records err as one of THIS run's errors — the end-user's own
// failures, to be reported once the lifecycle settles. It does NOT print and
// does NOT stop the lifecycle — a handler accumulates one or more errors with
// RecordError (it may call it any number of times, across any hook), then
// chooses HOW to stop independently: [Context.SignalExit] for a graceful stop
// that still unwinds teardown, or [Context.Exit] to skip teardown. Either way —
// and even if neither is called — the program's funnel ([Program.WithFunnel])
// receives the recorded errors as a slice once the run settles. A nil err is
// ignored.
//
// This is the error channel of five outcome channels handed to the one funnel.
// Recovered panics and rotini-detected faults (a wiring mismatch, a resolver
// fault, a [MustGet] on a missing service) are NOT recorded here — the lifecycle
// captures them as the funnel's panics slice; infos/successes/warnings have their
// own channels ([Context.RecordInfo] / [Context.RecordSuccess] /
// [Context.RecordWarning]).
//
//	inputs, err := rotini.Collect[MycliInputs](rtx)
//	if err != nil {
//	    rtx.RecordError(err)
//	    rtx.SignalExit(1) // graceful; or rtx.Exit(…) to skip teardown
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

// copyErrors snapshots the errors recorded this run (via [Context.RecordError]),
// in recording order, as a copy. It is unexported: the recorded errors are
// PRIVATE — they surface only as the slice the runtime hands to the funnel
// ([Program.WithFunnel]), never through a drainable accessor.
func (rtx *Context) copyErrors() []error {
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
// stops the lifecycle: it appends, and the program's funnel ([Program.WithFunnel])
// receives the recorded warnings as a slice once the run settles. A warning is an
// error value (so it can be typed and branched with errors.As, and secrets stay
// redacted), but it never raises the exit code. A nil warn is ignored.
func (rtx *Context) RecordWarning(warn error) {
	if rtx == nil || warn == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.warnings = append(rtx.warnings, warn)
}

// RecordSuccess records msg as a success message of this run — what went right,
// for the program's funnel ([Program.WithFunnel]) to present. Like the other
// record calls it neither prints nor stops the lifecycle: it appends, and the
// funnel receives the recorded messages as a slice once the run settles.
// Recording a success does not by itself set the exit code (a clean run is
// already 0). An empty msg is ignored.
func (rtx *Context) RecordSuccess(msg string) {
	if rtx == nil || msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.successes = append(rtx.successes, msg)
}

// copyWarnings snapshots the warnings recorded this run (via
// [Context.RecordWarning]), in recording order, as a copy. Unexported: warnings
// are PRIVATE and surface only as the slice handed to the funnel.
func (rtx *Context) copyWarnings() []error {
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

// copySuccesses snapshots the success messages recorded this run (via
// [Context.RecordSuccess]), in recording order, as a copy. Unexported:
// successes are PRIVATE and surface only as the slice handed to the funnel.
func (rtx *Context) copySuccesses() []string {
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

// copyFaults snapshots the recovered panics + rotini-detected faults captured
// this run, in capture order, as a copy. Unexported: faults are PRIVATE (there
// is no public record call either — the lifecycle captures them) and surface
// only as the panics slice handed to the funnel ([Program.WithFunnel]).
func (rtx *Context) copyFaults() []*PanicError {
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
// fault channel (handed to the funnel as its panics slice). It is unexported on
// purpose: faults are the lifecycle's to capture, never the handler's to record.
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
// funnel (as a fault) instead of handling it inline.
func Get[T any](rtx *Context, key string) (T, bool) {
	v, ok := rtx.Value(key).(T)
	return v, ok
}

// MustGet returns the service bound under key as T, or panics with a
// [*ServiceError] (unwrapping to [ErrServiceNotFound]) when it is absent or not a
// T. The panic is intentional and recoverable: the runtime recovers it inside
// dispatch and routes it through the program's funnel — so a handler that
// cannot run without a service reaches for MustGet instead of handling a miss
// inline:
//
//	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)
//	var in MycliInputs
//	err := parser.Parse(rtx, &in)
func MustGet[T any](rtx *Context, key string) T {
	v, ok := Get[T](rtx, key)
	if !ok {
		panic(&ServiceError{Key: key})
	}
	return v
}
