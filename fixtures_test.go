package rotini

import (
	"bytes"
	"context"
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
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
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
