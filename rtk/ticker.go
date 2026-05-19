package rtk

import (
	"sync"
	"time"
)

// TickHandlerFunc is the function signature for a tick handler. The
// supplied [Ctx] gives access to the registry. The handler runs in a
// goroutine spawned by the lifecycle layer; long-running work should
// honor context cancellation surfaced via Ctx.
type TickHandlerFunc func(ctx Ctx) error

// TickHandler binds an interval to a handler function. Codegen emits one
// of these per `OnProgramTicks` / `On<Cmd>Ticks` hook the user wrote.
//
// IDs are optional. A non-empty ID enables in-place replacement on
// subsequent Register calls (re-registering with the same ID overwrites
// the prior entry) and is the key for [Ticker.Pause] / [Ticker.Resume].
type TickHandler struct {
	// ID is the optional unique identifier for this handler. Two handlers
	// may share an empty ID; pause/resume by ID won't work in that case.
	ID string

	// Interval is the period between ticks. A non-positive Interval
	// disables the handler (the lifecycle layer skips it).
	Interval time.Duration

	// Jitter, when positive, is the maximum random offset added to each
	// tick interval. Lifecycle uses it to spread out the tick wakeups so
	// many handlers don't fire in lockstep.
	Jitter time.Duration

	// Paused, when true, suspends the handler. The lifecycle layer keeps
	// the goroutine alive but skips invocation until Paused returns false.
	Paused bool

	// Handler is the function to call on each tick.
	Handler TickHandlerFunc
}

// Ticker is the rtk-default tick-handler registry. Generated code binds
// a [Ticker] under the "ticker" registry key; the lifecycle layer reads
// [Ticker.Snapshot] to start the actual tick-driving goroutines.
//
// Pause and Resume mutate the registered handler's Paused flag in place;
// lifecycle observes the flag at each tick and skips invocation while it
// is true. (The goroutine is not torn down — pause is intended for short
// suspends, not long-term disabling. Use Unregister for permanent removal.)
type Ticker interface {
	// Register adds a handler. If a handler with the same non-empty ID
	// already exists, it is replaced in-place; otherwise the handler is
	// appended.
	Register(handler TickHandler)

	// Unregister removes every handler with the given ID. No-op when id
	// is empty or no match exists.
	Unregister(id string)

	// Pause flips the Paused flag on the handler with the given ID.
	// Returns true if a matching handler was found. No-op for empty ID.
	Pause(id string) bool

	// Resume clears the Paused flag on the handler with the given ID.
	// Returns true if a matching handler was found. No-op for empty ID.
	Resume(id string) bool

	// Snapshot returns a copy of the current handler list. The slice is
	// safe to retain.
	Snapshot() []TickHandler
}

// NewTicker returns the rtk-default [Ticker] implementation: an in-
// memory, lock-guarded list of registrations. The default implementation
// does not itself drive any timers — that is the lifecycle layer's job.
func NewTicker() Ticker { return &defaultTicker{} }

// defaultTicker is the lock-guarded slice implementation of [Ticker].
type defaultTicker struct {
	mu       sync.Mutex
	handlers []TickHandler
}

// Register adds a handler. ID-replacement is in-place; new handlers are
// appended. Safe for concurrent calls.
func (r *defaultTicker) Register(h TickHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.ID != "" {
		for i := range r.handlers {
			if r.handlers[i].ID == h.ID {
				r.handlers[i] = h
				return
			}
		}
	}
	r.handlers = append(r.handlers, h)
}

// Unregister removes every handler with the given ID. Safe for
// concurrent calls.
func (r *defaultTicker) Unregister(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.handlers[:0]
	for _, h := range r.handlers {
		if h.ID != id {
			out = append(out, h)
		}
	}
	for i := len(out); i < len(r.handlers); i++ {
		r.handlers[i] = TickHandler{}
	}
	r.handlers = out
}

// Pause sets Paused=true on the matching handler. Returns true on hit.
func (r *defaultTicker) Pause(id string) bool {
	return r.setPaused(id, true)
}

// Resume sets Paused=false on the matching handler. Returns true on hit.
func (r *defaultTicker) Resume(id string) bool {
	return r.setPaused(id, false)
}

func (r *defaultTicker) setPaused(id string, paused bool) bool {
	if id == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.handlers {
		if r.handlers[i].ID == id {
			r.handlers[i].Paused = paused
			return true
		}
	}
	return false
}

// Snapshot returns a defensive copy of the handler list. Returns nil
// when no handlers are registered.
func (r *defaultTicker) Snapshot() []TickHandler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handlers) == 0 {
		return nil
	}
	cp := make([]TickHandler, len(r.handlers))
	copy(cp, r.handlers)
	return cp
}
