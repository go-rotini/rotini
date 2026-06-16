package rotini

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
)

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

func (a ctxAgg) Main() CommandHandlers { return a.h }

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
	if code, _ := p.run(nil); code != 0 {
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

func (a lifeAgg) Main() CommandHandlers { return a.h }

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
	if code, _ := p.run(nil); code != 0 {
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
	code, err := newLifeProgram(h).WithContext(ctx).run(nil)
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
	code, _ := newLifeProgram(h).WithContext(ctx).run(nil)
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
		MustGet[context.CancelFunc](rtx, "cancel")()
	}}
	code, err := newLifeProgram(h).WithContext(ctx).Bind("cancel", cancel).run(nil)
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
		MustGet[context.CancelCauseFunc](rtx, "cancel")(ExitCode(3))
	}}
	code, _ := newLifeProgram(h).WithContext(ctx).Bind("cancel", cancel).run(nil)
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (bound cancel with ExitCode(3))", code)
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v", order, want)
	}
}

// By default (WithPanicForward true), a recovered panic halts forward progress but
// teardown for the begun setup hook still runs; the fault exits non-zero.
func TestProgram_panic_default_runsTeardown(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	code, _ := newLifeProgram(h).run(nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (panic → fault)", code)
	}
	if want := []string{"CascadingPreRun", "CascadingPostRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (teardown runs on panic by default)", order, want)
	}
}

// WithPanicForward(false) makes a recovered panic a hard stop: remaining teardown is skipped.
func TestProgram_panic_withPanicForwardFalse_skipsTeardown(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	code, _ := newLifeProgram(h).WithPanicForward(false).run(nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (panic → fault)", code)
	}
	if want := []string{"CascadingPreRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (teardown skipped on panic)", order, want)
	}
}

// WithoutPanicRecover disables recovery: a hook panic propagates raw (no recover,
// no teardown, no OnPanic). The closure recovers it so the test process survives.
func TestProgram_withoutPanicRecover_propagates(t *testing.T) {
	var order []string
	h := lifeRec{order: &order, onCascadingPreRun: func(context.Context, *Context) { panic("boom") }}
	recovered := func() (r any) {
		defer func() { r = recover() }()
		newLifeProgram(h).WithoutPanicRecover().run(nil)
		return nil
	}()
	if recovered != "boom" {
		t.Errorf("recovered %v, want the propagated panic %q", recovered, "boom")
	}
	if want := []string{"CascadingPreRun"}; !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v (panic propagated; no teardown)", order, want)
	}
}
