package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestProgram_WithExit_capturesCode proves WithExit makes Execute hand the resolved code
// to a callback instead of calling os.Exit, so Execute returns and a test can assert on
// the code — here a handler's rtx.SignalExit(1).
func TestProgram_WithExit_capturesCode(t *testing.T) {
	var code int
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.SignalExit(1) }}

	NewProgram(testDef(), h).
		WithArgs([]string{"run", "x"}).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 1 {
		t.Errorf("WithExit captured code = %d, want 1", code)
	}
}

// TestProgram_WithStderr_capturesDiagnostics proves WithStderr redirects the runtime's
// own diagnostics: a panicking hook is funneled to the default OnPanic, which writes to
// the program's stderr and exits 1.
func TestProgram_WithStderr_capturesDiagnostics(t *testing.T) {
	var code int
	errb := &bytes.Buffer{}
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { panic("boom") }}

	NewProgram(testDef(), h).
		WithArgs([]string{"run", "x"}).
		WithStderr(errb).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 1 {
		t.Errorf("panic exit code = %d, want %d", code, 1)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("stderr = %q, want it to contain the panic message", errb.String())
	}
}

// TestProgram_WithStdout_capturesRuntimeOutput proves WithStdout redirects what the
// runtime writes directly — here the hidden __complete entry's candidates.
func TestProgram_WithStdout_capturesRuntimeOutput(t *testing.T) {
	out := &bytes.Buffer{}
	code := -1

	NewProgram(testDef(), &testHandlers{log: new([]string)}).
		WithArgs([]string{"__complete", "ru"}).
		WithStdout(out).
		WithExit(func(c int) { code = c }).
		Execute()

	if code != 0 || strings.TrimSpace(out.String()) != "run" {
		t.Errorf("__complete via Execute: out=%q code=%d, want out=%q code=0", out.String(), code, "run")
	}
}

// TestProgram_Run_isReentrant proves the contract [Program.Run] exists for: a Program is
// reusable across invocations (the REPL / daemon / stdio-server case). Each call gets a
// fresh Context, so neither the recorded outcomes nor the exit code of one run leak into
// the next.
func TestProgram_Run_isReentrant(t *testing.T) {
	var n int
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		n++
		if n == 1 { // only the FIRST run fails
			rtx.RecordError(errors.New("first run failed"))
			rtx.SignalExit(3)
		}
	}}
	p, _, errb := newTestProgram(h, nil)

	if code, _ := p.Run([]string{"run", "x"}); code != 3 {
		t.Fatalf("run 1 code = %d, want 3", code)
	}
	if !strings.Contains(errb.String(), "first run failed") {
		t.Errorf("run 1 stderr = %q, want the recorded error", errb.String())
	}

	errb.Reset()
	code, err := p.Run([]string{"run", "x"})
	if code != 0 || err != nil {
		t.Errorf("run 2 = (%d, %v), want (0, nil) — run 1's exit code and errors must not carry over", code, err)
	}
	if errb.String() != "" {
		t.Errorf("run 2 stderr = %q, want empty — run 1's records must not be re-reported", errb.String())
	}
}

// TestProgram_Run_seedsBoundServicesPerRun pins the registry half of the re-entrancy
// contract: services bound with [Program.Bind] reach EVERY run, while a binding a handler
// makes DURING a run is local to that run.
func TestProgram_Run_seedsBoundServicesPerRun(t *testing.T) {
	var seeded, leaked []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		_, ok := rtx.Get[string]("seeded")
		seeded = append(seeded, ok)
		_, ok = rtx.Get[string]("per-run")
		leaked = append(leaked, ok)
		rtx.Bind("per-run", "bound during this run")
	}}
	p, _, _ := newTestProgram(h, nil)
	p.Bind("seeded", "bound before any run")

	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if want := []bool{true, true}; !slices.Equal(seeded, want) {
		t.Errorf("Program.Bind visibility = %v, want %v — a seeded service reaches every run", seeded, want)
	}
	if want := []bool{false, false}; !slices.Equal(leaked, want) {
		t.Errorf("in-run Bind visibility = %v, want %v — a run's own binding must not leak forward", leaked, want)
	}
}

// TestProgram_RunContext_scopesOneInvocation pins why RunContext exists alongside
// WithContext: a host that dispatches many invocations (a REPL, a stdio server)
// must be able to scope EACH one without permanently changing the program.
func TestProgram_RunContext_scopesOneInvocation(t *testing.T) {
	var seen []bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {}}
	p, _, _ := newTestProgram(h, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := p.RunContext(ctx, []string{"run", "x"}); err != nil {
		t.Fatalf("RunContext = %v", err)
	}
	cancel()
	// The program was NOT mutated: a later plain Run still owns its own context.
	if p.ctx != nil {
		t.Error("RunContext mutated the program's context")
	}
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Errorf("Run after RunContext = %v, want nil", err)
	}
	_ = seen
}

// A canceled context halts the invocation.
func TestProgram_RunContext_cancellationHalts(t *testing.T) {
	var ran bool
	h := &testHandlers{log: new([]string), onRun: func(*Context) { ran = true }}
	p, _, _ := newTestProgram(h, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.RunContext(ctx, []string{"run", "x"}); err != nil {
		t.Fatalf("RunContext on a canceled context = %v", err)
	}
	if ran {
		t.Error("the handler ran under an already-canceled context")
	}
}

// A nil context is a wiring mistake, reported rather than panicking deeper.
func TestProgram_RunContext_nilContext(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, nil)

	// Declared rather than passed as a literal nil: "never pass a nil Context" is
	// right at every real call site, and vet and IDE inspections say so. The point
	// here is that the guard exists for callers who get it wrong anyway.
	var missing context.Context
	code, err := p.RunContext(missing, []string{"run", "x"})
	if !errors.Is(err, ErrInternal) || code == 0 {
		t.Errorf("RunContext(nil) = (%d, %v), want a non-zero code and an internal error", code, err)
	}
}

// ── outcome funnel ──────────────────────────────────────────.

// TestRun_recordSuccess_defaultToStdout: a recorded success reaches the default funnel,
// which prints it to stdout, and the run stays exit 0.
func TestRun_recordSuccess_defaultToStdout(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordSuccess("deployed 3 services")
	}}
	p, out, _ := newTestProgram(h, []string{"run"})
	code, err := p.Run(p.args)
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
	code, err := p.Run(p.args)
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
	code, err := p.Run(p.args)
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
	p.WithFunnel(func(_ context.Context, rtx *Context, out Outcome) {
		infos := out.Infos
		successes := out.Successes
		warnings := out.Warnings
		errs := out.Errors
		panics := out.Panics
		gotInfos, gotSuccesses, gotWarnings, gotErrors, gotPanics = infos, successes, warnings, errs, panics
		rtx.Exit(1)
	})

	code, _ := p.Run(p.args)
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
	if code, _ := pd.Run(pd.args); code != 1 {
		t.Errorf("default funnel: code = %d, want 1 (floors)", code)
	}

	// Custom funnel that sets no code → exits 0 (no floor; the funnel owns the code).
	pc, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	pc.WithFunnel(func(context.Context, *Context, Outcome) {})
	if code, _ := pc.Run(pc.args); code != 0 {
		t.Errorf("custom funnel without an exit: code = %d, want 0 (funnel owns the code)", code)
	}

	// Custom funnel that calls rtx.Exit(7) → that code wins.
	ps, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: rec}, []string{"run"})
	ps.WithFunnel(func(_ context.Context, rtx *Context, _ Outcome) { rtx.Exit(7) })
	if code, _ := ps.Run(ps.args); code != 7 {
		t.Errorf("custom funnel rtx.Exit(7): code = %d, want 7", code)
	}

	// A handler set 2 during the lifecycle; the funnel OVERRIDES it to 5.
	po, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("boom"))
		rtx.SignalExit(2)
	}}, []string{"run"})
	po.WithFunnel(func(_ context.Context, rtx *Context, _ Outcome) { rtx.Exit(5) })
	if code, _ := po.Run(po.args); code != 5 {
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
	if code, _ := pd.Run(pd.args); code == 0 {
		t.Error("default funnel let a panic exit 0; want non-zero (the floor)")
	}

	// A custom funnel that sets no code masks the fault to 0 (it owns the exit).
	pc, _, _ := newTestProgram(&testHandlers{log: new([]string), onRun: panicRun}, []string{"run"})
	var sawPanic bool
	pc.WithFunnel(func(_ context.Context, _ *Context, out Outcome) {
		panics := out.Panics
		sawPanic = len(panics) == 1
	})
	if code, _ := pc.Run(pc.args); code != 0 || !sawPanic {
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
	code, err := p.Run(p.args) // default funnel
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
	p.WithFunnel(func(_ context.Context, rtx *Context, out Outcome) {
		errs := out.Errors
		panics := out.Panics
		gotErrors = errs
		if len(panics) > 0 {
			seen = panics[0]
		}
		rtx.Exit(1)
	})

	code, err := p.Run(p.args)
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
	p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) {
		errs := out.Errors
		panics := out.Panics
		nErrors, nPanics = len(errs), len(panics)
	})

	if _, _ = p.Run(p.args); nErrors != 0 || nPanics != 1 {
		t.Errorf("(errors, panics) = (%d, %d), want (0, 1) for a resolver fault", nErrors, nPanics)
	}
}

// TestRun_silentRun_doesNotInvokeFunnel: a run that records nothing and never faults does
// not invoke the funnel and exits 0.
func TestRun_silentRun_doesNotInvokeFunnel(t *testing.T) {
	fired := false
	h := &testHandlers{log: new([]string)} // onRun nil → a clean run
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(context.Context, *Context, Outcome) { fired = true })

	code, err := p.Run(p.args)
	if fired {
		t.Error("a silent successful run invoked the funnel; want no call")
	}
	if code != 0 || err != nil {
		t.Errorf("run() = (%d, %v), want (0, nil)", code, err)
	}
}

// ── context propagation ─────────────────────────────────────.

// ctxRec is a minimal one-command handler that captures the context passed to Run.
type ctxRec struct{ run func(ctx context.Context) }

func (ctxRec) CascadingPreRun(context.Context, *Context)  {}
func (ctxRec) PreRun(context.Context, *Context)           {}
func (ctxRec) PostRun(context.Context, *Context)          {}
func (ctxRec) CascadingPostRun(context.Context, *Context) {}
func (h ctxRec) Run(ctx context.Context, _ *Context) {
	if h.run != nil {
		h.run(ctx)
	}
}

type ctxAgg struct{ h ctxRec }

func (a ctxAgg) Main() Handlers { return a.h }

func newCtxProgram(h ctxRec) *Program {
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, ctxAgg{h})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return p
}

func TestProgram_WithContext_threadsToHooks(t *testing.T) {
	type key struct{}
	base := context.WithValue(context.Background(), key{}, "v")
	var got any
	p := newCtxProgram(ctxRec{run: func(ctx context.Context) { got = ctx.Value(key{}) }}).WithContext(base)
	if code, _ := p.Run(nil); code != 0 {
		t.Fatalf("run exit = %d, want 0", code)
	}
	if got != "v" {
		t.Errorf("handler's ctx value = %v, want v (WithContext did not thread to the hook)", got)
	}
}

// A fresh program has no base context (the sentinel for "rotini owns the lifecycle and
// installs the default signal trap"); WithContext(nil) must be a no-op that preserves it.
func TestProgram_WithContext_nilIgnored(t *testing.T) {
	p := newCtxProgram(ctxRec{})
	if p.ctx != nil {
		t.Fatal("a fresh program should have no base context (default signal handling)")
	}
	p.WithContext(nil)
	if p.ctx != nil {
		t.Error("WithContext(nil) should be a no-op, leaving the default (nil) context")
	}
}

// lifeRec records the order of lifecycle hooks and can run a side effect (e.g.
// cancel the run) inside CascadingPreRun.
type lifeRec struct {
	order             *[]string
	onCascadingPreRun func(ctx context.Context, rtx *Context)
}

func (h lifeRec) CascadingPreRun(ctx context.Context, rtx *Context) {
	*h.order = append(*h.order, "CascadingPreRun")
	if h.onCascadingPreRun != nil {
		h.onCascadingPreRun(ctx, rtx)
	}
}
func (h lifeRec) PreRun(context.Context, *Context)  { *h.order = append(*h.order, "PreRun") }
func (h lifeRec) Run(context.Context, *Context)     { *h.order = append(*h.order, "Run") }
func (h lifeRec) PostRun(context.Context, *Context) { *h.order = append(*h.order, "PostRun") }
func (h lifeRec) CascadingPostRun(context.Context, *Context) {
	*h.order = append(*h.order, "CascadingPostRun")
}

type lifeAgg struct{ h lifeRec }

func (a lifeAgg) Main() Handlers { return a.h }

func newLifeProgram(h lifeRec) *Program {
	p := NewProgram(Definition{Name: "app", Handler: "Main"}, lifeAgg{h})
	p.stdout, p.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return p
}

// A non-canceled WithContext context runs the full lifecycle (regression guard
// for the symmetric-halt change).
func TestProgram_WithContext_noCancelRunsFullLifecycle(t *testing.T) {
	var order []string
	p := newLifeProgram(lifeRec{order: &order}).WithContext(context.Background())
	if code, _ := p.Run(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := []string{"CascadingPreRun", "PreRun", "Run", "PostRun", "CascadingPostRun"}
	if !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v", order, want)
	}
}

// Canceling a caller-owned context mid-lifecycle halts forward progress (no
// further setup/PreRun/Run starts) but still runs teardown, and the ExitCode
// cause sets the process exit code.
func TestProgram_WithContext_cancelHaltsForwardRunsTeardown(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { cancel(ExitCode(3)) }}
	code, err := newLifeProgram(h).WithContext(ctx).Run(nil)
	if err != nil {
		t.Fatalf("run err = %v, want nil (cancel is a clean stop, not an error)", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (from the ExitCode cancellation cause)", code)
	}
	want := []string{"CascadingPreRun", "CascadingPostRun"}
	if !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (forward halted, teardown ran)", order, want)
	}
}

// Canceling without an ExitCode cause halts cleanly and exits 0 (the code falls
// through to the normal resolution).
func TestProgram_WithContext_cancelWithoutCodeExitsZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { cancel() }}
	code, _ := newLifeProgram(h).WithContext(ctx).Run(nil)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (plain cancel, no ExitCode cause)", code)
	}
	want := []string{"CascadingPreRun", "CascadingPostRun"}
	if !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v", order, want)
	}
}

// ExitCode's cause is matchable via context.Cause for callers/loggers.
func TestExitCode_isCancelCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ExitCode(7))
	var ec exitCodeError
	if !errors.As(context.Cause(ctx), &ec) || ec.code != 7 {
		t.Errorf("context.Cause = %v, want an ExitCode(7) cause", context.Cause(ctx))
	}
}

// The recommended way for a handler to cancel the run: the caller binds its own
// cancel func; the handler retrieves it via MustGet and calls it. A plain cancel
// halts forward progress, runs teardown, and exits 0.
func TestProgram_handlerCancelsViaBoundCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(_ context.Context, rtx *Context) {
		rtx.MustGet[context.CancelFunc]("cancel")()
	}}
	code, err := newLifeProgram(h).WithContext(ctx).Bind("cancel", cancel).Run(nil)
	if err != nil {
		t.Fatalf("run err = %v, want nil", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (plain bound cancel)", code)
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (forward halted, teardown ran)", order, want)
	}
}

// Binding a WithCancelCause cancel lets a handler choose the exit code with ExitCode.
func TestProgram_handlerCancelsWithExitCode(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(_ context.Context, rtx *Context) {
		rtx.MustGet[context.CancelCauseFunc]("cancel")(ExitCode(3))
	}}
	code, _ := newLifeProgram(h).WithContext(ctx).Bind("cancel", cancel).Run(nil)
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (bound cancel with ExitCode(3))", code)
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v", order, want)
	}
}

// By default (WithTeardownOnPanic true), a recovered panic halts forward progress but
// teardown for the begun setup hook still runs; the fault exits non-zero.
func TestProgram_panic_default_runsTeardown(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	code, _ := newLifeProgram(h).Run(nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (panic → fault)", code)
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (teardown runs on panic by default)", order, want)
	}
}

// WithTeardownOnPanic(false) makes a recovered panic a hard stop: remaining teardown is skipped.
func TestProgram_panic_withTeardownOnPanicFalse_skipsTeardown(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	code, _ := newLifeProgram(h).WithTeardownOnPanic(false).Run(nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (panic → fault)", code)
	}
	if want := []string{"CascadingPreRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (teardown skipped on panic)", order, want)
	}
}

// WithPanicRecover(false) with the default PanicForward(true): teardown still runs, THEN the
// panic is re-raised raw. The closure recovers it so the test process survives.
func TestProgram_withPanicRecoverFalse_forwardTrue_teardownThenRepanic(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	recovered := func() (r any) {
		defer func() { r = recover() }()
		newLifeProgram(h).WithPanicRecover(false).Run(nil)
		return nil
	}()
	if recovered != "boom" {
		t.Errorf("recovered %v, want the re-raised panic %q", recovered, "boom")
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (teardown ran, then re-panic)", order, want)
	}
}

// WithPanicRecover(false) + WithTeardownOnPanic(false): "panic now" — the panic propagates
// immediately, skipping teardown.
func TestProgram_withPanicRecoverFalse_forwardFalse_panicsNow(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	recovered := func() (r any) {
		defer func() { r = recover() }()
		newLifeProgram(h).WithPanicRecover(false).WithTeardownOnPanic(false).Run(nil)
		return nil
	}()
	if recovered != "boom" {
		t.Errorf("recovered %v, want the propagated panic %q", recovered, "boom")
	}
	if want := []string{"CascadingPreRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (panic now; no teardown)", order, want)
	}
}

// ── run lifecycle and outcomes ──────────────────────────────.

func TestRun_lifecycleOrderAndContext(t *testing.T) {
	var log []string
	var gotChain []ResolvedCommand
	var gotArgs []string
	args := []string{"--verbose", "run", "alice", "x", "y", "--count", "3"}
	h := &testHandlers{log: &log, onRun: func(rtx *Context) {
		gotChain, gotArgs = rtx.Chain(), rtx.Args
	}}

	p, _, errb := newTestProgram(h, args)
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0 (stderr: %s)", code, errb)
	}

	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
		"run.CascadingPostRun", "app.CascadingPostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}

	if names := chainNames(gotChain); len(names) != 2 || names[0] != "app" || names[1] != "run" {
		t.Errorf("Chain() during Run = %v, want [app run]", names)
	}
	if !reflect.DeepEqual(gotArgs, args) {
		t.Errorf("rtx.Args during Run = %v, want %v", gotArgs, args)
	}
}

func TestRun_aliasResolves(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"r", "bob"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !contains(log, "run.Run") {
		t.Errorf("alias did not resolve to run command: %v", log)
	}
}

// Under §17 the runtime no longer rejects an unknown command itself: it resolves
// what it can and dispatches the leaf handler. A typo of a sub-command lands on
// the (branching) root handler, which surfaces the error only if it opts into
// Parse — see TestParse_unknownCommand. run itself succeeds and runs that handler.
func TestRun_unresolvedDispatchesLeaf(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"ru"})
	if code, _ := p.Run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0 (the root handler runs; it owns input errors)", code)
	}
	if !contains(log, "app.Run") {
		t.Errorf("expected the root handler to run, got %v", log)
	}
}

func TestRun_exitCodePropagates(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.SignalExit(5) }}
	p, _, _ := newTestProgram(h, []string{"run"})
	if code, _ := p.Run(p.args); code != 5 {
		t.Errorf("run() = %d, want 5 (handler called Exit)", code)
	}
}

func TestRun_mustGetRoutesToFunnelPanics(t *testing.T) {
	var seen *PanicError
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		panic(&ServiceError{Key: "no-such-service"})
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(_ context.Context, rtx *Context, out Outcome) {
		panics := out.Panics
		seen = panics[0]
		rtx.Exit(7)
	})

	code, err := p.Run(p.args)
	if code != 7 {
		t.Fatalf("run() = %d, want 7 (the funnel's exit code)", code)
	}

	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("run returned err = %v, want it to wrap ErrServiceNotFound", err)
	}

	if !errors.Is(seen, ErrServiceNotFound) {
		t.Errorf("funnel got %v, want it to wrap ErrServiceNotFound", seen)
	}
	var se *ServiceError
	if !errors.As(seen, &se) || se.Key != "no-such-service" {
		t.Errorf("funnel error did not carry the key: %v", seen)
	}
}

func TestRun_defaultFunnelFaultFloorsTo1(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		panic(&ServiceError{Key: "missing"})
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	if code, _ := p.Run(p.args); code != 1 {
		t.Errorf("run() = %d, want 1 (the default funnel floors a fault)", code)
	}
}

func TestRun_defaultOnPanicPrintsAndFails(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		panic("boom")
	}}
	p, _, errb := newTestProgram(h, []string{"run"})
	code, err := p.Run(p.args)
	if code != 1 {
		t.Fatalf("run() = %d, want %d (default OnPanic)", code, 1)
	}

	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("run returned err = %v, want it to carry %q", err, "boom")
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("default OnPanic should print the panic, stderr: %s", errb)
	}
}

// Exit in PreRun: Run is skipped, but PostRun (paired with PreRun) still runs.
func TestRun_exitInPreRunStillRunsPostRun(t *testing.T) {
	code, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.SignalExit(1) }},
	})
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.PostRun",
		"run.CascadingPostRun", "app.CascadingPostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}

// Exit in Run: PostRun (paired with PreRun) still runs.
func TestRun_exitInRunStillRunsPostRun(t *testing.T) {
	_, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "Run", do: func(rtx *Context) { rtx.SignalExit(1) }},
	})
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
		"run.CascadingPostRun", "app.CascadingPostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
}

// Hard Exit in Run: forward stops AND all pending teardown is skipped — no PostRun,
// no CascadingPostRun runs (contrast TestRun_exitInRunStillRunsPostRun).
func TestRun_hardExitInRunSkipsTeardown(t *testing.T) {
	code, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "Run", do: func(rtx *Context) { rtx.Exit(4) }},
	})
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
	if code != 4 {
		t.Errorf("code = %d, want 4", code)
	}
}

// Hard Exit from within a teardown hook: the rest of teardown is skipped too — the
// leaf's PostRun runs (where Exit is called) but the CascadingPostRuns after it do not.
func TestRun_hardExitInTeardownSkipsRemainingTeardown(t *testing.T) {
	_, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "PostRun", do: func(rtx *Context) { rtx.Exit(1) }},
	})
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
}

// A hard Exit skips remaining teardown but does NOT bypass the funnel: a panic
// recovered before the Exit is still handed to the funnel after the (skipped)
// teardown. Run panics, then the leaf's PostRun hard-Exits — the trailing
// CascadingPostRuns are skipped, yet the funnel still fires with the panic.
func TestRun_hardExitStillRoutesPendingPanicToFunnel(t *testing.T) {
	var log []string
	var seen *PanicError
	p, _, _ := newTestProgram(&panicThenHardExit{log: &log}, []string{"run"})
	p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) {
		panics := out.Panics
		seen = panics[0]
	})

	code, err := p.Run(p.args)

	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
	if seen == nil || seen.Error() != "boom" {
		t.Errorf("OnPanic saw %v, want the recovered panic %q", seen, "boom")
	}
	if !errorContains(err, "boom") {
		t.Errorf("run returned err = %v, want it to carry %q", err, "boom")
	}
	if code != 3 {
		t.Errorf("code = %d, want 3 (the hard Exit's code, kept over the fault's 1)", code)
	}
}

// Exit in the leaf's CascadingPreRun: PreRun never begins, so PostRun does not run
// (its pair never started), but both CascadingPostRuns do.
func TestRun_exitInLeafCascadingPreRunSkipsPostRun(t *testing.T) {
	_, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.SignalExit(1) }},
	})
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.CascadingPostRun", "app.CascadingPostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
}

// Exit in a non-leaf CascadingPreRun: the leaf's CascadingPreRun never begins, so
// its CascadingPostRun must NOT run — only begun commands tear down (paired rule).
func TestRun_exitInRootCascadingPreRunSkipsUnstartedTeardown(t *testing.T) {
	_, log := runActs(t, []string{"run"}, map[string]act{
		"app": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.SignalExit(1) }},
	})
	want := []string{"app.CascadingPreRun", "app.CascadingPostRun"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("hook order:\n got=%v\nwant=%v", log, want)
	}
}

// The first non-zero Exit wins: a teardown hook calling Exit again cannot change
// the verdict set during Run.
func TestRun_firstNonZeroExitWins(t *testing.T) {
	code, _ := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "Run", do: func(rtx *Context) { rtx.SignalExit(3) }},
		"app": {at: "CascadingPostRun", do: func(rtx *Context) { rtx.SignalExit(7) }},
	})
	if code != 3 {
		t.Errorf("code = %d, want 3 (first non-zero Exit wins; teardown Exit(7) ignored)", code)
	}
}

// A panic runs all begun teardown first, then funnels last.
func TestRun_panicRunsTeardownThenFunnelLast(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "Run", do: func(*Context) { panic("boom") }},
	}}, []string{"run"})
	p.WithFunnel(func(_ context.Context, rtx *Context, out Outcome) {
		panics := out.Panics
		log = append(log, "funnel:"+panics[0].Error())
		rtx.Exit(5)
	})
	code, _ := p.Run(p.args)
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run",
		"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
		"funnel:boom",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("teardown-then-funnel order:\n got=%v\nwant=%v", log, want)
	}
	if code != 5 {
		t.Errorf("code = %d, want 5 (the funnel's explicit exit)", code)
	}
}

// A panic inside a teardown hook is recovered: the remaining teardown still runs
// and the funnel fires exactly once.
func TestRun_teardownPanicContinuesAndFunnelsOnce(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "PostRun", do: func(*Context) { panic("teardown-boom") }},
	}}, []string{"run"})
	calls := 0
	p.WithFunnel(func(_ context.Context, rtx *Context, _ Outcome) {
		calls++
		rtx.Exit(1)
	})
	code, _ := p.Run(p.args)
	if !contains(log, "app.CascadingPostRun") {
		t.Errorf("remaining teardown did not run after a teardown panic: %v", log)
	}
	if calls != 1 {
		t.Errorf("funnel called %d times, want 1", calls)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}

// errOutcomeTest is a stand-in error for the Outcome predicate table.
var errOutcomeTest = errors.New("outcome test")

// TestOutcome_EmptyAndFailed pins the two predicates the runtime and the default funnel key
// on, so a custom funnel can rely on them meaning exactly what the engine means.
func TestOutcome_EmptyAndFailed(t *testing.T) {
	cases := []struct {
		name        string
		out         Outcome
		empty, fail bool
	}{
		{"zero value", Outcome{}, true, false},
		{"info only", Outcome{Infos: []string{"x"}}, false, false},
		{"success only", Outcome{Successes: []string{"x"}}, false, false},
		{"warning only", Outcome{Warnings: []error{errOutcomeTest}}, false, false},
		{"error", Outcome{Errors: []error{errOutcomeTest}}, false, true},
		{"panic", Outcome{Panics: []*PanicError{{Value: "boom"}}}, false, true},
		{"warning does not fail", Outcome{Infos: []string{"i"}, Warnings: []error{errOutcomeTest}}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.out.Empty(); got != tc.empty {
				t.Errorf("Empty() = %v, want %v", got, tc.empty)
			}
			if got := tc.out.Failed(); got != tc.fail {
				t.Errorf("Failed() = %v, want %v", got, tc.fail)
			}
		})
	}
}

// TestFunnel_receivesEveryChannelByName is the reason Outcome is a struct: a funnel reads
// each channel by name, so the two []string channels and the two []error channels cannot be
// transposed by a call site that gets the order wrong.
func TestFunnel_receivesEveryChannelByName(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordInfo("an info")
		rtx.RecordSuccess("a success")
		rtx.RecordWarning(errors.New("a warning"))
		rtx.RecordError(errors.New("an error"))
	}}
	p, _, _ := newTestProgram(h, []string{"run"})

	var got Outcome
	p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) { got = out })
	if _, err := p.Run(p.args); err == nil {
		t.Fatal("run recorded an error, want it returned")
	}

	for _, c := range []struct {
		channel string
		n       int
		first   string
	}{
		{"Infos", len(got.Infos), first(got.Infos)},
		{"Successes", len(got.Successes), first(got.Successes)},
		{"Warnings", len(got.Warnings), firstErr(got.Warnings)},
		{"Errors", len(got.Errors), firstErr(got.Errors)},
	} {
		if c.n != 1 {
			t.Errorf("%s: got %d entries, want 1", c.channel, c.n)
		}
	}
	if first(got.Infos) != "an info" || first(got.Successes) != "a success" {
		t.Errorf("string channels transposed: infos=%q successes=%q", got.Infos, got.Successes)
	}
	if firstErr(got.Warnings) != "a warning" || firstErr(got.Errors) != "an error" {
		t.Errorf("error channels transposed: warnings=%v errors=%v", got.Warnings, got.Errors)
	}
	if len(got.Panics) != 0 {
		t.Errorf("Panics = %v, want empty", got.Panics)
	}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func firstErr(e []error) string {
	if len(e) == 0 {
		return ""
	}
	return e[0].Error()
}

// syncWriter serializes writes, standing in for the concurrency-safe writer a program driving
// concurrent runs must supply.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// concurrentHandlers is a handler set with NO shared mutable state, so this test measures
// rotini's own concurrency safety rather than the shared recording fixture's. That split is
// the contract itself: rotini gives each run its own Context, but the handlers value passed
// to NewProgram is shared across runs, so handler state is the author's to protect.
type concurrentHandlers struct{ seen chan string }

func (c *concurrentHandlers) App() Handlers    { return &concurrentHandler{} }
func (c *concurrentHandlers) AppRun() Handlers { return &concurrentHandler{seen: c.seen} }

type concurrentHandler struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	seen chan string
}

func (h *concurrentHandler) Run(_ context.Context, rtx *Context) {
	if h.seen == nil {
		return
	}
	// A service bound once, before any run, must be visible to every run.
	if v, ok := rtx.Get[string]("shared"); !ok || v != "bound once" {
		h.seen <- "missing shared service"
		return
	}
	rtx.RecordInfo("ran") // into this run's own context; nothing may leak across runs
	h.seen <- rtx.Path()
}

// TestRun_concurrentDispatch pins what doc.go promises: Run is not merely re-entrant (the
// REPL's requirement — one dispatch after another) but safe to call CONCURRENTLY once
// configuration is done, which is what a StdioServer author will assume when a peer
// pipelines requests. Each run gets its own Context with its own records, while services
// bound before the runs reach all of them.
//
// It runs under -race in CI; without that this test proves much less.
func TestRun_concurrentDispatch(t *testing.T) {
	const runs = 32

	// Concurrent runs SHARE the program's streams, so the writer has to be safe for
	// concurrent use. os.Stdout is; a bytes.Buffer is not. That requirement is part of the
	// contract, and it is why this test wraps its buffers rather than passing them raw.
	seen := make(chan string, runs)
	p := NewProgram(testDef(), &concurrentHandlers{seen: seen}).
		WithStdout(&syncWriter{w: new(bytes.Buffer)}).
		WithStderr(&syncWriter{w: new(bytes.Buffer)}).
		WithExit(func(int) {})
	p.Bind("shared", "bound once")

	// Signal trapping is per-run: every concurrent run would install its own handler and
	// all of them would observe one signal. A concurrent driver therefore supplies its own
	// context, which is exactly what doc.go tells it to do.
	ctx := context.Background()

	var wg sync.WaitGroup
	for range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, err := p.RunContext(ctx, []string{"run"})
			if err != nil {
				t.Errorf("concurrent run: %v", err)
			}
			if code != 0 {
				t.Errorf("concurrent run exited %d, want 0", code)
			}
		}()
	}
	wg.Wait()
	close(seen)

	n := 0
	for path := range seen {
		if path != "app run" {
			t.Errorf("a run resolved to %q, want \"app run\"", path)
		}
		n++
	}
	if n != runs {
		t.Errorf("%d runs dispatched, want %d", n, runs)
	}
}

// TestRun_recordsDoNotLeakBetweenRuns is the other half of re-entrancy: run N+1 must see none
// of run N's records. The REPL and StdioServer shapes both depend on it.
func TestRun_recordsDoNotLeakBetweenRuns(t *testing.T) {
	var got []Outcome
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("one error per run"))
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(_ context.Context, _ *Context, out Outcome) { got = append(got, out) })

	for range 3 {
		if _, err := p.Run(p.args); err == nil {
			t.Fatal("each run records an error, want it returned")
		}
	}

	if len(got) != 3 {
		t.Fatalf("funnel fired %d times, want 3", len(got))
	}
	for i, out := range got {
		if len(out.Errors) != 1 {
			t.Errorf("run %d saw %d errors, want exactly its own 1 — records leaked", i+1, len(out.Errors))
		}
	}
}

// ── Execute's returned error (P4) ────────────────────────────────────────────
//
// Execute returns an error that, under the default exit action, is unreachable: os.Exit ends
// the process before the return statement runs. That was undocumented and untested, so the
// seam an embedder depends on had nothing holding it in place. These pin the contract the
// doc now states: the error is the run's own failure, joined, and it arrives exactly when the
// exit action returns.

// TestExecute_returnsTheRunsFailure: with a returning exit action, the recorded error reaches
// the caller — and reaches it as a value, not as text.
func TestExecute_returnsTheRunsFailure(t *testing.T) {
	boom := &ParseError{Kind: ParseKindUnknownFlag, Msg: "unknown flag \"--nope\""}

	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(boom)
		rtx.Halt()
	}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})

	code := -1
	err := p.WithExit(func(c int) { code = c }).Execute()

	if err == nil {
		t.Fatal("Execute() = nil, want the error the run recorded")
	}
	// errors.As reaches through the join, which is the point of returning a value at all.
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Errorf("errors.As(*ParseError) failed on %v — the caller cannot classify the failure", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("errors.Is(recorded) failed on %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want the default funnel's error floor of 1", code)
	}
}

// TestExecute_joinsEveryRecordedFailure: the error is the whole run's failure, not the first.
func TestExecute_joinsEveryRecordedFailure(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")

	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(first)
		rtx.RecordError(second)
		rtx.Halt()
	}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})

	err := p.WithExit(func(int) {}).Execute()
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Errorf("Execute() = %v, want both recorded errors reachable", err)
	}
}

// TestExecute_returnsNilOnACleanRun: nothing recorded, nothing returned.
func TestExecute_returnsNilOnACleanRun(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(*Context) {}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})

	code := -1
	if err := p.WithExit(func(c int) { code = c }).Execute(); err != nil {
		t.Errorf("Execute() = %v, want nil on a clean run", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestExecute_exitRunsBeforeTheReturn pins the ordering the doc rests on: the exit action is
// called with the resolved code FIRST, and only then does Execute return. Under os.Exit that
// ordering is exactly why the error cannot arrive.
func TestExecute_exitRunsBeforeTheReturn(t *testing.T) {
	var order []string

	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(errors.New("boom"))
		rtx.Halt()
	}}
	p, _, _ := newTestProgram(h, []string{"run", "x"})

	err := p.WithExit(func(int) { order = append(order, "exit") }).Execute()
	order = append(order, "returned")

	if len(order) != 2 || order[0] != "exit" || order[1] != "returned" {
		t.Errorf("order = %v, want the exit action to run before Execute returns", order)
	}
	if err == nil {
		t.Error("Execute() = nil, want the recorded error")
	}
}

// ── WithArgs' scope (P5) ─────────────────────────────────────────────────────

// TestWithArgs_onlyExecuteConsultsIt pins the boundary the doc now states.
//
// WithArgs used to be documented as "overrides the argument vector", which reads like a
// program-level setting. It is not: Run and RunContext take argv as a parameter and use
// exactly what they were given. The behaviour is deliberate — Run is the re-entrant core a
// REPL calls once per line, so inheriting a program-level default would answer the wrong
// question — but nothing said so and nothing held it in place.
func TestWithArgs_onlyExecuteConsultsIt(t *testing.T) {
	var got []string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		got = append([]string(nil), rtx.Args...)
	}}

	// Execute consults it.
	p, _, _ := newTestProgram(h, []string{"run", "from-withargs"})
	p.WithExit(func(int) {}).Execute()
	if len(got) == 0 || got[len(got)-1] != "from-withargs" {
		t.Errorf("Execute saw %q, want the vector set by WithArgs", got)
	}

	// Run does not: it uses exactly what it was handed.
	got = nil
	p2, _, _ := newTestProgram(h, []string{"run", "from-withargs"})
	if _, err := p2.Run([]string{"run", "explicit"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) == 0 || got[len(got)-1] != "explicit" {
		t.Errorf("Run(argv) saw %q, want the argv it was passed", got)
	}

	// And Run(nil) means "no arguments", NOT "fall back to WithArgs". The fallback is
	// deliberately absent: it would make a REPL line silently inherit the process's argv.
	got = nil
	p3, _, _ := newTestProgram(h, []string{"run", "from-withargs"})
	if _, err := p3.Run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Run(nil) saw %q, want no arguments — WithArgs must not leak into Run", got)
	}
}

// TestWithArgs_nilIsIgnored: a nil vector leaves the default in place, so a conditional
// caller cannot accidentally erase os.Args by passing a nil slice.
func TestWithArgs_nilIsIgnored(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run", "kept"})
	p.WithArgs(nil)
	if len(p.args) != 2 || p.args[1] != "kept" {
		t.Errorf("args = %q, want the previous vector kept when nil is passed", p.args)
	}
	p.WithArgs([]string{})
	if len(p.args) != 0 {
		t.Errorf("args = %q, want an explicit empty vector to take effect", p.args)
	}
}

// TestPanicKnobs_allFourQuadrants exercises the renamed option against its partner, so the
// rename is shown to be behaviour-preserving rather than assumed to be.
//
// The two are orthogonal: WithPanicRecover decides where the panic GOES, WithTeardownOnPanic
// decides whether teardown still RUNS. The old name conflated them, which is the whole reason
// for the rename — so the grid is worth having written down.
func TestPanicKnobs_allFourQuadrants(t *testing.T) {
	for _, tc := range []struct {
		name              string
		recover, teardown bool
		wantTeardown      bool
		wantReRaise       bool
	}{
		{"default — recovered, teardown runs", true, true, true, false},
		{"recovered, teardown skipped", true, false, false, false},
		{"re-raised after teardown", false, true, true, true},
		{"re-raised immediately, teardown skipped", false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}

			var reRaised bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						reRaised = true
					}
				}()
				newLifeProgram(h).
					WithPanicRecover(tc.recover).
					WithTeardownOnPanic(tc.teardown).
					Run(nil)
			}()

			if reRaised != tc.wantReRaise {
				t.Errorf("panic re-raised to the caller = %v, want %v", reRaised, tc.wantReRaise)
			}
			ranTeardown := slices.Contains(order, "CascadingPostRun")
			if ranTeardown != tc.wantTeardown {
				t.Errorf("teardown ran = %v, want %v (hooks: %v)", ranTeardown, tc.wantTeardown, order)
			}
		})
	}
}

// TestDispatch_nilHandlersIsAWiringFault: NewProgram's handlers parameter is `any`, and the
// generated NewProgram takes an interface — so NewProgram(nil) compiles, and used to crash out
// of Run with `reflect: call of reflect.Value.MethodByName on zero Value`.
//
// Two promises were broken at once: the comment at that line says "a wiring failure is
// detected and routed as a fault, never panicked", and the error taxonomy says rotini's own
// messages never leak internals. A reflect panic escaping a public API is both.
func TestDispatch_nilHandlersIsAWiringFault(t *testing.T) {
	var out, errs bytes.Buffer
	p := NewProgram(testDef(), nil)
	p.stdout, p.stderr = &out, &errs

	code, err := func() (c int, e error) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Run panicked instead of reporting a fault: %v", r)
			}
		}()
		return p.Run([]string{"run", "x"})
	}()

	if code == 0 {
		t.Error("exit code = 0, want non-zero for a program with no handlers")
	}
	var we *WiringError
	if !errors.As(err, &we) {
		t.Errorf("error is %T (%v), want a *WiringError", err, err)
	}
	if s := errs.String(); strings.Contains(s, "reflect") || strings.Contains(s, "MethodByName") {
		t.Errorf("the reported message leaks internals: %q", s)
	}
	if !strings.Contains(errs.String(), "nil handlers") {
		t.Errorf("stderr = %q, want it to name the actual mistake", errs.String())
	}
}
