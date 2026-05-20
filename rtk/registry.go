package rtk

import "sync"

// Registry is the rotini service registry. It maps string keys to opaque
// service values (typically interfaces) and provides typed retrieval via the
// package-level [Get] function.
//
// A Registry is safe for concurrent use; reads and writes are guarded by an
// internal sync.RWMutex.
//
// Generated rotini programs construct a single Registry per invocation, auto-
// bind the well-known defaults (parser, io, os, fs, signals, …), and pass a
// [Ctx] holding a pointer to that Registry into every handler hook. Handler
// code retrieves services via [Get]:
//
//	rp := rtk.Get[rtk.Parser](rtx.Registry, "parser")
//	io := rtk.Get[rtk.IO](rtx.Registry, "io")
//
// Users override a default by binding a different implementation under the
// same key in main.go:
//
//	cmd.Program.
//		Bind("io", customIO).
//		Execute()
//
// Bindings persist for the lifetime of the Registry; the auto-bind step only
// runs for keys the user did not already supply.
type Registry struct {
	mu       sync.RWMutex
	services map[string]any
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{services: make(map[string]any)}
}

// Bind associates value with key, overwriting any prior binding. It returns
// the receiver so calls can be chained:
//
//	cmd.Program.Bind("io", customIO).Bind("os", customOS).Execute()
//
// Bind is safe for concurrent use.
func (r *Registry) Bind(key string, value any) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services == nil {
		r.services = make(map[string]any)
	}
	r.services[key] = value
	return r
}

// Has reports whether a binding exists under key.
func (r *Registry) Has(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.services[key]
	return ok
}

// Get retrieves the binding stored under key, type-asserts it to T, and
// returns the asserted value. It returns the zero value of T if either the
// key is unbound or the bound value does not satisfy T.
//
// Get is the standard accessor for handler code. It is a package-level
// function — not a method — because Go method generics cannot introduce free
// type parameters.
//
//	rp := rtk.Get[rtk.Parser](rtx.Registry, "parser")
//	io := rtk.Get[rtk.IO](rtx.Registry, "io")
//
// Callers that want to distinguish "unbound" from "wrong type" should call
// [Registry.Has] first, then Get.
func Get[T any](r *Registry, key string) T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.services[key]; ok {
		if typed, ok := v.(T); ok {
			return typed
		}
	}
	var zero T
	return zero
}

// Ctx is the per-command context passed into every handler hook
// (CascadingPreRun, PreRun, Run, PostRun, CascadingPostRun). It carries the
// service Registry; handler code retrieves services via [Get].
//
// Generated code emits per-command aliases — `type RotiniGenerateCtx = Ctx` —
// so handler signatures read self-documentingly across commands while sharing
// a single underlying type.
type Context struct {
	// Registry is the service registry bound for this invocation. Handler
	// code uses it to retrieve services:
	//
	//	rp := rtk.Get[rtk.Parser](rtx.Registry, "parser")
	Registry *Registry
}

// BuildInfo carries linker-flags-injected build metadata. Generated rotini
// programs bind a BuildInfo value into the registry under the key
// "build-info". The fields are typically populated via `-ldflags
// "-X path.Version=v1.2.3 -X path.Commit=abc123 -X path.Date=2026-05-18"`
// at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}
