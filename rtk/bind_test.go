package rtk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rotini/rotini"
)

// Generated-shape inputs for the binder tests: one command "app" with an argv flag,
// an env channel, and a config channel (recon tags as codegen emits them).
type tbFlags struct {
	Verbose bool `rotini:"verbose"`
}
type tbEnv struct {
	Region string `rotini:"region" recon:"region"`
}
type tbConfig struct {
	Endpoint string `rotini:"endpoint" recon:"api.endpoint"`
	Token    string `rotini:"token" recon:"api.token,required"`
}
type tbCmdInputs struct {
	Flags     tbFlags
	Arguments struct{}
	Env       tbEnv
	Config    tbConfig
}
type tbInputs struct {
	App tbCmdInputs
}

func tbDef() rotini.Definition {
	return rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestBinder_fillsEnvAndConfig(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n  token: secret123\n")
	t.Setenv("REGION", "us-west")

	rtx := rotini.NewContextFor(tbDef(), []string{"--verbose"})
	binder := NewBinder(rotini.BindMeta{ConfigFiles: []rotini.ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	var in tbInputs
	if err := binder.Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !in.App.Flags.Verbose {
		t.Errorf("argv flag Verbose not bound")
	}
	if in.App.Env.Region != "us-west" { // recon env: REGION -> "region"
		t.Errorf("Env.Region = %q, want us-west", in.App.Env.Region)
	}
	if in.App.Config.Endpoint != "https://api.example" {
		t.Errorf("Config.Endpoint = %q, want https://api.example", in.App.Config.Endpoint)
	}
	if in.App.Config.Token != "secret123" {
		t.Errorf("Config.Token = %q, want secret123", in.App.Config.Token)
	}
}

func TestBinder_requiredConfigMissing(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n") // no api.token
	rtx := rotini.NewContextFor(tbDef(), nil)
	binder := NewBinder(rotini.BindMeta{ConfigFiles: []rotini.ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	var in tbInputs
	if err := binder.Bind(rtx, &in); err == nil {
		t.Fatal("expected an error for a missing required config value (recon required)")
	}
}

func TestBinder_noConfigFilesLeavesConfigZero(t *testing.T) {
	// With no config sources, config fields stay zero; a non-required env still binds.
	t.Setenv("REGION", "eu-central")
	rtx := rotini.NewContextFor(tbDef(), nil)
	binder := NewBinder(rotini.BindMeta{}) // no config files

	var s tbNoReqInputs
	if err := binder.Bind(rtx, &s); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if s.App.Env.Region != "eu-central" {
		t.Errorf("Env.Region = %q, want eu-central", s.App.Env.Region)
	}
	if s.App.Config.Endpoint != "" {
		t.Errorf("Config.Endpoint = %q, want empty (no config source)", s.App.Config.Endpoint)
	}
}

// Fallback-flag shapes: a flag with a config key reconciles argv > env > config > default.
type tbFbFlags struct {
	Color string `rotini:"color" recon:"create.color"`
}
type tbFbCmd struct {
	Flags     tbFbFlags
	Arguments struct{}
}
type tbFbInputs struct{ App tbFbCmd }

func tbFbDef() rotini.Definition {
	return rotini.Definition{
		Name: "app", Handler: "App",
		Flags: []rotini.FlagDef{{Name: "color", Identifiers: []string{"--color"}, Type: "string", Default: "blue"}},
	}
}

func TestBinder_flagFallbackPrecedence(t *testing.T) {
	cfg := writeConfig(t, "create:\n  color: red\n")
	meta := rotini.BindMeta{ConfigFiles: []rotini.ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	cases := []struct {
		name string
		argv []string
		env  string // CREATE_COLOR; "" = leave unset
		want string
	}{
		{"argv wins over all", []string{"--color", "green"}, "teal", "green"},
		{"env over config and default", nil, "teal", "teal"},
		{"config over default", nil, "", "red"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("CREATE_COLOR", c.env)
			}
			rtx := rotini.NewContextFor(tbFbDef(), c.argv)
			var in tbFbInputs
			if err := NewBinder(meta).Bind(rtx, &in); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if in.App.Flags.Color != c.want {
				t.Errorf("Color = %q, want %q", in.App.Flags.Color, c.want)
			}
		})
	}

	// No source provides the flag → the Parser-applied default is kept.
	t.Run("default kept when no source", func(t *testing.T) {
		rtx := rotini.NewContextFor(tbFbDef(), nil)
		var in tbFbInputs
		if err := NewBinder(rotini.BindMeta{}).Bind(rtx, &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Flags.Color != "blue" {
			t.Errorf("Color = %q, want blue (FlagDef default)", in.App.Flags.Color)
		}
	})
}

// tbNoReqInputs mirrors tbInputs but with no required config field, so the
// no-config-files case binds cleanly.
type tbNoReqInputs struct{ App tbNoReqCmd }
type tbNoReqCmd struct {
	Flags     tbFlags
	Arguments struct{}
	Env       tbEnv
	Config    struct {
		Endpoint string `rotini:"endpoint" recon:"api.endpoint"`
	}
}
