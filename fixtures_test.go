package rotini

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

// Test fixtures shared across the package's tests: a recording handler set, the
// command tree they all dispatch against, and the program constructor that wires them
// to captured streams.
//
// This is one of the few test files with no source counterpart by design — it holds no
// tests, only the scaffolding several test files need in common.

// recHandler records each lifecycle hook it runs, and optionally inspects the
// context during Run. It satisfies Handlers.
type recHandler struct {
	name  string
	log   *[]string
	onRun func(rtx *Context)
}

func (h *recHandler) CascadingPreRun(_ context.Context, _ *Context) { h.note("CascadingPreRun") }

func (h *recHandler) PreRun(_ context.Context, _ *Context) { h.note("PreRun") }

func (h *recHandler) PostRun(_ context.Context, _ *Context) { h.note("PostRun") }

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

func contains(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

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

func (h *actHandler) PreRun(_ context.Context, rtx *Context) { h.hook("PreRun", rtx) }

func (h *actHandler) Run(_ context.Context, rtx *Context) { h.hook("Run", rtx) }

func (h *actHandler) PostRun(_ context.Context, rtx *Context) { h.hook("PostRun", rtx) }

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

func (p *actProgram) App() Handlers { return p.mk("app") }

func (p *actProgram) AppRun() Handlers { return p.mk("run") }

func runActs(t *testing.T, args []string, actions map[string]act) (int, []string) {
	t.Helper()
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: actions}, args)
	code, _ := p.Run(p.args)
	return code, log
}

// panicThenHardExit is a leaf whose Run panics and whose PostRun then hard-Exits(3),
// to exercise the panic-funnel-vs-hard-Exit interaction.
type panicThenHardExit struct{ log *[]string }

func (p *panicThenHardExit) App() Handlers { return &recHandler{name: "app", log: p.log} }

func (p *panicThenHardExit) AppRun() Handlers {
	return &panicThenHardExitLeaf{log: p.log}
}

type panicThenHardExitLeaf struct{ log *[]string }

func (h *panicThenHardExitLeaf) note(n string) { *h.log = append(*h.log, "run."+n) }

func (h *panicThenHardExitLeaf) CascadingPreRun(context.Context, *Context) { h.note("CascadingPreRun") }

func (h *panicThenHardExitLeaf) PreRun(context.Context, *Context) { h.note("PreRun") }

func (h *panicThenHardExitLeaf) Run(context.Context, *Context) { h.note("Run"); panic("boom") }

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

// An inputs struct is anchored on the command whose hook is running, not guessed from its field
// count against the chain. These tests are the matrix that motivated the change, asserted the
// right way round.
//
// The tree is root → mid → leaf. Each frame owns a uniquely named flag so attribution is
// unambiguous, and every frame also declares --help, which every real CLI does and which used to
// disable the alignment guard entirely — so a misalignment here cannot be reported and would
// come back as a wrong value instead.

type fRootFlags struct {
	RootOnly bool `rotini:"rootonly"`
	Help     bool `rotini:"help"`
}
type fMidFlags struct {
	MidOnly bool `rotini:"midonly"`
	Help    bool `rotini:"help"`
}
type fLeafFlags struct {
	LeafOnly bool `rotini:"leafonly"`
	Help     bool `rotini:"help"`
}

type fRootCmd struct {
	Flags     fRootFlags
	Arguments struct{}
}
type fMidCmd struct {
	Flags     fMidFlags
	Arguments struct{}
}
type fLeafCmd struct {
	Flags     fLeafFlags
	Arguments struct{}
}

// fMidSpan is what codegen emits for `mid` in an ORDINARY cli: its whole lineage.
type fMidSpan struct {
	Root fRootCmd
	Mid  fMidCmd
}

// fMidOwn is what codegen emits for a COMPOSED CHILD's root command: one field, because the
// child cannot know which tree it will be mounted into.
type fMidOwn struct{ Mid fMidCmd }

// fLeafSpan is the leaf's own full-lineage type.
type fLeafSpan struct {
	Root fRootCmd
	Mid  fMidCmd
	Leaf fLeafCmd
}

func fFlags(own string) []FlagDef {
	return []FlagDef{
		{Name: own, Identifiers: []string{"--" + own}, Type: "bool"},
		{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
	}
}

func fDef() Definition {
	return Definition{
		Name: "root", Handler: "Root", Flags: fFlags("rootonly"),
		Commands: []CommandDef{{
			Name: "mid", Handler: "Mid", Flags: fFlags("midonly"),
			Commands: []CommandDef{{Name: "leaf", Handler: "Leaf", Flags: fFlags("leafonly")}},
		}},
	}
}

type fProg struct {
	inMid  func(*Context)
	inRoot func(*Context)
}

func (h fProg) Root() Handlers { return fHooks{cascading: h.inRoot} }
func (h fProg) Mid() Handlers  { return fHooks{cascading: h.inMid} }
func (h fProg) Leaf() Handlers { return fHooks{} }

type fHooks struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	cascading func(*Context)
}

func (h fHooks) Run(context.Context, *Context) {}
func (h fHooks) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.cascading != nil {
		h.cascading(rtx)
	}
}

func runF(t *testing.T, argv []string, inMid, inRoot func(*Context)) {
	t.Helper()
	p := NewProgram(fDef(), fProg{inMid: inMid, inRoot: inRoot}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run(argv); err != nil {
		t.Fatalf("Run(%v) = %v", argv, err)
	}
}

type fLeafProbe struct{ capture func(*Context) }

func (h fLeafProbe) Root() Handlers { return fHooks{} }
func (h fLeafProbe) Mid() Handlers  { return fHooks{} }
func (h fLeafProbe) Leaf() Handlers { return fLeafRun(h) }

type fLeafRun fLeafProbe

func (fLeafRun) CascadingPreRun(context.Context, *Context)  {}
func (fLeafRun) PreRun(context.Context, *Context)           {}
func (fLeafRun) PostRun(context.Context, *Context)          {}
func (fLeafRun) CascadingPostRun(context.Context, *Context) {}
func (h fLeafRun) Run(_ context.Context, rtx *Context)      { h.capture(rtx) }

// What a custom Resolver or Lifecycle is handed, and what it may do with it. These are the
// expert seams: nothing else in the API lets a caller replace a phase, so nothing else can
// corrupt a Program that serves many runs.

type seamProgram struct{ ran *[]string }

func (p seamProgram) App() Handlers    { return seamHandlers{ran: p.ran} }
func (p seamProgram) AppRun() Handlers { return seamHandlers{ran: p.ran} }

type seamHandlers struct {
	DefaultHooks
	ran *[]string
}

func (h seamHandlers) Run(_ context.Context, rtx *Context) {
	*h.ran = append(*h.ran, rtx.Frame().Name)
}
