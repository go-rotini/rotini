package rotini

import (
	"testing"
)

// A bool follows the empty-variable rule every type does: an env input set to "" is given, so
// "" must parse as a bool and doesn't; a flag's environment fallback set to "" is unset.
func TestInputReader_emptyBoolVariable(t *testing.T) {
	type inputs struct {
		App struct {
			Flags struct {
				Quiet bool `rotini:"quiet" recon:"quiet" env:"APP_QUIET"`
			}
			Arguments struct{}
			Env       struct {
				NoColor bool `rotini:"no-color" recon:"no-color" env:"APP_NO_COLOR"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "quiet", Identifiers: []string{"--quiet"}, Type: "bool"}}}

	t.Run("flag fallback", func(t *testing.T) {
		t.Setenv("APP_QUIET", "")
		var in inputs
		if err := NewInputReader(InputSettings{}).Read(NewContextFor(def, nil), &in); err != nil || in.App.Flags.Quiet {
			t.Fatalf("Quiet = %v, err = %v; want false and no error", in.App.Flags.Quiet, err)
		}
	})
	t.Run("env input", func(t *testing.T) {
		t.Setenv("APP_NO_COLOR", "")
		var in inputs
		if err := NewInputReader(InputSettings{}).Read(NewContextFor(def, nil), &in); err == nil || CategoryOf(err) != CategoryUsage {
			t.Fatalf("err = %v, want a usage error for an empty bool", err)
		}
	})
}
