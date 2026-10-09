package rotini

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// These tests drive the companion cli through its generated Program, binding doubles for the
// codegen entry points (handlers use SetDependencyIfAbsent, so a test's double wins). Run is
// used instead of Execute so nothing exits, with a fresh Program per test. Run's error is the
// run's recorded failure, so on a failure path a non-nil error is expected.

const testVersion = "9.9.9"

// newTestCLI builds an isolated companion cli program with captured streams and a fixed
// version. The caller binds codegen doubles.
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

// Every command answers --help without doing any work, even beside a bad value or a missing
// argument.
func TestCLI_helpFlagOnEveryCommand(t *testing.T) {
	for _, argv := range [][]string{
		{"--help"},
		{"initialize", "--help"},
		{"generate", "--help"},
		{"validate", "--help"},
		{"version", "--help"},
		{"help", "--help"},
		{"initialize", "--format", "xml", "--help"},
		{"validate", "--fail", "slow", "--help"},
		{"completion", "--help"},
		{"completion", "tcsh", "--help"},
		{"man", "--help"},
		{"man", "--dir", "/nonexistent/must-not-be-created", "--help"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			p, out, _ := newTestCLI(t)
			// Doubles that fail the test prove --help short-circuits before codegen work.
			p.WithDependency(generateDep, codegen.GenerateFn(func(string, string, bool, func(string, error), func([]error)) error {
				t.Error("--help ran the generate work")
				return nil
			}))
			p.WithDependency(validateDep, codegen.ValidateFn(func(string, string, bool, string, string, func(string, error), func([]error)) error {
				t.Error("--help ran the validate work")
				return nil
			}))
			p.WithDependency(initializeDep, codegen.InitializeFn(func(string, string, bool) (codegen.Initialized, error) {
				t.Error("--help ran the initialize work")
				return codegen.Initialized{}, nil
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

// --help waives what a command requires, but not input that can't be read: an extra
// positional is still an error.
func TestCLI_helpKeepsUnreadableInput(t *testing.T) {
	p, _, errb := newTestCLI(t)
	if code, _ := p.Run([]string{"version", "extra", "--help"}); code == 0 {
		t.Error("version extra --help exited 0")
	}
	if !strings.Contains(errb.String(), "takes no arguments") {
		t.Errorf("stderr = %q, want the extra-argument error", errb.String())
	}
}

// A bare root invocation prints help on stderr and exits non-zero.
func TestCLI_rootWithNoArgs(t *testing.T) {
	p, out, errb := newTestCLI(t)
	code, err := p.Run(nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code == 0 {
		t.Error("bare invocation exited 0; it printed usage, so it should signal a mistake")
	}
	if !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("bare invocation printed no usage on stderr:\n%s", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("bare invocation wrote to stdout:\n%s", out.String())
	}
}

// failingWriter fails every write, as stdout does on a full disk.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A failed write to stdout fails the run instead of exiting 0 with nothing written.
func TestCLI_writeErrorsFailTheRun(t *testing.T) {
	for _, argv := range [][]string{
		{"version"},
		{"--version"},
		{"--help"},
		{"help"},
		{"completion", "bash"},
		{"man"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			errb := &bytes.Buffer{}
			p := NewProgram(Handlers()).
				WithStdout(failingWriter{}).
				WithStderr(errb).
				WithExit(func(int) { t.Error("Run must not exit the process") }).
				WithVersion(testVersion)
			code, _ := p.Run(argv)
			if code == 0 {
				t.Error("exited 0 although stdout could not be written")
			}
			if !strings.Contains(errb.String(), "write output: no space left on device") {
				t.Errorf("stderr = %q, want the write error", errb.String())
			}
		})
	}
}

func TestCLI_generateDelegatesItsArguments(t *testing.T) {
	var gotSpec, gotConf string
	var gotWatch bool

	p, out, _ := newTestCLI(t)
	p.WithDependency(generateDep, codegen.GenerateFn(func(spec, conf string, watch bool, onGenerate func(string, error), _ func([]error)) error {
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
	if !strings.Contains(out.String(), "generated ok") {
		t.Errorf("the generate callback's result was not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "my.spec.yaml") || !strings.Contains(out.String(), "my.conf.yaml") {
		t.Errorf("generate did not echo its inputs:\n%s", out.String())
	}
}

// A codegen failure is reported on stderr and exits non-zero.
func TestCLI_generateFailureIsReported(t *testing.T) {
	p, _, errb := newTestCLI(t)
	p.WithDependency(generateDep, codegen.GenerateFn(func(string, string, bool, func(string, error), func([]error)) error {
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
	p.WithDependency(validateDep, codegen.ValidateFn(
		func(spec, conf string, _ bool, failMode, _ string, onValidate func(string, error), _ func([]error)) error {
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

// Warnings are printed without failing the run.
func TestCLI_validateWarningsDoNotFail(t *testing.T) {
	p, out, errb := newTestCLI(t)
	p.WithDependency(validateDep, codegen.ValidateFn(
		func(_, _ string, _ bool, _, _ string, _ func(string, error), onWarnings func([]error)) error {
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
	t.Chdir(t.TempDir()) // no go.mod requiring rotini, so the runtime warning is due

	p, _, errb := newTestCLI(t)
	p.WithDependency(initializeDep, codegen.InitializeFn(func(name, format string, force bool) (codegen.Initialized, error) {
		gotName, gotFormat, gotForce = name, format, force
		return codegen.Initialized{}, nil
	}))

	code, err := p.Run([]string{"init", "mycli", "--format", "json", "--force"})
	if err != nil || code != 0 {
		t.Fatalf("Run: code %d, %v", code, err)
	}
	if gotName != "mycli" || gotFormat != "json" || !gotForce {
		t.Errorf("initialize got (%q, %q, force=%v), want the parsed argv", gotName, gotFormat, gotForce)
	}
	if want := "run `go get github.com/go-rotini/rotini` before building ./cmd/mycli"; !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want the runtime warning", errb.String())
	}
}

// TestCLI_initializeReportsWhatItWrote pins init's report (spec, conf, timing line) and the
// absence of a warning when go.mod already requires the runtime.
func TestCLI_initializeReportsWhatItWrote(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/a\n\nrequire github.com/go-rotini/rotini v1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	p, out, errb := newTestCLI(t)
	p.WithDependency(initializeDep, codegen.InitializeFn(func(string, string, bool) (codegen.Initialized, error) {
		return codegen.Initialized{
			Spec:   "cmd/mycli/.rotini.spec.yaml",
			Conf:   "cmd/mycli/.rotini.conf.yaml",
			Result: "[12:00:00] 5ms",
		}, nil
	}))
	if code, err := p.Run([]string{"init", "mycli"}); err != nil || code != 0 {
		t.Fatalf("Run: code %d, %v", code, err)
	}
	want := "spec: cmd/mycli/.rotini.spec.yaml\nconf: cmd/mycli/.rotini.conf.yaml\n[12:00:00] 5ms\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errb.String())
	}
}

// init requires the name argument.
func TestCLI_initializeRequiresAName(t *testing.T) {
	p, _, errb := newTestCLI(t)
	p.WithDependency(initializeDep, codegen.InitializeFn(func(string, string, bool) (codegen.Initialized, error) {
		t.Error("initialize ran without a name")
		return codegen.Initialized{}, nil
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

// An unknown flag is reported by name and exits non-zero.
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

// Each alias reaches its command's handler.
func TestCLI_aliases(t *testing.T) {
	for _, tc := range []struct{ alias, service string }{
		{"gen", "generate"},
		{"val", "validate"},
		{"init", "initialize"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			var ran bool
			p, _, _ := newTestCLI(t)
			p.WithDependency(generateDep, func(string, string, bool, func(string, error), func([]error)) error { ran = true; return nil })
			p.WithDependency(validateDep, func(string, string, bool, string, string, func(string, error), func([]error)) error {
				ran = true
				return nil
			})
			p.WithDependency(initializeDep, func(string, string, bool) (codegen.Initialized, error) { ran = true; return codegen.Initialized{}, nil })

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

// TestResolveVersion pins version resolution: a build-info release beats the -ldflags stamp,
// "(devel)" and pseudo-versions are not releases, and results are trimmed to X.Y.Z.
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

		// A pseudo-version is not a release, so the -ldflags stamp wins.
		{"a pseudo-version is not a release", "1.4.2", "v0.0.0-20260901233311-3ef400c2a629", "1.4.2"},
		{"a pseudo-version off a tag is not a release", "1.4.2", "v1.3.0-0.20260901233311-3ef400c2a629", "1.4.2"},
		{"an empty build-info version falls back", "1.4.2", "", "1.4.2"},
		{"a real tag still wins", "0.0.0", "v1.9.4", "1.9.4"},
		{"a tagged pre-release wins and is trimmed", "0.0.0", "v2.0.0-rc.1", "2.0.0"},

		// `make rotini-build` stamps `git describe --tags --always --dirty`; a build between
		// tags reports the last release.
		{"git describe on a tag", "v1.2.0", "v1.2.1-0.20261003120000-805e610abcde", "1.2.0"},
		{"git describe between tags", "v1.1.1-3-g805e610", "(devel)", "1.1.1"},
		{"git describe with uncommitted changes", "v1.1.1-3-g805e610-dirty", "", "1.1.1"},
		{"git describe with no tags is a bare hash", "805e610", "", "805e610"},
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

// The root's -v and --version print the bound version, like the version sub-command.
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

// `help` with no argument prints the root's help.
func TestCLI_bareHelpCommand(t *testing.T) {
	p, out, _ := newTestCLI(t)
	if _, err := p.Run([]string{"help"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("`help` printed no usage:\n%s", out.String())
	}
}

// An unknown help topic is reported or exits non-zero.
func TestCLI_unknownHelpTopic(t *testing.T) {
	p, out, errb := newTestCLI(t)
	code, _ := p.Run([]string{"help", "no-such-command"})
	if code == 0 && !strings.Contains(out.String()+errb.String(), "no-such-command") {
		t.Errorf("an unknown help topic was neither reported nor non-zero:\nout=%s\nerr=%s", out.String(), errb.String())
	}
}

// Sentinels the CLI tests inject through the codegen doubles.
var (
	errBadSpec  = errors.New("spec is broken")
	errAdvisory = errors.New("an advisory warning")
)

// TestCLIPageMatchesGeneratedHelp pins each `$ rotini …` code block on the docs site's CLI page
// to the help the binary prints. The page is checked rather than generated because prose
// surrounds the blocks.
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

// TestRequiresRuntime pins requiresRuntime against the nearest go.mod, including from a
// subdirectory and with no go.mod at all.
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

// Every command reports bad input the same way: one "Error:" line on stderr and a non-zero
// exit, with no help page dumped after it.
func TestCLI_inputErrorsAreReportedAlike(t *testing.T) {
	for _, argv := range [][]string{
		{"nope"},
		{"--nope"},
		{"version", "extra"},
		{"init", "a", "b"},
		{"validate", "--nope"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			p, out, errb := newTestCLI(t)
			code, _ := p.Run(argv)
			if code == 0 {
				t.Error("exited 0")
			}
			if !strings.HasPrefix(errb.String(), "Error: ") {
				t.Errorf("stderr = %q, want an Error: line", errb.String())
			}
			if out.Len() != 0 {
				t.Errorf("printed to stdout on an input error:\n%s", out.String())
			}
		})
	}
}

// With no conf given or found, the banner says the conf defaults apply.
func TestCLI_bannerNamesTheFilesRead(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(".rotini.spec.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, out, _ := newTestCLI(t)
	var gotSpec, gotConf string
	p.WithDependency(validateDep, codegen.ValidateFn(func(spec, conf string, _ bool, _, _ string, _ func(string, error), _ func([]error)) error {
		gotSpec, gotConf = spec, conf
		return nil
	}))
	if _, err := p.Run([]string{"validate"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotSpec != ".rotini.spec.json" || gotConf != "" {
		t.Errorf("validate got (%q, %q), want the discovered spec and no conf", gotSpec, gotConf)
	}
	if !strings.Contains(out.String(), "conf: none (defaults)") {
		t.Errorf("banner does not say the conf defaults apply:\n%s", out.String())
	}
}

// A near-miss command, flag, value or help topic names the nearest accepted spelling; a word
// near nothing gets no guess.
func TestCLI_suggestsNearestSpelling(t *testing.T) {
	cases := []struct {
		argv []string
		want string // "" means no hint
	}{
		{argv: []string{"genrate"}, want: `unknown command "genrate" for "rotini"; did you mean "generate"?`},
		{argv: []string{"hlep"}, want: `did you mean "help"?`},
		{argv: []string{"validate", "--fial", "fast"}, want: `unknown flag "--fial"; did you mean "--fail"?`},
		{argv: []string{"init", "x", "--format", "jsn"}, want: `did you mean "json"?`},
		{argv: []string{"help", "genrate"}, want: `no help for command "genrate"; did you mean "generate"?`},
		{argv: []string{"kubernetes"}},
		{argv: []string{"init", "x", "--bogus"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			p, _, errb := newTestCLI(t)
			code, _ := p.Run(tc.argv)
			if code == 0 {
				t.Fatalf("Run(%q) exit = 0, want non-zero", tc.argv)
			}
			got := errb.String()
			if tc.want == "" {
				if strings.Contains(got, "did you mean") {
					t.Errorf("Run(%q) guessed at a word near nothing:\n%s", tc.argv, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("Run(%q) stderr = %q, want it to contain %q", tc.argv, got, tc.want)
			}
		})
	}
}

// `rotini completion <shell>` prints each shell's script and rejects an unknown shell as a usage
// error.
func TestCLI_completion(t *testing.T) {
	for shell, want := range map[string]string{
		"bash":       "-F _rotini_complete rotini",
		"zsh":        "#compdef rotini",
		"fish":       "complete -c rotini",
		"powershell": "Register-ArgumentCompleter",
	} {
		p, out, _ := newTestCLI(t)
		if code, err := p.Run([]string{"completion", shell}); code != 0 || err != nil {
			t.Fatalf("completion %s = (%d, %v)", shell, code, err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("completion %s lacks %q:\n%s", shell, want, out.String())
		}
	}

	p, _, errb := newTestCLI(t)
	if code, _ := p.Run([]string{"completion", "tcsh"}); code != 1 || !strings.Contains(errb.String(), `"tcsh"`) {
		t.Errorf("completion tcsh = exit %d, stderr %q; want a usage error naming the shell", code, errb.String())
	}
}

// `rotini man [command...]` prints one roff page; with no command it is rotini's own.
func TestCLI_man(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"man"}, `.TH "ROTINI" 1 `},
		{[]string{"man", "generate"}, `.TH "ROTINI\-GENERATE" 1 `},
		{[]string{"man", "gen"}, `.TH "ROTINI\-GENERATE" 1 `}, // an alias names the same page
	} {
		p, out, _ := newTestCLI(t)
		if code, err := p.Run(tc.argv); code != 0 || err != nil {
			t.Fatalf("%q = (%d, %v)", tc.argv, code, err)
		}
		if !strings.HasPrefix(out.String(), tc.want) {
			t.Errorf("%q printed %q…, want a page starting %q", tc.argv, firstLine(out.String()), tc.want)
		}
	}

	p, _, errb := newTestCLI(t)
	if code, _ := p.Run([]string{"man", "genrate"}); code != 1 || !strings.Contains(errb.String(), `did you mean "generate"`) {
		t.Errorf("man genrate = exit %d, stderr %q; want a usage error with a suggestion", code, errb.String())
	}
}

// `rotini man --dir <path>` creates the directory and writes every page as <name>.<section>.
func TestCLI_manDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "man", "man1")
	p, _, errb := newTestCLI(t)
	if code, err := p.Run([]string{"man", "--dir", dir}); code != 0 || err != nil {
		t.Fatalf("man --dir = (%d, %v)", code, err)
	}
	if !strings.Contains(errb.String(), "wrote 8 man pages to "+dir) {
		t.Errorf("stderr = %q, want it to say what it wrote", errb.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"rotini-completion.1", "rotini-generate.1", "rotini-help.1", "rotini-initialize.1",
		"rotini-man.1", "rotini-validate.1", "rotini-version.1", "rotini.1"}
	if !slices.Equal(names, want) {
		t.Errorf("wrote %q, want %q", names, want)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "rotini-man.1"))
	if page, _ := Man("man"); string(body) != page {
		t.Error("the file written for rotini man differs from what `rotini man man` prints")
	}

	p, _, errb = newTestCLI(t)
	if code, _ := p.Run([]string{"man", "generate", "--dir", dir}); code == 0 || !strings.Contains(errb.String(), "--dir writes every page") {
		t.Errorf("man generate --dir = exit %d, stderr %q; want a usage error", code, errb.String())
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
