package rotini

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// recHandler records each lifecycle hook it runs, and optionally inspects the
// context during Run. It satisfies CommandHandlers.
type recHandler struct {
	name  string
	log   *[]string
	onRun func(rtx *Context)
}

func (h *recHandler) CascadingPreRun(_ context.Context, _ *Context)  { h.note("CascadingPreRun") }
func (h *recHandler) PreRun(_ context.Context, _ *Context)           { h.note("PreRun") }
func (h *recHandler) PostRun(_ context.Context, _ *Context)          { h.note("PostRun") }
func (h *recHandler) CascadingPostRun(_ context.Context, _ *Context) { h.note("CascadingPostRun") }
func (h *recHandler) Run(_ context.Context, rtx *Context) {
	h.note("Run")
	if h.onRun != nil {
		h.onRun(rtx)
	}
}
func (h *recHandler) note(hook string) { *h.log = append(*h.log, h.name+"."+hook) }

// testHandlers is the aggregate the runtime dispatches against by reflection.
type testHandlers struct {
	log   *[]string
	onRun func(rtx *Context)
}

func (t *testHandlers) App() CommandHandlers { return &recHandler{name: "app", log: t.log} }
func (t *testHandlers) AppRun() CommandHandlers {
	return &recHandler{name: "run", log: t.log, onRun: t.onRun}
}

func testDef() Definition {
	return Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Commands: []CommandDef{
			{
				Name:    "run",
				Handler: "AppRun",
				Aliases: []string{"r"},
				Flags:   []FlagDef{{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int"}},
				Arguments: []ArgDef{
					{Name: "name", Type: "string"},
					{Name: "rest", Type: "[]string", Variadic: true},
				},
			},
		},
	}
}

func newTestProgram(h any, args []string) (*Program, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgram(testDef(), h).WithArgs(args)
	p.stdout, p.stderr = out, errb
	return p, out, errb
}

func TestRun_lifecycleOrderAndContext(t *testing.T) {
	var log []string
	var gotChain []ResolvedCommand
	var gotArgs []string
	args := []string{"--verbose", "run", "alice", "x", "y", "--count", "3"}
	h := &testHandlers{log: &log, onRun: func(rtx *Context) {
		gotChain, gotArgs = rtx.Chain(), rtx.Args()
	}}

	p, _, errb := newTestProgram(h, args)
	if code := p.run(p.args); code != 0 {
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
	// The runtime resolves the chain + exposes raw argv; binding those into typed
	// inputs is the rtk package's job (tested there).
	if names := chainNames(gotChain); len(names) != 2 || names[0] != "app" || names[1] != "run" {
		t.Errorf("Chain() during Run = %v, want [app run]", names)
	}
	if !reflect.DeepEqual(gotArgs, args) {
		t.Errorf("Args() during Run = %v, want %v", gotArgs, args)
	}
}

func TestRun_aliasResolves(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"r", "bob"})
	if code := p.run(p.args); code != 0 {
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
	p, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"ru"}) // typo of "run"
	if code := p.run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0 (the root handler runs; it owns input errors)", code)
	}
	if !contains(log, "app.Run") {
		t.Errorf("expected the root handler to run, got %v", log)
	}
}

func TestRun_exitCodePropagates(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { rtx.Exit(5) }}
	p, _, _ := newTestProgram(h, []string{"run"})
	if code := p.run(p.args); code != 5 {
		t.Errorf("run() = %d, want 5 (handler called Exit)", code)
	}
}

func TestRun_mustGetRoutesToOnError(t *testing.T) {
	var seen error
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		// What rotini.MustGet panics on a missing service; recovered into the funnel.
		panic(&ServiceError{Key: "no-such-service"})
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.OnError(func(_ context.Context, rtx *Context, err error) {
		seen = err
		rtx.Exit(7)
	})

	if code := p.run(p.args); code != 7 {
		t.Fatalf("run() = %d, want 7 (OnError's exit code)", code)
	}
	if !errors.Is(seen, ErrServiceNotFound) {
		t.Errorf("OnError got %v, want it to wrap ErrServiceNotFound", seen)
	}
	var se *ServiceError
	if !errors.As(seen, &se) || se.Key != "no-such-service" {
		t.Errorf("OnError error did not carry the key: %v", seen)
	}
}

func TestRun_onErrorWithoutExitStillFails(t *testing.T) {
	// A funnel that classifies/logs but forgets to call rtx.Exit must not leak a
	// success code out of a panic: dispatch floors the panic path to 1.
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		panic(&ServiceError{Key: "missing"})
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.OnError(func(_ context.Context, _ *Context, _ error) {}) // no rtx.Exit
	if code := p.run(p.args); code != 1 {
		t.Errorf("run() = %d, want 1 (panic path floors to non-zero)", code)
	}
}

func TestRun_defaultOnErrorPrintsAndReturns1(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		panic("boom") // a non-error panic value is wrapped before the funnel
	}}
	p, _, errb := newTestProgram(h, []string{"run"})
	if code := p.run(p.args); code != 1 {
		t.Fatalf("run() = %d, want 1 (default OnError)", code)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("default OnError should print the error, stderr: %s", errb)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- rtx.Exit / teardown / OnError behavior (see .docs/ROTINI_RTX_EXIT.md) ---

// act is an action injected into one named hook of one command.
type act struct {
	at string // hook name, e.g. "PreRun", "Run", "CascadingPreRun"
	do func(rtx *Context)
}

// actHandler records each hook it runs and, in its one configured hook, runs the
// injected action (rtx.Exit or panic).
type actHandler struct {
	name string
	log  *[]string
	act  act
}

func (h *actHandler) hook(name string, rtx *Context) {
	*h.log = append(*h.log, h.name+"."+name)
	if name == h.act.at && h.act.do != nil {
		h.act.do(rtx)
	}
}
func (h *actHandler) CascadingPreRun(_ context.Context, rtx *Context) { h.hook("CascadingPreRun", rtx) }
func (h *actHandler) PreRun(_ context.Context, rtx *Context)          { h.hook("PreRun", rtx) }
func (h *actHandler) Run(_ context.Context, rtx *Context)             { h.hook("Run", rtx) }
func (h *actHandler) PostRun(_ context.Context, rtx *Context)         { h.hook("PostRun", rtx) }
func (h *actHandler) CascadingPostRun(_ context.Context, rtx *Context) {
	h.hook("CascadingPostRun", rtx)
}

// actProgram is the reflection-dispatched aggregate; it builds an actHandler per
// command from a name→action map.
type actProgram struct {
	log     *[]string
	actions map[string]act
}

func (p *actProgram) mk(name string) CommandHandlers {
	return &actHandler{name: name, log: p.log, act: p.actions[name]}
}
func (p *actProgram) App() CommandHandlers    { return p.mk("app") }
func (p *actProgram) AppRun() CommandHandlers { return p.mk("run") }

func runActs(t *testing.T, args []string, actions map[string]act) (int, []string) {
	t.Helper()
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: actions}, args)
	return p.run(p.args), log
}

// Exit in PreRun: Run is skipped, but PostRun (paired with PreRun) still runs.
func TestRun_exitInPreRunStillRunsPostRun(t *testing.T) {
	code, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "PreRun", do: func(rtx *Context) { rtx.Exit(1) }},
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
		"run": {at: "Run", do: func(rtx *Context) { rtx.Exit(1) }},
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

// Exit in the leaf's CascadingPreRun: PreRun never begins, so PostRun does not run
// (its pair never started), but both CascadingPostRuns do.
func TestRun_exitInLeafCascadingPreRunSkipsPostRun(t *testing.T) {
	_, log := runActs(t, []string{"run"}, map[string]act{
		"run": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.Exit(1) }},
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
		"app": {at: "CascadingPreRun", do: func(rtx *Context) { rtx.Exit(1) }},
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
		"run": {at: "Run", do: func(rtx *Context) { rtx.Exit(3) }},
		"app": {at: "CascadingPostRun", do: func(rtx *Context) { rtx.Exit(7) }},
	})
	if code != 3 {
		t.Errorf("code = %d, want 3 (first non-zero Exit wins; teardown Exit(7) ignored)", code)
	}
}

// A panic runs all begun teardown first, then funnels to OnError last.
func TestRun_panicRunsTeardownThenOnErrorLast(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "Run", do: func(*Context) { panic("boom") }},
	}}, []string{"run"})
	p.OnError(func(_ context.Context, rtx *Context, err error) {
		log = append(log, "onError:"+err.Error())
		rtx.Exit(2)
	})
	code := p.run(p.args)
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run",
		"run.PostRun", "run.CascadingPostRun", "app.CascadingPostRun",
		"onError:boom",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("teardown-then-OnError order:\n got=%v\nwant=%v", log, want)
	}
	if code != 2 {
		t.Errorf("code = %d, want 2 (OnError's exit)", code)
	}
}

// A panic inside a teardown hook is recovered: the remaining teardown still runs
// and OnError is funneled exactly once.
func TestRun_teardownPanicContinuesAndFunnelsOnce(t *testing.T) {
	var log []string
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: map[string]act{
		"run": {at: "PostRun", do: func(*Context) { panic("teardown-boom") }},
	}}, []string{"run"})
	calls := 0
	p.OnError(func(_ context.Context, rtx *Context, _ error) {
		calls++
		rtx.Exit(1)
	})
	code := p.run(p.args)
	if !contains(log, "app.CascadingPostRun") {
		t.Errorf("remaining teardown did not run after a teardown panic: %v", log)
	}
	if calls != 1 {
		t.Errorf("OnError called %d times, want 1", calls)
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}
