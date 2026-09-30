package rotini

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// onlyRun is the common case: embed all four non-Run defaults, supply only Run.
type onlyRun struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	ran bool
}

func (o *onlyRun) Run(ctx context.Context, rtx *Context) { o.ran = true }

// granular embeds individual defaults and overrides one hook (CascadingPreRun) plus
// the mandatory Run — proving the defaults mix and match and that an explicit hook
// shadows a default it does not embed.
type granular struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	preRan bool
}

func (g *granular) CascadingPreRun(ctx context.Context, rtx *Context) { g.preRan = true }
func (g *granular) Run(ctx context.Context, rtx *Context)             {}

// Compile-time proof that both shapes satisfy the lifecycle interface. (A handler that
// embedded the defaults but omitted Run would fail to compile here — the guarantee
// that Run is mandatory.)
var (
	_ Handlers = (*onlyRun)(nil)
	_ Handlers = (*granular)(nil)
)

func TestDefaultEmbeds_bundleSatisfiesInterfaceAndNoOps(t *testing.T) {
	h := &onlyRun{}
	var iface Handlers = h

	// The four embedded hooks run as harmless no-ops (nil rtx is fine — they ignore it).
	iface.CascadingPreRun(context.Background(), nil)
	iface.PreRun(context.Background(), nil)
	iface.PostRun(context.Background(), nil)
	iface.CascadingPostRun(context.Background(), nil)
	iface.Run(context.Background(), nil)

	if !h.ran {
		t.Error("Run was not invoked through the interface")
	}
}

func TestDefaultEmbeds_granularAndOverride(t *testing.T) {
	g := &granular{}
	var iface Handlers = g

	// The explicitly-defined CascadingPreRun is used (not a default — none was embedded).
	iface.CascadingPreRun(context.Background(), nil)
	if !g.preRan {
		t.Error("explicit CascadingPreRun did not run")
	}

	// The embedded defaults still no-op without panic.
	iface.PreRun(context.Background(), nil)
	iface.PostRun(context.Background(), nil)
	iface.CascadingPostRun(context.Background(), nil)
}

func TestDefaultEmbeds_overrideShadowsDefault(t *testing.T) {
	// Embedding the defaults but defining a hook explicitly: the explicit one wins.
	var iface Handlers = &overrider{}
	iface.PreRun(context.Background(), nil)
	if !overriderPreRan {
		t.Error("explicit PreRun did not shadow the embedded DefaultPreRun")
	}
	overriderPreRan = false
}

type overrider struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}

var overriderPreRan bool

func (*overrider) PreRun(ctx context.Context, rtx *Context) { overriderPreRan = true }
func (*overrider) Run(ctx context.Context, rtx *Context)    {}

// dhHandlers is the shape DefaultHooks exists for: a handler written by hand, behind a spec's
// `handler: {import, convention}`, with only the hook it actually implements written out.
type dhHandlers struct {
	DefaultHooks
	ran *bool
}

func (h *dhHandlers) Run(_ context.Context, rtx *Context) { *h.ran = true }

type dhProgram struct{ ran *bool }

func (p dhProgram) App() Handlers    { return &dhHandlers{ran: p.ran} }
func (p dhProgram) AppRun() Handlers { return &dhHandlers{ran: p.ran} }

// TestDefaultHooks_satisfiesHandlersWithOnlyRun is the whole point: one embed plus Run, and the
// promotion through two levels still produces a complete Handlers.
func TestDefaultHooks_satisfiesHandlersWithOnlyRun(t *testing.T) {
	var ran bool
	p := NewProgram(testDef(), dhProgram{ran: &ran}).WithStdout(io.Discard).WithStderr(io.Discard)
	if code, err := p.Run([]string{"run", "x"}); err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	if !ran {
		t.Error("the handler's Run never fired")
	}
}

// TestDefaultHooks_isTheSameFourNoOps pins that it adds no behaviour of its own — it is an
// assembly of the existing embeds, not a new kind of hook.
func TestDefaultHooks_isTheSameFourNoOps(t *testing.T) {
	var h DefaultHooks
	ctx, rtx := context.Background(), newContext()
	h.CascadingPreRun(ctx, rtx)
	h.PreRun(ctx, rtx)
	h.PostRun(ctx, rtx)
	h.CascadingPostRun(ctx, rtx)

	if !(Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}).Empty() {
		t.Error("a DefaultHooks hook recorded something")
	}
}

// TestDefaultHooks_ownMethodWins: embedding it must not make implementing a hook any different
// from before — a method on the outer type shadows the promoted no-op.
func TestDefaultHooks_ownMethodWins(t *testing.T) {
	log := []string{}
	p := NewProgram(testDef(), dhOverride{log: &log}).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if !contains(log, "PreRun") || !contains(log, "Run") {
		t.Errorf("hooks run = %v, want the declared PreRun to have shadowed the embedded no-op", log)
	}
}

type dhOverride struct{ log *[]string }

func (p dhOverride) App() Handlers    { return &dhOverrideLeaf{log: p.log} }
func (p dhOverride) AppRun() Handlers { return &dhOverrideLeaf{log: p.log} }

type dhOverrideLeaf struct {
	DefaultHooks
	log *[]string
}

func (h *dhOverrideLeaf) PreRun(context.Context, *Context) { *h.log = append(*h.log, "PreRun") }
func (h *dhOverrideLeaf) Run(context.Context, *Context)    { *h.log = append(*h.log, "Run") }

// TestDefaultHooks_stillRequiresRunAtCompileTime is the property that must survive collapsing
// the embeds, and it cannot be asserted from inside a passing test binary — a program that does
// not compile cannot be linked into this one. So it is compiled out of process.
//
// Without it, a handler could embed DefaultHooks, forget Run entirely, satisfy Handlers, and do
// nothing at runtime. The whole reason `rotini generate`'s hook audit skips Run is that the
// compiler owns that case.
func TestDefaultHooks_stillRequiresRunAtCompileTime(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a throwaway package")
	}
	dir := t.TempDir()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module dhcheck\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+root+"\n")
	write("main.go", `package main

import "github.com/go-rotini/rotini"

// DefaultHooks supplies the four no-ops. Run is NOT among them, so this must not compile.
type handlers struct{ rotini.DefaultHooks }

var _ rotini.Handlers = (*handlers)(nil)

func main() {}
`)

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("a handler embedding DefaultHooks with no Run compiled — the compile-time guarantee is gone")
	}
	if !strings.Contains(string(out), "missing method Run") {
		t.Errorf("compile failed, but not by naming the missing Run:\n%s", out)
	}
}

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
// a REPL or a concurrent host: with a fresh value per call, handler fields are per-run state and
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

// TestDefaultEmbedsAreNoOps executes the four embeddable defaults. Every generated stub embeds
// them, so "does nothing, safely, with a nil context" is a real contract.
func TestDefaultEmbedsAreNoOps(t *testing.T) {
	var h struct {
		DefaultCascadingPreRun
		DefaultPreRun
		DefaultPostRun
		DefaultCascadingPostRun
	}
	ctx, rtx := context.Background(), newContext()
	h.CascadingPreRun(ctx, rtx)
	h.PreRun(ctx, rtx)
	h.PostRun(ctx, rtx)
	h.CascadingPostRun(ctx, rtx)

	// A no-op must not have recorded anything, or an "empty" handler would not settle
	// silently.
	if !(Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}).Empty() {
		t.Error("a default hook recorded something")
	}

	// They also have to be callable with a nil context — the defaults are reached through
	// an interface a user may drive directly in a test.
	h.PreRun(ctx, nil)
	h.PostRun(ctx, nil)
}
