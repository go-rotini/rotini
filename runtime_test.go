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
// context during Run. It satisfies Handlers.
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

func (t *testHandlers) App() Handlers { return &recHandler{name: "app", log: t.log} }
func (t *testHandlers) AppRun() Handlers {
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
	// The runtime resolves the chain + exposes raw argv; binding those into typed
	// inputs is the rtk package's job (tested there).
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
	p, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"ru"}) // typo of "run"
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
		// What rotini.MustGet panics on a missing service; recovered → the funnel's panics.
		panic(&ServiceError{Key: "no-such-service"})
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, _ []error, panics []*PanicError) {
		seen = panics[0]
		rtx.Exit(7)
	})

	code, err := p.Run(p.args)
	if code != 7 {
		t.Fatalf("run() = %d, want 7 (the funnel's exit code)", code)
	}
	// The recovered panic is also returned to the caller (the run err).
	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("run returned err = %v, want it to wrap ErrServiceNotFound", err)
	}
	// The funnel gets the *PanicError; its Value carries the panicked *ServiceError.
	if !errors.Is(seen, ErrServiceNotFound) {
		t.Errorf("funnel got %v, want it to wrap ErrServiceNotFound", seen)
	}
	var se *ServiceError
	if !errors.As(seen, &se) || se.Key != "no-such-service" {
		t.Errorf("funnel error did not carry the key: %v", seen)
	}
}

func TestRun_defaultFunnelFaultFloorsTo1(t *testing.T) {
	// With the DEFAULT funnel (none wired), a recovered fault floors the exit to 1 — a
	// panic never leaks a success code. (A CUSTOM funnel may mask it; see
	// TestRun_faultExit_defaultFloorsButFunnelCanMask.)
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
		panic("boom") // a non-error panic value is wrapped before the funnel
	}}
	p, _, errb := newTestProgram(h, []string{"run"})
	code, err := p.Run(p.args)
	if code != 1 {
		t.Fatalf("run() = %d, want %d (default OnPanic)", code, 1)
	}
	// A non-error panic value is wrapped and returned to the caller.
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("run returned err = %v, want it to carry %q", err, "boom")
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("default OnPanic should print the panic, stderr: %s", errb)
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

func (p *actProgram) mk(name string) Handlers {
	return &actHandler{name: name, log: p.log, act: p.actions[name]}
}
func (p *actProgram) App() Handlers    { return p.mk("app") }
func (p *actProgram) AppRun() Handlers { return p.mk("run") }

func runActs(t *testing.T, args []string, actions map[string]act) (int, []string) {
	t.Helper()
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: actions}, args)
	code, _ := p.Run(p.args)
	return code, log
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
	p.WithFunnel(func(_ context.Context, _ *Context, _, _ []string, _, _ []error, panics []*PanicError) {
		seen = panics[0]
	})

	code, err := p.Run(p.args)

	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun", // CascadingPostRuns skipped by the hard Exit
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

// panicThenHardExit is a leaf whose Run panics and whose PostRun then hard-Exits(3),
// to exercise the panic-funnel-vs-hard-Exit interaction.
type panicThenHardExit struct{ log *[]string }

func (p *panicThenHardExit) App() Handlers { return &recHandler{name: "app", log: p.log} }
func (p *panicThenHardExit) AppRun() Handlers {
	return &panicThenHardExitLeaf{log: p.log}
}

type panicThenHardExitLeaf struct{ log *[]string }

func (h *panicThenHardExitLeaf) note(n string)                             { *h.log = append(*h.log, "run."+n) }
func (h *panicThenHardExitLeaf) CascadingPreRun(context.Context, *Context) { h.note("CascadingPreRun") }
func (h *panicThenHardExitLeaf) PreRun(context.Context, *Context)          { h.note("PreRun") }
func (h *panicThenHardExitLeaf) Run(context.Context, *Context)             { h.note("Run"); panic("boom") }
func (h *panicThenHardExitLeaf) PostRun(_ context.Context, rtx *Context) {
	h.note("PostRun")
	rtx.Exit(3)
}
func (h *panicThenHardExitLeaf) CascadingPostRun(context.Context, *Context) {
	h.note("CascadingPostRun")
}

func errorContains(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
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
	p.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, _ []error, panics []*PanicError) {
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
	p.WithFunnel(func(_ context.Context, rtx *Context, _, _ []string, _, _ []error, _ []*PanicError) {
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
