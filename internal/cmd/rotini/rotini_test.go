package rotini

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-rotini/rotini"
)

// svc is a registry binding a test injects in addition to the always-present parser — used
// to substitute a double for a handler's work dependency (e.g. internal.Generate) at the
// DI seam, so a handler's branches are exercised without running the real codegen.
type svc struct {
	key string
	val any
}

// testVersion is the VersionSemantic stamped into the *rotini.Build the harness binds under
// "build"; the version paths print it (as "v"+VersionSemantic), so this sentinel proves the
// bound value flows through.
const testVersion = "9.9.9"

// runRotini drives the companion through the real program lifecycle exactly as main.go
// would — capturing stdout/stderr and recording the exit code — so a handler's every path
// is exercised end-to-end. "parser" and "build" are the services main.go binds, so both
// are always bound here too (we are testing handler logic, not those deps). The version paths
// print build.Version; the work handlers feed build.VersionSemantic to
// internal.{Generate,Validate,Initialize} for the $schema segment, a path these handler tests
// don't reach (their work dependency is doubled or errors out early). A handler's own work
// dependency (generate/validate/initialize) is self-bound via BindIfAbsent, so leaving it
// unbound here exercises that real production wiring. Extra binds inject doubles for that work
// dependency. WithContext opts out of the default signal trap, which these tests don't exercise.
func runRotini(t *testing.T, argv []string, binds ...svc) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	p := NewProgram(Handlers()).
		WithContext(context.Background()).
		WithArgs(argv).
		WithStdout(&out).
		WithStderr(&errb).
		WithExit(func(c int) { code = c }).
		Bind(rotini.KeyParser, rotini.NewParser()).
		Bind(rotini.KeySuggestor, rotini.NewSuggestor()).
		Bind(rotini.KeyVersioner, &rotini.Versioner{VersionSemantic: testVersion})
	for _, b := range binds {
		p.Bind(b.key, b.val)
	}
	p.Execute()
	return out.String(), errb.String(), code
}

// check asserts a run's outcome. An empty wantOut/wantErr means "expect nothing on that
// stream" (exact-empty); a non-empty value is matched as a substring, so tests can assert
// against a generated help/script constant without pinning its whole text.
func check(t *testing.T, gotOut, gotErr string, gotCode int, wantOut, wantErr string, wantCode int) {
	t.Helper()
	switch {
	case wantOut == "" && gotOut != "":
		t.Errorf("stdout = %q, want empty", gotOut)
	case wantOut != "" && !bytes.Contains([]byte(gotOut), []byte(wantOut)):
		t.Errorf("stdout = %q, want to contain %q", gotOut, wantOut)
	}
	switch {
	case wantErr == "" && gotErr != "":
		t.Errorf("stderr = %q, want empty", gotErr)
	case wantErr != "" && !bytes.Contains([]byte(gotErr), []byte(wantErr)):
		t.Errorf("stderr = %q, want to contain %q", gotErr, wantErr)
	}
	if gotCode != wantCode {
		t.Errorf("exit code = %d, want %d", gotCode, wantCode)
	}
}

// TestRotini covers the root command handler (rotini.go): the --help and --version flags,
// the no-flags default (help + exit 1), and parse errors. The handler now records the
// parse error and stops; rotini's default OnError prints "rotini: <err>" to stderr and
// exits by category (no help dump, and — per the opt-in rules — no "did you mean").
func TestRotini(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		wantOut, wantErr string
		wantCode         int
	}{
		{"help flag", []string{"--help"}, HelpRotini, "", 0},
		{"version flag", []string{"--version"}, testVersion, "", 0},
		{"no args prints help and fails", []string{}, HelpRotini, "", 1},
		{"parse error on unknown flag", []string{"--nope"}, "", `rotini: unknown flag "--nope"`, rotini.ExitUsage},
		{"mistyped command, no auto-suggestion", []string{"generte"}, "", `rotini: unknown command "generte"`, rotini.ExitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}

// TestRotini_neverSuggests pins the opt-in rule: rotini's own CLI never emits a
// "did you mean" — suggestions are the end-user's OnError to add, against the
// bound Suggestor. A mistyped command surfaces the plain unknown-command error.
func TestRotini_neverSuggests(t *testing.T) {
	out, errb, _ := runRotini(t, []string{"generte"})
	for _, s := range []string{out, errb} {
		if bytes.Contains([]byte(s), []byte("Did you mean")) || bytes.Contains([]byte(s), []byte("generate")) {
			t.Errorf("output suggested a correction (must not): out=%q err=%q", out, errb)
		}
	}
}
