package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	code, _ := p.Run(p.args)
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

// A resolver alias that rewrites the token and delegates to the default: Resolution.Argv
// carries the rewritten vector into rtx.Argv, so routing and parsing agree.
func TestWithResolver_alias(t *testing.T) {
	var log []string
	var gotArgs []string
	h := &testHandlers{log: &log, onRun: func(rtx *Context) { gotArgs = rtx.Argv }}
	p, _, errb := newTestProgram(h, []string{"st", "alice"})
	p.WithResolver(func(def Definition, argv []string) (Resolution, error) {
		if len(argv) > 0 && argv[0] == "st" {
			argv = append([]string{"run"}, argv[1:]...)
		}
		return DefaultResolver(def, argv)
	})

	if code, _ := p.Run(p.args); code != 0 {
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
		t.Errorf("rtx.Argv = %v, want the REWRITTEN vector %v (so Parse agrees with routing)", gotArgs, want)
	}
}

// A resolver error runs no hooks, exits 1, and is CategoryInternal unless the resolver
// tagged its own category.
func TestWithResolver_error(t *testing.T) {
	var log []string
	p, _, errb := newTestProgram(&testHandlers{log: &log}, []string{"run"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{}, errors.New("routing table on fire")
	})
	code, err := p.Run(p.args)
	// Reported as a panic; the default exits 1 and the returned err is internal.
	if code != 1 || err == nil {
		t.Errorf("run() = (%d, %v), want (1, the resolver error)", code, err)
	}
	if !strings.Contains(errb.String(), "routing table on fire") {
		t.Errorf("stderr = %q, want the resolver error via the reporter", errb)
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
	_, err2 := p2.Run(p2.args)
	if CategoryOf(err2) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage — the resolver's own tag must survive", CategoryOf(err2))
	}
}

// A custom reporter can map categories to exit codes with one switch over CategoryOf.
func TestWithReporter_categorySwitch(t *testing.T) {
	// Map CategoryInternal → 70.
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		rtx.RecordError(InternalError(errors.New("boom")))
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithReporter(func(_ context.Context, rtx *Context, out Outcome) {
		errs := out.Errors
		switch CategoryOf(errors.Join(errs...)) {
		case CategoryUsage:
			rtx.Exit(1)
		case CategoryInternal:
			rtx.Exit(70)
		default:
			rtx.Exit(1)
		}
	})
	if code, _ := p.Run(p.args); code != 70 {
		t.Errorf("run() = %d, want %d via the category switch", code, 70)
	}
}

// A Definition/handlers mismatch is a *WiringError naming the command and method, always
// CategoryInternal.
func TestRun_wiringError(t *testing.T) {
	p, _, _ := newTestProgram(&testHandlers{log: &[]string{}}, nil)
	p.def = Definition{Name: "app", Handler: "Nope"} // no such handler method
	_, err := p.Run(p.args)
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

// An empty chain from a resolver is reported as a fault.
func TestWithResolver_emptyChain(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: &[]string{}}, nil)
	p.WithResolver(func(Definition, []string) (Resolution, error) { return Resolution{}, nil })
	// Reported as a panic; the default exits 1.
	if code, err := p.Run(p.args); code != 1 || err == nil {
		t.Errorf("run() = (%d, %v), want (1, an empty-chain error)", code, err)
	}
	if !strings.Contains(errb.String(), "empty chain") {
		t.Errorf("stderr = %q, want the empty-chain diagnostic", errb)
	}
}

// A custom resolution may divert to a plugin dispatch; a missing binary follows the normal
// plugin error path.
func TestWithResolver_customPlugin(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: &[]string{}}, []string{"anything"})
	p.WithResolver(func(Definition, []string) (Resolution, error) {
		return Resolution{Plugin: &PluginDispatch{Def: PluginDef{Name: "ghost", Binary: "rotini-test-no-such-binary"}}}, nil
	})
	// Not Discovered, so a declared plugin: the missing binary is recorded as an error and
	// the default exits 1.
	if code, _ := p.Run(p.args); code != 1 {
		t.Errorf("run() = %d, want 1 for an unresolvable plugin binary", code)
	}
	if errb.Len() == 0 {
		t.Error("stderr empty, want the plugin resolution error")
	}
}

// A custom lifecycle reorders teardown: swapping the cascading Undo pairing
// makes CascadingPostRun unwind root→leaf instead of the default leaf→root.
// The engine's contract holds around the custom plan: PostRun still pairs with
// PreRun, and unwind still covers exactly the begun steps.
func TestWithLifecycle_reversedTeardown(t *testing.T) {
	reversed := func(chain []Command, hs []Handler) []LifecycleStep {
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

// haltHandlers panics in the leaf's PreRun, to show the unwind holds for a custom plan.
type haltHandlers struct{ log *[]string }

func (t *haltHandlers) App() Handler { return &recHandler{name: "app", log: t.log} }
func (t *haltHandlers) AppRun() Handler {
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
	code, err := p.Run(p.args)
	if code != 1 || err == nil {
		t.Fatalf("run() = (%d, %v), want (1, the panic via the reporter)", code, err)
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
		t.Errorf("stderr = %q, want the reported panic, after teardown", errb)
	}
}

// panicValueHandlers panics in the leaf's Run with an arbitrary value, to
// exercise the PanicError contract across value shapes.
type panicValueHandlers struct {
	log *[]string
	val any
}

func (t *panicValueHandlers) App() Handler { return &recHandler{name: "app", log: t.log} }
func (t *panicValueHandlers) AppRun() Handler {
	return &panicRunHandler{recHandler{name: "run", log: t.log}, t.val}
}

type panicRunHandler struct {
	recHandler
	val any
}

func (h *panicRunHandler) Run(context.Context, *Context) { panic(h.val) }

// A recovered panic reaches the reporter as a *PanicError carrying the stack; Error() is the
// panic value alone, and a panicked error keeps its sentinels and category through Unwrap.
func TestPanicError(t *testing.T) {
	// capture runs argv against handlers and returns the *PanicError the reporter was handed.
	capture := func(t *testing.T, h any) error {
		t.Helper()
		var got error
		p, _, _ := newTestProgram(h, []string{"run"})
		p.WithReporter(func(_ context.Context, rtx *Context, out Outcome) {
			panics := out.Panics
			got = panics[0]
			rtx.Exit(1)
		})
		if code, _ := p.Run(p.args); code != 1 {
			t.Fatalf("run() = %d, want the reporter's exit 1", code)
		}
		return got
	}

	t.Run("error value: stack + message + unwrap", func(t *testing.T) {
		var log []string
		got := capture(t, &haltHandlers{log: &log})
		var pe *PanicError
		if !errors.As(got, &pe) {
			t.Fatalf("reported %T, want a *PanicError", got)
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

	t.Run("non-error value: rendered, and still classified internal", func(t *testing.T) {
		var log []string
		got := capture(t, &panicValueHandlers{log: &log, val: 42})
		var pe *PanicError
		if !errors.As(got, &pe) {
			t.Fatalf("reported %T, want a *PanicError", got)
		}
		if pe.Error() != "42" {
			t.Errorf("Error() = %q, want \"42\"", pe.Error())
		}
		// A non-error panic value still unwraps to ErrInternal.
		if !errors.Is(got, ErrInternal) {
			t.Error("a panicked non-error value does not reach ErrInternal")
		}
		if CategoryOf(got) != CategoryInternal {
			t.Errorf("CategoryOf = %v, want internal for a recovered panic", CategoryOf(got))
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

func (exHandlers) App() Handler       { return exHook{name: "app"} }
func (exHandlers) AppStatus() Handler { return exHook{name: "status"} }

// exampleDef is a root with one "status" sub-command.
func exampleDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{Name: "status", Handler: "AppStatus"}},
	}
}

// A resolver that adds a routing alias: "st" rewrites to "status" and delegates to
// [DefaultResolver], so routing and parsing agree on the rewritten argv. A resolver alias is
// not completed; declare aliases in the spec for that.
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

// A lifecycle that wraps [DefaultLifecycle] and swaps the cascading pairs, so
// CascadingPostRun unwinds root→leaf. Halting, unwind and panic handling are unchanged.
func ExampleProgram_WithLifecycle() {
	NewProgram(exampleDef(), exHandlers{}).
		WithArgs([]string{"status"}).
		WithExit(func(int) {}).
		WithLifecycle(func(chain []Command, hs []Handler) []LifecycleStep {
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

// TestAsCommand_customLifecycleFallsBackToTheLeaf: a hand-built plan without AsCommand reports
// the invoked command in every hook; with AsCommand, the hook's own command.
func TestAsCommand_customLifecycleFallsBackToTheLeaf(t *testing.T) {
	var unlabeled, labeled string

	// Steps built by hand, with no AsCommand: the root's cascading hook must see the leaf.
	bare := func(chain []Command, hs []Handler) []LifecycleStep {
		return []LifecycleStep{
			{Name: "bare", Do: func(_ context.Context, rtx *Context) { unlabeled = rtx.Command().Name }},
		}
	}
	p := NewProgram(fDef(), fProg{}).WithLifecycle(bare).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if unlabeled != "leaf" {
		t.Errorf("an unlabeled step saw Command() = %q, want the leaf %q", unlabeled, "leaf")
	}

	// The same plan with AsCommand applied opts in explicitly.
	opted := func(chain []Command, hs []Handler) []LifecycleStep {
		return []LifecycleStep{
			{Name: "opted", Do: AsCommand(0, func(_ context.Context, rtx *Context) { labeled = rtx.Command().Name })},
		}
	}
	p2 := NewProgram(fDef(), fProg{}).WithLifecycle(opted).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p2.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if labeled != "root" {
		t.Errorf("AsCommand(0) saw Command() = %q, want %q", labeled, "root")
	}
}

// TestAsCommand_restoresThePreviousFrame: a nested AsCommand restores the outer command on
// return.
func TestAsCommand_restoresThePreviousFrame(t *testing.T) {
	var outer, inner, after string
	runF(t, []string{"mid", "leaf"}, func(rtx *Context) {
		outer = rtx.Command().Name
		AsCommand(0, func(_ context.Context, r *Context) { inner = r.Command().Name })(context.Background(), rtx)
		after = rtx.Command().Name
	}, nil)

	if outer != "mid" || inner != "root" || after != "mid" {
		t.Errorf("frames = outer:%q inner:%q after:%q, want mid/root/mid", outer, inner, after)
	}
}

// TestLifecycle_nilDoIsATeardownOnlyStep: a step with a nil Do still runs its Undo.
func TestLifecycle_nilDoIsATeardownOnlyStep(t *testing.T) {
	var order []string
	p := NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithLifecycle(func(chain []Command, hs []Handler) []LifecycleStep {
			return []LifecycleStep{
				{Name: "teardown-only", Undo: func(context.Context, *Context) {
					order = append(order, "undo")
				}},
				{Name: "work", Do: func(context.Context, *Context) {
					order = append(order, "do")
				}},
			}
		}).WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code != 0 || err != nil {
		t.Fatalf("(%d, %v), want a clean run", code, err)
	}
	if strings.Join(order, ",") != "do,undo" {
		t.Errorf("order = %v, want the work then the teardown-only step's Undo", order)
	}
}

// TestLifecycle_emptyPlanIsACleanNoOp: a plan with no steps runs nothing and exits 0.
func TestLifecycle_emptyPlanIsACleanNoOp(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).
		WithLifecycle(func([]Command, []Handler) []LifecycleStep { return nil }).
		WithStdout(io.Discard).WithStderr(io.Discard)

	if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Errorf("(%d, %v), want a plan with no steps to do nothing, quietly", code, err)
	}
	if len(ran) != 0 {
		t.Errorf("an empty plan still ran %v", ran)
	}
}
