package rotini

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// ErrServiceNotFound is the sentinel reported by [Get]/[MustGet] when a registry
// key is unbound. A wrong-type binding reports [ErrServiceWrongType]. Both are
// carried by a [*ServiceError] (use errors.As to recover the key/types):
//
//	if _, err := rotini.Get[*myService](rtx, "svc"); errors.Is(err, rotini.ErrServiceNotFound) {
//		// bind a default, or fail the command
//	}
var (
	ErrServiceNotFound  = errors.New("rotini: service not found")
	ErrServiceWrongType = errors.New("rotini: service has wrong type")
)

// ServiceError describes a failed [Get]/[MustGet]: the key requested, the type
// the caller asked for, and the type actually bound (nil when nothing was). It
// unwraps to [ErrServiceNotFound] or [ErrServiceWrongType], so handlers can
// branch with errors.Is and recover the details with errors.As.
type ServiceError struct {
	Key  string       // the registry key that was requested
	Want reflect.Type // the type the caller asked for
	Got  reflect.Type // the type actually bound, or nil if the key was unbound
}

func (e *ServiceError) Error() string {
	if e.Got == nil {
		return fmt.Sprintf("rotini: no service bound under key %q", e.Key)
	}
	return fmt.Sprintf("rotini: service %q is %s, not %s", e.Key, e.Got, e.Want)
}

func (e *ServiceError) Unwrap() error {
	if e.Got == nil {
		return ErrServiceNotFound
	}
	return ErrServiceWrongType
}

// Rtx is rotini's per-invocation context: the service registry plus the bits the
// runtime resolves before dispatch — the raw argument vector (read via [Rtx.Args])
// and the resolved command chain (read via [Rtx.Chain]). A single Rtx is built per
// invocation and passed (as [Context], a pointer) into every handler hook, so all
// hooks share the same bindings and exit state.
//
// The registry is the dependency-injection seam: bind any service with [Rtx.Bind]
// (a real implementation in production, a double in tests) and retrieve it with
// the package-level [Get]/[MustGet]. Bindings persist for the lifetime of the Rtx.
// Opt-in input parsing (the rtk package's Parse) reads [Rtx.Args]/[Rtx.Chain]; the
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
//	in, err := rtk.Parse[rtg.RotiniGenerateInputs](rtx)
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

// Get retrieves the binding under key and type-asserts it to T. On success it
// returns the value and a nil error; otherwise it returns the zero value of T
// and a [*ServiceError] that unwraps to [ErrServiceNotFound] (key unbound) or
// [ErrServiceWrongType] (bound value does not satisfy T). The handler owns the
// failure — inspect it with errors.Is/errors.As and decide inline:
//
//	io, err := rotini.Get[IO](rtx, "io")
//	if err != nil {
//		io = defaultIO // recover locally
//	}
//
// Get is a package-level function, not a method, because Go forbids type
// parameters on methods. Use [MustGet] to route failures to the OnError funnel
// instead of handling them here.
func Get[T any](r *Rtx, key string) (T, error) {
	var zero T
	want := reflect.TypeFor[T]()
	if r == nil {
		return zero, &ServiceError{Key: key, Want: want}
	}
	r.mu.RLock()
	v, ok := r.services[key]
	r.mu.RUnlock()
	if !ok {
		return zero, &ServiceError{Key: key, Want: want}
	}
	typed, ok := v.(T)
	if !ok {
		return zero, &ServiceError{Key: key, Want: want, Got: reflect.TypeOf(v)}
	}
	return typed, nil
}

// MustGet retrieves the binding under key as T or panics with the [*ServiceError]
// from [Get]. The panic is not a dead end: the runtime recovers it inside dispatch
// and routes it through the program's OnError funnel (see [program.OnError]),
// which classifies/logs/prints it and returns the process exit code. Use MustGet
// for services a handler cannot run without, and centralize the failure handling
// in one OnError funnel rather than at every call site.
func MustGet[T any](r *Rtx, key string) T {
	v, err := Get[T](r, key)
	if err != nil {
		panic(err)
	}
	return v
}

// Context is the per-command context passed into every handler hook
// (CascadingPreRun, PreRun, Run, PostRun, CascadingPostRun). It is a pointer to
// the per-invocation [Rtx], so every hook shares the same registry, argv, and
// exit state. Handlers retrieve services via [Get]/[MustGet] and the raw argv via
// [Rtx.Args]; typed inputs are an opt-in via the rtk package's Parse.
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
