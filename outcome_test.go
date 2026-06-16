package rotini

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRun_recordSuccess_firesOnSuccess: a recorded success fires OnSuccess; the
// default prints it to stdout and the run stays exit 0.
func TestRun_recordSuccess_firesOnSuccess(t *testing.T) {
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

// TestRun_recordWarning_firesOnWarning_nonFatal: a recorded warning fires
// OnWarning to stderr and does NOT fail the run.
func TestRun_recordWarning_firesOnWarning_nonFatal(t *testing.T) {
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

// TestRun_outcomeOrderAndCoexistence: a run that records a warning, a success,
// AND an error fires all three funnels in order (Warning → Success → Error); the
// OnError funnel owns the exit code (here it chooses 1), and success/warning never
// change it.
func TestRun_outcomeOrderAndCoexistence(t *testing.T) {
	var log []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordWarning(errors.New("w"))
		rtx.RecordSuccess("s")
		rtx.RecordError(UsageError(errors.New("e")))
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithOnWarningFn(func(_ context.Context, _ *Context, _ []error) { log = append(log, "warning") })
	p.WithOnSuccessFn(func(_ context.Context, _ *Context, _ []string) { log = append(log, "success") })
	p.WithOnErrorFn(func(_ context.Context, rtx *Context, _ []error) { log = append(log, "error"); rtx.SignalExit(1) })

	code, _ := p.run(p.args)
	if want := []string{"warning", "success", "error"}; strings.Join(log, ",") != strings.Join(want, ",") {
		t.Errorf("funnel order = %v, want %v", log, want)
	}
	if code != 1 {
		t.Errorf("code = %d, want %d (the OnError funnel chose it; success/warning never change it)", code, 1)
	}
}

// TestRun_errorFloor_isDefaultFunnelsNotRuntime pins the hybrid exit-code floor for
// the ERROR channel: the DEFAULT OnError floors a recorded error to 1 (it calls
// rtx.SignalExit(1)), but a CUSTOM OnError OWNS the code — one that omits SignalExit
// exits 0 on the same recorded error, and one that sets a code gets exactly that.
func TestRun_errorFloor_isDefaultFunnelsNotRuntime(t *testing.T) {
	rec := func(rtx *Context) { rtx.RecordError(errors.New("boom")) }

	// Default OnError → floors to 1 (the default funnel owns the floor now).
	pd, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	if code, _ := pd.run(pd.args); code != 1 {
		t.Errorf("default OnError: code = %d, want 1 (default funnel floors)", code)
	}

	// Custom OnError that omits SignalExit → exits 0 (the gap this change closes).
	pc, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	pc.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) {}) // reports nothing, sets no code
	if code, _ := pc.run(pc.args); code != 0 {
		t.Errorf("custom OnError without SignalExit: code = %d, want 0 (custom funnel owns the code)", code)
	}

	// Custom OnError that DOES SignalExit → that code wins.
	ps, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	ps.WithOnErrorFn(func(_ context.Context, rtx *Context, _ []error) { rtx.SignalExit(7) })
	if code, _ := ps.run(ps.args); code != 7 {
		t.Errorf("custom OnError with SignalExit(7): code = %d, want 7", code)
	}
}

// TestRun_faultFloor_isRuntimeNotFunnel pins the other half of the hybrid: the FAULT
// floor is NON-overridable. A custom OnPanic that omits SignalExit STILL exits
// non-zero — a recovered panic / detected fault is the runtime's to floor, never the
// funnel's to mask to 0.
func TestRun_faultFloor_isRuntimeNotFunnel(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: func(_ *Context) { panic("kaboom") }}, []string{"run"})
	p.WithOnPanicFn(func(_ context.Context, _ *Context, _ []*PanicError) {}) // reports nothing, sets no code
	if code, _ := p.run(p.args); code == 0 {
		t.Error("custom OnPanic without SignalExit exited 0; a fault must stay non-zero (runtime floor)")
	}
}

// TestRun_errorAndPanic_bothFire: a run that records an error AND then panics
// fires BOTH OnError and OnPanic, and exits 1 (the default for any error/fault).
func TestRun_errorAndPanic_bothFire(t *testing.T) {
	firedError, firedPanic := false, false
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(UsageError(errors.New("bad input")))
		panic("kaboom")
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) { firedError = true })
	p.WithOnPanicFn(func(_ context.Context, _ *Context, _ []*PanicError) { firedPanic = true })

	code, _ := p.run(p.args)
	if !firedError || !firedPanic {
		t.Errorf("fired OnError=%v OnPanic=%v, want both", firedError, firedPanic)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1 (any error or fault exits 1 by default)", code)
	}
}

// TestRun_wiringFaultRoutesToOnPanic: a Definition↔handlers mismatch is a
// rotini-detected fault — it fires OnPanic (carrying the *WiringError), NOT
// OnError, and exits 1.
func TestRun_wiringFaultRoutesToOnPanic(t *testing.T) {
	var seen *PanicError
	firedError := false
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)
	p.def = Definition{Name: "app", Handler: "Nope"} // no such handler method
	p.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) { firedError = true })
	p.WithOnPanicFn(func(_ context.Context, _ *Context, panics []*PanicError) { seen = panics[0] })

	code, err := p.run(p.args)
	if firedError {
		t.Error("OnError fired for a wiring fault; want OnPanic only")
	}
	var we *WiringError
	if !errors.As(seen, &we) || we.Handler != "Nope" {
		t.Errorf("OnPanic saw %v, want a *WiringError naming Nope", seen)
	}
	if CategoryOf(err) != CategoryInternal || code != 1 {
		t.Errorf("(category, code) = (%v, %d), want (internal, %d)", CategoryOf(err), code, 1)
	}
}

// TestRun_resolverFaultRoutesToOnPanic: a custom resolver's error is a
// resolution-phase fault → OnPanic, not OnError.
func TestRun_resolverFaultRoutesToOnPanic(t *testing.T) {
	firedError, firedPanic := false, false
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, errors.New("routing table on fire")
	})
	p.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) { firedError = true })
	p.WithOnPanicFn(func(_ context.Context, _ *Context, _ []*PanicError) { firedPanic = true })

	code, _ := p.run(p.args)
	if firedError || !firedPanic {
		t.Errorf("fired OnError=%v OnPanic=%v, want only OnPanic for a resolver fault", firedError, firedPanic)
	}
	if code != 1 {
		t.Errorf("code = %d, want %d", code, 1)
	}
}

// TestRun_silentRun_firesNothing: a run that records nothing and never faults
// fires no funnel and exits 0.
func TestRun_silentRun_firesNothing(t *testing.T) {
	fired := false
	h := &testHandlers{log: new([]string)} // onRun nil → a clean run
	p, _, _ := newTestProgram(h, []string{"run"})
	mark := func() { fired = true }
	p.WithOnSuccessFn(func(_ context.Context, _ *Context, _ []string) { mark() })
	p.WithOnWarningFn(func(_ context.Context, _ *Context, _ []error) { mark() })
	p.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) { mark() })
	p.WithOnPanicFn(func(_ context.Context, _ *Context, _ []*PanicError) { mark() })

	code, err := p.run(p.args)
	if fired {
		t.Error("a silent successful run fired a funnel; want none")
	}
	if code != 0 || err != nil {
		t.Errorf("run() = (%d, %v), want (0, nil)", code, err)
	}
}
