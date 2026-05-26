package rtk

import (
	"context"
	"sync"
	"time"
)

// TickHandler is the callback run on each tick of a registered interval. The
// context is the one passed to [Tickers.Start] (cancelled on [Tickers.Stop] or
// program shutdown), so long-running tick work can bail out promptly.
type TickHandler func(context.Context)

// Tickers is an opt-in rtk service for running callbacks on independent
// intervals — rotini's answer to "do X every N" without putting behavior in the
// spec. It mirrors [Signals]: a named registry you Add to, Start/Stop, and
// Pause/Remove. Each ticker is keyed by a name (one ticker per name; Add
// replaces) and runs in its own goroutine, so different intervals never
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
// (Add/Pause/Remove and their All variants) are safe before or after Start/Stop.
type Tickers struct {
	mu      sync.Mutex
	entries map[string]*tickerEntry
	ctx     context.Context // set by Start; used to launch tickers added while running
	running bool
	wg      sync.WaitGroup
}

// tickerEntry is one named ticker. stop is non-nil exactly while its goroutine
// is running.
type tickerEntry struct {
	interval  time.Duration
	fn        TickHandler
	immediate bool
	stop      chan struct{}
}

// NewTickers returns a new, unstarted tickers client, ready to bind under the
// "tickers" registry key.
func NewTickers() *Tickers {
	return &Tickers{entries: make(map[string]*tickerEntry)}
}

// Add registers fn to run every interval under name, replacing any ticker
// already registered under that name — one ticker per name. The first run is
// after one full interval. It may be called before or after [Tickers.Start];
// adding while running launches the ticker immediately. A nil fn or a
// non-positive interval is ignored. Chainable.
func (t *Tickers) Add(name string, interval time.Duration, fn TickHandler) *Tickers {
	return t.add(name, interval, fn, false)
}

// AddNow is like [Tickers.Add] but also runs fn once immediately when the ticker
// starts, then every interval thereafter.
func (t *Tickers) AddNow(name string, interval time.Duration, fn TickHandler) *Tickers {
	return t.add(name, interval, fn, true)
}

func (t *Tickers) add(name string, interval time.Duration, fn TickHandler, immediate bool) *Tickers {
	if fn == nil || interval <= 0 {
		return t
	}
	t.mu.Lock()
	if old, ok := t.entries[name]; ok && old.stop != nil {
		close(old.stop) // replacing a running ticker: stop the old goroutine
		old.stop = nil
	}
	e := &tickerEntry{interval: interval, fn: fn, immediate: immediate}
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
		go t.run(ctx, e, stop)
	}
	return t
}

// run drives one ticker until its stop is closed or ctx is cancelled. It reads
// only the entry's immutable fields and the passed channels, so it never needs
// the lock.
func (t *Tickers) run(ctx context.Context, e *tickerEntry, stop chan struct{}) {
	defer t.wg.Done()
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

// Start launches a goroutine for every registered ticker. Nothing runs until
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
		e    *tickerEntry
		stop chan struct{}
	}
	launch := make([]pending, 0, len(t.entries))
	for _, e := range t.entries {
		if e.stop == nil {
			e.stop = make(chan struct{})
			t.wg.Add(1)
			launch = append(launch, pending{e, e.stop})
		}
	}
	t.mu.Unlock()

	for _, p := range launch {
		go t.run(ctx, p.e, p.stop)
	}
}

// Stop stops every ticker and waits for any in-flight callback to return. It is
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

// Pause stops the named tickers from firing while keeping their registration, so
// re-registering with [Tickers.Add] relaunches them (and a Stop+Start cycle
// relaunches all kept tickers). Unknown or already-paused names are skipped.
// Chainable.
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

// PauseAll pauses every registered ticker (see [Tickers.Pause]).
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

// Remove stops the named tickers and drops their registration entirely.
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

// RemoveAll stops every ticker and drops all registrations. Chainable.
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
