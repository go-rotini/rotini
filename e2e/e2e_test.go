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
	// The version is stamped so a script can assert it, and so the spec/conf version guard
	// has something real to compare against.
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version=1.2.3", "-o", bin, "./cmd/rotini")
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
				if neg || len(args) != 1 {
					ts.Fatalf("usage: gomodinit <module-path>")
				}
				ts.Check(os.WriteFile(
					filepath.Join(ts.MkAbs("."), "go.mod"),
					[]byte(gomod(args[0], ts.Getenv("ROTINI_ROOT"))),
					0o600,
				))
			},
		},
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// gomod is the go.mod every script starts from: rotini required at a placeholder version and
// replaced with the working tree.
func gomod(module, root string) string {
	return fmt.Sprintf(`module %s

go 1.27

require github.com/go-rotini/rotini v0.0.0

replace github.com/go-rotini/rotini => %s
`, module, strings.ReplaceAll(root, `\`, `/`))
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
