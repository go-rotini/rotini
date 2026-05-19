package rtk

import (
	"os"
	"sync"
	"syscall"
)

// ShutdownSignals is the conventional set of signals indicating a graceful
// shutdown request. Codegen-emitted dispatch code uses this as the default
// signal set for the program-level shutdown hook.
var ShutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// SignalHandlerFunc is the function signature for a signal handler. The
// supplied [Ctx] gives access to the registry; the [os.Signal] identifies
// which signal fired (useful when one function services multiple signals).
type SignalHandlerFunc func(ctx Ctx, sig os.Signal) error

// SignalHandler binds a signal to a handler function. Codegen emits one
// of these per `OnProgramSignals` / `On<Cmd>Signals` hook the user wrote.
type SignalHandler struct {
	// Signal identifies which OS signal triggers Handler.
	Signal os.Signal

	// Handler is the function to call when Signal fires. It runs in a
	// goroutine spawned by the lifecycle layer; long-running work should
	// honor context cancellation surfaced via Ctx.
	Handler SignalHandlerFunc
}

// Signals is the rtk-default signal-handler registry. Generated code
// binds an [Signals] under the "signals" registry key; the lifecycle
// layer reads [Signals.Snapshot] to start the actual signal-watching
// goroutines.
//
// Test fakes bind a different implementation to capture or simulate
// Register/Unregister calls without involving the OS signal layer.
type Signals interface {
	// Register adds a handler binding. Multiple handlers may share the
	// same signal; all of them fire (in registration order) when the
	// signal arrives.
	Register(handler SignalHandler)

	// Unregister removes every handler bound to sig.
	Unregister(sig os.Signal)

	// Snapshot returns a copy of the current handler list. The slice is
	// safe to retain; subsequent Register/Unregister calls do not mutate
	// the returned slice.
	Snapshot() []SignalHandler
}

// NewSignals returns the rtk-default [Signals] implementation: an in-
// memory, lock-guarded list of registrations. The default implementation
// does not itself watch OS signals — that is the lifecycle layer's job.
func NewSignals() Signals { return &defaultSignals{} }

// defaultSignals is the lock-guarded slice implementation of [Signals].
type defaultSignals struct {
	mu       sync.Mutex
	handlers []SignalHandler
}

// Register adds a handler. Safe for concurrent calls.
func (r *defaultSignals) Register(h SignalHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
}

// Unregister removes every handler bound to sig. Safe for concurrent calls.
func (r *defaultSignals) Unregister(sig os.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.handlers[:0]
	for _, h := range r.handlers {
		if h.Signal != sig {
			out = append(out, h)
		}
	}
	// Zero the trailing slot(s) of the original backing array to release
	// references to the dropped SignalHandler.Handler closures.
	for i := len(out); i < len(r.handlers); i++ {
		r.handlers[i] = SignalHandler{}
	}
	r.handlers = out
}

// Snapshot returns a defensive copy of the handler list. Returns nil when
// no handlers are registered (saves an allocation on the common empty
// case).
func (r *defaultSignals) Snapshot() []SignalHandler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handlers) == 0 {
		return nil
	}
	cp := make([]SignalHandler, len(r.handlers))
	copy(cp, r.handlers)
	return cp
}
