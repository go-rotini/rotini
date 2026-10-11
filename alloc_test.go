package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Allocation budgets for the paths every invocation pays: a seeded root --help and a normal
// dispatch, where the root's CascadingPreRun and the leaf's Run both read their inputs. The
// counts must not depend on the size of the environment.

const (
	allocBudgetHelp     = 100
	allocBudgetDispatch = 250
	allocBudgetRegistry = 1000
)

// allocDef has the shape of a generated program: a root with short-circuit --help and
// --version, and a leaf with string, int, bool, duration and list flags and one argument.
func allocDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "version", Identifiers: []string{"--version"}, Type: "bool", ShortCircuit: true},
		},
		Commands: []CommandDef{{
			Name: "build", Handler: "AppBuild",
			Flags: []FlagDef{
				{Name: "name", Identifiers: []string{"--name"}, Type: "string"},
				{Name: "count", Identifiers: []string{"--count"}, Type: "int"},
				{Name: "force", Identifiers: []string{"--force"}, Type: "bool"},
				{Name: "timeout", Identifiers: []string{"--timeout"}, Type: "time.Duration"},
				{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string"},
			},
			Arguments: []ArgDef{{Name: "target", Type: "string", Required: true}},
		}},
	}
}

type allocAppInputs struct {
	App struct {
		Flags struct {
			Help    bool `rotini:"help"`
			Version bool `rotini:"version"`
		}
		Arguments struct{}
	}
}

type allocBuildCommandInputs struct {
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

type allocBuildInputs struct {
	App      struct{ Flags, Arguments struct{} }
	AppBuild allocBuildCommandInputs
}

type allocHandlers struct{}

func (allocHandlers) App() Handler      { return allocRoot{} }
func (allocHandlers) AppBuild() Handler { return allocBuild{} }

type allocRoot struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}

func (allocRoot) CascadingPreRun(_ context.Context, rtx *Context) {
	in, err := rtx.Inputs[allocAppInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if in.App.Flags.Help {
		_, _ = io.WriteString(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
	}
}

func (allocRoot) Run(_ context.Context, rtx *Context) { rtx.HaltWithCode(1) }

type allocBuild struct {
	NoCascadingPreRun
	NoPreRun
	NoPostRun
	NoCascadingPostRun
}

func (allocBuild) Run(_ context.Context, rtx *Context) {
	if _, err := rtx.Inputs[allocBuildInputs](); err != nil {
		rtx.HaltWith(err)
	}
}

func allocProgram() *Program {
	return NewProgram(allocDef(), allocHandlers{}).
		WithInputSettings(InputSettings{}).
		WithHelp(func(...string) (string, error) { return "usage: app\n", nil }).
		WithStdin(&emptyReader{}).WithStdout(io.Discard).WithStderr(io.Discard).
		WithoutSignalHandling()
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

var (
	allocHelpArgv     = []string{"--help"}
	allocDispatchArgv = []string{"build", "--name", "x", "--count", "3", "--force", "--timeout", "5s", "--tag", "a", "--tag", "b", "tgt"}
)

// allocsFor runs argv through p and returns the mean allocations per run, failing on a
// non-zero exit.
func allocsFor(t *testing.T, p *Program, argv []string) float64 {
	t.Helper()
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run(%q) = %d, %v", argv, code, err)
	}
	return testing.AllocsPerRun(50, func() { _, _ = p.Run(argv) })
}

func TestAllocs_inputsBudget(t *testing.T) {
	p := allocProgram()
	help := allocsFor(t, p, allocHelpArgv)
	dispatch := allocsFor(t, p, allocDispatchArgv)
	t.Logf("environment of %d variables: --help %.0f allocs, dispatch %.0f allocs", len(os.Environ()), help, dispatch)
	if help > allocBudgetHelp {
		t.Errorf("--help: %.0f allocations, budget %d", help, allocBudgetHelp)
	}
	if dispatch > allocBudgetDispatch {
		t.Errorf("dispatch: %.0f allocations, budget %d", dispatch, allocBudgetDispatch)
	}

	for i := range 500 {
		t.Setenv(fmt.Sprintf("ROTINI_ALLOC_PAD_%03d", i), "some padding value")
	}
	bigHelp := allocsFor(t, p, allocHelpArgv)
	bigDispatch := allocsFor(t, p, allocDispatchArgv)
	t.Logf("environment of %d variables: --help %.0f allocs, dispatch %.0f allocs", len(os.Environ()), bigHelp, bigDispatch)
	if bigHelp != help || bigDispatch != dispatch {
		t.Errorf("allocations depend on the environment's size: --help %.0f → %.0f, dispatch %.0f → %.0f",
			help, bigHelp, dispatch, bigDispatch)
	}

	env := os.Environ()
	injected := allocProgram().WithEnviron(env)
	injHelp := allocsFor(t, injected, allocHelpArgv)
	injDispatch := allocsFor(t, injected, allocDispatchArgv)
	t.Logf("injected environment of %d variables: --help %.0f allocs, dispatch %.0f allocs", len(env), injHelp, injDispatch)
	if injHelp > allocBudgetHelp || injDispatch > allocBudgetDispatch {
		t.Errorf("with an injected environment: --help %.0f, dispatch %.0f; budgets %d and %d", injHelp, injDispatch, allocBudgetHelp, allocBudgetDispatch)
	}
}

// The registry path: a leaf with an env input, an env fallback flag and a config file.
func TestAllocs_registryPath(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(cfg, []byte("level: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def := allocDef()
	def.Commands[0].Flags = append(def.Commands[0].Flags, FlagDef{Name: "level", Identifiers: []string{"--level"}, Type: "int"})
	p := NewProgram(def, allocRegistryHandlers{}).
		WithInputSettings(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg}}}).
		WithStdin(&emptyReader{}).WithStdout(io.Discard).WithStderr(io.Discard).
		WithoutSignalHandling()
	t.Setenv("ALLOC_REGION", "eu")
	n := allocsFor(t, p, allocDispatchArgv)
	for i := range 500 {
		t.Setenv(fmt.Sprintf("ROTINI_ALLOC_PAD_%03d", i), "some padding value")
	}
	big := allocsFor(t, p, allocDispatchArgv)
	t.Logf("registry path: %.0f allocs, %.0f with 500 more variables", n, big)
	// recon's registries run a watch goroutine whose allocations land in the count on their
	// own schedule, so this budget leaves room.
	if n > allocBudgetRegistry || big > allocBudgetRegistry {
		t.Errorf("registry path: %.0f allocations, %.0f with a large environment; budget %d", n, big, allocBudgetRegistry)
	}
}

type allocRegistryInputs struct {
	App      struct{ Flags, Arguments struct{} }
	AppBuild struct {
		Flags struct {
			Name    string        `rotini:"name"`
			Count   int           `rotini:"count"`
			Force   bool          `rotini:"force"`
			Timeout time.Duration `rotini:"timeout"`
			Tag     []string      `rotini:"tag"`
			Level   int           `rotini:"level" recon:"level" env:"ALLOC_LEVEL"`
		}
		Arguments struct {
			Target string `rotini:"target"`
		}
		Env struct {
			Region string `rotini:"region" recon:"region" env:"ALLOC_REGION"`
		}
		Config struct {
			Level int `rotini:"level" recon:"level"`
		}
	}
}

type allocRegistryHandlers struct{ allocHandlers }

func (allocRegistryHandlers) AppBuild() Handler { return allocRegistryBuild{} }

type allocRegistryBuild struct{ allocBuild }

func (allocRegistryBuild) Run(_ context.Context, rtx *Context) {
	in, err := rtx.Inputs[allocRegistryInputs]()
	switch {
	case err != nil:
		rtx.HaltWith(err)
	case in.AppBuild.Env.Region != "eu" || in.AppBuild.Flags.Level != 3:
		rtx.HaltWith(fmt.Errorf("unexpected inputs %+v", in.AppBuild))
	}
}

// The hot path every generated program runs: the seeded root hook reads its inputs, and a leaf
// reads its own.
func BenchmarkInputs_help(b *testing.B)     { benchmarkRun(b, allocHelpArgv) }
func BenchmarkInputs_dispatch(b *testing.B) { benchmarkRun(b, allocDispatchArgv) }

func benchmarkRun(b *testing.B, argv []string) {
	p := allocProgram()
	b.ReportAllocs()
	for b.Loop() {
		if code, err := p.Run(argv); code != 0 || err != nil {
			b.Fatalf("Run = %d, %v", code, err)
		}
	}
}
