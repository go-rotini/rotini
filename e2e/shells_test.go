//go:build !mutation

package e2e

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/testmod"
)

var updateShells = flag.Bool("update-shells", false, "rewrite testdata/shells/want from the shells' answers")

// A shell is one pinned image in the completion matrix: the image's Dockerfile under
// testdata/shells/images, and the command that runs its driver in /w.
type shell struct {
	name string
	run  []string
}

var shells = []shell{
	{"bash3", []string{"bash", "drivers/bash.sh", "completion_bash.sh", "cases.txt"}},
	{"bash4", []string{"bash", "drivers/bash.sh", "completion_bash.sh", "cases.txt"}},
	{"bash5", []string{"bash", "drivers/bash.sh", "completion_bash.sh", "cases.txt"}},
	{"zsh", []string{"zsh", "-f", "drivers/zsh.zsh", "completion_zsh.zsh", "cases.txt"}},
	{"fish", []string{"fish", "--no-config", "drivers/fish.fish", "completion_fish.fish", "cases.txt"}},
	{"pwsh", []string{"pwsh", "-NoProfile", "-NonInteractive", "-File", "drivers/pwsh.ps1", "completion_powershell.ps1", "cases.txt"}},
	{"nu", []string{"nu", "--no-config-file", "drivers/nu.nu", "cases.txt"}},
}

// zshWidget completes the cases in widgetCases in a live zsh completion widget, in the zsh image,
// so zsh's own matching decides which candidates it keeps.
var zshWidget = shell{"zsh-widget", []string{"zsh", "-f", "drivers/zsh_widget.zsh", "completion_zsh.zsh", "drivers/widget-cases.txt"}}

// widgetCases are the cases zshWidget completes: those whose candidates zsh's matching filters.
var widgetCases = []string{"commands", "command-prefix", "flags", "enum", "enum-inline", "enum-ignore-case", "enum-ignore-case-inline", "sub-command-enum", "quote-all"}

// TestCompletionShells runs the same generated CLI and the same cases (testdata/shells/cases.txt)
// in one pinned Docker image per shell. Each driver sources the completion script rotini
// generated and calls the shell's completion the way TAB does, and the candidates, descriptions
// and messages it prints must match testdata/shells/want/<shell>.txt. Each image adds the
// account rotiniuser, the group rotinigroup and the host rotini-host, so the user, group and host
// kinds have a known answer. A subset of the cases also runs the same scripts against a binary
// built with the previous release, matching want/skew-<shell>.txt: a user who updates one side
// and not the other still gets completions. zsh also completes a few cases in a live completion
// widget (want/zsh-widget.txt), where its own matching filters the candidates. Run it with
// `make test-completion-shells`; -update-shells rewrites the want files.
func TestCompletionShells(t *testing.T) {
	if os.Getenv("ROTINI_COMPLETION_SHELLS") == "" {
		t.Skip("set ROTINI_COMPLETION_SHELLS=1 (make test-completion-shells) to run the Docker shell matrix")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("the completion shell matrix needs docker")
	}
	arch := strings.TrimSpace(mustRun(t, "", nil, "docker", "version", "--format", "{{.Server.Arch}}"))

	src, err := filepath.Abs(filepath.Join("testdata", "shells"))
	if err != nil {
		t.Fatal(err)
	}
	goenv := []string{"GOWORK=off", "GOFLAGS=-mod=mod"}
	linux := []string{"GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=0"}
	work := t.TempDir()
	scripts := buildShellsFixture(t, src, "app", "app", work, goenv, linux)
	cases, err := os.ReadFile(filepath.Join(src, "cases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	stageShells(t, work, src, scripts, string(cases))
	widget := keepCases(string(cases), widgetCases...)
	mustWrite(t, filepath.Join(work, "drivers", "widget-cases.txt"), widget, 0o644)

	// The same scripts against a binary built entirely with the previous release: its own code
	// generation, its own runtime, and a fixture (testdata/shells/skew) written for it.
	skew := t.TempDir()
	skewApp := filepath.Join(skew, "src")
	if err := os.CopyFS(skewApp, os.DirFS(filepath.Join(src, "skew"))); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(skewApp, "go.mod"), "module example.com/shells\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini "+skewRelease+"\n", 0o600)
	mustRun(t, skewApp, goenv, "go", "run", "github.com/go-rotini/rotini/cmd/rotini@"+skewRelease, "generate", "cmd/app/.rotini.spec.yaml", "--config", "cmd/app/.rotini.conf.yaml")
	mustRun(t, skewApp, goenv, "go", "mod", "tidy")
	mustRun(t, skewApp, append(goenv, linux...), "go", "build", "-o", filepath.Join(skew, "bin", "app"), "./cmd/app")
	skewCases := keepCases(string(cases), "commands", "flags", "enum", "file-extensions", "directory", "none-with-message", "completer", "sub-command-enum")
	stageShells(t, skew, src, scripts, skewCases)

	// A program whose name has a dash, which no shell variable name may hold.
	dashed := t.TempDir()
	dashedScripts := buildShellsFixture(t, src, "dashed", "my-app", dashed, goenv, linux)
	dashedCases, err := os.ReadFile(filepath.Join(src, "dashed-cases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	stageShells(t, dashed, src, dashedScripts, string(dashedCases))

	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			image := "rotini-completion-" + sh.name
			mustRun(t, src, nil, "docker", "build", "--quiet", "--tag", image, "--file", filepath.Join("images", sh.name+".Dockerfile"), "images")
			runShell(t, sh, image, work, string(cases), filepath.Join(src, "want", sh.name+".txt"))
			runShell(t, sh, image, skew, skewCases, filepath.Join(src, "want", "skew-"+sh.name+".txt"))
			runShell(t, sh, image, dashed, string(dashedCases), filepath.Join(src, "want", "dashed-"+sh.name+".txt"))
			if sh.name == "zsh" {
				runShell(t, zshWidget, image, work, widget, filepath.Join(src, "want", zshWidget.name+".txt"))
			}
		})
	}
}

// buildShellsFixture generates the CLI named name from testdata/shells/<fixture> with this
// tree's rotini, builds it for Linux into dir/bin, and returns its completion scripts by the file
// names the drivers read.
func buildShellsFixture(t *testing.T, src, fixture, name, dir string, goenv, linux []string) map[string]string {
	t.Helper()
	app := filepath.Join(dir, "src")
	if err := os.CopyFS(app, os.DirFS(filepath.Join(src, fixture))); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/shells\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\n" +
		"replace github.com/go-rotini/rotini => " + repoRoot(t) + "\n" + testmod.Directives(repoRoot(t))
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, app, nil, rotiniBin(t), "generate", "cmd/"+name+"/.rotini.spec.yaml", "--config", "cmd/"+name+"/.rotini.conf.yaml")
	mustRun(t, app, goenv, "go", "mod", "tidy")
	scripts := map[string]string{}
	for script, file := range map[string]string{
		"bash": "completion_bash.sh", "zsh": "completion_zsh.zsh", "fish": "completion_fish.fish",
		"powershell": "completion_powershell.ps1", "nushell": "completion_nushell.nu",
	} {
		body, err := os.ReadFile(filepath.Join(app, "internal", "cmd", name, "renders", "completion_"+script+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		scripts[file] = string(body)
	}
	mustRun(t, app, append(goenv, linux...), "go", "build", "-o", filepath.Join(dir, "bin", name), "./cmd/"+name)
	return scripts
}

// skewRelease is the release whose binary the skew run pairs with this tree's completion scripts.
const skewRelease = "v1.4.3"

// stageShells lays out dir as /w in the container: the scripts, the drivers, the cases, files for
// the path kinds to find, and beside the program in bin, an executable for the executable kind.
func stageShells(t *testing.T, dir, src string, scripts map[string]string, cases string) {
	t.Helper()
	for file, body := range scripts {
		mustWrite(t, filepath.Join(dir, file), body, 0o644)
	}
	if err := os.CopyFS(filepath.Join(dir, "drivers"), os.DirFS(filepath.Join(src, "drivers"))); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "cases.txt"), cases, 0o644)
	mustWrite(t, filepath.Join(dir, "bin", "rotini-exe-demo"), "#!/bin/sh\n", 0o755)
	for _, f := range []string{"a.yaml", "b.yml", "c.txt", "sub/d.yaml"} {
		mustWrite(t, filepath.Join(dir, f), "", 0o644)
	}
}

// keepCases returns the lines of cases naming one of names.
func keepCases(cases string, names ...string) string {
	var b strings.Builder
	for line := range strings.Lines(cases) {
		if name, _, _ := strings.Cut(line, "\t"); slices.Contains(names, name) {
			b.WriteString(line)
		}
	}
	return b.String()
}

// runShell runs sh's driver over the layout in dir and compares its answers with want.
func runShell(t *testing.T, sh shell, image, dir, cases, want string) {
	t.Helper()
	args := []string{"run", "--rm", "--network", "none", "--add-host", "rotini-host:10.0.0.7",
		"--volume", dir + ":/w", "--workdir", "/w", "--env", "HOME=/tmp", image, "env", "PATH=/w/bin:/usr/local/bin:/usr/bin:/bin"}
	got := sortUnordered(mustRun(t, "", nil, "docker", append(args, sh.run...)...), cases)
	if strings.Contains(got, ":rotini:") {
		t.Errorf("a directive reached the shell's candidates:\n%s", got)
	}
	if *updateShells {
		mustWrite(t, want, got, 0o644)
		return
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("%v (run with -update-shells to create it)", err)
	}
	if got != string(b) {
		t.Errorf("%s completion differs from %s\n--- got\n%s--- want\n%s", sh.name, want, got, b)
	}
}

// sortUnordered sorts the candidates of each case cases.txt marks unordered: those a shell reads
// from a directory, in the file system's order.
func sortUnordered(out, cases string) string {
	unordered := map[string]bool{}
	for line := range strings.Lines(cases) {
		if f := strings.Split(strings.TrimRight(line, "\n"), "\t"); len(f) == 3 && f[2] == "unordered" {
			unordered["== "+f[0]] = true
		}
	}
	var b strings.Builder
	var block []string
	flush := func() {
		slices.Sort(block)
		for _, l := range block {
			b.WriteString(l)
		}
		block = nil
	}
	sorting := false
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "== ") {
			flush()
			sorting = unordered[strings.TrimRight(line, "\n")]
			b.WriteString(line)
			continue
		}
		if sorting {
			block = append(block, line)
		} else {
			b.WriteString(line)
		}
	}
	flush()
	return b.String()
}

// mustRun runs name in dir with extra environment and returns its stdout, failing the test with its
// stderr when it fails.
func mustRun(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}
