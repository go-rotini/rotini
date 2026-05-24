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
	onRun func(rtx Context)
}

func (h *recHandler) CascadingPreRun(_ context.Context, _ Context)  { h.note("CascadingPreRun") }
func (h *recHandler) PreRun(_ context.Context, _ Context)           { h.note("PreRun") }
func (h *recHandler) PostRun(_ context.Context, _ Context)          { h.note("PostRun") }
func (h *recHandler) CascadingPostRun(_ context.Context, _ Context) { h.note("CascadingPostRun") }
func (h *recHandler) Run(_ context.Context, rtx Context) {
	h.note("Run")
	if h.onRun != nil {
		h.onRun(rtx)
	}
}
func (h *recHandler) note(hook string) { *h.log = append(*h.log, h.name+"."+hook) }

// testHandlers is the aggregate the runtime dispatches against by reflection.
type testHandlers struct {
	log   *[]string
	onRun func(rtx Context)
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

func newTestProgram(h any, args []string) (*program, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgram(testDef(), h).WithArguments(args)
	p.stdout, p.stderr = out, errb
	return p, out, errb
}

func TestRun_lifecycleOrderAndInputs(t *testing.T) {
	var log []string
	var got runInputs
	h := &testHandlers{log: &log, onRun: func(rtx Context) { got = Inputs[runInputs](rtx) }}

	p, _, errb := newTestProgram(h, []string{"--verbose", "run", "alice", "x", "y", "--count", "3"})
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
	if !got.App.Flags.Verbose {
		t.Errorf("ancestor flag not bound during Run: App.Flags.Verbose = false")
	}
	if got.Run.Flags.Count != 3 {
		t.Errorf("Count = %d, want 3", got.Run.Flags.Count)
	}
	if got.Run.Arguments.Name != "alice" {
		t.Errorf("Name = %q, want alice", got.Run.Arguments.Name)
	}
	if r := got.Run.Arguments.Rest; len(r) != 2 || r[0] != "x" || r[1] != "y" {
		t.Errorf("Rest = %v, want [x y]", r)
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
	h := &testHandlers{log: new([]string), onRun: func(rtx Context) { Exit(rtx, 5) }}
	p, _, _ := newTestProgram(h, []string{"run"})
	if code := p.run(p.args); code != 5 {
		t.Errorf("run() = %d, want 5 (handler called Exit)", code)
	}
}

func TestRun_mustGetRoutesToOnError(t *testing.T) {
	var seen error
	h := &testHandlers{log: new([]string), onRun: func(rtx Context) {
		_ = MustGet[*Parser](rtx, "no-such-service") // panics; recovered into the funnel
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.OnError(func(_ Context, err error) int {
		seen = err
		return 7
	})

	if code := p.run(p.args); code != 7 {
		t.Fatalf("run() = %d, want 7 (OnError's code)", code)
	}
	if !errors.Is(seen, ErrServiceNotFound) {
		t.Errorf("OnError got %v, want it to wrap ErrServiceNotFound", seen)
	}
	var se *ServiceError
	if !errors.As(seen, &se) || se.Key != "no-such-service" {
		t.Errorf("OnError error did not carry the key: %v", seen)
	}
}

func TestRun_defaultOnErrorPrintsAndReturns1(t *testing.T) {
	h := &testHandlers{log: new([]string), onRun: func(rtx Context) {
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

func TestUsage_fromContext(t *testing.T) {
	chain, _ := resolveChain(testDef(), []string{"run"})
	rtx := NewRtx()
	rtx.chain = chain
	s := Usage(rtx)
	if !strings.Contains(s, "Usage:") || !strings.Contains(s, "app run") {
		t.Errorf("Usage(rtx) unexpected:\n%s", s)
	}
	if Usage(NewRtx()) != "" {
		t.Errorf("Usage on an unresolved context should be empty")
	}
}

func TestPrintUsage_rich(t *testing.T) {
	def := Definition{
		Name:        "app",
		Handler:     "App",
		Summary:     "Do things.",
		Description: "A longer description of app.",
		Flags:       []FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool", Description: "Be loud."}},
		Commands: []CommandDef{
			{
				Name: "run", Handler: "AppRun", Aliases: []string{"r"}, Summary: "Run it.",
				Flags:     []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int", Description: "How many.", Default: "1"}},
				Arguments: []ArgDef{{Name: "name", Description: "Who to run."}},
			},
		},
	}

	var buf bytes.Buffer
	writeUsage(&buf, []frame{rootFrame(def)})
	root := buf.String()
	for _, want := range []string{"Do things.", "Usage:", "A longer description of app.", "Commands:", "run, r", "Run it.", "Be loud."} {
		if !strings.Contains(root, want) {
			t.Errorf("root help missing %q:\n%s", want, root)
		}
	}

	buf.Reset()
	writeUsage(&buf, []frame{rootFrame(def), cmdFrame(def.Commands[0])})
	leaf := buf.String()
	for _, want := range []string{"Run it.", "app run", "Arguments:", "Who to run.", "Flags:", "--count int", "How many.", `(default "1")`} {
		if !strings.Contains(leaf, want) {
			t.Errorf("run help missing %q:\n%s", want, leaf)
		}
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
