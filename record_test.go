package rotini

import (
	"context"
	"errors"
	"testing"
)

// TestContext_RecordError_api pins the bare recording API: append accumulates in
// order, nil is a no-op, and Errors returns a copy (not the live slice).
func TestContext_RecordError_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.Errors(); got != nil {
		t.Errorf("fresh Errors() = %v, want nil", got)
	}
	e1, e2 := errors.New("one"), errors.New("two")
	rtx.RecordError(e1)
	rtx.RecordError(nil) // no-op
	rtx.RecordError(e2)
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
	nilRtx.RecordError(e1)
	if nilRtx.Errors() != nil {
		t.Error("nil Context Errors() should be nil")
	}
}

// TestContext_RecordWarning_api mirrors the error API for the warning channel:
// order, nil no-op, copy-not-live, nil-receiver safe — and warnings are kept
// SEPARATE from errors.
func TestContext_RecordWarning_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.Warnings(); got != nil {
		t.Errorf("fresh Warnings() = %v, want nil", got)
	}
	w1, w2 := errors.New("warn one"), errors.New("warn two")
	rtx.RecordWarning(w1)
	rtx.RecordWarning(nil) // no-op
	rtx.RecordWarning(w2)
	got := rtx.Warnings()
	if len(got) != 2 || got[0] != w1 || got[1] != w2 {
		t.Fatalf("Warnings() = %v, want [warn one, warn two] with nil skipped", got)
	}
	got[0] = errors.New("mutated")
	if rtx.Warnings()[0] != w1 {
		t.Error("Warnings() returned the live slice; want a copy")
	}
	// Channels are independent: a warning is not an error.
	if rtx.Errors() != nil {
		t.Errorf("RecordWarning leaked into Errors() = %v", rtx.Errors())
	}
	var nilRtx *Context
	nilRtx.RecordWarning(w1)
	if nilRtx.Warnings() != nil {
		t.Error("nil Context Warnings() should be nil")
	}
}

// TestContext_RecordSuccess_api mirrors it for the success channel; an empty
// string is the no-op (success carries a message).
func TestContext_RecordSuccess_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.Successes(); got != nil {
		t.Errorf("fresh Successes() = %v, want nil", got)
	}
	rtx.RecordSuccess("done a")
	rtx.RecordSuccess("") // no-op
	rtx.RecordSuccess("done b")
	got := rtx.Successes()
	if len(got) != 2 || got[0] != "done a" || got[1] != "done b" {
		t.Fatalf("Successes() = %v, want [done a, done b] with empty skipped", got)
	}
	got[0] = "mutated"
	if rtx.Successes()[0] != "done a" {
		t.Error("Successes() returned the live slice; want a copy")
	}
	if rtx.Errors() != nil || rtx.Warnings() != nil {
		t.Error("RecordSuccess leaked into Errors()/Warnings()")
	}
	var nilRtx *Context
	nilRtx.RecordSuccess("x")
	if nilRtx.Successes() != nil {
		t.Error("nil Context Successes() should be nil")
	}
}

// TestContext_Panics_api pins the private fault channel: recordFault (the
// lifecycle's, not a handler's) accumulates; Panics returns a copy; nil is a
// no-op; and faults are independent of the error channel.
func TestContext_Panics_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.Panics(); got != nil {
		t.Errorf("fresh Panics() = %v, want nil", got)
	}
	p1, p2 := &PanicError{Value: "boom"}, &PanicError{Value: errors.New("kaboom")}
	rtx.recordFault(p1)
	rtx.recordFault(nil) // no-op
	rtx.recordFault(p2)
	got := rtx.Panics()
	if len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("Panics() = %v, want [p1 p2] with nil skipped", got)
	}
	got[0] = &PanicError{Value: "mutated"}
	if rtx.Panics()[0] != p1 {
		t.Error("Panics() returned the live slice; want a copy")
	}
	if rtx.Errors() != nil {
		t.Errorf("recordFault leaked into Errors() = %v", rtx.Errors())
	}
	var nilRtx *Context
	nilRtx.recordFault(p1)
	if nilRtx.Panics() != nil {
		t.Error("nil Context Panics() should be nil")
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
			rtx.RecordError(errA)
			rtx.RecordError(errB)
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
			rtx.RecordError(errA)
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
			rtx.RecordError(errB) // no SignalExit/Exit
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

// TestDefaultExitCode pins EH3's exit mapping: the most severe category present
// wins — internal outranks usage outranks an unclassified failure — and an
// unclassified set still floors to 1 (never 0).
func TestDefaultExitCode(t *testing.T) {
	usage := UsageError(errors.New("u"))
	internal := InternalError(errors.New("i"))
	none := errors.New("n")
	cases := []struct {
		name string
		errs []error
		want int
	}{
		{"unclassified only", []error{none}, 1},
		{"usage only", []error{usage}, ExitUsage},
		{"internal only", []error{internal}, ExitInternal},
		{"usage then internal", []error{usage, internal}, ExitInternal},
		{"internal then usage", []error{internal, usage}, ExitInternal},
		{"none alongside usage", []error{none, usage}, ExitUsage},
		{"none alongside internal", []error{none, internal}, ExitInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultExitCode(tc.errs); got != tc.want {
				t.Errorf("defaultExitCode(%v) = %d, want %d", tc.errs, got, tc.want)
			}
		})
	}
}

// TestRun_defaultOnError_classifiesAndPrints is EH3's end-to-end golden: with no
// custom funnel, the default drains rtx.Errors(), prints one program-name-
// prefixed line per error to stderr, and exits by the most severe category.
func TestRun_defaultOnError_classifiesAndPrints(t *testing.T) {
	cases := []struct {
		name   string
		record []error
		want   int
		stderr string // exact stderr (the format golden); program name is "app"
	}{
		{"usage", []error{UsageError(errors.New("bad flag"))}, ExitUsage, "app: bad flag\n"},
		{"internal", []error{InternalError(errors.New("broken wiring"))}, ExitInternal, "app: broken wiring\n"},
		{"unclassified", []error{errors.New("mystery")}, 1, "app: mystery\n"},
		{"mixed: internal outranks usage", []error{UsageError(errors.New("bad flag")), InternalError(errors.New("bug"))}, ExitInternal, "app: bad flag\napp: bug\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs := tc.record
			h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
				for _, e := range recs {
					rtx.RecordError(e)
				}
			}}
			p, _, errb := newTestProgram(h, []string{"run"}) // no WithOnErrorFn → default
			code, _ := p.run(p.args)
			if code != tc.want {
				t.Errorf("code = %d, want %d", code, tc.want)
			}
			if got := errb.String(); got != tc.stderr {
				t.Errorf("stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}
