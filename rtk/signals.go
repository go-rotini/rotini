package rtk

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// SignalHandler is a callback run when a registered OS signal is received. The
// signal that fired is passed in, so one handler can serve several signals.
type SignalHandler func(os.Signal)

// Signals is an opt-in rtk service for reacting to OS signals with callbacks —
// rotini's answer to "do X when SIGHUP arrives" without putting behavior in the
// spec. It is a registry: register any number of handlers for any signals you
// care about, then arm it; a background goroutine delivers received signals to
// their handlers in registration order.
//
// Like the other rtk services it is bound once and retrieved by handlers, and it
// is driven from the lifecycle hooks — register + [Signals.Start] in a
// CascadingPreRun/PreRun, [Signals.Stop] in a CascadingPostRun/PostRun:
//
//	// main.go
//	rth.Program.Bind("signals", rtk.NewSignals()).Execute()
//
//	// a handler
//	sig := rotini.MustGet[*rtk.Signals](rtx, "signals")
//	sig.On(syscall.SIGHUP, func(os.Signal) { reload() })
//	sig.Start(ctx)
//
// Start and Stop are idempotent, so it is safe for a cascading (program-wide)
// hook and a leaf hook to both arm/tear-down the same service.
type Signals struct {
	mu       sync.Mutex
	handlers map[os.Signal][]SignalHandler
	ch       chan os.Signal
	stop     chan struct{}
	running  bool
}

// NewSignals returns an unstarted [Signals] service, ready to bind under the
// "signals" registry key.
func NewSignals() *Signals {
	return &Signals{
		handlers: make(map[os.Signal][]SignalHandler),
		ch:       make(chan os.Signal, 4),
	}
}

// On registers fn to run when sig is received. Multiple handlers for the same
// signal run in registration order. It is safe to call before or after
// [Signals.Start] — the OS notification is wired immediately, so a signal that
// arrives between On and Start is buffered, not lost. A nil fn is ignored.
// On returns the receiver so registrations chain.
func (s *Signals) On(sig os.Signal, fn SignalHandler) *Signals {
	if fn == nil {
		return s
	}
	s.mu.Lock()
	s.handlers[sig] = append(s.handlers[sig], fn)
	s.mu.Unlock()
	signal.Notify(s.ch, sig)
	return s
}

// OnAny registers fn for each signal in sigs (see [Signals.On]).
func (s *Signals) OnAny(fn SignalHandler, sigs ...os.Signal) *Signals {
	for _, sig := range sigs {
		s.On(sig, fn)
	}
	return s
}

// Start begins delivering received signals to their handlers from a background
// goroutine. It is idempotent (a second call while running is a no-op) and stops
// when [Signals.Stop] is called or ctx is cancelled. Call it from a
// CascadingPreRun or PreRun hook.
func (s *Signals) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	stop := s.stop
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

// dispatch runs, in registration order, the handlers registered for sig. It
// copies the slice under the lock so a handler may register more handlers
// without deadlocking.
func (s *Signals) dispatch(sig os.Signal) {
	s.mu.Lock()
	hs := append([]SignalHandler(nil), s.handlers[sig]...)
	s.mu.Unlock()
	for _, h := range hs {
		h(sig)
	}
}

// Stop stops delivery and releases the OS notification (signal.Stop). It is
// idempotent. Call it from a CascadingPostRun or PostRun hook.
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

// Ignore makes the program ignore the given signals — they are caught and
// discarded, suppressing their default OS action (e.g. ignore SIGHUP so a
// terminal hang-up does not kill the program). It overrides any handlers
// registered with [Signals.On] for those signals (the OS no longer delivers
// them). Wraps signal.Ignore; chainable.
func (s *Signals) Ignore(sigs ...os.Signal) *Signals {
	signal.Ignore(sigs...)
	return s
}

// Reset undoes the effect of [Signals.On] and [Signals.Ignore] for the given
// signals, restoring their default OS behavior, and drops their registered
// handlers. With no arguments it resets every signal and clears all handlers.
// Wraps signal.Reset; chainable.
func (s *Signals) Reset(sigs ...os.Signal) *Signals {
	signal.Reset(sigs...)
	s.mu.Lock()
	if len(sigs) == 0 {
		s.handlers = make(map[os.Signal][]SignalHandler)
	} else {
		for _, sig := range sigs {
			delete(s.handlers, sig)
		}
	}
	s.mu.Unlock()
	return s
}

// GracefulContext returns a context derived from parent that is cancelled the
// first time one of sigs is received — the common "stop work on Ctrl-C, then
// drain and exit" pattern. A second signal force-exits the process with code
// 128+signum, so a hung shutdown is still killable. Delivery begins when
// [Signals.Start] runs; this is a convenience built on the same handler registry
// (the plain [Signals.On] registry stays unopinionated — it never exits for you).
func (s *Signals) GracefulContext(parent context.Context, sigs ...os.Signal) context.Context {
	ctx, cancel := context.WithCancel(parent)
	go func() { <-ctx.Done(); cancel() }() // release resources once cancelled (also satisfies vet)

	var once sync.Once
	s.OnAny(func(sig os.Signal) {
		graceful := false
		once.Do(func() { graceful = true; cancel() })
		if graceful {
			return
		}
		code := 130
		if sg, ok := sig.(syscall.Signal); ok {
			code = 128 + int(sg)
		}
		os.Exit(code)
	}, sigs...)
	return ctx
}
