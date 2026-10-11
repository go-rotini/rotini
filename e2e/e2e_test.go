//go:build !mutation

// Package e2e is rotini's outside-in test tier: each script drives the rotini binary through a
// user's whole path (write a spec, generate, build, run the generated binary) and asserts its
// output and exit status. Other tiers stop at `go build` or at golden output, so they cannot
// show that a generated CLI behaves correctly.
//
// # Writing a script
//
// A script is a .txtar under testdata/script: commands, then files after `-- name --` lines.
// `rotini` is on $PATH, built from this working tree. The usual verbs:
//
//	exec rotini init demo      run a command; a leading ! means it must fail
//	stdout 'Usage:'            the last command's stdout must match this regexp
//	! stderr .                 stderr must be empty
//	cmp got.txt want.txt       two files must be identical (-update rewrites want)
//	env NO_COLOR=1             set an environment variable
//	exists cmd/demo/main.go    a path must exist
//	wantexit 2 ./app --bad     run a command and assert its exact exit status
//
// wantexit, execout, gomodinit, nodeps, sizebelow, writebytes, genlines, heldstdin and rssbelow
// are rotini's own commands, and pinnedgo its own condition; `! exec` only proves
// a non-zero exit.
//
// # Script names
//
// A script's name starts with the area it covers, and its first comment line repeats it:
//
//	r0   the new-user path: init, generate, validate, regenerate, the setup guide
//	r1   inputs: flags, arguments, env, config, stdin, and checking them
//	r2   rendered output: help, man and markdown pages, completion scripts
//	r3   outcomes and exit codes
//	r4   the lifecycle: hooks, Halt, and the generate-time audits of handlers
//	r5   long-running programs and signals
//	r6   composing one program's spec into another's
//	r7   rotini validate's messages
//	r8   the version guard
//	r9   plugins
//	r10  structured output
//	r11  a lean runtime: linked packages, binary size
//	r12  the recipes page: each recipe's code, built and run
package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain hands control to testscript, which requires it to run scripts.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){})
}

// rotiniBin builds the codegen binary from this working tree, so a script exercises the
// source under test rather than whatever happens to be installed.
func rotiniBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "rotini")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// The binary is unstamped so it reports the same version as `go tool rotini` under
	// `go generate`; a mismatch would trip the version guard on freshly seeded documents.
	// -buildvcs=false stops Go from taking the version from a git tag. A script that needs a
	// real version builds its own stamped binary (see r8_version_guard).
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/rotini")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build rotini: %v\n%s", err, out)
	}
	return bin
}

// repoRoot returns the module root, one level up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestScripts builds rotini from this working tree and runs every .txtar under testdata/script,
// each in a temp $WORK directory. Scripts call gomodinit for a go.mod that replaces rotini with
// the working tree; testing against a published version is a separate release-gate job.
func TestScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs generated modules; skipped under -short")
	}

	root := repoRoot(t)
	bin := rotiniBin(t)

	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			env.Vars = append(env.Vars,
				"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
				"ROTINI_ROOT="+root,
				// testscript's $HOME does not exist, so point the go command at the ambient
				// caches; sharing them also avoids re-downloading and re-compiling per script.
				"GOMODCACHE="+goEnv("GOMODCACHE"),
				"GOCACHE="+goEnv("GOCACHE"),
				"GOPATH="+goEnv("GOPATH"),
				"GOFLAGS=-mod=mod",
				"GOTOOLCHAIN=local",
				// Deterministic output: no color, no terminal-dependent behavior.
				"NO_COLOR=1",
				"TERM=dumb",
			)
			// Without PATHEXT, PowerShell treats app.exe as a document to open rather than a
			// program to run, and captures none of its output.
			if runtime.GOOS == "windows" {
				env.Vars = append(env.Vars, "PATHEXT="+os.Getenv("PATHEXT"))
			}
			return nil
		},
		Condition: func(cond string) (bool, error) {
			if cond == "pinnedgo" {
				return pinnedToolchain(root)
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
		Cmds: mergeCmds(dataCmds(), map[string]func(ts *testscript.TestScript, neg bool, args []string){
			// nodeps fails when the last command's stdout, a `go list -deps` listing, names a
			// package in testdata/forbidden_deps.txt.
			"nodeps": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 0 {
					ts.Fatalf("usage: nodeps")
				}
				list, err := os.ReadFile(filepath.Join(ts.Getenv("ROTINI_ROOT"), "testdata", "forbidden_deps.txt"))
				ts.Check(err)
				deps := strings.Fields(ts.ReadFile("stdout"))
				var linked []string
				for line := range strings.Lines(string(list)) {
					pkg := strings.TrimSpace(line)
					if pkg != "" && !strings.HasPrefix(pkg, "#") && slices.Contains(deps, pkg) {
						linked = append(linked, pkg)
					}
				}
				if len(linked) > 0 {
					ts.Fatalf("the program links %s", strings.Join(linked, ", "))
				}
			},
			// concat writes the files after the first, joined, to the first: a page's code block
			// that shows only part of a file, completed by a head the script carries.
			"concat": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 2 {
					ts.Fatalf("usage: concat <out> <file>...")
				}
				var b strings.Builder
				for _, f := range args[1:] {
					b.WriteString(ts.ReadFile(f))
				}
				ts.Check(os.WriteFile(ts.MkAbs(args[0]), []byte(b.String()), 0o644))
			},
			// sizebelow fails when a file is not smaller than a number of bytes.
			"sizebelow": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 2 {
					ts.Fatalf("usage: sizebelow <file> <bytes>")
				}
				limit, err := strconv.ParseInt(args[1], 10, 64)
				if err != nil {
					ts.Fatalf("sizebelow: %q is not a byte count", args[1])
				}
				info, err := os.Stat(ts.MkAbs(args[0]))
				ts.Check(err)
				ts.Logf("%s: %d bytes (budget %d)", args[0], info.Size(), limit)
				if info.Size() >= limit {
					ts.Fatalf("%s is %d bytes, over the budget of %d", args[0], info.Size(), limit)
				}
			},
			// wantexit runs a command and asserts its exact exit status.
			"wantexit": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 2 {
					ts.Fatalf("usage: wantexit <code> <command> [args...]")
				}
				want, err := strconv.Atoi(args[0])
				if err != nil {
					ts.Fatalf("wantexit: %q is not an exit code", args[0])
				}

				runErr := ts.Exec(args[1], args[2:]...)

				got := 0
				var ee *exec.ExitError
				switch {
				case errors.As(runErr, &ee):
					got = ee.ExitCode()
				case runErr != nil:
					ts.Fatalf("wantexit: running %v: %v", args[1:], runErr)
				}
				if got != want {
					ts.Fatalf("wantexit: %v exited %d, want %d", args[1:], got, want)
				}
			},
			// execout runs a command with its stdout written to a file, such as /dev/full, which
			// testscript's captured stdout can't stand in for. Its stderr is the script's stderr.
			"execout": func(ts *testscript.TestScript, neg bool, args []string) {
				if len(args) < 2 {
					ts.Fatalf("usage: execout <stdout-file> <command> [args...]")
				}
				out, err := os.OpenFile(ts.MkAbs(args[0]), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
				ts.Check(err)
				defer out.Close()
				cmd := exec.Command(lookScriptPath(ts, args[1]), args[2:]...)
				cmd.Dir = ts.MkAbs(".")
				cmd.Env = scriptEnv(ts)
				cmd.Stdout = out
				cmd.Stderr = ts.Stderr()
				err = cmd.Run()
				if neg != (err != nil) {
					ts.Fatalf("execout %v: unexpected result: %v", args[1:], err)
				}
			},
			// gomodinit writes a go.mod wired to this working tree (see gomod).
			"gomodinit": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 1 || len(args) > 2 {
					ts.Fatalf("usage: gomodinit <module-path> [rotini-version]")
				}
				version := "v0.0.0"
				if len(args) == 2 {
					version = args[1]
				}
				ts.Check(os.WriteFile(
					filepath.Join(ts.MkAbs("."), "go.mod"),
					[]byte(gomod(args[0], version, ts.Getenv("ROTINI_ROOT"))),
					0o600,
				))
			},
		}),
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// pinnedToolchain reports whether the go command scripts run is the toolchain go.mod names,
// which the linker-output and size checks are calibrated against.
func pinnedToolchain(root string) (bool, error) {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false, err
	}
	for line := range strings.Lines(string(mod)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "toolchain "); ok {
			// Scripts run with GOTOOLCHAIN=local, so ask the same go command.
			cmd := exec.Command("go", "env", "GOVERSION")
			cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
			out, err := cmd.Output()
			if err != nil {
				return false, err
			}
			return strings.TrimSpace(string(out)) == v, nil
		}
	}
	return false, nil
}

// gomod returns a script's go.mod:rotini required at version, declared as a tool (so the
// seeded //go:generate line's `go tool rotini` resolves), and replaced with the working tree at
// root. A rotini built through this module reports version, since build info outranks an
// -ldflags stamp, so a script that needs a particular version passes it here.
func gomod(module, version, root string) string {
	return fmt.Sprintf(`module %s

go 1.27

require github.com/go-rotini/rotini %s

tool github.com/go-rotini/rotini/cmd/rotini

replace github.com/go-rotini/rotini %s => %s
`, module, version, version, strings.ReplaceAll(root, `\`, `/`))
}

// goEnv returns one `go env` value, preferring an already-set environment variable. Results are
// memoized, since Setup runs per script.
func goEnv(name string) string {
	goEnvOnce.Do(func() {
		goEnvCache = map[string]string{}
	})
	goEnvMu.Lock()
	defer goEnvMu.Unlock()
	if v, ok := goEnvCache[name]; ok {
		return v
	}
	v := os.Getenv(name)
	if v == "" {
		if out, err := exec.Command("go", "env", name).Output(); err == nil {
			v = strings.TrimSpace(string(out))
		}
	}
	goEnvCache[name] = v
	return v
}

var (
	goEnvOnce  sync.Once
	goEnvMu    sync.Mutex
	goEnvCache map[string]string
)

// TestTutorialHandlerMatchesTheSetupPage pins r0_tutorial's handler fixture to the handler the
// docs setup page shows, so the script keeps proving the documented code builds and runs.
func TestTutorialHandlerMatchesTheSetupPage(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "content", "docs", "_index.md"))
	if err != nil {
		t.Fatalf("read the setup page: %v", err)
	}
	script, err := os.ReadFile(filepath.Join("testdata", "script", "r0_tutorial.txtar"))
	if err != nil {
		t.Fatalf("read r0_tutorial: %v", err)
	}

	want, ok := codeBlock(string(page), "internal/cmd/todo/todo_add.go")
	if !ok {
		t.Fatal(`the setup page no longer shows a "internal/cmd/todo/todo_add.go" block`)
	}
	_, rest, ok := strings.Cut(string(script), "-- handler.go.txt --\n")
	if !ok {
		t.Fatal("r0_tutorial no longer carries a handler.go.txt fixture")
	}
	if got := strings.TrimRight(rest, "\n"); got != want {
		t.Errorf("r0_tutorial's handler.go.txt is not the page's handler.\n--- fixture\n%s\n--- page\n%s", got, want)
	}
}

// codeBlock returns the body of the site's `{{< code title="<title>" … >}}` shortcode.
func codeBlock(page, title string) (string, bool) {
	_, after, ok := strings.Cut(page, `{{< code title="`+title+`"`)
	if !ok {
		return "", false
	}
	_, body, ok := strings.Cut(after, ">}}\n")
	if !ok {
		return "", false
	}
	body, _, ok = strings.Cut(body, "\n{{< /code >}}")
	return body, ok
}

// scriptEnv is the environment a command a custom script command starts runs with: the
// variables Setup gives every script.
func scriptEnv(ts *testscript.TestScript) []string {
	var env []string
	for _, name := range []string{"PATH", "HOME", "WORK", "TMPDIR", "NO_COLOR", "TERM", "PATHEXT", "SYSTEMROOT", "GOCACHE", "GOMODCACHE", "GOPATH"} {
		if v := ts.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// lookScriptPath resolves a command the way exec does in a script: a path relative to $WORK,
// or a name searched on the script's PATH.
func lookScriptPath(ts *testscript.TestScript, name string) string {
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return ts.MkAbs(name)
	}
	for _, dir := range filepath.SplitList(ts.Getenv("PATH")) {
		for _, ext := range []string{"", ".exe"} {
			p := filepath.Join(dir, name+ext)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	ts.Fatalf("%s: not found on the script's PATH", name)
	return ""
}
