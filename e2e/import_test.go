//go:build !mutation

package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/rotini/internal/testmod"
)

// importCmd returns the `rotini import cobra` command line, ending with args.
func importCmd(t *testing.T, args ...string) []string {
	t.Helper()
	return frameworkImportCmd(t, "cobra", args...)
}

// frameworkImportCmd returns the `rotini import <framework>` command line, ending with args.
// The import runs the release of github.com/go-rotini/import that rotini pins, downloaded
// into a temporary go.mod, or the local checkout ROTINI_IMPORT_SHIM names. The programs it
// reads are copies of that repository's corpus, under testdata/import.
func frameworkImportCmd(t *testing.T, framework string, args ...string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test and go build in imported modules; skipped under -short")
	}
	cmd := []string{"import", framework}
	if shim := os.Getenv("ROTINI_IMPORT_SHIM"); shim != "" {
		if _, err := os.Stat(filepath.Join(shim, "go.mod")); err != nil {
			t.Fatalf("ROTINI_IMPORT_SHIM: %v", err)
		}
		cmd = append(cmd, "--importer-version", shim)
	}
	return append(cmd, args...)
}

// importCase is one corpus program to import and round-trip.
type importCase struct {
	framework, fixture, pkg, cli string
	args                         []string
	// completion is set when the program has a completion command, which the import keeps.
	completion bool
	// summary, when set, is text the import's summary line must contain.
	summary string
	// runs is a command line the built CLI must accept.
	runs []string
	// checks are runs of the built CLI that must keep the program's behaviour.
	checks []importCheck
}

// importCheck runs the built CLI with args and env set; it must succeed and print want, or,
// when fail is set, fail and print want.
type importCheck struct {
	args, env []string
	want      string
	fail      bool
}

// TestImportCobra imports each of the importer's own Cobra corpus programs, then validates,
// regenerates, builds and runs the imported CLI. The program's own files, go.mod and go.sum
// must be byte-identical after the import.
func TestImportCobra(t *testing.T) {
	importCmd(t)
	roundTrip(t, []importCase{
		// ssh stops parsing flags at its first argument, so a word after it that looks like a
		// flag is an argument.
		{framework: "cobra", fixture: "demo", pkg: ".", cli: "acme", completion: true, runs: []string{"ssh", "node1", "--not-a-flag", "-t"},
			checks: []importCheck{
				{args: []string{"rollback", "api", "--reason", "r", "--last"}, want: "acme rollback"},
				{args: []string{"deploy", "nope"}, want: "nope", fail: true},
				{args: []string{"deploy", "api", "--json", "--yaml"}, want: "--yaml", fail: true},
				{args: []string{"deploy", "api", "--cert", "c"}, want: "--key", fail: true},
				{args: []string{"rollback", "api", "--last"}, want: "--reason", fail: true},
				{args: []string{"rollback", "api", "--reason", "r"}, want: "--last", fail: true},
				{args: []string{"rollback", "api", "--reason", "r", "--last", "--to", "3"}, want: "--to", fail: true},
			}},
		{framework: "cobra", fixture: "cobracli", pkg: "./cmd", cli: "acmecli", args: []string{"--strict"}, completion: true},
	})
}

// TestImportKong imports the importer's Kong corpus programs and round-trips them, as
// TestImportCobra does.
func TestImportKong(t *testing.T) {
	frameworkImportCmd(t, "kong")
	roundTrip(t, []importCase{
		// ssh's command is a passthrough argument: the words after the node are taken as typed.
		{framework: "kong", fixture: "kong-demo", pkg: ".", cli: "acme", args: []string{"--name", "acme", "--var", "stage=staging"},
			runs: []string{"ssh", "node1", "uptime", "--not-a-flag"},
			checks: []importCheck{
				// env:"ACME_REPLICAS,REPLICAS" reads the first variable that is set.
				{args: []string{"deploy", "api"}, env: []string{"ACME_REPLICAS=5", "REPLICAS=6"}, want: "Replicas:5"},
				{args: []string{"deploy", "api"}, env: []string{"REPLICAS=6"}, want: "Replicas:6"},
				{args: []string{"deploy", "api", "-r", "7"}, env: []string{"ACME_REPLICAS=5"}, want: "Replicas:7"},
				{args: []string{"deploy", "api"}, env: []string{"ACME_CONTEXT=prod"}, want: "Context:prod"},
				{args: []string{"deploy", "nope"}, want: "nope", fail: true},
				{args: []string{"deploy", "api", "--json", "--yaml"}, want: "--yaml", fail: true},
				{args: []string{"rollback", "svc", "--reason", "r"}, want: "--last", fail: true},
			}},
		// No --name: the CLI is named after the module, as go build names its binary.
		{framework: "kong", fixture: "kong-readme", pkg: ".", cli: "shell", runs: []string{"rm", "--force", "a.txt"}},
	})
}

// TestImportURFave imports the importer's urfave/cli corpus programs and round-trips them, as
// TestImportCobra does.
func TestImportURFave(t *testing.T) {
	frameworkImportCmd(t, "urfave")
	roundTrip(t, []importCase{
		// exec takes every word after its name as typed.
		{framework: "urfave", fixture: "urfave-demo", pkg: ".", cli: "acme", completion: true, runs: []string{"exec", "ls", "--not-a-flag"},
			checks: []importCheck{
				// Sources: cli.EnvVars("ACME_REPLICAS", "REPLICAS") reads the first variable that is set.
				{args: []string{"deploy", "svc", "--reason", "r", "--last"}, env: []string{"ACME_REPLICAS=4", "REPLICAS=6"}, want: "Replicas:4"},
				{args: []string{"deploy", "svc", "--reason", "r", "--last"}, env: []string{"REPLICAS=6"}, want: "Replicas:6"},
				{args: []string{"deploy", "svc", "--last"}, want: "--reason", fail: true},
				{args: []string{"deploy", "svc", "--reason", "r"}, want: "--last", fail: true},
				{args: []string{"deploy", "svc", "--reason", "r", "--last", "--to", "x"}, want: "--to", fail: true},
				{args: []string{"deploy", "svc", "--reason", "r", "--last", "--json", "--yaml"}, want: "--yaml", fail: true},
			}},
		{framework: "urfave", fixture: "urfave-demo", pkg: ".", cli: "acme", args: []string{"--strict", "--dir", "acme-strict"}, completion: true},
		// The root has no name: the CLI is named after the module.
		{framework: "urfave", fixture: "urfave-readme", pkg: ".", cli: "tasks", runs: []string{"template", "add", "x"}},
	})
}

// roundTrip imports each case's program, checks that a dry run and the import leave the
// program's tree byte-identical, then validates, regenerates, builds and runs the imported CLI.
// When the pinned importer release has no package for the framework and no checkout is
// named, the case is skipped.
func roundTrip(t *testing.T, cases []importCase) {
	t.Helper()
	bin := rotiniBin(t)
	root := repoRoot(t)
	for _, tc := range cases {
		dir := tc.cli
		if i := slices.Index(tc.args, "--dir"); i >= 0 {
			dir = tc.args[i+1]
		}
		t.Run(tc.fixture+"/"+dir, func(t *testing.T) {
			name := tc.fixture
			if !strings.Contains(name, "/") {
				name = "corpus/" + name
			}
			fixture := importFixture(t, name)
			before := treeHash(t, fixture)
			run := runIn(t, fixture)

			// A dry run writes nothing.
			cmd := exec.Command(bin, frameworkImportCmd(t, tc.framework, append([]string{"--dry-run", tc.pkg}, tc.args...)...)...)
			cmd.Dir = fixture
			out, err := cmd.CombinedOutput()
			if missing := "has no " + tc.framework + " package"; err != nil && strings.Contains(string(out), missing) && os.Getenv("ROTINI_IMPORT_SHIM") == "" {
				t.Skipf("the pinned importer release %s; set ROTINI_IMPORT_SHIM to a checkout of github.com/go-rotini/import that has it", missing)
			}
			if err == nil || !strings.Contains(string(out), "dry run:") {
				t.Fatalf("import --dry-run: %v\n%s", err, out)
			}
			if treeHash(t, fixture) != before {
				t.Fatal("a dry run changed the module")
			}

			got := run(bin, frameworkImportCmd(t, tc.framework, append([]string{tc.pkg}, tc.args...)...)...)
			if !strings.Contains(got, "spec: cmd/"+dir+"/.rotini.spec.yaml") || !strings.Contains(got, "imported "+tc.summary) {
				t.Fatalf("import output:\n%s", got)
			}
			if after := treeHash(t, fixture); after != before {
				t.Fatal("the import changed the program's files, go.mod or go.sum")
			}

			spec := filepath.Join("cmd", dir, ".rotini.spec.yaml")
			conf := filepath.Join("cmd", dir, ".rotini.conf.yaml")
			run(bin, "validate", spec, "--config", conf)
			if got := run(bin, "generate", spec, "--config", conf); strings.Contains(got, "created:") {
				t.Errorf("a generate right after the import created files:\n%s", got)
			}

			// Importing again over the first import writes the same spec and conf, and leaves
			// the generated code and handlers as they are.
			first := treeOf(t, filepath.Join(fixture, "cmd", dir)) + treeOf(t, filepath.Join(fixture, "internal", "cmd", dir))
			run(bin, frameworkImportCmd(t, tc.framework, append([]string{tc.pkg, "--force"}, tc.args...)...)...)
			if again := treeOf(t, filepath.Join(fixture, "cmd", dir)) + treeOf(t, filepath.Join(fixture, "internal", "cmd", dir)); again != first {
				t.Error("a second import changed the imported CLI's files")
			}

			run("go", append([]string{"mod", "edit", "-require=github.com/go-rotini/rotini@v0.0.0",
				"-replace=github.com/go-rotini/rotini=" + strings.ReplaceAll(root, `\`, `/`)}, testmod.EditFlags(root)...)...)
			run("go", "mod", "tidy")
			exe := dir
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			run("go", "build", "-o", exe, "./cmd/"+dir)
			help := run(filepath.Join(fixture, exe), "--help")
			if !strings.Contains(help, "Usage:\n  "+tc.cli) {
				t.Errorf("%s --help:\n%s", tc.cli, help)
			}
			if tc.completion {
				if script := run(filepath.Join(fixture, exe), "completion", "bash"); !strings.Contains(script, "bash completion for "+tc.cli) {
					t.Errorf("%s completion bash printed no script:\n%.200s", tc.cli, script)
				}
			}
			if tc.runs != nil {
				run(filepath.Join(fixture, exe), tc.runs...)
			}
			for _, c := range tc.checks {
				cmd := exec.Command(filepath.Join(fixture, exe), c.args...)
				cmd.Dir = fixture
				cmd.Env = append(os.Environ(), c.env...)
				out, err := cmd.CombinedOutput()
				if (err != nil) != c.fail || !strings.Contains(string(out), c.want) {
					t.Errorf("%s %s %s: %v\n%s\nwant fail=%t and output containing %q", strings.Join(c.env, " "), tc.cli, strings.Join(c.args, " "), err, out, c.fail, c.want)
				}
			}
		})
	}
}

// TestImportCobraFailures pins the explained failures: flag.Parse in an init function, and a
// package with no root to find.
func TestImportCobraFailures(t *testing.T) {
	bin := rotiniBin(t)

	dir := importFixture(t, "corpus/flagparse")
	cmd := exec.Command(bin, importCmd(t, ".")...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "flag.Parse in an init function") {
		t.Errorf("flag.Parse in init: %v\n%s", err, out)
	}

	dir = importFixture(t, "corpus/cobracli")
	cmd = exec.Command(bin, importCmd(t, ".")...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "point at the package that declares rootCmd") {
		t.Errorf("no root: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "acmecli")); !os.IsNotExist(err) {
		t.Error("a failed import wrote files")
	}
}

// TestImportCobraNetwork imports the importer's pinned large real programs, Delve and kubectl,
// then validates, regenerates and builds the imported CLI and prints its help. Their modules
// are downloaded, so it also needs ROTINI_IMPORT_NETWORK=1. Each step's time is logged.
func TestImportCobraNetwork(t *testing.T) {
	importCmd(t)
	if os.Getenv("ROTINI_IMPORT_NETWORK") != "1" {
		t.Skip("set ROTINI_IMPORT_NETWORK=1 to import the importer's pinned real programs")
	}
	bin := rotiniBin(t)
	root := repoRoot(t)
	cases := []struct {
		fixture, cli string
		sub          []string // a sub-command whose help must print
	}{
		{fixture: "delve", cli: "dlv", sub: []string{"exec", "--help"}},
		{fixture: "kubectl", cli: "kubectl", sub: []string{"get", "--help"}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			dir := importFixture(t, "network/"+tc.fixture)
			before := treeHash(t, dir)
			run := runIn(t, dir)
			step := func(what string, f func()) {
				t.Helper()
				start := time.Now()
				f()
				t.Logf("%s: %s", what, time.Since(start).Round(time.Millisecond))
			}

			spec := filepath.Join("cmd", tc.cli, ".rotini.spec.yaml")
			conf := filepath.Join("cmd", tc.cli, ".rotini.conf.yaml")
			step("import", func() {
				out := run(bin, importCmd(t, ".")...)
				if !strings.Contains(out, "spec: cmd/"+tc.cli+"/.rotini.spec.yaml") {
					t.Fatalf("import output:\n%s", out)
				}
			})
			if treeHash(t, dir) != before {
				t.Fatal("the import changed the program's files, go.mod or go.sum")
			}
			step("validate", func() { run(bin, "validate", spec, "--config", conf) })
			step("generate", func() {
				if got := run(bin, "generate", spec, "--config", conf); strings.Contains(got, "created:") {
					t.Errorf("a generate right after the import created files:\n%s", got)
				}
			})
			exe := tc.cli
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			step("build", func() {
				run("go", append([]string{"mod", "edit", "-require=github.com/go-rotini/rotini@v0.0.0",
					"-replace=github.com/go-rotini/rotini=" + strings.ReplaceAll(root, `\`, `/`)}, testmod.EditFlags(root)...)...)
				run("go", "mod", "tidy")
				run("go", "build", "-o", exe, "./cmd/"+tc.cli)
			})
			if help := run(filepath.Join(dir, exe), "--help"); !strings.Contains(help, "Usage:") || !strings.Contains(help, "completion") {
				t.Errorf("%s --help:\n%s", tc.cli, help)
			}
			if help := run(filepath.Join(dir, exe), tc.sub...); !strings.Contains(help, "Usage:\n  "+tc.cli+" "+tc.sub[0]) {
				t.Errorf("%s %s:\n%s", tc.cli, strings.Join(tc.sub, " "), help)
			}
		})
	}
}

// TestTreeHash pins what the import must leave alone: code under internal/ counts, the
// imported CLI's own directories don't.
func TestTreeHash(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module x\n")
	write("internal/cmd/tool/tool.go", "package tool\n")
	before := treeHash(t, dir)
	write("cmd/acme/.rotini.spec.yaml", "name: acme\n")
	write("internal/cmd/acme/acme.go", "package acme\n")
	if treeHash(t, dir) != before {
		t.Error("the imported CLI's directories changed the hash")
	}
	write("internal/cmd/tool/tool.go", "package tool // edited\n")
	if treeHash(t, dir) == before {
		t.Error("an edit under internal/cmd/tool left the hash unchanged")
	}
}

// runIn returns a function that runs a command in dir and returns its combined output, failing
// the test when it fails.
func runIn(t *testing.T, dir string) func(name string, args ...string) string {
	return func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, out.String(), errb.String())
		}
		return out.String() + errb.String()
	}
}

// copyFixture copies a fixture module into a temporary directory.
func copyFixture(t *testing.T, from string) string {
	t.Helper()
	to := t.TempDir()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return to
}

// importedCLIs are the CLI names the import tests write under cmd/.
var importedCLIs = []string{"acme", "acme-strict", "acmecli", "dlv", "edge", "kubectl", "shell", "single", "tasks"}

// importFixture copies the corpus program name ("corpus/demo", "network/delve") to a
// temporary directory, adds a package of its own under internal/cmd, and returns it.
func importFixture(t *testing.T, name string) string {
	t.Helper()
	dir := copyFixture(t, filepath.Join("testdata", "import", filepath.FromSlash(name)))
	own := filepath.Join(dir, "internal", "cmd", "tool")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "tool.go"), []byte("package tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// treeHash hashes every file of the module that isn't under cmd/<cli> or internal/cmd/<cli>
// (one of importedCLIs): the program's own files, go.mod and go.sum.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			parent := filepath.ToSlash(filepath.Dir(rel))
			if (parent == "cmd" || parent == "internal/cmd") && slices.Contains(importedCLIs, filepath.Base(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(rel + "\n"))
		h.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
