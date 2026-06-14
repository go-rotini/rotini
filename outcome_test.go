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
	if !strings.Contains(errb.String(), "warning") || !strings.Contains(errb.String(), "--old is deprecated") {
		t.Errorf("stderr = %q, want the warning", errb)
	}
}

// TestRun_outcomeOrderAndCoexistence: a run that records a warning, a success,
// AND an error fires all three funnels in order (Warning → Success → Error), and
// the error's category drives the exit (D5/D6).
func TestRun_outcomeOrderAndCoexistence(t *testing.T) {
	var log []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordWarning(errors.New("w"))
		rtx.RecordSuccess("s")
		rtx.RecordError(UsageError(errors.New("e"))) // usage → exit 2
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithOnWarningFn(func(_ context.Context, _ *Context, _ []error) { log = append(log, "warning") })
	p.WithOnSuccessFn(func(_ context.Context, _ *Context, _ []string) { log = append(log, "success") })
	p.WithOnErrorFn(func(_ context.Context, _ *Context, _ []error) { log = append(log, "error") })

	code, _ := p.run(p.args)
	if want := []string{"warning", "success", "error"}; strings.Join(log, ",") != strings.Join(want, ",") {
		t.Errorf("funnel order = %v, want %v", log, want)
	}
	if code != 1 {
		t.Errorf("code = %d, want %d (the error's category, success notwithstanding)", code, 1)
	}
}

// TestRun_errorAndPanic_maxSeverityExit: a run that records a usage error AND
// then panics fires BOTH OnError and OnPanic, and exits by max severity — the
// panic's 70 (70) is not masked by the error's 1 (2) (D7).
func TestRun_errorAndPanic_maxSeverityExit(t *testing.T) {
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
	if code != 70 {
		t.Errorf("code = %d, want %d (panic severity not masked by the usage error)", code, 70)
	}
}

// TestRun_wiringFaultRoutesToOnPanic: a Definition↔handlers mismatch is a
// rotini-detected fault — it fires OnPanic (carrying the *WiringError), NOT
// OnError, and exits 70.
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
	if CategoryOf(err) != CategoryInternal || code != 70 {
		t.Errorf("(category, code) = (%v, %d), want (internal, %d)", CategoryOf(err), code, 70)
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
	if code != 70 {
		t.Errorf("code = %d, want %d", code, 70)
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
