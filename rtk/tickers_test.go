package rtk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickersAdd(t *testing.T) {
	var n int64
	tk := NewTickers().Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
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

func TestTickersAddNowRunsImmediately(t *testing.T) {
	var n int64
	tk := NewTickers().AddNow("a", time.Hour, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	tk.Stop()
	if got := atomic.LoadInt64(&n); got != 1 {
		t.Fatalf("AddNow should run exactly once immediately (1h interval), got %d", got)
	}
}

func TestTickersAddReplacesByName(t *testing.T) {
	var a, b int64
	tk := NewTickers().
		Add("job", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&a, 1) }).
		Add("job", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&b, 1) }) // replaces "job"
	tk.Start(context.Background())
	time.Sleep(45 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&a) != 0 {
		t.Fatalf("replaced ticker should not run; a=%d", a)
	}
	if atomic.LoadInt64(&b) < 2 {
		t.Fatalf("replacement ticker should run; b=%d", b)
	}
}

func TestTickersIndependentNames(t *testing.T) {
	var fast, slow int64
	tk := NewTickers().
		Add("fast", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&fast, 1) }).
		Add("slow", 50*time.Millisecond, func(context.Context) { atomic.AddInt64(&slow, 1) })
	tk.Start(context.Background())
	time.Sleep(120 * time.Millisecond)
	tk.Stop()
	if f, s := atomic.LoadInt64(&fast), atomic.LoadInt64(&slow); f <= s {
		t.Fatalf("fast ticker should fire more than slow: fast=%d slow=%d", f, s)
	}
}

func TestTickersDynamicAddAfterStart(t *testing.T) {
	var n int64
	tk := NewTickers()
	tk.Start(context.Background())
	tk.Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) }) // added after Start
	time.Sleep(55 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&n) < 3 {
		t.Fatalf("a ticker registered after Start should run, got %d ticks", n)
	}
}

func TestTickersPauseStopsFiring(t *testing.T) {
	var n int64
	tk := NewTickers().Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(45 * time.Millisecond)
	tk.Pause("a")
	time.Sleep(15 * time.Millisecond) // let the goroutine observe the close and exit
	got := atomic.LoadInt64(&n)
	if got < 2 {
		t.Fatalf("expected ticks before pause, got %d", got)
	}
	time.Sleep(40 * time.Millisecond)
	if after := atomic.LoadInt64(&n); after != got {
		t.Fatalf("paused ticker kept firing: %d -> %d", got, after)
	}
	tk.Stop()
}

func TestTickersPauseKeptAcrossRestart(t *testing.T) {
	var n int64
	tk := NewTickers().Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	tk.Pause("a") // paused before its first tick
	time.Sleep(15 * time.Millisecond)
	tk.Stop()
	tk.Start(context.Background()) // restart relaunches the kept (paused) registration
	time.Sleep(45 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&n) < 1 {
		t.Fatal("a paused ticker's registration should survive and relaunch on restart")
	}
}

func TestTickersRemoveDropsRegistration(t *testing.T) {
	var n int64
	tk := NewTickers().Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(35 * time.Millisecond)
	tk.Remove("a")
	time.Sleep(15 * time.Millisecond)
	got := atomic.LoadInt64(&n)

	tk.Stop()
	tk.Start(context.Background()) // removed ticker must NOT come back
	time.Sleep(40 * time.Millisecond)
	tk.Stop()
	if after := atomic.LoadInt64(&n); after != got {
		t.Fatalf("removed ticker fired after Remove/restart: %d -> %d", got, after)
	}
}

func TestTickersAfterRunsOnceAndSelfRemoves(t *testing.T) {
	var n int64
	tk := NewTickers().After("once", 25*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(context.Background())
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt64(&n); got != 1 {
		t.Fatalf("After should fire exactly once, got %d", got)
	}
	if tk.Has("once") {
		t.Fatal("After should self-remove from the registry once it has fired")
	}
	tk.Stop()
}

func TestTickersAfterDynamicAndStoppedBeforeFire(t *testing.T) {
	var n int64
	tk := NewTickers()
	tk.Start(context.Background())
	tk.After("later", time.Hour, func(context.Context) { atomic.AddInt64(&n, 1) }) // armed after Start
	tk.Stop()                                                                      // before the 1h delay
	if atomic.LoadInt64(&n) != 0 {
		t.Fatal("After must not fire if stopped before its delay")
	}
	if !tk.Has("later") {
		t.Fatal("an un-fired After should remain registered after Stop")
	}
}

func TestTickersHasAndNames(t *testing.T) {
	tk := NewTickers().
		Add("b", time.Hour, func(context.Context) {}).
		Add("a", time.Hour, func(context.Context) {})
	if !tk.Has("a") || !tk.Has("b") {
		t.Fatal("Has should report registered entries")
	}
	if tk.Has("zzz") {
		t.Fatal("Has should be false for an unregistered name")
	}
	if got := tk.Names(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names should return sorted registered names, got %v", got)
	}
	tk.Remove("a")
	if tk.Has("a") {
		t.Fatal("Remove should drop the name from Has")
	}
	if got := tk.Names(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("Names after Remove, got %v", got)
	}
}

func TestTickersStopBeforeStartIsNoop(t *testing.T) {
	NewTickers().Stop() // never started: no-op, no panic
}

func TestTickersStopIdempotentAndRestart(t *testing.T) {
	tk := NewTickers().Add("a", time.Hour, func(context.Context) {})
	tk.Start(context.Background())
	tk.Start(context.Background()) // idempotent
	tk.Stop()
	tk.Stop()                      // idempotent, no double-close panic
	tk.Start(context.Background()) // restart
	tk.Stop()
}

func TestTickersStartNilContextNoPanic(t *testing.T) {
	var n int64
	var nilCtx context.Context // exercise the nil-ctx guard without a literal nil (SA1012)
	tk := NewTickers().Add("a", 10*time.Millisecond, func(context.Context) { atomic.AddInt64(&n, 1) })
	tk.Start(nilCtx) // must default to Background, not panic the ticker goroutine
	time.Sleep(35 * time.Millisecond)
	tk.Stop()
	if atomic.LoadInt64(&n) < 1 {
		t.Fatal("ticker should have run with a nil (defaulted) context")
	}
}

func TestTickersControlMethodsBeforeStartNoPanic(t *testing.T) {
	tk := NewTickers()
	tk.Pause("a").PauseAll().Remove("a").RemoveAll() // safe with nothing registered/started
	tk.Add("a", time.Hour, func(context.Context) {}).Pause("a").Remove("a")
}

func TestTickersIgnoresInvalidRegistrations(t *testing.T) {
	tk := NewTickers().Add("z", 0, func(context.Context) {}).Add("y", time.Second, nil)
	tk.Start(context.Background()) // nothing registered → no goroutines
	tk.Stop()
}
