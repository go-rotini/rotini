package rtk_test

import (
	"sync"
	"testing"
	"time"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Ticker registry
// =============================================================================

func TestTicker_NewTicker_emptySnapshot(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	if got := tk.Snapshot(); got != nil {
		t.Errorf("empty Snapshot: got %v, want nil", got)
	}
}

func TestTicker_Register_addsHandler(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{ID: "metrics", Interval: time.Second, Handler: nopTick})
	got := tk.Snapshot()
	if len(got) != 1 {
		t.Fatalf("Snapshot: got %d handlers, want 1", len(got))
	}
	if got[0].ID != "metrics" || got[0].Interval != time.Second {
		t.Errorf("handler[0]: got %+v, want {ID: metrics, Interval: 1s}", got[0])
	}
}

func TestTicker_Register_replacesByID(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{ID: "metrics", Interval: time.Second, Handler: nopTick})
	tk.Register(rtk.TickHandler{ID: "metrics", Interval: 5 * time.Second, Handler: nopTick})
	got := tk.Snapshot()
	if len(got) != 1 {
		t.Fatalf("Snapshot after replace: got %d handlers, want 1", len(got))
	}
	if got[0].Interval != 5*time.Second {
		t.Errorf("replaced handler interval: got %v, want 5s", got[0].Interval)
	}
}

func TestTicker_Register_emptyIDAppends(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{Interval: time.Second, Handler: nopTick})
	tk.Register(rtk.TickHandler{Interval: time.Second, Handler: nopTick})
	if got := tk.Snapshot(); len(got) != 2 {
		t.Errorf("empty-ID Register: got %d handlers, want 2", len(got))
	}
}

func TestTicker_Unregister_removesByID(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{ID: "a", Interval: time.Second, Handler: nopTick})
	tk.Register(rtk.TickHandler{ID: "b", Interval: time.Second, Handler: nopTick})
	tk.Register(rtk.TickHandler{ID: "c", Interval: time.Second, Handler: nopTick})

	tk.Unregister("b")

	got := tk.Snapshot()
	if len(got) != 2 {
		t.Fatalf("post-unregister: got %d handlers, want 2", len(got))
	}
	for _, h := range got {
		if h.ID == "b" {
			t.Errorf("Unregister(b): \"b\" still present in snapshot")
		}
	}
}

func TestTicker_Unregister_emptyIDNoop(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{Interval: time.Second, Handler: nopTick})
	tk.Unregister("")
	if got := tk.Snapshot(); len(got) != 1 {
		t.Errorf("Unregister(\"\"): mutated state (got %d, want 1)", len(got))
	}
}

// =============================================================================
// Pause / resume
// =============================================================================

func TestTicker_PauseResume(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{ID: "h", Interval: time.Second, Handler: nopTick})

	if !tk.Pause("h") {
		t.Fatal("Pause: returned false for existing handler")
	}
	if got := tk.Snapshot(); !got[0].Paused {
		t.Errorf("Snapshot after Pause: Paused=false, want true")
	}

	if !tk.Resume("h") {
		t.Fatal("Resume: returned false for existing handler")
	}
	if got := tk.Snapshot(); got[0].Paused {
		t.Errorf("Snapshot after Resume: Paused=true, want false")
	}
}

func TestTicker_PauseUnknownID(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	if tk.Pause("nonexistent") {
		t.Errorf("Pause(nonexistent): returned true, want false")
	}
}

func TestTicker_PauseEmptyID(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	if tk.Pause("") {
		t.Errorf("Pause(\"\"): returned true, want false")
	}
}

func TestTicker_ResumeUnknownID(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	if tk.Resume("nonexistent") {
		t.Errorf("Resume(nonexistent): returned true, want false")
	}
}

// =============================================================================
// Snapshot is a defensive copy
// =============================================================================

func TestTicker_Snapshot_isDefensiveCopy(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{ID: "h", Interval: time.Second, Handler: nopTick})

	snap := tk.Snapshot()
	snap[0].Interval = 999 * time.Hour
	snap[0].Paused = true

	got := tk.Snapshot()
	if got[0].Interval != time.Second {
		t.Errorf("snapshot interval mutation leaked: got %v, want 1s", got[0].Interval)
	}
	if got[0].Paused {
		t.Error("snapshot Paused mutation leaked")
	}
}

// =============================================================================
// Concurrent access — race-detector smoke test
// =============================================================================

func TestTicker_ConcurrentRegisterUnregisterPauseResume(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N * 4)
	for i := range N {
		id := stringID(i)
		go func() {
			defer wg.Done()
			tk.Register(rtk.TickHandler{ID: id, Interval: time.Second, Handler: nopTick})
		}()
		go func() {
			defer wg.Done()
			tk.Pause(id)
		}()
		go func() {
			defer wg.Done()
			tk.Resume(id)
		}()
		go func() {
			defer wg.Done()
			tk.Unregister(id)
		}()
	}
	wg.Wait()
	_ = tk.Snapshot()
}

// =============================================================================
// Jitter field round-trips
// =============================================================================

func TestTicker_JitterRoundTrip(t *testing.T) {
	t.Parallel()
	tk := rtk.NewTicker()
	tk.Register(rtk.TickHandler{
		ID:       "jittered",
		Interval: time.Second,
		Jitter:   200 * time.Millisecond,
		Handler:  nopTick,
	})
	got := tk.Snapshot()
	if got[0].Jitter != 200*time.Millisecond {
		t.Errorf("Jitter: got %v, want 200ms", got[0].Jitter)
	}
}

// =============================================================================
// Handler invocation via Snapshot
// =============================================================================

func TestTicker_HandlerCarriesContext(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	reg.Bind("marker", "alive")
	tk := rtk.NewTicker()

	var gotMarker string
	tk.Register(rtk.TickHandler{
		ID:       "h",
		Interval: time.Second,
		Handler: func(c rtk.Ctx) error {
			gotMarker = rtk.Get[string](c.Registry, "marker")
			return nil
		},
	})

	// Simulate lifecycle: snapshot and invoke once.
	for _, h := range tk.Snapshot() {
		if err := h.Handler(rtk.Ctx{Registry: reg}); err != nil {
			t.Fatalf("handler error: %v", err)
		}
	}

	if gotMarker != "alive" {
		t.Errorf("marker via Ctx.Registry: got %q, want %q", gotMarker, "alive")
	}
}

// =============================================================================
// Registry binding
// =============================================================================

func TestTicker_BoundInRegistry(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	want := rtk.NewTicker()
	reg.Bind("ticker", want)
	got := rtk.Get[rtk.Ticker](reg, "ticker")
	if got == nil {
		t.Fatal("Get returned nil Ticker")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func nopTick(_ rtk.Ctx) error { return nil }

// stringID returns a stable string identifier for fuzz/ID generation.
func stringID(i int) string {
	// Avoid strconv for clarity; small range of test IDs.
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < len(digits) {
		return string(digits[i])
	}
	return string(digits[i%len(digits)]) + string(digits[i/len(digits)%len(digits)])
}
