package codegen

import (
	"context"
	"encoding/json"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
		quoted, _ := json.Marshal(moduleRoot) // a Windows root's backslashes need escaping
		res, err := readImportResult(strings.NewReader(strings.ReplaceAll(testDescription, "$ROOT", string(quoted[1:len(quoted)-1]))))
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
	// The summary counts the notes as reported: two examples dropped for one reason are one note.
	if want := "imported 7 commands, 8 flags: 3 lossy, 1 unsupported, 2 info"; got.Summary != want {
		t.Errorf("summary = %q, want %q", got.Summary, want)
	}
	notes := strings.Join(got.Notes, "\n")
	for _, want := range []string{
		"[info] acme: the root is rootCmd",
		"[lossy] acme deploy: --at is a time flag",
		`[lossy] acme: 2 examples dropped, as they don't run acme: "# a comment", "other run"`,
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
	if !strings.Contains(stub, "script, err := rtx.CompletionScript(inputs.AcmeCompletion.Arguments.Shell)") {
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

// TestImportStatsPlural pins the summary's nouns to their counts.
func TestImportStatsPlural(t *testing.T) {
	one := &importResult{Command: &importCommand{Name: "a", Flags: []*importFlag{{}}}}
	if got, want := one.stats(nil), "imported 1 command, 1 flag: 0 lossy, 0 unsupported, 0 info"; got != want {
		t.Errorf("stats = %q, want %q", got, want)
	}
	two := &importResult{Command: &importCommand{Name: "a", Commands: []*importCommand{{Name: "b"}}}}
	if got, want := two.stats(nil), "imported 2 commands, 0 flags: 0 lossy, 0 unsupported, 0 info"; got != want {
		t.Errorf("stats = %q, want %q", got, want)
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
		if err := explainTestFailure(ctx, "cobra", fail, output); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("explainTestFailure(%q) = %v, want it to contain %q", output, err, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := explainTestFailure(canceled, "cobra", fail, ""); err == nil || !strings.Contains(err.Error(), "import stopped") {
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

// TestExplainBuild pins the note a failed package listing gets when the environment names a
// go.work, which the run turns off: a package that builds only through the workspace fails.
func TestExplainBuild(t *testing.T) {
	fail := errors.New("go list: exit status 1\nno required module provides package example.com/lib")
	if err := (&importRun{}).explainBuild(fail); err == nil || err.Error() != fail.Error() {
		t.Errorf("outside a workspace: %v, want the error unchanged", err)
	}
	err := (&importRun{workspace: true}).explainBuild(fail)
	if !errors.Is(err, fail) || !strings.Contains(err.Error(), "rotini import runs with GOWORK=off") ||
		!strings.Contains(err.Error(), "import from a package that builds from its own module") {
		t.Errorf("in a workspace: %v", err)
	}
}

// TestImportRefusesGoNameClashes pins that a program whose names make one Go name is refused
// before anything is written.
func TestImportRefusesGoNameClashes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	t.Chdir(dir)
	restore := importRunner
	t.Cleanup(func() { importRunner = restore })
	importRunner = func(context.Context, string, ImportOptions) (*importResult, []importNote, error) {
		res, err := readImportResult(strings.NewReader(`{"format": "rotini-import/1", "command": {"name": "acme",
			"flags": [{"name": "x-y", "identifiers": ["--x-y"], "schema": {"type": "string"}}, {"name": "x_y", "identifiers": ["--x_y"], "schema": {"type": "string"}}],
			"commands": [{"name": "sub-cmd"}, {"name": "sub_cmd"}]}}`))
		return res, nil, err
	}
	_, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Package: "."})
	if err == nil || !strings.Contains(err.Error(), "can't be imported") || !strings.Contains(err.Error(), `flag "x_y"`) || !strings.Contains(err.Error(), "AcmeSubCmd") {
		t.Fatalf("err = %v, want the clashes refused", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd")); !os.IsNotExist(err) {
		t.Error("the refused import wrote files")
	}
}

// TestYAMLString_quotesIndicators pins that a value starting with a YAML indicator is quoted,
// since other YAML parsers reject it plain.
func TestYAMLString_quotesIndicators(t *testing.T) {
	for _, s := range []string{",", ",x", "@x", "`x", "%x", "[x", "{x", "#x", "&x", "*x", "!x", "|x", ">x", `"x`, "'x"} {
		if got := yamlString(s, 0); got == s {
			t.Errorf("yamlString(%q) left it plain", s)
		}
	}
	for _, s := range []string{"x,y", "a@b", "-x", "plain"} {
		if got := yamlString(s, 0); got != s {
			t.Errorf("yamlString(%q) = %q, want it plain", s, got)
		}
	}
}

// TestYAMLString_quotesYAML11Typed pins that strings YAML 1.1 readers take as numbers or
// timestamps are quoted, while look-alikes they read as strings stay plain.
func TestYAMLString_quotesYAML11Typed(t *testing.T) {
	for _, s := range []string{"1:20", "-1:20:30", "190:20:30.15", "2001-12-14", "2001-12-14t21:59:43.10-05:00",
		"2001-12-14 21:59:43.10 -5", "1_000", "0b101", "0x_ff", "+12", "017", ".inf", "-.Inf", ".NaN", "1_0.5"} {
		if got := yamlString(s, 0); got == s {
			t.Errorf("yamlString(%q) left it plain", s)
		}
	}
	for _, s := range []string{"12:30pm", "1.2.3", "v1:20", "2001-12-14x", "a:b", "1:2:3:x", "host:8080"} {
		if got := yamlString(s, 0); got != s {
			t.Errorf("yamlString(%q) = %q, want it plain", s, got)
		}
	}
}

// TestImportTestArgs_vetOff pins that the import's go test runs without vet, which before Go
// 1.23 can't see the overlaid test file and fails the import.
func TestImportTestArgs_vetOff(t *testing.T) {
	r := &importRun{modfile: "go.mod", opts: ImportOptions{Package: "./cmd", Tags: "x"}}
	args := r.testArgs("overlay.json", "TestRotiniImport_1", time.Minute)
	if !slices.Contains(args, "-vet=off") || args[len(args)-1] != "./cmd" || !slices.Contains(args, "-overlay") {
		t.Errorf("go test args = %q, want -vet=off with the overlay, ending in the package", args)
	}
}

// TestRootImports pins that a --root expression can use the package's own imports: the import
// test repeats the ones it names, under the names the package uses, and none it doesn't name
// or that the package's own declarations shadow.
func TestRootImports(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "package cmd\n\nimport (\n\t\"github.com/cli/cli/v2/pkg/cmd/factory\"\n\tghroot \"github.com/cli/cli/v2/pkg/cmd/root\"\n\t\"os\"\n)\n\nvar config = 1\n")
	writeTestFile(t, dir, "b.go", "package cmd\n\nimport yaml \"gopkg.in/yaml.v3\"\n")
	writeTestFile(t, dir, "c_test.go", "package cmd\n\nimport \"net/http\"\n")
	got, _ := rootImports(dir, `ghroot.NewCmdRoot(factory.New("x"), config.V, http.X, yaml.Y, os.Args)`, nil, nil)
	want := "\n\tfactory \"github.com/cli/cli/v2/pkg/cmd/factory\"\n\tghroot \"github.com/cli/cli/v2/pkg/cmd/root\"\n\tos \"os\"\n\tyaml \"gopkg.in/yaml.v3\""
	if got != want {
		t.Errorf("rootImports = %q, want %q", got, want)
	}
	for path, want := range map[string]string{"gopkg.in/yaml.v3": "yaml", "github.com/a/b/v2": "b", "github.com/mattn/go-isatty": "isatty", "os": "os"} {
		if got := defaultImportName(path); got != want {
			t.Errorf("defaultImportName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestRootImports_ambiguousConstrainedRenamed pins that a name the package's files import
// from different paths is left out and reported, that a file the build excludes doesn't
// count, and that a package whose name isn't the one its path suggests is found by its name.
func TestRootImports_ambiguousConstrainedRenamed(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "package cmd\n\nimport (\n\t\"log\"\n\t\"example.com/cfg\"\n)\n")
	writeTestFile(t, dir, "b.go", "package cmd\n\nimport log \"example.com/mylog\"\n")
	writeTestFile(t, dir, "c_other.go", "//go:build ignore\n\npackage cmd\n\nimport cfg \"example.com/othercfg\"\n")
	writeTestFile(t, dir, "d.go", "package cmd\n\nimport \"example.com/go-weird\"\n")
	match := build.Default.MatchFile
	names := func(paths []string) map[string]string {
		out := map[string]string{}
		for _, p := range paths {
			if p == "example.com/go-weird" {
				out[p] = "strange"
			}
		}
		return out
	}
	got, ambiguous := rootImports(dir, `cfg.Root(log.X, strange.Y)`, match, names)
	if want := "\n\tcfg \"example.com/cfg\"\n\tstrange \"example.com/go-weird\""; got != want {
		t.Errorf("rootImports = %q, want %q", got, want)
	}
	if want := []string{"log (example.com/mylog, log)"}; !slices.Equal(ambiguous, want) {
		t.Errorf("ambiguous = %q, want %q", ambiguous, want)
	}
}

// TestImport_vendorModWritesNothing pins that a repository keeping its module in vendor.mod
// imports with the module read from vendor.mod, and that no go.mod or go.sum is ever put into
// the program's tree, in a dry run or a real one.
func TestImport_vendorModWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "vendor.mod", "module example.com/acme\n\ngo 1.26\n")
	writeTestFile(t, dir, "vendor.sum", "")
	t.Chdir(dir)
	fakeImporter(t)
	fake := importRunner
	importRunner = func(ctx context.Context, moduleRoot string, opts ImportOptions) (*importResult, []importNote, error) {
		for _, f := range []string{"go.mod", "go.sum"} {
			if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
				t.Errorf("%s exists in the program's tree during the import: %v", f, err)
			}
		}
		return fake(ctx, moduleRoot, opts)
	}
	p := NewProcessor("1.4.0")
	planned, err := p.ImportDryRun(context.Background(), ImportOptions{Package: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(planned.Notes, "\n"), "vendor.mod") {
		t.Errorf("notes = %q, want one about vendor.mod", planned.Notes)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the dry run wrote into the module: %v", entries)
	}
}

// TestRunImporter_vendorMod pins that the go commands an import runs find a vendor.mod
// module's root through an overlaid go.mod, without writing one: the run gets as far as the
// importer's missing package, past go list and the module edit.
func TestRunImporter_vendorMod(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go")
	}
	importer := t.TempDir()
	writeTestFile(t, importer, "go.mod", "module github.com/go-rotini/import\n\ngo 1.22\n")
	writeTestFile(t, importer, "imp.go", "package imp\n")
	dir := t.TempDir()
	writeTestFile(t, dir, "vendor.mod", "module example.com/shell\n\ngo 1.22\n")
	writeTestFile(t, dir, "main.go", "package main\n\nvar CLI struct{}\n\nfunc main() {}\n")
	t.Chdir(dir)
	_, _, err := runImporter(context.Background(), dir, ImportOptions{Framework: FrameworkKong, Package: ".", Root: "&CLI", ImporterVersion: importer})
	if err == nil || !strings.Contains(err.Error(), "has no kong package") {
		t.Errorf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the run wrote into the module: %v", entries)
	}
}

// TestImport_valueShapes pins how the value shapes an importer describes are written, on a
// program with no sub-commands: a map default as a mapping with sorted keys, an empty list
// default left out, a negated identifier, repeatable: false, several environment variables, a
// file completion with extensions, an implicit value, an integer enum, and a bounded variadic
// argument. The spec must validate and generate, and a second import over it must write it
// byte for byte.
func TestImport_valueShapes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	t.Chdir(dir)
	restore := importRunner
	t.Cleanup(func() { importRunner = restore })
	importRunner = func(context.Context, string, ImportOptions) (*importResult, []importNote, error) {
		res, err := readImportResult(strings.NewReader(`{"format": "rotini-import/1", "source": {"framework": "kong"}, "command": {"name": "acme", "summary": "Acme",
			"flags": [
				{"name": "set", "identifiers": ["--set"], "schema": {"type": "map[string]string", "default": {"b": "2", "a": "1"}}},
				{"name": "tags", "identifiers": ["--tags"], "schema": {"type": "[]string", "default": []}},
				{"name": "color", "identifiers": ["--color"], "schema": {"type": "bool", "default": true, "negatable": "--plain"}},
				{"name": "once", "identifiers": ["--once"], "schema": {"type": "string", "repeatable": false}},
				{"name": "token", "identifiers": ["--token"], "schema": {"type": "string", "variable": ["ACME_TOKEN", "TOKEN"]}},
				{"name": "config", "identifiers": ["--config"], "schema": {"type": "string", "placeholder": "FILE", "complete": {"kind": "file", "extensions": ["yaml", "yml"]}}},
				{"name": "when", "identifiers": ["--when"], "schema": {"type": "string", "enum": [{"value": "auto"}, {"value": "always"}, {"value": "never"}], "default": "auto", "implicit_value": "always"}},
				{"name": "level", "identifiers": ["-l", "--level"], "schema": {"type": "int", "enum": [{"value": "1", "summary": "low"}, {"value": "2"}], "default": 1}}
			],
			"arguments": [{"name": "files", "schema": {"type": "[]string", "required": true, "minItems": 1, "maxItems": 3}}]}}`))
		return res, nil, err
	}
	p := NewProcessor("1.4.0")
	if _, err := p.Import(context.Background(), ImportOptions{Package: "."}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cmd", "acme", ".rotini.spec.yaml")
	spec := readTestFile(t, path)
	for _, want := range []string{
		"        default:\n          a: \"1\"\n          b: \"2\"\n",
		"        negatable: --plain\n",
		"        repeatable: false\n",
		"        variable:\n          - ACME_TOKEN\n          - TOKEN\n",
		"        complete:\n          kind: file\n          extensions:\n            - yaml\n            - yml\n        placeholder: FILE\n",
		"        implicit_value: always\n",
		"          - value: \"1\"\n            summary: low\n          - \"2\"\n",
		"        minItems: 1\n        maxItems: 3\n",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec =\n%s\nwant it to contain\n%s", spec, want)
		}
	}
	if strings.Contains(spec, "footer:") || strings.Contains(spec, "commands:") {
		t.Errorf("a program with no sub-commands got a help footer or commands:\n%s", spec)
	}
	if _, tags, _ := strings.Cut(spec, "- name: tags\n"); strings.Contains(strings.SplitN(tags, "- name:", 2)[0], "default") {
		t.Errorf("an empty list default was written:\n%s", spec)
	}
	if _, err := p.Import(context.Background(), ImportOptions{Package: ".", Force: true}); err != nil {
		t.Fatal(err)
	}
	if again := readTestFile(t, path); again != spec {
		t.Errorf("a second import wrote\n%s\nwant\n%s", again, spec)
	}
}

// TestImportForce pins that an existing spec or conf alone stops an import, and that --force
// replaces both; a dry run of an import that would write the same files plans no change.
func TestImportForce(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	writeTestFile(t, dir, "cmd/acme/.rotini.conf.yaml", "old conf\n")
	t.Chdir(dir)
	fakeImporter(t)
	p := NewProcessor("1.4.0")
	if _, err := p.Import(context.Background(), ImportOptions{Package: "."}); err == nil || !strings.Contains(err.Error(), "cmd/acme/.rotini.conf.yaml already exists (use --force to overwrite)") {
		t.Fatalf("err = %v, want the existing conf refused", err)
	}
	writeTestFile(t, dir, "cmd/acme/.rotini.spec.yaml", "old spec\n")
	if _, err := p.Import(context.Background(), ImportOptions{Package: ".", Force: true}); err != nil {
		t.Fatal(err)
	}
	if spec := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.spec.yaml")); !strings.Contains(spec, "name: acme") {
		t.Errorf("spec =\n%s\nwant it replaced", spec)
	}
	if conf := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.conf.yaml")); !strings.Contains(conf, "generate:") {
		t.Errorf("conf =\n%s\nwant it replaced", conf)
	}
	planned, err := p.ImportDryRun(context.Background(), ImportOptions{Package: ".", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Changes) != 0 {
		t.Errorf("changes = %q, want none", planned.Changes)
	}
}

// TestImportTestArgs_timeoutAndTags pins that --timeout bounds the import's go test and that
// --tags reach it.
func TestImportTestArgs_timeoutAndTags(t *testing.T) {
	r := &importRun{modfile: "go.mod", opts: ImportOptions{Package: ".", Tags: "dev,e2e"}}
	args := strings.Join(r.testArgs("overlay.json", "TestRotiniImport_1", 30*time.Second), " ")
	if !strings.Contains(args, "-timeout 30s") || !strings.Contains(args, "-tags dev,e2e") {
		t.Errorf("go test args = %q", args)
	}
}
