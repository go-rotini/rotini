package rtk

import (
	"context"
	"sync"
	"time"
)

// TickHandler is a callback run on each tick of a registered interval. The
// context is the one passed to [Tickers.Start] (cancelled on [Tickers.Stop] or
// program shutdown), so long-running tick work can bail out promptly.
type TickHandler func(context.Context)

// Tickers is an opt-in rtk service for running callbacks on independent
// intervals — rotini's answer to "do X every N seconds" without putting behavior
// in the spec. Register as many callbacks as you like, each on its own interval;
// each runs in its own goroutine, so different intervals never interfere.
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
//	tk.Every(30*time.Second, func(ctx context.Context) { heartbeat(ctx) })
//	tk.Start(ctx)
//
// Start and Stop are idempotent, so a cascading (program-wide) hook and a leaf
// hook may both drive the same service.
type Tickers struct {
	mu      sync.Mutex
	entries []tick
	running bool
	ctx     context.Context // set by Start; used to launch tickers registered after Start
	stop    chan struct{}
	wg      sync.WaitGroup
}

// tick is one registered interval callback.
type tick struct {
	interval  time.Duration
	fn        TickHandler
	immediate bool
}

// NewTickers returns an unstarted [Tickers] service, ready to bind under the
// "tickers" registry key.
func NewTickers() *Tickers {
	return &Tickers{}
}

// Every registers fn to run every interval; the first run is after one full
// interval. Each registration is independent of every other. A nil fn or a
// non-positive interval is ignored. Every returns the receiver so registrations
// chain, and is safe to call before [Tickers.Start].
func (t *Tickers) Every(interval time.Duration, fn TickHandler) *Tickers {
	return t.add(interval, fn, false)
}

// EveryNow is like [Tickers.Every] but also runs fn once immediately when
// [Tickers.Start] is called, then every interval thereafter.
func (t *Tickers) EveryNow(interval time.Duration, fn TickHandler) *Tickers {
	return t.add(interval, fn, true)
}

func (t *Tickers) add(interval time.Duration, fn TickHandler, immediate bool) *Tickers {
	if fn == nil || interval <= 0 {
		return t
	}
	e := tick{interval: interval, fn: fn, immediate: immediate}
	t.mu.Lock()
	t.entries = append(t.entries, e)
	// Registered after Start: launch it now against the running context so
	// Every/EveryNow behave the same whether called before or after Start.
	if t.running {
		ctx, stop := t.ctx, t.stop
		t.wg.Add(1)
		t.mu.Unlock()
		go t.run(ctx, e, stop)
		return t
	}
	t.mu.Unlock()
	return t
}

// Start launches one goroutine per registered interval. It is idempotent (a
// second call while running is a no-op) and each goroutine stops when
// [Tickers.Stop] is called or ctx is cancelled; a nil ctx is treated as
// context.Background(). Call it from a CascadingPreRun or PreRun hook.
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
	t.stop = make(chan struct{})
	stop := t.stop
	entries := append([]tick(nil), t.entries...)
	t.mu.Unlock()

	for _, e := range entries {
		t.wg.Add(1)
		go t.run(ctx, e, stop)
	}
}

// run drives one registered interval until stop is closed or ctx is cancelled.
func (t *Tickers) run(ctx context.Context, e tick, stop chan struct{}) {
	defer t.wg.Done()
	if e.immediate {
		e.fn(ctx)
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

// Stop stops all tickers and waits for any in-flight callback to return. It is
// idempotent. Call it from a CascadingPostRun or PostRun hook. (Callbacks should
// respect the context passed to [Tickers.Start] so a long one can be cancelled
// rather than make Stop block.)
func (t *Tickers) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	t.ctx = nil
	close(t.stop)
	t.mu.Unlock()
	t.wg.Wait()
}
