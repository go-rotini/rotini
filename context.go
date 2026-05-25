package rotini

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrServiceNotFound is the sentinel reported when a registry key is unbound — the
// rtk package's MustGet panics a [*ServiceError] wrapping it, which the runtime
// recovers and routes to OnError. A funnel classifies it with errors.Is:
//
//	case errors.Is(err, rotini.ErrServiceNotFound):
var ErrServiceNotFound = errors.New("rotini: service not found")

// ServiceError reports a registry key that was requested but unbound (or bound to
// the wrong type) — the rtk package's MustGet panics it. It unwraps to
// [ErrServiceNotFound]; recover the key with errors.As.
type ServiceError struct {
	Key string // the registry key that was requested
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("rotini: no service bound under key %q", e.Key)
}

func (e *ServiceError) Unwrap() error { return ErrServiceNotFound }

// Context is rotini's per-invocation context: the service registry plus the bits
// the runtime resolves before dispatch — the raw argument vector (read via
// [Context.Args]) and the resolved command chain (read via [Context.Chain]). One
// is built per invocation and passed (as *Context) into every handler hook, so all
// hooks share the same bindings and exit state.
//
// The registry is the dependency-injection seam: bind any service with
// [Context.Bind] (a real implementation in production, a double in tests) and
// retrieve it with [Context.Value] (or the rtk package's typed Get/MustGet).
// Bindings persist for the lifetime of the Context. Opt-in input parsing (the rtk
// package's Parser) reads [Context.Args]/[Context.Chain]; the runtime itself never
// parses flags.
//
// A Context is safe for concurrent registry access; reads and writes are guarded
// by an internal sync.RWMutex. Always pass it as a pointer — it must not be copied.
type Context struct {
	mu       sync.RWMutex
	services map[string]any
	args     []string                                           // raw argument vector for this invocation
	chain    []ResolvedCommand                                  // resolved command path, root → leaf
	onError  func(ctx context.Context, rtx *Context, err error) // funnel for MustGet/panic failures (see Program.OnError)
	exitCode int                                                // process exit code requested via [Context.Exit]
	stopped  bool                                               // [Context.Exit] was called; remaining leaf hooks are skipped
}

// NewContext returns an empty [Context] with an initialized registry and no
// resolved command. The runtime builds one per invocation and fills in the
// resolved chain before dispatch; tests and standalone tooling can use it
// directly, or [NewContextFor] to also resolve a command.
func NewContext() *Context {
	return &Context{services: make(map[string]any)}
}

// NewContextFor builds a [Context] with argv resolved against def — the same
// context the runtime hands a handler at dispatch (raw [Context.Args] + the
// resolved [Context.Chain]). It is the entry point for exercising a handler, or
// the rtk Parser/Usage helpers, in isolation:
//
//	rtx := rotini.NewContextFor(rtg.Definition, []string{"generate", "x.yaml"})
//	parser := rtk.MustGet[*rtk.Parser](rtx, "parser")
//	var in rtg.RotiniGenerateInputs
//	err := parser.Parse(rtx, &in)
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
//	cmd.Program.Bind("io", customIO).Bind("os", customOS).Execute()
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
// instead of the rtk package's Parse. The slice is the runtime's; treat it as
// read-only.
func (rtx *Context) Args() []string {
	if rtx == nil {
		return nil
	}
	return rtx.args
}

// Chain returns the resolved command path for this invocation, root → leaf — the
// command tree the runtime descended to choose this handler. Opt-in tooling (the
// rtk package's Parse and Usage) reads it to bind inputs and render help against
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
//	parser, ok := rtx.Value("parser").(*rtk.Parser)
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// Value reports a miss as nil and never panics; prefer the rtk package's typed Get
// (comma-ok) or MustGet (panics → OnError funnel) for type-safe retrieval.
func (rtx *Context) Value(key string) any {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.services[key]
}

// Exit records a non-zero exit code for the program and stops the current
// command's remaining leaf hooks (PreRun/Run/PostRun); CascadingPostRun still
// runs so cleanup is not skipped. The process exits with code once the lifecycle
// completes. It is the handler-facing way to fail a command until lifecycle
// hooks themselves return errors.
func (rtx *Context) Exit(code int) {
	if rtx == nil {
		return
	}
	rtx.exitCode = code
	rtx.stopped = true
}
