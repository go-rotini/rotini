package rotini

import (
	"context"
	"io"
	"sync"
	"testing"
)

// The instance-lifetime contract documented on [Handlers], pinned.
//
// It is load-bearing rather than incidental: the docs now tell authors that a field is the right
// home for state flowing between one command's own hooks, and that advice is only true because
// dispatch asks the ProgramHandlers for a handler once per frame per run and reuses that value
// for the frame's hooks. If that ever changed, generated CLIs would lose state silently between
// PreRun and Run, so it is asserted rather than described.

// ltFrame records the pointer identity of the handler serving each hook it runs.
type ltFrame struct {
	name string
	seen *[]ltSighting
	// state exists to prove a field survives from one of THIS frame's hooks to another.
	state string
}

type ltSighting struct {
	frame string // which command
	hook  string // which hook
	ptr   *ltFrame
	state string
}

func (h *ltFrame) mark(hook string) {
	*h.seen = append(*h.seen, ltSighting{frame: h.name, hook: hook, ptr: h, state: h.state})
}

func (h *ltFrame) CascadingPreRun(context.Context, *Context) {
	h.state = "from CascadingPreRun"
	h.mark("CascadingPreRun")
}
func (h *ltFrame) PreRun(context.Context, *Context) { h.state = "from PreRun"; h.mark("PreRun") }
func (h *ltFrame) Run(context.Context, *Context)    { h.mark("Run") }
func (h *ltFrame) PostRun(context.Context, *Context) {
	h.mark("PostRun")
}
func (h *ltFrame) CascadingPostRun(context.Context, *Context) { h.mark("CascadingPostRun") }

// ltFresh is what codegen emits: a new value per call.
type ltFresh struct{ seen *[]ltSighting }

func (w ltFresh) App() Handlers    { return &ltFrame{name: "app", seen: w.seen} }
func (w ltFresh) AppRun() Handlers { return &ltFrame{name: "run", seen: w.seen} }

func find(seen []ltSighting, frame, hook string) *ltSighting {
	for i := range seen {
		if seen[i].frame == frame && seen[i].hook == hook {
			return &seen[i]
		}
	}
	return nil
}

// TestHandlerLifetime_oneValuePerFrameForTheWholeRun is the fact the "use a field" advice rests
// on: every hook belonging to one command is served by the same value.
func TestHandlerLifetime_oneValuePerFrameForTheWholeRun(t *testing.T) {
	var seen []ltSighting
	p := NewProgram(testDef(), ltFresh{seen: &seen}).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}

	// The leaf's three non-cascading hooks share one value…
	pre, run, post := find(seen, "run", "PreRun"), find(seen, "run", "Run"), find(seen, "run", "PostRun")
	if pre == nil || run == nil || post == nil {
		t.Fatalf("missing leaf hooks in %v", seen)
	}
	if pre.ptr != run.ptr || run.ptr != post.ptr {
		t.Error("the leaf's PreRun, Run and PostRun were served by different handler values")
	}
	// …so a field set in PreRun is still there in Run and PostRun. This is the documented use.
	if run.state != "from PreRun" || post.state != "from PreRun" {
		t.Errorf("a field did not survive PreRun → Run → PostRun: run=%q post=%q", run.state, post.state)
	}

	// A frame's cascading pair likewise shares one value, so a teardown sees what its setup set.
	cpre, cpost := find(seen, "app", "CascadingPreRun"), find(seen, "app", "CascadingPostRun")
	if cpre == nil || cpost == nil {
		t.Fatalf("missing root cascading hooks in %v", seen)
	}
	if cpre.ptr != cpost.ptr {
		t.Error("a frame's CascadingPreRun and CascadingPostRun were served by different values")
	}
	if cpost.state != "from CascadingPreRun" {
		t.Errorf("a field did not survive CascadingPreRun → CascadingPostRun: %q", cpost.state)
	}
}

// TestHandlerLifetime_differentFramesGetDifferentValues is the limit of that advice, and the
// reason the registry exists: state travelling DOWN the chain cannot ride a field.
func TestHandlerLifetime_differentFramesGetDifferentValues(t *testing.T) {
	var seen []ltSighting
	p := NewProgram(testDef(), ltFresh{seen: &seen}).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	root, leaf := find(seen, "app", "CascadingPreRun"), find(seen, "run", "PreRun")
	if root == nil || leaf == nil {
		t.Fatalf("missing hooks in %v", seen)
	}
	if root.ptr == leaf.ptr {
		t.Error("two different commands were served by the same handler value")
	}
}

// TestHandlerLifetime_generatedWiringIsFreshPerRun pins what generated code gives you: a new
// value each run, so a field is per-run state and nothing leaks between invocations.
func TestHandlerLifetime_generatedWiringIsFreshPerRun(t *testing.T) {
	var seen []ltSighting
	p := NewProgram(testDef(), ltFresh{seen: &seen}).WithStdout(io.Discard).WithStderr(io.Discard)
	for range 2 {
		if _, err := p.Run([]string{"run", "x"}); err != nil {
			t.Fatal(err)
		}
	}

	var leafPtrs []*ltFrame
	for _, s := range seen {
		if s.frame == "run" && s.hook == "PreRun" {
			leafPtrs = append(leafPtrs, s.ptr)
		}
	}
	if len(leafPtrs) != 2 {
		t.Fatalf("expected two runs, saw %d leaf PreRuns", len(leafPtrs))
	}
	if leafPtrs[0] == leafPtrs[1] {
		t.Error("two runs shared one handler value — a field would leak state between invocations")
	}
}

// ltShared is the shape the docs warn about: a hand-written aggregate returning one value.
//
// Sharing is not itself wrong — a STATELESS handler is the common case and is fine shared — so
// this is not something the runtime can reject. It is a contract the author keeps, which is why
// the doc says to return a new value per call if your handlers keep state in fields.
type ltShared struct {
	root *ltFrame
	leaf *ltFrame
}

func (w ltShared) App() Handlers    { return w.root }
func (w ltShared) AppRun() Handlers { return w.leaf }

// TestHandlerLifetime_sharedWiringPersistsAcrossRuns demonstrates the hazard concretely: the
// runtime uses whatever the wiring method returns, so freshness is the author's to provide.
func TestHandlerLifetime_sharedWiringPersistsAcrossRuns(t *testing.T) {
	var seen []ltSighting
	w := ltShared{root: &ltFrame{name: "app", seen: &seen}, leaf: &ltFrame{name: "run", seen: &seen}}
	p := NewProgram(testDef(), w).WithStdout(io.Discard).WithStderr(io.Discard)
	for range 2 {
		if _, err := p.Run([]string{"run", "x"}); err != nil {
			t.Fatal(err)
		}
	}

	var leafPtrs []*ltFrame
	for _, s := range seen {
		if s.frame == "run" && s.hook == "PreRun" {
			leafPtrs = append(leafPtrs, s.ptr)
		}
	}
	if len(leafPtrs) != 2 || leafPtrs[0] != leafPtrs[1] {
		t.Error("shared wiring should hand the same value to both runs — the documented hazard")
	}
}

// TestHandlerLifetime_generatedWiringIsRaceFreeUnderConcurrentRuns is the claim that matters for
// a REPL or a StdioServer: with a fresh value per call, handler fields are per-run state and
// concurrent runs cannot collide. Meaningful under -race; harmless without it.
func TestHandlerLifetime_generatedWiringIsRaceFreeUnderConcurrentRuns(t *testing.T) {
	seen := make([]ltSighting, 0)
	var mu sync.Mutex
	p := NewProgram(testDef(), ltRecording{record: func(s ltSighting) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, s)
	}}).WithStdout(io.Discard).WithStderr(io.Discard)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Run([]string{"run", "x"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// ltRecording is ltFresh with a mutex-guarded sink, so the TEST's own bookkeeping is not what
// -race reports on.
type ltRecording struct{ record func(ltSighting) }

func (w ltRecording) App() Handlers    { return &ltConcurrent{name: "app", record: w.record} }
func (w ltRecording) AppRun() Handlers { return &ltConcurrent{name: "run", record: w.record} }

type ltConcurrent struct {
	name   string
	record func(ltSighting)
	state  string
}

func (h *ltConcurrent) CascadingPreRun(context.Context, *Context) { h.state = "x" }
func (h *ltConcurrent) PreRun(context.Context, *Context)          { h.state = "y" }
func (h *ltConcurrent) Run(context.Context, *Context) {
	h.record(ltSighting{frame: h.name, state: h.state})
}
func (h *ltConcurrent) PostRun(context.Context, *Context)          {}
func (h *ltConcurrent) CascadingPostRun(context.Context, *Context) {}
