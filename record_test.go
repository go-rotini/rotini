package rotini

import (
	"context"
	"errors"
	"testing"
)

// TestContext_RecordErr_api pins the bare recording API: append accumulates in
// order, nil is a no-op, and Errors returns a copy (not the live slice).
func TestContext_RecordErr_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.Errors(); got != nil {
		t.Errorf("fresh Errors() = %v, want nil", got)
	}
	e1, e2 := errors.New("one"), errors.New("two")
	rtx.RecordErr(e1)
	rtx.RecordErr(nil) // no-op
	rtx.RecordErr(e2)
	got := rtx.Errors()
	if len(got) != 2 || got[0] != e1 || got[1] != e2 {
		t.Fatalf("Errors() = %v, want [one two] in order with nil skipped", got)
	}
	got[0] = errors.New("mutated")
	if rtx.Errors()[0] != e1 {
		t.Error("Errors() returned the live slice; want a copy")
	}
	// Nil receiver is safe.
	var nilRtx *Context
	nilRtx.RecordErr(e1)
	if nilRtx.Errors() != nil {
		t.Error("nil Context Errors() should be nil")
	}
}

// TestRun_recordedErrorsFireOnError pins EH1's firing contract and the three
// locked edges: OnError fires iff ≥1 error was recorded (edge 1); record-
// without-exit still fires it (edge 2); recorded errors floor a 0 code to 1
// (edge 3); and the SignalExit/Exit choice still governs teardown.
func TestRun_recordedErrorsFireOnError(t *testing.T) {
	errA := UsageError(errors.New("bad flag")) // carries ErrUsage through the join
	errB := errors.New("also bad")

	exec := func(onRun func(rtx *Context)) (log []string, code int, funneled error, drained []error, fired bool) {
		h := &testHandlers{log: &log, onRun: onRun}
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithOnErrorFn(func(_ context.Context, rtx *Context, err error) {
			fired, funneled, drained = true, err, rtx.Errors()
		})
		code, _ = p.run(p.args)
		return
	}

	t.Run("record + SignalExit: fires, teardown runs, join keeps tags", func(t *testing.T) {
		log, code, funneled, drained, fired := exec(func(rtx *Context) {
			rtx.RecordErr(errA)
			rtx.RecordErr(errB)
			rtx.SignalExit(ExitUsage)
		})
		if !fired {
			t.Fatal("OnError did not fire on recorded errors")
		}
		if len(drained) != 2 {
			t.Errorf("rtx.Errors() drained %d, want 2", len(drained))
		}
		if !errors.Is(funneled, ErrUsage) {
			t.Error("joined err lost errA's ErrUsage tag")
		}
		if code != ExitUsage {
			t.Errorf("code = %d, want %d", code, ExitUsage)
		}
		if !contains(log, "run.PostRun") || !contains(log, "app.CascadingPostRun") {
			t.Errorf("teardown did not run on graceful SignalExit: %v", log)
		}
	})

	t.Run("record + Exit: fires, teardown skipped", func(t *testing.T) {
		log, code, _, _, fired := exec(func(rtx *Context) {
			rtx.RecordErr(errA)
			rtx.Exit(5)
		})
		if !fired {
			t.Fatal("OnError did not fire")
		}
		if code != 5 {
			t.Errorf("code = %d, want 5", code)
		}
		if contains(log, "run.PostRun") {
			t.Errorf("teardown ran despite hard Exit: %v", log)
		}
	})

	t.Run("record without exit: still fires, code floored to 1 (edges 2,3)", func(t *testing.T) {
		log, code, _, _, fired := exec(func(rtx *Context) {
			rtx.RecordErr(errB) // no SignalExit/Exit
		})
		if !fired {
			t.Fatal("OnError did not fire on record-without-exit (edge 2)")
		}
		if code != 1 {
			t.Errorf("code = %d, want 1 (floored — edge 3)", code)
		}
		if !contains(log, "run.PostRun") {
			t.Errorf("teardown should run when no exit was called: %v", log)
		}
	})

	t.Run("no errors recorded: does NOT fire (edge 1)", func(t *testing.T) {
		_, code, _, _, fired := exec(func(rtx *Context) {
			rtx.SignalExit(0) // clean success
		})
		if fired {
			t.Error("OnError fired with no recorded errors (edge 1 violated)")
		}
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
}
