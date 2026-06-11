package rotini

import (
	"bytes"
	"context"
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
