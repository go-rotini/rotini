package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// guardAllocBudgets caps the allocations of one in-process run of a generated-shaped program (see
// guardProgram), measured with testing.AllocsPerRun. Every invocation of a CLI pays these, so a
// change that raises one should be a deliberate edit to this table.
//
// Each row runs with an empty environment plus extraEnv unrelated variables, so the counts do
// not depend on the shell the tests run in.
var guardAllocBudgets = []struct {
	name     string
	argv     []string
	extraEnv int
	budget   float64
}{
	{"help", []string{"--help"}, 0, 100},
	{"help, large environment", []string{"--help"}, 200, 100},
	{"dispatch", []string{"build", "--name", "x", "--count", "2", "--timeout", "5s", "--tag", "a", "tgt"}, 0, 250},
	{"dispatch, large environment", []string{"build", "--name", "x", "--count", "2", "--timeout", "5s", "--tag", "a", "tgt"}, 200, 250},
}

type guardRootFlags struct {
	Help    bool `rotini:"help"`
	Version bool `rotini:"version"`
}

type guardRootInputs struct {
	App struct {
		Flags     guardRootFlags
		Arguments struct{}
	}
}

type guardBuildInputs struct {
	App struct {
		Flags     guardRootFlags
		Arguments struct{}
	}
	AppBuild struct {
		Flags struct {
			Name    string        `rotini:"name"`
			Count   int           `rotini:"count"`
			Force   bool          `rotini:"force"`
			Timeout time.Duration `rotini:"timeout"`
			Tag     []string      `rotini:"tag"`
		}
		Arguments struct {
			Target string `rotini:"target"`
		}
	}
}

// guardRoot is the seeded root handler: it answers --help and --version from rtx.Inputs.
type guardRoot struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}

func (*guardRoot) CascadingPreRun(_ context.Context, rtx *Context) {
	in, err := rtx.Inputs[guardRootInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if in.App.Flags.Help {
		fmt.Fprintln(rtx.Stdout, "Usage: app [command]")
		rtx.HaltWithCode(0)
	}
}

func (*guardRoot) Run(_ context.Context, rtx *Context) { rtx.HaltWithCode(1) }

// guardBuild is a leaf handler that reads its inputs.
type guardBuild struct{ NoHooks }

func (*guardBuild) Run(_ context.Context, rtx *Context) {
	in, err := rtx.Inputs[guardBuildInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	fmt.Fprintln(rtx.Stdout, in.AppBuild.Arguments.Target)
}

// guardProgram builds the program the way generated code does: NewProgramFunc with a switch,
// the generated InputSettings, and no signal trap, so the counts measure rotini's own work.
func guardProgram() *Program {
	leafFlags := []FlagDef{
		{Name: "name", Identifiers: []string{"--name"}, Type: "string"},
		{Name: "count", Identifiers: []string{"--count"}, Type: "int"},
		{Name: "force", Identifiers: []string{"--force"}, Type: "bool"},
		{Name: "timeout", Identifiers: []string{"--timeout"}, Type: "time.Duration"},
		{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string"},
	}
	def := Definition{
		Name:    "app",
		Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "version", Identifiers: []string{"-v", "--version"}, Type: "bool", ShortCircuit: true},
		},
		Commands: []CommandDef{
			{
				Name: "build", Handler: "AppBuild", Flags: leafFlags,
				Arguments: []ArgDef{{Name: "target", Type: "string", Required: true}},
			},
			{
				Name: "deploy", Handler: "AppDeploy", Flags: leafFlags,
				Arguments: []ArgDef{{Name: "target", Type: "string", Required: true}},
			},
		},
	}
	lookup := func(name string) (Handler, bool) {
		switch name {
		case "App":
			return &guardRoot{}, true
		case "AppBuild", "AppDeploy":
			return &guardBuild{}, true
		}
		return nil, false
	}
	return NewProgramFunc(def, lookup).
		WithInputSettings(InputSettings{}).
		WithoutSignalHandling().
		WithStdout(io.Discard).
		WithStderr(io.Discard)
}

// guardClearEnv empties the environment for the rest of the test and restores it afterwards.
func guardClearEnv(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			k, v, _ := strings.Cut(kv, "=")
			_ = os.Setenv(k, v)
		}
	})
}

func TestGuard_allocBudgets(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation budgets are measured in full runs only")
	}
	for _, row := range guardAllocBudgets {
		t.Run(row.name, func(t *testing.T) {
			guardClearEnv(t)
			for i := range row.extraEnv {
				t.Setenv(fmt.Sprintf("ROTINI_ALLOC_UNRELATED_%03d", i), "value")
			}
			p := guardProgram()
			if code, err := p.Run(row.argv); code != 0 || err != nil {
				t.Fatalf("Run(%q) = %d, %v; want 0, nil", row.argv, code, err)
			}
			got := testing.AllocsPerRun(20, func() { _, _ = p.Run(row.argv) })
			t.Logf("%s: %.0f allocations (budget %.0f)", row.name, got, row.budget)
			if got > row.budget {
				t.Errorf("%s: %.0f allocations per run, over the budget of %.0f", row.name, got, row.budget)
			}
		})
	}
}
