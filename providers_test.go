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

// provClient is a provided value that logs its Close.
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
			a := rtx.MustGetDependency(dep)
			b, err := rtx.Dependency(dep)
			c, ok := rtx.GetDependency(dep)
			if err != nil || !ok || a != b || b != c {
				t.Errorf("the three accessors disagree: %p %p %p, %v, %v", a, b, c, err, ok)
			}
			seen = append(seen, a)
		},
	})
	p.WithProvider(dep, func(context.Context, *Context) (*strings.Builder, error) {
		calls.Add(1)
		return &strings.Builder{}, nil
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
			wg.Go(func() { got[i] = rtx.MustGetDependency(dep) })
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
	p.WithProvider(dep, func(context.Context, *Context) (*int, error) {
		calls.Add(1)
		return new(int), nil
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
	provide := func(context.Context, *Context) (string, error) { called = true; return "provided", nil }

	var got string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = rtx.MustGetDependency(dep) }}
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
		"c0.Run": func(_ context.Context, rtx *Context) { _, ok := rtx.GetDependency(dep); called = ok },
	})
	p.WithProvider(dep, provide).WithProvider(dep, nil)
	p.Run(argv)
	if called {
		t.Error("a removed provider still provided")
	}
}

// Provided closers are closed after every hook, before the reporter, in reverse order of
// construction; set values and values the run never built are not closed.
func TestProvider_closedAfterTeardownInReverse(t *testing.T) {
	var log []string
	a, b, c := NewDependency[*provClient]("a"), NewDependency[*provClient]("b"), NewDependency[*provClient]("unused")
	set := NewDependency[*provClient]("set")
	mk := func(name string) func(context.Context, *Context) (*provClient, error) {
		return func(context.Context, *Context) (*provClient, error) {
			return &provClient{name: name, closeLog: &log}, nil
		}
	}
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c1.Run": func(_ context.Context, rtx *Context) {
			rtx.MustGetDependency(a)
			rtx.MustGetDependency(b)
			rtx.MustGetDependency(set)
			rtx.RecordSuccess("done") // so the reporter runs
		},
		"c0.CascadingPostRun": func(_ context.Context, rtx *Context) {
			rtx.MustGetDependency(a) // still open in the last teardown hook
			log = append(log, "teardown")
		},
	})
	p.WithProvider(a, mk("a")).WithProvider(b, mk("b")).WithProvider(c, mk("unused"))
	p.WithDependency(set, &provClient{name: "set", closeLog: &log})
	p.WithReporter(func(context.Context, *Context, Outcome) { log = append(log, "reporter") })
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if want := []string{"teardown", "b", "a", "reporter"}; !slices.Equal(log, want) {
		t.Errorf("order = %q, want %q", log, want)
	}
}

// A provider that asks for another provided dependency is built after it, so it closes first.
func TestProvider_nestedClosesOuterFirst(t *testing.T) {
	var log []string
	inner, outer := NewDependency[*provClient]("inner"), NewDependency[*provClient]("outer")
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(outer) },
	})
	p.WithProvider(inner, func(context.Context, *Context) (*provClient, error) {
		return &provClient{name: "inner", closeLog: &log}, nil
	})
	p.WithProvider(outer, func(_ context.Context, rtx *Context) (*provClient, error) {
		if _, err := rtx.Dependency(inner); err != nil {
			return nil, err
		}
		return &provClient{name: "outer", closeLog: &log}, nil
	})
	p.Run(argv)
	if want := []string{"outer", "inner"}; !slices.Equal(log, want) {
		t.Errorf("close order = %q, want %q", log, want)
	}
}

// A Close error is recorded as an error, reaching the reporter and the exit floor, and keeps
// its category.
func TestProvider_closeErrorRecorded(t *testing.T) {
	var log []string
	errClose := errors.New("flush failed")
	dep := NewDependency[*provClient]("db")
	p, argv, _, errs := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(dep) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, error) {
		return &provClient{name: "db", closeLog: &log, closeErr: UsageError(errClose)}, nil
	})
	code, err := p.Run(argv)
	if code != 1 || !errors.Is(err, errClose) || CategoryOf(err) != CategoryUsage {
		t.Errorf("Run = %d, %v; want 1 and the usage-category close error", code, err)
	}
	if got := errs.String(); !strings.Contains(got, `closing the dependency "db": flush failed`) {
		t.Errorf("stderr = %q", got)
	}
}

// A Close that panics is a fault; the other values still close.
func TestProvider_closePanicIsAFault(t *testing.T) {
	var log []string
	ok, bad := NewDependency[*provClient]("ok"), NewDependency[panicCloser]("bad")
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			rtx.MustGetDependency(ok)
			rtx.MustGetDependency(bad)
		},
	})
	p.WithProvider(ok, func(context.Context, *Context) (*provClient, error) {
		return &provClient{name: "ok", closeLog: &log}, nil
	})
	p.WithProvider(bad, func(context.Context, *Context) (panicCloser, error) { return panicCloser{}, nil })
	code, err := p.Run(argv)
	var pe *PanicError
	if code != 1 || !errors.As(err, &pe) || !slices.Equal(log, []string{"ok"}) {
		t.Errorf("Run = %d, %v, closed %q; want a fault and ok closed", code, err, log)
	}
}

type panicCloser struct{}

func (panicCloser) Close() error { panic("close boom") }

// A provider's error is returned as is, keeps its category, is cached for the run, and is
// not recorded unless the handler returns it; MustGetDependency wraps it in a
// *DependencyError that errors.As and CategoryOf see through.
func TestProvider_errors(t *testing.T) {
	errBad := errors.New("the server URL is malformed")
	dep := NewDependency[*int]("client")
	var calls int
	provide := func(context.Context, *Context) (*int, error) { calls++; return nil, UsageError(errBad) }

	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			if v, ok := rtx.GetDependency(dep); ok || v != nil {
				t.Errorf("GetDependency = %v, %v; want unavailable", v, ok)
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
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(dep) },
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
	p.WithProvider(dep, func(context.Context, *Context) (*int, error) { return nil, plain })
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
	p.WithProvider(NewDependency[string]("p"), func(context.Context, *Context) (string, error) { return "x", nil })
	p.Run(argv)
}

// A run that halts before asking (a --help answered in the root's CascadingPreRun) never
// builds the dependency.
func TestProvider_notBuiltWhenNeverAsked(t *testing.T) {
	dep := NewDependency[*provClient]("never")
	called := false
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.HaltWithCode(0) },
		"c1.Run":             func(_ context.Context, rtx *Context) { rtx.MustGetDependency(dep) },
	})
	p.WithProvider(dep, func(context.Context, *Context) (*provClient, error) { called = true; return &provClient{}, nil })
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
		if v := rtx.MustGetDependency(dep); v != "hook/alice" {
			t.Errorf("provided %q, want the hook's context value and the argument", v)
		}
	}}
	p, _, _ := newTestProgram(h, nil)
	p.WithProvider(dep, func(ctx context.Context, rtx *Context) (string, error) {
		in, err := rtx.Inputs[provRunInputs]()
		if err != nil {
			return "", err
		}
		v, _ := ctx.Value(key{}).(string)
		return v + "/" + in.AppRun.Arguments.Name, nil
	})
	if code, err := p.Run([]string{"run", "alice"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}

	boom := NewDependency[int]("boom")
	var second error
	p, argv, _, _ := comboTree(2, map[string]comboHook{
		"c1.Run": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(boom) },
		"c0.CascadingPostRun": func(_ context.Context, rtx *Context) {
			_, second = rtx.Dependency(boom)
		},
	})
	p.WithProvider(boom, func(context.Context, *Context) (int, error) { panic("provider boom") })
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

// The Option form registers the same provider.
func TestProvider_option(t *testing.T) {
	dep := NewDependency[string]("opt")
	var got string
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { got = rtx.MustGetDependency(dep) },
	})
	p.With(WithProvider(dep, func(context.Context, *Context) (string, error) { return "opt", nil }))
	p.Run(argv)
	if got != "opt" {
		t.Errorf("got %q", got)
	}
}
