package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

// Generated-shape inputs for the input reader tests: one command "app" with an argv flag,
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

func tbDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
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

func TestInputReader_fillsEnvAndConfig(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n  token: secret123\n")
	t.Setenv("REGION", "us-west")

	rtx := NewContextFor(tbDef(), []string{"--verbose"})
	reader := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	var in tbInputs
	if err := reader.Read(rtx, &in); err != nil {
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

// TestInputReader_customSources pins InputSettings.Sources: custom recon sources join the
// config layer after the declared configuration_files and serve both config inputs and flags'
// config fallbacks, through Read and the per-channel surface alike.
func TestInputReader_customSources(t *testing.T) {
	vault := func() recon.Source {
		// MapSource takes the nested shape a config decoder produces.
		return recon.NewMapSource("vault", map[string]any{
			"api": map[string]any{
				"endpoint": "vault-endpoint",
				"token":    "vault-token",
			},
		})
	}

	t.Run("sources alone supply config inputs", func(t *testing.T) {
		rtx := NewContextFor(tbDef(), nil)
		var in tbInputs
		err := NewInputReader(InputSettings{Sources: []recon.Source{vault()}}).Read(rtx, &in)
		if err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Config.Endpoint != "vault-endpoint" || in.App.Config.Token != "vault-token" {
			t.Errorf("config = %q/%q, want the custom source's values", in.App.Config.Endpoint, in.App.Config.Token)
		}
	})

	t.Run("declared files beat custom sources", func(t *testing.T) {
		cfg := writeConfig(t, "api:\n  endpoint: file-endpoint\n") // no token: vault still supplies it
		rtx := NewContextFor(tbDef(), nil)
		var in tbInputs
		meta := InputSettings{
			ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}},
			Sources:     []recon.Source{vault()},
		}
		if err := NewInputReader(meta).Read(rtx, &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Config.Endpoint != "file-endpoint" {
			t.Errorf("endpoint = %q, want the declared file to win", in.App.Config.Endpoint)
		}
		if in.App.Config.Token != "vault-token" {
			t.Errorf("token = %q, want the custom source to fill the gap", in.App.Config.Token)
		}
	})

	t.Run("per-channel surface sees sources via InputSettings", func(t *testing.T) {
		rtx := NewContextFor(tbDef(), nil)
		rtx.WithInputSettings(InputSettings{Sources: []recon.Source{vault()}})
		files, err := rtx.FileInputs[tbInputs]()
		if err != nil {
			t.Fatalf("FileInputs: %v", err)
		}
		if files.Values.App.Config.Token != "vault-token" {
			t.Errorf("files layer token = %q, want the custom source's value", files.Values.App.Config.Token)
		}
	})
}

func TestInputReader_requiredConfigMissing(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n") // no api.token
	rtx := NewContextFor(tbDef(), nil)
	reader := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	var in tbInputs
	if err := reader.Read(rtx, &in); err == nil {
		t.Fatal("expected an error for a missing required config value (recon required)")
	}
}

func TestInputReader_noConfigFilesLeavesConfigZero(t *testing.T) {
	// With no config sources, config fields stay zero; a non-required env still binds.
	t.Setenv("REGION", "eu-central")
	rtx := NewContextFor(tbDef(), nil)
	reader := NewInputReader(InputSettings{}) // no config files

	var s tbNoReqInputs
	if err := reader.Read(rtx, &s); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if s.App.Env.Region != "eu-central" {
		t.Errorf("Env.Region = %q, want eu-central", s.App.Env.Region)
	}
	if s.App.Config.Endpoint != "" {
		t.Errorf("Config.Endpoint = %q, want empty (no config source)", s.App.Config.Endpoint)
	}
}

// Discovery shapes (spec discover:): a walk-up entry found in an ancestor of
// the working directory, and an xdg entry under $XDG_CONFIG_HOME/<app>.
func TestInputReader_discoverWalkUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// The file lives two levels above the working directory.
	if err := os.WriteFile(filepath.Join(root, ".app.yaml"), []byte("api:\n  endpoint: from-walk-up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	meta := InputSettings{ConfigFiles: []ConfigFile{{
		Name: "project", Format: "yaml",
		Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"},
	}}}
	var in tbNoReqInputs
	if err := NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Endpoint != "from-walk-up" {
		t.Errorf("Endpoint = %q, want from-walk-up (discovered above the cwd)", in.App.Config.Endpoint)
	}

	// The nearest directory wins: a file in the cwd shadows the ancestor's.
	if err := os.WriteFile(filepath.Join(nested, ".app.yaml"), []byte("api:\n  endpoint: from-cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var in2 tbNoReqInputs
	if err := NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in2); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in2.App.Config.Endpoint != "from-cwd" {
		t.Errorf("Endpoint = %q, want from-cwd (nearest directory wins)", in2.App.Config.Endpoint)
	}
}

func TestInputReader_discoverXDG(t *testing.T) {
	xdg := t.TempDir()
	appDir := filepath.Join(xdg, "acme")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.yaml"), []byte("api:\n  endpoint: from-xdg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	meta := InputSettings{ConfigFiles: []ConfigFile{{
		Name: "user", Format: "yaml",
		Discover: &DiscoverDef{Strategy: "xdg", App: "acme", File: "config.yaml"},
	}}}
	var in tbNoReqInputs
	if err := NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Endpoint != "from-xdg" {
		t.Errorf("Endpoint = %q, want from-xdg", in.App.Config.Endpoint)
	}

	// An absent discovered file is simply absent — same as a missing fixed path.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var in2 tbNoReqInputs
	if err := NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in2); err != nil {
		t.Fatalf("Bind(absent discovered file): %v", err)
	}
	if in2.App.Config.Endpoint != "" {
		t.Errorf("Endpoint = %q, want empty", in2.App.Config.Endpoint)
	}
}

// Config-schema shapes (spec configuration_files[].schema): the loaded
// document is validated at bind time, before any value is read.
const tbCfgSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["api"],"properties":{"api":{"type":"object","required":["endpoint"],"properties":{"endpoint":{"type":"string"}}}}}`

func TestInputReader_configSchemaValidation(t *testing.T) {
	bind := func(t *testing.T, meta InputSettings) error {
		t.Helper()
		var in tbNoReqInputs
		return NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in)
	}
	withSchema := func(cf ConfigFile) InputSettings {
		cf.Schema = tbCfgSchema
		return InputSettings{ConfigFiles: []ConfigFile{cf}}
	}

	t.Run("conforming file passes", func(t *testing.T) {
		cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n")
		if err := bind(t, withSchema(ConfigFile{Name: "app", Path: cfg, Format: "yaml"})); err != nil {
			t.Errorf("Bind = %v, want nil", err)
		}
	})
	t.Run("non-conforming file errors naming the file", func(t *testing.T) {
		cfg := writeConfig(t, "api:\n  retries: 3\n") // api.endpoint missing
		err := bind(t, withSchema(ConfigFile{Name: "app", Path: cfg, Format: "yaml"}))
		if err == nil || !strings.Contains(err.Error(), cfg) || !strings.Contains(err.Error(), "app") {
			t.Errorf("Bind = %v, want a violation naming the entry and the file", err)
		}
	})
	t.Run("absent file passes vacuously", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.yaml")
		if err := bind(t, withSchema(ConfigFile{Name: "app", Path: missing, Format: "yaml"})); err != nil {
			t.Errorf("Bind(absent) = %v, want nil — absence is required's concern", err)
		}
	})
	t.Run("discovered file is validated", func(t *testing.T) {
		xdg := t.TempDir()
		if err := os.MkdirAll(filepath.Join(xdg, "acme"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xdg, "acme", "config.yaml"), []byte("api:\n  retries: 3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", xdg)
		meta := withSchema(ConfigFile{Name: "user", Format: "yaml",
			Discover: &DiscoverDef{Strategy: "xdg", App: "acme", File: "config.yaml"}})
		if err := bind(t, meta); err == nil {
			t.Error("Bind(discovered non-conforming file) = nil, want the validation error")
		}
	})
	t.Run("config_source-supplied file is validated", func(t *testing.T) {
		bad := writeConfig(t, "api:\n  retries: 3\n")
		meta := withSchema(ConfigFile{Name: "app", Path: filepath.Join(t.TempDir(), "declared.yaml"), Format: "yaml",
			PathFrom: &PathFromDef{Env: "APP_CONFIG"}})
		t.Setenv("APP_CONFIG", bad)
		if err := bind(t, meta); err == nil {
			t.Error("Bind(config_source non-conforming file) = nil, want the validation error")
		}
	})
	t.Run("a pinned input's registry validates too", func(t *testing.T) {
		bad := writeConfig(t, "api:\n  retries: 3\n")
		meta := withSchema(ConfigFile{Name: "app", Path: bad, Format: "yaml"})
		var in struct {
			App struct {
				Flags     struct{}
				Arguments struct{}
				Config    struct {
					Endpoint string `rotini:"endpoint" recon:"api.endpoint" cfgfile:"app"`
				}
			}
		}
		if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in); err == nil {
			t.Error("Bind(pinned, non-conforming file) = nil, want the validation error")
		}
	})
}

// config_source shapes (spec config_source): a --config flag (and an env
// fallback variable) supplies the file the config channel then reads — the
// declarative two-phase parse.
type tbCfgSrcInputs struct {
	App struct {
		Flags struct {
			Config string `rotini:"config"`
		}
		Arguments struct{}
		Config    struct {
			Endpoint string `rotini:"endpoint" recon:"api.endpoint"`
		}
	}
}

func tbCfgSrcDef(withDefault string) Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "config", Identifiers: []string{"--config"}, Type: "string", Default: withDefault}},
	}
}

func TestInputReader_configSourceTwoPhase(t *testing.T) {
	declared := writeConfig(t, "api:\n  endpoint: from-declared\n")
	flagged := writeConfig(t, "api:\n  endpoint: from-flag\n")
	fromEnv := writeConfig(t, "api:\n  endpoint: from-env\n")
	meta := InputSettings{ConfigFiles: []ConfigFile{{
		Name: "app", Path: declared, Format: "yaml",
		PathFrom: &PathFromDef{Flag: "config", Env: "APP_CONFIG"},
	}}}
	bind := func(t *testing.T, def Definition, argv []string) tbCfgSrcInputs {
		t.Helper()
		var in tbCfgSrcInputs
		if err := NewInputReader(meta).Read(NewContextFor(def, argv), &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		return in
	}

	t.Run("argv flag wins over everything", func(t *testing.T) {
		t.Setenv("APP_CONFIG", fromEnv)
		in := bind(t, tbCfgSrcDef(declared), []string{"--config", flagged})
		if in.App.Config.Endpoint != "from-flag" {
			t.Errorf("Endpoint = %q, want from-flag", in.App.Config.Endpoint)
		}
	})
	t.Run("env var over flag default and declared path", func(t *testing.T) {
		t.Setenv("APP_CONFIG", fromEnv)
		in := bind(t, tbCfgSrcDef(declared), nil)
		if in.App.Config.Endpoint != "from-env" {
			t.Errorf("Endpoint = %q, want from-env", in.App.Config.Endpoint)
		}
	})
	t.Run("flag default over the declared path", func(t *testing.T) {
		in := bind(t, tbCfgSrcDef(flagged), nil)
		if in.App.Config.Endpoint != "from-flag" {
			t.Errorf("Endpoint = %q, want from-flag (the flag's default)", in.App.Config.Endpoint)
		}
	})
	t.Run("nothing supplied: the declared path stands", func(t *testing.T) {
		in := bind(t, tbCfgSrcDef(""), nil)
		if in.App.Config.Endpoint != "from-declared" {
			t.Errorf("Endpoint = %q, want from-declared", in.App.Config.Endpoint)
		}
	})
	t.Run("an explicitly-supplied missing file errors", func(t *testing.T) {
		var in tbCfgSrcInputs
		err := NewInputReader(meta).Read(NewContextFor(tbCfgSrcDef(""), []string{"--config", "/nonexistent/app.yaml"}), &in)
		if err == nil {
			t.Error("Bind = nil error, want a missing-file error — the user explicitly asked for that file")
		}
	})
}

// Explicit-env-var shape: an env field whose `env:"…"` tag pins the environment
// variable (the spec's `variable`), overriding recon's SNAKE_UPPER default.
type tbEnvVarInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Token string `rotini:"token" recon:"token" env:"WIDGET_TOKEN"`
		}
	}
}

func TestInputReader_explicitEnvVar(t *testing.T) {
	t.Setenv("WIDGET_TOKEN", "s3cret")
	t.Setenv("TOKEN", "wrong-default") // the SNAKE_UPPER default — must be ignored

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbEnvVarInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Env.Token != "s3cret" {
		t.Errorf("Env.Token = %q, want s3cret (from $WIDGET_TOKEN, not $TOKEN)", in.App.Env.Token)
	}

	// The explicit variable must resolve on its own, with no convention-named
	// $TOKEN set.
	os.Unsetenv("TOKEN")
	var alone tbEnvVarInputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &alone); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if alone.App.Env.Token != "s3cret" {
		t.Errorf("Env.Token = %q, want s3cret from the explicit-only variable", alone.App.Env.Token)
	}
}

// Pinned-config shapes (spec file:): an input read from one named
// configuration_files entry, not the merged precedence chain.
func TestInputReader_configFilePinned(t *testing.T) {
	system := writeConfig(t, "api:\n  endpoint: from-system\n  token: sys-token\n")
	user := writeConfig(t, "api:\n  endpoint: from-user\n")
	meta := InputSettings{ConfigFiles: []ConfigFile{
		{Name: "system", Path: system, Format: "yaml"}, // higher precedence
		{Name: "user", Path: user, Format: "yaml"},
	}}

	type pinned struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				// Pinned to "user": must not see system's value despite precedence.
				Endpoint string `rotini:"endpoint" recon:"api.endpoint" cfgfile:"user"`
			}
		}
	}
	var in pinned
	if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Endpoint != "from-user" {
		t.Errorf("Endpoint = %q, want from-user — file: pins the source", in.App.Config.Endpoint)
	}

	// A pinned required key is judged against its file: present elsewhere
	// doesn't count.
	type pinnedReq struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				Token string `rotini:"token" recon:"api.token,required" cfgfile:"user"`
			}
		}
	}
	var in2 pinnedReq
	if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in2); err == nil {
		t.Error("Bind = nil error, want missing-required — api.token lives in system, not the pinned user file")
	}

	// A pin naming an undeclared entry is a loud error, never a silent merge.
	type pinnedBad struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				X string `rotini:"x" recon:"api.endpoint" cfgfile:"nope"`
			}
		}
	}
	var in3 pinnedBad
	if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in3); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Bind(unknown pin) = %v, want an unknown-configuration-file error", err)
	}
}

// Nested-env shapes (spec nesting:): a map-typed env input aggregating a
// variable family — `envnest:"<BASE>,<sep>"` as codegen emits it.
type tbNestInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			HTTP map[string]any `rotini:"http" recon:"http" envnest:"ACME_HTTP,__"`
		}
	}
}

func TestInputReader_envNesting(t *testing.T) {
	t.Setenv("ACME_HTTP__TIMEOUT", "30")
	t.Setenv("ACME_HTTP__RETRY__MAX", "9")
	t.Setenv("ACME_HTTPX", "decoy") // wrong separator boundary — not family

	var in tbNestInputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got := in.App.Env.HTTP["timeout"]; got != "30" {
		t.Errorf("http[timeout] = %#v, want %q", got, "30")
	}
	retry, ok := in.App.Env.HTTP["retry"].(map[string]any)
	if !ok || retry["max"] != "9" {
		t.Errorf("http[retry] = %#v, want nested {max: 9}", in.App.Env.HTTP["retry"])
	}
	if len(in.App.Env.HTTP) != 2 {
		t.Errorf("http = %#v, want exactly timeout + retry (the decoy must not join the family)", in.App.Env.HTTP)
	}
}

func TestInputReader_envNestingRequired(t *testing.T) {
	// No ACME_DB__* variables exist: a required nested family errors via recon.
	type reqNest struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				DB map[string]any `rotini:"db" recon:"db" envnest:"ACME_DB,__,required"`
			}
		}
	}
	var in reqNest
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in); err == nil {
		t.Error("Bind = nil error, want a missing-required error for the empty variable family")
	}
}

// Stdin-payload shapes: a leaf command with a typed stdin payload (format on the tag).
type tbStdinPayload struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}
type tbStdinCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *tbStdinPayload `stdin:"yaml"`
}
type tbStdinInputs struct{ App tbStdinCmd }

// withPipedStdin replaces os.Stdin with a pipe carrying body for the test, restoring
// it afterward. An empty body yields an immediately-closed pipe (EOF, no data).
func withPipedStdin(t *testing.T, body string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
	go func() { _, _ = w.WriteString(body); _ = w.Close() }()
}

func TestInputReader_decodesStdin(t *testing.T) {
	withPipedStdin(t, "kind: Widget\nname: foo\n")
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	var in tbStdinInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin == nil {
		t.Fatal("Stdin payload was not decoded")
	}
	if in.App.Stdin.Kind != "Widget" || in.App.Stdin.Name != "foo" {
		t.Errorf("Stdin = %+v, want {Widget foo}", in.App.Stdin)
	}
}

// The InputReader reads its stdin channel from [Context.Stdin] (which the Program sets from
// Program.WithStdin), so a test can supply input via an ordinary reader without touching
// the process's os.Stdin.
func TestInputReader_decodesStdinFromContextStdin(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("kind: Widget\nname: foo\n")

	var in tbStdinInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin == nil || in.App.Stdin.Kind != "Widget" || in.App.Stdin.Name != "foo" {
		t.Errorf("Stdin = %+v, want {Widget foo} decoded from rtx.Stdin", in.App.Stdin)
	}
}

func TestInputReader_noStdinLeavesNil(t *testing.T) {
	withPipedStdin(t, "") // nothing piped → EOF, no data
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	var in tbStdinInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin != nil {
		t.Errorf("Stdin = %+v, want nil with no piped input", in.App.Stdin)
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

func tbFbDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "color", Identifiers: []string{"--color"}, Type: "string", Default: "blue"}},
	}
}

func TestInputReader_flagFallbackPrecedence(t *testing.T) {
	cfg := writeConfig(t, "create:\n  color: red\n")
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

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
			rtx := NewContextFor(tbFbDef(), c.argv)
			var in tbFbInputs
			if err := NewInputReader(meta).Read(rtx, &in); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if in.App.Flags.Color != c.want {
				t.Errorf("Color = %q, want %q", in.App.Flags.Color, c.want)
			}
		})
	}

	// No source provides the flag → the Parser-applied default is kept.
	t.Run("default kept when no source", func(t *testing.T) {
		rtx := NewContextFor(tbFbDef(), nil)
		var in tbFbInputs
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Flags.Color != "blue" {
			t.Errorf("Color = %q, want blue (FlagDef default)", in.App.Flags.Color)
		}
	})
}

type tbPassInputs struct {
	App struct {
		Flags struct {
			Region string `rotini:"region" recon:"region"`
		}
		Arguments struct{}
	}
	Exec struct {
		Flags     struct{}
		Arguments struct {
			Cmd []string `rotini:"cmd"`
		}
	}
}

func tbPassDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "region", Identifiers: []string{"--region"}, Type: "string"}},
		Commands: []CommandDef{{
			Name: "exec", Handler: "AppExec", Passthrough: true,
			Arguments: []ArgDef{{Name: "cmd", Type: "[]string", Variadic: true}},
		}},
	}
}

// TestInputReader_passthroughWordsDontSetAFallbackFlag pins that a flag's identifier among a
// passthrough command's raw words is not the flag being set: its env value still applies.
func TestInputReader_passthroughWordsDontSetAFallbackFlag(t *testing.T) {
	t.Setenv("REGION", "from-env")
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"exec", "aws", "s3", "ls"}, "from-env"},
		{[]string{"exec", "aws", "--region", "us-west-2", "s3", "ls"}, "from-env"},
		{[]string{"exec", "aws", "--region=us-west-2"}, "from-env"},
		{[]string{"--region", "eu-west-1", "exec", "aws", "--region", "us-west-2"}, "eu-west-1"},
	} {
		rtx := NewContextFor(tbPassDef(), c.argv)
		var in tbPassInputs
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
			t.Fatalf("%v: %v", c.argv, err)
		}
		if in.App.Flags.Region != c.want {
			t.Errorf("%v: Region = %q, want %q", c.argv, in.App.Flags.Region, c.want)
		}
		if got := in.Exec.Arguments.Cmd; len(got) == 0 || got[0] != "aws" {
			t.Errorf("%v: Cmd = %q, want the raw words", c.argv, got)
		}
	}
}

// Required-fallback shapes: a *required* flag that declares a recon key, with no
// default, must be satisfiable from env or config, not only from argv.
type tbReqFlags struct {
	Token string `rotini:"token" recon:"api.token"`
}
type tbReqCmd struct {
	Flags     tbReqFlags
	Arguments struct{}
}
type tbReqInputs struct{ App tbReqCmd }

func tbReqDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}},
	}
}

func TestInputReader_requiredFlagSatisfiedByConfig(t *testing.T) {
	cfg := writeConfig(t, "api:\n  token: from-config\n")
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbReqDef(), nil) // not on argv
	var in tbReqInputs
	if err := NewInputReader(meta).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v (a required flag should be satisfiable via config)", err)
	}
	if in.App.Flags.Token != "from-config" {
		t.Errorf("Token = %q, want from-config", in.App.Flags.Token)
	}
}

func TestInputReader_requiredFlagSatisfiedByEnv(t *testing.T) {
	t.Setenv("API_TOKEN", "from-env") // SNAKE_UPPER of recon key "api.token"

	// No config files: also exercises the path where env is the only fallback source.
	rtx := NewContextFor(tbReqDef(), nil)
	var in tbReqInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v (a required flag should be satisfiable via env)", err)
	}
	if in.App.Flags.Token != "from-env" {
		t.Errorf("Token = %q, want from-env", in.App.Flags.Token)
	}
}

func TestInputReader_requiredFlagMissingEverywhere(t *testing.T) {
	rtx := NewContextFor(tbReqDef(), nil) // no argv, no env, no config
	var in tbReqInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err == nil {
		t.Fatal("expected a missing-required error when a required fallback flag is in no source")
	}
}

// Enum-on-reconciled: an env/config-supplied flag value must be enum-checked too.
type tbEnumFlags struct {
	Color string `rotini:"color" recon:"create.color"`
}
type tbEnumCmd struct {
	Flags     tbEnumFlags
	Arguments struct{}
}
type tbEnumInputs struct{ App tbEnumCmd }

func tbEnumDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "color", Identifiers: []string{"--color"}, Type: "string", Enum: []string{"red", "green", "blue"}}},
	}
}

func TestInputReader_enumCheckedOnReconciledValue(t *testing.T) {
	cfg := writeConfig(t, "create:\n  color: teal\n") // teal is not in the enum
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbEnumDef(), nil)
	var in tbEnumInputs
	if err := NewInputReader(meta).Read(rtx, &in); err == nil {
		t.Fatal("expected an enum error for a config-supplied value outside the declared enum")
	}
}

// Constraints are enforced over the reconciled value, so a bound that a
// config-supplied flag violates is caught.
type tbPortFlags struct {
	Port int `rotini:"port" recon:"create.port"`
}
type tbPortCmd struct {
	Flags     tbPortFlags
	Arguments struct{}
}
type tbPortInputs struct{ App tbPortCmd }

func tbPortDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "port", Identifiers: []string{"--port"}, Type: "int",
			Minimum: new(1.0), Maximum: new(65535.0),
		}},
	}
}

func TestInputReader_constraintCheckedOnReconciledValue(t *testing.T) {
	cfg := writeConfig(t, "create:\n  port: 70000\n") // above the declared maximum
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbPortDef(), nil)
	var in tbPortInputs
	if err := NewInputReader(meta).Read(rtx, &in); err == nil || !strings.Contains(err.Error(), "must be <= 65535") {
		t.Fatalf("Bind error = %v, want a max-bound violation for the config-supplied port", err)
	}
}

// Channel-constraint shapes carry the validation struct-tags codegen emits on env/
// config fields, which the input reader enforces over the reconciled value.
type tbEnvPort struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Port int `rotini:"port" recon:"port" min:"1" max:"65535"`
		}
	}
}

type tbCfgName struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Name string `rotini:"name" recon:"app.name" minlen:"2" maxlen:"5" pattern:"^[a-z]+$"`
		}
	}
}

func tbAppDef() Definition { return Definition{Name: "app", Handler: "App"} }

func TestInputReader_envConstraintEnforced(t *testing.T) {
	t.Setenv("PORT", "70000") // above max
	var in tbEnvPort
	err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in)
	if err == nil || !strings.Contains(err.Error(), "<= 65535") {
		t.Fatalf("Bind err = %v, want a max-bound violation for env PORT", err)
	}
}

func TestInputReader_envConstraintValid(t *testing.T) {
	t.Setenv("PORT", "8080")
	var in tbEnvPort
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Env.Port != 8080 {
		t.Errorf("Port = %d, want 8080", in.App.Env.Port)
	}
}

func TestInputReader_channelConstraintAbsentSkipped(t *testing.T) {
	// Genuinely unset (not empty): the field is never sourced, so the constraint is
	// skipped and the value stays zero — absence is `required`'s job, not the bounds'.
	if prev, had := os.LookupEnv("PORT"); had {
		os.Unsetenv("PORT")
		t.Cleanup(func() { os.Setenv("PORT", prev) })
	}
	var in tbEnvPort
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("absent env value should skip its constraint check: %v", err)
	}
	if in.App.Env.Port != 0 {
		t.Errorf("Port = %d, want 0 (unset)", in.App.Env.Port)
	}
}

func TestInputReader_configConstraintEnforced(t *testing.T) {
	cfg := writeConfig(t, "app:\n  name: TOOLONG\n") // length 7 > maxlen 5 (and not lowercase)
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbCfgName
	if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in); err == nil {
		t.Fatal("expected a constraint violation for the config value app.name")
	}
}

func TestInputReader_configConstraintValid(t *testing.T) {
	cfg := writeConfig(t, "app:\n  name: abc\n")
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbCfgName
	if err := NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Name != "abc" {
		t.Errorf("Name = %q, want abc", in.App.Config.Name)
	}
}

// Stdin-validation shape: a payload whose schema (in InputSettings) the input reader checks
// before binding.
type tbStdinValPayload struct {
	Port int `json:"port"`
}
type tbStdinValCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *tbStdinValPayload `stdin:"yaml"`
}
type tbStdinValInputs struct{ App tbStdinValCmd }

const tbStdinSchema = `{"type":"object","properties":{"port":{"type":"integer","minimum":1,"maximum":65535}}}`

func tbStdinMeta() InputSettings {
	return InputSettings{StdinSchemas: map[string]string{"tbStdinValPayload": tbStdinSchema}}
}

func TestInputReader_stdinPayloadRejectedBySchema(t *testing.T) {
	withPipedStdin(t, "port: 70000\n") // above the schema maximum
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbStdinValInputs
	if err := NewInputReader(tbStdinMeta()).Read(rtx, &in); err == nil {
		t.Fatal("expected the stdin payload to be rejected (port above maximum)")
	}
}

func TestInputReader_stdinPayloadValid(t *testing.T) {
	withPipedStdin(t, "port: 8080\n")
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbStdinValInputs
	if err := NewInputReader(tbStdinMeta()).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin == nil || in.App.Stdin.Port != 8080 {
		t.Errorf("Stdin = %+v, want port 8080", in.App.Stdin)
	}
}

// Secret channel shape: a config field marked secret (recon `,secret`) with a
// constraint, whose value must be redacted in the constraint-violation error.
type tbSecretCfg struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Token string `rotini:"token" recon:"app.token,secret" minlen:"8"`
		}
	}
}

func TestInputReader_secretChannelValueRedacted(t *testing.T) {
	cfg := writeConfig(t, "app:\n  token: short\n") // length 5 < minlen 8
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbSecretCfg
	err := NewInputReader(meta).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	if err == nil {
		t.Fatal("expected a length-constraint violation")
	}
	if strings.Contains(err.Error(), "short") {
		t.Errorf("secret config value leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error should redact the secret value/length: %v", err)
	}
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

// Required-stdin shapes: the generated `stdin:"<format>,required"` tag makes an
// empty stdin an error instead of a nil payload.
type tbStdinReqPayload struct {
	Kind string `recon:"kind"`
}

type tbStdinReqCommandInputs struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *tbStdinReqPayload `stdin:"yaml,required"`
}

type tbStdinReqInputs struct {
	App tbStdinReqCommandInputs
}

// TestInputReader_stdinRequired confirms the spec's stdin required: true is honored:
// empty stdin errors, a piped document binds.
func TestInputReader_stdinRequired(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}

	rtx := NewContextFor(def, nil)
	rtx.Stdin = strings.NewReader("")
	var in tbStdinReqInputs
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "required stdin payload is empty") {
		t.Errorf("Bind(empty required stdin) = %v, want a required-stdin error", err)
	}

	rtx2 := NewContextFor(def, nil)
	rtx2.Stdin = strings.NewReader("kind: demo\n")
	var in2 tbStdinReqInputs
	if err := NewInputReader(InputSettings{}).Read(rtx2, &in2); err != nil {
		t.Fatalf("Bind(piped required stdin) = %v", err)
	}
	if in2.App.Stdin == nil || in2.App.Stdin.Kind != "demo" {
		t.Errorf("payload = %+v, want Kind=demo", in2.App.Stdin)
	}
}

func TestParseStdinTag(t *testing.T) {
	for tag, want := range map[string]struct {
		format   string
		required bool
	}{
		"yaml":          {"yaml", false},
		"yaml,required": {"yaml", true},
		"json,required": {"json", true},
		"json,optional": {"json", false}, // unknown markers are ignored
	} {
		format, required := parseStdinTag(tag)
		if format != want.format || required != want.required {
			t.Errorf("parseStdinTag(%q) = (%q, %v), want (%q, %v)", tag, format, required, want.format, want.required)
		}
	}
}

// TestInputReader_chainConfigFiles pins the config_files cascade: the union along the invoked
// chain, nearest-wins (deepest command first), off-branch sources excluded, and unscoped
// (Scope=="") sources always in scope, last.
func TestInputReader_chainConfigFiles(t *testing.T) {
	b := &InputReader{configFiles: []ConfigFile{
		{Name: "rootA", Scope: "app"},
		{Name: "rootB", Scope: "app"},
		{Name: "deploy", Scope: "app/deploy"},
		{Name: "aws", Scope: "app/deploy/aws"},
		{Name: "sibling", Scope: "app/build"}, // off-branch — excluded
		{Name: "global", Scope: ""},           // unscoped — always in scope, last
	}}
	chain := []Command{{Name: "app"}, {Name: "deploy"}, {Name: "aws"}}
	var names []string
	for _, f := range b.chainConfigFiles(chain) {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, ","), "aws,deploy,rootA,rootB,global"; got != want {
		t.Errorf("chainConfigFiles = %q, want %q", got, want)
	}
}

// ── InputError ───────────────────────────────────────────────.

// beEnv is a minimal env channel: a bool that "junk" cannot coerce into, and a
// secret int whose bad value must never reach an InputError message.
type beEnv struct {
	Loud  bool `rotini:"loud" recon:"loud"`
	Token int  `rotini:"token" recon:"token,secret"`
}
type beCmdInputs struct {
	Flags     struct{}
	Arguments struct{}
	Env       beEnv
}
type beInputs struct {
	App beCmdInputs
}

func beBind(t *testing.T) error {
	t.Helper()
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in beInputs
	return NewInputReader(InputSettings{}).Read(rtx, &in)
}

// TestInputError_envCoercion_isCleanUsage pins that an env coercion failure is a usage-class
// *InputError whose message omits recon's text, with the recon cause reachable via errors.As.
func TestInputError_envCoercion_isCleanUsage(t *testing.T) {
	t.Setenv("LOUD", "junk") // not a bool
	err := beBind(t)
	if err == nil {
		t.Fatal("Bind = nil, want a coercion error for LOUD=junk")
	}

	// Typed + structured.
	var be *InputError
	if !errors.As(err, &be) {
		t.Fatalf("err is not an *InputError: %T (%v)", err, err)
	}
	if be.Channel != channelEnv || be.Input != "loud" {
		t.Errorf("InputError = {Channel:%q Input:%q}, want {env loud}", be.Channel, be.Input)
	}

	// Categorized as usage (errors.Is and CategoryOf), not internal.
	if !errors.Is(err, ErrUsage) || errors.Is(err, ErrInternal) {
		t.Errorf("Is(ErrUsage)=%v Is(ErrInternal)=%v, want true/false", errors.Is(err, ErrUsage), errors.Is(err, ErrInternal))
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}

	// Non-leaky message: names the input in plain language, never echoes recon
	// internals or the bad value.
	msg := err.Error()
	if !strings.Contains(msg, "environment variable") || !strings.Contains(msg, `"loud"`) || !strings.Contains(msg, "expected") {
		t.Errorf("message = %q, want a clean environment-variable phrasing", msg)
	}
	if strings.Contains(msg, "recon") || strings.Contains(msg, "junk") {
		t.Errorf("message = %q, leaks recon text or the bad value", msg)
	}

	// The recon cause is still reachable for a handler that wants the detail.
	if ce, ok := errors.AsType[*recon.CoercionError](err); !ok {
		t.Error("errors.As could not reach the recon *CoercionError cause")
	} else if ce.Path.String() != "loud" {
		t.Errorf("CoercionError.Path = %q, want loud", ce.Path.String())
	}
}

// TestInputError_secretNeverLeaks: a coercion failure on a secret-tagged input
// must not put the offending value in the message.
func TestInputError_secretNeverLeaks(t *testing.T) {
	const secret = "sk_live_not_a_number"
	t.Setenv("TOKEN", secret) // not an int, and tagged secret
	err := beBind(t)
	if err == nil {
		t.Fatal("Bind = nil, want a coercion error for the secret TOKEN")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("message = %q, leaks the secret value", err.Error())
	}
	if CategoryOf(err) != CategoryUsage {
		t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
	}
}

// TestInputError_typeContract pins the type's category + unwrap behavior directly,
// independent of any channel: Error is the clean message, the category sentinel
// and the cause are both reachable.
func TestInputError_typeContract(t *testing.T) {
	boom := errors.New("low-level cause")

	usage := usageBind(channelConfig, "api.token", "config key \"api.token\" is required", boom)
	if usage.Error() != `config key "api.token" is required` {
		t.Errorf("Error() = %q, want the clean message", usage.Error())
	}
	if CategoryOf(usage) != CategoryUsage || !errors.Is(usage, boom) {
		t.Errorf("usageBind: CategoryOf=%v Is(boom)=%v, want usage/true", CategoryOf(usage), errors.Is(usage, boom))
	}

	internal := internalBind(channelConfig, "", "could not build the configuration registry", boom)
	if CategoryOf(internal) != CategoryInternal || !errors.Is(internal, boom) {
		t.Errorf("internalBind: CategoryOf=%v Is(boom)=%v, want internal/true", CategoryOf(internal), errors.Is(internal, boom))
	}

	// A nil cause is fine — the sentinel is still reachable.
	noCause := usageBind(channelStdin, "", "required stdin payload is empty", nil)
	if !errors.Is(noCause, ErrUsage) {
		t.Error("a nil-cause usage InputError must still match ErrUsage")
	}
}

// ── WithInputReader as an override ────────────────────────────────────────────────.

// A WithInputReader function replaces the input reader Inputs would build.
func TestWithInputReader_overridesTheDefault(t *testing.T) {
	custom := NewInputReader(InputSettings{EnvPrefix: "SENTINEL"})

	var got *InputReader
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = readerFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)
	p.WithInputReader(func(InputSettings) *InputReader { return custom })

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Errorf("readerFor returned %p, want the supplied %p", got, custom)
	}
}

// The override receives the program's InputSettings.
func TestWithInputReader_receivesTheProgramsMeta(t *testing.T) {
	meta := InputSettings{EnvPrefix: "ACME", ConfigFiles: []ConfigFile{{Name: "project", Scope: "app"}}}

	var seen InputSettings
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { _ = readerFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)
	p.WithInputSettings(meta).WithInputReader(func(m InputSettings) *InputReader {
		seen = m
		return NewInputReader(m)
	})

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if seen.EnvPrefix != "ACME" || len(seen.ConfigFiles) != 1 {
		t.Errorf("the override saw %+v, want the program's InputSettings — an override that cannot see the descriptor silently drops channels", seen)
	}
}

// Without WithInputReader, the default reader is built from the InputSettings.
func TestWithInputReader_defaultsWhenUnset(t *testing.T) {
	var got *InputReader
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = readerFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("readerFor returned nil with no reader supplied")
	}
}

// A function that returns nil falls back rather than handing a nil input reader to Inputs.
func TestWithInputReader_ignoresANilResult(t *testing.T) {
	var got *InputReader
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = readerFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)
	p.WithInputReader(func(InputSettings) *InputReader { return nil })

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("a nil result produced a nil input reader instead of the default")
	}
}

// ── raw stdin formats (text / lines) ─────────────────────────────────────────
//
// Payloads bound as text rather than decoded as a document.

type tbTextCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *string `stdin:"text"`
}
type tbTextInputs struct{ App tbTextCmd }

type tbLinesCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *[]string `stdin:"lines"`
}
type tbLinesInputs struct{ App tbLinesCmd }

type tbTextReqCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *string `stdin:"text,required"`
}
type tbTextReqInputs struct{ App tbTextReqCmd }

func TestInputReader_stdinText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"a single line loses its trailing newline", "hello world\n", "hello world"},
		{"no trailing newline is fine", "hello world", "hello world"},
		{"interior newlines are content", "a\nb\nc\n", "a\nb\nc"},
		{"interior whitespace is content", "  two  spaces  \n", "  two  spaces  "},
		{"a CRLF line ending is handled", "hello\r\n", "hello"},
		{"only the LAST newline goes", "a\n\n", "a\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
			rtx.Stdin = strings.NewReader(tc.body)

			var in tbTextInputs
			if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if in.App.Stdin == nil {
				t.Fatal("the payload is nil, but something was piped")
			}
			if *in.App.Stdin != tc.want {
				t.Errorf("payload = %q, want %q", *in.App.Stdin, tc.want)
			}
		})
	}
}

func TestInputReader_stdinLines(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"three lines", "alpha\nbeta\ngamma\n", []string{"alpha", "beta", "gamma"}},
		// A trailing newline is a terminator, not a separator: "a\nb\n" is two lines.
		{"a trailing newline adds no empty element", "a\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
		{"a blank interior line is a line", "a\n\nb\n", []string{"a", "", "b"}},
		{"one line", "only\n", []string{"only"}},
		{"CRLF input stays usable", "a\r\nb\r\n", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
			rtx.Stdin = strings.NewReader(tc.body)

			var in tbLinesInputs
			if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if in.App.Stdin == nil {
				t.Fatal("the payload is nil, but something was piped")
			}
			if !slices.Equal(*in.App.Stdin, tc.want) {
				t.Errorf("payload = %q, want %q", *in.App.Stdin, tc.want)
			}
		})
	}
}

// TestInputReader_rawStdinNilVsEmpty pins that nothing piped leaves the pointer payload nil, so
// a handler can tell it apart from an empty payload.
func TestInputReader_rawStdinNilVsEmpty(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("")

	var in tbTextInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin != nil {
		t.Errorf("nothing piped left a non-nil payload %q", *in.App.Stdin)
	}
}

// TestInputReader_rawStdinRequired pins that a required raw payload rejects an empty stdin with
// a usage-class error naming the format.
func TestInputReader_rawStdinRequired(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("")

	var in tbTextReqInputs
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	if err == nil {
		t.Fatal("an empty required stdin payload was accepted")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("category = %v, want usage — an empty pipe is the caller's doing", CategoryOf(err))
	}
	for _, want := range []string{"required", "text"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err, want)
		}
	}
}

// ── absent vs empty InputSettings ────────────────────────────────────────────.

// newInputReaderWithoutDescriptor builds an input reader for which no InputSettings was ever
// supplied, as distinct from an empty one.
func newInputReaderWithoutDescriptor() *InputReader {
	b := NewInputReader(InputSettings{})
	b.described = false
	return b
}

// TestCheckDescribed_faultsWhenConfigInputsHaveNoDescriptor pins that config inputs with no
// InputSettings supplied are a *WiringError naming WithInputSettings, not silently zero.
func TestCheckDescribed_faultsWhenConfigInputsHaveNoDescriptor(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				Region string `rotini:"region" recon:"api.region"`
			}
		}
	}

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in inputs
	err := newInputReaderWithoutDescriptor().Read(rtx, &in)

	if err == nil {
		t.Fatal("Bind succeeded with config: inputs and no InputSettings — the values would be silently zero")
	}
	if _, ok := errors.AsType[*WiringError](err); !ok {
		t.Errorf("error is %T (%v), want a *WiringError — this is the program author's mistake, not the user's", err, err)
	}
	if !strings.Contains(err.Error(), "WithInputSettings") {
		t.Errorf("error %q does not name the call that fixes it", err)
	}
}

// An explicitly empty InputSettings (no configuration sources) is legal.
func TestCheckDescribed_anEmptyDescriptorIsLegal(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				Region string `rotini:"region" recon:"api.region"`
			}
		}
	}

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithInputSettings(InputSettings{})
	var in inputs
	if err := readerFor(rtx).Read(rtx, &in); err != nil {
		t.Errorf("Bind failed with an explicitly empty InputSettings: %v", err)
	}
}

// A command with no config inputs needs no InputSettings.
func TestCheckDescribed_noConfigInputsNeedsNoDescriptor(t *testing.T) {
	type inputs struct {
		App struct {
			Flags struct {
				Verbose bool `rotini:"verbose"`
			}
			Arguments struct{}
		}
	}

	rtx := NewContextFor(Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"}},
	}, []string{"-v"})
	var in inputs
	if err := newInputReaderWithoutDescriptor().Read(rtx, &in); err != nil {
		t.Errorf("Bind failed for an argv-only command with no descriptor: %v", err)
	}
	if !in.App.Flags.Verbose {
		t.Error("argv did not bind")
	}
}

// ── fallback values the flag's type cannot hold ─────────────────────────.

type tbFallbackInputs struct {
	App struct {
		Flags struct {
			Port int `rotini:"port" recon:"port" env:"PORT"`
		}
		Arguments struct{}
	}
}

func tbFallbackDef(secret bool) Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "port", Identifiers: []string{"--port"}, Type: "int", Secret: secret}},
	}
}

// A bad env or config value for a flag is a usage error naming its source, as `--port abc` is.
func TestInputReader_badFallbackValueIsAUsageError(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		t.Setenv("PORT", "abc")
		var in tbFallbackInputs
		err := NewInputReader(InputSettings{}).Read(NewContextFor(tbFallbackDef(false), nil), &in)
		var be *InputError
		if !errors.As(err, &be) || CategoryOf(err) != CategoryUsage {
			t.Fatalf("err = %v (%T), want a usage *InputError", err, err)
		}
		for _, want := range []string{"--port", `"abc" is not a valid integer`, "environment variable PORT"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q missing %q", err, want)
			}
		}
	})
	t.Run("config", func(t *testing.T) {
		cfg := writeConfig(t, "port: abc\n")
		var in tbFallbackInputs
		err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).
			Read(NewContextFor(tbFallbackDef(false), nil), &in)
		if err == nil || !strings.Contains(err.Error(), `configuration file "app"`) {
			t.Fatalf("err = %v, want it to name the configuration file", err)
		}
	})
	t.Run("secret", func(t *testing.T) {
		t.Setenv("PORT", "hunter2")
		var in tbFallbackInputs
		err := NewInputReader(InputSettings{}).Read(NewContextFor(tbFallbackDef(true), nil), &in)
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("err = %v, want an error that does not show the secret", err)
		}
	})
	t.Run("good value still binds", func(t *testing.T) {
		t.Setenv("PORT", "8080")
		var in tbFallbackInputs
		if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbFallbackDef(false), nil), &in); err != nil || in.App.Flags.Port != 8080 {
			t.Fatalf("Port = %d, err = %v; want 8080", in.App.Flags.Port, err)
		}
	})
}

// ── enums on env and config inputs ──────────────────────────────────────.

type tbChannelEnumInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Mode  string   `rotini:"mode" recon:"mode" env:"MODE" enum:"[\"fast\",\"slow\"]"`
			Level string   `rotini:"level" recon:"level" env:"LEVEL" enum:"[\"debug\",\"info\"]" ignorecase:"true"`
			Tags  []string `rotini:"tags" recon:"tags" env:"TAGS" enum:"[\"a\",\"b\"]" ignorecase:"true"`
		}
		Config struct {
			Tier string `rotini:"tier" recon:"tier" enum:"[\"gold\",\"silver\"]"`
		}
	}
}

// An enum on an env or config input is enforced as on argv, including ignorecase.
func TestInputReader_channelEnumEnforced(t *testing.T) {
	bind := func(t *testing.T, meta InputSettings) (tbChannelEnumInputs, error) {
		t.Helper()
		var in tbChannelEnumInputs
		err := NewInputReader(meta).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
		return in, err
	}
	t.Run("env rejects a non-member, naming the variable", func(t *testing.T) {
		t.Setenv("MODE", "bogus")
		_, err := bind(t, InputSettings{})
		if err == nil || CategoryOf(err) != CategoryUsage || !strings.Contains(err.Error(), `invalid value "bogus" for MODE (one of: fast, slow)`) {
			t.Fatalf("err = %v, want a usage error naming MODE and its members", err)
		}
	})
	t.Run("env is case-sensitive by default", func(t *testing.T) {
		t.Setenv("MODE", "FAST")
		if _, err := bind(t, InputSettings{}); err == nil {
			t.Fatal("MODE=FAST accepted without ignorecase")
		}
	})
	t.Run("ignorecase binds the declared spelling", func(t *testing.T) {
		t.Setenv("LEVEL", "INFO")
		t.Setenv("TAGS", "A,b")
		in, err := bind(t, InputSettings{})
		if err != nil {
			t.Fatal(err)
		}
		if in.App.Env.Level != "info" || !slices.Equal(in.App.Env.Tags, []string{"a", "b"}) {
			t.Errorf("Level = %q, Tags = %v; want info, [a b]", in.App.Env.Level, in.App.Env.Tags)
		}
	})
	t.Run("config rejects a non-member", func(t *testing.T) {
		cfg := writeConfig(t, "tier: bronze\n")
		_, err := bind(t, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})
		if err == nil || !strings.Contains(err.Error(), `invalid value "bronze" for tier`) {
			t.Fatalf("err = %v, want an enum violation for tier", err)
		}
	})
}

// ── list and map flags with a fallback ──────────────────────────────────.

type tbListInputs struct {
	App struct {
		Flags struct {
			Tags   []string          `rotini:"tags" recon:"tags" env:"TAGS"`
			Labels map[string]string `rotini:"labels" recon:"labels" env:"LABELS"`
			Ports  []int             `rotini:"ports" recon:"ports" env:"PORTS"`
		}
		Arguments struct{}
	}
}

func tbListDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string"},
		{Name: "labels", Identifiers: []string{"--labels"}, Type: "map[string]string"},
		{Name: "ports", Identifiers: []string{"--ports"}, Type: "[]int", Separator: ","},
	}}
}

// A list or map flag that declares a fallback binds element-wise from argv, a config file's
// list or map, and env (split only on a declared separator).
func TestInputReader_listAndMapFlagsWithAFallback(t *testing.T) {
	bind := func(t *testing.T, argv []string, meta InputSettings) tbListInputs {
		t.Helper()
		var in tbListInputs
		if err := NewInputReader(meta).Read(NewContextFor(tbListDef(), argv), &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		return in
	}
	t.Run("argv", func(t *testing.T) {
		t.Setenv("TAGS", "from-env") // argv outranks it
		in := bind(t, []string{"--tags", "a", "--tags", "b", "--labels", "k=v", "--ports", "1,2"}, InputSettings{})
		if !slices.Equal(in.App.Flags.Tags, []string{"a", "b"}) || in.App.Flags.Labels["k"] != "v" || !slices.Equal(in.App.Flags.Ports, []int{1, 2}) {
			t.Errorf("tags=%q labels=%v ports=%v", in.App.Flags.Tags, in.App.Flags.Labels, in.App.Flags.Ports)
		}
	})
	t.Run("config list and map", func(t *testing.T) {
		cfg := writeConfig(t, "tags: [a, b]\nlabels: {k: v, x: y}\nports: [8080, 9090]\n")
		in := bind(t, nil, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})
		if !slices.Equal(in.App.Flags.Tags, []string{"a", "b"}) || len(in.App.Flags.Labels) != 2 || in.App.Flags.Labels["x"] != "y" ||
			!slices.Equal(in.App.Flags.Ports, []int{8080, 9090}) {
			t.Errorf("tags=%q labels=%v ports=%v", in.App.Flags.Tags, in.App.Flags.Labels, in.App.Flags.Ports)
		}
	})
	t.Run("env splits on the separator only when declared", func(t *testing.T) {
		t.Setenv("PORTS", "1, 2,3")
		t.Setenv("TAGS", "a,b") // no separator: one item, as written
		in := bind(t, nil, InputSettings{})
		if !slices.Equal(in.App.Flags.Ports, []int{1, 2, 3}) || !slices.Equal(in.App.Flags.Tags, []string{"a,b"}) {
			t.Errorf("ports=%v tags=%q", in.App.Flags.Ports, in.App.Flags.Tags)
		}
	})
}

// ── several variable names ──────────────────────────────────────────────.

type tbMultiEnvInputs struct {
	App struct {
		Flags struct {
			Token string `rotini:"token" recon:"token" env:"GH_TOKEN,GITHUB_TOKEN"`
		}
		Arguments struct{}
		Env       struct {
			Region string `rotini:"region" recon:"region" env:"APP_REGION,AWS_REGION" enum:"[\"us\",\"eu\"]"`
		}
	}
}

// `variable: [GH_TOKEN, GITHUB_TOKEN]` generates env:"GH_TOKEN,GITHUB_TOKEN": the first name
// that is set supplies the value, on a flag's fallback and on an env input alike.
func TestInputReader_severalVariableNames(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string"}}}
	bind := func(t *testing.T) (tbMultiEnvInputs, error) {
		t.Helper()
		var in tbMultiEnvInputs
		err := NewInputReader(InputSettings{}).Read(NewContextFor(def, nil), &in)
		return in, err
	}
	t.Run("a later name when the first is unset", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "from-github")
		t.Setenv("AWS_REGION", "eu")
		in, err := bind(t)
		if err != nil || in.App.Flags.Token != "from-github" || in.App.Env.Region != "eu" {
			t.Fatalf("token=%q region=%q err=%v", in.App.Flags.Token, in.App.Env.Region, err)
		}
	})
	t.Run("the first name wins when both are set", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "from-gh")
		t.Setenv("GITHUB_TOKEN", "from-github")
		t.Setenv("APP_REGION", "us")
		t.Setenv("AWS_REGION", "eu")
		in, err := bind(t)
		if err != nil || in.App.Flags.Token != "from-gh" || in.App.Env.Region != "us" {
			t.Fatalf("token=%q region=%q err=%v", in.App.Flags.Token, in.App.Env.Region, err)
		}
	})
	t.Run("an error names the variable that was set", func(t *testing.T) {
		t.Setenv("AWS_REGION", "mars")
		if _, err := bind(t); err == nil || !strings.Contains(err.Error(), "for AWS_REGION") {
			t.Fatalf("err = %v, want it to name AWS_REGION", err)
		}
	})
}

// For a flag's fallback, an environment variable that is set but empty counts as unset, so the
// config file supplies the value.
func TestInputReader_emptyEnvFallsThrough(t *testing.T) {
	cfg := writeConfig(t, "port: 9090\n")
	t.Setenv("PORT", "")
	var in tbFallbackInputs
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).
		Read(NewContextFor(tbFallbackDef(false), nil), &in)
	if err != nil || in.App.Flags.Port != 9090 {
		t.Fatalf("Port = %d, err = %v; want the config file's 9090", in.App.Flags.Port, err)
	}
}

// An env or config input's bool accepts the spellings a flag's does (yes, on, ...); a string
// input whose value is "yes" is untouched.
func TestInputReader_boolSpellingsOnEnvAndConfig(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Cache  bool   `rotini:"cache" recon:"cache" env:"CACHE"`
				Answer string `rotini:"answer" recon:"answer" env:"ANSWER"`
			}
			Config struct {
				Debug *bool `rotini:"debug" recon:"debug"`
			}
		}
	}
	cfg := writeConfig(t, "debug: 'on'\n")
	t.Setenv("CACHE", "Yes")
	t.Setenv("ANSWER", "yes")
	var in inputs
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).
		Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	if err != nil {
		t.Fatal(err)
	}
	if !in.App.Env.Cache || in.App.Env.Answer != "yes" || in.App.Config.Debug == nil || !*in.App.Config.Debug {
		t.Errorf("cache=%v answer=%q debug=%v", in.App.Env.Cache, in.App.Env.Answer, in.App.Config.Debug)
	}
	t.Setenv("CACHE", "maybe")
	var bad inputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &bad); err == nil {
		t.Error("CACHE=maybe accepted")
	}
}

// A time on an env or config input parses under its generated layout tag, as a flag's does.
func TestInputReader_channelTimeLayouts(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Since time.Time `rotini:"since" recon:"since" env:"SINCE" layout:"2006-01-02"`
				Epoch time.Time `rotini:"epoch" recon:"epoch" env:"EPOCH" layout:"unix"`
			}
			Config struct {
				Until *time.Time `rotini:"until" recon:"until" layout:"02/01/2006"`
			}
		}
	}
	cfg := writeConfig(t, "until: '25/12/2026'\n")
	t.Setenv("SINCE", "2026-09-29")
	t.Setenv("EPOCH", "1759104000")
	var in inputs
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).
		Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	if err != nil {
		t.Fatal(err)
	}
	e, c := in.App.Env, in.App.Config
	if !e.Since.Equal(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) || e.Epoch.Unix() != 1759104000 || c.Until == nil || c.Until.Month() != 12 {
		t.Errorf("since=%v epoch=%v until=%v", e.Since, e.Epoch, c.Until)
	}
	t.Setenv("SINCE", "yesterday")
	var bad inputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &bad); err == nil {
		t.Error("SINCE=yesterday accepted")
	}
}

// An env or config duration accepts days and weeks as a flag's does, and duration and size
// bounds apply on those channels.
func TestInputReader_channelDurationsAndBounds(t *testing.T) {
	type inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				TTL time.Duration `rotini:"ttl" recon:"ttl" env:"TTL" max:"604800000000000"`
			}
			Config struct {
				Cache ByteSize `rotini:"cache" recon:"cache" max:"1073741824"`
			}
		}
	}
	bind := func(t *testing.T, cfg string) (inputs, error) {
		t.Helper()
		var in inputs
		meta := InputSettings{}
		if cfg != "" {
			meta.ConfigFiles = []ConfigFile{{Name: "app", Path: writeConfig(t, cfg), Format: "yaml"}}
		}
		err := NewInputReader(meta).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
		return in, err
	}
	t.Setenv("TTL", "3d")
	in, err := bind(t, "cache: 512Mi\n")
	if err != nil || in.App.Env.TTL != 72*time.Hour || in.App.Config.Cache != 512<<20 {
		t.Fatalf("ttl=%v cache=%v err=%v", in.App.Env.TTL, in.App.Config.Cache, err)
	}
	t.Setenv("TTL", "8d")
	if _, err := bind(t, ""); err == nil || !strings.Contains(err.Error(), "TTL must be <= 7d (got 8d)") {
		t.Errorf("TTL=8d: err = %v, want the 7d bound", err)
	}
	t.Setenv("TTL", "1h")
	if _, err := bind(t, "cache: 2Gi\n"); err == nil || !strings.Contains(err.Error(), "cache must be <= 1Gi") {
		t.Errorf("cache 2Gi: err = %v, want the 1Gi bound", err)
	}
}

// A parent collecting its own inputs validates only the commands its type describes, so a
// leaf's required flag does not fail the parent's collect (e.g. `app sub -h`).
func TestInputReader_validatesOnlyTheFramesTheTypeDescribes(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "config", Identifiers: []string{"--config"}, Type: "string"}},
		Commands: []CommandDef{{
			Name: "sub", Handler: "AppSub",
			Flags: []FlagDef{
				{Name: "file", Identifiers: []string{"-f"}, Type: "string", Required: true},
				{Name: "help", Identifiers: []string{"-h"}, Type: "bool"},
			},
		}},
	}
	var root struct {
		App struct {
			Flags struct {
				Config string `rotini:"config"`
			}
			Arguments struct{}
		}
	}
	rtx := NewContextFor(def, []string{"--config", "c.yaml", "sub", "-h"})
	rtx.frame = 0 // collecting from the root's own hook
	if err := NewInputReader(InputSettings{}).Read(rtx, &root); err != nil || root.App.Flags.Config != "c.yaml" {
		t.Fatalf("root collect: config=%q err=%v", root.App.Flags.Config, err)
	}
	// The leaf's own collect still enforces its required flag.
	var leaf struct {
		App struct {
			Flags struct {
				Config string `rotini:"config"`
			}
			Arguments struct{}
		}
		AppSub struct {
			Flags struct {
				File string `rotini:"file"`
				Help bool   `rotini:"help"`
			}
			Arguments struct{}
		}
	}
	rtx.frame = 1
	if err := NewInputReader(InputSettings{}).Read(rtx, &leaf); err == nil || !strings.Contains(err.Error(), "-f") {
		t.Errorf("leaf collect: err = %v, want the missing -f", err)
	}
}

// TestReconBind_unrecognizedCause pins reconBind's fallback arm: an untyped recon failure is a
// usage-class *InputError naming the channel in plain words, without the raw cause text.
func TestReconBind_unrecognizedCause(t *testing.T) {
	cause := errors.New("some unrecognized recon failure")
	cases := []struct {
		channel string
		want    string
	}{
		{channelEnv, "environment"},
		{channelConfig, "configuration"},
		{channelStdin, "stdin"},
		{channelFlag, "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.channel, func(t *testing.T) {
			err := reconBind(tc.channel, cause)

			var be *InputError
			if !errors.As(err, &be) {
				t.Fatalf("reconBind returned %T, want an *InputError", err)
			}
			if !strings.Contains(be.Msg, tc.want) {
				t.Errorf("message %q does not name the channel as %q", be.Msg, tc.want)
			}
			if !errors.Is(err, cause) {
				t.Error("the original cause is not reachable with errors.Is")
			}
			if !errors.Is(err, ErrUsage) {
				t.Error("an unrecognized channel failure should still be usage-class")
			}
			// Non-leaky: the raw recon text must not reach the user's message.
			if strings.Contains(be.Msg, cause.Error()) {
				t.Errorf("message %q leaks the raw cause", be.Msg)
			}
		})
	}

	if err := reconBind(channelEnv, nil); err != nil {
		t.Errorf("reconBind(nil) = %v, want nil", err)
	}
}

// TestReconBind_rootPathIsNamedByChannel pins that a recon error with an empty Path (a
// document-level failure) is labeled by the channel name, while a non-empty Path keeps the
// per-field label.
func TestReconBind_rootPathIsNamedByChannel(t *testing.T) {
	root, field := recon.Path{}, recon.Path{"tags"}
	cases := []struct {
		name  string
		cause error
		want  string
	}{
		{"validation at the root", &recon.ValidationError{Path: root, Msg: `missing required property "name"`}, `stdin: missing required property "name"`},
		{"validation on a field", &recon.ValidationError{Path: field, Msg: "value is not of type array"}, `stdin field "tags": value is not of type array`},
		{"missing required at the root", &recon.MissingRequiredError{Path: root}, "stdin is required"},
		{"missing required on a field", &recon.MissingRequiredError{Path: field}, `stdin field "tags" is required`},
		{"coercion at the root", &recon.CoercionError{Path: root, Target: "int"}, "stdin: expected int"},
		{"empty value at the root", &recon.EmptyValueError{Path: root}, "stdin must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var be *InputError
			if !errors.As(reconBind(channelStdin, tc.cause), &be) {
				t.Fatalf("reconBind did not return an *InputError for %T", tc.cause)
			}
			if be.Msg != tc.want {
				t.Errorf("message = %q, want %q", be.Msg, tc.want)
			}
			if strings.Contains(be.Msg, `""`) {
				t.Errorf("message %q names an empty input", be.Msg)
			}
		})
	}
}

func TestTrimAcquiredPayload_oneRuleForBothPaths(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"one trailing newline goes", "hello\n", "hello"},
		{"a CRLF ending goes whole", "hello\r\n", "hello"},
		{"only ONE ending goes", "hello\n\n", "hello\n"},
		{"leading whitespace is content", "  hello", "  hello"},
		{"interior whitespace is content", "a  b", "a  b"},
		{"trailing spaces are content", "hello  ", "hello  "},
		{"spaces before the ending survive", "  hello  \n", "  hello  "},
		{"no ending, nothing to do", "hello", "hello"},
		{"empty stays empty", "", ""},
		{"a lone newline empties", "\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := trimAcquiredPayload(tc.in); got != tc.want {
				t.Errorf("trimAcquiredPayload(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// The argv sentinels (resolveFlagValue) and the stdin channel (bindRawStdin) must agree.
	const payload = "  hello  \n"
	dir := t.TempDir()
	file := filepath.Join(dir, "value.txt")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	fd := FlagDef{Name: "input", Identifiers: []string{"-i"}, From: []string{"file", "stdin"}}

	fromFile, err := resolveFlagValue(fd, "-i", "@"+file, nil)
	if err != nil {
		t.Fatalf("resolveFlagValue(@file): %v", err)
	}
	fromStdin, err := resolveFlagValue(fd, "-i", "-", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("resolveFlagValue(-): %v", err)
	}
	var channel *string
	sf := reflect.ValueOf(&channel).Elem()
	if err := bindRawStdin(sf, "text", []byte(payload)); err != nil {
		t.Fatalf("bindRawStdin: %v", err)
	}

	if fromFile != fromStdin || fromFile != *channel {
		t.Errorf("the three acquisition paths disagree: @file=%q -=%q channel=%q", fromFile, fromStdin, *channel)
	}
	if *channel != "  hello  " {
		t.Errorf("payload = %q, want %q — leading and trailing spaces are content", *channel, "  hello  ")
	}
}
