package rotini

import (
	"bytes"
	"context"
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

func TestRun_unknownCommandSuggests(t *testing.T) {
	var log []string
	p, _, errb := newTestProgram(&testHandlers{log: &log}, []string{"ru"}) // typo of "run"
	code := p.run(p.args)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown command "ru"`) {
		t.Errorf("stderr missing unknown-command message: %s", errb)
	}
	if !strings.Contains(errb.String(), `Did you mean "run"?`) {
		t.Errorf("stderr missing suggestion: %s", errb)
	}
	if len(log) != 0 {
		t.Errorf("no handler should run on parse failure: %v", log)
	}
}

func TestRun_unknownFlag(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: new([]string)}, []string{"run", "--nope"})
	if code := p.run(p.args); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown flag "--nope"`) {
		t.Errorf("stderr missing unknown-flag message: %s", errb)
	}
}

func TestRun_flagNeedsValue(t *testing.T) {
	p, _, errb := newTestProgram(&testHandlers{log: new([]string)}, []string{"run", "--count"})
	if code := p.run(p.args); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "needs a value") {
		t.Errorf("stderr missing needs-a-value message: %s", errb)
	}
}

func TestRun_helpFlag(t *testing.T) {
	p, out, _ := newTestProgram(&testHandlers{log: new([]string)}, []string{"run", "--help"})
	if code := p.run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if s := out.String(); !strings.Contains(s, "Usage:") || !strings.Contains(s, "app run") {
		t.Errorf("help output unexpected: %s", s)
	}
}

func TestRun_bareNamespacePrintsHelp(t *testing.T) {
	var log []string
	p, out, _ := newTestProgram(&testHandlers{log: &log}, []string{})
	if code := p.run(p.args); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if s := out.String(); !strings.Contains(s, "Commands:") || !strings.Contains(s, "run") {
		t.Errorf("bare help output unexpected: %s", s)
	}
	if len(log) != 0 {
		t.Errorf("bare namespace should not run a handler: %v", log)
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
