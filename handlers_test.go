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
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
	ran bool
}

func (o *onlyRun) Run(ctx context.Context, rtx *Context) { o.ran = true }

// granular embeds three no-ops and defines CascadingPreRun and Run itself.
type granular struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
	preRan bool
}

func (g *granular) CascadingPreRun(ctx context.Context, rtx *Context) { g.preRan = true }
func (g *granular) Run(ctx context.Context, rtx *Context)             {}

// Both shapes satisfy Handler; one without Run would not compile.
var (
	_ Handler = (*onlyRun)(nil)
	_ Handler = (*granular)(nil)
)

func TestDefaultEmbeds_bundleSatisfiesInterfaceAndNoOps(t *testing.T) {
	h := &onlyRun{}
	var iface Handler = h

	// The four embedded hooks are no-ops, even with a nil rtx.
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
	var iface Handler = g

	// The explicitly defined CascadingPreRun is used.
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
	var iface Handler = &overrider{}
	iface.PreRun(context.Background(), nil)
	if !overriderPreRan {
		t.Error("explicit PreRun did not shadow the embedded NoPreRun")
	}
	overriderPreRan = false
}

type overrider struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}

var overriderPreRan bool

func (*overrider) PreRun(ctx context.Context, rtx *Context) { overriderPreRan = true }
func (*overrider) Run(ctx context.Context, rtx *Context)    {}

// dhHandlers is a hand-written handler embedding NoHooks and defining only Run.
type dhHandlers struct {
	NoHooks
	ran *bool
}

func (h *dhHandlers) Run(_ context.Context, rtx *Context) { *h.ran = true }

type dhProgram struct{ ran *bool }

func (p dhProgram) App() Handler    { return &dhHandlers{ran: p.ran} }
func (p dhProgram) AppRun() Handler { return &dhHandlers{ran: p.ran} }

// TestNoHooks_satisfiesHandlerWithOnlyRun: NoHooks plus Run is a complete Handler through
// two levels of promotion.
func TestNoHooks_satisfiesHandlerWithOnlyRun(t *testing.T) {
	var ran bool
	p := NewProgram(testDef(), dhProgram{ran: &ran}).WithStdout(io.Discard).WithStderr(io.Discard)
	if code, err := p.Run([]string{"run", "x"}); err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	if !ran {
		t.Error("the handler's Run never fired")
	}
}

// TestNoHooks_isTheSameFourNoOps pins that NoHooks adds no behavior beyond the four no-ops.
func TestNoHooks_isTheSameFourNoOps(t *testing.T) {
	var h NoHooks
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
		t.Error("a NoHooks hook recorded something")
	}
}

// TestNoHooks_ownMethodWins: a method on the outer type shadows the promoted no-op.
func TestNoHooks_ownMethodWins(t *testing.T) {
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

func (p dhOverride) App() Handler    { return &dhOverrideLeaf{log: p.log} }
func (p dhOverride) AppRun() Handler { return &dhOverrideLeaf{log: p.log} }

type dhOverrideLeaf struct {
	NoHooks
	log *[]string
}

func (h *dhOverrideLeaf) PreRun(context.Context, *Context) { *h.log = append(*h.log, "PreRun") }
func (h *dhOverrideLeaf) Run(context.Context, *Context)    { *h.log = append(*h.log, "Run") }

// TestNoHooks_stillRequiresRunAtCompileTime pins that NoHooks without Run does not satisfy
// Handler. A compile failure cannot be asserted in-process, so the program is compiled out of
// process. `rotini generate`'s hook audit relies on this to skip Run.
func TestNoHooks_stillRequiresRunAtCompileTime(t *testing.T) {
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

// NoHooks supplies the four no-ops. Run is NOT among them, so this must not compile.
type handlers struct{ rotini.NoHooks }

var _ rotini.Handler = (*handlers)(nil)

func main() {}
`)

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("a handler embedding NoHooks with no Run compiled — the compile-time guarantee is gone")
	}
	if !strings.Contains(string(out), "missing method Run") {
		t.Errorf("compile failed, but not by naming the missing Run:\n%s", out)
	}
}

// ── handler instance lifetime ───────────────────────────────────────────────.
//
// Dispatch asks the handler set once per command per run and reuses that value for the
// command's hooks, which is what makes a field valid state between those hooks.

// ltFrame records the pointer identity of the handler serving each hook it runs.
type ltFrame struct {
	name string
	seen *[]ltSighting
	// state shows a field surviving between this command's hooks.
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

func (w ltFresh) App() Handler    { return &ltFrame{name: "app", seen: w.seen} }
func (w ltFresh) AppRun() Handler { return &ltFrame{name: "run", seen: w.seen} }

func find(seen []ltSighting, frame, hook string) *ltSighting {
	for i := range seen {
		if seen[i].frame == frame && seen[i].hook == hook {
			return &seen[i]
		}
	}
	return nil
}

// TestHandlerLifetime_oneValuePerFrameForTheWholeRun: every hook of one command is served by
// the same value.
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
	// …so a field set in PreRun is still there in Run and PostRun.
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

// TestHandlerLifetime_differentFramesGetDifferentValues: different commands get different
// values, so a field cannot carry state down the chain.
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

// TestHandlerLifetime_generatedWiringIsFreshPerRun: generated-style wiring yields a new value
// each run, so fields do not carry over between runs.
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

// ltShared is a hand-written handler set that returns one shared value on every call.
type ltShared struct {
	root *ltFrame
	leaf *ltFrame
}

func (w ltShared) App() Handler    { return w.root }
func (w ltShared) AppRun() Handler { return w.leaf }

// TestHandlerLifetime_sharedWiringPersistsAcrossRuns: the runtime uses whatever the wiring
// method returns, so a shared value's fields persist across runs.
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

// TestHandlerLifetime_generatedWiringIsRaceFreeUnderConcurrentRuns: with a fresh value per call,
// concurrent runs do not share handler fields. Meaningful under -race.
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
		wg.Go(func() {
			if _, err := p.Run([]string{"run", "x"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

// ltRecording is ltFresh with a mutex-guarded sink, so the test's own bookkeeping is race-free.
type ltRecording struct{ record func(ltSighting) }

func (w ltRecording) App() Handler    { return &ltConcurrent{name: "app", record: w.record} }
func (w ltRecording) AppRun() Handler { return &ltConcurrent{name: "run", record: w.record} }

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

// TestDefaultEmbedsAreNoOps pins that the four No* hooks do nothing, including with a nil
// context.
func TestDefaultEmbedsAreNoOps(t *testing.T) {
	var h struct {
		NoCascadingPreRun
		NoPreRun
		NoPostRun
		NoCascadingPostRun
	}
	ctx, rtx := context.Background(), newContext()
	h.CascadingPreRun(ctx, rtx)
	h.PreRun(ctx, rtx)
	h.PostRun(ctx, rtx)
	h.CascadingPostRun(ctx, rtx)

	// A no-op records nothing.
	if !(Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}).Empty() {
		t.Error("a default hook recorded something")
	}

	// They are callable with a nil context.
	h.PreRun(ctx, nil)
	h.PostRun(ctx, nil)
}
