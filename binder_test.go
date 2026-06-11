package rotini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			Constraints: Constraints{Minimum: 1, Maximum: 65535},
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
