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
)

// importShim returns the local checkout of github.com/go-rotini/import that `rotini import`
// runs, from ROTINI_IMPORT_SHIM, or skips the test. The import adds the importer to a
// temporary go.mod, so the run needs it published or checked out.
func importShim(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test and go build in imported modules; skipped under -short")
	}
	shim := os.Getenv("ROTINI_IMPORT_SHIM")
	if shim == "" {
		t.Skip("set ROTINI_IMPORT_SHIM to a checkout of github.com/go-rotini/import to run the import tests")
	}
	if _, err := os.Stat(filepath.Join(shim, "cobra", "testdata", "corpus")); err != nil {
		t.Fatalf("ROTINI_IMPORT_SHIM: %v", err)
	}
	return shim
}

// TestImportCobra imports each of the importer's own corpus programs, then validates,
// regenerates, builds and runs the imported CLI. The program's own files, go.mod and go.sum
// must be byte-identical after the import.
func TestImportCobra(t *testing.T) {
	shim := importShim(t)
	bin := rotiniBin(t)
	root := repoRoot(t)
	cases := []struct {
		fixture, pkg, cli string
		args              []string
		// runs is a command line the built CLI must accept: ssh stops parsing flags at its
		// first argument, so a word after it that looks like a flag is an argument.
		runs []string
	}{
		{fixture: "demo", pkg: ".", cli: "acme", runs: []string{"ssh", "node1", "--not-a-flag", "-t"}},
		{fixture: "cobracli", pkg: "./cmd", cli: "acmecli", args: []string{"--strict"}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			dir := copyFixture(t, filepath.Join(shim, "cobra", "testdata", "corpus", tc.fixture))
			before := treeHash(t, dir)
			run := runIn(t, dir)

			// A dry run writes nothing.
			cmd := exec.Command(bin, append([]string{"import", "cobra", "--importer-version", shim, "--dry-run", tc.pkg}, tc.args...)...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "dry run:") {
				t.Fatalf("import --dry-run: %v\n%s", err, out)
			}
			if treeHash(t, dir) != before {
				t.Fatal("a dry run changed the module")
			}

			out := run(bin, append([]string{"import", "cobra", "--importer-version", shim, tc.pkg}, tc.args...)...)
			if !strings.Contains(out, "spec: cmd/"+tc.cli+"/.rotini.spec.yaml") || !strings.Contains(out, "imported ") {
				t.Fatalf("import output:\n%s", out)
			}
			if after := treeHash(t, dir); after != before {
				t.Fatal("the import changed the program's files, go.mod or go.sum")
			}

			spec := filepath.Join("cmd", tc.cli, ".rotini.spec.yaml")
			conf := filepath.Join("cmd", tc.cli, ".rotini.conf.yaml")
			run(bin, "validate", spec, "--config", conf)
			if got := run(bin, "generate", spec, "--config", conf); strings.Contains(got, "created:") {
				t.Errorf("a generate right after the import created files:\n%s", got)
			}

			run("go", "mod", "edit", "-require=github.com/go-rotini/rotini@v0.0.0",
				"-replace=github.com/go-rotini/rotini="+strings.ReplaceAll(root, `\`, `/`))
			run("go", "mod", "tidy")
			exe := tc.cli
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			run("go", "build", "-o", exe, "./cmd/"+tc.cli)
			help := run(filepath.Join(dir, exe), "--help")
			if !strings.Contains(help, "Usage:") || !strings.Contains(help, "completion") {
				t.Errorf("%s --help:\n%s", tc.cli, help)
			}
			if script := run(filepath.Join(dir, exe), "completion", "bash"); !strings.Contains(script, "bash completion for "+tc.cli) {
				t.Errorf("%s completion bash printed no script:\n%.200s", tc.cli, script)
			}
			if tc.runs != nil {
				run(filepath.Join(dir, exe), tc.runs...)
			}
		})
	}
}

// TestImportCobraFailures pins the explained failures: flag.Parse in an init function, and a
// package with no root to find.
func TestImportCobraFailures(t *testing.T) {
	shim := importShim(t)
	bin := rotiniBin(t)

	dir := copyFixture(t, filepath.Join(shim, "cobra", "testdata", "corpus", "flagparse"))
	cmd := exec.Command(bin, "import", "cobra", "--importer-version", shim, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "flag.Parse in an init function") {
		t.Errorf("flag.Parse in init: %v\n%s", err, out)
	}

	dir = copyFixture(t, filepath.Join(shim, "cobra", "testdata", "corpus", "cobracli"))
	cmd = exec.Command(bin, "import", "cobra", "--importer-version", shim, ".")
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
	shim := importShim(t)
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
			dir := copyFixture(t, filepath.Join(shim, "cobra", "testdata", "network", tc.fixture))
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
				out := run(bin, "import", "cobra", "--importer-version", shim, ".")
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
				run("go", "mod", "edit", "-require=github.com/go-rotini/rotini@v0.0.0",
					"-replace=github.com/go-rotini/rotini="+strings.ReplaceAll(root, `\`, `/`))
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
var importedCLIs = []string{"acme", "acmecli", "dlv", "kubectl"}

// treeHash hashes every file of the module that isn't under cmd/<cli> (one of importedCLIs) or
// internal: the program's own files, go.mod and go.sum.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if rel == "internal" || filepath.Dir(rel) == "cmd" && slices.Contains(importedCLIs, filepath.Base(rel)) {
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
