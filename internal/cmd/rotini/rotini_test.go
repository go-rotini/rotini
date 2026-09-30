package rotini

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// The companion CLI is rotini's own dogfood, and these tests drive it exactly as a
// user's CLI would be driven: build the generated Program, bind doubles for the
// codegen entry points (the reason GenerateFn/ValidateFn/InitializeFn are named
// types bound through the registry), and call Run.
//
// Run is used rather than Execute so nothing calls os.Exit, and a fresh Program per
// test keeps registry bindings from leaking between them. Run returns (code, err):
// the error is the run's own recorded failure, so on a failure path a non-nil error
// is the expected result rather than a broken test.

const testVersion = "9.9.9"

// newTestCLI builds an isolated companion-CLI program with captured streams and the
// services main.go binds. Extra services (the codegen doubles) are bound by the caller.
func newTestCLI(t *testing.T) (*rotini.Program, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgram(Handlers()).
		WithStdout(out).
		WithStderr(errb).
		WithExit(func(int) { t.Error("a handler called the exit action; Run must not exit the process") }).
		WithVersion(testVersion)

	return p, out, errb
}

func TestCLI_version(t *testing.T) {
	p, out, _ := newTestCLI(t)
	code, err := p.Run([]string{"version"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "v"+testVersion {
		t.Errorf("version output = %q, want %q", got, "v"+testVersion)
	}
}

// Every command answers --help without doing any work, so a user can always discover
// a command without running it.
func TestCLI_helpFlagOnEveryCommand(t *testing.T) {
	for _, argv := range [][]string{
		{"--help"},
		{"initialize", "--help"},
		{"generate", "--help"},
		{"validate", "--help"},
		{"version", "--help"},
		{"help", "--help"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			p, out, _ := newTestCLI(t)
			// Binding doubles that fail the test proves --help short-circuits before
			// any codegen work is attempted.
			p.Bind("generate", codegen.GenerateFn(func(string, string, bool, func(string, error), func([]error)) error {
				t.Error("--help ran the generate work")
				return nil
			}))
			p.Bind("validate", codegen.ValidateFn(func(string, string, bool, string, func(string, error), func([]error)) error {
				t.Error("--help ran the validate work")
				return nil
			}))
			p.Bind("initialize", codegen.InitializeFn(func(string, string, bool) error {
				t.Error("--help ran the initialize work")
				return nil
			}))

			if _, err := p.Run(argv); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(out.String(), "Usage:") {
				t.Errorf("help output has no usage section:\n%s", out.String())
			}
		})
	}
}

// The root command with no arguments prints help and exits NON-zero: nothing was
// asked for, so the invocation was a mistake.
func TestCLI_rootWithNoArgs(t *testing.T) {
	p, out, _ := newTestCLI(t)
	code, err := p.Run(nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code == 0 {
		t.Error("bare invocation exited 0; it printed usage, so it should signal a mistake")
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("bare invocation printed no usage:\n%s", out.String())
	}
}

func TestCLI_generateDelegatesItsArguments(t *testing.T) {
	var gotSpec, gotConf string
	var gotWatch bool

	p, out, _ := newTestCLI(t)
	p.Bind("generate", codegen.GenerateFn(func(spec, conf string, watch bool, onGenerate func(string, error), _ func([]error)) error {
		gotSpec, gotConf, gotWatch = spec, conf, watch
		onGenerate("generated ok", nil)
		return nil
	}))

	code, err := p.Run([]string{"generate", "my.spec.yaml", "--config", "my.conf.yaml", "--watch"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if gotSpec != "my.spec.yaml" || gotConf != "my.conf.yaml" || !gotWatch {
		t.Errorf("generate got (%q, %q, watch=%v), want the parsed argv", gotSpec, gotConf, gotWatch)
	}
	// The progress callback's result reaches the user.
	if !strings.Contains(out.String(), "generated ok") {
		t.Errorf("the generate callback's result was not printed:\n%s", out.String())
	}
	// The paths are echoed so a failing run says which files it read.
	if !strings.Contains(out.String(), "my.spec.yaml") || !strings.Contains(out.String(), "my.conf.yaml") {
		t.Errorf("generate did not echo its inputs:\n%s", out.String())
	}
}

// A codegen failure is recorded and exits non-zero, rather than being swallowed.
func TestCLI_generateFailureIsReported(t *testing.T) {
	p, _, errb := newTestCLI(t)
	p.Bind("generate", codegen.GenerateFn(func(string, string, bool, func(string, error), func([]error)) error {
		return rotini.UsageError(errBadSpec)
	}))

	code, err := p.Run([]string{"generate", "broken.yaml"})
	if err == nil {
		t.Error("Run returned no error for a failed generate")
	}
	if code == 0 {
		t.Error("a failed generate exited 0")
	}
	if !strings.Contains(errb.String(), errBadSpec.Error()) {
		t.Errorf("the failure was not reported on stderr:\n%s", errb.String())
	}
}

func TestCLI_validateDelegatesFailMode(t *testing.T) {
	var gotSpec, gotConf, gotFail string
	p, out, _ := newTestCLI(t)
	p.Bind("validate", codegen.ValidateFn(
		func(spec, conf string, _ bool, failMode string, onValidate func(string, error), _ func([]error)) error {
			gotSpec, gotConf, gotFail = spec, conf, failMode
			onValidate("valid", nil)
			return nil
		}))

	if _, err := p.Run([]string{"validate", "a.yaml", "-c", "b.yaml", "--fail", "fast"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotSpec != "a.yaml" || gotConf != "b.yaml" || gotFail != "fast" {
		t.Errorf("validate got (%q, %q, fail=%q), want the parsed argv", gotSpec, gotConf, gotFail)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("the validate callback's result was not printed:\n%s", out.String())
	}
}

// Warnings are surfaced without failing the run — that is what makes them warnings.
func TestCLI_validateWarningsDoNotFail(t *testing.T) {
	p, out, errb := newTestCLI(t)
	p.Bind("validate", codegen.ValidateFn(
		func(_, _ string, _ bool, _ string, _ func(string, error), onWarnings func([]error)) error {
			onWarnings([]error{errAdvisory})
			return nil
		}))

	code, err := p.Run([]string{"validate", "a.yaml"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0 — a warning must not fail validation", code)
	}
	if combined := out.String() + errb.String(); !strings.Contains(combined, errAdvisory.Error()) {
		t.Errorf("the warning was not surfaced:\n%s", combined)
	}
}

func TestCLI_initializeDelegatesItsArguments(t *testing.T) {
	var gotName, gotFormat string
	var gotForce bool
	t.Chdir(t.TempDir()) // no go.mod requiring rotini: the `go get` step is due

	p, out, _ := newTestCLI(t)
	p.Bind("initialize", codegen.InitializeFn(func(name, format string, force bool) error {
		gotName, gotFormat, gotForce = name, format, force
		return nil
	}))

	if _, err := p.Run([]string{"init", "mycli", "--format", "json", "--force"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotName != "mycli" || gotFormat != "json" || !gotForce {
		t.Errorf("initialize got (%q, %q, force=%v), want the parsed argv", gotName, gotFormat, gotForce)
	}
	// Init tells the user what to do next; without it the following `go build` fails
	// on a missing module with no hint.
	for _, want := range []string{"initialized cmd/mycli", "go get github.com/go-rotini/rotini", "go build ./cmd/mycli"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init output missing %q:\n%s", want, out.String())
		}
	}
}

// The name argument is required: without it there is nothing to scaffold.
func TestCLI_initializeRequiresAName(t *testing.T) {
	p, _, errb := newTestCLI(t)
	p.Bind("initialize", codegen.InitializeFn(func(string, string, bool) error {
		t.Error("initialize ran without a name")
		return nil
	}))

	code, err := p.Run([]string{"init"})
	if err == nil {
		t.Error("Run returned no error for a missing required argument")
	}
	if code == 0 {
		t.Error("init with no name exited 0")
	}
	if !strings.Contains(errb.String(), "name") {
		t.Errorf("the error does not mention the missing name:\n%s", errb.String())
	}
}

// An unknown flag is a usage error, reported and non-zero — not a silent no-op.
func TestCLI_unknownFlagIsAUsageError(t *testing.T) {
	p, _, errb := newTestCLI(t)
	code, err := p.Run([]string{"generate", "--not-a-real-flag"})
	if err == nil {
		t.Error("Run returned no error for an unknown flag")
	}
	if code == 0 {
		t.Error("an unknown flag exited 0")
	}
	if !strings.Contains(errb.String(), "not-a-real-flag") {
		t.Errorf("the error does not name the offending flag:\n%s", errb.String())
	}
}

func TestCLI_helpCommandPrintsCommandHelp(t *testing.T) {
	p, out, _ := newTestCLI(t)
	if _, err := p.Run([]string{"help", "generate"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "generate") || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("`help generate` did not print generate's help:\n%s", out.String())
	}
}

// Aliases are part of the CLI's contract, so they are exercised, not assumed.
func TestCLI_aliases(t *testing.T) {
	for _, tc := range []struct{ alias, service string }{
		{"gen", "generate"},
		{"val", "validate"},
		{"init", "initialize"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			var ran bool
			p, _, _ := newTestCLI(t)
			p.Bind("generate", func(string, string, bool, func(string, error), func([]error)) error { ran = true; return nil })
			p.Bind("validate", func(string, string, bool, string, func(string, error), func([]error)) error { ran = true; return nil })
			p.Bind("initialize", func(string, string, bool) error { ran = true; return nil })

			argv := []string{tc.alias, "x"}
			if _, err := p.Run(argv); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !ran {
				t.Errorf("alias %q did not reach the %s handler", tc.alias, tc.service)
			}
		})
	}
}

// TestResolveVersion covers the version-resolution rules: a build-info RELEASE version wins
// over the -ldflags default (the `go install pkg@v1.2.3` path), a pseudo-version or "(devel)"
// does not count as one, and either way the leading "v" and any pre-release suffix are
// trimmed to a bare X.Y.Z.
func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name      string
		ldflags   string
		buildInfo string // "" means ReadBuildInfo reports nothing usable
		want      string
	}{
		{"ldflags only", "1.2.3", "", "1.2.3"},
		{"ldflags with a v prefix", "v1.2.3", "", "1.2.3"},
		{"build info wins over ldflags", "0.0.0", "v2.0.0", "2.0.0"},
		{"a devel build falls back to ldflags", "1.2.3", "(devel)", "1.2.3"},
		{"pre-release suffix is trimmed", "v1.2.3-rc1+meta", "", "1.2.3"},
		{"an unparseable version passes through", "not-a-version", "", "not-a-version"},

		// A PSEUDO-version is not a release. This is the case the e2e tier caught: a
		// release pipeline stamps -X main.version=1.4.2, the checkout is untagged so the
		// go tool records v0.0.0-<timestamp>-<hash>, and the old unanchored matcher read
		// its leading "v0.0.0" as a release — so the binary reported 0.0.0 and its own
		// version guard then rejected every correctly-versioned spec in the project.
		{"a pseudo-version is not a release", "1.4.2", "v0.0.0-20260901233311-3ef400c2a629", "1.4.2"},
		{"a pseudo-version off a tag is not a release", "1.4.2", "v1.3.0-0.20260901233311-3ef400c2a629", "1.4.2"},
		{"an empty build-info version falls back", "1.4.2", "", "1.4.2"},
		{"a real tag still wins", "0.0.0", "v1.9.4", "1.9.4"},
		{"a tagged pre-release wins and is trimmed", "0.0.0", "v2.0.0-rc.1", "2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := readBuildInfo
			t.Cleanup(func() { readBuildInfo = restore })
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				if tc.buildInfo == "" {
					return nil, false
				}
				return &debug.BuildInfo{Main: debug.Module{Version: tc.buildInfo}}, true
			}
			if got := ResolveVersion(tc.ldflags); got != tc.want {
				t.Errorf("ResolveVersion(%q) = %q, want %q", tc.ldflags, got, tc.want)
			}
		})
	}
}

// The root command's --version prints the bound version, the same as the version
// sub-command — two spellings of one question.
func TestCLI_rootVersionFlag(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		p, out, _ := newTestCLI(t)
		code, err := p.Run([]string{flag})
		if err != nil {
			t.Fatalf("Run(%s): %v", flag, err)
		}
		if code != 0 {
			t.Errorf("Run(%s) exit = %d, want 0", flag, code)
		}
		if got := strings.TrimSpace(out.String()); got != "v"+testVersion {
			t.Errorf("Run(%s) = %q, want %q", flag, got, "v"+testVersion)
		}
	}
}

// Styling is suppressed on request — by flag, by the program's own env var, and by
// CI — because a styled help page in a log file is noise, not emphasis.
func TestCLI_noStyles(t *testing.T) {
	styled := func(t *testing.T) string {
		t.Helper()
		p, out, _ := newTestCLI(t)
		if _, err := p.Run([]string{"--help"}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if !strings.Contains(styled(t), "\x1b") {
		t.Skip("the generated help carries no styling, so there is nothing to strip")
	}

	cases := []struct {
		name string
		argv []string
		env  map[string]string
	}{
		{name: "--no-styles flag", argv: []string{"--no-styles", "--help"}},
		{name: "ROTINI_NO_STYLES env", argv: []string{"--help"}, env: map[string]string{"ROTINI_NO_STYLES": "1"}},
		{name: "CI env", argv: []string{"--help"}, env: map[string]string{"CI": "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			p, out, _ := newTestCLI(t)
			if _, err := p.Run(tc.argv); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Errorf("%s did not strip styling from the help page", tc.name)
			}
		})
	}
}

// `help` with no argument is the same request as `--help` on the root.
func TestCLI_bareHelpCommand(t *testing.T) {
	p, out, _ := newTestCLI(t)
	if _, err := p.Run([]string{"help"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("`help` printed no usage:\n%s", out.String())
	}
}

// An unknown command name is reported rather than silently doing nothing.
func TestCLI_unknownHelpTopic(t *testing.T) {
	p, out, errb := newTestCLI(t)
	code, _ := p.Run([]string{"help", "no-such-command"})
	if code == 0 && !strings.Contains(out.String()+errb.String(), "no-such-command") {
		t.Errorf("an unknown help topic was neither reported nor non-zero:\nout=%s\nerr=%s", out.String(), errb.String())
	}
}

// ── test sentinels ──────────────────────────────────────────.

// Sentinels the CLI tests inject through the codegen doubles.
var (
	errBadSpec  = errors.New("spec is broken")
	errAdvisory = errors.New("an advisory warning")
)

// TestCLIPageMatchesGeneratedHelp keeps the docs site's CLI page byte-identical to the help
// the binary actually prints.
//
// That page is a transcript: every block is titled with the command that produced it. A
// transcript is only worth anything if it is true, and this one had already drifted twice —
// an alias separator changed from ";" to ", " and `validate --watch` stopped saying
// "re-generate" — with nothing to notice. Both are invisible in review and both teach a
// reader something false.
//
// The page is checked rather than generated because prose surrounds the blocks. What the test
// owns is the blocks; what a writer owns is everything between them.
func TestCLIPageMatchesGeneratedHelp(t *testing.T) {
	page := filepath.Join("..", "..", "..", "docs", "content", "cli", "_index.md")
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}

	// title="$ rotini <args>" … the block's body … {{< /code >}}
	blockRe := regexp.MustCompile(`(?s)\{\{< code title="\$ rotini([^"]*)"[^>]*>\}\}\n(.*?)\n\{\{< /code >\}\}`)
	matches := blockRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatalf("%s quotes no rotini help output — if that is deliberate, delete this test", page)
	}

	for _, m := range matches {
		args, quoted := strings.Fields(m[1]), m[2]
		// "$ rotini --help" is the root page; "$ rotini help <path...>" is that command's.
		path := args
		switch {
		case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
			path = nil
		case len(args) > 0 && args[0] == "help":
			path = args[1:]
		}
		want, err := Help(path...)
		if err != nil {
			t.Errorf("$ rotini%s: %v", m[1], err)
			continue
		}
		if quoted != want {
			t.Errorf("$ rotini%s: the CLI page is stale.\n--- page\n%s\n--- binary\n%s", m[1], quoted, want)
		}
	}
}

// TestRequiresRuntime decides whether `rotini init` tells the user to `go get` the runtime: only
// when the module governing the directory does not already require it. It once printed the step
// unconditionally, sending users with rotini in go.mod to run a command that changes nothing.
func TestRequiresRuntime(t *testing.T) {
	root := t.TempDir()
	write := func(dir, gomod string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	with := write(filepath.Join(root, "with"), "module example.com/a\n\ngo 1.26\n\nrequire (\n\tgithub.com/go-rotini/rotini v1.0.0 // a comment\n)\n")
	without := write(filepath.Join(root, "without"), "module example.com/b\n\ngo 1.26\n\nrequire github.com/go-rotini/recon v1.0.2\n")
	sub := filepath.Join(with, "cmd", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{with: true, sub: true, without: false, t.TempDir(): false} {
		if got := requiresRuntime(dir); got != want {
			t.Errorf("requiresRuntime(%s) = %v, want %v", dir, got, want)
		}
	}
}
