package rtk

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSignalsDispatchOrder(t *testing.T) {
	var order []int
	s := NewSignals()
	s.On(os.Interrupt, func(os.Signal) { order = append(order, 1) }).
		On(os.Interrupt, func(os.Signal) { order = append(order, 2) })
	s.dispatch(os.Interrupt)
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("handlers should run in registration order, got %v", order)
	}
}

func TestSignalsPassesFiredSignal(t *testing.T) {
	var got os.Signal
	NewSignals().On(os.Interrupt, func(sig os.Signal) { got = sig }).dispatch(os.Interrupt)
	if got != os.Interrupt {
		t.Fatalf("handler should receive the fired signal, got %v", got)
	}
}

func TestSignalsNilHandlerIgnored(t *testing.T) {
	NewSignals().On(os.Interrupt, nil).dispatch(os.Interrupt) // must not panic
}

func TestSignalsStartDelivers(t *testing.T) {
	s := NewSignals()
	fired := make(chan os.Signal, 1)
	s.On(os.Interrupt, func(sig os.Signal) { fired <- sig })
	s.Start(context.Background())
	defer s.Stop()

	s.ch <- os.Interrupt // simulate OS delivery into the running loop
	select {
	case sig := <-fired:
		if sig != os.Interrupt {
			t.Fatalf("got %v", sig)
		}
	case <-time.After(time.Second):
		t.Fatal("handler not invoked by the delivery goroutine")
	}
}

func TestSignalsStartStopIdempotent(t *testing.T) {
	s := NewSignals().On(os.Interrupt, func(os.Signal) {})
	s.Start(context.Background())
	s.Start(context.Background()) // no-op while running
	s.Stop()
	s.Stop() // must not panic
}

func TestSignalsIgnoreResetChainable(t *testing.T) {
	s := NewSignals()
	if s.Ignore(os.Interrupt) != s {
		t.Fatal("Ignore should return the receiver for chaining")
	}
	if s.Reset(os.Interrupt) != s { // restore default SIGINT disposition
		t.Fatal("Reset should return the receiver for chaining")
	}
}

func TestSignalsResetClearsHandlers(t *testing.T) {
	s := NewSignals().On(os.Interrupt, func(os.Signal) {
		t.Fatal("handler should have been cleared by Reset")
	})
	s.Reset(os.Interrupt)
	s.dispatch(os.Interrupt) // no handlers remain → nothing runs
}

func TestSignalsStartCancelsWithContext(t *testing.T) {
	s := NewSignals().On(os.Interrupt, func(os.Signal) {})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	cancel() // the delivery goroutine should exit; Stop still safe afterward
	time.Sleep(20 * time.Millisecond)
	s.Stop()
}
