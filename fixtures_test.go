package rotini

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

// Test fixtures shared across the package's tests: a recording handler set, the command tree
// they dispatch against, and the program constructor that wires them to captured streams.

// recHandler records each lifecycle hook it runs, and optionally inspects the
// context during Run. It satisfies Handler.
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

func (t *testHandlers) App() Handler { return &recHandler{name: "app", log: t.log} }

func (t *testHandlers) AppRun() Handler {
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

func (p *actProgram) mk(name string) Handler {
	return &actHandler{name: name, log: p.log, act: p.actions[name]}
}

func (p *actProgram) App() Handler { return p.mk("app") }

func (p *actProgram) AppRun() Handler { return p.mk("run") }

func runActs(t *testing.T, args []string, actions map[string]act) (int, []string) {
	t.Helper()
	log := []string{}
	p, _, _ := newTestProgram(&actProgram{log: &log, actions: actions}, args)
	code, _ := p.Run(p.args)
	return code, log
}

// panicThenHardExit is a leaf whose Run panics and whose PostRun then hard-Exits(3),
// to exercise the panic-reporter-vs-hard-Exit interaction.
type panicThenHardExit struct{ log *[]string }

func (p *panicThenHardExit) App() Handler { return &recHandler{name: "app", log: p.log} }

func (p *panicThenHardExit) AppRun() Handler {
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

// Fixtures for anchoring an inputs struct on the command whose hook is running. The tree is
// root → mid → leaf. Each command owns a uniquely named flag so attribution is unambiguous, and
// each also declares --help, as real CLIs do.

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

// fMidSpan is what codegen emits for `mid` in an ordinary CLI: its whole lineage.
type fMidSpan struct {
	Root fRootCmd
	Mid  fMidCmd
}

// fMidOwn is what codegen emits for a composed child's root command: one field, since the
// child does not know the tree it is mounted into.
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

func (h fProg) Root() Handler { return fHooks{cascading: h.inRoot} }
func (h fProg) Mid() Handler  { return fHooks{cascading: h.inMid} }
func (h fProg) Leaf() Handler { return fHooks{} }

type fHooks struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
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

func (h fLeafProbe) Root() Handler { return fHooks{} }
func (h fLeafProbe) Mid() Handler  { return fHooks{} }
func (h fLeafProbe) Leaf() Handler { return fLeafRun(h) }

type fLeafRun fLeafProbe

func (fLeafRun) CascadingPreRun(context.Context, *Context)  {}
func (fLeafRun) PreRun(context.Context, *Context)           {}
func (fLeafRun) PostRun(context.Context, *Context)          {}
func (fLeafRun) CascadingPostRun(context.Context, *Context) {}
func (h fLeafRun) Run(_ context.Context, rtx *Context)      { h.capture(rtx) }

// Fixtures for custom Resolver and Lifecycle tests: what each is handed and may do with it.

type seamProgram struct{ ran *[]string }

func (p seamProgram) App() Handler    { return seamHandlers{ran: p.ran} }
func (p seamProgram) AppRun() Handler { return seamHandlers{ran: p.ran} }

type seamHandlers struct {
	NoHooks
	ran *[]string
}

func (h seamHandlers) Run(_ context.Context, rtx *Context) {
	*h.ran = append(*h.ran, rtx.Command().Name)
}
