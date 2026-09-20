package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
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

func TestBinder_fillsEnvAndConfig(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n  token: secret123\n")
	t.Setenv("REGION", "us-west")

	rtx := NewContextFor(tbDef(), []string{"--verbose"})
	binder := NewBinder(BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

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

// TestBinder_customSources pins the BindMeta.Sources seam (ergonomics E1/
// E3-S4): custom recon sources join the config layer AFTER the declared
// configuration_files — explicit files beat ambient services — serving both
// config inputs and flags' config fallbacks, through Bind and the per-channel
// surface alike.
func TestBinder_customSources(t *testing.T) {
	vault := func() recon.Source {
		// MapSource takes the NESTED shape a config decoder produces.
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
		err := NewBinder(BindMeta{Sources: []recon.Source{vault()}}).Bind(rtx, &in)
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
		meta := BindMeta{
			ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}},
			Sources:     []recon.Source{vault()},
		}
		if err := NewBinder(meta).Bind(rtx, &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Config.Endpoint != "file-endpoint" {
			t.Errorf("endpoint = %q, want the declared file to win", in.App.Config.Endpoint)
		}
		if in.App.Config.Token != "vault-token" {
			t.Errorf("token = %q, want the custom source to fill the gap", in.App.Config.Token)
		}
	})

	t.Run("per-channel surface sees sources via KeyBindMeta", func(t *testing.T) {
		rtx := NewContextFor(tbDef(), nil)
		rtx.Bind(KeyBindMeta, BindMeta{Sources: []recon.Source{vault()}})
		files, err := ParseFiles[tbInputs](rtx)
		if err != nil {
			t.Fatalf("ParseFiles: %v", err)
		}
		if files.Values.App.Config.Token != "vault-token" {
			t.Errorf("files layer token = %q, want the custom source's value", files.Values.App.Config.Token)
		}
	})
}

func TestBinder_requiredConfigMissing(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: https://api.example\n") // no api.token
	rtx := NewContextFor(tbDef(), nil)
	binder := NewBinder(BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	var in tbInputs
	if err := binder.Bind(rtx, &in); err == nil {
		t.Fatal("expected an error for a missing required config value (recon required)")
	}
}

func TestBinder_noConfigFilesLeavesConfigZero(t *testing.T) {
	// With no config sources, config fields stay zero; a non-required env still binds.
	t.Setenv("REGION", "eu-central")
	rtx := NewContextFor(tbDef(), nil)
	binder := NewBinder(BindMeta{}) // no config files

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

// Discovery shapes (spec discover:): a walk-up entry found in an ancestor of
// the working directory, and an xdg entry under $XDG_CONFIG_HOME/<app>.
func TestBinder_discoverWalkUp(t *testing.T) {
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

	meta := BindMeta{ConfigFiles: []ConfigFile{{
		Name: "project", Format: "yaml",
		Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"},
	}}}
	var in tbNoReqInputs
	if err := NewBinder(meta).Bind(NewContextFor(tbDef(), nil), &in); err != nil {
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
	if err := NewBinder(meta).Bind(NewContextFor(tbDef(), nil), &in2); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in2.App.Config.Endpoint != "from-cwd" {
		t.Errorf("Endpoint = %q, want from-cwd (nearest directory wins)", in2.App.Config.Endpoint)
	}
}

func TestBinder_discoverXDG(t *testing.T) {
	xdg := t.TempDir()
	appDir := filepath.Join(xdg, "acme")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.yaml"), []byte("api:\n  endpoint: from-xdg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	meta := BindMeta{ConfigFiles: []ConfigFile{{
		Name: "user", Format: "yaml",
		Discover: &DiscoverDef{Strategy: "xdg", App: "acme", File: "config.yaml"},
	}}}
	var in tbNoReqInputs
	if err := NewBinder(meta).Bind(NewContextFor(tbDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Endpoint != "from-xdg" {
		t.Errorf("Endpoint = %q, want from-xdg", in.App.Config.Endpoint)
	}

	// An absent discovered file is simply absent — same as a missing fixed path.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var in2 tbNoReqInputs
	if err := NewBinder(meta).Bind(NewContextFor(tbDef(), nil), &in2); err != nil {
		t.Fatalf("Bind(absent discovered file): %v", err)
	}
	if in2.App.Config.Endpoint != "" {
		t.Errorf("Endpoint = %q, want empty", in2.App.Config.Endpoint)
	}
}

// Config-schema shapes (spec configuration_files[].schema): the loaded
// document is validated at bind time, before any value is read (fidelity F1).
const tbCfgSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["api"],"properties":{"api":{"type":"object","required":["endpoint"],"properties":{"endpoint":{"type":"string"}}}}}`

func TestBinder_configSchemaValidation(t *testing.T) {
	bind := func(t *testing.T, meta BindMeta) error {
		t.Helper()
		var in tbNoReqInputs
		return NewBinder(meta).Bind(NewContextFor(tbDef(), nil), &in)
	}
	withSchema := func(cf ConfigFile) BindMeta {
		cf.Schema = tbCfgSchema
		return BindMeta{ConfigFiles: []ConfigFile{cf}}
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
		if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in); err == nil {
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

func TestBinder_configSourceTwoPhase(t *testing.T) {
	declared := writeConfig(t, "api:\n  endpoint: from-declared\n")
	flagged := writeConfig(t, "api:\n  endpoint: from-flag\n")
	fromEnv := writeConfig(t, "api:\n  endpoint: from-env\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{
		Name: "app", Path: declared, Format: "yaml",
		PathFrom: &PathFromDef{Flag: "config", Env: "APP_CONFIG"},
	}}}
	bind := func(t *testing.T, def Definition, argv []string) tbCfgSrcInputs {
		t.Helper()
		var in tbCfgSrcInputs
		if err := NewBinder(meta).Bind(NewContextFor(def, argv), &in); err != nil {
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
		err := NewBinder(meta).Bind(NewContextFor(tbCfgSrcDef(""), []string{"--config", "/nonexistent/app.yaml"}), &in)
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

func TestBinder_explicitEnvVar(t *testing.T) {
	t.Setenv("WIDGET_TOKEN", "s3cret")
	t.Setenv("TOKEN", "wrong-default") // the SNAKE_UPPER default — must be ignored

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbEnvVarInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Env.Token != "s3cret" {
		t.Errorf("Env.Token = %q, want s3cret (from $WIDGET_TOKEN, not $TOKEN)", in.App.Env.Token)
	}

	// Regression (found by the conformance suite): the explicit variable must
	// resolve ON ITS OWN — with no convention-named $TOKEN anchoring the
	// registry snapshot, $WIDGET_TOKEN used to silently not bind at all.
	os.Unsetenv("TOKEN")
	var alone tbEnvVarInputs
	if err := NewBinder(BindMeta{}).Bind(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &alone); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if alone.App.Env.Token != "s3cret" {
		t.Errorf("Env.Token = %q, want s3cret from the explicit-only variable", alone.App.Env.Token)
	}
}

// Pinned-config shapes (spec file:): an input read from ONE named
// configuration_files entry, not the merged precedence chain.
func TestBinder_configFilePinned(t *testing.T) {
	system := writeConfig(t, "api:\n  endpoint: from-system\n  token: sys-token\n")
	user := writeConfig(t, "api:\n  endpoint: from-user\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{
		{Name: "system", Path: system, Format: "yaml"}, // higher precedence
		{Name: "user", Path: user, Format: "yaml"},
	}}

	type pinned struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				// Pinned to "user": must NOT see system's value despite precedence.
				Endpoint string `rotini:"endpoint" recon:"api.endpoint" cfgfile:"user"`
			}
		}
	}
	var in pinned
	if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Endpoint != "from-user" {
		t.Errorf("Endpoint = %q, want from-user — file: pins the source", in.App.Config.Endpoint)
	}

	// A pinned required key is judged against ITS file: present elsewhere
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
	if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in2); err == nil {
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
	if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in3); err == nil || !strings.Contains(err.Error(), "nope") {
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

func TestBinder_envNesting(t *testing.T) {
	t.Setenv("ACME_HTTP__TIMEOUT", "30")
	t.Setenv("ACME_HTTP__RETRY__MAX", "9")
	t.Setenv("ACME_HTTPX", "decoy") // wrong separator boundary — not family

	var in tbNestInputs
	if err := NewBinder(BindMeta{}).Bind(NewContextFor(tbAppDef(), nil), &in); err != nil {
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

func TestBinder_envNestingRequired(t *testing.T) {
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
	if err := NewBinder(BindMeta{}).Bind(NewContextFor(tbAppDef(), nil), &in); err == nil {
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

func TestBinder_decodesStdin(t *testing.T) {
	withPipedStdin(t, "kind: Widget\nname: foo\n")
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	var in tbStdinInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin == nil {
		t.Fatal("Stdin payload was not decoded")
	}
	if in.App.Stdin.Kind != "Widget" || in.App.Stdin.Name != "foo" {
		t.Errorf("Stdin = %+v, want {Widget foo}", in.App.Stdin)
	}
}

// The Binder reads its stdin channel from [Context.Stdin] (which the Program sets from
// Program.WithStdin), so a test can supply input via an ordinary reader without touching
// the process's os.Stdin.
func TestBinder_decodesStdinFromContextStdin(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("kind: Widget\nname: foo\n")

	var in tbStdinInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin == nil || in.App.Stdin.Kind != "Widget" || in.App.Stdin.Name != "foo" {
		t.Errorf("Stdin = %+v, want {Widget foo} decoded from rtx.Stdin", in.App.Stdin)
	}
}

func TestBinder_noStdinLeavesNil(t *testing.T) {
	withPipedStdin(t, "") // nothing piped → EOF, no data
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)

	var in tbStdinInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
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

func TestBinder_flagFallbackPrecedence(t *testing.T) {
	cfg := writeConfig(t, "create:\n  color: red\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

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
		rtx := NewContextFor(tbFbDef(), nil)
		var in tbFbInputs
		if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if in.App.Flags.Color != "blue" {
			t.Errorf("Color = %q, want blue (FlagDef default)", in.App.Flags.Color)
		}
	})
}

// Required-fallback shapes: a *required* flag that declares a recon key, with no
// default, must be satisfiable from env or config — not only from argv. (Regression
// for the bug where the Parser's required-check fired before fallback ran.)
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

func TestBinder_requiredFlagSatisfiedByConfig(t *testing.T) {
	cfg := writeConfig(t, "api:\n  token: from-config\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbReqDef(), nil) // not on argv
	var in tbReqInputs
	if err := NewBinder(meta).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v (a required flag should be satisfiable via config)", err)
	}
	if in.App.Flags.Token != "from-config" {
		t.Errorf("Token = %q, want from-config", in.App.Flags.Token)
	}
}

func TestBinder_requiredFlagSatisfiedByEnv(t *testing.T) {
	t.Setenv("API_TOKEN", "from-env") // SNAKE_UPPER of recon key "api.token"

	// No config files: also exercises the path where env is the only fallback source.
	rtx := NewContextFor(tbReqDef(), nil)
	var in tbReqInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v (a required flag should be satisfiable via env)", err)
	}
	if in.App.Flags.Token != "from-env" {
		t.Errorf("Token = %q, want from-env", in.App.Flags.Token)
	}
}

func TestBinder_requiredFlagMissingEverywhere(t *testing.T) {
	rtx := NewContextFor(tbReqDef(), nil) // no argv, no env, no config
	var in tbReqInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err == nil {
		t.Fatal("expected a missing-required error when a required fallback flag is in no source")
	}
}

// Enum-on-reconciled: an env/config-supplied flag value must be enum-checked too
// (previously the enum check was bypassed because it ran before reconciliation).
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

func TestBinder_enumCheckedOnReconciledValue(t *testing.T) {
	cfg := writeConfig(t, "create:\n  color: teal\n") // teal is not in the enum
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbEnumDef(), nil)
	var in tbEnumInputs
	if err := NewBinder(meta).Bind(rtx, &in); err == nil {
		t.Fatal("expected an enum error for a config-supplied value outside the declared enum")
	}
}

// Constraints are enforced over the fully-reconciled value, so a bound that a
// config-supplied flag violates is caught (proving A1 runs at the single post-
// reconciliation validation locus, not just on argv).
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
			Minimum: Ptr(1.0), Maximum: Ptr(65535.0),
		}},
	}
}

func TestBinder_constraintCheckedOnReconciledValue(t *testing.T) {
	cfg := writeConfig(t, "create:\n  port: 70000\n") // above the declared maximum
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbPortDef(), nil)
	var in tbPortInputs
	if err := NewBinder(meta).Bind(rtx, &in); err == nil || !strings.Contains(err.Error(), "must be <= 65535") {
		t.Fatalf("Bind error = %v, want a max-bound violation for the config-supplied port", err)
	}
}

// Channel-constraint shapes carry the validation struct-tags codegen emits on env/
// config fields (A2d), which the binder enforces over the reconciled value.
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

func TestBinder_envConstraintEnforced(t *testing.T) {
	t.Setenv("PORT", "70000") // above max
	var in tbEnvPort
	err := NewBinder(BindMeta{}).Bind(NewContextFor(tbAppDef(), nil), &in)
	if err == nil || !strings.Contains(err.Error(), "<= 65535") {
		t.Fatalf("Bind err = %v, want a max-bound violation for env PORT", err)
	}
}

func TestBinder_envConstraintValid(t *testing.T) {
	t.Setenv("PORT", "8080")
	var in tbEnvPort
	if err := NewBinder(BindMeta{}).Bind(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Env.Port != 8080 {
		t.Errorf("Port = %d, want 8080", in.App.Env.Port)
	}
}

func TestBinder_channelConstraintAbsentSkipped(t *testing.T) {
	// Genuinely unset (not empty): the field is never sourced, so the constraint is
	// skipped and the value stays zero — absence is `required`'s job, not the bounds'.
	if prev, had := os.LookupEnv("PORT"); had {
		os.Unsetenv("PORT")
		t.Cleanup(func() { os.Setenv("PORT", prev) })
	}
	var in tbEnvPort
	if err := NewBinder(BindMeta{}).Bind(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("absent env value should skip its constraint check: %v", err)
	}
	if in.App.Env.Port != 0 {
		t.Errorf("Port = %d, want 0 (unset)", in.App.Env.Port)
	}
}

func TestBinder_configConstraintEnforced(t *testing.T) {
	cfg := writeConfig(t, "app:\n  name: TOOLONG\n") // length 7 > maxlen 5 (and not lowercase)
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbCfgName
	if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in); err == nil {
		t.Fatal("expected a constraint violation for the config value app.name")
	}
}

func TestBinder_configConstraintValid(t *testing.T) {
	cfg := writeConfig(t, "app:\n  name: abc\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbCfgName
	if err := NewBinder(meta).Bind(NewContextFor(tbAppDef(), nil), &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Config.Name != "abc" {
		t.Errorf("Name = %q, want abc", in.App.Config.Name)
	}
}

// Stdin-validation shape: a payload whose schema (in BindMeta) the binder checks
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

func tbStdinMeta() BindMeta {
	return BindMeta{StdinSchemas: map[string]string{"tbStdinValPayload": tbStdinSchema}}
}

func TestBinder_stdinPayloadRejectedBySchema(t *testing.T) {
	withPipedStdin(t, "port: 70000\n") // above the schema maximum
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbStdinValInputs
	if err := NewBinder(tbStdinMeta()).Bind(rtx, &in); err == nil {
		t.Fatal("expected the stdin payload to be rejected (port above maximum)")
	}
}

func TestBinder_stdinPayloadValid(t *testing.T) {
	withPipedStdin(t, "port: 8080\n")
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	var in tbStdinValInputs
	if err := NewBinder(tbStdinMeta()).Bind(rtx, &in); err != nil {
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

func TestBinder_secretChannelValueRedacted(t *testing.T) {
	cfg := writeConfig(t, "app:\n  token: short\n") // length 5 < minlen 8
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	var in tbSecretCfg
	err := NewBinder(meta).Bind(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
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

// TestBinder_stdinRequired confirms the spec's stdin required: true is honored:
// empty stdin errors, a piped document binds.
func TestBinder_stdinRequired(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}

	rtx := NewContextFor(def, nil)
	rtx.Stdin = strings.NewReader("")
	var in tbStdinReqInputs
	err := NewBinder(BindMeta{}).Bind(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "required stdin payload is empty") {
		t.Errorf("Bind(empty required stdin) = %v, want a required-stdin error", err)
	}

	rtx2 := NewContextFor(def, nil)
	rtx2.Stdin = strings.NewReader("kind: demo\n")
	var in2 tbStdinReqInputs
	if err := NewBinder(BindMeta{}).Bind(rtx2, &in2); err != nil {
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

// TestBinder_chainConfigFiles pins the cascade (D-W3.1): config_files in scope for
// the invoked chain = the union along it, NEAREST-WINS (deepest command first), with
// off-branch sources excluded and unscoped (Scope=="") sources always in scope, last.
func TestBinder_chainConfigFiles(t *testing.T) {
	b := &Binder{configFiles: []ConfigFile{
		{Name: "rootA", Scope: "app"},
		{Name: "rootB", Scope: "app"},
		{Name: "deploy", Scope: "app/deploy"},
		{Name: "aws", Scope: "app/deploy/aws"},
		{Name: "sibling", Scope: "app/build"}, // off-branch — excluded
		{Name: "global", Scope: ""},           // unscoped — always in scope, last
	}}
	chain := []ResolvedCommand{{Name: "app"}, {Name: "deploy"}, {Name: "aws"}}
	var names []string
	for _, f := range b.chainConfigFiles(chain) {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, ","), "aws,deploy,rootA,rootB,global"; got != want {
		t.Errorf("chainConfigFiles = %q, want %q", got, want)
	}
}

// ── BindError ───────────────────────────────────────────────.

// beEnv is a minimal env channel: a bool that "junk" cannot coerce into, and a
// secret int whose bad value must never reach a BindError message.
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
	return NewBinder(BindMeta{}).Bind(rtx, &in)
}

// TestBindError_envCoercion_isCleanUsage is EH4's headline: the famous
// "recon: coerce …: string → bool" leak becomes a typed, categorized,
// non-leaky *BindError — while the recon cause stays reachable via errors.As.
func TestBindError_envCoercion_isCleanUsage(t *testing.T) {
	t.Setenv("LOUD", "junk") // not a bool
	err := beBind(t)
	if err == nil {
		t.Fatal("Bind = nil, want a coercion error for LOUD=junk")
	}

	// Typed + structured.
	var be *BindError
	if !errors.As(err, &be) {
		t.Fatalf("err is not a *BindError: %T (%v)", err, err)
	}
	if be.Channel != channelEnv || be.Input != "loud" {
		t.Errorf("BindError = {Channel:%q Input:%q}, want {env loud}", be.Channel, be.Input)
	}

	// Categorized as usage (errors.Is AND CategoryOf), not internal.
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

// TestBindError_secretNeverLeaks: a coercion failure on a secret-tagged input
// must not put the offending value in the message.
func TestBindError_secretNeverLeaks(t *testing.T) {
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

// TestBindError_typeContract pins the type's category + unwrap behavior directly,
// independent of any channel: Error is the clean message, the category sentinel
// and the cause are both reachable.
func TestBindError_typeContract(t *testing.T) {
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
		t.Error("a nil-cause usage BindError must still match ErrUsage")
	}
}

// ── KeyBinder as an override ─────────────────────────────────────────────.

// A Binder bound under KeyBinder replaces the one Collect would build. Before this
// worked, KeyBinder was exported and documented but had no consumer: binding one had
// no effect, and the advice to bind a custom binder was untrue.
func TestKeyBinder_overridesTheDefault(t *testing.T) {
	custom := NewBinder(BindMeta{EnvPrefix: "SENTINEL"})

	var got *Binder
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = binderFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)
	p.Bind(KeyBinder, custom)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Errorf("binderFor returned %p, want the bound %p — KeyBinder must override", got, custom)
	}
}

// With nothing bound, Collect still works: the default is built from the generated
// descriptor, so binding is an override rather than a prerequisite.
func TestKeyBinder_defaultsWhenUnbound(t *testing.T) {
	var got *Binder
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = binderFor(rtx) }}
	p, _, _ := newTestProgram(h, nil)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("binderFor returned nil with no binder bound")
	}
}

// A nil or wrongly-typed binding falls back rather than panicking mid-run.
func TestKeyBinder_ignoresUnusableBindings(t *testing.T) {
	for name, value := range map[string]any{"nil": (*Binder)(nil), "wrong type": "not a binder"} {
		t.Run(name, func(t *testing.T) {
			var got *Binder
			h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { got = binderFor(rtx) }}
			p, _, _ := newTestProgram(h, nil)
			p.Bind(KeyBinder, value)

			if _, err := p.Run([]string{"run", "x"}); err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Error("an unusable KeyBinder binding produced a nil binder instead of the default")
			}
		})
	}
}

// ── raw stdin formats (text / lines) ─────────────────────────────────────────
//
// The grep/jq/fmt family, whose stdin is not a document. Before these formats a command
// consuming plain text could not declare its stdin channel at all: it read rtx.Stdin
// directly, which appears in no help page, no completion and no validation.

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

func TestBinder_stdinText(t *testing.T) {
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
			if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
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

func TestBinder_stdinLines(t *testing.T) {
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
			if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
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

// TestBinder_rawStdinNilVsEmpty: the payload is a POINTER so a filter can tell "nothing was
// piped" from "an empty payload was piped" — for a filter that is a real difference, and it is
// why the field is not a plain string.
func TestBinder_rawStdinNilVsEmpty(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("")

	var in tbTextInputs
	if err := NewBinder(BindMeta{}).Bind(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if in.App.Stdin != nil {
		t.Errorf("nothing piped left a non-nil payload %q", *in.App.Stdin)
	}
}

// TestBinder_rawStdinRequired: `required: true` on a raw payload rejects an empty stdin, with
// a usage-class message naming the format rather than a nil the handler dereferences.
func TestBinder_rawStdinRequired(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("")

	var in tbTextReqInputs
	err := NewBinder(BindMeta{}).Bind(rtx, &in)
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
