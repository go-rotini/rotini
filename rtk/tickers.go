package rtk

import (
	"context"
	"sort"
	"sync"
	"time"
)

// TickHandler is the callback run on each tick of a registered interval (or once,
// for a one-shot [Tickers.After]). The context is the one passed to
// [Tickers.Start] (cancelled on [Tickers.Stop] or program shutdown), so
// long-running work can bail out promptly.
type TickHandler func(context.Context)

// Tickers is an opt-in rtk service for running callbacks on a schedule —
// rotini's answer to "do X every N" (and "do X once after N") without putting
// behavior in the spec. It mirrors [Signals]: a named registry you Add to,
// Start/Stop, and Pause/Remove. Each entry is keyed by a name (one per name; the
// register methods replace) and runs in its own goroutine, so schedules never
// interfere.
//
// Like the other rtk services it is bound once and retrieved by handlers, and it
// is driven from the lifecycle hooks — register + [Tickers.Start] in a
// CascadingPreRun/PreRun, [Tickers.Stop] in a CascadingPostRun/PostRun:
//
//	// main.go
//	rth.Program.Bind("tickers", rtk.NewTickers()).Execute()
//
//	// a handler
//	tk := rotini.MustGet[*rtk.Tickers](rtx, "tickers")
//	tk.Add("heartbeat", 30*time.Second, func(ctx context.Context) { heartbeat(ctx) })
//	tk.Start(ctx)
//
// Every method is safe to call in any order and any number of times: Start and
// Stop are idempotent, Stop before Start is a no-op, and the control methods
// (Add/AddNow/After/Pause/Remove and their All variants) are safe before or
// after Start/Stop.
type Tickers struct {
	mu      sync.Mutex
	entries map[string]*tickerEntry
	ctx     context.Context // set by Start; used to launch entries registered while running
	running bool
	wg      sync.WaitGroup
}

// tickerEntry is one named schedule. stop is non-nil exactly while its goroutine
// is running. once marks a one-shot (After); immediate marks a recurring ticker
// that also fires at launch (AddNow).
type tickerEntry struct {
	interval  time.Duration // tick interval, or the one-shot delay when once
	fn        TickHandler
	immediate bool
	once      bool
	stop      chan struct{}
}

// NewTickers returns a new, unstarted tickers client, ready to bind under the
// "tickers" registry key.
func NewTickers() *Tickers {
	return &Tickers{entries: make(map[string]*tickerEntry)}
}

// Add registers fn to run every interval under name, replacing any entry already
// registered under that name — one per name. The first run is after one full
// interval. It may be called before or after [Tickers.Start]; adding while
// running launches it immediately. A nil fn or a non-positive interval is
// ignored. Chainable.
func (t *Tickers) Add(name string, interval time.Duration, fn TickHandler) *Tickers {
	return t.register(name, &tickerEntry{interval: interval, fn: fn})
}

// AddNow is like [Tickers.Add] but also runs fn once immediately when it starts,
// then every interval thereafter.
func (t *Tickers) AddNow(name string, interval time.Duration, fn TickHandler) *Tickers {
	return t.register(name, &tickerEntry{interval: interval, fn: fn, immediate: true})
}

// After registers fn to run once under name, delay later, then removes itself
// from the registry — the one-shot counterpart to [Tickers.Add]. It may be
// called before or after [Tickers.Start]; adding while running arms it
// immediately, and replaces any entry already under name. A nil fn or a
// non-positive delay is ignored. Chainable.
func (t *Tickers) After(name string, delay time.Duration, fn TickHandler) *Tickers {
	return t.register(name, &tickerEntry{interval: delay, fn: fn, once: true})
}

func (t *Tickers) register(name string, e *tickerEntry) *Tickers {
	if e.fn == nil || e.interval <= 0 {
		return t
	}
	t.mu.Lock()
	if old, ok := t.entries[name]; ok && old.stop != nil {
		close(old.stop) // replacing a running entry: stop the old goroutine
		old.stop = nil
	}
	t.entries[name] = e
	ctx := t.ctx
	var stop chan struct{}
	if t.running {
		e.stop = make(chan struct{})
		stop = e.stop
		t.wg.Add(1)
	}
	t.mu.Unlock()

	if stop != nil { // launch outside the lock so an immediate fn can't block on it
		go t.run(ctx, name, e, stop)
	}
	return t
}

// run drives one entry until its stop is closed or ctx is cancelled. It reads
// only the entry's immutable fields and the passed channels, so it never needs
// the lock (except a one-shot's self-removal at the end).
func (t *Tickers) run(ctx context.Context, name string, e *tickerEntry, stop chan struct{}) {
	defer t.wg.Done()

	if e.once {
		timer := time.NewTimer(e.interval)
		defer timer.Stop()
		select {
		case <-timer.C:
			e.fn(ctx)
			t.removeIfCurrent(name, e) // one-shot: drop itself after firing
		case <-stop:
		case <-ctx.Done():
		}
		return
	}

	if e.immediate {
		// Skip the immediate run (and the whole ticker) if it was stopped or the
		// context cancelled before the goroutine got going — same respect for
		// stop/ctx that the periodic ticks have.
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
			e.fn(ctx)
		}
	}
	tk := time.NewTicker(e.interval)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			e.fn(ctx)
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// removeIfCurrent drops name from the registry, but only if it still maps to e —
// so a one-shot that has already been replaced by a re-registration under the
// same name does not delete the replacement.
func (t *Tickers) removeIfCurrent(name string, e *tickerEntry) {
	t.mu.Lock()
	if cur, ok := t.entries[name]; ok && cur == e {
		delete(t.entries, name)
	}
	t.mu.Unlock()
}

// Start launches a goroutine for every registered entry. Nothing runs until
// Start is called. It is idempotent (a second call while running is a no-op) and
// each goroutine stops when [Tickers.Stop] is called or ctx is cancelled; a nil
// ctx is treated as context.Background(). Call it from a CascadingPreRun or
// PreRun hook.
func (t *Tickers) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.ctx = ctx
	type pending struct {
		name string
		e    *tickerEntry
		stop chan struct{}
	}
	launch := make([]pending, 0, len(t.entries))
	for name, e := range t.entries {
		if e.stop == nil {
			e.stop = make(chan struct{})
			t.wg.Add(1)
			launch = append(launch, pending{name, e, e.stop})
		}
	}
	t.mu.Unlock()

	for _, p := range launch {
		go t.run(ctx, p.name, p.e, p.stop)
	}
}

// Stop stops every entry and waits for any in-flight callback to return. It is
// idempotent — calling it before Start, or more than once, is a no-op.
// Registrations are kept, so a later Start relaunches them. Call it from a
// CascadingPostRun or PostRun hook.
func (t *Tickers) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	t.ctx = nil
	for _, e := range t.entries {
		if e.stop != nil {
			close(e.stop)
			e.stop = nil
		}
	}
	t.mu.Unlock()
	t.wg.Wait()
}

// Pause stops the named entries from firing while keeping their registration, so
// re-registering relaunches them (and a Stop+Start cycle relaunches all kept
// entries). Unknown or already-paused names are skipped. Chainable.
func (t *Tickers) Pause(names ...string) *Tickers {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range names {
		if e, ok := t.entries[name]; ok && e.stop != nil {
			close(e.stop)
			e.stop = nil
		}
	}
	return t
}

// PauseAll pauses every registered entry (see [Tickers.Pause]).
func (t *Tickers) PauseAll() *Tickers {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.entries {
		if e.stop != nil {
			close(e.stop)
			e.stop = nil
		}
	}
	return t
}

// Remove stops the named entries and drops their registration entirely.
// Chainable.
func (t *Tickers) Remove(names ...string) *Tickers {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range names {
		if e, ok := t.entries[name]; ok {
			if e.stop != nil {
				close(e.stop)
				e.stop = nil
			}
			delete(t.entries, name)
		}
	}
	return t
}

// RemoveAll stops every entry and drops all registrations. Chainable.
func (t *Tickers) RemoveAll() *Tickers {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, e := range t.entries {
		if e.stop != nil {
			close(e.stop)
			e.stop = nil
		}
		delete(t.entries, name)
	}
	return t
}

// Has reports whether an entry is currently registered under name. (A one-shot
// [Tickers.After] is removed once it fires, so Has returns false afterward.)
func (t *Tickers) Has(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.entries[name]
	return ok
}

// Names returns the names of all registered entries, sorted.
func (t *Tickers) Names() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, 0, len(t.entries))
	for name := range t.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
