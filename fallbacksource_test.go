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
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Secret: secret, Constraints: Constraints{Maximum: &high}},
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
