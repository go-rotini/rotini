package rotini

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"sync"
)

// ErrServiceNotFound is the sentinel reported when a registry key is unbound.
// [Context.MustGet] panics a [*ServiceError] wrapping it, which the runtime recovers and
// routes to the funnel.
var ErrServiceNotFound = errors.New("rotini: service not found")

// ServiceError reports a registry key that was requested but unbound, or bound to the wrong
// type. It unwraps to [ErrServiceNotFound]; recover the key with errors.As.
type ServiceError struct {
	Key string // the registry key that was requested
}

// Error renders the fault as a single line.
func (e *ServiceError) Error() string {
	return fmt.Sprintf("rotini: no service bound under key %q", e.Key)
}

// Unwrap exposes both [ErrServiceNotFound] and [ErrInternal], so [CategoryOf] classifies a
// missing service as [CategoryInternal] — a wiring bug, not the user's fault.
func (e *ServiceError) Unwrap() []error { return []error{ErrServiceNotFound, ErrInternal} }

// Context is rotini's per-invocation context: the service registry, the program's streams, and
// what the runtime resolved before dispatch — the raw argument vector ([Context.Args]) and the
// resolved command chain ([Context.Chain]). One is built per [Program.Run] and passed to every
// hook, so all hooks share the same bindings and exit state and no records leak between
// invocations.
//
// The registry is the dependency-injection seam: bind a service with [Context.Bind] and
// retrieve it with [Context.Get], [Context.MustGet] or the raw [Context.Value]. Bindings last
// the lifetime of the Context. Input parsing is opt-in — the runtime itself never parses flags.
//
// A Context is safe for concurrent registry access. Always pass it as a pointer; it must not
// be copied.
type Context struct {
	mu sync.RWMutex

	// Stdin, Stdout and Stderr are the program's streams, mirroring [Program.WithStdin] and
	// friends. A handler reads and writes through these rather than os.Std* directly, so the
	// same handler code can be driven by a test that configures the Program's streams. They
	// are set before dispatch, never mutated thereafter, and never nil.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Args is the raw argument vector for this invocation, with everything after the resolved
	// command path still present, so a handler can run its own parser instead of
	// [Parser.Parse]. It is the live slice, not a copy: a handler that mutates it changes what
	// every later read sees, including the Parser and Binder.
	Args []string

	services    map[string]any
	chain       []ResolvedCommand // resolved command path, root → leaf
	exitCode    int               // first non-zero wins; the funnel overrides
	stopped     bool              // an exit was requested: forward progress halts
	exitNow     bool              // hard Exit: skip remaining teardown too
	funnelStage bool              // the funnel is executing: Exit overrides, SignalExit is a no-op
	infos       []string          // the five outcome channels, all private: they surface
	recorded    []error           // only as the slices handed to the funnel. faults are
	warnings    []error           // the lifecycle's to capture, never a handler's to record.
	successes   []string
	faults      []*PanicError
}

// cloneServices snapshots the registry's bindings. Every run's Context is seeded from the
// Program's registry through it, so a service bound before the run is visible to every run
// while one bound during a run stays local to it.
func (rtx *Context) cloneServices() map[string]any {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return maps.Clone(rtx.services)
}

// newContext returns an empty [Context] with an initialized registry and no resolved command.
// [NewContextFor] is the public entry.
func newContext() *Context {
	return &Context{
		services: make(map[string]any),
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
}

// NewContextFor builds a [Context] with argv resolved against an explicit def — the same
// context the runtime hands a handler at dispatch. Use it to exercise the [Parser] or [Usage]
// helpers, or a single hook, against a Definition you construct:
//
//	def := rotini.Definition{Name: "app", Handler: "App", Commands: []rotini.CommandDef{ … }}
//	rtx := rotini.NewContextFor(def, []string{"build", "x.yaml"}).Bind(rotini.KeyParser, rotini.NewParser())
//	var in appInputs
//	err := rtx.MustGet[*rotini.Parser](rotini.KeyParser).Parse(rtx, &in)
//
// To drive a whole generated program end to end — the usual handler test — construct it with
// the generated NewProgram and run it under a recording exit and captured streams instead; the
// generated command tree is unexported.
//
// A remote token resolves to as much of the chain as precedes it. NewContextFor does not exec
// the sibling binary the runtime would.
func NewContextFor(def Definition, argv []string) *Context {
	rtx := newContext()
	chain, _ := resolveChain(def, argv)
	rtx.Args = argv
	rtx.chain = chain
	return rtx
}

// Bind associates value with key, overwriting any prior binding, and returns the receiver so
// calls chain. It is safe for concurrent use.
func (rtx *Context) Bind(key string, value any) *Context {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.services == nil {
		rtx.services = make(map[string]any)
	}
	rtx.services[key] = value
	return rtx
}

// BindIfAbsent binds value under key only if key is not already bound, atomically. It is the
// registered-default form of [Context.Bind]: a handler registers the real implementation of a
// dependency, but a test that bound a double under the same key earlier keeps it. Either way
// the dependency is resolvable from the registry rather than hidden inline.
//
//	rtx.BindIfAbsent("generate", internal.Generate)
//	gen := rtx.MustGet[internal.GenerateFn]("generate")
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

// Chain returns the resolved command path for this invocation, root → leaf. The [Parser] and
// [Usage] read it to bind inputs and render help against the command whose handler ran. Treat
// the slice as read-only.
func (rtx *Context) Chain() []ResolvedCommand {
	if rtx == nil {
		return nil
	}
	return rtx.chain
}

// Command returns the command this invocation resolved to — the leaf of the chain, whose Run
// is executing, or the root for a bare root invocation:
//
//	rtx.RecordError(fmt.Errorf("%s: %w", rtx.Command().Name, err))
//
// [Context.Chain] has the ancestors and the argv token that matched each one.
func (rtx *Context) Command() ResolvedCommand {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.chain) == 0 {
		return ResolvedCommand{}
	}
	return rtx.chain[len(rtx.chain)-1]
}

// Path returns the invoked command path, space-joined — "tasks add" for a sub-command,
// "tasks" for a bare root invocation.
//
// The names are canonical, not the tokens the user typed, so an invocation through an alias
// reports the real command name and a path is stable to log and aggregate on. Each frame's
// Matched token in [Context.Chain] is what the user actually typed.
func (rtx *Context) Path() string {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	names := make([]string, 0, len(rtx.chain))
	for _, c := range rtx.chain {
		names = append(names, c.Name)
	}
	return strings.Join(names, " ")
}

// Value returns the service bound under key, or nil if none is bound — the raw accessor,
// mirroring [context.Context.Value]. It never panics. Prefer the typed [Context.Get] or
// [Context.MustGet].
func (rtx *Context) Value(key string) any {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.services[key]
}

// Halt stops the lifecycle's forward progress WITHOUT claiming an exit code, leaving the
// verdict to whatever else the run records and to the funnel. Teardown is unaffected: every
// PostRun and CascadingPostRun whose paired setup hook began still runs, in reverse.
//
// It is the honest spelling of the commonest stop there is — a handler that has recorded an
// error and has nothing further to do:
//
//	if err != nil {
//	    rtx.RecordError(err)
//	    rtx.Halt()          // the funnel decides what this costs
//	    return
//	}
//
// [Context.SignalExit] does two jobs at once — set the code AND stop — so a program that
// centralizes its exit policy in a funnel had to write a number it did not mean purely to
// stop, and explain in a comment that the number was a lie. Worse, the number then reads as
// redundant: deleting it looks like tidying and silently removes the halt, so the next hook
// collects the same inputs, hits the same validation and records the same error again. That
// is not hypothetical — it is how one bad flag came to be reported three times, with a
// fourth misleading error on top, while this example was being written.
//
// Halt claims nothing, so it cannot be mistaken for policy and cannot be deleted as
// redundant. Reach for [Context.SignalExit] when the code IS the point (a filter reporting
// "no match" as 1), and [Context.Exit] when pending teardown must not run.
//
// Like SignalExit it is a no-op inside the funnel, where the lifecycle has already run.
func (rtx *Context) Halt() {
	if rtx == nil || rtx.funnelStage {
		return
	}
	rtx.stopped = true
}

// SignalExit records the program's exit code and stops the lifecycle's forward progress.
// Teardown is unaffected: every PostRun and CascadingPostRun whose paired setup hook began
// still runs, in reverse. The first non-zero code wins, so a later SignalExit cannot change
// the verdict, and SignalExit records no error — it is a deliberate stop, not a failure. Use
// [Context.Exit] for an abort that skips pending teardown.
//
// It is a no-op inside the funnel, where the lifecycle has already run; [Context.Exit] is how
// the funnel sets the code.
func (rtx *Context) SignalExit(code int) {
	if rtx == nil || rtx.funnelStage {
		return
	}
	rtx.stopped = true
	if rtx.exitCode == 0 {
		rtx.exitCode = code
	}
}

// Exit records the program's exit code and stops the lifecycle immediately — every pending
// teardown hook is skipped. Use it where remaining cleanup must not run; prefer
// [Context.SignalExit] for an orderly stop. The first non-zero code wins, and Exit is a
// deliberate stop, not an error.
//
// It skips teardown, not fault reporting: a panic recovered before Exit still reaches the
// funnel, so a handler cannot silently swallow one — though the funnel, as the final
// authority, may.
//
// Inside the funnel, Exit overrides any code the lifecycle set; during the lifecycle it keeps
// first-non-zero-wins.
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

// RecordInfo records msg as an informational message of this run — neutral output such as
// progress or context, distinct from a success message only by intent. Like every record call
// it neither prints nor stops the lifecycle: the funnel receives the infos once the run
// settles. An empty msg is ignored.
func (rtx *Context) RecordInfo(msg string) {
	if rtx == nil || msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.infos = append(rtx.infos, msg)
}

// copyInfos snapshots the recorded infos, in recording order.
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

// RecordError records err as one of this run's errors — the end-user's own failures. It
// neither prints nor stops the lifecycle: a handler accumulates errors across any number of
// calls and hooks, then chooses how to stop, and the funnel receives them once the run
// settles either way. A nil err is ignored.
//
// Recovered panics and rotini-detected faults are not recorded here; the lifecycle captures
// them as the funnel's panics slice.
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

// copyErrors snapshots the recorded errors, in recording order.
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

// RecordWarning records warn as a non-fatal warning of this run — a deprecation, a fallback,
// a skipped item. It is an error value so it can be typed and branched on with errors.As and
// so secrets stay redacted, but it never raises the exit code. A nil warn is ignored.
func (rtx *Context) RecordWarning(warn error) {
	if rtx == nil || warn == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.warnings = append(rtx.warnings, warn)
}

// RecordSuccess records msg as a success message of this run, for the funnel to present. An
// empty msg is ignored.
//
// The funnel reports after the lifecycle settles, so recorded outcomes appear after anything a
// handler wrote directly to [Context.Stdout] during Run. See the Ordering section on [Printer].
func (rtx *Context) RecordSuccess(msg string) {
	if rtx == nil || msg == "" {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.successes = append(rtx.successes, msg)
}

// copyWarnings snapshots the recorded warnings, in recording order.
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

// copySuccesses snapshots the recorded successes, in recording order.
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

// copyFaults snapshots the captured faults, in capture order.
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

// Failed reports whether this invocation has recorded an error or suffered a fault SO FAR.
//
// It exists for teardown. A PostRun or CascadingPostRun that owns a resource has exactly one
// decision to make — commit or roll back, keep or discard, publish or delete — and it cannot
// make it without knowing whether the work it was bracketing succeeded:
//
//	func (*migrateHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
//	    if rtx.Failed() {
//	        tx.Rollback()
//	        return
//	    }
//	    tx.Commit()
//	}
//
// The [Outcome] a funnel receives answers the same question, but a funnel runs AFTER every
// teardown has finished — the right place to report a failure and much too late to undo one.
//
// It is deliberately one bit and not the errors themselves. A teardown that could read them
// would be tempted to print them, and the whole point of the funnel is that a run reports its
// outcome exactly once, in one place, after everything has settled. Faults count: a panic in
// the bracketed work is a failure, and a rollback is even more clearly right there.
//
// Read from a forward hook it is also meaningful — an earlier hook in the chain may already
// have recorded an error — but the answer only grows over a run, so a false is never a promise
// about what comes next.
func (rtx *Context) Failed() bool {
	if rtx == nil {
		return false
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return len(rtx.recorded) > 0 || len(rtx.faults) > 0
}

// recordFault appends a recovered panic or detected fault. Unexported on purpose: faults are
// the lifecycle's to capture, never a handler's to record.
func (rtx *Context) recordFault(pe *PanicError) {
	if rtx == nil || pe == nil {
		return
	}
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.faults = append(rtx.faults, pe)
}

// Get returns the service bound under key as T, reporting ok=false when nothing is bound there
// or the bound value is not a T. It never panics; use [Context.MustGet] to route a miss
// through the funnel instead of handling it inline, or a typed [Key], which supplies T for you.
//
//	parser, ok := rtx.Get[*rotini.Parser](rotini.KeyParser)
func (rtx *Context) Get[T any](key string) (T, bool) {
	v, ok := rtx.Value(key).(T)
	return v, ok
}

// MustGet returns the service bound under key as T, or panics with a [*ServiceError] when it
// is absent or not a T. The panic is intentional: the runtime recovers it inside dispatch and
// routes it through the funnel, so a handler that cannot run without a service reaches for
// MustGet rather than handling a miss inline.
//
//	parser := rtx.MustGet[*rotini.Parser](rotini.KeyParser)
func (rtx *Context) MustGet[T any](key string) T {
	v, ok := rtx.Get[T](key)
	if !ok {
		panic(&ServiceError{Key: key})
	}
	return v
}
