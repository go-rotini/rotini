package codegen

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rootCase is a package's files and the root expression a finder should pick from them, or
// the error it should give.
type rootCase struct {
	name  string
	files map[string]string
	want  string
	err   string
}

// checkRoots runs find over each case's package.
func checkRoots(t *testing.T, find func(string) (string, string, error), cases []rootCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				writeTestFile(t, dir, name, src)
			}
			got, how, err := find(dir)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want || how == "" {
				t.Errorf("root = %q (%s), %v; want %q", got, how, err, tc.want)
			}
		})
	}
}

// TestFindKongRoot pins how the grammar expression is picked from a package's code.
func TestFindKongRoot(t *testing.T) {
	checkRoots(t, findKongRoot, []rootCase{
		{"anonymous struct variable", map[string]string{
			"main.go": "package main\nimport \"github.com/alecthomas/kong\"\nvar CLI struct {\n\tRm struct{} `cmd:\"\" help:\"Remove.\"`\n}\nfunc main() { kong.Parse(&CLI) }\n",
		}, "&CLI", ""},
		{"variable named cli preferred", map[string]string{
			"a.go": "package app\ntype Grammar struct {\n\tDebug bool `help:\"Debug.\"`\n}\nvar other Grammar\nvar cli = Grammar{}\n",
		}, "&cli", ""},
		{"pointer variable", map[string]string{
			"a.go": "package app\ntype Commands struct {\n\tRun struct{} `cmd:\"\"`\n}\nvar commands = &Commands{}\n",
		}, "commands", ""},
		{"grammar type", map[string]string{
			"a.go": "package demo\ntype DeployCmd struct {\n\tService string `arg:\"\"`\n}\ntype CLI struct {\n\tDeploy DeployCmd `cmd:\"\"`\n}\n",
		}, "&CLI{}", ""},
		{"untagged struct variable", map[string]string{
			"a.go": "package demo\nvar config struct{ Port int }\ntype Root struct{ Globals }\ntype Globals struct{}\n",
		}, "&Root{}", ""},
		{"none", map[string]string{
			"a.go": "package demo\ntype Commands struct {\n\tRun struct{} `cmd:\"\"`\n}\n",
		}, "", "struct types with Kong tags: Commands"},
		{"test files ignored", map[string]string{
			"a.go":      "package demo\n",
			"a_test.go": "package demo\nvar CLI struct {\n\tRm struct{} `cmd:\"\"`\n}\n",
		}, "", "no Kong grammar found"},
	})
}

// TestFindURFaveRoot pins how the root command is picked from a package's code.
func TestFindURFaveRoot(t *testing.T) {
	checkRoots(t, findURFaveRoot, []rootCase{
		{"root variable", map[string]string{
			"a.go": "package cmd\nimport \"github.com/urfave/cli/v3\"\nvar deployCmd = &cli.Command{}\nvar app = &cli.Command{Commands: []*cli.Command{deployCmd}}\n",
		}, "app", ""},
		{"listed variables are not roots", map[string]string{
			"a.go": "package cmd\nimport ucli \"github.com/urfave/cli/v3\"\nvar deploy = &ucli.Command{}\nvar top *ucli.Command\nfunc init() { top = &ucli.Command{Commands: []*ucli.Command{deploy}} }\n",
		}, "top", ""},
		{"constructor", map[string]string{
			"main.go": "package main\nimport \"github.com/urfave/cli/v3\"\nfunc sub(n int) *cli.Command { return nil }\nfunc newApp() *cli.Command { return nil }\nfunc main() {}\n",
		}, "newApp()", ""},
		{"only unlisted function", map[string]string{
			"a.go": "package tasks\nimport \"github.com/urfave/cli/v3\"\nfunc addCmd() *cli.Command { return nil }\n" +
				"func tasksCommand() *cli.Command { return &cli.Command{Commands: []*cli.Command{addCmd()}} }\n",
		}, "tasksCommand()", ""},
		{"none", map[string]string{
			"main.go": "package main\nimport \"github.com/urfave/cli/v3\"\nfunc build(n int) *cli.Command { return nil }\nfunc main() {}\n",
		}, "", "functions returning *cli.Command: build"},
		{"another framework's command", map[string]string{
			"a.go": "package cmd\nimport \"github.com/spf13/cobra\"\nvar rootCmd = &cobra.Command{}\n",
		}, "", "no root command found"},
	})
}

// TestImportTestFile pins the test file an import adds for each framework: it parses, and
// calls the framework's importer with the root and the options.
func TestImportTestFile(t *testing.T) {
	cases := []struct {
		opts ImportOptions
		root string
		want string
	}{
		{ImportOptions{Strict: true}, "rootCmd",
			`rotiniimportcobra "github.com/go-rotini/import/cobra"` + "\n\t" + `rotiniimportos "os"` +
				"\x00" + `rotiniimportcobra.FromCobra((rootCmd).Root(), rotiniimportcobra.Options{Strict: true})`},
		{ImportOptions{Framework: FrameworkKong}, "&CLI",
			`rotiniimportkong "github.com/go-rotini/import/kong"` +
				"\x00" + `rotiniimportkong.FromKong(&CLI, rotiniimportkong.Options{Name: "shell"})`},
		{ImportOptions{Framework: FrameworkKong, Name: "acme", Vars: map[string]string{"stage": "prod", "a\"b": "x\ny"}}, "&CLI{}",
			`rotiniimportkong.FromKong(&CLI{}, rotiniimportkong.Options{Name: "acme", Vars: map[string]string{"a\"b": "x\ny", "stage": "prod"}})`},
		{ImportOptions{Framework: FrameworkURFave, Strict: true}, "newApp()",
			`rotiniimporturfave "github.com/go-rotini/import/urfave"` +
				"\x00" + `rotiniimporturfave.FromURFave(newApp(), rotiniimporturfave.Options{Name: "shell", Strict: true})`},
	}
	for _, tc := range cases {
		fw, err := frameworkFor(tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		src := fmt.Sprintf(importTestTemplate, "main", fw.pkg, "TestRotiniImport_x", fw.call(tc.root, tc.opts, "shell"), "")
		if _, err := parser.ParseFile(token.NewFileSet(), "x_test.go", src, 0); err != nil {
			t.Errorf("%s test file doesn't parse: %v\n%s", fw.pkg, err, src)
		}
		for want := range strings.SplitSeq(tc.want, "\x00") {
			if !strings.Contains(src, want) {
				t.Errorf("%s test file =\n%s\nwant it to contain %s", fw.pkg, src, want)
			}
		}
	}
	if _, err := frameworkFor(ImportOptions{Framework: "clap"}); err == nil || !strings.Contains(err.Error(), "unknown framework") {
		t.Errorf("unknown framework: %v", err)
	}
}

// TestBinaryName pins the default CLI name: the last element of the import path, without a
// major version suffix.
func TestBinaryName(t *testing.T) {
	for path, want := range map[string]string{
		"example.com/shell":       "shell",
		"example.com/acme/cmd/v2": "cmd",
		"example.com/acme/v2":     "acme",
		"tool":                    "tool",
		"example.com/acme/vx":     "vx",
		"example.com/v":           "v",
	} {
		if got := binaryName(path); got != want {
			t.Errorf("binaryName(%q) = %q, want %q", path, got, want)
		}
	}
}

// testDescriptionNewFields is a urfave/cli-shaped description using the description's
// optional fields: a flag's help group, environment variables, a negatable bool in both
// forms, a flag that can't repeat and a passthrough argument; plus a help command alias.
const testDescriptionNewFields = `{
  "format": "rotini-import/1",
  "source": {"framework": "urfave", "version": "v3.14.0"},
  "command": {
    "name": "acme",
    "summary": "Acme ops tool",
    "flags": [
      {"name": "help", "summary": "show help", "identifiers": ["-h", "--help"], "cascading": true, "short_circuit": true, "schema": {"type": "bool"}, "builtin": "help"},
      {"name": "config", "summary": "config file", "identifiers": ["--config"], "group": "Settings", "schema": {"type": "string", "variable": ["ACME_CONFIG"]}},
      {"name": "token", "summary": "token", "identifiers": ["--token"], "group": "Settings", "schema": {"type": "string", "variable": ["ACME_TOKEN", "TOKEN"]}},
      {"name": "color", "summary": "color", "identifiers": ["--color"], "schema": {"type": "bool", "negatable": true}},
      {"name": "pager", "summary": "pager", "identifiers": ["--pager"], "schema": {"type": "bool", "negatable": "--no-paging"}},
      {"name": "region", "summary": "region", "identifiers": ["--region"], "schema": {"type": "string", "repeatable": false}}
    ],
    "commands": [
      {"name": "exec", "summary": "Run a command", "arguments": [
        {"name": "node", "schema": {"type": "string", "required": true}},
        {"name": "command", "passthrough": true, "schema": {"type": "[]string"}}
      ]},
      {"name": "help", "aliases": ["h"], "summary": "Shows help", "builtin": "help", "arguments": [{"name": "command", "schema": {"type": "[]string"}}]},
      {"name": "completion", "summary": "Output shell completion script", "hidden": true, "builtin": "completion",
       "arguments": [{"name": "shell", "schema": {"type": "string", "required": true, "enum": [{"value": "bash"}, {"value": "zsh"}, {"value": "fish"}, {"value": "powershell"}]}}]}
    ]
  }
}`

// importDescription makes importRunner return desc for the length of the test.
func importDescription(t *testing.T, desc string) {
	t.Helper()
	restore := importRunner
	t.Cleanup(func() { importRunner = restore })
	importRunner = func(context.Context, string, ImportOptions) (*importResult, []importNote, error) {
		res, err := readImportResult(strings.NewReader(desc))
		return res, nil, err
	}
}

// TestImportNewFields pins how the description's optional fields are written as spec keys,
// in a spec that validates and generates.
func TestImportNewFields(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n")
	t.Chdir(dir)
	importDescription(t, testDescriptionNewFields)

	if _, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Framework: FrameworkURFave, Package: "."}); err != nil {
		t.Fatal(err)
	}
	spec := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.spec.yaml"))
	for _, want := range []string{
		"    - name: config\n      identifiers:\n        - --config\n      summary: config file\n      group: Settings\n      schema:\n        type: string\n        variable: ACME_CONFIG\n",
		"        variable:\n          - ACME_TOKEN\n          - TOKEN\n",
		"        type: bool\n        negatable: true\n",
		"        negatable: --no-paging\n",
		"        repeatable: false\n",
		"        - name: command\n          passthrough: true\n",
		"    - name: help\n      aliases:\n        - h\n      summary: print help\n",
		`  footer: Use "acme help <command>" for more information about a command.`,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec =\n%s\nwant it to contain\n%s", spec, want)
		}
	}
	conf := readTestFile(t, filepath.Join(dir, "cmd", "acme", ".rotini.conf.yaml"))
	if !strings.Contains(conf, "    - type: completion\n      enabled: true\n") {
		t.Errorf("conf =\n%s\nwant the completion feature on", conf)
	}
	stub := readTestFile(t, filepath.Join(dir, "internal", "cmd", "acme", "acme_completion.go"))
	if !strings.Contains(stub, "Completion(inputs.AcmeCompletion.Arguments.Shell)") {
		t.Errorf("completion stub =\n%s\nwant it to print the shell's script", stub)
	}
}

// TestImportNoHelpCommand pins that a tree without a help command, as Kong builds, gets no
// footer pointing at one.
func TestImportNoHelpCommand(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/shell\n\ngo 1.26\n")
	t.Chdir(dir)
	importDescription(t, `{"format": "rotini-import/1", "source": {"framework": "kong"}, "command": {"name": "shell",
	  "flags": [{"name": "help", "summary": "Show context-sensitive help.", "identifiers": ["-h", "--help"], "cascading": true, "short_circuit": true, "schema": {"type": "bool"}, "builtin": "help"}],
	  "commands": [{"name": "rm", "summary": "Remove files."}]}}`)
	if _, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Framework: FrameworkKong, Package: "."}); err != nil {
		t.Fatal(err)
	}
	spec := readTestFile(t, filepath.Join(dir, "cmd", "shell", ".rotini.spec.yaml"))
	if strings.Contains(spec, "footer:") || strings.Contains(spec, "name: help\n      summary: print help\n      description") {
		t.Errorf("spec =\n%s\nwant no help command and no footer", spec)
	}
	if !strings.Contains(spec, "summary: print help") {
		t.Errorf("spec =\n%s\nwant the help flag", spec)
	}
}

// TestImportUnknownFramework pins that an unknown framework is refused before anything runs.
func TestImportUnknownFramework(t *testing.T) {
	_, err := NewProcessor("1.4.0").Import(context.Background(), ImportOptions{Framework: "clap", Package: "."})
	if err == nil || !strings.Contains(err.Error(), `unknown framework "clap"`) {
		t.Errorf("err = %v", err)
	}
}

// TestExplainImporterError pins that the importer's own error is shown for each framework.
func TestExplainImporterError(t *testing.T) {
	fail := errors.New("exit status 1")
	out := "--- FAIL\n    x_test.go:9: kong import: build the Kong model: <anonymous struct>.Rm: duplicate flag --force\nFAIL\n"
	err := explainTestFailure(context.Background(), "kong", fail, out)
	if err == nil || err.Error() != "kong import: build the Kong model: <anonymous struct>.Rm: duplicate flag --force" {
		t.Errorf("err = %v", err)
	}
}

// TestImporterWithoutPackage pins the message when the importer release has no package for
// the framework.
func TestImporterWithoutPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go")
	}
	importer := t.TempDir()
	writeTestFile(t, importer, "go.mod", "module github.com/go-rotini/import\n\ngo 1.22\n")
	writeTestFile(t, importer, "imp.go", "package imp\n")
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/shell\n\ngo 1.22\n")
	writeTestFile(t, dir, "main.go", "package main\n\nvar CLI struct{}\n\nfunc main() {}\n")
	t.Chdir(dir)
	_, _, err := runImporter(context.Background(), dir, ImportOptions{Framework: FrameworkKong, Package: ".", Root: "&CLI", ImporterVersion: importer})
	if err == nil || !strings.Contains(err.Error(), "has no kong package; pass an --importer-version that has it") {
		t.Errorf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("the run wrote into the module: %v", entries)
	}
}

// TestCommentsFor_unlocatedHookNamesFunc pins that a hook the importer couldn't locate is
// named by its function in the spec comment, instead of ending in an empty position. A located
// hook's file is a runtime file name, which has forward slashes on every system.
func TestCommentsFor_unlocatedHookNamesFunc(t *testing.T) {
	root := t.TempDir()
	sb := &importSpecBuilder{moduleRoot: root, res: &importResult{
		Source: importSource{Framework: "cobra"},
		Hooks: []importHook{
			{Path: "app", Framework: "RunE", Rotini: "Run", Func: "(*server).run"},
			{Path: "app", Framework: "PreRunE", Rotini: "PreRun", Func: "pre", File: filepath.ToSlash(filepath.Join(root, "cmd", "a.go")), Line: 3},
			{Path: "app", Framework: "PostRunE", Rotini: "PostRun"},
		},
	}}
	got := sb.commentsFor("app")
	want := []string{"cobra RunE -> rotini Run: (*server).run", "cobra PreRunE -> rotini PreRun: cmd/a.go:3", "cobra PostRunE -> rotini PostRun"}
	if !slices.Equal(got, want) {
		t.Errorf("commentsFor = %q, want %q", got, want)
	}
}

// TestFindRoot_reportsSyntaxError pins that when no root is found and a file of the package
// doesn't parse, each root finder names that file's syntax error.
func TestFindRoot_reportsSyntaxError(t *testing.T) {
	for name, find := range map[string]func(string) (string, string, error){
		"cobra": findImportRoot, "kong": findKongRoot, "urfave": findURFaveRoot,
	} {
		dir := t.TempDir()
		writeTestFile(t, dir, "a.go", "package main\n\nfunc main() {}\n")
		writeTestFile(t, dir, "main.go", "package main\n\nvar s = \"broken\n\"\n")
		_, _, err := find(dir)
		if err == nil || !strings.Contains(err.Error(), "main.go:3: ") || !strings.Contains(err.Error(), "fix it or pass --root") {
			t.Errorf("%s: err = %v, want main.go's syntax error", name, err)
		}
	}
}
