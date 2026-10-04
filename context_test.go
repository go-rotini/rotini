package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGet_typed(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.SetDependency(NewDependency[any]("buf"), any(buf))

	if got, ok := rtx.GetDependency(NewDependency[*bytes.Buffer]("buf")); !ok || got != buf {
		t.Fatalf("Get[*bytes.Buffer] = (%v, %v), want the bound buffer", got, ok)
	}
	if _, ok := rtx.GetDependency(NewDependency[*bytes.Buffer]("missing")); ok {
		t.Error("Get of an unbound key should be ok=false")
	}
	if _, ok := rtx.GetDependency(NewDependency[*int]("buf")); ok {
		t.Error("Get of a wrong-typed binding should be ok=false")
	}
}

func TestSetDependencyIfAbsent_keepsExistingElseSetsDefault(t *testing.T) {
	rtx := newContext()
	injected, def := &bytes.Buffer{}, &bytes.Buffer{}

	// A prior binding (e.g. a test's double) is kept — SetDependencyIfAbsent no-ops.
	rtx.SetDependency(NewDependency[any]("buf"), any(injected))
	rtx.SetDependencyIfAbsent(NewDependency[any]("buf"), any(def))
	if rtx.MustGetDependency(NewDependency[*bytes.Buffer]("buf")) != injected {
		t.Error("SetDependencyIfAbsent must not overwrite an existing value")
	}

	// An absent key gets the default registered into the registry.
	rtx.SetDependencyIfAbsent(NewDependency[any]("other"), any(def))
	if rtx.MustGetDependency(NewDependency[*bytes.Buffer]("other")) != def {
		t.Error("SetDependencyIfAbsent should register the default when none is registered")
	}
}

func TestMustGet_returnsBoundService(t *testing.T) {
	rtx := newContext()
	buf := &bytes.Buffer{}
	rtx.SetDependency(NewDependency[any]("buf"), any(buf))
	if rtx.MustGetDependency(NewDependency[*bytes.Buffer]("buf")) != buf {
		t.Fatal("MustGet did not return the bound service")
	}
}

func TestMustGetDependency_panicsDependencyError(t *testing.T) {
	rtx := newContext() // nothing bound

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok {
			t.Fatalf("MustGet panicked with %v (%T), want an error", r, r)
		}
		if !errors.Is(err, ErrDependencyNotFound) {
			t.Errorf("panic = %v, want it to wrap ErrDependencyNotFound", err)
		}
		var se *DependencyError
		if !errors.As(err, &se) || se.Name != "missing" {
			t.Errorf("panic did not carry the key: %v", err)
		}
	}()

	_ = rtx.MustGetDependency(NewDependency[*bytes.Buffer]("missing")) // panics → recovered above
}

func TestMustGet_panicsOnWrongType(t *testing.T) {
	rtx := newContext()
	rtx.SetDependency(NewDependency[any]("buf"), any(&bytes.Buffer{}))

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet of a wrong-typed binding should panic")
		}
	}()

	_ = rtx.MustGetDependency(NewDependency[*int]("buf")) // bound, but not an *int → panics
}

// TestContext_concurrentRegistry sets and reads dependencies from many goroutines; under
// -race it pins that every path is locked, and that every dependency survives.
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
				rtx.SetDependency(NewDependency[any](key), any(w))
				_ = rtx.dependency(key)
				_, _ = rtx.GetDependency(NewDependency[int](key))
			}
		}(w)
	}
	wg.Wait()

	for k := range keys {
		if rtx.dependency(fmt.Sprintf("svc-%d", k)) == nil {
			t.Errorf("key svc-%d unbound after concurrent access", k)
		}
	}
}

// TestContext_ArgsField pins that rtx.Argv is exactly the argument vector the invocation was
// given.
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

// TestContext_RecordError_api: errors accumulate in order, nil is ignored, and the snapshot is
// a copy.
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

// TestContext_RecordWarning_api: as for errors, and warnings stay separate from errors.
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

// TestContext_RecordSuccess_api: as for errors, with an empty string ignored.
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

// TestContext_Panics_api: faults accumulate in order, nil is ignored, the snapshot is a copy,
// and faults stay separate from errors.
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

// TestRun_recordedErrorsFireReporter pins, with a custom reporter: it fires iff a channel
// recorded something, including without a stop; a reporter that sets no code exits 0; and
// HaltWithCode versus Exit still governs teardown.
func TestRun_recordedErrorsFireReporter(t *testing.T) {
	errA := UsageError(errors.New("bad flag")) // carries ErrUsage through the join
	errB := errors.New("also bad")

	exec := func(onRun func(rtx *Context)) (log []string, code int, reported error, drained []error, fired bool) {
		h := &testHandlers{log: &log, onRun: onRun}
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithReporter(func(_ context.Context, _ *Context, out Outcome) {
			errs := out.Errors
			fired, reported, drained = true, errors.Join(errs...), errs
		})
		code, _ = p.Run(p.args)
		return
	}

	t.Run("record + HaltWithCode: fires, teardown runs, join keeps tags", func(t *testing.T) {
		log, code, reported, drained, fired := exec(func(rtx *Context) {
			rtx.RecordError(errA)
			rtx.RecordError(errB)
			rtx.HaltWithCode(1)
		})
		if !fired {
			t.Fatal("the reporter did not fire on recorded errors")
		}
		if len(drained) != 2 {
			t.Errorf("rtx.copyErrors() drained %d, want 2", len(drained))
		}
		if !errors.Is(reported, ErrUsage) {
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
			t.Fatal("the reporter did not fire")
		}
		if code != 5 {
			t.Errorf("code = %d, want 5", code)
		}
		if contains(log, "run.PostRun") {
			t.Errorf("teardown ran despite hard Exit: %v", log)
		}
	})

	t.Run("record without exit + custom reporter: fires, code stays 0 (custom reporter owns it — edges 2,3)", func(t *testing.T) {
		log, code, _, _, fired := exec(func(rtx *Context) {
			rtx.RecordError(errB) // no HaltWithCode/Exit; the custom reporter (in exec) sets no code either
		})
		if !fired {
			t.Fatal("the reporter did not fire on record-without-exit (edge 2)")
		}
		if code != 0 {
			t.Errorf("code = %d, want 0 — the error floor is the DEFAULT reporter's; a custom reporter that sets no code exits 0 (edge 3)", code)
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
			t.Error("the reporter fired with no recorded errors (edge 1 violated)")
		}
		if code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
}

// TestRun_defaultReporterPrintsErrors: the default reporter prints one "Error:" line per
// recorded error to stderr and exits 1 for any category.
func TestRun_defaultReporterPrintsErrors(t *testing.T) {
	cases := []struct {
		name   string
		record []error
		want   int
		stderr string // exact stderr (the format golden): the default reporter labels each line "Error:"
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
			p, _, errb := newTestProgram(h, []string{"run"}) // no WithReporter → default
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

// TestContext_Failed: only a recorded error or a fault counts as failure.
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

// TestContext_Halt pins that Halt stops forward progress without setting an exit code.
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
			t.Errorf("Halt claimed exit code %d; the verdict is the reporter's", rtx.exitCode)
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
			t.Errorf("Halt pre-empted the reporter with %d", rtx.exitCode)
		}
	})

	t.Run("no-op in the reporter", func(t *testing.T) {
		t.Parallel()
		rtx := &Context{reporterStage: true}
		rtx.Halt()
		if rtx.stopped {
			t.Error("Halt stopped a lifecycle that had already finished")
		}
		// A nil receiver panics; see TestContext_nilReceiverPanicsAtTheCall.
	})
}

// TestContext_seamAccessorsAreRaceFree pins, under -race, that seam accessors read under the
// lock their setters write under.
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

// TestContext_chainIsACopy pins that modifying the slice CommandChain returns does not affect
// the run.
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

	got := rtx.CommandChain()
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
	if len(rtx.CommandChain()) != 2 {
		t.Errorf("Chain() length = %d, want 2 — reslicing the copy must not shorten the run's own", len(rtx.CommandChain()))
	}
}

// Two CommandChain calls return independent slices.
func TestContext_chainCopiesAreIndependent(t *testing.T) {
	rtx := NewContextFor(Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}, []string{"run"})

	a, b := rtx.CommandChain(), rtx.CommandChain()
	a[0].Name = "changed"
	if b[0].Name == "changed" {
		t.Error("two Chain() results share backing storage")
	}
}

// TestContext_nilReceiverPanicsAtTheCall pins that Context methods panic on a nil receiver.
// Functions taking a Context as an argument check it instead; see
// TestContext_boundaryStillRejectsNil.
func TestContext_nilReceiverPanicsAtTheCall(t *testing.T) {
	for name, call := range map[string]func(*Context){
		"Chain":         func(rtx *Context) { _ = rtx.CommandChain() },
		"Command":       func(rtx *Context) { _ = rtx.Command() },
		"Path":          func(rtx *Context) { _ = rtx.CommandPath() },
		"Value":         func(rtx *Context) { _ = rtx.dependency("k") },
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

// TestContext_boundaryStillRejectsNil: functions that take a Context as an argument handle a
// nil one without panicking.
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

// TestContext_argsStayLiveButChainDoesNot: Argv is the live slice; CommandChain is a copy.
func TestContext_argsStayLiveButChainDoesNot(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "run", Handler: "AppRun"}}}
	rtx := NewContextFor(def, []string{"run", "x"})

	rtx.Argv[1] = "mutated"
	if rtx.Argv[1] != "mutated" {
		t.Error("Args is not the live slice; a handler running its own parser depends on it")
	}

	c := rtx.CommandChain()
	c[0].Name = "mutated"
	if rtx.CommandChain()[0].Name == "mutated" {
		t.Error("Chain handed back live storage")
	}
}

// ── Halt and HaltWith ───────────────────────────────────────────────────────.

// TestHalt_isLoadBearingInSetupAndInertElsewhere pins that Halt affects only CascadingPreRun and
// PreRun: the unwind ignores `stopped`, and Run is the last forward step.
func TestHalt_isLoadBearingInSetupAndInertElsewhere(t *testing.T) {
	full := []string{
		"app.CascadingPreRun", "run.CascadingPreRun", "run.PreRun", "run.Run",
		"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
	}

	for _, tc := range []struct {
		hook string
		want []string
	}{
		// Setup: the command does not proceed; begun steps still unwind.
		{"CascadingPreRun", []string{
			"app.CascadingPreRun", "run.CascadingPreRun",
			"run.CascadingPostRun", "app.CascadingPostRun",
		}},
		{"PreRun", []string{
			"app.CascadingPreRun", "run.CascadingPreRun", "run.PreRun",
			"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
		}},
		// Inert: there is no forward progress left to stop.
		{"Run", full},
		{"PostRun", full},
		{"CascadingPostRun", full},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			_, log := runActs(t, []string{"run", "x"}, map[string]act{
				"run": {at: tc.hook, do: func(rtx *Context) { rtx.Halt() }},
			})
			if fmt.Sprint(log) != fmt.Sprint(tc.want) {
				t.Errorf("Halt in %s:\n got %v\nwant %v", tc.hook, log, tc.want)
			}
		})
	}
}

// TestHaltWith_isExactlyRecordErrorPlusHalt is the equivalence contract. HaltWith is a
// convenience, not a new behaviour: if the two forms ever diverge, the convenience has become a
// second set of semantics to learn, which is the opposite of the point.
func TestHaltWith_isExactlyRecordErrorPlusHalt(t *testing.T) {
	boom := errors.New("setup failed")

	oneCall, oneLog := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.HaltWith(boom) }},
	})
	twoCalls, twoLog := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.RecordError(boom); rtx.Halt() }},
	})

	if oneCall != twoCalls {
		t.Errorf("exit code: HaltWith = %d, RecordError+Halt = %d", oneCall, twoCalls)
	}
	if fmt.Sprint(oneLog) != fmt.Sprint(twoLog) {
		t.Errorf("hooks run:\n HaltWith        %v\n RecordError+Halt %v", oneLog, twoLog)
	}
	if oneCall == 0 {
		t.Error("a recorded error exited 0")
	}
}

// TestHaltWith_stopsTheWorkTheHookRefused pins that HaltWith in a setup hook prevents Run. The
// exit code and stderr match either way, so the test asserts on which hooks ran.
func TestHaltWith_stopsTheWorkTheHookRefused(t *testing.T) {
	boom := errors.New("no connection was opened")

	_, halted := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.HaltWith(boom) }},
	})
	if contains(halted, "run.Run") {
		t.Errorf("Run executed after its setup hook failed: %v", halted)
	}

	// RecordError without a stop: same verdict, but Run still executes.
	_, leaked := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.RecordError(boom) }},
	})
	if !contains(leaked, "run.Run") {
		t.Fatal("fixture no longer demonstrates the hazard: Run was skipped without a halt")
	}
}

// TestHaltWith_isCorrectInEveryHook pins that HaltWith in any hook reaches the reporter and
// fails the run.
func TestHaltWith_isCorrectInEveryHook(t *testing.T) {
	for _, hook := range []string{"CascadingPreRun", "PreRun", "Run", "PostRun", "CascadingPostRun"} {
		t.Run(hook, func(t *testing.T) {
			log := []string{}
			p, _, errb := newTestProgram(&actProgram{log: &log, actions: map[string]act{
				"run": {at: hook, do: func(rtx *Context) { rtx.HaltWith(errors.New("failed in " + hook)) }},
			}}, []string{"run", "x"})

			code, _ := p.Run(p.args)
			if code == 0 {
				t.Errorf("HaltWith in %s exited 0", hook)
			}
			if got := errb.String(); !strings.Contains(got, "failed in "+hook) {
				t.Errorf("HaltWith in %s did not reach the reporter; stderr = %q", hook, got)
			}
		})
	}
}

// TestHaltWith_nilErrorStillHalts: a nil error records nothing but still halts.
func TestHaltWith_nilErrorStillHalts(t *testing.T) {
	code, log := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.HaltWith(nil) }},
	})
	if contains(log, "run.Run") {
		t.Errorf("HaltWith(nil) did not halt: %v", log)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0 — nothing was recorded, so nothing failed", code)
	}
}

// TestRecordError_aloneStillContinues pins that RecordError alone does not stop the run.
func TestRecordError_aloneStillContinues(t *testing.T) {
	var sawFailed bool
	code, log := runActs(t, []string{"run", "x"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) {
			rtx.RecordError(errors.New("first problem"))
			rtx.RecordError(errors.New("second problem"))
		}},
		"app": {at: "CascadingPreRun", do: func(rtx *Context) { sawFailed = rtx.Failed() }},
	})
	if !contains(log, "run.Run") {
		t.Errorf("RecordError alone stopped the run: %v", log)
	}
	if code == 0 {
		t.Error("two recorded errors exited 0")
	}
	if sawFailed {
		t.Error("Failed() was true before anything had been recorded")
	}
}

// TestHaltWith_inTheReporterDoesNotReenter: HaltWith inside the reporter returns without
// re-entering the run.
func TestHaltWith_inTheReporterDoesNotReenter(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("original"))
	}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})
	p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) {
		rtx.HaltWith(errors.New("while reporting"))
	})

	done := make(chan int, 1)
	go func() { code, _ := p.Run(p.args); done <- code }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HaltWith inside the reporter did not return")
	}
}

// ── Command from goroutines ─────────────────────────────────────────────────.
//
// A goroutine the hook waits for sees its spawner's command; one that outlives the hook sees
// the step running when it calls Command.

type fgHandlers struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
	cascading func(*Context)
}

func (h fgHandlers) Run(context.Context, *Context) {}
func (h fgHandlers) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.cascading != nil {
		h.cascading(rtx)
	}
}

type fgLeaf struct {
	NoHooks
	onRun func(*Context)
}

func (h fgLeaf) Run(_ context.Context, rtx *Context) {
	if h.onRun != nil {
		h.onRun(rtx)
	}
}

type fgProg struct {
	cascading func(*Context)
	onRun     func(*Context)
}

func (p fgProg) App() Handler    { return fgHandlers{cascading: p.cascading} }
func (p fgProg) AppRun() Handler { return fgLeaf{onRun: p.onRun} }

// TestFrame_goroutineTheHookWaitsForSeesTheSpawnersFrame: goroutines the hook waits for read
// the hook's own command.
func TestFrame_goroutineTheHookWaitsForSeesTheSpawnersFrame(t *testing.T) {
	var seen []string
	var mu sync.Mutex

	p := NewProgram(testDef(), fgProg{cascading: func(rtx *Context) {
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, rtx.Command().Name)
			}()
		}
		wg.Wait() // the hook does not return until they are done
	}}).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d readings, want 4", len(seen))
	}
	for _, got := range seen {
		if got != "app" {
			t.Errorf("a waited-for goroutine read Command() = %q, want its spawner's %q", got, "app")
		}
	}
}

// TestFrame_goroutineThatOutlivesItsHookSeesTheCurrentStep: a detached goroutine reads the
// current step, while a value captured before spawning stays stable.
func TestFrame_goroutineThatOutlivesItsHookSeesTheCurrentStep(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	var detached, captured string
	var capturedFrame Command

	p := NewProgram(testDef(), fgProg{
		cascading: func(rtx *Context) {
			capturedFrame = rtx.Command() // captured while the hook is running
			go func() {
				defer close(done)
				<-release // resume only after the spawning hook has returned
				detached = rtx.Command().Name
				captured = capturedFrame.Name
			}()
		},
		onRun: func(rtx *Context) {
			close(release)
			<-done
		},
	}).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}

	// The run has moved on to the leaf by the time the detached goroutine looks.
	if detached != "run" {
		t.Errorf("a detached goroutine read Command() = %q; the documented behaviour is the current step, %q", detached, "run")
	}
	// The captured value is stable.
	if captured != "app" {
		t.Errorf("the captured frame changed under the caller: %q, want %q", captured, "app")
	}
}

// TestCommand_reportsTheHooksOwnCommand: in a cascading hook, Command is the command the hook
// belongs to, and the invoked command is the last entry of CommandChain.
func TestCommand_reportsTheHooksOwnCommand(t *testing.T) {
	var rootSaw, midSaw, invokedSaw string
	runF(t, []string{"mid", "leaf"},
		func(rtx *Context) {
			midSaw = rtx.Command().Name
			chain := rtx.CommandChain()
			invokedSaw = chain[len(chain)-1].Name
		},
		func(rtx *Context) { rootSaw = rtx.Command().Name },
	)
	if rootSaw != "root" || midSaw != "mid" {
		t.Errorf("Command() = root:%q mid:%q, want root/mid", rootSaw, midSaw)
	}
	if invokedSaw != "leaf" {
		t.Errorf("the chain's last command in mid's cascading hook = %q, want the leaf %q", invokedSaw, "leaf")
	}
}

// TestCommand_outsideAHookIsTheInvokedCommand covers a Context from NewContextFor, where no hook
// is running.
func TestCommand_outsideAHookIsTheInvokedCommand(t *testing.T) {
	rtx := NewContextFor(fDef(), []string{"mid", "leaf"})
	if got := rtx.Command().Name; got != "leaf" {
		t.Errorf("Command() outside a hook = %q, want the leaf %q", got, "leaf")
	}
	if !rtx.Command().Invoked {
		t.Error("Command() outside a hook is the invoked command, but Invoked = false")
	}
	if _, err := rtx.Inputs[fLeafSpan](); err != nil {
		t.Errorf("the leaf's own type must collect from a hookless Context: %v", err)
	}
}

// TestInvoked pins that a cascading hook's Command().Invoked is true only for the invoked
// command.
func TestInvoked(t *testing.T) {
	for _, tc := range []struct {
		argv     []string
		wantMid  bool
		wantRoot bool
	}{
		{[]string{"mid"}, true, false},          // mid IS the invocation
		{[]string{"mid", "leaf"}, false, false}, // both are ancestors of `leaf`
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			var midSaw, rootSaw bool
			runF(t, tc.argv,
				func(rtx *Context) { midSaw = rtx.Command().Invoked },
				func(rtx *Context) { rootSaw = rtx.Command().Invoked },
			)
			if midSaw != tc.wantMid {
				t.Errorf("mid's cascading hook: Invoked = %v, want %v", midSaw, tc.wantMid)
			}
			if rootSaw != tc.wantRoot {
				t.Errorf("root's cascading hook: Invoked = %v, want %v", rootSaw, tc.wantRoot)
			}
		})
	}
}

// TestInvoked_isTrueInANonCascadingHook: PreRun, Run and PostRun only ever run for the invoked
// command, so the answer there is not interesting — but it must not be wrong.
func TestInvoked_isTrueInANonCascadingHook(t *testing.T) {
	var inRun bool
	p := NewProgram(fDef(), fLeafProbe{capture: func(rtx *Context) { inRun = rtx.Command().Invoked }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if !inRun {
		t.Error("the leaf's Run reported Invoked = false")
	}
}

// TestInvoked_isExactlyTheLastCommand: one entry of the chain is the invoked command, and it is
// the last — including when a name repeats at two depths.
func TestInvoked_isExactlyTheLastCommand(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "app", Handler: "AppApp",
			Commands: []CommandDef{{Name: "app", Handler: "AppAppApp"}},
		}},
	}
	chain := NewContextFor(def, []string{"app", "app"}).CommandChain()
	if len(chain) != 3 {
		t.Fatalf("chain = %d commands, want 3", len(chain))
	}
	for i, c := range chain {
		if want := i == len(chain)-1; c.Invoked != want {
			t.Errorf("chain[%d].Invoked = %v, want %v", i, c.Invoked, want)
		}
	}
}

// TestCommandPathAndHelp_followTheRunningCommand: in a cascading hook, CommandPath and Help
// describe the hook's own command.
func TestCommandPathAndHelp_followTheRunningCommand(t *testing.T) {
	var midPath, rootPath, midHelp, rootHelp string
	h := fProg{
		inMid:  func(rtx *Context) { midPath, midHelp = rtx.CommandPath(), rtx.Help() },
		inRoot: func(rtx *Context) { rootPath, rootHelp = rtx.CommandPath(), rtx.Help() },
	}
	help := func(path ...string) (string, error) { return "page:" + strings.Join(path, "/"), nil }
	p := NewProgram(fDef(), h).WithHelp(help).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if rootPath != "root" || midPath != "root mid" {
		t.Errorf("CommandPath() = root:%q mid:%q, want %q and %q", rootPath, midPath, "root", "root mid")
	}
	if rootHelp != "page:" || midHelp != "page:mid" {
		t.Errorf("Help() = root:%q mid:%q, want %q and %q", rootHelp, midHelp, "page:", "page:mid")
	}
}

// Command and CommandPath report the running command in Run.
func TestContext_commandAndPath(t *testing.T) {
	var cmd Command
	var path string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		cmd, path = rtx.Command(), rtx.CommandPath()
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"run", "x"})

	if cmd.Name != "run" {
		t.Errorf("Command().Name = %q, want run", cmd.Name)
	}
	if path != "app run" {
		t.Errorf("Path() = %q, want %q", path, "app run")
	}
}

// An alias reports the canonical name.
func TestContext_pathIsCanonicalNotTyped(t *testing.T) {
	var path string
	var matched string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		path = rtx.CommandPath()
		matched = rtx.Command().Matched
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Run([]string{"r", "x"}) // "r" is an alias of "run"

	if path != "app run" {
		t.Errorf("Path() via alias = %q, want the canonical %q", path, "app run")
	}
	if matched != "r" {
		t.Errorf("Matched = %q, want the typed token %q", matched, "r")
	}
}

// A bare root invocation has a command and a path.
func TestContext_commandAtRoot(t *testing.T) {
	var cmd Command
	var path string

	p := NewProgram(
		Definition{Name: "app", Handler: "Main"},
		rootOnlyHandlers{capture: func(rtx *Context) { cmd, path = rtx.Command(), rtx.CommandPath() }},
	).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run(nil); err != nil {
		t.Fatal(err)
	}
	if cmd.Name != "app" || path != "app" {
		t.Errorf("root Command/Path = %q/%q, want app/app", cmd.Name, path)
	}
}

// rootOnlyHandlers is a one-command handler set, for asserting on the root frame.
type rootOnlyHandlers struct{ capture func(*Context) }

func (h rootOnlyHandlers) Main() Handler { return rootOnlyRun(h) }

type rootOnlyRun rootOnlyHandlers

func (rootOnlyRun) CascadingPreRun(context.Context, *Context)  {}
func (rootOnlyRun) PreRun(context.Context, *Context)           {}
func (rootOnlyRun) PostRun(context.Context, *Context)          {}
func (rootOnlyRun) CascadingPostRun(context.Context, *Context) {}
func (h rootOnlyRun) Run(_ context.Context, rtx *Context)      { h.capture(rtx) }
