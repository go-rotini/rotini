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
)

var updateShells = flag.Bool("update-shells", false, "rewrite testdata/shells/want from the shells' answers")

// A shell is one pinned image in the completion matrix: the image's Dockerfile under
// testdata/shells/images, and the command that runs its driver in /w.
type shell struct {
	name string
	run  []string
}

var shells = []shell{
	{"bash4", []string{"bash", "drivers/bash.sh", "completion_bash.sh", "cases.txt"}},
	{"bash5", []string{"bash", "drivers/bash.sh", "completion_bash.sh", "cases.txt"}},
	{"zsh", []string{"zsh", "-f", "drivers/zsh.zsh", "completion_zsh.zsh", "cases.txt"}},
	{"fish", []string{"fish", "--no-config", "drivers/fish.fish", "completion_fish.fish", "cases.txt"}},
	{"pwsh", []string{"pwsh", "-NoProfile", "-NonInteractive", "-File", "drivers/pwsh.ps1", "completion_powershell.ps1", "cases.txt"}},
	{"nu", []string{"nu", "--no-config-file", "drivers/nu.nu", "cases.txt"}},
}

// TestCompletionShells runs the same generated CLI and the same cases (testdata/shells/cases.txt)
// in one pinned Docker image per shell. Each driver sources the completion script rotini
// generated and calls the shell's completion the way TAB does, and the candidates, descriptions
// and messages it prints must match testdata/shells/want/<shell>.txt. Each image adds the
// account rotiniuser, the group rotinigroup and the host rotini-host, so the user, group and host
// kinds have a known answer. Run it with `make test-completion-shells`; -update-shells rewrites
// the want files.
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
	work := t.TempDir()
	app := filepath.Join(work, "src")
	if err := os.CopyFS(app, os.DirFS(filepath.Join(src, "app"))); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/shells\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\n" +
		"replace github.com/go-rotini/rotini => " + repoRoot(t) + "\n"
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	goenv := []string{"GOWORK=off", "GOFLAGS=-mod=mod"}
	mustRun(t, app, nil, rotiniBin(t), "generate", "cmd/app/.rotini.spec.yaml", "--config", "cmd/app/.rotini.conf.yaml")
	mustRun(t, app, goenv, "go", "mod", "tidy")

	// /w in the container: the program on PATH, the scripts, the drivers, the cases, and files
	// for the path kinds to find.
	bin := filepath.Join(work, "bin")
	mustRun(t, app, append(goenv, "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0"), "go", "build", "-o", filepath.Join(bin, "app"), "./cmd/app")
	mustWrite(t, filepath.Join(bin, "rotini-exe-demo"), "#!/bin/sh\n", 0o755)
	for script, file := range map[string]string{
		"bash": "completion_bash.sh", "zsh": "completion_zsh.zsh", "fish": "completion_fish.fish",
		"powershell": "completion_powershell.ps1", "nushell": "completion_nushell.nu",
	} {
		body, err := os.ReadFile(filepath.Join(app, "internal", "cmd", "app", "renders", "completion_"+script+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(work, file), string(body), 0o644)
	}
	if err := os.CopyFS(filepath.Join(work, "drivers"), os.DirFS(filepath.Join(src, "drivers"))); err != nil {
		t.Fatal(err)
	}
	cases, err := os.ReadFile(filepath.Join(src, "cases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(work, "cases.txt"), string(cases), 0o644)
	for _, f := range []string{"a.yaml", "b.yml", "c.txt", "sub/d.yaml"} {
		mustWrite(t, filepath.Join(work, f), "", 0o644)
	}

	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			image := "rotini-completion-" + sh.name
			mustRun(t, src, nil, "docker", "build", "--quiet", "--tag", image, "--file", filepath.Join("images", sh.name+".Dockerfile"), "images")
			args := []string{"run", "--rm", "--network", "none", "--add-host", "rotini-host:10.0.0.7",
				"--volume", work + ":/w", "--workdir", "/w", "--env", "HOME=/tmp", image, "env", "PATH=/w/bin:/usr/local/bin:/usr/bin:/bin"}
			got := sortUnordered(mustRun(t, "", nil, "docker", append(args, sh.run...)...), string(cases))
			if strings.Contains(got, ":rotini:") {
				t.Errorf("a directive reached the shell's candidates:\n%s", got)
			}
			want := filepath.Join(src, "want", sh.name+".txt")
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
		})
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
