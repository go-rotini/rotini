package rotini

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// runLogged runs testDef with the recording handlers and returns the hook log
// and exit code; configure customizes the program (nil for the defaults).
func runLogged(t *testing.T, args []string, configure func(*Program)) ([]string, int) {
	t.Helper()
	var log []string
	p, _, errb := newTestProgram(&testHandlers{log: &log}, args)
	if configure != nil {
		configure(p)
	}
	code, _ := p.run(p.args)
	if errb.Len() > 0 {
		t.Logf("stderr: %s", errb)
	}
	return log, code
}

// Setting the seams to their exported defaults is the identity: hook order and
// exit code match a program with the seams unset, invocation for invocation.
func TestLifecycle_explicitDefaultsAreIdentity(t *testing.T) {
	for _, args := range [][]string{
		{"run", "alice"},
		{"--verbose", "run", "alice", "x", "--count", "3"},
		{},
	} {
		plain, plainCode := runLogged(t, args, nil)
		seamed, seamedCode := runLogged(t, args, func(p *Program) {
			p.WithResolver(DefaultResolver).WithLifecycle(DefaultLifecycle)
		})
		if !reflect.DeepEqual(plain, seamed) || plainCode != seamedCode {
			t.Errorf("args %v: explicit defaults diverged:\n unset  %v (code %d)\n seamed %v (code %d)",
				args, plain, plainCode, seamed, seamedCode)
		}
	}
}

// A resolver alias: rewrite the token, delegate to the default, and return its
// resolution — routing AND parsing then agree, because Resolution.Args carries
// the rewritten vector into rtx.Args.
func TestWithResolver_alias(t *testing.T) {
	var log []string
	var gotArgs []string
	h := &testHandlers{log: &log, onRun: func(rtx *Context) { gotArgs = rtx.Args }}
	p, _, errb := newTestProgram(h, []string{"st", "alice"})
	p.WithResolver(func(def Definition, argv []string) (Resolution, error) {
		if len(argv) > 0 && argv[0] == "st" {
			argv = append([]string{"run"}, argv[1:]...)
		}
		return DefaultResolver(def, argv)
	})

	if code, _ := p.run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0 (stderr: %s)", code, errb)
	}
	if want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
		"run.CascadingPostRun", "app.CascadingPostRun",
	}; !reflect.DeepEqual(log, want) {
		t.Errorf("aliased dispatch order:\n got=%v\nwant=%v", log, want)
	}
	if want := []string{"run", "alice"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("rtx.Args = %v, want the REWRITTEN vector %v (so Parse agrees with routing)", gotArgs, want)
	}
}

// A resolver error is a wiring-class failure: routed through the funnel,
// exit floored to 1, no hooks run, and pre-classified CategoryInternal —
// unless the resolver tagged its own category, which must survive.
func TestWithResolver_error(t *testing.T) {
	var log []string
	p, _, errb := newTestProgram(&testHandlers{log: &log}, []string{"run"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, errors.New("routing table on fire")
	})
	code, err := p.run(p.args)
	// An untagged resolver error is wiring-class (internal), so the default
	// OnError exits ExitInternal (EH3 maps category → code).
	if code != ExitInternal || err == nil {
		t.Errorf("run() = (%d, %v), want (%d, the resolver error)", code, err, ExitInternal)
	}
	if !strings.Contains(errb.String(), "routing table on fire") {
		t.Errorf("stderr = %q, want the resolver error via the funnel", errb)
	}
	if len(log) != 0 {
		t.Errorf("hooks ran despite a resolver failure: %v", log)
	}
	if CategoryOf(err) != CategoryInternal {
		t.Errorf("CategoryOf = %v, want internal (an untagged resolver error is a wiring-class failure)", CategoryOf(err))
	}

	// A resolver that classifies its own error keeps that classification.
	p2, _, _ := newTestProgram(&testHandlers{log: &log}, []string{"run"})
	p2.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, UsageError(errors.New("bad token"))
	})
	_, err2 := p2.run(p2.args)
	if CategoryOf(err2) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage — the resolver's own tag must survive", CategoryOf(err2))
	}
}

// The pre-classified diagnostics make the documented one-switch funnel work:
// a custom funnel maps CategoryInternal to ExitInternal with no taxonomy
// re-derivation of its own.
func TestWithOnErrorFn_categorySwitch(t *testing.T) {
	def := Definition{Name: "app", Handler: "Nope"} // no such handler method: a wiring failure
	p, _, _ := newTestProgram(&testHandlers{log: &[]string{}}, nil)
	p.def = def
	p.WithOnErrorFn(func(_ context.Context, rtx *Context, err error) {
		switch CategoryOf(err) {
		case CategoryUsage:
			rtx.SignalExit(ExitUsage)
		case CategoryInternal:
			rtx.SignalExit(ExitInternal)
		default:
			rtx.SignalExit(1)
		}
	})
	if code, _ := p.run(p.args); code != ExitInternal {
		t.Errorf("run() = %d, want %d via the category switch", code, ExitInternal)
	}
}

// EH6: a Definition↔handlers mismatch is a typed, As-able *WiringError naming
// the command and method, and always CategoryInternal.
func TestRun_wiringError(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: &[]string{}}, nil)
	p.def = Definition{Name: "app", Handler: "Nope"} // no such handler method
	_, err := p.run(p.args)
	var we *WiringError
	if !errors.As(err, &we) {
		t.Fatalf("err is not a *WiringError: %T (%v)", err, err)
	}
	if we.Command != "app" || we.Handler != "Nope" {
		t.Errorf("WiringError = {Command:%q Handler:%q}, want {app Nope}", we.Command, we.Handler)
	}
	if CategoryOf(err) != CategoryInternal {
		t.Errorf("CategoryOf = %v, want internal", CategoryOf(err))
	}
}

// An empty chain is a resolver bug, reported loudly — never a silent no-op.
func TestWithResolver_emptyChain(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: &[]string{}}, nil)
	p.WithResolver(func(Definition, []string) (Resolution, error) { return Resolution{}, nil })
	// An empty chain is a resolver bug (internal) → default exits ExitInternal.
	if code, err := p.run(p.args); code != ExitInternal || err == nil {
		t.Errorf("run() = (%d, %v), want (%d, an empty-chain error)", code, err, ExitInternal)
	}
	if !strings.Contains(errb.String(), "empty chain") {
		t.Errorf("stderr = %q, want the empty-chain diagnostic", errb)
	}
}

// A custom resolution may divert to a remote dispatch, exactly like a declared
// remote command; a missing binary follows the normal remote error path.
func TestWithResolver_customRemote(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: &[]string{}}, []string{"anything"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{Remote: &RemoteDispatch{Def: RemoteDef{Name: "ghost", Binary: "rotini-test-no-such-binary"}}}, nil
	})
	// Not flagged Discovered → a declared remote → missing binary is internal,
	// so the default OnError exits ExitInternal (EH3 maps category → code).
	if code, _ := p.run(p.args); code != ExitInternal {
		t.Errorf("run() = %d, want %d for an unresolvable remote binary", code, ExitInternal)
	}
	if errb.Len() == 0 {
		t.Error("stderr empty, want the remote resolution error")
	}
}

// A custom lifecycle reorders teardown: swapping the cascading Undo pairing
// makes CascadingPostRun unwind root→leaf instead of the default leaf→root.
// The engine's contract holds around the custom plan: PostRun still pairs with
// PreRun, and unwind still covers exactly the begun steps.
func TestWithLifecycle_reversedTeardown(t *testing.T) {
	reversed := func(chain []ResolvedCommand, hs []CommandHandlers) []LifecycleStep {
		steps := DefaultLifecycle(chain, hs)
		for i, j := 0, len(hs)-1; i < j; i, j = i+1, j-1 {
			steps[i].Undo, steps[j].Undo = steps[j].Undo, steps[i].Undo
		}
		return steps
	}

	log, code := runLogged(t, []string{"run", "alice"}, func(p *Program) { p.WithLifecycle(reversed) })
	if code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun", "run.Run", "run.PostRun",
		"app.CascadingPostRun", "run.CascadingPostRun", // root→leaf: the reversal
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("reversed teardown order:\n got=%v\nwant=%v", log, want)
	}
}

// haltHandlers panics in the leaf's PreRun, to prove the engine's balanced
// unwind holds for a custom plan too.
type haltHandlers struct{ log *[]string }

func (t *haltHandlers) App() CommandHandlers { return &recHandler{name: "app", log: t.log} }
func (t *haltHandlers) AppRun() CommandHandlers {
	return &panicPreRunHandler{recHandler{name: "run", log: t.log}}
}

type panicPreRunHandler struct{ recHandler }

func (h *panicPreRunHandler) PreRun(context.Context, *Context) {
	h.note("PreRun")
	panic(errors.New("setup failed"))
}

func TestWithLifecycle_customPlanKeepsUnwindContract(t *testing.T) {
	var log []string
	p, _, errb := newTestProgram(&haltHandlers{log: &log}, []string{"run"})
	p.WithLifecycle(DefaultLifecycle) // explicitly seamed; the engine owns halting/unwind
	code, err := p.run(p.args)
	if code != 1 || err == nil {
		t.Fatalf("run() = (%d, %v), want (1, the panic)", code, err)
	}
	want := []string{
		"app.CascadingPreRun", "run.CascadingPreRun",
		"run.PreRun",  // panics: Run never starts
		"run.PostRun", // its pair began → unwinds
		"run.CascadingPostRun", "app.CascadingPostRun",
	}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("unwind after a PreRun panic:\n got=%v\nwant=%v", log, want)
	}
	if !strings.Contains(errb.String(), "setup failed") {
		t.Errorf("stderr = %q, want the funneled panic, after teardown", errb)
	}
}

// panicValueHandlers panics in the leaf's Run with an arbitrary value, to
// exercise the PanicError contract across value shapes.
type panicValueHandlers struct {
	log *[]string
	val any
}

func (t *panicValueHandlers) App() CommandHandlers { return &recHandler{name: "app", log: t.log} }
func (t *panicValueHandlers) AppRun() CommandHandlers {
	return &panicRunHandler{recHandler{name: "run", log: t.log}, t.val}
}

type panicRunHandler struct {
	recHandler
	val any
}

func (h *panicRunHandler) Run(context.Context, *Context) { panic(h.val) }

// A recovered panic reaches the funnel as a *PanicError: the recovery-point
// stack rides along, Error() stays the panicked value alone (the default
// funnel's one-line output is pinned by the unwind test above), and a panicked
// error value keeps its sentinels and category tags through Unwrap.
func TestPanicError(t *testing.T) {
	// capture runs argv against handlers and returns what the funnel was handed.
	capture := func(t *testing.T, h any) error {
		t.Helper()
		var got error
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithOnErrorFn(func(_ context.Context, rtx *Context, err error) { got = err })
		if code, _ := p.run(p.args); code != 1 {
			t.Fatalf("run() = %d, want the panic path's floored 1", code)
		}
		return got
	}

	t.Run("error value: stack + message + unwrap", func(t *testing.T) {
		var log []string
		got := capture(t, &haltHandlers{log: &log})
		var pe *PanicError
		if !errors.As(got, &pe) {
			t.Fatalf("funneled %T, want a *PanicError", got)
		}
		if pe.Error() != "setup failed" {
			t.Errorf("Error() = %q, want the panicked message verbatim", pe.Error())
		}
		if !strings.Contains(string(pe.Stack), "PreRun") {
			t.Error("Stack does not name the panicking hook")
		}
		if !strings.Contains(string(pe.Stack), "panicPreRunHandler") {
			t.Error("Stack does not name the panicking type")
		}
	})

	t.Run("tagged error: category survives the wrapping", func(t *testing.T) {
		var log []string
		got := capture(t, &panicValueHandlers{log: &log, val: UsageError(errors.New("bad input"))})
		if CategoryOf(got) != CategoryUsage {
			t.Errorf("CategoryOf = %v, want usage — the tag must survive PanicError", CategoryOf(got))
		}
	})

	t.Run("non-error value: rendered, nothing to unwrap", func(t *testing.T) {
		var log []string
		got := capture(t, &panicValueHandlers{log: &log, val: 42})
		var pe *PanicError
		if !errors.As(got, &pe) {
			t.Fatalf("funneled %T, want a *PanicError", got)
		}
		if pe.Error() != "42" {
			t.Errorf("Error() = %q, want \"42\"", pe.Error())
		}
		if pe.Unwrap() != nil {
			t.Errorf("Unwrap() = %v, want nil for a non-error value", pe.Unwrap())
		}
	})
}

// ── documented examples ──────────────────────────────────────────────────────

// exHandlers is the example programs' handler set: every hook prints itself.
type exHandlers struct{}

type exHook struct{ name string }

func (h exHook) CascadingPreRun(context.Context, *Context) { fmt.Println(h.name + ".CascadingPreRun") }
func (h exHook) PreRun(context.Context, *Context)          { fmt.Println(h.name + ".PreRun") }
func (h exHook) Run(context.Context, *Context)             { fmt.Println(h.name + ".Run") }
func (h exHook) PostRun(context.Context, *Context)         { fmt.Println(h.name + ".PostRun") }
func (h exHook) CascadingPostRun(context.Context, *Context) {
	fmt.Println(h.name + ".CascadingPostRun")
}

func (exHandlers) App() CommandHandlers       { return exHook{name: "app"} }
func (exHandlers) AppStatus() CommandHandlers { return exHook{name: "status"} }

// exampleDef is a root with one "status" sub-command.
func exampleDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "status", Handler: "AppStatus"}},
	}
}

// A resolver that teaches the program a routing alias: "st" rewrites to
// "status" and delegates to [DefaultResolver], so routing and (later) parsing
// agree on the rewritten argv. Declare real aliases in the spec when you also
// want completion; a resolver alias is routing-only.
func ExampleProgram_WithResolver() {
	NewProgram(exampleDef(), exHandlers{}).
		WithArgs([]string{"st"}).
		WithExit(func(int) {}).
		WithResolver(func(def Definition, argv []string) (Resolution, error) {
			if len(argv) > 0 && argv[0] == "st" {
				argv = append([]string{"status"}, argv[1:]...)
			}
			return DefaultResolver(def, argv)
		}).
		Execute()
	// Output:
	// app.CascadingPreRun
	// status.CascadingPreRun
	// status.PreRun
	// status.Run
	// status.PostRun
	// status.CascadingPostRun
	// app.CascadingPostRun
}

// A lifecycle that reverses teardown order: wrapping [DefaultLifecycle] and
// swapping the cascading pairs makes CascadingPostRun unwind root→leaf. Only
// the plan changes — halting, balanced unwind, and the panic funnel stay
// rotini's.
func ExampleProgram_WithLifecycle() {
	NewProgram(exampleDef(), exHandlers{}).
		WithArgs([]string{"status"}).
		WithExit(func(int) {}).
		WithLifecycle(func(chain []ResolvedCommand, hs []CommandHandlers) []LifecycleStep {
			steps := DefaultLifecycle(chain, hs)
			for i, j := 0, len(hs)-1; i < j; i, j = i+1, j-1 {
				steps[i].Undo, steps[j].Undo = steps[j].Undo, steps[i].Undo
			}
			return steps
		}).
		Execute()
	// Output:
	// app.CascadingPreRun
	// status.CascadingPreRun
	// status.PreRun
	// status.Run
	// status.PostRun
	// app.CascadingPostRun
	// status.CascadingPostRun
}
