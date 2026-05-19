package rtk_test

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Signals registry
// =============================================================================

func TestSignals_NewSignals_emptySnapshot(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	if got := s.Snapshot(); got != nil {
		t.Errorf("empty Snapshot: got %v, want nil", got)
	}
}

func TestSignals_Register_addsHandler(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	s.Register(rtk.SignalHandler{
		Signal:  os.Interrupt,
		Handler: func(_ rtk.Ctx, _ os.Signal) error { return nil },
	})
	got := s.Snapshot()
	if len(got) != 1 {
		t.Fatalf("Snapshot: got %d handlers, want 1", len(got))
	}
	if got[0].Signal != os.Interrupt {
		t.Errorf("handler[0].Signal: got %v, want %v", got[0].Signal, os.Interrupt)
	}
}

func TestSignals_Register_multipleSameSignal(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
	if got := s.Snapshot(); len(got) != 2 {
		t.Errorf("two handlers for same signal: got %d, want 2", len(got))
	}
}

func TestSignals_Unregister_removesBySignal(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
	s.Register(rtk.SignalHandler{Signal: syscall.SIGTERM, Handler: nopSig})
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})

	s.Unregister(os.Interrupt)

	got := s.Snapshot()
	if len(got) != 1 {
		t.Fatalf("post-unregister Snapshot: got %d handlers, want 1", len(got))
	}
	if got[0].Signal != syscall.SIGTERM {
		t.Errorf("remaining handler: got %v, want SIGTERM", got[0].Signal)
	}
}

func TestSignals_Unregister_noopForUnknown(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
	s.Unregister(syscall.SIGHUP) // not registered
	if got := s.Snapshot(); len(got) != 1 {
		t.Errorf("unregister-unknown: got %d handlers, want 1", len(got))
	}
}

func TestSignals_Snapshot_isDefensiveCopy(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
	snap := s.Snapshot()
	// Mutating the snapshot must not affect future snapshots.
	snap[0].Signal = syscall.SIGTERM
	snap2 := s.Snapshot()
	if snap2[0].Signal != os.Interrupt {
		t.Errorf("snapshot mutation leaked: got %v, want os.Interrupt", snap2[0].Signal)
	}
}

func TestSignals_ConcurrentRegisterUnregister(t *testing.T) {
	t.Parallel()
	s := rtk.NewSignals()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for range N {
		go func() {
			defer wg.Done()
			s.Register(rtk.SignalHandler{Signal: os.Interrupt, Handler: nopSig})
		}()
		go func() {
			defer wg.Done()
			s.Unregister(os.Interrupt)
		}()
	}
	wg.Wait()
	// We don't assert a specific count — the test exists to assert
	// that the race detector finds no data races.
	_ = s.Snapshot()
}

// =============================================================================
// Handler invocation through the registered surface
// =============================================================================

func TestSignals_HandlerCarriesContextAndSignal(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	reg.Bind("marker", "alive")
	s := rtk.NewSignals()

	var gotSig os.Signal
	var gotMarker string
	s.Register(rtk.SignalHandler{
		Signal: syscall.SIGUSR1,
		Handler: func(c rtk.Ctx, sig os.Signal) error {
			gotSig = sig
			gotMarker = rtk.Get[string](c.Registry, "marker")
			return nil
		},
	})

	// Simulate lifecycle invocation: snapshot, find matching handler, call it.
	snap := s.Snapshot()
	for _, h := range snap {
		if h.Signal == syscall.SIGUSR1 {
			if err := h.Handler(rtk.Ctx{Registry: reg}, syscall.SIGUSR1); err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
		}
	}

	if gotSig != syscall.SIGUSR1 {
		t.Errorf("Sig: got %v, want SIGUSR1", gotSig)
	}
	if gotMarker != "alive" {
		t.Errorf("marker via Ctx.Registry: got %q, want %q", gotMarker, "alive")
	}
}

// =============================================================================
// Constants
// =============================================================================

func TestSignals_ShutdownSignals(t *testing.T) {
	t.Parallel()
	got := rtk.ShutdownSignals
	if len(got) < 2 {
		t.Fatalf("ShutdownSignals: got %v, want at least 2 entries", got)
	}
	hasInt, hasTerm := false, false
	for _, sig := range got {
		if sig == os.Interrupt {
			hasInt = true
		}
		if sig == syscall.SIGTERM {
			hasTerm = true
		}
	}
	if !hasInt || !hasTerm {
		t.Errorf("ShutdownSignals: got %v, want both os.Interrupt and SIGTERM", got)
	}
}

// =============================================================================
// Registry binding
// =============================================================================

func TestSignals_BoundInRegistry(t *testing.T) {
	t.Parallel()
	reg := rtk.NewRegistry()
	want := rtk.NewSignals()
	reg.Bind("signals", want)
	got := rtk.Get[rtk.Signals](reg, "signals")
	if got == nil {
		t.Fatal("Get returned nil Signals")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func nopSig(_ rtk.Ctx, _ os.Signal) error { return nil }

// errors.Is import retained for consistency with neighboring tests; future
// tests may assert specific sentinel error chains.
var _ = errors.Is
