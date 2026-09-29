package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestGet_typed(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)

	if got, ok := rtx.Get[*bytes.Buffer]("buf"); !ok || got != buf {
		t.Fatalf("Get[*bytes.Buffer] = (%v, %v), want the bound buffer", got, ok)
	}
	if _, ok := rtx.Get[*bytes.Buffer]("missing"); ok {
		t.Error("Get of an unbound key should be ok=false")
	}
	if _, ok := rtx.Get[*int]("buf"); ok {
		t.Error("Get of a wrong-typed binding should be ok=false")
	}
}

func TestBindIfAbsent_keepsExistingElseRegistersDefault(t *testing.T) {
	rtx := newContext()
	injected, def := &bytes.Buffer{}, &bytes.Buffer{}

	// A prior binding (e.g. a test's double) is kept — BindIfAbsent no-ops.
	rtx.Bind("buf", injected)
	rtx.BindIfAbsent("buf", def)
	if rtx.MustGet[*bytes.Buffer]("buf") != injected {
		t.Error("BindIfAbsent must not overwrite an existing binding")
	}

	// An absent key gets the default registered into the registry.
	rtx.BindIfAbsent("other", def)
	if rtx.MustGet[*bytes.Buffer]("other") != def {
		t.Error("BindIfAbsent should register the default when the key is absent")
	}
}

func TestMustGet_returnsBoundService(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.Bind("buf", buf)
	if rtx.MustGet[*bytes.Buffer]("buf") != buf {
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

	_ = rtx.MustGet[*bytes.Buffer]("missing") // panics → recovered above
}

func TestMustGet_panicsOnWrongType(t *testing.T) {
	rtx := newContext()
	rtx.Bind("buf", &bytes.Buffer{})

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet of a wrong-typed binding should panic")
		}
	}()

	_ = rtx.MustGet[*int]("buf") // bound, but not an *int → panics
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
	for w := range workers {
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("svc-%d", w%keys)
			for range iters {
				rtx.Bind(key, w)
				_ = rtx.Value(key)
				_, _ = rtx.Get[int](key)
			}
		}(w)
	}
	wg.Wait()

	for k := range keys {
		if rtx.Value(fmt.Sprintf("svc-%d", k)) == nil {
			t.Errorf("key svc-%d unbound after concurrent access", k)
		}
	}
}

// TestContext_ArgsField locks the W1 contract: rtx.Argv is the exact argument
// vector the invocation was given — os.Args[1:] or the Program.WithArgs
// override — exposed as a plain field (the live slice, not a copy).
func TestContext_ArgsField(t *testing.T) {
	argv := []string{"--verbose", "run", "alice", "--", "-not-a-flag"}

	// The standalone constructor round-trips argv exactly.
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, argv)
	if !reflect.DeepEqual(rtx.Argv, argv) {
		t.Errorf("NewContextFor args = %v, want %v", rtx.Argv, argv)
	}

	// The program path round-trips WithArgs exactly, including tokens after "--".
	var got []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.Argv }}
	p, _, errb := newTestProgram(h, argv)
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run() = %d (stderr: %s)", code, errb)
	}
	if !reflect.DeepEqual(got, argv) {
		t.Errorf("rtx.Argv during Run = %v, want %v", got, argv)
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
// HaltWithCode/Exit choice still governs teardown.
func TestRun_recordedErrorsFireFunnel(t *testing.T) {
	errA := UsageError(errors.New("bad flag")) // carries ErrUsage through the join
	errB := errors.New("also bad")

	exec := func(onRun func(rtx *Context)) (log []string, code int, funneled error, drained []error, fired bool) {
		h := &testHandlers{log: &log, onRun: onRun}
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) {
			errs := out.Errors
			fired, funneled, drained = true, errors.Join(errs...), errs
		})
		code, _ = p.Run(p.args)
		return
	}

	t.Run("record + HaltWithCode: fires, teardown runs, join keeps tags", func(t *testing.T) {
		log, code, funneled, drained, fired := exec(func(rtx *Context) {
			rtx.RecordError(errA)
			rtx.RecordError(errB)
			rtx.HaltWithCode(1)
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
			t.Errorf("teardown did not run on graceful HaltWithCode: %v", log)
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
			rtx.RecordError(errB) // no HaltWithCode/Exit; the custom OnError (in exec) sets no code either
		})
		if !fired {
			t.Fatal("OnError did not fire on record-without-exit (edge 2)")
		}
		if code != 0 {
			t.Errorf("code = %d, want 0 — the error floor is the DEFAULT OnError's; a custom funnel that omits HaltWithCode exits 0 (edge 3)", code)
		}
		if !contains(log, "run.PostRun") {
			t.Errorf("teardown should run when no exit was called: %v", log)
		}
	})

	t.Run("no errors recorded: does NOT fire (edge 1)", func(t *testing.T) {
		_, code, _, _, fired := exec(func(rtx *Context) {
			rtx.HaltWithCode(0) // clean success
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

// TestContext_Failed covers the one bit of outcome state a teardown hook can read.
//
// A PostRun that owns a transaction has to choose commit or rollback, and before Failed there
// was no way to ask: Context had five Record* methods and no reader, while the Outcome that
// answers the question reaches the funnel only after every teardown has already run. A program
// had to keep its own parallel "did we fail" flag, which a panic would not set.
func TestContext_Failed(t *testing.T) {
	t.Parallel()
	t.Run("a clean run has not failed", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.RecordInfo("working")
		rtx.RecordSuccess("done")
		rtx.RecordWarning(errors.New("a warning is not a failure"))
		if rtx.Failed() {
			t.Error("Failed() = true after only infos, successes and warnings")
		}
	})
	t.Run("a recorded error is a failure", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.RecordError(errors.New("boom"))
		if !rtx.Failed() {
			t.Error("Failed() = false after RecordError")
		}
	})
	t.Run("a fault is a failure, with no error recorded", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.recordFault(&PanicError{Value: "boom"})
		if !rtx.Failed() {
			t.Error("Failed() = false after a panic — the case a rollback most needs")
		}
	})
}

// TestContext_Halt covers stopping the lifecycle without claiming an exit code.
//
// Before Halt, HaltWithCode was the only way to stop, and it does two jobs at once. A program
// whose funnel owns exit codes therefore wrote a meaningless number purely to halt — and
// because the number then looked redundant, deleting it read as tidying while silently
// removing the halt. That is how one bad flag came to be reported three times in
// example-lifecycle, with a fourth misleading error from a hook whose setup had been skipped.
func TestContext_Halt(t *testing.T) {
	t.Parallel()

	t.Run("stops without claiming a code", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.Halt()
		if !rtx.stopped {
			t.Error("Halt did not stop forward progress")
		}
		if rtx.exitCode != 0 {
			t.Errorf("Halt claimed exit code %d; the verdict is the funnel's", rtx.exitCode)
		}
		if rtx.exitNow {
			t.Error("Halt skipped teardown; that is Exit's job")
		}
	})

	t.Run("does not displace a code already signalled", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.HaltWithCode(3)
		rtx.Halt()
		if rtx.exitCode != 3 {
			t.Errorf("exitCode = %d, want 3 — Halt must not clear a deliberate code", rtx.exitCode)
		}
	})

	t.Run("a later HaltWithCode still sets the code", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.Halt()
		rtx.HaltWithCode(2)
		if rtx.exitCode != 2 {
			t.Errorf("exitCode = %d, want 2", rtx.exitCode)
		}
	})

	t.Run("a recorded error still decides the verdict", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{}
		rtx.RecordError(errors.New("boom"))
		rtx.Halt()
		if !rtx.Failed() {
			t.Error("Halt discarded the recorded failure")
		}
		if rtx.exitCode != 0 {
			t.Errorf("Halt pre-empted the funnel with %d", rtx.exitCode)
		}
	})

	t.Run("no-op in the funnel", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{funnelStage: true}
		rtx.Halt()
		if rtx.stopped {
			t.Error("Halt stopped a lifecycle that had already finished")
		}
		// A nil receiver is NOT part of this contract — it panics, like every other
		// method. See TestContext_nilReceiverPanicsAtTheCall.
	})
}

// TestContext_seamAccessorsAreRaceFree pins that every seam accessor reads under the same lock
// its setter writes under.
//
// Version() did not: it read rtx.version unlocked while WithVersion wrote it under the write
// lock, which -race reported the moment a handler goroutine touched both. The Context is
// explicitly designed for concurrent access from goroutines a handler spawns — that is why
// every Record* method is mutex-guarded — so an unsynchronized accessor is a defect, not a
// theoretical one.
func TestContext_seamAccessorsAreRaceFree(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rtx.WithVersion("1.2.3").
				WithParser(NewParser())
		}()
		go func() {
			defer wg.Done()
			_ = rtx.Version()
			_ = rtx.Parser()
		}()
	}
	wg.Wait()
}

// TestContext_chainIsACopy pins S2: the chain a handler receives cannot reach the run.
//
// Chain() used to return the live slice with a doc asking callers to "treat the slice as
// read-only" — a request, not a guarantee. One assignment through it rewrote the invocation:
//
//	Path() before="app run"   after mutating what Chain() returned="app HIJACKED"
//
// and with it Command(), the binder's leaf anchoring and configuration-file scoping, silently,
// for the rest of the run. Every other internal slice the Context hands out is copied; this
// was the one that was not.
func TestContext_chainIsACopy(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}
	rtx := NewContextFor(def, []string{"run"})

	want := rtx.CommandPath()
	if want != "app run" {
		t.Fatalf("Path() = %q, want %q — the fixture is wrong", want, "app run")
	}

	// Every mutation a handler could plausibly make to the slice it was handed.
	got := rtx.Chain()
	got[1].Name = "HIJACKED"
	got[0], got[1] = got[1], got[0]
	got = got[:1]
	_ = got

	if rtx.CommandPath() != want {
		t.Errorf("Path() = %q after mutating the returned slice, want %q", rtx.CommandPath(), want)
	}
	if rtx.Command().Name != "run" {
		t.Errorf("Command().Name = %q, want run", rtx.Command().Name)
	}
	if len(rtx.Chain()) != 2 {
		t.Errorf("Chain() length = %d, want 2 — reslicing the copy must not shorten the run's own", len(rtx.Chain()))
	}
}

// Two calls hand back independent slices, so one handler's edits cannot reach another's.
func TestContext_chainCopiesAreIndependent(t *testing.T) {
	rtx := NewContextFor(Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}, []string{"run"})

	a, b := rtx.Chain(), rtx.Chain()
	a[0].Name = "changed"
	if b[0].Name == "changed" {
		t.Error("two Chain() results share backing storage")
	}
}

// TestContext_nilReceiverPanicsAtTheCall pins S3: every exported method dereferences, the
// same rule Program follows.
//
// Half of them used to tolerate a nil receiver and half did not, so rtx.Chain() answered while
// rtx.CommandPath() panicked — three views of the same data, two behaviours. Tolerating it is the
// worse of the two: a nil that reports "nothing recorded" or "no chain" hides the mistake and
// surfaces it later, at a call that was not wrong.
//
// rotini's own entry points that ACCEPT a Context from a caller still check it; the guard
// belongs at that boundary, not on every method behind it — see
// TestContext_boundaryStillRejectsNil.
func TestContext_nilReceiverPanicsAtTheCall(t *testing.T) {
	for name, call := range map[string]func(*Context){
		"Chain":         func(rtx *Context) { _ = rtx.Chain() },
		"Command":       func(rtx *Context) { _ = rtx.Command() },
		"Path":          func(rtx *Context) { _ = rtx.CommandPath() },
		"Value":         func(rtx *Context) { _ = rtx.Value("k") },
		"Failed":        func(rtx *Context) { _ = rtx.Failed() },
		"Halt":          func(rtx *Context) { rtx.Halt() },
		"HaltWithCode":  func(rtx *Context) { rtx.HaltWithCode(1) },
		"Exit":          func(rtx *Context) { rtx.Exit(1) },
		"RecordInfo":    func(rtx *Context) { rtx.RecordInfo("x") },
		"RecordError":   func(rtx *Context) { rtx.RecordError(errors.New("x")) },
		"RecordWarning": func(rtx *Context) { rtx.RecordWarning(errors.New("x")) },
		"RecordSuccess": func(rtx *Context) { rtx.RecordSuccess("x") },
		"Version":       func(rtx *Context) { _ = rtx.Version() },
		"Parser":        func(rtx *Context) { _ = rtx.Parser() },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic: the nil receiver was tolerated, hiding the caller's mistake")
				}
			}()
			call(nil)
		})
	}
}

// TestContext_boundaryStillRejectsNil is the other half of the contract: a function that takes
// a Context from a caller reports a nil one rather than panicking, because there the nil is an
// argument to validate rather than a receiver that should never have been nil.
func TestContext_boundaryStillRejectsNil(t *testing.T) {
	if got := Deprecations(nil); got != nil {
		t.Errorf("Deprecations(nil) = %v, want nil", got)
	}

	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
		}
	}
	if err := NewParser().Parse(nil, &in); err == nil {
		t.Error("Parser.Parse(nil, …) returned no error")
	} else if !strings.Contains(err.Error(), "nil context") {
		t.Errorf("Parse(nil) error = %q, want it to name the nil context", err)
	}
}

// TestContext_argsStayLiveButChainDoesNot pins the two halves of the slice rule in one place,
// because they are easy to conflate: Args is deliberately the live argv, Chain deliberately is
// not.
func TestContext_argsStayLiveButChainDoesNot(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "run", Handler: "AppRun"}}}
	rtx := NewContextFor(def, []string{"run", "x"})

	rtx.Argv[1] = "mutated"
	if rtx.Argv[1] != "mutated" {
		t.Error("Args is not the live slice; a handler running its own parser depends on it")
	}

	c := rtx.Chain()
	c[0].Name = "mutated"
	if rtx.Chain()[0].Name == "mutated" {
		t.Error("Chain handed back live storage")
	}
}
