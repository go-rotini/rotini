package rtk_test

import (
	"io"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// TestSpec_literalConstructable exercises that ProgramSpec / CommandSpec /
// FlagSpec / ArgumentSpec / StdinSpec literals compile with their full
// constraint vocabulary. It is a compile-time check first and a smoke
// assertion second; if any spec field is renamed or removed, this test fails
// to build.
func TestSpec_literalConstructable(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Name:        "todo",
		Summary:     "A todo CLI",
		Description: "Manage tasks from the command line.",
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
			{Name: "version", Identifiers: []string{"-v", "--version"}, Type: "bool"},
		},
		Commands: []rtk.CommandSpec{
			{
				Path:        "add",
				Name:        "add",
				Aliases:     []string{"a"},
				Summary:     "Add a task",
				Description: "Add a new task to the store.",
				Flags: []rtk.FlagSpec{
					{
						Name:        "priority",
						Identifiers: []string{"-p", "--priority"},
						Type:        "string",
						Summary:     "Priority level",
						Required:    false,
						Default:     "medium",
						Enum:        []string{"low", "medium", "high"},
						Pattern:     "",
						Min:         nil,
						Max:         nil,
						MinLength:   rtk.Ptr(1),
						MaxLength:   rtk.Ptr(10),
						Nullable:    false,
						EnvKey:      "TODO_PRIORITY",
						ConfigKey:   "defaults.priority",
						EnvOnly:     false,
						Deprecated:  "",
					},
				},
				Arguments: []rtk.ArgumentSpec{
					{
						Name:      "text",
						Type:      "string",
						Required:  true,
						MinLength: rtk.Ptr(1),
						MaxLength: rtk.Ptr(500),
						EnvKey:    "",
						ConfigKey: "",
					},
				},
				Stdin: &rtk.StdinSpec{
					Format: "text",
				},
			},
			{
				Path: "list",
				Name: "list",
				Flags: []rtk.FlagSpec{
					{
						Name:        "filter",
						Identifiers: []string{"-f", "--filter"},
						Type:        "string",
						Enum:        []string{"all", "open", "done"},
						Default:     "all",
					},
				},
				Stdin: &rtk.StdinSpec{
					Format: "json",
					Fields: []rtk.StdinField{
						{Name: "since", Type: "string", Required: false},
						{Name: "limit", Type: "int", Required: false},
					},
				},
			},
			{
				Path: "done",
				Name: "done",
				Arguments: []rtk.ArgumentSpec{
					{
						Name:     "id",
						Type:     "int",
						Required: true,
						Min:      rtk.Ptr[float64](1),
					},
				},
			},
		},
	}

	if spec.Name != "todo" {
		t.Errorf("ProgramSpec.Name: got %q, want %q", spec.Name, "todo")
	}
	if len(spec.Commands) != 3 {
		t.Errorf("ProgramSpec.Commands: got %d, want 3", len(spec.Commands))
	}
	if spec.Commands[0].Flags[0].MinLength == nil || *spec.Commands[0].Flags[0].MinLength != 1 {
		t.Error("FlagSpec.MinLength pointer not round-tripping")
	}
	if spec.Commands[2].Arguments[0].Min == nil || *spec.Commands[2].Arguments[0].Min != 1 {
		t.Error("ArgumentSpec.Min pointer not round-tripping")
	}
}

// TestPtrHelpers smoke-checks IntPtr / Float64Ptr.
func TestPtrHelpers(t *testing.T) {
	t.Parallel()

	if got := rtk.Ptr(42); got == nil || *got != 42 {
		t.Errorf("IntPtr(42): got %v, want pointer to 42", got)
	}
	if got := rtk.Ptr[float64](3.14); got == nil || *got != 3.14 {
		t.Errorf("Float64Ptr(3.14): got %v, want pointer to 3.14", got)
	}
}

// fakeTarget exercises the Target interface contract — a compile-time check
// that any Inputs-shaped struct can satisfy it.
type fakeTarget struct {
	pathReturn string
	popCalls   int
	popResult  *rtk.Result
	popErr     error
}

func (f *fakeTarget) RotiniCommandPath() string { return f.pathReturn }

func (f *fakeTarget) PopulateFromArgv(r *rtk.Result) error {
	f.popCalls++
	f.popResult = r
	return f.popErr
}

func TestTarget_interfaceSatisfaction(t *testing.T) {
	t.Parallel()

	var tgt rtk.Target = &fakeTarget{pathReturn: "generate"}
	if got := tgt.RotiniCommandPath(); got != "generate" {
		t.Errorf("RotiniCommandPath: got %q, want %q", got, "generate")
	}

	result := &rtk.Result{
		CommandPath:  []string{"generate"},
		FlagsByScope: map[string]map[string]any{"": {"help": false}},
		ParsedArgs:   []string{"foo"},
	}
	if err := tgt.PopulateFromArgv(result); err != nil {
		t.Errorf("PopulateFromArgv: unexpected error %v", err)
	}
	ft, ok := tgt.(*fakeTarget)
	if !ok {
		t.Fatalf("Target is not *fakeTarget")
	}
	if ft.popCalls != 1 || ft.popResult != result {
		t.Errorf("PopulateFromArgv did not receive the expected Result: calls=%d result=%v", ft.popCalls, ft.popResult)
	}
}

// fakeEnv / fakeConfig confirm the EnvLookup / ConfigLookup interfaces are
// satisfiable and the Inputs struct holds them.
type fakeEnv map[string]string

func (f fakeEnv) Lookup(key string) (string, bool) {
	v, ok := f[key]
	return v, ok
}

type fakeConfig map[string]any

func (f fakeConfig) Lookup(path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	v, ok := f[path[0]]
	return v, ok
}

func TestInputs_holdsEnvAndConfig(t *testing.T) {
	t.Parallel()

	env := fakeEnv{"FOO": "bar"}
	cfg := fakeConfig{"server": map[string]any{"port": 8080}}

	in := rtk.Inputs{
		Argv:   []string{"--flag", "value"},
		Stdin:  io.NopCloser(nil),
		Env:    env,
		Config: cfg,
	}

	if v, ok := in.Env.Lookup("FOO"); !ok || v != "bar" {
		t.Errorf("Env.Lookup(FOO): got (%q, %v), want (bar, true)", v, ok)
	}
	if v, ok := in.Config.Lookup([]string{"server"}); !ok || v == nil {
		t.Errorf("Config.Lookup([server]): got (%v, %v), want (non-nil, true)", v, ok)
	}
	if len(in.Argv) != 2 {
		t.Errorf("Inputs.Argv: got len=%d, want 2", len(in.Argv))
	}
}
