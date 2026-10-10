package codegen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/yaml"
)

// testDescription is an importer's description of a small program, covering the builtins,
// hooks, notes, examples and the value shapes the spec writer handles.
const testDescription = `{
  "format": "rotini-import/1",
  "source": {"framework": "cobra", "version": "v1.10.2"},
  "command": {
    "name": "acme",
    "summary": "Acme ops tool",
    "description": "Acme ops tool.\n\nIt deploys: services, # and more.",
    "examples": ["acme deploy api", "# a comment", "other run", "acme deploy nope", "$ acme deploy web --json | jq ."],
    "flags": [
      {"name": "help", "summary": "help for acme", "identifiers": ["-h", "--help"], "cascading": true, "short_circuit": true, "schema": {"type": "bool"}, "builtin": "help"},
      {"name": "output", "summary": "output: json|yaml", "identifiers": ["-o", "--output"], "cascading": true, "schema": {"type": "string", "default": "yes"}},
      {"name": "version", "summary": "version for acme", "identifiers": ["-v", "--version"], "short_circuit": true, "schema": {"type": "bool"}, "builtin": "version"}
    ],
    "commands": [
      {"name": "completion", "summary": "Generate the autocompletion script", "builtin": "completion",
       "arguments": [{"name": "shell", "schema": {"type": "string", "required": true, "enum": [{"value": "bash"}, {"value": "fish"}, {"value": "powershell"}, {"value": "zsh"}]}}],
       "flags": [{"name": "no-descriptions", "summary": "disable completion descriptions", "identifiers": ["--no-descriptions"], "schema": {"type": "bool"}}]},
      {"name": "deploy", "aliases": ["d"], "summary": "Deploy a service", "group": "Core Commands",
       "arguments": [{"name": "service", "schema": {"type": "string", "required": true, "enum": [{"value": "api", "summary": "the API"}, {"value": "web"}]}}],
       "flags": [
         {"name": "replicas", "summary": "count", "identifiers": ["--replicas"], "schema": {"type": "int", "default": 3}},
         {"name": "set", "summary": "values", "identifiers": ["--set"], "schema": {"type": "[]string", "default": ["a=1", "b"], "separator": ","}},
         {"name": "json", "summary": "JSON", "identifiers": ["--json"], "schema": {"type": "bool"}},
         {"name": "yaml", "summary": "YAML", "identifiers": ["--yaml"], "schema": {"type": "bool"}}
       ],
       "flag_groups": [{"kind": "mutually_exclusive", "flags": ["json", "yaml"]}]},
      {"name": "exec", "summary": "Run a command", "passthrough": true, "arguments": [{"name": "args", "schema": {"type": "[]string"}}]},
      {"name": "help", "summary": "Help about any command", "builtin": "help", "arguments": [{"name": "command", "schema": {"type": "[]string"}}]},
      {"name": "ssh", "summary": "Open a shell", "options_first": true, "arguments": [{"name": "host", "schema": {"type": "string", "required": true}}, {"name": "command", "schema": {"type": "[]string"}}]},
      {"name": "old", "summary": "Old", "deprecated": "use deploy", "hidden": true}
    ]
  },
  "notes": [
    {"path": "acme deploy", "level": "lossy", "msg": "--at is a time flag"},
    {"path": "acme deploy", "level": "info", "msg": "Args unset"},
    {"path": "acme", "level": "unsupported", "msg": "SuggestFor dropped"}
  ],
  "hooks": [
    {"path": "acme deploy", "framework": "RunE", "rotini": "Run", "file": "$ROOT/cmd/deploy.go", "line": 42},
    {"path": "acme", "framework": "PersistentPreRunE", "rotini": "CascadingPreRun", "file": "example.com/lib@v1.0.0/hook.go", "line": 7}
  ]
}`

// fakeImporter makes importRunner return testDescription, with $ROOT replaced by the module
// root, for the length of the test.
func fakeImporter(t *testing.T) {
	t.Helper()
	restore := importRunner
	t.Cleanup(func() { importRunner = restore })
	importRunner = func(_ context.Context, moduleRoot string, _ ImportOptions) (*importResult, []importNote, error) {
		res, err := readImportResult(strings.NewReader(strings.ReplaceAll(testDescription, "$ROOT", moduleRoot)))
		return res, []importNote{{Path: "acme", Level: "info", Msg: "the root is rootCmd"}}, err
	}
}

// TestImport pins the spec, conf and stubs an import writes from a description: the seed's
// builtins, comments above each command, examples that don't run the program or don't parse
// dropped, the completion feature switched on with a stub that prints the script, and the notes.
func TestImport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	t.Chdir(dir)
	fakeImporter(t)

	got, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Package: "./cmd"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec != filepath.Join("cmd", "acme", ".rotini.spec.yaml") || got.Conf != filepath.Join("cmd", "acme", ".rotini.conf.yaml") || got.Result == "" {
		t.Errorf("report = %+v", got)
	}
	if want := "imported 7 commands, 8 flags: 4 lossy, 1 unsupported, 2 info"; got.Summary != want {
		t.Errorf("summary = %q, want %q", got.Summary, want)
	}
	notes := strings.Join(got.Notes, "\n")
	for _, want := range []string{
		"[info] acme: the root is rootCmd",
		"[lossy] acme deploy: --at is a time flag",
		`[lossy] acme: example "# a comment" dropped: it doesn't run acme`,
		`[lossy] acme: example "other run" dropped: it doesn't run acme`,
		`[lossy] acme: example "acme deploy nope" dropped: it doesn't parse: `,
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes =\n%s\nwant a line %q", notes, want)
		}
	}

	spec := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.spec.yaml"))
	for _, want := range []string{
		"version: 1.4.0\n",
		"command:\n  # cobra PersistentPreRunE -> rotini CascadingPreRun: example.com/lib@v1.0.0/hook.go:7\n  # [unsupported] SuggestFor dropped\n  # [lossy] example \"# a comment\" dropped: it doesn't run acme\n",
		"    # cobra RunE -> rotini Run: cmd/deploy.go:42\n    # [lossy] --at is a time flag\n    - name: deploy\n",
		"  examples:\n    - acme deploy api\n    - acme deploy web --json | jq .\n  footer:",
		`  footer: Use "acme help <command>" for more information about a command.`,
		"      summary: print help\n      cascading: true\n      short_circuit: true\n",
		"    - name: version\n      identifiers:\n        - -v\n        - --version\n      summary: print version\n      short_circuit: true\n",
		"        default: \"yes\"\n",
		"    - name: help\n      summary: print help\n      description: Print help for a command.\n",
		"            complete:\n              kind: command\n",
		"      passthrough: true\n",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec =\n%s\nwant it to contain\n%s", spec, want)
		}
	}
	if strings.Contains(spec, "Args unset") {
		t.Error("an info note was written into the spec")
	}
	if strings.Count(spec, "options_first: true") != 1 {
		t.Errorf("spec =\n%s\nwant options_first on one command", spec)
	}
	if _, ssh, _ := strings.Cut(spec, "    - name: ssh\n"); !strings.Contains(strings.SplitN(ssh, "\n    - name:", 2)[0], "      options_first: true") {
		t.Errorf("spec =\n%s\nwant options_first on ssh", spec)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(spec), &doc); err != nil {
		t.Fatalf("the spec doesn't parse: %v", err)
	}
	cmd := doc["command"].(map[string]any)
	if cmd["description"] != "Acme ops tool.\n\nIt deploys: services, # and more." {
		t.Errorf("description = %q", cmd["description"])
	}

	conf := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.conf.yaml"))
	if !strings.Contains(conf, "    - type: completion\n      enabled: true\n") {
		t.Errorf("conf =\n%s\nwant the completion feature on", conf)
	}
	stub := readTestFile(t, filepath.Join(dir, "internal", "cmd", "acme", "acme_completion.go"))
	if !strings.Contains(stub, "script, err := Completion(inputs.AcmeCompletion.Arguments.Shell)") {
		t.Errorf("completion stub =\n%s\nwant it to print the shell's script", stub)
	}
	for _, f := range []string{"acme_deploy.go", "acme_exec.go", "acme_help.go", "zz_rotini.go"} {
		if _, err := os.Stat(filepath.Join(dir, "internal", "cmd", "acme", f)); err != nil {
			t.Errorf("not written: %v", err)
		}
	}

	// A second import refuses to write over the first.
	if _, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Package: "./cmd"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second import: %v, want a refusal", err)
	}
}

// TestImportFormatsAndDryRun pins that a non-YAML spec carries no comments, that --name and
// --dir rename the CLI and its directory, and that a dry run writes nothing.
func TestImportFormatsAndDryRun(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	t.Chdir(dir)
	fakeImporter(t)
	p := NewProcessor("1.4.0")

	planned, err := p.ImportDryRun(context.Background(), ImportOptions{Package: ".", Name: "ops", Dir: "ops-rotini"})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Changes) == 0 || !strings.Contains(strings.Join(planned.Changes, "\n"), "cmd/ops-rotini/.rotini.spec.yaml") {
		t.Errorf("changes = %q", planned.Changes)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd")); !os.IsNotExist(err) {
		t.Error("a dry run wrote files")
	}

	if _, err := p.Import(context.Background(), ImportOptions{Package: ".", Name: "ops", Format: "json"}); err != nil {
		t.Fatal(err)
	}
	spec := readTestFile(t, filepath.Join(dir, "cmd", "ops", ".rotini.spec.json"))
	if strings.Contains(spec, "cobra RunE") || !strings.Contains(spec, `"name": "ops"`) || !strings.Contains(spec, `"ops deploy api"`) {
		t.Errorf("json spec =\n%s", spec)
	}
}

// TestImportTargets pins the refusals that keep an import from writing beside the program it
// reads, and --force.
func TestImportTargets(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	writeTestFile(t, dir, "internal/cmd/acme/root.go", "package acme\n")
	t.Chdir(dir)
	fakeImporter(t)
	p := NewProcessor("1.4.0")
	_, err := p.Import(context.Background(), ImportOptions{Package: "./internal/cmd/acme"})
	if err == nil || !strings.Contains(err.Error(), "--dir acme-rotini") {
		t.Fatalf("err = %v, want a refusal suggesting --dir", err)
	}

	writeTestFile(t, dir, "cmd/acme2/main.go", "package main\n")
	if _, err := p.Import(context.Background(), ImportOptions{Package: ".", Dir: "acme2"}); err == nil || !strings.Contains(err.Error(), "main.go already exists") {
		t.Errorf("err = %v, want a refusal for the program's main.go", err)
	}
	if _, err := p.Import(context.Background(), ImportOptions{Package: ".", Dir: "acme2", Force: true}); err != nil {
		t.Errorf("--force: %v", err)
	}
	for _, opts := range []ImportOptions{{}, {Package: ".", Name: "9x"}, {Package: ".", Dir: "a/b"}, {Package: ".", Format: "xml"}} {
		if _, err := p.Import(context.Background(), opts); err == nil {
			t.Errorf("Import(%+v) succeeded, want an error", opts)
		}
	}
}

// TestReadImportResult pins that a description in another major version, or with no command,
// is refused.
func TestReadImportResult(t *testing.T) {
	for _, in := range []string{
		`{"format": "rotini-import/2", "command": {"name": "a"}}`,
		`{"format": "other/1", "command": {"name": "a"}}`,
		`{"format": "rotini-import/1"}`,
		`{`,
	} {
		if _, err := readImportResult(strings.NewReader(in)); err == nil {
			t.Errorf("readImportResult(%s) succeeded", in)
		}
	}
	if _, err := readImportResult(strings.NewReader(`{"format": "rotini-import/1.3", "command": {"name": "a"}}`)); err != nil {
		t.Errorf("a minor version was refused: %v", err)
	}
}

// TestYAMLString pins that every rendered scalar reads back as the string it came from.
func TestYAMLString(t *testing.T) {
	for _, s := range []string{
		"plain", "yes", "true", "3", "1.5", "null", "~", "", " lead", "trail ", "a: b", "a #b", "#x", "-x", "- x",
		"[x]", "{x}", "*x", "&x", "!x", "%x", "@x", "`x`", `"q"`, "'q'", "a\nb", "a\n\nb", "a\n b", "a\n", "\tx",
		"x\ty", "multi\nline: yes\n# no", "ünï", ",", "?", "|", ">",
	} {
		var got map[string]any
		text := "k: " + yamlString(s, 0) + "\n"
		if err := yaml.Unmarshal([]byte(text), &got); err != nil {
			t.Errorf("yamlString(%q) = %q: %v", s, text, err)
			continue
		}
		if str, _ := got["k"].(string); str != s {
			t.Errorf("yamlString(%q) = %q reads back as %q", s, text, got["k"])
		}
	}
}

// TestFindImportRoot pins how the root expression is picked from a package's code.
func TestFindImportRoot(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
		err   string
	}{
		{"root variable preferred", map[string]string{
			"a.go": "package cmd\nimport \"github.com/spf13/cobra\"\nvar serveCmd = &cobra.Command{}\n",
			"b.go": "package cmd\nimport \"github.com/spf13/cobra\"\nvar rootCmd = &cobra.Command{}\n",
		}, "rootCmd", ""},
		{"any variable", map[string]string{
			"a.go": "package cmd\nimport c \"github.com/spf13/cobra\"\nvar serve, other *c.Command\n",
		}, "serve", ""},
		{"constructor", map[string]string{
			"a.go":      "package demo\nimport \"github.com/spf13/cobra\"\nfunc helper(x int) *cobra.Command { return nil }\nfunc Root() *cobra.Command { return nil }\n",
			"a_test.go": "package demo\nimport \"github.com/spf13/cobra\"\nvar testCmd = &cobra.Command{}\n",
		}, "Root()", ""},
		{"none", map[string]string{
			"a.go": "package main\nimport \"github.com/spf13/cobra\"\nfunc build(n int) *cobra.Command { return nil }\nfunc main() {}\n",
		}, "", "functions returning *cobra.Command: build"},
		{"no cobra", map[string]string{"a.go": "package main\nvar rootCmd = 1\n"}, "", "no root command found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				writeTestFile(t, dir, name, src)
			}
			got, _, err := findImportRoot(dir)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("findImportRoot = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestImportGoEnv pins that the import's go commands run outside any workspace and without a
// -mod or -modfile from GOFLAGS.
func TestImportGoEnv(t *testing.T) {
	t.Setenv("GOWORK", "/somewhere/go.work")
	t.Setenv("GOFLAGS", "-mod=vendor -tags=x -modfile=a.mod -trimpath")
	env := strings.Join(importGoEnv(), "\n")
	if !strings.Contains(env, "\nGOWORK=off") || strings.Contains(env, "/somewhere") {
		t.Errorf("GOWORK not turned off:\n%s", env)
	}
	if !strings.Contains(env, "\nGOFLAGS=-tags=x -trimpath") {
		t.Errorf("GOFLAGS not cleaned:\n%s", env)
	}
}

// TestExplainTestFailure pins the causes an import failure is recognized by.
func TestExplainTestFailure(t *testing.T) {
	ctx := context.Background()
	fail := errors.New("exit status 1")
	cases := map[string]string{
		"flag provided but not defined: -test.paniconexit0\nUsage of x":                    "flag.Parse in an init function",
		"--- FAIL\n    x_test.go:9: cobra import: walking the command tree panicked: boom": "cobra import: walking the command tree panicked: boom",
		"panic: test timed out after 2m0s":                                                 "the import timed out",
		"some compile error":                                                               "the import failed: exit status 1\nsome compile error",
	}
	for output, want := range cases {
		if err := explainTestFailure(ctx, fail, output); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("explainTestFailure(%q) = %v, want it to contain %q", output, err, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := explainTestFailure(canceled, fail, ""); err == nil || !strings.Contains(err.Error(), "import stopped") {
		t.Errorf("canceled: %v", err)
	}
}

// TestCreatedLines pins that generate reports each file it created the author owns, and
// nothing on a run that creates none.
func TestCreatedLines(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - name: ship\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	p := NewProcessor("0.0.0")
	var results []string
	onGenerate := func(result string, err error) {
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
	}
	for range 2 {
		if err := p.Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, onGenerate, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(results[0], "created: internal/cmd/demo/demo_ship.go\n") || !strings.HasPrefix(results[0], "created: ") {
		t.Errorf("first generate = %q, want created: lines", results[0])
	}
	if strings.Contains(results[1], "created:") {
		t.Errorf("second generate = %q, want no created: lines", results[1])
	}
}
