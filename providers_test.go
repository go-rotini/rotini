package rotini

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// provClient is a provided value whose Close, used as its cleanup, logs its name.
type provClient struct {
	name     string
	closeLog *[]string
	closeErr error
}

func (c *provClient) Close() error {
	*c.closeLog = append(*c.closeLog, c.name)
	return c.closeErr
}

// Nothing is built until a hook asks, then the provider runs once for the run, and each run
// gets its own value.
func TestProvider_lazyOncePerRun(t *testing.T) {
	dep := NewDependency[*strings.Builder]("prov.buf")
	var calls atomic.Int32
	var log []string
	var seen []*strings.Builder
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, _ *Context) {
			log = append(log, "pre:"+strconv.Itoa(int(calls.Load())))
		},
		"c1.Run": func(_ context.Context, rtx *Context) {
			a := rtx.MustDependency(dep)
			b, err := rtx.Dependency(dep)
			c, ok := rtx.LookupDependency(dep)
			if err != nil || !ok || a != b || b != c {
				t.Errorf("the three accessors disagree: %p %p %p, %v, %v", a, b, c, err, ok)
			}
			seen = append(seen, a)
		},
	})
	p.WithProvider(dep, func(context.Context, *Context) (*strings.Builder, func() error, error) {
		calls.Add(1)
		return &strings.Builder{}, nil, nil
	})
	for range 2 {
		if code, err := p.Run(argv); code != 0 || err != nil {
			t.Fatalf("Run = %d, %v", code, err)
		}
	}
	if calls.Load() != 2 || !slices.Equal(log, []string{"pre:0", "pre:1"}) {
		t.Errorf("provider ran %d times, before-hook log %q; want once per run, after setup", calls.Load(), log)
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Error("two runs shared one provided value")
	}
}

// Concurrent requests in one run share one provider call.
func TestProvider_concurrentRequestsShareOneCall(t *testing.T) {
	dep := NewDependency[*int]("prov.n")
	var calls atomic.Int32
	onRun := func(rtx *Context) {
		var wg sync.WaitGroup
		got := make([]*int, 32)
		for i := range got {
			wg.Go(func() { got[i] = rtx.MustDependency(dep) })
		}
		wg.Wait()
		for _, v := range got {
			if v != got[0] {
				t.Error("concurrent requests got different values")
				return
			}
		}
	}
	// Each run gets fresh handlers, so the runs share nothing but the program.
	p := NewProgramFunc(testDef(), func(name string) (Handler, bool) {
		h := &recHandler{name: name, log: new([]string)}
		if name == "AppRun" {
			h.onRun = onRun
		}
		return h, true
	}).WithStdout(io.Discard).WithStderr(io.Discard)
	p.WithProvider(dep, func(context.Context, *Context) (*int, func() error, error) {
		calls.Add(1)
		return new(int), nil, nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
				t.Errorf("Run = %d, %v", code, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 4 {
		t.Errorf("provider ran %d times for 4 runs, want 4", calls.Load())
	}
}

// A value set with WithDependency or SetDependency wins, and the provider never runs.
func TestProvider_setValueWins(t *testing.T) {
	dep := NewDependency[string]("prov.s")
	called := false
	provide := func(context.Context, *Context) (string, func() error, error) {
		called = true
		return "provided", nil, nil
	}

	var got string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.MustDependency(dep) }}
	p, _, _ := newTestProgram(h, nil)
	p.WithDependency(dep, "double").WithProvider(dep, provide)
	p.Run([]string{"run", "x"})
	if got != "double" || called {
		t.Errorf("got %q (provider called %v), want the program-wide double", got, called)
	}

	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.SetDependency(dep, "run") },
		"c1.Run":             func(_ context.Context, rtx *Context) { got, _ = rtx.Dependency(dep) },
	})
	p.WithProvider(dep, provide)
	p.Run(argv)
	if got != "run" || called {
		t.Errorf("got %q (provider called %v), want the run's own value", got, called)
	}

	// A nil provider removes the registration.
	p, argv, _, _ = comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { _, ok := rtx.LookupDependency(dep); called = ok },
	})
	p.WithProvider(dep, provide).WithProvider(dep, nil)
	p.Run(argv)
	if called {
		t.Error("a removed provider still provided")
	}
}

// Cleanups run after every hook and after the reporter, in reverse order of construction; set
// values, values the run never built, and values provided with a nil cleanup are left alone,
// even when they have a Close method.
func TestProvider_cleanedUpAfterTeardownInReverse(t *testing.T) {
	var log []string
	a, b, c := NewDependency[*provClient]("a"), NewDependency[*provClient]("b"), NewDependency[*provClient]("unused")
	set, shared := NewDependency[*provClient]("set"), NewDependency[*provClient]("shared")
	mk := func(name string) func(context.Context, *Context) (*provClient, func() error, error) {
		return func(context.Context, *Context) (*provClient, func() error, error) {
			pc := &provClient{name: name, closeLog: &log}
			return pc, pc.Close, nil
		}
	}
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c1.Run": func(_ context.Context, rtx *Context) {
			rtx.MustDependency(a)
			rtx.MustDependency(b)
			rtx.MustDependency(set)
			rtx.MustDependency(shared)
			rtx.RecordSuccess("done") // so the reporter runs
		},
		"c0.CascadingPostRun": func(_ context.Context, rtx *Context) {
			rtx.MustDependency(a) // still open in the last teardown hook
			log = append(log, "teardown")
		},
	})
	p.WithProvider(a, mk("a")).WithProvider(b, mk("b")).WithProvider(c, mk("unused"))
	p.WithDependency(set, &provClient{name: "set", closeLog: &log})
	sharedClient := &provClient{name: "shared", closeLog: &log}
	p.WithProvider(shared, func(context.Context, *Context) (*provClient, func() error, error) {
		return sharedClient, nil, nil
	})
	p.WithReporter(func(context.Context, *Context, Outcome) { log = append(log, "reporter") })
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if want := []string{"teardown", "reporter", "b", "a"}; !slices.Equal(log, want) {
		t.Errorf("order = %q, want %q", log, want)
	}
}

// The reporter can use what the run's providers built: it gets the value the hooks used, still
// open, and a dependency first asked for in the reporter is built and cleaned up too.
func TestProvider_reporterUsesProvided(t *testing.T) {
	var log []string
	used, late := NewDependency[*provClient]("used"), NewDependency[*provClient]("late")
	var hookValue, reporterValue *provClient
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			hookValue = rtx.MustDependency(used)
			rtx.RecordSuccess("done")
		},
	})
	mk := func(name string) func(context.Context, *Context) (*provClient, func() error, error) {
		return func(context.Context, *Context) (*provClient, func() error, error) {
			pc := &provClient{name: name, closeLog: &log}
			return pc, pc.Close, nil
		}
	}
	p.WithProvider(used, mk("used")).WithProvider(late, mk("late"))
	p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) {
		reporterValue = rtx.MustDependency(used)
		rtx.MustDependency(late)
		log = append(log, "reporter")
	})
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if reporterValue != hookValue {
		t.Error("the reporter got a different value from the one the hook used")
	}
	if want := []string{"reporter", "late", "used"}; !slices.Equal(log, want) {
		t.Errorf("order = %q, want %q", log, want)
	}
}

// A cleanup error after the reporter has reported can't reach it: it joins the error Run
// returns, and a run that would exit 0 exits 1 instead. A non-zero code stays.
func TestProvider_cleanupErrorAfterReporter(t *testing.T) {
	errClose := errors.New("flush failed")
	for _, tc := range []struct {
		name     string
		halt     int
		wantCode int
	}{{"a clean run exits 1", 0, 1}, {"a set code stays", 3, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			var log []string
			dep := NewDependency[*provClient]("db")
			p, argv, _, errs := comboTree(1, map[string]comboHook{
				"c0.Run": func(_ context.Context, rtx *Context) {
					rtx.MustDependency(dep)
					rtx.RecordSuccess("done")
					if tc.halt != 0 {
						rtx.HaltWithCode(tc.halt)
					}
				},
			})
			p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
				pc := &provClient{name: "db", closeLog: &log, closeErr: errClose}
				return pc, pc.Close, nil
			})
			var reported Outcome
			p.WithReporter(func(ctx context.Context, rtx *Context, out Outcome) {
				reported = out
				DefaultReporter(ctx, rtx, out)
			})
			code, err := p.Run(argv)
			if code != tc.wantCode || !errors.Is(err, errClose) {
				t.Errorf("Run = %d, %v; want %d and the cleanup error", code, err, tc.wantCode)
			}
			if len(reported.Errors) != 0 || strings.Contains(errs.String(), "Error:") {
				t.Errorf("reported %v, stderr %q; want the cleanup error unreported", reported.Errors, errs.String())
			}
		})
	}
}

// A run that records nothing skips the reporter, so its cleanups run first and a cleanup error
// is reported; a dependency the reporter then asks for is built again, open, and cleaned up.
func TestProvider_cleanupErrorOnQuietRunReported(t *testing.T) {
	var log []string
	errClose := errors.New("flush failed")
	dep := NewDependency[*provClient]("db")
	var values []*provClient
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { values = append(values, rtx.MustDependency(dep)) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
		pc := &provClient{name: "db" + strconv.Itoa(len(values)), closeLog: &log}
		if len(values) == 0 {
			pc.closeErr = errClose
		}
		return pc, pc.Close, nil
	})
	var reported Outcome
	var reporterClosed int
	p.WithReporter(func(_ context.Context, rtx *Context, out Outcome) {
		reported = out
		values = append(values, rtx.MustDependency(dep))
		reporterClosed = len(log)
	})
	code, err := p.Run(argv)
	if code != 0 || !errors.Is(err, errClose) || len(reported.Errors) != 1 {
		t.Fatalf("Run = %d, %v, reported %v; want the first cleanup error reported (exit left to the reporter)", code, err, reported.Errors)
	}
	if len(values) != 2 || values[0] == values[1] || reporterClosed != 1 || !slices.Equal(log, []string{"db0", "db1"}) {
		t.Errorf("values %v, closed before the reporter's %d, log %q; want a fresh value for the reporter, cleaned up after it", values, reporterClosed, log)
	}
}

// A panic re-raised by WithPanicRecover(false), after teardown or unguarded, still cleans up
// what the run's providers built.
func TestProvider_cleanedUpWhenPanicReraised(t *testing.T) {
	for _, teardown := range []bool{true, false} {
		t.Run("teardown="+strconv.FormatBool(teardown), func(t *testing.T) {
			var log []string
			dep := NewDependency[*provClient]("db")
			p, argv, _, _ := comboTree(1, map[string]comboHook{
				"c0.Run": func(_ context.Context, rtx *Context) {
					rtx.MustDependency(dep)
					panic("boom")
				},
			})
			p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
				pc := &provClient{name: "db", closeLog: &log}
				return pc, pc.Close, nil
			})
			p.WithPanicRecover(false).WithTeardownOnPanic(teardown)
			func() {
				defer func() {
					if r := recover(); r != "boom" {
						t.Errorf("recovered %v, want the hook's panic", r)
					}
				}()
				p.Run(argv)
			}()
			if !slices.Equal(log, []string{"db"}) {
				t.Errorf("cleanups = %q, want the provided value cleaned up", log)
			}
		})
	}
}

// sharedDB is a value opened once, outside any run, that breaks once closed.
type sharedDB struct{ closed atomic.Bool }

func (db *sharedDB) Close() error { db.closed.Store(true); return nil }

func (db *sharedDB) Query() error {
	if db.closed.Load() {
		return errors.New("the database is closed")
	}
	return nil
}

// A provider that hands out a shared value with a nil cleanup leaves it open, so a second run
// of the same program can still use it.
func TestProvider_sharedValueSurvivesRuns(t *testing.T) {
	db := &sharedDB{}
	dep := NewDependency[*sharedDB]("prov.db")
	var queryErrs []error
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			queryErrs = append(queryErrs, rtx.MustDependency(dep).Query())
		},
	})
	p.WithProvider(dep, func(context.Context, *Context) (*sharedDB, func() error, error) { return db, nil, nil })
	for range 2 {
		if code, err := p.Run(argv); code != 0 || err != nil {
			t.Fatalf("Run = %d, %v", code, err)
		}
	}
	if len(queryErrs) != 2 || queryErrs[0] != nil || queryErrs[1] != nil || db.closed.Load() {
		t.Errorf("queries = %v, closed %v; want the shared value open and usable in both runs", queryErrs, db.closed.Load())
	}
}

// A provider that fails has its value and cleanup discarded: the cleanup is never called.
func TestProvider_failedProviderCleanupDiscarded(t *testing.T) {
	errDial := errors.New("dial failed")
	dep := NewDependency[*provClient]("prov.fail")
	cleaned := false
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { _, _ = rtx.Dependency(dep) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
		return &provClient{}, func() error { cleaned = true; return nil }, errDial
	})
	if code, err := p.Run(argv); code != 0 || err != nil || cleaned {
		t.Errorf("Run = %d, %v, cleanup called %v; want a clean run and no cleanup", code, err, cleaned)
	}
}

// A provider that asks for another provided dependency is built after it, so it is cleaned up
// first.
func TestProvider_nestedCleansUpOuterFirst(t *testing.T) {
	var log []string
	inner, outer := NewDependency[*provClient]("inner"), NewDependency[*provClient]("outer")
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustDependency(outer) },
	})
	p.WithProvider(inner, func(context.Context, *Context) (*provClient, func() error, error) {
		pc := &provClient{name: "inner", closeLog: &log}
		return pc, pc.Close, nil
	})
	p.WithProvider(outer, func(_ context.Context, rtx *Context) (*provClient, func() error, error) {
		if _, err := rtx.Dependency(inner); err != nil {
			return nil, nil, err
		}
		pc := &provClient{name: "outer", closeLog: &log}
		return pc, pc.Close, nil
	})
	p.Run(argv)
	if want := []string{"outer", "inner"}; !slices.Equal(log, want) {
		t.Errorf("cleanup order = %q, want %q", log, want)
	}
}

// A cleanup error is recorded as an error, reaching the reporter and the exit floor, and
// keeps its category.
func TestProvider_cleanupErrorRecorded(t *testing.T) {
	var log []string
	errClose := errors.New("flush failed")
	dep := NewDependency[*provClient]("db")
	p, argv, _, errs := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustDependency(dep) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
		pc := &provClient{name: "db", closeLog: &log, closeErr: UsageError(errClose)}
		return pc, pc.Close, nil
	})
	code, err := p.Run(argv)
	if code != 1 || !errors.Is(err, errClose) || CategoryOf(err) != CategoryUsage {
		t.Errorf("Run = %d, %v; want 1 and the usage-category cleanup error", code, err)
	}
	if got := errs.String(); !strings.Contains(got, `cleaning up the dependency "db": flush failed`) {
		t.Errorf("stderr = %q", got)
	}
}

// A cleanup that panics is a fault; the other cleanups still run.
func TestProvider_cleanupPanicIsAFault(t *testing.T) {
	var log []string
	ok, bad := NewDependency[*provClient]("ok"), NewDependency[int]("bad")
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			rtx.MustDependency(ok)
			rtx.MustDependency(bad)
		},
	})
	p.WithProvider(ok, func(context.Context, *Context) (*provClient, func() error, error) {
		pc := &provClient{name: "ok", closeLog: &log}
		return pc, pc.Close, nil
	})
	p.WithProvider(bad, func(context.Context, *Context) (int, func() error, error) {
		return 1, func() error { panic("cleanup boom") }, nil
	})
	code, err := p.Run(argv)
	var pe *PanicError
	if code != 1 || !errors.As(err, &pe) || pe.Value != "cleanup boom" || !slices.Equal(log, []string{"ok"}) {
		t.Errorf("Run = %d, %v, cleaned up %q; want a fault and ok cleaned up", code, err, log)
	}
}

// A provider's error is returned as is, keeps its category, is cached for the run, and is
// not recorded unless the handler returns it; MustDependency wraps it in a
// *DependencyError that errors.As and CategoryOf see through.
func TestProvider_errors(t *testing.T) {
	errBad := errors.New("the server URL is malformed")
	dep := NewDependency[*int]("client")
	var calls int
	provide := func(context.Context, *Context) (*int, func() error, error) {
		calls++
		return nil, nil, UsageError(errBad)
	}

	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			if v, ok := rtx.LookupDependency(dep); ok || v != nil {
				t.Errorf("LookupDependency = %v, %v; want unavailable", v, ok)
			}
			_, err := rtx.Dependency(dep)
			if !errors.Is(err, errBad) || CategoryOf(err) != CategoryUsage {
				t.Errorf("Dependency err = %v (%v), want the provider's usage error", err, CategoryOf(err))
			}
		},
	})
	p.WithProvider(dep, provide)
	if code, err := p.Run(argv); code != 0 || err != nil || calls != 1 {
		t.Errorf("Run = %d, %v, provider calls %d; want a clean run and one call", code, err, calls)
	}

	p, argv, _, _ = comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustDependency(dep) },
	})
	p.WithProvider(dep, provide)
	code, err := p.Run(argv)
	var de *DependencyError
	if code != 1 || !errors.As(err, &de) || de.Name != "client" || !errors.Is(err, errBad) {
		t.Fatalf("Run = %d, %v; want a *DependencyError wrapping the provider's error", code, err)
	}
	if errors.Is(de, ErrDependencyNotFound) || CategoryOf(de) != CategoryUsage {
		t.Errorf("DependencyError = %v: matches not-found %v, category %v; want neither, usage",
			de, errors.Is(de, ErrDependencyNotFound), CategoryOf(de))
	}
	if want := `rotini: the dependency "client" could not be provided: the server URL is malformed`; de.Error() != want {
		t.Errorf("Error() = %q, want %q", de.Error(), want)
	}

	// An uncategorized provider error, recorded by a hook, has no category, exactly as the
	// hook's own error would have.
	plain := errors.New("plain")
	p, argv, _, _ = comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			if _, err := rtx.Dependency(dep); err != nil {
				rtx.RecordError(err)
			}
		},
	})
	p.WithProvider(dep, func(context.Context, *Context) (*int, func() error, error) { return nil, nil, plain })
	if code, err := p.Run(argv); code != 1 || !errors.Is(err, plain) || CategoryOf(err) != CategoryNone {
		t.Errorf("Run = %d, %v (%v); want the plain error, uncategorized", code, err, CategoryOf(err))
	}
}

// Missing and mistyped requests through Dependency return a *DependencyError.
func TestProvider_dependencyMissingAndMistyped(t *testing.T) {
	rtx := newContext()
	_, err := rtx.Dependency(NewDependency[int]("none"))
	var de *DependencyError
	if !errors.As(err, &de) || !errors.Is(err, ErrDependencyNotFound) {
		t.Errorf("missing: %v, want a not-found *DependencyError", err)
	}
	rtx.SetDependency(NewDependency[string]("s"), "x")
	if _, err := rtx.Dependency(NewDependency[int]("s")); err == nil || !strings.Contains(err.Error(), "is a string, not the int") {
		t.Errorf("mistyped: %v", err)
	}

	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			if _, err := rtx.Dependency(NewDependency[int]("p")); err == nil || !strings.Contains(err.Error(), "is a string") {
				t.Errorf("provided as another type: %v", err)
			}
		},
	})
	p.WithProvider(NewDependency[string]("p"), func(context.Context, *Context) (string, func() error, error) { return "x", nil, nil })
	p.Run(argv)
}

// A run that halts before asking (a --help answered in the root's CascadingPreRun) never
// builds the dependency.
func TestProvider_notBuiltWhenNeverAsked(t *testing.T) {
	dep := NewDependency[*provClient]("never")
	called := false
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.HaltWithCode(0) },
		"c1.Run":             func(_ context.Context, rtx *Context) { rtx.MustDependency(dep) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, func() error, error) {
		called = true
		return &provClient{}, nil, nil
	})
	if code, err := p.Run(argv); code != 0 || err != nil || called {
		t.Errorf("Run = %d, %v, provider called %v; want a clean halt and no build", code, err, called)
	}
}

// A provider receives the asking hook's context, may read the run's inputs, and its panic
// reaches the reporter as a fault while later requests get an internal error.
func TestProvider_contextInputsAndPanic(t *testing.T) {
	type key struct{}
	dep := NewDependency[string]("ctx")
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.SetContext(context.WithValue(rtx.Context(), key{}, "hook"))
		if v := rtx.MustDependency(dep); v != "hook/alice" {
			t.Errorf("provided %q, want the hook's context value and the argument", v)
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	p.WithProvider(dep, func(ctx context.Context, rtx *Context) (string, func() error, error) {
		in, err := rtx.Inputs[provRunInputs]()
		if err != nil {
			return "", nil, err
		}
		v, _ := ctx.Value(key{}).(string)
		return v + "/" + in.AppRun.Arguments.Name, nil, nil
	})
	if code, err := p.Run([]string{"run", "alice"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}

	boom := NewDependency[int]("boom")
	var second error
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c1.Run": func(_ context.Context, rtx *Context) { rtx.MustDependency(boom) },
		"c0.CascadingPostRun": func(_ context.Context, rtx *Context) {
			_, second = rtx.Dependency(boom)
		},
	})
	p.WithProvider(boom, func(context.Context, *Context) (int, func() error, error) { panic("provider boom") })
	code, err := p.Run(argv)
	var pe *PanicError
	if code != 1 || !errors.As(err, &pe) || pe.Value != "provider boom" {
		t.Errorf("Run = %d, %v; want the provider's panic as a fault", code, err)
	}
	if CategoryOf(second) != CategoryInternal || !strings.Contains(second.Error(), "panicked") {
		t.Errorf("request after the panic = %v, want an internal error", second)
	}
}

type provRunInputs struct {
	App struct {
		Flags struct {
			Verbose bool `rotini:"verbose"`
		}
		Arguments struct{}
	}
	AppRun struct {
		Flags struct {
			Count int `rotini:"count"`
		}
		Arguments struct {
			Name string   `rotini:"name"`
			Rest []string `rotini:"rest"`
		}
	}
}
