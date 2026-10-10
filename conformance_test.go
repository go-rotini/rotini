package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// This file is the in-process tier of the input conformance suite; `make
// test-conformance` runs it. TestConformance_matrixComplete holds the canonical
// ID list, which with each case's comment is the matrix definition. Every ID
// appears exactly once: here, or in the acceptance tier (acceptance_test.go)
// for cases only a real process can observe (exit codes, auto-detected pipes,
// the no-pipe sentinel).

// ── the acme fixture ─────────────────────────────────────────────────────────
//
// One CLI exercising every input channel, in exactly the shapes codegen emits
// (the internal package's golden generate tests pin that correspondence).

type acRootFlags struct {
	Config  string `rotini:"config"`
	Verbose bool   `rotini:"verbose"`
	Loud    int    `rotini:"loud"` // type:count — the occurrence tally
}
type acRootCmd struct {
	Flags     acRootFlags
	Arguments struct{}
}

type acDeployFlags struct {
	DryRun bool              `rotini:"dry-run"`
	Env    string            `rotini:"env" recon:"acme.env"`
	Output string            `rotini:"output" recon:"acme.output"`
	Labels map[string]string `rotini:"label"`
}
type acDeployEnv struct {
	HTTP   map[string]any `rotini:"http" recon:"http" envnest:"ACME_HTTP,__"`
	Region string         `rotini:"region" recon:"region" env:"ACME_REGION"`
}
type acDeployCmd struct {
	Flags     acDeployFlags
	Arguments struct{}
	Env       acDeployEnv
}
type acDeployInputs struct {
	Acme   acRootCmd
	Deploy acDeployCmd
}

type acWidgetCmd struct {
	Flags     struct{}
	Arguments struct{}
}

type acGetInputs struct {
	Acme   acRootCmd
	Widget acWidgetCmd
	Get    struct {
		Flags     struct{}
		Arguments struct {
			Name string `rotini:"name"`
		}
	}
}

type acDeleteInputs struct {
	Acme   acRootCmd
	Widget acWidgetCmd
	Delete struct {
		Flags     struct{}
		Arguments struct {
			Names []string `rotini:"names"`
		}
	}
}

type acCreateInputs struct {
	Acme   acRootCmd
	Widget acWidgetCmd
	Create struct {
		Flags struct {
			Spec string `rotini:"spec"`
		}
		Arguments struct{}
	}
}

type acApplyInputs struct {
	Acme   acRootCmd
	Widget acWidgetCmd
	Apply  struct {
		Flags struct {
			File string `rotini:"file"`
		}
		Arguments struct{}
	}
}

type acLoginInputs struct {
	Acme  acRootCmd
	Login struct {
		Flags struct {
			Token string `rotini:"token" recon:"acme.token"`
		}
		Arguments struct{}
	}
}

type acRunInputs struct {
	Acme acRootCmd
	Run  struct {
		Flags     struct{}
		Arguments struct {
			Script []string `rotini:"script"`
		}
	}
}

type acImportInputs struct {
	Acme   acRootCmd
	Import struct {
		Flags     struct{}
		Arguments struct {
			Path string `rotini:"path"`
		}
	}
}

type acWrapInputs struct {
	Acme acRootCmd
	Wrap struct {
		Flags     struct{}
		Arguments struct {
			Cmdline []string `rotini:"cmdline"`
		}
	}
}

type acIngestPayload struct {
	Kind string `recon:"kind"`
}
type acIngestInputs struct {
	Acme   acRootCmd
	Ingest struct {
		Flags     struct{}
		Arguments struct{}
		Stdin     *acIngestPayload `stdin:"yaml,required"`
	}
}

func acmeDef() Definition {
	return Definition{
		Name: "acme", Handler: "Acme",
		Flags: []FlagDef{
			{Name: "config", Identifiers: []string{"--config"}, Type: "string"},
			{Name: "verbose", Identifiers: []string{"--verbose", "-v"}, Type: "bool"},
			{Name: "loud", Identifiers: []string{"--loud", "-l"}, Type: "count"},
		},
		Commands: []CommandDef{
			{Name: "deploy", Handler: "AcmeDeploy", Flags: []FlagDef{
				{Name: "dry-run", Identifiers: []string{"--dry-run", "-d"}, Type: "bool"},
				{Name: "env", Identifiers: []string{"--env", "-e"}, Type: "string", Default: "dev"},
				{Name: "output", Identifiers: []string{"--output"}, Type: "string", Default: "table"},
				{Name: "label", Identifiers: []string{"--label"}, Type: "map[string]string"},
			}},
			{Name: "widget", Handler: "AcmeWidget", Commands: []CommandDef{
				{Name: "get", Handler: "AcmeWidgetGet",
					Arguments: []ArgDef{{Name: "name", Type: "string", Required: true}}},
				{Name: "delete", Handler: "AcmeWidgetDelete",
					Arguments: []ArgDef{{Name: "names", Type: "[]string", Variadic: true}}},
				{Name: "create", Handler: "AcmeWidgetCreate", Flags: []FlagDef{
					{Name: "spec", Identifiers: []string{"--spec"}, Type: "string", From: []string{"value", "file"}},
				}},
				{Name: "apply", Handler: "AcmeWidgetApply", Flags: []FlagDef{
					{Name: "file", Identifiers: []string{"--file", "-f"}, Type: "string", From: []string{"value", "stdin"}},
				}},
			}},
			{Name: "login", Handler: "AcmeLogin", Flags: []FlagDef{
				{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, From: []string{"value", "file"}},
			}},
			{Name: "run", Handler: "AcmeRun",
				Arguments: []ArgDef{{Name: "script", Type: "[]string", Variadic: true}}},
			{Name: "import", Handler: "AcmeImport",
				Arguments: []ArgDef{{Name: "path", Type: "string", Required: true}}},
			{Name: "ingest", Handler: "AcmeIngest"},
			{Name: "wrap", Handler: "AcmeWrap", Passthrough: true,
				Arguments: []ArgDef{{Name: "cmdline", Type: "[]string", Variadic: true}}},
		},
	}
}

// acmeMeta is the fixture's InputSettings over a case directory: a main config
// (path suppliable via --config / $ACME_CONFIG — CFG-01, ENV-03), a walk-up
// project config (CFG-03), and an xdg user config (CFG-02), in that
// precedence order.
func acmeMeta(dir string) InputSettings {
	return InputSettings{ConfigFiles: []ConfigFile{
		{Name: "main", Path: filepath.Join(dir, "acme.yaml"), Format: "yaml",
			PathFrom: &PathFromDef{Flag: "config", Env: "ACME_CONFIG"}},
		{Name: "project", Format: "yaml",
			Discover: &DiscoverDef{Strategy: "walk-up", File: ".acme.yaml"}},
		{Name: "user", Format: "yaml",
			Discover: &DiscoverDef{Strategy: "xdg", App: "acme", File: "config.yaml"}},
	}}
}

// ── the harness ──────────────────────────────────────────────────────────────

// inputCase is one matrix entry: a fixture invocation (args/env/stdin/files)
// and its assertion. The runner makes each case hermetic: a fresh case
// directory holding any declared files, the working directory moved to its
// work/ subdirectory (so a "./.acme.yaml" file is one walk-up level above the
// cwd and "work/…" files are in it), a fresh empty $XDG_CONFIG_HOME at xdg/,
// and every ACME_* variable cleared before the case's own env is applied.
type inputCase struct {
	id    string            // the matrix ID, e.g. "PREC-02" — the subtest name
	skip  string            // non-empty → t.Skip(reason): the matrix may lead the code, never trail it
	args  []string          // argv after the program name
	env   map[string]string // applied via t.Setenv
	stdin string            // the piped payload ("" → an empty reader)
	files map[string]string // path (relative to the case dir) → contents
	check func(t *testing.T, rtx *Context, meta InputSettings)
}

// bindAs is the case-body idiom: bind the fixture invocation into T via the
// default InputReader and fail the case on error.
func bindAs[T any](t *testing.T, rtx *Context, meta InputSettings) T {
	t.Helper()
	var in T
	if err := NewInputReader(meta).Read(rtx, &in); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return in
}

func runInputCase(t *testing.T, c inputCase) {
	t.Helper()
	if c.skip != "" {
		t.Skip(c.skip)
	}

	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	xdg := filepath.Join(dir, "xdg")
	for _, d := range []string{work, xdg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range c.files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "ACME_") {
			t.Setenv(name, "") // register the restore...
			os.Unsetenv(name)  // ...then make it genuinely unset (empty ≠ unset: ENV-05)
		}
	}
	for k, v := range c.env {
		t.Setenv(k, v)
	}

	rtx := NewContextFor(acmeDef(), c.args).WithStdin(strings.NewReader(c.stdin))
	c.check(t, rtx, acmeMeta(dir))
}

// ── the matrix, in-process tier ──────────────────────────────────────────────

func conformanceCases() []inputCase {
	return []inputCase{
		// ── ARG — positional arguments ──
		{id: "ARG-01", args: []string{"widget", "get", "my-widget"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acGetInputs](t, rtx, meta); in.Get.Arguments.Name != "my-widget" {
					t.Errorf("name = %q, want my-widget", in.Get.Arguments.Name)
				}
			}},
		{id: "ARG-02", args: []string{"widget", "delete", "w1", "w2", "w3"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeleteInputs](t, rtx, meta)
				if want := []string{"w1", "w2", "w3"}; !reflect.DeepEqual(in.Delete.Arguments.Names, want) {
					t.Errorf("names = %v, want %v", in.Delete.Arguments.Names, want)
				}
			}},
		{id: "ARG-03", args: []string{"deploy"},
			check: func(t *testing.T, rtx *Context, _ InputSettings) {
				// The sub-command token routes: the resolved chain is the input.
				names := chainNames(rtx.CommandChain())
				if want := []string{"acme", "deploy"}; !reflect.DeepEqual(names, want) {
					t.Errorf("chain = %v, want %v", names, want)
				}
			}},
		{id: "ARG-05", args: []string{"import", "./data/widgets.csv"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// The path binds verbatim — relative to the user's CWD, never
				// rewritten; opening it is the handler's job.
				if in := bindAs[acImportInputs](t, rtx, meta); in.Import.Arguments.Path != "./data/widgets.csv" {
					t.Errorf("path = %q, want the verbatim relative path", in.Import.Arguments.Path)
				}
			}},
		{id: "ARG-06", args: []string{"widget", "get", "héllo wörld — ünïcode"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acGetInputs](t, rtx, meta); in.Get.Arguments.Name != "héllo wörld — ünïcode" {
					t.Errorf("name = %q, want the unicode token intact", in.Get.Arguments.Name)
				}
			}},
		{id: "ARG-07", args: []string{"run", "--", "--weird-name"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"--weird-name"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want the dash-prefixed positional %v", in.Run.Arguments.Script, want)
				}
			}},
		{id: "ARG-08", args: []string{"run", "-5", "-0.5"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"-5", "-0.5"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want negative numbers as positionals %v", in.Run.Arguments.Script, want)
				}
			}},

		{id: "ARG-09", args: []string{"--verbose", "wrap", "--dry-run", "-x", "--", "literal", "-"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Passthrough: ancestor flags before the command parse normally;
				// everything after it — flag-shaped tokens, "--", bare "-" — is a
				// raw positional, verbatim and in order.
				in := bindAs[acWrapInputs](t, rtx, meta)
				if !in.Acme.Flags.Verbose {
					t.Error("root --verbose before the passthrough boundary did not parse")
				}
				want := []string{"--dry-run", "-x", "--", "literal", "-"}
				if !reflect.DeepEqual(in.Wrap.Arguments.Cmdline, want) {
					t.Errorf("cmdline = %v, want the raw tokens %v", in.Wrap.Arguments.Cmdline, want)
				}
			}},

		// ── FLAG — flags / options ──
		{id: "FLAG-01", args: []string{"deploy", "--dry-run"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); !in.Deploy.Flags.DryRun {
					t.Error("dry-run = false, want presence = true")
				}
			}},
		{id: "FLAG-02", args: []string{"deploy", "--env", "staging"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "staging" {
					t.Errorf("env = %q, want staging", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-03", args: []string{"deploy", "--env=staging"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "staging" {
					t.Errorf("env = %q, want staging (equals form ≡ space form)", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-04", args: []string{"deploy", "-e", "prod"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod via the short alias", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-05", args: []string{"deploy", "--label", "tier=web", "--label", "app=acme"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				want := map[string]string{"tier": "web", "app": "acme"}
				if !reflect.DeepEqual(in.Deploy.Flags.Labels, want) {
					t.Errorf("labels = %v, want %v", in.Deploy.Flags.Labels, want)
				}
			}},
		{id: "FLAG-06", args: []string{"widget", "create", "--spec", "@widget.json"},
			files: map[string]string{"work/widget.json": `{"kind":"Widget"}` + "\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acCreateInputs](t, rtx, meta); in.Create.Flags.Spec != `{"kind":"Widget"}` {
					t.Errorf("spec = %q, want the file's trimmed contents", in.Create.Flags.Spec)
				}
			}},
		{id: "FLAG-07", args: []string{"run", "--", "--verbose", "./script.sh"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"--verbose", "./script.sh"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want %v", in.Run.Arguments.Script, want)
				}
				if in.Acme.Flags.Verbose {
					t.Error("verbose = true — a flag after the -- terminator must NOT parse as a flag")
				}
			}},
		{id: "FLAG-08", args: []string{"deploy", "--frobnicate"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				var in acDeployInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "--frobnicate") {
					t.Fatalf("err = %v, want an error naming the unknown flag", err)
				}
				if CategoryOf(err) != CategoryUsage {
					t.Errorf("CategoryOf = %v, want usage (the reporter convention maps it to exit %d)", CategoryOf(err), 1)
				}
				var pe *ParseError
				if !errors.As(err, &pe) || len(pe.Candidates) == 0 {
					t.Errorf("ParseError.Candidates = %v, want the flag vocabulary for suggestions", pe)
				}
			}},
		{id: "FLAG-09", args: []string{"deploy", "-vd"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if !in.Acme.Flags.Verbose || !in.Deploy.Flags.DryRun {
					t.Errorf("cluster -vd: verbose=%v dry-run=%v, want both true", in.Acme.Flags.Verbose, in.Deploy.Flags.DryRun)
				}
			}},
		{id: "FLAG-10", args: []string{"deploy", "--env="},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// An explicit empty value is present-and-empty; absence falls
				// to the default. Both halves asserted here.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "" {
					t.Errorf("--env= bound %q, want the explicit empty string", in.Deploy.Flags.Env)
				}
				rtx2 := NewContextFor(acmeDef(), []string{"deploy"})
				if in := bindAs[acDeployInputs](t, rtx2, meta); in.Deploy.Flags.Env != "dev" {
					t.Errorf("absent --env bound %q, want the default dev", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-11", args: []string{"deploy", "--env", "a", "--env", "b"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Pinned: a repeated scalar flag is last-wins, not an error.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "b" {
					t.Errorf("env = %q, want b (last occurrence wins)", in.Deploy.Flags.Env)
				}
			}},

		// ── STDIN — standard input ──
		{id: "STDIN-01", args: []string{"widget", "apply", "-f", "-"},
			stdin: "apiVersion: acme/v1\nkind: Widget\n",
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acApplyInputs](t, rtx, meta)
				if want := "apiVersion: acme/v1\nkind: Widget"; in.Apply.Flags.File != want {
					t.Errorf("file = %q, want the piped document (trimmed)", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-03", args: []string{"widget", "apply", "-f", "-"},
			stdin: "apiVersion: acme/v1\nkind: Widget\nmetadata: { name: demo }\n",
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// A heredoc is stdin by the time it reaches the process; the
				// multi-line document arrives intact.
				in := bindAs[acApplyInputs](t, rtx, meta)
				if !strings.Contains(in.Apply.Flags.File, "name: demo") {
					t.Errorf("file = %q, want the inline document with name: demo", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-04", args: []string{"widget", "apply", "-f", "/dev/fd/63"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Process substitution hands the program a PATH — it binds
				// verbatim like any path value (the handler opens it).
				if in := bindAs[acApplyInputs](t, rtx, meta); in.Apply.Flags.File != "/dev/fd/63" {
					t.Errorf("file = %q, want the literal fd path", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-05", args: []string{"widget", "apply", "-f", "-"}, stdin: "",
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				var in acApplyInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "stdin is empty") {
					t.Errorf("err = %v, want the explicit empty-input error, never a silent no-op", err)
				}
			}},
		{id: "STDIN-06", args: []string{"ingest"}, stdin: "{{{{ not a document \x00",
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				var in acIngestInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "could not parse stdin as yaml at line 1") {
					t.Errorf("err = %v, want a loud decode error for a malformed payload", err)
				}
			}},

		// ── ENV — environment variables ──
		{id: "ENV-01", args: []string{"login"}, env: map[string]string{"ACME_TOKEN": "sk_live_xxx"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Bound from the environment, never from argv. (Never echoed
				// is SEC-03's sweep.)
				if in := bindAs[acLoginInputs](t, rtx, meta); in.Login.Flags.Token != "sk_live_xxx" {
					t.Errorf("token = %q, want the env-supplied secret", in.Login.Flags.Token)
				}
			}},
		{id: "ENV-02", args: []string{"deploy"}, env: map[string]string{"ACME_ENV": "prod"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod from $ACME_ENV (no --env given)", in.Deploy.Flags.Env)
				}
			}},
		{id: "ENV-03", args: []string{"deploy"},
			env:   map[string]string{"ACME_CONFIG": "../alt/alt.yaml"},
			files: map[string]string{"alt/alt.yaml": "acme:\n  output: from-env-named\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// $ACME_CONFIG names the active config file (two-phase: the
				// env var is read before the file channel opens anything).
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-env-named" {
					t.Errorf("output = %q, want the env-named file's value", in.Deploy.Flags.Output)
				}
			}},
		{id: "ENV-04", args: []string{"deploy"},
			env: map[string]string{"ACME_HTTP__TIMEOUT": "30s"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if got := in.Deploy.Env.HTTP["timeout"]; got != "30s" {
					t.Errorf("http[timeout] = %#v, want %q via the __ convention", got, "30s")
				}
			}},
		{id: "ENV-05", args: []string{"deploy"}, env: map[string]string{"ACME_REGION": ""},
			check: func(t *testing.T, rtx *Context, _ InputSettings) {
				// Presence semantics: an empty-string variable is set; an
				// unset one is not. The env layer's Presence distinguishes them.
				layer, err := rtx.EnvInputs[acDeployInputs]()
				if err != nil {
					t.Fatalf("EnvInputs: %v", err)
				}
				if _, ok := layer.Set["Deploy.Env.Region"]; !ok {
					t.Error("empty $ACME_REGION not recorded as present — empty must differ from unset")
				}
				os.Unsetenv("ACME_REGION")
				layer2, err := NewContextFor(acmeDef(), rtx.Argv).EnvInputs[acDeployInputs]()
				if err != nil {
					t.Fatalf("EnvInputs(unset): %v", err)
				}
				if _, ok := layer2.Set["Deploy.Env.Region"]; ok {
					t.Error("unset $ACME_REGION recorded as present")
				}
			}},

		{id: "ENV-06", args: []string{"deploy"},
			env: map[string]string{
				"ACME_CITY":   "portland",          // prefixed derived name → binds
				"CITY":        "wrong",             // unprefixed conventional name → scoped out
				"MOOD":        "wrong",             // ditto, with no prefixed var at all
				"ACME_OUTPUT": "from-prefixed-env", // a flag's env fallback, prefixed
				"PLAIN_OTHER": "exempt-value",      // explicit variable: exempt from the prefix
			},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// env_prefix scopes every derived env name under PREFIX_; explicit
				// variable: names stay exact.
				meta.EnvPrefix = "ACME"
				type envPrefixInputs struct {
					Acme   acRootCmd
					Deploy struct {
						Flags struct {
							Output string `rotini:"output" recon:"output"`
						}
						Arguments struct{}
						Env       struct {
							City  string `rotini:"city" recon:"city"`
							Mood  string `rotini:"mood" recon:"mood"`
							Other string `rotini:"other" recon:"other" env:"PLAIN_OTHER"`
						}
					}
				}
				in := bindAs[envPrefixInputs](t, rtx, meta)
				if in.Deploy.Env.City != "portland" {
					t.Errorf("city = %q, want the ACME_CITY value", in.Deploy.Env.City)
				}
				if in.Deploy.Env.Mood != "" {
					t.Errorf("mood = %q, want empty — bare MOOD must not bind under a prefix", in.Deploy.Env.Mood)
				}
				if in.Deploy.Env.Other != "exempt-value" {
					t.Errorf("other = %q, want the explicit PLAIN_OTHER value (prefix-exempt)", in.Deploy.Env.Other)
				}
				if in.Deploy.Flags.Output != "from-prefixed-env" {
					t.Errorf("output = %q, want the ACME_OUTPUT flag fallback", in.Deploy.Flags.Output)
				}
			}},

		{id: "FLAG-12", args: []string{"--loud", "-ll", "deploy"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// A count flag tallies occurrences across long, short, and
				// clustered forms — no value is ever consumed.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Acme.Flags.Loud != 3 {
					t.Errorf("loud = %d, want 3 (--loud + -ll)", in.Acme.Flags.Loud)
				}
				// The value form is a parse error: there is no value to give.
				rtx2 := NewContextFor(acmeDef(), []string{"--loud=2", "deploy"})
				var in acDeployInputs
				if err := NewInputReader(meta).Read(rtx2, &in); err == nil || !strings.Contains(err.Error(), "takes no value") {
					t.Errorf("Bind(--loud=2) = %v, want the counts-occurrences parse error", err)
				}
				// Unset stays the zero tally.
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), []string{"deploy"}), meta); in.Acme.Flags.Loud != 0 {
					t.Errorf("unset loud = %d, want 0", in.Acme.Flags.Loud)
				}
			}},

		// ── CFG — config files ──
		{id: "CFG-01", args: []string{"--config", "../explicit.yaml", "deploy"},
			files: map[string]string{"explicit.yaml": "acme:\n  output: from-explicit\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-explicit" {
					t.Errorf("output = %q, want exactly the --config file's value", in.Deploy.Flags.Output)
				}
				// The other half: an explicitly named file that is missing errors.
				rtx2 := NewContextFor(acmeDef(), []string{"--config", "../no-such.yaml", "deploy"})
				var in acDeployInputs
				if err := NewInputReader(meta).Read(rtx2, &in); err == nil {
					t.Error("missing --config file bound silently, want a loud error")
				}
			}},
		{id: "CFG-02", args: []string{"deploy"},
			files: map[string]string{"xdg/acme/config.yaml": "acme:\n  output: from-xdg\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-xdg" {
					t.Errorf("output = %q, want the discovered xdg value (absence elsewhere is OK)", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-03", args: []string{"deploy"},
			files: map[string]string{
				".acme.yaml":           "acme:\n  output: from-project\n", // one walk-up level above the cwd
				"xdg/acme/config.yaml": "acme:\n  output: from-user\n",
			},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-project" {
					t.Errorf("output = %q, want from-project — project-local wins over user-global", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-04", args: []string{"--config", "../broken.yaml", "deploy"},
			files: map[string]string{"broken.yaml": ":: definitely [ not yaml\n  - ::\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				var in acDeployInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil {
					t.Fatal("malformed config bound silently, want a parse error")
				}
				if !strings.Contains(err.Error(), "broken.yaml") {
					t.Errorf("err = %v, want the offending file named", err)
				}
				// A malformed user-supplied file is a usage-class *InputError
				// that does not leak recon's parser text.
				var be *InputError
				if !errors.As(err, &be) || be.Channel != "config" {
					t.Errorf("err = %v, want a config *InputError", err)
				}
				if CategoryOf(err) != CategoryUsage {
					t.Errorf("CategoryOf = %v, want usage for a malformed config file", CategoryOf(err))
				}
				if strings.Contains(err.Error(), "recon") {
					t.Errorf("err = %q, leaks recon text", err.Error())
				}
			}},
		{id: "CFG-05", args: []string{"deploy"},
			files: map[string]string{
				"acme.yaml":  "acme:\n  output: from-main\n",
				".acme.yaml": "acme:\n  output: from-project\n",
			},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Two files declare the same key: declared order is precedence
				// within the file layer — main is declared first and wins.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-main" {
					t.Errorf("output = %q, want from-main (declared order is precedence)", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-06", args: []string{"deploy"},
			files: map[string]string{"acme.yaml": "acme:\n  output: secret-perms\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if runtime.GOOS == "windows" {
					t.Skip("0o000 permission bits don't deny the owner on windows")
				}
				if os.Geteuid() == 0 {
					t.Skip("running as root: permission bits don't bite")
				}
				if err := os.Chmod(filepath.Join("..", "acme.yaml"), 0o000); err != nil {
					t.Fatal(err)
				}
				var in acDeployInputs
				if err := NewInputReader(meta).Read(rtx, &in); err == nil {
					t.Error("unreadable config bound silently, want a loud permission error")
				}
			}},

		{id: "CFG-07", args: []string{"deploy"},
			files: map[string]string{"acme.yaml": "acme:\n  output: json\n"}, // no acme.env — violates the schema below
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// A configuration_files entry's schema: gates the loaded
				// document at bind time, before any value is read.
				meta.ConfigFiles[0].Schema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["acme"],"properties":{"acme":{"type":"object","required":["env"]}}}`
				var in acDeployInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "acme.yaml") {
					t.Errorf("Bind = %v, want a schema violation naming the file", err)
				}
				// The same schema passes once the file conforms.
				if err := os.WriteFile(filepath.Join("..", "acme.yaml"), []byte("acme:\n  env: prod\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), rtx.Argv), meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod from the now-conforming file", in.Deploy.Flags.Env)
				}
			}},

		// ── SEC — secret-safe input paths ──
		{id: "CFG-08", args: []string{"deploy"},
			files: map[string]string{
				"app.jsonc": "{\n  // jsonc: comments and trailing commas decode\n  \"acme\": {\"output\": \"from-jsonc\"},\n}\n",
				"app.env":   "ACME_GREETING=hello-from-dotenv\n",
			},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// The format enum's jsonc/dotenv members ride the same declared-
				// format passthrough as yaml/json/toml. jsonc keeps dotted keys,
				// so the ordinary flag fallback reads it…
				meta.ConfigFiles = append(meta.ConfigFiles,
					ConfigFile{Name: "extra-jsonc", Path: "../app.jsonc", Format: "jsonc"},
					ConfigFile{Name: "extra-env", Path: "../app.env", Format: "dotenv"},
				)
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-jsonc" {
					t.Errorf("output = %q, want from-jsonc through the declared-format jsonc entry", in.Deploy.Flags.Output)
				}
				// …while dotenv keys stay verbatim (KEY=value lines, no dotted
				// projection): the reading input declares the variable name.
				type dotenvInputs struct {
					Acme   acRootCmd
					Deploy struct {
						Flags     struct{}
						Arguments struct{}
						Config    struct {
							Greeting string `rotini:"greeting" recon:"ACME_GREETING"`
						}
					}
				}
				if in := bindAs[dotenvInputs](t, rtx, meta); in.Deploy.Config.Greeting != "hello-from-dotenv" {
					t.Errorf("greeting = %q, want the dotenv value under its verbatim key", in.Deploy.Config.Greeting)
				}
			}},

		{id: "SEC-01", args: []string{"login", "--token", "@token.txt"},
			files: map[string]string{"work/token.txt": "sk_live_from_file\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				if in := bindAs[acLoginInputs](t, rtx, meta); in.Login.Flags.Token != "sk_live_from_file" {
					t.Errorf("token = %q, want the trimmed file contents (argv carries only the @path)", in.Login.Flags.Token)
				}
			}},
		{id: "SEC-02",
			skip: "out of scope: rotini ships no interactive prompt — " +
				"the secret: schema docs point handlers at rtx.Stdin + any prompt library.",
			check: func(t *testing.T, rtx *Context, meta InputSettings) {}},
		{id: "SEC-03", args: []string{"login"}, env: map[string]string{"ACME_TOKEN": "sk_live_leakme"},
			check: func(t *testing.T, rtx *Context, _ InputSettings) {
				// The redaction sweep: the secret's text must appear in no
				// error or provenance path, across channels.
				secretDef := Definition{
					Name: "acme", Handler: "Acme",
					Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string",
						Secret: true, Enum: []string{"sk_test_only"}}},
				}
				var sink struct {
					Acme struct {
						Flags struct {
							Token string `rotini:"token"`
						}
						Arguments struct{}
					}
				}
				err := NewParser().Parse(NewContextFor(secretDef, []string{"--token", "sk_live_leakme"}), &sink)
				if err == nil {
					t.Fatal("enum violation expected")
				}
				if strings.Contains(err.Error(), "sk_live_leakme") {
					t.Errorf("secret leaked in the enum error: %v", err)
				}
				if !strings.Contains(err.Error(), "[redacted]") {
					t.Errorf("enum error should carry the redaction marker: %v", err)
				}
				// Provenance redacts too (env-supplied secret).
				layer, perr := rtx.EnvInputs[acLoginInputs]()
				if perr != nil {
					t.Fatalf("EnvInputs: %v", perr)
				}
				for path, prov := range layer.Set {
					if strings.Contains(prov.Raw, "sk_live_leakme") {
						t.Errorf("secret leaked in provenance %s: %q", path, prov.Raw)
					}
				}
			}},

		// ── PREC — defaults & precedence (the overlay) ──
		{id: "PREC-01", args: []string{"deploy"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Flags.Output != "table" || in.Deploy.Flags.Env != "dev" {
					t.Errorf("defaults: output=%q env=%q, want table/dev with no input at all", in.Deploy.Flags.Output, in.Deploy.Flags.Env)
				}
			}},
		{id: "PREC-02", args: []string{"deploy", "--env=prod"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  env: test\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// default < config < env < flag, dropped one layer at a time.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod (the flag wins)", in.Deploy.Flags.Env)
				}
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), []string{"deploy"}), meta); in.Deploy.Flags.Env != "staging" {
					t.Errorf("env = %q, want staging (env wins over the file)", in.Deploy.Flags.Env)
				}
				os.Unsetenv("ACME_ENV")
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), []string{"deploy"}), meta); in.Deploy.Flags.Env != "test" {
					t.Errorf("env = %q, want test (the file wins over the default)", in.Deploy.Flags.Env)
				}
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), []string{"deploy"}), InputSettings{}); in.Deploy.Flags.Env != "dev" {
					t.Errorf("env = %q, want the default dev", in.Deploy.Flags.Env)
				}
			}},
		{id: "PREC-03", args: []string{"deploy"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  output: json\n"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Flags.Env != "staging" || in.Deploy.Flags.Output != "json" {
					t.Errorf("env=%q output=%q, want staging/json — unset layers are skipped, not zeroed", in.Deploy.Flags.Env, in.Deploy.Flags.Output)
				}
			}},
		{id: "PREC-04", args: []string{"deploy", "--env=prod"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  env: test\n"},
			check: func(t *testing.T, rtx *Context, _ InputSettings) {
				// Provenance: the Report knows which layer won, and the full
				// history beneath it.
				rtx.WithInputSettings(acmeMeta(filepath.Dir(mustGetwd(t))))
				defaults, _ := rtx.DefaultInputs[acDeployInputs]()
				files, _ := rtx.FileInputs[acDeployInputs]()
				env, _ := rtx.EnvInputs[acDeployInputs]()
				argv, _ := rtx.ArgvInputs[acDeployInputs]()
				_, rep := MergeInputsWithReport(defaults, files, env, argv)
				win, ok := rep.Winner("Deploy.Flags.Env")
				if !ok || win.Layer != "argv" || win.Raw != "prod" {
					t.Errorf("Winner = %+v (ok=%v), want argv/prod", win, ok)
				}
				var layers []string
				for _, p := range rep.History("Deploy.Flags.Env") {
					layers = append(layers, p.Layer)
				}
				if want := []string{"defaults", "files", "env", "argv"}; !reflect.DeepEqual(layers, want) {
					t.Errorf("history = %v, want %v", layers, want)
				}
			}},

		// ── INJ — an injected environment and directory ──
		{id: "INJ-01", args: []string{"deploy"},
			env: map[string]string{"ACME_REGION": "from-process", "ACME_ENV": "from-process"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// The run's environment replaces the process's for every env read. It is the whole
				// environment, so it names the config home too.
				xdg, _ := os.LookupEnv("XDG_CONFIG_HOME")
				rtx.WithEnviron([]string{"ACME_REGION=from-run", "ACME_ENV=from-run", "XDG_CONFIG_HOME=" + xdg})
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Env.Region != "from-run" || in.Deploy.Flags.Env != "from-run" {
					t.Errorf("region=%q env=%q, want both from the injected environment", in.Deploy.Env.Region, in.Deploy.Flags.Env)
				}
			}},
		{id: "INJ-02", args: []string{"deploy"},
			files: map[string]string{
				".acme.yaml":           "acme:\n  output: from-process-cwd\n",
				"other/.acme.yaml":     "acme:\n  output: from-run-dir\n",
				"other/sub/.gitkeep":   "",
				"xdg/acme/config.yaml": "acme:\n  env: from-process-xdg\n",
			},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Walk-up starts at the run's directory, and xdg follows the run's environment.
				caseDir := filepath.Dir(mustGetwd(t))
				rtx.WithDir(filepath.Join(caseDir, "other", "sub")).WithEnviron([]string{"XDG_CONFIG_HOME=" + filepath.Join(caseDir, "nowhere")})
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Flags.Output != "from-run-dir" || in.Deploy.Flags.Env == "from-process-xdg" {
					t.Errorf("output=%q env=%q, want the run directory's file and no process xdg file", in.Deploy.Flags.Output, in.Deploy.Flags.Env)
				}
			}},
		{id: "INJ-03", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A plugin is found on the run's PATH and runs with the run's environment and
				// directory.
				if runtime.GOOS == "windows" {
					t.Skip("a shell-script stand-in for a plugin cannot run on Windows; see e2e r9_plugins")
				}
				bin, work := t.TempDir(), t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "acme-ext"), []byte("#!/bin/sh\necho \"$ACME_MARK\"\npwd\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				def := Definition{Name: "acme", Handler: "Acme", Plugins: []PluginDef{{Name: "ext", Binary: "acme-ext"}}}
				var out strings.Builder
				p := NewProgram(def, nil).WithEnviron([]string{"PATH=" + bin, "ACME_MARK=from-run"}).WithDir(work).
					WithStdout(&out).WithStderr(&out).WithoutSignalHandling()
				if code, err := p.Run([]string{"ext"}); code != 0 {
					t.Fatalf("Run = %d, %v: %s", code, err, out.String())
				}
				lines := strings.Split(strings.TrimSpace(out.String()), "\n")
				gotDir, _ := filepath.EvalSymlinks(lines[len(lines)-1])
				wantDir, _ := filepath.EvalSymlinks(work)
				if lines[0] != "from-run" || gotDir != wantDir {
					t.Errorf("plugin printed %q, want from-run and %s", lines, wantDir)
				}
			}},

		// ── argv words: numbers versus flags, "--", map keys, bounds ──
		{id: "ARG-13", args: []string{"run", "-.5", "-5s", "-1e3"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// Only "-" then a digit, or "-." then a digit, is a number; every other dash
				// word is a flag, so a declared -I takes "-Inf" as -I with the value "nf".
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"-.5", "-5s", "-1e3"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want number-shaped words as positionals %v", in.Run.Arguments.Script, want)
				}
				calc := bindAs[acCalcInputs](t, NewContextFor(acCalcDef(), []string{"-Inf", "-.5"}), meta)
				if calc.Calc.Flags.Include != "nf" || !reflect.DeepEqual(calc.Calc.Arguments.Nums, []float64{-0.5}) {
					t.Errorf("-Inf -.5 = include %q, nums %v; want nf, [-0.5]", calc.Calc.Flags.Include, calc.Calc.Arguments.Nums)
				}
			}},
		{id: "ARG-14", args: []string{"--", "deploy"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// "--" ends command lookup: a sub-command's name after it is a positional, and
				// the error says so instead of calling it unknown.
				var in acDeployInputs
				err := NewInputReader(meta).Read(rtx, &in)
				pe, ok := errors.AsType[*ParseError](err)
				if !ok || !strings.Contains(pe.Msg, `"deploy" after "--" is not read as a command`) {
					t.Fatalf("err = %v, want the ended-lookup message", err)
				}
				if slices.Contains(pe.Candidates, "deploy") {
					t.Errorf("candidates = %v, want deploy left out", pe.Candidates)
				}
			}},
		{id: "FLAG-15", args: []string{"deploy", "--label", "=web"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// A map pair needs a key.
				var in acDeployInputs
				err := NewInputReader(meta).Read(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), `--label needs a key before "="`) || CategoryOf(err) != CategoryUsage {
					t.Errorf("err = %v, want the empty-key usage error", err)
				}
			}},
		{id: "FLAG-16", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, meta InputSettings) {
				// A declared bound implies a finite number: NaN and ±Inf are rejected.
				for _, v := range []string{"nan", "inf", "-Inf"} {
					var in acCalcInputs
					err := NewInputReader(meta).Read(NewContextFor(acCalcDef(), []string{"--ratio=" + v}), &in)
					if err == nil || !strings.Contains(err.Error(), "must be a finite number") {
						t.Errorf("--ratio=%s: err = %v, want a finite-number error", v, err)
					}
				}
			}},

		// ── where flags stop: options_first, a passthrough argument, digit options ──
		{id: "ARG-10", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// options_first: flags stop at the command's first argument.
				in, err := bdParse[bdSSHInputs](t, "ssh", "-p", "22", "host", "-v", "-p", "1")
				if err != nil || in.Ssh.Flags.Port != 22 || in.App.Flags.Verbose || !slices.Equal(in.Ssh.Arguments.Cmd, []string{"-v", "-p", "1"}) {
					t.Errorf("got %+v (verbose %v), %v; want flags after the host passed on", in.Ssh, in.App.Flags.Verbose, err)
				}
			}},
		{id: "ARG-11", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// After an options_first command's first argument, "--" is an argument; before
				// it, "--" is consumed.
				in, err := bdParse[bdSSHInputs](t, "ssh", "host", "--", "x")
				if err != nil || !slices.Equal(in.Ssh.Arguments.Cmd, []string{"--", "x"}) {
					t.Errorf("ssh host -- x: cmd %q, %v; want the -- kept", in.Ssh.Arguments.Cmd, err)
				}
				in, err = bdParse[bdSSHInputs](t, "ssh", "--", "-host", "x")
				if err != nil || in.Ssh.Arguments.Host != "-host" || !slices.Equal(in.Ssh.Arguments.Cmd, []string{"x"}) {
					t.Errorf("ssh -- -host x: %+v, %v", in.Ssh.Arguments, err)
				}
			}},
		{id: "ARG-12", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A cascading short-circuit flag after the boundary is an argument and waives
				// nothing.
				for _, argv := range [][]string{{"ssh", "host", "--help"}, {"exec", "ls", "--help"}} {
					rtx := bdContext(t, argv...)
					if store := quietParse(rtx.CommandChain(), argv); store == nil || shortCircuited(rtx.CommandChain(), store) {
						t.Errorf("%q: short-circuited, want --help passed on", argv)
					}
				}
			}},
		{id: "ARG-15", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A passthrough argument: flags before it parse, every word from it on is raw.
				in, err := bdParse[bdExecInputs](t, "--verbose", "exec", "--region", "eu", "ls", "-la", "--", "--region", "x")
				if err != nil || !in.App.Flags.Verbose || in.Exec.Flags.Region != "eu" ||
					!slices.Equal(in.Exec.Arguments.Command, []string{"ls", "-la", "--", "--region", "x"}) {
					t.Errorf("got %+v, %v", in.Exec, err)
				}
			}},
		{id: "ARG-16", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A variadic before a fixed argument: the fixed one binds from the end.
				in, err := bdParse[bdCpInputs](t, "cp", "a", "b", "dst")
				if err != nil || !slices.Equal(in.Cp.Arguments.Src, []string{"a", "b"}) || in.Cp.Arguments.Dst != "dst" {
					t.Errorf("cp a b dst: %+v, %v", in.Cp.Arguments, err)
				}
			}},
		{id: "FLAG-13", args: []string{"widget", "create", "--spec", "@@literal"},
			check: func(t *testing.T, rtx *Context, meta InputSettings) {
				// A doubled @ on a from: [file] flag is one literal @, read from no file.
				if in := bindAs[acCreateInputs](t, rtx, meta); in.Create.Flags.Spec != "@literal" {
					t.Errorf("spec = %q, want @literal", in.Create.Flags.Spec)
				}
			}},
		{id: "FLAG-14", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A declared digit option is a flag, clustering like a letter; other negative
				// numbers stay numbers.
				in, err := bdParse[bdSSHInputs](t, "ssh", "-46", "host", "-5")
				if err != nil || !in.Ssh.Flags.IPv4 || !in.Ssh.Flags.IPv6 || !slices.Equal(in.Ssh.Arguments.Cmd, []string{"-5"}) {
					t.Errorf("got %+v, %v", in.Ssh, err)
				}
			}},

		// ── response files ──
		{id: "RSP-01", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A response file's lines are words, and can name the command.
				got := runResponse(t, map[string]string{"a.rsp": "exec\n--region\neu\n"}, "@a.rsp", "ls")
				if want := []string{"exec", "--region", "eu", "ls"}; !slices.Equal(got, want) {
					t.Errorf("argv = %q, want %q", got, want)
				}
			}},
		{id: "RSP-02", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// "--" stops expansion.
				got := runResponse(t, map[string]string{"a.rsp": "x\n"}, "exec", "--", "@a.rsp")
				if want := []string{"exec", "--", "@a.rsp"}; !slices.Equal(got, want) {
					t.Errorf("argv = %q, want %q", got, want)
				}
			}},
		{id: "RSP-03", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// A doubled prefix is the literal word with one prefix.
				got := runResponse(t, nil, "exec", "@@a.rsp")
				if want := []string{"exec", "@a.rsp"}; !slices.Equal(got, want) {
					t.Errorf("argv = %q, want %q", got, want)
				}
			}},
		{id: "RSP-04", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// Words read from a file are not expanded again.
				got := runResponse(t, map[string]string{"a.rsp": "exec\n@b.rsp\n", "b.rsp": "never\n"}, "@a.rsp")
				if want := []string{"exec", "@b.rsp"}; !slices.Equal(got, want) {
					t.Errorf("argv = %q, want %q", got, want)
				}
			}},

		// ── repeated values ──
		{id: "FLAG-17", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, meta InputSettings) {
				// repeatable: false makes a second occurrence a usage error naming both values;
				// a flag without it keeps the last value.
				var in acSetInputs
				err := NewInputReader(meta).Read(NewContextFor(acSetDef(), []string{"--name", "a", "--name", "b"}), &in)
				if err == nil || err.Error() != `--name was given more than once ("a", then "b"); it takes one value` || CategoryOf(err) != CategoryUsage {
					t.Errorf("err = %v, want the repeat usage error", err)
				}
				if err := NewInputReader(meta).Read(NewContextFor(acmeDef(), []string{"deploy", "--env", "dev", "--env", "prod"}), &acDeployInputs{}); err != nil {
					t.Errorf("last-wins by default: %v", err)
				}
			}},
		{id: "FLAG-18", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, meta InputSettings) {
				// uniqueItems compares values as the type reads them: 01 is 1.
				var in acSetInputs
				err := NewInputReader(meta).Read(NewContextFor(acSetDef(), []string{"--port", "1", "--port", "01"}), &in)
				if err == nil || err.Error() != `--port must not repeat a value (got "01" twice)` || CategoryOf(err) != CategoryUsage {
					t.Errorf("err = %v, want the duplicate usage error", err)
				}
			}},
		{id: "ENV-07", args: []string{"deploy"}, env: map[string]string{"SET_PORTS": "80,443,80"},
			check: func(t *testing.T, _ *Context, meta InputSettings) {
				// An environment list is checked element by element: uniqueItems and bounds.
				var in acSetInputs
				err := NewInputReader(meta).Read(NewContextFor(acSetDef(), nil), &in)
				if err == nil || !strings.Contains(err.Error(), `SET_PORTS must not repeat a value (got "80" twice)`) {
					t.Errorf("err = %v, want the duplicate error naming the variable", err)
				}
				t.Setenv("SET_PORTS", "80,70000")
				err = NewInputReader(meta).Read(NewContextFor(acSetDef(), nil), &in)
				if err == nil || !strings.Contains(err.Error(), "must be <= 65535 (got 70000)") {
					t.Errorf("err = %v, want the element's bound", err)
				}
			}},
		{id: "ARG-17", args: []string{"deploy"}, check: confArgumentEnvFallback},
		{id: "ARG-18", args: []string{"deploy"}, check: confArgumentConfigFallback},
		{id: "ARG-19", args: []string{"deploy"}, check: confArgumentFrom},
		{id: "FLAG-19", args: []string{"deploy"}, check: confCustomNegation},
		{id: "FLAG-20", args: []string{"deploy"}, check: confEnumValueForms},
		{id: "FLAG-21", args: []string{"deploy"}, check: confTimeLayouts},
		{id: "FLAG-22", args: []string{"deploy"}, check: confRelativeTime},
		{id: "FLAG-23", args: []string{"deploy"}, check: confPatternKinds},
		{id: "ENV-08", args: []string{"deploy"}, check: confEnvListSeparator},

		// ── FILE — input and output files ──
		{id: "FILE-01", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// "-" among inputfile values is stdin, read in place.
				if got := fileCat(t, "piped\n", "a.txt", "-", "b.txt"); got != "a\npiped\nb\n" {
					t.Errorf("cat a.txt - b.txt = %q", got)
				}
			}},
		{id: "FILE-02", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// stdin can be named only once.
				if _, err := fileParse(t, "-", "a.txt", "-"); err == nil || CategoryOf(err) != CategoryUsage {
					t.Errorf("err = %v, want a usage error", err)
				}
			}},
		{id: "FILE-03", args: []string{"deploy"},
			check: func(t *testing.T, _ *Context, _ InputSettings) {
				// "-" as an outputfile is stdout; an existing file is kept without Overwrite.
				fileOutput(t)
			}},

		// ── flag sets, value rules, literal-free secrets, hidden spellings ──
		{id: "FLAG-24", args: []string{"deploy"}, check: confFlagSetMember},
		{id: "FLAG-25", args: []string{"deploy"}, check: confHiddenIdentifier},
		{id: "DEP-01", args: []string{"deploy"}, check: confDependencyEquals},
		{id: "DEP-02", args: []string{"deploy"}, check: confDependencyArgvOnly},
		{id: "DEP-03", args: []string{"deploy"}, check: confDependencyForbids},
		{id: "DEP-04", args: []string{"deploy"}, check: confDependencyUnless},
		{id: "DEP-05", args: []string{"deploy"}, check: confDependencyTyped},
		{id: "SEC-04", args: []string{"deploy"}, check: confSecretLiteralFree},
	}
}

// acCalcDef is a small CLI beside the acme fixture for the cases about number-shaped words.
func acCalcDef() Definition {
	return Definition{
		Name: "calc", Handler: "Calc",
		Flags: []FlagDef{
			{Name: "include", Identifiers: []string{"--include", "-I"}, Type: "string"},
			{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64", ExclusiveMinimum: new(0.0)},
		},
		Arguments: []ArgDef{{Name: "nums", Type: "[]float64", Variadic: true}},
	}
}

type acCalcInputs struct {
	Calc struct {
		Flags struct {
			Include string  `rotini:"include"`
			Ratio   float64 `rotini:"ratio"`
		}
		Arguments struct {
			Nums []float64 `rotini:"nums"`
		}
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// TestConformance_InputMatrix runs the in-process tier of the matrix.
func TestConformance_InputMatrix(t *testing.T) {
	for _, c := range conformanceCases() {
		t.Run(c.id, func(t *testing.T) { runInputCase(t, c) })
	}
}

// TestConformance_matrixComplete enforces the suite's shape: every matrix ID
// appears exactly once across both tiers, and nothing is missing.
func TestConformance_matrixComplete(t *testing.T) {
	want := []string{
		"ARG-01", "ARG-02", "ARG-03", "ARG-04", "ARG-05", "ARG-06", "ARG-07", "ARG-08", "ARG-09",
		"FLAG-01", "FLAG-02", "FLAG-03", "FLAG-04", "FLAG-05", "FLAG-06", "FLAG-07", "FLAG-08",
		"FLAG-09", "FLAG-10", "FLAG-11", "FLAG-12",
		"STDIN-01", "STDIN-02", "STDIN-03", "STDIN-04", "STDIN-05", "STDIN-06", "STDIN-07",
		"ENV-01", "ENV-02", "ENV-03", "ENV-04", "ENV-05", "ENV-06",
		"CFG-01", "CFG-02", "CFG-03", "CFG-04", "CFG-05", "CFG-06", "CFG-07", "CFG-08",
		"SEC-01", "SEC-02", "SEC-03",
		"PREC-01", "PREC-02", "PREC-03", "PREC-04",
		"INJ-01", "INJ-02", "INJ-03",
		"ARG-13", "ARG-14", "FLAG-15", "FLAG-16",
		"ARG-10", "ARG-11", "ARG-12", "ARG-15", "ARG-16", "FLAG-13", "FLAG-14",
		"RSP-01", "RSP-02", "RSP-03", "RSP-04",
		"FLAG-17", "FLAG-18", "ENV-07",
		"ARG-17", "ARG-18", "ARG-19", "FLAG-19", "FLAG-20", "FLAG-21", "FLAG-22", "FLAG-23", "ENV-08",
		"FILE-01", "FILE-02", "FILE-03",
		"STDIN-08", "STDIN-09", "STDIN-10", "STDIN-11", "STDIN-12", "STDIN-13", "STDIN-14",
		"LIST-01", "LIST-02", "LIST-03", "ENV-09", "ENV-10", "ENV-11", "PREC-05", "CFG-09", "CFG-10",
		"FLAG-24", "FLAG-25", "DEP-01", "DEP-02", "DEP-03", "DEP-04", "DEP-05", "SEC-04",
		"INJ-04", "PREC-06",
		"FLAG-26", "ENV-12", "ENV-13", "CFG-11", "CFG-12", "ARG-20",
		"OUT-01", "OUT-02", "OUT-03", "OUT-04",
		"PROF-01", "PROF-02", "PROF-03", "PROF-04", "PROF-05", "PROF-06", "PROF-07", "PROF-08", "PROF-09",
		"WRITE-01", "WRITE-02", "WRITE-03", "WRITE-04", "WRITE-05", "WRITE-06", "WRITE-07", "WRITE-08",
	}
	seen := map[string]int{}
	for _, c := range conformanceCases() {
		seen[c.id]++
	}
	for _, c := range append(dataConformanceCases(), runtimeConformanceCases()...) {
		seen[c.id]++
	}
	for _, c := range pathConformanceCases() {
		seen[c.id]++
	}
	for _, c := range outputConformanceCases() {
		seen[c.id]++
	}
	for _, c := range profileConformanceCases() {
		seen[c.id]++
	}
	for _, c := range writeConformanceCases() {
		seen[c.id]++
	}
	for _, id := range acceptanceMatrixIDs {
		seen[id]++
	}
	for _, id := range want {
		if n := seen[id]; n != 1 {
			t.Errorf("matrix ID %s appears %d times, want exactly once", id, n)
		}
		delete(seen, id)
	}
	for id, n := range seen {
		t.Errorf("unexpected matrix ID %s (×%d) — extend the canonical list", id, n)
	}
}

// runResponse runs boundaryDef with response files on, in a directory holding files, and
// returns the argv its handler saw.
func runResponse(t *testing.T, files map[string]string, argv ...string) []string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	def := boundaryDef()
	def.ResponseFiles = &ResponseFilesDef{Prefix: "@"}
	var out strings.Builder
	var seen []string
	if code, err := bdProgram(def, dir, &out, &seen).Run(argv); code != 0 {
		t.Fatalf("Run(%q) = %d, %v: %s", argv, code, err, out.String())
	}
	return seen
}
