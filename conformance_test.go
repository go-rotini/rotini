package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// This file is the in-process tier of the input conformance suite (the
// business-like input matrix of .docs/ROTINI_INPUT_BEHAVIOR.md, encoded as
// code). `make test-conformance` runs it. Each matrix ID appears exactly once
// across the whole suite — most here, and the handful that only a real
// process can witness (exit codes, auto-detected pipes, the no-pipe sentinel)
// in the acceptance tier (acceptance_test.go); TestConformance_matrixComplete
// enforces the exactly-once split.

// ── the acme fixture ─────────────────────────────────────────────────────────
//
// One CLI exercising every input channel, in exactly the shapes codegen emits
// (the internal package's golden generate tests pin that correspondence).

type acRootFlags struct {
	Config  string `rotini:"config"`
	Verbose bool   `rotini:"verbose"`
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
		},
	}
}

// acmeMeta is the fixture's BindMeta over a case directory: a main config
// (path suppliable via --config / $ACME_CONFIG — CFG-01, ENV-03), a walk-up
// project config (CFG-03), and an xdg user config (CFG-02), in that
// precedence order.
func acmeMeta(dir string) BindMeta {
	return BindMeta{ConfigFiles: []ConfigFile{
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
	check func(t *testing.T, rtx *Context, meta BindMeta)
}

// bindAs is the case-body idiom: bind the fixture invocation into T via the
// default Binder and fail the case on error.
func bindAs[T any](t *testing.T, rtx *Context, meta BindMeta) T {
	t.Helper()
	var in T
	if err := NewBinder(meta).Bind(rtx, &in); err != nil {
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

	rtx := NewContextFor(acmeDef(), c.args)
	rtx.Stdin = strings.NewReader(c.stdin)
	c.check(t, rtx, acmeMeta(dir))
}

// ── the matrix, in-process tier ──────────────────────────────────────────────

func conformanceCases() []inputCase {
	return []inputCase{
		// ── ARG — positional arguments ──
		{id: "ARG-01", args: []string{"widget", "get", "my-widget"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acGetInputs](t, rtx, meta); in.Get.Arguments.Name != "my-widget" {
					t.Errorf("name = %q, want my-widget", in.Get.Arguments.Name)
				}
			}},
		{id: "ARG-02", args: []string{"widget", "delete", "w1", "w2", "w3"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeleteInputs](t, rtx, meta)
				if want := []string{"w1", "w2", "w3"}; !reflect.DeepEqual(in.Delete.Arguments.Names, want) {
					t.Errorf("names = %v, want %v", in.Delete.Arguments.Names, want)
				}
			}},
		{id: "ARG-03", args: []string{"deploy"},
			check: func(t *testing.T, rtx *Context, _ BindMeta) {
				// The sub-command token routes: the resolved chain is the input.
				names := chainNames(rtx.Chain())
				if want := []string{"acme", "deploy"}; !reflect.DeepEqual(names, want) {
					t.Errorf("chain = %v, want %v", names, want)
				}
			}},
		{id: "ARG-05", args: []string{"import", "./data/widgets.csv"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// The path binds verbatim — relative to the user's CWD, never
				// rewritten; opening it is the handler's job.
				if in := bindAs[acImportInputs](t, rtx, meta); in.Import.Arguments.Path != "./data/widgets.csv" {
					t.Errorf("path = %q, want the verbatim relative path", in.Import.Arguments.Path)
				}
			}},
		{id: "ARG-06", args: []string{"widget", "get", "héllo wörld — ünïcode"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acGetInputs](t, rtx, meta); in.Get.Arguments.Name != "héllo wörld — ünïcode" {
					t.Errorf("name = %q, want the unicode token intact", in.Get.Arguments.Name)
				}
			}},
		{id: "ARG-07", args: []string{"run", "--", "--weird-name"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"--weird-name"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want the dash-prefixed positional %v", in.Run.Arguments.Script, want)
				}
			}},
		{id: "ARG-08", args: []string{"run", "-5", "-0.5"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"-5", "-0.5"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want negative numbers as positionals %v", in.Run.Arguments.Script, want)
				}
			}},

		// ── FLAG — flags / options ──
		{id: "FLAG-01", args: []string{"deploy", "--dry-run"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); !in.Deploy.Flags.DryRun {
					t.Error("dry-run = false, want presence = true")
				}
			}},
		{id: "FLAG-02", args: []string{"deploy", "--env", "staging"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "staging" {
					t.Errorf("env = %q, want staging", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-03", args: []string{"deploy", "--env=staging"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "staging" {
					t.Errorf("env = %q, want staging (equals form ≡ space form)", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-04", args: []string{"deploy", "-e", "prod"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod via the short alias", in.Deploy.Flags.Env)
				}
			}},
		{id: "FLAG-05", args: []string{"deploy", "--label", "tier=web", "--label", "app=acme"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				want := map[string]string{"tier": "web", "app": "acme"}
				if !reflect.DeepEqual(in.Deploy.Flags.Labels, want) {
					t.Errorf("labels = %v, want %v", in.Deploy.Flags.Labels, want)
				}
			}},
		{id: "FLAG-06", args: []string{"widget", "create", "--spec", "@widget.json"},
			files: map[string]string{"work/widget.json": `{"kind":"Widget"}` + "\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acCreateInputs](t, rtx, meta); in.Create.Flags.Spec != `{"kind":"Widget"}` {
					t.Errorf("spec = %q, want the file's trimmed contents", in.Create.Flags.Spec)
				}
			}},
		{id: "FLAG-07", args: []string{"run", "--", "--verbose", "./script.sh"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acRunInputs](t, rtx, meta)
				if want := []string{"--verbose", "./script.sh"}; !reflect.DeepEqual(in.Run.Arguments.Script, want) {
					t.Errorf("script = %v, want %v", in.Run.Arguments.Script, want)
				}
				if in.Acme.Flags.Verbose {
					t.Error("verbose = true — a flag after the -- terminator must NOT parse as a flag")
				}
			}},
		{id: "FLAG-08", args: []string{"deploy", "--frobnicate"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				var in acDeployInputs
				err := NewBinder(meta).Bind(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "--frobnicate") {
					t.Fatalf("err = %v, want an error naming the unknown flag", err)
				}
				if CategoryOf(err) != CategoryUsage {
					t.Errorf("CategoryOf = %v, want usage (the funnel convention maps it to exit %d)", CategoryOf(err), ExitUsage)
				}
				var pe *ParseError
				if !errors.As(err, &pe) || len(pe.Candidates) == 0 {
					t.Errorf("ParseError.Candidates = %v, want the flag vocabulary for suggestions", pe)
				}
			}},
		{id: "FLAG-09", args: []string{"deploy", "-vd"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if !in.Acme.Flags.Verbose || !in.Deploy.Flags.DryRun {
					t.Errorf("cluster -vd: verbose=%v dry-run=%v, want both true", in.Acme.Flags.Verbose, in.Deploy.Flags.DryRun)
				}
			}},
		{id: "FLAG-10", args: []string{"deploy", "--env="},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
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
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// Pinned: a repeated scalar flag is last-wins, not an error.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "b" {
					t.Errorf("env = %q, want b (last occurrence wins)", in.Deploy.Flags.Env)
				}
			}},

		// ── STDIN — standard input ──
		{id: "STDIN-01", args: []string{"widget", "apply", "-f", "-"},
			stdin: "apiVersion: acme/v1\nkind: Widget\n",
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acApplyInputs](t, rtx, meta)
				if want := "apiVersion: acme/v1\nkind: Widget"; in.Apply.Flags.File != want {
					t.Errorf("file = %q, want the piped document (trimmed)", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-03", args: []string{"widget", "apply", "-f", "-"},
			stdin: "apiVersion: acme/v1\nkind: Widget\nmetadata: { name: demo }\n",
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// A heredoc IS stdin by the time it reaches the process; the
				// multi-line document arrives intact.
				in := bindAs[acApplyInputs](t, rtx, meta)
				if !strings.Contains(in.Apply.Flags.File, "name: demo") {
					t.Errorf("file = %q, want the inline document with name: demo", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-04", args: []string{"widget", "apply", "-f", "/dev/fd/63"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// Process substitution hands the program a PATH — it binds
				// verbatim like any path value (the handler opens it).
				if in := bindAs[acApplyInputs](t, rtx, meta); in.Apply.Flags.File != "/dev/fd/63" {
					t.Errorf("file = %q, want the literal fd path", in.Apply.Flags.File)
				}
			}},
		{id: "STDIN-05", args: []string{"widget", "apply", "-f", "-"}, stdin: "",
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				var in acApplyInputs
				err := NewBinder(meta).Bind(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "stdin is empty") {
					t.Errorf("err = %v, want the explicit empty-input error, never a silent no-op", err)
				}
			}},
		{id: "STDIN-06", args: []string{"ingest"}, stdin: "{{{{ not a document \x00",
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				var in acIngestInputs
				err := NewBinder(meta).Bind(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "decode") {
					t.Errorf("err = %v, want a loud decode error for a malformed payload", err)
				}
			}},

		// ── ENV — environment variables ──
		{id: "ENV-01", args: []string{"login"}, env: map[string]string{"ACME_TOKEN": "sk_live_xxx"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// Bound from the environment, never from argv. (Never echoed
				// is SEC-03's sweep.)
				if in := bindAs[acLoginInputs](t, rtx, meta); in.Login.Flags.Token != "sk_live_xxx" {
					t.Errorf("token = %q, want the env-supplied secret", in.Login.Flags.Token)
				}
			}},
		{id: "ENV-02", args: []string{"deploy"}, env: map[string]string{"ACME_ENV": "prod"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod from $ACME_ENV (no --env given)", in.Deploy.Flags.Env)
				}
			}},
		{id: "ENV-03", args: []string{"deploy"},
			env:   map[string]string{"ACME_CONFIG": "../alt/alt.yaml"},
			files: map[string]string{"alt/alt.yaml": "acme:\n  output: from-env-named\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// $ACME_CONFIG names the active config file (two-phase: the
				// env var is read before the file channel opens anything).
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-env-named" {
					t.Errorf("output = %q, want the env-named file's value", in.Deploy.Flags.Output)
				}
			}},
		{id: "ENV-04", args: []string{"deploy"},
			env: map[string]string{"ACME_HTTP__TIMEOUT": "30s"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if got := in.Deploy.Env.HTTP["timeout"]; got != "30s" {
					t.Errorf("http[timeout] = %#v, want %q via the __ convention", got, "30s")
				}
			}},
		{id: "ENV-05", args: []string{"deploy"}, env: map[string]string{"ACME_REGION": ""},
			check: func(t *testing.T, rtx *Context, _ BindMeta) {
				// Presence semantics: an empty-string variable IS set; an
				// unset one is not. The env layer's Presence distinguishes them.
				layer, err := ParseEnv[acDeployInputs](NewBinder(BindMeta{}), rtx)
				if err != nil {
					t.Fatalf("ParseEnv: %v", err)
				}
				if _, ok := layer.Set["Deploy.Env.Region"]; !ok {
					t.Error("empty $ACME_REGION not recorded as present — empty must differ from unset")
				}
				os.Unsetenv("ACME_REGION")
				layer2, err := ParseEnv[acDeployInputs](NewBinder(BindMeta{}), NewContextFor(acmeDef(), rtx.Args))
				if err != nil {
					t.Fatalf("ParseEnv(unset): %v", err)
				}
				if _, ok := layer2.Set["Deploy.Env.Region"]; ok {
					t.Error("unset $ACME_REGION recorded as present")
				}
			}},

		// ── CFG — config files ──
		{id: "CFG-01", args: []string{"--config", "../explicit.yaml", "deploy"},
			files: map[string]string{"explicit.yaml": "acme:\n  output: from-explicit\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-explicit" {
					t.Errorf("output = %q, want exactly the --config file's value", in.Deploy.Flags.Output)
				}
				// The other half: an explicitly named file that is missing errors.
				rtx2 := NewContextFor(acmeDef(), []string{"--config", "../no-such.yaml", "deploy"})
				var in acDeployInputs
				if err := NewBinder(meta).Bind(rtx2, &in); err == nil {
					t.Error("missing --config file bound silently, want a loud error")
				}
			}},
		{id: "CFG-02", args: []string{"deploy"},
			files: map[string]string{"xdg/acme/config.yaml": "acme:\n  output: from-xdg\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-xdg" {
					t.Errorf("output = %q, want the discovered xdg value (absence elsewhere is OK)", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-03", args: []string{"deploy"},
			files: map[string]string{
				".acme.yaml":           "acme:\n  output: from-project\n", // one walk-up level above the cwd
				"xdg/acme/config.yaml": "acme:\n  output: from-user\n",
			},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-project" {
					t.Errorf("output = %q, want from-project — project-local wins over user-global", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-04", args: []string{"--config", "../broken.yaml", "deploy"},
			files: map[string]string{"broken.yaml": ":: definitely [ not yaml\n  - ::\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				var in acDeployInputs
				err := NewBinder(meta).Bind(rtx, &in)
				if err == nil {
					t.Fatal("malformed config bound silently, want a parse error")
				}
				if !strings.Contains(err.Error(), "broken.yaml") {
					t.Errorf("err = %v, want the offending file named", err)
				}
			}},
		{id: "CFG-05", args: []string{"deploy"},
			files: map[string]string{
				"acme.yaml":  "acme:\n  output: from-main\n",
				".acme.yaml": "acme:\n  output: from-project\n",
			},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// Two files declare the same key: declared order is precedence
				// within the file layer — main is declared first and wins.
				if in := bindAs[acDeployInputs](t, rtx, meta); in.Deploy.Flags.Output != "from-main" {
					t.Errorf("output = %q, want from-main (declared order is precedence)", in.Deploy.Flags.Output)
				}
			}},
		{id: "CFG-06", args: []string{"deploy"},
			files: map[string]string{"acme.yaml": "acme:\n  output: secret-perms\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
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
				if err := NewBinder(meta).Bind(rtx, &in); err == nil {
					t.Error("unreadable config bound silently, want a loud permission error")
				}
			}},

		{id: "CFG-07", args: []string{"deploy"},
			files: map[string]string{"acme.yaml": "acme:\n  output: json\n"}, // no acme.env — violates the schema below
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				// A configuration_files entry's schema: gates the loaded
				// document at bind time, before any value is read.
				meta.ConfigFiles[0].Schema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["acme"],"properties":{"acme":{"type":"object","required":["env"]}}}`
				var in acDeployInputs
				err := NewBinder(meta).Bind(rtx, &in)
				if err == nil || !strings.Contains(err.Error(), "acme.yaml") {
					t.Errorf("Bind = %v, want a schema violation naming the file", err)
				}
				// The same schema passes once the file conforms.
				if err := os.WriteFile(filepath.Join("..", "acme.yaml"), []byte("acme:\n  env: prod\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), rtx.Args), meta); in.Deploy.Flags.Env != "prod" {
					t.Errorf("env = %q, want prod from the now-conforming file", in.Deploy.Flags.Env)
				}
			}},

		// ── SEC — secret-safe input paths ──
		{id: "SEC-01", args: []string{"login", "--token", "@token.txt"},
			files: map[string]string{"work/token.txt": "sk_live_from_file\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				if in := bindAs[acLoginInputs](t, rtx, meta); in.Login.Flags.Token != "sk_live_from_file" {
					t.Errorf("token = %q, want the trimmed file contents (argv carries only the @path)", in.Login.Flags.Token)
				}
			}},
		{id: "SEC-02",
			skip: "DECIDED out of scope (W5-F6, option a): rotini ships no interactive prompt — " +
				"the secret: schema docs point handlers at rtx.Stdin + any prompt library.",
			check: func(t *testing.T, rtx *Context, meta BindMeta) {}},
		{id: "SEC-03", args: []string{"login"}, env: map[string]string{"ACME_TOKEN": "sk_live_leakme"},
			check: func(t *testing.T, rtx *Context, _ BindMeta) {
				// The redaction sweep: the secret's text must appear in NO
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
				layer, perr := ParseEnv[acLoginInputs](NewBinder(BindMeta{}), rtx)
				if perr != nil {
					t.Fatalf("ParseEnv: %v", perr)
				}
				for path, prov := range layer.Set {
					if strings.Contains(prov.Raw, "sk_live_leakme") {
						t.Errorf("secret leaked in provenance %s: %q", path, prov.Raw)
					}
				}
			}},

		// ── PREC — defaults & precedence (the overlay) ──
		{id: "PREC-01", args: []string{"deploy"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Flags.Output != "table" || in.Deploy.Flags.Env != "dev" {
					t.Errorf("defaults: output=%q env=%q, want table/dev with no input at all", in.Deploy.Flags.Output, in.Deploy.Flags.Env)
				}
			}},
		{id: "PREC-02", args: []string{"deploy", "--env=prod"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  env: test\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
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
				if in := bindAs[acDeployInputs](t, NewContextFor(acmeDef(), []string{"deploy"}), BindMeta{}); in.Deploy.Flags.Env != "dev" {
					t.Errorf("env = %q, want the default dev", in.Deploy.Flags.Env)
				}
			}},
		{id: "PREC-03", args: []string{"deploy"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  output: json\n"},
			check: func(t *testing.T, rtx *Context, meta BindMeta) {
				in := bindAs[acDeployInputs](t, rtx, meta)
				if in.Deploy.Flags.Env != "staging" || in.Deploy.Flags.Output != "json" {
					t.Errorf("env=%q output=%q, want staging/json — unset layers are skipped, not zeroed", in.Deploy.Flags.Env, in.Deploy.Flags.Output)
				}
			}},
		{id: "PREC-04", args: []string{"deploy", "--env=prod"},
			env:   map[string]string{"ACME_ENV": "staging"},
			files: map[string]string{"acme.yaml": "acme:\n  env: test\n"},
			check: func(t *testing.T, rtx *Context, _ BindMeta) {
				// Provenance: the Report knows WHICH layer won, and the full
				// history beneath it.
				b := NewBinder(acmeMeta(filepath.Dir(mustGetwd(t))))
				defaults, _ := Defaults[acDeployInputs](rtx)
				files, _ := ParseFiles[acDeployInputs](b, rtx)
				env, _ := ParseEnv[acDeployInputs](b, rtx)
				argv, _ := ParseArgv[acDeployInputs](b, rtx)
				_, rep := OverlayInputsP(defaults, files, env, argv)
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
		"ARG-01", "ARG-02", "ARG-03", "ARG-04", "ARG-05", "ARG-06", "ARG-07", "ARG-08",
		"FLAG-01", "FLAG-02", "FLAG-03", "FLAG-04", "FLAG-05", "FLAG-06", "FLAG-07", "FLAG-08",
		"FLAG-09", "FLAG-10", "FLAG-11",
		"STDIN-01", "STDIN-02", "STDIN-03", "STDIN-04", "STDIN-05", "STDIN-06", "STDIN-07",
		"ENV-01", "ENV-02", "ENV-03", "ENV-04", "ENV-05",
		"CFG-01", "CFG-02", "CFG-03", "CFG-04", "CFG-05", "CFG-06", "CFG-07",
		"SEC-01", "SEC-02", "SEC-03",
		"PREC-01", "PREC-02", "PREC-03", "PREC-04",
	}
	seen := map[string]int{}
	for _, c := range conformanceCases() {
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
