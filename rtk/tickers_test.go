package rtk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickersEvery(t *testing.T) {
	var n int64
	tk := NewTickers().Every(10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(75 * time.Millisecond)
	tk.Stop()

	got := atomic.LoadInt64(&n)
	if got < 3 {
		t.Fatalf("expected at least 3 ticks in ~75ms at 10ms, got %d", got)
	}
	time.Sleep(40 * time.Millisecond)
	if after := atomic.LoadInt64(&n); after != got {
		t.Fatalf("ticker kept firing after Stop: %d -> %d", got, after)
	}
}

func TestTickersEveryNowRunsImmediately(t *testing.T) {
	var n int64
	tk := NewTickers().EveryNow(time.Hour, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	tk.Stop()
	if got := atomic.LoadInt64(&n); got != 1 {
		t.Fatalf("EveryNow should run exactly once immediately (1h interval), got %d", got)
	}
}

func TestTickersIndependentIntervals(t *testing.T) {
	var fast, slow int64
	tk := NewTickers().
		Every(10*time.Millisecond, func(context.Context) { atomic.AddInt64(&fast, 1) }).
		Every(50*time.Millisecond, func(context.Context) { atomic.AddInt64(&slow, 1) })
	tk.Start(context.Background())
	time.Sleep(120 * time.Millisecond)
	tk.Stop()

	f, s := atomic.LoadInt64(&fast), atomic.LoadInt64(&slow)
	if f <= s {
		t.Fatalf("fast ticker should fire more than slow: fast=%d slow=%d", f, s)
	}
}

func TestTickersDynamicAddAfterStart(t *testing.T) {
	var n int64
	tk := NewTickers()
	tk.Start(context.Background())
	tk.Every(10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) }) // added after Start
	time.Sleep(55 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&n) < 3 {
		t.Fatalf("a ticker registered after Start should run, got %d ticks", n)
	}
}

func TestTickersStopIdempotent(t *testing.T) {
	tk := NewTickers().Every(time.Hour, func(context.Context) {})
	tk.Start(context.Background())
	tk.Stop()
	tk.Stop() // must not panic or hang
}

func TestTickersStopBeforeStartIsNoop(t *testing.T) {
	NewTickers().Stop() // never started: no-op, no panic
}

func TestTickersStartNilContextNoPanic(t *testing.T) {
	var n int64
	var nilCtx context.Context // exercise the nil-ctx guard without a literal nil (SA1012)
	tk := NewTickers().Every(10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(nilCtx) // must default to Background, not panic the ticker goroutine
	time.Sleep(35 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&n) < 1 {
		t.Fatal("ticker should have run with a nil (defaulted) context")
	}
}

func TestTickersIgnoresInvalidRegistrations(t *testing.T) {
	tk := NewTickers().Every(0, func(context.Context) {}).Every(time.Second, nil)
	tk.Start(context.Background()) // nothing registered → no goroutines
	tk.Stop()
}
