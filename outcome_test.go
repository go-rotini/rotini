package rotini

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRun_recordSuccess_defaultToStdout: a recorded success reaches the default funnel,
// which prints it to stdout, and the run stays exit 0.
func TestRun_recordSuccess_defaultToStdout(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordSuccess("deployed 3 services")
	}}
	p, out, _ := newTestProgram(h, []string{"run"})
	code, err := p.run(p.args)
	if code != 0 || err != nil {
		t.Fatalf("run() = (%d, %v), want (0, nil)", code, err)
	}
	if !strings.Contains(out.String(), "deployed 3 services") {
		t.Errorf("stdout = %q, want the success on stdout", out)
	}
}

// TestRun_recordInfo_defaultToStdout: a recorded info reaches the default funnel, prints
// to stdout (like success, by intent only), and never changes the exit code.
func TestRun_recordInfo_defaultToStdout(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordInfo("scanning 12 files")
	}}
	p, out, _ := newTestProgram(h, []string{"run"})
	code, err := p.run(p.args)
	if code != 0 || err != nil {
		t.Fatalf("run() = (%d, %v), want (0, nil)", code, err)
	}
	if !strings.Contains(out.String(), "scanning 12 files") {
		t.Errorf("stdout = %q, want the info on stdout", out)
	}
}

// TestRun_recordWarning_defaultNonFatal: a recorded warning reaches the default funnel
// (stderr) and does NOT fail the run.
func TestRun_recordWarning_defaultNonFatal(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordWarning(errors.New("--old is deprecated"))
	}}
	p, _, errb := newTestProgram(h, []string{"run"})
	code, err := p.run(p.args)
	if code != 0 || err != nil {
		t.Fatalf("run() = (%d, %v), want (0, nil) — a warning is non-fatal", code, err)
	}
	if !strings.Contains(errb.String(), "Warning") || !strings.Contains(errb.String(), "--old is deprecated") {
		t.Errorf("stderr = %q, want the warning", errb)
	}
}

// TestRun_funnelReceivesAllChannels: a run that records an info, a success, a warning,
// AND an error hands ALL of them to the single funnel in ONE call (cross-channel
// visibility), each slice in recording order. The funnel owns the exit code.
func TestRun_funnelReceivesAllChannels(t *testing.T) {
	var gotInfos, gotSuccesses []string
	var gotWarnings, gotErrors []error
	var gotPanics []*PanicError
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordInfo("i")
		rtx.RecordWarning(errors.New("w"))
		rtx.RecordSuccess("s")
		rtx.RecordError(UsageError(errors.New("e")))
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(_ context.Context, rtx *Context, infos, successes []string, warnings, errs []error, panics []*PanicError) {
		gotInfos, gotSuccesses, gotWarnings, gotErrors, gotPanics = infos, successes, warnings, errs, panics
		rtx.Exit(1)
	})

	code, _ := p.run(p.args)
	if strings.Join(gotInfos, ",") != "i" || strings.Join(gotSuccesses, ",") != "s" {
		t.Errorf("infos=%v successes=%v, want [i] [s]", gotInfos, gotSuccesses)
	}
	if len(gotWarnings) != 1 || len(gotErrors) != 1 || len(gotPanics) != 0 {
		t.Errorf("warnings=%v errors=%v panics=%v, want 1 warning, 1 error, 0 panics", gotWarnings, gotErrors, gotPanics)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1 (the funnel chose it)", code)
	}
}

// TestRun_exitCode_funnelIsFinalAuthority pins the exit-code model: the DEFAULT funnel
// floors a recorded error to 1, but a CUSTOM funnel OWNS the code — one that ignores the
// error exits 0, one that calls rtx.Exit(7) exits 7, and rtx.Exit even OVERRIDES a code a
// handler set during the lifecycle.
func TestRun_exitCode_funnelIsFinalAuthority(t *testing.T) {
	rec := func(rtx *Context) { rtx.RecordError(errors.New("boom")) }

	// Default funnel → floors to 1.
	pd, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	if code, _ := pd.run(pd.args); code != 1 {
		t.Errorf("default funnel: code = %d, want 1 (floors)", code)
	}

	// Custom funnel that sets no code → exits 0 (no floor; the funnel owns the code).
	pc, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	pc.WithFunnel(func(context.Context, *Context, []string, []string, []error, []error, []*PanicError) {})
	if code, _ := pc.run(pc.args); code != 0 {
		t.Errorf("custom funnel without an exit: code = %d, want 0 (funnel owns the code)", code)
	}

	// Custom funnel that calls rtx.Exit(7) → that code wins.
	ps, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	ps.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, _ []error, _ []*PanicError) { rtx.Exit(7) })
	if code, _ := ps.run(ps.args); code != 7 {
		t.Errorf("custom funnel rtx.Exit(7): code = %d, want 7", code)
	}

	// A handler set 2 during the lifecycle; the funnel OVERRIDES it to 5.
	po, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("boom"))
		rtx.SignalExit(2)
	}}, []string{"run"})
	po.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, _ []error, _ []*PanicError) { rtx.Exit(5) })
	if code, _ := po.run(po.args); code != 5 {
		t.Errorf("funnel override: code = %d, want 5 (the funnel is the final authority)", code)
	}
}

// TestRun_faultExit_defaultFloorsButFunnelCanMask: the DEFAULT funnel floors a recovered
// panic to non-zero, but — per the single-funnel model — a CUSTOM funnel is the final
// authority and MAY mask it to 0 (it owns the exit entirely).
func TestRun_faultExit_defaultFloorsButFunnelCanMask(t *testing.T) {
	panicRun := func(_ *Context) { panic("kaboom") }

	// Default funnel floors a fault to non-zero.
	pd, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: panicRun}, []string{"run"})
	if code, _ := pd.run(pd.args); code == 0 {
		t.Error("default funnel let a panic exit 0; want non-zero (the floor)")
	}

	// A custom funnel that sets no code masks the fault to 0 (it owns the exit).
	pc, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: panicRun}, []string{"run"})
	var sawPanic bool
	pc.WithFunnel(func(_ context.Context, _ *Context, _, _ []string, _, _ []error, panics []*PanicError) {
		sawPanic = len(panics) == 1
	})
	if code, _ := pc.run(pc.args); code != 0 || !sawPanic {
		t.Errorf("custom funnel: (code, sawPanic) = (%d, %v), want (0, true) — the funnel owns the exit", code, sawPanic)
	}
}

// TestRun_errorAndPanic_arriveTogether: a run that records an error AND then panics hands
// BOTH to the one funnel — a non-empty errors slice and a non-empty panics slice — and the
// default floors the exit to 1.
func TestRun_errorAndPanic_arriveTogether(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(UsageError(errors.New("bad input")))
		panic("kaboom")
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	code, err := p.run(p.args) // default funnel
	if code != 1 {
		t.Errorf("code = %d, want 1 (any error or fault floors)", code)
	}
	// joinOutcome surfaces both for errors.As.
	var pe *PanicError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "bad input") {
		t.Errorf("returned err = %v, want both the recorded error and the panic", err)
	}
}

// TestRun_wiringFaultArrivesAsPanic: a Definition↔handlers mismatch is a rotini-detected
// fault — it arrives in the funnel's panics slice (carrying the *WiringError), NOT the
// errors slice, and the default exits 1.
func TestRun_wiringFaultArrivesAsPanic(t *testing.T) {
	var gotErrors []error
	var seen *PanicError
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	p.def = Definition{Name: "app", Handler: "Nope"} // no such handler method
	p.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, errs []error, panics []*PanicError) {
		gotErrors = errs
		if len(panics) > 0 {
			seen = panics[0]
		}
		rtx.Exit(1)
	})

	code, err := p.run(p.args)
	if len(gotErrors) != 0 {
		t.Errorf("errors slice = %v, want empty (a wiring fault is a panic, not an error)", gotErrors)
	}
	var we *WiringError
	if !errors.As(seen, &we) || we.Handler != "Nope" {
		t.Errorf("panics[0] = %v, want a *WiringError naming Nope", seen)
	}
	if CategoryOf(err) != CategoryInternal || code != 1 {
		t.Errorf("(category, code) = (%v, %d), want (internal, 1)", CategoryOf(err), code)
	}
}

// TestRun_resolverFaultArrivesAsPanic: a custom resolver's error is a resolution-phase
// fault → the funnel's panics slice, not its errors slice.
func TestRun_resolverFaultArrivesAsPanic(t *testing.T) {
	var nErrors, nPanics int
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, errors.New("routing table on fire")
	})
	p.WithFunnel(func(_ context.Context, _ *Context, _, _ []string, _, errs []error, panics []*PanicError) {
		nErrors, nPanics = len(errs), len(panics)
	})

	if _, _ = p.run(p.args); nErrors != 0 || nPanics != 1 {
		t.Errorf("(errors, panics) = (%d, %d), want (0, 1) for a resolver fault", nErrors, nPanics)
	}
}

// TestRun_silentRun_doesNotInvokeFunnel: a run that records nothing and never faults does
// not invoke the funnel and exits 0.
func TestRun_silentRun_doesNotInvokeFunnel(t *testing.T) {
	fired := false
	h := &testHandlers{log: new([]string)} // onRun nil → a clean run
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(context.Context, *Context, []string, []string, []error, []error, []*PanicError) { fired = true })

	code, err := p.run(p.args)
	if fired {
		t.Error("a silent successful run invoked the funnel; want no call")
	}
	if code != 0 || err != nil {
		t.Errorf("run() = (%d, %v), want (0, nil)", code, err)
	}
}
