//go:build !mutation

// Package e2e is rotini's outside-in tier: each test is a script that drives the REAL rotini
// binary through a user's whole path — write a spec, generate, build, and then RUN the binary
// and assert what it printed and what it exited with.
//
// # Why this tier exists
//
// Every other tier stops at `go build`. The codegen golden tests pin that generated output
// does not CHANGE; nothing pinned that it is RIGHT. Swap two flag identifiers in codegen and
// the whole suite still passes after `-update`: the golden absorbs the change, the module
// still compiles, and the conformance matrix — which runs against hand-written structs shaped
// like codegen's output — never sees it. It would reach a user as "my -f flag does nothing".
//
// The proof that this gap was real: `rotini init` shipped a scaffold that compiled, that
// TestInitialize_endToEnd verified compiled, and whose `--help` exited 1 with
// `unknown flag "--help"`. No test in the repo could have caught it. The first script here is
// that case.
//
// # Writing a script
//
// A script is a .txtar under testdata/script: commands, then files after `-- name --` lines.
// `rotini` is on $PATH, built from this working tree. The usual verbs:
//
//	exec rotini init demo      run a command; a leading ! means it must FAIL
//	stdout 'Usage:'            the last command's stdout must match this regexp
//	! stderr .                 stderr must be empty
//	cmp got.txt want.txt       two files must be identical (-update rewrites want)
//	env NO_COLOR=1             set an environment variable
//	exists cmd/demo/main.go    a path must exist
//	wantexit 2 ./app --bad     run a command and assert its EXACT exit status
//
// wantexit is rotini's own addition. `! exec` only proves non-zero, and an exit code is a
// CLI's most load-bearing output — the one thing a shell script branches on.
//
// Scripts are the campaign's rigs made permanent: a rig used to be a directory someone
// replayed by hand, and its value evaporated when the campaign ended. A .txtar runs in CI.
package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain builds the rotini binary once and puts it on every script's $PATH.
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{}))
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
	// Deliberately UNSTAMPED, so it reports the same version `go tool rotini` does when a
	// script runs `go generate` — that path compiles rotini from the replace and cannot be
	// stamped, and a mismatch between the two would fire the version guard on documents
	// `rotini init` had just seeded. A script that needs a real version builds its own
	// stamped binary (see r8_version_guard).
	//
	// -buildvcs=false is what keeps it unstamped. Since Go 1.24 a build inside a git checkout
	// takes the main module's version from VCS, so on a tagged commit this binary reported
	// that tag (v1.1.0, or v1.1.0+dirty) and the guard rejected every fixture's `version:`.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/rotini")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build rotini: %v\n%s", err, out)
	}
	return bin
}

// repoRoot is the module root — one level up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestScripts runs every .txtar under testdata/script.
//
// Each gets a temp $WORK directory with a go.mod already wired: the module requires rotini at
// a placeholder version and REPLACES it with this working tree, which is the campaign's
// "Mode B". Mode A — a clean `go get -tool` against a published version, with no replace — is
// a separate release-gate job, because Mode B can be green while Mode A is broken.
func TestScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs generated modules; skipped under -short")
	}

	root := repoRoot(t)
	bin := rotiniBin(t)

	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			// The rotini binary, invoked exactly as a user invokes it.
			env.Vars = append(env.Vars,
				"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
				"ROTINI_ROOT="+root,
				// testscript hands each script a $HOME that does not exist, so the go
				// command's caches have to be pointed at the ambient ones explicitly.
				// Sharing them also keeps every script from re-downloading and
				// re-compiling the dependency graph, without which this tier is
				// unusably slow.
				"GOMODCACHE="+goEnv("GOMODCACHE"),
				"GOCACHE="+goEnv("GOCACHE"),
				"GOPATH="+goEnv("GOPATH"),
				"GOFLAGS=-mod=mod",
				"GOTOOLCHAIN=local",
				// Deterministic output: no color, no terminal-dependent behavior.
				"NO_COLOR=1",
				"TERM=dumb",
			)
			// Windows decides what is a PROGRAM by extension, from PATHEXT, and testscript
			// passes almost no variables through. Without it PowerShell takes app.exe for a
			// document to open rather than a program to run, and captures none of its output.
			if runtime.GOOS == "windows" {
				env.Vars = append(env.Vars, "PATHEXT="+os.Getenv("PATHEXT"))
			}
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			// wantexit runs a command and asserts its exact exit status, which `exec`
			// and `! exec` between them cannot express.
			"wantexit": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 2 {
					ts.Fatalf("usage: wantexit <code> <command> [args...]")
				}
				want, err := strconv.Atoi(args[0])
				if err != nil {
					ts.Fatalf("wantexit: %q is not an exit code", args[0])
				}

				// ts.Exec runs with the script's own environment and working directory,
				// and reports the child's status as an *exec.ExitError.
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
			// gomodinit writes a go.mod wired to this working tree, which every script
			// needs before it can build anything.
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
		},
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// gomod is the go.mod every script starts from: rotini required at a placeholder version,
// declared as a TOOL, and replaced with the working tree.
//
// The tool directive is what `go tool rotini` and therefore `go generate ./...` resolve
// through — the seeded main.go carries a //go:generate line that calls it, so a module
// without it cannot run the loop the docs describe. `go get -tool` writes the same line;
// here the replace points it at the working tree instead of a published version.
//
// The required VERSION is what a rotini built through this module reports: build info names
// the module version, and that outranks any -ldflags stamp (which is the right precedence —
// `go install pkg@v1.2.3` must report v1.2.3). A script that needs a particular version asks
// for it here rather than trying to stamp a build it does not control.
func gomod(module, version, root string) string {
	return fmt.Sprintf(`module %s

go 1.27

require github.com/go-rotini/rotini %s

tool github.com/go-rotini/rotini/cmd/rotini

replace github.com/go-rotini/rotini %s => %s
`, module, version, version, strings.ReplaceAll(root, `\`, `/`))
}

// goEnv reads one `go env` value, preferring an already-set environment variable. Resolved
// once per test binary and memoized: `go env` is a process spawn, and Setup runs per script.
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
// setup page actually shows.
//
// r0_tutorial says it copies "the page's handler, verbatim" and then asserts it builds and
// runs — which is the whole reason that script is worth having. But the fixture is a copy, and
// a copy drifts: the page gained an `rtx.Halt()` and the fixture did not, so for a while the
// script was proving a handler no reader would ever have written.
//
// A test is cheaper than the discipline it replaces.
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
