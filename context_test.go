package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestGet_typed(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)

	if got, ok := Get[*bytes.Buffer](rtx, "buf"); !ok || got != buf {
		t.Fatalf("Get[*bytes.Buffer] = (%v, %v), want the bound buffer", got, ok)
	}
	if _, ok := Get[*bytes.Buffer](rtx, "missing"); ok {
		t.Error("Get of an unbound key should be ok=false")
	}
	if _, ok := Get[*int](rtx, "buf"); ok {
		t.Error("Get of a wrong-typed binding should be ok=false")
	}
}

func TestBindIfAbsent_keepsExistingElseRegistersDefault(t *testing.T) {
	rtx := newContext()
	injected, def := &bytes.Buffer{}, &bytes.Buffer{}

	// A prior binding (e.g. a test's double) is kept — BindIfAbsent no-ops.
	rtx.Bind("buf", injected)
	rtx.BindIfAbsent("buf", def)
	if MustGet[*bytes.Buffer](rtx, "buf") != injected {
		t.Error("BindIfAbsent must not overwrite an existing binding")
	}

	// An absent key gets the default registered into the registry.
	rtx.BindIfAbsent("other", def)
	if MustGet[*bytes.Buffer](rtx, "other") != def {
		t.Error("BindIfAbsent should register the default when the key is absent")
	}
}

func TestMustGet_returnsBoundService(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)
	if MustGet[*bytes.Buffer](rtx, "buf") != buf {
		t.Fatal("MustGet did not return the bound service")
	}
}

func TestMustGet_panicsServiceError(t *testing.T) {
	rtx := newContext() // nothing bound

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok {
			t.Fatalf("MustGet panicked with %v (%T), want an error", r, r)
		}
		if !errors.Is(err, ErrServiceNotFound) {
			t.Errorf("panic = %v, want it to wrap ErrServiceNotFound", err)
		}
		var se *ServiceError
		if !errors.As(err, &se) || se.Key != "missing" {
			t.Errorf("panic did not carry the key: %v", err)
		}
	}()

	_ = MustGet[*bytes.Buffer](rtx, "missing") // panics → recovered above
}

func TestMustGet_panicsOnWrongType(t *testing.T) {
	rtx := newContext()
	rtx.Bind("buf", &bytes.Buffer{})

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet of a wrong-typed binding should panic")
		}
	}()

	_ = MustGet[*int](rtx, "buf") // bound, but not an *int → panics
}

// The registry is the DI seam shared by every hook, and concurrent tooling (tickers,
// completers, a handler's own goroutines) may read and write it at once. This hammers
// Bind/Has/Value/Get from many goroutines so the race detector proves the RWMutex
// guards every path, and confirms all bindings survive the storm.
func TestContext_concurrentRegistry(t *testing.T) {
	rtx := newContext()
	const (
		workers = 64
		keys    = 8
		iters   = 250
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("svc-%d", w%keys)
			for i := 0; i < iters; i++ {
				rtx.Bind(key, w)
				_ = rtx.Value(key)
				_, _ = Get[int](rtx, key)
			}
		}(w)
	}
	wg.Wait()

	for k := 0; k < keys; k++ {
		if rtx.Value(fmt.Sprintf("svc-%d", k)) == nil {
			t.Errorf("key svc-%d unbound after concurrent access", k)
		}
	}
}

// TestContext_ArgsField locks the W1 contract: rtx.Args is the exact argument
// vector the invocation was given — os.Args[1:] or the Program.WithArgs
// override — exposed as a plain field (the live slice, not a copy).
func TestContext_ArgsField(t *testing.T) {
	argv := []string{"--verbose", "run", "alice", "--", "-not-a-flag"}

	// The standalone constructor round-trips argv exactly.
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, argv)
	if !reflect.DeepEqual(rtx.Args, argv) {
		t.Errorf("NewContextFor args = %v, want %v", rtx.Args, argv)
	}

	// The program path round-trips WithArgs exactly, including tokens after "--".
	var got []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.Args }}
	p, _, errb := newTestProgram(h, argv)
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run() = %d (stderr: %s)", code, errb)
	}
	if !reflect.DeepEqual(got, argv) {
		t.Errorf("rtx.Args during Run = %v, want %v", got, argv)
	}
}

// ── outcome recording ───────────────────────────────────────────.

// TestContext_RecordError_api pins the bare recording API: append accumulates in
// order, nil is a no-op, and Errors returns a copy (not the live slice).
func TestContext_RecordError_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.copyErrors(); got != nil {
		t.Errorf("fresh Errors() = %v, want nil", got)
	}
	e1, e2 := errors.New("one"), errors.New("two")
	rtx.RecordError(e1)
	rtx.RecordError(nil) // no-op
	rtx.RecordError(e2)
	got := rtx.copyErrors()
	if len(got) != 2 || got[0] != e1 || got[1] != e2 {
		t.Fatalf("Errors() = %v, want [one two] in order with nil skipped", got)
	}
	got[0] = errors.New("mutated")
	if rtx.copyErrors()[0] != e1 {
		t.Error("Errors() returned the live slice; want a copy")
	}
	// Nil receiver is safe.
	var nilRtx *Context
	nilRtx.RecordError(e1)
	if nilRtx.copyErrors() != nil {
		t.Error("nil Context Errors() should be nil")
	}
}

// TestContext_RecordWarning_api mirrors the error API for the warning channel:
// order, nil no-op, copy-not-live, nil-receiver safe — and warnings are kept
// SEPARATE from errors.
func TestContext_RecordWarning_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.copyWarnings(); got != nil {
		t.Errorf("fresh Warnings() = %v, want nil", got)
	}
	w1, w2 := errors.New("warn one"), errors.New("warn two")
	rtx.RecordWarning(w1)
	rtx.RecordWarning(nil) // no-op
	rtx.RecordWarning(w2)
	got := rtx.copyWarnings()
	if len(got) != 2 || got[0] != w1 || got[1] != w2 {
		t.Fatalf("Warnings() = %v, want [warn one, warn two] with nil skipped", got)
	}
	got[0] = errors.New("mutated")
	if rtx.copyWarnings()[0] != w1 {
		t.Error("Warnings() returned the live slice; want a copy")
	}
	// Channels are independent: a warning is not an error.
	if rtx.copyErrors() != nil {
		t.Errorf("RecordWarning leaked into Errors() = %v", rtx.copyErrors())
	}
	var nilRtx *Context
	nilRtx.RecordWarning(w1)
	if nilRtx.copyWarnings() != nil {
		t.Error("nil Context Warnings() should be nil")
	}
}

// TestContext_RecordSuccess_api mirrors it for the success channel; an empty
// string is the no-op (success carries a message).
func TestContext_RecordSuccess_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.copySuccesses(); got != nil {
		t.Errorf("fresh Successes() = %v, want nil", got)
	}
	rtx.RecordSuccess("done a")
	rtx.RecordSuccess("") // no-op
	rtx.RecordSuccess("done b")
	got := rtx.copySuccesses()
	if len(got) != 2 || got[0] != "done a" || got[1] != "done b" {
		t.Fatalf("Successes() = %v, want [done a, done b] with empty skipped", got)
	}
	got[0] = "mutated"
	if rtx.copySuccesses()[0] != "done a" {
		t.Error("Successes() returned the live slice; want a copy")
	}
	if rtx.copyErrors() != nil || rtx.copyWarnings() != nil {
		t.Error("RecordSuccess leaked into Errors()/Warnings()")
	}
	var nilRtx *Context
	nilRtx.RecordSuccess("x")
	if nilRtx.copySuccesses() != nil {
		t.Error("nil Context Successes() should be nil")
	}
}

// TestContext_Panics_api pins the private fault channel: recordFault (the
// lifecycle's, not a handler's) accumulates; Panics returns a copy; nil is a
// no-op; and faults are independent of the error channel.
func TestContext_Panics_api(t *testing.T) {
	rtx := NewContextFor(testDef(), nil)
	if got := rtx.copyFaults(); got != nil {
		t.Errorf("fresh Panics() = %v, want nil", got)
	}
	p1, p2 := &PanicError{Value: "boom"}, &PanicError{Value: errors.New("kaboom")}
	rtx.recordFault(p1)
	rtx.recordFault(nil) // no-op
	rtx.recordFault(p2)
	got := rtx.copyFaults()
	if len(got) != 2 || got[0] != p1 || got[1] != p2 {
		t.Fatalf("Panics() = %v, want [p1 p2] with nil skipped", got)
	}
	got[0] = &PanicError{Value: "mutated"}
	if rtx.copyFaults()[0] != p1 {
		t.Error("Panics() returned the live slice; want a copy")
	}
	if rtx.copyErrors() != nil {
		t.Errorf("recordFault leaked into Errors() = %v", rtx.copyErrors())
	}
	var nilRtx *Context
	nilRtx.recordFault(p1)
	if nilRtx.copyFaults() != nil {
		t.Error("nil Context Panics() should be nil")
	}
}

// TestRun_recordedErrorsFireFunnel pins the firing contract and the locked edges, with
// a CUSTOM funnel: it fires iff ≥1 channel recorded something (edge 1); record-without-exit
// still fires it (edge 2); a custom funnel OWNS the exit code — the error floor is the
// DEFAULT funnel's, so a custom funnel that sets no code exits 0 (edge 3); and the
// SignalExit/Exit choice still governs teardown.
func TestRun_recordedErrorsFireFunnel(t *testing.T) {
	errA := UsageError(errors.New("bad flag")) // carries ErrUsage through the join
	errB := errors.New("also bad")

	exec := func(onRun func(rtx *Context)) (log []string, code int, funneled error, drained []error, fired bool) {
		h := &testHandlers{log: &log, onRun: onRun}
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithFunnel(func(_ context.Context, _ *Context, _, _ []string, _, errs []error, _ []*PanicError) {
			fired, funneled, drained = true, errors.Join(errs...), errs
		})
		code, _ = p.Run(p.args)
		return
	}

	t.Run("record + SignalExit: fires, teardown runs, join keeps tags", func(t *testing.T) {
		log, code, funneled, drained, fired := exec(func(rtx *Context) {
			rtx.RecordError(errA)
			rtx.RecordError(errB)
			rtx.SignalExit(1)
		})
		if !fired {
			t.Fatal("OnError did not fire on recorded errors")
		}
		if len(drained) != 2 {
			t.Errorf("rtx.copyErrors() drained %d, want 2", len(drained))
		}
		if !errors.Is(funneled, ErrUsage) {
			t.Error("joined err lost errA's ErrUsage tag")
		}
		if code != 1 {
			t.Errorf("code = %d, want %d", code, 1)
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

	t.Run("record without exit + custom funnel: fires, code stays 0 (custom funnel owns it — edges 2,3)", func(t *testing.T) {
		log, code, _, _, fired := exec(func(rtx *Context) {
			rtx.RecordError(errB) // no SignalExit/Exit; the custom OnError (in exec) sets no code either
		})
		if !fired {
			t.Fatal("OnError did not fire on record-without-exit (edge 2)")
		}
		if code != 0 {
			t.Errorf("code = %d, want 0 — the error floor is the DEFAULT OnError's; a custom funnel that omits SignalExit exits 0 (edge 3)", code)
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

// TestRun_defaultOnError_prints is the end-to-end golden: with no custom funnel,
// the default prints one program-name-prefixed line per recorded error to
// stderr and exits 1 (rotini holds no exit-code constants — any error is 1).
func TestRun_defaultOnError_prints(t *testing.T) {
	cases := []struct {
		name   string
		record []error
		want   int
		stderr string // exact stderr (the format golden): the default OnError labels each line "Error:"
	}{
		{"usage", []error{UsageError(errors.New("bad flag"))}, 1, "Error: bad flag\n"},
		{"internal", []error{InternalError(errors.New("broken wiring"))}, 1, "Error: broken wiring\n"},
		{"unclassified", []error{errors.New("mystery")}, 1, "Error: mystery\n"},
		{"multiple", []error{UsageError(errors.New("bad flag")), InternalError(errors.New("bug"))}, 1, "Error: bad flag\nError: bug\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs := tc.record
			h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
				for _, e := range recs {
					rtx.RecordError(e)
				}
			}}
			p, _, errb := newTestProgram(h, []string{"run"}) // no WithFunnel → default
			code, _ := p.Run(p.args)
			if code != tc.want {
				t.Errorf("code = %d, want %d", code, tc.want)
			}
			if got := errb.String(); got != tc.stderr {
				t.Errorf("stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}
