package rtk

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSignalsAddReplacesHandler(t *testing.T) {
	var ran string
	s := NewSignals()
	s.Add(os.Interrupt, func() { ran = "first" }).
		Add(os.Interrupt, func() { ran = "second" }) // one handler per signal: last wins
	s.dispatch(os.Interrupt)
	if ran != "second" {
		t.Fatalf("Add should replace the per-signal handler; ran %q", ran)
	}
}

func TestSignalsDispatchRunsHandler(t *testing.T) {
	ran := false
	NewSignals().Add(os.Interrupt, func() { ran = true }).dispatch(os.Interrupt)
	if !ran {
		t.Fatal("dispatch should run the registered handler")
	}
}

func TestSignalsNilHandlerIgnored(t *testing.T) {
	NewSignals().Add(os.Interrupt, nil).dispatch(os.Interrupt) // must not panic
}

func TestSignalsStartDelivers(t *testing.T) {
	s := NewSignals()
	fired := make(chan struct{}, 1)
	s.Add(os.Interrupt, func() { fired <- struct{}{} })
	s.Start(context.Background())
	defer s.Stop()

	s.ch <- os.Interrupt // simulate OS delivery into the running loop
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("handler not invoked by the delivery goroutine")
	}
}

func TestSignalsStartStopIdempotent(t *testing.T) {
	s := NewSignals().Add(os.Interrupt, func() {})
	s.Start(context.Background())
	s.Start(context.Background()) // no-op while running
	s.Stop()
	s.Stop() // must not panic
}

func TestSignalsRemoveDropsHandler(t *testing.T) {
	s := NewSignals().Add(os.Interrupt, func() {
		t.Fatal("handler should have been removed")
	})
	s.Remove(os.Interrupt)   // reset + drop handler
	s.dispatch(os.Interrupt) // nothing remains to run
}

func TestSignalsRemoveAllDropsHandlers(t *testing.T) {
	s := NewSignals().Add(os.Interrupt, func() { t.Fatal("should be cleared") })
	s.RemoveAll()
	s.dispatch(os.Interrupt)
}

func TestSignalsPauseChainableAndScoped(t *testing.T) {
	s := NewSignals()
	if s.PauseAll() != s { // no handlers registered → no-op, still chainable
		t.Fatal("PauseAll should be chainable")
	}
	s.Add(os.Interrupt, func() {})
	if s.Pause(os.Interrupt) != s {
		t.Fatal("Pause should be chainable")
	}
	s.Remove(os.Interrupt) // restore default disposition after pausing
}

func TestSignalsStopBeforeStartIsNoop(t *testing.T) {
	NewSignals().Stop() // never started: must be a no-op, no panic, no nil-channel close
}

func TestSignalsRestart(t *testing.T) {
	s := NewSignals().Add(os.Interrupt, func() {})
	s.Start(context.Background())
	s.Stop()
	s.Start(context.Background()) // restart: fresh stop channel + goroutine, no double-close panic
	s.Stop()
}

func TestSignalsStartNilContextNoPanic(t *testing.T) {
	var nilCtx context.Context // exercise the nil-ctx guard without a literal nil (SA1012)
	s := NewSignals().Add(os.Interrupt, func() {})
	s.Start(nilCtx) // must default to Background, not panic the delivery goroutine
	time.Sleep(20 * time.Millisecond)
	s.Stop()
}

func TestSignalsControlMethodsBeforeStartNoPanic(t *testing.T) {
	s := NewSignals()
	s.Pause(os.Interrupt).PauseAll().Remove(os.Interrupt).RemoveAll() // all safe with nothing registered/started
	s.Add(os.Interrupt, func() {}).Pause(os.Interrupt).PauseAll()
	s.Remove(os.Interrupt) // restore default disposition
}

func TestSignalsHasAndSignals(t *testing.T) {
	s := NewSignals()
	if s.Has(os.Interrupt) {
		t.Fatal("Has should be false before Add")
	}
	if len(s.Signals()) != 0 {
		t.Fatal("Signals should be empty initially")
	}
	s.Add(os.Interrupt, func() {})
	if !s.Has(os.Interrupt) {
		t.Fatal("Has should be true after Add")
	}
	if got := s.Signals(); len(got) != 1 || got[0] != os.Interrupt {
		t.Fatalf("Signals should list the registered signal, got %v", got)
	}
	s.Remove(os.Interrupt)
	if s.Has(os.Interrupt) {
		t.Fatal("Has should be false after Remove")
	}
	if len(s.Signals()) != 0 {
		t.Fatal("Signals should be empty after Remove")
	}
}

func TestSignalsStartCancelsWithContext(t *testing.T) {
	s := NewSignals().Add(os.Interrupt, func() {})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	cancel() // the delivery goroutine should exit; Stop still safe afterward
	time.Sleep(20 * time.Millisecond)
	s.Stop()
}
