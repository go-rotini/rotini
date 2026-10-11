package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const installConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
  features:
    - type: completion
      enabled: true
      install_dir: share
    - type: man
      enabled: true
      embed: %s
      section: 8
      install_dir: share
`

const installSpec = `version: 0.0.0
command:
  name: demo
  topics:
    - { name: filters, summary: how filters work, body: A filter is a word. }
  commands:
    - name: add
      summary: add one
    - name: secret
      hidden: true
`

// TestInstallFiles checks that install_dir gets every shell's script and every visible man page
// (topics included, hidden commands not), each holding exactly what the generated variable
// holds, whether the man feature embeds its pages or not.
func TestInstallFiles(t *testing.T) {
	for _, embed := range []string{"false", "true"} {
		t.Run("embed="+embed, func(t *testing.T) {
			dir := generateInModule(t, installSpec, strings.Replace(installConf, "%s", embed, 1))
			share := filepath.Join(dir, "share")
			gp := resolveInModule(t, dir)
			scripts := map[string]string{"bash": "demo.bash", "zsh": "_demo", "fish": "demo.fish", "powershell": "demo.ps1"}
			for shell, file := range scripts {
				want, err := completionScript("demo", shell, completionEnvs{})
				if err != nil {
					t.Fatal(err)
				}
				if got := readTestFile(t, filepath.Join(share, "completions", file)); got != want {
					t.Errorf("completions/%s differs from the %s script", file, shell)
				}
			}
			nodes := flattenFeature(gp, manFeatureDesc)
			contents, err := docFeatureContents(gp.plan, "", nodes, manFeatureDesc, false)
			if err != nil {
				t.Fatal(err)
			}
			for i, n := range nodes {
				path := filepath.Join(share, "man", "man8", n.file)
				if !n.listed {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Errorf("%s: a hidden command got an installed page", n.file)
					}
					continue
				}
				if got := readTestFile(t, path); got != contents[i] {
					t.Errorf("man/man8/%s differs from the page", n.file)
				}
			}
			if _, err := os.Stat(filepath.Join(share, "man", "man8", "demo-filters.8")); err != nil {
				t.Errorf("the topic's page was not installed: %v", err)
			}
		})
	}
}

// TestInstallFiles_prune checks that a removed command's installed page and every page of an
// old section are removed, while a file that isn't one of the program's pages survives.
func TestInstallFiles_prune(t *testing.T) {
	dir := generateInModule(t, installSpec, strings.Replace(installConf, "%s", "false", 1))
	writeTestFile(t, dir, "share/man/man8/other.8", "not ours")
	writeTestFile(t, dir, "share/man/man1/demo-old.1", "a page from section 1")

	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n")
	runGenerate(t, dir)
	for _, gone := range []string{"share/man/man8/demo-add.8", "share/man/man8/demo-filters.8", "share/man/man1/demo-old.1"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived; want it pruned", gone)
		}
	}
	for _, kept := range []string{"share/man/man8/demo.8", "share/man/man8/other.8"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s: %v; want it kept", kept, err)
		}
	}
}

// TestInstallFiles_dryRun checks that a dry run lists the install files and writes none.
func TestInstallFiles_dryRun(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", installSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", strings.Replace(installConf, "%s", "false", 1))
	t.Chdir(dir)
	planned, err := NewProcessor("0.0.0").GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(planned.Changes, "\n")
	for _, want := range []string{"create share/completions/_demo", "create share/man/man8/demo-add.8"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes =\n%s\nwant a line with %q", joined, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "share")); !os.IsNotExist(err) {
		t.Error("a dry run wrote install files")
	}
}

// generateInModule writes spec and conf into a fresh module, generates there, and returns the
// module's directory.
func generateInModule(t *testing.T, spec, conf string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	runGenerate(t, dir)
	return dir
}

// runGenerate runs a generate pass in dir, failing the test on any error.
func runGenerate(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
	var reported error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			reported = e
		}
	}, func([]error) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if reported != nil {
		t.Fatalf("Generate reported: %v", reported)
	}
}

// resolveInModule resolves the module's spec and conf the way generate does.
func resolveInModule(t *testing.T, dir string) *program {
	t.Helper()
	rs, err := reconcileSpec(filepath.Join(dir, ".rotini.spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := reconcileConf(filepath.Join(dir, ".rotini.spec.yaml"), filepath.Join(dir, ".rotini.conf.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gp, err := resolveTree(rs.spec, filepath.Join(dir, ".rotini.spec.yaml"), "example.com/demo")
	if err != nil {
		t.Fatal(err)
	}
	gp.conf = rc.conf
	return gp
}
