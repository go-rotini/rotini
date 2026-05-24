package rotini

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrServiceNotFound is the sentinel [Rtx.MustGet] panics with (wrapped in a
// [*ServiceError]) when a registry key is unbound. A handler's OnError funnel
// classifies it with errors.Is:
//
//	case errors.Is(err, rotini.ErrServiceNotFound):
var ErrServiceNotFound = errors.New("rotini: service not found")

// ServiceError is the error [Rtx.MustGet] panics with when key is unbound. It
// unwraps to [ErrServiceNotFound]; recover the key with errors.As.
type ServiceError struct {
	Key string // the registry key that was requested
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("rotini: no service bound under key %q", e.Key)
}

func (e *ServiceError) Unwrap() error { return ErrServiceNotFound }

// Rtx is rotini's per-invocation context: the service registry plus the bits the
// runtime resolves before dispatch — the raw argument vector (read via [Rtx.Args])
// and the resolved command chain (read via [Rtx.Chain]). A single Rtx is built per
// invocation and passed (as [Context], a pointer) into every handler hook, so all
// hooks share the same bindings and exit state.
//
// The registry is the dependency-injection seam: bind any service with [Rtx.Bind]
// (a real implementation in production, a double in tests) and retrieve it with
// [Rtx.Get]/[Rtx.MustGet]. Bindings persist for the lifetime of the Rtx. Opt-in
// input parsing (the rtk package's Parser) reads [Rtx.Args]/[Rtx.Chain]; the
// runtime itself never parses flags.
//
// An Rtx is safe for concurrent registry access; reads and writes are guarded by
// an internal sync.RWMutex.
type Rtx struct {
	mu       sync.RWMutex
	services map[string]any
	args     []string                                          // raw argument vector for this invocation
	chain    []ResolvedCommand                                 // resolved command path, root → leaf
	onError  func(ctx context.Context, rtx Context, err error) // funnel for MustGet/panic failures (see Program.OnError)
	exitCode int                                               // process exit code requested via [Rtx.Exit]
	stopped  bool                                              // [Rtx.Exit] was called; remaining leaf hooks are skipped
}

// NewRtx returns an empty Rtx with an initialized registry.
func NewRtx() *Rtx {
	return &Rtx{services: make(map[string]any)}
}

// NewContext builds a [Context] with argv resolved against def — the same context
// the runtime hands a handler at dispatch (raw [Rtx.Args] + the resolved
// [Rtx.Chain]). It is the entry point for exercising a handler, or the rtk Parse
// and Usage helpers, in isolation:
//
//	rtx := rotini.NewContext(rtg.Definition, []string{"generate", "x.yaml"})
//	parser := rtx.MustGet("parser").(*rtk.Parser)
//	var in rtg.RotiniGenerateInputs
//	err := parser.Parse(rtx, &in)
//
// A remote/co-located token resolves to as much of the chain as precedes it; the
// runtime would exec the sibling binary, which NewContext does not.
func NewContext(def Definition, argv []string) Context {
	chain, _ := resolveChain(def, argv)
	rtx := NewRtx()
	rtx.args = argv
	rtx.chain = chain
	return rtx
}

// Bind associates value with key, overwriting any prior binding. It returns the
// receiver so calls can be chained:
//
//	cmd.Program.Bind("io", customIO).Bind("os", customOS).Execute()
//
// Bind is safe for concurrent use.
func (r *Rtx) Bind(key string, value any) *Rtx {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services == nil {
		r.services = make(map[string]any)
	}
	r.services[key] = value
	return r
}

// Has reports whether a binding exists under key.
func (r *Rtx) Has(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.services[key]
	return ok
}

// Args returns the raw argument vector for this invocation: everything after the
// resolved command path is still present, so a handler can run its own parser
// instead of the rtk package's Parse. The slice is the runtime's; treat it as
// read-only.
func (r *Rtx) Args() []string {
	if r == nil {
		return nil
	}
	return r.args
}

// Chain returns the resolved command path for this invocation, root → leaf — the
// command tree the runtime descended to choose this handler. Opt-in tooling (the
// rtk package's Parse and Usage) reads it to bind inputs and render help against
// the exact command whose handler ran. The slice is the runtime's; treat it as
// read-only.
func (r *Rtx) Chain() []ResolvedCommand {
	if r == nil {
		return nil
	}
	return r.chain
}

// Get returns the service bound under key, or nil if none is bound — mirroring
// [context.Context.Value]. Callers type-assert to the expected type, using the
// comma-ok form to handle an unbound (or wrong-type) service:
//
//	parser, ok := rtx.Get("parser").(*rtk.Parser)
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// Get reports a miss as nil and never panics; use [Rtx.MustGet] to route a
// missing service through the OnError funnel instead.
func (r *Rtx) Get(key string) any {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.services[key]
}

// Context is the per-command context passed into every handler hook
// (CascadingPreRun, PreRun, Run, PostRun, CascadingPostRun). It is a pointer to
// the per-invocation [Rtx], so every hook shares the same registry, argv, and
// exit state. Handlers retrieve services via [Rtx.Get]/[Rtx.MustGet] and the raw
// argv via [Rtx.Args]; typed inputs are an opt-in via the rtk package's Parser.
type Context = *Rtx

// Exit records a non-zero exit code for the program and stops the current
// command's remaining leaf hooks (PreRun/Run/PostRun); CascadingPostRun still
// runs so cleanup is not skipped. The process exits with code once the lifecycle
// completes. It is the handler-facing way to fail a command until lifecycle
// hooks themselves return errors.
func (rtx *Rtx) Exit(code int) {
	if rtx == nil {
		return
	}
	rtx.exitCode = code
	rtx.stopped = true
}
