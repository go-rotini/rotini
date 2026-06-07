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
