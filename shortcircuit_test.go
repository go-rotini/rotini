package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scDef is a root with a short-circuit --help and every kind of declared requirement, plus a
// sub-command whose own requirement a root short-circuit flag also waives.
func scDef(t *testing.T) Definition {
	t.Helper()
	low, high := 1.0, 10.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
			{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}},
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Constraints: Constraints{Minimum: &low, Maximum: &high}},
			{Name: "file", Identifiers: []string{"--file"}, Type: "existingfile"},
			{Name: "labels", Identifiers: []string{"--labels"}, Type: "map[string]string"},
			{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
		},
		FlagGroups:       []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"a", "b"}}},
		FlagDependencies: []FlagDependency{{When: "level", Requires: []string{"port"}}},
		Commands: []CommandDef{{
			Name: "run", Handler: "AppRun",
			Arguments: []ArgDef{{Name: "target", Type: "string", Required: true}},
		}},
	}
}

// TestShortCircuit_parseWaivesRequirements walks the plan's kept/waived table through
// Parser.Parse: requirements are waived when a short-circuit flag is set on argv; what cannot
// be read is still an error.
func TestShortCircuit_parseWaivesRequirements(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		kind ParseKind // ParseKindUnspecified = no error expected
	}{
		{"required without the flag", []string{}, ParseKindMissingRequired},
		{"required waived", []string{"--help"}, ParseKindUnspecified},
		{"short identifier", []string{"-h"}, ParseKindUnspecified},
		{"enum waived", []string{"--help", "--level", "medium"}, ParseKindUnspecified},
		{"bound waived", []string{"--help", "--port", "99"}, ParseKindUnspecified},
		{"existing-file check waived", []string{"--help", "--file", "/no/such/file"}, ParseKindUnspecified},
		{"flag group waived", []string{"--help", "--a", "--b"}, ParseKindUnspecified},
		{"flag dependency waived", []string{"--help", "--level", "low"}, ParseKindUnspecified},
		{"sub-command requirement waived", []string{"run", "--help"}, ParseKindUnspecified},
		{"explicit false waives nothing", []string{"--help=false"}, ParseKindMissingRequired},
		{"unknown flag kept", []string{"--help", "--bogus"}, ParseKindUnknownFlag},
		{"unknown command kept", []string{"nope", "--help"}, ParseKindUnknownCommand},
		{"uncoercible value kept", []string{"--help", "--port", "abc"}, ParseKindInvalidValue},
		{"malformed map kept", []string{"--help", "--labels", "novalue"}, ParseKindInvalidValue},
		{"extra positional kept", []string{"run", "x", "y", "--help"}, ParseKindTooManyArguments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A typed field, as generated code has, so a value of the wrong type is coerced
			// (and fails) at bind time. A sub-command run binds a type for that command.
			var root struct {
				App struct {
					Flags struct {
						Port int `rotini:"port"`
					}
				}
			}
			var out any = &root
			if len(tt.argv) > 0 && tt.argv[0] == "run" {
				out = &struct{}{}
			}
			err := NewParser().Parse(NewContextFor(scDef(t), tt.argv), out)
			if tt.kind == ParseKindUnspecified {
				if err != nil {
					t.Fatalf("Parse(%q) = %v, want no error", tt.argv, err)
				}
				return
			}
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Kind != tt.kind {
				t.Fatalf("Parse(%q) = %v, want ParseKind %v", tt.argv, err, tt.kind)
			}
		})
	}
}

// TestShortCircuit_flagValueReadable confirms the short-circuit flag itself reads true, which is
// how a handler knows to act, and that the other values given are still bound.
func TestShortCircuit_flagValueReadable(t *testing.T) {
	var in struct {
		App struct {
			Flags struct {
				Help  bool   `rotini:"help"`
				Level string `rotini:"level"`
			}
		}
	}
	if err := NewParser().Parse(NewContextFor(scDef(t), []string{"--help", "--level", "medium"}), &in); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !in.App.Flags.Help || in.App.Flags.Level != "medium" {
		t.Errorf("bound %+v, want Help=true Level=medium", in.App.Flags)
	}
}

type scChannelInputs struct {
	App struct {
		Flags struct {
			Help bool `rotini:"help"`
		}
		Arguments struct{}
		Env       struct {
			Region string `rotini:"region" recon:"region,required"`
			Port   int    `rotini:"port" recon:"port"`
		}
		Config struct {
			Token string `rotini:"token" recon:"api.token,required"`
		}
		Stdin *tbStdinReqPayload `stdin:"yaml,required"`
	}
}

func scChannelDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true}},
	}
}

// TestShortCircuit_readerWaivesChannelRequirements covers the env, config and stdin channels: a
// required env var, a required config key, a config file's schema and a required stdin payload
// are all waived, and still enforced without the flag.
func TestShortCircuit_readerWaivesChannelRequirements(t *testing.T) {
	cfg := writeConfig(t, "api:\n  other: x\n")
	reader := func() *InputReader {
		return NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{
			Name: "app", Path: cfg, Format: "yaml",
			Schema: `{"type":"object","required":["must"]}`,
		}}})
	}

	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	var in scChannelInputs
	if err := reader().Read(rtx, &in); err != nil {
		t.Fatalf("Read(--help) = %v, want every channel requirement waived", err)
	}
	if !in.App.Flags.Help {
		t.Error("Help not bound")
	}

	plain := NewContextFor(scChannelDef(), nil)
	plain.Stdin = strings.NewReader("")
	var in2 scChannelInputs
	if err := reader().Read(plain, &in2); err == nil {
		t.Fatal("Read(no flag) succeeded, want a requirement error")
	}
}

// TestShortCircuit_readerKeepsUnreadableInput confirms a waived run still reports input that
// cannot be read: an env value of the wrong type, and a config file that is not valid YAML.
func TestShortCircuit_readerKeepsUnreadableInput(t *testing.T) {
	t.Run("env value of the wrong type", func(t *testing.T) {
		t.Setenv("PORT", "abc")
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in scChannelInputs
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err == nil {
			t.Fatal("Read = nil, want the coercion error kept")
		}
	})
	t.Run("malformed config file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app.yaml")
		if err := os.WriteFile(path, []byte("api: [unclosed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rtx := NewContextFor(scChannelDef(), []string{"--help"})
		rtx.Stdin = strings.NewReader("")
		var in scChannelInputs
		err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: "yaml"}}}).Read(rtx, &in)
		if err == nil {
			t.Fatal("Read = nil, want the unreadable config file reported")
		}
	})
}

// TestShortCircuit_resolvedValuesStillBound confirms the waiver reads every channel: values that
// resolve are bound even though missing required ones are not reported.
func TestShortCircuit_resolvedValuesStillBound(t *testing.T) {
	t.Setenv("PORT", "8080")
	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	var in scChannelInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if in.App.Env.Port != 8080 {
		t.Errorf("Env.Port = %d, want 8080 bound alongside the waived required REGION", in.App.Env.Port)
	}
}

// TestShortCircuit_perChannelReaders confirms the per-channel layers apply the same waiver.
func TestShortCircuit_perChannelReaders(t *testing.T) {
	rtx := NewContextFor(scChannelDef(), []string{"--help"})
	rtx.Stdin = strings.NewReader("")
	if _, err := rtx.EnvInputs[scChannelInputs](); err != nil {
		t.Errorf("EnvInputs(--help) = %v, want the required env var waived", err)
	}
	if _, err := rtx.StdinInputs[scChannelInputs](); err != nil {
		t.Errorf("StdinInputs(--help) = %v, want the required payload waived", err)
	}
	plain := NewContextFor(scChannelDef(), nil)
	if _, err := plain.EnvInputs[scChannelInputs](); err == nil {
		t.Error("EnvInputs(no flag) = nil, want the required env var reported")
	}
}

// TestShortCircuit_completionStops confirms nothing is offered once a short-circuit flag is on
// the line, file fallback included, and that --help=false does not stop completion.
func TestShortCircuit_completionStops(t *testing.T) {
	def := scDef(t)
	rtx := NewContextFor(def, nil)
	if got := complete(def, []string{"--help", ""}, nil, rtx); len(got) != 0 {
		t.Errorf("complete after --help = %q, want nothing", got)
	}
	if hint := completionHintFor(def, []string{"--help", ""}); hint.Kind != "none" {
		t.Errorf("hint after --help = %+v, want kind none", hint)
	}
	if got := complete(def, []string{"--help=false", ""}, nil, rtx); len(got) == 0 {
		t.Error("complete after --help=false offered nothing, want the usual candidates")
	}
}
