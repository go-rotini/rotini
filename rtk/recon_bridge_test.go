package rtk_test

import (
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// TestPrecedence_argvBeatsAll verifies the canonical precedence: a flag
// supplied on argv shadows env, config, and default. The bridge should
// see argv values already in scope and skip recon entirely for them.
func TestPrecedence_argvBeatsAll(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{
				Name:        "output",
				Identifiers: []string{"-o", "--output"},
				Type:        "string",
				Default:     "table",
				EnvKey:      "TODO_OUTPUT",
				ConfigKey:   "defaults.output",
			},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{"--output", "argv-wins"},
		Env:    testEnv{"TODO_OUTPUT": "env-loses"},
		Config: testConfig{"defaults": map[string]any{"output": "config-loses"}},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "argv-wins" {
		t.Errorf("argv-vs-others: got %v, want \"argv-wins\"", v)
	}
}

// TestPrecedence_envBeatsConfigAndDefault verifies that with no argv value,
// env wins over config and default.
func TestPrecedence_envBeatsConfigAndDefault(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{
				Name:      "output",
				Type:      "string",
				Default:   "default-loses",
				EnvKey:    "TODO_OUTPUT",
				ConfigKey: "output",
			},
			// A pad flag so argv isn't empty (forces parseProgram past its
			// short-circuit even after the empty-argv fix in M1.4).
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{"--help"},
		Env:    testEnv{"TODO_OUTPUT": "env-wins"},
		Config: testConfig{"output": "config-loses"},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "env-wins" {
		t.Errorf("env-vs-others: got %v, want \"env-wins\"", v)
	}
}

// TestPrecedence_configBeatsDefault verifies that with no argv or env value,
// config wins over default.
func TestPrecedence_configBeatsDefault(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{
				Name:      "output",
				Type:      "string",
				Default:   "default-loses",
				ConfigKey: "output",
			},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{"--help"},
		Config: testConfig{"output": "config-wins"},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "config-wins" {
		t.Errorf("config-vs-default: got %v, want \"config-wins\"", v)
	}
}

// TestPrecedence_defaultFallback verifies that when no source supplies a
// value, the Default falls through.
func TestPrecedence_defaultFallback(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Type: "string", Default: "default-wins"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--help"}})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["output"])
	if v != "default-wins" {
		t.Errorf("default fallback: got %v, want \"default-wins\"", v)
	}
}

// TestPrecedence_intCoercion_fromConfig verifies that a config-supplied
// numeric value flows through coercion when the spec declares an int type.
func TestPrecedence_intCoercion_fromConfig(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "port", Type: "int", ConfigKey: "server.port"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}

	// Config supplies an int (typical YAML-decoded value).
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:   []string{"--help"},
		Config: testConfig{"server": map[string]any{"port": 8080}},
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope[""]["port"])
	if v != 8080 {
		t.Errorf("int from config: got %v (%T), want 8080 (int)", v, v)
	}
}

// TestPrecedence_envBypassedByArgv_subcommand verifies that the subcommand
// scope honors the argv-beats-env precedence too — argv-set on a subcommand
// flag shadows the env value, mirroring root-level behavior.
func TestPrecedence_envBypassedByArgv_subcommand(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "list", Path: "list",
				Flags: []rtk.FlagSpec{
					{
						Name:        "filter",
						Identifiers: []string{"-f"},
						Type:        "string",
						EnvKey:      "TODO_FILTER",
						Default:     "all",
					},
				},
			},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{
		Argv: []string{"list", "-f", "open"},
		Env:  testEnv{"TODO_FILTER": "env-shouldnt-win"},
	})
	tgt := &asTarget{path: "list"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope["list"]["filter"])
	if v != "open" {
		t.Errorf("subcommand argv-vs-env: got %v, want \"open\"", v)
	}
}

// TestPrecedence_envFallback_subcommand verifies env fallback works on
// subcommand scopes when argv doesn't supply the flag.
func TestPrecedence_envFallback_subcommand(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "list", Path: "list",
				Flags: []rtk.FlagSpec{
					{
						Name:        "filter",
						Identifiers: []string{"-f"},
						Type:        "string",
						EnvKey:      "TODO_FILTER",
						Default:     "all",
					},
				},
			},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{
		Argv: []string{"list"},
		Env:  testEnv{"TODO_FILTER": "open"},
	})
	tgt := &asTarget{path: "list"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope["list"]["filter"])
	if v != "open" {
		t.Errorf("subcommand env fallback: got %v, want \"open\"", v)
	}
}

// TestPrecedence_noSources_noValue verifies that when nothing supplies a
// flag and there's no Default, the scope doesn't get a value at all. This
// matters for Required-flag validation (M1.4) — missing flags should be
// missing.
func TestPrecedence_noSources_noValue(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Flags: []rtk.FlagSpec{
			{Name: "output", Type: "string"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}

	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--help"}})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if _, present := tgt.result.FlagsByScope[""]["output"]; present {
		t.Errorf("output: present in scope, but no source supplied a value")
	}
}
