package rtk

import (
	"context"
	"os"
	"os/signal"
	"sort"
	"sync"
)

// SignalHandler is the callback run when its registered OS signal is received.
// It takes no argument: a handler is registered for exactly one signal (the one
// passed to [Signals.Add]), so the signal is already known at the call site.
type SignalHandler func()

// Signals is an opt-in rtk service for reacting to OS signals with callbacks —
// rotini's answer to "do X when SIGHUP arrives" without putting behavior in the
// spec. It is a one-handler-per-signal registry: register the handler for each
// signal you want to act on, then arm it. There is no "catch any signal" mode —
// every signal you handle is named explicitly.
//
// Listening does not begin until [Signals.Start]; a background goroutine then
// delivers each received signal to its handler. Like the other rtk services it
// is bound once and retrieved by handlers, and it is driven from the lifecycle
// hooks — register + [Signals.Start] in a CascadingPreRun/PreRun, [Signals.Stop]
// in a CascadingPostRun/PostRun:
//
//	// main.go
//	rth.Program.Bind("signals", rtk.NewSignals()).Execute()
//
//	// a handler
//	sig := rotini.MustGet[*rtk.Signals](rtx, "signals")
//	sig.Add(syscall.SIGHUP, func() { reload() })
//	sig.Start(ctx)
//
// Every method is safe to call in any order and any number of times: Start and
// Stop are idempotent, Stop before Start is a no-op, and the control methods
// (Add/Pause/Remove and their All variants) are safe before or after Start/Stop.
type Signals struct {
	mu       sync.Mutex
	handlers map[os.Signal]SignalHandler
	ch       chan os.Signal
	stop     chan struct{}
	running  bool
}

// NewSignals returns a new, unstarted signals client, ready to bind under the
// "signals" registry key.
func NewSignals() *Signals {
	return &Signals{
		handlers: make(map[os.Signal]SignalHandler),
		ch:       make(chan os.Signal, 4),
	}
}

// Add registers fn as the handler for sig, replacing any handler previously
// registered for that signal — each signal has at most one handler. It may be
// called before or after [Signals.Start]; registering after Start begins
// delivery for that signal immediately. A nil fn is ignored. Chainable.
func (s *Signals) Add(sig os.Signal, fn SignalHandler) *Signals {
	if fn == nil {
		return s
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[sig] = fn
	if s.running {
		signal.Notify(s.ch, sig)
	}
	return s
}

// Start begins listening for the registered signals and dispatching them to
// their handlers from a background goroutine. Listening does not begin until
// Start is called. It is idempotent (a second call while running is a no-op) and
// stops when [Signals.Stop] is called or ctx is cancelled; a nil ctx is treated
// as context.Background(). Call it from a CascadingPreRun or PreRun hook.
func (s *Signals) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	stop := s.stop
	if len(s.handlers) > 0 {
		sigs := make([]os.Signal, 0, len(s.handlers))
		for sig := range s.handlers {
			sigs = append(sigs, sig)
		}
		signal.Notify(s.ch, sigs...)
	}
	s.mu.Unlock()

	go func() {
		for {
			select {
			case sig := <-s.ch:
				s.dispatch(sig)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// dispatch runs the handler registered for sig, if any. The handler runs outside
// the lock so it may itself call Add/Remove/etc. without deadlocking.
func (s *Signals) dispatch(sig os.Signal) {
	s.mu.Lock()
	h := s.handlers[sig]
	s.mu.Unlock()
	if h != nil {
		h()
	}
}

// Stop stops listening and dispatch, and releases the OS notification
// (signal.Stop). It is idempotent — calling it before Start, or more than once,
// is a no-op. Call it from a CascadingPostRun or PostRun hook.
func (s *Signals) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	signal.Stop(s.ch)
	close(s.stop)
}

// Pause makes the program ignore the given signals that currently have a handler
// registered — the OS discards them, so the handler stops firing — while keeping
// the registration, so re-registering with [Signals.Add] re-enables delivery.
// Signals without a registered handler are skipped. Wraps signal.Ignore.
// Chainable.
func (s *Signals) Pause(sigs ...os.Signal) *Signals {
	s.mu.Lock()
	defer s.mu.Unlock()
	paused := make([]os.Signal, 0, len(sigs))
	for _, sig := range sigs {
		if _, ok := s.handlers[sig]; ok {
			paused = append(paused, sig)
		}
	}
	if len(paused) > 0 {
		signal.Ignore(paused...)
	}
	return s
}

// PauseAll pauses every signal that currently has a handler (see [Signals.Pause]).
func (s *Signals) PauseAll() *Signals {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.handlers) == 0 {
		return s
	}
	sigs := make([]os.Signal, 0, len(s.handlers))
	for sig := range s.handlers {
		sigs = append(sigs, sig)
	}
	signal.Ignore(sigs...)
	return s
}

// Remove resets the given signals to their default OS behavior and drops their
// handlers. With no arguments it is a no-op (use [Signals.RemoveAll]). Wraps
// signal.Reset. Chainable.
func (s *Signals) Remove(sigs ...os.Signal) *Signals {
	if len(sigs) == 0 {
		return s
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	signal.Reset(sigs...)
	for _, sig := range sigs {
		delete(s.handlers, sig)
	}
	return s
}

// RemoveAll resets every signal to its default OS behavior and drops all
// handlers. Chainable.
func (s *Signals) RemoveAll() *Signals {
	s.mu.Lock()
	defer s.mu.Unlock()
	signal.Reset()
	s.handlers = make(map[os.Signal]SignalHandler)
	return s
}

// Has reports whether a handler is currently registered for sig.
func (s *Signals) Has(sig os.Signal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.handlers[sig]
	return ok
}

// Signals returns the signals that currently have a registered handler, sorted
// by name.
func (s *Signals) Signals() []os.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	sigs := make([]os.Signal, 0, len(s.handlers))
	for sig := range s.handlers {
		sigs = append(sigs, sig)
	}
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].String() < sigs[j].String() })
	return sigs
}
