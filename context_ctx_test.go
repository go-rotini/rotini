package rotini

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type ctxKey string

// ctxProgram runs a fixed list of steps through the real engine, with the testDef chain resolved
// from argv "run x".
func ctxProgram(steps []LifecycleStep) *Program {
	return NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithLifecycle(func([]Command, []Handler) []LifecycleStep { return steps }).
		WithStdout(io.Discard).WithStderr(io.Discard)
}

func TestSetContext_laterHooksAndTheirTeardownsReceiveIt(t *testing.T) {
	got := map[string]any{}
	steps := []LifecycleStep{
		{
			Name: "root",
			Do: func(ctx context.Context, rtx *Context) {
				rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "root"))
			},
			Undo: func(ctx context.Context, rtx *Context) {
				got["root.undo"] = ctx.Value(ctxKey("k"))
				got["root.undo.rtx"] = rtx.Context().Value(ctxKey("k"))
			},
		},
		{
			Name: "child",
			Do: func(ctx context.Context, rtx *Context) {
				got["child.do"] = ctx.Value(ctxKey("k"))
				got["child.do.rtx"] = rtx.Context().Value(ctxKey("k"))
				rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "child"))
			},
			Undo: func(ctx context.Context, _ *Context) { got["child.undo"] = ctx.Value(ctxKey("k")) },
		},
		{Name: "run", Do: func(ctx context.Context, _ *Context) { got["run"] = ctx.Value(ctxKey("k")) }},
	}
	if code, err := ctxProgram(steps).Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Fatalf("(%d, %v), want a clean run", code, err)
	}
	want := map[string]any{
		"root.undo": nil, "root.undo.rtx": nil,
		"child.do": "root", "child.do.rtx": "root", "child.undo": "root",
		"run": "child",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s saw %v, want %v", k, got[k], w)
		}
	}
}

// An author's deadline that expires is not the run's cancellation: the run doesn't halt with
// a silent exit 0, and the hook's own error decides the outcome.
func TestSetContext_expiredDeadlineIsNotASilentExit(t *testing.T) {
	var cancel context.CancelFunc
	steps := []LifecycleStep{
		{
			Name: "root",
			Do: func(ctx context.Context, rtx *Context) {
				var c context.Context
				c, cancel = context.WithTimeout(ctx, time.Nanosecond)
				rtx.SetContext(c)
				<-c.Done()
			},
			Undo: func(context.Context, *Context) { cancel() },
		},
		{Name: "run", Do: func(ctx context.Context, rtx *Context) {
			if err := ctx.Err(); err != nil {
				rtx.HaltWith(err)
			}
		}},
	}
	code, err := ctxProgram(steps).Run([]string{"run", "x"})
	if code != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("(%d, %v), want exit 1 with the hook's deadline error", code, err)
	}
}

// A root teardown gets the context its pre-run received, not a later child's expired one.
func TestSetContext_teardownGetsWhatItsDoReceived(t *testing.T) {
	var rootUndoErr error
	steps := []LifecycleStep{
		{Name: "root", Do: func(context.Context, *Context) {}, Undo: func(ctx context.Context, _ *Context) { rootUndoErr = ctx.Err() }},
		{Name: "child", Do: func(ctx context.Context, rtx *Context) {
			c, cancel := context.WithCancel(ctx)
			cancel()
			rtx.SetContext(c)
		}},
		{Name: "run", Do: func(context.Context, *Context) {}},
	}
	if code, _ := ctxProgram(steps).Run([]string{"run", "x"}); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if rootUndoErr != nil {
		t.Errorf("the root teardown's context is done (%v), want the one its Do received", rootUndoErr)
	}
}

func TestSetContext_reporterReceivesTheRunContext(t *testing.T) {
	var reported any
	steps := []LifecycleStep{{Name: "run", Do: func(ctx context.Context, rtx *Context) {
		rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "hook"))
		rtx.RecordWarning(errors.New("w"))
	}}}
	run := context.WithValue(context.Background(), ctxKey("k"), "run")
	p := ctxProgram(steps).WithReporter(func(ctx context.Context, rtx *Context, _ Outcome) {
		reported = [2]any{ctx.Value(ctxKey("k")), rtx.Context().Value(ctxKey("k"))}
	})
	if _, err := p.RunContext(run, []string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if reported != [2]any{"run", "run"} {
		t.Errorf("the reporter saw %v, want the run's context twice", reported)
	}
}

// The cancel a hook keeps in a handler field is called in its teardown.
type cancelHandlers struct{ called *bool }

func (h cancelHandlers) App() Handler    { return &cancelRoot{called: h.called} }
func (h cancelHandlers) AppRun() Handler { return seamHandlers{ran: new([]string)} }

type cancelRoot struct {
	NoPreRun
	NoPostRun
	called *bool
	cancel context.CancelFunc
}

func (h *cancelRoot) CascadingPreRun(ctx context.Context, rtx *Context) {
	ctx, h.cancel = context.WithCancel(ctx)
	rtx.SetContext(ctx)
}
func (*cancelRoot) Run(context.Context, *Context) {}
func (h *cancelRoot) CascadingPostRun(context.Context, *Context) {
	h.cancel()
	*h.called = true
}

func TestSetContext_cancelInAHandlerField(t *testing.T) {
	called := false
	p := NewProgram(testDef(), cancelHandlers{called: &called}).WithStdout(io.Discard).WithStderr(io.Discard)
	if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Fatalf("(%d, %v), want a clean run", code, err)
	}
	if !called {
		t.Error("the root's teardown never called the stored cancel")
	}
}

func TestSetContext_exitCauseOnADerivedContextSetsNoCode(t *testing.T) {
	steps := []LifecycleStep{
		{Name: "root", Do: func(ctx context.Context, rtx *Context) {
			c, cancel := context.WithCancelCause(ctx)
			cancel(ExitCause(3))
			rtx.SetContext(c)
		}},
		{Name: "run", Do: func(context.Context, *Context) {}},
	}
	if code, _ := ctxProgram(steps).Run([]string{"run", "x"}); code != 0 {
		t.Errorf("exit %d, want 0: only the run context's cause sets a code", code)
	}
}

func TestSetContext_inATeardownChangesNothing(t *testing.T) {
	var second any
	steps := []LifecycleStep{
		{Name: "outer", Do: func(context.Context, *Context) {}, Undo: func(ctx context.Context, _ *Context) { second = ctx.Value(ctxKey("k")) }},
		{Name: "inner", Do: func(context.Context, *Context) {}, Undo: func(ctx context.Context, rtx *Context) {
			rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "teardown"))
			if v := rtx.Context().Value(ctxKey("k")); v != nil {
				t.Errorf("SetContext in a teardown took effect: %v", v)
			}
		}},
	}
	if _, err := ctxProgram(steps).Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Errorf("the next teardown saw %v, want nothing", second)
	}
}

func TestSetContext_teardownOnlyStepAndNil(t *testing.T) {
	var undo any
	steps := []LifecycleStep{
		{Name: "set", Do: func(ctx context.Context, rtx *Context) {
			rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "set"))
			var none context.Context
			rtx.SetContext(none) //nolint:contextcheck // a nil context is ignored
		}},
		{Name: "teardown-only", Undo: func(ctx context.Context, _ *Context) { undo = ctx.Value(ctxKey("k")) }},
	}
	if _, err := ctxProgram(steps).Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if undo != "set" {
		t.Errorf("the teardown-only step saw %v, want the context current when it was reached", undo)
	}
}

func TestSetContext_unguardedMode(t *testing.T) {
	var run any
	steps := []LifecycleStep{
		{Name: "root", Do: func(ctx context.Context, rtx *Context) {
			rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "root"))
		}},
		{Name: "run", Do: func(ctx context.Context, _ *Context) { run = ctx.Value(ctxKey("k")) }},
	}
	p := ctxProgram(steps).WithPanicRecover(false).WithTeardownOnPanic(false)
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if run != "root" {
		t.Errorf("run saw %v, want root", run)
	}
}

func TestSetContext_concurrentCalls(t *testing.T) {
	steps := []LifecycleStep{{Name: "run", Do: func(ctx context.Context, rtx *Context) {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				rtx.SetContext(context.WithValue(ctx, ctxKey("k"), "g"))
				_ = rtx.Context()
			})
		}
		wg.Wait()
	}}}
	if _, err := ctxProgram(steps).Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestContext_standaloneIsNeverNil(t *testing.T) {
	if (&Context{}).Context() == nil {
		t.Error("Context() = nil on a standalone Context")
	}
}
