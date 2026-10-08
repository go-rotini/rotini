package rotini

import (
	"strings"
	"testing"
)

// A flag's env or config fallback supplies a value the user never typed, so an error about
// that value names where it came from, as a value of the wrong type already did.

type fsInputs struct {
	App struct {
		Flags struct {
			Port int    `rotini:"port" recon:"port" env:"PORT"`
			Mode string `rotini:"mode" recon:"mode" env:"MODE"`
		}
		Arguments struct{}
	}
}

func fsDef(secret bool) Definition {
	high := 100.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Secret: secret, Maximum: &high},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"fast", "slow"}},
		},
	}
}

func fsRead(t *testing.T, def Definition, argv []string, meta InputSettings) error {
	t.Helper()
	var in fsInputs
	return NewInputReader(meta).Read(NewContextFor(def, argv), &in)
}

func TestFallbackSource_namedInValueErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		argv []string
		want string // "" = no error
	}{
		{"env bound", map[string]string{"PORT": "500"}, nil,
			"--port must be <= 100 (got 500) (from environment variable PORT)"},
		{"env enum", map[string]string{"MODE": "fastest"}, nil,
			`invalid value "fastest" for --mode (one of: fast, slow) (from environment variable MODE)`},
		{"argv bound names no source", nil, []string{"--port", "500"},
			"--port must be <= 100 (got 500)"},
		{"argv overrides an env value", map[string]string{"PORT": "500"}, []string{"--port", "5"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			err := fsRead(t, fsDef(false), tt.argv, InputSettings{})
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Read = %v, want no error", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Read = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFallbackSource_argvValueHasNoSuffixEvenWithEnvSet(t *testing.T) {
	t.Setenv("PORT", "5")
	err := fsRead(t, fsDef(false), []string{"--port", "500"}, InputSettings{})
	if err == nil || strings.Contains(err.Error(), "(from") {
		t.Fatalf("Read = %v, want a bound error without a source: the value was typed", err)
	}
}

func TestFallbackSource_configFile(t *testing.T) {
	cfg := writeConfig(t, "port: 500\n")
	err := fsRead(t, fsDef(false), nil, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})
	if err == nil || !strings.HasSuffix(err.Error(), `(from configuration file "app")`) {
		t.Fatalf("Read = %v, want the config file named", err)
	}
}

func TestFallbackSource_secretValueRedactedNameShown(t *testing.T) {
	t.Setenv("PORT", "500")
	err := fsRead(t, fsDef(true), nil, InputSettings{})
	if err == nil {
		t.Fatal("Read = nil, want a bound error")
	}
	msg := err.Error()
	if strings.Contains(msg, "500") || !strings.Contains(msg, "environment variable PORT") {
		t.Errorf("message %q: want the value redacted and the variable named", msg)
	}
}

// TestFallbackSource_reportMatchesInputs pins parity: the overlay path's report names the same
// source with the same words as rtx.Inputs.
func TestFallbackSource_reportMatchesInputs(t *testing.T) {
	t.Setenv("PORT", "500")
	rtx := NewContextFor(fsDef(false), nil)
	_, inputsErr := rtx.Inputs[fsInputs]()
	_, _, reportErr := rtx.InputsWithReport[fsInputs]()
	if inputsErr == nil || reportErr == nil || inputsErr.Error() != reportErr.Error() {
		t.Fatalf("Inputs = %v, InputsWithReport = %v; want the same error", inputsErr, reportErr)
	}
}

// fsRuleInputs carries one flag per value rule, each with an environment fallback.
type fsRuleInputs struct {
	App struct {
		Flags struct {
			Step  int      `rotini:"step" recon:"step" env:"STEP"`
			Name  string   `rotini:"name" recon:"name" env:"NAME"`
			Code  string   `rotini:"code" recon:"code" env:"CODE"`
			Sku   string   `rotini:"sku" recon:"sku" env:"SKU"`
			Tags  []string `rotini:"tags" recon:"tags" env:"TAGS"`
			Token string   `rotini:"token" recon:"token" env:"TOKEN"`
		}
		Arguments struct{}
	}
}

func fsRuleDef() Definition {
	five := 5.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "step", Identifiers: []string{"--step"}, Type: "int", MultipleOf: &five},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", MinLength: 3},
			{Name: "code", Identifiers: []string{"--code"}, Type: "string", Pattern: "^[a-z]+$"},
			{Name: "sku", Identifiers: []string{"--sku"}, Type: "string", Pattern: "^[A-Z]{3}$", PatternMessage: "three capital letters"},
			{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Separator: ",", MaxItems: 1},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
		},
	}
}

// TestFallbackSource_everyValueRule pins the suffix on each kind of value rule, and its
// absence on a missing required input, which no source supplied.
func TestFallbackSource_everyValueRule(t *testing.T) {
	tests := []struct {
		env, value string
		want       string // a substring of the message
	}{
		{"STEP", "7", "(from environment variable STEP)"},
		{"NAME", "ab", "(from environment variable NAME)"},
		{"CODE", "ABC", "(from environment variable CODE)"},
		{"SKU", "abc", `three capital letters (got "abc") (from environment variable SKU)`},
		{"TAGS", "a,b", "(from environment variable TAGS)"},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("TOKEN", "t")
			t.Setenv(tt.env, tt.value)
			var in fsRuleInputs
			err := NewInputReader(InputSettings{}).Read(NewContextFor(fsRuleDef(), nil), &in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Read = %v, want a message containing %q", err, tt.want)
			}
		})
	}
	t.Run("missing required carries no source", func(t *testing.T) {
		var in fsRuleInputs
		err := NewInputReader(InputSettings{}).Read(NewContextFor(fsRuleDef(), nil), &in)
		if err == nil || !strings.Contains(err.Error(), "--token") || strings.Contains(err.Error(), "(from") {
			t.Fatalf("Read = %v, want a missing --token without a source", err)
		}
	})
}
